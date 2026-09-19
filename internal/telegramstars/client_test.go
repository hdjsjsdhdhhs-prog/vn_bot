package telegramstars

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	logger.Log = zap.NewNop()
	os.Exit(m.Run())
}

func testAPI(t *testing.T, handler http.HandlerFunc) *tgbotapi.BotAPI {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true}}`)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	api, err := tgbotapi.NewBotAPIWithClient("private-test-token", server.URL+"/bot%s/%s", server.Client())
	require.NoError(t, err)
	return api
}

func TestStarsClient_InvoiceAndNegativeAnswer(t *testing.T) {
	calls := 0
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NoError(t, r.ParseForm())
		if strings.HasSuffix(r.URL.Path, "/answerPreCheckoutQuery") {
			assert.Equal(t, "false", r.Form.Get("ok"))
			assert.Equal(t, "query", r.Form.Get("pre_checkout_query_id"))
			assert.NotEmpty(t, r.Form.Get("error_message"))
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		assert.Equal(t, "XTR", r.Form.Get("currency"))
		assert.Equal(t, "stars:v1:reference", r.Form.Get("payload"))
		assert.Empty(t, r.Form.Get("provider_token"))
		assert.Empty(t, r.Form.Get("subscription_period"), "one-time purchase, not recurring billing")
		var prices []tgbotapi.LabeledPrice
		require.NoError(t, json.Unmarshal([]byte(r.Form.Get("prices")), &prices))
		require.Len(t, prices, 1)
		assert.Equal(t, 75, prices[0].Amount)
		_, _ = io.WriteString(w, `{"ok":true,"result":"https://t.me/$invoice"}`)
	})
	client := New(api)
	link, err := client.CreateInvoiceLink(context.Background(), "Subscription", "stars:v1:reference", 75)
	require.NoError(t, err)
	assert.Equal(t, "https://t.me/$invoice", link)
	require.NoError(t, client.AnswerPreCheckout(context.Background(), "query", false))
	for _, amount := range []int64{0, -1, 10001} {
		_, err := client.CreateInvoiceLink(context.Background(), "Title", "payload", amount)
		require.Error(t, err)
	}
	assert.Equal(t, 2, calls)
}

func TestStarsClient_RejectsInvalidResponsesAndRedactsErrors(t *testing.T) {
	for _, response := range []string{
		`{"ok":false,"description":"private-test-token"}`,
		`{"ok":true,"result":null}`,
		`{"ok":true,"result":"http://t.me/$invoice"}`,
		`{"ok":true,"result":"https://evil.example/$invoice"}`,
		`{"ok":true,"result":"https://user:pass@t.me/$invoice"}`,
		`{"ok":true,"result":"https://t.me/not-an-invoice"}`,
		`not-json`,
	} {
		t.Run(response, func(t *testing.T) {
			api := testAPI(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, response) })
			_, err := New(api).CreateInvoiceLink(context.Background(), "Title", "payload", 1)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-test-token")
		})
	}
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request must not reach transport") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(api).CreateInvoiceLink(ctx, "Title", "payload", 1)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-test-token")
}

func TestStarsUpdates_CaptureBeforeAcknowledgmentAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	offsets := make(chan string, 4)
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		offsets <- r.Form.Get("offset")
		var allowed []string
		require.NoError(t, json.Unmarshal([]byte(r.Form.Get("allowed_updates")), &allowed))
		assert.ElementsMatch(t, []string{"message", "callback_query", "pre_checkout_query"}, allowed)
		if r.Form.Get("offset") == "11" {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":10,"message":{"successful_payment":{"currency":"XTR","total_amount":75,"invoice_payload":"stars:v1:test","telegram_payment_charge_id":"charge"}}}]}`)
	})
	attempts := make(chan int, 3)
	n := 0
	updates := Updates(ctx, api, 10, func(context.Context, tgbotapi.Update) error {
		n++
		attempts <- n
		if n == 1 {
			return errors.New("database unavailable")
		}
		return nil
	})
	select {
	case <-offsets:
	case <-time.After(5 * time.Second):
		t.Fatal("no initial poll")
	}
	select {
	case <-attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("no capture")
	}
	select {
	case <-updates:
		t.Fatal("delivered before durable capture")
	case <-offsets:
		t.Fatal("acknowledged before durable capture")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case update := <-updates:
		assert.Equal(t, 10, update.UpdateID)
	case <-time.After(5 * time.Second):
		t.Fatal("capture retry did not deliver")
	}
	select {
	case offset := <-offsets:
		assert.Equal(t, "11", offset)
	case <-time.After(5 * time.Second):
		t.Fatal("no acknowledgment after capture")
	}
	cancel()
	select {
	case _, ok := <-updates:
		assert.False(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not stop")
	}
}
