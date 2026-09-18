# API Reference — rs8kvn_bot

**Base URL:** `http://localhost:8880` (configurable via `WEB_SERVER_PORT`)

---

## Table of Contents

1. [Health Checks](#1-health-checks)
2. [Trial Landing Page](#2-trial-landing-page)
3. [Subscription Proxy](#3-subscription-proxy)
4. [Prometheus Metrics](#4-prometheus-metrics)
5. [Payment Callback](#5-payment-callback)
6. [Static Files](#6-static-files)
7. [Error Codes](#7-error-codes)
8. [Rate Limits](#8-rate-limits)
9. [cURL Examples](#9-curl-examples)
10. [Versioning](#10-versioning)
11. [Telegram Mini App Authentication](#11-telegram-mini-app-authentication)

---

## 1. Health Checks

### `GET /healthz`

Liveness probe — returns overall service health, aggregating registered component checkers (`database`).

**Response 200 OK** (all components healthy):
```json
{
  "status": "ok",
  "components": {
    "database": {"status": "ok"}
  },
  "timestamp": "2026-07-02T05:30:00Z",
  "uptime": "4h32m11s"
}
```

**Response 503 Service Unavailable** (a component is down):
```json
{
  "status": "down",
  "components": {
    "database": {"status": "down", "message": "connection refused"}
  },
  "timestamp": "2026-07-02T05:30:00Z",
  "uptime": "4h32m11s"
}
```

> `status` is `ok` only when every component is `ok`; it becomes `down` if any component is `down`.

---

## 2. Trial Landing Page

### `GET /i/{code}`

Trial invitation page. Validates invite code, applies IP rate limit, creates trial subscription, and renders the Happ/Telegram activation page.

**Path Parameters:** `code` — alphanumeric invite code with `_`/`-`.

**Response 200:** HTML page with Happ download links, subscription URL, and Telegram activation link.

**Response 404:** Invite code not found or invalid.

**Response 429:** IP rate limit exceeded.

**Response 500:** VPN node or database failure.

---

## 3. Subscription Proxy

### `GET /sub/{subID}`

Returns the merged subscription configuration from all active nodes. Responses are cached for 240 seconds; inactive/expired subscriptions return 404.

**Response 200:** Subscription body and aggregated `Subscription-Userinfo` headers.

**Response 404:** Subscription not found, inactive, or expired.

**Response 503:** Subserver not initialized.

**Response 405:** Non-GET request.

---

### `GET /connect/{token}` — customer connection page

Server-rendered mobile-first HTML (`internal/web/templates/connect.html`, embedded in the Go binary). Uses the same bearer-token lookup and customer-safe presentation as `GET /subscription-info/{token}`; no Telegram authentication is required.

- Shows subscription status, expiration in UTC (or no expiration), the public subscription URL, an embedded QR image, and a local copy-link button.
- The URL comes only from `Config.SubURL(token)` (`GLOBAL_SUB_URL`), not from a ProviderSource. The existing `utils.GenerateQRCodePNG` encodes exactly that URL on the server. No client deep links or third-party QR services are used.
- Copy uses the browser Clipboard API, with local selection/`execCommand` fallback and manual-copy guidance when clipboard access is blocked. The page remains readable without JavaScript.
- Existing expired, revoked, paused and canceled subscriptions return **200** with their inactive state. An `active` row whose expiry has passed is presented as expired, matching subscription-serving eligibility; this also applies to `/subscription-info/{token}`. Presentation does not mutate the row.
- Malformed, unknown and mismatched tokens return the same safe **404** page. Non-GET methods return **405** (`Allow: GET`); infrastructure/rendering failures return a generic **500**.
- Responses (including errors and connection-path canonicalization redirects) use `Cache-Control: no-store`. The server sets `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex, nofollow` and existing browser security headers. Metrics normalize connection paths to `/connect/:token`, including canonicalization redirects.
- No provider URL, HWID, upstream User-Agent/headers, credentials or internal subscription/infrastructure IDs enter the page view model. Treat the page URL, subscription link and QR as credentials; do not share or log them. Reverse proxies must independently redact bearer paths in their access logs.

**Focused verification** (Go checks require the project's CGO/SQLite toolchain):

```bash
go test -p 1 ./internal/web ./internal/metrics ./internal/utils
go test -p 1 ./internal/service -run 'TestGetPublicSubscriptionInfo|TestFormatSubscriptionMessage'
node --test internal/web/testdata/connect_copy.test.cjs
```

The web tests include a real local HTTP listener backed by a temporary migrated SQLite database, legacy/ProviderSource fixtures, exact QR-image comparison, data-boundary checks, safe failures and metrics redaction. The optional Node check uses only built-in modules; no frontend build or npm dependencies are required.

---

## 4. Prometheus Metrics

### `GET /metrics`

Prometheus exposition endpoint. It exposes application, bot, cache, subserver, database, subscription, and payment metrics. Requests to `/metrics` itself and `/static/*` are deliberately excluded from `http_requests_total` and `http_request_duration_seconds`, so scrapes and asset 404s do not pollute application traffic metrics.

---

## 5. Payment Callback

### `POST /payment/callback`

Receives Platega transaction status callbacks. Requests must include non-empty `X-MerchantId` and `X-Secret` headers matching the configured credentials. The body is limited to **256 KiB**, decoded with fixed-point JSON numbers, and must contain exactly one JSON document.

Required JSON fields:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440111",
  "amount": 230.00,
  "currency": "RUB",
  "status": "CONFIRMED"
}
```

`id` is a provider transaction UUID. The JSON value is validated at the webhook boundary; the universal `orders.provider_payment_id` database column remains text for compatibility with historical IDs from other providers. `paymentMethod` и `payload` принимаются при наличии. Официальные страницы Platega противоречат друг другу по обязательности этих полей; интеграция сохраняет совместимость. Поддерживаются `PENDING`, `CANCELED`, `CONFIRMED` и `CHARGEBACKED`; неизвестные статусы подтверждаются без изменения заказа и отправляют администратору alert для проверки.

**Responses:**

- `200 {"ok":true}` — callback processed, ignored as a terminal-order no-op (including duplicate or late `CONFIRMED` callbacks), unknown provider ID, or manual-review event;
- `400` — malformed payload, invalid UUID/amount, invalid currency, or callback amount below the stored order amount; a larger callback amount is accepted because Platega may include its payment-method commission;
- `401` — invalid or missing provider credentials;
- `405` — method other than POST (`Allow: POST`);
- `503` — payments disabled, order service/bot not wired, or runtime payment readiness has not been enabled after real bot and SyncService initialization;
- `500` — temporary database/transaction failure. The provider should retry these callbacks.

`CONFIRMED` atomically changes `pending → paid`, updates the subscription and creates DB sync prerequisites in one transaction. Post-commit VPN sync and Telegram delivery are best-effort and do not roll back the payment. Only the callback that actually activated the order (`Activated=true`) sends the success notifications: the user gets a Markdown-formatted Premium welcome message with the benefits (unlimited traffic, more servers/options, experimental features and priority support), subscription details, expiry and link; the admin gets a Markdown alert (`notifyAdminPaid`) with tariff, formatted amount and a clickable buyer link (`utils.FormatUserLink` — `t.me/username` or `tg://user?id=…`). The title distinguishes a first purchase (`🆕 Покупка подтверждена`) from a renewal of an already-paid subscription (`🔄 Продление подтверждено`, detected from `PricePaidCents`/`ProductID` before the CAS mutates the subscription). Duplicate `CONFIRMED` callbacks are idempotent no-ops and send nothing. `CANCELED` cancels only pending orders. `CHARGEBACKED` cancels pending/paid orders; a chargeback on a previously-paid order automatically downgrades the subscription to the free plan (unless another paid order for the same subscription exists — access is then preserved), and when money was actually collected (`WasPaid=true`) sends a single `notifyAdminChargeback` Markdown alert with tariff, amount, buyer link and access status (downgraded to free vs preserved). Infrastructure-level failures (DB outage, mismatches, late/unknown callbacks, provider errors) continue to flow through `NotifyPaymentIssue` → `notifyAdmin` for ops visibility.

The payment link lifetime is taken from Platega `expiresIn` and stored as an absolute local UTC `payment_expires_at`. A still-valid saved link is reused. After local expiry, the pending order is terminalized as `expired`; its provider ID and URL are retained and the next request creates a new pending order. If the provider request has an uncertain outcome, automatic retry is blocked and the administrator receives a reconciliation alert. A late `CONFIRMED` for an already `expired`/`canceled` order never activates it: the money is flagged for manual review (refund or manual activation) and the admin gets a `late_confirmed_callback` alert.

---

## 6. Static Files

### `GET /static/logo.png`

Returns the embedded 512×512 PNG logo. Also responds to `HEAD`.

---

## 7. Error Codes

| Code | HTTP Status | Description |
|------|------------|-------------|
| `INVITE_NOT_FOUND` | 404 | Invite code invalid |
| `RATE_LIMIT_EXCEEDED` | 429 | Too many trial requests from IP |
| `SUBSCRIPTION_NOT_FOUND` | 404 | Subscription not found/inactive/expired |
| `TRIAL_CREATION_FAILED` | 500 | VPN node failed to create trial |
| `DATABASE_ERROR` | 500 | Database query failed |
| `INTERNAL_ERROR` | 500 | Unexpected system failure |

---

## 8. Rate Limits

| Endpoint | Limit | Enforcement |
|----------|-------|-------------|
| `/i/{code}` (trial) | 3 requests/hour per IP | Database counter |
| `/sub/{subID}` | None | 240-second response cache |
| `/metrics` | None | Prometheus scrape interval |
| Telegram bot commands | 30 tokens/user, 5/sec refill | In-memory token bucket |

---

## 9. cURL Examples

**Health check:**
```bash
curl -s http://localhost:8880/healthz | jq
```

**Trial page:**
```bash
curl -i http://localhost:8880/i/ABC123def456
```

**Payment callback:**
```bash
curl -i -X POST http://localhost:8880/payment/callback \
  -H 'X-MerchantId: <merchant-id>' \
  -H 'X-Secret: <secret>' \
  -H 'Content-Type: application/json' \
  -d '{"id":"550e8400-e29b-41d4-a716-446655440111","amount":230.00,"currency":"RUB","status":"CONFIRMED"}'
```

---

## 10. Versioning

API version is implicit in endpoint paths. Bot version in logs: `rs8kvn_bot@<version>`.

---

## 11. Telegram Mini App Authentication

### `GET /api/miniapp/subscription`

Read-only authenticated view of the current Telegram user's subscription. Registered by `web.Server.Start` in the existing application; uses the same `SubscriptionService` through `SubscriptionManagement.Current`, with no additional user table, subscription model, cache or lifecycle logic. Response shape and eligibility semantics: [Subscription Management contract](subscription-management.md).

Send exactly one header:

```http
Authorization: tma <Telegram.WebApp.initData>
```

Use the original URL-encoded `initData` string, not `initDataUnsafe`, a decoded/re-encoded JSON object, a bot token or a public subscription token. Credentials in query parameters, cookies, request bodies or alternate identity headers do not authenticate. Unsigned IDs and target tokens never select a customer. `tma` is case-insensitive.

### Validation and lifetime

- HMAC-SHA256 verification uses the existing configured `TELEGRAM_BOT_TOKEN`, without calling Telegram. The derived key is HMAC-SHA256 with key `WebAppData` and message bot token; the data-check-string contains sorted decoded field names and values, excluding only `hash`. `signature`, when present, remains signed by this HMAC procedure.
- Signature comparison is constant-time. Missing, malformed, ambiguous/duplicate fields, invalid user JSON/IDs, wrong-bot signatures and oversized data are rejected. Maximum raw initData size: **16 KiB**. Identity comes exclusively from verified `user.id` (positive integer, at most 52 bits), not chat, receiver, username or request-supplied IDs.
- `auth_date` must be no older than **5 minutes**, with at most **30 seconds** future clock skew, inclusive at Unix-second precision. Every request is validated; there are no cookies, server sessions, refresh tokens or sliding expiry. Reopen the Mini App to obtain fresh initData after expiration.
- A captured credential can be replayed within this fixed lifetime; this read-only milestone does not implement single-use nonces. Keep production traffic on HTTPS, synchronize the server clock, and never log initData, Authorization, returned URLs or tokens. TLS/proxy configuration is not changed by this milestone.

### Responses and authority

| HTTP | JSON error / result |
| --- | --- |
| 200 | Existing customer-safe Subscription Management representation |
| 401 | `{"error":"unauthorized"}`; missing/invalid/expired auth, with `WWW-Authenticate: tma realm="miniapp"` |
| 403 | `{"error":"forbidden"}`; management authorization/ownership rejected |
| 404 | `{"error":"subscription_not_found"}`; authenticated customer has no subscription |
| 404 | `{"error":"not_found"}`; authenticated request to an unknown Mini App route |
| 405 | `{"error":"method_not_allowed"}`, `Allow: GET`; authenticated non-GET request |
| 503 | `{"error":"service_unavailable"}`; bot configuration, service or database unavailable |

Authentication precedes route/method dispatch (including OPTIONS). No permissive CORS or cookie authentication is enabled. Missing credentials return 401 even if bot configuration is missing; a `tma` request with missing bot configuration returns 503. Standard ServeMux path-canonicalization redirects can occur before authentication and disclose no subscription data.

Existing expired/revoked/paused/canceled subscriptions remain readable; effective expiry, customer links and renewal eligibility come from Subscription Management. Reads never create, revive, renew or provision access. `renewal.eligible` is lifecycle information, **not grant authority**: there is no renewal route and the Mini App policy denies all grants, even for the bot administrator.

Responses and namespace canonicalization redirects use `Cache-Control: no-store`, `X-Robots-Tag: noindex, nofollow` and existing security headers. Errors do not expose parser/DB details. Metrics collapse the namespace to `/api/miniapp/*`, including unknown paths and canonicalization redirects. Reverse-proxy logs must independently exclude/redact Authorization, query credentials and sensitive paths. Existing `/connect/`, `/subscription-info/`, `/sub/`, health and callback routes retain their own policies.

### Verification

Run sequentially with CGO/SQLite enabled:

```bash
go test -p 1 ./internal/telegramauth -count=1 -timeout=120s
go test -p 1 ./internal/web -run '^TestMiniApp' -count=1 -timeout=120s
go test -p 1 ./internal/web ./internal/metrics -count=1 -timeout=120s
go test -p 1 ./internal/database ./internal/service -run 'TestSubscriptionManagement|TestSubscriptionRenewal|TestGetPublicSubscriptionInfo' -count=1 -timeout=120s
```

Mini App tests exercise the actual HTTP listener with a migrated SQLite database, two customers (legacy and ProviderSource-backed), the configured admin, forged identities, denied writes, fresh lifecycle state, missing subscriptions, safe failures, headers, logs and metrics. Validator tests include a fixed independent HMAC vector, malformed signed data, time boundaries and fuzz seeds.

---

*This reference is maintained against the current codebase.*
