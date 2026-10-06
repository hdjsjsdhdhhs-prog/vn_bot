package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWarmupSender records sends; script returns an error per chat (nil:
// success) and is consumed one entry per send.
type fakeWarmupSender struct {
	mu     sync.Mutex
	sent   []fakeWarmupSend
	script map[int64][]error
}

type fakeWarmupSend struct {
	ChatID int64
	Msg    service.WarmupMessage
}

func (f *fakeWarmupSender) SendWarmup(_ context.Context, chatID int64, msg *service.WarmupMessage) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, fakeWarmupSend{ChatID: chatID, Msg: *msg})
	if errs := f.script[chatID]; len(errs) > 0 {
		f.script[chatID] = errs[1:]
		if errs[0] != nil {
			return 0, errs[0]
		}
	}
	return 1000 + len(f.sent), nil
}

func (f *fakeWarmupSender) sends() []fakeWarmupSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeWarmupSend(nil), f.sent...)
}

func (f *fakeWarmupSender) fail(chatID int64, errs ...error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.script == nil {
		f.script = map[int64][]error{}
	}
	f.script[chatID] = append(f.script[chatID], errs...)
}

type warmupWorkerFixture struct {
	ctx    context.Context
	db     *database.Service
	svc    *service.WarmupService
	sender *fakeWarmupSender
	worker *WarmupWorker
	now    time.Time
}

// newWarmupWorkerFixture switches the warm-up on globally (the migration
// leaves it off) and keeps only the given scenarios enabled (switched on a
// day ago, so their recent events are due).
func newWarmupWorkerFixture(t *testing.T, keys ...string) *warmupWorkerFixture {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "warmup-worker.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	gdb := db.GetDB()
	require.NoError(t, gdb.Exec(`UPDATE warmup_settings SET enabled = 1 WHERE id = 1`).Error)
	require.NoError(t, gdb.Exec(`UPDATE warmup_scenarios SET enabled = 0, enabled_at = NULL`).Error)
	for _, key := range keys {
		require.NoError(t, gdb.Model(&database.WarmupScenario{}).Where("key = ?", key).
			Updates(map[string]any{"enabled": true, "enabled_at": now.Add(-24 * time.Hour)}).Error)
	}
	svc := service.NewWarmupService(db, database.TrialDefaults{DurationHours: 72, RateLimitPerHour: 3})
	sender := &fakeWarmupSender{}
	f := &warmupWorkerFixture{ctx: context.Background(), db: db, svc: svc, sender: sender, now: now}
	f.worker = NewWarmupWorker(svc, sender)
	f.worker.now = func() time.Time { return f.now }
	return f
}

// started records a live /start 13 hours ago: "trial_not_activated" (12h)
// is due an hour ago.
func (f *warmupWorkerFixture) started(t *testing.T, telegramID int64, username string) {
	t.Helper()
	require.NoError(t, f.db.TouchWarmupContact(f.ctx, telegramID, username, "ru", f.now.Add(-13*time.Hour)))
}

func (f *warmupWorkerFixture) tick(t *testing.T) WarmupTickStats {
	t.Helper()
	stats, ran := f.worker.process(f.ctx)
	require.True(t, ran)
	return stats
}

func (f *warmupWorkerFixture) deliveries(t *testing.T, telegramID int64) []database.WarmupDeliveryRow {
	t.Helper()
	rows, _, err := f.db.ListWarmupDeliveries(f.ctx, database.WarmupDeliveryFilter{TelegramID: telegramID})
	require.NoError(t, err)
	return rows
}

func (f *warmupWorkerFixture) scenario(t *testing.T, key string) *database.WarmupScenario {
	t.Helper()
	var sc database.WarmupScenario
	require.NoError(t, f.db.GetDB().Where("key = ?", key).First(&sc).Error)
	return &sc
}

func (f *warmupWorkerFixture) updateScenario(t *testing.T, key string, cols map[string]any) {
	t.Helper()
	require.NoError(t, f.db.GetDB().Model(&database.WarmupScenario{}).Where("key = ?", key).Updates(cols).Error)
}

func (f *warmupWorkerFixture) setSettings(t *testing.T, cols map[string]any) {
	t.Helper()
	require.NoError(t, f.db.GetDB().Model(&database.WarmupSettings{}).Where("id = 1").Updates(cols).Error)
}

func transient() error {
	return &service.WarmupSendError{Kind: service.WarmupSendTransient, Err: errors.New("dial tcp: i/o timeout")}
}

func TestWarmupWorker_SendsDueScenarioOnce(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.started(t, 7001, "alice")

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Due)
	assert.Equal(t, 1, stats.Claimed)
	assert.Equal(t, 1, stats.Sent)

	sends := f.sender.sends()
	require.Len(t, sends, 1)
	assert.Equal(t, int64(7001), sends[0].ChatID)
	assert.Contains(t, sends[0].Msg.HTML, "Это 3 дня без оплаты", "variables are rendered")
	assert.Empty(t, sends[0].Msg.Buttons, "the trial button has no URL yet: not sent")
	assert.Equal(t, 1, sends[0].Msg.HiddenButtons)

	rows := f.deliveries(t, 7001)
	require.Len(t, rows, 1)
	assert.Equal(t, database.WarmupDeliverySent, rows[0].Status)
	require.NotNil(t, rows[0].SentAt)
	require.NotNil(t, rows[0].MessageID)
	assert.Equal(t, 1001, *rows[0].MessageID)

	// Idempotency: the next tick sees the sent delivery and does nothing.
	f.now = f.now.Add(10 * time.Minute)
	stats = f.tick(t)
	assert.Equal(t, 1, stats.Handled)
	assert.Zero(t, stats.Claimed)
	assert.Len(t, f.sender.sends(), 1)
}

func TestWarmupWorker_HistoricalUsersAndDisabledSwitch(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	// A backfilled (historical) contact is never a "bot started" event.
	require.NoError(t, f.db.GetDB().Create(&database.WarmupContact{TelegramID: 7101, Source: database.WarmupContactSourceBackfill,
		FirstSeenAt: f.now.Add(-13 * time.Hour), LastSeenAt: f.now.Add(-13 * time.Hour)}).Error)
	f.started(t, 7102, "new")

	f.setSettings(t, map[string]any{"enabled": false})
	stats := f.tick(t)
	assert.Zero(t, stats.Due, "the global switch stops everything")
	assert.Empty(t, f.sender.sends())

	f.setSettings(t, map[string]any{"enabled": true})
	f.tick(t)
	sends := f.sender.sends()
	require.Len(t, sends, 1)
	assert.Equal(t, int64(7102), sends[0].ChatID)
}

// The migration defaults as they are: globally OFF with 11 scenarios on.
// Nothing is sent until the admin switches the warm-up on.
func TestWarmupWorker_MigrationDefaultsGloballyOff(t *testing.T) {
	t.Parallel()
	db, err := database.NewService(filepath.Join(t.TempDir(), "warmup-defaults.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	svc := service.NewWarmupService(db, database.TrialDefaults{DurationHours: 72, RateLimitPerHour: 3})

	settings, err := svc.Settings(ctx)
	require.NoError(t, err)
	assert.False(t, settings.Enabled, "globally OFF after the migration")
	scenarios, err := svc.EnabledScenarios(ctx)
	require.NoError(t, err)
	assert.Len(t, scenarios, 11, "the scenario switches stay as seeded")

	// A live /start after the migration: "trial_not_activated" (12h) is due.
	migratedAt := time.Now().UTC()
	require.NoError(t, db.TouchWarmupContact(ctx, 7051, "fresh", "ru", migratedAt.Add(time.Minute)))
	sender := &fakeWarmupSender{}
	worker := NewWarmupWorker(svc, sender)
	worker.now = func() time.Time { return migratedAt.Add(12*time.Hour + 30*time.Minute) }

	stats, ran := worker.process(ctx)
	require.True(t, ran)
	assert.Zero(t, stats.Due)
	assert.Zero(t, stats.Claimed)
	assert.Empty(t, sender.sends(), "global OFF: nothing is sent")
	rows, _, err := db.ListWarmupDeliveries(ctx, database.WarmupDeliveryFilter{})
	require.NoError(t, err)
	assert.Empty(t, rows, "global OFF: nothing is journaled")

	require.NoError(t, db.GetDB().Model(&database.WarmupSettings{}).Where("id = 1").Update("enabled", true).Error)
	stats, ran = worker.process(ctx)
	require.True(t, ran)
	assert.Equal(t, 1, stats.Sent)
	sends := sender.sends()
	require.Len(t, sends, 1, "global ON: the due event is sent")
	assert.Equal(t, int64(7051), sends[0].ChatID)
}

// The first manual switch-on (OFF -> ON through the admin service) must not
// send the events that became due while the warm-up was off: it moves
// enabled_at of the enabled scenarios to the switch-on moment.
func TestWarmupWorker_SwitchOnDoesNotSendBacklog(t *testing.T) {
	t.Parallel()
	db, err := database.NewService(filepath.Join(t.TempDir(), "warmup-switch-on.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	svc := service.NewWarmupService(db, database.TrialDefaults{DurationHours: 72, RateLimitPerHour: 3})
	sender := &fakeWarmupSender{}
	worker := NewWarmupWorker(svc, sender)

	// The migration was applied a day ago; the warm-up stayed globally OFF.
	start := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.GetDB().Model(&database.WarmupScenario{}).Where("enabled = ?", true).
		Update("enabled_at", start.Add(-24*time.Hour)).Error)
	settings, err := svc.Settings(ctx)
	require.NoError(t, err)
	require.False(t, settings.Enabled)

	// 1. An event ready to send: "trial_not_activated" (12h) of a /start 13
	// hours ago is due an hour ago — inside the catch-up window and after the
	// scenario's enabled_at.
	require.NoError(t, db.TouchWarmupContact(ctx, 7061, "old", "ru", start.Add(-13*time.Hour)))
	var sc database.WarmupScenario
	require.NoError(t, db.GetDB().Where("key = ?", "trial_not_activated").First(&sc).Error)
	require.True(t, sc.Enabled)
	require.NotNil(t, sc.EnabledAt)
	due := start.Add(-time.Hour)
	require.True(t, due.After(*sc.EnabledAt), "due after enabled_at")
	require.True(t, due.After(start.Add(-time.Duration(settings.CatchupHours)*time.Hour)), "due inside the catch-up window")

	// 2. Globally OFF: the worker sends nothing.
	worker.now = func() time.Time { return start }
	stats, ran := worker.process(ctx)
	require.True(t, ran)
	assert.Zero(t, stats.Due)
	assert.Empty(t, sender.sends(), "global OFF: nothing is sent")

	// 3. OFF -> ON moves enabled_at of the enabled scenarios to now.
	switchOn := time.Now().UTC().Truncate(time.Second)
	res, err := svc.UpdateSettings(ctx, "admin", "switch-on", database.WarmupSettingsInput{
		Version: settings.Version, Enabled: true, MaxPerDay: settings.MaxPerDay,
		CooldownHours: settings.CooldownHours, CatchupHours: settings.CatchupHours,
	})
	require.NoError(t, err)
	onAt := res.Audit.CreatedAt
	scenarios, err := svc.EnabledScenarios(ctx)
	require.NoError(t, err)
	require.Len(t, scenarios, 11, "the scenario switches stay as seeded")
	for _, s := range scenarios {
		require.NotNil(t, s.EnabledAt, s.Key)
		assert.False(t, s.EnabledAt.Before(switchOn), "%s: enabled_at moved to the switch-on", s.Key)
		assert.WithinDuration(t, onAt, *s.EnabledAt, time.Second, s.Key)
	}

	// 4. The old event (due before the switch-on) is not sent, although it is
	// still inside the catch-up window.
	worker.now = func() time.Time { return onAt.Add(time.Minute) }
	_, ran = worker.process(ctx)
	require.True(t, ran)
	assert.Empty(t, sender.sends(), "the backlog is not sent on the switch-on")

	// 5. A new event after the switch-on is sent.
	require.NoError(t, db.TouchWarmupContact(ctx, 7062, "fresh", "ru", onAt.Add(time.Minute)))
	worker.now = func() time.Time { return onAt.Add(12*time.Hour + 30*time.Minute) }
	stats, ran = worker.process(ctx)
	require.True(t, ran)
	assert.Equal(t, 1, stats.Sent)
	sends := sender.sends()
	require.Len(t, sends, 1, "only the new event is sent")
	assert.Equal(t, int64(7062), sends[0].ChatID)

	rows, _, err := db.ListWarmupDeliveries(ctx, database.WarmupDeliveryFilter{TelegramID: 7061})
	require.NoError(t, err)
	for _, row := range rows {
		assert.NotEqual(t, database.WarmupDeliverySent, row.Status, "the old event is never sent")
	}
}

func TestWarmupWorker_HTMLEscapingOfValues(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.updateScenario(t, "trial_not_activated", map[string]any{"message_html": "<b>Привет, {{username}}</b> ({{telegram_id}})"})
	f.started(t, 7201, "<i>evil</i>&co")

	f.tick(t)
	sends := f.sender.sends()
	require.Len(t, sends, 1)
	assert.Equal(t, "<b>Привет, @&lt;i&gt;evil&lt;/i&gt;&amp;co</b> (7201)", sends[0].Msg.HTML)
}

func TestWarmupWorker_RetryAfterTransientErrorWithBackoff(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.started(t, 7301, "retry")
	f.sender.fail(7301, transient())

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Retry)
	rows := f.deliveries(t, 7301)
	assert.Equal(t, database.WarmupDeliveryRetry, rows[0].Status)
	assert.Equal(t, 1, rows[0].Attempts)
	assert.Contains(t, rows[0].Error, "i/o timeout")

	f.now = f.now.Add(5 * time.Minute)
	stats = f.tick(t)
	assert.Zero(t, stats.Claimed, "still backing off (10 minutes)")
	assert.Len(t, f.sender.sends(), 1)

	f.now = f.now.Add(6 * time.Minute)
	stats = f.tick(t)
	assert.Equal(t, 1, stats.Sent)
	rows = f.deliveries(t, 7301)
	assert.Equal(t, database.WarmupDeliverySent, rows[0].Status)
	assert.Equal(t, 2, rows[0].Attempts)
	assert.Empty(t, rows[0].Error)
}

func TestWarmupWorker_RetriesExhaustedFailWithCouponRevoked(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.updateScenario(t, "trial_not_activated", map[string]any{"discount_percent": 10, "message_html": "Код {{discount_code}}"})
	f.started(t, 7401, "tired")
	f.sender.fail(7401, transient(), transient(), transient())

	f.tick(t)
	f.now = f.now.Add(11 * time.Minute)
	f.tick(t)
	f.now = f.now.Add(21 * time.Minute)
	stats := f.tick(t)
	assert.Equal(t, 1, stats.Failed)
	assert.Len(t, f.sender.sends(), 3)
	codes := map[string]bool{}
	for _, s := range f.sender.sends() {
		codes[s.Msg.HTML] = true
	}
	assert.Len(t, codes, 1, "every retry carries the same personal coupon")

	rows := f.deliveries(t, 7401)
	assert.Equal(t, database.WarmupDeliveryFailed, rows[0].Status)
	assert.Equal(t, 3, rows[0].Attempts)
	coupons, _, err := f.db.ListWarmupCoupons(f.ctx, database.WarmupCouponFilter{TelegramID: 7401})
	require.NoError(t, err)
	require.Len(t, coupons, 1)
	assert.Equal(t, database.WarmupCouponRevoked, coupons[0].Status)

	f.now = f.now.Add(time.Hour)
	f.tick(t)
	assert.Len(t, f.sender.sends(), 3, "a failed delivery is never sent again")
}

func TestWarmupWorker_FloodPausesTheTick(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.started(t, 7501, "a")
	f.started(t, 7502, "b")
	f.sender.fail(7501, &service.WarmupSendError{Kind: service.WarmupSendFlood, RetryAfter: 5 * time.Second,
		Err: &tgbotapi.Error{Code: 429, Message: "Too Many Requests: retry after 5"}})

	stats := f.tick(t)
	assert.True(t, stats.FloodPaused)
	assert.Equal(t, 1, stats.Retry)
	require.Len(t, f.sender.sends(), 1, "after a 429 nobody else is sent in this tick")
	assert.Equal(t, database.WarmupDeliveryRetry, f.deliveries(t, 7501)[0].Status)
	assert.Empty(t, f.deliveries(t, 7502))

	f.now = f.now.Add(10 * time.Minute)
	stats = f.tick(t)
	assert.Equal(t, 2, stats.Sent, "the next tick sends both")
}

func TestWarmupWorker_BlockedUserIsExcluded(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated", "trial_not_connected_3h")
	f.started(t, 7601, "blocker")
	f.sender.fail(7601, &service.WarmupSendError{Kind: service.WarmupSendBlocked,
		Err: &tgbotapi.Error{Code: 403, Message: "Forbidden: bot was blocked by the user"}})

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Blocked)
	rows := f.deliveries(t, 7601)
	assert.Equal(t, database.WarmupDeliveryBlocked, rows[0].Status)
	contact, err := f.db.GetWarmupContact(f.ctx, 7601)
	require.NoError(t, err)
	require.NotNil(t, contact.BlockedAt)

	// Another scenario becomes due for the same user: skipped.
	f.updateScenario(t, "trial_not_connected_3h", map[string]any{"trigger_type": service.WarmupTriggerBotStarted, "delay_value": 13, "conditions": "{}"})
	f.now = f.now.Add(10 * time.Minute)
	stats = f.tick(t)
	assert.Zero(t, stats.Claimed)
	assert.Len(t, f.sender.sends(), 1)
}

func TestWarmupWorker_UnreachableUserFailsAndIsExcluded(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.started(t, 7701, "gone")
	f.sender.fail(7701, &service.WarmupSendError{Kind: service.WarmupSendUnreachable,
		Err: &tgbotapi.Error{Code: 403, Message: "Forbidden: user is deactivated"}})

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Unreachable)
	rows := f.deliveries(t, 7701)
	assert.Equal(t, database.WarmupDeliveryFailed, rows[0].Status)
	assert.Contains(t, rows[0].Error, "deactivated")
	facts, err := f.db.GetWarmupUserFacts(f.ctx, 7701, f.now)
	require.NoError(t, err)
	assert.True(t, facts.Blocked)
}

func TestWarmupWorker_PermanentErrorFailsAtOnce(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.started(t, 7801, "bad")
	f.sender.fail(7801, &service.WarmupSendError{Kind: service.WarmupSendPermanent,
		Err: &tgbotapi.Error{Code: 400, Message: "Bad Request: can't parse entities"}})

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Failed)
	assert.Equal(t, database.WarmupDeliveryFailed, f.deliveries(t, 7801)[0].Status)
	assert.Equal(t, 1, f.deliveries(t, 7801)[0].Attempts)
}

func TestWarmupWorker_RenderFailureFailsDelivery(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	// Bypasses validation on purpose: plan_name has no value for bot_started.
	f.updateScenario(t, "trial_not_activated", map[string]any{"message_html": "Тариф {{plan_name}}"})
	f.started(t, 7901, "x")

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Failed)
	assert.Empty(t, f.sender.sends(), "an unrenderable message is never sent")
	rows := f.deliveries(t, 7901)
	assert.Equal(t, database.WarmupDeliveryFailed, rows[0].Status)
	assert.Contains(t, rows[0].Error, "render:")
}

// twoStartScenarios makes two scenarios due for the same users.
func twoStartScenarios(t *testing.T, f *warmupWorkerFixture) {
	t.Helper()
	f.updateScenario(t, "trial_not_connected_3h", map[string]any{"trigger_type": service.WarmupTriggerBotStarted, "delay_value": 12, "conditions": "{}"})
	f.updateScenario(t, "trial_not_connected_24h", map[string]any{"trigger_type": service.WarmupTriggerBotStarted, "delay_value": 12, "conditions": "{}"})
}

func TestWarmupWorker_OneMessagePerUserPerTickAndDailyLimit(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated", "trial_not_connected_3h", "trial_not_connected_24h")
	twoStartScenarios(t, f)
	f.setSettings(t, map[string]any{"cooldown_hours": 0, "max_per_day": 2})
	f.started(t, 8001, "busy")

	stats := f.tick(t)
	assert.Equal(t, 3, stats.Due)
	assert.Equal(t, 1, stats.Sent)
	assert.Equal(t, 2, stats.Skipped["same_tick"], "never several messages in a row from one tick")

	f.now = f.now.Add(10 * time.Minute)
	stats = f.tick(t)
	assert.Equal(t, 1, stats.Sent)

	f.now = f.now.Add(10 * time.Minute)
	stats = f.tick(t)
	assert.Zero(t, stats.Sent)
	assert.Equal(t, 1, stats.Skipped[service.WarmupSkipUserDailyLimit], "2 per rolling 24 hours")
	assert.Len(t, f.sender.sends(), 2)
}

func TestWarmupWorker_CooldownBetweenMessages(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated", "trial_not_connected_3h")
	twoStartScenarios(t, f)
	f.setSettings(t, map[string]any{"cooldown_hours": 24, "max_per_day": 5})
	f.started(t, 8101, "cool")

	f.tick(t)
	f.now = f.now.Add(10 * time.Minute)
	stats := f.tick(t)
	assert.Equal(t, 1, stats.Skipped[service.WarmupSkipCooldown])
	assert.Len(t, f.sender.sends(), 1)

	// The scenario's own cooldown overrides the global one.
	f.updateScenario(t, "trial_not_connected_3h", map[string]any{"cooldown_hours": 0})
	f.now = f.now.Add(10 * time.Minute)
	stats = f.tick(t)
	assert.Equal(t, 1, stats.Sent)
}

func TestWarmupWorker_ScenarioDailyCap(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.updateScenario(t, "trial_not_activated", map[string]any{"daily_cap": 2})
	for i := range int64(4) {
		f.started(t, 8201+i, fmt.Sprintf("u%d", i))
	}
	stats := f.tick(t)
	assert.Equal(t, 2, stats.Sent)
	assert.Equal(t, 2, stats.Skipped[service.WarmupSkipScenarioCap])
}

func TestWarmupWorker_IssuesPersonalCoupon(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.updateScenario(t, "trial_not_activated", map[string]any{
		"discount_percent": 15, "coupon_template": "HELLO", "coupon_ttl_hours": 48,
		"message_html": "Скидка {{discount_percent}}%: <code>{{discount_code}}</code>",
	})
	f.started(t, 8301, "coupon")

	f.tick(t)
	coupons, _, err := f.db.ListWarmupCoupons(f.ctx, database.WarmupCouponFilter{TelegramID: 8301})
	require.NoError(t, err)
	require.Len(t, coupons, 1)
	c := coupons[0]
	assert.Regexp(t, `^HELLO-[A-HJ-NP-Z2-9]{8}$`, c.Code)
	assert.Equal(t, 15, c.Percent)
	assert.Equal(t, database.WarmupCouponIssued, c.Status)
	assert.WithinDuration(t, f.now.Add(48*time.Hour), c.ExpiresAt, time.Second)
	assert.Nil(t, c.OrderID, "coupons are not connected to payments")
	assert.Nil(t, c.RedeemedAt)

	sends := f.sender.sends()
	require.Len(t, sends, 1)
	assert.Equal(t, "Скидка 15%: <code>"+c.Code+"</code>", sends[0].Msg.HTML)
	rows := f.deliveries(t, 8301)
	require.NotNil(t, rows[0].CouponCode)
	assert.Equal(t, c.Code, *rows[0].CouponCode)
}

func TestWarmupWorker_HousekeepingRecoversAndExpires(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	sc := f.scenario(t, "trial_not_activated")
	policy := service.WarmupRetryPolicy()
	// A send interrupted 40 minutes ago, and a retry whose event is 7h old.
	stale, err := f.db.ClaimWarmupDelivery(f.ctx, database.WarmupClaim{ScenarioID: sc.ID, ScenarioName: sc.Name, TelegramID: 8401,
		AnchorKey: "start", AnchorAt: f.now.Add(-13 * time.Hour), DueAt: f.now.Add(-time.Hour)}, policy, f.now.Add(-40*time.Minute))
	require.NoError(t, err)
	old, err := f.db.ClaimWarmupDelivery(f.ctx, database.WarmupClaim{ScenarioID: sc.ID, ScenarioName: sc.Name, TelegramID: 8402,
		AnchorKey: "start", AnchorAt: f.now.Add(-19 * time.Hour), DueAt: f.now.Add(-7 * time.Hour)}, policy, f.now.Add(-7*time.Hour))
	require.NoError(t, err)
	require.NoError(t, f.db.ReleaseWarmupDelivery(f.ctx, old.Delivery.ID, "net", false, f.now.Add(-7*time.Hour)))

	stats := f.tick(t)
	assert.EqualValues(t, 1, stats.Recovered)
	assert.EqualValues(t, 1, stats.Expired)
	assert.Equal(t, database.WarmupDeliveryRetry, f.deliveries(t, 8401)[0].Status)
	assert.Equal(t, stale.Delivery.ID, f.deliveries(t, 8401)[0].ID)
	assert.Equal(t, database.WarmupDeliveryFailed, f.deliveries(t, 8402)[0].Status)
}

func TestWarmupWorker_ConcurrentWorkersSendEachEventOnce(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	const users = 25
	for i := range int64(users) {
		f.started(t, 8500+i, fmt.Sprintf("c%d", i))
	}
	// Two workers (e.g. two processes during a deploy switch) on one database.
	other := NewWarmupWorker(f.svc, f.sender)
	other.now = f.worker.now

	var wg sync.WaitGroup
	var errs [2]int
	for i, w := range []*WarmupWorker{f.worker, other} {
		wg.Add(1)
		go func(i int, w *WarmupWorker) {
			defer wg.Done()
			stats, _ := w.process(f.ctx)
			errs[i] = stats.Errors
		}(i, w)
	}
	wg.Wait()
	assert.Zero(t, errs[0]+errs[1], "no SQLite locking errors")

	perChat := map[int64]int{}
	for _, s := range f.sender.sends() {
		perChat[s.ChatID]++
	}
	assert.Len(t, perChat, users)
	for chat, n := range perChat {
		assert.Equal(t, 1, n, "chat %d", chat)
	}
	var sent int64
	require.NoError(t, f.db.GetDB().Model(&database.WarmupDelivery{}).Where("status = ?", database.WarmupDeliverySent).Count(&sent).Error)
	assert.EqualValues(t, users, sent)
}

func TestWarmupWorker_OverlappingTickIsSkipped(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t)
	f.worker.running.Lock()
	_, ran := f.worker.process(f.ctx)
	f.worker.running.Unlock()
	assert.False(t, ran)
}

func TestWarmupWorker_ShutdownDuringSendIsRetried(t *testing.T) {
	t.Parallel()
	f := newWarmupWorkerFixture(t, "trial_not_activated")
	f.started(t, 8601, "late")
	f.sender.fail(8601, context.Canceled)

	stats := f.tick(t)
	assert.Equal(t, 1, stats.Retry)
	rows := f.deliveries(t, 8601)
	assert.Equal(t, database.WarmupDeliveryRetry, rows[0].Status)
	assert.Contains(t, rows[0].Error, "interrupted")
}
