package poc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestSharedUpstreamIndependentExpiryAndRenewal(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	upstreamHits := 0
	s := New("http://secret-upstream.invalid")
	s.now = func() time.Time { return now }
	s.fetch = func(context.Context, string) (*subserver.NodeResponse, error) {
		upstreamHits++
		return &subserver.NodeResponse{Body: []byte("vless://shared-config")}, nil
	}
	s.Seed("A", 10*time.Minute)
	s.Seed("B", 20*time.Minute)
	s.Seed("C", 30*time.Minute)
	call := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/sub/"+id, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, id := range []string{"A", "B", "C"} {
		require.Equal(t, 200, call(id).Code)
		require.Equal(t, "vless://shared-config", call(id).Body.String())
	}
	require.Equal(t, 1, upstreamHits)
	now = now.Add(11 * time.Minute)
	require.Equal(t, 404, call("A").Code)
	require.Equal(t, 200, call("B").Code)
	require.Equal(t, 200, call("C").Code)
	require.Equal(t, 1, upstreamHits)
	now = now.Add(10 * time.Minute)
	require.Equal(t, 404, call("B").Code)
	require.Equal(t, 200, call("C").Code)
	now = now.Add(10 * time.Minute)
	require.Equal(t, 404, call("C").Code)
	now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	original := s.subs["A"].ID
	renewed, err := s.Renew("A", 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, original, renewed.ID)
	require.Equal(t, 200, call("A").Code)
}

func TestExpiredSubscriptionDoesNotFetchOrExposeUpstream(t *testing.T) {
	s := New("https://secret.example/token")
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	hits := 0
	s.fetch = func(context.Context, string) (*subserver.NodeResponse, error) {
		hits++
		return &subserver.NodeResponse{Body: []byte("shared")}, nil
	}
	s.Seed("A", time.Minute)
	now = now.Add(2 * time.Minute)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/A", nil))
	require.Equal(t, 404, w.Code)
	require.Zero(t, hits)
	require.NotContains(t, w.Body.String(), "secret.example")
}

func TestPOCPageAndQRUseOnlyLocalURL(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New("https://secret-upstream.example/token")
	s.now = func() time.Time { return now }
	for _, id := range []string{"A", "B", "C"} {
		s.Seed(id, 10*time.Minute)
		page := httptest.NewRecorder()
		s.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8890/poc/"+id, nil))
		require.Equal(t, http.StatusOK, page.Code)
		require.Contains(t, page.Body.String(), "Подписка "+id)
		require.Contains(t, page.Body.String(), "Активна")
		require.Contains(t, page.Body.String(), LANBaseURL+"/sub/"+id)
		require.NotContains(t, page.Body.String(), "secret-upstream.example")

		qr := httptest.NewRecorder()
		s.ServeHTTP(qr, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8890/poc/"+id+"/qr.png", nil))
		require.Equal(t, http.StatusOK, qr.Code)
		require.Equal(t, "image/png", qr.Header().Get("Content-Type"))
		expectedQR, err := utils.GenerateQRCodePNG(LANBaseURL + "/sub/" + id)
		require.NoError(t, err)
		require.Equal(t, expectedQR, qr.Body.Bytes())
		require.NotContains(t, qr.Body.String(), "secret-upstream.example")
	}

	now = now.Add(11 * time.Minute)
	expired := httptest.NewRecorder()
	s.ServeHTTP(expired, httptest.NewRequest(http.MethodGet, "/poc/A", nil))
	require.Equal(t, http.StatusOK, expired.Code)
	require.Contains(t, expired.Body.String(), "Истекла")

	_, err := s.Renew("A", 10*time.Minute)
	require.NoError(t, err)
	renewed := httptest.NewRecorder()
	s.ServeHTTP(renewed, httptest.NewRequest(http.MethodGet, "/poc/A", nil))
	require.Contains(t, renewed.Body.String(), "Активна")
}
