DROP INDEX IF EXISTS idx_subscriptions_provider_source_id;

ALTER TABLE subscriptions DROP COLUMN provider_source_id;
