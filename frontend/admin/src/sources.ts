// Pure logic of the source catalogue screens: format labels, sync state and
// error texts, count labels, entry filters and the per-source summary shown
// in the Builder. No DOM here; covered by tests/admin-sources.test.ts.
import type { AdminBuilder, AdminSource, BuilderConfigItemInput, BuilderItem, BuilderPreview, FingerprintStatus, SourceCatalogue, SourceEntry, SourceSyncResult } from './api';
import { pluralRu } from './tariffs';

/** ProviderSource.type: the expected response format (database.SourceFormats). */
export const SOURCE_FORMATS = [
  {
    value: 'auto', label: 'Автоопределение',
    hint: 'Формат определяется по ответу провайдера. Подходит почти всегда.',
  },
  {
    value: 'json', label: 'JSON (Xray / 3x-ui)',
    hint: 'JSON-массив полных конфигов Xray (Remnawave, Marzban и т. п.) или серверов 3x-ui.',
  },
  {
    value: 'base64', label: 'Base64-подписка',
    hint: 'Ссылки vless://, vmess://, trojan://, ss://, hysteria2://, закодированные в base64 (формат v2rayN, Happ, v2rayTun).',
  },
  {
    value: 'plain', label: 'Список ссылок',
    hint: 'Те же ссылки без кодирования, по одной на строку.',
  },
  {
    value: 'clash', label: 'Clash / Mihomo (YAML)',
    hint: 'YAML-конфиг с разделом proxies.',
  },
] as const;

export type SourceFormat = typeof SOURCE_FORMATS[number]['value'];

const FORMAT_VALUES: readonly string[] = SOURCE_FORMATS.map(f => f.value);

/** Legacy free-text types never had a runtime meaning: they read as auto. */
export const normalizeFormat = (type: string): SourceFormat =>
  (FORMAT_VALUES.includes(type) ? type : 'auto') as SourceFormat;

export const formatLabel = (type: string) => SOURCE_FORMATS.find(f => f.value === normalizeFormat(type))!.label;

/** Detected format of a sync result (json | base64 | plain | clash). */
export const detectedFormatLabel = (format: string) =>
  SOURCE_FORMATS.find(f => f.value === format && f.value !== 'auto')?.label ?? format;

const countFormat = new Intl.NumberFormat('ru-RU');
export const serversLabel = (n: number) => `${countFormat.format(n)} ${pluralRu(n, 'сервер', 'сервера', 'серверов')}`;
export const countriesLabel = (n: number) => `${countFormat.format(n)} ${pluralRu(n, 'страна', 'страны', 'стран')}`;
export const catalogueSummary = (c: SourceCatalogue) => `${serversLabel(c.entries)} · ${countriesLabel(c.countries)}`;

export type SyncState = 'never' | 'ok' | 'partial' | 'error';

export function syncState(src: Pick<AdminSource, 'last_sync_at' | 'last_sync_status'>): SyncState {
  if (!src.last_sync_at) return 'never';
  switch (src.last_sync_status) {
    case 'ok': return 'ok';
    case 'partial': return 'partial';
    // Pre-catalogue values ("fetch_error", "parse_error", ...) were failures too.
    default: return 'error';
  }
}

export const SYNC_STATE_LABELS: Readonly<Record<SyncState, string>> = {
  never: 'Не синхронизирован',
  ok: 'Синхронизирован',
  partial: 'Синхронизирован частично',
  error: 'Ошибка синхронизации',
};

/** Human text for a stable sync code (service.SourceSyncResult.error). */
export function syncErrorText(code: string): string {
  if (!code) return '';
  const skipped = /^skipped:(\d+)$/.exec(code);
  if (skipped) return `Не удалось разобрать ${serversLabel(Number(skipped[1]))} из ответа.`;
  const http = /^http_(\d{3})$/.exec(code);
  if (http) return `Источник ответил ошибкой HTTP ${http[1]}.`;
  const mismatch = /^format_mismatch:(\w+)$/.exec(code);
  if (mismatch) return `Ответ пришёл в формате «${detectedFormatLabel(mismatch[1])}», а в источнике указан другой. Выберите «Автоопределение» или правильный формат.`;
  switch (code) {
    case 'invalid_config': return 'Неверные параметры источника: URL, заголовки, HWID или User-Agent.';
    case 'timeout': return 'Источник не ответил вовремя.';
    case 'unreachable': return 'Источник недоступен: не удалось подключиться.';
    case 'read_error': return 'Не удалось прочитать ответ источника.';
    case 'too_large': return 'Ответ источника слишком большой.';
    case 'credential_echo': return 'Ответ содержит переданные учётные данные и отклонён.';
    case 'empty_response': return 'Источник вернул пустой ответ.';
    case 'unknown_format': return 'Не удалось определить формат ответа: это не подписка.';
    case 'no_servers': return 'В ответе нет ни одного сервера.';
    case 'internal': return 'Внутренняя ошибка при синхронизации.';
    default: return `Ошибка синхронизации (${code}).`;
  }
}

export interface SyncMessage { tone: 'success' | 'info' | 'error'; text: string }

/** The notice shown after a sync (initial or manual). */
export function syncResultMessage(r: SourceSyncResult): SyncMessage {
  if (r.status === 'error') {
    const kept = r.total > 0 ? ` Сохранён прежний каталог: ${serversLabel(r.total)}.` : ' Каталог пуст.';
    return { tone: 'error', text: `Синхронизация не удалась. ${syncErrorText(r.error)}${kept}` };
  }
  const parts = [
    `Синхронизировано: ${catalogueSummary(r.source.catalogue)}.`,
    `Добавлено ${countFormat.format(r.added)}, обновлено ${countFormat.format(r.updated)}, исчезло ${countFormat.format(r.removed)}.`,
  ];
  if (r.duplicates > 0) parts.push(`Повторов объединено: ${countFormat.format(r.duplicates)}.`);
  if (r.status === 'partial') parts.push(syncErrorText(r.error));
  return { tone: r.status === 'partial' ? 'info' : 'success', text: parts.join(' ') };
}

/** Regional-indicator flag for a two-letter code ('' otherwise). */
export function flagEmoji(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return '';
  return String.fromCodePoint(...[...code].map(c => 0x1F1E6 + c.charCodeAt(0) - 65));
}

export const shortFingerprint = (fp: string) => (fp.length > 12 ? `${fp.slice(0, 12)}…` : fp);

// ---------------------------------------------------------------------------
// Entry filters (source details)

/** country: '' = all, '-' = entries without a country, else an ISO code. */
export interface EntryFilter { country: string; protocol: string; query: string; showAbsent: boolean }

export const EMPTY_FILTER: EntryFilter = { country: '', protocol: '', query: '', showAbsent: false };

export function filterEntries(entries: readonly SourceEntry[], f: EntryFilter): SourceEntry[] {
  const q = f.query.trim().toLocaleLowerCase('ru-RU');
  return entries.filter(e =>
    (f.showAbsent || e.present) &&
    (f.country === '' || (f.country === '-' ? e.country_code === '' : e.country_code === f.country)) &&
    (f.protocol === '' || e.protocol === f.protocol) &&
    (q === '' || e.original_name.toLocaleLowerCase('ru-RU').includes(q)));
}

export const protocolsOf = (entries: readonly SourceEntry[]) =>
  [...new Set(entries.map(e => e.protocol).filter(Boolean))].sort();

// ---------------------------------------------------------------------------
// Builder: sources are merged only there; each keeps its own statistics.

export interface BuilderSourceRow { id: number; name: string; summary: string; state: SyncState; enabled: boolean }

export interface BuilderSourcesSummary {
  rows: BuilderSourceRow[];
  /** Sum of the present entries of the selected ENABLED sources. */
  entries: number;
  /** Distinct country codes across the selected enabled sources. */
  countries: number;
  /** Selected sources that are disabled: /sub and Preview skip them. */
  disabled: number;
}

export function builderSourcesSummary(sources: readonly AdminSource[]): BuilderSourcesSummary {
  const codes = new Set<string>();
  let entries = 0;
  let disabled = 0;
  const rows = sources.map(s => {
    if (s.enabled) {
      entries += s.catalogue.entries;
      for (const c of s.catalogue.by_country) codes.add(c.code);
    } else {
      disabled++;
    }
    return { id: s.id, name: s.name, summary: catalogueSummary(s.catalogue), state: syncState(s), enabled: s.enabled };
  });
  return { rows, entries, countries: codes.size, disabled };
}

// ---------------------------------------------------------------------------
// Builder list card. Counts come from the saved configuration the backend
// returns (service.BuilderView.sources / items) and follow the /sub resolver
// (subserver.builderSourcesToFetch + selectBuilderEntries):
//   source  = a linked provider source (subscription_builder_sources row);
//   rule    = a subscription_builder_items row: a country rule (every server
//             of one country, resolved on each build) or a node rule (one
//             concrete server of the catalogue);
//   no rules         → every server of every linked source is served;
//   no linked source → nothing is served;
//   rules present but none enabled on a linked source → nothing is served.

export interface BuilderCardSummary {
  sources: number;
  rules: number;
  /** Rules switched off: /sub skips them. */
  disabledRules: number;
  /** Enabled rules whose source is not linked: /sub ignores them. */
  unlinkedRules: number;
  /** Country codes of the applied country rules, rule order, unique ('' = without a country). */
  countries: string[];
  /** Applied node rules (one concrete server each). */
  nodes: number;
  /** "v3 · 2 источника · 4 правила (1 выкл.)" */
  meta: string;
  /** What /sub serves with this configuration. */
  composition: string;
  /** The configuration serves nothing. */
  empty: boolean;
}

type CardBuilder = Pick<AdminBuilder, 'version' | 'sources' | 'items'>;

/**
 * Rules of the editor as the configuration save expects them: in display
 * order (the index is the position), local placeholders (id <= 0) as new rules
 * (id 0), only the fields of the rule kind.
 */
export function builderConfigItems(items: readonly BuilderItem[]): BuilderConfigItemInput[] {
  return items.map(it => ({
    id: it.id > 0 ? it.id : 0,
    kind: it.kind,
    source_id: it.source_id,
    country_code: it.kind === 'country' ? (it.country_code ?? '') : '',
    fingerprint: it.kind === 'node' ? (it.fingerprint ?? '') : '',
    original_name: it.kind === 'node' ? (it.original_name ?? '') : '',
    custom_name: it.custom_name ?? null,
    description: it.description ?? '',
    enabled: it.enabled,
  }));
}

export function builderCardSummary(b: CardBuilder): BuilderCardSummary {
  const sources = b.sources ?? [];
  const items = b.items ?? [];
  const linked = new Set(sources.map(s => s.source_id));
  const countries: string[] = [];
  let disabledRules = 0, unlinkedRules = 0, nodes = 0;
  for (const it of [...items].sort((x, y) => x.position - y.position || x.id - y.id)) {
    if (!it.enabled) { disabledRules++; continue; }
    if (!linked.has(it.source_id)) { unlinkedRules++; continue; }
    if (it.kind === 'node') { nodes++; continue; }
    const code = (it.country_code ?? '').toUpperCase();
    if (!countries.includes(code)) countries.push(code);
  }
  const off = [
    disabledRules ? `${countFormat.format(disabledRules)} выкл.` : '',
    unlinkedRules ? `${countFormat.format(unlinkedRules)} без источника` : '',
  ].filter(Boolean).join(', ');
  const meta = [
    `v${b.version}`,
    `${countFormat.format(sources.length)} ${pluralRu(sources.length, 'источник', 'источника', 'источников')}`,
    `${countFormat.format(items.length)} ${pluralRu(items.length, 'правило', 'правила', 'правил')}${off ? ` (${off})` : ''}`,
  ].join(' · ');

  let composition: string;
  let empty = false;
  if (sources.length === 0) {
    composition = 'Источники не подключены: подписка не соберётся.';
    empty = true;
  } else if (items.length === 0) {
    composition = 'Без правил: все серверы подключённых источников.';
  } else if (countries.length === 0 && nodes === 0) {
    composition = 'Ни одно правило не применяется: подписка не соберётся.';
    empty = true;
  } else {
    const parts: string[] = [];
    if (countries.length) parts.push(`Страны: ${countries.map(c => c ? countryLabel(c) : 'без страны').join(', ')}`);
    if (nodes) parts.push(`отдельных серверов: ${countFormat.format(nodes)}`);
    composition = parts.join(' · ');
  }
  return { sources: sources.length, rules: items.length, disabledRules, unlinkedRules, countries, nodes, meta, composition, empty };
}

// ---------------------------------------------------------------------------
// Builder rules against the real catalogue. The backend (Preview and /sub)
// stays authoritative; these helpers only explain a rule before it is saved
// and mirror database.CountryMatches / MatchOriginalName.

export const COUNTRY_CODE = /^[A-Z]{2}$/;

/** Present servers of one country in a source's catalogue (0 when absent). */
export function countryCount(src: Pick<AdminSource, 'catalogue'> | undefined, code: string): number {
  const want = code.trim().toUpperCase();
  return src?.catalogue.by_country.find(c => c.code.toUpperCase() === want)?.count ?? 0;
}

/** "🇩🇪 DE" (or just the code when it is not a flaggable pair). */
export const countryLabel = (code: string) => `${flagEmoji(code)} ${code}`.trim();

// database.originalNameTiers: exact, then %XX decoding, then "+" as space.
const safeDecode = (s: string, plusAsSpace: boolean) => {
  try { return decodeURIComponent(plusAsSpace ? s.replace(/\+/g, ' ') : s); } catch { return s; }
};
const NAME_TIERS: readonly ((s: string) => string)[] = [
  s => s,
  s => safeDecode(s, false).trim(),
  s => safeDecode(s, true).trim(),
];

/** database.MatchOriginalName: indexes of names matching want. */
export function matchOriginalName(want: string, names: readonly string[]): number[] {
  if (want === '') return [];
  for (const norm of NAME_TIERS) {
    const target = norm(want);
    if (target === '') continue;
    const out: number[] = [];
    names.forEach((n, i) => { if (norm(n) === target) out.push(i); });
    if (out.length > 0) return out;
  }
  return [];
}

export interface NodeResolution { status: FingerprintStatus; entry: SourceEntry | null }

/** database.ResolveNodeItem against present entries: fingerprint, then a unique name. */
export function resolveNode(fingerprint: string, originalName: string, entries: readonly SourceEntry[]): NodeResolution {
  const present = entries.filter(e => e.present);
  if (fingerprint) {
    const hit = present.find(e => e.fingerprint === fingerprint);
    if (hit) return { status: 'matched', entry: hit };
  }
  const matches = matchOriginalName(originalName, present.map(e => e.original_name));
  if (matches.length === 1) return { status: 'fallback', entry: present[matches[0]] };
  return { status: matches.length === 0 ? 'missing' : 'conflict', entry: null };
}

export const RESOLUTION_LABELS: Readonly<Record<FingerprintStatus, string>> = {
  matched: 'Найден', fallback: 'Найден по имени', missing: 'Не найден', conflict: 'Неоднозначно',
};

type RuleFields = Pick<BuilderItem, 'kind' | 'source_id' | 'country_code' | 'fingerprint' | 'original_name' | 'custom_name' | 'enabled'>;

export interface RuleSummary {
  title: string;
  detail: string;
  /** Why the rule serves nothing right now ('' when it works). */
  warning: string;
}

/**
 * How a rule reads in the Builder editor: which source it takes servers
 * from and what it yields by the source's current catalogue.
 */
export function ruleSummary(item: RuleFields, sources: readonly AdminSource[], selectedIds: readonly number[]): RuleSummary {
  const src = sources.find(s => s.id === item.source_id);
  const srcName = src ? `«${src.name}»` : `источника #${item.source_id}`;
  let warning = '';
  if (!selectedIds.includes(item.source_id)) warning = 'Источник не подключён к построителю: правило не применяется.';
  else if (src && !src.enabled) warning = 'Источник отключён: правило пропускается.';
  if (item.kind === 'country') {
    const n = countryCount(src, item.country_code);
    if (!warning && src && n === 0) warning = 'В каталоге источника сейчас нет серверов этой страны.';
    return {
      title: item.custom_name || countryLabel(item.country_code),
      detail: `Все серверы страны ${countryLabel(item.country_code)} из ${srcName} · сейчас ${serversLabel(n)}`,
      warning,
    };
  }
  return {
    title: item.custom_name || item.original_name || shortFingerprint(item.fingerprint) || '—',
    detail: `Сервер из ${srcName}${item.custom_name && item.original_name ? ` · в источнике: ${item.original_name}` : ''}`,
    warning,
  };
}

/** Entries of the node picker: present, filtered, capped for rendering. */
export function pickableEntries(entries: readonly SourceEntry[], query: string, country: string, limit = 200) {
  const all = filterEntries(entries, { country, protocol: '', query, showAbsent: false });
  return { shown: all.slice(0, limit), total: all.length };
}

export interface PreviewSummary {
  /** Output servers (matched + fallback), as /sub would serve them now. */
  servers: number;
  countries: number;
  /** Output servers per source id, in first-seen order. */
  bySource: { sourceId: number; count: number }[];
}

export function previewSummary(preview: Pick<BuilderPreview, 'items'>): PreviewSummary {
  const codes = new Set<string>();
  const per = new Map<number, number>();
  let servers = 0;
  for (const it of preview.items) {
    if (!it.entry || (it.status !== 'matched' && it.status !== 'fallback')) continue;
    servers++;
    if (it.entry.country_code) codes.add(it.entry.country_code);
    per.set(it.source_id, (per.get(it.source_id) ?? 0) + 1);
  }
  return { servers, countries: codes.size, bySource: [...per].map(([sourceId, count]) => ({ sourceId, count })) };
}
