import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Api, ApiError } from '../src/api';
import type { Order } from '../src/api';
import { Store } from '../src/store';
import type { InvoiceResult } from '../src/telegram';
import { offer, order, subscription, telegram } from './fixtures';

const stores: Store[] = [];
function setup() {
  const api = new Api('signed');
  const tg = telegram();
  vi.spyOn(api, 'subscription').mockResolvedValue(subscription);
  vi.spyOn(api, 'offers').mockResolvedValue({ offers: [offer] });
  vi.spyOn(api, 'recent').mockResolvedValue({ orders: [] });
  vi.spyOn(api, 'create').mockResolvedValue(order);
  vi.spyOn(api, 'order').mockResolvedValue(order);
  vi.spyOn(api, 'invoice').mockResolvedValue({ invoice_url: 'https://t.me/$test-invoice' });
  const store = new Store(api, tg, sessionStorage); stores.push(store);
  return { api, tg, store };
}
beforeEach(() => { sessionStorage.clear(); vi.useFakeTimers(); });
afterEach(() => { stores.splice(0).forEach(store => store.dispose()); vi.useRealTimers(); });

describe('purchase orchestration', () => {
  it('loads independent resources and handles users without a subscription', async () => {
    const { api, store } = setup();
    vi.mocked(api.subscription).mockResolvedValue(null);
    vi.mocked(api.offers).mockResolvedValue({ offers: [] });
    await store.refresh();
    expect(store.subscription.data).toBeNull(); expect(store.offers.data).toEqual([]);
    expect(store.subscription.error).toBeUndefined(); expect(store.recent.loading).toBe(false);
  });
  it('locks rapid Buy clicks synchronously and only sends offer/key', async () => {
    const { api, store } = setup();
    const results = await Promise.all([store.buy(offer), store.buy(offer), store.buy(offer)]);
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(results.filter(Boolean)).toHaveLength(1);
    expect(api.create).toHaveBeenCalledWith(offer.offer_id, expect.stringMatching(/^[a-f0-9-]{36}$/));
    expect(sessionStorage.getItem('miniapp-intent')).toBeNull();
  });
  it('reuses the same idempotency key after lost response and a WebView reload', async () => {
    const { api, store, tg } = setup();
    vi.mocked(api.create).mockRejectedValueOnce(new ApiError('network'));
    await store.buy(offer);
    const key = vi.mocked(api.create).mock.calls[0][1];
    const recovered = new Store(api, tg, sessionStorage); stores.push(recovered);
    await recovered.buy(offer);
    expect(api.create).toHaveBeenLastCalledWith(offer.offer_id, key);
    expect(recovered.order?.order_id).toBe(order.order_id);
  });
  it('does not drop an ambiguous key when a different offer is selected', async () => {
    const { api, store } = setup();
    vi.mocked(api.create).mockRejectedValueOnce(new ApiError('timeout'));
    await store.buy(offer);
    const saved = sessionStorage.getItem('miniapp-intent');
    await store.buy({ ...offer, offer_id: 'c'.repeat(32) });
    expect(api.create).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem('miniapp-intent')).toBe(saved);
    await store.buy(offer);
    expect(api.create).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.create).mock.calls[0][1]).toBe(vi.mocked(api.create).mock.calls[1][1]);
  });
  it('recovers a server pending purchase without creating another', async () => {
    const { api, store } = setup();
    vi.mocked(api.recent).mockResolvedValue({ orders: [order] });
    await store.refresh(); await store.buy(offer);
    expect(api.create).not.toHaveBeenCalled(); expect(store.order).toEqual(order);
  });
  it('retrieves recovery data on purchase_pending conflict', async () => {
    const { api, store } = setup();
    vi.mocked(api.create).mockRejectedValue(new ApiError('purchase_pending', 409));
    vi.mocked(api.recent).mockResolvedValue({ orders: [order] });
    await store.buy(offer);
    expect(store.recent.data).toEqual([order]);
    expect(sessionStorage.getItem('miniapp-intent')).toBeNull();
  });
  it.each<InvoiceResult>(['paid', 'pending', 'cancelled', 'failed'])('SDK %s is only a hint; server status remains authoritative', async result => {
    const { api, store, tg } = setup();
    await store.selectOrder(order.order_id);
    await Promise.all([store.pay(), store.pay()]);
    expect(api.invoice).toHaveBeenCalledTimes(1); expect(tg.openInvoice).toHaveBeenCalledTimes(1);
    const callback = vi.mocked(tg.openInvoice).mock.calls[0][1];
    callback(result); await vi.advanceTimersByTimeAsync(0);
    expect(store.paymentHint).toBe(result); expect(store.order?.status).toBe('pending');
    expect(api.subscription).not.toHaveBeenCalled();
    vi.mocked(api.order).mockResolvedValue({ ...order, status: 'paid', checkout_started: true });
    await store.checkOrder();
    expect(store.order?.status).toBe('paid'); expect(store.subscription.data).toEqual(subscription);
    expect(api.subscription).toHaveBeenCalledTimes(1);
  });
  it('never starts a second checkout after server reservation, including expired delayed settlement', async () => {
    const { api, store, tg } = setup();
    vi.mocked(api.order).mockResolvedValue({ ...order, checkout_started: true });
    await store.selectOrder(order.order_id); await store.pay();
    expect(tg.openInvoice).not.toHaveBeenCalled();
    vi.mocked(api.order).mockResolvedValue({ ...order, status: 'expired', checkout_started: true });
    await store.checkOrder(); await store.pay();
    expect(api.invoice).not.toHaveBeenCalled();
    vi.mocked(api.order).mockResolvedValue({ ...order, status: 'paid', checkout_started: true });
    await store.checkOrder(); expect(store.order?.status).toBe('paid');
  });
  it('blocks payment retry after callback refresh fails until server state is read', async () => {
    const { api, store, tg } = setup();
    await store.selectOrder(order.order_id); await store.pay();
    vi.mocked(api.order).mockRejectedValue(new ApiError('network'));
    vi.mocked(tg.openInvoice).mock.calls[0][1]('failed');
    await vi.advanceTimersByTimeAsync(0); await store.pay();
    expect(api.invoice).toHaveBeenCalledTimes(1);
    vi.mocked(api.order).mockResolvedValue({ ...order, checkout_started: true });
    await store.checkOrder(); await store.pay();
    expect(api.invoice).toHaveBeenCalledTimes(1);
  });
  it('401 clears sensitive state and stops polling even if another request succeeds later', async () => {
    const { api, store } = setup();
    await store.refresh(); await store.selectOrder(order.order_id);
    vi.mocked(api.order).mockRejectedValue(new ApiError('unauthorized', 401));
    await store.checkOrder(); await vi.advanceTimersByTimeAsync(120000);
    expect(store.unauthorized).toBe(true); expect(store.order).toBeNull();
    expect(store.subscription.data).toBeNull(); expect(store.recent.data).toEqual([]);
    expect(api.order).toHaveBeenCalledTimes(2);
  });
  it('does not let a slow previous order replace a newly selected purchase', async () => {
    const { api, store } = setup();
    let resolve!: (value: Order) => void;
    vi.mocked(api.order).mockReturnValueOnce(new Promise(done => { resolve = done; }));
    const old = store.selectOrder(order.order_id);
    const other = { ...order, order_id: 'c'.repeat(32) };
    vi.mocked(api.order).mockResolvedValue(other);
    await store.selectOrder(other.order_id); resolve(order); await old;
    expect(store.order?.order_id).toBe(other.order_id); expect(store.checking).toBe(false);
  });
  it('polling is bounded and manual refresh can observe later settlement', async () => {
    const { api, store } = setup();
    await store.selectOrder(order.order_id);
    await vi.advanceTimersByTimeAsync(600000);
    expect(vi.mocked(api.order).mock.calls.length).toBeLessThanOrEqual(25);
    const count = vi.mocked(api.order).mock.calls.length;
    await vi.advanceTimersByTimeAsync(600000);
    expect(api.order).toHaveBeenCalledTimes(count);
    vi.mocked(api.order).mockResolvedValue({ ...order, status: 'paid' });
    await store.checkOrder(); expect(store.order?.status).toBe('paid');
  });
  it.each(['expired', 'canceled'] as const)('does not pay a server %s order', async status => {
    const { api, store } = setup();
    vi.mocked(api.order).mockResolvedValue({ ...order, status });
    await store.selectOrder(order.order_id); await store.pay();
    expect(api.invoice).not.toHaveBeenCalled();
  });
  it('rejects unsupported currency and non-Telegram invoice URLs', async () => {
    const { api, store, tg } = setup();
    await store.buy({ ...offer, currency: 'RUB' }); expect(api.create).not.toHaveBeenCalled();
    await store.selectOrder(order.order_id);
    vi.mocked(api.invoice).mockResolvedValue({ invoice_url: 'https://attacker.example/invoice' });
    await store.pay(); expect(tg.openInvoice).not.toHaveBeenCalled(); expect(store.error).toBeDefined();
  });
});
