package database

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var trialKeySeq atomic.Int64

func trialMeta() AdminConfigMeta {
	return AdminConfigMeta{Actor: "admin", RequestKey: fmt.Sprintf("trial-key-%d", trialKeySeq.Add(1))}
}

var trialTestDefaults = TrialDefaults{DurationHours: 3, RateLimitPerHour: 3}

type trialFixture struct {
	svc     *Service
	ctx     context.Context
	a, b    *ProviderSource
	builder *SubscriptionBuilder
	plan    *Plan
}

// newTrialFixture: two sources with a catalogue (A: DE×2, NL, one node without
// country; B: DE, JP), a builder linked to both, a trial node.
func newTrialFixture(t *testing.T) *trialFixture {
	t.Helper()
	svc := newTestService(t)
	ctx := context.Background()
	f := &trialFixture{svc: svc, ctx: ctx}
	f.a = newProviderSourceTestSource(t, svc, "trial-a-"+t.Name())
	f.b = newProviderSourceTestSource(t, svc, "trial-b-"+t.Name())
	_, err := svc.SyncSourceEntries(ctx, f.a.ID, catalogueEntries(
		[3]string{"a-de1", "🇩🇪 A Berlin", "DE"}, [3]string{"a-de2", "🇩🇪 A Munich", "DE"},
		[3]string{"a-nl", "🇳🇱 A Amsterdam", "NL"}, [3]string{"a-x", "A Unknown", ""},
	), SourceSyncOK, "")
	require.NoError(t, err)
	_, err = svc.SyncSourceEntries(ctx, f.b.ID, catalogueEntries(
		[3]string{"b-de", "🇩🇪 B Frankfurt", "DE"}, [3]string{"b-jp", "🇯🇵 B Tokyo", "JP"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	f.builder = newRuntimeTestBuilder(t, svc, "trial-builder-"+t.Name(), true)
	require.NoError(t, svc.SetBuilderSources(ctx, f.builder.ID, []uint{f.a.ID, f.b.ID}))
	createTestNode(t, svc, "trial-node", "trial.example", "token")
	f.plan, err = svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)
	return f
}

func (f *trialFixture) builderVersion(t *testing.T, id uint) int {
	t.Helper()
	b, err := f.svc.GetBuilder(f.ctx, id)
	require.NoError(t, err)
	return b.Version
}

func (f *trialFixture) draft(builderID *uint, c *TrialComposition) TrialDraft {
	return TrialDraft{Enabled: true, DurationHours: 6, RateLimitPerHour: 5, BuilderID: builderID, Composition: c}
}

func (f *trialFixture) save(t *testing.T, version int, d TrialDraft) (*AdminConfigResult, error) {
	t.Helper()
	return f.svc.UpdateTrial(f.ctx, trialMeta(), TrialUpdateInput{Version: version, Defaults: trialTestDefaults, TrialDraft: d})
}

func trialReason(t *testing.T, err error) string {
	t.Helper()
	var fe *TrialFieldError
	require.ErrorAs(t, err, &fe)
	return fe.Reason
}

func previewItemNames(p *TrialPreview) []string {
	out := []string{}
	for _, it := range p.Items {
		if it.Entry != nil {
			out = append(out, it.DisplayName)
		}
	}
	return out
}

func uptr(v uint) *uint { return &v }

func TestTrial_DefaultsWithoutStoredSettings(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := context.Background()

	eff, err := svc.GetTrialEffective(ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.True(t, eff.Enabled, "a database without trial_settings keeps the trial on")
	assert.Equal(t, 3, eff.DurationHours)
	assert.Equal(t, 3, eff.RateLimitPerHour)
	assert.False(t, eff.Stored)
	assert.Zero(t, eff.Version)

	// Legacy serving (no builder) still needs the trial node CreateTrial uses.
	_, err = svc.CheckTrialIssuance(ctx, trialTestDefaults)
	require.ErrorIs(t, err, ErrTrialUnavailable)
	createTestNode(t, svc, "legacy", "legacy.example", "token")
	issuance, err := svc.CheckTrialIssuance(ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.Equal(t, TrialServeLegacy, issuance.Preview.Serve)
}

func TestTrial_SaveDurationEnableAndVersioning(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)

	d := f.draft(nil, nil)
	d.DurationHours, d.Title, d.Features, d.Badge = 24, "Пробный доступ", []string{"Без карты"}, "Бесплатно"
	res, err := f.save(t, 0, d)
	require.NoError(t, err)
	assert.Equal(t, string(AdminActionTrialUpdated), res.Audit.Action)
	assert.Equal(t, AdminTargetTrial, res.Audit.TargetType)
	assert.Contains(t, res.Audit.NewValue, `"duration_hours":24`)
	assert.Contains(t, res.Audit.OldValue, `"duration_hours":3`, "the audit keeps the environment values replaced")

	eff, err := f.svc.GetTrialEffective(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.Equal(t, 24, eff.DurationHours)
	assert.Equal(t, []string{"Без карты"}, eff.Features)
	assert.Equal(t, 1, eff.Version)
	assert.True(t, eff.Stored)

	// A stale editor (version 0) is a conflict, not an overwrite.
	_, err = f.save(t, 0, d)
	require.ErrorIs(t, err, ErrTrialVersionConflict)

	// Switching off is stored (false is not replaced by the column default).
	d.Enabled = false
	_, err = f.save(t, 1, d)
	require.NoError(t, err)
	eff, err = f.svc.GetTrialEffective(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.False(t, eff.Enabled)
	_, err = f.svc.CheckTrialIssuance(f.ctx, trialTestDefaults)
	require.ErrorIs(t, err, ErrTrialDisabled)

	// Replaying the same request key with the same payload does not re-apply.
	meta := trialMeta()
	d.Enabled = true
	in := TrialUpdateInput{Version: 2, Defaults: trialTestDefaults, TrialDraft: d}
	first, err := f.svc.UpdateTrial(f.ctx, meta, in)
	require.NoError(t, err)
	again, err := f.svc.UpdateTrial(f.ctx, meta, in)
	require.NoError(t, err)
	assert.True(t, again.Replayed)
	assert.Equal(t, first.Audit.ID, again.Audit.ID)

	// The same key with another payload is a conflict.
	in.DurationHours = 48
	_, err = f.svc.UpdateTrial(f.ctx, meta, in)
	require.ErrorIs(t, err, ErrAdminRequestKeyConflict)
}

func TestTrial_StaticValidation(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*TrialDraft)
		field  string
	}{
		{"zero duration", func(d *TrialDraft) { d.DurationHours = 0 }, "duration_hours"},
		{"duration above 7 days", func(d *TrialDraft) { d.DurationHours = 169 }, "duration_hours"},
		{"zero rate limit", func(d *TrialDraft) { d.RateLimitPerHour = 0 }, "rate_limit_per_hour"},
		{"long badge", func(d *TrialDraft) { d.Badge = "очень длинный бейдж больше лимита" }, "badge"},
		{"too many features", func(d *TrialDraft) { d.Features = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} }, "features"},
	} {
		d := f.draft(nil, nil)
		tc.mutate(&d)
		_, err := f.save(t, 0, d)
		var fe *TrialFieldError
		require.ErrorAs(t, err, &fe, tc.name)
		assert.Equal(t, tc.field, fe.Field, tc.name)
	}
}

func TestTrial_BuilderCompositionCountriesAndNodes(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	v := f.builderVersion(t, f.builder.ID)

	// Only DE of source A (whole country) and the Tokyo node of source B; NL
	// and the rest of B are excluded by not being selected.
	c := &TrialComposition{BuilderVersion: v, Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID, f.b.ID}, Rules: []TrialRule{
		{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "de"},
		{Kind: BuilderItemKindNode, SourceID: f.b.ID, Fingerprint: "b-jp"},
	}}
	preview, err := f.svc.PreviewTrial(f.ctx, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)
	require.True(t, preview.Issuable, "%+v", preview.Problems)
	assert.Equal(t, []string{"🇩🇪 A Berlin", "🇩🇪 A Munich", "🇯🇵 B Tokyo"}, previewItemNames(preview))
	assert.Equal(t, []TrialCountryCount{{Code: "DE", Count: 2}, {Code: "JP", Count: 1}}, preview.Countries)
	assert.Equal(t, TrialModeSelected, preview.Mode)
	assert.Equal(t, 6, preview.DurationHours)
	assert.WithinDuration(t, time.Now().Add(6*time.Hour), preview.ExpiresAt, time.Minute)

	_, err = f.save(t, 0, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)

	plan, err := f.svc.GetPlanByName(f.ctx, TrialPlanName)
	require.NoError(t, err)
	require.NotNil(t, plan.SubscriptionBuilderID)
	assert.Equal(t, f.builder.ID, *plan.SubscriptionBuilderID, "the composition is the trial plan's builder")
	saved, err := f.svc.GetBuilder(f.ctx, f.builder.ID)
	require.NoError(t, err)
	assert.Equal(t, v+1, saved.Version, "the builder version moves so /sub serves the new rules")
	require.Len(t, saved.Items, 2)
	assert.Equal(t, "DE", saved.Items[0].CountryCode)
	assert.Equal(t, "🇯🇵 B Tokyo", saved.Items[1].OriginalName, "node rules keep the catalogue name for the fallback match")

	// The preview of the saved builder is the PreviewBuilder result: one resolver.
	direct, err := f.svc.PreviewBuilder(f.ctx, f.builder.ID)
	require.NoError(t, err)
	issuance, err := f.svc.CheckTrialIssuance(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.Equal(t, previewNames(direct), previewItemNames(issuance.Preview))
	assert.Equal(t, direct.Total, issuance.Preview.Total)

	// A stale builder version is rejected.
	_, err = f.save(t, 1, f.draft(uptr(f.builder.ID), c))
	require.ErrorIs(t, err, ErrBuilderVersionConflict)
}

func TestTrial_AllCountriesIncludesNewOnes(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	c := &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID), Mode: TrialModeAll, SourceIDs: []uint{f.a.ID}}
	_, err := f.save(t, 0, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)
	issuance, err := f.svc.CheckTrialIssuance(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.Equal(t, 4, issuance.Preview.Total, "all entries of source A, the node without a country included")
	assert.Contains(t, issuance.Preview.Countries, TrialCountryCount{Code: "", Count: 1})

	// A new country appears upstream: served without touching the trial.
	_, err = f.svc.SyncSourceEntries(f.ctx, f.a.ID, catalogueEntries(
		[3]string{"a-de1", "🇩🇪 A Berlin", "DE"}, [3]string{"a-de2", "🇩🇪 A Munich", "DE"},
		[3]string{"a-nl", "🇳🇱 A Amsterdam", "NL"}, [3]string{"a-x", "A Unknown", ""}, [3]string{"a-fi", "🇫🇮 A Helsinki", "FI"},
	), SourceSyncOK, "")
	require.NoError(t, err)
	issuance, err = f.svc.CheckTrialIssuance(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.Equal(t, 5, issuance.Preview.Total)
}

func TestTrial_CompositionValidation(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	v := f.builderVersion(t, f.builder.ID)
	comp := func(mode string, sources []uint, rules ...TrialRule) *TrialComposition {
		return &TrialComposition{BuilderVersion: v, Mode: mode, SourceIDs: sources, Rules: rules}
	}
	cases := []struct {
		name   string
		c      *TrialComposition
		reason string
	}{
		{"unknown country", comp(TrialModeSelected, []uint{f.a.ID}, TrialRule{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "US"}), "country_not_found"},
		{"empty country code", comp(TrialModeSelected, []uint{f.a.ID}, TrialRule{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: ""}), "country_code"},
		{"unknown node", comp(TrialModeSelected, []uint{f.a.ID}, TrialRule{Kind: BuilderItemKindNode, SourceID: f.a.ID, Fingerprint: "gone"}), "node_not_found"},
		{"node inside a selected country", comp(TrialModeSelected, []uint{f.a.ID},
			TrialRule{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"},
			TrialRule{Kind: BuilderItemKindNode, SourceID: f.a.ID, Fingerprint: "a-de1"}), "node_covered_by_country"},
		{"duplicate rule", comp(TrialModeSelected, []uint{f.a.ID},
			TrialRule{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"},
			TrialRule{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "de"}), "duplicate_rule"},
		{"rule of an unlinked source", comp(TrialModeSelected, []uint{f.a.ID}, TrialRule{Kind: BuilderItemKindNode, SourceID: f.b.ID, Fingerprint: "b-jp"}), "rule_source_not_linked"},
		{"rules in all mode", comp(TrialModeAll, []uint{f.a.ID}, TrialRule{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"}), "rules_in_all_mode"},
		{"selected without rules", comp(TrialModeSelected, []uint{f.a.ID}), "required"},
		{"no sources", comp(TrialModeAll, nil), "required"},
		{"unknown source", comp(TrialModeAll, []uint{999999}), "source_not_found"},
	}
	for _, tc := range cases {
		_, err := f.save(t, 0, f.draft(uptr(f.builder.ID), tc.c))
		assert.Equal(t, tc.reason, trialReason(t, err), tc.name)
	}

	// A node without a country can only be chosen as a node rule.
	_, err := f.save(t, 0, f.draft(uptr(f.builder.ID), comp(TrialModeSelected, []uint{f.a.ID},
		TrialRule{Kind: BuilderItemKindNode, SourceID: f.a.ID, Fingerprint: "a-x"})))
	require.NoError(t, err)

	// Nothing was written by the rejected drafts before it.
	_, err = f.save(t, 0, f.draft(nil, nil))
	require.ErrorIs(t, err, ErrTrialVersionConflict, "exactly one save succeeded")
}

func TestTrial_InvalidDisabledAndEmptyBuilder(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)

	_, err := f.save(t, 0, f.draft(uptr(424242), nil))
	assert.Equal(t, "builder_not_found", trialReason(t, err))

	off := newRuntimeTestBuilder(t, f.svc, "trial-off-"+t.Name(), false)
	require.NoError(t, f.svc.SetBuilderSources(f.ctx, off.ID, []uint{f.a.ID}))
	_, err = f.save(t, 0, f.draft(uptr(off.ID), nil))
	assert.Equal(t, "builder_disabled", trialReason(t, err), "an enabled trial is never saved onto a disabled builder")

	// With the trial switched off the configuration may be prepared.
	d := f.draft(uptr(off.ID), nil)
	d.Enabled = false
	_, err = f.save(t, 0, d)
	require.NoError(t, err)

	// A builder whose rules resolve to nothing cannot be switched on.
	empty := newRuntimeTestBuilder(t, f.svc, "trial-empty-"+t.Name(), true)
	require.NoError(t, f.svc.SetBuilderSources(f.ctx, empty.ID, []uint{f.a.ID}))
	_, err = f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: empty.ID, Kind: BuilderItemKindNode, SourceID: f.a.ID, Fingerprint: "vanished", Enabled: true})
	require.NoError(t, err)
	_, err = f.save(t, 1, f.draft(uptr(empty.ID), nil))
	assert.Equal(t, "empty_result", trialReason(t, err))

	// A disabled source is skipped like on /sub; the preview says so.
	_, err = f.svc.SetProviderSourceEnabled(f.ctx, f.a.ID, false)
	require.NoError(t, err)
	p, err := f.svc.PreviewTrial(f.ctx, f.draft(uptr(f.builder.ID), nil))
	require.NoError(t, err)
	assert.Equal(t, []string{"🇩🇪 B Frankfurt", "🇯🇵 B Tokyo"}, previewItemNames(p), "fallback: the other source still serves")
	assert.NotEmpty(t, p.Warnings)
	assert.True(t, p.Issuable)

	// Issued configuration later broken: no new trial (existing ones untouched).
	setRuntimePlanBuilder(t, f.svc, f.plan.ID, &off.ID)
	require.NoError(t, f.svc.db.Model(&TrialSettings{}).Where("id = ?", 1).Update("enabled", true).Error)
	_, err = f.svc.CheckTrialIssuance(f.ctx, trialTestDefaults)
	require.ErrorIs(t, err, ErrTrialUnavailable)
	assert.ErrorContains(t, err, "builder_disabled")
}

func TestTrial_SharedBuilderIsProtectedAndCopied(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	premium := &Plan{Name: "premium-" + t.Name(), IsActive: true}
	require.NoError(t, f.svc.db.Create(premium).Error)
	setRuntimePlanBuilder(t, f.svc, premium.ID, &f.builder.ID)
	before, err := f.svc.GetBuilder(f.ctx, f.builder.ID)
	require.NoError(t, err)

	// Assigning a shared builder as is: allowed, nothing of it changes.
	_, err = f.save(t, 0, f.draft(uptr(f.builder.ID), nil))
	require.NoError(t, err)

	// Editing its composition from the trial would change the paid plan.
	c := &TrialComposition{BuilderVersion: before.Version, Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID},
		Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL"}}}
	_, err = f.save(t, 1, f.draft(uptr(f.builder.ID), c))
	require.ErrorIs(t, err, ErrTrialBuilderShared)
	after, err := f.svc.GetBuilder(f.ctx, f.builder.ID)
	require.NoError(t, err)
	assert.Equal(t, before.Version, after.Version)
	assert.Len(t, after.Sources, 2)

	// Copy: the trial gets its own builder, the paid plan keeps the original.
	res, err := f.svc.CopyTrialBuilder(f.ctx, trialMeta(), TrialBuilderCopyInput{BuilderID: f.builder.ID})
	require.NoError(t, err)
	copyID := res.TargetID
	require.NotEqual(t, f.builder.ID, copyID)
	assert.Equal(t, string(AdminActionTrialBuilderCopied), res.Audit.Action)
	plan, err := f.svc.GetPlanByName(f.ctx, TrialPlanName)
	require.NoError(t, err)
	assert.Equal(t, copyID, *plan.SubscriptionBuilderID)
	var paid Plan
	require.NoError(t, f.svc.db.First(&paid, premium.ID).Error)
	assert.Equal(t, f.builder.ID, *paid.SubscriptionBuilderID, "the paid plan is untouched")
	copied, err := f.svc.GetBuilder(f.ctx, copyID)
	require.NoError(t, err)
	assert.Len(t, copied.Sources, 2)
	assert.Contains(t, copied.Name, "пробная")

	c.BuilderVersion = copied.Version
	_, err = f.save(t, 1, f.draft(uptr(copyID), c))
	require.NoError(t, err)
	original, err := f.svc.GetBuilder(f.ctx, f.builder.ID)
	require.NoError(t, err)
	assert.Equal(t, before.Version, original.Version, "the paid plan's builder never changed")
	assert.Empty(t, original.Items)

	// A per-subscription override also makes a builder shared.
	other := newRuntimeTestBuilder(t, f.svc, "override-"+t.Name(), true)
	sub := createTestSubscription(t, f.svc, 770001, "override", "override-client")
	setRuntimeSubBuilder(t, f.svc, sub, &other.ID)
	usage, err := builderUsage(f.svc.db, other.ID)
	require.NoError(t, err)
	assert.True(t, usage.Shared())
}

func TestTrial_AdminViewAndHistory(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	c := &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID), Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID},
		Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL"}}}
	_, err := f.save(t, 0, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)

	view, err := f.svc.GetTrialAdminView(f.ctx, trialTestDefaults)
	require.NoError(t, err)
	assert.Equal(t, 6, view.Settings.DurationHours)
	require.NotNil(t, view.BuilderID)
	require.NotNil(t, view.Composition)
	assert.Equal(t, TrialModeSelected, view.Composition.Mode)
	assert.Equal(t, []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL"}}, view.Composition.Rules)
	assert.Equal(t, 1, view.Preview.Total)
	require.Len(t, view.History, 1)
	assert.Equal(t, string(AdminActionTrialUpdated), view.History[0].Action)
	var summary *TrialBuilderSummary
	for i := range view.Builders {
		if view.Builders[i].ID == f.builder.ID {
			summary = &view.Builders[i]
		}
	}
	require.NotNil(t, summary)
	assert.True(t, summary.AssignedToTrial)
	assert.False(t, summary.Shared)
	assert.Equal(t, 1, summary.Total)
	assert.Equal(t, 1, summary.Countries)
}

func TestTrial_ExpiryUsesExpiresAt(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	now := time.Now().UTC()
	// Issued with a long duration a moment ago, already past its own expiry.
	expired := &Subscription{TelegramID: -771001, ClientID: "exp-client", SubscriptionID: "exp-sub", PlanID: f.plan.ID,
		Status: "active", ExpiresAt: ptrTime(now.Add(-time.Minute)), CreatedAt: now.Add(-10 * time.Minute)}
	// Created long ago but still valid: a longer trial must not be cut short.
	valid := &Subscription{TelegramID: -771002, ClientID: "valid-client", SubscriptionID: "valid-sub", PlanID: f.plan.ID,
		Status: "active", ExpiresAt: ptrTime(now.Add(time.Hour)), CreatedAt: now.Add(-100 * time.Hour)}
	require.NoError(t, f.svc.db.Create(expired).Error)
	require.NoError(t, f.svc.db.Create(valid).Error)

	claimed, err := f.svc.ClaimExpiredTrials(f.ctx, 3)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, "exp-sub", claimed[0].SubscriptionID)
}

func TestTrial_PaidPlanAndSubscriptionsUnaffected(t *testing.T) {
	t.Parallel()
	f := newTrialFixture(t)
	paidBuilder := newRuntimeTestBuilder(t, f.svc, "paid-"+t.Name(), true)
	require.NoError(t, f.svc.SetBuilderSources(f.ctx, paidBuilder.ID, []uint{f.b.ID}))
	paid := &Plan{Name: "paid-" + t.Name(), IsActive: true}
	require.NoError(t, f.svc.db.Create(paid).Error)
	setRuntimePlanBuilder(t, f.svc, paid.ID, &paidBuilder.ID)
	sub := createTestSubscription(t, f.svc, 772001, "payer", "payer-client")
	require.NoError(t, f.svc.db.Model(&Subscription{}).Where("id = ?", sub.ID).Update("plan_id", paid.ID).Error)
	sub.PlanID = paid.ID
	paidBefore, err := f.svc.PreviewBuilder(f.ctx, paidBuilder.ID)
	require.NoError(t, err)

	c := &TrialComposition{BuilderVersion: f.builderVersion(t, f.builder.ID), Mode: TrialModeSelected, SourceIDs: []uint{f.a.ID},
		Rules: []TrialRule{{Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE"}}}
	_, err = f.save(t, 0, f.draft(uptr(f.builder.ID), c))
	require.NoError(t, err)

	var plan Plan
	require.NoError(t, f.svc.db.First(&plan, paid.ID).Error)
	assert.Equal(t, paidBuilder.ID, *plan.SubscriptionBuilderID)
	paidAfter, err := f.svc.PreviewBuilder(f.ctx, paidBuilder.ID)
	require.NoError(t, err)
	assert.Equal(t, previewNames(paidBefore), previewNames(paidAfter))
	resolved, err := f.svc.ResolveSubscriptionBuilder(f.ctx, sub)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, paidBuilder.ID, resolved.Builder.ID, "the paid subscription is still served by its plan builder")
}
