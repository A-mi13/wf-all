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
  (`data-theme` до модульных скриптов, фон = `tokens.color.bg[тема]`), обход всех разделов `NAV` (`e2e/sections.ts`,
  первым — `/`, сверка с `NAV` — `env.test.ts`) и панели приглашения без `securitypolicyviolation`; обход начинается со
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
- TOTP — `TotpDevice` (код шага один раз, иначе ждёт следующий), письма — `waitForMail` (фон admin-api шлёт не сразу);
  «письмо одно» — принято не раньше подтверждения и `expectMailCountStable` (~3 с опроса), а не одна проверка «сейчас
  одно».

## На что опирается
Элементы — по ролям и подписям `t(key)` из `KEYS` (`e2e/keys.ts`; новый ключ для e2e — туда, страж `e2e/keys.test.ts`).
Устройство элементов: `OtpInput` — `role="group"` + 6 `textbox`, роль приглашения — `combobox` + `option` (мышью сначала
значение не по умолчанию; `prod.spec` открывает список роли — пузырь под настоящей CSP), приглашение — `Drawer`
(`role="dialog"`), вкладка «Входящие N» (число в имени), «Подтвердить» без окна, «Выйти» видна на 1280 px; палитра —
`dialog` с именем `palette.title`, поле — `combobox`, пункты — `option` (подпись клавиши `aria-hidden`, в имя не входит).
