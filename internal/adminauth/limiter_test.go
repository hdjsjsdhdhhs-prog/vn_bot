package adminauth

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiterConcurrentBudgets(t *testing.T) {
	for _, sharedPeer := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared_peer_%t", sharedPeer), func(t *testing.T) {
			l := limiter{peers: make(map[string]attemptWindow)}
			now := time.Now()
			var allowed atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range 128 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := httptest.NewRequest("POST", "/admin/login", nil)
					r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1)
					if sharedPeer {
						r.RemoteAddr = fmt.Sprintf("192.0.2.1:%d", 1000+i)
					}
					r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
					r.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i+1))
					<-start
					if l.allow(r, now, 5, 20) {
						allowed.Add(1)
					}
				}()
			}
			close(start)
			wg.Wait()
			want := int32(20)
			if sharedPeer {
				want = 5
			}
			require.Equal(t, want, allowed.Load())
			r := httptest.NewRequest("POST", "/admin/login", nil)
			r.RemoteAddr = "192.0.2.1:9999"
			require.False(t, l.allow(r, now.Add(rateWindow-time.Nanosecond), 5, 20))
			require.True(t, l.allow(r, now.Add(rateWindow), 5, 20))
		})
	}
}

func TestLimiterNormalizesPeerAddresses(t *testing.T) {
	for _, peers := range [][]string{
		{"192.0.2.1:1", "[::ffff:192.0.2.1]:2", "192.0.2.1"},
		{"[2001:db8::1]:1", "[2001:0db8:0:0:0:0:0:1]:2", "2001:db8::1"},
		{"invalid", "", "other-invalid"},
	} {
		l := limiter{peers: make(map[string]attemptWindow)}
		now := time.Now()
		for i, peer := range peers {
			r := httptest.NewRequest("POST", "/admin/login", nil)
			r.RemoteAddr = peer
			require.Equal(t, i == 0, l.allow(r, now, 1, 20), peer)
		}
	}
}
