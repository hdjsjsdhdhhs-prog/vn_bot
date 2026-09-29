package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 046 on a populated database: existing products keep their IDs,
// offer references and terms; orders and subscriptions keep pointing at them;
// the new columns get safe defaults; (plan_id, duration_days) is no longer
// unique so a used product can be versioned.
func TestTariffMigration046_PopulatedDownUp(t *testing.T) {
	f := newTariffFixture(t)
	ctx := context.Background()
	original := f.create("Месяц", 19900)
	order, sub := f.useInOrder(original, 710001)

	sqlDB, err := f.svc.db.DB()
	require.NoError(t, err)
	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(45), "046 down on a populated catalogue")
	var cols int
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('products')
		WHERE name IN ('description','features','badge','sort_order','version','previous_id')`).Scan(&cols))
	assert.Zero(t, cols)
	// 045 semantics are restored: one product per plan and duration.
	_, err = sqlDB.Exec(`INSERT INTO products (plan_id, name, duration_days, price_cents, currency, is_active) VALUES (?, 'dup', 30, 1, 'RUB', 1)`, f.plan.ID)
	require.ErrorContains(t, err, "UNIQUE constraint failed")

	// Up through the production path: 046 rebuilds products with
	// PRAGMA foreign_keys, so it runs alone on the NoTxWrap driver.
	require.NoError(t, applyMigrations(sqlDB, 46), "046 up on a populated catalogue")
	version, dirty, err := migrationState(sqlDB)
	require.NoError(t, err)
	assert.Equal(t, uint(46), version)
	assert.False(t, dirty)

	upgraded := f.product(original.ID)
	assert.Equal(t, original.OfferID, upgraded.OfferID, "the public offer reference survives the rebuild")
	assert.Equal(t, original.Name, upgraded.Name)
	assert.Equal(t, original.PriceCents, upgraded.PriceCents)
	assert.Equal(t, original.DurationDays, upgraded.DurationDays)
	assert.Equal(t, original.IsActive, upgraded.IsActive)
	assert.Equal(t, 0, upgraded.SortOrder, "existing rows keep the historical price order")
	assert.Equal(t, 1, upgraded.Version)
	assert.Equal(t, "[]", upgraded.Features)
	assert.Empty(t, upgraded.Description)
	assert.Empty(t, upgraded.Badge)
	assert.Nil(t, upgraded.PreviousID)

	var reloadedOrder Order
	require.NoError(t, f.svc.db.First(&reloadedOrder, order.ID).Error)
	assert.Equal(t, original.ID, reloadedOrder.ProductID)
	reloadedSub, err := f.svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, reloadedSub.ProductID)
	assert.Equal(t, original.ID, *reloadedSub.ProductID)

	for _, query := range []string{
		`SELECT COUNT(*) FROM pragma_foreign_key_check`,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'products_new'`,
	} {
		var n int
		require.NoError(t, sqlDB.QueryRow(query).Scan(&n))
		assert.Zero(t, n, query)
	}
	var indexes int
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'products'
		AND name IN ('idx_products_plan', 'idx_products_offer_id')`).Scan(&indexes))
	assert.Equal(t, 2, indexes)
	complete, err := foreignKeysMigrationSchemaComplete(sqlDB, 46)
	require.NoError(t, err)
	assert.True(t, complete, "the schema recovery verifier recognises a finished 046")

	// The used product can now be versioned for the same plan and duration.
	res, err := f.svc.UpdateTariff(ctx, f.meta(), TariffUpdateInput{ID: original.ID, Version: upgraded.Version, TariffInput: f.input("Месяц", 29900)})
	require.NoError(t, err)
	assert.NotEqual(t, original.ID, res.TargetID)

	var integrity string
	require.NoError(t, sqlDB.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	assert.Equal(t, "ok", integrity)
}
