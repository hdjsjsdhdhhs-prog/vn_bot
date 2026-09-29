-- Migration: 046_tariff_catalog (DOWN)
-- Description: Removes the tariff-editor columns and restores uniqueness of
-- (plan_id, duration_days).
--
-- Down migrations run inside golang-migrate's transaction, where
-- PRAGMA foreign_keys cannot be switched off; rebuilding products there would
-- make DROP TABLE fail on the orders/subscriptions foreign keys. The columns
-- are therefore dropped in place (SQLite >= 3.35, already required by 006/031),
-- and the uniqueness of 013 comes back as a unique index instead of the inline
-- table constraint — the same guarantee for the application and for 046 up.
--
-- Fails on the unique index if successor versions were created (two rows with
-- the same plan and duration); resolve those rows manually first.
--
-- ROLLBACK RISK AFTER SUCCESSOR PRODUCTS EXIST
-- Once the tariff editor has versioned a used product, the retired original
-- and its successor share plan_id and duration_days, and orders/subscriptions
-- still reference the original. This down cannot restore the 045 uniqueness
-- without deleting or rewriting one of them, and it must never do that
-- silently: the retired row carries the terms that were paid for, so neither
-- row can simply be dropped. Before rolling back, merge or remove successors
-- deliberately (and re-point any orders/subscriptions of a removed row), or do
-- not roll back 046 at all.
--
-- If this down is attempted anyway and fails, the transaction is rolled back:
-- the products table, its data and all six columns remain exactly as in 046.
-- golang-migrate has nevertheless already written schema_migrations as
-- version=45, dirty=true, i.e. metadata that no longer matches the schema.
-- Do NOT start the bot in that state: its dirty-state recovery treats 045 as a
-- transactional migration, rewinds to 44 and re-applies 045 over a schema that
-- already has it ("duplicate column name: description"), so startup fails.
-- Recover by marking the real schema version clean, e.g.
--     migrate -path internal/database/migrations -database "sqlite3://<db>" force 46
-- (or UPDATE schema_migrations SET version = 46, dirty = 0), and verify first
-- that the tariff columns (description, features, badge, sort_order, version,
-- previous_id) and the products_new-free schema are present. Only use force 45
-- after the down has really completed.

ALTER TABLE products DROP COLUMN previous_id;
ALTER TABLE products DROP COLUMN version;
ALTER TABLE products DROP COLUMN sort_order;
ALTER TABLE products DROP COLUMN badge;
ALTER TABLE products DROP COLUMN features;
ALTER TABLE products DROP COLUMN description;

CREATE UNIQUE INDEX IF NOT EXISTS idx_products_plan_duration ON products(plan_id, duration_days);
