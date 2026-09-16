-- Migration: 039_create_provider_sources
-- Description: Stores external VPN provider subscription feeds independently
-- from provisioned VPN nodes. Runtime integration is intentionally deferred.

CREATE TABLE provider_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    subscription_url VARCHAR(2048) NOT NULL,
    hwid VARCHAR(255) NOT NULL DEFAULT '',
    user_agent VARCHAR(512) NOT NULL DEFAULT '',
    headers TEXT NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_provider_sources_type ON provider_sources(type);
CREATE INDEX idx_provider_sources_enabled ON provider_sources(enabled);
