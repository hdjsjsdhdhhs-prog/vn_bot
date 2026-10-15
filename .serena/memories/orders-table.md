> **Внимание:** записи ниже о панелях 3x-ui/proxman, узлах (`nodes`, `subscription_nodes`), синхронизации, `internal/vpn`, `internal/xui` и панельных уведомлениях трафика — **исторические**: подсистема удалена миграцией `051_remove_legacy_nodes`. Актуальная модель: подписка — локальное состояние, содержимое `/sub` собирает Subscription Builder из ProviderSource (см. `doc/architecture.md`).

# Orders & Payment (Platega)

## Two-phase Provisioning
- `ConfirmOrderPaidCAS` — одна SQL-транзакция: CAS `orders.status='pending'→'paid'` + обновление подписки. Материализации `subscription_nodes` и внешней синхронизации больше нет (удалены миграцией 051).

## Web entrypoint
- `POST /payment/callback` (web.Server) — checks `web.PaymentConfig` first (else 503), then `X-MerchantId`/`X-Secret` (platega.VerifyHeaders), MaxBytesReader(256 KiB), UUID id, exact amount match.
- 503 fires when payments not configured; 401 on bad headers; 400 invalid body/UUID; 200 success.

## Service layer
- `OrderService.RequestPayment` products list confirm keyboard (`buy_product_{id}`), creates order row with status `pending`, calls `platega.Client.CreateTransaction` for the link.
- `OrderService.ConfirmPayment` (idempotent, CAS) → `NotifyPaidUser` (returns `0, "", nil` when `subscription.TelegramID<=0` so no telegram call is attempted).

## Admin notifications (TelegramAdminID, best-effort Markdown)
- On activation (`Activated=true`, only the pending→paid CAS caller) → `notifyAdminPaid`: tariff, formatted amount (`formatMoneyCents`), clickable buyer link (`utils.FormatUserLink` — `t.me/username` or `tg://user?id=…`), provider ID, sub/order IDs, expiry. Title distinguishes `🆕 Покупка подтверждена` vs `🔄 Продление подтверждено` — `isRenewal` captured **before** the CAS mutates `sub` (`PricePaidCents > 0 || ProductID != nil`).
- On `CHARGEBACKED` with `WasPaid=true` (money collected) → `notifyAdminChargeback`: tariff, amount, buyer link, access status (`понижен до бесплатного` / `сохранён` if another paid order exists). Sent after per-order payment lock release. This is the only admin notification sent on the success path; the dedicated chargeback alert already carries the outcome, so a separate integration-issue message is not raised.
- Every payment integration problem (mismatches, late/unknown callbacks, provider errors, DB failures, uncertain outcomes) → `NotifyPaymentIssue` → `notifyAdmin` (plain-text) + `payment_issues_total{event}`.
- Duplicate CONFIRMED/CHARGEBACKED callbacks are silent (idempotent no-op) — no repeated alerts.

## Bot integration
- `buy_premium_list` → renders products via `KeyboardBuilder.BuyProductList` (callbacks `buy_product_{N}`).
- `buy_product_{N}` → `OrderService.RequestPayment`, keyboard `BuyProductConfirm` with "Оплатить" (URL) + "← Назад" → `buy_premium_list`.

## Test contract
- `internal/web/payment_test.go`: 503 when PaymentConfig missing, 405 GET, sanity UUID validators, MaxBytesReader smoke.
- `internal/bot/keyboard_test.go`: §7.1 labels (`💎 Купить Premium`), callbacks (`buy_premium_list`, `buy_product_*`), `BuyProductList` rows.
