import { describe, expect, it, vi } from 'vitest';
import { AdminApi, ApiError, type SourceEntry, type TrialPreview, type TrialView } from '../admin/src/api';
import {
  TRIAL_SERVER_FIELDS, compositionChanged, compositionFromBuilder, countryFromName, countryName, countrySelection, durationHours,
  entryCountry, formFromView, groupByCountry, legacyNodeType, linkedNodeIds, moveRule, nodeSelected, problemText, rulesForSources,
  sameNodeIds, toggleCountry, toggleLegacyNode, toggleNode, trialDurationLabel, validateTrialForm, withComposition,
  type CompositionState, type TrialForm,
} from '../admin/src/trial';

// Pure logic of the trial editor (admin/src/trial.ts) and the trial part of the
// admin API client, against database.TrialDraft bounds, database.EntryCountry
// and the builder rule semantics (country rule = whole country, node rule =
// one server).

const entry = (id: number, fingerprint: string, name: string, country = '', present = true): SourceEntry => ({
  id, source_id: 1, fingerprint, original_name: name, protocol: 'vless', country_code: country, upstream_position: id,
  present, last_seen_at: '2026-09-30T10:00:00Z',
});

const CATALOGUE = [
  entry(1, 'de-1', '🇩🇪 Berlin', 'DE'), entry(2, 'de-2', '🇩🇪 Munich'), entry(3, 'nl-1', 'Amsterdam', 'NL'),
  entry(4, 'x-1', 'Unknown'), entry(5, 'gone', '🇩🇪 Gone', 'DE', false),
];

const preview = (over: Partial<TrialPreview> = {}): TrialPreview => ({
  issuable: true, enabled: true, problems: [], serve: 'builder', duration_hours: 3, expires_at: '2026-10-01T15:00:00Z',
  builder: null, mode: 'all', sources: [], legacy_nodes: 1, total: 0, countries: [], items: [], warnings: [], missing: 0,
  conflicts: 0, ...over,
});

const view = (over: Partial<TrialView> = {}): TrialView => ({
  settings: {
    enabled: true, duration_hours: 72, rate_limit_per_hour: 3, title: 'Пробный', description: '', features: ['Без карты'],
    badge: '', version: 2, stored: true, updated_at: '2026-10-01T10:00:00Z',
  },
  defaults: { duration_hours: 3, rate_limit_per_hour: 3 }, plan_id: 1, builder_id: 9,
  composition: { builder_version: 4, mode: 'selected', source_ids: [1], rules: [{ kind: 'country', source_id: 1, country_code: 'DE', fingerprint: '', original_name: '' }] },
  builders: [], preview: preview(), active_trials: 0, history: [],
  legacy_nodes: [
    { id: 3, name: 'Panel', type: '3x-ui', is_active: true, linked: true },
    { id: 1, name: 'Fetch', type: 'fetch', is_active: true, linked: false },
    { id: 2, name: 'Old', type: '3x-ui', is_active: false, linked: true },
  ],
  ...over,
});

const stored = (v: TrialView): CompositionState => ({
  builderVersion: v.composition!.builder_version, mode: v.composition!.mode, sourceIds: [...v.composition!.source_ids],
  rules: v.composition!.rules.map(r => ({ ...r })),
});

describe('trial duration', () => {
  it('reads hours or days and labels them', () => {
    expect(durationHours({ duration: '3', unit: 'hours' })).toBe(3);
    expect(durationHours({ duration: '7', unit: 'days' })).toBe(168);
    expect(durationHours({ duration: '1,5', unit: 'hours' })).toBeNaN();
    expect(trialDurationLabel(3)).toBe('3 часа');
    expect(trialDurationLabel(21)).toBe('21 час');
    expect(trialDurationLabel(72)).toBe('72 часа (3 дня)');
  });

  it('opens a whole number of days in days', () => {
    expect(formFromView(view())).toMatchObject({ duration: '3', unit: 'days', rateLimit: '3', features: 'Без карты', builderId: 9, mode: 'selected' });
    const v = view({ settings: { ...view().settings, duration_hours: 5 } });
    expect(formFromView(v)).toMatchObject({ duration: '5', unit: 'hours' });
  });
});

describe('trial form validation', () => {
  const base = () => formFromView(view());

  it('enforces the server bounds (1..168 hours, 1..100 per IP)', () => {
    for (const [duration, unit] of [['0', 'hours'], ['169', 'hours'], ['8', 'days'], ['', 'hours']] as const) {
      const { draft, errors } = validateTrialForm({ ...base(), duration, unit }, null, true);
      expect(draft).toBeNull();
      expect(errors.duration).toBeTruthy();
    }
    expect(validateTrialForm({ ...base(), rateLimit: '0' }, null, true).errors.rateLimit).toBeTruthy();
    expect(validateTrialForm({ ...base(), badge: 'x'.repeat(25) }, null, true).errors.badge).toBeTruthy();
    expect(validateTrialForm({ ...base(), features: 'a\nb\nc\nd\ne\nf\ng\nh\ni' }, null, true).errors.features).toBeTruthy();
  });

  it('sends the composition only when it was edited', () => {
    const v = view();
    const form = formFromView(v);
    expect(validateTrialForm(form, stored(v), true).draft?.composition).toBeNull();
    const edited: TrialForm = { ...form, rules: [...form.rules, { kind: 'node', source_id: 1, country_code: '', fingerprint: 'nl-1', original_name: 'Amsterdam' }] };
    expect(compositionChanged(edited, stored(v))).toBe(true);
    const { draft } = validateTrialForm(edited, stored(v), true);
    expect(draft?.composition).toEqual({ builder_version: 4, mode: 'selected', source_ids: [1], rules: edited.rules });
    expect(draft).toMatchObject({ duration_hours: 72, rate_limit_per_hour: 3, builder_id: 9, features: ['Без карты'] });
  });

  it('refuses composition edits of a shared builder and empty selections', () => {
    const v = view();
    const edited: TrialForm = { ...formFromView(v), rules: [] };
    expect(validateTrialForm(edited, stored(v), false).errors.composition).toMatch(/Построители/);
    expect(validateTrialForm(edited, stored(v), true).errors.composition).toMatch(/хотя бы одну/);
    expect(validateTrialForm({ ...edited, mode: 'all', sourceIds: [] }, stored(v), true).errors.composition).toMatch(/источник/);
    // "All countries" sends no rules.
    const all = validateTrialForm({ ...edited, mode: 'all' }, stored(v), true).draft;
    expect(all?.composition).toMatchObject({ mode: 'all', rules: [] });
  });

  it('switches builders with their stored composition', () => {
    const form = withComposition(formFromView(view()), 5, { builderVersion: 2, mode: 'all', sourceIds: [3], rules: [] });
    expect(form).toMatchObject({ builderId: 5, mode: 'all', sourceIds: [3], rules: [] });
    expect(withComposition(form, null, null)).toMatchObject({ builderId: null, sourceIds: [], rules: [] });
  });
});

describe('trial issuance node (plan_nodes of the trial plan)', () => {
  it('starts from the linked active nodes, sorted by id', () => {
    expect(linkedNodeIds(view().legacy_nodes)).toEqual([3]);
    expect(formFromView(view()).legacyNodeIds).toEqual([3]);
  });

  it('toggles nodes and keeps them sorted', () => {
    expect(toggleLegacyNode([3], 1)).toEqual([1, 3]);
    expect(toggleLegacyNode([1, 3], 3)).toEqual([1]);
    expect(sameNodeIds([1, 3], [1, 3])).toBe(true);
    expect(sameNodeIds([1], [1, 3])).toBe(false);
    expect(legacyNodeType('fetch')).toBe('fetch');
    expect(legacyNodeType('')).toBe('—');
  });

  it('sends legacy_node_ids only when the choice changed', () => {
    const v = view();
    const form = formFromView(v);
    const baseline = linkedNodeIds(v.legacy_nodes);
    expect(validateTrialForm(form, stored(v), true, baseline).draft?.legacy_node_ids).toBeNull();
    // Without a baseline the links are never sent.
    expect(validateTrialForm({ ...form, legacyNodeIds: [1] }, stored(v), true).draft?.legacy_node_ids).toBeNull();
    const picked = { ...form, legacyNodeIds: toggleLegacyNode(form.legacyNodeIds, 1) };
    expect(validateTrialForm(picked, stored(v), true, baseline).draft?.legacy_node_ids).toEqual([1, 3]);
    // The composition stays untouched when only the nodes change.
    expect(validateTrialForm(picked, stored(v), true, baseline).draft?.composition).toBeNull();
  });

  it('an enabled trial needs a node; a switched-off one may have none', () => {
    const v = view();
    const baseline = linkedNodeIds(v.legacy_nodes);
    const none = { ...formFromView(v), legacyNodeIds: [] };
    const { draft, errors } = validateTrialForm(none, stored(v), true, baseline);
    expect(draft).toBeNull();
    expect(errors.legacyNodes).toMatch(/узел выдачи/);
    expect(validateTrialForm({ ...none, enabled: false }, stored(v), true, baseline).draft?.legacy_node_ids).toEqual([]);
  });

  it('maps server fields and explains node problems', () => {
    expect(TRIAL_SERVER_FIELDS.legacy_node_ids).toBe('legacyNodes');
    expect(TRIAL_SERVER_FIELDS.legacy_nodes).toBe('legacyNodes');
    expect(problemText({ code: 'no_trial_node' })).toMatch(/нет узла/);
    expect(problemText({ code: 'legacy_node_inactive' })).toMatch(/отключён/);
    expect(problemText({ code: 'duplicate', field: 'legacy_node_ids' })).toBe('Узел выдачи выбран дважды.');
    expect(problemText({ code: 'duplicate' })).toBe('Источник выбран дважды.');
  });
});

describe('catalogue by country', () => {
  it('uses the catalogue code, else the flag in the name (database.EntryCountry)', () => {
    expect(countryFromName('🇩🇪 Munich')).toBe('DE');
    expect(countryFromName('Munich')).toBe('');
    expect(entryCountry(CATALOGUE[1])).toBe('DE');
    expect(entryCountry(CATALOGUE[2])).toBe('NL');
  });

  it('groups present entries; "no country" last', () => {
    const groups = groupByCountry(CATALOGUE);
    expect(groups.map(g => [g.code, g.entries.map(e => e.fingerprint)])).toEqual([
      ['DE', ['de-1', 'de-2']], ['NL', ['nl-1']], ['', ['x-1']],
    ]);
    expect(countryName('')).toBe('Без страны');
    expect(countryName('DE')).toMatch(/DE$/);
  });
});

describe('selection → builder rules', () => {
  const [de, nl, none] = groupByCountry(CATALOGUE);

  it('a whole country is one country rule; unticking a node splits it', () => {
    let rules = toggleCountry([], 1, de);
    expect(rules).toEqual([{ kind: 'country', source_id: 1, country_code: 'DE', fingerprint: '', original_name: '' }]);
    expect(countrySelection(rules, 1, de)).toEqual({ state: 'country', nodes: 2 });
    expect(nodeSelected(rules, 1, de, de.entries[1])).toBe(true);
    rules = toggleNode(rules, 1, de, de.entries[0]);
    expect(rules).toEqual([{ kind: 'node', source_id: 1, country_code: '', fingerprint: 'de-2', original_name: '🇩🇪 Munich' }]);
    expect(countrySelection(rules, 1, de)).toEqual({ state: 'nodes', nodes: 1 });
    // Ticking the country again replaces its node rules.
    expect(toggleCountry(rules, 1, de)).toEqual([]);
    expect(toggleCountry([], 1, de)).toHaveLength(1);
  });

  it('a group without a country selects all its nodes as node rules', () => {
    const rules = toggleCountry([], 1, none);
    expect(rules).toEqual([{ kind: 'node', source_id: 1, country_code: '', fingerprint: 'x-1', original_name: 'Unknown' }]);
    expect(toggleCountry(rules, 1, none)).toEqual([]);
  });

  it('keeps order, moves rules and drops rules of removed sources', () => {
    const rules = toggleNode(toggleCountry([], 1, nl), 1, de, de.entries[0]);
    expect(rules.map(r => r.country_code || r.fingerprint)).toEqual(['NL', 'de-1']);
    expect(moveRule(rules, 1, -1).map(r => r.country_code || r.fingerprint)).toEqual(['de-1', 'NL']);
    expect(rulesForSources([...rules, { ...rules[0], source_id: 2 }], [1])).toHaveLength(2);
  });

  it('reads builder items like database.compositionOf', () => {
    const c = compositionFromBuilder(3, [1], [
      { id: 2, kind: 'node', source_id: 1, country_code: '', fingerprint: 'nl-1', original_name: 'Amsterdam', custom_name: null, description: '', position: 1, enabled: true },
      { id: 1, kind: 'country', source_id: 1, country_code: 'de', fingerprint: '', original_name: '', custom_name: null, description: '', position: 0, enabled: true },
      { id: 3, kind: 'country', source_id: 1, country_code: 'FI', fingerprint: '', original_name: '', custom_name: null, description: '', position: 2, enabled: false },
    ]);
    expect(c.mode).toBe('selected');
    expect(c.rules.map(r => r.country_code || r.fingerprint)).toEqual(['DE', 'nl-1']);
    expect(compositionFromBuilder(1, [1], []).mode).toBe('all');
  });

  it('explains server problems', () => {
    expect(problemText({ code: 'builder_shared' })).toMatch(/копию/);
    expect(problemText({ code: 'empty_result' })).toMatch(/пуст/);
    expect(problemText({ code: 'something_new' })).toMatch(/something_new/);
  });
});

describe('trial API client', () => {
  const json = (data: unknown, status = 200) =>
    new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
  const setup = () => {
    const transport = vi.fn<typeof fetch>();
    return { transport, api: new AdminApi(transport) };
  };

  it('parses the view and rejects a malformed one', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json(view()));
    await expect(api.getTrial()).resolves.toMatchObject({ builder_id: 9, settings: { duration_hours: 72 }, legacy_nodes: [{ id: 3, linked: true }, {}, {}] });
    transport.mockResolvedValueOnce(json({ ...view(), settings: { ...view().settings, features: null } }));
    await expect(api.getTrial()).rejects.toMatchObject({ code: 'invalid_response' });
    // The issuance node list is part of the contract.
    transport.mockResolvedValueOnce(json({ ...view(), legacy_nodes: undefined }));
    await expect(api.getTrial()).rejects.toMatchObject({ code: 'invalid_response' });
    transport.mockResolvedValueOnce(json({ ...view(), legacy_nodes: [{ id: 1, name: 'x', type: 'fetch', is_active: 'yes', linked: false }] }));
    await expect(api.getTrial()).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('sends the save body web.trialUpdateBody accepts', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ trial: view(), target_id: 1, replayed: false, audit: {} }));
    const draft = {
      enabled: true, duration_hours: 6, rate_limit_per_hour: 3, title: '', description: '', features: [], badge: '',
      builder_id: null, composition: null, legacy_node_ids: [1, 3],
    };
    await api.saveTrial(draft, 2, 'ui-key');
    const [path, init] = transport.mock.calls[0];
    expect(path).toBe('/admin/api/trial');
    expect(init?.method).toBe('PUT');
    expect(JSON.parse(String(init?.body))).toEqual({ request_key: 'ui-key', version: 2, ...draft });
  });

  it('reports field and reason of a rejection', async () => {
    const { transport, api } = setup();
    transport.mockResolvedValueOnce(json({ error: 'invalid_trial', field: 'composition.rules', reason: 'country_not_found' }, 400));
    const error = await api.saveTrial({
      enabled: true, duration_hours: 6, rate_limit_per_hour: 3, title: '', description: '', features: [], badge: '', builder_id: 1, composition: null,
      legacy_node_ids: null,
    }, 0, 'k').catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ code: 'invalid_trial', status: 400, field: 'composition.rules', reason: 'country_not_found' });
  });
});
