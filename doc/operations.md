# Operations Guide — rs8kvn_bot

---

## Table of Contents

1. [Quick Reference](#1-quick-reference)
2. [Upgrade](#2-upgrade)
3. [Backup & Restore](#3-backup--restore)
4. [Monitoring](#4-monitoring)
5. [Troubleshooting](#5-troubleshooting)
6. [Scaling](#6-scaling)
7. [Security Hardening](#7-security-hardening)

---

## 1. Quick Reference

### Essential Commands

```bash
# View logs (Docker)
docker logs -f rs8kvn_bot

# View logs (binary)
journalctl -u rs8kvn_bot -f  # if using systemd

# Restart service
docker restart rs8kvn_bot
# or
systemctl restart rs8kvn_bot

# Check health
curl http://localhost:8880/healthz
curl http://localhost:8880/readyz

# Check database
sqlite3 ./data/rs8kvn.db "SELECT COUNT(*) FROM subscriptions;"

# List backups
ls -lh ./data/*.backup*

# Test bot manually
go run ./cmd/bot
# or
./rs8kvn_bot
```

### Configuration Files

| File | Purpose |
|------|---------|
| `.env` | Environment variables (secrets) |
| `data/rs8kvn.db` | SQLite database |
| `data/bot.log` | Application logs (rotated) |
| `data/subserver.log` | Optional `/sub/{id}` access log |
| `internal/database/migrations/` | DB migration SQL files |

---

## 2. Upgrade

### 2.1 Docker (recommended)

**Zero-downtime upgrade:**

```bash
# 1. Pull new image
docker pull ghcr.io/kereal/rs8kvn_bot:latest

# 2. Stop old container
docker stop rs8kvn_bot

# 3. Remove old container
docker rm rs8kvn_bot

# 4. Start new container (same command as installation)
docker run -d \
  --name rs8kvn_bot \
  --restart unless-stopped \
  -v $(pwd)/.env:/app/.env:ro \
  -v $(pwd)/data:/app/data \
  -p 127.0.0.1:8880:8880 \
  ghcr.io/kereal/rs8kvn_bot:latest
```

**Using Docker Compose:**

```bash
docker-compose pull
docker-compose up -d
```

**Rollback:** If something goes wrong, specify previous tag:
```bash
docker run ... ghcr.io/kereal/rs8kvn_bot:v2.2.0
```

### 2.2 Binary (manual build)

```bash
# 1. Pull latest code
git pull origin dev

# 2. Build
go build -ldflags="-s -w -X main.version=$(git describe --tags --abbrev=0) -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o rs8kvn_bot ./cmd/bot

# 3. Stop old process (find PID)
pkill -f rs8kvn_bot
# or systemctl stop rs8kvn_bot

# 4. Start new
./rs8kvn_bot &
```

### 2.3 Database Migrations

Migrations run automatically on startup. If migration fails:
- Bot logs error and exits (fatal)
- Database remains in previous state (safe)
- Check `data/rs8kvn.db` integrity

**Manual migration:**
```bash
# Apply migrations using golang-migrate CLI
migrate -path internal/database/migrations -database "sqlite3://data/rs8kvn.db" up
```

**Rollback migration:**
```bash
migrate -path internal/database/migrations -database "sqlite3://data/rs8kvn.db" down 1
```

---

## 3. Backup & Restore

### 3.1 Automatic Backups

**Configured:**
- **Schedule:** Daily at 03:00 (config `DefaultBackupHour`)
- **Retention:** 30 rotated backup files (`config.DefaultBackupRetention`)
- **Location:** Same directory as database (`./data/`). The scheduler keeps 30 rotated backup files by default.
- **Naming:** `rs8kvn.db.backup.YYYYMMDD_HHMMSS`

**What gets backed up:**
- WAL checkpoint executed first → consistent snapshot
- Full copy of `rs8kvn.db`
- Not compressed (fast restore)

### 3.2 Manual Backup

```bash
# There is no standalone backup CLI. For a manual backup, stop the bot
# (or otherwise ensure no writes are in progress), checkpoint WAL, then copy:
sqlite3 ./data/rs8kvn.db "PRAGMA wal_checkpoint(TRUNCATE);"
cp --preserve=mode,timestamps ./data/rs8kvn.db ./data/backup.manual
chmod 600 ./data/backup.manual
```

### 3.3 Restore from Backup

```bash
# 1. Stop bot
docker stop rs8kvn_bot

# 2. Rename current DB (for safety)
mv ./data/rs8kvn.db ./data/rs8kvn.db.before-restore-$(date +%s)

# 3. Copy backup over
cp ./data/rs8kvn.db.backup.20260417_030000 ./data/rs8kvn.db

# 4. Fix permissions (if needed)
chown 1000:1000 ./data/rs8kvn.db  # Docker user
# or chown current-user if running binary

# 5. Start bot
docker start rs8kvn_bot
```

**Verify:**
```bash
sqlite3 ./data/rs8kvn.db "SELECT COUNT(*) FROM subscriptions;"
docker logs rs8kvn_bot | grep -i "database initialized"
```

### 3.4 Off-site Backups (recommended)

Currently backups are local only. For production, configure remote storage:

**Options:**
1. **S3-compatible** (MinIO, Backblaze B2, AWS S3):
   ```bash
   # Sync to S3 daily (cron)
   aws s3 cp ./data/rs8kvn.db.backup.* s3://your-bucket/backups/ --storage-class STANDARD_IA
   ```

2. **rsync to remote server:**
   ```bash
   rsync -avz ./data/rs8kvn.db.backup.* user@remote:/backup/rs8kvn/
   ```

3. **Encrypted backup** (GPG):
   ```bash
   gpg --symmetric --cipher-algo AES256 -o backup.gpg ./data/rs8kvn.db.backup
   ```

---

## 4. Monitoring

### 4.1 Health Checks

**Endpoints** (port 8880):

| Endpoint | Description | Success | Failure |
|----------|-------------|---------|---------|
| `GET /healthz` | Liveness: DB ping | 200 | 503 |
| `GET /readyz` | Readiness: all components initialized | 200 | 503 |

**Usage:**
```bash
curl -f http://localhost:8880/healthz || echo "DOWN"
curl -f http://localhost:8880/readyz || echo "NOT READY"
```

**Kubernetes probes:**
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8880
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 8880
  initialDelaySeconds: 5
  periodSeconds: 5
```

### 4.2 Heartbeat

If `HEARTBEAT_URL` configured, bot POSTs `{}` every `HEARTBEAT_INTERVAL` seconds (default: 300s).

**Verify:**
```bash
# Check recent heartbeat in logs
docker logs rs8kvn_bot 2>&1 | grep "Heartbeat sent"

# Monitor endpoint (UptimeRobot, healthchecks.io)
# Provide URL: http://your-host:8880/healthz
```

### 4.3 Subscription Access Log

If `SUBSERVER_ACCESS_LOG` is set, the bot appends every `/sub/{id}` request to that file as a space-separated line (no zap-console encoding, no message/caller/field keys). Fields are separated by spaces; values containing spaces are wrapped in double quotes. Access log writes are buffered asynchronously so disk I/O does not block subscription responses. If the file cannot be opened, the bot continues without the access log and writes an error to the main application log. Each line contains, space-separated:

- `timestamp` — request time in UTC (`2006-01-02T15:04:05.000Z0700`)
- `method` — HTTP method (e.g. `GET`)
- `request_uri` — full request URI (e.g. `/sub/{id}`)
- `status_code` — response status code
- `success/total` — upstream source fetch stats (e.g. `3/5`), or `-` if no sources were queried
- `client_ip` — client IP address
- `X-Hwid`, `X-Device-Os`, `X-Ver-Os`, `X-Device-Model`, and `User-Agent` request headers (sanitised: CR/LF/TAB replaced with spaces)

The main application log records an error message when this access log cannot be opened. To disable it, set `SUBSERVER_ACCESS_LOG` to an empty value and restart the bot.

### 4.4 Logs

**Location:** `./data/bot.log` (rotated: max 10MB × 2 backups, 14-day age limit)

**Log rotation:**
- Daily rotation at midnight (if using lumberjack)
- Manual rotation: `kill -USR1 <pid>` (not currently configured)
- Or via logrotate (system):

```conf
# /etc/logrotate.d/rs8kvn_bot
./data/bot.log {
    daily
    rotate 2
    compress
    missingok
    notifempty
    copytruncate
}
```

**Log levels:** Set `LOG_LEVEL=debug` for verbose, `info` for production.

**View logs:**
```bash
# Follow (zap console format)
tail -f ./data/bot.log

# Search errors
grep '"level":"error"' ./data/bot.log

# Last 100 lines
tail -n 100 ./data/bot.log
```

### 4.5 Sentry

If `SENTRY_DSN` configured, errors and panics are reported automatically.

**Features:**
- Release tracking (`rs8kvn_bot@<version>`)
- Performance traces sampled at 10% by the built-in logger configuration
- Stack traces on panic

**Test:**
```bash
# Trigger test panic (temporary)
kill -ABRT <pid>
# Check Sentry dashboard for new issue
```

### 4.6 Prometheus Metrics

The bot exposes a `/metrics` endpoint on the same port as health checks (default 8880).

**Endpoint:** `GET /metrics`

**Результат аудита (2026-08-26):**

Цель — держать только метрики, по которым есть алерт, SLO или оперативное действие. Сейчас объявлено 34 прикладных collector-а (без стандартных `go_*`, `process_*` и `promhttp_*`); это ещё приемлемо, но часть из них имеет невысокую диагностическую ценность. Платёжные метрики в целом добавлены правильно: отсутствуют user/order/provider ID labels, а суммы нормализуются до копеек и валюты.

**Рекомендуемый обязательный набор:**

- `http_requests_total`, `http_request_duration_seconds` — трафик, ошибки и latency API;
- `bot_updates_total`, `bot_update_duration_seconds` — основной пользовательский канал;
- `subserver_source_fetch_total`, `subserver_source_fetch_duration_seconds`, `subserver_no_items_total` — доступность и latency upstream-источников (ProviderSource), из которых Builders собирают подписку;
- `db_queries_total`, `db_query_duration_seconds`, `db_pool_wait` — ошибки/замедление SQLite;
- `active_subscriptions` — все активные подписки;
- `premium_subscriptions` — активные оплаченные Premium-подписки;
- `free_subscriptions` — активные бесплатные подписки;
- `trial_subscriptions` — активные trial-подписки; отозванные/неактивные отдельно не считаются;
- `payment_operations_total`, `payment_operation_duration_seconds`, `payment_amount_cents_total`, `payment_issues_total` — платёжная воронка и инциденты;

**Второстепенные:** cache hit/miss, Telegram API latency, subserver fetch детализация, worker run/latency и `db_pool_wait` полезны для расследований, но не требуют обязательных алертов.

**Кандидаты на удаление или исправление:**

1. `db_pool_open`, `db_pool_in_use`, `db_pool_idle` удалены: при `MaxOpenConns=1` они почти не дают новой информации.
2. `subscription_reminder_runs_total` и `subscription_expire_total` оставлены только как контроль запуска фоновых workers; обязательные алерты на них не нужны.
3. Детальные cache hit/miss duration-метрики удалены, чтобы не дублировать HTTP latency.
4. В документации используется фактическое имя `payment_amount_cents_total`.

**Labels:**

- Оставить bounded labels: `status` (лучше числовой код или класс `2xx/4xx/5xx`), `method`, нормализованный route, `result`, `operation`, `currency`, `event`, `window`, `format`, `cache`, `target`.
- Для HTTP желательно перейти от ручного `normalizePath` к router route pattern, чтобы новые динамические endpoints не создавали series по ID. Текущая нормализация покрывает только `/i/*` и `/sub/*`.
- Не добавлять `telegram_id`, `user_id`, `order_id`, `subscription_id`, `provider_payment_id`, URL, payload, username или payment URL: это unbounded/high-cardinality и потенциально PII.
- Для платежей полезно добавить только bounded `provider` (сейчас фактически всегда Platega) и, если появятся провайдеры, `payment_provider` в `payment_operations_total` и `payment_operation_duration_seconds`. Не добавлять `payment_method`, если его набор/значения не зафиксированы и он не нужен для отдельного алерта.
- Для `payment_operations_total` стоит различать bounded результаты `success`, `error`, `disabled`, `idempotent`, `manual_review`, `expired`, `rejected`; это даст воронку без IDs. Отдельный `payment_issues_total{event}` уже подходит для ручного разбора.
- `currency` допустима только при allowlist ISO-кодов; текущая нормализация регистра правильна, но желательно явно ограничить неожиданные значения.

**Минимальные платёжные запросы:**

```promql
# Ошибки платёжных операций
sum by (operation) (rate(payment_operations_total{result="error"}[15m]))

# Конверсия request → confirm (приближённо, по успешным операциям)
sum(rate(payment_operations_total{operation="confirm",result="success"}[1h]))
/
sum(rate(payment_operations_total{operation="request",result="success"}[1h]))

# Подтверждённый оборот за сутки в копейках
sum by (currency) (increase(payment_amount_cents_total{operation="confirmed"}[24h]))

# Chargeback amount rate (доля подтверждённого оборота, возвращённая через chargeback)
sum(rate(payment_amount_cents_total{operation="chargeback"}[24h]))
/
sum(rate(payment_amount_cents_total{operation="confirmed"}[24h]))
```

> Важно: суммы — counters, поэтому для оборота используйте `increase`, а не текущее значение. Для денежных алертов полезно дополнительно иметь в логах/БД точную детализацию; Prometheus не должен становиться платёжным ledger.

**Key metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | method, path, status | Application HTTP requests; `/metrics` and `/static/*` excluded |
| `http_request_duration_seconds` | Histogram | method, path | Application HTTP latency; `/metrics` and `/static/*` excluded |
| `premium_subscriptions` | Gauge | — | Текущее количество активных оплаченных Premium-подписок |
| `bot_updates_total` | Counter | command, result | Bot updates processed (success/error/rate_limited) |
| `bot_update_duration_seconds` | Histogram | — | Bot update processing time |
| `cache_hits_total` / `cache_misses_total` | Counter | cache | Cache hit/miss (subscription, referral, subserver) |
| `subserver_source_fetch_total` | Counter | result, format | Upstream source fetch results (success/error by format) |
| `subserver_source_fetch_duration_seconds` | Histogram | result | Upstream source fetch duration |
| `subserver_cache_invalidations_total` | Counter | reason | Cache invalidations by reason |
| `subserver_no_items_total` | Counter | — | Requests returning no items |
| `payment_operations_total` | Counter | operation, result | Payment request/confirm/cancel attempts by outcome |
| `payment_operation_duration_seconds` | Histogram | operation | Payment service operation latency |
| `payment_amount_cents_total` | Counter | operation, currency | Monetary amounts in cents by outcome (`confirmed`, `chargeback`) — watch for chargeback totals vs confirmed |
| `payment_issues_total` | Counter | event | Payment incidents requiring investigation or operator attention |

**Prometheus scrape config:**
```yaml
scrape_configs:
  - job_name: 'rs8kvn_bot'
    static_configs:
      - targets: ['localhost:8880']
    metrics_path: /metrics
```

**Grafana dashboard ideas:**
- HTTP error rate: `rate(http_requests_total{status=~"5.."}[5m]) / rate(http_requests_total[5m])`
- Bot error rate: `rate(bot_updates_total{result="error"}[5m])`
- Upstream-источники p99: `histogram_quantile(0.99, rate(subserver_source_fetch_duration_seconds_bucket[5m]))`
- Active subscriptions: `active_subscriptions`


## 5. Troubleshooting

### 5.1 Bot won't start

**Symptom:** Process exits immediately, logs show "Failed to ..."

**Common causes:**

| Error | Cause | Fix |
|-------|-------|-----|
| `Failed to load config: TELEGRAM_BOT_TOKEN is required` | Missing env var | Set in `.env` or export |
| `Failed to initialize database` | Permission denied or disk full | Check `data/` dir, permissions |
| `listen tcp :8880: bind: address already in use` | Port occupied | Change `WEB_SERVER_PORT` or kill process using port |

**Diagnostic:**
```bash
# Check config
cat .env | grep -v '^#'

# Test DB connection
sqlite3 ./data/rs8kvn.db "SELECT 1;"

# Check port
lsof -i :8880
netstat -tlnp | grep 8880
```

---

### 5.2 Bot starts but doesn't respond to messages

**Symptom:** `/start` sends no reply.

**Check:**
1. **Bot token valid?** Verify with @BotFather
2. **Webhook configured?** Bot uses `GetUpdates`, so webhook must be deleted:
   ```bash
   curl "https://api.telegram.org/bot$TOKEN/deleteWebhook"
   ```
3. **Bot is running?** `docker ps`, `ps aux | grep rs8kvn_bot`
4. **Updates received?** Check logs: `logger.Info("Received update")`
5. **Rate limited?** Check `ratelimiter` metrics (not exposed yet)

**Fix:**
```bash
# Reset webhook
curl "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/deleteWebhook"

# Restart bot
docker restart rs8kvn_bot
```

---

### 5.3 Subscription creation fails

**Symptom:** User clicks "Get subscription" → error message.

Creating a subscription is a local database operation: no VPN panel is called, so failures are database-side.

**Common errors:**

| Error | Cause | Fix |
|-------|-------|-----|
| `cannot allocate UUID` | DB full or read-only | Check disk space, DB permissions |
| `database is locked` | Concurrent writers on SQLite | See 5.7 |

**Debug steps:**
```bash
# Check DB state
sqlite3 ./data/rs8kvn.db "SELECT * FROM subscriptions WHERE telegram_id = <user_id> ORDER BY created_at DESC LIMIT 5;"

# Watch logs while testing
docker logs -f rs8kvn_bot 2>&1 | grep -E "error|failed|subscription"
```

---

### 5.4 Trial page returns 500

**Symptom:** `/i/INVITECODE` shows "Internal Server Error".

**Check:**
1. **Invite code valid?** Query DB:
   ```sql
   SELECT * FROM invites WHERE code = 'INVITECODE';
   ```
2. **IP rate limited?** Check `trial_requests` table:
   ```sql
   SELECT COUNT(*) FROM trial_requests WHERE ip = 'user_ip' AND created_at > datetime('now','-1 hour');
   ```
3. **File permission:** `data/` writable (SQLite writes)

**Logs to check:**
```bash
grep "trial" ./data/bot.log | tail -50
```

---

### 5.5 `/sub/{subID}` returns 502/empty

**Symptom:** Subscription link doesn't work.

**Possible causes:**
- The subscription is `revoked`, expired or not found (the endpoint answers 404)
- No linked ProviderSource could be fetched (`ErrProviderSourceUnavailable`, 502): the sources are down, disabled or unreachable from the host
- The Builder is disabled, has no enabled sources, or its item rules select nothing (`subserver_no_items_total` grows)
- A cached body is served (the cache key changes with the Builder/source configuration; otherwise the TTL is 240s)

**Fix:**
```bash
# Check subID in DB
sqlite3 ./data/rs8kvn.db "SELECT subscription_id, status, expires_at FROM subscriptions WHERE subscription_id = 'abc123';"

# Invalidate cache (restart bot)
docker restart rs8kvn_bot

# Check subserver logs
docker logs rs8kvn_bot | grep "subserver"
```

**Check upstream state:**
- Metrics: `subserver_source_fetch_total{result="error"}` and `subserver_source_fetch_duration_seconds` show failing or slow sources.
- In the admin UI/API check that the ProviderSource is enabled and its catalogue sync succeeded, and that the Builder links at least one enabled source.

---

### 5.6 High memory usage

**Symptom:** Container/binary uses >200MB RAM.

**Common causes:**
- Large broadcast in progress
- Cache size too large (default 1000, could increase)
- Memory leak (goroutine leak)

**Diagnose:**
```bash
# Docker stats
docker stats rs8kvn_bot

# Go heap (if binary, send SIGUSR1 to get heap profile)
kill -USR1 <pid>
# profile in /tmp

# Count goroutines
curl http://localhost:8880/debug/pprof/goroutine?debug=1  # if enabled
```

**Mitigation:**
- Increase `GOGC` (default 100, lower = more frequent GC)
- Reduce `CacheMaxSize` (recompile)
- Restart bot (graceful shutdown respects in-flight handlers)

---

### 5.7 Database locked / slow queries

**Symptom:** Logs show "database is locked" or timeouts.

**Cause:** SQLite allows only 1 writer. Concurrent writes queue.

**Solutions:**
1. **Tune connection pool:** Already set `MaxOpenConns=1`. Check if other processes accessing DB.
2. **Reduce write frequency:** Trial cleanup runs every 3 hours, backup daily — OK.
3. **Consider PostgreSQL** if >10k writes/day.

**Check locking:**
```bash
sqlite3 ./data/rs8kvn.db "PRAGMA wal_checkpoint(TRUNCATE);"
sqlite3 ./data/rs8kvn.db "PRAGMA journal_mode=WAL;"
```

---

### 5.8 Logs fill disk

**Symptom:** `data/bot.log` grows beyond 10MB or rotated files are not being created.

**Cause:** Rotation misconfigured or log level `debug` with high traffic.

**Fix:**
```bash
# Truncate log (safe, lumberjack will continue)
> ./data/bot.log

# Adjust rotation in logger config (code: internal/logger/logger.go)
# MaxSize: 10 MB, MaxBackups: 2, MaxAge: 14 days
```

**Logrotate alternative:** See section 4.3.

---

## 6. Scaling

### 6.1 Current Limitations

| Resource | Limit | Notes |
|----------|-------|-------|
| DB writes | ~1/sec | SQLite single-writer |
| Concurrent handlers | 10 | Config `MaxConcurrentHandlers` |
| Cache size | 1000 subs | `CacheMaxSize` constant |
| Users | ~10k comfortably | Memory: ~100 bytes/user |

### 6.2 When to Scale

- **>10k active users:** Consider PostgreSQL
- **>50k users:** Consider a read replica (requires moving off SQLite)
- **High write load:** Move to PostgreSQL (SQLite is single-writer)

### 6.3 Horizontal Scaling (multiple bot instances)

**Not supported for polling mode** — Telegram only allows one `getUpdates` connection per bot token.

**To scale horizontally:**
1. Switch to **webhook mode** (requires public HTTPS URL)
2. Put multiple bot instances behind load balancer
3. Share same database (PostgreSQL required)
4. Use Redis for distributed cache (instead of in-memory)

**Complexity:** High. Not recommended for <100k users.

### 6.4 Vertical Scaling

**Increase resources:**
- **Memory:** 128MB → 512MB (if cache increased)
- **CPU:** 0.5 cores → 1-2 cores (if high traffic)
- **Disk:** SSD recommended for SQLite performance

**Docker:**
```yaml
deploy:
  resources:
    limits:
      memory: 512M
      cpus: "1.0"
```

---

## 7. Security Hardening

### 7.1 Checklist

- [ ] Use HTTPS for ProviderSource URLs (no HTTP in production)
- [ ] Set strong `TELEGRAM_BOT_TOKEN` (20+ chars, random)
- [ ] Restrict `.env` permissions: `chmod 600 .env`
- [ ] Run Docker with `--read-only` (not possible due to SQLite writes)
- [ ] Enable `no-new-privileges:true` (already in docker-compose)
- [ ] Use non-root user (already in Dockerfile)
- [ ] Regular OS security updates
- [ ] Enable Sentry for error monitoring
- [ ] Configure firewall: only 8880 port open to internet (optional, health checks)
- [ ] Backup encryption (if using S3, enable SSE)
- [ ] Monitor `/healthz` with external service (UptimeRobot, healthchecks.io)
- [ ] Review logs daily for `error`/`warn` level

### 7.2 Secret Management

**Do NOT:**
- Commit `.env` to git (in `.gitignore`)
- Share secrets in logs
- Use default credentials or tokens

**Do:**
- Use secrets manager (HashiCorp Vault, AWS Secrets Manager)
- Rotate ProviderSource credentials (headers/tokens) according to the provider's policy
- Rotate Telegram bot token if exposed

### 7.3 Network Security

**Recommended reverse proxy (nginx):**
```nginx
server {
    listen 443 ssl http2;
    server_name vpn.example.com;

    location /healthz {
        proxy_pass http://localhost:8880;
        allow 127.0.0.1;  # Only local
        deny all;          # Block external health check probes
    }

    location /readyz {
        proxy_pass http://localhost:8880;
        allow 127.0.0.1;
        deny all;
    }

    # /sub and /i endpoints can be public
    location /sub/ {
        proxy_pass http://localhost:8880;
        limit_req zone=sub burst=10 nodelay;
    }

    location /i/ {
        proxy_pass http://localhost:8880;
        limit_req zone=trial burst=5 nodelay;
    }
}
```

**Rate limiting nginx:**
```nginx
limit_req_zone $binary_remote_addr zone=trial:10m rate=3r/m;
limit_req_zone $binary_remote_addr zone=sub:10m rate=30r/m;
```

---

## 8. Support

**Issues:** https://github.com/kereal/rs8kvn_bot/issues  
**Security:** security@kereal.me (example — configure real email)

**Before reporting:**
1. Check logs in `data/bot.log`
2. Verify configuration (`.env`)
3. Check that the ProviderSource URLs used by your Builders are reachable
4. Include version: `./rs8kvn_bot --version` or logs startup line

---

*This guide is maintained against the current codebase.*
