# Database Guide

## Overview

The service uses SQLite through GORM. The production database defaults to `./data/rs8kvn.db`, runs in WAL mode, and is mounted as persistent VPS storage. SQLite is intentionally configured as a single-writer database; do not increase write concurrency without measuring lock behaviour.

Schema changes are applied at startup by `golang-migrate`. SQL files under `internal/database/migrations/` are embedded into the binary with `go:embed`.

## Main entities

- `subscriptions` — Telegram users, VPN subscription identifiers, plan/product state, expiry, device/IP observations, and reminder flags.
- `nodes` — 3x-ui, proxman, or fetch endpoints and their credentials/configuration.
- `plans` and `plan_nodes` — plans and their many-to-many node assignment.
- `products` — purchasable durations and prices attached to plans.
- `orders` — payment lifecycle and provider identifiers.
- `subscription_nodes` — per-node provisioning state (`active`, `pending_add`, `pending_remove`, `pending_update`).
- `invites` and `trial_requests` — referrals and trial rate limiting.
- `broadcasts` — durable broadcast definition, audience snapshot, progress, and delivery report.

Refer to `internal/database/models.go` and the migrations for authoritative details. GORM structs are not a substitute for migration files.

## Migration rules

1. Add a new numbered `*.up.sql` migration; never edit an already released migration.
2. Add a matching `*.down.sql` whenever rollback can be made safe. If rollback is unsafe, document the recovery procedure explicitly.
3. Keep migration numbering monotonic. Migration `037` is intentionally absent in the current history; do not reuse it.
4. Update model constants and SQL constraints together. Status values are enforced in SQL.
5. Preserve data when rebuilding SQLite tables. Recreate indexes, constraints, and foreign keys explicitly.
6. Migrations that change `PRAGMA foreign_keys` require the repository's reviewed non-transactional migration path and schema recovery checks.
7. Never call `AutoMigrate` as a replacement for reviewed migrations.
8. Add or update migration tests in `internal/database/migration_test.go` or a focused test file.
9. Test both a clean database and upgrade from the previous schema version.
10. Before VPS deployment, create and verify a backup. A failed or dirty migration must stop deployment; do not force migration metadata without inspecting schema integrity.

## Data invariants

- Positive `telegram_id` values represent bound users; negative values represent unbound trials.
- Subscription deletion is exceptional. Ordinary lifecycle transitions preserve the row and use status changes; only the approved admin deletion flow and expired anonymous-trial cleanup physically delete subscriptions.
- `subscription_nodes` is the durable source of provisioning retries. DB setup is synchronous; external VPN synchronization is best-effort and retried by workers.
- Payment confirmation and subscription activation must remain atomic and idempotent.
- Node API tokens and payment-related values are sensitive. Never print them in logs, fixtures, documentation, or Pull Requests.

## Verification

```bash
go test -race -count=1 ./internal/database/...
go test -race -count=1 ./internal/service/...
go test -race -count=1 ./...
```

For a disposable database, start the application with test credentials or use existing migration tests. Never test migration changes against the only production database copy.

Useful production integrity checks after a backed-up upgrade:

```bash
sqlite3 ./data/rs8kvn.db 'PRAGMA integrity_check;'
sqlite3 ./data/rs8kvn.db 'PRAGMA foreign_key_check;'
```
