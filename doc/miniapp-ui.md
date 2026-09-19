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
