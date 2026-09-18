package database

import (
	"context"
	"fmt"
	"time"
)

// ExpireProviderSubscription closes finite provider access without turning it
// into perpetual access to the same upstream. The CAS preserves concurrent
// renewal, revocation and the original token/source/expiry for later extension.
func (s *Service) ExpireProviderSubscription(ctx context.Context, id uint) error {
	result := s.db.WithContext(ctx).Model(&Subscription{}).
		Where("id = ? AND provider_source_id IS NOT NULL AND status = ? AND expires_at <= ?",
			id, string(SubscriptionStatusActive), time.Now().UTC()).
		Update("status", string(SubscriptionStatusExpired))
	if result.Error != nil {
		return fmt.Errorf("expire provider subscription: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		_, err := s.GetByID(ctx, id)
		return err
	}
	return nil
}
