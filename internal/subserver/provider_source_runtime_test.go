package subserver

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSubscription_ProviderSource_LegacyPathUnchanged(t *testing.T) {
	t.Parallel()

	legacyUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("vless://legacy@legacy.example:443#Legacy"))
	}))
	defer legacyUpstream.Close()

	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return activeRuntimeSubscription("legacy-runtime", nil), nil
	}

	var legacyLoads atomic.Int32
	db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
		legacyLoads.Add(1)

		return &database.SubscriptionFull{
			Subscription: *activeRuntimeSubscription("legacy-runtime", nil),
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Nodes: []database.Node{{
				ID:              1,
				Name:            "legacy",
				SubscriptionURL: legacyUpstream.URL + "/",
			}},
		}, nil
	}

	result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), "legacy-runtime", "", nil)
	require.NoError(t, err)
	assert.Equal(t, int32(1), legacyLoads.Load())
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, total)
	decoded, err := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, err)
	assert.Contains(t, string(decoded), "vless://legacy@legacy.example")
}

func TestHandleSubscription_ProviderSource_UsesConfiguredRequestAndHidesCredentials(t *testing.T) {
	t.Parallel()

	const (
		hwid          = "provider-secret-hwid"
		userAgent     = "provider-secret-agent/1.0"
		authorization = "Bearer provider-secret-token"
	)

	received := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.Header().Set("X-HWID", hwid)
		w.Header().Set("X-Provider-Echo", authorization)
		w.Header().Set("X-Custom-Echo", "iOS")
		w.Header().Set("Profile-Web-Page-Url", "prefix "+r.URL.String())
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=1000; expire=1893456000")
		_, _ = w.Write([]byte("vless://provider@provider.example:443#Provider"))
	}))
	defer upstream.Close()

	sourceID := uint(41)
	source := &database.ProviderSource{
		ID:              sourceID,
		Name:            "RelayHub",
		Type:            "relayhub",
		SubscriptionURL: upstream.URL + "/subscription/opaque-token",
		HWID:            hwid,
		UserAgent:       userAgent,
		Headers:         `{"Authorization":"Bearer provider-secret-token","X-Device-OS":"iOS"}`,
		Enabled:         true,
		UpdatedAt:       time.Unix(100, 0),
	}

	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return activeRuntimeSubscription("provider-runtime", source), nil
	}
	db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
		t.Fatal("ProviderSource path must not load legacy plan nodes")
		return nil, errors.New("unexpected legacy path")
	}

	result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), "provider-runtime", "", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, total)

	requestHeaders := <-received
	assert.Equal(t, hwid, requestHeaders.Get("X-HWID"))
	assert.Equal(t, userAgent, requestHeaders.Get("User-Agent"))
	assert.Equal(t, authorization, requestHeaders.Get("Authorization"))
	assert.Equal(t, "iOS", requestHeaders.Get("X-Device-OS"))

	decoded, err := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, err)
	assert.Contains(t, string(decoded), "vless://provider@provider.example")
	assert.Equal(t, "upload=10; download=20; total=1000; expire=1893456000", result.Headers["subscription-userinfo"])

	clientVisible := string(result.Body) + headersAsString(result.Headers)
	for _, secret := range []string{source.SubscriptionURL, hwid, userAgent, authorization, "iOS"} {
		assert.NotContains(t, clientVisible, secret)
	}
}

func TestFetchFromProviderSource_FiltersPrivateURLReferences(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		header string
		omit   bool
	}{
		{"root relative", "/private/feed", true},
		{"path relative", "feed", true},
		{"normalized relative", "./feed", true},
		{"fragment", "#profile", true},
		{"query", "?view=1", true},
		{"public absolute", "https://support.example/help", false},
		{"public relative", "/help", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Profile-Web-Page-Url", tc.header)
				w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=1000")
				_, _ = w.Write([]byte("vless://connection@vpn.example:443#Customer"))
			}))
			defer upstream.Close()

			response, err := FetchFromProviderSource(context.Background(), database.ProviderSource{
				SubscriptionURL: upstream.URL + "/private/feed",
				Enabled:         true,
			})
			require.NoError(t, err)
			if tc.omit {
				assert.NotContains(t, response.Headers, "profile-web-page-url")
			} else {
				assert.Equal(t, tc.header, response.Headers["profile-web-page-url"])
			}
			assert.Equal(t, "upload=10; download=20; total=1000", response.Headers["subscription-userinfo"])
			assert.Equal(t, "vless://connection@vpn.example:443#Customer", string(response.Body))
		})
	}
}

func TestFetchFromProviderSource_RootURLPreservesMetadata(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Profile-Web-Page-Url", "/")
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=1000")
		w.Header().Set("Profile-Update-Interval", "12")
		_, _ = w.Write([]byte("vless://connection@vpn.example:443#Customer"))
	}))
	defer upstream.Close()

	response, err := FetchFromProviderSource(context.Background(), database.ProviderSource{
		SubscriptionURL: upstream.URL,
		Enabled:         true,
	})
	require.NoError(t, err)
	assert.NotContains(t, response.Headers, "profile-web-page-url")
	assert.Equal(t, "upload=10; download=20; total=1000", response.Headers["subscription-userinfo"])
	assert.Equal(t, "12", response.Headers["profile-update-interval"])
}

func TestFetchFromProviderSource_CrossHostRedirectStripsCredentials(t *testing.T) {
	t.Parallel()

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("X-HWID"))
		assert.NotEqual(t, "provider-secret-agent", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("vless://provider@provider.example:443#Provider"))
	}))
	defer redirectTarget.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer redirector.Close()

	response, err := FetchFromProviderSource(context.Background(), database.ProviderSource{
		ID:              46,
		SubscriptionURL: redirector.URL,
		HWID:            "provider-secret-hwid",
		UserAgent:       "provider-secret-agent",
		Headers:         `{"Authorization":"Bearer provider-secret-token"}`,
		Enabled:         true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, response.Body)
}

func TestHandleSubscription_ProviderSource_MissingDoesNotFallBack(t *testing.T) {
	t.Parallel()

	sourceID := uint(42)
	sub := activeRuntimeSubscription("missing-provider", nil)
	sub.ProviderSourceID = &sourceID

	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return sub, nil
	}

	var legacyLoads atomic.Int32
	db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
		legacyLoads.Add(1)
		return nil, errors.New("unexpected legacy path")
	}

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrProviderSourceUnavailable)
	assert.Equal(t, int32(0), legacyLoads.Load())
}

func TestHandleSubscription_ProviderSource_DisabledDoesNotFetchOrFallBack(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("vless://must-not-be-served@example:443"))
	}))
	defer upstream.Close()

	source := &database.ProviderSource{
		ID:              43,
		SubscriptionURL: upstream.URL,
		Headers:         `{}`,
		Enabled:         false,
	}
	db := providerRuntimeDatabase("disabled-provider", source)
	db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
		t.Fatal("disabled ProviderSource must not fall back to legacy nodes")
		return nil, errors.New("unexpected legacy path")
	}

	result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), "disabled-provider", "", nil)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrProviderSourceUnavailable)
	assert.Zero(t, requests.Load())
}

func TestHandleSubscription_ProviderSource_FetchErrorIsCredentialSafe(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider-secret-hwid provider-secret-agent", http.StatusBadGateway)
	}))
	defer upstream.Close()

	source := &database.ProviderSource{
		ID:              44,
		SubscriptionURL: upstream.URL + "/upstream-secret-url",
		HWID:            "provider-secret-hwid",
		UserAgent:       "provider-secret-agent",
		Headers:         `{"Authorization":"Bearer provider-secret-token"}`,
		Enabled:         true,
	}
	db := providerRuntimeDatabase("provider-fetch-error", source)

	result, success, total, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), "provider-fetch-error", "", nil)
	assert.Nil(t, result)
	assert.Equal(t, 0, success)
	assert.Equal(t, 1, total)
	assert.ErrorIs(t, err, ErrProviderSourceUnavailable)

	errorText := err.Error()
	for _, secret := range []string{source.SubscriptionURL, source.HWID, source.UserAgent, "provider-secret-token"} {
		assert.NotContains(t, errorText, secret)
	}
}

func TestHandleSubscription_ProviderSource_ExpiredOrRevokedIsNotServed(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		status string
		expiry *time.Time
	}{
		{name: "revoked", status: string(database.SubscriptionStatusRevoked)},
		{name: "expired", status: string(database.SubscriptionStatusActive), expiry: timePointer(time.Now().Add(-time.Minute))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := &database.ProviderSource{ID: 45, SubscriptionURL: "https://provider.invalid/sub", Headers: `{}`, Enabled: true}
			sub := activeRuntimeSubscription("provider-"+testCase.name, source)
			sub.Status = testCase.status
			sub.ExpiresAt = testCase.expiry

			db := testutil.NewDatabaseService()
			db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
				return sub, nil
			}

			result, _, _, err := HandleSubscription(context.Background(), db, newTestSubSvc(t), sub.SubscriptionID, "", nil)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, ErrSubscriptionNotFound)
		})
	}
}

func providerRuntimeDatabase(subID string, source *database.ProviderSource) *testutil.DatabaseService {
	db := testutil.NewDatabaseService()
	db.GetSubscriptionWithProviderSourceFunc = func(context.Context, string) (*database.Subscription, error) {
		return activeRuntimeSubscription(subID, source), nil
	}

	return db
}

func activeRuntimeSubscription(subID string, source *database.ProviderSource) *database.Subscription {
	sub := &database.Subscription{
		ID:             1,
		SubscriptionID: subID,
		Status:         string(database.SubscriptionStatusActive),
	}
	if source != nil {
		sub.ProviderSourceID = &source.ID
		sub.ProviderSource = source
	}

	return sub
}

func headersAsString(headers map[string]string) string {
	var result strings.Builder
	for key, value := range headers {
		result.WriteString(key)
		result.WriteString(value)
	}

	return result.String()
}

func timePointer(value time.Time) *time.Time {
	return &value
}
