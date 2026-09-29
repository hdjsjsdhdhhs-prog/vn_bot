package subserver_test

// Smoke test for the real provider source sync.
// Activated only when SMOKE_SOURCE_URL is set in the environment.
// The URL is never printed, logged or included in test output.
// Run:
//   SMOKE_SOURCE_URL=<url> go test -v -run TestSmoke ./internal/subserver/

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
)

func TestSmokeSourceSync(t *testing.T) {
	url := os.Getenv("SMOKE_SOURCE_URL")
	if url == "" {
		t.Skip("SMOKE_SOURCE_URL not set; skipping smoke test")
	}

	src := database.ProviderSource{
		ID:              9999,
		Name:            "smoke-fixture",
		Type:            database.SourceFormatAuto,
		SubscriptionURL: url,
		HWID:            "",
		UserAgent:       "",
		Headers:         "{}",
		Enabled:         true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	t.Log("=== SMOKE: first sync ===")
	cat1, err := subserver.FetchSourceCatalogue(ctx, src)
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	reportCatalogue(t, "first sync", cat1)

	// Basic sanity checks.
	if len(cat1.Entries) == 0 {
		t.Fatal("first sync: no entries returned")
	}
	if cat1.Format == "" {
		t.Fatal("first sync: format not detected")
	}

	// Verify fingerprints are unique (catalogue deduplication).
	seen := make(map[string]bool, len(cat1.Entries))
	for _, e := range cat1.Entries {
		if seen[e.Fingerprint] {
			t.Errorf("duplicate fingerprint in catalogue: %s", e.Fingerprint)
		}
		seen[e.Fingerprint] = true
	}

	// Verify country codes are valid ISO 3166-1 alpha-2 (or empty).
	for _, e := range cat1.Entries {
		if e.CountryCode != "" && (len(e.CountryCode) != 2 || strings.ToUpper(e.CountryCode) != e.CountryCode) {
			t.Errorf("invalid country code %q for entry %q", e.CountryCode, e.OriginalName)
		}
	}

	t.Log("=== SMOKE: second sync (idempotency) ===")
	cat2, err := subserver.FetchSourceCatalogue(ctx, src)
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	reportCatalogue(t, "second sync", cat2)

	// The second sync must return the same number of entries and the same format.
	if len(cat2.Entries) != len(cat1.Entries) {
		t.Errorf("second sync: entry count changed: %d → %d", len(cat1.Entries), len(cat2.Entries))
	}
	if cat2.Format != cat1.Format {
		t.Errorf("second sync: format changed: %q → %q", cat1.Format, cat2.Format)
	}

	// Multiple-source isolation: a second source with the same URL must
	// produce an independent catalogue. The upstream rotates servers between
	// requests (dynamic pool), so fingerprints may differ — but the aggregate
	// statistics (total count, format, country count) must be stable.
	t.Log("=== SMOKE: multiple sources (same upstream, different IDs) ===")
	src2 := src
	src2.ID = 9998
	src2.Name = "smoke-fixture-2"
	cat3, err := subserver.FetchSourceCatalogue(ctx, src2)
	if err != nil {
		t.Fatalf("second source sync failed: %v", err)
	}
	reportCatalogue(t, "second source", cat3)

	// The total entry count must be the same (same pool size).
	if len(cat3.Entries) != len(cat1.Entries) {
		t.Errorf("second source: entry count differs from first: %d vs %d", len(cat3.Entries), len(cat1.Entries))
	}
	// Format must be the same.
	if cat3.Format != cat1.Format {
		t.Errorf("second source: format differs: %q vs %q", cat3.Format, cat1.Format)
	}
	// Country count must be the same (the pool covers the same countries).
	countries1 := make(map[string]bool)
	for _, e := range cat1.Entries {
		if e.CountryCode != "" {
			countries1[e.CountryCode] = true
		}
	}
	countries3 := make(map[string]bool)
	for _, e := range cat3.Entries {
		if e.CountryCode != "" {
			countries3[e.CountryCode] = true
		}
	}
	if len(countries3) != len(countries1) {
		t.Errorf("second source: country count differs: %d vs %d", len(countries3), len(countries1))
	}
	// Fingerprints are unique within each catalogue (deduplication works per-source).
	seen3 := make(map[string]bool, len(cat3.Entries))
	for _, e := range cat3.Entries {
		if seen3[e.Fingerprint] {
			t.Errorf("second source: duplicate fingerprint %s", e.Fingerprint)
		}
		seen3[e.Fingerprint] = true
	}
	t.Logf("multiple sources: source1=%d entries, source2=%d entries, format=%s, countries1=%d, countries2=%d ✓",
		len(cat1.Entries), len(cat3.Entries), cat3.Format, len(countries1), len(countries3))
}

// reportCatalogue prints aggregated statistics without any URL, credential or
// raw config content.
func reportCatalogue(t *testing.T, label string, cat *subserver.SourceCatalogue) {
	t.Helper()

	// Count by country.
	byCountry := make(map[string]int)
	noCountry := 0
	for _, e := range cat.Entries {
		if e.CountryCode == "" {
			noCountry++
		} else {
			byCountry[e.CountryCode]++
		}
	}

	// Count by protocol.
	byProto := make(map[string]int)
	for _, e := range cat.Entries {
		if e.Protocol != "" {
			byProto[e.Protocol]++
		}
	}

	// Sort countries by count desc, then code asc.
	type cc struct{ code string; count int }
	countries := make([]cc, 0, len(byCountry))
	for code, n := range byCountry {
		countries = append(countries, cc{code, n})
	}
	sort.Slice(countries, func(i, j int) bool {
		if countries[i].count != countries[j].count {
			return countries[i].count > countries[j].count
		}
		return countries[i].code < countries[j].code
	})

	// Sort protocols by count desc.
	type pc struct{ proto string; count int }
	protos := make([]pc, 0, len(byProto))
	for p, n := range byProto {
		protos = append(protos, pc{p, n})
	}
	sort.Slice(protos, func(i, j int) bool {
		if protos[i].count != protos[j].count {
			return protos[i].count > protos[j].count
		}
		return protos[i].proto < protos[j].proto
	})

	t.Logf("[%s] format=%s  total_entries=%d  skipped=%d  duplicates=%d",
		label, cat.Format, len(cat.Entries), cat.Skipped, cat.Duplicates)
	t.Logf("[%s] countries=%d  no_country=%d", label, len(byCountry), noCountry)

	// Country breakdown (top 20).
	limit := len(countries)
	if limit > 20 {
		limit = 20
	}
	parts := make([]string, 0, limit)
	for _, c := range countries[:limit] {
		parts = append(parts, fmt.Sprintf("%s:%d", c.code, c.count))
	}
	if len(countries) > 20 {
		parts = append(parts, fmt.Sprintf("…+%d more", len(countries)-20))
	}
	t.Logf("[%s] country_breakdown: %s", label, strings.Join(parts, "  "))

	// Protocol breakdown.
	pparts := make([]string, 0, len(protos))
	for _, p := range protos {
		pparts = append(pparts, fmt.Sprintf("%s:%d", p.proto, p.count))
	}
	t.Logf("[%s] protocol_breakdown: %s", label, strings.Join(pparts, "  "))

	// First 10 server names (no fingerprints, no links).
	nameLimit := len(cat.Entries)
	if nameLimit > 10 {
		nameLimit = 10
	}
	names := make([]string, nameLimit)
	for i, e := range cat.Entries[:nameLimit] {
		names[i] = fmt.Sprintf("%d:%q", i+1, e.OriginalName)
	}
	t.Logf("[%s] first_%d_names: %s", label, nameLimit, strings.Join(names, "  "))
}
