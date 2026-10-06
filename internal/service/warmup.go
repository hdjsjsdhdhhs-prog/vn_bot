package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register decoders for upload validation
	_ "image/png"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// WarmupRepository is the persistence seam of WarmupService. *database.Service
// implements it.
type WarmupRepository interface {
	GetWarmupSettings(ctx context.Context) (*database.WarmupSettings, error)
	UpdateWarmupSettings(ctx context.Context, meta database.AdminConfigMeta, in database.WarmupSettingsInput) (*database.AdminConfigResult, error)
	ListWarmupScenarios(ctx context.Context) ([]database.WarmupScenario, error)
	ListEnabledWarmupScenarios(ctx context.Context) ([]database.WarmupScenario, error)
	GetWarmupScenario(ctx context.Context, id uint) (*database.WarmupScenario, error)
	UpdateWarmupScenario(ctx context.Context, meta database.AdminConfigMeta, in database.WarmupScenarioUpdate) (*database.AdminConfigResult, error)
	CreateWarmupScenario(ctx context.Context, meta database.AdminConfigMeta, in database.WarmupScenarioCreate) (*database.AdminConfigResult, error)
	UploadWarmupImage(ctx context.Context, meta database.AdminConfigMeta, mime string, data []byte) (*database.AdminConfigResult, error)
	DeleteWarmupImage(ctx context.Context, meta database.AdminConfigMeta, id uint) (*database.AdminConfigResult, error)
	WarmupImageUsage(ctx context.Context, id uint) (int64, error)
	GetWarmupSummary(ctx context.Context, now time.Time) (*database.WarmupSummary, error)
	ListWarmupDeliveries(ctx context.Context, f database.WarmupDeliveryFilter) ([]database.WarmupDeliveryRow, int64, error)
	ListWarmupCoupons(ctx context.Context, f database.WarmupCouponFilter) ([]database.WarmupCoupon, int64, error)
	GetWarmupImage(ctx context.Context, id uint, withData bool) (*database.WarmupImage, error)
	TouchWarmupContact(ctx context.Context, telegramID int64, username, languageCode string, at time.Time) error
	WarmupContactsStartedBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*database.WarmupAnchorPage, error)
	WarmupTrialsActivatedBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*database.WarmupAnchorPage, error)
	WarmupTrialPeriodEndsBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*database.WarmupAnchorPage, error)
	WarmupPaidExpiringBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*database.WarmupAnchorPage, error)
	WarmupPaidExpiredBetween(ctx context.Context, from, to time.Time, cursor int64, limit int) (*database.WarmupAnchorPage, error)
	GetWarmupUserFacts(ctx context.Context, telegramID int64, now time.Time) (*database.WarmupUserFacts, error)
	ClaimWarmupDelivery(ctx context.Context, c database.WarmupClaim, policy database.WarmupRetryPolicy, now time.Time) (*database.WarmupClaimResult, error)
	WarmupDeliveryStatus(ctx context.Context, scenarioID uint, telegramID int64, anchorKey string) (string, error)
	MarkWarmupDeliverySent(ctx context.Context, id uint, messageID int, now time.Time) error
	ReleaseWarmupDelivery(ctx context.Context, id uint, sendErr string, final bool, now time.Time) error
	MarkWarmupDeliveryBlocked(ctx context.Context, id uint, sendErr string, now time.Time) error
	MarkWarmupDeliveryUnreachable(ctx context.Context, id uint, sendErr string, now time.Time) error
	ReleaseStaleWarmupDeliveries(ctx context.Context, olderThan, now time.Time) (int64, error)
	ExpireWarmupRetries(ctx context.Context, dueBefore, now time.Time) (int64, error)
	CountWarmupUserDeliveriesSince(ctx context.Context, telegramID int64, since time.Time) (int64, error)
	LastWarmupUserDeliveryAt(ctx context.Context, telegramID int64) (*time.Time, error)
	CountWarmupScenarioDeliveriesSince(ctx context.Context, scenarioID uint, since time.Time) (int64, error)
	GetTrialEffective(ctx context.Context, d database.TrialDefaults) (*database.TrialEffective, error)
}

var _ WarmupRepository = (*database.Service)(nil)

// ErrWarmupInvalid is unwrapped from WarmupFieldError.
var ErrWarmupInvalid = errors.New("warmup configuration is invalid")

// WarmupFieldError names the rejected field and a stable reason code.
type WarmupFieldError struct {
	Field  string
	Reason string
	Detail string
}

func (e *WarmupFieldError) Error() string {
	msg := ErrWarmupInvalid.Error() + ": " + e.Field + ": " + e.Reason
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

func (e *WarmupFieldError) Unwrap() error { return ErrWarmupInvalid }

// Problem severities of a scenario evaluation: invalid blocks saving and
// sending; warning is shown by the preview only.
const (
	WarmupProblemInvalid = "invalid"
	WarmupProblemWarning = "warning"
)

// WarmupProblem is one finding of a scenario evaluation.
type WarmupProblem struct {
	Field    string `json:"field"`
	Code     string `json:"code"`
	Detail   string `json:"detail,omitempty"`
	Severity string `json:"severity"`
}

// Bounds (the schema CHECK constraints mirror the numeric ones).
const (
	warmupMaxNameLength        = 100
	warmupMaxDescriptionLength = 500
	warmupMaxMessageBytes      = 16384
	warmupMaxDelayValue        = 525600
	warmupMaxDelay             = 366 * 24 * time.Hour
	warmupMaxCooldownHours     = 720
	warmupMaxDailyCap          = 100000
	warmupMaxCouponTTLHours    = 8760
	warmupMaxPosition          = 1000000
	warmupMinMaxPerDay         = 1
	warmupMaxMaxPerDay         = 20
	warmupMinCatchupHours      = 1
	warmupMaxCatchupHours      = 72
	warmupCouponRandomLength   = 8
	warmupCouponAttempts       = 5
	warmupLimitWindow          = 24 * time.Hour
)

const warmupCouponAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var (
	warmupKeyPattern            = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
	warmupCouponTemplatePattern = regexp.MustCompile(`^[A-Z0-9]{0,16}$`)
)

// warmupMSK is the zone of dates shown to users (the bot promises MSK).
var warmupMSK = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}()

// WarmupService owns the warm-up rules: scenario validation, rendering,
// trigger resolution, conditions and limits. It sends nothing: the worker
// (scheduler) and the Telegram adapter use it.
type WarmupService struct {
	repo          WarmupRepository
	trialDefaults database.TrialDefaults
	random        io.Reader
}

// NewWarmupService wires the warm-up. trialDefaults are the environment trial
// values (used for {{trial_days}} while no trial settings are stored).
func NewWarmupService(repo WarmupRepository, trialDefaults database.TrialDefaults) *WarmupService {
	return &WarmupService{repo: repo, trialDefaults: trialDefaults, random: rand.Reader}
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// Settings returns the global settings.
func (s *WarmupService) Settings(ctx context.Context) (*database.WarmupSettings, error) {
	settings, err := s.repo.GetWarmupSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("warmup settings: %w", err)
	}
	return settings, nil
}

// UpdateSettings validates and saves the global settings.
func (s *WarmupService) UpdateSettings(ctx context.Context, actor, requestKey string, in database.WarmupSettingsInput) (*database.AdminConfigResult, error) {
	switch {
	case in.MaxPerDay < warmupMinMaxPerDay || in.MaxPerDay > warmupMaxMaxPerDay:
		return nil, &WarmupFieldError{Field: "max_per_day", Reason: "out_of_range"}
	case in.CooldownHours < 0 || in.CooldownHours > warmupMaxCooldownHours:
		return nil, &WarmupFieldError{Field: "cooldown_hours", Reason: "out_of_range"}
	case in.CatchupHours < warmupMinCatchupHours || in.CatchupHours > warmupMaxCatchupHours:
		return nil, &WarmupFieldError{Field: "catchup_hours", Reason: "out_of_range"}
	case in.Version < 1:
		return nil, &WarmupFieldError{Field: "version", Reason: "required"}
	}
	return s.repo.UpdateWarmupSettings(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, in)
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

// WarmupScenarioInput is the editor payload of a scenario.
type WarmupScenarioInput struct {
	Category        string           `json:"category"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	TriggerType     string           `json:"trigger_type"`
	Timing          string           `json:"timing"`
	DelayValue      int              `json:"delay_value"`
	DelayUnit       string           `json:"delay_unit"`
	Enabled         bool             `json:"enabled"`
	MessageHTML     string           `json:"message_html"`
	ImageID         *uint            `json:"image_id"`
	Buttons         []WarmupButton   `json:"buttons"`
	Conditions      WarmupConditions `json:"conditions"`
	CooldownHours   *int             `json:"cooldown_hours"`
	DailyCap        int              `json:"daily_cap"`
	DiscountPercent int              `json:"discount_percent"`
	CouponTemplate  string           `json:"coupon_template"`
	CouponTTLHours  int              `json:"coupon_ttl_hours"`
	Position        int              `json:"position"`
}

// WarmupScenarioView is a stored scenario with its JSON documents decoded.
type WarmupScenarioView struct {
	database.WarmupScenario

	Buttons    []WarmupButton   `json:"buttons"`
	Conditions WarmupConditions `json:"conditions"`
}

// ListScenarios returns every scenario in admin order.
func (s *WarmupService) ListScenarios(ctx context.Context) ([]WarmupScenarioView, error) {
	scenarios, err := s.repo.ListWarmupScenarios(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]WarmupScenarioView, 0, len(scenarios))
	for i := range scenarios {
		view, err := scenarioView(&scenarios[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func scenarioView(sc *database.WarmupScenario) (*WarmupScenarioView, error) {
	buttons, err := DecodeWarmupButtons(sc.Buttons)
	if err != nil {
		return nil, fmt.Errorf("warmup scenario %d buttons: %w", sc.ID, err)
	}
	conditions, err := DecodeWarmupConditions(sc.Conditions)
	if err != nil {
		return nil, fmt.Errorf("warmup scenario %d conditions: %w", sc.ID, err)
	}
	return &WarmupScenarioView{WarmupScenario: *sc, Buttons: buttons, Conditions: conditions}, nil
}

// InputOf returns the editor payload of a stored scenario.
func InputOf(view *WarmupScenarioView) WarmupScenarioInput {
	sc := view.WarmupScenario
	return WarmupScenarioInput{
		Category: sc.Category, Name: sc.Name, Description: sc.Description, TriggerType: sc.TriggerType,
		Timing: sc.Timing, DelayValue: sc.DelayValue, DelayUnit: sc.DelayUnit, Enabled: sc.Enabled,
		MessageHTML: sc.MessageHTML, ImageID: sc.ImageID, Buttons: view.Buttons, Conditions: view.Conditions,
		CooldownHours: sc.CooldownHours, DailyCap: sc.DailyCap, DiscountPercent: sc.DiscountPercent,
		CouponTemplate: sc.CouponTemplate, CouponTTLHours: sc.CouponTTLHours, Position: sc.Position,
	}
}

// warmupEvaluation is a validated scenario draft.
type warmupEvaluation struct {
	fields   database.WarmupScenarioFields
	buttons  []WarmupButton
	image    *database.WarmupImage
	problems []WarmupProblem
}

func (e *warmupEvaluation) add(field, code, detail, severity string) {
	e.problems = append(e.problems, WarmupProblem{Field: field, Code: code, Detail: detail, Severity: severity})
}

func (e *warmupEvaluation) invalid(field string, err error) {
	var te *warmupTemplateError
	if errors.As(err, &te) {
		e.add(field, te.Reason, te.Detail, WarmupProblemInvalid)
		return
	}
	e.add(field, "invalid", err.Error(), WarmupProblemInvalid)
}

func (e *warmupEvaluation) firstInvalid() *WarmupFieldError {
	for _, p := range e.problems {
		if p.Severity == WarmupProblemInvalid {
			return &WarmupFieldError{Field: p.Field, Reason: p.Code, Detail: p.Detail}
		}
	}
	return nil
}

func (e *warmupEvaluation) hasInvalid() bool {
	return e.firstInvalid() != nil
}

func oneOf(v string, allowed ...string) bool {
	return slices.Contains(allowed, v)
}

// evaluateScenario normalizes and validates a draft. Problems are collected;
// an error is an infrastructure failure.
func (s *WarmupService) evaluateScenario(ctx context.Context, in WarmupScenarioInput) (*warmupEvaluation, error) {
	e := &warmupEvaluation{}
	f := database.WarmupScenarioFields{
		Category: strings.TrimSpace(in.Category), Name: strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description), TriggerType: strings.TrimSpace(in.TriggerType),
		Timing: strings.TrimSpace(in.Timing), DelayValue: in.DelayValue, DelayUnit: strings.TrimSpace(in.DelayUnit),
		Enabled: in.Enabled, MessageHTML: strings.TrimSpace(strings.ReplaceAll(in.MessageHTML, "\r\n", "\n")),
		ImageID: in.ImageID, CooldownHours: in.CooldownHours, DailyCap: in.DailyCap, DiscountPercent: in.DiscountPercent,
		CouponTemplate: strings.ToUpper(strings.TrimSpace(in.CouponTemplate)), CouponTTLHours: in.CouponTTLHours,
		Position: in.Position,
	}

	if !oneOf(f.Category, database.WarmupCategoryTrial, database.WarmupCategorySubscription, database.WarmupCategoryWinback) {
		e.add("category", "unknown", f.Category, WarmupProblemInvalid)
	}
	if f.Name == "" || !utf8.ValidString(f.Name) || utf8.RuneCountInString(f.Name) > warmupMaxNameLength {
		e.add("name", "length", "", WarmupProblemInvalid)
	}
	if !utf8.ValidString(f.Description) || utf8.RuneCountInString(f.Description) > warmupMaxDescriptionLength {
		e.add("description", "length", "", WarmupProblemInvalid)
	}
	trigger, known := warmupTriggerByType(f.TriggerType)
	if !known {
		e.add("trigger_type", "unknown", f.TriggerType, WarmupProblemInvalid)
	} else if !trigger.allowsTiming(f.Timing) {
		e.add("timing", "not_allowed", f.Timing, WarmupProblemInvalid)
	}
	if !oneOf(f.DelayUnit, database.WarmupUnitMinutes, database.WarmupUnitHours, database.WarmupUnitDays) {
		e.add("delay_unit", "unknown", f.DelayUnit, WarmupProblemInvalid)
	} else if f.DelayValue < 0 || f.DelayValue > warmupMaxDelayValue || database.WarmupDelay(f.DelayValue, f.DelayUnit) > warmupMaxDelay {
		e.add("delay_value", "out_of_range", "", WarmupProblemInvalid)
	}

	if f.CooldownHours != nil && (*f.CooldownHours < 0 || *f.CooldownHours > warmupMaxCooldownHours) {
		e.add("cooldown_hours", "out_of_range", "", WarmupProblemInvalid)
	}
	if f.DailyCap < 0 || f.DailyCap > warmupMaxDailyCap {
		e.add("daily_cap", "out_of_range", "", WarmupProblemInvalid)
	}
	if f.DiscountPercent < 0 || f.DiscountPercent > 100 {
		e.add("discount_percent", "out_of_range", "", WarmupProblemInvalid)
	}
	if !warmupCouponTemplatePattern.MatchString(f.CouponTemplate) {
		e.add("coupon_template", "format", "", WarmupProblemInvalid)
	}
	if f.CouponTTLHours < 1 || f.CouponTTLHours > warmupMaxCouponTTLHours {
		e.add("coupon_ttl_hours", "out_of_range", "", WarmupProblemInvalid)
	}
	if f.Position < -warmupMaxPosition || f.Position > warmupMaxPosition {
		e.add("position", "out_of_range", "", WarmupProblemInvalid)
	}

	if f.ImageID != nil {
		img, err := s.repo.GetWarmupImage(ctx, *f.ImageID, false)
		switch {
		case errors.Is(err, database.ErrWarmupImageNotFound):
			e.add("image_id", "not_found", "", WarmupProblemInvalid)
		case err != nil:
			return nil, err
		default:
			e.image = img
		}
	}

	buttons, err := normalizeWarmupButtons(in.Buttons)
	if err != nil {
		e.invalid("buttons", err)
	} else {
		e.buttons = buttons
		if _, hidden := visibleWarmupButtons(buttons); hidden > 0 {
			e.add("buttons", "button_url_missing", strconv.Itoa(hidden), WarmupProblemWarning)
		}
		f.Buttons, err = encodeWarmupJSON(buttons)
		if err != nil {
			return nil, fmt.Errorf("encode warmup buttons: %w", err)
		}
	}
	conditions, err := normalizeWarmupConditions(in.Conditions)
	if err != nil {
		e.invalid("conditions", err)
	} else {
		f.Conditions, err = encodeWarmupJSON(conditions)
		if err != nil {
			return nil, fmt.Errorf("encode warmup conditions: %w", err)
		}
	}

	s.checkMessage(e, &f, known)
	if known && f.Enabled {
		checkReminderOverlap(e, &f)
	}
	e.fields = f
	return e, nil
}

// checkMessage validates the HTML, the placeholders and the length rendered
// with the (long) sample values.
func (s *WarmupService) checkMessage(e *warmupEvaluation, f *database.WarmupScenarioFields, triggerKnown bool) {
	const field = "message_html"
	switch {
	case f.MessageHTML == "":
		e.add(field, "required", "", WarmupProblemInvalid)
		return
	case len(f.MessageHTML) > warmupMaxMessageBytes:
		e.add(field, "too_long", "", WarmupProblemInvalid)
		return
	}
	err := validateTelegramHTML(f.MessageHTML)
	if err != nil {
		e.invalid(field, err)
		return
	}
	names, err := warmupPlaceholders(f.MessageHTML)
	if err != nil {
		e.invalid(field, err)
		return
	}
	if !triggerKnown {
		return
	}
	discount := f.DiscountPercent > 0
	err = checkWarmupPlaceholders(names, f.TriggerType, discount)
	if err != nil {
		e.invalid(field, err)
		return
	}
	if discount && !oneOf(WarmupVarDiscountCode, names...) {
		e.add(field, "discount_code_required", "", WarmupProblemInvalid)
		return
	}
	samples := map[string]string{}
	for _, v := range warmupVariables {
		samples[v.Name] = v.Sample
	}
	rendered, err := renderWarmupTemplate(f.MessageHTML, samples)
	if err != nil {
		e.invalid(field, err)
		return
	}
	limit := warmupMaxTextLength
	if f.ImageID != nil {
		limit = warmupMaxCaptionLength
	}
	if n := warmupVisibleLength(rendered); n > limit {
		e.add(field, "too_long", fmt.Sprintf("%d/%d", n, limit), WarmupProblemInvalid)
	}
}

// checkReminderOverlap keeps the existing expiry reminders (3d/1d/3h, never
// changed by the warm-up) from being duplicated: an enabled paid-expiry
// scenario cannot fire at the lead time of one of them.
func checkReminderOverlap(e *warmupEvaluation, f *database.WarmupScenarioFields) {
	if f.TriggerType != WarmupTriggerPaidExpiry || f.Timing != database.WarmupTimingBefore {
		return
	}
	delay := database.WarmupDelay(f.DelayValue, f.DelayUnit)
	for _, w := range ExpiryReminderWindows() {
		if delay == w.LeadTime {
			e.add("delay_value", "duplicates_expiry_reminder", w.Name, WarmupProblemInvalid)
			return
		}
	}
	e.add("trigger_type", "expiry_reminders_active", "", WarmupProblemWarning)
}

// UpdateScenario validates and saves a scenario; version is the version the
// editor loaded.
func (s *WarmupService) UpdateScenario(ctx context.Context, actor, requestKey string, id uint, version int, in WarmupScenarioInput) (*database.AdminConfigResult, error) {
	e, err := s.evaluateScenario(ctx, in)
	if err != nil {
		return nil, err
	}
	fieldErr := e.firstInvalid()
	if fieldErr != nil {
		return nil, fieldErr
	}
	return s.repo.UpdateWarmupScenario(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey},
		database.WarmupScenarioUpdate{ID: id, Version: version, WarmupScenarioFields: e.fields})
}

// CreateScenario validates and adds a scenario under a new unique key.
func (s *WarmupService) CreateScenario(ctx context.Context, actor, requestKey, key string, in WarmupScenarioInput) (*database.AdminConfigResult, error) {
	key = strings.TrimSpace(key)
	if !warmupKeyPattern.MatchString(key) {
		return nil, &WarmupFieldError{Field: "key", Reason: "format"}
	}
	e, err := s.evaluateScenario(ctx, in)
	if err != nil {
		return nil, err
	}
	fieldErr := e.firstInvalid()
	if fieldErr != nil {
		return nil, fieldErr
	}
	return s.repo.CreateWarmupScenario(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey},
		database.WarmupScenarioCreate{Key: key, WarmupScenarioFields: e.fields})
}

// ---------------------------------------------------------------------------
// Rendering and preview
// ---------------------------------------------------------------------------

// WarmupRecipient is what a message is rendered for.
type WarmupRecipient struct {
	TelegramID int64
	Username   string
	PlanName   string
	ExpiresAt  *time.Time
	Coupon     *database.WarmupCoupon
}

// WarmupMessage is a rendered scenario, ready for Telegram (parse_mode HTML).
type WarmupMessage struct {
	HTML    string         `json:"html"`
	ImageID *uint          `json:"image_id"`
	Buttons []WarmupButton `json:"buttons"`
	// HiddenButtons counts URL/Mini App buttons without a URL (not sent).
	HiddenButtons int `json:"hidden_buttons"`
	// VisibleLength is the length Telegram counts; MaxLength its limit.
	VisibleLength int `json:"visible_length"`
	MaxLength     int `json:"max_length"`
}

// FormatWarmupDate formats a date for users (MSK).
func FormatWarmupDate(t time.Time) string {
	return t.In(warmupMSK).Format("02.01.2006 15:04") + " МСК"
}

// russianPlural picks the form for n: one (1), few (2-4), many (5-20, 0).
func russianPlural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

// warmupTrialDuration renders the trial length: whole days as days, else hours.
func warmupTrialDuration(hours int) (days, words string) {
	d := (hours + 23) / 24
	days = strconv.Itoa(d)
	if hours%24 == 0 {
		return days, fmt.Sprintf("%d %s", hours/24, russianPlural(hours/24, "день", "дня", "дней"))
	}
	return days, fmt.Sprintf("%d %s", hours, russianPlural(hours, "час", "часа", "часов"))
}

// recipientValues returns the variables that have a value for the recipient
// and the trigger. A variable without a value is absent, so rendering fails
// instead of sending a hole.
func (s *WarmupService) recipientValues(ctx context.Context, trigger string, r WarmupRecipient) (map[string]string, error) {
	values := map[string]string{WarmupVarTelegramID: strconv.FormatInt(r.TelegramID, 10)}
	if u := strings.TrimPrefix(strings.TrimSpace(r.Username), "@"); u != "" {
		values[WarmupVarUsername] = "@" + u
	} else {
		values[WarmupVarUsername] = "друг"
	}
	trial, err := s.repo.GetTrialEffective(ctx, s.trialDefaults)
	if err != nil {
		return nil, fmt.Errorf("warmup trial settings: %w", err)
	}
	if trial.DurationHours > 0 {
		values[WarmupVarTrialDays], values[WarmupVarTrialDuration] = warmupTrialDuration(trial.DurationHours)
	}
	if v, _ := warmupVariableByName(WarmupVarPlanName); v.availableFor(trigger) && r.PlanName != "" {
		values[WarmupVarPlanName] = r.PlanName
	}
	if v, _ := warmupVariableByName(WarmupVarExpireDate); v.availableFor(trigger) && r.ExpiresAt != nil {
		values[WarmupVarExpireDate] = FormatWarmupDate(*r.ExpiresAt)
	}
	if r.Coupon != nil {
		values[WarmupVarDiscountCode] = r.Coupon.Code
		values[WarmupVarDiscountPercent] = strconv.Itoa(r.Coupon.Percent)
	}
	return values, nil
}

// render builds the message of a validated scenario for a recipient.
func (s *WarmupService) render(ctx context.Context, f *database.WarmupScenarioFields, buttons []WarmupButton, r WarmupRecipient) (*WarmupMessage, error) {
	values, err := s.recipientValues(ctx, f.TriggerType, r)
	if err != nil {
		return nil, err
	}
	text, err := renderWarmupTemplate(f.MessageHTML, values)
	if err != nil {
		return nil, &WarmupFieldError{Field: "message_html", Reason: "variable_unavailable", Detail: err.Error()}
	}
	visible, hidden := visibleWarmupButtons(buttons)
	msg := &WarmupMessage{HTML: text, ImageID: f.ImageID, Buttons: visible, HiddenButtons: hidden,
		VisibleLength: warmupVisibleLength(text), MaxLength: warmupMaxTextLength}
	if f.ImageID != nil {
		msg.MaxLength = warmupMaxCaptionLength
	}
	if msg.VisibleLength > msg.MaxLength {
		return nil, &WarmupFieldError{Field: "message_html", Reason: "too_long", Detail: fmt.Sprintf("%d/%d", msg.VisibleLength, msg.MaxLength)}
	}
	return msg, nil
}

// RenderScenario re-validates a stored scenario against the registries and
// renders it for a recipient. Any problem is an error: nothing is sent with
// an invalid HTML, an unknown or unavailable variable, or an empty URL.
func (s *WarmupService) RenderScenario(ctx context.Context, sc *database.WarmupScenario, r WarmupRecipient) (*WarmupMessage, error) {
	view, err := scenarioView(sc)
	if err != nil {
		return nil, err
	}
	e, err := s.evaluateScenario(ctx, InputOf(view))
	if err != nil {
		return nil, err
	}
	fieldErr := e.firstInvalid()
	if fieldErr != nil {
		return nil, fieldErr
	}
	if sc.DiscountPercent > 0 && r.Coupon == nil {
		return nil, &WarmupFieldError{Field: "discount_percent", Reason: "coupon_required"}
	}
	return s.render(ctx, &e.fields, e.buttons, r)
}

// WarmupPreview is the preview of an unsaved scenario. Nothing is sent, no
// coupon is stored and no delivery is journaled: DiscountCode is a test code
// (DiscountCodeTest).
type WarmupPreview struct {
	Valid            bool                  `json:"valid"`
	Problems         []WarmupProblem       `json:"problems"`
	Message          *WarmupMessage        `json:"message"`
	Image            *database.WarmupImage `json:"image"`
	Variables        map[string]string     `json:"variables"`
	DiscountCode     string                `json:"discount_code"`
	DiscountCodeTest bool                  `json:"discount_code_test"`
}

// WarmupPreviewContext is the example recipient of a preview. Zero fields
// take example values; nothing refers to a real user.
type WarmupPreviewContext struct {
	TelegramID int64      `json:"telegram_id"`
	Username   string     `json:"username"`
	PlanName   string     `json:"plan_name"`
	ExpiresAt  *time.Time `json:"expire_date"`
}

func (pc WarmupPreviewContext) recipient(now time.Time) WarmupRecipient {
	r := WarmupRecipient{TelegramID: pc.TelegramID, Username: pc.Username, PlanName: pc.PlanName, ExpiresAt: pc.ExpiresAt}
	if r.TelegramID == 0 {
		r.TelegramID = 1234567890
	}
	if r.Username == "" {
		r.Username = "dictator_vpn_user"
	}
	if r.PlanName == "" {
		r.PlanName = "Premium 12 месяцев"
	}
	if r.ExpiresAt == nil {
		expires := now.Add(48 * time.Hour)
		r.ExpiresAt = &expires
	}
	return r
}

// Preview validates a draft and renders it with example values.
func (s *WarmupService) Preview(ctx context.Context, in WarmupScenarioInput) (*WarmupPreview, error) {
	return s.PreviewWith(ctx, in, WarmupPreviewContext{})
}

// scenarioOf is the in-memory scenario of validated fields (never stored).
func scenarioOf(f *database.WarmupScenarioFields) *database.WarmupScenario {
	return &database.WarmupScenario{
		Category: f.Category, Name: f.Name, Description: f.Description, TriggerType: f.TriggerType, Timing: f.Timing,
		DelayValue: f.DelayValue, DelayUnit: f.DelayUnit, Enabled: f.Enabled, MessageHTML: f.MessageHTML, ImageID: f.ImageID,
		Buttons: f.Buttons, Conditions: f.Conditions, CooldownHours: f.CooldownHours, DailyCap: f.DailyCap,
		DiscountPercent: f.DiscountPercent, CouponTemplate: f.CouponTemplate, CouponTTLHours: f.CouponTTLHours, Position: f.Position,
	}
}

// PreviewWith validates a draft and renders it for an example recipient with
// RenderScenario — the renderer the worker sends with.
func (s *WarmupService) PreviewWith(ctx context.Context, in WarmupScenarioInput, pc WarmupPreviewContext) (*WarmupPreview, error) {
	e, err := s.evaluateScenario(ctx, in)
	if err != nil {
		return nil, err
	}
	p := &WarmupPreview{Problems: e.problems, Image: e.image, Variables: map[string]string{}}
	if p.Problems == nil {
		p.Problems = []WarmupProblem{}
	}
	if e.hasInvalid() {
		return p, nil
	}
	recipient := pc.recipient(time.Now())
	if e.fields.DiscountPercent > 0 {
		code, err := s.couponCode(e.fields.CouponTemplate)
		if err != nil {
			return nil, err
		}
		p.DiscountCode, p.DiscountCodeTest = code, true
		recipient.Coupon = &database.WarmupCoupon{Code: code, Percent: e.fields.DiscountPercent}
	}
	values, err := s.recipientValues(ctx, e.fields.TriggerType, recipient)
	if err != nil {
		return nil, err
	}
	for _, v := range warmupVariables {
		if value, ok := values[v.Name]; ok && v.availableFor(e.fields.TriggerType) {
			p.Variables[v.Name] = value
		}
	}
	msg, err := s.RenderScenario(ctx, scenarioOf(&e.fields), recipient)
	if err != nil {
		var fieldErr *WarmupFieldError
		if errors.As(err, &fieldErr) {
			p.Problems = append(p.Problems, WarmupProblem{Field: fieldErr.Field, Code: fieldErr.Reason, Detail: fieldErr.Detail, Severity: WarmupProblemInvalid})
			//nolint:nilerr // a message that does not render is a preview finding, not a failure
			return p, nil
		}
		return nil, err
	}
	p.Message, p.Valid = msg, true
	return p, nil
}

// ---------------------------------------------------------------------------
// Coupons
// ---------------------------------------------------------------------------

// couponCode generates PREFIX-XXXXXXXX (or XXXXXXXX) from an unambiguous
// alphabet with crypto/rand.
func (s *WarmupService) couponCode(prefix string) (string, error) {
	var b strings.Builder
	if prefix != "" {
		b.WriteString(prefix)
		b.WriteByte('-')
	}
	limit := big.NewInt(int64(len(warmupCouponAlphabet)))
	for range warmupCouponRandomLength {
		n, err := rand.Int(s.random, limit)
		if err != nil {
			return "", fmt.Errorf("generate coupon code: %w", err)
		}
		b.WriteByte(warmupCouponAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// ---------------------------------------------------------------------------
// Trigger resolution
// ---------------------------------------------------------------------------

// WarmupAnchorWindow returns the anchor range whose events are due now.
//
// A scenario fires for an event when due = event ± delay is inside the
// catch-up window [now - catchup, now] AND due >= enabled_at. Everything that
// was due before the scenario was switched on is ignored, so enabling a
// scenario (or deploying it) never replays history. ok is false when nothing
// can be due (disabled, or enabled in the future).
func WarmupAnchorWindow(sc *database.WarmupScenario, catchup time.Duration, now time.Time) (from, to time.Time, ok bool) {
	if !sc.Enabled || sc.EnabledAt == nil {
		return time.Time{}, time.Time{}, false
	}
	now = now.UTC()
	dueFrom, dueTo := now.Add(-catchup), now
	if enabledAt := sc.EnabledAt.UTC(); enabledAt.After(dueFrom) {
		dueFrom = enabledAt
	}
	if dueFrom.After(dueTo) {
		return time.Time{}, time.Time{}, false
	}
	delay := sc.Delay()
	if sc.Timing == database.WarmupTimingBefore {
		return dueFrom.Add(delay), dueTo.Add(delay), true
	}
	return dueFrom.Add(-delay), dueTo.Add(-delay), true
}

// WarmupDueAt is when the scenario is due for an anchor.
func WarmupDueAt(sc *database.WarmupScenario, anchorAt time.Time) time.Time {
	if sc.Timing == database.WarmupTimingBefore {
		return anchorAt.UTC().Add(-sc.Delay())
	}
	return anchorAt.UTC().Add(sc.Delay())
}

// WarmupDue is one anchor event for which a scenario is due now.
type WarmupDue struct {
	Anchor database.WarmupAnchor
	DueAt  time.Time
}

// DueAnchors returns one page of the events the scenario is due for now.
// next is the cursor of the next page (0: done).
func (s *WarmupService) DueAnchors(ctx context.Context, sc *database.WarmupScenario, settings *database.WarmupSettings, now time.Time, cursor int64, limit int) ([]WarmupDue, int64, error) {
	from, to, ok := WarmupAnchorWindow(sc, time.Duration(settings.CatchupHours)*time.Hour, now)
	if !ok {
		return []WarmupDue{}, 0, nil
	}
	var (
		page *database.WarmupAnchorPage
		err  error
	)
	switch sc.TriggerType {
	case WarmupTriggerBotStarted:
		page, err = s.repo.WarmupContactsStartedBetween(ctx, from, to, cursor, limit)
	case WarmupTriggerTrialActivated:
		page, err = s.repo.WarmupTrialsActivatedBetween(ctx, from, to, cursor, limit)
	case WarmupTriggerTrialPeriodEnd:
		page, err = s.repo.WarmupTrialPeriodEndsBetween(ctx, from, to, cursor, limit)
	case WarmupTriggerPaidExpiry:
		page, err = s.repo.WarmupPaidExpiringBetween(ctx, from, to, cursor, limit)
	case WarmupTriggerPaidExpired:
		page, err = s.repo.WarmupPaidExpiredBetween(ctx, from, to, cursor, limit)
	default:
		return nil, 0, &WarmupFieldError{Field: "trigger_type", Reason: "unknown", Detail: sc.TriggerType}
	}
	if err != nil {
		return nil, 0, err
	}
	due := make([]WarmupDue, 0, len(page.Anchors))
	for _, a := range page.Anchors {
		if a.AnchorAt.Before(from) || a.AnchorAt.After(to) {
			continue
		}
		due = append(due, WarmupDue{Anchor: a, DueAt: WarmupDueAt(sc, a.AnchorAt)})
	}
	return due, page.Next, nil
}

// ---------------------------------------------------------------------------
// Eligibility: conditions and limits
// ---------------------------------------------------------------------------

// Reasons a due scenario is not sent (now).
const (
	WarmupSkipDisabled         = "warmup_disabled"
	WarmupSkipScenarioDisabled = "scenario_disabled"
	WarmupSkipOutsideWindow    = "outside_window"
	WarmupSkipBlocked          = "blocked"
	WarmupSkipConditions       = "conditions"
	WarmupSkipScenarioCap      = "scenario_daily_cap"
	WarmupSkipUserDailyLimit   = "user_daily_limit"
	WarmupSkipCooldown         = "cooldown"
)

// WarmupDecision is the outcome of Evaluate.
type WarmupDecision struct {
	Send   bool
	Reason string
	Facts  *database.WarmupUserFacts
}

// MatchWarmupConditions evaluates scenario conditions against user facts.
func MatchWarmupConditions(c WarmupConditions, f *database.WarmupUserFacts) bool {
	return matchWarmupFlag(c.HadTrial, f.HadTrial) &&
		matchWarmupFlag(c.HasSubscription, f.HasSubscription) &&
		matchWarmupFlag(c.HasActivePaid, f.HasActivePaid) &&
		matchWarmupFlag(c.EverPaid, f.EverPaid) &&
		matchWarmupFlag(c.Connected, f.Connected) &&
		matchWarmupLanguage(c.Languages, f.LanguageCode)
}

// Evaluate decides whether a due scenario may be sent to its user now:
// switches, the due window, blocked users, conditions, the scenario daily
// cap, the per-user daily maximum and the cooldown between messages (the
// scenario's own cooldown overrides the global one). Daily limits are rolling
// 24-hour windows. A skipped event leaves no journal row, so it is sent later
// within the window once the limits allow it.
func (s *WarmupService) Evaluate(ctx context.Context, sc *database.WarmupScenario, settings *database.WarmupSettings, due WarmupDue, now time.Time) (*WarmupDecision, error) {
	now = now.UTC()
	switch {
	case !settings.Enabled:
		return &WarmupDecision{Reason: WarmupSkipDisabled}, nil
	case !sc.Enabled || sc.EnabledAt == nil:
		return &WarmupDecision{Reason: WarmupSkipScenarioDisabled}, nil
	case due.DueAt.After(now) || due.DueAt.Before(now.Add(-time.Duration(settings.CatchupHours)*time.Hour)) ||
		due.DueAt.Before(sc.EnabledAt.UTC()):
		return &WarmupDecision{Reason: WarmupSkipOutsideWindow}, nil
	}
	conditions, err := DecodeWarmupConditions(sc.Conditions)
	if err != nil {
		return nil, fmt.Errorf("warmup scenario %d conditions: %w", sc.ID, err)
	}
	facts, err := s.repo.GetWarmupUserFacts(ctx, due.Anchor.TelegramID, now)
	if err != nil {
		return nil, err
	}
	decision := &WarmupDecision{Facts: facts}
	if facts.Blocked {
		decision.Reason = WarmupSkipBlocked
		return decision, nil
	}
	if !MatchWarmupConditions(conditions, facts) {
		decision.Reason = WarmupSkipConditions
		return decision, nil
	}
	since := now.Add(-warmupLimitWindow)
	if sc.DailyCap > 0 {
		n, err := s.repo.CountWarmupScenarioDeliveriesSince(ctx, sc.ID, since)
		if err != nil {
			return nil, err
		}
		if n >= int64(sc.DailyCap) {
			decision.Reason = WarmupSkipScenarioCap
			return decision, nil
		}
	}
	n, err := s.repo.CountWarmupUserDeliveriesSince(ctx, facts.TelegramID, since)
	if err != nil {
		return nil, err
	}
	if n >= int64(settings.MaxPerDay) {
		decision.Reason = WarmupSkipUserDailyLimit
		return decision, nil
	}
	cooldown := settings.CooldownHours
	if sc.CooldownHours != nil {
		cooldown = *sc.CooldownHours
	}
	if cooldown > 0 {
		last, err := s.repo.LastWarmupUserDeliveryAt(ctx, facts.TelegramID)
		if err != nil {
			return nil, err
		}
		if last != nil && last.After(now.Add(-time.Duration(cooldown)*time.Hour)) {
			decision.Reason = WarmupSkipCooldown
			return decision, nil
		}
	}
	decision.Send = true
	return decision, nil
}

// Claim takes the right to send a due scenario (journal row in status
// sending), issuing its personal coupon when the scenario has a discount.
// nil means the event is already handled (sent, failed, blocked, being sent
// or backing off).
func (s *WarmupService) Claim(ctx context.Context, sc *database.WarmupScenario, due WarmupDue, policy database.WarmupRetryPolicy, now time.Time) (*database.WarmupClaimResult, error) {
	now = now.UTC()
	claim := database.WarmupClaim{
		ScenarioID: sc.ID, ScenarioName: sc.Name, TelegramID: due.Anchor.TelegramID, Username: due.Anchor.Username,
		AnchorKey: due.Anchor.AnchorKey, AnchorAt: due.Anchor.AnchorAt, DueAt: due.DueAt,
	}
	for attempt := 1; ; attempt++ {
		if sc.DiscountPercent > 0 {
			code, err := s.couponCode(sc.CouponTemplate)
			if err != nil {
				return nil, err
			}
			claim.Coupon = &database.WarmupCouponDraft{
				Code: code, Percent: sc.DiscountPercent, ExpiresAt: now.Add(time.Duration(sc.CouponTTLHours) * time.Hour),
			}
		}
		result, err := s.repo.ClaimWarmupDelivery(ctx, claim, policy, now)
		if errors.Is(err, database.ErrWarmupCouponCodeTaken) && attempt < warmupCouponAttempts {
			continue
		}
		return result, err
	}
}

// ---------------------------------------------------------------------------
// Contacts and images
// ---------------------------------------------------------------------------

// TouchContact records a live /start (or any later message) of a user.
func (s *WarmupService) TouchContact(ctx context.Context, telegramID int64, username, languageCode string) error {
	return s.repo.TouchWarmupContact(ctx, telegramID, strings.TrimPrefix(strings.TrimSpace(username), "@"),
		strings.ToLower(strings.TrimSpace(languageCode)), time.Now().UTC())
}

// Telegram photo constraints (sendPhoto): width + height <= 10000 and an
// aspect ratio of at most 20.
const (
	warmupMaxImageSides = 10000
	warmupMaxImageRatio = 20
)

// validateWarmupImage checks an upload: at most 2 MB, a decodable JPEG or PNG
// (sniffed from the bytes, never taken from the client) that Telegram
// accepts as a photo. It returns the MIME type.
func validateWarmupImage(data []byte) (string, error) {
	if len(data) == 0 {
		return "", &WarmupFieldError{Field: "image", Reason: "required"}
	}
	if len(data) > database.WarmupImageMaxBytes {
		return "", &WarmupFieldError{Field: "image", Reason: "too_large"}
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" {
		return "", &WarmupFieldError{Field: "image", Reason: "unsupported_type", Detail: mime}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != mime {
		return "", &WarmupFieldError{Field: "image", Reason: "corrupt"}
	}
	w, h := cfg.Width, cfg.Height
	if w <= 0 || h <= 0 || w+h > warmupMaxImageSides || w > h*warmupMaxImageRatio || h > w*warmupMaxImageRatio {
		return "", &WarmupFieldError{Field: "image", Reason: "dimensions"}
	}
	return mime, nil
}

// UploadImage validates and stores a picture (audited, idempotent by request
// key). The same bytes resolve to the existing image.
func (s *WarmupService) UploadImage(ctx context.Context, actor, requestKey string, data []byte) (*WarmupImageOutcome, error) {
	mime, err := validateWarmupImage(data)
	if err != nil {
		return nil, err
	}
	result, err := s.repo.UploadWarmupImage(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, mime, data)
	if err != nil {
		return nil, err
	}
	img, err := s.repo.GetWarmupImage(ctx, result.TargetID, false)
	if err != nil {
		return nil, err
	}
	return &WarmupImageOutcome{Image: img, TargetID: result.TargetID, Replayed: result.Replayed, Audit: result.Audit}, nil
}
