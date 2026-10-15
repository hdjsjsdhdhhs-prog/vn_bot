-- Revert: восстанавливает схему старой подсистемы узлов (без данных).
-- Данные узлов, их привязки и состояние синхронизации миграция 051 не
-- сохраняет: откат возвращает только пустые таблицы и колонки.

ALTER TABLE plans ADD COLUMN traffic_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plans ADD COLUMN devices_limit INTEGER NOT NULL DEFAULT 1;

ALTER TABLE subscriptions ADD COLUMN traffic_reminders_sent INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL,
    is_active INTEGER DEFAULT 1,
    host VARCHAR(255),
    api_token VARCHAR(255),
    subscription_url VARCHAR(512) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    type VARCHAR(10) NOT NULL DEFAULT '3x-ui',
    inbound_ids TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS plan_nodes (
    plan_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    PRIMARY KEY (plan_id, node_id),
    FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS subscription_nodes (
    subscription_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN (
            'active',
            'pending_add',
            'pending_remove',
            'pending_update'
        )
    ),
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    retry_at DATETIME,
    last_error TEXT,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (subscription_id, node_id),
    FOREIGN KEY (subscription_id) REFERENCES subscriptions(id),
    FOREIGN KEY (node_id) REFERENCES nodes(id),
    CHECK (retry_count = 0 OR retry_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_subscription_nodes_subscription_id ON subscription_nodes(subscription_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_node_id ON subscription_nodes(node_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_status ON subscription_nodes(status);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_updated_at ON subscription_nodes(updated_at);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_retry ON subscription_nodes(status, retry_at);
