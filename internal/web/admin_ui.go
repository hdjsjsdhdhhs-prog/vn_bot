package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed admin_dist
var adminUIFiles embed.FS

// adminUIContentTypes pins MIME types for the built assets so responses do not
// depend on the host MIME database (nosniff makes a wrong type fatal).
var adminUIContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
}

// newAdminUIHandler serves the browser admin panel under /admin-ui/. It is
// static only: /admin/login, /admin/session and /admin/api/* stay the sole
// authenticated surface, reached same-origin so browser Origin checks hold.
func newAdminUIHandler() http.Handler {
	files, _ := fs.Sub(adminUIFiles, "admin_dist/public")
	server := http.StripPrefix("/admin-ui/", http.FileServer(http.FS(files)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, err := fs.Stat(files, "index.html"); err != nil {
			http.Error(w, "Admin frontend is not built", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/admin-ui/")
		if name != "" && !strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		if name == "" {
			w.Header().Set("Content-Type", adminUIContentTypes[".html"])
		} else {
			info, err := fs.Stat(files, name)
			if err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
			if contentType, ok := adminUIContentTypes[path.Ext(name)]; ok {
				w.Header().Set("Content-Type", contentType)
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		server.ServeHTTP(w, r)
	})
}
