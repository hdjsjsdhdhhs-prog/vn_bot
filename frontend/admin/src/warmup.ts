// Customer warm-up (Admin → «Прогрев клиентов»): wire types, the API client,
// error texts and the pure logic of the editor.
//
// Backend contract: internal/web/admin_warmup_api.go → service.WarmupService.
// The backend is the source of truth for every rule: trigger types, timings,
// variables, callback actions and condition keys come from the registry in the
// overview (service.WarmupRegistry); validation here only catches obvious
// input mistakes early, the server re-validates every save and preview.
// Event timing (enabled_at, the catch-up window) is never computed here.

import { ApiError, errorText, newRequestKey, type AdminApi } from './api';

// =============================================================================
// Wire types
// =============================================================================

export const WARMUP_CATEGORIES = ['trial', 'subscription', 'winback'] as const;
export type WarmupCategoryKey = (typeof WARMUP_CATEGORIES)[number];

export const WARMUP_DELAY_UNITS = ['minutes', 'hours', 'days'] as const;
export type WarmupDelayUnit = (typeof WARMUP_DELAY_UNITS)[number];

export const WARMUP_BUTTON_KINDS = ['url', 'web_app', 'callback'] as const;
export type WarmupButtonKind = (typeof WARMUP_BUTTON_KINDS)[number];

export const WARMUP_DELIVERY_STATUSES = ['sending', 'sent', 'retry', 'failed', 'blocked'] as const;
export type WarmupDeliveryStatus = (typeof WARMUP_DELIVERY_STATUSES)[number];

/** Effective coupon statuses accepted by the coupons filter. */
export const WARMUP_COUPON_STATUSES = ['issued', 'expired', 'revoked', 'redeemed'] as const;
export type WarmupCouponStatus = (typeof WARMUP_COUPON_STATUSES)[number];

/** database.WarmupSettings. */
export interface WarmupSettings {
  readonly enabled: boolean;
  readonly max_per_day: number;
  readonly cooldown_hours: number;
  readonly catchup_hours: number;
  readonly version: number;
  readonly updated_at: string;
}

/** service.WarmupButton; same_row puts the button next to the previous one. */
export interface WarmupButton {
  readonly text: string;
  readonly kind: string;
  readonly value: string;
  readonly same_row?: boolean;
}

/** service.WarmupConditions: an absent flag is "any", empty languages any language. */
export interface WarmupConditions {
  readonly had_trial?: boolean;
  readonly has_subscription?: boolean;
  readonly has_active_paid?: boolean;
  readonly ever_paid?: boolean;
  readonly connected?: boolean;
  readonly languages?: readonly string[];
}

export const WARMUP_FLAG_CONDITIONS = ['had_trial', 'has_subscription', 'has_active_paid', 'ever_paid', 'connected'] as const;
export type WarmupFlagCondition = (typeof WARMUP_FLAG_CONDITIONS)[number];

/** service.WarmupScenarioView. */
export interface WarmupScenario {
  readonly id: number;
  readonly key: string;
  readonly category: string;
  readonly name: string;
  readonly description: string;
  readonly trigger_type: string;
  readonly timing: string;
  readonly delay_value: number;
  readonly delay_unit: string;
  readonly enabled: boolean;
  /** Set by the server when the scenario is switched on or its timing changes. */
  readonly enabled_at: string | null;
  readonly message_html: string;
  readonly image_id: number | null;
  /** null: the global cooldown applies. */
  readonly cooldown_hours: number | null;
  /** 0: no daily cap. */
  readonly daily_cap: number;
  readonly discount_percent: number;
  readonly coupon_template: string;
  readonly coupon_ttl_hours: number;
  readonly position: number;
  readonly version: number;
  readonly created_at: string;
  readonly updated_at: string;
  readonly buttons: readonly WarmupButton[];
  readonly conditions: WarmupConditions;
}

export interface WarmupCategory {
  readonly key: string;
  readonly label: string;
  readonly scenarios: number;
  readonly enabled: number;
}

export interface WarmupTrigger {
  readonly type: string;
  readonly label: string;
  readonly description: string;
  readonly timings: readonly string[];
}

export interface WarmupVariable {
  readonly name: string;
  readonly label: string;
  readonly description: string;
  readonly sample: string;
  /** null: available for every trigger. */
  readonly triggers: readonly string[] | null;
  /** Needs a discount (discount_percent > 0). */
  readonly discount: boolean;
}

export interface WarmupCallbackAction {
  readonly data: string;
  readonly label: string;
}

export interface WarmupConditionKey {
  readonly key: string;
  readonly label: string;
  readonly description: string;
  /** bool | languages */
  readonly kind: string;
}

export interface WarmupLimits {
  readonly image_max_bytes: number;
  readonly max_buttons: number;
  readonly max_text_length: number;
  readonly max_caption_length: number;
  readonly max_cooldown_hours: number;
  readonly max_daily_cap: number;
  readonly max_coupon_ttl_hours: number;
  readonly max_per_day_limit: number;
  readonly max_catchup_hours: number;
}

export interface WarmupRegistry {
  readonly triggers: readonly WarmupTrigger[];
  readonly variables: readonly WarmupVariable[];
  readonly callbacks: readonly WarmupCallbackAction[];
  readonly conditions: readonly WarmupConditionKey[];
  readonly limits: WarmupLimits;
}

export interface WarmupScenarioStat {
  readonly scenario_id: number;
  readonly sent: number;
  readonly last_sent_at: string | null;
}

/** database.WarmupSummary. last_24h counts deliveries by status. */
export interface WarmupSummary {
  readonly scenarios: number;
  readonly enabled_scenarios: number;
  readonly last_24h: Readonly<Record<string, number>>;
  readonly sent_today: number;
  readonly sent_7d: number;
  readonly active_coupons: number;
  readonly coupons_created: number;
  readonly contacts: number;
  readonly blocked_contacts: number;
  readonly scenario_stats: readonly WarmupScenarioStat[];
}

export interface WarmupOverview {
  readonly settings: WarmupSettings;
  readonly categories: readonly WarmupCategory[];
  readonly scenarios: readonly WarmupScenario[];
  readonly summary: WarmupSummary;
  readonly registry: WarmupRegistry;
}

/** database.WarmupImage (never the bytes). */
export interface WarmupImage {
  readonly id: number;
  readonly mime: string;
  readonly size_bytes: number;
  readonly sha256: string;
  readonly created_at: string;
}

export interface WarmupProblem {
  readonly field: string;
  readonly code: string;
  readonly detail: string;
  /** invalid: blocks saving and sending; warning: shown only. */
  readonly severity: string;
}

/** service.WarmupMessage: the message the worker would send. */
export interface WarmupMessage {
  readonly html: string;
  readonly image_id: number | null;
  /** Visible buttons only (URL buttons without a URL are dropped). */
  readonly buttons: readonly WarmupButton[];
  readonly hidden_buttons: number;
  readonly visible_length: number;
  readonly max_length: number;
}

/** service.WarmupPreview. Nothing is sent, stored or journaled by a preview. */
export interface WarmupPreview {
  readonly valid: boolean;
  readonly problems: readonly WarmupProblem[];
  readonly message: WarmupMessage | null;
  readonly image: WarmupImage | null;
  readonly variables: Readonly<Record<string, string>>;
  readonly discount_code: string;
  readonly discount_code_test: boolean;
}

/** service.WarmupPreviewContext: an example recipient, never a real user. */
export interface WarmupPreviewContext {
  readonly telegram_id?: number;
  readonly username?: string;
  readonly plan_name?: string;
  readonly expire_date?: string;
}

export interface WarmupScenarioOutcome {
  readonly scenario: WarmupScenario;
  readonly replayed: boolean;
}

export interface WarmupSettingsOutcome {
  readonly settings: WarmupSettings;
  readonly replayed: boolean;
}

export interface WarmupImageOutcome {
  readonly image: WarmupImage | null;
  readonly target_id: number;
  readonly replayed: boolean;
}

/** database.WarmupDeliveryRow. */
export interface WarmupDelivery {
  readonly id: number;
  readonly scenario_id: number;
  readonly scenario_name: string;
  readonly telegram_id: number;
  readonly username: string;
  readonly status: string;
  readonly attempts: number;
  readonly error: string;
  readonly coupon_code: string | null;
  readonly created_at: string;
  readonly due_at: string;
  readonly sent_at: string | null;
}

export interface WarmupDeliveryPage {
  readonly deliveries: readonly WarmupDelivery[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

/** service.WarmupCouponRow. redeemed_at/order_id are not connected to payments yet. */
export interface WarmupCoupon {
  readonly id: number;
  readonly code: string;
  readonly telegram_id: number;
  readonly scenario_id: number;
  readonly percent: number;
  readonly status: string;
  readonly effective_status: string;
  readonly created_at: string;
  readonly expires_at: string;
  readonly redeemed_at: string | null;
  readonly order_id: number | null;
}

export interface WarmupCouponPage {
  readonly coupons: readonly WarmupCoupon[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

/** Query of GET /admin/api/warmup/deliveries; from/to are RFC 3339 or ''. */
export interface WarmupDeliveryQuery {
  readonly scenarioId: number | null;
  readonly category: WarmupCategoryKey | '';
  readonly status: WarmupDeliveryStatus | '';
  readonly telegramId: string;
  readonly from: string;
  readonly to: string;
  readonly coupon: string;
  readonly offset: number;
}

export interface WarmupCouponQuery {
  readonly scenarioId: number | null;
  readonly status: WarmupCouponStatus | '';
  readonly telegramId: string;
  readonly code: string;
  readonly offset: number;
}

export const EMPTY_DELIVERY_QUERY: WarmupDeliveryQuery = {
  scenarioId: null, category: '', status: '', telegramId: '', from: '', to: '', coupon: '', offset: 0,
};
export const EMPTY_COUPON_QUERY: WarmupCouponQuery = { scenarioId: null, status: '', telegramId: '', code: '', offset: 0 };

/**
 * Partial scenario edit (service.WarmupScenarioPatch): absent fields keep the
 * stored value; image_id and cooldown_hours accept an explicit null.
 */
export interface WarmupScenarioPatch {
  category?: string;
  name?: string;
  description?: string;
  trigger_type?: string;
  timing?: string;
  delay_value?: number;
  delay_unit?: string;
  enabled?: boolean;
  message_html?: string;
  image_id?: number | null;
  buttons?: WarmupButton[];
  conditions?: WarmupConditions;
  cooldown_hours?: number | null;
  daily_cap?: number;
  discount_percent?: number;
  coupon_template?: string;
  coupon_ttl_hours?: number;
  position?: number;
}

/**
 * Full scenario payload (service.WarmupScenarioInput): POST /scenarios (with
 * request_key and key) and the draft preview POST /preview (with context).
 */
export interface WarmupScenarioInput {
  readonly category: string;
  readonly name: string;
  readonly description: string;
  readonly trigger_type: string;
  readonly timing: string;
  readonly delay_value: number;
  readonly delay_unit: string;
  readonly enabled: boolean;
  readonly message_html: string;
  readonly image_id: number | null;
  readonly buttons: readonly WarmupButton[];
  readonly conditions: WarmupConditions;
  readonly cooldown_hours: number | null;
  readonly daily_cap: number;
  readonly discount_percent: number;
  readonly coupon_template: string;
  readonly coupon_ttl_hours: number;
  readonly position: number;
}

export interface WarmupSettingsSave {
  readonly version: number;
  readonly enabled: boolean;
  readonly max_per_day: number;
  readonly cooldown_hours: number;
  readonly catchup_hours: number;
}

// =============================================================================
// Parsing (a malformed response is ApiError invalid_response)
// =============================================================================

type Wire = Record<string, unknown>;

const isRecord = (v: unknown): v is Wire => typeof v === 'object' && v !== null && !Array.isArray(v);
const isInt = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v);
const isCount = (v: unknown): v is number => isInt(v) && v >= 0;
const isId = (v: unknown): v is number => isInt(v) && v > 0;
const isText = (v: unknown): v is string => typeof v === 'string';
const isBool = (v: unknown): v is boolean => typeof v === 'boolean';
const isTime = (v: unknown): v is string => typeof v === 'string' && v.length <= 40 && !Number.isNaN(Date.parse(v));
const isNullTime = (v: unknown): v is string | null => v === null || v === undefined || isTime(v);
const isTextList = (v: unknown): v is string[] => Array.isArray(v) && v.every(isText);

function invalid(): never {
  throw new ApiError('invalid_response');
}

function parseSettings(v: unknown): WarmupSettings {
  if (!isRecord(v) || !isBool(v.enabled) || !isCount(v.max_per_day) || !isCount(v.cooldown_hours) || !isCount(v.catchup_hours) ||
    !isId(v.version) || !isTime(v.updated_at)) invalid();
  return {
    enabled: v.enabled, max_per_day: v.max_per_day, cooldown_hours: v.cooldown_hours, catchup_hours: v.catchup_hours,
    version: v.version, updated_at: v.updated_at,
  };
}

function parseButton(v: unknown): WarmupButton {
  if (!isRecord(v) || !isText(v.text) || !isText(v.kind) || !isText(v.value) || (v.same_row !== undefined && !isBool(v.same_row))) invalid();
  return v.same_row ? { text: v.text, kind: v.kind, value: v.value, same_row: true } : { text: v.text, kind: v.kind, value: v.value };
}

function parseConditions(v: unknown): WarmupConditions {
  if (v === null || v === undefined) return {};
  if (!isRecord(v)) invalid();
  const out: { -readonly [K in keyof WarmupConditions]: WarmupConditions[K] } = {};
  for (const key of WARMUP_FLAG_CONDITIONS) {
    const flag = v[key];
    if (flag === undefined || flag === null) continue;
    if (!isBool(flag)) invalid();
    out[key] = flag;
  }
  if (v.languages !== undefined && v.languages !== null) {
    if (!isTextList(v.languages)) invalid();
    if (v.languages.length) out.languages = [...v.languages];
  }
  return out;
}

export function parseScenario(v: unknown): WarmupScenario {
  if (!isRecord(v)) invalid();
  const {
    id, key, category, name, description, trigger_type, timing, delay_value, delay_unit, enabled, enabled_at = null,
    message_html, image_id = null, cooldown_hours = null, daily_cap, discount_percent, coupon_template, coupon_ttl_hours,
    position, version, created_at, updated_at, buttons, conditions,
  } = v;
  if (!isId(id) || !isText(key) || !isText(category) || !isText(name) || !isText(description) || !isText(trigger_type) ||
    !isText(timing) || !isCount(delay_value) || !isText(delay_unit) || !isBool(enabled) || !isNullTime(enabled_at) ||
    !isText(message_html) || !(image_id === null || isId(image_id)) || !(cooldown_hours === null || isCount(cooldown_hours)) ||
    !isCount(daily_cap) || !isCount(discount_percent) || !isText(coupon_template) || !isCount(coupon_ttl_hours) ||
    !isInt(position) || !isId(version) || !isTime(created_at) || !isTime(updated_at) || !Array.isArray(buttons)) invalid();
  return {
    id, key, category, name, description, trigger_type, timing, delay_value, delay_unit, enabled, enabled_at: enabled_at ?? null,
    message_html, image_id, cooldown_hours, daily_cap, discount_percent, coupon_template, coupon_ttl_hours, position, version,
    created_at, updated_at, buttons: buttons.map(parseButton), conditions: parseConditions(conditions),
  };
}

function parseRegistry(v: unknown): WarmupRegistry {
  if (!isRecord(v) || !Array.isArray(v.triggers) || !Array.isArray(v.variables) || !Array.isArray(v.callbacks) ||
    !Array.isArray(v.conditions) || !isRecord(v.limits)) invalid();
  const triggers = v.triggers.map(t => {
    if (!isRecord(t) || !isText(t.type) || !isText(t.label) || !isText(t.description) || !isTextList(t.timings)) invalid();
    return { type: t.type, label: t.label, description: t.description, timings: [...t.timings] };
  });
  const variables = v.variables.map(x => {
    if (!isRecord(x) || !isText(x.name) || !isText(x.label) || !isText(x.description) || !isText(x.sample) ||
      !(x.triggers === null || x.triggers === undefined || isTextList(x.triggers)) || !isBool(x.discount)) invalid();
    return { name: x.name, label: x.label, description: x.description, sample: x.sample, triggers: x.triggers ? [...x.triggers] : null, discount: x.discount };
  });
  const callbacks = v.callbacks.map(c => {
    if (!isRecord(c) || !isText(c.data) || !isText(c.label)) invalid();
    return { data: c.data, label: c.label };
  });
  const conditions = v.conditions.map(c => {
    if (!isRecord(c) || !isText(c.key) || !isText(c.label) || !isText(c.description) || !isText(c.kind)) invalid();
    return { key: c.key, label: c.label, description: c.description, kind: c.kind };
  });
  const l = v.limits;
  const keys = ['image_max_bytes', 'max_buttons', 'max_text_length', 'max_caption_length', 'max_cooldown_hours', 'max_daily_cap',
    'max_coupon_ttl_hours', 'max_per_day_limit', 'max_catchup_hours'] as const;
  if (!keys.every(k => isId(l[k]))) invalid();
  const limits = Object.fromEntries(keys.map(k => [k, l[k]])) as unknown as WarmupLimits;
  return { triggers, variables, callbacks, conditions, limits };
}

function parseSummary(v: unknown): WarmupSummary {
  if (!isRecord(v) || !isCount(v.scenarios) || !isCount(v.enabled_scenarios) || !isRecord(v.last_24h) ||
    !Object.values(v.last_24h).every(isCount) || !isCount(v.active_coupons) || !isCount(v.contacts) || !isCount(v.blocked_contacts)) invalid();
  const { sent_today = 0, sent_7d = 0, coupons_created = 0, scenario_stats = [] } = v;
  if (!isCount(sent_today) || !isCount(sent_7d) || !isCount(coupons_created) || !Array.isArray(scenario_stats)) invalid();
  const stats = scenario_stats.map(s => {
    if (!isRecord(s) || !isId(s.scenario_id) || !isCount(s.sent) || !isNullTime(s.last_sent_at)) invalid();
    return { scenario_id: s.scenario_id, sent: s.sent, last_sent_at: s.last_sent_at ?? null };
  });
  return {
    scenarios: v.scenarios, enabled_scenarios: v.enabled_scenarios, last_24h: { ...v.last_24h } as Record<string, number>,
    sent_today, sent_7d, active_coupons: v.active_coupons, coupons_created, contacts: v.contacts, blocked_contacts: v.blocked_contacts,
    scenario_stats: stats,
  };
}

export function parseOverview(v: unknown): WarmupOverview {
  if (!isRecord(v) || !Array.isArray(v.categories) || !Array.isArray(v.scenarios)) invalid();
  const categories = v.categories.map(c => {
    if (!isRecord(c) || !isText(c.key) || !isText(c.label) || !isCount(c.scenarios) || !isCount(c.enabled)) invalid();
    return { key: c.key, label: c.label, scenarios: c.scenarios, enabled: c.enabled };
  });
  return {
    settings: parseSettings(v.settings), categories, scenarios: v.scenarios.map(parseScenario),
    summary: parseSummary(v.summary), registry: parseRegistry(v.registry),
  };
}

function parseImage(v: unknown): WarmupImage {
  if (!isRecord(v) || !isId(v.id) || !isText(v.mime) || !isCount(v.size_bytes) || !isText(v.sha256)) invalid();
  return { id: v.id, mime: v.mime, size_bytes: v.size_bytes, sha256: v.sha256, created_at: isTime(v.created_at) ? v.created_at : '' };
}

export function parsePreview(v: unknown): WarmupPreview {
  if (!isRecord(v) || !isBool(v.valid) || !Array.isArray(v.problems)) invalid();
  const problems = v.problems.map(p => {
    if (!isRecord(p) || !isText(p.field) || !isText(p.code) || !isText(p.severity) || (p.detail !== undefined && !isText(p.detail))) invalid();
    return { field: p.field, code: p.code, detail: p.detail ?? '', severity: p.severity };
  });
  let message: WarmupMessage | null = null;
  if (v.message !== null && v.message !== undefined) {
    const m = v.message;
    if (!isRecord(m) || !isText(m.html) || !(m.image_id === null || m.image_id === undefined || isId(m.image_id)) ||
      !Array.isArray(m.buttons) || !isCount(m.hidden_buttons) || !isCount(m.visible_length) || !isCount(m.max_length)) invalid();
    message = {
      html: m.html, image_id: m.image_id ?? null, buttons: m.buttons.map(parseButton), hidden_buttons: m.hidden_buttons,
      visible_length: m.visible_length, max_length: m.max_length,
    };
  }
  const variables: Record<string, string> = {};
  if (isRecord(v.variables)) {
    for (const [name, value] of Object.entries(v.variables)) if (isText(value)) variables[name] = value;
  }
  return {
    valid: v.valid, problems, message, image: v.image === null || v.image === undefined ? null : parseImage(v.image), variables,
    discount_code: isText(v.discount_code) ? v.discount_code : '', discount_code_test: v.discount_code_test === true,
  };
}

function parseDelivery(v: unknown): WarmupDelivery {
  if (!isRecord(v) || !isId(v.id) || !isCount(v.scenario_id) || !isText(v.scenario_name) || !isInt(v.telegram_id) ||
    !isText(v.username) || !isText(v.status) || !isCount(v.attempts) || !isText(v.error) ||
    !(v.coupon_code === null || v.coupon_code === undefined || isText(v.coupon_code)) || !isTime(v.created_at) ||
    !isTime(v.due_at) || !isNullTime(v.sent_at)) invalid();
  return {
    id: v.id, scenario_id: v.scenario_id, scenario_name: v.scenario_name, telegram_id: v.telegram_id, username: v.username,
    status: v.status, attempts: v.attempts, error: v.error, coupon_code: v.coupon_code ?? null, created_at: v.created_at,
    due_at: v.due_at, sent_at: v.sent_at ?? null,
  };
}

function parseCoupon(v: unknown): WarmupCoupon {
  if (!isRecord(v) || !isId(v.id) || !isText(v.code) || !isInt(v.telegram_id) || !isCount(v.scenario_id) || !isCount(v.percent) ||
    !isText(v.status) || !isText(v.effective_status) || !isTime(v.created_at) || !isTime(v.expires_at) ||
    !isNullTime(v.redeemed_at) || !(v.order_id === null || v.order_id === undefined || isId(v.order_id))) invalid();
  return {
    id: v.id, code: v.code, telegram_id: v.telegram_id, scenario_id: v.scenario_id, percent: v.percent, status: v.status,
    effective_status: v.effective_status, created_at: v.created_at, expires_at: v.expires_at, redeemed_at: v.redeemed_at ?? null,
    order_id: v.order_id ?? null,
  };
}

function parsePage<T>(v: unknown, key: string, item: (x: unknown) => T): { items: T[]; total: number; limit: number; offset: number } {
  if (!isRecord(v) || !Array.isArray(v[key]) || !isCount(v.total) || !isId(v.limit) || !isCount(v.offset)) invalid();
  const list = v[key] as unknown[];
  if (list.length > v.limit) invalid();
  return { items: list.map(item), total: v.total, limit: v.limit, offset: v.offset };
}

// =============================================================================
// API client
// =============================================================================

const BASE = '/admin/api/warmup';

function checkId(id: number) {
  if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
}

export function deliveriesPath(q: WarmupDeliveryQuery): string {
  const p = new URLSearchParams();
  if (q.scenarioId !== null) p.set('scenario_id', String(q.scenarioId));
  if (q.category) p.set('category', q.category);
  if (q.status) p.set('status', q.status);
  if (q.telegramId.trim()) p.set('telegram_id', q.telegramId.trim());
  if (q.from) p.set('from', q.from);
  if (q.to) p.set('to', q.to);
  if (q.coupon.trim()) p.set('coupon', q.coupon.trim());
  if (q.offset > 0) p.set('offset', String(q.offset));
  const s = p.toString();
  return s ? `${BASE}/deliveries?${s}` : `${BASE}/deliveries`;
}

export function couponsPath(q: WarmupCouponQuery): string {
  const p = new URLSearchParams();
  if (q.scenarioId !== null) p.set('scenario_id', String(q.scenarioId));
  if (q.status) p.set('status', q.status);
  if (q.telegramId.trim()) p.set('telegram_id', q.telegramId.trim());
  if (q.code.trim()) p.set('code', q.code.trim());
  if (q.offset > 0) p.set('offset', String(q.offset));
  const s = p.toString();
  return s ? `${BASE}/coupons?${s}` : `${BASE}/coupons`;
}

/** URL of the picture itself (same-origin <img>, the session cookie authorizes it). */
export const warmupImageUrl = (id: number) => `${BASE}/images/${id}`;

/**
 * Every warm-up request of the admin. Writes carry a request_key: the same key
 * with the same payload replays the committed outcome, so an uncertain
 * request is retried with the SAME key; saves carry the version loaded.
 */
export class WarmupApi {
  constructor(private readonly api: Pick<AdminApi, 'request'>) {}

  async overview(): Promise<WarmupOverview> {
    return parseOverview(await this.api.request('GET', BASE));
  }

  /** GET /settings: the global settings alone (refresh after a conflict). */
  async settings(): Promise<WarmupSettings> {
    return parseSettings(await this.api.request('GET', `${BASE}/settings`));
  }

  /** GET /scenarios: every scenario in admin order (filter lists). */
  async scenarios(): Promise<WarmupScenario[]> {
    const data = await this.api.request('GET', `${BASE}/scenarios`);
    if (!isRecord(data) || !Array.isArray(data.scenarios)) invalid();
    return data.scenarios.map(parseScenario);
  }

  /**
   * POST /scenarios: a new scenario under a unique key. The UI always sends
   * enabled=false, so nothing is scheduled until the administrator switches it on.
   */
  async createScenario(key: string, input: WarmupScenarioInput, requestKey: string): Promise<WarmupScenarioOutcome> {
    const data = await this.api.request('POST', `${BASE}/scenarios`, JSON.stringify({ request_key: requestKey, key, ...input }));
    if (!isRecord(data) || !isBool(data.replayed)) invalid();
    return { scenario: parseScenario(data.scenario), replayed: data.replayed };
  }

  /** POST /preview: an unsaved draft (create form); writes nothing. */
  async previewDraft(input: WarmupScenarioInput, context: WarmupPreviewContext): Promise<WarmupPreview> {
    return parsePreview(await this.api.request('POST', `${BASE}/preview`, JSON.stringify({ ...input, context })));
  }

  async scenario(id: number): Promise<WarmupScenario> {
    checkId(id);
    const sc = parseScenario(await this.api.request('GET', `${BASE}/scenarios/${id}`));
    if (sc.id !== id) invalid();
    return sc;
  }

  /** PATCH /scenarios/{id}: 409 version_conflict when another session saved first. */
  async patchScenario(id: number, version: number, patch: WarmupScenarioPatch, requestKey: string): Promise<WarmupScenarioOutcome> {
    checkId(id);
    const data = await this.api.request('PATCH', `${BASE}/scenarios/${id}`, JSON.stringify({ request_key: requestKey, version, ...patch }));
    if (!isRecord(data) || !isBool(data.replayed)) invalid();
    const scenario = parseScenario(data.scenario);
    if (scenario.id !== id) invalid();
    return { scenario, replayed: data.replayed };
  }

  /** POST /scenarios/{id}/preview: the stored scenario with an unsaved patch; writes nothing. */
  async previewScenario(id: number, patch: WarmupScenarioPatch, context: WarmupPreviewContext): Promise<WarmupPreview> {
    checkId(id);
    return parsePreview(await this.api.request('POST', `${BASE}/scenarios/${id}/preview`, JSON.stringify({ ...patch, context })));
  }

  async saveSettings(save: WarmupSettingsSave, requestKey: string): Promise<WarmupSettingsOutcome> {
    const data = await this.api.request('PUT', `${BASE}/settings`, JSON.stringify({ request_key: requestKey, ...save }));
    if (!isRecord(data) || !isBool(data.replayed)) invalid();
    return { settings: parseSettings(data.settings), replayed: data.replayed };
  }

  /** POST /images with the picture as base64; the server sniffs JPEG/PNG and checks the size again. */
  async uploadImage(base64: string, requestKey: string): Promise<WarmupImage> {
    const data = await this.api.request('POST', `${BASE}/images`, JSON.stringify({ request_key: requestKey, data: base64 }));
    if (!isRecord(data)) invalid();
    return parseImage(data.image);
  }

  /** DELETE /images/{id}: 409 image_in_use while a scenario still shows it. */
  async deleteImage(id: number, requestKey: string): Promise<void> {
    checkId(id);
    const data = await this.api.request('DELETE', `${BASE}/images/${id}`, JSON.stringify({ request_key: requestKey }));
    if (!isRecord(data) || !isBool(data.replayed)) invalid();
  }

  async deliveries(q: WarmupDeliveryQuery): Promise<WarmupDeliveryPage> {
    const page = parsePage(await this.api.request('GET', deliveriesPath(q)), 'deliveries', parseDelivery);
    return { deliveries: page.items, total: page.total, limit: page.limit, offset: page.offset };
  }

  async coupons(q: WarmupCouponQuery): Promise<WarmupCouponPage> {
    const page = parsePage(await this.api.request('GET', couponsPath(q)), 'coupons', parseCoupon);
    return { coupons: page.items, total: page.total, limit: page.limit, offset: page.offset };
  }
}

// =============================================================================
// Error texts
// =============================================================================

export const SCENARIO_CONFLICT_TEXT = 'Сценарий был изменён в другой сессии. Обновите данные.';
export const SETTINGS_CONFLICT_TEXT = 'Настройки прогрева были изменены в другой сессии. Обновите данные.';
export const REMINDER_CONFLICT_TEXT = 'Этот сценарий конфликтует с существующим напоминанием о подписке и не может быть включён одновременно с ним.';
export const KEY_TAKEN_TEXT = 'Ключ уже занят другим сценарием.';

/** Texts of the problem/rejection reasons (service.WarmupFieldError.Reason, WarmupProblem.Code). */
const REASONS: Readonly<Record<string, string>> = {
  required: 'Обязательное поле.',
  length: 'Недопустимая длина.',
  too_long: 'Текст слишком длинный.',
  unknown: 'Неизвестное значение.',
  not_allowed: 'Это значение недоступно для выбранного триггера.',
  out_of_range: 'Значение вне допустимого диапазона.',
  format: 'Недопустимый формат.',
  not_found: 'Не найдено.',
  html_encoding: 'Текст содержит недопустимые символы.',
  html_unclosed_tag: 'Есть незакрытый тег.',
  html_bad_nesting: 'Теги закрыты в неправильном порядке.',
  html_bad_tag: 'Недопустимый тег.',
  html_unsupported_tag: 'Telegram не поддерживает этот тег.',
  html_bad_attribute: 'Недопустимый атрибут тега.',
  html_bad_link: 'Недопустимая ссылка: нужен адрес https://, http:// или tg://.',
  html_unescaped: 'Символы <, > и & в тексте нужно писать как &lt;, &gt; и &amp;.',
  placeholder_malformed: 'Переменная записана неправильно: используйте {{имя}}.',
  variable_unknown: 'Неизвестная переменная.',
  variable_unavailable: 'Переменная недоступна для выбранного триггера.',
  variable_needs_discount: 'Переменная скидки доступна только при включённой скидке.',
  discount_code_required: 'При включённой скидке в тексте должен быть {{discount_code}}.',
  coupon_required: 'Для скидки нужен купон.',
  button_text: 'Текст кнопки: от 1 до 64 символов, без переменных.',
  button_url: 'Недопустимый адрес кнопки (Mini App — только https://).',
  button_callback: 'Действие кнопки не из списка разрешённых.',
  button_kind: 'Неизвестный тип кнопки.',
  buttons_too_many: 'Слишком много кнопок.',
  button_url_missing: 'Кнопка без URL будет скрыта при отправке.',
  conditions_languages: 'Коды языков: 2–3 латинские буквы (ru, en…), не больше 20.',
  duplicates_expiry_reminder: REMINDER_CONFLICT_TEXT,
  expiry_reminders_active: 'Старые напоминания о подписке (3 дня, 1 день, 3 часа) продолжают работать.',
  too_large: 'Файл больше 2 МБ.',
  unsupported_type: 'Поддерживаются только JPEG и PNG.',
  corrupt: 'Файл повреждён или не является изображением.',
  dimensions: 'Telegram не примет картинку такого размера (сумма сторон до 10000, соотношение до 1:20).',
  base64: 'Файл не удалось прочитать.',
  empty: 'Нет изменений для сохранения.',
};

/** Field labels for the messages (service field names). */
export const FIELD_LABELS: Readonly<Record<string, string>> = {
  category: 'Категория', name: 'Название', description: 'Описание', trigger_type: 'Триггер', timing: 'Момент',
  delay_value: 'Задержка', delay_unit: 'Единица', message_html: 'Сообщение', image_id: 'Картинка', image: 'Картинка',
  buttons: 'Кнопки', conditions: 'Условия', cooldown_hours: 'Cooldown', daily_cap: 'Лимит в сутки',
  discount_percent: 'Скидка', coupon_template: 'Префикс кода', coupon_ttl_hours: 'Срок купона', position: 'Порядок',
  max_per_day: 'Максимум сообщений', catchup_hours: 'Catch-up window', version: 'Версия', key: 'Ключ',
};

export function reasonText(code: string, detail = ''): string {
  const base = REASONS[code] ?? 'Сервер отклонил значение.';
  if (code === 'too_long' && /^\d+\/\d+$/.test(detail)) return `Текст слишком длинный: ${detail.replace('/', ' из ')} символов.`;
  if ((code === 'variable_unknown' || code === 'variable_unavailable' || code === 'variable_needs_discount') && /^[a-z_]+$/.test(detail)) {
    return `${base} {{${detail}}}`;
  }
  return base;
}

export function problemText(p: Pick<WarmupProblem, 'field' | 'code' | 'detail'>): string {
  const label = FIELD_LABELS[p.field.replace(/\[\d+\]$/, '')];
  const text = reasonText(p.code, p.detail);
  return label ? `${label}: ${text}` : text;
}

const REJECTIONS: Readonly<Record<string, string>> = {
  version_conflict: SCENARIO_CONFLICT_TEXT,
  duplicates_expiry_reminder: REMINDER_CONFLICT_TEXT,
  scenario_not_found: 'Сценарий не найден.',
  image_not_found: 'Картинка не найдена: возможно, её уже удалили.',
  image_in_use: 'Картинку использует сценарий: сначала уберите её из сценария.',
  image_too_large: 'Файл больше 2 МБ.',
  image_invalid_type: 'Поддерживаются только JPEG и PNG.',
  callback_not_allowed: 'Действие кнопки не из списка разрешённых.',
  scenario_key_taken: KEY_TAKEN_TEXT,
  request_key_conflict: 'Запрос конфликтует с ранее отправленным. Повторите действие.',
  invalid_request: 'Сервер отклонил параметры запроса.',
};

const FIELD_CODES = new Set(['validation_error', 'invalid_html', 'invalid_variable', 'invalid_button', 'invalid_trigger',
  'callback_not_allowed', 'image_invalid_type', 'image_too_large', 'image_not_found', 'duplicates_expiry_reminder']);

/** A warm-up rejection in words; field rejections name the field and the reason. */
export function warmupErrorText(error: unknown, conflict = SCENARIO_CONFLICT_TEXT): string {
  if (!(error instanceof ApiError)) return errorText(error);
  if (error.code === 'version_conflict') return conflict;
  if (error.code === 'duplicates_expiry_reminder') return REMINDER_CONFLICT_TEXT;
  if (FIELD_CODES.has(error.code) && error.reason) return problemText({ field: error.field, code: error.reason, detail: error.detail });
  return REJECTIONS[error.code] ?? errorText(error);
}

// =============================================================================
// Labels and descriptions
// =============================================================================

export const CATEGORY_LABELS: Readonly<Record<string, string>> = {
  trial: 'Пробный период', subscription: 'Подписка', winback: 'Возврат клиента',
};

export const CATEGORY_HINTS: Readonly<Record<string, string>> = {
  trial: 'Новые пользователи и пробный период: активация, подключение, окончание.',
  subscription: 'Окончание платной подписки. Старые напоминания (3 дня, 1 день, 3 часа) работают отдельно.',
  winback: 'Пользователи, у которых закончилась платная подписка.',
};

const TRIGGER_EMOJI: Readonly<Record<string, string>> = {
  bot_started: '👋', trial_activated: '🚀', trial_period_end: '⏳', paid_expiry: '🔔', paid_expired: '💌',
};
export const triggerEmoji = (trigger: string) => TRIGGER_EMOJI[trigger] ?? '✉️';

/** The anchor event in the genitive, for "через N после …". */
const TRIGGER_EVENT: Readonly<Record<string, string>> = {
  bot_started: 'первого запуска бота',
  trial_activated: 'активации пробного периода',
  trial_period_end: 'окончания пробного периода',
  paid_expiry: 'окончания подписки',
  paid_expired: 'окончания подписки',
};

export const UNIT_LABELS: Readonly<Record<WarmupDelayUnit, string>> = { minutes: 'минут', hours: 'часов', days: 'дней' };
export const TIMING_LABELS: Readonly<Record<string, string>> = { after: 'После события', before: 'До события' };

export function pluralRu(n: number, one: string, few: string, many: string): string {
  const mod100 = Math.abs(n) % 100;
  if (mod100 >= 11 && mod100 <= 14) return many;
  switch (mod100 % 10) {
    case 1: return one;
    case 2: case 3: case 4: return few;
    default: return many;
  }
}

export function delayText(value: number, unit: string): string {
  switch (unit) {
    case 'minutes': return `${value} ${pluralRu(value, 'минуту', 'минуты', 'минут')}`;
    case 'hours': return `${value} ${pluralRu(value, 'час', 'часа', 'часов')}`;
    case 'days': return `${value} ${pluralRu(value, 'день', 'дня', 'дней')}`;
    default: return `${value} ${unit}`;
  }
}

export const hoursText = (hours: number) => `${hours} ${pluralRu(hours, 'час', 'часа', 'часов')}`;

/**
 * "Через 12 часов после первого запуска бота", "За 24 часа до окончания
 * пробного периода". Describes the configuration only; when a message is
 * actually due is decided by the server.
 */
export function triggerText(trigger: string, timing: string, value: number, unit: string, registry?: WarmupRegistry): string {
  const event = TRIGGER_EVENT[trigger] ?? `события «${registry?.triggers.find(t => t.type === trigger)?.label ?? trigger}»`;
  if (timing === 'before') return value > 0 ? `За ${delayText(value, unit)} до ${event}` : `В момент ${event}`;
  return value > 0 ? `Через ${delayText(value, unit)} после ${event}` : `Сразу после ${event}`;
}

export const DELIVERY_STATUS_LABELS: Readonly<Record<string, string>> = {
  sending: 'Отправляется', sent: 'Отправлено', retry: 'Повтор', failed: 'Ошибка', blocked: 'Заблокировал',
};

export const COUPON_STATUS_LABELS: Readonly<Record<string, string>> = {
  issued: 'Действует', expired: 'Истёк', revoked: 'Отозван', redeemed: 'Использован',
};

export const BUTTON_KIND_LABELS: Readonly<Record<string, string>> = { url: 'URL', web_app: 'Mini App', callback: 'Callback' };

/** Key of the standard scenario whose URL button opens the trial page. */
export const TRIAL_URL_SCENARIO = 'trial_not_activated';
export const HIDDEN_BUTTON_TEXT = 'Кнопка будет скрыта при отправке, пока URL не задан.';

// =============================================================================
// Message text
// =============================================================================

const ENTITY = /&(lt|gt|amp|quot|#(\d{1,7})|#x([0-9a-fA-F]{1,6}));/g;

function decodeEntities(s: string): string {
  return s.replace(ENTITY, (m: string, name: string, dec?: string, hex?: string) => {
    if (name === 'lt') return '<';
    if (name === 'gt') return '>';
    if (name === 'amp') return '&';
    if (name === 'quot') return '"';
    const code = dec ? Number(dec) : parseInt(hex ?? '', 16);
    return Number.isFinite(code) && code <= 0x10FFFF ? String.fromCodePoint(code) : m;
  });
}

/**
 * Length Telegram counts for the template text: tags dropped, entities
 * decoded, UTF-16 code units (service.warmupVisibleLength). Variables are
 * counted as written; the preview reports the length with real values.
 */
export function visibleLength(html: string): number {
  return decodeEntities(html.replace(/<[^>]*>/g, '')).length;
}

export const byteLength = (s: string) => new TextEncoder().encode(s).length;

/** service.warmupMaxMessageBytes: the stored HTML, before rendering. */
export const MESSAGE_MAX_BYTES = 16384;

/** Plain text of a message, for card previews. */
export function plainText(html: string): string {
  return decodeEntities(html.replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim();
}

/** {{name}} placeholders used by a message, unique, in order. */
export function placeholders(html: string): string[] {
  const names: string[] = [];
  for (const m of html.matchAll(/\{\{\s*([a-z_]+)\s*\}\}/g)) if (!names.includes(m[1])) names.push(m[1]);
  return names;
}

export interface Edit {
  readonly text: string;
  readonly start: number;
  readonly end: number;
}

/** Wraps the selection in a tag pair; the selection stays on the wrapped text. */
export function wrapSelection(text: string, start: number, end: number, open: string, close: string): Edit {
  const inner = text.slice(start, end);
  return { text: text.slice(0, start) + open + inner + close + text.slice(end), start: start + open.length, end: start + open.length + inner.length };
}

/** Replaces the selection with value; the caret goes after it. */
export function insertText(text: string, start: number, end: number, value: string): Edit {
  const caret = start + value.length;
  return { text: text.slice(0, start) + value + text.slice(end), start: caret, end: caret };
}

/** A link around the selection with a placeholder address selected for typing. */
export function insertLink(text: string, start: number, end: number): Edit {
  const href = 'https://';
  const open = `<a href="${href}">`;
  const inner = text.slice(start, end) || 'ссылка';
  const hrefStart = start + '<a href="'.length;
  return { text: text.slice(0, start) + open + inner + '</a>' + text.slice(end), start: hrefStart, end: hrefStart + href.length };
}

export type VariableState = 'available' | 'trigger' | 'discount';

/** Whether a variable can be used with the trigger and discount (service.checkWarmupPlaceholders). */
export function variableState(v: WarmupVariable, trigger: string, discount: boolean): VariableState {
  if (v.triggers && !v.triggers.includes(trigger)) return 'trigger';
  if (v.discount && !discount) return 'discount';
  return 'available';
}

// =============================================================================
// Telegram HTML → DOM (the server-rendered message, never a local renderer)
// =============================================================================

const TAGS: Readonly<Record<string, keyof HTMLElementTagNameMap>> = {
  b: 'b', strong: 'strong', i: 'i', em: 'em', u: 'u', ins: 'ins', s: 's', strike: 's', del: 'del',
  code: 'code', pre: 'pre', blockquote: 'blockquote',
};

function copyTelegram(from: Node, to: Node, doc: Document) {
  for (const node of Array.from(from.childNodes)) {
    if (node.nodeType === Node.TEXT_NODE) {
      to.appendChild(doc.createTextNode(node.textContent ?? ''));
      continue;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) continue;
    const source = node as Element;
    const name = source.tagName.toLowerCase();
    let target: HTMLElement | null = null;
    if (name in TAGS) {
      target = doc.createElement(TAGS[name]);
    } else if (name === 'tg-spoiler' || (name === 'span' && source.getAttribute('class') === 'tg-spoiler')) {
      target = doc.createElement('span');
      target.className = 'tg-spoiler';
    } else if (name === 'a') {
      const href = source.getAttribute('href') ?? '';
      const link = doc.createElement('a');
      if (/^(https?|tg):/i.test(href)) {
        link.href = href;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
      }
      target = link;
    }
    if (target) {
      copyTelegram(source, target, doc);
      to.appendChild(target);
    } else {
      copyTelegram(source, to, doc);
    }
  }
}

/**
 * Builds the DOM of a Telegram HTML message returned by the preview API. The
 * markup is parsed inert (DOMParser runs no script) and only the tags Telegram
 * supports are re-created, with text as text nodes and safe link schemes.
 */
export function telegramFragment(html: string, doc: Document = document): DocumentFragment {
  const parsed = new DOMParser().parseFromString(`<!doctype html><body>${html}</body>`, 'text/html');
  const out = doc.createDocumentFragment();
  copyTelegram(parsed.body, out, doc);
  return out;
}

// =============================================================================
// Buttons
// =============================================================================

/** Groups buttons into keyboard rows (same_row joins the previous row). */
export function buttonRows<T extends Pick<WarmupButton, 'same_row'>>(buttons: readonly T[]): T[][] {
  const rows: T[][] = [];
  for (const b of buttons) {
    if (b.same_row && rows.length) rows[rows.length - 1].push(b);
    else rows.push([b]);
  }
  return rows;
}

/** Moves a button up or down; the first button always starts a row. */
export function moveButton(buttons: readonly WarmupButton[], index: number, delta: -1 | 1): WarmupButton[] {
  const next = [...buttons];
  const to = index + delta;
  if (to < 0 || to >= next.length) return next;
  [next[index], next[to]] = [next[to], next[index]];
  return normalizeRows(next);
}

export function normalizeRows(buttons: readonly WarmupButton[]): WarmupButton[] {
  return buttons.map((b, i) => (i === 0 && b.same_row ? withoutSameRow(b) : b));
}

function withoutSameRow(b: WarmupButton): WarmupButton {
  return { text: b.text, kind: b.kind, value: b.value };
}

export function setSameRow(b: WarmupButton, sameRow: boolean): WarmupButton {
  return sameRow ? { ...withoutSameRow(b), same_row: true } : withoutSameRow(b);
}

/** service.validateWarmupURL: http/https/tg for URL buttons, https only for a Mini App. */
export function validUrl(raw: string, httpsOnly: boolean): boolean {
  if (!raw || raw.length > 2048 || /\s/.test(raw)) return false;
  try {
    const u = new URL(raw);
    if (!u.host) return false;
    return u.protocol === 'https:' || (!httpsOnly && (u.protocol === 'http:' || u.protocol === 'tg:'));
  } catch {
    return false;
  }
}

/** First problem of a button, '' when it is fine (an empty URL is allowed but hidden). */
export function buttonError(b: WarmupButton, callbacks: readonly WarmupCallbackAction[]): string {
  const text = b.text.trim();
  if (!text || Array.from(text).length > 64 || text.includes('{{')) return reasonText('button_text');
  const value = b.value.trim();
  if (b.kind === 'url' || b.kind === 'web_app') {
    if (value && !validUrl(value, b.kind === 'web_app')) return reasonText('button_url');
    return '';
  }
  if (b.kind === 'callback') return callbacks.some(c => c.data === value) ? '' : reasonText('button_callback');
  return reasonText('button_kind');
}

export const isHiddenButton = (b: WarmupButton) => (b.kind === 'url' || b.kind === 'web_app') && b.value.trim() === '';

// =============================================================================
// Conditions
// =============================================================================

export type FlagChoice = 'any' | 'yes' | 'no';

export function flagChoice(c: WarmupConditions, key: WarmupFlagCondition): FlagChoice {
  const v = c[key];
  return v === undefined ? 'any' : v ? 'yes' : 'no';
}

export function withFlag(c: WarmupConditions, key: WarmupFlagCondition, choice: FlagChoice): WarmupConditions {
  const next: { -readonly [K in keyof WarmupConditions]: WarmupConditions[K] } = { ...c };
  if (choice === 'any') delete next[key];
  else next[key] = choice === 'yes';
  return next;
}

export const isFlagCondition = (key: string): key is WarmupFlagCondition => (WARMUP_FLAG_CONDITIONS as readonly string[]).includes(key);

/** "ru, en" → ['ru', 'en'], or null when a code is not 2–3 latin letters (service.warmupLanguagePattern). */
export function parseLanguages(raw: string): string[] | null {
  const codes: string[] = [];
  for (const part of raw.split(/[\s,;]+/)) {
    const code = part.trim().toLowerCase();
    if (!code) continue;
    if (!/^[a-z]{2,3}$/.test(code)) return null;
    if (!codes.includes(code)) codes.push(code);
  }
  return codes.length > 20 ? null : codes;
}

export function withLanguages(c: WarmupConditions, languages: readonly string[]): WarmupConditions {
  const next: { -readonly [K in keyof WarmupConditions]: WarmupConditions[K] } = { ...c };
  if (languages.length) next.languages = [...languages];
  else delete next.languages;
  return next;
}

function sameConditions(a: WarmupConditions, b: WarmupConditions): boolean {
  return WARMUP_FLAG_CONDITIONS.every(k => a[k] === b[k]) &&
    (a.languages ?? []).join(',') === (b.languages ?? []).join(',');
}

// =============================================================================
// Scenario form
// =============================================================================

/** Editable state of a scenario; numbers are kept as typed until validated. */
export interface ScenarioForm {
  name: string;
  description: string;
  category: string;
  position: string;
  trigger_type: string;
  timing: string;
  delay_value: string;
  delay_unit: string;
  message_html: string;
  image_id: number | null;
  buttons: WarmupButton[];
  conditions: WarmupConditions;
  /** false: the global cooldown applies (cooldown_hours null). */
  own_cooldown: boolean;
  cooldown_hours: string;
  daily_cap: string;
  discount: boolean;
  discount_percent: string;
  coupon_template: string;
  coupon_ttl_hours: string;
}

export type ScenarioField = keyof ScenarioForm;
export type ScenarioErrors = Partial<Record<ScenarioField, string>>;

/** Server field → form field (for rejections and preview problems). */
export const SERVER_FIELDS: Readonly<Record<string, ScenarioField>> = {
  name: 'name', description: 'description', category: 'category', position: 'position', trigger_type: 'trigger_type',
  timing: 'timing', delay_value: 'delay_value', delay_unit: 'delay_unit', message_html: 'message_html', image_id: 'image_id',
  image: 'image_id', buttons: 'buttons', conditions: 'conditions', cooldown_hours: 'cooldown_hours', daily_cap: 'daily_cap',
  discount_percent: 'discount_percent', coupon_template: 'coupon_template', coupon_ttl_hours: 'coupon_ttl_hours',
};

export function formFromScenario(sc: WarmupScenario): ScenarioForm {
  return {
    name: sc.name, description: sc.description, category: sc.category, position: String(sc.position),
    trigger_type: sc.trigger_type, timing: sc.timing, delay_value: String(sc.delay_value), delay_unit: sc.delay_unit,
    message_html: sc.message_html, image_id: sc.image_id, buttons: sc.buttons.map(b => ({ ...b })),
    conditions: { ...sc.conditions, ...(sc.conditions.languages ? { languages: [...sc.conditions.languages] } : {}) },
    own_cooldown: sc.cooldown_hours !== null, cooldown_hours: String(sc.cooldown_hours ?? ''), daily_cap: String(sc.daily_cap),
    discount: sc.discount_percent > 0, discount_percent: String(sc.discount_percent > 0 ? sc.discount_percent : 10),
    coupon_template: sc.coupon_template, coupon_ttl_hours: String(sc.coupon_ttl_hours),
  };
}

export const cloneForm = (f: ScenarioForm): ScenarioForm => ({
  ...f, buttons: f.buttons.map(b => ({ ...b })),
  conditions: { ...f.conditions, ...(f.conditions.languages ? { languages: [...f.conditions.languages] } : {}) },
});

function sameButtons(a: readonly WarmupButton[], b: readonly WarmupButton[]): boolean {
  return a.length === b.length && a.every((x, i) => x.text === b[i].text && x.kind === b[i].kind && x.value === b[i].value &&
    Boolean(x.same_row) === Boolean(b[i].same_row));
}

const parseInteger = (raw: string): number | null => (/^-?\d{1,9}$/.test(raw.trim()) ? Number(raw.trim()) : null);

export interface ScenarioValues {
  name: string; description: string; category: string; position: number; trigger_type: string; timing: string;
  delay_value: number; delay_unit: string; message_html: string; image_id: number | null; buttons: WarmupButton[];
  conditions: WarmupConditions; cooldown_hours: number | null; daily_cap: number; discount_percent: number;
  coupon_template: string; coupon_ttl_hours: number;
}

/** Normalized values of a form, or the errors that block sending it. */
export function formValues(f: ScenarioForm, registry: WarmupRegistry): { values: ScenarioValues | null; errors: ScenarioErrors } {
  const errors: ScenarioErrors = {};
  const limits = registry.limits;
  const int = (field: ScenarioField, raw: string, min: number, max: number): number => {
    const n = parseInteger(raw);
    if (n === null || n < min || n > max) {
      errors[field] = `Целое число от ${min} до ${max}.`;
      return 0;
    }
    return n;
  };
  const name = f.name.trim();
  if (!name || Array.from(name).length > 100) errors.name = 'От 1 до 100 символов.';
  if (Array.from(f.description.trim()).length > 500) errors.description = 'Не больше 500 символов.';
  if (!(WARMUP_CATEGORIES as readonly string[]).includes(f.category)) errors.category = 'Выберите категорию.';
  const position = int('position', f.position, -1_000_000, 1_000_000);
  const trigger = registry.triggers.find(t => t.type === f.trigger_type);
  if (!trigger) errors.trigger_type = 'Выберите триггер.';
  else if (!trigger.timings.includes(f.timing)) errors.timing = 'Этот момент недоступен для выбранного триггера.';
  if (!(WARMUP_DELAY_UNITS as readonly string[]).includes(f.delay_unit)) errors.delay_unit = 'Выберите единицу.';
  // service.warmupMaxDelayValue (525600) and warmupMaxDelay (366 days).
  const maxDelay = f.delay_unit === 'minutes' ? 525_600 : f.delay_unit === 'hours' ? 8784 : 366;
  const delay = int('delay_value', f.delay_value, 0, maxDelay);
  const html = f.message_html.replace(/\r\n/g, '\n').trim();
  if (!html) errors.message_html = 'Введите текст сообщения.';
  else if (byteLength(html) > MESSAGE_MAX_BYTES) errors.message_html = 'Текст слишком длинный.';
  const buttons = normalizeRows(f.buttons.map(b => ({ ...b, text: b.text.trim(), value: b.value.trim() })));
  if (buttons.length > limits.max_buttons) errors.buttons = `Не больше ${limits.max_buttons} кнопок.`;
  else {
    const bad = buttons.findIndex(b => buttonError(b, registry.callbacks) !== '');
    if (bad >= 0) errors.buttons = `Кнопка ${bad + 1}: ${buttonError(buttons[bad], registry.callbacks)}`;
  }
  const cooldown = f.own_cooldown ? int('cooldown_hours', f.cooldown_hours, 0, limits.max_cooldown_hours) : null;
  const dailyCap = int('daily_cap', f.daily_cap, 0, limits.max_daily_cap);
  const percent = f.discount ? int('discount_percent', f.discount_percent, 1, 100) : 0;
  const template = f.coupon_template.trim().toUpperCase();
  if (!/^[A-Z0-9]{0,16}$/.test(template)) errors.coupon_template = 'Латинские буквы и цифры, до 16 символов.';
  const ttl = int('coupon_ttl_hours', f.coupon_ttl_hours, 1, limits.max_coupon_ttl_hours);
  if (Object.keys(errors).length) return { values: null, errors };
  return {
    values: {
      name, description: f.description.trim(), category: f.category, position, trigger_type: f.trigger_type, timing: f.timing,
      delay_value: delay, delay_unit: f.delay_unit, message_html: html, image_id: f.image_id, buttons, conditions: f.conditions,
      cooldown_hours: cooldown, daily_cap: dailyCap, discount_percent: percent, coupon_template: template, coupon_ttl_hours: ttl,
    },
    errors,
  };
}

/** Full patch of every editable field (enabled excluded): what a preview evaluates. */
export function fullPatch(v: ScenarioValues): WarmupScenarioPatch {
  return {
    category: v.category, name: v.name, description: v.description, trigger_type: v.trigger_type, timing: v.timing,
    delay_value: v.delay_value, delay_unit: v.delay_unit, message_html: v.message_html, image_id: v.image_id,
    buttons: v.buttons, conditions: v.conditions, cooldown_hours: v.cooldown_hours, daily_cap: v.daily_cap,
    discount_percent: v.discount_percent, coupon_template: v.coupon_template, coupon_ttl_hours: v.coupon_ttl_hours,
    position: v.position,
  };
}

/** Only the fields that differ from the stored scenario: the save is a partial edit. */
export function changedPatch(stored: WarmupScenario, v: ScenarioValues): WarmupScenarioPatch {
  const patch: WarmupScenarioPatch = {};
  if (v.category !== stored.category) patch.category = v.category;
  if (v.name !== stored.name) patch.name = v.name;
  if (v.description !== stored.description) patch.description = v.description;
  if (v.trigger_type !== stored.trigger_type) patch.trigger_type = v.trigger_type;
  if (v.timing !== stored.timing) patch.timing = v.timing;
  if (v.delay_value !== stored.delay_value) patch.delay_value = v.delay_value;
  if (v.delay_unit !== stored.delay_unit) patch.delay_unit = v.delay_unit;
  if (v.message_html !== stored.message_html) patch.message_html = v.message_html;
  if (v.image_id !== stored.image_id) patch.image_id = v.image_id;
  if (!sameButtons(v.buttons, stored.buttons)) patch.buttons = v.buttons;
  if (!sameConditions(v.conditions, stored.conditions)) patch.conditions = v.conditions;
  if (v.cooldown_hours !== stored.cooldown_hours) patch.cooldown_hours = v.cooldown_hours;
  if (v.daily_cap !== stored.daily_cap) patch.daily_cap = v.daily_cap;
  if (v.discount_percent !== stored.discount_percent) patch.discount_percent = v.discount_percent;
  if (v.coupon_template !== stored.coupon_template) patch.coupon_template = v.coupon_template;
  if (v.coupon_ttl_hours !== stored.coupon_ttl_hours) patch.coupon_ttl_hours = v.coupon_ttl_hours;
  if (v.position !== stored.position) patch.position = v.position;
  return patch;
}

/** The form differs from the stored scenario (raw text compared, as typed). */
export function formDirty(f: ScenarioForm, base: ScenarioForm): boolean {
  return f.name !== base.name || f.description !== base.description || f.category !== base.category ||
    f.position !== base.position || f.trigger_type !== base.trigger_type || f.timing !== base.timing ||
    f.delay_value !== base.delay_value || f.delay_unit !== base.delay_unit || f.message_html !== base.message_html ||
    f.image_id !== base.image_id || !sameButtons(f.buttons, base.buttons) || !sameConditions(f.conditions, base.conditions) ||
    f.own_cooldown !== base.own_cooldown || (f.own_cooldown && f.cooldown_hours !== base.cooldown_hours) ||
    f.daily_cap !== base.daily_cap || f.discount !== base.discount || (f.discount && f.discount_percent !== base.discount_percent) ||
    f.coupon_template !== base.coupon_template || f.coupon_ttl_hours !== base.coupon_ttl_hours;
}

/** "PREFIX-XXXXXXXX" (service.couponCode): what a personal code looks like. */
export function couponExample(template: string): string {
  const prefix = template.trim().toUpperCase();
  return prefix ? `${prefix}-XXXXXXXX` : 'XXXXXXXX';
}

/** The create payload of validated values: a new scenario always starts disabled. */
export function scenarioInput(v: ScenarioValues): WarmupScenarioInput {
  return {
    category: v.category, name: v.name, description: v.description, trigger_type: v.trigger_type, timing: v.timing,
    delay_value: v.delay_value, delay_unit: v.delay_unit, enabled: false, message_html: v.message_html, image_id: v.image_id,
    buttons: v.buttons, conditions: v.conditions, cooldown_hours: v.cooldown_hours, daily_cap: v.daily_cap,
    discount_percent: v.discount_percent, coupon_template: v.coupon_template, coupon_ttl_hours: v.coupon_ttl_hours,
    position: v.position,
  };
}

/** service.warmupKeyPattern: the stable key of a scenario. */
export const SCENARIO_KEY_PATTERN = /^[a-z][a-z0-9_]{2,63}$/;

/** A free key for a new scenario: custom_<base36 time>, never one already used. */
export function suggestKey(taken: readonly string[], now = Date.now()): string {
  const base = `custom_${now.toString(36)}`;
  let key = base;
  for (let i = 2; taken.includes(key); i++) key = `${base}_${i}`;
  return key;
}

/** Next position in a category: 10 after the last one, as the seed scenarios are spaced. */
export function nextPosition(scenarios: readonly WarmupScenario[], category: string): number {
  const positions = scenarios.filter(s => s.category === category).map(s => s.position);
  return positions.length ? Math.max(...positions) + 10 : 10;
}

/** Default form of a new scenario: disabled, global cooldown, no discount. */
export function newScenarioForm(registry: WarmupRegistry, scenarios: readonly WarmupScenario[], category: WarmupCategoryKey = 'trial'): ScenarioForm {
  const trigger = registry.triggers.find(t => t.type === 'bot_started') ?? registry.triggers[0];
  return {
    name: '', description: '', category, position: String(nextPosition(scenarios, category)),
    trigger_type: trigger?.type ?? '', timing: trigger?.timings[0] ?? 'after', delay_value: '1', delay_unit: 'days',
    message_html: '<b>Заголовок</b>\n\nТекст сообщения.', image_id: null, buttons: [], conditions: {},
    own_cooldown: false, cooldown_hours: '', daily_cap: '0', discount: false, discount_percent: '10',
    coupon_template: 'VPN', coupon_ttl_hours: '48',
  };
}

/** "12 часов", "24 часа", "3 дня": whole days read as days. */
export function durationWords(hours: number): string {
  return hours > 0 && hours % 24 === 0 ? delayText(hours / 24, 'days') : hoursText(hours);
}

/**
 * Example recipient of every preview (never a real user). trial_days and
 * trial_duration come from the server's trial settings; the discount code is a
 * TEST- code generated by the server and never stored.
 */
export const PREVIEW_CONTEXT: WarmupPreviewContext = {
  telegram_id: 1234567890, username: 'bob', plan_name: 'Gold', expire_date: '2026-12-31T20:59:00Z',
};

/** Whether a failed write may have been committed: retry it with the same request key. */
export function isUncertain(error: unknown): boolean {
  if (!(error instanceof ApiError)) return true;
  return error.code === 'network' || error.code === 'timeout' || error.status === 408 || error.status === 429 || error.status >= 500;
}

/**
 * Request keys per action. The same payload of the same action reuses its key
 * until a definite outcome (success or a rejection), so an uncertain response
 * is retried as a replay and never applied twice.
 */
export class RequestKeys {
  readonly #pending = new Map<string, { payload: string; key: string }>();
  readonly #fresh: () => string;

  constructor(fresh: () => string = newRequestKey) {
    this.#fresh = fresh;
  }

  key(scope: string, payload: string): string {
    const pending = this.#pending.get(scope);
    if (pending && pending.payload === payload) return pending.key;
    const key = this.#fresh();
    this.#pending.set(scope, { payload, key });
    return key;
  }

  /** Forgets the key after a definite outcome; keeps it after an uncertain failure. */
  settle(scope: string, error?: unknown) {
    if (error === undefined || !isUncertain(error)) this.#pending.delete(scope);
  }
}

// =============================================================================
// Settings form
// =============================================================================

export interface SettingsForm {
  enabled: boolean;
  max_per_day: string;
  cooldown_hours: string;
  catchup_hours: string;
}

export type SettingsField = Exclude<keyof SettingsForm, 'enabled'>;

export const settingsForm = (s: WarmupSettings): SettingsForm => ({
  enabled: s.enabled, max_per_day: String(s.max_per_day), cooldown_hours: String(s.cooldown_hours), catchup_hours: String(s.catchup_hours),
});

export const sameSettingsForm = (a: SettingsForm, b: SettingsForm) =>
  a.enabled === b.enabled && a.max_per_day === b.max_per_day && a.cooldown_hours === b.cooldown_hours && a.catchup_hours === b.catchup_hours;

/** service.UpdateSettings bounds: max_per_day 1..20, cooldown 0..720, catch-up 1..72. */
export function settingsSave(f: SettingsForm, version: number, limits: WarmupLimits): { save: WarmupSettingsSave | null; errors: Partial<Record<SettingsField, string>> } {
  const errors: Partial<Record<SettingsField, string>> = {};
  const int = (field: SettingsField, min: number, max: number) => {
    const n = parseInteger(f[field]);
    if (n === null || n < min || n > max) { errors[field] = `Целое число от ${min} до ${max}.`; return 0; }
    return n;
  };
  const max_per_day = int('max_per_day', 1, limits.max_per_day_limit);
  const cooldown_hours = int('cooldown_hours', 0, limits.max_cooldown_hours);
  const catchup_hours = int('catchup_hours', 1, limits.max_catchup_hours);
  if (Object.keys(errors).length) return { save: null, errors };
  return { save: { version, enabled: f.enabled, max_per_day, cooldown_hours, catchup_hours }, errors };
}

// =============================================================================
// Routes: #/warmup, #/warmup/settings, #/warmup/deliveries, #/warmup/coupons,
// #/warmup/create and #/warmup/scenario/{id}
// =============================================================================

/** Pages reached from the tab bar of the section. */
export const WARMUP_TABS = ['overview', 'settings', 'deliveries', 'coupons'] as const;
export type WarmupTab = (typeof WARMUP_TABS)[number];

export const TAB_LABELS: Readonly<Record<WarmupTab, string>> = {
  overview: 'Сценарии', settings: 'Настройки', deliveries: 'История отправок', coupons: 'Купоны',
};

export type WarmupRoute =
  | { readonly view: WarmupTab }
  | { readonly view: 'create' }
  | { readonly view: 'scenario'; readonly scenarioId: number };

const WARMUP_ROUTE = /^warmup(?:\/(settings|deliveries|coupons|create)|\/scenario\/([1-9][0-9]{0,15}))?$/;

/** The route of a hash path without "#/"; null when it is not a warm-up address. */
export function parseWarmupRoute(path: string): WarmupRoute | null {
  const m = WARMUP_ROUTE.exec(path);
  if (!m) return null;
  if (m[2]) {
    const id = Number(m[2]);
    return Number.isSafeInteger(id) ? { view: 'scenario', scenarioId: id } : null;
  }
  const view = m[1];
  if (view === 'create') return { view: 'create' };
  return { view: view === 'settings' || view === 'deliveries' || view === 'coupons' ? view : 'overview' };
}

export function warmupHash(route: WarmupRoute): string {
  switch (route.view) {
    case 'scenario': return `#/warmup/scenario/${route.scenarioId}`;
    case 'overview': return '#/warmup';
    default: return `#/warmup/${route.view}`;
  }
}

/** Local date (yyyy-mm-dd) → RFC 3339 instant of its start, optionally days later. */
export function dayStartIso(value: string, plusDays = 0): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!m) return '';
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + plusDays);
  return Number.isNaN(d.getTime()) ? '' : d.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

export function formatBytes(n: number): string {
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toLocaleString('ru-RU', { maximumFractionDigits: 1 })} МБ`;
  if (n >= 1024) return `${Math.round(n / 1024).toLocaleString('ru-RU')} КБ`;
  return `${n} Б`;
}

/** File → base64 (without the data: prefix) for POST /images. */
export function fileToBase64(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = typeof reader.result === 'string' ? reader.result : '';
      const comma = result.indexOf(',');
      if (comma < 0) reject(new Error('read failed'));
      else resolve(result.slice(comma + 1));
    };
    reader.onerror = () => reject(reader.error ?? new Error('read failed'));
    reader.readAsDataURL(file);
  });
}
