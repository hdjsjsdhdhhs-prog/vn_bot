package subserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// SourceCatalogue is the server-side parse of one provider source response:
// the catalogue that a sync stores for the admin panel, the Builder rules and
// the Preview. It is produced by the same fetch and parser as the /sub
// runtime, so the catalogue shows exactly what a builder can serve.
type SourceCatalogue struct {
	// Format is the detected response format: json | base64 | plain | clash.
	Format string
	// Entries are the parsed servers in upstream order with unique
	// fingerprints (a repeated server keeps its first position).
	Entries []database.ProviderSourceEntry
	// Skipped counts servers of the response that could not be parsed.
	Skipped int
	// Duplicates counts servers repeated in the response (merged into one entry).
	Duplicates int
}

// SourceSyncError is a failed catalogue sync. Code is stable and
// credential-free (never the URL, headers or response content):
//
//	invalid_config, timeout, unreachable, http_<status>, read_error,
//	too_large, credential_echo, empty_response, unknown_format,
//	format_mismatch:<detected>, no_servers
type SourceSyncError struct {
	Code string
}

func (e *SourceSyncError) Error() string { return "provider source sync failed: " + e.Code }

// SyncCode exposes the stable failure code to callers that do not import
// subserver (service.BuilderService matches it with errors.As).
func (e *SourceSyncError) SyncCode() string { return e.Code }

// FetchSourceCatalogue fetches and parses a provider source for the catalogue.
// A disabled source can still be synced (to inspect it before enabling); every
// other configuration check of the runtime fetch applies. The configured
// source type is the expected format ("auto" accepts every format).
func FetchSourceCatalogue(ctx context.Context, source database.ProviderSource) (*SourceCatalogue, error) {
	source.Enabled = true

	resp, failure := fetchProviderSource(ctx, source)
	if failure != "" {
		return nil, &SourceSyncError{Code: failure}
	}

	return parseSourceCatalogue(source, resp)
}

// parseSourceCatalogue turns one fetched response into a catalogue.
func parseSourceCatalogue(source database.ProviderSource, resp *NodeResponse) (*SourceCatalogue, error) {
	format := DetectFormat(resp.Body)
	if format == FormatUnknown {
		if strings.TrimSpace(string(resp.Body)) == "" {
			return nil, &SourceSyncError{Code: "empty_response"}
		}
		return nil, &SourceSyncError{Code: "unknown_format"}
	}

	expected := database.NormalizeSourceFormat(source.Type)
	if expected != database.SourceFormatAuto && expected != format.String() {
		return nil, &SourceSyncError{Code: "format_mismatch:" + format.String()}
	}

	// The stored catalogue is not consulted: countries are derived from the
	// current response only, so a sync never inherits a stale country.
	parsed, stats := parseBuilderSourceCounted(fmt.Sprintf("catalogue-%d", source.ID), source, resp, nil)

	out := &SourceCatalogue{Format: format.String(), Skipped: stats.skipped + stats.nonServerLines}
	seen := make(map[string]bool, len(parsed.entries))
	for _, e := range parsed.entries {
		if e.link != "" && !isValidServer(e.link) {
			continue // comment or non-server line (counted in stats)
		}
		if seen[e.fingerprint] {
			out.Duplicates++
			continue
		}
		seen[e.fingerprint] = true
		out.Entries = append(out.Entries, database.ProviderSourceEntry{
			Fingerprint:  e.fingerprint,
			OriginalName: truncateRunes(e.name, 255),
			Protocol:     truncateRunes(e.protocol, 32),
			CountryCode:  e.country,
		})
	}

	if len(out.Entries) == 0 {
		return nil, &SourceSyncError{Code: "no_servers"}
	}

	return out, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
