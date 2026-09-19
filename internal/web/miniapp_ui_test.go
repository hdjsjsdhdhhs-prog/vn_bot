package web

import (
 "net/http"
 "net/http/httptest"
 "regexp"
 "testing"

 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestMiniAppUI_EmbeddedProductionAssets(t *testing.T) {
 mux := http.NewServeMux()
 mux.Handle("/miniapp/", newMiniAppUIHandler())
 handler := SecurityHeadersMiddleware(mux)
 request := func(method, path string) *httptest.ResponseRecorder {
  t.Helper()
  rr := httptest.NewRecorder()
  handler.ServeHTTP(rr, httptest.NewRequest(method, path, nil))
  return rr
 }
 rr := request("GET", "/miniapp/")
 require.Equal(t, 200, rr.Code, "run npm --prefix frontend run build before testing embedded production UI")
 assert.Contains(t, rr.Body.String(), "https://telegram.org/js/telegram-web-app.js")
 assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
 assert.Equal(t, "no-referrer", rr.Header().Get("Referrer-Policy"))
 assert.Empty(t, rr.Header().Get("X-Frame-Options"))
 assert.Contains(t, rr.Header().Get("Content-Security-Policy"), "frame-ancestors https://web.telegram.org https://*.web.telegram.org")
 assert.Contains(t, rr.Header().Get("Content-Security-Policy"), "connect-src 'self'")
 assets := regexp.MustCompile(`(?:src|href)="(/miniapp/assets/[^" ]+)"`).FindAllStringSubmatch(rr.Body.String(), -1)
 require.GreaterOrEqual(t, len(assets), 2)
 for _, asset := range assets {
  response := request("GET", asset[1])
  require.Equal(t, 200, response.Code)
  assert.Equal(t, "public, max-age=31536000, immutable", response.Header().Get("Cache-Control"))
  assert.NotEmpty(t, response.Body.Bytes())
 }
 assert.Empty(t, request("HEAD", "/miniapp/").Body.String())
 assert.Equal(t, 405, request("POST", "/miniapp/").Code)
 for _, path := range []string{"/miniapp/README.txt", "/miniapp/assets/", "/miniapp/assets/not-found.js", "/miniapp/source.ts", "/miniapp/index.html"} {
  assert.Equal(t, 404, request("GET", path).Code, path)
 }
 assert.Equal(t, "DENY", request("GET", "/other").Header().Get("X-Frame-Options"), "framing policy must not relax other endpoints")
}
