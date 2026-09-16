package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionBeforeCreate_TokenValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("empty token is generated for direct GORM create", func(t *testing.T) {
		svc := newTestService(t)
		sub := newTokenTestSubscription(t, svc, "direct-generated")

		require.NoError(t, svc.db.WithContext(ctx).Create(sub).Error)
		assert.True(t, utils.IsValidSubscriptionToken(sub.Token))
	})

	t.Run("valid explicit token is preserved for direct GORM create", func(t *testing.T) {
		svc := newTestService(t)
		token, err := utils.GenerateSubscriptionToken()
		require.NoError(t, err)
		sub := newTokenTestSubscription(t, svc, "direct-explicit")
		sub.Token = token

		require.NoError(t, svc.db.WithContext(ctx).Create(sub).Error)
		assert.Equal(t, token, sub.Token)
	})

	for name, token := range map[string]string{
		"uppercase":    strings.Repeat("A", utils.SubscriptionTokenLength),
		"wrong length": strings.Repeat("a", utils.SubscriptionTokenLength-1),
		"non hex":      strings.Repeat("g", utils.SubscriptionTokenLength),
		"whitespace":   " " + strings.Repeat("a", utils.SubscriptionTokenLength-1),
		"prefix":       "0x" + strings.Repeat("a", utils.SubscriptionTokenLength-2),
	} {
		t.Run("invalid explicit token is rejected: "+name, func(t *testing.T) {
			svc := newTestService(t)
			sub := newTokenTestSubscription(t, svc, "direct-invalid-"+name)
			sub.Token = token

			err := svc.db.WithContext(ctx).Create(sub).Error
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidSubscriptionToken))
		})
	}
}

func newTokenTestSubscription(t *testing.T, svc *Service, suffix string) *Subscription {
	t.Helper()

	return &Subscription{
		TelegramID:     41900,
		ClientID:       "token-client-" + suffix,
		SubscriptionID: "token-subscription-" + suffix,
		PlanID:         testFreePlanID(t, svc),
		Status:         string(SubscriptionStatusActive),
	}
}

func TestCreateSubscription_GeneratesAndPersistsToken(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	sub := &Subscription{
		TelegramID:     41001,
		ClientID:       "token-client-1",
		SubscriptionID: "token-subscription-1",
		PlanID:         testFreePlanID(t, svc),
		Status:         string(SubscriptionStatusActive),
	}

	require.NoError(t, svc.CreateSubscription(context.Background(), sub, ""))
	assert.True(t, utils.IsValidSubscriptionToken(sub.Token))

	stored, err := svc.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.Token, stored.Token)

	byToken, err := svc.GetByToken(context.Background(), sub.Token)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, byToken.ID)
	assert.Equal(t, sub.SubscriptionID, byToken.SubscriptionID)
}

func TestCreateSubscription_RejectsDuplicateToken(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	token, err := utils.GenerateSubscriptionToken()
	require.NoError(t, err)
	planID := testFreePlanID(t, svc)

	first := &Subscription{TelegramID: 41002, ClientID: "token-client-2", SubscriptionID: "token-subscription-2", Token: token, PlanID: planID, Status: string(SubscriptionStatusActive)}
	second := &Subscription{TelegramID: 41003, ClientID: "token-client-3", SubscriptionID: "token-subscription-3", Token: token, PlanID: planID, Status: string(SubscriptionStatusActive)}

	require.NoError(t, svc.CreateSubscription(context.Background(), first, ""))
	err = svc.CreateSubscription(context.Background(), second, "")
	require.Error(t, err)

	resolved, lookupErr := svc.GetByToken(context.Background(), token)
	require.NoError(t, lookupErr)
	assert.Equal(t, first.ID, resolved.ID)
}

func TestGetByToken_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	_, err := svc.GetByToken(context.Background(), "missing-token")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}
