-- Migration: 048_network_monitor
-- Description: Network monitor ("Мониторинг" admin page). Tracks the servers
-- that builders actually serve (Builder -> Country -> Node) with
-- handshake-level checks. See doc/network-monitoring.md.
--
-- * monitor_targets: one row per network endpoint (protocol, host, port,
--   transport). A server used by several builders is one target and is
--   checked once. No credentials: the endpoint_key is a sha256 of the
--   credential-free identity. Targets are never deleted; a target no builder
--   serves any more is inactive (active = 0) and keeps its history.
-- * monitor_bindings: Builder -> Source -> catalogue fingerprint -> target,
--   with the country and display name. Kept forever; active = 0 once the
--   builder stops serving the server (catalogue change, rule change,
--   disabled source/builder).
-- * monitor_country_states: current state of a (builder, country).
-- * monitor_outages: one row per outage of a target (down) or a builder
--   country (down/degraded). A partial unique index allows at most one open
--   outage per subject, so a repeated DOWN can never open a duplicate.
-- * monitor_samples: 5-minute check aggregates per target (count, failures,
--   latency) instead of one row per identical UP/UP check; pruned after 35
--   days. bucket_start is Unix seconds.
-- * Timestamps use the Go/mattn format in UTC, like journal_events.
-- No foreign keys to builders/sources: monitoring history outlives them.

CREATE TABLE monitor_targets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    endpoint_key TEXT NOT NULL,
    protocol TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL,
    security TEXT NOT NULL DEFAULT '',
    transport TEXT NOT NULL DEFAULT '',
    sni TEXT NOT NULL DEFAULT '',
    probe TEXT NOT NULL CHECK (probe IN ('tcp', 'tls', 'quic', 'unsupported')),
    active BOOLEAN NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'up', 'down')),
    status_since DATETIME,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    first_failure_at DATETIME,
    last_checked_at DATETIME,
    last_up_at DATETIME,
    last_down_at DATETIME,
    last_latency_ms INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    first_seen_at DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX idx_monitor_targets_key ON monitor_targets(endpoint_key);
CREATE INDEX idx_monitor_targets_active ON monitor_targets(active);

CREATE TABLE monitor_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    builder_id INTEGER NOT NULL,
    builder_name TEXT NOT NULL DEFAULT '',
    source_id INTEGER NOT NULL,
    fingerprint TEXT NOT NULL,
    target_id INTEGER NOT NULL REFERENCES monitor_targets(id),
    country_code TEXT NOT NULL DEFAULT '',
    node_name TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL DEFAULT 1,
    first_seen_at DATETIME NOT NULL,
    last_seen_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX idx_monitor_bindings_identity ON monitor_bindings(builder_id, source_id, fingerprint);
CREATE INDEX idx_monitor_bindings_target ON monitor_bindings(target_id);
CREATE INDEX idx_monitor_bindings_country ON monitor_bindings(builder_id, country_code);

CREATE TABLE monitor_country_states (
    builder_id INTEGER NOT NULL,
    country_code TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'up', 'degraded', 'down')),
    status_since DATETIME,
    active BOOLEAN NOT NULL DEFAULT 1,
    last_checked_at DATETIME,
    last_up_at DATETIME,
    last_down_at DATETIME,
    PRIMARY KEY (builder_id, country_code)
);

CREATE TABLE monitor_outages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scope TEXT NOT NULL CHECK (scope IN ('target', 'country')),
    target_id INTEGER,
    builder_id INTEGER,
    country_code TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('down', 'degraded')),
    started_at DATETIME NOT NULL,
    ended_at DATETIME,
    end_reason TEXT NOT NULL DEFAULT '' CHECK (end_reason IN ('', 'recovered', 'changed', 'removed')),
    error_code TEXT NOT NULL DEFAULT '',
    CHECK ((scope = 'target' AND target_id IS NOT NULL) OR (scope = 'country' AND builder_id IS NOT NULL))
);

CREATE UNIQUE INDEX idx_monitor_outages_open_target ON monitor_outages(target_id)
    WHERE scope = 'target' AND ended_at IS NULL;
CREATE UNIQUE INDEX idx_monitor_outages_open_country ON monitor_outages(builder_id, country_code)
    WHERE scope = 'country' AND ended_at IS NULL;
CREATE INDEX idx_monitor_outages_target ON monitor_outages(target_id, started_at);
CREATE INDEX idx_monitor_outages_country ON monitor_outages(builder_id, country_code, started_at);

CREATE TABLE monitor_samples (
    target_id INTEGER NOT NULL,
    bucket_start INTEGER NOT NULL,
    checks INTEGER NOT NULL DEFAULT 0,
    failures INTEGER NOT NULL DEFAULT 0,
    latency_count INTEGER NOT NULL DEFAULT 0,
    latency_sum_ms INTEGER NOT NULL DEFAULT 0,
    latency_min_ms INTEGER,
    latency_max_ms INTEGER,
    PRIMARY KEY (target_id, bucket_start)
) WITHOUT ROWID;

CREATE INDEX idx_monitor_samples_bucket ON monitor_samples(bucket_start);
