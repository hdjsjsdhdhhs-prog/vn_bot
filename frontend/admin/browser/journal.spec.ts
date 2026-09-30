import { test, expect } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

// Journal page of the browser admin panel against a mock backend that follows
// the real contract of web.adminAPI.journalList / database.ListJournal:
//   order     newest first (created_at DESC, id DESC)
//   q         numeric → exact telegram_id or subscription_id, else username substring
//   type, plan_kind, from (inclusive), to (exclusive), offset; server page size
// The journal is read-only: the page must never send anything but GETs.

const CSRF = 'c'.repeat(43);
const PAGE_SIZE = 3;

interface Wire {
  id: number; created_at: string; event_type: string; outcome: string; actor: string; actor_name: string;
  telegram_id: number; username: string; subscription_id: number | null; plan_id: number | null; plan_name: string;
  plan_kind: string; order_id: number | null; amount_cents: number | null; currency: string | null; description: string;
  details: Record<string, unknown>;
}

function event(over: Partial<Wire> & { id: number; created_at: string; event_type: string }): Wire {
  return {
    outcome: 'success', actor: 'user', actor_name: '', telegram_id: 1001, username: 'anya', subscription_id: 42,
    plan_id: 1, plan_name: 'free', plan_kind: 'free', order_id: null, amount_cents: null, currency: null,
    description: 'Описание', details: {}, ...over,
  };
}

const EVENTS: Wire[] = [
  event({ id: 1, created_at: '2026-09-01T10:00:00Z', event_type: 'user_registered', description: 'Пользователь зарегистрирован, подписка подключена',
    details: { after: { status: 'active', expires_at: null, plan_id: 1, plan_name: 'free' } } }),
  event({ id: 2, created_at: '2026-09-05T09:00:00Z', event_type: 'trial_started', telegram_id: -77, username: '', subscription_id: 50,
    plan_id: 2, plan_name: 'trial', plan_kind: 'trial', description: 'Активирована пробная подписка' }),
  event({ id: 3, created_at: '2026-09-10T12:00:00Z', event_type: 'payment_succeeded', actor_name: 'platega', plan_id: 3,
    plan_name: 'Премиум', plan_kind: 'paid', order_id: 900, amount_cents: 29900, currency: 'RUB',
    description: 'Оплата заказа #900 прошла успешно', details: { provider: 'platega' } }),
  event({ id: 4, created_at: '2026-09-10T12:00:00Z', event_type: 'paid_activated', actor_name: 'platega', plan_id: 3,
    plan_name: 'Премиум', plan_kind: 'paid', order_id: 900, amount_cents: 29900, currency: 'RUB',
    description: 'Активирована платная подписка «Премиум месяц» на 30 дн.',
    details: {
      before: { status: 'active', expires_at: null, plan_id: 1, plan_name: 'free' },
      after: { status: 'active', expires_at: '2026-10-10T12:00:00Z', plan_id: 3, plan_name: 'Премиум' },
    } }),
  event({ id: 5, created_at: '2026-09-12T08:30:00Z', event_type: 'subscription_disabled', actor: 'admin', actor_name: 'admin',
    telegram_id: 2002, username: 'boris', subscription_id: 43, description: 'Администратор отключил подписку',
    details: { before: { status: 'active', expires_at: null, plan_id: 1 }, after: { status: 'paused', expires_at: null, plan_id: 1 } } }),
  event({ id: 6, created_at: '2026-09-15T00:00:00Z', event_type: 'payment_failed', outcome: 'failed', actor: 'system',
    actor_name: 'platega', telegram_id: 2002, username: 'boris', subscription_id: 43, plan_id: 3, plan_name: 'Премиум',
    plan_kind: 'paid', order_id: 901, amount_cents: 29900, currency: 'RUB', description: 'Оплата заказа #901 не прошла: платёж отменён провайдером' }),
  event({ id: 7, created_at: '2026-09-20T18:00:00Z', event_type: 'subscription_expired', actor: 'system', plan_id: 3,
    plan_name: 'Премиум', plan_kind: 'paid', description: 'Срок подписки истёк, подписка переведена на бесплатный тариф' }),
];

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

async function backend(page: Page, events: Wire[] = EVENTS) {
  const calls: { method: string; path: string; params: URLSearchParams }[] = [];
  const problems: string[] = [];
  await page.route('**/admin/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();
    calls.push({ method, path: url.pathname, params: url.searchParams });
    if ((url.pathname === '/admin/login' || url.pathname === '/admin/session') && method === 'GET') {
      return json(route, 200, { authenticated: true, csrf_token: CSRF });
    }
    if (method !== 'GET') {
      problems.push(`unexpected ${method} ${url.pathname}`);
      return json(route, 405, { error: 'method_not_allowed' });
    }
    if (url.pathname !== '/admin/api/journal') {
      problems.push(`unexpected GET ${url.pathname}`);
      return json(route, 404, { error: 'not_found' });
    }
    const p = url.searchParams;
    const known = ['q', 'type', 'plan_kind', 'from', 'to', 'offset', 'limit'];
    const unknown = [...p.keys()].filter(key => !known.includes(key));
    if (unknown.length) problems.push(`unknown params ${unknown.join(',')}`);
    for (const bound of ['from', 'to']) {
      const value = p.get(bound);
      if (value !== null && !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(value)) return json(route, 400, { error: 'invalid_request' });
    }
    const q = (p.get('q') ?? '').trim();
    const numeric = /^[0-9]+$/.test(q) ? Number(q) : null;
    const from = p.get('from');
    const to = p.get('to');
    const filtered = events
      .filter(e => !p.get('type') || e.event_type === p.get('type'))
      .filter(e => !p.get('plan_kind') || e.plan_kind === p.get('plan_kind'))
      .filter(e => !from || Date.parse(e.created_at) >= Date.parse(from))
      .filter(e => !to || Date.parse(e.created_at) < Date.parse(to))
      .filter(e => !q || (numeric !== null ? e.telegram_id === numeric || e.subscription_id === numeric
        : e.username.toLowerCase().includes(q.replace(/^@/, '').toLowerCase())))
      .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at) || b.id - a.id);
    const offset = Number(p.get('offset') ?? 0);
    return json(route, 200, { events: filtered.slice(offset, offset + PAGE_SIZE), total: filtered.length, limit: PAGE_SIZE, offset });
  });
  return {
    journalCalls: () => calls.filter(call => call.path === '/admin/api/journal'),
    last: () => calls.filter(call => call.path === '/admin/api/journal').at(-1)!.params,
    problems: () => problems,
  };
}

const table = (page: Page) => page.getByRole('table', { name: 'Журнал событий, от новых к старым' });
const rows = (page: Page) => table(page).locator('tbody tr');
const eventNames = (page: Page) => table(page).locator('.journal-open');

async function openJournal(page: Page) {
  await page.goto('/#/audit');
  await expect(page.getByRole('heading', { level: 1, name: 'Журнал' })).toBeVisible();
  await expect(rows(page).first()).toBeVisible();
}

test('shows events newest first with user, event, subscription, actor and status', async ({ page }) => {
  const api = await backend(page);
  await openJournal(page);

  await expect(page.getByText('Журнал появится на следующем этапе')).toHaveCount(0);
  await expect(eventNames(page)).toHaveText(['Подписка истекла', 'Неуспешная оплата', 'Подписка отключена']);
  await expect(page.getByText('Всего: 7')).toBeVisible();

  const first = rows(page).nth(0);
  await expect(first).toContainText('20.09.2026, 18:00');
  await expect(first).toContainText('@anya');
  await expect(first).toContainText('Срок подписки истёк');
  await expect(first).toContainText('#42');
  await expect(first).toContainText('Премиум · Платная');
  await expect(first).toContainText('Система');
  await expect(first).toContainText('Успешно');

  const failed = rows(page).nth(1);
  await expect(failed).toContainText('Ошибка');
  await expect(failed).toContainText('Platega');
  await expect(rows(page).nth(2)).toContainText('Администратор');
  expect(api.problems()).toEqual([]);
});

test('pages through the journal with the server page size', async ({ page }) => {
  const api = await backend(page);
  await openJournal(page);
  const nav = page.getByRole('navigation', { name: 'Страницы журнала' });
  await expect(nav.getByText('Страница 1 из 3')).toBeVisible();

  await nav.getByRole('button', { name: 'Следующая' }).click();
  // Equal timestamps: the newer row (activation, id 4) comes before the payment (id 3).
  await expect(eventNames(page)).toHaveText(['Платная подписка активирована', 'Успешная оплата', 'Пробная подписка активирована']);
  expect(api.last().get('offset')).toBe('3');
  await nav.getByRole('button', { name: 'Следующая' }).click();
  await expect(eventNames(page)).toHaveText(['Регистрация']);
  await expect(page.getByText('С 7 по 7 из 7')).toBeVisible();
  await nav.getByRole('button', { name: 'Предыдущая' }).click();
  await expect(nav.getByText('Страница 2 из 3')).toBeVisible();
  expect(api.problems()).toEqual([]);
});

test('filters by event type, subscription type, user and period', async ({ page }) => {
  const api = await backend(page);
  await openJournal(page);

  await page.getByLabel('Тип события').selectOption('payment_succeeded');
  await expect(eventNames(page)).toHaveText(['Успешная оплата']);
  expect(api.last().get('type')).toBe('payment_succeeded');
  expect(api.last().has('offset')).toBe(false);

  await page.getByLabel('Тип события').selectOption('');
  await page.getByLabel('Тип подписки').selectOption('trial');
  await expect(eventNames(page)).toHaveText(['Пробная подписка активирована']);
  await expect(rows(page).first()).toContainText('Анонимный посетитель');
  expect(api.last().get('plan_kind')).toBe('trial');

  await page.getByLabel('Тип подписки').selectOption('');
  await page.getByLabel('Поиск по пользователю').fill('@boris');
  await expect(eventNames(page)).toHaveText(['Неуспешная оплата', 'Подписка отключена']);
  expect(api.last().get('q')).toBe('@boris');
  await page.getByLabel('Поиск по пользователю').fill('');
  await page.getByLabel('Поиск по пользователю').press('Enter');
  await expect(page.getByText('Всего: 7')).toBeVisible();

  // Calendar days in the browser time zone (UTC here); the end day is inclusive.
  await page.getByLabel('С', { exact: true }).fill('2026-09-10');
  await page.getByLabel('по', { exact: true }).fill('2026-09-12');
  await expect(eventNames(page)).toHaveText(['Подписка отключена', 'Платная подписка активирована', 'Успешная оплата']);
  expect(api.last().get('from')).toBe('2026-09-10T00:00:00Z');
  expect(api.last().get('to')).toBe('2026-09-13T00:00:00Z');

  const before = api.journalCalls().length;
  await page.getByLabel('С', { exact: true }).fill('2026-09-20');
  await expect(page.getByText('Начало периода позже его окончания.')).toBeVisible();
  expect(api.journalCalls().length).toBe(before);

  await page.getByLabel('по', { exact: true }).fill('');
  await page.getByLabel('С', { exact: true }).fill('2026-09-25');
  await expect(page.getByText('События не найдены')).toBeVisible();
  await page.getByRole('button', { name: 'Сбросить фильтры' }).click();
  await expect(page.getByText('Всего: 7')).toBeVisible();
  expect(api.problems()).toEqual([]);
});

test('opens read-only details of an event and returns focus', async ({ page }) => {
  const api = await backend(page);
  await openJournal(page);
  await page.getByLabel('Тип события').selectOption('paid_activated');
  await expect(eventNames(page)).toHaveText(['Платная подписка активирована']);

  // A click anywhere on the row opens the event.
  await rows(page).first().locator('td').first().click();
  const dialog = page.getByRole('dialog', { name: 'Платная подписка активирована' });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('Активирована платная подписка «Премиум месяц» на 30 дн.');
  await expect(dialog).toContainText('10.09.2026, 12:00:00');
  await expect(dialog).toContainText('#900');
  await expect(dialog).toContainText(new Intl.NumberFormat('ru-RU', { style: 'currency', currency: 'RUB' }).format(299));
  await expect(dialog).toContainText('Пользователь · Platega');
  await expect(dialog.locator('.change').filter({ hasText: 'Бессрочно' })).toContainText('10.10.2026, 12:00');
  await expect(dialog.locator('.change').filter({ hasText: 'free' })).toContainText('Премиум');
  await expect(dialog.getByRole('link', { name: '@anya · 1001' })).toHaveAttribute('href', '#/users/1001');
  await expect(dialog.getByRole('textbox')).toHaveCount(0);

  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(eventNames(page).first()).toBeFocused();

  await eventNames(page).first().press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: 'Закрыть' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(api.problems()).toEqual([]);
});

test('refreshing only reads the journal', async ({ page }) => {
  const api = await backend(page);
  await openJournal(page);
  const refresh = page.getByRole('button', { name: 'Обновить' });
  for (let i = 0; i < 3; i++) {
    await refresh.click();
    await expect(refresh).toBeEnabled();
  }
  await page.reload();
  await expect(rows(page).first()).toBeVisible();
  await expect(page.getByText('Всего: 7')).toBeVisible();
  expect(api.journalCalls().every(call => call.method === 'GET')).toBe(true);
  expect(api.problems()).toEqual([]);
});

test('shows an empty journal', async ({ page }) => {
  await backend(page, []);
  await page.goto('/#/audit');
  await expect(page.getByText('Событий пока нет')).toBeVisible();
});
