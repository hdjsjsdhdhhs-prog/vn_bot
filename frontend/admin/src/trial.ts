// Pure logic of the trial editor (Admin → «Тарифы» → «Пробная подписка»):
// duration in hours or days, form validation with the server bounds
// (database.TrialDraft), the catalogue grouped by country exactly like
// database.EntryCountry, and country/node selection mapped onto the existing
// builder rules (country rule = whole country, node rule = one server). No DOM,
// no network: covered by tests/admin-trial.test.ts.

import type { BuilderItem, SourceEntry, TrialDraft, TrialLegacyNode, TrialMode, TrialProblem, TrialRule, TrialView } from './api';
import { featuresFromText, moveItem, pluralRu } from './tariffs';

export const TRIAL_LIMITS = {
  minHours: 1, maxHours: 168, minRate: 1, maxRate: 100,
  title: 64, description: 500, features: 8, feature: 80, badge: 24,
} as const;

/** Quick choices, in hours. */
export const TRIAL_DURATION_PRESETS = [3, 24, 72, 168] as const;

const countFormat = new Intl.NumberFormat('ru-RU');
const chars = (value: string) => Array.from(value).length;

export const hoursLabel = (h: number) => `${countFormat.format(h)} ${pluralRu(h, 'час', 'часа', 'часов')}`;
export const daysLabel = (d: number) => `${countFormat.format(d)} ${pluralRu(d, 'день', 'дня', 'дней')}`;

/** "3 часа", "24 часа (1 день)", "168 часов (7 дней)". */
export function trialDurationLabel(hours: number): string {
  if (hours >= 24 && hours % 24 === 0) return `${hoursLabel(hours)} (${daysLabel(hours / 24)})`;
  return hoursLabel(hours);
}

export type DurationUnit = 'hours' | 'days';

/** Raw editor state, as typed. */
export interface TrialForm {
  enabled: boolean;
  duration: string;
  unit: DurationUnit;
  rateLimit: string;
  title: string;
  description: string;
  features: string;
  badge: string;
  builderId: number | null;
  mode: TrialMode;
  sourceIds: number[];
  rules: TrialRule[];
  /** Issuance nodes of the trial plan (plan_nodes), sorted by id. */
  legacyNodeIds: number[];
}

export type TrialField = 'duration' | 'rateLimit' | 'title' | 'description' | 'features' | 'badge' | 'builderId' | 'composition' | 'legacyNodes';
export type TrialErrors = Partial<Record<TrialField, string>>;

/** Server field (database.TrialProblem.Field) → editor field. */
export const TRIAL_SERVER_FIELDS: Readonly<Record<string, TrialField>> = {
  duration_hours: 'duration', rate_limit_per_hour: 'rateLimit', title: 'title', description: 'description',
  features: 'features', badge: 'badge', builder_id: 'builderId', composition: 'composition',
  'composition.rules': 'composition', 'composition.source_ids': 'composition',
  legacy_nodes: 'legacyNodes', legacy_node_ids: 'legacyNodes',
};

// ---------------------------------------------------------------------------
// Issuance node (legacy nodes of the trial plan). It only carries the client
// CreateTrial provisions; the VPN composition comes from the builder.

/** Linked active nodes: the choice the editor starts from (sorted by id). */
export const linkedNodeIds = (nodes: readonly TrialLegacyNode[]) =>
  nodes.filter(n => n.linked && n.is_active).map(n => n.id).sort((a, b) => a - b);

/** Ticks or unticks one node; the result stays sorted by id. */
export function toggleLegacyNode(ids: readonly number[], id: number): number[] {
  return (ids.includes(id) ? ids.filter(other => other !== id) : [...ids, id]).sort((a, b) => a - b);
}

export const sameNodeIds = (a: readonly number[], b: readonly number[]) =>
  a.length === b.length && a.every((id, i) => id === b[i]);

/** Node type as stored (3x-ui | proxman | fetch). */
export const legacyNodeType = (type: string) => type || '—';

/** Builder composition as edited: stored rules of a builder (enabled only). */
export interface CompositionState { builderVersion: number; mode: TrialMode; sourceIds: number[]; rules: TrialRule[] }

/** Builder items → composition, like database.compositionOf (enabled rules in position order). */
export function compositionFromBuilder(version: number, sourceIds: readonly number[], items: readonly BuilderItem[]): CompositionState {
  const rules = [...items].filter(item => item.enabled)
    .sort((a, b) => a.position - b.position || a.id - b.id)
    .map((item): TrialRule => ({
      kind: item.kind === 'country' ? 'country' : 'node', source_id: item.source_id,
      country_code: item.kind === 'country' ? item.country_code.toUpperCase() : '',
      fingerprint: item.kind === 'node' ? item.fingerprint : '', original_name: item.kind === 'node' ? item.original_name : '',
    }));
  return { builderVersion: version, mode: items.length ? 'selected' : 'all', sourceIds: [...sourceIds], rules };
}

export function formFromView(view: TrialView): TrialForm {
  const s = view.settings;
  const days = s.duration_hours >= 24 && s.duration_hours % 24 === 0;
  const c = view.composition;
  return {
    enabled: s.enabled, duration: String(days ? s.duration_hours / 24 : s.duration_hours), unit: days ? 'days' : 'hours',
    rateLimit: String(s.rate_limit_per_hour), title: s.title, description: s.description, features: s.features.join('\n'),
    badge: s.badge, builderId: view.builder_id,
    mode: c?.mode ?? 'all', sourceIds: c ? [...c.source_ids] : [], rules: c ? c.rules.map(rule => ({ ...rule })) : [],
    legacyNodeIds: linkedNodeIds(view.legacy_nodes),
  };
}

/** The form with the composition of another builder (on builder change). */
export function withComposition(form: TrialForm, builderId: number | null, c: CompositionState | null): TrialForm {
  return { ...form, builderId, mode: c?.mode ?? 'all', sourceIds: c ? [...c.sourceIds] : [], rules: c ? c.rules.map(r => ({ ...r })) : [] };
}

export const sameTrialForm = (a: TrialForm, b: TrialForm) => JSON.stringify(a) === JSON.stringify(b);

/** Hours of the duration input; NaN when it is not a whole number. */
export function durationHours(form: Pick<TrialForm, 'duration' | 'unit'>): number {
  const text = form.duration.trim();
  if (!/^\d{1,4}$/.test(text)) return NaN;
  return Number(text) * (form.unit === 'days' ? 24 : 1);
}

const ruleKey = (r: TrialRule) => (r.kind === 'country' ? `c|${r.source_id}|${r.country_code}` : `n|${r.source_id}|${r.fingerprint}`);
const compositionKey = (mode: TrialMode, sourceIds: readonly number[], rules: readonly TrialRule[]) =>
  JSON.stringify([mode, sourceIds, rules.map(ruleKey)]);

/** Whether the edited composition differs from the builder's stored one. */
export function compositionChanged(form: TrialForm, stored: CompositionState | null): boolean {
  if (!stored) return false;
  return compositionKey(form.mode, form.sourceIds, form.rules) !== compositionKey(stored.mode, stored.sourceIds, stored.rules);
}

/**
 * Validates with the server bounds. The draft carries the composition only
 * when it was edited and may be edited (a builder used by nothing but the
 * trial), so saving never rewrites rules nobody touched. Likewise the issuance
 * nodes are sent only when they differ from storedNodes (the linked active
 * nodes the editor loaded); without storedNodes they are never sent.
 */
export function validateTrialForm(form: TrialForm, stored: CompositionState | null, editable: boolean,
  storedNodes: readonly number[] | null = null): { draft: TrialDraft | null; errors: TrialErrors } {
  const errors: TrialErrors = {};
  const hours = durationHours(form);
  if (!Number.isInteger(hours) || hours < TRIAL_LIMITS.minHours || hours > TRIAL_LIMITS.maxHours) {
    errors.duration = `Срок — от ${TRIAL_LIMITS.minHours} часа до ${TRIAL_LIMITS.maxHours} часов (7 дней).`;
  }
  const rateText = form.rateLimit.trim();
  const rate = /^\d{1,3}$/.test(rateText) ? Number(rateText) : NaN;
  if (!Number.isInteger(rate) || rate < TRIAL_LIMITS.minRate || rate > TRIAL_LIMITS.maxRate) {
    errors.rateLimit = `Целое число от ${TRIAL_LIMITS.minRate} до ${TRIAL_LIMITS.maxRate}.`;
  }
  const title = form.title.trim();
  if (chars(title) > TRIAL_LIMITS.title) errors.title = `Не длиннее ${TRIAL_LIMITS.title} символов.`;
  const description = form.description.replace(/\r\n/g, '\n').trim();
  if (chars(description) > TRIAL_LIMITS.description) errors.description = `Не длиннее ${TRIAL_LIMITS.description} символов.`;
  const features = featuresFromText(form.features);
  if (features.length > TRIAL_LIMITS.features) errors.features = `Не больше ${TRIAL_LIMITS.features} пунктов.`;
  else if (features.some(item => chars(item) > TRIAL_LIMITS.feature)) errors.features = `Каждый пункт — не длиннее ${TRIAL_LIMITS.feature} символов.`;
  const badge = form.badge.trim();
  if (chars(badge) > TRIAL_LIMITS.badge) errors.badge = `Не длиннее ${TRIAL_LIMITS.badge} символов.`;

  const changed = form.builderId !== null && compositionChanged(form, stored);
  if (changed && !editable) errors.composition = 'Состав этого построителя меняется только в разделе «Построители» или в его копии.';
  if (changed && editable) {
    if (form.sourceIds.length === 0) errors.composition = 'Выберите хотя бы один источник.';
    else if (form.mode === 'selected' && form.rules.length === 0) errors.composition = 'Выберите хотя бы одну страну или сервер.';
  }

  const nodesChanged = storedNodes !== null && !sameNodeIds(form.legacyNodeIds, storedNodes);
  if (nodesChanged && form.enabled && form.legacyNodeIds.length === 0) {
    errors.legacyNodes = 'Выберите хотя бы один активный узел выдачи или выключите выдачу пробной подписки.';
  }

  if (Object.keys(errors).length > 0) return { draft: null, errors };
  return {
    draft: {
      enabled: form.enabled, duration_hours: hours, rate_limit_per_hour: rate, title, description, features, badge,
      builder_id: form.builderId,
      composition: changed && stored
        ? { builder_version: stored.builderVersion, mode: form.mode, source_ids: [...form.sourceIds], rules: form.mode === 'all' ? [] : form.rules.map(r => ({ ...r })) }
        : null,
      legacy_node_ids: nodesChanged ? [...form.legacyNodeIds] : null,
    },
    errors,
  };
}

// ---------------------------------------------------------------------------
// Catalogue by country (database.EntryCountry: catalogue code, else the flag
// emoji in the name; '' = no country).

export function countryFromName(name: string): string {
  const points = Array.from(name).map(ch => ch.codePointAt(0) ?? 0);
  for (let i = 0; i + 1 < points.length; i++) {
    const [a, b] = [points[i], points[i + 1]];
    if (a >= 0x1F1E6 && a <= 0x1F1FF && b >= 0x1F1E6 && b <= 0x1F1FF) {
      return String.fromCharCode(65 + a - 0x1F1E6, 65 + b - 0x1F1E6);
    }
  }
  return '';
}

export const entryCountry = (e: Pick<SourceEntry, 'country_code' | 'original_name'>) =>
  (e.country_code || countryFromName(e.original_name)).toUpperCase();

export interface CountryGroup { code: string; entries: SourceEntry[] }

/** Present entries grouped by country, sorted by code; "no country" last. */
export function groupByCountry(entries: readonly SourceEntry[]): CountryGroup[] {
  const groups = new Map<string, SourceEntry[]>();
  for (const e of entries) {
    if (!e.present) continue;
    const code = entryCountry(e);
    const list = groups.get(code) ?? [];
    list.push(e);
    groups.set(code, list);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)))
    .map(([code, list]) => ({ code, entries: list }));
}

const regionNames = typeof Intl.DisplayNames === 'function' ? new Intl.DisplayNames(['ru'], { type: 'region' }) : null;

/** "Германия · DE", "Без страны". */
export function countryName(code: string): string {
  if (!code) return 'Без страны';
  let name = '';
  try { name = regionNames?.of(code) ?? ''; } catch { name = ''; }
  return name && name !== code ? `${name} · ${code}` : code;
}

// ---------------------------------------------------------------------------
// Selection → builder rules

export type CountrySelection = 'country' | 'nodes' | 'none';

const isCountryRule = (r: TrialRule, sourceId: number, code: string) => r.kind === 'country' && r.source_id === sourceId && r.country_code === code;
const isNodeRule = (r: TrialRule, sourceId: number, fp: string) => r.kind === 'node' && r.source_id === sourceId && r.fingerprint === fp;
const nodeRule = (sourceId: number, e: SourceEntry): TrialRule =>
  ({ kind: 'node', source_id: sourceId, country_code: '', fingerprint: e.fingerprint, original_name: e.original_name });

/** How a country of a source is selected: the whole country, some nodes, nothing. */
export function countrySelection(rules: readonly TrialRule[], sourceId: number, group: CountryGroup): { state: CountrySelection; nodes: number } {
  if (group.code && rules.some(r => isCountryRule(r, sourceId, group.code))) return { state: 'country', nodes: group.entries.length };
  const nodes = group.entries.filter(e => rules.some(r => isNodeRule(r, sourceId, e.fingerprint))).length;
  return { state: nodes ? 'nodes' : 'none', nodes };
}

export const nodeSelected = (rules: readonly TrialRule[], sourceId: number, group: CountryGroup, e: SourceEntry) =>
  (group.code !== '' && rules.some(r => isCountryRule(r, sourceId, group.code))) || rules.some(r => isNodeRule(r, sourceId, e.fingerprint));

/**
 * Toggles a whole country. Selecting it replaces its node rules by one country
 * rule (new servers of the country then join automatically). A group without
 * a country has no country rule: it selects or clears all its nodes.
 */
export function toggleCountry(rules: readonly TrialRule[], sourceId: number, group: CountryGroup): TrialRule[] {
  const { state } = countrySelection(rules, sourceId, group);
  const fps = new Set(group.entries.map(e => e.fingerprint));
  const others = rules.filter(r => !(r.source_id === sourceId && ((r.kind === 'node' && fps.has(r.fingerprint)) || isCountryRule(r, sourceId, group.code))));
  if (!group.code) {
    return state === 'none' ? [...others, ...group.entries.map(e => nodeRule(sourceId, e))] : others;
  }
  if (state !== 'none') return others;
  return [...others, { kind: 'country', source_id: sourceId, country_code: group.code, fingerprint: '', original_name: '' }];
}

/**
 * Toggles one node. Unticking a node of a whole-country selection turns the
 * country rule into node rules for the other servers (at the same position),
 * so the result is exactly what is ticked.
 */
export function toggleNode(rules: readonly TrialRule[], sourceId: number, group: CountryGroup, e: SourceEntry): TrialRule[] {
  const countryIndex = group.code ? rules.findIndex(r => isCountryRule(r, sourceId, group.code)) : -1;
  if (countryIndex >= 0) {
    const rest = group.entries.filter(other => other.fingerprint !== e.fingerprint).map(other => nodeRule(sourceId, other));
    return [...rules.slice(0, countryIndex), ...rest, ...rules.slice(countryIndex + 1)];
  }
  if (rules.some(r => isNodeRule(r, sourceId, e.fingerprint))) return rules.filter(r => !isNodeRule(r, sourceId, e.fingerprint));
  return [...rules, nodeRule(sourceId, e)];
}

/** Rules of sources no longer in the composition are dropped. */
export const rulesForSources = (rules: readonly TrialRule[], sourceIds: readonly number[]) =>
  rules.filter(r => sourceIds.includes(r.source_id));

export const moveRule = (rules: readonly TrialRule[], index: number, delta: -1 | 1) => moveItem(rules, index, index + delta);

// ---------------------------------------------------------------------------
// Problems (database.TrialProblem codes)

const PROBLEM_TEXT: Readonly<Record<string, string>> = {
  out_of_range: 'Значение вне допустимого диапазона.',
  too_long: 'Слишком длинный текст.',
  too_many: 'Слишком много элементов.',
  no_trial_node: 'У тарифа «trial» нет узла: пробную подписку не на чем создать.',
  builder_not_found: 'Построитель не найден: возможно, его удалили.',
  builder_disabled: 'Построитель отключён: новые пробные подписки не выдаются.',
  builder_shared: 'Построитель используется другими тарифами или подписками: его состав здесь не меняется. Создайте копию.',
  builder_required: 'Состав задаётся только вместе с построителем.',
  builder_version_conflict: 'Построитель изменили в другом окне.',
  mode: 'Неизвестный режим состава.',
  required: 'Выберите источники и хотя бы одну страну или сервер.',
  duplicate: 'Источник выбран дважды.',
  source_not_found: 'Один из источников не найден.',
  rules_in_all_mode: 'В режиме «все страны» отдельные правила не нужны.',
  rule_source_not_linked: 'Правило относится к источнику, который не выбран.',
  country_code: 'Страна без кода выбирается только по серверам.',
  country_not_found: 'Выбранной страны нет в каталоге источника.',
  node_not_found: 'Выбранного сервера нет в каталоге источника.',
  kind: 'Неизвестный тип правила.',
  duplicate_rule: 'Правило повторяется.',
  node_covered_by_country: 'Сервер уже входит в выбранную целиком страну.',
  no_sources: 'У построителя нет источников.',
  empty_result: 'Состав пуст: ни одного доступного сервера.',
  legacy_node_not_found: 'Выбранный узел выдачи не найден: возможно, его удалили.',
  legacy_node_inactive: 'Выбранный узел выдачи отключён: на нём пробную подписку не создать.',
};

/** Codes whose meaning depends on the field. */
const FIELD_PROBLEM_TEXT: Readonly<Record<string, string>> = {
  'legacy_node_ids|duplicate': 'Узел выдачи выбран дважды.',
  'legacy_node_ids|too_many': 'Слишком много узлов выдачи.',
};

export const problemText = (p: Pick<TrialProblem, 'code'> & { field?: string }) =>
  FIELD_PROBLEM_TEXT[`${p.field ?? ''}|${p.code}`] ?? PROBLEM_TEXT[p.code] ?? `Конфигурация отклонена (${p.code}).`;
