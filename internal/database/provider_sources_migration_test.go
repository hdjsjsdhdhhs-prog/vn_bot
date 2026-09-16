package database

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration039ProviderSourcesUpDown(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite3", t.TempDir()+"/provider-sources.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, migrateTo(db, 38, false))
	assertProviderSourcesTableExists(t, db, false)

	require.NoError(t, migrateTo(db, 39, false))
	assertProviderSourcesTableExists(t, db, true)

	var defaultEnabled string
	require.NoError(t, db.QueryRow(`SELECT dflt_value FROM pragma_table_info('provider_sources') WHERE name = 'enabled'`).Scan(&defaultEnabled))
	assert.Equal(t, "TRUE", defaultEnabled)

	m, err := newMigration(db, false)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))
	assertProviderSourcesTableExists(t, db, false)
}

func assertProviderSourcesTableExists(t *testing.T, db *sql.DB, expected bool) {
	t.Helper()

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'provider_sources'`).Scan(&count))
	assert.Equal(t, expected, count == 1)
}
