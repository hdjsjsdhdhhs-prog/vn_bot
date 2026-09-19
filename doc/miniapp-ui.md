# Telegram Mini App UI

## Audit at 62999c7 (before implementation)

The repository has Go HTML templates, no SPA, package.json or frontend tooling.
`internal/web` owns routing; templates are embedded into the Go binary. Purchase
Foundation and Stars already own pricing, ownership, eligibility and settlement.

| UI need | Existing contract |
| --- | --- |
| Authentication | Telegram WebApp.initData, `Authorization: tma <raw initData>` on every request; no cookies. Server validity is five minutes; reopen Mini App on 401. initDataUnsafe is display-only. |
| Current subscription | GET /api/miniapp/subscription; status, expires_at, subscription_url, connection_url, renewal preview (NOT grant authority). 404 subscription_not_found is an empty state. |
| Catalog / product | GET /api/miniapp/offers; offer_id, name, duration_days, amount_cents, currency, optional available_until. Detail uses the same DTO. |
| Purchase | POST /api/miniapp/orders, JSON containing only offer_id, UUID-v4 Idempotency-Key. |
| Invoice | POST /api/miniapp/orders/{reference}/invoice, no body/query; invoice_url. |
| Payment | Telegram.WebApp.openInvoice(invoice_url); callback is only a hint to re-fetch, never proof of payment. |
| Result | GET /api/miniapp/orders/{reference}; pending / paid / expired / canceled. Only server paid means settlement. |
| Connection | Use supplied connection_url for existing /connect/{token} page (QR / import instructions); copy subscription_url only on explicit user action. |
| Subscription states | active / expired / revoked / paused / canceled. No client renewal endpoint. |

For XTR, amount_cents is an integer number of Stars, not cents. Other currencies
are not payable through the Stars adapter. Catalog eligibility is server-owned.
Missing subscriptions are onboarded through the existing bot; a read must never
create one. The schema has one subscription per Telegram identity, so “My
subscriptions” shows that subscription, not fictional multi-subscription support.

## Concrete gaps and minimal changes

1. **Purchase recovery screen:** after losing a WebView, no order reference is
   available. Existing GET-by-reference cannot discover a pending or recently
   settled purchase; retry with a new key returns purchase_pending. Add
   GET /api/miniapp/orders/recent returning `{orders: [...]}` (latest 20, explicitly
   not full history), verified buyer AND current subscription owner, using the
   existing safe DTO and server expiry semantics. No new write API or migration.
2. **Safe payment retry:** after a reserved pre-checkout, another checkout is
   deliberately rejected. Add `checkout_started` boolean to the safe order DTO;
   no checkout ID or payment credentials. UI displays waiting instead of a second
   Pay button, including after reopening. Invoice endpoint still validates.
3. **Frontend delivery:** mount embedded static app at /miniapp/; preserve existing
   API and public connection pages. Allow Telegram Web iframe origins via scoped
   CSP instead of global X-Frame-Options DENY for the app only.

## Architecture

TypeScript + Vite, no runtime framework/router/store dependency. Hash routes
(home, catalog, product, purchase, subscriptions, connection, profile), small DOM
components using textContent, typed API client with bounded fetch and no automatic
POST retries. One controller owns request state and synchronously locks purchase
and invoice actions. Same-origin API, no credentials in URLs, storage or logs.
Only an unconfirmed offer/idempotency pair is kept in sessionStorage for ambiguous
network retries; it contains no identity, initData, subscription URL or credential.
Every replay is still authorized by the backend. Recent purchases restore state
on reopen. Polls are sequential, bounded and paused while hidden; manual refresh
remains available. 401 clears sensitive in-memory state and asks to reopen.

Mobile-first CSS, Telegram theme/safe-area events, reduced motion, keyboard focus,
large targets, skeleton/error/empty/success states, bottom navigation and Telegram
BackButton. Profile name is display-only from Telegram, not an API identity.

## Build and deployment

Frontend lives in `frontend/`. Commands from the repository root:

```
npm --prefix frontend ci
npm --prefix frontend run lint
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix frontend run build
```

Production output is embedded from `internal/web/miniapp_dist/`. Build frontend
before Go; Docker has a separate Node build stage. Configure the bot's Mini App
URL as `https://<existing-web-origin>/miniapp/` via BotFather. Same HTTPS origin
must serve /api/miniapp/. No server/DNS settings are changed by this work.
The existing Stars backend requires active XTR offers and wired Telegram services.
Never publish tokens, initData or connection URLs in analytics/error reporting.

Local tests simulate Telegram SDK and HTTP responses; Go tests exercise real
SQLite/authentication/Stars settlement with a local Bot API server. Live Telegram
WebView / real Stars acceptance requires an operator-run smoke test; simulated
callbacks are not verification of a real payment.

## Checkpoint follow-up (6d3373c)

The checkpoint was incomplete, not a release acceptance marker:

| Area | Initial classification | Follow-up |
| --- | --- | --- |
| TypeScript/Vite, screens, auth adapter, purchase and Stars integration | PARTIAL | Kept the architecture and existing backend contracts; exercised the production bundle in Chromium. |
| Browser API requests | BROKEN | Detached the stored fetch function before calling it: binding native fetch to the Api instance caused `Illegal invocation` in Chromium despite passing Node-based unit tests. |
| Recent-purchase HTTP tests | BROKEN | Historical fixtures now supply required deadlines at creation instead of modifying immutable purchase terms; production DB triggers remain enabled. |
| Concurrent requests and invoice navigation | PARTIAL | Older subscription/history responses cannot replace settlement state; old order reads cannot release a newer read's lock; leaving a purchase stops polling and suppresses delayed invoices. SDK callbacks remain attached to their own purchase. |
| Retry and reopen | PARTIAL | Keep idempotency keys on ambiguous HTTP 408/429; test lost responses and recovery without session storage. Home also offers recovery for expired, approved checkouts awaiting settlement. |
| Mobile launch and theme | PARTIAL | Consume Telegram launch hash after SDK initialization; handle all four safe-area edges and remove obsolete theme overrides. |
| Server payment authority and ownership | DONE for audited contracts | Retain verified initData, buyer + current-owner filtering, bounded safe recent DTO, server-only settlement. Invoice preparation now rejects already-reserved checkouts under the existing DB transaction. |
| Real Telegram / VPN acceptance and final Docker image | MISSING verification | Docker build and Linux embedded-UI tests now pass. Live Telegram/VPN acceptance still requires the release checks below. |

Browser tests run `vite build` then `vite preview`, not the development server.
Run `npm run test:browser` from `frontend/`; if Chromium is missing, run
`npx playwright install chromium` there first. Tests cover launch parameters,
loading/empty/error states, mobile navigation, duplicate Pay, failed/cancelled/
pending SDK signals, lost responses, reopen and delayed settlement. The Go
Stars HTTP integration test uses signed initData, real SQLite and bot update
routing, then reads recent orders and the subscription API and validates the
existing connection page's QR against the returned subscription URL.

### Windows verification notes

- This environment's `npm --prefix frontend ...` incorrectly selected the root
  package. Run npm with cwd set to `frontend` instead; no package.json change is
  necessary.
- Vitest loaded duplicate runner contexts with lowercase `d:` here. Using the
  canonical cwd from Node `fs.realpathSync.native('frontend')` fixed the actual
  test run without changing dependency versions or skipping tests.
- Playwright's readiness requests went through the environment proxy and got
  HTTP 503. Set `NO_PROXY=127.0.0.1,localhost` (and lowercase `no_proxy` when
  needed) for the test process only. Do not change production networking.
- Go/SQLite tests use the existing `tests\\run-stars-tests.cmd` helper (CGO and
  MSYS2 UCRT gcc), with packages executed sequentially using `-p 1`.
- A full `go test ./... -count=1` was attempted and **failed** outside the Mini
  App scope: open-file rename/cleanup failures on Windows, stale migration
  expectations (41 vs current 43), older token fixtures, and an `expires_now`
  timing assertion. The full regression is not green; focused passing tests
  must not be reported as a complete regression pass.
- Docker initially had no running daemon; a subsequent build hit a temporary
  `storage.googleapis.com` DNS timeout. Retrying the unchanged Dockerfile
  completed successfully, including frontend build, Linux Go/CGO compilation,
  UPX and final image export. No DNS settings were changed.

### Observed automated verification (2026-09-19)

- `npm install`, lint, typecheck, production build: passed; package manifests and
  lockfile unchanged. Vitest: **47 passed**. Playwright Chromium: **15 passed**.
- Windows: complete `internal/web`, `internal/telegramauth`,
  `internal/telegramstars` package tests passed. Focused MiniApp/Purchase/Stars/
  SubscriptionManagement/navigation tests passed across web/service/database/bot.
- Native CGO build of `./cmd/bot` passed after rebuilding frontend assets.
- `docker build -t rs8-miniapp-verification .`: passed.
- Builder image from the same Dockerfile: `go test -p 1 ./internal/web -run
  MiniApp -count=1` passed under Linux with networking disabled. This includes
  production embedded HTML/CSS/JS serving and signed-auth purchase settlement.
- Full Windows regression: **FAILED**, as detailed above. Not replaced by a
  claim that the narrower Linux run is a full regression.

### Release acceptance still required

1. Smoke-test `/miniapp/` and its hashed assets in the deployed production
   service. The image has been built, but the live service was not started.
2. Resolve the unrelated full-regression failures or record an explicit release
   decision; do not disable assertions to get a green run.
3. Open from the configured Telegram bot in native mobile and Telegram Web.
   Check safe areas, Back, invoice cancellation, then an operator-authorized real
   Stars payment. Closing and reopening must recover the same server order.
4. Verify that official payment settlement produces the subscription, that
   provisioning finishes, and that the connection URL/QR imports into a real VPN
   client. Until then, live payment and connection are **UNVERIFIED**.
