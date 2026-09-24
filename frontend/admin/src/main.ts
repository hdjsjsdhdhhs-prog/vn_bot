import './style.css';
import { AdminApi, ApiError, errorText, limits, type Dashboard } from './api';

// ---------------------------------------------------------------------------
// Icons. Geometry from Lucide (ISC License, https://lucide.dev), vendored so a
// handful of glyphs does not add a runtime dependency. One family, one stroke.

type IconName = 'overview' | 'users' | 'audit' | 'logout' | 'menu' | 'close' | 'alert' | 'info' | 'eye' | 'eyeOff' | 'refresh';
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
// Sections. Overview reads /admin/api/dashboard; the other screens are still
// page frames with a placeholder until their stages land.

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
    description: 'Поиск клиентов и управление их подписками.',
    placeholder: {
      title: 'Список пользователей появится на следующем этапе',
      text: 'Здесь будет поиск по Telegram ID и имени, карточка клиента и действия с подпиской.',
    },
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

function currentSection(): Section {
  const id = location.hash.replace(/^#\/?/, '');
  return SECTIONS.find(section => section.id === id) ?? SECTIONS[0];
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
    const section = currentSection();
    const canonical = `#/${section.id}`;
    if (location.hash !== canonical) history.replaceState(null, '', canonical);
    for (const [id, link] of this.shell.links) {
      if (id === section.id) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    }
    document.title = `${section.label} · RS8 Admin`;

    const header = el('header', 'page-header');
    const heading = el('div', 'page-heading');
    const title = el('h1', 'page-title', section.label);
    title.tabIndex = -1;
    heading.append(title, el('p', 'page-desc', section.description));
    header.append(heading);
    const page = el('div', 'page');
    page.append(header);

    // Leaving Overview invalidates any dashboard request still in flight.
    this.overview = null;
    this.dashboardRequest++;
    if (section.placeholder) {
      const empty = el('section', 'panel empty');
      empty.setAttribute('aria-labelledby', 'empty-title');
      const emptyTitle = el('h2', 'empty-title', section.placeholder.title);
      emptyTitle.id = 'empty-title';
      empty.append(icon(section.id, 20), emptyTitle, el('p', 'empty-text', section.placeholder.text));
      page.append(empty);
    } else {
      page.append(this.buildOverview(header));
    }
    this.shell.content.replaceChildren(page);
    if (focus) title.focus();
    if (!section.placeholder) void this.loadDashboard();
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
