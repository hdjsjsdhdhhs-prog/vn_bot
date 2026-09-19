package telegramstars

import (
	"context"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/logger"
)

// Updates uses the existing official getUpdates transport, but unlike the SDK's
// GetUpdatesChan it cannot acknowledge successful_payment before durable capture.
// After a crash Telegram redelivers unacknowledged updates; the receipt worker
// recovers captured payments even if delivery to the handler never completed.
func Updates(ctx context.Context, bot *tgbotapi.BotAPI, timeout int, capture func(context.Context, tgbotapi.Update) error) tgbotapi.UpdatesChannel {
	updates := make(chan tgbotapi.Update, 100)
	go func() {
		defer close(updates)
		api := *bot
		api.Debug = false
		api.Client = contextClient{ctx, bot.Client}
		config := tgbotapi.NewUpdate(0)
		config.Timeout = timeout
		config.AllowedUpdates = []string{"message", "callback_query", "pre_checkout_query"}
		for ctx.Err() == nil {
			batch, err := api.GetUpdates(config)
			if err != nil {
				// SDK/HTTP errors may contain the credential-bearing bot URL.
				if ctx.Err() == nil {
					logger.Warn("Telegram getUpdates failed; retrying")
				}
				if !wait(ctx) {
					return
				}
				continue
			}
			for _, update := range batch {
				if update.UpdateID < config.Offset {
					continue
				}
				// Retry capture without advancing offset. No lossy acknowledge on a DB
				// outage, even when later updates in the same batch are ordinary messages.
				for {
					if err := capture(ctx, update); err == nil {
						break
					}
					if ctx.Err() == nil {
						logger.Warn("Telegram payment capture failed; update remains unacknowledged")
					}
					if !wait(ctx) {
						return
					}
				}
				select {
				case updates <- update:
					config.Offset = update.UpdateID + 1
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return updates
}

func wait(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
