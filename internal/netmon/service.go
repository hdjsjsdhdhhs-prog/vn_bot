package netmon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/subserver"

	"go.uber.org/zap"
)

// Defaults of the monitor configuration (config.NetMon* override them).
const (
	DefaultInterval    = 60 * time.Second
	DefaultTimeout     = 5 * time.Second
	DefaultRefresh     = 10 * time.Minute
	DefaultConcurrency = 8
	// SampleRetention keeps 5-minute samples long enough for the 30-day view.
	SampleRetention = 35 * 24 * time.Hour
)

// Config tunes the monitor.
type Config struct {
	Enabled     bool          // background worker runs (NETMON_ENABLED)
	Interval    time.Duration // between check rounds
	Timeout     time.Duration // per probe
	Refresh     time.Duration // between builder re-resolutions
	Concurrency int           // probes in flight
	DownAfter   int           // consecutive failures before DOWN
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Refresh <= 0 {
		c.Refresh = DefaultRefresh
	}
	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}
	if c.DownAfter <= 0 {
		c.DownAfter = database.DefaultMonitorDownAfter
	}
	return c
}

// Repository is the storage seam; *database.Service implements it.
type Repository interface {
	ListMonitorBuilders(ctx context.Context) ([]database.SubscriptionBuilder, error)
	SyncMonitorBindings(ctx context.Context, now time.Time, snapshots []database.MonitorBuilderSnapshot) error
	ListActiveMonitorTargets(ctx context.Context) ([]database.MonitorTarget, error)
	RecordMonitorChecks(ctx context.Context, now time.Time, results []database.MonitorCheckResult, downAfter int) error
	PruneMonitorSamples(ctx context.Context, cutoff time.Time) (int64, error)
	ListMonitorBindings(ctx context.Context, builderID uint, activeOnly bool) ([]database.MonitorBindingRow, error)
	ListMonitorCountryStates(ctx context.Context, builderID uint) ([]database.MonitorCountryState, error)
	ListMonitorOutages(ctx context.Context, f database.MonitorOutageFilter) ([]database.MonitorOutage, error)
	LatestMonitorCountryOutages(ctx context.Context) ([]database.MonitorOutage, error)
	MonitorLatencySeries(ctx context.Context, targetIDs []uint, from time.Time, stepSeconds int64) ([]database.MonitorLatencyPoint, error)
	ListMonitorTargets(ctx context.Context, ids []uint) ([]database.MonitorTarget, error)
}

var _ Repository = (*database.Service)(nil)

// ResolveFunc resolves the current servers of enabled builders
// (subserver.ResolveMonitorBuilders in production).
type ResolveFunc func(ctx context.Context, builders []database.SubscriptionBuilder) ([]subserver.MonitorBuilder, error)

// ErrNotFound is returned for an unknown builder.
var ErrNotFound = errors.New("monitor subject not found")

// ErrInvalidQuery is returned for an invalid history query.
var ErrInvalidQuery = errors.New("invalid monitor query")

// Service runs check rounds and serves the admin views.
type Service struct {
	repo    Repository
	resolve ResolveFunc
	prober  Prober
	cfg     Config
	now     func() time.Time

	mu sync.Mutex
	// endpoints by key from the last resolution. They carry the in-memory
	// Hysteria2 obfuscation key, which is never persisted; a target is only
	// probed after a resolution in this process.
	endpoints     map[string]subserver.MonitorEndpoint
	signatures    map[uint]string
	builderInfo   map[uint]builderRun
	lastRefreshAt *time.Time
	lastRoundAt   *time.Time
	running       bool
}

type builderRun struct {
	sourceErrors int
	unresolved   int
}

// New wires a monitor service.
func New(repo Repository, resolve ResolveFunc, prober Prober, cfg Config) *Service {
	return &Service{
		repo: repo, resolve: resolve, prober: prober, cfg: cfg.withDefaults(), now: time.Now,
		endpoints: map[string]subserver.MonitorEndpoint{}, signatures: map[uint]string{}, builderInfo: map[uint]builderRun{},
	}
}

// Config returns the effective configuration.
func (s *Service) Config() Config { return s.cfg }

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

func (s *Service) nowUTC() time.Time { return s.now().UTC() }

// Refresh re-resolves every enabled builder with the /sub pipeline and stores
// the bindings. Disabled builders keep only history.
func (s *Service) Refresh(ctx context.Context) error {
	builders, err := s.repo.ListMonitorBuilders(ctx)
	if err != nil {
		return err
	}
	enabled := make([]database.SubscriptionBuilder, 0, len(builders))
	signatures := make(map[uint]string, len(builders))
	for i := range builders {
		if builders[i].Enabled {
			enabled = append(enabled, builders[i])
			signatures[builders[i].ID] = subserver.BuilderConfigSignature(&builders[i])
		}
	}

	resolved, err := s.resolve(ctx, enabled)
	if err != nil {
		return fmt.Errorf("resolve monitor builders: %w", err)
	}

	endpoints := make(map[string]subserver.MonitorEndpoint)
	info := make(map[uint]builderRun, len(resolved))
	snapshots := make([]database.MonitorBuilderSnapshot, 0, len(resolved))
	for _, mb := range resolved {
		snap := database.MonitorBuilderSnapshot{
			BuilderID: mb.BuilderID, BuilderName: mb.BuilderName,
			ScopeSources: mb.ScopeSources, FetchedSources: mb.FetchedSources,
		}
		for _, n := range mb.Nodes {
			ep := n.Endpoint
			endpoints[ep.Key] = ep
			snap.Nodes = append(snap.Nodes, database.MonitorNodeSnapshot{
				SourceID: n.SourceID, Fingerprint: n.Fingerprint, Name: n.Name, Country: n.Country,
				Endpoint: database.MonitorEndpointSnapshot{
					Key: ep.Key, Protocol: ep.Protocol, Host: ep.Host, Port: ep.Port,
					Security: ep.Security, Transport: ep.Transport, SNI: ep.SNI, Probe: ep.Probe,
				},
			})
		}
		snapshots = append(snapshots, snap)
		info[mb.BuilderID] = builderRun{sourceErrors: len(mb.SourceErrors), unresolved: mb.Unresolved}
		if len(mb.SourceErrors) > 0 {
			logger.Warn("Network monitor: builder sources unavailable",
				zap.Uint("builder_id", mb.BuilderID), zap.Int("failed_sources", len(mb.SourceErrors)))
		}
	}

	now := s.nowUTC()
	if err := s.repo.SyncMonitorBindings(ctx, now, snapshots); err != nil {
		return err
	}

	s.mu.Lock()
	// Keep endpoints of scope sources that failed to fetch this time: their
	// bindings stay active, so their targets keep being checked.
	for key, ep := range s.endpoints {
		if _, ok := endpoints[key]; !ok {
			endpoints[key] = ep
		}
	}
	s.endpoints = endpoints
	s.signatures = signatures
	s.builderInfo = info
	s.lastRefreshAt = &now
	s.mu.Unlock()
	return nil
}

// ConfigChanged reports whether any builder or source configuration changed
// since the last Refresh (same digest as the /sub response cache).
func (s *Service) ConfigChanged(ctx context.Context) (bool, error) {
	builders, err := s.repo.ListMonitorBuilders(ctx)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastRefreshAt == nil {
		return true, nil
	}
	enabled := 0
	for i := range builders {
		if !builders[i].Enabled {
			continue
		}
		enabled++
		if s.signatures[builders[i].ID] != subserver.BuilderConfigSignature(&builders[i]) {
			return true, nil
		}
	}
	return enabled != len(s.signatures), nil
}

// Round checks every active target once (each endpoint once, however many
// builders serve it) and records the results.
func (s *Service) Round(ctx context.Context) error {
	targets, err := s.repo.ListActiveMonitorTargets(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	type job struct {
		id uint
		ep subserver.MonitorEndpoint
	}
	jobs := make([]job, 0, len(targets))
	for _, t := range targets {
		if t.Probe == subserver.ProbeUnsupported {
			continue
		}
		ep, ok := s.endpoints[t.EndpointKey]
		if !ok {
			continue // not resolved in this process yet
		}
		jobs = append(jobs, job{id: t.ID, ep: ep})
	}
	s.mu.Unlock()

	results := make([]database.MonitorCheckResult, len(jobs))
	sem := make(chan struct{}, s.cfg.Concurrency)
	var wg sync.WaitGroup
	for i := range jobs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			r := s.prober.Probe(ctx, jobs[i].ep)
			results[i] = database.MonitorCheckResult{
				TargetID: jobs[i].id, OK: r.OK, LatencyMS: r.LatencyMS, ErrorCode: r.ErrorCode, CheckedAt: s.nowUTC(),
			}
		}(i)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}

	now := s.nowUTC()
	if err := s.repo.RecordMonitorChecks(ctx, now, results, s.cfg.DownAfter); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastRoundAt = &now
	s.mu.Unlock()
	return nil
}

// Prune drops samples beyond SampleRetention.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	return s.repo.PruneMonitorSamples(ctx, s.nowUTC().Add(-SampleRetention))
}

// Run is the background loop: resolve, then a check round every Interval;
// re-resolve every Refresh or as soon as a builder/source configuration
// changes; prune samples every 6 hours. Failures are logged (best-effort)
// and never stop the loop. Blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context, initialDelay time.Duration) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	logger.Info("Network monitor started",
		zap.Duration("interval", s.cfg.Interval), zap.Duration("refresh", s.cfg.Refresh),
		zap.Duration("timeout", s.cfg.Timeout), zap.Int("concurrency", s.cfg.Concurrency))

	select {
	case <-time.After(initialDelay):
	case <-ctx.Done():
		return
	}

	s.refreshBestEffort(ctx)
	s.roundBestEffort(ctx)

	round := time.NewTicker(s.cfg.Interval)
	defer round.Stop()
	refresh := time.NewTicker(s.cfg.Refresh)
	defer refresh.Stop()
	prune := time.NewTicker(6 * time.Hour)
	defer prune.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Network monitor stopped")
			return
		case <-refresh.C:
			s.refreshBestEffort(ctx)
		case <-round.C:
			if changed, err := s.ConfigChanged(ctx); err == nil && changed {
				s.refreshBestEffort(ctx)
			}
			s.roundBestEffort(ctx)
		case <-prune.C:
			if n, err := s.Prune(ctx); err != nil {
				logger.Warn("Network monitor: sample pruning failed", zap.Error(err))
			} else if n > 0 {
				logger.Debug("Network monitor: samples pruned", zap.Int64("rows", n))
			}
		}
	}
}

func (s *Service) refreshBestEffort(ctx context.Context) {
	if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
		logger.Warn("Network monitor: resolution failed", zap.Error(err))
	}
}

func (s *Service) roundBestEffort(ctx context.Context) {
	if err := s.Round(ctx); err != nil && ctx.Err() == nil {
		logger.Warn("Network monitor: check round failed", zap.Error(err))
	}
}

// sortedCountries orders countries by code with "" (no country) last.
func sortedCountries(keys []string) []string {
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "") != (keys[j] == "") {
			return keys[j] == ""
		}
		return keys[i] < keys[j]
	})
	return keys
}
