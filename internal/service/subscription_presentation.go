package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/utils"
)

// PremiumBenefitsText is the concise user-facing summary shared by purchase
// and payment-success messages.
const PremiumBenefitsText = "♾️ Безлимитный трафик\n🌍 Больше серверов и вариантов подключения\n🧪 Дополнительные и экспериментальные функции\n💬 Приоритетная поддержка"

// PublicSubscriptionInfo contains the customer-safe subscription fields exposed
// to holders of the public bearer token.
type PublicSubscriptionInfo struct {
	Status          string     `json:"status"`
	ExpiresAt       *time.Time `json:"expires_at"`
	SubscriptionURL string     `json:"subscription_url"`
}

// GetPublicSubscriptionInfo resolves a bearer token without loading provider or
// provisioning details and builds the canonical public subscription URL.
func (s *SubscriptionService) GetPublicSubscriptionInfo(ctx context.Context, token string) (*PublicSubscriptionInfo, error) {
	if !utils.IsValidSubscriptionToken(token) {
		return nil, database.ErrSubscriptionNotFound
	}

	sub, err := s.db.GetByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("get public subscription info: %w", err)
	}
	if sub == nil || sub.Token != token {
		return nil, database.ErrSubscriptionNotFound
	}

	return &PublicSubscriptionInfo{
		Status:          sub.Status,
		ExpiresAt:       sub.ExpiresAt,
		SubscriptionURL: SubscriptionURL(s.cfg, sub.Token),
	}, nil
}

// FormatSubscriptionMessage renders the canonical subscription presentation.
// Both the bot's "My subscription" screen and payment success notification use
// this helper so tariff, traffic, dates, reset information and URL stay aligned.
func FormatSubscriptionMessage(heading, status string, traffic *TrafficInfo, subURL string) string {
	if traffic == nil {
		traffic = &TrafficInfo{}
	}

	trafficInfo := "неограничен"
	progress := ""

	if traffic.LimitGB > 0 {
		trafficInfo = fmt.Sprintf("%.2f из %d Гб (%.0f%%)", traffic.UsedGB, traffic.LimitGB, traffic.Percentage)
		progress = "\n" + traffic.ProgressBar
	}

	resetInfo := traffic.ResetInfo
	if resetInfo == "" {
		resetInfo = "нет"
	}

	if status == "" {
		status = "активна"
	}

	return fmt.Sprintf(
		"%s\n\n✌️ Статус: *%s*\n💡 Тариф: *%s*\n📊 Трафик: %s%s\n\n📅 Создана: %s\n⏰ Истекает: %s\n🔄 Сброс: %s\n\n🔗 Ссылка\n`%s`",
		heading,
		status,
		utils.EscapeLegacyMarkdown(traffic.PlanName),
		trafficInfo,
		progress,
		traffic.CreatedAtFormatted,
		traffic.ExpiresAtFormatted,
		resetInfo,
		subURL,
	)
}

// SubscriptionURL is kept as a tiny presentation seam for callers that already
// carry Config and should not duplicate URL construction.
func SubscriptionURL(cfg *config.Config, token string) string {
	if cfg == nil {
		return ""
	}

	return cfg.SubURL(token)
}
