package adminauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBrowserRepeatedLoginRotatesSessionAndCSRF(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := authenticatedTest(t, handler)
	w := request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin)
	newCSRF := tokenFrom(t, w, true)
	require.Len(t, w.Result().Cookies(), 1)
	newCookie := w.Result().Cookies()[0]
	require.NotEqual(t, cookie.Value, newCookie.Value)
	require.NotEqual(t, csrf, newCSRF)
	require.Equal(t, 401, request(handler, "GET", "/admin/session", "", cookie, "", "").Code)
	require.Equal(t, 403, request(handler, "POST", "/admin/logout", "", newCookie, csrf, testOrigin).Code)
	require.Equal(t, newCSRF, tokenFrom(t, request(handler, "GET", "/admin/session", "", newCookie, "", ""), true))
	// Fetching the login token again must not rotate or extend a session.
	w = request(handler, "GET", "/admin/login", "", newCookie, "", "")
	require.Equal(t, newCSRF, tokenFrom(t, w, true))
	require.Empty(t, w.Result().Cookies())
}

func TestBrowserRejectedRequestsDoNotExtendIdleExpiry(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := authenticatedTest(t, handler)
	created := now
	now = created.Add(idleTTL - time.Second)
	for _, origin := range []string{"", "https://evil.example"} {
		require.Equal(t, 403, request(handler, "POST", "/admin/logout", "", cookie, csrf, origin).Code)
	}
	for _, token := range []string{"", strings.Repeat("a", 43)} {
		require.Equal(t, 403, request(handler, "POST", "/admin/logout", "", cookie, token, testOrigin).Code)
	}
	tokenFrom(t, request(handler, "GET", "/admin/login", "", cookie, "", ""), true)
	now = created.Add(idleTTL)
	require.Equal(t, 401, request(handler, "GET", "/admin/session", "", cookie, "", "").Code)
}

func TestBrowserLoginRechecksSessionAfterPassword(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprintf("logout_%t", revoke), func(t *testing.T) {
			h := newTestHandler(t)
			now := time.Now()
			h.now = func() time.Time { return now }
			handler := h.Routes(http.NotFoundHandler())
			cookie, csrf := bootstrapTest(t, handler)
			clockReads := 0
			h.now = func() time.Time {
				clockReads++
				// login reads the clock for rate limit, CSRF, then rotation.
				if clockReads == 3 {
					if revoke {
						h.sessions.delete(cookie.Value)
					} else {
						now = now.Add(loginTTL)
					}
				}
				return now
			}
			w := request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin)
			require.Equal(t, 3, clockReads)
			require.Equal(t, 403, w.Code)
			require.Empty(t, w.Result().Cookies())
			require.Empty(t, h.sessions.entries)
		})
	}
}

func TestBrowserConcurrentLogoutCannotResurrectSession(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := authenticatedTest(t, handler)
	var wg sync.WaitGroup
	start := make(chan struct{})
	statuses := make(chan int, 32)
	for range cap(statuses) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			statuses <- request(handler, "GET", "/admin/session", "", cookie, "", "").Code
		}()
	}
	close(start)
	require.Equal(t, 204, request(handler, "POST", "/admin/logout", "", cookie, csrf, testOrigin).Code)
	wg.Wait()
	for range cap(statuses) {
		require.Contains(t, []int{200, 401, 403}, <-statuses)
	}
	require.Equal(t, 401, request(handler, "GET", "/admin/session", "", cookie, "", "").Code)
	require.Empty(t, h.sessions.entries)
}

func TestBrowserRateLimitsAcrossPeersAndWindowReset(t *testing.T) {
	h := newTestHandler(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	handler := h.Routes(http.NotFoundHandler())
	for _, tc := range []struct {
		method, path                   string
		perPeer, global, allowedStatus int
	}{
		{"POST", "/admin/login", 5, 20, 403}, // Missing session is charged before bcrypt.
		{"GET", "/admin/login", 20, 200, 200},
	} {
		t.Run(tc.method, func(t *testing.T) {
			send := func(peer int) *httptest.ResponseRecorder {
				r := httptest.NewRequest(tc.method, tc.path, nil)
				r.Header.Set("Origin", testOrigin)
				r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", peer)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			for i := range tc.global {
				require.Equal(t, tc.allowedStatus, send(i/tc.perPeer+1).Code)
				if i == tc.perPeer-1 {
					require.Equal(t, 429, send(1).Code)
				}
			}
			w := send(254)
			require.Equal(t, 429, w.Code)
			require.Equal(t, "60", w.Header().Get("Retry-After"))
			now = now.Add(rateWindow)
			require.Equal(t, tc.allowedStatus, send(254).Code)
		})
	}
}

func TestBrowserPasswordWorkIsBounded(t *testing.T) {
	h := newTestHandler(t)
	handler := h.Routes(http.NotFoundHandler())
	cookie, csrf := bootstrapTest(t, handler)
	// Occupy the single bcrypt slot as another in-flight login would.
	h.passwordSlot <- struct{}{}
	w := request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin)
	<-h.passwordSlot
	require.Equal(t, 429, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
	require.Empty(t, w.Result().Cookies())
	tokenFrom(t, request(handler, "POST", "/admin/login", testLogin, cookie, csrf, testOrigin), true)
}
