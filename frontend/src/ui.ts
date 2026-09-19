import type { Offer, Order, Subscription } from './api';
import { errorText } from './api';
import type { Store, Resource } from './store';
import { safeHTTPS } from './telegram';
import type { WebApp } from './telegram';

export function el<K extends keyof HTMLElementTagNameMap>(tag: K, className = '', text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function button(text: string, action: () => void, secondary = false, disabled = false) {
  const node = el('button', secondary ? 'button secondary' : 'button', text);
  node.type = 'button'; node.disabled = disabled; node.addEventListener('click', action);
  return node;
}
function go(route: string) { window.location.hash = route; }
function link(text: string, route: string, secondary = false) {
  const node = el('a', secondary ? 'button secondary' : 'button', text); node.href = `#${route}`; return node;
}
function heading(title: string, subtitle: string) {
  const node = el('div', 'section-heading'); node.append(el('h1', '', title), el('p', 'muted', subtitle)); return node;
}
function message(title: string, text: string, tone = '') {
  const node = el('section', `card message ${tone}`); node.append(el('h2', '', title), el('p', 'muted', text)); return node;
}
function errorCard(error: unknown, retry: () => void) {
  const node = message('Не получилось загрузить', errorText(error), 'error'); node.setAttribute('role', 'alert');
  node.append(button('Попробовать снова', retry, true)); return node;
}
function skeleton() {
  const node = el('div', 'skeletons'); node.setAttribute('role', 'status'); node.setAttribute('aria-label', 'Загрузка');
  for (let i = 0; i < 3; i++) node.append(el('div', 'skeleton'));
  return node;
}
function resource<T>(value: Resource<T>, render: (data: T) => HTMLElement, retry: () => void): HTMLElement {
  if (value.loading) return skeleton();
  if (value.error) return errorCard(value.error, retry);
  return render(value.data);
}
const date = (raw: string) => new Date(raw).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });
export function price(offer: Offer) {
  if (offer.currency === 'XTR') return `${offer.amount_cents.toLocaleString('ru-RU')} ★`;
  return `${(offer.amount_cents / 100).toLocaleString('ru-RU')} ${offer.currency}`;
}
const subLabels: Record<Subscription['status'], string> = {
  active: 'Активна', expired: 'Срок закончился', revoked: 'Доступ отозван', paused: 'Приостановлена', canceled: 'Отменена',
};
const orderLabels: Record<Order['status'], string> = {
  pending: 'Ожидает оплаты', paid: 'Оплачено', expired: 'Срок счёта истёк', canceled: 'Покупка отменена',
};
function subscriptionCard(sub: Subscription | null, details = false) {
  if (!sub) {
    const card = message('Начнём с подключения', 'Подписки пока нет. Вернитесь в чат с ботом и пройдите начальное подключение, затем откройте Mini App снова.');
    card.append(el('span', 'badge', 'Ваш доступ появится здесь')); return card;
  }
  const active = sub.status === 'active';
  const card = el('section', active ? 'card access-card' : 'card');
  card.append(el('span', active ? 'badge positive' : 'badge', subLabels[sub.status] ?? 'Статус уточняется'), el('h2', 'access-title', active ? 'Ваш личный доступ' : 'Моя подписка'));
  if (sub.expires_at) {
    card.append(el('p', 'muted', `До ${date(sub.expires_at)}`));
    // Date arithmetic is presentation only: never override server status.
    if (active) {
      const days = Math.ceil((Date.parse(sub.expires_at) - Date.now()) / 86400000);
      card.append(el('p', 'remaining', days > 0 ? `Осталось ${days} дн.` : 'Обновите статус подписки'));
    }
  } else card.append(el('p', 'muted', 'Без даты окончания'));
  if (active) card.append(link('Подключиться ↗', 'connection'));
  else if (sub.status === 'expired') {
    card.append(el('p', 'muted', 'Выберите доступное предложение, чтобы снова пользоваться подпиской.'), link('Выбрать доступ', 'catalog'));
  } else card.append(el('p', 'muted', 'Для уточнения статуса обратитесь в поддержку через бота.'));
  if (!details) card.append(link('Подробнее о подписке', 'subscriptions', true));
  return card;
}
function offerCard(offer: Offer) {
  const card = el('a', 'card offer-card'); card.href = `#product/${offer.offer_id}`;
  card.append(el('span', 'offer-icon', '↗'), el('h2', '', offer.name), el('p', 'muted', `${offer.duration_days} дней доступа`));
  const row = el('div', 'row'); row.append(el('strong', 'price', price(offer)), el('span', 'arrow', '→')); card.append(row);
  if (offer.currency !== 'XTR') card.append(el('p', 'small muted', 'Оплата Stars недоступна'));
  return card;
}
function catalog(offers: Offer[]) {
  const node = el('div', 'stack');
  if (!offers.length) return message('Предложения появятся здесь', 'Сейчас для вашей подписки нет доступных предложений. Проверьте статус подписки или вернитесь чуть позже.');
  for (const offer of offers) node.append(offerCard(offer));
  return node;
}

export function mount(root: HTMLElement, store: Store, telegram?: WebApp) {
  let notice = '';
  let previousRoute = '';
  const refresh = () => { void store.refresh(); };
  const back = () => {
    const route = location.hash.slice(1);
    go(route.startsWith('product/') ? 'catalog' : route === 'connection' ? 'subscriptions' : 'home');
  };
  telegram?.BackButton.onClick(back);
  const render = () => {
    const activeElement = document.activeElement as HTMLElement | null;
    const focusKey = activeElement?.dataset.focus;
    const route = location.hash.slice(1) || 'home';
    const view = el('div', 'app-shell');
    const header = el('header', 'header');
    const brand = el('a', 'brand', 'RS8'); brand.href = '#home'; brand.setAttribute('aria-label', 'На главную');
    header.append(brand, el('span', 'brand-caption', 'ЛИЧНЫЙ КАБИНЕТ'));
    view.append(header);
    const main = el('main', 'content'); main.id = 'main';
    if (!telegram?.initData || store.unauthorized) {
      main.append(message('Откройте из Telegram', store.unauthorized ? errorText(store.error) : 'Этот личный кабинет работает внутри Telegram. Откройте Mini App из меню вашего бота.'));
      if (telegram?.initData) main.append(button('Закрыть Mini App', () => telegram.close()));
      view.append(main); root.replaceChildren(view); telegram?.BackButton.hide(); return;
    }
    if (route !== 'home') main.append(button('← Назад', back, true));
    if (route.startsWith('product/') || route.startsWith('purchase/') || route === 'connection') telegram.BackButton.show();
    else telegram.BackButton.hide();
    if (notice) { const live = el('p', 'notice', notice); live.setAttribute('role', 'status'); main.append(live); }
    const retry = () => { void store.checkOrder(route.split('/')[1]); };
    if (route === 'home') {
      const name = telegram.initDataUnsafe?.user?.first_name;
      main.append(heading(name ? `Привет, ${name}` : 'Добро пожаловать', 'Ваш доступ. Всё под контролем.'));
      main.append(resource(store.subscription, sub => subscriptionCard(sub), refresh));
      const banner = el('section', 'card promo');
      banner.append(el('span', 'eyebrow', 'БОЛЬШЕ ВОЗМОЖНОСТЕЙ'), el('h2', '', 'Доступ в вашем ритме'), el('p', 'muted', 'Выберите срок и оплатите прямо в Telegram.'), link('Смотреть предложения', 'catalog', true)); main.append(banner);
      const pending = store.recent.data.find(order => order.status === 'pending');
      if (pending) main.append(link('Продолжить покупку →', `purchase/${pending.order_id}`, true));
      main.append(button('Обновить данные', refresh, true, store.subscription.loading));
    } else if (route === 'catalog') {
      main.append(heading('Выберите свой доступ', 'Прозрачная цена. Удобная оплата в Telegram.'), resource(store.offers, catalog, refresh));
    } else if (route.startsWith('product/')) {
      main.append(heading('Ваш следующий шаг', 'Проверьте предложение перед покупкой.'));
      main.append(resource(store.offers, offers => {
        const offer = offers.find(item => item.offer_id === route.split('/')[1]);
        if (!offer) return message('Предложение недоступно', 'Вернитесь в каталог и обновите список.');
        const card = el('section', 'card product-detail');
        card.append(el('span', 'badge', 'Доступ по подписке'), el('h2', '', offer.name), el('p', 'price large', price(offer)), el('p', 'muted', `${offer.duration_days} дней · разовая покупка`));
        if (offer.available_until) card.append(el('p', 'small muted', `Предложение доступно до ${date(offer.available_until)}`));
        card.append(el('div', 'divider'), el('p', '', '✦ Подключение через ваш личный кабинет'), el('p', '', '✦ Без автоматических списаний'));
        if (offer.currency === 'XTR') card.append(button(store.busy ? 'Создаём покупку…' : 'Перейти к покупке', () => { void store.buy(offer).then(order => { if (order) go(`purchase/${order.order_id}`); }); }, false, store.busy));
        else card.append(el('p', 'muted', 'Это предложение не поддерживает Stars. Выберите предложение с ценой в звёздах.'));
        return card;
      }, refresh));
      if (store.error) main.append(errorCard(store.error, refresh));
    } else if (route.startsWith('purchase/')) {
      main.append(heading('Ваша покупка', 'Безопасная оплата Telegram Stars.'));
      const order = store.order;
      if (!order || order.order_id !== route.split('/')[1]) {
        main.append(store.error ? errorCard(store.error, retry) : skeleton());
      } else {
        const card = el('section', 'card');
        const paid = order.status === 'paid';
        card.append(el('span', paid ? 'badge positive' : 'badge', orderLabels[order.status] ?? 'Статус уточняется'), el('h2', '', order.name), el('p', 'price large', price(order)), el('p', 'muted', `${order.duration_days} дней доступа`));
        if (paid) {
          card.append(el('p', 'success-copy', 'Готово! Оплата подтверждена сервером.'), el('p', 'muted', 'Подготовка доступа может занять немного времени.'));
          main.append(card, resource(store.subscription, sub => subscriptionCard(sub, true), refresh));
        } else {
          if (order.status === 'pending') {
            if (order.expires_at) card.append(el('p', 'small muted', `Счёт действует до ${new Date(order.expires_at).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}`));
            if (order.checkout_started) card.append(el('p', 'muted', 'Платёж уже начат. Ждём подтверждение. Не оплачивайте повторно. Если оплата прервана, дождитесь окончания срока счёта.'));
            else card.append(button(store.busy ? 'Открываем Telegram…' : `Оплатить ${price(order)}`, () => { void store.pay(); }, false, store.busy || store.checking || !telegram.isVersionAtLeast('6.1') || order.currency !== 'XTR'));
            if (!telegram.isVersionAtLeast('6.1')) card.append(el('p', 'muted', 'Для оплаты обновите Telegram.'));
          }
          if (order.status === 'expired') card.append(el('p', 'muted', 'Срок счёта закончился. Если звёзды уже списаны, не платите повторно: подтверждение может задержаться. Обновите статус или обратитесь в поддержку через бота.'), link('Вернуться в каталог', 'catalog', true));
          if (order.status === 'canceled') card.append(el('p', 'muted', 'Сервер отменил эту покупку.'), link('Выбрать предложение', 'catalog', true));
          if (store.paymentHint) {
            const hints = { opening: 'Следуйте подсказкам в окне Telegram.', paid: 'Telegram завершил оплату. Проверяем подтверждение сервера…', pending: 'Платёж обрабатывается. Мы проверяем статус.', cancelled: 'Окно оплаты закрыто. Проверяем, не был ли платёж уже начат.', failed: 'Telegram сообщил об ошибке оплаты. Проверяем состояние на сервере.' };
            const hint = el('p', 'notice', hints[store.paymentHint]); hint.setAttribute('role', 'status'); card.append(hint);
          }
          card.append(button(store.checking ? 'Проверяем…' : 'Проверить статус', retry, true, store.checking));
          main.append(card);
        }
        if (store.error) main.append(errorCard(store.error, retry));
      }
    } else if (route === 'subscriptions') {
      main.append(heading('Мои подписки', 'Ваш доступ и срок действия.'), resource(store.subscription, sub => subscriptionCard(sub, true), refresh), button('Обновить статус', refresh, true, store.subscription.loading));
    } else if (route === 'connection') {
      main.append(heading('Вы на шаг ближе', 'Подключите доступ на вашем устройстве.'));
      main.append(resource(store.subscription, sub => {
        if (!sub || sub.status !== 'active') return subscriptionCard(sub, true);
        const card = el('section', 'card steps');
        for (const [index, [title, text]] of [['Откройте инструкцию', 'Готовая страница с QR-кодом и ссылкой подключения.'], ['Добавьте подписку', 'Следуйте инструкции в вашем VPN-приложении.'], ['Подключитесь', 'Выберите сервер и включите соединение.']].entries()) {
          const step = el('div', 'step'); const copy = el('div'); copy.append(el('h2', '', title), el('p', 'muted', text)); step.append(el('span', 'step-number', String(index + 1)), copy); card.append(step);
        }
        card.append(button('Открыть подключение ↗', () => {
          try { telegram.openLink(safeHTTPS(sub.connection_url)); } catch { notice = 'Не удалось открыть подключение. Обновите подписку.'; render(); }
        }), button('Скопировать ссылку подписки', () => {
          void (async () => {
            try { await navigator.clipboard.writeText(safeHTTPS(sub.subscription_url)); notice = 'Ссылка скопирована. Не передавайте её другим.'; }
            catch { notice = 'Копирование недоступно. Откройте страницу подключения и скопируйте ссылку там.'; }
            render();
          })();
        }, true), el('p', 'small muted', 'Ссылка — ваш личный ключ доступа. Не делитесь ей.'));
        return card;
      }, refresh));
    } else if (route === 'profile') {
      main.append(heading('Профиль', 'Покупки и настройки приложения.'));
      const user = telegram.initDataUnsafe?.user;
      const card = el('section', 'card profile');
      const name = user?.first_name ?? 'Пользователь Telegram';
      card.append(el('div', 'avatar', Array.from(name)[0]?.toUpperCase() ?? 'T'), el('h2', '', [name, user?.last_name].filter(Boolean).join(' ')));
      if (user?.username) card.append(el('p', 'muted', `@${user.username}`));
      card.append(el('span', 'badge', 'Вход через Telegram')); main.append(card);
      main.append(message('Как вам удобно', 'Тема синхронизируется с Telegram. Язык интерфейса — русский. Для помощи вернитесь в чат с ботом.'));
      main.append(el('h2', 'section-title', 'Последние покупки'), el('p', 'small muted', 'Показываем до 20 последних покупок Mini App.'));
      main.append(resource(store.recent, orders => {
        if (!orders.length) return message('Покупок пока нет', 'Когда вы выберете доступ, покупка появится здесь.');
        const list = el('div', 'stack');
        for (const order of orders) {
          const item = el('a', 'card order-row'); item.href = `#purchase/${order.order_id}`;
          item.append(el('strong', '', order.name), el('span', 'muted small', `${orderLabels[order.status]} · ${price(order)} · ${date(order.created_at)}`)); list.append(item);
        }
        return list;
      }, refresh), button('Обновить покупки', refresh, true, store.recent.loading));
    } else main.append(message('Страница не найдена', 'Перейдите в один из разделов ниже.'));
    view.append(main);
    const nav = el('nav', 'bottom-nav'); nav.setAttribute('aria-label', 'Основная навигация');
    for (const [path, symbol, label] of [['home', '⌂', 'Главная'], ['catalog', '◈', 'Каталог'], ['subscriptions', '◎', 'Подписки'], ['profile', '◉', 'Профиль']]) {
      const item = el('a', 'nav-item'); item.href = `#${path}`;
      const current = route === path || (path === 'catalog' && route.startsWith('product/')) || (path === 'subscriptions' && route === 'connection') || (path === 'profile' && route.startsWith('purchase/'));
      if (current) item.setAttribute('aria-current', 'page');
      const icon = el('span', 'nav-icon', symbol); icon.setAttribute('aria-hidden', 'true');
      item.append(icon, el('span', '', label)); nav.append(item);
    }
    view.append(nav);
    // Restore keyboard focus across state renders, without HTML interpolation.
    for (const [index, node] of Array.from(view.querySelectorAll<HTMLElement>('button, a')).entries()) node.dataset.focus = `${route}:${node.textContent}:${index}`;
    root.replaceChildren(view);
    if (focusKey) Array.from(root.querySelectorAll<HTMLElement>('[data-focus]')).find(node => node.dataset.focus === focusKey)?.focus({ preventScroll: true });
    if (route !== previousRoute) { previousRoute = route; window.scrollTo(0, 0); }
  };
  const routeChanged = () => {
    notice = '';
    const route = location.hash.slice(1);
    if (route.startsWith('purchase/')) void store.selectOrder(route.split('/')[1]);
    render();
  };
  const visibility = () => store.resume();
  const unsubscribe = store.subscribe(render);
  window.addEventListener('hashchange', routeChanged);
  document.addEventListener('visibilitychange', visibility);
  window.addEventListener('online', visibility);
  routeChanged();
  return () => {
    unsubscribe(); telegram?.BackButton.offClick(back);
    window.removeEventListener('hashchange', routeChanged);
    window.removeEventListener('online', visibility);
    document.removeEventListener('visibilitychange', visibility);
  };
}
