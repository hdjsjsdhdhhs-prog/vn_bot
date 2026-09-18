package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// MaxSubscriptionRenewalDays bounds each grant, not the subscription's lifetime.
const MaxSubscriptionRenewalDays = 3650

var (
	ErrInvalidRenewalDays       = errors.New("renewal days must be between 1 and 3650")
	ErrSubscriptionNotRenewable = errors.New("subscription is not eligible for renewal")
)

// RenewSubscription extends the current finite entitlement in one transaction.
// Each successful call is a distinct extension (not an idempotent payment API).
// No identity, plan, source, purchase, traffic, or provisioning fields change.
func (s *Service) RenewSubscription(ctx context.Context, id uint, days int) (*Subscription, error) {
	return s.renewSubscription(ctx, map[string]any{"id": id}, days)
}

func (s *Service) renewSubscription(ctx context.Context, selector map[string]any, days int) (*Subscription, error) {
	if days <= 0 || days > MaxSubscriptionRenewalDays {
		return nil, ErrInvalidRenewalDays
	}
	var renewed Subscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SQLite has no SELECT FOR UPDATE. Acquire its writer reservation BEFORE
		// reading expiry/source so concurrent renewals (even on separate DB
		// connections) read the previous committed extension, not a stale snapshot.
		// This no-op does not change UpdatedAt and rolls back with any failure.
		claim := tx.Model(&Subscription{}).Where(selector).
			UpdateColumn("id", gorm.Expr("id"))
		if claim.Error != nil {
			return fmt.Errorf("lock subscription for renewal: %w", claim.Error)
		}
		if claim.RowsAffected == 0 {
			return ErrSubscriptionNotFound
		}
		if err := tx.Where(selector).First(&renewed).Error; err != nil {
			return fmt.Errorf("load subscription for renewal: %w", err)
		}
		expiry, err := subscriptionRenewalExpiry(tx, &renewed, days, time.Now().UTC())
		if err != nil {
			return err
		}
		result := tx.Model(&Subscription{}).Where("id = ?", renewed.ID).Updates(map[string]any{
			"expires_at":     expiry,
			"status":         string(SubscriptionStatusActive),
			"reminders_sent": 0,
		})
		if result.Error != nil {
			return fmt.Errorf("update subscription renewal: %w", result.Error)
		}
		// No associations are preloaded: the internal result carries no source
		// credentials. Publish the committed snapshot only after commit succeeds.
		return tx.First(&renewed, renewed.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &renewed, nil
}

// subscriptionRenewalExpiry is the single lifecycle policy for renewal and its
// read-only capability preview. Callers validate days before invoking it.
func subscriptionRenewalExpiry(tx *gorm.DB, sub *Subscription, days int, now time.Time) (time.Time, error) {
	if sub.TelegramID <= 0 || sub.ExpiresAt == nil ||
		(sub.Status != string(SubscriptionStatusActive) && sub.Status != string(SubscriptionStatusExpired)) {
		return time.Time{}, ErrSubscriptionNotRenewable
	}
	var plan Plan
	if err := tx.First(&plan, sub.PlanID).Error; err != nil {
		return time.Time{}, fmt.Errorf("load renewal plan: %w", err)
	}
	if plan.Name == TrialPlanName {
		return time.Time{}, ErrSubscriptionNotRenewable
	}
	if sub.ProviderSourceID != nil {
		var source ProviderSource
		if err := tx.First(&source, *sub.ProviderSourceID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return time.Time{}, ErrProviderSourceNotFound
			}
			return time.Time{}, fmt.Errorf("load renewal provider source: %w", err)
		}
		// Local configuration validation only: no upstream I/O and no fallback.
		if _, _, err := source.RequestConfiguration(); err != nil {
			return time.Time{}, err
		}
		if !plan.IsActive {
			return time.Time{}, ErrSubscriptionNotRenewable
		}
	} else if plan.Name == FreePlanName {
		// An already-downgraded legacy subscription needs explicit plan
		// selection through the existing activation/admin flow, not inference.
		return time.Time{}, ErrSubscriptionNotRenewable
	}

	base := now.UTC()
	if sub.ExpiresAt.After(base) {
		base = sub.ExpiresAt.UTC()
	}
	expiry := base.AddDate(0, 0, days)
	if !expiry.After(base) || expiry.Year() > 9999 {
		return time.Time{}, ErrSubscriptionNotRenewable
	}
	return expiry, nil
}
