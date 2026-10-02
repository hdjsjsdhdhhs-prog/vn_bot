import { test, expect } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

// Trial editor (#/tariffs/trial) of the browser admin panel against a mock
// backend that follows the real contract:
//   routes    internal/web/admin_trial_api.go (GET/PUT /admin/api/trial,
//             POST /preview and /builder-copy; unknown fields are rejected;
//             400 invalid_trial {field, reason}, 409 version_conflict |
//             builder_version_conflict | builder_shared | request_key_conflict)
//   rules     database.UpdateTrial / CopyTrialBuilder / evaluateTrialDraft
//             (settings version, builder version, a shared builder's
//             composition is never edited, an enabled trial is never saved
//             onto a configuration that cannot issue it)
//   replay    runAdminConfigMutation (same key + same payload replays)
// No real provider address or credential is used.

const CSRF = 'c'.repeat(43);
const KEY_PATTERN = /^ui-[0-9a-f]{32}$/;
const T0 = '2026-09-20T08:00:00Z';

interface Entry {
  id: number; source_id: number; fingerprint: string; original_name: string; protocol: string; country_code: string;
  upstream_position: number; present: boolean; last_seen_at: string;
}
interface Rule { kind: 'country' | 'node'; source_id: number; country_code: string; fingerprint: string; original_name: string }
interface Item extends Rule { id: number; custom_name: string | null; description: string; position: number; enabled: boolean }
interface Builder {
  id: number; name: string; description: string; enabled: boolean; profile_title: string; support_url: string; announce: string;
  version: number; sources: { source_id: number; position: number }[]; items: Item[];
}
interface Composition { builder_version: number; mode: 'all' | 'selected'; source_ids: number[]; rules: Rule[] }
interface Draft {
  enabled: boolean; duration_hours: number; rate_limit_per_hour: number; title: string; description: string; features: string[];
  badge: string; builder_id: number | null; composition: Composition | null;
}
interface Settings {
  enabled: boolean; duration_hours: number; rate_limit_per_hour: number; title: string; description: string; features: string[];
  badge: string; version: number; stored: boolean; updated_at: string | null;
}
interface Problem { field: string; code: string; severity: 'invalid' | 'unavailable' }
interface Call { method: string; path: string; body: Record<string, unknown> }

interface Options {
  /** Builder of the trial plan when the page loads (7 trial-only, 8 shared, null legacy). */
  trialBuilder?: number | null;
  /** false: nothing stored yet, the environment values apply. */
  stored?: boolean;
  /** GET /admin/api/trial answered with 500 this many times. */
  viewFailures?: number;
  /** Committed mutations whose response is lost. */
  lostResponses?: number;
}

const DRAFT_FIELDS = ['enabled', 'duration_hours', 'rate_limit_per_hour', 'title', 'description', 'features', 'badge', 'builder_id', 'composition'];
const SAVE_FIELDS = ['request_key', 'version', ...DRAFT_FIELDS];
const COPY_FIELDS = ['request_key', 'builder_id'];
const DEFAULTS = { duration_hours: 3, rate_limit_per_hour: 3 };

const entry = (id: number, source_id: number, fingerprint: string, original_name: string, country_code: string, protocol = 'vless'): Entry =>
  ({ id, source_id, fingerprint, original_name, protocol, country_code, upstream_position: id, present: true, last_seen_at: T0 });

const country = (source_id: number, code: string): Rule => ({ kind: 'country', source_id, country_code: code, fingerprint: '', original_name: '' });
const node = (source_id: number, fingerprint: string, original_name: string): Rule => ({ kind: 'node', source_id, country_code: '', fingerprint, original_name });

/** database.EntryCountry: catalogue code, else the flag emoji in the name. */
function entryCountry(e: Entry): string {
  if (e.country_code) return e.country_code.toUpperCase();
  const points = Array.from(e.original_name).map(ch => ch.codePointAt(0) ?? 0);
  for (let i = 0; i + 1 < points.length; i++) {
    const [a, b] = [points[i], points[i + 1]];
    if (a >= 0x1F1E6 && a <= 0x1F1FF && b >= 0x1F1E6 && b <= 0x1F1FF) return String.fromCharCode(65 + a - 0x1F1E6, 65 + b - 0x1F1E6);
  }
  return '';
}

async function backend(page: Page, options: Options = {}) {
  let clock = Date.parse(T0);
  const now = () => new Date(clock += 60_000).toISOString();
  const sources = [
    { id: 1, name: 'Provider A', enabled: true },
    { id: 2, name: 'Provider B', enabled: true },
  ];
  const entries: Entry[] = [
    entry(1, 1, 'a-de1', '🇩🇪 Berlin', 'DE'), entry(2, 1, 'a-de2', '🇩🇪 Munich', 'DE', 'trojan'),
    entry(3, 1, 'a-nl', '🇳🇱 Amsterdam', 'NL'), entry(4, 1, 'a-x', 'Mystery', '', 'ss'),
    entry(5, 2, 'b-us', '🇺🇸 New York', 'US'), entry(6, 2, 'b-jp', '🇯🇵 Tokyo', 'JP'),
  ];
  let nextItemId = 100;
  const item = (rule: Rule, position: number, enabled = true): Item =>
    ({ ...rule, id: nextItemId++, custom_name: null, description: '', position, enabled });
  const builders: Builder[] = [
    { id: 7, name: 'Пробный', description: '', enabled: true, profile_title: 'DictatorVPN Trial', support_url: 'https://t.me/support',
      announce: '', version: 1, sources: [{ source_id: 1, position: 0 }], items: [] },
    { id: 8, name: 'Основной', description: '', enabled: true, profile_title: 'DictatorVPN', support_url: '', announce: '', version: 3,
      sources: [{ source_id: 1, position: 0 }, { source_id: 2, position: 1 }],
      items: [item(country(2, 'US'), 0), item(node(1, 'a-nl', '🇳🇱 Amsterdam'), 1)] },
    { id: 9, name: 'Отключённый', description: '', enabled: false, profile_title: '', support_url: '', announce: '', version: 1,
      sources: [{ source_id: 2, position: 0 }], items: [] },
  ];
  let nextBuilderId = 10;
  // plans.subscription_builder_id; "trial" is the system plan of the trial.
  const plans: { id: number; name: string; builder: number | null }[] = [
    { id: 1, name: 'trial', builder: options.trialBuilder === undefined ? 7 : options.trialBuilder },
    { id: 3, name: 'Стандарт', builder: 8 },
  ];
  const trialPlan = plans[0];
  let settings: Settings = options.stored === false
    ? { enabled: true, ...DEFAULTS, title: '', description: '', features: [], badge: '', version: 0, stored: false, updated_at: null }
    : { enabled: true, duration_hours: 72, rate_limit_per_hour: 3, title: 'Пробный доступ', description: 'Три дня бесплатно',
      features: ['Без карты'], badge: 'Бесплатно', version: 1, stored: true, updated_at: T0 };
  const history: { id: number; actor: string; action: string; target_type: string; created_at: string; success: boolean }[] = [];
  let auditId = 0;
  let viewFailures = options.viewFailures ?? 0;
  let lostResponses = options.lostResponses ?? 0;
  const calls: Call[] = [];
  const problems: string[] = [];
  const recorded = new Map<string, { hash: string; status: number; payload: Record<string, unknown> }>();

  const builderOf = (id: number | null) => builders.find(b => b.id === id);
  const presentOf = (id: number) => entries.filter(x => x.source_id === id && x.present).sort((a, b) => a.upstream_position - b.upstream_position);
  const usageOf = (b: Builder) => {
    const users = plans.filter(p => p.builder === b.id);
    return { plans: users.filter(p => p !== trialPlan).map(p => p.name), subscriptions: 0, trial_plan: users.includes(trialPlan) };
  };
  const shared = (b: Builder) => usageOf(b).plans.length > 0;
  const ref = (b: Builder) => ({ id: b.id, name: b.name, enabled: b.enabled, profile_title: b.profile_title, support_url: b.support_url, announce: b.announce, version: b.version });
  /** database.compositionOf: enabled rules in position order. */
  const compositionOf = (b: Builder): Composition => ({
    builder_version: b.version, mode: b.items.length ? 'selected' : 'all',
    source_ids: [...b.sources].sort((x, y) => x.position - y.position).map(s => s.source_id),
    rules: [...b.items].filter(it => it.enabled).sort((x, y) => x.position - y.position)
      .map(it => ({ kind: it.kind, source_id: it.source_id, country_code: it.country_code, fingerprint: it.fingerprint, original_name: it.original_name })),
  });

  /** database.previewBuilderModel for sources + rules (no rules = every present entry). */
  const resolve = (sourceIds: number[], rules: Rule[]) => {
    const served: Entry[] = [];
    const seen = new Set<number>();
    const add = (x: Entry) => { if (!seen.has(x.id)) { seen.add(x.id); served.push(x); } };
    const usable = (id: number) => sources.find(s => s.id === id)?.enabled ?? false;
    if (!rules.length) {
      for (const id of sourceIds) if (usable(id)) presentOf(id).forEach(add);
    }
    for (const r of rules) {
      if (!sourceIds.includes(r.source_id) || !usable(r.source_id)) continue;
      const live = presentOf(r.source_id);
      if (r.kind === 'country') live.filter(x => entryCountry(x) === r.country_code).forEach(add);
      else live.filter(x => x.fingerprint === r.fingerprint).forEach(add);
    }
    return served;
  };

  /** database.evaluateTrialDraft (the parts the editor relies on). */
  const evaluate = (d: Draft) => {
    const list: Problem[] = [];
    const add = (field: string, code: string, severity: Problem['severity']) => list.push({ field, code, severity });
    if (!Number.isInteger(d.duration_hours) || d.duration_hours < 1 || d.duration_hours > 168) add('duration_hours', 'out_of_range', 'invalid');
    if (!Number.isInteger(d.rate_limit_per_hour) || d.rate_limit_per_hour < 1 || d.rate_limit_per_hour > 100) add('rate_limit_per_hour', 'out_of_range', 'invalid');
    const base = {
      enabled: d.enabled, duration_hours: d.duration_hours, previewed_at: now(), legacy_nodes: 1, sources: [] as unknown[], warnings: [] as string[],
      missing: 0, conflicts: 0, expires_at: new Date(clock + d.duration_hours * 3_600_000).toISOString(),
    };
    const done = (rest: Record<string, unknown>) => ({ ...base, problems: list, issuable: list.length === 0, ...rest });
    if (d.builder_id === null) {
      if (d.composition) add('composition', 'builder_required', 'invalid');
      return done({ serve: 'legacy', builder: null, mode: '', total: 0, countries: [], items: [] });
    }
    const b = builderOf(d.builder_id);
    if (!b) {
      add('builder_id', 'builder_not_found', 'invalid');
      return done({ serve: 'builder', builder: null, mode: '', total: 0, countries: [], items: [] });
    }
    if (!b.enabled) add('builder_id', 'builder_disabled', 'unavailable');
    let model = compositionOf(b);
    if (d.composition) {
      if (shared(b)) {
        add('composition', 'builder_shared', 'invalid');
        return done({ serve: 'builder', builder: ref(b), mode: '', total: 0, countries: [], items: [] });
      }
      const c = d.composition;
      if (c.mode === 'selected' && !c.rules.length) add('composition.rules', 'required', 'invalid');
      if (c.mode === 'all' && c.rules.length) add('composition.rules', 'rules_in_all_mode', 'invalid');
      if (!c.source_ids.length) add('composition.source_ids', 'required', 'invalid');
      if (c.rules.some(r => !c.source_ids.includes(r.source_id))) add('composition.rules', 'rule_source_not_linked', 'invalid');
      model = c;
    }
    const served = resolve(model.source_ids, model.rules);
    const counts = new Map<string, number>();
    for (const x of served) counts.set(entryCountry(x), (counts.get(entryCountry(x)) ?? 0) + 1);
    if (!model.source_ids.length) add('composition.source_ids', 'no_sources', 'unavailable');
    else if (!served.length) add('composition', 'empty_result', 'unavailable');
    return done({
      serve: 'builder', builder: ref(b), mode: model.rules.length ? 'selected' : 'all', total: served.length,
      countries: [...counts].map(([code, count]) => ({ code, count })),
      items: served.map(x => ({ kind: 'source', source_id: x.source_id, display_name: x.original_name, status: 'matched', entry: x })),
      sources: model.source_ids.map(id => ({ id, name: sources.find(s => s.id === id)?.name ?? '', enabled: true, usable: true })),
    });
  };

  const summary = (b: Builder) => {
    const c = compositionOf(b);
    const served = resolve(c.source_ids, c.rules);
    return {
      ...ref(b), source_ids: c.source_ids, rules: b.items.filter(it => it.enabled).length, disabled_rules: b.items.filter(it => !it.enabled).length,
      total: served.length, countries: new Set(served.map(entryCountry)).size, usage: usageOf(b), shared: shared(b),
      assigned_to_trial: trialPlan.builder === b.id,
    };
  };

  /** database.GetTrialAdminView. */
  const view = () => {
    const b = builderOf(trialPlan.builder);
    return {
      settings: { ...settings, features: [...settings.features] }, defaults: DEFAULTS, plan_id: trialPlan.id, builder_id: trialPlan.builder,
      composition: b ? compositionOf(b) : null,
      builders: [...builders].sort((x, y) => x.name.localeCompare(y.name)).map(summary),
      preview: evaluate({ ...settings, builder_id: trialPlan.builder, composition: null }),
      active_trials: 4, history: [...history].reverse(),
    };
  };

  const sourceView = (s: typeof sources[number]) => {
    const live = presentOf(s.id);
    const codes = new Map<string, number>();
    for (const x of live) if (x.country_code) codes.set(x.country_code, (codes.get(x.country_code) ?? 0) + 1);
    return {
      ...s, type: 'auto', description: '', last_sync_at: T0, last_sync_status: 'ok', last_sync_error: '', created_at: T0, updated_at: T0,
      catalogue: {
        entries: live.length, countries: codes.size, no_country: live.filter(x => !x.country_code).length, absent: 0,
        by_country: [...codes].map(([code, count]) => ({ code, count })), protocols: {},
      },
    };
  };

  const audit = (action: string) => {
    const row = { id: ++auditId, actor: 'admin', action, target_type: 'trial', created_at: now(), success: true };
    history.push(row);
    return row;
  };
  // service.TrialService.outcome reloads the view after the commit: the new
  // audit row is part of the returned history.
  const outcome = (target_id: number, action: string) => {
    const row = audit(action);
    return { trial: view(), target_id, replayed: false, audit: row };
  };
  const fieldError = (p: Problem): [number, Record<string, unknown>] => p.code === 'builder_shared'
    ? [409, { error: 'builder_shared', field: p.field, reason: p.code }]
    : [400, { error: 'invalid_trial', field: p.field, reason: p.code }];

  /** One mutation; returns [status, payload]. */
  const mutate = (path: string, body: Record<string, unknown>): [number, Record<string, unknown>] => {
    if (path === '/admin/api/trial/builder-copy') {
      const src = builderOf(Number(body.builder_id));
      if (!src) return [400, { error: 'invalid_trial', field: 'builder_id', reason: 'builder_not_found' }];
      const copy: Builder = {
        ...src, id: nextBuilderId++, name: `${src.name} · пробная`, version: 1,
        sources: src.sources.map(s => ({ ...s })), items: src.items.map(it => ({ ...it, id: nextItemId++ })),
      };
      builders.push(copy);
      trialPlan.builder = copy.id;
      return [200, outcome(copy.id, 'trial_builder_copied')];
    }
    const d = body as unknown as Draft & { version: number };
    if (d.version !== settings.version) return [409, { error: 'version_conflict' }];
    const result = evaluate(d);
    const invalid = result.problems.find(p => p.severity === 'invalid');
    if (invalid) return fieldError(invalid);
    const unavailable = result.problems.find(p => p.severity === 'unavailable');
    if (d.enabled && unavailable) return fieldError(unavailable);
    const b = builderOf(d.builder_id);
    if (d.composition && b && b.version !== d.composition.builder_version) return [409, { error: 'builder_version_conflict' }];
    settings = {
      enabled: d.enabled, duration_hours: d.duration_hours, rate_limit_per_hour: d.rate_limit_per_hour, title: d.title,
      description: d.description, features: [...d.features], badge: d.badge, version: settings.version + 1, stored: true, updated_at: now(),
    };
    trialPlan.builder = d.builder_id;
    if (d.composition && b) {
      b.sources = d.composition.source_ids.map((source_id, position) => ({ source_id, position }));
      b.items = d.composition.rules.map((rule, position) => item(rule, position));
      b.version++;
    }
    return [200, outcome(1, 'trial_updated')];
  };

  const json = (route: Route, status: number, payload: unknown) =>
    route.fulfill({ status, contentType: 'application/json', headers: { 'Cache-Control': 'no-store' }, body: JSON.stringify(payload) });

  await page.route('**/admin/**', async route => {
    const request = route.request();
    const { pathname, searchParams } = new URL(request.url());
    const method = request.method();

    if ((pathname === '/admin/login' || pathname === '/admin/session') && method === 'GET') {
      return json(route, 200, { authenticated: true, csrf_token: CSRF });
    }
    if (method === 'GET') {
      if (pathname === '/admin/api/trial') {
        if (viewFailures > 0) { viewFailures--; return json(route, 500, { error: 'internal' }); }
        return json(route, 200, view());
      }
      if (pathname === '/admin/api/tariffs') return json(route, 200, { tariffs: [] });
      if (pathname === '/admin/api/sources') return json(route, 200, { sources: sources.map(sourceView) });
      const list = /^\/admin\/api\/sources\/(\d+)\/entries$/.exec(pathname);
      if (list) {
        const id = Number(list[1]);
        if (searchParams.get('country') !== '*') problems.push(`entries country ${searchParams.get('country')}`);
        return json(route, 200, { source_id: id, entries: presentOf(id) });
      }
      const one = /^\/admin\/api\/builders\/(\d+)$/.exec(pathname);
      if (one) {
        const b = builderOf(Number(one[1]));
        return b ? json(route, 200, { ...b, created_at: T0, updated_at: T0 }) : json(route, 404, { error: 'not_found' });
      }
      problems.push(`unexpected GET ${pathname}`);
      return json(route, 404, { error: 'not_found' });
    }

    let body: Record<string, unknown> = {};
    try { body = JSON.parse(request.postData() ?? '{}') as Record<string, unknown>; } catch { problems.push(`bad json ${pathname}`); }
    calls.push({ method, path: pathname, body });
    const headers = request.headers();
    if (headers['x-csrf-token'] !== CSRF) { problems.push(`csrf ${pathname}`); return json(route, 403, { error: 'forbidden' }); }
    if (headers['content-type']?.split(';')[0].trim() !== 'application/json') { problems.push(`content type ${pathname}`); return json(route, 415, { error: 'unsupported_media_type' }); }

    const route_ = `${method} ${pathname}`;
    const allowed = route_ === 'POST /admin/api/trial/preview' ? DRAFT_FIELDS
      : route_ === 'PUT /admin/api/trial' ? SAVE_FIELDS
        : route_ === 'POST /admin/api/trial/builder-copy' ? COPY_FIELDS : null;
    if (!allowed) { problems.push(`unexpected ${route_}`); return json(route, 404, { error: 'not_found' }); }
    // decodeAdminJSON: unknown fields are rejected, never ignored.
    const unknown = Object.keys(body).filter(key => !allowed.includes(key));
    if (unknown.length) { problems.push(`unknown fields ${route_}: ${unknown.join(',')}`); return json(route, 400, { error: 'invalid_request' }); }

    if (pathname === '/admin/api/trial/preview') return json(route, 200, evaluate(body as unknown as Draft));

    if (typeof body.request_key !== 'string' || !KEY_PATTERN.test(body.request_key)) {
      problems.push(`request_key ${route_}`);
      return json(route, 400, { error: 'invalid_request' });
    }
    // Keeps the request in flight long enough for double clicks to matter.
    await new Promise(resolve => setTimeout(resolve, 120));
    const key = body.request_key;
    const rest = { ...body };
    delete rest.request_key;
    const hash = `${route_} ${JSON.stringify(rest)}`;
    const previous = recorded.get(key);
    let status: number;
    let payload: Record<string, unknown>;
    if (previous) {
      if (previous.hash !== hash) return json(route, 409, { error: 'request_key_conflict' });
      status = previous.status;
      payload = { ...previous.payload, trial: view(), replayed: true };
    } else {
      [status, payload] = mutate(pathname, body);
      if (status < 300) recorded.set(key, { hash, status, payload });
    }
    if (status < 300 && lostResponses > 0) {
      lostResponses--;
      return route.abort('failed');
    }
    return json(route, status, payload);
  });

  return {
    problems: () => problems,
    mutations: (path?: string) => calls.filter(call => call.method !== 'GET' && call.path !== '/admin/api/trial/preview' && (!path || call.path === path)),
    previews: () => calls.filter(call => call.path === '/admin/api/trial/preview'),
    settings: () => settings,
    builder: (id: number) => builderOf(id)!,
    plan: (name: string) => plans.find(p => p.name === name)!,
    history: () => history,
    /** Another admin saved the trial after the page loaded. */
    touchSettings: (change: Partial<Settings>) => { settings = { ...settings, ...change, version: settings.version + 1, updated_at: now() }; },
    /** Another admin edited a builder after the page loaded. */
    touchBuilder: (id: number) => { builderOf(id)!.version++; },
    /** A paid plan starts using a builder after the page loaded. */
    assign: (planName: string, builderId: number) => { plans.push({ id: 50 + plans.length, name: planName, builder: builderId }); },
  };
}

const heading = (page: Page, name: string) => page.getByRole('heading', { level: 1, name });
const editor = (page: Page) => page.getByRole('form', { name: 'Параметры пробной подписки' });
const preview = (page: Page) => page.getByRole('region', { name: 'Предпросмотр' });
const saveButton = (page: Page) => editor(page).getByRole('button', { name: 'Сохранить', exact: true });
const statusText = (page: Page) => editor(page).locator('.editor-status');
const countryBox = (page: Page, code: string) => page.getByRole('checkbox', { name: new RegExp(`· ${code}$`) });
const countryRow = (page: Page, code: string) => page.locator('.trial-country').filter({ has: countryBox(page, code) });

async function openTrial(page: Page) {
  await page.goto('/#/tariffs/trial');
  await expect(heading(page, 'Пробная подписка')).toBeVisible();
  await expect(editor(page)).toBeVisible();
}

test('route: the Tariffs page links to #/tariffs/trial and back', async ({ page }) => {
  const api = await backend(page);
  await page.goto('/#/tariffs');
  await expect(heading(page, 'Тарифы')).toBeVisible();
  const entryPanel = page.getByRole('region', { name: 'Пробная подписка' });
  await expect(entryPanel).toContainText('Платные тарифы и их построители не меняются.');
  await entryPanel.getByRole('link', { name: 'Настроить пробную подписку' }).click();
  await expect(page).toHaveURL(/#\/tariffs\/trial$/);
  await expect(heading(page, 'Пробная подписка')).toBeVisible();
  await expect(page).toHaveTitle('Пробная подписка · DictatorVPN Admin');
  await expect(page.getByRole('link', { name: 'Тарифы' })).toHaveAttribute('aria-current', 'page');
  await expect(editor(page)).toBeVisible();

  // Leaving with unsaved edits asks first.
  await page.getByLabel('Название', { exact: true }).fill('Другое');
  page.once('dialog', dialog => void dialog.dismiss());
  await page.getByRole('link', { name: 'Назад к тарифам' }).click();
  await expect(page).toHaveURL(/#\/tariffs\/trial$/);
  page.once('dialog', dialog => void dialog.accept());
  await page.getByRole('link', { name: 'Назад к тарифам' }).click();
  await expect(heading(page, 'Тарифы')).toBeVisible();
  expect(api.mutations()).toHaveLength(0);
  expect(api.problems()).toEqual([]);
});

test('load: stored settings, composition and preview are shown', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  await expect(page.getByLabel('Выдавать пробную подписку новым посетителям')).toBeChecked();
  await expect(page.getByLabel('Срок', { exact: true })).toHaveValue('3');
  await expect(page.getByLabel('Единица')).toHaveValue('days');
  await expect(page.getByLabel('Пробных подписок с одного IP в час')).toHaveValue('3');
  await expect(page.getByLabel('Название', { exact: true })).toHaveValue('Пробный доступ');
  await expect(page.getByLabel('Описание')).toHaveValue('Три дня бесплатно');
  await expect(page.getByLabel('Преимущества')).toHaveValue('Без карты');
  await expect(page.getByLabel('Бейдж', { exact: true })).toHaveValue('Бесплатно');
  await expect(page.getByLabel('Построитель', { exact: true })).toHaveValue('7');
  await expect(page.locator('.page-desc')).toContainText('Версия 1');
  await expect(page.getByRole('radio', { name: /^Все страны/ })).toBeChecked();
  await expect(editor(page).getByText('Изменений нет.')).toBeVisible();
  await expect(saveButton(page)).toBeDisabled();

  const p = preview(page);
  await expect(p.locator('[data-preview="ok"]')).toContainText('Новая пробная подписка будет выдана с этим составом.');
  await expect(p).toContainText('72 часа (3 дня)');
  await expect(p).toContainText('Пробный');
  await expect(p).toContainText('DictatorVPN Trial');
  await expect(p.locator('.trial-sample li')).toHaveText([
    '🇩🇪 Berlin · Provider A', '🇩🇪 Munich · Provider A', '🇳🇱 Amsterdam · Provider A', 'Mystery · Provider A',
  ]);
  await expect(p).toContainText('DE 2 · NL 1 · без страны 1');
  // The history panel and the anonymous trial counter.
  await expect(page.getByRole('region', { name: 'Дополнительно' })).toContainText('Изменений ещё не было.');
  await expect(page.getByRole('region', { name: 'Дополнительно' })).toContainText('Пробных без привязки');
  // Loading the editor writes nothing.
  expect(api.mutations()).toHaveLength(0);
  expect(api.problems()).toEqual([]);
});

test('load: nothing stored shows the environment values and saves version 0', async ({ page }) => {
  const api = await backend(page, { stored: false, trialBuilder: null });
  await openTrial(page);
  await expect(page.locator('.page-desc')).toContainText('Сейчас действуют значения из окружения сервера.');
  await expect(page.getByLabel('Срок', { exact: true })).toHaveValue('3');
  await expect(page.getByLabel('Единица')).toHaveValue('hours');
  await expect(page.getByLabel('Построитель', { exact: true })).toHaveValue('');
  await expect(page.getByRole('region', { name: 'Состав' })).toContainText('Без построителя подписка собирается из узлов тарифа «trial» (сейчас 1)');
  await expect(page.getByRole('region', { name: 'Страны' })).toContainText('Выберите построитель в разделе «Состав»');
  await expect(preview(page)).toContainText('Узлы тарифа «trial»');
  await expect(preview(page)).toContainText('По узлам тарифа');

  await page.getByLabel('Название', { exact: true }).fill('Пробный');
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  const [put] = api.mutations('/admin/api/trial');
  expect(put.body).toEqual({
    request_key: expect.stringMatching(KEY_PATTERN), version: 0, enabled: true, duration_hours: 3, rate_limit_per_hour: 3,
    title: 'Пробный', description: '', features: [], badge: '', builder_id: null, composition: null,
  });
  await expect(page.locator('.page-desc')).toContainText('Версия 1');
  expect(api.problems()).toEqual([]);
});

test('load: an error offers a retry', async ({ page }) => {
  const api = await backend(page, { viewFailures: 1 });
  await page.goto('/#/tariffs/trial');
  const alert = page.getByRole('alert').filter({ hasText: 'Не удалось загрузить пробную подписку' });
  await expect(alert).toBeVisible();
  await alert.getByRole('button', { name: 'Повторить' }).click();
  await expect(page.getByLabel('Срок', { exact: true })).toHaveValue('3');
  expect(api.problems()).toEqual([]);
});

test('duration: presets, units, bounds and the saved value', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  const durationInput = page.getByLabel('Срок', { exact: true });
  const presets = page.getByRole('group', { name: 'Быстрый выбор срока' });
  await expect(presets.getByRole('button', { name: '72 часа' })).toHaveAttribute('aria-pressed', 'true');

  await presets.getByRole('button', { name: '24 часа' }).click();
  await expect(durationInput).toHaveValue('1');
  await expect(page.getByLabel('Единица')).toHaveValue('days');
  await expect(presets.getByRole('button', { name: '24 часа' })).toHaveAttribute('aria-pressed', 'true');
  await expect(editor(page)).toContainText('24 часа (1 день). Выданная сейчас подписка истечёт');
  await expect(editor(page).getByText('Есть несохранённые изменения.')).toBeVisible();
  await expect(preview(page)).toContainText('24 часа (1 день)');

  // Beyond 7 days: the field is rejected locally, nothing is sent.
  await page.getByLabel('Единица').selectOption('hours');
  await durationInput.fill('200');
  await expect(preview(page)).toContainText('Исправьте ошибки в полях — предпросмотр обновится.');
  await saveButton(page).click();
  await expect(editor(page).getByText('Срок — от 1 часа до 168 часов (7 дней).')).toBeVisible();
  await expect(durationInput).toHaveAttribute('aria-invalid', 'true');
  await expect(durationInput).toBeFocused();
  for (const bad of ['0', '1,5', '']) {
    await durationInput.fill(bad);
    await saveButton(page).click();
    await expect(durationInput).toHaveAttribute('aria-invalid', 'true');
  }
  expect(api.mutations()).toHaveLength(0);

  await durationInput.fill('6');
  await expect(durationInput).not.toHaveAttribute('aria-invalid', 'true');
  await expect(preview(page)).toContainText('6 часов');
  await saveButton(page).dblclick();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  const puts = api.mutations('/admin/api/trial');
  expect(puts).toHaveLength(1);
  expect(puts[0].body).toMatchObject({ version: 1, duration_hours: 6, builder_id: 7, composition: null });
  expect(api.settings()).toMatchObject({ duration_hours: 6, version: 2 });
  // The saved state becomes the new baseline.
  await expect(durationInput).toHaveValue('6');
  await expect(page.getByLabel('Единица')).toHaveValue('hours');
  await expect(saveButton(page)).toBeDisabled();
  await expect(page.locator('.page-desc')).toContainText('Версия 2');
  await expect(page.getByRole('region', { name: 'Дополнительно' })).toContainText('Настройки сохранены · admin');
  expect(api.problems()).toEqual([]);
});

test('builder: switching builders shows their composition and usage', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  const select = page.getByLabel('Построитель', { exact: true });
  await expect(select.locator('option')).toHaveText([
    'Без построителя — узлы тарифа «trial»', 'Основной — общий', 'Отключённый — отключён', 'Пробный',
  ]);
  const composition = page.getByRole('region', { name: 'Состав' });
  await expect(composition).toContainText('Только пробной подпиской');

  // A shared builder: usable as is, its rules are read with GET /builders/{id}.
  await select.selectOption('8');
  await expect(composition).toContainText('тарифы: Стандарт');
  await expect(composition.getByRole('button', { name: 'Создать копию для пробной подписки' })).toBeVisible();
  await expect(page.getByRole('radio', { name: 'Только выбранные страны и серверы' })).toBeChecked();
  await expect(page.getByRole('radio', { name: 'Только выбранные страны и серверы' })).toBeDisabled();
  await expect(page.getByRole('region', { name: 'Страны' })).toContainText('Только просмотр');
  await expect(countryBox(page, 'US')).toBeChecked();
  await expect(countryBox(page, 'US')).toBeDisabled();
  await expect(preview(page).locator('.trial-sample li')).toHaveText(['🇺🇸 New York · Provider B', '🇳🇱 Amsterdam · Provider A']);

  // Without a builder: the trial plan nodes, no countries.
  await select.selectOption('');
  await expect(composition).toContainText('Без построителя подписка собирается из узлов тарифа «trial»');
  await expect(preview(page)).toContainText('Узлы тарифа «trial»');

  await select.selectOption('8');
  await expect(composition).toContainText('тарифы: Стандарт');
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  const [put] = api.mutations('/admin/api/trial');
  // Assigning a shared builder never sends a composition: its rules are untouched.
  expect(put.body).toMatchObject({ builder_id: 8, composition: null });
  expect(api.plan('trial').builder).toBe(8);
  expect(api.builder(8).version).toBe(3);
  expect(api.plan('Стандарт').builder).toBe(8);
  expect(api.problems()).toEqual([]);
});

test('countries and nodes: selection maps to country and node rules', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  const countries = page.getByRole('region', { name: 'Страны' });

  await page.getByRole('radio', { name: 'Только выбранные страны и серверы' }).check();
  await expect(countryBox(page, 'DE')).toBeVisible();
  await expect(countryRow(page, 'DE')).toContainText('2 сервера');
  await expect(countries.locator('.trial-country-name')).toHaveText(['Германия · DE', 'Нидерланды · NL', 'Без страны']);
  // Nothing chosen yet: invalid, no preview request for it.
  await expect(preview(page)).toContainText('Исправьте ошибки в полях — предпросмотр обновится.');

  // A whole country: one country rule; new servers of the country join automatically.
  await countryBox(page, 'DE').check();
  await expect(countryRow(page, 'DE')).toContainText('вся страна, новые серверы — автоматически');
  await expect(preview(page).locator('.trial-sample li')).toHaveText(['🇩🇪 Berlin · Provider A', '🇩🇪 Munich · Provider A']);

  // Unticking one server of the country keeps exactly the others.
  await countryRow(page, 'DE').getByRole('button', { name: 'Серверы' }).click();
  const servers = page.getByRole('list', { name: 'Серверы: Германия · DE' });
  await expect(servers.getByRole('checkbox')).toHaveCount(2);
  await servers.getByLabel('🇩🇪 Berlin').uncheck();
  await expect(servers.getByLabel('🇩🇪 Munich')).toBeChecked();
  await expect(countryRow(page, 'DE')).toContainText('выбрано 1');
  expect(await countryBox(page, 'DE').evaluate(box => (box as HTMLInputElement).indeterminate)).toBe(true);
  await expect(preview(page).locator('.trial-sample li')).toHaveText(['🇩🇪 Munich · Provider A']);

  // A group without a country is chosen by its servers.
  await page.getByRole('checkbox', { name: 'Без страны' }).check();
  await expect(countries.locator('.trial-country').filter({ hasText: 'Без страны' })).toContainText('выбирается по серверам');

  // A second source: its catalogue loads and its countries can be added.
  await page.getByRole('group', { name: 'Источники построителя' }).getByRole('checkbox', { name: /^Provider B/ }).check();
  await expect(countryBox(page, 'JP')).toBeVisible();
  await countryBox(page, 'JP').check();

  const order = countries.locator('.trial-rule');
  await expect(order.locator('.slot-name')).toHaveText(['🇩🇪 Munich', 'Mystery', 'Япония · JP']);
  await order.nth(2).getByRole('button', { name: 'Выше' }).click();
  await expect(order.locator('.slot-name')).toHaveText(['🇩🇪 Munich', 'Япония · JP', 'Mystery']);
  await expect(preview(page).locator('.trial-sample li')).toHaveText(['🇩🇪 Munich · Provider A', '🇯🇵 Tokyo · Provider B', 'Mystery · Provider A']);
  await expect(preview(page)).toContainText('DE 1 · JP 1 · без страны 1');

  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  const [put] = api.mutations('/admin/api/trial');
  expect(put.body.composition).toEqual({
    builder_version: 1, mode: 'selected', source_ids: [1, 2],
    rules: [node(1, 'a-de2', '🇩🇪 Munich'), country(2, 'JP'), node(1, 'a-x', 'Mystery')],
  });
  expect(api.builder(7)).toMatchObject({ version: 2, sources: [{ source_id: 1, position: 0 }, { source_id: 2, position: 1 }] });
  expect(api.builder(7).items.map(it => it.fingerprint || it.country_code)).toEqual(['a-de2', 'JP', 'a-x']);
  // After the save the stored rules are the baseline.
  await expect(saveButton(page)).toBeDisabled();
  await expect(order.locator('.slot-name')).toHaveText(['🇩🇪 Munich', 'Япония · JP', 'Mystery']);

  // Back to "all countries": the rules are dropped from the saved composition.
  await page.getByRole('radio', { name: /^Все страны/ }).check();
  await expect(countries).toContainText('Подписка получит все серверы всех выбранных источников');
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  expect(api.mutations('/admin/api/trial')[1].body.composition).toEqual({ builder_version: 2, mode: 'all', source_ids: [1, 2], rules: [] });
  expect(api.builder(7).items).toEqual([]);
  expect(api.problems()).toEqual([]);
});

test('preview: the server evaluates the draft without saving; problems block issuance', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  await page.getByLabel('Срок', { exact: true }).fill('5');
  await expect(preview(page)).toContainText('5 дней');
  const last = api.previews().at(-1)!;
  // The preview body is the draft only: no request key, no version.
  expect(Object.keys(last.body).sort()).toEqual([...DRAFT_FIELDS].sort());
  expect(last.body).toMatchObject({ duration_hours: 120, builder_id: 7, composition: null });
  expect(api.mutations()).toHaveLength(0);
  expect(api.history()).toHaveLength(0);

  // A disabled builder: the preview names the problem.
  await page.getByLabel('Построитель', { exact: true }).selectOption('9');
  const blocked = preview(page).locator('[data-preview="blocked"]');
  await expect(blocked).toContainText('С такой конфигурацией пробная подписка не выдаётся.');
  await expect(preview(page).locator('.trial-problems')).toContainText('Построитель отключён: новые пробные подписки не выдаются.');
  await expect(page.getByRole('region', { name: 'Состав' })).toContainText('Новые пробные подписки не выдаются, пока построитель отключён.');

  // Saving it switched on is refused by the server and the field is marked.
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Сервер не сохранил настройки: Построитель отключён');
  await expect(page.getByLabel('Построитель', { exact: true })).toHaveAttribute('aria-invalid', 'true');
  expect(api.settings().version).toBe(1);

  // Switched off, the configuration may be prepared.
  await page.getByLabel('Выдавать пробную подписку новым посетителям').uncheck();
  await expect(blocked).toContainText('Конфигурация неполная: включить пробную подписку с ней нельзя.');
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  expect(api.settings()).toMatchObject({ enabled: false, version: 2 });
  expect(api.plan('trial').builder).toBe(9);
  const puts = api.mutations('/admin/api/trial');
  expect(puts).toHaveLength(2);
  expect(puts[0].body.request_key).not.toBe(puts[1].body.request_key);

  // Switched off with a valid configuration: an informational preview.
  await page.getByLabel('Построитель', { exact: true }).selectOption('7');
  await expect(preview(page).locator('[data-preview="off"]')).toContainText('Выдача выключена. Конфигурация корректна');
  expect(api.problems()).toEqual([]);
});

test('save: a lost response is retried with the same key and is not applied twice', async ({ page }) => {
  const api = await backend(page, { lostResponses: 1 });
  await openTrial(page);
  await page.getByLabel('Бейдж', { exact: true }).fill('Хит');
  await saveButton(page).click();
  await expect(statusText(page).getByRole('alert')).toContainText('Не удалось сохранить.');
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Эти изменения уже были сохранены.');
  const puts = api.mutations('/admin/api/trial');
  expect(puts).toHaveLength(2);
  expect(puts[1].body).toEqual(puts[0].body);
  expect(api.settings()).toMatchObject({ badge: 'Хит', version: 2 });
  expect(api.history()).toHaveLength(1);
  expect(api.problems()).toEqual([]);
});

test('version conflict: another save is detected and the latest version reloads', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  await page.getByLabel('Описание').fill('Моё описание');
  api.touchSettings({ title: 'Изменено в другом окне' });
  await saveButton(page).click();
  const alert = statusText(page).getByRole('alert');
  await expect(alert).toContainText('Настройки пробной подписки изменили в другом окне.');
  await expect(alert).toContainText('несохранённые изменения будут потеряны');
  expect(api.settings()).toMatchObject({ description: 'Три дня бесплатно', version: 2 });

  await alert.getByRole('button', { name: 'Загрузить актуальную версию' }).click();
  await expect(page.getByLabel('Название', { exact: true })).toHaveValue('Изменено в другом окне');
  await expect(page.getByLabel('Описание')).toHaveValue('Три дня бесплатно');
  await page.getByLabel('Описание').fill('Моё описание');
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  expect(api.mutations('/admin/api/trial').map(call => call.body.version)).toEqual([1, 2]);
  expect(api.settings()).toMatchObject({ title: 'Изменено в другом окне', description: 'Моё описание', version: 3 });
  expect(api.problems()).toEqual([]);
});

test('builder version conflict: rules edited elsewhere are not overwritten', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  await page.getByRole('radio', { name: 'Только выбранные страны и серверы' }).check();
  await countryBox(page, 'NL').check();
  api.touchBuilder(7);
  await saveButton(page).click();
  await expect(statusText(page).getByRole('alert')).toContainText('Построитель изменили в другом окне.');
  expect(api.builder(7).items).toEqual([]);
  expect(api.settings().version).toBe(1);
  expect(api.problems()).toEqual([]);
});

test('shared builder: composition is read-only, a copy is created and edited instead', async ({ page }) => {
  const api = await backend(page, { trialBuilder: 8 });
  await openTrial(page);
  const composition = page.getByRole('region', { name: 'Состав' });
  await expect(page.getByLabel('Построитель', { exact: true })).toHaveValue('8');
  await expect(composition).toContainText('Этот построитель используют другие тарифы или подписки');
  await expect(countryBox(page, 'DE')).toBeDisabled();
  await expect(page.getByRole('group', { name: 'Источники построителя' })).toHaveCount(0);

  // Cancelling the confirmation sends nothing.
  await composition.getByRole('button', { name: 'Создать копию для пробной подписки' }).click();
  const confirm = page.getByRole('dialog', { name: 'Создать копию построителя?' });
  await expect(confirm).toContainText('Тарифы и подписки, которые используют «Основной», не изменятся.');
  await confirm.getByRole('button', { name: 'Отмена' }).click();
  await expect(confirm).toBeHidden();
  expect(api.mutations()).toHaveLength(0);

  await composition.getByRole('button', { name: 'Создать копию для пробной подписки' }).click();
  await confirm.getByRole('button', { name: 'Создать копию' }).click();
  await expect(statusText(page)).toContainText('Создан построитель «Основной · пробная» и назначен пробной подписке.');
  const [copy] = api.mutations('/admin/api/trial/builder-copy');
  expect(copy.body).toEqual({ request_key: expect.stringMatching(KEY_PATTERN), builder_id: 8 });
  await expect(page.getByLabel('Построитель', { exact: true })).toHaveValue('10');
  await expect(composition).toContainText('Только пробной подпиской');
  // The copy is already assigned: nothing left to save.
  await expect(saveButton(page)).toBeDisabled();

  // The copy's rules are editable; the original's are not touched.
  await expect(countryBox(page, 'DE')).toBeEnabled();
  await countryBox(page, 'DE').check();
  await saveButton(page).click();
  await expect(statusText(page)).toContainText('Пробная подписка сохранена.');
  const [put] = api.mutations('/admin/api/trial');
  expect(put.body).toMatchObject({ builder_id: 10, composition: { builder_version: 1, mode: 'selected', source_ids: [1, 2] } });
  expect((put.body.composition as Composition).rules).toEqual([country(2, 'US'), node(1, 'a-nl', '🇳🇱 Amsterdam'), country(1, 'DE')]);
  expect(api.builder(8)).toMatchObject({ version: 3 });
  expect(api.builder(8).items.map(it => it.country_code || it.fingerprint)).toEqual(['US', 'a-nl']);
  expect(api.plan('Стандарт').builder).toBe(8);
  expect(api.plan('trial').builder).toBe(10);
  await expect(page.getByRole('region', { name: 'Дополнительно' })).toContainText('Создана копия построителя · admin');
  expect(api.problems()).toEqual([]);
});

test('shared builder: a builder shared after loading is refused by the server', async ({ page }) => {
  const api = await backend(page);
  await openTrial(page);
  await page.getByRole('radio', { name: 'Только выбранные страны и серверы' }).check();
  await countryBox(page, 'DE').check();
  // A paid plan starts using the trial builder meanwhile.
  api.assign('Премиум', 7);
  await saveButton(page).click();
  await expect(statusText(page).getByRole('alert')).toContainText('Создайте копию.');
  expect(api.builder(7)).toMatchObject({ version: 1, items: [] });
  expect(api.settings().version).toBe(1);
  expect(api.problems()).toEqual([]);
});

test('responsive: the editor fits narrow and wide screens', async ({ page }) => {
  await backend(page);
  for (const width of [360, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await openTrial(page);
    await page.getByRole('radio', { name: 'Только выбранные страны и серверы' }).check();
    await countryRow(page, 'DE').getByRole('button', { name: 'Серверы' }).click();
    await expect(page.getByRole('list', { name: 'Серверы: Германия · DE' })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.reload();
  }
});
