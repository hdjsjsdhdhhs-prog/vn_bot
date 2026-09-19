# Telegram Stars backend

## Scope and configuration

Stars is a one-time payment adapter over Purchase Foundation, not another order
or subscription system. The existing bot token/Telegram initData authentication
is reused; no payment-provider credential is needed. `PaymentEnabled` controls
legacy payments only. Stars becomes available after the real Bot API client and
subscription sync service are wired in `cmd/bot/main.go`.

Provision an explicit active product on an active paid plan with currency `XTR`
and a positive integer price (currently 1–10000 Stars). Existing names
`price_cents` / `amount_cents` are retained for schema/API compatibility: for
`XTR` the integer is the number of Stars, **not hundredths of a Star**. Do not
convert or relabel existing fiat offers. Existing product guards freeze price,
currency, duration and plan once referenced by an order.

Purchase Foundation requires an existing active/expired Telegram-owned
subscription. Its onboarding/SubscriptionManagement remains responsible for
creating the initial subscription; payment activates/upgrades/renews that same
row. ProviderSource subscriptions can purchase only their current plan and keep
the same source. No source credentials enter payment payloads or responses.

## Boundary

1. Authenticate every Mini App request with `Authorization: tma <initData>`.
2. Use existing `GET /api/miniapp/offers` and `POST /api/miniapp/orders` with
   `{"offer_id":"..."}` and UUID-v4 `Idempotency-Key`.
3. `POST /api/miniapp/orders/{order_id}/invoice` accepts **no body or query**.
   Response: `{"invoice_url":"https://t.me/$..."}`. Only the authenticated owner
   may request it. Non-XTR purchases cannot generate a Stars invoice.
4. `createInvoiceLink` sends one XTR price and an empty provider token. Payload
   `stars:v1:<purchase reference>` is an identifier, never a bearer credential.
5. Bot polling includes `pre_checkout_query`. Approval validates owner/payer,
   amount, currency, pending state, purchase deadline, offer and source policy.
   It reserves one query ID; it does not grant access. Replaying that query is
   safe, while a different query for the same purchase is rejected. After a
   failed/abandoned reserved checkout, wait for intent expiry and create a new
   purchase rather than authorizing another possible charge.
6. Only trusted Telegram `successful_payment` updates may settle. There is no
   browser settlement endpoint. Payment handling bypasses normal bot commands,
   rate limiting and broadcast input handling.

## Settlement and recovery

Migration 043 adds nullable checkout fields and `telegram_payment_receipts`.
Polling persists a successful payment receipt **before** advancing getUpdates
acknowledgment. Infrastructure errors retain the update for retry. Permanently
malformed updates are rejected without wedging the entire bot. Conflicting
charge replays never overwrite the original receipt and fail strict settlement.

The existing order CAS, charge binding, subscription entitlement, any plan-change
DB prerequisites and receipt completion commit in one SQLite writer transaction.
Duplicate/concurrent payment delivery does not extend access twice or create a
second subscription. VPN API synchronization after commit remains best-effort;
the existing node sync worker retries it.

The Stars worker retries unsettled receipts at startup and every minute with
keyset pagination. A failed receipt does not starve later pages. A previously
approved, actually paid checkout can settle after intent expiry or catalog
deactivation: expiry blocks new charges, not delivery of an already paid
entitlement. Canceled purchases, revoked subscriptions, changed ownership and
invalid ProviderSource policy fail closed and remain for operator reconciliation.
No automatic refunds or new external payment integrations are provided.

## Focused verification on Windows

`tests\run-stars-tests.cmd` sets `CGO_ENABLED=1`,
`CC=C:\msys64\ucrt64\bin\gcc.exe`, prepends its bin directory to `PATH`, and runs
`go test -p 1` with the supplied arguments. Run packages sequentially, for example:

```bat
tests\run-stars-tests.cmd ./internal/telegramstars -count=1
tests\run-stars-tests.cmd ./internal/service -run "Stars|Purchase|Order|Payment|SubscriptionManagement|Renew|ProviderSource" -count=1
tests\run-stars-tests.cmd ./internal/database -run "Stars|Purchase|Payment|Order|Product|Renew|ProviderSource" -count=1
tests\run-stars-tests.cmd ./internal/web -run "MiniApp|Payment|Subscription" -count=1
tests\run-stars-tests.cmd ./internal/bot -run "Stars|Payment|BuyPremium|Navigation_OpenAndBack" -count=1
```

Tests use real migrations/SQLite, signed initData, the pinned Telegram update
structures and a local HTTP Bot API server. They do not spend real Stars or
claim verification against the live Telegram service.
