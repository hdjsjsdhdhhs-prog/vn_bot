package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
)

var (
	ErrPurchaseAccessDenied = errors.New("purchase access denied")
	ErrInvalidPurchase = errors.New("invalid purchase request")
	ErrOfferUnavailable = errors.New("offer unavailable")
	ErrPurchaseUnavailable = errors.New("purchase service unavailable")
)

// PurchaseAuthorizer resolves a verified application identity, never request
// fields or bearer tokens. Purchasing does not authorize entitlement grants.
type PurchaseAuthorizer func(context.Context) (int64, error)

type purchaseRepository interface {
	ReadPurchaseCatalog(context.Context, int64) (*database.PurchaseCatalog, error)
	CreatePurchaseOrder(context.Context, int64, string, string, database.PurchaseValidator) (*database.PurchaseRecord, bool, error)
	ReadPurchaseOrder(context.Context, int64, string) (*database.PurchaseRecord, error)
}

// PurchaseService is the provider-neutral application boundary over the existing
// products/orders repository. It cannot provision, renew, or contact payments.
// SubscriptionService supplies the shared repository, not another account model.
type PurchaseService struct {
	repo purchaseRepository
	authorize PurchaseAuthorizer
}

func NewPurchaseService(subscriptions *SubscriptionService, authorize PurchaseAuthorizer) *PurchaseService {
	p := &PurchaseService{authorize: authorize}
	if subscriptions != nil {
		p.repo, _ = subscriptions.db.(purchaseRepository)
	}
	return p
}

// SubscriptionOffer is a safe projection of a server-defined Product.
type SubscriptionOffer struct {
	OfferID string `json:"offer_id"`
	Name string `json:"name"`
	DurationDays int `json:"duration_days"`
	AmountCents int64 `json:"amount_cents"`
	Currency string `json:"currency"`
	AvailableUntil *time.Time `json:"available_until,omitempty"`
}

// PurchaseOrderInfo intentionally omits internal IDs, identity, idempotency key,
// provider fields and subscription credentials. ExpiresAt is the intent deadline,
// not the subscription entitlement or the provider's payment-link deadline.
type PurchaseOrderInfo struct {
	OrderID string `json:"order_id"`
	OfferID string `json:"offer_id"`
	Name string `json:"name"`
	DurationDays int `json:"duration_days"`
	AmountCents int64 `json:"amount_cents"`
	Currency string `json:"currency"`
	Status database.OrderStatus `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	CheckoutStarted bool `json:"checkout_started"`
}

func (p *PurchaseService) customer(ctx context.Context) (int64, error) {
	if p.authorize == nil {
		return 0, ErrPurchaseAccessDenied
	}
	id, err := p.authorize(ctx)
	if err != nil || id <= 0 {
		return 0, ErrPurchaseAccessDenied
	}
	if p.repo == nil {
		return 0, ErrPurchaseUnavailable
	}
	return id, nil
}

// Offers returns only offers currently selectable by this customer. Missing,
// revoked, paused and canceled subscriptions have an empty catalogue. Reading
// never creates/revives a subscription. Creation revalidates under the DB lock.
func (p *PurchaseService) Offers(ctx context.Context) ([]SubscriptionOffer, error) {
	id, err := p.customer(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := p.repo.ReadPurchaseCatalog(ctx, id)
	if err != nil {
		return nil, safePurchaseError(err)
	}
	if catalog == nil || (catalog.Subscription != nil && catalog.Subscription.TelegramID != id) {
		return nil, ErrPurchaseAccessDenied
	}
	offers := make([]SubscriptionOffer, 0)
	for i := range catalog.Products {
		product := &catalog.Products[i]
		if _, err := validatePurchaseOffer(catalog, product); err == nil {
			offers = append(offers, SubscriptionOffer{OfferID: product.OfferID, Name: product.Name,
				DurationDays: product.DurationDays, AmountCents: product.PriceCents,
				Currency: product.Currency, AvailableUntil: product.OfferEndsAt})
		}
	}
	return offers, nil
}

// Create accepts only offer reference and UUID v4 idempotency key. Identity,
// price, currency, lifecycle state and timestamps cannot be supplied by clients.
// Same identity/key/offer returns the original order's current state (created=false),
// including terminal orders. Key reuse for another offer or another key while the
// same offer is pending returns a conflict, never a second pending purchase.
func (p *PurchaseService) Create(ctx context.Context, offerID, key string) (*PurchaseOrderInfo, bool, error) {
	id, err := p.customer(ctx)
	if err != nil {
		return nil, false, err
	}
	parsed, parseErr := uuid.Parse(key)
	if !validPurchaseReference(offerID) || parseErr != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != key {
		return nil, false, ErrInvalidPurchase
	}
	record, created, err := p.repo.CreatePurchaseOrder(ctx, id, offerID, key, func(c *database.PurchaseCatalog, product *database.Product) (time.Time, error) {
		if c == nil || c.Subscription == nil || c.Subscription.TelegramID != id {
			return time.Time{}, ErrOfferUnavailable
		}
		return validatePurchaseOffer(c, product)
	})
	if err != nil {
		return nil, false, safePurchaseError(err)
	}
	info, err := purchaseInfo(record, id)
	return info, created, err
}

func (p *PurchaseService) Order(ctx context.Context, orderID string) (*PurchaseOrderInfo, error) {
	id, err := p.customer(ctx)
	if err != nil {
		return nil, err
	}
	if !validPurchaseReference(orderID) {
		return nil, database.ErrOrderNotFound
	}
	record, err := p.repo.ReadPurchaseOrder(ctx, id, orderID)
	if err != nil {
		return nil, safePurchaseError(err)
	}
	return purchaseInfo(record, id)
}

const purchaseIntentTTL = 30 * time.Minute

func validatePurchaseOffer(c *database.PurchaseCatalog, product *database.Product) (time.Time, error) {
	if c == nil || c.Subscription == nil || product == nil || product.Plan == nil {
		return time.Time{}, ErrOfferUnavailable
	}
	sub := c.Subscription
	if sub.TelegramID <= 0 || (sub.Status != string(database.SubscriptionStatusActive) && sub.Status != string(database.SubscriptionStatusExpired)) ||
		!product.IsActive || !product.Plan.IsActive || product.Plan.Name == database.TrialPlanName || product.Plan.Name == database.FreePlanName ||
		!validPurchaseReference(product.OfferID) || strings.TrimSpace(product.Name) == "" || product.PriceCents <= 0 ||
		product.DurationDays <= 0 || product.DurationDays > database.MaxSubscriptionRenewalDays || !validPurchaseCurrency(product.Currency) ||
		(product.OfferEndsAt != nil && !c.Now.Before(*product.OfferEndsAt)) {
		return time.Time{}, ErrOfferUnavailable
	}
	// No source inference or migration: a ProviderSource customer can purchase
	// only its existing plan, with the same locally validated source. No I/O.
	if sub.ProviderSourceID != nil {
		if c.Source == nil || c.Source.ID != *sub.ProviderSourceID || product.PlanID != sub.PlanID {
			return time.Time{}, ErrOfferUnavailable
		}
		if _, _, err := c.Source.RequestConfiguration(); err != nil {
			return time.Time{}, ErrOfferUnavailable
		}
	}
	// Use the existing payment expiry calculation, not renewal/grant semantics.
	if expiry := database.CalculatePaymentExpiry(c.Now, sub, product); !expiry.After(c.Now) || expiry.Year() > 9999 {
		return time.Time{}, ErrOfferUnavailable
	}
	deadline := c.Now.Add(purchaseIntentTTL)
	if product.OfferEndsAt != nil && product.OfferEndsAt.Before(deadline) {
		deadline = *product.OfferEndsAt
	}
	return deadline.UTC(), nil
}

func validPurchaseCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, c := range currency {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func validPurchaseReference(ref string) bool {
	if len(ref) != 32 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func purchaseInfo(record *database.PurchaseRecord, id int64) (*PurchaseOrderInfo, error) {
	if record == nil || record.Order.BuyerTelegramID == nil || *record.Order.BuyerTelegramID != id || record.Order.PurchaseID == nil {
		return nil, ErrPurchaseAccessDenied
	}
	o, product := record.Order, record.Product
	return &PurchaseOrderInfo{OrderID: *o.PurchaseID, OfferID: product.OfferID, Name: product.Name,
		DurationDays: product.DurationDays, AmountCents: o.AmountCents, Currency: o.Currency,
		Status: o.Status, CreatedAt: o.CreatedAt, ExpiresAt: o.PurchaseExpiresAt, CheckoutStarted: o.StarsCheckoutID != nil}, nil
}

// Preserve trusted diagnostics via Unwrap while keeping raw SQL/credentials out
// of the public error text, following the management service's error convention.
type purchaseError struct { kind, cause error }
func (e *purchaseError) Error() string { return e.kind.Error() }
func (e *purchaseError) Unwrap() error { return e.cause }
func (e *purchaseError) Is(target error) bool { return target == e.kind }

func safePurchaseError(err error) error {
	kind := ErrPurchaseUnavailable
	switch {
	case errors.Is(err, database.ErrProductNotFound), errors.Is(err, database.ErrSubscriptionNotFound), errors.Is(err, ErrOfferUnavailable):
		kind = ErrOfferUnavailable
	case errors.Is(err, database.ErrOrderNotFound):
		kind = database.ErrOrderNotFound
	case errors.Is(err, database.ErrPurchaseKeyConflict):
		kind = database.ErrPurchaseKeyConflict
	case errors.Is(err, database.ErrPurchasePending):
		kind = database.ErrPurchasePending
	case errors.Is(err, context.Canceled):
		kind = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		kind = context.DeadlineExceeded
	}
	return &purchaseError{kind: kind, cause: err}
}
