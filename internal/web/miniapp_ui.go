package web

import (
 "embed"
 "io/fs"
 "net/http"
 "strings"
)

//go:embed miniapp_dist
var miniAppFiles embed.FS

// Only static assets are public. Every customer API request still goes through
// the existing initData middleware. Never embed configuration or credentials.
func newMiniAppUIHandler() http.Handler {
 files, _ := fs.Sub(miniAppFiles, "miniapp_dist/public")
 server := http.StripPrefix("/miniapp/", http.FileServer(http.FS(files)))
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Cache-Control", "no-store")
  w.Header().Set("X-Robots-Tag", "noindex, nofollow")
  // Telegram Web hosts Mini Apps in an iframe. Native WebViews are top-level.
  w.Header().Del("X-Frame-Options")
  w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' https://telegram.org; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors https://web.telegram.org https://*.web.telegram.org")
  if r.Method != http.MethodGet && r.Method != http.MethodHead {
   w.Header().Set("Allow", "GET, HEAD")
   http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
   return
  }
  if _, err := fs.Stat(files, "index.html"); err != nil {
   http.Error(w, "Mini App frontend is not built", http.StatusServiceUnavailable)
   return
  }
  path := strings.TrimPrefix(r.URL.Path, "/miniapp/")
  if path != "" && !strings.HasPrefix(path, "assets/") {
   http.NotFound(w, r)
   return
  }
  if path != "" {
   info, err := fs.Stat(files, path)
   if err != nil || info.IsDir() {
    http.NotFound(w, r)
    return
   }
   w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
  }
  server.ServeHTTP(w, r)
 })
}
