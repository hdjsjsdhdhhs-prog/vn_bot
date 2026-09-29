package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Tariff editor HTTP contract (/admin/api/tariffs*, /admin/api/plans) through
// the exact handler installed by Start: session, CSRF, body decoding, error
// codes, versioning and idempotent replay. Repository rules are covered by
// internal/database/tariffs_test.go.

type tariffAPIFixture struct {
	*adminAPIFixture
	plan *database.Plan
}

func newTariffAPIFixture(t *testing.T, withTariffs bool) *tariffAPIFixture {
	t.Helper()
	base := newAdminAPIFixtureNoServer(t)
	plan := &database.Plan{Name: "api-premium", IsActive: true, DevicesLimit: 3}
	require.NoError(t, base.db.GetDB().Create(plan).Error)

	hash, err := bcrypt.GenerateFromPassword([]byte(adminTestPassword), config.AdminMinBcryptCost)
	require.NoError(t, err)
	cfg := &config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: "https://admin.example.com"}
	s := NewServer("127.0.0.1:0", nil, cfg, "", nil, nil)
	s.SetAdminService(service.NewAdminService(base.db, nil, nil))
	if withTariffs {
		s.SetTariffService(service.NewTariffService(base.db))
	}
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(ctx))
	})
	base.attach(s)
	return &tariffAPIFixture{adminAPIFixture: base, plan: plan}
}

// newAdminAPIFixtureNoServer seeds the database like newAdminAPIFixture (one
// linked active customer) without starting a server: the tariff fixture wires
// its own services before Start.
func newAdminAPIFixtureNoServer(t *testing.T) *adminAPIFixture {
	t.Helper()
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	expiry := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	active := testutil.CreateTestSubscription(testutil.DefaultTelegramID, testutil.DefaultUsername, string(database.SubscriptionStatusActive), &expiry)
	require.NoError(t, db.CreateSubscription(context.Background(), active, ""))
	return &adminAPIFixture{t: t, db: db, active: active}
}

// body is a create payload. The request key (printable ASCII, as the server
// requires) is derived from the content, so the same call replays.
func (f *tariffAPIFixture) body(name string, price int64, extra string) string {
	key := fmt.Sprintf("key-%x-%d", name, price)
	return fmt.Sprintf(`{"request_key":%q,"name":%q,"plan_id":%d,"duration_days":30,"price_cents":%d,"currency":"RUB",`+
		`"description":"Описание","features":["Раз","Два"],"badge":"Хит","is_active":true%s}`,
		key, name, f.plan.ID, price, extra)
}

type tariffOutcomeWire struct {
	Tariff    *service.TariffView `json:"tariff"`
	Previous  *service.TariffView `json:"previous"`
	Versioned bool                `json:"versioned"`
	Replayed  bool                `json:"replayed"`
	Audit     struct {
		ID     uint   `json:"id"`
		Action string `json:"action"`
	} `json:"audit"`
}

func (f *tariffAPIFixture) create(name string, price int64) service.TariffView {
	f.t.Helper()
	resp := f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withJSON(f.body(name, price, "")))
	require.Equal(f.t, http.StatusCreated, resp.StatusCode)
	var out tariffOutcomeWire
	f.decode(resp, &out)
	require.NotNil(f.t, out.Tariff)
	return *out.Tariff
}

func tariffPath(id uint, suffix string) string {
	return "/admin/api/tariffs/" + strconv.FormatUint(uint64(id), 10) + suffix
}

func TestAdminTariffAPI_RequiresSessionAndCSRF(t *testing.T) {
	f := newTariffAPIFixture(t, true)
	for _, path := range []string{"/admin/api/tariffs", "/admin/api/plans", "/admin/api/tariffs/1"} {
		f.expectError(f.doAs(f.anon, http.MethodGet, path), http.StatusUnauthorized, "unauthorized")
	}
	f.login()
	before := f.auditCount()
	// Unsafe methods without the synchronizer token never reach the service.
	resp := f.do(http.MethodPost, "/admin/api/tariffs", withJSON(f.body("NoCSRF", 100, "")))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp = f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withOrigin(adminAPIForeignOrigin), withJSON(f.body("Foreign", 100, "")))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, before, f.auditCount())
}

func TestAdminTariffAPI_ServiceUnavailableWithoutTariffService(t *testing.T) {
	f := newTariffAPIFixture(t, false)
	f.login()
	f.expectError(f.do(http.MethodGet, "/admin/api/tariffs"), http.StatusServiceUnavailable, "service_unavailable")
	f.expectError(f.do(http.MethodGet, "/admin/api/plans"), http.StatusServiceUnavailable, "service_unavailable")
}

func TestAdminTariffAPI_CRUDLifecycle(t *testing.T) {
	f := newTariffAPIFixture(t, true)
	f.login()

	var empty struct {
		Tariffs []service.TariffView `json:"tariffs"`
	}
	resp := f.do(http.MethodGet, "/admin/api/tariffs")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.decode(resp, &empty)
	assert.NotNil(t, empty.Tariffs, "an empty catalogue is [] on the wire")
	assert.Empty(t, empty.Tariffs)

	month := f.create("Месяц", 19900)
	assert.Equal(t, 1, month.Version)
	assert.Equal(t, "Описание", month.Description)
	assert.Equal(t, []string{"Раз", "Два"}, month.Features)
	assert.Equal(t, "Хит", month.Badge)
	assert.Equal(t, f.plan.Name, month.PlanName)
	assert.False(t, month.InUse)
	year := f.create("Год", 99900)
	assert.Equal(t, 1, year.SortOrder)

	// GET one.
	resp = f.do(http.MethodGet, tariffPath(month.ID, ""))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	wire := f.raw(resp)
	for _, key := range []string{"id", "offer_id", "name", "plan_id", "plan_name", "builder_id", "duration_days", "price_cents",
		"currency", "is_active", "description", "features", "badge", "sort_order", "version", "previous_id",
		"replaced_by_id", "orders", "subscriptions", "in_use"} {
		assert.Contains(t, wire, key)
	}
	f.expectError(f.do(http.MethodGet, tariffPath(99999, "")), http.StatusNotFound, "not_found")

	// PATCH in place (unused product), text is normalized.
	patch := fmt.Sprintf(`{"request_key":"patch-1","version":1,"name":"  Месяц+  ","plan_id":%d,"duration_days":31,`+
		`"price_cents":24900,"currency":"rub","description":"Новое\r\nописание","features":["  A ",""," "],"badge":"","is_active":true}`, f.plan.ID)
	resp = f.do(http.MethodPatch, tariffPath(month.ID, ""), f.withCSRF(), withJSON(patch))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var updated tariffOutcomeWire
	f.decode(resp, &updated)
	require.NotNil(t, updated.Tariff)
	assert.False(t, updated.Versioned)
	assert.Equal(t, month.ID, updated.Tariff.ID)
	assert.Equal(t, "Месяц+", updated.Tariff.Name)
	assert.Equal(t, "RUB", updated.Tariff.Currency)
	assert.Equal(t, "Новое\nописание", updated.Tariff.Description)
	assert.Equal(t, []string{"A"}, updated.Tariff.Features)
	assert.Equal(t, 2, updated.Tariff.Version)
	assert.Equal(t, "tariff_updated", updated.Audit.Action)

	// Disable / enable carry the version.
	resp = f.do(http.MethodPost, tariffPath(month.ID, "/disable"), f.withCSRF(), withJSON(`{"request_key":"dis-1","version":2}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var toggled tariffOutcomeWire
	f.decode(resp, &toggled)
	assert.False(t, toggled.Tariff.IsActive)
	assert.Equal(t, 3, toggled.Tariff.Version)
	resp = f.do(http.MethodPost, tariffPath(month.ID, "/enable"), f.withCSRF(), withJSON(`{"request_key":"en-1","version":3}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	f.decode(resp, &toggled)
	assert.True(t, toggled.Tariff.IsActive)

	// DELETE an unused tariff.
	resp = f.do(http.MethodDelete, tariffPath(year.ID, ""), f.withCSRF(), withJSON(`{"request_key":"del-1","version":1}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	deleted := f.raw(resp)
	assert.EqualValues(t, year.ID, deleted["deleted"])
	f.expectError(f.do(http.MethodGet, tariffPath(year.ID, "")), http.StatusNotFound, "not_found")
}

func TestAdminTariffAPI_ValidationAndDecoding(t *testing.T) {
	f := newTariffAPIFixture(t, true)
	f.login()
	before := f.auditCount()

	resp := f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withJSON(f.body("", 100, "")))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	payload := f.raw(resp)
	assert.Equal(t, "invalid_tariff", payload["error"])
	assert.Equal(t, "name", payload["field"], "the offending field is named for the editor")

	f.expectError(f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withJSON(`{"request_key":"x","unknown":1}`)),
		http.StatusBadRequest, "invalid_request")
	f.expectError(f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withJSON(f.body("A", 100, "")), withContentType("text/plain")),
		http.StatusUnsupportedMediaType, "unsupported_media_type")
	f.expectError(f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(),
		withJSON(`{"request_key":"big","name":"`+strings.Repeat("a", adminTariffMaxBody)+`"}`)),
		http.StatusBadRequest, "invalid_request")
	// A missing request key is rejected before anything is written.
	f.expectError(f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(),
		withJSON(strings.Replace(f.body("NoKey", 100, ""), fmt.Sprintf(`"request_key":"key-%x-100"`, "NoKey"), `"request_key":""`, 1))),
		http.StatusBadRequest, "invalid_request")
	f.expectError(f.do(http.MethodPut, "/admin/api/tariffs", f.withCSRF()), http.StatusMethodNotAllowed, "method_not_allowed")
	f.expectError(f.do(http.MethodGet, "/admin/api/tariffs/reorder"), http.StatusMethodNotAllowed, "method_not_allowed")
	f.expectError(f.do(http.MethodGet, "/admin/api/tariffs/abc"), http.StatusNotFound, "not_found")
	f.expectError(f.do(http.MethodGet, "/admin/api/tariffs/1/enable"), http.StatusMethodNotAllowed, "method_not_allowed")
	assert.Equal(t, before, f.auditCount(), "rejected requests are not audited")
}

func TestAdminTariffAPI_VersionConflictAndIdempotency(t *testing.T) {
	f := newTariffAPIFixture(t, true)
	f.login()
	month := f.create("Месяц", 19900)

	patch := func(key string, version int, price int64) *http.Response {
		return f.do(http.MethodPatch, tariffPath(month.ID, ""), f.withCSRF(), withJSON(fmt.Sprintf(
			`{"request_key":%q,"version":%d,"name":"Месяц","plan_id":%d,"duration_days":30,"price_cents":%d,"currency":"RUB",`+
				`"description":"","features":[],"badge":"","is_active":true}`, key, version, f.plan.ID, price)))
	}
	resp := patch("edit-a", 1, 20000)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Same key + same payload replays the committed outcome.
	resp = patch("edit-a", 1, 20000)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var replay tariffOutcomeWire
	f.decode(resp, &replay)
	assert.True(t, replay.Replayed)
	assert.Equal(t, 2, replay.Tariff.Version, "a replay does not apply the change twice")

	// Same key + another payload is a key conflict.
	f.expectError(patch("edit-a", 1, 30000), http.StatusConflict, "request_key_conflict")
	// A stale editor (version 1) is rejected.
	f.expectError(patch("edit-b", 1, 30000), http.StatusConflict, "version_conflict")

	// Create replay: one product only.
	body := f.body("Replay", 500, "")
	first := f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withJSON(body))
	require.Equal(t, http.StatusCreated, first.StatusCode)
	var created tariffOutcomeWire
	f.decode(first, &created)
	again := f.do(http.MethodPost, "/admin/api/tariffs", f.withCSRF(), withJSON(body))
	require.Equal(t, http.StatusCreated, again.StatusCode)
	var replayed tariffOutcomeWire
	f.decode(again, &replayed)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, created.Tariff.ID, replayed.Tariff.ID)
	var n int64
	require.NoError(t, f.db.GetDB().Model(&database.Product{}).Count(&n).Error)
	assert.Equal(t, int64(2), n)
}

func TestAdminTariffAPI_UsedTariffVersioningAndDeleteProtection(t *testing.T) {
	f := newTariffAPIFixture(t, true)
	f.login()
	month := f.create("Месяц", 19900)

	// A paid purchase references the product.
	require.NoError(t, f.db.GetDB().Model(&database.Subscription{}).Where("id = ?", f.active.ID).
		Updates(map[string]any{"product_id": month.ID, "price_paid_cents": 19900}).Error)
	order := &database.Order{SubscriptionID: f.active.ID, ProductID: month.ID, Status: database.OrderStatusPaid, AmountCents: 19900, Currency: "RUB"}
	require.NoError(t, f.db.GetDB().Create(order).Error)

	resp := f.do(http.MethodGet, tariffPath(month.ID, ""))
	var used service.TariffView
	f.decode(resp, &used)
	assert.True(t, used.InUse)
	assert.EqualValues(t, 1, used.Orders)
	assert.EqualValues(t, 1, used.Subscriptions)

	f.expectError(f.do(http.MethodDelete, tariffPath(month.ID, ""), f.withCSRF(), withJSON(`{"request_key":"del-used","version":1}`)),
		http.StatusConflict, "tariff_in_use")

	resp = f.do(http.MethodPatch, tariffPath(month.ID, ""), f.withCSRF(), withJSON(fmt.Sprintf(
		`{"request_key":"price-up","version":1,"name":"Месяц","plan_id":%d,"duration_days":30,"price_cents":24900,"currency":"RUB",`+
			`"description":"Новая цена","features":[],"badge":"","is_active":true}`, f.plan.ID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out tariffOutcomeWire
	f.decode(resp, &out)
	require.True(t, out.Versioned)
	require.NotNil(t, out.Tariff)
	require.NotNil(t, out.Previous)
	assert.NotEqual(t, month.ID, out.Tariff.ID)
	assert.EqualValues(t, 24900, out.Tariff.PriceCents)
	require.NotNil(t, out.Tariff.PreviousID)
	assert.Equal(t, month.ID, *out.Tariff.PreviousID)
	assert.Equal(t, month.ID, out.Previous.ID)
	assert.False(t, out.Previous.IsActive)
	assert.EqualValues(t, 19900, out.Previous.PriceCents, "the historical terms are untouched")
	require.NotNil(t, out.Previous.ReplacedByID)
	assert.Equal(t, out.Tariff.ID, *out.Previous.ReplacedByID)

	var reloaded database.Order
	require.NoError(t, f.db.GetDB().First(&reloaded, order.ID).Error)
	assert.Equal(t, month.ID, reloaded.ProductID, "old orders keep their product")
	sub, err := f.db.GetByID(context.Background(), f.active.ID)
	require.NoError(t, err)
	require.NotNil(t, sub.ProductID)
	assert.Equal(t, month.ID, *sub.ProductID, "old subscriptions keep their product")

	f.expectError(f.do(http.MethodPost, tariffPath(month.ID, "/enable"), f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"reenable","version":%d}`, out.Previous.Version))),
		http.StatusConflict, "tariff_superseded")
}

func TestAdminTariffAPI_ReorderAndPlans(t *testing.T) {
	f := newTariffAPIFixture(t, true)
	f.login()
	a := f.create("A", 100)
	b := f.create("B", 200)
	c := f.create("C", 300)

	body := fmt.Sprintf(`{"request_key":"reorder-1","ids":[%d,%d,%d]}`, c.ID, a.ID, b.ID)
	resp := f.do(http.MethodPost, "/admin/api/tariffs/reorder", f.withCSRF(), withJSON(body))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var reordered struct {
		Tariffs  []service.TariffView `json:"tariffs"`
		Replayed bool                 `json:"replayed"`
	}
	f.decode(resp, &reordered)
	require.Len(t, reordered.Tariffs, 3)
	assert.Equal(t, []uint{c.ID, a.ID, b.ID}, []uint{reordered.Tariffs[0].ID, reordered.Tariffs[1].ID, reordered.Tariffs[2].ID})

	f.expectError(f.do(http.MethodPost, "/admin/api/tariffs/reorder", f.withCSRF(),
		withJSON(fmt.Sprintf(`{"request_key":"reorder-2","ids":[%d,%d]}`, a.ID, b.ID))), http.StatusConflict, "order_stale")
	f.expectError(f.do(http.MethodPost, "/admin/api/tariffs/reorder", f.withCSRF(),
		withJSON(`{"request_key":"reorder-3","ids":[]}`)), http.StatusBadRequest, "invalid_request")

	resp = f.do(http.MethodGet, "/admin/api/plans")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var plans struct {
		Plans []service.TariffPlanView `json:"plans"`
	}
	f.decode(resp, &plans)
	byName := map[string]service.TariffPlanView{}
	for _, p := range plans.Plans {
		byName[p.Name] = p
	}
	require.Contains(t, byName, f.plan.Name)
	assert.True(t, byName[f.plan.Name].Selectable)
	assert.EqualValues(t, 3, byName[f.plan.Name].Tariffs)
	assert.False(t, byName[database.TrialPlanName].Selectable)
	assert.False(t, byName[database.FreePlanName].Selectable)
	f.expectError(f.do(http.MethodPost, "/admin/api/plans", f.withCSRF(), withJSON(`{}`)), http.StatusMethodNotAllowed, "method_not_allowed")
}
