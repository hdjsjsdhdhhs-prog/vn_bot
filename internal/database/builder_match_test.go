package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountryMatches_SharedRule(t *testing.T) {
	t.Parallel()

	assert.True(t, CountryMatches("DE", "plain", "de"), "catalogue country, case-insensitive")
	assert.True(t, CountryMatches("", "🇩🇪 Frankfurt", "DE"), "flag fallback when catalogue has no country")
	assert.False(t, CountryMatches("FI", "🇩🇪 Frankfurt", "DE"), "catalogue country wins over the flag")
	assert.False(t, CountryMatches("", "plain", ""), "entries without country never match")
	assert.Equal(t, "NL", CountryFromName("Fast 🇳🇱"))
}

func TestMatchOriginalName_URLEncoding(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		want  string
		names []string
		out   []int
	}{
		{"exact", "My Node", []string{"Other", "My Node"}, []int{1}},
		{"percent-encoded want", "My%20Node", []string{"My Node"}, []int{0}},
		{"percent-encoded entry", "My Node", []string{"My%20Node"}, []int{0}},
		{"plus as space in want", "Fast+Node", []string{"Fast Node"}, []int{0}},
		{"plus as space in entry", "Fast Node", []string{"Fast+Node"}, []int{0}},
		{"encoded unicode", "%F0%9F%87%A9%F0%9F%87%AA%20Berlin", []string{"🇩🇪 Berlin"}, []int{0}},
		{"literal plus wins over decoded", "Node+", []string{"Node ", "Node+"}, []int{1}},
		{"decoded conflict", "A+B", []string{"A B", "A%20B"}, []int{0, 1}},
		{"invalid escape kept literal", "100%", []string{"100%"}, []int{0}},
		{"empty never matches", "", []string{""}, nil},
		{"no match", "Z", []string{"A"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.out, MatchOriginalName(tc.want, tc.names))
		})
	}
}

func seedPreviewCatalogue(t *testing.T, svc *Service, src *ProviderSource, entries []ProviderSourceEntry) {
	t.Helper()
	require.NoError(t, svc.UpsertSourceEntries(context.Background(), src.ID, entries, "ok", ""))
}

func previewNames(res *PreviewBuilderResult) []string {
	out := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		if it.Entry != nil {
			out = append(out, it.Entry.OriginalName)
		}
	}
	return out
}

func TestPreviewBuilder_CountryUsesSharedRule(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newTestService(t)
	src := newProviderSourceTestSource(t, svc, "preview-country")
	seedPreviewCatalogue(t, svc, src, []ProviderSourceEntry{
		{Fingerprint: "fp-1", OriginalName: "🇩🇪 Frankfurt"},                    // no catalogue country, flag only
		{Fingerprint: "fp-2", OriginalName: "Berlin plain", CountryCode: "DE"}, // catalogue country
		{Fingerprint: "fp-3", OriginalName: "🇩🇪 Mislabelled", CountryCode: "FI"},
		{Fingerprint: "fp-4", OriginalName: "🇳🇱 Amsterdam"},
	})

	b := newRuntimeTestBuilder(t, svc, "preview-country", true)
	require.NoError(t, svc.SetBuilderSources(ctx, b.ID, []uint{src.ID}))
	_, err := svc.UpsertBuilderItem(ctx, SubscriptionBuilderItem{BuilderID: b.ID, Kind: BuilderItemKindCountry, SourceID: src.ID, CountryCode: "de", Enabled: true})
	require.NoError(t, err)

	res, err := svc.PreviewBuilder(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 Frankfurt", "Berlin plain"}, previewNames(res))
	assert.Equal(t, 2, res.Total)
}

func TestPreviewBuilder_IgnoresUnlinkedSourceRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newTestService(t)
	linked := newProviderSourceTestSource(t, svc, "preview-linked")
	unlinked := newProviderSourceTestSource(t, svc, "preview-unlinked")
	seedPreviewCatalogue(t, svc, linked, []ProviderSourceEntry{{Fingerprint: "fp-l", OriginalName: "🇩🇪 Linked"}})
	seedPreviewCatalogue(t, svc, unlinked, []ProviderSourceEntry{
		{Fingerprint: "fp-u1", OriginalName: "🇩🇪 Unlinked"},
		{Fingerprint: "fp-u2", OriginalName: "Unlinked node"},
	})

	b := newRuntimeTestBuilder(t, svc, "preview-unlinked", true)
	require.NoError(t, svc.SetBuilderSources(ctx, b.ID, []uint{linked.ID}))
	for _, it := range []SubscriptionBuilderItem{
		{BuilderID: b.ID, Kind: BuilderItemKindCountry, SourceID: linked.ID, CountryCode: "DE", Position: 0, Enabled: true},
		{BuilderID: b.ID, Kind: BuilderItemKindCountry, SourceID: unlinked.ID, CountryCode: "DE", Position: 1, Enabled: true},
		{BuilderID: b.ID, Kind: BuilderItemKindNode, SourceID: unlinked.ID, Fingerprint: "fp-u2", Position: 2, Enabled: true},
		{BuilderID: b.ID, Kind: BuilderItemKindNode, SourceID: unlinked.ID, OriginalName: "gone", Position: 3, Enabled: true},
	} {
		_, err := svc.UpsertBuilderItem(ctx, it)
		require.NoError(t, err)
	}

	res, err := svc.PreviewBuilder(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 Linked"}, previewNames(res))
	assert.Len(t, res.Items, 1)
	assert.Equal(t, 1, res.Total)
	assert.Zero(t, res.Missing, "unlinked rules are ignored, not reported as missing")
	assert.Zero(t, res.Conflicts)
}

func TestResolveNodeItem_OriginalNameURLEncoding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newTestService(t)
	src := newProviderSourceTestSource(t, svc, "preview-names")
	seedPreviewCatalogue(t, svc, src, []ProviderSourceEntry{
		{Fingerprint: "fp-1", OriginalName: "Fast Node"},
		{Fingerprint: "fp-2", OriginalName: "🇩🇪 Berlin"},
		{Fingerprint: "fp-3", OriginalName: "Plus+"},
		{Fingerprint: "fp-4", OriginalName: "Plus "},
		{Fingerprint: "fp-5", OriginalName: ""},
	})

	for _, tc := range []struct {
		name     string
		original string
		status   FingerprintStatus
		want     string
	}{
		{"plus as space", "Fast+Node", FingerprintStatusFallback, "Fast Node"},
		{"percent encoded", "Fast%20Node", FingerprintStatusFallback, "Fast Node"},
		{"percent encoded unicode", "%F0%9F%87%A9%F0%9F%87%AA%20Berlin", FingerprintStatusFallback, "🇩🇪 Berlin"},
		{"literal plus preferred", "Plus+", FingerprintStatusFallback, "Plus+"},
		{"empty name is missing", "", FingerprintStatusMissing, ""},
		{"unknown", "Nope", FingerprintStatusMissing, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.ResolveNodeItem(ctx, SubscriptionBuilderItem{Kind: BuilderItemKindNode, SourceID: src.ID, Fingerprint: "stale", OriginalName: tc.original})
			require.NoError(t, err)
			assert.Equal(t, tc.status, res.Status)
			if tc.want != "" {
				require.NotNil(t, res.Entry)
				assert.Equal(t, tc.want, res.Entry.OriginalName)
			} else {
				assert.Nil(t, res.Entry)
			}
		})
	}
}
