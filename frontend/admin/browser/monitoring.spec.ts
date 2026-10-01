import { test, expect } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

// Network monitor page ("Мониторинг") against a mock backend that follows the
// contract of web.adminAPI.routeMonitoring / netmon views:
//   GET /admin/api/monitoring                 overview of every builder
//   GET /admin/api/monitoring/builders/{id}   countries and servers of a builder
//   GET /admin/api/monitoring/history         builder_id, country ('' = no country), target_id, period
// The page is read-only: it must never send anything but GETs, and a check is
// never triggered by reading.

const CSRF = 'c'.repeat(43);
const T0 = '2026-09-30T12:00:00Z';
const MIN_AGO = (m: number) => new Date(Date.parse(T0) - m * 60_000).toISOString();

interface WireNode {
  target_id: number; name: string; source_id: number; source_name: string; protocol: string; host: string; port: number;
  security: string; transport: string; probe: string; status: string; status_since: string | null; latency_ms: number | null;
  last_checked_at: string | null; last_up_at: string | null; last_down_at: string | null; last_error: string; shared_with: number;
  [extra: string]: unknown;
}

function node(over: Partial<WireNode> & { target_id: number; name: string }): WireNode {
  return {
    source_id: 1, source_name: 'Provider A', protocol: 'vless', host: 'de1.example', port: 443, security: 'reality',
    transport: 'tcp', probe: 'tls', status: 'up', status_since: MIN_AGO(60), latency_ms: 40, last_checked_at: T0,
    last_up_at: T0, last_down_at: null, last_error: '', shared_with: 0, ...over,
  };
}

function country(code: string, status: string, nodes: WireNode[], over: Record<string, unknown> = {}) {
  return {
    country_code: code, status, status_since: MIN_AGO(10), latency_ms: 40, nodes_total: nodes.length,
    nodes_up: nodes.filter(n => n.status === 'up').length, nodes_down: nodes.filter(n => n.status === 'down').length,
    last_checked_at: T0, last_up_at: T0, last_down_at: null, nodes, ...over,
  };
}

const COUNTRIES = [
  country('DE', 'up', [
    // An unexpected credential on the wire must never reach the page.
    node({ target_id: 11, name: 'de-vless', uuid: 'uuid-secret-123' }),
    node({ target_id: 12, name: 'de-trojan', protocol: 'trojan', host: 'de2.example', security: 'tls', shared_with: 2 }),
  ]),
  country('NL', 'down', [
    node({ target_id: 21, name: 'nl-hy2', protocol: 'hysteria2', host: '2001:db8::1', port: 8443, security: '', transport: 'quic',
      probe: 'quic', status: 'down', latency_ms: null, last_up_at: MIN_AGO(30), last_down_at: MIN_AGO(10), last_error: 'timeout' }),
  ], { latency_ms: null, last_up_at: MIN_AGO(30), last_down_at: MIN_AGO(10) }),
  country('', 'unknown', [
    node({ target_id: 31, name: 'ss-1', protocol: 'shadowsocks', host: 'ss.example', port: 8388, security: '', transport: '',
      probe: 'tcp', status: 'unknown', latency_ms: null, last_checked_at: null, last_up_at: null }),
  ], { latency_ms: null, last_checked_at: null, last_up_at: null }),
];

const BUILDERS = [
  {
    id: 3, name: 'Основной', enabled: true, status: 'degraded', countries_total: 3, countries_up: 1, countries_degraded: 0,
    countries_down: 1, nodes_total: 4, nodes_up: 2, nodes_down: 1, nodes_unknown: 1, avg_latency_ms: 42, last_checked_at: T0,
    last_transition: { country_code: 'NL', event: 'down', at: MIN_AGO(10) }, source_errors: 1, unresolved: 2,
  },
  {
    id: 4, name: 'Резерв', enabled: false, status: 'disabled', countries_total: 0, countries_up: 0, countries_degraded: 0,
    countries_down: 0, nodes_total: 0, nodes_up: 0, nodes_down: 0, nodes_unknown: 0, avg_latency_ms: null, last_checked_at: null,
    last_transition: null, source_errors: 0, unresolved: 0,
  },
];

const PERIOD_SPAN: Record<string, number> = { '24h': 86_400_000, '7d': 7 * 86_400_000, '30d': 30 * 86_400_000 };

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

interface Options { monitor?: 'ok' | 'unavailable' }

async function backend(page: Page, { monitor = 'ok' }: Options = {}) {
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
    if (monitor === 'unavailable' && url.pathname.startsWith('/admin/api/monitoring')) {
      return json(route, 503, { error: 'service_unavailable' });
    }
    if (url.pathname === '/admin/api/monitoring') {
      return json(route, 200, {
        enabled: true, generated_at: new Date().toISOString(), interval_seconds: 60, down_after: 3, last_round_at: T0,
        last_refresh_at: T0, builders: BUILDERS,
      });
    }
    const detail = /^\/admin\/api\/monitoring\/builders\/(\d+)$/.exec(url.pathname);
    if (detail) {
      if (detail[1] !== '3') return json(route, 404, { error: 'not_found' });
      return json(route, 200, {
        generated_at: new Date().toISOString(), interval_seconds: 60, last_round_at: T0, builder: BUILDERS[0], countries: COUNTRIES,
      });
    }
    if (url.pathname === '/admin/api/monitoring/history') {
      const p = url.searchParams;
      const unknown = [...p.keys()].filter(key => !['builder_id', 'country', 'target_id', 'period'].includes(key));
      if (unknown.length) problems.push(`unknown params ${unknown.join(',')}`);
      const period = p.get('period') ?? '24h';
      if (!PERIOD_SPAN[period]) return json(route, 400, { error: 'invalid_request' });
      if (p.has('country') && !p.has('builder_id')) return json(route, 400, { error: 'invalid_request' });
      const scope = p.has('target_id') ? 'target' : p.has('country') ? 'country' : 'builder';
      const to = Date.parse(T0);
      const outages = scope === 'target' ? [] : [{
        id: 7, scope: 'country', kind: 'down', country_code: 'NL', target_id: null, started_at: MIN_AGO(10), ended_at: null,
        duration_seconds: 600, ongoing: true, end_reason: '', error_code: 'timeout',
      }, {
        id: 5, scope: 'country', kind: 'degraded', country_code: 'DE', target_id: null, started_at: MIN_AGO(300), ended_at: MIN_AGO(280),
        duration_seconds: 1200, ongoing: false, end_reason: 'recovered', error_code: 'refused',
      }];
      return json(route, 200, {
        scope, period, from: new Date(to - PERIOD_SPAN[period]).toISOString(), to: T0, step_seconds: 900,
        summary: { outages: outages.length, down_seconds: 600, degraded_seconds: 1200, availability: scope === 'builder' ? null : 99.3, checks: 96, failures: 10 },
        outages,
        latency: [
          { at: MIN_AGO(60), checks: 4, failures: 0, avg_ms: 40, min_ms: 30, max_ms: 50 },
          { at: MIN_AGO(15), checks: 4, failures: 2, avg_ms: 80, min_ms: 60, max_ms: 100 },
        ],
      });
    }
    problems.push(`unexpected GET ${url.pathname}`);
    return json(route, 404, { error: 'not_found' });
  });
  const of = (path: string) => calls.filter(call => call.path === path);
  return {
    overviewCalls: () => of('/admin/api/monitoring'),
    builderCalls: () => of('/admin/api/monitoring/builders/3'),
    historyCalls: () => of('/admin/api/monitoring/history'),
    lastHistory: () => of('/admin/api/monitoring/history').at(-1)!.params,
    problems: () => problems,
  };
}

const builders = (page: Page) => page.getByRole('table', { name: 'Состояние сети по построителям' }).locator(':scope > tbody > tr');
const countries = (page: Page) => page.getByRole('table', { name: 'Состояние стран построителя' }).locator(':scope > tbody > tr');

async function openBuilder(page: Page) {
  await page.goto('/#/monitoring/3');
  await expect(page.getByRole('heading', { level: 1, name: 'Основной' })).toBeVisible();
  await expect(countries(page).first()).toBeVisible();
}

test('overview lists builders with status, countries, servers, latency and the last transition', async ({ page }) => {
  await page.clock.install({ time: new Date(T0) });
  const api = await backend(page);
  await page.goto('/#/monitoring');
  await expect(page.getByRole('heading', { level: 1, name: 'Мониторинг' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Мониторинг' })).toHaveAttribute('aria-current', 'page');
  await expect(builders(page)).toHaveCount(2);

  const main = builders(page).nth(0);
  await expect(main).toContainText('Основной');
  await expect(main).toContainText('Частично');
  await expect(main).toContainText('1 из 3');
  await expect(main).toContainText('недоступны: 1');
  await expect(main).toContainText('2 из 4');
  await expect(main).toContainText('нет данных: 1');
  await expect(main).toContainText('42 мс');
  await expect(main).toContainText('NL · Нидерланды: недоступна');
  await expect(main).toContainText('10 мин назад');
  await expect(main).toContainText('Источников с ошибкой: 1');
  await expect(builders(page).nth(1)).toContainText('Отключён');
  await expect(page.getByText('Сервер становится недоступным после 3 неудачных проверок подряд.')).toBeVisible();

  await page.getByLabel('Статус построителя').selectOption('disabled');
  await expect(builders(page)).toHaveCount(1);
  await expect(page.getByText('Найдено: 1 из 2')).toBeVisible();
  await page.getByLabel('Статус построителя').selectOption('down');
  await expect(page.getByRole('heading', { name: 'Построители не найдены' })).toBeVisible();
  await page.getByRole('button', { name: 'Показать все' }).click();
  await expect(builders(page)).toHaveCount(2);

  await builders(page).nth(0).getByRole('link', { name: 'Основной' }).click();
  await expect(page).toHaveURL(/#\/monitoring\/3$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Основной' })).toBeVisible();
  expect(api.overviewCalls().length).toBe(1);
  expect(api.problems()).toEqual([]);
});

test('builder page: countries, expandable servers, notices and filters', async ({ page }) => {
  await page.clock.install({ time: new Date(T0) });
  const api = await backend(page);
  await openBuilder(page);

  await expect(page.getByText('Источников с ошибкой загрузки при последнем разборе: 1.')).toBeVisible();
  await expect(page.getByText('Серверов без проверяемого адреса: 2')).toBeVisible();
  await expect(countries(page)).toHaveCount(3);
  await expect(countries(page).nth(0)).toContainText('DE · Германия');
  await expect(countries(page).nth(0)).toContainText('Работает');
  await expect(countries(page).nth(1)).toContainText('Недоступен');
  await expect(countries(page).nth(1)).toContainText('30 мин назад'); // last UP
  await expect(countries(page).nth(2)).toContainText('Без страны');
  await expect(countries(page).nth(2)).toContainText('Нет данных');
  await expect(countries(page).nth(2)).toContainText('Не проверялся');

  const de = page.getByRole('button', { name: 'DE · Германия', exact: true });
  await expect(de).toHaveAttribute('aria-expanded', 'false');
  await de.click();
  await expect(page.getByRole('button', { name: 'DE · Германия', exact: true })).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByRole('button', { name: 'DE · Германия', exact: true })).toBeFocused();
  const deNodes = page.getByRole('table', { name: 'Серверы: DE · Германия' });
  await expect(deNodes.locator('tbody tr')).toHaveCount(2);
  await expect(deNodes).toContainText('de-vless');
  await expect(deNodes).toContainText('de1.example:443');
  await expect(deNodes).toContainText('VLESS');
  await expect(deNodes).toContainText('tcp · reality');
  await expect(deNodes).toContainText('TLS handshake');
  await expect(deNodes).toContainText('ещё в построителях: 2');

  await page.getByRole('button', { name: 'NL · Нидерланды', exact: true }).click();
  const nlNodes = page.getByRole('table', { name: 'Серверы: NL · Нидерланды' });
  await expect(nlNodes).toContainText('[2001:db8::1]:8443');
  await expect(nlNodes).toContainText('Hysteria2');
  await expect(nlNodes).toContainText('QUIC handshake');
  await expect(nlNodes).toContainText('Нет ответа (таймаут)');
  await expect(page.locator('body')).not.toContainText('uuid-secret-123');

  await page.getByLabel('Протокол').selectOption('trojan');
  await expect(countries(page).filter({ has: page.locator('.monitor-toggle') })).toHaveCount(1);
  await expect(page.getByText('Показано стран: 1 из 3')).toBeVisible();
  await expect(page.getByRole('table', { name: 'Серверы: DE · Германия' }).locator('tbody tr')).toHaveCount(1);
  await page.getByLabel('Протокол').selectOption('');
  await page.getByLabel('Статус страны').selectOption('degraded');
  await expect(page.getByRole('heading', { name: 'Ничего не найдено' })).toBeVisible();
  await page.getByRole('button', { name: 'Сбросить фильтры' }).click();
  await page.getByLabel('Страна').selectOption('-');
  await expect(countries(page).filter({ has: page.locator('.monitor-toggle') })).toHaveCount(1);
  await expect(countries(page).first()).toContainText('Без страны');
  expect(api.builderCalls().length).toBe(1);
  expect(api.problems()).toEqual([]);
});

test('history of a builder, a country, a server and periods', async ({ page }) => {
  await page.clock.install({ time: new Date(T0) });
  const api = await backend(page);
  await openBuilder(page);

  await expect(page.getByRole('heading', { name: 'История: Весь построитель' })).toBeVisible();
  let q = api.lastHistory();
  expect(q.get('builder_id')).toBe('3');
  expect(q.has('country')).toBe(false);
  expect(q.get('period')).toBe('24h');
  const outages = page.getByRole('table', { name: 'Сбои за период, от новых к старым' }).locator('tbody tr');
  await expect(outages).toHaveCount(2);
  await expect(outages.nth(0)).toContainText('Недоступность');
  await expect(outages.nth(0)).toContainText('Нет ответа (таймаут)');
  await expect(outages.nth(0)).toContainText('NL · Нидерланды');
  await expect(outages.nth(0)).toContainText('Продолжается');
  await expect(outages.nth(1)).toContainText('Частичный сбой');
  await expect(outages.nth(1)).toContainText('20 мин');
  await expect(outages.nth(1)).toContainText('Восстановлено');
  await expect(page.getByRole('img', { name: /Средняя задержка за 24 часа, максимум 80 мс/ })).toBeVisible();
  await expect(page.getByText('Считается для страны и сервера')).toBeVisible();

  await page.getByRole('button', { name: 'История: NL · Нидерланды' }).click();
  await expect(page.getByRole('heading', { name: 'История: NL · Нидерланды' })).toBeFocused();
  q = api.lastHistory();
  expect(q.get('builder_id')).toBe('3');
  expect(q.get('country')).toBe('NL');
  await expect(page.getByText('99,30 %')).toBeVisible();

  await page.getByRole('group', { name: 'Период' }).getByRole('button', { name: '7 дней' }).click();
  await expect(page.getByRole('button', { name: '7 дней' })).toHaveAttribute('aria-pressed', 'true');
  expect(api.lastHistory().get('period')).toBe('7d');
  expect(api.lastHistory().get('country')).toBe('NL');

  await page.getByRole('button', { name: 'История: Без страны' }).click();
  await expect(page.getByRole('heading', { name: 'История: Без страны' })).toBeVisible();
  q = api.lastHistory();
  expect(q.has('country')).toBe(true);
  expect(q.get('country')).toBe('');
  expect(q.get('period')).toBe('7d');

  await page.getByRole('button', { name: 'DE · Германия', exact: true }).click();
  await page.getByRole('button', { name: 'История: de-trojan' }).click();
  await expect(page.getByRole('heading', { name: 'История: de-trojan' })).toBeVisible();
  q = api.lastHistory();
  expect(q.get('target_id')).toBe('12');
  expect(q.has('builder_id')).toBe(false);
  await expect(page.getByText('За 7 дней сбоев не было.')).toBeVisible();

  await page.getByRole('button', { name: 'Весь построитель' }).click();
  await expect(page.getByRole('heading', { name: 'История: Весь построитель' })).toBeVisible();
  expect(api.lastHistory().has('target_id')).toBe(false);
  expect(api.problems()).toEqual([]);
});

test('polls while open, keeps "ago" labels current and stops after leaving', async ({ page }) => {
  await page.clock.install({ time: new Date(T0) });
  const api = await backend(page);
  await page.goto('/#/monitoring');
  await expect(builders(page)).toHaveCount(2);
  const checked = builders(page).nth(0).locator('td[data-label="Проверено"]');
  await expect(checked).toHaveText('только что');
  expect(api.overviewCalls().length).toBe(1);

  await page.clock.fastForward(15_000);
  await expect.poll(() => api.overviewCalls().length).toBe(2);
  await expect(checked).toHaveText('15 с назад');
  await page.clock.fastForward(15_000);
  await expect.poll(() => api.overviewCalls().length).toBe(3);

  await page.goto('/#/monitoring/3');
  await expect(countries(page).first()).toBeVisible();
  const before = api.builderCalls().length;
  await page.clock.fastForward(15_000);
  await expect.poll(() => api.builderCalls().length).toBe(before + 1);

  await page.getByRole('link', { name: 'Журнал' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Журнал' })).toBeVisible();
  const overview = api.overviewCalls().length;
  const detail = api.builderCalls().length;
  await page.clock.fastForward(45_000);
  expect(api.overviewCalls().length).toBe(overview);
  expect(api.builderCalls().length).toBe(detail);
});

test('explains a backend running without the monitor', async ({ page }) => {
  await backend(page, { monitor: 'unavailable' });
  await page.goto('/#/monitoring');
  await expect(page.getByRole('heading', { name: 'Не удалось загрузить мониторинг' })).toBeVisible();
  await expect(page.getByText('Мониторинг сети не запущен на сервере.')).toBeVisible();
});

test('shows a missing builder', async ({ page }) => {
  const api = await backend(page);
  await page.goto('/#/monitoring/99');
  await expect(page.getByRole('heading', { name: 'Построитель не найден' })).toBeVisible();
  await page.getByRole('link', { name: 'К мониторингу', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Мониторинг' })).toBeVisible();
  expect(api.problems()).toEqual([]);
});
