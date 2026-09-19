package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type starsTestClient struct {
	payload    string
	amount     int64
	invoices   int
	approved   bool
	answerErr  error
	invoiceErr error
}

func (c *starsTestClient) CreateInvoiceLink(_ context.Context, _, payload string, amount int64) (string, error) {
	c.payload, c.amount = payload, amount
	c.invoices++
	return "https://t.me/$test", c.invoiceErr
}
func (c *starsTestClient) AnswerPreCheckout(_ context.Context, _ string, ok bool) error {
	c.approved = ok
	return c.answerErr
}

type starsFixture struct {
	purchases *PurchaseService
	db        *database.Service
	sub       *database.Subscription
	product   *database.Product
	stars     *StarsPaymentService
	client    *starsTestClient
	ref       string
}

func newStarsFixture(t *testing.T) *starsFixture {
	t.Helper()
	p, db, sub, product := purchaseServiceFixture(t)
	require.NoError(t, db.GetDB().Model(product).Updates(map[string]any{"currency": "XTR", "price_cents": 75}).Error)
	product.Currency, product.PriceCents = "XTR", 75
	client := &starsTestClient{}
	subscriptions := NewSubscriptionService(db, nil, nil, nil, nil)
	orders := NewOrderService(db, subscriptions, NewSyncService(db, nil, nil), nil, "", nil)
	return &starsFixture{purchases: p, db: db, sub: sub, product: product, stars: NewStarsPaymentService(orders, client), client: client}
}
func (f *starsFixture) invoice(t *testing.T) {
	t.Helper()
	info, created, err := f.purchases.Create(context.Background(), f.product.OfferID, uuid.NewString())
	require.NoError(t, err)
	require.True(t, created)
	f.ref = info.OrderID
	link, err := f.stars.Invoice(context.Background(), f.purchases, f.ref)
	require.NoError(t, err)
	require.Equal(t, "https://t.me/$test", link)
}
func (f *starsFixture) query() *tgbotapi.PreCheckoutQuery {
	return &tgbotapi.PreCheckoutQuery{ID: "checkout", From: &tgbotapi.User{ID: f.sub.TelegramID}, Currency: "XTR", TotalAmount: 75, InvoicePayload: "stars:v1:" + f.ref}
}
func (f *starsFixture) paid() tgbotapi.Update {
	return tgbotapi.Update{UpdateID: 10, Message: &tgbotapi.Message{From: &tgbotapi.User{ID: f.sub.TelegramID}, Date: int(time.Now().Unix()), SuccessfulPayment: &tgbotapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 75, InvoicePayload: "stars:v1:" + f.ref, TelegramPaymentChargeID: "charge"}}}
}
func (f *starsFixture) subscription(t *testing.T) *database.Subscription {
	t.Helper()
	sub, err := f.db.GetByID(context.Background(), f.sub.ID)
	require.NoError(t, err)
	return sub
}
func (f *starsFixture) state(t *testing.T, status database.OrderStatus) {
	t.Helper()
	require.NoError(t, f.db.GetDB().Model(&database.Order{}).Where("purchase_id = ?", f.ref).Update("status", status).Error)
}

func TestStarsInvoice_ServerTermsAndNoActivation(t *testing.T) {
	f := newStarsFixture(t)
	before := f.subscription(t)
	f.invoice(t)
	assert.Equal(t, int64(75), f.client.amount)
	assert.Equal(t, "stars:v1:"+f.ref, f.client.payload)
	assert.NotContains(t, f.client.payload, before.Token)
	assert.Equal(t, before, f.subscription(t))
	_, err := f.stars.Invoice(context.Background(), f.purchases, f.ref)
	require.NoError(t, err)
	assert.Equal(t, 1, f.client.invoices, "reuse persisted invoice")
	require.NoError(t, f.stars.PreCheckout(context.Background(), f.query()))
	assert.True(t, f.client.approved)
	assert.Equal(t, before, f.subscription(t), "checkout approval is not fulfillment")
	require.NoError(t, f.stars.PreCheckout(context.Background(), f.query()), "same query retries safely")
	other := f.query()
	other.ID = "second-checkout"
	require.Error(t, f.stars.PreCheckout(context.Background(), other))
	assert.False(t, f.client.approved, "one purchase cannot authorize two charges")
	changed := *f.product
	changed.DurationDays++
	require.ErrorIs(t, f.db.UpdateProductGuarded(context.Background(), &changed), database.ErrProductImmutable)
}

func TestStarsPreCheckout_RejectsInvalidBoundary(t *testing.T) {
	for _, name := range []string{"payer", "missing payer", "amount", "currency", "payload", "missing purchase", "expired", "canceled", "paid", "revoked", "disabled offer", "legacy provider", "unprepared"} {
		t.Run(name, func(t *testing.T) {
			f := newStarsFixture(t)
			if name == "expired" {
				require.NoError(t, f.db.GetDB().Model(f.product).Update("offer_ends_at", time.Now().UTC().Add(2*time.Second)).Error)
			}
			f.invoice(t)
			q := f.query()
			switch name {
			case "payer":
				q.From.ID++
			case "missing payer":
				q.From = nil
			case "amount":
				q.TotalAmount++
			case "currency":
				q.Currency = "RUB"
			case "payload":
				q.InvoicePayload = "invalid"
			case "missing purchase":
				q.InvoicePayload = "stars:v1:" + strings.Repeat("f", 32)
			case "expired":
				time.Sleep(2100 * time.Millisecond)
			case "canceled":
				f.state(t, database.OrderStatusCanceled)
			case "paid":
				f.state(t, database.OrderStatusPaid)
			case "revoked":
				require.NoError(t, f.db.GetDB().Model(f.sub).Update("status", "revoked").Error)
			case "disabled offer":
				require.NoError(t, f.db.GetDB().Model(f.product).Update("is_active", false).Error)
			case "legacy provider":
				require.NoError(t, f.db.GetDB().Model(&database.Order{}).Where("purchase_id = ?", f.ref).Update("payment_provider", "platega").Error)
			case "unprepared":
				require.NoError(t, f.db.GetDB().Model(&database.Order{}).Where("purchase_id = ?", f.ref).Update("payment_provider", "").Error)
			}
			before := f.subscription(t)
			require.Error(t, f.stars.PreCheckout(context.Background(), q))
			assert.False(t, f.client.approved)
			assert.Equal(t, before, f.subscription(t))
			switch name {
			case "expired", "canceled", "paid", "revoked", "disabled offer", "legacy provider":
				_, err := f.stars.Invoice(context.Background(), f.purchases, f.ref)
				require.Error(t, err)
			}
		})
	}
}

func TestStarsSuccessfulPayment_RejectsInvalidBoundary(t *testing.T) {
	for _, name := range []string{"payer", "missing payer", "amount", "currency", "payload", "missing purchase", "missing charge", "missing message", "missing payment", "unapproved", "canceled", "revoked", "conflicting replay"} {
		t.Run(name, func(t *testing.T) {
			f := newStarsFixture(t)
			f.invoice(t)
			if name != "unapproved" {
				require.NoError(t, f.stars.PreCheckout(context.Background(), f.query()))
			}
			u := f.paid()
			switch name {
			case "payer":
				u.Message.From.ID++
			case "missing payer":
				u.Message.From = nil
			case "amount":
				u.Message.SuccessfulPayment.TotalAmount++
			case "currency":
				u.Message.SuccessfulPayment.Currency = "RUB"
			case "payload":
				u.Message.SuccessfulPayment.InvoicePayload = "invalid"
			case "missing purchase":
				u.Message.SuccessfulPayment.InvoicePayload = "stars:v1:" + strings.Repeat("f", 32)
			case "missing charge":
				u.Message.SuccessfulPayment.TelegramPaymentChargeID = ""
			case "missing message":
				u.Message = nil
			case "missing payment":
				u.Message.SuccessfulPayment = nil
			case "canceled":
				f.state(t, database.OrderStatusCanceled)
			case "revoked":
				require.NoError(t, f.db.GetDB().Model(f.sub).Update("status", "revoked").Error)
			case "conflicting replay":
				require.NoError(t, f.stars.CaptureUpdate(context.Background(), u))
				u.Message.SuccessfulPayment.TotalAmount++
			}
			before := f.subscription(t)
			_, err := f.stars.SuccessfulPayment(context.Background(), u)
			require.Error(t, err)
			assert.Equal(t, before, f.subscription(t))
		})
	}
}

func TestStarsSettlement_ProviderSourceAndConcurrentReplay(t *testing.T) {
	f := newStarsFixture(t)
	ctx := context.Background()
	source := &database.ProviderSource{Name: "source", Type: "external", Enabled: true, SubscriptionURL: "https://provider.example/sub/private", Headers: `{"Authorization":"private"}`}
	require.NoError(t, f.db.GetDB().Create(source).Error)
	expiry := time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, f.db.GetDB().Model(f.sub).Updates(map[string]any{"provider_source_id": source.ID, "expires_at": expiry}).Error)
	f.invoice(t)
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	before := f.subscription(t)
	var wg sync.WaitGroup
	results := make(chan *PaymentConfirmation, 6)
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.stars.SuccessfulPayment(ctx, f.paid())
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	activated := 0
	for result := range results {
		if result.Activated {
			activated++
		}
	}
	assert.Equal(t, 1, activated)
	after := f.subscription(t)
	assert.Equal(t, &source.ID, after.ProviderSourceID)
	assert.Equal(t, before.Token, after.Token)
	assert.Equal(t, before.ClientID, after.ClientID)
	assert.Equal(t, expiry.AddDate(0, 0, 30), *after.ExpiresAt)
	var count int64
	require.NoError(t, f.db.GetDB().Model(&database.Subscription{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.NoError(t, f.db.GetDB().Model(&database.TelegramPaymentReceipt{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.NoError(t, f.db.GetDB().Model(&database.SubscriptionNode{}).Count(&count).Error)
	assert.Zero(t, count, "provider-backed subscription must not get legacy nodes")
	require.Error(t, f.stars.PreCheckout(ctx, f.query()))
	_, err := f.stars.Invoice(ctx, f.purchases, f.ref)
	require.Error(t, err)
	second := f.paid()
	second.Message.SuccessfulPayment.TelegramPaymentChargeID = "another-charge"
	_, err = f.stars.SuccessfulPayment(ctx, second)
	require.Error(t, err)
	assert.Equal(t, after, f.subscription(t))
}

func TestStarsSettlement_RollbackAndDurableRecovery(t *testing.T) {
	f := newStarsFixture(t)
	ctx := context.Background()
	free, err := f.db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	require.NoError(t, f.db.GetDB().Model(f.sub).Update("plan_id", free.ID).Error)
	node := &database.Node{Name: "stars-node", Type: database.NodeType3xUI, Host: "https://unused.example", IsActive: true}
	require.NoError(t, f.db.GetDB().Create(node).Error)
	require.NoError(t, f.db.GetDB().Create(&database.PlanNode{PlanID: f.product.PlanID, NodeID: node.ID}).Error)
	f.invoice(t)
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	before := f.subscription(t)
	// Fail a real DB prerequisite, not a mocked settlement/CAS.
	require.NoError(t, f.db.GetDB().Exec(`CREATE TRIGGER stars_test_fail BEFORE INSERT ON subscription_nodes BEGIN SELECT RAISE(ABORT, 'injected DB setup failure'); END`).Error)
	_, err = f.stars.SuccessfulPayment(ctx, f.paid())
	require.ErrorContains(t, err, "injected DB setup failure")
	assert.Equal(t, before, f.subscription(t))
	record, err := f.db.ReadPurchaseOrder(ctx, f.sub.TelegramID, f.ref)
	require.NoError(t, err)
	assert.Equal(t, database.OrderStatusPending, record.Order.Status)
	assert.Empty(t, record.Order.ProviderPaymentID)
	assert.Nil(t, record.Order.ActivatedAt)
	receipts, err := f.db.PendingTelegramPayments(ctx, 0, 50)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.NoError(t, f.db.GetDB().Exec(`DROP TRIGGER stars_test_fail`).Error)
	// A new service instance represents process restart. No Telegram redelivery needed.
	recovered := NewStarsPaymentService(NewOrderService(f.db, nil, NewSyncService(f.db, nil, nil), nil, "", nil), f.client)
	require.NoError(t, recovered.RetryPayments(ctx))
	after := f.subscription(t)
	assert.Equal(t, f.product.PlanID, after.PlanID)
	assert.Equal(t, "active", after.Status)
	require.NotNil(t, after.ExpiresAt)
	nodes, err := f.db.GetBySubscriptionID(ctx, after.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, database.SyncStatusPendingAdd, nodes[0].Status, "external sync failure retains durable node prerequisites")
	require.NoError(t, recovered.RetryPayments(ctx))
	assert.Equal(t, after, f.subscription(t))
	receipts, err = f.db.PendingTelegramPayments(ctx, 0, 50)
	require.NoError(t, err)
	assert.Empty(t, receipts)
}

func TestStarsSettlement_LateApprovedPayment(t *testing.T) {
	f := newStarsFixture(t)
	ctx := context.Background()
	require.NoError(t, f.db.GetDB().Model(f.product).Update("offer_ends_at", time.Now().UTC().Add(2*time.Second)).Error)
	f.invoice(t)
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	require.NoError(t, f.stars.CaptureUpdate(ctx, f.paid()))
	time.Sleep(2100 * time.Millisecond)
	info, err := f.purchases.Order(ctx, f.ref)
	require.NoError(t, err)
	assert.Equal(t, database.OrderStatusExpired, info.Status)
	require.Error(t, f.stars.PreCheckout(ctx, f.query()))
	require.NoError(t, f.db.GetDB().Model(f.product).Update("is_active", false).Error)
	require.NoError(t, f.stars.RetryPayments(ctx), "approved paid entitlement survives offer deactivation/intent expiry")
	assert.Equal(t, "active", f.subscription(t).Status)
	require.NotNil(t, f.subscription(t).ExpiresAt)
}

func TestStarsInvoice_FailureRetryAndLegacyIsolation(t *testing.T) {
	f := newStarsFixture(t)
	ctx := context.Background()
	info, _, err := f.purchases.Create(ctx, f.product.OfferID, uuid.NewString())
	require.NoError(t, err)
	f.client.invoiceErr = errors.New("network unavailable")
	_, err = f.stars.Invoice(ctx, f.purchases, info.OrderID)
	require.Error(t, err)
	f.client.invoiceErr = nil
	_, err = f.stars.Invoice(ctx, f.purchases, info.OrderID)
	require.NoError(t, err)
	assert.Equal(t, 2, f.client.invoices)
	// Legacy request must not send XTR to its provider.
	orders := NewOrderService(f.db, nil, NewSyncService(f.db, nil, nil), fakePaymentProvider{}, "", nil)
	_, _, err = orders.RequestPayment(ctx, f.sub.TelegramID, "", f.product)
	require.ErrorIs(t, err, ErrPaymentDisabled)
	p, db, sub, product := purchaseServiceFixture(t)
	legacyStars := NewStarsPaymentService(NewOrderService(db, nil, NewSyncService(db, nil, nil), nil, "", nil), &starsTestClient{})
	legacy, _, err := p.Create(ctx, product.OfferID, uuid.NewString())
	require.NoError(t, err)
	_, err = legacyStars.Invoice(ctx, p, legacy.OrderID)
	require.ErrorIs(t, err, database.ErrStarsPurchaseInvalid)
	unchanged, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Nil(t, unchanged.ExpiresAt)
}

func TestStarsPreCheckout_DBAndAnswerFailures(t *testing.T) {
	f := newStarsFixture(t)
	f.invoice(t)
	ctx := context.Background()
	before := f.subscription(t)
	require.NoError(t, f.db.GetDB().Exec(`CREATE TRIGGER stars_test_fail BEFORE UPDATE ON orders BEGIN SELECT RAISE(ABORT, 'checkout database unavailable'); END`).Error)
	require.ErrorContains(t, f.stars.PreCheckout(ctx, f.query()), "checkout database unavailable")
	assert.False(t, f.client.approved)
	require.NoError(t, f.db.GetDB().Exec(`DROP TRIGGER stars_test_fail`).Error)
	f.client.answerErr = errors.New("answer network failure")
	require.ErrorContains(t, f.stars.PreCheckout(ctx, f.query()), "answer network failure")
	assert.Equal(t, before, f.subscription(t))
	f.client.answerErr = nil
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()), "same query can be answered again after timeout")
	assert.Equal(t, before, f.subscription(t))
}

func TestStarsSettlement_ExpiredSubscriptionAndUnapprovedExpiredOrder(t *testing.T) {
	f := newStarsFixture(t)
	ctx := context.Background()
	oldExpiry := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, f.db.GetDB().Model(f.sub).Updates(map[string]any{"status": "expired", "expires_at": oldExpiry}).Error)
	f.invoice(t)
	f.state(t, database.OrderStatusExpired)
	before := f.subscription(t)
	_, err := f.stars.SuccessfulPayment(ctx, f.paid())
	require.Error(t, err, "expired purchase without approved checkout must not activate")
	assert.Equal(t, before, f.subscription(t))
	// A fresh approved order activates through the same existing lifecycle.
	f.invoice(t)
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	paid := f.paid()
	paid.Message.SuccessfulPayment.TelegramPaymentChargeID = "fresh-charge"
	result, err := f.stars.SuccessfulPayment(ctx, paid)
	require.NoError(t, err)
	assert.True(t, result.Activated)
	after := f.subscription(t)
	assert.Equal(t, "active", after.Status)
	assert.WithinDuration(t, time.Now().UTC().AddDate(0, 0, 30), *after.ExpiresAt, 5*time.Second)
	assert.Equal(t, before.ID, after.ID)
}

func TestStarsSettlement_ChargeCannotPayAnotherPurchase(t *testing.T) {
	f := newStarsFixture(t)
	f.invoice(t)
	ctx := context.Background()
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	_, err := f.stars.SuccessfulPayment(ctx, f.paid())
	require.NoError(t, err)
	after := f.subscription(t)
	f.invoice(t) // another real purchase, after the first was paid
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	_, err = f.stars.SuccessfulPayment(ctx, f.paid()) // same charge, new payload
	require.ErrorIs(t, err, database.ErrStarsPurchaseInvalid)
	assert.Equal(t, after, f.subscription(t))
	info, err := f.purchases.Order(ctx, f.ref)
	require.NoError(t, err)
	assert.Equal(t, database.OrderStatusPending, info.Status)
}

func TestStarsRetry_InvalidReceiptDoesNotStarveNextPage(t *testing.T) {
	f := newStarsFixture(t)
	f.invoice(t)
	ctx := context.Background()
	require.NoError(t, f.stars.PreCheckout(ctx, f.query()))
	for i := 0; i < 51; i++ {
		require.NoError(t, f.db.RecordTelegramPayment(ctx, &database.TelegramPaymentReceipt{TelegramChargeID: uuid.NewString(), PayerTelegramID: f.sub.TelegramID, InvoicePayload: "invalid", Currency: "XTR", TotalAmount: 75}))
	}
	require.NoError(t, f.stars.CaptureUpdate(ctx, f.paid()))
	require.Error(t, f.stars.RetryPayments(ctx), "reports bad receipts but still processes later pages")
	info, err := f.purchases.Order(ctx, f.ref)
	require.NoError(t, err)
	assert.Equal(t, database.OrderStatusPaid, info.Status)
	after := f.subscription(t)
	require.Error(t, f.stars.RetryPayments(ctx))
	assert.Equal(t, after, f.subscription(t))
}

func TestStarsCapture_PermanentInvalidUpdatesDoNotBlockPolling(t *testing.T) {
	f := newStarsFixture(t)
	f.invoice(t)
	ctx := context.Background()
	malformed := f.paid()
	malformed.Message.SuccessfulPayment.TelegramPaymentChargeID = ""
	require.NoError(t, f.stars.CaptureUpdate(ctx, malformed), "permanent invalid update must not wedge the polling offset")
	_, err := f.stars.SuccessfulPayment(ctx, malformed)
	require.ErrorIs(t, err, database.ErrStarsPurchaseInvalid)
	require.NoError(t, f.stars.CaptureUpdate(ctx, f.paid()))
	conflict := f.paid()
	conflict.Message.SuccessfulPayment.TotalAmount++
	require.NoError(t, f.stars.CaptureUpdate(ctx, conflict))
	_, err = f.stars.SuccessfulPayment(ctx, conflict)
	require.ErrorIs(t, err, database.ErrStarsPurchaseInvalid)
}
