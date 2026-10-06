-- Migration: 050_customer_warmup
-- Description: Customer warm-up (Admin -> "Прогрев клиентов"): automatic
-- Telegram messages triggered by user lifecycle events.
--
-- The feature only READS subscriptions, plans, orders and journal_events; it
-- owns these tables and nothing else:
--
-- * warmup_settings   one row (id = 1): global switch and per-user limits.
-- * warmup_scenarios  one row per scenario. trigger_type names an event of the
--                     code registry (service/warmup_triggers.go); new
--                     scenarios are new rows, the schema does not change.
--                     buttons and conditions are JSON validated by the service.
-- * warmup_images     uploaded pictures (max 2 MB). Bytes live in the database
--                     because every release has its own data directory and
--                     only the database file is carried over; tg_file_id
--                     caches the Telegram file after the first send.
-- * warmup_contacts   Telegram users seen by the bot (/start and later
--                     activity). The bot has no users table and a /start
--                     without a subscription was not recorded anywhere.
--                     source = 'start'    first seen live by /start: its
--                                         first_seen_at is a real first start;
--                     source = 'backfill' restored below from subscriptions:
--                                         never a "bot started" event.
-- * warmup_deliveries one row per (scenario, user, anchor event): the send
--                     journal, the worker state and the duplicate guard
--                     (UNIQUE).
-- * warmup_coupons    personal discount codes issued with a message. They are
--                     NOT applied to payments yet (redeemed_at/order_id stay
--                     NULL until the payment integration exists).
--
-- The warm-up is globally OFF after this migration (warmup_settings.enabled
-- = 0): nothing is sent until the admin switches it on in the admin UI. The
-- standard scenarios keep their own switches (11 on, 3 off, see below).
--
-- No mass sending after a deploy: a scenario fires only when event + delay is
-- inside the catch-up window AND not earlier than its enabled_at, so history
-- before the switch-on is never sent.
-- created_at/updated_at use the Go/mattn UTC format like journal_events.

CREATE TABLE warmup_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT 0,
    max_per_day INTEGER NOT NULL DEFAULT 2 CHECK (max_per_day BETWEEN 1 AND 20),
    cooldown_hours INTEGER NOT NULL DEFAULT 24 CHECK (cooldown_hours BETWEEN 0 AND 720),
    catchup_hours INTEGER NOT NULL DEFAULT 6 CHECK (catchup_hours BETWEEN 1 AND 72),
    version INTEGER NOT NULL DEFAULT 1,
    updated_at DATETIME NOT NULL
);

CREATE TABLE warmup_images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    mime TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 2097152),
    sha256 TEXT NOT NULL,
    data BLOB NOT NULL,
    tg_file_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX idx_warmup_images_sha256 ON warmup_images(sha256);

CREATE TABLE warmup_scenarios (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN ('trial', 'subscription', 'winback')),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    trigger_type TEXT NOT NULL,
    timing TEXT NOT NULL DEFAULT 'after' CHECK (timing IN ('after', 'before')),
    delay_value INTEGER NOT NULL CHECK (delay_value BETWEEN 0 AND 525600),
    delay_unit TEXT NOT NULL CHECK (delay_unit IN ('minutes', 'hours', 'days')),
    enabled BOOLEAN NOT NULL DEFAULT 0,
    enabled_at DATETIME,
    message_html TEXT NOT NULL,
    image_id INTEGER REFERENCES warmup_images(id),
    buttons TEXT NOT NULL DEFAULT '[]',
    conditions TEXT NOT NULL DEFAULT '{}',
    cooldown_hours INTEGER CHECK (cooldown_hours IS NULL OR cooldown_hours BETWEEN 0 AND 720),
    daily_cap INTEGER NOT NULL DEFAULT 0 CHECK (daily_cap BETWEEN 0 AND 100000),
    discount_percent INTEGER NOT NULL DEFAULT 0 CHECK (discount_percent BETWEEN 0 AND 100),
    coupon_template TEXT NOT NULL DEFAULT '',
    coupon_ttl_hours INTEGER NOT NULL DEFAULT 48 CHECK (coupon_ttl_hours BETWEEN 1 AND 8760),
    position INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CHECK (enabled = 0 OR enabled_at IS NOT NULL)
);

CREATE UNIQUE INDEX idx_warmup_scenarios_key ON warmup_scenarios(key);
CREATE INDEX idx_warmup_scenarios_category ON warmup_scenarios(category, position, id);

CREATE TABLE warmup_contacts (
    telegram_id INTEGER PRIMARY KEY CHECK (telegram_id > 0),
    username TEXT NOT NULL DEFAULT '',
    language_code TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'start' CHECK (source IN ('start', 'backfill')),
    first_seen_at DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL,
    blocked_at DATETIME
);

CREATE INDEX idx_warmup_contacts_first_seen ON warmup_contacts(source, first_seen_at);
CREATE INDEX idx_warmup_contacts_last_seen ON warmup_contacts(last_seen_at);

CREATE TABLE warmup_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scenario_id INTEGER NOT NULL REFERENCES warmup_scenarios(id),
    scenario_name TEXT NOT NULL,
    telegram_id INTEGER NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    anchor_key TEXT NOT NULL,
    anchor_at DATETIME NOT NULL,
    due_at DATETIME NOT NULL,
    -- sending: claimed by the worker; sent: delivered; retry: transient send
    -- error, claimed again after a backoff; failed: permanent error or retries
    -- exhausted; blocked: the user blocked the bot.
    status TEXT NOT NULL CHECK (status IN ('sending', 'sent', 'retry', 'failed', 'blocked')),
    attempts INTEGER NOT NULL DEFAULT 1,
    error TEXT NOT NULL DEFAULT '',
    message_id INTEGER,
    coupon_id INTEGER,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    started_at DATETIME NOT NULL,
    sent_at DATETIME
);

CREATE UNIQUE INDEX idx_warmup_deliveries_dedup ON warmup_deliveries(scenario_id, telegram_id, anchor_key);
CREATE INDEX idx_warmup_deliveries_user ON warmup_deliveries(telegram_id, created_at);
CREATE INDEX idx_warmup_deliveries_created ON warmup_deliveries(created_at, id);
CREATE INDEX idx_warmup_deliveries_status ON warmup_deliveries(status, updated_at);
CREATE INDEX idx_warmup_deliveries_scenario ON warmup_deliveries(scenario_id, created_at);

CREATE TABLE warmup_coupons (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL,
    telegram_id INTEGER NOT NULL,
    scenario_id INTEGER NOT NULL REFERENCES warmup_scenarios(id),
    delivery_id INTEGER REFERENCES warmup_deliveries(id),
    percent INTEGER NOT NULL CHECK (percent BETWEEN 1 AND 100),
    -- issued: created with a message; revoked: its message was never
    -- delivered (permanent send failure). Expiry is derived from expires_at.
    -- redeemed is reserved for the future payment integration.
    status TEXT NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'redeemed', 'revoked')),
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    redeemed_at DATETIME,
    order_id INTEGER
);

CREATE UNIQUE INDEX idx_warmup_coupons_code ON warmup_coupons(code);
CREATE UNIQUE INDEX idx_warmup_coupons_delivery ON warmup_coupons(delivery_id);
CREATE INDEX idx_warmup_coupons_user ON warmup_coupons(telegram_id, created_at);

-- Globally OFF: the admin switches the warm-up on explicitly.
INSERT INTO warmup_settings (id, enabled, updated_at) VALUES (1, 0, strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'));

-- Historical contacts: every linked customer, first seen when the first of
-- their subscriptions was created. source = 'backfill' keeps them out of the
-- "bot started" trigger: existing users never get "trial not activated" just
-- because they existed before this feature.
INSERT INTO warmup_contacts (telegram_id, username, language_code, source, first_seen_at, last_seen_at)
SELECT s.telegram_id,
       COALESCE((SELECT l.username FROM subscriptions l WHERE l.telegram_id = s.telegram_id ORDER BY l.id DESC LIMIT 1), ''),
       '', 'backfill',
       strftime('%Y-%m-%d %H:%M:%f+00:00', MIN(s.created_at)),
       strftime('%Y-%m-%d %H:%M:%f+00:00', MAX(s.created_at))
FROM subscriptions s
WHERE s.telegram_id > 0 AND s.created_at IS NOT NULL
GROUP BY s.telegram_id;

-- The 14 standard scenarios. Every field is editable in the admin.
-- 11 enabled now (enabled_at = migration time): 1-6 and 10-14. They send
-- nothing while warmup_settings.enabled = 0.
-- 3 disabled: the subscription scenarios 7-9 (48h, 24h, expired), so they
-- never duplicate the existing 3d/1d/3h expiry reminders, which stay in force.
-- The "Активировать пробник" URL button has no URL yet: it is hidden until
-- the admin sets the trial page URL (an empty URL is never sent).
INSERT INTO warmup_scenarios (key, category, name, description, trigger_type, timing, delay_value, delay_unit, enabled, enabled_at,
                              message_html, buttons, conditions, position, created_at, updated_at)
SELECT key, category, name, description, trigger_type, timing, delay_value, delay_unit, enabled,
       CASE WHEN enabled THEN strftime('%Y-%m-%d %H:%M:%f+00:00', 'now') END,
       message_html, buttons, conditions, position,
       strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'), strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
FROM (
    SELECT 'trial_not_activated' AS key, 'trial' AS category, 'Пробник не активирован' AS name,
           'Через 12 часов после первого запуска бота, если пробный период не активирован' AS description,
           'bot_started' AS trigger_type, 'after' AS timing, 12 AS delay_value, 'hours' AS delay_unit, 1 AS enabled,
           '<b>Пробный период ждёт вас</b>' || char(10) || char(10) ||
           'Вы ещё не активировали бесплатный доступ.' || char(10) ||
           'Это {{trial_duration}} без оплаты и без привязки карты: включаете VPN и пользуетесь.' || char(10) || char(10) ||
           'Активировать можно на экране тарифов.' AS message_html,
           '[{"text":"Активировать пробник","kind":"url","value":""}]' AS buttons,
           '{"had_trial":false,"has_subscription":false,"has_active_paid":false}' AS conditions, 10 AS position
    UNION ALL SELECT 'trial_not_connected_3h', 'trial', 'Пробник не подключён, 3 часа',
           'Через 3 часа после активации пробного периода, если конфиг не забрали',
           'trial_activated', 'after', 3, 'hours', 1,
           '<b>Осталось подключиться</b>' || char(10) || char(10) ||
           'Пробный период активирован, но VPN вы ещё не включали.' || char(10) ||
           'Подключение занимает минуту: откройте подписку и выберите свою систему.',
           '[{"text":"Открыть подписку","kind":"callback","value":"menu_subscription"}]',
           '{"connected":false,"has_active_paid":false}', 20
    UNION ALL SELECT 'trial_not_connected_24h', 'trial', 'Пробник не подключён, сутки',
           'Через 24 часа после активации пробного периода, если конфиг не забрали',
           'trial_activated', 'after', 24, 'hours', 1,
           '<b>VPN всё ещё не подключён</b>' || char(10) || char(10) ||
           'С активации прошли сутки, а подключения так и не было.' || char(10) ||
           'Откройте подписку: там пошаговая инструкция для вашей системы. Если что-то не получается, напишите в поддержку.',
           '[{"text":"Открыть подписку","kind":"callback","value":"menu_subscription"},{"text":"Поддержка","kind":"callback","value":"menu_support"}]',
           '{"connected":false,"has_active_paid":false}', 30
    UNION ALL SELECT 'trial_ending', 'trial', 'Пробник заканчивается',
           'За 24 часа до окончания пробного периода',
           'trial_period_end', 'before', 24, 'hours', 1,
           '<b>Пробный период скоро закончится</b>' || char(10) || char(10) ||
           'Бесплатный доступ действует до {{expire_date}}.' || char(10) ||
           'Чтобы VPN работал без перерыва, выберите тариф заранее.',
           '[{"text":"Выбрать тариф","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 40
    UNION ALL SELECT 'trial_no_purchase', 'trial', 'Пробник взяли, но не купили',
           'Через 2 дня после активации пробного периода, если нет платной подписки',
           'trial_activated', 'after', 2, 'days', 1,
           '<b>Как вам VPN?</b>' || char(10) || char(10) ||
           'Вы пользуетесь пробным доступом уже два дня.' || char(10) ||
           'Если всё нравится, оформите тариф: безлимитный трафик и больше серверов.',
           '[{"text":"Выбрать тариф","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false,"ever_paid":false}', 50
    UNION ALL SELECT 'trial_ended', 'trial', 'Пробник закончился',
           'В день окончания пробного периода',
           'trial_period_end', 'after', 0, 'hours', 1,
           '<b>Пробный период закончился</b>' || char(10) || char(10) ||
           'Спасибо, что попробовали. Чтобы пользоваться VPN дальше, выберите подходящий тариф.',
           '[{"text":"Выбрать тариф","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 60
    UNION ALL SELECT 'sub_expiring_48h', 'subscription', 'Подписка: осталось 48 часов',
           'За 48 часов до окончания платной подписки',
           'paid_expiry', 'before', 48, 'hours', 0,
           '<b>Подписка закончится через 48 часов</b>' || char(10) || char(10) ||
           '«{{plan_name}}» действует до {{expire_date}}.' || char(10) ||
           'Продлите заранее: новые дни прибавятся к текущему сроку.',
           '[{"text":"Продлить подписку","kind":"callback","value":"buy_premium_list"}]',
           '{}', 10
    UNION ALL SELECT 'sub_expiring_24h', 'subscription', 'Подписка: остались сутки',
           'За 24 часа до окончания платной подписки',
           'paid_expiry', 'before', 24, 'hours', 0,
           '<b>Подписка закончится через сутки</b>' || char(10) || char(10) ||
           '«{{plan_name}}» действует до {{expire_date}}.' || char(10) ||
           'Продлите сейчас, чтобы не остаться без VPN.',
           '[{"text":"Продлить подписку","kind":"callback","value":"buy_premium_list"}]',
           '{}', 20
    UNION ALL SELECT 'sub_expired', 'subscription', 'Подписка закончилась',
           'В день окончания платной подписки',
           'paid_expired', 'after', 0, 'hours', 0,
           '<b>Подписка закончилась</b>' || char(10) || char(10) ||
           'Срок «{{plan_name}}» истёк {{expire_date}}.' || char(10) ||
           'Продлите подписку, чтобы вернуть доступ ко всем серверам.',
           '[{"text":"Продлить подписку","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 30
    UNION ALL SELECT 'winback_3d', 'winback', 'Возврат клиента: 3 дня',
           'Через 3 дня после окончания платной подписки',
           'paid_expired', 'after', 3, 'days', 1,
           '<b>Мы скучаем</b>' || char(10) || char(10) ||
           'Прошло три дня с окончания подписки.' || char(10) ||
           'Вернуть доступ можно за пару минут: выберите тариф.',
           '[{"text":"Продлить подписку","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 10
    UNION ALL SELECT 'trial_winback_3d', 'winback', 'После пробника: 3 дня',
           'Через 3 дня после окончания пробного периода, если не купил',
           'trial_period_end', 'after', 3, 'days', 1,
           '<b>Попробовали — понравилось?</b>' || char(10) || char(10) ||
           'Пробный период закончился три дня назад.' || char(10) ||
           'Оформите тариф и пользуйтесь VPN без ограничений.',
           '[{"text":"Выбрать тариф","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false,"ever_paid":false}', 20
    UNION ALL SELECT 'winback_7d', 'winback', 'Возврат клиента: 7 дней',
           'Через 7 дней после окончания платной подписки',
           'paid_expired', 'after', 7, 'days', 1,
           '<b>Возвращайтесь</b>' || char(10) || char(10) ||
           'Неделя без VPN. Все серверы на месте: продлите подписку, и доступ появится сразу.',
           '[{"text":"Продлить подписку","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 30
    UNION ALL SELECT 'winback_14d', 'winback', 'Возврат клиента: 14 дней',
           'Через 14 дней после окончания платной подписки',
           'paid_expired', 'after', 14, 'days', 1,
           '<b>Две недели без VPN</b>' || char(10) || char(10) ||
           'Загляните в актуальные тарифы: продлить подписку можно в пару нажатий.',
           '[{"text":"Посмотреть тарифы","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 40
    UNION ALL SELECT 'winback_30d', 'winback', 'Возврат клиента: 30 дней',
           'Через 30 дней после окончания платной подписки',
           'paid_expired', 'after', 30, 'days', 1,
           '<b>Месяц без VPN</b>' || char(10) || char(10) ||
           'Возвращайтесь: продлить подписку можно в пару нажатий.',
           '[{"text":"Продлить подписку","kind":"callback","value":"buy_premium_list"}]',
           '{"has_active_paid":false}', 50
);
