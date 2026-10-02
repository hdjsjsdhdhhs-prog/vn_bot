package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTrialPolicy is a TrialPolicy returning fixed settings and an issuance error.
type fakeTrialPolicy struct {
	settings database.TrialEffective
	issueErr error
	gotDef   database.TrialDefaults
}

func (f *fakeTrialPolicy) GetTrialEffective(_ context.Context, d database.TrialDefaults) (*database.TrialEffective, error) {
	f.gotDef = d
	s := f.settings
	return &s, nil
}

func (f *fakeTrialPolicy) CheckTrialIssuance(_ context.Context, d database.TrialDefaults) (*database.TrialIssuance, error) {
	f.gotDef = d
	return &database.TrialIssuance{Settings: f.settings}, f.issueErr
}

type trialServiceFixture struct {
	svc      *SubscriptionService
	xuiCalls int
	expiry   time.Time
	created  int
}

func newTrialServiceFixture(t *testing.T, policy TrialPolicy) *trialServiceFixture {
	t.Helper()
	f := &trialServiceFixture{}
	cfg := &config.Config{TrialDurationHours: 3, TrialRateLimit: 3}
	db := &testutil.DatabaseService{
		GetPlanByNameFunc: func(_ context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: name, TrafficLimit: 1 << 30}, nil
		},
		CreateTrialSubscriptionFunc: func(_ context.Context, _, subID, clientID string, expiry time.Time) (*database.Subscription, error) {
			f.created++
			f.expiry = expiry
			return &database.Subscription{ID: 1, SubscriptionID: subID, ClientID: clientID, ExpiresAt: &expiry,
				Token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, nil
		},
	}
	xuiClient := &testutil.XUIClient{
		AddClientWithIDFunc: func(_ context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
			f.xuiCalls++
			return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID}, nil
		},
	}
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", InboundIDs: "[1]"}}
	f.svc = NewSubscriptionService(db, map[uint]interfaces.XUIClient{1: xuiClient},
		map[uint]vpn.Client{1: vpn.NewThreeXUIClient(xuiClient, []int{1})}, nodes, cfg)
	if policy != nil {
		f.svc.SetTrialPolicy(policy)
	}
	return f
}

func TestCreateTrial_UsesConfiguredDuration(t *testing.T) {
	t.Parallel()
	policy := &fakeTrialPolicy{settings: database.TrialEffective{Enabled: true, DurationHours: 48, RateLimitPerHour: 3}}
	f := newTrialServiceFixture(t, policy)

	result, err := f.svc.CreateTrial(context.Background(), "invite")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.WithinDuration(t, time.Now().Add(48*time.Hour), f.expiry, time.Minute)
	assert.Equal(t, database.TrialDefaults{DurationHours: 3, RateLimitPerHour: 3}, policy.gotDef,
		"the environment values are the defaults while nothing is stored")
}

func TestCreateTrial_WithoutPolicyKeepsEnvironmentDuration(t *testing.T) {
	t.Parallel()
	f := newTrialServiceFixture(t, nil)
	_, err := f.svc.CreateTrial(context.Background(), "invite")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(3*time.Hour), f.expiry, time.Minute)
}

func TestCreateTrial_DisabledOrUnavailableIssuesNothing(t *testing.T) {
	t.Parallel()
	for _, issueErr := range []error{
		database.ErrTrialDisabled,
		fmt.Errorf("%w: builder_disabled", database.ErrTrialUnavailable),
		fmt.Errorf("%w: empty_result", database.ErrTrialUnavailable),
	} {
		policy := &fakeTrialPolicy{settings: database.TrialEffective{Enabled: true, DurationHours: 3}, issueErr: issueErr}
		f := newTrialServiceFixture(t, policy)
		result, err := f.svc.CreateTrial(context.Background(), "invite")
		require.ErrorIs(t, err, issueErr)
		assert.Nil(t, result)
		assert.Zero(t, f.xuiCalls, "no panel client for %v", issueErr)
		assert.Zero(t, f.created, "no subscription row for %v", issueErr)
	}
}

func TestTrialOffer_Fallbacks(t *testing.T) {
	t.Parallel()
	f := newTrialServiceFixture(t, nil)
	offer, err := f.svc.TrialOffer(context.Background())
	require.NoError(t, err)
	assert.True(t, offer.Enabled)
	assert.Equal(t, 3, offer.DurationHours)
	assert.Equal(t, 3, offer.RateLimitPerHour)

	policy := &fakeTrialPolicy{settings: database.TrialEffective{Enabled: false, DurationHours: 12, RateLimitPerHour: 7, Title: "Попробуйте"}}
	f = newTrialServiceFixture(t, policy)
	offer, err = f.svc.TrialOffer(context.Background())
	require.NoError(t, err)
	assert.False(t, offer.Enabled)
	assert.Equal(t, 7, offer.RateLimitPerHour)
	assert.Equal(t, "Попробуйте", offer.Title)
}

func TestTrialService_NormalizesInput(t *testing.T) {
	t.Parallel()
	in := TrialInput{
		Title: "  Пробный  ", Description: " строка\r\nвторая ", Features: []string{" один ", "", "  "}, Badge: " Хит ",
		Composition: &database.TrialComposition{Mode: " selected ", SourceIDs: []uint{1},
			Rules: []database.TrialRule{{Kind: " country ", SourceID: 1, CountryCode: " de "}}},
	}
	d := in.normalize()
	assert.Equal(t, "Пробный", d.Title)
	assert.Equal(t, "строка\nвторая", d.Description)
	assert.Equal(t, []string{"один"}, d.Features)
	assert.Equal(t, "Хит", d.Badge)
	require.NotNil(t, d.Composition)
	assert.Equal(t, "selected", d.Composition.Mode)
	assert.Equal(t, "country", d.Composition.Rules[0].Kind)
	assert.Equal(t, "DE", d.Composition.Rules[0].CountryCode)
	assert.Equal(t, " de ", in.Composition.Rules[0].CountryCode, "the caller's input is not mutated")
}
