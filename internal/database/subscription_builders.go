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

// ListBuilders returns all builders ordered by name. Sources and Items are NOT
// preloaded — use GetBuilder for the full record.
func (s *Service) ListBuilders(ctx context.Context) ([]SubscriptionBuilder, error) {
	var builders []SubscriptionBuilder
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&builders).Error; err != nil {
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
// Builder source/item management (not audited — structural, low-risk)
// ---------------------------------------------------------------------------

// SetBuilderSources replaces the full source list for a builder in one
// transaction. positions are assigned by the slice order (0-based).
func (s *Service) SetBuilderSources(ctx context.Context, builderID uint, sourceIDs []uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

// UpsertBuilderItem creates or updates a single item rule inside a builder.
// If item.ID == 0 a new row is inserted; otherwise the existing row is updated.
func (s *Service) UpsertBuilderItem(ctx context.Context, item SubscriptionBuilderItem) (*SubscriptionBuilderItem, error) {
	if item.Kind != BuilderItemKindCountry && item.Kind != BuilderItemKindNode {
		return nil, fmt.Errorf("invalid builder item kind %q: %w", item.Kind, ErrProviderSourceInvalid)
	}
	if item.ID == 0 {
		if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
			return nil, fmt.Errorf("create builder item: %w", err)
		}
	} else {
		if err := s.db.WithContext(ctx).Save(&item).Error; err != nil {
			return nil, fmt.Errorf("update builder item: %w", err)
		}
	}
	return &item, nil
}

// DeleteBuilderItem removes a single item rule by ID.
func (s *Service) DeleteBuilderItem(ctx context.Context, itemID uint) error {
	result := s.db.WithContext(ctx).Delete(&SubscriptionBuilderItem{}, itemID)
	if result.Error != nil {
		return fmt.Errorf("delete builder item: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("builder item %d not found: %w", itemID, ErrBuilderNotFound)
	}
	return nil
}

// ReorderBuilderItems updates the position of every item in the provided slice.
// The caller supplies the full ordered list; positions are assigned 0-based.
func (s *Service) ReorderBuilderItems(ctx context.Context, builderID uint, itemIDs []uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Mark all existing entries as absent; we'll flip present=true for
		// those that appear in the new list.
		if err := tx.Model(&ProviderSourceEntry{}).
			Where("source_id = ?", sourceID).
			Updates(map[string]any{"present": false}).Error; err != nil {
			return fmt.Errorf("mark entries absent: %w", err)
		}

		for i := range entries {
			entries[i].SourceID = sourceID
			entries[i].Present = true
			entries[i].LastSeenAt = now
			entries[i].UpstreamPosition = i

			// Upsert by (source_id, fingerprint).
			var existing ProviderSourceEntry
			err := tx.Where("source_id = ? AND fingerprint = ?", sourceID, entries[i].Fingerprint).
				First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err2 := tx.Create(&entries[i]).Error; err2 != nil {
					return fmt.Errorf("insert source entry %q: %w", entries[i].Fingerprint, err2)
				}
			} else if err != nil {
				return fmt.Errorf("lookup source entry %q: %w", entries[i].Fingerprint, err)
			} else {
				entries[i].ID = existing.ID
				if err2 := tx.Save(&entries[i]).Error; err2 != nil {
					return fmt.Errorf("update source entry %q: %w", entries[i].Fingerprint, err2)
				}
			}
		}

		// Update source sync metadata.
		if err := tx.Model(&ProviderSource{}).Where("id = ?", sourceID).
			Updates(map[string]any{
				"last_sync_at":     now,
				"last_sync_status": syncStatus,
				"last_sync_error":  syncError,
			}).Error; err != nil {
			return fmt.Errorf("update source sync metadata: %w", err)
		}
		return nil
	})
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

	// 2. Name-based fallback.
	var entries []ProviderSourceEntry
	if err := s.db.WithContext(ctx).
		Where("source_id = ? AND original_name = ? AND present = ?", item.SourceID, item.OriginalName, true).
		Find(&entries).Error; err != nil {
		return FingerprintResolution{}, fmt.Errorf("resolve by name: %w", err)
	}
	switch len(entries) {
	case 0:
		return FingerprintResolution{Status: FingerprintStatusMissing}, nil
	case 1:
		return FingerprintResolution{Entry: &entries[0], Status: FingerprintStatusFallback}, nil
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
