package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type journalPageWire struct {
	Events []struct {
		ID             uint           `json:"id"`
		CreatedAt      time.Time      `json:"created_at"`
		EventType      string         `json:"event_type"`
		Outcome        string         `json:"outcome"`
		Actor          string         `json:"actor"`
		ActorName      string         `json:"actor_name"`
		TelegramID     int64          `json:"telegram_id"`
		SubscriptionID *uint          `json:"subscription_id"`
		PlanKind       string         `json:"plan_kind"`
		Description    string         `json:"description"`
		Details        map[string]any `json:"details"`
	} `json:"events"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

func (f *adminAPIFixture) journal(query url.Values) journalPageWire {
	f.t.Helper()
	path := "/admin/api/journal"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	resp := f.do(http.MethodGet, path)
	require.Equal(f.t, http.StatusOK, resp.StatusCode)
	var page journalPageWire
	f.decode(resp, &page)
	return page
}

func TestAdminAPI_Journal(t *testing.T) {
	f := newAdminAPIFixture(t)

	t.Run("requires a session", func(t *testing.T) {
		resp := f.doAs(f.anon, http.MethodGet, "/admin/api/journal")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	f.login()

	// The fixture created two customers and one anonymous trial.
	initial := f.journal(nil)
	require.EqualValues(t, 3, initial.Total)
	assert.Equal(t, database.AdminDefaultPageSize, initial.Limit)
	assert.Equal(t, string(database.JournalTrialStarted), initial.Events[0].EventType, "newest first")

	t.Run("refreshing never creates events", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			assert.EqualValues(t, 3, f.journal(nil).Total)
		}
	})

	t.Run("admin action appears on top with admin actor", func(t *testing.T) {
		resp := f.do(http.MethodPost, f.subscriptionPath(f.active.ID)+"/disable", f.withCSRF(), withJSON(`{"request_key":"journal-api-disable"}`))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		// Retried with the same key: replayed, not journaled again.
		resp = f.do(http.MethodPost, f.subscriptionPath(f.active.ID)+"/disable", f.withCSRF(), withJSON(`{"request_key":"journal-api-disable"}`))
		require.Equal(t, http.StatusOK, resp.StatusCode)

		page := f.journal(nil)
		require.EqualValues(t, 4, page.Total)
		top := page.Events[0]
		assert.Equal(t, string(database.JournalDisabled), top.EventType)
		assert.Equal(t, database.JournalActorAdmin, top.Actor)
		assert.Equal(t, f.s.cfg.AdminUsername, top.ActorName)
		require.NotNil(t, top.SubscriptionID)
		assert.Equal(t, f.active.ID, *top.SubscriptionID)
		assert.Equal(t, "paused", top.Details["after"].(map[string]any)["status"])

		resp = f.do(http.MethodGet, "/admin/api/journal/"+strconv.FormatUint(uint64(top.ID), 10))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		detail := f.raw(resp)
		assert.Equal(t, top.Description, detail["description"])
		assert.IsType(t, map[string]any{}, detail["details"], "details are a JSON object, not a string")
		assert.NotContains(t, detail, "dedup_key")
	})

	t.Run("filters", func(t *testing.T) {
		byType := f.journal(url.Values{"type": {string(database.JournalUserRegistered)}})
		assert.EqualValues(t, 2, byType.Total)
		byKind := f.journal(url.Values{"plan_kind": {database.PlanKindTrial}})
		assert.EqualValues(t, 1, byKind.Total)
		byUser := f.journal(url.Values{"q": {strconv.FormatInt(f.active.TelegramID, 10)}})
		assert.EqualValues(t, 2, byUser.Total)
		paged := f.journal(url.Values{"limit": {"1"}, "offset": {"1"}})
		require.Len(t, paged.Events, 1)
		assert.EqualValues(t, 4, paged.Total)
		future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
		assert.Zero(t, f.journal(url.Values{"from": {future}}).Total)
	})

	t.Run("rejections", func(t *testing.T) {
		for _, query := range []string{"type=unknown", "plan_kind=gold", "from=yesterday", "limit=-1", "offset=x",
			"from=2026-09-02T00:00:00Z&to=2026-09-01T00:00:00Z"} {
			f.expectError(f.do(http.MethodGet, "/admin/api/journal?"+query), http.StatusBadRequest, "invalid_request")
		}
		f.expectError(f.do(http.MethodGet, "/admin/api/journal/999999"), http.StatusNotFound, "not_found")
		f.expectError(f.do(http.MethodGet, "/admin/api/journal/007"), http.StatusNotFound, "not_found")
		for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
			resp := f.do(method, "/admin/api/journal/1", f.withCSRF(), withJSON(`{}`))
			f.expectError(resp, http.StatusMethodNotAllowed, "method_not_allowed")
		}
		f.expectError(f.do(http.MethodPost, "/admin/api/journal", f.withCSRF(), withJSON(`{}`)), http.StatusMethodNotAllowed, "method_not_allowed")
	})
}
