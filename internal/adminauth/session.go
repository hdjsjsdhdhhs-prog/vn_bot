// Package adminauth implements browser administrator authentication independently
// of Telegram identities, bot credentials and customer subscription tokens.
package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const (
	CookieName  = "__Host-rs8kvn_admin"
	CSRFHeader  = "X-CSRF-Token"
	idleTTL     = 30 * time.Minute
	absoluteTTL = 12 * time.Hour
	loginTTL    = 10 * time.Minute
	maxSessions = 1024
)

type session struct {
	csrf          string
	authenticated bool
	created       time.Time
	lastSeen      time.Time
}

func (s session) expired(now time.Time) bool {
	if !s.authenticated {
		return !now.Before(s.created.Add(loginTTL))
	}
	return !now.Before(s.created.Add(absoluteTTL)) || !now.Before(s.lastSeen.Add(idleTTL))
}

type sessionStore struct {
	mu      sync.Mutex
	entries map[string]session
}

func randomToken() string {
	var b [32]byte
	// crypto/rand.Read fills the buffer or terminates on an OS entropy failure.
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func sessionID(r *http.Request) string {
	cookies := r.CookiesNamed(CookieName)
	if len(cookies) != 1 || len(cookies[0].Value) != 43 {
		return ""
	}
	return cookies[0].Value
}

func (s *sessionStore) get(id string, now time.Time, csrf string, requireCSRF, touch bool) (session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	if !ok {
		return session{}, false
	}
	if entry.expired(now) {
		delete(s.entries, id)
		return session{}, false
	}
	if requireCSRF && subtle.ConstantTimeCompare([]byte(entry.csrf), []byte(csrf)) != 1 {
		return session{}, false
	}
	// Requests sample the clock before taking this lock and may arrive here
	// out of order. An older request must never shorten the idle deadline.
	if touch && now.After(entry.lastSeen) {
		entry.lastSeen = now
		s.entries[id] = entry
	}
	return entry, true
}

// issue atomically replaces an existing session on login. A concurrent logout,
// successful login or expiry invalidates the in-flight login's old session too.
func (s *sessionStore) issue(oldID, csrf string, authenticated bool, now time.Time) (string, session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.entries {
		if entry.expired(now) {
			delete(s.entries, id)
		}
	}
	if authenticated {
		old, ok := s.entries[oldID]
		if !ok || subtle.ConstantTimeCompare([]byte(old.csrf), []byte(csrf)) != 1 {
			return "", session{}, false
		}
	} else if len(s.entries) >= maxSessions {
		return "", session{}, false
	}
	id := randomToken()
	for _, exists := s.entries[id]; exists; _, exists = s.entries[id] {
		id = randomToken()
	}
	entry := session{csrf: randomToken(), authenticated: authenticated, created: now, lastSeen: now}
	delete(s.entries, oldID)
	s.entries[id] = entry
	return id, entry, true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
}

func setCookie(w http.ResponseWriter, id string) {
	cookie := &http.Cookie{Name: CookieName, Value: id, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if id == "" {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0)
	}
	// Browser-session cookie; the authoritative idle/absolute clocks live only
	// on the server. No Domain is permitted by the __Host- prefix.
	http.SetCookie(w, cookie)
}
