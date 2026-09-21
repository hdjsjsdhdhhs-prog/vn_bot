package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// This is also the local runtime smoke check: real SQLite migrations, service
// creation, two loopback HTTP listeners, middleware and all three public routes.
func TestCustomerLifecycle_LocalHTTP(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	previousLog := logger.Log
	stopped := make(chan struct{})
	logger.Log = zap.New(core, zap.Hooks(func(entry zapcore.Entry) error {
		if entry.Message == "HTTP server stopped gracefully" {
			close(stopped)
		}
		return nil
	}))
	t.Cleanup(func() { logger.Log = previousLog })
	ctx := context.Background()
	db, err := database.NewService(filepath.Join(t.TempDir(), "lifecycle.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var providerCalls, legacyCalls atomic.Int32
	var echoSecrets atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		assert.Equal(t, "private-hwid", r.Header.Get("X-HWID"))
		assert.Equal(t, "private-agent", r.Header.Get("User-Agent"))
		assert.Equal(t, "Bearer private-auth", r.Header.Get("Authorization"))
		w.Header().Set("X-HWID", "private-hwid")
		w.Header().Set("Profile-Web-Page-Url", "/upstream-private-feed")
		if echoSecrets.Load() {
			_, _ = w.Write([]byte("private-hwid private-agent private-auth"))
			return
		}
		_, _ = w.Write([]byte("vless://connection@vpn.example:443#Customer"))
	}))
	t.Cleanup(upstream.Close)
	legacyUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		legacyCalls.Add(1)
		assert.Empty(t, r.Header.Get("X-HWID"))
		_, _ = w.Write([]byte("vless://legacy@legacy.example:443#Legacy"))
	}))
	t.Cleanup(legacyUpstream.Close)
	source := &database.ProviderSource{Name: "private-provider", Type: "external", Enabled: true, SubscriptionURL: upstream.URL + "/upstream-private-feed", HWID: "private-hwid", UserAgent: "private-agent", Headers: `{"Authorization":"Bearer private-auth"}`}
	require.NoError(t, db.CreateProviderSource(ctx, source))
	plan := &database.Plan{Name: "customer", IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	free, err := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	node := &database.Node{Name: "legacy", Type: database.NodeTypeFetch, IsActive: true, SubscriptionURL: legacyUpstream.URL}
	require.NoError(t, db.GetDB().Create(node).Error)
	for _, planID := range []uint{plan.ID, free.ID} {
		require.NoError(t, db.GetDB().Create(&database.PlanNode{PlanID: planID, NodeID: node.ID}).Error)
	}
	cfg := &config.Config{GlobalSubURL: "https://customer.example/sub/"}
	subSvc := service.NewSubscriptionService(db, nil, nil, nil, cfg)
	runtime := subserver.NewService(time.Minute)
	t.Cleanup(runtime.Stop)
	subSvc.SetInvalidateBySubIDFunc(runtime.InvalidateCache)
	srv := NewServer("127.0.0.1:0", db, cfg, "", subSvc, runtime)
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, srv.Stop(stopCtx))
		// Shutdown waits for requests, not the Serve goroutine's final log.
		// Synchronize that last global-logger read before restoring logger.Log.
		select {
		case <-stopped:
		case <-stopCtx.Done():
			t.Fatal("HTTP serve goroutine did not report shutdown")
		}
	})
	base := "http://" + srv.Addr()
	cfg.GlobalSubURL = base + "/sub/"
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) (int, string, http.Header) {
		t.Helper()
		resp, err := client.Get(base + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body), resp.Header
	}
	assertSafe := func(body string, headers http.Header) {
		t.Helper()
		// Ignore the opaque QR image when checking internal data.
		visible := body + fmt.Sprint(headers)
		for _, secret := range []string{source.SubscriptionURL, "private-hwid", "private-agent", "private-auth", "private-provider", "upstream-private-feed", "provider_source_id", "telegram_id", "client_id"} {
			assert.NotContains(t, visible, secret)
		}
	}
	expires := time.Now().UTC().AddDate(0, 0, 30).Truncate(time.Second)
	created, err := subSvc.Create(ctx, 987654321, "customer", "", service.CustomerSubscriptionTerms{ProviderSourceID: source.ID, PlanID: plan.ID, ExpiresAt: expires})
	require.NoError(t, err)
	sub := created.Subscription
	assert.Equal(t, cfg.SubURL(sub.Token), created.SubscriptionURL)
	for range 2 { // first fetch and cached response
		status, body, headers := get("/sub/" + sub.Token)
		require.Equal(t, http.StatusOK, status)
		assertSafe(body, headers)
		decoded, err := base64.StdEncoding.DecodeString(body)
		require.NoError(t, err)
		assert.Contains(t, string(decoded), "vless://connection@vpn.example")
	}
	assert.Equal(t, int32(1), providerCalls.Load())
	assert.Zero(t, legacyCalls.Load())
	status, body, headers := get("/subscription-info/" + sub.Token)
	require.Equal(t, http.StatusOK, status)
	assertSafe(body, headers)
	var info service.PublicSubscriptionInfo
	require.NoError(t, json.Unmarshal([]byte(body), &info))
	assert.Equal(t, "active", info.Status)
	require.NotNil(t, info.ExpiresAt)
	assert.True(t, expires.Equal(*info.ExpiresAt))
	assert.Equal(t, created.SubscriptionURL, info.SubscriptionURL)
	status, body, headers = get("/connect/" + sub.Token)
	require.Equal(t, http.StatusOK, status)
	assertSafe(body, headers)
	assertConnectionURLAndQR(t, body, created.SubscriptionURL)
	assert.NotContains(t, body, "987654321")
	assert.NotContains(t, body, sub.SubscriptionID)
	assert.NotContains(t, body, sub.ClientID)

	assertRenewalVisible := func() {
		t.Helper()
		callsBefore, legacyBefore := providerCalls.Load(), legacyCalls.Load()
		renewed, err := subSvc.RenewSubscription(ctx, sub.ID, 7)
		require.NoError(t, err)
		assert.Equal(t, sub.ID, renewed.Subscription.ID)
		assert.Equal(t, sub.SubscriptionID, renewed.Subscription.SubscriptionID)
		assert.Equal(t, sub.Token, renewed.Subscription.Token)
		assert.Equal(t, sub.ProviderSourceID, renewed.Subscription.ProviderSourceID)
		assert.Equal(t, created.SubscriptionURL, renewed.SubscriptionURL)
		assert.Equal(t, callsBefore, providerCalls.Load(), "renewal must not call upstream")
		for range 2 {
			status, body, headers := get("/sub/" + sub.Token)
			require.Equal(t, http.StatusOK, status)
			assertSafe(body, headers)
			decoded, err := base64.StdEncoding.DecodeString(body)
			require.NoError(t, err)
			assertSafe(string(decoded), headers)
			assert.Contains(t, string(decoded), "vless://connection@vpn.example")
		}
		assert.Equal(t, callsBefore+1, providerCalls.Load(), "renewal must invalidate the warmed provider cache")
		assert.Equal(t, legacyBefore, legacyCalls.Load(), "renewal must never fall back to legacy")
		status, body, headers := get("/subscription-info/" + sub.Token)
		require.Equal(t, http.StatusOK, status)
		assertSafe(body, headers)
		var info service.PublicSubscriptionInfo
		require.NoError(t, json.Unmarshal([]byte(body), &info))
		assert.Equal(t, "active", info.Status)
		require.NotNil(t, info.ExpiresAt)
		assert.True(t, renewed.Subscription.ExpiresAt.Equal(*info.ExpiresAt))
		assert.Equal(t, created.SubscriptionURL, info.SubscriptionURL)
		status, body, headers = get("/connect/" + sub.Token)
		require.Equal(t, http.StatusOK, status)
		assertSafe(body, headers)
		assertConnectionURLAndQR(t, body, created.SubscriptionURL)
		assert.NotContains(t, body, "Истекла")
		assert.Contains(t, body, renewed.Subscription.ExpiresAt.UTC().Format("02.01.2006 15:04 UTC"))
	}
	assertRenewalVisible() // active renewal with warmed feed cache

	// Existing free creation still delivers through legacy nodes.
	legacy, err := subSvc.Create(ctx, 876543210, "legacy", "")
	require.NoError(t, err)
	status, body, _ = get("/sub/" + legacy.Subscription.Token)
	require.Equal(t, http.StatusOK, status)
	decoded, err := base64.StdEncoding.DecodeString(body)
	require.NoError(t, err)
	assert.Contains(t, string(decoded), "vless://legacy@legacy.example")
	assert.Equal(t, int32(1), legacyCalls.Load())

	// Revalidate the source even on a cache hit; no legacy fallback.
	require.NoError(t, db.GetDB().Model(source).Update("enabled", false).Error)
	status, body, headers = get("/sub/" + sub.Token)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assertSafe(body, headers)
	assert.Equal(t, int32(1), legacyCalls.Load())
	require.NoError(t, db.GetDB().Model(source).Update("enabled", true).Error)
	echoSecrets.Store(true)
	status, body, headers = get("/sub/" + sub.Token)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assertSafe(body, headers)
	echoSecrets.Store(false)
	// Expiry is enforced before the worker and after it, including warmed cache.
	past := time.Now().UTC().Add(-time.Minute)
	sub.ExpiresAt = &past
	require.NoError(t, db.UpdateSubscription(ctx, sub))
	for _, expireWorker := range []bool{false, true} {
		if expireWorker {
			require.NoError(t, subSvc.ExpireSubscription(ctx, sub.ID))
		}
		status, _, _ = get("/sub/" + sub.Token)
		assert.Equal(t, http.StatusNotFound, status)
		status, body, headers = get("/subscription-info/" + sub.Token)
		require.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, `"status":"expired"`)
		assertSafe(body, headers)
		status, body, headers = get("/connect/" + sub.Token)
		require.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "Истекла")
		assertSafe(body, headers)
	}
	assertRenewalVisible() // expired access becomes available immediately at the same URLs
	for _, entry := range logs.All() {
		text := entry.Message + fmt.Sprint(entry.ContextMap())
		for _, secret := range []string{sub.Token, legacy.Subscription.Token, source.SubscriptionURL, "private-hwid", "private-agent", "private-auth"} {
			assert.False(t, strings.Contains(text, secret), "application log leaked a bearer or provider credential")
		}
	}
}
