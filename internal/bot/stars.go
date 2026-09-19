package bot

import (
	"context"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"go.uber.org/zap"
)

// SetStarsPaymentService is startup-only, before processing any updates.
func (h *Handler) SetStarsPaymentService(stars *service.StarsPaymentService) { h.starsService = stars }

func (h *Handler) HandleStarsUpdate(ctx context.Context, update tgbotapi.Update) error {
	if update.PreCheckoutQuery != nil {
		return h.starsService.PreCheckout(ctx, update.PreCheckoutQuery)
	}
	if update.Message != nil && update.Message.SuccessfulPayment != nil {
		ctx, cancel := context.WithTimeout(ctx, HandlerTimeout)
		defer cancel()
		_, err := h.starsService.SuccessfulPayment(ctx, update)
		return err
	}
	return nil
}

func (h *Handler) StartStarsPaymentWorker(ctx context.Context) {
	if h.starsService == nil {
		return
	}
	h.bgWg.Go(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			passCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
			err := h.starsService.RetryPayments(passCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				logger.Warn("Stars settlement retry requires attention", zap.Error(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
