import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../admin/src/api';
import {
  RequestKeys, SCENARIO_CONFLICT_TEXT, REMINDER_CONFLICT_TEXT, WarmupApi, couponExample, durationWords, formFromScenario,
  formValues, isUncertain, parseOverview, parseWarmupRoute, scenarioInput, suggestKey, triggerText, warmupErrorText, warmupHash,
  type WarmupButton, type WarmupRoute,
} from '../admin/src/warmup';
import { WarmupScreen, resetWarmupMemory, type WarmupHost } from '../admin/src/warmup-ui';

// The customer warm-up section (admin/src/warmup-ui.ts over admin/src/warmup.ts)
// rendered in jsdom against an in-memory backend that follows the contract of
// internal/web/admin_warmup_api.go → service.WarmupService:
//   - 14 seed scenarios of migration 050 (trial 6, subscription 3, winback 5);
//   - PATCH/PUT are optimistic-locked by version (409 version_conflict);
//   - enabling a paid-expiry scenario at a reminder lead time (24h) is
//     409 duplicates_expiry_reminder;
//   - POST /scenarios needs a key ^[a-z][a-z0-9_]{2,63}$ and keeps enabled as sent;
//   - previews write nothing (no delivery, no coupon).
// No real Telegram user is involved.

const T0 = '2026-10-01T10:00:00Z';
const MB = 1024 * 1024;

type Json = Record<string, unknown>;
interface Call { method: string; path: string; body: Json | null }

interface Scenario {
  id: number; key: string; category: string; name: string; description: string; trigger_type: string; timing: string;
  delay_value: number; delay_unit: string; enabled: boolean; enabled_at: string | null; message_html: string;
  image_id: number | null; cooldown_hours: number | null; daily_cap: number; discount_percent: number; coupon_template: string;
  coupon_ttl_hours: number; position: number; version: number; created_at: string; updated_at: string;
  buttons: WarmupButton[]; conditions: Json;
}

const cb = (text: string, value: string): WarmupButton => ({ text, kind: 'callback', value });
const SEED: readonly [string, string, string, string, string, number, string, boolean, string, WarmupButton[], Json, number][] = [
  ['trial_not_activated', 'trial', 'Пробник не активирован', 'bot_started', 'after', 12, 'hours', true, '<b>Пробный период ждёт вас</b>\n\nЭто {{trial_duration}} без оплаты.', [{ text: 'Активировать пробник', kind: 'url', value: '' }], { had_trial: false, has_subscription: false, has_active_paid: false }, 10],
  ['trial_not_connected_3h', 'trial', 'Пробник не подключён, 3 часа', 'trial_activated', 'after', 3, 'hours', true, '<b>Осталось подключиться</b>', [cb('Открыть подписку', 'menu_subscription')], { connected: false, has_active_paid: false }, 20],
  ['trial_not_connected_24h', 'trial', 'Пробник не подключён, сутки', 'trial_activated', 'after', 24, 'hours', true, '<b>VPN всё ещё не подключён</b>', [cb('Открыть подписку', 'menu_subscription'), cb('Поддержка', 'menu_support')], { connected: false, has_active_paid: false }, 30],
  ['trial_ending', 'trial', 'Пробник заканчивается', 'trial_period_end', 'before', 24, 'hours', true, 'Доступ до {{expire_date}}.', [cb('Выбрать тариф', 'buy_premium_list')], { has_active_paid: false }, 40],
  ['trial_no_purchase', 'trial', 'Пробник взяли, но не купили', 'trial_activated', 'after', 2, 'days', true, '<b>Как вам VPN?</b>', [cb('Выбрать тариф', 'buy_premium_list')], { has_active_paid: false, ever_paid: false }, 50],
  ['trial_ended', 'trial', 'Пробник закончился', 'trial_period_end', 'after', 0, 'hours', true, '<b>Пробный период закончился</b>', [cb('Выбрать тариф', 'buy_premium_list')], { has_active_paid: false }, 60],
  ['sub_expiring_48h', 'subscription', 'Подписка: осталось 48 часов', 'paid_expiry', 'before', 48, 'hours', false, '«{{plan_name}}» до {{expire_date}}.', [cb('Продлить подписку', 'buy_premium_list')], {}, 10],
  ['sub_expiring_24h', 'subscription', 'Подписка: остались сутки', 'paid_expiry', 'before', 24, 'hours', false, '«{{plan_name}}» до {{expire_date}}.', [cb('Продлить подписку', 'buy_premium_list')], {}, 20],
  ['sub_expired', 'subscription', 'Подписка закончилась', 'paid_expired', 'after', 0, 'hours', false, 'Срок «{{plan_name}}» истёк.', [cb('Продлить подписку', 'buy_premium_list')], { has_active_paid: false }, 30],
  ['winback_3d', 'winback', 'Возврат клиента: 3 дня', 'paid_expired', 'after', 3, 'days', true, '<b>Мы скучаем</b>', [cb('Продлить подписку', 'buy_premium_list')], { has_active_paid: false }, 10],
  ['trial_winback_3d', 'winback', 'После пробника: 3 дня', 'trial_period_end', 'after', 3, 'days', true, '<b>Понравилось?</b>', [cb('Выбрать тариф', 'buy_premium_list')], { has_active_paid: false, ever_paid: false }, 20],
  ['winback_7d', 'winback', 'Возврат клиента: 7 дней', 'paid_expired', 'after', 7, 'days', true, '<b>Возвращайтесь</b>', [cb('Продлить подписку', 'buy_premium_list')], { has_active_paid: false }, 30],
  ['winback_14d', 'winback', 'Возврат клиента: 14 дней', 'paid_expired', 'after', 14, 'days', true, '<b>Две недели без VPN</b>', [cb('Посмотреть тарифы', 'buy_premium_list')], { has_active_paid: false }, 40],
  ['winback_30d', 'winback', 'Возврат клиента: 30 дней', 'paid_expired', 'after', 30, 'days', true, '<b>Месяц без VPN</b>', [cb('Продлить подписку', 'buy_premium_list')], { has_active_paid: false }, 50],
];

const REGISTRY = {
  triggers: [
    { type: 'bot_started', label: 'Первый запуск бота', description: 'Первый /start', timings: ['after'] },
    { type: 'trial_activated', label: 'Пробник активирован', description: 'Пробник привязан', timings: ['after'] },
    { type: 'trial_period_end', label: 'Окончание пробного периода', description: 'Срок пробника', timings: ['before', 'after'] },
    { type: 'paid_expiry', label: 'Окончание платной подписки', description: 'Срок платной', timings: ['before'] },
    { type: 'paid_expired', label: 'Платная подписка закончилась', description: 'Срок истёк', timings: ['after'] },
  ],
  variables: [
    { name: 'username', label: 'Имя пользователя', description: '@username', sample: '@dictator_vpn_user_long_name', triggers: null, discount: false },
    { name: 'telegram_id', label: 'Telegram ID', description: 'ID', sample: '1234567890', triggers: null, discount: false },
    { name: 'plan_name', label: 'Тариф', description: 'Тариф', sample: 'Premium', triggers: ['paid_expiry', 'paid_expired'], discount: false },
    { name: 'trial_days', label: 'Дней пробника', description: 'Дни', sample: '3', triggers: null, discount: false },
    { name: 'trial_duration', label: 'Длительность пробника', description: 'Словами', sample: '72 часа', triggers: null, discount: false },
    { name: 'expire_date', label: 'Дата окончания', description: 'МСК', sample: '31.12.2026 23:59 МСК', triggers: ['trial_period_end', 'paid_expiry', 'paid_expired'], discount: false },
    { name: 'discount_code', label: 'Код скидки', description: 'Купон', sample: 'TEST-7K3P9QXD', triggers: null, discount: true },
    { name: 'discount_percent', label: 'Размер скидки', description: 'Процент', sample: '15', triggers: null, discount: true },
  ],
  callbacks: [
    { data: 'menu_subscription', label: 'Моя подписка' }, { data: 'buy_premium_list', label: 'Тарифы (купить Premium)' },
    { data: 'menu_support', label: 'Поддержка' }, { data: 'back_to_start', label: 'Главное меню' },
  ],
  conditions: [
    { key: 'had_trial', label: 'Был пробник', description: 'Активировал пробник', kind: 'bool' },
    { key: 'has_subscription', label: 'Есть подписка', description: 'Любая подписка', kind: 'bool' },
    { key: 'has_active_paid', label: 'Активная платная', description: 'Действующая платная', kind: 'bool' },
    { key: 'ever_paid', label: 'Платил', description: 'Была оплата', kind: 'bool' },
    { key: 'connected', label: 'Подключался', description: 'Забирал конфиг', kind: 'bool' },
    { key: 'languages', label: 'Язык Telegram', description: 'Коды языка', kind: 'languages' },
  ],
  limits: {
    image_max_bytes: 2 * MB, max_buttons: 8, max_text_length: 4096, max_caption_length: 1024, max_cooldown_hours: 720,
    max_daily_cap: 100000, max_coupon_ttl_hours: 8760, max_per_day_limit: 20, max_catchup_hours: 72,
  },
};

const PATCH_FIELDS = new Set(['category', 'name', 'description', 'trigger_type', 'timing', 'delay_value', 'delay_unit', 'enabled',
  'message_html', 'image_id', 'buttons', 'conditions', 'cooldown_hours', 'daily_cap', 'discount_percent', 'coupon_template',
  'coupon_ttl_hours', 'position']);

interface Options {
  /** PATCH /scenarios answered with this error once before it is applied. */
  patchFailure?: ApiError;
  /** Another session saved scenario N meanwhile: its PATCH answers 409. */
  staleScenario?: number;
  staleSettings?: boolean;
  deliveries?: number;
  coupons?: number;
}

function backend(options: Options = {}) {
  let clock = Date.parse(T0);
  const now = () => new Date(clock += 60_000).toISOString();
  const scenarios: Scenario[] = SEED.map(([key, category, name, trigger, timing, value, unit, enabled, html, buttons, conditions, position], i) => ({
    id: i + 1, key, category, name, description: '', trigger_type: trigger, timing, delay_value: value, delay_unit: unit, enabled,
    enabled_at: enabled ? T0 : null, message_html: html, image_id: null, cooldown_hours: null, daily_cap: 0, discount_percent: 0,
    coupon_template: '', coupon_ttl_hours: 48, position, version: 1, created_at: T0, updated_at: T0, buttons, conditions,
  }));
  let settings = { enabled: false, max_per_day: 2, cooldown_hours: 12, catchup_hours: 6, version: 1, updated_at: T0 };
  const images = new Map<number, { id: number; mime: string; size_bytes: number; sha256: string; created_at: string }>();
  let nextImage = 50;
  const deliveries = Array.from({ length: options.deliveries ?? 5 }, (_, i) => ({
    id: i + 1, scenario_id: (i % 3) + 1, scenario_name: SEED[i % 3][2], telegram_id: 1000 + i, username: i % 2 ? '' : `user${i}`,
    status: ['sent', 'retry', 'failed', 'blocked', 'sent'][i % 5], attempts: i % 5 === 1 ? 2 : 1, error: i % 5 === 2 ? 'Bad Request: chat not found' : '',
    coupon_code: i === 0 ? 'VPN-7K3P9QXD' : null, created_at: T0, due_at: T0, sent_at: i % 5 === 0 ? T0 : null,
  }));
  const coupons = Array.from({ length: options.coupons ?? 3 }, (_, i) => ({
    id: i + 1, code: `VPN-CODE000${i}`, telegram_id: 2000 + i, scenario_id: 1, percent: 15, status: 'issued',
    effective_status: i === 2 ? 'expired' : 'issued', created_at: T0, expires_at: '2026-10-03T10:00:00Z', redeemed_at: null, order_id: null,
  }));
  const calls: Call[] = [];
  let patchFailure = options.patchFailure;

  const fail = (code: string, status: number, field = '', reason = '', detail = ''): never => { throw new ApiError(code, status, 0, field, reason, detail); };
  const summary = () => ({
    scenarios: scenarios.length, enabled_scenarios: scenarios.filter(s => s.enabled).length,
    last_24h: { sent: 7, retry: 1, failed: 2, blocked: 3 }, sent_today: 4, sent_7d: 21, active_coupons: 5, coupons_created: 9,
    contacts: 120, blocked_contacts: 6,
    scenario_stats: [{ scenario_id: 1, sent: 11, last_sent_at: T0 }, { scenario_id: 10, sent: 3, last_sent_at: T0 }],
  });
  const overview = () => ({
    settings,
    categories: ['trial', 'subscription', 'winback'].map(key => ({
      key, label: key, scenarios: scenarios.filter(s => s.category === key).length, enabled: scenarios.filter(s => s.category === key && s.enabled).length,
    })),
    scenarios, summary: summary(), registry: REGISTRY,
  });
  const render = (input: Json) => {
    const discount = Number(input.discount_percent) > 0;
    const html = String(input.message_html);
    const problems: Json[] = [];
    const buttons = (input.buttons as WarmupButton[]) ?? [];
    const hidden = buttons.filter(b => (b.kind === 'url' || b.kind === 'web_app') && !b.value).length;
    if (hidden) problems.push({ field: 'buttons', code: 'button_url_missing', detail: String(hidden), severity: 'warning' });
    if (discount && !html.includes('{{discount_code}}')) {
      problems.push({ field: 'message_html', code: 'discount_code_required', detail: '', severity: 'invalid' });
      return { valid: false, problems, message: null, image: null, variables: {}, discount_code: '', discount_code_test: false };
    }
    const values: Record<string, string> = {
      username: `@${(input.context as Json | undefined)?.username ?? 'dictator_vpn_user'}`, plan_name: String((input.context as Json | undefined)?.plan_name ?? ''),
      trial_duration: '3 дня', trial_days: '3', expire_date: '31.12.2026 23:59 МСК', discount_code: discount ? 'TEST-7K3P9QXD' : '',
      discount_percent: String(input.discount_percent),
    };
    const out = html.replace(/\{\{([a-z_]+)\}\}/g, (_, name: string) => values[name] ?? '');
    const imageId = input.image_id === null || input.image_id === undefined ? null : Number(input.image_id);
    return {
      valid: true, problems, image: imageId !== null ? images.get(imageId) ?? null : null, variables: values,
      message: {
        html: out, image_id: imageId, buttons: buttons.filter(b => !((b.kind === 'url' || b.kind === 'web_app') && !b.value)),
        hidden_buttons: hidden, visible_length: out.replace(/<[^>]*>/g, '').length, max_length: imageId !== null ? 1024 : 4096,
      },
      discount_code: discount ? 'TEST-7K3P9QXD' : '', discount_code_test: discount,
    };
  };
  const reminderConflict = (s: Scenario) => s.enabled && s.trigger_type === 'paid_expiry' && s.timing === 'before' &&
    ((s.delay_unit === 'hours' && [72, 24, 3].includes(s.delay_value)) || (s.delay_unit === 'days' && [3, 1].includes(s.delay_value)));

  const request = vi.fn(async (method: string, path: string, raw?: string): Promise<unknown> => {
    const body = raw === undefined ? null : JSON.parse(raw) as Json;
    calls.push({ method, path, body });
    const url = new URL(`http://admin${path}`);
    const parts = url.pathname.replace(/^\/admin\/api\/warmup\/?/, '').split('/').filter(Boolean);
    if (method === 'GET' && parts.length === 0) return structuredClone(overview());
    if (parts[0] === 'settings') {
      if (method === 'GET') return { ...settings };
      if (options.staleSettings || body?.version !== settings.version) fail('version_conflict', 409);
      settings = { enabled: Boolean(body?.enabled), max_per_day: Number(body?.max_per_day), cooldown_hours: Number(body?.cooldown_hours),
        catchup_hours: Number(body?.catchup_hours), version: settings.version + 1, updated_at: now() };
      return { settings, replayed: false };
    }
    if (parts[0] === 'scenarios' && parts.length === 1) {
      if (method === 'GET') return { scenarios: structuredClone(scenarios) };
      const key = String(body?.key ?? '');
      if (!/^[a-z][a-z0-9_]{2,63}$/.test(key)) fail('validation_error', 400, 'key', 'format');
      if (scenarios.some(s => s.key === key)) fail('scenario_key_taken', 409);
      const at = now();
      const created: Scenario = {
        id: scenarios.length + 1, key, version: 1, created_at: at, updated_at: at, enabled_at: body?.enabled ? at : null,
        ...(body as unknown as Omit<Scenario, 'id' | 'key' | 'version' | 'created_at' | 'updated_at' | 'enabled_at'>),
      };
      delete (created as unknown as Json).request_key;
      scenarios.push(created);
      return { scenario: created, target_id: created.id, replayed: false };
    }
    if (parts[0] === 'scenarios') {
      const sc = scenarios.find(s => s.id === Number(parts[1]));
      if (!sc) return fail('scenario_not_found', 404);
      if (parts[2] === 'preview') return render({ ...sc, ...body });
      if (method === 'GET') return structuredClone(sc);
      if (patchFailure) { const e = patchFailure; patchFailure = undefined; throw e; }
      if (options.staleScenario === sc.id || body?.version !== sc.version) fail('version_conflict', 409);
      for (const k of Object.keys(body ?? {})) if (!PATCH_FIELDS.has(k) && k !== 'request_key' && k !== 'version') fail('invalid_request', 400);
      const next: Scenario = { ...sc };
      for (const [k, v] of Object.entries(body ?? {})) if (PATCH_FIELDS.has(k)) (next as unknown as Json)[k] = v;
      if (reminderConflict(next)) fail('duplicates_expiry_reminder', 409, 'delay_value', 'duplicates_expiry_reminder', 'expiry_reminder_1d');
      if (next.buttons.some(b => b.kind === 'callback' && !REGISTRY.callbacks.some(c => c.data === b.value))) fail('callback_not_allowed', 400, 'buttons', 'button_callback', 'buttons[0]');
      const at = now();
      if (next.enabled && !sc.enabled) next.enabled_at = at;
      next.version = sc.version + 1;
      next.updated_at = at;
      scenarios[scenarios.indexOf(sc)] = next;
      return { scenario: structuredClone(next), target_id: next.id, replayed: false };
    }
    if (parts[0] === 'preview') return render(body ?? {});
    if (parts[0] === 'images' && method === 'POST') {
      const bytes = atob(String(body?.data));
      const image = { id: nextImage++, mime: bytes.startsWith('\x89PNG') ? 'image/png' : 'image/jpeg', size_bytes: bytes.length, sha256: 'a'.repeat(64), created_at: now() };
      images.set(image.id, image);
      return { image, target_id: image.id, replayed: false };
    }
    if (parts[0] === 'images' && method === 'DELETE') {
      const id = Number(parts[1]);
      if (scenarios.some(s => s.image_id === id)) fail('image_in_use', 409);
      images.delete(id);
      return { image: null, target_id: id, replayed: false };
    }
    const limit = 2;
    const offset = Number(url.searchParams.get('offset') ?? 0);
    if (parts[0] === 'deliveries') {
      const status = url.searchParams.get('status');
      const telegram = url.searchParams.get('telegram_id');
      const rows = deliveries.filter(d => (!status || d.status === status) && (!telegram || String(d.telegram_id) === telegram));
      return { deliveries: rows.slice(offset, offset + limit), total: rows.length, limit, offset };
    }
    if (parts[0] === 'coupons') {
      const status = url.searchParams.get('status');
      const rows = coupons.filter(c => !status || c.effective_status === status);
      return { coupons: rows.slice(offset, offset + limit), total: rows.length, limit, offset };
    }
    return fail('not_found', 404);
  });

  return { request, calls, scenarios, images, deliveries, settings: () => settings };
}

// ---------------------------------------------------------------------------
// Harness

interface Mounted {
  screen: WarmupScreen;
  host: WarmupHost & { navigated: string[]; toasts: string[]; unsaved: () => boolean };
  page: { header: HTMLElement; title: HTMLElement; desc: HTMLElement };
  root: HTMLElement;
}

const flush = async (rounds = 6) => { for (let i = 0; i < rounds; i++) await new Promise(r => setTimeout(r, 0)); };

async function mount(server: ReturnType<typeof backend>, route: WarmupRoute, confirm = true): Promise<Mounted> {
  const navigated: string[] = [];
  const toasts: string[] = [];
  let check: (() => boolean) | null = null;
  const host = {
    api: new WarmupApi({ request: (method, path, body) => server.request(method, path, body) }),
    icon: () => document.createElement('i'),
    toast: (text: string) => { toasts.push(text); },
    confirm: vi.fn(() => Promise.resolve(confirm)),
    setUnsaved: (c: (() => boolean) | null) => { check = c; },
    signedOut: () => Promise.resolve(false),
    navigate: (hash: string) => { navigated.push(hash); },
    sessionUsed: () => undefined,
    navigated, toasts,
    unsaved: () => check?.() ?? false,
  };
  const page = { header: document.createElement('header'), title: document.createElement('h1'), desc: document.createElement('p') };
  const screen = new WarmupScreen(host, route, page);
  const root = document.createElement('main');
  root.append(page.header, screen.body);
  document.body.replaceChildren(root);
  await screen.load();
  await flush();
  return { screen, host, page, root };
}

const $ = <T extends HTMLElement = HTMLElement>(selector: string): T => {
  const node = document.querySelector<T>(selector);
  if (!node) throw new Error(`missing ${selector}`);
  return node;
};
const type = (selector: string, value: string) => {
  const node = $<HTMLInputElement | HTMLTextAreaElement>(selector);
  node.value = value;
  node.dispatchEvent(new Event('input', { bubbles: true }));
};
const choose = (selector: string, value: string) => {
  const node = $<HTMLSelectElement | HTMLInputElement>(selector);
  if (node instanceof HTMLInputElement && (node.type === 'checkbox' || node.type === 'radio')) node.checked = value === 'on';
  else node.value = value;
  node.dispatchEvent(new Event('change', { bubbles: true }));
};
const click = (selector: string) => $<HTMLElement>(selector).click();
const text = (selector: string) => $(selector).textContent ?? '';
const buttonByText = (label: string, scope: ParentNode = document) => {
  const node = Array.from(scope.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent?.trim() === label);
  if (!node) throw new Error(`missing button ${label}`);
  return node;
};
const lastCall = (server: ReturnType<typeof backend>, method: string, fragment: string) =>
  [...server.calls].reverse().find(c => c.method === method && c.path.includes(fragment));

function pngFile(bytes: number, name = 'promo.png', mime = 'image/png'): File {
  const data = new Uint8Array(bytes);
  data.set([0x89, 0x50, 0x4e, 0x47]);
  return new File([data], name, { type: mime });
}
function pick(file: File) {
  const input = $<HTMLInputElement>('#wu-image-file');
  Object.defineProperty(input, 'files', { value: [file], configurable: true });
  input.dispatchEvent(new Event('change'));
}

beforeEach(() => resetWarmupMemory());
afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); });

// ---------------------------------------------------------------------------

describe('1. navigation', () => {
  it('parses and builds every warm-up route', () => {
    const routes: [string, WarmupRoute][] = [
      ['warmup', { view: 'overview' }], ['warmup/create', { view: 'create' }], ['warmup/settings', { view: 'settings' }],
      ['warmup/deliveries', { view: 'deliveries' }], ['warmup/coupons', { view: 'coupons' }],
      ['warmup/scenario/12', { view: 'scenario', scenarioId: 12 }],
    ];
    for (const [path, route] of routes) {
      expect(parseWarmupRoute(path)).toEqual(route);
      expect(warmupHash(route)).toBe(`#/${path}`);
    }
    for (const bad of ['warmup/scenario/0', 'warmup/scenario/abc', 'warmup/trial', 'warmupx', 'warmup/create/1']) expect(parseWarmupRoute(bad)).toBeNull();
  });

  it('shows the tab bar and a create link on the overview', async () => {
    await mount(backend(), { view: 'overview' });
    const tabs = Array.from(document.querySelectorAll<HTMLAnchorElement>('.wu-tab')).map(a => [a.textContent, a.getAttribute('href')]);
    expect(tabs).toEqual([['Сценарии', '#/warmup'], ['Настройки', '#/warmup/settings'], ['История отправок', '#/warmup/deliveries'], ['Купоны', '#/warmup/coupons']]);
    expect($('.wu-tab[aria-current="page"]').textContent).toBe('Сценарии');
    expect(Array.from(document.querySelectorAll('a')).some(a => a.getAttribute('href') === '#/warmup/create')).toBe(true);
  });
});

describe('2–4. overview, the 14 seed scenarios and categories', () => {
  it('2. shows the summary and the global switch', async () => {
    await mount(backend(), { view: 'overview' });
    const kpis = Object.fromEntries(Array.from(document.querySelectorAll('.wu-kpi')).map(k => [k.querySelector('dt')?.textContent, k.querySelector('dd')?.textContent]));
    expect(kpis).toEqual({
      'Сценариев всего': '14', 'Активных': '11', 'Отправлено сегодня': '4', 'За 7 дней': '21', 'Retry': '1', 'Failed': '2', 'Blocked': '3', 'Купонов': '5',
    });
    expect($<HTMLInputElement>('#wu-global').checked).toBe(false);
    expect(document.body.textContent).toContain('Прогрев выключен глобально');
  });

  it('3. renders the 14 standard scenarios as cards', async () => {
    await mount(backend(), { view: 'overview' });
    const cards = document.querySelectorAll('.wu-card');
    expect(cards).toHaveLength(14);
    const first = $('.wu-card[data-scenario="1"]');
    expect(first.querySelector('.wu-card-title')?.textContent).toBe('Пробник не активирован');
    expect(first.querySelector('.tag')?.textContent).toBe('ВКЛ');
    expect(first.querySelector('.wu-card-trigger')?.textContent).toBe('Через 12 часов после первого запуска бота');
    expect(first.querySelector('.wu-card-text')?.textContent).toContain('Пробный период ждёт вас');
    expect(first.querySelector('.wu-card-stats')?.textContent).toContain('Отправлено: 11');
    expect(first.textContent).toContain('Кнопка без URL');
    expect($('.wu-card[data-scenario="7"] .tag').textContent).toBe('ВЫКЛ');
    expect($('.wu-card[data-scenario="7"] .wu-card-stats').textContent).toContain('ещё не отправлялся');
  });

  it('4. groups the cards into the three categories in position order', async () => {
    await mount(backend(), { view: 'overview' });
    const names = (category: string) => Array.from(document.querySelectorAll(`#wu-category-${category} .wu-card-title`)).map(n => n.textContent);
    expect(names('trial')).toHaveLength(6);
    expect(names('subscription')).toEqual(['Подписка: осталось 48 часов', 'Подписка: остались сутки', 'Подписка закончилась']);
    expect(names('winback')).toHaveLength(5);
    expect(text('#wu-category-trial-title')).toBe('Пробный период');
    expect(text('#wu-category-subscription .wu-category-meta')).toBe('3 · включено 0');
  });
});

describe('5. create scenario', () => {
  it('creates a disabled scenario with POST /scenarios and opens its editor', async () => {
    const server = backend();
    const { host } = await mount(server, { view: 'create' });
    expect(server.calls.some(c => c.path.includes('/scenarios/') && c.method !== 'GET')).toBe(false);
    expect(text('.wu-status-row')).toContain('создаётся выключенным');
    type('#wu-name', 'Новый прогрев');
    choose('#wu-category', 'winback');
    choose('#wu-trigger', 'paid_expired');
    type('#wu-delay', '10');
    type('#wu-key', 'winback_10d');
    expect(host.unsaved()).toBe(true);
    click('#wu-save');
    await flush();
    const call = lastCall(server, 'POST', '/scenarios');
    expect(call?.path).toBe('/admin/api/warmup/scenarios');
    expect(call?.body).toMatchObject({ key: 'winback_10d', name: 'Новый прогрев', category: 'winback', trigger_type: 'paid_expired', delay_value: 10, delay_unit: 'days', enabled: false });
    expect(String(call?.body?.request_key)).toMatch(/^ui-[0-9a-f]{32}$/);
    const created = server.scenarios.at(-1);
    expect(created).toMatchObject({ id: 15, enabled: false, enabled_at: null, version: 1 });
    expect(host.navigated).toEqual(['#/warmup/scenario/15']);
    expect(server.deliveries).toHaveLength(5);
    // The editor of the created scenario greets with the notice once.
    await mount(server, { view: 'scenario', scenarioId: 15 });
    expect(text('.editor-status')).toContain('Сценарий создан');
    expect(text('.wu-status-row')).toContain('ВЫКЛ');
  });

  it('rejects an invalid or taken key', async () => {
    const server = backend();
    await mount(server, { view: 'create' });
    type('#wu-name', 'X');
    type('#wu-key', 'Bad Key');
    click('#wu-save');
    await flush();
    expect(text('#wu-key-error')).toContain('Латинские');
    expect(server.calls.some(c => c.method === 'POST' && c.path.endsWith('/scenarios'))).toBe(false);
    type('#wu-key', 'trial_ended');
    click('#wu-save');
    await flush();
    expect(text('#wu-key-error')).toBe('Ключ уже занят другим сценарием.');
  });

  it('uses the draft preview POST /preview before the scenario exists', async () => {
    const server = backend();
    await mount(server, { view: 'create' });
    type('#wu-name', 'Черновик');
    type('#wu-message', 'Привет, {{username}}!');
    click('#wu-preview');
    await flush();
    const call = lastCall(server, 'POST', '/warmup/preview');
    expect(call?.body).toMatchObject({ message_html: 'Привет, {{username}}!', enabled: false, context: { username: 'bob', plan_name: 'Gold' } });
    expect(text('.tg-text')).toBe('Привет, @bob!');
    expect(server.scenarios).toHaveLength(14);
  });
});

describe('6–8. edit, enable, disable', () => {
  it('6. saves only the changed fields with the loaded version', async () => {
    const server = backend();
    const { host, page } = await mount(server, { view: 'scenario', scenarioId: 2 });
    expect(page.title.textContent).toContain('Пробник не подключён, 3 часа');
    expect(host.unsaved()).toBe(false);
    type('#wu-name', 'Подключитесь, пожалуйста');
    expect(host.unsaved()).toBe(true);
    expect(text('.unsaved-text')).toBe('Есть несохранённые изменения');
    click('#wu-save');
    await flush();
    const call = lastCall(server, 'PATCH', '/scenarios/2');
    expect(call?.body).toEqual({ request_key: expect.stringMatching(/^ui-/), version: 1, name: 'Подключитесь, пожалуйста' });
    expect(text('.editor-status')).toBe('Сценарий сохранён.');
    expect(host.unsaved()).toBe(false);
    expect(server.scenarios[1].version).toBe(2);
  });

  it('7. enables a scenario from its card and shows the new enabled_at', async () => {
    const server = backend();
    await mount(server, { view: 'overview' });
    click('#wu-toggle-7');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/7')?.body).toMatchObject({ version: 1, enabled: true });
    expect($('.wu-card[data-scenario="7"] .tag').textContent).toBe('ВКЛ');
    expect($('.wu-card[data-scenario="7"] .notice').textContent).toContain('Сценарий включён с');
    expect(text('#wu-category-subscription .wu-category-meta')).toBe('3 · включено 1');
    expect(server.scenarios[6].enabled_at).not.toBeNull();
  });

  it('8. disables a scenario in the editor and keeps unsaved edits', async () => {
    const server = backend();
    const { host } = await mount(server, { view: 'scenario', scenarioId: 10 });
    type('#wu-name', 'Ещё не сохранено');
    click('#wu-enable');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/10')?.body).toEqual({ request_key: expect.any(String), version: 1, enabled: false });
    expect(text('.wu-status-row')).toContain('ВЫКЛ');
    expect($<HTMLInputElement>('#wu-name').value).toBe('Ещё не сохранено');
    expect(host.unsaved()).toBe(true);
    expect(server.scenarios[9].name).toBe('Возврат клиента: 3 дня');
  });

  it('asks before enabling and does nothing when declined', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 9 }, false);
    click('#wu-enable');
    await flush();
    expect(server.calls.some(c => c.method === 'PATCH')).toBe(false);
  });
});

describe('9–10. trigger and delay', () => {
  it('9. offers only the timings of the chosen trigger and describes it', async () => {
    await mount(backend(), { view: 'scenario', scenarioId: 1 });
    expect(Array.from($<HTMLSelectElement>('#wu-timing').options).map(o => o.value)).toEqual(['after']);
    expect($<HTMLSelectElement>('#wu-timing').disabled).toBe(true);
    choose('#wu-trigger', 'trial_period_end');
    expect(Array.from($<HTMLSelectElement>('#wu-timing').options).map(o => o.value)).toEqual(['before', 'after']);
    choose('#wu-timing', 'before');
    expect(text('#wu-trigger-text')).toBe('За 12 часов до окончания пробного периода');
    choose('#wu-trigger', 'paid_expired');
    expect($<HTMLSelectElement>('#wu-timing').value).toBe('after');
  });

  it('10. delay presets, units and range check', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 1 });
    buttonByText('3 дня').click();
    expect(text('#wu-trigger-text')).toBe('Через 3 дня после первого запуска бота');
    buttonByText('Сразу').click();
    expect(text('#wu-trigger-text')).toBe('Сразу после первого запуска бота');
    type('#wu-delay', '400');
    choose('#wu-unit', 'days');
    click('#wu-save');
    await flush();
    expect(text('#wu-delay-error')).toBe('Целое число от 0 до 366.');
    expect(server.calls.some(c => c.method === 'PATCH')).toBe(false);
    expect(triggerText('paid_expiry', 'before', 48, 'hours')).toBe('За 48 часов до окончания подписки');
  });
});

describe('11–12. HTML message and variables', () => {
  it('11. toolbar wraps the selection in Telegram HTML and counts N / 4096', async () => {
    await mount(backend(), { view: 'scenario', scenarioId: 2 });
    const message = $<HTMLTextAreaElement>('#wu-message');
    type('#wu-message', 'Привет мир');
    message.setSelectionRange(0, 6);
    buttonByText('B').click();
    expect(message.value).toBe('<b>Привет</b> мир');
    message.setSelectionRange(message.value.length - 3, message.value.length);
    buttonByText('Ссылка').click();
    expect(message.value).toBe('<b>Привет</b> <a href="https://">мир</a>');
    expect(message.value.slice(message.selectionStart, message.selectionEnd)).toBe('https://');
    for (const [label, open] of [['I', '<i>'], ['U', '<u>'], ['Code', '<code>'], ['Pre', '<pre>']] as const) {
      message.setSelectionRange(0, 0);
      buttonByText(label).click();
      expect(message.value.startsWith(open)).toBe(true);
    }
    expect(text('#wu-counter')).toMatch(/^\d+ \/ 4\s096$/);
  });

  it('12. inserts registry variables and disables the ones the trigger cannot use', async () => {
    await mount(backend(), { view: 'scenario', scenarioId: 1 });
    const chip = (name: string) => $<HTMLButtonElement>(`[data-variable="${name}"]`);
    expect(Array.from(document.querySelectorAll('[data-variable]')).map(n => n.getAttribute('data-variable')))
      .toEqual(['username', 'telegram_id', 'plan_name', 'trial_days', 'trial_duration', 'expire_date', 'discount_code', 'discount_percent']);
    expect(chip('plan_name').disabled).toBe(true);
    expect(chip('expire_date').disabled).toBe(true);
    expect(chip('discount_code').disabled).toBe(true);
    const message = $<HTMLTextAreaElement>('#wu-message');
    message.setSelectionRange(message.value.length, message.value.length);
    chip('username').click();
    expect(message.value.endsWith('{{username}}')).toBe(true);
    type('#wu-message', 'Тариф {{plan_name}}');
    expect(document.querySelector('.wu-variable-notes')?.textContent).toContain('{{plan_name}} недоступна для выбранного триггера.');
    choose('#wu-trigger', 'paid_expired');
    expect(chip('plan_name').disabled).toBe(false);
  });
});

describe('13–14. image upload and delete', () => {
  it('13. checks 2 MB and the type locally, uploads base64 and saves image_id', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 2 });
    pick(pngFile(2 * MB + 1));
    await flush();
    expect(text('#wu-image-error')).toBe('Файл больше 2 МБ.');
    pick(pngFile(10, 'a.gif', 'image/gif'));
    await flush();
    expect(text('#wu-image-error')).toBe('Поддерживаются только JPEG и PNG.');
    expect(server.calls.some(c => c.path.endsWith('/images'))).toBe(false);
    pick(pngFile(2048));
    await flush(12);
    const upload = lastCall(server, 'POST', '/images');
    expect(atob(String(upload?.body?.data))).toHaveLength(2048);
    expect($<HTMLImageElement>('.wu-image-thumb').getAttribute('src')).toBe('/admin/api/warmup/images/50');
    expect(document.querySelector('.wu-image-facts')?.textContent).toContain('promo.png');
    expect(document.querySelector('.wu-image-facts')?.textContent).toContain('image/png');
    expect(text('#wu-counter')).toMatch(/ \/ 1\s024 \(подпись к картинке\)$/);
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/2')?.body).toMatchObject({ image_id: 50 });
  });

  it('14. deletes an unsaved upload at once and the replaced stored picture after the save', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 2 });
    pick(pngFile(100));
    await flush(12);
    click('#wu-image-remove');
    await flush();
    expect(lastCall(server, 'DELETE', '/images/50')).toBeTruthy();
    expect(text('.wu-image-empty')).toContain('Картинки нет');
    pick(pngFile(100, 'b.png'));
    await flush(12);
    click('#wu-save');
    await flush();
    expect(server.scenarios[1].image_id).toBe(51);
    click('#wu-image-remove');
    expect(lastCall(server, 'DELETE', '/images/51')).toBeUndefined();
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/2')?.body).toMatchObject({ image_id: null });
    expect(lastCall(server, 'DELETE', '/images/51')).toBeTruthy();
    expect(server.images.has(51)).toBe(false);
  });
});

describe('15–18. buttons builder', () => {
  it('15. URL button: rows, validation and the hidden-without-URL warning', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 3 });
    expect(document.querySelectorAll('.wu-button-item')).toHaveLength(2);
    expect(document.querySelectorAll('.wu-kb-row')).toHaveLength(2);
    choose('#wu-b1-row', 'on');
    expect(document.querySelectorAll('.wu-kb-row')).toHaveLength(1);
    click('#wu-button-add');
    choose('#wu-b2-kind', 'url');
    type('#wu-b2-text', 'Сайт');
    expect($('.wu-button-item[data-index="2"] .wu-button-warn').textContent).toBe('Кнопка будет скрыта при отправке, пока URL не задан.');
    type('#wu-b2-value', 'ftp://example.com');
    expect($('.wu-button-item[data-index="2"] .wu-button-error').textContent).toContain('Недопустимый адрес кнопки');
    type('#wu-b2-value', 'https://example.com');
    expect($('.wu-button-item[data-index="2"] .wu-button-error').textContent).toBe('');
    // Move the new button to the top: the first button always starts a row.
    $<HTMLButtonElement>('.wu-button-item[data-index="2"] [aria-label="Кнопка 3: выше"]').click();
    $<HTMLButtonElement>('.wu-button-item[data-index="1"] [aria-label="Кнопка 2: выше"]').click();
    expect($<HTMLInputElement>('#wu-b0-text').value).toBe('Сайт');
    expect($<HTMLInputElement>('#wu-b0-row').disabled).toBe(true);
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/3')?.body?.buttons).toEqual([
      { text: 'Сайт', kind: 'url', value: 'https://example.com' },
      { text: 'Открыть подписку', kind: 'callback', value: 'menu_subscription' },
      { text: 'Поддержка', kind: 'callback', value: 'menu_support', same_row: true },
    ]);
    $<HTMLButtonElement>('.wu-button-item[data-index="0"] [aria-label="Кнопка 1: удалить"]').click();
    expect(document.querySelectorAll('.wu-button-item')).toHaveLength(2);
  });

  it('16. Mini App button accepts only https', async () => {
    await mount(backend(), { view: 'scenario', scenarioId: 2 });
    choose('#wu-b0-kind', 'web_app');
    type('#wu-b0-value', 'http://app.example');
    expect($('.wu-button-item[data-index="0"] .wu-button-error').textContent).toContain('Mini App — только https://');
    type('#wu-b0-value', 'https://app.example/miniapp');
    expect($('.wu-button-item[data-index="0"] .wu-button-error').textContent).toBe('');
  });

  it('17. callback buttons pick an action from the whitelist, never free text', async () => {
    await mount(backend(), { view: 'scenario', scenarioId: 2 });
    const action = $<HTMLSelectElement>('#wu-b0-value');
    expect(action.tagName).toBe('SELECT');
    expect(Array.from(action.options).map(o => o.value)).toEqual(['menu_subscription', 'buy_premium_list', 'menu_support', 'back_to_start']);
    expect(document.querySelector('input#wu-b0-value')).toBeNull();
    choose('#wu-b0-value', 'menu_support');
    expect($('.wu-kb-btn').textContent).toBe('Открыть подписку');
  });

  it('18. an action outside the whitelist is flagged and the server rejection is shown', async () => {
    const server = backend();
    server.scenarios[1].buttons = [cb('Старое', 'legacy_action')];
    await mount(server, { view: 'scenario', scenarioId: 2 });
    expect($<HTMLSelectElement>('#wu-b0-value').options[0].textContent).toBe('Недоступно: legacy_action');
    expect($('.wu-button-item .wu-button-error').textContent).toBe('Действие кнопки не из списка разрешённых.');
    click('#wu-save');
    await flush();
    expect(text('#wu-buttons-error')).toContain('Кнопка 1');
    expect(server.calls.some(c => c.method === 'PATCH')).toBe(false);
  });

  it('scenario 1 shows the trial page URL field with the hidden-button warning', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 1 });
    expect(text('label[for="wu-trial-url"]')).toBe('URL страницы пробника');
    expect(document.querySelector('#wu-trial-url')?.parentElement?.parentElement?.textContent).toContain('Кнопка будет скрыта при отправке, пока URL не задан.');
    expect($('#wu-trial-url-error').textContent).toBe('');
    type('#wu-trial-url', 'https://example.com/trial');
    expect($<HTMLInputElement>('#wu-b0-value').value).toBe('https://example.com/trial');
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/1')?.body?.buttons).toEqual([{ text: 'Активировать пробник', kind: 'url', value: 'https://example.com/trial' }]);
  });
});

describe('19. conditions builder', () => {
  it('edits only registry conditions and validates languages', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 7 });
    expect(Array.from(document.querySelectorAll('.wu-condition select, .wu-condition input')).map(n => n.id))
      .toEqual(['wu-cond-had_trial', 'wu-cond-has_subscription', 'wu-cond-has_active_paid', 'wu-cond-ever_paid', 'wu-cond-connected', 'wu-cond-languages']);
    expect(text('.wu-conditions + .field-error + .wu-human')).toBe('Отправлять всем, кого выбрал триггер.');
    choose('#wu-cond-has_active_paid', 'no');
    choose('#wu-cond-had_trial', 'yes');
    type('#wu-cond-languages', 'ru, english');
    expect(text('#wu-cond-languages-error')).toContain('Коды языков');
    type('#wu-cond-languages', 'ru, EN');
    expect(text('#wu-cond-languages-error')).toBe('');
    expect(document.body.textContent).toContain('Отправлять, если был пробник: да · активная платная: нет · язык: ru, en.');
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/7')?.body?.conditions).toEqual({ had_trial: true, has_active_paid: false, languages: ['ru', 'en'] });
  });
});

describe('20–21. discount and coupon TTL', () => {
  it('20. a percent discount needs {{discount_code}} and saves the coupon settings', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 10 });
    expect($('.wu-discount').hidden).toBe(true);
    choose('#wu-discount-percent', 'on');
    expect($('.wu-discount').hidden).toBe(false);
    expect(document.querySelector('.wu-variable-notes')?.textContent).toContain('{{discount_code}}');
    expect(text('#wu-coupon-example')).toBe('Пример кода: VPN-XXXXXXXX');
    expect(document.body.textContent).toContain('Купон пока не применяется автоматически при оплате.');
    expect($<HTMLInputElement>('#wu-personal').checked).toBe(true);
    type('#wu-percent', '15');
    type('#wu-template', 'back');
    buttonByText('Вставить {{discount_code}} в текст').click();
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/10')?.body).toMatchObject({ discount_percent: 15, coupon_template: 'BACK' });
    expect(String(lastCall(server, 'PATCH', '/scenarios/10')?.body?.message_html)).toContain('{{discount_code}}');
    expect(couponExample('')).toBe('XXXXXXXX');
  });

  it('21. coupon TTL in hours with words and bounds', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 10 });
    choose('#wu-discount-percent', 'on');
    type('#wu-message', 'Код {{discount_code}}');
    type('#wu-ttl', '72');
    expect(text('#wu-ttl-hint')).toBe('Действует 3 дня после отправки.');
    type('#wu-ttl', '0');
    click('#wu-save');
    await flush();
    expect(text('#wu-ttl-error')).toBe('Целое число от 1 до 8760.');
    type('#wu-ttl', '24');
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/10')?.body).toMatchObject({ coupon_ttl_hours: 24, discount_percent: 10 });
    expect([durationWords(12), durationWords(24), durationWords(72)]).toEqual(['12 часов', '1 день', '3 дня']);
  });

  it('own cooldown and daily cap', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 10 });
    expect(document.body.textContent).toContain('Общий из настроек (12 часов)');
    choose('#wu-cooldown-own', 'on');
    type('#wu-cooldown', '72');
    expect(text('#wu-cooldown-hint')).toContain('= 3 дня');
    type('#wu-cap', '50');
    click('#wu-save');
    await flush();
    expect(lastCall(server, 'PATCH', '/scenarios/10')?.body).toMatchObject({ cooldown_hours: 72, daily_cap: 50 });
  });
});

describe('22. preview', () => {
  it('renders the server preview like Telegram with the test context and writes nothing', async () => {
    const server = backend();
    await mount(server, { view: 'scenario', scenarioId: 3 });
    const auto = lastCall(server, 'POST', '/scenarios/3/preview');
    expect(auto?.body).toMatchObject({ context: { telegram_id: 1234567890, username: 'bob', plan_name: 'Gold', expire_date: '2026-12-31T20:59:00Z' }, enabled: true });
    expect($('.tg-text b').textContent).toBe('VPN всё ещё не подключён');
    expect(Array.from(document.querySelectorAll('.tg-kb-btn')).map(n => n.textContent)).toEqual(['Открыть подписку', 'Поддержка']);
    choose('#wu-discount-percent', 'on');
    type('#wu-message', '<b>Скидка {{discount_percent}}%</b> по коду {{discount_code}} <script>x</script>');
    expect(document.querySelector('.wu-stale')).not.toBeNull();
    click('#wu-preview');
    await flush();
    expect(text('.tg-text')).toBe('Скидка 10% по коду TEST-7K3P9QXD x');
    expect(document.querySelector('.tg-text script')).toBeNull();
    expect(text('.wu-preview-facts')).toContain('Код скидки: TEST-7K3P9QXD (тестовый, не сохраняется)');
    expect(server.scenarios[2].version).toBe(1);
    expect(server.deliveries).toHaveLength(5);
  });

  it('lists preview problems and the hidden button', async () => {
    await mount(backend(), { view: 'scenario', scenarioId: 1 });
    expect(text('.wu-problems')).toContain('Кнопка без URL будет скрыта при отправке.');
    expect(text('.wu-preview-facts')).toContain('Скрыто кнопок без URL: 1');
    expect(document.querySelectorAll('.tg-kb-btn')).toHaveLength(0);
  });
});

describe('23–25. save, version conflict, duplicate reminder', () => {
  it('23. an uncertain failure retries with the same request key', async () => {
    const server = backend({ patchFailure: new ApiError('service_unavailable', 503) });
    await mount(server, { view: 'scenario', scenarioId: 2 });
    type('#wu-name', 'Повтор');
    click('#wu-save');
    await flush();
    expect(text('.editor-status')).toContain('Повторите действие');
    click('#wu-save');
    await flush();
    const patches = server.calls.filter(c => c.method === 'PATCH');
    expect(patches).toHaveLength(2);
    expect(patches[0].body?.request_key).toBe(patches[1].body?.request_key);
    expect(text('.editor-status')).toBe('Сценарий сохранён.');
    const keys = new RequestKeys(() => `k${Math.random()}`);
    const a = keys.key('s', 'p');
    keys.settle('s', new ApiError('version_conflict', 409));
    expect(keys.key('s', 'p')).not.toBe(a);
    expect([isUncertain(new ApiError('network')), isUncertain(new ApiError('version_conflict', 409))]).toEqual([true, false]);
  });

  it('24. a version conflict never overwrites the other session', async () => {
    const server = backend({ staleScenario: 4 });
    const { host } = await mount(server, { view: 'scenario', scenarioId: 4 });
    type('#wu-name', 'Моя правка');
    click('#wu-save');
    await flush();
    expect(text('.editor-status')).toContain(SCENARIO_CONFLICT_TEXT);
    expect(SCENARIO_CONFLICT_TEXT).toBe('Сценарий был изменён в другой сессии. Обновите данные.');
    expect(server.scenarios[3].name).toBe('Пробник заканчивается');
    expect($<HTMLInputElement>('#wu-name').value).toBe('Моя правка');
    expect(host.unsaved()).toBe(true);
    buttonByText('Обновить данные').click();
    await flush();
    expect(host.confirm).toHaveBeenCalled();
    expect($<HTMLInputElement>('#wu-name').value).toBe('Пробник заканчивается');
  });

  it('25. scenario 8 cannot be enabled together with the expiry reminder', async () => {
    const server = backend();
    await mount(server, { view: 'overview' });
    click('#wu-toggle-8');
    await flush();
    expect($('.wu-card[data-scenario="8"] .notice').textContent).toBe(REMINDER_CONFLICT_TEXT);
    expect(REMINDER_CONFLICT_TEXT).toBe('Этот сценарий конфликтует с существующим напоминанием о подписке и не может быть включён одновременно с ним.');
    expect(server.scenarios[7].enabled).toBe(false);
    await mount(server, { view: 'scenario', scenarioId: 8 });
    click('#wu-enable');
    await flush();
    expect(text('.editor-status')).toBe(REMINDER_CONFLICT_TEXT);
  });
});

describe('26. settings', () => {
  it('saves with PUT /settings and explains the catch-up window', async () => {
    const server = backend();
    const { host } = await mount(server, { view: 'settings' });
    expect(text('#wu-set-catchup_hours-hint')).toContain('6 часов. События старше этого окна не догоняются после включения.');
    choose('#wu-set-enabled', 'on');
    type('#wu-set-max_per_day', '3');
    expect(host.unsaved()).toBe(true);
    type('#wu-set-catchup_hours', '100');
    click('#wu-settings-save');
    await flush();
    expect(text('#wu-set-catchup_hours-error')).toBe('Целое число от 1 до 72.');
    type('#wu-set-catchup_hours', '24');
    click('#wu-settings-save');
    await flush();
    expect(lastCall(server, 'PUT', '/settings')?.body).toEqual({ request_key: expect.stringMatching(/^ui-/), version: 1, enabled: true, max_per_day: 3, cooldown_hours: 12, catchup_hours: 24 });
    expect(text('.editor-status')).toBe('Настройки сохранены.');
    expect(server.settings()).toMatchObject({ enabled: true, version: 2 });
    expect(host.unsaved()).toBe(false);
  });

  it('shows the settings conflict and reloads them with GET /settings', async () => {
    const server = backend({ staleSettings: true });
    await mount(server, { view: 'settings' });
    type('#wu-set-max_per_day', '5');
    click('#wu-settings-save');
    await flush();
    expect(text('.editor-status')).toContain('Настройки прогрева были изменены в другой сессии. Обновите данные.');
    buttonByText('Обновить данные').click();
    await flush();
    expect(lastCall(server, 'GET', '/warmup/settings')).toBeTruthy();
    expect($<HTMLInputElement>('#wu-set-max_per_day').value).toBe('2');
  });

  it('switches the whole warm-up on from the overview', async () => {
    const server = backend();
    await mount(server, { view: 'overview' });
    choose('#wu-global', 'on');
    await flush();
    expect(lastCall(server, 'PUT', '/settings')?.body).toMatchObject({ enabled: true, version: 1, catchup_hours: 6 });
    expect($<HTMLInputElement>('#wu-global').checked).toBe(true);
  });
});

describe('27–29. deliveries, pagination and filters', () => {
  it('27. shows a page of the history with statuses, errors and coupons', async () => {
    const server = backend();
    await mount(server, { view: 'deliveries' });
    expect(lastCall(server, 'GET', '/deliveries')?.path).toBe('/admin/api/warmup/deliveries');
    expect(lastCall(server, 'GET', '/warmup/scenarios')).toBeTruthy();
    expect(Array.from(document.querySelectorAll('.wu-table th')).map(n => n.textContent)).toEqual(['Дата', 'Пользователь', 'Сценарий', 'Категория', 'Статус', 'Ошибка', 'Купон']);
    const rows = document.querySelectorAll('.wu-table tbody tr');
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('@user0');
    expect(rows[0].textContent).toContain('Отправлено');
    expect(rows[0].textContent).toContain('Пробный период');
    expect(rows[0].textContent).toContain('VPN-7K3P9QXD');
    expect(rows[1].textContent).toContain('Повтор');
    expect(text('.toolbar-meta')).toBe('Найдено: 5');
  });

  it('28. pages with offset and never loads the whole history', async () => {
    const server = backend();
    await mount(server, { view: 'deliveries' });
    expect(text('.pager-page')).toBe('Страница 1 из 3');
    $<HTMLButtonElement>('[data-pager="next"]').click();
    await flush();
    expect(lastCall(server, 'GET', '/deliveries')?.path).toBe('/admin/api/warmup/deliveries?offset=2');
    expect(text('.pager-page')).toBe('Страница 2 из 3');
    expect(document.querySelector('.wu-table tbody')?.textContent).toContain('Ошибка');
    expect(document.querySelector('.wu-table tbody')?.textContent).toContain('chat not found');
  });

  it('29. filters by status, user, scenario, category, dates and coupon', async () => {
    const server = backend();
    await mount(server, { view: 'deliveries' });
    type('#wu-f-telegram', 'abc');
    $<HTMLFormElement>('.wu-filters').requestSubmit();
    await flush();
    expect(document.body.textContent).toContain('Telegram ID — положительное целое число.');
    choose('#wu-f-status', 'failed');
    type('#wu-f-telegram', '1002');
    choose('#wu-f-scenario', '3');
    choose('#wu-f-category', 'trial');
    $<HTMLInputElement>('#wu-f-from').value = '2026-10-01';
    $<HTMLInputElement>('#wu-f-to').value = '2026-10-02';
    type('#wu-f-coupon', ' VPN ');
    $<HTMLFormElement>('.wu-filters').requestSubmit();
    await flush();
    const url = new URL(`http://x${lastCall(server, 'GET', '/deliveries')?.path ?? ''}`);
    expect(Object.fromEntries(url.searchParams)).toMatchObject({ scenario_id: '3', category: 'trial', status: 'failed', telegram_id: '1002', coupon: 'VPN' });
    expect(url.searchParams.get('from')).toMatch(/^2026-(09-30|10-01)T/);
    expect(url.searchParams.get('to')).toMatch(/^2026-10-0[23]T/);
    buttonByText('Сбросить').click();
    await flush();
    expect(lastCall(server, 'GET', '/deliveries')?.path).toBe('/admin/api/warmup/deliveries');
  });

  it('shows an empty state for filters without results', async () => {
    const server = backend({ deliveries: 0 });
    await mount(server, { view: 'deliveries' });
    expect(text('.wu-empty')).toContain('Отправок пока не было');
  });
});

describe('30. coupons', () => {
  it('lists coupons with empty redeemed/order and filters by status', async () => {
    const server = backend();
    await mount(server, { view: 'coupons' });
    expect(Array.from(document.querySelectorAll('.wu-table th')).map(n => n.textContent))
      .toEqual(['Код', 'Telegram ID', 'Скидка', 'Создан', 'Истекает', 'Статус', 'Использован', 'Заказ']);
    const first = $('.wu-table tbody tr');
    const cells = Array.from(first.querySelectorAll('td')).map(td => td.textContent);
    expect(cells[0]).toContain('VPN-CODE0000');
    expect(cells.slice(1, 3)).toEqual(['2000', '15%']);
    expect(cells[5]).toBe('Действует');
    expect(cells.slice(6)).toEqual(['—', '—']);
    expect(document.body.textContent).toContain('Купон пока не применяется автоматически при оплате');
    choose('#wu-c-status', 'expired');
    $<HTMLFormElement>('.wu-filters').requestSubmit();
    await flush();
    expect(lastCall(server, 'GET', '/coupons')?.path).toBe('/admin/api/warmup/coupons?status=expired');
    expect(Array.from(document.querySelectorAll('.wu-table tbody tr td:nth-child(6)')).map(n => n.textContent)).toEqual(['Истёк']);
  });
});

describe('wire contract', () => {
  it('parses the overview of the seed and builds the create payload from a form', () => {
    const server = backend();
    const overview = parseOverview({
      settings: server.settings(), categories: [], scenarios: server.scenarios, registry: REGISTRY,
      summary: { scenarios: 14, enabled_scenarios: 11, last_24h: {}, active_coupons: 0, contacts: 0, blocked_contacts: 0 },
    });
    expect(overview.scenarios).toHaveLength(14);
    const { values } = formValues(formFromScenario(overview.scenarios[0]), overview.registry);
    expect(values).not.toBeNull();
    if (!values) return;
    expect(scenarioInput(values)).toMatchObject({ enabled: false, trigger_type: 'bot_started', image_id: null, cooldown_hours: null });
    expect(Object.keys(scenarioInput(values)).sort()).toEqual([...PATCH_FIELDS].sort());
    expect(suggestKey(['custom_1'], 1)).toBe('custom_1_2');
  });

  it('reads a button rejection as the real server sends it (index in detail)', async () => {
    // service.normalizeWarmupButtons → WarmupFieldError{Field: "buttons", Detail: "buttons[1]"}.
    const { AdminApi } = await import('../admin/src/api');
    const transport = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ error: 'invalid_button', field: 'buttons', reason: 'button_url', detail: 'buttons[1]' }), { status: 400 }));
    const api = new AdminApi(transport);
    const error = await api.request('PATCH', '/admin/api/warmup/scenarios/1', '{}').catch((e: unknown) => e);
    expect(error).toMatchObject({ code: 'invalid_button', field: 'buttons', reason: 'button_url', detail: 'buttons[1]' });
    expect(warmupErrorText(error)).toBe('Кнопки: Недопустимый адрес кнопки (Mini App — только https://).');
  });
});
