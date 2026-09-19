package testutil

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewTestDatabaseServiceClosesBeforeTempDirCleanup(t *testing.T) {
	require.NoError(t, InitLogger(t))
	var sqlDB *sql.DB
	t.Run("database owner", func(t *testing.T) {
		db, err := NewTestDatabaseService(t)
		require.NoError(t, err)
		sqlDB, err = db.GetDB().DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Ping())
	})
	// Subtest cleanups have run; a leaked pool would still accept Ping on Unix
	// and would also prevent TempDir cleanup on Windows.
	require.NotNil(t, sqlDB)
	require.ErrorContains(t, sqlDB.Ping(), "database is closed")
}
