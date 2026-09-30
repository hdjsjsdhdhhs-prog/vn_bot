import './style.css';
import {
  AdminApi, ApiError, errorText, limits, newRequestKey, RENEW_MAX_DAYS,
  type AdminNode, type AdminPlan, type AdminSubscription, type Dashboard, type Mutation, type MutationAction,
  type MutationOutcome, type SubscriptionStatus, type UserDetail, type UsersPage, type UsersQuery,
  type AdminSource, type AdminBuilder, type BuilderItem, type SourceEntry, type SourceSyncResult,
  type CreateSourceInput, type UpdateSourceInput,
  type CreateBuilderInput, type UpdateBuilderInput, type UpsertBuilderItemInput, type SetBuilderInput,
  TARIFF_LIMITS, type AdminTariff, type TariffPlan, type TariffOutcome,
  JOURNAL_EVENT_TYPES, PLAN_KINDS, type JournalEvent, type JournalEventType, type JournalPage, type JournalQuery,
  type JournalState, type PlanKind,
} from './api';
import {
  BADGE_PRESETS, DURATION_PRESETS, SERVER_FIELDS, TARIFF_CURRENCIES, botButtonLabel, catalogueOrder, durationLabel,
  featuresFromText, formFromTariff, formatTariffPrice, isRetired, moveItem, parsePriceInput, pluralRu, reorderIds, sameForm,
  termsChanged, validateTariffForm, type FieldErrors, type TariffField, type TariffForm,
} from './tariffs';
import {
  COUNTRY_CODE, EMPTY_FILTER, RESOLUTION_LABELS, SOURCE_FORMATS, SYNC_STATE_LABELS, builderSourcesSummary,
  catalogueSummary, countriesLabel, countryCount, countryLabel, detectedFormatLabel, filterEntries, flagEmoji,
  formatLabel, normalizeFormat, pickableEntries, previewSummary, protocolsOf, resolveNode, ruleSummary,
  serversLabel, shortFingerprint, syncErrorText, syncResultMessage, syncState, type EntryFilter, type SyncState,
} from './sources';

// ---------------------------------------------------------------------------
// Icons. Geometry from Lucide (ISC License, https://lucide.dev), vendored so a
// handful of glyphs does not add a runtime dependency. One family, one stroke.

type IconName = 'overview' | 'users' | 'tariffs' | 'up' | 'audit' | 'sources' | 'builders' | 'logout' | 'menu' | 'close' | 'alert' | 'info' | 'eye' | 'eyeOff' | 'refresh'
  | 'search' | 'back' | 'prev' | 'next' | 'chevron' | 'check' | 'plus' | 'trash' | 'drag' | 'edit';
type Shape = readonly ['path' | 'circle' | 'rect', Readonly<Record<string, string>>];

const ICONS: Record<IconName, readonly Shape[]> = {
  overview: [
    ['rect', { x: '3', y: '3', width: '7', height: '9', rx: '1' }],
    ['rect', { x: '14', y: '3', width: '7', height: '5', rx: '1' }],
    ['rect', { x: '14', y: '12', width: '7', height: '9', rx: '1' }],
    ['rect', { x: '3', y: '16', width: '7', height: '5', rx: '1' }],
  ],
  users: [
    ['path', { d: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2' }],
    ['circle', { cx: '9', cy: '7', r: '4' }],
    ['path', { d: 'M22 21v-2a4 4 0 0 0-3-3.87' }],
    ['path', { d: 'M16 3.13a4 4 0 0 1 0 7.75' }],
  ],
  tariffs: [
    ['path', { d: 'M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z' }],
    ['circle', { cx: '7.5', cy: '7.5', r: '.5' }],
  ],
  up: [['path', { d: 'm18 15-6-6-6 6' }]],
  audit: [
    ['path', { d: 'M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8' }],
    ['path', { d: 'M3 3v5h5' }],
    ['path', { d: 'M12 7v5l4 2' }],
  ],
  logout: [
    ['path', { d: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4' }],
    ['path', { d: 'm16 17 5-5-5-5' }],
    ['path', { d: 'M21 12H9' }],
  ],
  menu: [['path', { d: 'M4 6h16' }], ['path', { d: 'M4 12h16' }], ['path', { d: 'M4 18h16' }]],
  close: [['path', { d: 'M18 6 6 18' }], ['path', { d: 'm6 6 12 12' }]],
  alert: [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'M12 8v4' }], ['path', { d: 'M12 16h.01' }]],
  info: [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'M12 16v-4' }], ['path', { d: 'M12 8h.01' }]],
  eye: [['path', { d: 'M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z' }], ['circle', { cx: '12', cy: '12', r: '3' }]],
  eyeOff: [
    ['path', { d: 'M9.88 9.88a3 3 0 1 0 4.24 4.24' }],
    ['path', { d: 'M10.73 5.08A10.43 10.43 0 0 1 12 5c7 0 10 7 10 7a13.16 13.16 0 0 1-1.67 2.68' }],
    ['path', { d: 'M6.61 6.61A13.526 13.526 0 0 0 2 12s3 7 10 7a9.74 9.74 0 0 0 5.39-1.61' }],
    ['path', { d: 'm2 2 20 20' }],
  ],
  refresh: [['path', { d: 'M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8' }], ['path', { d: 'M21 3v5h-5' }]],
  search: [['circle', { cx: '11', cy: '11', r: '8' }], ['path', { d: 'm21 21-4.3-4.3' }]],
  back: [['path', { d: 'm12 19-7-7 7-7' }], ['path', { d: 'M19 12H5' }]],
  prev: [['path', { d: 'm15 18-6-6 6-6' }]],
  next: [['path', { d: 'm9 18 6-6-6-6' }]],
  chevron: [['path', { d: 'm6 9 6 6 6-6' }]],
  check: [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'm9 12 2 2 4-4' }]],
  sources: [
    ['path', { d: 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z' }],
  ],
  builders: [
    ['path', { d: 'M3 9h18' }],
    ['path', { d: 'M3 15h18' }],
    ['path', { d: 'M9 3v18' }],
    ['path', { d: 'M15 3v18' }],
  ],
  plus: [['path', { d: 'M12 5v14' }], ['path', { d: 'M5 12h14' }]],
  trash: [
    ['path', { d: 'M3 6h18' }],
    ['path', { d: 'M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6' }],
    ['path', { d: 'M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2' }],
  ],
  drag: [['path', { d: 'M9 5h2' }], ['path', { d: 'M9 12h2' }], ['path', { d: 'M9 19h2' }], ['path', { d: 'M13 5h2' }], ['path', { d: 'M13 12h2' }], ['path', { d: 'M13 19h2' }]],
  edit: [['path', { d: 'M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7' }], ['path', { d: 'M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z' }]],
};

const SVG_NS = 'http://www.w3.org/2000/svg';

function icon(name: IconName, size: 16 | 20 = 16): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, 'svg');
  const attrs: Record<string, string> = {
    class: 'icon', viewBox: '0 0 24 24', width: String(size), height: String(size),
    fill: 'none', stroke: 'currentColor', 'stroke-width': '1.5',
    'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true', focusable: 'false',
  };
  for (const [key, value] of Object.entries(attrs)) svg.setAttribute(key, value);
  for (const [tag, shapeAttrs] of ICONS[name]) {
    const shape = document.createElementNS(SVG_NS, tag);
    for (const [key, value] of Object.entries(shapeAttrs)) shape.setAttribute(key, value);
    svg.append(shape);
  }
  return svg;
}

// ---------------------------------------------------------------------------
// DOM helpers. Text is always assigned through textContent, never as HTML.

function el<K extends keyof HTMLElementTagNameMap>(tag: K, className = '', text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(label: string, className: string, leading?: IconName): HTMLButtonElement {
  const node = el('button', className);
  node.type = 'button';
  if (leading) node.append(icon(leading));
  node.append(el('span', 'btn-label', label));
  return node;
}

function setLabel(node: HTMLButtonElement, label: string) {
  const target = node.querySelector('.btn-label');
  if (target) target.textContent = label;
}

function brand(): HTMLElement {
  const node = el('div', 'brand');
  node.append(el('span', 'brand-mark', 'RS8'), el('span', 'brand-tag', 'Admin'));
  return node;
}

type Tone = 'error' | 'info' | 'success';
interface Notice { tone: Tone; text: string }

function notice({ tone, text }: Notice): HTMLElement {
  const node = el('div', `notice notice-${tone}`);
  node.append(icon(tone === 'error' ? 'alert' : tone === 'success' ? 'check' : 'info'), el('p', '', text));
  return node;
}

const byteLength = (value: string) => new TextEncoder().encode(value).length;
const clock = (date: Date) => date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });

// ---------------------------------------------------------------------------
// Sections. Overview reads /admin/api/dashboard, Users reads /admin/api/users,
// the Journal (route #/audit) reads /admin/api/journal.

type SectionId = 'overview' | 'users' | 'tariffs' | 'audit' | 'sources' | 'builders';
interface Section {
  id: SectionId;
  label: string;
  description: string;
  placeholder?: { title: string; text: string };
}

const SECTIONS: readonly Section[] = [
  {
    id: 'overview', label: 'Обзор',
    description: 'Подписки, пользователи и действия в панели по текущим данным сервиса.',
  },
  {
    id: 'users', label: 'Пользователи',
    description: 'Клиенты с привязанным Telegram-аккаунтом и их подписки. Пробные подписки без привязки сюда не входят.',
  },
  {
    id: 'tariffs', label: 'Тарифы',
    description: 'Предложения для покупки в Mini App и боте: цена, срок, карточка и порядок показа.',
  },
  {
    id: 'sources', label: 'Источники',
    description: 'Внешние подписки-источники VPN-конфигураций для построителей.',
  },
  {
    id: 'builders', label: 'Построители',
    description: 'Конфигурации подписок: правила фильтрации, порядок серверов, назначение планам.',
  },
  {
    id: 'audit', label: 'Журнал',
    description: 'История действий пользователей, администраторов и системы: регистрации, подписки и платежи. Записи создаются автоматически и не редактируются.',
  },
];

// Routes: #/overview, #/users, #/users/{telegram_id}, #/audit. Only canonical
// positive Telegram IDs open a user, matching the API route.
// Tariffs: #/tariffs, #/tariffs/new, #/tariffs/{product_id}.
// Sources: #/sources, #/sources/{source_id} (catalogue of one source).
interface Route { section: Section; userId: number | null; tariffId: number | 'new' | null; sourceId: number | null }
const USER_ROUTE = /^users\/([1-9][0-9]{0,15})$/;
const TARIFF_ROUTE = /^tariffs\/(new|[1-9][0-9]{0,15})$/;
const SOURCE_ROUTE = /^sources\/([1-9][0-9]{0,15})$/;

function currentRoute(): Route {
  const path = location.hash.replace(/^#\/?/, '');
  const match = USER_ROUTE.exec(path);
  const users = SECTIONS.find(section => section.id === 'users');
  if (match && users) {
    const id = Number(match[1]);
    if (Number.isSafeInteger(id)) return { section: users, userId: id, tariffId: null, sourceId: null };
  }
  const tariff = TARIFF_ROUTE.exec(path);
  const tariffs = SECTIONS.find(section => section.id === 'tariffs');
  if (tariff && tariffs) {
    const id = tariff[1] === 'new' ? 'new' : Number(tariff[1]);
    if (id === 'new' || Number.isSafeInteger(id)) return { section: tariffs, userId: null, tariffId: id, sourceId: null };
  }
  const source = SOURCE_ROUTE.exec(path);
  const sources = SECTIONS.find(section => section.id === 'sources');
  if (source && sources) {
    const id = Number(source[1]);
    if (Number.isSafeInteger(id)) return { section: sources, userId: null, tariffId: null, sourceId: id };
  }
  return { section: SECTIONS.find(section => section.id === path) ?? SECTIONS[0], userId: null, tariffId: null, sourceId: null };
}

// ---------------------------------------------------------------------------
// Overview. Every figure is a counter from GET /admin/api/dashboard; shares
// are derived from those counters only. The API has no time series, so the
// only graphic is the status composition of the current snapshot.

const numberFormat = new Intl.NumberFormat('ru-RU');
const percentFormat = new Intl.NumberFormat('ru-RU', { style: 'percent', maximumFractionDigits: 0 });
const formatCount = (value: number) => numberFormat.format(value);
const clockSeconds = (date: Date) => date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' });

/** Share of the total; never rounds a non-zero part to 0 % or a partial one to 100 %. */
function formatShare(part: number, total: number): string {
  if (total <= 0) return percentFormat.format(0);
  const ratio = part / total;
  if (part > 0 && ratio < 0.005) return `< ${percentFormat.format(0.01)}`;
  if (part < total && ratio > 0.995) return `> ${percentFormat.format(0.99)}`;
  return percentFormat.format(ratio);
}

type StatusKey = 'active' | 'expired' | 'paused' | 'revoked' | 'canceled';
// Labels match connectionStatusLabel in internal/web/web.go. Only "active"
// carries the accent; the rest step down a single neutral ramp.
const STATUS_ROWS: readonly { key: StatusKey; label: string }[] = [
  { key: 'active', label: 'Активна' },
  { key: 'expired', label: 'Истекла' },
  { key: 'paused', label: 'Приостановлена' },
  { key: 'revoked', label: 'Отозвана' },
  { key: 'canceled', label: 'Отменена' },
];

function panelHead(id: string, title: string, meta?: string): HTMLElement {
  const head = el('div', 'panel-head');
  const heading = el('h2', 'panel-title', title);
  heading.id = id;
  head.append(heading);
  if (meta) head.append(el('p', 'panel-meta', meta));
  return head;
}

function kpiStrip(d: Dashboard): HTMLElement {
  const items: readonly [label: string, value: number, meta: string][] = [
    ['Подписки', d.total_subscriptions, 'Во всех статусах'],
    ['Активные', d.active, `${formatShare(d.active, d.total_subscriptions)} от всех подписок`],
    ['Пользователи', d.users, `Пробных без привязки: ${formatCount(d.trials)}`],
    ['Платные', d.paid, `${formatShare(d.paid, d.total_subscriptions)} от всех подписок`],
    ['Действия за 24 ч', d.audit_last_24h, 'Изменения из панели'],
  ];
  const strip = el('section', 'panel kpis');
  strip.setAttribute('aria-label', 'Ключевые показатели');
  const list = el('dl', 'kpi-grid');
  for (const [label, value, meta] of items) {
    const cell = el('div', 'kpi');
    cell.append(el('dt', 'kpi-label', label), el('dd', 'kpi-value', formatCount(value)), el('dd', 'kpi-meta', meta));
    list.append(cell);
  }
  strip.append(list);
  return strip;
}

function statusPanel(d: Dashboard): HTMLElement {
  const total = d.total_subscriptions;
  const panel = el('section', 'panel');
  panel.setAttribute('aria-labelledby', 'status-title');

  const bar = el('div', 'dist');
  bar.setAttribute('role', 'img');
  bar.setAttribute('aria-label', STATUS_ROWS
    .filter(row => d[row.key] > 0)
    .map(row => `${row.label}: ${formatShare(d[row.key], total)}`)
    .join(', '));
  for (const row of STATUS_ROWS) {
    if (d[row.key] <= 0) continue;
    const segment = el('span', `dist-seg tone-${row.key}`);
    // CSSOM, not a style attribute: allowed under the style-src 'self' CSP.
    segment.style.flexGrow = String(d[row.key]);
    bar.append(segment);
  }

  const table = el('table', 'table');
  table.append(el('caption', 'sr-only', 'Количество подписок по статусам'));
  const head = el('thead');
  const headRow = el('tr');
  for (const [text, cls] of [['Статус', ''], ['Количество', 'num'], ['Доля', 'num']] as const) {
    const th = el('th', cls, text);
    th.scope = 'col';
    headRow.append(th);
  }
  head.append(headRow);
  const body = el('tbody');
  for (const row of STATUS_ROWS) {
    const tr = el('tr');
    if (d[row.key] === 0) tr.className = 'is-zero';
    const name = el('th', 'status-cell');
    name.scope = 'row';
    name.append(el('span', `swatch tone-${row.key}`), el('span', '', row.label));
    tr.append(name, el('td', 'num', formatCount(d[row.key])), el('td', 'num muted', formatShare(d[row.key], total)));
    body.append(tr);
  }
  const foot = el('tfoot');
  const footRow = el('tr');
  const footName = el('th', '', 'Всего');
  footName.scope = 'row';
  footRow.append(footName, el('td', 'num', formatCount(total)), el('td', 'num muted', percentFormat.format(total > 0 ? 1 : 0)));
  foot.append(footRow);
  table.append(head, body, foot);

  const scroll = el('div', 'table-wrap');
  scroll.append(table);
  panel.append(panelHead('status-title', 'Подписки по статусам', `Всего ${formatCount(total)}`), bar, scroll);
  return panel;
}

function attentionPanel(d: Dashboard): HTMLElement {
  const items: readonly [value: number, title: string, text: string, alert: boolean][] = [
    [d.active_expired, 'Срок истёк, статус «Активна»', 'Ещё не обработаны фоновой проверкой истечения сроков.', true],
    [d.expiring_in_7d, 'Истекают в ближайшие 7 дней', 'Активные подписки с датой окончания в течение недели.', false],
  ];
  const panel = el('section', 'panel');
  panel.setAttribute('aria-labelledby', 'attention-title');
  const list = el('ul', 'stat-list');
  for (const [value, title, text, alert] of items) {
    const item = el('li', 'stat');
    const copy = el('div', 'stat-copy');
    copy.append(el('p', 'stat-title', title), el('p', 'stat-text', text));
    const figure = el('p', alert && value > 0 ? 'stat-value is-alert' : 'stat-value', formatCount(value));
    item.append(copy, figure);
    list.append(item);
  }
  panel.append(panelHead('attention-title', 'Требует внимания'), list);
  return panel;
}

function overviewContent(d: Dashboard): HTMLElement[] {
  if (d.total_subscriptions === 0) {
    const empty = el('section', 'panel empty');
    empty.setAttribute('aria-labelledby', 'overview-empty-title');
    const title = el('h2', 'empty-title', 'Подписок пока нет');
    title.id = 'overview-empty-title';
    empty.append(icon('overview', 20), title, el('p', 'empty-text', 'Распределение по статусам и сроки появятся, когда в базе будут первые подписки.'));
    return [kpiStrip(d), empty];
  }
  const grid = el('div', 'overview-grid');
  grid.append(statusPanel(d), attentionPanel(d));
  return [kpiStrip(d), grid];
}

function overviewSkeleton(): HTMLElement[] {
  const strip = el('div', 'panel kpis');
  const cells = el('div', 'kpi-grid');
  for (let i = 0; i < 5; i++) {
    const cell = el('div', 'kpi');
    cell.append(el('span', 'skeleton skeleton-label'), el('span', 'skeleton skeleton-value'), el('span', 'skeleton skeleton-meta'));
    cells.append(cell);
  }
  strip.append(cells);
  const block = (rows: number) => {
    const panel = el('div', 'panel');
    const head = el('div', 'panel-head');
    head.append(el('span', 'skeleton skeleton-label'));
    const lines = el('div', 'skeleton-rows');
    for (let i = 0; i < rows; i++) lines.append(el('span', 'skeleton skeleton-row'));
    panel.append(head, lines);
    return panel;
  };
  const grid = el('div', 'overview-grid');
  grid.append(block(6), block(2));
  return [strip, grid];
}

// ---------------------------------------------------------------------------
// Users. Rows come from GET /admin/api/users and the user page from
// GET /admin/api/users/{telegram_id}; only fields those responses carry are
// shown. Search, filter and page live in memory, never in the URL.

const SEARCH_DEBOUNCE_MS = 350;
const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;
const dateFormat = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' });
const dateTimeFormat = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
const relativeFormat = new Intl.RelativeTimeFormat('ru-RU', { numeric: 'auto' });
const trafficFormat = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 1 });
const formatDate = (iso: string) => dateFormat.format(new Date(iso));
const formatDateTime = (iso: string) => dateTimeFormat.format(new Date(iso));

function relativeTime(iso: string, now: number): string {
  const diff = Date.parse(iso) - now;
  const abs = Math.abs(diff);
  if (abs >= DAY_MS) return relativeFormat.format(Math.round(diff / DAY_MS), 'day');
  if (abs >= HOUR_MS) return relativeFormat.format(Math.round(diff / HOUR_MS), 'hour');
  return relativeFormat.format(Math.round(diff / MINUTE_MS), 'minute');
}

const handle = (username: string) => (username.startsWith('@') ? username : `@${username}`);
const displayName = (user: AdminSubscription) => (user.username ? handle(user.username) : `Пользователь ${user.telegram_id}`);
const planLabel = (user: AdminSubscription) => user.plan_name || `Тариф #${user.plan_id}`;
const statusLabel = (status: string) => STATUS_ROWS.find(row => row.key === status)?.label ?? status;
const parseStatus = (value: string): SubscriptionStatus | '' => STATUS_ROWS.find(row => row.key === value)?.key ?? '';

/** Status active with the expiry already passed: not yet downgraded by the expiry worker. */
const isActiveExpired = (user: AdminSubscription, now: number) =>
  user.status === 'active' && user.expires_at !== null && Date.parse(user.expires_at) <= now;

function formatPrice(amount: number, currency: string | null): string {
  // Telegram Stars are stored as whole Stars, other currencies in minor units.
  if (currency === 'XTR') return `${formatCount(amount)} XTR`;
  if (!currency) return `${formatCount(amount)} (валюта не указана)`;
  try {
    return new Intl.NumberFormat('ru-RU', { style: 'currency', currency }).format(amount / 100);
  } catch {
    return `${formatCount(amount)} ${currency}`;
  }
}

function formatTraffic(bytes: number): string {
  if (bytes === 0) return 'Без ограничения';
  const gib = bytes / 1024 ** 3;
  return gib >= 1 ? `${trafficFormat.format(gib)} ГБ` : `${trafficFormat.format(bytes / 1024 ** 2)} МБ`;
}

// database.SyncStatus values.
const SYNC_LABELS: Readonly<Record<string, string>> = {
  active: 'Синхронизирован',
  pending_add: 'Ожидает добавления',
  pending_remove: 'Ожидает удаления',
  pending_update: 'Ожидает обновления',
};

interface UsersState {
  /** Last applied query; the page size is the server default. */
  query: UsersQuery;
  /** Search box text, possibly not applied yet. */
  draft: string;
  page: UsersPage | null;
  pageQuery: UsersQuery | null;
  at: Date | null;
  /** Telegram ID of the user opened from the list, to restore focus on return. */
  lastOpened: number | null;
}

interface UsersView {
  results: HTMLElement;
  refresh: HTMLButtonElement;
  updated: HTMLElement;
  summary: HTMLElement;
  search: HTMLInputElement;
  clear: HTMLButtonElement;
  status: HTMLSelectElement;
  setSearchError: (text: string) => void;
  pagerFocus: 'prev' | 'next' | null;
}

interface DetailView {
  id: number;
  body: HTMLElement;
  refresh: HTMLButtonElement;
  updated: HTMLElement;
  title: HTMLElement;
  desc: HTMLElement;
}

function initialUsersState(): UsersState {
  return { query: { q: '', status: '', offset: 0 }, draft: '', page: null, pageQuery: null, at: null, lastOpened: null };
}

const sameQuery = (a: UsersQuery, b: UsersQuery) => a.q === b.q && a.status === b.status && a.offset === b.offset;

function setLoading(node: HTMLElement, label: string | null) {
  if (label) {
    node.setAttribute('aria-busy', 'true');
    node.setAttribute('role', 'status');
    node.setAttribute('aria-label', label);
  } else {
    node.removeAttribute('aria-busy');
    node.removeAttribute('role');
    node.removeAttribute('aria-label');
  }
}

function refreshActions(onRefresh: () => void) {
  const actions = el('div', 'page-actions');
  const updated = el('p', 'updated');
  updated.setAttribute('aria-live', 'polite');
  const refresh = button('Обновить', 'btn btn-secondary btn-sm', 'refresh');
  refresh.addEventListener('click', onRefresh);
  actions.append(updated, refresh);
  return { actions, refresh, updated };
}

function setRefreshBusy(refresh: HTMLButtonElement, busy: boolean) {
  refresh.disabled = busy;
  refresh.setAttribute('aria-busy', String(busy));
  setLabel(refresh, busy ? 'Обновляем…' : 'Обновить');
}

/** A table cell that also carries its column name for the stacked mobile layout. */
function cell(label: string, content: string | Node, className = ''): HTMLTableCellElement {
  const td = el('td', className);
  td.dataset.label = label;
  td.append(content);
  return td;
}

function headRow(columns: readonly (readonly [string, string])[]): HTMLTableSectionElement {
  const head = el('thead');
  const row = el('tr');
  for (const [text, className] of columns) {
    const th = el('th', className, text);
    th.scope = 'col';
    row.append(th);
  }
  head.append(row);
  return head;
}

function option(value: string, label: string): HTMLOptionElement {
  const node = el('option', '', label);
  node.value = value;
  return node;
}

function messagePanel(name: IconName, title: string, text: string, role: 'status' | 'alert', action?: HTMLElement): HTMLElement {
  const panel = el('section', 'panel empty');
  panel.setAttribute('role', role);
  panel.append(icon(name, 20), el('h2', 'empty-title', title), el('p', 'empty-text', text));
  if (action) panel.append(action);
  return panel;
}

function retryButton(onRetry: () => void): HTMLButtonElement {
  const retry = button('Повторить', 'btn btn-secondary btn-sm', 'refresh');
  retry.addEventListener('click', onRetry);
  return retry;
}

/**
 * Drag-and-drop reorder for a list. Items with the given CSS class are
 * draggable. The callback receives the new order as an array of data-src-id or
 * data-drag-idx values (strings), depending on which attribute is present.
 */
function setupDragReorder(list: HTMLElement, itemClass: string, onReorder: (newOrder: string[]) => void) {
  let dragSrc: HTMLElement | null = null;

  const items = list.querySelectorAll<HTMLElement>(`.${itemClass}`);
  for (const item of items) {
    item.addEventListener('dragstart', (e) => {
      dragSrc = item;
      item.classList.add('dragging');
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', item.dataset.srcId ?? item.dataset.dragIdx ?? '');
      }
    });
    item.addEventListener('dragend', () => {
      item.classList.remove('dragging');
      list.querySelectorAll('.drag-over').forEach(n => n.classList.remove('drag-over'));
      dragSrc = null;
    });
    item.addEventListener('dragover', (e) => {
      if (!dragSrc || dragSrc === item) return;
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
      list.querySelectorAll('.drag-over').forEach(n => n.classList.remove('drag-over'));
      item.classList.add('drag-over');
    });
    item.addEventListener('drop', (e) => {
      e.preventDefault();
      if (!dragSrc || dragSrc === item) return;
      const allItems = [...list.querySelectorAll<HTMLElement>(`.${itemClass}`)];
      const fromIdx = allItems.indexOf(dragSrc);
      const toIdx = allItems.indexOf(item);
      if (fromIdx === -1 || toIdx === -1) return;
      allItems.splice(fromIdx, 1);
      allItems.splice(toIdx, 0, dragSrc);
      for (const n of allItems) list.append(n);
      const newOrder = allItems.map(n => n.dataset.srcId ?? n.dataset.dragIdx ?? '');
      onReorder(newOrder);
    });
  }
}

function skeletonPanel(rows: number, head = true): HTMLElement {
  const panel = el('div', 'panel');
  if (head) {
    const top = el('div', 'panel-head');
    top.append(el('span', 'skeleton skeleton-label'));
    panel.append(top);
  }
  const lines = el('div', 'skeleton-rows');
  for (let i = 0; i < rows; i++) lines.append(el('span', 'skeleton skeleton-row'));
  panel.append(lines);
  return panel;
}

function statusBadge(status: string): HTMLElement {
  const node = el('span', 'status');
  const known = STATUS_ROWS.some(row => row.key === status);
  node.append(el('span', known ? `swatch tone-${status}` : 'swatch tone-unknown'), el('span', '', statusLabel(status)));
  return node;
}

function userLink(telegramId: number): HTMLAnchorElement {
  const link = el('a', 'inline-link', String(telegramId));
  link.href = `#/users/${telegramId}`;
  return link;
}

const USER_COLUMNS = [
  ['Пользователь', 'col-user'], ['Telegram ID', 'col-id'], ['Статус', 'col-status'], ['Тариф', 'col-md'],
  ['Действует до', ''], ['Последний запрос', 'col-lg'], ['Оплата', 'col-md'],
] as const;

const NODE_COLUMNS = [
  ['Узел', ''], ['Синхронизация', ''], ['Попытки', 'num'], ['Следующая попытка', ''], ['Обновлено', ''], ['Последняя ошибка', ''],
] as const;

function fact(label: string, value: string | Node, note?: string, noteClass = ''): HTMLElement {
  const item = el('div', 'fact');
  const data = el('dd');
  data.append(value);
  if (note) data.append(el('span', noteClass ? `fact-note ${noteClass}` : 'fact-note', note));
  item.append(el('dt', '', label), data);
  return item;
}

function subscriptionPanel(user: AdminSubscription, plan: AdminPlan | null, now: number): HTMLElement {
  const panel = el('section', 'panel');
  panel.setAttribute('aria-labelledby', 'subscription-title');
  const expired = isActiveExpired(user, now);
  const referrer: string | Node = user.referred_by === null ? 'Нет'
    : user.referred_by > 0 ? userLink(user.referred_by) : String(user.referred_by);
  const facts = el('dl', 'facts');
  facts.append(
    fact('Статус', statusBadge(user.status), expired ? 'Срок истёк, статус ещё не обновлён' : undefined, 'is-danger'),
    fact('Действует до', user.expires_at ? formatDateTime(user.expires_at) : 'Бессрочно',
      user.expires_at ? relativeTime(user.expires_at, now) : undefined, expired ? 'is-danger' : ''),
    fact('Тариф', planLabel(user)),
    fact('Оплата', user.is_paid ? 'Платная' : 'Бесплатная',
      user.price_paid_cents > 0 ? formatPrice(user.price_paid_cents, user.currency) : undefined),
    fact('Устройства', formatCount(user.devices), plan ? `Лимит тарифа: ${formatCount(plan.devices_limit)}` : undefined),
    fact('IP-адреса', formatCount(user.ips)),
    fact('Последний запрос', user.last_request ? formatDateTime(user.last_request) : 'Не было',
      user.last_request ? relativeTime(user.last_request, now) : undefined),
    fact('Начало', user.started_at ? formatDateTime(user.started_at) : 'Нет данных'),
    fact('Источник', user.provider_source_id === null ? 'Узлы сервиса' : `Внешний провайдер #${user.provider_source_id}`),
    fact('Пригласил', referrer),
    fact('Создана', formatDateTime(user.created_at)),
    fact('Обновлена', formatDateTime(user.updated_at)),
  );
  panel.append(panelHead('subscription-title', 'Подписка', `#${user.id}`), facts);
  return panel;
}

function planPanel(user: AdminSubscription, plan: AdminPlan | null): HTMLElement {
  const panel = el('section', 'panel');
  panel.setAttribute('aria-labelledby', 'plan-title');
  panel.append(panelHead('plan-title', 'Тариф', plan ? `#${plan.id}` : undefined));
  if (!plan) {
    panel.append(el('p', 'panel-note', `Тариф #${user.plan_id} не найден в базе.`));
    return panel;
  }
  const facts = el('dl', 'facts facts-single');
  facts.append(
    fact('Название', plan.name || `Тариф #${plan.id}`),
    fact('Состояние', plan.is_active ? 'Доступен' : 'Отключён'),
    fact('Лимит устройств', formatCount(plan.devices_limit)),
    fact('Лимит трафика', formatTraffic(plan.traffic_limit)),
  );
  panel.append(facts);
  return panel;
}

function nodesPanel(user: AdminSubscription, nodes: readonly AdminNode[]): HTMLElement {
  const panel = el('section', 'panel');
  panel.setAttribute('aria-labelledby', 'nodes-title');
  panel.append(panelHead('nodes-title', 'Узлы', formatCount(nodes.length)));
  if (nodes.length === 0) {
    panel.append(el('p', 'panel-note', user.provider_source_id === null
      ? 'К подписке не привязан ни один узел.'
      : 'Подписка обслуживается внешним провайдером, узлы сервиса к ней не привязываются.'));
    return panel;
  }
  const table = el('table', 'table table-cards nodes-table');
  table.append(el('caption', 'sr-only', 'Узлы подписки'), headRow(NODE_COLUMNS));
  const body = el('tbody');
  for (const node of nodes) {
    const sync = el('span', 'status');
    sync.append(el('span', node.status === 'active' ? 'swatch tone-active' : 'swatch tone-paused'),
      el('span', '', SYNC_LABELS[node.status] ?? node.status));
    const row = el('tr');
    row.append(
      cell('Узел', node.node_name || `Узел #${node.node_id}`),
      cell('Синхронизация', sync),
      cell('Попытки', formatCount(node.retry_count), 'num'),
      cell('Следующая попытка', node.retry_at ? formatDateTime(node.retry_at) : 'Нет', node.retry_at ? '' : 'muted'),
      cell('Обновлено', formatDateTime(node.updated_at)),
      cell('Последняя ошибка', node.last_error ? el('span', 'error-text', node.last_error) : 'Нет',
        node.last_error ? 'cell-wrap' : 'muted'),
    );
    body.append(row);
  }
  table.append(body);
  const wrap = el('div', 'table-wrap');
  wrap.append(table);
  panel.append(wrap);
  return panel;
}

function usersSkeleton(): HTMLElement {
  const panel = skeletonPanel(8, false);
  panel.classList.add('users-panel');
  return panel;
}

function detailSkeleton(): HTMLElement[] {
  const grid = el('div', 'detail-grid');
  grid.append(skeletonPanel(6), skeletonPanel(4));
  return [grid, skeletonPanel(3)];
}

// ---------------------------------------------------------------------------
// Journal. Rows come from GET /admin/api/journal: append-only events written
// by the backend in the transaction of each action. Only fields the response
// carries are shown; filters and page live in memory, never in the URL.

const JOURNAL_LABELS: Readonly<Record<JournalEventType, string>> = {
  user_registered: 'Регистрация',
  trial_started: 'Пробная подписка активирована',
  trial_bound: 'Пробная подписка привязана',
  trial_expired: 'Пробная подписка истекла',
  subscription_reconnected: 'Подписка подключена повторно',
  free_activated: 'Бесплатная подписка активирована',
  paid_activated: 'Платная подписка активирована',
  plan_changed: 'Тариф изменён',
  subscription_renewed: 'Продление',
  expiry_changed: 'Срок действия изменён',
  subscription_expired: 'Подписка истекла',
  subscription_disabled: 'Подписка отключена',
  subscription_enabled: 'Подписка включена',
  subscription_revoked: 'Подписка отозвана',
  payment_succeeded: 'Успешная оплата',
  payment_failed: 'Неуспешная оплата',
  payment_refunded: 'Возврат платежа',
};

const JOURNAL_GROUPS: readonly { label: string; types: readonly JournalEventType[] }[] = [
  { label: 'Пользователи', types: ['user_registered', 'trial_started', 'trial_bound', 'trial_expired'] },
  {
    label: 'Подписки',
    types: ['subscription_reconnected', 'free_activated', 'paid_activated', 'plan_changed', 'subscription_renewed',
      'expiry_changed', 'subscription_expired', 'subscription_disabled', 'subscription_enabled', 'subscription_revoked'],
  },
  { label: 'Платежи', types: ['payment_succeeded', 'payment_failed', 'payment_refunded'] },
];

const PLAN_KIND_LABELS: Readonly<Record<PlanKind, string>> = { free: 'Бесплатная', trial: 'Пробная', paid: 'Платная' };
const ACTOR_LABELS: Readonly<Record<string, string>> = { user: 'Пользователь', admin: 'Администратор', system: 'Система' };
const OUTCOME_LABELS: Readonly<Record<string, string>> = { success: 'Успешно', failed: 'Ошибка', rejected: 'Отклонено' };
// Outcome swatches reuse the status ramp: accent for success, danger for failure.
const OUTCOME_TONES: Readonly<Record<string, string>> = { success: 'tone-active', failed: 'tone-failed', rejected: 'tone-paused' };
const ACTOR_NAME_LABELS: Readonly<Record<string, string>> = { platega: 'Platega', telegram_stars: 'Telegram Stars', bot: 'Telegram-бот' };

const journalLabel = (type: string) => (JOURNAL_LABELS as Readonly<Record<string, string>>)[type] ?? type;
const planKindLabel = (kind: string) => (PLAN_KIND_LABELS as Readonly<Record<string, string>>)[kind] ?? '';
const actorNameLabel = (name: string) => ACTOR_NAME_LABELS[name] ?? name;
const parseJournalType = (value: string): JournalEventType | '' =>
  (JOURNAL_EVENT_TYPES as readonly string[]).includes(value) ? value as JournalEventType : '';
const parsePlanKind = (value: string): PlanKind | '' =>
  (PLAN_KINDS as readonly string[]).includes(value) ? value as PlanKind : '';
const dateTimeSecondsFormat = new Intl.DateTimeFormat('ru-RU', {
  day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
});

const JOURNAL_COLUMNS = [
  ['Дата и время', 'col-date'], ['Пользователь', 'col-user'], ['Событие', ''], ['Подписка и тариф', 'col-md'],
  ['Инициатор', 'col-md'], ['Статус', 'col-status'],
] as const;

interface JournalListState {
  /** Last applied query; the page size is the server default. */
  query: JournalQuery;
  /** Search box text, possibly not applied yet. */
  draft: string;
  /** Period inputs (local calendar days, YYYY-MM-DD) behind query.from/to. */
  fromDate: string;
  toDate: string;
  page: JournalPage | null;
  pageQuery: JournalQuery | null;
  at: Date | null;
}

interface JournalView {
  results: HTMLElement;
  refresh: HTMLButtonElement;
  updated: HTMLElement;
  summary: HTMLElement;
  search: HTMLInputElement;
  clear: HTMLButtonElement;
  type: HTMLSelectElement;
  kind: HTMLSelectElement;
  from: HTMLInputElement;
  to: HTMLInputElement;
  setError: (text: string) => void;
  pagerFocus: 'prev' | 'next' | null;
}

function initialJournalState(): JournalListState {
  return {
    query: { q: '', type: '', planKind: '', from: '', to: '', offset: 0 },
    draft: '', fromDate: '', toDate: '', page: null, pageQuery: null, at: null,
  };
}

const sameJournalQuery = (a: JournalQuery, b: JournalQuery) =>
  a.q === b.q && a.type === b.type && a.planKind === b.planKind && a.from === b.from && a.to === b.to && a.offset === b.offset;

/** Start of a local calendar day (plus days) as the RFC 3339 instant the API takes. */
function dayStart(value: string, plusDays = 0): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return '';
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]) + plusDays);
  return Number.isNaN(date.getTime()) ? '' : date.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/** Who the event is about: a linked customer, an anonymous trial or nobody known. */
function journalSubject(event: JournalEvent): { text: string; anon: boolean } {
  if (event.username) return { text: handle(event.username), anon: false };
  if (event.telegram_id > 0) return { text: `Пользователь ${event.telegram_id}`, anon: false };
  if (event.telegram_id < 0) return { text: 'Анонимный посетитель', anon: true };
  return { text: 'Не указан', anon: true };
}

function outcomeBadge(outcome: string): HTMLElement {
  const node = el('span', 'status');
  node.append(el('span', `swatch ${OUTCOME_TONES[outcome] ?? 'tone-unknown'}`), el('span', '', OUTCOME_LABELS[outcome] ?? outcome));
  return node;
}

function actorText(event: JournalEvent): string {
  return ACTOR_LABELS[event.actor] ?? event.actor;
}

function planText(event: JournalEvent): string {
  if (!event.plan_name) return 'Тариф не указан';
  const kind = planKindLabel(event.plan_kind);
  return kind ? `${event.plan_name} · ${kind}` : event.plan_name;
}

function stateValue(state: JournalState | null, pick: 'status' | 'expiry' | 'plan'): string | null {
  if (!state) return null;
  switch (pick) {
    case 'status': return statusLabel(state.status);
    case 'expiry': return state.expires_at ? formatDateTime(state.expires_at) : 'Бессрочно';
    case 'plan': return state.plan_name || (state.plan_id ? `Тариф #${state.plan_id}` : 'Нет');
  }
}

/** "before → after", or the single value with a note when it did not change. */
function changeNode(before: string | null, after: string | null): HTMLElement {
  const node = el('span', 'change');
  if (before !== null && after !== null && before !== after) {
    node.append(el('span', 'change-old', before), el('span', 'change-arrow', '→'), el('span', '', after));
  } else {
    node.append(el('span', '', after ?? before ?? 'Нет данных'));
    if (before !== null && after !== null) node.append(el('span', 'change-same', 'без изменений'));
  }
  return node;
}

// ---------------------------------------------------------------------------
// Subscription management. Availability mirrors database.applyAdminAction so
// only meaningful actions are offered; the backend stays the authority and
// its rejections are reported, never second-guessed. disable is a pause:
// active|expired → paused, reversible by enable while the expiry has not passed.

type ManageTone = 'primary' | 'secondary' | 'danger';

interface ManageItem {
  action: MutationAction;
  title: string;
  text: string;
  label: string;
  tone: ManageTone;
  /** Set when the action is shown but cannot be applied in the current state. */
  blocked?: string;
}

const hasExpired = (sub: AdminSubscription, now: number) => sub.expires_at !== null && Date.parse(sub.expires_at) <= now;

function manageItems(sub: AdminSubscription, now: number): ManageItem[] {
  const { status } = sub;
  if (status !== 'active' && status !== 'expired' && status !== 'paused') return [];
  const perpetual = sub.expires_at === null;
  const lapsed = hasExpired(sub, now);
  const canEnable = status === 'paused' && !lapsed;
  const items: ManageItem[] = [];
  if (status === 'paused') {
    items.push({
      action: 'enable', title: 'Возобновление', label: 'Возобновить', tone: canEnable ? 'primary' : 'secondary',
      text: 'Вернуть подписку в активное состояние и восстановить доступ к VPN.',
      blocked: canEnable ? undefined : 'Срок уже истёк: сначала продлите подписку или измените срок.',
    });
  }
  items.push({
    action: 'renew', title: 'Продление', label: 'Продлить', tone: perpetual || canEnable ? 'secondary' : 'primary',
    text: lapsed ? 'Добавить дни от текущего момента: срок уже истёк.' : 'Добавить дни к текущему сроку.',
    blocked: perpetual ? 'Подписка бессрочная, продлевать нечего.' : undefined,
  });
  items.push({
    action: 'expiry', title: 'Срок действия', label: 'Изменить срок', tone: 'secondary',
    text: perpetual ? 'Установить дату окончания. Сейчас подписка бессрочная.' : 'Установить конкретную дату окончания.',
  });
  if (status === 'active' || status === 'expired') {
    items.push({
      action: 'disable', title: 'Приостановка', label: 'Приостановить', tone: 'danger',
      text: 'Временно отключить доступ к VPN. Срок действия не изменится.',
    });
  }
  return items;
}

function unmanageableText(status: string): string {
  if (status === 'revoked' || status === 'canceled') {
    return `Подписка со статусом «${statusLabel(status)}» не продлевается, не приостанавливается и не возобновляется из панели.`;
  }
  return `Статус «${status}» панели неизвестен, поэтому действия с подпиской недоступны.`;
}

const btnClass = (tone: ManageTone) => (tone === 'primary' ? 'btn btn-primary' : tone === 'danger' ? 'btn btn-danger' : 'btn btn-secondary');

/** Go time.AddDate(0, 0, days) in UTC, from max(now, expiry) like applyAdminAction. */
function renewedExpiry(sub: AdminSubscription, days: number, now: number): string | null {
  if (sub.expires_at === null) return null;
  const current = Date.parse(sub.expires_at);
  const next = new Date(current > now ? current : now);
  next.setUTCDate(next.getUTCDate() + days);
  return next.toISOString();
}

/** Status after a successful mutation, as applyAdminAction would set it. */
function nextStatus(sub: AdminSubscription, action: MutationAction, expiresAt: string | null, now: number): string {
  switch (action) {
    case 'renew': return sub.status === 'expired' ? 'active' : sub.status;
    case 'expiry': return sub.status === 'expired' && expiresAt !== null && Date.parse(expiresAt) > now ? 'active' : sub.status;
    case 'disable': return 'paused';
    case 'enable': return 'active';
  }
}

const expiryLabel = (iso: string | null) => (iso ? formatDateTime(iso) : 'Бессрочно');

const pad2 = (value: number) => String(value).padStart(2, '0');

/** A local Date as the value of <input type="datetime-local"> (minute precision). */
function localInputValue(date: Date): string {
  return `${String(date.getFullYear()).padStart(4, '0')}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}T${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

/** Parses a datetime-local value; null for malformed or non-existent local times (DST gaps). */
function parseLocalInput(value: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (!match) return null;
  const [year, month, day, hour, minute] = match.slice(1).map(Number);
  const date = new Date(year, month - 1, day, hour, minute);
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day ||
    date.getHours() !== hour || date.getMinutes() !== minute) return null;
  return date;
}

/** RFC 3339 UTC without fractional seconds, as time.Parse(time.RFC3339) expects. */
const toRFC3339 = (date: Date) => date.toISOString().replace(/\.\d{3}Z$/, 'Z');

function timeZoneLabel(): string {
  const offset = -new Date().getTimezoneOffset();
  const sign = offset >= 0 ? '+' : '−';
  return `UTC${sign}${pad2(Math.floor(Math.abs(offset) / 60))}:${pad2(Math.abs(offset) % 60)}`;
}

const ACTION_FAILED: Readonly<Record<MutationAction, string>> = {
  renew: 'Подписка не продлена.',
  expiry: 'Срок не изменён.',
  disable: 'Подписка не приостановлена.',
  enable: 'Подписка не возобновлена.',
};

/** Recorded domain rejections (HTTP 409): nothing changed; the page is refreshed. */
const REJECTIONS: Readonly<Record<string, string>> = {
  invalid_state: 'Текущий статус подписки не позволяет это действие. Данные подписки обновлены.',
  subscription_expired: 'Срок подписки уже истёк, поэтому её нельзя возобновить. Сначала продлите подписку или измените срок.',
  perpetual_subscription: 'Подписка бессрочная, продлевать нечего. Чтобы задать дату окончания, используйте «Изменить срок».',
  invalid_expiry: 'Итоговый срок выходит за допустимый диапазон.',
  request_key_conflict: 'Запрос конфликтует с ранее отправленным. Данные подписки обновлены, повторите действие.',
};

function successText(action: MutationAction, before: AdminSubscription, outcome: MutationOutcome): string {
  const after = outcome.subscription;
  const parts: string[] = [];
  switch (action) {
    case 'renew': parts.push(`Подписка продлена до ${expiryLabel(after.expires_at)} (было: ${expiryLabel(before.expires_at)}).`); break;
    case 'expiry': parts.push(`Срок изменён: ${expiryLabel(before.expires_at)} → ${expiryLabel(after.expires_at)}.`); break;
    case 'disable': parts.push('Подписка приостановлена, доступ к VPN отключается.'); break;
    case 'enable': parts.push('Подписка возобновлена, доступ к VPN восстанавливается.'); break;
  }
  if (action !== 'disable' && action !== 'enable' && before.status !== after.status) {
    parts.push(`Статус: ${statusLabel(before.status)} → ${statusLabel(after.status)}.`);
  }
  if (after.status === 'paused' && (action === 'renew' || action === 'expiry')) {
    parts.push('Подписка остаётся приостановленной.');
  }
  if (outcome.replayed) parts.push('Этот запрос уже был выполнен ранее, повторно изменение не применялось.');
  return parts.join(' ');
}

/** "old → new" for the confirmation summary; unchanged values say so. */
function change(before: string, after: string | null): HTMLElement {
  const node = el('span', 'change');
  if (after === before) {
    node.append(el('span', '', before), el('span', 'change-same', 'не изменится'));
    return node;
  }
  const arrow = el('span', 'change-arrow', '→');
  arrow.setAttribute('aria-hidden', 'true');
  node.append(el('span', 'change-old', before), arrow, el('span', 'sr-only', ' станет '),
    el('span', 'change-new', after ?? '—'));
  return node;
}

function summaryRow(label: string, value: string | Node): HTMLElement {
  const row = el('div', 'summary-row');
  const data = el('dd');
  data.append(value);
  row.append(el('dt', '', label), data);
  return row;
}

// ---------------------------------------------------------------------------
// Application

const SESSION_CHECK_INTERVAL_MS = 60_000;
const TOAST_MS = 6_000;

interface Shell {
  sidebar: HTMLElement;
  menuButton: HTMLButtonElement;
  links: Map<SectionId, HTMLAnchorElement>;
  content: HTMLElement;
}

interface OverviewView {
  body: HTMLElement;
  refresh: HTMLButtonElement;
  updated: HTMLElement;
}

interface TariffsView {
  body: HTMLElement;
  refresh: HTMLButtonElement;
  updated: HTMLElement;
  summary: HTMLElement;
}

interface TariffEditorView {
  /** null while creating a tariff. */
  id: number | null;
  body: HTMLElement;
  title: HTMLElement;
  desc: HTMLElement;
}

interface SourceDetailView {
  id: number;
  body: HTMLElement;
  refresh: HTMLButtonElement;
  updated: HTMLElement;
  title: HTMLElement;
  desc: HTMLElement;
  source: AdminSource | null;
  entries: readonly SourceEntry[] | null;
  filter: EntryFilter;
  syncing: boolean;
  /** Sync progress/outcome, announced politely; survives repaints of body. */
  status: HTMLElement;
  sync: HTMLButtonElement;
}

/** What a builder rule is read against: every source and the builder's selection. */
interface RuleContext {
  sources: () => readonly AdminSource[];
  selectedIds: () => readonly number[];
}

function enabledBadge(enabled: boolean): HTMLElement {
  return el('span', enabled ? 'badge badge-active' : 'badge badge-disabled', enabled ? 'Включён' : 'Отключён');
}

const SYNC_BADGE_CLASS: Readonly<Record<SyncState, string>> = {
  never: 'badge badge-disabled', ok: 'badge badge-active', partial: 'badge badge-warn', error: 'badge badge-danger',
};

function syncBadge(state: SyncState): HTMLElement {
  return el('span', SYNC_BADGE_CLASS[state], SYNC_STATE_LABELS[state]);
}

/** "Последняя синхронизация: …" (or the last failed attempt). */
function lastSyncText(src: AdminSource): string {
  if (!src.last_sync_at) return 'Синхронизации ещё не было';
  const at = formatDateTime(src.last_sync_at);
  return syncState(src) === 'error' ? `Последняя попытка: ${at}` : `Последняя синхронизация: ${at}`;
}

const protocolsText = (protocols: Readonly<Record<string, number>>) =>
  Object.entries(protocols).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([name, n]) => `${name} ${formatCount(n)}`).join(' · ') || '—';

class AdminApp {
  private view: 'boot' | 'fatal' | 'login' | 'app' = 'boot';
  private shell: Shell | null = null;
  private signedInAt: Date | null = null;
  private lastSessionCheck = 0;
  private lockedUntil = 0;
  private countdownTimer = 0;
  private toastTimer = 0;
  private readonly toasts = el('div', 'toast-region');
  // Last dashboard snapshot, kept in memory only so returning to Overview
  // shows figures at once while a fresh request runs. Cleared on sign-out.
  private dashboard: { data: Dashboard; at: Date } | null = null;
  private overview: OverviewView | null = null;
  private dashboardRequest = 0;
  // Users list state (search, filter, page, last snapshot) survives navigation
  // within the signed-in session; it is never written to the URL or storage.
  private usersState: UsersState = initialUsersState();
  private usersView: UsersView | null = null;
  private usersRequest = 0;
  private searchTimer = 0;
  private detailCache: { id: number; data: UserDetail; at: Date } | null = null;
  private detailView: DetailView | null = null;
  private detailRequest = 0;
  // Journal list state (filters, page, last snapshot); like the users list it
  // survives navigation within the session and never reaches the URL.
  private journalState: JournalListState = initialJournalState();
  private journalView: JournalView | null = null;
  private journalRequest = 0;
  // The open confirmation dialog, if any, and the outcome of the last
  // management action shown on the subscription page it belongs to.
  private dialog: { dismiss: () => void } | null = null;
  private manageNotice: { subscriptionId: number; notice: Notice } | null = null;
  // Sources section state
  private sourcesCache: readonly AdminSource[] | null = null;
  private sourcesRequest = 0;
  private sourcesView: { body: HTMLElement; refresh: HTMLButtonElement } | null = null;
  // Source details (#/sources/{id}): one source and its catalogue.
  private sourceDetailView: SourceDetailView | null = null;
  private sourceDetailRequest = 0;
  // Outcome of the last sync per source, shown on its row and detail page.
  private syncNotices = new Map<number, Notice>();
  // Builders section state
  private buildersCache: readonly AdminBuilder[] | null = null;
  private buildersRequest = 0;
  private buildersView: { body: HTMLElement; refresh: HTMLButtonElement } | null = null;
  // Builder editor state (single builder open)
  private builderEditorRequest = 0;
  // Tariffs section state. The catalogue snapshot survives navigation within
  // the session; pendingOrder is an edited, not yet saved catalogue order.
  private tariffsCache: readonly AdminTariff[] | null = null;
  private plansCache: readonly TariffPlan[] | null = null;
  private tariffsRequest = 0;
  private tariffsView: TariffsView | null = null;
  private tariffsAt: Date | null = null;
  private showRetired = false;
  private pendingOrder: number[] | null = null;
  // Idempotency key of the order being saved: the same edited order retries
  // with the same key, so an uncertain response is never applied twice.
  private orderKey: { ids: string; key: string } | null = null;
  private tariffEditorView: TariffEditorView | null = null;
  private tariffEditorRequest = 0;
  private orderSaving = false;
  // Outcome of a create or a new version, shown once the editor re-opens on
  // the resulting tariff.
  private tariffNotice: { id: number; notice: Notice } | null = null;
  // Reports unsaved changes of the open screen (tariff editor, edited order).
  // Leaving through navigation or reload asks for confirmation first.
  private unsavedChanges: (() => boolean) | null = null;

  constructor(private readonly root: HTMLElement, private readonly api: AdminApi) {
    this.toasts.setAttribute('aria-live', 'polite');
    window.addEventListener('beforeunload', event => {
      if (this.unsavedChanges?.()) event.preventDefault();
    });
    window.addEventListener('hashchange', event => this.onRoute(event));
    document.addEventListener('visibilitychange', () => void this.checkSession());
    document.addEventListener('keydown', event => {
      if (event.key === 'Escape' && this.shell?.sidebar.dataset.open === 'true') {
        this.setMenu(false);
        this.shell.menuButton.focus();
      }
    });
  }

  async start(): Promise<void> {
    this.renderBoot();
    try {
      const session = await this.api.bootstrap();
      if (session.authenticated) this.enterApp();
      else this.renderLogin();
    } catch (error) {
      this.renderFatal(error);
    }
  }

  private mount(...nodes: HTMLElement[]) {
    this.closeDialog();
    this.unsavedChanges = null;
    this.pendingOrder = null;
    this.tariffsView = null;
    this.tariffEditorView = null;
    this.overview = null;
    this.usersView = null;
    this.detailView = null;
    this.journalView = null;
    window.clearTimeout(this.searchTimer);
    window.clearInterval(this.countdownTimer);
    window.clearTimeout(this.toastTimer);
    this.toasts.replaceChildren();
    this.root.replaceChildren(...nodes, this.toasts);
  }

  // Boot and fatal states ---------------------------------------------------

  private renderBoot() {
    this.view = 'boot';
    this.shell = null;
    const card = el('section', 'auth-card');
    card.setAttribute('role', 'status');
    card.setAttribute('aria-busy', 'true');
    card.setAttribute('aria-label', 'Проверяем сессию');
    const lines = el('div', 'skeleton-group');
    lines.append(el('span', 'skeleton skeleton-title'), el('span', 'skeleton skeleton-text'));
    const fields = el('div', 'skeleton-group');
    fields.append(el('span', 'skeleton skeleton-field'), el('span', 'skeleton skeleton-field'), el('span', 'skeleton skeleton-button'));
    card.append(brand(), lines, fields);
    const screen = el('main', 'auth-screen');
    screen.append(card);
    this.mount(screen);
  }

  private renderFatal(error: unknown) {
    this.view = 'fatal';
    this.shell = null;
    const disabled = error instanceof ApiError && error.code === 'not_found';
    const card = el('section', 'auth-card');
    card.setAttribute('aria-labelledby', 'fatal-title');
    const head = el('div', 'auth-head');
    const title = el('h1', 'auth-title', disabled ? 'Панель отключена' : 'Не удалось открыть панель');
    title.id = 'fatal-title';
    head.append(title, el('p', 'muted', disabled
      ? 'Вход в панель администратора не настроен на сервере. Обратитесь к ответственному за развёртывание.'
      : errorText(error)));
    const retry = button('Повторить', 'btn btn-secondary btn-block');
    retry.addEventListener('click', () => void this.start());
    card.append(brand(), head, retry);
    const screen = el('main', 'auth-screen');
    screen.append(card);
    this.mount(screen);
    retry.focus();
  }

  // Login -------------------------------------------------------------------

  private renderLogin(initial?: Notice) {
    this.view = 'login';
    this.shell = null;
    this.signedInAt = null;
    this.dashboard = null;
    this.usersState = initialUsersState();
    this.journalState = initialJournalState();
    this.detailCache = null;
    this.manageNotice = null;
    this.tariffsCache = null;
    this.tariffsAt = null;
    this.plansCache = null;
    this.orderKey = null;

    const card = el('section', 'auth-card');
    card.setAttribute('aria-labelledby', 'login-title');
    const head = el('div', 'auth-head');
    const title = el('h1', 'auth-title', 'Вход в панель');
    title.id = 'login-title';
    head.append(title, el('p', 'muted', 'Используйте учётные данные администратора.'));

    const form = el('form', 'form');
    form.noValidate = true;
    const status = el('div', 'form-status');
    status.setAttribute('role', 'alert');
    const show = (value: Notice | null) => status.replaceChildren(...(value ? [notice(value)] : []));

    const username = el('input', 'input');
    Object.assign(username, { id: 'login-username', name: 'username', type: 'text', autocomplete: 'username', spellcheck: false, required: true });
    username.setAttribute('autocapitalize', 'none');
    const password = el('input', 'input');
    Object.assign(password, { id: 'login-password', name: 'password', type: 'password', autocomplete: 'current-password', required: true });

    const reveal = el('button', 'icon-button');
    reveal.type = 'button';
    const setRevealed = (on: boolean) => {
      password.type = on ? 'text' : 'password';
      reveal.setAttribute('aria-pressed', String(on));
      reveal.setAttribute('aria-label', on ? 'Скрыть пароль' : 'Показать пароль');
      reveal.replaceChildren(icon(on ? 'eyeOff' : 'eye'));
    };
    setRevealed(false);
    reveal.addEventListener('click', () => {
      setRevealed(password.type === 'password');
      password.focus();
    });

    const userField = field('Логин', username);
    const passField = field('Пароль', password, reveal);
    const submit = el('button', 'btn btn-primary btn-block');
    submit.type = 'submit';
    submit.append(el('span', 'btn-label', 'Войти'));

    form.append(status, userField.root, passField.root, submit);
    card.append(brand(), head, form, el('p', 'auth-foot', 'Сессия завершается после 30 минут бездействия.'));
    const screen = el('main', 'auth-screen');
    screen.append(card);
    this.mount(screen);

    let busy = false;
    const setBusy = (value: boolean) => {
      busy = value;
      submit.disabled = value;
      submit.setAttribute('aria-busy', String(value));
      setLabel(submit, value ? 'Входим…' : 'Войти');
      username.readOnly = value;
      password.readOnly = value;
    };

    // Rate limiting: announce once through the alert region, then count down
    // inside the button so assistive technology is not flooded every second.
    const runCountdown = () => {
      const tick = () => {
        const left = Math.ceil((this.lockedUntil - Date.now()) / 1000);
        if (left <= 0) {
          window.clearInterval(this.countdownTimer);
          submit.disabled = false;
          setLabel(submit, 'Войти');
          show({ tone: 'info', text: 'Можно повторить вход.' });
          return;
        }
        submit.disabled = true;
        setLabel(submit, `Повторить через ${left} с`);
      };
      show({ tone: 'error', text: 'Слишком много попыток входа. Вход временно ограничен.' });
      window.clearInterval(this.countdownTimer);
      tick();
      this.countdownTimer = window.setInterval(tick, 1000);
    };

    form.addEventListener('submit', event => {
      event.preventDefault();
      if (busy || Date.now() < this.lockedUntil) return;
      userField.setError('');
      passField.setError('');
      const user = username.value;
      const secret = password.value;
      let invalid: HTMLInputElement | null = null;
      if (!user.trim()) { userField.setError('Введите логин.'); invalid ??= username; }
      else if (byteLength(user) > limits.usernameBytes) { userField.setError('Логин слишком длинный.'); invalid ??= username; }
      if (!secret) { passField.setError('Введите пароль.'); invalid ??= password; }
      else if (byteLength(secret) > limits.passwordBytes) { passField.setError('Пароль слишком длинный.'); invalid ??= password; }
      if (invalid) { invalid.focus(); return; }

      setRevealed(false);
      setBusy(true);
      show(null);
      void this.api.login(user, secret).then(
        () => {
          password.value = '';
          this.signedInAt = new Date();
          this.enterApp();
        },
        (error: unknown) => {
          setBusy(false);
          if (error instanceof ApiError && error.code === 'unauthorized') {
            password.value = '';
            show({ tone: 'error', text: 'Неверный логин или пароль.' });
            password.focus();
          } else if (error instanceof ApiError && error.code === 'rate_limited') {
            this.lockedUntil = Date.now() + error.retryAfter * 1000;
            runCountdown();
          } else {
            show({ tone: 'error', text: errorText(error) });
          }
        },
      );
    });

    if (Date.now() < this.lockedUntil) runCountdown();
    else if (initial) show(initial);
    username.focus();
  }

  // Application shell -------------------------------------------------------

  private enterApp() {
    this.view = 'app';
    this.lastSessionCheck = Date.now();

    const sidebar = el('aside', 'sidebar');
    sidebar.dataset.open = 'false';
    const bar = el('div', 'sidebar-bar');
    const menuButton = el('button', 'icon-button menu-button');
    menuButton.type = 'button';
    menuButton.setAttribute('aria-controls', 'sidebar-panel');
    bar.append(brand(), menuButton);

    const panel = el('div', 'sidebar-panel');
    panel.id = 'sidebar-panel';
    const nav = el('nav', 'nav');
    nav.setAttribute('aria-label', 'Разделы панели');
    const links = new Map<SectionId, HTMLAnchorElement>();
    for (const section of SECTIONS) {
      const link = el('a', 'nav-link');
      link.href = `#/${section.id}`;
      link.append(icon(section.id), el('span', '', section.label));
      links.set(section.id, link);
      nav.append(link);
    }

    const foot = el('div', 'sidebar-foot');
    const session = el('div', 'session');
    session.append(
      el('p', 'session-name', 'Администратор'),
      el('p', 'session-meta', this.signedInAt ? `Вход выполнен в ${clock(this.signedInAt)}` : 'Сессия активна'),
    );
    const logout = button('Выйти', 'btn btn-ghost btn-block btn-start', 'logout');
    logout.addEventListener('click', () => void this.logout(logout));
    foot.append(session, logout);
    panel.append(nav, foot);
    sidebar.append(bar, panel);

    const content = el('main', 'main');
    const app = el('div', 'app');
    app.append(sidebar, content);
    this.shell = { sidebar, menuButton, links, content };
    menuButton.addEventListener('click', () => this.setMenu(sidebar.dataset.open !== 'true'));
    this.setMenu(false);
    this.mount(app);
    this.renderSection(true);
  }

  private setMenu(open: boolean) {
    if (!this.shell) return;
    const { sidebar, menuButton } = this.shell;
    sidebar.dataset.open = String(open);
    menuButton.setAttribute('aria-expanded', String(open));
    menuButton.setAttribute('aria-label', open ? 'Закрыть меню' : 'Открыть меню');
    menuButton.replaceChildren(icon(open ? 'close' : 'menu', 20));
  }

  private onRoute(event?: HashChangeEvent) {
    if (this.view !== 'app') return;
    if (this.unsavedChanges?.()) {
      if (!window.confirm('Есть несохранённые изменения. Уйти без сохранения?')) {
        // Stay: put the previous address back without rendering anything.
        const previous = event ? new URL(event.oldURL).hash : '';
        if (previous) history.replaceState(null, '', previous);
        return;
      }
    }
    this.unsavedChanges = null;
    this.pendingOrder = null;
    this.setMenu(false);
    this.renderSection(true);
  }

  private renderSection(focus: boolean) {
    if (!this.shell) return;
    const { section, userId, tariffId, sourceId } = currentRoute();
    const canonical = userId !== null ? `#/users/${userId}` : tariffId !== null ? `#/tariffs/${tariffId}`
      : sourceId !== null ? `#/sources/${sourceId}` : `#/${section.id}`;
    if (location.hash !== canonical) history.replaceState(null, '', canonical);
    for (const [id, link] of this.shell.links) {
      if (id === section.id) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    }
    document.title = `${section.label} · RS8 Admin`;

    // Leaving a screen invalidates every request still in flight for it. A
    // mutation already sent still completes on the server; its dialog closes.
    this.closeDialog();
    this.manageNotice = null;
    this.overview = null;
    this.usersView = null;
    this.detailView = null;
    this.journalView = null;
    this.journalRequest++;
    this.sourcesView = null;
    this.sourceDetailView = null;
    this.sourceDetailRequest++;
    this.buildersView = null;
    this.tariffsView = null;
    this.tariffEditorView = null;
    this.unsavedChanges = null;
    this.pendingOrder = null;
    this.tariffsRequest++;
    this.tariffEditorRequest++;
    this.dashboardRequest++;
    this.usersRequest++;
    this.detailRequest++;
    this.sourcesRequest++;
    this.buildersRequest++;
    this.builderEditorRequest++;
    window.clearTimeout(this.searchTimer);

    const header = el('header', 'page-header');
    const heading = el('div', 'page-heading');
    const title = el('h1', 'page-title', section.label);
    title.tabIndex = -1;
    const desc = el('p', 'page-desc', section.description);
    heading.append(title, desc);
    header.append(heading);
    const page = el('div', 'page');

    let load: (() => Promise<void>) | null = null;
    if (userId !== null) {
      const back = el('a', 'back-link');
      back.href = '#/users';
      back.append(icon('back'), el('span', '', 'Назад к пользователям'));
      page.append(back, header, this.buildUserDetail(userId, header, title, desc));
      load = () => this.loadUser();
    } else if (section.id === 'overview') {
      page.append(header, this.buildOverview(header));
      load = () => this.loadDashboard();
    } else if (section.id === 'users') {
      page.append(header, this.buildUsers(header));
      load = () => this.loadUsers();
    } else if (tariffId !== null) {
      const back = el('a', 'back-link');
      back.href = '#/tariffs';
      back.append(icon('back'), el('span', '', 'Назад к тарифам'));
      page.append(back, header, this.buildTariffEditor(tariffId === 'new' ? null : tariffId, title, desc));
      load = () => this.loadTariffEditor();
    } else if (section.id === 'tariffs') {
      page.append(header, this.buildTariffsSection(header));
      load = () => this.loadTariffs();
    } else if (sourceId !== null) {
      const back = el('a', 'back-link');
      back.href = '#/sources';
      back.append(icon('back'), el('span', '', 'Назад к источникам'));
      page.append(back, header, this.buildSourceDetail(sourceId, header, title, desc));
      load = () => this.loadSourceDetail();
    } else if (section.id === 'sources') {
      page.append(header, this.buildSourcesSection(header));
      load = () => this.loadSources();
    } else if (section.id === 'builders') {
      page.append(header, this.buildBuildersSection(header));
      load = () => this.loadBuilders();
    } else if (section.id === 'audit') {
      page.append(header, this.buildJournal(header));
      load = () => this.loadJournal();
    } else if (section.placeholder) {
      const empty = el('section', 'panel empty');
      empty.setAttribute('aria-labelledby', 'empty-title');
      const emptyTitle = el('h2', 'empty-title', section.placeholder.title);
      emptyTitle.id = 'empty-title';
      empty.append(icon(section.id, 20), emptyTitle, el('p', 'empty-text', section.placeholder.text));
      page.append(header, empty);
    }
    this.shell.content.replaceChildren(page);
    // Back on the list, focus returns to the row of the user just viewed.
    const restored = userId === null && section.id === 'users' ? this.returnFocus() : null;
    if (focus) (restored ?? title).focus();
    if (load) void load();
  }

  // Overview ----------------------------------------------------------------

  private buildOverview(header: HTMLElement): HTMLElement {
    const actions = el('div', 'page-actions');
    const updated = el('p', 'updated');
    updated.setAttribute('aria-live', 'polite');
    const refresh = button('Обновить', 'btn btn-secondary btn-sm', 'refresh');
    refresh.addEventListener('click', () => void this.loadDashboard());
    actions.append(updated, refresh);
    header.classList.add('has-actions');
    header.append(actions);

    const body = el('div', 'overview');
    this.overview = { body, refresh, updated };
    if (this.dashboard) this.paintDashboard(this.overview, this.dashboard.data, this.dashboard.at);
    else this.paintSkeleton(this.overview);
    return body;
  }
  private paintSkeleton(view: OverviewView) {
    view.body.setAttribute('aria-busy', 'true');
    view.body.setAttribute('role', 'status');
    view.body.setAttribute('aria-label', 'Загружаем показатели');
    view.body.replaceChildren(...overviewSkeleton());
    view.updated.textContent = '';
  }

  private paintDashboard(view: OverviewView, data: Dashboard, at: Date) {
    view.body.removeAttribute('aria-busy');
    view.body.removeAttribute('role');
    view.body.removeAttribute('aria-label');
    view.body.replaceChildren(...overviewContent(data));
    view.updated.textContent = `Обновлено в ${clockSeconds(at)}`;
  }

  private paintError(view: OverviewView, error: unknown) {
    view.body.removeAttribute('aria-busy');
    view.body.removeAttribute('role');
    view.body.removeAttribute('aria-label');
    const panel = el('section', 'panel empty');
    panel.setAttribute('role', 'alert');
    const title = el('h2', 'empty-title', 'Не удалось загрузить показатели');
    const retry = button('Повторить', 'btn btn-secondary btn-sm', 'refresh');
    retry.addEventListener('click', () => void this.loadDashboard());
    panel.append(icon('alert', 20), title, el('p', 'empty-text', errorText(error)), retry);
    view.body.replaceChildren(panel);
    view.updated.textContent = '';
  }
  private setRefreshing(view: OverviewView, busy: boolean) {
    view.refresh.disabled = busy;
    view.refresh.setAttribute('aria-busy', String(busy));
    setLabel(view.refresh, busy ? 'Обновляем…' : 'Обновить');
  }

  /**
   * Loads the dashboard into the current Overview. A stale response (the
   * administrator navigated away or a newer request started) is dropped. With
   * figures already on screen a failed refresh keeps them and raises a toast;
   * without them the error replaces the skeleton and offers a retry.
   */
  private async loadDashboard() {
    const view = this.overview;
    if (!view) return;
    const request = ++this.dashboardRequest;
    const current = () => this.overview === view && request === this.dashboardRequest;
    this.setRefreshing(view, true);
    if (!this.dashboard) this.paintSkeleton(view);
    try {
      const data = await this.api.dashboard();
      if (!current()) return;
      const at = new Date();
      this.dashboard = { data, at };
      this.lastSessionCheck = Date.now();
      this.paintDashboard(view, data, at);
    } catch (error) {
      if (!current()) return;
      // Confirm a 401 against /admin/session before leaving: re-entering the
      // app on a session the API still rejects would loop without end.
      if (error instanceof ApiError && error.code === 'unauthorized') {
        const session = await this.api.session().catch(() => null);
        if (!current()) return;
        if (session && !session.authenticated) {
          await this.signedOut('Сессия завершилась. Войдите снова.');
          return;
        }
      }
      if (this.dashboard) this.toast(`Не удалось обновить показатели. ${errorText(error)}`);
      else this.paintError(view, error);
    } finally {
      if (current()) this.setRefreshing(view, false);
    }
  }

  // Users -------------------------------------------------------------------

  private buildUsers(header: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadUsers());
    header.classList.add('has-actions');
    header.append(actions);
    const state = this.usersState;

    const search = el('input', 'input search-input');
    Object.assign(search, {
      id: 'users-search', name: 'q', type: 'search', value: state.draft, autocomplete: 'off', spellcheck: false,
      placeholder: 'Telegram ID, ID подписки или @username', maxLength: limits.queryBytes,
    });
    search.setAttribute('enterkeyhint', 'search');
    const searchLabel = el('label', 'sr-only', 'Поиск пользователей');
    searchLabel.htmlFor = search.id;
    const clear = el('button', 'icon-button search-clear');
    clear.type = 'button';
    clear.setAttribute('aria-label', 'Очистить поиск');
    clear.append(icon('close'));
    clear.hidden = search.value === '';
    const searchBox = el('div', 'search');
    searchBox.append(icon('search'), search, clear);

    const status = el('select', 'input select-input');
    status.id = 'users-status';
    status.append(option('', 'Все статусы'), ...STATUS_ROWS.map(row => option(row.key, row.label)));
    status.value = state.query.status;
    const statusCaption = el('label', 'sr-only', 'Статус подписки');
    statusCaption.htmlFor = status.id;
    const statusBox = el('div', 'select');
    statusBox.append(status, icon('chevron'));

    const summary = el('p', 'toolbar-meta');
    summary.setAttribute('aria-live', 'polite');
    const toolbar = el('div', 'toolbar');
    toolbar.setAttribute('role', 'search');
    toolbar.append(searchLabel, searchBox, statusCaption, statusBox, summary);

    const error = el('p', 'field-error');
    error.id = 'users-search-error';
    const setSearchError = (text: string) => {
      error.textContent = text;
      if (text) {
        search.setAttribute('aria-invalid', 'true');
        search.setAttribute('aria-describedby', error.id);
      } else {
        search.removeAttribute('aria-invalid');
        search.removeAttribute('aria-describedby');
      }
    };
    const results = el('div', 'results');
    const view: UsersView = { results, refresh, updated, summary, search, clear, status, setSearchError, pagerFocus: null };
    this.usersView = view;

    // Typing waits for a pause; Enter, clearing and the status filter apply at once.
    const submit = (delay: number) => {
      window.clearTimeout(this.searchTimer);
      const run = () => this.applyUsersQuery(view, { q: search.value.trim(), status: parseStatus(status.value), offset: 0 });
      if (delay > 0) this.searchTimer = window.setTimeout(run, delay);
      else run();
    };
    search.addEventListener('input', () => {
      this.usersState.draft = search.value;
      clear.hidden = search.value === '';
      submit(SEARCH_DEBOUNCE_MS);
    });
    search.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        submit(0);
      } else if (event.key === 'Escape' && search.value !== '') {
        event.preventDefault();
        this.clearSearch(view);
        submit(0);
      }
    });
    clear.addEventListener('click', () => {
      this.clearSearch(view);
      search.focus();
      submit(0);
    });
    status.addEventListener('change', () => submit(0));

    // A search typed but not applied before the list was left applies now.
    const draft = state.draft.trim();
    if (draft !== state.query.q && byteLength(draft) <= limits.queryBytes) state.query = { ...state.query, q: draft, offset: 0 };
    if (state.page && state.pageQuery && state.at) this.paintUsers(view, state.page, state.pageQuery, state.at);
    else this.paintUsersSkeleton(view);

    const wrap = el('div', 'users');
    wrap.append(toolbar, error, results);
    return wrap;
  }

  /** Applies a search or filter change; it always restarts from the first page. */
  private applyUsersQuery(view: UsersView, next: UsersQuery) {
    if (this.usersView !== view) return;
    if (byteLength(next.q) > limits.queryBytes) {
      view.setSearchError('Запрос слишком длинный. Сократите его.');
      return;
    }
    view.setSearchError('');
    const current = this.usersState.query;
    if (next.q === current.q && next.status === current.status) return;
    this.usersState.query = next;
    void this.loadUsers();
  }

  private clearSearch(view: UsersView) {
    view.search.value = '';
    view.clear.hidden = true;
    this.usersState.draft = '';
  }

  private paintUsersSkeleton(view: UsersView) {
    setLoading(view.results, 'Загружаем пользователей');
    view.results.replaceChildren(usersSkeleton());
    view.summary.textContent = '';
    view.updated.textContent = '';
  }

  private paintUsersError(view: UsersView, error: unknown) {
    setLoading(view.results, null);
    view.results.replaceChildren(messagePanel('alert', 'Не удалось загрузить пользователей', errorText(error), 'alert',
      retryButton(() => void this.loadUsers())));
    view.summary.textContent = '';
    view.updated.textContent = '';
  }

  private paintUsers(view: UsersView, page: UsersPage, query: UsersQuery, at: Date) {
    setLoading(view.results, null);
    const filtered = query.q !== '' || query.status !== '';
    view.summary.textContent = page.total > 0 ? `${filtered ? 'Найдено' : 'Всего'}: ${formatCount(page.total)}` : '';
    view.updated.textContent = `Обновлено в ${clockSeconds(at)}`;
    if (page.users.length === 0) {
      view.results.replaceChildren(this.usersEmpty(view, query));
      return;
    }
    // Repainting replaces the rows; keep keyboard focus on the same user.
    const active = document.activeElement;
    const focused = active instanceof HTMLElement && view.results.contains(active) ? active.dataset.user : undefined;

    const panel = el('section', 'panel users-panel');
    panel.setAttribute('aria-label', 'Список пользователей');
    const table = el('table', 'table table-cards users-table');
    table.append(el('caption', 'sr-only', 'Пользователи'), headRow(USER_COLUMNS));
    const body = el('tbody');
    const now = Date.now();
    for (const user of page.users) body.append(this.userRow(user, now));
    table.append(body);
    const wrap = el('div', 'table-wrap');
    wrap.append(table);
    panel.append(wrap, this.pager(view, page));
    view.results.replaceChildren(panel);

    if (focused) panel.querySelector<HTMLElement>(`a[data-user='${focused}']`)?.focus({ preventScroll: true });
    const direction = view.pagerFocus;
    view.pagerFocus = null;
    if (direction) {
      const [prev, next] = panel.querySelectorAll<HTMLButtonElement>('.pager-nav .btn');
      const target = direction === 'prev' ? (prev?.disabled ? next : prev) : (next?.disabled ? prev : next);
      view.results.scrollIntoView({ block: 'start' });
      target?.focus({ preventScroll: true });
    }
  }

  /** Tells an empty service apart from a search or filter with no match. */
  private usersEmpty(view: UsersView, query: UsersQuery): HTMLElement {
    if (query.q === '' && query.status === '') {
      return messagePanel('users', 'Пользователей пока нет',
        'Здесь появятся клиенты, которые привязали Telegram-аккаунт к подписке. Пробные подписки без привязки в список не входят.',
        'status');
    }
    const parts: string[] = [];
    if (query.q) parts.push(`по запросу «${query.q}»`);
    if (query.status) parts.push(`со статусом «${statusLabel(query.status)}»`);
    const hint = /^[0-9]+$/.test(query.q) ? ' Числовой запрос ищет точное совпадение Telegram ID или ID подписки.' : '';
    const reset = button('Сбросить фильтры', 'btn btn-secondary btn-sm');
    reset.addEventListener('click', () => {
      this.clearSearch(view);
      view.status.value = '';
      this.applyUsersQuery(view, { q: '', status: '', offset: 0 });
      view.search.focus();
    });
    return messagePanel('search', 'Пользователи не найдены', `Нет пользователей ${parts.join(' ')}.${hint}`, 'status', reset);
  }

  private userRow(user: AdminSubscription, now: number): HTMLTableRowElement {
    const link = el('a', user.username ? 'user-link' : 'user-link is-anon', user.username ? handle(user.username) : 'Без имени');
    link.href = `#/users/${user.telegram_id}`;
    link.dataset.user = String(user.telegram_id);
    link.addEventListener('click', () => { this.usersState.lastOpened = user.telegram_id; });
    const expired = isActiveExpired(user, now);
    const status = cell('Статус', statusBadge(user.status), 'col-status');
    if (expired) status.append(el('span', 'flag', 'срок истёк'));
    const expiry = cell('Действует до', user.expires_at ? formatDate(user.expires_at) : 'Бессрочно',
      expired ? 'is-danger' : user.expires_at ? '' : 'muted');
    if (user.expires_at) expiry.title = `${formatDateTime(user.expires_at)}, ${relativeTime(user.expires_at, now)}`;
    const row = el('tr', 'row-link');
    row.append(
      cell('Пользователь', link, 'col-user'),
      cell('Telegram ID', String(user.telegram_id), 'col-id'),
      status,
      cell('Тариф', planLabel(user), 'col-md'),
      expiry,
      cell('Последний запрос', user.last_request ? formatDateTime(user.last_request) : 'Не было', user.last_request ? 'col-lg' : 'col-lg muted'),
      cell('Оплата', user.is_paid ? 'Платная' : 'Бесплатная', user.is_paid ? 'col-md' : 'col-md muted'),
    );
    // The whole row opens the user; the link inside keeps it reachable by keyboard.
    row.addEventListener('click', event => {
      if (event.target instanceof Element && event.target.closest('a')) return;
      if (document.getSelection()?.type === 'Range') return;
      link.click();
    });
    return row;
  }

  /** Page controls from the server's total, limit and offset. */
  private pager(view: UsersView, page: UsersPage): HTMLElement {
    const foot = el('div', 'pager');
    const from = page.offset + 1;
    const to = page.offset + page.users.length;
    foot.append(el('p', 'pager-range', `С ${formatCount(from)} по ${formatCount(to)} из ${formatCount(page.total)}`));
    const pages = Math.ceil(page.total / page.limit);
    if (pages <= 1) return foot;
    const nav = el('nav', 'pager-nav');
    nav.setAttribute('aria-label', 'Страницы списка');
    const prev = button('Предыдущая', 'btn btn-secondary btn-sm', 'prev');
    const next = button('Следующая', 'btn btn-secondary btn-sm');
    next.append(icon('next'));
    prev.disabled = page.offset === 0;
    next.disabled = page.offset + page.limit >= page.total;
    const go = (offset: number, direction: 'prev' | 'next') => {
      view.pagerFocus = direction;
      this.usersState.query = { ...this.usersState.query, offset };
      void this.loadUsers();
    };
    prev.addEventListener('click', () => go(Math.max(0, page.offset - page.limit), 'prev'));
    next.addEventListener('click', () => go(page.offset + page.limit, 'next'));
    const current = Math.floor(page.offset / page.limit) + 1;
    nav.append(el('p', 'pager-page', `Страница ${formatCount(current)} из ${formatCount(pages)}`), prev, next);
    foot.append(nav);
    return foot;
  }

  /**
   * Loads the current users query. A stale response (the administrator left
   * the list or a newer query started) is dropped. A failed refresh of the
   * list on screen keeps it and raises a toast; otherwise the error replaces
   * the results and offers a retry.
   */
  private async loadUsers() {
    const view = this.usersView;
    if (!view) return;
    const state = this.usersState;
    const query = state.query;
    const request = ++this.usersRequest;
    const current = () => this.usersView === view && request === this.usersRequest;
    setRefreshBusy(view.refresh, true);
    if (state.page) view.results.setAttribute('aria-busy', 'true');
    else this.paintUsersSkeleton(view);
    try {
      const page = await this.api.users(query);
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      // The list shrank below the requested page: step back to its last page.
      // The offset strictly decreases, so this cannot loop.
      const last = page.total > 0 ? Math.floor((page.total - 1) / page.limit) * page.limit : 0;
      if (page.users.length === 0 && query.offset > last) {
        state.query = { ...query, offset: last };
        void this.loadUsers();
        return;
      }
      state.page = page;
      state.pageQuery = query;
      state.at = new Date();
      this.paintUsers(view, page, query, state.at);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      if (state.page && state.pageQuery && sameQuery(state.pageQuery, query)) {
        view.results.removeAttribute('aria-busy');
        this.toast(`Не удалось обновить список. ${errorText(error)}`);
      } else {
        this.paintUsersError(view, error);
      }
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  /** Back on the list, the row of the user just viewed takes focus (and scrolls into view). */
  private returnFocus(): HTMLElement | null {
    const opened = this.usersState.lastOpened;
    this.usersState.lastOpened = null;
    if (opened === null || !this.usersView) return null;
    return this.usersView.results.querySelector<HTMLElement>(`a[data-user='${opened}']`);
  }

  // Journal -----------------------------------------------------------------

  private buildJournal(header: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadJournal());
    header.classList.add('has-actions');
    header.append(actions);
    const state = this.journalState;

    const search = el('input', 'input search-input');
    Object.assign(search, {
      id: 'journal-search', name: 'q', type: 'search', value: state.draft, autocomplete: 'off', spellcheck: false,
      placeholder: 'Telegram ID, ID подписки или @username', maxLength: limits.queryBytes,
    });
    search.setAttribute('enterkeyhint', 'search');
    const searchLabel = el('label', 'sr-only', 'Поиск по пользователю');
    searchLabel.htmlFor = search.id;
    const clear = el('button', 'icon-button search-clear');
    clear.type = 'button';
    clear.setAttribute('aria-label', 'Очистить поиск');
    clear.append(icon('close'));
    clear.hidden = search.value === '';
    const searchBox = el('div', 'search');
    searchBox.append(icon('search'), search, clear);

    const type = el('select', 'input select-input');
    type.id = 'journal-type';
    type.append(option('', 'Все события'));
    for (const group of JOURNAL_GROUPS) {
      const optgroup = el('optgroup');
      optgroup.label = group.label;
      optgroup.append(...group.types.map(item => option(item, JOURNAL_LABELS[item])));
      type.append(optgroup);
    }
    type.value = state.query.type;
    const typeCaption = el('label', 'sr-only', 'Тип события');
    typeCaption.htmlFor = type.id;
    const typeBox = el('div', 'select');
    typeBox.append(type, icon('chevron'));

    const kind = el('select', 'input select-input');
    kind.id = 'journal-kind';
    kind.append(option('', 'Все типы подписок'), ...PLAN_KINDS.map(item => option(item, PLAN_KIND_LABELS[item])));
    kind.value = state.query.planKind;
    const kindCaption = el('label', 'sr-only', 'Тип подписки');
    kindCaption.htmlFor = kind.id;
    const kindBox = el('div', 'select');
    kindBox.append(kind, icon('chevron'));

    const period = el('div', 'period');
    period.setAttribute('role', 'group');
    period.setAttribute('aria-label', 'Период');
    const dateInput = (id: string, value: string, label: string) => {
      const input = el('input', 'input date-input');
      Object.assign(input, { id, type: 'date', value });
      const caption = el('label', 'period-label', label);
      caption.htmlFor = id;
      period.append(caption, input);
      return input;
    };
    const from = dateInput('journal-from', state.fromDate, 'С');
    const to = dateInput('journal-to', state.toDate, 'по');

    const summary = el('p', 'toolbar-meta');
    summary.setAttribute('aria-live', 'polite');
    const toolbar = el('div', 'toolbar journal-toolbar');
    toolbar.setAttribute('role', 'search');
    toolbar.append(searchLabel, searchBox, typeCaption, typeBox, kindCaption, kindBox, period, summary);

    const error = el('p', 'field-error');
    error.id = 'journal-filter-error';
    error.setAttribute('role', 'alert');
    const setError = (text: string) => { error.textContent = text; };
    const results = el('div', 'results');
    const view: JournalView = { results, refresh, updated, summary, search, clear, type, kind, from, to, setError, pagerFocus: null };
    this.journalView = view;

    // Typing waits for a pause; Enter, clearing, selects and dates apply at once.
    const submit = (delay: number) => {
      window.clearTimeout(this.searchTimer);
      const run = () => this.applyJournalFilters(view);
      if (delay > 0) this.searchTimer = window.setTimeout(run, delay);
      else run();
    };
    search.addEventListener('input', () => {
      this.journalState.draft = search.value;
      clear.hidden = search.value === '';
      submit(SEARCH_DEBOUNCE_MS);
    });
    search.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        submit(0);
      } else if (event.key === 'Escape' && search.value !== '') {
        event.preventDefault();
        search.value = '';
        clear.hidden = true;
        this.journalState.draft = '';
        submit(0);
      }
    });
    clear.addEventListener('click', () => {
      search.value = '';
      clear.hidden = true;
      this.journalState.draft = '';
      search.focus();
      submit(0);
    });
    for (const control of [type, kind, from, to]) control.addEventListener('change', () => submit(0));

    if (state.page && state.pageQuery && state.at) this.paintJournal(view, state.page, state.pageQuery, state.at);
    else this.paintJournalSkeleton(view);

    const wrap = el('div', 'users journal');
    wrap.append(toolbar, error, results);
    return wrap;
  }

  /** Reads the filter controls; a changed filter always restarts from the first page. */
  private applyJournalFilters(view: JournalView) {
    if (this.journalView !== view) return;
    const q = view.search.value.trim();
    if (byteLength(q) > limits.queryBytes) {
      view.setError('Запрос слишком длинный. Сократите его.');
      return;
    }
    const fromDate = view.from.value;
    const toDate = view.to.value;
    if (fromDate && toDate && fromDate > toDate) {
      view.setError('Начало периода позже его окончания.');
      return;
    }
    view.setError('');
    this.journalState.fromDate = fromDate;
    this.journalState.toDate = toDate;
    // The end date is inclusive for the reader: the query ends at the next midnight.
    const next: JournalQuery = {
      q, type: parseJournalType(view.type.value), planKind: parsePlanKind(view.kind.value),
      from: fromDate ? dayStart(fromDate) : '', to: toDate ? dayStart(toDate, 1) : '', offset: 0,
    };
    const current = this.journalState.query;
    if (sameJournalQuery({ ...current, offset: 0 }, next) && current.offset === 0) return;
    this.journalState.query = next;
    void this.loadJournal();
  }

  private paintJournalSkeleton(view: JournalView) {
    setLoading(view.results, 'Загружаем журнал');
    view.results.replaceChildren(usersSkeleton());
    view.summary.textContent = '';
    view.updated.textContent = '';
  }

  private paintJournalError(view: JournalView, error: unknown) {
    setLoading(view.results, null);
    view.results.replaceChildren(messagePanel('alert', 'Не удалось загрузить журнал', errorText(error), 'alert',
      retryButton(() => void this.loadJournal())));
    view.summary.textContent = '';
    view.updated.textContent = '';
  }

  private paintJournal(view: JournalView, page: JournalPage, query: JournalQuery, at: Date) {
    setLoading(view.results, null);
    const filtered = query.q !== '' || query.type !== '' || query.planKind !== '' || query.from !== '' || query.to !== '';
    view.summary.textContent = page.total > 0 ? `${filtered ? 'Найдено' : 'Всего'}: ${formatCount(page.total)}` : '';
    view.updated.textContent = `Обновлено в ${clockSeconds(at)}`;
    if (page.events.length === 0) {
      view.results.replaceChildren(this.journalEmpty(view, filtered));
      return;
    }
    const active = document.activeElement;
    const focused = active instanceof HTMLElement && view.results.contains(active) ? active.dataset.event : undefined;

    const panel = el('section', 'panel users-panel');
    panel.setAttribute('aria-label', 'События журнала');
    const table = el('table', 'table table-cards users-table journal-table');
    table.append(el('caption', 'sr-only', 'Журнал событий, от новых к старым'), headRow(JOURNAL_COLUMNS));
    const body = el('tbody');
    for (const event of page.events) body.append(this.journalRow(event));
    table.append(body);
    const wrap = el('div', 'table-wrap');
    wrap.append(table);
    panel.append(wrap, this.journalPager(view, page));
    view.results.replaceChildren(panel);

    if (focused) panel.querySelector<HTMLElement>(`button[data-event='${focused}']`)?.focus({ preventScroll: true });
    const direction = view.pagerFocus;
    view.pagerFocus = null;
    if (direction) {
      const [prev, next] = panel.querySelectorAll<HTMLButtonElement>('.pager-nav .btn');
      const target = direction === 'prev' ? (prev?.disabled ? next : prev) : (next?.disabled ? prev : next);
      view.results.scrollIntoView({ block: 'start' });
      target?.focus({ preventScroll: true });
    }
  }

  private journalEmpty(view: JournalView, filtered: boolean): HTMLElement {
    if (!filtered) {
      return messagePanel('audit', 'Событий пока нет',
        'Записи появятся автоматически при регистрации пользователей, изменениях подписок и оплатах.', 'status');
    }
    const reset = button('Сбросить фильтры', 'btn btn-secondary btn-sm');
    reset.addEventListener('click', () => {
      view.search.value = '';
      view.clear.hidden = true;
      view.type.value = '';
      view.kind.value = '';
      view.from.value = '';
      view.to.value = '';
      this.journalState.draft = '';
      this.applyJournalFilters(view);
      view.search.focus();
    });
    const hint = /^[0-9]+$/.test(this.journalState.query.q) ? ' Числовой запрос ищет точное совпадение Telegram ID или ID подписки.' : '';
    return messagePanel('search', 'События не найдены', `Нет событий по выбранным условиям.${hint}`, 'status', reset);
  }

  private journalRow(event: JournalEvent): HTMLTableRowElement {
    const subject = journalSubject(event);
    const user = el('span', subject.anon ? 'user-link is-anon' : 'user-link', subject.text);
    const userCell = cell('Пользователь', user, 'col-user');
    if (event.telegram_id > 0) userCell.append(el('span', 'cell-note', String(event.telegram_id)));

    // The event name is the keyboard entry point of the row.
    const open = el('button', 'journal-open', journalLabel(event.event_type));
    open.type = 'button';
    open.dataset.event = String(event.id);
    open.addEventListener('click', () => this.openJournalEvent(event, open));
    const what = cell('Событие', open, 'cell-wrap');
    what.append(el('span', 'cell-note', event.description));

    const plan = cell('Подписка и тариф', event.subscription_id ? `#${event.subscription_id}` : 'Без подписки', 'col-md');
    plan.append(el('span', 'cell-note', planText(event)));
    const actor = cell('Инициатор', actorText(event), 'col-md');
    if (event.actor_name) actor.append(el('span', 'cell-note', actorNameLabel(event.actor_name)));
    const date = cell('Дата и время', formatDateTime(event.created_at), 'col-date');
    date.title = dateTimeSecondsFormat.format(new Date(event.created_at));

    const row = el('tr', 'row-link');
    row.append(date, userCell, what, plan, actor, cell('Статус', outcomeBadge(event.outcome), 'col-status'));
    row.addEventListener('click', clickEvent => {
      if (clickEvent.target instanceof Element && clickEvent.target.closest('button, a')) return;
      if (document.getSelection()?.type === 'Range') return;
      open.click();
    });
    return row;
  }

  private journalPager(view: JournalView, page: JournalPage): HTMLElement {
    const foot = el('div', 'pager');
    const from = page.offset + 1;
    const to = page.offset + page.events.length;
    foot.append(el('p', 'pager-range', `С ${formatCount(from)} по ${formatCount(to)} из ${formatCount(page.total)}`));
    const pages = Math.ceil(page.total / page.limit);
    if (pages <= 1) return foot;
    const nav = el('nav', 'pager-nav');
    nav.setAttribute('aria-label', 'Страницы журнала');
    const prev = button('Предыдущая', 'btn btn-secondary btn-sm', 'prev');
    const next = button('Следующая', 'btn btn-secondary btn-sm');
    next.append(icon('next'));
    prev.disabled = page.offset === 0;
    next.disabled = page.offset + page.limit >= page.total;
    const go = (offset: number, direction: 'prev' | 'next') => {
      view.pagerFocus = direction;
      this.journalState.query = { ...this.journalState.query, offset };
      void this.loadJournal();
    };
    prev.addEventListener('click', () => go(Math.max(0, page.offset - page.limit), 'prev'));
    next.addEventListener('click', () => go(page.offset + page.limit, 'next'));
    const current = Math.floor(page.offset / page.limit) + 1;
    nav.append(el('p', 'pager-page', `Страница ${formatCount(current)} из ${formatCount(pages)}`), prev, next);
    foot.append(nav);
    return foot;
  }

  /**
   * Loads the current journal query. A stale response is dropped; a failed
   * refresh of the page on screen keeps it and raises a toast. Reading never
   * changes the journal: refreshing the page cannot create events.
   */
  private async loadJournal() {
    const view = this.journalView;
    if (!view) return;
    const state = this.journalState;
    const query = state.query;
    const request = ++this.journalRequest;
    const current = () => this.journalView === view && request === this.journalRequest;
    setRefreshBusy(view.refresh, true);
    if (state.page) view.results.setAttribute('aria-busy', 'true');
    else this.paintJournalSkeleton(view);
    try {
      const page = await this.api.journal(query);
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      // New events can shift the list: step back when the page ran empty.
      const last = page.total > 0 ? Math.floor((page.total - 1) / page.limit) * page.limit : 0;
      if (page.events.length === 0 && query.offset > last) {
        state.query = { ...query, offset: last };
        void this.loadJournal();
        return;
      }
      state.page = page;
      state.pageQuery = query;
      state.at = new Date();
      this.paintJournal(view, page, query, state.at);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      if (state.page && state.pageQuery && sameJournalQuery(state.pageQuery, query)) {
        view.results.removeAttribute('aria-busy');
        this.toast(`Не удалось обновить журнал. ${errorText(error)}`);
      } else {
        this.paintJournalError(view, error);
      }
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  /** Read-only details of one event; the journal offers no way to edit it. */
  private openJournalEvent(event: JournalEvent, trigger: HTMLElement) {
    if (this.dialog || this.view !== 'app') return;
    const dialog = el('dialog', 'modal modal-wide');
    dialog.setAttribute('aria-labelledby', 'journal-dialog-title');
    dialog.setAttribute('aria-describedby', 'journal-dialog-text');
    const heading = el('h2', 'modal-title', journalLabel(event.event_type));
    heading.id = 'journal-dialog-title';
    const text = el('p', 'modal-text', event.description);
    text.id = 'journal-dialog-text';

    const list = el('dl', 'summary');
    const row = (label: string, value: string | Node) => {
      const item = el('div', 'summary-row');
      const data = el('dd');
      data.append(value);
      item.append(el('dt', '', label), data);
      list.append(item);
    };
    row('Дата и время', dateTimeSecondsFormat.format(new Date(event.created_at)));
    row('Статус', outcomeBadge(event.outcome));
    const subject = journalSubject(event);
    if (event.telegram_id > 0) {
      const link = el('a', 'inline-link', `${subject.text} · ${event.telegram_id}`);
      link.href = `#/users/${event.telegram_id}`;
      row('Пользователь', link);
    } else {
      row('Пользователь', subject.text);
    }
    row('Подписка', event.subscription_id ? `#${event.subscription_id}` : 'Без подписки');
    row('Тариф', planText(event));
    row('Инициатор', event.actor_name ? `${actorText(event)} · ${actorNameLabel(event.actor_name)}` : actorText(event));
    if (event.order_id !== null) row('Заказ', `#${event.order_id}`);
    if (event.amount_cents !== null) row('Сумма', formatPrice(event.amount_cents, event.currency));
    const days = event.details.days;
    if (typeof days === 'number' && Number.isSafeInteger(days) && days > 0) row('Дней', formatCount(days));
    if (event.before || event.after) {
      row('Статус подписки', changeNode(stateValue(event.before, 'status'), stateValue(event.after, 'status')));
      row('Действует до', changeNode(stateValue(event.before, 'expiry'), stateValue(event.after, 'expiry')));
      row('Тариф подписки', changeNode(stateValue(event.before, 'plan'), stateValue(event.after, 'plan')));
    }
    row('ID события', `#${event.id}`);

    const body = el('div', 'modal-body');
    body.append(heading, text, list);
    if (event.details.backfill === true) {
      body.append(el('p', 'field-hint', 'Запись восстановлена из истории при обновлении системы: время и состав данных соответствуют сохранённым фактам.'));
    }
    const close = button('Закрыть', 'btn btn-secondary');
    const actions = el('div', 'modal-actions');
    actions.append(close);
    body.append(actions);
    dialog.append(body);

    const handle = {
      dismiss: () => {
        if (dialog.open) dialog.close();
        dialog.remove();
      },
    };
    const finish = () => {
      if (this.dialog === handle) this.dialog = null;
      handle.dismiss();
      if (trigger.isConnected) trigger.focus();
    };
    close.addEventListener('click', finish);
    dialog.addEventListener('cancel', cancelEvent => { cancelEvent.preventDefault(); finish(); });
    // A click on the backdrop (outside the dialog box) closes it too.
    dialog.addEventListener('click', clickEvent => { if (clickEvent.target === dialog) finish(); });
    this.dialog = handle;
    this.root.append(dialog);
    dialog.showModal();
    close.focus();
  }

  // User detail -------------------------------------------------------------

  private buildUserDetail(id: number, header: HTMLElement, title: HTMLElement, desc: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadUser());
    header.classList.add('has-actions');
    header.append(actions);
    const cached = this.detailCache && this.detailCache.id === id ? this.detailCache : null;
    // The list row, if any, names the user while the page loads.
    const known = cached?.data.subscription ?? this.usersState.page?.users.find(user => user.telegram_id === id);
    title.textContent = known ? displayName(known) : 'Пользователь';
    desc.textContent = `Telegram ID ${id}`;
    const body = el('div', 'detail');
    const view: DetailView = { id, body, refresh, updated, title, desc };
    this.detailView = view;
    if (cached) this.paintDetail(view, cached.data, cached.at);
    else this.paintDetailSkeleton(view);
    return body;
  }

  private paintDetailSkeleton(view: DetailView) {
    setLoading(view.body, 'Загружаем пользователя');
    view.body.replaceChildren(...detailSkeleton());
    view.updated.textContent = '';
  }

  private paintDetail(view: DetailView, data: UserDetail, at: Date) {
    setLoading(view.body, null);
    const user = data.subscription;
    const name = displayName(user);
    view.title.textContent = name;
    view.desc.textContent = `Telegram ID ${user.telegram_id} · Подписка #${user.id}`;
    document.title = `${name} · RS8 Admin`;
    view.updated.textContent = `Обновлено в ${clockSeconds(at)}`;
    // Repainting replaces every node; keep keyboard focus on the same control.
    const active = document.activeElement;
    const focusKey = active instanceof HTMLElement && view.body.contains(active) ? active.dataset.focusKey : undefined;
    const now = Date.now();
    const grid = el('div', 'detail-grid');
    grid.append(subscriptionPanel(user, data.plan, now), planPanel(user, data.plan));
    view.body.replaceChildren(grid, this.managePanel(data, now), this.builderPanel(data, view), nodesPanel(user, data.nodes));
    if (focusKey) this.focusManage(focusKey);
  }

  /** The "Управление подпиской" panel: actions valid for the current status plus the last outcome. */
  private managePanel(data: UserDetail, now: number): HTMLElement {
    const sub = data.subscription;
    const panel = el('section', 'panel manage');
    panel.setAttribute('aria-labelledby', 'manage-title');
    const head = panelHead('manage-title', 'Управление подпиской', `#${sub.id}`);
    const heading = head.querySelector<HTMLElement>('.panel-title');
    if (heading) {
      heading.tabIndex = -1;
      heading.dataset.focusKey = 'manage-title';
    }
    panel.append(head);
    const shown = this.manageNotice?.subscriptionId === sub.id ? this.manageNotice.notice : null;
    if (shown) {
      const slot = el('div', 'manage-notice');
      const node = notice(shown);
      node.tabIndex = -1;
      node.dataset.focusKey = 'manage-notice';
      node.setAttribute('role', shown.tone === 'error' ? 'alert' : 'status');
      slot.append(node);
      panel.append(slot);
    }
    const items = manageItems(sub, now);
    if (items.length === 0) {
      panel.append(el('p', 'panel-note', unmanageableText(sub.status)));
      return panel;
    }
    const list = el('ul', 'manage-list');
    for (const item of items) {
      const entry = el('li', 'manage-item');
      const textId = `manage-${item.action}-text`;
      const text = el('p', item.blocked ? 'manage-text is-blocked' : 'manage-text', item.blocked ?? item.text);
      text.id = textId;
      const trigger = button(item.label, `${btnClass(item.tone)} btn-sm`);
      trigger.dataset.focusKey = `manage-${item.action}`;
      trigger.setAttribute('aria-describedby', textId);
      trigger.disabled = item.blocked !== undefined;
      trigger.addEventListener('click', () => this.openManage(item.action, data));
      entry.append(el('p', 'manage-title', item.title), text, trigger);
      list.append(entry);
    }
    panel.append(list);
    return panel;
  }

  /** Focuses a management control by key; a vanished or disabled one falls back to the panel title. */
  private focusManage(key: string) {
    const body = this.detailView?.body;
    if (!body) return;
    const target = body.querySelector<HTMLElement>(`[data-focus-key='${key}']`);
    const usable = target && !(target instanceof HTMLButtonElement && target.disabled) ? target : null;
    const fallback = key.startsWith('manage-') ? body.querySelector<HTMLElement>("[data-focus-key='manage-title']") : null;
    (usable ?? fallback)?.focus({ preventScroll: usable === null });
  }

  private closeDialog() {
    const open = this.dialog;
    this.dialog = null;
    open?.dismiss();
  }

  /** Keeps the users list snapshot in step with a subscription changed here. */
  private patchUsersRow(sub: AdminSubscription) {
    const page = this.usersState.page;
    if (!page || !page.users.some(user => user.id === sub.id)) return;
    this.usersState.page = { ...page, users: page.users.map(user => (user.id === sub.id ? sub : user)) };
  }

  /**
   * Confirmation dialog for one mutation: idle → confirmation → submitting →
   * success | error. The request key is fixed per confirmed payload: an
   * uncertain outcome (network, timeout, 5xx) freezes the parameters so the
   * retry replays instead of applying twice. Nothing is retried automatically.
   */
  private openManage(action: MutationAction, data: UserDetail) {
    const view = this.detailView;
    if (!view || this.dialog || this.view !== 'app') return;
    const before = data.subscription;
    const telegramId = before.telegram_id;
    const opened = Date.now();

    const spec: Readonly<Record<MutationAction, { title: string; text: string; confirm: string; tone: ManageTone }>> = {
      renew: {
        title: 'Продлить подписку?', confirm: 'Продлить', tone: 'primary',
        text: 'Дни добавляются к текущему сроку, а если он уже истёк — к текущему моменту. Счётчик напоминаний об окончании сбрасывается.',
      },
      expiry: {
        title: 'Изменить срок подписки?', confirm: 'Изменить срок', tone: 'primary',
        text: 'Новая дата окончания заменит текущую. Счётчик напоминаний об окончании сбрасывается.',
      },
      disable: {
        title: 'Приостановить подписку?', confirm: 'Приостановить', tone: 'danger',
        text: 'Доступ к VPN будет отключён. Срок действия не изменится, подписку можно возобновить, пока он не истёк.',
      },
      enable: {
        title: 'Возобновить подписку?', confirm: 'Возобновить', tone: 'primary',
        text: 'Подписка станет активной, доступ к VPN будет восстановлен.',
      },
    };
    const { title: titleText, text: explanation, confirm: confirmText, tone } = spec[action];

    const dialog = el('dialog', 'modal');
    dialog.setAttribute('aria-labelledby', 'manage-dialog-title');
    dialog.setAttribute('aria-describedby', 'manage-dialog-text');
    const title = el('h2', 'modal-title', titleText);
    title.id = 'manage-dialog-title';
    const text = el('p', 'modal-text', explanation);
    text.id = 'manage-dialog-text';

    const statusValue = el('span');
    const expiryValue = el('span');
    const summary = el('dl', 'summary');
    summary.append(
      summaryRow('Пользователь', `${displayName(before)} · ${telegramId}`),
      summaryRow('Подписка', `#${before.id}`),
      summaryRow('Статус', statusValue),
      summaryRow('Действует до', expiryValue),
    );

    // Parameters: days for renew, a local date and time for expiry.
    const controls = el('div', 'modal-controls');
    let input: HTMLInputElement | null = null;
    let setFieldError: (message: string) => void = () => undefined;
    const presets: HTMLButtonElement[] = [];
    if (action === 'renew') {
      input = el('input', 'input');
      Object.assign(input, { id: 'manage-days', name: 'days', type: 'number', min: '1', max: String(RENEW_MAX_DAYS), step: '1', value: '30' });
      input.inputMode = 'numeric';
      const days = field('Количество дней', input);
      setFieldError = days.setError;
      const chips = el('div', 'chips');
      chips.setAttribute('role', 'group');
      chips.setAttribute('aria-label', 'Быстрый выбор');
      for (const preset of [7, 30, 90, 365]) {
        const chip = button(`${preset} дн.`, 'chip');
        chip.dataset.days = String(preset);
        chip.addEventListener('click', () => {
          if (!input || input.readOnly) return;
          input.value = String(preset);
          input.dispatchEvent(new Event('input'));
        });
        presets.push(chip);
        chips.append(chip);
      }
      controls.append(days.root, chips, el('p', 'field-hint', `От 1 до ${formatCount(RENEW_MAX_DAYS)} дней.`));
    } else if (action === 'expiry') {
      input = el('input', 'input');
      const current = before.expires_at ? new Date(before.expires_at) : null;
      const initial = current && current.getTime() > opened ? current : new Date(opened + 30 * DAY_MS);
      Object.assign(input, { id: 'manage-expiry', name: 'expires_at', type: 'datetime-local', step: '60', value: localInputValue(initial), max: '9999-12-31T23:59' });
      input.min = localInputValue(new Date(opened + MINUTE_MS));
      const date = field('Новый срок', input);
      setFieldError = date.setError;
      controls.append(date.root, el('p', 'field-hint', `Дата и время в часовом поясе браузера (${timeZoneLabel()}).`));
    }

    /** The mutation for the current parameters, or an error message. */
    const read = (): Mutation | string => {
      if (action === 'renew') {
        const raw = input?.value.trim() ?? '';
        const days = /^\d{1,4}$/.test(raw) ? Number(raw) : NaN;
        if (!Number.isInteger(days) || days < 1 || days > RENEW_MAX_DAYS) return `Укажите целое число дней от 1 до ${formatCount(RENEW_MAX_DAYS)}.`;
        return { action, days };
      }
      if (action === 'expiry') {
        const date = parseLocalInput(input?.value ?? '');
        if (!date) return 'Укажите корректные дату и время.';
        if (date.getFullYear() > 9999) return 'Год не может быть больше 9999.';
        if (date.getTime() <= Date.now()) return 'Новый срок должен быть в будущем.';
        if (before.expires_at && Math.floor(date.getTime() / MINUTE_MS) === Math.floor(Date.parse(before.expires_at) / MINUTE_MS)) {
          return 'Новый срок совпадает с текущим.';
        }
        return { action, expiresAt: toRFC3339(date) };
      }
      return { action };
    };

    const preview = () => {
      const mutation = read();
      const valid = typeof mutation !== 'string';
      const now = Date.now();
      let expiresAt: string | null = before.expires_at;
      if (valid && mutation.action === 'renew') expiresAt = renewedExpiry(before, mutation.days, now);
      if (valid && mutation.action === 'expiry') expiresAt = mutation.expiresAt;
      const parametric = action === 'renew' || action === 'expiry';
      const nextExpiry = parametric && !valid ? null : expiryLabel(expiresAt);
      statusValue.replaceChildren(change(statusLabel(before.status), parametric && !valid ? null : statusLabel(nextStatus(before, action, expiresAt, now))));
      expiryValue.replaceChildren(change(expiryLabel(before.expires_at), nextExpiry));
      for (const chip of presets) chip.setAttribute('aria-pressed', String(valid && mutation.action === 'renew' && chip.dataset.days === String(mutation.days)));
    };

    const status = el('div', 'modal-status');
    status.setAttribute('role', 'alert');
    const show = (value: Notice | null) => status.replaceChildren(...(value ? [notice(value)] : []));
    const cancel = button('Отмена', 'btn btn-secondary');
    const confirm = button(confirmText, btnClass(tone));
    const actions = el('div', 'modal-actions');
    actions.append(cancel, confirm);
    const body = el('div', 'modal-body');
    body.append(title, text, summary);
    if (input) body.append(controls);
    if (action === 'renew') body.append(el('p', 'field-hint', 'Новый срок рассчитан предварительно: точное значение сервер вычисляет в момент выполнения.'));
    body.append(status, actions);
    dialog.append(body);

    let key = newRequestKey();
    let submitting = false;
    // An uncertain or committed attempt freezes the payload under its key.
    let frozen = false;
    // The page may no longer match the server: refresh it when the dialog closes.
    let stale = false;
    // A final answer (404): the dialog can only be closed.
    let settled = false;
    let confirmLabel = confirmText;
    let retryAt = 0;
    let cooldown = 0;

    const sync = () => {
      const waiting = Date.now() < retryAt;
      confirm.disabled = submitting || settled || waiting;
      confirm.setAttribute('aria-busy', String(submitting));
      cancel.disabled = submitting;
      if (input) input.readOnly = submitting || frozen;
      for (const chip of presets) chip.disabled = submitting || frozen;
      if (submitting) setLabel(confirm, 'Выполняем…');
      else if (waiting) setLabel(confirm, `Повторить через ${Math.ceil((retryAt - Date.now()) / 1000)} с`);
      else setLabel(confirm, confirmLabel);
    };

    const handle = {
      dismiss: () => {
        window.clearInterval(cooldown);
        if (dialog.open) dialog.close();
        dialog.remove();
      },
    };
    const isOpen = () => this.dialog === handle;

    const close = (focusKey: string) => {
      if (!isOpen()) return;
      this.dialog = null;
      handle.dismiss();
      this.focusManage(focusKey);
      if (stale) void this.loadUser(before.id);
    };
    /** Ends the dialog with an outcome shown on the page. */
    const finish = (value: Notice) => {
      this.manageNotice = { subscriptionId: before.id, notice: value };
      stale = true;
      const detail = this.detailView;
      const cached = this.detailCache;
      if (detail && cached && cached.id === telegramId) this.paintDetail(detail, cached.data, cached.at);
      close('manage-notice');
    };

    input?.addEventListener('input', () => {
      // A changed payload is a new request; a frozen one keeps its key.
      if (frozen) return;
      key = newRequestKey();
      setFieldError('');
      if (!submitting) show(null);
      preview();
    });
    input?.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        confirm.click();
      }
    });
    cancel.addEventListener('click', () => close(`manage-${action}`));
    // Escape closes only while nothing is in flight. The keydown is handled
    // here so the browser cannot close the dialog mid-request on its own.
    dialog.addEventListener('keydown', event => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      if (!submitting) close(`manage-${action}`);
    });
    dialog.addEventListener('cancel', event => {
      event.preventDefault();
      if (!submitting) close(`manage-${action}`);
    });
    // Closed by the browser anyway: forget it; an in-flight answer is dropped.
    dialog.addEventListener('close', () => {
      if (!isOpen()) return;
      this.dialog = null;
      handle.dismiss();
      this.focusManage(`manage-${action}`);
      if (stale || submitting) void this.loadUser(before.id);
    });

    confirm.addEventListener('click', () => {
      if (submitting || settled || Date.now() < retryAt) return;
      const mutation = read();
      if (typeof mutation === 'string') {
        setFieldError(mutation);
        input?.focus();
        return;
      }
      submitting = true;
      show(null);
      sync();
      void this.api.mutate(before.id, mutation, key).then(
        outcome => {
          submitting = false;
          if (!isOpen()) {
            // The page moved on; drop cached copies that are now outdated.
            if (this.detailCache?.id === telegramId) this.detailCache = null;
            return;
          }
          this.lastSessionCheck = Date.now();
          const cached = this.detailCache;
          if (cached && cached.id === telegramId) {
            // The mutation response carries no plan name; the plan is unchanged.
            const subscription = { ...outcome.subscription, plan_name: outcome.subscription.plan_name || cached.data.subscription.plan_name };
            this.detailCache = { ...cached, data: { ...cached.data, subscription }, at: new Date() };
            this.patchUsersRow(subscription);
          }
          finish({ tone: 'success', text: successText(action, before, outcome) });
        },
        async (error: unknown) => {
          submitting = false;
          if (!isOpen()) {
            if (this.detailCache?.id === telegramId) this.detailCache = null;
            return;
          }
          if (await this.endIfSignedOut(error, isOpen)) return;
          if (!isOpen()) return;
          const code = error instanceof ApiError ? error.code : '';
          const httpStatus = error instanceof ApiError ? error.status : 0;
          if (httpStatus === 409 && code in REJECTIONS) {
            finish({ tone: 'error', text: `${ACTION_FAILED[action]} ${REJECTIONS[code]}` });
            return;
          }
          if (httpStatus === 404) {
            settled = true;
            stale = true;
            show({ tone: 'error', text: 'Подписка не найдена. Возможно, она удалена. Закройте окно, чтобы обновить страницу.' });
          } else if (code === 'sync_failed') {
            // Committed and audited; only the node sync setup failed. The
            // same key replays the change and re-runs the sync.
            frozen = true;
            stale = true;
            confirmLabel = 'Повторить синхронизацию';
            show({ tone: 'error', text: 'Изменение сохранено, но синхронизация с узлами не запущена. Повторите: изменение не применится второй раз.' });
            void this.loadUser(before.id);
          } else if (httpStatus === 401) {
            // Rejected before the service; the session check above found it alive.
            show({ tone: 'error', text: `${ACTION_FAILED[action]} ${errorText(error)}` });
          } else if (httpStatus === 400) {
            show({ tone: 'error', text: `${ACTION_FAILED[action]} Сервер отклонил параметры запроса. Проверьте значения.` });
          } else if (httpStatus === 403) {
            show({ tone: 'error', text: `${ACTION_FAILED[action]} Сервер отклонил запрос: нет доступа или устарел ключ защиты сессии. Повторите попытку.` });
            // Refresh the session's CSRF token for a manual retry; sign out only
            // if the session is really gone (no logout loop on a live session).
            void this.api.session().then(
              session => { if (!session.authenticated && this.view === 'app') void this.signedOut('Сессия завершилась. Войдите снова.'); },
              () => undefined,
            );
          } else if (httpStatus === 429 && error instanceof ApiError) {
            retryAt = Date.now() + error.retryAfter * 1000;
            show({ tone: 'error', text: `${ACTION_FAILED[action]} ${errorText(error)}` });
            window.clearInterval(cooldown);
            cooldown = window.setInterval(() => {
              sync();
              if (Date.now() >= retryAt) window.clearInterval(cooldown);
            }, 1000);
          } else {
            // Network, timeout, 5xx or an unreadable answer: the change may
            // or may not have been applied. Retrying with this key is safe.
            frozen = true;
            stale = true;
            confirmLabel = 'Повторить';
            show({ tone: 'error', text: `Не удалось подтвердить результат. ${errorText(error)} Повтор безопасен: изменение не применится дважды.` });
          }
          sync();
          if (!settled && !confirm.disabled) confirm.focus();
          else cancel.focus();
        },
      );
    });

    preview();
    sync();
    this.dialog = handle;
    this.manageNotice = null;
    const panelNotice = view.body.querySelector('.manage-notice');
    panelNotice?.remove();
    this.root.append(dialog);
    dialog.showModal();
    (input ?? cancel).focus();
  }

  private paintDetailMissing(view: DetailView) {
    setLoading(view.body, null);
    view.title.textContent = 'Пользователь';
    view.updated.textContent = '';
    const back = el('a', 'btn btn-secondary btn-sm', 'К списку пользователей');
    back.href = '#/users';
    view.body.replaceChildren(messagePanel('users', 'Пользователь не найден',
      `Среди клиентов с привязанным Telegram-аккаунтом нет пользователя с Telegram ID ${view.id}.`, 'status', back));
  }

  private paintDetailError(view: DetailView, error: unknown) {
    setLoading(view.body, null);
    view.updated.textContent = '';
    view.body.replaceChildren(messagePanel('alert', 'Не удалось загрузить пользователя', errorText(error), 'alert',
      retryButton(() => void this.loadUser())));
  }

  /**
   * Same policy as the list: stale responses are dropped, data on screen
   * survives a failed refresh. After a management action the page is re-read
   * by subscription ID (GET /admin/api/subscriptions/{id}).
   */
  private async loadUser(subscriptionId?: number) {
    const view = this.detailView;
    if (!view) return;
    const request = ++this.detailRequest;
    const current = () => this.detailView === view && request === this.detailRequest;
    const shown = this.detailCache !== null && this.detailCache.id === view.id;
    setRefreshBusy(view.refresh, true);
    if (!shown) this.paintDetailSkeleton(view);
    try {
      const data = subscriptionId === undefined ? await this.api.user(view.id) : await this.api.subscription(subscriptionId, view.id);
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      const at = new Date();
      this.detailCache = { id: view.id, data, at };
      this.patchUsersRow(data.subscription);
      this.paintDetail(view, data, at);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      if (error instanceof ApiError && error.status === 404) {
        if (this.detailCache && this.detailCache.id === view.id) this.detailCache = null;
        this.paintDetailMissing(view);
      } else if (shown) {
        this.toast(`Не удалось обновить данные пользователя. ${errorText(error)}`);
      } else {
        this.paintDetailError(view, error);
      }
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  /**
   * Confirms a 401 against /admin/session and signs out when the session is
   * really gone; true when the caller must stop. Re-entering the app on a
   * session the API still rejects would loop without end, so a live session
   * leaves the error to the caller.
   */
  private async endIfSignedOut(error: unknown, current: () => boolean): Promise<boolean> {
    if (!(error instanceof ApiError && error.code === 'unauthorized')) return false;
    const session = await this.api.session().catch(() => null);
    if (!current()) return true;
    if (session && !session.authenticated) {
      await this.signedOut('Сессия завершилась. Войдите снова.');
      return true;
    }
    return false;
  }

  // Session lifecycle -------------------------------------------------------

  /** Re-validates the session when the tab returns to the foreground. */
  private async checkSession() {
    if (this.view !== 'app' || document.visibilityState !== 'visible') return;
    if (Date.now() - this.lastSessionCheck < SESSION_CHECK_INTERVAL_MS) return;
    this.lastSessionCheck = Date.now();
    try {
      const session = await this.api.session();
      if (!session.authenticated && this.view === 'app') await this.signedOut('Сессия завершилась. Войдите снова.');
    } catch {
      // A transient network failure must not sign the administrator out; the
      // next protected request or check reports the real session state.
    }
  }

  private async logout(trigger: HTMLButtonElement) {
    trigger.disabled = true;
    trigger.setAttribute('aria-busy', 'true');
    setLabel(trigger, 'Выходим…');
    try {
      await this.api.logout();
    } catch (error) {
      trigger.disabled = false;
      trigger.removeAttribute('aria-busy');
      setLabel(trigger, 'Выйти');
      this.toast(`Не удалось выйти. ${errorText(error)}`);
      return;
    }
    await this.signedOut('Вы вышли из панели.');
  }

  /** Obtains a fresh pre-login session and returns to the login screen. */
  private async signedOut(message: string) {
    try {
      const session = await this.api.bootstrap();
      if (session.authenticated) { this.enterApp(); return; }
    } catch (error) {
      this.renderFatal(error);
      return;
    }
    this.renderLogin({ tone: 'info', text: message });
  }

  private toast(text: string) {
    const node = el('div', 'toast');
    node.append(icon('alert'), el('p', '', text));
    this.toasts.replaceChildren(node);
    window.clearTimeout(this.toastTimer);
    this.toastTimer = window.setTimeout(() => node.remove(), TOAST_MS);
  }

  // ===========================================================================
  // Builder assignment panel (Plan → Builder, Subscription → Builder override)
  // ===========================================================================

  /** Renders the "Построитель" panel on the subscription detail page. */
  private builderPanel(data: UserDetail, _view: DetailView): HTMLElement {
    const sub = data.subscription;
    const plan = data.plan;
    const panel = el('section', 'panel');
    panel.setAttribute('aria-labelledby', 'builder-assign-title');
    panel.append(panelHead('builder-assign-title', 'Построитель'));

    const facts = el('dl', 'facts facts-single');

    const mkBuilderFact = (label: string, builderId: number | null, note: string, onEdit: () => void, disabled = false) => {
      const name = builderId !== null
        ? this.buildersCache?.find(b => b.id === builderId)?.name ?? `Построитель #${builderId}`
        : '—';
      const factEl = fact(label, name, note);
      const btn = button('Изменить', 'btn btn-secondary btn-xs');
      btn.disabled = disabled;
      btn.addEventListener('click', onEdit);
      factEl.querySelector('dd')?.append(btn);
      return factEl;
    };

    // Plan builder
    const planBuilderId = plan?.subscription_builder_id ?? null;
    facts.append(mkBuilderFact(
      'Построитель плана',
      planBuilderId,
      plan ? 'Применяется ко всем подпискам этого тарифа' : 'Тариф не найден',
      () => {
        if (!plan) return;
        const view = this.detailView;
        void this.planImpact(plan.id, plan.name).then(impact => {
          if (this.detailView !== view) return;
          this.openBuilderAssignDialog('plan', plan.id, planBuilderId, (newId) => {
            void this.loadUser();
            this.toast(newId === null ? 'Построитель плана снят.' : 'Построитель плана назначен.');
          }, impact);
        });
      },
      !plan,
    ));

    // Subscription override
    const subBuilderId = sub.subscription_builder_id;
    facts.append(mkBuilderFact(
      'Переопределение подписки',
      subBuilderId,
      subBuilderId !== null ? 'Переопределяет построитель плана для этой подписки' : 'Используется построитель плана',
      () => {
        this.openBuilderAssignDialog('subscription', sub.id, subBuilderId, (newId) => {
          void this.loadUser();
          this.toast(newId === null ? 'Переопределение построителя снято.' : 'Построитель подписки назначен.');
        });
      },
    ));

    panel.append(facts);
    return panel;
  }

  /** Opens a dialog to assign or clear a builder for a plan or subscription. */
  private openBuilderAssignDialog(
    target: 'plan' | 'subscription',
    targetId: number,
    currentBuilderId: number | null,
    onDone: (newBuilderId: number | null) => void,
    impact?: PlanImpact,
  ) {
    if (this.dialog || this.view !== 'app') return;

    const builders = this.buildersCache ?? [];
    const dialog = el('dialog', 'modal modal-wide');
    dialog.setAttribute('aria-labelledby', 'ba-title');

    const titleText = target === 'plan' ? 'Построитель плана' : 'Построитель подписки';
    const titleEl = el('h2', 'modal-title', titleText);
    titleEl.id = 'ba-title';

    // Select
    const sel = el('select', 'input');
    sel.id = 'ba-builder';
    const optNone = el('option', '', target === 'plan' ? '— Без построителя —' : '— Использовать построитель плана —');
    optNone.value = '';
    sel.append(optNone);
    for (const b of builders) {
      const opt = el('option', '', b.name + (b.enabled ? '' : ' (отключён)'));
      opt.value = String(b.id);
      sel.append(opt);
    }
    sel.value = currentBuilderId !== null ? String(currentBuilderId) : '';

    const selField = el('div', 'field');
    const selLabel = el('label', 'field-label', 'Построитель');
    selLabel.htmlFor = 'ba-builder';
    const selControl = el('div', 'control');
    selControl.append(sel);
    selField.append(selLabel, selControl);

    if (builders.length === 0) {
      selField.append(el('p', 'field-hint', 'Построители не загружены. Откройте раздел «Построители» и вернитесь.'));
    }

    const statusEl = el('div', 'modal-status');
    statusEl.setAttribute('role', 'alert');
    const showStatus = (n: Notice | null) => statusEl.replaceChildren(...(n ? [notice(n)] : []));

    const cancelBtn = button('Отмена', 'btn btn-secondary');
    const saveBtn = button('Сохранить', 'btn btn-primary');
    const actionsRow = el('div', 'modal-actions');
    actionsRow.append(cancelBtn, saveBtn);

    const body = el('div', 'modal-body');
    body.append(titleEl);
    // A plan's builder applies to every subscription of the plan: say how many
    // before the change, and tie the warning to the picker for screen readers.
    if (target === 'plan' && impact) {
      const warning = notice({ tone: 'info', text: planImpactText(impact) });
      warning.id = 'ba-impact';
      warning.classList.add('notice-impact');
      warning.dataset.subscriptions = impact.subscriptions === null ? 'unknown' : String(impact.subscriptions);
      sel.setAttribute('aria-describedby', warning.id);
      body.append(warning);
    }
    body.append(selField, statusEl, actionsRow);
    dialog.append(body);

    let submitting = false;
    const handle = { dismiss: () => { if (dialog.open) dialog.close(); dialog.remove(); } };
    const isOpen = () => this.dialog === handle;
    const close = () => { if (!isOpen()) return; this.dialog = null; handle.dismiss(); };

    cancelBtn.addEventListener('click', close);
    dialog.addEventListener('keydown', e => { if (e.key === 'Escape' && !submitting) close(); });
    dialog.addEventListener('cancel', e => { e.preventDefault(); if (!submitting) close(); });

    saveBtn.addEventListener('click', async () => {
      if (submitting) return;
      submitting = true;
      saveBtn.disabled = true; saveBtn.setAttribute('aria-busy', 'true');
      setLabel(saveBtn, 'Сохраняем…');
      showStatus(null);
      try {
        const rawVal = sel.value;
        const newBuilderId = rawVal === '' ? null : parseInt(rawVal, 10);
        const input: SetBuilderInput = { request_key: newRequestKey(), builder_id: newBuilderId };
        if (target === 'plan') {
          await this.api.setPlanBuilder(targetId, input);
        } else {
          await this.api.setSubscriptionBuilder(targetId, input);
        }
        if (!isOpen()) return;
        close();
        onDone(newBuilderId);
      } catch (error) {
        if (!isOpen()) return;
        submitting = false;
        saveBtn.disabled = false; saveBtn.removeAttribute('aria-busy');
        setLabel(saveBtn, 'Сохранить');
        if (await this.endIfSignedOut(error, isOpen)) return;
        showStatus({ tone: 'error', text: `Не удалось сохранить. ${errorText(error)}` });
      }
    });

    this.dialog = handle;
    this.root.append(dialog);
    dialog.showModal();
    sel.focus();
  }

  // ===========================================================================
  // Sources section
  // ===========================================================================

  private buildSourcesSection(header: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadSources());
    const addBtn = button('Добавить источник', 'btn btn-primary btn-sm', 'plus');
    addBtn.addEventListener('click', () => this.openSourceEditor(null));
    actions.prepend(addBtn);
    header.classList.add('has-actions');
    header.append(actions);

    const body = el('div', 'sources-section');
    this.sourcesView = { body, refresh };
    if (this.sourcesCache) this.paintSources(body, this.sourcesCache, updated);
    else this.paintSourcesSkeleton(body, updated);
    return body;
  }

  private paintSourcesSkeleton(body: HTMLElement, updated: HTMLElement) {
    setLoading(body, 'Загружаем источники');
    body.replaceChildren(skeletonPanel(5));
    updated.textContent = '';
  }

  private paintSourcesError(body: HTMLElement, updated: HTMLElement, error: unknown) {
    setLoading(body, null);
    body.replaceChildren(messagePanel('alert', 'Не удалось загрузить источники', errorText(error), 'alert',
      retryButton(() => void this.loadSources())));
    updated.textContent = '';
  }

  private paintSources(body: HTMLElement, sources: readonly AdminSource[], updated: HTMLElement) {
    setLoading(body, null);
    updated.textContent = `Обновлено в ${clockSeconds(new Date())}`;
    if (sources.length === 0) {
      const add = button('Добавить источник', 'btn btn-primary btn-sm', 'plus');
      add.addEventListener('click', () => this.openSourceEditor(null));
      body.replaceChildren(messagePanel('sources', 'Источников пока нет',
        'Добавьте подписку провайдера: сервер скачает её, разберёт серверы и страны, и построители смогут их использовать.', 'status', add));
      return;
    }
    const panel = el('section', 'panel');
    panel.setAttribute('aria-labelledby', 'sources-list-title');
    const total = sources.reduce((n, s) => n + s.catalogue.entries, 0);
    panel.append(panelHead('sources-list-title', 'Источники', `${formatCount(sources.length)} · ${serversLabel(total)} всего`));
    const list = el('ul', 'source-list');
    for (const src of sources) list.append(this.sourceRow(src));
    panel.append(list,
      el('p', 'panel-note', 'Каталог каждого источника хранится отдельно. Серверы разных источников объединяются только в построителе.'));
    body.replaceChildren(panel);
  }

  private sourceRow(src: AdminSource): HTMLElement {
    const state = syncState(src);
    const item = el('li', 'source-item');
    item.dataset.sourceId = String(src.id);
    item.setAttribute('aria-label', src.name);
    const info = el('div', 'source-info');
    const nameRow = el('div', 'source-name-row');
    const name = el('a', 'source-name', src.name);
    name.href = `#/sources/${src.id}`;
    nameRow.append(name, enabledBadge(src.enabled), syncBadge(state));
    const stats = el('p', 'source-stats', state === 'never' && src.catalogue.entries === 0
      ? 'Каталог ещё не загружен' : catalogueSummary(src.catalogue));
    const meta = el('p', 'source-meta', `${formatLabel(src.type)} · ${lastSyncText(src)}`);
    info.append(nameRow, stats, meta);
    if ((state === 'error' || state === 'partial') && src.last_sync_error) {
      info.append(el('p', state === 'error' ? 'source-error' : 'source-warn', syncErrorText(src.last_sync_error)));
    }
    if (src.description) info.append(el('p', 'source-desc', src.description));
    const last = this.syncNotices.get(src.id);
    if (last) info.append(notice(last));

    const acts = el('div', 'source-actions');
    const open = el('a', 'btn btn-secondary btn-xs');
    open.href = `#/sources/${src.id}`;
    open.setAttribute('aria-label', `Каталог «${src.name}»`);
    open.append(el('span', 'btn-label', 'Каталог'));
    const sync = button('Синхронизировать', 'btn btn-secondary btn-xs', 'refresh');
    sync.setAttribute('aria-label', `Синхронизировать «${src.name}»`);
    sync.addEventListener('click', () => void this.syncFromList(src, sync));
    const editBtn = button('Изменить', 'btn btn-secondary btn-xs', 'edit');
    editBtn.setAttribute('aria-label', `Изменить «${src.name}»`);
    editBtn.addEventListener('click', () => this.openSourceEditor(src));
    const toggleBtn = button(src.enabled ? 'Отключить' : 'Включить', 'btn btn-secondary btn-xs');
    toggleBtn.setAttribute('aria-label', `${src.enabled ? 'Отключить' : 'Включить'} «${src.name}»`);
    toggleBtn.addEventListener('click', () => void this.toggleSource(src, toggleBtn));
    acts.append(open, sync, editBtn, toggleBtn);
    item.append(info, acts);
    return item;
  }

  /** Keeps the list cache current and repaints the list if it is open. */
  private replaceSource(updated: AdminSource) {
    if (this.sourcesCache) {
      this.sourcesCache = this.sourcesCache.some(s => s.id === updated.id)
        ? this.sourcesCache.map(s => s.id === updated.id ? updated : s)
        : [...this.sourcesCache, updated];
    }
    const view = this.sourcesView;
    if (view && this.sourcesCache) {
      const updatedEl = view.refresh.closest('.page-actions')?.querySelector<HTMLElement>('.updated') ?? el('p', 'updated');
      this.paintSources(view.body, this.sourcesCache, updatedEl);
    }
  }

  /** Records the outcome of a sync for the row and the detail page. */
  private applySyncResult(result: SourceSyncResult) {
    const message = syncResultMessage(result);
    const format = result.status !== 'error' && result.format ? ` Формат ответа: ${detectedFormatLabel(result.format)}.` : '';
    this.syncNotices.set(result.source.id, { tone: message.tone, text: message.text + format });
    this.replaceSource(result.source);
  }

  private async syncFromList(src: AdminSource, btn: HTMLButtonElement) {
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    setLabel(btn, 'Синхронизируем…');
    try {
      const result = await this.api.syncSource(src.id);
      this.applySyncResult(result);
      this.toast(result.status === 'error' ? `«${src.name}»: синхронизация не удалась.` : `«${src.name}» синхронизирован.`);
    } catch (error) {
      if (await this.endIfSignedOut(error, () => this.sourcesView !== null)) return;
      this.toast(`Не удалось синхронизировать «${src.name}». ${errorText(error)}`);
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
      setLabel(btn, 'Синхронизировать');
    }
  }

  private async toggleSource(src: AdminSource, btn: HTMLButtonElement) {
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    try {
      const updated = src.enabled ? await this.api.disableSource(src.id) : await this.api.enableSource(src.id);
      this.replaceSource(updated);
      if (this.sourceDetailView?.id === updated.id) {
        this.sourceDetailView.source = updated;
        this.paintSourceDetail(this.sourceDetailView);
      }
    } catch (error) {
      if (await this.endIfSignedOut(error, () => this.sourcesView !== null || this.sourceDetailView !== null)) return;
      this.toast(`Не удалось изменить статус источника. ${errorText(error)}`);
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
    }
  }

  private async loadSources() {
    const view = this.sourcesView;
    if (!view) return;
    const request = ++this.sourcesRequest;
    const current = () => this.sourcesView === view && request === this.sourcesRequest;
    setRefreshBusy(view.refresh, true);
    const dummy = el('p', 'updated');
    if (!this.sourcesCache) this.paintSourcesSkeleton(view.body, dummy);
    try {
      const sources = await this.api.listSources();
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      this.sourcesCache = sources;
      const updated = view.refresh.closest('.page-actions')?.querySelector<HTMLElement>('.updated') ?? dummy;
      this.paintSources(view.body, sources, updated);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      const updated = view.refresh.closest('.page-actions')?.querySelector<HTMLElement>('.updated') ?? dummy;
      if (this.sourcesCache) this.toast(`Не удалось обновить источники. ${errorText(error)}`);
      else this.paintSourcesError(view.body, updated, error);
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  // Source editor dialog -------------------------------------------------------

  private openSourceEditor(src: AdminSource | null) {
    if (this.dialog || this.view !== 'app') return;

    const isNew = src === null;
    const dialog = el('dialog', 'modal modal-wide');
    dialog.setAttribute('aria-labelledby', 'src-editor-title');

    const title = el('h2', 'modal-title', isNew ? 'Новый источник' : `Источник: ${src!.name}`);
    title.id = 'src-editor-title';

    const mkInput = (id: string, labelText: string, value = '', type = 'text', hint?: string) => {
      const inp = el('input', 'input');
      Object.assign(inp, { id, type, value, spellcheck: false, autocomplete: 'off' });
      const f = field(labelText, inp);
      if (hint) f.root.append(el('p', 'field-hint', hint));
      return { inp, ...f };
    };

    const nameF = mkInput('src-name', 'Название', src?.name ?? '', 'text', 'Например: «Liberty — 20 стран». Видно только в панели.');
    const descF = mkInput('src-desc', 'Описание', src?.description ?? '');

    // ProviderSource.type is the expected response format, not a provider kind.
    const typeSel = el('select', 'input');
    typeSel.id = 'src-type';
    for (const f of SOURCE_FORMATS) typeSel.append(option(f.value, f.label));
    typeSel.value = normalizeFormat(src?.type ?? 'auto');
    const typeF = field('Формат подписки', typeSel);
    const typeHint = el('p', 'field-hint');
    const syncTypeHint = () => {
      typeHint.textContent = `${SOURCE_FORMATS.find(f => f.value === typeSel.value)?.hint ?? ''} Если ответ придёт в другом формате, синхронизация сообщит об этом.`;
    };
    typeSel.addEventListener('change', syncTypeHint);
    syncTypeHint();
    typeF.root.append(typeHint);

    const urlF = mkInput('src-url', 'URL подписки', '', 'url',
      'Полный адрес подписки провайдера (http:// или https://). Хранится только на сервере и не показывается в панели.');
    const hwidF = mkInput('src-hwid', 'HWID', '', 'text', 'Идентификатор устройства, если провайдер его требует (заголовок X-HWID).');
    const uaF = mkInput('src-ua', 'User-Agent', '', 'text', 'Пусто — стандартный. Некоторые провайдеры отдают формат в зависимости от клиента (например, Happ).');
    const headersF = mkInput('src-headers', 'Дополнительные заголовки', '', 'text',
      'JSON-объект, например {"Authorization": "Bearer …"}. Значения только записываются и не показываются.');

    const enabledChk = el('input', 'checkbox');
    enabledChk.type = 'checkbox';
    enabledChk.id = 'src-enabled';
    enabledChk.checked = src?.enabled ?? true;
    const enabledLabel = el('label', 'checkbox-label', 'Включён: построители могут использовать источник');
    enabledLabel.htmlFor = 'src-enabled';
    const enabledRow = el('div', 'checkbox-row');
    enabledRow.append(enabledChk, enabledLabel);

    const statusEl = el('div', 'modal-status');
    statusEl.setAttribute('role', 'alert');
    const showStatus = (n: Notice | null) => statusEl.replaceChildren(...(n ? [notice(n)] : []));

    const cancel = button('Отмена', 'btn btn-secondary');
    const saveLabel = isNew ? 'Создать и синхронизировать' : 'Сохранить';
    const save = button(saveLabel, 'btn btn-primary');
    const acts = el('div', 'modal-actions');
    acts.append(cancel, save);

    const body = el('div', 'modal-body');
    body.append(title, nameF.root, descF.root, typeF.root);
    if (isNew) {
      body.append(urlF.root, hwidF.root, uaF.root, headersF.root,
        el('p', 'field-hint', 'После создания сервер сразу скачает подписку и построит каталог серверов.'));
    } else {
      body.append(el('p', 'field-hint', 'URL и учётные данные изменить здесь нельзя: они только записываются при создании.'));
    }
    body.append(enabledRow, statusEl, acts);
    dialog.append(body);

    const fields: Readonly<Record<string, { setError: (t: string) => void; focus: () => void }>> = {
      name: { setError: nameF.setError, focus: () => nameF.inp.focus() },
      type: { setError: typeF.setError, focus: () => typeSel.focus() },
      subscription_url: { setError: urlF.setError, focus: () => urlF.inp.focus() },
      headers: { setError: headersF.setError, focus: () => headersF.inp.focus() },
      hwid: { setError: hwidF.setError, focus: () => hwidF.inp.focus() },
      user_agent: { setError: uaF.setError, focus: () => uaF.inp.focus() },
    };
    const FIELD_ERRORS: Readonly<Record<string, string>> = {
      name: 'Введите название (до 255 символов).',
      type: 'Выберите формат из списка.',
      subscription_url: 'Нужен полный адрес http:// или https:// без #фрагмента.',
      headers: 'Заголовки — JSON-объект {"Имя": "значение"}: имена без пробелов, значения без переносов строк.',
      hwid: 'HWID не должен содержать переносов строк и управляющих символов.',
      user_agent: 'User-Agent не должен содержать переносов строк и управляющих символов.',
    };
    const clearErrors = () => { for (const f of Object.values(fields)) f.setError(''); };

    let submitting = false;
    const handle = {
      dismiss: () => { if (dialog.open) dialog.close(); dialog.remove(); },
    };
    const isOpen = () => this.dialog === handle;
    const close = () => {
      if (!isOpen()) return;
      this.dialog = null;
      handle.dismiss();
    };

    cancel.addEventListener('click', () => close());
    dialog.addEventListener('keydown', e => { if (e.key === 'Escape' && !submitting) close(); });
    dialog.addEventListener('cancel', e => { e.preventDefault(); if (!submitting) close(); });

    save.addEventListener('click', async () => {
      if (submitting) return;
      clearErrors();
      showStatus(null);
      const name = nameF.inp.value.trim();
      if (!name) { nameF.setError(FIELD_ERRORS.name); nameF.inp.focus(); return; }
      const url = urlF.inp.value.trim();
      if (isNew && !/^https?:\/\/[^\s/#]+/i.test(url)) { urlF.setError(FIELD_ERRORS.subscription_url); urlF.inp.focus(); return; }
      const headers = headersF.inp.value.trim();
      if (isNew && headers) {
        let ok = false;
        try { const parsed: unknown = JSON.parse(headers); ok = typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed); } catch { ok = false; }
        if (!ok) { headersF.setError(FIELD_ERRORS.headers); headersF.inp.focus(); return; }
      }
      submitting = true;
      save.disabled = true; save.setAttribute('aria-busy', 'true');
      setLabel(save, isNew ? 'Создаём и синхронизируем…' : 'Сохраняем…');
      if (isNew) showStatus({ tone: 'info', text: 'Сервер скачивает подписку и строит каталог…' });
      try {
        if (isNew) {
          const input: CreateSourceInput = {
            name, description: descF.inp.value.trim(), type: typeSel.value,
            subscription_url: url, hwid: hwidF.inp.value.trim(),
            user_agent: uaF.inp.value.trim(), headers,
            enabled: enabledChk.checked,
          };
          const created = await this.api.createSource(input);
          if (!isOpen()) return;
          if (created.sync) this.applySyncResult(created.sync);
          else this.replaceSource(created.source);
          close();
          this.toast('Источник создан.');
          location.hash = `#/sources/${created.source.id}`;
        } else {
          const input: UpdateSourceInput = { name, description: descF.inp.value.trim(), type: typeSel.value };
          const updated = await this.api.updateSource(src!.id, input);
          if (!isOpen()) return;
          this.replaceSource(updated);
          if (this.sourceDetailView?.id === updated.id) {
            this.sourceDetailView.source = updated;
            this.paintSourceDetail(this.sourceDetailView);
          }
          close();
          this.toast('Источник обновлён.');
        }
      } catch (error) {
        if (!isOpen()) return;
        submitting = false;
        save.disabled = false; save.removeAttribute('aria-busy');
        setLabel(save, saveLabel);
        if (await this.endIfSignedOut(error, isOpen)) return;
        const target = error instanceof ApiError && error.code === 'invalid_source' ? fields[error.field] : undefined;
        if (target) {
          target.setError(FIELD_ERRORS[(error as ApiError).field]);
          target.focus();
          showStatus(null);
        } else {
          showStatus({ tone: 'error', text: `Не удалось сохранить. ${errorText(error)}` });
        }
      }
    });

    this.dialog = handle;
    this.root.append(dialog);
    dialog.showModal();
    nameF.inp.focus();
  }

  // Source details (#/sources/{id}) --------------------------------------------

  private buildSourceDetail(id: number, header: HTMLElement, title: HTMLElement, desc: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadSourceDetail());
    const sync = button('Синхронизировать', 'btn btn-primary btn-sm', 'refresh');
    sync.addEventListener('click', () => void this.syncFromDetail());
    actions.prepend(sync);
    header.classList.add('has-actions');
    header.append(actions);
    title.textContent = `Источник #${id}`;
    desc.textContent = 'Каталог серверов по последней синхронизации.';

    const wrap = el('div', 'source-detail');
    const status = el('div', 'source-sync-status');
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    const body = el('div', 'source-detail-body');
    wrap.append(status, body);
    this.sourceDetailView = {
      id, body, refresh, updated, title, desc, source: null, entries: null,
      filter: { ...EMPTY_FILTER }, syncing: false, status, sync,
    };
    setLoading(body, 'Загружаем источник');
    body.replaceChildren(skeletonPanel(4), skeletonPanel(8));
    return wrap;
  }

  private async loadSourceDetail() {
    const view = this.sourceDetailView;
    if (!view) return;
    const request = ++this.sourceDetailRequest;
    const current = () => this.sourceDetailView === view && request === this.sourceDetailRequest;
    setRefreshBusy(view.refresh, true);
    try {
      const [source, entries] = await Promise.all([this.api.getSource(view.id), this.api.listAllSourceEntries(view.id)]);
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      view.source = source;
      view.entries = entries;
      this.paintSourceDetail(view);
      view.updated.textContent = `Обновлено в ${clockSeconds(new Date())}`;
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      setLoading(view.body, null);
      if (error instanceof ApiError && error.status === 404) {
        const back = el('a', 'btn btn-secondary btn-sm');
        back.href = '#/sources';
        back.append(el('span', 'btn-label', 'К списку источников'));
        view.sync.disabled = true;
        view.body.replaceChildren(messagePanel('sources', `Источника #${view.id} нет`, 'Возможно, он был удалён.', 'status', back));
      } else if (view.source) {
        this.toast(`Не удалось обновить источник. ${errorText(error)}`);
      } else {
        view.body.replaceChildren(messagePanel('alert', 'Не удалось загрузить источник', errorText(error), 'alert',
          retryButton(() => void this.loadSourceDetail())));
      }
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  private async syncFromDetail() {
    const view = this.sourceDetailView;
    if (!view || view.syncing) return;
    view.syncing = true;
    view.sync.disabled = true;
    view.sync.setAttribute('aria-busy', 'true');
    setLabel(view.sync, 'Синхронизируем…');
    view.status.replaceChildren(notice({ tone: 'info', text: 'Синхронизация: сервер скачивает и разбирает подписку…' }));
    const current = () => this.sourceDetailView === view;
    try {
      const result = await this.api.syncSource(view.id);
      if (!current()) return;
      this.applySyncResult(result);
      view.source = result.source;
      view.entries = await this.api.listAllSourceEntries(view.id);
      if (!current()) return;
      // Cleared before the repaint: it shows the sync outcome only when idle.
      view.syncing = false;
      this.paintSourceDetail(view);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      view.status.replaceChildren(notice({ tone: 'error', text: `Не удалось запустить синхронизацию. ${errorText(error)}` }));
    } finally {
      if (current()) {
        view.syncing = false;
        view.sync.disabled = false;
        view.sync.removeAttribute('aria-busy');
        setLabel(view.sync, 'Синхронизировать');
      }
    }
  }

  private paintSourceDetail(view: SourceDetailView) {
    const src = view.source;
    if (!src) return;
    const entries = view.entries ?? [];
    setLoading(view.body, null);
    view.title.textContent = src.name;
    view.desc.textContent = `${formatLabel(src.type)} · ${src.enabled ? 'включён' : 'отключён'}${src.description ? ` · ${src.description}` : ''}`;
    document.title = `${src.name} · Источники · RS8 Admin`;
    const last = this.syncNotices.get(src.id);
    view.status.replaceChildren(...(last && !view.syncing ? [notice(last)] : []));

    const state = syncState(src);
    const c = src.catalogue;

    // Summary --------------------------------------------------------------
    const summary = el('section', 'panel');
    summary.setAttribute('aria-labelledby', 'src-summary-title');
    const facts = el('dl', 'facts');
    const syncValue = el('span', 'source-sync-value');
    syncValue.append(syncBadge(state));
    facts.append(
      fact('Серверы', serversLabel(c.entries), c.absent > 0 ? `Исчезли из подписки: ${formatCount(c.absent)}` : undefined),
      fact('Страны', countriesLabel(c.countries), c.no_country > 0 ? `Без страны: ${formatCount(c.no_country)}` : undefined),
      fact('Протоколы', protocolsText(c.protocols)),
      fact('Формат', formatLabel(src.type)),
      fact('Синхронизация', syncValue, (state === 'error' || state === 'partial') ? syncErrorText(src.last_sync_error) : undefined,
        state === 'error' ? 'is-danger' : ''),
      fact('Когда', src.last_sync_at ? formatDateTime(src.last_sync_at) : 'Ещё не было',
        state === 'error' ? 'Показан каталог последней успешной синхронизации' : undefined),
    );
    const manage = el('div', 'source-detail-actions');
    const editBtn = button('Изменить', 'btn btn-secondary btn-sm', 'edit');
    editBtn.addEventListener('click', () => this.openSourceEditor(src));
    const toggleBtn = button(src.enabled ? 'Отключить' : 'Включить', 'btn btn-secondary btn-sm');
    toggleBtn.addEventListener('click', () => void this.toggleSource(src, toggleBtn));
    manage.append(editBtn, toggleBtn);
    summary.append(panelHead('src-summary-title', 'Сводка', catalogueSummary(c)), facts, manage);

    // Countries ------------------------------------------------------------
    const countries = el('section', 'panel');
    countries.setAttribute('aria-labelledby', 'src-countries-title');
    countries.append(panelHead('src-countries-title', 'Страны', countriesLabel(c.countries)));
    const chips = el('ul', 'country-chips');
    const chip = (code: string, label: string, count: number) => {
      const li = el('li');
      const b = button(`${label} · ${formatCount(count)}`, 'chip country-chip');
      b.dataset.country = code;
      b.setAttribute('aria-pressed', String(view.filter.country === code));
      b.addEventListener('click', () => {
        view.filter = { ...view.filter, country: view.filter.country === code ? '' : code };
        this.paintSourceDetail(view);
        this.sourceDetailView?.body.querySelector<HTMLButtonElement>(`.country-chip[data-country="${code}"]`)?.focus();
      });
      li.append(b);
      return li;
    };
    for (const { code, count } of c.by_country) chips.append(chip(code, `${flagEmoji(code)} ${code}`.trim(), count));
    if (c.no_country > 0) chips.append(chip('-', 'Без страны', c.no_country));
    if (c.by_country.length === 0 && c.no_country === 0) countries.append(el('p', 'panel-note', 'Серверов в каталоге нет.'));
    else countries.append(chips);

    // Servers --------------------------------------------------------------
    const servers = el('section', 'panel');
    servers.setAttribute('aria-labelledby', 'src-servers-title');
    const absent = entries.filter(e => !e.present).length;
    servers.append(panelHead('src-servers-title', 'Серверы', serversLabel(entries.length - absent)));

    const toolbar = el('div', 'toolbar source-filters');
    const countrySel = el('select', 'input');
    countrySel.id = 'src-filter-country';
    countrySel.append(option('', 'Все страны'));
    for (const { code, count } of c.by_country) countrySel.append(option(code, `${flagEmoji(code)} ${code} (${formatCount(count)})`));
    if (c.no_country > 0) countrySel.append(option('-', `Без страны (${formatCount(c.no_country)})`));
    countrySel.value = view.filter.country;
    const protocolSel = el('select', 'input');
    protocolSel.id = 'src-filter-protocol';
    protocolSel.append(option('', 'Все протоколы'));
    for (const p of protocolsOf(entries)) protocolSel.append(option(p, p));
    protocolSel.value = view.filter.protocol;
    const search = el('input', 'input');
    Object.assign(search, { id: 'src-filter-q', type: 'search', value: view.filter.query, placeholder: 'Имя сервера', spellcheck: false });
    const absentChk = el('input', 'checkbox');
    absentChk.type = 'checkbox';
    absentChk.id = 'src-filter-absent';
    absentChk.checked = view.filter.showAbsent;
    const absentLabel = el('label', 'checkbox-label', `Показывать исчезнувшие (${formatCount(absent)})`);
    absentLabel.htmlFor = absentChk.id;
    const absentRow = el('div', 'checkbox-row');
    absentRow.append(absentChk, absentLabel);
    toolbar.append(field('Страна', countrySel).root, field('Протокол', protocolSel).root, field('Поиск по имени', search).root, absentRow);

    const shown = el('p', 'source-filter-count');
    shown.setAttribute('aria-live', 'polite');
    const table = el('table', 'table source-entries');
    table.append(el('caption', 'sr-only', `Серверы источника ${src.name}`),
      headRow([['#', 'num'], ['Сервер', ''], ['Страна', ''], ['Протокол', ''], ['Fingerprint', 'col-lg'], ['В подписке', '']]));
    const tbody = el('tbody');
    table.append(tbody);
    const tableWrap = el('div', 'table-wrap');
    tableWrap.append(table);

    const renderRows = () => {
      const list = filterEntries(entries, view.filter);
      shown.textContent = `Показано ${formatCount(list.length)} из ${formatCount(view.filter.showAbsent ? entries.length : entries.length - absent)}`;
      tbody.replaceChildren(...list.map(e => {
        const tr = el('tr', e.present ? '' : 'is-absent');
        const fp = el('code', 'fingerprint', shortFingerprint(e.fingerprint));
        fp.title = e.fingerprint;
        tr.append(
          cell('#', e.present ? String(e.upstream_position + 1) : '—', 'num'),
          cell('Сервер', e.original_name || '(без имени)'),
          cell('Страна', e.country_code ? `${flagEmoji(e.country_code)} ${e.country_code}` : '—'),
          cell('Протокол', e.protocol || '—'),
          cell('Fingerprint', fp, 'col-lg'),
          cell('В подписке', e.present ? 'Да' : `Исчез · ${formatDateTime(e.last_seen_at)}`),
        );
        return tr;
      }));
      if (list.length === 0) {
        const tr = el('tr');
        const td = el('td', 'empty-row', entries.length === 0 ? 'Каталог пуст. Нажмите «Синхронизировать».' : 'Нет серверов под выбранные фильтры.');
        td.colSpan = 6;
        tr.append(td);
        tbody.append(tr);
      }
    };
    const onFilter = () => {
      view.filter = {
        country: countrySel.value, protocol: protocolSel.value, query: search.value, showAbsent: absentChk.checked,
      };
      for (const b of view.body.querySelectorAll<HTMLButtonElement>('.country-chip')) {
        b.setAttribute('aria-pressed', String(b.dataset.country === view.filter.country));
      }
      renderRows();
    };
    countrySel.addEventListener('change', onFilter);
    protocolSel.addEventListener('change', onFilter);
    search.addEventListener('input', onFilter);
    absentChk.addEventListener('change', onFilter);
    renderRows();
    servers.append(toolbar, shown, tableWrap);

    view.body.replaceChildren(summary, countries, servers);
  }

  // ===========================================================================
  // Builders section
  // ===========================================================================

  private buildBuildersSection(header: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadBuilders());
    const addBtn = button('Новый построитель', 'btn btn-primary btn-sm', 'plus');
    addBtn.addEventListener('click', () => this.openBuilderEditor(null));
    actions.prepend(addBtn);
    header.classList.add('has-actions');
    header.append(actions);

    const body = el('div', 'builders-section');
    this.buildersView = { body, refresh };
    if (this.buildersCache) this.paintBuilders(body, this.buildersCache, updated);
    else this.paintBuildersSkeleton(body, updated);
    return body;
  }

  private paintBuildersSkeleton(body: HTMLElement, updated: HTMLElement) {
    setLoading(body, 'Загружаем построители');
    body.replaceChildren(skeletonPanel(5));
    updated.textContent = '';
  }

  private paintBuildersError(body: HTMLElement, updated: HTMLElement, error: unknown) {
    setLoading(body, null);
    body.replaceChildren(messagePanel('alert', 'Не удалось загрузить построители', errorText(error), 'alert',
      retryButton(() => void this.loadBuilders())));
    updated.textContent = '';
  }

  private paintBuilders(body: HTMLElement, builders: readonly AdminBuilder[], updated: HTMLElement) {
    setLoading(body, null);
    updated.textContent = `Обновлено в ${clockSeconds(new Date())}`;
    if (builders.length === 0) {
      body.replaceChildren(messagePanel('builders', 'Построителей пока нет',
        'Создайте построитель, чтобы задать правила фильтрации и порядок серверов для подписок.', 'status'));
      return;
    }
    const panel = el('section', 'panel');
    panel.setAttribute('aria-labelledby', 'builders-list-title');
    panel.append(panelHead('builders-list-title', 'Построители', formatCount(builders.length)));
    const list = el('ul', 'builder-list');
    for (const b of builders) list.append(this.builderRow(b));
    panel.append(list);
    body.replaceChildren(panel);
  }

  private builderRow(b: AdminBuilder): HTMLElement {
    const item = el('li', 'builder-item');
    const info = el('div', 'builder-info');
    const nameRow = el('div', 'builder-name-row');
    const name = el('span', 'builder-name', b.name);
    const badge = el('span', b.enabled ? 'badge badge-active' : 'badge badge-disabled',
      b.enabled ? 'Активен' : 'Отключён');
    nameRow.append(name, badge);
    const meta = el('p', 'builder-meta');
    const srcCount = b.sources?.length ?? 0;
    const itemCount = b.items?.length ?? 0;
    meta.textContent = [
      `v${b.version}`,
      `${srcCount} ${srcCount === 1 ? 'источник' : srcCount < 5 ? 'источника' : 'источников'}`,
      `${itemCount} ${itemCount === 1 ? 'правило' : itemCount < 5 ? 'правила' : 'правил'}`,
    ].join(' · ');
    info.append(nameRow, meta);
    if (b.description) info.append(el('p', 'builder-desc', b.description));

    const acts = el('div', 'builder-actions');
    const editBtn = button('Редактировать', 'btn btn-primary btn-xs', 'edit');
    editBtn.addEventListener('click', () => this.openBuilderEditor(b));
    const toggleBtn = button(b.enabled ? 'Отключить' : 'Включить', 'btn btn-secondary btn-xs');
    toggleBtn.addEventListener('click', () => void this.toggleBuilder(b, toggleBtn));
    acts.append(editBtn, toggleBtn);
    item.append(info, acts);
    return item;
  }

  private async toggleBuilder(b: AdminBuilder, btn: HTMLButtonElement) {
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    try {
      const updated = b.enabled ? await this.api.disableBuilder(b.id) : await this.api.enableBuilder(b.id);
      if (this.buildersCache) {
        this.buildersCache = this.buildersCache.map(x => x.id === updated.id ? updated : x);
        const view = this.buildersView;
        if (view) {
          const dummy = el('p', 'updated');
          this.paintBuilders(view.body, this.buildersCache, dummy);
        }
      }
    } catch (error) {
      if (await this.endIfSignedOut(error, () => this.buildersView !== null)) return;
      this.toast(`Не удалось изменить статус построителя. ${errorText(error)}`);
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
    }
  }

  private async loadBuilders() {
    const view = this.buildersView;
    if (!view) return;
    const request = ++this.buildersRequest;
    const current = () => this.buildersView === view && request === this.buildersRequest;
    setRefreshBusy(view.refresh, true);
    const dummy = el('p', 'updated');
    if (!this.buildersCache) this.paintBuildersSkeleton(view.body, dummy);
    try {
      const builders = await this.api.listBuilders();
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      this.buildersCache = builders;
      const updated = view.refresh.closest('.page-actions')?.querySelector<HTMLElement>('.updated') ?? dummy;
      this.paintBuilders(view.body, builders, updated);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      const updated = view.refresh.closest('.page-actions')?.querySelector<HTMLElement>('.updated') ?? dummy;
      if (this.buildersCache) this.toast(`Не удалось обновить построители. ${errorText(error)}`);
      else this.paintBuildersError(view.body, updated, error);
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  // ===========================================================================
  // Builder editor (full-screen dialog)
  // ===========================================================================

  private openBuilderEditor(b: AdminBuilder | null) {
    if (this.dialog || this.view !== 'app') return;
    const isNew = b === null;

    // ---- State ----
    let currentBuilder: AdminBuilder | null = b;
    let unsaved = false;
    let submitting = false;
    // Ordered list of source IDs (drag-reorder)
    let selectedSourceIds: number[] = b?.sources?.slice().sort((a, x) => a.position - x.position).map(s => s.source_id) ?? [];
    // Items (rules) — local copy for drag-reorder
    let localItems: BuilderItem[] = b?.items?.slice().sort((a, x) => a.position - x.position) ?? [];
    // Rules stored on the server: those removed locally are deleted on save.
    let serverItemIds = new Set(localItems.map(it => it.id).filter(id => id > 0));
    // Assigned below; the source picker repaints the rules (their warnings
    // depend on which sources are linked).
    let renderItems: () => void = () => {};
    // Preview always reflects the SAVED builder, exactly like /sub.
    const previewNote = el('p', 'field-hint preview-note');

    // ---- Dialog shell ----
    const dialog = el('dialog', 'modal modal-editor');
    dialog.setAttribute('aria-labelledby', 'be-title');

    const titleEl = el('h2', 'modal-title', isNew ? 'Новый построитель' : `Построитель: ${b!.name}`);
    titleEl.id = 'be-title';

    // ---- Unsaved indicator ----
    const unsavedBadge = el('span', 'badge badge-warn unsaved-badge');
    unsavedBadge.textContent = 'Несохранённые изменения';
    unsavedBadge.hidden = true;
    const markUnsaved = () => {
      unsaved = true;
      unsavedBadge.hidden = false;
      previewNote.textContent = 'Есть несохранённые изменения: предпросмотр показывает сохранённую версию. Сохраните, чтобы увидеть результат.';
    };

    // ---- Basic fields ----
    const mkInp = (id: string, labelText: string, value = '', hint?: string) => {
      const inp = el('input', 'input');
      Object.assign(inp, { id, type: 'text', value, spellcheck: false });
      const f = field(labelText, inp);
      if (hint) f.root.append(el('p', 'field-hint', hint));
      inp.addEventListener('input', markUnsaved);
      return { inp, ...f };
    };

    const nameF = mkInp('be-name', 'Название', b?.name ?? '');
    const descF = mkInp('be-desc', 'Описание', b?.description ?? '');
    const profileTitleF = mkInp('be-profile-title', 'Заголовок профиля', b?.profile_title ?? '',
      'Отображается в клиентском приложении как имя подписки.');
    const supportUrlF = mkInp('be-support-url', 'URL поддержки', b?.support_url ?? '');
    const announceF = mkInp('be-announce', 'Анонс', b?.announce ?? '',
      'Короткое сообщение, которое клиент видит при обновлении подписки.');

    const enabledChk = el('input', 'checkbox');
    enabledChk.type = 'checkbox';
    enabledChk.id = 'be-enabled';
    enabledChk.checked = b?.enabled ?? true;
    enabledChk.addEventListener('change', markUnsaved);
    const enabledLabel = el('label', 'checkbox-label', 'Активен');
    enabledLabel.htmlFor = 'be-enabled';
    const enabledRow = el('div', 'checkbox-row');
    enabledRow.append(enabledChk, enabledLabel);

    // ---- Sources panel ----
    const sourcesPanel = el('section', 'editor-panel');
    sourcesPanel.setAttribute('aria-labelledby', 'be-sources-title');
    const sourcesPanelHead = panelHead('be-sources-title', 'Источники');
    sourcesPanelHead.append(el('p', 'panel-note', 'Выберите источники и задайте их порядок перетаскиванием. Построитель использует их в указанном порядке.'));
    sourcesPanel.append(sourcesPanelHead);

    const sourcesListEl = el('ul', 'sources-picker');
    sourcesListEl.setAttribute('aria-label', 'Источники построителя');
    const sourcesTotal = el('p', 'sources-total');
    sourcesTotal.setAttribute('aria-live', 'polite');
    const sourceMeta = (src: AdminSource) => {
      const meta = el('span', 'source-pick-meta');
      const state = syncState(src);
      meta.append(el('span', 'source-pick-stats', state === 'never' && src.catalogue.entries === 0
        ? 'каталог не загружен' : catalogueSummary(src.catalogue)), syncBadge(state));
      if (!src.enabled) meta.append(enabledBadge(false));
      return meta;
    };
    const renderSourcesPicker = () => {
      sourcesListEl.replaceChildren();
      const allSources = this.sourcesCache ?? [];
      const selected = selectedSourceIds.map(id => allSources.find(s => s.id === id)).filter(Boolean) as AdminSource[];
      const unselected = allSources.filter(s => !selectedSourceIds.includes(s.id));

      for (const src of selected) {
        const li = el('li', 'source-pick-item source-pick-selected');
        li.draggable = true;
        li.dataset.srcId = String(src.id);
        const dragHandle = el('span', 'drag-handle');
        dragHandle.append(icon('drag'));
        dragHandle.setAttribute('aria-hidden', 'true');
        const lbl = el('span', 'source-pick-name', src.name);
        const removeBtn = button('Убрать', 'btn btn-secondary btn-xs');
        removeBtn.setAttribute('aria-label', `Убрать «${src.name}»`);
        removeBtn.addEventListener('click', () => {
          selectedSourceIds = selectedSourceIds.filter(id => id !== src.id);
          markUnsaved();
          renderSourcesPicker();
        });
        li.append(dragHandle, lbl, sourceMeta(src), removeBtn);
        sourcesListEl.append(li);
      }
      for (const src of unselected) {
        const li = el('li', 'source-pick-item');
        li.dataset.srcId = String(src.id);
        const lbl = el('span', 'source-pick-name', src.name);
        const addBtn = button('Добавить', 'btn btn-primary btn-xs');
        addBtn.setAttribute('aria-label', `Добавить «${src.name}»`);
        addBtn.addEventListener('click', () => {
          selectedSourceIds = [...selectedSourceIds, src.id];
          markUnsaved();
          renderSourcesPicker();
        });
        li.append(lbl, sourceMeta(src), addBtn);
        sourcesListEl.append(li);
      }
      if (allSources.length === 0) {
        sourcesListEl.append(el('li', 'source-pick-empty', this.sourcesCache ? 'Источников нет. Добавьте их в разделе «Источники».' : 'Загружаем источники…'));
      }
      // Sources are merged only here: the total is the sum of the selected
      // catalogues, countries are counted once across them.
      const total = builderSourcesSummary(selected);
      const skipped = total.disabled > 0 ? ` Отключённые (${formatCount(total.disabled)}) не учитываются: сборка их пропускает.` : '';
      sourcesTotal.textContent = selected.length === 0 ? 'Источники не выбраны.'
        : `Итого: ${serversLabel(total.entries)} · ${countriesLabel(total.countries)} (уникальных) из ${formatCount(selected.length)} ${pluralRu(selected.length, 'источника', 'источников', 'источников')}.${skipped}`;
      setupDragReorder(sourcesListEl, 'source-pick-selected', (newOrder) => {
        selectedSourceIds = newOrder.map(Number);
        markUnsaved();
        renderSourcesPicker();
      });
      renderItems();
    };
    renderSourcesPicker();
    if (!this.sourcesCache) {
      void this.api.listSources().then(sources => {
        this.sourcesCache = sources;
        if (dialog.isConnected) renderSourcesPicker();
      }).catch(() => {
        if (dialog.isConnected) sourcesListEl.replaceChildren(el('li', 'source-pick-empty', 'Не удалось загрузить источники. Закройте редактор и повторите.'));
      });
    }
    sourcesPanel.append(sourcesListEl, sourcesTotal);

    // ---- Items (rules) panel ----
    const itemsPanel = el('section', 'editor-panel');
    itemsPanel.setAttribute('aria-labelledby', 'be-items-title');
    const itemsPanelHead = panelHead('be-items-title', 'Правила');
    itemsPanelHead.append(el('p', 'panel-note',
      'Правила определяют, какие серверы и страны включаются в подписку и в каком порядке. Без правил в подписку попадают все серверы подключённых источников.'));
    itemsPanel.append(itemsPanelHead);

    const itemsListEl = el('ul', 'items-list');
    itemsListEl.setAttribute('aria-label', 'Правила построителя');
    const addItemBtn = button('Добавить правило', 'btn btn-secondary btn-sm', 'plus');
    const ruleContext: RuleContext = {
      sources: () => this.sourcesCache ?? [],
      selectedIds: () => selectedSourceIds,
    };

    renderItems = () => {
      itemsListEl.replaceChildren();
      if (localItems.length === 0) {
        itemsListEl.append(el('li', 'items-empty', 'Правил пока нет. Добавьте страну или конкретный сервер.'));
      }
      for (let i = 0; i < localItems.length; i++) {
        const item = localItems[i];
        itemsListEl.append(this.buildItemRow(item, i, localItems, (updated) => {
          localItems = updated;
          markUnsaved();
          renderItems();
        }, ruleContext));
      }
      setupDragReorder(itemsListEl, 'item-row', (newOrder) => {
        const reordered = newOrder.map(idx => localItems[Number(idx)]);
        localItems = reordered.map((it, pos) => ({ ...it, position: pos }));
        markUnsaved();
        renderItems();
      });
    };
    renderItems();

    addItemBtn.addEventListener('click', () => {
      this.openItemEditor(null, ruleContext, (newItem) => {
        localItems = [...localItems, { ...newItem, position: localItems.length }];
        markUnsaved();
        renderItems();
      });
    });
    itemsPanel.append(itemsListEl, addItemBtn);

    // ---- Preview panel ----
    const previewPanel = el('section', 'editor-panel');
    previewPanel.setAttribute('aria-labelledby', 'be-preview-title');
    previewPanel.append(panelHead('be-preview-title', 'Предпросмотр'));
    const previewBody = el('div', 'preview-body');
    const previewBtn = button('Запустить предпросмотр', 'btn btn-secondary btn-sm', 'eye');
    previewBtn.disabled = isNew;
    previewBtn.title = isNew ? 'Сначала сохраните построитель' : '';
    previewBtn.addEventListener('click', () => void this.runPreview(currentBuilder, previewBody, previewBtn));
    previewPanel.append(previewNote, previewBody, previewBtn);

    // ---- Status / actions ----
    const statusEl = el('div', 'modal-status');
    statusEl.setAttribute('role', 'alert');
    const showStatus = (n: Notice | null) => statusEl.replaceChildren(...(n ? [notice(n)] : []));

    const cancelBtn = button('Закрыть', 'btn btn-secondary');
    const saveBtn = button(isNew ? 'Создать' : 'Сохранить', 'btn btn-primary');

    const actionsRow = el('div', 'modal-actions');
    actionsRow.append(cancelBtn, saveBtn);

    // ---- Assemble body ----
    const bodyEl = el('div', 'modal-body editor-body');
    bodyEl.append(
      titleEl, unsavedBadge,
      el('div', 'editor-section-title', 'Основные настройки'),
      nameF.root, descF.root, profileTitleF.root, supportUrlF.root, announceF.root, enabledRow,
      sourcesPanel,
      itemsPanel,
      previewPanel,
      statusEl, actionsRow,
    );
    dialog.append(bodyEl);

    // ---- Dialog lifecycle ----
    let handle: { dismiss: () => void };
    const isOpen = () => this.dialog === handle;
    const close = (force = false) => {
      if (!isOpen()) return;
      if (unsaved && !force) {
        if (!window.confirm('Есть несохранённые изменения. Закрыть без сохранения?')) return;
      }
      this.dialog = null;
      handle.dismiss();
    };
    handle = {
      dismiss: () => { if (dialog.open) dialog.close(); dialog.remove(); },
    };

    cancelBtn.addEventListener('click', () => close());
    dialog.addEventListener('keydown', e => { if (e.key === 'Escape' && !submitting) close(); });
    dialog.addEventListener('cancel', e => { e.preventDefault(); if (!submitting) close(); });

    // ---- Save ----
    saveBtn.addEventListener('click', async () => {
      if (submitting) return;
      nameF.setError('');
      const name = nameF.inp.value.trim();
      if (!name) { nameF.setError('Введите название.'); nameF.inp.focus(); return; }

      submitting = true;
      saveBtn.disabled = true; saveBtn.setAttribute('aria-busy', 'true');
      setLabel(saveBtn, 'Сохраняем…');
      showStatus(null);

      try {
        let saved: AdminBuilder;
        // A new builder is created once; later saves in the same editor update it.
        const creating = currentBuilder === null;
        if (creating) {
          const input: CreateBuilderInput = {
            request_key: newRequestKey(),
            name, description: descF.inp.value.trim(),
            enabled: enabledChk.checked,
            profile_title: profileTitleF.inp.value.trim(),
            support_url: supportUrlF.inp.value.trim(),
            announce: announceF.inp.value.trim(),
          };
          const res = await this.api.createBuilder(input);
          saved = res.builder;
        } else {
          const input: UpdateBuilderInput = {
            request_key: newRequestKey(),
            version: currentBuilder!.version,
            name, description: descF.inp.value.trim(),
            enabled: enabledChk.checked,
            profile_title: profileTitleF.inp.value.trim(),
            support_url: supportUrlF.inp.value.trim(),
            announce: announceF.inp.value.trim(),
          };
          const res = await this.api.updateBuilder(currentBuilder!.id, input);
          saved = res.builder;
        }
        if (!isOpen()) return;

        // Save sources order
        if (selectedSourceIds.length > 0 || !creating) {
          await this.api.setBuilderSources(saved.id, selectedSourceIds);
        }

        // Rules removed in the editor are deleted, so Preview and /sub match
        // what the editor shows.
        const keptIds = new Set(localItems.map(it => it.id));
        for (const id of serverItemIds) {
          if (!keptIds.has(id)) await this.api.deleteBuilderItem(saved.id, id);
        }
        serverItemIds = new Set([...serverItemIds].filter(id => keptIds.has(id)));

        // Save items (upsert all, then reorder)
        const upserted: BuilderItem[] = [];
        if (localItems.length > 0) {
          for (const item of localItems) {
            const inp: UpsertBuilderItemInput = {
              id: item.id > 0 ? item.id : undefined,
              kind: item.kind,
              source_id: item.source_id,
              country_code: item.kind === 'country' ? item.country_code : undefined,
              fingerprint: item.kind === 'node' ? item.fingerprint : undefined,
              original_name: item.kind === 'node' ? item.original_name : undefined,
              custom_name: item.custom_name,
              description: item.description,
              position: item.position,
              enabled: item.enabled,
            };
            const upsertedItem = await this.api.upsertBuilderItem(saved.id, inp);
            upserted.push(upsertedItem);
          }
          if (upserted.length > 1) {
            await this.api.reorderBuilderItems(saved.id, upserted.map(it => it.id));
          }
        }
        // The server ids replace the local placeholders: a second save updates
        // these rules instead of creating copies.
        localItems = upserted.map((it, pos) => ({ ...it, position: pos }));
        serverItemIds = new Set(localItems.map(it => it.id));

        if (!isOpen()) return;
        currentBuilder = saved;
        previewNote.textContent = '';
        renderItems();
        unsaved = false;
        unsavedBadge.hidden = true;
        titleEl.textContent = `Построитель: ${saved.name}`;
        previewBtn.disabled = false;
        previewBtn.title = '';

        // Update cache
        if (creating) {
          this.buildersCache = this.buildersCache ? [...this.buildersCache, saved] : [saved];
        } else {
          this.buildersCache = this.buildersCache?.map(x => x.id === saved.id ? saved : x) ?? null;
        }
        void this.loadBuilders();
        this.toast(creating ? 'Построитель создан.' : 'Построитель сохранён.');
        submitting = false;
        saveBtn.disabled = false; saveBtn.removeAttribute('aria-busy');
        setLabel(saveBtn, 'Сохранить');
        showStatus({ tone: 'success', text: 'Изменения сохранены.' });
      } catch (error) {
        if (!isOpen()) return;
        submitting = false;
        saveBtn.disabled = false; saveBtn.removeAttribute('aria-busy');
        setLabel(saveBtn, currentBuilder === null ? 'Создать' : 'Сохранить');
        if (await this.endIfSignedOut(error, isOpen)) return;
        const code = error instanceof ApiError ? error.code : '';
        const status = error instanceof ApiError ? error.status : 0;
        if (status === 409 && code === 'version_conflict') {
          showStatus({ tone: 'error', text: 'Конфликт версий: построитель был изменён в другой вкладке. Закройте редактор и откройте снова.' });
        } else if (status === 409 && code === 'name_taken') {
          nameF.setError('Построитель с таким названием уже существует.');
          nameF.inp.focus();
        } else {
          showStatus({ tone: 'error', text: `Не удалось сохранить. ${errorText(error)}` });
        }
      }
    });

    this.dialog = handle;
    this.root.append(dialog);
    dialog.showModal();
    nameF.inp.focus();
  }

  // ---- Item row in builder editor ----
  // A rule reads against the real catalogue of its source: a country rule
  // shows how many servers it yields now, a rule on an unlinked or disabled
  // source says it serves nothing.
  private buildItemRow(
    item: BuilderItem,
    index: number,
    allItems: BuilderItem[],
    onChange: (updated: BuilderItem[]) => void,
    ctx: RuleContext,
  ): HTMLElement {
    const summary = ruleSummary(item, ctx.sources(), ctx.selectedIds());
    const li = el('li', item.enabled ? 'item-row' : 'item-row is-disabled');
    li.draggable = true;
    li.dataset.dragIdx = String(index);
    li.dataset.kind = item.kind;
    li.setAttribute('aria-label', summary.title);

    const dragHandle = el('span', 'drag-handle');
    dragHandle.append(icon('drag'));
    dragHandle.setAttribute('aria-hidden', 'true');

    const body = el('div', 'item-body');
    const head = el('div', 'item-head');
    head.append(
      el('span', item.kind === 'country' ? 'tag tag-on' : 'tag', item.kind === 'country' ? 'Страна' : 'Сервер'),
      el('span', 'item-name', summary.title),
    );
    if (!item.enabled) head.append(el('span', 'tag', 'Выключено'));
    body.append(head, el('p', 'item-detail', summary.detail));
    if (summary.warning) body.append(el('p', 'item-warn', summary.warning));

    const editBtn = button('Изменить', 'btn btn-secondary btn-xs', 'edit');
    editBtn.setAttribute('aria-label', `Изменить правило «${summary.title}»`);
    editBtn.addEventListener('click', () => {
      this.openItemEditor(item, ctx, (updated) => {
        onChange(allItems.map((it, i) => i === index ? { ...updated, position: it.position } : it));
      });
    });

    const deleteBtn = button('Удалить', 'btn btn-secondary btn-xs', 'trash');
    deleteBtn.setAttribute('aria-label', `Удалить правило «${summary.title}»`);
    deleteBtn.addEventListener('click', () => {
      if (!window.confirm('Удалить правило?')) return;
      // The builder editor deletes it on the server on the next save.
      onChange(allItems.filter((_, i) => i !== index).map((it, pos) => ({ ...it, position: pos })));
    });

    const acts = el('div', 'item-actions');
    acts.append(editBtn, deleteBtn);

    li.append(dragHandle, body, acts);
    return li;
  }

  // ---- Item editor sub-dialog ----
  // Source, country and server come from the source catalogues; nothing is
  // typed by hand except an ISO code for a country not in the catalogue yet.
  private openItemEditor(item: BuilderItem | null, ctx: RuleContext, onSave: (item: BuilderItem) => void) {
    const isNew = item === null;
    const dialog = el('dialog', 'modal modal-wide rule-editor');
    dialog.setAttribute('aria-labelledby', 'ie-title');
    const close = () => { if (dialog.open) dialog.close(); dialog.remove(); };

    const titleEl = el('h2', 'modal-title', isNew ? 'Новое правило' : 'Изменить правило');
    titleEl.id = 'ie-title';

    // The builder's sources in their order, plus the rule's own source when
    // it was unlinked meanwhile (so the rule can be moved to a linked one).
    const all = ctx.sources();
    const selectedIds = ctx.selectedIds();
    const choices = selectedIds.map(id => all.find(s => s.id === id)).filter((s): s is AdminSource => s !== undefined);
    if (item && !choices.some(s => s.id === item.source_id)) {
      const own = all.find(s => s.id === item.source_id);
      if (own) choices.push(own);
    }

    const kindSel = el('select', 'input');
    kindSel.id = 'ie-kind';
    kindSel.append(option('country', 'Страна — все её серверы (динамически)'), option('node', 'Конкретный сервер'));
    kindSel.value = item?.kind ?? 'country';
    const kindF = field('Тип правила', kindSel);

    const sourceSel = el('select', 'input');
    sourceSel.id = 'ie-source';
    for (const s of choices) {
      const unlinked = selectedIds.includes(s.id) ? '' : ' (не подключён)';
      sourceSel.append(option(String(s.id), `${s.name} — ${catalogueSummary(s.catalogue)}${unlinked}`));
    }
    sourceSel.value = String(item?.source_id ?? choices[0]?.id ?? '');
    const sourceF = field('Источник', sourceSel);
    const sourceHint = el('p', 'field-hint');
    sourceF.root.append(sourceHint);
    const currentSource = () => choices.find(s => String(s.id) === sourceSel.value);

    // ---- Country rule ----
    const OTHER = '*other';
    const countrySel = el('select', 'input');
    countrySel.id = 'ie-country';
    const countryF = field('Страна', countrySel);
    const countryHint = el('p', 'field-hint rule-live');
    countryHint.setAttribute('aria-live', 'polite');
    countryF.root.append(countryHint);
    const otherInp = el('input', 'input');
    Object.assign(otherInp, { id: 'ie-country-other', type: 'text', maxLength: 2, placeholder: 'Например: NL', spellcheck: false, autocomplete: 'off' });
    const otherF = field('Код страны (ISO 3166-1 alpha-2)', otherInp);
    otherF.root.append(el('p', 'field-hint', 'Правило начнёт работать, когда серверы этой страны появятся в источнике.'));
    let countryValue = item?.kind === 'country' ? item.country_code.toUpperCase() : '';
    const chosenCountry = () => (countrySel.value === OTHER ? otherInp.value.trim().toUpperCase() : countrySel.value);
    const syncCountry = () => {
      otherF.root.hidden = kindSel.value !== 'country' || countrySel.value !== OTHER;
      const code = chosenCountry();
      countryHint.textContent = COUNTRY_CODE.test(code)
        ? `Динамическое правило: при каждой сборке подписки берутся все серверы ${countryLabel(code)} из источника. Сейчас в каталоге: ${serversLabel(countryCount(currentSource(), code))}.`
        : '';
    };
    const fillCountries = () => {
      const list = currentSource()?.catalogue.by_country ?? [];
      countrySel.replaceChildren(...list.map(({ code, count }) => option(code, `${countryLabel(code)} — ${serversLabel(count)}`)),
        option(OTHER, 'Другая страна…'));
      if (countryValue && list.some(c => c.code === countryValue)) countrySel.value = countryValue;
      else if (countryValue) { countrySel.value = OTHER; otherInp.value = countryValue; }
      else countrySel.value = list[0]?.code ?? OTHER;
      syncCountry();
    };
    countrySel.addEventListener('change', () => {
      countryValue = chosenCountry();
      countryF.setError(''); otherF.setError('');
      syncCountry();
    });
    otherInp.addEventListener('input', () => {
      countryValue = chosenCountry();
      otherF.setError('');
      syncCountry();
    });

    // ---- Node rule: pick a server from the source catalogue ----
    const nodeBox = el('div', 'field rule-node');
    const nodeLabel = el('p', 'field-label', 'Сервер');
    nodeLabel.id = 'ie-node-label';
    const nodeChosen = el('p', 'rule-node-chosen');
    nodeChosen.setAttribute('aria-live', 'polite');
    const nodeSearch = el('input', 'input');
    Object.assign(nodeSearch, { id: 'ie-node-q', type: 'search', placeholder: 'Поиск по имени', spellcheck: false, autocomplete: 'off' });
    nodeSearch.setAttribute('aria-label', 'Поиск сервера по имени');
    const nodeCountry = el('select', 'input');
    nodeCountry.id = 'ie-node-country';
    nodeCountry.setAttribute('aria-label', 'Страна сервера');
    const nodeTools = el('div', 'rule-node-tools');
    nodeTools.append(nodeSearch, nodeCountry);
    const nodeList = el('ul', 'entry-pick');
    nodeList.setAttribute('aria-labelledby', nodeLabel.id);
    const nodeCount = el('p', 'field-hint');
    const nodeError = el('p', 'field-error');
    nodeBox.append(nodeLabel, nodeChosen, nodeTools, nodeList, nodeCount, nodeError);

    let picked: { sourceId: number; fingerprint: string; original_name: string } | null =
      item?.kind === 'node' ? { sourceId: item.source_id, fingerprint: item.fingerprint, original_name: item.original_name } : null;
    let entries: readonly SourceEntry[] | null = null;
    let entriesFor = 0;
    let entriesRequest = 0;

    const paintNodes = () => {
      const src = currentSource();
      const mine = picked && src && picked.sourceId === src.id ? picked : null;
      const res = mine && entries ? resolveNode(mine.fingerprint, mine.original_name, entries) : null;
      if (!mine) {
        nodeChosen.textContent = 'Сервер не выбран.';
        nodeChosen.className = 'rule-node-chosen';
      } else {
        const name = mine.original_name || shortFingerprint(mine.fingerprint);
        nodeChosen.textContent = `Выбран: ${name}${res ? ` · ${RESOLUTION_LABELS[res.status]}` : ''}`;
        nodeChosen.className = res && (res.status === 'missing' || res.status === 'conflict') ? 'rule-node-chosen is-danger' : 'rule-node-chosen';
      }
      if (!entries) return;
      const { shown, total } = pickableEntries(entries, nodeSearch.value, nodeCountry.value);
      nodeList.replaceChildren(...shown.map(e => {
        const li = el('li');
        const pick = el('button', 'entry-pick-item');
        pick.type = 'button';
        pick.dataset.fingerprint = e.fingerprint;
        pick.setAttribute('aria-pressed', String(res?.entry?.fingerprint === e.fingerprint));
        pick.append(el('span', 'entry-pick-name', e.original_name || '(без имени)'),
          el('span', 'entry-pick-meta', [e.country_code ? countryLabel(e.country_code) : 'Без страны', e.protocol].filter(Boolean).join(' · ')));
        pick.addEventListener('click', () => {
          picked = { sourceId: e.source_id, fingerprint: e.fingerprint, original_name: e.original_name };
          nodeError.textContent = '';
          paintNodes();
          nodeList.querySelector<HTMLButtonElement>(`[data-fingerprint="${CSS.escape(e.fingerprint)}"]`)?.focus();
        });
        li.append(pick);
        return li;
      }));
      if (total === 0) {
        nodeList.append(el('li', 'entry-pick-empty',
          entries.length === 0 ? 'Каталог источника пуст. Синхронизируйте источник.' : 'Нет серверов под выбранный фильтр.'));
      }
      nodeCount.textContent = total > shown.length
        ? `Показаны первые ${formatCount(shown.length)} из ${formatCount(total)}. Уточните поиск.`
        : `Серверов: ${formatCount(total)}`;
    };

    const loadEntries = async () => {
      const src = currentSource();
      if (!src) return;
      if (entries && entriesFor === src.id) { paintNodes(); return; }
      const request = ++entriesRequest;
      entries = null;
      nodeCount.textContent = '';
      nodeList.replaceChildren(el('li', 'entry-pick-empty', 'Загружаем серверы источника…'));
      paintNodes();
      try {
        const list = await this.api.listSourceEntries(src.id);
        if (request !== entriesRequest || !dialog.isConnected) return;
        entries = list;
        entriesFor = src.id;
        nodeCountry.replaceChildren(option('', 'Все страны'),
          ...src.catalogue.by_country.map(({ code, count }) => option(code, `${countryLabel(code)} (${formatCount(count)})`)));
        if (src.catalogue.no_country > 0) nodeCountry.append(option('-', `Без страны (${formatCount(src.catalogue.no_country)})`));
        paintNodes();
      } catch (error) {
        if (request !== entriesRequest || !dialog.isConnected) return;
        if (await this.endIfSignedOut(error, () => dialog.isConnected)) { close(); return; }
        nodeList.replaceChildren(el('li', 'entry-pick-empty', `Не удалось загрузить серверы. ${errorText(error)}`));
      }
    };
    nodeSearch.addEventListener('input', paintNodes);
    nodeCountry.addEventListener('change', paintNodes);

    const mkInp = (id: string, labelText: string, value = '', hint?: string) => {
      const inp = el('input', 'input');
      Object.assign(inp, { id, type: 'text', value, spellcheck: false, autocomplete: 'off' });
      const f = field(labelText, inp);
      if (hint) f.root.append(el('p', 'field-hint', hint));
      return { inp, ...f };
    };
    const customNameF = mkInp('ie-custom-name', 'Своё имя', item?.custom_name ?? '',
      'Если задано, заменяет имя сервера в подписке (для правила страны — у каждого её сервера).');
    const descF = mkInp('ie-desc', 'Описание', item?.description ?? '', 'Видно только в панели.');

    const enabledChk = el('input', 'checkbox');
    enabledChk.type = 'checkbox';
    enabledChk.id = 'ie-enabled';
    enabledChk.checked = item?.enabled ?? true;
    const enabledLabel = el('label', 'checkbox-label', 'Включено');
    enabledLabel.htmlFor = 'ie-enabled';
    const enabledRow = el('div', 'checkbox-row');
    enabledRow.append(enabledChk, enabledLabel);

    const syncSource = () => {
      const src = currentSource();
      sourceHint.textContent = !src ? ''
        : !selectedIds.includes(src.id) ? 'Источник не подключён к построителю: такое правило не применяется. Выберите подключённый источник.'
          : !src.enabled ? 'Источник отключён: сборка его пропускает.'
            : syncState(src) === 'never' ? 'Источник ещё не синхронизирован: каталог пуст.'
              : `Каталог: ${catalogueSummary(src.catalogue)} · ${lastSyncText(src)}`;
      fillCountries();
    };
    const updateKind = () => {
      const isCountry = kindSel.value === 'country';
      countryF.root.hidden = !isCountry;
      nodeBox.hidden = isCountry;
      syncCountry();
      if (!isCountry) void loadEntries();
    };
    sourceSel.addEventListener('change', () => { syncSource(); if (kindSel.value === 'node') void loadEntries(); });
    kindSel.addEventListener('change', updateKind);

    const statusEl = el('div', 'modal-status');
    statusEl.setAttribute('role', 'alert');

    const cancelBtn = button('Отмена', 'btn btn-secondary');
    const saveBtn = button('Применить', 'btn btn-primary');
    const acts = el('div', 'modal-actions');
    acts.append(cancelBtn, saveBtn);

    const body = el('div', 'modal-body');
    if (choices.length === 0) {
      saveBtn.disabled = true;
      body.append(titleEl, notice({ tone: 'info', text: 'Сначала добавьте в построитель хотя бы один источник: правила выбирают серверы из его каталога.' }), acts);
    } else {
      body.append(titleEl, kindF.root, sourceF.root, countryF.root, otherF.root, nodeBox,
        customNameF.root, descF.root, enabledRow, statusEl, acts);
      syncSource();
      updateKind();
    }
    dialog.append(body);

    cancelBtn.addEventListener('click', close);
    dialog.addEventListener('cancel', e => { e.preventDefault(); close(); });

    saveBtn.addEventListener('click', () => {
      const src = currentSource();
      if (!src) return;
      const kind = kindSel.value as 'country' | 'node';
      let countryCode = '';
      if (kind === 'country') {
        countryCode = chosenCountry();
        if (!COUNTRY_CODE.test(countryCode)) {
          const target = countrySel.value === OTHER ? otherF : countryF;
          target.setError('Укажите двухбуквенный код страны латиницей, например DE.');
          (countrySel.value === OTHER ? otherInp : countrySel).focus();
          return;
        }
      }
      const node = kind === 'node' && picked && picked.sourceId === src.id ? picked : null;
      if (kind === 'node' && !node) {
        nodeError.textContent = 'Выберите сервер из списка.';
        nodeSearch.focus();
        return;
      }
      onSave({
        id: item?.id ?? 0,
        kind,
        source_id: src.id,
        country_code: countryCode,
        fingerprint: node?.fingerprint ?? '',
        original_name: node?.original_name ?? '',
        custom_name: customNameF.inp.value.trim() || null,
        description: descF.inp.value.trim(),
        position: item?.position ?? 0,
        enabled: enabledChk.checked,
      });
      close();
    });

    document.body.append(dialog);
    dialog.showModal();
    kindSel.focus();
  }

  // ---- Preview ----
  // Dry run of the SAVED builder by the backend against the stored catalogue,
  // with the same rules /sub applies.
  private async runPreview(builder: AdminBuilder | null, body: HTMLElement, btn: HTMLButtonElement) {
    if (!builder) return;
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    body.replaceChildren(el('p', 'preview-loading', 'Загружаем предпросмотр…'));
    const sourceName = (id: number) => this.sourcesCache?.find(s => s.id === id)?.name ?? `Источник #${id}`;
    try {
      const preview = await this.api.previewBuilder(builder.id);
      body.replaceChildren();
      const sum = previewSummary(preview);

      const stats = el('p', 'preview-stats');
      stats.textContent = [
        `В подписке: ${serversLabel(sum.servers)} · ${countriesLabel(sum.countries)}`,
        preview.missing > 0 ? `не найдено: ${formatCount(preview.missing)}` : '',
        preview.conflicts > 0 ? `неоднозначно: ${formatCount(preview.conflicts)}` : '',
      ].filter(Boolean).join(' · ');
      body.append(stats);
      if (sum.bySource.length > 1) {
        body.append(el('p', 'preview-sources',
          sum.bySource.map(s => `${sourceName(s.sourceId)}: ${formatCount(s.count)}`).join(' · ')));
      }

      if (preview.warnings && preview.warnings.length > 0) {
        const warnBox = el('div', 'preview-warnings');
        for (const w of preview.warnings) warnBox.append(el('p', 'preview-warn', w));
        body.append(warnBox);
      }

      if (preview.items.length === 0) {
        body.append(el('p', 'preview-empty', 'Предпросмотр пуст — нет подходящих серверов.'));
      } else {
        const list = el('ol', 'preview-list');
        list.setAttribute('aria-label', 'Серверы подписки');
        for (const pi of preview.items) {
          const li = el('li', `preview-item preview-${pi.status}`);
          const served = pi.status === 'matched' || pi.status === 'fallback';
          const pos = el('span', 'preview-pos', served ? String(pi.position + 1) : '—');
          const name = el('span', 'preview-name', pi.display_name || pi.entry?.original_name || '—');
          const code = pi.entry?.country_code ?? '';
          const country = el('span', 'preview-country', code ? countryLabel(code) : '');
          const origin = el('span', 'preview-origin', sourceName(pi.source_id));
          const statusBadge = el('span', `tag preview-status-${pi.status}`, RESOLUTION_LABELS[pi.status] ?? pi.status);
          li.append(pos, name, country, origin, statusBadge);
          list.append(li);
        }
        body.append(list);
      }
    } catch (error) {
      if (await this.endIfSignedOut(error, () => body.isConnected)) return;
      body.replaceChildren(el('p', 'preview-error', `Ошибка предпросмотра: ${errorText(error)}`));
    } finally {
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
    }
  }

  // ===========================================================================
  // Tariffs: catalogue list and order (#/tariffs)
  // ===========================================================================

  private buildTariffsSection(header: HTMLElement): HTMLElement {
    const { actions, refresh, updated } = refreshActions(() => void this.loadTariffs());
    const add = el('a', 'btn btn-primary btn-sm');
    add.href = '#/tariffs/new';
    add.append(icon('plus'), el('span', 'btn-label', 'Новый тариф'));
    actions.prepend(add);
    actions.classList.add('tariffs-actions');
    header.classList.add('has-actions');
    header.append(actions);

    const root = el('div', 'tariffs');
    const toolbar = el('div', 'toolbar tariffs-toolbar');
    const summary = el('p', 'tariffs-summary');
    const toggle = el('label', 'toggle');
    const retired = el('input');
    retired.type = 'checkbox';
    retired.checked = this.showRetired;
    retired.addEventListener('change', () => {
      this.showRetired = retired.checked;
      if (this.tariffsView && this.tariffsCache) this.paintTariffs(this.tariffsView);
    });
    toggle.append(retired, el('span', '', 'Показывать прежние версии'));
    toolbar.append(summary, toggle);
    const body = el('div', 'tariffs-body');
    root.append(toolbar, body);

    const view: TariffsView = { body, refresh, updated, summary };
    this.tariffsView = view;
    this.unsavedChanges = () => this.pendingOrder !== null;
    if (this.tariffsCache) this.paintTariffs(view);
    else this.paintTariffsSkeleton(view);
    return root;
  }

  private paintTariffsSkeleton(view: TariffsView) {
    setLoading(view.body, 'Загружаем тарифы');
    view.body.replaceChildren(skeletonPanel(5));
    view.updated.textContent = '';
    view.summary.textContent = '';
  }

  private paintTariffsError(view: TariffsView, error: unknown) {
    setLoading(view.body, null);
    view.body.replaceChildren(messagePanel('alert', 'Не удалось загрузить тарифы', errorText(error), 'alert',
      retryButton(() => void this.loadTariffs())));
    view.updated.textContent = '';
    view.summary.textContent = '';
  }

  /** Current (not retired) tariffs in the saved catalogue order. */
  private liveTariffIds(): number[] {
    return catalogueOrder(this.tariffsCache ?? []).filter(tariff => !isRetired(tariff)).map(tariff => tariff.id);
  }

  private paintTariffs(view: TariffsView, focusKey?: string) {
    setLoading(view.body, null);
    const all = this.tariffsCache ?? [];
    const ordered = catalogueOrder(all);
    const live = ordered.filter(tariff => !isRetired(tariff));
    const retired = ordered.filter(isRetired);
    const onSale = live.filter(tariff => tariff.is_active).length;
    view.updated.textContent = this.tariffsAt ? `Обновлено в ${clockSeconds(this.tariffsAt)}` : '';
    const parts = [`${formatCount(live.length)} ${pluralRu(live.length, 'тариф', 'тарифа', 'тарифов')}`, `в продаже ${formatCount(onSale)}`];
    if (retired.length) parts.push(`прежних версий ${formatCount(retired.length)}`);
    view.summary.textContent = all.length ? parts.join(' · ') : '';

    if (all.length === 0) {
      const create = el('a', 'btn btn-primary btn-sm');
      create.href = '#/tariffs/new';
      create.append(icon('plus'), el('span', 'btn-label', 'Создать тариф'));
      view.body.replaceChildren(messagePanel('tariffs', 'Тарифов пока нет',
        'Создайте первый тариф: включённый тариф сразу появится в каталоге Mini App и в боте.', 'status', create));
      return;
    }

    const byId = new Map(all.map(tariff => [tariff.id, tariff] as const));
    const order = (this.pendingOrder ?? live.map(tariff => tariff.id))
      .map(id => byId.get(id))
      .filter((tariff): tariff is AdminTariff => tariff !== undefined);
    const nodes: HTMLElement[] = [];
    if (this.pendingOrder) nodes.push(this.orderBanner());

    const panel = el('section', 'panel');
    panel.setAttribute('aria-labelledby', 'tariffs-list-title');
    panel.append(panelHead('tariffs-list-title', 'Каталог', 'Порядок показа в Mini App и боте'));
    if (order.length) {
      const list = el('ol', 'tariff-list');
      order.forEach((tariff, index) => list.append(this.tariffRow(tariff, index, order.length)));
      panel.append(list);
    } else {
      panel.append(el('p', 'panel-note', 'Действующих тарифов нет: остались только прежние версии. Создайте новый тариф.'));
    }
    nodes.push(panel);

    if (this.showRetired && retired.length) {
      const old = el('section', 'panel');
      old.setAttribute('aria-labelledby', 'tariffs-retired-title');
      old.append(panelHead('tariffs-retired-title', 'Прежние версии', formatCount(retired.length)),
        el('p', 'panel-note tariff-note', 'Заменены новыми версиями при изменении условий покупки. Подписки, купленные по ним, продолжают действовать.'));
      const list = el('ul', 'tariff-list');
      for (const tariff of retired) list.append(this.tariffRow(tariff, -1, 0));
      old.append(list);
      nodes.push(old);
    }
    view.body.replaceChildren(...nodes);
    if (focusKey) view.body.querySelector<HTMLElement>(`[data-focus-key='${focusKey}']`)?.focus();
  }

  /** One catalogue row; index -1 marks a retired version (not reorderable). */
  private tariffRow(tariff: AdminTariff, index: number, count: number): HTMLElement {
    const movable = index >= 0;
    const item = el('li', 'tariff-row');
    item.dataset.tariffId = String(tariff.id);
    if (movable) {
      const position = el('span', 'tariff-pos', String(index + 1));
      position.setAttribute('aria-hidden', 'true');
      item.append(position);
    }
    const info = el('div', 'tariff-info');
    const nameRow = el('div', 'tariff-name-row');
    const link = el('a', 'tariff-name', tariff.name);
    link.href = `#/tariffs/${tariff.id}`;
    nameRow.append(link, tariffStateTag(tariff));
    if (tariff.badge) nameRow.append(el('span', 'tag tag-accent', tariff.badge));
    const meta = [
      formatTariffPrice(tariff.price_cents, tariff.currency), durationLabel(tariff.duration_days),
      `план «${tariff.plan_name}»${tariff.plan_active ? '' : ' (отключён)'}`, tariffUsage(tariff),
    ];
    info.append(nameRow, el('p', 'tariff-meta', meta.join(' · ')));

    const actions = el('div', 'tariff-actions');
    if (movable) {
      const up = iconButton('up', `Поднять «${tariff.name}» выше`);
      up.dataset.focusKey = `up-${tariff.id}`;
      up.disabled = index === 0 || this.orderSaving;
      up.addEventListener('click', () => this.moveTariff(tariff.id, -1));
      const down = iconButton('chevron', `Опустить «${tariff.name}» ниже`);
      down.dataset.focusKey = `down-${tariff.id}`;
      down.disabled = index === count - 1 || this.orderSaving;
      down.addEventListener('click', () => this.moveTariff(tariff.id, 1));
      actions.append(up, down);
    }
    // A retired version can still be taken off sale, never put back on it.
    if (tariff.is_active || !isRetired(tariff)) {
      const label = tariff.is_active ? 'Скрыть' : 'Включить';
      const toggle = button(label, 'btn btn-secondary btn-sm');
      toggle.setAttribute('aria-label', `${label} «${tariff.name}»`);
      toggle.dataset.focusKey = `toggle-${tariff.id}`;
      toggle.addEventListener('click', () => void this.toggleTariff(tariff, toggle));
      actions.append(toggle);
    }
    item.append(info, actions);
    return item;
  }

  private orderBanner(): HTMLElement {
    const bar = el('div', 'unsaved-banner tariff-order-bar');
    bar.setAttribute('role', 'region');
    bar.setAttribute('aria-label', 'Несохранённый порядок');
    const text = el('p', 'unsaved-text', this.orderSaving ? 'Сохраняем порядок…' : 'Порядок тарифов изменён и ещё не сохранён.');
    const actions = el('div', 'unsaved-actions');
    const reset = button('Отменить', 'btn btn-secondary btn-sm');
    reset.disabled = this.orderSaving;
    reset.addEventListener('click', () => {
      this.pendingOrder = null;
      if (this.tariffsView) this.paintTariffs(this.tariffsView);
    });
    const save = button(this.orderSaving ? 'Сохраняем…' : 'Сохранить порядок', 'btn btn-primary btn-sm');
    save.dataset.focusKey = 'order-save';
    save.disabled = this.orderSaving;
    save.setAttribute('aria-busy', String(this.orderSaving));
    save.addEventListener('click', () => void this.saveTariffOrder());
    actions.append(reset, save);
    bar.append(text, actions);
    return bar;
  }

  private moveTariff(id: number, delta: -1 | 1) {
    const view = this.tariffsView;
    if (!view || !this.tariffsCache || this.orderSaving) return;
    const saved = this.liveTariffIds();
    const base = this.pendingOrder ?? saved;
    const from = base.indexOf(id);
    if (from === -1) return;
    const next = moveItem(base, from, from + delta);
    this.pendingOrder = next.join(',') === saved.join(',') ? null : next;
    // Keep focus on the arrow just used; at an edge it is disabled, so move to the other one.
    const to = next.indexOf(id);
    const atEdge = delta < 0 ? to === 0 : to === next.length - 1;
    const direction = delta < 0 ? (atEdge ? 'down' : 'up') : (atEdge ? 'up' : 'down');
    this.paintTariffs(view, `${direction}-${id}`);
  }

  /**
   * Saves the edited order. The same order retries with the same request key,
   * so an uncertain outcome is replayed instead of applied twice; a stale
   * catalogue (a tariff created or deleted meanwhile) drops the edited order.
   */
  private async saveTariffOrder() {
    const view = this.tariffsView;
    const cache = this.tariffsCache;
    if (!view || !cache || !this.pendingOrder || this.orderSaving) return;
    const ids = reorderIds(this.pendingOrder, cache);
    const signature = ids.join(',');
    if (!this.orderKey || this.orderKey.ids !== signature) this.orderKey = { ids: signature, key: newRequestKey() };
    const key = this.orderKey.key;
    this.orderSaving = true;
    this.paintTariffs(view, 'order-save');
    const current = () => this.tariffsView === view;
    const repaintOther = () => { if (this.tariffsView && this.tariffsView !== view) this.paintTariffs(this.tariffsView); };
    try {
      const tariffs = await this.api.reorderTariffs(ids, key);
      this.orderKey = null;
      this.orderSaving = false;
      this.tariffsCache = tariffs;
      this.tariffsAt = new Date();
      this.lastSessionCheck = Date.now();
      if (!current()) { repaintOther(); return; }
      this.pendingOrder = null;
      this.paintTariffs(view);
      this.toast('Порядок тарифов сохранён. Mini App и бот показывают его сразу.');
    } catch (error) {
      this.orderSaving = false;
      if (!current()) { repaintOther(); return; }
      if (await this.endIfSignedOut(error, current)) return;
      const code = error instanceof ApiError ? error.code : '';
      if (code === 'order_stale' || code === 'request_key_conflict' || code === 'invalid_request') {
        this.orderKey = null;
        this.pendingOrder = null;
        this.paintTariffs(view);
        this.toast(`Порядок не сохранён. ${tariffErrorText(error)} Список обновлён — расставьте тарифы заново.`);
        void this.loadTariffs();
        return;
      }
      this.paintTariffs(view, 'order-save');
      this.toast(`Порядок не сохранён. ${errorText(error)} Повторите: повтор не применит порядок дважды.`);
    }
  }

  private async toggleTariff(tariff: AdminTariff, trigger: HTMLButtonElement) {
    const view = this.tariffsView;
    if (!view) return;
    const active = !tariff.is_active;
    trigger.disabled = true;
    trigger.setAttribute('aria-busy', 'true');
    const current = () => this.tariffsView === view;
    try {
      // The version guards the change: a retry after an uncertain outcome is a
      // version conflict, never a second toggle.
      const outcome = await this.api.setTariffActive(tariff.id, tariff.version, active, newRequestKey());
      this.applyTariffOutcome(outcome);
      if (!current()) return;
      this.paintTariffs(view, `toggle-${tariff.id}`);
      this.toast(active
        ? `Тариф «${tariff.name}» включён и показывается в каталоге.`
        : `Тариф «${tariff.name}» скрыт из каталога. Купленные подписки продолжают действовать.`);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      trigger.disabled = false;
      trigger.removeAttribute('aria-busy');
      this.toast(`${active ? 'Тариф не включён' : 'Тариф не скрыт'}. ${tariffErrorText(error)}`);
      if (isStaleTariffError(error)) void this.loadTariffs();
    }
  }

  /** Keeps the catalogue snapshot in step with a mutation outcome. */
  private applyTariffOutcome(outcome: TariffOutcome) {
    if (!this.tariffsCache) return;
    const next = [...this.tariffsCache];
    for (const tariff of [outcome.tariff, outcome.previous]) {
      if (!tariff) continue;
      const index = next.findIndex(item => item.id === tariff.id);
      if (index === -1) next.push(tariff);
      else next[index] = tariff;
    }
    this.tariffsCache = next;
  }

  private removeCachedTariff(id: number) {
    if (this.tariffsCache) this.tariffsCache = this.tariffsCache.filter(tariff => tariff.id !== id);
  }

  private async loadTariffs() {
    const view = this.tariffsView;
    if (!view) return;
    const request = ++this.tariffsRequest;
    const current = () => this.tariffsView === view && request === this.tariffsRequest;
    setRefreshBusy(view.refresh, true);
    const shown = this.tariffsCache !== null;
    if (!shown) this.paintTariffsSkeleton(view);
    try {
      const tariffs = await this.api.listTariffs();
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      this.tariffsCache = tariffs;
      this.tariffsAt = new Date();
      // An edited order survives a refresh only while it covers the same tariffs.
      if (this.pendingOrder) {
        const live = this.liveTariffIds();
        const same = live.length === this.pendingOrder.length && this.pendingOrder.every(id => live.includes(id));
        if (!same) {
          this.pendingOrder = null;
          this.toast('Каталог изменился: несохранённый порядок сброшен.');
        }
      }
      this.paintTariffs(view);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      if (shown) this.toast(`Не удалось обновить тарифы. ${errorText(error)}`);
      else this.paintTariffsError(view, error);
    } finally {
      if (current()) setRefreshBusy(view.refresh, false);
    }
  }

  // ===========================================================================
  // Tariff editor (#/tariffs/new, #/tariffs/{id})
  // ===========================================================================

  private buildTariffEditor(id: number | null, title: HTMLElement, desc: HTMLElement): HTMLElement {
    const known = id === null ? null : this.tariffsCache?.find(tariff => tariff.id === id) ?? null;
    title.textContent = id === null ? 'Новый тариф' : known?.name ?? 'Тариф';
    desc.textContent = id === null ? NEW_TARIFF_DESC : `Тариф #${id}`;
    const body = el('div', 'tariff-editor');
    const view: TariffEditorView = { id, body, title, desc };
    this.tariffEditorView = view;
    this.paintTariffEditorSkeleton(view);
    return body;
  }

  private paintTariffEditorSkeleton(view: TariffEditorView) {
    setLoading(view.body, 'Загружаем тариф');
    const grid = el('div', 'editor-grid');
    const main = el('div', 'editor-main');
    main.append(skeletonPanel(8));
    const side = el('div', 'editor-side');
    side.append(skeletonPanel(4));
    grid.append(main, side);
    view.body.replaceChildren(grid);
  }

  private paintTariffMissing(view: TariffEditorView) {
    setLoading(view.body, null);
    view.title.textContent = 'Тариф не найден';
    view.desc.textContent = view.id === null ? '' : `Тариф #${view.id}`;
    const back = el('a', 'btn btn-secondary btn-sm', 'К списку тарифов');
    back.href = '#/tariffs';
    view.body.replaceChildren(messagePanel('tariffs', 'Тариф не найден',
      `Тарифа #${view.id} нет: возможно, его уже удалили.`, 'status', back));
  }

  private paintTariffEditorError(view: TariffEditorView, error: unknown) {
    setLoading(view.body, null);
    view.body.replaceChildren(messagePanel('alert', 'Не удалось загрузить тариф', errorText(error), 'alert',
      retryButton(() => void this.loadTariffEditor())));
  }

  private async loadTariffEditor() {
    const view = this.tariffEditorView;
    if (!view) return;
    const request = ++this.tariffEditorRequest;
    const current = () => this.tariffEditorView === view && request === this.tariffEditorRequest;
    this.paintTariffEditorSkeleton(view);
    try {
      const [plans, tariff] = await Promise.all([
        this.api.listPlans(),
        view.id === null ? Promise.resolve(null) : this.api.getTariff(view.id).catch((error: unknown) => {
          if (error instanceof ApiError && error.status === 404) return 'missing' as const;
          throw error;
        }),
      ]);
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      this.plansCache = plans;
      if (tariff === 'missing') {
        if (view.id !== null) this.removeCachedTariff(view.id);
        this.paintTariffMissing(view);
        return;
      }
      if (tariff) this.applyTariffOutcome({ tariff, previous: null, versioned: false, replayed: false });
      this.paintTariffEditor(view, tariff, plans);
    } catch (error) {
      if (!current()) return;
      if (await this.endIfSignedOut(error, current)) return;
      this.paintTariffEditorError(view, error);
    }
  }

  /**
   * The editor: purchase terms, card, publication, a live preview of the Mini
   * App card and the bot button, the plan with its builder, and sale/delete.
   * Saving is idempotent per payload (like openManage): an uncertain outcome
   * freezes the form so the retry carries exactly the same request.
   */
  private paintTariffEditor(view: TariffEditorView, loaded: AdminTariff | null, plans: readonly TariffPlan[]) {
    setLoading(view.body, null);
    let base = loaded;
    const selectable = plans.filter(plan => plan.selectable);
    const fallbackPlan = selectable.find(plan => plan.is_active) ?? selectable[0] ?? null;
    let baseline = formFromTariff(base, fallbackPlan?.id ?? null);
    let form: TariffForm = { ...baseline };
    let saving = false;
    let frozen = false;
    let pending: { payload: string; key: string } | null = null;
    const isCurrent = () => this.tariffEditorView === view;
    const retired = () => base !== null && isRetired(base);
    const dirty = () => !sameForm(form, baseline);
    this.unsavedChanges = () => isCurrent() && !retired() && (dirty() || frozen);

    // Status and notices ------------------------------------------------------
    const retiredSlot = el('div', 'editor-status');
    const statusSlot = el('div', 'editor-status');
    const showStatus = (next: Notice | null, actions: HTMLElement[] = [], focus = true) => {
      if (!next) { statusSlot.replaceChildren(); return; }
      const node = noticeWithActions(next, actions);
      node.setAttribute('role', next.tone === 'error' ? 'alert' : 'status');
      node.tabIndex = -1;
      statusSlot.replaceChildren(node);
      if (focus) node.focus();
    };
    const backLink = () => {
      const link = el('a', 'btn btn-secondary btn-sm', 'К списку тарифов');
      link.href = '#/tariffs';
      return link;
    };

    const paintHeading = () => {
      const name = base?.name ?? 'Новый тариф';
      view.title.textContent = name;
      document.title = `${name} · RS8 Admin`;
      view.desc.textContent = base
        ? [`Тариф #${base.id}`, base.previous_id ? `новая версия тарифа #${base.previous_id}` : '', `изменён ${formatDateTime(base.updated_at)}`]
          .filter(Boolean).join(' · ')
        : NEW_TARIFF_DESC;
    };

    // Controls ----------------------------------------------------------------
    const setters = new Map<TariffField, (text: string) => void>();
    const targets = new Map<TariffField, HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>();
    const counters: (() => void)[] = [];
    const register = (key: TariffField, control: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement, setError: (text: string) => void) => {
      setters.set(key, setError);
      targets.set(key, control);
    };
    const textInput = (id: string, inputMode: 'text' | 'numeric' | 'decimal' = 'text') => {
      const node = el('input', 'input');
      node.id = id;
      node.type = 'text';
      node.autocomplete = 'off';
      node.inputMode = inputMode;
      return node;
    };
    const hint = (root: HTMLElement, text: string) => {
      const node = el('p', 'field-hint', text);
      root.append(node);
      return node;
    };
    const counter = (root: HTMLElement, control: HTMLInputElement | HTMLTextAreaElement, limit: number) => {
      const node = el('p', 'counter');
      node.setAttribute('aria-hidden', 'true');
      root.append(node);
      counters.push(() => {
        const length = Array.from(control.value.trim()).length;
        node.textContent = `${formatCount(length)} / ${formatCount(limit)}`;
        node.classList.toggle('is-over', length > limit);
      });
    };
    const chipGroup = (label: string, values: readonly { value: string; text: string }[], apply: (value: string) => void) => {
      const group = el('div', 'chips');
      group.setAttribute('role', 'group');
      group.setAttribute('aria-label', label);
      const chips = values.map(({ value, text }) => {
        const chip = button(text, 'chip');
        chip.dataset.value = value;
        chip.setAttribute('aria-pressed', 'false');
        chip.addEventListener('click', () => apply(value));
        group.append(chip);
        return chip;
      });
      return { group, chips };
    };

    const name = textInput('tf-name');
    const nameField = field('Название', name);
    register('name', name, nameField.setError);
    counter(nameField.root, name, TARIFF_LIMITS.name);

    const plan = el('select', 'input');
    plan.id = 'tf-plan';
    if (!selectable.length) plan.append(option('', 'Нет планов, доступных для продажи'));
    for (const item of plans) {
      if (!item.selectable && item.id !== base?.plan_id) continue;
      const entry = option(String(item.id), `${item.name}${item.is_active ? '' : ' — план отключён'}${item.selectable ? '' : ' — системный'}`);
      entry.disabled = !item.selectable;
      plan.append(entry);
    }
    if (base && !plans.some(item => item.id === base?.plan_id)) plan.append(option(String(base.plan_id), base.plan_name || `План #${base.plan_id}`));
    const planField = field('План', plan);
    register('planId', plan, planField.setError);
    hint(planField.root, selectable.length
      ? 'План задаёт лимиты подписки и построитель конфигурации. Системные планы (пробный, бесплатный) не продаются.'
      : 'Нет планов, доступных для продажи: создайте план в боте, затем вернитесь сюда.');

    const duration = textInput('tf-duration', 'numeric');
    const durationField = field('Срок, дней', duration);
    register('durationDays', duration, durationField.setError);
    const durationChips = chipGroup('Быстрый выбор срока',
      DURATION_PRESETS.map(days => ({ value: String(days), text: durationLabel(days) })),
      value => { duration.value = value; durationField.setError(''); sync(); });
    durationField.root.append(durationChips.group);

    const price = textInput('tf-price', 'decimal');
    const priceField = field('Цена', price);
    register('price', price, priceField.setError);
    const priceHint = hint(priceField.root, '');

    const currency = el('select', 'input');
    currency.id = 'tf-currency';
    const currencies: string[] = [...TARIFF_CURRENCIES];
    if (base && !currencies.includes(base.currency)) currencies.push(base.currency);
    for (const code of currencies) currency.append(option(code, CURRENCY_LABELS[code] ?? code));
    const currencyField = field('Валюта', currency);
    register('currency', currency, currencyField.setError);
    const priceRow = el('div', 'form-row');
    priceRow.append(priceField.root, currencyField.root);

    const description = el('textarea', 'input textarea');
    description.id = 'tf-description';
    description.rows = 3;
    const descriptionField = field('Описание', description);
    register('description', description, descriptionField.setError);
    counter(descriptionField.root, description, TARIFF_LIMITS.description);

    const features = el('textarea', 'input textarea');
    features.id = 'tf-features';
    features.rows = 4;
    const featuresField = field('Преимущества', features);
    register('features', features, featuresField.setError);
    const featuresHint = hint(featuresField.root, '');

    const badge = textInput('tf-badge');
    const badgeField = field('Бейдж', badge);
    register('badge', badge, badgeField.setError);
    counter(badgeField.root, badge, TARIFF_LIMITS.badge);
    const badgeChips = chipGroup('Готовые бейджи',
      [...BADGE_PRESETS.map(text => ({ value: text, text })), { value: '', text: 'Без бейджа' }],
      value => { badge.value = value; badgeField.setError(''); sync(); });
    badgeField.root.append(badgeChips.group);

    const sort = textInput('tf-sort', 'numeric');
    const sortField = field('Позиция в каталоге', sort);
    register('sortOrder', sort, sortField.setError);
    hint(sortField.root, base
      ? 'Меньше — выше. Порядок удобнее менять стрелками в списке тарифов.'
      : 'Меньше — выше. Оставьте пустым, чтобы добавить тариф в конец каталога.');

    const active = el('input');
    active.type = 'checkbox';
    active.id = 'tf-active';
    const activeRow = el('label', 'toggle');
    activeRow.htmlFor = 'tf-active';
    activeRow.append(active, el('span', '', 'В продаже: показывать в каталоге Mini App и бота'));

    const textControls = [name, duration, price, description, features, badge, sort];
    const choiceControls = [plan, currency, active];
    const chips = [...durationChips.chips, ...badgeChips.chips];

    const read = (): TariffForm => ({
      // Same key order as formFromTariff: sameForm compares serialized forms.
      name: name.value, planId: plan.value ? Number(plan.value) : null, durationDays: duration.value, price: price.value,
      currency: currency.value, description: description.value, features: features.value, badge: badge.value,
      isActive: active.checked, sortOrder: sort.value,
    });
    const write = (next: TariffForm) => {
      name.value = next.name;
      plan.value = next.planId === null ? '' : String(next.planId);
      duration.value = next.durationDays;
      price.value = next.price;
      currency.value = next.currency;
      description.value = next.description;
      features.value = next.features;
      badge.value = next.badge;
      active.checked = next.isActive;
      sort.value = next.sortOrder;
    };
    const clearErrors = () => { for (const setError of setters.values()) setError(''); };
    const showFieldErrors = (errors: FieldErrors) => {
      let first: HTMLElement | null = null;
      for (const key of TARIFF_FIELD_ORDER) {
        const text = errors[key];
        if (!text) continue;
        setters.get(key)?.(text);
        first ??= targets.get(key) ?? null;
      }
      first?.focus();
    };

    // Panels ------------------------------------------------------------------
    const termsSlot = el('div', 'terms-note');
    const termsPanel = el('section', 'panel');
    termsPanel.setAttribute('aria-labelledby', 'tf-terms-title');
    const termsBody = el('div', 'tariff-form');
    termsBody.append(termsSlot, nameField.root, planField.root, durationField.root, priceRow);
    termsPanel.append(panelHead('tf-terms-title', 'Условия покупки'), termsBody);

    const cardPanel = el('section', 'panel');
    cardPanel.setAttribute('aria-labelledby', 'tf-card-title');
    const cardBody = el('div', 'tariff-form');
    cardBody.append(descriptionField.root, featuresField.root, badgeField.root);
    cardPanel.append(panelHead('tf-card-title', 'Карточка'), cardBody);

    const publishPanel = el('section', 'panel');
    publishPanel.setAttribute('aria-labelledby', 'tf-publish-title');
    const publishBody = el('div', 'tariff-form');
    publishBody.append(activeRow, sortField.root);
    publishPanel.append(panelHead('tf-publish-title', 'Публикация'), publishBody);

    const dirtyNote = el('p', 'unsaved-text');
    dirtyNote.setAttribute('aria-live', 'polite');
    const reset = button('Отменить изменения', 'btn btn-secondary');
    const save = button('Сохранить', 'btn btn-primary');
    save.type = 'submit';
    const actionBar = el('div', 'panel editor-actions');
    actionBar.append(dirtyNote, reset, save);

    const formEl = el('form', 'editor-main');
    formEl.noValidate = true;
    formEl.setAttribute('aria-label', 'Параметры тарифа');
    formEl.append(retiredSlot, statusSlot, termsPanel, cardPanel, publishPanel, actionBar);

    const previewPanel = el('section', 'panel');
    previewPanel.setAttribute('aria-labelledby', 'tf-preview-title');
    const previewBody = el('div', 'tariff-preview');
    previewPanel.append(panelHead('tf-preview-title', 'Предпросмотр'), previewBody);

    const planPanel = el('section', 'panel');
    planPanel.setAttribute('aria-labelledby', 'tf-plan-title');
    const planBody = el('div');
    planPanel.append(panelHead('tf-plan-title', 'План и построитель'), planBody);

    const managePanel = el('section', 'panel');
    managePanel.setAttribute('aria-labelledby', 'tf-manage-title');
    const manageBody = el('div', 'tariff-manage');
    managePanel.append(panelHead('tf-manage-title', 'Продажа и удаление'), manageBody);

    const side = el('div', 'editor-side');
    side.append(previewPanel, planPanel);
    if (base) side.append(managePanel);
    const grid = el('div', 'editor-grid');
    grid.append(formEl, side);
    view.body.replaceChildren(grid);

    // Painters ----------------------------------------------------------------
    const paintTerms = () => {
      termsSlot.replaceChildren();
      if (!base || retired() || !base.in_use) return;
      const { input } = validateTariffForm(form);
      const versioned = input !== null && termsChanged(base, input);
      const node = notice({
        tone: 'info',
        text: versioned
          ? `Тариф уже покупали (${tariffUsage(base)}). Сохранение создаст новую версию тарифа: текущая будет снята с продажи, купленные подписки не изменятся.`
          : `Тариф уже покупали (${tariffUsage(base)}). Название, план, срок, цена и валюта защищены: их изменение создаст новую версию. Карточку и публикацию можно менять свободно.`,
      });
      node.dataset.terms = versioned ? 'versioned' : 'protected';
      termsSlot.append(node);
    };

    const paintPreview = () => {
      const code = form.currency;
      const amount = parsePriceInput(form.price, code);
      const title = form.name.trim() || 'Название тарифа';
      const days = /^\d{1,5}$/.test(form.durationDays.trim()) ? Number(form.durationDays.trim()) : 0;
      const card = el('div', form.isActive ? 'offer-preview' : 'offer-preview is-hidden');
      card.setAttribute('role', 'group');
      card.setAttribute('aria-label', 'Карточка в Mini App');
      const top = el('div', 'offer-preview-top');
      const mark = el('span', 'offer-preview-icon', '↗');
      mark.setAttribute('aria-hidden', 'true');
      top.append(mark);
      const badgeText = form.badge.trim();
      if (badgeText) top.append(el('span', 'tag tag-accent offer-preview-badge', badgeText));
      card.append(top, el('p', 'offer-preview-name', title), el('p', 'offer-preview-days', days ? `${days} дней доступа` : 'Срок не указан'));
      const text = form.description.replace(/\r\n/g, '\n').trim();
      if (text) card.append(el('p', 'offer-preview-desc', text));
      const items = featuresFromText(form.features);
      if (items.length) {
        const list = el('ul', 'offer-preview-features');
        for (const item of items) list.append(el('li', '', item));
        card.append(list);
      }
      const row = el('div', 'offer-preview-row');
      const arrow = el('span', 'offer-preview-arrow', '→');
      arrow.setAttribute('aria-hidden', 'true');
      row.append(el('strong', 'offer-preview-price', amount !== null && amount > 0 ? miniAppPrice(amount, code) : '—'), arrow);
      card.append(row);
      if (code !== 'XTR') card.append(el('p', 'offer-preview-note', 'Оплата Stars недоступна'));

      const bot = el('div', 'bot-preview');
      bot.setAttribute('role', 'group');
      bot.setAttribute('aria-label', 'Кнопка в боте');
      bot.append(el('p', 'preview-caption', 'Кнопка в боте'));
      if (code === 'XTR') bot.append(el('p', 'field-hint', 'Не показывается: оплата Telegram Stars доступна только в Mini App.'));
      else bot.append(el('span', 'bot-button', botButtonLabel(title, amount ?? 0)));

      const nodes: HTMLElement[] = [el('p', 'preview-caption', 'Карточка в Mini App'), card, bot];
      if (!form.isActive) nodes.push(el('p', 'field-hint', 'Тариф скрыт: покупатели не увидят его, пока он не включён.'));
      previewBody.replaceChildren(...nodes);
    };

    let paintedPlan: number | null | undefined;
    const paintPlan = (force = false) => {
      if (!force && paintedPlan === form.planId) return;
      paintedPlan = form.planId;
      const selected = (this.plansCache ?? plans).find(item => item.id === form.planId) ?? null;
      if (!selected) {
        planBody.replaceChildren(el('p', 'panel-note', 'Выберите план: он задаёт лимиты подписки и построитель конфигурации.'));
        return;
      }
      const facts = el('dl', 'facts facts-single');
      const builderName = selected.builder_name
        || (selected.subscription_builder_id !== null ? `Построитель #${selected.subscription_builder_id}` : 'Не назначен');
      facts.append(
        fact('План', selected.name, selected.is_active ? undefined : 'План отключён', 'is-danger'),
        fact('Устройства', selected.devices_limit ? formatCount(selected.devices_limit) : 'Без ограничения'),
        fact('Трафик', formatTraffic(selected.traffic_limit)),
        fact('Построитель', builderName, 'Назначается плану и действует для всех его тарифов и подписок.'),
      );
      const assign = button('Изменить построитель', 'btn btn-secondary btn-sm', 'builders');
      assign.addEventListener('click', () => void this.assignPlanBuilder(selected, () => paintPlan(true)));
      const foot = el('div', 'panel-foot');
      foot.append(assign);
      planBody.replaceChildren(facts, foot);
    };

    const paintManage = () => {
      if (!base) return;
      const current = base;
      const busy = saving || frozen;
      const nodes: HTMLElement[] = [];
      const facts = el('dl', 'facts facts-single');
      facts.append(fact('Использование', current.in_use ? tariffUsage(current) : 'Ещё не покупали'),
        fact('Создан', formatDateTime(current.created_at)));
      if (current.previous_id !== null) {
        const link = el('a', 'inline-link', `Тариф #${current.previous_id}`);
        link.href = `#/tariffs/${current.previous_id}`;
        facts.append(fact('Прежняя версия', link));
      }
      nodes.push(facts);
      const list = el('div', 'tariff-manage-actions');
      if (!(retired() && !current.is_active)) {
        const toggle = current.is_active
          ? button('Скрыть из каталога', 'btn btn-secondary btn-sm')
          : button('Включить продажу', 'btn btn-primary btn-sm');
        toggle.disabled = busy;
        toggle.addEventListener('click', () => void toggleActive());
        const item = el('div', 'tariff-manage-item');
        item.append(el('p', 'manage-text', current.is_active
          ? 'Тариф в продаже. Скрытый тариф пропадёт из каталога; купленные подписки продолжат действовать.'
          : 'Тариф скрыт из каталога Mini App и бота.'), toggle);
        list.append(item);
      }
      const remove = button('Удалить тариф', 'btn btn-danger btn-sm', 'trash');
      remove.disabled = busy || current.in_use;
      remove.addEventListener('click', () => void removeTariff(remove));
      const removeItem = el('div', 'tariff-manage-item');
      removeItem.append(el('p', current.in_use ? 'manage-text is-blocked' : 'manage-text', current.in_use
        ? `Удалить нельзя: ${tariffUsage(current)}. Тариф можно только скрыть.`
        : 'Тариф ещё не покупали — его можно удалить безвозвратно.'), remove);
      list.append(removeItem);
      nodes.push(list);
      manageBody.replaceChildren(...nodes);
    };

    const applyEditable = () => {
      const locked = retired() || frozen;
      for (const control of textControls) {
        control.readOnly = locked;
        control.disabled = retired();
      }
      for (const control of choiceControls) control.disabled = locked;
      for (const chip of chips) chip.disabled = locked;
      retiredSlot.replaceChildren();
      if (base && retired() && base.replaced_by_id !== null) {
        const successor = el('a', 'btn btn-secondary btn-sm', 'Открыть актуальную версию');
        successor.href = `#/tariffs/${base.replaced_by_id}`;
        retiredSlot.append(noticeWithActions({
          tone: 'info',
          text: 'Это прежняя версия тарифа: её заменили новой при изменении условий покупки, и она больше не редактируется. Подписки, купленные по ней, продолжают действовать.',
        }, [successor]));
      }
    };

    const paintActions = () => {
      const isDirty = dirty();
      const { input } = validateTariffForm(form);
      const versioned = base !== null && base.in_use && input !== null && termsChanged(base, input);
      actionBar.hidden = retired();
      dirtyNote.textContent = frozen ? 'Результат сохранения не подтверждён'
        : isDirty ? 'Есть несохранённые изменения' : base ? 'Все изменения сохранены' : '';
      dirtyNote.classList.toggle('is-dirty', isDirty || frozen);
      reset.disabled = saving || frozen || !isDirty;
      save.disabled = saving || (base !== null && !isDirty && !frozen);
      save.setAttribute('aria-busy', String(saving));
      setLabel(save, saving ? 'Сохраняем…' : frozen ? 'Повторить сохранение'
        : !base ? 'Создать тариф' : versioned ? 'Сохранить как новую версию' : 'Сохранить');
    };

    const sync = () => {
      form = read();
      for (const paint of counters) paint();
      for (const chip of durationChips.chips) chip.setAttribute('aria-pressed', String(chip.dataset.value === form.durationDays.trim()));
      for (const chip of badgeChips.chips) chip.setAttribute('aria-pressed', String(chip.dataset.value === form.badge.trim()));
      priceHint.textContent = form.currency === 'XTR'
        ? 'Целое число звёзд Telegram Stars. Такой тариф продаётся только в Mini App.'
        : 'Например 199 или 199,90. Тариф в рублях продаётся через бота.';
      const count = featuresFromText(form.features).length;
      featuresHint.textContent = `По одному в строке, не больше ${TARIFF_LIMITS.features} пунктов по ${TARIFF_LIMITS.feature} символов. Сейчас: ${count}.`;
      paintTerms();
      paintPreview();
      paintPlan();
      paintActions();
    };

    // Server state reload after a conflict ----------------------------------
    const refetch = async (keepEdits: boolean, prefix = '') => {
      if (!base) return;
      const id = base.id;
      try {
        const latest = await this.api.getTariff(id);
        if (!isCurrent()) return;
        this.applyTariffOutcome({ tariff: latest, previous: null, versioned: false, replayed: false });
        base = latest;
        const before = baseline;
        baseline = formFromTariff(latest, null);
        // Only the fields edited here move onto the latest version; the rest
        // keeps what the other change saved.
        form = keepEdits && !isRetired(latest) ? rebaseForm(before, form, baseline) : { ...baseline };
        write(form);
        pending = null;
        frozen = false;
        clearErrors();
        applyEditable();
        paintHeading();
        sync();
        paintManage();
        if (isRetired(latest)) {
          showStatus({ tone: 'error', text: `${prefix}Эта версия тарифа уже заменена новой.` });
        } else {
          showStatus({
            tone: 'info',
            text: keepEdits
              ? `${prefix}Загружена актуальная версия тарифа. Ваши правки остались в форме — проверьте их и сохраните снова.`
              : `${prefix}Загружена актуальная версия тарифа.`,
          });
        }
      } catch (error) {
        if (!isCurrent()) return;
        if (await this.endIfSignedOut(error, isCurrent)) return;
        if (error instanceof ApiError && error.status === 404) {
          this.removeCachedTariff(id);
          this.unsavedChanges = null;
          this.paintTariffMissing(view);
          return;
        }
        showStatus({ tone: 'error', text: `Не удалось загрузить тариф. ${errorText(error)}` });
      }
    };

    const showConflict = (prefix: string) => {
      const keep = button('Перенести мои правки', 'btn btn-primary btn-sm');
      keep.addEventListener('click', () => void refetch(true));
      const reload = button('Загрузить актуальную версию', 'btn btn-secondary btn-sm', 'refresh');
      reload.addEventListener('click', () => void refetch(false));
      showStatus({
        tone: 'error',
        text: `${prefix} Тариф успели изменить в другом окне или другим администратором. Перенесите свои правки на актуальную версию или загрузите её без них.`,
      }, [keep, reload]);
    };

    /** Common rejection handling of save/toggle/delete; false for an uncertain outcome. */
    const reject = async (error: unknown, prefix: string): Promise<boolean> => {
      const code = error instanceof ApiError ? error.code : '';
      if (error instanceof ApiError && code === 'invalid_tariff') {
        const key = SERVER_FIELDS[error.field];
        if (key) showFieldErrors({ [key]: key === 'planId' ? 'План недоступен для продажи: он системный или удалён.' : 'Сервер отклонил это значение.' });
        showStatus({ tone: 'error', text: `${prefix} ${tariffErrorText(error)}` }, [], !key);
        return true;
      }
      if (code === 'version_conflict') { showConflict(prefix); return true; }
      if (code === 'tariff_superseded') { await refetch(false, `${prefix} `); return true; }
      if (error instanceof ApiError && error.status === 404) {
        showStatus({ tone: 'error', text: `${prefix} ${tariffErrorText(error)}` }, [backLink()]);
        return true;
      }
      if (code === 'tariff_in_use') { await refetch(true, `${prefix} ${tariffErrorText(error)} `); return true; }
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
        showStatus({ tone: 'error', text: `${prefix} ${tariffErrorText(error)}` });
        return true;
      }
      return false;
    };

    // Actions -----------------------------------------------------------------
    const submit = async () => {
      if (saving || retired()) return;
      form = read();
      clearErrors();
      const { input, errors } = validateTariffForm(form);
      if (!input) {
        showStatus({ tone: 'error', text: 'Проверьте выделенные поля.' }, [], false);
        showFieldErrors(errors);
        return;
      }
      if (base && !dirty() && !frozen) return;
      const target = base;
      const submitted = { ...form };
      const payload = JSON.stringify([target?.id ?? 0, target?.version ?? 0, input]);
      if (!pending || pending.payload !== payload) pending = { payload, key: newRequestKey() };
      const key = pending.key;
      const prefix = target ? 'Изменения не сохранены.' : 'Тариф не создан.';
      saving = true;
      showStatus(null);
      paintActions();
      paintManage();
      try {
        const outcome = target ? await this.api.updateTariff(target.id, target.version, input, key) : await this.api.createTariff(input, key);
        pending = null;
        frozen = false;
        saving = false;
        this.lastSessionCheck = Date.now();
        this.applyTariffOutcome(outcome);
        if (!isCurrent()) return;
        const saved = outcome.tariff;
        const replay = outcome.replayed ? ' Этот запрос уже был выполнен ранее, повторно изменение не применялось.' : '';
        if (!saved) {
          this.unsavedChanges = null;
          showStatus({ tone: 'error', text: `Тариф был сохранён ранее, но с тех пор удалён.${replay}` }, [backLink()]);
          applyEditable();
          paintActions();
          return;
        }
        if (!target || outcome.versioned) {
          // Re-open the editor on the resulting tariff; the notice survives the render.
          this.unsavedChanges = null;
          this.tariffNotice = {
            id: saved.id,
            notice: {
              tone: 'success',
              text: !target
                ? `Тариф «${saved.name}» создан${saved.is_active ? ' и показывается в каталоге' : ' и пока скрыт'}.${replay}`
                : `Условия покупки изменены: создана новая версия тарифа #${saved.id}. Прежняя версия #${target.id} снята с продажи, купленные по ней подписки не изменились.${replay}`,
            },
          };
          location.hash = `#/tariffs/${saved.id}`;
          return;
        }
        base = saved;
        baseline = formFromTariff(saved, null);
        // Keep anything typed while the request was in flight.
        form = sameForm(read(), submitted) ? { ...baseline } : read();
        write(form);
        applyEditable();
        paintHeading();
        sync();
        paintManage();
        showStatus({ tone: 'success', text: `Изменения сохранены.${replay}` });
      } catch (error) {
        saving = false;
        if (!isCurrent()) return;
        if (await this.endIfSignedOut(error, isCurrent)) return;
        if (await reject(error, prefix)) {
          pending = null;
        } else {
          frozen = true;
          showStatus({
            tone: 'error',
            text: `Не удалось подтвердить сохранение. ${errorText(error)} Повторите: повторная отправка не создаст дубликат и не применит изменения дважды.`,
          });
        }
        applyEditable();
        paintActions();
        paintManage();
      }
    };

    const toggleActive = async () => {
      if (!base || saving || frozen) return;
      const target = base;
      const next = !target.is_active;
      saving = true;
      paintActions();
      paintManage();
      try {
        const outcome = await this.api.setTariffActive(target.id, target.version, next, newRequestKey());
        this.applyTariffOutcome(outcome);
        saving = false;
        if (!isCurrent()) return;
        if (outcome.tariff) {
          base = outcome.tariff;
          baseline = formFromTariff(base, null);
          // Other edits stay in the form; only the publication flag follows the server.
          form = { ...read(), isActive: base.is_active };
          write(form);
        }
        applyEditable();
        paintHeading();
        sync();
        paintManage();
        showStatus({ tone: 'success', text: next ? 'Тариф включён и показывается в каталоге.' : 'Тариф скрыт из каталога. Купленные подписки продолжают действовать.' });
      } catch (error) {
        saving = false;
        if (!isCurrent()) return;
        if (await this.endIfSignedOut(error, isCurrent)) return;
        const prefix = next ? 'Тариф не включён.' : 'Тариф не скрыт.';
        if (!(await reject(error, prefix))) showStatus({ tone: 'error', text: `${prefix} ${errorText(error)}` });
        paintActions();
        paintManage();
      }
    };

    const removeTariff = async (trigger: HTMLButtonElement) => {
      if (!base || saving || frozen || base.in_use) return;
      const target = base;
      const confirmed = await this.confirmAction(`Удалить тариф «${target.name}»?`,
        'Тариф ещё не покупали. Он исчезнет из каталога и из панели, действие нельзя отменить.', 'Удалить');
      if (!isCurrent()) return;
      if (!confirmed) { trigger.focus(); return; }
      saving = true;
      paintActions();
      paintManage();
      try {
        await this.api.deleteTariff(target.id, target.version, newRequestKey());
        this.removeCachedTariff(target.id);
        if (!isCurrent()) return;
        this.unsavedChanges = null;
        this.toast(`Тариф «${target.name}» удалён.`);
        location.hash = '#/tariffs';
      } catch (error) {
        saving = false;
        if (!isCurrent()) return;
        if (await this.endIfSignedOut(error, isCurrent)) return;
        if (!(await reject(error, 'Тариф не удалён.'))) showStatus({ tone: 'error', text: `Тариф не удалён. ${errorText(error)}` });
        paintActions();
        paintManage();
      }
    };

    // Wiring ------------------------------------------------------------------
    for (const control of [...textControls, ...choiceControls]) {
      control.addEventListener('input', sync);
      control.addEventListener('change', sync);
    }
    // Saving reports every problem; editing a field clears its own. No blur
    // validation: a message appearing or vanishing on blur shifts the layout
    // under the pointer and the click lands elsewhere.
    for (const [key, control] of targets) {
      control.addEventListener('input', () => setters.get(key)?.(''));
    }
    reset.addEventListener('click', () => {
      form = { ...baseline };
      write(form);
      clearErrors();
      showStatus(null);
      sync();
      name.focus();
    });
    formEl.addEventListener('submit', event => {
      event.preventDefault();
      void submit();
    });

    write(form);
    applyEditable();
    paintHeading();
    sync();
    paintManage();
    const shown = this.tariffNotice;
    if (shown && base && shown.id === base.id) {
      this.tariffNotice = null;
      showStatus(shown.notice, [], false);
    }
  }

  /** Assigns the plan's builder from the tariff editor (builders are per plan, never per tariff). */
  private async assignPlanBuilder(plan: TariffPlan, onDone: () => void) {
    if (this.dialog) return;
    const view = this.tariffEditorView;
    const current = () => this.tariffEditorView === view;
    if (!this.buildersCache) {
      try {
        this.buildersCache = await this.api.listBuilders();
      } catch (error) {
        if (!current()) return;
        if (await this.endIfSignedOut(error, current)) return;
        this.toast(`Не удалось загрузить построители. ${errorText(error)}`);
        return;
      }
      if (!current()) return;
    }
    this.openBuilderAssignDialog('plan', plan.id, plan.subscription_builder_id, builderId => {
      const builderName = builderId === null ? '' : this.buildersCache?.find(item => item.id === builderId)?.name ?? '';
      this.plansCache = (this.plansCache ?? []).map(item => item.id === plan.id
        ? { ...item, subscription_builder_id: builderId, builder_name: builderName } : item);
      if (this.tariffsCache) {
        this.tariffsCache = this.tariffsCache.map(tariff => tariff.plan_id === plan.id ? { ...tariff, builder_id: builderId } : tariff);
      }
      if (current()) onDone();
      this.toast(builderId === null ? `Построитель плана «${plan.name}» снят.` : `Построитель назначен плану «${plan.name}».`);
    }, { planName: plan.name, subscriptions: plan.subscriptions });
  }

  /**
   * Subscriptions affected by a plan's builder (TariffPlan.subscriptions, all
   * statuses), from the plans snapshot or GET /admin/api/plans. A failed read
   * still opens the dialog, with a warning that the count is unknown.
   */
  private async planImpact(planId: number, planName: string): Promise<PlanImpact> {
    let plan = this.plansCache?.find(item => item.id === planId);
    if (!plan) {
      try {
        this.plansCache = await this.api.listPlans();
        plan = this.plansCache.find(item => item.id === planId);
      } catch {
        // The dialog still opens; its warning says the count is unknown.
      }
    }
    return { planName: plan?.name ?? planName, subscriptions: plan ? plan.subscriptions : null };
  }

  /** A modal yes/no confirmation; resolves false when dismissed in any way. */
  private confirmAction(titleText: string, text: string, confirmLabel: string): Promise<boolean> {
    return new Promise(resolve => {
      if (this.dialog || this.view !== 'app') { resolve(false); return; }
      const dialog = el('dialog', 'modal');
      dialog.setAttribute('aria-labelledby', 'confirm-action-title');
      const heading = el('h2', 'modal-title', titleText);
      heading.id = 'confirm-action-title';
      const cancel = button('Отмена', 'btn btn-secondary');
      const confirm = button(confirmLabel, 'btn btn-danger');
      const actions = el('div', 'modal-actions');
      actions.append(cancel, confirm);
      const body = el('div', 'modal-body');
      body.append(heading, el('p', 'modal-text', text), actions);
      dialog.append(body);
      let settled = false;
      const handle = {
        dismiss: () => {
          if (dialog.open) dialog.close();
          dialog.remove();
          if (!settled) { settled = true; resolve(false); }
        },
      };
      const finish = (value: boolean) => {
        if (settled) return;
        settled = true;
        if (this.dialog === handle) this.dialog = null;
        handle.dismiss();
        resolve(value);
      };
      cancel.addEventListener('click', () => finish(false));
      confirm.addEventListener('click', () => finish(true));
      dialog.addEventListener('cancel', event => { event.preventDefault(); finish(false); });
      this.dialog = handle;
      this.root.append(dialog);
      dialog.showModal();
      cancel.focus();
    });
  }
}

// ---------------------------------------------------------------------------
// Tariff helpers

const NEW_TARIFF_DESC = 'Заполните условия покупки и карточку. Включённый тариф сразу появится в каталоге.';
const TARIFF_FIELD_ORDER: readonly TariffField[] = ['name', 'planId', 'durationDays', 'price', 'currency', 'description', 'features', 'badge', 'sortOrder'];
const CURRENCY_LABELS: Readonly<Record<string, string>> = {
  RUB: 'RUB — рубли (бот)',
  XTR: 'XTR — Telegram Stars (Mini App)',
};

/** The Mini App's price label (frontend/src/ui.ts price). */
function miniAppPrice(amount: number, currency: string): string {
  if (currency === 'XTR') return `${amount.toLocaleString('ru-RU')} ★`;
  return `${(amount / 100).toLocaleString('ru-RU')} ${currency}`;
}

const TARIFF_REJECTIONS: Readonly<Record<string, string>> = {
  version_conflict: 'Тариф успели изменить в другом окне или другим администратором.',
  tariff_superseded: 'Эта версия тарифа уже заменена новой и больше не редактируется.',
  tariff_in_use: 'Тариф уже используется в заказах или подписках: удалить его нельзя, можно только скрыть.',
  order_stale: 'Каталог изменился после загрузки: тариф добавлен или удалён.',
  request_key_conflict: 'Запрос конфликтует с ранее отправленным.',
  invalid_request: 'Сервер отклонил параметры запроса.',
};

function tariffErrorText(error: unknown): string {
  if (!(error instanceof ApiError)) return errorText(error);
  if (error.code === 'invalid_tariff') {
    return error.field === 'plan_id' ? 'План недоступен для продажи: он системный или удалён.' : 'Сервер отклонил значение одного из полей.';
  }
  if (error.code === 'not_found' && error.status === 404) return 'Тариф не найден: возможно, его уже удалили.';
  return TARIFF_REJECTIONS[error.code] ?? errorText(error);
}

const isStaleTariffError = (error: unknown) =>
  error instanceof ApiError && ['version_conflict', 'tariff_superseded', 'tariff_in_use', 'not_found'].includes(error.code);

function tariffUsage(tariff: AdminTariff): string {
  if (!tariff.in_use) return 'не покупали';
  return `${formatCount(tariff.orders)} ${pluralRu(tariff.orders, 'заказ', 'заказа', 'заказов')}, ` +
    `${formatCount(tariff.subscriptions)} ${pluralRu(tariff.subscriptions, 'подписка', 'подписки', 'подписок')}`;
}

function tariffStateTag(tariff: AdminTariff): HTMLElement {
  if (isRetired(tariff)) return el('span', 'tag', 'Прежняя версия');
  return tariff.is_active ? el('span', 'tag tag-on', 'В продаже') : el('span', 'tag', 'Скрыт');
}

function iconButton(name: IconName, label: string): HTMLButtonElement {
  const node = el('button', 'icon-button');
  node.type = 'button';
  node.setAttribute('aria-label', label);
  node.title = label;
  node.append(icon(name));
  return node;
}

/** Subscriptions of a plan reached by a change of its builder; null when unknown. */
interface PlanImpact { planName: string; subscriptions: number | null }

/** The explicit warning of the plan builder dialog. */
function planImpactText({ planName, subscriptions }: PlanImpact): string {
  if (subscriptions === null) return `Изменение затронет все подписки плана «${planName}». Число подписок получить не удалось.`;
  if (subscriptions === 0) return `У плана «${planName}» нет подписок: изменение не затронет ни одной активной подписки.`;
  return `Изменение затронет ${formatCount(subscriptions)} ${pluralRu(subscriptions, 'подписку', 'подписки', 'подписок')} плана «${planName}». ` +
    'Учитываются подписки во всех статусах.';
}

/** The latest server form with the fields edited locally (mine vs before) applied on top. */
function rebaseForm(before: TariffForm, mine: TariffForm, latest: TariffForm): TariffForm {
  const next: TariffForm = { ...latest };
  const target = next as unknown as Record<string, unknown>;
  const edited = mine as unknown as Record<string, unknown>;
  const original = before as unknown as Record<string, unknown>;
  for (const key of Object.keys(edited)) {
    if (edited[key] !== original[key]) target[key] = edited[key];
  }
  return next;
}

/** A notice with action buttons under its text. */
function noticeWithActions(next: Notice, actions: HTMLElement[]): HTMLElement {
  const node = notice(next);
  if (!actions.length) return node;
  const text = node.querySelector('p');
  const content = el('div', 'notice-body');
  if (text) content.append(text);
  const row = el('div', 'notice-actions');
  row.append(...actions);
  content.append(row);
  node.append(content);
  return node;
}

function field(label: string, input: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement, action?: HTMLElement) {
  const root = el('div', 'field');
  const caption = el('label', 'field-label', label);
  caption.htmlFor = input.id;
  const control = el('div', action ? 'control has-action' : 'control');
  control.append(input);
  if (action) control.append(action);
  const error = el('p', 'field-error');
  error.id = `${input.id}-error`;
  root.append(caption, control, error);
  const setError = (text: string) => {
    error.textContent = text;
    if (text) {
      input.setAttribute('aria-invalid', 'true');
      input.setAttribute('aria-describedby', error.id);
    } else {
      input.removeAttribute('aria-invalid');
      input.removeAttribute('aria-describedby');
    }
  };
  return { root, setError };
}

const root = document.getElementById('app');
if (root) void new AdminApp(root, new AdminApi()).start();
