package bot

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/ratelimiter"
	"github.com/kereal/rs8kvn_bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warmupFakeBot records every Chattable and answers from a script.
type warmupFakeBot struct {
	mu      sync.Mutex
	sent    []tgbotapi.Chattable
	results []warmupFakeResult
}

type warmupFakeResult struct {
	msg tgbotapi.Message
	err error
}

func (b *warmupFakeBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, c)
	if len(b.results) > 0 {
		r := b.results[0]
		b.results = b.results[1:]
		return r.msg, r.err
	}
	return tgbotapi.Message{MessageID: 100 + len(b.sent)}, nil
}

func (b *warmupFakeBot) Request(tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (b *warmupFakeBot) calls() []tgbotapi.Chattable {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]tgbotapi.Chattable(nil), b.sent...)
}

// warmupFakeImages is an in-memory WarmupImageStore.
type warmupFakeImages struct {
	mu      sync.Mutex
	images  map[uint]database.WarmupImage
	fileIDs []string // every SetWarmupImageFileID value, in order
}

func (s *warmupFakeImages) GetWarmupImage(_ context.Context, id uint, withData bool) (*database.WarmupImage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	img, ok := s.images[id]
	if !ok {
		return nil, database.ErrWarmupImageNotFound
	}
	if !withData {
		img.Data = nil
	}
	return &img, nil
}

func (s *warmupFakeImages) SetWarmupImageFileID(_ context.Context, id uint, fileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	img := s.images[id]
	img.TGFileID = fileID
	s.images[id] = img
	s.fileIDs = append(s.fileIDs, fileID)
	return nil
}

func newWarmupSenderFixture(results ...warmupFakeResult) (*WarmupSender, *warmupFakeBot, *warmupFakeImages) {
	bot := &warmupFakeBot{results: results}
	images := &warmupFakeImages{images: map[uint]database.WarmupImage{}}
	sender := NewMessageSender(bot, ratelimiter.NewPerUserRateLimiter(1000, 1000))
	return NewWarmupSender(sender, images), bot, images
}

func keyboardJSON(t *testing.T, markup any) string {
	t.Helper()
	encoded, err := json.Marshal(markup)
	require.NoError(t, err)
	return string(encoded)
}

func TestWarmupSender_TextWithHTMLAndButtons(t *testing.T) {
	t.Parallel()
	sender, bot, _ := newWarmupSenderFixture()
	msg := &service.WarmupMessage{
		HTML: "<b>Привет, @&lt;user&gt;</b>",
		Buttons: []service.WarmupButton{
			{Text: "Тарифы", Kind: service.WarmupButtonCallback, Value: "buy_premium_list"},
			{Text: "Сайт", Kind: service.WarmupButtonURL, Value: "https://example.com/trial"},
			{Text: "Mini App", Kind: service.WarmupButtonWebApp, Value: "https://example.com/miniapp/"},
			{Text: "Без URL", Kind: service.WarmupButtonURL, Value: ""},
		},
	}

	id, err := sender.SendWarmup(context.Background(), 42, msg)
	require.NoError(t, err)
	assert.Equal(t, 101, id)
	calls := bot.calls()
	require.Len(t, calls, 1)
	text, ok := calls[0].(tgbotapi.MessageConfig)
	require.True(t, ok, "a message without a picture is a text message")
	assert.Equal(t, int64(42), text.ChatID)
	assert.Equal(t, msg.HTML, text.Text)
	assert.Equal(t, tgbotapi.ModeHTML, text.ParseMode, "warm-up messages are Telegram HTML, never MarkdownV2")
	assert.True(t, text.DisableWebPagePreview)
	assert.JSONEq(t, `{"inline_keyboard":[
		[{"text":"Тарифы","callback_data":"buy_premium_list"}],
		[{"text":"Сайт","url":"https://example.com/trial"}],
		[{"text":"Mini App","web_app":{"url":"https://example.com/miniapp/"}}]
	]}`, keyboardJSON(t, text.ReplyMarkup), "a URL button without a URL is never sent")
}

func TestWarmupSender_ButtonRows(t *testing.T) {
	t.Parallel()
	sender, bot, _ := newWarmupSenderFixture()
	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{
		HTML: "x",
		Buttons: []service.WarmupButton{
			{Text: "Активировать", Kind: service.WarmupButtonURL, Value: "https://example.com/trial"},
			{Text: "Тарифы", Kind: service.WarmupButtonCallback, Value: "buy_premium_list"},
			{Text: "Поддержка", Kind: service.WarmupButtonCallback, Value: "menu_support", SameRow: true},
			{Text: "Помощь", Kind: service.WarmupButtonCallback, Value: "menu_help"},
		},
	})
	require.NoError(t, err)
	text := bot.calls()[0].(tgbotapi.MessageConfig)
	assert.JSONEq(t, `{"inline_keyboard":[
		[{"text":"Активировать","url":"https://example.com/trial"}],
		[{"text":"Тарифы","callback_data":"buy_premium_list"},{"text":"Поддержка","callback_data":"menu_support"}],
		[{"text":"Помощь","callback_data":"menu_help"}]
	]}`, keyboardJSON(t, text.ReplyMarkup), "same_row joins the previous row")
}

func TestWarmupSender_ButtonRowsAfterHiddenButton(t *testing.T) {
	t.Parallel()
	sender, bot, _ := newWarmupSenderFixture()
	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{
		HTML: "x",
		Buttons: []service.WarmupButton{
			{Text: "Без URL", Kind: service.WarmupButtonURL, Value: ""},
			{Text: "Тарифы", Kind: service.WarmupButtonCallback, Value: "buy_premium_list", SameRow: true},
		},
	})
	require.NoError(t, err)
	text := bot.calls()[0].(tgbotapi.MessageConfig)
	assert.JSONEq(t, `{"inline_keyboard":[[{"text":"Тарифы","callback_data":"buy_premium_list"}]]}`, keyboardJSON(t, text.ReplyMarkup),
		"a row whose first button is hidden starts with the next visible one")
}

func TestWarmupSender_TextWithoutButtonsHasNoKeyboard(t *testing.T) {
	t.Parallel()
	sender, bot, _ := newWarmupSenderFixture()
	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{
		HTML: "x", Buttons: []service.WarmupButton{{Text: "Пробник", Kind: service.WarmupButtonURL}},
	})
	require.NoError(t, err)
	text := bot.calls()[0].(tgbotapi.MessageConfig)
	assert.Nil(t, text.ReplyMarkup, "only URL-less buttons: no keyboard at all")
}

func TestWarmupSender_RejectsCallbackOutsideWhitelist(t *testing.T) {
	t.Parallel()
	sender, bot, _ := newWarmupSenderFixture()
	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{
		HTML: "x", Buttons: []service.WarmupButton{{Text: "admin", Kind: service.WarmupButtonCallback, Value: "admin_stats"}},
	})
	require.Error(t, err)
	assert.Equal(t, service.WarmupSendPermanent, service.WarmupSendErrorKindOf(err))
	assert.Empty(t, bot.calls(), "nothing is sent")
}

func TestWarmupSender_PhotoWithValidFileID(t *testing.T) {
	t.Parallel()
	sender, bot, images := newWarmupSenderFixture()
	images.images[7] = database.WarmupImage{ID: 7, Mime: "image/png", Data: []byte("png"), TGFileID: "CACHED"}
	imageID := uint(7)

	id, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{
		HTML: "<b>caption</b>", ImageID: &imageID,
		Buttons: []service.WarmupButton{{Text: "Подписка", Kind: service.WarmupButtonCallback, Value: "menu_subscription"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 101, id)
	calls := bot.calls()
	require.Len(t, calls, 1, "a working file_id is sent once, without an upload")
	photo, ok := calls[0].(tgbotapi.PhotoConfig)
	require.True(t, ok)
	assert.Equal(t, tgbotapi.FileID("CACHED"), photo.File)
	assert.Equal(t, "<b>caption</b>", photo.Caption)
	assert.Equal(t, tgbotapi.ModeHTML, photo.ParseMode)
	assert.JSONEq(t, `{"inline_keyboard":[[{"text":"Подписка","callback_data":"menu_subscription"}]]}`, keyboardJSON(t, photo.ReplyMarkup))
	assert.Empty(t, images.fileIDs, "the cached file_id is kept")
}

func TestWarmupSender_PhotoWithRejectedFileIDReuploadsBlob(t *testing.T) {
	t.Parallel()
	uploaded := tgbotapi.Message{MessageID: 555, Photo: []tgbotapi.PhotoSize{
		{FileID: "SMALL", Width: 90, Height: 90}, {FileID: "BIG", Width: 800, Height: 800}, {FileID: "MID", Width: 320, Height: 320},
	}}
	sender, bot, images := newWarmupSenderFixture(
		warmupFakeResult{err: &tgbotapi.Error{Code: 400, Message: "Bad Request: wrong file identifier/HTTP URL specified"}},
		warmupFakeResult{msg: uploaded},
	)
	images.images[7] = database.WarmupImage{ID: 7, Mime: "image/png", Data: []byte("png-bytes"), TGFileID: "EXPIRED"}
	imageID := uint(7)

	id, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{HTML: "c", ImageID: &imageID})
	require.NoError(t, err)
	assert.Equal(t, 555, id)
	calls := bot.calls()
	require.Len(t, calls, 2)
	assert.Equal(t, tgbotapi.FileID("EXPIRED"), calls[0].(tgbotapi.PhotoConfig).File)
	upload, ok := calls[1].(tgbotapi.PhotoConfig).File.(tgbotapi.FileBytes)
	require.True(t, ok, "the second attempt uploads the bytes stored in the database")
	assert.Equal(t, []byte("png-bytes"), upload.Bytes)
	assert.Equal(t, "warmup.png", upload.Name)
	assert.Equal(t, []string{"", "BIG"}, images.fileIDs, "the stale id is forgotten, the largest new one cached")
}

func TestWarmupSender_PhotoWithoutFileIDUploadsAndCaches(t *testing.T) {
	t.Parallel()
	sender, bot, images := newWarmupSenderFixture(warmupFakeResult{msg: tgbotapi.Message{MessageID: 9, Photo: []tgbotapi.PhotoSize{{FileID: "NEW", Width: 10, Height: 10}}}})
	images.images[3] = database.WarmupImage{ID: 3, Mime: "image/jpeg", Data: []byte("jpg")}
	imageID := uint(3)

	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{HTML: "c", ImageID: &imageID})
	require.NoError(t, err)
	require.Len(t, bot.calls(), 1)
	assert.Equal(t, "warmup.jpg", bot.calls()[0].(tgbotapi.PhotoConfig).File.(tgbotapi.FileBytes).Name)
	assert.Equal(t, []string{"NEW"}, images.fileIDs)
}

func TestWarmupSender_PhotoFloodDoesNotReupload(t *testing.T) {
	t.Parallel()
	sender, bot, images := newWarmupSenderFixture(warmupFakeResult{err: &tgbotapi.Error{Code: 429, Message: "Too Many Requests: retry after 7",
		ResponseParameters: tgbotapi.ResponseParameters{RetryAfter: 7}}})
	images.images[7] = database.WarmupImage{ID: 7, Mime: "image/png", Data: []byte("png"), TGFileID: "CACHED"}
	imageID := uint(7)

	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{HTML: "c", ImageID: &imageID})
	require.Error(t, err)
	var sendErr *service.WarmupSendError
	require.ErrorAs(t, err, &sendErr)
	assert.Equal(t, service.WarmupSendFlood, sendErr.Kind)
	assert.Equal(t, 7*time.Second, sendErr.RetryAfter)
	assert.Len(t, bot.calls(), 1, "a flood error is not a stale file_id")
	assert.Empty(t, images.fileIDs)
}

func TestWarmupSender_MissingImageIsTransient(t *testing.T) {
	t.Parallel()
	sender, bot, _ := newWarmupSenderFixture()
	imageID := uint(99)
	_, err := sender.SendWarmup(context.Background(), 42, &service.WarmupMessage{HTML: "c", ImageID: &imageID})
	require.Error(t, err)
	assert.Equal(t, service.WarmupSendTransient, service.WarmupSendErrorKindOf(err))
	assert.Empty(t, bot.calls())
}

func TestClassifyWarmupSendError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := map[string]struct {
		err  error
		kind service.WarmupSendErrorKind
	}{
		"429":         {&tgbotapi.Error{Code: 429, Message: "Too Many Requests: retry after 3", ResponseParameters: tgbotapi.ResponseParameters{RetryAfter: 3}}, service.WarmupSendFlood},
		"blocked":     {&tgbotapi.Error{Code: 403, Message: "Forbidden: bot was blocked by the user"}, service.WarmupSendBlocked},
		"deactivated": {&tgbotapi.Error{Code: 403, Message: "Forbidden: user is deactivated"}, service.WarmupSendUnreachable},
		"chat":        {&tgbotapi.Error{Code: 400, Message: "Bad Request: chat not found"}, service.WarmupSendUnreachable},
		"entities":    {&tgbotapi.Error{Code: 400, Message: "Bad Request: can't parse entities"}, service.WarmupSendPermanent},
		"button url":  {&tgbotapi.Error{Code: 400, Message: "Bad Request: BUTTON_URL_INVALID"}, service.WarmupSendPermanent},
		"5xx":         {&tgbotapi.Error{Code: 502, Message: "Bad Gateway"}, service.WarmupSendTransient},
		"network":     {errors.New("Post \"https://api.telegram.org\": dial tcp: i/o timeout"), service.WarmupSendTransient},
	}
	for name, tc := range cases {
		err := classifyWarmupSendError(ctx, tc.err)
		var sendErr *service.WarmupSendError
		require.ErrorAs(t, err, &sendErr, name)
		assert.Equal(t, tc.kind, sendErr.Kind, name)
		assert.ErrorIs(t, err, tc.err, name)
		if tc.kind == service.WarmupSendFlood {
			assert.Equal(t, 3*time.Second, sendErr.RetryAfter)
		} else {
			assert.Zero(t, sendErr.RetryAfter, name)
		}
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	assert.ErrorIs(t, classifyWarmupSendError(cancelled, context.Canceled), context.Canceled)
	var sendErr *service.WarmupSendError
	assert.False(t, errors.As(classifyWarmupSendError(cancelled, context.Canceled), &sendErr), "shutdown is not a send failure")
}

func TestIsWarmupFileIDError(t *testing.T) {
	t.Parallel()
	assert.True(t, isWarmupFileIDError(&tgbotapi.Error{Code: 400, Message: "Bad Request: wrong file identifier/HTTP URL specified"}))
	assert.True(t, isWarmupFileIDError(&tgbotapi.Error{Code: 400, Message: "Bad Request: wrong remote file identifier specified: Wrong padding in the string"}))
	assert.True(t, isWarmupFileIDError(errors.New("Bad Request: FILE_REFERENCE_EXPIRED")))
	assert.False(t, isWarmupFileIDError(&tgbotapi.Error{Code: 429, Message: "Too Many Requests"}))
	assert.False(t, isWarmupFileIDError(&tgbotapi.Error{Code: 400, Message: "Bad Request: can't parse entities"}))
	assert.False(t, isWarmupFileIDError(errors.New("dial tcp: file identifier")))
}
