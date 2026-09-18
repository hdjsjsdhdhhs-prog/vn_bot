# Customer subscription management (application contract)

`service.NewSubscriptionManagement(subscriptionService, authorizer)` exposes:

- `Current(ctx) (*SubscriptionManagementInfo, error)` — fresh, read-only view of the customer's subscription in **any** state. Missing rows return `database.ErrSubscriptionNotFound`; reads never create, revive or downgrade access.
- `Renew(ctx, days) (*CustomerSubscriptionInfo, error)` — an explicitly authorized access grant. Returns the committed safe snapshot with updated expiry and unchanged URLs. There is no fallible management re-read after commit. Use `Current` to obtain a fresh eligibility preview.

## Identity and authorization

`SubscriptionAuthorizer(ctx, action, days)` is supplied by trusted application wiring. It resolves the existing positive Telegram identity from verified caller context; `telegram_id` is unique in the current schema. Management methods accept neither a target subscription/customer ID nor a bearer token. No Users table or additional identity store is introduced.

The adapter must distinguish `SubscriptionManagementRead` (`days=0`) from `SubscriptionManagementRenew` and authorize the **specific duration** being granted. Knowing a customer's identity, authenticating as that customer, or holding a `/sub/` token is not by itself permission to grant access days. Missing adapters, adapter errors and zero/negative identities fail closed with `service.ErrSubscriptionAccessDenied`.

Adapters for bot/Mini App/admin/confirmation callers must verify their own trusted context. The Mini App read adapter is now wired in `web.Server.Start`: it verifies Telegram initData, places only the authenticated user ID into a private context key, and calls `Current` using a startup-bound read-only policy. See [Telegram Mini App API](api.md#11-telegram-mini-app-authentication). It never authorizes `Renew`, including for the configured bot administrator. Do not implement an adapter that trusts arbitrary request IDs, public tokens or unsigned client claims. Construct management once with trusted policy code, not with a callback chosen by the requesting customer.

## Customer representation

`Current` returns (illustrative token and date):

```json
{
  "status": "active",
  "expires_at": "2026-10-18T12:00:00Z",
  "subscription_url": "https://customer.example/sub/<token>",
  "connection_url": "https://customer.example/connect/<token>",
  "renewal": {
    "eligible": true,
    "operation": "extend_days",
    "min_days": 1,
    "max_days": 3650
  }
}
```

`Renew` returns the same four subscription fields, without `renewal`. No internal IDs, separate token field, provider metadata, HWID, headers or credentials are serialized. Both URLs remain bearer credentials and must not be logged. The connection URL uses `/connect/<token>` on the configured `GLOBAL_SUB_URL` origin, matching the existing root web route; deployment must serve both routes on that origin.

## Lifecycle reuse and consistency

- Effective status is shared with `/subscription-info/` and `/connect/`, including expiry before the worker runs.
- Eligibility and subscription state are read in one database snapshot without a new cache. Eligibility calls the same internal renewal policy/expiry helper as the write transaction; it is a one-day capability preview, **not authorization**. Ineligible subscriptions return `{"eligible":false}`. DB failures are errors, not a false eligibility result.
- `max_days` is the existing per-call input limit. Eligibility is advisory: the requested duration must still fit the supported timestamp range, and current state/source validity are checked again during the write.
- Customer-scoped renewal selects the owner inside the existing SQLite renewal transaction, avoiding an ownership lookup/write race. It uses the same calculation, ProviderSource validation and post-commit cache invalidation as trusted ID-based renewal. ProviderSource and legacy lifecycle rules are unchanged.
- Creation remains `SubscriptionService.Create` with trusted creation terms; management reads do not implicitly create access.
- Expected errors remain identifiable with `errors.Is`: access denied, subscription not found, invalid days, not renewable, cancellation/deadline, or management unavailable. Error text is sanitized; wrapped causes are for trusted diagnostics only and must not be reflected or logged unredacted.

## Limits

The Mini App exposes only authenticated reads of this contract; no HTTP renewal/grant endpoint, UI, payment processing or automatic billing is introduced. The existing public bearer routes stay read-only. Each successful `Renew` call adds days; future payment/queue callers must implement deduplication before calling it, rather than treating this operation as an idempotent payment API.

## Focused verification

CGO/SQLite required. Run sequentially:

```bash
go test -p 1 ./internal/database ./internal/service ./internal/web -run '^TestSubscriptionManagement' -count=1 -timeout=120s
go test -p 1 ./internal/database ./internal/service ./internal/scheduler -run '^TestSubscriptionRenewal|^TestGetPublicSubscriptionInfo' -count=1 -timeout=120s
```

Tests cover customer isolation and missing/invalid identity, separate duration-specific grant authorization, state/source eligibility, legacy behavior, read freshness, rollback, sanitized errors, mixed concurrent management/ID renewal, migrated-schema uniqueness and the three real local HTTP public endpoints after active/expired renewal. HTTP checks cover warmed feed-cache invalidation, stable URLs/token, read-only methods, presentation `no-store` headers and credential/log boundaries.
