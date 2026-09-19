package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func purchaseServiceFixture(t *testing.T) (*PurchaseService, *database.Service, *database.Subscription, *database.Product) {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "purchase.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	plan := &database.Plan{Name: "purchase-policy", IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	product := &database.Product{PlanID: plan.ID, Name: "Monthly", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, db.GetDB().Create(product).Error)
	sub := &database.Subscription{TelegramID: 44001, ClientID: "purchase-client", SubscriptionID: "purchase-sub", PlanID: plan.ID, Status: "active"}
	require.NoError(t, db.CreateSubscription(context.Background(), sub, ""))
	p := NewPurchaseService(NewSubscriptionService(db, nil, nil, nil, nil), func(context.Context) (int64, error) { return 44001, nil })
	return p, db, sub, product
}

func TestPurchaseService_OfferAvailability(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
		field string
		value any
	}{
		{"disabled offer", "products", "is_active", false},
		{"ended offer", "products", "offer_ends_at", time.Now().UTC().Add(-time.Minute)},
		{"disabled plan", "plans", "is_active", false},
		{"free plan", "plans", "name", database.FreePlanName},
		{"trial plan", "plans", "name", database.TrialPlanName},
		{"zero price", "products", "price_cents", 0},
		{"zero duration", "products", "duration_days", 0},
		{"excessive duration", "products", "duration_days", database.MaxSubscriptionRenewalDays + 1},
		{"invalid currency", "products", "currency", "rub"},
		{"blank name", "products", "name", " "},
		{"revoked customer", "subscriptions", "status", "revoked"},
		{"paused customer", "subscriptions", "status", "paused"},
		{"canceled customer", "subscriptions", "status", "canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, db, sub, product := purchaseServiceFixture(t)
			id := product.ID
			if tc.table == "plans" {
				id = product.PlanID
			}
			if tc.table == "subscriptions" {
				id = sub.ID
			}
			if tc.name == "free plan" || tc.name == "trial plan" {
				plan, err := db.GetPlanByName(context.Background(), tc.value.(string))
				require.NoError(t, err)
				require.NoError(t, db.GetDB().Model(product).Update("plan_id", plan.ID).Error)
			} else {
				require.NoError(t, db.GetDB().Table(tc.table).Where("id = ?", id).Update(tc.field, tc.value).Error)
			}
			offers, err := p.Offers(context.Background())
			require.NoError(t, err)
			assert.Empty(t, offers)
			info, created, err := p.Create(context.Background(), product.OfferID, uuid.NewString())
			require.ErrorIs(t, err, ErrOfferUnavailable)
			assert.Nil(t, info)
			assert.False(t, created)
			var count int64
			require.NoError(t, db.GetDB().Model(&database.Order{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestPurchaseService_ProviderSourceConstraints(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*database.ProviderSource)
		otherPlan bool
		allowed   bool
	}{
		{"same plan enabled", func(*database.ProviderSource) {}, false, true},
		{"different plan", func(*database.ProviderSource) {}, true, false},
		{"disabled source", func(s *database.ProviderSource) { s.Enabled = false }, false, false},
		{"invalid URL", func(s *database.ProviderSource) { s.SubscriptionURL = "file:///invalid" }, false, false},
		{"invalid headers", func(s *database.ProviderSource) { s.Headers = `{broken` }, false, false},
		{"duplicate normalized header", func(s *database.ProviderSource) { s.Headers = `{"X-Test":"a","x-test":"b"}` }, false, false},
		{"invalid user agent", func(s *database.ProviderSource) { s.UserAgent = "bad\r\nvalue" }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, db, sub, product := purchaseServiceFixture(t)
			source := &database.ProviderSource{Name: "purchase-source", Type: "external", Enabled: true, SubscriptionURL: "https://provider.example/sub/test"}
			tc.change(source)
			require.NoError(t, db.GetDB().Create(source).Error)
			// GORM defaults can turn false into true at insert; persist the intended value.
			if tc.name == "disabled source" {
				require.NoError(t, db.GetDB().Model(source).Update("enabled", false).Error)
			}
			require.NoError(t, db.GetDB().Model(sub).Update("provider_source_id", source.ID).Error)
			if tc.otherPlan {
				other := &database.Plan{Name: "other-paid", IsActive: true}
				require.NoError(t, db.GetDB().Create(other).Error)
				require.NoError(t, db.GetDB().Model(product).Update("plan_id", other.ID).Error)
			}
			offers, err := p.Offers(context.Background())
			require.NoError(t, err)
			info, created, err := p.Create(context.Background(), product.OfferID, uuid.NewString())
			if tc.allowed {
				require.NoError(t, err)
				require.Len(t, offers, 1)
				require.NotNil(t, info)
				assert.True(t, created)
			} else {
				require.ErrorIs(t, err, ErrOfferUnavailable)
				assert.Empty(t, offers)
				assert.Nil(t, info)
				assert.False(t, created)
			}
			after, err := db.GetByID(context.Background(), sub.ID)
			require.NoError(t, err)
			assert.Equal(t, sub.PlanID, after.PlanID)
			assert.Equal(t, &source.ID, after.ProviderSourceID)
		})
	}
}

func TestPurchaseOfferPolicy_Boundaries(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		mutate  func(*database.PurchaseCatalog, *database.Product)
		allowed bool
	}{
		{"active", func(*database.PurchaseCatalog, *database.Product) {}, true},
		{"expired customer", func(c *database.PurchaseCatalog, _ *database.Product) { c.Subscription.Status = "expired" }, true},
		{"missing customer", func(c *database.PurchaseCatalog, _ *database.Product) { c.Subscription = nil }, false},
		{"missing plan", func(_ *database.PurchaseCatalog, p *database.Product) { p.Plan = nil }, false},
		{"missing source", func(c *database.PurchaseCatalog, _ *database.Product) { c.Source = nil }, false},
		{"mismatched source", func(c *database.PurchaseCatalog, _ *database.Product) { c.Source.ID++ }, false},
		{"offer ends exactly now", func(_ *database.PurchaseCatalog, p *database.Product) { p.OfferEndsAt = &now }, false},
		{"unrepresentable entitlement", func(c *database.PurchaseCatalog, _ *database.Product) {
			expiry := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
			c.Subscription.ExpiresAt = &expiry
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourceID := uint(1)
			catalog := &database.PurchaseCatalog{Now: now,
				Subscription: &database.Subscription{TelegramID: 44001, PlanID: 1, Status: "active", ProviderSourceID: &sourceID},
				Source:       &database.ProviderSource{ID: sourceID, Enabled: true, SubscriptionURL: "https://provider.example/sub/test"}}
			ends := now.Add(10 * time.Minute)
			product := &database.Product{OfferID: strings.Repeat("a", 32), Name: "Monthly", PlanID: 1,
				Plan: &database.Plan{Name: "paid", IsActive: true}, IsActive: true,
				PriceCents: 5000, Currency: "RUB", DurationDays: 30, OfferEndsAt: &ends}
			tc.mutate(catalog, product)
			deadline, err := validatePurchaseOffer(catalog, product)
			if !tc.allowed {
				require.ErrorIs(t, err, ErrOfferUnavailable)
				assert.True(t, deadline.IsZero())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, ends, deadline, "offer end caps the intent deadline")
			product.OfferEndsAt = nil
			deadline, err = validatePurchaseOffer(catalog, product)
			require.NoError(t, err)
			assert.Equal(t, now.Add(purchaseIntentTTL), deadline)
		})
	}
}

func TestPurchaseService_InvalidKeysAndMissingOffer(t *testing.T) {
	p, db, _, product := purchaseServiceFixture(t)
	key := "550e8400-e29b-41d4-a716-446655440000"
	for _, bad := range []string{"", "invalid", uuid.Nil.String(), "550e8400-e29b-11d4-a716-446655440000", "550e8400-e29b-41d4-0716-446655440000", strings.ToUpper(key), " " + key, key + " ", "{" + key + "}", "urn:uuid:" + key, strings.ReplaceAll(key, "-", ""), key + "," + key} {
		t.Run(bad, func(t *testing.T) {
			info, created, err := p.Create(context.Background(), product.OfferID, bad)
			require.ErrorIs(t, err, ErrInvalidPurchase)
			assert.Nil(t, info)
			assert.False(t, created)
		})
	}
	_, _, err := p.Create(context.Background(), strings.Repeat("f", 32), key)
	require.ErrorIs(t, err, ErrOfferUnavailable)
	for _, ref := range []string{"", "1", strings.Repeat("F", 32), strings.Repeat("g", 32)} {
		_, _, err := p.Create(context.Background(), ref, key)
		require.ErrorIs(t, err, ErrInvalidPurchase)
	}
	var count int64
	require.NoError(t, db.GetDB().Model(&database.Order{}).Count(&count).Error)
	assert.Zero(t, count)
	// A valid request after all rejected attempts still creates the canonical terms.
	info, created, err := p.Create(context.Background(), product.OfferID, key)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, product.PriceCents, info.AmountCents)
}

func TestPurchaseService_StalePaymentSettlement(t *testing.T) {
	for _, age := range []time.Duration{time.Minute, 10 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			p, db, sub, product := purchaseServiceFixture(t)
			ctx := context.Background()
			deadline := time.Now().UTC().Add(-age)
			providerID := uuid.New()
			old := &database.Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: database.OrderStatusPending,
				AmountCents: product.PriceCents, Currency: product.Currency, PaymentProvider: "platega",
				ProviderPaymentID: providerID.String(), PaymentExpiresAt: &deadline, CreatedAt: deadline.Add(-time.Hour)}
			require.NoError(t, db.GetDB().Create(old).Error)
			info, created, err := p.Create(ctx, product.OfferID, uuid.NewString())
			require.NoError(t, err)
			require.True(t, created)
			stored, err := db.GetOrderByID(ctx, old.ID)
			require.NoError(t, err)
			assert.Equal(t, database.OrderStatusExpired, stored.Status)
			assert.Equal(t, &deadline, stored.PaymentExpiresAt)
			before, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			// Same-plan confirmation requires no node provisioning. Exercise the real
			// callback and CAS with a real DB, not a mocked settlement decision.
			orders := NewOrderService(db, nil, NewSyncService(db, nil, nil), fakePaymentProvider{}, "", nil)
			confirmation, err := orders.ConfirmPayment(ctx, providerID, json.Number("50.00"), "RUB")
			require.NoError(t, err)
			require.NotNil(t, confirmation)
			assert.Equal(t, age == time.Minute, confirmation.Activated)
			after, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			if age == time.Minute {
				require.NotNil(t, after.ExpiresAt)
			} else {
				assert.Equal(t, before, after)
			}
			purchase, err := p.Order(ctx, info.OrderID)
			require.NoError(t, err)
			assert.Equal(t, database.OrderStatusPending, purchase.Status)
		})
	}
}
