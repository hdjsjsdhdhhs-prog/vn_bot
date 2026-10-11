// Shared VPN client catalog. The JSON is the single source of truth for both
// /connect/{token} (embedded and rendered by Go) and this Mini App, so names,
// platforms and official download links cannot drift. See doc/vpn-apps.md.
import catalogJSON from '../../internal/web/templates/vpn_apps.json';
import { safeHTTPS } from './telegram';

export type PlatformId = 'ios' | 'android' | 'windows' | 'linux' | 'macos';
export interface Download { label: string; url: string }
export interface AppPlatform { import: 'direct' | 'best_effort'; downloads: Download[] }
export interface VpnApp {
  id: string; name: string; description: string; import_prefix: string;
  enable_steps: string[]; platforms: Partial<Record<PlatformId, AppPlatform>>;
}
export interface Platform { id: PlatformId; label: string }
export interface Choice { platform: PlatformId; app: string }

interface Catalog { platforms: Platform[]; apps: VpnApp[] }
export const catalog = catalogJSON as unknown as Catalog;
export const platforms: readonly Platform[] = catalog.platforms;

export function isPlatform(value: unknown): value is PlatformId {
  return typeof value === 'string' && platforms.some(p => p.id === value);
}
export function appsFor(platform: PlatformId): VpnApp[] {
  return catalog.apps.filter(app => app.platforms[platform]);
}
export function appById(id: string): VpnApp | undefined {
  return catalog.apps.find(app => app.id === id);
}
export function platformLabel(platform: PlatformId): string {
  return platforms.find(p => p.id === platform)?.label ?? platform;
}
/** Keeps the app when available on the new platform, otherwise picks its first app. */
export function choose(platform: PlatformId, app?: string): Choice {
  const available = appsFor(platform);
  const picked = available.find(item => item.id === app) ?? available[0];
  return { platform, app: picked.id };
}
export function downloadsFor(choice: Choice): Download[] {
  return appById(choice.app)?.platforms[choice.platform]?.downloads ?? [];
}

/**
 * Best-effort initial recommendation only: the user can always switch.
 * Telegram's own platform wins over the user agent when it is specific.
 */
export function detectPlatform(userAgent = '', telegramPlatform = '', touchPoints = 0, navPlatform = ''): PlatformId {
  const tg = telegramPlatform.toLowerCase();
  if (tg === 'ios') return 'ios';
  if (tg.startsWith('android')) return 'android';
  if (tg === 'macos') return 'macos';
  const both = `${navPlatform} ${userAgent}`;
  if (/iPhone|iPad|iPod/i.test(userAgent) || (/Mac/i.test(both) && touchPoints > 1)) return 'ios';
  if (/Android/i.test(both)) return 'android';
  if (/Win/i.test(both)) return 'windows';
  if (/Mac/i.test(both)) return 'macos';
  if (/Linux|X11|CrOS/i.test(both)) return 'linux';
  return 'android';
}

// Only the two public identifiers are persisted: never the subscription or
// connection URL, which are bearer credentials.
const KEY = 'dictatorvpn.vpn-client';
export function loadChoice(storage?: Pick<Storage, 'getItem'>): Choice | null {
  try {
    const raw: unknown = JSON.parse(storage?.getItem(KEY) ?? 'null');
    if (typeof raw !== 'object' || raw === null) return null;
    const { platform, app } = raw as Record<string, unknown>;
    if (!isPlatform(platform) || typeof app !== 'string' || !appById(app)?.platforms[platform]) return null;
    return { platform, app };
  } catch { return null; }
}
export function saveChoice(choice: Choice, storage?: Pick<Storage, 'setItem'>): void {
  try { storage?.setItem(KEY, JSON.stringify({ platform: choice.platform, app: choice.app })); }
  catch { /* Telegram privacy modes may disable storage: keep the in-memory choice. */ }
}

/**
 * The external https connection page of THIS subscription, preselecting the
 * choice through the URL fragment (never sent to the server). The page then
 * launches the app from a real user tap; the Mini App WebView never opens
 * custom schemes itself.
 */
export function connectPageLink(connectionURL: string, choice: Choice): string {
  const url = new URL(safeHTTPS(connectionURL));
  if (!/^\/connect\/[a-f0-9]{64}$/.test(url.pathname) || url.search) throw new Error('Unexpected connection URL');
  if (!appById(choice.app)?.platforms[choice.platform]) throw new Error('Unsupported choice');
  url.hash = new URLSearchParams({ os: choice.platform, app: choice.app }).toString();
  return url.href;
}
