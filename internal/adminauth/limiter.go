package adminauth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const rateWindow = time.Minute

type attemptWindow struct {
	start time.Time
	count int
}

type limiter struct {
	mu     sync.Mutex
	peers  map[string]attemptWindow
	global attemptWindow
}

// allow reserves attempts before bcrypt, including concurrent/failed attempts.
// Forwarded headers are deliberately ignored: there is no configured trusted
// proxy list in Admin Config. Behind a proxy its peers share the same budget.
func (l *limiter) allow(r *http.Request, now time.Time, perPeer, total int) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	ip := net.ParseIP(peer)
	if ip == nil {
		peer = "unknown"
	} else {
		peer = ip.String()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.global.start.Add(rateWindow)) {
		l.global = attemptWindow{start: now}
	}
	if l.global.count >= total {
		return false
	}
	for key, window := range l.peers {
		if !now.Before(window.start.Add(rateWindow)) {
			delete(l.peers, key)
		}
	}
	window, exists := l.peers[peer]
	if !exists {
		if len(l.peers) >= 4096 {
			return false
		}
		window.start = now
	}
	if window.count >= perPeer {
		return false
	}
	window.count++
	l.peers[peer] = window
	l.global.count++
	return true
}
