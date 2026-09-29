import { describe, expect, it, vi } from 'vitest';
import { AdminApi, type AdminSource, type BuilderPreview, type SourceCatalogue, type SourceEntry, type SourceSyncResult } from '../admin/src/api';
import {
  EMPTY_FILTER, builderSourcesSummary, catalogueSummary, countryCount, countryLabel, filterEntries, flagEmoji,
  formatLabel, matchOriginalName, normalizeFormat, pickableEntries, previewSummary, protocolsOf, resolveNode,
  ruleSummary, syncErrorText, syncResultMessage, syncState,
} from '../admin/src/sources';

// Pure logic of the admin source catalogue screens (admin/src/sources.ts) and
// the source part of the admin API client, against the wire shapes of
// service.SourceView / SourceSyncResult and database.ProviderSourceEntry.

const T0 = '2026-09-20T08:00:00Z';

const catalogue = (over: Partial<SourceCatalogue> = {}): SourceCatalogue => ({
  entries: 5, countries: 2, no_country: 1, absent: 1,
  by_country: [{ code: 'DE', count: 3 }, { code: 'NL', count: 1 }], protocols: { vless: 4, trojan: 1 }, ...over,
});

const source = (over: Partial<AdminSource> = {}): AdminSource => ({
  id: 1, name: 'Provider A', type: 'auto', description: '', enabled: true, last_sync_at: T0, last_sync_status: 'ok',
  last_sync_error: '', catalogue: catalogue(), created_at: T0, updated_at: T0, ...over,
});

let nextEntry = 1;
const entry = (over: Partial<SourceEntry> = {}): SourceEntry => ({
  id: nextEntry++, source_id: 1, fingerprint: `fp${nextEntry}`, original_name: 'Server', protocol: 'vless',
  country_code: 'DE', upstream_position: 0, present: true, last_seen_at: T0, ...over,
});

const syncResult = (over: Partial<SourceSyncResult> = {}): SourceSyncResult => ({
  source: source(), status: 'ok', error: '', format: 'base64', added: 5, updated: 0, removed: 0, total: 5,
  skipped: 0, duplicates: 0, ...over,
});

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

describe('source format', () => {
  it('reads legacy free-text types as auto', () => {
    expect(normalizeFormat('base64')).toBe('base64');
    expect(normalizeFormat('xui')).toBe('auto');
    expect(normalizeFormat('')).toBe('auto');
    expect(formatLabel('xui')).toBe('Автоопределение');
    expect(formatLabel('clash')).toBe('Clash / Mihomo (YAML)');
  });
});

describe('sync state and messages', () => {
  it('derives the state from the last sync', () => {
    expect(syncState({ last_sync_at: null, last_sync_status: '' })).toBe('never');
    expect(syncState({ last_sync_at: T0, last_sync_status: 'ok' })).toBe('ok');
    expect(syncState({ last_sync_at: T0, last_sync_status: 'partial' })).toBe('partial');
    expect(syncState({ last_sync_at: T0, last_sync_status: 'error' })).toBe('error');
    // Pre-catalogue failure values are failures too.
    expect(syncState({ last_sync_at: T0, last_sync_status: 'fetch_error' })).toBe('error');
  });

  it('explains stable sync codes', () => {
    expect(syncErrorText('timeout')).toBe('Источник не ответил вовремя.');
    expect(syncErrorText('http_503')).toContain('HTTP 503');
    expect(syncErrorText('skipped:2')).toBe('Не удалось разобрать 2 сервера из ответа.');
    expect(syncErrorText('format_mismatch:clash')).toContain('Clash / Mihomo');
    expect(syncErrorText('unknown_code')).toBe('Ошибка синхронизации (unknown_code).');
    expect(syncErrorText('')).toBe('');
  });

  it('reports success with the statistics of the catalogue', () => {
    const m = syncResultMessage(syncResult({ added: 3, updated: 2, removed: 1, duplicates: 2 }));
    expect(m.tone).toBe('success');
    expect(m.text).toContain('5 серверов · 2 страны');
    expect(m.text).toContain('Добавлено 3, обновлено 2, исчезло 1.');
    expect(m.text).toContain('Повторов объединено: 2.');
  });

  it('reports partial sync as info with the skipped count', () => {
    const m = syncResultMessage(syncResult({ status: 'partial', error: 'skipped:1', skipped: 1 }));
    expect(m.tone).toBe('info');
    expect(m.text).toContain('Не удалось разобрать 1 сервер');
  });

  it('says the previous catalogue is kept when a refresh fails', () => {
    const kept = syncResultMessage(syncResult({ status: 'error', error: 'timeout', added: 0, total: 5 }));
    expect(kept.tone).toBe('error');
    expect(kept.text).toContain('Источник не ответил вовремя.');
    expect(kept.text).toContain('Сохранён прежний каталог: 5 серверов.');
    const empty = syncResultMessage(syncResult({ status: 'error', error: 'unreachable', total: 0 }));
    expect(empty.text).toContain('Каталог пуст.');
  });
});

describe('catalogue statistics', () => {
  it('summarises one source', () => {
    expect(catalogueSummary(catalogue())).toBe('5 серверов · 2 страны');
    expect(catalogueSummary(catalogue({ entries: 1, countries: 1 }))).toBe('1 сервер · 1 страна');
  });

  it('builds flags only for two-letter codes', () => {
    expect(flagEmoji('DE')).toBe('🇩🇪');
    expect(flagEmoji('de')).toBe('');
    expect(flagEmoji('')).toBe('');
    expect(countryLabel('NL')).toBe('🇳🇱 NL');
  });

  it('counts the servers of one country', () => {
    const src = source();
    expect(countryCount(src, 'DE')).toBe(3);
    expect(countryCount(src, 'de')).toBe(3);
    expect(countryCount(src, 'US')).toBe(0);
    expect(countryCount(undefined, 'DE')).toBe(0);
  });
});

describe('entry filters (source details)', () => {
  const list = [
    entry({ original_name: '🇩🇪 Frankfurt 1', country_code: 'DE', protocol: 'vless' }),
    entry({ original_name: '🇩🇪 Berlin', country_code: 'DE', protocol: 'trojan' }),
    entry({ original_name: 'Amsterdam', country_code: 'NL', protocol: 'vless' }),
    entry({ original_name: 'Unknown', country_code: '', protocol: 'ss' }),
    entry({ original_name: 'Gone', country_code: 'DE', protocol: 'vless', present: false }),
  ];

  it('hides absent entries unless asked', () => {
    expect(filterEntries(list, EMPTY_FILTER)).toHaveLength(4);
    expect(filterEntries(list, { ...EMPTY_FILTER, showAbsent: true })).toHaveLength(5);
  });

  it('filters by country, entries without a country, protocol and name', () => {
    expect(filterEntries(list, { ...EMPTY_FILTER, country: 'DE' }).map(e => e.original_name)).toEqual(['🇩🇪 Frankfurt 1', '🇩🇪 Berlin']);
    expect(filterEntries(list, { ...EMPTY_FILTER, country: '-' }).map(e => e.original_name)).toEqual(['Unknown']);
    expect(filterEntries(list, { ...EMPTY_FILTER, protocol: 'vless' })).toHaveLength(2);
    expect(filterEntries(list, { ...EMPTY_FILTER, query: '  FRANK ' }).map(e => e.original_name)).toEqual(['🇩🇪 Frankfurt 1']);
    expect(filterEntries(list, { ...EMPTY_FILTER, country: 'DE', protocol: 'trojan', query: 'ber' })).toHaveLength(1);
  });

  it('lists protocols once, sorted', () => {
    expect(protocolsOf(list)).toEqual(['ss', 'trojan', 'vless']);
  });

  it('caps the node picker and keeps the full count', () => {
    const many = Array.from({ length: 250 }, (_, i) => entry({ original_name: `n${i}` }));
    const { shown, total } = pickableEntries(many, '', '');
    expect(shown).toHaveLength(200);
    expect(total).toBe(250);
    expect(pickableEntries(many, 'n24', '').total).toBe(11);
  });
});

describe('multiple sources in a builder', () => {
  it('sums servers and counts distinct countries of the enabled sources', () => {
    const a = source({ id: 1, catalogue: catalogue({ entries: 5, by_country: [{ code: 'DE', count: 3 }, { code: 'NL', count: 1 }] }) });
    const b = source({ id: 2, name: 'Provider B', catalogue: catalogue({ entries: 4, by_country: [{ code: 'DE', count: 2 }, { code: 'US', count: 2 }] }) });
    const off = source({ id: 3, name: 'Off', enabled: false, catalogue: catalogue({ entries: 7, by_country: [{ code: 'JP', count: 7 }] }) });
    const sum = builderSourcesSummary([a, b, off]);
    expect(sum.entries).toBe(9);
    expect(sum.countries).toBe(3);
    expect(sum.disabled).toBe(1);
    expect(sum.rows.map(r => r.id)).toEqual([1, 2, 3]);
    expect(sum.rows[0].summary).toBe('5 серверов · 2 страны');
  });
});

describe('builder rules against the catalogue', () => {
  const a = source({ id: 1, name: 'A' });
  const b = source({ id: 2, name: 'B', enabled: false });
  const rule = { kind: 'country' as const, source_id: 1, country_code: 'DE', fingerprint: '', original_name: '', custom_name: null, enabled: true };

  it('shows what a dynamic country rule yields now', () => {
    const r = ruleSummary(rule, [a, b], [1, 2]);
    expect(r.title).toBe('🇩🇪 DE');
    expect(r.detail).toBe('Все серверы страны 🇩🇪 DE из «A» · сейчас 3 сервера');
    expect(r.warning).toBe('');
  });

  it('warns when the rule serves nothing', () => {
    expect(ruleSummary({ ...rule, country_code: 'US' }, [a], [1]).warning).toContain('нет серверов этой страны');
    expect(ruleSummary(rule, [a], []).warning).toContain('не подключён');
    expect(ruleSummary({ ...rule, source_id: 2 }, [a, b], [1, 2]).warning).toContain('отключён');
  });

  it('names a node rule by its custom or original name', () => {
    const node = { ...rule, kind: 'node' as const, country_code: '', fingerprint: 'abcdef0123456789', original_name: 'DE-1' };
    expect(ruleSummary(node, [a], [1]).title).toBe('DE-1');
    const custom = ruleSummary({ ...node, custom_name: 'Германия' }, [a], [1]);
    expect(custom.title).toBe('Германия');
    expect(custom.detail).toBe('Сервер из «A» · в источнике: DE-1');
    expect(ruleSummary({ ...node, original_name: '' }, [a], [1]).title).toBe('abcdef012345…');
  });

  it('matches original names in the database tiers', () => {
    expect(matchOriginalName('DE 1', ['DE 1', 'DE%201'])).toEqual([0]);
    expect(matchOriginalName('DE%201', ['DE 1', 'NL'])).toEqual([0]);
    expect(matchOriginalName('DE+1', ['DE 1'])).toEqual([0]);
    expect(matchOriginalName('', ['x'])).toEqual([]);
    expect(matchOriginalName('%E0%A4%A', ['%E0%A4%A'])).toEqual([0]);
  });

  it('resolves a node like the backend: fingerprint, then a unique name', () => {
    const e1 = entry({ fingerprint: 'f1', original_name: 'DE-1' });
    const e2 = entry({ fingerprint: 'f2', original_name: 'Dup' });
    const e3 = entry({ fingerprint: 'f3', original_name: 'Dup' });
    const gone = entry({ fingerprint: 'f4', original_name: 'Gone', present: false });
    const list = [e1, e2, e3, gone];
    expect(resolveNode('f1', 'other', list)).toEqual({ status: 'matched', entry: e1 });
    expect(resolveNode('old', 'DE-1', list)).toEqual({ status: 'fallback', entry: e1 });
    expect(resolveNode('old', 'Dup', list).status).toBe('conflict');
    expect(resolveNode('f4', 'Gone', list).status).toBe('missing');
  });
});

describe('preview summary', () => {
  it('counts only served entries, countries and per-source totals', () => {
    const preview: Pick<BuilderPreview, 'items'> = {
      items: [
        { item_id: 1, kind: 'country', source_id: 1, entry: entry({ country_code: 'DE' }), display_name: 'a', status: 'matched', position: 0 },
        { item_id: 1, kind: 'country', source_id: 1, entry: entry({ country_code: 'DE' }), display_name: 'b', status: 'matched', position: 1 },
        { item_id: 2, kind: 'node', source_id: 2, entry: entry({ source_id: 2, country_code: 'NL' }), display_name: 'c', status: 'fallback', position: 2 },
        { item_id: 3, kind: 'node', source_id: 2, entry: null, display_name: '', status: 'missing', position: 3 },
      ],
    };
    expect(previewSummary(preview)).toEqual({ servers: 3, countries: 2, bySource: [{ sourceId: 1, count: 2 }, { sourceId: 2, count: 1 }] });
  });
});

describe('source API client', () => {
  const setup = () => {
    const transport = vi.fn<typeof fetch>();
    return { transport, api: new AdminApi(transport) };
  };

  it('creates a source and returns the initial sync', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ source: source(), sync: syncResult() }, 201));
    const input = {
      name: 'Provider A', description: '', type: 'auto', subscription_url: 'https://provider.example/sub/token',
      hwid: '', user_agent: '', headers: '', enabled: true,
    };
    const created = await api.createSource(input);
    expect(created.sync?.status).toBe('ok');
    expect(created.source.catalogue.entries).toBe(5);
    const [path, init] = transport.mock.calls[0];
    expect(path).toBe('/admin/api/sources');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual(input);
  });

  it('accepts a created source without a sync result', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ source: source({ last_sync_at: null, last_sync_status: '' }), sync: null }, 201));
    await expect(api.createSource({
      name: 'x', description: '', type: 'auto', subscription_url: 'https://provider.example/', hwid: '', user_agent: '', headers: '', enabled: true,
    })).resolves.toMatchObject({ sync: null });
  });

  it('refreshes server-side: sends no entries, a failure is a result', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(syncResult({ status: 'error', error: 'timeout', added: 0 })));
    const r = await api.syncSource(1);
    expect(r.status).toBe('error');
    expect(r.source.catalogue.entries).toBe(5);
    const [path, init] = transport.mock.calls[0];
    expect(path).toBe('/admin/api/sources/1/refresh');
    expect(init?.body).toBe('{}');
  });

  it('rejects malformed sync results and entries', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(syncResult({ status: 'weird' as 'ok' })));
    await expect(api.syncSource(1)).rejects.toMatchObject({ code: 'invalid_response' });
    transport.mockResolvedValueOnce(json({ ...syncResult(), source: { ...source(), catalogue: null } }));
    await expect(api.syncSource(1)).rejects.toMatchObject({ code: 'invalid_response' });
    transport.mockResolvedValueOnce(json({ entries: [{ ...entry(), present: 'yes' }] }));
    await expect(api.listSourceEntries(1)).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('lists every entry with all=1 and present ones by country', async () => {
    const { transport, api } = setup();
    transport.mockImplementation(() => Promise.resolve(json({ source_id: 1, entries: [entry()] })));
    await api.listAllSourceEntries(3);
    await api.listSourceEntries(3, 'DE');
    await api.listSourceEntries(3);
    expect(transport.mock.calls.map(c => c[0])).toEqual([
      '/admin/api/sources/3/entries?all=1', '/admin/api/sources/3/entries?country=DE', '/admin/api/sources/3/entries?country=*',
    ]);
  });

  it('reports the rejected field of invalid_source', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ error: 'invalid_source', field: 'subscription_url' }, 400));
    await expect(api.createSource({
      name: 'x', description: '', type: 'auto', subscription_url: 'ftp://x', hwid: '', user_agent: '', headers: '', enabled: true,
    })).rejects.toMatchObject({ code: 'invalid_source', field: 'subscription_url', status: 400 });
  });
});
