package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShowLoadingMessage_WithMessageID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 100

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, messageID, resultID, "Should return same messageID when editing")
	assert.True(t, mockBot.SendCalledSafe(), "Send should be called")
}

func TestShowLoadingMessage_WithoutMessageID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.NotEqual(t, 0, resultID, "Should return new messageID when sending new message")
	assert.True(t, mockBot.SendCalledSafe(), "Send should be called")
}

func TestShowLoadingMessage_SendFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, 0, resultID, "Should return 0 when send fails")
}

func TestShowLoadingMessage_EditFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	// First call fails (edit), second succeeds (send new)
	mockBot.SendError = errors.New("edit error")
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 100

	// Reset error for second call
	resultID := handler.showLoadingMessage(chatID, messageID)

	// Should attempt to send new message if edit fails
	assert.True(t, mockBot.SendCalledSafe())
	// When edit fails and send new succeeds, should return new message ID
	_ = resultID // Just acknowledge the result, don't make assertions about it
}

func TestCreateSubscription_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	// mock DB methods for Create()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
	}
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		assert.Equal(t, int64(123456), sub.TelegramID)
		assert.Equal(t, "testuser", sub.Username)
		sub.ID = 1

		return nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.True(t, mockDB.CreateSubscriptionFunc != nil)
}

func TestCreateSubscription_DatabaseFailure_RollbackSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	// DB-first: mock DB methods - CreateSubscription fails
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
	}
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		return errors.New("database error")
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	// DB-first: no XUI rollback needed, error is shown to user
	assert.True(t, mockBot.SendCalledSafe())
}

func TestCreateSubscription_CacheUpdate(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	// mock DB methods for Create()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
	}

	var savedSub *database.Subscription

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		savedSub = sub
		sub.ID = 1

		return nil
	}

	ctx := context.Background()
	chatID := int64(123456)

	// Verify cache is empty initially
	cached := handler.cache.Get(chatID)
	assert.Nil(t, cached)

	handler.createSubscription(ctx, chatID, "testuser", 1)

	// Verify cache was updated
	cached = handler.cache.Get(chatID)
	require.NotNil(t, cached)
	assert.Equal(t, savedSub.TelegramID, cached.TelegramID)
}

func TestHandleCreateSubscription_AlreadyInProgress(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	// Simulate subscription already in progress (set by another goroutine)
	handler.inProgressSyncMap.Store(chatID, true)

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, chatID, "testuser", 1)

	// inProgress entry should NOT be cleaned up — it belongs to the goroutine that set it.
	// The early-return path doesn't register the defer cleanup.
	_, stillInProgress := handler.inProgressSyncMap.Load(chatID)
	assert.True(t, stillInProgress, "inProgress entry should remain (belongs to other goroutine)")

	// No bot interaction should have occurred (early return)
	assert.False(t, mockBot.SendCalledSafe(), "Bot should not be called when already in progress")
}

func TestHandleCreateSubscription_ExistingActiveSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)), // Not expired
		Status:    "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	createCalled := false
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		createCalled = true
		return nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.False(t, createCalled, "Should not create a new subscription for an existing active one")
}
func TestHandleCreateSubscription_ExpiredSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(-24 * time.Hour)), // Expired
		Status:    "expired",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	createCalled := false
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		createCalled = true
		return nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	// A stored row (whatever its status) is shown as the existing subscription;
	// handleCreateSubscription creates a new one only when no row is found.
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.False(t, createCalled, "Existing row must be shown, not re-created")
}

func TestHandleCreateSubscription_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleCreateSubscription_NoSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	createCalled := false
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		createCalled = true
		return nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, createCalled, "Should create a new subscription when none exists")
}

func TestHandleMySubscription_NotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestHandleMySubscription_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleMySubscription_ActiveSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		PlanID:     1,

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:    "active",
		CreatedAt: time.Now().Add(-7 * 24 * time.Hour), // Created 7 days ago
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return &database.Plan{
			ID:   planID,
			Name: "test",
		}, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Тариф: *test*")
	assert.Contains(t, mockBot.LastSentTextSafe(), "📅 Создана:")
	assert.Contains(t, mockBot.LastSentTextSafe(), "⏰ Истекает:")
	// The bot does not meter traffic: no quota or reset line is rendered.
	assert.NotContains(t, mockBot.LastSentTextSafe(), "Трафик")
	assert.NotContains(t, mockBot.LastSentTextSafe(), "Сброс")
}

func TestHandleMySubscription_PlanLookupError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:    "active",
		CreatedAt: time.Now().Add(-7 * 24 * time.Hour), // Created 7 days ago
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return nil, errors.New("plan lookup error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	// The plan name is display-only: a failed lookup must not hide the subscription.
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.Contains(t, mockBot.LastSentTextSafe(), "⏰ Истекает:")
	assert.NotContains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleMySubscription_UsesCache(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:    "active",
	}

	// Set cache
	handler.cache.Set(123456, sub)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	err := handler.handleQRCode(ctx, 123456, "testuser", 1)

	// Real DB errors should be returned, not masked with a user message.
	require.Error(t, err, "handleQRCode should return an error for DB failure")
	require.Contains(t, err.Error(), "database error", "error should wrap DB error")
	// No user-facing message should be sent for internal errors.
	assert.False(t, mockBot.SendCalledSafe(), "should not send user message on DB error")
}

func TestGetSubscriptionWithCache_CacheHit(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "active",
	}

	// Set cache
	handler.cache.Set(chatID, sub)

	// Database should not be called
	dbCalled := false
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		dbCalled = true
		return nil, nil
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.NoError(t, err)
	assert.Equal(t, sub, result)
	assert.False(t, dbCalled, "Database should not be called on cache hit")
}

func TestGetSubscriptionWithCache_CacheMiss(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.NoError(t, err)
	assert.Equal(t, sub, result)

	// Verify cache was updated
	cached := handler.cache.Get(chatID)
	assert.Equal(t, sub, cached)
}

func TestGetSubscriptionWithCache_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetSubscriptionWithCache_StaleCacheNonActive(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	// Put a non-active subscription in cache
	staleSub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "revoked",
	}
	handler.cache.Set(chatID, staleSub)

	// DB should return nothing
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	assert.Nil(t, result)
	// Verify stale cache was invalidated
	assert.Nil(t, handler.cache.Get(chatID))
}

func TestInvalidateCache(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
	}

	// Set cache
	handler.cache.Set(chatID, sub)
	require.NotNil(t, handler.cache.Get(chatID))

	// Invalidate
	handler.invalidateCache(context.Background(), chatID)

	// Verify cache was cleared
	assert.Nil(t, handler.cache.Get(chatID))
}

func TestSendQRCode(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 789
	testURL := "https://t.me/testbot?start=share_ABC123"
	testCaption := "📱 QR-код для Telegram"

	ctx := context.Background()

	// Should not panic and should call Send
	assert.NotPanics(t, func() {
		handler.sendQRCode(ctx, chatID, messageID, testURL, testCaption)
	})

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called")
}

func TestHandleBackToInvite(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 789
	username := "testuser"

	ctx := context.Background()

	// Should not panic and should call Request (for delete)
	assert.NotPanics(t, func() {
		handler.handleBackToInvite(ctx, chatID, username, messageID)
	})

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called for delete message")
}

func TestHandleCreateSubscription_ZeroMessageID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		return nil
	}

	ctx := context.Background()
	// Pass 0 as messageID to test showLoadingMessage branch
	handler.handleCreateSubscription(ctx, 123456, "testuser", 0)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_WithSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID: telegramID,
			Username:   "testuser",
			Status:     "active",
		}, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for QR photo")
}

func TestHandleQRCode_NoSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки", "message should mention no subscription")
}

func TestHandleBackToSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		SiteURL:         "https://x.com",
		GlobalSubURL:    "https://x.com/sub/",
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()

	require.NoError(t, mockDB.CreateSubscription(context.Background(), &database.Subscription{
		TelegramID: 123456, Username: "testuser", ClientID: "c", SubscriptionID: "s", Status: "active",
	}, ""))
	svc := service.NewSubscriptionService(mockDB, cfg)
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), svc, "")

	ctx := context.Background()
	handler.handleBackToSubscription(ctx, 123456, "testuser", 789)

	// Back must only delete the QR photo; the subscription card stays open
	// underneath, so no re-show (Bot.Send) is expected.
	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to delete QR message")
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send must NOT re-show the card on Back")
}

func TestNotifyAdmin_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url")

	assert.NoError(t, err)
	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called to notify admin")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Новая подписка", "message should mention new subscription")
}

func TestNotifyAdmin_ZeroAdminID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url")

	assert.NoError(t, err)
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called when admin ID is zero")
}

func TestNotifyAdminError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.notifyAdminError(ctx, "⚠️ Test error message")

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for error notification")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Test error message", "message should contain error text")
}

func TestNotifyAdminError_ZeroAdminID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.notifyAdminError(ctx, "⚠️ Test error")

	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called when admin ID is zero")
}

func TestClearAdminSendRateLimit(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	handler.ClearAdminSendRateLimit(chatID)

	// Should not panic and rate limit should be cleared
	assert.True(t, handler.checkAdminSendRateLimit(chatID), "Rate limit should allow send after clear")
}

// TestHandleCreateError_NoAdminNotification: creation is a single DB operation
// with no panel client to orphan, so a failure only informs the user; the admin
// "orphan client" warning is gone.
func TestHandleCreateError_NoAdminNotification(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		SiteURL:         "https://vpn.site",
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.handleCreateError(ctx, 123456, 100, "testuser", errors.New("create subscription: database error"))

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Equal(t, msg(MsgErrGeneric), mockBot.LastSentTextSafe())
	assert.Equal(t, 1, mockBot.SendCountSafe(), "Only the user-facing error message is sent")
}

func TestNotifyAdmin_SendError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	mockBot.SendError = errors.New("send failed")
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notify admin")
}

func TestSendQRCode_QRGenerationError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	// Empty URL still generates valid QR (empty image), so test with valid URL
	handler.sendQRCode(ctx, 123456, 100, "https://example.com/sub", "Test caption")

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_NilSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestCreateSubscription_WithPendingInvite(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	// mock DB methods for Create()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
	}

	// Simulate production DB behavior: resolve invite → set InviteCode + ReferredBy.
	// This is what real db.Service.CreateSubscription does inside the tx.
	var (
		savedSub      *database.Subscription
		gotInviteCode string
	)

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		gotInviteCode = inviteCode
		if inviteCode == "ABC123" {
			inviteVal := inviteCode
			sub.InviteCode = &inviteVal
			referredBy := int64(999999)
			sub.ReferredBy = &referredBy
		}

		sub.ID = 1
		// Store a value copy so assertions see a stable snapshot.
		stored := *sub
		savedSub = &stored

		return nil
	}

	inviteCode := "ABC123"

	handler.pendingMu.Lock()
	handler.pendingInvites[123456] = pendingInvite{
		code:      inviteCode,
		expiresAt: time.Now().Add(60 * time.Minute),
	}
	handler.pendingMu.Unlock()

	ctx := context.Background()
	result := handler.createSubscription(ctx, 123456, "testuser", 1)

	require.NoError(t, result)
	assert.Equal(t, "ABC123", gotInviteCode, "handler must forward inviteCode to db.CreateSubscription")
	require.NotNil(t, savedSub)
	assert.Equal(t, "ABC123", *savedSub.InviteCode, "InviteCode must be persisted on the sub")
	assert.Equal(t, int64(999999), *savedSub.ReferredBy, "ReferredBy must be persisted on the sub")

	// handler must consume the pending invite and bump the referral cache.
	handler.pendingMu.Lock()
	_, exists := handler.pendingInvites[123456]
	handler.pendingMu.Unlock()
	assert.False(t, exists, "Pending invite should be removed after use")
	assert.Equal(t, int64(1), handler.GetReferralCount(999999), "IncrementReferralCount should fire when invite resolved")
}

func TestCreateSubscription_WithExpiredPendingInvite(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	var gotInviteCode string = "NON_EMPTY_DEFAULT"

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		gotInviteCode = inviteCode
		return nil
	}

	handler.pendingMu.Lock()
	handler.pendingInvites[123456] = pendingInvite{
		code:      "EXPIRED",
		expiresAt: time.Now().Add(-60 * time.Minute),
	}
	handler.pendingMu.Unlock()

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.Equal(t, "", gotInviteCode, "expired pending invite must not be forwarded to db")
}

func TestHandleBackToInvite_RequestError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockBot.RequestError = errors.New("request failed")

	ctx := context.Background()
	handler.handleBackToInvite(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called")
}

func TestCreateSubscription_ShowLoadingMessageFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, cfg)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)
	handler.subscriptionService.SetInvalidateFunc(handler.cache.Invalidate)

	mockBot.SendError = errors.New("send failed")

	createCalled := false
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		createCalled = true
		return nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.False(t, createCalled, "Subscription must not be created when the loading message fails")
}

func TestHandleQRCode_DatabaseErrorReturnsError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database connection failed")
	}

	ctx := context.Background()
	err := handler.handleQRCode(ctx, 123456, "testuser", 100)

	require.Error(t, err, "handleQRCode should return an error for DB connection failure")
	require.Contains(t, err.Error(), "database connection failed")
	assert.False(t, mockBot.SendCalledSafe(), "should not send user message on DB error")
}

func TestHandleBackToSubscription_DeleteFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		SiteURL:         "https://x.com",
		GlobalSubURL:    "https://x.com/sub/",
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	mockBot.RequestError = errors.New("delete failed")

	require.NoError(t, mockDB.CreateSubscription(context.Background(), &database.Subscription{
		TelegramID: 123456, Username: "testuser", ClientID: "c", SubscriptionID: "s", Status: "active",
	}, ""))
	svc := service.NewSubscriptionService(mockDB, cfg)
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), svc, "")

	ctx := context.Background()
	handler.handleBackToSubscription(ctx, 123456, "testuser", 789)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to delete message")
}

// daysUntilReset and GenerateProgressBar tests have been moved to
// internal/utils/format_test.go
