package service

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func customerLifecycleFixture(t *testing.T) (*database.Service, *SubscriptionService, CustomerSubscriptionTerms) {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "lifecycle.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	plan := &database.Plan{Name: "customer", TrafficLimit: 1 << 30, IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	source := &database.ProviderSource{Name: "configured", Type: "external", Enabled: true, SubscriptionURL: "https://upstream.example/private-feed", HWID: "private-hwid", UserAgent: "private-agent"}
	require.NoError(t, db.CreateProviderSource(context.Background(), source))
	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{GlobalSubURL: "https://customer.example/sub/"})
	terms := CustomerSubscriptionTerms{ProviderSourceID: source.ID, PlanID: plan.ID, ExpiresAt: time.Now().AddDate(0, 0, 30).In(time.FixedZone("customer-zone", -7*3600))}
	return db, svc, terms
}

func TestCustomerLifecycle_CreateAndLegacyIsolation(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	// Multiple sources must not change explicit assignment, even if an earlier
	// source is usable. Neither a provider type nor plan_nodes selects it.
	other := &database.ProviderSource{Name: "selected", Type: "another-provider", Enabled: true, SubscriptionURL: "https://other.example/feed"}
	require.NoError(t, db.CreateProviderSource(ctx, other))
	terms.ProviderSourceID = other.ID
	node := &database.Node{Name: "legacy", Type: database.NodeTypeFetch, IsActive: true, SubscriptionURL: "https://legacy.example/sub"}
	require.NoError(t, db.GetDB().Create(node).Error)
	require.NoError(t, db.GetDB().Create(&database.PlanNode{PlanID: terms.PlanID, NodeID: node.ID}).Error)
	syncSvc := NewSyncService(db, nil, nil)
	svc.SetSyncService(syncSvc)
	_, err := db.GetOrCreateInvite(ctx, 9876, "lifecycle-invite")
	require.NoError(t, err)
	before := time.Now().UTC()
	result, err := svc.Create(ctx, 12345, "", "lifecycle-invite", terms)
	require.NoError(t, err)
	sub := result.Subscription
	assert.NotZero(t, sub.ID)
	assert.Equal(t, int64(12345), sub.TelegramID)
	assert.Equal(t, "tgId_12345", sub.Username)
	require.NotNil(t, sub.ProviderSourceID)
	assert.Equal(t, other.ID, *sub.ProviderSourceID)
	assert.Nil(t, sub.ProviderSource, "creation must not return loaded provider credentials")
	assert.True(t, utils.IsValidSubscriptionToken(sub.Token))
	assert.Len(t, sub.Token, 64)
	assert.NotEqual(t, sub.SubscriptionID, sub.Token)
	assert.Equal(t, "active", sub.Status)
	require.NotNil(t, sub.ExpiresAt)
	assert.True(t, terms.ExpiresAt.Equal(*sub.ExpiresAt))
	assert.Equal(t, time.UTC, sub.ExpiresAt.Location())
	require.NotNil(t, sub.StartedAt)
	assert.False(t, sub.StartedAt.Before(before))
	assert.False(t, sub.StartedAt.After(time.Now()))
	assert.Equal(t, svc.cfg.SubURL(sub.Token), result.SubscriptionURL)
	assert.Equal(t, int64(9876), result.ReferrerTGID)
	assert.False(t, sub.IsPaid(), "granting access must not invent payment history")
	stored, err := db.GetByToken(ctx, sub.Token)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, stored.ID)
	assert.True(t, stored.ExpiresAt.Equal(terms.ExpiresAt))

	require.NoError(t, syncSvc.ReconcilePlanNodes(ctx, sub.ID))
	require.NoError(t, syncSvc.ApplyPlanToSubscription(ctx, sub.ID))
	require.NoError(t, db.Transaction(ctx, func(tx *gorm.DB) error {
		return syncSvc.ApplyPlanToSubscriptionInTx(ctx, tx, sub.ID, sub.PlanID)
	}))
	revoked, err := svc.ReconcileOrphanedClients(ctx)
	require.NoError(t, err)
	assert.Zero(t, revoked)
	nodes, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Empty(t, nodes, "provider creation/reconciliation must never enqueue legacy provisioning")
	targets, err := db.GetActiveSubscriptionsWithTrafficLimit(ctx)
	require.NoError(t, err)
	assert.Empty(t, targets, "local quotas must not be enforced against provider customers")
	_, traffic, err := svc.GetWithTraffic(ctx, sub.TelegramID)
	require.NoError(t, err)
	assert.Zero(t, traffic.LimitGB, "provider access must not inherit the local plan quota")
	assert.Zero(t, traffic.UsedGB)
	// The reminder worker queries in UTC, matching the persisted expiry.
	reminders, err := db.GetSubscriptionsExpiringInRange(ctx, terms.ExpiresAt.UTC().Add(-time.Minute), terms.ExpiresAt.UTC().Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, reminders, 1)
	assert.Equal(t, sub.Token, reminders[0].Token)

	// Existing bot/order callers return the same row, without changing terms.
	reused, err := svc.GetOrCreateSubscription(ctx, sub.TelegramID, "ignored", "")
	require.NoError(t, err)
	assert.Equal(t, sub.Token, reused.Token)
	assert.True(t, terms.ExpiresAt.Equal(*reused.ExpiresAt))
	duplicate, err := svc.Create(ctx, sub.TelegramID, "duplicate", "", terms)
	require.Error(t, err)
	assert.Nil(t, duplicate)
	assert.NotContains(t, err.Error(), sub.Token)

	legacy, err := svc.Create(ctx, 54321, "legacy", "")
	require.NoError(t, err)
	assert.Nil(t, legacy.Subscription.ProviderSourceID)
	assert.Nil(t, legacy.Subscription.ExpiresAt)
	assert.True(t, utils.IsValidSubscriptionToken(legacy.Subscription.Token))
	trial, err := db.CreateTrialSubscription(ctx, "", "trial-internal", "trial-client", time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Negative(t, trial.TelegramID)
	assert.Nil(t, trial.ProviderSourceID)
	assert.True(t, utils.IsValidSubscriptionToken(trial.Token))
}

func TestCustomerLifecycle_LegacyGetOrCreateAndExpiry(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	svc.SetSyncService(NewSyncService(db, nil, nil))
	free, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	node := &database.Node{Name: "legacy-free", Type: database.NodeTypeFetch, IsActive: true, SubscriptionURL: "https://legacy.example/sub"}
	require.NoError(t, db.GetDB().Create(node).Error)
	require.NoError(t, db.GetDB().Create(&database.PlanNode{PlanID: free.ID, NodeID: node.ID}).Error)

	sub, err := svc.GetOrCreateSubscription(ctx, 555, "legacy", "")
	require.NoError(t, err)
	assert.Equal(t, free.ID, sub.PlanID)
	assert.Nil(t, sub.ProviderSourceID)
	assert.Nil(t, sub.ExpiresAt)
	assert.True(t, utils.IsValidSubscriptionToken(sub.Token))
	nodes, err := db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1, "legacy creation must still provision free-plan nodes")

	// Expiry through the service must retain the legacy Free downgrade, also
	// when the transaction-scoped sync path is wired.
	past := time.Now().UTC().Add(-time.Hour)
	sub.PlanID = terms.PlanID
	sub.ExpiresAt = &past
	require.NoError(t, db.UpdateSubscription(ctx, sub))
	require.NoError(t, svc.ExpireSubscription(ctx, sub.ID))
	stored, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, free.ID, stored.PlanID)
	assert.Equal(t, "active", stored.Status)
	assert.Nil(t, stored.ExpiresAt)
	assert.Nil(t, stored.ProviderSourceID)
	assert.Equal(t, sub.Token, stored.Token)

	stored.Status = "revoked"
	require.NoError(t, db.UpdateSubscription(ctx, stored))
	revived, err := svc.GetOrCreateSubscription(ctx, sub.TelegramID, "legacy", "")
	require.NoError(t, err)
	assert.Equal(t, sub.ID, revived.ID)
	assert.Equal(t, sub.Token, revived.Token)
	assert.Equal(t, free.ID, revived.PlanID)
	assert.Equal(t, "active", revived.Status)
	assert.Nil(t, revived.ExpiresAt)
	assert.Nil(t, revived.ProviderSourceID)
	nodes, err = db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1, "legacy reanimation must restore free-plan provisioning")
	count, err := db.CountAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCustomerLifecycle_RejectsInvalidTermsAndSources(t *testing.T) {
	db, svc, valid := customerLifecycleFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		mutate   func(*CustomerSubscriptionTerms)
		customer int64
	}{
		{"missing source selection", func(v *CustomerSubscriptionTerms) { v.ProviderSourceID = 0 }, 1},
		{"missing source row", func(v *CustomerSubscriptionTerms) { v.ProviderSourceID = 999999 }, 1},
		{"missing plan", func(v *CustomerSubscriptionTerms) { v.PlanID = 999999 }, 1},
		{"zero expiry", func(v *CustomerSubscriptionTerms) { v.ExpiresAt = time.Time{} }, 1},
		{"expired", func(v *CustomerSubscriptionTerms) { v.ExpiresAt = time.Now().Add(-time.Second) }, 1},
		{"zero identity", func(*CustomerSubscriptionTerms) {}, 0},
		{"trial identity", func(*CustomerSubscriptionTerms) {}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terms := valid
			tc.mutate(&terms)
			result, err := svc.Create(ctx, tc.customer, "customer", "", terms)
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
	for _, tc := range []struct {
		name    string
		changes map[string]any
	}{
		{"disabled", map[string]any{"enabled": false}},
		{"empty URL", map[string]any{"subscription_url": ""}},
		{"bad URL", map[string]any{"subscription_url": "https://secret-host/%invalid"}},
		{"bad scheme", map[string]any{"subscription_url": "file:///private-feed"}},
		{"bad JSON", map[string]any{"headers": `{"Authorization":42}`}},
		{"bad header name", map[string]any{"headers": `{"private header":"private-value"}`}},
		{"bad header value", map[string]any{"headers": `{"Authorization":"private\r\nvalue"}`}},
		{"bad HWID", map[string]any{"hwid": "private\nvalue"}},
		{"bad agent", map[string]any{"user_agent": "private\rvalue"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &database.ProviderSource{Name: "invalid", Type: "external", Enabled: true, SubscriptionURL: "https://provider.example/private-feed"}
			require.NoError(t, db.CreateProviderSource(ctx, source))
			require.NoError(t, db.GetDB().Model(source).Updates(tc.changes).Error)
			terms := valid
			terms.ProviderSourceID = source.ID
			result, err := svc.Create(ctx, 222, "customer", "", terms)
			require.ErrorIs(t, err, database.ErrProviderSourceUnusable)
			assert.Nil(t, result)
			assert.NotContains(t, err.Error(), "private")
			assert.NotContains(t, err.Error(), "secret-host")
		})
	}
	for _, publicURL := range []string{"", "/sub", "https://user:password@customer.example/sub/", "https://customer.example/sub/?private=value"} {
		svc.cfg.GlobalSubURL = publicURL
		result, err := svc.Create(ctx, 222, "customer", "", valid)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.NotContains(t, err.Error(), "password")
	}
	count, err := db.CountAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Zero(t, count, "all failures must leave no customer subscription behind")
}

func TestCustomerLifecycle_ConcurrentCreation(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	const requests = 8
	start := make(chan struct{})
	results := make(chan *CreateResult, requests)
	errors := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := svc.Create(ctx, 333, "customer", "", terms)
			results <- result
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	successes, failures := 0, 0
	for result := range results {
		if result != nil {
			successes++
			stored, err := db.GetByToken(ctx, result.Subscription.Token)
			require.NoError(t, err)
			assert.Equal(t, result.Subscription.ID, stored.ID)
			assert.NotNil(t, stored.ProviderSourceID)
		}
	}
	for err := range errors {
		if err != nil {
			failures++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, requests-1, failures)
	count, err := db.CountAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCustomerLifecycle_ExpiryDoesNotGrantPerpetualAccess(t *testing.T) {
	db, svc, terms := customerLifecycleFixture(t)
	ctx := context.Background()
	result, err := svc.Create(ctx, 444, "customer", "", terms)
	require.NoError(t, err)
	sub := result.Subscription
	// A delayed expiry job must not erase a future extension.
	require.NoError(t, svc.ExpireSubscription(ctx, sub.ID))
	stored, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", stored.Status)
	assert.True(t, stored.ExpiresAt.Equal(terms.ExpiresAt))
	past := time.Now().UTC().Add(-time.Hour)
	sub.ExpiresAt = &past
	require.NoError(t, db.UpdateSubscription(ctx, sub))
	info, err := svc.GetPublicSubscriptionInfo(ctx, sub.Token)
	require.NoError(t, err)
	assert.Equal(t, "expired", info.Status)
	_, err = svc.GetOrCreateSubscription(ctx, sub.TelegramID, "customer", "")
	require.ErrorIs(t, err, ErrSubscriptionInactive)
	require.NoError(t, svc.ExpireSubscription(ctx, sub.ID))
	require.NoError(t, svc.ExpireSubscription(ctx, sub.ID))
	stored, err = db.GetByToken(ctx, sub.Token)
	require.NoError(t, err)
	assert.Equal(t, "expired", stored.Status)
	assert.True(t, stored.ExpiresAt.Equal(past))
	assert.Equal(t, sub.PlanID, stored.PlanID)
	assert.Equal(t, sub.ProviderSourceID, stored.ProviderSourceID)
	_, err = svc.GetOrCreateSubscription(ctx, sub.TelegramID, "customer", "")
	require.ErrorIs(t, err, ErrSubscriptionInactive)
	_, err = svc.DowngradeToFreePlan(ctx, stored)
	require.Error(t, err)
	free, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	_, err = svc.AdminSetPlan(ctx, stored.ID, free.ID, 0)
	require.Error(t, err)
	// Existing explicit admin extension preserves source and token, and does
	// not create plan_nodes. This is not a new renewal implementation.
	extended, err := svc.AdminSetPlan(ctx, stored.ID, stored.PlanID, 30)
	require.NoError(t, err)
	assert.Equal(t, sub.Token, extended.Token)
	assert.Equal(t, sub.ProviderSourceID, extended.ProviderSourceID)
	assert.True(t, extended.IsActive())
}
