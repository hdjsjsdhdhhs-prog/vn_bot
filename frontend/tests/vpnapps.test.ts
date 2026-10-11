import { describe, expect, it } from 'vitest';
import type { PlatformId } from '../src/vpnapps';
import { appsFor, catalog, choose, connectPageLink, detectPlatform, downloadsFor, loadChoice, platforms, saveChoice } from '../src/vpnapps';

const token = 'a'.repeat(64);
const other = 'b'.repeat(64);
const connection = `https://customer.example/connect/${token}`;

// Approved matrix; mirrors wantVPNMatrix in internal/web/vpnapps_test.go.
const matrix: Record<PlatformId, string[]> = {
  ios: ['happ', 'incy', 'v2raytun'], android: ['happ', 'incy', 'v2raytun'],
  windows: ['happ'], linux: ['happ'], macos: ['happ'],
};

class MemoryStorage {
  data = new Map<string, string>();
  getItem(key: string) { return this.data.get(key) ?? null; }
  setItem(key: string, value: string) { this.data.set(key, value); }
}

describe('shared VPN catalog', () => {
  it('offers all five platforms in order', () => {
    expect(platforms.map(p => p.id)).toEqual(['ios', 'android', 'windows', 'linux', 'macos']);
    expect(platforms.map(p => p.label)).toEqual(['iOS', 'Android', 'Windows', 'Linux', 'macOS']);
  });

  it('lists exactly the approved apps for each platform', () => {
    for (const platform of platforms) expect(appsFor(platform.id).map(a => a.id)).toEqual(matrix[platform.id]);
    expect(catalog.apps.map(a => a.name)).toEqual(['Happ', 'Инси (Incy)', 'V2RayTun']);
  });

  it('switches the app when the new platform does not offer it', () => {
    expect(choose('android', 'v2raytun')).toEqual({ platform: 'android', app: 'v2raytun' });
    expect(choose('windows', 'v2raytun')).toEqual({ platform: 'windows', app: 'happ' });
    expect(choose('linux', 'incy')).toEqual({ platform: 'linux', app: 'happ' });
    expect(choose('ios', 'unknown')).toEqual({ platform: 'ios', app: 'happ' });
    expect(choose('macos')).toEqual({ platform: 'macos', app: 'happ' });
  });

  it('uses only official https download links', () => {
    expect(downloadsFor({ platform: 'ios', app: 'happ' }).map(d => d.url)).toEqual(['https://apps.apple.com/us/app/happ-proxy-utility/id6504287215', 'https://happ.info/']);
    expect(downloadsFor({ platform: 'android', app: 'incy' })[0].url).toBe('https://play.google.com/store/apps/details?id=llc.itdev.incy');
    expect(downloadsFor({ platform: 'android', app: 'v2raytun' })[0].url).toBe('https://play.google.com/store/apps/details?id=com.v2raytun.android');
    expect(downloadsFor({ platform: 'windows', app: 'happ' })[0].url).toBe('https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe');
    const hosts = new Set(['apps.apple.com', 'play.google.com', 'github.com', 'happ.info', 'incy.cc', 'v2raytun.com']);
    for (const app of catalog.apps) for (const platform of Object.values(app.platforms)) for (const d of platform!.downloads) {
      const url = new URL(d.url);
      expect(url.protocol).toBe('https:');
      expect(hosts.has(url.hostname)).toBe(true);
    }
  });

  it('documented import prefixes, desktop Happ marked best-effort', () => {
    expect(Object.fromEntries(catalog.apps.map(a => [a.id, a.import_prefix]))).toEqual({ happ: 'happ://add/', incy: 'incy://add/', v2raytun: 'v2raytun://import/' });
    for (const platform of ['windows', 'linux', 'macos'] as const) expect(catalog.apps[0].platforms[platform]?.import).toBe('best_effort');
    for (const app of catalog.apps) for (const platform of ['ios', 'android'] as const) expect(app.platforms[platform]?.import).toBe('direct');
  });
});

describe('platform detection (initial recommendation only)', () => {
  it.each([
    ['', 'ios', 0, '', 'ios'],
    ['', 'android_x', 0, '', 'android'],
    ['', 'macos', 0, '', 'macos'],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)', 'unknown', 5, 'iPhone', 'ios'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', '', 5, 'MacIntel', 'ios'],
    ['Mozilla/5.0 (Linux; Android 14; Pixel 8)', 'weba', 5, 'Linux armv8l', 'android'],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'tdesktop', 0, 'Win32', 'windows'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', 'tdesktop', 0, 'MacIntel', 'macos'],
    ['Mozilla/5.0 (X11; Linux x86_64)', 'tdesktop', 0, 'Linux x86_64', 'linux'],
    ['', '', 0, '', 'android'],
  ])('%s / %s → %s', (ua, tg, touch, nav, want) => {
    expect(detectPlatform(ua, tg, touch, nav)).toBe(want);
  });
});

describe('choice persistence', () => {
  it('stores only platform and app identifiers', () => {
    const storage = new MemoryStorage();
    saveChoice({ platform: 'android', app: 'incy' }, storage);
    expect([...storage.data.values()]).toEqual(['{"platform":"android","app":"incy"}']);
    expect([...storage.data.values()].join()).not.toContain('https://');
    expect(loadChoice(storage)).toEqual({ platform: 'android', app: 'incy' });
  });

  it('rejects tampered or unsupported stored values', () => {
    for (const raw of ['{', 'null', '[]', '{"platform":"tv","app":"happ"}', '{"platform":"windows","app":"incy"}', '{"platform":"ios","app":1}']) {
      const storage = new MemoryStorage(); storage.data.set('dictatorvpn.vpn-client', raw);
      expect(loadChoice(storage)).toBeNull();
    }
    expect(loadChoice(undefined)).toBeNull();
    const broken = { getItem: () => { throw new Error('denied'); }, setItem: () => { throw new Error('denied'); } };
    expect(loadChoice(broken)).toBeNull();
    expect(() => saveChoice({ platform: 'ios', app: 'happ' }, broken)).not.toThrow();
  });
});

describe('connection page link (variant A)', () => {
  it('keeps this subscription\'s https page and carries only os/app in the fragment', () => {
    for (const platform of platforms) for (const app of appsFor(platform.id)) {
      const link = connectPageLink(connection, { platform: platform.id, app: app.id });
      expect(link).toBe(`${connection}#os=${platform.id}&app=${app.id}`);
      const url = new URL(link);
      expect(url.search).toBe('');
      expect([...new URLSearchParams(url.hash.slice(1)).keys()]).toEqual(['os', 'app']);
      expect(link).not.toContain(other);
      expect(link).not.toContain('/sub/');
    }
    expect(connectPageLink(`https://customer.example/connect/${other}`, { platform: 'ios', app: 'happ' })).toContain(other);
  });

  it('never builds links for unsafe URLs or unsupported choices', () => {
    for (const bad of [
      `http://customer.example/connect/${token}`, `https://u:p@customer.example/connect/${token}`,
      `https://customer.example/sub/${token}`, `https://customer.example/connect/${token}?x=1`,
      'https://customer.example/connect/short', 'happ://add/https://customer.example/sub/x', 'not a url',
    ]) expect(() => connectPageLink(bad, { platform: 'ios', app: 'happ' }), bad).toThrow();
    expect(() => connectPageLink(connection, { platform: 'windows', app: 'incy' })).toThrow();
    expect(connectPageLink(`${connection}#old=1`, { platform: 'ios', app: 'happ' })).toBe(`${connection}#os=ios&app=happ`);
  });
});
