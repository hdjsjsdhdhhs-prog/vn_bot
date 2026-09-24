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

/** Server-side credential bounds (adminauth.login), measured in UTF-8 bytes. */
export const limits = { usernameBytes: 64, passwordBytes: 72 } as const;

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
