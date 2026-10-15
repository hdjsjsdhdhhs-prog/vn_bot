> **Внимание:** записи ниже о панелях 3x-ui/proxman, узлах (`nodes`, `subscription_nodes`), синхронизации, `internal/vpn`, `internal/xui` и панельных уведомлениях трафика — **исторические**: подсистема удалена миграцией `051_remove_legacy_nodes`. Актуальная модель: подписка — локальное состояние, содержимое `/sub` собирает Subscription Builder из ProviderSource (см. `doc/architecture.md`).

# rs8kvn_bot — Telegram Bot для раздачи VLESS-подписок

## Назначение
Telegram-бот для продажи и управления VLESS+Reality+Vision подписками. Подписка — локальное состояние; содержимое `/sub` собирает Subscription Builder из каталогов ProviderSource (клиентов на панелях 3x-ui/proxman больше нет).
Production-grade: миграции, мониторинг, rate-limiting, graceful shutdown.

## Subscription expiry reminders
- Окна: 3 дня, 1 день, 3 часа до истечения подписки (`internal/service/subscription_reminders.go`)
- Фоновая задача: `SubscriptionReminderWorker` запускается раз в 30 минут, сканирует активные paid-подписки в окне ±30 минут от целевого срока.
- Напоминание отправляется ровно один раз на окно: `reminders_sent` bitmask в `subscriptions` + атомарный `ClaimReminder`/`ReleaseReminder` по `expires_at`.
- При `RenewSubscription` битмасп сбрасывается в 0, чтобы новое поколение подписки получило напоминания заново.
- Исключаются free и trial планы (`GetSubscriptionsExpiringInRange`).
- Пушечные метрики: `SubscriptionRemindersTotal` (`name`, `status`), `SubscriptionReminderRunsTotal` (`internal/metrics/metrics.go`).
- Миграция 030: добавлено поле `subscriptions.reminders_sent`.
- Триггер жизненного цикла: `SubscriptionReminderWorker` запускается в `cmd/bot/main.go` (startBackgroundWorkers).

## Subscription expiration
- Paid-подписки автоматически помечаются `expired`, если истёк `expires_at` и план не free/trial (`GetExpiredPaidSubscriptions`).
- Таймер истечения хранится в БД на момент Create.

## Subscription traffic notifications — удалено (историческая справка)
Воркер панельных уведомлений трафика (`SubscriptionTrafficWorker`, `ProcessTrafficNotifications`, `subscriptions.traffic_reminders_sent`, `plans.traffic_limit`) удалён миграцией 051 вместе с подсистемой VPN-узлов. Детали старой реализации: `fixes/2026-08-27-subscription-traffic-worker` (историческая запись).

## Типы подписок и фильтр планов
- Trial-подписки теперь **не** считаются paid при автоматическом истечении и не получают напоминания: `GetExpiredPaidSubscriptions` и `GetSubscriptionsExpiringInRange` исключают `plans.name = TrialPlanName`.

## Ключевые фичи
- Планы (trial/free/paid) без `duration`, без `price` (duration/price вынесены в products)
- `subscriptions.client_id` и `subscriptions.subscription_id` имеют `NOT NULL UNIQUE` enforcement через migration 023 и GORM-модель
- Subscription Builder + ProviderSource: содержимое `/sub` собирается из каталогов источников; панельной провизии, очереди синхронизации и fallback через `nodes` нет (миграция 051)
- Реферальная система: in-memory cache + периодический sync
- Админ-уведомления, heartbeat, health endpoints (`/healthz`, `/readyz`)
- Hysteria2/Clash port-hopping нормализация: `firstPortFromPorts` берёт первый конкретный порт из `ports`/диапазона
- Ротация логов (zap), ежедневные бэкапы БД (имя `rs8kvn.db.backup.YYYYMMDD_HHMMSS`)
- Sentry, rate-limiting per-user
- O(1) LRU кэш подписок (RLock для concurrent reads)
- Subscription status check в `/sub/{subID}` — revoked/expired → 404
- Subscription expiration хранится в БД на момент Create

## Subserver share-link conversion
- Поддерживаемые схемы: `vmess://`, `trojan://`, `ss://`, `hysteria://`, `hysteria2://`, `hy2://`, `tuic://`
- ALPN list→comma string (Clash YAML list → v2rayN share-link param)
- Shadowsocks SIP002 plugin: `obfs`→`obfs-local` alias + `plugin-opts` serialisation
- VLESS xhttp/splithttp normalisation + `mode` param
- `security=tls` для Trojan/VLESS (3x-ui flat format)
- IPv6-safe addresses через `net.JoinHostPort`
- VLESS/Trojan/Hysteria/TUIC: TLS flag нормализуется так, чтобы `security=tls` не потеряться при `tls` nil
- VMess port as string для v2rayNG
- Clash Meta/V2rayN `packetEncoding` support через `setPacketEncoding` (`none`/`packet`/`xudp`)

## Access log
- Space-separated format, async writer с bounded queue (1024 records)
- Fields: timestamp, method, URI, status, success/total, client IP, hwid, os, ver, model, user-agent
- Quote-wrapping для значений с пробелами
- `statusRecorder` tracks per-request source success/total counts

## Broadcast (рассылка `/broadcast`)
- Админский флоу (сессия с TTL 15 мин): `/broadcast` → **название** → **текст** (MarkdownV2, спецсимволы авто-экранируются `utils.EscapeMarkdownV2`) → превью → **фильтры** (`bfilter_*`: plan paid/free, status active/all/revoked, дата регистрации 90/180/365, inactive 0/30/90 дн., ever_paid) → подтверждение (`broadcast_confirm` → счётчик получателей → `broadcast_final_confirm`).
- **Durable BroadcastWorker** (`internal/bot/broadcast_worker.go`): тик 15 с, таймаут кампании 5 мин, батчи по 100 (concurrency 10), независим от update-loop, переживает рестарт процесса (кампании `scheduled|running` резюмируются через `retry_at`).
- Аудитория снапшотится в JSON **`recipients_state`** той же строки `broadcasts` (миграция 036): snapshot + per-recipient `pending|sending|sent|blocked|unreachable|failed`, lease 2 мин, восстановление после краха (`RecoverStaleBroadcastRecipients`). Строка `broadcasts`: `filters` (JSON), статус (`scheduled|running|completed|failed|canceled`, CHECK-constraint), `planned_at`/`started_at`/`finished_at`, счётчики `recipients_total`/`sent_count`/`blocked_count`/`unreachable_count`/`failed_count`, `delivery_report` (JSON: `delivered`/`blocked`/`unreachable` — списки telegram_id, `errors` — `{telegram_id, error}`), `last_error`/`retry_at`/`retry_count`.
- **Ретраи**: per-message 2 попытки (300/600 мс); `blocked` — только явная блокировка бота («bot was blocked by the user»), deactivated/недоступный/удалённый чат (вкл. «chat not found») → `unreachable`, не ретраются. Инфраструктурные/персистентные сбои кампании → `retry_at` с exponential backoff (5 с → 15 мин), статус остаётся `running`.
- Управление: кнопки «📋 Детали рассылки» (`broadcast_details_<id>`), «⏹ Отменить» (`broadcast_cancel_<id>`), «🔁 Повторить ошибки» (`broadcast_retry_<id>`) в карточке; история — `/broadcasts` (10 последних).
- Отчёт админу: счётчики `Отправлено/Заблокировали/Недоступны/Ошибок`; полные списки ID остаются в `delivery_report`. `UpdateBroadcast` обновляет только изменяемые поля, карточку не затирает.
- **Логирование**: `Info` — создание/старт/завершение/отмена/retry кампании и черновика, плановый resume после таймаута (`DeadlineExceeded`/`errBroadcastIncomplete`); `Warn` — фоновые сбои (загрузка runnable, launch failed, markCampaignFailed, report load); `Error` — паника получателя, сбой scheduleRetry/создания, ошибки cancel/retry из callback.

## News: Subscription Traffic Worker (2026-08)
- Добавлен `SubscriptionTrafficWorker` + `SubscriptionService.ProcessTrafficNotifications`.
- Три уведомления с дедупликацией по битовой маске: 90%, исчерпан (доступ приостановлен) и сброс+авто-включение.
- Продающие тексты с кнопкой `buy_premium_list` для 90%/исчерпан; у сброса — без продажи.
- Миграция 038 `subscriptions.traffic_reminders_sent`.
- Тесты: xui (Enable true/false + декодирование), БД (claim/release/выборка с лимитом), service (4 сценария + кнопка Premium), scheduler (worker: итерация/skip trial/continue-on-error/repo error). До этого был детальный обзор истории, удалён ненужный legacy-коллапс, `totalGB` оставлен как есть.

## 3x-ui v3.7.0 auto-renew (2026-08-29)
- Уточнён контракт панели по фактическому payload: `reset` — интервал продления, `resetDay=0` — rolling-режим, `resetMax=0` — без лимита, `trafficReset=monthly`, `trafficResetDay=1` — ежемесячный сброс 1-го числа.
- **Проверено по исходникам панели (main, ~v3.7.0)**: `model.Client` — `trafficReset ∈ {never,hourly,daily,weekly,monthly}` (пустая строка принимается и нормализуется в `never`), `resetDay ∈ [0,31]` (0 = rolling), `resetMax ≥ 0` (0 = безлимит); `autoRenewClients` (`internal/web/service/inbound_traffic.go`) продлевает rolling-клиентов на `reset*86400000`, сбрасывает up/down; клиенты с `reset>0`/`reset_day>0` не удаляются purge'ом (`depletedClientsClause`).
- `xui.ClientRequest`, `vpn.SubscriptionProvision` и адаптер 3x-ui передают эти поля при add/update; free-подписки получают профиль, trial — `reset=0` без автопродления.
- Поля профиля отправляются **условно**: только когда задан `TrafficReset != ""`. Панель при update **не мержит** — отсутствующий `trafficReset` пишется как `never` (`normalizeClientTrafficReset`), поэтому каждый update лимитированного плана обязан слать полный профиль.
- `/clients/traffic/{email}` не отдаёт `trafficReset/trafficResetDay`, поэтому `reenableAndNotify` читает профиль через новый `xui.Client.GetClientByEmail` (inbound settings) — best-effort: при ошибке Warn и продолжение без профиля.
- Fallback восстановления срока — только в `xui.doUpdateClient` (guard `resetDays > 0`): истёкший free-клиент получает срок now + 30 дней; trial никогда не продлевается; sync-слой срок не двигает.
- Существующих клиентов пересоздавать не нужно; их достаточно один раз сохранить с указанными параметрами панели.
- Регрессионные тесты: JSON payload (add/update), условная отправка полей, неизменность неистёкшего срока, восстановление истёкшего, проброс профиля в traffic-воркере через `GetClientByEmail`, тесты самого `GetClientByEmail` (modern/legacy settings), sync не продлевает trial. `go test ./...` проходит (2124).

## Последние изменения (2026-08-26, ветка feat/broadcast-campaigns)
- **ResetTraffic при смене тарифа**: `vpn.Client.ResetTraffic` (3x-ui POST `/panel/api/clients/resetTraffic/{email}`; proxman/fetch — no-op). Вызывается в `processPendingUpdate` после update, **best-effort** (Warn при ошибке, не блокирует переход в `active`).
- **CleanupExpiredTrials двухфазный**: `ClaimExpiredTrials` (атомарный claim) → deprovision внешних клиентов → `DeleteClaimedTrial` только после успеха; сбой оставляет строку для ретрая, ошибки агрегируются (`errors.Join`).
- **BindTrial**: панель обновляется ДО DB-bind; если DB-bind проиграл гонку и строка всё ещё не привязана (`telegram_id < 0`) — панель откатывается к анонимной `trial_`-идентичности; при неопределённости (check error) rollback не делается.
- **ReconcileOrphanedClients**: активная non-trial подписка без node bindings — пересоздаётся `pending_add`-очередь (`ensureSubscriptionNodes`), а не ревокация.
- **Subserver**: кэш-ревалидация **fail-closed** (Error + 5xx вместо stale после revoke/chargeback); лимит тела 2 MiB (`config.MaxResponseSize`); `analyticsMu` сериализует `UpdateDevices`/`UpdateIPs`; base64-агрегация поддерживает RawStdEncoding (без padding) и разбивает по строкам.
- **Web**: check-and-record trial rate-limit под `trialRateMu`; `SecurityHeadersMiddleware` (nosniff/DENY/no-referrer/Permissions-Policy).
- **Тесты**: `internal/bot/broadcast_admin_flow_test.go` (callback ID, cancel/retry, фильтры, форматтеры), `internal/xui/reset_traffic_test.go`, sync ResetTraffic best-effort, BindTrial rollback, CleanupExpiredTrials deprovision-failure, ReconcileOrphaned queue recovery.
- **Документация**: добавлен краткий README по флоу рассылок — `doc/broadcast.md` (флоу админа, воркер доставки, управление, указатели на код); ссылка добавлена в «Project Documentation» корневого README.md.

## Стек
- **Go 1.25** (go.mod)
- **Bot**: telegram-bot-api/v5
- **DB**: SQLite + GORM + golang-migrate (embedded, current schema migrations are applied at startup)
- **Logging**: Zap (с ротацией)
- **Tests**: testify
- **QR**: piglig/go-qr
- **Errors**: getsentry/sentry-go

## Subserver
- Clash/Mihomo конвертер покрывает VMess, VLESS, Trojan, Hysteria2, TUIC, Shadowsocks SIP002.
- Транспортная нормализация сведена в `normaliseTransportNetwork` + `setPacketEncoding`.

## Scheduler
- Backup ежедневно, trial cleanup сразу при старте и затем каждые 3 часа, expire — каждый час, reminders — каждые 30 минут (`SubscriptionReminderWorker`), provider fee — каждые 5 минут, warm-up — каждые 10 минут.

## Базовый worker set
- Backup ежедневно, trial cleanup каждые 3 часа, expire — каждый час, reminders — каждые 30 минут, provider fee — каждые 5 минут, warm-up — каждые 10 минут.

## Структура
```text
cmd/bot/                     — точка входа, graceful shutdown, lifecycle workers
internal/bot/                — handlers, commands, callbacks, referral cache, keyboard/menu
internal/database/           — GORM-модели, embedded migrations, транзакции
internal/service/            — SubscriptionService + OrderService + reminders + builders
internal/interfaces/         — контракты (SubscriptionRepository, reminder repr.)
internal/testutil/           — моки (db slice fakes, testutil helpers)
internal/utils/              — time, UUID, QR, Markdown EscapeMarkdownV2/EscapeMarkdown
internal/config/             — загрузка, валидация
internal/logger/             — zap setup
internal/heartbeat/          — мониторинг
internal/backup/             — ежедневные бэкапы БД с WAL checkpoint
internal/scheduler/          — backup + trial cleanup + expiry reminders
internal/ratelimiter/        — per-user token bucket
internal/web/                — /healthz, /readyz, /i/{code}, /sub/{subID}, /metrics, /payment/callback, ... + singleflight
internal/subserver/           — кэш + merge подписок; Clash/Mihomo нормализация share-ссылок; optional access log
internal/metrics/            — Prometheus/обёртка; напоминания имеют выделенные счётчики
```

## Bootstrap для AI-агента
При старте сессии **обязательно**:
1. `activate_project("rs8kvn_bot")`
2. Прочитать памяти: `project_overview` (этот), `git-workflow`, `architecture`, `code_style`
3. Папки `xui/*`, `vpn/*`, `subscription-nodes/*` — исторические (подсистема удалена миграцией 051), для текущей работы не нужны
4. При работе с reminders/subscriptions — `architecture`; фиксы — `fixes/*` (исторические)
5. **Отвечать на русском** (AGENTS.md)
6. **Не удалять** legacy-код без явного запроса

---
## Подробности
- Архитектура: см. `architecture`
- Стиль кода: см. `code_style`
- Git workflow: см. `git-workflow`
- Дорожная карта: см. `roadmap`
- Тесты: см. `test-info`
- Аудиты: см. `audit/*`
- Исторические фиксы: см. `fixes/*`
- Рассылки (кратко): см. `doc/broadcast.md`
- Traffic-уведомления: см. `fixes/2026-08-27-subscription-traffic-worker`
- x-ui протокол (историческая справка, подсистема удалена): см. `xui/*`
- Subscription Nodes (историческая справка, подсистема удалена): см. `subscription-nodes/*`
