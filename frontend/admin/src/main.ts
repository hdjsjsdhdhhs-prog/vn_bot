import './style.css';
import {
  AdminApi, ApiError, errorText, limits,
  type AdminNode, type AdminPlan, type AdminSubscription, type Dashboard, type SubscriptionStatus,
  type UserDetail, type UsersPage, type UsersQuery,
} from './api';

// ---------------------------------------------------------------------------
// Icons. Geometry from Lucide (ISC License, https://lucide.dev), vendored so a
// handful of glyphs does not add a runtime dependency. One family, one stroke.

type IconName = 'overview' | 'users' | 'audit' | 'logout' | 'menu' | 'close' | 'alert' | 'info' | 'eye' | 'eyeOff' | 'refresh'
  | 'search' | 'back' | 'prev' | 'next' | 'chevron';
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

type Tone = 'error' | 'info';
interface Notice { tone: Tone; text: string }

function notice({ tone, text }: Notice): HTMLElement {
  const node = el('div', `notice notice-${tone}`);
  node.append(icon(tone === 'error' ? 'alert' : 'info'), el('p', '', text));
  return node;
}

const byteLength = (value: string) => new TextEncoder().encode(value).length;
const clock = (date: Date) => date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });

// ---------------------------------------------------------------------------
// Sections. Overview reads /admin/api/dashboard, Users reads /admin/api/users;
// Audit is still a page frame with a placeholder until its stage lands.

type SectionId = 'overview' | 'users' | 'audit';
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

    // Leaving a screen invalidates every request still in flight for it.
    this.overview = null;
    this.usersView = null;
    this.detailView = null;
    this.dashboardRequest++;
    this.usersRequest++;
    this.detailRequest++;
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
    const now = Date.now();
    const grid = el('div', 'detail-grid');
    grid.append(subscriptionPanel(user, data.plan, now), planPanel(user, data.plan));
    view.body.replaceChildren(grid, nodesPanel(user, data.nodes));
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

  /** Same policy as the list: stale responses are dropped, data on screen survives a failed refresh. */
  private async loadUser() {
    const view = this.detailView;
    if (!view) return;
    const request = ++this.detailRequest;
    const current = () => this.detailView === view && request === this.detailRequest;
    const shown = this.detailCache !== null && this.detailCache.id === view.id;
    setRefreshBusy(view.refresh, true);
    if (!shown) this.paintDetailSkeleton(view);
    try {
      const data = await this.api.user(view.id);
      if (!current()) return;
      this.lastSessionCheck = Date.now();
      const at = new Date();
      this.detailCache = { id: view.id, data, at };
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
