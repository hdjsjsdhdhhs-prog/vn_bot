# Handover Summary — rs8kvn_bot

**Repo:** https://github.com/kereal/rs8kvn_bot
**Module:** `rs8kvn_bot` (Go 1.25+)
**Branch:** `dev` (GitFlow: `main` = production, `dev` = integration, feature branches from dev or `plans_and_pricing`)

---

## Architecture

### High-Level Component Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Telegram Bot API                             │
└─────────────────────────────┬───────────────────────────────────────┘
                              │ GetUpdates (long polling)
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     cmd/bot/main.go (Entry Point)                    │
│  ┌────────────────────────────────────────────────────────────────┐ │
│  │ • Config loading                                                │ │
│  │ • Service initialization (DB, Bot, Web, Subserver)          │ │
│  │ • Graceful shutdown coordination (signal handling)              │ │
│  │ • Worker pool semaphore (10 concurrent handlers)                │ │
│  └────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────┬───────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐    ┌───────────────┐    ┌───────────────┐
│   Bot Layer   │    │  Web Layer    │    │  Background   │
│ internal/bot/ │    │  internal/web/│    │  Jobs         │
│               │    │               │    │               │
│ • Handler     │    │ • /healthz    │    │ • Backup      │
│ • Commands    │    │ • /readyz     │    │   (03:00)     │
│ • Callbacks   │    │ • /i/{code}   │    │ • Trial       │
│ • Cache       │    │ • /sub/{subID}│    │   cleanup     │
│ • RateLimit   │    │ • /metrics    │    │   (hourly)    │
└───────────────┘    └───────────────┘    └───────────────┘
        │                     │                     │
        └─────────────────────┼─────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐    ┌───────────────┐    ┌───────────────┐
│   Database    │    │ Provider src  │    │ External      │
│  SQLite DB    │    │ (read-only)   │    │ Services      │
│               │    │               │    │               │
│ • Subscriptions│   │ • Catalogue   │    │ • Telegram    │
│ • Invites      │   │ • Live fetch  │    │ • Sentry      │
│ • TrialReqs    │   │ • No writes   │    │ • Heartbeat   │
│ • Indexes      │   │ • Builders    │    │   endpoint    │
└───────────────┘    └───────────────┘    └───────────────┘
```

### Data Flow: Subscription Creation

```
User clicks "Get subscription"
        │
        ▼
Telegram Update → main.go event loop (semaphore acquire)
        │
        ▼
Handler.HandleUpdate → HandleCallback ("create_subscription")
        │
        ▼
1. Check inProgressSyncMap (prevent double-click)
2. Check cache (SubscriptionCache.Get)
        │
        ├─ Cache hit → return cached sub
        │
        └─ Cache miss → proceed
        │
        ▼
SubscriptionService.Create(ctx, telegramID, ...)
        │
        ├─ Generate UUID, SubID and the public subscription token
        ├─ Build subscription URL from GLOBAL_SUB_URL + token
        │
        ▼
Database: CreateSubscription (transaction)
        │
        ├─ Revoke old active subs for this user (UPDATE status='revoked')
        └─ INSERT new subscription
        │
        ▼
Cache: Set(telegramID, subscription)
        │
        ▼
Notify admin: "New subscription: @username"
        │
        ▼
Send message to user: subscription URL + QR code
```

There is no panel provisioning or sync step: access is the subscription row itself. The content is assembled on request (see "Subscription Proxy Flow").

### Data Flow: Trial Activation via Landing Page

```
User visits: https://vpn.site/i/ABC123DEF
        │
        ▼
GET /i/{code} Handler
        │
        ├─ Validate invite code (regex + DB lookup)
        ├─ Check cookie "rs8kvn_trial_{code}" (already used?)
        ├─ Rate limit: CountTrialRequestsByIPLastHour() < TRIAL_RATE_LIMIT
        │
        ▼ (passed)
CreateTrialSubscription(ctx, inviteCode)
        │
        ├─ Resolve the trial plan and its Builder (admin trial settings)
        │   • Expiry: now + trial duration
        │
        ▼
       Database: CreateTrialSubscription (отрицательный telegram_id до активации)
        │
        ▼
Set cookie: rs8kvn_trial_{code} = {subID}; HttpOnly; Secure; SameSite=Strict
        │
        ▼
Render trial.html with:
  • HappLink: "happ://add/{subscriptionURL}"
  • SubURL (copyable)
  • TelegramLink: "tg://resolve?domain=bot_username?start=trial_{subID}"
        │
        ▼
User clicks "Добавить в Happ" → opens Happ app with subscription
User clicks "Активировать" → opens Telegram bot, binds trial to account
```

### Subscription Proxy Flow

```
User opens subscription link in client
        │
        ▼
GET /sub/...
        │
        ├─ Validate the identifier format
        ├─ Check cache (TTL 240s)
        │
        ├─ Cache hit? → verify subscription active in DB → serve cached
        └─ Cache miss → proceed
        │
        ▼
Load the subscription (status, plan, expiry); revoked / missing / expired → 404
        │
        ▼
ResolveSubscriptionBuilder: subscription override → plan default builder
        │
        ▼
Fetch the builder's linked, enabled ProviderSources (best-effort per source):
  ├─ FetchFromProviderSource(ctx, source) — HTTP GET with server-side headers
  ├─ DetectFormat (JSON / Clash / Base64 / Plain)
  └─ Collect subscription-userinfo headers
        │
        ▼
Apply the builder rules (country/node items, renames, ordering):
  • JSON configs → share links if mixed mode
  • Base64/Plain → decode and join
  • No source reachable → ErrProviderSourceUnavailable (no fallback)
        │
        ▼
Cache.Set(240s) → return body with Content-Type + Subscription-Userinfo
```

## Stack

| Component | Library/DB | Version |
|-----------|------------|---------|
| Go | `go` | 1.25+ |
| Telegram API | `go-telegram-bot-api/telegram-bot-api/v5` | v5.5.1 |
| ORM | `gorm.io/gorm` + `gorm.io/driver/sqlite` | v1.31.2 |
| DB engine | SQLite (mattn/go-sqlite3, CGO) | `./data/rs8kvn.db` |
| Migrations | `golang-migrate/migrate/v4` | v4.19.1 |
| QR Code | `piglig/go-qr` | v1.1.0 |
| Logging | `go.uber.org/zap` + `lumberjack.v2` | v2.2.1 |
| Error tracking | `getsentry/sentry-go` | v0.48.0 |
| Metrics | `prometheus/client_golang` | v1.23.2 |
| Concurrency | `golang.org/x/sync/singleflight` | — |
| Testing | `stretchr/testify` | v1.11.1 |
| CI/CD | GitHub Actions → golangci-lint, gosec, test, Docker → GHCR | — |

## Current State

### Working Features

**User Features:**
- `/start`, `/help`, `/mysub` — start commands with inline keyboards (`/mysub` opens the subscription window directly)
- 📋 Subscription view — traffic usage, subscription link, QR code
- ☕ Donate, ❓ Help — auxiliary menus
- 🔗 Referral system — share links with in-memory cache + periodic DB sync
- 🎁 Trial subscriptions via `/i/{code}` landing page with Happ deep-links


**Admin Features:**
- `/del <id>` — delete subscription by ID
- `/setplan <subscription_id> <plan_id> [days]` — change subscription plan through the service layer (subscription row; free plan clears expiry, future expiry preserved when `days` omitted, else 30-day default)
- `/broadcast` — asynchronous broadcast flow: name → draft → **filter selection** → **recipient count preview** → inline confirm. Confirmation queues a campaign; a durable worker snapshots the audience into `broadcasts.recipients_state`, sends in batches (100, concurrency 10), retries transient errors twice, recovers stale leases after restart, and stores final counters/report in `broadcasts`. The confirm step offers **send now** or **schedule** (day picker → hour picker, Moscow time). A scheduled campaign keeps `planned_at` (persisted, never overwritten) and stays `scheduled`; `GetRunnableBroadcasts` only returns it once `planned_at` is due, so it also survives restarts and can be canceled before launch.
- `/broadcasts` — recent campaign history with details, cancellation of active campaigns, and retry of failed recipients. Anonymous trials are excluded; `expired` is not offered as a broadcast status, while `all` is explicit. Migration 036 creates `broadcasts` with recipient state, delivery classification, and retry metadata columns; no recipient table is created. `blocked` means Telegram explicitly reports that the user blocked the bot; deactivated or missing chats are `unreachable`. Details send one compact admin message, while complete ID lists remain in the persisted campaign report.

#### Broadcast Filters

After entering the draft text, the admin sees an inline keyboard for selecting recipient filters. Filters are stored as JSON in `broadcasts.filters` and applied server-side via SQL WHERE clauses.

**Filter fields (`BroadcastFilter` struct):**

| Field | JSON key | Type | Description |
|-------|----------|------|-------------|
| `PlanType` | `plan_type` | `string` | `"paid"` (has `product_id` or positive `price_paid_cents`) or `"free"` (free plan without product and payment). Payment state, not plan name. Empty = all eligible plans. Trials are always excluded. |
| `SubscriptionStatus` | `subscription_status` | `string` | `"active"`, `"revoked"`, `"all"`. Empty = `"active"` (default); expired is not a broadcast option. |
| `RegisteredAfter` | `registered_after` | `*time.Time` | Users registered after this date (inclusive). |
| `RegisteredBefore` | `registered_before` | `*time.Time` | Users registered before this date (inclusive). |
| `InactiveDays` | `inactive_days` | `*int` | `0` = never accessed (`last_request IS NULL`). `>0` = last access older than N days. `nil` = no filter. |
| `EverPaid` | `ever_paid` | `*bool` | `true` = at least one paid order in `orders` table. `false` = never paid. `nil` = no filter. |

**How filters combine:**

All active filters are combined with **AND** logic. Example:
```json
{"plan_type": "paid", "inactive_days": 30, "ever_paid": true}
```
= Paid plan AND inactive for 30+ days AND has payment history.

**Keyboard layout (filter selection):**

```
[👥 Все] [💰 Платные] [🆓 Бесплатные]
[📋 Все] [✅ Активные] [🚫 Отозванные]
[📅 Рег. за 3 мес] [📅 Рег. за 6 мес] [📅 Рег. за год] [📅 Рег. все]
[🚫 Не обращались] [⏰ > 1 мес] [⏰ > 3 мес] [👤 Без фильтра]
[💳 Платили] [🆓 Не платили] [👤 Без фильтра]
[✅ Отправить] [❌ Отмена]
```

**Recipient count preview:**

After pressing "✅ Отправить", the system queries `GetFilteredTelegramIDCount` and shows:

```
📦 Рассылка: Промо

👥 Получателей: 1234
🔍 Фильтр: Платные · Активные · Не активны > 30 дн.

⚡ Отправить 1234 пользователям?
```

Buttons:
```
[✅ Подтвердить]
[🔙 Назад к фильтрам] [❌ Отмена]
```

The admin can go back to adjust filters or confirm to start the broadcast.

**SQL WHERE clauses generated:**

| Filter | SQL condition |
|--------|---------------|
| `plan_type: paid` | `product_id IS NOT NULL OR price_paid_cents > 0` |
| `plan_type: free` | `plan_id = (SELECT id FROM plans WHERE name = 'free') AND product_id IS NULL AND price_paid_cents <= 0` |
| `subscription_status: active` | `status = 'active'` |
| `subscription_status: revoked` | `status = 'revoked'` |
| `registered_after` | `created_at >= ?` |
| `registered_before` | `created_at <= ?` |
| `inactive_days: 0` | `last_request IS NULL` |
| `inactive_days: N` | `last_request < datetime('now', '-N days')` |
| `ever_paid: true` | `id IN (SELECT subscription_id FROM orders WHERE status = 'paid')` |
| `ever_paid: false` | `id NOT IN (SELECT subscription_id FROM orders WHERE status = 'paid')` |

**Important notes:**
- `PricePaidCents` is NOT cumulative — it stores the current product price, not total spent. For historical payment checks, the `orders` table is used.
- `plan_type: paid` uses payment fields (`product_id`/`price_paid_cents`), so trial and manually granted premium plans are not treated as paid. Trials are excluded by `telegram_id > 0` and the trial-plan predicate.
- Empty filter `{}` = all active users (same as no filter).
- Broadcast details show counters plus complete Telegram ID lists for delivered, blocked, failed, and unprocessed recipients; blocked IDs are retained in `delivery_report.blocked`.
- Filters are applied at the SQL level (not in-memory), so they scale to thousands of users.
- `/send <id|username> <msg>` — private message (30s cooldown per admin)
- `/refstats` — referral statistics (top 10 from cache)
- 📊 Stats — bot statistics

| **Infrastructure** |
| - Health endpoints — `/healthz` (503 when Down), `/readyz` (503 during init)
| - Invite/trial landing — `/i/{code}` with IP rate limit (3/hour), cookie dedup (3h) |
| - Per-user rate limiting — chatID token bucket (30 tokens, 5/sec refill, 10-min idle cleanup) |
| - Subscription proxy — `GET /sub/{subID}` with Builder-based aggregation of ProviderSources, headers, 240s TTL cache, singleflight |
| - Daily backups — WAL checkpoint, atomic copy, 30 rotated backups by default |
| - Sentry error tracking (+ traces), Zap structured JSON logging with rotation
| - Order/Product tracking — payment lifecycle (pending/paid/expired/canceled) with 30-min payment window
| - Docker: multi-stage build (UPX compression), non-root user, healthcheck, GHCR images
| - CI/CD: GitHub Actions — golangci-lint, gosec, tests (race), Docker build/push
| - Prometheus metrics — `/metrics` endpoint with HTTP, bot, DB, cache, subscription, and payment metrics |
| - Plans & pricing — `plans`, `products`, `orders` tables for subscription plan management and payment lifecycle |
| - Subscription expire worker — background worker handling subscription expiration |

### Test Coverage

| Module | Coverage | Status |
|--------|----------|--------|
| `internal/flag` | **97.7%** | ✅ |
| `internal/ratelimiter` | **97.4%** | ✅ |
| `internal/heartbeat` | **96.2%** | ✅ |
| `internal/service` | **95.2%** | ✅ |
| `internal/config` | **91.8%** | ✅ |
| `internal/web` | **90.3%** | ✅ |
| `internal/bot` | **92.6%** | ✅ |
| `internal/utils` | **90.0%** | ✅ |
| `internal/logger` | **88.9%** | ✅ |
| `internal/backup` | **83.2%** | ✅ |
| `internal/subserver` | **82.5%** | ✅ |
| `internal/scheduler` | **81.2%** | ✅ |
| `internal/database` | **77.8%** | 🟡 |
| `cmd/bot` | **5.4%** | 🟡 (integration tests cover indirectly) |
| **Overall** | **66.2%** | 🟡 |

All tests pass with `-race` detector. Fuzzing enabled for critical functions.

---

## Critical Nuances

### Subscription Content (Builders)
- **No panel integration:** the bot never registers clients on a VPN panel; a subscription is a local row. The legacy node subsystem (3x-ui/proxman, `nodes`, the sync state machine, panel traffic accounting) was removed in migration `051_remove_legacy_nodes`.
- **Builder resolution:** `ResolveSubscriptionBuilder` picks `subscriptions.subscription_builder_id`, otherwise the plan default (`plans.subscription_builder_id`); a disabled or dangling builder is skipped.
- **Sources:** `provider_sources` hold URL, format and headers (server-side only). The stored catalogue (`provider_source_entries`) only enriches entries; live upstream data is authoritative.
- **Resilience:** a failing source is skipped; if no source can be fetched the request fails with `ErrProviderSourceUnavailable`. Fetch errors never contain upstream URLs or credentials.
- **Configuration:** ProviderSources, Builders and their rules are managed in the admin UI/API.

### Subscription Flow
- **Trial:** `/i/{code}` → IP rate limit (3/hour) → DB trial record (negative telegram_id) → user clicks link in Telegram → `BindTrialSubscription` sets telegram_id (из отрицательного в реальный), sets referred_by if from invite
- **Regular:** `create_subscription` callback → DB record (`SubscriptionService.Create`) → cache invalidate → admin notify (no external provisioning)
- **Trial cookie:** `rs8kvn_trial_{code}` prevents duplication for 3 hours (HttpOnly, Secure, SameSite=Strict)
- **Atomic cleanup:** `DELETE ... RETURNING` for expired trials (prevent race with bind)
- **Share referral:** `pendingInvites[chatID]` cached 60 min (in-memory, periodic cleanup prevents leak)
- **TelegramID conventions:** Positive = bound users, Negative = unbound trial subscriptions

### Payments (Platega)
- **Entry point:** `POST /payment/callback` — guard chain 405/503/401/400/200; body 256 KiB, `UseNumber`, single JSON document.
- **Confirm:** `CONFIRMED` → `OrderService.ConfirmPayment` — atomic CAS `pending → paid` + subscription update + plan update in one DB transaction (`ConfirmOrderPaidCAS`). There is no external sync step after commit.
- **Admin alerts (best-effort Markdown, `TelegramAdminID`):** on activation (`Activated=true`, only the pending→paid caller) `notifyAdminPaid` — tariff, formatted amount, clickable buyer link (`utils.FormatUserLink`), `🆕 Покупка` vs `🔄 Продление` (from `PricePaidCents`/`ProductID` captured before CAS); on paid-order chargeback (`WasPaid=true`) `notifyAdminChargeback` — single alert with tariff, amount, buyer link and access status (downgraded to free / preserved). Every integration problem (mismatch, late/unknown callback, provider error, DB failure) → `NotifyPaymentIssue` → `notifyAdmin` + `payment_issues_total{event}`. Duplicate callbacks are silent.
- **Late confirmed payment:** `CONFIRMED` for an `expired`/`canceled` order never activates it — money flagged for manual review (refund or manual activation), admin alerted (`late_confirmed_callback`). Race documented in `doc/platega-T3.md` §5.4.
- **Chargeback:** `CHARGEBACKED` on a paid order auto-downgrades the subscription to the free plan unless another paid order exists (access preserved for manual review).
- **Payment window:** link lifetime is determined by Platega `expiresIn` and stored as absolute UTC `payment_expires_at`; a still-valid saved link is reused. Only a new `RequestPayment` after that timestamp terminalizes the pending order as `expired`; a late `CONFIRMED` for an `expired`/`canceled` order never activates it and is sent for manual review.
- **Metrics:** `payment_operations_total{operation,result}`, `payment_operation_duration_seconds`, `payment_amounts_cents_total{operation,currency}` (confirmed/chargeback), `payment_issues_total{event}`.

### Subscription Deletion
- **Order:** mark as revoked (journaled) → physical deletion of the DB row → cache invalidation (no external deprovisioning)
- **Rationale:** the subscription is inaccessible (`/sub` answers 404) as soon as the revoked status is set; physical deletion happens last. There is no VPN deprovisioning. See AGENTS.md for the flow description.
- **Referral cache:** `DecrementReferralCount` called after successful deletion.

### Subscription Proxy
- **Endpoint:** `GET /sub/{subID}` — subID = SubscriptionID from DB (14 random bytes → 28 hex chars)
- **Config:** the subserver serves the content assembled by the resolved Builder from ProviderSources; no extra-servers file is used.
- **Cache:** 240s TTL hardcoded (`config.SubServerCacheTTL`)
- **Singleflight:** First request fetches, others wait and get same result (prevents thundering herd) — загрузка внешнего конфиг-файла не выполняется (фича удалена)
- **Content-Length:** Removed after merge (body size changes, Go uses chunked encoding)
- **Rate limiting:** Currently none — 240s cache TTL mitigates abuse; future: per-IP limit

### Referral Cache
- **Source of truth:** subscriptions table (`SELECT referred_by, COUNT(*) GROUP BY referred_by`)
- **Cache purpose:** Read-through for real-time display (`/refstats`) without hitting DB
- **Save() is no-op:** DB already reflects correct count after changes (Created/Deleted/Bound)
- **Sync():** Calls `Load()` hourly to refresh from DB
- **Admin rate limit:** 30s cooldown between `/send` commands per admin (sync.Map)

### Database
- **Engine:** SQLite (mattn/go-sqlite3, WAL mode)
- **Soft deletes:** none. On deletion the subscription is first marked revoked, then the row is physically deleted (see AGENTS.md: Delete flow).
- **Trial subscriptions:** `telegram_id < 0` (negative value) until activated via `/start trial_{subID}`
- **Migrations:** Auto-applied on startup from `internal/database/migrations/` (embedded via `go:embed`)
- **Legacy support:** Auto-migration for pre-embedded databases (adds `subscription_id` column, drops `x_ui_host`)
- **trial_requests cleanup:** 1-hour cutoff (matching rate-limit window) + 1s buffer to avoid boundary race
- **Connection pool:** `MaxOpenConns=1` (SQLite single-writer), `MaxIdle=1`, `ConnMaxLifetime=5m`
- **Orders:** `orders` table tracks payment lifecycle: pending → paid → expired/canceled. Statuses enforced via CHECK constraint (created with the table, migration 017 — effective, unlike the ALTER-based retry CHECK from 025). Payment window is provider-defined by Platega `expiresIn` and stored as absolute `payment_expires_at`; a new `RequestPayment` after that timestamp terminalizes the pending order as `expired`, while a late `CONFIRMED` for an `expired`/`canceled` order never activates it and is handled as manual review.
- **Plans:** `plans` table (name, default builder reference) and `products` (duration, price). Builders and ProviderSources live in the `subscription_builders*` and `provider_sources*` tables.
- **Devices tracking:** `subscriptions.devices` column stores JSON array of client request header maps (HWID, Device-OS, etc.). `ips` column stores IP→timestamp entries. `last_request` column (*time, indexed) records the last time a client fetched its subscription via `/sub/{id}` (best-effort, updated on both cache hit and cache miss).

### Configuration
- **Required:** `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ADMIN_ID` (must be positive), `GLOBAL_SUB_URL` (required, builds sub URLs)
- **Validated:**
  - `GLOBAL_SUB_URL` — must be valid URL with http/https scheme (S3: scheme allowlist)
  - `SENTRY_DSN`, `HEARTBEAT_URL` — must be valid URLs with http/https scheme
  - `SITE_URL` — must be valid URL
- **Web server:** Runs on `WEB_SERVER_PORT` (default 8880), bound to all interfaces
- **Init failure:** Fatal exit for DB and Bot API init errors (cannot operate without them)
- **SUBSERVER_ACCESS_LOG:** Optional path for `/sub/{id}` access logging (space-separated format with upstream fetch stats)

### Rate Limiting
- **Per-user (Telegram):** Each `chatID` gets own token bucket (max 30 tokens, refill 5/sec)
  - Applied to all command/callback handlers
  - `MaxConcurrentHandlers=10` semaphore prevents goroutine explosion
- **Trial endpoint (IP-based):** 3 requests/hour per IP (DB-tracked, auto-cleanup)
- **Admin `/send`:** 30-second cooldown per admin (in-memory sync.Map)
- **Broadcast:** 50ms delay between messages (~20 msg/sec, respects Telegram limits)

### Security
- **Input validation:** Regex validation rejects path-separator/invalid characters in invite codes (`internal/web/web.go`) and subscription IDs.
- **IP spoofing (S2):** `getClientIP` uses rightmost IP from `X-Forwarded-For` (set by trusted reverse proxy), NOT leftmost (client-controlled, spoofable). Only trusted from loopback.
- **URL scheme restriction (S3):** `validateURL` restricts all configured URLs to `http`/`https` schemes only — prevents `file://`, `gopher://`, etc. SSRF vectors
- **Web→bot dependency break (A1):** `internal/web` no longer imports `internal/bot` — `Server.botUsername string` instead of `*bot.BotConfig`, reducing coupling and attack surface
- **API auth:** Timing-safe token comparison via `crypto/subtle.ConstantTimeCompare`
- **No secrets in code:** `.env` only, `.env.example` has placeholders
- **HTTP timeouts:** ReadHeaderTimeout 5s, ReadTimeout 10s, WriteTimeout 30s, IdleTimeout 60s
- **Port binding:** Verified before goroutine launch — `net.Listen()` then `Serve()` in separate goroutine
- **Non-root Docker:** UID 1000, `no-new-privileges:true`


### Health Checks
- **`/healthz`:** DB ping → 200 (ok) or 503 (down)
- **`/readyz`:** Simple flag — set to true only after all services initialized → 200 or 503

### Docker
- **Base:** Alpine 3.21 (runtime, with procps), golang:1.25-alpine (builder)
- **Binary:** UPX compressed (-9) — ~30–40% smaller
- **Migrations:** Embedded via `COPY internal/database/migrations`
- **Data volume:** `./data:/app/data` (persistent)
- **Health check:** `wget -q -O - http://127.0.0.1:8880/healthz` (application and database health)
- **Resource limits:** 0.5 CPU, 128MB memory (2× GOMEMLIMIT for GC headroom)
- **Stop grace period:** 90s, SIGTERM

---

## Quick Commands

```bash
# Run all tests with race detector
go test -race -count=1 ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Build binary
go build -ldflags="-s -w -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# Run linters
golangci-lint run ./...
gosec ./...

# Run locally
go run ./cmd/bot
```
