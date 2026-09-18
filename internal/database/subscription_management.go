package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// GetCustomerSubscription reads the unique Telegram customer's row and renewal
// eligibility in one DB snapshot, regardless of status. Eligibility means that
// a one-day extension is currently valid, not that the caller is authorized.
// No associations/credentials are returned and no state or caches are changed.
func (s *Service) GetCustomerSubscription(ctx context.Context, telegramID int64) (*Subscription, bool, error) {
	if telegramID <= 0 {
		return nil, false, ErrSubscriptionNotFound
	}
	var sub Subscription
	var eligible bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("telegram_id = ?", telegramID).First(&sub).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSubscriptionNotFound
			}
			return fmt.Errorf("read customer subscription: %w", err)
		}
		_, err := subscriptionRenewalExpiry(tx, &sub, 1, time.Now().UTC())
		switch {
		case err == nil:
			eligible = true
		case errors.Is(err, ErrSubscriptionNotRenewable), errors.Is(err, ErrProviderSourceUnusable), errors.Is(err, ErrProviderSourceNotFound):
			// A known lifecycle/configuration rejection is a capability, not an
			// infrastructure failure. Never hide a DB failure as ineligibility.
		default:
			return err
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &sub, eligible, nil
}

// RenewCustomerSubscription scopes the existing renewal transaction to its
// owner. There is no lookup-ID-then-write gap in which ownership could change.
// Only trusted application code may invoke this repository operation.
func (s *Service) RenewCustomerSubscription(ctx context.Context, telegramID int64, days int) (*Subscription, error) {
	if telegramID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	return s.renewSubscription(ctx, map[string]any{"telegram_id": telegramID}, days)
}
