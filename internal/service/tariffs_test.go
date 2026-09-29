package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mini App output of the tariff editor: card presentation and the shared
// catalogue order reach /api/miniapp/offers; empty presentation stays empty
// (never null) so the Mini App falls back to its default card.
func TestPurchaseOffers_TariffPresentationAndOrder(t *testing.T) {
	p, db, _, legacy := purchaseServiceFixture(t)
	ctx := context.Background()
	tariffs := NewTariffService(db)

	created, err := tariffs.CreateTariff(ctx, "admin", "offers-create", TariffInput{
		Name: "Год", PlanID: legacy.PlanID, DurationDays: 365, PriceCents: 99900, Currency: "RUB",
		Description: "  Лучшая цена  ", Features: []string{" Все серверы ", "", "Поддержка"}, Badge: " Хит ", IsActive: true,
	})
	require.NoError(t, err)
	year := created.Tariff
	require.NotNil(t, year)
	assert.Equal(t, 1, year.SortOrder, "appended after the legacy product")

	offers, err := p.Offers(ctx)
	require.NoError(t, err)
	require.Len(t, offers, 2)
	assert.Equal(t, legacy.OfferID, offers[0].OfferID)
	assert.Equal(t, year.OfferID, offers[1].OfferID)

	// Legacy product without presentation: empty values, features [] on the wire.
	assert.Empty(t, offers[0].Description)
	assert.Empty(t, offers[0].Badge)
	assert.Equal(t, []string{}, offers[0].Features)
	encoded, err := json.Marshal(offers[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"features":[]`)
	assert.Contains(t, string(encoded), `"sort_order":0`)

	assert.Equal(t, "Лучшая цена", offers[1].Description, "text is normalized by the editor")
	assert.Equal(t, []string{"Все серверы", "Поддержка"}, offers[1].Features)
	assert.Equal(t, "Хит", offers[1].Badge)
	assert.Equal(t, 1, offers[1].SortOrder)

	// Reordering in the editor reorders the Mini App catalogue (not price).
	list, err := tariffs.ListTariffs(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	_, _, err = tariffs.ReorderTariffs(ctx, "admin", "offers-reorder", []uint{year.ID, legacy.ID})
	require.NoError(t, err)
	offers, err = p.Offers(ctx)
	require.NoError(t, err)
	require.Len(t, offers, 2)
	assert.Equal(t, []string{year.OfferID, legacy.OfferID}, []string{offers[0].OfferID, offers[1].OfferID})

	// A disabled tariff disappears from the Mini App.
	current, err := tariffs.GetTariff(ctx, year.ID)
	require.NoError(t, err)
	_, err = tariffs.SetTariffActive(ctx, "admin", "offers-disable", year.ID, current.Version, false)
	require.NoError(t, err)
	offers, err = p.Offers(ctx)
	require.NoError(t, err)
	require.Len(t, offers, 1)
	assert.Equal(t, legacy.OfferID, offers[0].OfferID)
}

func TestTariffService_UpdateReportsVersioning(t *testing.T) {
	_, db, sub, product := purchaseServiceFixture(t)
	ctx := context.Background()
	tariffs := NewTariffService(db)
	require.NoError(t, db.GetDB().Model(&database.Subscription{}).Where("id = ?", sub.ID).Update("product_id", product.ID).Error)

	view, err := tariffs.GetTariff(ctx, product.ID)
	require.NoError(t, err)
	require.True(t, view.InUse)

	in := TariffInput{Name: view.Name, PlanID: view.PlanID, DurationDays: view.DurationDays, PriceCents: view.PriceCents + 100,
		Currency: view.Currency, IsActive: true}
	out, err := tariffs.UpdateTariff(ctx, "admin", "svc-version", product.ID, view.Version, in)
	require.NoError(t, err)
	assert.True(t, out.Versioned)
	require.NotNil(t, out.Tariff)
	require.NotNil(t, out.Previous)
	assert.Equal(t, product.ID, out.Previous.ID)
	assert.NotEqual(t, product.ID, out.Tariff.ID)

	replay, err := tariffs.UpdateTariff(ctx, "admin", "svc-version", product.ID, view.Version, in)
	require.NoError(t, err)
	assert.True(t, replay.Replayed)
	assert.True(t, replay.Versioned)
	assert.Equal(t, out.Tariff.ID, replay.Tariff.ID)
}
