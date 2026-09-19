// Package telegramstars provides the Stars methods missing from the pinned
// Telegram SDK, using that SDK's existing Bot API transport and credentials.
package telegramstars

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Client struct{ bot *tgbotapi.BotAPI }

func New(bot *tgbotapi.BotAPI) *Client { return &Client{bot: bot} }

type contextClient struct {
	ctx    context.Context
	client tgbotapi.HTTPClient
}

func (c contextClient) Do(r *http.Request) (*http.Response, error) {
	return c.client.Do(r.WithContext(c.ctx))
}

func (c *Client) request(ctx context.Context, method string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
	if c == nil || c.bot == nil || c.bot.Client == nil {
		return nil, errors.New("Telegram Stars client unavailable")
	}
	// Request-scoped SDK copy: no mutation of the shared bot/long-polling client.
	api := *c.bot
	api.Debug = false // request params and token must never enter SDK debug logs
	api.Client = contextClient{ctx, c.bot.Client}
	response, err := api.MakeRequest(method, params)
	if err != nil || response == nil || !response.Ok {
		// net/http errors can include the bot token URL; do not propagate them.
		return nil, errors.New("Telegram Stars API request failed")
	}
	return response, nil
}

func (c *Client) CreateInvoiceLink(ctx context.Context, title, payload string, amount int64) (string, error) {
	if amount <= 0 || amount > 10000 {
		return "", errors.New("invalid Stars amount")
	}
	prices, err := json.Marshal([]tgbotapi.LabeledPrice{{Label: "Subscription", Amount: int(amount)}})
	if err != nil {
		return "", err
	}
	response, err := c.request(ctx, "createInvoiceLink", tgbotapi.Params{
		"title": title, "description": "Access for the period specified in your purchase offer.",
		"payload": payload, "currency": "XTR", "provider_token": "", "prices": string(prices),
	})
	if err != nil {
		return "", err
	}
	var link string
	if err := json.Unmarshal(response.Result, &link); err != nil {
		return "", errors.New("invalid Telegram invoice response")
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "t.me" || parsed.User != nil || !strings.HasPrefix(parsed.Path, "/$") {
		return "", errors.New("invalid Telegram invoice link")
	}
	return link, nil
}

func (c *Client) AnswerPreCheckout(ctx context.Context, queryID string, approved bool) error {
	params := tgbotapi.Params{"pre_checkout_query_id": queryID, "ok": "true"}
	if !approved {
		params["ok"] = "false"
		params["error_message"] = "Покупка недоступна или уже оплачена. Обновите заказ в приложении."
	}
	_, err := c.request(ctx, "answerPreCheckoutQuery", params)
	return err
}
