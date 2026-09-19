export type InvoiceResult = 'paid' | 'cancelled' | 'failed' | 'pending';
export interface WebApp {
  initData: string;
  initDataUnsafe?: { user?: { first_name?: string; last_name?: string; username?: string } };
  colorScheme: 'light' | 'dark';
  themeParams: { bg_color?: string; text_color?: string; secondary_bg_color?: string; hint_color?: string; button_color?: string };
  safeAreaInset?: { top: number; bottom: number; left?: number; right?: number };
  contentSafeAreaInset?: { top: number; bottom: number; left?: number; right?: number };
  ready(): void;
  expand(): void;
  close(): void;
  isVersionAtLeast(version: string): boolean;
  openInvoice(url: string, callback: (status: InvoiceResult) => void): void;
  openLink(url: string): void;
  onEvent(name: string, fn: () => void): void;
  offEvent(name: string, fn: () => void): void;
  BackButton: { show(): void; hide(): void; onClick(fn: () => void): void; offClick(fn: () => void): void };
}
declare global { interface Window { Telegram?: { WebApp?: WebApp } } }

export function initializeTelegram(app?: WebApp): () => void {
  if (!app) return () => {};
  const update = () => {
    const root = document.documentElement;
    root.dataset.theme = app.colorScheme;
    const theme = app.themeParams;
    for (const [name, value] of Object.entries({ bg: theme.bg_color, text: theme.text_color, surface: theme.secondary_bg_color })) {
      if (value && /^#[0-9a-f]{6}$/i.test(value)) root.style.setProperty(`--${name}`, value);
      else root.style.removeProperty(`--${name}`);
    }
    for (const side of ['top', 'bottom', 'left', 'right'] as const) {
      root.style.setProperty(`--safe-${side}`, `${Math.max(0, app.safeAreaInset?.[side] ?? 0) + Math.max(0, app.contentSafeAreaInset?.[side] ?? 0)}px`);
    }
  };
  update();
  app.onEvent('themeChanged', update);
  app.onEvent('safeAreaChanged', update);
  app.onEvent('contentSafeAreaChanged', update);
  app.ready();
  app.expand();
  return () => {
    for (const event of ['themeChanged', 'safeAreaChanged', 'contentSafeAreaChanged']) app.offEvent(event, update);
  };
}

export function safeHTTPS(raw: string): string {
  const url = new URL(raw);
  if (url.protocol !== 'https:' || url.username || url.password) throw new Error('Unsafe link');
  return url.href;
}
export function safeInvoice(raw: string): string {
  const url = new URL(safeHTTPS(raw));
  if (url.hostname !== 't.me' || url.port || !/^\/(\$|invoice\/)[\w-]+$/.test(url.pathname) || url.search || url.hash) throw new Error('Invalid invoice');
  return url.href;
}
