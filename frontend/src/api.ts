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

// Only this class knows the raw initData. Never persist or log it.
export class Api {
  constructor(private initData: string, private transport: typeof fetch = fetch) {}
  private async request<T>(path: string, method = 'GET', body?: object, key?: string): Promise<T> {
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
      if (data === null || typeof data !== 'object') throw new ApiError('invalid_response');
      return data as T;
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(controller.signal.aborted ? 'timeout' : 'network');
    } finally { clearTimeout(timeout); }
  }
  async subscription(): Promise<Subscription | null> {
    try { return await this.request<Subscription>('subscription'); }
    catch (error) {
      if (error instanceof ApiError && error.status === 404 && error.code === 'subscription_not_found') return null;
      throw error;
    }
  }
  offers() { return this.request<{ offers: Offer[] }>('offers'); }
  recent() { return this.request<{ orders: Order[] }>('orders/recent'); }
  create(offer: string, key: string) { return this.request<Order>('orders', 'POST', { offer_id: offer }, key); }
  order(id: string) { return this.request<Order>(`orders/${encodeURIComponent(id)}`); }
  invoice(id: string) { return this.request<{ invoice_url: string }>(`orders/${encodeURIComponent(id)}/invoice`, 'POST'); }
}
