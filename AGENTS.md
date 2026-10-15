# Instructions for AI Agents

## Before starting work

1. Activate the project in Serena:
   ```bash
   serena_activate_project(project="rs8kvn_bot")
   ```
2. Read the relevant Serena memories for context (architecture, code_style, test-info, etc.)
3. Отвечай всегда на русском
4. После окончания работы, если это требуется, обновляй документацию и память
5. Выбирай и применяй подходящие skills
6. Используй подходящие mcp-инструменты

## VPS Codex workflow

- Работай только в текущей git-ветке. Не переключай, не создавай и не удаляй ветки без явного запроса пользователя.
- Перед любыми изменениями проверяй `git status`. Не смешивай свои изменения с неизвестными или незавершёнными изменениями в рабочем дереве.
- Перед крупными изменениями изучи существующую архитектуру, связи компонентов и действующие соглашения проекта.
- Не удаляй рабочие компоненты, файлы или POC-артефакты без явного подтверждения пользователя.
- Не меняй DNS, SSL/TLS, firewall, системные и серверные настройки без отдельного разрешения пользователя.
- После изменений обязательно запускай проверки, соответствующие области изменения (как минимум релевантные тесты; при необходимости — полный набор проверок проекта).
- Перед commit покажи пользователю итоговый `git diff`.
- Делай commit только после успешного завершения обязательных проверок и явного запроса пользователя.

Текущий workflow:

```text
Developer request
→ ChatGPT architecture/review
→ VPS Codex implementation
→ tests
→ git commit
→ git push
```

## VPS Resource Constraints

Разработка выполняется на VPS с ограниченными ресурсами:

- 1 vCPU
- 2 GB RAM

Правила работы:

- Перед тяжёлыми командами проверяй доступную память.
- Не запускай одновременно несколько тяжёлых процессов.
- Не запускай параллельно:
  - `go test ./...`
  - `go test -race`
  - `golangci-lint`
  - `docker build`
  - несколько Codex-задач.
- Предпочитай последовательное выполнение проверок.
- Сначала используй узкие тесты затронутых пакетов, затем запускай полный набор.
- Полный regression-запуск выполняй только после успешных локальных проверок.
- Не создавай несколько VS Code Remote/Codex-сессий на одном VPS без необходимости.


## Git commits

- Коммит-сообщения — Conventional Commits: `feat:`, `fix:`, `docs:`, `test:`, `chore:` и т.п.
- НИКОГДА не добавлять авто-футеры вида «Generated with Codebuff» / «Co-Authored-By: …»
  в сообщения коммитов — только описание изменения.


## RTK - Rust Token Killer

**Usage**: Token-optimized CLI proxy for shell commands.

### Rule

Always prefix shell commands with `rtk` to minimize token consumption.

Examples:

```bash
rtk git status
rtk ls src/
rtk grep "pattern" src/
rtk find "*.rs" .
rtk docker ps
rtk gh pr list
```

## Codebase Knowledge Graph (codebase-memory-mcp)

This project uses codebase-memory-mcp to maintain a knowledge graph of the codebase.
ALWAYS prefer MCP graph tools over grep/glob/file-search for code discovery.

Project name in the graph: **`home-kereal-rs8kvn_bot`** (auto-generated from repo path).

### Priority Order
1. `search_graph` — find functions, classes, routes, variables by pattern
2. `trace_path` — trace who calls a function or what it calls
3. `get_code_snippet` — read specific function/class source code
4. `query_graph` — run Cypher queries for complex patterns
5. `get_architecture` — high-level project summary

### When to fall back to grep/glob
- Searching for string literals, error messages, config values
- Searching non-code files (Dockerfiles, shell scripts, configs)
- When MCP tools return insufficient results

### Examples
- Find a handler: `search_graph(project="home-kereal-rs8kvn_bot", name_pattern=".*Handler.*")`
- Who calls it: `trace_path(project="home-kereal-rs8kvn_bot", function_name="NewHandler", direction="inbound")`
- Read source: `get_code_snippet(project="home-kereal-rs8kvn_bot", qualified_name="home-kereal-rs8kvn_bot.internal.bot.handler.NewHandler")`
- Check architecture: `get_architecture(project="home-kereal-rs8kvn_bot")`


## Docs

Don't read and don't write
  * bypass_clients_comparison.md
  * bypass_research.md
  * marketing_strategy.md
  * nginx-xhttp-hysteria2-architecture.md
  * task-bot-integration.md


## Back-button navigation (CRITICAL — was broken once)

Screens with content in a SEPARATE message (QR photo, invite QR) + Back button:
**Open** = send new message, keep card underneath. **Back** = delete only that
message (its id comes in the callback), never re-show the card — re-sending
spawns a stray duplicate. Guard: `TestNavigation_OpenAndBack`
(`internal/bot/repro_qr_test.go`). Ref: `handleQRCode`/`handleBackToSubscription`.

## Error Handling Conventions

This project distinguishes between user-initiated operations (must be reliable) and background best-effort work (can tolerate partial failure).

**Subscription deletion policy**: subscriptions are NEVER physically deleted, except two cases:
1. admin `/del` — `SubscriptionService.DeleteByID` (the only product deletion path, backed by `DeleteSubscriptionByID`);
2. expired anonymous trials — `CleanupExpiredTrials` (trials are not real user subscriptions).
Everything else only changes subscription status (`revoked`, free-plan downgrade, reanimation). There is no `Delete(telegramID)` anymore — it was a dead duplicate.

- **User-initiated** (`Create`, `BindTrial`, `RenewSubscription`, `DeleteByID`): return errors to the caller. The handler will surface the failure to the user. Do NOT log + continue silently.
- **Access is local state (no external provisioning)**: there is no VPN panel, sync queue or retry worker. Create/renew/expire/plan-change only change DB state (status, plan, expiry) and invalidate caches; the `/sub` content is assembled on request by Subscription Builders from ProviderSource catalogues. A failure of a state change MUST be returned to the caller (the handler surfaces it to the user).
- **Delete flow** (`DeleteByID` only — admin `/del`): marks the subscription `revoked` with a journal record (so `/sub` answers 404 immediately), then physically deletes the row, then invalidates caches. If the delete fails the subscription stays `revoked`. There is no external deprovisioning step.
- **Background workers** (`TrialCleanupScheduler`, `SubscriptionExpireWorker`, `SubscriptionReminderWorker`, `ProviderFeeWorker`, `WarmupWorker`): best-effort. Per-item failures are logged as `Warn` and processing continues; a worker may return an aggregate error (`errors.Join`, e.g. `CleanupExpiredTrials`) so the caller can observe a degraded run. Only `context.Canceled`/`DeadlineExceeded` abort a scan early.
- **Provider sources on `/sub`**: a failure of one source is best-effort (the source is skipped). If no linked source can be fetched the request fails with `ErrProviderSourceUnavailable`; there is no fallback to a legacy pipeline.
- **Never** use `panic` for control flow in handlers or services. Panic recovery exists only at the top level (`main.go`, `handleUpdateSafely`).
- Always wrap errors with `%w` to preserve the chain for `errors.Is` / `errors.As` checks.
- Sentinel errors (`database.ErrSubscriptionNotFound`, `subserver.ErrProviderSourceUnavailable`) are the preferred way to signal expected "not found" states. Callers must use `errors.Is` to distinguish them from infrastructure errors.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
