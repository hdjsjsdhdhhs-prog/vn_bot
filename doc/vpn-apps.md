# VPN-приложения, deep links и ограничения

Единый источник: `internal/web/templates/vpn_apps.json`.

- Go встраивает его в бинарник и рендерит `/connect/{token}`: `internal/web/vpnapps.go`.
- Mini App импортирует тот же файл при сборке: `frontend/src/vpnapps.ts`; в Docker он копируется в frontend-stage.
- Каталог валидируется при старте и в тестах: только `https`, белый список официальных хостов, схема импорта не может быть `http(s)`, `javascript`, `data`, `intent` и т.п., каждая платформа покрыта хотя бы одним приложением.

## Матрица

| Платформа | Приложение | Импорт подписки | Режим | Официальная загрузка |
|---|---|---|---|---|
| iOS | Happ | `happ://add/{subscription_url}` | direct | [App Store (Global) id6504287215](https://apps.apple.com/us/app/happ-proxy-utility/id6504287215), [happ.info](https://happ.info/) |
| iOS | Инси (Incy) | `incy://add/{subscription_url}` | direct | [App Store id6756943388](https://apps.apple.com/ru/app/incy/id6756943388), [incy.cc](https://incy.cc/) |
| iOS | V2RayTun | `v2raytun://import/{subscription_url}` | direct | [v2raytun.com](https://v2raytun.com/) |
| Android | Happ | `happ://add/{subscription_url}` | direct | [Google Play](https://play.google.com/store/apps/details?id=com.happproxy), [APK (GitHub)](https://github.com/Happ-proxy/happ-android/releases/latest/download/Happ.apk) |
| Android | Инси (Incy) | `incy://add/{subscription_url}` | direct | [Google Play](https://play.google.com/store/apps/details?id=llc.itdev.incy), [incy.cc](https://incy.cc/) |
| Android | V2RayTun | `v2raytun://import/{subscription_url}` | direct | [Google Play](https://play.google.com/store/apps/details?id=com.v2raytun.android), [v2raytun.com](https://v2raytun.com/) |
| Windows | Happ | `happ://add/{subscription_url}` | best_effort | [x64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe), [ARM64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.arm64.exe) |
| Linux | Happ | `happ://add/{subscription_url}` | best_effort | [DEB x64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.deb), [RPM x64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.rpm), [Arch x64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.pkg.tar.zst), [DEB ARM64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.arm64.deb), [RPM ARM64](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.arm64.rpm) |
| macOS | Happ | `happ://add/{subscription_url}` | best_effort | [App Store (Global)](https://apps.apple.com/us/app/happ-proxy-utility/id6504287215), [DMG (GitHub)](https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.macOS.universal.dmg), [happ.info](https://happ.info/) |

`{subscription_url}` — это `Config.SubURL(token)` (`GLOBAL_SUB_URL/<token>`), а не URL страницы `/connect/`. URL вставляется без percent-encoding — так в примерах разработчиков.

Deep link строится только если ссылка подписки состоит из `https://host[:port]/path` с символами `[A-Za-z0-9._~-]`: без query, fragment, userinfo, пробелов и кавычек. Иначе на странице остаются QR-код и копирование.

## Источники (проверено 2026-10-11)

- **Happ.** Ссылки на загрузку — с официального каталога [happ.su/main/readme](https://www.happ.su/main/readme.md); сайт — [happ.info](https://happ.info/).
  - Документация Happ ([adding-configuration-subscription](https://www.happ.su/main/faq/adding-configuration-subscription.md)) подтверждает импорт подписки через deep link, но формат URI не публикует.
  - `happ://add/` — схема, которую проект уже использует на `/i/{code}`.
  - Старый RU ID `id6746188973` в App Store отдаёт 404. Он остаётся в сообщениях бота и на trial-странице: их правка — отдельная задача.
- **Incy.** Официальная документация [docs.incy.cc/deep-links](https://docs.incy.cc/deep-links/): `incy://add/{url}` («Добавить подписку или конфигурацию напрямую»), пример `incy://import/https://…`. Загрузка — [incy.cc](https://incy.cc/) и [github.com/INCY-DEV/incy-platforms](https://github.com/INCY-DEV/incy-platforms).
- **V2RayTun.** Официальная документация [docs.v2raytun.com/deep-link](https://docs.v2raytun.com/deep-link): `v2raytun://import/{subscription_link}`.
  - Ссылки на загрузку — с [v2raytun.com](https://v2raytun.com/).
  - Указанная там страница App Store `id6476628951` на момент проверки отдавала 404 (US и RU), lookup Apple возвращал пусто. Поэтому для iOS кнопка установки ведёт на официальный сайт.
  - v2rayNG и другие клиенты намеренно не добавлены.

## Поведение

- **`/connect/{token}`.** Кнопка «Добавить подписку в {App}» — обычная ссылка `<a href="scheme://…">`: переход идёт прямо из нажатия пользователя.
  - Скрипт страницы не содержит URL и схем, а только переключает видимость.
  - После нажатия показывается «Открываем {App}…» с подсказкой про шаг 3, QR и копирование. Надписи «подписка добавлена» нет нигде: браузер не может узнать результат импорта.
- **Mini App** (вариант A). На `#connection` и в карточке активной подписки на `#subscriptions` кнопка вызывает `Telegram.WebApp.openLink(connection_url#os=…&app=…)` для подписки этой карточки.
  - Внешний браузер открывает `/connect/{token}` с предвыбором, и пользователь нажимает «Добавить подписку».
  - Telegram WebView никогда не открывает `happ://`, `incy://`, `v2raytun://`.
  - В localStorage хранится только `{"platform","app"}`.
- **Desktop Happ (`best_effort`).** Показывается предупреждение: регистрация `happ://` в Windows, Linux и macOS разработчиком не задокументирована.

## Что нельзя подтвердить без устройств

| Проверка | Статус |
|---|---|
| Happ iOS/Android открывается и импортирует по `happ://add/` | НЕ ПРОВЕРЕНО на устройстве (схема уже работает на `/i/{code}` в production) |
| Incy iOS/Android импортирует по `incy://add/` | НЕ ПРОВЕРЕНО |
| V2RayTun iOS/Android импортирует по `v2raytun://import/` | НЕ ПРОВЕРЕНО |
| Happ desktop (Windows/Linux/macOS) регистрирует `happ://` | НЕ ПРОВЕРЕНО, best-effort |
| `openLink` из Telegram iOS/Android/Desktop открывает внешний браузер с сохранённым `#fragment` | НЕ ПРОВЕРЕНО |
| Поведение Safari/Chrome при отсутствующем приложении (диалог, тишина) | НЕ ПРОВЕРЕНО: зависит от браузера, на странице всегда остаются запасные пути |
| Доступность App Store-ссылок в регионе пользователя | Проверено только по HTTP-статусу на дату выше |

Чеклист ручной проверки: для каждой строки матрицы открыть Mini App → `#connection` → выбрать систему и приложение → «Добавить подписку» → в браузере «Добавить подписку в {App}» → убедиться, что приложение показало импорт именно этой подписки и VPN подключается. Отдельно проверить сценарий без установленного приложения.

## Изменение каталога

1. Сверить ссылку или схему с официальной страницей разработчика.
2. Отредактировать `vpn_apps.json`. Если нужен новый хост — добавить его в `vpnDownloadHosts` (Go) и в тест `frontend/tests/vpnapps.test.ts`.
3. Обновить ожидания в `internal/web/vpnapps_test.go`, затем выполнить `UPDATE_GOLDEN=1 go test ./internal/web -run ConnectGolden`.
4. Прогнать `go test ./internal/web`, `npx vitest run`, `npx playwright test`.
