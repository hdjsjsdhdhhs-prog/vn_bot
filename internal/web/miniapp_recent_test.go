package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiniAppRecent_RecoveryAndOwnership(t *testing.T) {
	handler, db, sub, product := purchaseHTTPFixture(t)
	read := func(id int64) []service.PurchaseOrderInfo {
		t.Helper()
		rr := purchaseHTTPRequest(handler, id, "GET", "/api/miniapp/orders/recent", "", nil)
		require.Equal(t, 200, rr.Code, rr.Body.String())
		var result struct {
			Orders []service.PurchaseOrderInfo `json:"orders"`
		}
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
		for _, private := range []string{sub.Token, sub.ClientID, "purchase_key", "buyer_telegram_id", "stars_checkout_id"} {
			assert.NotContains(t, rr.Body.String(), private)
		}
		return result.Orders
	}
	require.Empty(t, read(sub.TelegramID))
	created := purchaseHTTPRequest(handler, sub.TelegramID, "POST", "/api/miniapp/orders", `{"offer_id":"`+product.OfferID+`"}`, []string{uuid.NewString()})
	require.Equal(t, 201, created.Code)
	var order service.PurchaseOrderInfo
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &order))
	require.Equal(t, []service.PurchaseOrderInfo{order}, read(sub.TelegramID))
	require.Empty(t, read(sub.TelegramID+1))
	// Purchase deadlines are immutable. Seed a separate historical purchase,
	// rather than bypassing the trigger to force the HTTP-created order to expire.
	require.NoError(t, db.GetDB().Model(&database.Order{}).Where("purchase_id = ?", order.OrderID).
		Update("status", database.OrderStatusCanceled).Error)
	historical, _, err := db.CreatePurchaseOrder(context.Background(), sub.TelegramID, product.OfferID, uuid.NewString(),
		func(c *database.PurchaseCatalog, _ *database.Product) (time.Time, error) {
			return c.Now.Add(-time.Minute), nil
		})
	require.NoError(t, err)
	require.NoError(t, db.GetDB().Model(&database.Order{}).Where("id = ?", historical.Order.ID).
		Update("stars_checkout_id", "private-checkout").Error)
	recent := read(sub.TelegramID)
	require.Len(t, recent, 2)
	assert.Equal(t, *historical.Order.PurchaseID, recent[0].OrderID)
	assert.Equal(t, database.OrderStatusExpired, recent[0].Status)
	assert.True(t, recent[0].CheckoutStarted)
	assert.Equal(t, database.OrderStatusCanceled, recent[1].Status)
	before, err := db.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	read(sub.TelegramID)
	after, err := db.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "recovery must not grant access")
	require.NoError(t, db.GetDB().Model(sub).Update("telegram_id", sub.TelegramID+1).Error)
	require.Empty(t, read(before.TelegramID), "original buyer cannot read after transfer")
	require.Empty(t, read(before.TelegramID+1), "new owner did not buy original order")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/api/miniapp/orders/recent", nil))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/api/miniapp/orders/recent", 405},
		{"GET", "/api/miniapp/orders/recent?telegram_id=1", 400},
	} {
		rr := purchaseHTTPRequest(handler, before.TelegramID, tc.method, tc.path, "", nil)
		assert.Equal(t, tc.status, rr.Code)
	}
}

func TestMiniAppRecent_BoundedNewestFirst(t *testing.T) {
	handler, db, sub, product := purchaseHTTPFixture(t)
	for i := 0; i < 23; i++ {
		ref := strings.ReplaceAll(uuid.NewString(), "-", "")
		key := uuid.NewString()
		deadline := time.Now().UTC().Add(time.Hour)
		order := &database.Order{PurchaseID: &ref, PurchaseKey: &key, BuyerTelegramID: &sub.TelegramID, PurchaseExpiresAt: &deadline,
			SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPaid,
			AmountCents: product.PriceCents, Currency: product.Currency, CreatedAt: time.Now()}
		require.NoError(t, db.GetDB().Create(order).Error)
	}
	rr := purchaseHTTPRequest(handler, sub.TelegramID, "GET", "/api/miniapp/orders/recent", "", nil)
	require.Equal(t, 200, rr.Code)
	var result struct {
		Orders []service.PurchaseOrderInfo `json:"orders"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &result))
	require.Len(t, result.Orders, 20)
	for i := 1; i < len(result.Orders); i++ {
		assert.False(t, result.Orders[i].CreatedAt.After(result.Orders[i-1].CreatedAt))
	}
}
