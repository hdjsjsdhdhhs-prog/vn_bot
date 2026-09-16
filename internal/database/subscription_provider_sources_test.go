package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionProviderSource_LegacySubscriptionRemainsUnlinked(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	sub := newProviderSourceTestSubscription(t, svc, 10001, "legacy-provider-source-sub")

	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	require.Nil(t, sub.ProviderSourceID)

	stored, err := svc.GetSubscriptionWithProviderSource(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Nil(t, stored.ProviderSourceID)
	assert.Nil(t, stored.ProviderSource)

	legacy, err := svc.GetWithPlanAndNodes(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, legacy.Subscription.ID)
}

func TestSubscriptionProviderSource_CreateAndLoadAssociation(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	source := newProviderSourceTestSource(t, svc, "create-and-load")
	sub := newProviderSourceTestSubscription(t, svc, 10002, "linked-provider-source-sub")
	sub.ProviderSourceID = &source.ID

	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	storedWithoutPreload, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, storedWithoutPreload.ProviderSourceID)
	assert.Equal(t, source.ID, *storedWithoutPreload.ProviderSourceID)

	stored, err := svc.GetSubscriptionWithProviderSource(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	require.NotNil(t, stored.ProviderSourceID)
	assert.Equal(t, source.ID, *stored.ProviderSourceID)
	require.NotNil(t, stored.ProviderSource)
	assert.Equal(t, source.ID, stored.ProviderSource.ID)
	assert.Equal(t, source.Name, stored.ProviderSource.Name)
}

func TestSubscriptionProviderSource_BindAndUnbind(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	source := newProviderSourceTestSource(t, svc, "bind-and-unbind")
	sub := newProviderSourceTestSubscription(t, svc, 10003, "bind-provider-source-sub")
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

	require.NoError(t, svc.BindProviderSource(ctx, sub.ID, source.ID))
	linked, err := svc.GetSubscriptionWithProviderSource(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	require.NotNil(t, linked.ProviderSource)
	assert.Equal(t, source.ID, linked.ProviderSource.ID)

	require.NoError(t, svc.UnbindProviderSource(ctx, sub.ID))
	unlinked, err := svc.GetSubscriptionWithProviderSource(ctx, sub.SubscriptionID)
	require.NoError(t, err)
	assert.Nil(t, unlinked.ProviderSourceID)
	assert.Nil(t, unlinked.ProviderSource)
}

func TestGetSubscriptionWithProviderSource_RejectsExpiredAndRevokedSubscriptions(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		status    string
		expiresAt *time.Time
	}{
		{
			name:      "expired",
			status:    string(SubscriptionStatusActive),
			expiresAt: ptrTime(time.Now().Add(-time.Minute)),
		},
		{
			name:      "revoked",
			status:    string(SubscriptionStatusRevoked),
			expiresAt: ptrTime(time.Now().Add(time.Hour)),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			svc := newTestService(t)
			sub := newProviderSourceTestSubscription(t, svc, -20001, "unservable-provider-source-"+testCase.name)
			sub.Status = testCase.status
			sub.ExpiresAt = testCase.expiresAt
			require.NoError(t, svc.CreateSubscription(context.Background(), sub, ""))

			_, err := svc.GetSubscriptionWithProviderSource(context.Background(), sub.SubscriptionID)
			assert.ErrorIs(t, err, ErrSubscriptionNotFound)
		})
	}
}

func newProviderSourceTestSource(t *testing.T, svc *Service, suffix string) *ProviderSource {
	t.Helper()

	source := &ProviderSource{
		Name:            "Provider " + suffix,
		Type:            "external",
		SubscriptionURL: "https://provider.example/sub/" + suffix,
		Headers:         `{}`,
		Enabled:         true,
	}
	require.NoError(t, svc.CreateProviderSource(context.Background(), source))

	return source
}

func newProviderSourceTestSubscription(t *testing.T, svc *Service, telegramID int64, subscriptionID string) *Subscription {
	t.Helper()

	return &Subscription{
		TelegramID:     telegramID,
		ClientID:       "client-" + subscriptionID,
		SubscriptionID: subscriptionID,
		ExpiresAt:      ptrTime(time.Now().Add(time.Hour)),
		Status:         string(SubscriptionStatusActive),
		PlanID:         testFreePlanID(t, svc),
	}
}
