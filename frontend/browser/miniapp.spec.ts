import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';

const offer = { offer_id: 'a'.repeat(32), name: 'Месяц свободы', duration_days: 30, amount_cents: 150, currency: 'XTR' };
const baseOrder = { ...offer, order_id: 'b'.repeat(32), status: 'pending', checkout_started: false, created_at: '2026-09-19T12:00:00Z', expires_at: '2026-09-19T12:30:00Z' };
const subscription = { status: 'active', expires_at: '2026-12-19T12:00:00Z', subscription_url: 'https://customer.example/sub/private-test', connection_url: 'https://customer.example/connect/private-test' };
async function fixture(page: Page, options: { missing?: boolean; expired?: boolean; noAuth?: boolean; failed?: boolean; unauthorized?: boolean; empty?: boolean } = {}) {
  let status = 'pending'; let creates = 0; let invoices = 0; let reads = 0;
  await page.addInitScript(({ noAuth }) => {
    const state = { invoiceCalls: 0, link: '', callback: undefined as undefined | ((status: string) => void), ready: false };
    Object.assign(window, { testTelegram: state, Telegram: { WebApp: {
      initData: noAuth ? '' : 'signed-test-data', initDataUnsafe: { user: { first_name: 'Аня', username: 'anya' } },
      colorScheme: 'dark', themeParams: {}, safeAreaInset: { top: 12, bottom: 16 }, contentSafeAreaInset: { top: 0, bottom: 0 },
      ready: () => { state.ready = true; }, expand: () => {}, close: () => {}, onEvent: () => {}, offEvent: () => {}, isVersionAtLeast: () => true,
      openInvoice: (_url: string, callback: (status: string) => void) => { state.invoiceCalls++; state.callback = callback; },
      openLink: (url: string) => { state.link = url; },
      BackButton: { show: () => {}, hide: () => {}, onClick: () => {}, offClick: () => {} },
    } } });
  }, { noAuth: options.noAuth });
  await page.route('https://telegram.org/**', route => route.fulfill({ body: '', contentType: 'application/javascript' }));
  await page.route('**/api/miniapp/**', async route => {
    reads++;
    expect(route.request().headers()['authorization']).toBe('tma signed-test-data');
    const path = new URL(route.request().url()).pathname;
    if (options.unauthorized) return route.fulfill({ status: 401, json: { error: 'unauthorized' } });
    if (options.failed) return route.fulfill({ status: 503, json: { error: 'service_unavailable' } });
    if (path.endsWith('/subscription')) return route.fulfill(options.missing ? { status: 404, json: { error: 'subscription_not_found' } } : { json: { ...subscription, status: options.expired && status !== 'paid' ? 'expired' : 'active' } });
    if (path.endsWith('/offers')) return route.fulfill({ json: { offers: options.missing || options.empty ? [] : [offer] } });
    if (path.endsWith('/orders/recent')) return route.fulfill({ json: { orders: creates ? [{ ...baseOrder, status }] : [] } });
    if (path.endsWith('/orders')) {
      creates++;
      expect(route.request().postDataJSON()).toEqual({ offer_id: offer.offer_id });
      expect(route.request().headers()['idempotency-key']).toMatch(/^[a-f0-9-]{36}$/);
      await new Promise(resolve => setTimeout(resolve, 150));
      return route.fulfill({ status: 201, json: { ...baseOrder, status } });
    }
    if (path.endsWith('/invoice')) {
      invoices++;
      expect(route.request().postData()).toBeNull();
      return route.fulfill({ json: { invoice_url: 'https://t.me/$test-invoice' } });
    }
    return route.fulfill({ json: { ...baseOrder, status } });
  });
  return { paid: () => { status = 'paid'; }, state: (value: string) => { status = value; }, creates: () => creates, invoices: () => invoices, reads: () => reads };
}
async function purchase(page: Page) {
  await page.goto('/miniapp/');
  await page.getByRole('link', { name: 'Каталог', exact: true }).click();
  await page.getByRole('link', { name: /Месяц свободы/ }).click();
  await page.getByRole('button', { name: 'Перейти к покупке' }).dblclick();
  await expect(page.getByRole('heading', { name: 'Ваша покупка' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Оплатить 150 ★' })).toBeEnabled();
}
async function callback(page: Page, status: string) {
  await page.evaluate(value => {
    const state = (window as unknown as { testTelegram: { callback: (status: string) => void } }).testTelegram;
    state.callback(value);
  }, status);
}

test('mobile home → catalog → purchase → Stars → pending → server paid → connection', async ({ page }) => {
  const backend = await fixture(page);
  await purchase(page);
  expect(backend.creates()).toBe(1);
  await page.getByRole('button', { name: 'Оплатить 150 ★' }).click();
  expect(backend.invoices()).toBe(1);
  await callback(page, 'paid');
  await expect(page.getByText('Telegram завершил оплату.', { exact: false })).toBeVisible();
  await expect(page.getByText('Оплата подтверждена сервером.', { exact: false })).toHaveCount(0);
  backend.paid();
  await page.getByRole('button', { name: 'Проверить статус' }).click();
  await expect(page.getByText('Готово! Оплата подтверждена сервером.')).toBeVisible();
  await page.getByRole('link', { name: 'Подключиться ↗' }).click();
  await page.getByRole('button', { name: 'Открыть подключение ↗' }).click();
  expect(await page.evaluate(() => (window as unknown as { testTelegram: { link: string } }).testTelegram.link)).toBe(subscription.connection_url);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.locator('body')).not.toContainText('private-test');
  await page.getByRole('link', { name: 'Профиль', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Аня' })).toBeVisible();
});

test('missing authentication never calls the API', async ({ page }) => {
  const backend = await fixture(page, { noAuth: true });
  await page.goto('/miniapp/');
  await expect(page.getByRole('heading', { name: 'Откройте из Telegram' })).toBeVisible();
  expect(backend.reads()).toBe(0);
});
test('invalid/expired authentication requests reopening, not client identity fallback', async ({ page }) => {
  await fixture(page, { unauthorized: true }); await page.goto('/miniapp/');
  await expect(page.getByText('Сессия завершилась.', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Закрыть Mini App' })).toBeVisible();
});
test('missing subscription stays an onboarding empty state, catalog stays empty', async ({ page }) => {
  await fixture(page, { missing: true }); await page.goto('/miniapp/');
  await expect(page.getByRole('heading', { name: 'Начнём с подключения' })).toBeVisible();
  await page.getByRole('link', { name: 'Каталог', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Предложения появятся здесь' })).toBeVisible();
});
test('expired subscription offers catalog, never client renewal', async ({ page }) => {
  await fixture(page, { expired: true }); await page.goto('/miniapp/#subscriptions');
  await expect(page.getByText('Срок закончился')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Выбрать доступ' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Подключиться ↗' })).toHaveCount(0);
});
test('backend failure presents a retry action', async ({ page }) => {
  await fixture(page, { failed: true }); await page.goto('/miniapp/');
  await expect(page.getByRole('alert')).toContainText('Сервис временно недоступен');
  await page.getByRole('button', { name: 'Попробовать снова' }).click();
  await expect(page.getByRole('alert')).toBeVisible();
});
test('cancelled and failed Telegram results do not settle purchases', async ({ page }) => {
  await fixture(page); await purchase(page);
  await page.getByRole('button', { name: 'Оплатить 150 ★' }).click(); await callback(page, 'cancelled');
  await expect(page.getByText('Окно оплаты закрыто.', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Оплатить 150 ★' })).toBeEnabled();
  await page.getByRole('button', { name: 'Оплатить 150 ★' }).click(); await callback(page, 'failed');
  await expect(page.getByText('Telegram сообщил об ошибке оплаты.', { exact: false })).toBeVisible();
  await expect(page.getByText('Готово! Оплата подтверждена сервером.')).toHaveCount(0);
});
test('server expired and canceled purchase states', async ({ page }) => {
  const backend = await fixture(page); await purchase(page);
  backend.state('expired'); await page.getByRole('button', { name: 'Проверить статус' }).click();
  await expect(page.getByText('Срок счёта истёк')).toBeVisible();
  backend.state('canceled'); await page.getByRole('button', { name: 'Проверить статус' }).click();
  await expect(page.getByText('Покупка отменена')).toBeVisible();
  await expect(page.getByRole('button', { name: /Оплатить/ })).toHaveCount(0);
});
test('responsive layout at narrow and wide sizes', async ({ page }) => {
  await fixture(page); await page.goto('/miniapp/');
  for (const width of [320, 390, 768]) {
    await page.setViewportSize({ width, height: 800 });
    await expect(page.getByRole('heading', { name: 'Ваш личный доступ' })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
});
