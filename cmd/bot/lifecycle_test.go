package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
)

// Startup must not depend on any VPN node: a brand-new database comes up with
// the current schema, no legacy node tables and the seeded plans, which is all
// that is needed to issue subscriptions through Builders.
func TestInitDatabase_StartsWithoutAnyVPNNodes(t *testing.T) {
	cfg := &config.Config{DatabasePath: filepath.Join(t.TempDir(), "bot.db")}

	svc, err := initDatabase(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, svc.Close()) })

	for _, table := range []string{"nodes", "plan_nodes", "subscription_nodes"} {
		assert.False(t, svc.GetDB().Migrator().HasTable(table), "legacy table %s must not exist", table)
	}

	for _, name := range []string{database.TrialPlanName, database.FreePlanName} {
		plan, err := svc.GetPlanByName(t.Context(), name)
		require.NoError(t, err, name)
		assert.NotZero(t, plan.ID, name)
	}
}
