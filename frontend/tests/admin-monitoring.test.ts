import { describe, expect, it, vi } from 'vitest';
import {
  AdminApi, monitorHistoryPath, type MonitorBuilderSummary, type MonitorCountry, type MonitorHistory, type MonitorNode,
} from '../admin/src/api';
import {
  EMPTY_MONITOR_FILTER, endReasonLabel, errorLabel, filterCountries, formatAgo, formatAvailability, formatDuration,
  formatLatency, isFiltered, latencyChart, monitorCountryLabel, protocolsOf, transitionText, transportLabel,
} from '../admin/src/monitoring';

// Pure logic of the network monitor screens (admin/src/monitoring.ts) and the
// monitor part of the admin API client, against the wire shapes of
// netmon.Overview / BuilderDetail / History (web.adminAPI.routeMonitoring).

const T0 = '2026-09-30T12:00:00Z';
const NOW = Date.parse(T0);

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

const summary = (over: Partial<MonitorBuilderSummary> = {}): MonitorBuilderSummary => ({
  id: 3, name: 'Основной', enabled: true, status: 'degraded', countries_total: 2, countries_up: 1, countries_degraded: 0,
  countries_down: 1, nodes_total: 3, nodes_up: 2, nodes_down: 1, nodes_unknown: 0, avg_latency_ms: 42, last_checked_at: T0,
  last_transition: { country_code: 'NL', event: 'down', at: T0 }, source_errors: 0, unresolved: 0, ...over,
});

let nextTarget = 1;
const node = (over: Partial<MonitorNode> = {}): MonitorNode => ({
  target_id: nextTarget++, name: 'srv', source_id: 1, source_name: 'Provider A', protocol: 'vless', host: 'de1.example',
  port: 443, security: 'reality', transport: 'tcp', probe: 'tls', status: 'up', status_since: T0, latency_ms: 40,
  last_checked_at: T0, last_up_at: T0, last_down_at: null, last_error: '', shared_with: 0, ...over,
});

const country = (code: string, status: MonitorCountry['status'], nodes: MonitorNode[]): MonitorCountry => ({
  country_code: code, status, status_since: T0, latency_ms: 40, nodes_total: nodes.length,
  nodes_up: nodes.filter(n => n.status === 'up').length, nodes_down: nodes.filter(n => n.status === 'down').length,
  last_checked_at: T0, last_up_at: T0, last_down_at: null, nodes,
});

const COUNTRIES = [
  country('DE', 'up', [node({ protocol: 'vless' }), node({ protocol: 'trojan' })]),
  country('NL', 'down', [node({ protocol: 'hysteria2', probe: 'quic', status: 'down', last_error: 'timeout' })]),
  country('', 'unknown', [node({ protocol: 'shadowsocks', probe: 'tcp', status: 'unknown' })]),
];

const history = (over: Partial<MonitorHistory> = {}): MonitorHistory => ({
  scope: 'builder', period: '24h', from: '2026-09-29T12:00:00Z', to: T0, step_seconds: 900,
  summary: { outages: 1, down_seconds: 600, degraded_seconds: 0, availability: null, checks: 96, failures: 10 },
  outages: [{
    id: 7, scope: 'country', kind: 'down', country_code: 'NL', target_id: null, started_at: '2026-09-30T11:50:00Z',
    ended_at: null, duration_seconds: 600, ongoing: true, end_reason: '', error_code: 'timeout',
  }],
  latency: [{ at: '2026-09-30T11:45:00Z', checks: 4, failures: 1, avg_ms: 40, min_ms: 30, max_ms: 50 }],
  ...over,
});

describe('monitor formatting', () => {
  it('formats "ago" labels relative to now', () => {
    const ago = (seconds: number) => formatAgo(new Date(NOW - seconds * 1000).toISOString(), NOW);
    expect(ago(3)).toBe('только что');
    expect(ago(42)).toBe('42 с назад');
    expect(ago(5 * 60)).toBe('5 мин назад');
    expect(ago(3 * 3600)).toBe('3 ч назад');
    expect(ago(2 * 86400)).toBe('2 дня назад');
    expect(ago(-30)).toBe('только что'); // clock skew never shows a future time
    expect(formatAgo(null, NOW)).toBe('Не проверялся');
    expect(formatAgo(null, NOW, 'Не было')).toBe('Не было');
  });

  it('formats outage durations', () => {
    expect(formatDuration(45)).toBe('45 с');
    expect(formatDuration(720)).toBe('12 мин');
    expect(formatDuration(7200)).toBe('2 ч');
    expect(formatDuration(7500)).toBe('2 ч 5 мин');
    expect(formatDuration(86400)).toBe('1 дн');
    expect(formatDuration(3 * 86400 + 4 * 3600)).toBe('3 дн 4 ч');
  });

  it('never rounds a real outage up to 100 % availability', () => {
    expect(formatAvailability(null)).toBe('—');
    expect(formatAvailability(100)).toBe('100 %');
    expect(formatAvailability(99.999)).toBe('99,99 %');
    expect(formatAvailability(99.3)).toBe('99,30 %'); // 99.3 * 100 is 9929.999… in binary
    expect(formatAvailability(0)).toBe('0,00 %');
  });

  it('labels latency, countries, errors, transports and transitions', () => {
    expect(formatLatency(null)).toBe('—');
    expect(formatLatency(87)).toBe('87 мс');
    expect(monitorCountryLabel('')).toBe('Без страны');
    expect(monitorCountryLabel('DE')).toBe('DE · Германия');
    expect(monitorCountryLabel('weird')).toBe('weird');
    expect(errorLabel('')).toBe('');
    expect(errorLabel('timeout')).toBe('Нет ответа (таймаут)');
    expect(errorLabel('new_code')).toBe('new_code');
    expect(transportLabel({ transport: 'tcp', security: 'reality' })).toBe('tcp · reality');
    expect(transportLabel({ transport: 'ws', security: 'none' })).toBe('ws');
    expect(transitionText(null)).toBe('Переходов не было');
    expect(transitionText({ country_code: 'DE', event: 'down', at: T0 })).toBe('DE · Германия: недоступна');
    expect(transitionText({ country_code: '', event: 'up', at: T0 })).toBe('Без страны: восстановлена');
    expect(endReasonLabel({ ongoing: true, end_reason: '' })).toBe('Продолжается');
    expect(endReasonLabel({ ongoing: false, end_reason: 'recovered' })).toBe('Восстановлено');
  });
});

describe('monitor filters', () => {
  it('filters countries by country, status and protocol', () => {
    expect(isFiltered(EMPTY_MONITOR_FILTER)).toBe(false);
    expect(filterCountries(COUNTRIES, EMPTY_MONITOR_FILTER)).toHaveLength(3);
    expect(filterCountries(COUNTRIES, { ...EMPTY_MONITOR_FILTER, country: '-' }).map(c => c.country_code)).toEqual(['']);
    expect(filterCountries(COUNTRIES, { ...EMPTY_MONITOR_FILTER, country: 'NL' }).map(c => c.country_code)).toEqual(['NL']);
    expect(filterCountries(COUNTRIES, { ...EMPTY_MONITOR_FILTER, status: 'down' }).map(c => c.country_code)).toEqual(['NL']);
    const trojan = filterCountries(COUNTRIES, { ...EMPTY_MONITOR_FILTER, protocol: 'trojan' });
    expect(trojan.map(c => c.country_code)).toEqual(['DE']);
    expect(trojan[0].nodes.map(n => n.protocol)).toEqual(['trojan']);
    expect(COUNTRIES[0].nodes).toHaveLength(2); // the source data is not mutated
    expect(filterCountries(COUNTRIES, { country: 'DE', status: 'down', protocol: '' })).toEqual([]);
  });

  it('lists protocols by label', () => {
    expect(protocolsOf(COUNTRIES)).toEqual(['hysteria2', 'shadowsocks', 'trojan', 'vless']);
  });
});

describe('latency chart', () => {
  it('scales the average per step and marks steps with failures', () => {
    const from = '2026-09-30T00:00:00Z';
    const at = (s: number) => new Date(Date.parse(from) + s * 1000).toISOString();
    const g = latencyChart([
      { at: at(0), checks: 2, failures: 0, avg_ms: 10, min_ms: 10, max_ms: 10 },
      { at: at(50), checks: 2, failures: 1, avg_ms: 5, min_ms: 5, max_ms: 5 },
      { at: at(100), checks: 2, failures: 2, avg_ms: null, min_ms: null, max_ms: null },
    ], from, at(100), 100, 10);
    expect(g).toEqual({ line: '0,0 50,5', failures: [50, 100], maxMs: 10 });
  });

  it('is empty without points', () => {
    expect(latencyChart([], T0, T0, 100, 10)).toEqual({ line: '', failures: [], maxMs: 0 });
  });
});

describe('monitor API client', () => {
  const setup = () => {
    const transport = vi.fn<typeof fetch>();
    return { transport, api: new AdminApi(transport) };
  };

  it('reads the overview', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({
      enabled: true, generated_at: T0, interval_seconds: 60, down_after: 3, last_round_at: T0, last_refresh_at: null,
      builders: [summary(), summary({ id: 4, last_transition: null, avg_latency_ms: null, last_checked_at: null })],
    }));
    const overview = await api.monitoring();
    expect(overview.builders.map(b => b.id)).toEqual([3, 4]);
    expect(overview.builders[1].last_transition).toBeNull();
    const [path, init] = transport.mock.calls[0];
    expect(path).toBe('/admin/api/monitoring');
    expect(init?.method ?? 'GET').toBe('GET');
  });

  it('rejects a malformed overview', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({
      enabled: true, generated_at: T0, interval_seconds: 60, down_after: 3, last_round_at: null, last_refresh_at: null,
      builders: [summary({ status: 'broken' as 'up' })],
    }));
    await expect(api.monitoring()).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('reads a builder and checks that it is the requested one', async () => {
    const { transport, api } = setup();
    const detail = { generated_at: T0, interval_seconds: 60, last_round_at: T0, builder: summary(), countries: COUNTRIES };
    transport.mockResolvedValueOnce(json(detail));
    const got = await api.monitoringBuilder(3);
    expect(got.countries.map(c => c.country_code)).toEqual(['DE', 'NL', '']);
    expect(transport.mock.calls[0][0]).toBe('/admin/api/monitoring/builders/3');

    transport.mockResolvedValueOnce(json(detail));
    await expect(api.monitoringBuilder(9)).rejects.toMatchObject({ code: 'invalid_response' });
    transport.mockResolvedValueOnce(json({ ...detail, countries: [{ ...COUNTRIES[0], nodes: [{ ...COUNTRIES[0].nodes[0], port: 70000 }] }] }));
    await expect(api.monitoringBuilder(3)).rejects.toMatchObject({ code: 'invalid_response' });
    await expect(api.monitoringBuilder(0)).rejects.toMatchObject({ status: 404 });
    expect(transport).toHaveBeenCalledTimes(3);
  });

  it('never accepts credentials it was not given: extra wire fields are dropped', async () => {
    const { transport, api } = setup();
    const leaky = { ...COUNTRIES[0].nodes[0], uuid: 'secret', password: 'secret' };
    transport.mockResolvedValueOnce(json({
      generated_at: T0, interval_seconds: 60, last_round_at: null, builder: summary(),
      countries: [{ ...COUNTRIES[0], nodes: [leaky] }],
    }));
    const got = await api.monitoringBuilder(3);
    expect(Object.keys(got.countries[0].nodes[0])).not.toContain('uuid');
    expect(Object.keys(got.countries[0].nodes[0])).not.toContain('password');
  });

  it('builds history queries for a builder, a country and a server', () => {
    expect(monitorHistoryPath({ builderId: 3, country: null, targetId: null, period: '24h' }))
      .toBe('/admin/api/monitoring/history?builder_id=3&period=24h');
    expect(monitorHistoryPath({ builderId: 3, country: '', targetId: null, period: '7d' }))
      .toBe('/admin/api/monitoring/history?builder_id=3&country=&period=7d');
    expect(monitorHistoryPath({ builderId: 3, country: 'DE', targetId: null, period: '30d' }))
      .toBe('/admin/api/monitoring/history?builder_id=3&country=DE&period=30d');
    expect(monitorHistoryPath({ builderId: null, country: null, targetId: 9, period: '24h' }))
      .toBe('/admin/api/monitoring/history?target_id=9&period=24h');
  });

  it('reads history and rejects a different period or a broken point', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(history()));
    const got = await api.monitoringHistory({ builderId: 3, country: null, targetId: null, period: '24h' });
    expect(got.outages[0].ongoing).toBe(true);
    expect(got.summary.availability).toBeNull();

    transport.mockResolvedValueOnce(json(history()));
    await expect(api.monitoringHistory({ builderId: 3, country: null, targetId: null, period: '7d' }))
      .rejects.toMatchObject({ code: 'invalid_response' });
    transport.mockResolvedValueOnce(json(history({ latency: [{ at: T0, checks: 1, failures: 2, avg_ms: 1, min_ms: 1, max_ms: 1 }] })));
    await expect(api.monitoringHistory({ builderId: 3, country: null, targetId: null, period: '24h' }))
      .rejects.toMatchObject({ code: 'invalid_response' });
    transport.mockResolvedValueOnce(json(history({ summary: { ...history().summary, availability: 101 } })));
    await expect(api.monitoringHistory({ builderId: 3, country: null, targetId: null, period: '24h' }))
      .rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('surfaces a backend without the monitor as 503', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ error: 'service_unavailable' }, 503));
    await expect(api.monitoring()).rejects.toMatchObject({ status: 503 });
  });
});
