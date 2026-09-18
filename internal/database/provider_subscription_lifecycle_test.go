package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProviderLifecycle_CreateRollbackDoesNotPublishPartialState(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	source := newProviderSourceTestSource(t, svc, "rollback")
	sub := newProviderSourceTestSubscription(t, svc, 12001, "rollback-provider")
	sub.ProviderSourceID = &source.ID
	failure := errors.New("injected post-insert failure")
	require.NoError(t, svc.db.Callback().Create().After("gorm:create").Register("lifecycle_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "subscriptions" {
			tx.AddError(failure)
		}
	}))
	err := svc.CreateSubscription(ctx, sub, "")
	require.ErrorIs(t, err, failure)
	assert.Zero(t, sub.ID)
	assert.Empty(t, sub.Token)
	count, err := svc.CountAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
	storedSource, err := svc.GetProviderSourceByID(ctx, source.ID)
	require.NoError(t, err)
	assert.True(t, storedSource.Enabled)
}

func TestProviderLifecycle_ConcurrentDisableCannotCommitStaleAssignment(t *testing.T) {
	svc := newTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Production uses a single connection; this test needs two to model an
	// independent writer rather than waiting for its own transaction to finish.
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	// WAL permits a second connection to commit while the first transaction
	// holds a read snapshot. Promoting that stale snapshot to a writer MUST fail.
	require.NoError(t, svc.db.Exec("PRAGMA journal_mode=WAL").Error)
	source := newProviderSourceTestSource(t, svc, "concurrent-disable")
	sub := newProviderSourceTestSubscription(t, svc, 12002, "concurrent-provider")
	sub.ProviderSourceID = &source.ID
	var disableErr error
	disabled := false
	require.NoError(t, svc.db.Callback().Query().After("gorm:query").Register("disable_after_source_read", func(tx *gorm.DB) {
		if tx.Statement.Table != "provider_sources" || disabled {
			return
		}
		disabled = true
		disableErr = svc.db.WithContext(ctx).Model(&ProviderSource{}).Where("id = ?", source.ID).Update("enabled", false).Error
	}))
	err = svc.CreateSubscription(ctx, sub, "")
	require.True(t, disabled)
	require.NoError(t, disableErr)
	require.Error(t, err, "a transaction cannot commit the source's stale enabled snapshot")
	assert.Zero(t, sub.ID)
	assert.Empty(t, sub.Token)
	count, err := svc.CountAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
	// A fresh attempt observes disabled rather than silently taking legacy nodes.
	require.ErrorIs(t, svc.CreateSubscription(ctx, sub, ""), ErrProviderSourceUnusable)
}

func TestProviderLifecycle_ExpiryQueryAndCAS(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	source := newProviderSourceTestSource(t, svc, "expiry")
	sub := newProviderSourceTestSubscription(t, svc, 12003, "expiry-provider")
	sub.ProviderSourceID = &source.ID
	sub.ExpiresAt = ptrTime(time.Now().UTC().Add(-time.Hour))
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	// Source-backed finite grants are selected even with a legacy free plan.
	due, err := svc.GetExpiredPaidSubscriptions(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, sub.ID, due[0].ID)
	// Renewal between scan and CAS must win.
	future := time.Now().UTC().Add(time.Hour)
	sub.ExpiresAt = &future
	require.NoError(t, svc.UpdateSubscription(ctx, sub))
	require.NoError(t, svc.ExpireProviderSubscription(ctx, sub.ID))
	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", stored.Status)
	assert.True(t, stored.ExpiresAt.Equal(future))
	sub.Status = "revoked"
	sub.ExpiresAt = ptrTime(time.Now().UTC().Add(-time.Hour))
	require.NoError(t, svc.UpdateSubscription(ctx, sub))
	require.NoError(t, svc.ExpireProviderSubscription(ctx, sub.ID))
	stored, err = svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "revoked", stored.Status)
}
