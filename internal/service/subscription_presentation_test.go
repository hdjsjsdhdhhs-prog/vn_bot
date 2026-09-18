package service

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPublicSubscriptionInfo(t *testing.T) {
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	expiresAt := time.Date(2027, time.January, 2, 3, 4, 5, 0, time.UTC)
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(_ context.Context, gotToken string) (*database.Subscription, error) {
		require.Equal(t, token, gotToken)
		return &database.Subscription{
			ID:             123,
			SubscriptionID: "internal-id",
			Token:          gotToken,
			Status:         string(database.SubscriptionStatusActive),
			ExpiresAt:      &expiresAt,
		}, nil
	}
	cfg := &config.Config{GlobalSubURL: "https://customer.example/sub/"}
	svc := NewSubscriptionService(db, nil, nil, nil, cfg)

	info, err := svc.GetPublicSubscriptionInfo(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, string(database.SubscriptionStatusActive), info.Status)
	assert.Equal(t, expiresAt, *info.ExpiresAt)
	assert.Equal(t, cfg.SubURL(token), info.SubscriptionURL)

	_, err = svc.GetPublicSubscriptionInfo(context.Background(), "malformed")
	assert.ErrorIs(t, err, database.ErrSubscriptionNotFound)
}

func TestGetPublicSubscriptionInfo_EffectiveStatus(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		status    string
		expiresAt *time.Time
		want      string
	}{
		{"active", nil, "active"},
		{"active", &future, "active"},
		{"active", &past, "expired"},
		{"revoked", &past, "revoked"},
		{"paused", &past, "paused"},
		{"canceled", &past, "canceled"},
		{"expired", &future, "expired"},
	} {
		t.Run(tc.status+"/"+tc.want, func(t *testing.T) {
			db := testutil.NewDatabaseService()
			sub := &database.Subscription{Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: tc.status, ExpiresAt: tc.expiresAt}
			db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) { return sub, nil }
			svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{GlobalSubURL: "https://customer.example/sub/"})
			info, err := svc.GetPublicSubscriptionInfo(context.Background(), sub.Token)
			require.NoError(t, err)
			assert.Equal(t, tc.want, info.Status)
			assert.Equal(t, tc.status, sub.Status, "presentation must not mutate persisted state")
		})
	}
}

func TestFormatSubscriptionMessage_UsesCanonicalFields(t *testing.T) {
	traffic := &TrafficInfo{
		PlanName:           "Premium",
		LimitGB:            100,
		UsedGB:             12.5,
		Percentage:         12.5,
		ProgressBar:        "████",
		CreatedAtFormatted: "01.01.2026 12:00",
		ExpiresAtFormatted: "31.01.2026 12:00",
		ResetInfo:          "через 5 дн.",
	}

	got := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", traffic, "https://example.test/sub/abc")

	assert.Contains(t, got, "Статус: *активна*")
	assert.Contains(t, got, "Тариф: *Premium*")
	assert.Contains(t, got, "12.50 из 100 Гб (12%)")
	assert.Contains(t, got, "████")
	assert.Contains(t, got, "01.01.2026 12:00")
	assert.Contains(t, got, "31.01.2026 12:00")
	assert.Contains(t, got, "через 5 дн.")
	assert.Contains(t, got, "`https://example.test/sub/abc`")
}

func TestFormatSubscriptionMessage_DefaultsUnlimitedAndStatus(t *testing.T) {
	got := FormatSubscriptionMessage("Heading", "", &TrafficInfo{PlanName: "Free"}, "")

	assert.Contains(t, got, "Статус: *активна*")
	assert.Contains(t, got, "Тариф: *Free*")
	assert.Contains(t, got, "Трафик: неограничен")
	assert.Contains(t, got, "Сброс: нет")
}

func TestFormatSubscriptionMessage_EscapesMarkdownInPlanName(t *testing.T) {
	tests := []struct {
		name             string
		planName         string
		expectedInOutput string
	}{
		{
			name:             "underscore in plan name",
			planName:         "Premium_Pro",
			expectedInOutput: `Тариф: *Premium\_Pro*`,
		},
		{
			name:             "asterisk in plan name",
			planName:         "Plan*VIP",
			expectedInOutput: `Тариф: *Plan\*VIP*`,
		},
		{
			name:             "backtick in plan name",
			planName:         "Plan`Special",
			expectedInOutput: "Тариф: *Plan\\`Special*",
		},
		{
			name:             "opening bracket in plan name",
			planName:         "Plan[Elite]",
			expectedInOutput: `Тариф: *Plan\[Elite]*`,
		},
		{
			name:             "multiple special chars",
			planName:         "Pro_*Test*_Plan",
			expectedInOutput: `Тариф: *Pro\_\*Test\*\_Plan*`,
		},
		{
			name:             "all special chars",
			planName:         "_*`[VIP",
			expectedInOutput: "Тариф: *\\_\\*\\`\\[VIP*",
		},
		{
			name:             "normal plan name unchanged",
			planName:         "Premium Pro 2024",
			expectedInOutput: `Тариф: *Premium Pro 2024*`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			traffic := &TrafficInfo{
				PlanName:           tc.planName,
				CreatedAtFormatted: "01.01.2026",
				ExpiresAtFormatted: "31.01.2026",
			}

			got := FormatSubscriptionMessage("Test", "активна", traffic, "https://example.com/sub/test")

			assert.Contains(t, got, tc.expectedInOutput,
				"Plan name should be properly escaped in the output message")
		})
	}
}
