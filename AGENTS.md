# Instructions for AI Agents

This document is the operating contract for AI coding agents working in `rs8kvn_bot`. Preserve the established architecture and safety invariants. GitHub is the source of truth for code and repository state.

## Before starting work

1. Activate the project in Serena:
   ```bash
   serena_activate_project(project="rs8kvn_bot")
   ```
2. Read relevant Serena memories for architecture, code style, and tests.
3. Отвечай всегда на русском.
4. Update documentation and memory when the change requires it.
5. Select appropriate skills and MCP tools.
6. Inspect the current branch, latest `main`, recent commits, README, Go modules, Docker files, database migrations, tests, and affected architecture before writing.
7. For architecture changes or work touching more than five files, present a short plan and wait for approval.

## Project stack

- Go 1.25, module `github.com/kereal/rs8kvn_bot`.
- Telegram long polling via `go-telegram-bot-api/v5`.
- HTTP endpoints and server-rendered templates; there is no standalone frontend application.
- SQLite in WAL mode through GORM, with `mattn/go-sqlite3`/CGO.
- Embedded SQL migrations through `golang-migrate` and `go:embed`.
- VPN integrations: 3x-ui, proxman, and fetch sources.
- Zap/lumberjack logging, Prometheus metrics, Sentry, background schedulers.
- Docker/Compose deployment to a VPS; release images are built by GitHub Actions and published to GHCR.

## Architecture rules

- `cmd/bot` is the composition root and process lifecycle.
- `internal/bot` owns Telegram presentation and interaction flow.
- `internal/web` owns HTTP transport, health/readiness, invite pages, and payment callbacks.
- `internal/service` owns business transactions and orchestration.
- `internal/database` owns persistence, models, repositories, and migrations.
- `internal/vpn` and `internal/xui` own external VPN adapters.
- `internal/subserver` owns subscription aggregation and format conversion.
- `internal/scheduler` owns background workers; workers must honor context cancellation and isolate per-item failures when work is best-effort.
- Keep dependencies directed toward narrow interfaces. Do not move business rules into handlers, templates, `main`, or persistence helpers.
- Do not introduce a second implementation when an existing package can be extended safely.
- Preserve the web-to-bot dependency isolation and the narrow database service seam documented in `doc/adr/`.

## Coding standards

- Run `gofmt`; follow idiomatic Go and the repository's `.golangci.yml`.
- Keep functions focused, pass `context.Context` through I/O paths, and use explicit timeouts for external calls.
- Never use `panic` for control flow. Top-level recovery exists only as a final containment boundary.
- Wrap errors with `%w`. Use `errors.Is`/`errors.As` with sentinel errors for expected states.
- Do not log credentials, Telegram tokens, API tokens, payment secrets, subscription IDs, or unnecessary personal data.
- Preserve concurrency controls, idempotency, bounded retry/backoff, and graceful shutdown.
- Use bounded Prometheus labels only; never put user/order/subscription IDs or URLs into labels.
- Commit messages use Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `ci:`). Never add generated or `Co-Authored-By` footers.

## Git workflow

- `main` is production. Never commit or develop directly on `main`/`master`.
- Create a dedicated `feature/*`, `fix/*`, `docs/*`, `test/*`, `ci/*`, or `chore/*` branch from current `main`.
- Workflow: branch → Pull Request to `main` → CI → review → merge.
- Do not merge or deploy without explicit approval.
- Before opening a PR, inspect the final diff for unrelated changes and secrets.
- PR descriptions must list scope, affected files/components, exact verification, deployment/migration/configuration notes, risks, and rollback considerations.
- For large work, prefer small coherent PRs over one mixed architectural change.

## Database migration rules

- Add a new monotonic numbered migration in `internal/database/migrations`; never edit a migration that may have shipped.
- Migration `037` is intentionally absent; do not reuse it.
- Add a safe down migration when possible, or document why rollback requires backup restoration/manual recovery.
- Keep GORM models, status constants, SQL constraints, indexes, and foreign keys consistent.
- Do not use `AutoMigrate` as a replacement for reviewed SQL migrations.
- SQLite table rebuilds must preserve data and recreate all constraints/indexes. Changes involving `PRAGMA foreign_keys` must use the repository's explicit recovery-aware path.
- Add migration tests for fresh databases and upgrades. Dirty migration state must stop startup; never blindly force metadata.
- Back up the production database and verify integrity before migration deployment.

## Deployment rules

- Validate `Dockerfile`, both Compose files, required environment variables, persistent volumes, health checks, and rollback steps.
- Prefer immutable SemVer or commit-SHA images in production. Verify the GHCR owner: inherited files may still reference `kereal/rs8kvn_bot` while this repository is `hdjsjsdhdhhs-prog/vn_bot`.
- Keep `.env` out of Git and restrict it to mode `600` on the VPS.
- Preserve the non-root container, `no-new-privileges`, loopback port binding, and persistent `data/` volume.
- Before a VPS update: create and verify an off-host backup, pull the intended image, validate Compose, deploy, wait for health/readiness, inspect logs, and run a low-risk smoke test.
- Do not run multiple polling instances for one Telegram token.
- Database rollback and application rollback are separate decisions; never downgrade across incompatible migrations without a reviewed restore plan.

## Testing requirements

Run focused tests while iterating, then the complete relevant suite. Minimum checks for Go changes:

```bash
gofmt -l $(find . -type f -name '*.go' -not -path './.git/*')
go test -race -count=1 -timeout 5m ./...
go vet ./...
go build ./...
golangci-lint run ./...
```

For security, CI, Docker, migration, or deployment changes also run:

```bash
gosec ./...
docker compose config
docker compose -f docker-compose.local.yml config
docker build -t rs8kvn_bot:verify .
```

A non-empty `gofmt -l` result is a failure. Do not claim a command passed unless it actually ran successfully. Behaviour changes require tests; configuration changes require `.env.example` and deployment documentation updates.

## Autonomous AI workflow

1. Receive and restate the scoped outcome internally.
2. Analyze GitHub before modifying anything.
3. Identify architecture, data, security, environment, test, and VPS impact.
4. Present a plan and request confirmation when required.
5. Create a branch from latest `main`.
6. Implement the smallest coherent change without unrelated refactoring.
7. Run real checks; inspect failures rather than weakening tests.
8. Scan the diff for secrets and accidental generated files.
9. Commit and open a PR to `main`.
10. Report changed/created files, verification, risks, and VPS instructions.
11. Stop and ask when requirements, access, migration safety, or production state are uncertain.

Notion Agent coordinates user intent. GitHub MCP is used for repository reads/writes and PR operations. A VPS AI Engineer may run deployment checks only when explicitly authorized, using reversible commands and a verified backup. GitHub remains the canonical source.

## RTK - Rust Token Killer

Always prefix supported shell commands with `rtk` when it is installed to minimize token consumption, for example:

```bash
rtk git status
rtk ls internal/
rtk grep "pattern" internal/
rtk docker ps
rtk gh pr list
```

Do not invent `rtk` support in CI or environments where it is not installed.

## Codebase Knowledge Graph

This project uses `codebase-memory-mcp`. Prefer graph tools for code discovery:

1. `search_graph`
2. `trace_path`
3. `get_code_snippet`
4. `query_graph`
5. `get_architecture`

Graph project name: `home-kereal-rs8kvn_bot`. Fall back to grep/glob for string literals, errors, configuration/non-code files, or insufficient graph results.

The repository may also contain `graphify-out/`. When available, use `graphify query`, `graphify path`, or `graphify explain` before broad source browsing; use the generated wiki for navigation. After code changes run `graphify update .` when the tool is installed. Dirty generated graph files are not by themselves a reason to skip graph queries.

## Documentation exclusions

Do not read or write these documents unless the user explicitly scopes the task to them:

- `bypass_clients_comparison.md`
- `bypass_research.md`
- `marketing_strategy.md`
- `nginx-xhttp-hysteria2-architecture.md`
- `task-bot-integration.md`

## Critical product invariants

### Back-button navigation

Screens whose content is sent as a separate message (QR photo or invite QR) keep the original card underneath. Open sends a new message. Back deletes only the callback message and must not re-send the card. Preserve `TestNavigation_OpenAndBack` in `internal/bot/repro_qr_test.go` and the `handleQRCode`/`handleBackToSubscription` contract.

### Subscription deletion

Subscriptions are never physically deleted except:

1. admin `/del` through `SubscriptionService.DeleteByID`/`DeleteSubscriptionByID`;
2. cleanup of expired anonymous trials.

Other flows revoke, downgrade, or reanimate records.

### Provisioning reliability

Provisioning has two phases:

- DB setup (`GetNodesByPlanID`, `MarkActiveNodesPendingUpdate`, `ReconcilePlanNodes`) is a structural prerequisite and errors must be returned to the initiating caller.
- External synchronization (`SyncSubscription`) is best-effort; pending state remains durable and background workers retry it.

Admin deletion is also phased: mark revoked, deprovision best-effort, then physically delete only through the approved path. Background scans log per-item failures and continue; aggregate degraded runs may be returned, while context cancellation aborts promptly.

### Traffic notifications

Traffic notification processing is best-effort and applies only to traffic-limited plans. Never re-enable a client whose quota is still exceeded. Re-enable only after counters reset below the limit and the panel client remains disabled. Preserve the `traffic_reminders_sent` idempotency bitmask.
