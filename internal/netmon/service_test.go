package netmon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/subserver"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	secretUUID = "0f0f0f0f-aaaa-bbbb-cccc-121212121212"
	secretPass = "trojan-secret-pass"
	secretObfs = "salamander-secret"
)

// fakeProber answers from a per-host table and counts calls per endpoint.
type fakeProber struct {
	mu    sync.Mutex
	up    map[string]bool
	lat   map[string]int
	calls map[string]int
}

func newFakeProber() *fakeProber {
	return &fakeProber{up: map[string]bool{}, lat: map[string]int{}, calls: map[string]int{}}
}

func (p *fakeProber) set(host string, up bool, latency int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up[host], p.lat[host] = up, latency
}

func (p *fakeProber) Probe(_ context.Context, ep subserver.MonitorEndpoint) Result {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[ep.Key]++
	if p.up[ep.Host] {
		return Result{OK: true, LatencyMS: p.lat[ep.Host]}
	}
	return Result{ErrorCode: ErrCodeTimeout}
}

func (p *fakeProber) totalCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		n += c
	}
	return n
}

// fakeResolver returns configured nodes per builder, only for the enabled
// builders it is given (like the real pipeline).
type fakeResolver struct {
	mu      sync.Mutex
	nodes   map[uint][]subserver.MonitorNode
	failed  map[uint]map[uint]string // builder -> failed source -> code
	scope   map[uint][]uint
	calls   int
	lastIDs []uint
}

func (r *fakeResolver) resolve(_ context.Context, builders []database.SubscriptionBuilder) ([]subserver.MonitorBuilder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastIDs = nil
	out := make([]subserver.MonitorBuilder, 0, len(builders))
	for _, b := range builders {
		r.lastIDs = append(r.lastIDs, b.ID)
		mb := subserver.MonitorBuilder{BuilderID: b.ID, BuilderName: b.Name, ScopeSources: r.scope[b.ID], SourceErrors: map[uint]string{}}
		for _, s := range mb.ScopeSources {
			if code, failed := r.failed[b.ID][s]; failed {
				mb.SourceErrors[s] = code
				continue
			}
			mb.FetchedSources = append(mb.FetchedSources, s)
		}
		for _, n := range r.nodes[b.ID] {
			if _, failed := r.failed[b.ID][n.SourceID]; !failed {
				mb.Nodes = append(mb.Nodes, n)
			}
		}
		out = append(out, mb)
	}
	return out, nil
}

func node(t *testing.T, source uint, country, link string) subserver.MonitorNode {
	t.Helper()
	ep, ok := subserver.MonitorEndpointFromLink(link)
	require.True(t, ok, link)
	return subserver.MonitorNode{SourceID: source, Fingerprint: subserver.EntryFingerprint(link), Name: subserver.EntryName(link), Country: country, Endpoint: ep}
}

type monFixture struct {
	db       *database.Service
	svc      *Service
	prober   *fakeProber
	resolver *fakeResolver
	now      time.Time
}

func (f *monFixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func newMonFixture(t *testing.T) *monFixture {
	t.Helper()
	db, err := database.NewService(filepath.Join(t.TempDir(), "mon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, b := range []database.SubscriptionBuilder{
		{ID: 1, Name: "Основной", Enabled: true, Version: 1},
		{ID: 2, Name: "Резервный", Enabled: true, Version: 1},
		{ID: 3, Name: "Отключённый", Enabled: false, Version: 1},
	} {
		require.NoError(t, db.GetDB().Create(&b).Error)
	}
	// gorm replaces a zero bool with the column default (true) on Create.
	require.NoError(t, db.GetDB().Model(&database.SubscriptionBuilder{}).Where("id = ?", 3).Update("enabled", false).Error)

	f := &monFixture{
		db: db, prober: newFakeProber(),
		resolver: &fakeResolver{nodes: map[uint][]subserver.MonitorNode{}, failed: map[uint]map[uint]string{}, scope: map[uint][]uint{}},
		now:      time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
	}
	f.svc = New(db, f.resolver.resolve, f.prober, Config{Enabled: true, Interval: time.Minute, DownAfter: 2})
	f.svc.SetClock(func() time.Time { return f.now })
	return f
}

// Topology: builder 1 serves DE (shared + de2) and NL from source 10 and 11;
// builder 2 serves the shared DE server and a trojan in FI from source 20.
func (f *monFixture) standardTopology(t *testing.T) {
	shared := node(t, 10, "DE", "vless://"+secretUUID+"@shared.example.com:443?security=reality&sni=www.microsoft.com&pbk=PBK&type=tcp#🇩🇪 Shared")
	sharedOtherCreds := node(t, 20, "DE", "vless://99999999-aaaa-bbbb-cccc-121212121212@shared.example.com:443?security=reality&sni=www.microsoft.com&type=tcp#DE via B2")
	f.resolver.nodes[1] = []subserver.MonitorNode{
		shared,
		node(t, 10, "DE", "vless://"+secretUUID+"@de2.example.com:443?security=tls&sni=de2.example.com&type=ws#🇩🇪 DE 2"),
		node(t, 11, "NL", "hysteria2://"+secretPass+"@nl.example.com:443/?sni=nl.example.com&obfs=salamander&obfs-password="+secretObfs+"#🇳🇱 NL"),
	}
	f.resolver.nodes[2] = []subserver.MonitorNode{
		sharedOtherCreds,
		node(t, 20, "FI", "trojan://"+secretPass+"@fi.example.com:443?sni=fi.example.com#🇫🇮 FI"),
	}
	f.resolver.scope[1] = []uint{10, 11}
	f.resolver.scope[2] = []uint{20}
	for _, h := range []string{"shared.example.com", "de2.example.com", "nl.example.com", "fi.example.com"} {
		f.prober.set(h, true, 40)
	}
}

func (f *monFixture) round(t *testing.T) {
	t.Helper()
	require.NoError(t, f.svc.Round(context.Background()))
	f.advance(time.Minute)
}

func countryOf(t *testing.T, d *BuilderDetail, code string) CountryView {
	t.Helper()
	for _, c := range d.Countries {
		if c.CountryCode == code {
			return c
		}
	}
	t.Fatalf("country %q not in detail", code)
	return CountryView{}
}

func TestService_SharedEndpointCheckedOnceAndStatuses(t *testing.T) {
	t.Parallel()
	f := newMonFixture(t)
	f.standardTopology(t)
	ctx := context.Background()

	require.NoError(t, f.svc.Refresh(ctx))
	assert.Equal(t, []uint{1, 2}, f.resolver.lastIDs, "disabled builders are not resolved")
	f.round(t)
	assert.Equal(t, 4, f.prober.totalCalls(), "5 served nodes, 4 distinct endpoints: the shared one is checked once")
	for key, n := range f.prober.calls {
		assert.Equal(t, 1, n, key)
	}

	ov, err := f.svc.Overview(ctx)
	require.NoError(t, err)
	require.Len(t, ov.Builders, 3)
	byID := map[uint]BuilderSummary{}
	for _, b := range ov.Builders {
		byID[b.ID] = b
	}
	b1 := byID[1]
	assert.Equal(t, database.MonitorStatusUp, b1.Status)
	assert.Equal(t, 2, b1.CountriesTotal)
	assert.Equal(t, 2, b1.CountriesUp)
	assert.Equal(t, 3, b1.NodesTotal)
	assert.Equal(t, 3, b1.NodesUp)
	require.NotNil(t, b1.AvgLatencyMS)
	assert.Equal(t, 40, *b1.AvgLatencyMS)
	assert.Equal(t, "disabled", byID[3].Status)
	assert.Zero(t, byID[3].NodesTotal)

	// de2 goes DOWN (2 failed rounds): DE is DEGRADED, the builder too.
	f.prober.set("de2.example.com", false, 0)
	f.round(t)
	f.round(t)
	d1, err := f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, database.MonitorStatusDegraded, d1.Builder.Status)
	de := countryOf(t, d1, "DE")
	assert.Equal(t, database.MonitorStatusDegraded, de.Status)
	assert.Equal(t, 2, de.NodesTotal)
	assert.Equal(t, 1, de.NodesUp)
	assert.Equal(t, 1, de.NodesDown)
	assert.Equal(t, database.MonitorStatusUp, countryOf(t, d1, "NL").Status)
	var sharedNode NodeView
	for _, n := range de.Nodes {
		if n.Host == "shared.example.com" {
			sharedNode = n
		}
	}
	assert.Equal(t, 1, sharedNode.SharedWith, "the endpoint is also served by builder 2")
	require.NotNil(t, d1.Builder.LastTransition)
	assert.Equal(t, database.MonitorStatusDegraded, d1.Builder.LastTransition.Event)
	assert.Equal(t, "DE", d1.Builder.LastTransition.CountryCode)

	// Builder 2 is unaffected (it does not serve de2).
	d2, err := f.svc.BuilderDetail(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, database.MonitorStatusUp, d2.Builder.Status)

	// The shared server goes DOWN too: DE is DOWN in builder 1 and 2.
	f.prober.set("shared.example.com", false, 0)
	f.round(t)
	f.round(t)
	d1, err = f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, database.MonitorStatusDown, countryOf(t, d1, "DE").Status)
	assert.Equal(t, database.MonitorStatusDegraded, d1.Builder.Status, "NL still up")
	d2, err = f.svc.BuilderDetail(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, database.MonitorStatusDown, countryOf(t, d2, "DE").Status)

	// Recovery.
	f.prober.set("shared.example.com", true, 25)
	f.prober.set("de2.example.com", true, 35)
	f.round(t)
	d1, err = f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, database.MonitorStatusUp, d1.Builder.Status)
	de = countryOf(t, d1, "DE")
	require.NotNil(t, de.LatencyMS)
	assert.Equal(t, 30, *de.LatencyMS)

	// History of DE in builder 1: DEGRADED -> DOWN -> recovered.
	country := "DE"
	h, err := f.svc.History(ctx, HistoryQuery{BuilderID: 1, Country: &country, Period: "24h"})
	require.NoError(t, err)
	assert.Equal(t, database.MonitorScopeCountry, h.Scope)
	require.Len(t, h.Outages, 2)
	// Rounds run at 12:00, 12:01, …: de2 fails at 12:01 and is DOWN at 12:02
	// (DE degraded); shared fails at 12:03 and is DOWN at 12:04 (DE down);
	// both answer at 12:05.
	down, degraded := h.Outages[0], h.Outages[1] // newest first
	assert.Equal(t, database.MonitorOutageDown, down.Kind)
	assert.Equal(t, database.MonitorEndRecovered, down.EndReason)
	assert.Equal(t, int64(60), down.DurationSeconds, "12:04 -> 12:05")
	assert.Equal(t, database.MonitorOutageDegraded, degraded.Kind)
	assert.Equal(t, database.MonitorEndChanged, degraded.EndReason)
	assert.Equal(t, int64(120), degraded.DurationSeconds, "12:02 -> 12:04")
	assert.Equal(t, "timeout", down.ErrorCode)
	assert.Equal(t, int64(60), h.Summary.DownSeconds)
	assert.Equal(t, int64(120), h.Summary.DegradedSeconds)
	require.NotNil(t, h.Summary.Availability)
	assert.InDelta(t, 100-60.0*100/86400, *h.Summary.Availability, 0.0001)
	require.NotEmpty(t, h.Latency)
	assert.Positive(t, h.Summary.Checks)
	assert.Positive(t, h.Summary.Failures)

	// Target history of de2: one outage opened at its first failure.
	h, err = f.svc.History(ctx, HistoryQuery{TargetID: nodeTarget(t, d1, "de2.example.com"), Period: "7d"})
	require.NoError(t, err)
	require.Len(t, h.Outages, 1)
	assert.Equal(t, int64(4*60), h.Outages[0].DurationSeconds)
	assert.False(t, h.Outages[0].Ongoing)
	assert.Equal(t, int64(3600), h.StepSeconds)

	// Builder history: every country outage of the builder.
	h, err = f.svc.History(ctx, HistoryQuery{BuilderID: 1, Period: "30d"})
	require.NoError(t, err)
	assert.Equal(t, "builder", h.Scope)
	assert.Len(t, h.Outages, 2)
	assert.Nil(t, h.Summary.Availability)
}

func endpointKeyOf(t *testing.T, link string) string {
	t.Helper()
	ep, ok := subserver.MonitorEndpointFromLink(link)
	require.True(t, ok)
	return ep.Key
}

func nodeTarget(t *testing.T, d *BuilderDetail, host string) uint {
	t.Helper()
	for _, c := range d.Countries {
		for _, n := range c.Nodes {
			if n.Host == host {
				return n.TargetID
			}
		}
	}
	t.Fatalf("host %s not found", host)
	return 0
}

func TestService_CatalogueChangeFailedSourceAndHistorySurvive(t *testing.T) {
	t.Parallel()
	f := newMonFixture(t)
	f.standardTopology(t)
	ctx := context.Background()
	require.NoError(t, f.svc.Refresh(ctx))
	f.prober.set("nl.example.com", false, 0)
	f.round(t)
	f.round(t)
	d1, err := f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	nlTarget := nodeTarget(t, d1, "nl.example.com")

	// Source 11 fails to fetch: NL is unknown-not-gone and keeps being checked.
	f.resolver.failed[1] = map[uint]string{11: "timeout"}
	require.NoError(t, f.svc.Refresh(ctx))
	nlCalls := f.prober.calls[endpointKeyOf(t, "hysteria2://x@nl.example.com:443/?sni=nl.example.com&obfs=salamander&obfs-password=k#x")]
	f.round(t)
	assert.Equal(t, nlCalls+1, f.prober.calls[endpointKeyOf(t, "hysteria2://x@nl.example.com:443/?sni=nl.example.com&obfs=salamander&obfs-password=k#x")],
		"a server of a source that failed to fetch keeps being checked")
	d1, err = f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, database.MonitorStatusDown, countryOf(t, d1, "NL").Status)
	assert.Equal(t, 1, d1.Builder.SourceErrors)

	// The catalogue drops NL for real (source fetched, server gone).
	f.resolver.failed[1] = nil
	f.resolver.nodes[1] = f.resolver.nodes[1][:2]
	require.NoError(t, f.svc.Refresh(ctx))
	d1, err = f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	for _, c := range d1.Countries {
		assert.NotEqual(t, "NL", c.CountryCode, "a server gone from the catalogue is not shown as current")
	}
	calls := f.prober.totalCalls()
	f.round(t)
	assert.Equal(t, calls+3, f.prober.totalCalls(), "the removed endpoint is no longer checked")

	// Its history is kept.
	h, err := f.svc.History(ctx, HistoryQuery{TargetID: nlTarget, Period: "24h"})
	require.NoError(t, err)
	require.Len(t, h.Outages, 1)
	assert.Equal(t, database.MonitorEndRemoved, h.Outages[0].EndReason)
	nl := "NL"
	h, err = f.svc.History(ctx, HistoryQuery{BuilderID: 1, Country: &nl, Period: "24h"})
	require.NoError(t, err)
	require.Len(t, h.Outages, 1)
	assert.NotEmpty(t, h.Latency, "latency history of a retired server stays")
}

func TestService_ConfigChangedAndQueries(t *testing.T) {
	t.Parallel()
	f := newMonFixture(t)
	f.standardTopology(t)
	ctx := context.Background()

	changed, err := f.svc.ConfigChanged(ctx)
	require.NoError(t, err)
	assert.True(t, changed, "never resolved")
	require.NoError(t, f.svc.Refresh(ctx))
	changed, err = f.svc.ConfigChanged(ctx)
	require.NoError(t, err)
	assert.False(t, changed)

	require.NoError(t, f.db.GetDB().Model(&database.SubscriptionBuilder{}).Where("id = ?", 2).Update("version", 2).Error)
	changed, err = f.svc.ConfigChanged(ctx)
	require.NoError(t, err)
	assert.True(t, changed, "builder edited")

	_, err = f.svc.History(ctx, HistoryQuery{BuilderID: 1, Period: "1y"})
	assert.ErrorIs(t, err, ErrInvalidQuery)
	_, err = f.svc.History(ctx, HistoryQuery{Period: "24h"})
	assert.ErrorIs(t, err, ErrInvalidQuery)
	de := "DE"
	_, err = f.svc.History(ctx, HistoryQuery{Country: &de, Period: "24h"})
	assert.ErrorIs(t, err, ErrInvalidQuery)
	_, err = f.svc.History(ctx, HistoryQuery{BuilderID: 999, Period: "24h"})
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.History(ctx, HistoryQuery{TargetID: 999, Period: "24h"})
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.BuilderDetail(ctx, 999)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestService_RunLoopRefreshesAndChecks(t *testing.T) {
	t.Parallel()
	f := newMonFixture(t)
	f.standardTopology(t)
	f.svc.SetClock(time.Now)
	f.svc.cfg.Interval = 20 * time.Millisecond
	f.svc.cfg.Refresh = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.Run(ctx, 0); close(done) }()
	require.Eventually(t, func() bool { return f.prober.totalCalls() >= 8 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	<-done
	f.resolver.mu.Lock()
	defer f.resolver.mu.Unlock()
	assert.Equal(t, 1, f.resolver.calls, "no re-resolution without a configuration change")
}

// TestService_NoCredentialsInViewsStorageOrLogs is serial: it swaps the
// global logger to capture every line the monitor writes.
func TestService_NoCredentialsInViewsStorageOrLogs(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	previous := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() { logger.Log = previous })

	f := newMonFixture(t)
	f.standardTopology(t)
	f.resolver.failed[1] = map[uint]string{11: "http_502"}
	ctx := context.Background()
	require.NoError(t, f.svc.Refresh(ctx))
	f.prober.set("de2.example.com", false, 0)
	for i := 0; i < 3; i++ {
		f.round(t)
	}

	var blobs []string
	ov, err := f.svc.Overview(ctx)
	require.NoError(t, err)
	d1, err := f.svc.BuilderDetail(ctx, 1)
	require.NoError(t, err)
	h, err := f.svc.History(ctx, HistoryQuery{BuilderID: 1, Period: "24h"})
	require.NoError(t, err)
	for _, v := range []any{ov, d1, h} {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		blobs = append(blobs, string(b))
	}
	for _, table := range []string{"monitor_targets", "monitor_bindings", "monitor_outages", "monitor_country_states", "monitor_samples"} {
		var rows []map[string]any
		require.NoError(t, f.db.GetDB().Raw("SELECT * FROM "+table).Scan(&rows).Error)
		blobs = append(blobs, fmt.Sprint(rows))
	}
	for _, entry := range logs.All() {
		blobs = append(blobs, entry.Message, fmt.Sprint(entry.ContextMap()))
	}
	all := strings.Join(blobs, "\n")
	require.Contains(t, all, "de2.example.com", "sanity: the views were captured")
	for _, secret := range []string{secretUUID, secretPass, secretObfs, "PBK"} {
		assert.NotContains(t, all, secret)
	}
}
