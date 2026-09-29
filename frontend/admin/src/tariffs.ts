// Pure logic of the tariff editor: price formatting/parsing, form validation
// with the server bounds, and detection of purchase-term changes. No DOM, no
// network: covered by tests/admin-tariffs.test.ts.

import { TARIFF_LIMITS, type AdminTariff, type TariffInput } from './api';

/** Currencies offered by the editor. XTR = Telegram Stars (Mini App only). */
export const TARIFF_CURRENCIES = ['RUB', 'XTR'] as const;
export const DURATION_PRESETS = [30, 90, 180, 365] as const;
export const BADGE_PRESETS = ['Хит', 'Выгодно', 'Новинка'] as const;

const countFormat = new Intl.NumberFormat('ru-RU');

/** XTR is stored as whole Stars; other currencies in minor units (1/100). */
export const isWholeUnits = (currency: string) => currency === 'XTR';

const chars = (value: string) => Array.from(value).length;

export function pluralRu(n: number, one: string, few: string, many: string): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return one;
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few;
  return many;
}

export const durationLabel = (days: number) => `${countFormat.format(days)} ${pluralRu(days, 'день', 'дня', 'дней')}`;

/** Human price, e.g. "199 ₽", "199,90 ₽", "150 ★", "12,50 USD". */
export function formatTariffPrice(amount: number, currency: string): string {
  if (isWholeUnits(currency)) return `${countFormat.format(amount)} ★`;
  const major = amount / 100;
  const text = new Intl.NumberFormat('ru-RU', { minimumFractionDigits: amount % 100 === 0 ? 0 : 2, maximumFractionDigits: 2 }).format(major);
  return currency === 'RUB' ? `${text} ₽` : `${text} ${currency}`;
}

/**
 * The bot's product button (bot.KeyboardBuilder.BuyProductList +
 * utils.FormatPriceCents): "Месяц — 199₽" / "Месяц — 199.99₽".
 */
export function botButtonLabel(name: string, amount: number): string {
  const rub = Math.trunc(amount / 100);
  const kop = amount % 100;
  return `${name} — ${kop === 0 ? rub : `${rub}.${String(kop).padStart(2, '0')}`}₽`;
}

/** Value for the price input: "199", "199.9" → "199,90"; XTR as an integer. */
export function priceInputValue(amount: number, currency: string): string {
  if (isWholeUnits(currency)) return String(amount);
  const rub = Math.trunc(amount / 100);
  const kop = amount % 100;
  return kop === 0 ? String(rub) : `${rub},${String(kop).padStart(2, '0')}`;
}

/** Parses the price input into minor units (whole Stars for XTR); null when invalid. */
export function parsePriceInput(raw: string, currency: string): number | null {
  const text = raw.trim().replace(/\s+/g, '').replace(',', '.');
  if (isWholeUnits(currency)) {
    if (!/^\d{1,9}$/.test(text)) return null;
    return Number(text);
  }
  const match = /^(\d{1,9})(?:\.(\d{1,2}))?$/.exec(text);
  if (!match) return null;
  return Number(match[1]) * 100 + Number((match[2] ?? '').padEnd(2, '0') || '0');
}

/** One feature per line; blank lines are dropped (the server does the same). */
export const featuresFromText = (text: string) => text.split(/\r?\n/).map(line => line.trim()).filter(Boolean);

/** Raw editor state, as typed. */
export interface TariffForm {
  name: string;
  planId: number | null;
  durationDays: string;
  price: string;
  currency: string;
  description: string;
  features: string;
  badge: string;
  isActive: boolean;
  /** Catalogue position; blank appends on create and keeps it on update. */
  sortOrder: string;
}

export type TariffField = 'name' | 'planId' | 'durationDays' | 'price' | 'currency' | 'description' | 'features' | 'badge' | 'sortOrder';
export type FieldErrors = Partial<Record<TariffField, string>>;

/** Server field names (database.TariffFieldError.Field) → editor fields. */
export const SERVER_FIELDS: Readonly<Record<string, TariffField>> = {
  name: 'name', plan_id: 'planId', duration_days: 'durationDays', price_cents: 'price', currency: 'currency',
  description: 'description', features: 'features', badge: 'badge', sort_order: 'sortOrder',
};

export function formFromTariff(tariff: AdminTariff | null, defaultPlanId: number | null): TariffForm {
  if (!tariff) {
    return {
      name: '', planId: defaultPlanId, durationDays: '30', price: '', currency: 'RUB', description: '', features: '', badge: '',
      isActive: true, sortOrder: '',
    };
  }
  return {
    name: tariff.name, planId: tariff.plan_id, durationDays: String(tariff.duration_days),
    price: priceInputValue(tariff.price_cents, tariff.currency), currency: tariff.currency,
    description: tariff.description, features: tariff.features.join('\n'), badge: tariff.badge, isActive: tariff.is_active,
    sortOrder: String(tariff.sort_order),
  };
}

export const sameForm = (a: TariffForm, b: TariffForm) => JSON.stringify(a) === JSON.stringify(b);

/** Validates with the server bounds; input is null when any field is invalid. */
export function validateTariffForm(form: TariffForm): { input: TariffInput | null; errors: FieldErrors } {
  const errors: FieldErrors = {};
  const name = form.name.trim();
  if (!name) errors.name = 'Введите название.';
  else if (chars(name) > TARIFF_LIMITS.name) errors.name = `Не длиннее ${TARIFF_LIMITS.name} символов.`;

  if (form.planId === null) errors.planId = 'Выберите план.';

  const duration = /^\d{1,5}$/.test(form.durationDays.trim()) ? Number(form.durationDays.trim()) : NaN;
  if (!Number.isInteger(duration) || duration < 1 || duration > TARIFF_LIMITS.maxDurationDays) {
    errors.durationDays = `Срок — целое число дней от 1 до ${TARIFF_LIMITS.maxDurationDays}.`;
  }

  if (!/^[A-Z]{3}$/.test(form.currency)) errors.currency = 'Выберите валюту.';
  const price = parsePriceInput(form.price, form.currency);
  if (price === null) {
    errors.price = isWholeUnits(form.currency) ? 'Цена в звёздах — целое число.' : 'Введите цену, например 199 или 199,90.';
  } else if (price <= 0) errors.price = 'Цена должна быть больше нуля.';
  else if (price > TARIFF_LIMITS.maxPriceCents) errors.price = 'Слишком большая цена.';

  const description = form.description.replace(/\r\n/g, '\n').trim();
  if (chars(description) > TARIFF_LIMITS.description) errors.description = `Не длиннее ${TARIFF_LIMITS.description} символов.`;

  const features = featuresFromText(form.features);
  if (features.length > TARIFF_LIMITS.features) errors.features = `Не больше ${TARIFF_LIMITS.features} пунктов.`;
  else if (features.some(item => chars(item) > TARIFF_LIMITS.feature)) errors.features = `Каждый пункт — не длиннее ${TARIFF_LIMITS.feature} символов.`;

  const badge = form.badge.trim();
  if (chars(badge) > TARIFF_LIMITS.badge) errors.badge = `Не длиннее ${TARIFF_LIMITS.badge} символов.`;

  const sortText = form.sortOrder.trim();
  const sortOrder = sortText === '' ? null : /^\d{1,6}$/.test(sortText) ? Number(sortText) : NaN;
  if (sortOrder !== null && (!Number.isInteger(sortOrder) || sortOrder > TARIFF_LIMITS.maxSortOrder)) {
    errors.sortOrder = `Позиция — целое число от 0 до ${countFormat.format(TARIFF_LIMITS.maxSortOrder)} или пусто.`;
  }

  if (Object.keys(errors).length > 0 || form.planId === null || price === null) return { input: null, errors };
  return {
    input: {
      name, plan_id: form.planId, duration_days: duration, price_cents: price, currency: form.currency,
      description, features, badge, is_active: form.isActive, sort_order: sortOrder,
    },
    errors,
  };
}

/**
 * Whether the immutable purchase terms differ (database.TariffInput.sameTerms).
 * For a used tariff this means the save creates a new version.
 */
export function termsChanged(tariff: AdminTariff, input: TariffInput): boolean {
  return tariff.name !== input.name || tariff.plan_id !== input.plan_id || tariff.duration_days !== input.duration_days ||
    tariff.price_cents !== input.price_cents || tariff.currency !== input.currency;
}

/** Catalogue position order used by the bot and the Mini App. */
export function catalogueOrder(tariffs: readonly AdminTariff[]): AdminTariff[] {
  return [...tariffs].sort((a, b) => a.sort_order - b.sort_order || a.price_cents - b.price_cents || a.id - b.id);
}

/** A retired version (replaced by a successor) is frozen. */
export const isRetired = (tariff: AdminTariff) => tariff.replaced_by_id !== null;

/**
 * Full reorder list for POST /tariffs/reorder: the edited order of the current
 * tariffs, then the retired versions in their existing order.
 */
export function reorderIds(currentOrder: readonly number[], all: readonly AdminTariff[]): number[] {
  const retired = catalogueOrder(all).filter(isRetired).map(tariff => tariff.id);
  return [...currentOrder, ...retired.filter(id => !currentOrder.includes(id))];
}

export function moveItem<T>(list: readonly T[], from: number, to: number): T[] {
  const next = [...list];
  if (from < 0 || from >= next.length || to < 0 || to >= next.length) return next;
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}
