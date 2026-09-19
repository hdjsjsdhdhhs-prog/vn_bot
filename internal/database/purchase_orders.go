package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrPurchaseKeyConflict = errors.New("idempotency key already used for another offer")
	ErrPurchasePending     = errors.New("a purchase is already pending for this offer")
)

// PurchaseCatalog is an internal, consistent snapshot for application policy.
// It must never be serialized: Subscription/Source contain credentials.
type PurchaseCatalog struct {
	Subscription *Subscription
	Source       *ProviderSource
	Products     []Product // includes Plan for policy, never for transport
	Now          time.Time
}

// PurchaseRecord combines an existing order with its immutable product terms.
type PurchaseRecord struct {
	Order   Order
	Product Product
}

// PurchaseValidator runs trusted application policy inside the creation
// transaction. It supplies the intent deadline, not price or caller identity.
type PurchaseValidator func(*PurchaseCatalog, *Product) (time.Time, error)

func newPurchaseReference() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate purchase reference: %w", err)
	}
	return strings.ReplaceAll(id.String(), "-", ""), nil
}

// BeforeCreate assigns products a public reference without changing pricing.
func (p *Product) BeforeCreate(_ *gorm.DB) error {
	if p.OfferID != "" {
		return nil
	}
	ref, err := newPurchaseReference()
	if err != nil {
		return err
	}
	p.OfferID = ref
	return nil
}

func loadPurchaseCustomer(tx *gorm.DB, telegramID int64) (*PurchaseCatalog, error) {
	catalog := &PurchaseCatalog{Now: time.Now().UTC()}
	var sub Subscription
	err := tx.Where("telegram_id = ?", telegramID).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return catalog, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load purchase customer: %w", err)
	}
	catalog.Subscription = &sub
	if sub.ProviderSourceID != nil {
		var source ProviderSource
		err := tx.First(&source, *sub.ProviderSourceID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("load purchase source: %w", err)
		}
		if err == nil {
			catalog.Source = &source
		}
	}
	return catalog, nil
}

func (s *Service) ReadPurchaseCatalog(ctx context.Context, telegramID int64) (*PurchaseCatalog, error) {
	if telegramID <= 0 {
		return nil, ErrSubscriptionNotFound
	}
	var catalog *PurchaseCatalog
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		catalog, err = loadPurchaseCustomer(tx, telegramID)
		if err != nil {
			return err
		}
		// Same base catalogue as ListActiveProducts, with associations needed
		// only by the application policy. No source or external API calls.
		return tx.Preload("Plan").Where("is_active = ? AND price_cents > 0", true).
			Order("price_cents ASC, id ASC").Find(&catalog.Products).Error
	})
	return catalog, err
}

// expireUnsubmittedPurchases uses server time and touches only provider-neutral
// purchase intents. Once attached to a provider, its existing lifecycle owns it.
func expireUnsubmittedPurchases(tx *gorm.DB, telegramID int64, now time.Time) error {
	return tx.Model(&Order{}).
		Where("buyer_telegram_id = ? AND status = ? AND purchase_expires_at <= ?", telegramID, OrderStatusPending, now).
		Where("COALESCE(payment_provider, '') = '' AND COALESCE(provider_payment_id, '') = '' AND payment_creation_uncertain = ?", false).
		UpdateColumn("status", OrderStatusExpired).Error
}

// CreatePurchaseOrder serializes with SQLite writers BEFORE reading ownership,
// product terms or duplicates. DB unique indexes remain the final replay guards.
func (s *Service) CreatePurchaseOrder(ctx context.Context, telegramID int64, offerID, key string, validate PurchaseValidator) (*PurchaseRecord, bool, error) {
	if telegramID <= 0 || validate == nil {
		return nil, false, ErrSubscriptionNotFound
	}
	var record PurchaseRecord
	var created bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := tx.Model(&Subscription{}).Where("telegram_id = ?", telegramID).UpdateColumn("id", gorm.Expr("id"))
		if claim.Error != nil {
			return fmt.Errorf("lock purchase customer: %w", claim.Error)
		}
		if claim.RowsAffected == 0 {
			return ErrSubscriptionNotFound
		}
		now := time.Now().UTC()
		if err := expireUnsubmittedPurchases(tx, telegramID, now); err != nil {
			return fmt.Errorf("expire pending purchases: %w", err)
		}
		var previous Order
		err := tx.Where("buyer_telegram_id = ? AND purchase_key = ?", telegramID, key).First(&previous).Error
		if err == nil {
			if err := tx.First(&record.Product, previous.ProductID).Error; err != nil {
				return err
			}
			if record.Product.OfferID != offerID {
				return ErrPurchaseKeyConflict
			}
			// Do not replay an order after its subscription changed owner.
			var ownerCount int64
			if err := tx.Model(&Subscription{}).Where("id = ? AND telegram_id = ?", previous.SubscriptionID, telegramID).Count(&ownerCount).Error; err != nil {
				return err
			}
			if ownerCount != 1 {
				return ErrOrderNotFound
			}
			record.Order = previous
			return nil // replay original snapshot, even after expiry/deactivation
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Preload("Plan").Where("offer_id = ?", offerID).First(&record.Product).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrProductNotFound
			}
			return err
		}
		catalog, err := loadPurchaseCustomer(tx, telegramID)
		if err != nil {
			return err
		}
		deadline, err := validate(catalog, &record.Product)
		if err != nil {
			return err
		}
		// Reuse the payment lifecycle under this transaction's writer lock:
		// expired links release the pending slot, uncertain attempts do not.
		// The original provider deadline remains intact for callback settlement
		// grace, which belongs to OrderService, not to purchase intent expiry.
		payments := &Service{db: tx}
		if _, err := payments.FindPendingPaymentOrder(ctx, catalog.Subscription.ID, record.Product.ID, catalog.Now); err != nil {
			return err
		}
		// Do not create a second intent alongside an eligible provider payment.
		var pending int64
		if err := tx.Model(&Order{}).Where("subscription_id = ? AND product_id = ? AND status = ?", catalog.Subscription.ID, record.Product.ID, OrderStatusPending).Count(&pending).Error; err != nil {
			return err
		}
		if pending != 0 {
			return ErrPurchasePending
		}
		ref, err := newPurchaseReference()
		if err != nil {
			return err
		}
		record.Order = Order{
			PurchaseID: &ref, BuyerTelegramID: &telegramID, PurchaseKey: &key, PurchaseExpiresAt: &deadline,
			SubscriptionID: catalog.Subscription.ID, ProductID: record.Product.ID,
			Status: OrderStatusPending, AmountCents: record.Product.PriceCents, Currency: record.Product.Currency,
			CreatedAt: catalog.Now,
		}
		if err := tx.Create(&record.Order).Error; err != nil {
			return fmt.Errorf("create purchase order: %w", err)
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &record, created, nil
}

func (s *Service) ReadPurchaseOrder(ctx context.Context, telegramID int64, purchaseID string) (*PurchaseRecord, error) {
	var record PurchaseRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// This write also serializes expiry with provider transitions.
		if err := expireUnsubmittedPurchases(tx, telegramID, time.Now().UTC()); err != nil {
			return err
		}
		err := tx.Table("orders").Select("orders.*").Joins("JOIN subscriptions ON subscriptions.id = orders.subscription_id").
			Where("orders.purchase_id = ? AND orders.buyer_telegram_id = ? AND subscriptions.telegram_id = ?", purchaseID, telegramID, telegramID).
			First(&record.Order).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		return tx.First(&record.Product, record.Order.ProductID).Error
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}
