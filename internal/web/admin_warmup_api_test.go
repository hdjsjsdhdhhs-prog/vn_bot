package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
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

// Customer warm-up HTTP contract (/admin/api/warmup*): session, CSRF,
// request_key replay, version conflicts, audit and error codes through the
// handler installed by Start. Rules are covered by the service tests.

type warmupAPIFixture struct {
	*adminAPIFixture
}

func newWarmupAPIFixture(t *testing.T, withWarmup bool) *warmupAPIFixture {
	t.Helper()
	base := newAdminAPIFixtureNoServer(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(adminTestPassword), config.AdminMinBcryptCost)
	require.NoError(t, err)
	cfg := &config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: "https://admin.example.com"}
	s := NewServer("127.0.0.1:0", nil, cfg, "", nil, nil)
	s.SetAdminService(service.NewAdminService(base.db, nil, nil))
	if withWarmup {
		s.SetWarmupService(service.NewWarmupService(base.db, database.TrialDefaults{DurationHours: 72, RateLimitPerHour: 3}))
	}
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(stopCtx))
	})
	base.attach(s)
	return &warmupAPIFixture{adminAPIFixture: base}
}

func (f *warmupAPIFixture) scenarioID(key string) uint {
	f.t.Helper()
	var sc database.WarmupScenario
	require.NoError(f.t, f.db.GetDB().Where("key = ?", key).First(&sc).Error)
	return sc.ID
}

func (f *warmupAPIFixture) scenarioPath(key string, suffix string) string {
	return fmt.Sprintf("/admin/api/warmup/scenarios/%d%s", f.scenarioID(key), suffix)
}

func (f *warmupAPIFixture) lastAudit() database.AdminAuditLog {
	f.t.Helper()
	var entry database.AdminAuditLog
	require.NoError(f.t, f.db.GetDB().Order("id DESC").First(&entry).Error)
	return entry
}

// expectCode asserts the status and the "error" code (validation errors also
// carry field and reason).
func (f *warmupAPIFixture) expectCode(resp *http.Response, status int, code string) map[string]any {
	f.t.Helper()
	require.Equal(f.t, status, resp.StatusCode)
	payload := f.raw(resp)
	require.Equal(f.t, code, payload["error"], "%v", payload)
	return payload
}

func (f *warmupAPIFixture) ok(resp *http.Response) map[string]any {
	f.t.Helper()
	require.Equal(f.t, http.StatusOK, resp.StatusCode)
	return f.raw(resp)
}

// The request wrappers below send one request and always close the response
// body themselves (defer), so no test leaks a body on an early failure.

// okDo sends a browser request and returns the 200 JSON payload.
func (f *warmupAPIFixture) okDo(method, path string, opts ...adminRequestOption) map[string]any {
	f.t.Helper()
	resp := f.do(method, path, opts...)
	defer resp.Body.Close()
	return f.ok(resp)
}

// codeDo sends a browser request and asserts the status and "error" code.
func (f *warmupAPIFixture) codeDo(status int, code, method, path string, opts ...adminRequestOption) map[string]any {
	f.t.Helper()
	resp := f.do(method, path, opts...)
	defer resp.Body.Close()
	return f.expectCode(resp, status, code)
}

// errorAs sends a request as client and asserts the bare {"error": code} body.
func (f *warmupAPIFixture) errorAs(client *http.Client, status int, code, method, path string, opts ...adminRequestOption) {
	f.t.Helper()
	resp := f.doAs(client, method, path, opts...)
	defer resp.Body.Close()
	f.expectError(resp, status, code)
}

// errorDo is errorAs for the browser client.
func (f *warmupAPIFixture) errorDo(status int, code, method, path string, opts ...adminRequestOption) {
	f.t.Helper()
	f.errorAs(f.client, status, code, method, path, opts...)
}

// statusDo sends a browser request and asserts only the status.
func (f *warmupAPIFixture) statusDo(status int, method, path string, opts ...adminRequestOption) {
	f.t.Helper()
	resp := f.do(method, path, opts...)
	defer resp.Body.Close()
	require.Equal(f.t, status, resp.StatusCode)
}

func patchJSON(requestKey string, version int, fields string) adminRequestOption {
	return withJSON(fmt.Sprintf(`{"request_key":%q,"version":%d,%s}`, requestKey, version, fields))
}

func (f *warmupAPIFixture) patchOK(key, requestKey string, version int, fields string) map[string]any {
	f.t.Helper()
	return f.okDo(http.MethodPatch, f.scenarioPath(key, ""), f.withCSRF(), patchJSON(requestKey, version, fields))
}

func (f *warmupAPIFixture) patchCode(status int, code, key, requestKey string, version int, fields string) map[string]any {
	f.t.Helper()
	return f.codeDo(status, code, http.MethodPatch, f.scenarioPath(key, ""), f.withCSRF(), patchJSON(requestKey, version, fields))
}

func warmupTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	img.Set(1, 1, color.RGBA{G: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestAdminWarmupAPI_AuthCSRFAndUnavailable(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.errorAs(f.anon, http.StatusUnauthorized, "unauthorized", http.MethodGet, "/admin/api/warmup")
	f.errorAs(f.anon, http.StatusUnauthorized, "unauthorized", http.MethodGet, "/admin/api/warmup/deliveries")
	f.login()
	before := f.auditCount()
	// State changes need the CSRF token.
	f.statusDo(http.StatusForbidden, http.MethodPut, "/admin/api/warmup/settings",
		withJSON(`{"request_key":"no-csrf","version":1,"enabled":false,"max_per_day":2,"cooldown_hours":24}`))
	f.statusDo(http.StatusForbidden, http.MethodPatch, f.scenarioPath("winback_3d", ""),
		withJSON(`{"request_key":"no-csrf-2","version":1,"enabled":false}`))
	assert.Equal(t, before, f.auditCount())

	unavailable := newWarmupAPIFixture(t, false)
	unavailable.login()
	unavailable.errorDo(http.StatusServiceUnavailable, "service_unavailable", http.MethodGet, "/admin/api/warmup")
}

func TestAdminWarmupAPI_OverviewSettingsAndReplay(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()

	overview := f.okDo(http.MethodGet, "/admin/api/warmup")
	assert.Len(t, overview["scenarios"], 14)
	assert.Len(t, overview["categories"], 3)
	assert.Contains(t, overview, "summary")
	assert.Contains(t, overview["registry"], "callbacks")
	for _, sc := range overview["scenarios"].([]any) {
		m := sc.(map[string]any)
		assert.NotContains(t, m, "tg_file_id")
	}

	settings := f.okDo(http.MethodGet, "/admin/api/warmup/settings")
	assert.EqualValues(t, 1, settings["version"])

	body := `{"request_key":"settings-1","version":1,"enabled":true,"max_per_day":3,"cooldown_hours":12}`
	out := f.okDo(http.MethodPut, "/admin/api/warmup/settings", f.withCSRF(), withJSON(body))
	saved := out["settings"].(map[string]any)
	assert.EqualValues(t, 3, saved["max_per_day"])
	assert.EqualValues(t, 6, saved["catchup_hours"], "an absent catch-up window keeps the stored one")
	assert.EqualValues(t, 2, saved["version"])
	assert.Equal(t, string(database.AdminActionWarmupSettingsUpdated), f.lastAudit().Action)

	audits := f.auditCount()
	replay := f.okDo(http.MethodPut, "/admin/api/warmup/settings", f.withCSRF(), withJSON(body))
	assert.Equal(t, true, replay["replayed"])
	assert.Equal(t, audits, f.auditCount(), "a replay is not applied twice")

	f.codeDo(http.StatusConflict, "version_conflict", http.MethodPut, "/admin/api/warmup/settings", f.withCSRF(),
		withJSON(`{"request_key":"settings-2","version":1,"enabled":true,"max_per_day":4,"cooldown_hours":12}`))
	f.codeDo(http.StatusConflict, "request_key_conflict", http.MethodPut, "/admin/api/warmup/settings", f.withCSRF(),
		withJSON(`{"request_key":"settings-1","version":2,"enabled":true,"max_per_day":5,"cooldown_hours":12}`))
	bad := f.codeDo(http.StatusBadRequest, "validation_error", http.MethodPut, "/admin/api/warmup/settings", f.withCSRF(),
		withJSON(`{"request_key":"settings-3","version":2,"enabled":true,"max_per_day":0,"cooldown_hours":12}`))
	assert.Equal(t, "max_per_day", bad["field"])
	f.errorDo(http.StatusBadRequest, "invalid_request", http.MethodPut, "/admin/api/warmup/settings", f.withCSRF(), withJSON(`{"request_key":"x","bogus":1}`))
}

func TestAdminWarmupAPI_ListAndGetScenarios(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()
	count := func(query string) int {
		return len(f.okDo(http.MethodGet, "/admin/api/warmup/scenarios"+query)["scenarios"].([]any))
	}
	assert.Equal(t, 14, count(""))
	assert.Equal(t, 3, count("?category=subscription"))
	assert.Equal(t, 3, count("?enabled=false"))
	assert.Equal(t, 11, count("?enabled=true"))
	assert.Equal(t, 5, count("?trigger=paid_expired"))
	names := f.okDo(http.MethodGet, "/admin/api/warmup/scenarios?order=name")["scenarios"].([]any)
	assert.LessOrEqual(t, names[0].(map[string]any)["name"], names[1].(map[string]any)["name"])
	f.codeDo(http.StatusBadRequest, "validation_error", http.MethodGet, "/admin/api/warmup/scenarios?order=bogus")
	f.errorDo(http.StatusBadRequest, "invalid_request", http.MethodGet, "/admin/api/warmup/scenarios?enabled=maybe")

	one := f.okDo(http.MethodGet, f.scenarioPath("trial_not_activated", ""))
	assert.Equal(t, "trial_not_activated", one["key"])
	assert.Len(t, one["buttons"], 1)
	f.errorDo(http.StatusNotFound, "scenario_not_found", http.MethodGet, "/admin/api/warmup/scenarios/9999")
	f.errorDo(http.StatusNotFound, "scenario_not_found", http.MethodGet, "/admin/api/warmup/scenarios/abc")
}

func TestAdminWarmupAPI_PatchEnableVersionAuditAndEnabledAt(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()

	out := f.patchOK("sub_expired", "enable-1", 1, `"enabled":true`)
	sc := out["scenario"].(map[string]any)
	assert.Equal(t, true, sc["enabled"])
	require.NotNil(t, sc["enabled_at"], "switching on sets enabled_at")
	assert.Equal(t, string(database.AdminActionWarmupScenarioEnabled), f.lastAudit().Action)
	assert.Equal(t, database.AdminTargetWarmupScenario, f.lastAudit().TargetType)

	audits := f.auditCount()
	assert.Equal(t, true, f.patchOK("sub_expired", "enable-1", 1, `"enabled":true`)["replayed"])
	assert.Equal(t, audits, f.auditCount())
	f.patchCode(http.StatusConflict, "version_conflict", "sub_expired", "enable-2", 1, `"name":"stale"`)

	out = f.patchOK("sub_expired", "disable-1", 2, `"enabled":false`)
	assert.Nil(t, out["scenario"].(map[string]any)["enabled_at"])
	assert.Equal(t, string(database.AdminActionWarmupScenarioDisable), f.lastAudit().Action)

	// An enabled scenario switched on long ago: a text edit keeps enabled_at,
	// a delay or trigger change restarts it (no history replay).
	past := time.Now().Add(-72 * time.Hour).UTC()
	require.NoError(t, f.db.GetDB().Model(&database.WarmupScenario{}).Where("key = ?", "winback_3d").Update("enabled_at", past).Error)
	out = f.patchOK("winback_3d", "rename-1", 1, `"name":"Возврат: 3 дня"`)
	at, err := time.Parse(time.RFC3339Nano, out["scenario"].(map[string]any)["enabled_at"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, past, at, time.Second)
	out = f.patchOK("winback_3d", "delay-1", 2, `"delay_value":4`)
	at, err = time.Parse(time.RFC3339Nano, out["scenario"].(map[string]any)["enabled_at"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), at, time.Minute)
	out = f.patchOK("winback_3d", "trigger-1", 3, `"trigger_type":"trial_period_end"`)
	assert.EqualValues(t, 4, out["scenario"].(map[string]any)["version"])

	f.patchOK("winback_7d", "coupon-1", 1, `"discount_percent":10,"message_html":"Код {{discount_code}}"`)
	assert.Equal(t, string(database.AdminActionWarmupScenarioUpdated), f.lastAudit().Action)
	f.patchOK("winback_7d", "coupon-2", 2, `"coupon_ttl_hours":72`)
	assert.Equal(t, string(database.AdminActionWarmupCouponChanged), f.lastAudit().Action)
	assert.Nil(t, f.patchOK("winback_7d", "no-image", 3, `"image_id":null`)["scenario"].(map[string]any)["image_id"])
	f.patchCode(http.StatusBadRequest, "invalid_request", "winback_7d", "empty-1", 4, `"request_hint":1`)
}

func TestAdminWarmupAPI_ValidationCodes(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()
	before := f.auditCount()
	cases := []struct{ fields, code, field string }{
		{`"enabled":true`, "duplicates_expiry_reminder", "delay_value"},
		{`"message_html":"Привет, {{first_name}}"`, "invalid_variable", "message_html"},
		{`"message_html":"{{discount_code}}"`, "invalid_variable", "message_html"},
		{`"message_html":"<div>x</div>"`, "invalid_html", "message_html"},
		{`"buttons":[{"text":"x","kind":"callback","value":"admin_stats"}]`, "callback_not_allowed", "buttons"},
		{`"buttons":[{"text":"x","kind":"url","value":"javascript:alert(1)"}]`, "invalid_button", "buttons"},
		{`"buttons":[{"text":"x","kind":"web_app","value":"http://example.com/app"}]`, "invalid_button", "buttons"},
		{`"trigger_type":"purchase"`, "invalid_trigger", "trigger_type"},
		{`"timing":"after"`, "invalid_trigger", "timing"},
		{`"delay_value":-1`, "validation_error", "delay_value"},
		{`"discount_percent":101`, "validation_error", "discount_percent"},
		{`"coupon_ttl_hours":0`, "validation_error", "coupon_ttl_hours"},
		{`"category":"promo"`, "validation_error", "category"},
		{`"image_id":999`, "image_not_found", "image_id"},
	}
	for _, tc := range cases {
		status := http.StatusBadRequest
		if tc.code == "duplicates_expiry_reminder" {
			status = http.StatusConflict
		}
		payload := f.patchCode(status, tc.code, "sub_expiring_24h", "bad-"+tc.code+tc.field, 1, tc.fields)
		assert.Equal(t, tc.field, payload["field"], tc.fields)
	}
	assert.Equal(t, before, f.auditCount(), "nothing invalid is stored")
}

func TestAdminWarmupAPI_TrialURLAndPreview(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()
	audits := f.auditCount()

	preview := f.okDo(http.MethodPost, f.scenarioPath("trial_not_activated", "/preview"), f.withCSRF(), withJSON(`{}`))
	assert.Equal(t, true, preview["valid"])
	assert.Contains(t, fmt.Sprint(preview["problems"]), "button_url_missing", "a button without URL is reported")
	assert.Empty(t, preview["message"].(map[string]any)["buttons"])

	withVars := f.okDo(http.MethodPost, f.scenarioPath("sub_expiring_48h", "/preview"), f.withCSRF(),
		withJSON(`{"context":{"username":"<b>bob","plan_name":"Gold & Co","expire_date":"2026-12-31T20:59:00Z"}}`))
	html := withVars["message"].(map[string]any)["html"].(string)
	assert.Contains(t, html, "«Gold &amp; Co» действует до 31.12.2026 23:59 МСК.")

	coupon := f.okDo(http.MethodPost, f.scenarioPath("winback_7d", "/preview"), f.withCSRF(),
		withJSON(`{"discount_percent":15,"coupon_template":"TEST","message_html":"Код <code>{{discount_code}}</code> для {{username}}"}`))
	code := coupon["discount_code"].(string)
	assert.True(t, strings.HasPrefix(code, "TEST-"))
	assert.Equal(t, true, coupon["discount_code_test"])
	assert.Contains(t, coupon["message"].(map[string]any)["html"], code)

	draft := f.okDo(http.MethodPost, "/admin/api/warmup/preview", f.withCSRF(), withJSON(`{"category":"winback","name":"Черновик",`+
		`"trigger_type":"paid_expired","timing":"after","delay_value":1,"delay_unit":"days","message_html":"{{oops}}","coupon_ttl_hours":48}`))
	assert.Equal(t, false, draft["valid"])

	var coupons, deliveries int64
	require.NoError(t, f.db.GetDB().Model(&database.WarmupCoupon{}).Count(&coupons).Error)
	require.NoError(t, f.db.GetDB().Model(&database.WarmupDelivery{}).Count(&deliveries).Error)
	assert.Zero(t, coupons, "a preview never issues a coupon")
	assert.Zero(t, deliveries, "a preview never journals a delivery")
	assert.Equal(t, audits, f.auditCount(), "a preview writes nothing")

	out := f.patchOK("trial_not_activated", "trial-url", 1, `"buttons":[{"text":"Активировать пробник","kind":"url","value":"https://dictator.example/trial"}]`)
	assert.Equal(t, "https://dictator.example/trial", out["scenario"].(map[string]any)["buttons"].([]any)[0].(map[string]any)["value"])
	preview = f.okDo(http.MethodPost, f.scenarioPath("trial_not_activated", "/preview"), f.withCSRF(), withJSON(`{}`))
	assert.NotContains(t, fmt.Sprint(preview["problems"]), "button_url_missing")
	assert.Len(t, preview["message"].(map[string]any)["buttons"], 1)
}

func TestAdminWarmupAPI_Images(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()
	data := warmupTestPNG(t)
	body := fmt.Sprintf(`{"request_key":"img-1","data":%q}`, base64.StdEncoding.EncodeToString(data))

	out := f.okDo(http.MethodPost, "/admin/api/warmup/images", f.withCSRF(), withJSON(body))
	img := out["image"].(map[string]any)
	assert.Equal(t, "image/png", img["mime"])
	assert.Len(t, img["sha256"], 64)
	assert.NotContains(t, img, "data")
	assert.NotContains(t, img, "tg_file_id")
	assert.Equal(t, string(database.AdminActionWarmupImageUploaded), f.lastAudit().Action)
	id := int(img["id"].(float64))
	assert.Equal(t, true, f.okDo(http.MethodPost, "/admin/api/warmup/images", f.withCSRF(), withJSON(body))["replayed"])

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/admin/api/warmup/images/%d", f.tls.URL, id), nil)
	require.NoError(t, err)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, data, raw)

	big := base64.StdEncoding.EncodeToString(make([]byte, database.WarmupImageMaxBytes+1))
	f.codeDo(http.StatusRequestEntityTooLarge, "image_too_large", http.MethodPost, "/admin/api/warmup/images", f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"img-big","data":%q}`, big)))
	gif := base64.StdEncoding.EncodeToString([]byte("GIF89a......."))
	f.codeDo(http.StatusBadRequest, "image_invalid_type", http.MethodPost, "/admin/api/warmup/images", f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"img-gif","data":%q}`, gif)))

	// In use by a scenario: not deleted. Detached: deleted.
	f.patchOK("winback_14d", "attach", 1, fmt.Sprintf(`"image_id":%d`, id))
	path := fmt.Sprintf("/admin/api/warmup/images/%d", id)
	f.errorDo(http.StatusConflict, "image_in_use", http.MethodDelete, path, f.withCSRF(), withJSON(`{"request_key":"del-1"}`))
	f.patchOK("winback_14d", "detach", 2, `"image_id":null`)
	f.okDo(http.MethodDelete, path, f.withCSRF(), withJSON(`{"request_key":"del-2"}`))
	assert.Equal(t, string(database.AdminActionWarmupImageDeleted), f.lastAudit().Action)
	assert.Equal(t, true, f.okDo(http.MethodDelete, path, f.withCSRF(), withJSON(`{"request_key":"del-2"}`))["replayed"])
	f.errorDo(http.StatusNotFound, "image_not_found", http.MethodGet, path)
}

func TestAdminWarmupAPI_DeliveriesAndCoupons(t *testing.T) {
	f := newWarmupAPIFixture(t, true)
	f.login()
	ctx := context.Background()
	trial := f.scenarioID("trial_ending")
	winback := f.scenarioID("winback_3d")
	now := time.Now().UTC()
	policy := database.WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}
	for i := range 5 {
		sc := winback
		if i%2 == 0 {
			sc = trial
		}
		claim := database.WarmupClaim{ScenarioID: sc, ScenarioName: "s", TelegramID: int64(7000 + i), AnchorKey: "a", AnchorAt: now, DueAt: now}
		if i == 1 {
			claim.Coupon = &database.WarmupCouponDraft{Code: "WB-ABCDEFGH", Percent: 10, ExpiresAt: now.Add(time.Hour)}
		}
		res, err := f.db.ClaimWarmupDelivery(ctx, claim, policy, now)
		require.NoError(t, err)
		if i < 3 {
			require.NoError(t, f.db.MarkWarmupDeliverySent(ctx, res.Delivery.ID, 100+i, now))
		}
	}
	expired := database.WarmupClaim{ScenarioID: winback, ScenarioName: "s", TelegramID: 7100, AnchorKey: "old", AnchorAt: now, DueAt: now,
		Coupon: &database.WarmupCouponDraft{Code: "WB-EXPIRED1", Percent: 5, ExpiresAt: now.Add(-time.Hour)}}
	_, err := f.db.ClaimWarmupDelivery(ctx, expired, policy, now)
	require.NoError(t, err)

	page := f.okDo(http.MethodGet, "/admin/api/warmup/deliveries?limit=2&offset=0")
	assert.EqualValues(t, 6, page["total"])
	assert.Len(t, page["deliveries"], 2)
	assert.EqualValues(t, 2, page["limit"])
	last := f.okDo(http.MethodGet, "/admin/api/warmup/deliveries?limit=2&offset=4")
	assert.Len(t, last["deliveries"], 2)
	total := func(query string) float64 {
		return f.okDo(http.MethodGet, "/admin/api/warmup/deliveries?"+query)["total"].(float64)
	}
	assert.EqualValues(t, 3, total("status=sent"))
	assert.EqualValues(t, 1, total("telegram_id=7001"))
	assert.EqualValues(t, 3, total(fmt.Sprintf("scenario_id=%d", winback)))
	assert.EqualValues(t, 3, total("category=trial"))
	assert.EqualValues(t, 1, total("coupon=wb-abcdefgh"))
	q := url.Values{"from": {now.Add(-time.Minute).Format(time.RFC3339)}, "to": {now.Add(time.Minute).Format(time.RFC3339)}}
	assert.EqualValues(t, 6, total(q.Encode()))
	q = url.Values{"from": {now.Add(time.Hour).Format(time.RFC3339)}}
	assert.EqualValues(t, 0, total(q.Encode()))
	f.codeDo(http.StatusBadRequest, "validation_error", http.MethodGet, "/admin/api/warmup/deliveries?status=lost")
	f.errorDo(http.StatusBadRequest, "invalid_request", http.MethodGet, "/admin/api/warmup/deliveries?from=yesterday")

	coupons := f.okDo(http.MethodGet, "/admin/api/warmup/coupons")
	assert.EqualValues(t, 2, coupons["total"])
	issued := f.okDo(http.MethodGet, "/admin/api/warmup/coupons?status=issued")["coupons"].([]any)
	require.Len(t, issued, 1)
	c := issued[0].(map[string]any)
	assert.Equal(t, "WB-ABCDEFGH", c["code"])
	assert.EqualValues(t, 7001, c["telegram_id"])
	assert.EqualValues(t, 10, c["percent"])
	assert.Equal(t, "issued", c["effective_status"])
	assert.Nil(t, c["redeemed_at"], "prepared field, not connected to payments")
	assert.Nil(t, c["order_id"])
	for _, key := range []string{"created_at", "expires_at", "status"} {
		assert.Contains(t, c, key)
	}
	expiredList := f.okDo(http.MethodGet, "/admin/api/warmup/coupons?status=expired")["coupons"].([]any)
	require.Len(t, expiredList, 1)
	assert.Equal(t, "expired", expiredList[0].(map[string]any)["effective_status"])
	f.codeDo(http.StatusBadRequest, "validation_error", http.MethodGet, "/admin/api/warmup/coupons?status=gone")
}
