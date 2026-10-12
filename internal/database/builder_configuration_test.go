package database

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type builderConfigFixture struct {
	t      *testing.T
	ctx    context.Context
	svc    *Service
	a, b   *ProviderSource
	keySeq int
}

func newBuilderConfigFixture(t *testing.T) *builderConfigFixture {
	t.Helper()
	svc, err := NewService(filepath.Join(t.TempDir(), "builder-config.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	f := &builderConfigFixture{t: t, ctx: context.Background(), svc: svc}
	f.a = f.source("Provider A")
	f.b = f.source("Provider B")
	return f
}

func (f *builderConfigFixture) source(name string) *ProviderSource {
	src := &ProviderSource{Name: name, Type: "auto", SubscriptionURL: "https://provider.example/" + name, Headers: `{}`, Enabled: true}
	require.NoError(f.t, f.svc.CreateProviderSource(f.ctx, src))
	return src
}

func (f *builderConfigFixture) meta() AdminConfigMeta {
	f.keySeq++
	return AdminConfigMeta{Actor: "admin", RequestKey: fmt.Sprintf("cfg-%d", f.keySeq)}
}

func (f *builderConfigFixture) builder(name string) *SubscriptionBuilder {
	res, err := f.svc.CreateBuilder(f.ctx, f.meta(), BuilderCreateInput{Name: name, Enabled: true})
	require.NoError(f.t, err)
	b, err := f.svc.GetBuilder(f.ctx, res.TargetID)
	require.NoError(f.t, err)
	return b
}

func (f *builderConfigFixture) reload(id uint) *SubscriptionBuilder {
	b, err := f.svc.GetBuilder(f.ctx, id)
	require.NoError(f.t, err)
	return b
}

// snapshot is the stored configuration of a builder, for "nothing changed".
func (f *builderConfigFixture) snapshot(id uint) string {
	b := f.reload(id)
	out := fmt.Sprintf("v%d|%s|%t|", b.Version, b.Name, b.Enabled)
	for _, s := range b.Sources {
		out += fmt.Sprintf("s%d@%d,", s.SourceID, s.Position)
	}
	for _, it := range b.Items {
		out += fmt.Sprintf("i%d:%s:%d:%s:%s:%d:%t,", it.ID, it.Kind, it.SourceID, it.CountryCode, it.Fingerprint, it.Position, it.Enabled)
	}
	return out
}

func (f *builderConfigFixture) config(b *SubscriptionBuilder, sources []uint, items ...BuilderConfigItem) BuilderConfigurationInput {
	if items == nil {
		items = []BuilderConfigItem{}
	}
	return BuilderConfigurationInput{ID: b.ID, Version: b.Version, Name: b.Name, Description: b.Description, Enabled: b.Enabled, SourceIDs: sources, Items: items}
}

func countryRule(src uint, code string, enabled bool) BuilderConfigItem {
	return BuilderConfigItem{Kind: BuilderItemKindCountry, SourceID: src, CountryCode: code, Enabled: enabled}
}

func TestSaveBuilderConfiguration_AtomicVersionedSave(t *testing.T) {
	f := newBuilderConfigFixture(t)
	b := f.builder("FINLAN")
	require.Equal(t, 1, b.Version)

	custom := "Даллас"
	in := f.config(b, []uint{f.a.ID, f.b.ID},
		countryRule(f.a.ID, "de", true),
		countryRule(f.a.ID, "NL", false),
		BuilderConfigItem{Kind: BuilderItemKindNode, SourceID: f.b.ID, Fingerprint: "fp-dallas", OriginalName: "🇺🇸 Dallas", CustomName: &custom, Enabled: true},
	)
	in.Description = "  Финляндия  "
	res, err := f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), in)
	require.NoError(t, err)
	assert.Equal(t, b.ID, res.TargetID)
	assert.Equal(t, string(AdminActionBuilderConfigured), res.Audit.Action)
	assert.Contains(t, res.Audit.NewValue, `"source_ids":[`)

	saved := f.reload(b.ID)
	assert.Equal(t, 2, saved.Version, "sources and rules bump the version")
	assert.Equal(t, "Финляндия", saved.Description)
	require.Len(t, saved.Sources, 2)
	assert.Equal(t, []uint{f.a.ID, f.b.ID}, []uint{saved.Sources[0].SourceID, saved.Sources[1].SourceID})
	require.Len(t, saved.Items, 3)
	assert.Equal(t, "DE", saved.Items[0].CountryCode, "country codes are normalized")
	assert.False(t, saved.Items[1].Enabled, "a rule saved switched off stays off (GORM default:true)")
	assert.Equal(t, "fp-dallas", saved.Items[2].Fingerprint)
	assert.Equal(t, []int{0, 1, 2}, []int{saved.Items[0].Position, saved.Items[1].Position, saved.Items[2].Position})

	// Saving the reopened configuration unchanged keeps every rule in place.
	again := f.config(saved, []uint{f.a.ID, f.b.ID})
	for _, it := range saved.Items {
		again.Items = append(again.Items, BuilderConfigItem{ID: it.ID, Kind: it.Kind, SourceID: it.SourceID, CountryCode: it.CountryCode,
			Fingerprint: it.Fingerprint, OriginalName: it.OriginalName, CustomName: it.CustomName, Description: it.Description, Enabled: it.Enabled})
	}
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), again)
	require.NoError(t, err)
	resaved := f.reload(b.ID)
	assert.Equal(t, 3, resaved.Version)
	for i := range saved.Items {
		assert.Equal(t, saved.Items[i].ID, resaved.Items[i].ID, "rules are updated in place, not recreated")
		assert.Equal(t, saved.Items[i].Enabled, resaved.Items[i].Enabled)
	}

	// Reorder + remove + toggle in one save.
	reordered := f.config(resaved, []uint{f.b.ID},
		BuilderConfigItem{ID: resaved.Items[2].ID, Kind: BuilderItemKindNode, SourceID: f.b.ID, Fingerprint: "fp-dallas", Enabled: false},
		BuilderConfigItem{ID: resaved.Items[1].ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL", Enabled: true},
	)
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), reordered)
	require.NoError(t, err)
	final := f.reload(b.ID)
	require.Len(t, final.Sources, 1)
	require.Len(t, final.Items, 2)
	assert.Equal(t, resaved.Items[2].ID, final.Items[0].ID)
	assert.False(t, final.Items[0].Enabled)
	assert.Nil(t, final.Items[0].CustomName, "the saved state replaces the custom name")
	assert.Equal(t, resaved.Items[1].ID, final.Items[1].ID)
	assert.True(t, final.Items[1].Enabled)
	var removed int64
	require.NoError(t, f.svc.db.Model(&SubscriptionBuilderItem{}).Where("id = ?", resaved.Items[0].ID).Count(&removed).Error)
	assert.Zero(t, removed)
}

func TestSaveBuilderConfiguration_StaleVersionIsRefusedAndWritesNothing(t *testing.T) {
	f := newBuilderConfigFixture(t)
	b := f.builder("shared")
	// Tab 1 and tab 2 load version 1.
	tab1 := f.config(b, []uint{f.a.ID}, countryRule(f.a.ID, "DE", true))
	tab2 := f.config(b, []uint{f.b.ID}, countryRule(f.b.ID, "US", true))

	_, err := f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), tab1)
	require.NoError(t, err)
	after := f.snapshot(b.ID)

	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), tab2)
	assert.ErrorIs(t, err, ErrBuilderVersionConflict)
	assert.Equal(t, after, f.snapshot(b.ID), "the stale tab overwrote nothing")

	// A change made through the single-step endpoints also invalidates it.
	cur := f.reload(b.ID)
	tab3 := f.config(cur, []uint{f.a.ID})
	require.NoError(t, f.svc.SetBuilderSources(f.ctx, b.ID, []uint{f.a.ID, f.b.ID}))
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), tab3)
	assert.ErrorIs(t, err, ErrBuilderVersionConflict)
	assert.Len(t, f.reload(b.ID).Sources, 2)
}

func TestSaveBuilderConfiguration_RejectionsAndRollback(t *testing.T) {
	f := newBuilderConfigFixture(t)
	a := f.builder("A")
	other := f.builder("B")
	_, err := f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), f.config(a, []uint{f.a.ID}, countryRule(f.a.ID, "DE", true)))
	require.NoError(t, err)
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), f.config(other, []uint{f.b.ID}, countryRule(f.b.ID, "US", true)))
	require.NoError(t, err)
	a, other = f.reload(a.ID), f.reload(other.ID)
	foreign := other.Items[0]
	aBefore, otherBefore := f.snapshot(a.ID), f.snapshot(other.ID)

	unchanged := func(label string) {
		t.Helper()
		assert.Equal(t, aBefore, f.snapshot(a.ID), label)
		assert.Equal(t, otherBefore, f.snapshot(other.ID), label)
	}

	// A rule of another builder cannot be taken over, updated or kept.
	steal := f.config(a, []uint{f.a.ID}, BuilderConfigItem{ID: foreign.ID, Kind: BuilderItemKindCountry, SourceID: f.b.ID, CountryCode: "FI", Enabled: true})
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), steal)
	assert.ErrorIs(t, err, ErrBuilderItemNotFound)
	unchanged("foreign rule id")

	// A rule id that does not exist is not created under that id.
	ghost := f.config(a, []uint{f.a.ID}, BuilderConfigItem{ID: 999999, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "FI", Enabled: true})
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), ghost)
	assert.ErrorIs(t, err, ErrBuilderItemNotFound)
	unchanged("missing rule id")

	var fieldErr *BuilderFieldError
	for _, tc := range []struct {
		in    BuilderConfigurationInput
		field string
	}{
		{f.config(a, []uint{f.a.ID, 424242}), "source_ids"},
		{f.config(a, []uint{f.a.ID, f.a.ID}), "source_ids"},
		{f.config(a, []uint{f.a.ID}, countryRule(424242, "DE", true)), "source_ids"},
		{f.config(a, []uint{f.a.ID}, countryRule(f.a.ID, "DEU", true)), "items"},
		{f.config(a, []uint{f.a.ID}, BuilderConfigItem{Kind: "region", SourceID: f.a.ID}), "items"},
		{f.config(a, []uint{f.a.ID}, BuilderConfigItem{Kind: BuilderItemKindNode, SourceID: f.a.ID}), "items"},
		{func() BuilderConfigurationInput { c := f.config(a, nil); c.Name = "  "; return c }(), "name"},
	} {
		_, err := f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), tc.in)
		require.ErrorAs(t, err, &fieldErr, tc.field)
		assert.ErrorIs(t, err, ErrAdminInvalidRequest)
		assert.Equal(t, tc.field, fieldErr.Field)
	}
	unchanged("invalid input")

	// The other builder's name.
	taken := f.config(a, []uint{f.b.ID}, countryRule(f.b.ID, "US", true))
	taken.Name = other.Name
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), taken)
	assert.ErrorIs(t, err, ErrBuilderNameTaken)
	unchanged("name taken")

	// A failure AFTER the metadata, the sources and the first rules were
	// written rolls everything back (the trigger aborts the third insert).
	require.NoError(t, f.svc.db.Exec(`CREATE TRIGGER fail_builder_item BEFORE INSERT ON subscription_builder_items
		WHEN NEW.original_name = 'boom' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error)
	boom := f.config(a, []uint{f.b.ID},
		BuilderConfigItem{ID: a.Items[0].ID, Kind: BuilderItemKindCountry, SourceID: f.b.ID, CountryCode: "JP", Enabled: false},
		countryRule(f.b.ID, "US", true),
		BuilderConfigItem{Kind: BuilderItemKindNode, SourceID: f.b.ID, OriginalName: "boom", Enabled: true},
	)
	boom.Name = "A renamed"
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, f.meta(), boom)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "injected failure")
	unchanged("mid-write failure")
	var audits int64
	require.NoError(t, f.svc.db.Model(&AdminAuditLog{}).Where("target_type = ? AND target_id = ? AND action = ?", AdminTargetBuilder, a.ID, AdminActionBuilderConfigured).Count(&audits).Error)
	assert.Equal(t, int64(1), audits, "rejected and failed saves are not audited")
}

func TestSaveBuilderConfiguration_RequestKeyReplay(t *testing.T) {
	f := newBuilderConfigFixture(t)
	b := f.builder("replay")
	in := f.config(b, []uint{f.a.ID}, countryRule(f.a.ID, "DE", true))
	meta := f.meta()
	first, err := f.svc.SaveBuilderConfiguration(f.ctx, meta, in)
	require.NoError(t, err)
	// A retried request (lost response) is not applied twice.
	again, err := f.svc.SaveBuilderConfiguration(f.ctx, meta, in)
	require.NoError(t, err)
	assert.True(t, again.Replayed)
	assert.Equal(t, first.Audit.ID, again.Audit.ID)
	saved := f.reload(b.ID)
	assert.Equal(t, 2, saved.Version)
	assert.Len(t, saved.Items, 1)
	// The same key with another payload is a conflict.
	in.SourceIDs = []uint{f.b.ID}
	_, err = f.svc.SaveBuilderConfiguration(f.ctx, meta, in)
	assert.ErrorIs(t, err, ErrAdminRequestKeyConflict)
}

// The single-step rule endpoints are scoped to the builder of the request.
func TestBuilderItemOwnership_SingleStepOperations(t *testing.T) {
	f := newBuilderConfigFixture(t)
	a := f.builder("A")
	other := f.builder("B")
	ownItem, err := f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: a.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE", Enabled: true})
	require.NoError(t, err)
	foreign, err := f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: other.ID, Kind: BuilderItemKindCountry, SourceID: f.b.ID, CountryCode: "US", Enabled: true})
	require.NoError(t, err)
	aBefore, otherBefore := f.snapshot(a.ID), f.snapshot(other.ID)
	unchanged := func(label string) {
		t.Helper()
		assert.Equal(t, aBefore, f.snapshot(a.ID), label)
		assert.Equal(t, otherBefore, f.snapshot(other.ID), label)
	}

	// Update a foreign rule through builder A: refused, not moved.
	_, err = f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{ID: foreign.ID, BuilderID: a.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "FI", Enabled: true})
	assert.ErrorIs(t, err, ErrBuilderItemNotFound)
	unchanged("upsert foreign")

	// A missing id is not inserted under that id.
	_, err = f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{ID: 777777, BuilderID: a.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "FI", Enabled: true})
	assert.ErrorIs(t, err, ErrBuilderItemNotFound)
	unchanged("upsert missing")

	assert.ErrorIs(t, f.svc.DeleteBuilderItem(f.ctx, a.ID, foreign.ID), ErrBuilderItemNotFound)
	unchanged("delete foreign")

	assert.ErrorIs(t, f.svc.ReorderBuilderItems(f.ctx, a.ID, []uint{foreign.ID, ownItem.ID}), ErrBuilderItemNotFound)
	unchanged("reorder with foreign")
	assert.ErrorIs(t, f.svc.ReorderBuilderItems(f.ctx, a.ID, []uint{ownItem.ID, ownItem.ID}), ErrAdminInvalidRequest)
	unchanged("reorder duplicate")

	// Own operations work and bump the version.
	v := f.reload(a.ID).Version
	_, err = f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{ID: ownItem.ID, BuilderID: a.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "NL", Enabled: false})
	require.NoError(t, err)
	updated := f.reload(a.ID)
	assert.Equal(t, v+1, updated.Version)
	assert.Equal(t, "NL", updated.Items[0].CountryCode)
	assert.False(t, updated.Items[0].Enabled)
	require.NoError(t, f.svc.ReorderBuilderItems(f.ctx, a.ID, []uint{ownItem.ID}))
	require.NoError(t, f.svc.DeleteBuilderItem(f.ctx, a.ID, ownItem.ID))
	assert.Equal(t, v+3, f.reload(a.ID).Version)
	assert.Equal(t, otherBefore, f.snapshot(other.ID))
}

func TestCreateBuilderAndRule_DisabledIsStored(t *testing.T) {
	f := newBuilderConfigFixture(t)
	res, err := f.svc.CreateBuilder(f.ctx, f.meta(), BuilderCreateInput{Name: "off", Enabled: false})
	require.NoError(t, err)
	b := f.reload(res.TargetID)
	assert.False(t, b.Enabled, "a builder created switched off is not served")

	item, err := f.svc.UpsertBuilderItem(f.ctx, SubscriptionBuilderItem{BuilderID: b.ID, Kind: BuilderItemKindCountry, SourceID: f.a.ID, CountryCode: "DE", Enabled: false})
	require.NoError(t, err)
	assert.False(t, item.Enabled)
	assert.False(t, f.reload(b.ID).Items[0].Enabled)
}
