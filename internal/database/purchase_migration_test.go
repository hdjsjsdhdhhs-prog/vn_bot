package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPurchaseMigration042_DownPopulated(t *testing.T) {
	svc, sub, product, validate := purchaseFixture(t)
	ctx := context.Background()
	for _, status := range []OrderStatus{OrderStatusPaid, OrderStatusExpired, OrderStatusCanceled, OrderStatusPending} {
		legacy := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: status,
			AmountCents: 5000, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: uuid.NewString(),
			PaymentURL: "https://payments.example/legacy", PaymentExpiresAt: ptrTime(time.Now().UTC().Add(time.Hour)), CreatedAt: time.Now().UTC()}
		// Create the purchase before the legacy pending payment so each generation
		// of rows is populated without bypassing the production purchase path.
		purchase, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, uuid.NewString(), validate)
		require.NoError(t, err)
		require.True(t, created)
		require.NoError(t, svc.db.Model(&Order{}).Where("id = ?", purchase.Order.ID).Update("status", status).Error)
		require.NoError(t, svc.db.Create(legacy).Error)
	}
	before, err := svc.GetOrdersBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, before, 8)
	for i := range before {
		before[i].PurchaseID = nil
		before[i].BuyerTelegramID = nil
		before[i].PurchaseKey = nil
		before[i].PurchaseExpiresAt = nil
	}
	var originalProduct Product
	require.NoError(t, svc.db.First(&originalProduct, product.ID).Error)
	originalProduct.OfferID = ""
	originalProduct.OfferEndsAt = nil
	originalSub, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)
	require.NoError(t, m.Steps(-1), "execute the real embedded down migration on populated tables")
	version, dirty, err := migrationState(sqlDB)
	require.NoError(t, err)
	assert.Equal(t, uint(41), version)
	assert.False(t, dirty)
	after, err := svc.GetOrdersBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "down must retain every non-purchase order field")
	var afterProduct Product
	require.NoError(t, svc.db.First(&afterProduct, product.ID).Error)
	assert.Equal(t, originalProduct, afterProduct)
	afterSub, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, originalSub, afterSub)
	for _, query := range []string{
		`SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name IN ('purchase_id','buyer_telegram_id','purchase_key','purchase_expires_at')`,
		`SELECT COUNT(*) FROM pragma_table_info('products') WHERE name IN ('offer_id','offer_ends_at')`,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('orders_purchase_identity_immutable','orders_purchase_required_insert','idx_orders_purchase_pending','idx_orders_purchase_key','idx_orders_purchase_id','idx_products_offer_id')`,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`,
	} {
		var count int
		require.NoError(t, sqlDB.QueryRow(query).Scan(&count))
		assert.Zero(t, count, query)
	}
	var integrity string
	require.NoError(t, sqlDB.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	assert.Equal(t, "ok", integrity)
	// A pre-042 application can still write and update ordinary orders.
	_, err = sqlDB.Exec(`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, created_at)
  VALUES (?, ?, 'paid', 1234, 'RUB', CURRENT_TIMESTAMP)`, sub.ID, product.ID)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`UPDATE orders SET payment_url = 'https://payments.example/updated' WHERE status = 'paid'`)
	require.NoError(t, err)
	// Existing provider duplicate protection remains present after down.
	_, err = sqlDB.Exec(`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, payment_provider, created_at)
  VALUES (?, ?, 'pending', 5000, 'RUB', 'platega', CURRENT_TIMESTAMP)`, sub.ID, product.ID)
	require.ErrorContains(t, err, "UNIQUE constraint failed")
}
