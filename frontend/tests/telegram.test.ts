import { afterEach, describe, expect, it, vi } from 'vitest';
import { initializeTelegram, safeHTTPS, safeInvoice } from '../src/telegram';
import { telegram } from './fixtures';

afterEach(() => {
  document.documentElement.removeAttribute('style');
  delete document.documentElement.dataset.theme;
});

describe('Telegram WebView integration', () => {
  it('updates all safe-area edges and resets obsolete theme overrides', () => {
    const app = telegram();
    app.themeParams = { bg_color: '#101010' };
    app.safeAreaInset = { top: 12, bottom: 16, left: 28, right: 28 };
    app.contentSafeAreaInset = { top: 20, bottom: 4, left: 8, right: 0 };
    const cleanup = initializeTelegram(app);
    const root = document.documentElement;
    expect(root.style.getPropertyValue('--safe-top')).toBe('32px');
    expect(root.style.getPropertyValue('--safe-bottom')).toBe('20px');
    expect(root.style.getPropertyValue('--safe-left')).toBe('36px');
    expect(root.style.getPropertyValue('--safe-right')).toBe('28px');
    expect(root.style.getPropertyValue('--bg')).toBe('#101010');
    expect(app.ready).toHaveBeenCalledOnce();
    expect(app.expand).toHaveBeenCalledOnce();
    app.themeParams = {}; app.colorScheme = 'light';
    const update = vi.mocked(app.onEvent).mock.calls.find(([name]) => name === 'themeChanged')![1];
    update();
    expect(root.dataset.theme).toBe('light');
    expect(root.style.getPropertyValue('--bg')).toBe('');
    cleanup();
    expect(app.offEvent).toHaveBeenCalledTimes(3);
  });
  it.each(['javascript:alert(1)', 'http://example.com', 'https://user:pass@example.com'])('rejects unsafe connection URL %s', url => {
    expect(() => safeHTTPS(url)).toThrow();
  });
  it.each(['https://t.me.evil.example/$invoice', 'https://t.me:8443/$invoice', 'https://t.me/$invoice?token=x', 'https://t.me/$invoice#x', 'https://t.me/other'])('rejects non-invoice link %s', url => {
    expect(() => safeInvoice(url)).toThrow();
  });
  it('accepts only supported HTTPS Telegram invoice forms', () => {
    expect(safeInvoice('https://t.me/$test-invoice')).toBe('https://t.me/$test-invoice');
    expect(safeInvoice('https://t.me/invoice/test-invoice')).toBe('https://t.me/invoice/test-invoice');
  });
});
