package bot

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/require"
)

type starsAnswerClient struct {
	answers  int
	approved bool
}

func (*starsAnswerClient) CreateInvoiceLink(context.Context, string, string, int64) (string, error) {
	return "", service.ErrPaymentDisabled
}
func (c *starsAnswerClient) AnswerPreCheckout(_ context.Context, _ string, ok bool) error {
	c.answers++
	c.approved = ok
	return nil
}

func TestStarsUpdate_PaymentRoutingBeforeNormalHandlers(t *testing.T) {
	client := &starsAnswerClient{}
	h := &Handler{} // No sender/rate limiter: payment updates must bypass normal routing.
	h.SetStarsPaymentService(service.NewStarsPaymentService(nil, client))
	update := tgbotapi.Update{PreCheckoutQuery: &tgbotapi.PreCheckoutQuery{ID: "invalid", InvoicePayload: "malformed"}}
	require.NotPanics(t, func() { h.HandleUpdate(context.Background(), update) })
	require.Equal(t, 1, client.answers)
	require.False(t, client.approved)
	require.ErrorIs(t, h.HandleStarsUpdate(context.Background(), tgbotapi.Update{PreCheckoutQuery: &tgbotapi.PreCheckoutQuery{}}), database.ErrStarsPurchaseInvalid)
	require.NotPanics(t, func() {
		h.HandleUpdate(context.Background(), tgbotapi.Update{Message: &tgbotapi.Message{SuccessfulPayment: &tgbotapi.SuccessfulPayment{}}})
	})
	h.SetStarsPaymentService(nil)
	require.NotPanics(t, func() { h.HandleUpdate(context.Background(), update) })
}
