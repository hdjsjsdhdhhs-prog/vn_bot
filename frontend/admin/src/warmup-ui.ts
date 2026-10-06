// Customer warm-up screens (Admin → «Прогрев клиентов»): the scenario overview
// grouped by category, the scenario editor (also used to create a scenario),
// the global settings, the delivery history and the coupons.
//
// The screens are plain DOM over WarmupApi (admin/src/warmup.ts). Every rule
// stays on the server: triggers, timings, variables, callback actions and
// condition keys come from the registry in the overview; previews are rendered
// by the server with the worker's renderer; event timing is never computed
// here. Text is always assigned as text; the only markup shown is the
// server-rendered Telegram HTML, rebuilt tag by tag (telegramFragment).
//
// The host (AdminApp in main.ts) owns routing, the session, the confirmation
// dialog and the unsaved-changes guard; a screen is created per route and
// disposed when the route changes, which drops every response still in flight.

import { ApiError } from './api';
import {
  BUTTON_KIND_LABELS, CATEGORY_HINTS, CATEGORY_LABELS, COUPON_STATUS_LABELS, DELIVERY_STATUS_LABELS, EMPTY_COUPON_QUERY,
  EMPTY_DELIVERY_QUERY, HIDDEN_BUTTON_TEXT, KEY_TAKEN_TEXT, PREVIEW_CONTEXT, REMINDER_CONFLICT_TEXT, RequestKeys,
  SCENARIO_CONFLICT_TEXT, SCENARIO_KEY_PATTERN, SERVER_FIELDS, SETTINGS_CONFLICT_TEXT, TAB_LABELS, TIMING_LABELS,
  TRIAL_URL_SCENARIO, UNIT_LABELS, WARMUP_BUTTON_KINDS, WARMUP_CATEGORIES, WARMUP_COUPON_STATUSES, WARMUP_DELAY_UNITS,
  WARMUP_DELIVERY_STATUSES, WARMUP_TABS, buttonError, buttonRows, changedPatch, cloneForm, couponExample, dayStartIso,
  delayText, durationWords, fileToBase64, flagChoice, formDirty, formFromScenario, formValues, formatBytes, fullPatch,
  insertLink, insertText, isFlagCondition, isHiddenButton, moveButton, newScenarioForm, normalizeRows, parseLanguages,
  placeholders, plainText, problemText, reasonText, sameSettingsForm, scenarioInput, setSameRow, settingsForm,
  settingsSave, suggestKey, telegramFragment, triggerEmoji, triggerText, variableState, visibleLength, warmupErrorText,
  warmupHash, warmupImageUrl, withFlag, withLanguages, wrapSelection,
  type FlagChoice, type ScenarioField, type ScenarioForm, type SettingsField, type SettingsForm,
  type WarmupApi, type WarmupButton, type WarmupCouponPage, type WarmupCouponQuery, type WarmupDelivery,
  type WarmupDeliveryPage, type WarmupDeliveryQuery, type WarmupOverview, type WarmupPreview, type WarmupRegistry,
  type WarmupRoute, type WarmupScenario, type WarmupSettings, type WarmupTab,
} from './warmup';

// =============================================================================
// Host contract
// =============================================================================

/** Icons the screens use (a subset of the admin icon set). */
export type WarmupIcon = 'plus' | 'refresh' | 'back' | 'edit' | 'trash' | 'up' | 'chevron' | 'alert' | 'info' | 'check' | 'prev' | 'next' | 'eye';

export interface WarmupHost {
  readonly api: WarmupApi;
  icon(name: WarmupIcon): Element;
  toast(text: string): void;
  /** A modal yes/no question; false when dismissed. */
  confirm(title: string, text: string, confirmLabel: string): Promise<boolean>;
  /** Reports unsaved changes of the open screen to the navigation guard (null: none). */
  setUnsaved(check: (() => boolean) | null): void;
  /** true when the error ended the session and the host left the screen. */
  signedOut(error: unknown, current: () => boolean): Promise<boolean>;
  /** Opens a hash route; the caller has already dealt with unsaved changes. */
  navigate(hash: string): void;
  /** A protected request just succeeded (the session is alive). */
  sessionUsed(): void;
}

export interface WarmupPage {
  readonly header: HTMLElement;
  readonly title: HTMLElement;
  readonly desc: HTMLElement;
}

// What survives navigation within the signed-in session (never the URL or
// storage): list filters, the notice of a just-created scenario and the file
// names of pictures uploaded in this session (the server keeps no file name).
interface Memory {
  deliveries: WarmupDeliveryQuery;
  deliveryDates: { from: string; to: string };
  coupons: WarmupCouponQuery;
  created: { id: number; text: string } | null;
  imageNames: Map<number, string>;
}

const memory: Memory = {
  deliveries: EMPTY_DELIVERY_QUERY, deliveryDates: { from: '', to: '' }, coupons: EMPTY_COUPON_QUERY, created: null,
  imageNames: new Map(),
};

/** Forgets the session memory (sign-out, tests). */
export function resetWarmupMemory() {
  memory.deliveries = EMPTY_DELIVERY_QUERY;
  memory.deliveryDates = { from: '', to: '' };
  memory.coupons = EMPTY_COUPON_QUERY;
  memory.created = null;
  memory.imageNames.clear();
}

// =============================================================================
// DOM helpers (text is always assigned as text)
// =============================================================================

function el<K extends keyof HTMLElementTagNameMap>(tag: K, className = '', text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function option(value: string, label: string): HTMLOptionElement {
  const node = el('option', '', label);
  node.value = value;
  return node;
}

function textInput(id: string, mode: 'text' | 'numeric' = 'text'): HTMLInputElement {
  const node = el('input', 'input');
  node.id = id;
  node.type = 'text';
  node.autocomplete = 'off';
  node.spellcheck = false;
  node.inputMode = mode;
  return node;
}

function select(id: string, options: readonly (readonly [string, string])[]): HTMLSelectElement {
  const node = el('select', 'input select-input');
  node.id = id;
  for (const [value, label] of options) node.append(option(value, label));
  return node;
}

function checkbox(id: string, label: string): { root: HTMLLabelElement; input: HTMLInputElement } {
  const input = el('input');
  input.type = 'checkbox';
  input.id = id;
  const root = el('label', 'toggle');
  root.htmlFor = id;
  root.append(input, el('span', '', label));
  return { root, input };
}

interface Field {
  readonly root: HTMLElement;
  readonly hint: HTMLElement;
  setError(text: string): void;
}

/** Label, control (with an optional unit after it), hint and error. */
function field(label: string, control: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement, hint = '', suffix = ''): Field {
  const root = el('div', 'field');
  const caption = el('label', 'field-label', label);
  caption.htmlFor = control.id;
  const box = el('div', suffix ? 'control wu-suffixed' : 'control');
  box.append(control);
  if (suffix) box.append(el('span', 'wu-suffix', suffix));
  const hintNode = el('p', 'field-hint', hint);
  hintNode.id = `${control.id}-hint`;
  if (hint) control.setAttribute('aria-describedby', hintNode.id);
  const error = el('p', 'field-error');
  error.id = `${control.id}-error`;
  error.setAttribute('aria-live', 'polite');
  root.append(caption, box, hintNode, error);
  return {
    root, hint: hintNode,
    setError(text: string) {
      error.textContent = text;
      if (text) control.setAttribute('aria-invalid', 'true');
      else control.removeAttribute('aria-invalid');
    },
  };
}

function row(...nodes: HTMLElement[]): HTMLElement {
  const node = el('div', 'form-row');
  node.append(...nodes);
  return node;
}

const numberFormat = new Intl.NumberFormat('ru-RU');
const count = (n: number) => numberFormat.format(n);
const dateTimeFormat = new Intl.DateTimeFormat('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
const formatDateTime = (iso: string) => dateTimeFormat.format(new Date(iso));

function truncate(text: string, max: number): string {
  const chars = Array.from(text);
  return chars.length > max ? `${chars.slice(0, max - 1).join('')}…` : text;
}

type Tone = 'error' | 'info' | 'success' | 'warn';
interface Notice { tone: Tone; text: string; actions?: HTMLElement[] }

const isCode = (error: unknown, code: string) => error instanceof ApiError && error.code === code;

/** "@bob" or the Telegram ID when the user has no username. */
const userLabel = (username: string, telegramId: number) => (username ? (username.startsWith('@') ? username : `@${username}`) : `ID ${telegramId}`);

// =============================================================================
// Screen
// =============================================================================

/** Delay presets of the trigger panel: [value, unit]. */
const DELAY_PRESETS: readonly (readonly [number, 'hours' | 'days'])[] = [[0, 'hours'], [1, 'hours'], [3, 'hours'], [12, 'hours'], [24, 'hours'], [3, 'days'], [7, 'days']];

const TOOLBAR: readonly (readonly [label: string, title: string, open: string, close: string])[] = [
  ['B', 'Жирный', '<b>', '</b>'],
  ['I', 'Курсив', '<i>', '</i>'],
  ['U', 'Подчёркнутый', '<u>', '</u>'],
  ['Code', 'Моноширинный', '<code>', '</code>'],
  ['Pre', 'Блок кода', '<pre>', '</pre>'],
];

type PreviewState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'invalid' }
  | { kind: 'error'; error: unknown }
  | { kind: 'data'; preview: WarmupPreview; stale: boolean };

export class WarmupScreen {
  readonly body = el('div', 'warmup');
  #live = true;
  #request = 0;
  readonly #keys = new RequestKeys();

  constructor(private readonly host: WarmupHost, private readonly route: WarmupRoute, private readonly page: WarmupPage) {}

  /** The route changed: drop every response still in flight. */
  dispose() {
    this.#live = false;
    this.#request++;
  }

  load(): Promise<void> {
    switch (this.route.view) {
      case 'overview': return this.#loadOverview();
      case 'settings': return this.#loadSettings();
      case 'deliveries': return this.#loadDeliveries(null);
      case 'coupons': return this.#loadCoupons(null);
      case 'create': return this.#loadEditor(null);
      case 'scenario': return this.#loadEditor(this.route.scenarioId);
    }
  }

  // Shared ------------------------------------------------------------------

  /** Starts a request of this screen; the returned check is false once it is stale. */
  #begin(): () => boolean {
    const request = ++this.#request;
    return () => this.#live && request === this.#request;
  }

  #button(label: string, className: string, leading?: WarmupIcon): HTMLButtonElement {
    const node = el('button', className);
    node.type = 'button';
    if (leading) node.append(this.host.icon(leading));
    node.append(el('span', 'btn-label', label));
    return node;
  }

  #iconButton(name: WarmupIcon, label: string, className = ''): HTMLButtonElement {
    const node = el('button', `icon-button ${className}`.trim());
    node.type = 'button';
    node.setAttribute('aria-label', label);
    node.title = label;
    node.append(this.host.icon(name));
    return node;
  }

  #link(label: string, href: string, className: string, leading?: WarmupIcon): HTMLAnchorElement {
    const node = el('a', className);
    node.href = href;
    if (leading) node.append(this.host.icon(leading));
    node.append(el('span', 'btn-label', label));
    return node;
  }

  #notice(next: Notice): HTMLElement {
    const node = el('div', `notice notice-${next.tone}`);
    node.setAttribute('role', next.tone === 'error' ? 'alert' : 'status');
    node.append(this.host.icon(next.tone === 'error' || next.tone === 'warn' ? 'alert' : next.tone === 'success' ? 'check' : 'info'));
    const content = el('div', 'notice-body');
    content.append(el('p', '', next.text));
    if (next.actions?.length) {
      const actions = el('div', 'notice-actions');
      actions.append(...next.actions);
      content.append(actions);
    }
    node.append(content);
    return node;
  }

  /** The tab bar of the section pages. */
  #tabs(active: WarmupTab): HTMLElement {
    const nav = el('nav', 'wu-tabs');
    nav.setAttribute('aria-label', 'Разделы прогрева');
    for (const tab of WARMUP_TABS) {
      const link = el('a', 'wu-tab', TAB_LABELS[tab]);
      link.href = warmupHash({ view: tab });
      if (tab === active) link.setAttribute('aria-current', 'page');
      nav.append(link);
    }
    return nav;
  }

  #headerActions(...nodes: HTMLElement[]) {
    const actions = el('div', 'page-actions');
    actions.append(...nodes);
    this.page.header.classList.add('has-actions');
    this.page.header.append(actions);
  }

  #skeleton(label: string, panels: number) {
    this.body.setAttribute('aria-busy', 'true');
    this.body.setAttribute('aria-label', label);
    const nodes: HTMLElement[] = [];
    for (let i = 0; i < panels; i++) {
      const panel = el('div', 'panel wu-skeleton');
      const lines = el('div', 'skeleton-rows');
      for (let j = 0; j < 4; j++) lines.append(el('span', 'skeleton skeleton-row'));
      panel.append(lines);
      nodes.push(panel);
    }
    return nodes;
  }

  #ready() {
    this.body.removeAttribute('aria-busy');
    this.body.removeAttribute('aria-label');
  }

  #errorPanel(title: string, error: unknown, retry: () => void): HTMLElement {
    const panel = el('section', 'panel empty wu-error');
    panel.setAttribute('role', 'alert');
    const again = this.#button('Повторить', 'btn btn-secondary btn-sm', 'refresh');
    again.addEventListener('click', retry);
    panel.append(this.host.icon('alert'), el('h2', 'empty-title', title), el('p', 'empty-text', warmupErrorText(error)), again);
    return panel;
  }

  #emptyPanel(title: string, text: string, action?: HTMLElement): HTMLElement {
    const panel = el('section', 'panel empty wu-empty');
    panel.setAttribute('role', 'status');
    panel.append(this.host.icon('info'), el('h2', 'empty-title', title), el('p', 'empty-text', text));
    if (action) panel.append(action);
    return panel;
  }

  #panel(id: string, title: string, meta = ''): { root: HTMLElement; body: HTMLElement } {
    const root = el('section', 'panel wu-panel');
    root.setAttribute('aria-labelledby', id);
    const head = el('div', 'panel-head');
    const heading = el('h2', 'panel-title', title);
    heading.id = id;
    head.append(heading);
    if (meta) head.append(el('p', 'panel-meta', meta));
    const body = el('div', 'wu-panel-body');
    root.append(head, body);
    return { root, body };
  }

  /** Runs a failed load through the session check; true when nothing more should be painted. */
  async #failed(error: unknown, current: () => boolean): Promise<boolean> {
    if (!current()) return true;
    return this.host.signedOut(error, current);
  }

  // ===========================================================================
  // Overview (#/warmup)
  // ===========================================================================

  async #loadOverview(status?: Notice) {
    const current = this.#begin();
    if (!this.body.childElementCount) {
      const refresh = this.#button('Обновить', 'btn btn-secondary btn-sm', 'refresh');
      refresh.addEventListener('click', () => void this.#loadOverview());
      this.#headerActions(refresh, this.#link('Новый сценарий', warmupHash({ view: 'create' }), 'btn btn-primary btn-sm', 'plus'));
      this.body.replaceChildren(this.#tabs('overview'), ...this.#skeleton('Загружаем сценарии прогрева', 3));
    }
    try {
      const data = await this.host.api.overview();
      if (!current()) return;
      this.host.sessionUsed();
      this.#paintOverview(data, status ?? null);
    } catch (error) {
      if (await this.#failed(error, current)) return;
      this.#ready();
      this.body.replaceChildren(this.#tabs('overview'), this.#errorPanel('Не удалось загрузить прогрев', error, () => void this.#loadOverview()));
    }
  }

  #paintOverview(data: WarmupOverview, status: Notice | null, cardNotices = new Map<number, Notice>(), focusId = '') {
    this.#ready();
    const s = data.summary;
    const statusSlot = el('div', 'editor-status');
    if (status) statusSlot.append(this.#notice(status));

    // Top bar: global switch, short statistics, create.
    const top = el('section', 'panel wu-top');
    top.setAttribute('aria-label', 'Состояние прогрева');
    const sw = checkbox('wu-global', 'Прогрев включён');
    sw.input.checked = data.settings.enabled;
    sw.input.setAttribute('role', 'switch');
    sw.root.classList.add('wu-switch');
    const state = el('p', data.settings.enabled ? 'wu-state is-on' : 'wu-state', data.settings.enabled
      ? 'Сообщения отправляются по включённым сценариям.'
      : 'Прогрев выключен: ничего не отправляется, даже если сценарии включены.');
    const control = el('div', 'wu-top-control');
    control.append(sw.root, state);
    const short = el('p', 'wu-top-meta',
      `${count(s.enabled_scenarios)} из ${count(s.scenarios)} сценариев включено · сегодня отправлено ${count(s.sent_today)} · ` +
      `не больше ${count(data.settings.max_per_day)} в сутки на пользователя · cooldown ${durationWords(data.settings.cooldown_hours)}`);
    const create = this.#link('Новый сценарий', warmupHash({ view: 'create' }), 'btn btn-primary btn-sm', 'plus');
    top.append(control, short, create);
    sw.input.addEventListener('change', () => void this.#toggleGlobal(data, sw.input));

    // Summary figures.
    const last = (key: string) => s.last_24h[key] ?? 0;
    const kpis: readonly (readonly [string, number, string, string])[] = [
      ['Сценариев всего', s.scenarios, `${count(data.categories.length)} категории`, ''],
      ['Активных', s.enabled_scenarios, 'включены сейчас', ''],
      ['Отправлено сегодня', s.sent_today, 'с начала суток', ''],
      ['За 7 дней', s.sent_7d, 'отправлено', ''],
      ['Retry', last('retry'), 'за 24 часа', last('retry') ? 'is-warn' : ''],
      ['Failed', last('failed'), 'за 24 часа', last('failed') ? 'is-alert' : ''],
      ['Blocked', last('blocked'), `за 24 часа · всего ${count(s.blocked_contacts)}`, ''],
      ['Купонов', s.active_coupons, `действуют · выдано ${count(s.coupons_created)}`, ''],
    ];
    const grid = el('dl', 'wu-kpis');
    grid.setAttribute('aria-label', 'Сводка прогрева');
    for (const [label, value, meta, tone] of kpis) {
      const item = el('div', 'kpi wu-kpi');
      const dd = el('dd', `kpi-value ${tone}`.trim(), count(value));
      item.append(el('dt', 'kpi-label', label), dd, el('p', 'kpi-meta', meta));
      grid.append(item);
    }

    const nodes: HTMLElement[] = [this.#tabs('overview'), statusSlot, top, grid];
    if (!data.settings.enabled) {
      nodes.push(this.#notice({ tone: 'warn', text: 'Прогрев выключен глобально. Включите его переключателем выше или в настройках, когда сценарии будут готовы.' }));
    }

    const stats = new Map(s.scenario_stats.map(x => [x.scenario_id, x]));
    for (const category of WARMUP_CATEGORIES) {
      const scenarios = data.scenarios.filter(sc => sc.category === category)
        .sort((a, b) => a.position - b.position || a.id - b.id);
      const enabled = scenarios.filter(sc => sc.enabled).length;
      const section = el('section', 'wu-category');
      section.id = `wu-category-${category}`;
      section.setAttribute('aria-labelledby', `wu-category-${category}-title`);
      const head = el('div', 'wu-category-head');
      const heading = el('h2', 'wu-category-title', CATEGORY_LABELS[category] ?? category);
      heading.id = `wu-category-${category}-title`;
      head.append(heading, el('p', 'wu-category-meta', `${count(scenarios.length)} · включено ${count(enabled)}`));
      section.append(head, el('p', 'wu-category-hint', CATEGORY_HINTS[category] ?? ''));
      if (!scenarios.length) {
        section.append(el('p', 'wu-category-empty', 'В этой категории пока нет сценариев.'));
      } else {
        const list = el('div', 'wu-cards');
        for (const sc of scenarios) {
          list.append(this.#card(data, sc, stats.get(sc.id)?.sent ?? 0, stats.get(sc.id)?.last_sent_at ?? null, cardNotices));
        }
        section.append(list);
      }
      nodes.push(section);
    }
    this.body.replaceChildren(...nodes);
    if (focusId) document.getElementById(focusId)?.focus();
  }

  #card(data: WarmupOverview, sc: WarmupScenario, sent: number, lastSent: string | null, notices: Map<number, Notice>): HTMLElement {
    const card = el('article', sc.enabled ? 'wu-card' : 'wu-card is-off');
    card.dataset.scenario = String(sc.id);
    card.setAttribute('aria-labelledby', `wu-card-${sc.id}`);
    const head = el('div', 'wu-card-head');
    const emoji = el('span', 'wu-emoji', triggerEmoji(sc.trigger_type));
    emoji.setAttribute('aria-hidden', 'true');
    const name = el('h3', 'wu-card-title', sc.name);
    name.id = `wu-card-${sc.id}`;
    const badge = el('span', sc.enabled ? 'tag tag-on' : 'tag', sc.enabled ? 'ВКЛ' : 'ВЫКЛ');
    head.append(emoji, name, badge);

    const trigger = el('p', 'wu-card-trigger', triggerText(sc.trigger_type, sc.timing, sc.delay_value, sc.delay_unit, data.registry));
    const text = el('p', 'wu-card-text', truncate(plainText(sc.message_html), 140));
    const marks = el('ul', 'wu-marks');
    marks.setAttribute('aria-label', 'Особенности сценария');
    const mark = (label: string, className = '') => marks.append(el('li', `wu-mark ${className}`.trim(), label));
    if (sc.image_id !== null) mark('🖼 Картинка');
    if (sc.discount_percent > 0) mark(`🏷 Скидка ${sc.discount_percent}%`, 'is-accent');
    if (sc.buttons.length) mark(`${count(sc.buttons.length)} ${sc.buttons.length === 1 ? 'кнопка' : sc.buttons.length < 5 ? 'кнопки' : 'кнопок'}`);
    if (sc.buttons.some(isHiddenButton)) mark('Кнопка без URL', 'is-warn');
    const statsLine = el('p', 'wu-card-stats',
      `Отправлено: ${count(sent)} · ${lastSent ? `последняя ${formatDateTime(lastSent)}` : 'ещё не отправлялся'}`);

    const actions = el('div', 'wu-card-actions');
    const edit = this.#link('Редактировать', warmupHash({ view: 'scenario', scenarioId: sc.id }), 'btn btn-secondary btn-sm', 'edit');
    const toggle = this.#button(sc.enabled ? 'Выключить' : 'Включить', sc.enabled ? 'btn btn-ghost btn-sm' : 'btn btn-primary btn-sm');
    toggle.id = `wu-toggle-${sc.id}`;
    toggle.setAttribute('aria-describedby', name.id);
    toggle.addEventListener('click', () => void this.#toggleCard(data, sc, toggle, notices));
    actions.append(edit, toggle);
    card.append(head, trigger, text);
    if (marks.childElementCount) card.append(marks);
    card.append(statsLine, actions);
    const n = notices.get(sc.id);
    if (n) card.append(this.#notice(n));
    return card;
  }

  async #toggleGlobal(data: WarmupOverview, input: HTMLInputElement) {
    const s = data.settings;
    const next = input.checked;
    if (next && !(await this.host.confirm('Включить прогрев?',
      `Сообщения начнут уходить по включённым сценариям (${count(data.summary.enabled_scenarios)}). ` +
      `События старше ${durationWords(s.catchup_hours)} не догоняются.`, 'Включить'))) {
      input.checked = !next;
      return;
    }
    if (!this.#live) return;
    const save = { version: s.version, enabled: next, max_per_day: s.max_per_day, cooldown_hours: s.cooldown_hours, catchup_hours: s.catchup_hours };
    const key = this.#keys.key('settings', JSON.stringify(save));
    input.disabled = true;
    try {
      const out = await this.host.api.saveSettings(save, key);
      this.#keys.settle('settings');
      if (!this.#live) return;
      this.#paintOverview({ ...data, settings: out.settings },
        { tone: 'success', text: next ? 'Прогрев включён.' : 'Прогрев выключен: новые сообщения не отправляются.' }, undefined, 'wu-global');
    } catch (error) {
      this.#keys.settle('settings', error);
      if (!this.#live) return;
      input.disabled = false;
      input.checked = !next;
      if (isCode(error, 'version_conflict')) {
        const reload = this.#button('Обновить данные', 'btn btn-secondary btn-sm', 'refresh');
        reload.addEventListener('click', () => void this.#loadOverview());
        this.#paintOverview(data, { tone: 'error', text: SETTINGS_CONFLICT_TEXT, actions: [reload] });
      } else {
        this.#paintOverview(data, { tone: 'error', text: warmupErrorText(error, SETTINGS_CONFLICT_TEXT) });
      }
    }
  }

  async #toggleCard(data: WarmupOverview, sc: WarmupScenario, trigger: HTMLButtonElement, notices: Map<number, Notice>) {
    const next = !sc.enabled;
    const scope = `enable:${sc.id}`;
    const key = this.#keys.key(scope, JSON.stringify({ version: sc.version, enabled: next }));
    trigger.disabled = true;
    trigger.setAttribute('aria-busy', 'true');
    try {
      const out = await this.host.api.patchScenario(sc.id, sc.version, { enabled: next }, key);
      this.#keys.settle(scope);
      if (!this.#live) return;
      const scenarios = data.scenarios.map(x => (x.id === sc.id ? out.scenario : x));
      const enabledCount = scenarios.filter(x => x.enabled).length;
      const categories = data.categories.map(c => ({ ...c, enabled: scenarios.filter(x => x.category === c.key && x.enabled).length }));
      notices.set(sc.id, {
        tone: 'success',
        text: out.scenario.enabled
          ? `Сценарий включён${out.scenario.enabled_at ? ` с ${formatDateTime(out.scenario.enabled_at)}` : ''}. События раньше этого момента не догоняются дальше окна catch-up.`
          : 'Сценарий выключен: новые сообщения по нему не отправляются.',
      });
      this.#paintOverview({ ...data, scenarios, categories, summary: { ...data.summary, enabled_scenarios: enabledCount } }, null, notices, trigger.id);
    } catch (error) {
      this.#keys.settle(scope, error);
      if (!this.#live) return;
      if (isCode(error, 'version_conflict')) {
        const reload = this.#button('Обновить данные', 'btn btn-secondary btn-sm', 'refresh');
        reload.addEventListener('click', () => void this.#loadOverview());
        notices.set(sc.id, { tone: 'error', text: SCENARIO_CONFLICT_TEXT, actions: [reload] });
      } else if (isCode(error, 'duplicates_expiry_reminder')) {
        notices.set(sc.id, { tone: 'warn', text: REMINDER_CONFLICT_TEXT });
      } else {
        notices.set(sc.id, { tone: 'error', text: warmupErrorText(error) });
      }
      this.#paintOverview(data, null, notices, trigger.id);
    }
  }

  // ===========================================================================
  // Scenario editor (#/warmup/scenario/{id}) and create (#/warmup/create)
  // ===========================================================================

  async #loadEditor(id: number | null) {
    const current = this.#begin();
    this.host.setUnsaved(null);
    this.page.title.textContent = id === null ? 'Новый сценарий' : 'Сценарий прогрева';
    this.body.replaceChildren(...this.#skeleton('Загружаем сценарий', 4));
    try {
      const [overview, scenario] = await Promise.all([this.host.api.overview(), id === null ? null : this.host.api.scenario(id)]);
      if (!current()) return;
      this.host.sessionUsed();
      let status: Notice | undefined;
      if (scenario && memory.created?.id === scenario.id) {
        status = { tone: 'success', text: memory.created.text };
        memory.created = null;
      }
      this.#paintEditor(overview, scenario, { status });
    } catch (error) {
      if (await this.#failed(error, current)) return;
      this.#ready();
      if (isCode(error, 'scenario_not_found') || isCode(error, 'not_found')) {
        this.page.title.textContent = 'Сценарий не найден';
        this.body.replaceChildren(this.#emptyPanel('Сценарий не найден', 'Возможно, адрес набран с ошибкой.',
          this.#link('К сценариям', warmupHash({ view: 'overview' }), 'btn btn-secondary btn-sm', 'back')));
        return;
      }
      this.body.replaceChildren(this.#errorPanel('Не удалось загрузить сценарий', error, () => void this.#loadEditor(id)));
    }
  }

  #paintEditor(overview: WarmupOverview, stored: WarmupScenario | null, opts: { status?: Notice; form?: ScenarioForm; key?: string } = {}) {
    this.#ready();
    const registry: WarmupRegistry = overview.registry;
    const limits = registry.limits;
    const creating = stored === null;
    const base = stored;
    const baseline: ScenarioForm = base ? formFromScenario(base) : newScenarioForm(registry, overview.scenarios);
    const form: ScenarioForm = cloneForm(opts.form ?? baseline);
    const baseKey = suggestKey(overview.scenarios.map(s => s.key));
    let saving = false;
    let toggling = false;
    let preview: PreviewState = { kind: 'idle' };
    let previewRequest = 0;
    let previewedAt = '';
    const imageMeta = new Map<number, { size: number; mime: string }>();
    const isLive = () => this.#live;
    const keyInput = textInput('wu-key');
    keyInput.value = opts.key ?? baseKey;
    const dirty = () => formDirty(form, baseline) || (creating && keyInput.value.trim() !== baseKey);
    this.host.setUnsaved(() => isLive() && dirty());
    this.page.title.textContent = creating ? 'Новый сценарий' : `${triggerEmoji(base?.trigger_type ?? '')} ${base?.name ?? ''}`;
    this.page.desc.textContent = creating
      ? 'Новый сценарий создаётся выключенным: он ничего не отправит, пока вы его не включите.'
      : 'Текст, кнопки, условия и время отправки сценария. Сохранение меняет только этот сценарий.';

    const statusSlot = el('div', 'editor-status');
    const showStatus = (next: Notice | null) => {
      statusSlot.replaceChildren(...(next ? [this.#notice(next)] : []));
      if (next) statusSlot.scrollIntoView?.({ block: 'nearest' });
    };
    if (opts.status) showStatus(opts.status);

    const setters = new Map<ScenarioField | 'key', (text: string) => void>();
    const targets = new Map<ScenarioField | 'key', HTMLElement>();
    const register = (name: ScenarioField | 'key', f: Field, target: HTMLElement) => {
      setters.set(name, f.setError);
      targets.set(name, target);
    };
    const clearErrors = () => { for (const set of setters.values()) set(''); };
    const showErrors = (errors: Partial<Record<ScenarioField | 'key', string>>) => {
      let first: HTMLElement | null = null;
      for (const [name, text] of Object.entries(errors) as [ScenarioField | 'key', string][]) {
        setters.get(name)?.(text);
        first ??= targets.get(name) ?? null;
      }
      first?.focus();
    };

    // Basics -------------------------------------------------------------------
    const basics = this.#panel('wu-basics-title', 'Основные');
    const name = textInput('wu-name');
    name.maxLength = 100;
    name.value = form.name;
    const nameField = field('Название', name, 'Видно только в панели.');
    register('name', nameField, name);
    const description = el('textarea', 'input textarea');
    description.id = 'wu-description';
    description.rows = 2;
    description.maxLength = 500;
    description.value = form.description;
    const descriptionField = field('Описание', description, 'Заметка для администраторов, до 500 символов.');
    register('description', descriptionField, description);
    const category = select('wu-category', WARMUP_CATEGORIES.map(c => [c, CATEGORY_LABELS[c] ?? c] as const));
    category.value = form.category;
    const categoryField = field('Категория', category);
    register('category', categoryField, category);
    const position = textInput('wu-position', 'numeric');
    position.value = form.position;
    const positionField = field('Позиция', position, 'Порядок внутри категории: меньше — выше.');
    register('position', positionField, position);
    basics.body.append(nameField.root, descriptionField.root, row(categoryField.root, positionField.root));
    if (creating) {
      const keyField = field('Ключ', keyInput, 'Латинские строчные буквы, цифры и _, от 3 до 64, первая — буква. После создания не меняется.');
      register('key', keyField, keyInput);
      basics.body.append(keyField.root);
    }
    const statusRow = el('div', 'wu-status-row');
    const paintStatus = () => {
      statusRow.replaceChildren();
      if (!base) {
        statusRow.append(el('span', 'tag', 'ВЫКЛ'), el('p', 'field-hint', 'Новый сценарий создаётся выключенным и не создаёт отправок. Включить его можно после создания.'));
        return;
      }
      const toggle = this.#button(base.enabled ? 'Выключить' : 'Включить', base.enabled ? 'btn btn-secondary btn-sm' : 'btn btn-primary btn-sm');
      toggle.id = 'wu-enable';
      toggle.disabled = toggling;
      toggle.addEventListener('click', () => void onToggle());
      const text = base.enabled
        ? `Включён${base.enabled_at ? ` с ${formatDateTime(base.enabled_at)}` : ''}. ${overview.settings.enabled ? '' : 'Прогрев выключен глобально — сообщения не уходят.'}`
        : 'Выключен: сообщения по сценарию не отправляются.';
      statusRow.append(el('span', base.enabled ? 'tag tag-on' : 'tag', base.enabled ? 'ВКЛ' : 'ВЫКЛ'), el('p', 'wu-status-text', text.trim()), toggle);
      statusRow.append(el('p', 'wu-key-note', `Ключ: ${base.key}`));
    };
    paintStatus();
    const statusWrap = el('div', 'field');
    statusWrap.append(el('p', 'field-label', 'Статус'), statusRow);
    basics.body.append(statusWrap);

    // Trigger ----------------------------------------------------------------
    const triggerPanel = this.#panel('wu-trigger-title', 'Триггер');
    const triggerSelect = select('wu-trigger', registry.triggers.map(t => [t.type, t.label] as const));
    if (!registry.triggers.some(t => t.type === form.trigger_type)) triggerSelect.prepend(option(form.trigger_type, form.trigger_type || 'Выберите триггер'));
    triggerSelect.value = form.trigger_type;
    const triggerField = field('Событие', triggerSelect);
    register('trigger_type', triggerField, triggerSelect);
    const timing = select('wu-timing', []);
    const timingField = field('Момент', timing);
    register('timing', timingField, timing);
    const fillTimings = () => {
      const trigger = registry.triggers.find(t => t.type === form.trigger_type);
      const timings = trigger?.timings ?? [form.timing];
      timing.replaceChildren(...timings.map(t => option(t, TIMING_LABELS[t] ?? t)));
      if (!timings.includes(form.timing)) form.timing = timings[0] ?? 'after';
      timing.value = form.timing;
      timing.disabled = timings.length < 2;
      triggerField.hint.textContent = trigger?.description ?? '';
    };
    fillTimings();
    const delay = textInput('wu-delay', 'numeric');
    delay.value = form.delay_value;
    const delayField = field('Задержка', delay);
    register('delay_value', delayField, delay);
    const unit = select('wu-unit', WARMUP_DELAY_UNITS.map(u => [u, UNIT_LABELS[u]] as const));
    unit.value = form.delay_unit;
    const unitField = field('Единица', unit);
    register('delay_unit', unitField, unit);
    const presets = el('div', 'chips');
    presets.setAttribute('role', 'group');
    presets.setAttribute('aria-label', 'Быстрый выбор задержки');
    for (const [value, presetUnit] of DELAY_PRESETS) {
      const chip = el('button', 'chip', value === 0 ? 'Сразу' : delayText(value, presetUnit));
      chip.type = 'button';
      chip.addEventListener('click', () => {
        form.delay_value = String(value);
        form.delay_unit = presetUnit;
        delay.value = form.delay_value;
        unit.value = presetUnit;
        changed();
      });
      presets.append(chip);
    }
    const human = el('p', 'wu-human');
    human.id = 'wu-trigger-text';
    human.setAttribute('aria-live', 'polite');
    triggerPanel.body.append(row(triggerField.root, timingField.root), row(delayField.root, unitField.root), presets, human,
      el('p', 'field-hint', `Точное время отправки рассчитывает сервер. После включения события старше окна catch-up (${durationWords(overview.settings.catchup_hours)}) не догоняются.`));
    const paintHuman = () => {
      const value = /^\d{1,6}$/.test(form.delay_value.trim()) ? Number(form.delay_value.trim()) : null;
      human.textContent = value === null ? 'Укажите задержку целым числом.' : triggerText(form.trigger_type, form.timing, value, form.delay_unit, registry);
    };

    // Message ----------------------------------------------------------------
    const messagePanel = this.#panel('wu-message-title', 'Сообщение', 'Telegram HTML');
    const message = el('textarea', 'input textarea wu-code');
    message.id = 'wu-message';
    message.rows = 10;
    message.spellcheck = true;
    message.value = form.message_html;
    const messageField = field('Текст', message, 'Разметка Telegram HTML: <b>, <i>, <u>, <s>, <a href>, <code>, <pre>, <blockquote>, <tg-spoiler>. Символы <, > и & в тексте — &lt; &gt; &amp;.');
    register('message_html', messageField, message);
    const toolbar = el('div', 'wu-toolbar');
    toolbar.setAttribute('role', 'toolbar');
    toolbar.setAttribute('aria-label', 'Форматирование');
    toolbar.setAttribute('aria-controls', message.id);
    const applyEdit = (edit: { text: string; start: number; end: number }) => {
      message.value = edit.text;
      message.focus();
      message.setSelectionRange(edit.start, edit.end);
      form.message_html = message.value;
      changed();
    };
    for (const [label, title, open, close] of TOOLBAR) {
      const b = el('button', `wu-tool wu-tool-${label.toLowerCase()}`, label);
      b.type = 'button';
      b.title = title;
      b.setAttribute('aria-label', title);
      b.addEventListener('click', () => applyEdit(wrapSelection(message.value, message.selectionStart, message.selectionEnd, open, close)));
      toolbar.append(b);
    }
    const linkTool = el('button', 'wu-tool wu-tool-link', 'Ссылка');
    linkTool.type = 'button';
    linkTool.title = 'Ссылка';
    linkTool.addEventListener('click', () => applyEdit(insertLink(message.value, message.selectionStart, message.selectionEnd)));
    toolbar.insertBefore(linkTool, toolbar.children[3] ?? null);
    const counter = el('p', 'counter');
    counter.id = 'wu-counter';
    counter.setAttribute('aria-live', 'polite');
    const variables = el('div', 'wu-variables');
    variables.setAttribute('role', 'group');
    variables.setAttribute('aria-label', 'Переменные');
    const variableNote = el('div', 'wu-variable-notes');
    variableNote.setAttribute('aria-live', 'polite');
    const paintVariables = () => {
      variables.replaceChildren(el('span', 'wu-variables-label', 'Вставить:'));
      for (const v of registry.variables) {
        const state = variableState(v, form.trigger_type, form.discount);
        const chip = el('button', `chip wu-var${state === 'available' ? '' : ' is-unavailable'}`, `+ ${v.name}`);
        chip.type = 'button';
        chip.dataset.variable = v.name;
        chip.title = state === 'trigger' ? `${v.label}: недоступна для выбранного триггера`
          : state === 'discount' ? `${v.label}: доступна только при включённой скидке` : `${v.label}: ${v.description}`;
        chip.disabled = state !== 'available';
        chip.addEventListener('click', () => applyEdit(insertText(message.value, message.selectionStart, message.selectionEnd, `{{${v.name}}}`)));
        variables.append(chip);
      }
      // A hint only: the server decides whether a variable may be used.
      const notes: string[] = [];
      for (const used of placeholders(form.message_html)) {
        const v = registry.variables.find(x => x.name === used);
        if (!v) notes.push(`{{${used}}} — неизвестная переменная, сервер её не примет.`);
        else if (variableState(v, form.trigger_type, form.discount) === 'trigger') notes.push(`{{${used}}} недоступна для выбранного триггера.`);
        else if (variableState(v, form.trigger_type, form.discount) === 'discount') notes.push(`{{${used}}} работает только при включённой скидке.`);
      }
      if (form.discount && !placeholders(form.message_html).includes('discount_code')) notes.push(reasonText('discount_code_required'));
      variableNote.replaceChildren(...notes.map(t => el('p', 'wu-warn', t)));
    };
    const paintCounter = () => {
      const limit = form.image_id !== null ? limits.max_caption_length : limits.max_text_length;
      const n = visibleLength(form.message_html);
      counter.textContent = `${count(n)} / ${count(limit)}${form.image_id !== null ? ' (подпись к картинке)' : ''}`;
      counter.classList.toggle('is-over', n > limit);
    };
    messagePanel.body.append(toolbar, messageField.root, counter, variables, variableNote,
      el('p', 'field-hint', 'Длина считается без тегов, переменные — как написаны; точную длину с подставленными значениями показывает предпросмотр.'));

    // Image ------------------------------------------------------------------
    const imagePanel = this.#panel('wu-image-title', 'Картинка');
    const fileInput = el('input', 'sr-only');
    fileInput.type = 'file';
    fileInput.id = 'wu-image-file';
    fileInput.accept = 'image/jpeg,image/png';
    fileInput.tabIndex = -1;
    fileInput.setAttribute('aria-hidden', 'true');
    const imageBody = el('div', 'wu-image');
    const imageError = el('p', 'field-error');
    imageError.id = 'wu-image-error';
    imageError.setAttribute('role', 'alert');
    setters.set('image_id', text => { imageError.textContent = text; });
    targets.set('image_id', imageBody);
    let imageBusy = false;
    const deleteQuietly = async (id: number) => {
      const scope = `image-delete:${id}`;
      try {
        await this.host.api.deleteImage(id, this.#keys.key(scope, String(id)));
        this.#keys.settle(scope);
      } catch (error) {
        // Best-effort cleanup of a picture no longer shown: one still used by
        // a scenario (image_in_use) or already gone stays as it is.
        this.#keys.settle(scope, error);
      }
    };
    const paintImage = () => {
      imageBody.replaceChildren();
      const add = this.#button(form.image_id === null ? 'Добавить' : 'Заменить', 'btn btn-secondary btn-sm', 'plus');
      add.id = 'wu-image-add';
      add.disabled = imageBusy;
      add.setAttribute('aria-busy', String(imageBusy));
      add.addEventListener('click', () => fileInput.click());
      if (form.image_id === null) {
        imageBody.append(el('p', 'wu-image-empty', imageBusy ? 'Загружаем картинку…' : 'Картинки нет: сообщение уйдёт текстом.'), add);
        return;
      }
      const id = form.image_id;
      const figure = el('figure', 'wu-image-figure');
      const img = el('img', 'wu-image-thumb');
      img.src = warmupImageUrl(id);
      img.alt = 'Картинка сообщения';
      img.loading = 'lazy';
      const meta = imageMeta.get(id);
      const facts = el('dl', 'wu-image-facts');
      const fact = (label: string, value: string) => {
        const item = el('div');
        item.append(el('dt', '', label), el('dd', '', value));
        facts.append(item);
      };
      fact('Файл', memory.imageNames.get(id) ?? `Картинка #${id}`);
      fact('Размер', meta ? formatBytes(meta.size) : '—');
      fact('Тип', meta?.mime ?? '—');
      if (base && base.image_id === id) fact('Состояние', 'сохранена в сценарии');
      else fact('Состояние', 'новая, сохраните сценарий');
      figure.append(img, facts);
      const remove = this.#button('Удалить', 'btn btn-danger btn-sm', 'trash');
      remove.id = 'wu-image-remove';
      remove.disabled = imageBusy;
      remove.addEventListener('click', () => {
        const removed = form.image_id;
        form.image_id = null;
        if (removed !== null && removed !== base?.image_id) void deleteQuietly(removed);
        paintImage();
        changed();
      });
      const actions = el('div', 'wu-image-actions');
      actions.append(add, remove);
      imageBody.append(figure, actions);
    };
    const upload = async (file: File) => {
      imageError.textContent = '';
      if (file.type !== 'image/jpeg' && file.type !== 'image/png') { imageError.textContent = reasonText('unsupported_type'); return; }
      if (file.size > limits.image_max_bytes) { imageError.textContent = `Файл больше ${formatBytes(limits.image_max_bytes)}.`; return; }
      imageBusy = true;
      paintImage();
      const scope = 'image-upload';
      try {
        const data = await fileToBase64(file);
        const image = await this.host.api.uploadImage(data, this.#keys.key(scope, `${file.name}:${file.size}:${file.lastModified}`));
        this.#keys.settle(scope);
        if (!isLive()) return;
        const previous = form.image_id;
        memory.imageNames.set(image.id, file.name);
        imageMeta.set(image.id, { size: image.size_bytes, mime: image.mime });
        form.image_id = image.id;
        if (previous !== null && previous !== image.id && previous !== base?.image_id) void deleteQuietly(previous);
        changed();
      } catch (error) {
        this.#keys.settle(scope, error);
        if (!isLive()) return;
        imageError.textContent = error instanceof ApiError && error.reason ? reasonText(error.reason) : warmupErrorText(error);
      } finally {
        imageBusy = false;
        if (isLive()) paintImage();
      }
    };
    fileInput.addEventListener('change', () => {
      const file = fileInput.files?.[0];
      fileInput.value = '';
      if (file) void upload(file);
    });
    imagePanel.body.append(imageBody, imageError, fileInput,
      el('p', 'field-hint', `JPEG или PNG, до ${formatBytes(limits.image_max_bytes)}. С картинкой текст уходит подписью: до ${count(limits.max_caption_length)} символов. Сервер проверяет файл ещё раз.`));

    // Buttons ----------------------------------------------------------------
    const buttonsPanel = this.#panel('wu-buttons-title', 'Кнопки', `до ${limits.max_buttons}`);
    const buttonsError = el('p', 'field-error');
    buttonsError.id = 'wu-buttons-error';
    buttonsError.setAttribute('role', 'alert');
    setters.set('buttons', text => { buttonsError.textContent = text; });
    const buttonList = el('ol', 'wu-button-list');
    buttonList.id = 'wu-buttons';
    buttonList.tabIndex = -1;
    targets.set('buttons', buttonList);
    const keyboard = el('div', 'wu-keyboard');
    keyboard.setAttribute('aria-label', 'Раскладка клавиатуры');
    const addButton = this.#button('Добавить кнопку', 'btn btn-secondary btn-sm', 'plus');
    addButton.id = 'wu-button-add';
    const trialUrl = textInput('wu-trial-url');
    trialUrl.placeholder = 'https://';
    const trialUrlField = field('URL страницы пробника', trialUrl, 'Адрес кнопки «Активировать пробник».');
    const trialUrlWarn = el('p', 'wu-warn');
    trialUrlWarn.setAttribute('aria-live', 'polite');
    trialUrlField.root.append(trialUrlWarn);
    const trialUrlIndex = () => form.buttons.findIndex(b => b.kind === 'url');
    const isTrialScenario = base?.key === TRIAL_URL_SCENARIO;
    const paintTrialUrl = () => {
      const i = trialUrlIndex();
      trialUrl.disabled = i < 0;
      if (document.activeElement !== trialUrl) trialUrl.value = i >= 0 ? form.buttons[i].value : '';
      trialUrlWarn.textContent = i < 0 ? 'В сценарии нет URL-кнопки.' : form.buttons[i].value.trim() ? '' : HIDDEN_BUTTON_TEXT;
    };
    trialUrl.addEventListener('input', () => {
      const i = trialUrlIndex();
      if (i < 0) return;
      form.buttons = form.buttons.map((b, j) => (j === i ? { ...b, value: trialUrl.value } : b));
      paintButtons();
      changed();
    });
    const paintKeyboard = () => {
      keyboard.replaceChildren();
      if (!form.buttons.length) { keyboard.append(el('p', 'wu-keyboard-empty', 'Без кнопок.')); return; }
      for (const line of buttonRows(form.buttons)) {
        const r = el('div', 'wu-kb-row');
        for (const b of line) {
          const kind = b.kind === 'url' ? ' ↗' : b.kind === 'web_app' ? ' ▣' : '';
          r.append(el('span', isHiddenButton(b) ? 'wu-kb-btn is-hidden' : 'wu-kb-btn', `${b.text.trim() || 'Без текста'}${kind}`));
        }
        keyboard.append(r);
      }
    };
    const updateButton = (index: number, next: WarmupButton, repaint = false) => {
      form.buttons = form.buttons.map((b, i) => (i === index ? next : b));
      if (repaint) paintButtons(`b${index}-value`);
      else refreshButtonMeta();
      changed();
    };
    const refreshButtonMeta = () => {
      buttonList.querySelectorAll<HTMLElement>('.wu-button-item').forEach((item, i) => {
        const b = form.buttons[i];
        if (!b) return;
        const error = item.querySelector<HTMLElement>('.wu-button-error');
        const warn = item.querySelector<HTMLElement>('.wu-button-warn');
        if (error) error.textContent = buttonError(b, registry.callbacks);
        if (warn) warn.textContent = isHiddenButton(b) ? HIDDEN_BUTTON_TEXT : '';
      });
      paintKeyboard();
      if (isTrialScenario) paintTrialUrl();
      addButton.disabled = form.buttons.length >= limits.max_buttons;
    };
    const paintButtons = (focus = '') => {
      buttonList.replaceChildren();
      form.buttons.forEach((b, i) => {
        const item = el('li', 'wu-button-item');
        item.dataset.index = String(i);
        const head = el('div', 'wu-button-head');
        head.append(el('span', 'wu-button-pos', `Кнопка ${i + 1}`));
        const up = this.#iconButton('up', `Кнопка ${i + 1}: выше`);
        up.dataset.focus = `b${i}-up`;
        up.disabled = i === 0;
        up.addEventListener('click', () => { form.buttons = moveButton(form.buttons, i, -1); paintButtons(`b${i - 1}-up`); changed(); });
        const down = this.#iconButton('chevron', `Кнопка ${i + 1}: ниже`);
        down.dataset.focus = `b${i}-down`;
        down.disabled = i === form.buttons.length - 1;
        down.addEventListener('click', () => { form.buttons = moveButton(form.buttons, i, 1); paintButtons(`b${i + 1}-down`); changed(); });
        const remove = this.#iconButton('trash', `Кнопка ${i + 1}: удалить`, 'is-danger');
        remove.addEventListener('click', () => {
          form.buttons = normalizeRows(form.buttons.filter((_, j) => j !== i));
          paintButtons(form.buttons.length ? `b${Math.min(i, form.buttons.length - 1)}-text` : 'add');
          changed();
        });
        const tools = el('div', 'wu-button-tools');
        tools.append(up, down, remove);
        head.append(tools);

        const text = textInput(`wu-b${i}-text`);
        text.dataset.focus = `b${i}-text`;
        text.maxLength = 64;
        text.value = b.text;
        text.addEventListener('input', () => updateButton(i, { ...form.buttons[i], text: text.value }));
        const kind = select(`wu-b${i}-kind`, WARMUP_BUTTON_KINDS.map(k => [k, BUTTON_KIND_LABELS[k] ?? k] as const));
        kind.value = b.kind;
        kind.addEventListener('change', () => {
          const current = form.buttons[i];
          const urlLike = (k: string) => k === 'url' || k === 'web_app';
          const value = urlLike(kind.value) && urlLike(current.kind) ? current.value
            : kind.value === 'callback' ? (registry.callbacks[0]?.data ?? '') : '';
          updateButton(i, { ...current, kind: kind.value, value }, true);
        });
        let valueControl: HTMLInputElement | HTMLSelectElement;
        if (b.kind === 'callback') {
          // Only the backend whitelist; arbitrary callback_data is never typed.
          const action = select(`wu-b${i}-value`, registry.callbacks.map(c => [c.data, c.label] as const));
          if (!registry.callbacks.some(c => c.data === b.value)) action.prepend(option(b.value, b.value ? `Недоступно: ${b.value}` : 'Выберите действие'));
          action.value = b.value;
          action.addEventListener('change', () => updateButton(i, { ...form.buttons[i], value: action.value }));
          valueControl = action;
        } else {
          const url = textInput(`wu-b${i}-value`);
          url.placeholder = 'https://';
          url.inputMode = 'url';
          url.value = b.value;
          url.addEventListener('input', () => updateButton(i, { ...form.buttons[i], value: url.value }));
          valueControl = url;
        }
        valueControl.dataset.focus = `b${i}-value`;
        const sameRow = checkbox(`wu-b${i}-row`, 'В одной строке с предыдущей');
        sameRow.input.checked = Boolean(b.same_row) && i > 0;
        sameRow.input.disabled = i === 0;
        sameRow.input.addEventListener('change', () => updateButton(i, setSameRow(form.buttons[i], sameRow.input.checked)));
        const fields = el('div', 'wu-button-fields');
        fields.append(
          field('Текст', text).root,
          field('Тип', kind).root,
          field(b.kind === 'callback' ? 'Действие' : b.kind === 'web_app' ? 'URL Mini App (https)' : 'URL', valueControl).root,
        );
        const error = el('p', 'field-error wu-button-error');
        const warn = el('p', 'wu-warn wu-button-warn');
        item.append(head, fields, sameRow.root, error, warn);
        buttonList.append(item);
      });
      refreshButtonMeta();
      if (focus === 'add') addButton.focus();
      else if (focus) buttonList.querySelector<HTMLElement>(`[data-focus='${focus}']`)?.focus();
    };
    addButton.addEventListener('click', () => {
      if (form.buttons.length >= limits.max_buttons) return;
      form.buttons = [...form.buttons, { text: 'Кнопка', kind: 'callback', value: registry.callbacks[0]?.data ?? '' }];
      paintButtons(`b${form.buttons.length - 1}-text`);
      changed();
    });
    if (isTrialScenario) buttonsPanel.body.append(trialUrlField.root);
    buttonsPanel.body.append(buttonList, buttonsError, addButton, el('p', 'preview-caption', 'Раскладка'), keyboard,
      el('p', 'field-hint', 'Callback — только действия бота из списка. Mini App — только https://. Кнопка без URL скрывается при отправке.'));

    // Conditions -------------------------------------------------------------
    const conditionsPanel = this.#panel('wu-conditions-title', 'Условия', 'кому отправлять');
    const conditionsError = el('p', 'field-error');
    conditionsError.setAttribute('role', 'alert');
    const conditionsList = el('div', 'wu-conditions');
    conditionsList.id = 'wu-conditions';
    conditionsList.tabIndex = -1;
    setters.set('conditions', text => { conditionsError.textContent = text; });
    targets.set('conditions', conditionsList);
    const conditionsSummary = el('p', 'wu-human');
    conditionsSummary.setAttribute('aria-live', 'polite');
    const paintConditionsSummary = () => {
      const parts: string[] = [];
      for (const c of registry.conditions) {
        if (isFlagCondition(c.key)) {
          const choice = flagChoice(form.conditions, c.key);
          if (choice !== 'any') parts.push(`${c.label.toLowerCase()}: ${choice === 'yes' ? 'да' : 'нет'}`);
        } else if (c.kind === 'languages' && form.conditions.languages?.length) {
          parts.push(`язык: ${form.conditions.languages.join(', ')}`);
        }
      }
      conditionsSummary.textContent = parts.length ? `Отправлять, если ${parts.join(' · ')}.` : 'Отправлять всем, кого выбрал триггер.';
    };
    conditionsList.append(el('p', 'field-label', 'Отправлять, если:'));
    for (const c of registry.conditions) {
      const line = el('div', 'wu-condition');
      if (c.kind === 'bool' && isFlagCondition(c.key)) {
        const key = c.key;
        const choice = select(`wu-cond-${key}`, [['any', 'Не важно'], ['yes', 'Да'], ['no', 'Нет']]);
        choice.value = flagChoice(form.conditions, key);
        choice.addEventListener('change', () => {
          form.conditions = withFlag(form.conditions, key, choice.value as FlagChoice);
          changed();
        });
        line.append(field(c.label, choice, c.description).root);
      } else if (c.kind === 'languages') {
        const langs = textInput('wu-cond-languages');
        langs.placeholder = 'ru, en';
        langs.value = (form.conditions.languages ?? []).join(', ');
        const langField = field(c.label, langs, c.description);
        langs.addEventListener('input', () => {
          const parsed = parseLanguages(langs.value);
          langField.setError(parsed === null ? reasonText('conditions_languages') : '');
          if (parsed !== null) form.conditions = withLanguages(form.conditions, parsed);
          changed();
        });
        line.append(langField.root);
      } else {
        line.append(el('p', 'field-hint', `Условие «${c.label}» не поддерживается этой версией панели и не изменяется.`));
      }
      conditionsList.append(line);
    }
    conditionsPanel.body.append(conditionsList, conditionsError, conditionsSummary,
      el('p', 'field-hint', 'Всегда: пользователям, заблокировавшим бота, ничего не отправляется; общий лимит и cooldown из настроек действуют для каждого сценария.'));

    // Discount ---------------------------------------------------------------
    const discountPanel = this.#panel('wu-discount-title', 'Скидка к сообщению');
    const radios = el('div', 'wu-radios');
    radios.setAttribute('role', 'radiogroup');
    radios.setAttribute('aria-label', 'Скидка');
    const radio = (value: 'none' | 'percent', label: string) => {
      const input = el('input');
      input.type = 'radio';
      input.name = 'wu-discount';
      input.id = `wu-discount-${value}`;
      input.value = value;
      input.checked = (value === 'percent') === form.discount;
      input.addEventListener('change', () => {
        if (!input.checked) return;
        form.discount = value === 'percent';
        discountBody.hidden = !form.discount;
        // Seed scenarios have no prefix: suggest the usual one when a discount is turned on.
        if (form.discount && !form.coupon_template.trim()) {
          form.coupon_template = 'VPN';
          template.value = form.coupon_template;
        }
        changed();
      });
      const wrap = el('label', 'toggle');
      wrap.htmlFor = input.id;
      wrap.append(input, el('span', '', label));
      radios.append(wrap);
    };
    radio('none', 'Нет');
    radio('percent', 'Процент');
    const discountBody = el('div', 'wu-discount');
    discountBody.hidden = !form.discount;
    const percent = textInput('wu-percent', 'numeric');
    percent.value = form.discount_percent;
    const percentField = field('Процент', percent, 'От 1 до 100.', '%');
    register('discount_percent', percentField, percent);
    const ttl = textInput('wu-ttl', 'numeric');
    ttl.value = form.coupon_ttl_hours;
    const ttlField = field('Срок купона', ttl, '', 'часов');
    register('coupon_ttl_hours', ttlField, ttl);
    const template = textInput('wu-template');
    template.maxLength = 16;
    template.value = form.coupon_template;
    const templateField = field('Префикс кода', template, 'Латинские буквы и цифры, до 16. Пусто — код без префикса.');
    register('coupon_template', templateField, template);
    const personal = checkbox('wu-personal', 'Создавать персональный код');
    personal.input.checked = true;
    personal.input.disabled = true;
    const example = el('p', 'wu-human');
    example.id = 'wu-coupon-example';
    const insertCode = this.#button('Вставить {{discount_code}} в текст', 'btn btn-ghost btn-sm', 'plus');
    insertCode.addEventListener('click', () => applyEdit(insertText(message.value, message.selectionEnd, message.selectionEnd, '{{discount_code}}')));
    discountBody.append(row(percentField.root, ttlField.root), templateField.root, personal.root,
      el('p', 'field-hint', 'Сервер создаёт отдельный код каждому получателю в момент отправки. В тексте нужен {{discount_code}}.'),
      example, insertCode, this.#notice({ tone: 'info', text: 'Купон пока не применяется автоматически при оплате.' }));
    discountPanel.body.append(radios, discountBody);
    const paintDiscount = () => {
      const hours = /^\d{1,6}$/.test(form.coupon_ttl_hours.trim()) ? Number(form.coupon_ttl_hours.trim()) : null;
      ttlField.hint.textContent = hours !== null && hours > 0 ? `Действует ${durationWords(hours)} после отправки.` : `От 1 до ${limits.max_coupon_ttl_hours} часов.`;
      example.textContent = `Пример кода: ${couponExample(form.coupon_template)}`;
      insertCode.hidden = placeholders(form.message_html).includes('discount_code');
    };

    // Limits -----------------------------------------------------------------
    const limitsPanel = this.#panel('wu-limits-title', 'Время и ограничения');
    const cooldownRadios = el('div', 'wu-radios');
    cooldownRadios.setAttribute('role', 'radiogroup');
    cooldownRadios.setAttribute('aria-label', 'Cooldown');
    const cooldown = textInput('wu-cooldown', 'numeric');
    cooldown.value = form.cooldown_hours;
    const cooldownField = field('Свой cooldown', cooldown, '', 'часов');
    register('cooldown_hours', cooldownField, cooldown);
    const cooldownRadio = (own: boolean, label: string) => {
      const input = el('input');
      input.type = 'radio';
      input.name = 'wu-cooldown-mode';
      input.id = own ? 'wu-cooldown-own' : 'wu-cooldown-global';
      input.checked = form.own_cooldown === own;
      input.addEventListener('change', () => {
        if (!input.checked) return;
        form.own_cooldown = own;
        if (own && !form.cooldown_hours.trim()) { form.cooldown_hours = String(overview.settings.cooldown_hours); cooldown.value = form.cooldown_hours; }
        cooldownField.root.hidden = !own;
        changed();
      });
      const wrap = el('label', 'toggle');
      wrap.htmlFor = input.id;
      wrap.append(input, el('span', '', label));
      cooldownRadios.append(wrap);
    };
    cooldownRadio(false, `Общий из настроек (${durationWords(overview.settings.cooldown_hours)})`);
    cooldownRadio(true, 'Свой для сценария');
    cooldownField.root.hidden = !form.own_cooldown;
    const cap = textInput('wu-cap', 'numeric');
    cap.value = form.daily_cap;
    const capField = field('Лимит сценария в сутки', cap, `Сколько сообщений этого сценария уходит за сутки всем пользователям; 0 — без лимита (до ${count(limits.max_daily_cap)}).`, 'сообщений');
    register('daily_cap', capField, cap);
    const cooldownWrap = el('div', 'field');
    cooldownWrap.append(el('p', 'field-label', 'Cooldown'), cooldownRadios, cooldownField.root,
      el('p', 'field-hint', 'Минимальный интервал между сообщениями прогрева одному пользователю.'));
    limitsPanel.body.append(cooldownWrap, capField.root);
    const paintLimits = () => {
      const hours = /^\d{1,6}$/.test(form.cooldown_hours.trim()) ? Number(form.cooldown_hours.trim()) : null;
      cooldownField.hint.textContent = hours !== null ? `= ${durationWords(hours)}. От 0 до ${count(limits.max_cooldown_hours)} часов.` : `От 0 до ${count(limits.max_cooldown_hours)} часов.`;
    };

    // Preview ----------------------------------------------------------------
    const previewPanel = this.#panel('wu-preview-title', 'Предпросмотр', 'как в Telegram');
    previewPanel.root.classList.add('wu-preview-panel');
    const previewButton = this.#button('Предпросмотр', 'btn btn-secondary btn-sm', 'eye');
    previewButton.id = 'wu-preview';
    const previewBody = el('div', 'wu-preview');
    previewBody.id = 'wu-preview-body';
    previewBody.setAttribute('aria-live', 'polite');
    previewPanel.body.append(
      el('p', 'field-hint', 'Тестовый получатель: @bob, тариф Gold, окончание 31.12.2026. Реальным пользователям ничего не отправляется, отправки и купоны не создаются.'),
      previewButton, previewBody);
    const paintPreview = () => {
      previewBody.replaceChildren();
      previewButton.disabled = preview.kind === 'loading';
      previewButton.setAttribute('aria-busy', String(preview.kind === 'loading'));
      switch (preview.kind) {
        case 'idle': previewBody.append(el('p', 'field-hint', 'Нажмите «Предпросмотр», чтобы увидеть сообщение.')); return;
        case 'loading': previewBody.append(el('p', 'field-hint', 'Собираем сообщение…')); return;
        case 'invalid': previewBody.append(el('p', 'wu-warn', 'Исправьте отмеченные поля — тогда сервер соберёт предпросмотр.')); return;
        case 'error': previewBody.append(this.#notice({ tone: 'error', text: warmupErrorText(preview.error) })); return;
        case 'data': break;
      }
      const p = preview.preview;
      if (preview.stale) previewBody.append(el('p', 'wu-warn wu-stale', 'Форма изменилась — обновите предпросмотр.'));
      if (p.problems.length) {
        const list = el('ul', 'wu-problems');
        for (const problem of p.problems) {
          list.append(el('li', problem.severity === 'invalid' ? 'is-error' : 'is-warn', problemText(problem)));
        }
        previewBody.append(list);
      }
      if (!p.message) {
        previewBody.append(el('p', 'wu-warn', 'Сообщение не будет отправлено, пока ошибки не исправлены.'));
        return;
      }
      const chat = el('div', 'tg-chat');
      const bubble = el('div', 'tg-message');
      if (p.message.image_id !== null) {
        const img = el('img', 'tg-photo');
        img.src = warmupImageUrl(p.message.image_id);
        img.alt = 'Картинка сообщения';
        bubble.append(img);
      }
      const text = el('div', 'tg-text');
      text.append(telegramFragment(p.message.html));
      bubble.append(text, el('span', 'tg-time', '12:00'));
      chat.append(bubble);
      if (p.message.buttons.length) {
        const kb = el('div', 'tg-keyboard');
        for (const line of buttonRows(p.message.buttons)) {
          const r = el('div', 'tg-kb-row');
          for (const b of line) r.append(el('span', `tg-kb-btn tg-kb-${b.kind}`, b.text));
          kb.append(r);
        }
        chat.append(kb);
      }
      previewBody.append(chat);
      const facts = el('ul', 'wu-preview-facts');
      facts.append(el('li', p.message.visible_length > p.message.max_length ? 'is-error' : '',
        `Длина: ${count(p.message.visible_length)} / ${count(p.message.max_length)}`));
      if (p.message.hidden_buttons) facts.append(el('li', 'is-warn', `Скрыто кнопок без URL: ${p.message.hidden_buttons}`));
      if (p.discount_code) facts.append(el('li', '', `Код скидки: ${p.discount_code}${p.discount_code_test ? ' (тестовый, не сохраняется)' : ''}`));
      previewBody.append(facts);
    };
    const runPreview = async () => {
      const { values, errors } = formValues(form, registry);
      clearErrors();
      if (!values) {
        showErrors(errors);
        preview = { kind: 'invalid' };
        paintPreview();
        return;
      }
      const request = ++previewRequest;
      preview = { kind: 'loading' };
      paintPreview();
      const snapshot = JSON.stringify(form);
      try {
        const p = base
          ? await this.host.api.previewScenario(base.id, { ...fullPatch(values), enabled: base.enabled }, PREVIEW_CONTEXT)
          : await this.host.api.previewDraft(scenarioInput(values), PREVIEW_CONTEXT);
        if (!isLive() || request !== previewRequest) return;
        if (p.image) { imageMeta.set(p.image.id, { size: p.image.size_bytes, mime: p.image.mime }); paintImage(); }
        previewedAt = snapshot;
        preview = { kind: 'data', preview: p, stale: JSON.stringify(form) !== snapshot };
      } catch (error) {
        if (!isLive() || request !== previewRequest) return;
        preview = { kind: 'error', error };
      }
      paintPreview();
    };
    previewButton.addEventListener('click', () => void runPreview());

    // Actions ----------------------------------------------------------------
    const actions = el('div', 'panel editor-actions wu-actions');
    const unsaved = el('p', 'unsaved-text');
    unsaved.setAttribute('aria-live', 'polite');
    const saveButton = this.#button(creating ? 'Создать сценарий' : 'Сохранить', 'btn btn-primary');
    saveButton.id = 'wu-save';
    const previewAgain = this.#button('Предпросмотр', 'btn btn-secondary', 'eye');
    previewAgain.addEventListener('click', () => void runPreview());
    actions.append(unsaved, previewAgain, saveButton);
    const paintDirty = () => {
      const isDirty = dirty();
      unsaved.textContent = saving ? 'Сохраняем…' : isDirty ? 'Есть несохранённые изменения' : creating ? 'Новый сценарий не сохранён' : 'Изменений нет';
      unsaved.classList.toggle('is-dirty', isDirty);
      saveButton.disabled = saving;
      saveButton.setAttribute('aria-busy', String(saving));
    };

    function changed() {
      paintHuman();
      paintCounter();
      paintVariables();
      paintDiscount();
      paintLimits();
      paintConditionsSummary();
      paintDirty();
      if (preview.kind === 'data') {
        preview = { ...preview, stale: JSON.stringify(form) !== previewedAt };
        paintPreview();
      }
    }

    // Plain inputs ------------------------------------------------------------
    const bind = (input: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, apply: (value: string) => void) => {
      input.addEventListener(input instanceof HTMLSelectElement ? 'change' : 'input', () => { apply(input.value); changed(); });
    };
    bind(name, v => { form.name = v; });
    bind(description, v => { form.description = v; });
    bind(category, v => { form.category = v; });
    bind(position, v => { form.position = v; });
    bind(triggerSelect, v => { form.trigger_type = v; fillTimings(); });
    bind(timing, v => { form.timing = v; });
    bind(delay, v => { form.delay_value = v; });
    bind(unit, v => { form.delay_unit = v; });
    bind(message, v => { form.message_html = v; });
    bind(percent, v => { form.discount_percent = v; });
    bind(ttl, v => { form.coupon_ttl_hours = v; });
    bind(template, v => { form.coupon_template = v; });
    bind(cooldown, v => { form.cooldown_hours = v; });
    bind(cap, v => { form.daily_cap = v; });
    keyInput.addEventListener('input', () => { setters.get('key')?.(''); paintDirty(); });

    // Save and status ---------------------------------------------------------
    const reloadAction = () => {
      const reload = this.#button('Обновить данные', 'btn btn-secondary btn-sm', 'refresh');
      reload.addEventListener('click', () => void (async () => {
        if (dirty() && !(await this.host.confirm('Обновить данные?', 'Несохранённые изменения в форме будут потеряны.', 'Обновить'))) return;
        this.host.setUnsaved(null);
        void this.#loadEditor(base?.id ?? null);
      })());
      return reload;
    };
    const rejected = (error: unknown) => {
      if (isCode(error, 'version_conflict')) {
        showStatus({ tone: 'error', text: SCENARIO_CONFLICT_TEXT, actions: [reloadAction()] });
        return;
      }
      if (isCode(error, 'duplicates_expiry_reminder')) {
        setters.get('delay_value')?.(REMINDER_CONFLICT_TEXT);
        showStatus({ tone: 'warn', text: REMINDER_CONFLICT_TEXT });
        return;
      }
      if (isCode(error, 'scenario_key_taken')) {
        setters.get('key')?.(KEY_TAKEN_TEXT);
        keyInput.focus();
        showStatus({ tone: 'error', text: KEY_TAKEN_TEXT });
        return;
      }
      if (error instanceof ApiError && error.field) {
        const serverField = error.field.replace(/\[\d+\]$/, '');
        const target = serverField === 'key' ? 'key' : SERVER_FIELDS[serverField];
        if (target) {
          setters.get(target)?.(reasonText(error.reason || 'format', error.detail));
          targets.get(target)?.focus();
        }
      }
      const retry = isUncertainText(error);
      showStatus({ tone: 'error', text: warmupErrorText(error) + retry });
    };
    const save = async () => {
      if (saving) return;
      clearErrors();
      const { values, errors } = formValues(form, registry);
      const key = keyInput.value.trim();
      const allErrors: Partial<Record<ScenarioField | 'key', string>> = { ...errors };
      if (creating && !SCENARIO_KEY_PATTERN.test(key)) allErrors.key = 'Латинские строчные буквы, цифры и _, от 3 до 64, первая — буква.';
      if (!values || allErrors.key) {
        showErrors(allErrors);
        showStatus({ tone: 'error', text: 'Исправьте отмеченные поля.' });
        return;
      }
      if (creating) {
        const input = scenarioInput(values);
        const requestKey = this.#keys.key('create', JSON.stringify({ key, input }));
        saving = true;
        paintDirty();
        try {
          const out = await this.host.api.createScenario(key, input, requestKey);
          this.#keys.settle('create');
          if (!isLive()) return;
          memory.created = { id: out.scenario.id, text: 'Сценарий создан. Он выключен и ничего не отправляет — включите его, когда будете готовы.' };
          this.host.setUnsaved(null);
          this.host.navigate(warmupHash({ view: 'scenario', scenarioId: out.scenario.id }));
        } catch (error) {
          this.#keys.settle('create', error);
          if (!isLive()) return;
          rejected(error);
        } finally {
          saving = false;
          if (isLive()) paintDirty();
        }
        return;
      }
      if (!base) return;
      const patch = changedPatch(base, values);
      if (!Object.keys(patch).length) {
        showStatus({ tone: 'info', text: 'Нет изменений для сохранения.' });
        return;
      }
      const requestKey = this.#keys.key('save', JSON.stringify({ id: base.id, version: base.version, patch }));
      saving = true;
      paintDirty();
      try {
        const out = await this.host.api.patchScenario(base.id, base.version, patch, requestKey);
        this.#keys.settle('save');
        if (!isLive()) return;
        const oldImage = base.image_id;
        if (oldImage !== null && oldImage !== out.scenario.image_id) void deleteQuietly(oldImage);
        saving = false;
        this.#paintEditor(overview, out.scenario, { status: { tone: 'success', text: out.replayed ? 'Сценарий сохранён (повтор подтверждён сервером).' : 'Сценарий сохранён.' } });
      } catch (error) {
        this.#keys.settle('save', error);
        if (!isLive()) return;
        saving = false;
        paintDirty();
        rejected(error);
      }
    };
    saveButton.addEventListener('click', () => void save());

    const onToggle = async () => {
      if (!base || toggling) return;
      const next = !base.enabled;
      if (next && !(await this.host.confirm('Включить сценарий?',
        `Сообщения начнут уходить подходящим пользователям${overview.settings.enabled ? '' : ' после глобального включения прогрева'}. ` +
        `События старше ${durationWords(overview.settings.catchup_hours)} не догоняются.`, 'Включить'))) return;
      if (!isLive() || !base) return;
      const scope = `enable:${base.id}`;
      const requestKey = this.#keys.key(scope, JSON.stringify({ version: base.version, enabled: next }));
      toggling = true;
      paintStatus();
      try {
        const out = await this.host.api.patchScenario(base.id, base.version, { enabled: next }, requestKey);
        this.#keys.settle(scope);
        if (!isLive()) return;
        // Only the status changed on the server: keep the edits in the form.
        toggling = false;
        this.#paintEditor(overview, out.scenario, {
          form, key: keyInput.value,
          status: {
            tone: 'success',
            text: out.scenario.enabled
              ? `Сценарий включён${out.scenario.enabled_at ? ` с ${formatDateTime(out.scenario.enabled_at)}` : ''}.`
              : 'Сценарий выключен.',
          },
        });
      } catch (error) {
        this.#keys.settle(scope, error);
        if (!isLive()) return;
        toggling = false;
        paintStatus();
        if (isCode(error, 'duplicates_expiry_reminder')) showStatus({ tone: 'warn', text: REMINDER_CONFLICT_TEXT });
        else rejected(error);
      }
    };

    // Layout -----------------------------------------------------------------
    const grid = el('div', 'editor-grid wu-editor-grid');
    const main = el('div', 'editor-main');
    main.append(basics.root, triggerPanel.root, messagePanel.root, imagePanel.root, buttonsPanel.root, conditionsPanel.root,
      discountPanel.root, limitsPanel.root);
    const side = el('div', 'editor-side wu-side');
    side.append(previewPanel.root);
    grid.append(main, side);
    this.body.replaceChildren(statusSlot, grid, actions);
    paintImage();
    paintButtons();
    paintPreview();
    changed();
    if (!creating || opts.form) void runPreview();
  }

  // ===========================================================================
  // Settings (#/warmup/settings)
  // ===========================================================================

  async #loadSettings() {
    const current = this.#begin();
    this.host.setUnsaved(null);
    this.body.replaceChildren(this.#tabs('settings'), ...this.#skeleton('Загружаем настройки прогрева', 1));
    try {
      const data = await this.host.api.overview();
      if (!current()) return;
      this.host.sessionUsed();
      this.#paintSettings(data, data.settings, null);
    } catch (error) {
      if (await this.#failed(error, current)) return;
      this.#ready();
      this.body.replaceChildren(this.#tabs('settings'), this.#errorPanel('Не удалось загрузить настройки', error, () => void this.#loadSettings()));
    }
  }

  #paintSettings(data: WarmupOverview, settings: WarmupSettings, status: Notice | null) {
    this.#ready();
    const limits = data.registry.limits;
    const baseline = settingsForm(settings);
    const form: SettingsForm = { ...baseline };
    let saving = false;
    const dirty = () => !sameSettingsForm(form, baseline);
    this.host.setUnsaved(() => this.#live && dirty());
    const statusSlot = el('div', 'editor-status');
    const showStatus = (next: Notice | null) => statusSlot.replaceChildren(...(next ? [this.#notice(next)] : []));
    showStatus(status);

    const panel = this.#panel('wu-settings-title', 'Настройки прогрева', `версия ${settings.version} · изменены ${formatDateTime(settings.updated_at)}`);
    const enabled = checkbox('wu-set-enabled', 'Прогрев включён глобально');
    enabled.input.checked = form.enabled;
    enabled.input.setAttribute('role', 'switch');
    const setters = new Map<SettingsField, Field>();
    const number = (key: SettingsField, label: string, hint: string, suffix: string) => {
      const input = textInput(`wu-set-${key}`, 'numeric');
      input.value = form[key];
      const f = field(label, input, hint, suffix);
      setters.set(key, f);
      input.addEventListener('input', () => { form[key] = input.value; f.setError(''); refresh(); });
      return { input, f };
    };
    const max = number('max_per_day', 'Максимум сообщений за 24 часа', `Сколько сообщений прогрева один пользователь получит за 24 часа: от 1 до ${limits.max_per_day_limit}.`, 'сообщений');
    const cooldown = number('cooldown_hours', 'Cooldown', '', 'часов');
    const catchup = number('catchup_hours', 'Catch-up window', '', 'часов');
    const explain = el('p', 'wu-human');
    explain.setAttribute('aria-live', 'polite');
    panel.body.append(enabled.root,
      el('p', 'field-hint', 'Выключение останавливает все отправки; сценарии и их настройки сохраняются.'),
      max.f.root, row(cooldown.f.root, catchup.f.root), explain,
      el('p', 'field-hint', `Контактов прогрева: ${count(data.summary.contacts)}, заблокировали бота: ${count(data.summary.blocked_contacts)}.`));
    enabled.input.addEventListener('change', () => { form.enabled = enabled.input.checked; refresh(); });

    const actions = el('div', 'panel editor-actions wu-actions');
    const unsaved = el('p', 'unsaved-text');
    unsaved.setAttribute('aria-live', 'polite');
    const saveButton = this.#button('Сохранить', 'btn btn-primary');
    saveButton.id = 'wu-settings-save';
    actions.append(unsaved, saveButton);
    const words = (raw: string) => (/^\d{1,6}$/.test(raw.trim()) ? durationWords(Number(raw.trim())) : '');
    function refresh() {
      cooldown.f.hint.textContent = `${words(form.cooldown_hours) ? `= ${words(form.cooldown_hours)}. ` : ''}Минимальный интервал между сообщениями одному пользователю: от 0 до ${limits.max_cooldown_hours} часов.`;
      catchup.f.hint.textContent = `${words(form.catchup_hours) ? `${words(form.catchup_hours)}. ` : ''}События старше этого окна не догоняются после включения. От 1 до ${limits.max_catchup_hours} часов.`;
      explain.textContent = form.enabled ? 'Прогрев будет отправлять сообщения по включённым сценариям.' : 'Прогрев будет выключен: ничего не отправляется.';
      unsaved.textContent = saving ? 'Сохраняем…' : dirty() ? 'Есть несохранённые изменения' : 'Изменений нет';
      unsaved.classList.toggle('is-dirty', dirty());
      saveButton.disabled = saving;
    }
    refresh();

    const save = async () => {
      if (saving) return;
      for (const f of setters.values()) f.setError('');
      const { save: body, errors } = settingsSave(form, settings.version, limits);
      if (!body) {
        let first: HTMLElement | null = null;
        for (const [key, text] of Object.entries(errors) as [SettingsField, string][]) {
          setters.get(key)?.setError(text);
          first ??= document.getElementById(`wu-set-${key}`);
        }
        first?.focus();
        showStatus({ tone: 'error', text: 'Исправьте отмеченные поля.' });
        return;
      }
      if (sameSettingsForm(form, baseline)) { showStatus({ tone: 'info', text: 'Нет изменений для сохранения.' }); return; }
      if (body.enabled && !settings.enabled && !(await this.host.confirm('Включить прогрев?',
        `Сообщения начнут уходить по включённым сценариям (${count(data.summary.enabled_scenarios)}).`, 'Включить'))) return;
      if (!this.#live) return;
      const requestKey = this.#keys.key('settings', JSON.stringify(body));
      saving = true;
      refresh();
      try {
        const out = await this.host.api.saveSettings(body, requestKey);
        this.#keys.settle('settings');
        if (!this.#live) return;
        this.#paintSettings({ ...data, settings: out.settings }, out.settings, { tone: 'success', text: 'Настройки сохранены.' });
      } catch (error) {
        this.#keys.settle('settings', error);
        if (!this.#live) return;
        saving = false;
        refresh();
        if (isCode(error, 'version_conflict')) {
          const reload = this.#button('Обновить данные', 'btn btn-secondary btn-sm', 'refresh');
          reload.addEventListener('click', () => void (async () => {
            if (dirty() && !(await this.host.confirm('Обновить данные?', 'Несохранённые изменения будут потеряны.', 'Обновить'))) return;
            try {
              const latest = await this.host.api.settings();
              if (this.#live) this.#paintSettings({ ...data, settings: latest }, latest, { tone: 'info', text: 'Загружены актуальные настройки.' });
            } catch (e) {
              if (this.#live) showStatus({ tone: 'error', text: warmupErrorText(e, SETTINGS_CONFLICT_TEXT) });
            }
          })());
          showStatus({ tone: 'error', text: SETTINGS_CONFLICT_TEXT, actions: [reload] });
          return;
        }
        if (error instanceof ApiError && error.field && setters.has(error.field as SettingsField)) {
          setters.get(error.field as SettingsField)?.setError(reasonText(error.reason || 'out_of_range'));
        }
        showStatus({ tone: 'error', text: warmupErrorText(error, SETTINGS_CONFLICT_TEXT) + isUncertainText(error) });
      }
    };
    saveButton.addEventListener('click', () => void save());
    this.body.replaceChildren(this.#tabs('settings'), statusSlot, panel.root, actions);
  }

  // ===========================================================================
  // Delivery history (#/warmup/deliveries)
  // ===========================================================================

  #scenarioCache: readonly WarmupScenario[] | null = null;

  async #loadDeliveries(focus: 'prev' | 'next' | null) {
    const current = this.#begin();
    const query = memory.deliveries;
    if (!this.body.childElementCount) {
      const refresh = this.#button('Обновить', 'btn btn-secondary btn-sm', 'refresh');
      refresh.addEventListener('click', () => void this.#loadDeliveries(null));
      this.#headerActions(refresh);
      this.body.replaceChildren(this.#tabs('deliveries'), ...this.#skeleton('Загружаем историю отправок', 1));
    } else {
      this.body.querySelector('.wu-results')?.setAttribute('aria-busy', 'true');
    }
    try {
      const [scenarios, page] = await Promise.all([this.#scenarioCache ?? this.host.api.scenarios(), this.host.api.deliveries(query)]);
      if (!current()) return;
      this.host.sessionUsed();
      this.#scenarioCache = scenarios;
      // The list shrank below the requested page: step back to its last page.
      const last = page.total > 0 ? Math.floor((page.total - 1) / page.limit) * page.limit : 0;
      if (!page.deliveries.length && query.offset > last) {
        memory.deliveries = { ...query, offset: last };
        void this.#loadDeliveries(focus);
        return;
      }
      this.#paintDeliveries(scenarios, page, focus);
    } catch (error) {
      if (await this.#failed(error, current)) return;
      this.#ready();
      this.body.replaceChildren(this.#tabs('deliveries'), this.#errorPanel('Не удалось загрузить историю отправок', error, () => void this.#loadDeliveries(null)));
    }
  }

  #paintDeliveries(scenarios: readonly WarmupScenario[], page: WarmupDeliveryPage, focus: 'prev' | 'next' | null) {
    this.#ready();
    const q = memory.deliveries;
    const byId = new Map(scenarios.map(s => [s.id, s]));
    const form = el('form', 'toolbar wu-filters');
    form.noValidate = true;
    form.setAttribute('aria-label', 'Фильтры истории отправок');
    const scenario = select('wu-f-scenario', [['', 'Все сценарии'], ...scenarios.map(s => [String(s.id), s.name] as const)]);
    scenario.value = q.scenarioId === null ? '' : String(q.scenarioId);
    const category = select('wu-f-category', [['', 'Все категории'], ...WARMUP_CATEGORIES.map(c => [c, CATEGORY_LABELS[c] ?? c] as const)]);
    category.value = q.category;
    const status = select('wu-f-status', [['', 'Все статусы'], ...WARMUP_DELIVERY_STATUSES.map(s => [s, DELIVERY_STATUS_LABELS[s] ?? s] as const)]);
    status.value = q.status;
    const telegram = textInput('wu-f-telegram', 'numeric');
    telegram.placeholder = 'Telegram ID';
    telegram.value = q.telegramId;
    const from = el('input', 'input date-input');
    from.type = 'date';
    from.id = 'wu-f-from';
    from.value = memory.deliveryDates.from;
    const to = el('input', 'input date-input');
    to.type = 'date';
    to.id = 'wu-f-to';
    to.value = memory.deliveryDates.to;
    const coupon = textInput('wu-f-coupon');
    coupon.placeholder = 'Купон';
    coupon.maxLength = 64;
    coupon.value = q.coupon;
    const apply = el('button', 'btn btn-primary btn-sm');
    apply.type = 'submit';
    apply.append(el('span', 'btn-label', 'Применить'));
    const reset = this.#button('Сбросить', 'btn btn-ghost btn-sm');
    const filterError = el('p', 'field-error');
    filterError.setAttribute('role', 'alert');
    const labelled = (label: string, control: HTMLElement & { id: string }) => {
      const wrap = el('div', 'wu-filter');
      const caption = el('label', 'wu-filter-label', label);
      caption.htmlFor = control.id;
      wrap.append(caption, control);
      return wrap;
    };
    form.append(labelled('Сценарий', scenario), labelled('Категория', category), labelled('Статус', status), labelled('Пользователь', telegram),
      labelled('С', from), labelled('По', to), labelled('Купон', coupon), apply, reset);
    form.addEventListener('submit', event => {
      event.preventDefault();
      const id = telegram.value.trim();
      if (id && !/^[1-9]\d{0,18}$/.test(id)) { filterError.textContent = 'Telegram ID — положительное целое число.'; telegram.focus(); return; }
      if (from.value && to.value && to.value < from.value) { filterError.textContent = 'Дата «по» раньше даты «с».'; to.focus(); return; }
      memory.deliveryDates = { from: from.value, to: to.value };
      memory.deliveries = {
        scenarioId: scenario.value ? Number(scenario.value) : null,
        category: (WARMUP_CATEGORIES as readonly string[]).includes(category.value) ? category.value as WarmupDeliveryQuery['category'] : '',
        status: (WARMUP_DELIVERY_STATUSES as readonly string[]).includes(status.value) ? status.value as WarmupDeliveryQuery['status'] : '',
        telegramId: id, from: dayStartIso(from.value), to: dayStartIso(to.value, 1), coupon: coupon.value.trim(), offset: 0,
      };
      void this.#loadDeliveries(null);
    });
    reset.addEventListener('click', () => {
      memory.deliveries = EMPTY_DELIVERY_QUERY;
      memory.deliveryDates = { from: '', to: '' };
      void this.#loadDeliveries(null);
    });

    const results = el('div', 'wu-results');
    const filtered = q.scenarioId !== null || q.category !== '' || q.status !== '' || q.telegramId !== '' || q.from !== '' || q.to !== '' || q.coupon !== '';
    if (!page.deliveries.length) {
      results.append(this.#emptyPanel(filtered ? 'Нет отправок по этим условиям' : 'Отправок пока не было',
        filtered ? 'Измените или сбросьте фильтры.' : 'Сообщения появятся здесь, когда включённые сценарии начнут отправку.'));
    } else {
      const panel = el('section', 'panel');
      panel.setAttribute('aria-label', 'История отправок');
      const wrap = el('div', 'table-wrap');
      const table = el('table', 'table table-cards wu-table');
      table.append(head(['Дата', 'Пользователь', 'Сценарий', 'Категория', 'Статус', 'Ошибка', 'Купон']));
      const tbody = el('tbody');
      for (const d of page.deliveries) tbody.append(this.#deliveryRow(d, byId.get(d.scenario_id) ?? null));
      table.append(tbody);
      wrap.append(table);
      panel.append(wrap, this.#pager(page.total, page.limit, page.offset, page.deliveries.length, (offset, dir) => {
        memory.deliveries = { ...memory.deliveries, offset };
        void this.#loadDeliveries(dir);
      }));
      results.append(panel);
    }
    this.body.replaceChildren(this.#tabs('deliveries'), form, filterError,
      el('p', 'toolbar-meta', `Найдено: ${count(page.total)}`), results);
    if (focus) this.body.querySelector<HTMLElement>(`[data-pager='${focus}']:not(:disabled)`)?.focus();
  }

  #deliveryRow(d: WarmupDelivery, sc: WarmupScenario | null): HTMLTableRowElement {
    const tr = el('tr');
    const date = el('span', '', formatDateTime(d.created_at));
    const dateCell = cell('Дата', date);
    if (d.sent_at) dateCell.append(el('span', 'cell-note', `отправлено ${formatDateTime(d.sent_at)}`));
    else if (d.status === 'retry') dateCell.append(el('span', 'cell-note', `повтор ${formatDateTime(d.due_at)}`));
    const userCell = cell('Пользователь', userLabel(d.username, d.telegram_id));
    if (d.username) userCell.append(el('span', 'cell-note', String(d.telegram_id)));
    const scenarioCell = cell('Сценарий', d.scenario_name || (sc?.name ?? `#${d.scenario_id}`));
    const categoryCell = cell('Категория', sc ? CATEGORY_LABELS[sc.category] ?? sc.category : '—');
    const status = el('span', `wu-status wu-status-${d.status}`, DELIVERY_STATUS_LABELS[d.status] ?? d.status);
    const statusCell = cell('Статус', status);
    if (d.attempts > 1) statusCell.append(el('span', 'cell-note', `попыток: ${d.attempts}`));
    const errorCell = cell('Ошибка', d.error ? truncate(d.error, 120) : '—', d.error ? 'cell-wrap error-text' : 'muted');
    if (d.error) errorCell.title = d.error;
    tr.append(dateCell, userCell, scenarioCell, categoryCell, statusCell, errorCell, cell('Купон', d.coupon_code ?? '—', d.coupon_code ? 'wu-mono' : 'muted'));
    return tr;
  }

  #pager(total: number, limit: number, offset: number, shown: number, go: (offset: number, dir: 'prev' | 'next') => void): HTMLElement {
    const foot = el('div', 'pager');
    foot.append(el('p', 'pager-range', shown ? `С ${count(offset + 1)} по ${count(offset + shown)} из ${count(total)}` : `Всего ${count(total)}`));
    const pages = Math.ceil(total / limit);
    if (pages <= 1) return foot;
    const nav = el('nav', 'pager-nav');
    nav.setAttribute('aria-label', 'Страницы списка');
    const prev = this.#button('Предыдущая', 'btn btn-secondary btn-sm', 'prev');
    prev.dataset.pager = 'prev';
    const next = this.#button('Следующая', 'btn btn-secondary btn-sm');
    next.append(this.host.icon('next'));
    next.dataset.pager = 'next';
    prev.disabled = offset === 0;
    next.disabled = offset + limit >= total;
    prev.addEventListener('click', () => go(Math.max(0, offset - limit), 'prev'));
    next.addEventListener('click', () => go(offset + limit, 'next'));
    nav.append(el('p', 'pager-page', `Страница ${count(Math.floor(offset / limit) + 1)} из ${count(pages)}`), prev, next);
    foot.append(nav);
    return foot;
  }

  // ===========================================================================
  // Coupons (#/warmup/coupons)
  // ===========================================================================

  async #loadCoupons(focus: 'prev' | 'next' | null) {
    const current = this.#begin();
    const query = memory.coupons;
    if (!this.body.childElementCount) {
      const refresh = this.#button('Обновить', 'btn btn-secondary btn-sm', 'refresh');
      refresh.addEventListener('click', () => void this.#loadCoupons(null));
      this.#headerActions(refresh);
      this.body.replaceChildren(this.#tabs('coupons'), ...this.#skeleton('Загружаем купоны', 1));
    } else {
      this.body.querySelector('.wu-results')?.setAttribute('aria-busy', 'true');
    }
    try {
      const [scenarios, page] = await Promise.all([this.#scenarioCache ?? this.host.api.scenarios(), this.host.api.coupons(query)]);
      if (!current()) return;
      this.host.sessionUsed();
      this.#scenarioCache = scenarios;
      const last = page.total > 0 ? Math.floor((page.total - 1) / page.limit) * page.limit : 0;
      if (!page.coupons.length && query.offset > last) {
        memory.coupons = { ...query, offset: last };
        void this.#loadCoupons(focus);
        return;
      }
      this.#paintCoupons(scenarios, page, focus);
    } catch (error) {
      if (await this.#failed(error, current)) return;
      this.#ready();
      this.body.replaceChildren(this.#tabs('coupons'), this.#errorPanel('Не удалось загрузить купоны', error, () => void this.#loadCoupons(null)));
    }
  }

  #paintCoupons(scenarios: readonly WarmupScenario[], page: WarmupCouponPage, focus: 'prev' | 'next' | null) {
    this.#ready();
    const q = memory.coupons;
    const byId = new Map(scenarios.map(s => [s.id, s]));
    const form = el('form', 'toolbar wu-filters');
    form.noValidate = true;
    form.setAttribute('aria-label', 'Фильтры купонов');
    const scenario = select('wu-c-scenario', [['', 'Все сценарии'], ...scenarios.map(s => [String(s.id), s.name] as const)]);
    scenario.value = q.scenarioId === null ? '' : String(q.scenarioId);
    const status = select('wu-c-status', [['', 'Все статусы'], ...WARMUP_COUPON_STATUSES.map(s => [s, COUPON_STATUS_LABELS[s] ?? s] as const)]);
    status.value = q.status;
    const telegram = textInput('wu-c-telegram', 'numeric');
    telegram.placeholder = 'Telegram ID';
    telegram.value = q.telegramId;
    const code = textInput('wu-c-code');
    code.placeholder = 'Код';
    code.maxLength = 64;
    code.value = q.code;
    const apply = el('button', 'btn btn-primary btn-sm');
    apply.type = 'submit';
    apply.append(el('span', 'btn-label', 'Применить'));
    const reset = this.#button('Сбросить', 'btn btn-ghost btn-sm');
    const filterError = el('p', 'field-error');
    filterError.setAttribute('role', 'alert');
    const labelled = (label: string, control: HTMLElement & { id: string }) => {
      const wrap = el('div', 'wu-filter');
      const caption = el('label', 'wu-filter-label', label);
      caption.htmlFor = control.id;
      wrap.append(caption, control);
      return wrap;
    };
    form.append(labelled('Сценарий', scenario), labelled('Статус', status), labelled('Пользователь', telegram), labelled('Код', code), apply, reset);
    form.addEventListener('submit', event => {
      event.preventDefault();
      const id = telegram.value.trim();
      if (id && !/^[1-9]\d{0,18}$/.test(id)) { filterError.textContent = 'Telegram ID — положительное целое число.'; telegram.focus(); return; }
      memory.coupons = {
        scenarioId: scenario.value ? Number(scenario.value) : null,
        status: (WARMUP_COUPON_STATUSES as readonly string[]).includes(status.value) ? status.value as WarmupCouponQuery['status'] : '',
        telegramId: id, code: code.value.trim(), offset: 0,
      };
      void this.#loadCoupons(null);
    });
    reset.addEventListener('click', () => { memory.coupons = EMPTY_COUPON_QUERY; void this.#loadCoupons(null); });

    const results = el('div', 'wu-results');
    const filtered = q.scenarioId !== null || q.status !== '' || q.telegramId !== '' || q.code !== '';
    if (!page.coupons.length) {
      results.append(this.#emptyPanel(filtered ? 'Нет купонов по этим условиям' : 'Купонов пока нет',
        filtered ? 'Измените или сбросьте фильтры.' : 'Купоны создаются при отправке сценариев со скидкой.'));
    } else {
      const panel = el('section', 'panel');
      panel.setAttribute('aria-label', 'Купоны');
      const wrap = el('div', 'table-wrap');
      const table = el('table', 'table table-cards wu-table');
      table.append(head(['Код', 'Telegram ID', 'Скидка', 'Создан', 'Истекает', 'Статус', 'Использован', 'Заказ']));
      const tbody = el('tbody');
      for (const c of page.coupons) {
        const tr = el('tr');
        const codeCell = cell('Код', c.code, 'wu-mono');
        const sc = byId.get(c.scenario_id);
        if (sc) codeCell.append(el('span', 'cell-note', sc.name));
        const statusName = c.effective_status || c.status;
        tr.append(codeCell, cell('Telegram ID', String(c.telegram_id)), cell('Скидка', `${c.percent}%`),
          cell('Создан', formatDateTime(c.created_at)), cell('Истекает', formatDateTime(c.expires_at)),
          cell('Статус', el('span', `wu-status wu-coupon-${statusName}`, COUPON_STATUS_LABELS[statusName] ?? statusName)),
          cell('Использован', c.redeemed_at ? formatDateTime(c.redeemed_at) : '—', c.redeemed_at ? '' : 'muted'),
          cell('Заказ', c.order_id !== null ? `#${c.order_id}` : '—', c.order_id !== null ? '' : 'muted'));
        tbody.append(tr);
      }
      table.append(tbody);
      wrap.append(table);
      panel.append(wrap, this.#pager(page.total, page.limit, page.offset, page.coupons.length, (offset, dir) => {
        memory.coupons = { ...memory.coupons, offset };
        void this.#loadCoupons(dir);
      }));
      results.append(panel);
    }
    this.body.replaceChildren(this.#tabs('coupons'), form, filterError,
      this.#notice({ tone: 'info', text: 'Купон пока не применяется автоматически при оплате: «Использован» и «Заказ» остаются пустыми.' }),
      el('p', 'toolbar-meta', `Найдено: ${count(page.total)}`), results);
    if (focus) this.body.querySelector<HTMLElement>(`[data-pager='${focus}']:not(:disabled)`)?.focus();
  }
}

// =============================================================================
// Table helpers
// =============================================================================

function head(columns: readonly string[]): HTMLTableSectionElement {
  const thead = el('thead');
  const tr = el('tr');
  for (const text of columns) {
    const th = el('th', '', text);
    th.scope = 'col';
    tr.append(th);
  }
  thead.append(tr);
  return thead;
}

/** A cell that carries its column name for the stacked narrow layout. */
function cell(label: string, content: string | Node, className = ''): HTMLTableCellElement {
  const td = el('td', className);
  td.dataset.label = label;
  td.append(content);
  return td;
}

/** The hint after an uncertain failure: a retry is a replay, never a duplicate. */
function isUncertainText(error: unknown): string {
  if (error instanceof ApiError && !['network', 'timeout', 'service_unavailable'].includes(error.code) && error.status < 500) return '';
  return ' Повторите действие: запрос уйдёт с тем же ключом, дубля не будет.';
}
