package database

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tariff editor repository: CRUD, versioning of used products, optimistic
// locking, idempotency, delete protection and the shared catalogue order.

type tariffFixture struct {
	t    *testing.T
	svc  *Service
	ctx  context.Context
	plan *Plan
	keys int
}

func newTariffFixture(t *testing.T) *tariffFixture {
	t.Helper()
	svc := newTestService(t)
	plan := &Plan{Name: "tariff-premium", IsActive: true, DevicesLimit: 3}
	require.NoError(t, svc.db.Create(plan).Error)
	return &tariffFixture{t: t, svc: svc, ctx: context.Background(), plan: plan}
}

func (f *tariffFixture) meta() AdminConfigMeta {
	f.keys++
	return AdminConfigMeta{Actor: "admin", RequestKey: "key-" + strings.Repeat("x", f.keys)}
}

func (f *tariffFixture) input(name string, price int64) TariffInput {
	return TariffInput{
		Name: name, PlanID: f.plan.ID, DurationDays: 30, PriceCents: price, Currency: "RUB",
		Description: "Для всей семьи", Features: []string{"3 устройства", "Без рекламы"}, Badge: "Хит", IsActive: true,
	}
}

func (f *tariffFixture) create(name string, price int64) *Product {
	f.t.Helper()
	res, err := f.svc.CreateTariff(f.ctx, f.meta(), f.input(name, price))
	require.NoError(f.t, err)
	return f.product(res.TargetID)
}

func (f *tariffFixture) product(id uint) *Product {
	f.t.Helper()
	var p Product
	require.NoError(f.t, f.svc.db.First(&p, id).Error)
	return &p
}

func (f *tariffFixture) auditCount() int64 {
	f.t.Helper()
	var n int64
	require.NoError(f.t, f.svc.db.Model(&AdminAuditLog{}).Count(&n).Error)
	return n
}

// useInOrder attaches an order and a subscription to the product, as a paid
// purchase would.
func (f *tariffFixture) useInOrder(p *Product, telegramID int64) (*Order, *Subscription) {
	f.t.Helper()
	sub := createTestSubscription(f.t, f.svc, telegramID, "buyer", "buyer-client-"+strconv.FormatInt(telegramID, 10))
	require.NoError(f.t, f.svc.db.Model(sub).Updates(map[string]any{"product_id": p.ID, "price_paid_cents": p.PriceCents}).Error)
	order := &Order{SubscriptionID: sub.ID, ProductID: p.ID, Status: OrderStatusPaid, AmountCents: p.PriceCents, Currency: p.Currency}
	require.NoError(f.t, f.svc.db.Create(order).Error)
	var reloaded Subscription
	require.NoError(f.t, f.svc.db.First(&reloaded, sub.ID).Error)
	return order, &reloaded
}

func TestTariffCRUD(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)

	first := f.create("Месяц", 19900)
	assert.Equal(t, 1, first.Version)
	assert.Equal(t, 0, first.SortOrder)
	assert.True(t, first.IsActive)
	assert.Len(t, first.OfferID, 32, "a public offer reference is assigned")
	assert.Equal(t, []string{"3 устройства", "Без рекламы"}, DecodeTariffFeatures(first.Features))
	assert.Equal(t, "Хит", first.Badge)

	second := f.create("Год", 99900)
	assert.Equal(t, 1, second.SortOrder, "a new tariff is appended to the catalogue")

	disabled := f.input("Выключенный", 500)
	disabled.IsActive = false
	res, err := f.svc.CreateTariff(f.ctx, f.meta(), disabled)
	require.NoError(t, err)
	assert.False(t, f.product(res.TargetID).IsActive, "is_active=false is stored, not replaced by the column default")

	row, err := f.svc.GetTariff(f.ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, f.plan.Name, row.PlanName)
	assert.False(t, row.InUse())

	// An unused tariff is edited in place, purchase terms included.
	update := f.input("Месяц+", 24900)
	update.Features = []string{"5 устройств"}
	update.Badge = ""
	res, err = f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: first.ID, Version: 1, TariffInput: update})
	require.NoError(t, err)
	require.Equal(t, first.ID, res.TargetID, "no new version for an unused product")
	edited := f.product(first.ID)
	assert.Equal(t, "Месяц+", edited.Name)
	assert.Equal(t, int64(24900), edited.PriceCents)
	assert.Equal(t, 2, edited.Version)
	assert.Equal(t, first.OfferID, edited.OfferID)
	assert.Equal(t, []string{"5 устройств"}, DecodeTariffFeatures(edited.Features))

	list, err := f.svc.ListTariffs(f.ctx)
	require.NoError(t, err)
	require.Len(t, list, 3)

	res, err = f.svc.DeleteTariff(f.ctx, f.meta(), second.ID, second.Version)
	require.NoError(t, err)
	assert.Equal(t, second.ID, res.TargetID)
	_, err = f.svc.GetTariff(f.ctx, second.ID)
	require.ErrorIs(t, err, ErrProductNotFound)
	assert.Equal(t, int64(5), f.auditCount(), "every mutation is audited")
}

func TestTariffValidation(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	trial, err := f.svc.GetPlanByName(f.ctx, TrialPlanName)
	require.NoError(t, err)
	free, err := f.svc.GetPlanByName(f.ctx, FreePlanName)
	require.NoError(t, err)

	cases := map[string]struct {
		mutate func(*TariffInput)
		field  string
	}{
		"empty name":        {func(in *TariffInput) { in.Name = "" }, "name"},
		"long name":         {func(in *TariffInput) { in.Name = strings.Repeat("я", TariffMaxNameLength+1) }, "name"},
		"zero duration":     {func(in *TariffInput) { in.DurationDays = 0 }, "duration_days"},
		"too long duration": {func(in *TariffInput) { in.DurationDays = MaxSubscriptionRenewalDays + 1 }, "duration_days"},
		"free price":        {func(in *TariffInput) { in.PriceCents = 0 }, "price_cents"},
		"bad currency":      {func(in *TariffInput) { in.Currency = "rub" }, "currency"},
		"long description":  {func(in *TariffInput) { in.Description = strings.Repeat("a", TariffMaxDescriptionLength+1) }, "description"},
		"too many features": {func(in *TariffInput) { in.Features = make([]string, TariffMaxFeatures+1) }, "features"},
		"blank feature":     {func(in *TariffInput) { in.Features = []string{"ok", ""} }, "features"},
		"long badge":        {func(in *TariffInput) { in.Badge = strings.Repeat("b", TariffMaxBadgeLength+1) }, "badge"},
		"negative order":    {func(in *TariffInput) { v := -1; in.SortOrder = &v }, "sort_order"},
		"missing plan":      {func(in *TariffInput) { in.PlanID = 99999 }, "plan_id"},
		"trial plan":        {func(in *TariffInput) { in.PlanID = trial.ID }, "plan_id"},
		"free plan":         {func(in *TariffInput) { in.PlanID = free.ID }, "plan_id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := f.input("Тариф", 100)
			tc.mutate(&in)
			_, err := f.svc.CreateTariff(f.ctx, AdminConfigMeta{Actor: "admin", RequestKey: "validate-" + strings.ReplaceAll(name, " ", "-")}, in)
			var fieldErr *TariffFieldError
			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, tc.field, fieldErr.Field)
			require.ErrorIs(t, err, ErrTariffInvalid)
		})
	}
	var products int64
	require.NoError(t, f.svc.db.Model(&Product{}).Count(&products).Error)
	assert.Zero(t, products, "rejected tariffs are never written")
	assert.Zero(t, f.auditCount(), "rejections are not audited")
}

func TestTariffUpdate_UsedProductChangingTermsCreatesNewVersion(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	original := f.create("Месяц", 19900)
	f.create("Год", 99900)
	order, sub := f.useInOrder(original, 700001)

	changed := f.input("Месяц", 24900)
	changed.Description = "Новая цена"
	res, err := f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: original.ID, Version: original.Version, TariffInput: changed})
	require.NoError(t, err)
	require.NotEqual(t, original.ID, res.TargetID, "changing the price of a used product creates a successor")

	successor := f.product(res.TargetID)
	assert.Equal(t, int64(24900), successor.PriceCents)
	assert.Equal(t, "Новая цена", successor.Description)
	assert.True(t, successor.IsActive)
	assert.Equal(t, 1, successor.Version)
	require.NotNil(t, successor.PreviousID)
	assert.Equal(t, original.ID, *successor.PreviousID)
	assert.NotEqual(t, original.OfferID, successor.OfferID, "the successor is a new offer")
	assert.Equal(t, original.SortOrder, successor.SortOrder, "the successor keeps the catalogue position")

	retired := f.product(original.ID)
	assert.False(t, retired.IsActive, "the original is retired")
	assert.Equal(t, original.Version+1, retired.Version)
	assert.Equal(t, original.PriceCents, retired.PriceCents, "historical terms are untouched")
	assert.Equal(t, original.Name, retired.Name)
	assert.Equal(t, original.DurationDays, retired.DurationDays)
	assert.Equal(t, original.OfferID, retired.OfferID)

	// Old orders and subscriptions keep pointing at the original terms.
	var reloadedOrder Order
	require.NoError(t, f.svc.db.First(&reloadedOrder, order.ID).Error)
	assert.Equal(t, original.ID, reloadedOrder.ProductID)
	assert.Equal(t, int64(19900), reloadedOrder.AmountCents)
	var reloadedSub Subscription
	require.NoError(t, f.svc.db.First(&reloadedSub, sub.ID).Error)
	require.NotNil(t, reloadedSub.ProductID)
	assert.Equal(t, original.ID, *reloadedSub.ProductID)
	assert.Equal(t, int64(19900), reloadedSub.PricePaidCents)

	rows, err := f.svc.ListTariffs(f.ctx)
	require.NoError(t, err)
	byID := map[uint]TariffRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	require.NotNil(t, byID[original.ID].ReplacedByID)
	assert.Equal(t, successor.ID, *byID[original.ID].ReplacedByID)
	assert.True(t, byID[original.ID].InUse())
	assert.False(t, byID[successor.ID].InUse())

	active, err := f.svc.ListActiveProducts(f.ctx)
	require.NoError(t, err)
	for _, p := range active {
		assert.NotEqual(t, original.ID, p.ID, "the retired version is no longer sold")
	}

	// A retired version is frozen: no edit, no re-enable, no delete.
	_, err = f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: original.ID, Version: retired.Version, TariffInput: f.input("Месяц", 1)})
	require.ErrorIs(t, err, ErrTariffSuperseded)
	_, err = f.svc.SetTariffActive(f.ctx, f.meta(), original.ID, retired.Version, true)
	require.ErrorIs(t, err, ErrTariffSuperseded)
	_, err = f.svc.DeleteTariff(f.ctx, f.meta(), original.ID, retired.Version)
	require.ErrorIs(t, err, ErrTariffInUse)
}

func TestTariffUpdate_EachImmutableTermVersionsUsedProduct(t *testing.T) {
	t.Parallel()
	other := func(f *tariffFixture) uint {
		p := &Plan{Name: "tariff-other", IsActive: true, DevicesLimit: 1}
		require.NoError(t, f.svc.db.Create(p).Error)
		return p.ID
	}
	cases := map[string]func(*tariffFixture, *TariffInput){
		"name":     func(_ *tariffFixture, in *TariffInput) { in.Name = "Другое" },
		"price":    func(_ *tariffFixture, in *TariffInput) { in.PriceCents++ },
		"duration": func(_ *tariffFixture, in *TariffInput) { in.DurationDays = 90 },
		"currency": func(_ *tariffFixture, in *TariffInput) { in.Currency = "XTR" },
		"plan":     func(f *tariffFixture, in *TariffInput) { in.PlanID = other(f) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newTariffFixture(t)
			p := f.create("Месяц", 19900)
			f.useInOrder(p, 700100)
			in := f.input("Месяц", 19900)
			mutate(f, &in)
			res, err := f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: p.ID, Version: p.Version, TariffInput: in})
			require.NoError(t, err)
			assert.NotEqual(t, p.ID, res.TargetID)
		})
	}
}

func TestTariffUpdate_UsedProductPresentationEditsInPlace(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	p := f.create("Месяц", 19900)
	f.useInOrder(p, 700002)

	in := f.input("Месяц", 19900)
	in.Description, in.Features, in.Badge, in.IsActive = "Обновлённое описание", []string{"Новое"}, "Выгодно", false
	order := 7
	in.SortOrder = &order
	res, err := f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: p.ID, Version: p.Version, TariffInput: in})
	require.NoError(t, err)
	assert.Equal(t, p.ID, res.TargetID, "presentation, position and enabled flag never version a product")
	saved := f.product(p.ID)
	assert.Equal(t, "Обновлённое описание", saved.Description)
	assert.Equal(t, "Выгодно", saved.Badge)
	assert.Equal(t, 7, saved.SortOrder)
	assert.False(t, saved.IsActive)
	assert.Equal(t, 2, saved.Version)
	assert.Equal(t, p.PriceCents, saved.PriceCents)
	var n int64
	require.NoError(t, f.svc.db.Model(&Product{}).Count(&n).Error)
	assert.Equal(t, int64(1), n)
}

func TestTariffOptimisticLock(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	p := f.create("Месяц", 19900)
	_, err := f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: p.ID, Version: 1, TariffInput: f.input("A", 100)})
	require.NoError(t, err)
	before := f.auditCount()

	// A second editor still holding version 1 must not overwrite the change.
	_, err = f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: p.ID, Version: 1, TariffInput: f.input("B", 200)})
	require.ErrorIs(t, err, ErrTariffVersionConflict)
	_, err = f.svc.SetTariffActive(f.ctx, f.meta(), p.ID, 1, false)
	require.ErrorIs(t, err, ErrTariffVersionConflict)
	_, err = f.svc.DeleteTariff(f.ctx, f.meta(), p.ID, 1)
	require.ErrorIs(t, err, ErrTariffVersionConflict)
	assert.Equal(t, "A", f.product(p.ID).Name)
	assert.Equal(t, before, f.auditCount(), "conflicts are not audited")

	_, err = f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: 99999, Version: 1, TariffInput: f.input("C", 1)})
	require.ErrorIs(t, err, ErrProductNotFound)
}

func TestTariffOptimisticLock_ConcurrentEditorsOneWins(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	p := f.create("Месяц", 19900)
	const editors = 4
	errs := make([]error, editors)
	var wg sync.WaitGroup
	for i := range editors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := f.input("Editor", int64(1000+i))
			_, errs[i] = f.svc.UpdateTariff(f.ctx, AdminConfigMeta{Actor: "admin", RequestKey: "concurrent-" + string(rune('a'+i))},
				TariffUpdateInput{ID: p.ID, Version: 1, TariffInput: in})
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
			continue
		}
		// A loser sees the conflict, or SQLite's busy writer; never a double write.
		assert.True(t, errors.Is(err, ErrTariffVersionConflict) || strings.Contains(err.Error(), "locked") ||
			strings.Contains(err.Error(), "busy"), "unexpected error: %v", err)
	}
	assert.Equal(t, 1, wins, "exactly one editor holding version 1 commits")
	assert.Equal(t, 2, f.product(p.ID).Version)
}

func TestTariffIdempotency(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	meta := AdminConfigMeta{Actor: "admin", RequestKey: "create-once"}
	first, err := f.svc.CreateTariff(f.ctx, meta, f.input("Месяц", 19900))
	require.NoError(t, err)
	replay, err := f.svc.CreateTariff(f.ctx, meta, f.input("Месяц", 19900))
	require.NoError(t, err)
	assert.True(t, replay.Replayed)
	assert.Equal(t, first.TargetID, replay.TargetID)
	assert.Equal(t, first.Audit.ID, replay.Audit.ID)
	var n int64
	require.NoError(t, f.svc.db.Model(&Product{}).Count(&n).Error)
	assert.Equal(t, int64(1), n, "a retried create does not duplicate the tariff")

	_, err = f.svc.CreateTariff(f.ctx, meta, f.input("Другой", 1))
	require.ErrorIs(t, err, ErrAdminRequestKeyConflict)

	// A versioning update replays to the same successor.
	p := f.product(first.TargetID)
	f.useInOrder(p, 700003)
	updateMeta := AdminConfigMeta{Actor: "admin", RequestKey: "update-once"}
	in := TariffUpdateInput{ID: p.ID, Version: p.Version, TariffInput: f.input("Месяц", 29900)}
	updated, err := f.svc.UpdateTariff(f.ctx, updateMeta, in)
	require.NoError(t, err)
	again, err := f.svc.UpdateTariff(f.ctx, updateMeta, in)
	require.NoError(t, err)
	assert.True(t, again.Replayed)
	assert.Equal(t, updated.TargetID, again.TargetID)
	require.NoError(t, f.svc.db.Model(&Product{}).Count(&n).Error)
	assert.Equal(t, int64(2), n, "a retried versioning update creates one successor")
}

func TestTariffDeleteProtection(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)

	ordered := f.create("С заказом", 100)
	sub := createTestSubscription(t, f.svc, 700010, "order-only", "order-only-client")
	require.NoError(t, f.svc.db.Create(&Order{SubscriptionID: sub.ID, ProductID: ordered.ID, Status: OrderStatusPending, AmountCents: 100, Currency: "RUB"}).Error)
	_, err := f.svc.DeleteTariff(f.ctx, f.meta(), ordered.ID, ordered.Version)
	require.ErrorIs(t, err, ErrTariffInUse, "any order, even pending, protects the product")

	subscribed := f.create("С подпиской", 200)
	holder := createTestSubscription(t, f.svc, 700011, "sub-only", "sub-only-client")
	require.NoError(t, f.svc.db.Model(holder).Update("product_id", subscribed.ID).Error)
	_, err = f.svc.DeleteTariff(f.ctx, f.meta(), subscribed.ID, subscribed.Version)
	require.ErrorIs(t, err, ErrTariffInUse, "a subscription reference protects the product")

	for _, id := range []uint{ordered.ID, subscribed.ID} {
		_, err := f.svc.GetTariff(f.ctx, id)
		require.NoError(t, err, "protected tariffs survive")
	}

	// Disabling remains possible for used tariffs.
	_, err = f.svc.SetTariffActive(f.ctx, f.meta(), ordered.ID, ordered.Version, false)
	require.NoError(t, err)
	assert.False(t, f.product(ordered.ID).IsActive)

	// Deleting an unused successor leaves the retired original retired.
	used := f.create("Версии", 300)
	f.useInOrder(used, 700012)
	res, err := f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: used.ID, Version: used.Version, TariffInput: f.input("Версии", 400)})
	require.NoError(t, err)
	successor := f.product(res.TargetID)
	_, err = f.svc.DeleteTariff(f.ctx, f.meta(), successor.ID, successor.Version)
	require.NoError(t, err)
	assert.False(t, f.product(used.ID).IsActive)
}

func TestTariffReorderDrivesCatalogueOrder(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	cheap := f.create("Дешёвый", 100)
	mid := f.create("Средний", 500)
	expensive := f.create("Дорогой", 900)

	_, err := f.svc.ReorderTariffs(f.ctx, f.meta(), []uint{expensive.ID, cheap.ID, mid.ID})
	require.NoError(t, err)
	assert.Equal(t, 0, f.product(expensive.ID).SortOrder)
	assert.Equal(t, 1, f.product(cheap.ID).SortOrder)
	assert.Equal(t, 2, f.product(mid.ID).SortOrder)
	assert.Equal(t, 2, f.product(expensive.ID).Version, "moved rows get a new version")
	assert.Equal(t, 2, f.product(cheap.ID).Version)
	assert.Equal(t, 2, f.product(mid.ID).Version)

	active, err := f.svc.ListActiveProducts(f.ctx)
	require.NoError(t, err)
	require.Len(t, active, 3)
	assert.Equal(t, []uint{expensive.ID, cheap.ID, mid.ID}, []uint{active[0].ID, active[1].ID, active[2].ID},
		"the bot catalogue follows sort_order, not price")

	rows, err := f.svc.ListTariffs(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, expensive.ID, rows[0].ID)

	// A list built before another tariff was created is stale.
	f.create("Новый", 50)
	_, err = f.svc.ReorderTariffs(f.ctx, f.meta(), []uint{expensive.ID, cheap.ID, mid.ID})
	require.ErrorIs(t, err, ErrTariffOrderStale)
	_, err = f.svc.ReorderTariffs(f.ctx, f.meta(), []uint{expensive.ID, expensive.ID})
	require.ErrorIs(t, err, ErrAdminInvalidRequest)

	// An editor opened before the reorder cannot overwrite the new position.
	_, err = f.svc.UpdateTariff(f.ctx, f.meta(), TariffUpdateInput{ID: cheap.ID, Version: 1, TariffInput: f.input("Дешёвый", 100)})
	require.ErrorIs(t, err, ErrTariffVersionConflict)
}

func TestListActiveProducts_TiesFallBackToPriceThenID(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	zero := 0
	for _, price := range []int64{300, 100, 200} {
		in := f.input("Tie", price)
		in.SortOrder = &zero
		_, err := f.svc.CreateTariff(f.ctx, f.meta(), in)
		require.NoError(t, err)
	}
	active, err := f.svc.ListActiveProducts(f.ctx)
	require.NoError(t, err)
	require.Len(t, active, 3)
	assert.Equal(t, []int64{100, 200, 300}, []int64{active[0].PriceCents, active[1].PriceCents, active[2].PriceCents})
}

func TestListAdminPlans(t *testing.T) {
	t.Parallel()
	f := newTariffFixture(t)
	f.create("Месяц", 100)
	p := f.create("Год", 900)
	f.useInOrder(p, 700020)
	builder := &SubscriptionBuilder{Name: "tariff-builder", Version: 1}
	require.NoError(t, f.svc.db.Create(builder).Error)
	require.NoError(t, f.svc.db.Model(f.plan).Update("subscription_builder_id", builder.ID).Error)

	plans, err := f.svc.ListAdminPlans(f.ctx)
	require.NoError(t, err)
	byName := map[string]AdminPlanRow{}
	for _, plan := range plans {
		byName[plan.Name] = plan
	}
	premium := byName[f.plan.Name]
	assert.Equal(t, int64(2), premium.Tariffs)
	assert.True(t, premium.Selectable())
	assert.Equal(t, "tariff-builder", premium.BuilderName)
	require.NotNil(t, premium.SubscriptionBuilderID)
	assert.False(t, byName[TrialPlanName].Selectable())
	assert.False(t, byName[FreePlanName].Selectable())
}

func TestTariffFeaturesCodec(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "[]", EncodeTariffFeatures(nil))
	assert.Equal(t, `["a","б"]`, EncodeTariffFeatures([]string{"a", "б"}))
	assert.Equal(t, []string{}, DecodeTariffFeatures(""))
	assert.Equal(t, []string{}, DecodeTariffFeatures("not json"))
	assert.Equal(t, []string{}, DecodeTariffFeatures("null"))
	assert.Equal(t, []string{"x"}, DecodeTariffFeatures(`["x"]`))
}
