-- Migration: 045_subscription_builders
-- Description: Subscription Builder. ProviderSource stays the upstream
-- ("mother") subscription; a builder is a separate output configuration that
-- selects entries from one or more sources. Customer subscriptions use a
-- builder either through their own override or through their plan default.
-- Everything is additive: rows without a builder keep the existing routing.

ALTER TABLE provider_sources ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_sources ADD COLUMN last_sync_at DATETIME;
ALTER TABLE provider_sources ADD COLUMN last_sync_status VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE provider_sources ADD COLUMN last_sync_error VARCHAR(64) NOT NULL DEFAULT '';

-- Catalogue of entries seen on the last refresh of a source. Only metadata is
-- stored: share links carry credentials and are always fetched live.
CREATE TABLE provider_source_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL REFERENCES provider_sources(id) ON UPDATE CASCADE ON DELETE CASCADE,
    fingerprint VARCHAR(64) NOT NULL,
    original_name VARCHAR(255) NOT NULL DEFAULT '',
    protocol VARCHAR(32) NOT NULL DEFAULT '',
    country_code VARCHAR(2) NOT NULL DEFAULT '',
    upstream_position INTEGER NOT NULL DEFAULT 0,
    present BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at DATETIME NOT NULL,
    UNIQUE(source_id, fingerprint)
);
CREATE INDEX idx_provider_source_entries_source ON provider_source_entries(source_id, upstream_position);

CREATE TABLE subscription_builders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(128) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    profile_title VARCHAR(128) NOT NULL DEFAULT '',
    support_url VARCHAR(512) NOT NULL DEFAULT '',
    announce VARCHAR(512) NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE subscription_builder_sources (
    builder_id INTEGER NOT NULL REFERENCES subscription_builders(id) ON UPDATE CASCADE ON DELETE CASCADE,
    source_id INTEGER NOT NULL REFERENCES provider_sources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (builder_id, source_id)
);
CREATE INDEX idx_subscription_builder_sources_source ON subscription_builder_sources(source_id);

-- kind = 'country': every entry of the source whose detected country matches
--                   country_code ('' = entries without a country), resolved
--                   dynamically on every build;
-- kind = 'node':    one concrete entry, matched by fingerprint, falling back
--                   to a unique original_name.
CREATE TABLE subscription_builder_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    builder_id INTEGER NOT NULL REFERENCES subscription_builders(id) ON UPDATE CASCADE ON DELETE CASCADE,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('country', 'node')),
    source_id INTEGER NOT NULL REFERENCES provider_sources(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    country_code VARCHAR(2) NOT NULL DEFAULT '',
    fingerprint VARCHAR(64) NOT NULL DEFAULT '',
    original_name VARCHAR(255) NOT NULL DEFAULT '',
    custom_name VARCHAR(128),
    description TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_subscription_builder_items_builder ON subscription_builder_items(builder_id, position);
CREATE INDEX idx_subscription_builder_items_source ON subscription_builder_items(source_id);

ALTER TABLE subscriptions ADD COLUMN subscription_builder_id INTEGER
    REFERENCES subscription_builders(id) ON UPDATE CASCADE ON DELETE SET NULL;
CREATE INDEX idx_subscriptions_subscription_builder_id ON subscriptions(subscription_builder_id);

ALTER TABLE plans ADD COLUMN subscription_builder_id INTEGER
    REFERENCES subscription_builders(id) ON UPDATE CASCADE ON DELETE SET NULL;

-- Configuration changes are audited in the same table as subscription
-- mutations. Non-subscription targets store subscription_id = 0.
ALTER TABLE admin_audit_log ADD COLUMN target_type VARCHAR(32) NOT NULL DEFAULT 'subscription';
ALTER TABLE admin_audit_log ADD COLUMN target_id INTEGER NOT NULL DEFAULT 0;
UPDATE admin_audit_log SET target_id = subscription_id;
CREATE INDEX idx_admin_audit_target_ref ON admin_audit_log(target_type, target_id, id);
