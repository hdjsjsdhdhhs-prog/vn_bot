package service

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// Browser-admin boundary of the customer warm-up (Admin → «Прогрев
// клиентов»). Like TrialService it owns no authentication: the web layer runs
// behind adminauth (session, Origin, CSRF). Every mutation goes through
// runAdminConfigMutation in the database layer (audit log, request-key
// idempotency) and saves of a scenario or the settings are optimistic-locked
// by version.

// WarmupScenarioOutcome is returned by scenario mutations, also on replay.
type WarmupScenarioOutcome struct {
	Scenario *WarmupScenarioView    `json:"scenario"`
	TargetID uint                   `json:"target_id"`
	Replayed bool                   `json:"replayed"`
	Audit    database.AdminAuditLog `json:"audit"`
}

// WarmupSettingsOutcome is returned by a settings save, also on replay.
type WarmupSettingsOutcome struct {
	Settings *database.WarmupSettings `json:"settings"`
	Replayed bool                     `json:"replayed"`
	Audit    database.AdminAuditLog   `json:"audit"`
}

// WarmupImageOutcome is returned by image mutations, also on replay. Image is
// nil after a delete.
type WarmupImageOutcome struct {
	Image    *database.WarmupImage  `json:"image"`
	TargetID uint                   `json:"target_id"`
	Replayed bool                   `json:"replayed"`
	Audit    database.AdminAuditLog `json:"audit"`
}

// ---------------------------------------------------------------------------
// Overview
// ---------------------------------------------------------------------------

// WarmupCategory is one admin tab with its scenario counts.
type WarmupCategory struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Scenarios int    `json:"scenarios"`
	Enabled   int    `json:"enabled"`
}

var warmupCategoryLabels = []struct{ key, label string }{
	{database.WarmupCategoryTrial, "Пробный период"},
	{database.WarmupCategorySubscription, "Подписка"},
	{database.WarmupCategoryWinback, "Возврат клиента"},
}

// WarmupRegistry is what scenarios may reference (for the editor).
type WarmupRegistry struct {
	Triggers   []WarmupTrigger        `json:"triggers"`
	Variables  []WarmupVariable       `json:"variables"`
	Callbacks  []WarmupCallbackAction `json:"callbacks"`
	Conditions []WarmupConditionKey   `json:"conditions"`
	Limits     WarmupLimits           `json:"limits"`
}

// WarmupLimits are the editor bounds.
type WarmupLimits struct {
	ImageMaxBytes    int `json:"image_max_bytes"`
	MaxButtons       int `json:"max_buttons"`
	MaxTextLength    int `json:"max_text_length"`
	MaxCaptionLength int `json:"max_caption_length"`
	MaxCooldownHours int `json:"max_cooldown_hours"`
	MaxDailyCap      int `json:"max_daily_cap"`
	MaxCouponTTL     int `json:"max_coupon_ttl_hours"`
	MaxPerDayLimit   int `json:"max_per_day_limit"`
	MaxCatchupHours  int `json:"max_catchup_hours"`
}

// Registry returns the code registries and bounds.
func (s *WarmupService) Registry() WarmupRegistry {
	return WarmupRegistry{
		Triggers: WarmupTriggers(), Variables: WarmupVariables(), Callbacks: WarmupCallbackActions(), Conditions: WarmupConditionKeys(),
		Limits: WarmupLimits{
			ImageMaxBytes: database.WarmupImageMaxBytes, MaxButtons: warmupMaxButtons, MaxTextLength: warmupMaxTextLength,
			MaxCaptionLength: warmupMaxCaptionLength, MaxCooldownHours: warmupMaxCooldownHours, MaxDailyCap: warmupMaxDailyCap,
			MaxCouponTTL: warmupMaxCouponTTLHours, MaxPerDayLimit: warmupMaxMaxPerDay, MaxCatchupHours: warmupMaxCatchupHours,
		},
	}
}

// WarmupOverview is the admin page state.
type WarmupOverview struct {
	Settings   *database.WarmupSettings `json:"settings"`
	Categories []WarmupCategory         `json:"categories"`
	Scenarios  []WarmupScenarioView     `json:"scenarios"`
	Summary    *database.WarmupSummary  `json:"summary"`
	Registry   WarmupRegistry           `json:"registry"`
}

// Overview loads settings, categories, every scenario, the summary and the
// registries.
func (s *WarmupService) Overview(ctx context.Context) (*WarmupOverview, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	scenarios, err := s.ListScenarios(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := s.repo.GetWarmupSummary(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	categories := make([]WarmupCategory, 0, len(warmupCategoryLabels))
	for _, c := range warmupCategoryLabels {
		cat := WarmupCategory{Key: c.key, Label: c.label}
		for _, sc := range scenarios {
			if sc.Category == c.key {
				cat.Scenarios++
				if sc.Enabled {
					cat.Enabled++
				}
			}
		}
		categories = append(categories, cat)
	}
	return &WarmupOverview{Settings: settings, Categories: categories, Scenarios: scenarios, Summary: summary, Registry: s.Registry()}, nil
}

// WarmupScenarioFilter narrows ListScenariosFiltered; zero values match all.
// Order is "position" (admin order, default), "name" or "updated".
type WarmupScenarioFilter struct {
	Category string
	Enabled  *bool
	Trigger  string
	Order    string
}

// ListScenariosFiltered returns the scenarios matching the filter.
func (s *WarmupService) ListScenariosFiltered(ctx context.Context, f WarmupScenarioFilter) ([]WarmupScenarioView, error) {
	switch f.Order {
	case "", "position", "name", "updated":
	default:
		return nil, &WarmupFieldError{Field: "order", Reason: "unknown"}
	}
	all, err := s.ListScenarios(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WarmupScenarioView, 0, len(all))
	for _, sc := range all {
		if (f.Category == "" || sc.Category == f.Category) && (f.Trigger == "" || sc.TriggerType == f.Trigger) &&
			(f.Enabled == nil || sc.Enabled == *f.Enabled) {
			out = append(out, sc)
		}
	}
	switch f.Order {
	case "name":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	case "updated":
		sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	}
	return out, nil
}

// Scenario returns one scenario with its JSON documents decoded.
func (s *WarmupService) Scenario(ctx context.Context, id uint) (*WarmupScenarioView, error) {
	sc, err := s.repo.GetWarmupScenario(ctx, id)
	if err != nil {
		return nil, err
	}
	return scenarioView(sc)
}

// ---------------------------------------------------------------------------
// Partial scenario edit
// ---------------------------------------------------------------------------

// Nullable is a JSON field that distinguishes "absent" (Set false) from an
// explicit null (Set true, Value nil).
type Nullable[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON is only called for a present key.
func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Value = nil
		return nil
	}
	var v T
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// MarshalJSON keeps the request hash stable (absent and null both encode).
func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if !n.Set || n.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*n.Value)
}

// WarmupScenarioPatch is a partial edit: absent fields keep the stored
// value. ImageID and CooldownHours accept null (no picture; global cooldown).
type WarmupScenarioPatch struct {
	Category        *string            `json:"category,omitempty"`
	Name            *string            `json:"name,omitempty"`
	Description     *string            `json:"description,omitempty"`
	TriggerType     *string            `json:"trigger_type,omitempty"`
	Timing          *string            `json:"timing,omitempty"`
	DelayValue      *int               `json:"delay_value,omitempty"`
	DelayUnit       *string            `json:"delay_unit,omitempty"`
	Enabled         *bool              `json:"enabled,omitempty"`
	MessageHTML     *string            `json:"message_html,omitempty"`
	ImageID         Nullable[uint]     `json:"image_id"`
	Buttons         *[]WarmupButton    `json:"buttons,omitempty"`
	Conditions      *WarmupConditions  `json:"conditions,omitempty"`
	CooldownHours   Nullable[int]      `json:"cooldown_hours"`
	DailyCap        *int               `json:"daily_cap,omitempty"`
	DiscountPercent *int               `json:"discount_percent,omitempty"`
	CouponTemplate  *string            `json:"coupon_template,omitempty"`
	CouponTTLHours  *int               `json:"coupon_ttl_hours,omitempty"`
	Position        *int               `json:"position,omitempty"`
}

// apply merges the patch into the stored input.
func (p *WarmupScenarioPatch) apply(in *WarmupScenarioInput) {
	setIf(&in.Category, p.Category)
	setIf(&in.Name, p.Name)
	setIf(&in.Description, p.Description)
	setIf(&in.TriggerType, p.TriggerType)
	setIf(&in.Timing, p.Timing)
	setIf(&in.DelayValue, p.DelayValue)
	setIf(&in.DelayUnit, p.DelayUnit)
	setIf(&in.Enabled, p.Enabled)
	setIf(&in.MessageHTML, p.MessageHTML)
	setIf(&in.Buttons, p.Buttons)
	setIf(&in.Conditions, p.Conditions)
	setIf(&in.DailyCap, p.DailyCap)
	setIf(&in.DiscountPercent, p.DiscountPercent)
	setIf(&in.CouponTemplate, p.CouponTemplate)
	setIf(&in.CouponTTLHours, p.CouponTTLHours)
	setIf(&in.Position, p.Position)
	if p.ImageID.Set {
		in.ImageID = p.ImageID.Value
	}
	if p.CooldownHours.Set {
		in.CooldownHours = p.CooldownHours.Value
	}
}

func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// empty reports a patch that changes nothing.
func (p *WarmupScenarioPatch) empty() bool {
	return p.Category == nil && p.Name == nil && p.Description == nil && p.TriggerType == nil && p.Timing == nil &&
		p.DelayValue == nil && p.DelayUnit == nil && p.Enabled == nil && p.MessageHTML == nil && !p.ImageID.Set &&
		p.Buttons == nil && p.Conditions == nil && !p.CooldownHours.Set && p.DailyCap == nil && p.DiscountPercent == nil &&
		p.CouponTemplate == nil && p.CouponTTLHours == nil && p.Position == nil
}

// auditAction labels the audit row from the patch alone (never from the
// stored state, so a replay hashes the same): a switch-only edit is
// enabled/disabled, a coupon-only edit is coupon_settings_changed.
func (p *WarmupScenarioPatch) auditAction() database.AdminAction {
	only := *p
	only.Enabled = nil
	if p.Enabled != nil && only.empty() {
		if *p.Enabled {
			return database.AdminActionWarmupScenarioEnabled
		}
		return database.AdminActionWarmupScenarioDisable
	}
	coupon := *p
	coupon.DiscountPercent, coupon.CouponTemplate, coupon.CouponTTLHours = nil, nil, nil
	if coupon.empty() && !p.empty() {
		return database.AdminActionWarmupCouponChanged
	}
	return database.AdminActionWarmupScenarioUpdated
}

// warmupPatchRequest is what a patch's request key is bound to.
type warmupPatchRequest struct {
	ID      uint                `json:"id"`
	Version int                 `json:"version"`
	Patch   WarmupScenarioPatch `json:"patch"`
}

// PatchScenario applies a partial edit. The patch is merged into the stored
// scenario (at the version the editor loaded), validated like a full save
// and written with optimistic locking: a stale version is
// ErrWarmupVersionConflict, nothing is overwritten.
func (s *WarmupService) PatchScenario(ctx context.Context, actor, requestKey string, id uint, version int, patch WarmupScenarioPatch) (*WarmupScenarioOutcome, error) {
	if version < 1 {
		return nil, &WarmupFieldError{Field: "version", Reason: "required"}
	}
	if patch.empty() {
		return nil, &WarmupFieldError{Field: "patch", Reason: "empty"}
	}
	view, err := s.Scenario(ctx, id)
	if err != nil {
		return nil, err
	}
	in := InputOf(view)
	patch.apply(&in)
	e, err := s.evaluateScenario(ctx, in)
	if err != nil {
		return nil, err
	}
	fieldErr := e.firstInvalid()
	if fieldErr != nil {
		return nil, fieldErr
	}
	result, err := s.repo.UpdateWarmupScenario(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, database.WarmupScenarioUpdate{
		WarmupScenarioFields: e.fields, ID: id, Version: version, Action: patch.auditAction(),
		Request: warmupPatchRequest{ID: id, Version: version, Patch: patch},
	})
	if err != nil {
		return nil, err
	}
	return s.scenarioOutcome(ctx, result)
}

// CreateScenarioOutcome adds a scenario and returns it.
func (s *WarmupService) CreateScenarioOutcome(ctx context.Context, actor, requestKey, key string, in WarmupScenarioInput) (*WarmupScenarioOutcome, error) {
	result, err := s.CreateScenario(ctx, actor, requestKey, key, in)
	if err != nil {
		return nil, err
	}
	return s.scenarioOutcome(ctx, result)
}

func (s *WarmupService) scenarioOutcome(ctx context.Context, result *database.AdminConfigResult) (*WarmupScenarioOutcome, error) {
	out := &WarmupScenarioOutcome{TargetID: result.TargetID, Replayed: result.Replayed, Audit: result.Audit}
	view, err := s.Scenario(ctx, result.TargetID)
	if err != nil {
		return out, err
	}
	out.Scenario = view
	return out, nil
}

// PreviewScenario previews a stored scenario with an optional unsaved patch
// for an example recipient. Nothing is written.
func (s *WarmupService) PreviewScenario(ctx context.Context, id uint, patch WarmupScenarioPatch, pc WarmupPreviewContext) (*WarmupPreview, error) {
	view, err := s.Scenario(ctx, id)
	if err != nil {
		return nil, err
	}
	in := InputOf(view)
	patch.apply(&in)
	return s.PreviewWith(ctx, in, pc)
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// WarmupSettingsSave is a settings save; a nil CatchupHours keeps the stored
// catch-up window.
type WarmupSettingsSave struct {
	Version       int  `json:"version"`
	Enabled       bool `json:"enabled"`
	MaxPerDay     int  `json:"max_per_day"`
	CooldownHours int  `json:"cooldown_hours"`
	CatchupHours  *int `json:"catchup_hours"`
}

// SaveSettings validates and saves the global settings and returns them.
func (s *WarmupService) SaveSettings(ctx context.Context, actor, requestKey string, in WarmupSettingsSave) (*WarmupSettingsOutcome, error) {
	input := database.WarmupSettingsInput{Version: in.Version, Enabled: in.Enabled, MaxPerDay: in.MaxPerDay, CooldownHours: in.CooldownHours}
	if in.CatchupHours != nil {
		input.CatchupHours = *in.CatchupHours
	} else {
		current, err := s.Settings(ctx)
		if err != nil {
			return nil, err
		}
		input.CatchupHours = current.CatchupHours
	}
	result, err := s.UpdateSettings(ctx, actor, requestKey, input)
	if err != nil {
		return nil, err
	}
	out := &WarmupSettingsOutcome{Replayed: result.Replayed, Audit: result.Audit}
	out.Settings, err = s.Settings(ctx)
	return out, err
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

// Image returns a picture; withData also loads the bytes (for viewing).
func (s *WarmupService) Image(ctx context.Context, id uint, withData bool) (*database.WarmupImage, error) {
	return s.repo.GetWarmupImage(ctx, id, withData)
}

// DeleteImage removes a picture no scenario uses (audited, idempotent).
func (s *WarmupService) DeleteImage(ctx context.Context, actor, requestKey string, id uint) (*WarmupImageOutcome, error) {
	result, err := s.repo.DeleteWarmupImage(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey}, id)
	if err != nil {
		return nil, err
	}
	return &WarmupImageOutcome{TargetID: result.TargetID, Replayed: result.Replayed, Audit: result.Audit}, nil
}

// ---------------------------------------------------------------------------
// Journal and coupons
// ---------------------------------------------------------------------------

var warmupDeliveryStatuses = []string{
	database.WarmupDeliverySending, database.WarmupDeliverySent, database.WarmupDeliveryRetry,
	database.WarmupDeliveryFailed, database.WarmupDeliveryBlocked,
}

// WarmupDeliveryPage is one page of the delivery journal.
type WarmupDeliveryPage struct {
	Deliveries []database.WarmupDeliveryRow `json:"deliveries"`
	Total      int64                        `json:"total"`
	Limit      int                          `json:"limit"`
	Offset     int                          `json:"offset"`
}

// Deliveries pages the delivery journal (filters validated).
func (s *WarmupService) Deliveries(ctx context.Context, f database.WarmupDeliveryFilter) (*WarmupDeliveryPage, error) {
	if f.Status != "" && !oneOf(f.Status, warmupDeliveryStatuses...) {
		return nil, &WarmupFieldError{Field: "status", Reason: "unknown"}
	}
	if f.Category != "" && !oneOf(f.Category, database.WarmupCategoryTrial, database.WarmupCategorySubscription, database.WarmupCategoryWinback) {
		return nil, &WarmupFieldError{Field: "category", Reason: "unknown"}
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return nil, &WarmupFieldError{Field: "to", Reason: "before_from"}
	}
	f.CouponCode = strings.ToUpper(strings.TrimSpace(f.CouponCode))
	rows, total, err := s.repo.ListWarmupDeliveries(ctx, f)
	if err != nil {
		return nil, err
	}
	limit, offset := warmupPageBounds(f.Limit, f.Offset)
	return &WarmupDeliveryPage{Deliveries: rows, Total: total, Limit: limit, Offset: offset}, nil
}

// WarmupCouponRow is a coupon with its effective status. RedeemedAt/OrderID
// are prepared fields: coupons are not connected to payments yet.
type WarmupCouponRow struct {
	database.WarmupCoupon

	EffectiveStatus string `json:"effective_status"`
}

// WarmupCouponPage is one page of issued coupons.
type WarmupCouponPage struct {
	Coupons []WarmupCouponRow `json:"coupons"`
	Total   int64             `json:"total"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
}

// Coupons pages the issued coupons (filters validated).
func (s *WarmupService) Coupons(ctx context.Context, f database.WarmupCouponFilter) (*WarmupCouponPage, error) {
	if f.Status != "" && !oneOf(f.Status, database.WarmupCouponIssued, database.WarmupCouponExpired,
		database.WarmupCouponRevoked, database.WarmupCouponRedeemed) {
		return nil, &WarmupFieldError{Field: "status", Reason: "unknown"}
	}
	if f.Now.IsZero() {
		f.Now = time.Now()
	}
	f.Code = strings.ToUpper(strings.TrimSpace(f.Code))
	coupons, total, err := s.repo.ListWarmupCoupons(ctx, f)
	if err != nil {
		return nil, err
	}
	limit, offset := warmupPageBounds(f.Limit, f.Offset)
	page := &WarmupCouponPage{Coupons: make([]WarmupCouponRow, 0, len(coupons)), Total: total, Limit: limit, Offset: offset}
	for _, c := range coupons {
		page.Coupons = append(page.Coupons, WarmupCouponRow{WarmupCoupon: c, EffectiveStatus: c.EffectiveStatus(f.Now)})
	}
	return page, nil
}

// warmupPageBounds mirrors the repository paging bounds for the response.
func warmupPageBounds(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = database.AdminDefaultPageSize
	}
	if limit > database.AdminMaxPageSize {
		limit = database.AdminMaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
