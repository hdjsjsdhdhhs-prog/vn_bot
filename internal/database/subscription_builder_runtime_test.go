package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRuntimeTestBuilder(t *testing.T, svc *Service, name string, enabled bool) *SubscriptionBuilder {
	t.Helper()

	b := &SubscriptionBuilder{Name: name, Enabled: true, Version: 1}
	require.NoError(t, svc.db.Create(b).Error)
	if !enabled {
		// gorm skips zero values for columns with a DB default on insert.
		require.NoError(t, svc.db.Model(b).Update("enabled", false).Error)
		b.Enabled = false
	}

	return b
}

func setRuntimeSubBuilder(t *testing.T, svc *Service, sub *Subscription, builderID *uint) {
	t.Helper()
	require.NoError(t, svc.db.Model(&Subscription{}).Where("id = ?", sub.ID).Update("subscription_builder_id", builderID).Error)
	sub.SubscriptionBuilderID = builderID
}

func setRuntimePlanBuilder(t *testing.T, svc *Service, planID uint, builderID *uint) {
	t.Helper()
	require.NoError(t, svc.db.Model(&Plan{}).Where("id = ?", planID).Update("subscription_builder_id", builderID).Error)
}

func TestResolveSubscriptionBuilder_Priority(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type setup struct {
		override        bool
		overrideEnabled bool
		plan            bool
		planEnabled     bool
	}

	for _, tc := range []struct {
		name      string
		setup     setup
		want      string // builder name or "" for nil
		selection BuilderSelection
	}{
		{name: "none keeps existing pipeline", setup: setup{}, want: ""},
		{name: "subscription override", setup: setup{override: true, overrideEnabled: true}, want: "override", selection: BuilderSelectionSubscription},
		{name: "plan default", setup: setup{plan: true, planEnabled: true}, want: "plan", selection: BuilderSelectionPlan},
		{name: "override beats plan", setup: setup{override: true, overrideEnabled: true, plan: true, planEnabled: true}, want: "override", selection: BuilderSelectionSubscription},
		{name: "disabled override falls back to plan", setup: setup{override: true, plan: true, planEnabled: true}, want: "plan", selection: BuilderSelectionPlan},
		{name: "disabled plan keeps existing pipeline", setup: setup{plan: true}, want: ""},
		{name: "all disabled keeps existing pipeline", setup: setup{override: true, plan: true}, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestService(t)
			sub := newProviderSourceTestSubscription(t, svc, 20001, "builder-priority-sub")
			require.NoError(t, svc.CreateSubscription(ctx, sub, ""))

			if tc.setup.override {
				b := newRuntimeTestBuilder(t, svc, "override", tc.setup.overrideEnabled)
				setRuntimeSubBuilder(t, svc, sub, &b.ID)
			}
			if tc.setup.plan {
				b := newRuntimeTestBuilder(t, svc, "plan", tc.setup.planEnabled)
				setRuntimePlanBuilder(t, svc, sub.PlanID, &b.ID)
			}

			// Reload exactly like the /sub handler does.
			stored, err := svc.GetSubscriptionWithProviderSource(ctx, sub.SubscriptionID)
			require.NoError(t, err)

			resolved, err := svc.ResolveSubscriptionBuilder(ctx, stored)
			require.NoError(t, err)

			if tc.want == "" {
				assert.Nil(t, resolved)
				return
			}

			require.NotNil(t, resolved)
			assert.Equal(t, tc.want, resolved.Builder.Name)
			assert.Equal(t, tc.selection, resolved.Selection)
		})
	}
}

func TestResolveSubscriptionBuilder_PreloadsOrderedSourcesAndItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newTestService(t)

	srcA := newProviderSourceTestSource(t, svc, "runtime-a")
	srcB := newProviderSourceTestSource(t, svc, "runtime-b")
	b := newRuntimeTestBuilder(t, svc, "preload", true)

	require.NoError(t, svc.SetBuilderSources(ctx, b.ID, []uint{srcB.ID, srcA.ID}))
	_, err := svc.UpsertBuilderItem(ctx, SubscriptionBuilderItem{BuilderID: b.ID, Kind: BuilderItemKindNode, SourceID: srcA.ID, OriginalName: "second", Position: 2, Enabled: true})
	require.NoError(t, err)
	_, err = svc.UpsertBuilderItem(ctx, SubscriptionBuilderItem{BuilderID: b.ID, Kind: BuilderItemKindCountry, SourceID: srcB.ID, CountryCode: "DE", Position: 1, Enabled: true})
	require.NoError(t, err)

	sub := newProviderSourceTestSubscription(t, svc, 20002, "builder-preload-sub")
	require.NoError(t, svc.CreateSubscription(ctx, sub, ""))
	setRuntimeSubBuilder(t, svc, sub, &b.ID)

	resolved, err := svc.ResolveSubscriptionBuilder(ctx, sub)
	require.NoError(t, err)
	require.NotNil(t, resolved)

	require.Len(t, resolved.Builder.Sources, 2)
	assert.Equal(t, srcB.ID, resolved.Builder.Sources[0].SourceID)
	require.NotNil(t, resolved.Builder.Sources[0].Source)
	assert.Equal(t, srcB.Name, resolved.Builder.Sources[0].Source.Name)
	assert.Equal(t, srcA.ID, resolved.Builder.Sources[1].SourceID)

	require.Len(t, resolved.Builder.Items, 2)
	assert.Equal(t, BuilderItemKindCountry, resolved.Builder.Items[0].Kind)
	assert.Equal(t, "second", resolved.Builder.Items[1].OriginalName)
}

func TestResolveSubscriptionBuilder_NilSubscription(t *testing.T) {
	t.Parallel()

	resolved, err := newTestService(t).ResolveSubscriptionBuilder(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, resolved)
}
