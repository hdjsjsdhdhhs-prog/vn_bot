-- Migration: 040_link_subscriptions_provider_sources
-- Description: Adds an optional provider source to subscriptions. Existing
-- subscriptions remain on the legacy plan-and-nodes routing path.

ALTER TABLE subscriptions
    ADD COLUMN provider_source_id INTEGER
    REFERENCES provider_sources(id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_subscriptions_provider_source_id
    ON subscriptions(provider_source_id);
