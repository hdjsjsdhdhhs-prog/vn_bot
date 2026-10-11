package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The approved matrix (task decision): any change here must be re-verified
// against the developers' official pages, see doc/vpn-apps.md.
var wantVPNMatrix = map[string][]string{
	"ios":     {"happ", "incy", "v2raytun"},
	"android": {"happ", "incy", "v2raytun"},
	"windows": {"happ"},
	"linux":   {"happ"},
	"macos":   {"happ"},
}

var wantVPNPrefixes = map[string]string{
	"happ":     "happ://add/",
	"incy":     "incy://add/",
	"v2raytun": "v2raytun://import/",
}

var wantVPNDownloads = map[string][]string{
	"happ/ios":     {"https://apps.apple.com/us/app/happ-proxy-utility/id6504287215", "https://happ.info/"},
	"happ/android": {"https://play.google.com/store/apps/details?id=com.happproxy", "https://github.com/Happ-proxy/happ-android/releases/latest/download/Happ.apk"},
	"happ/windows": {"https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe", "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.arm64.exe"},
	"happ/linux": {
		"https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.deb",
		"https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.rpm",
		"https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.pkg.tar.zst",
		"https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.arm64.deb",
		"https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.arm64.rpm",
	},
	"happ/macos":       {"https://apps.apple.com/us/app/happ-proxy-utility/id6504287215", "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.macOS.universal.dmg", "https://happ.info/"},
	"incy/ios":         {"https://apps.apple.com/ru/app/incy/id6756943388", "https://incy.cc/"},
	"incy/android":     {"https://play.google.com/store/apps/details?id=llc.itdev.incy", "https://incy.cc/"},
	"v2raytun/ios":     {"https://v2raytun.com/"},
	"v2raytun/android": {"https://play.google.com/store/apps/details?id=com.v2raytun.android", "https://v2raytun.com/"},
}

func TestVPNCatalog_MatrixDownloadsAndPrefixes(t *testing.T) {
	catalog, err := loadVPNCatalog(staticFiles)
	require.NoError(t, err)

	var platforms []string
	for _, p := range catalog.Platforms {
		platforms = append(platforms, p.ID)
	}
	assert.Equal(t, []string{"ios", "android", "windows", "linux", "macos"}, platforms)

	names := map[string]string{}
	got := map[string][]string{}
	downloads := map[string][]string{}
	for _, a := range catalog.Apps {
		names[a.ID] = a.Name
		assert.Equal(t, wantVPNPrefixes[a.ID], a.ImportPrefix, a.ID)
		assert.NotEmpty(t, a.EnableSteps, a.ID)
		for _, p := range catalog.Platforms {
			platform, ok := a.Platforms[p.ID]
			if !ok {
				continue
			}
			got[p.ID] = append(got[p.ID], a.ID)
			for _, d := range platform.Downloads {
				downloads[a.ID+"/"+p.ID] = append(downloads[a.ID+"/"+p.ID], d.URL)
			}
			// Desktop Happ scheme registration is unconfirmed: never "direct".
			if p.ID == "windows" || p.ID == "linux" || p.ID == "macos" {
				assert.Equal(t, vpnImportBestEffort, platform.Import, a.ID+"/"+p.ID)
			} else {
				assert.Equal(t, vpnImportDirect, platform.Import, a.ID+"/"+p.ID)
			}
		}
	}
	assert.Equal(t, wantVPNMatrix, got)
	assert.Equal(t, wantVPNDownloads, downloads)
	assert.Equal(t, map[string]string{"happ": "Happ", "incy": "Инси (Incy)", "v2raytun": "V2RayTun"}, names)
	for _, urls := range downloads {
		for _, u := range urls {
			assert.NotContains(t, u, "id6746188973", "the old RU Happ listing is gone")
		}
	}
}

func TestVPNImportLink_ExactURIAndEncoding(t *testing.T) {
	sub := "https://customer.example/sub/" + subscriptionInfoTokenA
	for prefix, want := range map[string]string{
		"happ://add/":        "happ://add/" + sub,
		"incy://add/":        "incy://add/" + sub,
		"v2raytun://import/": "v2raytun://import/" + sub,
	} {
		got, ok := vpnImportLink(prefix, sub)
		require.True(t, ok, prefix)
		// Developer docs pass the raw URL after the prefix: no percent-encoding.
		assert.Equal(t, want, got)
	}
	got, ok := vpnImportLink("happ://add/", "https://vpn.example:8443/custom/sub/abc-DEF_1.2~x")
	assert.True(t, ok)
	assert.Equal(t, "happ://add/https://vpn.example:8443/custom/sub/abc-DEF_1.2~x", got)

	for _, bad := range []string{
		"", "http://customer.example/sub/abc", "https://user:pass@customer.example/sub/abc",
		"https://customer.example/sub/abc?x=1", "https://customer.example/sub/abc#frag",
		"https://customer.example/sub/a b", `https://customer.example/sub/"><script>`,
		"https://customer.example/sub/%2e%2e", "javascript:alert(1)", "https://кириллица.example/sub/a",
		"https:///sub/abc", "https://customer.example/sub/abc\n", "happ://add/https://x.example/a",
	} {
		_, ok := vpnImportLink("happ://add/", bad)
		assert.False(t, ok, "%q", bad)
	}
	for _, prefix := range []string{"", "javascript:", "happ://add", "https://add/", "http://add/", "intent://add/", "data://add/", "happ://add/x/", "HAPP://add/"} {
		_, ok := vpnImportLink(prefix, sub)
		assert.False(t, ok, prefix)
	}
}

func TestVPNCatalog_ValidationRejectsUnsafeEntries(t *testing.T) {
	base := `{"platforms":[{"id":"ios","label":"iOS"}],"apps":[{"id":"happ","name":"Happ","import_prefix":"happ://add/","platforms":{"ios":{"import":"direct","downloads":[{"label":"x","url":"%s"}]}}}]}`
	load := func(doc string) error {
		_, err := loadVPNCatalog(fstest.MapFS{vpnCatalogPath: {Data: []byte(doc)}})
		return err
	}
	require.NoError(t, load(strings.Replace(base, "%s", "https://apps.apple.com/us/app/x/id1", 1)))
	for _, u := range []string{"http://apps.apple.com/x", "https://evil.example/x", "https://u:p@github.com/x", "https://github.com:444/x", "javascript:alert(1)"} {
		assert.Error(t, load(strings.Replace(base, "%s", u, 1)), u)
	}
	ok := "https://github.com/x"
	for name, doc := range map[string]string{
		"unknown platform": strings.Replace(strings.Replace(base, "%s", ok, 1), `"ios":{`, `"tv":{`, 1),
		"bad prefix":       strings.Replace(strings.Replace(base, "%s", ok, 1), "happ://add/", "javascript:", 1),
		"web prefix":       strings.Replace(strings.Replace(base, "%s", ok, 1), "happ://add/", "https://add/", 1),
		"bad import mode":  strings.Replace(strings.Replace(base, "%s", ok, 1), `"direct"`, `"auto"`, 1),
		"uncovered":        strings.Replace(strings.Replace(base, "%s", ok, 1), `{"id":"ios","label":"iOS"}`, `{"id":"ios","label":"iOS"},{"id":"linux","label":"Linux"}`, 1),
		"duplicate app":    strings.Replace(strings.Replace(base, "%s", ok, 1), `"apps":[{`, `"apps":[{"id":"happ","name":"Happ","import_prefix":"happ://add/","platforms":{"ios":{"import":"direct","downloads":[{"label":"x","url":"https://github.com/x"}]}}},{`, 1),
		"not json":         "{",
	} {
		assert.Error(t, load(doc), name)
	}
	_, err := loadVPNCatalog(fstest.MapFS{})
	assert.Error(t, err)
}

func renderConnectFixture(t *testing.T, status string, expiry *time.Time) string {
	t.Helper()
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(_ context.Context, token string) (*database.Subscription, error) {
		return &database.Subscription{ID: 987654321, TelegramID: 765432109, Token: token, Status: status, ExpiresAt: expiry}, nil
	}
	srv, _ := newSubscriptionInfoTestServer(db)
	rr := httptest.NewRecorder()
	srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, "/connect/"+subscriptionInfoTokenA, nil))
	require.Equal(t, http.StatusOK, rr.Code)

	return strings.ReplaceAll(rr.Body.String(), "\r\n", "\n")
}

func TestConnectionPage_PickerDeepLinksAndDownloads(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	body := renderConnectFixture(t, "active", &future)
	sub := "https://customer.example/sub/" + subscriptionInfoTokenA

	// One panel per supported (app, platform) pair, each with its deep link.
	panels := regexp.MustCompile(`data-panel-app="([a-z0-9]+)" data-panel-platform="([a-z]+)"`).FindAllStringSubmatch(body, -1)
	got := map[string][]string{}
	for _, p := range panels {
		got[p[2]] = append(got[p[2]], p[1])
	}
	assert.Equal(t, wantVPNMatrix, got)
	assert.Equal(t, 5, strings.Count(body, `href="happ://add/`+sub+`"`))
	assert.Equal(t, 2, strings.Count(body, `href="incy://add/`+sub+`"`))
	assert.Equal(t, 2, strings.Count(body, `href="v2raytun://import/`+sub+`"`))
	assert.Equal(t, 9, strings.Count(body, `data-import=`))
	for _, name := range []string{"Добавить подписку в Happ", "Добавить подписку в Инси (Incy)", "Добавить подписку в V2RayTun"} {
		assert.Contains(t, body, name)
	}
	// Exactly one panel (server default iOS + Happ) is visible without JS.
	visible := regexp.MustCompile(`data-panel-app="([a-z0-9]+)" data-panel-platform="([a-z]+)">`).FindAllStringSubmatch(body, -1)
	require.Len(t, visible, 1)
	assert.Equal(t, []string{"happ", "ios"}, visible[0][1:])
	assert.Len(t, regexp.MustCompile(`data-panel-platform="[a-z]+" hidden>`).FindAllString(body, -1), 8)
	// Best-effort desktop notice, never on mobile.
	assert.Equal(t, 3, strings.Count(body, "открытие Happ по ссылке зависит от версии приложения"))

	for key, urls := range wantVPNDownloads {
		for _, u := range urls {
			assert.Contains(t, html.UnescapeString(body), `href="`+u+`" target="_blank" rel="noopener noreferrer"`, key)
		}
	}
	// No "success" claims anywhere in markup or script.
	for _, claim := range []string{"Подписка добавлена", "успешно добавлена", "добавлена в"} {
		assert.NotContains(t, body, claim)
	}
	// All five platforms are offered.
	for _, label := range []string{">iOS<", ">Android<", ">Windows<", ">Linux<", ">macOS<"} {
		assert.Contains(t, body, label)
	}
	assertConnectionURLAndQR(t, body, sub)
	assertNoConnectionInternals(t, body)
}

func TestConnectionPage_NoPickerOrDeepLinksWhenInactive(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	for _, status := range []string{"expired", "active", "revoked", "paused", "canceled"} {
		body := renderConnectFixture(t, status, &past)
		assert.NotContains(t, body, `id="connect-picker"`, status)
		for _, scheme := range []string{"happ://", "incy://", "v2raytun://"} {
			assert.NotContains(t, body, scheme, status)
		}
		// Fallbacks stay available in every state.
		assertConnectionURLAndQR(t, body, "https://customer.example/sub/"+subscriptionInfoTokenA)
		assert.Contains(t, body, "Не передавайте эту страницу")
	}
}

func TestConnectionPage_UnsafeSubscriptionURLKeepsOnlyFallbacks(t *testing.T) {
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		return &database.Subscription{Token: subscriptionInfoTokenA, Status: "active"}, nil
	}
	srv, cfg := newSubscriptionInfoTestServer(db)
	cfg.GlobalSubURL = "https://customer.example/sub/?key=1&x="
	rr := httptest.NewRecorder()
	srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, "/connect/"+subscriptionInfoTokenA, nil))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, `id="connect-picker"`)
	assert.NotContains(t, body, "://add/")
	assert.NotContains(t, body, "://import/")
	assert.Equal(t, 9, strings.Count(body, "Прямое добавление в "))
	assertConnectionURLAndQR(t, body, cfg.SubURL(subscriptionInfoTokenA))
}

// The rendered pages are fixtures for the jsdom and Playwright tests in
// frontend/, so the browser tests exercise the real Go template output.
// Regenerate with: UPDATE_GOLDEN=1 go test ./internal/web -run ConnectGolden
func TestConnectionPage_ConnectGolden(t *testing.T) {
	expiry := time.Date(2030, time.January, 2, 3, 4, 0, 0, time.UTC)
	for name, status := range map[string]string{"connect_active.golden.html": "active", "connect_expired.golden.html": "expired"} {
		// "expired" with a future date still renders as Истекла (status wins).
		body := renderConnectFixture(t, status, &expiry)
		path := filepath.Join("testdata", name)
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		}
		want, err := os.ReadFile(path)
		require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create %s", path)
		assert.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), body, "%s is stale: run UPDATE_GOLDEN=1 go test ./internal/web -run ConnectGolden", name)
	}
}
