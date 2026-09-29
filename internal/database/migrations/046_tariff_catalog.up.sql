-- Migration: 046_tariff_catalog
-- Description: Tariff editor. products gains the card presentation shown in the
-- Mini App (description, features, badge), a catalogue position shared by the
-- Mini App and the bot, an optimistic-locking version and the predecessor link
-- of a versioned tariff.
--
-- The inline UNIQUE(plan_id, duration_days) from 013 is dropped. Products are
-- immutable after the first order (ErrProductImmutable), so a new price for the
-- same plan and duration must be a new product row (successor version) while
-- the old row keeps the terms referenced by orders and subscriptions. The inline
-- constraint made that impossible, and SQLite cannot drop a table constraint in
-- place, hence the rebuild (same technique as 034 for orders). Existing rows keep
-- their IDs, offer references and terms; sort_order starts at 0 for every row,
-- so the catalogue order (sort_order, price, id) is unchanged by the upgrade.

PRAGMA foreign_keys = OFF;

CREATE TABLE products_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    duration_days INTEGER NOT NULL,
    price_cents INTEGER NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'RUB',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    name VARCHAR(255) NOT NULL DEFAULT '',
    offer_id TEXT,
    offer_ends_at DATETIME,
    description TEXT NOT NULL DEFAULT '',
    features TEXT NOT NULL DEFAULT '[]',
    badge VARCHAR(32) NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    -- Predecessor this row replaced when the admin changed the purchase terms
    -- of a product already referenced by orders/subscriptions. A plain column
    -- (no FK, no index) so the down migration can drop it in place.
    previous_id INTEGER
);

INSERT INTO products_new (id, plan_id, duration_days, price_cents, currency, is_active, created_at, updated_at, name, offer_id, offer_ends_at)
SELECT id, plan_id, duration_days, price_cents, currency, is_active, created_at, updated_at, name, offer_id, offer_ends_at
FROM products;

DROP TABLE products;

ALTER TABLE products_new RENAME TO products;

CREATE INDEX IF NOT EXISTS idx_products_plan ON products(plan_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_products_offer_id ON products(offer_id) WHERE offer_id IS NOT NULL AND offer_id <> '';

PRAGMA foreign_keys = ON;

-- Same guard as 034: fail the migration (before the version is stored) if the
-- rebuild left any row referencing a missing product, scoped to the tables that
-- reference products (orders, subscriptions).
DROP TABLE IF EXISTS migration_046_foreign_key_check;
CREATE TEMP TABLE migration_046_foreign_key_check (
    violation_count INTEGER NOT NULL CHECK (violation_count = 0)
);
INSERT INTO migration_046_foreign_key_check
SELECT (SELECT COUNT(*) FROM pragma_foreign_key_check('products'))
     + (SELECT COUNT(*) FROM pragma_foreign_key_check('orders') WHERE parent = 'products')
     + (SELECT COUNT(*) FROM pragma_foreign_key_check('subscriptions') WHERE parent = 'products');
DROP TABLE migration_046_foreign_key_check;
