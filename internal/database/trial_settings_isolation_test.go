package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Trial editor isolation: no trial mutation may change a paid tariff, a plan
// other than "trial", an existing subscription (paid, free or an already
// issued trial) or a builder the trial does not own. Plus the edge cases of
// copy, preview and composition replacement not covered by
// trial_settings_test.go.

// tableRows reads every row of a table (optionally filtered) in a stable order.
func tableRows(t *testing.T, svc *Service, table, order, where string, args ...any) []map[string]any {
	t.Helper()
	var rows []map[string]any
	q := svc.db.Table(table).Order(order)
	if where != "" {
		q = q.Where(where, args...)
	}
	require.NoError(t, q.Find(&rows).Error)
	return rows
}

type trialIsolationSnapshot struct {
	products, otherPlans, trialPlanRest, subscriptions, builders, builderSources, builderItems []map[string]any
}

// snapshotOutsideTrial captures everything a trial mutation must not touch:
// the trial plan except its builder column (and updated_at, which GORM moves
// with it), and every builder except owned.
func snapshotOutsideTrial(t *testing.T, svc *Service, owned ...uint) trialIsolationSnapshot {
	t.Helper()
	if len(owned) == 0 {
		owned = []uint{0}
	}
	trialPlan := tableRows(t, svc, "plans", "id", "name = ?", TrialPlanName)
	for _, row := range trialPlan {
		delete(row, "subscription_builder_id")
		delete(row, "updated_at")
	}
	return trialIsolationSnapshot{
		products:       tableRows(t, svc, "products", "id", ""),
		otherPlans:     tableRows(t, svc, "plans", "id", "name <> ?", TrialPlanName),
		trialPlanRest:  trialPlan,
		subscriptions:  tableRows(t, svc, "subscriptions", "id", ""),
		builders:       tableRows(t, svc, "subscription_builders", "id", "id NOT IN ?", owned),
		builderSources: tableRows(t, svc, "subscription_builder_sources", "builder_id, source_id", "builder_id NOT IN ?", owned),
		builderItems:   tableRows(t, svc, "subscription_builder_items", "id", "builder_id NOT IN ?", owned),
	}
}

func TestTrial_MutationsLeavePaidTariffsAndSubscriptionsUntouched(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	db := f.svc.db

	// A paid plan with its own builder (source B, Japan only) and a tariff.
	paidBuilder := newRuntimeTestBuilder(t, f.svc, "paid-"+t.Name(), true)
	require.NoError(t, f.svc.SetBuilderSources(f.ctx, paidBuilder.ID, []uint{f.b.ID}))
	_, err := f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: paidBuilder.ID, Kind: BuilderItemKindCountry, SourceID: f.b.ID, CountryCode: "JP", Enabled: true})
	require.NoError(t, err)
	paid := &Plan{Name: "paid-" + t.Name(), IsActive: true, DevicesLimit: 3}
	require.NoError(t, db.Create(paid).Error)
	setRuntimePlanBuilder(t, f.svc, paid.ID, &paidBuilder.ID)
	_, err = f.svc.CreateTariff(f.ctx, trialMeta(), TariffInput{
		Name: "Месяц", PlanID: paid.ID, DurationDays: 30, PriceCents: 19900, Currency: "RUB", Features: []string{"3 устройства"}, IsActive: true,
	})
	require.NoError(t, err)

	// Existing subscriptions: a paid one, a free one and an issued trial.
	payer := createTestSubscription(t, f.svc, 774001, "payer", "payer-client")
	require.NoError(t, db.Model(&Subscription{}).Where("id = ?", payer.ID).Update("plan_id", paid.ID).Error)
	payer.PlanID = paid.ID
	createTestSubscription(t, f.svc, 774002, "free", "free-client")
	issuedExpiry := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Second)
	issued := &Subscription{TelegramID: -774003, ClientID: "issued-client", SubscriptionID: "issued-sub", PlanID: f.plan.ID,
		Status: string(SubscriptionStatusActive), ExpiresAt: &issuedExpiry}
	require.NoError(t, db.Create(issued).Error)

	paidPreview, err := f.svc.PreviewBuilder(f.ctx, paidBuilder.ID)
	require.NoError(t, err)
	before := snapshotOutsideTrial(t, f.svc, f.builder.ID)
	require.NotEmpty(t, before.products)
	require.Len(t, before.subscriptions, 3)

	// Every kind of trial mutation, including a copy of the PAID plan's builder.
	_, err = f.svc.PreviewTrial(f.ctx, f.draft(uptr(f.builder.ID), &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID),
		Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID}, Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL"}}}))
	require.NoError(t, err)
	d := f.draft(uptr(f.builder.ID), &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID), Mode: TrialModeSelected,
		SourceIDs: []uint{f.a.ID}, Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"}}})
	d.DurationHours = 48
	_, err = f.save(t, 0, d)
	require.NoError(t, err)
	d.Composition, d.Enabled = nil, false
	_, err = f.save(t, 1, d)
	require.NoError(t, err)
	_, err = f.svc.CheckTrialIssuance(f.ctx, trialTestDefaults)
	require.ErrorIs(t, err, ErrTrialDisabled)
	copied, err := f.svc.CopyTrialBuilder(f.ctx, trialMeta(), TrialBuilderCopyInput{BuilderID: paidBuilder.ID})
	require.NoError(t, err)
	copyVersion := f.builderVersion(t, copied.TargetID)
	d = f.draft(uptr(copied.TargetID), &TrialComposition{BuilderVersion: copyVersion, Mode: TrialModeAll, SourceIDs: []uint{f.a.ID, f.b.ID}})
	d.DurationHours = 168
	_, err = f.save(t, 2, d)
	require.NoError(t, err)

	after := snapshotOutsideTrial(t, f.svc, f.builder.ID, copied.TargetID)
	assert.Equal(t, before.products, after.products, "paid tariffs are untouched")
	assert.Equal(t, before.otherPlans, after.otherPlans, "plans other than trial are untouched")
	assert.Equal(t, before.trialPlanRest, after.trialPlanRest, "only the builder of the trial plan changes")
	assert.Equal(t, before.subscriptions, after.subscriptions, "existing subscriptions (paid, free, issued trial) are untouched")
	assert.Equal(t, before.builders, after.builders, "builders the trial does not own are untouched")
	assert.Equal(t, before.builderSources, after.builderSources)
	assert.Equal(t, before.builderItems, after.builderItems)

	// The paid subscription is still served by the paid builder, unchanged.
	resolved, err := f.svc.ResolveSubscriptionBuilder(f.ctx, payer)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, paidBuilder.ID, resolved.Builder.ID)
	paidAfter, err := f.svc.PreviewBuilder(f.ctx, paidBuilder.ID)
	require.NoError(t, err)
	assert.Equal(t, previewNames(paidPreview), previewNames(paidAfter))
	assert.Equal(t, []string{"🇯🇵 B Tokyo"}, previewNames(paidAfter))

	// The issued trial keeps its own expiry: a new duration never moves it.
	var reloaded Subscription
	require.NoError(t, db.First(&reloaded, issued.ID).Error)
	require.NotNil(t, reloaded.ExpiresAt)
	assert.WithinDuration(t, issuedExpiry, *reloaded.ExpiresAt, time.Second)
	assert.Equal(t, string(SubscriptionStatusActive), reloaded.Status)
	claimed, err := f.svc.ClaimExpiredTrials(f.ctx, 1)
	require.NoError(t, err)
	assert.Empty(t, claimed, "an issued trial expires by its own expires_at, not by the new duration")
}

func TestTrial_CopyBuilderEdgeCases(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)

	_, err := f.svc.CopyTrialBuilder(f.ctx, trialMeta(), TrialBuilderCopyInput{})
	require.ErrorIs(t, err, ErrAdminInvalidRequest)
	_, err = f.svc.CopyTrialBuilder(f.ctx, trialMeta(), TrialBuilderCopyInput{BuilderID: 989898})
	assert.Equal(t, "builder_not_found", trialReason(t, err))

	// A disabled builder with a disabled rule: the copy keeps both switches.
	off := newRuntimeTestBuilder(t, f.svc, "Резерв", false)
	require.NoError(t, f.svc.SetBuilderSources(f.ctx, off.ID, []uint{f.a.ID}))
	custom := "Германия"
	_, err = f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: off.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE", CustomName: &custom, Enabled: true})
	require.NoError(t, err)
	disabledRule, err := f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: off.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL", Position: 1, Enabled: true})
	require.NoError(t, err)
	require.NoError(t, f.svc.db.Model(&SubscriptionBuilderItem{}).Where("id = ?", disabledRule.ID).Update("enabled", false).Error)
	original, err := f.svc.GetBuilder(f.ctx, off.ID)
	require.NoError(t, err)

	meta := trialMeta()
	first, err := f.svc.CopyTrialBuilder(f.ctx, meta, TrialBuilderCopyInput{BuilderID: off.ID})
	require.NoError(t, err)
	assert.Contains(t, first.Audit.NewValue, `"copied_from":`)
	copied, err := f.svc.GetBuilder(f.ctx, first.TargetID)
	require.NoError(t, err)
	assert.Equal(t, "Резерв · пробная", copied.Name)
	assert.False(t, copied.Enabled, "the copy keeps the original's switch")
	require.Len(t, copied.Items, 2)
	assert.True(t, copied.Items[0].Enabled)
	require.NotNil(t, copied.Items[0].CustomName)
	assert.Equal(t, "Германия", *copied.Items[0].CustomName)
	assert.False(t, copied.Items[1].Enabled, "a disabled rule stays disabled")
	assert.Len(t, copied.Sources, 1)

	// Replaying the key creates nothing new.
	var builders int64
	require.NoError(t, f.svc.db.Model(&SubscriptionBuilder{}).Count(&builders).Error)
	again, err := f.svc.CopyTrialBuilder(f.ctx, meta, TrialBuilderCopyInput{BuilderID: off.ID})
	require.NoError(t, err)
	assert.True(t, again.Replayed)
	assert.Equal(t, first.TargetID, again.TargetID)
	var buildersAfter int64
	require.NoError(t, f.svc.db.Model(&SubscriptionBuilder{}).Count(&buildersAfter).Error)
	assert.Equal(t, builders, buildersAfter)

	// A second copy gets a unique name.
	second, err := f.svc.CopyTrialBuilder(f.ctx, trialMeta(), TrialBuilderCopyInput{BuilderID: off.ID})
	require.NoError(t, err)
	copy2, err := f.svc.GetBuilder(f.ctx, second.TargetID)
	require.NoError(t, err)
	assert.Equal(t, "Резерв · пробная 2", copy2.Name)
	plan, err := f.svc.GetPlanByName(f.ctx, TrialPlanName)
	require.NoError(t, err)
	assert.Equal(t, second.TargetID, *plan.SubscriptionBuilderID)

	// The original is never modified by a copy.
	unchanged, err := f.svc.GetBuilder(f.ctx, off.ID)
	require.NoError(t, err)
	assert.Equal(t, original.Version, unchanged.Version)
	assert.Equal(t, original.Name, unchanged.Name)
	assert.Len(t, unchanged.Items, 2)
}

func TestTrial_PreviewAndRejectedSavesWriteNothing(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	v := f.builderVersion(t, f.builder.ID)
	c := &TrialComposition{BuilderVersion: v, Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID},
		Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"}}}
	counts := func() (settings, audit, items int64) {
		require.NoError(t, f.svc.db.Model(&TrialSettings{}).Count(&settings).Error)
		require.NoError(t, f.svc.db.Model(&AdminAuditLog{}).Count(&audit).Error)
		require.NoError(t, f.svc.db.Model(&SubscriptionBuilderItem{}).Where("builder_id = ?", f.builder.ID).Count(&items).Error)
		return
	}

	p, err := f.svc.PreviewTrial(f.ctx, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)
	require.True(t, p.Issuable, "%+v", p.Problems)
	s, a, i := counts()
	assert.Zero(t, s+a+i, "preview writes no settings, audit or rules")
	assert.Equal(t, v, f.builderVersion(t, f.builder.ID))
	plan, err := f.svc.GetPlanByName(f.ctx, TrialPlanName)
	require.NoError(t, err)
	assert.Nil(t, plan.SubscriptionBuilderID, "preview does not assign the builder")

	// A stale builder version, a composition without a builder and an invalid
	// draft are rejected without a trace.
	stale := *c
	stale.BuilderVersion = v + 5
	_, err = f.save(t, 0, f.draft(uptr(f.builder.ID), &stale))
	require.ErrorIs(t, err, ErrBuilderVersionConflict)
	_, err = f.save(t, 0, f.draft(nil, c))
	assert.Equal(t, "builder_required", trialReason(t, err))
	bad := f.draft(uptr(f.builder.ID), c)
	bad.RateLimitPerHour = 101
	_, err = f.save(t, 0, bad)
	assert.Equal(t, "out_of_range", trialReason(t, err))
	_, err = f.svc.UpdateTrial(f.ctx, trialMeta(), TrialUpdateInput{Version: -1, Defaults: trialTestDefaults, TrialDraft: f.draft(nil, nil)})
	require.ErrorIs(t, err, ErrAdminInvalidRequest)
	s, a, i = counts()
	assert.Zero(t, s+a+i)
	assert.Equal(t, v, f.builderVersion(t, f.builder.ID))
}

func TestTrial_SaveKeepsCustomNamesOfKeptRules(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	custom := "Германия Premium"
	_, err := f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: f.builder.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID,
		CountryCode: "DE", CustomName: &custom, Description: "основная", Enabled: true})
	require.NoError(t, err)

	c := &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID), Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID, f.b.ID}, Rules: []TrialRule{
		{Kind: BuilderItemKindNode, SourceID: f.b.ID, Fingerprint: "b-jp"},
		{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"},
	}}
	_, err = f.save(t, 0, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)
	saved, err := f.svc.GetBuilder(f.ctx, f.builder.ID)
	require.NoError(t, err)
	require.Len(t, saved.Items, 2)
	assert.Equal(t, "b-jp", saved.Items[0].Fingerprint, "the editor's order is stored")
	assert.Nil(t, saved.Items[0].CustomName)
	assert.Equal(t, "DE", saved.Items[1].CountryCode)
	require.NotNil(t, saved.Items[1].CustomName)
	assert.Equal(t, custom, *saved.Items[1].CustomName, "a kept rule keeps its custom name")
	assert.Equal(t, "основная", saved.Items[1].Description)
}

func TestTrial_ClaimExpiredTrialsLegacyRowsWithoutExpiry(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	// created_at is written like GORM's autoCreateTime (local time.Now()) and
	// compared with the local cutoff, as for rows created by the service.
	now := time.Now()
	old := &Subscription{TelegramID: -775001, ClientID: "legacy-old", SubscriptionID: "legacy-old", PlanID: f.plan.ID,
		Status: string(SubscriptionStatusActive), CreatedAt: now.Add(-5 * time.Hour)}
	fresh := &Subscription{TelegramID: -775002, ClientID: "legacy-fresh", SubscriptionID: "legacy-fresh", PlanID: f.plan.ID,
		Status: string(SubscriptionStatusActive), CreatedAt: now.Add(-time.Hour)}
	require.NoError(t, f.svc.db.Create(old).Error)
	require.NoError(t, f.svc.db.Create(fresh).Error)
	require.NoError(t, f.svc.db.Model(&Subscription{}).Where("id IN ?", []uint{old.ID, fresh.ID}).Update("expires_at", nil).Error)

	claimed, err := f.svc.ClaimExpiredTrials(context.Background(), 3)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, "legacy-old", claimed[0].SubscriptionID)
}
