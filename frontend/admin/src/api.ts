// Client for the browser-admin session contract served by internal/adminauth.
//
// The session cookie is HttpOnly and never visible here. The synchronizer CSRF
// token lives only in this module's memory: it is not persisted, logged or
// exposed, and it is replaced from every session payload the server returns.
// Passwords pass straight through to the request body and are never retained.

export class ApiError extends Error {
  constructor(
    readonly code: string,
    readonly status = 0,
    readonly retryAfter = 0,
    /** Offending input field named by the server (tariff editor: invalid_tariff). */
    readonly field = '',
  ) {
    super(code);
    this.name = 'ApiError';
  }
}

export interface Session {
  authenticated: boolean;
}

// GET /admin/api/dashboard (database.AdminDashboard): one snapshot of counters.
//   total_subscriptions  every subscription row, all statuses
//   users / trials       rows with telegram_id > 0 / < 0 (unbound trials)
//   active ... canceled  rows per status; together they sum to the total
//   active_expired       status active, expiry passed, not yet downgraded
//   paid                 product_id set or price_paid_cents > 0, any status
//   expiring_in_7d       status active, expiring within the next 7 days
//   audit_last_24h       admin panel audit entries in the last 24 hours
const DASHBOARD_FIELDS = [
  'total_subscriptions', 'users', 'trials',
  'active', 'paused', 'revoked', 'expired', 'canceled',
  'active_expired', 'paid', 'expiring_in_7d', 'audit_last_24h',
] as const;

export type Dashboard = Readonly<Record<(typeof DASHBOARD_FIELDS)[number], number>>;

// GET /admin/api/users and GET /admin/api/users/{telegram_id}
// (service.AdminUsersPage, service.AdminSubscriptionPage). A user is a
// subscription row with telegram_id > 0; telegram_id is unique, so every linked
// customer has exactly one row. Unbound trials (telegram_id < 0) never appear.

/** Values accepted by the status filter (service.isKnownSubscriptionStatus). */
export const SUBSCRIPTION_STATUSES = ['active', 'expired', 'paused', 'revoked', 'canceled'] as const;
export type SubscriptionStatus = (typeof SUBSCRIPTION_STATUSES)[number];

/** service.AdminSubscriptionView. Timestamps are RFC 3339 strings. */
export interface AdminSubscription {
  readonly id: number;
  readonly telegram_id: number;
  readonly username: string;
  /** Stored status; the known values are SUBSCRIPTION_STATUSES. */
  readonly status: string;
  /** null: the subscription never expires. */
  readonly expires_at: string | null;
  readonly plan_id: number;
  /** Omitted on the wire when the plan row is missing; '' here. */
  readonly plan_name: string;
  /** Set for provider-backed subscriptions, which use no service nodes. */
  readonly provider_source_id: number | null;
  readonly product_id: number | null;
  readonly is_paid: boolean;
  /** Minor units of currency; for XTR (Telegram Stars) whole Stars. */
  readonly price_paid_cents: number;
  readonly currency: string | null;
  readonly referred_by: number | null;
  readonly started_at: string | null;
  /** Last subscription feed request; null until the first one. */
  readonly last_request: string | null;
  readonly created_at: string;
  readonly updated_at: string;
  readonly reminders_sent: number;
  /** Registered device entries. */
  readonly devices: number;
  /** Recorded IP address entries. */
  readonly ips: number;
  /** Per-subscription builder override; null means use the plan's default. */
  readonly subscription_builder_id: number | null;
}

/** service.AdminUsersPage, newest first; limit is the page size the server applied. */
export interface UsersPage {
  readonly users: readonly AdminSubscription[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

/**
 * Query of GET /admin/api/users. q matches a numeric Telegram or subscription
 * ID exactly, otherwise a username substring (a leading @ is ignored). The page
 * size is left to the server default (database.AdminDefaultPageSize).
 */
export interface UsersQuery {
  readonly q: string;
  readonly status: SubscriptionStatus | '';
  readonly offset: number;
}

/** service.AdminPlanView. traffic_limit is in bytes, 0 means unlimited. */
export interface AdminPlan {
  readonly id: number;
  readonly name: string;
  readonly is_active: boolean;
  readonly devices_limit: number;
  readonly traffic_limit: number;
  /** Default builder for this plan; null means no builder assigned. */
  readonly subscription_builder_id: number | null;
}

/** database.AdminSubscriptionNode: sync state of the subscription on one VPN node. */
export interface AdminNode {
  readonly node_id: number;
  readonly node_name: string;
  /** database.SyncStatus: active, pending_add, pending_remove or pending_update. */
  readonly status: string;
  readonly retry_count: number;
  readonly retry_at: string | null;
  readonly last_error: string;
  readonly updated_at: string;
}

/**
 * GET /admin/api/users/{telegram_id}: the subscription page of the user. The
 * response also carries recent audit entries; they belong to the audit screen
 * and are not exposed here.
 */
export interface UserDetail {
  readonly subscription: AdminSubscription;
  readonly plan: AdminPlan | null;
  readonly nodes: readonly AdminNode[];
}

/**
 * Server-side input bounds in UTF-8 bytes: credentials from adminauth.login,
 * the users search query from web.adminAPI.users.
 */
export const limits = { usernameBytes: 64, passwordBytes: 72, queryBytes: 128 } as const;

// POST /admin/api/subscriptions/{id}/{renew|disable|enable|expiry}
// (web.adminAPI.mutate → service.AdminService.Mutate → database.applyAdminAction).
// Body: {request_key, days?, expires_at?}, unknown fields rejected, at most 4 KiB.
//   renew    days 1..3650 added to max(now, expires_at); expired → active,
//            paused stays paused; revoked/canceled and perpetual are rejected.
//   expiry   expires_at (RFC 3339) replaces the expiry; expired + future → active,
//            paused stays paused; revoked/canceled are rejected.
//   disable  active|expired → paused (VPN access removed on the nodes).
//   enable   paused → active, only while the expiry has not passed.
// A request key replays the recorded outcome for the same payload, so an
// uncertain request is retried with the SAME key and payload.

/** database.AdminMaxRenewalDays (MaxSubscriptionRenewalDays). */
export const RENEW_MAX_DAYS = 3650;

export type Mutation =
  | { readonly action: 'renew'; readonly days: number }
  | { readonly action: 'expiry'; readonly expiresAt: string }
  | { readonly action: 'disable' }
  | { readonly action: 'enable' };

export type MutationAction = Mutation['action'];

/** service.AdminMutationOutcome; the audit entry belongs to the audit screen. */
export interface MutationOutcome {
  /** As committed. plan_name is not part of the mutation response ('' here). */
  readonly subscription: AdminSubscription;
  /** true when the request key had already been applied. */
  readonly replayed: boolean;
}

/** RFC 3339 in UTC without fractional seconds, the form sent as expires_at. */
const EXPIRES_AT_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;

/**
 * A fresh idempotency key: printable ASCII, well under AdminMaxRequestKey.
 * getRandomValues works outside secure contexts, unlike randomUUID.
 */
export function newRequestKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return `ui-${Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')}`;
}

function mutationBody(mutation: Mutation, requestKey: string): string | null {
  switch (mutation.action) {
    case 'renew':
      if (!Number.isSafeInteger(mutation.days) || mutation.days < 1 || mutation.days > RENEW_MAX_DAYS) return null;
      return JSON.stringify({ request_key: requestKey, days: mutation.days });
    case 'expiry':
      if (!EXPIRES_AT_PATTERN.test(mutation.expiresAt) || Number.isNaN(Date.parse(mutation.expiresAt))) return null;
      return JSON.stringify({ request_key: requestKey, expires_at: mutation.expiresAt });
    case 'disable':
    case 'enable':
      return JSON.stringify({ request_key: requestKey });
  }
}

const CSRF_HEADER = 'X-CSRF-Token';
const REQUEST_TIMEOUT_MS = 12_000;
const DEFAULT_RETRY_AFTER_S = 60;

export function errorText(error: unknown): string {
  if (!(error instanceof ApiError)) return 'Не удалось выполнить действие. Попробуйте ещё раз.';
  switch (error.code) {
    case 'unauthorized': return 'Сессия недействительна. Войдите снова.';
    case 'forbidden': return 'Сервер отклонил запрос. Обновите страницу и повторите попытку.';
    case 'rate_limited': return `Слишком много запросов. Повторите через ${error.retryAfter || DEFAULT_RETRY_AFTER_S} с.`;
    case 'not_found': return 'Панель администратора отключена на сервере.';
    case 'network': return 'Нет связи с сервером. Проверьте подключение.';
    case 'timeout': return 'Сервер не ответил вовремя. Попробуйте ещё раз.';
    case 'invalid_response': return 'Не удалось прочитать ответ сервера.';
    case 'invalid_request': return 'Сервер отклонил параметры запроса. Измените условия поиска.';
    case 'service_unavailable': return 'Сервис временно недоступен. Попробуйте позже.';
    default: return 'Не удалось выполнить действие. Попробуйте ещё раз.';
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

// adminauth.randomToken: 32 random bytes, unpadded base64url (43 characters).
const CSRF_PATTERN = /^[A-Za-z0-9_-]{43}$/;

function isSessionPayload(value: unknown): value is { authenticated: boolean; csrf_token: string } {
  return isRecord(value) && typeof value.authenticated === 'boolean' &&
    typeof value.csrf_token === 'string' && CSRF_PATTERN.test(value.csrf_token);
}

function isDashboard(value: unknown): value is Dashboard {
  return isRecord(value) && DASHBOARD_FIELDS.every(key => {
    const count = value[key];
    return typeof count === 'number' && Number.isSafeInteger(count) && count >= 0;
  });
}

const isInteger = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value);
const isCount = (value: unknown): value is number => isInteger(value) && value >= 0;
const isPositive = (value: unknown): value is number => isInteger(value) && value > 0;
const isText = (value: unknown): value is string => typeof value === 'string';
// Go encodes time.Time as RFC 3339 with up to nine fractional digits.
const isTime = (value: unknown): value is string =>
  typeof value === 'string' && value.length <= 40 && !Number.isNaN(Date.parse(value));
const isCurrency = (value: unknown): value is string => typeof value === 'string' && /^[A-Z]{3}$/.test(value);

function isNullable<T>(value: unknown, check: (item: unknown) => item is T): value is T | null {
  return value === null || check(value);
}

function parseSubscription(value: unknown): AdminSubscription | null {
  if (!isRecord(value)) return null;
  const {
    id, telegram_id, username, status, expires_at, plan_id, plan_name = '', provider_source_id, product_id,
    is_paid, price_paid_cents, currency, referred_by, started_at, last_request, created_at, updated_at,
    reminders_sent, devices, ips, subscription_builder_id = null,
  } = value;
  if (!isPositive(id) || !isInteger(telegram_id) || !isText(username) || !isText(status) || status === '' ||
    !isNullable(expires_at, isTime) || !isCount(plan_id) || !isText(plan_name) ||
    !isNullable(provider_source_id, isPositive) || !isNullable(product_id, isPositive) ||
    typeof is_paid !== 'boolean' || !isCount(price_paid_cents) || !isNullable(currency, isCurrency) ||
    !isNullable(referred_by, isInteger) || !isNullable(started_at, isTime) || !isNullable(last_request, isTime) ||
    !isTime(created_at) || !isTime(updated_at) || !isCount(reminders_sent) || !isCount(devices) || !isCount(ips) ||
    !isNullable(subscription_builder_id, isPositive)) return null;
  return {
    id, telegram_id, username, status, expires_at, plan_id, plan_name, provider_source_id, product_id,
    is_paid, price_paid_cents, currency, referred_by, started_at, last_request, created_at, updated_at,
    reminders_sent, devices, ips, subscription_builder_id,
  };
}

function parseUsersPage(value: unknown): UsersPage | null {
  if (!isRecord(value)) return null;
  const { users: list, total, limit, offset } = value;
  if (!Array.isArray(list) || !isCount(total) || !isPositive(limit) || !isCount(offset) ||
    list.length > limit || list.length > total) return null;
  const users: AdminSubscription[] = [];
  for (const item of list) {
    const user = parseSubscription(item);
    // The list only ever holds linked customers.
    if (!user || user.telegram_id <= 0) return null;
    users.push(user);
  }
  return { users, total, limit, offset };
}

function parsePlan(value: unknown): AdminPlan | undefined {
  if (!isRecord(value)) return undefined;
  const { id, name, is_active, devices_limit, traffic_limit, subscription_builder_id = null } = value;
  if (!isPositive(id) || !isText(name) || typeof is_active !== 'boolean' || !isCount(devices_limit) ||
    !isCount(traffic_limit) || !isNullable(subscription_builder_id, isPositive)) return undefined;
  return { id, name, is_active, devices_limit, traffic_limit, subscription_builder_id };
}

function parseNode(value: unknown): AdminNode | null {
  if (!isRecord(value)) return null;
  const { node_id, node_name, status, retry_count, retry_at, last_error, updated_at } = value;
  if (!isPositive(node_id) || !isText(node_name) || !isText(status) || status === '' || !isCount(retry_count) ||
    !isNullable(retry_at, isTime) || !isText(last_error) || !isTime(updated_at)) return null;
  return { node_id, node_name, status, retry_count, retry_at, last_error, updated_at };
}

function parseUserDetail(value: unknown, telegramId: number): UserDetail | null {
  if (!isRecord(value) || !Array.isArray(value.nodes) || !Array.isArray(value.audit)) return null;
  const subscription = parseSubscription(value.subscription);
  // The route resolves by Telegram ID; any other row is not this user.
  if (!subscription || subscription.telegram_id !== telegramId) return null;
  const plan = value.plan === null ? null : parsePlan(value.plan);
  if (plan === undefined) return null;
  const nodes: AdminNode[] = [];
  for (const item of value.nodes) {
    const node = parseNode(item);
    if (!node) return null;
    nodes.push(node);
  }
  return { subscription, plan, nodes };
}

function parseOutcome(value: unknown, id: number): MutationOutcome | null {
  if (!isRecord(value) || typeof value.replayed !== 'boolean' || !isRecord(value.audit)) return null;
  const subscription = parseSubscription(value.subscription);
  if (!subscription || subscription.id !== id) return null;
  return { subscription, replayed: value.replayed };
}

function errorCode(data: unknown, status: number): string {
  if (isRecord(data) && typeof data.error === 'string' && /^[a-z_]{1,40}$/.test(data.error)) return data.error;
  // Non-JSON failures come from the proxy in front of the bot, not adminauth.
  if (status === 401) return 'unauthorized';
  if (status === 403) return 'forbidden';
  if (status === 404) return 'not_found';
  if (status === 429) return 'rate_limited';
  return 'service_unavailable';
}

function retryAfterSeconds(response: Response): number {
  const seconds = Number(response.headers.get('Retry-After'));
  return Number.isFinite(seconds) && seconds > 0 ? Math.min(Math.ceil(seconds), 3600) : DEFAULT_RETRY_AFTER_S;
}

const isForbidden = (error: unknown) => error instanceof ApiError && error.status === 403;

export class AdminApi {
  #csrf = '';
  readonly #transport: typeof fetch;

  constructor(transport: typeof fetch = fetch) {
    this.#transport = transport;
  }

  /** GET /admin/login: issues or resumes the browser session and its CSRF token. */
  async bootstrap(): Promise<Session> {
    return this.#adopt(await this.#send('GET', '/admin/login'));
  }

  /** GET /admin/session: reports the current session without extending it. */
  async session(): Promise<Session> {
    try {
      return this.#adopt(await this.#send('GET', '/admin/session'));
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        this.#csrf = '';
        return { authenticated: false };
      }
      throw error;
    }
  }

  /**
   * POST /admin/login. A pre-login session expires after ten minutes, so a
   * login screen left open answers 403; one fresh bootstrap recovers it.
   */
  async login(username: string, password: string): Promise<Session> {
    const body = JSON.stringify({ username, password });
    let session: Session;
    try {
      session = this.#adopt(await this.#send('POST', '/admin/login', body));
    } catch (error) {
      if (!isForbidden(error)) throw error;
      if ((await this.bootstrap()).authenticated) return { authenticated: true };
      session = this.#adopt(await this.#send('POST', '/admin/login', body));
    }
    if (!session.authenticated) throw new ApiError('invalid_response');
    return session;
  }

  /**
   * POST /admin/logout. Resolves once the server session is gone: an already
   * expired session (401) counts as signed out, and a stale CSRF token (403)
   * is refreshed once so a live session is never left behind.
   */
  async logout(): Promise<void> {
    try {
      await this.#send('POST', '/admin/logout');
    } catch (error) {
      if (!(error instanceof ApiError)) throw error;
      if (error.status === 403) {
        if ((await this.bootstrap()).authenticated) await this.#send('POST', '/admin/logout');
      } else if (error.status !== 401) {
        throw error;
      }
    }
    this.#csrf = '';
  }

  /** GET /admin/api/dashboard: overview counters. 401 means the session ended. */
  async dashboard(): Promise<Dashboard> {
    const data = await this.#send('GET', '/admin/api/dashboard');
    if (!isDashboard(data)) throw new ApiError('invalid_response');
    return data;
  }

  /** GET /admin/api/users: one page of linked customers. 401 means the session ended. */
  async users(query: UsersQuery): Promise<UsersPage> {
    const params = new URLSearchParams();
    if (query.q) params.set('q', query.q);
    if (query.status) params.set('status', query.status);
    if (query.offset > 0) params.set('offset', String(query.offset));
    const search = params.toString();
    const page = parseUsersPage(await this.#send('GET', search ? `/admin/api/users?${search}` : '/admin/api/users'));
    if (!page) throw new ApiError('invalid_response');
    return page;
  }

  /** GET /admin/api/users/{telegram_id}. 404 means no linked customer has this ID. */
  async user(telegramId: number): Promise<UserDetail> {
    // The route only accepts canonical positive IDs and answers 404 otherwise.
    if (!Number.isSafeInteger(telegramId) || telegramId <= 0) throw new ApiError('not_found', 404);
    const detail = parseUserDetail(await this.#send('GET', `/admin/api/users/${telegramId}`), telegramId);
    if (!detail) throw new ApiError('invalid_response');
    return detail;
  }

  /**
   * GET /admin/api/subscriptions/{id}: the same page as user(), addressed by
   * subscription. The row must still belong to the expected Telegram ID.
   */
  async subscription(id: number, telegramId: number): Promise<UserDetail> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    const detail = parseUserDetail(await this.#send('GET', `/admin/api/subscriptions/${id}`), telegramId);
    if (!detail || detail.subscription.id !== id) throw new ApiError('invalid_response');
    return detail;
  }

  /**
   * One audited lifecycle mutation. Rejections surface as ApiError with the
   * backend code: 409 invalid_state, subscription_expired,
   * perpetual_subscription, invalid_expiry (recorded, nothing changed) or
   * request_key_conflict; 500 sync_failed means the change IS committed but
   * node sync setup failed — retry with the same key. Never retried here.
   */
  async mutate(id: number, mutation: Mutation, requestKey: string): Promise<MutationOutcome> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    const body = mutationBody(mutation, requestKey);
    if (body === null) throw new ApiError('invalid_request', 400);
    const outcome = parseOutcome(await this.#send('POST', `/admin/api/subscriptions/${id}/${mutation.action}`, body), id);
    if (!outcome) throw new ApiError('invalid_response');
    return outcome;
  }

  #adopt(payload: unknown): Session {
    if (!isSessionPayload(payload)) throw new ApiError('invalid_response');
    this.#csrf = payload.csrf_token;
    return { authenticated: payload.authenticated };
  }

  async #send(method: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE', path: string, body?: string): Promise<unknown> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (method !== 'GET' && this.#csrf) headers[CSRF_HEADER] = this.#csrf;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    try {
      // Native fetch must not be invoked with this instance as its receiver.
      const transport = this.#transport;
      const response = await transport(path, {
        method, headers, body,
        credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: controller.signal,
      });
      if (response.status === 204) return null;
      const data: unknown = await response.json().catch(() => undefined);
      if (!response.ok) {
        const retryAfter = response.status === 429 ? retryAfterSeconds(response) : 0;
        const field = isRecord(data) && typeof data.field === 'string' && /^[a-z_]{1,40}$/.test(data.field) ? data.field : '';
        throw new ApiError(errorCode(data, response.status), response.status, retryAfter, field);
      }
      return data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(controller.signal.aborted ? 'timeout' : 'network');
    } finally {
      clearTimeout(timer);
    }
  }
  // ---------------------------------------------------------------------------
  // Sources
  // ---------------------------------------------------------------------------

  async listSources(): Promise<readonly AdminSource[]> {
    const data = await this.#send('GET', '/admin/api/sources');
    if (!isRecord(data) || !Array.isArray(data.sources)) throw new ApiError('invalid_response');
    return data.sources as AdminSource[];
  }

  async getSource(id: number): Promise<AdminSource> {
    const data = await this.#send('GET', `/admin/api/sources/${id}`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminSource;
  }

  async createSource(input: CreateSourceInput): Promise<AdminSource> {
    const data = await this.#send('POST', '/admin/api/sources', JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminSource;
  }

  async updateSource(id: number, input: UpdateSourceInput): Promise<AdminSource> {
    const data = await this.#send('PATCH', `/admin/api/sources/${id}`, JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminSource;
  }

  async enableSource(id: number): Promise<AdminSource> {
    const data = await this.#send('POST', `/admin/api/sources/${id}/enable`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminSource;
  }

  async disableSource(id: number): Promise<AdminSource> {
    const data = await this.#send('POST', `/admin/api/sources/${id}/disable`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminSource;
  }

  async listSourceEntries(id: number, country = '*'): Promise<readonly SourceEntry[]> {
    const params = `?country=${encodeURIComponent(country)}`;
    const data = await this.#send('GET', `/admin/api/sources/${id}/entries${params}`);
    if (!isRecord(data) || !Array.isArray(data.entries)) throw new ApiError('invalid_response');
    return data.entries as SourceEntry[];
  }

  async refreshSource(id: number, input: RefreshSourceInput): Promise<AdminSource> {
    const data = await this.#send('POST', `/admin/api/sources/${id}/refresh`, JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminSource;
  }

  // ---------------------------------------------------------------------------
  // Builders
  // ---------------------------------------------------------------------------

  async listBuilders(): Promise<readonly AdminBuilder[]> {
    const data = await this.#send('GET', '/admin/api/builders');
    if (!isRecord(data) || !Array.isArray(data.builders)) throw new ApiError('invalid_response');
    return data.builders as AdminBuilder[];
  }

  async getBuilder(id: number): Promise<AdminBuilder> {
    const data = await this.#send('GET', `/admin/api/builders/${id}`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminBuilder;
  }

  async createBuilder(input: CreateBuilderInput): Promise<{ builder: AdminBuilder; audit: unknown }> {
    const data = await this.#send('POST', '/admin/api/builders', JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as { builder: AdminBuilder; audit: unknown };
  }

  async updateBuilder(id: number, input: UpdateBuilderInput): Promise<{ builder: AdminBuilder; audit: unknown }> {
    const data = await this.#send('PATCH', `/admin/api/builders/${id}`, JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as { builder: AdminBuilder; audit: unknown };
  }

  async enableBuilder(id: number): Promise<AdminBuilder> {
    const data = await this.#send('POST', `/admin/api/builders/${id}/enable`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminBuilder;
  }

  async disableBuilder(id: number): Promise<AdminBuilder> {
    const data = await this.#send('POST', `/admin/api/builders/${id}/disable`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminBuilder;
  }

  async setBuilderSources(id: number, sourceIds: number[]): Promise<AdminBuilder> {
    const data = await this.#send('PUT', `/admin/api/builders/${id}/sources`, JSON.stringify({ source_ids: sourceIds }));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AdminBuilder;
  }

  async upsertBuilderItem(id: number, input: UpsertBuilderItemInput): Promise<BuilderItem> {
    const data = await this.#send('POST', `/admin/api/builders/${id}/items`, JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as BuilderItem;
  }

  async deleteBuilderItem(builderId: number, itemId: number): Promise<void> {
    await this.#send('DELETE', `/admin/api/builders/${builderId}/items/${itemId}`);
  }

  async reorderBuilderItems(id: number, itemIds: number[]): Promise<void> {
    await this.#send('POST', `/admin/api/builders/${id}/reorder`, JSON.stringify({ item_ids: itemIds }));
  }

  async previewBuilder(id: number): Promise<BuilderPreview> {
    const data = await this.#send('POST', `/admin/api/builders/${id}/preview`);
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as BuilderPreview;
  }

  // ---------------------------------------------------------------------------
  // Assignments
  // ---------------------------------------------------------------------------

  async setPlanBuilder(planId: number, input: SetBuilderInput): Promise<AssignmentAudit> {
    const data = await this.#send('POST', `/admin/api/plans/${planId}/builder`, JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AssignmentAudit;
  }

  async setSubscriptionBuilder(subscriptionId: number, input: SetBuilderInput): Promise<AssignmentAudit> {
    const data = await this.#send('POST', `/admin/api/subscriptions/${subscriptionId}/builder`, JSON.stringify(input));
    if (!isRecord(data)) throw new ApiError('invalid_response');
    return data as unknown as AssignmentAudit;
  }

  // ---------------------------------------------------------------------------
  // Tariffs (web.adminAPI.routeTariffs → service.TariffService). Every write
  // carries a request_key: the same key with the same payload replays the
  // committed outcome, so an uncertain request is retried with the SAME key.
  // Rejections: 400 invalid_tariff (+field) | invalid_request, 404 not_found,
  // 409 version_conflict | tariff_in_use | tariff_superseded | order_stale |
  // request_key_conflict.
  // ---------------------------------------------------------------------------

  /** GET /admin/api/tariffs: every product in catalogue order, retired versions included. */
  async listTariffs(): Promise<readonly AdminTariff[]> {
    const tariffs = parseTariffList(await this.#send('GET', '/admin/api/tariffs'));
    if (!tariffs) throw new ApiError('invalid_response');
    return tariffs;
  }

  /** GET /admin/api/tariffs/{id}. */
  async getTariff(id: number): Promise<AdminTariff> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    const tariff = parseTariff(await this.#send('GET', `/admin/api/tariffs/${id}`));
    if (!tariff || tariff.id !== id) throw new ApiError('invalid_response');
    return tariff;
  }

  /** GET /admin/api/plans: the plan picker (system plans are not selectable). */
  async listPlans(): Promise<readonly TariffPlan[]> {
    const data = await this.#send('GET', '/admin/api/plans');
    if (!isRecord(data) || !Array.isArray(data.plans)) throw new ApiError('invalid_response');
    const plans: TariffPlan[] = [];
    for (const item of data.plans) {
      const plan = parseTariffPlan(item);
      if (!plan) throw new ApiError('invalid_response');
      plans.push(plan);
    }
    return plans;
  }

  /** POST /admin/api/tariffs. A null sort_order appends the tariff to the catalogue. */
  async createTariff(input: TariffInput, requestKey: string): Promise<TariffOutcome> {
    return this.#tariffOutcome(await this.#send('POST', '/admin/api/tariffs', tariffBody(requestKey, input)));
  }

  /**
   * PATCH /admin/api/tariffs/{id}. For a used tariff, changing the purchase
   * terms creates a successor (outcome.versioned, outcome.previous).
   */
  async updateTariff(id: number, version: number, input: TariffInput, requestKey: string): Promise<TariffOutcome> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    return this.#tariffOutcome(await this.#send('PATCH', `/admin/api/tariffs/${id}`, tariffBody(requestKey, input, version)));
  }

  /** POST /admin/api/tariffs/{id}/enable|disable. */
  async setTariffActive(id: number, version: number, active: boolean, requestKey: string): Promise<TariffOutcome> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    const body = JSON.stringify({ request_key: requestKey, version });
    return this.#tariffOutcome(await this.#send('POST', `/admin/api/tariffs/${id}/${active ? 'enable' : 'disable'}`, body));
  }

  /** POST /admin/api/tariffs/reorder: ids must list every tariff exactly once. */
  async reorderTariffs(ids: readonly number[], requestKey: string): Promise<readonly AdminTariff[]> {
    const tariffs = parseTariffList(await this.#send('POST', '/admin/api/tariffs/reorder', JSON.stringify({ request_key: requestKey, ids })));
    if (!tariffs) throw new ApiError('invalid_response');
    return tariffs;
  }

  /** DELETE /admin/api/tariffs/{id}: only unused tariffs (409 tariff_in_use otherwise). */
  async deleteTariff(id: number, version: number, requestKey: string): Promise<void> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    const data = await this.#send('DELETE', `/admin/api/tariffs/${id}`, JSON.stringify({ request_key: requestKey, version }));
    if (!isRecord(data) || data.deleted !== id) throw new ApiError('invalid_response');
  }

  #tariffOutcome(data: unknown): TariffOutcome {
    if (!isRecord(data) || typeof data.versioned !== 'boolean' || typeof data.replayed !== 'boolean') throw new ApiError('invalid_response');
    const tariff = data.tariff === null ? null : parseTariff(data.tariff);
    const previous = data.previous === null || data.previous === undefined ? null : parseTariff(data.previous);
    if (tariff === undefined || previous === undefined) throw new ApiError('invalid_response');
    return { tariff, previous, versioned: data.versioned, replayed: data.replayed };
  }

}

// =============================================================================
// Builder / Source types (used by api methods above)
// =============================================================================

export interface AdminSource {
  readonly id: number;
  readonly name: string;
  readonly type: string;
  readonly description: string;
  readonly enabled: boolean;
  readonly last_sync_at: string | null;
  readonly last_sync_status: string;
  readonly last_sync_error: string;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface SourceEntry {
  readonly id: number;
  readonly source_id: number;
  readonly fingerprint: string;
  readonly original_name: string;
  readonly protocol: string;
  readonly country_code: string;
  readonly upstream_position: number;
  readonly present: boolean;
  readonly last_seen_at: string;
}

export interface CreateSourceInput {
  readonly name: string;
  readonly description: string;
  readonly type: string;
  readonly subscription_url: string;
  readonly hwid: string;
  readonly user_agent: string;
  readonly headers: string;
  readonly enabled: boolean;
}

export interface UpdateSourceInput {
  readonly name: string;
  readonly description: string;
  readonly type: string;
}

export interface RefreshSourceInput {
  readonly entries: readonly { fingerprint: string; original_name: string; protocol: string; country_code: string }[];
  readonly sync_status: string;
  readonly sync_error: string;
}

export interface BuilderSource {
  readonly source_id: number;
  readonly position: number;
}

export interface BuilderItem {
  readonly id: number;
  readonly kind: 'country' | 'node';
  readonly source_id: number;
  readonly country_code: string;
  readonly fingerprint: string;
  readonly original_name: string;
  readonly custom_name: string | null;
  readonly description: string;
  readonly position: number;
  readonly enabled: boolean;
}

export interface AdminBuilder {
  readonly id: number;
  readonly name: string;
  readonly description: string;
  readonly enabled: boolean;
  readonly profile_title: string;
  readonly support_url: string;
  readonly announce: string;
  readonly version: number;
  readonly sources: readonly BuilderSource[] | null;
  readonly items: readonly BuilderItem[] | null;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface CreateBuilderInput {
  readonly request_key: string;
  readonly name: string;
  readonly description: string;
  readonly enabled: boolean;
  readonly profile_title: string;
  readonly support_url: string;
  readonly announce: string;
}

export interface UpdateBuilderInput {
  readonly request_key: string;
  readonly version: number;
  readonly name: string;
  readonly description: string;
  readonly enabled: boolean;
  readonly profile_title: string;
  readonly support_url: string;
  readonly announce: string;
}

export interface UpsertBuilderItemInput {
  readonly id?: number;
  readonly kind: 'country' | 'node';
  readonly source_id: number;
  readonly country_code?: string;
  readonly fingerprint?: string;
  readonly original_name?: string;
  readonly custom_name?: string | null;
  readonly description?: string;
  readonly position: number;
  readonly enabled: boolean;
}

export type FingerprintStatus = 'matched' | 'fallback' | 'missing' | 'conflict';

export interface PreviewItem {
  readonly item_id: number;
  readonly kind: string;
  readonly source_id: number;
  readonly entry: SourceEntry | null;
  readonly display_name: string;
  readonly status: FingerprintStatus;
  readonly position: number;
}

export interface BuilderPreview {
  readonly builder_id: number;
  readonly items: readonly PreviewItem[];
  readonly warnings: readonly string[] | null;
  readonly missing: number;
  readonly conflicts: number;
  readonly total: number;
  readonly previewed_at: string;
}

export interface SetBuilderInput {
  readonly request_key: string;
  readonly builder_id: number | null;
}

export interface AssignmentAudit {
  readonly audit: {
    readonly id: number;
    readonly actor: string;
    readonly action: string;
    readonly target_type: string;
    readonly target_id: number;
    readonly created_at: string;
    readonly success: boolean;
  };
}

// =============================================================================
// Tariff editor types (service.TariffView, service.TariffPlanView)
// =============================================================================

/** Server-side bounds (database.Tariff* constants), lengths in characters. */
export const TARIFF_LIMITS = {
  name: 64, description: 500, features: 8, feature: 80, badge: 24,
  maxPriceCents: 100_000_000, maxDurationDays: RENEW_MAX_DAYS, maxSortOrder: 100_000,
} as const;

/**
 * A tariff is a Product. Purchase terms (name, plan, duration, price,
 * currency) are frozen once orders or subscriptions reference it (in_use):
 * changing them creates a successor version. Prices are minor units; for XTR
 * (Telegram Stars) whole Stars.
 */
export interface AdminTariff {
  readonly id: number;
  readonly offer_id: string;
  readonly name: string;
  readonly plan_id: number;
  readonly plan_name: string;
  readonly plan_active: boolean;
  /** The plan's default builder; builders are assigned per plan, never per tariff. */
  readonly builder_id: number | null;
  readonly duration_days: number;
  readonly price_cents: number;
  readonly currency: string;
  readonly is_active: boolean;
  readonly description: string;
  readonly features: readonly string[];
  readonly badge: string;
  readonly sort_order: number;
  /** Optimistic-locking version, sent back with every change. */
  readonly version: number;
  /** The tariff this one replaced (versioning). */
  readonly previous_id: number | null;
  /** Set on a retired version: it is frozen, edit the successor instead. */
  readonly replaced_by_id: number | null;
  readonly orders: number;
  readonly subscriptions: number;
  readonly in_use: boolean;
  readonly offer_ends_at: string | null;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface TariffPlan {
  readonly id: number;
  readonly name: string;
  readonly is_active: boolean;
  readonly devices_limit: number;
  readonly traffic_limit: number;
  readonly subscription_builder_id: number | null;
  readonly builder_name: string;
  /** Tariffs (all versions) and subscriptions on this plan. */
  readonly tariffs: number;
  readonly subscriptions: number;
  /** false for the system trial/free plans. */
  readonly selectable: boolean;
}

/** Editable content of a tariff (web.tariffBody without request_key/version). */
export interface TariffInput {
  readonly name: string;
  readonly plan_id: number;
  readonly duration_days: number;
  readonly price_cents: number;
  readonly currency: string;
  readonly description: string;
  readonly features: readonly string[];
  readonly badge: string;
  readonly is_active: boolean;
  /** Omitted/null: append on create, keep the position on update. */
  readonly sort_order?: number | null;
}

export interface TariffOutcome {
  /** The resulting tariff: the successor when versioned. */
  readonly tariff: AdminTariff | null;
  /** The retired original when versioned. */
  readonly previous: AdminTariff | null;
  readonly versioned: boolean;
  readonly replayed: boolean;
}

function tariffBody(requestKey: string, input: TariffInput, version?: number): string {
  // Exactly the fields web.tariffBody accepts: unknown fields are rejected.
  const body: Record<string, unknown> = {
    request_key: requestKey, name: input.name, plan_id: input.plan_id, duration_days: input.duration_days,
    price_cents: input.price_cents, currency: input.currency, description: input.description,
    features: input.features, badge: input.badge, is_active: input.is_active,
  };
  if (input.sort_order !== undefined && input.sort_order !== null) body.sort_order = input.sort_order;
  if (version !== undefined) body.version = version;
  return JSON.stringify(body);
}

const isStringList = (value: unknown): value is string[] => Array.isArray(value) && value.every(item => typeof item === 'string');

function parseTariff(value: unknown): AdminTariff | undefined {
  if (!isRecord(value)) return undefined;
  const {
    id, offer_id, name, plan_id, plan_name, plan_active, builder_id = null, duration_days, price_cents, currency,
    is_active, description, features, badge, sort_order, version, previous_id = null, replaced_by_id = null,
    orders, subscriptions, in_use, offer_ends_at = null, created_at, updated_at,
  } = value;
  if (!isPositive(id) || !isText(offer_id) || !isText(name) || !isCount(plan_id) || !isText(plan_name) ||
    typeof plan_active !== 'boolean' || !isNullable(builder_id, isPositive) || !isCount(duration_days) ||
    !isCount(price_cents) || !isCurrency(currency) || typeof is_active !== 'boolean' || !isText(description) ||
    !isStringList(features) || !isText(badge) || !isCount(sort_order) || !isPositive(version) ||
    !isNullable(previous_id, isPositive) || !isNullable(replaced_by_id, isPositive) || !isCount(orders) ||
    !isCount(subscriptions) || typeof in_use !== 'boolean' || !isNullable(offer_ends_at, isTime) ||
    !isTime(created_at) || !isTime(updated_at)) return undefined;
  return {
    id, offer_id, name, plan_id, plan_name, plan_active, builder_id, duration_days, price_cents, currency,
    is_active, description, features, badge, sort_order, version, previous_id, replaced_by_id,
    orders, subscriptions, in_use, offer_ends_at, created_at, updated_at,
  };
}

function parseTariffList(value: unknown): AdminTariff[] | null {
  if (!isRecord(value) || !Array.isArray(value.tariffs)) return null;
  const tariffs: AdminTariff[] = [];
  for (const item of value.tariffs) {
    const tariff = parseTariff(item);
    if (!tariff) return null;
    tariffs.push(tariff);
  }
  return tariffs;
}

function parseTariffPlan(value: unknown): TariffPlan | undefined {
  if (!isRecord(value)) return undefined;
  const {
    id, name, is_active, devices_limit, traffic_limit, subscription_builder_id = null, builder_name = '',
    tariffs, subscriptions, selectable,
  } = value;
  if (!isPositive(id) || !isText(name) || typeof is_active !== 'boolean' || !isCount(devices_limit) ||
    !isCount(traffic_limit) || !isNullable(subscription_builder_id, isPositive) || !isText(builder_name) ||
    !isCount(tariffs) || !isCount(subscriptions) || typeof selectable !== 'boolean') return undefined;
  return { id, name, is_active, devices_limit, traffic_limit, subscription_builder_id, builder_name, tariffs, subscriptions, selectable };
}
