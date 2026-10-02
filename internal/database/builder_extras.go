package database

import (
	"context"
	"fmt"
	"sort"
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

	return s.previewBuilderModel(ctx, b)
}

// previewBuilderModel is the PreviewBuilder resolver over an in-memory builder
// (its Sources and Items). The trial editor previews an unsaved composition
// through it, so the draft, the saved builder and the trial issuance check all
// resolve with exactly the same rules.
func (s *Service) previewBuilderModel(ctx context.Context, b *SubscriptionBuilder) (*PreviewBuilderResult, error) {
	builderID := b.ID
	result := &PreviewBuilderResult{
		BuilderID:   builderID,
		PreviewedAt: time.Now().UTC(),
	}

	linked := make(map[uint]bool, len(b.Sources))
	sourceIDs := make([]uint, 0, len(b.Sources))
	for _, src := range b.Sources {
		linked[src.SourceID] = true
		sourceIDs = append(sourceIDs, src.SourceID)
	}

	// Same as /sub: disabled or unusable sources are skipped.
	usable := make(map[uint]bool, len(sourceIDs))
	if len(sourceIDs) > 0 {
		var sources []ProviderSource
		if err := s.db.WithContext(ctx).Where("id IN ?", sourceIDs).Find(&sources).Error; err != nil {
			return nil, fmt.Errorf("preview load sources: %w", err)
		}
		for i := range sources {
			if _, _, err := sources[i].RequestConfiguration(); err == nil {
				usable[sources[i].ID] = true
			}
		}
	}

	// Same as /sub: an entry (source + fingerprint) is served once, at its
	// first position.
	seen := make(map[string]bool)
	pos := 0
	addMatched := func(itemID uint, kind string, sourceID uint, entry *ProviderSourceEntry, name string) {
		key := fmt.Sprintf("%d|%s", sourceID, entry.Fingerprint)
		if seen[key] {
			return
		}
		seen[key] = true
		result.Items = append(result.Items, PreviewItem{
			ItemID: itemID, Kind: kind, SourceID: sourceID, Entry: entry,
			DisplayName: name, Status: FingerprintStatusMatched, Position: pos,
		})
		pos++
		result.Total++
	}

	// Without rules /sub merges every entry of every linked source in source order.
	if len(b.Items) == 0 {
		for _, link := range b.Sources {
			if !usable[link.SourceID] {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("source %d is disabled or unusable, skipped", link.SourceID))
				continue
			}
			entries, err := s.GetSourceEntries(ctx, link.SourceID, "*")
			if err != nil {
				return nil, fmt.Errorf("preview source %d: %w", link.SourceID, err)
			}
			for i := range entries {
				addMatched(0, PreviewKindSource, link.SourceID, &entries[i], entries[i].OriginalName)
			}
		}
		return result, nil
	}

	items := make([]SubscriptionBuilderItem, len(b.Items))
	copy(items, b.Items)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].ID < items[j].ID
	})

	for _, item := range items {
		if !item.Enabled {
			continue
		}
		// Same as /sub: rules for a source not linked to the builder are ignored.
		if !linked[item.SourceID] {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("item %d: source %d is not linked to the builder, rule ignored", item.ID, item.SourceID))
			continue
		}
		if !usable[item.SourceID] {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("item %d: source %d is disabled or unusable, rule skipped", item.ID, item.SourceID))
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
			for i := range all {
				if !CountryMatches(all[i].CountryCode, all[i].OriginalName, item.CountryCode) {
					continue
				}
				name := all[i].OriginalName
				if item.CustomName != nil && *item.CustomName != "" {
					name = *item.CustomName
				}
				addMatched(item.ID, item.Kind, item.SourceID, &all[i], name)
			}

		case BuilderItemKindNode:
			res, err := s.ResolveNodeItem(ctx, item)
			if err != nil {
				return nil, fmt.Errorf("preview node item %d: %w", item.ID, err)
			}
			if res.Entry != nil && (res.Status == FingerprintStatusMatched || res.Status == FingerprintStatusFallback) {
				key := fmt.Sprintf("%d|%s", item.SourceID, res.Entry.Fingerprint)
				if seen[key] {
					continue // already served by an earlier rule, like /sub
				}
				seen[key] = true
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

// PreviewKindSource marks preview entries merged from a source because the
// builder has no rules (every entry of every linked source is served).
const PreviewKindSource = "source"
