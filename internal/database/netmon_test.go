package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func monEndpoint(host string) MonitorEndpointSnapshot {
	return MonitorEndpointSnapshot{
		Key: "key-" + host, Protocol: "vless", Host: host, Port: 443, Security: "reality",
		Transport: "tcp", SNI: "www.example.com", Probe: "tls",
	}
}

func monNode(source uint, fp, country, host string) MonitorNodeSnapshot {
	return MonitorNodeSnapshot{SourceID: source, Fingerprint: fp, Name: "node " + fp, Country: country, Endpoint: monEndpoint(host)}
}

func monSnapshot(builder uint, scope, fetched []uint, nodes ...MonitorNodeSnapshot) MonitorBuilderSnapshot {
	return MonitorBuilderSnapshot{BuilderID: builder, BuilderName: "b", ScopeSources: scope, FetchedSources: fetched, Nodes: nodes}
}

func monTargetsByHost(t *testing.T, s *Service) map[string]MonitorTarget {
	t.Helper()
	var all []MonitorTarget
	require.NoError(t, s.db.Order("id").Find(&all).Error)
	out := map[string]MonitorTarget{}
	for _, tg := range all {
		out[tg.Host] = tg
	}
	return out
}

func monOutages(t *testing.T, s *Service, scope string) []MonitorOutage {
	t.Helper()
	var out []MonitorOutage
	require.NoError(t, s.db.Where("scope = ?", scope).Order("id").Find(&out).Error)
	return out
}

func monCountry(t *testing.T, s *Service, builder uint, country string) MonitorCountryState {
	t.Helper()
	var st MonitorCountryState
	require.NoError(t, s.db.Where("builder_id = ? AND country_code = ?", builder, country).First(&st).Error)
	return st
}

func TestSyncMonitorBindings_SharedEndpointIsOneTarget(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()

	// Builders 1 and 2 serve the same server (same endpoint key) from two
	// sources, plus one own server each.
	err := s.SyncMonitorBindings(ctx, monAt(0), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "shared.example.com"), monNode(10, "fp-b", "NL", "nl.example.com")),
		monSnapshot(2, []uint{20}, []uint{20}, monNode(20, "fp-x", "DE", "shared.example.com"), monNode(20, "fp-y", "FI", "fi.example.com")),
	})
	require.NoError(t, err)

	targets := monTargetsByHost(t, s)
	require.Len(t, targets, 3, "the shared endpoint is one target")
	active, err := s.ListActiveMonitorTargets(ctx)
	require.NoError(t, err)
	assert.Len(t, active, 3)

	rows, err := s.ListMonitorBindings(ctx, 0, true)
	require.NoError(t, err)
	assert.Len(t, rows, 4)
	shared := targets["shared.example.com"].ID
	n := 0
	for _, r := range rows {
		if r.TargetID == shared {
			n++
		}
	}
	assert.Equal(t, 2, n, "two builders bind the one shared target")

	states, err := s.ListMonitorCountryStates(ctx, 0)
	require.NoError(t, err)
	assert.Len(t, states, 4) // (1,DE) (1,NL) (2,DE) (2,FI)

	// Re-syncing the same resolution changes nothing structurally.
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(1), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "shared.example.com"), monNode(10, "fp-b", "NL", "nl.example.com")),
		monSnapshot(2, []uint{20}, []uint{20}, monNode(20, "fp-x", "DE", "shared.example.com"), monNode(20, "fp-y", "FI", "fi.example.com")),
	}))
	assert.Len(t, monTargetsByHost(t, s), 3)
	rows, err = s.ListMonitorBindings(ctx, 0, false)
	require.NoError(t, err)
	assert.Len(t, rows, 4, "no duplicate bindings")
}

func TestRecordMonitorChecks_OutageLifecycleSamplesAndLatency(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()

	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(0), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com")),
	}))
	id := monTargetsByHost(t, s)["a.example.com"].ID
	check := func(minute int, ok bool, latency int, code string) {
		t.Helper()
		r := MonitorCheckResult{TargetID: id, OK: ok, LatencyMS: latency, ErrorCode: code, CheckedAt: monAt(minute)}
		require.NoError(t, s.RecordMonitorChecks(ctx, monAt(minute), []MonitorCheckResult{r}, 2))
	}

	check(0, true, 40, "")
	check(1, true, 60, "")
	check(2, false, 0, "timeout")
	check(3, false, 0, "timeout")
	check(4, false, 0, "timeout") // still DOWN: no second outage
	check(6, true, 50, "")        // recovered

	outages := monOutages(t, s, MonitorScopeTarget)
	require.Len(t, outages, 1, "exactly one outage for one DOWN period")
	o := outages[0]
	assert.Equal(t, monAt(2), o.StartedAt.UTC(), "outage starts at the first failed check")
	require.NotNil(t, o.EndedAt)
	assert.Equal(t, monAt(6), o.EndedAt.UTC())
	assert.Equal(t, 4*time.Minute, o.EndedAt.Sub(o.StartedAt))
	assert.Equal(t, MonitorEndRecovered, o.EndReason)
	assert.Equal(t, "timeout", o.ErrorCode)

	tg := monTargetsByHost(t, s)["a.example.com"]
	assert.Equal(t, MonitorStatusUp, tg.Status)
	assert.Equal(t, 50, *tg.LastLatencyMS)
	assert.Equal(t, monAt(2), tg.LastDownAt.UTC())

	// Samples: minutes 0-4 are one 5-minute bucket, minute 6 the next. No row
	// per identical check.
	var samples []struct {
		BucketStart  int64
		Checks       int
		Failures     int
		LatencyCount int
		LatencySumMs int
		LatencyMinMs *int
		LatencyMaxMs *int
	}
	require.NoError(t, s.db.Raw("SELECT * FROM monitor_samples WHERE target_id = ? ORDER BY bucket_start", id).Scan(&samples).Error)
	require.Len(t, samples, 2)
	assert.Equal(t, 5, samples[0].Checks)
	assert.Equal(t, 3, samples[0].Failures)
	assert.Equal(t, 2, samples[0].LatencyCount)
	assert.Equal(t, 100, samples[0].LatencySumMs)
	assert.Equal(t, 40, *samples[0].LatencyMinMs)
	assert.Equal(t, 60, *samples[0].LatencyMaxMs)
	assert.Equal(t, 1, samples[1].Checks)

	series, err := s.MonitorLatencySeries(ctx, []uint{id}, monAt(-60), 3600)
	require.NoError(t, err)
	require.Len(t, series, 1)
	assert.Equal(t, 6, series[0].Checks)
	assert.Equal(t, 3, series[0].Failures)
	assert.EqualValues(t, 150, series[0].LatencySum)
	assert.Equal(t, 3, series[0].LatencyCount)

	// Country (one server) followed: UP -> DOWN -> UP.
	country := monOutages(t, s, MonitorScopeCountry)
	require.Len(t, country, 1)
	assert.Equal(t, MonitorOutageDown, country[0].Kind)
	assert.Equal(t, monAt(3), country[0].StartedAt.UTC(), "the country is DOWN once the server is DOWN")
	assert.Equal(t, MonitorEndRecovered, country[0].EndReason)
	assert.Equal(t, "timeout", country[0].ErrorCode)
	assert.Equal(t, MonitorStatusUp, monCountry(t, s, 1, "DE").Status)
}

func TestRecordMonitorChecks_CountryDegradedDownRecovered(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()

	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(0), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com"), monNode(10, "fp-b", "DE", "b.example.com")),
	}))
	targets := monTargetsByHost(t, s)
	a, b := targets["a.example.com"].ID, targets["b.example.com"].ID
	round := func(minute int, aOK, bOK bool) {
		t.Helper()
		res := []MonitorCheckResult{
			{TargetID: a, OK: aOK, LatencyMS: 10, ErrorCode: map[bool]string{false: "refused"}[aOK], CheckedAt: monAt(minute)},
			{TargetID: b, OK: bOK, LatencyMS: 20, ErrorCode: map[bool]string{false: "timeout"}[bOK], CheckedAt: monAt(minute)},
		}
		require.NoError(t, s.RecordMonitorChecks(ctx, monAt(minute), res, 1))
	}

	round(0, true, true)
	assert.Equal(t, MonitorStatusUp, monCountry(t, s, 1, "DE").Status)
	round(1, true, false)
	assert.Equal(t, MonitorStatusDegraded, monCountry(t, s, 1, "DE").Status)
	round(2, true, false) // DEGRADED/DEGRADED: nothing new
	round(3, false, false)
	assert.Equal(t, MonitorStatusDown, monCountry(t, s, 1, "DE").Status)
	round(4, true, true)
	st := monCountry(t, s, 1, "DE")
	assert.Equal(t, MonitorStatusUp, st.Status)
	assert.Equal(t, monAt(3), st.LastDownAt.UTC())

	outages := monOutages(t, s, MonitorScopeCountry)
	require.Len(t, outages, 2)
	assert.Equal(t, MonitorOutageDegraded, outages[0].Kind)
	assert.Equal(t, monAt(1), outages[0].StartedAt.UTC())
	assert.Equal(t, monAt(3), outages[0].EndedAt.UTC())
	assert.Equal(t, MonitorEndChanged, outages[0].EndReason)
	assert.Equal(t, MonitorOutageDown, outages[1].Kind)
	assert.Equal(t, monAt(3), outages[1].StartedAt.UTC())
	assert.Equal(t, monAt(4), outages[1].EndedAt.UTC())
	assert.Equal(t, MonitorEndRecovered, outages[1].EndReason)

	latest, err := s.LatestMonitorCountryOutages(ctx)
	require.NoError(t, err)
	require.Len(t, latest, 1)
	assert.Equal(t, outages[1].ID, latest[0].ID)
}

func TestMonitorOutages_AtMostOneOpenPerSubject(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	id := uint(99)
	first := MonitorOutage{Scope: MonitorScopeTarget, TargetID: &id, Kind: MonitorOutageDown, StartedAt: monAt(0)}
	require.NoError(t, s.db.Create(&first).Error)
	dup := MonitorOutage{Scope: MonitorScopeTarget, TargetID: &id, Kind: MonitorOutageDown, StartedAt: monAt(1)}
	err := s.db.Create(&dup).Error
	require.Error(t, err, "the partial unique index rejects a second open outage")
	assert.True(t, strings.Contains(err.Error(), "UNIQUE"))
}

func TestSyncMonitorBindings_CatalogueChangeKeepsHistory(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()

	both := monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com"), monNode(10, "fp-b", "NL", "b.example.com"))
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(0), []MonitorBuilderSnapshot{both}))
	b := monTargetsByHost(t, s)["b.example.com"].ID
	for m := 0; m < 2; m++ { // b goes DOWN: open target + country outage
		require.NoError(t, s.RecordMonitorChecks(ctx, monAt(m), []MonitorCheckResult{{TargetID: b, ErrorCode: "refused", CheckedAt: monAt(m)}}, 2))
	}
	require.Len(t, monOutages(t, s, MonitorScopeTarget), 1)

	// b disappears from the fetched source catalogue.
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(5), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com")),
	}))
	tg := monTargetsByHost(t, s)["b.example.com"]
	assert.False(t, tg.Active, "an unserved target is inactive, not deleted")
	assert.Equal(t, MonitorStatusUnknown, tg.Status)
	outages := monOutages(t, s, MonitorScopeTarget)
	require.Len(t, outages, 1, "history is kept")
	assert.Equal(t, MonitorEndRemoved, outages[0].EndReason)
	assert.Equal(t, monAt(5), outages[0].EndedAt.UTC())
	country := monOutages(t, s, MonitorScopeCountry)
	require.Len(t, country, 1)
	assert.Equal(t, MonitorEndRemoved, country[0].EndReason)
	assert.False(t, monCountry(t, s, 1, "NL").Active)
	all, err := s.ListMonitorBindings(ctx, 1, false)
	require.NoError(t, err)
	assert.Len(t, all, 2, "retired bindings stay for history")
	active, err := s.ListActiveMonitorTargets(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "a.example.com", active[0].Host)

	// Results for an inactive target are ignored.
	require.NoError(t, s.RecordMonitorChecks(ctx, monAt(6), []MonitorCheckResult{{TargetID: b, OK: true, CheckedAt: monAt(6)}}, 2))
	assert.Equal(t, MonitorStatusUnknown, monTargetsByHost(t, s)["b.example.com"].Status)

	// The server comes back: same target and binding reactivate, fresh state.
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(10), []MonitorBuilderSnapshot{both}))
	tg = monTargetsByHost(t, s)["b.example.com"]
	assert.True(t, tg.Active)
	assert.Equal(t, 0, tg.ConsecutiveFailures)
	assert.True(t, monCountry(t, s, 1, "NL").Active)
	all, err = s.ListMonitorBindings(ctx, 1, false)
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestSyncMonitorBindings_FailedSourceDisabledSourceAndBuilder(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()

	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(0), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10, 11}, []uint{10, 11}, monNode(10, "fp-a", "DE", "a.example.com"), monNode(11, "fp-c", "FI", "c.example.com")),
		monSnapshot(2, []uint{20}, []uint{20}, monNode(20, "fp-z", "SE", "z.example.com")),
	}))

	// Source 11 fails to fetch: its node is unknown, not gone.
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(1), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10, 11}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com")),
		monSnapshot(2, []uint{20}, []uint{20}, monNode(20, "fp-z", "SE", "z.example.com")),
	}))
	assert.True(t, monTargetsByHost(t, s)["c.example.com"].Active)

	// Source 11 is disabled (out of scope) and builder 2 is disabled (absent).
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(2), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com")),
	}))
	targets := monTargetsByHost(t, s)
	assert.True(t, targets["a.example.com"].Active)
	assert.False(t, targets["c.example.com"].Active, "disabled source")
	assert.False(t, targets["z.example.com"].Active, "disabled builder")
	rows, err := s.ListMonitorBindings(ctx, 2, true)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestPruneMonitorSamples(t *testing.T) {
	t.Parallel()
	s := newTestService(t)
	ctx := context.Background()
	require.NoError(t, s.SyncMonitorBindings(ctx, monAt(0), []MonitorBuilderSnapshot{
		monSnapshot(1, []uint{10}, []uint{10}, monNode(10, "fp-a", "DE", "a.example.com")),
	}))
	id := monTargetsByHost(t, s)["a.example.com"].ID
	old := monT0.Add(-40 * 24 * time.Hour)
	require.NoError(t, s.RecordMonitorChecks(ctx, old, []MonitorCheckResult{{TargetID: id, OK: true, LatencyMS: 5, CheckedAt: old}}, 2))
	require.NoError(t, s.RecordMonitorChecks(ctx, monT0, []MonitorCheckResult{{TargetID: id, OK: true, LatencyMS: 5, CheckedAt: monT0}}, 2))

	n, err := s.PruneMonitorSamples(ctx, monT0.Add(-35*24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	var left int64
	require.NoError(t, s.db.Raw("SELECT COUNT(*) FROM monitor_samples").Scan(&left).Error)
	assert.EqualValues(t, 1, left)
}
