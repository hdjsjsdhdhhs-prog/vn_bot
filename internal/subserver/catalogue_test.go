package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Server-side catalogue sync (FetchSourceCatalogue) against a sanitized copy
// of a real provider response: testdata/provider_xray_configs.json is a JSON
// array of 40 full Xray client configs (Remnawave style: remarks + outbounds +
// routing/balancers). Addresses, ids, passwords, reality keys, auth, SNI and
// tags are replaced with test values; names, protocols and structure are real.

func loadXrayFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "provider_xray_configs.json"))
	require.NoError(t, err)
	return body
}

func catalogueSource(url, format string) database.ProviderSource {
	return database.ProviderSource{ID: 7, Name: "catalogue", Type: format, SubscriptionURL: url, Headers: `{}`, Enabled: true}
}

func serveBody(t *testing.T, status int, contentType string, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func catalogueByName(c *SourceCatalogue) map[string]database.ProviderSourceEntry {
	out := make(map[string]database.ProviderSourceEntry, len(c.Entries))
	for _, e := range c.Entries {
		out[e.OriginalName] = e
	}
	return out
}

func TestFetchSourceCatalogue_RealXrayFixture(t *testing.T) {
	t.Parallel()
	url := serveBody(t, http.StatusOK, "application/json", loadXrayFixture(t))

	c, err := FetchSourceCatalogue(context.Background(), catalogueSource(url, database.SourceFormatAuto))
	require.NoError(t, err)

	assert.Equal(t, "json", c.Format)
	require.Len(t, c.Entries, 40, "every config of the response is a catalogue entry")
	assert.Zero(t, c.Skipped)
	assert.Zero(t, c.Duplicates)

	protocols := map[string]int{}
	countries := map[string]int{}
	noCountry := 0
	fingerprints := map[string]bool{}
	for _, e := range c.Entries {
		protocols[e.Protocol]++
		if e.CountryCode == "" {
			noCountry++
		} else {
			countries[e.CountryCode]++
		}
		assert.Len(t, e.Fingerprint, 64)
		assert.False(t, fingerprints[e.Fingerprint], "unique fingerprints")
		fingerprints[e.Fingerprint] = true
		assert.NotContains(t, e.OriginalName, "redacted", "no credential in a catalogue name")
	}
	assert.Equal(t, map[string]int{"vless": 33, "hysteria2": 4, "shadowsocks": 3}, protocols)
	assert.Equal(t, 23, len(countries), "unique countries")
	assert.Equal(t, 1, noCountry, "the section header has no flag")
	assert.Equal(t, map[string]int{
		"CH": 7, "GB": 3, "FI": 3, "TR": 3, "DE": 2, "NL": 2, "IT": 2, "CA": 2,
		"EU": 1, "SE": 1, "PL": 1, "FR": 1, "HU": 1, "ES": 1, "RU": 1, "KZ": 1,
		"BR": 1, "IN": 1, "BE": 1, "JP": 1, "US": 1, "HK": 1, "MD": 1,
	}, countries)

	byName := catalogueByName(c)
	for name, want := range map[string][2]string{
		"🇪🇺 🚀Авто | Лучший сервер ⚡⚡": {"EU", "vless"},
		"🇩🇪⚡Германия":                 {"DE", "vless"},
		"🇨🇭🎮Швейцария GAMING":         {"CH", "hysteria2"},
		"🇬🇧Англия SS":                 {"GB", "shadowsocks"},
		"🇩🇪⚪Германия (БС-5)☁️":        {"DE", "vless"},
		"⬇️ Обходы белых списков ⬇️":  {"", "vless"},
	} {
		e, ok := byName[name]
		require.True(t, ok, name)
		assert.Equal(t, want[0], e.CountryCode, name)
		assert.Equal(t, want[1], e.Protocol, name)
	}
}

// The provider reorders its response on every request and may rotate
// credentials: the identity of a server must survive both.
func TestFetchSourceCatalogue_FingerprintSurvivesReorderAndCredentialRotation(t *testing.T) {
	t.Parallel()
	body := loadXrayFixture(t)
	var configs []json.RawMessage
	require.NoError(t, json.Unmarshal(body, &configs))

	reversed := make([]json.RawMessage, len(configs))
	for i := range configs {
		rotated := strings.ReplaceAll(string(configs[i]), "redacted-id-", "rotated-id-")
		rotated = strings.ReplaceAll(rotated, "redacted-password-", "rotated-password-")
		rotated = strings.ReplaceAll(rotated, "redacted-publicKey-", "rotated-publicKey-")
		reversed[len(configs)-1-i] = json.RawMessage(rotated)
	}
	shuffled, err := json.Marshal(reversed)
	require.NoError(t, err)
	require.NotEqual(t, string(body), string(shuffled))

	a, err := FetchSourceCatalogue(context.Background(), catalogueSource(serveBody(t, 200, "", body), "auto"))
	require.NoError(t, err)
	b, err := FetchSourceCatalogue(context.Background(), catalogueSource(serveBody(t, 200, "", shuffled), "auto"))
	require.NoError(t, err)

	fps := func(c *SourceCatalogue) map[string]string {
		out := map[string]string{}
		for _, e := range c.Entries {
			out[e.OriginalName] = e.Fingerprint
		}
		return out
	}
	assert.Equal(t, fps(a), fps(b))
	require.Len(t, b.Entries, len(a.Entries))
	for i := range a.Entries {
		assert.Equal(t, a.Entries[i].OriginalName, b.Entries[len(b.Entries)-1-i].OriginalName, "upstream order is kept")
	}
}

// A provider section header reuses the outbounds of a real server: both stay
// separate entries (the real server keeps its country), identical configs
// with the same name stay one entry.
func TestFetchSourceCatalogue_XrayCollisionKeepsNamedConfigs(t *testing.T) {
	t.Parallel()
	body := loadXrayFixture(t)
	var configs []json.RawMessage
	require.NoError(t, json.Unmarshal(body, &configs))
	// Repeat the first config verbatim: a true duplicate.
	withDup, err := json.Marshal(append(configs, configs[0]))
	require.NoError(t, err)

	c, err := FetchSourceCatalogue(context.Background(), catalogueSource(serveBody(t, 200, "", withDup), "auto"))
	require.NoError(t, err)
	assert.Len(t, c.Entries, 40)
	assert.Equal(t, 1, c.Duplicates)

	byName := catalogueByName(c)
	section, real := byName["⬇️ Обходы белых списков ⬇️"], byName["🇩🇪⚪Германия (БС-5)☁️"]
	assert.NotEqual(t, section.Fingerprint, real.Fingerprint)
	assert.Equal(t, "DE", real.CountryCode)
}

func TestFetchSourceCatalogue_Failures(t *testing.T) {
	t.Parallel()
	fixture := loadXrayFixture(t)

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)

	for _, tc := range []struct {
		name    string
		url     string
		format  string
		timeout time.Duration
		code    string
	}{
		{"http status", serveBody(t, http.StatusServiceUnavailable, "", []byte("down")), "auto", 0, "http_503"},
		{"unreachable", closedURL, "auto", 0, "unreachable"},
		{"timeout", slow.URL, "auto", 200 * time.Millisecond, "timeout"},
		{"empty", serveBody(t, 200, "", []byte("  \n")), "auto", 0, "empty_response"},
		{"unknown format", serveBody(t, 200, "text/html", []byte("<html>blocked</html>")), "auto", 0, "unknown_format"},
		{"format mismatch", serveBody(t, 200, "", fixture), database.SourceFormatBase64, 0, "format_mismatch:json"},
		{"no servers", serveBody(t, 200, "", []byte(`[{"remarks":"x","outbounds":[{"protocol":"freedom"}]}]`)), "auto", 0, "no_servers"},
		{"invalid config", "ftp://provider.example/sub", "auto", 0, "invalid_config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)
				defer cancel()
			}
			_, err := FetchSourceCatalogue(ctx, catalogueSource(tc.url, tc.format))
			var syncErr *SourceSyncError
			require.ErrorAs(t, err, &syncErr)
			assert.Equal(t, tc.code, syncErr.Code)
			assert.NotContains(t, err.Error(), "127.0.0.1", "failure codes never carry the URL")
			assert.NotContains(t, err.Error(), "provider.example")
		})
	}
}

func TestFetchSourceCatalogue_PartialJSONIsReported(t *testing.T) {
	t.Parallel()
	body := `[
		{"type":"vless","address":"198.51.100.1","port":443,"uuid":"u1","security":"tls","sni":"a.example","network":"tcp","remark":"🇩🇪 Flat A"},
		{"type":"vless","address":"","port":443,"uuid":"u2","remark":"🇩🇪 Broken (no address)"},
		{"remarks":"🇳🇱 No proxy","outbounds":[{"protocol":"freedom","tag":"direct"}]},
		{"type":"trojan","address":"198.51.100.2","port":443,"password":"p","security":"tls","sni":"b.example","remark":"🇳🇱 Flat B"}
	]`
	c, err := FetchSourceCatalogue(context.Background(), catalogueSource(serveBody(t, 200, "", []byte(body)), "json"))
	require.NoError(t, err)
	assert.Equal(t, "json", c.Format)
	assert.Equal(t, 2, c.Skipped, "unparsed servers are counted, never hidden")
	names := []string{}
	for _, e := range c.Entries {
		names = append(names, e.OriginalName)
	}
	assert.Equal(t, []string{"🇩🇪 Flat A", "🇳🇱 Flat B"}, names)
	assert.Equal(t, "trojan", c.Entries[1].Protocol)
}

func TestFetchSourceCatalogue_Base64LinksDedupAndProtocols(t *testing.T) {
	t.Parallel()
	links := strings.Join([]string{
		"#profile-title: comment lines are not servers",
		testLink("u1", "de.example", "🇩🇪 Berlin"),
		testLink("u2", "de.example", "🇩🇪 Berlin (same server)"),
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:pw")) + "@198.51.100.9:8388#" + "🇯🇵 Tokyo",
		"hy2://pw@198.51.100.10:443?sni=h.example#" + "🇫🇮 Helsinki",
		"not a server line",
	}, "\n")
	body := []byte(base64.StdEncoding.EncodeToString([]byte(links)))

	c, err := FetchSourceCatalogue(context.Background(), catalogueSource(serveBody(t, 200, "", body), "auto"))
	require.NoError(t, err)
	assert.Equal(t, "base64", c.Format)
	assert.Equal(t, 1, c.Duplicates, "the same server under two names is one entry")
	assert.Equal(t, 1, c.Skipped, "the junk line is reported")
	got := map[string][2]string{}
	for _, e := range c.Entries {
		got[e.OriginalName] = [2]string{e.CountryCode, e.Protocol}
	}
	assert.Equal(t, map[string][2]string{
		"🇩🇪 Berlin":   {"DE", "vless"},
		"🇯🇵 Tokyo":    {"JP", "shadowsocks"},
		"🇫🇮 Helsinki": {"FI", "hysteria2"},
	}, got)
}

func TestXrayPrimaryLink_FixtureProtocols(t *testing.T) {
	t.Parallel()
	var configs []json.RawMessage
	require.NoError(t, json.Unmarshal(loadXrayFixture(t), &configs))
	schemes := map[string]int{}
	for _, raw := range configs {
		info, ok := parseXrayFullConfig(raw)
		require.True(t, ok)
		link, ok := xrayPrimaryLink(raw, info.name)
		require.True(t, ok, info.name)
		require.True(t, isValidServer(link), info.name)
		assert.Equal(t, info.name, EntryName(link), "the link carries the config name")
		scheme, _, _ := strings.Cut(link, "://")
		schemes[scheme]++
	}
	assert.Equal(t, map[string]int{"vless": 33, "hysteria2": 4, "ss": 3}, schemes)
}

// ---------------------------------------------------------------------------
// Multiple sources: catalogue per source, Builder merges, Preview == /sub
// ---------------------------------------------------------------------------

type catalogueE2E struct {
	t    *testing.T
	ctx  context.Context
	db   *database.Service
	keys int
}

func newCatalogueE2E(t *testing.T) *catalogueE2E {
	t.Helper()
	svc, err := database.NewService(filepath.Join(t.TempDir(), "catalogue-e2e.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	return &catalogueE2E{t: t, ctx: context.Background(), db: svc}
}

func (e *catalogueE2E) meta() database.AdminConfigMeta {
	e.keys++
	return database.AdminConfigMeta{Actor: "test-admin", RequestKey: fmt.Sprintf("catalogue-%d", e.keys)}
}

// source creates a provider source served by body() and syncs its catalogue
// exactly like BuilderService.SyncSource (fetch + parse + SyncSourceEntries).
func (e *catalogueE2E) source(name string, body func() []byte) *database.ProviderSource {
	e.t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body()) }))
	e.t.Cleanup(srv.Close)
	src := &database.ProviderSource{Name: name, Type: "auto", SubscriptionURL: srv.URL, Headers: `{}`, Enabled: true}
	require.NoError(e.t, e.db.CreateProviderSource(e.ctx, src))
	e.sync(src)
	return src
}

func (e *catalogueE2E) sync(src *database.ProviderSource) database.SourceSyncCounts {
	e.t.Helper()
	c, err := FetchSourceCatalogue(e.ctx, *src)
	require.NoError(e.t, err)
	counts, err := e.db.SyncSourceEntries(e.ctx, src.ID, c.Entries, database.SourceSyncOK, "")
	require.NoError(e.t, err)
	return counts
}

func (e *catalogueE2E) subscription(builderID uint) string {
	e.t.Helper()
	plan, err := e.db.GetPlanByName(e.ctx, database.FreePlanName)
	require.NoError(e.t, err)
	sub := &database.Subscription{
		TelegramID: 515151, ClientID: "client-catalogue", SubscriptionID: fmt.Sprintf("catalogue-sub-%d", builderID),
		ExpiresAt: timePointer(time.Now().Add(time.Hour)), Status: string(database.SubscriptionStatusActive), PlanID: plan.ID,
	}
	require.NoError(e.t, e.db.CreateSubscription(e.ctx, sub, ""))
	_, err = e.db.SetSubscriptionBuilder(e.ctx, e.meta(), sub.ID, &builderID)
	require.NoError(e.t, err)
	return sub.SubscriptionID
}

func (e *catalogueE2E) serve(subID string) *SubscriptionResult {
	e.t.Helper()
	result, _, _, err := HandleSubscription(e.ctx, e.db, newTestSubSvc(e.t), subID, "", nil)
	require.NoError(e.t, err)
	return result
}

func (e *catalogueE2E) previewNames(builderID uint) []string {
	e.t.Helper()
	res, err := e.db.PreviewBuilder(e.ctx, builderID)
	require.NoError(e.t, err)
	out := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		if it.Entry != nil && (it.Status == database.FingerprintStatusMatched || it.Status == database.FingerprintStatusFallback) {
			out = append(out, it.DisplayName)
		}
	}
	return out
}

func jsonConfigNames(t *testing.T, result *SubscriptionResult) []string {
	t.Helper()
	var configs []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(result.Body, &configs), "all-JSON builders answer with a JSON array")
	out := make([]string, 0, len(configs))
	for _, c := range configs {
		var name string
		require.NoError(t, json.Unmarshal(c["remarks"], &name))
		out = append(out, name)
	}
	return out
}

func TestCatalogue_BuilderJSONSourceMatchesPreview(t *testing.T) {
	t.Parallel()
	e := newCatalogueE2E(t)
	fixture := loadXrayFixture(t)
	src := e.source("Liberty", func() []byte { return fixture })

	stats, err := e.db.SourceCatalogueStatsFor(e.ctx, src.ID)
	require.NoError(t, err)
	assert.Equal(t, 40, stats.Entries)
	assert.Equal(t, 23, stats.Countries)

	res, err := e.db.CreateBuilder(e.ctx, e.meta(), database.BuilderCreateInput{Name: "json-only", Enabled: true})
	require.NoError(t, err)
	require.NoError(t, e.db.SetBuilderSources(e.ctx, res.TargetID, []uint{src.ID}))
	_, err = e.db.UpsertBuilderItem(e.ctx, database.SubscriptionBuilderItem{
		BuilderID: res.TargetID, Kind: database.BuilderItemKindCountry, SourceID: src.ID, CountryCode: "CH", Position: 0, Enabled: true,
	})
	require.NoError(t, err)
	custom := "Germany EXTRA"
	de, err := e.db.GetSourceEntries(e.ctx, src.ID, "DE")
	require.NoError(t, err)
	require.Len(t, de, 2)
	_, err = e.db.UpsertBuilderItem(e.ctx, database.SubscriptionBuilderItem{
		BuilderID: res.TargetID, Kind: database.BuilderItemKindNode, SourceID: src.ID,
		Fingerprint: de[0].Fingerprint, OriginalName: de[0].OriginalName, CustomName: &custom, Position: 1, Enabled: true,
	})
	require.NoError(t, err)

	served := jsonConfigNames(t, e.serve(e.subscription(res.TargetID)))
	preview := e.previewNames(res.TargetID)
	assert.Len(t, served, 8, "7 CH configs + 1 renamed DE node")
	assert.Equal(t, preview, served, "Preview shows exactly what /sub serves")
	assert.Equal(t, custom, served[7])
}

func TestCatalogue_MultipleSourcesMergedOnlyInBuilder(t *testing.T) {
	t.Parallel()
	e := newCatalogueE2E(t)
	fixture := loadXrayFixture(t)
	linksV1 := strings.Join([]string{
		testLink("u", "de-b.example", "🇩🇪 B Frankfurt"),
		testLink("u", "jp-b.example", "🇯🇵 B Tokyo"),
	}, "\n")
	current := linksV1
	a := e.source("Source A", func() []byte { return fixture })
	b := e.source("Source B", func() []byte { return []byte(base64.StdEncoding.EncodeToString([]byte(current))) })

	// Each source keeps its own catalogue and statistics.
	all, err := e.db.SourceCatalogueStatsAll(e.ctx)
	require.NoError(t, err)
	assert.Equal(t, 40, all[a.ID].Entries)
	assert.Equal(t, 23, all[a.ID].Countries)
	assert.Equal(t, 2, all[b.ID].Entries)
	assert.Equal(t, 2, all[b.ID].Countries)

	res, err := e.db.CreateBuilder(e.ctx, e.meta(), database.BuilderCreateInput{Name: "multi", Enabled: true})
	require.NoError(t, err)
	bid := res.TargetID
	require.NoError(t, e.db.SetBuilderSources(e.ctx, bid, []uint{a.ID, b.ID}))
	for _, it := range []database.SubscriptionBuilderItem{
		// Dynamic country rules: current and future DE entries of each source.
		{BuilderID: bid, Kind: database.BuilderItemKindCountry, SourceID: a.ID, CountryCode: "DE", Position: 0, Enabled: true},
		{BuilderID: bid, Kind: database.BuilderItemKindCountry, SourceID: b.ID, CountryCode: "DE", Position: 1, Enabled: true},
		// Node rule: Source B + a concrete node.
		{BuilderID: bid, Kind: database.BuilderItemKindNode, SourceID: b.ID, OriginalName: "🇯🇵 B Tokyo", Position: 2, Enabled: true},
	} {
		_, err := e.db.UpsertBuilderItem(e.ctx, it)
		require.NoError(t, err)
	}
	subID := e.subscription(bid)

	// A JSON source + a link source answer with share links; the Xray configs
	// of source A are expressed through their primary outbound.
	served := linkNames(decodeBuilderLinks(t, e.serve(subID)))
	want := []string{"🇩🇪⚡Германия", "🇩🇪⚪Германия (БС-5)☁️", "🇩🇪 B Frankfurt", "🇯🇵 B Tokyo"}
	sort.Strings(served[:2])
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedWant[:2])
	assert.Equal(t, sortedWant, served)
	preview := e.previewNames(bid)
	sort.Strings(preview[:2])
	assert.Equal(t, sortedWant, preview, "Preview matches /sub across several sources")

	// Refresh of B adds a DE node: the country rule picks it up without any
	// builder change; the Tokyo node disappears and is no longer served.
	current = strings.Join([]string{
		testLink("u", "de-b.example", "🇩🇪 B Frankfurt"),
		testLink("u", "de2-b.example", "🇩🇪 B Munich"),
	}, "\n")
	counts := e.sync(b)
	assert.Equal(t, database.SourceSyncCounts{Added: 1, Updated: 1, Removed: 1, Total: 2}, counts)
	gone, err := e.db.ListSourceEntriesAll(e.ctx, b.ID)
	require.NoError(t, err)
	require.Len(t, gone, 3)
	assert.Equal(t, "🇯🇵 B Tokyo", gone[2].OriginalName)
	assert.False(t, gone[2].Present, "a disappeared node is kept as not present")

	preview = e.previewNames(bid)
	assert.Equal(t, []string{"🇩🇪 B Frankfurt", "🇩🇪 B Munich"}, preview[2:])
	served = linkNames(decodeBuilderLinks(t, e.serve(subID)))
	assert.Equal(t, preview[2:], served[2:], "/sub follows the refreshed catalogue")
}
