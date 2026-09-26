package subserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"

	"go.uber.org/zap"
)

// builderEntry is one server parsed from a live provider source response.
// link is always populated (JSON configs are converted for identity/naming);
// raw is set only for entries that came from a JSON source.
type builderEntry struct {
	sourceID    uint
	link        string
	raw         json.RawMessage
	name        string
	fingerprint string
	country     string
}

// builderSource is the parsed state of one successfully fetched source.
type builderSource struct {
	source  database.ProviderSource
	headers map[string]string
	entries []builderEntry
	allJSON bool
}

// builderPick is one entry selected for the output with its display name.
type builderPick struct {
	entry builderEntry
	name  string
}

// builderAggregate is the result of a builder build before buildResponse.
type builderAggregate struct {
	agg          aggregatedSources
	trafficLimit int64
}

// builderCacheKey derives the response cache key for a builder-served
// subscription. It covers the builder version/metadata, the linked sources
// (updated_at, enabled, last catalogue sync) and every item rule, so any
// configuration change is served fresh instead of from a stale cache entry.
func builderCacheKey(subID string, b *database.SubscriptionBuilder) string {
	h := sha256.New()
	fmt.Fprintf(h, "b|%d|%d|%d|%t|%q|%q|%q\n",
		b.ID, b.Version, b.UpdatedAt.UnixNano(), b.Enabled, b.ProfileTitle, b.SupportURL, b.Announce)

	for _, s := range b.Sources {
		fmt.Fprintf(h, "s|%d|%d", s.SourceID, s.Position)
		if s.Source != nil {
			var lastSync int64
			if s.Source.LastSyncAt != nil {
				lastSync = s.Source.LastSyncAt.UnixNano()
			}
			fmt.Fprintf(h, "|%d|%t|%d", s.Source.UpdatedAt.UnixNano(), s.Source.Enabled, lastSync)
		}
		fmt.Fprint(h, "\n")
	}

	for _, it := range b.Items {
		custom := "<nil>"
		if it.CustomName != nil {
			custom = *it.CustomName
		}
		fmt.Fprintf(h, "i|%d|%s|%d|%q|%q|%q|%q|%d|%t\n",
			it.ID, it.Kind, it.SourceID, it.CountryCode, it.Fingerprint, it.OriginalName, custom, it.Position, it.Enabled)
	}

	return fmt.Sprintf("%s:builder:%d:%d:%s", subID, b.ID, b.Version, hex.EncodeToString(h.Sum(nil))[:16])
}

// fetchAndAggregateBuilder fetches the builder's provider sources, applies its
// country/node rules, renames and orders the selected entries and returns the
// aggregate consumed by buildResponse. Per-source fetch failures are
// best-effort (the source is skipped); if no source could be fetched the
// request fails with ErrProviderSourceUnavailable and never falls back to the
// legacy pipeline.
func fetchAndAggregateBuilder(ctx context.Context, db interfaces.SubscriptionRepository, subID string, b *database.SubscriptionBuilder) (builderAggregate, int, int, error) {
	sources, selected := builderSourcesToFetch(subID, b)
	if selected == 0 {
		// Configuration selects nothing (no linked sources, or every rule is
		// disabled / points to an unlinked source): nothing to serve, the
		// sources themselves are not "unavailable".
		logger.Warn("Subscription builder selects no provider sources",
			zap.String("sub_id", subID),
			zap.Uint("builder_id", b.ID))

		metrics.SubserverNoItemsTotal.Inc()

		return builderAggregate{}, 0, 0, ErrNoSubscriptionItems
	}

	if len(sources) == 0 {
		logger.Warn("Subscription builder has no usable provider sources",
			zap.String("sub_id", subID),
			zap.Uint("builder_id", b.ID))

		return builderAggregate{}, 0, 0, ErrProviderSourceUnavailable
	}

	responses := make([]*NodeResponse, len(sources))
	sem := make(chan struct{}, maxSourceConcurrency)

	var wg sync.WaitGroup

	for i := range sources {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)

		sem <- struct{}{}

		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			responses[idx] = fetchBuilderSource(ctx, subID, b.ID, sources[idx])
		}(i)
	}

	wg.Wait()

	parsed := make([]builderSource, 0, len(sources))

	for i, resp := range responses {
		if resp == nil {
			continue
		}

		countries, err := sourceCatalogueCountries(ctx, db, sources[i].ID)
		if err != nil {
			logger.Error("Failed to load provider source catalogue",
				zap.String("sub_id", subID),
				zap.Uint("builder_id", b.ID),
				zap.Uint("provider_source_id", sources[i].ID),
				zap.Error(err))

			return builderAggregate{}, 0, len(sources), fmt.Errorf("database error: %w", err)
		}

		parsed = append(parsed, parseBuilderSource(subID, sources[i], resp, countries))
	}

	success := len(parsed)
	total := len(sources)

	if success == 0 {
		return builderAggregate{}, 0, total, ErrProviderSourceUnavailable
	}

	picks := selectBuilderEntries(subID, b, parsed)

	return builderAggregate{
		agg:          assembleBuilderAggregate(b, parsed, picks),
		trafficLimit: builderTrafficLimit(parsed),
	}, success, total, nil
}

// builderSourcesToFetch returns the linked, enabled and valid sources in
// builder position order. With item rules present only sources referenced by
// an enabled item are fetched; without rules every linked source is merged.
// selected counts linked sources chosen by the configuration before the
// enabled/validity filter, so callers can tell "selects nothing" apart from
// "selected sources are unavailable".
func builderSourcesToFetch(subID string, b *database.SubscriptionBuilder) ([]database.ProviderSource, int) {
	var referenced map[uint]bool
	if len(b.Items) > 0 {
		referenced = make(map[uint]bool)
		for _, it := range b.Items {
			if it.Enabled {
				referenced[it.SourceID] = true
			}
		}
	}

	out := make([]database.ProviderSource, 0, len(b.Sources))
	selected := 0

	for _, link := range b.Sources {
		if referenced != nil && !referenced[link.SourceID] {
			continue
		}

		selected++

		if link.Source == nil || !link.Source.Enabled {
			logger.Warn("Skipping disabled or missing builder source",
				zap.String("sub_id", subID),
				zap.Uint("builder_id", b.ID),
				zap.Uint("provider_source_id", link.SourceID))

			continue
		}

		if _, _, err := validateProviderSourceConfiguration(*link.Source); err != nil {
			logger.Warn("Skipping unusable builder source",
				zap.String("sub_id", subID),
				zap.Uint("builder_id", b.ID),
				zap.Uint("provider_source_id", link.SourceID))

			continue
		}

		out = append(out, *link.Source)
	}

	return out, selected
}

func fetchBuilderSource(ctx context.Context, subID string, builderID uint, source database.ProviderSource) *NodeResponse {
	start := time.Now()
	resp, err := FetchFromProviderSource(ctx, source)
	duration := time.Since(start).Seconds()

	if err != nil {
		metrics.SubserverSourceFetchTotal.WithLabelValues("error", "unknown").Inc()
		metrics.SubserverSourceFetchDuration.WithLabelValues("error").Observe(duration)
		logger.Warn("Builder source fetch failed",
			zap.String("sub_id", subID),
			zap.Uint("builder_id", builderID),
			zap.Uint("provider_source_id", source.ID))

		return nil
	}

	metrics.SubserverSourceFetchTotal.WithLabelValues("success", DetectFormat(resp.Body).String()).Inc()
	metrics.SubserverSourceFetchDuration.WithLabelValues("success").Observe(duration)

	return resp
}

// sourceCatalogueCountries maps fingerprint -> country_code from the stored
// catalogue. The catalogue only enriches entries; live data is authoritative.
func sourceCatalogueCountries(ctx context.Context, db interfaces.SubscriptionRepository, sourceID uint) (map[string]string, error) {
	entries, err := db.GetSourceEntries(ctx, sourceID, "*")
	if err != nil {
		return nil, fmt.Errorf("get source entries: %w", err)
	}

	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.Fingerprint != "" && e.CountryCode != "" {
			out[e.Fingerprint] = e.CountryCode
		}
	}

	return out, nil
}

// parseBuilderSource parses one source response with the same format
// detection/normalization as the legacy pipeline and turns every server into
// a builderEntry with a name, fingerprint and country.
func parseBuilderSource(subID string, source database.ProviderSource, resp *NodeResponse, countries map[string]string) builderSource {
	headers := resp.Headers
	if headers == nil {
		headers = map[string]string{}
	}

	agg := aggregatedSources{allJSON: true}
	aggregateFormat(&agg, DetectFormat(resp.Body), resp.Body, database.Node{ID: source.ID, Name: source.Name}, subID)

	out := builderSource{source: source, headers: headers, allJSON: agg.allJSON && len(agg.jsonConfigs) > 0}

	for _, link := range agg.items {
		out.entries = append(out.entries, newBuilderEntry(source.ID, link, nil, EntryName(link), countries))
	}

	for _, raw := range agg.jsonConfigs {
		link, err := ConvertSingleJSONToLink(raw)
		if err != nil {
			logger.Debug("Skipping unconvertible builder JSON entry",
				zap.String("sub_id", subID),
				zap.Uint("provider_source_id", source.ID))

			continue
		}

		name := EntryName(link)
		if cfg, cfgErr := toServerConfig(raw); cfgErr == nil {
			if cfg.Remark != "" {
				name = cfg.Remark
			} else if cfg.Ps != "" {
				name = cfg.Ps
			}
		}

		out.entries = append(out.entries, newBuilderEntry(source.ID, link, raw, name, countries))
	}

	return out
}

func newBuilderEntry(sourceID uint, link string, raw json.RawMessage, name string, countries map[string]string) builderEntry {
	fp := EntryFingerprint(link)

	country := countries[fp]
	if country == "" {
		country = countryFromName(name)
	}

	return builderEntry{
		sourceID:    sourceID,
		link:        link,
		raw:         raw,
		name:        name,
		fingerprint: fp,
		country:     country,
	}
}

// selectBuilderEntries applies the builder rules in position order. Without
// rules every entry of every fetched source is merged in source order.
// Duplicate entries (same source + fingerprint) keep their first position.
func selectBuilderEntries(subID string, b *database.SubscriptionBuilder, parsed []builderSource) []builderPick {
	bySource := make(map[uint][]builderEntry, len(parsed))
	for _, p := range parsed {
		bySource[p.source.ID] = p.entries
	}

	linked := make(map[uint]bool, len(b.Sources))
	for _, s := range b.Sources {
		linked[s.SourceID] = true
	}

	seen := make(map[string]bool)
	var picks []builderPick

	add := func(e builderEntry, name string) {
		key := fmt.Sprintf("%d|%s", e.sourceID, e.fingerprint)
		if seen[key] {
			return
		}
		seen[key] = true
		picks = append(picks, builderPick{entry: e, name: name})
	}

	if len(b.Items) == 0 {
		for _, p := range parsed {
			for _, e := range p.entries {
				add(e, e.name)
			}
		}

		return picks
	}

	items := make([]database.SubscriptionBuilderItem, len(b.Items))
	copy(items, b.Items)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].ID < items[j].ID
	})

	for _, it := range items {
		if !it.Enabled {
			continue
		}

		fields := []zap.Field{
			zap.String("sub_id", subID),
			zap.Uint("builder_id", b.ID),
			zap.Uint("item_id", it.ID),
			zap.Uint("provider_source_id", it.SourceID),
		}

		if !linked[it.SourceID] {
			logger.Warn("Builder item references a source not linked to the builder", fields...)
			continue
		}

		entries, ok := bySource[it.SourceID]
		if !ok {
			logger.Warn("Builder item skipped: source unavailable",
				append(fields, zap.String("status", string(database.FingerprintStatusMissing)))...)
			continue
		}

		switch it.Kind {
		case database.BuilderItemKindCountry:
			for _, e := range entries {
				if strings.EqualFold(e.country, it.CountryCode) {
					add(e, displayName(it, e))
				}
			}
		case database.BuilderItemKindNode:
			idx, status := resolveNodeEntry(it, entries)
			switch status {
			case database.FingerprintStatusMatched:
				add(entries[idx], displayName(it, entries[idx]))
			case database.FingerprintStatusFallback:
				logger.Warn("Builder node matched by original_name only", append(fields, zap.String("status", string(status)))...)
				add(entries[idx], displayName(it, entries[idx]))
			default:
				logger.Warn("Builder node item not served", append(fields, zap.String("status", string(status)))...)
			}
		default:
			logger.Warn("Builder item has unknown kind", fields...)
		}
	}

	return picks
}

// resolveNodeEntry resolves a node item against live entries following the
// fallback fingerprint spec: exact fingerprint first, then a unique
// original_name (fallback); zero name matches is missing, several is conflict.
func resolveNodeEntry(it database.SubscriptionBuilderItem, entries []builderEntry) (int, database.FingerprintStatus) {
	if it.Fingerprint != "" {
		for i := range entries {
			if entries[i].fingerprint == it.Fingerprint {
				return i, database.FingerprintStatusMatched
			}
		}
	}

	if it.OriginalName == "" {
		return -1, database.FingerprintStatusMissing
	}

	match := -1
	count := 0
	for i := range entries {
		if entries[i].name == it.OriginalName {
			if match < 0 {
				match = i
			}
			count++
		}
	}

	switch count {
	case 0:
		return -1, database.FingerprintStatusMissing
	case 1:
		return match, database.FingerprintStatusFallback
	default:
		return -1, database.FingerprintStatusConflict
	}
}

func displayName(it database.SubscriptionBuilderItem, e builderEntry) string {
	if it.CustomName != nil && strings.TrimSpace(*it.CustomName) != "" {
		return *it.CustomName
	}

	return e.name
}

// assembleBuilderAggregate turns the picks into the aggregate used by
// buildResponse, keeping the current response format: a JSON array when every
// fetched source is pure JSON, otherwise base64 share links. Headers come from
// the first fetched source with builder metadata applied on top.
func assembleBuilderAggregate(b *database.SubscriptionBuilder, parsed []builderSource, picks []builderPick) aggregatedSources {
	agg := aggregatedSources{allJSON: true}

	for _, p := range parsed {
		if !p.allJSON {
			agg.allJSON = false
		}

		updateMinExpire(&agg, p.headers)
		agg.totalUpload += ParseUserInfoValue(p.headers, "upload")
		agg.totalDownload += ParseUserInfoValue(p.headers, "download")
	}

	agg.firstSourceHeaders = applyBuilderHeaders(parsed[0].headers, b)

	for _, pick := range picks {
		if agg.allJSON && pick.entry.raw != nil {
			agg.jsonConfigs = append(agg.jsonConfigs, renameJSONConfig(pick.entry.raw, pick.name))
			continue
		}

		agg.items = append(agg.items, RenameLink(pick.entry.link, pick.name))
	}

	if len(agg.items) > 0 {
		agg.allJSON = false
	}

	return agg
}

// builderTrafficLimit sums upstream totals; any unlimited source (total=0)
// makes the combined subscription unlimited.
func builderTrafficLimit(parsed []builderSource) int64 {
	var sum int64
	for _, p := range parsed {
		t := ParseUserInfoValue(p.headers, "total")
		if t <= 0 {
			return 0
		}
		sum += t
	}

	return sum
}

// applyBuilderHeaders overrides profile-title/support-url/announce with the
// builder metadata when configured. Title and announce use the base64: form
// understood by Happ/v2RayTun/Hiddify clients.
func applyBuilderHeaders(src map[string]string, b *database.SubscriptionBuilder) map[string]string {
	out := make(map[string]string, len(src)+3)
	maps.Copy(out, src)

	if t := strings.TrimSpace(b.ProfileTitle); t != "" {
		out["profile-title"] = "base64:" + base64.StdEncoding.EncodeToString([]byte(t))
	}

	if u := strings.TrimSpace(b.SupportURL); u != "" {
		out["support-url"] = u
	}

	if a := strings.TrimSpace(b.Announce); a != "" {
		out["announce"] = "base64:" + base64.StdEncoding.EncodeToString([]byte(a))
	}

	return out
}

// ---------------------------------------------------------------------------
// Entry identity, naming and renaming
// ---------------------------------------------------------------------------

// fingerprintQueryKeys are the transport parameters that identify a server.
// Credentials (uuid/password), reality keys and the display name are excluded
// so the fingerprint survives renames and credential rotation.
var fingerprintQueryKeys = []string{"type", "security", "sni", "host", "path", "serviceName", "mode", "headerType"}

// EntryFingerprint returns a stable, credential-free identity (sha256 hex) for
// a share link: protocol, host, port and transport parameters.
func EntryFingerprint(link string) string {
	identity, ok := linkIdentity(strings.TrimSpace(link))
	if !ok {
		base, _, _ := strings.Cut(strings.TrimSpace(link), "#")
		identity = "raw|" + base
	}

	sum := sha256.Sum256([]byte("v1|" + identity))

	return hex.EncodeToString(sum[:])
}

func linkIdentity(link string) (string, bool) {
	scheme, rest, ok := strings.Cut(link, "://")
	if !ok {
		return "", false
	}

	scheme = strings.ToLower(scheme)
	if scheme == "hy2" {
		scheme = "hysteria2"
	}

	if scheme == "vmess" {
		obj, ok := decodeVMess(rest)
		if !ok {
			return "", false
		}

		parts := []string{"vmess", strings.ToLower(jsonString(obj["add"])), jsonString(obj["port"])}
		for _, k := range []string{"net", "type", "tls", "sni", "host", "path"} {
			parts = append(parts, k+"="+jsonString(obj[k]))
		}

		return strings.Join(parts, "|"), true
	}

	u, err := url.Parse(link)
	if err != nil {
		return "", false
	}

	host, port := u.Hostname(), u.Port()
	if scheme == "ss" && (host == "" || port == "") {
		// Legacy ss://base64(method:password@host:port)#name
		host, port = legacySSHostPort(rest)
	}

	if host == "" {
		return "", false
	}

	parts := []string{scheme, strings.ToLower(host), port}
	q := u.Query()
	for _, k := range fingerprintQueryKeys {
		parts = append(parts, k+"="+q.Get(k))
	}

	return strings.Join(parts, "|"), true
}

func legacySSHostPort(rest string) (string, string) {
	payload, _, _ := strings.Cut(rest, "#")
	payload, _, _ = strings.Cut(payload, "?")

	decoded, ok := decodeBase64Any(payload)
	if !ok {
		return "", ""
	}

	at := strings.LastIndexByte(decoded, '@')
	if at < 0 {
		return "", ""
	}

	host, port, err := net.SplitHostPort(decoded[at+1:])
	if err != nil {
		return "", ""
	}

	return host, port
}

func decodeBase64Any(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}

	return "", false
}

func decodeVMess(payload string) (map[string]any, bool) {
	payload, _, _ = strings.Cut(payload, "#")

	decoded, ok := decodeBase64Any(payload)
	if !ok {
		return nil, false
	}

	dec := json.NewDecoder(strings.NewReader(decoded))
	dec.UseNumber()

	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return nil, false
	}

	return obj, true
}

func jsonString(v any) string {
	if v == nil {
		return ""
	}

	return fmt.Sprint(v)
}

// EntryName returns the display name of a share link (vmess "ps" or the URI
// fragment).
func EntryName(link string) string {
	link = strings.TrimSpace(link)
	if strings.HasPrefix(strings.ToLower(link), "vmess://") {
		if obj, ok := decodeVMess(link[len("vmess://"):]); ok {
			return jsonString(obj["ps"])
		}
	}

	_, frag, ok := strings.Cut(link, "#")
	if !ok {
		return ""
	}

	if name, err := url.PathUnescape(frag); err == nil {
		return name
	}

	return frag
}

// RenameLink replaces the display name of a share link. Links whose name
// cannot be rewritten are returned unchanged.
func RenameLink(link, name string) string {
	link = strings.TrimSpace(link)
	if name == EntryName(link) {
		return link
	}

	if strings.HasPrefix(strings.ToLower(link), "vmess://") {
		obj, ok := decodeVMess(link[len("vmess://"):])
		if !ok {
			return link
		}

		obj["ps"] = name

		data, err := json.Marshal(obj)
		if err != nil {
			return link
		}

		return "vmess://" + base64.StdEncoding.EncodeToString(data)
	}

	base, _, _ := strings.Cut(link, "#")

	return base + "#" + url.PathEscape(name)
}

// renameJSONConfig sets the display name of a raw JSON server config while
// preserving every other field.
func renameJSONConfig(raw json.RawMessage, name string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(raw), &obj); err != nil {
		return raw
	}

	encoded, err := json.Marshal(name)
	if err != nil {
		return raw
	}

	obj["remark"] = encoded
	for _, k := range []string{"tag", "ps", "remarks"} {
		if _, ok := obj[k]; ok {
			obj[k] = encoded
		}
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}

	return out
}

// countryFromName extracts an ISO country code from the first flag emoji
// (pair of regional indicator symbols) in a server name.
func countryFromName(name string) string {
	runes := []rune(name)
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if isRegionalIndicator(a) && isRegionalIndicator(b) {
			return string([]rune{'A' + (a - 0x1F1E6), 'A' + (b - 0x1F1E6)})
		}
	}

	return ""
}

func isRegionalIndicator(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}
