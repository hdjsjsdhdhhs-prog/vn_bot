package service

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/utils"
)

// SubscriptionDetails holds the display-oriented view of a subscription for
// the bot's «Моя подписка» screen and the payment success notification. It
// carries already-formatted strings so callers can render a Telegram message
// without re-deriving presentation logic.
//
// There is deliberately no traffic usage or quota here: the bot does not meter
// traffic. Upstream providers report their own usage and quota to the VPN app
// in the subscription-userinfo header of /sub.
type SubscriptionDetails struct {
	PlanName           string
	CreatedAtFormatted string
	ExpiresAtFormatted string
}

// GetWithDetails returns the active subscription of a Telegram user together
// with its display details (tariff name, creation and expiry dates).
func (s *SubscriptionService) GetWithDetails(ctx context.Context, telegramID int64) (*database.Subscription, *SubscriptionDetails, error) {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, nil, err
	}

	var planName string

	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	if planErr == nil && plan != nil {
		planName = plan.Name
	}

	if sub.ProductID != nil && *sub.ProductID != 0 {
		product, productErr := s.db.GetProductByID(ctx, *sub.ProductID)
		if productErr == nil && product != nil && product.Name != "" {
			planName = product.Name
		}
	}

	return sub, &SubscriptionDetails{
		PlanName:           planName,
		CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
		ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
	}, nil
}

// formatExpiresAt formats ExpiresAt for display. NULL = "бессрочно".
func formatExpiresAt(t *time.Time) string {
	if t == nil {
		return "бессрочно"
	}

	return utils.FormatDateRu(*t)
}
