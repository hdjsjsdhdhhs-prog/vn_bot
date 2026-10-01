package netmon

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// Admin views. Every field is credential-free: endpoints are protocol, host,
// port and transport; names are the display names the builder serves.

// Overview is GET /admin/api/monitoring.
type Overview struct {
	Enabled         bool             `json:"enabled"`
	GeneratedAt     time.Time        `json:"generated_at"`
	IntervalSeconds int              `json:"interval_seconds"`
	DownAfter       int              `json:"down_after"`
	LastRoundAt     *time.Time       `json:"last_round_at"`
	LastRefreshAt   *time.Time       `json:"last_refresh_at"`
	Builders        []BuilderSummary `json:"builders"`
}

// BuilderSummary is one builder row of the overview.
type BuilderSummary struct {
	ID                uint        `json:"id"`
	Name              string      `json:"name"`
	Enabled           bool        `json:"enabled"`
	Status            string      `json:"status"`
	CountriesTotal    int         `json:"countries_total"`
	CountriesUp       int         `json:"countries_up"`
	CountriesDegraded int         `json:"countries_degraded"`
	CountriesDown     int         `json:"countries_down"`
	NodesTotal        int         `json:"nodes_total"`
	NodesUp           int         `json:"nodes_up"`
	NodesDown         int         `json:"nodes_down"`
	NodesUnknown      int         `json:"nodes_unknown"`
	AvgLatencyMS      *int        `json:"avg_latency_ms"`
	LastCheckedAt     *time.Time  `json:"last_checked_at"`
	LastTransition    *Transition `json:"last_transition"`
	SourceErrors      int         `json:"source_errors"`
	Unresolved        int         `json:"unresolved"`
}

// Transition is the latest country state change of a builder.
type Transition struct {
	CountryCode string `json:"country_code"`
	// Event: down | degraded (outage opened) or up (outage closed as recovered)
	// or removed (the country is no longer served).
	Event string    `json:"event"`
	At    time.Time `json:"at"`
}

// BuilderDetail is GET /admin/api/monitoring/builders/{id}.
type BuilderDetail struct {
	GeneratedAt     time.Time      `json:"generated_at"`
	IntervalSeconds int            `json:"interval_seconds"`
	LastRoundAt     *time.Time     `json:"last_round_at"`
	Builder         BuilderSummary `json:"builder"`
	Countries       []CountryView  `json:"countries"`
}

// CountryView is one country of a builder.
type CountryView struct {
	CountryCode   string     `json:"country_code"`
	Status        string     `json:"status"`
	StatusSince   *time.Time `json:"status_since"`
	LatencyMS     *int       `json:"latency_ms"`
	NodesTotal    int        `json:"nodes_total"`
	NodesUp       int        `json:"nodes_up"`
	NodesDown     int        `json:"nodes_down"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastUpAt      *time.Time `json:"last_up_at"`
	LastDownAt    *time.Time `json:"last_down_at"`
	Nodes         []NodeView `json:"nodes"`
}

// NodeView is one server of a builder country.
type NodeView struct {
	TargetID      uint       `json:"target_id"`
	Name          string     `json:"name"`
	SourceID      uint       `json:"source_id"`
	SourceName    string     `json:"source_name"`
	Protocol      string     `json:"protocol"`
	Host          string     `json:"host"`
	Port          int        `json:"port"`
	Security      string     `json:"security"`
	Transport     string     `json:"transport"`
	Probe         string     `json:"probe"`
	Status        string     `json:"status"`
	StatusSince   *time.Time `json:"status_since"`
	LatencyMS     *int       `json:"latency_ms"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastUpAt      *time.Time `json:"last_up_at"`
	LastDownAt    *time.Time `json:"last_down_at"`
	LastError     string     `json:"last_error"`
	// SharedWith counts other builders serving the same endpoint (checked once).
	SharedWith int `json:"shared_with"`
}

type builderMeta struct {
	name    string
	enabled bool
	sources map[uint]string
}

func (s *Service) builderMetas(ctx context.Context) (map[uint]builderMeta, []uint, error) {
	builders, err := s.repo.ListMonitorBuilders(ctx)
	if err != nil {
		return nil, nil, err
	}
	metas := make(map[uint]builderMeta, len(builders))
	order := make([]uint, 0, len(builders))
	for _, b := range builders {
		m := builderMeta{name: b.Name, enabled: b.Enabled, sources: map[uint]string{}}
		for _, link := range b.Sources {
			if link.Source != nil {
				m.sources[link.SourceID] = link.Source.Name
			}
		}
		metas[b.ID] = m
		order = append(order, b.ID)
	}
	return metas, order, nil
}

// Overview summarizes every builder.
func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	metas, order, err := s.builderMetas(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := s.repo.ListMonitorBindings(ctx, 0, true)
	if err != nil {
		return nil, err
	}
	states, err := s.repo.ListMonitorCountryStates(ctx, 0)
	if err != nil {
		return nil, err
	}
	latest, err := s.repo.LatestMonitorCountryOutages(ctx)
	if err != nil {
		return nil, err
	}
	byBuilder := make(map[uint][]database.MonitorBindingRow)
	for _, b := range bindings {
		byBuilder[b.BuilderID] = append(byBuilder[b.BuilderID], b)
	}
	statesOf := make(map[uint][]database.MonitorCountryState)
	for _, st := range states {
		if st.Active {
			statesOf[st.BuilderID] = append(statesOf[st.BuilderID], st)
		}
	}
	lastOf := make(map[uint]database.MonitorOutage)
	for _, o := range latest {
		if o.BuilderID != nil {
			lastOf[*o.BuilderID] = o
		}
	}

	s.mu.Lock()
	info := s.builderInfo
	out := &Overview{
		Enabled:     s.cfg.Enabled,
		GeneratedAt: s.nowUTC(), IntervalSeconds: int(s.cfg.Interval / time.Second), DownAfter: s.cfg.DownAfter,
		LastRoundAt: s.lastRoundAt, LastRefreshAt: s.lastRefreshAt,
	}
	s.mu.Unlock()

	for _, id := range order {
		meta := metas[id]
		sum := summarize(id, meta, byBuilder[id], statesOf[id])
		if o, ok := lastOf[id]; ok {
			sum.LastTransition = transitionOf(o)
		}
		sum.SourceErrors, sum.Unresolved = info[id].sourceErrors, info[id].unresolved
		out.Builders = append(out.Builders, sum)
	}
	if out.Builders == nil {
		out.Builders = []BuilderSummary{}
	}
	return out, nil
}

func transitionOf(o database.MonitorOutage) *Transition {
	if o.EndedAt != nil {
		event := database.MonitorStatusUp
		if o.EndReason == database.MonitorEndRemoved {
			event = database.MonitorEndRemoved
		} else if o.EndReason == database.MonitorEndChanged {
			return &Transition{CountryCode: o.CountryCode, Event: o.Kind, At: o.StartedAt}
		}
		return &Transition{CountryCode: o.CountryCode, Event: event, At: *o.EndedAt}
	}
	return &Transition{CountryCode: o.CountryCode, Event: o.Kind, At: o.StartedAt}
}

func summarize(id uint, meta builderMeta, bindings []database.MonitorBindingRow, states []database.MonitorCountryState) BuilderSummary {
	sum := BuilderSummary{ID: id, Name: meta.name, Enabled: meta.enabled}
	var latencySum, latencyN int
	for _, b := range bindings {
		sum.NodesTotal++
		switch b.Target.Status {
		case database.MonitorStatusUp:
			sum.NodesUp++
			if b.Target.LastLatencyMS != nil {
				latencySum += *b.Target.LastLatencyMS
				latencyN++
			}
		case database.MonitorStatusDown:
			sum.NodesDown++
		default:
			sum.NodesUnknown++
		}
		sum.LastCheckedAt = later(sum.LastCheckedAt, b.Target.LastCheckedAt)
	}
	if latencyN > 0 {
		avg := latencySum / latencyN
		sum.AvgLatencyMS = &avg
	}
	statuses := make([]string, 0, len(states))
	for _, st := range states {
		sum.CountriesTotal++
		switch st.Status {
		case database.MonitorStatusUp:
			sum.CountriesUp++
		case database.MonitorStatusDegraded:
			sum.CountriesDegraded++
		case database.MonitorStatusDown:
			sum.CountriesDown++
		}
		statuses = append(statuses, st.Status)
	}
	sum.Status = database.MonitorAggregateStatus(statuses)
	if !meta.enabled {
		sum.Status = "disabled"
	}
	return sum
}

// BuilderDetail returns the countries and servers of one builder.
func (s *Service) BuilderDetail(ctx context.Context, id uint) (*BuilderDetail, error) {
	metas, _, err := s.builderMetas(ctx)
	if err != nil {
		return nil, err
	}
	meta, ok := metas[id]
	if !ok {
		return nil, ErrNotFound
	}
	bindings, err := s.repo.ListMonitorBindings(ctx, id, true)
	if err != nil {
		return nil, err
	}
	all, err := s.repo.ListMonitorBindings(ctx, 0, true)
	if err != nil {
		return nil, err
	}
	states, err := s.repo.ListMonitorCountryStates(ctx, id)
	if err != nil {
		return nil, err
	}
	latest, err := s.repo.LatestMonitorCountryOutages(ctx)
	if err != nil {
		return nil, err
	}

	buildersOfTarget := make(map[uint]map[uint]bool)
	for _, b := range all {
		if buildersOfTarget[b.TargetID] == nil {
			buildersOfTarget[b.TargetID] = map[uint]bool{}
		}
		buildersOfTarget[b.TargetID][b.BuilderID] = true
	}

	active := make([]database.MonitorCountryState, 0, len(states))
	stateOf := make(map[string]database.MonitorCountryState)
	for _, st := range states {
		if st.Active {
			active = append(active, st)
			stateOf[st.CountryCode] = st
		}
	}
	nodesOf := make(map[string][]NodeView)
	for _, b := range bindings {
		t := b.Target
		nodesOf[b.CountryCode] = append(nodesOf[b.CountryCode], NodeView{
			TargetID: t.ID, Name: b.NodeName, SourceID: b.SourceID, SourceName: meta.sources[b.SourceID],
			Protocol: t.Protocol, Host: t.Host, Port: t.Port, Security: t.Security, Transport: t.Transport,
			Probe: t.Probe, Status: t.Status, StatusSince: t.StatusSince, LatencyMS: t.LastLatencyMS,
			LastCheckedAt: t.LastCheckedAt, LastUpAt: t.LastUpAt, LastDownAt: t.LastDownAt, LastError: t.LastError,
			SharedWith: len(buildersOfTarget[t.ID]) - 1,
		})
	}

	s.mu.Lock()
	info := s.builderInfo[id]
	out := &BuilderDetail{GeneratedAt: s.nowUTC(), IntervalSeconds: int(s.cfg.Interval / time.Second), LastRoundAt: s.lastRoundAt}
	s.mu.Unlock()

	out.Builder = summarize(id, meta, bindings, active)
	out.Builder.SourceErrors, out.Builder.Unresolved = info.sourceErrors, info.unresolved
	for _, o := range latest {
		if o.BuilderID != nil && *o.BuilderID == id {
			out.Builder.LastTransition = transitionOf(o)
		}
	}

	keys := make([]string, 0, len(nodesOf))
	for k := range nodesOf {
		keys = append(keys, k)
	}
	for _, code := range sortedCountries(keys) {
		nodes := nodesOf[code]
		st := stateOf[code]
		cv := CountryView{
			CountryCode: code, Status: st.Status, StatusSince: st.StatusSince,
			LastCheckedAt: st.LastCheckedAt, LastUpAt: st.LastUpAt, LastDownAt: st.LastDownAt, Nodes: nodes,
		}
		if cv.Status == "" {
			cv.Status = database.MonitorStatusUnknown
		}
		var latencySum, latencyN int
		for _, n := range nodes {
			cv.NodesTotal++
			switch n.Status {
			case database.MonitorStatusUp:
				cv.NodesUp++
				if n.LatencyMS != nil {
					latencySum += *n.LatencyMS
					latencyN++
				}
			case database.MonitorStatusDown:
				cv.NodesDown++
			}
		}
		if latencyN > 0 {
			avg := latencySum / latencyN
			cv.LatencyMS = &avg
		}
		out.Countries = append(out.Countries, cv)
	}
	if out.Countries == nil {
		out.Countries = []CountryView{}
	}
	return out, nil
}

// Periods of the history view with their latency step.
var periods = map[string]struct {
	span time.Duration
	step int64
}{
	"24h": {24 * time.Hour, 300},
	"7d":  {7 * 24 * time.Hour, 3600},
	"30d": {30 * 24 * time.Hour, 4 * 3600},
}

// HistoryQuery selects the subject of GET /admin/api/monitoring/history:
// a target (TargetID), a builder country (BuilderID + Country) or a whole
// builder (BuilderID).
type HistoryQuery struct {
	BuilderID uint
	Country   *string
	TargetID  uint
	Period    string
}

// History is outages and latency of one subject over a period.
type History struct {
	Scope       string         `json:"scope"` // builder | country | target
	Period      string         `json:"period"`
	From        time.Time      `json:"from"`
	To          time.Time      `json:"to"`
	StepSeconds int64          `json:"step_seconds"`
	Summary     HistorySummary `json:"summary"`
	Outages     []OutageView   `json:"outages"`
	Latency     []LatencyPoint `json:"latency"`
}

// HistorySummary aggregates the outages of the period (clipped to it).
type HistorySummary struct {
	Outages         int      `json:"outages"`
	DownSeconds     int64    `json:"down_seconds"`
	DegradedSeconds int64    `json:"degraded_seconds"`
	Availability    *float64 `json:"availability"` // percent of the period not DOWN (country/target)
	Checks          int      `json:"checks"`
	Failures        int      `json:"failures"`
}

// OutageView is one outage.
type OutageView struct {
	ID              uint       `json:"id"`
	Scope           string     `json:"scope"`
	Kind            string     `json:"kind"`
	CountryCode     string     `json:"country_code"`
	TargetID        *uint      `json:"target_id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSeconds int64      `json:"duration_seconds"`
	Ongoing         bool       `json:"ongoing"`
	EndReason       string     `json:"end_reason"`
	ErrorCode       string     `json:"error_code"`
}

// LatencyPoint is one step of the latency series.
type LatencyPoint struct {
	At       time.Time `json:"at"`
	Checks   int       `json:"checks"`
	Failures int       `json:"failures"`
	AvgMS    *int      `json:"avg_ms"`
	MinMS    *int      `json:"min_ms"`
	MaxMS    *int      `json:"max_ms"`
}

// History returns the outages and latency of a subject.
func (s *Service) History(ctx context.Context, q HistoryQuery) (*History, error) {
	period, ok := periods[q.Period]
	if !ok {
		return nil, fmt.Errorf("%w: period", ErrInvalidQuery)
	}
	if q.TargetID == 0 && q.BuilderID == 0 {
		return nil, fmt.Errorf("%w: subject", ErrInvalidQuery)
	}
	if q.Country != nil && q.BuilderID == 0 {
		return nil, fmt.Errorf("%w: country needs builder", ErrInvalidQuery)
	}
	to := s.nowUTC()
	from := to.Add(-period.span)
	h := &History{Period: q.Period, From: from, To: to, StepSeconds: period.step, Outages: []OutageView{}, Latency: []LatencyPoint{}}

	var targetIDs []uint
	filter := database.MonitorOutageFilter{From: from, To: to, Limit: 500}
	switch {
	case q.TargetID != 0:
		h.Scope = database.MonitorScopeTarget
		targets, err := s.repo.ListMonitorTargets(ctx, []uint{q.TargetID})
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			return nil, ErrNotFound
		}
		targetIDs = []uint{q.TargetID}
		filter.Scope, filter.TargetIDs = database.MonitorScopeTarget, targetIDs
	default:
		metas, _, err := s.builderMetas(ctx)
		if err != nil {
			return nil, err
		}
		if _, ok := metas[q.BuilderID]; !ok {
			return nil, ErrNotFound
		}
		bindings, err := s.repo.ListMonitorBindings(ctx, q.BuilderID, false) // history includes retired servers
		if err != nil {
			return nil, err
		}
		seen := map[uint]bool{}
		for _, b := range bindings {
			if q.Country != nil && b.CountryCode != *q.Country {
				continue
			}
			if !seen[b.TargetID] {
				seen[b.TargetID] = true
				targetIDs = append(targetIDs, b.TargetID)
			}
		}
		h.Scope = "builder"
		if q.Country != nil {
			h.Scope = database.MonitorScopeCountry
		}
		filter.Scope, filter.BuilderID, filter.Country = database.MonitorScopeCountry, q.BuilderID, q.Country
	}

	outages, err := s.repo.ListMonitorOutages(ctx, filter)
	if err != nil {
		return nil, err
	}
	for _, o := range outages {
		end := to
		if o.EndedAt != nil {
			end = *o.EndedAt
		}
		h.Outages = append(h.Outages, OutageView{
			ID: o.ID, Scope: o.Scope, Kind: o.Kind, CountryCode: o.CountryCode, TargetID: o.TargetID,
			StartedAt: o.StartedAt, EndedAt: o.EndedAt, DurationSeconds: int64(end.Sub(o.StartedAt) / time.Second),
			Ongoing: o.EndedAt == nil, EndReason: o.EndReason, ErrorCode: o.ErrorCode,
		})
		clipped := int64(minTime(end, to).Sub(maxTime(o.StartedAt, from)) / time.Second)
		if clipped < 0 {
			clipped = 0
		}
		if o.Kind == database.MonitorOutageDown {
			h.Summary.DownSeconds += clipped
		} else {
			h.Summary.DegradedSeconds += clipped
		}
	}
	h.Summary.Outages = len(h.Outages)
	if h.Scope != "builder" {
		span := int64(period.span / time.Second)
		down := h.Summary.DownSeconds
		if down > span {
			down = span
		}
		availability := float64(span-down) * 100 / float64(span)
		h.Summary.Availability = &availability
	}

	points, err := s.repo.MonitorLatencySeries(ctx, targetIDs, from, period.step)
	if err != nil {
		return nil, err
	}
	for _, p := range points {
		lp := LatencyPoint{At: time.Unix(p.BucketStart, 0).UTC(), Checks: p.Checks, Failures: p.Failures, MinMS: p.LatencyMin, MaxMS: p.LatencyMax}
		if p.LatencyCount > 0 {
			avg := int(p.LatencySum / int64(p.LatencyCount))
			lp.AvgMS = &avg
		}
		h.Summary.Checks += p.Checks
		h.Summary.Failures += p.Failures
		h.Latency = append(h.Latency, lp)
	}
	return h, nil
}

func later(a, b *time.Time) *time.Time {
	switch {
	case b == nil:
		return a
	case a == nil || b.After(*a):
		return b
	default:
		return a
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
