package bot

import (
	"context"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// Customer warm-up hooks in the update flow: /start records the contact
// (the "bot started" trigger), and callbacks pressed on a warm-up photo
// message are redirected to a fresh text message.

// WarmupContactRecorder records that a user talked to the bot.
// *service.WarmupService implements it.
type WarmupContactRecorder interface {
	TouchContact(ctx context.Context, telegramID int64, username, languageCode string) error
}

// SetWarmupContacts wires the warm-up contact recorder (nil disables it).
// Call before the update loop starts.
func (h *Handler) SetWarmupContacts(r WarmupContactRecorder) {
	h.warmupContacts = r
}

// recordWarmupContact is best-effort: /start must never fail because of the
// warm-up. Only private chats are contacts (messages go to the user's chat).
func (h *Handler) recordWarmupContact(ctx context.Context, msg *tgbotapi.Message) {
	if h.warmupContacts == nil || msg == nil || msg.From == nil || msg.Chat == nil || !msg.Chat.IsPrivate() {
		return
	}
	err := h.warmupContacts.TouchContact(ctx, msg.Chat.ID, msg.From.UserName, msg.From.LanguageCode)
	if err != nil {
		logger.Warn("Failed to record warmup contact", zap.Int64("chat_id", msg.Chat.ID), zap.Error(err))
	}
}

// warmupLoadingText is the placeholder of a redirected callback; the
// callback handler replaces it with its screen right away.
const warmupLoadingText = "⏳ Загрузка..."

// detachWarmupMediaCallback handles a whitelisted warm-up callback pressed on
// a photo message. Every callback handler edits the pressed message with
// editMessageText, which Telegram refuses for a photo ("there is no text in
// the message to edit"), and changing the caption would overwrite the
// warm-up offer and is limited to 1024 characters. Instead — the existing
// showLoadingMessage(chatID, 0) mechanism — a new text message is sent and
// the callback is re-targeted to it, so the handler edits that message as
// usual and the photo stays untouched. Text messages, and photo messages of
// other flows (QR screens use back_to_subscription/back_to_invite, which are
// not warm-up actions and keep their delete-on-back behaviour), are not
// affected. ok is false when the placeholder could not be sent.
func (h *Handler) detachWarmupMediaCallback(update tgbotapi.Update) (tgbotapi.Update, bool) {
	cq := update.CallbackQuery
	if cq == nil || cq.Message == nil || len(cq.Message.Photo) == 0 || !isWarmupCallbackAction(cq.Data) {
		return update, true
	}
	chatID := cq.Message.Chat.ID
	messageID := h.showLoadingMessage(chatID, 0)
	if messageID == 0 {
		return update, false
	}
	msg := *cq.Message
	msg.MessageID, msg.Photo, msg.Caption, msg.CaptionEntities, msg.ReplyMarkup = messageID, nil, "", nil, nil
	msg.Text = warmupLoadingText
	redirected := *cq
	redirected.Message = &msg
	update.CallbackQuery = &redirected
	return update, true
}
