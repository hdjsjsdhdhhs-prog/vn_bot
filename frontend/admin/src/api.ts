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
    reminders_sent, devices, ips,
  } = value;
  if (!isPositive(id) || !isInteger(telegram_id) || !isText(username) || !isText(status) || status === '' ||
    !isNullable(expires_at, isTime) || !isCount(plan_id) || !isText(plan_name) ||
    !isNullable(provider_source_id, isPositive) || !isNullable(product_id, isPositive) ||
    typeof is_paid !== 'boolean' || !isCount(price_paid_cents) || !isNullable(currency, isCurrency) ||
    !isNullable(referred_by, isInteger) || !isNullable(started_at, isTime) || !isNullable(last_request, isTime) ||
    !isTime(created_at) || !isTime(updated_at) || !isCount(reminders_sent) || !isCount(devices) || !isCount(ips)) return null;
  return {
    id, telegram_id, username, status, expires_at, plan_id, plan_name, provider_source_id, product_id,
    is_paid, price_paid_cents, currency, referred_by, started_at, last_request, created_at, updated_at,
    reminders_sent, devices, ips,
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
  const { id, name, is_active, devices_limit, traffic_limit } = value;
  if (!isPositive(id) || !isText(name) || typeof is_active !== 'boolean' || !isCount(devices_limit) ||
    !isCount(traffic_limit)) return undefined;
  return { id, name, is_active, devices_limit, traffic_limit };
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

  #adopt(payload: unknown): Session {
    if (!isSessionPayload(payload)) throw new ApiError('invalid_response');
    this.#csrf = payload.csrf_token;
    return { authenticated: payload.authenticated };
  }

  async #send(method: 'GET' | 'POST', path: string, body?: string): Promise<unknown> {
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
        throw new ApiError(errorCode(data, response.status), response.status, retryAfter);
      }
      return data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(controller.signal.aborted ? 'timeout' : 'network');
    } finally {
      clearTimeout(timer);
    }
  }
}
