import { describe, expect, it, vi } from 'vitest';
import { AdminApi } from '../admin/src/api';

// Regression: the user card (GET /admin/api/users/{id}, /admin/api/subscriptions/{id})
// and the tariff plan picker (GET /admin/api/plans) once required the legacy
// node subsystem fields (`nodes`, plan `devices_limit`/`traffic_limit`) and
// rejected every real response after their removal. The fixtures below are
// the shapes the backend encodes now (service.AdminSubscriptionPage,
// service.AdminPlanView, service.TariffPlanView).

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

const setup = () => {
  const transport = vi.fn<typeof fetch>();
  return { transport, api: new AdminApi(transport) };
};

/** service.AdminSubscriptionView. */
const subscription = (over: Record<string, unknown> = {}) => ({
  id: 41, telegram_id: 777001, username: 'alice', status: 'active', expires_at: '2026-11-01T00:00:00Z',
  plan_id: 3, plan_name: 'Стандарт', provider_source_id: null, product_id: null, is_paid: false, price_paid_cents: 0,
  currency: null, referred_by: null, started_at: null, last_request: null, created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-02T00:00:00Z', reminders_sent: 0, devices: 2, ips: 1, subscription_builder_id: null, ...over,
});

/** service.AdminSubscriptionPage: subscription, plan and audit — nothing else. */
const page = (over: Record<string, unknown> = {}) => ({
  subscription: subscription(),
  plan: { id: 3, name: 'Стандарт', is_active: true, subscription_builder_id: 2 },
  audit: [],
  ...over,
});

/** service.TariffPlanView. */
const tariffPlan = (over: Record<string, unknown> = {}) => ({
  id: 3, name: 'Стандарт', is_active: true, subscription_builder_id: 2, builder_name: 'Основной', tariffs: 2,
  subscriptions: 21, selectable: true, ...over,
});

describe('user card contract (no legacy nodes, no plan limits)', () => {
  it('parses the page the backend sends', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(page()));
    const detail = await api.user(777001);
    expect(detail).toEqual({
      subscription: expect.objectContaining({ id: 41, telegram_id: 777001, devices: 2 }),
      plan: { id: 3, name: 'Стандарт', is_active: true, subscription_builder_id: 2 },
    });
    expect(Object.keys(detail)).not.toContain('nodes');
    expect(Object.keys(detail.plan!)).not.toContain('devices_limit');
    expect(Object.keys(detail.plan!)).not.toContain('traffic_limit');
  });

  it('accepts a missing plan and a plan without a builder', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(page({ plan: null })));
    await expect(api.user(777001)).resolves.toMatchObject({ plan: null });
    transport.mockResolvedValueOnce(json(page({ plan: { id: 3, name: 'Стандарт', is_active: false, subscription_builder_id: null } })));
    await expect(api.user(777001)).resolves.toMatchObject({ plan: { subscription_builder_id: null } });
  });

  it('loads a subscription page by id the same way', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(page()));
    await expect(api.subscription(41, 777001)).resolves.toMatchObject({ subscription: { id: 41 }, plan: { id: 3 } });
    expect(transport.mock.calls[0][0]).toBe('/admin/api/subscriptions/41');
  });

  it('still rejects malformed pages', async () => {
    const { transport, api } = setup();
    for (const bad of [page({ audit: undefined }), page({ plan: { id: 3, name: 'Стандарт' } }), page({ subscription: subscription({ telegram_id: 1 }) })]) {
      transport.mockResolvedValueOnce(json(bad));
      await expect(api.user(777001)).rejects.toMatchObject({ code: 'invalid_response' });
    }
  });
});

describe('tariff plan picker contract (no plan limits)', () => {
  it('parses the plans the backend sends', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ plans: [tariffPlan(), tariffPlan({ id: 1, name: 'trial', subscription_builder_id: null, builder_name: '', selectable: false })] }));
    const plans = await api.listPlans();
    expect(plans).toHaveLength(2);
    expect(plans[0]).toEqual(tariffPlan());
    for (const plan of plans) {
      expect(Object.keys(plan)).not.toContain('devices_limit');
      expect(Object.keys(plan)).not.toContain('traffic_limit');
    }
  });

  it('still rejects a malformed plan', async () => {
    const { transport, api } = setup();
    for (const bad of [{ selectable: 'yes' }, { tariffs: -1 }, { subscription_builder_id: 0 }]) {
      transport.mockResolvedValueOnce(json({ plans: [tariffPlan(bad)] }));
      await expect(api.listPlans()).rejects.toMatchObject({ code: 'invalid_response' });
    }
  });
});
