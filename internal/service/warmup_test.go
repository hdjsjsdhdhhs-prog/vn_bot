package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var warmupTrialDefaults = database.TrialDefaults{DurationHours: 72, RateLimitPerHour: 3}

type warmupFixture struct {
	ctx context.Context
	db  *database.Service
	svc *WarmupService
}

func newWarmupFixture(t *testing.T) *warmupFixture {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "warmup.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &warmupFixture{ctx: context.Background(), db: db, svc: NewWarmupService(db, warmupTrialDefaults)}
}

func (f *warmupFixture) scenario(t *testing.T, key string) *database.WarmupScenario {
	t.Helper()
	var sc database.WarmupScenario
	require.NoError(t, f.db.GetDB().Where("key = ?", key).First(&sc).Error)
	return &sc
}

func (f *warmupFixture) input(t *testing.T, key string) WarmupScenarioInput {
	t.Helper()
	view, err := scenarioView(f.scenario(t, key))
	require.NoError(t, err)
	return InputOf(view)
}

func (f *warmupFixture) settings(t *testing.T) *database.WarmupSettings {
	t.Helper()
	s, err := f.svc.Settings(f.ctx)
	require.NoError(t, err)
	return s
}

func fieldReason(t *testing.T, err error) (string, string) {
	t.Helper()
	var fe *WarmupFieldError
	require.True(t, errors.As(err, &fe), "expected a field error, got %v", err)
	require.ErrorIs(t, err, ErrWarmupInvalid)
	return fe.Field, fe.Reason
}

func problemCodes(problems []WarmupProblem, severity string) []string {
	codes := []string{}
	for _, p := range problems {
		if p.Severity == severity {
			codes = append(codes, p.Field+":"+p.Code)
		}
	}
	return codes
}

func TestWarmupSeed_AllScenariosValidAndRenderable(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	views, err := f.svc.ListScenarios(f.ctx)
	require.NoError(t, err)
	require.Len(t, views, 14)

	expires := time.Date(2026, 12, 31, 20, 59, 0, 0, time.UTC)
	enabled := 0
	for i := range views {
		v := &views[i]
		e, err := f.svc.evaluateScenario(f.ctx, InputOf(v))
		require.NoError(t, err)
		assert.Empty(t, problemCodes(e.problems, WarmupProblemInvalid), "%s must satisfy the registries", v.Key)

		msg, err := f.svc.RenderScenario(f.ctx, &v.WarmupScenario, WarmupRecipient{
			TelegramID: 42, Username: "user", PlanName: "Premium", ExpiresAt: &expires,
		})
		require.NoError(t, err, v.Key)
		assert.NotContains(t, msg.HTML, "{{", v.Key)
		require.NoError(t, validateTelegramHTML(msg.HTML), v.Key)
		for _, b := range msg.Buttons {
			assert.NotEmpty(t, b.Value, "%s: no button is sent without a target", v.Key)
		}
		if v.Enabled {
			enabled++
		}
	}
	assert.Equal(t, 11, enabled)

	// The trial button has no URL until the admin sets it: hidden, not empty.
	trial := f.scenario(t, "trial_not_activated")
	msg, err := f.svc.RenderScenario(f.ctx, trial, WarmupRecipient{TelegramID: 1})
	require.NoError(t, err)
	assert.Empty(t, msg.Buttons)
	assert.Equal(t, 1, msg.HiddenButtons)
	assert.Contains(t, msg.HTML, "Это 3 дня без оплаты")

	sub := f.scenario(t, "sub_expiring_48h")
	msg, err = f.svc.RenderScenario(f.ctx, sub, WarmupRecipient{TelegramID: 1, PlanName: "Premium <PRO>", ExpiresAt: &expires})
	require.NoError(t, err)
	assert.Contains(t, msg.HTML, "«Premium &lt;PRO&gt;» действует до 31.12.2026 23:59 МСК.")
}

func TestWarmupRender_MissingValueIsNeverSent(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	_, err := f.svc.RenderScenario(f.ctx, f.scenario(t, "sub_expiring_48h"), WarmupRecipient{TelegramID: 1})
	field, reason := fieldReason(t, err)
	assert.Equal(t, "message_html", field)
	assert.Equal(t, "variable_unavailable", reason, "{{plan_name}} without a plan must not be sent")

	// A stored scenario that bypassed validation is re-checked before sending.
	sc := f.scenario(t, "winback_3d")
	sc.MessageHTML = "Привет, {{first_name}}"
	_, err = f.svc.RenderScenario(f.ctx, sc, WarmupRecipient{TelegramID: 1})
	_, reason = fieldReason(t, err)
	assert.Equal(t, "variable_unknown", reason)
	sc = f.scenario(t, "winback_3d")
	sc.Buttons = `[{"text":"x","kind":"callback","value":"admin_stats"}]`
	_, err = f.svc.RenderScenario(f.ctx, sc, WarmupRecipient{TelegramID: 1})
	_, reason = fieldReason(t, err)
	assert.Equal(t, "button_callback", reason)
}

func TestWarmupScenarioValidation(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	sc := f.scenario(t, "winback_3d")
	base := f.input(t, "winback_3d")

	cases := []struct {
		name   string
		mutate func(in *WarmupScenarioInput)
		field  string
		reason string
	}{
		{"unknown variable", func(in *WarmupScenarioInput) { in.MessageHTML = "Hi {{first_name}}" }, "message_html", "variable_unknown"},
		{"unavailable variable", func(in *WarmupScenarioInput) {
			in.TriggerType = WarmupTriggerBotStarted
			in.MessageHTML = "{{plan_name}}"
		}, "message_html", "variable_unavailable"},
		{"discount variable without discount", func(in *WarmupScenarioInput) { in.MessageHTML = "{{discount_code}}" }, "message_html", "variable_needs_discount"},
		{"discount without code", func(in *WarmupScenarioInput) { in.DiscountPercent = 10 }, "message_html", "discount_code_required"},
		{"markdown is not html", func(in *WarmupScenarioInput) { in.MessageHTML = "<div>*bold*</div>" }, "message_html", "html_unsupported_tag"},
		{"empty text", func(in *WarmupScenarioInput) { in.MessageHTML = "  " }, "message_html", "required"},
		{"callback not whitelisted", func(in *WarmupScenarioInput) {
			in.Buttons = []WarmupButton{{Text: "x", Kind: "callback", Value: "broadcast_confirm"}}
		}, "buttons", "button_callback"},
		{"unknown trigger", func(in *WarmupScenarioInput) { in.TriggerType = "purchase" }, "trigger_type", "unknown"},
		{"timing not allowed", func(in *WarmupScenarioInput) { in.Timing = "before" }, "timing", "not_allowed"},
		{"unknown category", func(in *WarmupScenarioInput) { in.Category = "promo" }, "category", "unknown"},
		{"delay too long", func(in *WarmupScenarioInput) { in.DelayValue = 400 }, "delay_value", "out_of_range"},
		{"cooldown out of range", func(in *WarmupScenarioInput) { v := 721; in.CooldownHours = &v }, "cooldown_hours", "out_of_range"},
		{"discount out of range", func(in *WarmupScenarioInput) { in.DiscountPercent = 101 }, "discount_percent", "out_of_range"},
		{"coupon template", func(in *WarmupScenarioInput) { in.CouponTemplate = "bad prefix" }, "coupon_template", "format"},
		{"coupon ttl", func(in *WarmupScenarioInput) { in.CouponTTLHours = 0 }, "coupon_ttl_hours", "out_of_range"},
		{"missing image", func(in *WarmupScenarioInput) { id := uint(9999); in.ImageID = &id }, "image_id", "not_found"},
		{"unknown language", func(in *WarmupScenarioInput) { in.Conditions.Languages = []string{"russian"} }, "conditions", "conditions_languages"},
		{"too long text", func(in *WarmupScenarioInput) { in.MessageHTML = strings.Repeat("я", 4097) }, "message_html", "too_long"},
	}
	for i, tc := range cases {
		in := base
		in.Buttons = append([]WarmupButton(nil), base.Buttons...)
		tc.mutate(&in)
		_, err := f.svc.UpdateScenario(f.ctx, "admin", "bad-"+tc.name, sc.ID, 1, in)
		require.Error(t, err, tc.name)
		field, reason := fieldReason(t, err)
		assert.Equal(t, tc.field, field, "%d %s", i, tc.name)
		assert.Equal(t, tc.reason, reason, "%d %s", i, tc.name)
	}

	// Nothing invalid was stored.
	stored := f.scenario(t, "winback_3d")
	assert.Equal(t, 1, stored.Version)

	// A valid edit with a discount and a whitelisted button is stored normalized.
	in := base
	in.DiscountPercent, in.CouponTemplate, in.CouponTTLHours = 15, "back", 72
	in.MessageHTML = "<b>Скидка {{discount_percent}}%</b>\r\nКод: <code>{{discount_code}}</code>"
	in.Buttons = []WarmupButton{{Text: " Тарифы ", Kind: "callback", Value: "buy_premium_list"}}
	in.Conditions.Languages = []string{"RU"}
	_, err := f.svc.UpdateScenario(f.ctx, "admin", "good", sc.ID, 1, in)
	require.NoError(t, err)
	stored = f.scenario(t, "winback_3d")
	assert.Equal(t, "BACK", stored.CouponTemplate)
	assert.Equal(t, 15, stored.DiscountPercent)
	assert.Equal(t, "<b>Скидка {{discount_percent}}%</b>\nКод: <code>{{discount_code}}</code>", stored.MessageHTML)
	assert.Equal(t, `[{"text":"Тарифы","kind":"callback","value":"buy_premium_list"}]`, stored.Buttons)
	assert.Equal(t, `{"has_active_paid":false,"languages":["ru"]}`, stored.Conditions)

	// A discount scenario is never rendered without its coupon.
	_, err = f.svc.RenderScenario(f.ctx, stored, WarmupRecipient{TelegramID: 1})
	_, reason := fieldReason(t, err)
	assert.Equal(t, "coupon_required", reason)
	msg, err := f.svc.RenderScenario(f.ctx, stored, WarmupRecipient{TelegramID: 1, Coupon: &database.WarmupCoupon{Code: "BACK-7K3P9QXD", Percent: 15}})
	require.NoError(t, err)
	assert.Equal(t, "<b>Скидка 15%</b>\nКод: <code>BACK-7K3P9QXD</code>", msg.HTML)
}

func TestWarmupScenario_ExpiryRemindersAreNotDuplicated(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	sc := f.scenario(t, "sub_expiring_24h")

	// Disabled (as seeded) it can be edited freely.
	in := f.input(t, "sub_expiring_24h")
	in.Name = "Подписка: остались сутки (черновик)"
	_, err := f.svc.UpdateScenario(f.ctx, "admin", "edit", sc.ID, 1, in)
	require.NoError(t, err)

	// Switching it on at the 1-day lead time of the existing reminder is refused.
	in.Enabled = true
	_, err = f.svc.UpdateScenario(f.ctx, "admin", "enable", sc.ID, 2, in)
	field, reason := fieldReason(t, err)
	assert.Equal(t, "delay_value", field)
	assert.Equal(t, "duplicates_expiry_reminder", reason)

	// 48 hours does not coincide with 3d/1d/3h: allowed, with a warning.
	in48 := f.input(t, "sub_expiring_48h")
	in48.Enabled = true
	p, err := f.svc.Preview(f.ctx, in48)
	require.NoError(t, err)
	assert.True(t, p.Valid)
	assert.Contains(t, problemCodes(p.Problems, WarmupProblemWarning), "trigger_type:expiry_reminders_active")
}

func TestWarmupScenario_CreateAndSettings(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	in := f.input(t, "winback_30d")
	in.DelayValue, in.Name = 60, "Возврат клиента: 60 дней"

	_, err := f.svc.CreateScenario(f.ctx, "admin", "c0", "Bad Key", in)
	field, reason := fieldReason(t, err)
	assert.Equal(t, "key", field)
	assert.Equal(t, "format", reason)
	res, err := f.svc.CreateScenario(f.ctx, "admin", "c1", "winback_60d", in)
	require.NoError(t, err)
	assert.NotZero(t, res.TargetID)
	views, err := f.svc.ListScenarios(f.ctx)
	require.NoError(t, err)
	assert.Len(t, views, 15)

	bad := []database.WarmupSettingsInput{
		{Version: 1, MaxPerDay: 0, CooldownHours: 24, CatchupHours: 6},
		{Version: 1, MaxPerDay: 2, CooldownHours: -1, CatchupHours: 6},
		{Version: 1, MaxPerDay: 2, CooldownHours: 24, CatchupHours: 73},
		{Version: 0, MaxPerDay: 2, CooldownHours: 24, CatchupHours: 6},
	}
	for i, in := range bad {
		_, err := f.svc.UpdateSettings(f.ctx, "admin", "bad-settings-"+string(rune('a'+i)), in)
		require.ErrorIs(t, err, ErrWarmupInvalid, "%d", i)
	}
	_, err = f.svc.UpdateSettings(f.ctx, "admin", "settings", database.WarmupSettingsInput{Version: 1, Enabled: true, MaxPerDay: 3, CooldownHours: 12, CatchupHours: 6})
	require.NoError(t, err)
	assert.Equal(t, 3, f.settings(t).MaxPerDay)
}

func TestWarmupPreview_SendsNothingAndShowsTestCoupon(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)

	trial := f.input(t, "trial_not_activated")
	p, err := f.svc.Preview(f.ctx, trial)
	require.NoError(t, err)
	assert.True(t, p.Valid)
	assert.Contains(t, problemCodes(p.Problems, WarmupProblemWarning), "buttons:button_url_missing")
	require.NotNil(t, p.Message)
	assert.Empty(t, p.Message.Buttons)
	assert.Equal(t, "3 дня", p.Variables[WarmupVarTrialDuration])
	_, hasPlan := p.Variables[WarmupVarPlanName]
	assert.False(t, hasPlan, "the example lists only variables of the trigger")

	trial.Buttons[0].Value = "https://dictatorvpn.example/trial"
	p, err = f.svc.Preview(f.ctx, trial)
	require.NoError(t, err)
	require.Len(t, p.Message.Buttons, 1)
	assert.Empty(t, problemCodes(p.Problems, WarmupProblemWarning))

	in := f.input(t, "winback_7d")
	in.DiscountPercent, in.CouponTemplate = 20, "WB"
	in.MessageHTML = "Код {{discount_code}} на {{discount_percent}}% для {{username}}"
	p, err = f.svc.Preview(f.ctx, in)
	require.NoError(t, err)
	require.True(t, p.Valid)
	assert.Regexp(t, `^WB-[A-HJ-NP-Z2-9]{8}$`, p.DiscountCode)
	assert.Equal(t, "Код "+p.DiscountCode+" на 20% для @dictator_vpn_user", p.Message.HTML)
	coupons, total, err := f.db.ListWarmupCoupons(f.ctx, database.WarmupCouponFilter{})
	require.NoError(t, err)
	assert.Zero(t, total, "a preview never stores a coupon")
	assert.Empty(t, coupons)
	_, deliveries, err := f.db.ListWarmupDeliveries(f.ctx, database.WarmupDeliveryFilter{})
	require.NoError(t, err)
	assert.Zero(t, deliveries, "a preview never journals a delivery")

	in.MessageHTML = "{{oops}}"
	p, err = f.svc.Preview(f.ctx, in)
	require.NoError(t, err)
	assert.False(t, p.Valid)
	assert.Nil(t, p.Message)
	assert.Contains(t, problemCodes(p.Problems, WarmupProblemInvalid), "message_html:variable_unknown")
}

func warmupPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestWarmupUploadImageAndCaptionLimit(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)

	uploaded, err := f.svc.UploadImage(f.ctx, "admin", "upload-ok", warmupPNG(t, 64, 32))
	require.NoError(t, err)
	img := uploaded.Image
	assert.Equal(t, "image/png", img.Mime)

	for name, data := range map[string][]byte{
		"required":         nil,
		"too_large":        make([]byte, database.WarmupImageMaxBytes+1),
		"unsupported_type": []byte("GIF89a....."),
		"corrupt":          append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...),
		"dimensions":       warmupPNG(t, 2100, 100),
	} {
		_, err := f.svc.UploadImage(f.ctx, "admin", "upload-"+name, data)
		_, reason := fieldReason(t, err)
		assert.Equal(t, name, reason)
	}

	// With a picture the text becomes a caption: 1024 instead of 4096.
	in := f.input(t, "winback_14d")
	in.ImageID = &img.ID
	in.MessageHTML = strings.Repeat("я", 1025)
	p, err := f.svc.Preview(f.ctx, in)
	require.NoError(t, err)
	assert.Contains(t, problemCodes(p.Problems, WarmupProblemInvalid), "message_html:too_long")
	in.MessageHTML = strings.Repeat("я", 1000)
	p, err = f.svc.Preview(f.ctx, in)
	require.NoError(t, err)
	require.True(t, p.Valid)
	require.NotNil(t, p.Image)
	assert.Equal(t, img.ID, p.Image.ID)
	assert.Equal(t, 1024, p.Message.MaxLength)
}

func TestWarmupAnchorWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	longAgo := now.Add(-30 * 24 * time.Hour)
	sc := &database.WarmupScenario{Enabled: true, EnabledAt: &longAgo, Timing: database.WarmupTimingAfter, DelayValue: 3, DelayUnit: database.WarmupUnitDays}

	from, to, ok := WarmupAnchorWindow(sc, 6*time.Hour, now)
	require.True(t, ok)
	assert.Equal(t, now.Add(-72*time.Hour-6*time.Hour), from)
	assert.Equal(t, now.Add(-72*time.Hour), to)
	assert.Equal(t, now, WarmupDueAt(sc, to))

	sc.Timing, sc.DelayValue, sc.DelayUnit = database.WarmupTimingBefore, 24, database.WarmupUnitHours
	from, to, ok = WarmupAnchorWindow(sc, 6*time.Hour, now)
	require.True(t, ok)
	assert.Equal(t, now.Add(18*time.Hour), from)
	assert.Equal(t, now.Add(24*time.Hour), to)
	assert.Equal(t, now.Add(-6*time.Hour), WarmupDueAt(sc, from))

	// Enabled an hour ago: only the last hour of due events, never history.
	recent := now.Add(-time.Hour)
	sc.EnabledAt = &recent
	from, _, ok = WarmupAnchorWindow(sc, 6*time.Hour, now)
	require.True(t, ok)
	assert.Equal(t, now.Add(23*time.Hour), from)

	future := now.Add(time.Minute)
	sc.EnabledAt = &future
	_, _, ok = WarmupAnchorWindow(sc, 6*time.Hour, now)
	assert.False(t, ok)
	sc.Enabled = false
	_, _, ok = WarmupAnchorWindow(sc, 6*time.Hour, now)
	assert.False(t, ok)
}

// setEnabledAt moves the switch-on time of a seeded scenario.
func (f *warmupFixture) setEnabledAt(t *testing.T, key string, at time.Time) *database.WarmupScenario {
	t.Helper()
	require.NoError(t, f.db.GetDB().Model(&database.WarmupScenario{}).Where("key = ?", key).
		Updates(map[string]any{"enabled": true, "enabled_at": at.UTC()}).Error)
	return f.scenario(t, key)
}

func TestWarmupDueAnchors_IgnoresHistoryBeforeEnabledAt(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	now := time.Now().UTC()

	// A trial activated in Telegram right now.
	_, err := f.db.CreateTrialSubscription(f.ctx, "", "due-trial", "due-trial-client", now.Add(time.Hour))
	require.NoError(t, err)
	_, err = f.db.BindTrialSubscription(f.ctx, "due-trial", 960001, "due")
	require.NoError(t, err)
	settings := f.settings(t)

	// The journal is append-only, so the scenario is moved instead of the
	// event: with a 0-minute delay the bind is due at once; a scenario
	// switched on after the bind treats it as history.
	sc := f.setEnabledAt(t, "trial_no_purchase", now.Add(time.Minute))
	due, _, err := f.svc.DueAnchors(f.ctx, sc, settings, now, 0, 100)
	require.NoError(t, err)
	assert.Empty(t, due, "enabled in the future: nothing is due")

	require.NoError(t, f.db.GetDB().Model(&database.WarmupScenario{}).Where("id = ?", sc.ID).
		Updates(map[string]any{"delay_value": 0, "delay_unit": database.WarmupUnitMinutes}).Error)
	sc = f.setEnabledAt(t, "trial_no_purchase", now.Add(10*time.Second))
	due, _, err = f.svc.DueAnchors(f.ctx, sc, settings, now.Add(time.Minute), 0, 100)
	require.NoError(t, err)
	assert.Empty(t, due, "the bind happened before the scenario was switched on: history is never sent")

	sc = f.setEnabledAt(t, "trial_no_purchase", now.Add(-time.Hour))
	due, _, err = f.svc.DueAnchors(f.ctx, sc, settings, now.Add(time.Minute), 0, 100)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, int64(960001), due[0].Anchor.TelegramID)
	assert.False(t, due[0].DueAt.Before(*sc.EnabledAt))

	// Outside the catch-up window (6h) the event is no longer due.
	due, _, err = f.svc.DueAnchors(f.ctx, sc, settings, now.Add(7*time.Hour), 0, 100)
	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestWarmupDueAnchors_AllTriggers(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	now := time.Now().UTC()
	settings := f.settings(t)
	longAgo := now.Add(-365 * 24 * time.Hour)

	require.NoError(t, f.svc.TouchContact(f.ctx, 961001, "@starter", "RU"))
	_, err := f.db.CreateTrialSubscription(f.ctx, "", "all-trial", "all-trial-client", now.Add(2*time.Hour))
	require.NoError(t, err)
	_, err = f.db.BindTrialSubscription(f.ctx, "all-trial", 961002, "trial")
	require.NoError(t, err)
	plan := &database.Plan{Name: "all-premium", IsActive: true}
	require.NoError(t, f.db.GetDB().Create(plan).Error)
	sub := &database.Subscription{TelegramID: 961003, Username: "paid", ClientID: "all-paid", SubscriptionID: "all-paid-sub",
		PlanID: plan.ID, Status: "active", ExpiresAt: ptrTimeWarmup(now.Add(48 * time.Hour))}
	require.NoError(t, f.db.GetDB().Create(sub).Error)
	gone := &database.Subscription{TelegramID: 961004, Username: "gone", ClientID: "all-gone", SubscriptionID: "all-gone-sub",
		PlanID: plan.ID, Status: "active", ExpiresAt: ptrTimeWarmup(now.Add(-time.Minute))}
	require.NoError(t, f.db.GetDB().Create(gone).Error)
	free, err := f.db.GetPlanByName(f.ctx, database.FreePlanName)
	require.NoError(t, err)
	require.NoError(t, f.db.ExpireSubscription(f.ctx, gone.ID, free.ID))

	cases := []struct {
		key, trigger, timing string
		delay                int
		at                   time.Time
		telegramID           int64
	}{
		{"trial_not_activated", WarmupTriggerBotStarted, "after", 0, now.Add(time.Minute), 961001},
		{"trial_not_connected_3h", WarmupTriggerTrialActivated, "after", 0, now.Add(time.Minute), 961002},
		{"trial_ending", WarmupTriggerTrialPeriodEnd, "before", 2, now.Add(time.Minute), 961002},
		{"sub_expiring_48h", WarmupTriggerPaidExpiry, "before", 48, now.Add(time.Minute), 961003},
		{"sub_expired", WarmupTriggerPaidExpired, "after", 0, now.Add(time.Minute), 961004},
	}
	for _, tc := range cases {
		sc := f.scenario(t, tc.key)
		sc.Enabled, sc.EnabledAt = true, &longAgo
		sc.TriggerType, sc.Timing, sc.DelayValue, sc.DelayUnit = tc.trigger, tc.timing, tc.delay, database.WarmupUnitHours
		due, next, err := f.svc.DueAnchors(f.ctx, sc, settings, tc.at, 0, 100)
		require.NoError(t, err, tc.key)
		assert.Zero(t, next)
		require.Len(t, due, 1, tc.key)
		assert.Equal(t, tc.telegramID, due[0].Anchor.TelegramID, tc.key)
		assert.False(t, due[0].DueAt.After(tc.at), tc.key)
	}
	contact, err := f.db.GetWarmupContact(f.ctx, 961001)
	require.NoError(t, err)
	assert.Equal(t, "starter", contact.Username, "the @ is not stored")
	assert.Equal(t, "ru", contact.LanguageCode)

	sc := f.scenario(t, "winback_3d")
	sc.TriggerType = "unknown"
	_, _, err = f.svc.DueAnchors(f.ctx, sc, settings, now, 0, 100)
	require.ErrorIs(t, err, ErrWarmupInvalid)
}

func ptrTimeWarmup(t time.Time) *time.Time { return &t }

func TestWarmupEvaluate_ConditionsAndLimits(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	now := time.Now().UTC()
	settings := f.settings(t)
	longAgo := now.Add(-time.Hour)
	policy := database.WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}

	require.NoError(t, f.svc.TouchContact(f.ctx, 962001, "eval", "ru"))
	sc := f.scenario(t, "trial_not_activated")
	sc.EnabledAt = &longAgo
	due := WarmupDue{Anchor: database.WarmupAnchor{TelegramID: 962001, AnchorKey: "start", AnchorAt: now.Add(-12 * time.Hour)}, DueAt: now}

	// The migration leaves the warm-up globally off.
	d, err := f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipDisabled, d.Reason, "globally OFF after the migration")
	settings.Enabled = true

	d, err = f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	require.True(t, d.Send, d.Reason)

	off := *settings
	off.Enabled = false
	d, err = f.svc.Evaluate(f.ctx, sc, &off, due, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipDisabled, d.Reason)

	late := due
	late.DueAt = now.Add(-7 * time.Hour)
	d, err = f.svc.Evaluate(f.ctx, sc, settings, late, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipOutsideWindow, d.Reason)
	beforeOn := due
	beforeOn.DueAt = longAgo.Add(-time.Minute)
	d, err = f.svc.Evaluate(f.ctx, sc, settings, beforeOn, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipOutsideWindow, d.Reason, "due before enabled_at is history")

	// Conditions: a user with a subscription is not offered the trial.
	createTestSubscriptionWarmup(t, f, 962002)
	other := due
	other.Anchor.TelegramID = 962002
	d, err = f.svc.Evaluate(f.ctx, sc, settings, other, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipConditions, d.Reason)

	// Language filter.
	sc.Conditions = `{"languages":["en"]}`
	d, err = f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipConditions, d.Reason)
	sc.Conditions = `{"languages":["ru"]}`

	// Cooldown: one message started 2h ago blocks the next for 24h.
	winback := f.scenario(t, "winback_3d")
	claim, err := f.svc.Claim(f.ctx, winback, WarmupDue{Anchor: database.WarmupAnchor{TelegramID: 962001, AnchorKey: "x", AnchorAt: now}, DueAt: now}, policy, now.Add(-2*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, claim)
	d, err = f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipCooldown, d.Reason)
	zero := 0
	sc.CooldownHours = &zero
	d, err = f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	assert.True(t, d.Send, "the scenario cooldown overrides the global one")

	// Per-user daily maximum (2 by default).
	_, err = f.svc.Claim(f.ctx, winback, WarmupDue{Anchor: database.WarmupAnchor{TelegramID: 962001, AnchorKey: "y", AnchorAt: now}, DueAt: now}, policy, now.Add(-time.Hour))
	require.NoError(t, err)
	d, err = f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipUserDailyLimit, d.Reason)

	// Scenario daily cap.
	winback.DailyCap = 1
	winback.EnabledAt = &longAgo
	d, err = f.svc.Evaluate(f.ctx, winback, settings, WarmupDue{Anchor: database.WarmupAnchor{TelegramID: 962003}, DueAt: now}, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipScenarioCap, d.Reason)

	// Blocked users are excluded from everything.
	require.NoError(t, f.db.MarkWarmupContactBlocked(f.ctx, 962001, now))
	sc.CooldownHours = nil
	d, err = f.svc.Evaluate(f.ctx, sc, settings, due, now)
	require.NoError(t, err)
	assert.Equal(t, WarmupSkipBlocked, d.Reason)
}

func createTestSubscriptionWarmup(t *testing.T, f *warmupFixture, telegramID int64) {
	t.Helper()
	free, err := f.db.GetPlanByName(f.ctx, database.FreePlanName)
	require.NoError(t, err)
	sub := &database.Subscription{TelegramID: telegramID, Username: "has-sub", ClientID: "has-sub-client", SubscriptionID: "has-sub-sub",
		PlanID: free.ID, Status: "active"}
	require.NoError(t, f.db.GetDB().Create(sub).Error)
}

// collidingRepo makes the first coupon claim collide.
type collidingRepo struct {
	*database.Service

	calls int
	codes []string
}

func (r *collidingRepo) ClaimWarmupDelivery(ctx context.Context, c database.WarmupClaim, p database.WarmupRetryPolicy, now time.Time) (*database.WarmupClaimResult, error) {
	r.calls++
	if c.Coupon != nil {
		r.codes = append(r.codes, c.Coupon.Code)
	}
	if r.calls == 1 {
		return nil, database.ErrWarmupCouponCodeTaken
	}
	return r.Service.ClaimWarmupDelivery(ctx, c, p, now)
}

func TestWarmupClaim_IssuesCouponAndRetriesCodeCollision(t *testing.T) {
	t.Parallel()
	f := newWarmupFixture(t)
	repo := &collidingRepo{Service: f.db}
	svc := NewWarmupService(repo, warmupTrialDefaults)
	now := time.Now().UTC()
	sc := f.scenario(t, "winback_7d")
	sc.DiscountPercent, sc.CouponTemplate, sc.CouponTTLHours = 25, "WB", 48

	res, err := svc.Claim(f.ctx, sc, WarmupDue{Anchor: database.WarmupAnchor{TelegramID: 963001, AnchorKey: "k", AnchorAt: now}, DueAt: now},
		database.WarmupRetryPolicy{MaxAttempts: 3, Backoff: time.Minute}, now)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.Coupon)
	assert.Equal(t, 2, repo.calls, "a colliding code is regenerated")
	require.Len(t, repo.codes, 2)
	assert.NotEqual(t, repo.codes[0], repo.codes[1])
	assert.Equal(t, res.Coupon.Code, repo.codes[1])
	assert.Equal(t, 25, res.Coupon.Percent)
	assert.Equal(t, int64(963001), res.Coupon.TelegramID)
	assert.WithinDuration(t, now.Add(48*time.Hour), res.Coupon.ExpiresAt, time.Second)

	msg, err := svc.RenderScenario(f.ctx, &database.WarmupScenario{
		ID: sc.ID, Category: sc.Category, Name: sc.Name, TriggerType: sc.TriggerType, Timing: sc.Timing,
		DelayValue: sc.DelayValue, DelayUnit: sc.DelayUnit, MessageHTML: "Ваш код: {{discount_code}}", Buttons: "[]",
		Conditions: "{}", DiscountPercent: 25, CouponTemplate: "WB", CouponTTLHours: 48,
	}, WarmupRecipient{TelegramID: 963001, Coupon: res.Coupon})
	require.NoError(t, err)
	assert.Equal(t, "Ваш код: "+res.Coupon.Code, msg.HTML)
}
