package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// journalOf returns the journal rows of one subscription in insertion order.
func journalOf(t *testing.T, svc *Service, subID uint) []JournalEvent {
	t.Helper()
	var events []JournalEvent
	require.NoError(t, svc.db.Where("subscription_id = ?", subID).Order("id ASC").Find(&events).Error)
	return events
}

func journalTypes(events []JournalEvent) []string {
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, e.EventType)
	}
	return types
}

func journalDetails(t *testing.T, e JournalEvent) map[string]any {
	t.Helper()
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(e.Details), &details))
	return details
}

type journalPaidFixture struct {
	svc     *Service
	plan    *Plan
	product *Product
	sub     *Subscription
}

func newJournalPaidFixture(t *testing.T, telegramID int64) *journalPaidFixture {
	t.Helper()
	svc := newTestService(t)
	plan := &Plan{Name: fmt.Sprintf("journal-premium-%d", telegramID), IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "Премиум месяц", DurationDays: 30, PriceCents: 29900, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	sub := createTestSubscription(t, svc, telegramID, fmt.Sprintf("user%d", telegramID), fmt.Sprintf("client-%d", telegramID))
	sub.ExpiresAt = nil
	require.NoError(t, svc.UpdateSubscription(context.Background(), sub))
	return &journalPaidFixture{svc: svc, plan: plan, product: product, sub: sub}
}

func (f *journalPaidFixture) pendingOrder(t *testing.T, provider uuid.UUID) *Order {
	t.Helper()
	order := &Order{SubscriptionID: f.sub.ID, ProductID: f.product.ID, Status: OrderStatusPending, AmountCents: f.product.PriceCents,
		Currency: f.product.Currency, PaymentProvider: "platega", ProviderPaymentID: provider.String(), CreatedAt: time.Now().UTC()}
	require.NoError(t, f.svc.db.Create(order).Error)
	return order
}

func (f *journalPaidFixture) confirm(t *testing.T, order *Order) bool {
	t.Helper()
	sub, err := f.svc.GetByID(context.Background(), f.sub.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	activated, err := f.svc.ConfirmOrderPaidCAS(context.Background(), order.ID, now, now, sub, f.product, nil, order.AmountCents)
	require.NoError(t, err)
	return activated
}

func TestJournal_RegistrationIsRecordedOnce(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	sub := createTestSubscription(t, svc, 880001, "reg-user", "reg-client")

	events := journalOf(t, svc, sub.ID)
	require.Len(t, events, 1)
	e := events[0]
	assert.Equal(t, string(JournalUserRegistered), e.EventType)
	assert.Equal(t, JournalActorUser, e.Actor)
	assert.Equal(t, JournalOutcomeSuccess, e.Outcome)
	assert.Equal(t, int64(880001), e.TelegramID)
	assert.Equal(t, "reg-user", e.Username)
	assert.Equal(t, FreePlanName, e.PlanName)
	assert.Equal(t, PlanKindFree, e.PlanKind, "ordinary activation is a free subscription")
	assert.NotEmpty(t, e.Description)
	assert.WithinDuration(t, time.Now(), e.CreatedAt, time.Minute)
}

func TestJournal_TrialStartBindAndExpiry(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()

	trial, err := svc.CreateTrialSubscription(ctx, "", "journal-trial", "journal-trial-client", time.Now().Add(3*time.Hour))
	require.NoError(t, err)
	events := journalOf(t, svc, trial.ID)
	require.Len(t, events, 1)
	assert.Equal(t, string(JournalTrialStarted), events[0].EventType)
	assert.Equal(t, PlanKindTrial, events[0].PlanKind)
	assert.Negative(t, events[0].TelegramID, "an unbound trial has no Telegram user yet")

	_, err = svc.BindTrialSubscription(ctx, "journal-trial", 880101, "binder")
	require.NoError(t, err)
	_, err = svc.BindTrialSubscription(ctx, "journal-trial", 880101, "binder")
	require.ErrorIs(t, err, ErrTrialAlreadyActivated)
	events = journalOf(t, svc, trial.ID)
	require.Equal(t, []string{string(JournalTrialStarted), string(JournalTrialBound)}, journalTypes(events),
		"a repeated bind must not add a second event")
	bound := events[1]
	assert.Equal(t, int64(880101), bound.TelegramID)
	assert.Equal(t, "binder", bound.Username)
	assert.Equal(t, PlanKindFree, bound.PlanKind)
	details := journalDetails(t, bound)
	assert.Equal(t, TrialPlanName, details["before"].(map[string]any)["plan_name"])
	assert.Equal(t, FreePlanName, details["after"].(map[string]any)["plan_name"])

	// An old anonymous trial expires; the cleanup scan claims it repeatedly
	// while deprovisioning is retried, but the expiry is journaled once.
	trialPlan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)
	old := &Subscription{TelegramID: -880102, ClientID: "old-journal-trial", SubscriptionID: "old-journal-trial-sub", PlanID: trialPlan.ID,
		Status: "active", ExpiresAt: ptrTime(time.Now().Add(-time.Hour)), CreatedAt: time.Now().Add(-48 * time.Hour)}
	require.NoError(t, svc.db.Create(old).Error)
	for i := 0; i < 2; i++ {
		claimed, err := svc.ClaimExpiredTrials(ctx, 24)
		require.NoError(t, err)
		require.Len(t, claimed, 1)
	}
	events = journalOf(t, svc, old.ID)
	require.Len(t, events, 1)
	assert.Equal(t, string(JournalTrialExpired), events[0].EventType)
	assert.Equal(t, JournalActorSystem, events[0].Actor)
	assert.Equal(t, PlanKindTrial, events[0].PlanKind)

	// The row is physically removed after deprovisioning; its history stays.
	require.NoError(t, svc.DeleteClaimedTrial(ctx, old.ID))
	assert.Len(t, journalOf(t, svc, old.ID), 1)
}

func TestJournal_PaidActivationRenewalAndReplay(t *testing.T) {
	t.Parallel()
	f := newJournalPaidFixture(t, 880201)

	first := f.pendingOrder(t, uuid.MustParse("550e8400-e29b-41d4-a716-446655447001"))
	require.True(t, f.confirm(t, first))
	events := journalOf(t, f.svc, f.sub.ID)
	require.Equal(t, []string{string(JournalUserRegistered), string(JournalPaymentSucceeded), string(JournalPaidActivated)}, journalTypes(events))
	payment, activation := events[1], events[2]
	for _, e := range []JournalEvent{payment, activation} {
		assert.Equal(t, JournalActorUser, e.Actor)
		assert.Equal(t, "platega", e.ActorName)
		assert.Equal(t, PlanKindPaid, e.PlanKind)
		assert.Equal(t, f.plan.Name, e.PlanName)
		require.NotNil(t, e.OrderID)
		assert.Equal(t, first.ID, *e.OrderID)
		require.NotNil(t, e.AmountCents)
		assert.Equal(t, int64(29900), *e.AmountCents)
		require.NotNil(t, e.Currency)
		assert.Equal(t, "RUB", *e.Currency)
	}
	before := journalDetails(t, activation)["before"].(map[string]any)
	assert.Equal(t, FreePlanName, before["plan_name"])
	assert.Nil(t, before["expires_at"])
	assert.NotNil(t, journalDetails(t, activation)["after"].(map[string]any)["expires_at"])

	// The provider retries the same CONFIRMED callback: the CAS is a no-op and
	// nothing new is journaled.
	assert.False(t, f.confirm(t, first))
	assert.Len(t, journalOf(t, f.svc, f.sub.ID), 3)

	// A second purchase of the same plan is a renewal.
	second := f.pendingOrder(t, uuid.MustParse("550e8400-e29b-41d4-a716-446655447002"))
	require.True(t, f.confirm(t, second))
	events = journalOf(t, f.svc, f.sub.ID)
	require.Len(t, events, 5)
	assert.Equal(t, string(JournalPaymentSucceeded), events[3].EventType)
	assert.Equal(t, string(JournalRenewed), events[4].EventType)
	assert.Equal(t, second.ID, *events[4].OrderID)
}

func TestJournal_ExpirationIsRecordedOncePerTerm(t *testing.T) {
	t.Parallel()
	f := newJournalPaidFixture(t, 880301)
	ctx := context.Background()
	freePlan, err := f.svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	past := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, f.svc.db.Model(&Subscription{}).Where("id = ?", f.sub.ID).Updates(map[string]any{
		"plan_id": f.plan.ID, "expires_at": past, "product_id": f.product.ID, "price_paid_cents": 29900,
	}).Error)

	require.NoError(t, f.svc.ExpireSubscriptionWithPlanCAS(ctx, f.sub.ID, freePlan.ID, nil))
	require.NoError(t, f.svc.ExpireSubscriptionWithPlanCAS(ctx, f.sub.ID, freePlan.ID, nil), "a queued repeat is a no-op")

	events := journalOf(t, f.svc, f.sub.ID)
	require.Equal(t, []string{string(JournalUserRegistered), string(JournalExpired)}, journalTypes(events))
	expired := events[1]
	assert.Equal(t, JournalActorSystem, expired.Actor)
	assert.Equal(t, PlanKindPaid, expired.PlanKind, "the event names the plan that expired")
	details := journalDetails(t, expired)
	assert.Equal(t, f.plan.Name, details["before"].(map[string]any)["plan_name"])
	assert.Equal(t, FreePlanName, details["after"].(map[string]any)["plan_name"])

	stored, err := f.svc.GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	assert.Equal(t, freePlan.ID, stored.PlanID, "the lifecycle change itself is unchanged")
}

func TestJournal_RenewalByCustomerAndSystem(t *testing.T) {
	t.Parallel()
	f := newJournalPaidFixture(t, 880401)
	ctx := context.Background()
	future := time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, f.svc.db.Model(&Subscription{}).Where("id = ?", f.sub.ID).Updates(map[string]any{"plan_id": f.plan.ID, "expires_at": future}).Error)

	_, err := f.svc.RenewCustomerSubscription(ctx, f.sub.TelegramID, 7)
	require.NoError(t, err)
	_, err = f.svc.RenewSubscription(ctx, f.sub.ID, 3)
	require.NoError(t, err)

	events := journalOf(t, f.svc, f.sub.ID)
	require.Equal(t, []string{string(JournalUserRegistered), string(JournalRenewed), string(JournalRenewed)}, journalTypes(events))
	assert.Equal(t, JournalActorUser, events[1].Actor)
	assert.Contains(t, events[1].Description, "7 дн.")
	assert.Equal(t, JournalActorSystem, events[2].Actor)
	assert.EqualValues(t, 3, journalDetails(t, events[2])["days"])
}

func TestJournal_AdminMutationsAndReplay(t *testing.T) {
	t.Parallel()
	future := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	svc, sub := adminFixture(t, string(SubscriptionStatusActive), &future)
	ctx := context.Background()

	_, err := svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionDisable, "journal-disable"))
	require.NoError(t, err)
	_, err = svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionDisable, "journal-disable"))
	require.NoError(t, err, "replay of the same key")
	_, err = svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionEnable, "journal-enable"))
	require.NoError(t, err)
	_, err = svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionEnable, "journal-enable-again"))
	require.ErrorIs(t, err, ErrAdminInvalidState)
	_, err = svc.AdminMutateSubscription(ctx, adminMutation(sub, AdminActionRenew, "journal-renew"))
	require.NoError(t, err)
	expiry := future.Add(240 * time.Hour)
	m := adminMutation(sub, AdminActionChangeExpiry, "journal-expiry")
	m.ExpiresAt = &expiry
	_, err = svc.AdminMutateSubscription(ctx, m)
	require.NoError(t, err)

	events := journalOf(t, svc, sub.ID)
	require.Equal(t, []string{
		string(JournalUserRegistered), string(JournalDisabled), string(JournalEnabled), string(JournalEnabled),
		string(JournalRenewed), string(JournalExpiryChanged),
	}, journalTypes(events), "the replayed disable must not be journaled twice")
	for _, e := range events[1:] {
		assert.Equal(t, JournalActorAdmin, e.Actor)
		assert.Equal(t, "admin", e.ActorName)
	}
	assert.Equal(t, JournalOutcomeSuccess, events[1].Outcome)
	assert.Equal(t, "paused", journalDetails(t, events[1])["after"].(map[string]any)["status"])
	rejected := events[3]
	assert.Equal(t, JournalOutcomeRejected, rejected.Outcome)
	assert.Contains(t, rejected.Description, "отклонено")
	assert.Equal(t, AdminErrorCodeInvalidState, journalDetails(t, rejected)["error_code"])
}

func TestJournal_PaymentFailureAndChargeback(t *testing.T) {
	t.Parallel()
	f := newJournalPaidFixture(t, 880501)
	ctx := context.Background()
	freePlan, err := f.svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)

	failedID := uuid.MustParse("550e8400-e29b-41d4-a716-446655447101")
	f.pendingOrder(t, failedID)
	for i := 0; i < 2; i++ {
		_, err := f.svc.CancelOrderCAS(ctx, "platega", failedID, []OrderStatus{OrderStatusPending})
		require.NoError(t, err)
	}
	paidID := uuid.MustParse("550e8400-e29b-41d4-a716-446655447102")
	paid := f.pendingOrder(t, paidID)
	require.True(t, f.confirm(t, paid))
	for i := 0; i < 2; i++ {
		_, err := f.svc.CancelPaidOrderAndDowngradeCAS(ctx, "platega", paidID, time.Now().UTC(), freePlan.ID, nil)
		require.NoError(t, err)
	}

	events := journalOf(t, f.svc, f.sub.ID)
	require.Equal(t, []string{
		string(JournalUserRegistered), string(JournalPaymentFailed),
		string(JournalPaymentSucceeded), string(JournalPaidActivated), string(JournalPaymentRefunded),
	}, journalTypes(events), "repeated provider callbacks must not duplicate events")
	failed := events[1]
	assert.Equal(t, JournalOutcomeFailed, failed.Outcome)
	assert.Equal(t, JournalActorSystem, failed.Actor)
	assert.Equal(t, PlanKindPaid, failed.PlanKind, "the plan of the attempted purchase")
	refund := events[4]
	assert.Equal(t, JournalActorSystem, refund.Actor)
	assert.Equal(t, true, journalDetails(t, refund)["downgraded"])
	assert.Contains(t, refund.Description, "бесплатный")
}

func TestJournal_UpdateSubscriptionWithJournal(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 880601, "revoked-user", "revoked-client")
	before := JournalStateOf(sub)
	sub.Status = string(SubscriptionStatusRevoked)
	require.NoError(t, svc.UpdateSubscriptionWithJournal(ctx, sub, JournalRecord{
		Type: JournalRevoked, Actor: JournalActorAdmin, ActorName: "bot", Description: "Подписка отозвана",
		Before: before, After: JournalStateOf(sub),
	}))
	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, string(SubscriptionStatusRevoked), stored.Status)
	events := journalOf(t, svc, sub.ID)
	require.Len(t, events, 2)
	assert.Equal(t, string(JournalRevoked), events[1].EventType)

	// A failed update records nothing.
	missing := &Subscription{ID: 999999, Status: "active"}
	err = svc.UpdateSubscriptionWithJournal(ctx, missing, JournalRecord{Type: JournalRevoked, Description: "x"})
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
	assert.Empty(t, journalOf(t, svc, missing.ID))
}

func TestJournal_IsAppendOnly(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	sub := createTestSubscription(t, svc, 880701, "append-only", "append-only-client")
	events := journalOf(t, svc, sub.ID)
	require.Len(t, events, 1)

	err := svc.db.Model(&JournalEvent{}).Where("id = ?", events[0].ID).Update("description", "edited").Error
	require.ErrorContains(t, err, "append-only")
	err = svc.db.Delete(&JournalEvent{}, events[0].ID).Error
	require.ErrorContains(t, err, "append-only")
	assert.Equal(t, events[0].Description, journalOf(t, svc, sub.ID)[0].Description)
}

func TestJournal_RecordingIsBestEffortAndDeduplicated(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sub := createTestSubscription(t, svc, 880801, "best-effort", "best-effort-client")

	err := svc.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Subscription{}).Where("id = ?", sub.ID).Update("username", "renamed").Error; err != nil {
			return err
		}
		// A CHECK violation of the journal must not abort the business change.
		recordJournal(ctx, tx, JournalRecord{Type: JournalRenewed, Actor: "robot", Subscription: sub, Description: "invalid actor"})
		recordJournal(ctx, tx, JournalRecord{Type: JournalRenewed, Subscription: sub, Description: "valid", DedupKey: "dedup-test"})
		recordJournal(ctx, tx, JournalRecord{Type: JournalRenewed, Subscription: sub, Description: "duplicate", DedupKey: "dedup-test"})
		return nil
	})
	require.NoError(t, err)

	stored, err := svc.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, "renamed", stored.Username, "the enclosing transaction committed")
	events := journalOf(t, svc, sub.ID)
	require.Len(t, events, 2)
	assert.Equal(t, "valid", events[1].Description)
	assert.Equal(t, JournalActorSystem, events[1].Actor, "actor defaults to system")
}

func TestJournal_ListOrderingFiltersAndPaging(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	free, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)
	trial, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)
	paid := &Plan{Name: "journal-list-paid", IsActive: true}
	require.NoError(t, svc.db.Create(paid).Error)

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	alice := &Subscription{ID: 7001, TelegramID: 5551, Username: "alice", PlanID: free.ID}
	bob := &Subscription{ID: 7002, TelegramID: 5552, Username: "bob_smith", PlanID: paid.ID}
	anon := &Subscription{ID: 7003, TelegramID: -5553, PlanID: trial.ID}
	// Inserted out of chronological order: the list sorts by event time.
	records := []JournalRecord{
		{Type: JournalPaymentSucceeded, Subscription: bob, Description: "b-pay", At: base.Add(3 * time.Hour)},
		{Type: JournalUserRegistered, Subscription: alice, Description: "a-reg", At: base},
		{Type: JournalTrialStarted, Subscription: anon, Description: "t-start", At: base.Add(time.Hour)},
		{Type: JournalPaidActivated, Subscription: bob, Description: "b-act", At: base.Add(3 * time.Hour)},
		{Type: JournalRenewed, Subscription: alice, PlanID: paid.ID, Description: "a-renew", At: base.Add(48 * time.Hour)},
	}
	for _, rec := range records {
		recordJournal(ctx, svc.db, rec)
	}

	descriptions := func(events []JournalEvent) []string {
		out := make([]string, 0, len(events))
		for _, e := range events {
			out = append(out, e.Description)
		}
		return out
	}

	all, total, err := svc.ListJournal(ctx, JournalFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.Equal(t, []string{"a-renew", "b-act", "b-pay", "t-start", "a-reg"}, descriptions(all),
		"newest first; equal timestamps fall back to the newest row")

	page, total, err := svc.ListJournal(ctx, JournalFilter{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.Equal(t, []string{"b-pay", "t-start"}, descriptions(page))

	byType, total, err := svc.ListJournal(ctx, JournalFilter{Type: string(JournalPaymentSucceeded)})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, []string{"b-pay"}, descriptions(byType))

	byKind, _, err := svc.ListJournal(ctx, JournalFilter{PlanKind: PlanKindPaid})
	require.NoError(t, err)
	assert.Equal(t, []string{"a-renew", "b-act", "b-pay"}, descriptions(byKind))

	byUsername, _, err := svc.ListJournal(ctx, JournalFilter{Query: "@bob"})
	require.NoError(t, err)
	assert.Equal(t, []string{"b-act", "b-pay"}, descriptions(byUsername))
	literal, _, err := svc.ListJournal(ctx, JournalFilter{Query: "b_s"})
	require.NoError(t, err)
	assert.Len(t, literal, 2)
	wildcard, _, err := svc.ListJournal(ctx, JournalFilter{Query: "bo_"})
	require.NoError(t, err)
	assert.Empty(t, wildcard, "LIKE wildcards in the query are literal (bo_ must not match bob)")
	byTelegram, _, err := svc.ListJournal(ctx, JournalFilter{Query: "5551"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a-renew", "a-reg"}, descriptions(byTelegram))
	bySubscription, _, err := svc.ListJournal(ctx, JournalFilter{Query: "7003"})
	require.NoError(t, err)
	assert.Equal(t, []string{"t-start"}, descriptions(bySubscription))

	from, to := base.Add(time.Hour), base.Add(3*time.Hour)
	period, _, err := svc.ListJournal(ctx, JournalFilter{From: &from, To: &to})
	require.NoError(t, err)
	assert.Equal(t, []string{"t-start"}, descriptions(period), "from inclusive, to exclusive")

	one, err := svc.GetJournalEvent(ctx, all[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "a-renew", one.Description)
	_, err = svc.GetJournalEvent(ctx, 999999)
	require.ErrorIs(t, err, ErrJournalEventNotFound)
}

// Migration 047 on a populated database: registrations, paid orders and admin
// audit entries are backfilled with the live dedup keys, so the upgrade adds
// history without duplicating it later.
func TestJournalMigration047_BackfillsExistingHistory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(46))

	free, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)
	paid := &Plan{Name: "backfill-paid", IsActive: true}
	require.NoError(t, svc.db.Create(paid).Error)
	product := &Product{PlanID: paid.ID, Name: "backfill", DurationDays: 30, PriceCents: 500, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	created := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	sub := &Subscription{TelegramID: 880901, Username: "legacy", ClientID: "legacy-client", SubscriptionID: "legacy-sub", PlanID: free.ID, Status: "active", CreatedAt: created}
	require.NoError(t, svc.db.Create(sub).Error)
	paidAt := created.Add(24 * time.Hour)
	order := &Order{SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid, AmountCents: 500, Currency: "RUB",
		PaymentProvider: "platega", ProviderPaymentID: "550e8400-e29b-41d4-a716-446655447201", CreatedAt: created, PaidAt: &paidAt}
	require.NoError(t, svc.db.Create(order).Error)
	audit := AdminAuditLog{Actor: "admin", Action: string(AdminActionRenew), SubscriptionID: sub.ID, CreatedAt: paidAt.Add(time.Hour),
		OldValue: `{"status":"active"}`, NewValue: `{"status":"active"}`, Success: true, RequestKey: "legacy-key", RequestHash: "h",
		TargetType: AdminTargetSubscription, TargetID: sub.ID}
	require.NoError(t, svc.db.Create(&audit).Error)

	require.NoError(t, applyMigrations(sqlDB, 47))

	events := journalOf(t, svc, sub.ID)
	require.Equal(t, []string{string(JournalUserRegistered), string(JournalPaymentSucceeded), string(JournalRenewed)}, journalTypes(events))
	assert.True(t, events[0].CreatedAt.Equal(created), "registration keeps the row creation time: %s", events[0].CreatedAt)
	assert.True(t, events[1].CreatedAt.Equal(paidAt))
	assert.Equal(t, PlanKindPaid, events[1].PlanKind)
	assert.Equal(t, JournalActorAdmin, events[2].Actor)
	for _, e := range events {
		assert.Equal(t, true, journalDetails(t, e)["backfill"])
	}

	list, _, err := svc.ListJournal(ctx, JournalFilter{Query: "legacy"})
	require.NoError(t, err)
	require.Len(t, list, 3)
	assert.Equal(t, string(JournalRenewed), list[0].EventType, "backfilled rows sort by their original time")

	// The same facts replayed by live code are recognised by the dedup key.
	recordJournal(ctx, svc.db, JournalRecord{Type: JournalPaymentSucceeded, Subscription: sub, Description: "live", DedupKey: journalKey("order_paid", order.ID)})
	assert.Len(t, journalOf(t, svc, sub.ID), 3)
}
