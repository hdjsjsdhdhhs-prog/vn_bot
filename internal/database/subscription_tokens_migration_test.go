package database

import (
	"database/sql"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration041BackfillsUniqueSubscriptionTokens(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite3", t.TempDir()+"/subscription-tokens.db")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, migrateTo(db, 40, false))
	for i := 1; i <= 3; i++ {
		_, err = db.Exec(`INSERT INTO subscriptions (telegram_id, client_id, subscription_id, status)
			VALUES (?, ?, ?, 'active')`, 42000+i, "migration-token-client-"+string(rune('0'+i)), "migration-token-sub-"+string(rune('0'+i)))
		require.NoError(t, err)
	}

	require.NoError(t, runMigrations(db))

	rows, err := db.Query(`SELECT token FROM subscriptions ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	tokens := make(map[string]struct{})
	for rows.Next() {
		var token string
		require.NoError(t, rows.Scan(&token))
		assert.NotEmpty(t, token)
		assert.True(t, utils.IsValidSubscriptionToken(token))
		tokens[token] = struct{}{}
	}
	require.NoError(t, rows.Err())
	assert.Len(t, tokens, 3)

	var indexCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_subscriptions_token'`).Scan(&indexCount))
	assert.Equal(t, 1, indexCount)

	_, err = db.Exec(`INSERT INTO subscriptions (telegram_id, client_id, subscription_id, status)
		VALUES (42999, 'missing-token-client', 'missing-token-sub', 'active')`)
	assert.ErrorContains(t, err, "subscription token is required")

	_, err = db.Exec(`INSERT INTO subscriptions (telegram_id, client_id, subscription_id, token, status)
		VALUES (43000, 'invalid-token-client', 'invalid-token-sub', 'invalid', 'active')`)
	assert.ErrorContains(t, err, "subscription token must be 64 lowercase hexadecimal characters")

	m, err := newMigration(db, false)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Steps(-1))

	var subscriptionCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM subscriptions`).Scan(&subscriptionCount))
	assert.Equal(t, 3, subscriptionCount)

	var tokenColumnCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'token'`).Scan(&tokenColumnCount))
	assert.Zero(t, tokenColumnCount)
}
