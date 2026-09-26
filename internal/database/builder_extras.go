package database

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// ProviderSource admin creation
// ---------------------------------------------------------------------------

// CreateProviderSourceAdmin creates a new ProviderSource with admin-supplied
// fields. Credentials (SubscriptionURL, HWID, UserAgent, Headers) are accepted
// here; the service layer must validate them before calling.
func (s *Service) CreateProviderSourceAdmin(ctx context.Context, src ProviderSource) (*ProviderSource, error) {
	if err := s.db.WithContext(ctx).Create(&src).Error; err != nil {
		return nil, fmt.Errorf("create provider source: %w", err)
	}
	return &src, nil
}

// ---------------------------------------------------------------------------
// Builder enable / disable (non-audited structural toggle)
// ---------------------------------------------------------------------------

// SetBuilderEnabled enables or disables a builder. This is a lightweight
// structural toggle and is NOT audited (no request-key idempotency). Use
// UpdateBuilder for audited mutations.
func (s *Service) SetBuilderEnabled(ctx context.Context, id uint, enabled bool) (*SubscriptionBuilder, error) {
	var b SubscriptionBuilder
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&b, id).Error; err != nil {
			if isNotFound(err) {
				return ErrBuilderNotFound
			}
			return fmt.Errorf("load builder: %w", err)
		}
		b.Enabled = enabled
		b.Version++
		if err := tx.Model(&b).Updates(map[string]any{
			"enabled": enabled,
			"version": b.Version,
		}).Error; err != nil {
			return fmt.Errorf("set builder enabled: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ---------------------------------------------------------------------------
// Preview (dry-run build)
// ---------------------------------------------------------------------------

// PreviewItem is one resolved entry in a builder preview.
type PreviewItem struct {
	// ItemID is the SubscriptionBuilderItem.ID that produced this entry.
	ItemID uint `json:"item_id"`
	// Kind mirrors SubscriptionBuilderItem.Kind.
	Kind string `json:"kind"`
	// SourceID is the originating provider source.
	SourceID uint `json:"source_id"`
	// Entry is the resolved catalogue entry (nil when Status != matched/fallback).
	Entry *ProviderSourceEntry `json:"entry,omitempty"`
	// DisplayName is CustomName if set, otherwise Entry.OriginalName.
	DisplayName string `json:"display_name"`
	// Status is the resolution outcome.
	Status FingerprintStatus `json:"status"`
	// Position is the final output position.
	Position int `json:"position"`
}

// PreviewBuilderResult is the full dry-run output of a builder.
type PreviewBuilderResult struct {
	BuilderID uint          `json:"builder_id"`
	Items     []PreviewItem `json:"items"`
	// Warnings lists items with fallback or conflict status.
	Warnings []string `json:"warnings,omitempty"`
	// Missing is the count of items that resolved to nothing.
	Missing int `json:"missing"`
	// Conflicts is the count of items with >1 name matches.
	Conflicts int `json:"conflicts"`
	// Total is the count of output entries (matched + fallback).
	Total int `json:"total"`
	// PreviewedAt is the timestamp of this dry-run.
	PreviewedAt time.Time `json:"previewed_at"`
}

// PreviewBuilder performs a dry-run build of a builder: resolves all items
// against the current catalogue without writing anything. Country items expand
// to all present entries of the source for that country.
func (s *Service) PreviewBuilder(ctx context.Context, builderID uint) (*PreviewBuilderResult, error) {
	b, err := s.GetBuilder(ctx, builderID)
	if err != nil {
		return nil, err
	}

	result := &PreviewBuilderResult{
		BuilderID:   builderID,
		PreviewedAt: time.Now().UTC(),
	}

	linked := make(map[uint]bool, len(b.Sources))
	for _, src := range b.Sources {
		linked[src.SourceID] = true
	}

	pos := 0
	for _, item := range b.Items {
		if !item.Enabled {
			continue
		}
		// Same as /sub: rules for a source not linked to the builder are ignored.
		if !linked[item.SourceID] {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("item %d: source %d is not linked to the builder, rule ignored", item.ID, item.SourceID))
			continue
		}
		switch item.Kind {
		case BuilderItemKindCountry:
			// Load every present entry and apply the shared country rule
			// (catalogue country, else flag emoji in the name) exactly like /sub.
			all, err := s.GetSourceEntries(ctx, item.SourceID, "*")
			if err != nil {
				return nil, fmt.Errorf("preview country item %d: %w", item.ID, err)
			}
			entries := make([]ProviderSourceEntry, 0, len(all))
			for i := range all {
				if CountryMatches(all[i].CountryCode, all[i].OriginalName, item.CountryCode) {
					entries = append(entries, all[i])
				}
			}
			for i := range entries {
				name := entries[i].OriginalName
				if item.CustomName != nil && *item.CustomName != "" {
					name = *item.CustomName
				}
				result.Items = append(result.Items, PreviewItem{
					ItemID:      item.ID,
					Kind:        item.Kind,
					SourceID:    item.SourceID,
					Entry:       &entries[i],
					DisplayName: name,
					Status:      FingerprintStatusMatched,
					Position:    pos,
				})
				pos++
				result.Total++
			}

		case BuilderItemKindNode:
			res, err := s.ResolveNodeItem(ctx, item)
			if err != nil {
				return nil, fmt.Errorf("preview node item %d: %w", item.ID, err)
			}
			pi := PreviewItem{
				ItemID:   item.ID,
				Kind:     item.Kind,
				SourceID: item.SourceID,
				Entry:    res.Entry,
				Status:   res.Status,
				Position: pos,
			}
			if res.Entry != nil {
				pi.DisplayName = res.Entry.OriginalName
				if item.CustomName != nil && *item.CustomName != "" {
					pi.DisplayName = *item.CustomName
				}
			}
			switch res.Status {
			case FingerprintStatusMatched:
				result.Total++
				pos++
			case FingerprintStatusFallback:
				result.Total++
				pos++
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("item %d: matched by name only (fingerprint mismatch)", item.ID))
			case FingerprintStatusMissing:
				result.Missing++
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("item %d: no matching entry found", item.ID))
			case FingerprintStatusConflict:
				result.Conflicts++
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("item %d: multiple entries match original_name %q", item.ID, item.OriginalName))
			}
			result.Items = append(result.Items, pi)
		}
	}

	return result, nil
}
