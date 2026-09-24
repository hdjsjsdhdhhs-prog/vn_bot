package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminUI_EmbeddedProductionAssets(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/admin-ui/", newAdminUIHandler())
	handler := SecurityHeadersMiddleware(mux)
	request := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, path, nil))
		return rr
	}

	rr := request("GET", "/admin-ui/")
	require.Equal(t, 200, rr.Code, "run npm --prefix frontend run build:admin before testing embedded admin UI")
	assert.Contains(t, rr.Body.String(), "<title>RS8 Admin</title>")
	assert.Equal(t, "text/html; charset=utf-8", rr.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	assert.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
	assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, rr.Header().Get("Content-Security-Policy"), "connect-src 'self'")
	assert.Contains(t, rr.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")

	// Vite builds the admin with base './', so assets resolve under /admin-ui/.
	assets := regexp.MustCompile(`(?:src|href)="\./(assets/[^" ]+)"`).FindAllStringSubmatch(rr.Body.String(), -1)
	require.GreaterOrEqual(t, len(assets), 2)
	wantTypes := map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}
	for _, asset := range assets {
		response := request("GET", "/admin-ui/"+asset[1])
		require.Equal(t, 200, response.Code, asset[1])
		assert.Equal(t, "public, max-age=31536000, immutable", response.Header().Get("Cache-Control"))
		assert.Equal(t, wantTypes[asset[1][strings.LastIndex(asset[1], "."):]], response.Header().Get("Content-Type"), asset[1])
		assert.NotEmpty(t, response.Body.Bytes())
	}

	redirect := request("GET", "/admin-ui")
	assert.Equal(t, http.StatusMovedPermanently, redirect.Code)
	assert.Equal(t, "/admin-ui/", redirect.Header().Get("Location"))

	assert.Empty(t, request("HEAD", "/admin-ui/").Body.String())
	assert.Equal(t, 405, request("POST", "/admin-ui/").Code)
	for _, path := range []string{"/admin-ui/README.txt", "/admin-ui/assets/", "/admin-ui/assets/not-found.js", "/admin-ui/src/main.ts", "/admin-ui/index.html"} {
		assert.Equal(t, 404, request("GET", path).Code, path)
	}
	assert.NotEqual(t, 200, request("GET", "/admin-ui/assets/../../admin_ui.go").Code, "traversal must not escape the embedded FS")
}

// The UI lives beside, not under, the /admin guard: the production router must
// serve it statically while /admin/* keeps answering through browser auth.
func TestAdminUI_ProductionRouterKeepsAdminGuard(t *testing.T) {
	s := startAdminTestServer(t, true)
	serve := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		return w
	}
	ui := serve("/admin-ui/")
	require.Equal(t, http.StatusOK, ui.Code)
	assert.Contains(t, ui.Body.String(), "<title>RS8 Admin</title>")
	redirect := serve("/admin-ui")
	assert.Equal(t, http.StatusMovedPermanently, redirect.Code)
	assert.Equal(t, "/admin-ui/", redirect.Header().Get("Location"))
	assert.Equal(t, http.StatusUnauthorized, serve("/admin/session").Code)
}
