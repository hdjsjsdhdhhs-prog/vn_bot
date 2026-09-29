package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Catalogue sync bookkeeping (SyncSourceEntries / MarkSourceSyncFailed),
// per-source statistics and the Preview built on the stored catalogue.
// Fetching and parsing are covered by internal/subserver/catalogue_test.go.

func catalogueEntries(specs ...[3]string) []ProviderSourceEntry {
	out := make([]ProviderSourceEntry, 0, len(specs))
	for _, s := range specs {
		out = append(out, ProviderSourceEntry{Fingerprint: s[0], OriginalName: s[1], CountryCode: s[2], Protocol: "vless"})
	}
	return out
}

func presentFingerprints(t *testing.T, svc *Service, sourceID uint) []string {
	t.Helper()
	entries, err := svc.GetSourceEntries(context.Background(), sourceID, "*")
	require.NoError(t, err)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Fingerprint)
	}
	return out
}

func TestSyncSourceEntries_InitialRefreshAddedUpdatedRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	src := newProviderSourceTestSource(t, svc, "catalogue-sync")

	// Initial sync: every entry is new.
	counts, err := svc.SyncSourceEntries(ctx, src.ID, catalogueEntries(
		[3]string{"fp-de", "🇩🇪 Berlin", "DE"},
		[3]string{"fp-nl", "🇳🇱 Amsterdam", "NL"},
		[3]string{"fp-fi", "🇫🇮 Helsinki", "FI"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	assert.Equal(t, SourceSyncCounts{Added: 3, Updated: 0, Removed: 0, Total: 3}, counts)

	stored, err := svc.GetProviderSourceByID(ctx, src.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.LastSyncAt)
	assert.Equal(t, SourceSyncOK, stored.LastSyncStatus)

	before, err := svc.ListSourceEntriesAll(ctx, src.ID)
	require.NoError(t, err)
	var fiSeen time.Time
	for _, e := range before {
		if e.Fingerprint == "fp-fi" {
			fiSeen = e.LastSeenAt
		}
	}
	time.Sleep(5 * time.Millisecond)

	// Refresh: a new node appears (fp-us), one disappears (fp-fi), the order changes.
	counts, err = svc.SyncSourceEntries(ctx, src.ID, catalogueEntries(
		[3]string{"fp-nl", "🇳🇱 Amsterdam", "NL"},
		[3]string{"fp-us", "🇺🇸 New York", "US"},
		[3]string{"fp-de", "🇩🇪 Berlin 2", "DE"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	assert.Equal(t, SourceSyncCounts{Added: 1, Updated: 2, Removed: 1, Total: 3}, counts)
	assert.Equal(t, []string{"fp-nl", "fp-us", "fp-de"}, presentFingerprints(t, svc, src.ID), "upstream order")

	all, err := svc.ListSourceEntriesAll(ctx, src.ID)
	require.NoError(t, err)
	require.Len(t, all, 4, "the disappeared entry is kept")
	gone := all[3]
	assert.Equal(t, "fp-fi", gone.Fingerprint, "absent entries are listed after the present ones")
	assert.False(t, gone.Present)
	assert.True(t, gone.LastSeenAt.Equal(fiSeen), "last_seen_at of a disappeared entry is not bumped")
	assert.Equal(t, "🇩🇪 Berlin 2", all[2].OriginalName, "a renamed entry keeps its row and gets the new name")

	// The node reappears: it counts as added again.
	counts, err = svc.SyncSourceEntries(ctx, src.ID, catalogueEntries(
		[3]string{"fp-fi", "🇫🇮 Helsinki", "FI"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	assert.Equal(t, SourceSyncCounts{Added: 1, Updated: 0, Removed: 3, Total: 1}, counts)
}

func TestMarkSourceSyncFailed_KeepsCatalogue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	src := newProviderSourceTestSource(t, svc, "catalogue-failed")
	_, err := svc.SyncSourceEntries(ctx, src.ID, catalogueEntries([3]string{"fp-1", "🇩🇪 A", "DE"}, [3]string{"fp-2", "🇩🇪 B", "DE"}), SourceSyncOK, "")
	require.NoError(t, err)

	require.NoError(t, svc.MarkSourceSyncFailed(ctx, src.ID, "unreachable"))

	stored, err := svc.GetProviderSourceByID(ctx, src.ID)
	require.NoError(t, err)
	assert.Equal(t, SourceSyncError, stored.LastSyncStatus)
	assert.Equal(t, "unreachable", stored.LastSyncError)
	assert.Equal(t, []string{"fp-1", "fp-2"}, presentFingerprints(t, svc, src.ID), "a failed sync never removes the catalogue")

	assert.ErrorIs(t, svc.MarkSourceSyncFailed(ctx, 999999, "unreachable"), ErrProviderSourceNotFound)
}

func TestSourceCatalogueStats_PerSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	a := newProviderSourceTestSource(t, svc, "stats-a")
	b := newProviderSourceTestSource(t, svc, "stats-b")
	empty := newProviderSourceTestSource(t, svc, "stats-empty")

	_, err := svc.SyncSourceEntries(ctx, a.ID, []ProviderSourceEntry{
		{Fingerprint: "a1", OriginalName: "🇨🇭 1", CountryCode: "CH", Protocol: "vless"},
		{Fingerprint: "a2", OriginalName: "🇨🇭 2", CountryCode: "CH", Protocol: "vless"},
		{Fingerprint: "a3", OriginalName: "🇩🇪 1", CountryCode: "DE", Protocol: "hysteria2"},
		{Fingerprint: "a4", OriginalName: "Section", Protocol: "vless"},
	}, SourceSyncOK, "")
	require.NoError(t, err)
	_, err = svc.SyncSourceEntries(ctx, b.ID, []ProviderSourceEntry{
		{Fingerprint: "b1", OriginalName: "🇩🇪 X", CountryCode: "DE", Protocol: "trojan"},
		{Fingerprint: "b2", OriginalName: "🇯🇵 Y", CountryCode: "JP", Protocol: "trojan"},
	}, SourceSyncOK, "")
	require.NoError(t, err)
	// One entry of b disappears: it is counted as absent, not as a server.
	_, err = svc.SyncSourceEntries(ctx, b.ID, []ProviderSourceEntry{
		{Fingerprint: "b1", OriginalName: "🇩🇪 X", CountryCode: "DE", Protocol: "trojan"},
	}, SourceSyncOK, "")
	require.NoError(t, err)

	all, err := svc.SourceCatalogueStatsAll(ctx)
	require.NoError(t, err)

	sa := all[a.ID]
	assert.Equal(t, 4, sa.Entries)
	assert.Equal(t, 2, sa.Countries)
	assert.Equal(t, 1, sa.NoCountry)
	assert.Equal(t, []SourceCountryCount{{Code: "CH", Count: 2}, {Code: "DE", Count: 1}}, sa.ByCountry)
	assert.Equal(t, map[string]int{"vless": 3, "hysteria2": 1}, sa.Protocols)

	sb := all[b.ID]
	assert.Equal(t, 1, sb.Entries, "the catalogue of b is counted apart from a")
	assert.Equal(t, 1, sb.Countries)
	assert.Equal(t, 1, sb.Absent)
	assert.Equal(t, []SourceCountryCount{{Code: "DE", Count: 1}}, sb.ByCountry)

	_, ok := all[empty.ID]
	assert.False(t, ok)
	one, err := svc.SourceCatalogueStatsFor(ctx, empty.ID)
	require.NoError(t, err)
	assert.Zero(t, one.Entries)
	assert.NotNil(t, one.ByCountry, "an empty breakdown is [] on the wire")
}

func TestPreviewBuilder_MatchesRuntimeOnCatalogue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	a := newProviderSourceTestSource(t, svc, "preview-rt-a")
	b := newProviderSourceTestSource(t, svc, "preview-rt-b")
	_, err := svc.SyncSourceEntries(ctx, a.ID, catalogueEntries(
		[3]string{"a-de", "🇩🇪 A Berlin", "DE"}, [3]string{"a-nl", "🇳🇱 A Amsterdam", "NL"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	_, err = svc.SyncSourceEntries(ctx, b.ID, catalogueEntries(
		[3]string{"b-de", "🇩🇪 B Frankfurt", "DE"}, [3]string{"b-jp", "🇯🇵 B Tokyo", "JP"},
	), SourceSyncOK, "")
	require.NoError(t, err)

	bld := newRuntimeTestBuilder(t, svc, "preview-rt", true)
	require.NoError(t, svc.SetBuilderSources(ctx, bld.ID, []uint{a.ID, b.ID}))

	// No rules: every entry of every linked source, in source order (like /sub).
	res, err := svc.PreviewBuilder(ctx, bld.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 A Berlin", "🇳🇱 A Amsterdam", "🇩🇪 B Frankfurt", "🇯🇵 B Tokyo"}, previewNames(res))
	assert.Equal(t, 4, res.Total)
	assert.Equal(t, PreviewKindSource, res.Items[0].Kind)

	// Rules: Source A + DE (dynamic), Source B + node, then an overlapping
	// node rule for an entry already served (deduplicated like /sub).
	for _, it := range []SubscriptionBuilderItem{
		{BuilderID: bld.ID, Kind: BuilderItemKindCountry, SourceID: a.ID, CountryCode: "DE", Position: 0, Enabled: true},
		{BuilderID: bld.ID, Kind: BuilderItemKindNode, SourceID: b.ID, Fingerprint: "b-jp", OriginalName: "🇯🇵 B Tokyo", Position: 1, Enabled: true},
		{BuilderID: bld.ID, Kind: BuilderItemKindNode, SourceID: a.ID, Fingerprint: "a-de", OriginalName: "🇩🇪 A Berlin", Position: 2, Enabled: true},
	} {
		_, err := svc.UpsertBuilderItem(ctx, it)
		require.NoError(t, err)
	}
	res, err = svc.PreviewBuilder(ctx, bld.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 A Berlin", "🇯🇵 B Tokyo"}, previewNames(res))
	assert.Equal(t, 2, res.Total)

	// Dynamic country rule: a refresh that adds a new DE node of source A
	// changes the preview without touching the builder.
	_, err = svc.SyncSourceEntries(ctx, a.ID, catalogueEntries(
		[3]string{"a-de", "🇩🇪 A Berlin", "DE"}, [3]string{"a-nl", "🇳🇱 A Amsterdam", "NL"}, [3]string{"a-de2", "🇩🇪 A Munich", "DE"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	res, err = svc.PreviewBuilder(ctx, bld.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 A Berlin", "🇩🇪 A Munich", "🇯🇵 B Tokyo"}, previewNames(res))

	// A node that disappeared upstream is reported missing, not served.
	_, err = svc.SyncSourceEntries(ctx, b.ID, catalogueEntries([3]string{"b-de", "🇩🇪 B Frankfurt", "DE"}), SourceSyncOK, "")
	require.NoError(t, err)
	res, err = svc.PreviewBuilder(ctx, bld.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 A Berlin", "🇩🇪 A Munich"}, previewNames(res))
	assert.Equal(t, 1, res.Missing)

	// A disabled source is skipped like at runtime.
	_, err = svc.SetProviderSourceEnabled(ctx, a.ID, false)
	require.NoError(t, err)
	res, err = svc.PreviewBuilder(ctx, bld.ID)
	require.NoError(t, err)
	assert.Empty(t, previewNames(res))
	assert.NotEmpty(t, res.Warnings)
}

func TestSourceFormats(t *testing.T) {
	t.Parallel()
	for _, f := range SourceFormats {
		assert.True(t, ValidSourceFormat(f), f)
		assert.Equal(t, f, NormalizeSourceFormat(f))
	}
	for _, legacy := range []string{"", "external", "relayhub", "xui", "JSON"} {
		assert.False(t, ValidSourceFormat(legacy), legacy)
		assert.Equal(t, SourceFormatAuto, NormalizeSourceFormat(legacy), "legacy free text has no runtime meaning")
	}
}
