import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { readFileSync } from 'node:fs';

// Real Go template output (TestConnectionPage_ConnectGolden), served on the
// production-like origin. Native app import itself cannot be observed here:
// these tests verify the final URI, the tap wiring and the fallbacks.
const token = 'a'.repeat(64);
const sub = `https://customer.example/sub/${token}`;
const page_ = `https://customer.example/connect/${token}`;
const golden = (name: string) => readFileSync(new URL(`../../internal/web/testdata/${name}`, import.meta.url), 'utf8');
const matrix: Record<string, Array<[string, string, string]>> = {
  iOS: [['happ', 'Happ', 'happ://add/'], ['incy', 'Инси (Incy)', 'incy://add/'], ['v2raytun', 'V2RayTun', 'v2raytun://import/']],
  Android: [['happ', 'Happ', 'happ://add/'], ['incy', 'Инси (Incy)', 'incy://add/'], ['v2raytun', 'V2RayTun', 'v2raytun://import/']],
  Windows: [['happ', 'Happ', 'happ://add/']], Linux: [['happ', 'Happ', 'happ://add/']], macOS: [['happ', 'Happ', 'happ://add/']],
};

async function open(page: Page, file = 'connect_active.golden.html', hash = '') {
  const requests: string[] = [];
  await page.route('https://customer.example/**', route => {
    requests.push(route.request().url());
    if (new URL(route.request().url()).pathname === '/static/logo.png') return route.fulfill({ status: 204 });
    return route.fulfill({ body: golden(file), contentType: 'text/html; charset=utf-8' });
  });
  await page.goto(page_ + hash);
  return requests;
}
const noHorizontalScroll = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
const visiblePanel = (page: Page) => page.locator('[data-panel-app]:visible');

test('390px: status card, five-platform switch and per-app deep links', async ({ page }) => {
  const requests = await open(page);
  await expect(page.getByRole('heading', { name: 'Подключение к подписке' })).toBeVisible();
  await expect(page.locator('.status.active')).toHaveText('Активна');
  await expect(page.getByText('02.01.2030 03:04 UTC')).toBeVisible();
  const platforms = page.getByRole('radiogroup', { name: 'Выберите систему' }).getByRole('radio');
  await expect(platforms).toHaveText(['iOS', 'Android', 'Windows', 'Linux', 'macOS']);
  for (const [label, apps] of Object.entries(matrix)) {
    await page.getByRole('radio', { name: label, exact: true }).click();
    await expect(page.getByRole('radio', { name: label, exact: true })).toHaveAttribute('aria-checked', 'true');
    await expect(page.locator('[data-app]:visible .app-name')).toHaveText(apps.map(app => app[1]));
    for (const [id, name, prefix] of apps) {
      await page.locator(`[data-app="${id}"]`).click();
      await expect(visiblePanel(page)).toHaveCount(1);
      const add = page.getByRole('link', { name: `Добавить подписку в ${name}` });
      await expect(add).toBeVisible();
      await expect(add).toHaveAttribute('href', prefix + sub);
      const box = await add.boundingBox();
      expect(box!.height).toBeGreaterThanOrEqual(44);
      expect(await noHorizontalScroll(page)).toBe(true);
    }
  }
  // Only the page itself and its logo were requested: no third parties.
  expect(requests.every(url => url.startsWith('https://customer.example/'))).toBe(true);
});

test('Mini App fragment preselects Android + V2RayTun; tap keeps fallbacks without false success', async ({ page }) => {
  await open(page, 'connect_active.golden.html', '#os=android&app=v2raytun');
  await expect(page.getByRole('radio', { name: 'Android', exact: true })).toHaveAttribute('aria-checked', 'true');
  await expect(page.locator('[data-app="v2raytun"]')).toHaveAttribute('aria-checked', 'true');
  const add = page.getByRole('link', { name: 'Добавить подписку в V2RayTun' });
  await expect(add).toHaveAttribute('href', `v2raytun://import/${sub}`);
  // The browser hands custom schemes to the OS; block that hand-off in CI and
  // verify the page's own reaction to the real user tap.
  await page.evaluate(() => document.addEventListener('click', event => event.preventDefault()));
  await add.click();
  const status = visiblePanel(page).getByRole('status');
  await expect(status).toContainText('Открываем V2RayTun');
  await expect(status).not.toContainText('добавлена');
  await expect(page.getByRole('img', { name: 'QR-код ссылки подписки' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Скопировать ссылку' })).toBeVisible();
  await expect(page.getByText('Не передавайте эту страницу')).toBeVisible();
  // Install links are official and open in a new context without referrer.
  const downloads = visiblePanel(page).locator('.buttons a');
  await expect(downloads).toHaveCount(2);
  await expect(downloads.nth(0)).toHaveAttribute('href', 'https://play.google.com/store/apps/details?id=com.v2raytun.android');
  await expect(downloads.nth(0)).toHaveAttribute('rel', 'noopener noreferrer');
});

test('copy fallback copies the subscription URL', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: 'https://customer.example' });
  await open(page);
  await page.getByRole('button', { name: 'Скопировать ссылку' }).click();
  await expect(page.locator('#copy-status')).toHaveText('Ссылка скопирована.');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(sub);
});

test('keyboard: arrows move the OS radio group', async ({ page }) => {
  await open(page, 'connect_active.golden.html', '#os=ios&app=happ');
  await page.getByRole('radio', { name: 'iOS', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('radio', { name: 'Android', exact: true })).toBeFocused();
  await expect(page.getByRole('radio', { name: 'Android', exact: true })).toHaveAttribute('aria-checked', 'true');
});

test('expired subscription keeps status, QR and link, without deep links', async ({ page }) => {
  await open(page, 'connect_expired.golden.html', '#os=android&app=happ');
  await expect(page.locator('.status')).toHaveText('Истекла');
  await expect(page.getByText('Подписка сейчас неактивна')).toBeVisible();
  await expect(page.getByRole('img', { name: 'QR-код ссылки подписки' })).toBeVisible();
  await expect(page.locator('#subscription-url')).toHaveValue(sub);
  await expect(page.locator('a[href*="://add/"], a[href*="://import/"]')).toHaveCount(0);
  expect(await noHorizontalScroll(page)).toBe(true);
});

test('responsive: 320, 390, 768 and desktop 1280 without horizontal scroll', async ({ page }) => {
  await open(page);
  for (const width of [320, 390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await noHorizontalScroll(page), `width ${width}`).toBe(true);
    await expect(page.getByRole('link', { name: 'Добавить подписку в Happ' })).toBeVisible();
    await page.screenshot({ path: `test-results/connect-${width}.png`, fullPage: true });
  }
  // Desktop: status/fallback column beside the connection steps.
  const status = await page.locator('.area-status').boundingBox();
  const connect = await page.locator('.area-connect').boundingBox();
  expect(connect!.x).toBeGreaterThan(status!.x + status!.width - 1);
});
