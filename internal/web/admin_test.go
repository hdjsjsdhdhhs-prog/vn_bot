package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/adminauth"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// adminTestPassword is the browser-admin password shared by every admin test.
const adminTestPassword = "test-only-password"

func startAdminTestServer(t *testing.T, enabled bool) *Server {
	t.Helper()
	return startAdminTestServerWith(t, enabled, nil)
}

// startAdminTestServerWith wires an optional AdminService before Start so the
// /admin/api handler is installed exactly as in production.
func startAdminTestServerWith(t *testing.T, enabled bool, svc *service.AdminService) *Server {
	t.Helper()
	cfg := &config.Config{}
	if enabled {
		hash, err := bcrypt.GenerateFromPassword([]byte(adminTestPassword), config.AdminMinBcryptCost)
		require.NoError(t, err)
		cfg.AdminUsername = "admin"
		cfg.AdminPasswordHash = string(hash)
		cfg.SiteURL = "https://admin.example.com"
	}
	s := NewServer("127.0.0.1:0", nil, cfg, "", nil, nil)
	if svc != nil {
		s.SetAdminService(svc)
	}
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, s.Stop(ctx))
	})
	return s
}

func TestAdminRoutesBeforeServeMux(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "enabled"
		if !enabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			s := startAdminTestServer(t, enabled)
			for _, target := range []string{
				"/admin", "/admin/", "/admin/private", "/admin/session", "/admin/logout",
				"/admin//private", "/admin/./private", "/admin/../healthz", "//admin/private",
				"/public/../admin/private", "/admin//login", "/%61dmin/private",
				"/admin%2fprivate", "/admin/%2e%2e/healthz", "/public/%2e%2e/admin/private",
			} {
				for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE", "CONNECT"} {
					t.Run(method+target, func(t *testing.T) {
						// Use origin-form so CONNECT also tests an admin path,
						// rather than parsing an absolute URL as an authority.
						r := httptest.NewRequest(method, target, nil)
						r.Header.Set("Origin", "https://admin.example.com")
						w := httptest.NewRecorder()
						s.server.Handler.ServeHTTP(w, r)
						status := http.StatusUnauthorized
						if !enabled {
							status = http.StatusNotFound
						}
						require.Equal(t, status, w.Code)
						require.Empty(t, w.Header().Get("Location"))
						require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
						require.Equal(t, "noindex, nofollow", w.Header().Get("X-Robots-Tag"))
						require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
					})
				}
			}
			// The admin guard must not capture neighboring public namespaces.
			for _, target := range []string{"/healthz", "/administrator", "/admin-other"} {
				w := httptest.NewRecorder()
				s.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
				status := http.StatusNotFound
				if target == "/healthz" {
					status = http.StatusOK
				}
				require.Equal(t, status, w.Code)
			}
		})
	}
}

func TestAdminConnectRouteThroughHTTP(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name, status := "enabled", http.StatusUnauthorized
		if !enabled {
			name, status = "disabled", http.StatusNotFound
		}
		t.Run(name, func(t *testing.T) {
			s := startAdminTestServer(t, enabled)
			for _, target := range []string{"/admin", "/admin/private", "/admin/../healthz"} {
				t.Run(target, func(t *testing.T) {
					conn, err := net.DialTimeout("tcp", s.Addr(), 5*time.Second)
					require.NoError(t, err)
					defer conn.Close()
					require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
					// Write the request line directly: a CONNECT transport may use
					// authority-form instead, which does not exercise an admin path.
					_, err = io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: admin.example.com\r\nOrigin: https://admin.example.com\r\nConnection: close\r\n\r\n")
					require.NoError(t, err)
					resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
					require.NoError(t, err)
					defer resp.Body.Close()
					require.Equal(t, status, resp.StatusCode)
					require.Empty(t, resp.Header.Get("Location"))
					require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
					require.Equal(t, "noindex, nofollow", resp.Header.Get("X-Robots-Tag"))
					require.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
				})
			}
		})
	}
}

func TestAdminBrowserSessionThroughWebStack(t *testing.T) {
	s := startAdminTestServer(t, true)
	// Exercise real HTTP and the browser's Secure-cookie jar against the exact
	// handler installed by Start, including security headers and metrics.
	tls := httptest.NewTLSServer(s.server.Handler)
	defer tls.Close()
	client := tls.Client()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client.Jar = jar
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	csrf := ""
	send := func(method, path, body string, token bool, status int) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, tls.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		r.Header.Set("Origin", s.cfg.SiteURL)
		r.Header.Set("Content-Type", "application/json")
		if token {
			r.Header.Set(adminauth.CSRFHeader, csrf)
		}
		resp, err := client.Do(r)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		require.Equal(t, status, resp.StatusCode)
		require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
		return resp
	}
	readSession := func(resp *http.Response, authenticated bool) {
		t.Helper()
		defer resp.Body.Close()
		var payload struct {
			Authenticated bool   `json:"authenticated"`
			CSRF          string `json:"csrf_token"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
		require.Equal(t, authenticated, payload.Authenticated)
		require.Len(t, payload.CSRF, 43)
		csrf = payload.CSRF
	}
	readSession(send("GET", "/admin/login", "", false, 200), false)
	oldCSRF := csrf
	readSession(send("POST", "/admin/login", `{"username":"admin","password":"`+adminTestPassword+`"}`, true, 200), true)
	require.NotEqual(t, oldCSRF, csrf)
	readSession(send("GET", "/admin/session", "", false, 200), true)
	for _, target := range []string{"/admin", "/admin/private", "/admin//private", "/admin/../healthz", "/public/../admin/private"} {
		resp := send("GET", target, "", false, 404)
		require.Empty(t, resp.Header.Get("Location"))
		resp.Body.Close()
		send("POST", target, "", false, 403).Body.Close()
		send("POST", target, "", true, 404).Body.Close()
	}
	send("POST", "/admin/logout", "", false, 403).Body.Close()
	send("POST", "/admin/logout", "", true, 204).Body.Close()
	send("GET", "/admin/session", "", false, 401).Body.Close()
}

func TestAdminInvalidConfigurationPreventsStart(t *testing.T) {
	s := NewServer("127.0.0.1:0", nil, &config.Config{AdminUsername: "admin"}, "", nil, nil)
	err := s.Start(context.Background())
	require.ErrorIs(t, err, config.ErrAdminConfiguration)
	require.Nil(t, s.server)
}

// ---------------------------------------------------------------------------
// /admin/api read-only routes
// ---------------------------------------------------------------------------

const (
	adminAPISecondTelegramID = testutil.DefaultTelegramID + 1
	adminAPIForeignOrigin    = "https://evil.example.com"
)

// adminAPIFixture drives /admin/api through the exact handler installed by
// Start, over real TLS so the browser cookie jar accepts the Secure __Host-
// cookie. The repository is the real SQLite service seeded with two linked
// customers (one active, one paused) and one anonymous trial. The AdminService
// has no SubscriptionService/SyncService: the routes under test are read-only
// and must not reach either. HTTP contract only — lifecycle rules and read
// models are covered by the database and service packages.
type adminAPIFixture struct {
	t      *testing.T
	s      *Server
	db     *database.Service
	tls    *httptest.Server
	client *http.Client // browser: cookie jar, no redirects
	anon   *http.Client // no cookie jar: never authenticated
	csrf   string
	active *database.Subscription
	paused *database.Subscription
	trial  *database.Subscription
}

func newAdminAPIFixture(t *testing.T) *adminAPIFixture {
	t.Helper()
	ctx := context.Background()
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	f := &adminAPIFixture{t: t, db: db}
	expiry := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	f.active = testutil.CreateTestSubscription(testutil.DefaultTelegramID, testutil.DefaultUsername, string(database.SubscriptionStatusActive), &expiry)
	require.NoError(t, db.CreateSubscription(ctx, f.active, ""))
	f.paused = testutil.CreateTestSubscription(adminAPISecondTelegramID, "second", string(database.SubscriptionStatusPaused), nil)
	require.NoError(t, db.CreateSubscription(ctx, f.paused, ""))
	f.trial, err = db.CreateTrialSubscription(ctx, "", "trial-sub", "trial-client", expiry)
	require.NoError(t, err)
	require.Negative(t, f.trial.TelegramID)

	f.attach(startAdminTestServerWith(t, true, service.NewAdminService(db, nil, nil)))
	return f
}

// attach exposes the started server over TLS and builds the two clients.
// httptest.Server.Client returns one shared *http.Client, so both are copies:
// the jar must not leak into the anonymous client.
func (f *adminAPIFixture) attach(s *Server) {
	f.t.Helper()
	f.s = s
	f.tls = httptest.NewTLSServer(s.server.Handler)
	f.t.Cleanup(f.tls.Close)
	noRedirect := func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	browser := *f.tls.Client()
	jar, err := cookiejar.New(nil)
	require.NoError(f.t, err)
	browser.Jar, browser.CheckRedirect = jar, noRedirect
	f.client = &browser

	anon := *f.tls.Client()
	anon.CheckRedirect = noRedirect
	f.anon = &anon
}

// newBrowser returns a second browser against the same server and database:
// its own cookie jar and CSRF token, no session yet.
func (f *adminAPIFixture) newBrowser() *adminAPIFixture {
	f.t.Helper()
	other := *f
	browser := *f.tls.Client()
	jar, err := cookiejar.New(nil)
	require.NoError(f.t, err)
	browser.Jar, browser.CheckRedirect = jar, f.client.CheckRedirect
	other.client, other.csrf = &browser, ""
	return &other
}

// seedAudit inserts an audit row directly: mutations are out of scope here, the
// audit routes only need rows to page over.
func (f *adminAPIFixture) seedAudit(subscriptionID uint, key string, success bool) database.AdminAuditLog {
	f.t.Helper()
	entry := database.AdminAuditLog{
		Actor: f.s.cfg.AdminUsername, Action: string(database.AdminActionRenew), SubscriptionID: subscriptionID,
		CreatedAt: time.Now().UTC(), OldValue: "{}", NewValue: "{}", Success: success,
		RequestKey: key, RequestHash: "hash-" + key,
	}
	if !success {
		entry.ErrorCode = database.AdminErrorCodeInvalidState
	}
	require.NoError(f.t, f.db.GetDB().Create(&entry).Error)
	return entry
}

type adminRequestOption func(*http.Request)

func withOrigin(origin string) adminRequestOption {
	return func(r *http.Request) {
		r.Header.Del("Origin")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
	}
}

func withHeader(name, value string) adminRequestOption {
	return func(r *http.Request) { r.Header.Add(name, value) }
}

func withJSON(body string) adminRequestOption {
	return func(r *http.Request) {
		r.Body = io.NopCloser(strings.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/json")
	}
}

// withContentType replaces the media type set by withJSON.
func withContentType(value string) adminRequestOption {
	return func(r *http.Request) { r.Header.Set("Content-Type", value) }
}

// withCSRF attaches the fixture's current synchronizer token.
func (f *adminAPIFixture) withCSRF() adminRequestOption {
	return func(r *http.Request) { r.Header.Set(adminauth.CSRFHeader, f.csrf) }
}

// do sends a request as the browser client. The configured Origin is attached
// by default (browsers send it on every fetch); options may override it.
func (f *adminAPIFixture) do(method, path string, opts ...adminRequestOption) *http.Response {
	f.t.Helper()
	return f.doAs(f.client, method, path, opts...)
}

func (f *adminAPIFixture) doAs(client *http.Client, method, path string, opts ...adminRequestOption) *http.Response {
	f.t.Helper()
	r, err := http.NewRequest(method, f.tls.URL+path, nil)
	require.NoError(f.t, err)
	r.Header.Set("Origin", f.s.cfg.SiteURL)
	for _, opt := range opts {
		opt(r)
	}
	resp, err := client.Do(r)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { resp.Body.Close() })
	// Every admin response, success or failure, is uncacheable JSON.
	assert.Equal(f.t, "no-store", resp.Header.Get("Cache-Control"), "%s %s", method, path)
	assert.Equal(f.t, "application/json", resp.Header.Get("Content-Type"), "%s %s", method, path)
	assert.Empty(f.t, resp.Header.Get("Location"), "%s %s", method, path)
	return resp
}

// decode reads a JSON body into v and closes it.
func (f *adminAPIFixture) decode(resp *http.Response, v any) {
	f.t.Helper()
	defer resp.Body.Close()
	require.NoError(f.t, json.NewDecoder(resp.Body).Decode(v))
}

// raw returns the body as a generic map so tests can assert on the presence
// (or, more importantly, absence) of wire keys.
func (f *adminAPIFixture) raw(resp *http.Response) map[string]any {
	f.t.Helper()
	var payload map[string]any
	f.decode(resp, &payload)
	return payload
}

// expectError asserts the status and the {"error": code} body.
func (f *adminAPIFixture) expectError(resp *http.Response, status int, code string) {
	f.t.Helper()
	require.Equal(f.t, status, resp.StatusCode)
	payload := f.raw(resp)
	require.Equal(f.t, code, payload["error"])
	require.Len(f.t, payload, 1, "error responses carry nothing but the code")
}

// session decodes a login/session payload and records the CSRF token.
func (f *adminAPIFixture) session(resp *http.Response, authenticated bool) {
	f.t.Helper()
	require.Equal(f.t, http.StatusOK, resp.StatusCode)
	var payload struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	f.decode(resp, &payload)
	require.Equal(f.t, authenticated, payload.Authenticated)
	require.Len(f.t, payload.CSRF, 43)
	f.csrf = payload.CSRF
}

// cookie returns the admin session cookie the browser jar currently holds.
func (f *adminAPIFixture) cookie() *http.Cookie {
	f.t.Helper()
	base, err := http.NewRequest(http.MethodGet, f.tls.URL+"/admin/session", nil)
	require.NoError(f.t, err)
	for _, c := range f.client.Jar.Cookies(base.URL) {
		if c.Name == adminauth.CookieName {
			return c
		}
	}
	return nil
}

// login bootstraps a session and authenticates it.
func (f *adminAPIFixture) login() {
	f.t.Helper()
	f.session(f.do(http.MethodGet, "/admin/login"), false)
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, f.s.cfg.AdminUsername, adminTestPassword)
	f.session(f.do(http.MethodPost, "/admin/login", f.withCSRF(), withJSON(body)), true)
}

func (f *adminAPIFixture) subscriptionPath(id uint) string {
	return "/admin/api/subscriptions/" + strconv.FormatUint(uint64(id), 10)
}

func (f *adminAPIFixture) userPath(telegramID int64) string {
	return "/admin/api/users/" + strconv.FormatInt(telegramID, 10)
}

// auditCount is the guard that rejected requests never reached the service.
func (f *adminAPIFixture) auditCount() int64 {
	f.t.Helper()
	var n int64
	require.NoError(f.t, f.db.GetDB().Model(&database.AdminAuditLog{}).Count(&n).Error)
	return n
}

var adminAPIReadRoutes = []string{"/admin/api/dashboard", "/admin/api/users", "/admin/api/audit"}

// 1. authenticated / unauthorized
func TestAdminAPI_RequiresAuthenticatedSession(t *testing.T) {
	f := newAdminAPIFixture(t)
	routes := append([]string{f.subscriptionPath(f.active.ID), f.userPath(f.active.TelegramID)}, adminAPIReadRoutes...)

	t.Run("no cookie", func(t *testing.T) {
		for _, path := range routes {
			f.expectError(f.doAs(f.anon, http.MethodGet, path), http.StatusUnauthorized, "unauthorized")
			// HEAD responses never carry a body, so only the status is on the wire.
			head := f.doAs(f.anon, http.MethodHead, path)
			require.Equal(t, http.StatusUnauthorized, head.StatusCode, path)
			body, err := io.ReadAll(head.Body)
			require.NoError(t, err)
			require.Empty(t, body, "HEAD carries no body")
		}
		// Mutation routes are unauthorized before any CSRF or body handling.
		f.expectError(f.doAs(f.anon, http.MethodPost, f.subscriptionPath(f.active.ID)+"/renew", withJSON(`{}`)), http.StatusUnauthorized, "unauthorized")
	})

	t.Run("forged cookie", func(t *testing.T) {
		forged := strings.Repeat("A", 43)
		for _, path := range routes {
			resp := f.doAs(f.anon, http.MethodGet, path, withHeader("Cookie", adminauth.CookieName+"="+forged))
			f.expectError(resp, http.StatusUnauthorized, "unauthorized")
		}
	})

	t.Run("bootstrapped but not logged in", func(t *testing.T) {
		f.session(f.do(http.MethodGet, "/admin/login"), false)
		require.NotNil(t, f.cookie(), "bootstrap issues the session cookie")
		for _, path := range routes {
			f.expectError(f.do(http.MethodGet, path), http.StatusUnauthorized, "unauthorized")
		}
		// A valid CSRF token does not substitute for authentication.
		f.expectError(f.do(http.MethodPost, f.subscriptionPath(f.active.ID)+"/renew", f.withCSRF(), withJSON(`{}`)), http.StatusUnauthorized, "unauthorized")
		f.expectError(f.do(http.MethodGet, "/admin/session"), http.StatusUnauthorized, "unauthorized")
	})

	t.Run("authenticated", func(t *testing.T) {
		f.login()
		for _, path := range routes {
			resp := f.do(http.MethodGet, path)
			require.Equal(t, http.StatusOK, resp.StatusCode, path)
			resp.Body.Close()
		}
	})

	require.Zero(t, f.auditCount(), "no rejected request may reach the service")
}

// 2. login / logout
func TestAdminAPI_LoginLogoutLifecycle(t *testing.T) {
	f := newAdminAPIFixture(t)

	// Bootstrap: anonymous session with a CSRF token and a Secure cookie.
	bootstrap := f.do(http.MethodGet, "/admin/login")
	f.session(bootstrap, false)
	cookie := f.cookie()
	require.NotNil(t, cookie)
	require.Len(t, cookie.Value, 43)
	bootstrapCSRF, bootstrapCookie := f.csrf, cookie.Value

	// Repeating the bootstrap with a live session keeps it (same token).
	f.session(f.do(http.MethodGet, "/admin/login"), false)
	require.Equal(t, bootstrapCSRF, f.csrf)
	require.Equal(t, bootstrapCookie, f.cookie().Value)

	// Login rotates both the cookie and the CSRF token.
	f.login()
	require.NotEqual(t, bootstrapCSRF, f.csrf)
	loginCookie := f.cookie().Value
	require.NotEqual(t, bootstrapCookie, loginCookie)
	// The pre-login session is gone: its cookie no longer resolves.
	f.expectError(f.doAs(f.anon, http.MethodGet, "/admin/session", withHeader("Cookie", adminauth.CookieName+"="+bootstrapCookie)), http.StatusUnauthorized, "unauthorized")

	// The session endpoint reports the authenticated state and the same token.
	loginCSRF := f.csrf
	f.session(f.do(http.MethodGet, "/admin/session"), true)
	require.Equal(t, loginCSRF, f.csrf)

	// The API is reachable now.
	resp := f.do(http.MethodGet, "/admin/api/dashboard")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Logout requires CSRF (unsafe method) and POST.
	f.expectError(f.do(http.MethodPost, "/admin/logout"), http.StatusForbidden, "forbidden")
	f.expectError(f.do(http.MethodGet, "/admin/logout"), http.StatusMethodNotAllowed, "method_not_allowed")
	resp = f.do(http.MethodPost, "/admin/logout", f.withCSRF())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()
	require.Nil(t, f.cookie(), "logout clears the cookie")

	// Everything is locked again; the old cookie is gone server-side too, so
	// replaying it from another client does not resurrect the session.
	f.expectError(f.do(http.MethodGet, "/admin/session"), http.StatusUnauthorized, "unauthorized")
	f.expectError(f.do(http.MethodGet, "/admin/api/dashboard"), http.StatusUnauthorized, "unauthorized")
	f.expectError(f.doAs(f.anon, http.MethodGet, "/admin/api/dashboard", withHeader("Cookie", adminauth.CookieName+"="+loginCookie)), http.StatusUnauthorized, "unauthorized")
	f.expectError(f.doAs(f.anon, http.MethodPost, "/admin/logout", withHeader("Cookie", adminauth.CookieName+"="+loginCookie), withHeader(adminauth.CSRFHeader, loginCSRF)), http.StatusUnauthorized, "unauthorized")
	// A second logout from the browser has no session to present: 401, not 204.
	f.expectError(f.do(http.MethodPost, "/admin/logout", f.withCSRF()), http.StatusUnauthorized, "unauthorized")
}

// 2b. login rejections. The login limiter allows five attempts per peer per
// minute and every fixture owns a fresh handler; this test spends exactly five.
func TestAdminAPI_LoginRejections(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.session(f.do(http.MethodGet, "/admin/login"), false)
	valid := fmt.Sprintf(`{"username":%q,"password":%q}`, f.s.cfg.AdminUsername, adminTestPassword)

	// Rejected before the limiter: Origin is checked first.
	f.expectError(f.do(http.MethodPost, "/admin/login", f.withCSRF(), withJSON(valid), withOrigin(adminAPIForeignOrigin)), http.StatusForbidden, "forbidden")
	f.expectError(f.do(http.MethodPost, "/admin/login", f.withCSRF(), withJSON(valid), withOrigin("")), http.StatusForbidden, "forbidden")

	// Five limiter-counted attempts, each rejected at a different stage.
	attempts := []struct {
		name   string
		opts   []adminRequestOption
		status int
		code   string
	}{
		{"no CSRF token", []adminRequestOption{withJSON(valid)}, http.StatusForbidden, "forbidden"},
		{"wrong media type", []adminRequestOption{f.withCSRF(), withJSON(valid), withContentType("text/plain")}, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"unknown JSON field", []adminRequestOption{f.withCSRF(), withJSON(`{"username":"admin","password":"` + adminTestPassword + `","extra":1}`)}, http.StatusBadRequest, "invalid_request"},
		{"wrong password", []adminRequestOption{f.withCSRF(), withJSON(`{"username":"admin","password":"wrong-password"}`)}, http.StatusUnauthorized, "unauthorized"},
		{"wrong username", []adminRequestOption{f.withCSRF(), withJSON(`{"username":"nobody","password":"` + adminTestPassword + `"}`)}, http.StatusUnauthorized, "unauthorized"},
	}
	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			f.expectError(f.do(http.MethodPost, "/admin/login", attempt.opts...), attempt.status, attempt.code)
		})
	}

	// Sixth attempt is rate-limited even with valid credentials.
	resp := f.do(http.MethodPost, "/admin/login", f.withCSRF(), withJSON(valid))
	require.Equal(t, "60", resp.Header.Get("Retry-After"))
	f.expectError(resp, http.StatusTooManyRequests, "rate_limited")

	// None of the above authenticated the session.
	f.expectError(f.do(http.MethodGet, "/admin/session"), http.StatusUnauthorized, "unauthorized")
	f.expectError(f.do(http.MethodGet, "/admin/api/dashboard"), http.StatusUnauthorized, "unauthorized")
}

// 3. CSRF rejection
func TestAdminAPI_CSRFRejection(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()
	target := f.subscriptionPath(f.active.ID) + "/renew"
	body := withJSON(`{"request_key":"csrf-test","days":1}`)

	// Every unsafe method needs the synchronizer token in the header, exactly
	// once, with the exact value. Nothing else is a token source.
	cases := []struct {
		name   string
		method string
		path   string
		opts   []adminRequestOption
	}{
		{"missing token", http.MethodPost, target, []adminRequestOption{body}},
		{"empty token", http.MethodPost, target, []adminRequestOption{body, withHeader(adminauth.CSRFHeader, "")}},
		{"truncated token", http.MethodPost, target, []adminRequestOption{body, withHeader(adminauth.CSRFHeader, f.csrf[:42])}},
		{"padded token", http.MethodPost, target, []adminRequestOption{body, withHeader(adminauth.CSRFHeader, f.csrf+"A")}},
		{"foreign token", http.MethodPost, target, []adminRequestOption{body, withHeader(adminauth.CSRFHeader, strings.Repeat("B", 43))}},
		{"duplicated header", http.MethodPost, target, []adminRequestOption{body, withHeader(adminauth.CSRFHeader, f.csrf), withHeader(adminauth.CSRFHeader, f.csrf)}},
		{"token in query string", http.MethodPost, target + "?" + adminauth.CSRFHeader + "=" + f.csrf, []adminRequestOption{body}},
		{"token as cookie", http.MethodPost, target, []adminRequestOption{body, withHeader("Cookie", adminauth.CSRFHeader+"="+f.csrf)}},
		{"token in body", http.MethodPost, target, []adminRequestOption{withJSON(`{"request_key":"csrf-test","days":1,"csrf_token":"` + f.csrf + `"}`)}},
		{"PUT on read route", http.MethodPut, "/admin/api/dashboard", nil},
		{"PATCH on read route", http.MethodPatch, "/admin/api/users", nil},
		{"DELETE on detail route", http.MethodDelete, f.subscriptionPath(f.active.ID), nil},
		{"POST on logout", http.MethodPost, "/admin/logout", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.expectError(f.do(tc.method, tc.path, tc.opts...), http.StatusForbidden, "forbidden")
		})
	}

	t.Run("OPTIONS is safe", func(t *testing.T) {
		// OPTIONS needs no token: the middleware passes it, and the route's own
		// method check answers 405 without touching the service.
		resp := f.do(http.MethodOptions, "/admin/api/dashboard")
		require.Equal(t, "GET, HEAD", resp.Header.Get("Allow"))
		f.expectError(resp, http.StatusMethodNotAllowed, "method_not_allowed")
	})

	t.Run("token from another session", func(t *testing.T) {
		// A second browser logs in to the same server and obtains its own
		// token; presenting it with the first browser's cookie must fail.
		other := f.newBrowser()
		other.login()
		require.NotEqual(t, f.csrf, other.csrf)
		f.expectError(f.do(http.MethodPost, target, body, withHeader(adminauth.CSRFHeader, other.csrf)), http.StatusForbidden, "forbidden")
	})

	// A CSRF rejection must not extend or destroy the session, and the correct
	// token still works afterwards (proving the rejections were about CSRF).
	f.session(f.do(http.MethodGet, "/admin/session"), true)
	f.expectError(f.do(http.MethodPost, target, f.withCSRF(), withJSON(`not json`)), http.StatusBadRequest, "invalid_request")
	require.Zero(t, f.auditCount(), "CSRF-rejected mutations never reach the service")
}

// 4. invalid Origin
func TestAdminAPI_InvalidOrigin(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()
	renew := f.subscriptionPath(f.active.ID) + "/renew"
	body := withJSON(`{"request_key":"origin-test","days":1}`)

	foreign := []string{
		adminAPIForeignOrigin,
		"http://admin.example.com",       // scheme downgrade
		"https://admin.example.com:8443", // explicit port
		"https://admin.example.com:443",  // browsers omit the default port; an explicit one is a different string
		"https://admin.example.com/",     // trailing slash is not canonical Origin syntax
		"https://ADMIN.example.com",      // case is not normalized: browsers send lowercase
		"https://admin.example.com.evil.com",
		"null",
	}
	for _, origin := range foreign {
		t.Run("unsafe "+origin, func(t *testing.T) {
			f.expectError(f.do(http.MethodPost, renew, f.withCSRF(), body, withOrigin(origin)), http.StatusForbidden, "forbidden")
		})
		t.Run("safe "+origin, func(t *testing.T) {
			// A mismatching Origin is rejected even on GET: the header, once
			// present, must be the configured one.
			f.expectError(f.do(http.MethodGet, "/admin/api/dashboard", withOrigin(origin)), http.StatusForbidden, "forbidden")
		})
	}

	t.Run("missing Origin on unsafe method", func(t *testing.T) {
		f.expectError(f.do(http.MethodPost, renew, f.withCSRF(), body, withOrigin("")), http.StatusForbidden, "forbidden")
		f.expectError(f.do(http.MethodPost, "/admin/logout", f.withCSRF(), withOrigin("")), http.StatusForbidden, "forbidden")
	})

	t.Run("multiple Origin headers", func(t *testing.T) {
		f.expectError(f.do(http.MethodGet, "/admin/api/dashboard", withHeader("Origin", f.s.cfg.SiteURL)), http.StatusForbidden, "forbidden")
		f.expectError(f.do(http.MethodPost, renew, f.withCSRF(), body, withHeader("Origin", f.s.cfg.SiteURL)), http.StatusForbidden, "forbidden")
	})

	t.Run("Sec-Fetch-Site cross-site", func(t *testing.T) {
		// Fetch metadata is decisive even when Origin looks right or is absent.
		f.expectError(f.do(http.MethodGet, "/admin/api/dashboard", withHeader("Sec-Fetch-Site", "cross-site")), http.StatusForbidden, "forbidden")
		f.expectError(f.do(http.MethodGet, "/admin/api/dashboard", withOrigin(""), withHeader("Sec-Fetch-Site", "cross-site")), http.StatusForbidden, "forbidden")
		f.expectError(f.do(http.MethodPost, renew, f.withCSRF(), body, withHeader("Sec-Fetch-Site", "cross-site")), http.StatusForbidden, "forbidden")
	})

	t.Run("bootstrap and session", func(t *testing.T) {
		f.expectError(f.doAs(f.anon, http.MethodGet, "/admin/login", withOrigin(adminAPIForeignOrigin)), http.StatusForbidden, "forbidden")
		f.expectError(f.do(http.MethodGet, "/admin/session", withOrigin(adminAPIForeignOrigin)), http.StatusForbidden, "forbidden")
	})

	// Origin rejections leave the session intact and never reach the service.
	f.session(f.do(http.MethodGet, "/admin/session"), true)
	require.Zero(t, f.auditCount())
}

// 5. valid Origin
func TestAdminAPI_ValidOrigin(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()

	ok := func(resp *http.Response) {
		t.Helper()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}
	// Exact configured origin, on every read route.
	for _, path := range adminAPIReadRoutes {
		ok(f.do(http.MethodGet, path, withOrigin(f.s.cfg.SiteURL)))
	}
	// Browsers omit Origin on same-origin GET navigations: allowed for safe methods.
	ok(f.do(http.MethodGet, "/admin/api/dashboard", withOrigin("")))
	ok(f.do(http.MethodHead, "/admin/api/dashboard", withOrigin("")))
	// Same-origin fetch metadata is fine alongside the correct Origin.
	ok(f.do(http.MethodGet, "/admin/api/dashboard", withHeader("Sec-Fetch-Site", "same-origin")))
	ok(f.do(http.MethodGet, "/admin/api/dashboard", withOrigin(""), withHeader("Sec-Fetch-Site", "none")))
	// The unsafe path with the right Origin gets past the origin check and is
	// rejected later on its merits (bad body → 400), proving the check passed.
	f.expectError(f.do(http.MethodPost, f.subscriptionPath(f.active.ID)+"/renew", f.withCSRF(), withJSON(`not json`)), http.StatusBadRequest, "invalid_request")
	// Neither Host nor Referer are accepted as a substitute for Origin.
	f.expectError(f.do(http.MethodPost, "/admin/logout", f.withCSRF(), withOrigin(""), withHeader("Referer", f.s.cfg.SiteURL+"/admin/")), http.StatusForbidden, "forbidden")
	f.session(f.do(http.MethodGet, "/admin/session"), true)
}

// 6. HTTP method / path validation
func TestAdminAPI_MethodAndPathValidation(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()
	detail := f.subscriptionPath(f.active.ID)
	user := f.userPath(f.active.TelegramID)

	t.Run("read routes accept GET and HEAD only", func(t *testing.T) {
		for _, path := range append([]string{detail, user}, adminAPIReadRoutes...) {
			resp := f.do(http.MethodHead, path)
			require.Equal(t, http.StatusOK, resp.StatusCode, path)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Empty(t, body, "HEAD carries no body")
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				resp := f.do(method, path, f.withCSRF(), withJSON(`{}`))
				require.Equal(t, "GET, HEAD", resp.Header.Get("Allow"), "%s %s", method, path)
				f.expectError(resp, http.StatusMethodNotAllowed, "method_not_allowed")
			}
			resp = f.do(http.MethodOptions, path)
			require.Equal(t, "GET, HEAD", resp.Header.Get("Allow"))
			f.expectError(resp, http.StatusMethodNotAllowed, "method_not_allowed")
		}
	})

	t.Run("action routes accept POST only", func(t *testing.T) {
		for action := range adminActionRoutes {
			path := detail + "/" + action
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
				resp := f.do(method, path)
				require.Equal(t, "POST", resp.Header.Get("Allow"), "%s %s", method, path)
				require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "%s %s", method, path)
				resp.Body.Close()
			}
			for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
				resp := f.do(method, path, f.withCSRF(), withJSON(`{}`))
				require.Equal(t, "POST", resp.Header.Get("Allow"), "%s %s", method, path)
				f.expectError(resp, http.StatusMethodNotAllowed, "method_not_allowed")
			}
		}
	})

	t.Run("unknown routes are 404 without redirects", func(t *testing.T) {
		unknown := []string{
			"/admin", "/admin/", "/admin/api", "/admin/api/",
			"/admin/api/dashboard/", "/admin/api/dashboards", "/admin/api/Dashboard",
			"/admin/api/users/", "/admin/api/users/" + strconv.FormatInt(f.active.TelegramID, 10) + "/",
			"/admin/api/users/" + strconv.FormatInt(f.active.TelegramID, 10) + "/renew",
			"/admin/api/subscriptions", "/admin/api/subscriptions/", detail + "/",
			detail + "/delete", detail + "/renew/", detail + "/renew/extra",
			"/admin/api/audit/", "/admin/api/audit/1",
			"/admin/api//dashboard", "/admin/api/./dashboard", "/admin/api/../api/dashboard",
			"/admin/API/dashboard", "/admin/apidashboard",
			"/admin/login/", "/admin/session/", "/admin/logout/",
		}
		for _, path := range unknown {
			resp := f.do(http.MethodGet, path)
			f.expectError(resp, http.StatusNotFound, "not_found")
			resp = f.do(http.MethodPost, path, f.withCSRF(), withJSON(`{}`))
			f.expectError(resp, http.StatusNotFound, "not_found")
		}
	})

	t.Run("query strings do not change routing", func(t *testing.T) {
		resp := f.do(http.MethodGet, "/admin/api/dashboard?ignored=1")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
		f.expectError(f.do(http.MethodGet, "/admin/api/nothing?path=/admin/api/dashboard"), http.StatusNotFound, "not_found")
	})

	require.Zero(t, f.auditCount())
}

// 6b. The API without a wired AdminService is session-protected but unavailable.
func TestAdminAPI_ServiceUnavailableWithoutService(t *testing.T) {
	f := &adminAPIFixture{t: t}
	f.attach(startAdminTestServer(t, true))

	// Unauthenticated callers cannot tell whether the API is wired.
	f.expectError(f.doAs(f.anon, http.MethodGet, "/admin/api/dashboard"), http.StatusUnauthorized, "unauthorized")
	f.login()
	for _, path := range adminAPIReadRoutes {
		f.expectError(f.do(http.MethodGet, path), http.StatusServiceUnavailable, "service_unavailable")
	}
	f.expectError(f.do(http.MethodPost, "/admin/api/subscriptions/1/renew", f.withCSRF(), withJSON(`{}`)), http.StatusServiceUnavailable, "service_unavailable")
	// Non-API admin paths stay 404 regardless of the service.
	f.expectError(f.do(http.MethodGet, "/admin"), http.StatusNotFound, "not_found")
}

// adminBearerKeys must never appear in any browser-admin payload. Subscription
// has no JSON tags, so an accidental leak of the raw model would surface the
// Go field names; the view's snake_case spellings are listed too.
var adminBearerKeys = []string{"Token", "token", "ClientID", "client_id", "SubscriptionID", "subscription_id", "Devices", "Ips", "InviteCode", "invite_code"}

// assertSafeSubscriptionView checks the wire shape of one AdminSubscriptionView.
func assertSafeSubscriptionView(t *testing.T, view map[string]any) {
	t.Helper()
	for _, key := range adminBearerKeys {
		assert.NotContains(t, view, key)
	}
	for _, key := range []string{"id", "telegram_id", "username", "status", "expires_at", "plan_id", "is_paid", "price_paid_cents", "created_at", "updated_at", "reminders_sent", "devices", "ips"} {
		assert.Contains(t, view, key)
	}
	// Devices and IPs are counts, never the stored payloads.
	assert.IsType(t, float64(0), view["devices"])
	assert.IsType(t, float64(0), view["ips"])
}

// 7. GET /admin/api/dashboard
func TestAdminAPI_Dashboard(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()
	f.seedAudit(f.active.ID, "dash-1", true)

	resp := f.do(http.MethodGet, "/admin/api/dashboard")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var dashboard database.AdminDashboard
	f.decode(resp, &dashboard)
	// Shape and the seed's headline numbers only; the counters themselves are
	// the database package's contract.
	assert.EqualValues(t, 3, dashboard.TotalSubscriptions)
	assert.EqualValues(t, 2, dashboard.Users)
	assert.EqualValues(t, 1, dashboard.Trials)
	assert.EqualValues(t, 2, dashboard.Active)
	assert.EqualValues(t, 1, dashboard.Paused)
	assert.EqualValues(t, 1, dashboard.AuditLast24h)

	// Wire keys are stable snake_case and the payload is nothing but counters.
	raw := f.raw(f.do(http.MethodGet, "/admin/api/dashboard"))
	for _, key := range []string{"total_subscriptions", "users", "trials", "active", "paused", "revoked", "expired", "canceled", "active_expired", "paid", "expiring_in_7d", "audit_last_24h"} {
		assert.Contains(t, raw, key)
		assert.IsType(t, float64(0), raw[key], key)
	}
	assert.Len(t, raw, 12)
}

// 8. GET /admin/api/users
func TestAdminAPI_Users(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()

	type page struct {
		Users  []map[string]any `json:"users"`
		Total  int64            `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	list := func(query string) page {
		t.Helper()
		resp := f.do(http.MethodGet, "/admin/api/users"+query)
		require.Equal(t, http.StatusOK, resp.StatusCode, query)
		var p page
		f.decode(resp, &p)
		return p
	}
	ids := func(p page) []float64 {
		out := make([]float64, 0, len(p.Users))
		for _, u := range p.Users {
			out = append(out, u["telegram_id"].(float64))
		}
		return out
	}

	t.Run("default page", func(t *testing.T) {
		p := list("")
		require.EqualValues(t, 2, p.Total)
		require.Len(t, p.Users, 2)
		assert.Equal(t, database.AdminDefaultPageSize, p.Limit)
		assert.Zero(t, p.Offset)
		// Newest first; the trial is not a user.
		assert.Equal(t, []float64{float64(adminAPISecondTelegramID), float64(testutil.DefaultTelegramID)}, ids(p))
		for _, u := range p.Users {
			assertSafeSubscriptionView(t, u)
		}
		assert.Equal(t, database.FreePlanName, p.Users[0]["plan_name"])
	})

	t.Run("pagination", func(t *testing.T) {
		first := list("?limit=1")
		require.Len(t, first.Users, 1)
		assert.EqualValues(t, 2, first.Total)
		assert.Equal(t, 1, first.Limit)
		second := list("?limit=1&offset=1")
		require.Len(t, second.Users, 1)
		assert.Equal(t, 1, second.Offset)
		assert.NotEqual(t, ids(first), ids(second))
		empty := list("?offset=5")
		assert.Empty(t, empty.Users)
		assert.EqualValues(t, 2, empty.Total)
		assert.NotNil(t, empty.Users, "users is [] rather than null")
		// limit=0 falls back to the default, oversized limits are clamped.
		assert.Equal(t, database.AdminDefaultPageSize, list("?limit=0").Limit)
		assert.Equal(t, database.AdminMaxPageSize, list("?limit=100000").Limit)
	})

	t.Run("filters", func(t *testing.T) {
		assert.Equal(t, []float64{float64(testutil.DefaultTelegramID)}, ids(list("?q="+testutil.DefaultUsername)))
		assert.Equal(t, []float64{float64(testutil.DefaultTelegramID)}, ids(list("?q=%40"+testutil.DefaultUsername)), "@username")
		assert.Equal(t, []float64{float64(testutil.DefaultTelegramID)}, ids(list("?q="+strconv.FormatInt(testutil.DefaultTelegramID, 10))), "numeric query matches telegram_id")
		assert.Equal(t, []float64{float64(adminAPISecondTelegramID)}, ids(list("?status="+string(database.SubscriptionStatusPaused))))
		assert.Empty(t, list("?status="+string(database.SubscriptionStatusRevoked)).Users)
		assert.Empty(t, list("?q=nobody").Users)
		// A trial cannot be found through the users list, by any key.
		assert.Empty(t, list("?q="+strconv.FormatInt(-f.trial.TelegramID, 10)).Users)
		assert.Empty(t, list("?q=trial").Users)
	})

	t.Run("invalid query parameters", func(t *testing.T) {
		for _, query := range []string{
			"?limit=abc", "?limit=-1", "?limit=1.5", "?limit=1e2", "?limit=%20",
			"?offset=abc", "?offset=-1",
			"?status=bogus", "?status=ACTIVE", "?status=active%00",
			"?q=" + strings.Repeat("a", 129),
		} {
			f.expectError(f.do(http.MethodGet, "/admin/api/users"+query), http.StatusBadRequest, "invalid_request")
		}
		// Boundary: 128 characters is still accepted.
		assert.Empty(t, list("?q="+strings.Repeat("a", 128)).Users)
		// Unknown parameters are ignored, not rejected.
		assert.EqualValues(t, 2, list("?unknown=1").Total)
	})
}

// 9. GET /admin/api/users/{telegramID}
func TestAdminAPI_UserByTelegramID(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()

	t.Run("resolves to the subscription page", func(t *testing.T) {
		resp := f.do(http.MethodGet, f.userPath(f.active.TelegramID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		byUser := f.raw(resp)
		bySubscription := f.raw(f.do(http.MethodGet, f.subscriptionPath(f.active.ID)))
		assert.Equal(t, bySubscription, byUser, "same page as /subscriptions/{id}")
		sub := byUser["subscription"].(map[string]any)
		assert.EqualValues(t, f.active.ID, sub["id"])
		assert.EqualValues(t, f.active.TelegramID, sub["telegram_id"])
		assertSafeSubscriptionView(t, sub)
	})

	t.Run("database id is not a telegram id", func(t *testing.T) {
		// Each seeded row has a small database ID that no customer uses as a
		// Telegram ID; the numeric users query matches both columns, so the
		// route must narrow the match to telegram_id.
		for _, sub := range []*database.Subscription{f.active, f.paused, f.trial} {
			require.NotEqual(t, int64(sub.ID), f.active.TelegramID)
			require.NotEqual(t, int64(sub.ID), f.paused.TelegramID)
			f.expectError(f.do(http.MethodGet, f.userPath(int64(sub.ID))), http.StatusNotFound, "not_found")
		}
	})

	t.Run("trials are not users", func(t *testing.T) {
		f.expectError(f.do(http.MethodGet, f.userPath(f.trial.TelegramID)), http.StatusNotFound, "not_found")
		f.expectError(f.do(http.MethodGet, f.userPath(-f.trial.TelegramID)), http.StatusNotFound, "not_found")
	})

	t.Run("unknown telegram id", func(t *testing.T) {
		f.expectError(f.do(http.MethodGet, f.userPath(testutil.AdminTelegramID)), http.StatusNotFound, "not_found")
	})
}

// 10. GET /admin/api/subscriptions/{id}
func TestAdminAPI_SubscriptionByID(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()
	newest := f.seedAudit(f.active.ID, "detail-1", true)
	f.seedAudit(f.paused.ID, "detail-other", false)

	t.Run("linked customer", func(t *testing.T) {
		resp := f.do(http.MethodGet, f.subscriptionPath(f.active.ID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		raw := f.raw(resp)
		require.ElementsMatch(t, []string{"subscription", "plan", "nodes", "audit"}, mapKeys(raw))

		sub := raw["subscription"].(map[string]any)
		assertSafeSubscriptionView(t, sub)
		assert.EqualValues(t, f.active.ID, sub["id"])
		assert.Equal(t, testutil.DefaultUsername, sub["username"])
		assert.Equal(t, string(database.SubscriptionStatusActive), sub["status"])
		assert.Equal(t, database.FreePlanName, sub["plan_name"])
		assert.Equal(t, false, sub["is_paid"])
		require.NotNil(t, sub["expires_at"])
		expires, err := time.Parse(time.RFC3339, sub["expires_at"].(string))
		require.NoError(t, err)
		assert.True(t, expires.Equal(*f.active.ExpiresAt))

		plan := raw["plan"].(map[string]any)
		assert.Equal(t, database.FreePlanName, plan["name"])
		assert.ElementsMatch(t, []string{"id", "name", "is_active", "devices_limit", "traffic_limit", "subscription_builder_id"}, mapKeys(plan))
		assert.Nil(t, plan["subscription_builder_id"], "no plan default builder assigned: null")
		assert.Contains(t, sub, "subscription_builder_id")
		assert.Nil(t, sub["subscription_builder_id"], "no subscription override assigned: null")

		assert.Equal(t, []any{}, raw["nodes"], "no bindings: [] not null")

		audit := raw["audit"].([]any)
		require.Len(t, audit, 1, "only this subscription's rows")
		entry := audit[0].(map[string]any)
		assert.EqualValues(t, newest.ID, entry["id"])
		assert.Equal(t, newest.RequestKey, entry["request_key"])
		assert.NotContains(t, entry, "request_hash")
		assert.NotContains(t, entry, "RequestHash")
	})

	t.Run("perpetual paused customer", func(t *testing.T) {
		raw := f.raw(f.do(http.MethodGet, f.subscriptionPath(f.paused.ID)))
		sub := raw["subscription"].(map[string]any)
		assert.Equal(t, string(database.SubscriptionStatusPaused), sub["status"])
		assert.Nil(t, sub["expires_at"], "perpetual: explicit null")
		entry := raw["audit"].([]any)[0].(map[string]any)
		assert.Equal(t, false, entry["success"])
		assert.Equal(t, database.AdminErrorCodeInvalidState, entry["error_code"])
	})

	t.Run("unknown id", func(t *testing.T) {
		f.expectError(f.do(http.MethodGet, f.subscriptionPath(f.trial.ID+1000)), http.StatusNotFound, "not_found")
	})
}

// 11. GET /admin/api/audit
func TestAdminAPI_Audit(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()

	type entries struct {
		Entries []database.AdminAuditLog `json:"entries"`
	}
	list := func(query string) []database.AdminAuditLog {
		t.Helper()
		resp := f.do(http.MethodGet, "/admin/api/audit"+query)
		require.Equal(t, http.StatusOK, resp.StatusCode, query)
		var page entries
		f.decode(resp, &page)
		require.NotNil(t, page.Entries, "entries is [] rather than null")
		return page.Entries
	}
	ids := func(rows []database.AdminAuditLog) []uint {
		out := make([]uint, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.ID)
		}
		return out
	}

	assert.Empty(t, list(""), "empty log")

	var seeded []database.AdminAuditLog
	for i := 1; i <= 3; i++ {
		seeded = append(seeded, f.seedAudit(f.active.ID, fmt.Sprintf("audit-active-%d", i), i != 2))
	}
	seeded = append(seeded, f.seedAudit(f.paused.ID, "audit-paused", true))

	t.Run("newest first", func(t *testing.T) {
		got := list("")
		require.Len(t, got, 4)
		assert.Equal(t, []uint{seeded[3].ID, seeded[2].ID, seeded[1].ID, seeded[0].ID}, ids(got))
		assert.Equal(t, seeded[3].RequestKey, got[0].RequestKey)
		assert.Equal(t, f.s.cfg.AdminUsername, got[0].Actor)
		assert.Empty(t, got[0].RequestHash, "request_hash is not on the wire")
		assert.Equal(t, database.AdminErrorCodeInvalidState, got[2].ErrorCode)
		assert.False(t, got[2].Success)
	})

	t.Run("filter by subscription", func(t *testing.T) {
		assert.Equal(t, []uint{seeded[2].ID, seeded[1].ID, seeded[0].ID}, ids(list("?subscription_id="+strconv.FormatUint(uint64(f.active.ID), 10))))
		assert.Equal(t, []uint{seeded[3].ID}, ids(list("?subscription_id="+strconv.FormatUint(uint64(f.paused.ID), 10))))
		assert.Empty(t, list("?subscription_id=999999"))
		// Zero means "all subscriptions", matching the filter's documented default.
		assert.Len(t, list("?subscription_id=0"), 4)
	})

	t.Run("cursor and limit", func(t *testing.T) {
		assert.Equal(t, []uint{seeded[3].ID, seeded[2].ID}, ids(list("?limit=2")))
		assert.Equal(t, []uint{seeded[1].ID, seeded[0].ID}, ids(list("?before_id="+strconv.FormatUint(uint64(seeded[2].ID), 10))))
		assert.Equal(t, []uint{seeded[1].ID}, ids(list("?before_id="+strconv.FormatUint(uint64(seeded[2].ID), 10)+"&limit=1")))
		assert.Equal(t, []uint{seeded[1].ID}, ids(list("?before_id="+strconv.FormatUint(uint64(seeded[2].ID), 10)+"&subscription_id="+strconv.FormatUint(uint64(f.active.ID), 10)+"&limit=1")))
		assert.Empty(t, list("?before_id="+strconv.FormatUint(uint64(seeded[0].ID), 10)))
		assert.Len(t, list("?limit=0"), 4, "limit=0 is the default page size")
		assert.Len(t, list("?limit=100000"), 4, "oversized limit is clamped, not rejected")
	})

	t.Run("invalid query parameters", func(t *testing.T) {
		for _, query := range []string{
			"?limit=abc", "?limit=-1", "?limit=1.0",
			"?subscription_id=abc", "?subscription_id=-1", "?subscription_id=1.0",
			"?before_id=abc", "?before_id=-1", "?before_id=%20",
		} {
			f.expectError(f.do(http.MethodGet, "/admin/api/audit"+query), http.StatusBadRequest, "invalid_request")
		}
	})
}

// 12. invalid / not-found IDs in the path
func TestAdminAPI_InvalidAndNotFoundIDs(t *testing.T) {
	f := newAdminAPIFixture(t)
	f.login()

	// Only canonical positive decimals identify a resource. Every alias of a
	// valid ID must be a distinct, unknown route.
	malformed := []string{
		"0", "-1", "+1", "007", "01", "1.0", "1e3", "0x1", "abc", "1a", "a1", "%201", "1%20",
		"１",                    // fullwidth digit
		"18446744073709551616", // > uint64
		"99999999999999999999999",
		strconv.FormatInt(f.active.TelegramID, 10) + "%00",
	}
	for _, id := range malformed {
		t.Run("users/"+id, func(t *testing.T) {
			f.expectError(f.do(http.MethodGet, "/admin/api/users/"+id), http.StatusNotFound, "not_found")
		})
		t.Run("subscriptions/"+id, func(t *testing.T) {
			f.expectError(f.do(http.MethodGet, "/admin/api/subscriptions/"+id), http.StatusNotFound, "not_found")
			// The action route rejects the ID before method, body or service.
			f.expectError(f.do(http.MethodPost, "/admin/api/subscriptions/"+id+"/renew", f.withCSRF(), withJSON(`{"request_key":"bad-id","days":1}`)), http.StatusNotFound, "not_found")
			f.expectError(f.do(http.MethodGet, "/admin/api/subscriptions/"+id+"/renew"), http.StatusNotFound, "not_found")
		})
	}

	t.Run("aliases of a real id are not that resource", func(t *testing.T) {
		canonical := strconv.FormatUint(uint64(f.active.ID), 10)
		for _, alias := range []string{"0" + canonical, "+" + canonical, canonical + ".0", " " + canonical, canonical + " "} {
			f.expectError(f.do(http.MethodGet, "/admin/api/subscriptions/"+strings.ReplaceAll(alias, " ", "%20")), http.StatusNotFound, "not_found")
		}
		canonical = strconv.FormatInt(f.active.TelegramID, 10)
		for _, alias := range []string{"0" + canonical, "+" + canonical, canonical + ".0"} {
			f.expectError(f.do(http.MethodGet, "/admin/api/users/"+alias), http.StatusNotFound, "not_found")
		}
	})

	t.Run("type bounds", func(t *testing.T) {
		// Subscription IDs are uint32-sized database keys; Telegram IDs are int64.
		f.expectError(f.do(http.MethodGet, "/admin/api/subscriptions/4294967296"), http.StatusNotFound, "not_found")
		f.expectError(f.do(http.MethodGet, "/admin/api/subscriptions/4294967295"), http.StatusNotFound, "not_found") // well-formed, unknown
		f.expectError(f.do(http.MethodGet, "/admin/api/users/9223372036854775808"), http.StatusNotFound, "not_found")
		f.expectError(f.do(http.MethodGet, "/admin/api/users/9223372036854775807"), http.StatusNotFound, "not_found") // well-formed, unknown
	})

	t.Run("well-formed but unknown", func(t *testing.T) {
		f.expectError(f.do(http.MethodGet, f.subscriptionPath(f.trial.ID+1)), http.StatusNotFound, "not_found")
		f.expectError(f.do(http.MethodGet, f.userPath(f.active.TelegramID+1000)), http.StatusNotFound, "not_found")
		// Unknown actions on a real subscription are unknown routes too.
		f.expectError(f.do(http.MethodPost, f.subscriptionPath(f.active.ID)+"/delete", f.withCSRF(), withJSON(`{}`)), http.StatusNotFound, "not_found")
		f.expectError(f.do(http.MethodPost, f.subscriptionPath(f.active.ID)+"/Renew", f.withCSRF(), withJSON(`{}`)), http.StatusNotFound, "not_found")
	})

	require.Zero(t, f.auditCount(), "no malformed request reached the service")
}
