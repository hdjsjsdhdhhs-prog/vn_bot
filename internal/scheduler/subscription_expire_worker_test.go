package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func newTestSubServiceForExpire(t testing.TB, db *database.Service) *service.SubscriptionService {
	t.Helper()

	cfg := &config.Config{
		TrialDurationHours: 1,
	}

	return service.NewSubscriptionService(db, cfg)
}

func TestSubscriptionExpireWorker_process_FindsAndExpires(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	// The test DSN enables SQLite foreign-key enforcement: ProductID must
	// reference an existing product row.
	product := &database.Product{PlanID: plan.ID, Name: "expire product", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(product).Error)

	expiredSub := &database.Subscription{
		TelegramID:     99991,
		Username:       "expireuser1",
		ClientID:       "c-expire1",
		SubscriptionID: "s-expire1",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
		PricePaidCents: 100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      &product.ID,
		StartedAt:      testutil.PtrTime(time.Now().Add(-48 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, expiredSub, ""))

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var updated database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&updated, expiredSub.ID).Error)
	assert.Equal(t, "active", updated.Status)
	assert.Equal(t, plan.ID, updated.PlanID)
}

func TestSubscriptionExpireWorker_process_EmptyResult(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	product := &database.Product{PlanID: plan.ID, Name: "noexpire product", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(product).Error)

	activeSub := &database.Subscription{
		TelegramID:     99992,
		Username:       "noexpire",
		ClientID:       "c-noexpire",
		SubscriptionID: "s-noexpire",
		Status:         "active",
		PlanID:         plan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
		PricePaidCents: 100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      &product.ID,
		StartedAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, activeSub, ""))

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var unchanged database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&unchanged, activeSub.ID).Error)
	assert.Equal(t, "active", unchanged.Status)
	assert.False(t, unchanged.ExpiresAt == nil)
}

func TestSubscriptionExpireWorker_Run_ContextCancel(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	subService := newTestSubServiceForExpire(t, db)
	worker := NewSubscriptionExpireWorker(db, subService)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		worker.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SubscriptionExpireWorker.Run should stop after context cancel")
	}
}

func TestSubscriptionExpireWorker_process_PaidPlanExpires_DowngradesToFree(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	freePlan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr)

	paidPlan := &database.Plan{Name: "paid-plan-expire-e2e"}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(paidPlan).Error)

	product := &database.Product{PlanID: paidPlan.ID, Name: "paid expire product", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(product).Error)

	sub := &database.Subscription{
		TelegramID:     99994,
		Username:       "expirepaiduser",
		ClientID:       "c-expire-paid",
		SubscriptionID: "s-expire-paid",
		Status:         "active",
		PlanID:         paidPlan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Minute)),
		PricePaidCents: 100,
		Currency:       testutil.PtrString("RUB"),
		ProductID:      &product.ID,
		StartedAt:      testutil.PtrTime(time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Minute)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	var (
		invalidateCalled bool
		invalidateSubID  string
	)

	subService := service.NewSubscriptionService(db, &config.Config{TrialDurationHours: 1})
	subService.SetInvalidateBySubIDFunc(func(id string) {
		invalidateCalled = true
		invalidateSubID = id
	})

	worker := NewSubscriptionExpireWorker(db, subService)

	worker.process(ctx)

	var updated database.Subscription
	require.NoError(t, db.GetDB().WithContext(ctx).First(&updated, sub.ID).Error)
	assert.Equal(t, freePlan.ID, updated.PlanID, "subscription should be downgraded to free plan")
	assert.Equal(t, "active", updated.Status)
	assert.Nil(t, updated.ExpiresAt, "the free plan is perpetual")
	assert.Nil(t, updated.ProductID)

	assert.True(t, invalidateCalled, "cache invalidation should be called")
	assert.Equal(t, sub.SubscriptionID, invalidateSubID)
}
