package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"gorm.io/gorm"
)

const (
	testSubscriptionToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testSubscriptionPath  = "/sub/" + testSubscriptionToken
)

func testServer(t *testing.T, db interfaces.DatabaseService, cfg *config.Config) *Server {
	if fake, ok := db.(*testutil.DatabaseService); ok && fake.GetByTokenFunc == nil {
		fake.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
			return &database.Subscription{ID: 1, SubscriptionID: "test123", Token: testSubscriptionToken, Status: string(database.SubscriptionStatusActive)}, nil
		}
	}
	subSvc := service.NewSubscriptionService(db, cfg)
	srv := NewServer(":0", db, cfg, "testbot", subSvc, subserver.NewService(config.SubServerCacheTTL))

	return srv
}

// providerSourceDB returns a fake DB whose active subscription is served from a
// single directly linked ProviderSource pointing at sourceURL — the serving
// route that needs no builder.
func providerSourceDB(sourceURL string) *testutil.DatabaseService {
	source := &database.ProviderSource{ID: 1, Name: "src", SubscriptionURL: sourceURL, Headers: `{}`, Enabled: true, UpdatedAt: time.Unix(1, 0)}
	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(_ context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			ID:               1,
			SubscriptionID:   subscriptionID,
			Status:           string(database.SubscriptionStatusActive),
			ProviderSourceID: &source.ID,
			ProviderSource:   source,
		}, nil
	}

	return db
}

func TestHandleSubscription_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "GET", w.Header().Get("Allow"))
}

func TestHandleSubscription_SubServerNil(t *testing.T) {
	t.Parallel()

	srv := &Server{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "Subscription server is not available", w.Body.String())
}

func TestHandleSubscription_InvalidCode(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	srv := testServer(t, db, &config.Config{})

	tests := []string{
		"/sub/",
		"/sub/with/path",
		"/sub/special@char",
	}

	for _, path := range tests {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		srv.handleSubscription(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code, "path: %s", path)
		assert.Equal(t, "Subscription not found", w.Body.String(), "path: %s", path)
	}
}

func TestHandleSubscription_InvalidTokenDoesNotLookupSubscription(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	var lookups atomic.Int32
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		lookups.Add(1)
		return nil, errors.New("unexpected lookup")
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/not-a-valid-token", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Zero(t, lookups.Load())
}

func TestHandleSubscription_TokenRoutesToProviderSource(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("vless://provider@provider.example:443#Provider"))
	}))
	defer upstream.Close()

	source := &database.ProviderSource{ID: 72, SubscriptionURL: upstream.URL, Headers: `{}`, Enabled: true, UpdatedAt: time.Unix(1, 0)}
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(_ context.Context, token string) (*database.Subscription, error) {
		return &database.Subscription{ID: 72, SubscriptionID: "internal-provider-sub", Token: token, Status: string(database.SubscriptionStatusActive)}, nil
	}
	db.GetSubscriptionWithProviderSourceFunc = func(_ context.Context, subscriptionID string) (*database.Subscription, error) {
		require.Equal(t, "internal-provider-sub", subscriptionID)
		return &database.Subscription{ID: 72, SubscriptionID: subscriptionID, Status: string(database.SubscriptionStatusActive), ProviderSourceID: &source.ID, ProviderSource: source}, nil
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	testServer(t, db, &config.Config{}).handleSubscription(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	decoded, err := base64.StdEncoding.DecodeString(w.Body.String())
	require.NoError(t, err)
	assert.Contains(t, string(decoded), "vless://provider@provider.example")
}

func TestHandleSubscription_TokenPreservesExpiredAndRevokedNotFound(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"expired", "revoked"} {
		t.Run(name, func(t *testing.T) {
			db := testutil.NewDatabaseService()
			db.GetByTokenFunc = func(_ context.Context, token string) (*database.Subscription, error) {
				return &database.Subscription{ID: 73, SubscriptionID: "internal-" + name, Token: token}, nil
			}
			db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
				return nil, database.ErrSubscriptionNotFound
			}

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
			testServer(t, db, &config.Config{}).handleSubscription(w, r)

			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Equal(t, "Subscription not found", w.Body.String())
		})
	}
}

func TestHandleSubscription_AccessLog(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Subscription not found", w.Body.String())
}

// TestHandleSubscription_NoBuilderReturns503: an active subscription with no
// enabled builder (neither its own nor its plan's) and no linked ProviderSource
// is a configuration error. /sub answers 503 with a generic body — no fallback
// route, no 404 (the subscription exists), and no internal details leak.
func TestHandleSubscription_NoBuilderReturns503(t *testing.T) {
	t.Parallel()

	builderID := uint(404)
	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(_ context.Context, subscriptionID string) (*database.Subscription, error) {
		return &database.Subscription{
			ID:                    1,
			SubscriptionID:        subscriptionID,
			PlanID:                7,
			Status:                string(database.SubscriptionStatusActive),
			SubscriptionBuilderID: &builderID, // dangling override: resolves to no builder
		}, nil
	}
	db.ResolveSubscriptionBuilderFunc = func(context.Context, *database.Subscription) (*database.ResolvedBuilder, error) {
		return nil, nil
	}
	var deviceWrites atomic.Int32
	db.UpdateDevicesFunc = func(context.Context, uint, string) error {
		deviceWrites.Add(1)
		return nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "Subscription is temporarily unavailable", w.Body.String())
	assert.NotContains(t, w.Body.String(), "builder")
	assert.NotContains(t, w.Body.String(), "test123")
	assert.Zero(t, deviceWrites.Load(), "an unservable subscription must not record device analytics")
}

func TestHandleSubscription_SourceVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		userInfo string
		check    func(t *testing.T, w *httptest.ResponseRecorder, body string)
	}{
		{
			name:     "plain source",
			body:     "vless://abc@x.com:443\nvmess://def@y.com:8443",
			userInfo: "upload=100; download=200; total=1000; expire=1234567890",
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
				require.NoError(t, err)
				assert.Contains(t, string(decoded), "vless://abc@x.com:443")
				assert.Contains(t, string(decoded), "vmess://def@y.com:8443")

				userInfo := w.Header().Get("Subscription-Userinfo")
				assert.Contains(t, userInfo, "upload=100")
				assert.Contains(t, userInfo, "download=200")
			},
		},
		{
			name:     "JSON source",
			body:     `[{"type":"vless","address":"x.com","port":443,"uuid":"abc123","encryption":"none","remark":"S1"}]`,
			userInfo: "upload=50; download=75; total=500; expire=9999",
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

				var items []json.RawMessage
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
				require.Len(t, items, 1)

				var parsed map[string]any
				require.NoError(t, json.Unmarshal(items[0], &parsed))
				assert.Equal(t, "vless", parsed["type"])
				assert.Equal(t, "x.com", parsed["address"])

				userInfo := w.Header().Get("Subscription-Userinfo")
				assert.Contains(t, userInfo, "upload=50")
			},
		},
		{
			name:     "base64 encoded source",
			body:     base64.StdEncoding.EncodeToString([]byte("vless://b64@x.com:443\nvmess://b64@y.com:443")),
			userInfo: "upload=10; download=20; total=100; expire=555",
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
				require.NoError(t, err)
				assert.Contains(t, string(decoded), "vless://b64@x.com:443")
				assert.Contains(t, string(decoded), "vmess://b64@y.com:443")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Subscription-Userinfo", tt.userInfo)
				w.Write([]byte(tt.body))
			}))
			defer backend.Close()

			srv := testServer(t, providerSourceDB(backend.URL), &config.Config{})

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
			srv.handleSubscription(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
			tt.check(t, w, w.Body.String())
		})
	}
}

func TestHandleSubscription_AccessLogVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupReq   func() *http.Request
		wantFields []string
	}{
		{
			name: "with headers",
			setupReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, testSubscriptionPath+"?debug=1", nil)
				r.RemoteAddr = "203.0.113.10:1234"
				r.Header.Set("X-Hwid", "hw-1")
				r.Header.Set("X-Device-Os", "iOS")
				r.Header.Set("X-Ver-Os", "17.0")
				r.Header.Set("X-Device-Model", "iPhone 15")
				r.Header.Set("User-Agent", "V2Ray/1.0")

				return r
			},
			wantFields: []string{"GET", "/sub/:token", "404", "-", "203.0.113.10", "hw-1", "iOS", "17.0", "iPhone 15", "V2Ray/1.0"},
		},
		{
			name: "missing optional headers",
			setupReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
				r.RemoteAddr = "203.0.113.10:1234"

				return r
			},
			wantFields: []string{"GET", "/sub/:token", "404", "-", "203.0.113.10", "", "", "", "", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testutil.NewDatabaseService()
			srv := testServer(t, db, &config.Config{})
			logPath := filepath.Join(t.TempDir(), "subserver.log")
			accessLogger, err := subserver.NewAccessLogger(logPath)
			require.NoError(t, err)

			srv.subserverLogger = accessLogger

			r := tt.setupReq()
			w := httptest.NewRecorder()
			srv.handleSubscription(w, r)

			require.NoError(t, accessLogger.Close())

			content, err := os.ReadFile(logPath)
			require.NoError(t, err)

			line := strings.TrimRight(string(content), "\n")

			parts := splitAccessLogLine(line)
			for len(parts) < 11 {
				parts = append(parts, "")
			}

			require.Len(t, parts, 11)
			assert.Regexp(t, regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`), parts[0])
			assert.NotContains(t, line, "\nGET")
			assert.NotContains(t, line, "SUBSERVER_ACCESS")
			assert.NotContains(t, line, `"method"`)
			assert.Equal(t, tt.wantFields, parts[1:])
		})
	}
}

func TestHandleSubscription_AccessLogSuccessTotalOrder(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "subserver.log")
	accessLogger, err := subserver.NewAccessLogger(logPath)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	r.RemoteAddr = "203.0.113.10:1234"
	// success=3, total=5 must be written as "3/5", not "5/3".
	accessLogger.Log(r, 200, "203.0.113.10", 3, 5)

	require.NoError(t, accessLogger.Close())

	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	line := strings.TrimRight(string(content), "\n")
	parts := splitAccessLogLine(line)
	require.GreaterOrEqual(t, len(parts), 5)
	// Field layout: timestamp method uri status success/total ...
	assert.Equal(t, "/sub/:token", parts[2])
	assert.NotContains(t, line, testSubscriptionToken)
	assert.Equal(t, "3/5", parts[4])
}

func splitAccessLogLine(line string) []string {
	var (
		parts   []string
		current strings.Builder
	)

	inQuotes := false
	for _, r := range line {
		if r == '"' {
			inQuotes = !inQuotes
		} else if r == ' ' && !inQuotes {
			parts = append(parts, current.String())
			current.Reset()

			continue
		}

		current.WriteRune(r)
	}

	parts = append(parts, current.String())
	for i, p := range parts {
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			parts[i] = p[1 : len(p)-1]
		}
	}

	return parts
}
func TestHandleSubscription_SourceFetchError(t *testing.T) {
	t.Parallel()

	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer b.Close()

	srv := testServer(t, providerSourceDB(b.URL), &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	// A failed upstream fetch is terminal for a ProviderSource route: safe 503,
	// no fallback and no upstream details in the body.
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "Subscription source is unavailable", w.Body.String())
	assert.NotContains(t, w.Body.String(), b.URL)
}

// TestHandleSubscription_DatabaseError_Returns500GenericBody verifies the audit #5
// fix: a real infrastructure (DB) error must return 500 with a generic body, NOT
// "Subscription not found" (which would conflate 404 and 500 semantics).
func TestHandleSubscription_DatabaseError_Returns500GenericBody(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return nil, gorm.ErrInvalidDB // arbitrary non-not-found infra error
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "infra error must be 500, not 404")
	assert.Equal(t, "Internal Server Error", w.Body.String(), "500 body must be generic, not 'Subscription not found'")
}

func TestHandleSubscription_ProviderSourceUnavailableReturnsSafe503(t *testing.T) {
	t.Parallel()

	const (
		upstreamURL = "https://provider.example/private/subscription-token"
		hwid        = "provider-secret-hwid"
		userAgent   = "provider-secret-agent"
	)

	sourceID := uint(91)
	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return &database.Subscription{
			ID:               1,
			SubscriptionID:   "provider-unavailable",
			Status:           string(database.SubscriptionStatusActive),
			ProviderSourceID: &sourceID,
			ProviderSource: &database.ProviderSource{
				ID:              sourceID,
				SubscriptionURL: upstreamURL,
				HWID:            hwid,
				UserAgent:       userAgent,
				Headers:         `{}`,
				Enabled:         false,
			},
		}, nil
	}

	srv := testServer(t, db, &config.Config{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "Subscription source is unavailable", w.Body.String())
	assert.NotContains(t, w.Body.String(), upstreamURL)
	assert.NotContains(t, w.Body.String(), hwid)
	assert.NotContains(t, w.Body.String(), userAgent)
}

func TestHandleSubscription_CacheSubscriptionResult(t *testing.T) {
	t.Parallel()

	plainContent := "vless://cached@x.com:443"

	var callCount atomic.Int32

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)

		w.Write([]byte(plainContent))
	}))
	defer backend.Close()

	srv := testServer(t, providerSourceDB(backend.URL), &config.Config{})

	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w1, r1)
	assert.Equal(t, http.StatusOK, w1.Code)

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	srv.handleSubscription(w2, r2)
	assert.Equal(t, http.StatusOK, w2.Code)

	assert.Equal(t, int32(1), callCount.Load(), "second request served from per-subID cache")
}

func TestHandleSubscription_DevicesTracking(t *testing.T) {
	t.Parallel()

	plainContent := "vless://dev@x.com:443"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(plainContent))
	}))
	defer backend.Close()

	var savedDevices string

	db := providerSourceDB(backend.URL)
	db.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		savedDevices = devicesJSON
		return nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, testSubscriptionPath, nil)
	r.Header.Set("X-Hwid", "device1")
	r.Header.Set("User-Agent", "v2rayN/1.0")
	srv.handleSubscription(w, r)

	require.NotEmpty(t, savedDevices, "devices should have been saved")

	var devices []map[string]string

	err := json.Unmarshal([]byte(savedDevices), &devices)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	assert.Equal(t, "device1", devices[0]["x-hwid"])
	assert.Equal(t, "v2rayn/1.0", devices[0]["user-agent"])
	_, err = time.Parse(time.RFC3339, devices[0]["timestamp"])
	assert.NoError(t, err, "timestamp should be RFC3339")
}

func init() {
	err := testutil.InitLogger(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
	}
}
