package web

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/netmon"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Network monitor HTTP contract (/admin/api/monitoring*). The monitor itself
// (resolution, checks, history) is covered by internal/netmon.

type stubMonitor struct {
	mu        sync.Mutex
	builders  []uint
	histories []netmon.HistoryQuery
	detailErr error
	histErr   error
}

func (m *stubMonitor) Overview(context.Context) (*netmon.Overview, error) {
	return &netmon.Overview{Enabled: true, IntervalSeconds: 60, Builders: []netmon.BuilderSummary{{ID: 1, Name: "b", Status: "up"}}}, nil
}

func (m *stubMonitor) BuilderDetail(_ context.Context, id uint) (*netmon.BuilderDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builders = append(m.builders, id)
	if m.detailErr != nil {
		return nil, m.detailErr
	}
	return &netmon.BuilderDetail{Builder: netmon.BuilderSummary{ID: id}, Countries: []netmon.CountryView{}}, nil
}

func (m *stubMonitor) History(_ context.Context, q netmon.HistoryQuery) (*netmon.History, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.histories = append(m.histories, q)
	if m.histErr != nil {
		return nil, m.histErr
	}
	return &netmon.History{Scope: "builder", Period: q.Period, Outages: []netmon.OutageView{}, Latency: []netmon.LatencyPoint{}}, nil
}

func newMonitorAPIFixture(t *testing.T, monitor NetworkMonitor) *adminAPIFixture {
	t.Helper()
	base := newAdminAPIFixtureNoServer(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(adminTestPassword), config.AdminMinBcryptCost)
	require.NoError(t, err)
	cfg := &config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: "https://admin.example.com"}
	s := NewServer("127.0.0.1:0", nil, cfg, "", nil, nil)
	s.SetAdminService(service.NewAdminService(base.db, nil, nil))
	if monitor != nil {
		s.SetNetworkMonitor(monitor)
	}
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(ctx))
	})
	base.attach(s)
	return base
}

func TestAdminAPI_Monitoring(t *testing.T) {
	monitor := &stubMonitor{}
	f := newMonitorAPIFixture(t, monitor)

	t.Run("requires a session", func(t *testing.T) {
		resp := f.doAs(f.anon, http.MethodGet, "/admin/api/monitoring")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	f.login()

	t.Run("overview", func(t *testing.T) {
		resp := f.do(http.MethodGet, "/admin/api/monitoring")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out netmon.Overview
		f.decode(resp, &out)
		assert.True(t, out.Enabled)
		require.Len(t, out.Builders, 1)
		assert.Equal(t, "up", out.Builders[0].Status)

		head := f.do(http.MethodHead, "/admin/api/monitoring")
		assert.Equal(t, http.StatusOK, head.StatusCode)
	})

	t.Run("read-only", func(t *testing.T) {
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
			resp := f.do(method, "/admin/api/monitoring", f.withCSRF(), withJSON("{}"))
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, method)
			_ = resp.Body.Close()
		}
	})

	t.Run("builder detail", func(t *testing.T) {
		resp := f.do(http.MethodGet, "/admin/api/monitoring/builders/7")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
		assert.Equal(t, []uint{7}, monitor.builders)

		for _, path := range []string{"/admin/api/monitoring/builders/0", "/admin/api/monitoring/builders/x", "/admin/api/monitoring/nope"} {
			resp := f.do(http.MethodGet, path)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
			_ = resp.Body.Close()
		}

		monitor.detailErr = netmon.ErrNotFound
		resp = f.do(http.MethodGet, "/admin/api/monitoring/builders/8")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		_ = resp.Body.Close()
		monitor.detailErr = nil
	})

	t.Run("history parameters", func(t *testing.T) {
		resp := f.do(http.MethodGet, "/admin/api/monitoring/history?builder_id=3&country=DE&period=7d")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
		resp = f.do(http.MethodGet, "/admin/api/monitoring/history?target_id=5")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
		resp = f.do(http.MethodGet, "/admin/api/monitoring/history?builder_id=3&country=")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()

		require.Len(t, monitor.histories, 3)
		q := monitor.histories[0]
		assert.EqualValues(t, 3, q.BuilderID)
		require.NotNil(t, q.Country)
		assert.Equal(t, "DE", *q.Country)
		assert.Equal(t, "7d", q.Period)
		assert.EqualValues(t, 5, monitor.histories[1].TargetID)
		assert.Equal(t, "24h", monitor.histories[1].Period, "default period")
		assert.Nil(t, monitor.histories[1].Country)
		require.NotNil(t, monitor.histories[2].Country, "an empty country is the no-country group")
		assert.Empty(t, *monitor.histories[2].Country)

		for _, query := range []string{"builder_id=abc", "target_id=-1", "builder_id=1&secret=1", "builder_id=1&country=ABCDEFGHIJ"} {
			resp := f.do(http.MethodGet, "/admin/api/monitoring/history?"+query)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, query)
			_ = resp.Body.Close()
		}
		assert.Len(t, monitor.histories, 3, "invalid queries never reach the monitor")

		monitor.histErr = netmon.ErrInvalidQuery
		resp = f.do(http.MethodGet, "/admin/api/monitoring/history?builder_id=1&period=1y")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		_ = resp.Body.Close()
		monitor.histErr = nil
	})
}

func TestAdminAPI_MonitoringNotWired(t *testing.T) {
	f := newMonitorAPIFixture(t, nil)
	f.login()
	resp := f.do(http.MethodGet, "/admin/api/monitoring")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	_ = resp.Body.Close()
}
