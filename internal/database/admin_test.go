package database

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adminFixture(t *testing.T, status string, expiresAt *time.Time) (*Service, *Subscription) {
	t.Helper()
	svc := newTestService(t)
	sub := createTestSubscription(t, svc, 91001, "admin-target", "admin-target-client")
	sub.Status = status
	sub.ExpiresAt = expiresAt
	sub.RemindersSent = 5
	require.NoError(t, svc.UpdateSubscription(context.Background(), sub))
	stored, err := svc.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	return svc, stored
}

func adminMutation(sub *Subscription, action AdminAction, key string) AdminMutation {
	return AdminMutation{Actor: "admin", RequestKey: key, Action: action, SubscriptionID: sub.ID, Days: 30}
}

func auditRows(t *testing.T, svc *Service, subID uint) []AdminAuditLog {
	t.Helper()
	rows, err := svc.ListAdminAudit(context.Background(), AdminAuditFilter{SubscriptionID: subID})
	require.NoError(t, err)
	return rows
}

func TestAdminMutation_RenewExtendsFromFutureExpiryAndKeepsPaused(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	for _, status := range []string{"active", "paused"} {
		t.Run(status, func(t *testing.T) {
			svc, sub := adminFixture(t, status, &future)
			result, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionRenew, "renew-1"))
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.False(t, result.Replayed)
			assert.Equal(t, status, result.Subscription.Status, "renew must not change paused/active")
			require.NotNil(t, result.Subscription.ExpiresAt)
			assert.True(t, future.AddDate(0, 0, 30).Equal(*result.Subscription.ExpiresAt))
			assert.Zero(t, result.Subscription.RemindersSent)
			assert.Equal(t, sub.Token, result.Subscription.Token)
			assert.Equal(t, sub.PlanID, result.Subscription.PlanID)

			rows := auditRows(t, svc, sub.ID)
			require.Len(t, rows, 1)
			assert.True(t, rows[0].Success)
			assert.Equal(t, "renew", rows[0].Action)
			assert.Equal(t, "admin", rows[0].Actor)
			assert.Empty(t, rows[0].ErrorCode)
			var before, after AdminSubscriptionState
			require.NoError(t, json.Unmarshal([]byte(rows[0].OldValue), &before))
			require.NoError(t, json.Unmarshal([]byte(rows[0].NewValue), &after))
			assert.Equal(t, status, before.Status)
			assert.Equal(t, 5, before.RemindersSent)
			assert.Equal(t, status, after.Status)
			assert.True(t, after.ExpiresAt.Equal(*result.Subscription.ExpiresAt))
		})
	}
}

func TestAdminMutation_RenewFromPastUsesNowAndReactivatesExpiredStatus(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	svc, sub := adminFixture(t, "expired", &past)
	before := time.Now().UTC()
	result, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionRenew, "renew-2"))
	require.NoError(t, err)
	assert.Equal(t, "active", result.Subscription.Status)
	assert.False(t, result.Subscription.ExpiresAt.Before(before.AddDate(0, 0, 30)))
	assert.False(t, result.Subscription.ExpiresAt.After(time.Now().UTC().AddDate(0, 0, 30)))
}

func TestAdminMutation_RenewRejectsPerpetualAndTerminal(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	cases := []struct {
		name      string
		status    string
		expiresAt *time.Time
		wantErr   error
		wantCode  string
	}{
		{"perpetual free", "active", nil, ErrAdminPerpetualSubscription, AdminErrorCodePerpetual},
		{"revoked", "revoked", &future, ErrAdminInvalidState, AdminErrorCodeInvalidState},
		{"canceled", "canceled", &future, ErrAdminInvalidState, AdminErrorCodeInvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, sub := adminFixture(t, tc.status, tc.expiresAt)
			result, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionRenew, "renew-x"))
			require.ErrorIs(t, err, tc.wantErr)
			require.NotNil(t, result, "rejections are audited and still return the recorded entry")
			assert.False(t, result.Audit.Success)
			assert.Equal(t, tc.wantCode, result.Audit.ErrorCode)
			after, err := svc.GetByID(context.Background(), sub.ID)
			require.NoError(t, err)
			assert.Equal(t, sub, after, "a rejected mutation must not touch the subscription")
			assert.Len(t, auditRows(t, svc, sub.ID), 1)
		})
	}
}

func TestAdminMutation_DisableEnableLifecycle(t *testing.T) {
	future := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	svc, sub := adminFixture(t, "active", &future)
	ctx := context.Background()

	disabled, err := svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionDisable, "disable-1"))
	require.NoError(t, err)
	assert.Equal(t, "paused", disabled.Subscription.Status)
	assert.True(t, future.Equal(*disabled.Subscription.ExpiresAt))
	assert.Equal(t, 5, disabled.Subscription.RemindersSent, "pause keeps reminder state")

	// Paused: disable again is a state error; renew/change_expiry keep paused.
	again, err := svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionDisable, "disable-2"))
	require.ErrorIs(t, err, ErrAdminInvalidState)
	assert.Equal(t, "paused", again.Subscription.Status)

	renewed, err := svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionRenew, "renew-paused"))
	require.NoError(t, err)
	assert.Equal(t, "paused", renewed.Subscription.Status)

	newExpiry := time.Now().UTC().Add(240 * time.Hour).Truncate(time.Second)
	changed, err := svc.AdminMutateSubscription(ctx, AdminMutation{Actor: "admin", RequestKey: "expiry-paused", Action: AdminActionChangeExpiry, SubscriptionID: sub.ID, ExpiresAt: &newExpiry})
	require.NoError(t, err)
	assert.Equal(t, "paused", changed.Subscription.Status)
	assert.True(t, newExpiry.Equal(*changed.Subscription.ExpiresAt))

	// Only enable leaves paused.
	enabled, err := svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionEnable, "enable-1"))
	require.NoError(t, err)
	assert.Equal(t, "active", enabled.Subscription.Status)
	assert.True(t, newExpiry.Equal(*enabled.Subscription.ExpiresAt))

	// Enable on active is a state error.
	_, err = svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionEnable, "enable-2"))
	require.ErrorIs(t, err, ErrAdminInvalidState)

	rows := auditRows(t, svc, sub.ID)
	require.Len(t, rows, 6)
	actions := make([]string, 0, len(rows))
	for _, row := range rows {
		actions = append(actions, fmt.Sprintf("%s:%t", row.Action, row.Success))
	}
	assert.Equal(t, []string{"enable:false", "enable:true", "change_expiry:true", "renew:true", "disable:false", "disable:true"}, actions)
}

func TestAdminMutation_EnableExpiredPausedIsRejected(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute)
	svc, sub := adminFixture(t, "paused", &past)
	result, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionEnable, "enable-expired"))
	require.ErrorIs(t, err, ErrAdminSubscriptionExpired)
	assert.Equal(t, AdminErrorCodeSubscriptionExpired, result.Audit.ErrorCode)
	assert.Equal(t, "paused", result.Subscription.Status)

	// After the admin extends the expiry the paused subscription can be enabled.
	renewed, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionRenew, "renew-then-enable"))
	require.NoError(t, err)
	assert.Equal(t, "paused", renewed.Subscription.Status)
	enabled, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionEnable, "enable-after-renew"))
	require.NoError(t, err)
	assert.Equal(t, "active", enabled.Subscription.Status)
}

func TestAdminMutation_EnablePerpetualPaused(t *testing.T) {
	svc, sub := adminFixture(t, "paused", nil)
	enabled, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionEnable, "enable-perpetual"))
	require.NoError(t, err)
	assert.Equal(t, "active", enabled.Subscription.Status)
	assert.Nil(t, enabled.Subscription.ExpiresAt)
}

func TestAdminMutation_ChangeExpiryReactivatesExpiredOnlyWhenFuture(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	svc, sub := adminFixture(t, "expired", &past)
	earlier := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	result, err := svc.AdminMutateSubscription(context.Background(), AdminMutation{Actor: "admin", RequestKey: "expiry-past", Action: AdminActionChangeExpiry, SubscriptionID: sub.ID, ExpiresAt: &earlier})
	require.NoError(t, err)
	assert.Equal(t, "expired", result.Subscription.Status)
	assert.True(t, earlier.Equal(*result.Subscription.ExpiresAt))
	later := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	result, err = svc.AdminMutateSubscription(context.Background(), AdminMutation{Actor: "admin", RequestKey: "expiry-future", Action: AdminActionChangeExpiry, SubscriptionID: sub.ID, ExpiresAt: &later})
	require.NoError(t, err)
	assert.Equal(t, "active", result.Subscription.Status)
	assert.Zero(t, result.Subscription.RemindersSent)
}

func TestAdminMutation_RequestKeyIdempotency(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	svc, sub := adminFixture(t, "active", &future)
	ctx := context.Background()
	m := adminMutation(sub, AdminActionRenew, "same-key")

	first, err := svc.AdminMutateSubscription(ctx, m)
	require.NoError(t, err)
	second, err := svc.AdminMutateSubscription(ctx, m)
	require.NoError(t, err)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.Audit.ID, second.Audit.ID)
	assert.True(t, first.Subscription.ExpiresAt.Equal(*second.Subscription.ExpiresAt), "replay must not extend again")
	assert.Len(t, auditRows(t, svc, sub.ID), 1)

	// Same key, different payload: rejected, nothing recorded.
	conflict := m
	conflict.Days = 31
	result, err := svc.AdminMutateSubscription(ctx, conflict)
	require.ErrorIs(t, err, ErrAdminRequestKeyConflict)
	assert.Nil(t, result)
	assert.Len(t, auditRows(t, svc, sub.ID), 1)

	// Same key, different actor: independent request.
	other := m
	other.Actor = "second-admin"
	result, err = svc.AdminMutateSubscription(ctx, other)
	require.NoError(t, err)
	assert.False(t, result.Replayed)
	assert.Len(t, auditRows(t, svc, sub.ID), 2)

	// Parameters the action ignores do not take part in the request identity:
	// the web layer forwards Days for every action, so a retry without it (or
	// with a different value) must replay rather than conflict.
	disable := adminMutation(sub, AdminActionDisable, "disable-key")
	disable.Days = 30
	disable.ExpiresAt = &future
	first, err = svc.AdminMutateSubscription(ctx, disable)
	require.NoError(t, err)
	assert.Equal(t, "paused", first.Subscription.Status)
	retry := adminMutation(sub, AdminActionDisable, "disable-key")
	retry.Days = 0
	retry.ExpiresAt = nil
	second, err = svc.AdminMutateSubscription(ctx, retry)
	require.NoError(t, err)
	assert.True(t, second.Replayed)
	assert.Equal(t, first.Audit.ID, second.Audit.ID)
	assert.Len(t, auditRows(t, svc, sub.ID), 3)

	// A recorded rejection replays as the same rejection without re-evaluating.
	past := time.Now().UTC().Add(-time.Minute)
	svc2, paused := adminFixture(t, "paused", &past)
	enable := adminMutation(paused, AdminActionEnable, "enable-key")
	_, err = svc2.AdminMutateSubscription(ctx, enable)
	require.ErrorIs(t, err, ErrAdminSubscriptionExpired)
	_, err = svc2.AdminMutateSubscription(ctx, adminMutation(paused, AdminActionRenew, "renew-key"))
	require.NoError(t, err)
	replayed, err := svc2.AdminMutateSubscription(ctx, enable)
	require.ErrorIs(t, err, ErrAdminSubscriptionExpired)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, "paused", replayed.Subscription.Status, "replay of a rejection must not apply the action now")
}

func TestAdminMutation_ConcurrentRenewalsSerialize(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	svc, sub := adminFixture(t, "active", &future)
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, svc.db.Exec("PRAGMA journal_mode=WAL").Error)
	const requests = 8
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	start := make(chan struct{})
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			m := adminMutation(sub, AdminActionRenew, fmt.Sprintf("concurrent-%d", i))
			m.Days = 1
			_, err := svc.AdminMutateSubscription(context.Background(), m)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	stored, err := svc.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.True(t, future.AddDate(0, 0, requests).Equal(*stored.ExpiresAt), "every extension must observe the previous committed expiry")
	assert.Len(t, auditRows(t, svc, sub.ID), requests)
}

func TestAdminMutation_ValidationAndNotFound(t *testing.T) {
	svc, sub := adminFixture(t, "active", nil)
	ctx := context.Background()
	base := adminMutation(sub, AdminActionRenew, "key")
	for name, mutate := range map[string]func(*AdminMutation){
		"empty actor":         func(m *AdminMutation) { m.Actor = "" },
		"empty key":           func(m *AdminMutation) { m.RequestKey = "" },
		"key with space":      func(m *AdminMutation) { m.RequestKey = "a b" },
		"long key":            func(m *AdminMutation) { m.RequestKey = string(make([]byte, AdminMaxRequestKey+1)) },
		"zero days":           func(m *AdminMutation) { m.Days = 0 },
		"too many days":       func(m *AdminMutation) { m.Days = AdminMaxRenewalDays + 1 },
		"unknown action":      func(m *AdminMutation) { m.Action = "delete" },
		"expiry without time": func(m *AdminMutation) { m.Action = AdminActionChangeExpiry; m.ExpiresAt = nil },
		"zero subscription":   func(m *AdminMutation) { m.SubscriptionID = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			m := base
			mutate(&m)
			result, err := svc.AdminMutateSubscription(ctx, m)
			require.ErrorIs(t, err, ErrAdminInvalidRequest)
			assert.Nil(t, result)
		})
	}
	missing := base
	missing.SubscriptionID = 999999
	result, err := svc.AdminMutateSubscription(ctx, missing)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
	assert.Nil(t, result)
	assert.Empty(t, auditRows(t, svc, sub.ID), "shape errors and unknown targets are never audited")
}

func TestAdminMutation_AuditRollsBackWithSubscriptionWrite(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	svc, sub := adminFixture(t, "active", &future)
	require.NoError(t, svc.db.Exec(`CREATE TRIGGER reject_admin_audit BEFORE INSERT ON admin_audit_log BEGIN SELECT RAISE(ABORT, 'audit blocked'); END`).Error)
	result, err := svc.AdminMutateSubscription(context.Background(), adminMutation(sub, AdminActionDisable, "atomic"))
	require.Error(t, err)
	assert.Nil(t, result)
	after, err := svc.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", after.Status, "subscription change must roll back with the audit insert")
}

func TestApplyAdminAction_PureRules(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	for _, tc := range []struct {
		name    string
		state   AdminSubscriptionState
		m       AdminMutation
		want    AdminSubscriptionState
		wantErr error
	}{
		{"renew active", AdminSubscriptionState{Status: "active", ExpiresAt: &future, RemindersSent: 3}, AdminMutation{Action: AdminActionRenew, Days: 2}, AdminSubscriptionState{Status: "active", ExpiresAt: ptrTime(future.AddDate(0, 0, 2))}, nil},
		{"renew paused", AdminSubscriptionState{Status: "paused", ExpiresAt: &past}, AdminMutation{Action: AdminActionRenew, Days: 1}, AdminSubscriptionState{Status: "paused", ExpiresAt: ptrTime(now.AddDate(0, 0, 1))}, nil},
		{"renew overflow", AdminSubscriptionState{Status: "active", ExpiresAt: ptrTime(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))}, AdminMutation{Action: AdminActionRenew, Days: 1}, AdminSubscriptionState{}, ErrAdminInvalidExpiry},
		{"disable expired", AdminSubscriptionState{Status: "expired", ExpiresAt: &past, RemindersSent: 1}, AdminMutation{Action: AdminActionDisable}, AdminSubscriptionState{Status: "paused", ExpiresAt: &past, RemindersSent: 1}, nil},
		{"disable revoked", AdminSubscriptionState{Status: "revoked"}, AdminMutation{Action: AdminActionDisable}, AdminSubscriptionState{}, ErrAdminInvalidState},
		{"enable paused future", AdminSubscriptionState{Status: "paused", ExpiresAt: &future}, AdminMutation{Action: AdminActionEnable}, AdminSubscriptionState{Status: "active", ExpiresAt: &future}, nil},
		{"enable paused expired", AdminSubscriptionState{Status: "paused", ExpiresAt: &past}, AdminMutation{Action: AdminActionEnable}, AdminSubscriptionState{}, ErrAdminSubscriptionExpired},
		{"enable revoked", AdminSubscriptionState{Status: "revoked"}, AdminMutation{Action: AdminActionEnable}, AdminSubscriptionState{}, ErrAdminInvalidState},
		{"change expiry canceled", AdminSubscriptionState{Status: "canceled"}, AdminMutation{Action: AdminActionChangeExpiry, ExpiresAt: &future}, AdminSubscriptionState{}, ErrAdminInvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyAdminAction(tc.state, tc.m, now)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, tc.state, got, "rejections return the input state unchanged")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want.Status, got.Status)
			assert.Equal(t, tc.want.RemindersSent, got.RemindersSent)
			if tc.want.ExpiresAt == nil {
				assert.Nil(t, got.ExpiresAt)
			} else {
				require.NotNil(t, got.ExpiresAt)
				assert.True(t, tc.want.ExpiresAt.Equal(*got.ExpiresAt))
			}
		})
	}
}

func TestAdminDashboardAndUsers(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(tg int64, name, status string, expiresAt *time.Time, paid bool) *Subscription {
		sub := &Subscription{TelegramID: tg, Username: name, ClientID: fmt.Sprintf("c-%d", tg), SubscriptionID: fmt.Sprintf("s-%d", tg), Status: status, ExpiresAt: expiresAt}
		if paid {
			sub.PricePaidCents = 100
		}
		require.NoError(t, svc.CreateSubscription(ctx, prepareForCreate(t, svc, sub), ""))
		return sub
	}
	mk(1001, "alice", "active", ptrTime(now.Add(3*24*time.Hour)), true)
	mk(1002, "bob_smith", "active", ptrTime(now.Add(-time.Hour)), false)
	mk(1003, "carol", "paused", ptrTime(now.Add(24*time.Hour)), true)
	mk(1004, "dave", "revoked", nil, false)
	mk(-5, "", "active", ptrTime(now.Add(time.Hour)), false)

	dashboard, err := svc.GetAdminDashboard(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), dashboard.TotalSubscriptions)
	assert.Equal(t, int64(4), dashboard.Users)
	assert.Equal(t, int64(1), dashboard.Trials)
	assert.Equal(t, int64(3), dashboard.Active)
	assert.Equal(t, int64(1), dashboard.Paused)
	assert.Equal(t, int64(1), dashboard.Revoked)
	assert.Equal(t, int64(1), dashboard.ActiveExpired)
	assert.Equal(t, int64(2), dashboard.Paid)
	assert.Equal(t, int64(2), dashboard.ExpiringIn7d, "alice and the trial expire within 7 days; carol is paused")
	assert.Zero(t, dashboard.AuditLast24h)

	rows, total, err := svc.ListAdminUsers(ctx, AdminUserFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, rows, 4)
	assert.Equal(t, "dave", rows[0].Username, "newest first")
	assert.Equal(t, FreePlanName, rows[0].PlanName)
	for _, row := range rows {
		assert.Positive(t, row.TelegramID, "trials are not users")
	}

	rows, total, err = svc.ListAdminUsers(ctx, AdminUserFilter{Status: "paused"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "carol", rows[0].Username)

	rows, total, err = svc.ListAdminUsers(ctx, AdminUserFilter{Query: "1002"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "bob_smith", rows[0].Username)

	// Numeric query is "telegram_id = ? OR id = ?"; combined with a status
	// filter it must stay grouped, otherwise alice (id=1, active) leaks through.
	rows, total, err = svc.ListAdminUsers(ctx, AdminUserFilter{Status: "paused", Query: "1"})
	require.NoError(t, err)
	assert.Zero(t, total, "OR condition must be parenthesised against the status filter")
	assert.Empty(t, rows)

	rows, _, err = svc.ListAdminUsers(ctx, AdminUserFilter{Query: "@BOB"})
	require.NoError(t, err)
	require.Len(t, rows, 1, "username search is case-insensitive and ignores @")
	assert.Equal(t, "bob_smith", rows[0].Username)

	rows, total, err = svc.ListAdminUsers(ctx, AdminUserFilter{Query: "%"})
	require.NoError(t, err)
	assert.Zero(t, total, "LIKE metacharacters are escaped")
	assert.Empty(t, rows)

	rows, total, err = svc.ListAdminUsers(ctx, AdminUserFilter{Limit: 2, Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, rows, 2)
	assert.Equal(t, "carol", rows[0].Username)
	assert.Equal(t, "bob_smith", rows[1].Username)
}

func TestAdminSubscriptionDetailAndAuditPaging(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	svc, sub := adminFixture(t, "active", &future)
	ctx := context.Background()
	node := createTestNode(t, svc, "node-a", "https://node-a", "token")
	require.NoError(t, svc.UpsertSubscriptionNode(ctx, &SubscriptionNode{SubscriptionID: sub.ID, NodeID: node.ID, Status: SyncStatusPendingAdd}))
	for i := range 3 {
		_, err := svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionRenew, fmt.Sprintf("k-%d", i)))
		require.NoError(t, err)
	}

	detail, err := svc.GetAdminSubscription(ctx, sub.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, detail.Subscription.ID)
	require.NotNil(t, detail.Plan)
	assert.Equal(t, FreePlanName, detail.Plan.Name)
	require.Len(t, detail.Nodes, 1)
	assert.Equal(t, "node-a", detail.Nodes[0].NodeName)
	assert.Equal(t, SyncStatusPendingAdd, detail.Nodes[0].Status)
	require.Len(t, detail.Audit, 2, "audit limit is honoured")
	assert.Greater(t, detail.Audit[0].ID, detail.Audit[1].ID)

	_, err = svc.GetAdminSubscription(ctx, 999999, 0)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)

	all, err := svc.ListAdminAudit(ctx, AdminAuditFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)
	page, err := svc.ListAdminAudit(ctx, AdminAuditFilter{BeforeID: all[0].ID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, all[1].ID, page[0].ID)
	none, err := svc.ListAdminAudit(ctx, AdminAuditFilter{SubscriptionID: 999999})
	require.NoError(t, err)
	assert.Empty(t, none)

	dashboard, err := svc.GetAdminDashboard(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), dashboard.AuditLast24h)
}
