package database

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Customer warm-up persistence (migration 050).

var warmupMeta = func(key string) AdminConfigMeta { return AdminConfigMeta{Actor: "admin", RequestKey: key} }

func warmupScenarioByKey(t *testing.T, svc *Service, key string) *WarmupScenario {
	t.Helper()
	var sc WarmupScenario
	require.NoError(t, svc.db.Where("key = ?", key).First(&sc).Error)
	return &sc
}

func warmupFieldsOf(sc *WarmupScenario) WarmupScenarioFields {
	return WarmupScenarioFields{
		Category: sc.Category, Name: sc.Name, Description: sc.Description, TriggerType: sc.TriggerType,
		Timing: sc.Timing, DelayValue: sc.DelayValue, DelayUnit: sc.DelayUnit, Enabled: sc.Enabled,
		MessageHTML: sc.MessageHTML, ImageID: sc.ImageID, Buttons: sc.Buttons, Conditions: sc.Conditions,
		CooldownHours: sc.CooldownHours, DailyCap: sc.DailyCap, DiscountPercent: sc.DiscountPercent,
		CouponTemplate: sc.CouponTemplate, CouponTTLHours: sc.CouponTTLHours, Position: sc.Position,
	}
}

func warmupTableCount(t *testing.T, svc *Service) int {
	t.Helper()
	var n int64
	require.NoError(t, svc.db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'warmup_%'`).Scan(&n).Error)
	return int(n)
}

func TestWarmupMigration050_Seed(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()

	settings, err := svc.GetWarmupSettings(ctx)
	require.NoError(t, err)
	assert.False(t, settings.Enabled, "the warm-up is globally OFF after the migration: the admin switches it on")
	assert.Equal(t, 2, settings.MaxPerDay)
	assert.Equal(t, 24, settings.CooldownHours)
	assert.Equal(t, 6, settings.CatchupHours)
	assert.Equal(t, 1, settings.Version)

	scenarios, err := svc.ListWarmupScenarios(ctx)
	require.NoError(t, err)
	require.Len(t, scenarios, 14, "exactly 14 standard scenarios")

	enabled, disabled := []string{}, []string{}
	for _, sc := range scenarios {
		if sc.Enabled {
			enabled = append(enabled, sc.Key)
			require.NotNil(t, sc.EnabledAt, "%s: an enabled scenario has its switch-on time", sc.Key)
			assert.WithinDuration(t, time.Now(), *sc.EnabledAt, time.Minute, sc.Key)
		} else {
			disabled = append(disabled, sc.Key)
			assert.Nil(t, sc.EnabledAt, sc.Key)
		}
		assert.Equal(t, 1, sc.Version, sc.Key)
		assert.Zero(t, sc.DiscountPercent, sc.Key)
	}
	assert.Len(t, enabled, 11)
	assert.ElementsMatch(t, []string{"sub_expiring_48h", "sub_expiring_24h", "sub_expired"}, disabled,
		"only the subscription scenarios are off: they would duplicate the existing expiry reminders")

	// Admin order: trial tab, subscription tab, winback tab, by position.
	order := make([]string, 0, len(scenarios))
	for _, sc := range scenarios {
		order = append(order, sc.Key)
	}
	assert.Equal(t, []string{
		"trial_not_activated", "trial_not_connected_3h", "trial_not_connected_24h", "trial_ending", "trial_no_purchase", "trial_ended",
		"sub_expiring_48h", "sub_expiring_24h", "sub_expired",
		"winback_3d", "trial_winback_3d", "winback_7d", "winback_14d", "winback_30d",
	}, order)

	notActivated := warmupScenarioByKey(t, svc, "trial_not_activated")
	assert.Equal(t, `{"had_trial":false,"has_subscription":false,"has_active_paid":false}`, notActivated.Conditions)
	assert.Contains(t, notActivated.Buttons, `"kind":"url"`)
}

func TestWarmupMigration050_BackfillAndDown(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	m, err := newMigration(sqlDB, false)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(49))
	assert.Zero(t, warmupTableCount(t, svc))

	old := createTestSubscription(t, svc, 950001, "old_user", "warmup-old")
	require.NoError(t, svc.db.Model(&Subscription{}).Where("id = ?", old.ID).
		Update("created_at", time.Now().Add(-90*24*time.Hour).UTC()).Error)
	_, err = svc.CreateTrialSubscription(ctx, "", "warmup-anon-trial", "warmup-anon-client", time.Now().Add(time.Hour))
	require.NoError(t, err)
	subsBefore := tableRows(t, svc, "subscriptions", "id", "")

	require.NoError(t, m.Migrate(50))
	assert.Equal(t, 6, warmupTableCount(t, svc))

	var contacts []WarmupContact
	require.NoError(t, svc.db.Order("telegram_id").Find(&contacts).Error)
	require.Len(t, contacts, 1, "only linked users are contacts; an anonymous trial is not")
	c := contacts[0]
	assert.Equal(t, int64(950001), c.TelegramID)
	assert.Equal(t, "old_user", c.Username)
	assert.Equal(t, WarmupContactSourceBackfill, c.Source)
	assert.WithinDuration(t, time.Now().Add(-90*24*time.Hour), c.FirstSeenAt, time.Minute)

	// A historical user is never a "bot started" event, whatever the window.
	page, err := svc.WarmupContactsStartedBetween(ctx, time.Now().Add(-365*24*time.Hour), time.Now().Add(time.Hour), 0, 100)
	require.NoError(t, err)
	assert.Empty(t, page.Anchors)

	// Rollback drops only the warm-up tables.
	require.NoError(t, m.Migrate(49))
	assert.Zero(t, warmupTableCount(t, svc))
	assert.Equal(t, subsBefore, tableRows(t, svc, "subscriptions", "id", ""))
	require.NoError(t, m.Migrate(50))
	scenarios, err := svc.ListWarmupScenarios(ctx)
	require.NoError(t, err)
	assert.Len(t, scenarios, 14)
}

func TestWarmupMigration050_Constraints(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	now := time.Now().UTC()

	assert.Error(t, svc.db.Exec(`INSERT INTO warmup_settings (id, updated_at) VALUES (2, ?)`, now).Error, "one settings row")
	assert.Error(t, svc.db.Exec(`UPDATE warmup_settings SET max_per_day = 0`).Error)
	assert.Error(t, svc.db.Exec(`UPDATE warmup_scenarios SET enabled = 1, enabled_at = NULL WHERE key = 'sub_expired'`).Error,
		"an enabled scenario always has enabled_at")
	big := make([]byte, WarmupImageMaxBytes+1)
	assert.Error(t, svc.db.Exec(`INSERT INTO warmup_images (mime, size_bytes, sha256, data, created_at) VALUES ('image/png', ?, 'x', ?, ?)`,
		len(big), big, now).Error, "images are limited to 2 MB")
	assert.Error(t, svc.db.Exec(`INSERT INTO warmup_contacts (telegram_id, source, first_seen_at, last_seen_at) VALUES (5, 'import', ?, ?)`, now, now).Error)
}

func TestWarmupContacts_TouchBlockAndUnblock(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	first := time.Now().Add(-time.Hour).UTC()

	require.NoError(t, svc.TouchWarmupContact(ctx, 951001, "alice", "ru", first))
	require.NoError(t, svc.TouchWarmupContact(ctx, 0, "nobody", "", first), "non-positive ids are ignored")
	c, err := svc.GetWarmupContact(ctx, 951001)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, WarmupContactSourceStart, c.Source)
	assert.WithinDuration(t, first, c.FirstSeenAt, time.Millisecond)

	later := first.Add(30 * time.Minute)
	require.NoError(t, svc.TouchWarmupContact(ctx, 951001, "alice_new", "", later))
	c, err = svc.GetWarmupContact(ctx, 951001)
	require.NoError(t, err)
	assert.WithinDuration(t, first, c.FirstSeenAt, time.Millisecond, "the first start never moves")
	assert.WithinDuration(t, later, c.LastSeenAt, time.Millisecond)
	assert.Equal(t, "alice_new", c.Username)
	assert.Equal(t, "ru", c.LanguageCode, "an unknown language keeps the known one")

	page, err := svc.WarmupContactsStartedBetween(ctx, first.Add(-time.Minute), first.Add(time.Minute), 0, 10)
	require.NoError(t, err)
	require.Len(t, page.Anchors, 1)
	assert.Equal(t, "start", page.Anchors[0].AnchorKey)

	require.NoError(t, svc.MarkWarmupContactBlocked(ctx, 951001, later))
	page, err = svc.WarmupContactsStartedBetween(ctx, first.Add(-time.Minute), first.Add(time.Minute), 0, 10)
	require.NoError(t, err)
	assert.Empty(t, page.Anchors, "blocked users are excluded")

	require.NoError(t, svc.TouchWarmupContact(ctx, 951001, "", "", later.Add(time.Minute)))
	c, err = svc.GetWarmupContact(ctx, 951001)
	require.NoError(t, err)
	assert.Nil(t, c.BlockedAt, "writing to the bot again unblocks")

	// Blocking an unknown user records a backfill contact.
	require.NoError(t, svc.MarkWarmupContactBlocked(ctx, 951002, later))
	c, err = svc.GetWarmupContact(ctx, 951002)
	require.NoError(t, err)
	assert.Equal(t, WarmupContactSourceBackfill, c.Source)
	assert.NotNil(t, c.BlockedAt)

	missing, err := svc.GetWarmupContact(ctx, 951999)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestWarmupContacts_StartedPaging(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for i := int64(1); i <= 5; i++ {
		require.NoError(t, svc.TouchWarmupContact(ctx, 952000+i, "", "", at))
	}
	var got []int64
	cursor := int64(0)
	for {
		page, err := svc.WarmupContactsStartedBetween(ctx, at.Add(-time.Second), at.Add(time.Second), cursor, 2)
		require.NoError(t, err)
		for _, a := range page.Anchors {
			got = append(got, a.TelegramID)
		}
		if page.Next == 0 {
			break
		}
		cursor = page.Next
	}
	assert.Equal(t, []int64{952001, 952002, 952003, 952004, 952005}, got)
}

func TestWarmupScenario_UpdateEnabledAtVersionAndAudit(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sc := warmupScenarioByKey(t, svc, "sub_expired")
	require.False(t, sc.Enabled)

	// Switching on sets enabled_at to now.
	f := warmupFieldsOf(sc)
	f.Enabled = true
	res, err := svc.UpdateWarmupScenario(ctx, warmupMeta("on"), WarmupScenarioUpdate{ID: sc.ID, Version: 1, WarmupScenarioFields: f})
	require.NoError(t, err)
	assert.Equal(t, sc.ID, res.TargetID)
	assert.Equal(t, AdminTargetWarmupScenario, res.Audit.TargetType)
	assert.Equal(t, string(AdminActionWarmupScenarioUpdated), res.Audit.Action)
	on, err := svc.GetWarmupScenario(ctx, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, on.EnabledAt)
	assert.WithinDuration(t, time.Now(), *on.EnabledAt, time.Minute)
	assert.Equal(t, 2, on.Version)

	// Replaying the request is a no-op; a stale version conflicts.
	replay, err := svc.UpdateWarmupScenario(ctx, warmupMeta("on"), WarmupScenarioUpdate{ID: sc.ID, Version: 1, WarmupScenarioFields: f})
	require.NoError(t, err)
	assert.True(t, replay.Replayed)
	_, err = svc.UpdateWarmupScenario(ctx, warmupMeta("stale"), WarmupScenarioUpdate{ID: sc.ID, Version: 1, WarmupScenarioFields: f})
	require.ErrorIs(t, err, ErrWarmupVersionConflict)

	// Pretend it was switched on long ago: a text edit keeps enabled_at.
	past := time.Now().Add(-72 * time.Hour).UTC()
	require.NoError(t, svc.db.Model(&WarmupScenario{}).Where("id = ?", sc.ID).Update("enabled_at", past).Error)
	f.Name = "Подписка закончилась!"
	_, err = svc.UpdateWarmupScenario(ctx, warmupMeta("rename"), WarmupScenarioUpdate{ID: sc.ID, Version: 2, WarmupScenarioFields: f})
	require.NoError(t, err)
	renamed, err := svc.GetWarmupScenario(ctx, sc.ID)
	require.NoError(t, err)
	assert.WithinDuration(t, past, *renamed.EnabledAt, time.Second)

	// A schedule change restarts from now: the new delay must not replay history.
	f.DelayValue, f.DelayUnit = 2, WarmupUnitHours
	_, err = svc.UpdateWarmupScenario(ctx, warmupMeta("delay"), WarmupScenarioUpdate{ID: sc.ID, Version: 3, WarmupScenarioFields: f})
	require.NoError(t, err)
	moved, err := svc.GetWarmupScenario(ctx, sc.ID)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), *moved.EnabledAt, time.Minute)

	// Switching off clears it.
	f.Enabled = false
	_, err = svc.UpdateWarmupScenario(ctx, warmupMeta("off"), WarmupScenarioUpdate{ID: sc.ID, Version: 4, WarmupScenarioFields: f})
	require.NoError(t, err)
	off, err := svc.GetWarmupScenario(ctx, sc.ID)
	require.NoError(t, err)
	assert.False(t, off.Enabled)
	assert.Nil(t, off.EnabledAt)

	_, err = svc.UpdateWarmupScenario(ctx, warmupMeta("missing"), WarmupScenarioUpdate{ID: 9999, Version: 1, WarmupScenarioFields: f})
	require.ErrorIs(t, err, ErrWarmupScenarioNotFound)
}

func TestWarmupScenario_CreateIsDataDriven(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	f := warmupFieldsOf(warmupScenarioByKey(t, svc, "winback_30d"))
	f.Name, f.DelayValue, f.Enabled = "Возврат клиента: 60 дней", 60, true

	res, err := svc.CreateWarmupScenario(ctx, warmupMeta("create"), WarmupScenarioCreate{Key: "winback_60d", WarmupScenarioFields: f})
	require.NoError(t, err)
	created, err := svc.GetWarmupScenario(ctx, res.TargetID)
	require.NoError(t, err)
	assert.Equal(t, "winback_60d", created.Key)
	assert.Equal(t, 1, created.Version)
	assert.NotNil(t, created.EnabledAt)

	_, err = svc.CreateWarmupScenario(ctx, warmupMeta("create-again"), WarmupScenarioCreate{Key: "winback_60d", WarmupScenarioFields: f})
	require.ErrorIs(t, err, ErrWarmupScenarioKeyTaken)
	all, err := svc.ListWarmupScenarios(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 15)
}

func TestWarmupSettings_Update(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	res, err := svc.UpdateWarmupSettings(ctx, warmupMeta("s1"), WarmupSettingsInput{Version: 1, Enabled: false, MaxPerDay: 3, CooldownHours: 12, CatchupHours: 4})
	require.NoError(t, err)
	assert.Equal(t, AdminTargetWarmupSettings, res.Audit.TargetType)
	s, err := svc.GetWarmupSettings(ctx)
	require.NoError(t, err)
	assert.False(t, s.Enabled, "false is stored, not replaced by the column default")
	assert.Equal(t, 3, s.MaxPerDay)
	assert.Equal(t, 2, s.Version)
	_, err = svc.UpdateWarmupSettings(ctx, warmupMeta("s2"), WarmupSettingsInput{Version: 1, Enabled: true, MaxPerDay: 3, CooldownHours: 12, CatchupHours: 4})
	require.ErrorIs(t, err, ErrWarmupVersionConflict)
}

// warmupScenarioStates snapshots key -> scenario.
func warmupScenarioStates(t *testing.T, svc *Service) map[string]WarmupScenario {
	t.Helper()
	all, err := svc.ListWarmupScenarios(context.Background())
	require.NoError(t, err)
	out := make(map[string]WarmupScenario, len(all))
	for _, sc := range all {
		out[sc.Key] = sc
	}
	return out
}

// Switching the warm-up on (OFF -> ON) moves enabled_at of the enabled
// scenarios to now in the same transaction; nothing else changes it.
func TestWarmupSettings_SwitchOnResetsEnabledAt(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	require.NoError(t, svc.db.Model(&WarmupScenario{}).Where("enabled = ?", true).Update("enabled_at", past).Error)
	before := warmupScenarioStates(t, svc)
	in := WarmupSettingsInput{Version: 1, MaxPerDay: 2, CooldownHours: 24, CatchupHours: 6}

	assertEnabledAtKept := func(msg string) {
		t.Helper()
		for key, sc := range warmupScenarioStates(t, svc) {
			if sc.Enabled {
				require.NotNil(t, sc.EnabledAt, key)
				assert.WithinDuration(t, past, *sc.EnabledAt, time.Second, "%s: %s", key, msg)
			} else {
				assert.Nil(t, sc.EnabledAt, key)
			}
		}
	}

	// OFF -> OFF (a limits edit) keeps enabled_at.
	in.MaxPerDay = 3
	_, err := svc.UpdateWarmupSettings(ctx, warmupMeta("off-off"), in)
	require.NoError(t, err)
	assertEnabledAtKept("OFF -> OFF")

	// A rejected OFF -> ON (stale version) rolls the reset back.
	stale := in
	stale.Enabled = true
	_, err = svc.UpdateWarmupSettings(ctx, warmupMeta("stale-on"), stale)
	require.ErrorIs(t, err, ErrWarmupVersionConflict)
	assertEnabledAtKept("rejected OFF -> ON")

	// OFF -> ON moves enabled_at of every enabled scenario to the switch-on.
	in.Version, in.Enabled = 2, true
	switchOn := time.Now().UTC()
	res, err := svc.UpdateWarmupSettings(ctx, warmupMeta("on"), in)
	require.NoError(t, err)
	require.False(t, res.Replayed)
	onAt := res.Audit.CreatedAt
	assert.WithinDuration(t, switchOn, onAt, time.Minute)
	after := warmupScenarioStates(t, svc)
	require.Len(t, after, 14)
	enabled := 0
	for key, sc := range after {
		old := before[key]
		assert.Equal(t, old.Enabled, sc.Enabled, "%s: scenario switches are kept", key)
		assert.Equal(t, old.Version, sc.Version, "%s: scenario version is kept", key)
		assert.Equal(t, old.MessageHTML, sc.MessageHTML, key)
		if !sc.Enabled {
			assert.Nil(t, sc.EnabledAt, "%s: a disabled scenario stays without enabled_at", key)
			continue
		}
		enabled++
		require.NotNil(t, sc.EnabledAt, key)
		assert.WithinDuration(t, onAt, *sc.EnabledAt, time.Second, "%s: enabled_at = switch-on", key)
	}
	assert.Equal(t, 11, enabled)

	// A replay of the same request does not re-apply.
	require.NoError(t, svc.db.Model(&WarmupScenario{}).Where("enabled = ?", true).Update("enabled_at", past).Error)
	replay, err := svc.UpdateWarmupSettings(ctx, warmupMeta("on"), in)
	require.NoError(t, err)
	assert.True(t, replay.Replayed)
	assertEnabledAtKept("replayed OFF -> ON")

	// ON -> ON (a limits edit) and ON -> OFF keep enabled_at.
	in.Version, in.MaxPerDay = 3, 4
	_, err = svc.UpdateWarmupSettings(ctx, warmupMeta("on-on"), in)
	require.NoError(t, err)
	assertEnabledAtKept("ON -> ON")
	in.Version, in.Enabled = 4, false
	_, err = svc.UpdateWarmupSettings(ctx, warmupMeta("on-off"), in)
	require.NoError(t, err)
	assertEnabledAtKept("ON -> OFF")
}

// warmupPaidPlan creates a purchasable plan.
func warmupPaidPlan(t *testing.T, svc *Service, name string) *Plan {
	t.Helper()
	plan := &Plan{Name: name, IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	return plan
}

func warmupPaidSubscription(t *testing.T, svc *Service, plan *Plan, telegramID int64, expiresAt time.Time) *Subscription {
	t.Helper()
	sub := &Subscription{
		TelegramID: telegramID, Username: fmt.Sprintf("paid%d", telegramID), ClientID: fmt.Sprintf("warmup-paid-%d", telegramID),
		SubscriptionID: fmt.Sprintf("warmup-paid-sub-%d", telegramID), PlanID: plan.ID, Status: string(SubscriptionStatusActive),
		ExpiresAt: ptrTime(expiresAt.UTC()),
	}
	require.NoError(t, svc.db.Create(sub).Error)
	return sub
}

func TestWarmupAnchors_Triggers(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	minute := time.Minute

	// A trial ending in 3h is activated in Telegram now.
	trial, err := svc.CreateTrialSubscription(ctx, "", "warmup-trial", "warmup-trial-client", now.Add(3*time.Hour))
	require.NoError(t, err)
	_, err = svc.BindTrialSubscription(ctx, "warmup-trial", 953001, "trial_user")
	require.NoError(t, err)

	activated, err := svc.WarmupTrialsActivatedBetween(ctx, now.Add(-minute), now.Add(minute), 0, 10)
	require.NoError(t, err)
	require.Len(t, activated.Anchors, 1)
	a := activated.Anchors[0]
	assert.Equal(t, int64(953001), a.TelegramID)
	assert.Equal(t, "trial_user", a.Username)
	assert.Equal(t, fmt.Sprintf("trial:%d", trial.ID), a.AnchorKey)
	require.NotNil(t, a.ExpiresAt)
	assert.WithinDuration(t, now.Add(3*time.Hour), *a.ExpiresAt, time.Second)

	ends, err := svc.WarmupTrialPeriodEndsBetween(ctx, now.Add(2*time.Hour), now.Add(4*time.Hour), 0, 10)
	require.NoError(t, err)
	require.Len(t, ends.Anchors, 1, "the trial end is the expiry the trial had when bound")
	assert.Equal(t, fmt.Sprintf("trial_end:%d", trial.ID), ends.Anchors[0].AnchorKey)
	assert.WithinDuration(t, now.Add(3*time.Hour), ends.Anchors[0].AnchorAt, time.Second)
	ends, err = svc.WarmupTrialPeriodEndsBetween(ctx, now.Add(-time.Hour), now.Add(time.Hour), 0, 10)
	require.NoError(t, err)
	assert.Empty(t, ends.Anchors)

	// Paid subscriptions: one expiring in 48h, one that expired an hour ago.
	plan := warmupPaidPlan(t, svc, "warmup-premium")
	expiring := warmupPaidSubscription(t, svc, plan, 953002, now.Add(48*time.Hour))
	expired := warmupPaidSubscription(t, svc, plan, 953003, now.Add(-time.Hour))
	free := createTestSubscription(t, svc, 953004, "free", "warmup-free")

	soon, err := svc.WarmupPaidExpiringBetween(ctx, now.Add(47*time.Hour), now.Add(49*time.Hour), 0, 10)
	require.NoError(t, err)
	require.Len(t, soon.Anchors, 1, "free and trial plans never count as paid expiry")
	assert.Equal(t, expiring.ID, soon.Anchors[0].SubscriptionID)
	assert.Equal(t, "warmup-premium", soon.Anchors[0].PlanName)
	assert.Equal(t, fmt.Sprintf("expiry:%d:%d", expiring.ID, expiring.ExpiresAt.Unix()), soon.Anchors[0].AnchorKey)

	freePlanID := testFreePlanID(t, svc)
	require.NoError(t, svc.ExpireSubscription(ctx, expired.ID, freePlanID))
	require.NoError(t, svc.ExpireSubscription(ctx, free.ID, freePlanID))
	gone, err := svc.WarmupPaidExpiredBetween(ctx, now.Add(-minute), time.Now().Add(minute), 0, 10)
	require.NoError(t, err)
	require.Len(t, gone.Anchors, 1, "only a paid term ending is a paid_expired event")
	g := gone.Anchors[0]
	assert.Equal(t, expired.ID, g.SubscriptionID)
	assert.Equal(t, "warmup-premium", g.PlanName)
	require.NotNil(t, g.ExpiresAt)
	assert.Equal(t, fmt.Sprintf("expiry:%d:%d", expired.ID, expired.ExpiresAt.Unix()), g.AnchorKey)
}

func TestWarmupUserFacts(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()

	unknown, err := svc.GetWarmupUserFacts(ctx, 954000, now)
	require.NoError(t, err)
	assert.False(t, unknown.HasSubscription || unknown.HadTrial || unknown.HasActivePaid || unknown.EverPaid || unknown.Connected || unknown.Blocked)

	_, err = svc.CreateTrialSubscription(ctx, "", "facts-trial", "facts-trial-client", now.Add(3*time.Hour))
	require.NoError(t, err)
	bound, err := svc.BindTrialSubscription(ctx, "facts-trial", 954001, "facts")
	require.NoError(t, err)
	require.NoError(t, svc.TouchWarmupContact(ctx, 954001, "facts", "uk", now))
	f, err := svc.GetWarmupUserFacts(ctx, 954001, now)
	require.NoError(t, err)
	assert.True(t, f.HasSubscription)
	assert.True(t, f.HadTrial)
	assert.False(t, f.HasActivePaid)
	assert.False(t, f.Connected)
	assert.Equal(t, "uk", f.LanguageCode)
	assert.Equal(t, FreePlanName, f.PlanName)

	require.NoError(t, svc.db.Model(&Subscription{}).Where("id = ?", bound.ID).Update("last_request", now).Error)
	require.NoError(t, svc.db.Create(&JournalEvent{CreatedAt: now, EventType: string(JournalPaymentSucceeded), Outcome: JournalOutcomeSuccess,
		Actor: JournalActorUser, TelegramID: 954001, Description: "paid", Details: "{}"}).Error)
	f, err = svc.GetWarmupUserFacts(ctx, 954001, now)
	require.NoError(t, err)
	assert.True(t, f.Connected)
	assert.True(t, f.EverPaid)

	plan := warmupPaidPlan(t, svc, "facts-premium")
	warmupPaidSubscription(t, svc, plan, 954002, now.Add(24*time.Hour))
	paid, err := svc.GetWarmupUserFacts(ctx, 954002, now)
	require.NoError(t, err)
	assert.True(t, paid.HasActivePaid)
	paidLater, err := svc.GetWarmupUserFacts(ctx, 954002, now.Add(25*time.Hour))
	require.NoError(t, err)
	assert.False(t, paidLater.HasActivePaid, "an expired term is not active even before the expiry worker ran")
}

func warmupTestClaim(sc *WarmupScenario, telegramID int64, anchor string, coupon *WarmupCouponDraft) WarmupClaim {
	now := time.Now().UTC()
	return WarmupClaim{ScenarioID: sc.ID, ScenarioName: sc.Name, TelegramID: telegramID, Username: "u",
		AnchorKey: anchor, AnchorAt: now, DueAt: now, Coupon: coupon}
}

func TestWarmupDeliveries_ClaimRetryAndOutcome(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sc := warmupScenarioByKey(t, svc, "winback_3d")
	policy := WarmupRetryPolicy{MaxAttempts: 2, Backoff: 10 * time.Minute}
	now := time.Now().UTC()
	coupon := &WarmupCouponDraft{Code: "WB-AAAA1111", Percent: 15, ExpiresAt: now.Add(48 * time.Hour)}

	first, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 955001, "expiry:1:1", coupon), policy, now)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, WarmupDeliverySending, first.Delivery.Status)
	assert.Equal(t, 1, first.Delivery.Attempts)
	require.NotNil(t, first.Coupon)
	assert.Equal(t, "WB-AAAA1111", first.Coupon.Code)
	assert.Equal(t, WarmupCouponIssued, first.Coupon.Status)
	require.NotNil(t, first.Delivery.CouponID)

	// The same event is never claimed twice while being sent.
	again, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 955001, "expiry:1:1", coupon), policy, now)
	require.NoError(t, err)
	assert.Nil(t, again)

	// A transient error: retried only after the backoff, with the same coupon.
	require.NoError(t, svc.ReleaseWarmupDelivery(ctx, first.Delivery.ID, "Too Many Requests", false, now))
	early, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 955001, "expiry:1:1", nil), policy, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Nil(t, early, "still backing off")
	retry, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 955001, "expiry:1:1", nil), policy, now.Add(11*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, first.Delivery.ID, retry.Delivery.ID)
	assert.Equal(t, 2, retry.Delivery.Attempts)
	require.NotNil(t, retry.Coupon)
	assert.Equal(t, first.Coupon.ID, retry.Coupon.ID, "a retry keeps its coupon")

	// Attempts exhausted: final failure, coupon revoked, never claimed again.
	require.NoError(t, svc.ReleaseWarmupDelivery(ctx, retry.Delivery.ID, strings.Repeat("x", 900), true, now.Add(12*time.Minute)))
	none, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 955001, "expiry:1:1", nil), policy, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Nil(t, none)
	rows, total, err := svc.ListWarmupDeliveries(ctx, WarmupDeliveryFilter{TelegramID: 955001})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Equal(t, WarmupDeliveryFailed, rows[0].Status)
	assert.Len(t, []rune(rows[0].Error), 500, "the stored error is bounded")
	require.NotNil(t, rows[0].CouponCode)
	assert.Equal(t, "WB-AAAA1111", *rows[0].CouponCode)
	coupons, _, err := svc.ListWarmupCoupons(ctx, WarmupCouponFilter{TelegramID: 955001})
	require.NoError(t, err)
	require.Len(t, coupons, 1)
	assert.Equal(t, WarmupCouponRevoked, coupons[0].Status)
	assert.ErrorIs(t, svc.MarkWarmupDeliverySent(ctx, retry.Delivery.ID, 1, now), ErrWarmupDeliveryStale,
		"only a delivery being sent can be finished")

	// A new anchor of the same user and scenario is a new delivery.
	sent, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 955001, "expiry:1:2", nil), policy, now)
	require.NoError(t, err)
	require.NotNil(t, sent)
	require.NoError(t, svc.MarkWarmupDeliverySent(ctx, sent.Delivery.ID, 777, now))
	rows, _, err = svc.ListWarmupDeliveries(ctx, WarmupDeliveryFilter{TelegramID: 955001, Status: WarmupDeliverySent})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].MessageID)
	assert.Equal(t, 777, *rows[0].MessageID)
	require.NotNil(t, rows[0].SentAt)
	assert.Nil(t, rows[0].CouponCode)

	assert.ErrorIs(t, svc.MarkWarmupDeliverySent(ctx, 99999, 1, now), ErrWarmupDeliveryNotFound)
}

func TestWarmupDeliveries_BlockedExcludesUser(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sc := warmupScenarioByKey(t, svc, "winback_7d")
	now := time.Now().UTC()
	require.NoError(t, svc.TouchWarmupContact(ctx, 956001, "blk", "ru", now))

	claim, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 956001, "a", &WarmupCouponDraft{Code: "BLK-1", Percent: 10, ExpiresAt: now.Add(time.Hour)}),
		WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}, now)
	require.NoError(t, err)
	require.NoError(t, svc.MarkWarmupDeliveryBlocked(ctx, claim.Delivery.ID, "Forbidden: bot was blocked by the user", now))

	c, err := svc.GetWarmupContact(ctx, 956001)
	require.NoError(t, err)
	require.NotNil(t, c.BlockedAt)
	facts, err := svc.GetWarmupUserFacts(ctx, 956001, now)
	require.NoError(t, err)
	assert.True(t, facts.Blocked)
	coupons, _, err := svc.ListWarmupCoupons(ctx, WarmupCouponFilter{TelegramID: 956001})
	require.NoError(t, err)
	assert.Equal(t, WarmupCouponRevoked, coupons[0].Status)
	rows, _, err := svc.ListWarmupDeliveries(ctx, WarmupDeliveryFilter{ScenarioID: sc.ID})
	require.NoError(t, err)
	assert.Equal(t, WarmupDeliveryBlocked, rows[0].Status)
}

func TestWarmupDeliveries_CouponCodeCollisionWritesNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	sc := warmupScenarioByKey(t, svc, "winback_14d")
	now := time.Now().UTC()
	policy := WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}
	draft := &WarmupCouponDraft{Code: "SAME-CODE", Percent: 20, ExpiresAt: now.Add(time.Hour)}

	_, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 957001, "a", draft), policy, now)
	require.NoError(t, err)
	_, err = svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, 957002, "a", draft), policy, now)
	require.ErrorIs(t, err, ErrWarmupCouponCodeTaken)
	_, total, err := svc.ListWarmupDeliveries(ctx, WarmupDeliveryFilter{TelegramID: 957002})
	require.NoError(t, err)
	assert.Zero(t, total, "the delivery of a failed claim is rolled back with its coupon")
}

func TestWarmupDeliveries_StaleSendingAndLimits(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	a := warmupScenarioByKey(t, svc, "winback_3d")
	b := warmupScenarioByKey(t, svc, "winback_7d")
	now := time.Now().UTC()
	policy := WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}

	old, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(a, 958001, "x", nil), policy, now.Add(-2*time.Hour))
	require.NoError(t, err)
	require.NoError(t, svc.MarkWarmupDeliverySent(ctx, old.Delivery.ID, 1, now.Add(-2*time.Hour)))
	_, err = svc.ClaimWarmupDelivery(ctx, warmupTestClaim(b, 958001, "y", nil), policy, now.Add(-30*time.Minute))
	require.NoError(t, err)

	n, err := svc.CountWarmupUserDeliveriesSince(ctx, 958001, now.Add(-24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 2, n, "sent and being-sent messages both count")
	n, err = svc.CountWarmupUserDeliveriesSince(ctx, 958001, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	n, err = svc.CountWarmupScenarioDeliveriesSince(ctx, a.ID, now.Add(-24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	last, err := svc.LastWarmupUserDeliveryAt(ctx, 958001)
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.WithinDuration(t, now.Add(-30*time.Minute), *last, time.Second)
	none, err := svc.LastWarmupUserDeliveryAt(ctx, 958999)
	require.NoError(t, err)
	assert.Nil(t, none)

	released, err := svc.ReleaseStaleWarmupDeliveries(ctx, now.Add(-10*time.Minute), now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, released, "a send interrupted 30 minutes ago returns to retry")
	n, err = svc.CountWarmupUserDeliveriesSince(ctx, 958001, now.Add(-time.Hour))
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestWarmupSummary_SentAndCouponCounters(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	a := warmupScenarioByKey(t, svc, "winback_3d")
	b := warmupScenarioByKey(t, svc, "winback_7d")
	now := time.Now().UTC()
	policy := WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}
	send := func(sc *WarmupScenario, telegramID int64, anchor string, at time.Time, coupon *WarmupCouponDraft) {
		t.Helper()
		claim, err := svc.ClaimWarmupDelivery(ctx, warmupTestClaim(sc, telegramID, anchor, coupon), policy, at)
		require.NoError(t, err)
		require.NoError(t, svc.MarkWarmupDeliverySent(ctx, claim.Delivery.ID, 1, at))
	}

	empty, err := svc.GetWarmupSummary(ctx, now)
	require.NoError(t, err)
	assert.Zero(t, empty.SentToday)
	assert.Zero(t, empty.Sent7d)
	assert.Zero(t, empty.CouponsCreated)
	assert.NotNil(t, empty.ScenarioStats, "an empty list, never null")
	assert.Empty(t, empty.ScenarioStats)

	send(a, 959001, "now", now, &WarmupCouponDraft{Code: "SUM-AAAA", Percent: 10, ExpiresAt: now.Add(time.Hour)})
	send(a, 959002, "3d", now.Add(-3*24*time.Hour), nil)
	send(b, 959003, "10d", now.Add(-10*24*time.Hour), &WarmupCouponDraft{Code: "SUM-BBBB", Percent: 5, ExpiresAt: now.Add(-9 * 24 * time.Hour)})
	// Claimed but not delivered: neither sent nor counted per scenario.
	_, err = svc.ClaimWarmupDelivery(ctx, warmupTestClaim(b, 959004, "pending", nil), policy, now)
	require.NoError(t, err)

	summary, err := svc.GetWarmupSummary(ctx, now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summary.SentToday)
	assert.EqualValues(t, 2, summary.Sent7d, "the 10-day-old message is outside the week")
	assert.EqualValues(t, 2, summary.CouponsCreated, "every issued coupon, also expired ones")
	assert.EqualValues(t, 1, summary.ActiveCoupons)
	require.Len(t, summary.ScenarioStats, 2)
	byID := map[uint]WarmupScenarioStat{}
	for _, s := range summary.ScenarioStats {
		byID[s.ScenarioID] = s
	}
	assert.EqualValues(t, 2, byID[a.ID].Sent)
	require.NotNil(t, byID[a.ID].LastSentAt)
	assert.WithinDuration(t, now, *byID[a.ID].LastSentAt, time.Second)
	assert.EqualValues(t, 1, byID[b.ID].Sent)
	require.NotNil(t, byID[b.ID].LastSentAt)
	assert.WithinDuration(t, now.Add(-10*24*time.Hour), *byID[b.ID].LastSentAt, time.Second)
}

func TestParseWarmupAggregateTime(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 10, 5, 7, 2, 3, 500000000, time.UTC)
	for _, raw := range []string{"2026-10-05 07:02:03.5+00:00", "2026-10-05T10:02:03.5+03:00", "2026-10-05T07:02:03.5Z", "2026-10-05 07:02:03.5"} {
		at, ok := parseWarmupAggregateTime(raw)
		require.True(t, ok, raw)
		assert.True(t, want.Equal(at), raw)
	}
	_, ok := parseWarmupAggregateTime("yesterday")
	assert.False(t, ok)
}

func TestWarmupCoupon_EffectiveStatus(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := WarmupCoupon{Status: WarmupCouponIssued, ExpiresAt: now.Add(time.Hour)}
	assert.Equal(t, WarmupCouponIssued, c.EffectiveStatus(now))
	assert.Equal(t, WarmupCouponExpired, c.EffectiveStatus(now.Add(2*time.Hour)))
	c.Status = WarmupCouponRevoked
	assert.Equal(t, WarmupCouponRevoked, c.EffectiveStatus(now.Add(2*time.Hour)))
}

func TestWarmupImages_StoreDedupAndFileID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()
	data := []byte("\x89PNG\r\n\x1a\nfake-image-bytes")

	img, err := svc.CreateWarmupImage(ctx, "image/png", data)
	require.NoError(t, err)
	assert.Len(t, img.SHA256, 64)
	assert.Nil(t, img.Data, "metadata loads never carry the bytes")
	same, err := svc.CreateWarmupImage(ctx, "image/png", data)
	require.NoError(t, err)
	assert.Equal(t, img.ID, same.ID, "identical bytes are stored once")

	full, err := svc.GetWarmupImage(ctx, img.ID, true)
	require.NoError(t, err)
	assert.Equal(t, data, full.Data)
	require.NoError(t, svc.SetWarmupImageFileID(ctx, img.ID, "AgACAgIAAxk"))
	full, err = svc.GetWarmupImage(ctx, img.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "AgACAgIAAxk", full.TGFileID)
	require.NoError(t, svc.SetWarmupImageFileID(ctx, img.ID, ""), "a stale file id is forgotten")

	_, err = svc.GetWarmupImage(ctx, 9999, false)
	require.ErrorIs(t, err, ErrWarmupImageNotFound)
	require.ErrorIs(t, svc.SetWarmupImageFileID(ctx, 9999, "x"), ErrWarmupImageNotFound)
}
