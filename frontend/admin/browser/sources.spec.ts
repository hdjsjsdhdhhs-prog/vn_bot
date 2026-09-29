import { test, expect } from '@playwright/test';
import type { Page, Route } from '@playwright/test';

// Source catalogue and its use in the Builder, against a mock backend that
// follows the real contract:
//   routes    internal/web/admin_builder_api.go (create returns {source, sync},
//             refresh takes no entries and answers 200 with a sync result even
//             when the upstream failed, unknown fields are rejected)
//   catalogue database.SourceCatalogueStats (present entries only, by_country
//             by descending count then code, absent counted apart); a failed
//             sync keeps the previous catalogue
//   preview   database.PreviewBuilder (country rules expand to every present
//             entry of the country, node rules resolve by fingerprint then by a
//             unique name, unlinked/disabled sources are skipped)
// URLs are placeholders: no real provider address or credential is used.

const CSRF = 'c'.repeat(43);
const T0 = '2026-09-20T08:00:00Z';
const KEY_PATTERN = /^ui-[0-9a-f]{32}$/;
const SECRET_URL = 'https://provider.test/sub/fixture-token-000';

interface Entry {
  id: number; source_id: number; fingerprint: string; original_name: string; protocol: string; country_code: string;
  upstream_position: number; present: boolean; last_seen_at: string;
}
type EntryInput = Pick<Entry, 'fingerprint' | 'original_name' | 'protocol' | 'country_code'>;

interface Source {
  id: number; name: string; type: string; description: string; enabled: boolean;
  last_sync_at: string | null; last_sync_status: string; last_sync_error: string;
}

/** The next upstream answer of a source: a parsed catalogue or a failure code. */
type Outcome = { entries: EntryInput[]; skipped?: number; format?: string } | { error: string };

interface Item {
  id: number; kind: 'country' | 'node'; source_id: number; country_code: string; fingerprint: string; original_name: string;
  custom_name: string | null; description: string; position: number; enabled: boolean;
}
interface Builder {
  id: number; name: string; description: string; enabled: boolean; profile_title: string; support_url: string; announce: string;
  version: number; sources: { source_id: number; position: number }[]; items: Item[];
}
interface Call { method: string; path: string; body: Record<string, unknown> }

const e = (fingerprint: string, original_name: string, country_code: string, protocol = 'vless'): EntryInput =>
  ({ fingerprint, original_name, country_code, protocol });

const CATALOGUE_A = [
  e('a1', '🇩🇪 Frankfurt 1', 'DE'), e('a2', '🇩🇪 Frankfurt 2', 'DE'), e('a3', '🇩🇪 Berlin', 'DE', 'trojan'),
  e('a4', '🇳🇱 Amsterdam', 'NL'), e('a5', 'Mystery', '', 'ss'),
];
const CATALOGUE_B = [e('b1', '🇺🇸 New York', 'US'), e('b2', '🇺🇸 Dallas', 'US'), e('b3', '🇩🇪 Munich', 'DE')];

const CREATE_FIELDS = ['name', 'description', 'type', 'subscription_url', 'hwid', 'user_agent', 'headers', 'enabled'];
const ITEM_FIELDS = ['id', 'kind', 'source_id', 'country_code', 'fingerprint', 'original_name', 'custom_name', 'description', 'position', 'enabled'];
const BUILDER_FIELDS = ['request_key', 'version', 'name', 'description', 'enabled', 'profile_title', 'support_url', 'announce'];

async function backend(page: Page, options: { outcomes?: Record<number, Outcome[]>; builderSources?: number[] } = {}) {
  let clock = Date.parse(T0);
  const now = () => new Date(clock += 60_000).toISOString();
  let nextEntryId = 1;
  let nextItemId = 100;
  const sources: Source[] = [];
  const entries: Entry[] = [];
  const outcomes = new Map<number, Outcome[]>(Object.entries(options.outcomes ?? {}).map(([k, v]) => [Number(k), [...v]]));
  const calls: Call[] = [];
  const problems: string[] = [];

  /** database.SyncSourceCatalogue: upsert by fingerprint, mark the rest absent. */
  const applySync = (src: Source, outcome: Outcome) => {
    const at = now();
    const present = () => entries.filter(x => x.source_id === src.id && x.present).length;
    if ('error' in outcome) {
      Object.assign(src, { last_sync_at: at, last_sync_status: 'error', last_sync_error: outcome.error });
      return { status: 'error', error: outcome.error, format: '', added: 0, updated: 0, removed: 0, total: present(), skipped: 0, duplicates: 0 };
    }
    let added = 0, updated = 0, removed = 0;
    const seen = new Set<string>();
    outcome.entries.forEach((input, position) => {
      seen.add(input.fingerprint);
      const old = entries.find(x => x.source_id === src.id && x.fingerprint === input.fingerprint);
      if (old) { Object.assign(old, input, { upstream_position: position, present: true, last_seen_at: at }); updated++; }
      else { entries.push({ id: nextEntryId++, source_id: src.id, ...input, upstream_position: position, present: true, last_seen_at: at }); added++; }
    });
    for (const x of entries) {
      if (x.source_id === src.id && x.present && !seen.has(x.fingerprint)) { x.present = false; removed++; }
    }
    const skipped = outcome.skipped ?? 0;
    const status = skipped > 0 ? 'partial' : 'ok';
    Object.assign(src, { last_sync_at: at, last_sync_status: status, last_sync_error: skipped > 0 ? `skipped:${skipped}` : '' });
    return { status, error: src.last_sync_error, format: outcome.format ?? 'base64', added, updated, removed, total: present(), skipped, duplicates: 0 };
  };

  const addSource = (over: Partial<Source> & { id: number; name: string }, catalogue: EntryInput[] | null) => {
    const src: Source = {
      type: 'auto', description: '', enabled: true, last_sync_at: null, last_sync_status: '', last_sync_error: '', ...over,
    };
    sources.push(src);
    if (catalogue) applySync(src, { entries: catalogue });
    return src;
  };
  addSource({ id: 1, name: 'Provider A' }, CATALOGUE_A);
  // One entry of A disappeared upstream: it is kept but counted apart.
  entries.push({ id: nextEntryId++, source_id: 1, ...e('a0', '🇩🇪 Old', 'DE'), upstream_position: 0, present: false, last_seen_at: T0 });
  addSource({ id: 2, name: 'Provider B', type: 'json' }, CATALOGUE_B);
  const broken = addSource({ id: 3, name: 'Broken', type: 'base64' }, null);
  applySync(broken, { error: 'timeout' });

  const builders: Builder[] = [{
    id: 7, name: 'Основной', description: '', enabled: true, profile_title: '', support_url: '', announce: '', version: 1,
    sources: (options.builderSources ?? [1]).map((source_id, position) => ({ source_id, position })), items: [],
  }];

  /** service.SourceView: metadata and catalogue statistics, never the URL or credentials. */
  const view = (src: Source) => {
    const mine = entries.filter(x => x.source_id === src.id);
    const live = mine.filter(x => x.present);
    const counts = new Map<string, number>();
    const protocols: Record<string, number> = {};
    for (const x of live) {
      if (x.country_code) counts.set(x.country_code, (counts.get(x.country_code) ?? 0) + 1);
      if (x.protocol) protocols[x.protocol] = (protocols[x.protocol] ?? 0) + 1;
    }
    const by_country = [...counts].map(([code, count]) => ({ code, count }))
      .sort((a, b) => b.count - a.count || a.code.localeCompare(b.code));
    return {
      ...src, created_at: T0, updated_at: T0,
      catalogue: {
        entries: live.length, countries: counts.size, no_country: live.filter(x => !x.country_code).length,
        absent: mine.length - live.length, by_country, protocols,
      },
    };
  };
  const presentOf = (id: number) => entries.filter(x => x.source_id === id && x.present).sort((a, b) => a.upstream_position - b.upstream_position);

  /** database.PreviewBuilder. */
  const preview = (b: Builder) => {
    const linked = new Set(b.sources.map(s => s.source_id));
    const usable = (id: number) => sources.find(s => s.id === id)?.enabled ?? false;
    const items: unknown[] = [];
    const warnings: string[] = [];
    const seen = new Set<string>();
    let pos = 0, missing = 0, conflicts = 0, total = 0;
    const add = (itemId: number, kind: string, x: Entry, name: string) => {
      const key = `${x.source_id}|${x.fingerprint}`;
      if (seen.has(key)) return;
      seen.add(key);
      items.push({ item_id: itemId, kind, source_id: x.source_id, entry: x, display_name: name, status: 'matched', position: pos++ });
      total++;
    };
    if (b.items.length === 0) {
      for (const link of b.sources) {
        if (!usable(link.source_id)) { warnings.push(`source ${link.source_id} is disabled or unusable, skipped`); continue; }
        for (const x of presentOf(link.source_id)) add(0, 'source', x, x.original_name);
      }
    }
    for (const it of [...b.items].sort((x, y) => x.position - y.position || x.id - y.id)) {
      if (!it.enabled) continue;
      if (!linked.has(it.source_id)) { warnings.push(`item ${it.id}: source ${it.source_id} is not linked to the builder, rule ignored`); continue; }
      if (!usable(it.source_id)) { warnings.push(`item ${it.id}: source ${it.source_id} is disabled or unusable, rule skipped`); continue; }
      const live = presentOf(it.source_id);
      if (it.kind === 'country') {
        for (const x of live) if (x.country_code.toUpperCase() === it.country_code.toUpperCase()) add(it.id, it.kind, x, it.custom_name || x.original_name);
        continue;
      }
      let hit = live.find(x => x.fingerprint === it.fingerprint);
      let status = 'matched';
      if (!hit) {
        const byName = live.filter(x => x.original_name === it.original_name);
        status = byName.length === 1 ? 'fallback' : byName.length === 0 ? 'missing' : 'conflict';
        hit = byName.length === 1 ? byName[0] : undefined;
      }
      if (hit) {
        const key = `${hit.source_id}|${hit.fingerprint}`;
        if (seen.has(key)) continue;
        seen.add(key);
      }
      items.push({ item_id: it.id, kind: it.kind, source_id: it.source_id, entry: hit ?? null,
        display_name: hit ? it.custom_name || hit.original_name : '', status, position: pos });
      if (hit) { pos++; total++; } else if (status === 'missing') missing++; else conflicts++;
    }
    return { builder_id: b.id, items, warnings, missing, conflicts, total, previewed_at: now() };
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
      if (pathname === '/admin/api/sources') return json(route, 200, { sources: sources.map(view) });
      const one = /^\/admin\/api\/sources\/(\d+)$/.exec(pathname);
      if (one) {
        const src = sources.find(s => s.id === Number(one[1]));
        return src ? json(route, 200, view(src)) : json(route, 404, { error: 'not_found' });
      }
      const list = /^\/admin\/api\/sources\/(\d+)\/entries$/.exec(pathname);
      if (list) {
        const id = Number(list[1]);
        if (!sources.some(s => s.id === id)) return json(route, 404, { error: 'not_found' });
        const country = searchParams.get('country') ?? '*';
        const out = searchParams.get('all') === '1' ? entries.filter(x => x.source_id === id)
          : presentOf(id).filter(x => country === '*' || x.country_code === country);
        return json(route, 200, { source_id: id, entries: out });
      }
      if (pathname === '/admin/api/builders') return json(route, 200, { builders });
      problems.push(`unexpected GET ${pathname}`);
      return json(route, 404, { error: 'not_found' });
    }

    const raw = request.postData();
    let body: Record<string, unknown> = {};
    if (raw) {
      try { body = JSON.parse(raw) as Record<string, unknown>; } catch { problems.push(`bad json ${pathname}`); }
      if (request.headers()['content-type']?.split(';')[0].trim() !== 'application/json') problems.push(`content type ${pathname}`);
    }
    calls.push({ method, path: pathname, body });
    if (request.headers()['x-csrf-token'] !== CSRF) { problems.push(`csrf ${pathname}`); return json(route, 403, { error: 'forbidden' }); }
    const unknown = (allowed: string[]) => Object.keys(body).filter(k => !allowed.includes(k));

    if (method === 'POST' && pathname === '/admin/api/sources') {
      if (unknown(CREATE_FIELDS).length) { problems.push(`unknown fields ${unknown(CREATE_FIELDS)}`); return json(route, 400, { error: 'invalid_request' }); }
      const url = String(body.subscription_url);
      if (!/^https?:\/\/[^\s/#]+/.test(url) || url.includes('rejected')) return json(route, 400, { error: 'invalid_source', field: 'subscription_url' });
      const id = Math.max(...sources.map(s => s.id)) + 1;
      const src = addSource({ id, name: String(body.name), type: String(body.type), description: String(body.description), enabled: Boolean(body.enabled) }, null);
      const sync = { ...applySync(src, outcomes.get(id)?.shift() ?? { entries: [] }) };
      return json(route, 201, { source: view(src), sync: { ...sync, source: view(src) } });
    }
    const refresh = /^\/admin\/api\/sources\/(\d+)\/refresh$/.exec(pathname);
    if (refresh && method === 'POST') {
      if (raw && raw.trim() !== '{}') { problems.push('refresh with a body'); return json(route, 400, { error: 'invalid_request' }); }
      const src = sources.find(s => s.id === Number(refresh[1]));
      if (!src) return json(route, 404, { error: 'not_found' });
      // Keeps the sync in flight long enough to observe the busy state.
      await new Promise(resolve => setTimeout(resolve, 300));
      const result = applySync(src, outcomes.get(src.id)?.shift() ?? { error: 'timeout' });
      return json(route, 200, { ...result, source: view(src) });
    }
    const toggle = /^\/admin\/api\/sources\/(\d+)\/(enable|disable)$/.exec(pathname);
    if (toggle && method === 'POST') {
      const src = sources.find(s => s.id === Number(toggle[1]))!;
      src.enabled = toggle[2] === 'enable';
      return json(route, 200, view(src));
    }

    const bm = /^\/admin\/api\/builders\/(\d+)(?:\/(sources|items|reorder|preview))?(?:\/(\d+))?$/.exec(pathname);
    const b = bm ? builders.find(x => x.id === Number(bm[1])) : undefined;
    if (bm && !b) return json(route, 404, { error: 'not_found' });
    if (b && method === 'PATCH' && !bm![2]) {
      if (unknown(BUILDER_FIELDS).length || !KEY_PATTERN.test(String(body.request_key))) { problems.push('builder body'); return json(route, 400, { error: 'invalid_request' }); }
      if (body.version !== b.version) return json(route, 409, { error: 'version_conflict' });
      Object.assign(b, { name: body.name, description: body.description, enabled: body.enabled, profile_title: body.profile_title,
        support_url: body.support_url, announce: body.announce, version: b.version + 1 });
      return json(route, 200, { builder: b, audit: {} });
    }
    if (b && method === 'PUT' && bm![2] === 'sources') {
      b.sources = (body.source_ids as number[]).map((source_id, position) => ({ source_id, position }));
      return json(route, 200, b);
    }
    if (b && method === 'POST' && bm![2] === 'items') {
      if (unknown(ITEM_FIELDS).length) { problems.push(`item fields ${unknown(ITEM_FIELDS)}`); return json(route, 400, { error: 'invalid_request' }); }
      const input = {
        kind: body.kind as Item['kind'], source_id: Number(body.source_id), country_code: String(body.country_code ?? ''),
        fingerprint: String(body.fingerprint ?? ''), original_name: String(body.original_name ?? ''),
        custom_name: (body.custom_name as string | null | undefined) ?? null, description: String(body.description ?? ''),
        position: Number(body.position), enabled: Boolean(body.enabled),
      };
      let it = body.id ? b.items.find(x => x.id === body.id) : undefined;
      if (body.id && !it) return json(route, 404, { error: 'not_found' });
      if (it) Object.assign(it, input);
      else { it = { id: nextItemId++, ...input }; b.items.push(it); }
      return json(route, 200, it);
    }
    if (b && method === 'DELETE' && bm![2] === 'items' && bm![3]) {
      const idx = b.items.findIndex(x => x.id === Number(bm![3]));
      if (idx < 0) return json(route, 404, { error: 'not_found' });
      b.items.splice(idx, 1);
      return json(route, 200, { deleted: Number(bm![3]) });
    }
    if (b && method === 'POST' && bm![2] === 'reorder') {
      (body.item_ids as number[]).forEach((id, position) => { const it = b.items.find(x => x.id === id); if (it) it.position = position; });
      return json(route, 200, { ok: true });
    }
    if (b && method === 'POST' && bm![2] === 'preview') return json(route, 200, preview(b));

    problems.push(`unexpected ${method} ${pathname}`);
    return json(route, 404, { error: 'not_found' });
  });

  return {
    calls: (path?: string | RegExp, method?: string) => calls.filter(c =>
      (!path || (typeof path === 'string' ? c.path === path : path.test(c.path))) && (!method || c.method === method)),
    problems: () => problems,
    builder: () => builders[0],
    source: (id: number) => sources.find(s => s.id === id)!,
    /** Queues the next upstream answers of a source. */
    queue: (id: number, ...next: Outcome[]) => outcomes.set(id, [...(outcomes.get(id) ?? []), ...next]),
    /** The upstream changed and a background sync ran (another admin, scheduler). */
    resync: (id: number, catalogue: EntryInput[]) => applySync(sources.find(s => s.id === id)!, { entries: catalogue }),
  };
}

const heading = (page: Page, name: string) => page.getByRole('heading', { level: 1, name });
const region = (page: Page, name: string) => page.getByRole('region', { name, exact: true });
const serverRows = (page: Page) => region(page, 'Серверы').locator('tbody tr');

async function openDetail(page: Page, id: number, name: string) {
  await page.goto(`/#/sources/${id}`);
  await expect(heading(page, name)).toBeVisible();
  await expect(region(page, 'Сводка')).toBeVisible();
}

async function openBuilder(page: Page) {
  await page.goto('/#/builders');
  await page.getByRole('button', { name: 'Редактировать' }).click();
  const dialog = page.getByRole('dialog', { name: 'Построитель: Основной' });
  await expect(dialog).toBeVisible();
  // The editor loads the source catalogues itself when the list was not visited.
  await expect(dialog.locator('.source-pick-item', { hasText: 'Provider A' })).toBeVisible();
  return dialog;
}

test.describe('sources', () => {
  test('list: sync status, servers and countries of each source', async ({ page }) => {
    const api = await backend(page);
    await page.goto('/#/sources');
    await expect(heading(page, 'Источники')).toBeVisible();
    const list = region(page, 'Источники');
    await expect(list.locator('.panel-meta')).toHaveText('3 · 8 серверов всего');

    const a = list.getByRole('listitem', { name: 'Provider A' });
    await expect(a.locator('.source-stats')).toHaveText('5 серверов · 2 страны');
    await expect(a).toContainText('Синхронизирован');
    await expect(a).toContainText('Автоопределение');
    await expect(list.getByRole('listitem', { name: 'Provider B' })).toContainText('JSON (Xray / 3x-ui)');

    const broken = list.getByRole('listitem', { name: 'Broken' });
    await expect(broken).toContainText('Ошибка синхронизации');
    await expect(broken.locator('.source-error')).toHaveText('Источник не ответил вовремя.');
    await expect(broken.locator('.source-stats')).toHaveText('0 серверов · 0 стран');

    await a.getByRole('link', { name: 'Provider A', exact: true }).click();
    await expect(heading(page, 'Provider A')).toBeVisible();
    await expect(page).toHaveURL(/#\/sources\/1$/);
    expect(api.problems()).toEqual([]);
  });

  test('create: Auto format by default, validation, initial sync, no URL echo', async ({ page }) => {
    const api = await backend(page, { outcomes: { 4: [{ entries: [e('n1', '🇫🇮 Helsinki', 'FI'), e('n2', '🇫🇮 Tampere', 'FI', 'trojan'), e('n3', '🇸🇪 Stockholm', 'SE')] }] } });
    await page.goto('/#/sources');
    await page.getByRole('button', { name: 'Добавить источник' }).first().click();
    const dialog = page.getByRole('dialog', { name: 'Новый источник' });
    await expect(dialog).toBeVisible();

    const format = dialog.getByLabel('Формат подписки');
    await expect(format).toHaveValue('auto');
    await expect(format.locator('option')).toHaveText([
      'Автоопределение', 'JSON (Xray / 3x-ui)', 'Base64-подписка', 'Список ссылок', 'Clash / Mihomo (YAML)',
    ]);
    await format.selectOption('clash');
    await expect(dialog).toContainText('YAML-конфиг с разделом proxies.');
    await format.selectOption('auto');

    const submit = dialog.getByRole('button', { name: 'Создать и синхронизировать' });
    await submit.click();
    await expect(dialog.getByText('Введите название (до 255 символов).')).toBeVisible();
    await dialog.getByLabel('Название').fill('Nordic');
    await dialog.getByLabel('URL подписки').fill('provider.test/sub');
    await submit.click();
    await expect(dialog.getByText('Нужен полный адрес http:// или https:// без #фрагмента.')).toBeVisible();
    await dialog.getByLabel('URL подписки').fill(SECRET_URL);
    await dialog.getByLabel('Дополнительные заголовки').fill('{broken');
    await submit.click();
    await expect(dialog.getByText(/Заголовки — JSON-объект/)).toBeVisible();
    expect(api.calls('/admin/api/sources', 'POST')).toHaveLength(0);

    await dialog.getByLabel('Дополнительные заголовки').fill('{"X-Client": "panel"}');
    await submit.click();
    await expect(heading(page, 'Nordic')).toBeVisible();
    await expect(page).toHaveURL(/#\/sources\/4$/);

    const [create] = api.calls('/admin/api/sources', 'POST');
    expect(create.body).toEqual({
      name: 'Nordic', description: '', type: 'auto', subscription_url: SECRET_URL, hwid: '', user_agent: '',
      headers: '{"X-Client": "panel"}', enabled: true,
    });
    // The initial sync ran on the server as part of the create request.
    expect(api.calls(/\/refresh$/)).toHaveLength(0);
    await expect(page.locator('.source-sync-status')).toContainText('Синхронизировано: 3 сервера · 2 страны.');
    await expect(page.locator('.source-sync-status')).toContainText('Формат ответа: Base64-подписка.');
    await expect(region(page, 'Сводка')).toContainText('Синхронизирован');
    await expect(serverRows(page)).toHaveCount(3);
    // The URL and credentials are write-only: never shown back.
    await expect(page.locator('body')).not.toContainText('fixture-token');
    expect(api.problems()).toEqual([]);
  });

  test('create: a failed initial sync keeps the source and says why; server field errors', async ({ page }) => {
    const api = await backend(page, { outcomes: { 4: [{ error: 'http_403' }] } });
    await page.goto('/#/sources');
    await page.getByRole('button', { name: 'Добавить источник' }).first().click();
    const dialog = page.getByRole('dialog', { name: 'Новый источник' });
    await dialog.getByLabel('Название').fill('Guarded');
    await dialog.getByLabel('URL подписки').fill('https://provider.test/rejected');
    await dialog.getByRole('button', { name: 'Создать и синхронизировать' }).click();
    await expect(dialog.getByText('Нужен полный адрес http:// или https:// без #фрагмента.')).toBeVisible();
    await expect(dialog.getByLabel('URL подписки')).toHaveAttribute('aria-invalid', 'true');

    await dialog.getByLabel('URL подписки').fill('https://provider.test/guarded');
    await dialog.getByRole('button', { name: 'Создать и синхронизировать' }).click();
    await expect(heading(page, 'Guarded')).toBeVisible();
    const status = page.locator('.source-sync-status');
    await expect(status).toContainText('Синхронизация не удалась. Источник ответил ошибкой HTTP 403. Каталог пуст.');
    await expect(region(page, 'Сводка')).toContainText('Ошибка синхронизации');
    await expect(serverRows(page)).toHaveText(['Каталог пуст. Нажмите «Синхронизировать».']);
    expect(api.problems()).toEqual([]);
  });

  test('details: statistics and country breakdown', async ({ page }) => {
    const api = await backend(page);
    await openDetail(page, 1, 'Provider A');
    const summary = region(page, 'Сводка');
    await expect(summary.locator('.panel-meta')).toHaveText('5 серверов · 2 страны');
    await expect(summary).toContainText('Исчезли из подписки: 1');
    await expect(summary).toContainText('Без страны: 1');
    await expect(summary).toContainText('vless 3 · ss 1 · trojan 1');

    const chips = region(page, 'Страны').locator('.country-chip');
    await expect(chips).toHaveText(['🇩🇪 DE · 3', '🇳🇱 NL · 1', 'Без страны · 1']);
    expect(api.problems()).toEqual([]);
  });

  test('details: full node list with country, protocol and name filters', async ({ page }) => {
    const api = await backend(page);
    await openDetail(page, 1, 'Provider A');
    const servers = region(page, 'Серверы');
    const count = servers.locator('.source-filter-count');
    await expect(serverRows(page)).toHaveCount(5);
    await expect(count).toHaveText('Показано 5 из 5');

    await region(page, 'Страны').locator('.country-chip[data-country="DE"]').click();
    await expect(servers.getByLabel('Страна')).toHaveValue('DE');
    await expect(serverRows(page)).toHaveCount(3);
    await expect(region(page, 'Страны').locator('.country-chip[data-country="DE"]')).toHaveAttribute('aria-pressed', 'true');

    await servers.getByLabel('Протокол').selectOption('trojan');
    await expect(serverRows(page)).toHaveCount(1);
    await expect(serverRows(page).first()).toContainText('🇩🇪 Berlin');
    await servers.getByLabel('Протокол').selectOption('');

    await servers.getByLabel('Поиск по имени').fill('frankfurt 2');
    await expect(serverRows(page)).toHaveCount(1);
    await expect(count).toHaveText('Показано 1 из 5');
    await servers.getByLabel('Поиск по имени').fill('');

    await servers.getByLabel('Страна').selectOption('-');
    await expect(serverRows(page)).toHaveCount(1);
    await expect(serverRows(page).first()).toContainText('Mystery');
    await servers.getByLabel('Страна').selectOption('');

    await servers.getByLabel('Показывать исчезнувшие (1)').check();
    await expect(serverRows(page)).toHaveCount(6);
    await expect(servers.locator('tbody tr.is-absent')).toContainText('🇩🇪 Old');
    await servers.getByLabel('Поиск по имени').fill('zzz');
    await expect(serverRows(page)).toHaveText(['Нет серверов под выбранные фильтры.']);
    expect(api.problems()).toEqual([]);
  });

  test('refresh: success updates the catalogue, partial is reported', async ({ page }) => {
    const api = await backend(page);
    api.queue(1,
      { entries: [...CATALOGUE_A.slice(0, 4), e('a6', '🇺🇸 Seattle', 'US', 'hysteria2')] },
      { entries: CATALOGUE_A, skipped: 2 });
    await openDetail(page, 1, 'Provider A');
    const sync = page.getByRole('button', { name: 'Синхронизировать', exact: true });
    await sync.click();
    await expect(page.getByRole('button', { name: 'Синхронизируем…' })).toBeDisabled();
    const status = page.locator('.source-sync-status');
    await expect(status).toContainText('Синхронизировано: 5 серверов · 3 страны.');
    await expect(status).toContainText('Добавлено 1, обновлено 4, исчезло 1.');
    await expect(region(page, 'Страны').locator('.country-chip')).toHaveText(['🇩🇪 DE · 3', '🇳🇱 NL · 1', '🇺🇸 US · 1']);
    await expect(serverRows(page)).toHaveCount(5);
    await expect(sync).toBeEnabled();

    await sync.click();
    await expect(status).toContainText('Не удалось разобрать 2 сервера из ответа.');
    await expect(region(page, 'Сводка')).toContainText('Синхронизирован частично');
    expect(api.calls('/admin/api/sources/1/refresh').map(c => c.body)).toEqual([{}, {}]);
    expect(api.problems()).toEqual([]);
  });

  test('refresh: a failure (timeout) keeps the previous catalogue', async ({ page }) => {
    const api = await backend(page);
    api.queue(1, { error: 'timeout' });
    await openDetail(page, 1, 'Provider A');
    await page.getByRole('button', { name: 'Синхронизировать', exact: true }).click();
    await expect(page.locator('.source-sync-status')).toContainText(
      'Синхронизация не удалась. Источник не ответил вовремя. Сохранён прежний каталог: 5 серверов.');
    const summary = region(page, 'Сводка');
    await expect(summary).toContainText('Ошибка синхронизации');
    await expect(summary).toContainText('Показан каталог последней успешной синхронизации');
    await expect(summary.locator('.panel-meta')).toHaveText('5 серверов · 2 страны');
    await expect(serverRows(page)).toHaveCount(5);

    // The list shows the same outcome for the row.
    await page.getByRole('link', { name: 'Назад к источникам' }).click();
    const row = region(page, 'Источники').getByRole('listitem', { name: 'Provider A' });
    await expect(row).toContainText('Ошибка синхронизации');
    await expect(row.locator('.source-stats')).toHaveText('5 серверов · 2 страны');
    expect(api.problems()).toEqual([]);
  });

  test('refresh from the list row', async ({ page }) => {
    const api = await backend(page);
    api.queue(3, { entries: [e('c1', '🇯🇵 Tokyo', 'JP')] });
    await page.goto('/#/sources');
    const row = region(page, 'Источники').getByRole('listitem', { name: 'Broken' });
    await row.getByRole('button', { name: 'Синхронизировать «Broken»' }).click();
    await expect(row.locator('.source-stats')).toHaveText('1 сервер · 1 страна');
    await expect(row).toContainText('Синхронизирован');
    await expect(row.locator('.notice')).toContainText('Синхронизировано: 1 сервер · 1 страна.');
    expect(api.problems()).toEqual([]);
  });
});

test.describe('builder uses the source catalogue', () => {
  test('selected sources show their servers, countries and sync status; totals merge them', async ({ page }) => {
    const api = await backend(page);
    const dialog = await openBuilder(page);
    const a = dialog.locator('.source-pick-item', { hasText: 'Provider A' });
    await expect(a.locator('.source-pick-stats')).toHaveText('5 серверов · 2 страны');
    await expect(a).toContainText('Синхронизирован');
    const broken = dialog.locator('.source-pick-item', { hasText: 'Broken' });
    await expect(broken).toContainText('Ошибка синхронизации');

    const total = dialog.locator('.sources-total');
    await expect(total).toHaveText('Итого: 5 серверов · 2 страны (уникальных) из 1 источника.');
    await dialog.getByRole('button', { name: 'Добавить «Provider B»' }).click();
    // DE is in both sources: countries are counted once.
    await expect(total).toHaveText('Итого: 8 серверов · 3 страны (уникальных) из 2 источников.');
    await dialog.getByRole('button', { name: 'Убрать «Provider A»' }).click();
    await expect(total).toHaveText('Итого: 3 сервера · 2 страны (уникальных) из 1 источника.');
    await dialog.getByRole('button', { name: 'Добавить «Provider A»' }).click();

    await dialog.getByRole('button', { name: 'Сохранить' }).click();
    await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
    expect(api.builder().sources.map(s => s.source_id)).toEqual([2, 1]);
    expect(api.problems()).toEqual([]);
  });

  test('dynamic country rule from the real catalogue; preview matches it and follows the catalogue', async ({ page }) => {
    const api = await backend(page, { builderSources: [1, 2] });
    const dialog = await openBuilder(page);
    await dialog.getByRole('button', { name: 'Добавить правило' }).click();
    const rule = page.getByRole('dialog', { name: 'Новое правило' });
    await expect(rule.getByLabel('Тип правила')).toHaveValue('country');
    await expect(rule.getByLabel('Источник').locator('option')).toHaveText([
      'Provider A — 5 серверов · 2 страны', 'Provider B — 3 сервера · 2 страны',
    ]);
    const country = rule.getByLabel('Страна', { exact: true });
    await expect(country.locator('option')).toHaveText(['🇩🇪 DE — 3 сервера', '🇳🇱 NL — 1 сервер', 'Другая страна…']);
    await rule.getByLabel('Источник').selectOption('2');
    await expect(country.locator('option')).toHaveText(['🇺🇸 US — 2 сервера', '🇩🇪 DE — 1 сервер', 'Другая страна…']);
    await rule.getByLabel('Источник').selectOption('1');
    await country.selectOption('DE');
    await expect(rule.locator('.rule-live')).toContainText('Сейчас в каталоге: 3 сервера.');

    // A country that is not in the catalogue yet needs a valid ISO code.
    await country.selectOption('*other');
    await rule.getByRole('button', { name: 'Применить' }).click();
    await expect(rule.getByText('Укажите двухбуквенный код страны латиницей, например DE.')).toBeVisible();
    await country.selectOption('DE');
    await rule.getByRole('button', { name: 'Применить' }).click();
    await expect(rule).toBeHidden();

    const row = dialog.getByRole('listitem', { name: '🇩🇪 DE' });
    await expect(row.locator('.item-detail')).toHaveText('Все серверы страны 🇩🇪 DE из «Provider A» · сейчас 3 сервера');
    await expect(dialog.locator('.preview-note')).toContainText('предпросмотр показывает сохранённую версию');

    await dialog.getByRole('button', { name: 'Сохранить' }).click();
    await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
    const [upsert] = api.calls('/admin/api/builders/7/items', 'POST');
    expect(upsert.body).toEqual({ kind: 'country', source_id: 1, country_code: 'DE', custom_name: null, description: '', position: 0, enabled: true });
    await expect(dialog.locator('.preview-note')).toBeEmpty();

    // Preview consistency: the same servers the catalogue counts for DE.
    const previewBtn = dialog.getByRole('button', { name: 'Запустить предпросмотр' });
    await previewBtn.click();
    const previewList = dialog.getByRole('list', { name: 'Серверы подписки' });
    await expect(dialog.locator('.preview-body .preview-stats')).toHaveText('В подписке: 3 сервера · 1 страна');
    await expect(previewList.locator('.preview-name')).toHaveText(['🇩🇪 Frankfurt 1', '🇩🇪 Frankfurt 2', '🇩🇪 Berlin']);
    await expect(previewList.locator('.preview-origin').first()).toHaveText('Provider A');

    // The rule is dynamic: a new German server upstream is served without editing the rule.
    api.resync(1, [...CATALOGUE_A, e('a7', '🇩🇪 Hamburg', 'DE')]);
    await previewBtn.click();
    await expect(dialog.locator('.preview-body .preview-stats')).toHaveText('В подписке: 4 сервера · 1 страна');
    await expect(previewList.locator('.preview-name').last()).toHaveText('🇩🇪 Hamburg');
    expect(api.problems()).toEqual([]);
  });

  test('node rule picks a server from the catalogue; removed rules are deleted; saving twice does not duplicate', async ({ page }) => {
    const api = await backend(page, { builderSources: [1, 2] });
    const dialog = await openBuilder(page);

    await dialog.getByRole('button', { name: 'Добавить правило' }).click();
    let rule = page.getByRole('dialog', { name: 'Новое правило' });
    await rule.getByLabel('Страна', { exact: true }).selectOption('NL');
    await rule.getByRole('button', { name: 'Применить' }).click();

    await dialog.getByRole('button', { name: 'Добавить правило' }).click();
    rule = page.getByRole('dialog', { name: 'Новое правило' });
    await rule.getByLabel('Тип правила').selectOption('node');
    await rule.getByLabel('Источник').selectOption('2');
    const picker = rule.getByRole('list', { name: 'Сервер' });
    await expect(picker.locator('.entry-pick-item')).toHaveCount(3);
    await rule.getByRole('button', { name: 'Применить' }).click();
    await expect(rule.getByText('Выберите сервер из списка.')).toBeVisible();
    await rule.getByLabel('Поиск сервера по имени').fill('dallas');
    await expect(picker.locator('.entry-pick-item')).toHaveCount(1);
    await rule.getByLabel('Поиск сервера по имени').fill('');
    await rule.getByLabel('Страна сервера').selectOption('US');
    await expect(picker.locator('.entry-pick-item')).toHaveCount(2);
    await picker.locator('.entry-pick-item[data-fingerprint="b2"]').click();
    await expect(picker.locator('.entry-pick-item[data-fingerprint="b2"]')).toHaveAttribute('aria-pressed', 'true');
    await expect(rule.locator('.rule-node-chosen')).toHaveText('Выбран: 🇺🇸 Dallas · Найден');
    await rule.getByLabel('Своё имя').fill('США — Даллас');
    await rule.getByRole('button', { name: 'Применить' }).click();
    await expect(rule).toBeHidden();
    await expect(dialog.getByRole('listitem', { name: 'США — Даллас' }).locator('.item-detail'))
      .toHaveText('Сервер из «Provider B» · в источнике: 🇺🇸 Dallas');

    await dialog.getByRole('button', { name: 'Сохранить' }).click();
    await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
    const upserts = api.calls('/admin/api/builders/7/items', 'POST');
    expect(upserts.map(c => c.body)).toEqual([
      { kind: 'country', source_id: 1, country_code: 'NL', custom_name: null, description: '', position: 0, enabled: true },
      { kind: 'node', source_id: 2, fingerprint: 'b2', original_name: '🇺🇸 Dallas', custom_name: 'США — Даллас', description: '', position: 1, enabled: true },
    ]);
    expect(api.builder().items).toHaveLength(2);

    await dialog.getByRole('button', { name: 'Запустить предпросмотр' }).click();
    await expect(dialog.locator('.preview-body .preview-stats')).toHaveText('В подписке: 2 сервера · 2 страны');
    await expect(dialog.locator('.preview-sources')).toHaveText('Provider A: 1 · Provider B: 1');
    await expect(dialog.getByRole('list', { name: 'Серверы подписки' }).locator('.preview-name')).toHaveText(['🇳🇱 Amsterdam', 'США — Даллас']);

    // Remove the country rule: it must be deleted on the server, and the
    // saved node rule is updated in place, not created again.
    page.once('dialog', d => void d.accept());
    await dialog.getByRole('button', { name: 'Удалить правило «🇳🇱 NL»' }).click();
    await dialog.getByRole('button', { name: 'Сохранить' }).click();
    await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
    expect(api.calls(/^\/admin\/api\/builders\/7\/items\/\d+$/, 'DELETE')).toHaveLength(1);
    expect(api.builder().items.map(i => i.fingerprint)).toEqual(['b2']);
    const last = api.calls('/admin/api/builders/7/items', 'POST').at(-1)!;
    expect(last.body).toMatchObject({ id: api.builder().items[0].id, kind: 'node', position: 0 });

    await dialog.getByRole('button', { name: 'Запустить предпросмотр' }).click();
    await expect(dialog.locator('.preview-body .preview-stats')).toHaveText('В подписке: 1 сервер · 1 страна');
    expect(api.problems()).toEqual([]);
  });

  test('rules on an unlinked source are flagged, like Preview and /sub ignore them', async ({ page }) => {
    const api = await backend(page, { builderSources: [1, 2] });
    const dialog = await openBuilder(page);
    await dialog.getByRole('button', { name: 'Добавить правило' }).click();
    const rule = page.getByRole('dialog', { name: 'Новое правило' });
    await rule.getByLabel('Источник').selectOption('2');
    await rule.getByLabel('Страна', { exact: true }).selectOption('US');
    await rule.getByRole('button', { name: 'Применить' }).click();
    const row = dialog.getByRole('listitem', { name: '🇺🇸 US' });
    await expect(row.locator('.item-warn')).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Убрать «Provider B»' }).click();
    await expect(row.locator('.item-warn')).toHaveText('Источник не подключён к построителю: правило не применяется.');
    await dialog.getByRole('button', { name: 'Сохранить' }).click();
    await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
    await dialog.getByRole('button', { name: 'Запустить предпросмотр' }).click();
    await expect(dialog.locator('.preview-body .preview-stats')).toHaveText('В подписке: 0 серверов · 0 стран');
    await expect(dialog.locator('.preview-warn')).toContainText('is not linked to the builder');
    expect(api.problems()).toEqual([]);
  });
});
