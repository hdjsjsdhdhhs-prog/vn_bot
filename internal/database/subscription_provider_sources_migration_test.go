package database

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration040SubscriptionProviderSourceUpDown(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite3", t.TempDir()+"/subscription-provider-source.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, migrateTo(db, 39, false))
	require.NoError(t, insertMigrationTestSubscription(db))
	assertSubscriptionProviderSourceColumnExists(t, db, false)

	require.NoError(t, migrateTo(db, 40, false))
	assertSubscriptionProviderSourceColumnExists(t, db, true)
	assertSubscriptionProviderSourceIndexExists(t, db, true)

	var providerSourceID sql.NullInt64
	require.NoError(t, db.QueryRow(`SELECT provider_source_id FROM subscriptions WHERE subscription_id = 'migration-040-sub'`).Scan(&providerSourceID))
	assert.False(t, providerSourceID.Valid)

	result, err := db.Exec(`INSERT INTO provider_sources (name, type, subscription_url) VALUES ('Migration provider', 'external', 'https://provider.example/sub/migration')`)
	require.NoError(t, err)
	sourceID, err := result.LastInsertId()
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE subscriptions SET provider_source_id = ? WHERE subscription_id = 'migration-040-sub'`, sourceID)
	require.NoError(t, err)

	m, err := newMigration(db, false)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))
	assertSubscriptionProviderSourceColumnExists(t, db, false)
	assertSubscriptionProviderSourceIndexExists(t, db, false)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM subscriptions WHERE subscription_id = 'migration-040-sub'`).Scan(&count))
	assert.Equal(t, 1, count)
}

func insertMigrationTestSubscription(db *sql.DB) error {
	_, err := db.Exec(`INSERT INTO subscriptions (telegram_id, client_id, subscription_id, status)
		VALUES (40040, 'migration-040-client', 'migration-040-sub', 'active')`)
	return err
}

func assertSubscriptionProviderSourceColumnExists(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'provider_source_id'`).Scan(&count))
	assert.Equal(t, expected, count == 1)
}

func assertSubscriptionProviderSourceIndexExists(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_subscriptions_provider_source_id'`).Scan(&count))
	assert.Equal(t, expected, count == 1)
}
