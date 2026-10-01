// Pure logic of the network monitor screens ("Мониторинг"): status and error
// labels, time/duration formatting, filters and the latency chart geometry.
// No DOM here; covered by tests/admin-monitoring.test.ts.
import type { MonitorCountry, MonitorLatencyPoint, MonitorNode, MonitorStatus, MonitorTransition } from './api';
import { pluralRu } from './tariffs';

const countFormat = new Intl.NumberFormat('ru-RU');

export const STATUS_LABELS: Readonly<Record<MonitorStatus, string>> = {
  up: 'Работает',
  degraded: 'Частично',
  down: 'Недоступен',
  unknown: 'Нет данных',
  disabled: 'Отключён',
};

/** Badge modifier of a status (style.css .badge-*). */
export const STATUS_BADGES: Readonly<Record<MonitorStatus, string>> = {
  up: 'badge badge-active',
  degraded: 'badge badge-warn',
  down: 'badge badge-danger',
  unknown: 'badge',
  disabled: 'badge',
};

/** Statuses offered by the status filter (a builder may also be disabled). */
export const FILTER_STATUSES: readonly MonitorStatus[] = ['up', 'degraded', 'down', 'unknown'];

/** netmon.ErrCode*: stable, credential-free failure codes. */
const ERROR_LABELS: Readonly<Record<string, string>> = {
  dns: 'Имя не разрешается (DNS)',
  timeout: 'Нет ответа (таймаут)',
  refused: 'Соединение отклонено',
  unreachable: 'Сеть недоступна',
  reset: 'Соединение сброшено',
  tls_handshake: 'TLS-handshake не прошёл',
  quic_handshake: 'QUIC-handshake не прошёл',
  obfs_key_missing: 'Нет ключа обфускации',
  unsupported: 'Протокол не проверяется',
  network_error: 'Сетевая ошибка',
};

export const errorLabel = (code: string) => (code ? ERROR_LABELS[code] ?? code : '');

const PROTOCOL_LABELS: Readonly<Record<string, string>> = {
  vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', shadowsocks: 'Shadowsocks', hysteria2: 'Hysteria2',
  hysteria: 'Hysteria', tuic: 'TUIC',
};

export const protocolLabel = (protocol: string) => PROTOCOL_LABELS[protocol] ?? protocol;

/** What the probe of a server proves (doc/network-monitoring.md). */
const PROBE_LABELS: Readonly<Record<string, string>> = {
  tcp: 'TCP connect',
  tls: 'TLS handshake',
  quic: 'QUIC handshake',
  unsupported: 'Не проверяется',
};

export const probeLabel = (probe: string) => PROBE_LABELS[probe] ?? probe;

/** Transport and security of a server, e.g. "tcp · reality". */
export function transportLabel(node: Pick<MonitorNode, 'transport' | 'security'>): string {
  const parts = [node.transport];
  if (node.security && node.security !== 'none') parts.push(node.security);
  return parts.filter(Boolean).join(' · ');
}

let regionNames: Intl.DisplayNames | null = null;
function regionName(code: string): string {
  try {
    regionNames ??= new Intl.DisplayNames(['ru'], { type: 'region' });
    return regionNames.of(code) ?? '';
  } catch {
    return '';
  }
}

/** "DE · Германия"; '' is the group of servers without a country. */
export function monitorCountryLabel(code: string): string {
  if (!code) return 'Без страны';
  if (!/^[A-Z]{2}$/.test(code)) return code;
  const name = regionName(code);
  return name && name !== code ? `${code} · ${name}` : code;
}

export const formatLatency = (ms: number | null) => (ms === null ? '—' : `${countFormat.format(ms)} мс`);

/** "12 с назад", "5 мин назад", …; null means never. */
export function formatAgo(iso: string | null, now: number, never = 'Не проверялся'): string {
  if (!iso) return never;
  const seconds = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (seconds < 5) return 'только что';
  if (seconds < 60) return `${seconds} с назад`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} мин назад`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} ч назад`;
  const days = Math.floor(hours / 24);
  return `${countFormat.format(days)} ${pluralRu(days, 'день', 'дня', 'дней')} назад`;
}

/** Outage length: "45 с", "12 мин", "2 ч 5 мин", "3 дн 4 ч". */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return `${s} с`;
  const minutes = Math.floor(s / 60);
  if (minutes < 60) return `${minutes} мин`;
  const hours = Math.floor(minutes / 60);
  const restMin = minutes % 60;
  if (hours < 24) return restMin ? `${hours} ч ${restMin} мин` : `${hours} ч`;
  const days = Math.floor(hours / 24);
  const restH = hours % 24;
  return restH ? `${days} дн ${restH} ч` : `${days} дн`;
}

export function formatAvailability(percent: number | null): string {
  if (percent === null) return '—';
  if (percent >= 100) return '100 %';
  // Never round a real outage up to 100 %. The epsilon only absorbs binary
  // representation error (99.3 * 100 = 9929.999…), it never rounds up a real value.
  const floored = Math.floor(percent * 100 + 1e-9) / 100;
  return `${floored.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} %`;
}

const TRANSITION_TEXT: Readonly<Record<string, string>> = {
  down: 'недоступна',
  degraded: 'работает частично',
  up: 'восстановлена',
  removed: 'больше не выдаётся',
};

/** "DE · Германия: недоступна". */
export function transitionText(t: MonitorTransition | null): string {
  if (!t) return 'Переходов не было';
  return `${monitorCountryLabel(t.country_code)}: ${TRANSITION_TEXT[t.event] ?? t.event}`;
}

export const outageKindLabel = (kind: string) => (kind === 'degraded' ? 'Частичный сбой' : 'Недоступность');

const END_REASONS: Readonly<Record<string, string>> = {
  recovered: 'Восстановлено',
  changed: 'Сменился тип сбоя',
  removed: 'Снято с выдачи',
};

export const endReasonLabel = (outage: { ongoing: boolean; end_reason: string }) =>
  (outage.ongoing ? 'Продолжается' : END_REASONS[outage.end_reason] ?? outage.end_reason);

// ---------------------------------------------------------------------------
// Filters of the builder page (in memory, never in the URL)

export interface MonitorFilter {
  /** '' = every country; '-' = servers without a country. */
  readonly country: string;
  readonly status: MonitorStatus | '';
  readonly protocol: string;
}

export const EMPTY_MONITOR_FILTER: MonitorFilter = { country: '', status: '', protocol: '' };

export const isFiltered = (f: MonitorFilter) => f.country !== '' || f.status !== '' || f.protocol !== '';

/**
 * Country and status filter the countries; protocol filters the servers of a
 * country, and a country without a matching server is hidden.
 */
export function filterCountries(countries: readonly MonitorCountry[], f: MonitorFilter): MonitorCountry[] {
  const out: MonitorCountry[] = [];
  for (const c of countries) {
    if (f.country && (f.country === '-' ? c.country_code !== '' : c.country_code !== f.country)) continue;
    if (f.status && c.status !== f.status) continue;
    const nodes = f.protocol ? c.nodes.filter(n => n.protocol === f.protocol) : c.nodes;
    if (nodes.length === 0) continue;
    out.push(nodes === c.nodes ? c : { ...c, nodes });
  }
  return out;
}

export function protocolsOf(countries: readonly MonitorCountry[]): string[] {
  const set = new Set<string>();
  for (const c of countries) for (const n of c.nodes) set.add(n.protocol);
  return [...set].sort((a, b) => protocolLabel(a).localeCompare(protocolLabel(b)));
}

// ---------------------------------------------------------------------------
// Latency chart: one polyline of the average per step, scaled into a box.

export interface ChartGeometry {
  /** SVG polyline "x,y x,y …" of the steps with a latency. */
  readonly line: string;
  /** Steps with at least one failed check, as x positions. */
  readonly failures: readonly number[];
  readonly maxMs: number;
}

export function latencyChart(points: readonly MonitorLatencyPoint[], from: string, to: string, width: number, height: number): ChartGeometry {
  const start = Date.parse(from);
  const span = Math.max(1, Date.parse(to) - start);
  let maxMs = 0;
  for (const p of points) if (p.avg_ms !== null && p.avg_ms > maxMs) maxMs = p.avg_ms;
  const scaleMax = maxMs > 0 ? maxMs : 1;
  const x = (iso: string) => Math.min(width, Math.max(0, ((Date.parse(iso) - start) / span) * width));
  const coords: string[] = [];
  const failures: number[] = [];
  for (const p of points) {
    const px = Math.round(x(p.at) * 10) / 10;
    if (p.avg_ms !== null) {
      const py = Math.round((height - (p.avg_ms / scaleMax) * height) * 10) / 10;
      coords.push(`${px},${py}`);
    }
    if (p.failures > 0) failures.push(px);
  }
  return { line: coords.join(' '), failures, maxMs };
}
