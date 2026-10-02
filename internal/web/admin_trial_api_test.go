package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Trial editor HTTP contract (/admin/api/trial*): session, CSRF, decoding,
// error codes and idempotent replay through the handler installed by Start.
// Repository rules are covered by internal/database/trial_settings_test.go.

type trialAPIFixture struct {
	*adminAPIFixture
	source  *database.ProviderSource
	builder *database.SubscriptionBuilder
}

func newTrialAPIFixture(t *testing.T, withTrial bool) *trialAPIFixture {
	t.Helper()
	base := newAdminAPIFixtureNoServer(t)
	ctx := context.Background()
	src := &database.ProviderSource{Name: "trial-api-source", Type: "external", SubscriptionURL: "https://provider.example/sub/trial", Headers: `{}`, Enabled: true}
	require.NoError(t, base.db.CreateProviderSource(ctx, src))
	_, err := base.db.SyncSourceEntries(ctx, src.ID, []database.ProviderSourceEntry{
		{Fingerprint: "de-1", OriginalName: "🇩🇪 Berlin", CountryCode: "DE", Protocol: "vless"},
		{Fingerprint: "nl-1", OriginalName: "🇳🇱 Amsterdam", CountryCode: "NL", Protocol: "vless"},
	}, database.SourceSyncOK, "")
	require.NoError(t, err)
	builder := &database.SubscriptionBuilder{Name: "trial-api-builder", Enabled: true, Version: 1}
	require.NoError(t, base.db.GetDB().Create(builder).Error)
	require.NoError(t, base.db.SetBuilderSources(ctx, builder.ID, []uint{src.ID}))
	node := database.Node{Name: "trial-node", IsActive: true, Host: "trial.example", APIToken: "token", Type: database.NodeType3xUI, InboundIDs: "[1]"}
	require.NoError(t, base.db.CreateNode(ctx, &node))
	require.NoError(t, base.db.LinkNodeToPlan(ctx, database.TrialPlanName, node.ID))

	hash, err := bcrypt.GenerateFromPassword([]byte(adminTestPassword), config.AdminMinBcryptCost)
	require.NoError(t, err)
	cfg := &config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: "https://admin.example.com"}
	s := NewServer("127.0.0.1:0", nil, cfg, "", nil, nil)
	s.SetAdminService(service.NewAdminService(base.db, nil, nil))
	if withTrial {
		s.SetTrialService(service.NewTrialService(base.db, database.TrialDefaults{DurationHours: 3, RateLimitPerHour: 3}))
	}
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(stopCtx))
	})
	base.attach(s)
	return &trialAPIFixture{adminAPIFixture: base, source: src, builder: builder}
}

type trialOutcomeWire struct {
	Trial    database.TrialAdminView `json:"trial"`
	TargetID uint                    `json:"target_id"`
	Replayed bool                    `json:"replayed"`
	Audit    struct {
		ID     uint   `json:"id"`
		Action string `json:"action"`
	} `json:"audit"`
}

// draft is a PUT/preview body; composition is raw JSON ("null" to keep).
func (f *trialAPIFixture) draft(key string, version int, enabled bool, hours int, builder string, composition string) string {
	head := ""
	if key != "" {
		head = fmt.Sprintf(`"request_key":%q,"version":%d,`, key, version)
	}
	return fmt.Sprintf(`{%s"enabled":%t,"duration_hours":%d,"rate_limit_per_hour":5,"title":"Пробный доступ",`+
		`"description":"Три дня","features":["Без карты"],"badge":"Бесплатно","builder_id":%s,"composition":%s}`,
		head, enabled, hours, builder, composition)
}

func (f *trialAPIFixture) selectedNL() string {
	return fmt.Sprintf(`{"builder_version":%d,"mode":"selected","source_ids":[%d],"rules":[{"kind":"country","source_id":%d,"country_code":"NL"}]}`,
		f.builder.Version, f.source.ID, f.source.ID)
}

func TestAdminTrialAPI_RequiresSessionAndCSRF(t *testing.T) {
	f := newTrialAPIFixture(t, true)
	f.expectError(f.doAs(f.anon, http.MethodGet, "/admin/api/trial"), http.StatusUnauthorized, "unauthorized")
	f.login()
	before := f.auditCount()
	resp := f.do(http.MethodPut, "/admin/api/trial", withJSON(f.draft("no-csrf", 0, true, 6, "null", "null")))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, before, f.auditCount())
}

func TestAdminTrialAPI_ServiceUnavailableWithoutTrialService(t *testing.T) {
	f := newTrialAPIFixture(t, false)
	f.login()
	f.expectError(f.do(http.MethodGet, "/admin/api/trial"), http.StatusServiceUnavailable, "service_unavailable")
}

func TestAdminTrialAPI_ViewSaveReplayAndPreview(t *testing.T) {
	f := newTrialAPIFixture(t, true)
	f.login()

	resp := f.do(http.MethodGet, "/admin/api/trial")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var view database.TrialAdminView
	f.decode(resp, &view)
	assert.True(t, view.Settings.Enabled)
	assert.Equal(t, 3, view.Settings.DurationHours, "environment defaults while nothing is stored")
	assert.False(t, view.Settings.Stored)
	assert.Nil(t, view.BuilderID)
	assert.Equal(t, database.TrialServeLegacy, view.Preview.Serve)
	require.Len(t, view.Builders, 1)

	// Preview writes nothing.
	before := f.auditCount()
	builderID := fmt.Sprint(f.builder.ID)
	resp = f.do(http.MethodPost, "/admin/api/trial/preview", f.withCSRF(), withJSON(f.draft("", 0, true, 6, builderID, f.selectedNL())))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var preview database.TrialPreview
	f.decode(resp, &preview)
	assert.True(t, preview.Issuable, "%+v", preview.Problems)
	assert.Equal(t, 1, preview.Total)
	assert.Equal(t, "🇳🇱 Amsterdam", preview.Items[0].DisplayName)
	assert.Equal(t, before, f.auditCount())

	body := f.draft("trial-save-1", 0, true, 6, builderID, f.selectedNL())
	resp = f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(body))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out trialOutcomeWire
	f.decode(resp, &out)
	assert.Equal(t, string(database.AdminActionTrialUpdated), out.Audit.Action)
	assert.Equal(t, 6, out.Trial.Settings.DurationHours)
	assert.Equal(t, 1, out.Trial.Settings.Version)
	require.NotNil(t, out.Trial.Composition)
	assert.Equal(t, database.TrialModeSelected, out.Trial.Composition.Mode)
	assert.Equal(t, "Пробный доступ", out.Trial.Settings.Title)

	// Same key, same payload: replay, no second audit row.
	count := f.auditCount()
	resp = f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(body))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var replay trialOutcomeWire
	f.decode(resp, &replay)
	assert.True(t, replay.Replayed)
	assert.Equal(t, count, f.auditCount())

	// A stale settings version is a conflict.
	f.expectError(f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(),
		withJSON(f.draft("trial-save-2", 0, true, 12, builderID, "null"))), http.StatusConflict, "version_conflict")
}

func TestAdminTrialAPI_Rejections(t *testing.T) {
	f := newTrialAPIFixture(t, true)
	f.login()
	builderID := fmt.Sprint(f.builder.ID)

	resp := f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(f.draft("bad-hours", 0, true, 0, "null", "null")))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	payload := f.raw(resp)
	assert.Equal(t, "invalid_trial", payload["error"])
	assert.Equal(t, "duration_hours", payload["field"])

	resp = f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(f.draft("bad-builder", 0, true, 6, "987654", "null")))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "builder_not_found", f.raw(resp)["reason"])

	// A builder used by a paid plan: its composition cannot change from here.
	paid := &database.Plan{Name: "trial-api-paid", IsActive: true, SubscriptionBuilderID: &f.builder.ID}
	require.NoError(t, f.db.GetDB().Create(paid).Error)
	resp = f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(f.draft("shared", 0, true, 6, builderID, f.selectedNL())))
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	payload = f.raw(resp)
	assert.Equal(t, "builder_shared", payload["error"])

	// Unknown fields are rejected, never ignored.
	bad := strings.Replace(f.draft("typo", 0, true, 6, "null", "null"), `"badge"`, `"bagde"`, 1)
	resp = f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(bad))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// Copying gives the trial its own builder; the paid plan keeps the original.
	resp = f.do(http.MethodPost, "/admin/api/trial/builder-copy", f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"copy-1","builder_id":%d}`, f.builder.ID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out trialOutcomeWire
	f.decode(resp, &out)
	require.NotNil(t, out.Trial.BuilderID)
	assert.Equal(t, out.TargetID, *out.Trial.BuilderID)
	assert.NotEqual(t, f.builder.ID, out.TargetID)
	var reloaded database.Plan
	require.NoError(t, f.db.GetDB().First(&reloaded, paid.ID).Error)
	assert.Equal(t, f.builder.ID, *reloaded.SubscriptionBuilderID)

	f.expectError(f.do(http.MethodDelete, "/admin/api/trial", f.withCSRF()), http.StatusMethodNotAllowed, "method_not_allowed")
	f.expectError(f.do(http.MethodGet, "/admin/api/trial/unknown"), http.StatusNotFound, "not_found")
}

func TestAdminTrialAPI_ConflictsAndCopyReplay(t *testing.T) {
	f := newTrialAPIFixture(t, true)
	f.login()
	builderID := fmt.Sprint(f.builder.ID)

	// The same request key with another payload is a conflict, not a save.
	resp := f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(f.draft("key-1", 0, true, 6, "null", "null")))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	audits := f.auditCount()
	f.expectError(f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(f.draft("key-1", 0, true, 12, "null", "null"))),
		http.StatusConflict, "request_key_conflict")
	assert.Equal(t, audits, f.auditCount())

	// Rules edited elsewhere meanwhile: the stale builder version is refused.
	stale := fmt.Sprintf(`{"builder_version":%d,"mode":"selected","source_ids":[%d],"rules":[{"kind":"country","source_id":%d,"country_code":"NL"}]}`,
		f.builder.Version+7, f.source.ID, f.source.ID)
	f.expectError(f.do(http.MethodPut, "/admin/api/trial", f.withCSRF(), withJSON(f.draft("key-2", 1, true, 6, builderID, stale))),
		http.StatusConflict, "builder_version_conflict")
	reloaded, err := f.db.GetBuilder(context.Background(), f.builder.ID)
	require.NoError(t, err)
	assert.Equal(t, f.builder.Version, reloaded.Version)
	assert.Empty(t, reloaded.Items)
	assert.Equal(t, audits, f.auditCount())

	// builder-copy and preview decode strictly.
	f.expectError(f.do(http.MethodPost, "/admin/api/trial/builder-copy", f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"copy-x","builder_id":%d,"name":"x"}`, f.builder.ID))), http.StatusBadRequest, "invalid_request")
	f.expectError(f.do(http.MethodPost, "/admin/api/trial/builder-copy", f.withCSRF(),
		withJSON(`{"request_key":"copy-0","builder_id":0}`)), http.StatusBadRequest, "invalid_request")
	resp = f.do(http.MethodPost, "/admin/api/trial/preview", f.withCSRF(),
		withJSON(strings.Replace(f.draft("", 0, true, 6, "null", "null"), `"badge"`, `"bagde"`, 1)))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
	f.expectError(f.do(http.MethodGet, "/admin/api/trial/preview"), http.StatusMethodNotAllowed, "method_not_allowed")

	// A lost copy response retried with the same key creates no second builder.
	body := withJSON(fmt.Sprintf(`{"request_key":"copy-1","builder_id":%d}`, f.builder.ID))
	resp = f.do(http.MethodPost, "/admin/api/trial/builder-copy", f.withCSRF(), body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var first trialOutcomeWire
	f.decode(resp, &first)
	assert.False(t, first.Replayed)
	var builders int64
	require.NoError(t, f.db.GetDB().Model(&database.SubscriptionBuilder{}).Count(&builders).Error)
	resp = f.do(http.MethodPost, "/admin/api/trial/builder-copy", f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"copy-1","builder_id":%d}`, f.builder.ID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var again trialOutcomeWire
	f.decode(resp, &again)
	assert.True(t, again.Replayed)
	assert.Equal(t, first.TargetID, again.TargetID)
	assert.Equal(t, first.Audit.ID, again.Audit.ID)
	var buildersAfter int64
	require.NoError(t, f.db.GetDB().Model(&database.SubscriptionBuilder{}).Count(&buildersAfter).Error)
	assert.Equal(t, builders, buildersAfter)

	// The view after the copy: the copy is the trial builder, its own rules.
	resp = f.do(http.MethodGet, "/admin/api/trial")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var view database.TrialAdminView
	f.decode(resp, &view)
	require.NotNil(t, view.BuilderID)
	assert.Equal(t, first.TargetID, *view.BuilderID)
	assert.Equal(t, 1, view.Settings.Version)
}
