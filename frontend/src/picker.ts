// Mini App platform/app picker, built from the shared catalog (vpnapps.ts).
// Pure DOM builders: state lives in ui.ts mount(), which re-renders on change.
import type { Choice, PlatformId } from './vpnapps';
import { appById, appsFor, choose, downloadsFor, platformLabel, platforms } from './vpnapps';

function node<K extends keyof HTMLElementTagNameMap>(tag: K, className = '', text?: string): HTMLElementTagNameMap[K] {
  const item = document.createElement(tag);
  item.className = className;
  if (text !== undefined) item.textContent = text;
  return item;
}

/** Radio semantics with roving tabindex and arrow-key navigation. */
function radio(label: string, checked: boolean, key: string, select: () => void, move: (delta: number) => void) {
  const item = node('button', '', label);
  item.type = 'button';
  item.setAttribute('role', 'radio');
  item.setAttribute('aria-checked', String(checked));
  item.tabIndex = checked ? 0 : -1;
  item.dataset.radio = key;
  item.addEventListener('click', select);
  item.addEventListener('keydown', event => {
    const delta = ({ ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 } as Record<string, number>)[event.key];
    if (delta) { event.preventDefault(); move(delta); }
  });
  return item;
}

export type ChoiceChange = (choice: Choice, focus?: string) => void;

export function platformSwitch(choice: Choice, change: ChoiceChange, labelId: string): HTMLElement {
  const group = node('div', 'segmented');
  group.setAttribute('role', 'radiogroup');
  group.setAttribute('aria-labelledby', labelId);
  const ids = platforms.map(p => p.id);
  const go = (platform: PlatformId) => change(choose(platform, choice.app), `platform:${platform}`);
  for (const platform of platforms) {
    group.append(radio(platform.label, platform.id === choice.platform, `platform:${platform.id}`, () => go(platform.id), delta => {
      go(ids[(ids.indexOf(choice.platform) + delta + ids.length) % ids.length]);
    }));
  }
  return group;
}

export function appList(choice: Choice, change: ChoiceChange, labelId: string): HTMLElement {
  const group = node('div', 'app-list');
  group.setAttribute('role', 'radiogroup');
  group.setAttribute('aria-labelledby', labelId);
  const apps = appsFor(choice.platform);
  const go = (app: string) => change({ platform: choice.platform, app }, `app:${app}`);
  for (const app of apps) {
    const item = radio('', app.id === choice.app, `app:${app.id}`, () => go(app.id), delta => {
      const index = apps.findIndex(entry => entry.id === choice.app);
      go(apps[(index + delta + apps.length) % apps.length].id);
    });
    item.className = 'app-option';
    const icon = node('span', 'app-icon', Array.from(app.name)[0]?.toUpperCase() ?? '');
    icon.setAttribute('aria-hidden', 'true');
    const text = node('span', 'app-text');
    text.append(node('span', 'app-name', app.name), node('span', 'app-desc', app.description));
    const check = node('span', 'app-check'); check.setAttribute('aria-hidden', 'true');
    item.append(icon, text, check);
    group.append(item);
  }
  return group;
}

/** Official download pages for the choice; opened externally via Telegram. */
export function downloadButtons(choice: Choice, open: (url: string) => void): HTMLElement {
  const row = node('div', 'download-row');
  for (const download of downloadsFor(choice)) {
    const item = node('button', 'button secondary download', `${download.label} ↗`);
    item.type = 'button';
    item.addEventListener('click', () => open(download.url));
    row.append(item);
  }
  return row;
}

export function appName(choice: Choice): string {
  return appById(choice.app)?.name ?? choice.app;
}
export function isBestEffort(choice: Choice): boolean {
  return appById(choice.app)?.platforms[choice.platform]?.import === 'best_effort';
}
export function enableSteps(choice: Choice): HTMLElement {
  const list = node('ol', 'enable-steps');
  for (const step of appById(choice.app)?.enable_steps ?? []) list.append(node('li', '', step));
  return list;
}
export function choiceSummary(choice: Choice): string {
  return `${platformLabel(choice.platform)} · ${appName(choice)}`;
}
