package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Source catalogue HTTP contract (/admin/api/sources*) through the handler
// installed by Start and the production fetcher/parser
// (subserver.FetchSourceCatalogue). The upstream serves the sanitized real
// provider fixture; its URL, HWID and header values must never reach a
// browser response.

const (
	sourceTestHWID   = "hwid-secret-4242"
	sourceTestHeader = "header-secret-9999"
)

type sourceAPIFixture struct {
	*adminAPIFixture
	upstream *httptest.Server
	status   atomic.Int32
	hits     atomic.Int32
}

func newSourceAPIFixture(t *testing.T) *sourceAPIFixture {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "subserver", "testdata", "provider_xray_configs.json"))
	require.NoError(t, err)

	f := &sourceAPIFixture{adminAPIFixture: newAdminAPIFixtureNoServer(t)}
	f.status.Store(http.StatusOK)
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		assert.Equal(t, sourceTestHWID, r.Header.Get("X-HWID"), "credentials are sent upstream")
		assert.Equal(t, sourceTestHeader, r.Header.Get("X-Token"))
		w.WriteHeader(int(f.status.Load()))
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.upstream.Close)

	hash, err := bcrypt.GenerateFromPassword([]byte(adminTestPassword), config.AdminMinBcryptCost)
	require.NoError(t, err)
	cfg := &config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: "https://admin.example.com"}
	s := NewServer("127.0.0.1:0", nil, cfg, "", nil, nil)
	s.SetAdminService(service.NewAdminService(f.db, nil, nil))
	builders := service.NewBuilderService(f.db)
	builders.SetCatalogueFetcher(func(ctx context.Context, src database.ProviderSource) (*service.FetchedCatalogue, error) {
		c, err := subserver.FetchSourceCatalogue(ctx, src)
		if err != nil {
			return nil, err
		}
		return &service.FetchedCatalogue{Format: c.Format, Entries: c.Entries, Skipped: c.Skipped, Duplicates: c.Duplicates}, nil
	})
	s.SetBuilderService(builders)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(ctx))
	})
	f.attach(s)
	return f
}

func (f *sourceAPIFixture) createBody(name, format string) string {
	return fmt.Sprintf(`{"name":%q,"description":"","type":%q,"subscription_url":%q,"hwid":%q,"user_agent":"Happ/2.0","headers":%q,"enabled":true}`,
		name, format, f.upstream.URL+"/connection/subs/private-token", sourceTestHWID, `{"X-Token":"`+sourceTestHeader+`"}`)
}

// text reads the whole body and asserts no credential is in it.
func (f *sourceAPIFixture) text(resp *http.Response) string {
	f.t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(f.t, err)
	s := string(b)
	for _, secret := range []string{f.upstream.URL, "private-token", sourceTestHWID, sourceTestHeader, "Happ/2.0", "redacted-"} {
		assert.NotContains(f.t, s, secret, "a browser response never carries credentials")
	}
	for _, key := range []string{"subscription_url", "SubscriptionURL", "hwid", "HWID", "headers", "user_agent"} {
		assert.NotContains(f.t, s, `"`+key+`"`, "no credential field on the wire")
	}
	return s
}

type syncWire struct {
	Source     service.SourceView `json:"source"`
	Status     string             `json:"status"`
	Error      string             `json:"error"`
	Format     string             `json:"format"`
	Added      int                `json:"added"`
	Updated    int                `json:"updated"`
	Removed    int                `json:"removed"`
	Total      int                `json:"total"`
	Skipped    int                `json:"skipped"`
	Duplicates int                `json:"duplicates"`
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func sourcePath(id uint, suffix string) string {
	return "/admin/api/sources/" + strconv.FormatUint(uint64(id), 10) + suffix
}

func TestAdminSourceAPI_CreateRunsInitialSyncWithoutLeakingCredentials(t *testing.T) {
	f := newSourceAPIFixture(t)
	f.login()

	resp := f.do(http.MethodPost, "/admin/api/sources", f.withCSRF(), withJSON(f.createBody("Liberty", "")))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	raw := f.text(resp)
	var created struct {
		Source service.SourceView `json:"source"`
		Sync   *syncWire          `json:"sync"`
	}
	require.NoError(t, jsonUnmarshal(raw, &created))
	require.NotNil(t, created.Sync, "initial sync runs on create")
	assert.Equal(t, int32(1), f.hits.Load())
	assert.Equal(t, "ok", created.Sync.Status)
	assert.Equal(t, "json", created.Sync.Format)
	assert.Equal(t, 40, created.Sync.Added)
	assert.Equal(t, 40, created.Sync.Total)
	assert.Equal(t, "auto", created.Source.Type)
	assert.Equal(t, 40, created.Source.Catalogue.Entries)
	assert.Equal(t, 23, created.Source.Catalogue.Countries)
	assert.Equal(t, database.SourceCountryCount{Code: "CH", Count: 7}, created.Source.Catalogue.ByCountry[0])
	assert.Equal(t, map[string]int{"vless": 33, "hysteria2": 4, "shadowsocks": 3}, created.Source.Catalogue.Protocols)
	require.NotNil(t, created.Source.LastSyncAt)

	// The list shows the per-source statistics.
	var list struct {
		Sources []service.SourceView `json:"sources"`
	}
	require.NoError(t, jsonUnmarshal(f.text(f.do(http.MethodGet, "/admin/api/sources")), &list))
	require.Len(t, list.Sources, 1)
	assert.Equal(t, 40, list.Sources[0].Catalogue.Entries)
	assert.Equal(t, "ok", list.Sources[0].LastSyncStatus)

	// Entries are snake_case and credential-free, disappeared ones included on ?all=1.
	entries := f.text(f.do(http.MethodGet, sourcePath(created.Source.ID, "/entries?all=1")))
	for _, key := range []string{`"original_name"`, `"country_code"`, `"fingerprint"`, `"protocol"`, `"present"`, `"last_seen_at"`, `"upstream_position"`} {
		assert.Contains(t, entries, key)
	}
	assert.NotContains(t, entries, `"OriginalName"`)
	assert.Contains(t, entries, "Швейцария GAMING")
}

func TestAdminSourceAPI_RefreshIsServerSide(t *testing.T) {
	f := newSourceAPIFixture(t)
	f.login()
	resp := f.do(http.MethodPost, "/admin/api/sources", f.withCSRF(), withJSON(f.createBody("Refresh", "json")))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		Source service.SourceView `json:"source"`
	}
	require.NoError(t, jsonUnmarshal(f.text(resp), &created))
	id := created.Source.ID

	// The caller can no longer supply the catalogue.
	forged := `{"entries":[{"fingerprint":"x","original_name":"forged","protocol":"vless","country_code":"DE"}],"sync_status":"ok","sync_error":""}`
	f.expectError(f.do(http.MethodPost, sourcePath(id, "/refresh"), f.withCSRF(), withJSON(forged)), http.StatusBadRequest, "invalid_request")

	for _, body := range []string{"{}", ""} {
		resp := f.do(http.MethodPost, sourcePath(id, "/refresh"), f.withCSRF(), withJSON(body))
		require.Equal(t, http.StatusOK, resp.StatusCode, "body %q", body)
		var out syncWire
		require.NoError(t, jsonUnmarshal(f.text(resp), &out))
		assert.Equal(t, "ok", out.Status)
		assert.Equal(t, 0, out.Added)
		assert.Equal(t, 40, out.Updated)
		assert.Equal(t, 0, out.Removed)
		assert.Equal(t, 40, out.Total)
	}
	assert.Equal(t, int32(3), f.hits.Load(), "every sync fetches the upstream on the server")

	// Upstream failure: 200 with status=error, the catalogue is kept.
	f.status.Store(http.StatusBadGateway)
	resp = f.do(http.MethodPost, sourcePath(id, "/refresh"), f.withCSRF(), withJSON("{}"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var failed syncWire
	require.NoError(t, jsonUnmarshal(f.text(resp), &failed))
	assert.Equal(t, "error", failed.Status)
	assert.Equal(t, "http_502", failed.Error)
	assert.Equal(t, 40, failed.Total)
	assert.Equal(t, 40, failed.Source.Catalogue.Entries)
	assert.Equal(t, "error", failed.Source.LastSyncStatus)

	f.expectError(f.do(http.MethodPost, sourcePath(999999, "/refresh"), f.withCSRF(), withJSON("{}")), http.StatusNotFound, "not_found")
}

func TestAdminSourceAPI_ValidationAndGuards(t *testing.T) {
	f := newSourceAPIFixture(t)
	f.expectError(f.doAs(f.anon, http.MethodGet, "/admin/api/sources"), http.StatusUnauthorized, "unauthorized")
	f.login()

	// Unsafe methods without CSRF or from a foreign origin never run a sync.
	resp := f.do(http.MethodPost, "/admin/api/sources", withJSON(f.createBody("NoCSRF", "")))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp = f.do(http.MethodPost, "/admin/api/sources", f.withCSRF(), withOrigin(adminAPIForeignOrigin), withJSON(f.createBody("Foreign", "")))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Zero(t, f.hits.Load())

	for _, tc := range []struct{ body, field string }{
		{strings.Replace(f.createBody("T", ""), `"type":""`, `"type":"xui"`, 1), "type"},
		{strings.Replace(f.createBody("", ""), `"name":""`, `"name":"  "`, 1), "name"},
		{strings.Replace(f.createBody("U", ""), f.upstream.URL, "ftp://provider.example", 1), "subscription_url"},
	} {
		resp := f.do(http.MethodPost, "/admin/api/sources", f.withCSRF(), withJSON(tc.body))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, tc.field)
		payload := f.raw(resp)
		assert.Equal(t, "invalid_source", payload["error"])
		assert.Equal(t, tc.field, payload["field"])
	}
	assert.Zero(t, f.hits.Load())
}
