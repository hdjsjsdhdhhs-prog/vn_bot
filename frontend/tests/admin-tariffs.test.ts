import { describe, expect, it, vi } from 'vitest';
import { AdminApi, ApiError, type AdminTariff } from '../admin/src/api';
import {
  botButtonLabel, catalogueOrder, durationLabel, featuresFromText, formFromTariff, formatTariffPrice, moveItem,
  parsePriceInput, pluralRu, priceInputValue, reorderIds, sameForm, termsChanged, validateTariffForm,
} from '../admin/src/tariffs';

// Pure logic of the admin tariff editor (admin/src/tariffs.ts) and the tariff
// part of the admin API client (admin/src/api.ts), against the server bounds
// of database.TariffInput.Validate and the service.TariffView wire shape.

const tariff = (over: Partial<AdminTariff> = {}): AdminTariff => ({
  id: 7, offer_id: 'a'.repeat(32), name: 'Месяц', plan_id: 3, plan_name: 'Стандарт', plan_active: true, builder_id: null,
  duration_days: 30, price_cents: 19900, currency: 'RUB', is_active: true, description: '', features: [], badge: '',
  sort_order: 0, version: 1, previous_id: null, replaced_by_id: null, orders: 0, subscriptions: 0, in_use: false,
  offer_ends_at: null, created_at: '2026-09-01T10:00:00Z', updated_at: '2026-09-02T10:00:00.123456789Z', ...over,
});

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

describe('tariff formatting', () => {
  it('formats prices in minor units and whole Stars', () => {
    expect(formatTariffPrice(19900, 'RUB')).toBe('199 ₽');
    expect(formatTariffPrice(19990, 'RUB')).toBe('199,90 ₽');
    expect(formatTariffPrice(150, 'XTR')).toBe('150 ★');
    expect(formatTariffPrice(1250, 'USD')).toBe('12,50 USD');
  });

  it('matches the bot button label (utils.FormatPriceCents)', () => {
    expect(botButtonLabel('Месяц', 19900)).toBe('Месяц — 199₽');
    expect(botButtonLabel('Месяц', 19905)).toBe('Месяц — 199.05₽');
  });

  it('pluralizes Russian counts', () => {
    expect([1, 2, 5, 11, 21, 22, 25, 112].map(n => pluralRu(n, 'день', 'дня', 'дней')))
      .toEqual(['день', 'дня', 'дней', 'дней', 'день', 'дня', 'дней', 'дней']);
    expect(durationLabel(365)).toBe('365 дней');
    expect(durationLabel(1)).toBe('1 день');
  });
});

describe('price input', () => {
  it('parses rubles with comma or dot and whole Stars', () => {
    expect(parsePriceInput('199', 'RUB')).toBe(19900);
    expect(parsePriceInput(' 199,9 ', 'RUB')).toBe(19990);
    expect(parsePriceInput('199.05', 'RUB')).toBe(19905);
    expect(parsePriceInput('1 000', 'RUB')).toBe(100000);
    expect(parsePriceInput('150', 'XTR')).toBe(150);
    for (const bad of ['', 'abc', '1,999', '-5', '1e3']) expect(parsePriceInput(bad, 'RUB')).toBeNull();
    expect(parsePriceInput('1.5', 'XTR')).toBeNull();
  });

  it('round-trips through the input value', () => {
    for (const amount of [100, 19900, 19905, 19990]) {
      expect(parsePriceInput(priceInputValue(amount, 'RUB'), 'RUB')).toBe(amount);
    }
    expect(priceInputValue(19990, 'RUB')).toBe('199,90');
    expect(priceInputValue(150, 'XTR')).toBe('150');
  });
});

describe('tariff form', () => {
  const valid = () => ({ ...formFromTariff(null, 3), name: '  Год  ', price: '1990', durationDays: '365' });

  it('builds a normalized input from a valid form', () => {
    const { input, errors } = validateTariffForm({ ...valid(), description: ' Лучшее\r\nпредложение ', features: 'Все серверы\n\n  Поддержка  \n', badge: ' Хит ' });
    expect(errors).toEqual({});
    expect(input).toEqual({
      name: 'Год', plan_id: 3, duration_days: 365, price_cents: 199000, currency: 'RUB',
      description: 'Лучшее\nпредложение', features: ['Все серверы', 'Поддержка'], badge: 'Хит', is_active: true, sort_order: null,
    });
    expect(validateTariffForm({ ...valid(), sortOrder: '4' }).input?.sort_order).toBe(4);
  });

  it('reports every field outside the server bounds', () => {
    const { input, errors } = validateTariffForm({
      name: 'я'.repeat(65), planId: null, durationDays: '3651', price: '0', currency: 'RUB',
      description: 'x'.repeat(501), features: Array.from({ length: 9 }, (_, i) => `f${i}`).join('\n'), badge: 'b'.repeat(25),
      isActive: true, sortOrder: '100001',
    });
    expect(input).toBeNull();
    expect(Object.keys(errors).sort()).toEqual(['badge', 'description', 'durationDays', 'features', 'name', 'planId', 'price', 'sortOrder']);
    expect(validateTariffForm({ ...valid(), name: '' }).errors.name).toBe('Введите название.');
    expect(validateTariffForm({ ...valid(), features: 'x'.repeat(81) }).errors.features).toContain('80');
    expect(validateTariffForm({ ...valid(), currency: 'XTR', price: '1,5' }).errors.price).toBe('Цена в звёздах — целое число.');
    expect(validateTariffForm({ ...valid(), durationDays: '0' }).errors.durationDays).toBeDefined();
    expect(validateTariffForm({ ...valid(), sortOrder: '-1' }).errors.sortOrder).toBeDefined();
  });

  it('counts length in characters, not UTF-16 units', () => {
    expect(validateTariffForm({ ...valid(), name: '😀'.repeat(64) }).errors.name).toBeUndefined();
    expect(validateTariffForm({ ...valid(), name: '😀'.repeat(65) }).errors.name).toBeDefined();
  });

  it('round-trips a tariff without spurious changes', () => {
    const source = tariff({ features: ['a', 'b'], description: 'Текст', badge: 'Хит', price_cents: 19990, sort_order: 5 });
    const form = formFromTariff(source, null);
    expect(form).toMatchObject({ price: '199,90', features: 'a\nb', sortOrder: '5', planId: 3 });
    expect(sameForm(form, formFromTariff(source, null))).toBe(true);
    const { input } = validateTariffForm(form);
    expect(input && termsChanged(source, input)).toBe(false);
  });

  it('detects purchase-term changes only', () => {
    const source = tariff();
    const base = validateTariffForm(formFromTariff(source, null)).input;
    expect(base).not.toBeNull();
    if (!base) return;
    expect(termsChanged(source, { ...base, description: 'новое', badge: 'Хит', features: ['x'], is_active: false })).toBe(false);
    for (const change of [{ name: 'Другой' }, { plan_id: 4 }, { duration_days: 31 }, { price_cents: 1 }, { currency: 'XTR' }]) {
      expect(termsChanged(source, { ...base, ...change })).toBe(true);
    }
  });

  it('drops blank feature lines', () => {
    expect(featuresFromText(' a \r\n\r\n b\n  ')).toEqual(['a', 'b']);
  });
});

describe('catalogue order', () => {
  const a = tariff({ id: 1, sort_order: 2 });
  const b = tariff({ id: 2, sort_order: 0, price_cents: 500 });
  const c = tariff({ id: 3, sort_order: 0, price_cents: 100 });
  const old = tariff({ id: 4, sort_order: 1, replaced_by_id: 5, is_active: false });

  it('sorts like the bot and the Mini App: sort_order, price, id', () => {
    expect(catalogueOrder([a, b, c, old]).map(item => item.id)).toEqual([3, 2, 4, 1]);
  });

  it('appends retired versions to the reorder list exactly once', () => {
    expect(reorderIds([1, 3, 2], [a, b, c, old])).toEqual([1, 3, 2, 4]);
    expect(reorderIds([1, 4, 3, 2], [a, b, c, old])).toEqual([1, 4, 3, 2]);
  });

  it('moves items within bounds only', () => {
    expect(moveItem([1, 2, 3], 2, 0)).toEqual([3, 1, 2]);
    expect(moveItem([1, 2, 3], 0, -1)).toEqual([1, 2, 3]);
    expect(moveItem([1, 2, 3], 2, 3)).toEqual([1, 2, 3]);
  });
});

describe('tariff API client', () => {
  const setup = () => {
    const transport = vi.fn<typeof fetch>();
    return { transport, api: new AdminApi(transport) };
  };

  it('parses the list and rejects a malformed tariff', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ tariffs: [tariff(), tariff({ id: 8, builder_id: 2, previous_id: 7 })] }));
    await expect(api.listTariffs()).resolves.toHaveLength(2);
    for (const bad of [{ features: null }, { price_cents: -1 }, { currency: 'rub' }, { version: 0 }, { in_use: 'no' }]) {
      transport.mockResolvedValueOnce(json({ tariffs: [tariff(bad as Partial<AdminTariff>)] }));
      await expect(api.listTariffs()).rejects.toMatchObject({ code: 'invalid_response' });
    }
  });

  it('sends exactly the fields web.tariffBody accepts', async () => {
    const { transport, api } = setup();
    transport.mockImplementation(() => Promise.resolve(json({ tariff: tariff(), previous: null, versioned: false, replayed: false, audit: {} })));
    const input = {
      name: 'Месяц', plan_id: 3, duration_days: 30, price_cents: 19900, currency: 'RUB', description: '', features: [],
      badge: '', is_active: true, sort_order: null,
    };
    await api.createTariff(input, 'ui-key');
    expect(JSON.parse(String(transport.mock.calls[0][1]?.body))).toEqual({
      request_key: 'ui-key', name: 'Месяц', plan_id: 3, duration_days: 30, price_cents: 19900, currency: 'RUB',
      description: '', features: [], badge: '', is_active: true,
    });
    await api.updateTariff(7, 4, { ...input, sort_order: 2 }, 'ui-key-2');
    const [path, init] = transport.mock.calls[1];
    expect(path).toBe('/admin/api/tariffs/7');
    expect(init?.method).toBe('PATCH');
    expect(JSON.parse(String(init?.body))).toMatchObject({ version: 4, sort_order: 2, request_key: 'ui-key-2' });
  });

  it('reports the rejected field of invalid_tariff', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ error: 'invalid_tariff', field: 'plan_id' }, 400));
    const error = await api.createTariff({
      name: 'x', plan_id: 1, duration_days: 1, price_cents: 1, currency: 'RUB', description: '', features: [], badge: '', is_active: true,
    }, 'k').catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ code: 'invalid_tariff', status: 400, field: 'plan_id' });
  });

  it('checks the delete confirmation', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ deleted: 7, replayed: false }));
    await expect(api.deleteTariff(7, 1, 'k')).resolves.toBeUndefined();
    transport.mockResolvedValueOnce(json({ deleted: 8 }));
    await expect(api.deleteTariff(7, 1, 'k')).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
