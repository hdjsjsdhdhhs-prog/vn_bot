# Architecture — rs8kvn_bot

rs8kvn_bot — production-ready Telegram bot for distributing VLESS+Reality+Vision VPN subscriptions. Built with Go, following Clean Architecture principles.

A subscription is purely local state: a `subscriptions` row (status, plan, expiry, token). The bot does **not** register clients on any VPN panel. The content served by the `/sub` endpoint is assembled on request by a **Subscription Builder** from **ProviderSource** catalogues (see [Subscription Content Assembly](#subscription-content-assembly)). The legacy 3x-ui/proxman node subsystem (`nodes`, `plan_nodes`, `subscription_nodes`, `internal/vpn`, `internal/xui`, the panel sync/traffic workers) was removed in migration `051_remove_legacy_nodes`.

**Key characteristics:**
- Event-driven with bounded concurrency (worker pool)
- Per-source best-effort fetching of upstream provider sources (a failed source is skipped; if none is available the request fails with `ErrProviderSourceUnavailable`)
- Comprehensive caching (in-memory LRU, TTL)
- Graceful shutdown with coordinated cleanup
- 66.2% aggregate test coverage in the latest documented run (unit, e2e, fuzz, leak detection)
- Payment/order tracking for subscription purchases
- Builder-based subscription assembly from ProviderSource catalogues (no panel provisioning, sync queue or sync worker)
- 3-touch expiry reminders (3d/1d/3h) with atomic claim and bitmask (`subscriptions.reminders_sent`)
- Dynamic plan resolution by name (no hardcoded IDs)

---

## System Context Diagram

```
┌───────────────────────────────────────────────────────────────────┐
│                          EXTERNAL SYSTEMS                         │
├───────────────────────────────────────────────────────────────────┤
│ Telegram Bot API        Provider sources          Platega / Stars │
│ (users, admin)          (upstream feeds,          (payments)      │
│                         read-only HTTP)                           │
│ Sentry (optional)       Heartbeat URL (optional)                  │
└───────────────────────────────────────────────────────────────────┘
                    │
                    ▼
┌────────────────────────────────────────────────────────────────────────────┐
│                         rs8kvn_bot (single binary)                         │
├────────────────────────────────────────────────────────────────────────────┤
│ cmd/bot             event loop, graceful shutdown, background workers      │
│ internal/bot        Telegram layer: commands, callbacks, admin, caches     │
│ internal/web        HTTP: /healthz /readyz /i/{code} /sub /metrics,        │
│                     /payment/callback, admin API/UI                        │
│ internal/subserver  /sub: Builder runtime, provider fetch, response cache  │
│ internal/service    business logic: subscriptions, orders, trials, warm-up │
│ internal/database   GORM + SQLite, embedded migrations                     │
│ internal/scheduler  backup, trial cleanup, expire, reminders, fee, warm-up │
└────────────────────────────────────────────────────────────────────────────┘
```

---

## Package Structure

```
internal/
├── bot/              # Telegram layer
│   ├── handler.go           # Main router, update loop
│   ├── command.go            # /start, /help, /invite
│   ├── callback.go           # Inline keyboard callbacks
│   ├── admin.go             # /del, /broadcast, /send, /refstats, broadcast cards
│   ├── subscription_handler.go # Create/view/QR subscription
│   ├── menu.go              # Navigation: donate, help, back
│   ├── cache.go             # LRU cache (1000 entries, 5min TTL)
│   ├── referral_cache.go    # Referral count cache (sync hourly)
│   ├── keyboard_builder.go  # Telegram inline keyboards
│   └── message_sender.go    # Rate-limited send wrapper
├── web/              # HTTP server (no longer imports internal/bot — A1 fix)
│   ├── web.go               # Server struct, routes, health, getClientIP
│   ├── middleware.go        # Access-log response recording
│   └── templates/           # trial.html, error.html
├── subserver/         # Subscription server (aggregation + proxy)
│   ├── service.go           # Thin cache adapter over SubscriptionCache (Get/Set/Invalidate)
│   ├── fetch.go             # Fetch from upstream provider sources + format detection (JSON/Clash/Base64/Plain)
│   └── servers.go           # Subscription format helpers and server-config conversion
├── service/          # Business logic
│   ├── subscription.go      # Use cases: Create, CreateTrial, BindTrial, DeleteByID (revoke → physical delete), AdminSetPlan, ExpireSubscription, CleanupExpiredTrials
│   ├── subscription_renewal.go   # Renewal of an existing subscription
│   ├── subscription_reminders.go # Reminder window model and Telegram delivery
│   ├── builder.go           # Subscription Builder / ProviderSource admin use cases
│   ├── order.go, purchase*.go, tariffs.go # Orders, purchase policy, tariffs
│   ├── stars.go             # Telegram Stars payments
│   ├── trial.go, warmup*.go # Trial offer and customer warm-up
│   └── payment/             # Payment provider integration
├── database/         # Persistence
│   ├── service.go           # GORM service + connection pool
│   ├── migrations.go        # Embedded migration runner
│   ├── migrations/          # Embedded SQL migrations through the current schema version
│   ├── models.go            # Subscription, Plan, Product, Order, Invite, ProviderSource, SubscriptionBuilder
│   ├── trials.go             # Trial subscription logic, generateTrialTelegramID
│   ├── subscriptions.go      # Subscription CRUD and lifecycle queries
│   ├── subscription_reminders.go # Atomic reminder claim/release and expiry query
│   ├── plans.go             # Plan CRUD
│   ├── products.go          # Product CRUD
│   └── orders.go            # Order CRUD
├── config/           # Configuration
│   ├── config.go            # Load + validate (URL scheme allowlist via S3)
│   └── constants.go         # All defaults & limits
├── flag/             # Typed environment variable registry
│   └── flag.go              # Registry, typed flags (string, int, int64)
├── metrics/          # Prometheus metrics
│   └── metrics.go           # HTTP, bot, DB, cache, subserver, subscription and payment metrics
├── logger/           # Logging
│   └── logger.go            # Zap + Sentry + lumberjack
├── ratelimiter/      # Rate limiting
│   ├── ratelimiter.go       # Token bucket core
│   └── per_user.go          # Per-chatID wrapper
├── scheduler/        # Background jobs
│   ├── backup.go            # Daily backup (03:00)
│   ├── trial_cleanup.go     # Expired trial cleanup at startup and every 3h
│   ├── provider_fee_worker.go        # Provider fee retry for paid orders
│   ├── warmup_worker.go              # Customer warm-up delivery
│   ├── subscription_expire_worker.go # Subscription expiry worker
│   └── subscription_reminder_worker.go # Expiry reminders: 3d / 1d / 3h, idempotent bitmask
├── backup/           # Backup engine
│   └── backup.go            # WAL checkpoint + atomic copy + rotate
├── heartbeat/        # External monitoring
│   └── heartbeat.go         # Periodic POST to HEARTBEAT_URL
├── interfaces/       # DI interfaces
│   └── interfaces.go        # BotAPI, DatabaseService (composition root); per-slice seams SubscriptionRepository/InviteRepository/TrialRepository/PlanRepository and composed WebRepository
├── utils/            # Utilities
│   ├── uuid.go              # Crypto-random UUID v4, SubID, invite code
│   ├── qr.go                # QR code generation
│   ├── format.go            # Progress bar, date formatting
│   └── markdown.go          # Markdown sanitization
└── testutil/         # Test helpers
    └── testutil.go          # Mock DB, Bot (flat DatabaseService fake)
```

---

## Component Deep Dive

### 1. Main Event Loop (cmd/bot/main.go)

**Concurrency model:** Bounded worker pool via semaphore.

```go
updateSem := make(chan struct{}, config.MaxConcurrentHandlers) // capacity = 10

for update := range updates {
    select {
    case updateSem <- struct{}{}: // acquire slot (blocks if full)
        updatesWg.Add(1)
        go func(u tgbotapi.Update) {
            defer func() {
                <-updateSem // release
                updatesWg.Done()
            }()
            handleUpdateSafely(ctx, handler, u)
        }(update)
    case <-ctx.Done():
        break eventLoop
    }
}
```

**Why semaphore?** Prevents unbounded goroutine creation under load (Telegram can send bursts).

**Graceful shutdown sequence:**
1. Signal received → `ctx.Done()` closes
2. `botAPI.StopReceivingUpdates()` — stops long polling
3. Drain updates channel (empty it)
4. Wait for `updatesWg` (in-flight handlers) with 90s timeout
5. Wait for background workers (`wg.Wait()`) with 90s timeout
6. Close logger, database

**Result:** No updates lost, all handlers complete or timeout, clean exit.

---

### 2. Rate Limiter (internal/ratelimiter/)

**Token bucket per user:**

```go
type TokenBucket struct {
    tokens     float64
    lastRefill time.Time
    mu         sync.RWMutex
}

// Allow() consumes 1 token if available, refills based on elapsed time.
// Returns true if token taken, false if would block.
```

**Per-user wrapper:**

```go
type PerUserRateLimiter struct {
    buckets map[int64]*TokenBucket // chatID → bucket
    mu      sync.RWMutex
    maxTokens  float64 // 30
    refillRate float64 // 5/sec
}
```

**Usage:**
```go
if !rateLimiter.Allow(chatID) {
    // reject command (return silently or send "rate limited")
    return
}
```

**Cleanup:** Stale buckets removed every `CacheTTL*2` (10 min) based on `lastRefill`.

---

### 3. Caching Strategy

**Two-level caching:**

| Cache | Purpose | TTL | Max Size | Eviction |
|-------|---------|-----|----------|----------|
| `SubscriptionCache` (bot/cache.go) | Cached subscriptions by `telegramID` | 5 min | 1000 entries | LRU + periodic cleanup |
| `ReferralCache` (bot/referral_cache.go) | Referral counts per referrer | 1 hour sync | unlimited (bounded by users) | N/A (full reload) |
| `SubserverCache` (subserver/cache.go) | Merged subscription bodies by `subID` | 240s (4 min) | 1000 entries | TTL expiration |

**Cache invalidation points:**
- Subscription created → `invalidateCache(telegramID)`
- Subscription deleted → `invalidateCache(telegramID)`
- Trial bound → `invalidateCache(telegramID)`
- Subserver reload → entire cache cleared on config change

**Pattern:** Cache-Aside with fail-closed status revalidation (a cache hit is not served when DB status cannot be verified).

---

### 4. Database Layer

**ORM:** GORM + SQLite (mattn/go-sqlite3)

**Connection pool:**
```go
MaxOpenConns = 1   // SQLite single-writer constraint
MaxIdleConns = 1
ConnMaxLifetime = 5m
ConnMaxIdleTime = 2m
```

**Migrations:** Embedded SQL files, applied via `golang-migrate` at startup.

**Transactions used for:**
- `CreateSubscription`: revoke old + create new (atomic)
- `BindTrialSubscription`: check telegram_id < 0 → update (race-safe)

**Orders/Products support:**
- `Product` — purchasable subscription product bound to a plan (name, price, duration)
- `Order` — purchase event with payment tracking (pending/paid/expired/canceled)
- `ListActiveProducts`, `GetProductByID`, `ConfirmOrderPaidCAS`
- Migration 017: `orders` table with CHECK constraint on status

**Indexes:**
```sql
CREATE INDEX idx_subscriptions_telegram_id    ON subscriptions(telegram_id);
CREATE INDEX idx_subscriptions_subscription_id ON subscriptions(subscription_id);
CREATE INDEX idx_subscriptions_expires_at     ON subscriptions(expires_at);
CREATE INDEX idx_subscriptions_last_request    ON subscriptions(last_request);
CREATE INDEX idx_subscriptions_invite_code     ON subscriptions(invite_code);
CREATE INDEX idx_subscriptions_referred_by     ON subscriptions(referred_by);
CREATE UNIQUE INDEX idx_invites_referrer_unique ON invites(referrer_tg_id);
CREATE INDEX idx_trial_requests_ip             ON trial_requests(ip);
CREATE INDEX idx_products_plan_id              ON products(plan_id);
CREATE INDEX idx_orders_subscription_id        ON orders(subscription_id);
CREATE INDEX idx_orders_status                 ON orders(status);
CREATE INDEX idx_orders_created_at             ON orders(created_at);
```

**Race-safe patterns:**
- `BindTrialSubscription`: `UPDATE WHERE status = active AND telegram_id < 0 AND plan_id = <trial_plan_id>` + `RowsAffected` check (trial-подписки используют отрицательные `telegram_id`, колонки `is_trial` нет)
- `CleanupExpiredTrials`: claim as `expired`, then physically delete the claimed anonymous trial rows (trials are not real user subscriptions); a failed delete leaves the claimed row for the next run
- `GetOrCreateInvite`: always returns the oldest (canonical) code for the referrer.
  The UNIQUE constraint + "one code per referrer" guarantee is enforced by migration 004
  (aggressive deduplication of historical duplicates that accumulated because 004 was
  never applied on legacy DBs due to the old runMigrations hack). Migration 005 is now
  a no-op placeholder to maintain linear migration history. Old codes deleted by 004
  are gone forever (old shared links using them will 404).

---

### 5. Graceful Shutdown

**Signal handling:**
```bash
SIGINT  (Ctrl+C)  → graceful shutdown
SIGTERM (docker stop) → graceful shutdown
SIGQUIT (kill -3) → graceful shutdown (also handled)
```

**Shutdown sequence** (`cmd/bot/lifecycle.go` — `runEventLoop` + `gracefulShutdown`; deferred cleanup of web server, logger and DB in `main()`):

1. `ctx.Done()` received → break event loop
2. `botAPI.StopReceivingUpdates()` — stops long polling, channel closes
3. Drain updates channel (discard remaining updates)
4. Wait for `updatesWg` (max 90s) — all handlers finish or timeout
5. Wait for background `wg` (DB pool metrics, backup, heartbeat, trial cleanup, provider fee, expire, reminder and warm-up workers) (max 90s)
6. Stop `subProxy` cache cleanup
7. Set `webServer.ready = false`
8. `webServer.Stop(ctx)` — shutdown HTTP server (5s timeout)
9. Close logger, database

**Timeouts:**
- `ShutdownTimeout = 90s` (config constant) for Telegram handlers and background workers
- Web server/access-log stop: 5s context in `main()`
- Docker `stop_grace_period`: 90s; the actual duration depends on the active shutdown phase

**Safety:** In-flight requests complete, no new updates accepted.

---

## Data Model

### ER Diagram (text)

```
┌─────────────────────────────────────────────────────────────┐
│                     subscriptions                           │
├─────────────────────────────────────────────────────────────┤
│ telegram_id          int64    UNIQUE INDEX                   │
│ username             string   INDEX                          │
│ client_id            string   NOT NULL UNIQUE                │
│ subscription_id      string   NOT NULL UNIQUE INDEX          │
│ token                string   NOT NULL UNIQUE INDEX          │
│ expires_at          *time     INDEX (NULL = perpetual)       │
│ status               string   default: "active"  INDEX       │
│ invite_code         *string   INDEX                          │
│ plan_id              uint     INDEX (FK → plans)             │
│ referred_by         *int64    INDEX                          │
│ product_id          *uint     INDEX                          │
│ provider_source_id   *uint     INDEX                         │
│ subscription_builder_id *uint  INDEX (override) │
│ started_at          *time                                     │
│ price_paid_cents     int64    default: 0                     │
│ currency            *string   size:3                         │
│ devices              text     default:'[]' (JSON array)      │
│ ips                  text     default:'[]' (JSON array)      │
│ last_request        *time     INDEX (NULL until first /sub)  │
│ created_at           time     autoCreate                     │
│ updated_at           time     autoUpdate                     │
│ reminders_sent       int      NOT NULL default: 0 (bitmask: 3d/1d/3h)   │
                               │
                               │ referred_by
                               ▼
                     ┌─────────────────────┐
                     │      invites        │
                     ├─────────────────────┤
                     │ code (PK)           │
                     │ referrer_tg_id (FK) │
                     │ created_at          │
                     └─────────────────────┘
                               │
                               │ 1:N (referrer → referrals)
                               ▼
                     (subscriptions.referred_by points here)

                     ┌─────────────────────┐
                     │   trial_requests    │
                     ├─────────────────────┤
                     │ id (PK)             │
                     │ ip (INDEX)          │
                     │ created_at          │
                     └─────────────────────┘
                               │
                               │ IP-based rate limit
                               ▼
                     (checked before trial creation)

┌─────────────────────────────────────────────────────────────┐
│                       products                              │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ plan_id              uint     INDEX  (FK → plans)           │
│ name                 string   VARCHAR(255) NOT NULL         │
│ duration_days        int      NOT NULL                      │
│ price_cents          int64    NOT NULL                      │
│ currency             char(3)  DEFAULT 'RUB'                 │
│ is_active            bool     DEFAULT true                  │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
└─────────────────────────────────────────────────────────────┘
                               │
                               │ 1:N (product → orders)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                       orders                                │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ subscription_id      uint     NOT NULL  (FK → subscriptions)│
│ product_id           uint     NOT NULL  (FK → products)     │
│ status               text     NOT NULL                      │
│                     CHECK (status IN                       │
│                       'pending','paid','expired','canceled')│
│ amount_cents         int64    NOT NULL                      │
│ currency             char(3)  DEFAULT 'RUB'                 │
│ payment_provider     text                                  │
│ provider_payment_id  text     External payment ID          │
│ created_at           datetime NOT NULL                      │
│ paid_at              datetime  When payment confirmed       │
│ activated_at         datetime  When subscription activated  │
│ expires_at           datetime  Payment expiry (e.g. 30 min) │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                        plans                                │
├─────────────────────────────────────────────────────────────┤
│ id (PK)              uint                                   │
│ name                 string   size:50 UNIQUE                │
│ is_active            bool     not null default: true        │
│ subscription_builder_id *uint  INDEX (plan default builder) │
│ created_at           time     autoCreate                    │
│ updated_at           time     autoUpdate                    │
└─────────────────────────────────────────────────────────────┘
```

**Order statuses:**
- `pending` — payment initiated, awaiting confirmation
- `paid` — payment confirmed, subscription activated
- `expired` — payment window expired (e.g. 30 min unpaid)
- `canceled` — user or system canceled

**Indexes:**
- `idx_orders_subscription_id` — fast lookup of orders by subscription
- `idx_orders_status` — filter by status
- `idx_orders_created_at` — chronological ordering
- `idx_products_plan_id` — fast lookup of products by plan

**Indexes rationale:**
- `telegram_id + status` → fast lookup of user's active subscription
- `subscription_id` → fast `/sub/{subID}` lookup
- `expires_at` → cleanup of expired subs
- `referred_by` → referral stats query
- `last_request` → "last active" queries (when a client last fetched its subscription)

---

## Configuration Schema

Configuration is loaded from **flat environment variables** (not nested YAML) via `internal/flag`
and validated in `internal/config/config.go`. Required vars must be set or `Load()` fails.

| Env var | Type | Default | Notes |
|---------|------|---------|-------|
| `TELEGRAM_BOT_TOKEN` | string | — (required) | Format `number:token` |
| `TELEGRAM_ADMIN_ID` | int64 | — (required) | Must be positive |
| `GLOBAL_SUB_URL` | URL | — (required) | e.g. `https://vpn.example.com/sub/` (trailing `/` appended) |
| `DATABASE_PATH` | string | `./data/rs8kvn.db` | SQLite file path |
| `LOG_FILE_PATH` | string | `./data/bot.log` | |
| `LOG_LEVEL` | enum | `info` | `debug`\|`info`\|`warn`\|`error` |
| `HEARTBEAT_URL` | URL | empty (disabled) | http/https only |
| `HEARTBEAT_INTERVAL` | int (s) | `300` | min `10` |
| `SENTRY_DSN` | URL | empty (disabled) | http/https only |
| `WEB_SERVER_PORT` | int | `8880` | `1`–`65535` |
| `SITE_URL` | URL | `https://vpn.site` | http/https only |
| `TRIAL_DURATION_HOURS` | int | `3` | `1`–`168` |
| `TRIAL_RATE_LIMIT` | int | `3` | `1`–`100` (per-IP per hour) |
| `CONTACT_USERNAME` | string | `''` | Support contact |
| `DONATE_CARD_NUMBER` | string | `''` | |
| `DONATE_URL` | string | `''` | |
| `DONATE_ENABLED` | bool | `true` | Toggle "☕ Донат" button in main menu |
| `SUBSERVER_ACCESS_LOG` | path | `''` (disabled) | Optional access log |

**Seeding:** Provider sources and Builders are managed through the admin UI/API and stored in the database, not in env.
Only the default **plans** (`trial`, `free`) are auto-seeded when the `plans` table is empty
(`internal/database/service.go`).

---

## Sequence Diagrams

### User gets subscription

```
User          Telegram        Bot handler      SubscriptionService         DB
 |  /start        |                |                    |                    |
 |--------------->|  update        |                    |                    |
 |                |--------------->|  main menu         |                    |
 |<---------------+----------------|                    |                    |
 |  "Get sub"     |                |                    |                    |
 |--------------->|  callback      |                    |                    |
 |                |--------------->|  Create(tgID, ...) |                    |
 |                |                |------------------->|  create / reanimate|
 |                |                |                    |------------------->|
 |                |                |                    |<-------------------|
 |                |                |                    | invalidate caches  |
 |                |                |<-------------------|                    |
 |  URL + QR      |                |                    |                    |
 |<---------------+----------------|                    |                    |
```

There is no panel call and no sync queue: the row is the whole access state.

### Client fetches the subscription

```
Client app     Web server      Subserver              DB          Provider sources
 |  GET /sub/..   |                |                    |                  |
 |--------------->|--------------->| load subscription, check status/expiry|
 |                |                |------------------->|                  |
 |                |                | ResolveSubscriptionBuilder            |
 |                |                |------------------->|                  |
 |                |                | fetch linked sources (best-effort)    |
 |                |                |-------------------------------------->|
 |                |                | apply rules, rename, assemble         |
 |<---------------+----------------| body + profile headers (cached)       |
```

---

## Subscription Content Assembly

A subscription row holds no VPN credentials of its own. On every `/sub` request the subserver (`internal/subserver`):

1. **Resolves the Subscription Builder** (`database.ResolveSubscriptionBuilder`): the per-subscription override `subscriptions.subscription_builder_id`, otherwise the plan default `plans.subscription_builder_id`. A disabled or dangling builder at one level is skipped and the next level is consulted.
2. **Fetches the linked, enabled ProviderSources** (`fetch.go`): read-only HTTP using server-side credentials; formats are JSON, Clash YAML, base64 and plain links. A failure of one source is best-effort (the source is skipped). If no source can be fetched the request fails with `ErrProviderSourceUnavailable` and never falls back to a legacy pipeline. Errors never contain upstream URLs or credentials.
3. **Applies the builder rules** (country/node items, renames, ordering). Servers are identified by a credential-free fingerprint (`EntryFingerprint`), so the identity survives renames and credential rotation. The response is a JSON array when every fetched source is JSON, otherwise base64 share links; builder metadata overrides `profile-title`, `support-url` and `announce`.
4. **Caches the merged body.** The cache key covers the builder version/metadata, the linked sources and every item rule, so configuration changes are served fresh.

The stored catalogue (`provider_sources`, `provider_source_entries`) only enriches entries (for example with a country); live upstream data is authoritative.

### Subscription lifecycle

- Create, renewal, plan change, expiry and downgrade only change database state (status, plan, expiry) and invalidate caches. There is no external provisioning, sync queue, retry state or orphan reconciliation.
- `revoked` — `/sub` answers 404, but the row is kept. A revoked subscription can be reanimated when the user returns.
- Subscriptions are physically deleted only by the admin `/del` command (`SubscriptionService.DeleteByID`: mark `revoked` with a journal record, then delete the row, then invalidate caches) and, for expired anonymous trials, by `CleanupExpiredTrials`.
- User-initiated operations (`Create`, `BindTrial`, `RenewSubscription`, `DeleteByID`) return errors to the caller; background workers log per-item failures as `Warn` and continue.

---

## Background Workers

Started from `cmd/bot/main.go` (`startBackgroundWorkers`); all stop on context cancellation and are awaited during graceful shutdown.

| Worker | Schedule | Description |
|--------|----------|-------------|
| DB pool metrics collector | Every 10s | Collects database pool metrics |
| `BackupScheduler` | Daily at 03:00 | WAL checkpoint, database backup and rotation |
| `Heartbeat` | `HEARTBEAT_INTERVAL`, default 5m | Sends the optional external monitoring heartbeat |
| `TrialCleanupScheduler` | Every 3h | Claims and deletes expired anonymous trials |
| `ProviderFeeWorker` | Every 5m | Retries best-effort provider fee lookups for paid orders (`orders.provider_fee_cents` is the durable retry queue); the only periodic payment reconciliation |
| `SubscriptionExpireWorker` | Every 1h | Expires paid subscriptions past `expires_at` (downgrade to the free plan) |
| `SubscriptionReminderWorker` | Every 30m | Sends idempotent 3d/1d/3h expiry reminders |
| `WarmupWorker` | Every 10m (tick budget 8m) | Delivers customer warm-up messages with claim-based idempotency |
| Network monitor | Optional (`NETMON_ENABLED`) | Best-effort checks of provider sources; never affects serving |

---

## Prometheus Metrics (internal/metrics/)

The bot exposes a `/metrics` endpoint (via `promhttp.Handler()`) on the HTTP server port.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | method, path, status | Total HTTP requests |
| `http_request_duration_seconds` | Histogram | method, path | HTTP request latency |
| `bot_updates_total` | Counter | command, result | Bot updates (success/error/rate_limited) |
| `bot_update_duration_seconds` | Histogram | — | Bot update processing time |
| `subserver_source_fetch_total` | Counter | `result`, `format` | Upstream source fetch results |
| `subserver_source_fetch_duration_seconds` | Histogram | `result` | Upstream source fetch duration |
| `cache_hits_total` / `cache_misses_total` | Counter | cache | Cache hit/miss |
| `subserver_cache_invalidations_total` | Counter | `reason` | Cache invalidations by reason |
| `subserver_no_items_total` | Counter | — | Requests returning no items |

**Path normalization:** `/i/{code}` → `/i/:code`, `/sub/{id}` → `/sub/:id` (reduces cardinality).

---

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-01 | Use SQLite over PostgreSQL | Simpler deployment, single file, adequate for <10k users |
| 2026-01 | In-memory caches vs Redis | No external dependency; cache sizes bounded (1000 entries) |
| 2026-02 | Long polling vs Webhook | Easier deployment (no public HTTPS needed), single instance ok |
| 2026-02 | GORM vs sqlx | Faster dev, migrations built-in, relationship support |
| 2026-03 | Separate subserver package | Reusable subscription server logic, clean separation |
| 2026-03 | Circuit breaker on XUI | Prevent cascade failures if panel down (superseded: removed by migration 051) |
| 2026-04 | 5-min subserver reload | Balance between config freshness and file I/O |
| 2026-04 | Token bucket rate limiting | Standard algorithm, per-user isolation, tunable |
| 2026-04 | Daily backup at 03:00 | Low-traffic period, WAL checkpoint ensures consistency |
| 2026-05 | Multi-node VPN abstraction (vpn/) | Support multiple 3x-ui/proxman panels, node-type routing (superseded: removed by migration 051) |
| 2026-05 | Plans & pricing model | Plans with devices_limit, traffic_limit; products with duration/price (superseded: removed by migration 051) |
| 2026-06 | Subscription sync pipeline | 4-state sync machine (subscription_nodes) with per-sub locking (superseded: removed by migration 051) |
| 2026-06 | Prometheus metrics | `/metrics` endpoint with HTTP, bot, DB, cache metrics |
| 2026-07 | Security hardening (S2/S3/A1) | X-Forwarded-For rightmost IP, URL scheme allowlist, web→bot dependency break |
| 2026-10 | Remove the legacy node subsystem (migration 051) | Subscriptions are assembled by Builders from ProviderSource catalogues; no panel provisioning, sync queue or panel traffic accounting |

---

## Broadcast Filters

The broadcast system supports recipient filtering via JSON predicates stored in `broadcasts.filters`. Filters are applied server-side using SQL WHERE clauses, ensuring scalability.

### Filter Schema

```json
{
  "plan_type": "paid",
  "subscription_status": "active",
  "registered_after": "2026-01-01T00:00:00Z",
  "registered_before": "2026-06-01T00:00:00Z",
  "inactive_days": 30,
  "ever_paid": true
}
```

All fields are optional. Empty filter `{}` = all active users.

### Filter Fields

| Field | Type | SQL Condition | Notes |
|-------|------|---------------|-------|
| `plan_type` | `string` | `"paid"`: `product_id IS NOT NULL OR price_paid_cents > 0`; `"free"`: free plan AND `product_id IS NULL` AND `price_paid_cents <= 0` | `"paid"` or `"free"` only |
| `subscription_status` | `string` | `status = ?` | Default: `"active"` |
| `registered_after` | `*time.Time` | `created_at >= ?` | Inclusive |
| `registered_before` | `*time.Time` | `created_at <= ?` | Inclusive |
| `inactive_days` | `*int` | `last_request < datetime('now', '-N days')` | `0` = never accessed |
| `ever_paid` | `*bool` | `id IN (SELECT subscription_id FROM orders WHERE status='paid')` | Historical check |

### Filter Combination Logic

All active filters are combined with **AND**:

```
WHERE telegram_id > 0
  AND status = 'active'
  AND (product_id IS NOT NULL OR price_paid_cents > 0)
  AND created_at >= '2026-01-01'
  AND last_request < datetime('now', '-30 days')
  AND id IN (SELECT subscription_id FROM orders WHERE status = 'paid')
```

### UX Flow

```
/broadcast
→ Enter name
→ Enter text (MarkdownV2)
→ Filter keyboard appears:
  [👥 All] [💰 Paid] [🆓 Free]
  [📋 All] [✅ Active] [🚫 Revoked]
  [📅 Reg. 3m] [📅 Reg. 6m] [📅 Reg. 1y] [📅 Reg. all]
  [🚫 Never accessed] [⏰ >1m] [⏰ >3m] [👤 No filter]
  [💳 Paid] [🆓 Never paid] [👤 No filter]
  [✅ Send] [❌ Cancel]
→ Admin selects filters (toggle on/off)
→ Preview updates with filter description
→ Press "✅ Send" → Recipient count preview:
  📦 Broadcast: Promo
  👥 Recipients: 1234
  🔍 Filter: Paid · Active · Inactive >30d
  ⚡ Send to 1234 users?
  [✅ Send now] [⏰ Schedule]
  [🔙 Back to filters] [❌ Cancel]
→ Press "✅ Send now" → Broadcast starts immediately
→ Press "⏰ Schedule" → Day picker (today / tomorrow / +3d / +1w)
  → Hour picker (00:00–23:00, Moscow time)
  → Preview: "Scheduled for 15.08.2026 18:00"
  [✅ Confirm] [🔙 Change time] [❌ Cancel]
→ Press "✅ Confirm" → campaign is stored with `planned_at` set and stays
  `scheduled`; the worker claims it once `planned_at` is due (also after restart).
  `planned_at` is preserved, not overwritten at start time.
```

### Important Implementation Details

1. **`PricePaidCents` is NOT cumulative** — it stores the current product price, not total spent. For historical payment checks, the `orders` table is used (`ever_paid` filter).

2. **Plan detection uses `plans` table** — the `plan_type` filter resolves the free plan by stable `plans.name = 'free'`, but eligibility is decided by payment state: `"paid"` = `product_id IS NOT NULL OR price_paid_cents > 0` (even on the free plan), `"free"` = free plan AND `product_id IS NULL AND price_paid_cents <= 0`.

3. **SQLite compatibility** — date arithmetic uses `datetime('now', '-N days')` syntax, not PostgreSQL `INTERVAL`.

4. **Performance** — all filters are applied at the SQL level (subqueries for `ever_paid`), not in-memory filtering. Scales to thousands of users.

---

## Future Considerations

| Area | Potential Improvement | Priority |
|------|----------------------|----------|
| **Database** | Migrate to PostgreSQL for horizontal scaling | P2 (when >10k users) |
| **Cache** | Redis for shared cache across multiple bot instances | P3 (if horizontal scaling) |
| **Rate limiting** | Distributed rate limiting via Redis (per-IP) | P2 (DDoS protection) |
| **Auth** | OAuth for admin panel (web UI) | P3 (convenience) |
| **Testing** | Property-based testing (quickcheck) | P2 (quality) |
| **CI/CD** | Automated security scanning (Trivy, gosec in CI) | P1 (security) |
| **Deployment** | Helm chart for Kubernetes | P2 (if using k8s) |
| **Storage** | Real Cloudflare R2 backend for UploadStore | P2 (WIRED-not-PROVEN) |
| **Transcription** | Live HTTP transcription endpoint | P2 (WIRED-not-PROVEN) |

---

### N. Subscription Reminder Worker (internal/scheduler/subscription_reminder_worker.go)

Worker каждые 30 минут выбирает активные платные подписки, у которых `expires_at` попадает в окна 3д / 1д / 3ч. Для каждого окна `SubscriptionService.SendExpiryReminder()` атомарно claim-ит bit только при неизменном сроке и отправляет продающее MarkdownV2-сообщение с преимуществами Premium, ссылкой на подписку и inline-кнопкой `💎 Продлить Premium` (`buy_premium_list`). При ошибке Telegram claim освобождается для повторной попытки. Повторы при пересекающихся окнах и параллельных вызовах исключаются условным claim по `subscriptions.reminders_sent`. При продлении (`RenewSubscription`) маска сбрасывается в `0` в одной транзакции с обновлением подписки и созданием order. Подписки без `expires_at`, а также free/trial планы не участвуют. Метрики: `subscription_reminders_total{window,result}` и `subscription_reminder_runs_total`.

*End of architecture documentation.*
