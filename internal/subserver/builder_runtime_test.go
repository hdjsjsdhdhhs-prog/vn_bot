package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type builderTestUpstream struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newBuilderTestUpstream(t *testing.T, status int, headers map[string]string, body string) *builderTestUpstream {
	t.Helper()

	u := &builderTestUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		u.hits.Add(1)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(u.srv.Close)

	return u
}

func testLink(user, host, name string) string {
	return fmt.Sprintf("vless://%s@%s:443?security=tls&sni=%s&type=tcp#%s", user, host, host, url.PathEscape(name))
}

func testSource(id uint, rawURL string) *database.ProviderSource {
	return &database.ProviderSource{
		ID:              id,
		Name:            fmt.Sprintf("source-%d", id),
		Type:            "external",
		SubscriptionURL: rawURL,
		Headers:         `{}`,
		Enabled:         true,
		UpdatedAt:       time.Unix(100, 0),
	}
}

func testBuilder(id uint, sources []*database.ProviderSource, items ...database.SubscriptionBuilderItem) *database.SubscriptionBuilder {
	b := &database.SubscriptionBuilder{ID: id, Name: fmt.Sprintf("builder-%d", id), Enabled: true, Version: 1, UpdatedAt: time.Unix(200, 0)}
	for i, s := range sources {
		b.Sources = append(b.Sources, database.SubscriptionBuilderSource{BuilderID: id, SourceID: s.ID, Position: i, Source: s})
	}
	for i := range items {
		if items[i].ID == 0 {
			items[i].ID = uint(i + 1)
		}
		items[i].BuilderID = id
	}
	b.Items = items

	return b
}

func countryItem(sourceID uint, country string, pos int) database.SubscriptionBuilderItem {
	return database.SubscriptionBuilderItem{Kind: database.BuilderItemKindCountry, SourceID: sourceID, CountryCode: country, Position: pos, Enabled: true}
}

func nodeItem(sourceID uint, fingerprint, originalName string, custom *string, pos int) database.SubscriptionBuilderItem {
	return database.SubscriptionBuilderItem{Kind: database.BuilderItemKindNode, SourceID: sourceID, Fingerprint: fingerprint, OriginalName: originalName, CustomName: custom, Position: pos, Enabled: true}
}

func strPtr(s string) *string { return &s }

// builderTestDB wires a mock repository that serves builderFn() for subID.
// Legacy plan-node loading fails the test: builder subscriptions never use it.
func builderTestDB(t *testing.T, sub *database.Subscription, builderFn func() *database.SubscriptionBuilder) *testutil.DatabaseService {
	t.Helper()

	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		copySub := *sub
		return &copySub, nil
	}
	db.ResolveSubscriptionBuilderFunc = func(context.Context, *database.Subscription) (*database.ResolvedBuilder, error) {
		b := builderFn()
		if b == nil {
			return nil, nil
		}
		return &database.ResolvedBuilder{Builder: *b, Selection: database.BuilderSelectionSubscription}, nil
	}
	db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
		t.Error("builder subscription must not load legacy plan nodes")
		return nil, errors.New("unexpected legacy path")
	}

	return db
}

func decodeBuilderLinks(t *testing.T, result *SubscriptionResult) []string {
	t.Helper()
	require.NotNil(t, result)

	decoded, err := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, err)

	return strings.Split(string(decoded), "\n")
}

func linkNames(links []string) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, EntryName(l))
	}
	return out
}

func headerValue(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// selection priority / legacy
// ---------------------------------------------------------------------------

func TestBuilder_OverridesProviderSourceAndLegacyPipeline(t *testing.T) {
	t.Parallel()

	provider := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("p", "provider.example", "Provider"))
	builderUp := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("b", "builder.example", "Builder"))

	linked := testSource(1, provider.srv.URL)
	sub := activeRuntimeSubscription("builder-override", linked)
	src := testSource(2, builderUp.srv.URL)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder { return testBuilder(7, []*database.ProviderSource{src}) })

	result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, total)
	assert.Equal(t, []string{"Builder"}, linkNames(decodeBuilderLinks(t, result)))
	assert.Zero(t, provider.hits.Load(), "linked ProviderSource must not be fetched when a builder is selected")
}

func TestBuilder_NoBuilderKeepsLegacyAndProviderPipelines(t *testing.T) {
	t.Parallel()

	t.Run("legacy plan nodes", func(t *testing.T) {
		t.Parallel()

		legacy := newBuilderTestUpstream(t, http.StatusOK, nil, "vless://legacy@legacy.example:443#Legacy")
		db := testutil.NewDatabaseService()
		db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
			return activeRuntimeSubscription("builder-legacy", nil), nil
		}
		var resolverCalls atomic.Int32
		db.ResolveSubscriptionBuilderFunc = func(context.Context, *database.Subscription) (*database.ResolvedBuilder, error) {
			resolverCalls.Add(1)
			return nil, nil
		}
		db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
			return &database.SubscriptionFull{
				Subscription: *activeRuntimeSubscription("builder-legacy", nil),
				Nodes:        []database.Node{{ID: 1, Name: "legacy", SubscriptionURL: legacy.srv.URL + "/"}},
			}, nil
		}

		result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), "builder-legacy", "", nil)
		require.NoError(t, err)
		assert.Equal(t, int32(1), resolverCalls.Load())
		assert.Equal(t, []string{"vless://legacy@legacy.example:443#Legacy"}, decodeBuilderLinks(t, result))
	})

	t.Run("provider source", func(t *testing.T) {
		t.Parallel()

		provider := newBuilderTestUpstream(t, http.StatusOK, nil, "vless://p@provider.example:443#Provider")
		sub := activeRuntimeSubscription("builder-provider", testSource(3, provider.srv.URL))
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder { return nil })

		result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"vless://p@provider.example:443#Provider"}, decodeBuilderLinks(t, result))
	})
}

func TestBuilder_ResolverErrorIsNotServedFromLegacy(t *testing.T) {
	t.Parallel()

	sub := activeRuntimeSubscription("builder-resolver-error", nil)
	db := builderTestDB(t, sub, nil)
	db.ResolveSubscriptionBuilderFunc = func(context.Context, *database.Subscription) (*database.ResolvedBuilder, error) {
		return nil, errors.New("db down")
	}

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSubscriptionNotFound)
}

// ---------------------------------------------------------------------------
// rules
// ---------------------------------------------------------------------------

func TestBuilder_CountryRules(t *testing.T) {
	t.Parallel()

	plain := testLink("u", "fi.example", "Helsinki plain")
	body := strings.Join([]string{
		testLink("u", "de1.example", "🇩🇪 Frankfurt"),
		testLink("u", "nl.example", "🇳🇱 Amsterdam"),
		plain,
		testLink("u", "de2.example", "🇩🇪 Berlin"),
	}, "\n")
	up := newBuilderTestUpstream(t, http.StatusOK, nil, body)
	src := testSource(10, up.srv.URL)

	sub := activeRuntimeSubscription("builder-country", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{src},
			countryItem(src.ID, "de", 0), // case-insensitive
			countryItem(src.ID, "FI", 1), // resolved via the stored catalogue
		)
	})
	db.GetSourceEntriesFunc = func(_ context.Context, sourceID uint, country string) ([]database.ProviderSourceEntry, error) {
		assert.Equal(t, src.ID, sourceID)
		assert.Equal(t, "*", country)
		return []database.ProviderSourceEntry{{SourceID: src.ID, Fingerprint: EntryFingerprint(plain), CountryCode: "FI", Present: true}}, nil
	}

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 Frankfurt", "🇩🇪 Berlin", "Helsinki plain"}, linkNames(decodeBuilderLinks(t, result)))
}

func TestBuilder_NodeRulesRenameAndOrder(t *testing.T) {
	t.Parallel()

	frankfurt := testLink("u", "de1.example", "🇩🇪 Frankfurt")
	amsterdam := testLink("u", "nl.example", "🇳🇱 Amsterdam")
	berlin := testLink("u", "de2.example", "🇩🇪 Berlin")
	up := newBuilderTestUpstream(t, http.StatusOK, nil, strings.Join([]string{frankfurt, amsterdam, berlin}, "\n"))
	src := testSource(11, up.srv.URL)

	disabled := nodeItem(src.ID, EntryFingerprint(berlin), "", strPtr("must not appear"), 0)
	disabled.Enabled = false

	sub := activeRuntimeSubscription("builder-node-order", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{src},
			// Deliberately out of slice order: Position drives the output.
			countryItem(src.ID, "DE", 20),
			nodeItem(src.ID, EntryFingerprint(amsterdam), "", strPtr("NL · Premium"), 10),
			disabled,
			// Duplicate of an entry already picked by the country rule.
			nodeItem(src.ID, EntryFingerprint(frankfurt), "", strPtr("dup"), 30),
		)
	})

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)

	links := decodeBuilderLinks(t, result)
	assert.Equal(t, []string{"NL · Premium", "🇩🇪 Frankfurt", "🇩🇪 Berlin"}, linkNames(links))
	// Renaming only touches the display name.
	assert.True(t, strings.HasPrefix(links[0], "vless://u@nl.example:443?security=tls&sni=nl.example&type=tcp#"))
	assert.Equal(t, EntryFingerprint(amsterdam), EntryFingerprint(links[0]))
}

func TestBuilder_FingerprintStatuses(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		testLink("u", "renamed-host.example", "Stable Name"), // fingerprint changed upstream
		testLink("u", "dup1.example", "Duplicate"),
		testLink("u", "dup2.example", "Duplicate"),
		testLink("u", "exact.example", "Exact"),
	}, "\n")
	up := newBuilderTestUpstream(t, http.StatusOK, nil, body)
	src := testSource(12, up.srv.URL)

	sub := activeRuntimeSubscription("builder-fp-status", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{src},
			nodeItem(src.ID, EntryFingerprint(testLink("u", "exact.example", "Old name")), "Old name", strPtr("matched"), 0),
			nodeItem(src.ID, "stale-fingerprint", "Stable Name", strPtr("fallback"), 1),
			nodeItem(src.ID, "stale-fingerprint-2", "Duplicate", strPtr("conflict"), 2),
			nodeItem(src.ID, "gone", "Gone", strPtr("missing"), 3),
			nodeItem(src.ID, "", "", strPtr("empty"), 4),
		)
	})

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"matched", "fallback"}, linkNames(decodeBuilderLinks(t, result)))
}

func TestBuilder_OriginalNameFallbackURLEncoding(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"vless://u@plus.example:443?security=tls&sni=plus.example&type=tcp#Fast+Node",
		testLink("u", "pct.example", "My Node"),
	}, "\n")
	up := newBuilderTestUpstream(t, http.StatusOK, nil, body)
	src := testSource(14, up.srv.URL)

	sub := activeRuntimeSubscription("builder-name-encoding", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{src},
			nodeItem(src.ID, "stale-1", "Fast Node", strPtr("plus"), 0),
			nodeItem(src.ID, "stale-2", "My%20Node", strPtr("percent"), 1),
		)
	})

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"plus", "percent"}, linkNames(decodeBuilderLinks(t, result)))
}

func TestResolveNodeEntry_Statuses(t *testing.T) {
	t.Parallel()

	entries := []builderEntry{
		{fingerprint: "fp-a", name: "A"},
		{fingerprint: "fp-b", name: "B"},
		{fingerprint: "fp-c", name: "B"},
		{fingerprint: "fp-d", name: "Fast+Node"}, // upstream fragment encoded space as "+"
		{fingerprint: "fp-e", name: "My Node"},
	}

	for _, tc := range []struct {
		name   string
		item   database.SubscriptionBuilderItem
		idx    int
		status database.FingerprintStatus
	}{
		{"exact fingerprint", nodeItem(1, "fp-c", "A", nil, 0), 2, database.FingerprintStatusMatched},
		{"fallback by unique name", nodeItem(1, "fp-x", "A", nil, 0), 0, database.FingerprintStatusFallback},
		{"name only fallback", nodeItem(1, "", "A", nil, 0), 0, database.FingerprintStatusFallback},
		{"conflict", nodeItem(1, "fp-x", "B", nil, 0), -1, database.FingerprintStatusConflict},
		{"missing", nodeItem(1, "fp-x", "Z", nil, 0), -1, database.FingerprintStatusMissing},
		{"missing without keys", nodeItem(1, "", "", nil, 0), -1, database.FingerprintStatusMissing},
		// original_name fallback is tolerant to URL encoding (same rule as Preview).
		{"fallback plus as space", nodeItem(1, "fp-x", "Fast Node", nil, 0), 3, database.FingerprintStatusFallback},
		{"fallback percent encoded", nodeItem(1, "fp-x", "My%20Node", nil, 0), 4, database.FingerprintStatusFallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx, status := resolveNodeEntry(tc.item, entries)
			assert.Equal(t, tc.idx, idx)
			assert.Equal(t, tc.status, status)
		})
	}
}

func TestBuilder_AllItemsMissingReturnsNoItems(t *testing.T) {
	t.Parallel()

	up := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("u", "a.example", "A"))
	src := testSource(13, up.srv.URL)
	sub := activeRuntimeSubscription("builder-all-missing", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{src}, nodeItem(src.ID, "nope", "Nope", nil, 0))
	})

	result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrNoSubscriptionItems)
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, total)
}

// ---------------------------------------------------------------------------
// multiple sources / disabled / failures
// ---------------------------------------------------------------------------

func TestBuilder_MultipleSourcesMerged(t *testing.T) {
	t.Parallel()

	upA := newBuilderTestUpstream(t, http.StatusOK, map[string]string{
		"Subscription-Userinfo":   "upload=10; download=20; total=1000; expire=1893456000",
		"Profile-Update-Interval": "6",
	}, testLink("a", "a1.example", "A1")+"\n"+testLink("a", "a2.example", "A2"))
	upB := newBuilderTestUpstream(t, http.StatusOK, map[string]string{
		"Subscription-Userinfo":   "upload=1; download=2; total=500; expire=1800000000",
		"Profile-Update-Interval": "99",
	}, testLink("b", "b1.example", "B1"))

	srcA := testSource(21, upA.srv.URL)
	srcB := testSource(22, upB.srv.URL)

	t.Run("no rules merges every source in position order", func(t *testing.T) {
		t.Parallel()

		sub := activeRuntimeSubscription("builder-merge", nil)
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
			return testBuilder(1, []*database.ProviderSource{srcB, srcA})
		})

		result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		assert.Equal(t, 2, success)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{"B1", "A1", "A2"}, linkNames(decodeBuilderLinks(t, result)))
		assert.Equal(t, "upload=11; download=22; total=1500; expire=1800000000", result.Headers["subscription-userinfo"])
		assert.Equal(t, "99", headerValue(result.Headers, "profile-update-interval"), "headers come from the first source")
	})

	t.Run("rules interleave sources", func(t *testing.T) {
		t.Parallel()

		sub := activeRuntimeSubscription("builder-interleave", nil)
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
			return testBuilder(1, []*database.ProviderSource{srcA, srcB},
				nodeItem(srcA.ID, "", "A2", nil, 0),
				nodeItem(srcB.ID, "", "B1", nil, 1),
				nodeItem(srcA.ID, "", "A1", nil, 2),
			)
		})

		result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"A2", "B1", "A1"}, linkNames(decodeBuilderLinks(t, result)))
	})
}

func TestBuilder_DisabledAndUnlinkedSources(t *testing.T) {
	t.Parallel()

	upOn := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("on", "on.example", "On"))
	upOff := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("off", "off.example", "Off"))

	on := testSource(31, upOn.srv.URL)
	off := testSource(32, upOff.srv.URL)
	off.Enabled = false

	t.Run("disabled source is skipped", func(t *testing.T) {
		sub := activeRuntimeSubscription("builder-disabled-src", nil)
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
			return testBuilder(1, []*database.ProviderSource{off, on},
				nodeItem(off.ID, "", "Off", nil, 0),
				nodeItem(on.ID, "", "On", nil, 1),
				nodeItem(99, "", "Unlinked", nil, 2), // source not linked to the builder
			)
		})

		result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		assert.Equal(t, 1, success)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{"On"}, linkNames(decodeBuilderLinks(t, result)))
		assert.Zero(t, upOff.hits.Load())
	})

	t.Run("all sources disabled is unavailable without fallback", func(t *testing.T) {
		sub := activeRuntimeSubscription("builder-all-disabled", nil)
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
			return testBuilder(1, []*database.ProviderSource{off})
		})

		result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrProviderSourceUnavailable)
		assert.Zero(t, upOff.hits.Load())
	})
}

// A configuration that selects nothing is "no items" (404), not "source
// unavailable" (503): nothing is fetched and the sources are healthy.
func TestBuilder_ConfigurationSelectingNothingIsNoItems(t *testing.T) {
	t.Parallel()

	up := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("u", "a.example", "A"))
	src := testSource(35, up.srv.URL)

	disabledItem := nodeItem(src.ID, "", "A", nil, 0)
	disabledItem.Enabled = false

	for _, tc := range []struct {
		name    string
		builder *database.SubscriptionBuilder
	}{
		{"all items disabled", testBuilder(1, []*database.ProviderSource{src}, disabledItem)},
		{"items only reference unlinked sources", testBuilder(1, []*database.ProviderSource{src}, nodeItem(99, "", "A", nil, 0))},
		{"no linked sources", testBuilder(1, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := activeRuntimeSubscription("builder-selects-nothing", nil)
			db := builderTestDB(t, sub, func() *database.SubscriptionBuilder { return tc.builder })

			result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrNoSubscriptionItems)
			assert.NotErrorIs(t, err, ErrProviderSourceUnavailable)
			assert.Zero(t, success)
			assert.Zero(t, total)
		})
	}

	assert.Zero(t, up.hits.Load(), "nothing selected → nothing fetched")
}

func TestBuilder_SourceFailures(t *testing.T) {
	t.Parallel()

	ok := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("ok", "ok.example", "OK"))
	bad := newBuilderTestUpstream(t, http.StatusBadGateway, nil, "provider-secret-body")
	srcOK := testSource(41, ok.srv.URL)
	srcBad := testSource(42, bad.srv.URL+"/secret-path")

	t.Run("partial failure serves remaining sources", func(t *testing.T) {
		t.Parallel()

		sub := activeRuntimeSubscription("builder-partial", nil)
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
			return testBuilder(1, []*database.ProviderSource{srcBad, srcOK})
		})

		result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		assert.Equal(t, 1, success)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{"OK"}, linkNames(decodeBuilderLinks(t, result)))
	})

	t.Run("total failure is credential safe", func(t *testing.T) {
		t.Parallel()

		sub := activeRuntimeSubscription("builder-total-failure", nil)
		db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
			return testBuilder(1, []*database.ProviderSource{srcBad})
		})

		result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrProviderSourceUnavailable)
		assert.Equal(t, 0, success)
		assert.Equal(t, 1, total)
		assert.NotContains(t, err.Error(), srcBad.SubscriptionURL)
	})
}

// ---------------------------------------------------------------------------
// response format / headers
// ---------------------------------------------------------------------------

func TestBuilder_ProfileMetadataHeaders(t *testing.T) {
	t.Parallel()

	up := newBuilderTestUpstream(t, http.StatusOK, map[string]string{
		"Profile-Title": "base64:" + base64.StdEncoding.EncodeToString([]byte("Upstream")),
		"Support-Url":   "https://upstream.example/support",
	}, testLink("u", "a.example", "A"))
	src := testSource(51, up.srv.URL)

	b := testBuilder(1, []*database.ProviderSource{src})
	b.ProfileTitle = "RS8 KVN"
	b.SupportURL = "https://t.me/rs8kvn_support"
	b.Announce = "Новые серверы"

	sub := activeRuntimeSubscription("builder-headers", nil)
	sub.PricePaidCents = 100 // paid → " Premium" suffix
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder { return b })

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)

	assert.Equal(t, "base64:"+base64.StdEncoding.EncodeToString([]byte("RS8 KVN Premium")), headerValue(result.Headers, "profile-title"))
	assert.Equal(t, "https://t.me/rs8kvn_support", headerValue(result.Headers, "support-url"))
	assert.Equal(t, "base64:"+base64.StdEncoding.EncodeToString([]byte("Новые серверы")), headerValue(result.Headers, "announce"))
	assert.Equal(t, "text/plain; charset=utf-8; profile=base64", result.Headers["content-type"])
}

func TestBuilder_EmptyMetadataKeepsUpstreamHeaders(t *testing.T) {
	t.Parallel()

	title := "base64:" + base64.StdEncoding.EncodeToString([]byte("Upstream"))
	up := newBuilderTestUpstream(t, http.StatusOK, map[string]string{"Profile-Title": title}, testLink("u", "a.example", "A"))
	src := testSource(52, up.srv.URL)
	sub := activeRuntimeSubscription("builder-upstream-headers", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder { return testBuilder(1, []*database.ProviderSource{src}) })

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, title, headerValue(result.Headers, "profile-title"))
	assert.Empty(t, headerValue(result.Headers, "announce"))
}

func TestBuilder_JSONSourcesKeepJSONFormat(t *testing.T) {
	t.Parallel()

	body := `[{"type":"vless","address":"j1.example","port":443,"uuid":"u1","remark":"J1"},` +
		`{"type":"vless","address":"j2.example","port":443,"uuid":"u2","remark":"J2"}]`
	up := newBuilderTestUpstream(t, http.StatusOK, nil, body)
	src := testSource(61, up.srv.URL)

	sub := activeRuntimeSubscription("builder-json", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{src},
			nodeItem(src.ID, "", "J2", strPtr("Renamed J2"), 0),
			nodeItem(src.ID, "", "J1", nil, 1),
		)
	})

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

	var configs []map[string]any
	require.NoError(t, json.Unmarshal(result.Body, &configs))
	require.Len(t, configs, 2)
	assert.Equal(t, "Renamed J2", configs[0]["remark"])
	assert.Equal(t, "j2.example", configs[0]["address"])
	assert.Equal(t, "J1", configs[1]["remark"])
}

func TestBuilder_MixedFormatsFallBackToLinks(t *testing.T) {
	t.Parallel()

	upJSON := newBuilderTestUpstream(t, http.StatusOK, nil, `{"type":"vless","address":"j.example","port":443,"uuid":"u","remark":"JSON"}`)
	upLinks := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("u", "l.example", "Link"))
	srcJSON := testSource(62, upJSON.srv.URL)
	srcLinks := testSource(63, upLinks.srv.URL)

	sub := activeRuntimeSubscription("builder-mixed", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		return testBuilder(1, []*database.ProviderSource{srcJSON, srcLinks},
			nodeItem(srcLinks.ID, "", "Link", nil, 0),
			nodeItem(srcJSON.ID, "", "JSON", strPtr("From JSON"), 1),
		)
	})

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"Link", "From JSON"}, linkNames(decodeBuilderLinks(t, result)))
}

// ---------------------------------------------------------------------------
// cache
// ---------------------------------------------------------------------------

func TestBuilderCacheKey_ChangesOnConfiguration(t *testing.T) {
	t.Parallel()

	base := func() *database.SubscriptionBuilder {
		src := testSource(1, "https://provider.example/sub")
		return testBuilder(5, []*database.ProviderSource{src}, countryItem(1, "DE", 0))
	}
	baseKey := builderCacheKey("sub", base())
	assert.Equal(t, baseKey, builderCacheKey("sub", base()), "key must be deterministic")
	assert.True(t, strings.HasPrefix(baseKey, "sub:builder:5:1:"))

	for _, tc := range []struct {
		name   string
		mutate func(b *database.SubscriptionBuilder)
	}{
		{"builder version", func(b *database.SubscriptionBuilder) { b.Version++ }},
		{"builder updated_at", func(b *database.SubscriptionBuilder) { b.UpdatedAt = b.UpdatedAt.Add(time.Second) }},
		{"profile title", func(b *database.SubscriptionBuilder) { b.ProfileTitle = "x" }},
		{"source updated_at", func(b *database.SubscriptionBuilder) {
			b.Sources[0].Source.UpdatedAt = b.Sources[0].Source.UpdatedAt.Add(time.Second)
		}},
		{"source disabled", func(b *database.SubscriptionBuilder) { b.Sources[0].Source.Enabled = false }},
		{"source catalogue synced", func(b *database.SubscriptionBuilder) {
			now := time.Unix(300, 0)
			b.Sources[0].Source.LastSyncAt = &now
		}},
		{"source added", func(b *database.SubscriptionBuilder) {
			b.Sources = append(b.Sources, database.SubscriptionBuilderSource{SourceID: 2, Position: 1, Source: testSource(2, "https://b.example")})
		}},
		{"item country", func(b *database.SubscriptionBuilder) { b.Items[0].CountryCode = "NL" }},
		{"item custom name", func(b *database.SubscriptionBuilder) { b.Items[0].CustomName = strPtr("x") }},
		{"item position", func(b *database.SubscriptionBuilder) { b.Items[0].Position = 3 }},
		{"item disabled", func(b *database.SubscriptionBuilder) { b.Items[0].Enabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := base()
			tc.mutate(b)
			assert.NotEqual(t, baseKey, builderCacheKey("sub", b))
		})
	}
}

func TestBuilder_CacheHitAndInvalidation(t *testing.T) {
	t.Parallel()

	up := newBuilderTestUpstream(t, http.StatusOK, nil, testLink("u", "a.example", "A"))
	src := testSource(71, up.srv.URL)

	var current atomic.Pointer[database.SubscriptionBuilder]
	current.Store(testBuilder(1, []*database.ProviderSource{src}))

	sub := activeRuntimeSubscription("builder-cache", nil)
	db := builderTestDB(t, sub, func() *database.SubscriptionBuilder {
		b := *current.Load()
		return &b
	})
	subSvc := newTestSubSvc(t)

	request := func() []string {
		result, _, _, err := HandleSubscription(context.Background(), db, subSvc, sub.SubscriptionID, "", nil)
		require.NoError(t, err)
		return linkNames(decodeBuilderLinks(t, result))
	}

	assert.Equal(t, []string{"A"}, request())
	assert.Equal(t, []string{"A"}, request())
	assert.Equal(t, int32(1), up.hits.Load(), "second request must be served from cache")

	// Builder update (version bump + new title/rename) → fresh build.
	next := testBuilder(1, []*database.ProviderSource{src}, nodeItem(src.ID, "", "A", strPtr("A renamed"), 0))
	next.Version = 2
	current.Store(next)
	assert.Equal(t, []string{"A renamed"}, request())
	assert.Equal(t, int32(2), up.hits.Load())

	// Source change (updated_at) → fresh build.
	changedSrc := *src
	changedSrc.UpdatedAt = src.UpdatedAt.Add(time.Minute)
	next2 := testBuilder(1, []*database.ProviderSource{&changedSrc}, nodeItem(src.ID, "", "A", strPtr("A renamed"), 0))
	next2.Version = 2
	current.Store(next2)
	request()
	assert.Equal(t, int32(3), up.hits.Load())

	// Revocation invalidates builder cache entries as well.
	subSvc.InvalidateCache(sub.SubscriptionID)
	request()
	assert.Equal(t, int32(4), up.hits.Load())
}

// ---------------------------------------------------------------------------
// identity helpers
// ---------------------------------------------------------------------------

func TestEntryFingerprint_StableAcrossRenameAndCredentials(t *testing.T) {
	t.Parallel()

	a := testLink("user-1", "a.example", "Name A")
	assert.Equal(t, EntryFingerprint(a), EntryFingerprint(RenameLink(a, "Other")))
	assert.Equal(t, EntryFingerprint(a), EntryFingerprint(testLink("user-2", "a.example", "Name A")))
	assert.NotEqual(t, EntryFingerprint(a), EntryFingerprint(testLink("user-1", "b.example", "Name A")))
	assert.NotEqual(t, EntryFingerprint(a), EntryFingerprint(strings.Replace(a, "type=tcp", "type=ws", 1)))
	assert.Len(t, EntryFingerprint(a), 64)
	assert.NotContains(t, EntryFingerprint(a), "user-1")
}

func TestRenameLink_VMessAndURI(t *testing.T) {
	t.Parallel()

	vmessJSON := `{"v":"2","ps":"Old","add":"vm.example","port":"443","id":"secret-id","net":"ws","path":"/p"}`
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON))

	renamed := RenameLink(vmess, "🇩🇪 Новое имя")
	assert.Equal(t, "🇩🇪 Новое имя", EntryName(renamed))
	assert.Equal(t, EntryFingerprint(vmess), EntryFingerprint(renamed))

	obj, ok := decodeVMess(strings.TrimPrefix(renamed, "vmess://"))
	require.True(t, ok)
	assert.Equal(t, "secret-id", obj["id"])
	assert.Equal(t, "/p", obj["path"])

	uri := "trojan://pass@t.example:443?sni=t.example#Old%20Name"
	assert.Equal(t, "Old Name", EntryName(uri))
	assert.Equal(t, "trojan://pass@t.example:443?sni=t.example#New%20Name", RenameLink(uri, "New Name"))
	assert.Equal(t, "trojan://pass@t.example:443#X", RenameLink("trojan://pass@t.example:443", "X"))
}

func TestCountryFromName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "DE", countryFromName("🇩🇪 Frankfurt"))
	assert.Equal(t, "NL", countryFromName("Fast 🇳🇱"))
	assert.Equal(t, "", countryFromName("No flag"))
	assert.Equal(t, "", countryFromName(""))
}
