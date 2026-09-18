package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionRenewal_OnlyLifecycleFieldsChange(t *testing.T) {
	for _, provider := range []bool{false, true} {
		for _, status := range []string{"active", "expired"} {
			name := "legacy/" + status
			if provider {
				name = "provider/" + status
			}
			t.Run(name, func(t *testing.T) {
				db := newTestService(t)
				ctx := context.Background()
				plan := &Plan{Name: "renewal", IsActive: true, TrafficLimit: 1024}
				require.NoError(t, db.db.Create(plan).Error)
				product := &Product{PlanID: plan.ID, Name: "original purchase", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
				require.NoError(t, db.db.Create(product).Error)
				sub := newProviderSourceTestSubscription(t, db, 13001, "renewal")
				sub.PlanID, sub.Status = plan.ID, status
				sub.ProductID, sub.PricePaidCents = &product.ID, 100
				currency := "RUB"
				sub.Currency = &currency
				sub.StartedAt = ptrTime(time.Now().UTC().Add(-24 * time.Hour))
				sub.RemindersSent, sub.TrafficRemindersSent = 7, 3
				if status == "expired" {
					sub.ExpiresAt = ptrTime(time.Now().UTC().Add(-time.Hour))
				}
				if provider {
					source := newProviderSourceTestSource(t, db, "renewal")
					sub.ProviderSourceID = &source.ID
				}
				require.NoError(t, db.CreateSubscription(ctx, sub, ""))
				before, err := db.GetByID(ctx, sub.ID)
				require.NoError(t, err)
				start := time.Now().UTC()
				renewed, err := db.RenewSubscription(ctx, sub.ID, 7)
				require.NoError(t, err)
				require.NotNil(t, renewed.ExpiresAt)
				assert.Equal(t, time.UTC, renewed.ExpiresAt.Location())
				if status == "active" {
					assert.True(t, before.ExpiresAt.AddDate(0, 0, 7).Equal(*renewed.ExpiresAt))
				} else {
					assert.False(t, renewed.ExpiresAt.Before(start.AddDate(0, 0, 7)))
					assert.False(t, renewed.ExpiresAt.After(time.Now().UTC().AddDate(0, 0, 7)))
				}
				assert.Equal(t, "active", renewed.Status)
				assert.Zero(t, renewed.RemindersSent)
				stored, err := db.GetByToken(ctx, before.Token)
				require.NoError(t, err)
				assert.Equal(t, renewed, stored)
				// Compare the complete row, allowing ONLY the documented lifecycle fields.
				expected := *before
				expected.ExpiresAt, expected.Status = renewed.ExpiresAt, "active"
				expected.RemindersSent, expected.UpdatedAt = 0, renewed.UpdatedAt
				assert.Equal(t, &expected, stored)
			})
		}
	}
}

func TestSubscriptionRenewal_BoundsAndTrialPlan(t *testing.T) {
	db := newTestService(t)
	ctx := context.Background()
	source := newProviderSourceTestSource(t, db, "bounds")
	sub := newProviderSourceTestSubscription(t, db, 13002, "renewal-bounds")
	sub.ProviderSourceID = &source.ID // finite provider grant on Free is not perpetual legacy Free
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	for _, days := range []int{-1, 0, MaxSubscriptionRenewalDays + 1} {
		result, err := db.RenewSubscription(ctx, sub.ID, days)
		require.ErrorIs(t, err, ErrInvalidRenewalDays)
		assert.Nil(t, result)
	}
	result, err := db.RenewSubscription(ctx, sub.ID, MaxSubscriptionRenewalDays)
	require.NoError(t, err)
	assert.True(t, sub.ExpiresAt.UTC().AddDate(0, 0, MaxSubscriptionRenewalDays).Equal(*result.ExpiresAt))
	// A date beyond the supported public timestamp range must roll back.
	require.NoError(t, db.db.Model(sub).Update("expires_at", time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)).Error)
	before, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	result, err = db.RenewSubscription(ctx, sub.ID, 1)
	require.ErrorIs(t, err, ErrSubscriptionNotRenewable)
	assert.Nil(t, result)
	after, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	trial, err := db.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)
	require.NoError(t, db.db.Model(sub).Updates(map[string]any{"plan_id": trial.ID, "expires_at": time.Now().UTC().Add(time.Hour)}).Error)
	result, err = db.RenewSubscription(ctx, sub.ID, 1)
	require.ErrorIs(t, err, ErrSubscriptionNotRenewable, "positive customer ID must not make a trial plan renewable")
	assert.Nil(t, result)
}
