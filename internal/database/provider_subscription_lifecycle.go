package database

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ExpireProviderSubscription closes finite provider access without turning it
// into perpetual access to the same upstream. The CAS preserves concurrent
// renewal, revocation and the original token/source/expiry for later extension.
func (s *Service) ExpireProviderSubscription(ctx context.Context, id uint) error {
	transitioned := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		prior, err := claimSubscriptionSnapshot(tx, id)
		if err != nil {
			return err
		}
		result := tx.Model(&Subscription{}).
			Where("id = ? AND provider_source_id IS NOT NULL AND status = ? AND expires_at <= ?",
				id, string(SubscriptionStatusActive), time.Now().UTC()).
			Update("status", string(SubscriptionStatusExpired))
		if result.Error != nil {
			return fmt.Errorf("expire provider subscription: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		transitioned = true
		after := JournalStateOf(prior)
		after.Status = string(SubscriptionStatusExpired)
		recordJournal(ctx, tx, JournalRecord{
			Type: JournalExpired, Actor: JournalActorSystem, Subscription: prior,
			Description: "Срок подписки истёк, доступ отключён",
			Before:      JournalStateOf(prior), After: after,
			DedupKey: journalExpiryKey("subscription_expired", prior.ID, prior.ExpiresAt),
		})
		return nil
	})
	if err != nil || transitioned {
		return err
	}
	// Not transitioned: keep the original contract (nil for an existing row).
	_, err = s.GetByID(ctx, id)
	return err
}
