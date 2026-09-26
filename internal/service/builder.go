package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// BuilderRepository is the persistence seam for BuilderService.
// *database.Service implements it; tests may substitute a fake.
type BuilderRepository interface {
	// Sources
	ListProviderSources(ctx context.Context) ([]database.ProviderSource, error)
	GetProviderSourceByID(ctx context.Context, id uint) (*database.ProviderSource, error)
	UpdateProviderSource(ctx context.Context, id uint, in database.ProviderSourceUpdateInput) (*database.ProviderSource, error)
	SetProviderSourceEnabled(ctx context.Context, id uint, enabled bool) (*database.ProviderSource, error)
	GetSourceEntries(ctx context.Context, sourceID uint, countryCode string) ([]database.ProviderSourceEntry, error)

	// Builders
	ListBuilders(ctx context.Context) ([]database.SubscriptionBuilder, error)
	GetBuilder(ctx context.Context, id uint) (*database.SubscriptionBuilder, error)
	CreateBuilder(ctx context.Context, meta database.AdminConfigMeta, in database.BuilderCreateInput) (*database.AdminConfigResult, error)
	UpdateBuilder(ctx context.Context, meta database.AdminConfigMeta, in database.BuilderUpdateInput) (*database.AdminConfigResult, error)
	SetBuilderSources(ctx context.Context, builderID uint, sourceIDs []uint) error
	UpsertBuilderItem(ctx context.Context, item database.SubscriptionBuilderItem) (*database.SubscriptionBuilderItem, error)
	DeleteBuilderItem(ctx context.Context, itemID uint) error
	ReorderBuilderItems(ctx context.Context, builderID uint, itemIDs []uint) error

	// Assignments
	SetPlanBuilder(ctx context.Context, meta database.AdminConfigMeta, planID uint, builderID *uint) (*database.AdminConfigResult, error)
	SetSubscriptionBuilder(ctx context.Context, meta database.AdminConfigMeta, subscriptionID uint, builderID *uint) (*database.AdminConfigResult, error)

	// Plans (for assignment validation)
	GetPlanByID(ctx context.Context, id uint) (*database.Plan, error)

	// Source creation
	CreateProviderSourceAdmin(ctx context.Context, src database.ProviderSource) (*database.ProviderSource, error)
	UpsertSourceEntries(ctx context.Context, sourceID uint, entries []database.ProviderSourceEntry, syncStatus, syncError string) error

	// Builder enable/disable
	SetBuilderEnabled(ctx context.Context, id uint, enabled bool) (*database.SubscriptionBuilder, error)

	// Preview
	PreviewBuilder(ctx context.Context, builderID uint) (*database.PreviewBuilderResult, error)
}

var _ BuilderRepository = (*database.Service)(nil)

// BuilderService is the application boundary for Subscription Builder
// management. It owns no authentication: callers must have authorized the
// actor before invoking it.
type BuilderService struct {
	repo BuilderRepository
}

// NewBuilderService wires the builder boundary.
func NewBuilderService(repo BuilderRepository) *BuilderService {
	return &BuilderService{repo: repo}
}

// ---------------------------------------------------------------------------
// View types (credential-free projections for the browser)
// ---------------------------------------------------------------------------

// SourceView is the browser-safe projection of a ProviderSource.
// SubscriptionURL, HWID, UserAgent and Headers are intentionally omitted.
type SourceView struct {
	ID             uint       `json:"id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	Description    string     `json:"description"`
	Enabled        bool       `json:"enabled"`
	LastSyncAt     *time.Time `json:"last_sync_at"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  string     `json:"last_sync_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func sourceViewOf(s *database.ProviderSource) SourceView {
	return SourceView{
		ID:             s.ID,
		Name:           s.Name,
		Type:           s.Type,
		Description:    s.Description,
		Enabled:        s.Enabled,
		LastSyncAt:     s.LastSyncAt,
		LastSyncStatus: s.LastSyncStatus,
		LastSyncError:  s.LastSyncError,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

// BuilderView is the browser-safe projection of a SubscriptionBuilder.
type BuilderView struct {
	ID           uint                `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Enabled      bool                `json:"enabled"`
	ProfileTitle string              `json:"profile_title"`
	SupportURL   string              `json:"support_url"`
	Announce     string              `json:"announce"`
	Version      int                 `json:"version"`
	Sources      []BuilderSourceView `json:"sources,omitempty"`
	Items        []BuilderItemView   `json:"items,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// BuilderSourceView is one source slot in a builder.
type BuilderSourceView struct {
	SourceID uint `json:"source_id"`
	Position int  `json:"position"`
}

// BuilderItemView is one selection rule in a builder.
type BuilderItemView struct {
	ID           uint    `json:"id"`
	Kind         string  `json:"kind"`
	SourceID     uint    `json:"source_id"`
	CountryCode  string  `json:"country_code,omitempty"`
	Fingerprint  string  `json:"fingerprint,omitempty"`
	OriginalName string  `json:"original_name,omitempty"`
	CustomName   *string `json:"custom_name,omitempty"`
	Description  string  `json:"description,omitempty"`
	Position     int     `json:"position"`
	Enabled      bool    `json:"enabled"`
}

func builderViewOf(b *database.SubscriptionBuilder) BuilderView {
	v := BuilderView{
		ID:           b.ID,
		Name:         b.Name,
		Description:  b.Description,
		Enabled:      b.Enabled,
		ProfileTitle: b.ProfileTitle,
		SupportURL:   b.SupportURL,
		Announce:     b.Announce,
		Version:      b.Version,
		CreatedAt:    b.CreatedAt,
		UpdatedAt:    b.UpdatedAt,
	}
	for _, s := range b.Sources {
		v.Sources = append(v.Sources, BuilderSourceView{SourceID: s.SourceID, Position: s.Position})
	}
	for _, item := range b.Items {
		v.Items = append(v.Items, BuilderItemView{
			ID:           item.ID,
			Kind:         item.Kind,
			SourceID:     item.SourceID,
			CountryCode:  item.CountryCode,
			Fingerprint:  item.Fingerprint,
			OriginalName: item.OriginalName,
			CustomName:   item.CustomName,
			Description:  item.Description,
			Position:     item.Position,
			Enabled:      item.Enabled,
		})
	}
	return v
}

// ---------------------------------------------------------------------------
// Source operations
// ---------------------------------------------------------------------------

// ListSources returns all provider sources (credentials stripped).
func (s *BuilderService) ListSources(ctx context.Context) ([]SourceView, error) {
	sources, err := s.repo.ListProviderSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	views := make([]SourceView, 0, len(sources))
	for i := range sources {
		views = append(views, sourceViewOf(&sources[i]))
	}
	return views, nil
}

// GetSource returns one provider source (credentials stripped).
func (s *BuilderService) GetSource(ctx context.Context, id uint) (*SourceView, error) {
	src, err := s.repo.GetProviderSourceByID(ctx, id)
	if err != nil {
		return nil, err // ErrProviderSourceNotFound propagates as-is
	}
	v := sourceViewOf(src)
	return &v, nil
}

// UpdateSourceInput holds the mutable admin fields for a ProviderSource.
type UpdateSourceInput struct {
	Name        string
	Description string
	Type        string
}

// UpdateSource updates the non-credential metadata of a provider source.
// Credentials are NOT updated here — they are managed out-of-band.
func (s *BuilderService) UpdateSource(ctx context.Context, id uint, in UpdateSourceInput) (*SourceView, error) {
	// Load current to preserve credentials.
	current, err := s.repo.GetProviderSourceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateProviderSource(ctx, id, database.ProviderSourceUpdateInput{
		Name:            in.Name,
		Description:     in.Description,
		Type:            in.Type,
		SubscriptionURL: current.SubscriptionURL,
		HWID:            current.HWID,
		UserAgent:       current.UserAgent,
		Headers:         current.Headers,
	})
	if err != nil {
		return nil, fmt.Errorf("update source: %w", err)
	}
	v := sourceViewOf(updated)
	return &v, nil
}

// SetSourceEnabled enables or disables a provider source.
func (s *BuilderService) SetSourceEnabled(ctx context.Context, id uint, enabled bool) (*SourceView, error) {
	src, err := s.repo.SetProviderSourceEnabled(ctx, id, enabled)
	if err != nil {
		return nil, err
	}
	v := sourceViewOf(src)
	return &v, nil
}

// ListSourceEntries returns the present catalogue entries for a source.
// countryCode="" returns entries without a country; "*" returns all.
func (s *BuilderService) ListSourceEntries(ctx context.Context, sourceID uint, countryCode string) ([]database.ProviderSourceEntry, error) {
	return s.repo.GetSourceEntries(ctx, sourceID, countryCode)
}

// ---------------------------------------------------------------------------
// Builder operations
// ---------------------------------------------------------------------------

// ListBuilders returns all builders (without Sources/Items preloaded).
func (s *BuilderService) ListBuilders(ctx context.Context) ([]BuilderView, error) {
	builders, err := s.repo.ListBuilders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list builders: %w", err)
	}
	views := make([]BuilderView, 0, len(builders))
	for i := range builders {
		views = append(views, builderViewOf(&builders[i]))
	}
	return views, nil
}

// GetBuilder returns one builder with Sources and Items preloaded.
func (s *BuilderService) GetBuilder(ctx context.Context, id uint) (*BuilderView, error) {
	b, err := s.repo.GetBuilder(ctx, id)
	if err != nil {
		return nil, err // ErrBuilderNotFound propagates as-is
	}
	v := builderViewOf(b)
	return &v, nil
}

// CreateBuilderInput is the request body for creating a builder.
type CreateBuilderInput struct {
	Actor        string
	RequestKey   string
	Name         string
	Description  string
	Enabled      bool
	ProfileTitle string
	SupportURL   string
	Announce     string
}

// CreateBuilder creates a new SubscriptionBuilder.
func (s *BuilderService) CreateBuilder(ctx context.Context, in CreateBuilderInput) (*BuilderView, *database.AdminAuditLog, error) {
	result, err := s.repo.CreateBuilder(ctx,
		database.AdminConfigMeta{Actor: in.Actor, RequestKey: in.RequestKey},
		database.BuilderCreateInput{
			Name:         in.Name,
			Description:  in.Description,
			Enabled:      in.Enabled,
			ProfileTitle: in.ProfileTitle,
			SupportURL:   in.SupportURL,
			Announce:     in.Announce,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.repo.GetBuilder(ctx, result.TargetID)
	if err != nil {
		return nil, &result.Audit, fmt.Errorf("reload builder after create: %w", err)
	}
	v := builderViewOf(b)
	return &v, &result.Audit, nil
}

// UpdateBuilderInput is the request body for updating a builder.
type UpdateBuilderInput struct {
	Actor        string
	RequestKey   string
	ID           uint
	Version      int
	Name         string
	Description  string
	Enabled      bool
	ProfileTitle string
	SupportURL   string
	Announce     string
}

// UpdateBuilder updates an existing SubscriptionBuilder.
func (s *BuilderService) UpdateBuilder(ctx context.Context, in UpdateBuilderInput) (*BuilderView, *database.AdminAuditLog, error) {
	result, err := s.repo.UpdateBuilder(ctx,
		database.AdminConfigMeta{Actor: in.Actor, RequestKey: in.RequestKey},
		database.BuilderUpdateInput{
			ID:           in.ID,
			Version:      in.Version,
			Name:         in.Name,
			Description:  in.Description,
			Enabled:      in.Enabled,
			ProfileTitle: in.ProfileTitle,
			SupportURL:   in.SupportURL,
			Announce:     in.Announce,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.repo.GetBuilder(ctx, result.TargetID)
	if err != nil {
		return nil, &result.Audit, fmt.Errorf("reload builder after update: %w", err)
	}
	v := builderViewOf(b)
	return &v, &result.Audit, nil
}

// SetBuilderSources replaces the source list for a builder.
func (s *BuilderService) SetBuilderSources(ctx context.Context, builderID uint, sourceIDs []uint) error {
	// Verify builder exists.
	if _, err := s.repo.GetBuilder(ctx, builderID); err != nil {
		return err
	}
	return s.repo.SetBuilderSources(ctx, builderID, sourceIDs)
}

// UpsertBuilderItemInput is the request body for creating/updating an item.
type UpsertBuilderItemInput struct {
	ID           uint // 0 = create
	BuilderID    uint
	Kind         string
	SourceID     uint
	CountryCode  string
	Fingerprint  string
	OriginalName string
	CustomName   *string
	Description  string
	Position     int
	Enabled      bool
}

// UpsertBuilderItem creates or updates a builder item rule.
func (s *BuilderService) UpsertBuilderItem(ctx context.Context, in UpsertBuilderItemInput) (*BuilderItemView, error) {
	item := database.SubscriptionBuilderItem{
		ID:           in.ID,
		BuilderID:    in.BuilderID,
		Kind:         in.Kind,
		SourceID:     in.SourceID,
		CountryCode:  in.CountryCode,
		Fingerprint:  in.Fingerprint,
		OriginalName: in.OriginalName,
		CustomName:   in.CustomName,
		Description:  in.Description,
		Position:     in.Position,
		Enabled:      in.Enabled,
	}
	saved, err := s.repo.UpsertBuilderItem(ctx, item)
	if err != nil {
		return nil, err
	}
	v := BuilderItemView{
		ID:           saved.ID,
		Kind:         saved.Kind,
		SourceID:     saved.SourceID,
		CountryCode:  saved.CountryCode,
		Fingerprint:  saved.Fingerprint,
		OriginalName: saved.OriginalName,
		CustomName:   saved.CustomName,
		Description:  saved.Description,
		Position:     saved.Position,
		Enabled:      saved.Enabled,
	}
	return &v, nil
}

// DeleteBuilderItem removes a builder item rule.
func (s *BuilderService) DeleteBuilderItem(ctx context.Context, itemID uint) error {
	return s.repo.DeleteBuilderItem(ctx, itemID)
}

// ReorderBuilderItems updates item positions.
func (s *BuilderService) ReorderBuilderItems(ctx context.Context, builderID uint, itemIDs []uint) error {
	return s.repo.ReorderBuilderItems(ctx, builderID, itemIDs)
}

// ---------------------------------------------------------------------------
// Assignment operations
// ---------------------------------------------------------------------------

// SetPlanBuilderInput is the request body for assigning a builder to a plan.
type SetPlanBuilderInput struct {
	Actor      string
	RequestKey string
	PlanID     uint
	BuilderID  *uint // nil = clear
}

// SetPlanBuilder assigns or clears the default builder for a plan.
func (s *BuilderService) SetPlanBuilder(ctx context.Context, in SetPlanBuilderInput) (*database.AdminAuditLog, error) {
	// Validate plan exists.
	if _, err := s.repo.GetPlanByID(ctx, in.PlanID); err != nil {
		if errors.Is(err, database.ErrPlanNotFound) {
			return nil, fmt.Errorf("plan %d not found: %w", in.PlanID, database.ErrAdminInvalidRequest)
		}
		return nil, fmt.Errorf("validate plan: %w", err)
	}
	// Validate builder exists (if setting, not clearing).
	if in.BuilderID != nil {
		if _, err := s.repo.GetBuilder(ctx, *in.BuilderID); err != nil {
			return nil, err
		}
	}
	result, err := s.repo.SetPlanBuilder(ctx,
		database.AdminConfigMeta{Actor: in.Actor, RequestKey: in.RequestKey},
		in.PlanID, in.BuilderID,
	)
	if err != nil {
		return nil, err
	}
	return &result.Audit, nil
}

// SetSubscriptionBuilderInput is the request body for assigning a builder to a subscription.
type SetSubscriptionBuilderInput struct {
	Actor          string
	RequestKey     string
	SubscriptionID uint
	BuilderID      *uint // nil = clear (fall back to plan default)
}

// SetSubscriptionBuilder assigns or clears the per-subscription builder override.
func (s *BuilderService) SetSubscriptionBuilder(ctx context.Context, in SetSubscriptionBuilderInput) (*database.AdminAuditLog, error) {
	// Validate builder exists (if setting, not clearing).
	if in.BuilderID != nil {
		if _, err := s.repo.GetBuilder(ctx, *in.BuilderID); err != nil {
			return nil, err
		}
	}
	result, err := s.repo.SetSubscriptionBuilder(ctx,
		database.AdminConfigMeta{Actor: in.Actor, RequestKey: in.RequestKey},
		in.SubscriptionID, in.BuilderID,
	)
	if err != nil {
		return nil, err
	}
	return &result.Audit, nil
}

// ---------------------------------------------------------------------------
// Source creation
// ---------------------------------------------------------------------------

// CreateSourceInput holds the fields for creating a new ProviderSource.
type CreateSourceInput struct {
	Name            string
	Description     string
	Type            string
	SubscriptionURL string
	HWID            string
	UserAgent       string
	Headers         string // JSON object, default "{}"
	Enabled         bool
}

// CreateSource creates a new ProviderSource. Credentials are accepted and
// stored; they are never returned to the browser (SourceView strips them).
func (s *BuilderService) CreateSource(ctx context.Context, in CreateSourceInput) (*SourceView, error) {
	headers := in.Headers
	if headers == "" {
		headers = "{}"
	}
	src := database.ProviderSource{
		Name:            in.Name,
		Description:     in.Description,
		Type:            in.Type,
		SubscriptionURL: in.SubscriptionURL,
		HWID:            in.HWID,
		UserAgent:       in.UserAgent,
		Headers:         headers,
		Enabled:         in.Enabled,
	}
	created, err := s.repo.CreateProviderSourceAdmin(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("create source: %w", err)
	}
	v := sourceViewOf(created)
	return &v, nil
}

// RefreshSource triggers a catalogue refresh for a provider source. In the
// current implementation the refresh is synchronous and best-effort: the
// caller supplies the parsed entries; the service upserts them and updates
// the sync metadata. A full async fetch pipeline is out of scope for this
// backend phase.
//
// entries must already be parsed from the upstream feed by the caller.
// syncStatus should be "ok" on success or a short error code on failure.
// syncError is a stable, credential-free error description (max 64 chars).
func (s *BuilderService) RefreshSource(ctx context.Context, sourceID uint, entries []database.ProviderSourceEntry, syncStatus, syncError string) (*SourceView, error) {
	// Verify source exists.
	if _, err := s.repo.GetProviderSourceByID(ctx, sourceID); err != nil {
		return nil, err
	}
	if err := s.repo.UpsertSourceEntries(ctx, sourceID, entries, syncStatus, syncError); err != nil {
		return nil, fmt.Errorf("refresh source entries: %w", err)
	}
	// Reload to return updated sync metadata.
	src, err := s.repo.GetProviderSourceByID(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("reload source after refresh: %w", err)
	}
	v := sourceViewOf(src)
	return &v, nil
}

// ---------------------------------------------------------------------------
// Builder enable / disable
// ---------------------------------------------------------------------------

// EnableBuilder enables a builder (non-audited structural toggle).
func (s *BuilderService) EnableBuilder(ctx context.Context, id uint) (*BuilderView, error) {
	b, err := s.repo.SetBuilderEnabled(ctx, id, true)
	if err != nil {
		return nil, err
	}
	v := builderViewOf(b)
	return &v, nil
}

// DisableBuilder disables a builder (non-audited structural toggle).
func (s *BuilderService) DisableBuilder(ctx context.Context, id uint) (*BuilderView, error) {
	b, err := s.repo.SetBuilderEnabled(ctx, id, false)
	if err != nil {
		return nil, err
	}
	v := builderViewOf(b)
	return &v, nil
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

// PreviewBuilder performs a dry-run build of a builder against the current
// catalogue. No data is written.
func (s *BuilderService) PreviewBuilder(ctx context.Context, builderID uint) (*database.PreviewBuilderResult, error) {
	// Verify builder exists first (GetBuilder is called inside PreviewBuilder
	// in the DB layer, but we want a clean ErrBuilderNotFound here).
	if _, err := s.repo.GetBuilder(ctx, builderID); err != nil {
		return nil, err
	}
	return s.repo.PreviewBuilder(ctx, builderID)
}
