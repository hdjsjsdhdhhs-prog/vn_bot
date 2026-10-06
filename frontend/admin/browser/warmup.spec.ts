import { test, expect } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

// Customer warm-up section (#/warmup…) of the browser admin panel against a
// mock backend that follows internal/web/admin_warmup_api.go:
//   GET  /admin/api/warmup                    overview (settings, scenarios, summary, registry)
//   GET/PUT /settings                         optimistic lock by version (409 version_conflict)
//   GET/POST /scenarios, GET/PATCH /scenarios/{id}, POST /scenarios/{id}/preview, POST /preview
//   POST /images (base64), GET/DELETE /images/{id}, GET /deliveries, GET /coupons
// Unknown body fields are rejected like decodeAdminJSON (DisallowUnknownFields).
// Nothing is ever sent to Telegram: the mock has no delivery side effect and
// every unexpected request is recorded as a problem.

const CSRF = 'c'.repeat(43);
const KEY_PATTERN = /^ui-[0-9a-f]{32}$/;
const T0 = '2026-10-01T10:00:00Z';
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');

interface Button { text: string; kind: string; value: string; same_row?: boolean }
interface Scenario {
  id: number; key: string; category: string; name: string; description: string; trigger_type: string; timing: string;
  delay_value: number; delay_unit: string; enabled: boolean; enabled_at: string | null; message_html: string; image_id: number | null;
  cooldown_hours: number | null; daily_cap: number; discount_percent: number; coupon_template: string; coupon_ttl_hours: number;
  position: number; version: number; created_at: string; updated_at: string; buttons: Button[]; conditions: Record<string, unknown>;
}
type Body = Record<string, unknown>;

const cb = (text: string, value: string): Button => ({ text, kind: 'callback', value });
const SEED: [string, string, string, string, string, number, string, boolean, string, Button[], Body, number][] = [
  ['trial_not_activated', 'trial', 'Пробник не активирован', 'bot_started', 'after', 12, 'hours', true, '<b>Пробный период ждёт вас</b>\n\nЭто {{trial_duration}} без оплаты.', [{ text: 'Активировать пробник', kind: 'url', value: '' }], { had_trial: false, has_subscription: false, has_active_paid: false }, 10],
  ['trial_not_connected_3h', 'trial', 'Пробник не подключён, 3 часа', 'trial_activated', 'after', 3, 'hours', true, '<b>Осталось подключиться</b>', [cb('Открыть подписку', 'menu_subscription')], { connected: false }, 20],
  ['trial_not_connected_24h', 'trial', 'Пробник не подключён, сутки', 'trial_activated', 'after', 24, 'hours', true, '<b>VPN всё ещё не подключён</b>', [cb('Открыть подписку', 'menu_subscription'), cb('Поддержка', 'menu_support')], { connected: false }, 30],
  ['trial_ending', 'trial', 'Пробник заканчивается', 'trial_period_end', 'before', 24, 'hours', true, 'Доступ до {{expire_date}}.', [cb('Выбрать тариф', 'buy_premium_list')], {}, 40],
  ['trial_no_purchase', 'trial', 'Пробник взяли, но не купили', 'trial_activated', 'after', 2, 'days', true, '<b>Как вам VPN?</b>', [cb('Выбрать тариф', 'buy_premium_list')], {}, 50],
  ['trial_ended', 'trial', 'Пробник закончился', 'trial_period_end', 'after', 0, 'hours', true, '<b>Пробный период закончился</b>', [cb('Выбрать тариф', 'buy_premium_list')], {}, 60],
  ['sub_expiring_48h', 'subscription', 'Подписка: осталось 48 часов', 'paid_expiry', 'before', 48, 'hours', false, '«{{plan_name}}» до {{expire_date}}.', [cb('Продлить подписку', 'buy_premium_list')], {}, 10],
  ['sub_expiring_24h', 'subscription', 'Подписка: остались сутки', 'paid_expiry', 'before', 24, 'hours', false, '«{{plan_name}}» до {{expire_date}}.', [cb('Продлить подписку', 'buy_premium_list')], {}, 20],
  ['sub_expired', 'subscription', 'Подписка закончилась', 'paid_expired', 'after', 0, 'hours', false, 'Срок «{{plan_name}}» истёк.', [cb('Продлить подписку', 'buy_premium_list')], {}, 30],
  ['winback_3d', 'winback', 'Возврат клиента: 3 дня', 'paid_expired', 'after', 3, 'days', true, '<b>Мы скучаем</b>', [cb('Продлить подписку', 'buy_premium_list')], {}, 10],
  ['trial_winback_3d', 'winback', 'После пробника: 3 дня', 'trial_period_end', 'after', 3, 'days', true, '<b>Понравилось?</b>', [cb('Выбрать тариф', 'buy_premium_list')], {}, 20],
  ['winback_7d', 'winback', 'Возврат клиента: 7 дней', 'paid_expired', 'after', 7, 'days', true, '<b>Возвращайтесь</b>', [cb('Продлить подписку', 'buy_premium_list')], {}, 30],
  ['winback_14d', 'winback', 'Возврат клиента: 14 дней', 'paid_expired', 'after', 14, 'days', true, '<b>Две недели без VPN</b>', [cb('Посмотреть тарифы', 'buy_premium_list')], {}, 40],
  ['winback_30d', 'winback', 'Возврат клиента: 30 дней', 'paid_expired', 'after', 30, 'days', true, '<b>Месяц без VPN</b>', [cb('Продлить подписку', 'buy_premium_list')], {}, 50],
];

const REGISTRY = {
  triggers: [
    { type: 'bot_started', label: 'Первый запуск бота', description: 'Первый /start пользователя', timings: ['after'] },
    { type: 'trial_activated', label: 'Пробник активирован', description: 'Пробная подписка привязана к Telegram', timings: ['after'] },
    { type: 'trial_period_end', label: 'Окончание пробного периода', description: 'Срок пробника', timings: ['before', 'after'] },
    { type: 'paid_expiry', label: 'Окончание платной подписки', description: 'Срок действия платной подписки', timings: ['before'] },
    { type: 'paid_expired', label: 'Платная подписка закончилась', description: 'Платный срок истёк', timings: ['after'] },
  ],
  variables: [
    { name: 'username', label: 'Имя пользователя', description: '@username', sample: '@x', triggers: null, discount: false },
    { name: 'telegram_id', label: 'Telegram ID', description: 'ID', sample: '1', triggers: null, discount: false },
    { name: 'plan_name', label: 'Тариф', description: 'Тариф', sample: 'P', triggers: ['paid_expiry', 'paid_expired'], discount: false },
    { name: 'trial_days', label: 'Дней пробника', description: 'Дни', sample: '3', triggers: null, discount: false },
    { name: 'trial_duration', label: 'Длительность пробника', description: 'Словами', sample: '3 дня', triggers: null, discount: false },
    { name: 'expire_date', label: 'Дата окончания', description: 'МСК', sample: 'd', triggers: ['trial_period_end', 'paid_expiry', 'paid_expired'], discount: false },
    { name: 'discount_code', label: 'Код скидки', description: 'Купон', sample: 'TEST', triggers: null, discount: true },
    { name: 'discount_percent', label: 'Размер скидки', description: 'Процент', sample: '15', triggers: null, discount: true },
  ],
  callbacks: [
    { data: 'menu_subscription', label: 'Моя подписка' }, { data: 'buy_premium_list', label: 'Тарифы (купить Premium)' },
    { data: 'menu_support', label: 'Поддержка' },
  ],
  conditions: [
    { key: 'had_trial', label: 'Был пробник', description: 'Активировал пробник', kind: 'bool' },
    { key: 'has_active_paid', label: 'Активная платная', description: 'Действующая платная', kind: 'bool' },
    { key: 'ever_paid', label: 'Платил', description: 'Была оплата', kind: 'bool' },
    { key: 'languages', label: 'Язык Telegram', description: 'Коды языка', kind: 'languages' },
  ],
  limits: {
    image_max_bytes: 2 * 1024 * 1024, max_buttons: 8, max_text_length: 4096, max_caption_length: 1024, max_cooldown_hours: 720,
    max_daily_cap: 100000, max_coupon_ttl_hours: 8760, max_per_day_limit: 20, max_catchup_hours: 72,
  },
};

const INPUT_FIELDS = ['category', 'name', 'description', 'trigger_type', 'timing', 'delay_value', 'delay_unit', 'enabled', 'message_html',
  'image_id', 'buttons', 'conditions', 'cooldown_hours', 'daily_cap', 'discount_percent', 'coupon_template', 'coupon_ttl_hours', 'position'];

async function json(route: Route, status: number, body: unknown) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

async function backend(page: Page) {
  let clock = Date.parse(T0);
  const now = () => new Date(clock += 60_000).toISOString();
  const scenarios: Scenario[] = SEED.map(([key, category, name, trigger, timing, value, unit, enabled, html, buttons, conditions, position], i) => ({
    id: i + 1, key, category, name, description: '', trigger_type: trigger, timing, delay_value: value, delay_unit: unit, enabled,
    enabled_at: enabled ? T0 : null, message_html: html, image_id: null, cooldown_hours: null, daily_cap: 0, discount_percent: 0,
    coupon_template: '', coupon_ttl_hours: 48, position, version: 1, created_at: T0, updated_at: T0, buttons, conditions,
  }));
  let settings = { enabled: false, max_per_day: 2, cooldown_hours: 12, catchup_hours: 6, version: 1, updated_at: T0 };
  const images = new Map<number, { id: number; mime: string; size_bytes: number; sha256: string; created_at: string }>();
  let nextImage = 70;
  const deliveries = Array.from({ length: 7 }, (_, i) => ({
    id: i + 1, scenario_id: 1, scenario_name: 'Пробник не активирован', telegram_id: 5000 + i, username: `u${i}`,
    status: ['sent', 'retry', 'failed', 'blocked'][i % 4], attempts: 1, error: i % 4 === 2 ? 'Forbidden: bot was blocked' : '',
    coupon_code: null, created_at: T0, due_at: T0, sent_at: i % 4 === 0 ? T0 : null,
  }));
  const coupons = [
    { id: 1, code: 'VPN-AB12CDEF', telegram_id: 6001, scenario_id: 10, percent: 15, status: 'issued', effective_status: 'issued', created_at: T0, expires_at: '2026-10-03T10:00:00Z', redeemed_at: null, order_id: null },
  ];
  const calls: { method: string; path: string; search: string; body: Body | null }[] = [];
  const problems: string[] = [];

  const strict = (body: Body, allowed: string[]) => Object.keys(body).every(k => allowed.includes(k));
  const preview = (input: Body) => {
    const discount = Number(input.discount_percent) > 0;
    const html = String(input.message_html ?? '');
    const buttons = (input.buttons as Button[] | undefined) ?? [];
    const hidden = buttons.filter(b => (b.kind === 'url' || b.kind === 'web_app') && !b.value).length;
    const problemsList = hidden ? [{ field: 'buttons', code: 'button_url_missing', detail: String(hidden), severity: 'warning' }] : [];
    if (discount && !html.includes('{{discount_code}}')) {
      return { valid: false, problems: [{ field: 'message_html', code: 'discount_code_required', detail: '', severity: 'invalid' }], message: null, image: null, variables: {}, discount_code: '', discount_code_test: false };
    }
    const ctx = (input.context ?? {}) as Body;
    const values: Record<string, string> = { username: `@${String(ctx.username)}`, plan_name: String(ctx.plan_name), trial_duration: '3 дня', expire_date: '31.12.2026 23:59 МСК', discount_code: 'TEST-7K3P9QXD', discount_percent: String(input.discount_percent) };
    const out = html.replace(/\{\{([a-z_]+)\}\}/g, (_, n: string) => values[n] ?? '');
    const imageId = (input.image_id as number | null | undefined) ?? null;
    return {
      valid: true, problems: problemsList, image: imageId ? images.get(imageId) ?? null : null, variables: values,
      message: { html: out, image_id: imageId, buttons: buttons.filter(b => !((b.kind === 'url' || b.kind === 'web_app') && !b.value)), hidden_buttons: hidden, visible_length: out.length, max_length: imageId ? 1024 : 4096 },
      discount_code: discount ? 'TEST-7K3P9QXD' : '', discount_code_test: discount,
    };
  };

  await page.route('**/admin/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const method = request.method();
    const body = request.postData() ? JSON.parse(request.postData() ?? '{}') as Body : null;
    calls.push({ method, path: url.pathname, search: url.search, body });
    if ((url.pathname === '/admin/login' || url.pathname === '/admin/session') && method === 'GET') return json(route, 200, { authenticated: true, csrf_token: CSRF });
    if (!url.pathname.startsWith('/admin/api/warmup')) { problems.push(`unexpected ${method} ${url.pathname}`); return json(route, 404, { error: 'not_found' }); }
    if (method !== 'GET' && request.headers()['x-csrf-token'] !== CSRF) { problems.push(`no csrf ${method} ${url.pathname}`); return json(route, 403, { error: 'forbidden' }); }
    if (body && method !== 'GET' && !url.pathname.includes('preview') && !KEY_PATTERN.test(String(body.request_key))) problems.push(`bad request_key ${url.pathname}`);
    const parts = url.pathname.replace(/^\/admin\/api\/warmup\/?/, '').split('/').filter(Boolean);
    const overview = () => ({
      settings, scenarios, registry: REGISTRY,
      categories: ['trial', 'subscription', 'winback'].map(key => ({ key, label: key, scenarios: scenarios.filter(s => s.category === key).length, enabled: scenarios.filter(s => s.category === key && s.enabled).length })),
      summary: { scenarios: scenarios.length, enabled_scenarios: scenarios.filter(s => s.enabled).length, last_24h: { sent: 3, retry: 1, failed: 1, blocked: 1 }, sent_today: 3, sent_7d: 12, active_coupons: 1, coupons_created: 1, contacts: 40, blocked_contacts: 2, scenario_stats: [{ scenario_id: 1, sent: 7, last_sent_at: T0 }] },
    });
    if (parts.length === 0 && method === 'GET') return json(route, 200, overview());
    if (parts[0] === 'settings') {
      if (method === 'GET') return json(route, 200, settings);
      if (!body || !strict(body, ['request_key', 'version', 'enabled', 'max_per_day', 'cooldown_hours', 'catchup_hours'])) return json(route, 400, { error: 'invalid_request' });
      if (body.version !== settings.version) return json(route, 409, { error: 'version_conflict' });
      settings = { enabled: Boolean(body.enabled), max_per_day: Number(body.max_per_day), cooldown_hours: Number(body.cooldown_hours), catchup_hours: Number(body.catchup_hours), version: settings.version + 1, updated_at: now() };
      return json(route, 200, { settings, replayed: false, audit: {} });
    }
    if (parts[0] === 'scenarios' && parts.length === 1) {
      if (method === 'GET') return json(route, 200, { scenarios });
      if (!body || !strict(body, ['request_key', 'key', ...INPUT_FIELDS])) return json(route, 400, { error: 'invalid_request' });
      const at = now();
      const input = Object.fromEntries(INPUT_FIELDS.map(k => [k, body[k]])) as unknown as Scenario;
      const created: Scenario = { ...input, id: scenarios.length + 1, key: String(body.key), version: 1, created_at: at, updated_at: at, enabled_at: body.enabled ? at : null };
      scenarios.push(created);
      return json(route, 200, { scenario: created, target_id: created.id, replayed: false, audit: {} });
    }
    if (parts[0] === 'scenarios') {
      const sc = scenarios.find(s => s.id === Number(parts[1]));
      if (!sc) return json(route, 404, { error: 'scenario_not_found' });
      if (parts[2] === 'preview') return json(route, 200, preview({ ...sc, ...body }));
      if (method === 'GET') return json(route, 200, sc);
      if (!body || !strict(body, ['request_key', 'version', ...INPUT_FIELDS])) return json(route, 400, { error: 'invalid_request' });
      if (body.version !== sc.version) return json(route, 409, { error: 'version_conflict' });
      const next = { ...sc } as unknown as Body;
      for (const k of INPUT_FIELDS) if (k in body) next[k] = body[k];
      const n = next as unknown as Scenario;
      if (n.enabled && n.trigger_type === 'paid_expiry' && n.delay_value === 24 && n.delay_unit === 'hours') {
        return json(route, 409, { error: 'duplicates_expiry_reminder', field: 'delay_value', reason: 'duplicates_expiry_reminder', detail: '1d' });
      }
      if (n.enabled && !sc.enabled) n.enabled_at = now();
      n.version = sc.version + 1;
      scenarios[scenarios.indexOf(sc)] = n;
      return json(route, 200, { scenario: n, target_id: n.id, replayed: false, audit: {} });
    }
    if (parts[0] === 'preview') return json(route, 200, preview(body ?? {}));
    if (parts[0] === 'images') {
      if (method === 'POST') {
        const bytes = Buffer.from(String(body?.data), 'base64');
        const image = { id: nextImage++, mime: 'image/png', size_bytes: bytes.length, sha256: 'b'.repeat(64), created_at: now() };
        images.set(image.id, image);
        return json(route, 200, { image, target_id: image.id, replayed: false, audit: {} });
      }
      const id = Number(parts[1]);
      if (method === 'GET') return route.fulfill({ status: 200, contentType: 'image/png', body: PNG });
      if (scenarios.some(s => s.image_id === id)) return json(route, 409, { error: 'image_in_use' });
      images.delete(id);
      return json(route, 200, { image: null, target_id: id, replayed: false, audit: {} });
    }
    const limit = 3;
    const offset = Number(url.searchParams.get('offset') ?? 0);
    if (parts[0] === 'deliveries') {
      const status = url.searchParams.get('status');
      const rows = deliveries.filter(d => !status || d.status === status);
      return json(route, 200, { deliveries: rows.slice(offset, offset + limit), total: rows.length, limit, offset });
    }
    if (parts[0] === 'coupons') return json(route, 200, { coupons, total: coupons.length, limit, offset: 0 });
    problems.push(`unhandled ${method} ${url.pathname}`);
    return json(route, 404, { error: 'not_found' });
  });
  return {
    calls, problems, scenarios, settings: () => settings,
    last: (method: string, fragment: string) => [...calls].reverse().find(c => c.method === method && c.path.includes(fragment)),
  };
}

async function open(page: Page, hash = '#/warmup') {
  await page.goto(`/${hash}`);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
}

test('opens the section from the navigation and lists the 14 scenarios in three categories', async ({ page }) => {
  const api = await backend(page);
  // Start on another warm-up page (the mock serves only warm-up routes), then use the main navigation.
  await page.goto('/#/warmup/settings');
  await expect(page.locator('#wu-set-max_per_day')).toBeVisible();
  await page.getByRole('navigation', { name: 'Разделы панели' }).getByRole('link', { name: 'Прогрев клиентов' }).click();
  await expect(page).toHaveURL(/#\/warmup$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Прогрев клиентов' })).toBeVisible();
  await expect(page.locator('.wu-card')).toHaveCount(14);
  await expect(page.locator('#wu-category-trial .wu-card')).toHaveCount(6);
  await expect(page.locator('#wu-category-subscription .wu-card')).toHaveCount(3);
  await expect(page.locator('#wu-category-winback .wu-card')).toHaveCount(5);
  await expect(page.locator('.wu-card[data-scenario="1"] .wu-card-trigger')).toHaveText('Через 12 часов после первого запуска бота');
  await expect(page.getByRole('switch', { name: 'Прогрев включён' })).not.toBeChecked();
  await expect(page.locator('.wu-kpi').filter({ hasText: 'Активных' }).locator('dd')).toHaveText('11');
  expect(api.problems).toEqual([]);
});

test('creates a disabled scenario, then edits, previews and saves it', async ({ page }) => {
  const api = await backend(page);
  await open(page, '#/warmup');
  await page.getByRole('link', { name: 'Новый сценарий' }).first().click();
  await expect(page).toHaveURL(/#\/warmup\/create$/);
  await page.getByLabel('Название').fill('Скидка после пробника');
  await page.getByLabel('Категория').selectOption('winback');
  await page.getByLabel('Событие').selectOption('trial_period_end');
  await page.getByLabel('Момент').selectOption('after');
  await page.locator('#wu-delay').fill('5');
  await expect(page.locator('#wu-trigger-text')).toHaveText('Через 5 дней после окончания пробного периода');
  await page.locator('#wu-key').fill('trial_discount_5d');
  await page.getByRole('button', { name: 'Создать сценарий' }).click();
  await expect(page).toHaveURL(/#\/warmup\/scenario\/15$/);
  await expect(page.locator('.editor-status')).toContainText('Сценарий создан');
  await expect(page.locator('.wu-status-row .tag')).toHaveText('ВЫКЛ');
  const created = api.calls.find(c => c.method === 'POST' && c.path === '/admin/api/warmup/scenarios');
  expect(created?.body).toMatchObject({ key: 'trial_discount_5d', enabled: false, category: 'winback', delay_value: 5, delay_unit: 'days' });
  expect(api.scenarios[14].enabled).toBe(false);

  // Message with the toolbar and a variable.
  const message = page.locator('#wu-message');
  await message.fill('Скидка для вас');
  await message.evaluate((node: HTMLTextAreaElement) => node.setSelectionRange(0, 6));
  await page.getByRole('button', { name: 'Жирный' }).click();
  await expect(message).toHaveValue('<b>Скидка</b> для вас');
  await message.evaluate((node: HTMLTextAreaElement) => node.setSelectionRange(node.value.length, node.value.length));
  await page.locator('[data-variable="username"]').click();
  await expect(page.locator('[data-variable="plan_name"]')).toBeDisabled();

  // Buttons: callback from the whitelist, a URL button in the same row.
  await page.getByRole('button', { name: 'Добавить кнопку' }).click();
  await page.locator('#wu-b0-text').fill('Тарифы');
  await page.locator('#wu-b0-value').selectOption('buy_premium_list');
  await page.getByRole('button', { name: 'Добавить кнопку' }).click();
  await page.locator('#wu-b1-kind').selectOption('url');
  await page.locator('#wu-b1-text').fill('Сайт');
  await page.locator('#wu-b1-value').fill('https://example.com');
  await page.locator('#wu-b1-row').check();
  await expect(page.locator('.wu-kb-row')).toHaveCount(1);

  // Discount with a personal code.
  await page.locator('#wu-discount-percent').check();
  await page.locator('#wu-percent').fill('15');
  await page.getByRole('button', { name: 'Вставить {{discount_code}} в текст' }).click();
  await expect(page.locator('#wu-coupon-example')).toHaveText('Пример кода: VPN-XXXXXXXX');
  await expect(page.getByText('Купон пока не применяется автоматически при оплате.')).toBeVisible();

  await page.locator('#wu-preview').click();
  await expect(page.locator('.tg-text b')).toHaveText('Скидка');
  await expect(page.locator('.tg-text')).toContainText('@bob');
  await expect(page.locator('.tg-text')).toContainText('TEST-7K3P9QXD');
  await expect(page.locator('.tg-kb-btn')).toHaveText(['Тарифы', 'Сайт']);
  expect(api.last('POST', '/scenarios/15/preview')?.body).toMatchObject({ context: { username: 'bob', plan_name: 'Gold' } });

  await page.getByRole('button', { name: 'Сохранить' }).click();
  await expect(page.locator('.editor-status')).toHaveText('Сценарий сохранён.');
  const saved = api.last('PATCH', '/scenarios/15')?.body;
  expect(saved).toMatchObject({ version: 1, discount_percent: 15 });
  // A partial edit: the prefix was already stored at create time, so it is not resent.
  expect(saved).not.toHaveProperty('coupon_template');
  expect(api.scenarios[14]).toMatchObject({ coupon_template: 'VPN', discount_percent: 15, version: 2 });
  expect(saved?.buttons).toEqual([
    { text: 'Тарифы', kind: 'callback', value: 'buy_premium_list' },
    { text: 'Сайт', kind: 'url', value: 'https://example.com', same_row: true },
  ]);
  expect(api.scenarios[14].enabled).toBe(false);
  expect(api.problems).toEqual([]);
});

test('uploads, previews and removes a picture', async ({ page }) => {
  const api = await backend(page);
  await open(page, '#/warmup/scenario/2');
  await expect(page.locator('.wu-image-empty')).toContainText('Картинки нет');
  await page.locator('#wu-image-file').setInputFiles({ name: 'promo.png', mimeType: 'image/png', buffer: PNG });
  await expect(page.locator('.wu-image-facts')).toContainText('promo.png');
  await expect(page.locator('.wu-image-thumb')).toHaveAttribute('src', '/admin/api/warmup/images/70');
  await expect(page.locator('#wu-counter')).toContainText('подпись к картинке');
  await page.locator('#wu-preview').click();
  await expect(page.locator('.tg-photo')).toBeVisible();
  await expect.poll(() => page.locator('.tg-photo').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(1);
  await page.locator('#wu-image-file').setInputFiles({ name: 'big.png', mimeType: 'image/png', buffer: Buffer.alloc(2 * 1024 * 1024 + 1) });
  await expect(page.locator('#wu-image-error')).toHaveText('Файл больше 2 МБ.');
  await page.getByRole('button', { name: 'Удалить', exact: true }).click();
  await expect(page.locator('.wu-image-empty')).toBeVisible();
  await expect.poll(() => api.last('DELETE', '/images/70')).toBeTruthy();
  expect(api.problems).toEqual([]);
});

test('enables and disables scenarios; scenario 8 conflicts with the expiry reminder', async ({ page }) => {
  const api = await backend(page);
  await open(page);
  await page.locator('#wu-toggle-7').click();
  await expect(page.locator('.wu-card[data-scenario="7"] .tag')).toHaveText('ВКЛ');
  await expect(page.locator('.wu-card[data-scenario="7"] .notice')).toContainText('Сценарий включён с');
  await page.locator('#wu-toggle-10').click();
  await expect(page.locator('.wu-card[data-scenario="10"] .tag')).toHaveText('ВЫКЛ');
  await page.locator('#wu-toggle-8').click();
  await expect(page.locator('.wu-card[data-scenario="8"] .notice')).toHaveText('Этот сценарий конфликтует с существующим напоминанием о подписке и не может быть включён одновременно с ним.');
  expect(api.scenarios[7].enabled).toBe(false);
  expect(api.problems).toEqual([]);
});

test('asks before leaving an editor with unsaved changes', async ({ page }) => {
  await backend(page);
  await open(page, '#/warmup/scenario/3');
  await page.locator('#wu-name').fill('Не сохранено');
  let asked = '';
  page.once('dialog', dialog => { asked = dialog.message(); void dialog.dismiss(); });
  await page.getByRole('link', { name: 'Назад к сценариям' }).click();
  await expect.poll(() => asked).toContain('несохранённые изменения');
  await expect(page).toHaveURL(/#\/warmup\/scenario\/3$/);
  await expect(page.locator('#wu-name')).toHaveValue('Не сохранено');
});

test('saves the global settings', async ({ page }) => {
  const api = await backend(page);
  await open(page, '#/warmup/settings');
  await expect(page.getByText('События старше этого окна не догоняются после включения.', { exact: false })).toBeVisible();
  await page.locator('#wu-set-max_per_day').fill('4');
  await page.locator('#wu-set-catchup_hours').fill('12');
  await page.getByRole('button', { name: 'Сохранить' }).click();
  await expect(page.locator('.editor-status')).toHaveText('Настройки сохранены.');
  expect(api.last('PUT', '/settings')?.body).toMatchObject({ version: 1, enabled: false, max_per_day: 4, cooldown_hours: 12, catchup_hours: 12 });
  expect(api.settings().version).toBe(2);
  expect(api.problems).toEqual([]);
});

test('browses the delivery history with filters and pages, and the coupons', async ({ page }) => {
  const api = await backend(page);
  await open(page, '#/warmup/deliveries');
  await expect(page.locator('.wu-table tbody tr')).toHaveCount(3);
  await expect(page.getByText('Страница 1 из 3')).toBeVisible();
  await page.getByRole('button', { name: 'Следующая' }).click();
  await expect(page.getByText('Страница 2 из 3')).toBeVisible();
  expect(api.last('GET', '/deliveries')?.search).toBe('?offset=3');
  await page.locator('#wu-f-status').selectOption('failed');
  await page.getByRole('button', { name: 'Применить' }).click();
  await expect(page.locator('.wu-table tbody tr')).toHaveCount(2);
  await expect(page.locator('.wu-table tbody')).toContainText('Forbidden: bot was blocked');
  expect(api.last('GET', '/deliveries')?.search).toBe('?status=failed');

  await page.getByRole('link', { name: 'Купоны' }).click();
  await expect(page).toHaveURL(/#\/warmup\/coupons$/);
  const row = page.locator('.wu-table tbody tr').first();
  await expect(row).toContainText('VPN-AB12CDEF');
  await expect(row.locator('td').nth(6)).toHaveText('—');
  await expect(row.locator('td').nth(7)).toHaveText('—');
  expect(api.problems).toEqual([]);
});

test('the editor, tables and preview fit a narrow viewport', async ({ page }) => {
  await backend(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '#/warmup/scenario/3');
  await expect(page.locator('.tg-message')).toBeVisible();
  const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(await overflow()).toBeLessThanOrEqual(0);
  await open(page, '#/warmup/deliveries');
  await expect(page.locator('.wu-table tbody tr').first()).toBeVisible();
  expect(await overflow()).toBeLessThanOrEqual(0);
});
