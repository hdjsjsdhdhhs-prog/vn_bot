package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	err := testutil.InitLogger(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestSubscriptionService_Create_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.DatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 1
			return nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Subscription)
	assert.NotEmpty(t, result.Subscription.ClientID)
	assert.NotEmpty(t, result.Subscription.SubscriptionID)
	assert.Equal(t, int64(123456), result.Subscription.TelegramID)
	assert.Equal(t, "testuser", result.Subscription.Username)
}

func TestSubscriptionService_Create_DBError_RollbackSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	db := &testutil.DatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			return errors.New("database error")
		},
	}

	svc := NewSubscriptionService(db, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create subscription")
}

func TestSubscriptionService_Create_PropagatesInviteCodeToDB(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var (
		gotSub        *database.Subscription
		gotInviteCode string
	)

	db := &testutil.DatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			gotSub = sub
			gotInviteCode = inviteCode
			sub.ID = 1

			return nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "INV-ABC")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "INV-ABC", gotInviteCode, "inviteCode must be forwarded to db.CreateSubscription")
	require.NotNil(t, gotSub)
	assert.Equal(t, int64(123456), gotSub.TelegramID)
	// Mock does not simulate invite resolution; the real DB layer would set
	// sub.InviteCode/sub.ReferredBy. We only assert the parameter was passed.
	assert.Equal(t, int64(0), result.ReferrerTGID)
}

func TestSubscriptionService_Create_EmptyInviteCodeIsNoop(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var gotInviteCode string

	db := &testutil.DatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			gotInviteCode = inviteCode
			sub.ID = 1

			return nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	result, err := svc.Create(context.Background(), 123456, "testuser", "")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "", gotInviteCode)
	assert.Equal(t, int64(0), result.ReferrerTGID)
}

func TestSubscriptionService_Create_FillsFallbackUsername(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	var gotSub *database.Subscription

	db := &testutil.DatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			gotSub = sub
			sub.ID = 1

			return nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	result, err := svc.Create(context.Background(), 123456, "", "")

	assert.NoError(t, err)
	require.NotNil(t, gotSub)
	assert.Equal(t, "tgId_123456", gotSub.Username)
	assert.Equal(t, "tgId_123456", result.Subscription.Username)
}

func TestSubscriptionService_GetWithDetails_PlanNameAndDates(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	expires := created.Add(7 * 24 * time.Hour)
	sub := &database.Subscription{TelegramID: 123456, Username: "testuser", PlanID: 1, CreatedAt: created, ExpiresAt: &expires}

	db := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: "Free"}, nil
		},
	}

	svc := NewSubscriptionService(db, &config.Config{})
	resultSub, details, err := svc.GetWithDetails(context.Background(), 123456)

	require.NoError(t, err)
	assert.Same(t, sub, resultSub)
	require.NotNil(t, details)
	assert.Equal(t, "Free", details.PlanName)
	assert.Equal(t, utils.FormatDateRu(created), details.CreatedAtFormatted)
	assert.Equal(t, utils.FormatDateRu(expires), details.ExpiresAtFormatted)
}

func TestSubscriptionService_GetWithDetails_ProductNameAndPerpetual(t *testing.T) {
	t.Parallel()

	productID := uint(7)
	sub := &database.Subscription{TelegramID: 1, PlanID: 2, ProductID: &productID, CreatedAt: time.Now()}

	db := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: "premium"}, nil
		},
		GetProductByIDFunc: func(ctx context.Context, id uint) (*database.Product, error) {
			return &database.Product{ID: id, Name: "Premium 30 дней"}, nil
		},
	}

	svc := NewSubscriptionService(db, &config.Config{})
	_, details, err := svc.GetWithDetails(context.Background(), 1)

	require.NoError(t, err)
	assert.Equal(t, "Premium 30 дней", details.PlanName, "the purchased product name wins over the plan name")
	assert.Equal(t, "бессрочно", details.ExpiresAtFormatted)
}

func TestSubscriptionService_GetWithDetails_NotFound(t *testing.T) {
	t.Parallel()

	db := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, database.ErrSubscriptionNotFound
		},
	}

	svc := NewSubscriptionService(db, &config.Config{})
	sub, details, err := svc.GetWithDetails(context.Background(), 1)

	require.ErrorIs(t, err, database.ErrSubscriptionNotFound)
	assert.Nil(t, sub)
	assert.Nil(t, details)
}

func TestSubscriptionService_CreateTrial_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 3, GlobalSubURL: "https://trial.example/sub/"}

	var (
		gotInvite string
		gotExpiry time.Time
	)

	db := &testutil.DatabaseService{
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			gotInvite, gotExpiry = inviteCode, expiryTime
			return &database.Subscription{ID: 1, SubscriptionID: subscriptionID, ClientID: clientID, Token: "trial-token", ExpiresAt: &expiryTime}, nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	result, err := svc.CreateTrial(context.Background(), "testcode")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.SubID)
	assert.NotEmpty(t, result.ClientID)
	assert.Equal(t, result.SubID, result.Subscription.SubscriptionID)
	assert.Equal(t, cfg.SubURL("trial-token"), result.SubscriptionURL)
	assert.Equal(t, "testcode", gotInvite)
	assert.WithinDuration(t, time.Now().UTC().Add(3*time.Hour), gotExpiry, time.Minute)
}

func TestSubscriptionService_CreateTrial_DBError(t *testing.T) {
	t.Parallel()

	db := &testutil.DatabaseService{
		CreateTrialSubscriptionFunc: func(ctx context.Context, inviteCode, subscriptionID, clientID string, expiryTime time.Time) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}

	svc := NewSubscriptionService(db, &config.Config{TrialDurationHours: 3})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create trial subscription")
	assert.Nil(t, result)
}

// TestSubscriptionService_BindTrial_Success: binding is the DB operation
// itself; the bound row is returned and the trial's /sub cache is dropped.
func TestSubscriptionService_BindTrial_Success(t *testing.T) {
	t.Parallel()

	bound := &database.Subscription{ID: 42, TelegramID: 123456, Username: "testuser", ClientID: "client-xyz", SubscriptionID: "trial-sub-1", Status: "active", PlanID: 2}

	var (
		gotSubID    string
		gotTG       int64
		gotUsername string
	)

	db := &testutil.DatabaseService{
		BindTrialSubscriptionFunc: func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
			gotSubID, gotTG, gotUsername = subscriptionID, telegramID, username
			return bound, nil
		},
	}

	svc := NewSubscriptionService(db, &config.Config{TrialDurationHours: 3})

	var invalidated []string
	svc.SetInvalidateBySubIDFunc(func(id string) { invalidated = append(invalidated, id) })

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "")
	require.NoError(t, err)
	assert.Same(t, bound, got)
	assert.Equal(t, "trial-sub-1", gotSubID)
	assert.Equal(t, int64(123456), gotTG)
	assert.Equal(t, "tgId_123456", gotUsername, "an empty Telegram username falls back to tgId_<id>")
	assert.Equal(t, []string{"trial-sub-1"}, invalidated)
}

func TestSubscriptionService_BindTrial_DBErrorPropagated(t *testing.T) {
	t.Parallel()

	db := &testutil.DatabaseService{
		BindTrialSubscriptionFunc: func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
			return nil, database.ErrTrialAlreadyActivated
		},
	}

	svc := NewSubscriptionService(db, &config.Config{})
	invalidated := false
	svc.SetInvalidateBySubIDFunc(func(string) { invalidated = true })

	got, err := svc.BindTrial(context.Background(), "trial-sub-1", 123456, "testuser")
	require.ErrorIs(t, err, database.ErrTrialAlreadyActivated)
	assert.Nil(t, got)
	assert.False(t, invalidated, "a failed bind changes nothing to invalidate")
}

// ==================== DeleteByID Tests ====================

func TestSubscriptionService_DeleteByID_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 7, TelegramID: 42, Username: "u", ClientID: "c1", SubscriptionID: "s1"}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			assert.Equal(t, uint(7), id)
			return &database.Subscription{ID: 7, TelegramID: 42, Username: "u", ClientID: "c1", SubscriptionID: "s1"}, nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	deleted, err := svc.DeleteByID(context.Background(), 7)

	assert.NoError(t, err)
	assert.NotNil(t, deleted)
	assert.Equal(t, uint(7), deleted.ID)
}

func TestSubscriptionService_DeleteByID_GetError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}

	svc := NewSubscriptionService(db, cfg)
	deleted, err := svc.DeleteByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.Error(), "get subscription")
}

func TestSubscriptionService_DeleteByID_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	db := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, ClientID: "c", SubscriptionID: "s", TelegramID: 1, Username: "u"}, nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("db fail")
		},
	}

	svc := NewSubscriptionService(db, cfg)
	deleted, err := svc.DeleteByID(context.Background(), 1)

	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.Error(), "db delete")
}

// TestSubscriptionService_DeleteByID_MarkRevokedBeforeDBDelete verifies audit #2
// fix: the subscription is marked revoked (Phase 1) BEFORE the physical DB delete
// (Phase 3). If the DB delete fails, the subscription is already revoked so /sub/{id}
// returns 404 and the user/admin no longer sees it as active. The deprovision
// (Phase 2) is skipped when syncService is nil.
func TestSubscriptionService_DeleteByID_MarkRevokedBeforeDBDelete(t *testing.T) {
	t.Parallel()

	var (
		updatedStatus      string
		updateBeforeDelete bool
	)

	deleteCalled := false

	cfg := &config.Config{}
	db := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, ClientID: "c", SubscriptionID: "s", TelegramID: 1, Username: "u", Status: "active"}, nil
		},
		UpdateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription) error {
			updatedStatus = sub.Status
			// Record that Update happened before Delete by checking the flag.
			if !deleteCalled {
				updateBeforeDelete = true
			}

			return nil
		},
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			deleteCalled = true
			return nil, errors.New("db delete failed")
		},
	}

	svc := NewSubscriptionService(db, cfg)
	_, err := svc.DeleteByID(context.Background(), 1)

	// DB delete failed → error returned to caller.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db delete")

	// Phase 1 happened: subscription was marked revoked before the delete attempt.
	assert.True(t, updateBeforeDelete, "UpdateSubscription (mark revoked) must happen before DeleteSubscriptionByID")
	assert.Equal(t, "revoked", updatedStatus, "subscription must be marked revoked before deprovision/delete")
}

// ==================== InvalidateSubscription Tests ====================

func TestSubscriptionService_InvalidateSubscription_CallsCallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}

	svc := NewSubscriptionService(&testutil.DatabaseService{}, cfg)

	var captured int64

	svc.SetInvalidateFunc(func(telegramID int64) {
		captured = telegramID
	})

	svc.InvalidateSubscription(context.Background(), 123)
	assert.Equal(t, int64(123), captured)
}

func TestSubscriptionService_InvalidateSubscription_NoCallback(t *testing.T) {
	t.Parallel()

	svc := NewSubscriptionService(&testutil.DatabaseService{}, &config.Config{})

	assert.NotPanics(t, func() {
		svc.InvalidateSubscription(context.Background(), 1)
	})
}

// ==================== CleanupExpiredTrials Tests ====================

func TestSubscriptionService_CleanupExpiredTrials_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	expired := []database.Subscription{
		{SubscriptionID: "exp1", ClientID: "c1", TelegramID: 0},
		{SubscriptionID: "exp2", ClientID: "c2", TelegramID: 0},
	}
	db := &testutil.DatabaseService{
		CleanupExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			assert.Equal(t, 24, hours)
			return expired, nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, int64(2), n)
}

func TestSubscriptionService_CleanupExpiredTrials_DBError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	db := &testutil.DatabaseService{
		CleanupExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			return nil, errors.New("db fail")
		},
	}

	svc := NewSubscriptionService(db, cfg)
	n, err := svc.CleanupExpiredTrials(context.Background())

	assert.Error(t, err)
	assert.Equal(t, int64(0), n)
}

// TestSubscriptionService_CleanupExpiredTrials_DeleteFailureIsSurfaced: a
// failed delete leaves the claimed trial row for the next run and is returned
// as an aggregate error; other claimed trials are still deleted.
func TestSubscriptionService_CleanupExpiredTrials_DeleteFailureIsSurfaced(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrialDurationHours: 24}
	expired := []database.Subscription{
		{ID: 1, ClientID: "c1", SubscriptionID: "exp1"},
		{ID: 2, ClientID: "c2", SubscriptionID: "exp2"},
	}
	var deleted []uint
	db := &testutil.DatabaseService{
		ClaimExpiredTrialsFunc: func(ctx context.Context, hours int) ([]database.Subscription, error) {
			return expired, nil
		},
		DeleteClaimedTrialFunc: func(ctx context.Context, id uint) error {
			if id == 1 {
				return errors.New("database is locked")
			}
			deleted = append(deleted, id)
			return nil
		},
	}

	svc := NewSubscriptionService(db, cfg)
	var invalidated []string
	svc.SetInvalidateBySubIDFunc(func(id string) { invalidated = append(invalidated, id) })
	n, err := svc.CleanupExpiredTrials(context.Background())

	require.Error(t, err, "delete failure must be surfaced")
	assert.Contains(t, err.Error(), "delete expired trial exp1")
	assert.Equal(t, int64(1), n)
	assert.Equal(t, []uint{2}, deleted)
	assert.Equal(t, []string{"exp2"}, invalidated, "only the deleted trial's cache is dropped")
}

// ==================== SubscriptionUsername Tests ====================// ==================== XUIEmail Tests ====================

func TestSubscriptionUsername_RealUsername(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "alice", SubscriptionUsername("alice", 42))
}

func TestSubscriptionUsername_FallbackToTgID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		chatID   int64
		want     string
	}{
		{"empty username", "", 100, "tgId_100"},
		{"special char", "user@name", 300, "tgId_300"},
		{"cyrillic", "юзер", 400, "tgId_400"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SubscriptionUsername(tt.username, tt.chatID))
		})
	}
}

// TestSubscriptionService_Create_ReanimatesRevoked verifies the bug fix: when a
// prior delete left the subscription in "revoked" state (partial delete failure),
// Create must reanimate that single row instead of inserting a duplicate — which
// would otherwise hit the telegram_id UNIQUE constraint and permanently block the
// user from re-subscribing.
func TestSubscriptionService_Create_ReanimatesRevoked(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	// Simulate a partially-failed delete: a row left in "revoked".
	revoked := &database.Subscription{
		TelegramID:     777777,
		Username:       "revoked_user",
		ClientID:       "client-revoked",
		SubscriptionID: "sub-revoked",
		PlanID:         1,
		Status:         "revoked",
	}
	require.NoError(t, db.CreateSubscription(ctx, revoked, ""))

	cfg := &config.Config{}
	svc := NewSubscriptionService(db, cfg)

	result, err := svc.Create(ctx, 777777, "revoked_user", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Exactly one row for this telegram_id, now active — no duplicate.
	all, err := db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	var matching []database.Subscription

	for _, s := range all {
		if s.TelegramID == 777777 {
			matching = append(matching, s)
		}
	}

	require.Len(t, matching, 1, "must not create a second row")
	assert.Equal(t, "active", matching[0].Status)

	freePlan, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	assert.Equal(t, freePlan.ID, matching[0].PlanID)
	assert.Nil(t, matching[0].ExpiresAt)
	assert.Equal(t, revoked.ID, matching[0].ID, "same row reanimated, not a new one")
	assert.Equal(t, revoked.Token, matching[0].Token, "the subscription link survives reanimation")
}

func TestSubscriptionService_AdminSetPlan_ChangesPlanAndInvalidatesCache(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	// The free plan is seeded by migrations; the premium plan is created here.
	freePlan, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	premiumPlan := &database.Plan{Name: "premium-admin-setplan"}
	require.NoError(t, db.GetDB().WithContext(ctx).Create(premiumPlan).Error)

	sub := &database.Subscription{
		TelegramID:     999802,
		Username:       "adminsetplan",
		ClientID:       "c-adminsetplan",
		SubscriptionID: "s-adminsetplan",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         freePlan.ID,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(5 * 24 * time.Hour)),
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	var invalidatedTGIDs []int64
	var invalidatedSubIDs []string

	svc := NewSubscriptionService(db, &config.Config{})
	svc.SetInvalidateFunc(func(id int64) { invalidatedTGIDs = append(invalidatedTGIDs, id) })
	svc.SetInvalidateBySubIDFunc(func(id string) { invalidatedSubIDs = append(invalidatedSubIDs, id) })

	updated, err := svc.AdminSetPlan(ctx, sub.ID, premiumPlan.ID, 30)
	require.NoError(t, err)

	assert.Equal(t, premiumPlan.ID, updated.PlanID)
	assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
	require.NotNil(t, updated.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(30*24*time.Hour), *updated.ExpiresAt, time.Minute)

	// Cache invalidated by both keys.
	assert.Contains(t, invalidatedTGIDs, sub.TelegramID)
	assert.Contains(t, invalidatedSubIDs, sub.SubscriptionID)

	stored, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, premiumPlan.ID, stored.PlanID, "the new plan (and its builder) serves the next /sub request")
	assert.Equal(t, sub.Token, stored.Token)
}
func TestSubscriptionService_AdminSetPlan_ExpirySemantics(t *testing.T) {
	t.Parallel()

	nearFuture := time.Now().Add(10 * 24 * time.Hour).Truncate(time.Second)
	past := time.Now().Add(-24 * time.Hour)

	tests := []struct {
		name      string
		planName  string
		days      int
		existing  *time.Time
		wantNil   bool
		wantDelta time.Duration // 0 means "preserve existing"
	}{
		{name: "explicit days", planName: "premium", days: 30, existing: testutil.PtrTime(nearFuture), wantDelta: 30 * 24 * time.Hour},
		{name: "future expiry preserved", planName: "premium", days: 0, existing: testutil.PtrTime(nearFuture), wantDelta: 0},
		{name: "nil expiry gets default", planName: "premium", days: 0, existing: nil, wantDelta: 30 * 24 * time.Hour},
		{name: "past expiry gets default", planName: "premium", days: 0, existing: testutil.PtrTime(past), wantDelta: 30 * 24 * time.Hour},
		{name: "free plan clears expiry", planName: database.FreePlanName, days: 30, existing: testutil.PtrTime(nearFuture), wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()

			sub := &database.Subscription{
				ID:             42,
				TelegramID:     999803,
				Username:       "expiry-semantics",
				ClientID:       "c-expiry",
				SubscriptionID: "s-expiry",
				Status:         string(database.SubscriptionStatusActive),
				PlanID:         1,
				ExpiresAt:      tt.existing,
			}

			var captured *database.Subscription

			db := &testutil.DatabaseService{
				GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
					return sub, nil
				},
				GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
					return &database.Plan{ID: 2, Name: tt.planName}, nil
				},
				UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
					captured = updated
					return nil
				},
			}

			svc := NewSubscriptionService(db, &config.Config{})

			updated, err := svc.AdminSetPlan(context.Background(), sub.ID, 2, tt.days)
			require.NoError(t, err)
			require.NotNil(t, captured, "UpdateSubscription must be called")

			assert.Equal(t, uint(2), updated.PlanID)
			assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
			assert.Equal(t, 0, updated.RemindersSent, "reminder bitmask must reset for the new expiry")

			if tt.wantNil {
				assert.Nil(t, captured.ExpiresAt, "free plan must clear expiry")
			} else if tt.wantDelta == 0 {
				require.NotNil(t, captured.ExpiresAt)
				assert.Equal(t, tt.existing, captured.ExpiresAt, "future expiry must be preserved")
			} else {
				require.NotNil(t, captured.ExpiresAt)
				assert.WithinDuration(t, now.Add(tt.wantDelta), *captured.ExpiresAt, time.Minute)
			}
		})
	}
}

func TestSubscriptionService_AdminSetPlan_Errors(t *testing.T) {
	t.Parallel()

	sub := &database.Subscription{
		ID:             42,
		TelegramID:     999804,
		Username:       "adminsetplan-errors",
		ClientID:       "c-errors",
		SubscriptionID: "s-errors",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
	}

	t.Run("subscription not found", func(t *testing.T) {
		db := &testutil.DatabaseService{
			GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
				return nil, database.ErrSubscriptionNotFound
			},
		}
		svc := NewSubscriptionService(db, &config.Config{})

		_, err := svc.AdminSetPlan(context.Background(), 42, 2, 30)
		require.Error(t, err)
		assert.ErrorIs(t, err, database.ErrSubscriptionNotFound)
		assert.Contains(t, err.Error(), "load subscription")
	})

	t.Run("plan not found", func(t *testing.T) {
		db := &testutil.DatabaseService{
			GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
				return sub, nil
			},
			GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
				return nil, database.ErrPlanNotFound
			},
		}
		svc := NewSubscriptionService(db, &config.Config{})

		_, err := svc.AdminSetPlan(context.Background(), 42, 2, 30)
		require.Error(t, err)
		assert.ErrorIs(t, err, database.ErrPlanNotFound)
		assert.Contains(t, err.Error(), "load plan")
	})

	t.Run("update failure surfaces to caller", func(t *testing.T) {
		updateErr := errors.New("database unavailable")

		db := &testutil.DatabaseService{
			GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
				return sub, nil
			},
			GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
				return &database.Plan{ID: 2, Name: "premium-errors"}, nil
			},
			UpdateSubscriptionFunc: func(context.Context, *database.Subscription) error {
				return updateErr
			},
		}

		svc := NewSubscriptionService(db, &config.Config{})

		_, err := svc.AdminSetPlan(context.Background(), 42, 2, 30)
		require.Error(t, err)
		assert.ErrorIs(t, err, updateErr, "a persistence failure must surface, not be swallowed")
	})
}

func TestSubscriptionService_AdminSetPlan_FreePlanClearsPaidState(t *testing.T) {
	t.Parallel()

	productID := uint(7)
	currency := "RUB"
	started := time.Now().Add(-48 * time.Hour)
	expiry := time.Now().Add(30 * 24 * time.Hour)

	sub := &database.Subscription{
		ID:             42,
		TelegramID:     999805,
		Username:       "adminsetplan-free",
		ClientID:       "c-free",
		SubscriptionID: "s-free",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
		ExpiresAt:      &expiry,
		ProductID:      &productID,
		StartedAt:      &started,
		PricePaidCents: 2300,
		Currency:       &currency,
	}

	var captured *database.Subscription

	db := &testutil.DatabaseService{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: database.FreePlanName}, nil
		},
		UpdateSubscriptionFunc: func(_ context.Context, updated *database.Subscription) error {
			captured = updated
			return nil
		},
	}

	svc := NewSubscriptionService(db, &config.Config{})

	updated, err := svc.AdminSetPlan(context.Background(), sub.ID, 2, 30)
	require.NoError(t, err)
	require.NotNil(t, captured, "UpdateSubscription must be called")

	assert.Equal(t, uint(2), updated.PlanID)
	assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
	assert.Nil(t, captured.ExpiresAt, "free plan must clear expiry")
	assert.Nil(t, captured.ProductID, "free plan must clear product reference")
	assert.Nil(t, captured.StartedAt, "free plan must clear started_at")
	assert.Zero(t, captured.PricePaidCents, "free plan must clear paid amount")
	assert.Nil(t, captured.Currency, "free plan must clear currency")
}

func TestSubscriptionService_AdminSetPlan_DaysTooLarge(t *testing.T) {
	t.Parallel()

	sub := &database.Subscription{
		ID:             42,
		TelegramID:     999806,
		Username:       "adminsetplan-days",
		ClientID:       "c-days",
		SubscriptionID: "s-days",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
	}

	updateCalled := false

	db := &testutil.DatabaseService{
		GetByIDFunc: func(context.Context, uint) (*database.Subscription, error) {
			return sub, nil
		},
		GetPlanByIDFunc: func(context.Context, uint) (*database.Plan, error) {
			return &database.Plan{ID: 2, Name: "premium-days"}, nil
		},
		UpdateSubscriptionFunc: func(context.Context, *database.Subscription) error {
			updateCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, &config.Config{})

	_, err := svc.AdminSetPlan(context.Background(), sub.ID, 2, AdminSetPlanMaxDays+1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.False(t, updateCalled, "oversized duration must fail before any DB write")
}
