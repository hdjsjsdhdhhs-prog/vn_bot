package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionManagement_CustomerRepository(t *testing.T) {
	db := newTestService(t)
	ctx := context.Background()
	source := newProviderSourceTestSource(t, db, "management")
	sub := newProviderSourceTestSubscription(t, db, 91001, "management")
	sub.ProviderSourceID = &source.ID
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	// The current migrated schema enforces one row per customer, not just an
	// application preference for the latest row.
	duplicate := newProviderSourceTestSubscription(t, db, sub.TelegramID, "management-duplicate")
	require.Error(t, db.CreateSubscription(ctx, duplicate, ""))
	for _, id := range []int64{0, -91001, 99999} {
		row, eligible, err := db.GetCustomerSubscription(ctx, id)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
		assert.Nil(t, row)
		assert.False(t, eligible)
		row, err = db.RenewCustomerSubscription(ctx, id, 7)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
		assert.Nil(t, row)
	}
	before, eligible, err := db.GetCustomerSubscription(ctx, sub.TelegramID)
	require.NoError(t, err)
	require.True(t, eligible)
	assert.Nil(t, before.ProviderSource)
	renewed, err := db.RenewCustomerSubscription(ctx, sub.TelegramID, 7)
	require.NoError(t, err)
	expected := *before
	expected.Status = "active"
	expected.ExpiresAt, expected.UpdatedAt, expected.RemindersSent = renewed.ExpiresAt, renewed.UpdatedAt, 0
	assert.Equal(t, &expected, renewed, "only lifecycle fields may change")
	assert.True(t, before.ExpiresAt.UTC().AddDate(0, 0, 7).Equal(*renewed.ExpiresAt))
	assert.Equal(t, time.UTC, renewed.ExpiresAt.Location())
	stored, err := db.GetByToken(ctx, before.Token)
	require.NoError(t, err)
	assert.Equal(t, renewed, stored)
}
