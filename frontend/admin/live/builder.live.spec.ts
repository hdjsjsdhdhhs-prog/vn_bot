import { test, expect } from '@playwright/test';
import type { Locator, Page } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Builder configuration path in the real admin UI against the real Go API and
// a temporary SQLite database (internal/web/admin_live_smoke_test.go). Nothing
// here is mocked: auth, CSRF, the atomic configuration save, Preview and /sub
// are the production code paths.

const STATE_DIR = process.env.ADMIN_LIVE_STATE_DIR ?? '';
const PASSWORD = process.env.ADMIN_LIVE_PASSWORD ?? '';

interface State { source_a: number; source_b: number; other_builder: number; other_rule: number; subscription_id: number; sub_token: string }
interface StoredItem { id: number; kind: string; source_id: number; country_code?: string; fingerprint?: string; original_name?: string; enabled: boolean }
interface StoredBuilder { id: number; name: string; version: number; sources: { source_id: number }[]; items: StoredItem[] }

const state = (): State => JSON.parse(readFileSync(join(STATE_DIR, 'state.json'), 'utf8')) as State;

async function login(page: Page) {
  await page.goto('/admin-ui/');
  await page.getByLabel('Логин').fill('admin');
  await page.getByLabel('Пароль', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.locator('.auth-screen')).toHaveCount(0);
}

/** Same-origin API call with the session cookie and CSRF token, like the UI. */
async function api(page: Page, method: string, path: string, body?: unknown): Promise<{ status: number; json: unknown }> {
  return page.evaluate(async ({ method, path, body }) => {
    const session = await (await fetch('/admin/session')).json() as { csrf_token: string };
    const resp = await fetch(path, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf_token },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await resp.text();
    return { status: resp.status, json: text ? JSON.parse(text) : null };
  }, { method, path, body });
}

const stored = async (page: Page, id: number) => (await api(page, 'GET', `/admin/api/builders/${id}`)).json as StoredBuilder;

async function addCountry(page: Page, dialog: Locator, sourceId: number, code: string) {
  await dialog.getByRole('button', { name: 'Добавить правило' }).click();
  const rule = page.getByRole('dialog', { name: 'Новое правило' });
  await rule.getByLabel('Источник').selectOption(String(sourceId));
  await rule.getByLabel('Страна', { exact: true }).selectOption(code);
  await rule.getByRole('button', { name: 'Применить' }).click();
  await expect(rule).toBeHidden();
}

async function addNode(page: Page, dialog: Locator, sourceId: number, name: string) {
  await dialog.getByRole('button', { name: 'Добавить правило' }).click();
  const rule = page.getByRole('dialog', { name: 'Новое правило' });
  await rule.getByLabel('Тип правила').selectOption('node');
  await rule.getByLabel('Источник').selectOption(String(sourceId));
  await rule.getByLabel('Поиск сервера по имени').fill(name);
  const picker = rule.getByRole('list', { name: 'Сервер' });
  await expect(picker.locator('.entry-pick-item')).toHaveCount(1);
  await picker.locator('.entry-pick-item').first().click();
  await rule.getByRole('button', { name: 'Применить' }).click();
  await expect(rule).toBeHidden();
}

const card = (page: Page) => page.locator('.builder-item', { hasText: 'Smoke builder' });

async function openSaved(page: Page) {
  await card(page).getByRole('button', { name: 'Редактировать' }).click();
  const dialog = page.getByRole('dialog', { name: 'Построитель: Smoke builder' });
  await expect(dialog).toBeVisible();
  return dialog;
}

test.afterAll(() => { if (STATE_DIR) writeFileSync(join(STATE_DIR, 'stop'), 'done'); });

test('builder configuration through the real API: save, reopen, reload, preview, /sub, ownership, stale tab', async ({ page, context }) => {
  test.skip(!STATE_DIR || !PASSWORD, 'ADMIN_LIVE_STATE_DIR and ADMIN_LIVE_PASSWORD are required');
  const s = state();
  await login(page);

  // 1–6. Create, two synced sources, two countries of A and one server of B.
  await page.goto('/admin-ui/#/builders');
  await page.getByRole('button', { name: 'Новый построитель' }).click();
  let dialog = page.getByRole('dialog', { name: 'Новый построитель' });
  await dialog.getByLabel('Название').fill('Smoke builder');
  await dialog.getByLabel('Описание').fill('Германия, Нидерланды и Даллас');
  await dialog.getByRole('button', { name: 'Добавить «Smoke A»' }).click();
  await dialog.getByRole('button', { name: 'Добавить «Smoke B»' }).click();
  await expect(dialog.locator('.sources-total')).toContainText('6 серверов');
  await addCountry(page, dialog, s.source_a, 'DE');
  await addCountry(page, dialog, s.source_a, 'NL');
  await addNode(page, dialog, s.source_b, 'Dallas');
  await dialog.getByRole('button', { name: 'Создать' }).click();
  // Once created, the editor is titled after the saved builder.
  dialog = page.getByRole('dialog', { name: 'Построитель: Smoke builder' });
  await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
  await expect(card(page).locator('.builder-meta')).toHaveText(/^v2 · 2 источника · 3 правила$/);
  await expect(card(page).locator('.builder-composition')).toHaveText('Страны: 🇩🇪 DE, 🇳🇱 NL · отдельных серверов: 1');
  const builderId = Number((await page.evaluate(async () => {
    const list = await (await fetch('/admin/api/builders')).json() as { builders: { id: number; name: string }[] };
    return list.builders.find(b => b.name === 'Smoke builder')?.id ?? 0;
  })));
  expect(builderId).toBeGreaterThan(0);
  const first = await stored(page, builderId);
  expect(first.sources.map(x => x.source_id)).toEqual([s.source_a, s.source_b]);
  expect(first.items.map(i => [i.kind, i.source_id, i.country_code ?? '', i.original_name ?? ''])).toEqual([
    ['country', s.source_a, 'DE', ''], ['country', s.source_a, 'NL', ''], ['node', s.source_b, '', '🇺🇸 Dallas'],
  ]);

  // 7. Close and reopen.
  await dialog.getByRole('button', { name: 'Закрыть' }).click();
  dialog = await openSaved(page);
  await expect(dialog.getByRole('listitem', { name: '🇩🇪 DE' })).toBeVisible();
  await expect(dialog.getByRole('listitem', { name: '🇳🇱 NL' })).toBeVisible();
  await expect(dialog.locator('.source-pick-selected')).toHaveCount(2);
  await dialog.getByRole('button', { name: 'Закрыть' }).click();

  // 8–9. Full reload, reopen, save without changes: same rules, version + 1.
  await page.reload();
  await expect(card(page).locator('.builder-meta')).toHaveText(/^v2 · 2 источника · 3 правила$/);
  dialog = await openSaved(page);
  await expect(dialog.getByRole('listitem', { name: '🇩🇪 DE' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Сохранить' }).click();
  await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
  const resaved = await stored(page, builderId);
  expect(resaved.version).toBe(first.version + 1);
  expect(resaved.items).toEqual(first.items);
  expect(resaved.sources).toEqual(first.sources);

  // 10. Preview and the real /sub of a subscription using the builder.
  await dialog.getByRole('button', { name: 'Запустить предпросмотр' }).click();
  await expect(dialog.locator('.preview-body .preview-stats')).toHaveText('В подписке: 4 сервера · 3 страны');
  const assigned = await api(page, 'POST', `/admin/api/subscriptions/${s.subscription_id}/builder`, { request_key: 'live-assign-1', builder_id: builderId });
  expect(assigned.status).toBe(200);
  const sub = await page.request.get(`/sub/${s.sub_token}`);
  expect(sub.status()).toBe(200);
  const names = Buffer.from(await sub.text(), 'base64').toString('utf8').split('\n')
    .map(l => decodeURIComponent(l.slice(l.indexOf('#') + 1)));
  expect(names).toEqual(['🇩🇪 Frankfurt', '🇩🇪 Berlin', '🇳🇱 Amsterdam', '🇺🇸 Dallas']);
  await dialog.getByRole('button', { name: 'Закрыть' }).click();

  // 11. A rule of another builder: refused by every rule endpoint, unchanged.
  const otherBefore = await stored(page, s.other_builder);
  const mineBefore = await stored(page, builderId);
  const refused = [
    await api(page, 'DELETE', `/admin/api/builders/${builderId}/items/${s.other_rule}`),
    await api(page, 'POST', `/admin/api/builders/${builderId}/items`, { id: s.other_rule, kind: 'country', source_id: s.source_a, country_code: 'FI', position: 0, enabled: true }),
    await api(page, 'POST', `/admin/api/builders/${builderId}/reorder`, { item_ids: [s.other_rule] }),
    await api(page, 'PUT', `/admin/api/builders/${builderId}/configuration`, {
      request_key: 'live-steal', version: mineBefore.version, name: mineBefore.name, description: '', enabled: true, profile_title: '', support_url: '', announce: '',
      source_ids: [s.source_a], items: [{ id: s.other_rule, kind: 'country', source_id: s.source_a, country_code: 'FI', fingerprint: '', original_name: '', custom_name: null, description: '', enabled: true }],
    }),
  ];
  expect(refused).toEqual(refused.map(() => ({ status: 404, json: { error: 'not_found' } })));
  expect(await stored(page, s.other_builder)).toEqual(otherBefore);
  expect(await stored(page, builderId)).toEqual(mineBefore);

  // 12. Two tabs load the same version; the later save is refused with 409.
  const tab2 = await context.newPage();
  await tab2.goto('/admin-ui/#/builders');
  const dialog2 = await openSaved(tab2);
  await expect(dialog2.getByRole('listitem', { name: '🇩🇪 DE' })).toBeVisible();

  dialog = await openSaved(page);
  page.once('dialog', d => void d.accept());
  await dialog.getByRole('button', { name: 'Удалить правило «🇳🇱 NL»' }).click();
  await dialog.getByRole('button', { name: 'Сохранить' }).click();
  await expect(dialog.getByText('Изменения сохранены.')).toBeVisible();
  const afterTab1 = await stored(page, builderId);
  expect(afterTab1.items.map(i => i.kind === 'node' ? i.original_name : i.country_code)).toEqual(['DE', '🇺🇸 Dallas']);
  expect(afterTab1.version).toBe(resaved.version + 1);

  await addCountry(tab2, dialog2, s.source_a, 'FI');
  await dialog2.getByRole('button', { name: 'Сохранить' }).click();
  await expect(dialog2.locator('.modal-status')).toContainText('Построитель изменён в другой вкладке или другим администратором');
  expect(await stored(page, builderId)).toEqual(afterTab1);
  const subAfter = Buffer.from(await (await page.request.get(`/sub/${s.sub_token}`)).text(), 'base64').toString('utf8').split('\n')
    .map(l => decodeURIComponent(l.slice(l.indexOf('#') + 1)));
  expect(subAfter).toEqual(['🇩🇪 Frankfurt', '🇩🇪 Berlin', '🇺🇸 Dallas']);
});
