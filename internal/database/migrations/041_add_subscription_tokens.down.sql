DROP TRIGGER IF EXISTS subscriptions_token_required_insert;
DROP TRIGGER IF EXISTS subscriptions_token_required_update;
DROP TRIGGER IF EXISTS subscriptions_token_format_insert;
DROP TRIGGER IF EXISTS subscriptions_token_format_update;
DROP INDEX IF EXISTS idx_subscriptions_token;

ALTER TABLE subscriptions DROP COLUMN token;
