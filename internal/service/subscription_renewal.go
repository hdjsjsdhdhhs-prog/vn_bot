package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// RenewalResult is for trusted application callers only, not an HTTP DTO.
// Public callers must continue using PublicSubscriptionInfo, which omits IDs.
type RenewalResult struct {
	Subscription    *database.Subscription `json:"-"`
	SubscriptionURL string                 `json:"-"`
}

type renewalRepository interface {
	RenewSubscription(context.Context, uint, int) (*database.Subscription, error)
}

// RenewSubscription grants days of access to the existing finite subscription.
// It extends from max(now UTC, current expiry), preserves identity and source,
// and accepts only active/expired customer subscriptions. Revocation is not a
// renewal. Disabled/unusable providers, trials and legacy Free are rejected.
//
// This is an INTERNAL operation: the caller must authorize the customer/admin
// before invoking it. No bearer-token renewal route is exposed. Each successful
// invocation adds days; delivery deduplication belongs to a future caller.
// Like existing same-plan legacy renewal, this does not reset traffic or enqueue
// node provisioning. It records no payment and performs no provider network I/O.
func (s *SubscriptionService) RenewSubscription(ctx context.Context, subscriptionID uint, days int) (*RenewalResult, error) {
	if err := s.validatePublicSubscriptionURL(); err != nil {
		return nil, err
	}
	repo, ok := s.db.(renewalRepository)
	if !ok {
		return nil, errors.New("subscription renewal repository is not configured")
	}
	sub, err := repo.RenewSubscription(ctx, subscriptionID, days)
	if err != nil {
		return nil, fmt.Errorf("renew subscription: %w", err)
	}
	return s.completeSubscriptionRenewal(ctx, sub), nil
}

// renewCustomerSubscription uses the same lifecycle with an owner-scoped DB
// selector. Only the management boundary calls it after authorizing the grant.
func (s *SubscriptionService) renewCustomerSubscription(ctx context.Context, telegramID int64, days int) (*RenewalResult, error) {
	if err := s.validatePublicSubscriptionURL(); err != nil {
		return nil, err
	}
	repo, ok := s.db.(interface {
		RenewCustomerSubscription(context.Context, int64, int) (*database.Subscription, error)
	})
	if !ok {
		return nil, errors.New("customer renewal repository is not configured")
	}
	sub, err := repo.RenewCustomerSubscription(ctx, telegramID, days)
	if err != nil {
		return nil, fmt.Errorf("renew customer subscription: %w", err)
	}
	return s.completeSubscriptionRenewal(ctx, sub), nil
}

// completeSubscriptionRenewal is shared by trusted ID-based and customer-scoped
// renewal. It must only receive a successfully committed repository result.
func (s *SubscriptionService) completeSubscriptionRenewal(ctx context.Context, sub *database.Subscription) *RenewalResult {
	// Invalidation belongs to this operation, never to its future callers.
	// Existing wiring clears both the bot cache and provider/legacy feed keys.
	s.InvalidateSubscription(ctx, sub.TelegramID)
	s.InvalidateBySubID(ctx, sub.SubscriptionID)
	s.RefreshActiveSubscriptionsMetric(ctx)
	return &RenewalResult{Subscription: sub, SubscriptionURL: s.cfg.SubURL(sub.Token)}
}
