package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrderByID_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 300, "user3", "client-order-3")
	plan := &Plan{Name: "plan-order-get"}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    100,
		Currency:       "RUB",
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	got, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, got.ID)
	assert.Equal(t, OrderStatusPending, got.Status)
	assert.Equal(t, sub.ID, got.SubscriptionID)
	assert.Equal(t, product.ID, got.ProductID)
}

func TestGetOrderByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetOrderByID(ctx, 99999)
	assert.ErrorIs(t, err, ErrOrderNotFound)
}

func TestSaveOrderPaymentAmounts(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 301, "user-payment-amounts", "client-payment-amounts")
	plan := &Plan{Name: "plan-order-amounts"}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPaid,
		AmountCents:    5000,
		Currency:       "RUB",
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	// First call stores the callback amount; the fee stays NULL.
	require.NoError(t, svc.SaveOrderPaymentAmounts(ctx, order.ID, 5250, nil))

	got, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CallbackAmountCents)
	assert.Equal(t, int64(5250), *got.CallbackAmountCents)
	assert.Nil(t, got.ProviderFeeCents)

	// Best-effort follow-up call adds the provider fee without touching the amount.
	require.NoError(t, svc.SaveOrderPaymentAmounts(ctx, order.ID, 5250, ptrInt64(450)))

	got, err = svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CallbackAmountCents)
	assert.Equal(t, int64(5250), *got.CallbackAmountCents)
	require.NotNil(t, got.ProviderFeeCents)
	assert.Equal(t, int64(450), *got.ProviderFeeCents)

	// A nil fee on a later call must not overwrite the stored commission with NULL.
	require.NoError(t, svc.SaveOrderPaymentAmounts(ctx, order.ID, 5250, nil))

	got, err = svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CallbackAmountCents)
	assert.Equal(t, int64(5250), *got.CallbackAmountCents)
	require.NotNil(t, got.ProviderFeeCents, "stored commission must survive a nil update")
	assert.Equal(t, int64(450), *got.ProviderFeeCents)
}

func TestGetPaidOrdersWithoutProviderFee(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 302, "user-provider-fee-queue", "client-provider-fee-queue")
	plan := &Plan{Name: "plan-provider-fee-queue"}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	fee := int64(10)
	orders := []*Order{
		{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "paid-without-fee"},
		{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "paid-with-fee", ProviderFeeCents: &fee},
		{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 100, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "pending-without-fee"},
		{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 100, Currency: "RUB", PaymentProvider: "other", ProviderPaymentID: "other-provider"},
	}
	for _, order := range orders {
		require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)
	}

	got, err := svc.GetPaidOrdersWithoutProviderFee(ctx, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "paid-without-fee", got[0].ProviderPaymentID)

	got, err = svc.GetPaidOrdersWithoutProviderFeeAfterID(ctx, got[0].ID, 10)
	require.NoError(t, err)
	assert.Empty(t, got, "paging after the only eligible order must not return it again")
}

func TestConfirmOrderPaidCAS_PersistsCallbackAmount(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "plan-cas-callback-amount"}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	sub := createTestSubscription(t, svc, 905, "cas-callback-amount", "client-cas-callback-amount")
	sub.PlanID = plan.ID
	require.NoError(t, svc.db.Save(sub).Error)

	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 5000, Currency: "RUB"}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC().Truncate(time.Second)
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, 5250)
	require.NoError(t, err)
	require.True(t, activated)

	stored, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.CallbackAmountCents, "callback amount must be persisted in the same transaction as the paid transition")
	assert.Equal(t, int64(5250), *stored.CallbackAmountCents)
}

func TestSaveOrderPaymentAmounts_UnknownOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	err := svc.SaveOrderPaymentAmounts(context.Background(), 99999, 5250, nil)
	require.NoError(t, err)
}

func TestOrderStatusConstants(t *testing.T) {
	assert.Equal(t, OrderStatus("pending"), OrderStatusPending)
	assert.Equal(t, OrderStatus("paid"), OrderStatusPaid)
	assert.Equal(t, OrderStatus("expired"), OrderStatusExpired)
	assert.Equal(t, OrderStatus("canceled"), OrderStatusCanceled)
}

func TestCalculatePaymentExpiry_SamePlanExtends(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &Product{PlanID: 1, DurationDays: 30}

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: &oldExpiry}, product)
	assert.Equal(t, oldExpiry.AddDate(0, 0, 30), result)
}

func TestCalculatePaymentExpiry_NilExpiryUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	product := &Product{PlanID: 1, DurationDays: 30}

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: nil}, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCalculatePaymentExpiry_DifferentPlanUsesNow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)
	product := &Product{PlanID: 2, DurationDays: 30}

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: &oldExpiry}, product)
	assert.Equal(t, now.AddDate(0, 0, 30), result)
}

func TestCalculatePaymentExpiry_NilProductUsesBase(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	oldExpiry := now.Add(10 * 24 * time.Hour)

	result := CalculatePaymentExpiry(now, &Subscription{PlanID: 1, ExpiresAt: &oldExpiry}, nil)
	assert.Equal(t, now, result)
}

func TestConfirmOrderPaidCAS_RecalculatesFromCurrentSubscription(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	plan := &Plan{Name: "plan-current-expiry"}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	currentExpiry := now.Add(10 * 24 * time.Hour)
	sub := createTestSubscription(t, svc, 1901, "current-expiry", "client-current-expiry")
	sub.PlanID = plan.ID
	sub.ExpiresAt = &currentExpiry
	require.NoError(t, svc.db.Save(sub).Error)
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending, AmountCents: 100, Currency: "RUB"}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	// The CAS must recompute expiry from the current subscription, not from any
	// stale caller snapshot: the expired-at value passed in is irrelevant.
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, &Subscription{ID: sub.ID, PlanID: plan.ID}, product, order.AmountCents)
	require.NoError(t, err)
	assert.True(t, activated)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ExpiresAt)
	assert.Equal(t, currentExpiry.AddDate(0, 0, 30), *got.ExpiresAt)
}

func TestConfirmOrderPaidCAS_AcceptsExpiredOrderForSettlementCallback(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "plan-cas-expired-callback"}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	sub := createTestSubscription(t, svc, 902, "expired-callback", "client-expired-callback")
	sub.PlanID = plan.ID
	require.NoError(t, svc.db.Save(sub).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusExpired,
		AmountCents:    100,
		Currency:       "RUB",
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC().Truncate(time.Second)
	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, order.AmountCents)
	require.NoError(t, err)
	require.True(t, activated, "the service grace-period check allows an expired order to enter the atomic paid transition")

	stored, err := svc.GetOrderByID(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, OrderStatusPaid, stored.Status)
}

func TestConfirmOrderPaidCAS_SwitchesPlanAndStatus(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()

	// trial plan + paid plan; sub starts on trial with expired status.
	trialPlan := &Plan{Name: "plan-cas-trial"}
	require.NoError(t, svc.db.WithContext(ctx).Create(trialPlan).Error)

	paidPlan := &Plan{Name: "plan-cas-premium"}
	require.NoError(t, svc.db.WithContext(ctx).Create(paidPlan).Error)
	product := &Product{PlanID: paidPlan.ID, Name: "1M", DurationDays: 30, PriceCents: 499, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	sub := createTestSubscription(t, svc, 900, "usercas", "client-cas")
	sub.PlanID = trialPlan.ID
	sub.Status = "expired"
	require.NoError(t, svc.db.Save(sub).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    499,
		Currency:       "RUB",
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC().Truncate(time.Second)

	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, order.AmountCents)
	require.NoError(t, err)
	assert.True(t, activated)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, paidPlan.ID, got.PlanID, "subscription must switch to the purchased plan")
	assert.Equal(t, string(SubscriptionStatusActive), got.Status, "payment must reactivate the subscription")
	assert.Equal(t, product.ID, *got.ProductID)

	// Idempotent retry: order already paid, no second activation.
	activated, err = svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, order.AmountCents)
	require.NoError(t, err)
	assert.False(t, activated)
}

// TestConfirmOrderPaidCAS_SamePlanRenewalExtendsExpiry locks the renewal
// contract: paying for the plan the subscription already has keeps the plan
// and extends the existing future expiry.
func TestConfirmOrderPaidCAS_SamePlanRenewalExtendsExpiry(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "plan-renew"}
	require.NoError(t, svc.db.WithContext(ctx).Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "1M", DurationDays: 30, PriceCents: 499, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.WithContext(ctx).Create(product).Error)

	expiresAt := time.Now().UTC().Add(10 * 24 * time.Hour)
	sub := createTestSubscription(t, svc, 901, "userrenew", "client-renew")
	sub.PlanID = plan.ID
	sub.Status = "active"
	sub.ExpiresAt = &expiresAt
	require.NoError(t, svc.db.Save(sub).Error)

	order := &Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPending,
		AmountCents:    499,
		Currency:       "RUB",
	}
	require.NoError(t, svc.db.WithContext(ctx).Create(order).Error)

	now := time.Now().UTC().Truncate(time.Second)

	activated, err := svc.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, product, order.AmountCents)
	require.NoError(t, err)
	assert.True(t, activated)

	got, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, plan.ID, got.PlanID)
	require.NotNil(t, got.ExpiresAt)
	// Renewal extends the existing future expiry by the product duration.
	assert.True(t, got.ExpiresAt.After(expiresAt), "renewal must extend the existing expiry")
}
