-- Migration: 047_journal_events
-- Description: Append-only journal of user, subscription and payment events
-- shown on the admin "Журнал" page. Rows are written by the backend inside the
-- same transaction as the state change they describe.
--
-- * subscription_id / order_id / plan_id are plain columns without foreign
--   keys: the journal must outlive admin /del and expired-trial cleanup, which
--   physically delete subscriptions. User and plan names are snapshots.
-- * dedup_key is UNIQUE; NULLs stay distinct in SQLite, so events without a
--   natural idempotency key are allowed while keyed events are recorded once.
-- * UPDATE and DELETE are rejected by triggers: the journal is append-only.
-- * created_at uses the Go/mattn format ("YYYY-MM-DD HH:MM:SS.fff+00:00", UTC)
--   so ordering and period filters compare lexicographically.

CREATE TABLE journal_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME NOT NULL,
    event_type TEXT NOT NULL,
    outcome TEXT NOT NULL DEFAULT 'success' CHECK (outcome IN ('success', 'failed', 'rejected')),
    actor TEXT NOT NULL CHECK (actor IN ('user', 'admin', 'system')),
    actor_name TEXT NOT NULL DEFAULT '',
    telegram_id INTEGER NOT NULL DEFAULT 0,
    username TEXT NOT NULL DEFAULT '',
    subscription_id INTEGER,
    plan_id INTEGER,
    plan_name TEXT NOT NULL DEFAULT '',
    plan_kind TEXT NOT NULL DEFAULT '' CHECK (plan_kind IN ('', 'free', 'trial', 'paid')),
    order_id INTEGER,
    amount_cents INTEGER,
    currency TEXT,
    description TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '{}',
    dedup_key TEXT
);

CREATE UNIQUE INDEX idx_journal_events_dedup ON journal_events(dedup_key);
CREATE INDEX idx_journal_events_created ON journal_events(created_at, id);
CREATE INDEX idx_journal_events_type ON journal_events(event_type, created_at);
CREATE INDEX idx_journal_events_telegram ON journal_events(telegram_id, created_at);
CREATE INDEX idx_journal_events_subscription ON journal_events(subscription_id, created_at);

-- Backfill from facts that are already recorded, with the same dedup keys the
-- live code uses, so nothing is journaled twice after the upgrade.

-- Registrations: every linked customer row.
INSERT INTO journal_events (created_at, event_type, actor, telegram_id, username, subscription_id, plan_id, plan_name, plan_kind, description, details, dedup_key)
SELECT strftime('%Y-%m-%d %H:%M:%f+00:00', s.created_at), 'user_registered', 'user', s.telegram_id, COALESCE(s.username, ''), s.id, s.plan_id,
       COALESCE(p.name, ''),
       CASE WHEN p.name = 'trial' THEN 'trial' WHEN p.name = 'free' THEN 'free' WHEN p.name IS NULL THEN '' ELSE 'paid' END,
       'Пользователь зарегистрирован (восстановлено из истории)', '{"backfill":true}', 'subscription_created:' || s.id
FROM subscriptions s
LEFT JOIN plans p ON p.id = s.plan_id
WHERE s.telegram_id > 0 AND s.created_at IS NOT NULL;

-- Successful payments: paid (and later charged back) orders with a paid_at.
INSERT INTO journal_events (created_at, event_type, actor, actor_name, telegram_id, username, subscription_id, plan_id, plan_name, plan_kind, order_id, amount_cents, currency, description, details, dedup_key)
SELECT strftime('%Y-%m-%d %H:%M:%f+00:00', o.paid_at), 'payment_succeeded', 'user', COALESCE(o.payment_provider, ''), COALESCE(s.telegram_id, 0), COALESCE(s.username, ''), o.subscription_id,
       pr.plan_id, COALESCE(p.name, ''),
       CASE WHEN p.name = 'trial' THEN 'trial' WHEN p.name = 'free' THEN 'free' WHEN p.name IS NULL THEN '' ELSE 'paid' END,
       o.id, o.amount_cents, o.currency,
       'Оплата заказа #' || o.id || ' прошла успешно (восстановлено из истории)', '{"backfill":true}', 'order_paid:' || o.id
FROM orders o
LEFT JOIN subscriptions s ON s.id = o.subscription_id
LEFT JOIN products pr ON pr.id = o.product_id
LEFT JOIN plans p ON p.id = pr.plan_id
WHERE o.paid_at IS NOT NULL;

-- Browser-admin subscription mutations (migration 044 audit log).
INSERT INTO journal_events (created_at, event_type, outcome, actor, actor_name, telegram_id, username, subscription_id, plan_id, plan_name, plan_kind, description, details, dedup_key)
SELECT strftime('%Y-%m-%d %H:%M:%f+00:00', a.created_at),
       CASE a.action WHEN 'renew' THEN 'subscription_renewed' WHEN 'change_expiry' THEN 'expiry_changed'
                     WHEN 'disable' THEN 'subscription_disabled' ELSE 'subscription_enabled' END,
       CASE WHEN a.success THEN 'success' ELSE 'rejected' END,
       'admin', a.actor, COALESCE(s.telegram_id, 0), COALESCE(s.username, ''), a.subscription_id, s.plan_id, COALESCE(p.name, ''),
       CASE WHEN p.name = 'trial' THEN 'trial' WHEN p.name = 'free' THEN 'free' WHEN p.name IS NULL THEN '' ELSE 'paid' END,
       CASE a.action WHEN 'renew' THEN 'Администратор продлил подписку'
                     WHEN 'change_expiry' THEN 'Администратор изменил срок действия подписки'
                     WHEN 'disable' THEN 'Администратор отключил подписку'
                     ELSE 'Администратор включил подписку' END
         || CASE WHEN a.success THEN '' ELSE ' — отклонено' END || ' (восстановлено из истории)',
       json_object('backfill', json('true'), 'audit_id', a.id, 'error_code', a.error_code,
                   'before', json(a.old_value), 'after', json(a.new_value)),
       'admin_audit:' || a.id
FROM admin_audit_log a
LEFT JOIN subscriptions s ON s.id = a.subscription_id
LEFT JOIN plans p ON p.id = s.plan_id
WHERE a.target_type = 'subscription' AND a.action IN ('renew', 'change_expiry', 'disable', 'enable');

CREATE TRIGGER journal_events_append_only_update BEFORE UPDATE ON journal_events
BEGIN SELECT RAISE(ABORT, 'journal_events is append-only'); END;

CREATE TRIGGER journal_events_append_only_delete BEFORE DELETE ON journal_events
BEGIN SELECT RAISE(ABORT, 'journal_events is append-only'); END;
