package adminauth

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionLastSeenNeverMovesBackwards(t *testing.T) {
	now := time.Now()
	s := sessionStore{entries: map[string]session{
		"id": {authenticated: true, csrf: "csrf", created: now, lastSeen: now},
	}}
	newer := now.Add(time.Minute)
	older := now.Add(time.Second)
	// Force the ordering of two overlapping requests: the request that sampled
	// the clock earlier acquires the store lock after the newer request.
	updated := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.get("id", newer, "", false, true)
		close(updated)
	}()
	go func() {
		defer wg.Done()
		<-updated
		s.get("id", older, "", false, true)
	}()
	wg.Wait()
	require.Equal(t, newer, s.entries["id"].lastSeen)
	// The older timestamp must not shorten the idle deadline.
	_, ok := s.get("id", older.Add(idleTTL), "", false, false)
	require.True(t, ok)
	_, ok = s.get("id", newer.Add(idleTTL), "", false, false)
	require.False(t, ok)
}

func TestSessionConcurrentRotationHasOneWinner(t *testing.T) {
	now := time.Now()
	s := sessionStore{entries: make(map[string]session)}
	oldID, old, ok := s.issue("", "", false, now)
	require.True(t, ok)
	type result struct {
		id    string
		entry session
		ok    bool
	}
	results := make(chan result, 32)
	start := make(chan struct{})
	for range cap(results) {
		go func() {
			<-start
			id, entry, issued := s.issue(oldID, old.csrf, true, now)
			results <- result{id, entry, issued}
		}()
	}
	close(start)
	winners := 0
	for range cap(results) {
		r := <-results
		if r.ok {
			winners++
			require.NotEqual(t, oldID, r.id)
			require.NotEqual(t, old.csrf, r.entry.csrf)
			require.True(t, r.entry.authenticated)
		}
	}
	require.Equal(t, 1, winners)
	require.Len(t, s.entries, 1)
	_, ok = s.get(oldID, now, "", false, false)
	require.False(t, ok)
}

func TestSessionRevocationAndExpiryPreventRotation(t *testing.T) {
	for _, reason := range []string{"logout", "login_expiry", "idle_expiry", "absolute_expiry", "bad_csrf"} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			s := sessionStore{entries: make(map[string]session)}
			id, entry, ok := s.issue("", "", false, now)
			require.True(t, ok)
			if reason == "idle_expiry" || reason == "absolute_expiry" {
				id, entry, ok = s.issue(id, entry.csrf, true, now)
				require.True(t, ok)
			}
			// Simulate an in-flight login that already validated CSRF, then
			// spent time in bcrypt while another operation invalidated it.
			_, ok = s.get(id, now, entry.csrf, true, false)
			require.True(t, ok)
			switch reason {
			case "logout":
				done := make(chan struct{})
				go func() { s.delete(id); close(done) }()
				<-done
			case "login_expiry":
				now = now.Add(loginTTL)
			case "idle_expiry":
				now = now.Add(idleTTL)
			case "absolute_expiry":
				entry.lastSeen = now.Add(absoluteTTL - time.Minute)
				s.entries[id] = entry
				now = now.Add(absoluteTTL)
			case "bad_csrf":
				entry.csrf = "wrong"
			}
			_, _, ok = s.issue(id, entry.csrf, true, now)
			require.False(t, ok)
			if reason != "bad_csrf" {
				require.Empty(t, s.entries)
			}
		})
	}
}

func TestSessionCapacityAndExpiredCleanup(t *testing.T) {
	now := time.Now()
	s := sessionStore{entries: make(map[string]session)}
	var id string
	var entry session
	for range maxSessions {
		var ok bool
		id, entry, ok = s.issue("", "", false, now)
		require.True(t, ok)
	}
	_, _, ok := s.issue("", "", false, now)
	require.False(t, ok)
	// Rotation replaces a slot; a full store must not prevent a valid login.
	_, _, ok = s.issue(id, entry.csrf, true, now)
	require.True(t, ok)
	require.Len(t, s.entries, maxSessions)
	_, _, ok = s.issue("", "", false, now.Add(loginTTL))
	require.True(t, ok)
	require.Len(t, s.entries, 2)
}
