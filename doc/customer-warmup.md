# Customer Warm-up («Прогрев клиентов»)

Автоматические сообщения в Telegram-боте, привязанные к событиям жизненного
цикла пользователя: первый запуск бота, пробный период, окончание и истечение
платной подписки. Сценарии, тексты, кнопки, картинки, скидки и лимиты
настраиваются в браузерной админке (раздел «Прогрев клиентов»), без релиза.

Документ описывает только то, что реализовано в коде. Источник истины — код,
ссылки на него даны в каждом разделе.

## 1. Архитектура

```
Telegram /start ──► bot.Handler.recordWarmupContact ──► warmup_contacts
                                                            │
subscriptions, plans, journal_events (только чтение) ───────┤
                                                            ▼
scheduler.WarmupWorker (тик 10 мин) ──► service.WarmupService
   DueAnchors → Evaluate (условия, лимиты) → Claim (+купон) → RenderScenario
        │                                                    │
        ▼                                                    ▼
bot.WarmupSender (MessageSender: rate limit, метрики) ──► Telegram Bot API
        │
        ▼
RecordSendResult ──► warmup_deliveries / warmup_coupons / warmup_contacts

Admin UI (frontend/admin/src/warmup-ui.ts, warmup.ts)
   ──► /admin/api/warmup/… (internal/web/admin_warmup_api.go, adminauth)
   ──► service.WarmupService (warmup_admin.go) ──► database (warmup*.go)
```

| Слой | Файлы |
| --- | --- |
| Схема и seed | `internal/database/migrations/050_customer_warmup.{up,down}.sql` |
| БД | `internal/database/warmup.go`, `warmup_admin.go`, `warmup_deliveries.go` |
| Сервис | `internal/service/warmup.go`, `warmup_admin.go`, `warmup_delivery.go`, `warmup_registry.go`, `warmup_template.go` |
| Воркер | `internal/scheduler/warmup_worker.go` |
| Telegram-адаптер | `internal/bot/warmup_sender.go`, `warmup_hooks.go`; `MessageSender.SendChattable` |
| Admin API | `internal/web/admin_warmup_api.go` (маршрут `/admin/api/warmup`) |
| Admin UI | `frontend/admin/src/warmup-ui.ts`, `warmup.ts`, стили в `style.css` |
| Подключение | `cmd/bot/main.go`: `SetWarmupService`, `SetWarmupContacts`, `NewWarmupWorker` |

Прогрев **только читает** `subscriptions`, `plans`, `orders`/`journal_events`
и не меняет платежи, подписки и существующие напоминания об окончании подписки.

## 2. Таблицы (миграция 050)

| Таблица | Назначение |
| --- | --- |
| `warmup_settings` | одна строка (`id = 1`): глобальный выключатель, лимиты, catch-up, `version` |
| `warmup_scenarios` | сценарии; `key` уникален; `buttons` и `conditions` — JSON, проверяемый сервисом |
| `warmup_images` | картинки (байты в БД, ≤ 2 МБ, `sha256` уникален, `tg_file_id` — кэш Telegram) |
| `warmup_contacts` | пользователи, писавшие боту (`source = start`) и исторические (`source = backfill`) |
| `warmup_deliveries` | журнал отправок и состояние воркера; `UNIQUE(scenario_id, telegram_id, anchor_key)` — защита от дублей |
| `warmup_coupons` | персональные коды скидки; `code` уникален, один купон на отправку |

Миграция также:

* создаёт строку настроек со значениями по умолчанию: **`enabled = 0`**
  (прогрев глобально выключен), `max_per_day = 2`, `cooldown_hours = 24`,
  `catchup_hours = 6`;
* заполняет `warmup_contacts` из `subscriptions` с `source = 'backfill'`
  (исторические пользователи никогда не считаются «первым запуском бота»);
* создаёт 14 стандартных сценариев (раздел 3), 11 из них включены с
  `enabled_at` = момент миграции. Пока глобальный выключатель выключен,
  они ничего не отправляют; при его включении `enabled_at` переносится на
  момент включения (раздел 5).

`down`-миграция удаляет все шесть таблиц прогрева.

## 3. 14 стандартных сценариев

| # | key | Категория | Триггер | Момент / задержка | Вкл. | Условия |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `trial_not_activated` | trial | `bot_started` | через 12 ч | да | не было пробника, нет подписки, нет активной платной |
| 2 | `trial_not_connected_3h` | trial | `trial_activated` | через 3 ч | да | не подключался, нет активной платной |
| 3 | `trial_not_connected_24h` | trial | `trial_activated` | через 24 ч | да | не подключался, нет активной платной |
| 4 | `trial_ending` | trial | `trial_period_end` | за 24 ч | да | нет активной платной |
| 5 | `trial_no_purchase` | trial | `trial_activated` | через 2 дня | да | нет активной платной, не платил |
| 6 | `trial_ended` | trial | `trial_period_end` | сразу после | да | нет активной платной |
| 7 | `sub_expiring_48h` | subscription | `paid_expiry` | за 48 ч | нет | — |
| 8 | `sub_expiring_24h` | subscription | `paid_expiry` | за 24 ч | нет | — |
| 9 | `sub_expired` | subscription | `paid_expired` | сразу после | нет | нет активной платной |
| 10 | `winback_3d` | winback | `paid_expired` | через 3 дня | да | нет активной платной |
| 11 | `trial_winback_3d` | winback | `trial_period_end` | через 3 дня | да | нет активной платной, не платил |
| 12 | `winback_7d` | winback | `paid_expired` | через 7 дней | да | нет активной платной |
| 13 | `winback_14d` | winback | `paid_expired` | через 14 дней | да | нет активной платной |
| 14 | `winback_30d` | winback | `paid_expired` | через 30 дней | да | нет активной платной |

Сценарии 7–9 выключены, чтобы не дублировать существующие напоминания об
окончании подписки (3 дня / 1 день / 3 часа), которые продолжают работать.
Сценарий 8 (за 24 ч) включить нельзя, пока он совпадает с напоминанием «1d»
(см. раздел 6). У сценария 1 кнопка «Активировать пробник» — URL-кнопка без
адреса: она скрывается при отправке, пока в админке не задан URL страницы пробника.

Все поля любого сценария редактируются в админке. Новые сценарии создаются
в админке (это новые строки, схема не меняется). Удаления сценариев нет.

## 4. Триггеры (`service/warmup_registry.go`)

| `trigger_type` | Событие (якорь) | Источник | `anchor_key` | Моменты |
| --- | --- | --- | --- | --- |
| `bot_started` | первый живой `/start` | `warmup_contacts` (`source = start`, не заблокирован) | `start` | after |
| `trial_activated` | пробник привязан к Telegram | `journal_events` `trial_bound` | `trial:{sub_id}` | after |
| `trial_period_end` | срок, который был у пробника при привязке | `trial_bound`, `details.before.expires_at` | `trial_end:{sub_id}` | before, after |
| `paid_expiry` | `expires_at` активной платной подписки | `subscriptions` + `plans` (не free/trial) | `expiry:{sub_id}:{unix}` | before |
| `paid_expired` | платный срок истёк | `journal_events` `subscription_expired` (plan_kind paid) | `expiry:{sub_id}:{unix}` или `expired:{event_id}` | after |

Продление подписки меняет `expires_at`, а значит и `anchor_key`: новый срок —
новое событие.

## 5. Время: delay, enabled_at, catch-up window

* `due = якорь + delay` для `after`, `due = якорь − delay` для `before`.
  Единицы: `minutes`, `hours`, `days`; задержка 0–525600 и не больше 366 дней.
* Сценарий срабатывает, только если `due` попадает в окно
  **`[max(now − catchup, enabled_at), now]`** (`service.WarmupAnchorWindow`).
* `enabled_at` ставит сервер: при включении сценария и при изменении
  расписания включённого сценария (триггер, момент или задержка); при
  выключении он сбрасывается (`database.warmupEnabledAt`). Поэтому включение
  или деплой **не рассылает историю**: события с `due` раньше `enabled_at`
  не отправляются никогда.
* Включение **глобального** выключателя (переход OFF → ON в
  `database.UpdateWarmupSettings`) в той же транзакции ставит `enabled_at = now`
  всем включённым сценариям: события, ставшие due, пока прогрев был выключен
  (в том числе внутри catch-up window), не рассылаются. Флаги `enabled`,
  контент и `version` сценариев не меняются; правка настроек без перехода
  OFF → ON (ON → ON, ON → OFF, OFF → OFF) `enabled_at` не трогает.
* **Catch-up window** (`catchup_hours`, 1–72, по умолчанию 6 ч): насколько
  назад воркер «догоняет» пропущенные события (простой, рестарт). События
  старше окна не догоняются.

## 6. Воркер (`scheduler/warmup_worker.go`)

* Тик каждые **10 минут** (первый — сразу при старте), бюджет тика 8 минут,
  пачки по 200 событий; параллельный тик пропускается (`TryLock`).
* Перед тиком — housekeeping: зависшие `sending` старше 30 минут
  возвращаются в `retry`; `retry`, вышедшие за catch-up window, переводятся в
  `failed`.
* Если `warmup_settings.enabled = false` (так после миграции), тик выполняет
  только housekeeping и ничего не отправляет.
* Для каждого включённого сценария: `DueAnchors` → проверка журнала
  (`sent`/`failed`/`blocked`/`sending` повторно не обрабатываются) →
  `Evaluate` → `Claim` (строка `sending` + купон) → `RenderScenario` →
  `SendWarmup` → `RecordSendResult`.
* Одному пользователю — не больше одного сообщения прогрева за тик.
* Пропущенное по условиям или лимитам событие **не пишется** в журнал и может
  уйти позже, пока оно в окне.
* Воркер запускается только в процессе бота после успешной инициализации
  Telegram-бота (`cmd/bot/main.go`, шаг 10).

### Retry и исходы отправки (`service.RecordSendResult`)

| Ошибка Telegram | Исход |
| --- | --- |
| нет ошибки | `sent`, `sent_at`, `message_id` |
| сеть / 5xx | `retry`, пока есть попытки, иначе `failed` |
| 429 flood | как выше, и текущий тик останавливается |
| пользователь заблокировал бота | `blocked`, купон отозван, контакт исключён |
| чат недоступен (деактивирован, не найден) | `failed`, купон отозван, контакт исключён |
| прочие 4xx (запрос отвергнут) | `failed`, купон отозван |
| отмена контекста (shutdown) | `retry` |

Максимум **3 попытки**; ожидание перед повтором 10 мин, затем 20 мин
(удвоение, предел 1 ч).

### Blocked handling

Заблокировавший бота пользователь получает `warmup_contacts.blocked_at` и
исключается из всех сценариев (`Evaluate` → `blocked`, `bot_started` его не
выбирает). Следующий `/start` пользователя снимает исключение
(`TouchWarmupContact` обнуляет `blocked_at`). В админке такие отправки видны
со статусом «Заблокировал».

### warmup_contacts

Контакт записывается при каждом `/start` в личном чате, включая deep links
`trial_`/`share_` (`bot/command.go` → `recordWarmupContact`, best-effort:
ошибка не ломает `/start`). Обновляются username, язык и `last_seen_at`;
`first_seen_at` и `source` первого появления сохраняются.

## 7. Сообщение

### Telegram HTML (`service/warmup_template.go`)

Хранится Telegram HTML (не Markdown), отправляется с `parse_mode=HTML`.
Разрешены теги `b strong i em u ins s strike del tg-spoiler span(class) a(href)
code(class=language-…) pre blockquote(expandable)`; ссылки — `https://`,
`http://`, `tg://`. Проверяются незакрытые и неверно вложенные теги и
неэкранированные `< > &`. Лимит хранения 16 КБ; видимая длина после подстановки
— 4096 символов (1024, если есть картинка: тогда текст уходит подписью).
Перед каждой отправкой сценарий проверяется заново: невалидное сообщение не
отправляется (`failed`, причина `render: …`).

### Переменные

| Переменная | Значение | Доступна |
| --- | --- | --- |
| `{{username}}` | `@username`, без него — «друг» | всегда |
| `{{telegram_id}}` | числовой ID | всегда |
| `{{plan_name}}` | название тарифа | `paid_expiry`, `paid_expired` |
| `{{trial_days}}` | дни пробника (число) | всегда (из настроек пробника) |
| `{{trial_duration}}` | длительность словами («3 дня», «24 часа») | всегда |
| `{{expire_date}}` | дата окончания, `ДД.ММ.ГГГГ ЧЧ:ММ МСК` | `trial_period_end`, `paid_expiry`, `paid_expired` |
| `{{discount_code}}` | персональный код | только со скидкой |
| `{{discount_percent}}` | процент скидки | только со скидкой |

Неизвестная или недоступная для триггера переменная отклоняется при
сохранении. При включённой скидке текст обязан содержать `{{discount_code}}`.

### Кнопки

До 8 кнопок; текст 1–64 символа без переменных. `same_row` ставит кнопку в
строку предыдущей (первая всегда начинает строку).

* `url` — `https://`, `http://` или `tg://`;
* `web_app` (Mini App) — только `https://`;
* `callback` — только из белого списка существующих действий бота
  (`menu_subscription`, `buy_premium_list`, `create_subscription`,
  `back_to_start`, `menu_help`, `menu_support`, `menu_donate`, `share_invite`,
  `menu_documents`); произвольный `callback_data` не принимается.

URL-кнопка или Mini App без адреса сохраняется, но **не отправляется**
(предупреждение `button_url_missing`). Callback, нажатый на сообщении с
картинкой, бот обрабатывает новым текстовым сообщением, картинка не меняется
(`bot/warmup_hooks.go`).

### Картинки

JPEG или PNG до 2 МБ. Тип определяется по байтам, не по клиенту; картинка
должна декодироваться и подходить Telegram (сумма сторон ≤ 10000, соотношение
сторон ≤ 1:20). Одинаковые байты дают ту же картинку (по `sha256`). Первая
отправка загружает байты, затем используется закэшированный `tg_file_id`;
если Telegram его отвергает, картинка загружается заново. Удалить картинку,
пока её использует сценарий, нельзя (`409 image_in_use`).

## 8. Условия, cooldown, лимиты

Условия (`conditions`, JSON): отсутствующий флаг — «не важно».

| Ключ | Факт |
| --- | --- |
| `had_trial` | был `trial_bound` |
| `has_subscription` | есть любая подписка |
| `has_active_paid` | активная платная, не истёкшая |
| `ever_paid` | был `payment_succeeded` |
| `connected` | VPN-клиент хотя бы раз забрал конфиг (`subscriptions.last_request`) |
| `languages` | префикс `language_code` (`ru` подходит к `ru-RU`), до 20 кодов; пусто — любой |

Лимиты (`service.Evaluate`), окна скользящие 24 ч, считаются `sent` и `sending`:

* `max_per_day` (глобально, 1–20) — сообщений прогрева одному пользователю за 24 ч;
* `cooldown_hours` (глобально, 0–720) — минимум между сообщениями прогрева
  одному пользователю; сценарий может задать свой (`cooldown_hours` сценария,
  `null` — глобальный);
* `daily_cap` сценария (0 — без лимита, до 100000) — сообщений этого
  сценария всем пользователям за 24 ч.

Наложение на напоминания об окончании подписки: включённый сценарий
`paid_expiry` + `before` с задержкой, равной 72 ч, 24 ч или 3 ч, отклоняется
(`409 duplicates_expiry_reminder`). При других задержках показывается
предупреждение, что старые напоминания продолжают работать.

## 9. Купоны

Если у сценария `discount_percent > 0`, при claim создаётся персональный код
`ПРЕФИКС-XXXXXXXX` (8 символов из алфавита без похожих символов,
`crypto/rand`; без префикса — `XXXXXXXX`), срок `coupon_ttl_hours`
(1–8760, по умолчанию 48). Повтор отправки использует тот же купон.
Купон отзывается (`revoked`), если сообщение так и не доставлено.
Статус в админке: `issued` («Действует»), `expired` (вычисляется по
`expires_at`), `revoked`, `redeemed`.

> **Купон пока не применяется автоматически при оплате.** Связи с платежами
> нет: `redeemed_at` и `order_id` всегда пустые, статус `redeemed`
> зарезервирован под будущую интеграцию.

Предпросмотр генерирует код в том же формате (с префиксом сценария), помечает
его как тестовый (`discount_code_test: true`) и нигде не сохраняет.

## 10. Admin API (`/admin/api/warmup`)

Все маршруты — за `adminauth`: сессия (`__Host-` cookie), для изменяющих
запросов — `Origin`, равный `SITE_URL`, и `X-CSRF-Token`. Тело — JSON с
`DisallowUnknownFields`. Без `SetWarmupService` маршруты отвечают 503.

| Метод и путь | Назначение |
| --- | --- |
| `GET /admin/api/warmup` | обзор: settings, categories, scenarios, summary, registry |
| `GET /settings` · `PUT /settings` | настройки (`version`, `request_key`) |
| `GET /scenarios` | список (фильтры `category`, `trigger`, `enabled`, `order`) |
| `POST /scenarios` | создание (`request_key`, `key` `^[a-z][a-z0-9_]{2,63}$`, поля сценария) |
| `GET /scenarios/{id}` · `PATCH /scenarios/{id}` | чтение; частичное изменение (`version`, `request_key`) |
| `POST /scenarios/{id}/preview` | предпросмотр сохранённого сценария с несохранённым patch |
| `POST /preview` | предпросмотр черновика (форма создания) |
| `POST /images` | загрузка (`request_key`, `data` в base64) |
| `GET /images/{id}` · `DELETE /images/{id}` | картинка (`nosniff`, inline); удаление (`request_key`) |
| `GET /deliveries` | журнал: `scenario_id`, `category`, `status`, `telegram_id`, `from`, `to`, `coupon`, `limit`, `offset` |
| `GET /coupons` | купоны: `scenario_id`, `status`, `telegram_id`, `code`, `limit`, `offset` |

Страница по умолчанию — 50 записей.

**version / request_key.** Сохранения сценария и настроек — с оптимистической
блокировкой: `version` загруженной версии, при расхождении
`409 version_conflict`. Каждая мутация идёт через `runAdminConfigMutation`:
пишется audit log, тот же `request_key` с тем же телом повторяет ответ
(`replayed: true`), с другим телом — `409 request_key_conflict`.

Ошибки валидации: `{error, field, reason, detail}`; коды `validation_error`,
`invalid_html`, `invalid_variable`, `invalid_button`, `invalid_trigger`,
`callback_not_allowed`, `image_invalid_type`, `image_too_large` (413),
`image_not_found`, `image_in_use`, `scenario_not_found`, `scenario_key_taken`,
`duplicates_expiry_reminder`.

Предпросмотр рендерит тем же `RenderScenario`, что и воркер, для тестового
получателя; он **ничего не отправляет и не пишет** (ни отправок, ни купонов).

## 11. Admin UI («Прогрев клиентов»)

Маршруты: `#/warmup`, `#/warmup/create`, `#/warmup/scenario/{id}`,
`#/warmup/settings`, `#/warmup/deliveries`, `#/warmup/coupons`.

* **Сценарии** — глобальный ON/OFF, сводка (сценарии, активные, отправлено
  сегодня и за 7 дней, retry/failed/blocked за 24 ч, купоны), карточки по
  трём категориям с кнопками «Редактировать» и «Вкл/Выкл».
* **Редактор / создание** — основные поля, триггер с описанием словами,
  редактор Telegram HTML (панель форматирования, счётчик, переменные из
  registry), картинка, конструктор кнопок (callback — только выбор из списка),
  поле URL страницы пробника у сценария 1, условия из registry, скидка,
  cooldown и лимит, предпросмотр в стиле Telegram. Новый сценарий создаётся
  **выключенным**. Сохранение отправляет только изменённые поля.
* **Настройки**, **История отправок** (фильтры, постраничная загрузка),
  **Купоны**.
* Все запросы — через сессию админки (CSRF); при неопределённой ошибке
  (сеть, 5xx) повтор идёт с тем же `request_key`. При конфликте версий
  чужие изменения не перезаписываются: «Сценарий был изменён в другой
  сессии. Обновите данные.» Несохранённая форма спрашивает подтверждение
  при уходе.

Правила проверяет сервер; клиент ловит только очевидные ошибки ввода.

## 12. Известные ограничения

* Купоны не применяются при оплате (раздел 9).
* Контакт записывается только по `/start`; другие сообщения боту `last_seen_at`
  не обновляют.
* Пользователи до миграции (`backfill`) не получают `bot_started`.
* Сценарии нельзя удалить, только выключить.
* Условий по тарифу или построителю нет — только ключи из раздела 8.
* Имя файла картинки сервер не хранит (в админке оно видно только в сессии загрузки).
* `trial_days`/`trial_duration` берутся из текущих настроек пробника, а не из
  срока конкретного пробника пользователя.
* **После миграции прогрев глобально выключен** (`warmup_settings.enabled = 0`):
  воркер работает, но ничего не отправляет и не пишет в журнал. 11 сценариев
  при этом остаются включёнными (3 выключены), поэтому прогрев запускается
  одним переключателем в админке.
* При включении глобального выключателя `enabled_at` сценариев не меняется
  (он остаётся моментом миграции). Поэтому первый тик после включения
  догонит события с `due` в пределах catch-up window (`catchup_hours`, по
  умолчанию 6 ч), накопившиеся, пока прогрев был выключен. Более старые
  события не отправляются.

## 13. Тесты

* Go: `internal/{database,service,scheduler,bot,web}/*warmup*_test.go`.
* Frontend unit: `frontend/tests/admin-warmup.test.ts` (jsdom, контракт API).
* Browser: `frontend/admin/browser/warmup.spec.ts` (`npm run test:browser:admin`).
