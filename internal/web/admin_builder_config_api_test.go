package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Full configuration path over HTTP with the real SQLite repository and the
// real catalogue sync: create → two synced sources → country and node rules
// saved atomically → reopen (list + get) → unchanged re-save → preview, then
// a rule of another builder and a stale second tab, which must change nothing.

type configItemWire struct {
	ID           uint    `json:"id"`
	Kind         string  `json:"kind"`
	SourceID     uint    `json:"source_id"`
	CountryCode  string  `json:"country_code"`
	Fingerprint  string  `json:"fingerprint"`
	OriginalName string  `json:"original_name"`
	CustomName   *string `json:"custom_name"`
	Description  string  `json:"description"`
	Enabled      bool    `json:"enabled"`
}

type configWire struct {
	RequestKey   string           `json:"request_key"`
	Version      int              `json:"version"`
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Enabled      bool             `json:"enabled"`
	ProfileTitle string           `json:"profile_title"`
	SupportURL   string           `json:"support_url"`
	Announce     string           `json:"announce"`
	SourceIDs    []uint           `json:"source_ids"`
	Items        []configItemWire `json:"items"`
}

// configOf is what the editor sends for an unchanged, reopened builder.
func configOf(v service.BuilderView, key string) configWire {
	c := configWire{RequestKey: key, Version: v.Version, Name: v.Name, Description: v.Description, Enabled: v.Enabled,
		ProfileTitle: v.ProfileTitle, SupportURL: v.SupportURL, Announce: v.Announce, SourceIDs: []uint{}, Items: []configItemWire{}}
	for _, s := range v.Sources {
		c.SourceIDs = append(c.SourceIDs, s.SourceID)
	}
	for _, it := range v.Items {
		c.Items = append(c.Items, configItemWire{ID: it.ID, Kind: it.Kind, SourceID: it.SourceID, CountryCode: it.CountryCode,
			Fingerprint: it.Fingerprint, OriginalName: it.OriginalName, CustomName: it.CustomName, Description: it.Description, Enabled: it.Enabled})
	}
	return c
}

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestAdminBuilderAPI_ConfigurationFullPathOwnershipAndConflicts(t *testing.T) {
	f := newSourceAPIFixture(t)
	f.login()

	createSource := func(name string) service.SourceView {
		resp := f.do(http.MethodPost, "/admin/api/sources", f.withCSRF(), withJSON(f.createBody(name, "")))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created struct {
			Source service.SourceView `json:"source"`
		}
		require.NoError(t, jsonUnmarshal(f.text(resp), &created))
		return created.Source
	}
	keys := 0
	createBuilder := func(name string) service.BuilderView {
		keys++
		resp := f.do(http.MethodPost, "/admin/api/builders", f.withCSRF(), withJSON(fmt.Sprintf(
			`{"request_key":"create-%d","name":%q,"description":"","enabled":true,"profile_title":"","support_url":"","announce":""}`, keys, name)))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var out struct {
			Builder service.BuilderView `json:"builder"`
		}
		require.NoError(t, jsonUnmarshal(f.text(resp), &out))
		return out.Builder
	}
	get := func(id uint) service.BuilderView {
		t.Helper()
		var out service.BuilderView
		require.NoError(t, jsonUnmarshal(f.text(f.do(http.MethodGet, builderPath(id, ""))), &out))
		return out
	}
	listed := func(id uint) service.BuilderView {
		t.Helper()
		var out struct {
			Builders []service.BuilderView `json:"builders"`
		}
		require.NoError(t, jsonUnmarshal(f.text(f.do(http.MethodGet, "/admin/api/builders")), &out))
		for _, b := range out.Builders {
			if b.ID == id {
				return b
			}
		}
		t.Fatalf("builder %d not listed", id)
		return service.BuilderView{}
	}
	save := func(id uint, c configWire, opts ...adminRequestOption) *http.Response {
		return f.do(http.MethodPut, builderPath(id, "/configuration"), append([]adminRequestOption{withJSON(jsonBody(t, c))}, opts...)...)
	}
	expectNotFound := func(resp *http.Response, label string) {
		t.Helper()
		require.Equal(t, http.StatusNotFound, resp.StatusCode, label)
		assert.Equal(t, map[string]any{"error": "not_found"}, f.raw(resp), label+": nothing about other builders is revealed")
	}

	// 1–3. Builder, two sources synced from the (sanitized) provider fixture.
	srcA, srcB := createSource("Provider A"), createSource("Provider B")
	b := createBuilder("FINLAN")
	require.GreaterOrEqual(t, len(srcA.Catalogue.ByCountry), 2)
	c1, c2 := srcA.Catalogue.ByCountry[0], srcA.Catalogue.ByCountry[1]
	var entries struct {
		Entries []database.ProviderSourceEntry `json:"entries"`
	}
	require.NoError(t, jsonUnmarshal(f.text(f.do(http.MethodGet, sourcePath(srcB.ID, "/entries"))), &entries))
	node := entries.Entries[0]

	// 4–6. Countries of A and one server of B, saved in one request.
	cfg := configOf(b, "cfg-1")
	cfg.Description = "Финляндия"
	cfg.SourceIDs = []uint{srcA.ID, srcB.ID}
	cfg.Items = []configItemWire{
		{Kind: "country", SourceID: srcA.ID, CountryCode: c1.Code, Enabled: true},
		{Kind: "country", SourceID: srcA.ID, CountryCode: c2.Code, Enabled: true},
		{Kind: "node", SourceID: srcB.ID, Fingerprint: node.Fingerprint, OriginalName: node.OriginalName, Enabled: true},
	}
	// The editor must send CSRF like every unsafe request.
	resp := save(b.ID, cfg)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_ = resp.Body.Close()
	resp = save(b.ID, cfg, f.withCSRF())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var savedResp struct {
		Builder service.BuilderView     `json:"builder"`
		Audit   *database.AdminAuditLog `json:"audit"`
	}
	require.NoError(t, jsonUnmarshal(f.text(resp), &savedResp))
	assert.Equal(t, "builder_configured", savedResp.Audit.Action)
	saved := savedResp.Builder
	assert.Equal(t, 2, saved.Version)
	require.Len(t, saved.Items, 3)

	// 7–8. Reopen and full reload read the same stored configuration.
	reopened, fromList := get(b.ID), listed(b.ID)
	assert.Equal(t, saved.Sources, reopened.Sources)
	assert.Equal(t, saved.Items, reopened.Items)
	assert.Equal(t, saved.Items, fromList.Items)
	assert.Equal(t, saved.Version, fromList.Version)

	// 9. Unchanged re-save: same rules (ids), only the version moves.
	resp = save(b.ID, configOf(reopened, "cfg-2"), f.withCSRF())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.text(resp)
	resaved := get(b.ID)
	assert.Equal(t, reopened.Version+1, resaved.Version)
	assert.Equal(t, reopened.Items, resaved.Items)
	assert.Equal(t, reopened.Sources, resaved.Sources)

	// 10. Preview resolves exactly those countries plus the server.
	resp = f.do(http.MethodPost, builderPath(b.ID, "/preview"), f.withCSRF())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var preview database.PreviewBuilderResult
	require.NoError(t, jsonUnmarshal(f.text(resp), &preview))
	assert.Equal(t, c1.Count+c2.Count+1, preview.Total)
	assert.Zero(t, preview.Missing)

	// 11. A rule of another builder: every rule endpoint refuses it.
	other := createBuilder("Другой")
	otherCfg := configOf(other, "cfg-other")
	otherCfg.SourceIDs = []uint{srcB.ID}
	otherCfg.Items = []configItemWire{{Kind: "country", SourceID: srcB.ID, CountryCode: c1.Code, Enabled: true}}
	resp = save(other.ID, otherCfg, f.withCSRF())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.text(resp)
	otherBefore := get(other.ID)
	foreignID := otherBefore.Items[0].ID
	mine := get(b.ID)

	expectNotFound(f.do(http.MethodDelete, builderPath(b.ID, fmt.Sprintf("/items/%d", foreignID)), f.withCSRF()), "delete")
	expectNotFound(f.do(http.MethodPost, builderPath(b.ID, "/items"), f.withCSRF(), withJSON(fmt.Sprintf(
		`{"id":%d,"kind":"country","source_id":%d,"country_code":"FI","position":0,"enabled":false}`, foreignID, srcA.ID))), "upsert")
	expectNotFound(f.do(http.MethodPost, builderPath(b.ID, "/reorder"), f.withCSRF(), withJSON(fmt.Sprintf(
		`{"item_ids":[%d,%d]}`, foreignID, mine.Items[0].ID))), "reorder")
	steal := configOf(mine, "cfg-steal")
	steal.Items = append(steal.Items, configItemWire{ID: foreignID, Kind: "country", SourceID: srcA.ID, CountryCode: "FI", Enabled: false})
	expectNotFound(save(b.ID, steal, f.withCSRF()), "configuration")
	assert.Equal(t, otherBefore, get(other.ID), "the other builder is untouched")
	assert.Equal(t, mine, get(b.ID), "the builder of the request is untouched")

	// 12. A second tab that loaded version N saves after the first tab: 409,
	// nothing written, the first tab's configuration stays.
	stale := configOf(mine, "cfg-tab2")
	stale.SourceIDs = []uint{srcB.ID}
	stale.Items = []configItemWire{}
	first := configOf(mine, "cfg-tab1")
	first.Items = first.Items[:2]
	resp = save(b.ID, first, f.withCSRF())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.text(resp)
	afterFirst := get(b.ID)
	f.expectError(save(b.ID, stale, f.withCSRF()), http.StatusConflict, "version_conflict")
	assert.Equal(t, afterFirst, get(b.ID))
	assert.Len(t, afterFirst.Items, 2)
	assert.Len(t, afterFirst.Sources, 2)

	// Validation answers name the field; omitted lists are rejected, never
	// read as "clear everything".
	resp = f.do(http.MethodPut, builderPath(b.ID, "/configuration"), f.withCSRF(), withJSON(fmt.Sprintf(
		`{"request_key":"cfg-x","version":%d,"name":"FINLAN","description":"","enabled":true,"profile_title":"","support_url":"","announce":"","source_ids":[%d]}`,
		afterFirst.Version, srcA.ID)))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, map[string]any{"error": "invalid_request", "field": "items", "reason": "required"}, f.raw(resp))
	bad := configOf(afterFirst, "cfg-y")
	bad.SourceIDs = append(bad.SourceIDs, 999999)
	resp = save(b.ID, bad, f.withCSRF())
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, map[string]any{"error": "invalid_request", "field": "source_ids", "reason": "source_not_found"}, f.raw(resp))
	f.expectError(save(424242, configOf(afterFirst, "cfg-z"), f.withCSRF()), http.StatusNotFound, "not_found")
	assert.Equal(t, afterFirst, get(b.ID))

	// The per-subscription override route is reachable (it used to be
	// swallowed by the lifecycle route and always answered 404).
	free, err := f.db.GetPlanByName(context.Background(), database.FreePlanName)
	require.NoError(t, err)
	sub := &database.Subscription{TelegramID: 919191, ClientID: "cfg-api-client", SubscriptionID: "cfg-api-sub",
		Status: string(database.SubscriptionStatusActive), PlanID: free.ID}
	require.NoError(t, f.db.CreateSubscription(context.Background(), sub, ""))
	path := fmt.Sprintf("/admin/api/subscriptions/%d/builder", sub.ID)
	resp = f.do(http.MethodPost, path, f.withCSRF(), withJSON(fmt.Sprintf(`{"request_key":"assign-1","builder_id":%d}`, b.ID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
	var stored database.Subscription
	require.NoError(t, f.db.GetDB().First(&stored, sub.ID).Error)
	require.NotNil(t, stored.SubscriptionBuilderID)
	assert.Equal(t, b.ID, *stored.SubscriptionBuilderID)
	f.expectError(f.do(http.MethodPost, path, f.withCSRF(), withJSON(`{"request_key":"assign-2","builder_id":424242}`)), http.StatusNotFound, "not_found")
	// The lifecycle actions of the same prefix still route as before.
	f.expectError(f.do(http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/unknown", sub.ID), f.withCSRF(), withJSON(`{}`)), http.StatusNotFound, "not_found")
}
