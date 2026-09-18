package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"html/template"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func assertConnectionURLAndQR(t *testing.T, body, wantURL string) {
	t.Helper()
	value := regexp.MustCompile(`id="subscription-url"[^>]* value="([^"]*)"`).FindStringSubmatch(body)
	require.Len(t, value, 2)
	assert.Equal(t, wantURL, html.UnescapeString(value[1]))

	image := regexp.MustCompile(`src="data:image/png;base64,([^"]+)"`).FindStringSubmatch(body)
	require.Len(t, image, 2, "QR must be embedded, not fetched from an external service")
	// Decode HTML attribute entities just as the browser does (e.g. &#43;).
	gotPNG, err := base64.StdEncoding.DecodeString(html.UnescapeString(image[1]))
	require.NoError(t, err)
	_, err = png.Decode(bytes.NewReader(gotPNG))
	require.NoError(t, err)
	// The existing encoder is deterministic: byte equality proves the displayed
	// image is the QR for exactly this URL, not a deep link or upstream feed.
	wantPNG, err := utils.GenerateQRCodePNG(wantURL)
	require.NoError(t, err)
	assert.Equal(t, wantPNG, gotPNG)
	assert.NotContains(t, body, "ZgotmplZ")
}

func assertNoConnectionInternals(t *testing.T, body string) {
	t.Helper()
	// Exclude the opaque PNG when checking numeric IDs: base64 may coincidentally
	// contain digits. Its complete contents are verified separately above.
	body = regexp.MustCompile(`data:image/png;base64,[^"]+`).ReplaceAllString(body, "[QR]")
	for _, secret := range []string{
		"987654321", "876543210", "765432109", "654321098",
		"internal-client", "internal-provisioning", "private-provider",
		"provider.example", "provider-user", "provider-password", "private-feed",
		"secret-hwid", "secret-agent", "secret-header", "Authorization",
		"ProviderSource", "provider_source", "telegram_id", "subscription_id",
	} {
		assert.NotContains(t, body, secret)
	}
}

func TestConnectionPage_StatesAndDataBoundary(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for _, tc := range []struct {
		name, status, label string
		expiry              *time.Time
		active              bool
	}{
		{"active", "active", "Активна", &future, true},
		{"unlimited", "active", "Активна", nil, true},
		{"expired", "expired", "Истекла", &past, false},
		{"expiry worker pending", "active", "Истекла", &past, false},
		{"revoked", "revoked", "Отозвана", &past, false},
		{"paused", "paused", "Приостановлена", &future, false},
		{"canceled", "canceled", "Отменена", nil, false},
		{"unknown status", "private-provider", "Неактивна", nil, false},
	} {
		for _, providerBacked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/provider=%t", tc.name, providerBacked), func(t *testing.T) {
				db := testutil.NewDatabaseService()
				db.GetByTokenFunc = func(_ context.Context, token string) (*database.Subscription, error) {
					require.Equal(t, subscriptionInfoTokenA, token)
					sub := &database.Subscription{
						ID: 987654321, TelegramID: 765432109, PlanID: 654321098,
						ClientID: "internal-client", SubscriptionID: "internal-provisioning",
						Token: token, Status: tc.status, ExpiresAt: tc.expiry,
					}
					if providerBacked {
						id := uint(876543210)
						sub.ProviderSourceID = &id
						sub.ProviderSource = &database.ProviderSource{
							ID: id, Name: "private-provider", Type: "private-provider",
							SubscriptionURL: "https://provider-user:provider-password@provider.example/private-feed",
							HWID:            "secret-hwid", UserAgent: "secret-agent",
							Headers: `{"Authorization":"Bearer secret-header"}`,
						}
					}
					return sub, nil
				}
				srv, cfg := newSubscriptionInfoTestServer(db)
				rr := httptest.NewRecorder()
				srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, "/connect/"+subscriptionInfoTokenA, nil))
				require.Equal(t, http.StatusOK, rr.Code)
				assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
				assert.Equal(t, "text/html; charset=utf-8", rr.Header().Get("Content-Type"))
				body := rr.Body.String()
				assert.Contains(t, body, ">"+tc.label+"</dd>")
				assert.Equal(t, tc.active, strings.Contains(body, `class="status active"`))
				assert.Equal(t, !tc.active, strings.Contains(body, "Подписка сейчас неактивна"))
				if tc.expiry == nil {
					assert.Contains(t, body, "Бессрочно")
				} else {
					assert.Contains(t, body, tc.expiry.UTC().Format("02.01.2006 15:04 UTC"))
				}
				assertConnectionURLAndQR(t, body, cfg.SubURL(subscriptionInfoTokenA))
				assertNoConnectionInternals(t, body)
			})
		}
	}
}

func TestConnectionPage_InvalidTokens(t *testing.T) {
	db := testutil.NewDatabaseService()
	lookups := 0
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		lookups++
		return nil, database.ErrSubscriptionNotFound
	}
	srv, _ := newSubscriptionInfoTestServer(db)
	var notFoundBody string
	for _, path := range []string{
		"/connect/", "/connect/not-a-token", "/connect/987654321",
		"/connect/" + strings.Repeat("A", 64), "/connect/" + strings.Repeat("g", 64),
		"/connect/" + strings.Repeat("a", 63), "/connect/" + strings.Repeat("a", 65),
		"/connect/" + subscriptionInfoTokenA + "/extra",
		"/subscription-info/" + subscriptionInfoTokenA,
		"/connect/" + subscriptionInfoTokenB,
	} {
		rr := httptest.NewRecorder()
		srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, rr.Code, path)
		assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
		assert.NotContains(t, rr.Body.String(), subscriptionInfoTokenA)
		assert.NotContains(t, rr.Body.String(), subscriptionInfoTokenB)
		assert.Contains(t, rr.Body.String(), "Подписка не найдена")
		if notFoundBody == "" {
			notFoundBody = rr.Body.String()
		}
		assert.Equal(t, notFoundBody, rr.Body.String(), "malformed and unknown tokens must have identical responses")
	}
	assert.Equal(t, 1, lookups, "only a well-formed token should reach the repository")

	for name, lookup := range map[string]func(context.Context, string) (*database.Subscription, error){
		"nil": func(context.Context, string) (*database.Subscription, error) { return nil, nil },
		"gorm not found": func(context.Context, string) (*database.Subscription, error) {
			return nil, fmt.Errorf("lookup: %w", gorm.ErrRecordNotFound)
		},
		"wrong customer": func(context.Context, string) (*database.Subscription, error) {
			return &database.Subscription{Token: subscriptionInfoTokenA, SubscriptionID: "internal-provisioning"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			db.GetByTokenFunc = lookup
			rr := httptest.NewRecorder()
			srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, "/connect/"+subscriptionInfoTokenB, nil))
			assert.Equal(t, http.StatusNotFound, rr.Code)
			assert.Equal(t, notFoundBody, rr.Body.String())
		})
	}
}

func TestConnectionPage_FailuresDoNotLeak(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	previous := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() { logger.Log = previous })

	for _, failure := range []string{"repository", "service", "qr", "template"} {
		t.Run(failure, func(t *testing.T) {
			db := testutil.NewDatabaseService()
			db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
				return &database.Subscription{Token: subscriptionInfoTokenA, Status: "active"}, nil
			}
			srv, cfg := newSubscriptionInfoTestServer(db)
			switch failure {
			case "repository":
				db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
					return nil, errors.New("query token=" + subscriptionInfoTokenA + " private-provider")
				}
			case "service":
				srv.subService = nil
			case "qr":
				cfg.GlobalSubURL = "https://customer.example/" + strings.Repeat("x", 5000)
			case "template":
				srv.connectionTemplate = template.Must(template.New("broken").Parse(`partial {{.SubURL}}{{.MissingField}}`))
			}
			rr := httptest.NewRecorder()
			srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, "/connect/"+subscriptionInfoTokenA, nil))
			assert.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
			assert.NotContains(t, rr.Body.String(), subscriptionInfoTokenA)
			assert.NotContains(t, rr.Body.String(), "partial")
			assert.NotContains(t, rr.Body.String(), "private-provider")
		})
	}
	require.GreaterOrEqual(t, logs.Len(), 4)
	for _, entry := range logs.All() {
		logText := fmt.Sprintf("%s %v", entry.Message, entry.ContextMap())
		assert.NotContains(t, logText, subscriptionInfoTokenA)
		assert.NotContains(t, logText, "private-provider")
	}
}

func TestConnectionPage_MethodNotAllowed(t *testing.T) {
	srv, _ := newSubscriptionInfoTestServer(testutil.NewDatabaseService())
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodHead, http.MethodDelete} {
		rr := httptest.NewRecorder()
		srv.handleConnectionPage(rr, httptest.NewRequest(method, "/connect/"+subscriptionInfoTokenA, nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
		assert.Equal(t, "GET", rr.Header().Get("Allow"))
		assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	}
}

func TestConnectionPage_TemplateEscapingAndLocalCopy(t *testing.T) {
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		return &database.Subscription{Token: subscriptionInfoTokenA, Status: `"><script>alert('status')</script>`}, nil
	}
	srv, cfg := newSubscriptionInfoTestServer(db)
	cfg.GlobalSubURL = `https://customer.example/sub/?x="><script>alert('url')</script>&y=1`
	rr := httptest.NewRecorder()
	srv.handleConnectionPage(rr, httptest.NewRequest(http.MethodGet, "/connect/"+subscriptionInfoTokenA, nil))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assertConnectionURLAndQR(t, body, cfg.SubURL(subscriptionInfoTokenA))
	assert.NotContains(t, body, `<script>alert`)
	assert.Contains(t, body, "Неактивна")
	assert.Equal(t, 1, strings.Count(body, "<script>"))
	script := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindStringSubmatch(body)[1]
	assert.NotContains(t, script, subscriptionInfoTokenA, "bearer data must not be interpolated into JS")
	for _, local := range []string{"input.value", "navigator.clipboard.writeText(value)", "document.execCommand('copy')", "input.setSelectionRange", "status.textContent"} {
		assert.Contains(t, script, local)
	}
	for _, remote := range []string{"fetch(", "XMLHttpRequest", "sendBeacon", "http://", "https://", "happ://", "hiddify://", "incy://", "throne://", "<script src="} {
		assert.NotContains(t, script, remote)
	}
}

func TestConnectionRoute_LocalHTTPWithDatabase(t *testing.T) {
	// Exercise migrations, persisted tokens, the shared presentation service,
	// embedded templates and the real listener/middleware/ServeMux together.
	db, err := database.NewService(filepath.Join(t.TempDir(), "connect.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	plan, err := db.GetPlanByName(context.Background(), database.FreePlanName)
	require.NoError(t, err)
	provider := &database.ProviderSource{
		Name: "private-provider", Type: "subscription", Enabled: true,
		SubscriptionURL: "https://provider-user:provider-password@provider.example/private-feed",
		HWID:            "secret-hwid", UserAgent: "secret-agent", Headers: `{"Authorization":"Bearer secret-header"}`,
	}
	require.NoError(t, db.CreateProviderSource(context.Background(), provider))
	for i, token := range []string{subscriptionInfoTokenA, subscriptionInfoTokenB} {
		sub := &database.Subscription{
			TelegramID: int64(100 + i), ClientID: fmt.Sprintf("internal-client-%d", i),
			SubscriptionID: fmt.Sprintf("internal-provisioning-%d", i), Token: token,
			PlanID: plan.ID, Status: "active",
		}
		if i == 1 {
			sub.ProviderSourceID = &provider.ID
		}
		require.NoError(t, db.CreateSubscription(context.Background(), sub, ""))
	}
	cfg := &config.Config{GlobalSubURL: "https://customer.example/custom/sub/"}
	subSvc := service.NewSubscriptionService(db, nil, nil, nil, cfg)
	srv := NewServer("127.0.0.1:0", db, cfg, "", subSvc, nil)
	require.NoError(t, srv.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, srv.Stop(ctx))
	})
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := client.Get("http://" + srv.Addr() + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, string(body)
	}
	for _, token := range []string{subscriptionInfoTokenA, subscriptionInfoTokenB} {
		resp, body := get("/connect/" + token + "?ignored=private-provider")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
		assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
		assert.Equal(t, "noindex, nofollow", resp.Header.Get("X-Robots-Tag"))
		assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		assertConnectionURLAndQR(t, body, cfg.SubURL(token))
		assertNoConnectionInternals(t, body)
		other := subscriptionInfoTokenA
		if token == other {
			other = subscriptionInfoTokenB
		}
		assert.NotContains(t, body, other)
	}
	for _, path := range []string{"/connect/", "/connect/bad", "/connect/" + strings.Repeat("c", 64), "/connect/" + subscriptionInfoTokenA + "/extra"} {
		resp, body := get(path)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
		assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
		assert.NotContains(t, body, subscriptionInfoTokenA)
	}
	for _, path := range []string{"/connect", "/connect//" + subscriptionInfoTokenA, "//connect/" + subscriptionInfoTokenA, "/prefix/../connect/" + subscriptionInfoTokenB} {
		resp, _ := get(path)
		assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"), "mux redirects must not cache bearer paths")
	}
	// Existing adjacent routes still reach their own handlers.
	resp, body := get("/subscription-info/" + subscriptionInfoTokenA)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `"subscription_url"`)
	resp, _ = get("/sub/" + subscriptionInfoTokenA)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "no subserver configured in this fixture")
	resp, _ = get("/healthz")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body = get("/metrics")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `path="/connect/:token"`)
	assert.NotContains(t, body, subscriptionInfoTokenA)
	assert.NotContains(t, body, subscriptionInfoTokenB)
	assert.NotContains(t, body, "private-provider")
}
