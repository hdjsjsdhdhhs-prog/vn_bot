package adminauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

const testOrigin = "https://admin.example.com"
const testLogin = `{"username":"admin","password":"test-only-password"}`

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("test-only-password"), config.AdminMinBcryptCost)
	require.NoError(t, err)
	h, err := New(&config.Config{AdminUsername: "admin", AdminPasswordHash: string(hash), SiteURL: testOrigin + "/site"})
	require.NoError(t, err)
	return h
}

func request(handler http.Handler, method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set(CSRFHeader, csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func tokenFrom(t *testing.T, w *httptest.ResponseRecorder, authenticated bool) string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var payload struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, authenticated, payload.Authenticated)
	require.Len(t, payload.CSRF, 43)
	return payload.CSRF
}

func bootstrapTest(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	w := request(handler, "GET", "/admin/login", "", nil, "", "")
	csrf := tokenFrom(t, w, false)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	require.Equal(t, CookieName, cookie.Name)
	require.True(t, cookie.Secure)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, "/", cookie.Path)
	require.Empty(t, cookie.Domain)
	require.Len(t, cookie.Value, 43)
	require.NotEqual(t, cookie.Value, csrf)
	return cookie, csrf
}

func authenticatedTest(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	cookie, csrf := bootstrapTest(t, handler)
	w := request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin)
	csrf = tokenFrom(t, w, true)
	require.Len(t, w.Result().Cookies(), 1)
	return w.Result().Cookies()[0], csrf
}

func TestBrowserLoginRotationLogout(t *testing.T) {
	h := newTestHandler(t)
	calls := 0
	handler := h.Routes(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(204) }))
	cookie, csrf := bootstrapTest(t, handler)
	for _, path := range []string{"/admin", "/admin/", "/admin/private"} {
		require.Equal(t, 401, request(handler, "GET", path, "", cookie, "", "").Code)
	}
	require.Zero(t, calls)
	w := request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin)
	newCSRF := tokenFrom(t, w, true)
	newCookie := w.Result().Cookies()[0]
	require.NotEqual(t, cookie.Value, newCookie.Value)
	require.NotEqual(t, csrf, newCSRF)
	require.Equal(t, 401, request(handler, "GET", "/admin", "", cookie, "", "").Code)
	require.Equal(t, 403, request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin).Code)
	require.Equal(t, newCSRF, tokenFrom(t, request(handler, "GET", "/admin/session", "", newCookie, "", ""), true))
	require.Equal(t, 204, request(handler, "GET", "/admin/private", "", newCookie, "", "").Code)
	require.Equal(t, 204, request(handler, "PATCH", "/admin/private", "{}", newCookie, newCSRF, testOrigin).Code)
	require.Equal(t, 2, calls)
	require.Equal(t, 405, request(handler, "GET", "/admin/logout", "", newCookie, "", "").Code)
	require.Equal(t, 403, request(handler, "POST", "/admin/logout", "", newCookie, csrf, testOrigin).Code)
	w = request(handler, "POST", "/admin/logout", "", newCookie, newCSRF, testOrigin)
	require.Equal(t, 204, w.Code)
	deleted := w.Result().Cookies()[0]
	require.Empty(t, deleted.Value)
	require.Equal(t, -1, deleted.MaxAge)
	require.True(t, deleted.Secure && deleted.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, deleted.SameSite)
	require.Equal(t, 401, request(handler, "GET", "/admin/session", "", newCookie, "", "").Code)
}

func TestBrowserOriginAndCSRF(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	cookie, csrf := authenticatedTest(t, handler)
	for _, origin := range []string{"", "null", "http://admin.example.com", testOrigin + ".evil", testOrigin + "/", testOrigin + ":444", "https://evil.example", testOrigin + " https://evil.example"} {
		for _, path := range []string{"/admin/login", "/admin/logout", "/admin/private"} {
			require.Equal(t, 403, request(handler, "POST", path, testLogin, cookie, csrf, origin).Code, path)
		}
	}
	for _, token := range []string{"", strings.Repeat("a", 43), cookie.Value} {
		require.Equal(t, 403, request(handler, "DELETE", "/admin/private", "", cookie, token, testOrigin).Code)
	}
	for _, header := range []string{"Origin", CSRFHeader} {
		r := httptest.NewRequest("POST", "/admin/private", nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", testOrigin)
		r.Header.Set(CSRFHeader, csrf)
		r.Header.Add(header, r.Header.Get(header))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 403, w.Code)
	}
	for _, path := range []string{"/admin/login", "/admin/session"} {
		require.Equal(t, 403, request(handler, "GET", path, "", cookie, "", "https://evil.example").Code)
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 403, w.Code)
	}
	// Host and proxy headers cannot replace the configured trusted origin.
	r := httptest.NewRequest("POST", "https://evil.example/admin/private", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-Forwarded-Host", "admin.example.com")
	r.AddCookie(cookie)
	r.Header.Set(CSRFHeader, csrf)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.Equal(t, 403, w.Code)
}

func TestBrowserLoginValidationAndRateLimit(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := bootstrapTest(t, handler)
	for _, body := range []string{`{"username":"other","password":"test-only-password"}`, `{"username":"admin","password":"wrong"}`} {
		w := request(handler, "POST", "/admin/login", body, cookie, csrf, testOrigin)
		require.Equal(t, 401, w.Code)
		require.JSONEq(t, `{"error":"unauthorized"}`, w.Body.String())
		require.Empty(t, w.Result().Cookies())
	}
	for _, body := range []string{testLogin + `{}`, `{"username":4}`, `{"unknown":"value"}`} {
		require.Equal(t, 400, request(handler, "POST", "/admin/login", body, cookie, csrf, testOrigin).Code)
	}
	w := request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin)
	require.Equal(t, 429, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
	require.Equal(t, 401, request(handler, "GET", "/admin/session", "", cookie, "", "").Code)
	later := time.Now().Add(rateWindow)
	h.now = func() time.Time { return later }
	tokenFrom(t, request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin), true)
}

func TestBrowserLoginMalformedRequests(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := bootstrapTest(t, handler)
	for _, tc := range []struct {
		body, contentType string
		status            int
	}{
		{testLogin, "application/x-www-form-urlencoded", 415},
		{strings.Repeat(" ", 4097) + testLogin, "application/json", 400},
		{`null`, "application/json", 401},
		{`{"username":"admin","password":"` + strings.Repeat("a", 73) + `"}`, "application/json", 401},
	} {
		r := httptest.NewRequest("POST", "/admin/login", strings.NewReader(tc.body))
		r.Header.Set("Origin", testOrigin)
		r.Header.Set(CSRFHeader, csrf)
		r.Header.Set("Content-Type", tc.contentType)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, tc.status, w.Code)
		require.Empty(t, w.Result().Cookies())
	}
	require.Equal(t, 405, request(handler, "PUT", "/admin/login", "", nil, "", "").Code)
	// Correct credentials without the pre-login session/CSRF do not log in.
	require.Equal(t, 403, request(handler, "POST", "/admin/login", testLogin, nil, "", testOrigin).Code)
}

func TestBrowserAuthIsolationAndDisabled(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.NotFoundHandler())
	for _, auth := range []string{"Bearer subscription-token", "tma telegram-init-data", "Basic YWRtaW46cGFzcw=="} {
		r := httptest.NewRequest("GET", "/admin/session?telegram_id=1&token=anything", nil)
		r.Header.Set("Authorization", auth)
		r.AddCookie(&http.Cookie{Name: "session", Value: "anything"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		require.Equal(t, 401, w.Code)
	}
	require.Equal(t, 401, request(handler, "OPTIONS", "/admin", "", nil, "", "").Code)
	cookie, _ := authenticatedTest(t, handler)
	r := httptest.NewRequest("GET", "/admin/session", nil)
	r.AddCookie(cookie)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
	fresh := newTestHandler(t).Routes(http.NotFoundHandler())
	require.Equal(t, 401, request(fresh, "GET", "/admin/session", "", cookie, "", "").Code)
	for _, cfg := range []*config.Config{nil, {}, {TelegramAdminID: 1, TelegramBotToken: "test-only"}} {
		disabled, err := New(cfg)
		require.NoError(t, err)
		for _, path := range []string{"/admin", "/admin/login", "/admin/session", "/admin/logout"} {
			require.Equal(t, 404, request(disabled.Routes(http.NotFoundHandler()), "GET", path, "", cookie, "", "").Code)
		}
	}
	_, err := New(&config.Config{AdminUsername: "admin"})
	require.ErrorIs(t, err, config.ErrAdminConfiguration)
}

func TestBrowserSessionExpiry(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := authenticatedTest(t, handler)
	created := now
	now = now.Add(idleTTL - time.Second)
	require.Equal(t, 403, request(handler, "POST", "/admin/logout", "", cookie, csrf, "").Code)
	now = created.Add(idleTTL)
	require.Equal(t, 401, request(handler, "GET", "/admin/session", "", cookie, "", "").Code)
	cookie, _ = authenticatedTest(t, handler)
	created = now
	for now.Before(created.Add(absoluteTTL)) {
		now = now.Add(idleTTL / 2)
		status := 200
		if !now.Before(created.Add(absoluteTTL)) {
			status = 401
		}
		require.Equal(t, status, request(handler, "GET", "/admin/session", "", cookie, "", "").Code)
	}
	cookie, csrf = bootstrapTest(t, handler)
	now = now.Add(loginTTL)
	require.Equal(t, 403, request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin).Code)
}
