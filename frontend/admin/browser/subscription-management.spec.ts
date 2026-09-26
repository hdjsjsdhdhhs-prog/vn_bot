import { test, expect } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

// Subscription Management of the browser admin panel against a mock backend
// that follows the real contract:
//   session   internal/adminauth (bootstrap/session payload, CSRF header)
//   mutate    internal/web/admin_api.go mutate (content type, unknown fields,
//             409 recorded rejection with outcome, 500 sync_failed with outcome)
//   policy    database.applyAdminAction (status and expiry rules)
//   replay    database.AdminMutateSubscription (same key + same payload replays
//             the recorded outcome, same key + other payload is a conflict)
// The browser clock is pinned to NOW so the UI and the mock agree on "now".

const NOW = Date.parse('2026-09-25T12:00:00Z');
const DAY_MS = 86_400_000;
const CSRF = 'c'.repeat(43);
const TELEGRAM_ID = 1001;
const SUB_ID = 42;
const KEY_PATTERN = /^ui-[0-9a-f]{32}$/;
const ACTIONS = { renew: 'renew', expiry: 'change_expiry', disable: 'disable', enable: 'enable' } as const;
type RouteAction = keyof typeof ACTIONS;

interface SubState {
  status: string;
  expires_at: string | null;
  reminders_sent: number;
}

interface MutationCall {
  action: RouteAction;
  body: Record<string, unknown>;
  csrf: string | undefined;
  contentType: string | undefined;
}

interface Recorded {
  hash: string;
  errorCode: string;
  audit: Record<string, unknown>;
}

interface Options {
  status?: string;
  expiresAt?: string | null;
  /** Mutations answered with 500 sync_failed after the commit (applies to replays too). */
  syncFailures?: number;
  /** Mutations whose response is lost after the commit (network failure). */
  lostResponses?: number;
}

const iso = (ms: number) => new Date(ms).toISOString();

/** database.applyAdminAction, ported one to one. Returns the new state or an error code. */
function apply(current: SubState, action: RouteAction, body: Record<string, unknown>, now: number): SubState | string {
  const terminal = current.status === 'revoked' || current.status === 'canceled';
  switch (action) {
    case 'renew': {
      if (terminal) return 'invalid_state';
      if (current.expires_at === null) return 'perpetual_subscription';
      const expiry = Date.parse(current.expires_at);
      const base = new Date(expiry > now ? expiry : now);
      base.setUTCDate(base.getUTCDate() + Number(body.days));
      return { status: current.status === 'expired' ? 'active' : current.status, expires_at: iso(base.getTime()), reminders_sent: 0 };
    }
    case 'expiry': {
      if (terminal) return 'invalid_state';
      const expiry = Date.parse(String(body.expires_at));
      return { status: current.status === 'expired' && expiry > now ? 'active' : current.status, expires_at: iso(expiry), reminders_sent: 0 };
    }
    case 'disable':
      if (current.status !== 'active' && current.status !== 'expired') return 'invalid_state';
      return { ...current, status: 'paused' };
    case 'enable':
      if (current.status !== 'paused') return 'invalid_state';
      if (current.expires_at !== null && Date.parse(current.expires_at) <= now) return 'subscription_expired';
      return { ...current, status: 'active' };
  }
}

/** web.adminAPI.mutate request validation; null when the body is acceptable. */
function invalidBody(action: RouteAction, body: unknown): string | null {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return 'body is not an object';
  const record = body as Record<string, unknown>;
  const unknown = Object.keys(record).filter(key => !['request_key', 'days', 'expires_at'].includes(key));
  if (unknown.length) return `unknown fields ${unknown.join(',')}`;
  if (typeof record.request_key !== 'string' || !KEY_PATTERN.test(record.request_key)) return 'bad request_key';
  const allowed = action === 'renew' ? ['request_key', 'days'] : action === 'expiry' ? ['request_key', 'expires_at'] : ['request_key'];
  const extra = Object.keys(record).filter(key => !allowed.includes(key));
  if (extra.length) return `fields not used by ${action}: ${extra.join(',')}`;
  if (action === 'renew' && (!Number.isInteger(record.days) || (record.days as number) < 1 || (record.days as number) > 3650)) return 'bad days';
  if (action === 'expiry' && (typeof record.expires_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(record.expires_at))) return 'bad expires_at';
  return null;
}

async function backend(page: Page, options: Options = {}) {
  const sub: SubState = {
    status: options.status ?? 'active',
    expires_at: options.expiresAt === undefined ? '2026-10-10T12:00:00Z' : options.expiresAt,
    reminders_sent: 1,
  };
  let updatedAt = '2026-09-20T08:00:00Z';
  let syncFailures = options.syncFailures ?? 0;
  let lostResponses = options.lostResponses ?? 0;
  let auditId = 0;
  const calls: MutationCall[] = [];
  const problems: string[] = [];
  const recorded = new Map<string, Recorded>();
  const reads: string[] = [];

  const view = (withPlanName: boolean) => ({
    id: SUB_ID, telegram_id: TELEGRAM_ID, username: 'anya', status: sub.status, expires_at: sub.expires_at,
    plan_id: 3, ...(withPlanName ? { plan_name: 'Стандарт' } : {}), provider_source_id: null, product_id: 7,
    is_paid: true, price_paid_cents: 150, currency: 'XTR', referred_by: null,
    started_at: '2026-08-01T10:00:00Z', last_request: '2026-09-25T11:30:00Z',
    created_at: '2026-08-01T10:00:00Z', updated_at: updatedAt, reminders_sent: sub.reminders_sent, devices: 2, ips: 3,
  });
  const detail = () => ({
    subscription: view(true),
    plan: { id: 3, name: 'Стандарт', is_active: true, devices_limit: 3, traffic_limit: 0 },
    nodes: [{
      node_id: 1, node_name: 'nl-ams-1', status: sub.status === 'paused' ? 'pending_remove' : 'active',
      retry_count: 0, retry_at: null, last_error: '', updated_at: updatedAt,
    }],
    audit: [],
  });
  const json = (route: Route, status: number, payload: unknown) =>
    route.fulfill({ status, contentType: 'application/json', headers: { 'Cache-Control': 'no-store' }, body: JSON.stringify(payload) });

  await page.clock.setFixedTime(NOW);
  await page.route('**/admin/**', async route => {
    const request = route.request();
    const { pathname } = new URL(request.url());
    const method = request.method();

    if (pathname === '/admin/login' && method === 'GET') return json(route, 200, { authenticated: true, csrf_token: CSRF });
    if (pathname === '/admin/session' && method === 'GET') return json(route, 200, { authenticated: true, csrf_token: CSRF });
    if (method === 'GET' && (pathname === `/admin/api/users/${TELEGRAM_ID}` || pathname === `/admin/api/subscriptions/${SUB_ID}`)) {
      reads.push(pathname);
      return json(route, 200, detail());
    }

    const match = /^\/admin\/api\/subscriptions\/(\d+)\/(renew|disable|enable|expiry)$/.exec(pathname);
    if (!match || method !== 'POST') {
      problems.push(`unexpected ${method} ${pathname}`);
      return json(route, 404, { error: 'not_found' });
    }
    const action = match[2] as RouteAction;
    const headers = request.headers();
    let body: unknown;
    try { body = JSON.parse(request.postData() ?? ''); } catch { body = undefined; }
    calls.push({ action, body: body as Record<string, unknown>, csrf: headers['x-csrf-token'], contentType: headers['content-type'] });

    // adminauth.Middleware: CSRF on unsafe methods.
    if (headers['x-csrf-token'] !== CSRF) {
      problems.push(`csrf header ${headers['x-csrf-token']}`);
      return json(route, 403, { error: 'forbidden' });
    }
    if (headers['content-type']?.split(';')[0].trim() !== 'application/json') {
      problems.push(`content type ${headers['content-type']}`);
      return json(route, 415, { error: 'unsupported_media_type' });
    }
    const invalid = invalidBody(action, body);
    if (invalid) {
      problems.push(invalid);
      return json(route, 400, { error: 'invalid_request' });
    }
    if (Number(match[1]) !== SUB_ID) return json(route, 404, { error: 'not_found' });

    const record = body as Record<string, unknown>;
    const key = record.request_key as string;
    const hash = [ACTIONS[action], SUB_ID, action === 'renew' ? record.days : '', action === 'expiry' ? record.expires_at : ''].join('|');
    // A short delay keeps the request in flight long enough for double clicks to matter.
    await new Promise(resolve => setTimeout(resolve, 150));

    let entry = recorded.get(key);
    let replayed = false;
    if (entry) {
      if (entry.hash !== hash) return json(route, 409, { error: 'request_key_conflict' });
      replayed = true;
    } else {
      const before = { ...sub };
      const next = apply(sub, action, record, NOW);
      const errorCode = typeof next === 'string' ? next : '';
      if (typeof next !== 'string') {
        Object.assign(sub, next);
        updatedAt = iso(NOW);
      }
      entry = {
        hash, errorCode,
        audit: {
          id: ++auditId, actor: 'admin', action: ACTIONS[action], subscription_id: SUB_ID, created_at: iso(NOW),
          old_value: JSON.stringify(before), new_value: JSON.stringify(errorCode ? before : sub),
          success: errorCode === '', error_code: errorCode, request_key: key,
        },
      };
      recorded.set(key, entry);
    }
    const outcome = { subscription: view(false), audit: entry.audit, replayed };
    if (entry.errorCode) return json(route, 409, { error: entry.errorCode, ...outcome });
    // Post-commit side effects run on every successful attempt, replays included.
    if (syncFailures > 0) {
      syncFailures--;
      return json(route, 500, { error: 'sync_failed', ...outcome });
    }
    if (lostResponses > 0) {
      lostResponses--;
      return route.abort('failed');
    }
    return json(route, 200, outcome);
  });

  return {
    calls: () => calls,
    problems: () => problems,
    reads: () => reads,
    state: () => ({ ...sub }),
    /** A change made elsewhere (another admin, a worker) after the page loaded. */
    setStatus: (status: string) => { sub.status = status; },
  };
}

async function openUser(page: Page) {
  await page.goto(`/#/users/${TELEGRAM_ID}`);
  await expect(page.getByRole('heading', { level: 1, name: '@anya' })).toBeVisible();
  return page.getByRole('region', { name: 'Управление подпиской' });
}

const subscriptionPanel = (page: Page) => page.getByRole('region', { name: 'Подписка', exact: true });

test('renew: preview, one request per confirm, committed result on the page', async ({ page }) => {
  const api = await backend(page);
  const manage = await openUser(page);
  await expect(manage.getByRole('button', { name: 'Продлить' })).toBeEnabled();
  await expect(manage.getByRole('button', { name: 'Изменить срок' })).toBeEnabled();
  await expect(manage.getByRole('button', { name: 'Приостановить' })).toBeEnabled();
  await expect(manage.getByRole('button', { name: 'Возобновить' })).toHaveCount(0);

  await manage.getByRole('button', { name: 'Продлить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Продлить подписку?' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel('Количество дней')).toBeFocused();
  await dialog.getByRole('button', { name: '90 дн.' }).click();
  await expect(dialog.getByRole('button', { name: '90 дн.' })).toHaveAttribute('aria-pressed', 'true');
  // 2026-10-10 12:00 UTC + 90 days, computed from the current expiry.
  await expect(dialog).toContainText('08.01.2027, 12:00');

  await dialog.getByRole('button', { name: 'Продлить', exact: true }).dblclick();
  await expect(dialog).toBeHidden();
  await expect(manage.getByRole('status')).toContainText('Подписка продлена до 08.01.2027, 12:00 (было: 10.10.2026, 12:00).');

  expect(api.calls()).toHaveLength(1);
  const [call] = api.calls();
  expect(call.action).toBe('renew');
  expect(call.body).toEqual({ request_key: expect.stringMatching(KEY_PATTERN), days: 90 });
  expect(call.csrf).toBe(CSRF);
  expect(api.state()).toEqual({ status: 'active', expires_at: '2027-01-08T12:00:00.000Z', reminders_sent: 0 });
  // The page is re-read by subscription ID after the dialog closes.
  await expect.poll(() => api.reads().at(-1)).toBe(`/admin/api/subscriptions/${SUB_ID}`);
  await expect(subscriptionPanel(page)).toContainText('08.01.2027, 12:00');
  expect(api.problems()).toEqual([]);
});

test('renew rejects out-of-range days locally without a request', async ({ page }) => {
  const api = await backend(page);
  const manage = await openUser(page);
  await manage.getByRole('button', { name: 'Продлить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Продлить подписку?' });
  await dialog.getByLabel('Количество дней').fill('3651');
  await dialog.getByRole('button', { name: 'Продлить', exact: true }).click();
  await expect(dialog.getByText('Укажите целое число дней от 1 до 3 650.')).toBeVisible();
  await dialog.getByRole('button', { name: 'Отмена' }).click();
  await expect(dialog).toBeHidden();
  await expect(manage.getByRole('button', { name: 'Продлить' })).toBeFocused();
  expect(api.calls()).toHaveLength(0);
});

test('pause and resume follow the lifecycle policy', async ({ page }) => {
  const api = await backend(page);
  const manage = await openUser(page);

  await manage.getByRole('button', { name: 'Приостановить' }).click();
  const pause = page.getByRole('dialog', { name: 'Приостановить подписку?' });
  await expect(pause).toContainText('Приостановлена');
  await pause.getByRole('button', { name: 'Приостановить' }).click();
  await expect(pause).toBeHidden();
  await expect(manage.getByRole('status')).toContainText('Подписка приостановлена, доступ к VPN отключается.');
  await expect(subscriptionPanel(page)).toContainText('Приостановлена');
  await expect(manage.getByRole('button', { name: 'Приостановить' })).toHaveCount(0);
  await expect(manage.getByRole('button', { name: 'Возобновить' })).toBeEnabled();
  await expect(page.getByRole('region', { name: 'Узлы' })).toContainText('Ожидает удаления');

  await manage.getByRole('button', { name: 'Возобновить' }).click();
  const resume = page.getByRole('dialog', { name: 'Возобновить подписку?' });
  await resume.getByRole('button', { name: 'Возобновить' }).click();
  await expect(resume).toBeHidden();
  await expect(manage.getByRole('status')).toContainText('Подписка возобновлена, доступ к VPN восстанавливается.');
  await expect(manage.getByRole('button', { name: 'Приостановить' })).toBeEnabled();

  expect(api.calls().map(call => [call.action, Object.keys(call.body)])).toEqual([
    ['disable', ['request_key']], ['enable', ['request_key']],
  ]);
  expect(api.calls()[0].body.request_key).not.toBe(api.calls()[1].body.request_key);
  expect(api.state().status).toBe('active');
  expect(api.problems()).toEqual([]);
});

test('paused with a lapsed expiry cannot be resumed until renewed', async ({ page }) => {
  const api = await backend(page, { status: 'paused', expiresAt: iso(NOW - DAY_MS) });
  const manage = await openUser(page);
  await expect(manage.getByRole('button', { name: 'Возобновить' })).toBeDisabled();
  await expect(manage).toContainText('Срок уже истёк: сначала продлите подписку или измените срок.');

  await manage.getByRole('button', { name: 'Продлить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Продлить подписку?' });
  await dialog.getByRole('button', { name: '7 дн.' }).click();
  await dialog.getByRole('button', { name: 'Продлить', exact: true }).click();
  await expect(dialog).toBeHidden();
  // Renewing counts from now and keeps the pause.
  await expect(manage.getByRole('status')).toContainText('до 02.10.2026, 12:00');
  await expect(manage.getByRole('status')).toContainText('Подписка остаётся приостановленной.');
  await expect(manage.getByRole('button', { name: 'Возобновить' })).toBeEnabled();
  expect(api.state()).toMatchObject({ status: 'paused', expires_at: '2026-10-02T12:00:00.000Z' });
  expect(api.problems()).toEqual([]);
});

test('change expiry of an expired subscription reactivates it', async ({ page }) => {
  const api = await backend(page, { status: 'expired', expiresAt: iso(NOW - 3 * DAY_MS) });
  const manage = await openUser(page);
  await manage.getByRole('button', { name: 'Изменить срок' }).click();
  const dialog = page.getByRole('dialog', { name: 'Изменить срок подписки?' });
  await expect(dialog).toContainText('UTC+00:00');
  await dialog.getByLabel('Новый срок').fill('2027-03-01T09:30');
  await expect(dialog).toContainText('Активна');
  await dialog.getByRole('button', { name: 'Изменить срок' }).click();
  await expect(dialog).toBeHidden();
  await expect(manage.getByRole('status')).toContainText('Срок изменён: 22.09.2026, 12:00 → 01.03.2027, 09:30.');
  await expect(manage.getByRole('status')).toContainText('Статус: Истекла → Активна.');

  expect(api.calls()).toHaveLength(1);
  expect(api.calls()[0].body).toEqual({ request_key: expect.stringMatching(KEY_PATTERN), expires_at: '2027-03-01T09:30:00Z' });
  expect(api.state()).toMatchObject({ status: 'active', expires_at: '2027-03-01T09:30:00.000Z' });
  expect(api.problems()).toEqual([]);
});

test('perpetual subscription: renew is blocked, expiry can be set', async ({ page }) => {
  await backend(page, { expiresAt: null });
  const manage = await openUser(page);
  await expect(subscriptionPanel(page)).toContainText('Бессрочно');
  await expect(manage.getByRole('button', { name: 'Продлить' })).toBeDisabled();
  await expect(manage).toContainText('Подписка бессрочная, продлевать нечего.');
  await expect(manage.getByRole('button', { name: 'Изменить срок' })).toBeEnabled();
});

test('recorded 409 rejection is reported and the page is refreshed', async ({ page }) => {
  const api = await backend(page);
  const manage = await openUser(page);
  // Revoked by someone else after the page was loaded.
  api.setStatus('revoked');
  await manage.getByRole('button', { name: 'Приостановить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Приостановить подписку?' });
  await dialog.getByRole('button', { name: 'Приостановить' }).click();
  await expect(dialog).toBeHidden();
  await expect(manage.getByRole('alert')).toContainText('Подписка не приостановлена. Текущий статус подписки не позволяет это действие.');
  await expect(manage).toContainText('Подписка со статусом «Отозвана» не продлевается');
  await expect(manage.getByRole('button')).toHaveCount(0);
  expect(api.calls()).toHaveLength(1);
  expect(api.problems()).toEqual([]);
});

test('sync_failed: committed once, retried with the same key as a replay', async ({ page }) => {
  const api = await backend(page, { syncFailures: 1 });
  const manage = await openUser(page);
  await manage.getByRole('button', { name: 'Продлить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Продлить подписку?' });
  await dialog.getByRole('button', { name: 'Продлить', exact: true }).click();

  await expect(dialog.getByRole('alert')).toContainText('Изменение сохранено, но синхронизация с узлами не запущена.');
  const retry = dialog.getByRole('button', { name: 'Повторить синхронизацию' });
  await expect(retry).toBeFocused();
  // The payload is frozen under its key.
  await expect(dialog.getByLabel('Количество дней')).toHaveAttribute('readonly', '');
  await expect(dialog.getByRole('button', { name: '90 дн.' })).toBeDisabled();

  await retry.click();
  await expect(dialog).toBeHidden();
  await expect(manage.getByRole('status')).toContainText('Этот запрос уже был выполнен ранее, повторно изменение не применялось.');

  const [first, second] = api.calls();
  expect(api.calls()).toHaveLength(2);
  expect(second.body).toEqual(first.body);
  // 30 days applied exactly once.
  expect(api.state().expires_at).toBe('2026-11-09T12:00:00.000Z');
  expect(api.problems()).toEqual([]);
});

test('lost response: retry replays instead of applying twice', async ({ page }) => {
  const api = await backend(page, { lostResponses: 1 });
  const manage = await openUser(page);
  await manage.getByRole('button', { name: 'Продлить' }).click();
  const dialog = page.getByRole('dialog', { name: 'Продлить подписку?' });
  await dialog.getByRole('button', { name: '7 дн.' }).click();
  await dialog.getByRole('button', { name: 'Продлить', exact: true }).click();

  await expect(dialog.getByRole('alert')).toContainText('Не удалось подтвердить результат. Нет связи с сервером.');
  await dialog.getByRole('button', { name: 'Повторить', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(manage.getByRole('status')).toContainText('до 17.10.2026, 12:00');
  await expect(manage.getByRole('status')).toContainText('Этот запрос уже был выполнен ранее');

  expect(api.calls()).toHaveLength(2);
  expect(api.calls()[1].body).toEqual(api.calls()[0].body);
  expect(api.state().expires_at).toBe('2026-10-17T12:00:00.000Z');
  expect(api.problems()).toEqual([]);
});
