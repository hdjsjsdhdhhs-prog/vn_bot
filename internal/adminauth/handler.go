package adminauth

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// Handler owns process-local browser sessions. Construct once per HTTP server;
// restarting the process invalidates every session. It never uses Telegram auth.
type Handler struct {
	enabled      bool
	origin       string
	username     string
	passwordHash []byte
	sessions     sessionStore
	logins       limiter
	bootstraps   limiter
	passwordSlot chan struct{}
	now          func() time.Time
}

// New consumes the existing Admin Config contract. Invalid configuration fails
// closed; absent credentials disable the entire admin namespace (404).
func New(cfg *config.Config) (*Handler, error) {
	enabled, origin, err := cfg.AdminConfiguration()
	if err != nil {
		return nil, err
	}
	h := &Handler{
		enabled: enabled, origin: origin, now: time.Now,
		sessions:     sessionStore{entries: make(map[string]session)},
		logins:       limiter{peers: make(map[string]attemptWindow)},
		bootstraps:   limiter{peers: make(map[string]attemptWindow)},
		passwordSlot: make(chan struct{}, 1),
	}
	if enabled {
		h.username, h.passwordHash = cfg.AdminUsername, []byte(cfg.AdminPasswordHash)
	}
	return h, nil
}

// Routes exposes login/logout/session endpoints and protects all other routes
// through Middleware. Dispatch the admin namespace here before ServeMux can
// normalize paths or redirect requests; include both raw and cleaned paths.
func (h *Handler) Routes(next http.Handler) http.Handler {
	protected := h.Middleware(next)
	logout := h.Middleware(http.HandlerFunc(h.logout))
	current := h.Middleware(http.HandlerFunc(h.current))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		if !h.enabled {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		switch r.URL.Path {
		case "/admin/login":
			switch r.Method {
			case http.MethodGet:
				h.bootstrap(w, r)
			case http.MethodPost:
				h.login(w, r)
			default:
				methodNotAllowed(w, "GET, POST")
			}
		case "/admin/logout":
			logout.ServeHTTP(w, r)
		case "/admin/session":
			current.ServeHTTP(w, r)
		default:
			protected.ServeHTTP(w, r)
		}
	})
}

// Middleware requires a browser session even for OPTIONS and unknown routes.
// Unsafe methods additionally require the configured Origin and a synchronizer
// CSRF token. Rejected requests never extend the session's idle lifetime.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers(w)
		if !h.enabled {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if !h.validOrigin(r, unsafe) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		id := sessionID(r)
		now := h.now()
		entry, ok := h.sessions.get(id, now, "", false, false)
		if !ok || !entry.authenticated {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if _, ok = h.sessions.get(id, now, csrfToken(r), unsafe, true); !ok {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Origin is compared to AdminConfiguration's canonical SITE_URL origin, never
// to Host, Referer or forwarded headers. Browser Origin uses canonical syntax.
func (h *Handler) validOrigin(r *http.Request, required bool) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return !required
	}
	return len(origins) == 1 && origins[0] == h.origin
}

func csrfToken(r *http.Request) string {
	values := r.Header.Values(CSRFHeader)
	if len(values) != 1 || len(values[0]) != 43 {
		return ""
	}
	return values[0]
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	if !h.validOrigin(r, false) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	now := h.now()
	if !h.bootstraps.allow(r, now, 20, 200) {
		rateLimited(w)
		return
	}
	entry, ok := h.sessions.get(sessionID(r), now, "", false, false)
	if !ok {
		id, created, issued := h.sessions.issue("", "", false, now)
		if !issued {
			writeError(w, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		entry = created
		setCookie(w, id)
	}
	writeSession(w, entry)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.validOrigin(r, true) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !h.logins.allow(r, h.now(), 5, 20) {
		rateLimited(w)
		return
	}
	id, csrf := sessionID(r), csrfToken(r)
	if _, ok := h.sessions.get(id, h.now(), csrf, true, false); !ok {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
		return
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	var credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decoder.Decode(&credentials); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if len(credentials.Username) == 0 || len(credentials.Username) > 64 || len(credentials.Password) == 0 || len(credentials.Password) > 72 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Bound expensive bcrypt work independently of IP/session cardinality. Do
	// not queue expensive requests on the resource-constrained application host.
	select {
	case h.passwordSlot <- struct{}{}:
		defer func() { <-h.passwordSlot }()
	default:
		rateLimited(w)
		return
	}
	passwordErr := bcrypt.CompareHashAndPassword(h.passwordHash, []byte(credentials.Password))
	usernameOK := subtle.ConstantTimeCompare([]byte(credentials.Username), []byte(h.username)) == 1
	if passwordErr != nil || !usernameOK {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Recheck expiry and existence after bcrypt; logout/another login wins.
	newID, entry, ok := h.sessions.issue(id, csrf, true, h.now())
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	setCookie(w, newID)
	writeSession(w, entry)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	h.sessions.delete(sessionID(r))
	setCookie(w, "")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) current(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	entry, ok := h.sessions.get(sessionID(r), h.now(), "", false, false)
	if !ok || !entry.authenticated {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeSession(w, entry)
}

func headers(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func writeSession(w http.ResponseWriter, entry session) {
	_ = json.NewEncoder(w).Encode(struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
	}{entry.authenticated, entry.csrf})
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{code})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func rateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "rate_limited")
}
