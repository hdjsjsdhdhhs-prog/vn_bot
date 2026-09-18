package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/telegramauth"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const miniAppTestToken = "123456:MINIAPP_TEST_ONLY"

func miniAppTestData(id int64, date time.Time, token string) string {
	user := fmt.Sprintf(`{"id":%d,"first_name":"Test + & user"}`, id)
	stamp := strconv.FormatInt(date.Unix(), 10)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	_, _ = secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	_, _ = mac.Write([]byte("auth_date=" + stamp + "\nuser=" + user))
	return url.Values{"auth_date": {stamp}, "user": {user}, "hash": {hex.EncodeToString(mac.Sum(nil))}}.Encode()
}

func TestMiniAppAuthentication(t *testing.T) {
	valid := miniAppTestData(1234567890123, time.Now(), miniAppTestToken)
	for _, tc := range []struct {
		name    string
		headers []string
		want    int
	}{
		{"valid", []string{"tma " + valid}, 204},
		{"case insensitive scheme", []string{"TMA " + valid}, 204},
		{"missing", nil, 401},
		{"empty", []string{""}, 401},
		{"empty payload", []string{"tma "}, 401},
		{"bare payload", []string{valid}, 401},
		{"malformed escape", []string{"tma %zz"}, 401},
		{"bearer", []string{"Bearer " + subscriptionInfoTokenA}, 401},
		{"bearer as initData", []string{"tma " + subscriptionInfoTokenA}, 401},
		{"unsigned user", []string{`tma user={"id":1234567890123}`}, 401},
		{"duplicate header", []string{"tma " + valid, "tma " + valid}, 401},
		{"joined header", []string{"tma " + valid + ", tma " + valid}, 401},
		{"extra space", []string{"tma  " + valid}, 401},
		{"oversized", []string{"tma " + strings.Repeat("a", telegramauth.MaxInitDataBytes+1)}, 401},
		{"tampered", []string{"tma " + strings.Replace(valid, "1234567890123", "1234567890124", 1)}, 401},
		{"expired", []string{"tma " + miniAppTestData(1234567890123, time.Now().Add(-6*time.Minute), miniAppTestToken)}, 401},
		{"future", []string{"tma " + miniAppTestData(1234567890123, time.Now().Add(time.Minute), miniAppTestToken)}, 401},
		{"other bot", []string{"tma " + miniAppTestData(1234567890123, time.Now(), "other:BOT")}, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := miniAppAuthentication(telegramauth.New(miniAppTestToken), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				id, err := authorizeMiniApp(r.Context(), service.SubscriptionManagementRead, 0)
				require.NoError(t, err)
				assert.Equal(t, int64(1234567890123), id)
				for _, action := range []service.SubscriptionManagementAction{service.SubscriptionManagementRenew, "unknown"} {
					_, err := authorizeMiniApp(r.Context(), action, 7)
					require.ErrorIs(t, err, service.ErrSubscriptionAccessDenied)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			// None of these unsigned claims may authenticate or override identity.
			req := httptest.NewRequest(http.MethodGet, "/api/miniapp/subscription?telegram_id=999&initData="+url.QueryEscape(valid), strings.NewReader(`{"telegram_id":999,"initDataUnsafe":{"user":{"id":999}}}`))
			req.Header["Authorization"] = tc.headers
			req.Header.Set("X-Telegram-ID", "999")
			req.AddCookie(&http.Cookie{Name: "initData", Value: valid})
			// Even a preexisting context value must not bypass verification.
			req = req.WithContext(context.WithValue(req.Context(), miniAppIdentityKey{}, int64(999)))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, tc.want, rr.Code)
			assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			assert.Empty(t, rr.Header().Get("Set-Cookie"))
			assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
			if tc.want == 401 {
				assert.Zero(t, calls)
				assert.JSONEq(t, `{"error":"unauthorized"}`, rr.Body.String())
				assert.Equal(t, `tma realm="miniapp"`, rr.Header().Get("WWW-Authenticate"))
			} else {
				assert.Equal(t, 1, calls)
			}
		})
	}
}

func TestMiniAppPolicyFailsClosed(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), context.WithValue(context.Background(), miniAppIdentityKey{}, int64(0)), context.WithValue(context.Background(), miniAppIdentityKey{}, int64(-42))} {
		_, err := authorizeMiniApp(ctx, service.SubscriptionManagementRead, 0)
		require.ErrorIs(t, err, service.ErrSubscriptionAccessDenied)
	}
	ctx := context.WithValue(context.Background(), miniAppIdentityKey{}, int64(42))
	_, err := authorizeMiniApp(ctx, service.SubscriptionManagementRead, 1)
	require.ErrorIs(t, err, service.ErrSubscriptionAccessDenied)
	_, err = service.NewSubscriptionManagement(nil, authorizeMiniApp).Renew(ctx, 7)
	require.ErrorIs(t, err, service.ErrSubscriptionAccessDenied, "deny before any lifecycle call")
	for _, cfg := range []*config.Config{nil, {}, {TelegramBotToken: " \t"}, {TelegramBotToken: miniAppTestToken}} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/miniapp/subscription", nil)
		req.Header.Set("Authorization", "tma "+miniAppTestData(42, time.Now(), miniAppTestToken))
		newMiniAppHandler(cfg, nil).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
		assert.JSONEq(t, `{"error":"service_unavailable"}`, rr.Body.String())

		rr = httptest.NewRecorder()
		req.Header.Del("Authorization")
		newMiniAppHandler(cfg, nil).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "missing credentials fail before configuration or service lookup")
		assert.JSONEq(t, `{"error":"unauthorized"}`, rr.Body.String())
	}
}

type miniAppFailingRepository struct {
	*testutil.DatabaseService
	sub *database.Subscription
	err error
}

func (r *miniAppFailingRepository) GetCustomerSubscription(context.Context, int64) (*database.Subscription, bool, error) {
	return r.sub, false, r.err
}

func TestMiniAppServiceFailuresAreSanitized(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	previous := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() { logger.Log = previous })
	for _, tc := range []struct {
		name   string
		sub    *database.Subscription
		err    error
		status int
		code   string
	}{
		{"missing", nil, fmt.Errorf("private-query: %w", database.ErrSubscriptionNotFound), 404, "subscription_not_found"},
		{"db failure", nil, errors.New("private-query token=" + subscriptionInfoTokenA), 503, "service_unavailable"},
		{"canceled", nil, context.Canceled, 503, "service_unavailable"},
		{"deadline", nil, context.DeadlineExceeded, 503, "service_unavailable"},
		{"wrong owner", &database.Subscription{TelegramID: 99, Token: subscriptionInfoTokenA}, nil, 403, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &miniAppFailingRepository{DatabaseService: testutil.NewDatabaseService(), sub: tc.sub, err: tc.err}
			cfg := &config.Config{TelegramBotToken: miniAppTestToken, GlobalSubURL: "https://customer.example/sub/"}
			svc := service.NewSubscriptionService(repo, nil, nil, nil, cfg)
			req := httptest.NewRequest(http.MethodGet, "/api/miniapp/subscription", nil)
			req.Header.Set("Authorization", "tma "+miniAppTestData(42, time.Now(), miniAppTestToken))
			rr := httptest.NewRecorder()
			newMiniAppHandler(cfg, svc).ServeHTTP(rr, req)
			assert.Equal(t, tc.status, rr.Code)
			assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, tc.code), rr.Body.String())
		})
	}
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), "private-query")
		assert.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), subscriptionInfoTokenA)
	}
}

func TestMiniAppRoute_LocalHTTPWithManagement(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	previous := logger.Log
	stopped := make(chan struct{})
	logger.Log = zap.New(core, zap.Hooks(func(entry zapcore.Entry) error {
		if entry.Message == "HTTP server stopped gracefully" {
			close(stopped)
		}
		return nil
	}))
	t.Cleanup(func() { logger.Log = previous })
	ctx := context.Background()
	db, err := database.NewService(filepath.Join(t.TempDir(), "miniapp.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	plan := &database.Plan{Name: "miniapp-paid", IsActive: true}
	require.NoError(t, db.GetDB().Create(plan).Error)
	provider := &database.ProviderSource{Name: "miniapp-private-provider", Type: "external", Enabled: true, SubscriptionURL: "https://provider.example/private-miniapp-feed", HWID: "miniapp-private-hwid", Headers: `{"Authorization":"Bearer miniapp-private-header"}`}
	require.NoError(t, db.CreateProviderSource(ctx, provider))
	cfg := &config.Config{TelegramBotToken: miniAppTestToken, GlobalSubURL: "https://customer.example/sub/", TelegramAdminID: 1234567890123}
	svc := service.NewSubscriptionService(db, nil, nil, nil, cfg)
	var subs []*database.Subscription
	for i, token := range []string{subscriptionInfoTokenA, subscriptionInfoTokenB} {
		expiry := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
		sub := &database.Subscription{TelegramID: 1234567890123 + int64(i), ClientID: fmt.Sprintf("miniapp-client-%d", i), SubscriptionID: fmt.Sprintf("miniapp-internal-%d", i), Token: token, PlanID: plan.ID, Status: "active", ExpiresAt: &expiry}
		if i == 1 {
			sub.ProviderSourceID = &provider.ID
		}
		require.NoError(t, db.CreateSubscription(ctx, sub, ""))
		subs = append(subs, sub)
	}
	srv := NewServer("127.0.0.1:0", db, cfg, "", svc, nil)
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, auth string, want int) (string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+srv.Addr()+path, nil)
		require.NoError(t, err)
		req.Host = "untrusted-host.example"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, want, resp.StatusCode, "%s %s: %s", method, path, body)
		if strings.Contains(path, "miniapp") {
			assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
			assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
			assert.Equal(t, "noindex, nofollow", resp.Header.Get("X-Robots-Tag"))
			assert.Empty(t, resp.Header.Get("Set-Cookie"))
		}
		return string(body), resp.Header
	}
	var credentials []string
	for i, sub := range subs {
		auth := "tma " + miniAppTestData(sub.TelegramID, time.Now(), miniAppTestToken)
		credentials = append(credentials, auth)
		before, err := db.GetByID(ctx, sub.ID)
		require.NoError(t, err)
		body, _ := request("GET", "/api/miniapp/subscription?telegram_id="+strconv.FormatInt(subs[1-i].TelegramID, 10)+"&token="+subs[1-i].Token, auth, 200)
		// Compare against the real existing application contract, not a second DTO.
		trusted := context.WithValue(ctx, miniAppIdentityKey{}, sub.TelegramID)
		info, err := service.NewSubscriptionManagement(svc, authorizeMiniApp).Current(trusted)
		require.NoError(t, err)
		want, err := json.Marshal(info)
		require.NoError(t, err)
		assert.JSONEq(t, string(want), body)
		require.True(t, info.Renewal.Eligible, "eligibility must not become grant authority")
		_, err = service.NewSubscriptionManagement(svc, authorizeMiniApp).Renew(trusted, 7)
		require.ErrorIs(t, err, service.ErrSubscriptionAccessDenied)
		assert.Contains(t, body, cfg.SubURL(sub.Token))
		assert.NotContains(t, body, subs[1-i].Token)
		for _, secret := range []string{miniAppTestToken, "untrusted-host.example", sub.ClientID, sub.SubscriptionID, "telegram_id", "provider_source", provider.SubscriptionURL, provider.HWID, "miniapp-private-header"} {
			assert.NotContains(t, body, secret)
		}
		// An HTTP method is also untrusted input; it must not leak a bearer
		// credential into metrics even when authentication rejects the request.
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", sub.Token} {
			request(method, "/api/miniapp/subscription", "", 401)
			_, headers := request(method, "/api/miniapp/subscription", auth, 405)
			assert.Equal(t, "GET", headers.Get("Allow"))
		}
		for _, path := range []string{"/api/miniapp/renew", "/api/miniapp/subscription/renew", "/api/miniapp/subscription/" + subs[1-i].Token} {
			request("POST", path, "", 401)
			request("POST", path, auth, 404)
		}
		after, err := db.GetByID(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, before, after, "reads and rejected writes must not change subscriptions, even for admin")
	}
	// Exercise rejection through the real listener and middleware stack, not
	// only the validator; rejected credentials must not appear in logs/metrics.
	rejectedCredentials := []string{
		"",
		"tma malformed-initdata-%zz",
		strings.Replace(credentials[0], "1234567890123", "1234567890124", 1),
		"tma " + miniAppTestData(subs[0].TelegramID, time.Now().Add(-6*time.Minute), miniAppTestToken),
		"Bearer " + subs[0].Token,
		"tma " + subs[0].Token,
	}
	for _, auth := range rejectedCredentials {
		body, headers := request("GET", "/api/miniapp/subscription", auth, 401)
		assert.JSONEq(t, `{"error":"unauthorized"}`, body)
		assert.Equal(t, `tma realm="miniapp"`, headers.Get("WWW-Authenticate"))
	}
	request("GET", "/api/miniapp/subscription?initData="+url.QueryEscape(strings.TrimPrefix(credentials[0], "tma "))+"&token="+subs[0].Token, "", 401)
	request("GET", "/api/miniapp/subscription", "tma "+miniAppTestData(999, time.Now(), miniAppTestToken), 404)
	var count int64
	require.NoError(t, db.GetDB().Model(&database.Subscription{}).Count(&count).Error)
	assert.Equal(t, int64(2), count, "missing customer must not trigger creation")
	// Same credential, fresh database state: no session, subscription cache or revival.
	require.NoError(t, db.GetDB().Model(provider).Update("enabled", false).Error)
	providerBody, _ := request("GET", "/api/miniapp/subscription", credentials[1], 200)
	var providerInfo service.SubscriptionManagementInfo
	require.NoError(t, json.Unmarshal([]byte(providerBody), &providerInfo))
	assert.False(t, providerInfo.Renewal.Eligible, "eligibility must use fresh service state")
	for _, status := range []string{"expired", "revoked", "paused", "canceled"} {
		require.NoError(t, db.GetDB().Model(subs[0]).Update("status", status).Error)
		body, _ := request("GET", "/api/miniapp/subscription", credentials[0], 200)
		var info service.SubscriptionManagementInfo
		require.NoError(t, json.Unmarshal([]byte(body), &info))
		assert.Equal(t, status, info.Status)
	}
	past := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, db.GetDB().Model(subs[0]).Updates(map[string]any{"status": "active", "expires_at": past}).Error)
	body, _ := request("GET", "/api/miniapp/subscription", credentials[0], 200)
	assert.Contains(t, body, `"status":"expired"`)
	stored, err := db.GetByID(ctx, subs[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "active", stored.Status, "presentation must not run expiry transitions")
	for _, path := range []string{"/api/miniapp", "//api/miniapp/" + subs[0].Token, "/prefix/../api/miniapp/" + subs[1].Token} {
		request("GET", path, "", 301)
	}
	// Public bearer routes and health remain independent of Telegram auth.
	request("GET", "/subscription-info/"+subs[0].Token, "", 200)
	request("GET", "/connect/"+subs[0].Token, "", 200)
	request("GET", "/healthz", "", 200)
	body, _ = request("GET", "/metrics", "", 200)
	assert.Contains(t, body, `path="/api/miniapp/*"`)
	secrets := []string{strings.TrimPrefix(credentials[0], "tma "), strings.TrimPrefix(credentials[1], "tma "), miniAppTestToken, subs[0].Token, subs[1].Token, provider.SubscriptionURL, provider.HWID, "miniapp-private-header"}
	for _, auth := range rejectedCredentials[1:] {
		secrets = append(secrets, strings.TrimPrefix(auth, "tma "))
	}
	for _, secret := range secrets {
		assert.NotContains(t, body, secret)
		for _, entry := range logs.All() {
			assert.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), secret)
		}
	}
}
