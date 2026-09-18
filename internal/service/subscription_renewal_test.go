package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionRenewal_ProviderLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		offset       time.Duration
	}{
		{"active", "active", 30 * 24 * time.Hour},
		{"near expiry", "active", time.Minute},
		{"expired before worker", "active", -time.Hour},
		{"expired after worker", "expired", -time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, svc, terms := customerLifecycleFixture(t)
			ctx := context.Background()
			created, err := svc.Create(ctx, 81001, "renewal", "", terms)
			require.NoError(t, err)
			sub := created.Subscription
			// Wire sync and real legacy plan nodes so accidental fallback is observable.
			node := &database.Node{Name: "must-not-provision", Type: database.NodeTypeFetch, IsActive: true}
			require.NoError(t, db.GetDB().Create(node).Error)
			require.NoError(t, db.GetDB().Create(&database.PlanNode{PlanID: sub.PlanID, NodeID: node.ID}).Error)
			svc.SetSyncService(NewSyncService(db, nil, nil))
			expiry := time.Now().Add(tc.offset).In(time.FixedZone("customer", 5*3600))
			sub.ExpiresAt = &expiry
			sub.Status = tc.status
			sub.RemindersSent = 7
			require.NoError(t, db.UpdateSubscription(ctx, sub))
			var invalidTG int64
			var invalidSub string
			svc.SetInvalidateFunc(func(id int64) { invalidTG = id })
			svc.SetInvalidateBySubIDFunc(func(id string) { invalidSub = id })
			before := time.Now().UTC()
			renewed, err := svc.RenewSubscription(ctx, sub.ID, 30)
			require.NoError(t, err)
			assertRenewalIdentity(t, sub, renewed.Subscription)
			assert.Equal(t, created.SubscriptionURL, renewed.SubscriptionURL)
			require.NotNil(t, renewed.Subscription.ExpiresAt)
			assert.Equal(t, time.UTC, renewed.Subscription.ExpiresAt.Location())
			if tc.offset > 0 {
				assert.True(t, expiry.UTC().AddDate(0, 0, 30).Equal(*renewed.Subscription.ExpiresAt))
			} else {
				assert.False(t, renewed.Subscription.ExpiresAt.Before(before.AddDate(0, 0, 30)))
				assert.False(t, renewed.Subscription.ExpiresAt.After(time.Now().UTC().AddDate(0, 0, 30)))
			}
			assert.True(t, renewed.Subscription.IsActive())
			assert.Zero(t, renewed.Subscription.RemindersSent)
			assert.Nil(t, renewed.Subscription.ProviderSource)
			assert.Equal(t, sub.TelegramID, invalidTG)
			assert.Equal(t, sub.SubscriptionID, invalidSub)
			stored, err := db.GetByToken(ctx, sub.Token)
			require.NoError(t, err)
			assert.True(t, stored.ExpiresAt.Equal(*renewed.Subscription.ExpiresAt))
			assertRenewalIdentity(t, sub, stored)
			count, err := db.CountAllSubscriptions(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(1), count)
			nodes, err := db.GetBySubscriptionID(ctx, sub.ID)
			require.NoError(t, err)
			assert.Empty(t, nodes)
			targets, err := db.GetActiveSubscriptionsWithTrafficLimit(ctx)
			require.NoError(t, err)
			assert.Empty(t, targets, "renewal must not enable legacy quota processing")
			_, traffic, err := svc.GetWithTraffic(ctx, sub.TelegramID)
			require.NoError(t, err)
			assert.Zero(t, traffic.LimitGB)
			assert.Zero(t, traffic.UsedGB)
			// The service result is internal and cannot accidentally serialize IDs
			// or bearer credentials as a future HTTP response.
			encoded, err := json.Marshal(renewed)
			require.NoError(t, err)
			assert.JSONEq(t, `{}`, string(encoded))
		})
	}
}

func assertRenewalIdentity(t *testing.T, before, after *database.Subscription) {
	t.Helper()
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, before.SubscriptionID, after.SubscriptionID)
	assert.Equal(t, before.Token, after.Token)
	assert.Equal(t, before.ProviderSourceID, after.ProviderSourceID)
	assert.Equal(t, before.PlanID, after.PlanID)
	assert.Equal(t, before.ClientID, after.ClientID)
	assert.Equal(t, before.StartedAt, after.StartedAt)
	assert.Equal(t, before.PricePaidCents, after.PricePaidCents)
}

func TestSubscriptionRenewal_RejectsInvalidStateAndSource(t *testing.T) {
	for _, name := range []string{"revoked", "paused", "canceled", "perpetual", "disabled", "unusable", "missing source", "inactive plan", "trial", "invalid URL", "invalid days", "missing subscription"} {
		t.Run(name, func(t *testing.T) {
			db, svc, terms := customerLifecycleFixture(t)
			ctx := context.Background()
			created, err := svc.Create(ctx, 81002, "renewal", "", terms)
			require.NoError(t, err)
			sub := created.Subscription
			past := time.Now().UTC().Add(-time.Hour)
			sub.ExpiresAt = &past
			sub.Status = "expired"
			sub.RemindersSent = 7
			wantErr := database.ErrSubscriptionNotRenewable
			days, id := 30, sub.ID
			switch name {
			case "revoked", "paused", "canceled":
				sub.Status = name
			case "perpetual":
				sub.ExpiresAt = nil
			case "disabled":
				require.NoError(t, db.GetDB().Model(&database.ProviderSource{}).Where("id = ?", terms.ProviderSourceID).Update("enabled", false).Error)
				wantErr = database.ErrProviderSourceUnusable
			case "unusable":
				require.NoError(t, db.GetDB().Model(&database.ProviderSource{}).Where("id = ?", terms.ProviderSourceID).Update("headers", `{"Authorization":42}`).Error)
				wantErr = database.ErrProviderSourceUnusable
			case "missing source":
				// Model a corrupt/imported dangling link, impossible with normal FK enforcement.
				require.NoError(t, db.GetDB().Exec("PRAGMA foreign_keys=OFF").Error)
				require.NoError(t, db.GetDB().Model(&database.Subscription{}).Where("id = ?", sub.ID).Update("provider_source_id", 999999).Error)
				require.NoError(t, db.GetDB().Exec("PRAGMA foreign_keys=ON").Error)
				wantErr = database.ErrProviderSourceNotFound
			case "inactive plan":
				require.NoError(t, db.GetDB().Model(&database.Plan{}).Where("id = ?", sub.PlanID).Update("is_active", false).Error)
			case "trial":
				sub.TelegramID = -81002
			case "invalid URL":
				svc.cfg.GlobalSubURL = "/sub/"
				wantErr = nil
			case "invalid days":
				days = 0
				wantErr = database.ErrInvalidRenewalDays
			case "missing subscription":
				id = 999999
				wantErr = database.ErrSubscriptionNotFound
			}
			require.NoError(t, db.UpdateSubscription(ctx, sub))
			before, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			svc.SetInvalidateFunc(func(int64) { t.Error("failed renewal must not invalidate") })
			svc.SetInvalidateBySubIDFunc(func(string) { t.Error("failed renewal must not invalidate") })
			result, err := svc.RenewSubscription(ctx, id, days)
			require.Error(t, err)
			if wantErr != nil {
				require.ErrorIs(t, err, wantErr)
			}
			assert.Nil(t, result)
			assert.NotContains(t, err.Error(), sub.Token)
			assert.NotContains(t, err.Error(), "private-hwid")
			assert.NotContains(t, err.Error(), "private-feed")
			after, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			assert.Equal(t, before, after, "rejection must leave the complete subscription unchanged")
		})
	}
}

func TestSubscriptionRenewal_ConcurrentExtensions(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	created, err := svc.Create(ctx, 81003, "renewal", "", terms)
	require.NoError(t, err)
	// Exercise actual independent SQLite connections, not only the production
	// one-connection pool. SQLite's writer reservation must serialize the reads.
	sqlDB, err := db.GetDB().DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, db.GetDB().Exec("PRAGMA journal_mode=WAL").Error)
	const requests = 8
	start := make(chan struct{})
	results := make(chan *RenewalResult, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := svc.RenewSubscription(ctx, created.Subscription.ID, 1)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := make(map[int64]bool)
	for result := range results {
		require.NotNil(t, result)
		assertRenewalIdentity(t, created.Subscription, result.Subscription)
		seen[result.Subscription.ExpiresAt.UnixNano()] = true
	}
	assert.Len(t, seen, requests, "every extension must observe the previous committed expiry")
	stored, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	assert.True(t, stored.ExpiresAt.Equal(terms.ExpiresAt.UTC().AddDate(0, 0, requests)))
}

func TestSubscriptionRenewal_Rollback(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, 81004, "renewal", "", terms)
	require.NoError(t, err)
	before, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	require.NoError(t, db.GetDB().Exec(`CREATE TRIGGER reject_renewal AFTER UPDATE OF expires_at ON subscriptions BEGIN SELECT RAISE(ABORT, 'renewal write blocked'); END`).Error)
	svc.SetInvalidateFunc(func(int64) { t.Error("rolled back renewal invalidated cache") })
	result, err := svc.RenewSubscription(ctx, before.ID, 30)
	require.Error(t, err)
	assert.Nil(t, result)
	after, err := db.GetByID(ctx, before.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestSubscriptionRenewal_LegacySamePlanPreservesQuotaAndFreeBehavior(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	legacy, err := svc.Create(ctx, 81005, "legacy", "")
	require.NoError(t, err)
	free := legacy.Subscription
	result, err := svc.RenewSubscription(ctx, free.ID, 30)
	require.ErrorIs(t, err, database.ErrSubscriptionNotRenewable)
	assert.Nil(t, result)
	stored, err := db.GetByID(ctx, free.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.ExpiresAt)
	// The existing admin path selects the finite legacy plan explicitly.
	sub, err := svc.AdminSetPlan(ctx, free.ID, terms.PlanID, 30)
	require.NoError(t, err)
	node := &database.Node{Name: "renewal-legacy", Type: database.NodeTypeFetch, IsActive: true}
	require.NoError(t, db.GetDB().Create(node).Error)
	require.NoError(t, db.GetDB().Create(&database.PlanNode{PlanID: sub.PlanID, NodeID: node.ID}).Error)
	require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: database.SyncStatusActive}))
	require.NoError(t, db.GetDB().Model(&database.Subscription{}).Where("id = ?", sub.ID).Update("traffic_reminders_sent", 3).Error)
	svc.SetSyncService(NewSyncService(db, nil, nil))
	nodesBefore, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	renewed, err := svc.RenewSubscription(ctx, sub.ID, 7)
	require.NoError(t, err)
	assertRenewalIdentity(t, sub, renewed.Subscription)
	assert.True(t, renewed.Subscription.ExpiresAt.Equal(sub.ExpiresAt.UTC().AddDate(0, 0, 7)))
	assert.Nil(t, renewed.Subscription.ProviderSourceID)
	assert.Equal(t, 3, renewed.Subscription.TrafficRemindersSent)
	nodesAfter, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, nodesBefore, nodesAfter, "same-plan renewal must not enqueue updates that reset traffic")
	// Provider-only restrictions must not leak into legacy renewal.
	require.NoError(t, db.GetDB().Model(&database.ProviderSource{}).Where("id = ?", terms.ProviderSourceID).Update("enabled", false).Error)
	_, err = svc.RenewSubscription(ctx, sub.ID, 1)
	require.NoError(t, err)
}

func TestSubscriptionRenewal_InvalidDays(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, 81006, "renewal", "", terms)
	require.NoError(t, err)
	for _, days := range []int{-1, 0, database.MaxSubscriptionRenewalDays + 1} {
		result, err := svc.RenewSubscription(ctx, created.Subscription.ID, days)
		require.ErrorIs(t, err, database.ErrInvalidRenewalDays)
		assert.Nil(t, result)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.RenewSubscription(cancelled, created.Subscription.ID, 1)
	require.True(t, errors.Is(err, context.Canceled))
	stored, err := db.GetByID(ctx, created.Subscription.ID)
	require.NoError(t, err)
	assert.True(t, stored.ExpiresAt.Equal(terms.ExpiresAt))
}
