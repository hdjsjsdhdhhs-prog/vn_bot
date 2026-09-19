import { vi } from 'vitest';
import type { Offer, Order, Subscription } from '../src/api';
import type { WebApp } from '../src/telegram';

export const offer: Offer = { offer_id: 'a'.repeat(32), name: 'Месяц свободы', duration_days: 30, amount_cents: 150, currency: 'XTR' };
export const order: Order = { ...offer, order_id: 'b'.repeat(32), status: 'pending', created_at: '2026-09-19T12:00:00Z', expires_at: '2026-09-19T12:30:00Z', checkout_started: false };
export const subscription: Subscription = { status: 'active', expires_at: '2026-12-19T12:00:00Z', subscription_url: 'https://customer.example/sub/test-private', connection_url: 'https://customer.example/connect/test-private' };
export function telegram(): WebApp {
  return {
    initData: 'test-signed-initdata', initDataUnsafe: { user: { first_name: 'Аня', username: 'anya' } },
    colorScheme: 'dark', themeParams: {}, isVersionAtLeast: () => true,
    ready: vi.fn(), expand: vi.fn(), close: vi.fn(), openInvoice: vi.fn(), openLink: vi.fn(), onEvent: vi.fn(), offEvent: vi.fn(),
    BackButton: { show: vi.fn(), hide: vi.fn(), onClick: vi.fn(), offClick: vi.fn() },
  };
}
export function json(data: unknown, status = 200) { return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } }); }
