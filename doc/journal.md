# Журнал событий (admin «Журнал»)

Журнал — append-only история действий пользователей, администраторов и системы
по пользователям, подпискам и платежам. Страница админки `#/audit` читает его
через `GET /admin/api/journal`; записи создаёт только backend.

## Хранение

Таблица `journal_events` (миграция `047_journal_events`):

| Поле | Назначение |
|---|---|
| `created_at` | момент события (UTC) |
| `event_type` | тип события, см. ниже |
| `outcome` | `success` / `failed` / `rejected` |
| `actor`, `actor_name` | `user` / `admin` / `system`; логин администратора или платёжный провайдер (`platega`, `telegram_stars`, `bot`) |
| `telegram_id`, `username` | снимок пользователя (`< 0` — анонимная пробная) |
| `subscription_id`, `plan_id`, `plan_name`, `plan_kind` | подписка и тариф, к которому относится событие; `plan_kind` = `free` / `trial` / `paid` |
| `order_id`, `amount_cents`, `currency` | для платёжных событий |
| `description` | понятное описание на русском |
| `details` | JSON: `before` / `after` (статус, срок, тариф), `days`, `provider`, `error_code`, `backfill` |
| `dedup_key` | UNIQUE-ключ идемпотентности (NULL допускается многократно) |

- Внешних ключей нет: история переживает `/del` и очистку пробных подписок.
- `UPDATE` и `DELETE` запрещены триггерами — журнал append-only.
- Миграция заполняет историю из уже существующих фактов (регистрации,
  оплаченные заказы, admin-аудит подписок) с теми же `dedup_key`, что и живой
  код; такие записи помечены `details.backfill = true`.

## Где создаются события

Запись идёт в той же транзакции, что и изменение состояния
(`database.recordJournal`). Вставка best-effort в SAVEPOINT: сбой записи
журнала логируется (`Warn`) и никогда не откатывает и не блокирует
бизнес-операцию (оплату, продление и т. п.).

| Тип | Источник | Actor |
|---|---|---|
| `user_registered` | `CreateSubscription` (telegram_id > 0) | user |
| `trial_started` | `CreateTrialSubscription` | user |
| `trial_bound` | `BindTrialSubscription` (trial → free) | user |
| `trial_expired` | `ClaimExpiredTrials` | system |
| `subscription_reconnected` | реанимация revoked-подписки при `/start` | user |
| `free_activated` | `/setplan free`, `DowngradeToFreePlan` | admin / system |
| `paid_activated` | `ConfirmOrderPaidCAS` (Platega и Stars) — покупка | user |
| `subscription_renewed` | оплата того же тарифа, Mini App renew, admin renew | user / admin / system |
| `plan_changed` | `/setplan <платный тариф>` | admin |
| `expiry_changed` | admin change_expiry | admin |
| `subscription_expired` | `ExpireSubscriptionWithPlanCAS`, `ExpireProviderSubscription` | system |
| `subscription_disabled` / `subscription_enabled` | admin disable / enable | admin |
| `subscription_revoked` | `/del` (фаза revoke), orphan-reconcile | admin / system |
| `payment_succeeded` | `ConfirmOrderPaidCAS` | user |
| `payment_failed` | `CancelOrderCAS`, chargeback неоплаченного заказа | system |
| `payment_refunded` | chargeback оплаченного заказа | system |

Отклонённые admin-мутации пишутся с `outcome = rejected`.

## Отсутствие дублей

- Событие пишется только при фактическом переходе состояния (CAS
  `RowsAffected > 0`); повторные колбэки провайдера, повторный `/start`,
  replay admin request key и повторные проходы воркеров ничего не добавляют.
- Ключи: `subscription_created:<id>`, `trial_bound:<id>`, `trial_expired:<id>`,
  `order_paid:<id>`, `order_activated:<id>`, `order_canceled:<id>`,
  `admin_audit:<audit_id>`, `subscription_expired:<id>:<unix expiry>`.
- Чтение (`GET`) журнал не меняет: обновление страницы не создаёт записей.

## API

`GET /admin/api/journal` — страница событий, от новых к старым
(`created_at DESC, id DESC`). Параметры: `q` (Telegram ID / ID подписки —
точное совпадение, иначе подстрока username), `type`, `plan_kind`,
`from` (включительно), `to` (исключительно) в RFC 3339, `limit`, `offset`.
Ответ: `{events, total, limit, offset}`; `details` — JSON-объект.

`GET /admin/api/journal/{id}` — одно событие. Изменяющих методов нет (405).

## Проверки

- `go test ./internal/database/ -run 'TestJournal'`
- `go test ./internal/service/ -run 'TestJournal|TestAdminService_ListJournal'`
- `go test ./internal/web/ -run TestAdminAPI_Journal`
- `npx playwright test -c playwright.admin.config.ts journal.spec.ts`
