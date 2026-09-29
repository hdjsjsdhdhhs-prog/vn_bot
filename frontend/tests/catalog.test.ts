import { describe, expect, it, vi } from 'vitest';
import { Api, catalogOrder } from '../src/api';
import type { Offer } from '../src/api';
import { catalog, productDetail } from '../src/ui';
import { json, offer } from './fixtures';

// Tariff editor presentation in the Mini App: description, features, badge and
// the shared sort_order, with the historical card as fallback.
const configured: Offer = {
  ...offer, offer_id: 'c'.repeat(32), name: 'Год', amount_cents: 990, sort_order: 0,
  description: 'Лучшая цена\nна весь год', features: ['Все серверы', '  ', 'Приоритетная поддержка'], badge: 'Хит',
};
const plain: Offer = { ...offer, sort_order: 1, description: '', features: [], badge: '' };

describe('catalogue presentation', () => {
  it('validates the optional presentation fields on the wire', async () => {
    const transport = vi.fn<typeof fetch>();
    const api = new Api('signed', transport);
    transport.mockResolvedValue(json({ offers: [configured, offer] }));
    await expect(api.offers()).resolves.toEqual({ offers: [configured, offer] });
    for (const bad of [{ features: 'x' }, { features: [1] }, { badge: 1 }, { description: null }, { sort_order: -1 }, { sort_order: 1.5 }]) {
      transport.mockResolvedValue(json({ offers: [{ ...offer, ...bad }] }));
      await expect(api.offers()).rejects.toMatchObject({ code: 'invalid_response' });
    }
  });

  it('orders by sort_order and keeps the server order for ties and missing values', () => {
    const a = { ...offer, offer_id: '1'.repeat(32), sort_order: 2 };
    const b = { ...offer, offer_id: '2'.repeat(32), sort_order: 0 };
    const c = { ...offer, offer_id: '3'.repeat(32), sort_order: 0 };
    const legacy = { ...offer, offer_id: '4'.repeat(32) };
    expect(catalogOrder([a, b, c, legacy]).map(item => item.offer_id)).toEqual([b, c, legacy, a].map(item => item.offer_id));
  });

  it('renders badge, description and features on the catalogue card in catalogue order', () => {
    const node = catalog([plain, configured]);
    const cards = Array.from(node.querySelectorAll<HTMLAnchorElement>('a.offer-card'));
    expect(cards.map(card => card.querySelector('h2')?.textContent)).toEqual(['Год', offer.name]);
    const [first, second] = cards;
    expect(first.querySelector('.offer-badge')?.textContent).toBe('Хит');
    expect(first.querySelector('.offer-description')?.textContent).toBe('Лучшая цена\nна весь год');
    expect(Array.from(first.querySelectorAll('.offer-features li'), li => li.textContent)).toEqual(['Все серверы', 'Приоритетная поддержка']);
    // Fallback: the historical card, without empty presentation elements.
    expect(second.querySelector('.offer-badge')).toBeNull();
    expect(second.querySelector('.offer-description')).toBeNull();
    expect(second.querySelector('.offer-features')).toBeNull();
    expect(second.textContent).toContain('30 дней доступа');
  });

  it('never interprets presentation text as HTML', () => {
    const node = catalog([{ ...configured, badge: '<img src=x onerror=alert(1)>', features: ['<b>bold</b>'] }]);
    expect(node.querySelector('img')).toBeNull();
    expect(node.querySelector('b')).toBeNull();
    expect(node.querySelector('.offer-features li')?.textContent).toBe('<b>bold</b>');
  });

  it('product detail uses configured features, otherwise the default copy', () => {
    const detailed = productDetail(configured);
    expect(detailed.querySelector('.badge')?.textContent).toBe('Хит');
    expect(Array.from(detailed.querySelectorAll('.offer-features li'), li => li.textContent)).toEqual(['Все серверы', 'Приоритетная поддержка']);
    expect(detailed.textContent).not.toContain('Без автоматических списаний');

    const fallback = productDetail(offer);
    expect(fallback.querySelector('.badge')?.textContent).toBe('Доступ по подписке');
    expect(fallback.querySelector('.offer-features')).toBeNull();
    expect(fallback.textContent).toContain('✦ Подключение через ваш личный кабинет');
    expect(fallback.textContent).toContain('✦ Без автоматических списаний');
  });
});
