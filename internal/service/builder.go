package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
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

	// Source catalogue (sync + statistics)
	SyncSourceEntries(ctx context.Context, sourceID uint, entries []database.ProviderSourceEntry, syncStatus, syncError string) (database.SourceSyncCounts, error)
	MarkSourceSyncFailed(ctx context.Context, sourceID uint, syncError string) error
	ListSourceEntriesAll(ctx context.Context, sourceID uint) ([]database.ProviderSourceEntry, error)
	SourceCatalogueStatsAll(ctx context.Context) (map[uint]database.SourceCatalogueStats, error)
	SourceCatalogueStatsFor(ctx context.Context, sourceID uint) (database.SourceCatalogueStats, error)

	// Builder enable/disable
	SetBuilderEnabled(ctx context.Context, id uint, enabled bool) (*database.SubscriptionBuilder, error)

	// Preview
	PreviewBuilder(ctx context.Context, builderID uint) (*database.PreviewBuilderResult, error)
}

var _ BuilderRepository = (*database.Service)(nil)

// FetchedCatalogue is one fetched and parsed provider source response.
type FetchedCatalogue struct {
	Format     string
	Entries    []database.ProviderSourceEntry
	Skipped    int
	Duplicates int
}

// SourceCatalogueFetcher fetches and parses a provider source on the server
// (subserver.FetchSourceCatalogue in production). A failure should carry a
// stable, credential-free code via a SyncCode() string method.
type SourceCatalogueFetcher func(ctx context.Context, source database.ProviderSource) (*FetchedCatalogue, error)

// ErrCatalogueFetcherUnavailable is returned when no fetcher is wired.
var ErrCatalogueFetcherUnavailable = errors.New("source catalogue fetcher is not configured")

// sourceSyncTimeout bounds one catalogue sync (fetch + parse + store).
const sourceSyncTimeout = 30 * time.Second

// BuilderService is the application boundary for Subscription Builder
// management. It owns no authentication: callers must have authorized the
// actor before invoking it.
type BuilderService struct {
	repo    BuilderRepository
	fetcher SourceCatalogueFetcher

	syncMu  sync.Mutex
	syncing map[uint]bool
}

// NewBuilderService wires the builder boundary.
func NewBuilderService(repo BuilderRepository) *BuilderService {
	return &BuilderService{repo: repo, syncing: make(map[uint]bool)}
}

// SetCatalogueFetcher wires the server-side source fetcher/parser.
func (s *BuilderService) SetCatalogueFetcher(f SourceCatalogueFetcher) {
	s.fetcher = f
}

// ---------------------------------------------------------------------------
// View types (credential-free projections for the browser)
// ---------------------------------------------------------------------------

// SourceView is the browser-safe projection of a ProviderSource.
// SubscriptionURL, HWID, UserAgent and Headers are intentionally omitted.
type SourceView struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	// Type is the expected response format (database.SourceFormats); legacy
	// free-text values are reported as "auto".
	Type           string     `json:"type"`
	Description    string     `json:"description"`
	Enabled        bool       `json:"enabled"`
	LastSyncAt     *time.Time `json:"last_sync_at"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  string     `json:"last_sync_error"`
	// Catalogue summarizes the entries stored by the last successful sync.
	Catalogue database.SourceCatalogueStats `json:"catalogue"`
	CreatedAt time.Time                     `json:"created_at"`
	UpdatedAt time.Time                     `json:"updated_at"`
}

func sourceViewOf(s *database.ProviderSource) SourceView {
	return SourceView{
		ID:             s.ID,
		Name:           s.Name,
		Type:           database.NormalizeSourceFormat(s.Type),
		Description:    s.Description,
		Enabled:        s.Enabled,
		LastSyncAt:     s.LastSyncAt,
		LastSyncStatus: s.LastSyncStatus,
		LastSyncError:  s.LastSyncError,
		Catalogue:      database.SourceCatalogueStats{ByCountry: []database.SourceCountryCount{}, Protocols: map[string]int{}},
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

// sourceView returns the projection with the current catalogue statistics.
func (s *BuilderService) sourceView(ctx context.Context, src *database.ProviderSource) (*SourceView, error) {
	v := sourceViewOf(src)
	stats, err := s.repo.SourceCatalogueStatsFor(ctx, src.ID)
	if err != nil {
		return nil, fmt.Errorf("source catalogue stats: %w", err)
	}
	v.Catalogue = stats
	return &v, nil
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

// ListSources returns all provider sources (credentials stripped) with the
// statistics of their own catalogue.
func (s *BuilderService) ListSources(ctx context.Context) ([]SourceView, error) {
	sources, err := s.repo.ListProviderSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	stats, err := s.repo.SourceCatalogueStatsAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	views := make([]SourceView, 0, len(sources))
	for i := range sources {
		v := sourceViewOf(&sources[i])
		if st, ok := stats[sources[i].ID]; ok {
			v.Catalogue = st
		}
		views = append(views, v)
	}
	return views, nil
}

// GetSource returns one provider source (credentials stripped).
func (s *BuilderService) GetSource(ctx context.Context, id uint) (*SourceView, error) {
	src, err := s.repo.GetProviderSourceByID(ctx, id)
	if err != nil {
		return nil, err // ErrProviderSourceNotFound propagates as-is
	}
	return s.sourceView(ctx, src)
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
	name, format, err := validateSourceMeta(in.Name, in.Type)
	if err != nil {
		return nil, err
	}
	// Load current to preserve credentials.
	current, err := s.repo.GetProviderSourceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdateProviderSource(ctx, id, database.ProviderSourceUpdateInput{
		Name:            name,
		Description:     strings.TrimSpace(in.Description),
		Type:            format,
		SubscriptionURL: current.SubscriptionURL,
		HWID:            current.HWID,
		UserAgent:       current.UserAgent,
		Headers:         current.Headers,
	})
	if err != nil {
		return nil, fmt.Errorf("update source: %w", err)
	}
	return s.sourceView(ctx, updated)
}

// SetSourceEnabled enables or disables a provider source.
func (s *BuilderService) SetSourceEnabled(ctx context.Context, id uint, enabled bool) (*SourceView, error) {
	src, err := s.repo.SetProviderSourceEnabled(ctx, id, enabled)
	if err != nil {
		return nil, err
	}
	return s.sourceView(ctx, src)
}

// ListAllSourceEntries returns every catalogue entry of a source, including
// entries that disappeared upstream (present=false).
func (s *BuilderService) ListAllSourceEntries(ctx context.Context, sourceID uint) ([]database.ProviderSourceEntry, error) {
	if _, err := s.repo.GetProviderSourceByID(ctx, sourceID); err != nil {
		return nil, err
	}
	return s.repo.ListSourceEntriesAll(ctx, sourceID)
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

// SourceFieldError names the invalid field of a source create/update. It wraps
// database.ErrProviderSourceInvalid and never contains the submitted value.
type SourceFieldError struct {
	Field string
}

func (e *SourceFieldError) Error() string { return "invalid provider source field: " + e.Field }
func (e *SourceFieldError) Unwrap() error { return database.ErrProviderSourceInvalid }

// validateSourceMeta checks the name and the format (empty = auto).
func validateSourceMeta(name, format string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 {
		return "", "", &SourceFieldError{Field: "name"}
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = database.SourceFormatAuto
	}
	if !database.ValidSourceFormat(format) {
		return "", "", &SourceFieldError{Field: "type"}
	}
	return name, format, nil
}

// validateSourceRequest checks the fetch configuration with the same rules as
// the runtime fetch (database.ProviderSource.RequestConfiguration) and names
// the offending field.
func validateSourceRequest(src database.ProviderSource) error {
	u, err := url.Parse(strings.TrimSpace(src.SubscriptionURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Fragment != "" {
		return &SourceFieldError{Field: "subscription_url"}
	}
	probe := src
	probe.Enabled = true
	if _, _, err := probe.RequestConfiguration(); err != nil {
		if raw := strings.TrimSpace(src.Headers); raw != "" && raw != "{}" {
			var headers map[string]string
			if json.Unmarshal([]byte(raw), &headers) != nil {
				return &SourceFieldError{Field: "headers"}
			}
		}
		probe.Headers = "{}"
		if _, _, err := probe.RequestConfiguration(); err == nil {
			return &SourceFieldError{Field: "headers"}
		}
		probe.HWID = ""
		if _, _, err := probe.RequestConfiguration(); err == nil {
			return &SourceFieldError{Field: "hwid"}
		}
		return &SourceFieldError{Field: "user_agent"}
	}
	return nil
}

// CreateSource creates a new ProviderSource and immediately runs the initial
// catalogue sync. Credentials are accepted and stored; they are never
// returned to the browser (SourceView strips them). The source is created
// even when the initial sync fails: the sync result reports the error and
// the admin can fix the upstream and sync again.
func (s *BuilderService) CreateSource(ctx context.Context, in CreateSourceInput) (*SourceView, *SourceSyncResult, error) {
	name, format, err := validateSourceMeta(in.Name, in.Type)
	if err != nil {
		return nil, nil, err
	}
	headers := strings.TrimSpace(in.Headers)
	if headers == "" {
		headers = "{}"
	}
	src := database.ProviderSource{
		Name:            name,
		Description:     strings.TrimSpace(in.Description),
		Type:            format,
		SubscriptionURL: strings.TrimSpace(in.SubscriptionURL),
		HWID:            strings.TrimSpace(in.HWID),
		UserAgent:       strings.TrimSpace(in.UserAgent),
		Headers:         headers,
		Enabled:         in.Enabled,
	}
	if err := validateSourceRequest(src); err != nil {
		return nil, nil, err
	}
	created, err := s.repo.CreateProviderSourceAdmin(ctx, src)
	if err != nil {
		return nil, nil, fmt.Errorf("create source: %w", err)
	}

	result, err := s.SyncSource(ctx, created.ID)
	if err != nil {
		// The source exists; only the sync could not run (e.g. no fetcher,
		// database error). Report the created source without a sync result.
		view, viewErr := s.sourceView(ctx, created)
		if viewErr != nil {
			return nil, nil, viewErr
		}
		return view, nil, nil
	}
	return &result.Source, result, nil
}

// SourceSyncResult is the outcome of one catalogue sync.
type SourceSyncResult struct {
	Source SourceView `json:"source"`
	// Status is database.SourceSync*: ok | partial | error.
	Status string `json:"status"`
	// Error is the stable failure code (status=error) or "skipped:N"
	// (status=partial). It never contains the URL or credentials.
	Error  string `json:"error"`
	Format string `json:"format"`
	database.SourceSyncCounts
	// Skipped counts servers of the response that could not be parsed.
	Skipped int `json:"skipped"`
	// Duplicates counts repeated servers merged into one entry.
	Duplicates int `json:"duplicates"`
}

// SyncSource fetches the upstream URL on the server, parses it with the /sub
// parser, stores the catalogue (fingerprints, countries, protocols) and
// updates the sync status. On a fetch/parse failure the previous catalogue is
// kept and the source is marked status=error with a stable code.
func (s *BuilderService) SyncSource(ctx context.Context, sourceID uint) (*SourceSyncResult, error) {
	if s.fetcher == nil {
		return nil, ErrCatalogueFetcherUnavailable
	}
	src, err := s.repo.GetProviderSourceByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if !s.beginSync(sourceID) {
		return nil, database.ErrProviderSourceSyncInProgress
	}
	defer s.endSync(sourceID)

	syncCtx, cancel := context.WithTimeout(ctx, sourceSyncTimeout)
	defer cancel()

	catalogue, fetchErr := s.fetcher(syncCtx, *src)
	if fetchErr != nil {
		code := "internal"
		var coded interface{ SyncCode() string }
		if errors.As(fetchErr, &coded) {
			code = coded.SyncCode()
		} else if syncCtx.Err() != nil {
			code = "timeout"
		}
		if err := s.repo.MarkSourceSyncFailed(ctx, sourceID, code); err != nil {
			return nil, fmt.Errorf("record sync failure: %w", err)
		}
		return s.syncResult(ctx, sourceID, &SourceSyncResult{Status: database.SourceSyncError, Error: code})
	}

	status, detail := database.SourceSyncOK, ""
	if catalogue.Skipped > 0 {
		status, detail = database.SourceSyncPartial, fmt.Sprintf("skipped:%d", catalogue.Skipped)
	}
	counts, err := s.repo.SyncSourceEntries(ctx, sourceID, catalogue.Entries, status, detail)
	if err != nil {
		return nil, fmt.Errorf("store source catalogue: %w", err)
	}
	return s.syncResult(ctx, sourceID, &SourceSyncResult{
		Status: status, Error: detail, Format: catalogue.Format, SourceSyncCounts: counts,
		Skipped: catalogue.Skipped, Duplicates: catalogue.Duplicates,
	})
}

func (s *BuilderService) syncResult(ctx context.Context, sourceID uint, r *SourceSyncResult) (*SourceSyncResult, error) {
	src, err := s.repo.GetProviderSourceByID(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("reload source after sync: %w", err)
	}
	view, err := s.sourceView(ctx, src)
	if err != nil {
		return nil, err
	}
	r.Source = *view
	if r.Status == database.SourceSyncError {
		r.Total = view.Catalogue.Entries // the kept catalogue
	}
	return r, nil
}

func (s *BuilderService) beginSync(id uint) bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if s.syncing == nil {
		s.syncing = make(map[uint]bool)
	}
	if s.syncing[id] {
		return false
	}
	s.syncing[id] = true
	return true
}

func (s *BuilderService) endSync(id uint) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	delete(s.syncing, id)
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
