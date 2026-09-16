package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderSourceRepository_CreateAndGet(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	source := &ProviderSource{
		Name:            "RelayHub",
		Type:            "relayhub",
		SubscriptionURL: "https://provider.example/sub/token",
		HWID:            "device-hwid",
		UserAgent:       "VPN Client/1.0",
		Headers:         `{"X-Device-OS":"iOS","X-Client":"VPN Client"}`,
		Enabled:         true,
	}

	require.NoError(t, svc.CreateProviderSource(ctx, source))
	require.NotZero(t, source.ID)

	stored, err := svc.GetProviderSourceByID(ctx, source.ID)
	require.NoError(t, err)
	assert.Equal(t, source.Name, stored.Name)
	assert.Equal(t, source.Type, stored.Type)
	assert.Equal(t, source.SubscriptionURL, stored.SubscriptionURL)
	assert.Equal(t, source.HWID, stored.HWID)
	assert.Equal(t, source.UserAgent, stored.UserAgent)
	assert.JSONEq(t, source.Headers, stored.Headers)
	assert.True(t, stored.Enabled)
	assert.False(t, stored.CreatedAt.IsZero())
	assert.False(t, stored.UpdatedAt.IsZero())
}

func TestProviderSourceRepository_PreservesDisabledFlag(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	source := &ProviderSource{
		Name:            "Disabled provider",
		Type:            "external",
		SubscriptionURL: "https://provider.example/sub/disabled",
		Headers:         `{}`,
		Enabled:         false,
	}

	require.NoError(t, svc.CreateProviderSource(ctx, source))

	stored, err := svc.GetProviderSourceByID(ctx, source.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
}

func TestProviderSourceRepository_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.GetProviderSourceByID(context.Background(), 99999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProviderSourceNotFound))
}
