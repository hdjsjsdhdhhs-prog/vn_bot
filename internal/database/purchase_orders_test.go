package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPurchaseMigration042_LegacyOrderSavePreservesNulls(t *testing.T) {
	sqlDB, cleanup := newSQLiteAtMigration30(t)
	t.Cleanup(cleanup)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, applyMigrations(sqlDB, 41))
	gdb, err := gorm.Open(gormsqlite.New(gormsqlite.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	svc := &Service{db: gdb}
	ctx := context.Background()
	// Migrations alone do not perform NewService's default-plan seeding.
	require.NoError(t, gdb.Create(&Plan{Name: FreePlanName, IsActive: true}).Error)
	sub := createTestSubscription(t, svc, 42001, "legacy-purchase", "legacy-purchase-client")
	// Insert before 042, without referencing any purchase/offer columns.
	result, err := sqlDB.Exec(`INSERT INTO products (plan_id, name, duration_days, price_cents, currency, is_active)
		VALUES (?, 'Legacy monthly', 30, 5000, 'RUB', 1)`, sub.PlanID)
	require.NoError(t, err)
	productID, err := result.LastInsertId()
	require.NoError(t, err)
	statuses := []OrderStatus{OrderStatusPending, OrderStatusPaid, OrderStatusExpired, OrderStatusCanceled}
	ids := make([]int64, len(statuses))
	createdAt := time.Now().UTC().Truncate(time.Second)
	for i, status := range statuses {
		result, err := sqlDB.Exec(`INSERT INTO orders
			(subscription_id, product_id, status, amount_cents, currency, payment_provider, provider_payment_id, created_at)
			VALUES (?, ?, ?, 5000, 'RUB', 'platega', ?, ?)`, sub.ID, productID, status, uuid.NewString(), createdAt)
		require.NoError(t, err)
		ids[i], err = result.LastInsertId()
		require.NoError(t, err)
	}
	// Use the real embedded migration and production migration runner, not AutoMigrate.
	require.NoError(t, runMigrations(sqlDB))
	version, dirty, err := migrationState(sqlDB)
	require.NoError(t, err)
	require.Equal(t, uint(expectedLatestMigrationVersion), version)
	require.False(t, dirty)

	assertNulls := func(id uint) {
		t.Helper()
		var allNull bool
		require.NoError(t, sqlDB.QueryRow(`SELECT purchase_id IS NULL AND buyer_telegram_id IS NULL
			AND purchase_key IS NULL AND purchase_expires_at IS NULL FROM orders WHERE id = ?`, id).Scan(&allNull))
		require.True(t, allNull, "all four purchase columns must remain SQL NULL")
	}
	for i, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			order, err := svc.GetOrderByID(ctx, uint(ids[i]))
			require.NoError(t, err)
			assertNulls(order.ID)
			original := *order
			order.PaymentURL = "https://payments.example/legacy"
			deadline := createdAt.Add(time.Hour)
			order.PaymentExpiresAt = &deadline
			for range 2 {
				// Save writes every model field, unlike Updates(struct), which skips zero values.
				require.NoError(t, svc.db.WithContext(ctx).Save(order).Error)
				assertNulls(order.ID)
				order, err = svc.GetOrderByID(ctx, order.ID)
				require.NoError(t, err)
			}
			assert.Nil(t, order.PurchaseID)
			assert.Nil(t, order.BuyerTelegramID)
			assert.Nil(t, order.PurchaseKey)
			assert.Nil(t, order.PurchaseExpiresAt)
			original.PaymentURL = order.PaymentURL
			original.PaymentExpiresAt = &deadline
			assert.Equal(t, &original, order, "Save must preserve all other legacy order data")
			assert.Equal(t, "https://payments.example/legacy", order.PaymentURL)
		})
	}
	// Negative control: verify that the actual 042 CHECK is active and rejects
	// the zero buyer value that a non-pointer Order field used to write on Save.
	_, err = sqlDB.Exec("UPDATE orders SET buyer_telegram_id = 0 WHERE id = ?", ids[0])
	require.ErrorContains(t, err, "CHECK constraint failed")
	assertNulls(uint(ids[0]))
}

func TestPurchaseOrder_NullableFieldsRoundTrip(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 42002, "purchase", "purchase-client")
	plan := &Plan{Name: "purchase-paid", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Monthly", DurationDays: 30, PriceCents: 5000, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	key := uuid.NewString()
	deadline := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	validate := func(c *PurchaseCatalog, p *Product) (time.Time, error) {
		require.Equal(t, sub.ID, c.Subscription.ID)
		require.Equal(t, product.ID, p.ID)
		return deadline, nil
	}
	record, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, validate)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, record.Order.PurchaseID)
	assert.Len(t, *record.Order.PurchaseID, 32)
	require.NotNil(t, record.Order.BuyerTelegramID)
	assert.Equal(t, sub.TelegramID, *record.Order.BuyerTelegramID)
	require.NotNil(t, record.Order.PurchaseKey)
	assert.Equal(t, key, *record.Order.PurchaseKey)
	require.Equal(t, &deadline, record.Order.PurchaseExpiresAt)
	stored, err := svc.ReadPurchaseOrder(ctx, sub.TelegramID, *record.Order.PurchaseID)
	require.NoError(t, err)
	assert.Equal(t, record.Order, stored.Order)
	stored.Order.Status = OrderStatusCanceled
	require.NoError(t, svc.db.Save(&stored.Order).Error, "populated pointers must preserve immutable purchase terms")
	replayed, created, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, key, validate)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, stored.Order, replayed.Order)
	_, err = svc.ReadPurchaseOrder(ctx, sub.TelegramID+1, *record.Order.PurchaseID)
	require.ErrorIs(t, err, ErrOrderNotFound)
}
