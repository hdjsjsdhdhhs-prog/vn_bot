export interface Offer {
  offer_id: string;
  name: string;
  duration_days: number;
  amount_cents: number;
  currency: string;
  available_until?: string;
}
export interface Order extends Offer {
  order_id: string;
  status: 'pending' | 'paid' | 'expired' | 'canceled';
  created_at: string;
  expires_at: string | null;
  checkout_started: boolean;
}
export interface Subscription {
  status: 'active' | 'expired' | 'revoked' | 'paused' | 'canceled';
  expires_at: string | null;
  subscription_url: string;
  connection_url: string;
}

export class ApiError extends Error {
  constructor(public code: string, public status = 0) { super(code); }
}
export function errorText(error: unknown): string {
  const code = error instanceof ApiError ? error.code : '';
  return ({
    unauthorized: 'Сессия завершилась. Закройте Mini App и откройте его снова из Telegram.',
    forbidden: 'Доступ недоступен для этого аккаунта. Обратитесь в поддержку через бота.',
    offer_unavailable: 'Предложение больше недоступно. Обновите каталог.',
    purchase_pending: 'Для этого предложения уже есть покупка. Откройте последние покупки в профиле.',
    unresolved_intent: 'Предыдущая попытка ещё не проверена. Повторите покупку исходного предложения или откройте последние покупки в профиле.',
    purchase_not_payable: 'Этот счёт больше нельзя оплатить. Обновите статус покупки.',
    order_not_found: 'Покупка недоступна. Обновите последние покупки.',
    idempotency_conflict: 'Не удалось повторить покупку. Откройте последние покупки в профиле.',
    service_unavailable: 'Сервис временно недоступен. Попробуйте ещё раз чуть позже.',
    network: 'Не удалось связаться с сервером. Проверьте интернет и повторите действие.',
    timeout: 'Сервер отвечает дольше обычного. Повтор безопасен: покупка не будет создана дважды.',
    invalid_response: 'Не удалось прочитать ответ сервера. Попробуйте позже.',
  } as Record<string, string>)[code] ?? 'Не удалось выполнить действие. Попробуйте ещё раз.';
}

// Validate the wire shape before publishing data to the store. TypeScript casts
// cannot protect rendering or payment retry state from a malformed HTTP 200.
function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
const reference = (value: unknown): value is string => typeof value === 'string' && /^[a-f0-9]{32}$/.test(value);
const timestamp = (value: unknown): value is string => typeof value === 'string' && Number.isFinite(Date.parse(value));
const positiveInteger = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
function isOffer(value: unknown): value is Offer {
  return record(value) && reference(value.offer_id) && typeof value.name === 'string' &&
    positiveInteger(value.duration_days) && positiveInteger(value.amount_cents) &&
    typeof value.currency === 'string' && /^[A-Z]{3}$/.test(value.currency) &&
    (value.available_until === undefined || timestamp(value.available_until));
}
function isOrder(value: unknown): value is Order {
  return record(value) && isOffer(value) && reference(value.order_id) &&
    typeof value.status === 'string' && ['pending', 'paid', 'expired', 'canceled'].includes(value.status) &&
    timestamp(value.created_at) && (value.expires_at === null || timestamp(value.expires_at)) &&
    typeof value.checkout_started === 'boolean';
}
function isSubscription(value: unknown): value is Subscription {
  return record(value) && typeof value.status === 'string' &&
    ['active', 'expired', 'revoked', 'paused', 'canceled'].includes(value.status) &&
    (value.expires_at === null || timestamp(value.expires_at)) &&
    typeof value.subscription_url === 'string' && typeof value.connection_url === 'string';
}
function isOffers(value: unknown): value is { offers: Offer[] } {
  return record(value) && Array.isArray(value.offers) && value.offers.every(isOffer);
}
function isRecent(value: unknown): value is { orders: Order[] } {
  return record(value) && Array.isArray(value.orders) && value.orders.every(isOrder);
}
function isInvoice(value: unknown): value is { invoice_url: string } {
  return record(value) && typeof value.invoice_url === 'string';
}

// Only this class knows the raw initData. Never persist or log it.
export class Api {
  constructor(private initData: string, private transport: typeof fetch = fetch) {}
  private async request<T>(path: string, valid: (value: unknown) => value is T, method = 'GET', body?: object, key?: string): Promise<T> {
    if (!this.initData) throw new ApiError('unauthorized', 401);
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const headers: Record<string, string> = { Authorization: `tma ${this.initData}` };
      if (body) headers['Content-Type'] = 'application/json';
      if (key) headers['Idempotency-Key'] = key;
      // Native Window.fetch must not receive the Api instance as its receiver.
      const transport = this.transport;
      const response = await transport(`/api/miniapp/${path}`, {
        method, headers, body: body ? JSON.stringify(body) : undefined,
        signal: controller.signal, cache: 'no-store', credentials: 'omit', redirect: 'error',
      });
      const data: unknown = await response.json().catch(() => null);
      if (!response.ok) {
        const code = typeof data === 'object' && data !== null && 'error' in data && typeof data.error === 'string'
          ? data.error : 'service_unavailable';
        throw new ApiError(response.status === 401 ? 'unauthorized' : code, response.status);
      }
      if (!valid(data)) throw new ApiError('invalid_response');
      return data;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(controller.signal.aborted ? 'timeout' : 'network');
    } finally { clearTimeout(timeout); }
  }
  async subscription(): Promise<Subscription | null> {
    try { return await this.request('subscription', isSubscription); }
    catch (error) {
      if (error instanceof ApiError && error.status === 404 && error.code === 'subscription_not_found') return null;
      throw error;
    }
  }
  offers() { return this.request('offers', isOffers); }
  recent() { return this.request('orders/recent', isRecent); }
  create(offer: string, key: string) { return this.request('orders', isOrder, 'POST', { offer_id: offer }, key); }
  order(id: string) { return this.request(`orders/${encodeURIComponent(id)}`, isOrder); }
  invoice(id: string) { return this.request(`orders/${encodeURIComponent(id)}/invoice`, isInvoice, 'POST'); }
}
