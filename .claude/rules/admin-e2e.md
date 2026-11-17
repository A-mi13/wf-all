---
paths:
  - "apps/admin/e2e/**"
  - "apps/admin/playwright.config.ts"
  - "apps/admin/tsconfig.e2e.json"
  - "scripts/e2e-admin.sh"
  - "scripts/check-admin-dist.mjs"
---

# Админка: живой e2e (`apps/admin/e2e`)

Ядро — `admin.md`. Запуск — `./task admin:e2e` (CLAUDE.md «Проверки»).

## Прогон
- `scripts/e2e-admin.sh`: замок прогона `.tools/e2e/run.lock` с PID (второй прогон не пересоздаст базу под первым,
  брошенный замок берётся заново); база `wf_e2e` на хосте `WF_E2E_PG_SUPERUSER_URL`, миграции, сборка admin-api,
  одноразовый ключ — base64url (Git Bash переписывает значение с ведущим «/» в путь Windows; `WF_E2E_*` — и в
  `MSYS2_ENV_CONV_EXCL`); страж `e2e/env.ts`: негодный ключ или адрес базы — ошибка до старта без значения. С проектом
  `prod` — сборка админки (`vite build` без tsc, шаг стенда) и `check-admin-dist.mjs` → Playwright.
- Серверы и порты — `e2e/env.ts`: Mailpit → admin-api → Vite; пара Chromium 5174/18181 с `__Host-` cookie, пара WebKit
  5175/18182 с `ADMIN_COOKIE_SECURE=false`, `prod` — admin-api 18183 без Vite (отдаёт `apps/admin/dist`,
  `ADMIN_STATIC_DIR` только у него, с настоящим `staticCSP`, Chromium, `__Host-` cookie); Mailpit 18026/11026. Ключ и
  база прогона `WF_E2E_*` — только admin-api под именами `ADMIN_*`, у Vite, Mailpit и дочерних процессов затёрты пустыми
  (Playwright складывает окружение сервера поверх `process.env`).
- Импорт GeoNames: скрипт собирает ещё воркер и `cmd/geofixture` (в образ не входит); серверы — Mailpit → фикстура
  GeoNames 18190 (HTTPS, тестовый ключ `httptest`, PEM корня `.tools/e2e/geofixture-ca.pem` → `WORKER_GEONAMES_CA_FILE`,
  готовность `/readyz`) → воркер (`workerEnv`: база `wf_e2e` под `worker`, источник — только фикстура, Mailpit e2e;
  готовность — строка «воркер запущен») → пары; dev `WORKER_*` вычищаются, `WF_E2E_WORKER_DATABASE_URL` — только воркеру.
- Публичный api (план I-4, Task 11): скрипт собирает `api` и `admin-api` с тегом `wf_e2e` (тестовые порты условий
  капитанства «выполнено»; прод-образ и стенд — без тега, страж `TestProdBuildWithoutE2ETag` в `config`, тест портов в
  CI — команда `backend:test`, страж `TestE2ECaptainChecksTestInCI`). Сервер — после воркера: `apiEnv()` (127.0.0.1:18184,
  готовность `/readyz`, база `wf_e2e` под ролью `api`, одноразовые `WF_E2E_JWT_SEEDS`/`HUMANCHECK_KEYS`/`DEVICE_KEYS`/
  `API_DATABASE_URL` → `API_*` только у api, кольцо identity — то же `WF_E2E_IDENTITY_SECRET_KEYS`, что у воркера: письма
  с кодами собирает воркер; `API_EMBED_WORKER=false`, документы — `E2E_LEGAL_DOCUMENTS`); dev `API_*` вычищаются, как
  `ADMIN_*`/`WORKER_*`. Клиент — `e2e/api.ts` (`PublicApi` на `APIRequestContext`: `device` android в теле, Bearer, ключ
  идемпотентности на мутациях с замком, `403 humancheck.required` — решает PoW и повторяет, политику не ослабляет).
- Лимиты admin-api в прогоне — умолчания (`ADMIN_RATE_LIMITS` пустой у всех пар, страж `env.test.ts`): обход разделов
  `prod.spec` — живой страж `ratelimit.AdminRules` и класса фоновых `staff_background` (`backend-http.md`); расширенный
  лимит прогона спрятал бы их поломку (итоговое ревью I-4, I1).
- Пары admin-api на одной базе все исполняют очередь staff: письмо может уйти из пары другого браузера со ссылкой на
  чужой origin — сценарии берут из письма только код.
- Проекты по умолчанию — `chromium,webkit,prod` (одно умолчание в `e2e/env.ts` и скрипте, страж `env.test.ts`), поэтому
  локальный `./task admin:e2e` (и `./task ci`) собирает админку; без браузера WebKit локально —
  `WF_E2E_PROJECTS=chromium,prod` (так в CI).
- Vitest берёт только `*.test.*` (`test.include`), `e2e/*.spec.ts` — Playwright; `e2e/`, `playwright.config.ts` и
  конфиги Vite проверяет `tsconfig.e2e.json` (node-типы) — `typecheck` гоняет оба tsconfig.

## Сценарии
- `staff.spec.ts` — сценарий §11 (serial, `retries: 0`; шаг 8 — свой владелец принимает приглашение на EN, 2FA на EN:
  подписи — `t(key, 'en')`; после принятия и 2FA — стартовая «Главная» `/` с `h1` `nav.home` и видимой «Выйти»);
  `stale-cookie.spec.ts` — вход с протухшей cookie в обоих браузерах.
- `prod.spec.ts` — собранная админка: тема до первого кадра при явном выборе против эмулированной системной
  (`data-theme` до модульных скриптов, фон = `tokens.color.bg[тема]`), обход всех разделов `NAV` (`e2e/sections.ts`:
  `{ path, titleKey }`, первым — `/`, сверка путей с `NAV` и заголовка с `labelKey` пункта — `env.test.ts`; исключения —
  `TITLE_NOT_NAV_LABEL`): в каждом разделе — тишина сети (`networkidle`) и `h1` **по имени** (у `RateLimitedState` и
  `ForbiddenState` тоже есть `h1`), за весь обход — ни одного ответа `/v1/*` ≥ 400 (слушатель `response`, список
  «раздел: статус метод путь») и панели приглашения без `securitypolicyviolation`; обход начинается со
  стартовой: палитра открывается **кнопкой** «Искать везде» (`palette.open`), ввод в combobox `palette.input`, выбор
  раздела — переход; затем клавишей Ctrl+K (настоящий браузер со своим Ctrl+K) и Esc; новые всплывающие дописывают своё открытие в обход.
  Матрица флагов (`/flags`) — виртуализация столбцов под настоящей CSP, доказанная положительно: столбцов в DOM меньше
  `aria-colcount`, последний город прокручивается в окно, `--wf-virtual-start` > 0. Городов больше порога засевает
  `scripts/e2e-admin.sh` шаг 3a после миграций (`FLAG_CITIES` = `E2E_FLAG_CITIES` = 150 ≥ `MATRIX_VIRTUALIZE_FROM` × 3,
  страж `env.test.ts`).
- `geo.spec.ts` — импорт RU из фикстуры → «выполнена», кто запустил — владелец, конфликтов нет → активация Михайловска
  526815 (Свердловская область) пилотом, slug `mikhaylovsk` (тёзка из сида — `mihaylovsk`, R13) → журнал
  (`geo.import_requested` — владелец, `geo.import` — система, `geo.city_activated` — владелец); один проект — `geoProject`
  (prod, без него chromium), без `securitypolicyviolation` на всём пути.
- `identity-api.spec.ts` и `captaincy.spec.ts` (план I-4, Task 11) — один проект `identityProject` (как geo: prod, без
  него chromium). Ядро identity через api: регистрация с кодом из Mailpit → refresh → logout (refresh после — 401
  `refresh_invalid`) → вход → удаление → вход `deletion_pending` → `cancelDeletion`. Капитанство: два игрока через api
  (город `stavropol` сида), заявка первого → владелец: «Капитаны» (счётчик 1 в имени ссылки — ветка `one` ICU), карточка
  (4 условия «выполнено»), контакты с причиной, «Одобрить» → `permits` игрока, журнал `captain_application_approved` и
  `pii_read` (без почты); второй — «Отклонить» «Другое» с комментарием → письмо → повторная подача `409
  application_cooldown`; «Игроки» — поиск по почте → карточка без 2FA; без `securitypolicyviolation`.
- TOTP — `TotpDevice` (код шага один раз, иначе ждёт следующий), письма — `waitForMail` (фон admin-api шлёт не сразу);
  «письмо одно» — принято не раньше подтверждения и `expectMailCountStable` (~3 с опроса), а не одна проверка «сейчас
  одно».

## На что опирается
Элементы — по ролям и подписям `t(key)` из `KEYS` (`e2e/keys.ts`; новый ключ для e2e — туда, страж `e2e/keys.test.ts`).
Устройство элементов: `OtpInput` — `role="group"` + 6 `textbox`, роль приглашения — `combobox` + `option` (мышью сначала
значение не по умолчанию; `prod.spec` открывает список роли — пузырь под настоящей CSP), приглашение — `Drawer`
(`role="dialog"`), вкладка «Входящие N» (число в имени), «Подтвердить» без окна, «Выйти» видна на 1280 px; палитра —
`dialog` с именем `palette.title`, поле — `combobox`, пункты — `option` (подпись клавиши `aria-hidden`, в имя не входит).
