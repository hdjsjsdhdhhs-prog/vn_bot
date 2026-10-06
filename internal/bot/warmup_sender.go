package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

// WarmupImageStore is the picture storage the warm-up sender needs.
// *database.Service implements it.
type WarmupImageStore interface {
	GetWarmupImage(ctx context.Context, id uint, withData bool) (*database.WarmupImage, error)
	SetWarmupImageFileID(ctx context.Context, id uint, fileID string) error
}

var _ WarmupImageStore = (*database.Service)(nil)

// WarmupSender is the Telegram adapter of the customer warm-up. It sends a
// rendered service.WarmupMessage (parse_mode HTML) as a text message or as a
// photo with a caption, with its inline keyboard, through the bot's
// MessageSender — the same per-user rate limiting, metrics and logging as
// every other message. Failures are classified with the broadcast helpers
// (isUserBlockedError, isUserUnreachableError, isFloodError).
type WarmupSender struct {
	sender *MessageSender
	images WarmupImageStore
}

var _ service.WarmupSender = (*WarmupSender)(nil)

// NewWarmupSender builds the adapter on a MessageSender.
func NewWarmupSender(sender *MessageSender, images WarmupImageStore) *WarmupSender {
	return &WarmupSender{sender: sender, images: images}
}

// WarmupSender returns the warm-up adapter sharing this handler's sender
// (and therefore its rate limiter).
func (h *Handler) WarmupSender(images WarmupImageStore) *WarmupSender {
	return NewWarmupSender(h.sender, images)
}

// warmupInlineButton is an inline keyboard button with web_app support:
// telegram-bot-api v5.5.1 has no WebApp field, and reply_markup is sent as
// JSON, so the warm-up keyboard is its own type.
type warmupInlineButton struct {
	Text         string            `json:"text"`
	URL          string            `json:"url,omitempty"`
	CallbackData string            `json:"callback_data,omitempty"`
	WebApp       *warmupWebAppInfo `json:"web_app,omitempty"`
}

type warmupWebAppInfo struct {
	URL string `json:"url"`
}

type warmupInlineKeyboard struct {
	InlineKeyboard [][]warmupInlineButton `json:"inline_keyboard"`
}

// warmupKeyboard builds the keyboard rows (a button joins the previous row
// when SameRow is set). Buttons were validated and URL-less ones dropped by
// the service; the adapter re-checks so that an empty URL or a callback
// outside the whitelist is never sent.
func warmupKeyboard(buttons []service.WarmupButton) (*warmupInlineKeyboard, error) {
	if len(buttons) == 0 {
		return nil, nil
	}
	kb := &warmupInlineKeyboard{}
	for _, b := range buttons {
		btn := warmupInlineButton{Text: b.Text}
		switch b.Kind {
		case service.WarmupButtonURL:
			if b.Value == "" {
				continue
			}
			btn.URL = b.Value
		case service.WarmupButtonWebApp:
			if b.Value == "" {
				continue
			}
			btn.WebApp = &warmupWebAppInfo{URL: b.Value}
		case service.WarmupButtonCallback:
			if !isWarmupCallbackAction(b.Value) {
				return nil, fmt.Errorf("callback %q is not allowed", b.Value)
			}
			btn.CallbackData = b.Value
		default:
			return nil, fmt.Errorf("unknown button kind %q", b.Kind)
		}
		if last := len(kb.InlineKeyboard) - 1; b.SameRow && last >= 0 {
			kb.InlineKeyboard[last] = append(kb.InlineKeyboard[last], btn)
			continue
		}
		kb.InlineKeyboard = append(kb.InlineKeyboard, []warmupInlineButton{btn})
	}
	if len(kb.InlineKeyboard) == 0 {
		return nil, nil
	}
	return kb, nil
}

// isWarmupCallbackAction reports whether data is a callback a warm-up button
// may carry (the service whitelist).
func isWarmupCallbackAction(data string) bool {
	for _, a := range service.WarmupCallbackActions() {
		if a.Data == data {
			return true
		}
	}
	return false
}

// SendWarmup sends one warm-up message and returns its Telegram message ID.
func (s *WarmupSender) SendWarmup(ctx context.Context, chatID int64, msg *service.WarmupMessage) (int, error) {
	kb, err := warmupKeyboard(msg.Buttons)
	if err != nil {
		return 0, &service.WarmupSendError{Kind: service.WarmupSendPermanent, Err: err}
	}
	if msg.ImageID == nil {
		text := tgbotapi.NewMessage(chatID, msg.HTML)
		text.ParseMode = tgbotapi.ModeHTML
		text.DisableWebPagePreview = true
		if kb != nil {
			text.ReplyMarkup = kb
		}
		sent, err := s.sender.SendChattable(ctx, chatID, text)
		if err != nil {
			return 0, classifyWarmupSendError(ctx, err)
		}
		return sent.MessageID, nil
	}
	return s.sendPhoto(ctx, chatID, msg, kb)
}

func newWarmupPhoto(chatID int64, file tgbotapi.RequestFileData, msg *service.WarmupMessage, kb *warmupInlineKeyboard) tgbotapi.PhotoConfig {
	photo := tgbotapi.NewPhoto(chatID, file)
	photo.Caption = msg.HTML
	photo.ParseMode = tgbotapi.ModeHTML
	if kb != nil {
		photo.ReplyMarkup = kb
	}
	return photo
}

// sendPhoto sends the picture by its cached Telegram file_id; when Telegram
// rejects the file_id (expired, other bot, deleted) it forgets it, uploads
// the bytes stored in the database and caches the new file_id.
func (s *WarmupSender) sendPhoto(ctx context.Context, chatID int64, msg *service.WarmupMessage, kb *warmupInlineKeyboard) (int, error) {
	img, err := s.images.GetWarmupImage(ctx, *msg.ImageID, false)
	if err != nil {
		return 0, fmt.Errorf("load warmup image: %w", err)
	}
	if img.TGFileID != "" {
		sent, err := s.sender.SendChattable(ctx, chatID, newWarmupPhoto(chatID, tgbotapi.FileID(img.TGFileID), msg, kb))
		if err == nil {
			return sent.MessageID, nil
		}
		if !isWarmupFileIDError(err) {
			return 0, classifyWarmupSendError(ctx, err)
		}
		logger.Warn("Warmup image file_id rejected by Telegram, re-uploading",
			zap.Uint("image_id", img.ID), zap.Error(err))
		clearErr := s.images.SetWarmupImageFileID(ctx, img.ID, "")
		if clearErr != nil {
			logger.Warn("Failed to forget stale warmup image file_id", zap.Uint("image_id", img.ID), zap.Error(clearErr))
		}
	}

	full, err := s.images.GetWarmupImage(ctx, img.ID, true)
	if err != nil {
		return 0, fmt.Errorf("load warmup image bytes: %w", err)
	}
	name := "warmup.jpg"
	if full.Mime == "image/png" {
		name = "warmup.png"
	}
	sent, err := s.sender.SendChattable(ctx, chatID, newWarmupPhoto(chatID, tgbotapi.FileBytes{Name: name, Bytes: full.Data}, msg, kb))
	if err != nil {
		return 0, classifyWarmupSendError(ctx, err)
	}
	if fileID := largestPhotoFileID(sent.Photo); fileID != "" {
		err := s.images.SetWarmupImageFileID(ctx, img.ID, fileID)
		if err != nil {
			// The message is delivered; the next send uploads again.
			logger.Warn("Failed to cache warmup image file_id", zap.Uint("image_id", img.ID), zap.Error(err))
		}
	}
	return sent.MessageID, nil
}

// largestPhotoFileID returns the file_id of the biggest size Telegram made.
func largestPhotoFileID(sizes []tgbotapi.PhotoSize) string {
	best, bestArea := "", -1
	for _, p := range sizes {
		if area := p.Width * p.Height; area > bestArea && p.FileID != "" {
			best, bestArea = p.FileID, area
		}
	}
	return best
}

// isWarmupFileIDError recognizes Telegram rejecting a file_id ("wrong file
// identifier/HTTP URL specified", "wrong remote file identifier specified",
// "FILE_REFERENCE_EXPIRED", ...). Only a Bad Request can mean that: a flood
// or network error must not trigger a re-upload.
func isWarmupFileIDError(err error) bool {
	var msg string
	var apiErr *tgbotapi.Error
	if errors.As(err, &apiErr) {
		if apiErr.Code != 400 {
			return false
		}
		msg = strings.ToLower(apiErr.Message)
	} else {
		msg = strings.ToLower(err.Error())
		if !strings.Contains(msg, "bad request") {
			return false
		}
	}
	return strings.Contains(msg, "file identifier") || strings.Contains(msg, "remote file") ||
		strings.Contains(msg, "file_id") || strings.Contains(msg, "file reference") ||
		strings.Contains(msg, "file_reference")
}

// classifyWarmupSendError maps a Telegram error to a service.WarmupSendError.
// A cancelled context is returned as is.
func classifyWarmupSendError(ctx context.Context, err error) error {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ctx.Err())) {
		return err
	}
	kind := service.WarmupSendTransient
	var retryAfter = floodRetryDelay(err)
	switch {
	case isUserBlockedError(err):
		kind = service.WarmupSendBlocked
	case isUserUnreachableError(err):
		kind = service.WarmupSendUnreachable
	case isFloodError(err):
		kind = service.WarmupSendFlood
	default:
		var apiErr *tgbotapi.Error
		if errors.As(err, &apiErr) && apiErr.Code >= 400 && apiErr.Code < 500 {
			// 400 (can't parse entities, bad button URL, ...) / 403: the
			// request itself is rejected; resending it cannot succeed.
			kind = service.WarmupSendPermanent
		}
	}
	if kind != service.WarmupSendFlood {
		retryAfter = 0
	}
	return &service.WarmupSendError{Kind: kind, RetryAfter: retryAfter, Err: err}
}
