package subserver

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuilder_E2E_SavedConfigurationIsServed drives the admin save path
// (database layer used by /admin/api/builders*) and /sub through the real
// SQLite repository: what the admin list reads (ListBuilders) is what /sub
// serves, for the trial plan builder and for a per-subscription override.
func TestBuilder_E2E_SavedConfigurationIsServed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, err := database.NewService(filepath.Join(t.TempDir(), "builder-admin-e2e.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	keySeq := 0
	meta := func() database.AdminConfigMeta {
		keySeq++
		return database.AdminConfigMeta{Actor: "test-admin", RequestKey: fmt.Sprintf("admin-e2e-%d", keySeq)}
	}
	newSource := func(name string, links ...string) *database.ProviderSource {
		up := newBuilderTestUpstream(t, http.StatusOK, nil, strings.Join(links, "\n"))
		src := &database.ProviderSource{Name: name, Type: "auto", SubscriptionURL: up.srv.URL, Headers: `{}`, Enabled: true}
		require.NoError(t, svc.CreateProviderSource(ctx, src))
		return src
	}

	srcA := newSource("Provider A",
		testLink("a", "de1.example", "🇩🇪 Frankfurt"),
		testLink("a", "nl.example", "🇳🇱 Amsterdam"),
		testLink("a", "de2.example", "🇩🇪 Berlin"),
		testLink("a", "fi.example", "🇫🇮 Helsinki"),
	)
	srcB := newSource("Provider B",
		testLink("b", "us.example", "🇺🇸 Dallas"),
		testLink("b", "de3.example", "🇩🇪 Munich"),
	)
	legacy := newSource("Legacy provider", testLink("l", "legacy.example", "Legacy"))

	created, err := svc.CreateBuilder(ctx, meta(), database.BuilderCreateInput{Name: "FINLAN - пробная", Description: "trial", Enabled: true})
	require.NoError(t, err)
	builderID := created.TargetID
	require.NoError(t, svc.SetBuilderSources(ctx, builderID, []uint{srcA.ID, srcB.ID}))
	usRule := database.SubscriptionBuilderItem{BuilderID: builderID, Kind: database.BuilderItemKindCountry, SourceID: srcB.ID, CountryCode: "US", Position: 1, Enabled: true}
	for _, it := range []*database.SubscriptionBuilderItem{
		{BuilderID: builderID, Kind: database.BuilderItemKindCountry, SourceID: srcA.ID, CountryCode: "DE", Position: 0, Enabled: true},
		&usRule,
		{BuilderID: builderID, Kind: database.BuilderItemKindNode, SourceID: srcA.ID, OriginalName: "🇳🇱 Amsterdam", Position: 2, Enabled: true},
	} {
		saved, err := svc.UpsertBuilderItem(ctx, *it)
		require.NoError(t, err)
		it.ID = saved.ID
	}

	// The admin list reads the stored configuration (regression: it used to
	// return the builder without sources and items).
	listed, err := svc.ListBuilders(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Len(t, listed[0].Sources, 2)
	require.Len(t, listed[0].Items, 3)
	assert.Equal(t, []uint{srcA.ID, srcB.ID}, []uint{listed[0].Sources[0].SourceID, listed[0].Sources[1].SourceID})
	assert.Equal(t, []string{"DE", "US", ""}, []string{listed[0].Items[0].CountryCode, listed[0].Items[1].CountryCode, listed[0].Items[2].CountryCode})

	// Trial plan default builder + an anonymous trial subscription.
	trialPlan, err := svc.GetPlanByName(ctx, database.TrialPlanName)
	require.NoError(t, err)
	_, err = svc.SetPlanBuilder(ctx, meta(), trialPlan.ID, &builderID)
	require.NoError(t, err)
	trial, err := svc.CreateTrialSubscription(ctx, "", "admin-e2e-trial", "admin-e2e-trial-client", time.Now().Add(3*time.Hour))
	require.NoError(t, err)

	// A paid subscription with the builder as its override.
	freePlan, err := svc.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	paid := &database.Subscription{
		TelegramID: 515151, ClientID: "admin-e2e-paid-client", SubscriptionID: "admin-e2e-paid",
		ExpiresAt: timePointer(time.Now().Add(time.Hour)), Status: string(database.SubscriptionStatusActive),
		PlanID: freePlan.ID, ProviderSourceID: &legacy.ID,
	}
	require.NoError(t, svc.CreateSubscription(ctx, paid, ""))
	_, err = svc.SetSubscriptionBuilder(ctx, meta(), paid.ID, &builderID)
	require.NoError(t, err)

	subSvc := newTestSubSvc(t)
	serve := func(subID string) ([]string, error) {
		t.Helper()
		result, _, _, err := HandleSubscription(ctx, svc, subSvc, subID, "", nil)
		if err != nil {
			return nil, err
		}
		return linkNames(decodeBuilderLinks(t, result)), nil
	}
	mustServe := func(subID string) []string {
		t.Helper()
		names, err := serve(subID)
		require.NoError(t, err)
		return names
	}

	// Rule order: DE of A (both), US of B, the Amsterdam node of A. Munich (DE
	// of B) and Helsinki are not selected.
	want := []string{"🇩🇪 Frankfurt", "🇩🇪 Berlin", "🇺🇸 Dallas", "🇳🇱 Amsterdam"}
	assert.Equal(t, want, mustServe(trial.SubscriptionID), "trial plan builder")
	assert.Equal(t, want, mustServe(paid.SubscriptionID), "subscription override")

	// A metadata save (the editor's PATCH) keeps sources and rules.
	current, err := svc.GetBuilder(ctx, builderID)
	require.NoError(t, err)
	_, err = svc.UpdateBuilder(ctx, meta(), database.BuilderUpdateInput{
		ID: builderID, Version: current.Version, Name: current.Name, Description: "trial, edited", Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, want, mustServe(trial.SubscriptionID), "after metadata save")

	// A disabled rule is skipped.
	usRule.Enabled = false
	_, err = svc.UpsertBuilderItem(ctx, usRule)
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 Frankfurt", "🇩🇪 Berlin", "🇳🇱 Amsterdam"}, mustServe(trial.SubscriptionID), "disabled rule")

	// What the old editor did when reopened from the list copy: saving an
	// empty source list. The builder then serves nothing — and the list says so.
	require.NoError(t, svc.SetBuilderSources(ctx, builderID, nil))
	_, err = serve(trial.SubscriptionID)
	assert.ErrorIs(t, err, ErrNoSubscriptionItems)
	listed, err = svc.ListBuilders(ctx)
	require.NoError(t, err)
	assert.Empty(t, listed[0].Sources)
	assert.Len(t, listed[0].Items, 3, "rules stay stored")
	require.NoError(t, svc.SetBuilderSources(ctx, builderID, []uint{srcA.ID, srcB.ID}))
	assert.Equal(t, []string{"🇩🇪 Frankfurt", "🇩🇪 Berlin", "🇳🇱 Amsterdam"}, mustServe(trial.SubscriptionID), "sources restored")

	// The editor's atomic configuration save is what /sub serves: DE of B,
	// then the Helsinki server of A; the switched-off NL rule is skipped.
	current, err = svc.GetBuilder(ctx, builderID)
	require.NoError(t, err)
	_, err = svc.SaveBuilderConfiguration(ctx, meta(), database.BuilderConfigurationInput{
		ID: builderID, Version: current.Version, Name: current.Name, Enabled: true,
		SourceIDs: []uint{srcB.ID, srcA.ID},
		Items: []database.BuilderConfigItem{
			{Kind: database.BuilderItemKindCountry, SourceID: srcB.ID, CountryCode: "de", Enabled: true},
			{Kind: database.BuilderItemKindNode, SourceID: srcA.ID, OriginalName: "🇫🇮 Helsinki", Enabled: true},
			{Kind: database.BuilderItemKindCountry, SourceID: srcA.ID, CountryCode: "NL", Enabled: false},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 Munich", "🇫🇮 Helsinki"}, mustServe(trial.SubscriptionID), "atomic configuration save")
	// A stale save (the version read above) is refused; /sub is unchanged.
	_, err = svc.SaveBuilderConfiguration(ctx, meta(), database.BuilderConfigurationInput{
		ID: builderID, Version: current.Version, Name: current.Name, Enabled: true, SourceIDs: []uint{}, Items: []database.BuilderConfigItem{},
	})
	assert.ErrorIs(t, err, database.ErrBuilderVersionConflict)
	assert.Equal(t, []string{"🇩🇪 Munich", "🇫🇮 Helsinki"}, mustServe(trial.SubscriptionID), "after a stale save")

	// A builder without configuration serves nothing (no silent fallback).
	empty, err := svc.CreateBuilder(ctx, meta(), database.BuilderCreateInput{Name: "empty", Enabled: true})
	require.NoError(t, err)
	_, err = svc.SetSubscriptionBuilder(ctx, meta(), paid.ID, &empty.TargetID)
	require.NoError(t, err)
	_, err = serve(paid.SubscriptionID)
	assert.ErrorIs(t, err, ErrNoSubscriptionItems, "builder without sources")

	// A disabled builder is skipped: the subscription falls back to the next
	// level (no plan default on free → its own ProviderSource).
	_, err = svc.SetSubscriptionBuilder(ctx, meta(), paid.ID, &builderID)
	require.NoError(t, err)
	_, err = svc.SetBuilderEnabled(ctx, builderID, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"Legacy"}, mustServe(paid.SubscriptionID), "disabled builder")
}
