-- Migration: 049_trial_settings
-- Description: Admin-editable trial offer (Admin -> "Тарифы" -> "Пробная
-- подписка"). One row at most (id = 1). Without a row the trial keeps the
-- legacy behaviour: enabled, TRIAL_DURATION_HOURS and TRIAL_RATE_LIMIT from
-- the environment, no landing-page texts.
--
-- The trial composition is NOT stored here: it stays the default builder of
-- the system "trial" plan (plans.subscription_builder_id) and that builder's
-- sources and rules, exactly what /sub already resolves.
--
-- Bounds mirror the environment validation (1..168 hours, 1..100 requests per
-- IP and hour) so a stored value can never be stricter or looser than what the
-- service accepts.

CREATE TABLE trial_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT 1,
    duration_hours INTEGER NOT NULL CHECK (duration_hours BETWEEN 1 AND 168),
    rate_limit_per_hour INTEGER NOT NULL CHECK (rate_limit_per_hour BETWEEN 1 AND 100),
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    features TEXT NOT NULL DEFAULT '[]',
    badge TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
