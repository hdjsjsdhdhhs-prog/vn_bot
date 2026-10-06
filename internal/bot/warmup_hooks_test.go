package bot

import (
	"context"
	"errors"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests: warm-up buttons pressed on a photo message, and the
// /start contact hook.

// recordingSends captures every Chattable the handler sends through the
// testutil bot (SendFunc runs under the mock's lock, so it records here).
type recordingSends struct {
	mu     sync.Mutex
	calls  []tgbotapi.Chattable
	nextID int
	fail   func(tgbotapi.Chattable) error
}

func (r *recordingSends) send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	if r.fail != nil {
		err := r.fail(c)
		if err != nil {
			return tgbotapi.Message{}, err
		}
	}
	r.nextID++
	return tgbotapi.Message{MessageID: r.nextID}, nil
}

func (r *recordingSends) all() []tgbotapi.Chattable {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tgbotapi.Chattable(nil), r.calls...)
}

// editedIDs returns the message IDs of every editMessageText.
func (r *recordingSends) editedIDs() []int {
	ids := []int{}
	for _, c := range r.all() {
		if e, ok := c.(tgbotapi.EditMessageTextConfig); ok {
			ids = append(ids, e.MessageID)
		}
	}
	return ids
}

func tapMessage(h *Handler, data string, msg *tgbotapi.Message) {
	_ = h.HandleCallback(context.Background(), tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{ID: "cb", From: &tgbotapi.User{ID: msg.Chat.ID, UserName: "u"}, Data: data, Message: msg},
	})
}

func warmupPhotoMessage(chatID int64, id int) *tgbotapi.Message {
	return &tgbotapi.Message{
		MessageID: id, Chat: &tgbotapi.Chat{ID: chatID, Type: "private"}, Caption: "<b>Скидка</b>",
		Photo: []tgbotapi.PhotoSize{{FileID: "P", Width: 100, Height: 100}},
	}
}

func TestWarmupCallback_FromPhotoMessageEditsAFreshTextMessage(t *testing.T) {
	const photoID = 900
	for _, data := range []string{"menu_subscription", "buy_premium_list", "back_to_start", "menu_help", "menu_donate"} {
		t.Run(data, func(t *testing.T) {
			handler, bot := newQRTestHandler(t)
			rec := &recordingSends{nextID: 1000}
			bot.SendFunc = rec.send

			tapMessage(handler, data, warmupPhotoMessage(42, photoID))

			calls := rec.all()
			require.NotEmpty(t, calls)
			placeholder, ok := calls[0].(tgbotapi.MessageConfig)
			require.True(t, ok, "a new text message is sent first, got %T", calls[0])
			assert.Equal(t, warmupLoadingText, placeholder.Text)
			assert.Equal(t, int64(42), placeholder.ChatID)

			edited := rec.editedIDs()
			require.NotEmpty(t, edited, "the handler renders its screen by editing")
			for _, id := range edited {
				assert.Equal(t, 1001, id, "only the fresh text message is edited")
				assert.NotEqual(t, photoID, id, "editMessageText never targets the photo")
			}
			for _, c := range calls {
				_, isCaption := c.(tgbotapi.EditMessageCaptionConfig)
				assert.False(t, isCaption, "the warm-up caption is not overwritten")
			}
			assert.NotContains(t, bot.DeletedMessageIDsSafe(), photoID, "the warm-up photo stays")
			assert.True(t, bot.RequestCalledSafe(), "the callback is answered")
		})
	}
}

func TestWarmupCallback_FromTextMessageEditsItInPlace(t *testing.T) {
	handler, bot := newQRTestHandler(t)
	rec := &recordingSends{nextID: 1000}
	bot.SendFunc = rec.send

	tapMessage(handler, "menu_subscription", &tgbotapi.Message{MessageID: 77, Chat: &tgbotapi.Chat{ID: 42, Type: "private"}, Text: "warm-up text"})

	for _, c := range rec.all() {
		if m, ok := c.(tgbotapi.MessageConfig); ok {
			assert.NotEqual(t, warmupLoadingText, m.Text, "a text message needs no placeholder")
		}
	}
	edited := rec.editedIDs()
	require.NotEmpty(t, edited)
	for _, id := range edited {
		assert.Equal(t, 77, id, "existing behaviour: the pressed text message is edited")
	}
}

func TestWarmupCallback_QRPhotoBackKeepsDeleteContract(t *testing.T) {
	handler, bot := newQRTestHandler(t)
	rec := &recordingSends{nextID: 1000}
	bot.SendFunc = rec.send

	// back_to_subscription lives on the QR photo; it is not a warm-up action.
	tapMessage(handler, "back_to_subscription", warmupPhotoMessage(42, 555))

	assert.Equal(t, []int{555}, bot.DeletedMessageIDsSafe(), "Back still deletes exactly the QR photo")
	for _, c := range rec.all() {
		if m, ok := c.(tgbotapi.MessageConfig); ok {
			assert.NotEqual(t, warmupLoadingText, m.Text)
		}
	}
}

func TestWarmupCallback_PlaceholderFailureSkipsHandler(t *testing.T) {
	handler, bot := newQRTestHandler(t)
	rec := &recordingSends{nextID: 1000, fail: func(c tgbotapi.Chattable) error {
		if _, ok := c.(tgbotapi.MessageConfig); ok {
			return errors.New("network down")
		}
		return nil
	}}
	bot.SendFunc = rec.send

	tapMessage(handler, "menu_subscription", warmupPhotoMessage(42, 900))

	assert.Empty(t, rec.editedIDs(), "without a text message to edit nothing is edited")
	assert.True(t, bot.RequestCalledSafe(), "the callback is still answered (no endless spinner)")
}

func TestDetachWarmupMediaCallback_LeavesOtherUpdatesUntouched(t *testing.T) {
	handler, bot := newQRTestHandler(t)
	update := tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{Data: "menu_subscription",
		Message: &tgbotapi.Message{MessageID: 5, Chat: &tgbotapi.Chat{ID: 42}, Text: "t"}}}
	out, ok := handler.detachWarmupMediaCallback(update)
	require.True(t, ok)
	assert.Same(t, update.CallbackQuery, out.CallbackQuery)
	assert.False(t, bot.SendCalledSafe())

	photo := warmupPhotoMessage(42, 6)
	update = tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{Data: "menu_subscription", Message: photo}}
	out, ok = handler.detachWarmupMediaCallback(update)
	require.True(t, ok)
	assert.Equal(t, 1, out.CallbackQuery.Message.MessageID, "re-targeted to the placeholder")
	assert.Empty(t, out.CallbackQuery.Message.Photo)
	assert.Equal(t, 6, photo.MessageID, "the original update is not mutated")
	assert.NotEmpty(t, photo.Photo)
}

type fakeWarmupContacts struct {
	mu    sync.Mutex
	calls [][3]any
	err   error
}

func (f *fakeWarmupContacts) TouchContact(_ context.Context, telegramID int64, username, languageCode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, [3]any{telegramID, username, languageCode})
	return f.err
}

func startUpdate(chatType, text string) tgbotapi.Update {
	return tgbotapi.Update{Message: &tgbotapi.Message{
		MessageID: 1, Text: text, Chat: &tgbotapi.Chat{ID: 42, Type: chatType},
		From:     &tgbotapi.User{ID: 42, UserName: "alice", LanguageCode: "ru"},
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
	}}
}

func TestHandleStart_RecordsWarmupContact(t *testing.T) {
	handler, _ := newQRTestHandler(t)
	contacts := &fakeWarmupContacts{}
	handler.SetWarmupContacts(contacts)

	require.NoError(t, handler.HandleStart(context.Background(), startUpdate("private", "/start")))
	require.Len(t, contacts.calls, 1)
	assert.Equal(t, [3]any{int64(42), "alice", "ru"}, contacts.calls[0])

	// Group chats are not warm-up contacts.
	require.NoError(t, handler.HandleStart(context.Background(), startUpdate("group", "/start")))
	assert.Len(t, contacts.calls, 1)

	// A failing recorder never breaks /start.
	contacts.err = errors.New("db down")
	require.NoError(t, handler.HandleStart(context.Background(), startUpdate("private", "/start")))
	assert.Len(t, contacts.calls, 2)
}

func TestHandleStart_WithoutWarmupIsUnchanged(t *testing.T) {
	handler, bot := newQRTestHandler(t)
	require.NoError(t, handler.HandleStart(context.Background(), startUpdate("private", "/start")))
	assert.True(t, bot.SendCalledSafe(), "the main menu is still sent")
}
