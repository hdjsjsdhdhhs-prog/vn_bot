import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';

const offer = { offer_id: 'a'.repeat(32), name: 'Месяц свободы', duration_days: 30, amount_cents: 150, currency: 'XTR' };
const baseOrder = { ...offer, order_id: 'b'.repeat(32), status: 'pending', checkout_started: false, created_at: '2026-09-19T12:00:00Z', expires_at: '2026-09-19T12:30:00Z' };
const subscription = { status: 'active', expires_at: '2026-12-19T12:00:00Z', subscription_url: 'https://customer.example/sub/private-test', connection_url: 'https://customer.example/connect/private-test' };
async function fixture(page: Page, options: { missing?: boolean; expired?: boolean; noAuth?: boolean; failed?: boolean; unauthorized?: boolean; empty?: boolean; existingPurchase?: boolean; checkoutStarted?: boolean; lostCreate?: boolean } = {}) {
  let status = 'pending'; let creates = options.existingPurchase ? 1 : 0; let invoices = 0; let reads = 0;
  let checkoutStarted = options.checkoutStarted ?? false;
  const keys = new Set<string>();
  const currentOrder = () => ({ ...baseOrder, status, checkout_started: checkoutStarted });
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
    if (path.endsWith('/orders/recent')) return route.fulfill({ json: { orders: creates ? [currentOrder()] : [] } });
    if (path.endsWith('/orders')) {
      expect(route.request().postDataJSON()).toEqual({ offer_id: offer.offer_id });
      const key = route.request().headers()['idempotency-key'];
      expect(key).toMatch(/^[a-f0-9-]{36}$/);
      const replay = keys.has(key);
      if (!replay) { keys.add(key); creates++; }
      await new Promise(resolve => setTimeout(resolve, 150));
      if (options.lostCreate && !replay) return route.abort('failed');
      return route.fulfill({ status: replay ? 200 : 201, json: currentOrder() });
    }
    if (path.endsWith('/invoice')) {
      invoices++;
      expect(route.request().postData()).toBeNull();
      return route.fulfill({ json: { invoice_url: 'https://t.me/$test-invoice' } });
    }
    return route.fulfill({ json: currentOrder() });
  });
  return { paid: () => { status = 'paid'; }, state: (value: string) => { status = value; }, checkout: () => { checkoutStarted = true; }, creates: () => creates, invoices: () => invoices, reads: () => reads };
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
  // A click starts an asynchronous invoice request, not an already-open SDK UI.
  await page.waitForFunction(() => typeof (window as unknown as { testTelegram: { callback?: unknown } }).testTelegram.callback === 'function');
  await page.evaluate(value => {
    const state = (window as unknown as { testTelegram: { callback?: (status: string) => void } }).testTelegram;
    const onClose = state.callback!;
    state.callback = undefined;
    onClose(value);
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
test('Telegram launch parameters are consumed before hash routing', async ({ page }) => {
  await fixture(page);
  await page.goto('/miniapp/#tgWebAppData=signed-test-data&tgWebAppVersion=8.0&tgWebAppPlatform=android');
  await expect(page.getByRole('heading', { name: 'Ваш личный доступ' })).toBeVisible();
  await expect(page).toHaveURL(/\/miniapp\/#home$/);
});

test('lost purchase response replays the same key, then recovers without browser storage', async ({ page }) => {
  const backend = await fixture(page, { lostCreate: true });
  await page.goto(`/miniapp/#product/${offer.offer_id}`);
  await page.getByRole('button', { name: 'Перейти к покупке' }).click();
  await expect(page.getByRole('alert')).toContainText('Проверьте интернет');
  await page.getByRole('button', { name: 'Перейти к покупке' }).click();
  await expect(page.getByRole('button', { name: 'Оплатить 150 ★' })).toBeEnabled();
  expect(backend.creates()).toBe(1);
  await page.evaluate(() => sessionStorage.clear());
  await page.goto('/miniapp/#home'); await page.reload();
  await page.getByRole('link', { name: 'Продолжить покупку →' }).click();
  await expect(page.getByRole('button', { name: 'Оплатить 150 ★' })).toBeEnabled();
  expect(backend.creates()).toBe(1);
});

test('reopened expired approved checkout waits for delayed server settlement', async ({ page }) => {
  const backend = await fixture(page, { existingPurchase: true, checkoutStarted: true, expired: true });
  backend.state('expired');
  await page.goto('/miniapp/');
  await page.getByRole('link', { name: 'Продолжить покупку →' }).click();
  await expect(page.getByText('Срок счёта истёк')).toBeVisible();
  await expect(page.getByRole('button', { name: /Оплатить/ })).toHaveCount(0);
  backend.paid();
  await page.getByRole('button', { name: 'Проверить статус' }).click();
  await expect(page.getByText('Готово! Оплата подтверждена сервером.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Подключиться ↗' })).toBeVisible();
  expect(backend.invoices()).toBe(0);
});

test('pending invoice callback and repeated Pay never imply payment success', async ({ page }) => {
  const backend = await fixture(page);
  await purchase(page);
  await page.getByRole('button', { name: 'Оплатить 150 ★' }).dblclick();
  backend.checkout();
  await callback(page, 'pending');
  await expect(page.getByText('Платёж уже начат.', { exact: false })).toBeVisible();
  await expect(page.getByRole('button', { name: /Оплатить/ })).toHaveCount(0);
  await expect(page.getByText('Готово! Оплата подтверждена сервером.')).toHaveCount(0);
  expect(backend.invoices()).toBe(1);
  await page.reload();
  await expect(page.getByText('Платёж уже начат.', { exact: false })).toBeVisible();
  expect(backend.creates()).toBe(1);
});

test('empty catalog and profile do not manufacture products or purchases', async ({ page }) => {
  await fixture(page, { empty: true });
  await page.goto('/miniapp/#catalog');
  await expect(page.getByRole('heading', { name: 'Предложения появятся здесь' })).toBeVisible();
  await page.getByRole('link', { name: 'Профиль', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Покупок пока нет' })).toBeVisible();
});

test('loading subscription shows a status until the backend responds', async ({ page }) => {
  await fixture(page);
  let release!: () => void;
  const ready = new Promise<void>(resolve => { release = resolve; });
  await page.route('**/api/miniapp/subscription', async route => {
    await ready;
    await route.fulfill({ json: subscription });
  });
  await page.goto('/miniapp/');
  await expect(page.getByRole('status', { name: 'Загрузка' })).toBeVisible();
  release();
  await expect(page.getByRole('heading', { name: 'Ваш личный доступ' })).toBeVisible();
});

test('malformed successful catalog response shows an error and can recover', async ({ page }) => {
  await fixture(page);
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/api/miniapp/offers', route => route.fulfill({ json: { offers: null } }));
  await page.goto('/miniapp/#catalog');
  await expect(page.getByRole('alert')).toContainText('Не удалось прочитать ответ сервера');
  await page.unroute('**/api/miniapp/offers');
  await page.getByRole('button', { name: 'Попробовать снова' }).click();
  await expect(page.getByRole('link', { name: /Месяц свободы/ })).toBeVisible();
  expect(errors).toEqual([]);
});

test('late purchase creation does not pull the user back from another screen', async ({ page }) => {
  await fixture(page);
  let release!: () => void;
  const ready = new Promise<void>(resolve => { release = resolve; });
  await page.route('**/api/miniapp/orders', async route => {
    await ready;
    await route.fulfill({ status: 201, json: baseOrder });
  });
  await page.goto(`/miniapp/#product/${offer.offer_id}`);
  await page.getByRole('button', { name: 'Перейти к покупке' }).click();
  await page.getByRole('link', { name: 'Профиль', exact: true }).click();
  release();
  await expect(page.getByRole('link', { name: /Месяц свободы/ })).toBeVisible();
  await expect(page).toHaveURL(/#profile$/);
  // The committed order stays recoverable, even though navigation was abandoned.
  await page.getByRole('link', { name: /Месяц свободы/ }).click();
  await expect(page.getByRole('button', { name: 'Оплатить 150 ★' })).toBeEnabled();
});

test('tariff editor presentation: sort_order, badge, description, features and fallback card', async ({ page }) => {
  await fixture(page);
  const configured = { ...offer, offer_id: 'c'.repeat(32), name: 'Год без границ', amount_cents: 990, sort_order: 0, badge: 'Хит', description: 'Лучшая цена за год', features: ['Все серверы', 'Приоритетная поддержка'] };
  const legacy = { ...offer, sort_order: 1, description: '', features: [], badge: '' };
  // The server lists the configured tariff second; sort_order moves it first.
  await page.route('**/api/miniapp/offers', route => route.fulfill({ json: { offers: [legacy, configured] } }));
  await page.goto('/miniapp/#catalog');
  const cards = page.locator('a.offer-card');
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(0)).toContainText('Год без границ');
  await expect(cards.nth(0)).toContainText('Хит');
  await expect(cards.nth(0)).toContainText('Лучшая цена за год');
  await expect(cards.nth(0).getByRole('listitem')).toHaveText(['Все серверы', 'Приоритетная поддержка']);
  await expect(cards.nth(1)).toContainText('Месяц свободы');
  await expect(cards.nth(1).getByRole('listitem')).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await cards.nth(0).click();
  await expect(page.getByRole('heading', { name: 'Год без границ' })).toBeVisible();
  await expect(page.getByRole('listitem')).toHaveText(['Все серверы', 'Приоритетная поддержка']);
  await expect(page.getByText('✦ Без автоматических списаний')).toHaveCount(0);
  await page.goto(`/miniapp/#product/${offer.offer_id}`);
  await expect(page.getByText('✦ Без автоматических списаний')).toBeVisible();
});

const tokenA = 'c'.repeat(64);
const tokenB = 'd'.repeat(64);
const activeSub = (token: string) => ({ status: 'active', expires_at: '2026-12-19T12:00:00Z', subscription_url: `https://customer.example/sub/${token}`, connection_url: `https://customer.example/connect/${token}` });
const lastLink = (page: Page) => page.evaluate(() => (window as unknown as { testTelegram: { link: string } }).testTelegram.link);
const noScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

test('#connection at 390px: platform → app → official download → add via the external connection page', async ({ page }) => {
  await fixture(page);
  await page.route('**/api/miniapp/subscription', route => route.fulfill({ json: activeSub(tokenA) }));
  await page.goto('/miniapp/#connection');
  const os = page.getByRole('radiogroup', { name: 'Выберите систему' }).getByRole('radio');
  await expect(os).toHaveText(['iOS', 'Android', 'Windows', 'Linux', 'macOS']);
  const apps = page.getByRole('radiogroup', { name: 'Выберите приложение' }).locator('.app-name');
  for (const [label, names] of [['iOS', ['Happ', 'Инси (Incy)', 'V2RayTun']], ['Android', ['Happ', 'Инси (Incy)', 'V2RayTun']], ['Windows', ['Happ']], ['Linux', ['Happ']], ['macOS', ['Happ']]] as const) {
    await page.getByRole('radio', { name: label, exact: true }).click();
    await expect(page.getByRole('radio', { name: label, exact: true })).toHaveAttribute('aria-checked', 'true');
    await expect(apps).toHaveText([...names]);
    expect(await noScroll(page)).toBe(true);
  }
  // Desktop Happ: best-effort notice.
  await expect(page.getByText('запуск Happ по ссылке зависит от версии приложения', { exact: false })).toBeVisible();
  // Switching to a platform without the chosen app falls back to Happ.
  await page.getByRole('radio', { name: 'Android', exact: true }).click();
  await page.locator('[data-radio="app:v2raytun"]').click();
  await page.getByRole('radio', { name: 'Linux', exact: true }).click();
  await expect(page.locator('[data-radio="app:happ"]')).toHaveAttribute('aria-checked', 'true');
  await page.getByRole('radio', { name: 'Android', exact: true }).click();
  await page.locator('[data-radio="app:incy"]').click();
  await expect(page.getByRole('heading', { name: 'Установите Инси (Incy)' })).toBeVisible();
  await page.getByRole('button', { name: 'Google Play ↗' }).click();
  expect(await lastLink(page)).toBe('https://play.google.com/store/apps/details?id=llc.itdev.incy');
  await page.getByRole('button', { name: 'Добавить подписку в Инси (Incy)' }).click();
  // Variant A: Telegram opens THIS subscription's https page with os/app only.
  expect(await lastLink(page)).toBe(`https://customer.example/connect/${tokenA}#os=android&app=incy`);
  await expect(page.getByRole('status').filter({ hasText: 'Открываем страницу подключения' })).toBeVisible();
  await expect(page.getByText('добавлена', { exact: false })).toHaveCount(0);
  // Fallbacks stay: QR/instructions page and copy.
  await expect(page.getByRole('button', { name: 'Открыть подключение ↗' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Скопировать ссылку подписки' })).toBeVisible();
  await expect(page).toHaveURL(/\/miniapp\/#connection$/);
  expect(await page.evaluate(() => localStorage.getItem('dictatorvpn.vpn-client'))).toBe('{"platform":"android","app":"incy"}');
  expect(await noScroll(page)).toBe(true);
  await page.screenshot({ path: 'test-results/miniapp-connection-390.png', fullPage: true });
});

test('#subscriptions remembers the choice and each card uses its own subscription link', async ({ page }) => {
  await fixture(page);
  let current = tokenA;
  await page.route('**/api/miniapp/subscription', route => route.fulfill({ json: activeSub(current) }));
  await page.goto('/miniapp/#subscriptions');
  // No choice yet: ask once instead of guessing silently.
  await expect(page.getByRole('link', { name: 'Выбрать приложение' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Добавить подписку в/ })).toHaveCount(0);
  await page.getByRole('link', { name: 'Выбрать приложение' }).click();
  await page.getByRole('radio', { name: 'iOS', exact: true }).click();
  await page.locator('[data-radio="app:v2raytun"]').click();
  await page.getByRole('link', { name: 'Подписки', exact: true }).click();
  await expect(page.getByText('Устройство: iOS · V2RayTun · Изменить')).toBeVisible();
  await page.getByRole('button', { name: 'Добавить подписку в V2RayTun' }).click();
  expect(await lastLink(page)).toBe(`https://customer.example/connect/${tokenA}#os=ios&app=v2raytun`);
  // Another subscription arrives from the server: the button follows it.
  current = tokenB;
  await page.getByRole('button', { name: 'Обновить статус' }).click();
  await page.getByRole('button', { name: 'Добавить подписку в V2RayTun' }).click();
  const link = await lastLink(page);
  expect(link).toBe(`https://customer.example/connect/${tokenB}#os=ios&app=v2raytun`);
  expect(link).not.toContain(tokenA);
  // Survives a reload; storage never holds the bearer URLs.
  await page.reload();
  await expect(page.getByRole('button', { name: 'Добавить подписку в V2RayTun' })).toBeVisible();
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
  expect(stored).not.toContain(tokenA);
  expect(stored).not.toContain(tokenB);
  expect(stored).not.toContain('customer.example');
  // Home and the existing renewal/connection affordances are unchanged.
  await expect(page.getByRole('link', { name: 'Подключиться ↗' })).toBeVisible();
  expect(await noScroll(page)).toBe(true);
  await page.screenshot({ path: 'test-results/miniapp-subscriptions-390.png', fullPage: true });
});

test('unexpected connection URL degrades to the manual fallback, never a custom scheme', async ({ page }) => {
  await fixture(page);
  await page.goto('/miniapp/#connection');
  await page.getByRole('button', { name: /Добавить подписку в/ }).click();
  await expect(page.getByText('Не удалось открыть страницу подключения.', { exact: false })).toBeVisible();
  expect(await lastLink(page)).toBe('');
  await expect(page.getByRole('button', { name: 'Скопировать ссылку подписки' })).toBeVisible();
});

test('responsive layout at narrow and wide sizes', async ({ page }) => {
  await fixture(page); await page.goto('/miniapp/');
  for (const width of [320, 390, 768]) {
    await page.setViewportSize({ width, height: 800 });
    await expect(page.getByRole('heading', { name: 'Ваш личный доступ' })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
});
