DROP INDEX IF EXISTS idx_admin_audit_target_ref;
DELETE FROM admin_audit_log WHERE target_type <> 'subscription';
ALTER TABLE admin_audit_log DROP COLUMN target_id;
ALTER TABLE admin_audit_log DROP COLUMN target_type;

ALTER TABLE plans DROP COLUMN subscription_builder_id;

DROP INDEX IF EXISTS idx_subscriptions_subscription_builder_id;
ALTER TABLE subscriptions DROP COLUMN subscription_builder_id;

DROP TABLE IF EXISTS subscription_builder_items;
DROP TABLE IF EXISTS subscription_builder_sources;
DROP TABLE IF EXISTS subscription_builders;
DROP TABLE IF EXISTS provider_source_entries;

ALTER TABLE provider_sources DROP COLUMN last_sync_error;
ALTER TABLE provider_sources DROP COLUMN last_sync_status;
ALTER TABLE provider_sources DROP COLUMN last_sync_at;
ALTER TABLE provider_sources DROP COLUMN description;
