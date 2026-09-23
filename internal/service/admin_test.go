package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminVPNClient is a goroutine-safe recording vpn.Client: concurrent admin
// mutations on one subscription are serialized by the sync lock, but the
// counters are read from the test goroutine afterwards.
type adminVPNClient struct {
	mu        sync.Mutex
	created   int
	updated   int
	deleted   int
	reset     int
	createErr error
	deleteErr error
}

func (c *adminVPNClient) CreateSubscription(context.Context, vpn.SubscriptionProvision) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created++
	return c.createErr
}

func (c *adminVPNClient) UpdateSubscription(context.Context, vpn.SubscriptionProvision) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updated++
	return nil
}

func (c *adminVPNClient) DeleteSubscription(context.Context, vpn.SubscriptionProvision) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted++
	return c.deleteErr
}

func (c *adminVPNClient) ResetTraffic(context.Context, vpn.SubscriptionProvision) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reset++
	return nil
}

func (c *adminVPNClient) Close() error { return nil }

func (c *adminVPNClient) counts() (created, updated, deleted, reset int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.created, c.updated, c.deleted, c.reset
}

// adminServiceFixture wires the real SQLite repository, the real
// SubscriptionService (for cache invalidation) and the real SyncService with
// recording VPN clients, so the post-commit contract is exercised end to end.
type adminServiceFixture struct {
	db    *database.Service
	subs  *SubscriptionService
	sync  *SyncService
	admin *AdminService
	plan  *database.Plan
	node1 *database.Node
	node2 *database.Node
	vpn   map[uint]*adminVPNClient
	sub   *database.Subscription

	mu             sync.Mutex
	invalidatedTG  []int64
	invalidatedSub []string
}

const (
	adminFixtureTelegramID = int64(91001)
	adminFixtureSubID      = "s-admin-target"
)

// newAdminServiceFixture creates a two-node plan and one subscription in the
// given lifecycle state. withNodes provisions both plan nodes as active
// bindings (the normal state of a live legacy subscription).
func newAdminServiceFixture(t *testing.T, status string, expiresAt *time.Time, withNodes bool) *adminServiceFixture {
	t.Helper()
	ctx := context.Background()
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	f := &adminServiceFixture{db: db, vpn: map[uint]*adminVPNClient{}}
	f.plan = &database.Plan{Name: "admin-plan", TrafficLimit: 1 << 30, IsActive: true}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(f.plan).Error)
	f.node1 = &database.Node{Name: "admin-node-1", Type: database.NodeType3xUI, IsActive: true, Host: "http://a1", APIToken: "t1", InboundIDs: `[1]`}
	f.node2 = &database.Node{Name: "admin-node-2", Type: database.NodeType3xUI, IsActive: true, Host: "http://a2", APIToken: "t2", InboundIDs: `[1]`}
	for _, n := range []*database.Node{f.node1, f.node2} {
		require.NoError(t, db.GetDB().WithContext(ctx).Create(n).Error)
		require.NoError(t, db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: f.plan.ID, NodeID: n.ID}).Error)
		f.vpn[n.ID] = &adminVPNClient{}
	}

	f.sub = &database.Subscription{
		TelegramID: adminFixtureTelegramID, Username: "admin-target", ClientID: "c-admin-target",
		SubscriptionID: adminFixtureSubID, Status: "active", PlanID: f.plan.ID, ExpiresAt: expiresAt,
	}
	require.NoError(t, db.CreateSubscription(ctx, f.sub, ""))
	f.sub.Status = status
	f.sub.RemindersSent = 3
	require.NoError(t, db.UpdateSubscription(ctx, f.sub))
	f.sub, err = db.GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	if withNodes {
		for _, n := range []*database.Node{f.node1, f.node2} {
			require.NoError(t, db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: f.sub.ID, NodeID: n.ID, Status: database.SyncStatusActive}))
		}
	}

	nodes := []database.Node{*f.node1, *f.node2}
	vpnClients := make(map[uint]vpn.Client, len(f.vpn))
	for id, c := range f.vpn {
		vpnClients[id] = c
	}
	f.subs = NewSubscriptionService(db, nil, vpnClients, nodes, &config.Config{GlobalSubURL: "https://admin.example/sub/"})
	f.subs.SetInvalidateFunc(func(id int64) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.invalidatedTG = append(f.invalidatedTG, id)
	})
	f.subs.SetInvalidateBySubIDFunc(func(id string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.invalidatedSub = append(f.invalidatedSub, id)
	})
	f.sync = NewSyncService(db, vpnClients, nodes)
	f.admin = NewAdminService(db, f.subs, f.sync)
	return f
}

func (f *adminServiceFixture) mutation(action database.AdminAction, key string) database.AdminMutation {
	return database.AdminMutation{Actor: "admin", RequestKey: key, Action: action, SubscriptionID: f.sub.ID, Days: 30}
}

func (f *adminServiceFixture) invalidations() ([]int64, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.invalidatedTG...), append([]string(nil), f.invalidatedSub...)
}

func (f *adminServiceFixture) nodeStatuses(t *testing.T) map[uint]database.SyncStatus {
	t.Helper()
	rows, err := f.db.GetBySubscriptionID(context.Background(), f.sub.ID)
	require.NoError(t, err)
	out := make(map[uint]database.SyncStatus, len(rows))
	for _, r := range rows {
		out[r.NodeID] = r.Status
	}
	return out
}

func (f *adminServiceFixture) nodeRow(t *testing.T, nodeID uint) database.SubscriptionNode {
	t.Helper()
	rows, err := f.db.GetBySubscriptionID(context.Background(), f.sub.ID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.NodeID == nodeID {
			return r
		}
	}
	t.Fatalf("node %d has no binding", nodeID)
	return database.SubscriptionNode{}
}

func (f *adminServiceFixture) auditRows(t *testing.T) []database.AdminAuditLog {
	t.Helper()
	rows, err := f.db.ListAdminAudit(context.Background(), database.AdminAuditFilter{SubscriptionID: f.sub.ID})
	require.NoError(t, err)
	return rows
}

func (f *adminServiceFixture) stored(t *testing.T) *database.Subscription {
	t.Helper()
	sub, err := f.db.GetByID(context.Background(), f.sub.ID)
	require.NoError(t, err)
	return sub
}

func (f *adminServiceFixture) assertInvalidatedTimes(t *testing.T, n int, msgAndArgs ...any) {
	t.Helper()
	tg, sub := f.invalidations()
	require.Len(t, tg, n, msgAndArgs...)
	require.Len(t, sub, n, msgAndArgs...)
	for i := 0; i < n; i++ {
		assert.Equal(t, adminFixtureTelegramID, tg[i])
		assert.Equal(t, adminFixtureSubID, sub[i])
	}
}

// assertViewHidesBearerMaterial guards the browser projection: the token,
// subscription_id and client_id are bearer credentials and must never be
// serialized for the admin UI.
func assertViewHidesBearerMaterial(t *testing.T, view AdminSubscriptionView, sub *database.Subscription) {
	t.Helper()
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	body := string(raw)
	assert.NotContains(t, body, sub.Token)
	assert.NotContains(t, body, sub.ClientID)
	assert.NotContains(t, body, sub.SubscriptionID)
	assert.NotContains(t, body, `"token"`)
	assert.NotContains(t, body, `"client_id"`)
	assert.Contains(t, body, `"telegram_id"`)
}

// fakeAdminRepo is an AdminRepository substitute for cases the SQLite
// repository cannot produce on demand (infrastructure errors, argument capture).
type fakeAdminRepo struct {
	mutate    func(ctx context.Context, m database.AdminMutation) (*database.AdminMutationResult, error)
	dashboard func(ctx context.Context) (*database.AdminDashboard, error)
	listUsers func(ctx context.Context, f database.AdminUserFilter) ([]database.AdminUserRow, int64, error)
	getSub    func(ctx context.Context, id uint, auditLimit int) (*database.AdminSubscriptionDetail, error)
	listAudit func(ctx context.Context, f database.AdminAuditFilter) ([]database.AdminAuditLog, error)

	mu          sync.Mutex
	mutateCalls int
}

var errFakeAdminRepo = errors.New("fake admin repo: not configured")

func (r *fakeAdminRepo) AdminMutateSubscription(ctx context.Context, m database.AdminMutation) (*database.AdminMutationResult, error) {
	r.mu.Lock()
	r.mutateCalls++
	r.mu.Unlock()
	if r.mutate == nil {
		return nil, errFakeAdminRepo
	}
	return r.mutate(ctx, m)
}

func (r *fakeAdminRepo) GetAdminDashboard(ctx context.Context) (*database.AdminDashboard, error) {
	if r.dashboard == nil {
		return nil, errFakeAdminRepo
	}
	return r.dashboard(ctx)
}

func (r *fakeAdminRepo) ListAdminUsers(ctx context.Context, f database.AdminUserFilter) ([]database.AdminUserRow, int64, error) {
	if r.listUsers == nil {
		return nil, 0, errFakeAdminRepo
	}
	return r.listUsers(ctx, f)
}

func (r *fakeAdminRepo) GetAdminSubscription(ctx context.Context, id uint, auditLimit int) (*database.AdminSubscriptionDetail, error) {
	if r.getSub == nil {
		return nil, errFakeAdminRepo
	}
	return r.getSub(ctx, id, auditLimit)
}

func (r *fakeAdminRepo) ListAdminAudit(ctx context.Context, f database.AdminAuditFilter) ([]database.AdminAuditLog, error) {
	if r.listAudit == nil {
		return nil, errFakeAdminRepo
	}
	return r.listAudit(ctx, f)
}

// --- Renew ---------------------------------------------------------------

func TestAdminService_Mutate_RenewExtendsWithoutTouchingNodes(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	f := newAdminServiceFixture(t, "active", &future, true)

	outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionRenew, "renew-1"))
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.False(t, outcome.Replayed)
	assert.Equal(t, "active", outcome.Subscription.Status)
	require.NotNil(t, outcome.Subscription.ExpiresAt)
	assert.True(t, future.AddDate(0, 0, 30).Equal(*outcome.Subscription.ExpiresAt))
	assert.Zero(t, outcome.Subscription.RemindersSent, "renew resets reminder bitmask")
	assert.True(t, outcome.Audit.Success)
	assert.Equal(t, "renew", outcome.Audit.Action)
	assert.Equal(t, f.sub.ID, outcome.Audit.SubscriptionID)
	assertViewHidesBearerMaterial(t, outcome.Subscription, f.sub)

	// Active bindings must stay active: no pending_update, no traffic reset,
	// no panel calls at all — same contract as RenewSubscription.
	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
	for id, c := range f.vpn {
		created, updated, deleted, reset := c.counts()
		assert.Zero(t, created+updated+deleted+reset, "node %d must not be touched by renew", id)
	}
	f.assertInvalidatedTimes(t, 1)

	stored := f.stored(t)
	assert.Equal(t, f.sub.Token, stored.Token)
	assert.Equal(t, f.sub.PlanID, stored.PlanID)
	assert.Len(t, f.auditRows(t), 1)
}

func TestAdminService_Mutate_RenewReactivatesExpiredAndReprovisions(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	// The expiry worker downgraded the row: status expired, bindings gone.
	f := newAdminServiceFixture(t, "expired", &past, false)

	outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionRenew, "renew-expired"))
	require.NoError(t, err)
	assert.Equal(t, "active", outcome.Subscription.Status)
	assert.True(t, outcome.Subscription.ExpiresAt.After(time.Now().UTC().AddDate(0, 0, 29)))

	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID], "missing plan node → pending_add → provisioned")
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
	for _, c := range f.vpn {
		created, _, _, _ := c.counts()
		assert.Equal(t, 1, created)
	}
	f.assertInvalidatedTimes(t, 1)
}

func TestAdminService_Mutate_RenewRejectionsAreAuditedWithoutSideEffects(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name      string
		status    string
		expiresAt *time.Time
		wantErr   error
		wantCode  string
	}{
		{"perpetual", "active", nil, database.ErrAdminPerpetualSubscription, database.AdminErrorCodePerpetual},
		{"revoked", "revoked", &future, database.ErrAdminInvalidState, database.AdminErrorCodeInvalidState},
		{"canceled", "canceled", &future, database.ErrAdminInvalidState, database.AdminErrorCodeInvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminServiceFixture(t, tc.status, tc.expiresAt, true)
			outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionRenew, "renew-rej"))
			require.ErrorIs(t, err, tc.wantErr)
			require.NotNil(t, outcome, "rejections return the recorded audit entry")
			assert.False(t, outcome.Audit.Success)
			assert.Equal(t, tc.wantCode, outcome.Audit.ErrorCode)
			assert.Equal(t, tc.status, outcome.Subscription.Status)
			assert.Equal(t, f.sub, f.stored(t), "rejected mutation must not touch the row")
			// Nothing was committed, so no post-commit work may run: no cache
			// invalidation, no node churn (revoked leftovers are the /del flow's job).
			f.assertInvalidatedTimes(t, 0)
			statuses := f.nodeStatuses(t)
			assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
			assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
			for _, c := range f.vpn {
				created, updated, deleted, reset := c.counts()
				assert.Zero(t, created+updated+deleted+reset)
			}
			assert.Len(t, f.auditRows(t), 1)
		})
	}
}

// --- Disable / Enable ------------------------------------------------------

func TestAdminService_Mutate_DisableDrainsNodesBestEffort(t *testing.T) {
	future := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	f := newAdminServiceFixture(t, "active", &future, true)
	panelDown := errors.New("panel unreachable")
	f.vpn[f.node2.ID].deleteErr = panelDown

	outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionDisable, "disable-1"))
	require.NoError(t, err, "external sync failure is best-effort and must not surface")
	assert.Equal(t, "paused", outcome.Subscription.Status)
	assert.True(t, future.Equal(*outcome.Subscription.ExpiresAt), "disable keeps expiry")
	assert.Equal(t, 3, outcome.Subscription.RemindersSent, "disable keeps reminders")
	assert.True(t, outcome.Audit.Success)

	// node1: pending_remove → deleted on the panel → binding dropped.
	// node2: pending_remove stays with retry metadata for the background worker.
	statuses := f.nodeStatuses(t)
	assert.NotContains(t, statuses, f.node1.ID)
	assert.Equal(t, database.SyncStatusPendingRemove, statuses[f.node2.ID])
	failed := f.nodeRow(t, f.node2.ID)
	assert.Equal(t, 1, failed.RetryCount)
	require.NotNil(t, failed.RetryAt)
	require.NotNil(t, failed.LastError)
	assert.Contains(t, *failed.LastError, panelDown.Error())
	for _, c := range f.vpn {
		created, _, deleted, _ := c.counts()
		assert.Zero(t, created)
		assert.Equal(t, 1, deleted)
	}
	f.assertInvalidatedTimes(t, 1)
}

func TestAdminService_Mutate_DisableRejectsNonActive(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	for _, status := range []string{"paused", "revoked", "canceled"} {
		t.Run(status, func(t *testing.T) {
			f := newAdminServiceFixture(t, status, &future, false)
			outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionDisable, "disable-rej"))
			require.ErrorIs(t, err, database.ErrAdminInvalidState)
			require.NotNil(t, outcome)
			assert.Equal(t, database.AdminErrorCodeInvalidState, outcome.Audit.ErrorCode)
			assert.Equal(t, f.sub, f.stored(t))
			f.assertInvalidatedTimes(t, 0)
		})
	}
}

func TestAdminService_Mutate_EnableRestoresPlanNodes(t *testing.T) {
	future := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	// A disable already drained the bindings; only the status is paused.
	f := newAdminServiceFixture(t, "paused", &future, false)

	outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionEnable, "enable-1"))
	require.NoError(t, err)
	assert.Equal(t, "active", outcome.Subscription.Status)
	assert.True(t, future.Equal(*outcome.Subscription.ExpiresAt))
	assert.True(t, outcome.Audit.Success)

	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
	for _, c := range f.vpn {
		created, _, deleted, reset := c.counts()
		assert.Equal(t, 1, created)
		assert.Zero(t, deleted)
		assert.Zero(t, reset, "enable must not grant fresh traffic")
	}
	f.assertInvalidatedTimes(t, 1)
}

func TestAdminService_Mutate_EnableReactivatesPendingRemoveLeftovers(t *testing.T) {
	future := time.Now().UTC().Add(72 * time.Hour)
	f := newAdminServiceFixture(t, "paused", &future, false)
	ctx := context.Background()
	// The disable's deprovision is still in flight: bindings are pending_remove.
	for _, n := range []*database.Node{f.node1, f.node2} {
		require.NoError(t, f.db.CreateSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: f.sub.ID, NodeID: n.ID, Status: database.SyncStatusPendingRemove}))
	}

	_, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionEnable, "enable-leftover"))
	require.NoError(t, err)

	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID], "pending_remove → pending_add → provisioned")
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
	for _, c := range f.vpn {
		created, _, deleted, _ := c.counts()
		assert.Equal(t, 1, created)
		assert.Zero(t, deleted, "reactivated bindings must not be removed first")
	}
}

func TestAdminService_Mutate_EnableRejectsExpiredPausedAndNonPaused(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute)
	future := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name      string
		status    string
		expiresAt *time.Time
		wantErr   error
		wantCode  string
	}{
		{"paused expired", "paused", &past, database.ErrAdminSubscriptionExpired, database.AdminErrorCodeSubscriptionExpired},
		{"active", "active", &future, database.ErrAdminInvalidState, database.AdminErrorCodeInvalidState},
		{"expired", "expired", &past, database.ErrAdminInvalidState, database.AdminErrorCodeInvalidState},
		{"revoked", "revoked", &future, database.ErrAdminInvalidState, database.AdminErrorCodeInvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminServiceFixture(t, tc.status, tc.expiresAt, false)
			outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionEnable, "enable-rej"))
			require.ErrorIs(t, err, tc.wantErr)
			require.NotNil(t, outcome)
			assert.False(t, outcome.Audit.Success)
			assert.Equal(t, tc.wantCode, outcome.Audit.ErrorCode)
			assert.Equal(t, tc.status, outcome.Subscription.Status)
			assert.Equal(t, f.sub, f.stored(t))
			assert.Empty(t, f.nodeStatuses(t), "no provisioning for a rejected enable")
			for _, c := range f.vpn {
				created, _, _, _ := c.counts()
				assert.Zero(t, created)
			}
			f.assertInvalidatedTimes(t, 0)
		})
	}
}

// --- Paused rules ------------------------------------------------------------

// Renew and change_expiry on a paused subscription keep it paused, and a
// paused subscription never keeps live VPN clients: leftover bindings are
// drained on every successful mutation, not only on disable.
func TestAdminService_Mutate_PausedRenewAndChangeExpiryStayPausedWithoutVPN(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	newExpiry := future.Add(24 * time.Hour)
	cases := []struct {
		name       string
		mutate     func(m database.AdminMutation) database.AdminMutation
		wantExpiry time.Time
	}{
		{"renew", func(m database.AdminMutation) database.AdminMutation {
			m.Action = database.AdminActionRenew
			return m
		}, future.AddDate(0, 0, 30)},
		{"change_expiry", func(m database.AdminMutation) database.AdminMutation {
			m.Action = database.AdminActionChangeExpiry
			m.ExpiresAt = &newExpiry
			return m
		}, newExpiry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Leftover active bindings model an interrupted disable.
			f := newAdminServiceFixture(t, "paused", &future, true)
			outcome, err := f.admin.Mutate(context.Background(), tc.mutate(f.mutation("", "paused-"+tc.name)))
			require.NoError(t, err)
			assert.Equal(t, "paused", outcome.Subscription.Status, "only enable leaves paused")
			assert.True(t, tc.wantExpiry.Equal(*outcome.Subscription.ExpiresAt))
			assert.True(t, outcome.Audit.Success)
			assert.Equal(t, "paused", f.stored(t).Status)

			assert.Empty(t, f.nodeStatuses(t), "paused subscription must not keep VPN bindings")
			for _, c := range f.vpn {
				created, _, deleted, _ := c.counts()
				assert.Zero(t, created)
				assert.Equal(t, 1, deleted)
			}
			f.assertInvalidatedTimes(t, 1)
		})
	}
}

func TestAdminService_Mutate_PausedExpiredRenewThenEnable(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	f := newAdminServiceFixture(t, "paused", &past, false)
	ctx := context.Background()

	_, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionEnable, "enable-before"))
	require.ErrorIs(t, err, database.ErrAdminSubscriptionExpired)

	renewed, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionRenew, "renew-paused"))
	require.NoError(t, err)
	assert.Equal(t, "paused", renewed.Subscription.Status, "renew from the past keeps paused, only moves expiry")
	assert.True(t, renewed.Subscription.ExpiresAt.After(time.Now().UTC()))
	assert.Empty(t, f.nodeStatuses(t))

	enabled, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionEnable, "enable-after"))
	require.NoError(t, err)
	assert.Equal(t, "active", enabled.Subscription.Status)
	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])

	rows := f.auditRows(t)
	require.Len(t, rows, 3)
	assert.Equal(t, []bool{true, true, false}, []bool{rows[0].Success, rows[1].Success, rows[2].Success}, "newest first")
	f.assertInvalidatedTimes(t, 2)
}

// --- ChangeExpiry ------------------------------------------------------------

func TestAdminService_Mutate_ChangeExpiry(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	farFuture := future.Add(240 * time.Hour)

	t.Run("expired to future reactivates and provisions", func(t *testing.T) {
		f := newAdminServiceFixture(t, "expired", &past, false)
		m := f.mutation(database.AdminActionChangeExpiry, "ce-1")
		m.ExpiresAt = &farFuture
		outcome, err := f.admin.Mutate(context.Background(), m)
		require.NoError(t, err)
		assert.Equal(t, "active", outcome.Subscription.Status)
		assert.True(t, farFuture.Equal(*outcome.Subscription.ExpiresAt))
		statuses := f.nodeStatuses(t)
		assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
		assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
		f.assertInvalidatedTimes(t, 1)
	})

	t.Run("active to past keeps status for the expiry worker", func(t *testing.T) {
		f := newAdminServiceFixture(t, "active", &future, true)
		m := f.mutation(database.AdminActionChangeExpiry, "ce-2")
		m.ExpiresAt = &past
		outcome, err := f.admin.Mutate(context.Background(), m)
		require.NoError(t, err)
		assert.Equal(t, "active", outcome.Subscription.Status, "admin sets the timestamp; the expiry worker downgrades")
		assert.True(t, past.Equal(*outcome.Subscription.ExpiresAt))
		statuses := f.nodeStatuses(t)
		assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
		assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
		for _, c := range f.vpn {
			created, updated, deleted, reset := c.counts()
			assert.Zero(t, created+updated+deleted+reset, "no pending_update: expiry change must not reset traffic")
		}
		f.assertInvalidatedTimes(t, 1)
	})

	t.Run("perpetual free gets an expiry", func(t *testing.T) {
		f := newAdminServiceFixture(t, "active", nil, true)
		m := f.mutation(database.AdminActionChangeExpiry, "ce-3")
		m.ExpiresAt = &future
		outcome, err := f.admin.Mutate(context.Background(), m)
		require.NoError(t, err)
		require.NotNil(t, outcome.Subscription.ExpiresAt)
		assert.True(t, future.Equal(*outcome.Subscription.ExpiresAt))
	})

	t.Run("terminal is rejected and audited", func(t *testing.T) {
		f := newAdminServiceFixture(t, "revoked", &future, false)
		m := f.mutation(database.AdminActionChangeExpiry, "ce-4")
		m.ExpiresAt = &farFuture
		outcome, err := f.admin.Mutate(context.Background(), m)
		require.ErrorIs(t, err, database.ErrAdminInvalidState)
		require.NotNil(t, outcome)
		assert.Equal(t, f.sub, f.stored(t))
		f.assertInvalidatedTimes(t, 0)
	})

	t.Run("missing expiry is a shape error and not audited", func(t *testing.T) {
		f := newAdminServiceFixture(t, "active", &future, false)
		outcome, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionChangeExpiry, "ce-5"))
		require.ErrorIs(t, err, database.ErrAdminInvalidRequest)
		assert.Nil(t, outcome)
		assert.Empty(t, f.auditRows(t))
		f.assertInvalidatedTimes(t, 0)
	})
}

// --- Legacy node sync / provider-backed --------------------------------------

func TestAdminService_Mutate_ProviderBackedDrainsLegacyBindings(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	f := newAdminServiceFixture(t, "active", &future, true)
	ctx := context.Background()
	source := &database.ProviderSource{Name: "upstream", Type: "external", Enabled: true, SubscriptionURL: "https://upstream.example/feed"}
	require.NoError(t, f.db.CreateProviderSource(ctx, source))
	f.sub.ProviderSourceID = &source.ID
	require.NoError(t, f.db.GetDB().WithContext(ctx).Model(&database.Subscription{}).Where("id = ?", f.sub.ID).Update("provider_source_id", source.ID).Error)

	outcome, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionRenew, "renew-provider"))
	require.NoError(t, err)
	assert.Equal(t, "active", outcome.Subscription.Status)
	require.NotNil(t, outcome.Subscription.ProviderSourceID)
	assert.Equal(t, source.ID, *outcome.Subscription.ProviderSourceID)

	assert.Empty(t, f.nodeStatuses(t), "provider-backed access never keeps legacy plan nodes")
	for _, c := range f.vpn {
		created, _, deleted, _ := c.counts()
		assert.Zero(t, created)
		assert.Equal(t, 1, deleted)
	}
	f.assertInvalidatedTimes(t, 1)
}

func TestAdminService_Mutate_ReconcilesStalePlanMembership(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	f := newAdminServiceFixture(t, "active", &future, true)
	ctx := context.Background()
	// node2 was removed from the plan while the binding stayed active.
	require.NoError(t, f.db.GetDB().WithContext(ctx).Where("plan_id = ? AND node_id = ?", f.plan.ID, f.node2.ID).Delete(&database.PlanNode{}).Error)

	_, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionRenew, "renew-stale"))
	require.NoError(t, err)

	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
	assert.NotContains(t, statuses, f.node2.ID, "stale binding → pending_remove → dropped")
	_, _, deleted1, _ := f.vpn[f.node1.ID].counts()
	_, _, deleted2, _ := f.vpn[f.node2.ID].counts()
	assert.Zero(t, deleted1)
	assert.Equal(t, 1, deleted2)
}

// --- Replay / idempotency ----------------------------------------------------

func TestAdminService_Mutate_ReplayFollowsCurrentStateNotTheOldAction(t *testing.T) {
	future := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	f := newAdminServiceFixture(t, "active", &future, true)
	ctx := context.Background()

	disabled, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionDisable, "k-disable"))
	require.NoError(t, err)
	assert.Equal(t, "paused", disabled.Subscription.Status)
	assert.Empty(t, f.nodeStatuses(t))

	enabled, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionEnable, "k-enable"))
	require.NoError(t, err)
	assert.Equal(t, "active", enabled.Subscription.Status)
	_, _, deletedBefore, _ := f.vpn[f.node1.ID].counts()

	// A late retry of the old disable request must not strip access again.
	replayed, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionDisable, "k-disable"))
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, disabled.Audit.ID, replayed.Audit.ID, "the original audit row is returned")
	assert.Equal(t, "active", replayed.Subscription.Status, "replay reports the current row")
	assert.Equal(t, "active", f.stored(t).Status)

	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
	_, _, deletedAfter, _ := f.vpn[f.node1.ID].counts()
	assert.Equal(t, deletedBefore, deletedAfter, "replayed disable must not deprovision an active subscription")
	assert.Len(t, f.auditRows(t), 2, "replay does not add an audit row")
	f.assertInvalidatedTimes(t, 3, "replays re-run cache invalidation so an interrupted first attempt can be retried")
}

func TestAdminService_Mutate_ReplayOfRejectionReturnsOriginalError(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	f := newAdminServiceFixture(t, "paused", &past, false)
	ctx := context.Background()

	first, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionEnable, "k-rej"))
	require.ErrorIs(t, err, database.ErrAdminSubscriptionExpired)
	second, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionEnable, "k-rej"))
	require.ErrorIs(t, err, database.ErrAdminSubscriptionExpired)
	require.NotNil(t, second)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.Audit.ID, second.Audit.ID)
	assert.Len(t, f.auditRows(t), 1)
	f.assertInvalidatedTimes(t, 0)
}

func TestAdminService_Mutate_RequestKeyConflictIsNotAudited(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	f := newAdminServiceFixture(t, "active", &future, true)
	ctx := context.Background()

	_, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionRenew, "k-same"))
	require.NoError(t, err)
	other := f.mutation(database.AdminActionRenew, "k-same")
	other.Days = 10
	outcome, err := f.admin.Mutate(ctx, other)
	require.ErrorIs(t, err, database.ErrAdminRequestKeyConflict)
	assert.Nil(t, outcome)
	assert.Len(t, f.auditRows(t), 1)
	f.assertInvalidatedTimes(t, 1, "the conflicting request committed nothing")
}

// --- Errors ------------------------------------------------------------------

func TestAdminService_Mutate_ValidatesBeforeRepository(t *testing.T) {
	repo := &fakeAdminRepo{}
	svc := NewAdminService(repo, nil, nil)
	future := time.Now().Add(time.Hour)
	cases := map[string]database.AdminMutation{
		"empty actor":       {RequestKey: "k", Action: database.AdminActionDisable, SubscriptionID: 1},
		"empty key":         {Actor: "a", Action: database.AdminActionDisable, SubscriptionID: 1},
		"non-ascii key":     {Actor: "a", RequestKey: "ключ", Action: database.AdminActionDisable, SubscriptionID: 1},
		"zero subscription": {Actor: "a", RequestKey: "k", Action: database.AdminActionDisable},
		"unknown action":    {Actor: "a", RequestKey: "k", Action: "delete", SubscriptionID: 1},
		"renew zero days":   {Actor: "a", RequestKey: "k", Action: database.AdminActionRenew, SubscriptionID: 1},
		"renew too long":    {Actor: "a", RequestKey: "k", Action: database.AdminActionRenew, SubscriptionID: 1, Days: database.AdminMaxRenewalDays + 1},
		"expiry missing":    {Actor: "a", RequestKey: "k", Action: database.AdminActionChangeExpiry, SubscriptionID: 1},
		"expiry year>9999":  {Actor: "a", RequestKey: "k", Action: database.AdminActionChangeExpiry, SubscriptionID: 1, ExpiresAt: testutil.PtrTime(future.AddDate(9000, 0, 0))},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			outcome, err := svc.Mutate(context.Background(), m)
			require.ErrorIs(t, err, database.ErrAdminInvalidRequest)
			assert.Nil(t, outcome)
		})
	}
	assert.Zero(t, repo.mutateCalls, "malformed requests must never reach the persistence seam")
}

func TestAdminService_Mutate_UnknownSubscription(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	f := newAdminServiceFixture(t, "active", &future, false)
	m := f.mutation(database.AdminActionRenew, "k-missing")
	m.SubscriptionID = f.sub.ID + 1000
	outcome, err := f.admin.Mutate(context.Background(), m)
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)
	assert.Nil(t, outcome)
	f.assertInvalidatedTimes(t, 0)
}

func TestAdminService_Mutate_InfrastructureErrorHasNoOutcomeAndNoSideEffects(t *testing.T) {
	boom := errors.New("sqlite: disk I/O error")
	repo := &fakeAdminRepo{mutate: func(context.Context, database.AdminMutation) (*database.AdminMutationResult, error) {
		return nil, boom
	}}
	mockDB := testutil.NewDatabaseService()
	subs := NewSubscriptionService(mockDB, nil, nil, nil, &config.Config{})
	invalidated := 0
	subs.SetInvalidateFunc(func(int64) { invalidated++ })
	subs.SetInvalidateBySubIDFunc(func(string) { invalidated++ })
	svc := NewAdminService(repo, subs, NewSyncService(mockDB, nil, nil))

	outcome, err := svc.Mutate(context.Background(), database.AdminMutation{Actor: "a", RequestKey: "k", Action: database.AdminActionDisable, SubscriptionID: 7})
	require.ErrorIs(t, err, boom)
	assert.Nil(t, outcome)
	assert.Zero(t, invalidated, "nothing committed → nothing to invalidate")
}

// The lifecycle change is committed; a failure lining up node work must be
// reported (so the admin retries with the same key) but caches are dropped
// regardless, otherwise the subserver keeps serving the pre-mutation state.
func TestAdminService_Mutate_NodeSyncSetupFailureIsReportedAndCacheInvalidated(t *testing.T) {
	committed := &database.Subscription{ID: 7, TelegramID: 555, SubscriptionID: "sub-555", Status: "paused", PlanID: 3}
	audit := database.AdminAuditLog{ID: 1, Actor: "a", Action: "disable", SubscriptionID: 7, Success: true, RequestKey: "k"}
	repo := &fakeAdminRepo{mutate: func(context.Context, database.AdminMutation) (*database.AdminMutationResult, error) {
		return &database.AdminMutationResult{Subscription: committed, Audit: audit}, nil
	}}
	loadErr := errors.New("subscription_nodes: database is locked")
	mockDB := testutil.NewDatabaseService()
	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) { return committed, nil }
	mockDB.GetBySubscriptionIDFunc = func(context.Context, uint) ([]database.SubscriptionNode, error) { return nil, loadErr }
	subs := NewSubscriptionService(mockDB, nil, nil, nil, &config.Config{})
	var tg []int64
	var ids []string
	subs.SetInvalidateFunc(func(id int64) { tg = append(tg, id) })
	subs.SetInvalidateBySubIDFunc(func(id string) { ids = append(ids, id) })
	svc := NewAdminService(repo, subs, NewSyncService(mockDB, nil, nil))

	outcome, err := svc.Mutate(context.Background(), database.AdminMutation{Actor: "a", RequestKey: "k", Action: database.AdminActionDisable, SubscriptionID: 7})
	require.ErrorIs(t, err, loadErr)
	assert.Contains(t, err.Error(), "admin disable: schedule node sync")
	require.NotNil(t, outcome, "the mutation is committed and audited; the caller must see both")
	assert.Equal(t, audit, outcome.Audit)
	assert.Equal(t, "paused", outcome.Subscription.Status)
	assert.Equal(t, []int64{555}, tg)
	assert.Equal(t, []string{"sub-555"}, ids)
	for _, sentinel := range []error{database.ErrAdminInvalidState, database.ErrAdminInvalidRequest, database.ErrAdminRequestKeyConflict} {
		assert.NotErrorIs(t, err, sentinel, "infrastructure failure must not look like a domain rejection")
	}
}

// cancellingAdminRepo commits through the real repository and then cancels the
// request context, modelling a client that disconnects right after the commit.
type cancellingAdminRepo struct {
	AdminRepository
	cancel context.CancelFunc
}

func (r cancellingAdminRepo) AdminMutateSubscription(ctx context.Context, m database.AdminMutation) (*database.AdminMutationResult, error) {
	result, err := r.AdminRepository.AdminMutateSubscription(ctx, m)
	r.cancel()
	return result, err
}

func TestAdminService_Mutate_CancelledContextAfterCommitStillInvalidatesCache(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	f := newAdminServiceFixture(t, "active", &future, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := NewAdminService(cancellingAdminRepo{AdminRepository: f.db, cancel: cancel}, f.subs, f.sync)

	outcome, err := svc.Mutate(ctx, f.mutation(database.AdminActionDisable, "k-cancel"))
	require.Error(t, err, "node-sync setup cannot run on a cancelled context")
	assert.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, outcome)
	assert.Equal(t, "paused", outcome.Subscription.Status)
	assert.Equal(t, "paused", f.stored(t).Status, "the commit is not undone")
	f.assertInvalidatedTimes(t, 1)

	// Retrying with the same key replays the commit and finishes the node work.
	replayed, err := f.admin.Mutate(context.Background(), f.mutation(database.AdminActionDisable, "k-cancel"))
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Empty(t, f.nodeStatuses(t))
	assert.Len(t, f.auditRows(t), 1)
	f.assertInvalidatedTimes(t, 2)
}

func TestAdminService_Mutate_WithoutSubscriptionAndSyncServices(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	f := newAdminServiceFixture(t, "active", &future, true)
	svc := NewAdminService(f.db, nil, nil)

	outcome, err := svc.Mutate(context.Background(), f.mutation(database.AdminActionDisable, "k-bare"))
	require.NoError(t, err)
	assert.Equal(t, "paused", outcome.Subscription.Status)
	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID], "no sync service → no node work")
	f.assertInvalidatedTimes(t, 0)
}

// --- Concurrency -------------------------------------------------------------

func TestAdminService_Mutate_ConcurrentRenewsSerializeOnTheRow(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	f := newAdminServiceFixture(t, "active", &future, true)
	const workers = 8

	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := f.mutation(database.AdminActionRenew, fmt.Sprintf("renew-%d", i))
			m.Days = 1
			_, errs[i] = f.admin.Mutate(context.Background(), m)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "worker %d", i)
	}

	stored := f.stored(t)
	assert.Equal(t, "active", stored.Status)
	assert.True(t, future.AddDate(0, 0, workers).Equal(*stored.ExpiresAt), "every renew must build on the previous committed expiry, got %s", stored.ExpiresAt)
	assert.Len(t, f.auditRows(t), workers)
	statuses := f.nodeStatuses(t)
	assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
	assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
	for _, c := range f.vpn {
		created, updated, deleted, reset := c.counts()
		assert.Zero(t, created+updated+deleted+reset)
	}
	f.assertInvalidatedTimes(t, workers)
}

func TestAdminService_Mutate_ConcurrentDisableEnableConverge(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	for round := 0; round < 5; round++ {
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			f := newAdminServiceFixture(t, "active", &future, true)
			var wg sync.WaitGroup
			var disableErr, enableErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, disableErr = f.admin.Mutate(context.Background(), f.mutation(database.AdminActionDisable, "race-disable"))
			}()
			go func() {
				defer wg.Done()
				_, enableErr = f.admin.Mutate(context.Background(), f.mutation(database.AdminActionEnable, "race-enable"))
			}()
			wg.Wait()

			require.NoError(t, disableErr, "disable always finds an active row")
			stored := f.stored(t)
			statuses := f.nodeStatuses(t)
			switch {
			case enableErr == nil:
				// disable committed first, enable second: access is restored.
				assert.Equal(t, "active", stored.Status)
				assert.Equal(t, database.SyncStatusActive, statuses[f.node1.ID])
				assert.Equal(t, database.SyncStatusActive, statuses[f.node2.ID])
			default:
				// enable ran first against an active row and was rejected.
				require.ErrorIs(t, enableErr, database.ErrAdminInvalidState)
				assert.Equal(t, "paused", stored.Status)
				assert.Empty(t, statuses, "paused → every binding drained")
			}
			assert.Len(t, f.auditRows(t), 2, "both requests are audited exactly once")
		})
	}
}

// --- Read models -------------------------------------------------------------

func TestAdminService_ListUsers_ValidatesAndClampsPaging(t *testing.T) {
	var seen database.AdminUserFilter
	repo := &fakeAdminRepo{listUsers: func(_ context.Context, f database.AdminUserFilter) ([]database.AdminUserRow, int64, error) {
		seen = f
		return []database.AdminUserRow{{Subscription: database.Subscription{ID: 1, TelegramID: 10, Token: "tok", ClientID: "cid", SubscriptionID: "sid", Status: "active"}, PlanName: "premium"}}, 1, nil
	}}
	svc := NewAdminService(repo, nil, nil)
	ctx := context.Background()

	_, err := svc.ListUsers(ctx, database.AdminUserFilter{Status: "deleted"})
	require.ErrorIs(t, err, database.ErrAdminInvalidRequest)

	page, err := svc.ListUsers(ctx, database.AdminUserFilter{Limit: 0, Offset: -5, Status: "active", Query: "@name"})
	require.NoError(t, err)
	assert.Equal(t, database.AdminDefaultPageSize, seen.Limit)
	assert.Zero(t, seen.Offset)
	assert.Equal(t, "active", seen.Status)
	assert.Equal(t, "@name", seen.Query)
	assert.Equal(t, database.AdminDefaultPageSize, page.Limit)
	assert.Equal(t, int64(1), page.Total)
	require.Len(t, page.Users, 1)
	assert.Equal(t, "premium", page.Users[0].PlanName)
	assertViewHidesBearerMaterial(t, page.Users[0], &database.Subscription{Token: "tok", ClientID: "cid", SubscriptionID: "sid"})

	_, err = svc.ListUsers(ctx, database.AdminUserFilter{Limit: database.AdminMaxPageSize + 1})
	require.NoError(t, err)
	assert.Equal(t, database.AdminMaxPageSize, seen.Limit)

	repo.listUsers = func(context.Context, database.AdminUserFilter) ([]database.AdminUserRow, int64, error) {
		return nil, 0, errFakeAdminRepo
	}
	_, err = svc.ListUsers(ctx, database.AdminUserFilter{})
	require.ErrorIs(t, err, errFakeAdminRepo)
}

func TestAdminService_ReadModelsAgainstRepository(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	f := newAdminServiceFixture(t, "active", &future, true)
	ctx := context.Background()
	_, err := f.admin.Mutate(ctx, f.mutation(database.AdminActionRenew, "read-renew"))
	require.NoError(t, err)

	page, err := f.admin.GetSubscription(ctx, f.sub.ID)
	require.NoError(t, err)
	assert.Equal(t, f.sub.ID, page.Subscription.ID)
	assert.Equal(t, "admin-plan", page.Subscription.PlanName)
	require.NotNil(t, page.Plan)
	assert.Equal(t, f.plan.ID, page.Plan.ID)
	assert.Len(t, page.Nodes, 2)
	require.Len(t, page.Audit, 1)
	assert.Equal(t, "renew", page.Audit[0].Action)
	assertViewHidesBearerMaterial(t, page.Subscription, f.sub)

	_, err = f.admin.GetSubscription(ctx, f.sub.ID+1000)
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)

	users, err := f.admin.ListUsers(ctx, database.AdminUserFilter{Query: "admin-target"})
	require.NoError(t, err)
	require.Len(t, users.Users, 1)
	assert.Equal(t, adminFixtureTelegramID, users.Users[0].TelegramID)

	entries, err := f.admin.ListAudit(ctx, database.AdminAuditFilter{SubscriptionID: f.sub.ID})
	require.NoError(t, err)
	assert.Len(t, entries, 1)

	dashboard, err := f.admin.Dashboard(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), dashboard.Users)
	assert.Equal(t, int64(1), dashboard.Active)
	assert.Equal(t, int64(1), dashboard.AuditLast24h)
}

func TestAdminService_ReadModelsWrapRepositoryErrors(t *testing.T) {
	repo := &fakeAdminRepo{}
	svc := NewAdminService(repo, nil, nil)
	ctx := context.Background()

	_, err := svc.Dashboard(ctx)
	require.ErrorIs(t, err, errFakeAdminRepo)
	_, err = svc.ListAudit(ctx, database.AdminAuditFilter{})
	require.ErrorIs(t, err, errFakeAdminRepo)
	_, err = svc.GetSubscription(ctx, 1)
	require.ErrorIs(t, err, errFakeAdminRepo)

	repo.getSub = func(context.Context, uint, int) (*database.AdminSubscriptionDetail, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	_, err = svc.GetSubscription(ctx, 1)
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)

	repo.getSub = func(context.Context, uint, int) (*database.AdminSubscriptionDetail, error) {
		return &database.AdminSubscriptionDetail{Subscription: database.Subscription{ID: 1, PlanID: 9}}, nil
	}
	page, err := svc.GetSubscription(ctx, 1)
	require.NoError(t, err)
	assert.Nil(t, page.Plan, "missing plan row must not fail the page")
	assert.Empty(t, page.Subscription.PlanName)
}
