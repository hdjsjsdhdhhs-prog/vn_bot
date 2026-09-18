package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"
)

// CustomerSubscriptionTerms opts Create into the finite ProviderSource-backed
// customer lifecycle. All fields are required. Source selection is explicit:
// no default, first-row selection, or fallback to legacy plan nodes.
// ExpiresAt is an absolute instant, normalized to UTC, not a local calendar date.
// This grants access without recording a purchase or charging the customer.
type CustomerSubscriptionTerms struct {
	ProviderSourceID uint
	PlanID           uint
	ExpiresAt        time.Time
}

var ErrSubscriptionInactive = errors.New("subscription is inactive; creation cannot renew it")

func (s *SubscriptionService) createCustomer(ctx context.Context, telegramID int64, username, inviteCode string, terms CustomerSubscriptionTerms) (*CreateResult, error) {
	now := time.Now().UTC()
	if telegramID <= 0 || terms.ProviderSourceID == 0 || terms.PlanID == 0 || !terms.ExpiresAt.After(now) {
		return nil, errors.New("customer creation requires a positive identity, provider source, plan and future expiry")
	}
	// Fail before persistence if a caller bypassed startup configuration checks.
	// Do not include the URL in the error: the canonical URL is a credential.
	if s.cfg == nil {
		return nil, errors.New("public subscription URL is not configured")
	}
	u, err := url.Parse(s.cfg.SubURL(""))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("public subscription URL is invalid")
	}
	plan, err := s.db.GetPlanByID(ctx, terms.PlanID)
	if err != nil {
		return nil, fmt.Errorf("resolve customer plan: %w", err)
	}
	if plan == nil || !plan.IsActive || plan.Name == database.TrialPlanName {
		return nil, errors.New("customer plan must be active and non-trial")
	}
	expiry := terms.ExpiresAt.UTC()
	// No get-or-renew here: the unique telegram_id constraint arbitrates
	// concurrent creation, including races against the legacy/trial paths.
	return s.createNewSubscription(ctx, telegramID, XUIEmail(username, telegramID), inviteCode, plan.ID, &terms.ProviderSourceID, &expiry, &now)
}

func (s *SubscriptionService) createNewSubscription(ctx context.Context, telegramID int64, username, inviteCode string, planID uint, sourceID *uint, expiry, started *time.Time) (*CreateResult, error) {
	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}
	sub := &database.Subscription{
		TelegramID: telegramID, Username: username, ClientID: clientID,
		SubscriptionID: subID, PlanID: planID, ProviderSourceID: sourceID,
		ExpiresAt: expiry, StartedAt: started, Status: string(database.SubscriptionStatusActive),
	}
	// Repository assignment validation and INSERT are one transaction. Token
	// generation/retry remains exclusively owned by the existing repository/hook.
	if err := s.db.CreateSubscription(ctx, sub, inviteCode); err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	if err := s.ensureSubscriptionNodes(ctx, sub); err != nil {
		return nil, fmt.Errorf("ensure subscription nodes: %w", err)
	}
	referrerID := int64(0)
	if sub.ReferredBy != nil {
		referrerID = *sub.ReferredBy
	}
	metrics.SubscriptionCreatesTotal.Inc()
	s.RefreshActiveSubscriptionsMetric(ctx)
	return &CreateResult{Subscription: sub, SubscriptionURL: s.cfg.SubURL(sub.Token), ReferrerTGID: referrerID}, nil
}
