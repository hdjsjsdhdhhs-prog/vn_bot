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

func TestMiniAppPurchase_NullableOrderRoundTrip(t *testing.T) {
	db, err := database.NewService(filepath.Join(t.TempDir(), "purchase.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()
	plan := &database.Plan{Name: "purchase-paid", IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	product := &database.Product{PlanID: plan.ID, Name: "Monthly", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)
	sub := &database.Subscription{TelegramID: 42003, ClientID: "purchase-client", SubscriptionID: "purchase-sub", PlanID: plan.ID, Status: "active"}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	before, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	cfg := &config.Config{TelegramBotToken: miniAppTestToken}
	handler := newMiniAppHandler(cfg, service.NewSubscriptionService(db, nil, nil, nil, cfg))
	key := uuid.NewString()
	request := func(method, path, body string, identity int64, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if identity != 0 {
			req.Header.Set("Authorization", "tma "+miniAppTestData(identity, time.Now(), miniAppTestToken))
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, want, rr.Code, "%s", rr.Body.String())
		assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
		return rr
	}
	body := `{"offer_id":"` + product.OfferID + `"}`
	request(http.MethodPost, "/api/miniapp/orders", body, 0, http.StatusUnauthorized)
	created := request(http.MethodPost, "/api/miniapp/orders", body, sub.TelegramID, http.StatusCreated)
	var info service.PurchaseOrderInfo
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &info))
	require.Len(t, info.OrderID, 32)
	assert.Equal(t, product.OfferID, info.OfferID)
	assert.Equal(t, product.PriceCents, info.AmountCents)
	assert.Equal(t, database.OrderStatusPending, info.Status)
	require.NotNil(t, info.ExpiresAt)
	path := "/api/miniapp/orders/" + info.OrderID
	assert.Equal(t, path, created.Header().Get("Location"))
	replayed := request(http.MethodPost, "/api/miniapp/orders", body, sub.TelegramID, http.StatusOK)
	assert.JSONEq(t, created.Body.String(), replayed.Body.String())
	read := request(http.MethodGet, path, "", sub.TelegramID, http.StatusOK)
	assert.JSONEq(t, created.Body.String(), read.Body.String())
	request(http.MethodGet, path, "", sub.TelegramID+1, http.StatusNotFound)
	for _, private := range []string{"buyer_telegram_id", "purchase_key", key, sub.Token, sub.ClientID} {
		assert.NotContains(t, created.Body.String(), private)
	}
	orders, err := db.GetOrdersBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, &info.OrderID, orders[0].PurchaseID)
	assert.Equal(t, &sub.TelegramID, orders[0].BuyerTelegramID)
	assert.Equal(t, &key, orders[0].PurchaseKey)
	after, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "purchase intent must not grant or change subscription access")
}
