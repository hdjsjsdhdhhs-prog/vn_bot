// Exercises the real Go-rendered /connect page (golden fixtures produced by
// TestConnectionPage_ConnectGolden) and its inline script in jsdom.
import { afterEach, describe, expect, it } from 'vitest';
import activeHTML from '../../internal/web/testdata/connect_active.golden.html?raw';
import expiredHTML from '../../internal/web/testdata/connect_expired.golden.html?raw';
import { appsFor, catalog, connectPageLink, platforms } from '../src/vpnapps';

const token = 'a'.repeat(64);
const sub = `https://customer.example/sub/${token}`;
const connection = `https://customer.example/connect/${token}`;
const UA = {
  iphone: ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)', 'iPhone', 5],
  android: ['Mozilla/5.0 (Linux; Android 14; Pixel 8)', 'Linux armv8l', 5],
  windows: ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Win32', 0],
  unknown: ['', '', 0],
} as const;

function load(html: string, hash = '', ua: keyof typeof UA = 'unknown') {
  const parsed = new DOMParser().parseFromString(html, 'text/html');
  const script = parsed.querySelector('script')?.textContent ?? '';
  parsed.querySelectorAll('script').forEach(node => node.remove());
  document.body.innerHTML = parsed.body.innerHTML;
  history.replaceState(null, '', `/connect/${token}${hash}`);
  const [agent, platform, touch] = UA[ua];
  Object.defineProperty(navigator, 'userAgent', { value: agent, configurable: true });
  Object.defineProperty(navigator, 'platform', { value: platform, configurable: true });
  Object.defineProperty(navigator, 'maxTouchPoints', { value: touch, configurable: true });
  new Function(script)();
  return script;
}
const checked = (selector: string) => Array.from(document.querySelectorAll<HTMLElement>(selector)).filter(n => n.getAttribute('aria-checked') === 'true').map(n => n.dataset.platform ?? n.dataset.app);
const visibleApps = () => Array.from(document.querySelectorAll<HTMLElement>('[data-app]')).filter(n => !n.hidden).map(n => n.dataset.app);
const visiblePanels = () => Array.from(document.querySelectorAll<HTMLElement>('[data-panel-app]')).filter(n => !n.hidden);
const panel = () => { const list = visiblePanels(); expect(list).toHaveLength(1); return list[0]; };
const click = (selector: string) => document.querySelector<HTMLElement>(selector)!.click();

afterEach(() => { document.body.innerHTML = ''; });

describe('/connect platform and app picker', () => {
  it('renders status, QR and copy fallback; script carries no URL or scheme', () => {
    const script = load(activeHTML);
    expect(document.querySelector('.status.active')?.textContent).toBe('Активна');
    expect(document.body.textContent).toContain('02.01.2030 03:04 UTC');
    expect(document.querySelector<HTMLImageElement>('img.qr')?.src).toMatch(/^data:image\/png;base64,/);
    expect(document.querySelector<HTMLInputElement>('#subscription-url')?.value).toBe(sub);
    for (const forbidden of ['https://', 'http://', 'happ://', 'incy://', 'v2raytun://', token, 'localStorage', 'fetch(']) expect(script).not.toContain(forbidden);
  });

  it.each(platforms.map(p => p.id))('selecting %s shows only its apps and keeps one panel', id => {
    load(activeHTML);
    click(`[data-platform="${id}"]`);
    expect(checked('[data-platform]')).toEqual([id]);
    expect(visibleApps()).toEqual(appsFor(id).map(a => a.id));
    for (const app of appsFor(id)) {
      click(`[data-app="${app.id}"]`);
      expect(checked('[data-app]')).toEqual([app.id]);
      const current = panel();
      expect(current.dataset.panelPlatform).toBe(id);
      expect(current.dataset.panelApp).toBe(app.id);
      const add = current.querySelector<HTMLAnchorElement>('a[data-import]')!;
      // Exact deep link per developer docs: prefix + raw subscription URL.
      expect(add.getAttribute('href')).toBe(app.import_prefix + sub);
      expect(add.textContent).toBe(`Добавить подписку в ${app.name}`);
      const downloads = Array.from(current.querySelectorAll<HTMLAnchorElement>('.buttons a')).map(a => [a.getAttribute('href'), a.target, a.rel]);
      expect(downloads).toEqual(app.platforms[id]!.downloads.map(d => [d.url, '_blank', 'noopener noreferrer']));
    }
  });

  it('switching platform moves an unavailable app to the first available one', () => {
    load(activeHTML);
    click('[data-platform="android"]'); click('[data-app="v2raytun"]');
    click('[data-platform="linux"]');
    expect(checked('[data-app]')).toEqual(['happ']);
    expect(panel().dataset.panelApp).toBe('happ');
    click('[data-platform="ios"]');
    expect(checked('[data-app]')).toEqual(['happ']);
  });

  it('preselects os/app from the Mini App fragment for every supported pair', () => {
    for (const platform of platforms) for (const app of appsFor(platform.id)) {
      const hash = new URL(connectPageLink(connection, { platform: platform.id, app: app.id })).hash;
      load(activeHTML, hash, 'windows');
      expect(checked('[data-platform]')).toEqual([platform.id]);
      expect(checked('[data-app]')).toEqual([app.id]);
      expect(panel().querySelector('a[data-import]')?.getAttribute('href')).toBe(app.import_prefix + sub);
      expect(document.getElementById('os-hint')?.hidden).toBe(true);
    }
  });

  it('ignores unknown fragment values and falls back to detection', () => {
    load(activeHTML, '#os=tv&app=evil', 'android');
    expect(checked('[data-platform]')).toEqual(['android']);
    expect(checked('[data-app]')).toEqual(['happ']);
    expect(document.getElementById('os-hint')?.hidden).toBe(false);
    load(activeHTML, '#os=windows&app=incy');
    expect(checked('[data-platform]')).toEqual(['windows']);
    expect(checked('[data-app]')).toEqual(['happ']);
    load(activeHTML, '#%%%');
    expect(checked('[data-platform]')).toEqual(['ios']);
  });

  it('auto-detects the platform only as an initial, switchable recommendation', () => {
    load(activeHTML, '', 'iphone');
    expect(checked('[data-platform]')).toEqual(['ios']);
    load(activeHTML, '', 'windows');
    expect(checked('[data-platform]')).toEqual(['windows']);
    expect(visibleApps()).toEqual(['happ']);
    click('[data-platform="android"]');
    expect(checked('[data-platform]')).toEqual(['android']);
    expect(document.getElementById('os-hint')?.hidden).toBe(true);
  });

  it('import tap never claims success and keeps fallbacks visible', () => {
    load(activeHTML, '#os=android&app=incy');
    const add = panel().querySelector<HTMLAnchorElement>('a[data-import]')!;
    // jsdom cannot navigate to custom schemes; stop it after our handler ran.
    add.addEventListener('click', event => event.preventDefault());
    add.click();
    const status = panel().querySelector('.import-status')!.textContent ?? '';
    expect(status).toContain('Открываем Инси (Incy)');
    expect(status).toContain('QR-код');
    expect(status).not.toMatch(/добавлена|успешно/i);
    expect(document.querySelector('img.qr')).not.toBeNull();
    expect(document.getElementById('copy-url')).not.toBeNull();
    click('[data-platform="ios"]');
    expect(panel().querySelector('.import-status')!.textContent).toBe('');
  });

  it('desktop Happ shows the best-effort notice, mobile does not', () => {
    load(activeHTML, '#os=windows&app=happ');
    expect(panel().textContent).toContain('зависит от версии приложения');
    load(activeHTML, '#os=android&app=happ');
    expect(panel().textContent).not.toContain('зависит от версии приложения');
  });

  it('arrow keys move between platforms like a radio group', () => {
    load(activeHTML, '#os=ios&app=happ');
    document.querySelector<HTMLElement>('[data-platform="ios"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    expect(checked('[data-platform]')).toEqual(['android']);
    expect(document.activeElement?.getAttribute('data-platform')).toBe('android');
    expect(document.querySelector<HTMLElement>('[data-platform="android"]')!.tabIndex).toBe(0);
    expect(document.querySelector<HTMLElement>('[data-platform="ios"]')!.tabIndex).toBe(-1);
  });

  it('copy fallback still copies exactly the subscription URL', async () => {
    load(activeHTML);
    let copied = '';
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true });
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: (v: string) => { copied = v; return Promise.resolve(); } }, configurable: true });
    click('#copy-url');
    await Promise.resolve();
    expect(copied).toBe(sub);
    expect(document.getElementById('copy-status')?.textContent).toBe('Ссылка скопирована.');
  });

  it('expired subscription: status and QR kept, no picker and no deep links', () => {
    load(expiredHTML, '#os=android&app=happ');
    expect(document.querySelector('.status')?.textContent).toBe('Истекла');
    expect(document.querySelector('.status.active')).toBeNull();
    expect(document.getElementById('connect-picker')).toBeNull();
    expect(document.body.innerHTML).not.toMatch(/(happ|incy|v2raytun):\/\//);
    expect(document.querySelector('img.qr')).not.toBeNull();
    expect(document.querySelector<HTMLInputElement>('#subscription-url')?.value).toBe(sub);
  });

  it('catalog JSON and the rendered page agree on every app name', () => {
    load(activeHTML);
    expect(Array.from(document.querySelectorAll('[data-app] .app-name')).map(n => n.textContent)).toEqual(catalog.apps.map(a => a.name));
  });
});
