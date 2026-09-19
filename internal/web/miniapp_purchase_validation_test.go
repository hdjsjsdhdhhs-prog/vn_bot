package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func purchaseHTTPFixture(t *testing.T) (http.Handler, *database.Service, *database.Subscription, *database.Product) {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "purchase.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	plan := &database.Plan{Name: "purchase-http", IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	product := &database.Product{PlanID: plan.ID, Name: "Monthly", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)
	sub := &database.Subscription{TelegramID: 45001, ClientID: "purchase-http-client", SubscriptionID: "purchase-http-sub", PlanID: plan.ID, Status: "active"}
	require.NoError(t, db.CreateSubscription(context.Background(), sub, ""))
	cfg := &config.Config{TelegramBotToken: miniAppTestToken}
	return newMiniAppHandler(cfg, service.NewSubscriptionService(db, nil, nil, nil, cfg)), db, sub, product
}

func purchaseHTTPRequest(handler http.Handler, identity int64, method, path, body string, keys []string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "tma "+miniAppTestData(identity, time.Now(), miniAppTestToken))
	req.Header.Set("Content-Type", "application/json")
	for _, key := range keys {
		req.Header.Add("Idempotency-Key", key)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestMiniAppPurchase_RejectsUntrustedInput(t *testing.T) {
	handler, db, sub, product := purchaseHTTPFixture(t)
	key := "550e8400-e29b-41d4-a716-446655440000"
	body := `{"offer_id":"` + product.OfferID + `"}`
	before, err := db.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	cases := []struct {
		name, path, body string
		keys             []string
	}{}
	// Use well-typed values: rejection must be for the extra server-owned
	// field, not merely because its value cannot be decoded into an integer.
	for field, value := range map[string]any{
		"telegram_id": sub.TelegramID + 1, "buyer_telegram_id": sub.TelegramID + 1,
		"user_id": sub.TelegramID + 1, "subscription_id": sub.ID, "product_id": product.ID,
		"amount_cents": 1, "price_cents": 1, "currency": "USD", "status": "paid",
		"purchase_id": strings.Repeat("a", 32), "purchase_key": uuid.NewString(),
		"purchase_expires_at": "2099-01-01T00:00:00Z", "created_at": "2026-01-01T00:00:00Z",
		"expires_at": "2099-01-01T00:00:00Z", "paid_at": "2026-01-01T00:00:00Z",
		"activated_at": "2026-01-01T00:00:00Z", "payment_provider": "platega",
		"provider_payment_id": uuid.NewString(), "payment_url": "https://payments.example/test",
		"payment_expires_at": "2099-01-01T00:00:00Z", "payment_creation_uncertain": false,
		"provider_source_id": 1, "plan_id": product.PlanID, "token": strings.Repeat("a", 64),
	} {
		payload, err := json.Marshal(map[string]any{"offer_id": product.OfferID, field: value})
		require.NoError(t, err)
		cases = append(cases, struct {
			name, path, body string
			keys             []string
		}{field, "/api/miniapp/orders", string(payload), []string{key}})
	}
	for _, malformed := range []string{"", "null", "[]", "{}", `{"offer_id":null}`, `{"offer_id":42}`, `{"offer_id":{}}`, `{"Offer_ID":"` + product.OfferID + `"}`, body + body, strings.TrimSuffix(body, "}"), strings.TrimSuffix(body, "}") + `,"offer_id":"` + product.OfferID + `"}`, strings.TrimSuffix(body, "}") + `,"offer_id":"` + strings.Repeat("f", 32) + `"}`, strings.TrimSuffix(body, "}") + `,"offer\u005fid":"` + product.OfferID + `"}`} {
		cases = append(cases, struct {
			name, path, body string
			keys             []string
		}{"json " + malformed, "/api/miniapp/orders", malformed, []string{key}})
	}
	for _, keys := range [][]string{nil, {""}, {key, key}, {"invalid"}, {uuid.Nil.String()}, {strings.ToUpper(key)}, {"550e8400-e29b-11d4-a716-446655440000"}, {"550e8400-e29b-41d4-0716-446655440000"}, {" " + key}, {key + " "}, {strings.ReplaceAll(key, "-", "")}, {"{" + key + "}"}, {"urn:uuid:" + key}, {key + "," + key}} {
		cases = append(cases, struct {
			name, path, body string
			keys             []string
		}{"key " + strings.Join(keys, "/"), "/api/miniapp/orders", body, keys})
	}
	cases = append(cases, struct {
		name, path, body string
		keys             []string
	}{"query identity", "/api/miniapp/orders?telegram_id=45002", body, []string{key}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := purchaseHTTPRequest(handler, sub.TelegramID, http.MethodPost, tc.path, tc.body, tc.keys)
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			assert.JSONEq(t, `{"error":"invalid_request"}`, rr.Body.String())
			assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
		})
	}
	var count int64
	require.NoError(t, db.GetDB().Model(&database.Order{}).Count(&count).Error)
	assert.Zero(t, count, "rejected HTTP inputs must not create orders")
	after, err := db.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	rr := purchaseHTTPRequest(handler, sub.TelegramID, http.MethodPost, "/api/miniapp/orders", body, []string{key})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var info service.PurchaseOrderInfo
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &info))
	assert.Equal(t, product.PriceCents, info.AmountCents)
	assert.Equal(t, product.Currency, info.Currency)
	assert.Equal(t, database.OrderStatusPending, info.Status)
}

func TestMiniAppPurchase_ConflictsAndTerminalReplay(t *testing.T) {
	handler, db, sub, product := purchaseHTTPFixture(t)
	key := uuid.NewString()
	body := `{"offer_id":"` + product.OfferID + `"}`
	request := func(offerBody, purchaseKey string, want int) *httptest.ResponseRecorder {
		t.Helper()
		rr := purchaseHTTPRequest(handler, sub.TelegramID, http.MethodPost, "/api/miniapp/orders", offerBody, []string{purchaseKey})
		require.Equal(t, want, rr.Code, rr.Body.String())
		return rr
	}
	rr := request(body, key, http.StatusCreated)
	var original service.PurchaseOrderInfo
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &original))
	other := &database.Product{PlanID: product.PlanID, Name: "Other", DurationDays: 60, PriceCents: 9000, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(other).Error)
	rr = request(`{"offer_id":"`+other.OfferID+`"}`, key, http.StatusConflict)
	assert.JSONEq(t, `{"error":"idempotency_conflict"}`, rr.Body.String())
	rr = request(body, uuid.NewString(), http.StatusConflict)
	assert.JSONEq(t, `{"error":"purchase_pending"}`, rr.Body.String())
	require.NoError(t, db.GetDB().Model(product).Update("is_active", false).Error)
	rr = request(body, uuid.NewString(), http.StatusConflict)
	assert.JSONEq(t, `{"error":"offer_unavailable"}`, rr.Body.String())
	for _, status := range []database.OrderStatus{database.OrderStatusPaid, database.OrderStatusExpired, database.OrderStatusCanceled} {
		require.NoError(t, db.GetDB().Model(&database.Order{}).Where("purchase_id = ?", original.OrderID).Update("status", status).Error)
		rr = request(body, key, http.StatusOK)
		var replay service.PurchaseOrderInfo
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &replay))
		expected := original
		expected.Status = status
		assert.Equal(t, expected, replay)
		read := purchaseHTTPRequest(handler, sub.TelegramID, http.MethodGet, "/api/miniapp/orders/"+original.OrderID, "", nil)
		require.Equal(t, http.StatusOK, read.Code)
		assert.JSONEq(t, rr.Body.String(), read.Body.String())
	}
	var count int64
	require.NoError(t, db.GetDB().Model(&database.Order{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
