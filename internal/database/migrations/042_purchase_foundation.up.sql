-- Reuse products/offers and orders; no second purchase or account table.
ALTER TABLE products ADD COLUMN offer_id TEXT;
ALTER TABLE products ADD COLUMN offer_ends_at DATETIME;
-- Public references are identifiers, not bearer credentials.
UPDATE products SET offer_id = lower(hex(randomblob(16)));
CREATE UNIQUE INDEX idx_products_offer_id ON products(offer_id) WHERE offer_id IS NOT NULL AND offer_id <> '';

ALTER TABLE orders ADD COLUMN purchase_id TEXT;
ALTER TABLE orders ADD COLUMN buyer_telegram_id INTEGER CHECK (buyer_telegram_id IS NULL OR buyer_telegram_id > 0);
ALTER TABLE orders ADD COLUMN purchase_key TEXT;
ALTER TABLE orders ADD COLUMN purchase_expires_at DATETIME;
CREATE UNIQUE INDEX idx_orders_purchase_id ON orders(purchase_id) WHERE purchase_id IS NOT NULL;
CREATE UNIQUE INDEX idx_orders_purchase_key ON orders(buyer_telegram_id, purchase_key) WHERE purchase_key IS NOT NULL;
CREATE UNIQUE INDEX idx_orders_purchase_pending ON orders(subscription_id, product_id) WHERE purchase_id IS NOT NULL AND status = 'pending';

-- New purchase rows must carry durable ownership and replay information.
CREATE TRIGGER orders_purchase_required_insert BEFORE INSERT ON orders
WHEN NEW.purchase_id IS NOT NULL AND (
    length(NEW.purchase_id) <> 32 OR NEW.purchase_id GLOB '*[^0-9a-f]*' OR
    NEW.buyer_telegram_id IS NULL OR NEW.purchase_key IS NULL OR length(NEW.purchase_key) <> 36 OR
    NEW.purchase_expires_at IS NULL OR
    NOT EXISTS (SELECT 1 FROM subscriptions WHERE id = NEW.subscription_id AND telegram_id = NEW.buyer_telegram_id)
)
BEGIN SELECT RAISE(ABORT, 'invalid purchase ownership or metadata'); END;

CREATE TRIGGER orders_purchase_identity_immutable BEFORE UPDATE ON orders
WHEN OLD.purchase_id IS NOT NULL AND (
    NEW.purchase_id IS NOT OLD.purchase_id OR NEW.buyer_telegram_id IS NOT OLD.buyer_telegram_id OR
    NEW.purchase_key IS NOT OLD.purchase_key OR NEW.subscription_id IS NOT OLD.subscription_id OR
    NEW.product_id IS NOT OLD.product_id OR NEW.amount_cents IS NOT OLD.amount_cents OR
    NEW.currency IS NOT OLD.currency OR NEW.created_at IS NOT OLD.created_at OR
    NEW.purchase_expires_at IS NOT OLD.purchase_expires_at
)
BEGIN SELECT RAISE(ABORT, 'purchase terms are immutable'); END;
