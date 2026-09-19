-- Stars reuses the purchase order state machine. A checkout reservation prevents
-- two invoice links for one order from authorizing two distinct charges.
ALTER TABLE orders ADD COLUMN stars_checkout_id TEXT;
ALTER TABLE orders ADD COLUMN stars_checkout_at DATETIME;

-- Delivery inbox, not a second order/payment lifecycle. Persist before advancing
-- Telegram getUpdates offset; retain receipts even if a subscription is deleted.
CREATE TABLE telegram_payment_receipts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_charge_id TEXT NOT NULL UNIQUE,
    provider_charge_id TEXT NOT NULL DEFAULT '',
    payer_telegram_id INTEGER NOT NULL,
    invoice_payload TEXT NOT NULL,
    currency TEXT NOT NULL,
    total_amount INTEGER NOT NULL,
    telegram_date INTEGER NOT NULL,
    received_at DATETIME NOT NULL,
    settled_at DATETIME
);
CREATE INDEX idx_telegram_receipts_unsettled ON telegram_payment_receipts(id) WHERE settled_at IS NULL;
