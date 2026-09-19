package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStarsMigration043_PopulatedUpDown(t *testing.T) {
	svc, sub, product, validate := purchaseFixture(t)
	ctx := context.Background()
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(42))
	// A pre-Stars order must not need any new fields to survive migration.
	_, err = sqlDB.Exec(`INSERT INTO orders (subscription_id,product_id,status,amount_cents,currency,payment_provider,provider_payment_id,created_at) VALUES (?,?,'paid',5000,'RUB','platega',?,?)`, sub.ID, product.ID, uuid.NewString(), time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, m.Migrate(43))
	before, err := svc.GetOrdersBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, before, 1)
	assert.Nil(t, before[0].StarsCheckoutID)
	assert.Nil(t, before[0].StarsCheckoutAt)
	record, _, err := svc.CreatePurchaseOrder(ctx, sub.TelegramID, product.OfferID, uuid.NewString(), validate)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, svc.db.Model(&Order{}).Where("id = ?", record.Order.ID).Updates(map[string]any{"stars_checkout_id": "checkout", "stars_checkout_at": now}).Error)
	receipt := &TelegramPaymentReceipt{TelegramChargeID: "charge", PayerTelegramID: sub.TelegramID, InvoicePayload: "stars:v1:" + *record.Order.PurchaseID, Currency: "XTR", TotalAmount: 75, TelegramDate: int(now.Unix())}
	require.NoError(t, svc.RecordTelegramPayment(ctx, receipt))
	require.NoError(t, svc.RecordTelegramPayment(ctx, &TelegramPaymentReceipt{TelegramChargeID: "charge", PayerTelegramID: sub.TelegramID, InvoicePayload: receipt.InvoicePayload, Currency: "XTR", TotalAmount: 75}))
	require.ErrorIs(t, svc.RecordTelegramPayment(ctx, &TelegramPaymentReceipt{TelegramChargeID: "charge", PayerTelegramID: sub.TelegramID, InvoicePayload: receipt.InvoicePayload, Currency: "XTR", TotalAmount: 76}), ErrStarsPurchaseInvalid)
	require.NoError(t, m.Migrate(42))
	var count int
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='telegram_payment_receipts'`).Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('orders') WHERE name IN ('stars_checkout_id','stars_checkout_at')`).Scan(&count))
	assert.Zero(t, count)
	// Non-Stars data is unchanged, including Purchase Foundation metadata.
	old, err := svc.GetOrderByID(ctx, before[0].ID)
	require.NoError(t, err)
	assert.Equal(t, &before[0], old)
	purchase, err := svc.GetOrderByID(ctx, record.Order.ID)
	require.NoError(t, err)
	assert.Equal(t, record.Order, *purchase)
	require.NoError(t, m.Migrate(43))
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&count))
	assert.Zero(t, count)
	var integrity string
	require.NoError(t, sqlDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	assert.Equal(t, "ok", integrity)
}
