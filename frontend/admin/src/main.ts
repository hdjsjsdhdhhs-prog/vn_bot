import './style.css';
import {
  AdminApi, ApiError, errorText, limits, newRequestKey, RENEW_MAX_DAYS,
  type AdminNode, type AdminPlan, type AdminSubscription, type Dashboard, type Mutation, type MutationAction,
  type MutationOutcome, type SubscriptionStatus, type UserDetail, type UsersPage, type UsersQuery,
  type AdminSource, type AdminBuilder, type BuilderItem,
  type CreateSourceInput, type UpdateSourceInput,
  type CreateBuilderInput, type UpdateBuilderInput, type UpsertBuilderItemInput, type SetBuilderInput,
} from './api';

// ---------------------------------------------------------------------------
// Icons. Geometry from Lucide (ISC License, https://lucide.dev), vendored so a
// handful of glyphs does not add a runtime dependency. One family, one stroke.

type IconName = 'overview' | 'users' | 'audit' | 'sources' | 'builders' | 'logout' | 'menu' | 'close' | 'alert' | 'info' | 'eye' | 'eyeOff' | 'refresh'
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
// Sections. Overview reads /admin/api/dashboard, Users reads /admin/api/users;
// Audit is still a page frame with a placeholder until its stage lands.

type SectionId = 'overview' | 'users' | 'audit' | 'sources' | 'builders';
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
    id: 'sources', label: 'Источники',
    description: 'Внешние подписки-источники VPN-конфигураций для построителей.',
  },
  {
    id: 'builders', label: 'Построители',
    description: 'Конфигурации подписок: правила фильтрации, порядок серверов, назначение планам.',
  },
  {
    id: 'audit', label: 'Журнал',
    description: 'История изменений, выполненных из панели.',
    placeholder: {
      title: 'Журнал появится на следующем этапе',
      text: 'Здесь будут записи о продлениях, отключениях и изменениях сроков подписок.',
    },
  },
];

// Routes: #/overview, #/users, #/users/{telegram_id}, #/audit. Only canonical
// positive Telegram IDs open a user, matching the API route.
interface Route { section: Section; userId: number | null }
const USER_ROUTE = /^users\/([1-9][0-9]{0,15})$/;

function currentRoute(): Route {
  const path = location.hash.replace(/^#\/?/, '');
  const match = USER_ROUTE.exec(path);
  const users = SECTIONS.find(section => section.id === 'users');
  if (match && users) {
    const id = Number(match[1]);
    if (Number.isSafeInteger(id)) return { section: users, userId: id };
  }
  return { section: SECTIONS.find(section => section.id === path) ?? SECTIONS[0], userId: null };
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
  // The open confirmation dialog, if any, and the outcome of the last
  // management action shown on the subscription page it belongs to.
  private dialog: { dismiss: () => void } | null = null;
  private manageNotice: { subscriptionId: number; notice: Notice } | null = null;
  // Sources section state
  private sourcesCache: readonly AdminSource[] | null = null;
  private sourcesRequest = 0;
  private sourcesView: { body: HTMLElement; refresh: HTMLButtonElement } | null = null;
  // Builders section state
  private buildersCache: readonly AdminBuilder[] | null = null;
  private buildersRequest = 0;
  private buildersView: { body: HTMLElement; refresh: HTMLButtonElement } | null = null;
  // Builder editor state (single builder open)
  private builderEditorRequest = 0;

  constructor(private readonly root: HTMLElement, private readonly api: AdminApi) {
    this.toasts.setAttribute('aria-live', 'polite');
    window.addEventListener('hashchange', () => this.onRoute());
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
    this.overview = null;
    this.usersView = null;
    this.detailView = null;
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
    this.detailCache = null;
    this.manageNotice = null;

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

  private onRoute() {
    if (this.view !== 'app') return;
    this.setMenu(false);
    this.renderSection(true);
  }

  private renderSection(focus: boolean) {
    if (!this.shell) return;
    const { section, userId } = currentRoute();
    const canonical = userId === null ? `#/${section.id}` : `#/users/${userId}`;
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
    this.sourcesView = null;
    this.buildersView = null;
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
    } else if (section.id === 'sources') {
      page.append(header, this.buildSourcesSection(header));
      load = () => this.loadSources();
    } else if (section.id === 'builders') {
      page.append(header, this.buildBuildersSection(header));
      load = () => this.loadBuilders();
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
        this.openBuilderAssignDialog('plan', plan.id, planBuilderId, (newId) => {
          void this.loadUser();
          this.toast(newId === null ? 'Построитель плана снят.' : 'Построитель плана назначен.');
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
    body.append(titleEl, selField, statusEl, actionsRow);
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
    const at = new Date();
    updated.textContent = `Обновлено в ${clockSeconds(at)}`;
    if (sources.length === 0) {
      body.replaceChildren(messagePanel('sources', 'Источников пока нет',
        'Добавьте внешнюю подписку-источник VPN-конфигураций, чтобы построители могли её использовать.', 'status'));
      return;
    }
    const panel = el('section', 'panel');
    panel.setAttribute('aria-labelledby', 'sources-list-title');
    panel.append(panelHead('sources-list-title', 'Источники', formatCount(sources.length)));
    const list = el('ul', 'source-list');
    for (const src of sources) list.append(this.sourceRow(src));
    panel.append(list);
    body.replaceChildren(panel);
  }

  private sourceRow(src: AdminSource): HTMLElement {
    const item = el('li', 'source-item');
    const info = el('div', 'source-info');
    const nameRow = el('div', 'source-name-row');
    const name = el('span', 'source-name', src.name);
    const badge = el('span', src.enabled ? 'badge badge-active' : 'badge badge-disabled',
      src.enabled ? 'Активен' : 'Отключён');
    nameRow.append(name, badge);
    const meta = el('p', 'source-meta');
    meta.textContent = [
      src.type,
      src.last_sync_at ? `Синхр. ${formatDateTime(src.last_sync_at)}` : 'Не синхронизирован',
      src.last_sync_status && src.last_sync_status !== 'ok' ? `⚠ ${src.last_sync_status}` : '',
    ].filter(Boolean).join(' · ');
    info.append(nameRow, meta);
    if (src.description) info.append(el('p', 'source-desc', src.description));

    const acts = el('div', 'source-actions');
    const editBtn = button('Изменить', 'btn btn-secondary btn-xs', 'edit');
    editBtn.addEventListener('click', () => this.openSourceEditor(src));
    const toggleBtn = button(src.enabled ? 'Отключить' : 'Включить', 'btn btn-secondary btn-xs');
    toggleBtn.addEventListener('click', () => void this.toggleSource(src, toggleBtn));
    acts.append(editBtn, toggleBtn);
    item.append(info, acts);
    return item;
  }

  private async toggleSource(src: AdminSource, btn: HTMLButtonElement) {
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    try {
      const updated = src.enabled ? await this.api.disableSource(src.id) : await this.api.enableSource(src.id);
      if (this.sourcesCache) {
        this.sourcesCache = this.sourcesCache.map(s => s.id === updated.id ? updated : s);
        const view = this.sourcesView;
        if (view) {
          const dummy = el('p', 'updated');
          this.paintSources(view.body, this.sourcesCache, dummy);
        }
      }
    } catch (error) {
      if (await this.endIfSignedOut(error, () => this.sourcesView !== null)) return;
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
      Object.assign(inp, { id, type, value, spellcheck: false });
      const f = field(labelText, inp);
      if (hint) f.root.append(el('p', 'field-hint', hint));
      return { inp, ...f };
    };

    const nameF = mkInput('src-name', 'Название', src?.name ?? '');
    const descF = mkInput('src-desc', 'Описание', src?.description ?? '');
    const typeF = mkInput('src-type', 'Тип', src?.type ?? 'xui', 'text', 'Например: xui, proxman');
    const urlF = mkInput('src-url', 'URL подписки', '', 'url', 'Полный URL внешней подписки');
    const hwidF = mkInput('src-hwid', 'HWID', '', 'text', 'Идентификатор устройства (если требуется)');
    const uaF = mkInput('src-ua', 'User-Agent', '', 'text');
    const headersF = mkInput('src-headers', 'Заголовки', '', 'text', 'Дополнительные HTTP-заголовки');

    const enabledChk = el('input', 'checkbox');
    enabledChk.type = 'checkbox';
    enabledChk.id = 'src-enabled';
    enabledChk.checked = src?.enabled ?? true;
    const enabledLabel = el('label', 'checkbox-label', 'Активен');
    enabledLabel.htmlFor = 'src-enabled';
    const enabledRow = el('div', 'checkbox-row');
    enabledRow.append(enabledChk, enabledLabel);

    const isCreate = isNew;
    if (!isCreate) {
      urlF.root.hidden = true;
      hwidF.root.hidden = true;
      uaF.root.hidden = true;
      headersF.root.hidden = true;
    }

    const statusEl = el('div', 'modal-status');
    statusEl.setAttribute('role', 'alert');
    const showStatus = (n: Notice | null) => statusEl.replaceChildren(...(n ? [notice(n)] : []));

    const cancel = button('Отмена', 'btn btn-secondary');
    const save = button(isNew ? 'Создать' : 'Сохранить', 'btn btn-primary');
    const acts = el('div', 'modal-actions');
    acts.append(cancel, save);

    const body = el('div', 'modal-body');
    body.append(title, nameF.root, descF.root, typeF.root);
    if (isCreate) body.append(urlF.root, hwidF.root, uaF.root, headersF.root);
    body.append(enabledRow, statusEl, acts);
    dialog.append(body);

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
      nameF.setError(''); descF.setError('');
      const name = nameF.inp.value.trim();
      if (!name) { nameF.setError('Введите название.'); nameF.inp.focus(); return; }
      submitting = true;
      save.disabled = true; save.setAttribute('aria-busy', 'true');
      setLabel(save, 'Сохраняем…');
      showStatus(null);
      try {
        let updated: AdminSource;
        if (isNew) {
          const input: CreateSourceInput = {
            name, description: descF.inp.value.trim(), type: typeF.inp.value.trim() || 'xui',
            subscription_url: urlF.inp.value.trim(), hwid: hwidF.inp.value.trim(),
            user_agent: uaF.inp.value.trim(), headers: headersF.inp.value.trim(),
            enabled: enabledChk.checked,
          };
          updated = await this.api.createSource(input);
        } else {
          const input: UpdateSourceInput = {
            name, description: descF.inp.value.trim(), type: typeF.inp.value.trim() || src!.type,
          };
          updated = await this.api.updateSource(src!.id, input);
        }
        if (!isOpen()) return;
        if (isNew) {
          this.sourcesCache = this.sourcesCache ? [...this.sourcesCache, updated] : [updated];
        } else {
          this.sourcesCache = this.sourcesCache?.map(s => s.id === updated.id ? updated : s) ?? null;
        }
        close();
        void this.loadSources();
        this.toast(isNew ? 'Источник создан.' : 'Источник обновлён.');
      } catch (error) {
        if (!isOpen()) return;
        submitting = false;
        save.disabled = false; save.removeAttribute('aria-busy');
        setLabel(save, isNew ? 'Создать' : 'Сохранить');
        if (await this.endIfSignedOut(error, isOpen)) return;
        showStatus({ tone: 'error', text: `Не удалось сохранить. ${errorText(error)}` });
      }
    });

    this.dialog = handle;
    this.root.append(dialog);
    dialog.showModal();
    nameF.inp.focus();
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

    // ---- Dialog shell ----
    const dialog = el('dialog', 'modal modal-editor');
    dialog.setAttribute('aria-labelledby', 'be-title');

    const titleEl = el('h2', 'modal-title', isNew ? 'Новый построитель' : `Построитель: ${b!.name}`);
    titleEl.id = 'be-title';

    // ---- Unsaved indicator ----
    const unsavedBadge = el('span', 'badge badge-warn unsaved-badge');
    unsavedBadge.textContent = 'Несохранённые изменения';
    unsavedBadge.hidden = true;
    const markUnsaved = () => { unsaved = true; unsavedBadge.hidden = false; };

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
        const bdg = el('span', src.enabled ? 'badge badge-active' : 'badge badge-disabled',
          src.enabled ? 'Активен' : 'Отключён');
        const removeBtn = button('Убрать', 'btn btn-secondary btn-xs');
        removeBtn.addEventListener('click', () => {
          selectedSourceIds = selectedSourceIds.filter(id => id !== src.id);
          markUnsaved();
          renderSourcesPicker();
        });
        li.append(dragHandle, lbl, bdg, removeBtn);
        sourcesListEl.append(li);
      }
      for (const src of unselected) {
        const li = el('li', 'source-pick-item');
        li.dataset.srcId = String(src.id);
        const lbl = el('span', 'source-pick-name', src.name);
        const bdg = el('span', src.enabled ? 'badge badge-active' : 'badge badge-disabled',
          src.enabled ? 'Активен' : 'Отключён');
        const addBtn = button('Добавить', 'btn btn-primary btn-xs');
        addBtn.addEventListener('click', () => {
          selectedSourceIds = [...selectedSourceIds, src.id];
          markUnsaved();
          renderSourcesPicker();
        });
        li.append(lbl, bdg, addBtn);
        sourcesListEl.append(li);
      }
      if (allSources.length === 0) {
        sourcesListEl.append(el('li', 'source-pick-empty', 'Источники не загружены. Обновите страницу.'));
      }
      setupDragReorder(sourcesListEl, 'source-pick-selected', (newOrder) => {
        selectedSourceIds = newOrder.map(Number);
        markUnsaved();
      });
    };
    renderSourcesPicker();
    sourcesPanel.append(sourcesListEl);

    // ---- Items (rules) panel ----
    const itemsPanel = el('section', 'editor-panel');
    itemsPanel.setAttribute('aria-labelledby', 'be-items-title');
    const itemsPanelHead = panelHead('be-items-title', 'Правила');
    itemsPanelHead.append(el('p', 'panel-note', 'Правила определяют, какие серверы и страны включаются в подписку и в каком порядке.'));
    itemsPanel.append(itemsPanelHead);

    const itemsListEl = el('ul', 'items-list');
    const addItemBtn = button('Добавить правило', 'btn btn-secondary btn-sm', 'plus');

    const renderItems = () => {
      itemsListEl.replaceChildren();
      if (localItems.length === 0) {
        itemsListEl.append(el('li', 'items-empty', 'Правил пока нет. Добавьте страну или конкретный узел.'));
      }
      for (let i = 0; i < localItems.length; i++) {
        const item = localItems[i];
        itemsListEl.append(this.buildItemRow(item, i, localItems, (updated) => {
          localItems = updated;
          markUnsaved();
          renderItems();
        }, currentBuilder, isNew));
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
      this.openItemEditor(null, currentBuilder, selectedSourceIds, (newItem) => {
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
    previewPanel.append(previewBody, previewBtn);

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
        if (isNew) {
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
        if (selectedSourceIds.length > 0 || !isNew) {
          await this.api.setBuilderSources(saved.id, selectedSourceIds);
        }

        // Save items (upsert all, then reorder)
        if (localItems.length > 0) {
          const upserted: BuilderItem[] = [];
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

        if (!isOpen()) return;
        currentBuilder = saved;
        unsaved = false;
        unsavedBadge.hidden = true;
        titleEl.textContent = `Построитель: ${saved.name}`;
        previewBtn.disabled = false;
        previewBtn.title = '';

        // Update cache
        if (isNew) {
          this.buildersCache = this.buildersCache ? [...this.buildersCache, saved] : [saved];
        } else {
          this.buildersCache = this.buildersCache?.map(x => x.id === saved.id ? saved : x) ?? null;
        }
        void this.loadBuilders();
        this.toast(isNew ? 'Построитель создан.' : 'Построитель сохранён.');
        submitting = false;
        saveBtn.disabled = false; saveBtn.removeAttribute('aria-busy');
        setLabel(saveBtn, 'Сохранить');
        showStatus({ tone: 'success', text: 'Изменения сохранены.' });
      } catch (error) {
        if (!isOpen()) return;
        submitting = false;
        saveBtn.disabled = false; saveBtn.removeAttribute('aria-busy');
        setLabel(saveBtn, isNew ? 'Создать' : 'Сохранить');
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
  private buildItemRow(
    item: BuilderItem,
    index: number,
    allItems: BuilderItem[],
    onChange: (updated: BuilderItem[]) => void,
    _builder: AdminBuilder | null,
    _isNew: boolean,
  ): HTMLElement {
    const li = el('li', 'item-row');
    li.draggable = true;
    li.dataset.dragIdx = String(index);

    const dragHandle = el('span', 'drag-handle');
    dragHandle.append(icon('drag'));
    dragHandle.setAttribute('aria-hidden', 'true');

    const kindBadge = el('span', item.kind === 'country' ? 'badge badge-country' : 'badge badge-node',
      item.kind === 'country' ? '🌍 Страна' : '🖥 Узел');

    const nameEl = el('span', 'item-name');
    if (item.kind === 'country') {
      nameEl.textContent = item.custom_name || item.country_code || '—';
    } else {
      nameEl.textContent = item.custom_name || item.original_name || item.fingerprint || '—';
    }

    const enabledBadge = el('span', item.enabled ? 'badge badge-active' : 'badge badge-disabled',
      item.enabled ? 'Вкл' : 'Выкл');

    const editBtn = button('Изменить', 'btn btn-secondary btn-xs', 'edit');
    editBtn.addEventListener('click', () => {
      this.openItemEditor(item, _builder, [], (updated) => {
        onChange(allItems.map((it, i) => i === index ? { ...updated, position: it.position } : it));
      });
    });

    const deleteBtn = button('Удалить', 'btn btn-secondary btn-xs', 'trash');
    deleteBtn.addEventListener('click', () => {
      if (!window.confirm('Удалить правило?')) return;
      // If item has a real id, delete from server on next save (handled by caller)
      onChange(allItems.filter((_, i) => i !== index).map((it, pos) => ({ ...it, position: pos })));
    });

    const acts = el('div', 'item-actions');
    acts.append(editBtn, deleteBtn);

    li.append(dragHandle, kindBadge, nameEl, enabledBadge, acts);
    return li;
  }

  // ---- Item editor sub-dialog ----
  private openItemEditor(
    item: BuilderItem | null,
    _builder: AdminBuilder | null,
    _sourceIds: number[],
    onSave: (item: BuilderItem) => void,
  ) {
    const isNew = item === null;
    const dialog = el('dialog', 'modal modal-wide');
    dialog.setAttribute('aria-labelledby', 'ie-title');

    const titleEl = el('h2', 'modal-title', isNew ? 'Новое правило' : 'Изменить правило');
    titleEl.id = 'ie-title';

    // Kind selector
    const kindSel = el('select', 'input');
    kindSel.id = 'ie-kind';
    const optCountry = el('option', '', 'Страна');
    optCountry.value = 'country';
    const optNode = el('option', '', 'Узел');
    optNode.value = 'node';
    kindSel.append(optCountry, optNode);
    kindSel.value = item?.kind ?? 'country';
    const kindField = el('div', 'field');
    const kindLabel = el('label', 'field-label', 'Тип правила');
    kindLabel.htmlFor = 'ie-kind';
    kindField.append(kindLabel, el('div', 'control', ''));
    kindField.querySelector('.control')!.append(kindSel);

    const mkInp = (id: string, labelText: string, value = '', hint?: string) => {
      const inp = el('input', 'input');
      Object.assign(inp, { id, type: 'text', value, spellcheck: false });
      const f = field(labelText, inp);
      if (hint) f.root.append(el('p', 'field-hint', hint));
      return { inp, ...f };
    };

    const countryF = mkInp('ie-country', 'Код страны (ISO 3166-1 alpha-2)', item?.country_code ?? '', 'Например: RU, DE, US');
    const fingerprintF = mkInp('ie-fingerprint', 'Fingerprint узла', item?.fingerprint ?? '');
    const origNameF = mkInp('ie-orig-name', 'Оригинальное имя', item?.original_name ?? '');
    const customNameF = mkInp('ie-custom-name', 'Пользовательское имя', item?.custom_name ?? '', 'Если задано, заменяет оригинальное имя в подписке.');
    const descF = mkInp('ie-desc', 'Описание', item?.description ?? '');
    const sourceIdF = mkInp('ie-source-id', 'ID источника', String(item?.source_id ?? 0), 'Числовой ID источника (0 = любой)');

    const enabledChk = el('input', 'checkbox');
    enabledChk.type = 'checkbox';
    enabledChk.id = 'ie-enabled';
    enabledChk.checked = item?.enabled ?? true;
    const enabledLabel = el('label', 'checkbox-label', 'Включено');
    enabledLabel.htmlFor = 'ie-enabled';
    const enabledRow = el('div', 'checkbox-row');
    enabledRow.append(enabledChk, enabledLabel);

    const updateVisibility = () => {
      const isCountry = kindSel.value === 'country';
      countryF.root.hidden = !isCountry;
      fingerprintF.root.hidden = isCountry;
      origNameF.root.hidden = isCountry;
    };
    kindSel.addEventListener('change', updateVisibility);
    updateVisibility();

    const statusEl = el('div', 'modal-status');
    statusEl.setAttribute('role', 'alert');

    const cancelBtn = button('Отмена', 'btn btn-secondary');
    const saveBtn = button('Применить', 'btn btn-primary');
    const acts = el('div', 'modal-actions');
    acts.append(cancelBtn, saveBtn);

    const body = el('div', 'modal-body');
    body.append(titleEl, kindField, countryF.root, fingerprintF.root, origNameF.root,
      customNameF.root, descF.root, sourceIdF.root, enabledRow, statusEl, acts);
    dialog.append(body);

    const close = () => { if (dialog.open) dialog.close(); dialog.remove(); };
    cancelBtn.addEventListener('click', close);
    dialog.addEventListener('cancel', e => { e.preventDefault(); close(); });

    saveBtn.addEventListener('click', () => {
      const kind = kindSel.value as 'country' | 'node';
      const sourceId = parseInt(sourceIdF.inp.value.trim(), 10);
      const result: BuilderItem = {
        id: item?.id ?? 0,
        kind,
        source_id: Number.isFinite(sourceId) ? sourceId : 0,
        country_code: kind === 'country' ? countryF.inp.value.trim().toUpperCase() : '',
        fingerprint: kind === 'node' ? fingerprintF.inp.value.trim() : '',
        original_name: kind === 'node' ? origNameF.inp.value.trim() : '',
        custom_name: customNameF.inp.value.trim() || null,
        description: descF.inp.value.trim(),
        position: item?.position ?? 0,
        enabled: enabledChk.checked,
      };
      onSave(result);
      close();
    });

    document.body.append(dialog);
    dialog.showModal();
    (kindSel.value === 'country' ? countryF.inp : fingerprintF.inp).focus();
  }

  // ---- Preview ----
  private async runPreview(builder: AdminBuilder | null, body: HTMLElement, btn: HTMLButtonElement) {
    if (!builder) return;
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    body.replaceChildren(el('p', 'preview-loading', 'Загружаем предпросмотр…'));
    try {
      const preview = await this.api.previewBuilder(builder.id);
      body.replaceChildren();

      if (preview.warnings && preview.warnings.length > 0) {
        const warnBox = el('div', 'preview-warnings');
        for (const w of preview.warnings) warnBox.append(el('p', 'preview-warn', `⚠ ${w}`));
        body.append(warnBox);
      }

      const stats = el('p', 'preview-stats');
      stats.textContent = [
        `Всего: ${preview.total}`,
        preview.missing > 0 ? `Не найдено: ${preview.missing}` : '',
        preview.conflicts > 0 ? `Конфликтов: ${preview.conflicts}` : '',
      ].filter(Boolean).join(' · ');
      body.append(stats);

      if (preview.items.length === 0) {
        body.append(el('p', 'preview-empty', 'Предпросмотр пуст — нет подходящих узлов.'));
      } else {
        const list = el('ul', 'preview-list');
        for (const pi of preview.items) {
          const li = el('li', `preview-item preview-${pi.status}`);
          const pos = el('span', 'preview-pos', String(pi.position + 1));
          const name = el('span', 'preview-name', pi.display_name || pi.entry?.original_name || '—');
          const statusBadge = el('span', `badge preview-status-badge preview-status-${pi.status}`, pi.status);
          const country = el('span', 'preview-country', pi.entry?.country_code ?? '');
          li.append(pos, name, country, statusBadge);
          list.append(li);
        }
        body.append(list);
      }
    } catch (error) {
      body.replaceChildren(el('p', 'preview-error', `Ошибка предпросмотра: ${errorText(error)}`));
    } finally {
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
    }
  }
}

function field(label: string, input: HTMLInputElement, action?: HTMLElement) {
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
