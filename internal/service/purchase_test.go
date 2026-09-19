package service

import (
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPurchaseInfo_NullableOrderFields(t *testing.T) {
	buyer := int64(42)
	ref := "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name   string
		record *database.PurchaseRecord
	}{
		{"missing record", nil},
		{"legacy order", &database.PurchaseRecord{Order: database.Order{Status: database.OrderStatusPaid}}},
		{"missing buyer", &database.PurchaseRecord{Order: database.Order{PurchaseID: &ref}}},
		{"missing purchase ID", &database.PurchaseRecord{Order: database.Order{BuyerTelegramID: &buyer}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := purchaseInfo(tc.record, buyer)
			require.ErrorIs(t, err, ErrPurchaseAccessDenied)
			assert.Nil(t, info)
		})
	}
	createdAt := time.Now().UTC()
	deadline := createdAt.Add(30 * time.Minute)
	record := &database.PurchaseRecord{
		Order: database.Order{PurchaseID: &ref, BuyerTelegramID: &buyer, PurchaseExpiresAt: &deadline,
			Status: database.OrderStatusPending, AmountCents: 5000, Currency: "RUB", CreatedAt: createdAt},
		Product: database.Product{OfferID: ref, Name: "Monthly", DurationDays: 30},
	}
	info, err := purchaseInfo(record, buyer)
	require.NoError(t, err)
	assert.Equal(t, &PurchaseOrderInfo{OrderID: ref, OfferID: ref, Name: "Monthly", DurationDays: 30,
		AmountCents: 5000, Currency: "RUB", Status: database.OrderStatusPending, CreatedAt: createdAt, ExpiresAt: &deadline}, info)
	info, err = purchaseInfo(record, buyer+1)
	require.ErrorIs(t, err, ErrPurchaseAccessDenied)
	assert.Nil(t, info)
}
