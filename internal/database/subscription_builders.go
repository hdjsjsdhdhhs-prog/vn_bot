package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Read helpers
// ---------------------------------------------------------------------------

// GetBuilder returns a single SubscriptionBuilder by ID, including its Sources
// and Items ordered by position.
func (s *Service) GetBuilder(ctx context.Context, id uint) (*SubscriptionBuilder, error) {
	var b SubscriptionBuilder
	err := s.db.WithContext(ctx).
		Preload("Sources", func(db *gorm.DB) *gorm.DB {
			return db.Order("position ASC")
		}).
		Preload("Items", func(db *gorm.DB) *gorm.DB {
			return db.Order("position ASC")
		}).
		First(&b, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBuilderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription builder: %w", err)
	}
	return &b, nil
}

// ListBuilders returns all builders ordered by name, with their Sources and
// Items ordered by position exactly like GetBuilder. The admin list shows the
// linked sources and rules of every builder; without the preload it reported
// "0 sources · 0 rules" for configured builders.
func (s *Service) ListBuilders(ctx context.Context) ([]SubscriptionBuilder, error) {
	var builders []SubscriptionBuilder
	err := s.db.WithContext(ctx).
		Preload("Sources", func(db *gorm.DB) *gorm.DB {
			return db.Order("position ASC")
		}).
		Preload("Items", func(db *gorm.DB) *gorm.DB {
			return db.Order("position ASC")
		}).
		Order("name ASC").
		Find(&builders).Error
	if err != nil {
		return nil, fmt.Errorf("list subscription builders: %w", err)
	}
	return builders, nil
}

// GetSourceEntries returns all present entries for a provider source, ordered
// by upstream_position. Pass countryCode="" to get entries without a country.
// Pass countryCode="*" to get all entries regardless of country.
func (s *Service) GetSourceEntries(ctx context.Context, sourceID uint, countryCode string) ([]ProviderSourceEntry, error) {
	q := s.db.WithContext(ctx).
		Where("source_id = ? AND present = ?", sourceID, true).
		Order("upstream_position ASC")
	if countryCode != "*" {
		q = q.Where("country_code = ?", countryCode)
	}
	var entries []ProviderSourceEntry
	if err := q.Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("get source entries: %w", err)
	}
	return entries, nil
}

// ---------------------------------------------------------------------------
// Builder mutations (audited via runAdminConfigMutation)
// ---------------------------------------------------------------------------

// BuilderCreateInput holds the fields for creating a new SubscriptionBuilder.
type BuilderCreateInput struct {
	Name         string
	Description  string
	Enabled      bool
	ProfileTitle string
	SupportURL   string
	Announce     string
}

// builderAuditView is the credential-free projection stored in audit old/new.
type builderAuditView struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Enabled      bool   `json:"enabled"`
	ProfileTitle string `json:"profile_title"`
	SupportURL   string `json:"support_url"`
	Announce     string `json:"announce"`
	Version      int    `json:"version"`
}

func builderView(b *SubscriptionBuilder) builderAuditView {
	return builderAuditView{
		ID:           b.ID,
		Name:         b.Name,
		Description:  b.Description,
		Enabled:      b.Enabled,
		ProfileTitle: b.ProfileTitle,
		SupportURL:   b.SupportURL,
		Announce:     b.Announce,
		Version:      b.Version,
	}
}

// CreateBuilder creates a new SubscriptionBuilder and records an audit entry.
func (s *Service) CreateBuilder(ctx context.Context, meta AdminConfigMeta, in BuilderCreateInput) (*AdminConfigResult, error) {
	return s.runAdminConfigMutation(ctx, meta,
		AdminActionBuilderCreated, AdminTargetBuilder, 0, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			b := SubscriptionBuilder{
				Name:         in.Name,
				Description:  in.Description,
				Enabled:      in.Enabled,
				ProfileTitle: in.ProfileTitle,
				SupportURL:   in.SupportURL,
				Announce:     in.Announce,
				Version:      1,
			}
			if err := tx.Create(&b).Error; err != nil {
				if isUniqueConstraintError(err, "subscription_builders.name") {
					return configChange{}, ErrBuilderNameTaken
				}
				return configChange{}, fmt.Errorf("create subscription builder: %w", err)
			}
			if !in.Enabled {
				// GORM leaves a false bool with a column default out of the
				// INSERT, which would store the default (TRUE).
				if err := tx.Model(&SubscriptionBuilder{}).Where("id = ?", b.ID).UpdateColumn("enabled", false).Error; err != nil {
					return configChange{}, fmt.Errorf("store builder switch: %w", err)
				}
				b.Enabled = false
			}
			return configChange{
				targetID: b.ID,
				oldValue: nil,
				newValue: builderView(&b),
			}, nil
		},
	)
}

// BuilderUpdateInput holds the mutable fields for updating a SubscriptionBuilder.
// Version must match the current row version (optimistic locking).
type BuilderUpdateInput struct {
	ID           uint
	Version      int // must match current row; ErrBuilderVersionConflict on mismatch
	Name         string
	Description  string
	Enabled      bool
	ProfileTitle string
	SupportURL   string
	Announce     string
}

// UpdateBuilder updates an existing SubscriptionBuilder and records an audit entry.
func (s *Service) UpdateBuilder(ctx context.Context, meta AdminConfigMeta, in BuilderUpdateInput) (*AdminConfigResult, error) {
	return s.runAdminConfigMutation(ctx, meta,
		AdminActionBuilderUpdated, AdminTargetBuilder, in.ID, in,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			var b SubscriptionBuilder
			if err := tx.First(&b, in.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return configChange{}, ErrBuilderNotFound
				}
				return configChange{}, fmt.Errorf("load subscription builder: %w", err)
			}
			if b.Version != in.Version {
				return configChange{}, ErrBuilderVersionConflict
			}
			old := builderView(&b)

			b.Name = in.Name
			b.Description = in.Description
			b.Enabled = in.Enabled
			b.ProfileTitle = in.ProfileTitle
			b.SupportURL = in.SupportURL
			b.Announce = in.Announce
			b.Version++

			if err := tx.Save(&b).Error; err != nil {
				if isUniqueConstraintError(err, "subscription_builders.name") {
					return configChange{}, ErrBuilderNameTaken
				}
				return configChange{}, fmt.Errorf("update subscription builder: %w", err)
			}
			return configChange{
				targetID: b.ID,
				oldValue: old,
				newValue: builderView(&b),
			}, nil
		},
	)
}

// ---------------------------------------------------------------------------
// Builder source/item management (single-step, not audited)
//
// These are the original fine-grained endpoints. Every rule operation is
// scoped to the builder of the request: a rule of another builder (or a
// missing one) is ErrBuilderItemNotFound and nothing is written. Every change
// bumps the builder version, so an editor holding an older version is refused
// by SaveBuilderConfiguration instead of overwriting it. The admin editor
// saves through SaveBuilderConfiguration (one transaction).
// ---------------------------------------------------------------------------

// ErrBuilderItemNotFound is returned when a rule does not exist in the builder
// of the request (missing, or owned by another builder: the two cases are not
// distinguished, so nothing is revealed about other builders).
var ErrBuilderItemNotFound = errors.New("subscription builder item not found")

// bumpBuilderVersion increments the version of an existing builder inside tx.
func bumpBuilderVersion(tx *gorm.DB, builderID uint, now time.Time) error {
	res := tx.Model(&SubscriptionBuilder{}).Where("id = ?", builderID).
		Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now})
	if res.Error != nil {
		return fmt.Errorf("bump builder version: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrBuilderNotFound
	}
	return nil
}

// createBuilderItem inserts item. GORM omits zero values of columns with a
// default from INSERT, so enabled=false would be stored as the column default
// (TRUE); it is written explicitly afterwards.
func createBuilderItem(tx *gorm.DB, item *SubscriptionBuilderItem) error {
	enabled := item.Enabled
	if err := tx.Create(item).Error; err != nil {
		return fmt.Errorf("create builder item: %w", err)
	}
	if !enabled {
		if err := tx.Model(&SubscriptionBuilderItem{}).Where("id = ?", item.ID).UpdateColumn("enabled", false).Error; err != nil {
			return fmt.Errorf("store builder item switch: %w", err)
		}
		item.Enabled = false
	}
	return nil
}

// builderItemColumns are the columns a rule update writes (builder_id never).
func builderItemColumns(item SubscriptionBuilderItem) map[string]any {
	return map[string]any{
		"kind": item.Kind, "source_id": item.SourceID, "country_code": item.CountryCode,
		"fingerprint": item.Fingerprint, "original_name": item.OriginalName, "custom_name": item.CustomName,
		"description": item.Description, "position": item.Position, "enabled": item.Enabled,
	}
}

// SetBuilderSources replaces the full source list for a builder in one
// transaction. positions are assigned by the slice order (0-based).
func (s *Service) SetBuilderSources(ctx context.Context, builderID uint, sourceIDs []uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := bumpBuilderVersion(tx, builderID, time.Now().UTC()); err != nil {
			return err
		}
		if err := tx.Where("builder_id = ?", builderID).Delete(&SubscriptionBuilderSource{}).Error; err != nil {
			return fmt.Errorf("clear builder sources: %w", err)
		}
		for i, sid := range sourceIDs {
			row := SubscriptionBuilderSource{BuilderID: builderID, SourceID: sid, Position: i}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("add builder source %d: %w", sid, err)
			}
		}
		return nil
	})
}

// UpsertBuilderItem creates or updates a single item rule inside the builder
// item.BuilderID. item.ID == 0 inserts a new row; otherwise the row must
// belong to that builder (ErrBuilderItemNotFound) and is updated in place —
// it is never moved to another builder nor created under a caller-chosen id.
func (s *Service) UpsertBuilderItem(ctx context.Context, item SubscriptionBuilderItem) (*SubscriptionBuilderItem, error) {
	if item.Kind != BuilderItemKindCountry && item.Kind != BuilderItemKindNode {
		return nil, fmt.Errorf("invalid builder item kind %q: %w", item.Kind, ErrProviderSourceInvalid)
	}
	item.Builder, item.Source = nil, nil
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := bumpBuilderVersion(tx, item.BuilderID, time.Now().UTC()); err != nil {
			return err
		}
		if item.ID == 0 {
			return createBuilderItem(tx, &item)
		}
		res := tx.Model(&SubscriptionBuilderItem{}).
			Where("id = ? AND builder_id = ?", item.ID, item.BuilderID).
			Updates(builderItemColumns(item))
		if res.Error != nil {
			return fmt.Errorf("update builder item: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrBuilderItemNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// DeleteBuilderItem removes one rule of the builder. A rule of another
// builder (or a missing one) is ErrBuilderItemNotFound and nothing is deleted.
func (s *Service) DeleteBuilderItem(ctx context.Context, builderID, itemID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND builder_id = ?", itemID, builderID).Delete(&SubscriptionBuilderItem{})
		if result.Error != nil {
			return fmt.Errorf("delete builder item: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrBuilderItemNotFound
		}
		return bumpBuilderVersion(tx, builderID, time.Now().UTC())
	})
}

// ReorderBuilderItems updates the position of every item in the provided slice.
// The caller supplies the ordered list; positions are assigned 0-based. Every
// id must be a distinct rule of this builder, otherwise nothing is changed
// (ErrBuilderItemNotFound for a foreign/missing id, ErrAdminInvalidRequest for
// a repeated one).
func (s *Service) ReorderBuilderItems(ctx context.Context, builderID uint, itemIDs []uint) error {
	seen := make(map[uint]bool, len(itemIDs))
	for _, id := range itemIDs {
		if seen[id] {
			return fmt.Errorf("%w: duplicate item id", ErrAdminInvalidRequest)
		}
		seen[id] = true
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := bumpBuilderVersion(tx, builderID, time.Now().UTC()); err != nil {
			return err
		}
		if len(itemIDs) > 0 {
			var owned int64
			if err := tx.Model(&SubscriptionBuilderItem{}).Where("builder_id = ? AND id IN ?", builderID, itemIDs).Count(&owned).Error; err != nil {
				return fmt.Errorf("check builder items: %w", err)
			}
			if int(owned) != len(itemIDs) {
				return ErrBuilderItemNotFound
			}
		}
		for i, id := range itemIDs {
			res := tx.Model(&SubscriptionBuilderItem{}).
				Where("id = ? AND builder_id = ?", id, builderID).
				Update("position", i)
			if res.Error != nil {
				return fmt.Errorf("reorder builder item %d: %w", id, res.Error)
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Plan / Subscription builder assignment (audited)
// ---------------------------------------------------------------------------

// SetPlanBuilder assigns (or clears, when builderID==0) the default builder
// for a plan and records an audit entry.
func (s *Service) SetPlanBuilder(ctx context.Context, meta AdminConfigMeta, planID uint, builderID *uint) (*AdminConfigResult, error) {
	type payload struct {
		PlanID    uint  `json:"plan_id"`
		BuilderID *uint `json:"builder_id"`
	}
	p := payload{PlanID: planID, BuilderID: builderID}
	return s.runAdminConfigMutation(ctx, meta,
		AdminActionPlanBuilderChanged, AdminTargetPlan, planID, p,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			var plan Plan
			if err := tx.First(&plan, planID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return configChange{}, fmt.Errorf("plan %d not found: %w", planID, ErrAdminInvalidRequest)
				}
				return configChange{}, fmt.Errorf("load plan: %w", err)
			}
			old := plan.SubscriptionBuilderID
			plan.SubscriptionBuilderID = builderID
			if err := tx.Model(&plan).Update("subscription_builder_id", builderID).Error; err != nil {
				return configChange{}, fmt.Errorf("set plan builder: %w", err)
			}
			return configChange{
				targetID: planID,
				oldValue: map[string]any{"plan_id": planID, "builder_id": old},
				newValue: map[string]any{"plan_id": planID, "builder_id": builderID},
			}, nil
		},
	)
}

// SetSubscriptionBuilder assigns (or clears, when builderID==nil) the per-
// subscription builder override and records an audit entry.
func (s *Service) SetSubscriptionBuilder(ctx context.Context, meta AdminConfigMeta, subscriptionID uint, builderID *uint) (*AdminConfigResult, error) {
	type payload struct {
		SubscriptionID uint  `json:"subscription_id"`
		BuilderID      *uint `json:"builder_id"`
	}
	p := payload{SubscriptionID: subscriptionID, BuilderID: builderID}
	return s.runAdminConfigMutation(ctx, meta,
		AdminActionSubscriptionBuilderChanged, AdminTargetSubscription, subscriptionID, p,
		func(tx *gorm.DB, now time.Time) (configChange, error) {
			var sub Subscription
			if err := tx.First(&sub, subscriptionID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return configChange{}, ErrSubscriptionNotFound
				}
				return configChange{}, fmt.Errorf("load subscription: %w", err)
			}
			old := sub.SubscriptionBuilderID
			if err := tx.Model(&sub).Update("subscription_builder_id", builderID).Error; err != nil {
				return configChange{}, fmt.Errorf("set subscription builder: %w", err)
			}
			return configChange{
				targetID:       subscriptionID,
				subscriptionID: subscriptionID,
				oldValue:       map[string]any{"subscription_id": subscriptionID, "builder_id": old},
				newValue:       map[string]any{"subscription_id": subscriptionID, "builder_id": builderID},
			}, nil
		},
	)
}

// ---------------------------------------------------------------------------
// Source entry catalogue
// ---------------------------------------------------------------------------

// UpsertSourceEntries replaces the full entry catalogue for a source in one
// transaction. Entries not present in the new list are marked present=false
// (soft-delete). Entries in the list are upserted by fingerprint.
// lastSyncStatus and lastSyncError are written to the provider_sources row.
func (s *Service) UpsertSourceEntries(ctx context.Context, sourceID uint, entries []ProviderSourceEntry, syncStatus, syncError string) error {
	_, err := s.SyncSourceEntries(ctx, sourceID, entries, syncStatus, syncError)
	return err
}

// ---------------------------------------------------------------------------
// Fallback fingerprint resolution (used by the build engine)
// ---------------------------------------------------------------------------

// FingerprintResolution is the result of resolving a node item against the
// source catalogue.
type FingerprintResolution struct {
	Entry    *ProviderSourceEntry
	Status   FingerprintStatus
	Conflict bool // true when >1 entries matched
}

// FingerprintStatus describes the outcome of a fingerprint lookup.
type FingerprintStatus string

const (
	FingerprintStatusMatched  FingerprintStatus = "matched"  // exact fingerprint hit
	FingerprintStatusFallback FingerprintStatus = "fallback" // matched by original_name (warning)
	FingerprintStatusMissing  FingerprintStatus = "missing"  // 0 matches
	FingerprintStatusConflict FingerprintStatus = "conflict" // >1 matches by name
)

// ResolveNodeItem resolves a BuilderItemKindNode item against the source
// catalogue following the fallback fingerprint spec:
//
//   - fingerprint non-empty → exact match; if 0 results fall through to name
//   - original_name match: 1 → fallback+warning; 0 → missing; >1 → conflict
func (s *Service) ResolveNodeItem(ctx context.Context, item SubscriptionBuilderItem) (FingerprintResolution, error) {
	// 1. Try exact fingerprint match.
	if item.Fingerprint != "" {
		var entry ProviderSourceEntry
		err := s.db.WithContext(ctx).
			Where("source_id = ? AND fingerprint = ? AND present = ?", item.SourceID, item.Fingerprint, true).
			First(&entry).Error
		if err == nil {
			return FingerprintResolution{Entry: &entry, Status: FingerprintStatusMatched}, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return FingerprintResolution{}, fmt.Errorf("resolve fingerprint: %w", err)
		}
		// Fall through to name-based lookup.
	}

	// 2. Name-based fallback (same tiered, URL-encoding tolerant comparison
	// as the /sub runtime).
	if item.OriginalName == "" {
		return FingerprintResolution{Status: FingerprintStatusMissing}, nil
	}

	var entries []ProviderSourceEntry
	if err := s.db.WithContext(ctx).
		Where("source_id = ? AND present = ?", item.SourceID, true).
		Order("upstream_position ASC").
		Find(&entries).Error; err != nil {
		return FingerprintResolution{}, fmt.Errorf("resolve by name: %w", err)
	}

	names := make([]string, len(entries))
	for i := range entries {
		names[i] = entries[i].OriginalName
	}

	matches := MatchOriginalName(item.OriginalName, names)
	switch len(matches) {
	case 0:
		return FingerprintResolution{Status: FingerprintStatusMissing}, nil
	case 1:
		return FingerprintResolution{Entry: &entries[matches[0]], Status: FingerprintStatusFallback}, nil
	default:
		return FingerprintResolution{Status: FingerprintStatusConflict, Conflict: true}, nil
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// isUniqueConstraintError returns true when err is a SQLite UNIQUE violation
// on the given column path (e.g. "subscription_builders.name").
// It uses a string-based heuristic because CGO-free SQLite drivers do not
// expose typed error codes.
func isUniqueConstraintError(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsAll(msg, "UNIQUE constraint failed", column)
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
