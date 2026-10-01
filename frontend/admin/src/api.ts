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
    case 'invalid_source': return 'Проверьте параметры источника.';
    case 'sync_in_progress': return 'Источник уже синхронизируется. Дождитесь завершения.';
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

  /**
   * GET /admin/api/journal: one page of journal events, newest first. The
   * journal is read-only here: events are written by the backend itself.
   */
  async journal(query: JournalQuery): Promise<JournalPage> {
    const params = new URLSearchParams();
    if (query.q) params.set('q', query.q);
    if (query.type) params.set('type', query.type);
    if (query.planKind) params.set('plan_kind', query.planKind);
    if (query.from) params.set('from', query.from);
    if (query.to) params.set('to', query.to);
    if (query.offset > 0) params.set('offset', String(query.offset));
    const search = params.toString();
    const page = parseJournalPage(await this.#send('GET', search ? `/admin/api/journal?${search}` : '/admin/api/journal'));
    if (!page) throw new ApiError('invalid_response');
    return page;
  }

  /** GET /admin/api/monitoring: every builder with its current network state. */
  async monitoring(): Promise<MonitorOverview> {
    const overview = parseMonitorOverview(await this.#send('GET', '/admin/api/monitoring'));
    if (!overview) throw new ApiError('invalid_response');
    return overview;
  }

  /** GET /admin/api/monitoring/builders/{id}: countries and servers of one builder. */
  async monitoringBuilder(id: number): Promise<MonitorBuilderDetail> {
    if (!Number.isSafeInteger(id) || id <= 0) throw new ApiError('not_found', 404);
    const detail = parseMonitorBuilderDetail(await this.#send('GET', `/admin/api/monitoring/builders/${id}`), id);
    if (!detail) throw new ApiError('invalid_response');
    return detail;
  }

  /** GET /admin/api/monitoring/history: outages and latency of a builder, country or server. */
  async monitoringHistory(query: MonitorHistoryQuery): Promise<MonitorHistory> {
    const history = parseMonitorHistory(await this.#send('GET', monitorHistoryPath(query)), query.period);
    if (!history) throw new ApiError('invalid_response');
    return history;
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

  async #send(method: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE', path: string, body?: string, timeoutMs = REQUEST_TIMEOUT_MS): Promise<unknown> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
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

  /** Creates a source; the server runs the initial catalogue sync right away. */
  async createSource(input: CreateSourceInput): Promise<SourceCreated> {
    const data = await this.#send('POST', '/admin/api/sources', JSON.stringify(input), SOURCE_SYNC_TIMEOUT_MS);
    if (!isRecord(data) || !isSource(data.source)) throw new ApiError('invalid_response');
    const sync = data.sync === null || data.sync === undefined ? null : parseSyncResult(data.sync);
    return { source: data.source, sync };
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

  /** Present entries of a source (optionally one country), in upstream order. */
  async listSourceEntries(id: number, country = '*'): Promise<readonly SourceEntry[]> {
    const params = `?country=${encodeURIComponent(country)}`;
    const data = await this.#send('GET', `/admin/api/sources/${id}/entries${params}`);
    if (!isRecord(data) || !Array.isArray(data.entries) || !data.entries.every(isSourceEntry)) throw new ApiError('invalid_response');
    return data.entries;
  }

  /** Every catalogue entry of a source, including those that disappeared upstream. */
  async listAllSourceEntries(id: number): Promise<readonly SourceEntry[]> {
    const data = await this.#send('GET', `/admin/api/sources/${id}/entries?all=1`);
    if (!isRecord(data) || !Array.isArray(data.entries) || !data.entries.every(isSourceEntry)) throw new ApiError('invalid_response');
    return data.entries;
  }

  /**
   * Server-side catalogue sync: the backend fetches and parses the upstream.
   * A failed fetch is a result with status "error", not a thrown error.
   */
  async syncSource(id: number): Promise<SourceSyncResult> {
    return parseSyncResult(await this.#send('POST', `/admin/api/sources/${id}/refresh`, '{}', SOURCE_SYNC_TIMEOUT_MS));
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

/** database.SourceCatalogueStats: the present catalogue of ONE source. */
export interface SourceCatalogue {
  readonly entries: number;
  readonly countries: number;
  readonly no_country: number;
  readonly absent: number;
  readonly by_country: readonly { readonly code: string; readonly count: number }[];
  readonly protocols: Readonly<Record<string, number>>;
}

export interface AdminSource {
  readonly id: number;
  readonly name: string;
  /** Expected response format: auto | json | base64 | plain | clash. */
  readonly type: string;
  readonly description: string;
  readonly enabled: boolean;
  readonly last_sync_at: string | null;
  /** '' (never synced) | ok | partial | error. */
  readonly last_sync_status: string;
  /** Stable code: failure code (error) or "skipped:N" (partial). */
  readonly last_sync_error: string;
  readonly catalogue: SourceCatalogue;
  readonly created_at: string;
  readonly updated_at: string;
}

/** service.SourceSyncResult. */
export interface SourceSyncResult {
  readonly source: AdminSource;
  readonly status: 'ok' | 'partial' | 'error';
  readonly error: string;
  readonly format: string;
  readonly added: number;
  readonly updated: number;
  readonly removed: number;
  readonly total: number;
  readonly skipped: number;
  readonly duplicates: number;
}

export interface SourceCreated {
  readonly source: AdminSource;
  /** Initial sync; null only when the server could not run it. */
  readonly sync: SourceSyncResult | null;
}

/** Server sync timeout (30 s) plus transport margin. */
const SOURCE_SYNC_TIMEOUT_MS = 35_000;

function isCatalogue(v: unknown): v is SourceCatalogue {
  return isRecord(v) && isCount(v.entries) && isCount(v.countries) && isCount(v.no_country) && isCount(v.absent) &&
    Array.isArray(v.by_country) && v.by_country.every(c => isRecord(c) && typeof c.code === 'string' && isCount(c.count)) &&
    isRecord(v.protocols) && Object.values(v.protocols).every(isCount);
}

function isSource(v: unknown): v is AdminSource {
  return isRecord(v) && isCount(v.id) && typeof v.name === 'string' && typeof v.type === 'string' &&
    typeof v.enabled === 'boolean' && (v.last_sync_at === null || typeof v.last_sync_at === 'string') &&
    typeof v.last_sync_status === 'string' && typeof v.last_sync_error === 'string' && isCatalogue(v.catalogue);
}

function isSourceEntry(v: unknown): v is SourceEntry {
  return isRecord(v) && isCount(v.id) && isCount(v.source_id) && typeof v.fingerprint === 'string' &&
    typeof v.original_name === 'string' && typeof v.protocol === 'string' && typeof v.country_code === 'string' &&
    isCount(v.upstream_position) && typeof v.present === 'boolean' && typeof v.last_seen_at === 'string';
}

function parseSyncResult(v: unknown): SourceSyncResult {
  if (!isRecord(v) || !isSource(v.source) || (v.status !== 'ok' && v.status !== 'partial' && v.status !== 'error') ||
    typeof v.error !== 'string' || typeof v.format !== 'string' ||
    ![v.added, v.updated, v.removed, v.total, v.skipped, v.duplicates].every(isCount)) {
    throw new ApiError('invalid_response');
  }
  return v as unknown as SourceSyncResult;
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

// =============================================================================
// Journal (web.adminAPI.journalList → service.JournalPage, migration 047)
// =============================================================================

/** database.JournalEventTypes, in the order the filter offers them. */
export const JOURNAL_EVENT_TYPES = [
  'user_registered', 'trial_started', 'trial_bound', 'trial_expired',
  'subscription_reconnected', 'free_activated', 'paid_activated', 'plan_changed',
  'subscription_renewed', 'expiry_changed', 'subscription_expired',
  'subscription_disabled', 'subscription_enabled', 'subscription_revoked',
  'payment_succeeded', 'payment_failed', 'payment_refunded',
] as const;
export type JournalEventType = (typeof JOURNAL_EVENT_TYPES)[number];

/** database.PlanKind* — the subscription type filter. */
export const PLAN_KINDS = ['free', 'trial', 'paid'] as const;
export type PlanKind = (typeof PLAN_KINDS)[number];

/** database.JournalState: lifecycle snapshot in details.before / details.after. */
export interface JournalState {
  readonly status: string;
  readonly expires_at: string | null;
  readonly plan_id: number;
  readonly plan_name: string;
}

/** service.JournalEventView. Unknown types and actors are kept verbatim. */
export interface JournalEvent {
  readonly id: number;
  readonly created_at: string;
  readonly event_type: string;
  /** success | failed | rejected */
  readonly outcome: string;
  /** user | admin | system */
  readonly actor: string;
  /** Admin login or payment provider; '' when not applicable. */
  readonly actor_name: string;
  /** > 0 linked customer, < 0 anonymous trial, 0 unknown. */
  readonly telegram_id: number;
  readonly username: string;
  /** May reference a subscription deleted since (admin /del, trial cleanup). */
  readonly subscription_id: number | null;
  readonly plan_id: number | null;
  readonly plan_name: string;
  /** free | trial | paid | '' */
  readonly plan_kind: string;
  readonly order_id: number | null;
  /** Minor units; for XTR whole Stars. */
  readonly amount_cents: number | null;
  readonly currency: string | null;
  readonly description: string;
  readonly before: JournalState | null;
  readonly after: JournalState | null;
  /** Remaining details (days, provider, error_code, backfill, …). */
  readonly details: Readonly<Record<string, unknown>>;
}

export interface JournalPage {
  readonly events: readonly JournalEvent[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

/**
 * Query of GET /admin/api/journal. from/to are RFC 3339 instants (from
 * inclusive, to exclusive) or ''. q matches a Telegram or subscription ID
 * exactly, otherwise a username substring.
 */
export interface JournalQuery {
  readonly q: string;
  readonly type: JournalEventType | '';
  readonly planKind: PlanKind | '';
  readonly from: string;
  readonly to: string;
  readonly offset: number;
}

function parseJournalState(value: unknown): JournalState | null | undefined {
  if (value === undefined || value === null) return null;
  if (!isRecord(value)) return undefined;
  const { status, expires_at = null, plan_id = 0, plan_name = '' } = value;
  if (!isText(status) || !isNullable(expires_at, isTime) || !isCount(plan_id) || !isText(plan_name)) return undefined;
  return { status, expires_at, plan_id, plan_name };
}

function parseJournalEvent(value: unknown): JournalEvent | null {
  if (!isRecord(value) || !isRecord(value.details)) return null;
  const {
    id, created_at, event_type, outcome, actor, actor_name, telegram_id, username, subscription_id, plan_id,
    plan_name, plan_kind, order_id, amount_cents, currency, description,
  } = value;
  if (!isPositive(id) || !isTime(created_at) || !isText(event_type) || event_type === '' || !isText(outcome) ||
    !isText(actor) || !isText(actor_name) || !isInteger(telegram_id) || !isText(username) ||
    !isNullable(subscription_id, isPositive) || !isNullable(plan_id, isPositive) || !isText(plan_name) ||
    !isText(plan_kind) || !isNullable(order_id, isPositive) || !isNullable(amount_cents, isInteger) ||
    !isNullable(currency, isCurrency) || !isText(description)) return null;
  const { before: rawBefore, after: rawAfter, ...details } = value.details;
  const before = parseJournalState(rawBefore);
  const after = parseJournalState(rawAfter);
  if (before === undefined || after === undefined) return null;
  return {
    id, created_at, event_type, outcome, actor, actor_name, telegram_id, username, subscription_id, plan_id,
    plan_name, plan_kind, order_id, amount_cents, currency, description, before, after, details,
  };
}

function parseJournalPage(value: unknown): JournalPage | null {
  if (!isRecord(value)) return null;
  const { events: list, total, limit, offset } = value;
  if (!Array.isArray(list) || !isCount(total) || !isPositive(limit) || !isCount(offset) ||
    list.length > limit || list.length > total) return null;
  const events: JournalEvent[] = [];
  for (const item of list) {
    const event = parseJournalEvent(item);
    if (!event) return null;
    events.push(event);
  }
  return { events, total, limit, offset };
}

// =============================================================================
// Network monitor (web.adminAPI.routeMonitoring → netmon views, migration 048)
// Read-only: checks run in the backend worker; reading never triggers one.
// =============================================================================

/** Target/country status; a builder may also be "disabled". */
export const MONITOR_STATUSES = ['up', 'degraded', 'down', 'unknown', 'disabled'] as const;
export type MonitorStatus = (typeof MONITOR_STATUSES)[number];

export const MONITOR_PERIODS = ['24h', '7d', '30d'] as const;
export type MonitorPeriod = (typeof MONITOR_PERIODS)[number];

/** netmon.Transition: the latest country state change of a builder. */
export interface MonitorTransition {
  readonly country_code: string;
  /** down | degraded (outage opened), up (recovered), removed (no longer served). */
  readonly event: string;
  readonly at: string;
}

/** netmon.BuilderSummary. */
export interface MonitorBuilderSummary {
  readonly id: number;
  readonly name: string;
  readonly enabled: boolean;
  readonly status: MonitorStatus;
  readonly countries_total: number;
  readonly countries_up: number;
  readonly countries_degraded: number;
  readonly countries_down: number;
  readonly nodes_total: number;
  readonly nodes_up: number;
  readonly nodes_down: number;
  readonly nodes_unknown: number;
  readonly avg_latency_ms: number | null;
  readonly last_checked_at: string | null;
  readonly last_transition: MonitorTransition | null;
  /** Sources of the builder that failed to fetch at the last resolution. */
  readonly source_errors: number;
  /** Served servers without a checkable endpoint (e.g. Xray config without a primary outbound). */
  readonly unresolved: number;
}

/** netmon.Overview. */
export interface MonitorOverview {
  readonly enabled: boolean;
  readonly generated_at: string;
  readonly interval_seconds: number;
  readonly down_after: number;
  readonly last_round_at: string | null;
  readonly last_refresh_at: string | null;
  readonly builders: readonly MonitorBuilderSummary[];
}

/** netmon.NodeView: credential-free endpoint of one served server. */
export interface MonitorNode {
  readonly target_id: number;
  readonly name: string;
  readonly source_id: number;
  readonly source_name: string;
  readonly protocol: string;
  readonly host: string;
  readonly port: number;
  readonly security: string;
  readonly transport: string;
  /** tcp | tls | quic | unsupported */
  readonly probe: string;
  readonly status: MonitorStatus;
  readonly status_since: string | null;
  readonly latency_ms: number | null;
  readonly last_checked_at: string | null;
  readonly last_up_at: string | null;
  readonly last_down_at: string | null;
  readonly last_error: string;
  /** Other builders serving the same endpoint (checked once for all). */
  readonly shared_with: number;
}

/** netmon.CountryView; country_code '' groups servers without a country. */
export interface MonitorCountry {
  readonly country_code: string;
  readonly status: MonitorStatus;
  readonly status_since: string | null;
  readonly latency_ms: number | null;
  readonly nodes_total: number;
  readonly nodes_up: number;
  readonly nodes_down: number;
  readonly last_checked_at: string | null;
  readonly last_up_at: string | null;
  readonly last_down_at: string | null;
  readonly nodes: readonly MonitorNode[];
}

/** netmon.BuilderDetail. */
export interface MonitorBuilderDetail {
  readonly generated_at: string;
  readonly interval_seconds: number;
  readonly last_round_at: string | null;
  readonly builder: MonitorBuilderSummary;
  readonly countries: readonly MonitorCountry[];
}

/** netmon.OutageView. */
export interface MonitorOutage {
  readonly id: number;
  /** target | country */
  readonly scope: string;
  /** down | degraded */
  readonly kind: string;
  readonly country_code: string;
  readonly target_id: number | null;
  readonly started_at: string;
  readonly ended_at: string | null;
  readonly duration_seconds: number;
  readonly ongoing: boolean;
  /** '' (ongoing) | recovered | changed | removed */
  readonly end_reason: string;
  readonly error_code: string;
}

/** netmon.LatencyPoint. */
export interface MonitorLatencyPoint {
  readonly at: string;
  readonly checks: number;
  readonly failures: number;
  readonly avg_ms: number | null;
  readonly min_ms: number | null;
  readonly max_ms: number | null;
}

/** netmon.HistorySummary. */
export interface MonitorHistorySummary {
  readonly outages: number;
  readonly down_seconds: number;
  readonly degraded_seconds: number;
  /** Percent of the period not DOWN; null for a whole builder. */
  readonly availability: number | null;
  readonly checks: number;
  readonly failures: number;
}

/** netmon.History. */
export interface MonitorHistory {
  /** builder | country | target */
  readonly scope: string;
  readonly period: MonitorPeriod;
  readonly from: string;
  readonly to: string;
  readonly step_seconds: number;
  readonly summary: MonitorHistorySummary;
  readonly outages: readonly MonitorOutage[];
  readonly latency: readonly MonitorLatencyPoint[];
}

/**
 * Subject of GET /admin/api/monitoring/history: a server (targetId), a
 * builder country (builderId + country, '' = without country) or a builder.
 */
export interface MonitorHistoryQuery {
  readonly builderId: number | null;
  readonly country: string | null;
  readonly targetId: number | null;
  readonly period: MonitorPeriod;
}

const isMonitorStatus = (value: unknown): value is MonitorStatus =>
  typeof value === 'string' && (MONITOR_STATUSES as readonly string[]).includes(value);
const isNonNegative = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0;

function parseMonitorTransition(value: unknown): MonitorTransition | null | undefined {
  if (value === null || value === undefined) return null;
  if (!isRecord(value)) return undefined;
  const { country_code, event, at } = value;
  if (!isText(country_code) || !isText(event) || event === '' || !isTime(at)) return undefined;
  return { country_code, event, at };
}

function parseMonitorBuilderSummary(value: unknown): MonitorBuilderSummary | null {
  if (!isRecord(value)) return null;
  const {
    id, name, enabled, status, countries_total, countries_up, countries_degraded, countries_down, nodes_total,
    nodes_up, nodes_down, nodes_unknown, avg_latency_ms, last_checked_at, source_errors, unresolved,
  } = value;
  const last_transition = parseMonitorTransition(value.last_transition);
  if (!isPositive(id) || !isText(name) || typeof enabled !== 'boolean' || !isMonitorStatus(status) ||
    ![countries_total, countries_up, countries_degraded, countries_down, nodes_total, nodes_up, nodes_down,
      nodes_unknown, source_errors, unresolved].every(isCount) ||
    !isNullable(avg_latency_ms, isCount) || !isNullable(last_checked_at, isTime) || last_transition === undefined) return null;
  return {
    id, name, enabled, status, countries_total: countries_total as number, countries_up: countries_up as number,
    countries_degraded: countries_degraded as number, countries_down: countries_down as number,
    nodes_total: nodes_total as number, nodes_up: nodes_up as number, nodes_down: nodes_down as number,
    nodes_unknown: nodes_unknown as number, avg_latency_ms, last_checked_at, last_transition,
    source_errors: source_errors as number, unresolved: unresolved as number,
  };
}

function parseMonitorOverview(value: unknown): MonitorOverview | null {
  if (!isRecord(value)) return null;
  const { enabled, generated_at, interval_seconds, down_after, last_round_at, last_refresh_at, builders: list } = value;
  if (typeof enabled !== 'boolean' || !isTime(generated_at) || !isPositive(interval_seconds) || !isPositive(down_after) ||
    !isNullable(last_round_at, isTime) || !isNullable(last_refresh_at, isTime) || !Array.isArray(list)) return null;
  const builders: MonitorBuilderSummary[] = [];
  for (const item of list) {
    const b = parseMonitorBuilderSummary(item);
    if (!b) return null;
    builders.push(b);
  }
  return { enabled, generated_at, interval_seconds, down_after, last_round_at, last_refresh_at, builders };
}

function parseMonitorNode(value: unknown): MonitorNode | null {
  if (!isRecord(value)) return null;
  const {
    target_id, name, source_id, source_name, protocol, host, port, security, transport, probe, status,
    status_since, latency_ms, last_checked_at, last_up_at, last_down_at, last_error, shared_with,
  } = value;
  if (!isPositive(target_id) || !isText(name) || !isCount(source_id) || !isText(source_name) || !isText(protocol) ||
    !isText(host) || host === '' || !isPositive(port) || port > 65535 || !isText(security) || !isText(transport) ||
    !isText(probe) || !isMonitorStatus(status) || !isNullable(status_since, isTime) || !isNullable(latency_ms, isCount) ||
    !isNullable(last_checked_at, isTime) || !isNullable(last_up_at, isTime) || !isNullable(last_down_at, isTime) ||
    !isText(last_error) || !isCount(shared_with)) return null;
  return {
    target_id, name, source_id, source_name, protocol, host, port, security, transport, probe, status,
    status_since, latency_ms, last_checked_at, last_up_at, last_down_at, last_error, shared_with,
  };
}

function parseMonitorCountry(value: unknown): MonitorCountry | null {
  if (!isRecord(value)) return null;
  const {
    country_code, status, status_since, latency_ms, nodes_total, nodes_up, nodes_down, last_checked_at,
    last_up_at, last_down_at, nodes: list,
  } = value;
  if (!isText(country_code) || !isMonitorStatus(status) || !isNullable(status_since, isTime) ||
    !isNullable(latency_ms, isCount) || !isCount(nodes_total) || !isCount(nodes_up) || !isCount(nodes_down) ||
    !isNullable(last_checked_at, isTime) || !isNullable(last_up_at, isTime) || !isNullable(last_down_at, isTime) ||
    !Array.isArray(list)) return null;
  const nodes: MonitorNode[] = [];
  for (const item of list) {
    const n = parseMonitorNode(item);
    if (!n) return null;
    nodes.push(n);
  }
  return {
    country_code, status, status_since, latency_ms, nodes_total, nodes_up, nodes_down, last_checked_at,
    last_up_at, last_down_at, nodes,
  };
}

function parseMonitorBuilderDetail(value: unknown, id: number): MonitorBuilderDetail | null {
  if (!isRecord(value)) return null;
  const { generated_at, interval_seconds, last_round_at, countries: list } = value;
  const builder = parseMonitorBuilderSummary(value.builder);
  if (!isTime(generated_at) || !isPositive(interval_seconds) || !isNullable(last_round_at, isTime) ||
    !builder || builder.id !== id || !Array.isArray(list)) return null;
  const countries: MonitorCountry[] = [];
  for (const item of list) {
    const c = parseMonitorCountry(item);
    if (!c) return null;
    countries.push(c);
  }
  return { generated_at, interval_seconds, last_round_at, builder, countries };
}

function parseMonitorOutage(value: unknown): MonitorOutage | null {
  if (!isRecord(value)) return null;
  const {
    id, scope, kind, country_code, target_id, started_at, ended_at, duration_seconds, ongoing, end_reason, error_code,
  } = value;
  if (!isPositive(id) || !isText(scope) || !isText(kind) || !isText(country_code) || !isNullable(target_id, isPositive) ||
    !isTime(started_at) || !isNullable(ended_at, isTime) || !isCount(duration_seconds) || typeof ongoing !== 'boolean' ||
    !isText(end_reason) || !isText(error_code)) return null;
  return { id, scope, kind, country_code, target_id, started_at, ended_at, duration_seconds, ongoing, end_reason, error_code };
}

function parseMonitorLatencyPoint(value: unknown): MonitorLatencyPoint | null {
  if (!isRecord(value)) return null;
  const { at, checks, failures, avg_ms, min_ms, max_ms } = value;
  if (!isTime(at) || !isCount(checks) || !isCount(failures) || failures > checks || !isNullable(avg_ms, isCount) ||
    !isNullable(min_ms, isCount) || !isNullable(max_ms, isCount)) return null;
  return { at, checks, failures, avg_ms, min_ms, max_ms };
}

function parseMonitorHistory(value: unknown, period: MonitorPeriod): MonitorHistory | null {
  if (!isRecord(value) || !isRecord(value.summary)) return null;
  const { scope, from, to, step_seconds, outages: outageList, latency: latencyList } = value;
  const { outages: count, down_seconds, degraded_seconds, availability, checks, failures } = value.summary;
  if (!isText(scope) || value.period !== period || !isTime(from) || !isTime(to) || !isPositive(step_seconds) ||
    !isCount(count) || !isCount(down_seconds) || !isCount(degraded_seconds) ||
    !isNullable(availability, isNonNegative) || (availability !== null && availability > 100) ||
    !isCount(checks) || !isCount(failures) || !Array.isArray(outageList) || !Array.isArray(latencyList)) return null;
  const outages: MonitorOutage[] = [];
  for (const item of outageList) {
    const o = parseMonitorOutage(item);
    if (!o) return null;
    outages.push(o);
  }
  const latency: MonitorLatencyPoint[] = [];
  for (const item of latencyList) {
    const p = parseMonitorLatencyPoint(item);
    if (!p) return null;
    latency.push(p);
  }
  return {
    scope, period, from, to, step_seconds,
    summary: { outages: count, down_seconds, degraded_seconds, availability, checks, failures },
    outages, latency,
  };
}

/** Builds the query string of GET /admin/api/monitoring/history. */
export function monitorHistoryPath(query: MonitorHistoryQuery): string {
  const params = new URLSearchParams();
  if (query.targetId !== null) params.set('target_id', String(query.targetId));
  if (query.builderId !== null) params.set('builder_id', String(query.builderId));
  if (query.country !== null) params.set('country', query.country);
  params.set('period', query.period);
  return `/admin/api/monitoring/history?${params.toString()}`;
}
