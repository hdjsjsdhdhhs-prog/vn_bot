package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 051 removes the legacy VPN-node subsystem (nodes, plan_nodes,
// subscription_nodes, the panel traffic bitmask and the plan limits). These
// tests run the real migration files on the real service schema: a fresh
// install, and an upgrade from schema 50 that still holds legacy rows.

var legacyNodeTables = []string{"nodes", "plan_nodes", "subscription_nodes"}

func sqliteCount(t *testing.T, sqlDB *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, sqlDB.QueryRow(query, args...).Scan(&n))
	return n
}

func columnPresent(t *testing.T, sqlDB *sql.DB, table, column string) bool {
	t.Helper()
	return sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column) > 0
}

// assertNoLegacyNodeSchema checks that nothing of the old subsystem remains:
// tables, their indexes, the dropped columns and any dangling reference.
func assertNoLegacyNodeSchema(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	for _, table := range legacyNodeTables {
		assert.Zero(t, sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table), "table %s must be gone", table)
	}
	assert.Zero(t, sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_subscription_nodes_%'`))
	assert.False(t, columnPresent(t, sqlDB, "plans", "traffic_limit"))
	assert.False(t, columnPresent(t, sqlDB, "plans", "devices_limit"))
	assert.False(t, columnPresent(t, sqlDB, "subscriptions", "traffic_reminders_sent"))
	assert.Zero(t, sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM sqlite_master WHERE sql LIKE '%REFERENCES nodes%' OR sql LIKE '%REFERENCES plan_nodes%' OR sql LIKE '%REFERENCES subscription_nodes%'`),
		"no table may still reference a removed table")
}

func TestMigration051_FreshInstallHasNoLegacyNodes(t *testing.T) {
	svc := newTestService(t)
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)

	assertNoLegacyNodeSchema(t, sqlDB)

	version, dirty, err := migrationState(sqlDB)
	require.NoError(t, err)
	assert.False(t, dirty)
	latest, err := latestEmbeddedMigrationVersion()
	require.NoError(t, err)
	assert.Equal(t, uint(latest), version)

	// The service runs from the seeded plans alone: there is no node table to
	// provision anything on.
	for _, name := range []string{TrialPlanName, FreePlanName} {
		_, err := svc.GetPlanByName(t.Context(), name)
		require.NoError(t, err, name)
	}
}

func TestMigration051_UpgradeFromSchema50KeepsEverythingElse(t *testing.T) {
	f := newTrialFixture(t)
	ctx := f.ctx
	sqlDB, err := f.svc.db.DB()
	require.NoError(t, err)

	// Data owned by the architecture that stays: provider sources with their
	// catalogue, a builder with a rule, a subscription that points at both.
	_, err = f.svc.UpsertBuilderItem(ctx, SubscriptionBuilderItem{BuilderID: f.builder.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE", Enabled: true})
	require.NoError(t, err)
	sub := newProviderSourceTestSubscription(t, f.svc, 51001, "mig051-sub")
	sub.ProviderSourceID = &f.a.ID
	sub.SubscriptionBuilderID = &f.builder.ID
	require.NoError(t, f.svc.db.Create(sub).Error)

	// Roll back to schema 50 (restores the empty legacy tables and columns),
	// then fill them like a pre-051 production database would be.
	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(50))
	for _, table := range legacyNodeTables {
		require.Equal(t, 1, sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table), "schema 50 has %s", table)
	}

	exec := func(query string, args ...any) {
		t.Helper()
		_, err := sqlDB.Exec(query, args...)
		require.NoError(t, err, query)
	}
	exec(`INSERT INTO nodes (name, is_active, host, api_token, subscription_url, type, inbound_ids)
	      VALUES ('legacy-node', 1, 'https://panel.example', 'legacy-token', 'https://panel.example/sub', '3x-ui', '[1,2]')`)
	exec(`INSERT INTO plan_nodes (plan_id, node_id) VALUES (?, (SELECT id FROM nodes WHERE name = 'legacy-node'))`, f.plan.ID)
	exec(`INSERT INTO subscription_nodes (subscription_id, node_id, status, retry_count, updated_at)
	      VALUES (?, (SELECT id FROM nodes WHERE name = 'legacy-node'), 'pending_add', 0, ?)`, sub.ID, time.Now().UTC())
	exec(`UPDATE plans SET traffic_limit = 1073741824, devices_limit = 3`)
	exec(`UPDATE subscriptions SET traffic_reminders_sent = 5 WHERE id = ?`, sub.ID)
	for _, table := range legacyNodeTables {
		require.Equal(t, 1, sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM `+table), "legacy row in %s", table)
	}
	require.Equal(t, 1, sqliteCount(t, sqlDB, `PRAGMA foreign_keys`), "the service enforces foreign keys")

	retained := []struct{ table, order string }{
		{"provider_sources", "rowid"}, {"provider_source_entries", "rowid"},
		{"subscription_builders", "rowid"}, {"subscription_builder_sources", "rowid"}, {"subscription_builder_items", "rowid"},
		{"products", "rowid"}, {"orders", "rowid"}, {"trial_settings", "rowid"}, {"admin_audit_log", "rowid"},
		{"plans", "rowid"}, {"subscriptions", "rowid"},
	}
	snapshot := func() map[string][]map[string]any {
		out := map[string][]map[string]any{}
		for _, r := range retained {
			rows := tableRows(t, f.svc, r.table, r.order, "")
			for _, row := range rows {
				switch r.table {
				case "plans":
					delete(row, "traffic_limit")
					delete(row, "devices_limit")
				case "subscriptions":
					delete(row, "traffic_reminders_sent")
				}
			}
			out[r.table] = rows
		}
		return out
	}
	before := snapshot()
	require.NotEmpty(t, before["provider_source_entries"])
	require.NotEmpty(t, before["subscription_builder_items"])
	require.NotEmpty(t, before["subscriptions"])

	// Upgrade 50 -> 51 through the production migration path.
	require.NoError(t, applyMigrations(sqlDB, 51))

	assertNoLegacyNodeSchema(t, sqlDB)
	assert.Equal(t, before, snapshot(), "sources, catalogue, builders, rules, tariffs, orders and subscriptions are untouched")

	version, dirty, err := migrationState(sqlDB)
	require.NoError(t, err)
	assert.False(t, dirty)
	assert.Equal(t, uint(51), version)
	assert.Zero(t, sqliteCount(t, sqlDB, `SELECT COUNT(*) FROM pragma_foreign_key_check`))
	var integrity string
	require.NoError(t, sqlDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	assert.Equal(t, "ok", integrity)

	// The application keeps working with no node rows at all: the builder is
	// resolved from the subscription and new subscriptions can be stored.
	resolved, err := f.svc.ResolveSubscriptionBuilder(ctx, sub)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, f.builder.ID, resolved.Builder.ID)
	require.NoError(t, f.svc.db.Create(newProviderSourceTestSubscription(t, f.svc, 51002, "mig051-sub-2")).Error)
}
