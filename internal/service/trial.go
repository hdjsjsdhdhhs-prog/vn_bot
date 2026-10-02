package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// TrialRepository is the persistence seam of TrialService. *database.Service
// implements it.
type TrialRepository interface {
	GetTrialAdminView(ctx context.Context, d database.TrialDefaults) (*database.TrialAdminView, error)
	PreviewTrial(ctx context.Context, draft database.TrialDraft) (*database.TrialPreview, error)
	UpdateTrial(ctx context.Context, meta database.AdminConfigMeta, in database.TrialUpdateInput) (*database.AdminConfigResult, error)
	CopyTrialBuilder(ctx context.Context, meta database.AdminConfigMeta, in database.TrialBuilderCopyInput) (*database.AdminConfigResult, error)
}

var _ TrialRepository = (*database.Service)(nil)

// TrialService is the browser-admin boundary of the trial editor (Admin →
// «Тарифы» → «Пробная подписка»). Like TariffService it owns no
// authentication. The trial is read live by the invite page and CreateTrial,
// and /sub keys its cache by builder version and rules, so mutations need no
// post-commit cache invalidation.
type TrialService struct {
	repo     TrialRepository
	defaults database.TrialDefaults
}

// NewTrialService wires the trial editor. defaults are the environment values
// in force while nothing is stored (TRIAL_DURATION_HOURS, TRIAL_RATE_LIMIT).
func NewTrialService(repo TrialRepository, defaults database.TrialDefaults) *TrialService {
	return &TrialService{repo: repo, defaults: defaults}
}

// TrialInput is the editor payload before normalization.
type TrialInput struct {
	Enabled          bool
	DurationHours    int
	RateLimitPerHour int
	Title            string
	Description      string
	Features         []string
	Badge            string
	BuilderID        *uint
	Composition      *database.TrialComposition
	// LegacyNodeIDs: issuance nodes of the trial plan; nil keeps the links.
	LegacyNodeIDs *[]uint
}

// normalize trims text, unifies line breaks and drops blank feature lines,
// exactly like the tariff card.
func (in TrialInput) normalize() database.TrialDraft {
	features := make([]string, 0, len(in.Features))
	for _, feature := range in.Features {
		if trimmed := strings.TrimSpace(feature); trimmed != "" {
			features = append(features, trimmed)
		}
	}
	var composition *database.TrialComposition
	if in.Composition != nil {
		c := *in.Composition
		c.Mode = strings.TrimSpace(c.Mode)
		c.SourceIDs = append([]uint(nil), c.SourceIDs...)
		c.Rules = append([]database.TrialRule(nil), c.Rules...)
		for i := range c.Rules {
			c.Rules[i].Kind = strings.TrimSpace(c.Rules[i].Kind)
			c.Rules[i].CountryCode = strings.ToUpper(strings.TrimSpace(c.Rules[i].CountryCode))
			c.Rules[i].Fingerprint = strings.TrimSpace(c.Rules[i].Fingerprint)
		}
		composition = &c
	}
	var legacyNodeIDs *[]uint
	if in.LegacyNodeIDs != nil {
		ids := append([]uint{}, *in.LegacyNodeIDs...)
		legacyNodeIDs = &ids
	}
	return database.TrialDraft{
		Enabled: in.Enabled, DurationHours: in.DurationHours, RateLimitPerHour: in.RateLimitPerHour,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(strings.ReplaceAll(in.Description, "\r\n", "\n")),
		Features:    features, Badge: strings.TrimSpace(in.Badge), BuilderID: in.BuilderID, Composition: composition,
		LegacyNodeIDs: legacyNodeIDs,
	}
}

// TrialOutcome is returned by every trial mutation, including replays.
type TrialOutcome struct {
	Trial    *database.TrialAdminView `json:"trial"`
	TargetID uint                     `json:"target_id"`
	Replayed bool                     `json:"replayed"`
	Audit    database.AdminAuditLog   `json:"audit"`
}

// View returns the editor state.
func (s *TrialService) View(ctx context.Context) (*database.TrialAdminView, error) {
	view, err := s.repo.GetTrialAdminView(ctx, s.defaults)
	if err != nil {
		return nil, fmt.Errorf("trial view: %w", err)
	}
	return view, nil
}

// Preview evaluates an unsaved draft with the issuance resolver.
func (s *TrialService) Preview(ctx context.Context, in TrialInput) (*database.TrialPreview, error) {
	preview, err := s.repo.PreviewTrial(ctx, in.normalize())
	if err != nil {
		return nil, fmt.Errorf("trial preview: %w", err)
	}
	return preview, nil
}

// Update saves the trial configuration; version is the settings version the
// editor loaded (0 when nothing was stored yet).
func (s *TrialService) Update(ctx context.Context, actor, requestKey string, version int, in TrialInput) (*TrialOutcome, error) {
	result, err := s.repo.UpdateTrial(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey},
		database.TrialUpdateInput{Version: version, Defaults: s.defaults, TrialDraft: in.normalize()})
	if err != nil {
		return nil, err
	}
	return s.outcome(ctx, result)
}

// CopyBuilder copies a builder for the trial and assigns the copy to it.
func (s *TrialService) CopyBuilder(ctx context.Context, actor, requestKey string, builderID uint) (*TrialOutcome, error) {
	result, err := s.repo.CopyTrialBuilder(ctx, database.AdminConfigMeta{Actor: actor, RequestKey: requestKey},
		database.TrialBuilderCopyInput{BuilderID: builderID})
	if err != nil {
		return nil, err
	}
	return s.outcome(ctx, result)
}

func (s *TrialService) outcome(ctx context.Context, result *database.AdminConfigResult) (*TrialOutcome, error) {
	out := &TrialOutcome{TargetID: result.TargetID, Replayed: result.Replayed, Audit: result.Audit}
	view, err := s.View(ctx)
	if err != nil {
		return out, err
	}
	out.Trial = view
	return out, nil
}
