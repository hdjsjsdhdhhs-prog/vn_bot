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
	"go.uber.org/zap/zaptest/observer"
)

func TestSubscriptionManagement_PublicEndpoints(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	previousLog := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() { logger.Log = previousLog })
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			ctx := context.Background()
			db, err := database.NewService(filepath.Join(t.TempDir(), "management.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "test-management-hwid", r.Header.Get("X-HWID"))
				assert.Equal(t, "test-management-agent", r.Header.Get("User-Agent"))
				assert.Equal(t, "Bearer test-management-auth", r.Header.Get("Authorization"))
				w.Header().Set("X-HWID", "test-management-hwid")
				_, _ = w.Write([]byte("vless://connection@vpn.example:443#Customer"))
			}))
			t.Cleanup(upstream.Close)
			source := &database.ProviderSource{Name: "management-provider", Type: "external", Enabled: true, SubscriptionURL: upstream.URL + "/private-management-feed", HWID: "test-management-hwid", UserAgent: "test-management-agent", Headers: `{"Authorization":"Bearer test-management-auth"}`}
			require.NoError(t, db.CreateProviderSource(ctx, source))
			plan := &database.Plan{Name: "management", IsActive: true, TrafficLimit: 1024}
			require.NoError(t, db.GetDB().Create(plan).Error)
			cfg := &config.Config{GlobalSubURL: "https://customer.example/sub/"}
			svc := service.NewSubscriptionService(db, nil, nil, nil, cfg)
			runtime := subserver.NewService(time.Minute)
			t.Cleanup(runtime.Stop)
			svc.SetInvalidateBySubIDFunc(runtime.InvalidateCache)
			srv := NewServer("127.0.0.1:0", db, cfg, "", svc, runtime)
			require.NoError(t, srv.Start(ctx))
			t.Cleanup(func() {
				stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				require.NoError(t, srv.Stop(stopCtx))
			})
			base := "http://" + srv.Addr()
			cfg.GlobalSubURL = base + "/sub/"
			created, err := svc.Create(ctx, 90501, "customer", "", service.CustomerSubscriptionTerms{ProviderSourceID: source.ID, PlanID: plan.ID, ExpiresAt: time.Now().UTC().AddDate(0, 0, 30)})
			require.NoError(t, err)
			sub := created.Subscription
			assertSafe := func(body string, headers http.Header) {
				t.Helper()
				for _, secret := range []string{source.SubscriptionURL, "test-management-hwid", "test-management-agent", "test-management-auth", "provider_source", "telegram_id", "client_id", sub.SubscriptionID, sub.ClientID} {
					assert.NotContains(t, body+fmt.Sprint(headers), secret)
				}
			}
			client := &http.Client{Timeout: 5 * time.Second}
			get := func(path string, want int) (string, http.Header) {
				t.Helper()
				resp, err := client.Get(base + path)
				require.NoError(t, err)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, want, resp.StatusCode)
				assertSafe(string(body), resp.Header)
				return string(body), resp.Header
			}
			get("/sub/"+sub.Token, http.StatusOK) // warm the actual feed cache
			if expired {
				past := time.Now().UTC().Add(-time.Hour)
				sub.ExpiresAt, sub.Status = &past, "expired"
				require.NoError(t, db.UpdateSubscription(ctx, sub))
				get("/sub/"+sub.Token, http.StatusNotFound)
			}
			type grantKey struct{}
			trustedCtx := context.WithValue(ctx, grantKey{}, true)
			management := service.NewSubscriptionManagement(svc, func(callCtx context.Context, action service.SubscriptionManagementAction, days int) (int64, error) {
				if callCtx.Value(grantKey{}) != true || (action == service.SubscriptionManagementRenew && days != 7) {
					return 0, service.ErrSubscriptionAccessDenied
				}
				return 90501, nil
			})
			before, err := management.Current(trustedCtx)
			require.NoError(t, err)
			assert.Equal(t, base+"/connect/"+sub.Token, before.ConnectionURL)
			_, err = management.Renew(ctx, 7)
			require.ErrorIs(t, err, service.ErrSubscriptionAccessDenied)
			callsBefore := calls.Load()
			renewed, err := management.Renew(trustedCtx, 7)
			require.NoError(t, err)
			assert.Equal(t, callsBefore, calls.Load(), "management grant must not call the provider")
			assert.Equal(t, before.SubscriptionURL, renewed.SubscriptionURL)
			assert.Equal(t, before.ConnectionURL, renewed.ConnectionURL)
			data, err := json.Marshal(renewed)
			require.NoError(t, err)
			assertSafe(string(data), nil)
			body, headers := get("/subscription-info/"+sub.Token, http.StatusOK)
			assert.Equal(t, "no-store", headers.Get("Cache-Control"))
			var public service.PublicSubscriptionInfo
			require.NoError(t, json.Unmarshal([]byte(body), &public))
			assert.Equal(t, renewed.PublicSubscriptionInfo, public)
			body, headers = get("/connect/"+sub.Token, http.StatusOK)
			assert.Equal(t, "no-store", headers.Get("Cache-Control"))
			assert.Contains(t, body, renewed.ExpiresAt.UTC().Format("02.01.2006 15:04 UTC"))
			assertConnectionURLAndQR(t, body, created.SubscriptionURL)
			for range 2 {
				body, headers = get("/sub/"+sub.Token, http.StatusOK)
				decoded, err := base64.StdEncoding.DecodeString(body)
				require.NoError(t, err)
				assertSafe(string(decoded), headers)
				assert.Contains(t, string(decoded), "vless://connection@vpn.example")
			}
			assert.Equal(t, callsBefore+1, calls.Load(), "grant must clear the warmed feed cache")
			for _, path := range []string{"/sub/", "/subscription-info/", "/connect/"} {
				resp, err := client.Post(base+path+sub.Token, "application/json", nil)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "bearer routes remain read-only")
			}
			stored, err := db.GetByID(ctx, sub.ID)
			require.NoError(t, err)
			assert.Equal(t, sub.Token, stored.Token)
			assert.True(t, renewed.ExpiresAt.Equal(*stored.ExpiresAt), "public requests must not grant days")
			for _, entry := range logs.All() {
				text := entry.Message + fmt.Sprint(entry.ContextMap())
				for _, secret := range []string{sub.Token, source.SubscriptionURL, "test-management-hwid", "test-management-agent", "test-management-auth"} {
					assert.NotContains(t, text, secret)
				}
			}
		})
	}
}
