//go:build adminsmoke

package web

// Local browser smoke stand: the real web.Server (admin auth, CSRF, admin API,
// embedded admin UI, /sub) on a temporary SQLite database, with a local
// upstream serving two test catalogues. It never touches a real provider,
// database or production setting. Run it only through
// frontend/admin/live/README.md (`go test -tags adminsmoke -run
// TestAdminLiveSmokeServer ./internal/web/`), then run the Playwright live
// spec against it; the test blocks until the spec writes the stop file.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func smokeLink(user, host, name string) string {
	return fmt.Sprintf("vless://%s@%s:443?security=tls&sni=%s&type=tcp#%s", user, host, host, url.PathEscape(name))
}

// liveSmokeState is what the Playwright spec reads.
type liveSmokeState struct {
	SourceA        uint   `json:"source_a"`
	SourceB        uint   `json:"source_b"`
	OtherBuilder   uint   `json:"other_builder"`
	OtherRule      uint   `json:"other_rule"`
	SubscriptionID uint   `json:"subscription_id"`
	SubToken       string `json:"sub_token"`
}

func TestAdminLiveSmokeServer(t *testing.T) {
	addr := os.Getenv("ADMIN_SMOKE_ADDR")
	origin := os.Getenv("ADMIN_SMOKE_ORIGIN")
	password := os.Getenv("ADMIN_SMOKE_PASSWORD")
	stateDir := os.Getenv("ADMIN_SMOKE_STATE_DIR")
	if addr == "" || origin == "" || password == "" || stateDir == "" {
		t.Skip("ADMIN_SMOKE_ADDR, ADMIN_SMOKE_ORIGIN, ADMIN_SMOKE_PASSWORD and ADMIN_SMOKE_STATE_DIR are required")
	}
	ctx := context.Background()

	catalogues := map[string][]string{
		"/a": {
			smokeLink("a1", "de1.smoke.test", "🇩🇪 Frankfurt"),
			smokeLink("a2", "de2.smoke.test", "🇩🇪 Berlin"),
			smokeLink("a3", "nl.smoke.test", "🇳🇱 Amsterdam"),
			smokeLink("a4", "fi.smoke.test", "🇫🇮 Helsinki"),
		},
		"/b": {
			smokeLink("b1", "us.smoke.test", "🇺🇸 Dallas"),
			smokeLink("b2", "de3.smoke.test", "🇩🇪 Munich"),
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		links, ok := catalogues[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(strings.Join(links, "\n")))
	}))
	t.Cleanup(upstream.Close)

	db, err := database.NewService(filepath.Join(t.TempDir(), "admin-live-smoke.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	builders := service.NewBuilderService(db)
	builders.SetCatalogueFetcher(func(ctx context.Context, src database.ProviderSource) (*service.FetchedCatalogue, error) {
		c, err := subserver.FetchSourceCatalogue(ctx, src)
		if err != nil {
			return nil, err
		}
		return &service.FetchedCatalogue{Format: c.Format, Entries: c.Entries, Skipped: c.Skipped, Duplicates: c.Duplicates}, nil
	})

	newSource := func(name, path string) uint {
		view, sync, err := builders.CreateSource(ctx, service.CreateSourceInput{
			Name: name, Type: "auto", SubscriptionURL: upstream.URL + path, Headers: "{}", Enabled: true,
		})
		require.NoError(t, err)
		require.NotNil(t, sync)
		require.Equal(t, database.SourceSyncOK, sync.Status, name)
		return view.ID
	}
	state := liveSmokeState{SourceA: newSource("Smoke A", "/a"), SourceB: newSource("Smoke B", "/b")}

	// Another builder with its own rule (ownership checks).
	other, err := db.CreateBuilder(ctx, database.AdminConfigMeta{Actor: "seed", RequestKey: "seed-other"}, database.BuilderCreateInput{Name: "Чужой", Enabled: true})
	require.NoError(t, err)
	state.OtherBuilder = other.TargetID
	cur, err := db.GetBuilder(ctx, other.TargetID)
	require.NoError(t, err)
	_, err = db.SaveBuilderConfiguration(ctx, database.AdminConfigMeta{Actor: "seed", RequestKey: "seed-other-cfg"}, database.BuilderConfigurationInput{
		ID: other.TargetID, Version: cur.Version, Name: cur.Name, Enabled: true, SourceIDs: []uint{state.SourceB},
		Items: []database.BuilderConfigItem{{Kind: database.BuilderItemKindCountry, SourceID: state.SourceB, CountryCode: "US", Enabled: true}},
	})
	require.NoError(t, err)
	cur, err = db.GetBuilder(ctx, other.TargetID)
	require.NoError(t, err)
	state.OtherRule = cur.Items[0].ID

	// A customer subscription; the spec assigns the builder to it.
	free, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	expires := time.Now().Add(24 * time.Hour)
	sub := &database.Subscription{
		TelegramID: 777001, ClientID: "smoke-client", SubscriptionID: "smoke-sub", ExpiresAt: &expires,
		Status: string(database.SubscriptionStatusActive), PlanID: free.ID,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	state.SubscriptionID, state.SubToken = sub.ID, sub.Token

	hash, err := bcrypt.GenerateFromPassword([]byte(password), config.AdminMinBcryptCost)
	require.NoError(t, err)
	cfg := &config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: origin}
	subSrv := subserver.NewService(time.Minute)
	t.Cleanup(subSrv.Stop)
	// The admin namespace requires an https SITE_URL (config.AdminConfiguration),
	// so the browser talks to a local TLS proxy (self-signed httptest cert) in
	// front of the plain server, exactly like production TLS termination.
	const internalAddr = "127.0.0.1:18182"
	s := NewServer(internalAddr, db, cfg, "", nil, subSrv)
	s.SetAdminService(service.NewAdminService(db, nil))
	s.SetBuilderService(builders)
	require.NoError(t, s.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Stop(stopCtx)
	})

	backend, err := url.Parse("http://" + internalAddr)
	require.NoError(t, err)
	proxy := httptest.NewUnstartedServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(backend)
		r.Out.Host = r.In.Host
	}})
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	_ = proxy.Listener.Close()
	proxy.Listener = listener
	proxy.StartTLS()
	t.Cleanup(proxy.Close)

	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, "state.json"), raw, 0o600))
	t.Logf("admin live smoke server ready on %s", addr)

	stop := filepath.Join(stateDir, "stop")
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stop); err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("smoke stand timed out waiting for the stop file")
}
