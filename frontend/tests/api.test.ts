import { afterEach, describe, expect, it, vi } from 'vitest';
import { Api } from '../src/api';
import { json, offer, order } from './fixtures';

afterEach(() => vi.useRealTimers());
describe('existing API boundary', () => {
  it('does not bind the transport to the Api instance (native browser fetch)', async () => {
    const transport = vi.fn(function (this: unknown) {
      expect(this).toBeUndefined();
      return Promise.resolve(json(order));
    });
    await expect(new Api('signed', transport).order(order.order_id)).resolves.toEqual(order);
  });
  it('sends initData only in Authorization and does not send client-owned payment terms', async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(json(order));
    const api = new Api('raw+signed&data', transport);
    await api.create(offer.offer_id, 'test-key');
    const [url, init] = transport.mock.calls[0];
    expect(url).toBe('/api/miniapp/orders');
    expect(init?.headers).toEqual({ Authorization: 'tma raw+signed&data', 'Content-Type': 'application/json', 'Idempotency-Key': 'test-key' });
    expect(init?.body).toBe(JSON.stringify({ offer_id: offer.offer_id }));
    expect(init?.credentials).toBe('omit'); expect(init?.cache).toBe('no-store'); expect(init?.redirect).toBe('error');
  });
  it('invoice POST has no body or query', async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(json({ invoice_url: 'https://t.me/$test' }));
    await new Api('signed', transport).invoice(order.order_id);
    expect(transport.mock.calls[0][0]).toBe(`/api/miniapp/orders/${order.order_id}/invoice`);
    expect(transport.mock.calls[0][1]?.body).toBeUndefined();
    expect(transport.mock.calls[0][1]?.method).toBe('POST');
  });
  it('fails closed without initData; empty subscription is not an infrastructure failure', async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(json({ error: 'subscription_not_found' }, 404));
    await expect(new Api('', transport).offers()).rejects.toMatchObject({ status: 401 });
    expect(transport).not.toHaveBeenCalled();
    await expect(new Api('signed', transport).subscription()).resolves.toBeNull();
    transport.mockResolvedValue(json({ error: 'service_unavailable' }, 503));
    await expect(new Api('signed', transport).subscription()).rejects.toMatchObject({ status: 503 });
  });
  it.each([401, 403, 409, 500, 503])('surfaces HTTP %i without exposing raw backend text', async status => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(new Response('private server diagnostic', { status }));
    await expect(new Api('signed', transport).offers()).rejects.toMatchObject({ status, code: status === 401 ? 'unauthorized' : 'service_unavailable' });
  });
  it('network failure does not automatically replay a POST', async () => {
    const transport = vi.fn<typeof fetch>().mockRejectedValue(new TypeError('private network diagnostic'));
    await expect(new Api('signed', transport).create(offer.offer_id, 'key')).rejects.toMatchObject({ code: 'network' });
    expect(transport).toHaveBeenCalledTimes(1);
  });
  it('bounds slow requests and handles invalid JSON', async () => {
    vi.useFakeTimers();
    const transport = vi.fn<typeof fetch>().mockImplementation((_url, init) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
    }));
    const pending = expect(new Api('signed', transport).offers()).rejects.toMatchObject({ code: 'timeout' });
    await vi.advanceTimersByTimeAsync(12000); await pending;
    transport.mockResolvedValue(new Response('not json'));
    await expect(new Api('signed', transport).offers()).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
