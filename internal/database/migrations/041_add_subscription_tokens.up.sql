-- Migration: 041_add_subscription_tokens
-- Token backfill and constraints are finalized by runMigrations with
-- crypto/rand after this nullable column has been added.

ALTER TABLE subscriptions ADD COLUMN token VARCHAR(64);
