package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// webTrialPolicy is a fixed admin trial configuration for the invite page.
type webTrialPolicy struct {
	settings database.TrialEffective
	issueErr error
}

func (p *webTrialPolicy) GetTrialEffective(context.Context, database.TrialDefaults) (*database.TrialEffective, error) {
	s := p.settings
	return &s, nil
}

func (p *webTrialPolicy) CheckTrialIssuance(context.Context, database.TrialDefaults) (*database.TrialIssuance, error) {
	return &database.TrialIssuance{Settings: p.settings}, p.issueErr
}

type inviteTrialFixture struct {
	srv          *Server
	db           *testutil.DatabaseService
	cfg          *config.Config
	rateChecks   int
	rateRecorded int
	created      int
	panelCalls   int
	requests     int
}

func newInviteTrialFixture(t *testing.T, policy *webTrialPolicy, priorRequests int) *inviteTrialFixture {
	t.Helper()
	f := &inviteTrialFixture{requests: priorRequests}
	mockDB := testutil.NewDatabaseService()
	cfg, subService, mockXUI := makeTestSubService(mockDB)
	f.cfg, f.db = cfg, mockDB
	mockDB.GetInviteByCodeFunc = func(context.Context, string) (*database.Invite, error) {
		return &database.Invite{Code: "trialcode", ReferrerTGID: 1}, nil
	}
	mockDB.CountTrialRequestsByIPLastHourFunc = func(context.Context, string) (int, error) {
		f.rateChecks++
		return f.requests, nil
	}
	mockDB.CreateTrialRequestFunc = func(context.Context, string) error {
		f.rateRecorded++
		return nil
	}
	mockDB.CreateTrialSubscriptionFunc = func(_ context.Context, inviteCode, subID, clientID string, expiry time.Time) (*database.Subscription, error) {
		f.created++
		return &database.Subscription{SubscriptionID: subID, ClientID: clientID, InviteCode: &inviteCode, ExpiresAt: &expiry}, nil
	}
	mockXUI.AddClientWithIDFunc = func(_ context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		f.panelCalls++
		return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
	}
	if policy != nil {
		subService.SetTrialPolicy(policy)
	}
	f.srv = NewServer(":8880", mockDB, cfg, "testbot", subService, nil)
	return f
}

func (f *inviteTrialFixture) get() *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/i/trialcode", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	f.srv.handleInvite(rec, req)
	return rec
}

// getWithTrialCookie is get() by a visitor whose cookie names an issued trial.
func (f *inviteTrialFixture) getWithTrialCookie(subID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/i/trialcode", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.AddCookie(&http.Cookie{Name: "rs8kvn_trial_trialcode", Value: subID})
	rec := httptest.NewRecorder()
	f.srv.handleInvite(rec, req)
	return rec
}

// issuedTrial makes the fixture database know one anonymous trial.
func (f *inviteTrialFixture) issuedTrial(t *testing.T, expiresAt *time.Time) {
	t.Helper()
	mockDB := f.db
	mockDB.GetTrialSubscriptionBySubIDFunc = func(_ context.Context, subID string) (*database.Subscription, error) {
		return &database.Subscription{SubscriptionID: subID, PlanID: 7, TelegramID: -1, ExpiresAt: expiresAt,
			Token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, nil
	}
	mockDB.GetPlanByIDFunc = func(_ context.Context, id uint) (*database.Plan, error) {
		return &database.Plan{ID: id, Name: database.TrialPlanName}, nil
	}
}

func TestHandleInvite_IssuedTrialShowsItsOwnExpiry(t *testing.T) {
	t.Parallel()
	// Issued 2 hours before its end (stored in a non-UTC zone); the admin has
	// set 72 hours since.
	expiry := time.Now().Add(2 * time.Hour).Truncate(time.Minute).In(time.FixedZone("MSK", 3*3600))
	for _, issueErr := range []error{nil, database.ErrTrialDisabled} {
		policy := &webTrialPolicy{settings: database.TrialEffective{Enabled: issueErr == nil, DurationHours: 72, RateLimitPerHour: 3, Title: "Пробный"}, issueErr: issueErr}
		f := newInviteTrialFixture(t, policy, 0)
		f.issuedTrial(t, &expiry)

		rec := f.getWithTrialCookie("issued-sub")
		require.Equal(t, http.StatusOK, rec.Code, "an issued trial is shown even when the trial is switched off (%v)", issueErr)
		body := rec.Body.String()
		want := `<span class="info-value">до ` + expiry.UTC().Format("02.01.2006 15:04") + ` UTC</span>`
		assert.Contains(t, body, want, "the term is the subscription's own expires_at, in UTC")
		assert.NotContains(t, body, "72 часа", "not the duration configured now")
		assert.Contains(t, body, "issued-sub", "the issued subscription is the one shown")
		assert.Contains(t, body, "Пробный", "the landing card still comes from the settings")
		// Issuance and eligibility are untouched: nothing new is created or counted.
		assert.Zero(t, f.created)
		assert.Zero(t, f.panelCalls)
		assert.Zero(t, f.rateChecks+f.rateRecorded)
		assert.Empty(t, rec.Result().Cookies(), "the cookie of the issued trial is not re-issued")
	}
}

func TestHandleInvite_WithoutActiveTrialShowsCurrentSettings(t *testing.T) {
	t.Parallel()
	policy := &webTrialPolicy{settings: database.TrialEffective{Enabled: true, DurationHours: 72, RateLimitPerHour: 3}}

	// The cookie's trial has expired: a new trial is issued with the duration
	// configured now, exactly as for a new visitor.
	f := newInviteTrialFixture(t, policy, 0)
	f.issuedTrial(t, testutil.PtrTime(time.Now().Add(-time.Minute)))
	rec := f.getWithTrialCookie("expired-sub")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `<span class="info-value">72 часа</span>`)
	assert.NotContains(t, rec.Body.String(), "info-value\">до ")
	assert.Equal(t, 1, f.created)
	assert.Equal(t, 1, f.rateChecks, "the usual eligibility check applies")

	// A new visitor: the current settings too.
	f = newInviteTrialFixture(t, policy, 0)
	rec = f.get()
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `<span class="info-value">72 часа</span>`)
	assert.Equal(t, 1, f.created)

	// A legacy issued row without expires_at has no own term to show: the
	// configured duration is shown, nothing is issued.
	f = newInviteTrialFixture(t, policy, 0)
	f.issuedTrial(t, nil)
	rec = f.getWithTrialCookie("legacy-sub")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `<span class="info-value">72 часа</span>`)
	assert.Zero(t, f.created)
}

func TestTrialExpiryText(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 2, 18, 4, 59, 0, time.FixedZone("MSK", 3*3600))
	assert.Equal(t, "до 02.10.2026 15:04 UTC", trialExpiryText(at))
}

func TestHandleInvite_TrialDisabledOrUnavailable503(t *testing.T) {
	t.Parallel()
	for _, issueErr := range []error{database.ErrTrialDisabled, fmt.Errorf("%w: empty_result", database.ErrTrialUnavailable)} {
		f := newInviteTrialFixture(t, &webTrialPolicy{settings: database.TrialEffective{Enabled: true, DurationHours: 3, RateLimitPerHour: 3}, issueErr: issueErr}, 0)
		rec := f.get()
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, issueErr.Error())
		assert.Contains(t, rec.Body.String(), "Пробный доступ временно недоступен")
		assert.Zero(t, f.rateChecks+f.rateRecorded, "the IP quota is not consumed")
		assert.Zero(t, f.created, "no subscription row")
		assert.Zero(t, f.panelCalls, "no panel client")
	}
}

func TestHandleInvite_UsesConfiguredOffer(t *testing.T) {
	t.Parallel()
	policy := &webTrialPolicy{settings: database.TrialEffective{
		Enabled: true, DurationHours: 21, RateLimitPerHour: 10,
		Title: "Пробный <доступ>", Description: "Попробуйте бесплатно", Features: []string{"Без карты", "Все страны"}, Badge: "Бесплатно",
	}}
	// 5 earlier requests: over the environment limit (3), under the configured one (10).
	f := newInviteTrialFixture(t, policy, 5)
	rec := f.get()
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Equal(t, 1, f.created)
	assert.Contains(t, body, "21 час<", "duration and Russian plural come from the offer")
	assert.Contains(t, body, "Пробный &lt;доступ&gt;", "text is escaped")
	assert.Contains(t, body, "Попробуйте бесплатно")
	assert.Contains(t, body, "<li style=\"padding: 6px 0; border-bottom: 1px solid #eee;\">Все страны</li>")
	assert.Contains(t, body, `class="offer-badge"`)
	cookie := rec.Result().Cookies()
	require.Len(t, cookie, 1)
	assert.WithinDuration(t, time.Now().Add(21*time.Hour), cookie[0].Expires, time.Minute)
}

func TestHandleInvite_ConfiguredRateLimitStillApplies(t *testing.T) {
	t.Parallel()
	policy := &webTrialPolicy{settings: database.TrialEffective{Enabled: true, DurationHours: 3, RateLimitPerHour: 2}}
	f := newInviteTrialFixture(t, policy, 2)
	rec := f.get()
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Zero(t, f.created)
}

func TestRenderTrialPage_DefaultOutputUnchanged(t *testing.T) {
	t.Parallel()
	srv := NewServer(":8880", nil, &config.Config{SiteURL: "https://vpn.site", TrialDurationHours: 3}, "testbot", nil, nil)
	w := httptest.NewRecorder()
	srv.renderTrialPage(w, "sub1", "https://example.com/sub", "https://t.me/testbot?start=trial_sub1", 3)
	html := w.Body.String()
	assert.Contains(t, html, "<img class=\"logo\" src=\"/static/logo.png\">\n\n        <h2>📱 Скачайте Happ</h2>",
		"without an admin card the page is byte-identical around the logo")
	assert.NotContains(t, html, `class="offer`)
	assert.Contains(t, html, `<span class="info-value">3 часа</span>`)
}

func TestTrialHoursText(t *testing.T) {
	t.Parallel()
	for hours, want := range map[int]string{1: "1 час", 2: "2 часа", 5: "5 часов", 11: "11 часов", 21: "21 час", 24: "24 часа", 168: "168 часов"} {
		assert.Equal(t, want, trialHoursText(hours))
	}
	assert.False(t, strings.Contains(trialHoursText(12), "часа"))
}
