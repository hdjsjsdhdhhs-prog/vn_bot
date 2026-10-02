package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// Trial editor (migration 049). The trial is NOT a separate system: it is the
// existing anonymous invite trial (CreateTrialSubscription / BindTrial /
// ClaimExpiredTrials) whose composition is the default builder of the system
// "trial" plan, resolved on /sub by ResolveSubscriptionBuilder exactly as for
// every other plan. This file adds:
//
//   - trial_settings: on/off, duration, IP limit and the landing-page texts;
//     without a row the environment values apply (backward compatible);
//   - the trial plan builder assignment and, for a builder used by nothing but
//     the trial plan, its sources and country/node rules;
//   - one evaluation (evaluateTrialDraft) used by the admin preview, by save
//     validation and by the issuance check before a trial is created. It
//     resolves the composition with previewBuilderModel, the PreviewBuilder
//     resolver, so preview and issuance can never disagree.
//
// Every change goes through runAdminConfigMutation (admin_audit_log,
// request-key idempotency) with target "trial".

// Audited trial actions and target.
const (
	AdminActionTrialUpdated       AdminAction = "trial_updated"
	AdminActionTrialBuilderCopied AdminAction = "trial_builder_copied"
	AdminTargetTrial                          = "trial"
)

// trialSettingsID is the only row of trial_settings.
const trialSettingsID = 1

// Bounds. Duration and IP limit mirror config validation (TRIAL_DURATION_HOURS
// 1..168, TRIAL_RATE_LIMIT 1..100); texts mirror the tariff card.
const (
	TrialMinDurationHours  = 1
	TrialMaxDurationHours  = 168
	TrialMinRateLimit      = 1
	TrialMaxRateLimit      = 100
	TrialMaxTitleLength    = TariffMaxNameLength
	TrialMaxDescription    = TariffMaxDescriptionLength
	TrialMaxFeatures       = TariffMaxFeatures
	TrialMaxFeatureLength  = TariffMaxFeatureLength
	TrialMaxBadgeLength    = TariffMaxBadgeLength
	TrialMaxSources        = 50
	TrialMaxRules          = 500
	trialMaxBuilderNameLen = 128
	trialHistoryLimit      = 10
)

// Composition modes of the trial builder. Both map onto existing builder
// semantics; no new rule kind exists.
const (
	// TrialModeAll: no rules — every entry of every linked source is served,
	// countries and nodes that appear later are included automatically.
	TrialModeAll = "all"
	// TrialModeSelected: country rules (whole country, new nodes of that
	// country included automatically) and node rules (one concrete node).
	TrialModeSelected = "selected"
)

// Trial issuance modes reported by the preview.
const (
	TrialServeBuilder = "builder"
	TrialServeLegacy  = "legacy"
)

// Problem severities: an invalid draft is never saved; an unavailable one is
// saved only with the trial switched off (it cannot issue subscriptions).
const (
	TrialProblemInvalid     = "invalid"
	TrialProblemUnavailable = "unavailable"
)

var (
	// ErrTrialDisabled: the admin switched the trial off; no new trial is issued.
	ErrTrialDisabled = errors.New("trial is disabled")
	// ErrTrialUnavailable: the configuration cannot issue a working trial
	// (builder disabled or missing, empty composition, no trial node).
	ErrTrialUnavailable = errors.New("trial configuration cannot issue a subscription")
	// ErrTrialInvalid is unwrapped from TrialFieldError.
	ErrTrialInvalid = errors.New("trial configuration is invalid")
	// ErrTrialVersionConflict: the settings were saved meanwhile.
	ErrTrialVersionConflict = errors.New("trial settings were changed concurrently")
	// ErrTrialBuilderShared: the composition of a builder used by other plans or
	// subscriptions cannot be edited from the trial editor.
	ErrTrialBuilderShared = errors.New("builder is used by other plans or subscriptions")
)

var trialCountryPattern = regexp.MustCompile(`^[A-Z]{2}$`)

// TrialFieldError names the rejected field and a stable reason code. It
// unwraps to ErrTrialInvalid, or to ErrTrialBuilderShared for that reason.
type TrialFieldError struct {
	Field  string
	Reason string
}

func (e *TrialFieldError) Error() string {
	return ErrTrialInvalid.Error() + ": " + e.Field + ": " + e.Reason
}

func (e *TrialFieldError) Unwrap() error {
	if e.Reason == "builder_shared" {
		return ErrTrialBuilderShared
	}
	return ErrTrialInvalid
}

// TrialSettings is the stored row of trial_settings.
type TrialSettings struct {
	ID               uint      `gorm:"primaryKey;column:id"`
	Enabled          bool      `gorm:"not null;column:enabled"`
	DurationHours    int       `gorm:"not null;column:duration_hours"`
	RateLimitPerHour int       `gorm:"not null;column:rate_limit_per_hour"`
	Title            string    `gorm:"not null;default:'';column:title"`
	Description      string    `gorm:"not null;default:'';column:description"`
	Features         string    `gorm:"not null;default:'[]';column:features"`
	Badge            string    `gorm:"not null;default:'';column:badge"`
	Version          int       `gorm:"not null;default:1;column:version"`
	CreatedAt        time.Time `gorm:"not null;column:created_at"`
	UpdatedAt        time.Time `gorm:"not null;column:updated_at"`
}

func (TrialSettings) TableName() string { return "trial_settings" }

// TrialDefaults are the environment values used while no row is stored.
type TrialDefaults struct {
	DurationHours    int `json:"duration_hours"`
	RateLimitPerHour int `json:"rate_limit_per_hour"`
}

// TrialEffective is the trial offer in force: the stored row, or the defaults.
type TrialEffective struct {
	Enabled          bool     `json:"enabled"`
	DurationHours    int      `json:"duration_hours"`
	RateLimitPerHour int      `json:"rate_limit_per_hour"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Features         []string `json:"features"`
	Badge            string   `json:"badge"`
	// Version is 0 while nothing is stored (the first save creates version 1).
	Version   int        `json:"version"`
	Stored    bool       `json:"stored"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func effectiveFromRow(row *TrialSettings, d TrialDefaults) TrialEffective {
	if row == nil {
		return TrialEffective{Enabled: true, DurationHours: d.DurationHours, RateLimitPerHour: d.RateLimitPerHour, Features: []string{}}
	}
	updated := row.UpdatedAt
	return TrialEffective{
		Enabled: row.Enabled, DurationHours: row.DurationHours, RateLimitPerHour: row.RateLimitPerHour,
		Title: row.Title, Description: row.Description, Features: DecodeTariffFeatures(row.Features), Badge: row.Badge,
		Version: row.Version, Stored: true, UpdatedAt: &updated,
	}
}

func loadTrialSettings(tx *gorm.DB) (*TrialSettings, error) {
	var row TrialSettings
	err := tx.First(&row, trialSettingsID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load trial settings: %w", err)
	}
	return &row, nil
}

// GetTrialEffective returns the trial offer in force.
func (s *Service) GetTrialEffective(ctx context.Context, d TrialDefaults) (*TrialEffective, error) {
	row, err := loadTrialSettings(s.db.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	eff := effectiveFromRow(row, d)
	return &eff, nil
}

// ---------------------------------------------------------------------------
// Draft, composition and preview
// ---------------------------------------------------------------------------

// TrialRule is one rule of the trial builder: a whole country of a source, or
// one concrete node (catalogue fingerprint) of a source.
type TrialRule struct {
	Kind         string `json:"kind"`
	SourceID     uint   `json:"source_id"`
	CountryCode  string `json:"country_code"`
	Fingerprint  string `json:"fingerprint"`
	OriginalName string `json:"original_name"`
}

func (r TrialRule) key() string {
	if r.Kind == BuilderItemKindCountry {
		return fmt.Sprintf("c|%d|%s", r.SourceID, r.CountryCode)
	}
	return fmt.Sprintf("n|%d|%s", r.SourceID, r.Fingerprint)
}

// TrialComposition replaces the sources and rules of the trial builder.
// BuilderVersion must match the builder (optimistic locking).
type TrialComposition struct {
	BuilderVersion int         `json:"builder_version"`
	Mode           string      `json:"mode"`
	SourceIDs      []uint      `json:"source_ids"`
	Rules          []TrialRule `json:"rules"`
}

// TrialDraft is the full editable trial configuration. A nil Composition
// leaves the builder's sources and rules untouched.
type TrialDraft struct {
	Enabled          bool              `json:"enabled"`
	DurationHours    int               `json:"duration_hours"`
	RateLimitPerHour int               `json:"rate_limit_per_hour"`
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	Features         []string          `json:"features"`
	Badge            string            `json:"badge"`
	BuilderID        *uint             `json:"builder_id"`
	Composition      *TrialComposition `json:"composition"`
}

// TrialProblem is one reason a draft cannot be saved or cannot issue trials.
type TrialProblem struct {
	Field    string `json:"field"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
}

// TrialBuilderRef is the builder serving the trial with the metadata /sub
// sends as subscription headers.
type TrialBuilderRef struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	ProfileTitle string `json:"profile_title"`
	SupportURL   string `json:"support_url"`
	Announce     string `json:"announce"`
	Version      int    `json:"version"`
}

// TrialSourceRef is a source of the composition (no credentials).
type TrialSourceRef struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Usable  bool   `json:"usable"`
}

// TrialCountryCount is one country of the resolved composition; Code "" is
// "no country" (no catalogue country and no flag in the name).
type TrialCountryCount struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// TrialPreview is the evaluated trial: what a new trial would receive.
type TrialPreview struct {
	// Issuable: the configuration can issue a working trial right now
	// (independent of the on/off switch, reported by Enabled).
	Issuable      bool                `json:"issuable"`
	Enabled       bool                `json:"enabled"`
	Problems      []TrialProblem      `json:"problems"`
	Serve         string              `json:"serve"`
	DurationHours int                 `json:"duration_hours"`
	ExpiresAt     time.Time           `json:"expires_at"`
	Builder       *TrialBuilderRef    `json:"builder"`
	Mode          string              `json:"mode"`
	Sources       []TrialSourceRef    `json:"sources"`
	LegacyNodes   int                 `json:"legacy_nodes"`
	Total         int                 `json:"total"`
	Countries     []TrialCountryCount `json:"countries"`
	Items         []PreviewItem       `json:"items"`
	Warnings      []string            `json:"warnings"`
	Missing       int                 `json:"missing"`
	Conflicts     int                 `json:"conflicts"`
	PreviewedAt   time.Time           `json:"previewed_at"`
}

func (p *TrialPreview) add(field, code, severity string) {
	p.Problems = append(p.Problems, TrialProblem{Field: field, Code: code, Severity: severity})
}

// firstProblem returns the first problem of the severity ("" = any).
func (p *TrialPreview) firstProblem(severity string) *TrialProblem {
	for i := range p.Problems {
		if severity == "" || p.Problems[i].Severity == severity {
			return &p.Problems[i]
		}
	}
	return nil
}

func validRunes(s string, limit int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit
}

func (d TrialDraft) staticProblems(p *TrialPreview) {
	if d.DurationHours < TrialMinDurationHours || d.DurationHours > TrialMaxDurationHours {
		p.add("duration_hours", "out_of_range", TrialProblemInvalid)
	}
	if d.RateLimitPerHour < TrialMinRateLimit || d.RateLimitPerHour > TrialMaxRateLimit {
		p.add("rate_limit_per_hour", "out_of_range", TrialProblemInvalid)
	}
	if !validRunes(d.Title, TrialMaxTitleLength) {
		p.add("title", "too_long", TrialProblemInvalid)
	}
	if !validRunes(d.Description, TrialMaxDescription) {
		p.add("description", "too_long", TrialProblemInvalid)
	}
	if len(d.Features) > TrialMaxFeatures {
		p.add("features", "too_many", TrialProblemInvalid)
	} else {
		for _, f := range d.Features {
			if f == "" || !validRunes(f, TrialMaxFeatureLength) {
				p.add("features", "too_long", TrialProblemInvalid)
				break
			}
		}
	}
	if !validRunes(d.Badge, TrialMaxBadgeLength) {
		p.add("badge", "too_long", TrialProblemInvalid)
	}
}

func trialPlan(tx *gorm.DB) (*Plan, error) {
	var plan Plan
	if err := tx.Where("name = ?", TrialPlanName).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("load trial plan: %w", err)
	}
	return &plan, nil
}

// BuilderUsage is who else relies on a builder besides the trial plan.
type BuilderUsage struct {
	Plans         []string `json:"plans"`
	Subscriptions int64    `json:"subscriptions"`
	TrialPlan     bool     `json:"trial_plan"`
}

// Shared reports whether changing the builder would change anything but the
// trial: another plan's default or a per-subscription override.
func (u BuilderUsage) Shared() bool { return len(u.Plans) > 0 || u.Subscriptions > 0 }

func builderUsage(tx *gorm.DB, builderID uint) (BuilderUsage, error) {
	var plans []Plan
	if err := tx.Select("id", "name").Where("subscription_builder_id = ?", builderID).Order("name ASC").Find(&plans).Error; err != nil {
		return BuilderUsage{}, fmt.Errorf("load builder plans: %w", err)
	}
	usage := BuilderUsage{Plans: []string{}}
	for _, p := range plans {
		if p.Name == TrialPlanName {
			usage.TrialPlan = true
			continue
		}
		usage.Plans = append(usage.Plans, p.Name)
	}
	if err := tx.Model(&Subscription{}).Where("subscription_builder_id = ?", builderID).Count(&usage.Subscriptions).Error; err != nil {
		return BuilderUsage{}, fmt.Errorf("count builder subscriptions: %w", err)
	}
	return usage, nil
}

// compositionOf reads the stored builder as a composition (enabled rules only).
func compositionOf(b *SubscriptionBuilder) TrialComposition {
	c := TrialComposition{BuilderVersion: b.Version, Mode: TrialModeAll, SourceIDs: []uint{}, Rules: []TrialRule{}}
	links := append([]SubscriptionBuilderSource(nil), b.Sources...)
	sort.SliceStable(links, func(i, j int) bool { return links[i].Position < links[j].Position })
	for _, l := range links {
		c.SourceIDs = append(c.SourceIDs, l.SourceID)
	}
	items := append([]SubscriptionBuilderItem(nil), b.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].ID < items[j].ID
	})
	for _, it := range items {
		if !it.Enabled {
			continue
		}
		c.Rules = append(c.Rules, TrialRule{
			Kind: it.Kind, SourceID: it.SourceID, CountryCode: strings.ToUpper(it.CountryCode),
			Fingerprint: it.Fingerprint, OriginalName: it.OriginalName,
		})
	}
	if len(b.Items) > 0 {
		c.Mode = TrialModeSelected
	}
	return c
}

// validateComposition checks the rules against the source catalogues and
// returns the in-memory builder the preview resolves. Rules are normalized in
// place (upper-case country, catalogue name of a node).
func (s *Service) validateComposition(ctx context.Context, b *SubscriptionBuilder, c *TrialComposition, p *TrialPreview) (*SubscriptionBuilder, error) {
	field := "composition"
	if c.Mode != TrialModeAll && c.Mode != TrialModeSelected {
		p.add(field, "mode", TrialProblemInvalid)
		return nil, nil
	}
	if len(c.SourceIDs) == 0 {
		p.add("composition.source_ids", "required", TrialProblemInvalid)
		return nil, nil
	}
	if len(c.SourceIDs) > TrialMaxSources {
		p.add("composition.source_ids", "too_many", TrialProblemInvalid)
		return nil, nil
	}
	linked := make(map[uint]bool, len(c.SourceIDs))
	for _, id := range c.SourceIDs {
		if id == 0 || linked[id] {
			p.add("composition.source_ids", "duplicate", TrialProblemInvalid)
			return nil, nil
		}
		linked[id] = true
	}
	var found int64
	if err := s.db.WithContext(ctx).Model(&ProviderSource{}).Where("id IN ?", c.SourceIDs).Count(&found).Error; err != nil {
		return nil, fmt.Errorf("check trial sources: %w", err)
	}
	if int(found) != len(c.SourceIDs) {
		p.add("composition.source_ids", "source_not_found", TrialProblemInvalid)
		return nil, nil
	}
	switch {
	case c.Mode == TrialModeAll && len(c.Rules) > 0:
		p.add("composition.rules", "rules_in_all_mode", TrialProblemInvalid)
		return nil, nil
	case c.Mode == TrialModeSelected && len(c.Rules) == 0:
		p.add("composition.rules", "required", TrialProblemInvalid)
		return nil, nil
	case len(c.Rules) > TrialMaxRules:
		p.add("composition.rules", "too_many", TrialProblemInvalid)
		return nil, nil
	}

	catalogue := map[uint][]ProviderSourceEntry{}
	entriesOf := func(sourceID uint) ([]ProviderSourceEntry, error) {
		if entries, ok := catalogue[sourceID]; ok {
			return entries, nil
		}
		entries, err := s.GetSourceEntries(ctx, sourceID, "*")
		if err != nil {
			return nil, err
		}
		catalogue[sourceID] = entries
		return entries, nil
	}
	seen := map[string]bool{}
	countries := map[string]bool{}
	for i := range c.Rules {
		r := &c.Rules[i]
		if !linked[r.SourceID] {
			p.add("composition.rules", "rule_source_not_linked", TrialProblemInvalid)
			return nil, nil
		}
		entries, err := entriesOf(r.SourceID)
		if err != nil {
			return nil, fmt.Errorf("load trial catalogue: %w", err)
		}
		switch r.Kind {
		case BuilderItemKindCountry:
			r.CountryCode = strings.ToUpper(strings.TrimSpace(r.CountryCode))
			r.Fingerprint, r.OriginalName = "", ""
			if !trialCountryPattern.MatchString(r.CountryCode) {
				p.add("composition.rules", "country_code", TrialProblemInvalid)
				return nil, nil
			}
			exists := false
			for _, e := range entries {
				if CountryMatches(e.CountryCode, e.OriginalName, r.CountryCode) {
					exists = true
					break
				}
			}
			if !exists {
				p.add("composition.rules", "country_not_found", TrialProblemInvalid)
				return nil, nil
			}
			countries[fmt.Sprintf("%d|%s", r.SourceID, r.CountryCode)] = true
		case BuilderItemKindNode:
			r.CountryCode = ""
			var match *ProviderSourceEntry
			for j := range entries {
				if r.Fingerprint != "" && entries[j].Fingerprint == r.Fingerprint {
					match = &entries[j]
					break
				}
			}
			if match == nil {
				p.add("composition.rules", "node_not_found", TrialProblemInvalid)
				return nil, nil
			}
			r.OriginalName = match.OriginalName
		default:
			p.add("composition.rules", "kind", TrialProblemInvalid)
			return nil, nil
		}
		if seen[r.key()] {
			p.add("composition.rules", "duplicate_rule", TrialProblemInvalid)
			return nil, nil
		}
		seen[r.key()] = true
	}
	// A node rule inside a country already taken whole is a contradiction:
	// the country rule serves it anyway, so the node choice would be a no-op.
	for _, r := range c.Rules {
		if r.Kind != BuilderItemKindNode {
			continue
		}
		for _, e := range catalogue[r.SourceID] {
			if e.Fingerprint == r.Fingerprint {
				if code := EntryCountry(e.CountryCode, e.OriginalName); code != "" && countries[fmt.Sprintf("%d|%s", r.SourceID, strings.ToUpper(code))] {
					p.add("composition.rules", "node_covered_by_country", TrialProblemInvalid)
					return nil, nil
				}
				break
			}
		}
	}

	model := &SubscriptionBuilder{
		ID: b.ID, Name: b.Name, Enabled: b.Enabled, ProfileTitle: b.ProfileTitle, SupportURL: b.SupportURL,
		Announce: b.Announce, Version: b.Version,
	}
	for i, id := range c.SourceIDs {
		model.Sources = append(model.Sources, SubscriptionBuilderSource{BuilderID: b.ID, SourceID: id, Position: i})
	}
	for i, r := range c.Rules {
		model.Items = append(model.Items, SubscriptionBuilderItem{
			BuilderID: b.ID, Kind: r.Kind, SourceID: r.SourceID, CountryCode: r.CountryCode,
			Fingerprint: r.Fingerprint, OriginalName: r.OriginalName, Position: i, Enabled: true,
		})
	}
	return model, nil
}

// evaluateTrialDraft resolves a draft into what a new trial would receive.
// It never writes. Problems are collected, not returned as errors; an error
// is an infrastructure failure.
func (s *Service) evaluateTrialDraft(ctx context.Context, d *TrialDraft, now time.Time) (*TrialPreview, error) {
	p := &TrialPreview{
		Enabled: d.Enabled, Problems: []TrialProblem{}, Sources: []TrialSourceRef{}, Countries: []TrialCountryCount{},
		Items: []PreviewItem{}, Warnings: []string{}, DurationHours: d.DurationHours, PreviewedAt: now,
	}
	d.staticProblems(p)
	if d.DurationHours >= TrialMinDurationHours && d.DurationHours <= TrialMaxDurationHours {
		p.ExpiresAt = now.Add(time.Duration(d.DurationHours) * time.Hour)
	}

	nodes, err := s.GetNodesByPlanName(ctx, TrialPlanName)
	if err != nil {
		return nil, err
	}
	p.LegacyNodes = len(nodes)
	// CreateTrial provisions the anonymous client on the first trial node in
	// every mode (a no-op for proxman/fetch nodes): without one it fails.
	if p.LegacyNodes == 0 {
		p.add("legacy_nodes", "no_trial_node", TrialProblemUnavailable)
	}

	if d.BuilderID == nil {
		p.Serve = TrialServeLegacy
		if d.Composition != nil {
			p.add("composition", "builder_required", TrialProblemInvalid)
		}
		p.Issuable = len(p.Problems) == 0
		return p, nil
	}

	p.Serve = TrialServeBuilder
	b, err := s.GetBuilder(ctx, *d.BuilderID)
	if errors.Is(err, ErrBuilderNotFound) {
		p.add("builder_id", "builder_not_found", TrialProblemInvalid)
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	p.Builder = &TrialBuilderRef{
		ID: b.ID, Name: b.Name, Enabled: b.Enabled, ProfileTitle: b.ProfileTitle, SupportURL: b.SupportURL,
		Announce: b.Announce, Version: b.Version,
	}
	if !b.Enabled {
		// /sub would silently skip a disabled builder; a new trial must not be
		// issued onto an unknown fallback.
		p.add("builder_id", "builder_disabled", TrialProblemUnavailable)
	}

	model := b
	if d.Composition != nil {
		usage, err := builderUsage(s.db.WithContext(ctx), b.ID)
		if err != nil {
			return nil, err
		}
		if usage.Shared() {
			p.add("composition", "builder_shared", TrialProblemInvalid)
			return p, nil
		}
		m, err := s.validateComposition(ctx, b, d.Composition, p)
		if err != nil {
			return nil, err
		}
		if m == nil {
			return p, nil
		}
		model = m
	}
	p.Mode = TrialModeAll
	if len(model.Items) > 0 {
		p.Mode = TrialModeSelected
	}

	sourceIDs := make([]uint, 0, len(model.Sources))
	for _, l := range model.Sources {
		sourceIDs = append(sourceIDs, l.SourceID)
	}
	if len(sourceIDs) > 0 {
		var sources []ProviderSource
		if err := s.db.WithContext(ctx).Where("id IN ?", sourceIDs).Find(&sources).Error; err != nil {
			return nil, fmt.Errorf("load trial sources: %w", err)
		}
		byID := make(map[uint]ProviderSource, len(sources))
		for _, src := range sources {
			byID[src.ID] = src
		}
		for _, id := range sourceIDs {
			src, ok := byID[id]
			if !ok {
				continue
			}
			_, _, cfgErr := src.RequestConfiguration()
			p.Sources = append(p.Sources, TrialSourceRef{ID: src.ID, Name: src.Name, Enabled: src.Enabled, Usable: cfgErr == nil})
		}
	}

	res, err := s.previewBuilderModel(ctx, model)
	if err != nil {
		return nil, err
	}
	p.Total, p.Missing, p.Conflicts = res.Total, res.Missing, res.Conflicts
	if res.Items != nil {
		p.Items = res.Items
	}
	if res.Warnings != nil {
		p.Warnings = res.Warnings
	}
	counts := map[string]int{}
	order := []string{}
	for _, it := range res.Items {
		if it.Entry == nil || (it.Status != FingerprintStatusMatched && it.Status != FingerprintStatusFallback) {
			continue
		}
		code := strings.ToUpper(EntryCountry(it.Entry.CountryCode, it.Entry.OriginalName))
		if _, ok := counts[code]; !ok {
			order = append(order, code)
		}
		counts[code]++
	}
	for _, code := range order {
		p.Countries = append(p.Countries, TrialCountryCount{Code: code, Count: counts[code]})
	}
	if len(model.Sources) == 0 {
		p.add("composition.source_ids", "no_sources", TrialProblemUnavailable)
	} else if p.Total == 0 {
		p.add("composition", "empty_result", TrialProblemUnavailable)
	}
	p.Issuable = len(p.Problems) == 0
	return p, nil
}

// PreviewTrial evaluates an unsaved draft (nothing is written).
func (s *Service) PreviewTrial(ctx context.Context, d TrialDraft) (*TrialPreview, error) {
	return s.evaluateTrialDraft(ctx, &d, time.Now().UTC())
}

// ---------------------------------------------------------------------------
// Issuance check (used before a trial is created)
// ---------------------------------------------------------------------------

// TrialIssuance is the outcome of the pre-creation check.
type TrialIssuance struct {
	Settings TrialEffective
	Preview  *TrialPreview
}

// CheckTrialIssuance returns the trial offer in force and whether a new trial
// may be issued: ErrTrialDisabled when switched off, ErrTrialUnavailable (with
// the first reason) when the configuration cannot issue a working trial. It
// evaluates the stored configuration with the same evaluation as the preview.
func (s *Service) CheckTrialIssuance(ctx context.Context, d TrialDefaults) (*TrialIssuance, error) {
	eff, err := s.GetTrialEffective(ctx, d)
	if err != nil {
		return nil, err
	}
	out := &TrialIssuance{Settings: *eff}
	if !eff.Enabled {
		return out, ErrTrialDisabled
	}
	plan, err := trialPlan(s.db.WithContext(ctx))
	if err != nil {
		return out, err
	}
	draft := draftFromEffective(*eff, plan.SubscriptionBuilderID)
	preview, err := s.evaluateTrialDraft(ctx, &draft, time.Now().UTC())
	if err != nil {
		return out, err
	}
	out.Preview = preview
	if problem := preview.firstProblem(""); problem != nil {
		return out, fmt.Errorf("%w: %s", ErrTrialUnavailable, problem.Code)
	}
	return out, nil
}

func draftFromEffective(eff TrialEffective, builderID *uint) TrialDraft {
	return TrialDraft{
		Enabled: eff.Enabled, DurationHours: eff.DurationHours, RateLimitPerHour: eff.RateLimitPerHour,
		Title: eff.Title, Description: eff.Description, Features: eff.Features, Badge: eff.Badge, BuilderID: builderID,
	}
}

// ---------------------------------------------------------------------------
// Admin view
// ---------------------------------------------------------------------------

// TrialBuilderSummary is one builder offered by the trial editor.
type TrialBuilderSummary struct {
	TrialBuilderRef
	SourceIDs       []uint       `json:"source_ids"`
	Rules           int          `json:"rules"`
	DisabledRules   int          `json:"disabled_rules"`
	Total           int          `json:"total"`
	Countries       int          `json:"countries"`
	Usage           BuilderUsage `json:"usage"`
	Shared          bool         `json:"shared"`
	AssignedToTrial bool         `json:"assigned_to_trial"`
}

// TrialAdminView is everything the trial editor shows.
type TrialAdminView struct {
	Settings    TrialEffective        `json:"settings"`
	Defaults    TrialDefaults         `json:"defaults"`
	PlanID      uint                  `json:"plan_id"`
	BuilderID   *uint                 `json:"builder_id"`
	Composition *TrialComposition     `json:"composition"`
	Builders    []TrialBuilderSummary `json:"builders"`
	Preview     *TrialPreview         `json:"preview"`
	// Trials counts the anonymous (not yet bound) trials by status.
	ActiveTrials int64           `json:"active_trials"`
	History      []AdminAuditLog `json:"history"`
}

// GetTrialAdminView loads the editor state: settings in force, the trial plan
// builder with its composition, every builder with usage, and the preview of
// the stored configuration.
func (s *Service) GetTrialAdminView(ctx context.Context, d TrialDefaults) (*TrialAdminView, error) {
	eff, err := s.GetTrialEffective(ctx, d)
	if err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	plan, err := trialPlan(db)
	if err != nil {
		return nil, err
	}
	view := &TrialAdminView{Settings: *eff, Defaults: d, PlanID: plan.ID, BuilderID: plan.SubscriptionBuilderID,
		Builders: []TrialBuilderSummary{}, History: []AdminAuditLog{}}

	var builders []SubscriptionBuilder
	if err := db.Preload("Sources", func(q *gorm.DB) *gorm.DB { return q.Order("position ASC") }).
		Preload("Items", func(q *gorm.DB) *gorm.DB { return q.Order("position ASC, id ASC") }).
		Order("name ASC").Find(&builders).Error; err != nil {
		return nil, fmt.Errorf("load trial builders: %w", err)
	}
	for i := range builders {
		b := &builders[i]
		usage, err := builderUsage(db, b.ID)
		if err != nil {
			return nil, err
		}
		res, err := s.previewBuilderModel(ctx, b)
		if err != nil {
			return nil, err
		}
		countries := map[string]bool{}
		for _, it := range res.Items {
			if it.Entry != nil && (it.Status == FingerprintStatusMatched || it.Status == FingerprintStatusFallback) {
				countries[strings.ToUpper(EntryCountry(it.Entry.CountryCode, it.Entry.OriginalName))] = true
			}
		}
		summary := TrialBuilderSummary{
			TrialBuilderRef: TrialBuilderRef{ID: b.ID, Name: b.Name, Enabled: b.Enabled, ProfileTitle: b.ProfileTitle,
				SupportURL: b.SupportURL, Announce: b.Announce, Version: b.Version},
			SourceIDs: []uint{}, Total: res.Total, Countries: len(countries), Usage: usage, Shared: usage.Shared(),
			AssignedToTrial: plan.SubscriptionBuilderID != nil && *plan.SubscriptionBuilderID == b.ID,
		}
		for _, l := range b.Sources {
			summary.SourceIDs = append(summary.SourceIDs, l.SourceID)
		}
		for _, it := range b.Items {
			if it.Enabled {
				summary.Rules++
			} else {
				summary.DisabledRules++
			}
		}
		view.Builders = append(view.Builders, summary)
		if summary.AssignedToTrial {
			c := compositionOf(b)
			view.Composition = &c
		}
	}

	draft := draftFromEffective(*eff, plan.SubscriptionBuilderID)
	if view.Preview, err = s.evaluateTrialDraft(ctx, &draft, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := db.Model(&Subscription{}).
		Where("plan_id = ? AND telegram_id < 0 AND status = ?", plan.ID, SubscriptionStatusActive).
		Count(&view.ActiveTrials).Error; err != nil {
		return nil, fmt.Errorf("count active trials: %w", err)
	}
	if err := db.Where("target_type = ?", AdminTargetTrial).Order("id DESC").Limit(trialHistoryLimit).
		Find(&view.History).Error; err != nil {
		return nil, fmt.Errorf("load trial history: %w", err)
	}
	return view, nil
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

// TrialUpdateInput is a save of the editor. Version is the settings version
// the editor loaded (0 when nothing was stored yet).
type TrialUpdateInput struct {
	Version  int           `json:"version"`
	Defaults TrialDefaults `json:"defaults"`
	TrialDraft
}

// trialAuditView is the audited projection of the trial configuration.
type trialAuditView struct {
	Settings    TrialEffective    `json:"settings"`
	BuilderID   *uint             `json:"builder_id"`
	Composition *TrialComposition `json:"composition,omitempty"`
}

// UpdateTrial saves the trial configuration: settings, the trial plan builder
// and, when given, the composition of that builder. A draft with an invalid
// problem is rejected (TrialFieldError); an enabled draft that cannot issue a
// trial is rejected too, so the trial is never switched on broken.
//
// Concurrent saves are retried when SQLite refuses the write lock (see
// retryTrialSaveOnBusy): each attempt is a new transaction that re-reads the
// version, so a save that lost the race ends with ErrTrialVersionConflict.
func (s *Service) UpdateTrial(ctx context.Context, meta AdminConfigMeta, in TrialUpdateInput) (*AdminConfigResult, error) {
	if in.Version < 0 {
		return nil, fmt.Errorf("%w: version", ErrAdminInvalidRequest)
	}
	var result *AdminConfigResult
	err := retryTrialSaveOnBusy(ctx, trialSaveAttempts, trialSaveBackoff, func() error {
		var err error
		result, err = s.updateTrialOnce(ctx, meta, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

const (
	// trialSaveAttempts bounds the transactions one trial save may start.
	trialSaveAttempts = 3
	// trialSaveBackoff is the wait before the second attempt; it doubles.
	trialSaveBackoff = 10 * time.Millisecond
)

// isSQLiteBusy reports a refused SQLite lock (SQLITE_BUSY, including
// BUSY_SNAPSHOT, or SQLITE_LOCKED). Transactions start DEFERRED: when several
// connections have read the trial row and one upgrades to write, SQLite
// refuses the others immediately instead of waiting for busy_timeout.
func isSQLiteBusy(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && (sqliteErr.Code == sqlite3.ErrBusy || sqliteErr.Code == sqlite3.ErrLocked)
}

// retryTrialSaveOnBusy runs fn up to attempts times while it fails with a
// refused SQLite lock, waiting backoff, 2×backoff, ... between attempts. Any
// other error (and success) ends at once; after the last attempt the lock
// error is returned as is. fn must be a whole transaction so a retry repeats
// every check.
func retryTrialSaveOnBusy(ctx context.Context, attempts int, backoff time.Duration, fn func() error) error {
	var err error
	for attempt := 1; ; attempt++ {
		if err = fn(); err == nil || !isSQLiteBusy(err) || attempt >= attempts {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		backoff *= 2
	}
}

// updateTrialOnce is one UpdateTrial transaction.
func (s *Service) updateTrialOnce(ctx context.Context, meta AdminConfigMeta, in TrialUpdateInput) (*AdminConfigResult, error) {
	return s.runAdminConfigMutation(ctx, meta, AdminActionTrialUpdated, AdminTargetTrial, trialSettingsID, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			svc := &Service{db: tx}
			row, err := loadTrialSettings(tx)
			if err != nil {
				return configChange{}, err
			}
			current := 0
			if row != nil {
				current = row.Version
			}
			if in.Version != current {
				return configChange{}, ErrTrialVersionConflict
			}
			plan, err := trialPlan(tx)
			if err != nil {
				return configChange{}, err
			}

			draft := in.TrialDraft
			preview, err := svc.evaluateTrialDraft(ctx, &draft, now)
			if err != nil {
				return configChange{}, err
			}
			if problem := preview.firstProblem(TrialProblemInvalid); problem != nil {
				return configChange{}, &TrialFieldError{Field: problem.Field, Reason: problem.Code}
			}
			if problem := preview.firstProblem(TrialProblemUnavailable); draft.Enabled && problem != nil {
				return configChange{}, &TrialFieldError{Field: problem.Field, Reason: problem.Code}
			}

			var builder *SubscriptionBuilder
			if draft.Composition != nil {
				if builder, err = svc.GetBuilder(ctx, *draft.BuilderID); err != nil {
					return configChange{}, err
				}
				if builder.Version != draft.Composition.BuilderVersion {
					return configChange{}, ErrBuilderVersionConflict
				}
			}

			old := trialAuditView{Settings: effectiveFromRow(row, in.Defaults), BuilderID: plan.SubscriptionBuilderID}
			if builder != nil {
				c := compositionOf(builder)
				old.Composition = &c
			}

			features := EncodeTariffFeatures(draft.Features)
			if row == nil {
				row = &TrialSettings{ID: trialSettingsID, CreatedAt: now}
			}
			row.Enabled, row.DurationHours, row.RateLimitPerHour = draft.Enabled, draft.DurationHours, draft.RateLimitPerHour
			row.Title, row.Description, row.Features, row.Badge = draft.Title, draft.Description, features, draft.Badge
			row.Version, row.UpdatedAt = current+1, now
			// Save writes every column, including the false Enabled (no default
			// substitution on update of an existing primary key).
			if current == 0 {
				if err := tx.Create(row).Error; err != nil {
					return configChange{}, fmt.Errorf("create trial settings: %w", err)
				}
				if !draft.Enabled {
					if err := tx.Model(&TrialSettings{}).Where("id = ?", trialSettingsID).UpdateColumn("enabled", false).Error; err != nil {
						return configChange{}, fmt.Errorf("store trial switch: %w", err)
					}
				}
			} else {
				res := tx.Model(&TrialSettings{}).Where("id = ? AND version = ?", trialSettingsID, current).Updates(map[string]any{
					"enabled": draft.Enabled, "duration_hours": draft.DurationHours, "rate_limit_per_hour": draft.RateLimitPerHour,
					"title": draft.Title, "description": draft.Description, "features": features, "badge": draft.Badge,
					"version": current + 1, "updated_at": now,
				})
				if res.Error != nil {
					return configChange{}, fmt.Errorf("update trial settings: %w", res.Error)
				}
				if res.RowsAffected == 0 {
					return configChange{}, ErrTrialVersionConflict
				}
			}

			if !sameBuilderID(plan.SubscriptionBuilderID, draft.BuilderID) {
				if err := tx.Model(&Plan{}).Where("id = ?", plan.ID).Update("subscription_builder_id", draft.BuilderID).Error; err != nil {
					return configChange{}, fmt.Errorf("set trial plan builder: %w", err)
				}
			}
			if builder != nil {
				if err := replaceBuilderComposition(tx, builder, draft.Composition, now); err != nil {
					return configChange{}, err
				}
			}

			saved, err := loadTrialSettings(tx)
			if err != nil {
				return configChange{}, err
			}
			next := trialAuditView{Settings: effectiveFromRow(saved, in.Defaults), BuilderID: draft.BuilderID}
			if builder != nil {
				reloaded, err := svc.GetBuilder(ctx, builder.ID)
				if err != nil {
					return configChange{}, err
				}
				c := compositionOf(reloaded)
				next.Composition = &c
			}
			return configChange{targetID: trialSettingsID, oldValue: old, newValue: next}, nil
		})
}

func sameBuilderID(a, b *uint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// replaceBuilderComposition writes the sources and rules of the trial builder
// and bumps its version (the /sub cache key covers version and rules, so the
// new composition is served without a stale cache entry). Custom names and
// descriptions of rules that stay are kept.
func replaceBuilderComposition(tx *gorm.DB, b *SubscriptionBuilder, c *TrialComposition, now time.Time) error {
	kept := map[string]SubscriptionBuilderItem{}
	for _, it := range b.Items {
		r := TrialRule{Kind: it.Kind, SourceID: it.SourceID, CountryCode: strings.ToUpper(it.CountryCode), Fingerprint: it.Fingerprint}
		kept[r.key()] = it
	}
	if err := tx.Where("builder_id = ?", b.ID).Delete(&SubscriptionBuilderItem{}).Error; err != nil {
		return fmt.Errorf("clear trial builder rules: %w", err)
	}
	if err := tx.Where("builder_id = ?", b.ID).Delete(&SubscriptionBuilderSource{}).Error; err != nil {
		return fmt.Errorf("clear trial builder sources: %w", err)
	}
	for i, id := range c.SourceIDs {
		if err := tx.Create(&SubscriptionBuilderSource{BuilderID: b.ID, SourceID: id, Position: i}).Error; err != nil {
			return fmt.Errorf("add trial builder source %d: %w", id, err)
		}
	}
	for i, r := range c.Rules {
		item := SubscriptionBuilderItem{
			BuilderID: b.ID, Kind: r.Kind, SourceID: r.SourceID, CountryCode: r.CountryCode,
			Fingerprint: r.Fingerprint, OriginalName: r.OriginalName, Position: i, Enabled: true,
		}
		if prev, ok := kept[r.key()]; ok {
			item.CustomName, item.Description = prev.CustomName, prev.Description
		}
		if err := tx.Create(&item).Error; err != nil {
			return fmt.Errorf("add trial builder rule: %w", err)
		}
	}
	res := tx.Model(&SubscriptionBuilder{}).Where("id = ? AND version = ?", b.ID, b.Version).
		Updates(map[string]any{"version": b.Version + 1, "updated_at": now})
	if res.Error != nil {
		return fmt.Errorf("bump trial builder version: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrBuilderVersionConflict
	}
	return nil
}

// TrialBuilderCopyInput copies a builder for the trial.
type TrialBuilderCopyInput struct {
	BuilderID uint `json:"builder_id"`
}

// CopyTrialBuilder creates a copy of a builder (metadata, sources and rules)
// and assigns it to the trial plan, so the trial composition can be edited
// without touching the plans and subscriptions that keep using the original.
// TargetID of the result is the new builder.
func (s *Service) CopyTrialBuilder(ctx context.Context, meta AdminConfigMeta, in TrialBuilderCopyInput) (*AdminConfigResult, error) {
	if in.BuilderID == 0 {
		return nil, fmt.Errorf("%w: builder_id", ErrAdminInvalidRequest)
	}
	return s.runAdminConfigMutation(ctx, meta, AdminActionTrialBuilderCopied, AdminTargetTrial, trialSettingsID, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			svc := &Service{db: tx}
			src, err := svc.GetBuilder(ctx, in.BuilderID)
			if err != nil {
				if errors.Is(err, ErrBuilderNotFound) {
					return configChange{}, &TrialFieldError{Field: "builder_id", Reason: "builder_not_found"}
				}
				return configChange{}, err
			}
			plan, err := trialPlan(tx)
			if err != nil {
				return configChange{}, err
			}
			name, err := uniqueBuilderName(tx, src.Name)
			if err != nil {
				return configChange{}, err
			}
			copyB := SubscriptionBuilder{
				Name: name, Description: src.Description, Enabled: true, ProfileTitle: src.ProfileTitle,
				SupportURL: src.SupportURL, Announce: src.Announce, Version: 1, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&copyB).Error; err != nil {
				return configChange{}, fmt.Errorf("create trial builder copy: %w", err)
			}
			if !src.Enabled {
				// Keep the switch of the original (GORM would store default:true).
				if err := tx.Model(&SubscriptionBuilder{}).Where("id = ?", copyB.ID).UpdateColumn("enabled", false).Error; err != nil {
					return configChange{}, fmt.Errorf("copy builder switch: %w", err)
				}
				copyB.Enabled = false
			}
			for _, l := range src.Sources {
				if err := tx.Create(&SubscriptionBuilderSource{BuilderID: copyB.ID, SourceID: l.SourceID, Position: l.Position}).Error; err != nil {
					return configChange{}, fmt.Errorf("copy builder source: %w", err)
				}
			}
			for _, it := range src.Items {
				item := it
				item.ID, item.BuilderID, item.Builder, item.Source = 0, copyB.ID, nil, nil
				enabled := item.Enabled
				if err := tx.Create(&item).Error; err != nil {
					return configChange{}, fmt.Errorf("copy builder rule: %w", err)
				}
				if !enabled {
					if err := tx.Model(&SubscriptionBuilderItem{}).Where("id = ?", item.ID).UpdateColumn("enabled", false).Error; err != nil {
						return configChange{}, fmt.Errorf("copy builder rule switch: %w", err)
					}
				}
			}
			old := plan.SubscriptionBuilderID
			if err := tx.Model(&Plan{}).Where("id = ?", plan.ID).Update("subscription_builder_id", copyB.ID).Error; err != nil {
				return configChange{}, fmt.Errorf("assign trial builder copy: %w", err)
			}
			return configChange{
				targetID: copyB.ID,
				oldValue: map[string]any{"builder_id": old},
				newValue: map[string]any{"builder_id": copyB.ID, "copied_from": src.ID, "name": copyB.Name},
			}, nil
		})
}

func uniqueBuilderName(tx *gorm.DB, base string) (string, error) {
	const suffix = " · пробная"
	root := []rune(base)
	if limit := trialMaxBuilderNameLen - utf8.RuneCountInString(suffix) - 4; len(root) > limit {
		root = root[:limit]
	}
	for n := 1; n < 1000; n++ {
		name := string(root) + suffix
		if n > 1 {
			name = fmt.Sprintf("%s %d", name, n)
		}
		var taken int64
		if err := tx.Model(&SubscriptionBuilder{}).Where("name = ?", name).Count(&taken).Error; err != nil {
			return "", fmt.Errorf("check builder name: %w", err)
		}
		if taken == 0 {
			return name, nil
		}
	}
	return "", ErrBuilderNameTaken
}
