CREATE TABLE admin_audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    subscription_id INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    old_value TEXT NOT NULL DEFAULT '{}',
    new_value TEXT NOT NULL DEFAULT '{}',
    success BOOLEAN NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    request_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    UNIQUE(actor, request_key)
);
CREATE INDEX idx_admin_audit_target ON admin_audit_log(subscription_id, id);
CREATE INDEX idx_admin_audit_created ON admin_audit_log(created_at, id);
