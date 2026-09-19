package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const StarsProvider = "telegram_stars"

var ErrStarsPurchaseInvalid = errors.New("purchase is not eligible for Stars payment")

// TelegramPaymentReceipt is a durable delivery inbox. Orders remain the source
// of truth for entitlement and settlement. TelegramChargeID is the official
// telegram_payment_charge_id needed by refundStarPayment/reconciliation.
type TelegramPaymentReceipt struct {
	ID               uint `gorm:"primaryKey"`
	TelegramChargeID string
	ProviderChargeID string
	PayerTelegramID  int64
	InvoicePayload   string
	Currency         string
	TotalAmount      int64
	TelegramDate     int
	ReceivedAt       time.Time
	SettledAt        *time.Time
}

// lockStarsPurchase takes SQLite's writer lock before any state/policy reads.
func lockStarsPurchase(tx *gorm.DB, ref string, payer int64) (*PurchaseRecord, *PurchaseCatalog, error) {
	claim := tx.Model(&Order{}).Where("purchase_id = ? AND buyer_telegram_id = ?", ref, payer).UpdateColumn("id", gorm.Expr("id"))
	if claim.Error != nil {
		return nil, nil, claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil, nil, ErrOrderNotFound
	}
	var record PurchaseRecord
	if err := tx.Where("purchase_id = ?", ref).First(&record.Order).Error; err != nil {
		return nil, nil, err
	}
	catalog, err := loadPurchaseCustomer(tx, payer)
	if err != nil {
		return nil, nil, err
	}
	if catalog.Subscription == nil || catalog.Subscription.ID != record.Order.SubscriptionID {
		return nil, nil, ErrOrderNotFound
	}
	if err := tx.Preload("Plan").First(&record.Product, record.Order.ProductID).Error; err != nil {
		return nil, nil, err
	}
	return &record, catalog, nil
}

func checkStarsPending(record *PurchaseRecord, now time.Time) error {
	o := &record.Order
	if o.Status != OrderStatusPending || o.ActivatedAt != nil || o.PaidAt != nil || o.ProviderPaymentID != "" || o.PaymentCreationUncertain ||
		(o.PaymentProvider != "" && o.PaymentProvider != StarsProvider) || o.Currency != "XTR" || o.AmountCents <= 0 || o.AmountCents > 10000 ||
		o.PurchaseExpiresAt == nil || !now.Before(*o.PurchaseExpiresAt) {
		return ErrStarsPurchaseInvalid
	}
	return nil
}

// PrepareStarsInvoice binds the existing purchase to Stars before network I/O.
// createInvoiceLink cannot charge a user, so uncertain network outcomes can be
// retried with the same payload; pre-checkout reserves at most one checkout.
func (s *Service) PrepareStarsInvoice(ctx context.Context, ref string, payer int64, validate PurchaseValidator) (*PurchaseRecord, error) {
	var record *PurchaseRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, c, err := lockStarsPurchase(tx, ref, payer)
		if err != nil {
			return err
		}
		if err = checkStarsPending(r, c.Now); err != nil {
			return err
		}
		if _, err = validate(c, &r.Product); err != nil {
			return err
		}
		if err = tx.Model(&Order{}).Where("id = ?", r.Order.ID).Updates(map[string]any{"payment_provider": StarsProvider, "payment_expires_at": r.Order.PurchaseExpiresAt}).Error; err != nil {
			return err
		}
		r.Order.PaymentProvider = StarsProvider
		record = r
		return nil
	})
	return record, err
}

func (s *Service) SaveStarsInvoiceLink(ctx context.Context, orderID uint, link string) error {
	result := s.db.WithContext(ctx).Model(&Order{}).Where("id = ? AND payment_provider = ? AND status = ? AND purchase_expires_at > ?", orderID, StarsProvider, OrderStatusPending, time.Now().UTC()).UpdateColumn("payment_url", link)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrStarsPurchaseInvalid
	}
	return nil
}

// ApproveStarsCheckout only reserves an eligible checkout; it grants no access.
// Telegram retries the same query safely. Another query must use a new purchase
// after expiry, rather than risking a second charge through a reusable invoice.
func (s *Service) ApproveStarsCheckout(ctx context.Context, ref string, payer int64, queryID, currency string, amount int64, validate PurchaseValidator) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, c, err := lockStarsPurchase(tx, ref, payer)
		if err != nil {
			return err
		}
		o := &r.Order
		if err = checkStarsPending(r, c.Now); err != nil {
			return err
		}
		if o.PaymentProvider != StarsProvider || currency != o.Currency || amount != o.AmountCents || queryID == "" ||
			(o.StarsCheckoutID != nil && *o.StarsCheckoutID != queryID) {
			return ErrStarsPurchaseInvalid
		}
		if _, err = validate(c, &r.Product); err != nil {
			return err
		}
		if o.StarsCheckoutID != nil {
			return nil
		}
		return tx.Model(&Order{}).Where("id = ?", o.ID).Updates(map[string]any{"stars_checkout_id": queryID, "stars_checkout_at": c.Now}).Error
	})
}

// RecordTelegramPayment must succeed before acknowledging the Telegram update.
// Conflicting replays never overwrite a receipt, even after settlement.
func (s *Service) RecordTelegramPayment(ctx context.Context, receipt *TelegramPaymentReceipt) error {
	if receipt == nil || receipt.TelegramChargeID == "" {
		return ErrStarsPurchaseInvalid
	}
	receipt.ReceivedAt = time.Now().UTC()
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "telegram_charge_id"}}, DoNothing: true}).Create(receipt)
	if result.Error != nil {
		return fmt.Errorf("persist Telegram payment receipt: %w", result.Error)
	}
	var existing TelegramPaymentReceipt
	if err := s.db.WithContext(ctx).Where("telegram_charge_id = ?", receipt.TelegramChargeID).First(&existing).Error; err != nil {
		return err
	}
	if existing.PayerTelegramID != receipt.PayerTelegramID || existing.InvoicePayload != receipt.InvoicePayload || existing.Currency != receipt.Currency || existing.TotalAmount != receipt.TotalAmount || existing.ProviderChargeID != receipt.ProviderChargeID {
		return ErrStarsPurchaseInvalid
	}
	*receipt = existing
	return nil
}

func (s *Service) PendingTelegramPayments(ctx context.Context, after uint, limit int) ([]TelegramPaymentReceipt, error) {
	var receipts []TelegramPaymentReceipt
	err := s.db.WithContext(ctx).Where("settled_at IS NULL AND id > ?", after).Order("id").Limit(limit).Find(&receipts).Error
	return receipts, err
}

// SettleStarsPayment wraps the existing order CAS, receipt completion, charge
// binding and all subscription/plan DB prerequisites in ONE writer transaction.
// Late successful payments may fulfill expired, previously approved checkouts.
// Canceled/revoked purchases never resurrect access and remain in the inbox for
// operational reconciliation. No expiry-based retry can grant an order twice.
func (s *Service) SettleStarsPayment(ctx context.Context, ref, chargeID string, validate PurchaseValidator, applyPlan ApplyPlanInTxFn) (*Order, *Subscription, bool, error) {
	var order *Order
	var sub *Subscription
	var activated bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Acquire writer lock before reading receipt or order (SQLite ignores FOR UPDATE).
		if err := tx.Model(&TelegramPaymentReceipt{}).Where("telegram_charge_id = ?", chargeID).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return err
		}
		var receipt TelegramPaymentReceipt
		if err := tx.Where("telegram_charge_id = ?", chargeID).First(&receipt).Error; err != nil {
			return err
		}
		r, c, err := lockStarsPurchase(tx, ref, receipt.PayerTelegramID)
		if err != nil {
			return err
		}
		o := &r.Order
		if receipt.InvoicePayload != "stars:v1:"+ref || o.PaymentProvider != StarsProvider || receipt.Currency != "XTR" || o.Currency != receipt.Currency || o.AmountCents != receipt.TotalAmount ||
			o.StarsCheckoutID == nil || o.StarsCheckoutAt == nil || o.PurchaseExpiresAt == nil || !o.StarsCheckoutAt.Before(*o.PurchaseExpiresAt) {
			return ErrStarsPurchaseInvalid
		}
		order, sub = o, c.Subscription
		if o.Status == OrderStatusPaid {
			if o.ProviderPaymentID != chargeID {
				return ErrStarsPurchaseInvalid
			}
		} else {
			if (o.Status != OrderStatusPending && o.Status != OrderStatusExpired) || o.ActivatedAt != nil || o.PaidAt != nil || o.ProviderPaymentID != "" {
				return ErrStarsPurchaseInvalid
			}
			if _, err = validate(c, &r.Product); err != nil {
				return err
			}
			if applyPlan == nil {
				return ErrStarsPurchaseInvalid
			}
			// Existing unique(payment_provider, provider_payment_id) also protects charge binding.
			if err = tx.Model(&Order{}).Where("id = ?", o.ID).UpdateColumn("provider_payment_id", chargeID).Error; err != nil {
				return err
			}
			payments := &Service{db: tx}
			// Honour the order price snapshot even if catalog flags changed after approval.
			product := r.Product
			product.PriceCents, product.Currency = o.AmountCents, o.Currency
			activated, err = payments.ConfirmOrderPaidCAS(ctx, o.ID, c.Now, c.Now, sub, &product, applyPlan, receipt.TotalAmount)
			if err != nil {
				return err
			}
			if !activated {
				return ErrStarsPurchaseInvalid
			}
			if err = tx.First(o, o.ID).Error; err != nil {
				return err
			}
		}
		return tx.Model(&TelegramPaymentReceipt{}).Where("id = ? AND settled_at IS NULL", receipt.ID).UpdateColumn("settled_at", c.Now).Error
	})
	if err != nil {
		return nil, nil, false, err
	}
	return order, sub, activated, nil
}
