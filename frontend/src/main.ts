import './style.css';
import { Api } from './api';
import { Store } from './store';
import { initializeTelegram } from './telegram';
import { mount } from './ui';

const telegram = window.Telegram?.WebApp;
const cleanupTelegram = initializeTelegram(telegram);
// The SDK has already consumed Telegram's launch parameters. They are not an
// application route (and raw initData must not remain in navigation history).
if (new URLSearchParams(location.hash.slice(1)).has('tgWebAppData')) {
  history.replaceState(null, '', `${location.pathname}${location.search}#home`);
}
let storage: Storage | undefined;
try { storage = window.sessionStorage; } catch { /* Telegram privacy modes may disable storage. */ }
const store = new Store(new Api(telegram?.initData ?? ''), telegram, storage);
const root = document.getElementById('app');
if (root) {
  const cleanupUI = mount(root, store, telegram);
  if (telegram?.initData) void store.refresh();
  window.addEventListener('pagehide', event => {
    if (!event.persisted) { cleanupUI(); cleanupTelegram(); store.dispose(); }
  });
}
