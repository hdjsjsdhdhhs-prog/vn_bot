package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	subscriptionInfoTokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	subscriptionInfoTokenB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newSubscriptionInfoTestServer(db *testutil.DatabaseService) (*Server, *config.Config) {
	cfg := &config.Config{GlobalSubURL: "https://customer.example/sub/"}
	subSvc := service.NewSubscriptionService(db, nil, nil, nil, cfg)

	return NewServer("127.0.0.1:0", db, cfg, "testbot", subSvc, nil), cfg
}

func TestHandleSubscriptionInfo_ValidTokenReturnsSafeResponse(t *testing.T) {
	expiresAt := time.Date(2027, time.March, 4, 5, 6, 7, 0, time.UTC)
	providerID := uint(456)
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(_ context.Context, token string) (*database.Subscription, error) {
		require.Equal(t, subscriptionInfoTokenA, token)

		return &database.Subscription{
			ID:               987654,
			SubscriptionID:   "internal-provisioning-secret",
			Token:            token,
			Status:           string(database.SubscriptionStatusActive),
			ExpiresAt:        &expiresAt,
			ProviderSourceID: &providerID,
			ProviderSource: &database.ProviderSource{
				ID:              providerID,
				SubscriptionURL: "https://upstream.example/private-feed",
				HWID:            "sensitive-hwid",
				UserAgent:       "sensitive-user-agent",
				Headers:         `{"Authorization":"Bearer sensitive-provider-secret"}`,
			},
		}, nil
	}
	srv, cfg := newSubscriptionInfoTestServer(db)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/subscription-info/"+subscriptionInfoTokenA, nil)
	srv.handleSubscriptionInfo(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, string(database.SubscriptionStatusActive), response["status"])
	assert.Equal(t, expiresAt.Format(time.RFC3339), response["expires_at"])
	assert.Equal(t, cfg.SubURL(subscriptionInfoTokenA), response["subscription_url"])
	assert.ElementsMatch(t, []string{"status", "expires_at", "subscription_url"}, mapKeys(response))

	body := recorder.Body.String()
	for _, sensitive := range []string{
		"987654",
		"internal-provisioning-secret",
		"https://upstream.example/private-feed",
		"sensitive-hwid",
		"sensitive-user-agent",
		"sensitive-provider-secret",
	} {
		assert.NotContains(t, body, sensitive)
	}
}

func TestHandleSubscriptionInfo_InvalidOrUnknownTokenReturns404(t *testing.T) {
	db := testutil.NewDatabaseService()
	var lookups atomic.Int32
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		lookups.Add(1)
		return nil, database.ErrSubscriptionNotFound
	}
	srv, _ := newSubscriptionInfoTestServer(db)

	for _, path := range []string{
		"/subscription-info/not-a-token",
		"/subscription-info/" + subscriptionInfoTokenB,
	} {
		recorder := httptest.NewRecorder()
		srv.handleSubscriptionInfo(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		assert.Equal(t, http.StatusNotFound, recorder.Code)
		assert.Equal(t, "Subscription not found\n", recorder.Body.String())
	}
	assert.Equal(t, int32(1), lookups.Load(), "malformed tokens must not reach the repository")
}

func TestHandleSubscriptionInfo_ExpiredAndRevokedAreRepresented(t *testing.T) {
	expiresAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	for _, status := range []database.SubscriptionStatus{
		database.SubscriptionStatusExpired,
		database.SubscriptionStatusRevoked,
	} {
		t.Run(string(status), func(t *testing.T) {
			db := testutil.NewDatabaseService()
			db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
				return &database.Subscription{Token: subscriptionInfoTokenA, Status: string(status), ExpiresAt: &expiresAt}, nil
			}
			srv, _ := newSubscriptionInfoTestServer(db)
			recorder := httptest.NewRecorder()

			srv.handleSubscriptionInfo(recorder, httptest.NewRequest(http.MethodGet, "/subscription-info/"+subscriptionInfoTokenA, nil))

			require.Equal(t, http.StatusOK, recorder.Code)
			var response service.PublicSubscriptionInfo
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, string(status), response.Status)
			assert.Equal(t, expiresAt, *response.ExpiresAt)
		})
	}
}

func TestHandleSubscriptionInfo_WrongTokenCannotResolveAnotherSubscription(t *testing.T) {
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		return &database.Subscription{Token: subscriptionInfoTokenA, SubscriptionID: "another-customer", Status: string(database.SubscriptionStatusActive)}, nil
	}
	srv, _ := newSubscriptionInfoTestServer(db)
	recorder := httptest.NewRecorder()

	srv.handleSubscriptionInfo(recorder, httptest.NewRequest(http.MethodGet, "/subscription-info/"+subscriptionInfoTokenB, nil))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "another-customer")
	assert.NotContains(t, recorder.Body.String(), subscriptionInfoTokenA)
}

func TestHandleSubscriptionInfo_LegacyAndProviderBackedSubscriptions(t *testing.T) {
	providerID := uint(10)
	for name, providerSourceID := range map[string]*uint{
		"legacy":          nil,
		"provider-backed": &providerID,
	} {
		t.Run(name, func(t *testing.T) {
			db := testutil.NewDatabaseService()
			db.GetByTokenFunc = func(_ context.Context, token string) (*database.Subscription, error) {
				return &database.Subscription{
					Token:            token,
					Status:           string(database.SubscriptionStatusActive),
					ProviderSourceID: providerSourceID,
				}, nil
			}
			srv, cfg := newSubscriptionInfoTestServer(db)
			recorder := httptest.NewRecorder()

			srv.handleSubscriptionInfo(recorder, httptest.NewRequest(http.MethodGet, "/subscription-info/"+subscriptionInfoTokenA, nil))

			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), cfg.SubURL(subscriptionInfoTokenA))
			assert.NotContains(t, recorder.Body.String(), "provider_source")
		})
	}
}

func TestSubscriptionInfoRoute_LocalHTTP(t *testing.T) {
	db := testutil.NewDatabaseService()
	db.GetByTokenFunc = func(context.Context, string) (*database.Subscription, error) {
		return &database.Subscription{Token: subscriptionInfoTokenA, Status: string(database.SubscriptionStatusActive)}, nil
	}
	srv, cfg := newSubscriptionInfoTestServer(db)
	require.NoError(t, srv.Start(context.Background()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, srv.Stop(ctx))
	})

	response, err := http.Get("http://" + srv.Addr() + "/subscription-info/" + subscriptionInfoTokenA)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Contains(t, string(body), cfg.SubURL(subscriptionInfoTokenA))
	assert.NotContains(t, string(body), "internal")
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	return keys
}

func TestHandleSubscriptionInfo_MethodNotAllowed(t *testing.T) {
	srv, _ := newSubscriptionInfoTestServer(testutil.NewDatabaseService())
	recorder := httptest.NewRecorder()

	srv.handleSubscriptionInfo(recorder, httptest.NewRequest(http.MethodPost, "/subscription-info/"+strings.Repeat("a", 64), nil))

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, "GET", recorder.Header().Get("Allow"))
}
