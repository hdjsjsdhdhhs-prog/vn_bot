package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 049 (trial_settings) up/down with data, and concurrent saves of
// one settings version.

func trialSettingsTableCount(t *testing.T, f *trialFixture) int {
	t.Helper()
	sqlDB, err := f.svc.db.DB()
	require.NoError(t, err)
	var n int
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'trial_settings'`).Scan(&n))
	return n
}

func TestTrialSettingsMigration049_PopulatedUpDown(t *testing.T) {
	f := newTrialFixture(t)
	sqlDB, err := f.svc.db.DB()
	require.NoError(t, err)

	// A stored trial with its own composition on the trial plan's builder.
	c := &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID), Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID},
		Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"}}}
	d := f.draft(uptr(f.builder.ID), c)
	d.DurationHours, d.Title = 48, "Пробный"
	_, err = f.save(t, 0, d)
	require.NoError(t, err)
	require.Equal(t, 1, trialSettingsTableCount(t, f))

	plansBefore := tableRows(t, f.svc, "plans", "id", "")
	buildersBefore := tableRows(t, f.svc, "subscription_builders", "id", "")
	itemsBefore := tableRows(t, f.svc, "subscription_builder_items", "id", "")
	sourcesBefore := tableRows(t, f.svc, "subscription_builder_sources", "builder_id, source_id", "")
	auditBefore := tableRows(t, f.svc, "admin_audit_log", "id", "")

	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)

	// Rollback drops only trial_settings: the trial plan keeps its builder and
	// the builder keeps its rules, so /sub serves the same composition and the
	// trial falls back to the environment values.
	require.NoError(t, m.Migrate(48))
	assert.Zero(t, trialSettingsTableCount(t, f))
	assert.Equal(t, plansBefore, tableRows(t, f.svc, "plans", "id", ""))
	assert.Equal(t, buildersBefore, tableRows(t, f.svc, "subscription_builders", "id", ""))
	assert.Equal(t, itemsBefore, tableRows(t, f.svc, "subscription_builder_items", "id", ""))
	assert.Equal(t, sourcesBefore, tableRows(t, f.svc, "subscription_builder_sources", "builder_id, source_id", ""))
	assert.Equal(t, auditBefore, tableRows(t, f.svc, "admin_audit_log", "id", ""))

	// Up again: an empty table, the environment values apply.
	require.NoError(t, m.Migrate(49))
	require.Equal(t, 1, trialSettingsTableCount(t, f))
	eff, err := f.svc.GetTrialEffective(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.False(t, eff.Stored)
	assert.True(t, eff.Enabled)
	assert.Equal(t, trialTestDefaults.DurationHours, eff.DurationHours)
	assert.Equal(t, trialTestDefaults.RateLimitPerHour, eff.RateLimitPerHour)

	// The CHECK constraints mirror the service bounds and the single row.
	now := time.Now().UTC()
	insert := func(id, hours, rate int) error {
		_, err := sqlDB.Exec(`INSERT INTO trial_settings (id, duration_hours, rate_limit_per_hour, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			id, hours, rate, now, now)
		return err
	}
	for _, tc := range []struct {
		name            string
		id, hours, rate int
	}{
		{"second row", 2, 3, 3}, {"zero hours", 1, 0, 3}, {"above 168 hours", 1, 169, 3},
		{"zero rate", 1, 3, 0}, {"rate above 100", 1, 3, 101},
	} {
		assert.Error(t, insert(tc.id, tc.hours, tc.rate), tc.name)
	}
	require.NoError(t, insert(1, 168, 100))
	var enabled bool
	var features string
	var version int
	require.NoError(t, sqlDB.QueryRow(`SELECT enabled, features, version FROM trial_settings WHERE id = 1`).Scan(&enabled, &features, &version))
	assert.True(t, enabled, "enabled defaults to on")
	assert.Equal(t, "[]", features)
	assert.Equal(t, 1, version)
	eff, err = f.svc.GetTrialEffective(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.True(t, eff.Stored)
	assert.Equal(t, 168, eff.DurationHours)

	var count int
	require.NoError(t, sqlDB.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&count))
	assert.Zero(t, count)
	var integrity string
	require.NoError(t, sqlDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity))
	assert.Equal(t, "ok", integrity)
}

func TestRetryTrialSaveOnBusy(t *testing.T) {
	t.Parallel()
	busy := fmt.Errorf("update trial settings: %w", sqlite3.Error{Code: sqlite3.ErrBusy})
	locked := sqlite3.Error{Code: sqlite3.ErrLocked}
	run := func(ctx context.Context, results ...error) (error, int) {
		calls := 0
		err := retryTrialSaveOnBusy(ctx, 3, time.Millisecond, func() error {
			calls++
			if calls <= len(results) {
				return results[calls-1]
			}
			return nil
		})
		return err, calls
	}

	err, calls := run(context.Background(), busy, locked)
	require.NoError(t, err, "busy and locked are retried")
	assert.Equal(t, 3, calls)

	err, calls = run(context.Background(), busy, busy, busy, busy)
	assert.Equal(t, 3, calls, "at most 3 attempts")
	assert.True(t, isSQLiteBusy(err), "the last lock error is returned as is")

	for _, other := range []error{
		ErrTrialVersionConflict,
		&TrialFieldError{Field: "duration_hours", Reason: "out_of_range"},
		fmt.Errorf("create trial settings: %w", sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintPrimaryKey}),
		errors.New("disk I/O"),
	} {
		err, calls = run(context.Background(), other)
		assert.Same(t, other, err, "%v is not retried nor rewrapped", other)
		assert.Equal(t, 1, calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err, calls = run(ctx, busy, busy)
	assert.Equal(t, 1, calls, "a cancelled request stops retrying")
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, isSQLiteBusy(err))
}

// The service runs with one connection (config.MaxOpenConns), which serializes
// transactions. This test widens the pool so the saves really overlap: the
// version check must still let exactly one through, with no primary-key or
// locking error leaking out of the others.
func TestTrial_ConcurrentSavesOfOneVersion(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	sqlDB, err := f.svc.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(5)
	sqlDB.SetMaxIdleConns(5)
	const requests = 8

	// Round 0 races the first INSERT of the row, round 1 the versioned UPDATE.
	for version := range 2 {
		var wg sync.WaitGroup
		errs := make(chan error, requests)
		start := make(chan struct{})
		for i := range requests {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				d := f.draft(nil, nil)
				d.DurationHours = 10*version + i + 1
				_, err := f.svc.UpdateTrial(f.ctx, AdminConfigMeta{Actor: "admin", RequestKey: fmt.Sprintf("concurrent-trial-%d-%d", version, i)},
					TrialUpdateInput{Version: version, Defaults: trialTestDefaults, TrialDraft: d})
				errs <- err
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)

		assert.Greater(t, sqlDB.Stats().OpenConnections, 1, "round %d ran on several connections", version)
		saved := 0
		for err := range errs {
			if err == nil {
				saved++
				continue
			}
			require.ErrorIs(t, err, ErrTrialVersionConflict, "round %d: only version_conflict, no PK or locking error", version)
		}
		assert.Equal(t, 1, saved, "round %d: exactly one editor of version %d wins", version, version)

		var rows int64
		require.NoError(t, f.svc.db.Model(&TrialSettings{}).Count(&rows).Error)
		assert.Equal(t, int64(1), rows)
		eff, err := f.svc.GetTrialEffective(f.ctx, trialTestDefaults)
		require.NoError(t, err)
		assert.Equal(t, version+1, eff.Version)
		var audits int64
		require.NoError(t, f.svc.db.Model(&AdminAuditLog{}).Where("action = ?", string(AdminActionTrialUpdated)).Count(&audits).Error)
		assert.Equal(t, int64(version+1), audits, "round %d: rejected saves leave no audit row", version)
	}
}
