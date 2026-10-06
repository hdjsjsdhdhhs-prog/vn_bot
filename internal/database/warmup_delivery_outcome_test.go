package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Warm-up delivery outcomes used by the worker (Stage 2).

func TestWarmupRetryPolicy_BackoffAfter(t *testing.T) {
	t.Parallel()
	p := WarmupRetryPolicy{MaxAttempts: 5, Backoff: 10 * time.Minute, MaxBackoff: 30 * time.Minute}
	assert.Equal(t, 10*time.Minute, p.BackoffAfter(1))
	assert.Equal(t, 20*time.Minute, p.BackoffAfter(2))
	assert.Equal(t, 30*time.Minute, p.BackoffAfter(3), "capped")
	assert.Equal(t, 30*time.Minute, p.BackoffAfter(10))
	assert.Equal(t, 40*time.Minute, WarmupRetryPolicy{Backoff: 10 * time.Minute}.BackoffAfter(3), "no cap")
}

func TestWarmupDeliveries_ExponentialReclaim(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sc := warmupScenarioByKey(t, svc, "winback_3d")
	policy := WarmupRetryPolicy{MaxAttempts: 3, Backoff: 10 * time.Minute}
	now := time.Now().UTC()

	first, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 959001, "a", nil), policy, now)
	require.NoError(t, err)
	require.NoError(t, svc.ReleaseWarmupDelivery(ctx, first.Delivery.ID, "net", false, now))
	second, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 959001, "a", nil), policy, now.Add(11*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, second)
	released := now.Add(12 * time.Minute)
	require.NoError(t, svc.ReleaseWarmupDelivery(ctx, second.Delivery.ID, "net", false, released))

	early, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 959001, "a", nil), policy, released.Add(15*time.Minute))
	require.NoError(t, err)
	assert.Nil(t, early, "after the second attempt the backoff doubles to 20 minutes")
	third, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 959001, "a", nil), policy, released.Add(21*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, third)
	assert.Equal(t, 3, third.Delivery.Attempts)
}

func TestWarmupDeliveries_StatusUnreachableAndExpiredRetries(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sc := warmupScenarioByKey(t, svc, "winback_7d")
	policy := WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}
	now := time.Now().UTC()

	status, err := svc.WarmupDeliveryStatus(ctx, sc.ID, 960001, "a")
	require.NoError(t, err)
	assert.Empty(t, status)

	// Unreachable: failed, coupon revoked, the user excluded.
	unreachable, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 960001, "a", &WarmupCouponDraft{Code: "UNR-1", Percent: 5, ExpiresAt: now.Add(time.Hour)}), policy, now)
	require.NoError(t, err)
	status, err = svc.WarmupDeliveryStatus(ctx, sc.ID, 960001, "a")
	require.NoError(t, err)
	assert.Equal(t, WarmupDeliverySending, status)
	require.NoError(t, svc.MarkWarmupDeliveryUnreachable(ctx, unreachable.Delivery.ID, "Bad Request: chat not found", now))
	status, err = svc.WarmupDeliveryStatus(ctx, sc.ID, 960001, "a")
	require.NoError(t, err)
	assert.Equal(t, WarmupDeliveryFailed, status)
	facts, err := svc.GetWarmupUserFacts(ctx, 960001, now)
	require.NoError(t, err)
	assert.True(t, facts.Blocked, "an unreachable chat is excluded from further sends")
	coupons, _, err := svc.ListWarmupCoupons(ctx, WarmupCouponFilter{TelegramID: 960001})
	require.NoError(t, err)
	assert.Equal(t, WarmupCouponRevoked, coupons[0].Status)
	assert.ErrorIs(t, svc.MarkWarmupDeliveryBlocked(ctx, unreachable.Delivery.ID, "x", now), ErrWarmupDeliveryStale)
	assert.ErrorIs(t, svc.MarkWarmupDeliveryBlocked(ctx, 99999, "x", now), ErrWarmupDeliveryNotFound)

	// A retry whose event left the catch-up window fails for good.
	oldClaim := warmupTestClaim(sc, 960002, "old", &WarmupCouponDraft{Code: "OLD-1", Percent: 5, ExpiresAt: now.Add(time.Hour)})
	oldClaim.DueAt = now.Add(-7 * time.Hour)
	old, err := svc.ClaimWarmupDelivery(ctx, oldClaim, policy, now.Add(-7*time.Hour))
	require.NoError(t, err)
	require.NoError(t, svc.ReleaseWarmupDelivery(ctx, old.Delivery.ID, "Too Many Requests", false, now.Add(-7*time.Hour)))
	fresh, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 960003, "new", nil), policy, now)
	require.NoError(t, err)
	require.NoError(t, svc.ReleaseWarmupDelivery(ctx, fresh.Delivery.ID, "net", false, now))

	expired, err := svc.ExpireWarmupRetries(ctx, now.Add(-6*time.Hour), now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, expired)
	rows, _, err := svc.ListWarmupDeliveries(ctx, WarmupDeliveryFilter{TelegramID: 960002})
	require.NoError(t, err)
	assert.Equal(t, WarmupDeliveryFailed, rows[0].Status)
	assert.Equal(t, "retry window expired: Too Many Requests", rows[0].Error)
	coupons, _, err = svc.ListWarmupCoupons(ctx, WarmupCouponFilter{TelegramID: 960002})
	require.NoError(t, err)
	assert.Equal(t, WarmupCouponRevoked, coupons[0].Status)
	rows, _, err = svc.ListWarmupDeliveries(ctx, WarmupDeliveryFilter{TelegramID: 960003})
	require.NoError(t, err)
	assert.Equal(t, WarmupDeliveryRetry, rows[0].Status, "a retry inside the window keeps waiting")
}
