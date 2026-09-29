package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Source format (provider_sources.type)
// ---------------------------------------------------------------------------

// ProviderSource.Type is the expected format of the upstream subscription
// response. The runtime (/sub) always detects the format of every response
// itself; the configured type only lets a catalogue sync reject a response in
// an unexpected format. "auto" accepts any supported format.
const (
	SourceFormatAuto   = "auto"
	SourceFormatJSON   = "json"   // JSON array/object: 3x-ui configs or full Xray configs
	SourceFormatBase64 = "base64" // base64-encoded share links
	SourceFormatPlain  = "plain"  // plain share links, one per line
	SourceFormatClash  = "clash"  // Clash/Mihomo YAML with a proxies section
)

// SourceFormats lists the accepted ProviderSource.Type values in UI order.
var SourceFormats = []string{SourceFormatAuto, SourceFormatJSON, SourceFormatBase64, SourceFormatPlain, SourceFormatClash}

// ValidSourceFormat reports whether t is an accepted ProviderSource.Type.
func ValidSourceFormat(t string) bool {
	for _, f := range SourceFormats {
		if t == f {
			return true
		}
	}
	return false
}

// NormalizeSourceFormat maps legacy free-text types (e.g. "relayhub",
// "external", written before the type became a format) to "auto": they never
// had a runtime meaning.
func NormalizeSourceFormat(t string) string {
	if ValidSourceFormat(t) {
		return t
	}
	return SourceFormatAuto
}

// Sync status codes stored in provider_sources.last_sync_status.
const (
	SourceSyncOK      = "ok"      // every server of the response is in the catalogue
	SourceSyncPartial = "partial" // some servers could not be parsed (last_sync_error says how many)
	SourceSyncError   = "error"   // the sync failed; the previous catalogue is kept
)

// ErrProviderSourceSyncInProgress is returned when a sync of the same source
// is already running.
var ErrProviderSourceSyncInProgress = errors.New("provider source sync already in progress")

// ---------------------------------------------------------------------------
// Catalogue sync
// ---------------------------------------------------------------------------

// SourceSyncCounts describes how a sync changed the catalogue of one source.
type SourceSyncCounts struct {
	Added   int `json:"added"`   // entries that were not present before (new or reappeared)
	Updated int `json:"updated"` // entries that were present before and are still present
	Removed int `json:"removed"` // entries that were present before and disappeared
	Total   int `json:"total"`   // present entries after the sync
}

// SyncSourceEntries replaces the present catalogue of a source in one
// transaction and returns the change counts. Entries missing from the new
// list are kept with present=false (their last_seen_at stays), entries in the
// list are upserted by fingerprint. The caller must pass unique fingerprints.
func (s *Service) SyncSourceEntries(ctx context.Context, sourceID uint, entries []ProviderSourceEntry, syncStatus, syncError string) (SourceSyncCounts, error) {
	now := time.Now().UTC()
	var counts SourceSyncCounts
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before []string
		if err := tx.Model(&ProviderSourceEntry{}).
			Where("source_id = ? AND present = ?", sourceID, true).
			Pluck("fingerprint", &before).Error; err != nil {
			return fmt.Errorf("load present entries: %w", err)
		}
		wasPresent := make(map[string]bool, len(before))
		for _, fp := range before {
			wasPresent[fp] = true
		}

		if err := tx.Model(&ProviderSourceEntry{}).
			Where("source_id = ?", sourceID).
			Updates(map[string]any{"present": false}).Error; err != nil {
			return fmt.Errorf("mark entries absent: %w", err)
		}

		seen := make(map[string]bool, len(entries))
		for i := range entries {
			entries[i].SourceID = sourceID
			entries[i].Present = true
			entries[i].LastSeenAt = now
			entries[i].UpstreamPosition = i

			var existing ProviderSourceEntry
			err := tx.Where("source_id = ? AND fingerprint = ?", sourceID, entries[i].Fingerprint).First(&existing).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				entries[i].ID = 0
				if err := tx.Create(&entries[i]).Error; err != nil {
					return fmt.Errorf("insert source entry %q: %w", entries[i].Fingerprint, err)
				}
			case err != nil:
				return fmt.Errorf("lookup source entry %q: %w", entries[i].Fingerprint, err)
			default:
				entries[i].ID = existing.ID
				if err := tx.Save(&entries[i]).Error; err != nil {
					return fmt.Errorf("update source entry %q: %w", entries[i].Fingerprint, err)
				}
			}

			if seen[entries[i].Fingerprint] {
				continue
			}
			seen[entries[i].Fingerprint] = true
			if wasPresent[entries[i].Fingerprint] {
				counts.Updated++
			} else {
				counts.Added++
			}
		}
		for fp := range wasPresent {
			if !seen[fp] {
				counts.Removed++
			}
		}
		counts.Total = len(seen)

		if err := tx.Model(&ProviderSource{}).Where("id = ?", sourceID).
			Updates(map[string]any{
				"last_sync_at":     now,
				"last_sync_status": syncStatus,
				"last_sync_error":  truncateSyncError(syncError),
			}).Error; err != nil {
			return fmt.Errorf("update source sync metadata: %w", err)
		}
		return nil
	})
	if err != nil {
		return SourceSyncCounts{}, err
	}
	return counts, nil
}

// MarkSourceSyncFailed records a failed sync attempt without touching the
// catalogue: the entries of the last successful sync stay present.
func (s *Service) MarkSourceSyncFailed(ctx context.Context, sourceID uint, syncError string) error {
	res := s.db.WithContext(ctx).Model(&ProviderSource{}).Where("id = ?", sourceID).
		Updates(map[string]any{
			"last_sync_at":     time.Now().UTC(),
			"last_sync_status": SourceSyncError,
			"last_sync_error":  truncateSyncError(syncError),
		})
	if res.Error != nil {
		return fmt.Errorf("record source sync failure: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrProviderSourceNotFound
	}
	return nil
}

// truncateSyncError keeps the stored code within last_sync_error (64 bytes).
func truncateSyncError(code string) string {
	if len(code) > 64 {
		return code[:64]
	}
	return code
}

// ListSourceEntriesAll returns every catalogue entry of a source, present ones
// first in upstream order, then the disappeared ones (most recently seen first).
func (s *Service) ListSourceEntriesAll(ctx context.Context, sourceID uint) ([]ProviderSourceEntry, error) {
	var entries []ProviderSourceEntry
	if err := s.db.WithContext(ctx).
		Where("source_id = ?", sourceID).
		Order("present DESC, CASE WHEN present THEN upstream_position ELSE 0 END ASC, last_seen_at DESC, id ASC").
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list source entries: %w", err)
	}
	return entries, nil
}

// ---------------------------------------------------------------------------
// Catalogue statistics
// ---------------------------------------------------------------------------

// SourceCountryCount is the number of present entries of one country.
type SourceCountryCount struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// SourceCatalogueStats summarizes the present catalogue of one source.
type SourceCatalogueStats struct {
	Entries   int                  `json:"entries"`    // present entries (servers)
	Countries int                  `json:"countries"`  // distinct country codes among them
	NoCountry int                  `json:"no_country"` // present entries without a country
	Absent    int                  `json:"absent"`     // entries that disappeared upstream
	ByCountry []SourceCountryCount `json:"by_country"` // descending count, then code
	Protocols map[string]int       `json:"protocols"`  // present entries per protocol
}

// SourceCatalogueStatsAll returns the catalogue statistics of every source
// that has entries, keyed by source ID. Sources without entries are absent.
func (s *Service) SourceCatalogueStatsAll(ctx context.Context) (map[uint]SourceCatalogueStats, error) {
	return s.sourceCatalogueStats(ctx, 0)
}

// SourceCatalogueStatsFor returns the catalogue statistics of one source.
func (s *Service) SourceCatalogueStatsFor(ctx context.Context, sourceID uint) (SourceCatalogueStats, error) {
	all, err := s.sourceCatalogueStats(ctx, sourceID)
	if err != nil {
		return SourceCatalogueStats{}, err
	}
	if st, ok := all[sourceID]; ok {
		return st, nil
	}
	return emptySourceStats(), nil
}

func emptySourceStats() SourceCatalogueStats {
	return SourceCatalogueStats{ByCountry: []SourceCountryCount{}, Protocols: map[string]int{}}
}

func (s *Service) sourceCatalogueStats(ctx context.Context, sourceID uint) (map[uint]SourceCatalogueStats, error) {
	type row struct {
		SourceID    uint
		Present     bool
		CountryCode string
		Protocol    string
		N           int
	}
	q := s.db.WithContext(ctx).Model(&ProviderSourceEntry{}).
		Select("source_id, present, country_code, protocol, COUNT(*) AS n").
		Group("source_id, present, country_code, protocol")
	if sourceID != 0 {
		q = q.Where("source_id = ?", sourceID)
	}
	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("source catalogue stats: %w", err)
	}

	byCountry := make(map[uint]map[string]int)
	out := make(map[uint]SourceCatalogueStats)
	for _, r := range rows {
		st, ok := out[r.SourceID]
		if !ok {
			st = emptySourceStats()
			byCountry[r.SourceID] = make(map[string]int)
		}
		if !r.Present {
			st.Absent += r.N
			out[r.SourceID] = st
			continue
		}
		st.Entries += r.N
		if r.Protocol != "" {
			st.Protocols[r.Protocol] += r.N
		}
		if r.CountryCode == "" {
			st.NoCountry += r.N
		} else {
			byCountry[r.SourceID][r.CountryCode] += r.N
		}
		out[r.SourceID] = st
	}
	for id, st := range out {
		for code, n := range byCountry[id] {
			st.ByCountry = append(st.ByCountry, SourceCountryCount{Code: code, Count: n})
		}
		sort.Slice(st.ByCountry, func(i, j int) bool {
			if st.ByCountry[i].Count != st.ByCountry[j].Count {
				return st.ByCountry[i].Count > st.ByCountry[j].Count
			}
			return st.ByCountry[i].Code < st.ByCountry[j].Code
		})
		st.Countries = len(st.ByCountry)
		out[id] = st
	}
	return out, nil
}
