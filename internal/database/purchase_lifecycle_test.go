package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func purchaseFixture(t *testing.T) (*Service, *Subscription, *Product, PurchaseValidator) {
	t.Helper()
	svc := newTestService(t)
	sub := createTestSubscription(t, svc, 43001, "purchase-policy", "purchase-policy-client")
	plan := &Plan{Name: "purchase-policy", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Monthly", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	validate := func(c *PurchaseCatalog, p *Product) (time.Time, error) { return c.Now.Add(30 * time.Minute), nil }
	return svc, sub, product, validate
}

func TestPurchasePendingPaymentLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		deadline  *time.Time
		uncertain bool
		provider  string
		allowed   bool
	}{
		{"stale", ptrTime(time.Now().Add(-10 * time.Minute)), false, "platega", true},
		// Existing lifecycle expires the link, not its callback settlement window.
		{"inside callback grace", ptrTime(time.Now().Add(-time.Minute)), false, "platega", true},
		{"valid", ptrTime(time.Now().Add(time.Hour)), false, "platega", false},
		{"no deadline", nil, false, "platega", false},
		{"uncertain stale", ptrTime(time.Now().Add(-time.Hour)), true, "platega", false},
		{"uncertain no deadline", nil, true, "platega", false},
		{"unknown provider", ptrTime(time.Now().Add(-time.Hour)), false, "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, sub, product, validate := purchaseFixture(t)
			old := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending,
				AmountCents: product.PriceCents, Currency: product.Currency, PaymentProvider: tc.provider,
				PaymentExpiresAt: tc.deadline, PaymentCreationUncertain: tc.uncertain, CreatedAt: time.Now().Add(-time.Hour)}
			require.NoError(t, svc.db.Create(old).Error)
			// Compare persisted snapshots, not time.Time's monotonic/local-zone metadata.
			old, err := svc.GetOrderByID(context.Background(), old.ID)
			require.NoError(t, err)
			record, created, err := svc.CreatePurchaseOrder(context.Background(), sub.TelegramID, product.OfferID, uuid.NewString(), validate)
			if tc.allowed {
				require.NoError(t, err)
				require.True(t, created)
				require.NotEqual(t, old.ID, record.Order.ID)
			} else {
				require.ErrorIs(t, err, ErrPurchasePending)
				assert.False(t, created)
				assert.Nil(t, record)
			}
			stored, err := svc.GetOrderByID(context.Background(), old.ID)
			require.NoError(t, err)
			expected := *old
			if tc.allowed {
				expected.Status = OrderStatusExpired
			}
			assert.Equal(t, expected, *stored, "only lifecycle status may change; callback deadline and uncertainty must survive")
			orders, err := svc.GetOrdersBySubscriptionID(context.Background(), sub.ID)
			require.NoError(t, err)
			if tc.allowed {
				assert.Len(t, orders, 2)
			} else {
				assert.Len(t, orders, 1)
			}
		})
	}
}

func TestPurchaseAttachedPaymentKeepsProviderLifecycle(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "valid payment link"
		if uncertain {
			name = "uncertain payment"
		}
		t.Run(name, func(t *testing.T) {
			svc, sub, product, validate := purchaseFixture(t)
			ctx := context.Background()
			ref, key := strings.ReplaceAll(uuid.NewString(), "-", ""), uuid.NewString()
			intentDeadline := time.Now().UTC().Add(-time.Hour)
			paymentDeadline := time.Now().UTC().Add(time.Hour)
			if uncertain {
				paymentDeadline = intentDeadline
			}
			old := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending,
				AmountCents: product.PriceCents, Currency: product.Currency, CreatedAt: intentDeadline,
				PurchaseID: &ref, BuyerTelegramID: &sub.TelegramID, PurchaseKey: &key, PurchaseExpiresAt: &intentDeadline,
				PaymentProvider: "platega", PaymentExpiresAt: &paymentDeadline, PaymentCreationUncertain: uncertain}
			require.NoError(t, svc.db.Create(old).Error)
			read, err := svc.ReadPurchaseOrder(ctx, sub.TelegramID, ref)
			require.NoError(t, err)
			assert.Equal(t, OrderStatusPending, read.Order.Status, "purchase TTL must not expire an attached payment")
			_, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, uuid.NewString(), validate)
			require.ErrorIs(t, err, ErrPurchasePending)
			assert.False(t, created)
			replay, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, validate)
			require.NoError(t, err)
			assert.False(t, created)
			assert.Equal(t, read.Order, replay.Order)
		})
	}
}

func TestPurchaseIdempotencyAndReplay(t *testing.T) {
	for _, status := range []OrderStatus{OrderStatusPending, OrderStatusPaid, OrderStatusExpired, OrderStatusCanceled} {
		t.Run(string(status), func(t *testing.T) {
			svc, sub, product, validate := purchaseFixture(t)
			ctx := context.Background()
			key := uuid.NewString()
			first, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, validate)
			require.NoError(t, err)
			require.True(t, created)
			require.NoError(t, svc.db.Model(&Order{}).Where("id = ?", first.Order.ID).Update("status", status).Error)
			first.Order.Status = status
			require.NoError(t, svc.db.Model(product).Update("is_active", false).Error)
			replay, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, func(*PurchaseCatalog, *Product) (time.Time, error) {
				t.Error("replay must not revalidate disabled offer")
				return time.Time{}, ErrProductNotFound
			})
			require.NoError(t, err)
			assert.False(t, created)
			assert.Equal(t, first.Order, replay.Order)
			other := &Product{PlanID: product.PlanID, Name: "Other", DurationDays: 60, PriceCents: 9000, Currency: "RUB", IsActive: true}
			require.NoError(t, svc.db.Create(other).Error)
			_, created, err = svc.CreatePurchaseOrder(ctx, sub.TelegramID, other.OfferID, key, validate)
			require.ErrorIs(t, err, ErrPurchaseKeyConflict)
			assert.False(t, created)
			require.NoError(t, svc.db.Model(product).Update("is_active", true).Error)
			next, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, uuid.NewString(), validate)
			if status == OrderStatusPending {
				require.ErrorIs(t, err, ErrPurchasePending)
				assert.False(t, created)
				assert.Nil(t, next)
			} else {
				require.NoError(t, err)
				assert.True(t, created)
				assert.NotEqual(t, first.Order.ID, next.Order.ID)
			}
		})
	}
}

func TestPurchaseConcurrentSubmissions(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		name := "different keys"
		if sameKey {
			name = "same key"
		}
		t.Run(name, func(t *testing.T) {
			svc, sub, product, validate := purchaseFixture(t)
			// Exercise separate SQLite connections, not just the single-connection queue.
			sqlDB, err := svc.db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(8)
			const workers = 8
			type result struct {
				record  *PurchaseRecord
				created bool
				err     error
			}
			results := make(chan result, workers)
			start := make(chan struct{})
			key := uuid.NewString()
			for range workers {
				workerKey := key
				if !sameKey {
					workerKey = uuid.NewString()
				}
				go func() {
					<-start
					r, c, e := svc.CreatePurchaseOrder(context.Background(), sub.TelegramID, product.OfferID, workerKey, validate)
					results <- result{r, c, e}
				}()
			}
			close(start)
			createdCount := 0
			var id uint
			for range workers {
				r := <-results
				if sameKey || r.created {
					require.NoError(t, r.err)
					require.NotNil(t, r.record)
					if id == 0 {
						id = r.record.Order.ID
					}
					assert.Equal(t, id, r.record.Order.ID)
				} else {
					require.ErrorIs(t, r.err, ErrPurchasePending)
					assert.Nil(t, r.record)
				}
				if r.created {
					createdCount++
				}
			}
			assert.Equal(t, 1, createdCount)
			orders, err := svc.GetOrdersBySubscriptionID(context.Background(), sub.ID)
			require.NoError(t, err)
			require.Len(t, orders, 1)
			assert.Equal(t, OrderStatusPending, orders[0].Status)
		})
	}
}

func TestPurchaseExpiredIntentAndOwnership(t *testing.T) {
	svc, sub, product, _ := purchaseFixture(t)
	ctx := context.Background()
	expired := func(c *PurchaseCatalog, p *Product) (time.Time, error) { return c.Now.Add(-time.Minute), nil }
	key := uuid.NewString()
	first, _, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, expired)
	require.NoError(t, err)
	replay, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, expired)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, OrderStatusExpired, replay.Order.Status)
	assert.Equal(t, first.Order.ID, replay.Order.ID)
	_, created, err = svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, uuid.NewString(), expired)
	require.NoError(t, err)
	assert.True(t, created)
	require.NoError(t, svc.db.Model(sub).Update("telegram_id", sub.TelegramID+1).Error)
	_, err = svc.ReadPurchaseOrder(ctx, 43001, *first.Order.PurchaseID)
	require.ErrorIs(t, err, ErrOrderNotFound)
	_, err = svc.ReadPurchaseOrder(ctx, 43002, *first.Order.PurchaseID)
	require.ErrorIs(t, err, ErrOrderNotFound)
	// Even if the original buyer now owns another subscription, replay cannot
	// expose the order that belongs to the transferred subscription.
	createTestSubscription(t, svc, 43001, "new-owner", "new-owner-client")
	_, _, err = svc.CreatePurchaseOrder(ctx, 43001, product.OfferID, key, expired)
	require.ErrorIs(t, err, ErrOrderNotFound)
}
