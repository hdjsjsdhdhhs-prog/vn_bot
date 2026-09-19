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

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/telegramstars"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiniAppStars_AuthenticatedPurchaseToTelegramSettlement(t *testing.T) {
	ctx := context.Background()
	db, err := database.NewService(filepath.Join(t.TempDir(), "stars.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	cfg := &config.Config{TelegramBotToken: miniAppTestToken} // legacy payments disabled
	plan := &database.Plan{Name: "stars-paid", IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	product := &database.Product{PlanID: plan.ID, Name: "Monthly Stars", DurationDays: 30, PriceCents: 123, Currency: "XTR", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)
	free, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	sub := &database.Subscription{TelegramID: 42003, ClientID: "stars-client", SubscriptionID: "stars-sub", PlanID: free.ID, Status: "active"}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	before, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	var payload string
	invoiceCalls, answers := 0, 0
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"stars_test_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/createInvoiceLink"):
			invoiceCalls++
			payload = r.Form.Get("payload")
			assert.Equal(t, "XTR", r.Form.Get("currency"))
			assert.Empty(t, r.Form.Get("provider_token"))
			var prices []tgbotapi.LabeledPrice
			require.NoError(t, json.Unmarshal([]byte(r.Form.Get("prices")), &prices))
			require.Len(t, prices, 1)
			assert.Equal(t, 123, prices[0].Amount)
			for _, secret := range []string{sub.Token, sub.ClientID, sub.SubscriptionID, miniAppTestToken} {
				assert.NotContains(t, payload, secret)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":"https://t.me/$stars-test"}`))
		case strings.HasSuffix(r.URL.Path, "/answerPreCheckoutQuery"):
			answers++
			assert.Equal(t, "checkout", r.Form.Get("pre_checkout_query_id"))
			assert.Equal(t, "true", r.Form.Get("ok"))
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			t.Errorf("unexpected Telegram method: %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer telegram.Close()
	api, err := tgbotapi.NewBotAPIWithClient("test-token", telegram.URL+"/bot%s/%s", telegram.Client())
	require.NoError(t, err)
	subscriptions := service.NewSubscriptionService(db, nil, nil, nil, cfg)
	stars := service.NewStarsPaymentService(service.NewOrderService(db, subscriptions, service.NewSyncService(db, nil, nil), nil, "", cfg), telegramstars.New(api))
	handler := newMiniAppHandler(cfg, subscriptions, func() *service.StarsPaymentService { return stars })
	request := func(method, path, body string, id int64, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if id != 0 {
			req.Header.Set("Authorization", "tma "+miniAppTestData(id, time.Now(), miniAppTestToken))
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.NewString())
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, want, rr.Code, rr.Body.String())
		return rr
	}
	for _, field := range []string{`"buyer_telegram_id":42004`, `"amount_cents":1`, `"currency":"RUB"`, `"duration_days":365`, `"provider_source_id":1`, `"status":"paid"`} {
		request("POST", "/api/miniapp/orders", `{"offer_id":"`+product.OfferID+`",`+field+`}`, sub.TelegramID, 400)
	}
	created := request("POST", "/api/miniapp/orders", `{"offer_id":"`+product.OfferID+`"}`, sub.TelegramID, 201)
	var purchase service.PurchaseOrderInfo
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &purchase))
	assert.Equal(t, int64(123), purchase.AmountCents)
	assert.Equal(t, "XTR", purchase.Currency)
	path := "/api/miniapp/orders/" + purchase.OrderID + "/invoice"
	request("POST", path, "", 0, 401)
	request("POST", path, "", sub.TelegramID+1, 404)
	request("GET", path, "", sub.TelegramID, 405)
	request("POST", path, "{}", sub.TelegramID, 400)
	request("POST", path+"?amount=1", "", sub.TelegramID, 400)
	request("POST", "/api/miniapp/orders/invalid/invoice", "", sub.TelegramID, 404)
	assert.Zero(t, invoiceCalls)
	invoice := request("POST", path, "", sub.TelegramID, 200)
	assert.JSONEq(t, `{"invoice_url":"https://t.me/$stars-test"}`, invoice.Body.String())
	assert.Equal(t, "stars:v1:"+purchase.OrderID, payload)
	assert.Equal(t, 1, invoiceCalls)
	// Browser cannot settle or activate via any payment callback route.
	request("POST", "/api/miniapp/orders/"+purchase.OrderID+"/settle", "", sub.TelegramID, 405)
	h := &bot.Handler{}
	h.SetStarsPaymentService(stars)
	// Decode the pinned SDK's actual Telegram JSON structures, then use bot routing.
	var checkout tgbotapi.Update
	raw, err := json.Marshal(map[string]any{"update_id": 1, "pre_checkout_query": map[string]any{"id": "checkout", "from": map[string]any{"id": sub.TelegramID}, "currency": "XTR", "total_amount": 123, "invoice_payload": payload}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &checkout))
	h.HandleUpdate(ctx, checkout)
	assert.Equal(t, 1, answers)
	unchanged, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, before, unchanged, "invoice and pre-checkout grant no access")
	raw, err = json.Marshal(map[string]any{"update_id": 2, "message": map[string]any{"message_id": 3, "date": time.Now().Unix(), "from": map[string]any{"id": sub.TelegramID}, "chat": map[string]any{"id": sub.TelegramID, "type": "private"}, "successful_payment": map[string]any{"currency": "XTR", "total_amount": 123, "invoice_payload": payload, "telegram_payment_charge_id": "telegram-official-charge", "provider_payment_charge_id": ""}}})
	require.NoError(t, err)
	var paid tgbotapi.Update
	require.NoError(t, json.Unmarshal(raw, &paid))
	h.HandleUpdate(ctx, paid)
	after, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.ID, after.PlanID)
	assert.Equal(t, "active", after.Status)
	assert.Equal(t, int64(123), after.PricePaidCents)
	require.NotNil(t, after.ExpiresAt)
	assert.WithinDuration(t, time.Now().UTC().AddDate(0, 0, 30), *after.ExpiresAt, 5*time.Second)
	assert.Equal(t, before.Token, after.Token)
	h.HandleUpdate(ctx, paid)
	replayed, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, after, replayed)
	response := request("GET", "/api/miniapp/orders/"+purchase.OrderID, "", sub.TelegramID, 200)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &purchase))
	assert.Equal(t, database.OrderStatusPaid, purchase.Status)
	request("POST", path, "", sub.TelegramID, 409)
	orders, err := db.GetOrdersBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, "telegram_stars", orders[0].PaymentProvider)
	assert.Equal(t, "telegram-official-charge", orders[0].ProviderPaymentID)
	assert.Equal(t, orders[0].ExpiresAt, after.ExpiresAt)
}
