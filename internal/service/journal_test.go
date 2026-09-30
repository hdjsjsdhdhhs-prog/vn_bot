package service

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func journalServiceFixture(t *testing.T) (*database.Service, *SubscriptionService) {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "journal.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{GlobalSubURL: "https://journal.example/sub/"})
	return db, svc
}

func serviceJournal(t *testing.T, db *database.Service, subID uint) []database.JournalEvent {
	t.Helper()
	var events []database.JournalEvent
	require.NoError(t, db.GetDB().Where("subscription_id = ?", subID).Order("id ASC").Find(&events).Error)
	return events
}

func serviceJournalTypes(events []database.JournalEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.EventType)
	}
	return out
}

func TestJournal_CreateIsIdempotentAndReconnectIsRecorded(t *testing.T) {
	db, svc := journalServiceFixture(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, 770001, "journal", "")
	require.NoError(t, err)
	// /start pressed again: the existing subscription is returned, nothing new happened.
	again, err := svc.Create(ctx, 770001, "journal", "")
	require.NoError(t, err)
	require.Equal(t, first.Subscription.ID, again.Subscription.ID)
	require.Equal(t, []string{string(database.JournalUserRegistered)}, serviceJournalTypes(serviceJournal(t, db, first.Subscription.ID)))

	// A revoked row is reanimated on the next /start: "connected again".
	sub := first.Subscription
	sub.Status = string(database.SubscriptionStatusRevoked)
	require.NoError(t, db.UpdateSubscription(ctx, sub))
	reconnected, err := svc.Create(ctx, 770001, "journal", "")
	require.NoError(t, err)
	require.Equal(t, sub.ID, reconnected.Subscription.ID)

	events := serviceJournal(t, db, sub.ID)
	require.Equal(t, []string{string(database.JournalUserRegistered), string(database.JournalSubscriptionReconnected)}, serviceJournalTypes(events))
	assert.Equal(t, database.JournalActorUser, events[1].Actor)
	assert.Equal(t, database.PlanKindFree, events[1].PlanKind)
}

func TestJournal_AdminSetPlanAndDelete(t *testing.T) {
	db, svc := journalServiceFixture(t)
	ctx := context.Background()
	paid := &database.Plan{Name: "journal-setplan", IsActive: true}
	require.NoError(t, db.GetDB().Create(paid).Error)
	free, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)

	created, err := svc.Create(ctx, 770101, "setplan", "")
	require.NoError(t, err)
	id := created.Subscription.ID

	_, err = svc.AdminSetPlan(ctx, id, paid.ID, 10)
	require.NoError(t, err)
	_, err = svc.AdminSetPlan(ctx, id, free.ID, 0)
	require.NoError(t, err)
	_, err = svc.DeleteByID(ctx, id)
	require.NoError(t, err)

	_, err = db.GetByID(ctx, id)
	require.ErrorIs(t, err, database.ErrSubscriptionNotFound, "/del physically deletes the row")
	events := serviceJournal(t, db, id)
	require.Equal(t, []string{
		string(database.JournalUserRegistered), string(database.JournalPlanChanged),
		string(database.JournalFreeActivated), string(database.JournalRevoked),
	}, serviceJournalTypes(events), "the history outlives the deleted subscription")
	assert.Equal(t, database.PlanKindPaid, events[1].PlanKind)
	assert.Contains(t, events[1].Description, paid.Name)
	for _, e := range events[1:] {
		assert.Equal(t, database.JournalActorAdmin, e.Actor)
	}
	assert.Equal(t, int64(770101), events[3].TelegramID)
}

func TestJournal_OrphanRevokeIsSystemEvent(t *testing.T) {
	db, svc := journalServiceFixture(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, 770201, "orphan", "")
	require.NoError(t, err)
	node := &database.Node{Name: "orphan-node", Type: database.NodeTypeFetch, IsActive: true, SubscriptionURL: "https://node.example/sub"}
	require.NoError(t, db.GetDB().Create(node).Error)
	require.NoError(t, db.UpsertSubscriptionNode(ctx, &database.SubscriptionNode{SubscriptionID: created.Subscription.ID, NodeID: node.ID, Status: database.SyncStatusPendingRemove}))

	revoked, err := svc.ReconcileOrphanedClients(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, revoked)
	revokedAgain, err := svc.ReconcileOrphanedClients(ctx)
	require.NoError(t, err)
	require.Zero(t, revokedAgain)

	events := serviceJournal(t, db, created.Subscription.ID)
	require.Equal(t, []string{string(database.JournalUserRegistered), string(database.JournalRevoked)}, serviceJournalTypes(events))
	assert.Equal(t, database.JournalActorSystem, events[1].Actor)
}

func TestAdminService_ListJournalValidatesFilters(t *testing.T) {
	db, svc := journalServiceFixture(t)
	ctx := context.Background()
	_, err := svc.Create(ctx, 770301, "lister", "")
	require.NoError(t, err)
	admin := NewAdminService(db, nil, nil)

	page, err := admin.ListJournal(ctx, database.JournalFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, page.Total)
	assert.Equal(t, database.AdminDefaultPageSize, page.Limit)
	require.Len(t, page.Events, 1)
	assert.JSONEq(t, `{"after":{"status":"active","expires_at":null,"plan_id":`+jsonUint(page.Events[0].PlanID)+`,"plan_name":"free"}}`, string(page.Events[0].Details))

	for _, f := range []database.JournalFilter{{Type: "unknown"}, {PlanKind: "gold"}} {
		_, err := admin.ListJournal(ctx, f)
		require.ErrorIs(t, err, database.ErrAdminInvalidRequest)
	}
	view, err := admin.GetJournalEvent(ctx, page.Events[0].ID)
	require.NoError(t, err)
	assert.Equal(t, page.Events[0].ID, view.ID)

	// A repository without journal support reports it instead of panicking.
	_, err = NewAdminService(&fakeAdminRepo{}, nil, nil).ListJournal(ctx, database.JournalFilter{})
	require.ErrorIs(t, err, ErrJournalUnavailable)
}

func jsonUint(v *uint) string {
	if v == nil {
		return "null"
	}
	return strconv.FormatUint(uint64(*v), 10)
}
