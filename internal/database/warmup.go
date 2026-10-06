package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Customer warm-up (migration 050, Admin → «Прогрев клиентов»). This file is
// the persistence of the configuration: global settings, scenarios, images
// and the contacts the bot has seen. Trigger anchors, user facts, the delivery
// journal and coupons live in warmup_deliveries.go.
//
// The database layer stores what the service validated: HTML, placeholders,
// buttons and conditions are checked by service.WarmupService against the
// code registries (triggers, variables, callbacks). Bounds that the schema
// enforces with CHECK constraints are not repeated here.

// Scenario categories (admin tabs).
const (
	WarmupCategoryTrial        = "trial"
	WarmupCategorySubscription = "subscription"
	WarmupCategoryWinback      = "winback"
)

// Scenario timing relative to the anchor event.
const (
	WarmupTimingAfter  = "after"
	WarmupTimingBefore = "before"
)

// Delay units.
const (
	WarmupUnitMinutes = "minutes"
	WarmupUnitHours   = "hours"
	WarmupUnitDays    = "days"
)

// Contact sources: only a live /start is a real "bot started" event.
const (
	WarmupContactSourceStart    = "start"
	WarmupContactSourceBackfill = "backfill"
)

// WarmupImageMaxBytes is the upload limit (schema CHECK mirrors it).
const WarmupImageMaxBytes = 2 << 20

// Audited warm-up actions and targets.
const (
	AdminActionWarmupSettingsUpdated AdminAction = "warmup_settings_updated"
	AdminActionWarmupScenarioCreated AdminAction = "warmup_scenario_created"
	AdminActionWarmupScenarioUpdated AdminAction = "warmup_scenario_updated"
	AdminActionWarmupScenarioEnabled AdminAction = "warmup_scenario_enabled"
	AdminActionWarmupScenarioDisable AdminAction = "warmup_scenario_disabled"
	AdminActionWarmupCouponChanged   AdminAction = "warmup_coupon_settings_changed"
	AdminActionWarmupImageUploaded   AdminAction = "warmup_image_uploaded"
	AdminActionWarmupImageDeleted    AdminAction = "warmup_image_deleted"
	AdminTargetWarmupSettings                    = "warmup_settings"
	AdminTargetWarmupScenario                    = "warmup_scenario"
	AdminTargetWarmupImage                       = "warmup_image"
)

const warmupSettingsID = 1

var (
	ErrWarmupScenarioNotFound = errors.New("warmup scenario not found")
	ErrWarmupVersionConflict  = errors.New("warmup configuration was changed concurrently")
	ErrWarmupScenarioKeyTaken = errors.New("warmup scenario key is already used")
	ErrWarmupImageNotFound    = errors.New("warmup image not found")
)

// WarmupSettings is the only row of warmup_settings.
type WarmupSettings struct {
	ID            uint      `gorm:"primaryKey;column:id"           json:"-"`
	Enabled       bool      `gorm:"not null;column:enabled"        json:"enabled"`
	MaxPerDay     int       `gorm:"not null;column:max_per_day"    json:"max_per_day"`
	CooldownHours int       `gorm:"not null;column:cooldown_hours" json:"cooldown_hours"`
	CatchupHours  int       `gorm:"not null;column:catchup_hours"  json:"catchup_hours"`
	Version       int       `gorm:"not null;column:version"        json:"version"`
	UpdatedAt     time.Time `gorm:"not null;column:updated_at"     json:"updated_at"`
}

func (WarmupSettings) TableName() string { return "warmup_settings" }

// WarmupScenario is one row of warmup_scenarios. Buttons and Conditions are
// JSON documents owned by the service.
type WarmupScenario struct {
	ID              uint       `gorm:"primaryKey;column:id"             json:"id"`
	Key             string     `gorm:"not null;column:key"              json:"key"`
	Category        string     `gorm:"not null;column:category"         json:"category"`
	Name            string     `gorm:"not null;column:name"             json:"name"`
	Description     string     `gorm:"not null;column:description"      json:"description"`
	TriggerType     string     `gorm:"not null;column:trigger_type"     json:"trigger_type"`
	Timing          string     `gorm:"not null;column:timing"           json:"timing"`
	DelayValue      int        `gorm:"not null;column:delay_value"      json:"delay_value"`
	DelayUnit       string     `gorm:"not null;column:delay_unit"       json:"delay_unit"`
	Enabled         bool       `gorm:"not null;column:enabled"          json:"enabled"`
	EnabledAt       *time.Time `gorm:"column:enabled_at"                json:"enabled_at"`
	MessageHTML     string     `gorm:"not null;column:message_html"     json:"message_html"`
	ImageID         *uint      `gorm:"column:image_id"                  json:"image_id"`
	Buttons         string     `gorm:"not null;column:buttons"          json:"-"`
	Conditions      string     `gorm:"not null;column:conditions"       json:"-"`
	CooldownHours   *int       `gorm:"column:cooldown_hours"            json:"cooldown_hours"`
	DailyCap        int        `gorm:"not null;column:daily_cap"        json:"daily_cap"`
	DiscountPercent int        `gorm:"not null;column:discount_percent" json:"discount_percent"`
	CouponTemplate  string     `gorm:"not null;column:coupon_template"  json:"coupon_template"`
	CouponTTLHours  int        `gorm:"not null;column:coupon_ttl_hours" json:"coupon_ttl_hours"`
	Position        int        `gorm:"not null;column:position"         json:"position"`
	Version         int        `gorm:"not null;column:version"          json:"version"`
	CreatedAt       time.Time  `gorm:"not null;column:created_at"       json:"created_at"`
	UpdatedAt       time.Time  `gorm:"not null;column:updated_at"       json:"updated_at"`
}

func (WarmupScenario) TableName() string { return "warmup_scenarios" }

// Delay is the configured offset from the anchor event.
func (sc *WarmupScenario) Delay() time.Duration {
	return WarmupDelay(sc.DelayValue, sc.DelayUnit)
}

// WarmupDelay converts a stored delay to a duration (unknown unit: 0).
func WarmupDelay(value int, unit string) time.Duration {
	switch unit {
	case WarmupUnitMinutes:
		return time.Duration(value) * time.Minute
	case WarmupUnitHours:
		return time.Duration(value) * time.Hour
	case WarmupUnitDays:
		return time.Duration(value) * 24 * time.Hour
	default:
		return 0
	}
}

// WarmupImage is an uploaded picture. Data is loaded only when sending.
type WarmupImage struct {
	ID        uint      `gorm:"primaryKey;column:id"       json:"id"`
	Mime      string    `gorm:"not null;column:mime"       json:"mime"`
	SizeBytes int       `gorm:"not null;column:size_bytes" json:"size_bytes"`
	SHA256    string    `gorm:"not null;column:sha256"     json:"sha256"`
	Data      []byte    `gorm:"not null;column:data"       json:"-"`
	TGFileID  string    `gorm:"not null;column:tg_file_id" json:"-"`
	CreatedAt time.Time `gorm:"not null;column:created_at" json:"created_at"`
}

func (WarmupImage) TableName() string { return "warmup_images" }

// WarmupContact is a Telegram user the bot has seen.
type WarmupContact struct {
	TelegramID   int64      `gorm:"primaryKey;autoIncrement:false;column:telegram_id" json:"telegram_id"`
	Username     string     `gorm:"not null;column:username"                          json:"username"`
	LanguageCode string     `gorm:"not null;column:language_code"                     json:"language_code"`
	Source       string     `gorm:"not null;column:source"                            json:"source"`
	FirstSeenAt  time.Time  `gorm:"not null;column:first_seen_at"                     json:"first_seen_at"`
	LastSeenAt   time.Time  `gorm:"not null;column:last_seen_at"                      json:"last_seen_at"`
	BlockedAt    *time.Time `gorm:"column:blocked_at"                                 json:"blocked_at"`
}

func (WarmupContact) TableName() string { return "warmup_contacts" }

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// GetWarmupSettings returns the global settings row (created by migration 050).
func (s *Service) GetWarmupSettings(ctx context.Context) (*WarmupSettings, error) {
	var row WarmupSettings
	err := s.db.WithContext(ctx).First(&row, warmupSettingsID).Error
	if err != nil {
		return nil, fmt.Errorf("load warmup settings: %w", err)
	}
	return &row, nil
}

// WarmupSettingsInput is a save of the global settings. Version is the
// version the editor loaded.
type WarmupSettingsInput struct {
	Version       int  `json:"version"`
	Enabled       bool `json:"enabled"`
	MaxPerDay     int  `json:"max_per_day"`
	CooldownHours int  `json:"cooldown_hours"`
	CatchupHours  int  `json:"catchup_hours"`
}

// UpdateWarmupSettings saves the global settings (audited, idempotent by
// request key, optimistic locking by version).
//
// Switching the warm-up on (OFF -> ON) also moves enabled_at of every enabled
// scenario to now, in the same transaction: events that became due while the
// warm-up was off (inside the catch-up window) are history and must not be
// sent on the switch-on. Scenario switches, content and versions are kept.
func (s *Service) UpdateWarmupSettings(ctx context.Context, meta AdminConfigMeta, in WarmupSettingsInput) (*AdminConfigResult, error) {
	return s.runAdminConfigMutation(ctx, meta, AdminActionWarmupSettingsUpdated, AdminTargetWarmupSettings, warmupSettingsID, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			var old WarmupSettings
			err := tx.First(&old, warmupSettingsID).Error
			if err != nil {
				return configChange{}, fmt.Errorf("load warmup settings: %w", err)
			}
			res := tx.Model(&WarmupSettings{}).Where("id = ? AND version = ?", warmupSettingsID, in.Version).Updates(map[string]any{
				"enabled": in.Enabled, "max_per_day": in.MaxPerDay, "cooldown_hours": in.CooldownHours,
				"catchup_hours": in.CatchupHours, "version": in.Version + 1, "updated_at": now,
			})
			if res.Error != nil {
				return configChange{}, fmt.Errorf("update warmup settings: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return configChange{}, ErrWarmupVersionConflict
			}
			if !old.Enabled && in.Enabled {
				err = tx.Model(&WarmupScenario{}).Where("enabled = ?", true).Update("enabled_at", now).Error
				if err != nil {
					return configChange{}, fmt.Errorf("reset warmup scenarios enabled_at: %w", err)
				}
			}
			var saved WarmupSettings
			err = tx.First(&saved, warmupSettingsID).Error
			if err != nil {
				return configChange{}, fmt.Errorf("reload warmup settings: %w", err)
			}
			return configChange{targetID: warmupSettingsID, oldValue: old, newValue: saved}, nil
		})
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

// warmupScenarioOrder lists scenarios tab by tab (trial, subscription,
// winback), then by position.
const warmupScenarioOrder = "CASE category WHEN 'trial' THEN 0 WHEN 'subscription' THEN 1 ELSE 2 END, position ASC, id ASC"

// ListWarmupScenarios returns every scenario in admin order.
func (s *Service) ListWarmupScenarios(ctx context.Context) ([]WarmupScenario, error) {
	scenarios := []WarmupScenario{}
	err := s.db.WithContext(ctx).Order(warmupScenarioOrder).Find(&scenarios).Error
	if err != nil {
		return nil, fmt.Errorf("list warmup scenarios: %w", err)
	}
	return scenarios, nil
}

// ListEnabledWarmupScenarios returns the scenarios the worker runs.
func (s *Service) ListEnabledWarmupScenarios(ctx context.Context) ([]WarmupScenario, error) {
	scenarios := []WarmupScenario{}
	err := s.db.WithContext(ctx).Where("enabled = ?", true).Order(warmupScenarioOrder).Find(&scenarios).Error
	if err != nil {
		return nil, fmt.Errorf("list enabled warmup scenarios: %w", err)
	}
	return scenarios, nil
}

// GetWarmupScenario loads one scenario.
func (s *Service) GetWarmupScenario(ctx context.Context, id uint) (*WarmupScenario, error) {
	var sc WarmupScenario
	err := s.db.WithContext(ctx).First(&sc, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWarmupScenarioNotFound
		}
		return nil, fmt.Errorf("get warmup scenario: %w", err)
	}
	return &sc, nil
}

// WarmupScenarioFields is every editable column of a scenario, already
// validated and normalized by the service (Buttons/Conditions are JSON).
type WarmupScenarioFields struct {
	Category        string `json:"category"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	TriggerType     string `json:"trigger_type"`
	Timing          string `json:"timing"`
	DelayValue      int    `json:"delay_value"`
	DelayUnit       string `json:"delay_unit"`
	Enabled         bool   `json:"enabled"`
	MessageHTML     string `json:"message_html"`
	ImageID         *uint  `json:"image_id"`
	Buttons         string `json:"buttons"`
	Conditions      string `json:"conditions"`
	CooldownHours   *int   `json:"cooldown_hours"`
	DailyCap        int    `json:"daily_cap"`
	DiscountPercent int    `json:"discount_percent"`
	CouponTemplate  string `json:"coupon_template"`
	CouponTTLHours  int    `json:"coupon_ttl_hours"`
	Position        int    `json:"position"`
}

func (f WarmupScenarioFields) columns() map[string]any {
	return map[string]any{
		"category": f.Category, "name": f.Name, "description": f.Description, "trigger_type": f.TriggerType,
		"timing": f.Timing, "delay_value": f.DelayValue, "delay_unit": f.DelayUnit, "enabled": f.Enabled,
		"message_html": f.MessageHTML, "image_id": f.ImageID, "buttons": f.Buttons, "conditions": f.Conditions,
		"cooldown_hours": f.CooldownHours, "daily_cap": f.DailyCap, "discount_percent": f.DiscountPercent,
		"coupon_template": f.CouponTemplate, "coupon_ttl_hours": f.CouponTTLHours, "position": f.Position,
	}
}

// warmupEnabledAt decides the enabled_at of a saved scenario. enabled_at is
// the switch-on moment: events due before it are never sent. It is (re)set to
// now when the scenario is switched on, and when the schedule of an enabled
// scenario changes (trigger, timing or delay) — a new schedule must not
// replay the history that the old one did not cover. It is cleared when the
// scenario is switched off.
func warmupEnabledAt(old *WarmupScenario, f WarmupScenarioFields, now time.Time) *time.Time {
	if !f.Enabled {
		return nil
	}
	if old == nil || !old.Enabled || old.EnabledAt == nil ||
		old.TriggerType != f.TriggerType || old.Timing != f.Timing ||
		old.Delay() != WarmupDelay(f.DelayValue, f.DelayUnit) {
		at := now
		return &at
	}
	at := old.EnabledAt.UTC()
	return &at
}

// WarmupScenarioUpdate is a save of an existing scenario.
//
// Action labels the audit row (default: warmup_scenario_updated) and Request
// is what the request key is bound to (default: the update itself). A partial
// edit passes the edit as Request: a replay of the same request then hashes
// the same, although the merged fields are computed from the stored state.
type WarmupScenarioUpdate struct {
	WarmupScenarioFields

	ID      uint        `json:"id"`
	Version int         `json:"version"`
	Action  AdminAction `json:"-"`
	Request any         `json:"-"`
}

// UpdateWarmupScenario saves a scenario (audited, idempotent by request key,
// optimistic locking by version).
func (s *Service) UpdateWarmupScenario(ctx context.Context, meta AdminConfigMeta, in WarmupScenarioUpdate) (*AdminConfigResult, error) {
	action := in.Action
	if action == "" {
		action = AdminActionWarmupScenarioUpdated
	}
	var request any = in
	if in.Request != nil {
		request = in.Request
	}
	return s.runAdminConfigMutation(ctx, meta, action, AdminTargetWarmupScenario, in.ID, request,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			var old WarmupScenario
			err := tx.First(&old, in.ID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return configChange{}, ErrWarmupScenarioNotFound
				}
				return configChange{}, fmt.Errorf("load warmup scenario: %w", err)
			}
			cols := in.columns()
			cols["enabled_at"] = warmupEnabledAt(&old, in.WarmupScenarioFields, now)
			cols["version"] = in.Version + 1
			cols["updated_at"] = now
			res := tx.Model(&WarmupScenario{}).Where("id = ? AND version = ?", in.ID, in.Version).Updates(cols)
			if res.Error != nil {
				return configChange{}, fmt.Errorf("update warmup scenario: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				return configChange{}, ErrWarmupVersionConflict
			}
			var saved WarmupScenario
			err = tx.First(&saved, in.ID).Error
			if err != nil {
				return configChange{}, fmt.Errorf("reload warmup scenario: %w", err)
			}
			return configChange{targetID: in.ID, oldValue: warmupScenarioAudit(&old), newValue: warmupScenarioAudit(&saved)}, nil
		})
}

// WarmupScenarioCreate adds a scenario: new scenarios are new rows, the schema
// never changes. Key is a stable unique identifier.
type WarmupScenarioCreate struct {
	WarmupScenarioFields

	Key string `json:"key"`
}

// CreateWarmupScenario inserts a scenario (audited, idempotent by request key).
func (s *Service) CreateWarmupScenario(ctx context.Context, meta AdminConfigMeta, in WarmupScenarioCreate) (*AdminConfigResult, error) {
	return s.runAdminConfigMutation(ctx, meta, AdminActionWarmupScenarioCreated, AdminTargetWarmupScenario, 0, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			var taken int64
			err := tx.Model(&WarmupScenario{}).Where("key = ?", in.Key).Count(&taken).Error
			if err != nil {
				return configChange{}, fmt.Errorf("check warmup scenario key: %w", err)
			}
			if taken > 0 {
				return configChange{}, ErrWarmupScenarioKeyTaken
			}
			f := in.WarmupScenarioFields
			sc := WarmupScenario{
				Key: in.Key, Category: f.Category, Name: f.Name, Description: f.Description, TriggerType: f.TriggerType,
				Timing: f.Timing, DelayValue: f.DelayValue, DelayUnit: f.DelayUnit, Enabled: f.Enabled,
				EnabledAt: warmupEnabledAt(nil, f, now), MessageHTML: f.MessageHTML, ImageID: f.ImageID,
				Buttons: f.Buttons, Conditions: f.Conditions, CooldownHours: f.CooldownHours, DailyCap: f.DailyCap,
				DiscountPercent: f.DiscountPercent, CouponTemplate: f.CouponTemplate, CouponTTLHours: f.CouponTTLHours,
				Position: f.Position, Version: 1, CreatedAt: now, UpdatedAt: now,
			}
			err = tx.Create(&sc).Error
			if err != nil {
				if isUniqueConstraintError(err, "warmup_scenarios.key") {
					return configChange{}, ErrWarmupScenarioKeyTaken
				}
				return configChange{}, fmt.Errorf("create warmup scenario: %w", err)
			}
			return configChange{targetID: sc.ID, newValue: warmupScenarioAudit(&sc)}, nil
		})
}

// warmupScenarioAudit is the audited projection (JSON columns inlined).
func warmupScenarioAudit(sc *WarmupScenario) map[string]any {
	return map[string]any{
		"id": sc.ID, "key": sc.Key, "category": sc.Category, "name": sc.Name, "description": sc.Description,
		"trigger_type": sc.TriggerType, "timing": sc.Timing, "delay_value": sc.DelayValue, "delay_unit": sc.DelayUnit,
		"enabled": sc.Enabled, "enabled_at": sc.EnabledAt, "message_html": sc.MessageHTML, "image_id": sc.ImageID,
		"buttons": sc.Buttons, "conditions": sc.Conditions, "cooldown_hours": sc.CooldownHours, "daily_cap": sc.DailyCap,
		"discount_percent": sc.DiscountPercent, "coupon_template": sc.CouponTemplate, "coupon_ttl_hours": sc.CouponTTLHours,
		"position": sc.Position, "version": sc.Version,
	}
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

// CreateWarmupImage stores an uploaded picture (validated by the service).
// The same bytes uploaded again return the existing row.
func (s *Service) CreateWarmupImage(ctx context.Context, mime string, data []byte) (*WarmupImage, error) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	img := WarmupImage{Mime: mime, SizeBytes: len(data), SHA256: digest, Data: data, CreatedAt: time.Now().UTC()}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "sha256"}}, DoNothing: true}).Create(&img).Error
	if err != nil {
		return nil, fmt.Errorf("create warmup image: %w", err)
	}
	var stored WarmupImage
	err = s.db.WithContext(ctx).Omit("data").Where("sha256 = ?", digest).First(&stored).Error
	if err != nil {
		return nil, fmt.Errorf("load warmup image: %w", err)
	}
	return &stored, nil
}

// GetWarmupImage loads a picture; withData also loads the bytes.
func (s *Service) GetWarmupImage(ctx context.Context, id uint, withData bool) (*WarmupImage, error) {
	q := s.db.WithContext(ctx)
	if !withData {
		q = q.Omit("data")
	}
	var img WarmupImage
	err := q.First(&img, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWarmupImageNotFound
		}
		return nil, fmt.Errorf("get warmup image: %w", err)
	}
	return &img, nil
}

// SetWarmupImageFileID caches (or, with "", forgets) the Telegram file id of a
// picture. A stale id is replaced by the one Telegram returns on the next
// upload of the bytes.
func (s *Service) SetWarmupImageFileID(ctx context.Context, id uint, fileID string) error {
	res := s.db.WithContext(ctx).Model(&WarmupImage{}).Where("id = ?", id).Update("tg_file_id", fileID)
	if res.Error != nil {
		return fmt.Errorf("set warmup image file id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrWarmupImageNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Contacts
// ---------------------------------------------------------------------------

// TouchWarmupContact records that the user talked to the bot (/start). A new
// contact gets source 'start' and first_seen_at = at; an existing one keeps
// its source and first_seen_at, refreshes last_seen_at, username and language
// (when known), and is unblocked: writing to the bot means it is not blocked.
func (s *Service) TouchWarmupContact(ctx context.Context, telegramID int64, username, languageCode string, at time.Time) error {
	if telegramID <= 0 {
		return nil
	}
	at = at.UTC()
	contact := WarmupContact{
		TelegramID: telegramID, Username: username, LanguageCode: languageCode,
		Source: WarmupContactSourceStart, FirstSeenAt: at, LastSeenAt: at,
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "telegram_id"}},
		DoUpdates: clause.Set{
			{Column: clause.Column{Name: "username"}, Value: gorm.Expr("CASE WHEN excluded.username <> '' THEN excluded.username ELSE warmup_contacts.username END")},
			{Column: clause.Column{Name: "language_code"}, Value: gorm.Expr("CASE WHEN excluded.language_code <> '' THEN excluded.language_code ELSE warmup_contacts.language_code END")},
			{Column: clause.Column{Name: "last_seen_at"}, Value: gorm.Expr("excluded.last_seen_at")},
			{Column: clause.Column{Name: "blocked_at"}, Value: nil},
		},
	}).Create(&contact).Error
	if err != nil {
		return fmt.Errorf("touch warmup contact: %w", err)
	}
	return nil
}

// MarkWarmupContactBlocked excludes the user from further warm-up messages.
// An unknown user is recorded as a backfill contact (never a "bot started").
func (s *Service) MarkWarmupContactBlocked(ctx context.Context, telegramID int64, at time.Time) error {
	return markWarmupContactBlocked(s.db.WithContext(ctx), telegramID, at)
}

func markWarmupContactBlocked(tx *gorm.DB, telegramID int64, at time.Time) error {
	if telegramID <= 0 {
		return nil
	}
	at = at.UTC()
	contact := WarmupContact{
		TelegramID: telegramID, Source: WarmupContactSourceBackfill, FirstSeenAt: at, LastSeenAt: at, BlockedAt: &at,
	}
	err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "telegram_id"}},
		DoUpdates: clause.Set{{Column: clause.Column{Name: "blocked_at"}, Value: at}},
	}).Create(&contact).Error
	if err != nil {
		return fmt.Errorf("mark warmup contact blocked: %w", err)
	}
	return nil
}

// GetWarmupContact returns the contact, or nil when the user is unknown.
func (s *Service) GetWarmupContact(ctx context.Context, telegramID int64) (*WarmupContact, error) {
	var c WarmupContact
	err := s.db.WithContext(ctx).Where("telegram_id = ?", telegramID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get warmup contact: %w", err)
	}
	return &c, nil
}
