# UI-фундамент админки: темы, адаптивность, паттерны скорости, витрина — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

> **Формат — бриф задач, без кода** (решение пользователя 13.10.2026, память `plan-format-brief`). Каждая задача:
> цель, уровень риска, файлы, точные интерфейсы, тесты (имя → вход → ожидаемое), страж с подсадкой бага, критерии
> приёмки, ловушки стыков, документы. Как реализовать — решает исполнитель по TDD, спеке и правилам `.claude/rules/`.
> Авторитет: спека (ред. 6) → «Ответы пользователя (03.11.2026)» → «Решения плана» (U*) → тексты
> задач. Где задача расходится со спекой без ссылки на решение U — права спека, задача — ошибка плана (сообщить
> контроллеру). Спеку не пересказываем: ссылки вида «§4.8, стр. 659–707» — на её текст, его исполнитель читает сам.

**Goal:** до G9 (Task 20–22 плана B) и до модулей игроков и матчей админка получает фундамент, на котором экраны только
собираются:
- две темы из одного `tokens.json` с проверенным контрастом и переключателем «Система · Светлая · Тёмная»;
- все разделы открыты на любой ширине, «только с компьютера» — действия по устройству, а не разделы по ширине;
- набор паттернов скорости и надёжности: палитра Ctrl/⌘+K, горячие клавиши, таблицы (шапка, столбцы, плотность,
  позиция, массовые действия, виртуализация), «Отменить» в тосте, защита от потери, относительное время,
  копирование, подсказки к терминам, свой выпадающий список и календарь;
- пять состояний данных и скелетоны одним компонентом;
- витрина компонентов, сравнение скриншотов в Linux-CI, временные скриншоты экранов для ревьюера;
- главная «Что требует внимания» — стартовая страница после входа.

**Architecture:**
- **Токены** — `contracts/tokens/tokens.json` (цвета парами `{dark, light}`, тени данными, новые токены) →
  `packages/tokens` генерирует `light-dark()` и правила `color-scheme` по `data-theme` корня (спека §2).
- **Иконки** — новый пакет `@wf/icons` (данные: выгрузка артборда 14 + дорисованные), рендер — в админке (`Icon`,
  `Illustration`), спека §4.9.
- **Админка** — всё повторяемое в `apps/admin/src/ui` (`Popover` как основа всплывающего в общем стеке оверлеев,
  `Select`/`DateField`, `DataState`, `ActionGate`, `ThemeProvider`, `CommandPalette`, `ShortcutsProvider` …), экраны
  `features/*` только собирают. Единственная запись стилей из JS — `setCssPx` (`src/ui/cssVar.ts`) для чисел
  положения `Popover` и виртуализации (спека §4.5, стр. 577–588).
- **Проверка вида** — `admin:shots` (Vite в режиме `shots` с MSW в браузере, Playwright), витрина (Ladle или своя,
  по пробе) и workflow `admin-visual.yml` (эталон — артефакт подходящего прогона `main`), спека §5.
- **Проверка под настоящей CSP** — прод-проект живого e2e: собранная админка, отданная admin-api со `staticCSP`.

**Tech Stack:** React 19.3.1, Vite 8.3.5 (минификатор CSS — Lightning CSS), TanStack Router 1.170.46 / Query
5.104.5, react-hook-form 7.91.0, use-intl 4.14.9, Vitest 5.0.5 + jsdom 30.1.3 + MSW 3.0.3 / openapi-msw 2.1.0,
Playwright 1.64.1. Новые: `@wf/icons` (воркспейс, UF1a), `@tanstack/react-virtual` (спека — 3.14.23; UF2b), `@ladle/react`
5.1.1 (UF3, только по пробе). Версия каждой новой зависимости — последняя стабильная на день установки, строка в
`docs/versions.md` (правило 8). Точные версии — `docs/versions.md`.

**Spec:** `docs/superpowers/specs/2026-10-28-ui-foundation-design.md` (ред. 7: ред. 6 Approved, fe24c4a, 09b9011, + §15 по ревью плана) —
дальше «спека». §1–§9 — основной текст, §10–§14 — поправки раундов (влиты в текст; при расхождении права поправка с
бо́льшим номером). Связанные: спека сотрудников `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §9
(«спека сотрудников»), план B `docs/superpowers/plans/2026-10-27-staff-b-geo2.md` (этап 4), правила
`.claude/rules/{frontend,admin,contracts,env}.md`, журнал `.superpowers/sdd/2026-10-28-staff-b-geo2/progress.md`.

**Предусловие старта:** спека закоммичена; группа V1 и G8 (Task 17–19) закрыты — их результат данность (спека §1.3).
План исполняется в ветке плана B (`spec/geo2-render`) между G8 и G9; бэкенд плана B (ревью G6 и N1, G7) идёт
параллельно и файлов фронта не трогает. Контракт admin заморожен — ни одна группа его не правит.

## Global Constraints

- **Правила репо** — `CLAUDE.md`, `.claude/rules/{frontend,admin,contracts,env}.md`; принципы фронта 1–15
  (`frontend.md`) — обязательны каждому экрану и проверяются ревьюером отдельным пунктом. TDD: сначала падающий тест
  (бэк и фронт); каждый страж проверяется подсадкой бага (в отчёт — что подсажено и что упало).
- **Риск задачи** — A/B/C по `.claude/agents/wf-implementer.md`: для A исполнитель до первого теста пишет таблицу
  режимов отказа (в задачах A ниже — обязательный перечень), ревьюер проходит 10 категорий.
- **Коммиты** — только свои пути: `git add <пути> && git commit -m "…" -- <пути>`, одна строка по-русски без тела.
  Никогда `git add -A/.`, `commit -a`, `stash`, `reset`, `clean`, `checkout -- .`. В папке работают другие агенты
  (ревьюеры и исполнители бэкенда) — их файлы не трогать. Снимки (`*.png`) не коммитятся никогда.
- **pnpm** (U2): два pnpm-процесса одновременно запрещены. Установки за план: UF1a.2 (связать `@wf/icons`) и UF2b.1
  (`@tanstack/react-virtual`) — исполнитель по сигналу контроллера; Ladle для пробы UF3.1 (и откат при отказе) — сам
  контроллер в тихом окне, исполнитель ждёт (I7). Сигнал «тихо» — ни один агент не гоняет процессы из
  `node_modules`, dev-сервер пользователя остановлен (m24). Всё остальное — бинарниками без pnpm: из `apps/admin` —
  `node_modules/.bin/vitest run <пути>`, `node_modules/.bin/eslint <пути>`, `node_modules/.bin/playwright`; токены —
  `node packages/tokens/scripts/build.mjs`, `node --test "packages/tokens/test/*.test.mjs"`; дизайн —
  `node --test "design/tools/*.test.mjs"`.
- **Проверки** — исполнитель гоняет только свои тесты и eslint своих путей; `typecheck`, `build`, полный lint и
  полные тесты — нет (глобальный CLAUDE.md). Контроллер после каждой фронтовой группы — `admin typecheck` (tsc
  напрямую); `admin:test`, `packages:ci`, `gen:check` — в конце плана B (K5 журнала). Живой e2e (`./task admin:e2e`) —
  один прогон после правки сценария + прогон с подсадкой, без повторов «для статистики» (память `no-repeat-runs`).
- **Docker локально не работает** — визуальное сравнение только в CI; локально `admin:shots` и витрина (смотреть).
- **CSP admin-api** (`style-src 'self'`, `script-src 'self'`): только CSS Modules (и один глобальный файл — U24);
  нет атрибута `style`, `<style>`, `dangerouslySetInnerHTML`, записи в `element.style`, кроме `setCssPx` в
  `src/ui/cssVar.ts` с вызывателями только в `src/ui/popover/*` (UF2a) и `src/ui/virtual/*` (UF2b). Инлайн-скриптов нет:
  `public/theme-init.js` — файл.
- **Токены** — только `--wf-*`. После UF1b: `accent` — только заливка; текст, линия, курсор — `accent-ink`; кольцо
  фокуса — `focus`; рамки полей — `border-control`; плейсхолдеры — `text-subtle`; на чипах — `text-muted`;
  `text-faint` — только значки (спека §2.3, §2.6).
- **Компоненты** — только из `src/ui`; нативные `<button>/<input>/<select>/<textarea>` в `features` и `app` запрещены
  (`controls.test.ts`), после UF2a.6 `<select>` запрещён и в `src/ui`. Каждый новый компонент `src/ui` — с историями
  (CSF, U12) во всех состояниях и обеих темах и с тестом на `renderWithIntl`/`renderWithProviders`.
- **Тексты** — только `apps/admin/messages/{ru,en}.json`, en — перевод; общие действия — `actions.*` (после UF2a.9);
  подписи, по которым ищет живой e2e, — `KEYS` (`e2e/keys.ts`, страж `e2e/keys.test.ts`).
- **Хранилище браузера** — каждый доступ к `localStorage`/`sessionStorage` в `try/catch` (приватный режим бросает);
  ключи — U22. Секретов и ПД сверх `display_name` в storage нет.
- **Поведение, закреплённое ревью** (решения подтверждений, `isUnknownOutcome`, ключи идемпотентности, фокус после
  исчезновения кнопки, черновики) не меняется — только оформление и обёртки (спека §6.1, стр. 1179–1182). Тесты
  экранов остаются зелёными без правки ожиданий, кроме перечисленных в задачах (имена кнопок, стартовая страница,
  телефон, выбор из списка — `pick` и перечень переписываний UF2a.6).
- **Общие файлы параллельных групп** (I8): у каждого общего файла на волну — один владелец (таблица в «Порядке
  исполнения»); группа-не-владелец не правит файл, а пишет нужные строки в свой отчёт разделом «Строки для общих
  файлов» — контроллер вносит их после ревью захода.
- **Снимки для ревьюера** (§5.4): каждая группа в конце снимает свои экраны `admin:shots` (есть с UF1a) в
  `.superpowers/sdd/<сессия>/shots/<группа>/` (каталог под `.gitignore`; не `.superpowers/shots/`), путь — в отчёте;
  удаляются после ревью группы (уборка конца сессии).
- **Документы** — по §6.4 спеки, каждая группа — свои строки, в коммитах группы (задача «документы» в конце группы).
  Память и журнал `progress.md` ведёт контроллер (U16).

## Review Focus

1. **Тема до первого кадра под настоящей CSP, прод-CSS = dev-CSS** (UF1b.4–UF1b.7): явный `build.cssTarget` (иначе
   Lightning CSS заменит `light-dark()` полифилом; страж — проверка `dist` в `admin:ci`: `light-dark(` есть,
   `--lightningcss-` нет), `theme-init.js` — файл с `no-cache`, прод-проект e2e сверяет фон `body` при явном выборе
   **против** системной темы (`wf.theme = light` при эмуляции тёмной системы и наоборот — иначе подсадка «без
   скрипта» немая, I3). Тест — `e2e/prod.spec.ts`, `scripts/check-admin-dist.mjs`.
2. **Стражи контраста не вакуумны** (UF1b.2–UF1b.3): пары собираются из CSS Modules, а не из ручного списка;
   плейсхолдер — только `text-subtle`; `text-subtle` на `chip-bg` запрещён. Тест — `contrast.test.mjs`,
   `contrast-css.test.ts`.
3. **«Только с компьютера» — по устройству** (UF2a.1): 640 px + `pointer: fine` (масштаб на ноутбуке) — все кнопки;
   390 + `coarse` — пометка; «Завершить все сеансы» цели-админа на телефоне есть (право самого действия). Решения
   `staff.*` — по префиксу, незнакомое — закрыто. Тест — `desktopOnly.test.tsx`.
4. **Стек оверлеев с видом записи** (UF2a.2): Esc — верхняя из всех; блок прокрутки, ловушка Tab, `topOverlayPanel`,
   `hasModalOverlay` — только модальные; немодальный закрывается по `focusout` якоря и пузыря; `mousedown` в пузыре
   отменён (кроме `input`/`textarea`), `pointerdown` — нет; открытие любого `Overlay` закрывает все `Popover`. Тест —
   `overlays.test.tsx`, `stack.test.ts`, `Select.test.tsx`.
5. **Исключение CSP узкое** (UF2a.2, UF2b.1): `setProperty` — только в `cssVar.ts`, вызыватели — только `popover/*` и
   `virtual/*`, имя — тип `--wf-…`, значение — число; подсадка прод-e2e — то, что CSP режет. Тест — `csp.test.ts`,
   `prod.spec.ts`.
6. **Свой `SelectField` без правки экранов** (UF2a.6): API поля и контракт `rules.onChange(e.target.value)` прежние;
   имя combobox — только подпись поля (как в примере APG); выбор мышью не теряется (страж R4-1 в jsdom + живой e2e
   выбирает роль не по умолчанию мышью); тесты экранов меняются только по перечню UF2a.6.
7. **Массовые действия** (UF2b.5): право на каждую строку; итог по видам; повтор — 4xx новым ключом, неизвестный исход
   тем же ключом, `202` — никогда; 429 — пауза по `Retry-After`. Тест — `bulk.test.tsx`.
8. **«Отменить»** (UF2b.6): обратное — новым ключом и с результатом прямого (актуальная `version`, прежнее значение —
   I5); исход обратного неизвестен — перечитать и сказать факт, не повторять вслепую; Esc тоста — только при пустом
   стеке. Тест — `undo.test.tsx`.
9. **Один вопрос на один уход** (UF2b.7–UF2b.8): после «Уйти» закрытие панели с `ignoreBlocker: true`; исключений по
   ключу панели нет; «Назад» с грязной панели спрашивает; все тесты ухода — на browser history. Тест —
   `leaveGuard.test.tsx`.
10. **Панели в адресе** (UF2b.8): `PANEL_SEARCH_KEYS` вырезаются из памяти «Назад» и не пересекаются с ключами
    фильтров ни на одном пути (панель страны — `?edit=`, а `country` — фильтр; страж, I1); уход из панели на другой
    путь — `replace`; ключ без права снимается; 403/404 по id — одно состояние «не найден»; `close` идемпотентен.
11. **Эталон скриншотов** (UF3.5–UF3.6): workflow без `paths`; база — `HEAD^1`; эталон — предок базы без изменений
    визуальных путей между ними; `expired` до скачивания; посторонняя метка не отменяет сравнение.
12. **Главная** (UF4.1–UF4.2): `?return=` важнее главной; плитки без права не рендерятся и не запрашивают; «99+» по
    `≥ 100` или `next_cursor`; все чтения — `background()`.
13. **Палитра** (UF4.3–UF4.5): `commands.ts` не импортирует экраны; запрос сущностей — только с правом, от 2 символов,
    последний ответ побеждает; недавнее стирается при выходе и при входе другого сотрудника.
14. **Матрица прав на телефоне и container queries** (UF2a.1, UF2a.5, UF2b.2): матрица остаётся таблицей с
    горизонтальной прокруткой и закреплённым первым столбцом, а не карточками; `Card`-сетка, формы, тело панели —
    `@container` по токенам `container.*`; столбцы `priority: 'low'` — по окну 768–1023, явно включённый не прячется
    (I6, N1).

---

## File Structure

| Путь | Ответственность | Задача |
| --- | --- | --- |
| `apps/admin/src/test/{base.ts,defaults.ts}`, `apps/admin/src/shots/*`, `apps/admin/src/app/styles.ts`, `apps/admin/vite.shots.ts`, `apps/admin/vite.config.ts`, `scripts/admin-shots.mjs` (+ тест), `apps/admin/Taskfile.yml`, корневой `Taskfile.yml` (`scripts:test`) | моки в браузере, `admin:shots` | UF1a.1 |
| `packages/icons/*`, `design/tools/extract-icons.mjs` (+ тест), `Taskfile.yml` (`packages:ci`), `apps/admin/package.json`, `pnpm-lock.yaml` | пакет `@wf/icons` | UF1a.2 |
| `apps/admin/src/ui/icon/*`, `apps/admin/src/ui/shell/{AppShell.tsx,types.ts}`, `apps/admin/src/app/nav.ts` | `Icon`, `Illustration`, перенос `NAV_ICONS` | UF1a.3 |
| `CLAUDE.md`, `.claude/agents/wf-{implementer,reviewer}.md`, `design/README.md`, `.claude/rules/{frontend,admin}.md` | документы UF1a | UF1a.4 |
| `contracts/tokens/tokens.json`, `packages/tokens/{scripts/build.mjs,scripts/contrast.mjs,tokens.css,tokens.ts,test/*}` | пары, новые токены, генератор, контраст | UF1b.2 |
| `apps/admin/src/**/*.module.css`, `apps/admin/src/ui/contrast-css.test.ts`, `apps/admin/src/ui/layout-css.test.ts` | перевод CSS на роли, стражи по CSS | UF1b.3 |
| `apps/admin/vite.config.ts` (+ тест), `scripts/check-admin-dist.mjs` (+ тест), `apps/admin/Taskfile.yml` (`ci`) | `build.cssTarget`, проверка `dist` | UF1b.4 |
| `apps/admin/public/theme-init.js`, `apps/admin/index.html`, `apps/admin/src/ui/theme/*`, `apps/admin/src/app/providers.tsx`, `backend/internal/httpapi/admin/static_test.go` | тема до кадра, провайдер, цвета темы | UF1b.5 |
| `apps/admin/src/ui/theme/ThemeSwitch.tsx`, `apps/admin/src/ui/shell/*`, `apps/admin/src/features/auth/{AuthLayout.tsx,Auth.module.css}` | переключатель, карточка сотрудника | UF1b.6 |
| `apps/admin/{playwright.config.ts,e2e/env.ts,e2e/prod.spec.ts}`, `scripts/e2e-admin.sh`, `.github/workflows/admin.yml` | прод-проект живого e2e | UF1b.7 |
| `design/03-токены.md`, `design/README.md`, `.claude/rules/{frontend,admin}.md`, спека сотрудников §9.1 | документы UF1b | UF1b.8 |
| `apps/admin/src/ui/media.ts`, `apps/admin/src/app/desktopOnly.ts`, `apps/admin/src/ui/gate/*`, `apps/admin/src/app/nav.ts`, `features/{staff,approvals}/*`, `apps/admin/src/test/media.ts` | открытые разделы, `ActionGate` | UF2a.1 |
| `apps/admin/src/ui/overlay/{stack.ts,Overlay.tsx}`, `apps/admin/src/ui/popover/*`, `apps/admin/src/ui/cssVar.ts`, `apps/admin/src/ui/csp.test.ts`, `apps/admin/src/test/setup.ts`, `apps/admin/src/ui/global.css`, `apps/admin/src/main.tsx` | `Popover` в стеке, `setCssPx`, прокладка jsdom | UF2a.2 |
| `apps/admin/src/test/app.tsx`, `.claude/rules/admin.md` («Тесты») | стенд browser history | UF2a.3 |
| `apps/admin/src/ui/hint/*`, `apps/admin/src/ui/CopyButton.tsx`, `messages/*` (`glossary.*`), `apps/admin/src/i18n/labels.test.ts` | `Tooltip`, `TermHint`, `CopyButton` | UF2a.4 |
| `apps/admin/src/ui/shell/{AppShell.tsx,AppShell.module.css}` | полоса 768–1023 | UF2a.5 |
| `apps/admin/src/ui/form/{Select.tsx,SelectField.tsx,MultiSelectField.tsx}`, `apps/admin/src/test/pick.ts`, 9 тестов экранов, `apps/admin/e2e/{staff.spec.ts,keys.ts}`, `apps/admin/src/ui/controls.test.ts` | свой выпадающий список | UF2a.6 |
| `apps/admin/src/ui/form/{DateField.tsx,Calendar.tsx}`, `apps/admin/src/i18n/week.ts`, `features/audit/AuditFilters.tsx` | свой календарь | UF2a.7 |
| `apps/admin/src/ui/states/{DataState.tsx,Skeleton.tsx,…}`, `apps/admin/src/app/query.ts` | пять состояний, скелетоны | UF2a.8 |
| `apps/admin/src/ui/time/*`, `apps/admin/src/i18n/{format.ts,vocabulary.test.ts}`, `apps/admin/eslint.config.mjs`, `messages/*` (`actions.*`) | время, числа, деньги, словарь | UF2a.9 |
| `features/{staff,me,approvals,audit,geo,auth}/*` | применение к экранам (§6.1, строки UF2a) | UF2a.10 |
| `features/geo/*`, `features/approvals/summary.ts`, `apps/admin/src/test/setup.ts`, `messages/*`, `apps/admin/src/i18n/labels.test.ts` | уборка фронта | UF2a.11 |
| спека сотрудников §9.2, §9.4, `.claude/rules/{admin,frontend}.md`, `design/README.md` | документы UF2a | UF2a.12 |
| `docs/superpowers/plans/2026-10-27-staff-b-geo2.md` (этап 4) | правки плана B (§6.3) | UF2a.13 |
| `apps/admin/package.json`, `pnpm-lock.yaml`, `apps/admin/src/ui/virtual/*`, `docs/versions.md` | виртуализация | UF2b.1 |
| `apps/admin/src/ui/table/*` | шапка, область прокрутки, `scroll-padding`, столбцы, плотность, позиция, `bulk` | UF2b.2–UF2b.5 |
| `apps/admin/src/ui/toast/*`, `apps/admin/src/api/undo.ts` | тост «Отменить», `useUndoable` | UF2b.6 |
| `apps/admin/src/app/leaveGuard.ts`, `apps/admin/src/ui/overlay/Overlay.tsx`, `apps/admin/src/app/logout.ts` | защита от потери | UF2b.7 |
| `apps/admin/src/app/{paths.ts,panels.ts}`, `apps/admin/src/ui/shell/BackLink.tsx` | панели в адресе | UF2b.8 |
| `apps/admin/src/app/router.tsx`, `apps/admin/src/ui/global.css` | ленивые маршруты, View Transitions | UF2b.9 |
| `features/{staff,audit,geo}/*`, `docs/versions.md`, `.claude/rules/admin.md` | применение к экранам (§6.1, строки UF2b), документы | UF2b.10 |
| `apps/admin/.ladle/*` или `apps/admin/{showcase.html,src/showcase/*}`, `apps/admin/src/showcase/csf.ts`, `apps/admin/Taskfile.yml`, `docs/versions.md` | витрина | UF3.1–UF3.2 |
| `apps/admin/src/ui/**/*.stories.tsx`, `apps/admin/src/ui/stories.test.ts` | истории существующих компонентов, страж | UF3.3–UF3.4 |
| `.github/workflows/admin-visual.yml`, `scripts/visual-baseline.mjs` (+ тест), `apps/admin/visual/*`, `Taskfile.yml` | визуальное сравнение в CI | UF3.5–UF3.6 |
| `CLAUDE.md`, `.claude/rules/frontend.md`, `.claude/agents/wf-reviewer.md` | документы UF3 | UF3.7 |
| `apps/admin/src/features/home/*`, `apps/admin/src/app/{nav.ts,paths.ts,router.tsx,guard.ts}` | главная, стартовая | UF4.1–UF4.2 |
| `apps/admin/src/app/commands.ts`, `apps/admin/src/features/*/commands.ts`, `apps/admin/src/ui/palette/*` | реестр команд, `fuzzyRank`, палитра | UF4.3–UF4.5 |
| `apps/admin/src/ui/shortcuts/*` | горячие клавиши, шпаргалка | UF4.6 |
| `apps/admin/e2e/*`, спека сотрудников §9.4, `.claude/rules/admin.md`, `design/README.md` | e2e стартовой, документы UF4 | UF4.7 |

# Задачи

Обозначения: **Риск** — A/B/C; **Тесты** — `Имя` → вход → ожидаемое; **Подсадка** — какой баг подсадить, какой тест
обязан упасть; **Приёмка** — что проверяет ревьюер; **Документы** — строки §6.4 спеки этой задачи. Файлы — ориентир:
исполнитель может разложить иначе внутри своего каталога, если не меняет интерфейсы стыков. Пути без префикса —
от `apps/admin/`.

## Группа UF1a «Снимки и иконки» (инструменты, вид не меняется)

Спека §7 (стр. 1239), §4.9, §5.4. Один исполнитель, один заход. Риск группы — B. Единственный `pnpm install` группы —
в UF1a.2, до всех остальных групп. Вид админки после группы не меняется (сверка — снимки до и после переноса иконок,
UF1a.3). После группы контроллер — `admin typecheck`.

### UF1a.1: моки в браузере и `admin:shots`

**Цель.** Снимать любой адрес админки на 1440 / 768 / 390 в обеих темах без живого стека: Vite в режиме `shots`
поднимает админку с MSW в браузере на тех же фикстурах, что тесты; Playwright снимает (спека §5.4, стр. 1127–1149).

**Риск:** B (инструмент разработки; в прод-сборку не попадает — страж).

**Файлы.** Создать: `src/test/{base.ts,defaults.ts}`; `src/shots/{main.tsx,server.ts,base.ts}`;
`src/app/styles.ts`; `vite.shots.ts` (плагин режима); `src/shots/shots.test.ts`; `scripts/admin-shots.mjs`,
`scripts/admin-shots.test.mjs`. Изменить: `src/test/server.ts` (база — из `./base`, общий обработчик — из
`./defaults`), `src/main.tsx` (стили — через `app/styles.ts`), `vite.config.ts` (режим `shots`), `vite.config.test.ts`,
`Taskfile.yml` админки (задача `shots`), корневой `Taskfile.yml` (задача `scripts:test` → `node --test
"scripts/*.test.mjs"`, в `ci` — m7, U21).

**Интерфейсы.**
- `src/test/base.ts`: `export const TEST_API = 'http://admin.test'` — единственное место базы тестов; `server.ts`
  реэкспортирует `TEST_API` (импорты тестов не меняются). `src/shots/base.ts`: `TEST_API = location.origin`.
- `src/test/defaults.ts` (m2): `defaultHandlers(http)` — общие ответы, нужные на любом разделе (сейчас — `GET
  /v1/approvals` пустым списком из аргумента `setupServer`); им пользуются оба сервера — тестовый и `shots`.
- `src/app/styles.ts` (m3): единственный модуль глобальных стилей — `@wf/tokens/tokens.css`, `app/fonts`, с UF2a.2 —
  `ui/global.css`; его импортируют и `main.tsx`, и `src/shots/main.tsx` — снимки видят те же стили, что сборка.
- `src/shots/server.ts` — тот же внешний вид, что `src/test/server.ts`: `TEST_API`, `http`
  (`createOpenApiHttp<paths>({ baseUrl: TEST_API })`), `server` с `use(...handlers)`, `resetHandlers()`,
  `listen()`/`close()` — поверх `setupWorker` из `msw/browser`; плюс `start(): Promise<void>` (регистрация воркера
  до монтирования). Неописанный запрос — `console.error` и счётчик `window.__wfShots.unhandled`.
- Плагин `wfShots()` (`vite.shots.ts`, имя `wf-shots`), включается только при `mode === 'shots'`:
  (1) `resolveId`: абсолютные пути `src/test/server.ts` и `src/test/base.ts` → `src/shots/server.ts`/`base.ts`;
  (2) `configureServer`: middleware отдаёт `/mockServiceWorker.js` из пакета `msw` (файл воркера в `node_modules/msw`,
  путь сверить в msw 3.0.3); (3) `transformIndexHtml`: `/src/main.tsx` → `/src/shots/main.tsx` (тот же `index.html` —
  с UF1b в нём `theme-init.js`). Режим `shots` — свой `cacheDir: 'node_modules/.vite-shots'` и порт 5176.
- `src/shots/main.tsx`: параметр адреса `?shots-role=owner|admin|moderator|setup|anonymous` (фикстуры `meOwner`,
  `meAdmin`, `meModerator`/`meGeoModerator`, `meSetup` из `src/test/me.ts`/`geo.ts`; `anonymous` — me `401`), ставит
  `defaultHandlers`, `sectionsServer()` и ручку `GET /v1/staff/me`, для роли `setup` — ещё ответ `POST
  /v1/staff/me/mfa/setup` (секрет-фикстура, m2), монтирует `AppProviders` + `AppRouter` на browser history (как
  `main.tsx`), после первой отрисовки и `document.fonts.ready` ставит атрибут `data-shots-ready` на `<html>`.
- `scripts/admin-shots.mjs`: `node scripts/admin-shots.mjs --out <каталог> [--role admin] [--widths 1440,768,390]
  [--themes dark,light] [--locale ru|en] <путь>…` (`./task admin:shots -- …` передаёт аргументы). Запускает
  `apps/admin/node_modules/.bin/vite --mode shots` (не через pnpm), ждёт порт, Chromium Playwright: на каждый путь ×
  ширину × тему — `addInitScript` (storage `wf.theme`, язык), `emulateMedia({ colorScheme, reducedMotion: 'reduce' })`,
  `goto`, ожидание `[data-shots-ready]` и тишины сети, снимок всей страницы в
  `<out>/<слаг-пути>-<ширина>-<тема>[-en].png`. Код выхода ≠ 0 при неописанных запросах или ошибках консоли (список —
  в вывод). Vite останавливается всегда (и при ошибке).

**Тесты:**
- `vite.config.test.ts` › «режим shots подключает плагин моков, test и production — нет» → `config({ mode })` →
  плагин `wf-shots` только при `shots`; у `shots` свой `cacheDir`.
- `src/shots/shots.test.ts` (страж, читает исходники как `csp.test.ts`): граф импортов от `src/main.tsx` —
  статических **и динамических** `import(…)` (после UF2b.9 разделы ленивые, m6) — не содержит `msw`, `src/shots/*`,
  `src/test/*`; в `public/` нет `mockServiceWorker.js`.
- `scripts/admin-shots.test.mjs` (`node --test`): без `--out` → ошибка с подсказкой; путь
  `/geo/cities?status=…` → безопасное имя файла (без `/`, `?`, `%`); неизвестная роль/тема → ошибка; список снимков =
  пути × ширины × темы.
- Ручная проверка (в отчёт, не тест): `/audit`, `/staff`, `/geo/cities`, `/me` на трёх ширинах — PNG не пустые,
  неописанных запросов нет.

**Подсадка.** Импорт `msw/browser` из `src/app/providers.tsx` — страж графа краснеет; то же через `await
import('msw/browser')` — краснеет; плагин без проверки режима — тест конфига краснеет.

**Приёмка.** Обычная сборка по содержанию не тронута (`main.tsx` — тот же набор стилей через `app/styles.ts`,
`index.html` прежний); фикстуры тестов не переписаны; путь к снимкам — параметр, а не догадка о сессии;
`./task scripts:test` гоняет `scripts/admin-shots.test.mjs`.

**Ловушки.**
- `resolve.alias` матчит спецификатор, а тесты и фикстуры импортируют `./server`/`../test/server` относительно —
  подмена только через `resolveId` по нормализованному абсолютному пути (Windows: `\` → `/`, регистр диска).
- Свой `cacheDir` и порт обязательны: два dev-сервера в общий `node_modules/.vite` дают EPERM на Windows (как у
  `e2e/vite.config.ts`).
- `allowBuilds: msw: false` (`pnpm-workspace.yaml`) — postinstall msw воркер не копирует; брать файл из пакета.
- Фикстуры для браузера не должны тянуть `vitest`/`@testing-library`/`node:*`: `src/test/{app,render,setup}.tsx` —
  не импортировать из `src/shots`.
- Остановка Vite на Windows — дерево процессов (`taskkill /T /F` по PID или запуск с отдельной группой процессов);
  брошенный Vite держит порт 5176.
- `worker.start()` — до `createRoot().render()`, иначе первые запросы me уйдут мимо моков.

**Документы.** `CLAUDE.md` «Проверки» — UF1a.4.

### UF1a.2: пакет `@wf/icons` — выгрузка из артборда и дорисованные

**Цель.** Данные иконок и иллюстраций — один пакет для веба и админки: выгрузка из
`design/screens/14-Иконки.dc.html` (результат коммитится, правка руками запрещена — страж), дорисованные — отдельный
файл вне стража выгрузки (спека §4.9, стр. 732–751).

**Риск:** B.

**Файлы.** Создать: `packages/icons/{package.json,icons.json,icons.extra.json,icons.ts,tsconfig.json}`,
`packages/icons/test/icons.test.mjs`, `design/tools/extract-icons.mjs`, `design/tools/extract-icons.test.mjs`.
Изменить: `apps/admin/package.json` (`"@wf/icons": "workspace:*"`), `pnpm-lock.yaml` (через `pnpm install`), корневой
`Taskfile.yml` (`packages:ci` — фильтр `@wf/icons`; `gen` — шаг выгрузки иконок), `.github/workflows/packages.yml`
(пути, если `packages/**` там нет — сверить).

**Интерфейсы.**
- `icons.json` (генерируется): `{ "<имя>": { "viewBox": "0 0 48 48", "layers": [{ "d": "…", "role": IconRole,
  "paint": "stroke" | "fill", "width"?: число, "opacity"?: число }] } }`, ключи по алфавиту, числа — стабильным
  форматом (без `2.0999999999999996`: округление до 2 знаков). Плюс служебный ключ `"$optical"` — **множитель** толщины
  контура на размер из «лесенки» мяча (одинаковый для всех слоёв: 20 px — ×1.25, 24 px — ×1.1667 к мастер-ширинам
  2.4/1.68; S2 ревью); служебные ключи на `$` в `IconName` не входят.
- **Роли слоёв** (I4): `type IconRole = 'object' | 'detail' | 'accent' | 'accent-detail' | 'knockout'`. Таблица «цвет
  артборда → роль» в скрипте, исчерпывающая: `#C3D0C7` → `object` (предмет); `#7E8C83` → `detail` («внутренняя линия»
  шапки артборда, 1.5); `#5E6B64` → `detail` с `opacity` (приглушённая линия; коэффициент — так, чтобы на `bg` тёмной
  темы цвет совпал, значение — в отчёт); `#C6F24E` → `accent`; `#8FA83A` → `accent-detail` (тень акцента);
  `#0B0F0D` → `knockout` (вырез по фону: бейджи «уведомлений» и «фильтра», пятиугольник «поиска», рубль «аренды»);
  атрибут `opacity` слоя переносится как есть. **Незнакомый цвет — ошибка выгрузки** (как незнакомая подпись).
  У `knockout` дополнительно `on: 'surface' | 'accent'` — вырез поверх акцентного слоя (рубль «аренды») рисуется
  `text-inverse`, остальные — цветом поверхности (U25).
- `icons.extra.json` (руками): группы `nav` (перенос `NAV_ICONS`, источник — макет 08, viewBox 24) и `drawn`
  (дорисованные *: `monitor`, `sun`, `moon`, `home`, `copy`, `check`, `info`, `keyboard`, `columns`, `undo`,
  `calendar`, `chevron-down`; `search` — только если контур «поиск» артборда не годится к 24) — U6.
- `icons.ts`: `export type IconName`, `export type IconLayer`, `export type IconDef = { viewBox: string; layers:
  readonly IconLayer[] }`, `export const ICONS: Record<IconName, IconDef>` (слияние трёх групп), `export const
  OPTICAL` (из `$optical`). React-компонента в пакете нет.
- `extract-icons.mjs` (`node design/tools/extract-icons.mjs`): читает артборд, находит именованные иконки (подпись под
  `<svg>`), русская подпись → английский ключ по таблице в скрипте (незнакомая подпись — ошибка, не пропуск);
  `sc-camel-view-box` → `viewBox`; примитивы `rect`, `circle`, `ellipse`, `line`, `polyline`, `polygon`, `path` →
  `d`; атрибуты `stroke`/`fill`/`stroke-width`/`opacity` наследуются от `<g>`; роль — по таблице цветов выше;
  `fill="none"` + `stroke` → `paint: 'stroke'`. Экспорт `extractIcons(html) → объект`.

**Тесты:**
- `extract-icons.test.mjs` (`design:test`): фикстура-фрагмент с `rect`, `circle`, `polygon`, `<g stroke>` → слои с
  `d`, роли по цвету, `paint`, `width` и `opacity` по наследованию; фикстура с `#0B0F0D` и `#7E8C83` → роли
  `knockout`/`detail`; вырез после акцентного слоя → `on: 'accent'`; незнакомый цвет `#123456` → ошибка с цветом и
  именем иконки; незнакомая подпись → ошибка с её текстом; выгрузка настоящего артборда совпадает с закоммиченным
  `icons.json` (страж «руками не правится»).
- `packages/icons/test/icons.test.mjs` (`packages:ci`): ключи `icons.json` и групп `icons.extra.json` не пересекаются;
  у каждого слоя непустой `d`, `role` и `paint` из перечня; каждый ключ группы `drawn` упомянут строкой «дорисовано *»
  в `design/README.md`; `$optical` содержит размеры 16, 20, 24, 48.

**Подсадка.** Правка числа в `icons.json` руками — страж совпадения краснеет; тот же ключ в `drawn` и `icons.json` —
страж пересечения краснеет; удалить строку `circle` из конвертера — тест фикстуры краснеет; «незнакомый цвет → object»
вместо ошибки — тест `#123456` краснеет.

**`pnpm install`** — один раз после создания пакета и зависимости админки, по сигналу контроллера (U2).

**Приёмка.** 32 именованные иконки артборда (`<svg width="46">`: мяч … настройки, аренда, форма) в `icons.json`;
подписи-ключи — английские; дорисованные — в той же стилистике (контур 2.4, внутренняя линия 1.5 на сетке 48, один
акцент); `packages:ci` знает пакет; лист всех иконок в `admin:shots` (история `Icon` «все иконки», обе темы) — вырезы
и детали различимы.

**Ловушки.** Артборд содержит «лесенку» размеров одной иконки (16–48) с разной толщиной контура — это не отдельные
иконки, а источник `$optical`; блок «кейлайны» — не иконка; три образца по 40 px («Три иконки держат весь набор…»:
мяч, ворота, повязка) с абзацем текста под последним — не иконки (n1). Критерий выборки — только `<svg width="46">`
с подписью из таблицы скрипта (он же критерий приёмки «32»); прочие `<svg>` выгрузка пропускает молча, а
`<svg width="46">` с незнакомой подписью — ошибка. Порядок слоёв сохраняется (акцент поверх предмета).
Пакет не импортирует приложения (boundaries); JSON в TS — `resolveJsonModule` (сверить с `@wf/config/tsconfig`).

**Документы.** `design/README.md` — дорисованные иконки (UF1a.4).

### UF1a.3: `Icon` и `Illustration` в админке, перенос `NAV_ICONS`

**Цель.** Рендер иконок из `@wf/icons` в админке; меню переходит на пакет без изменения вида (спека §4.9).

**Риск:** C.

**Файлы.** Создать: `src/ui/icon/{Icon.tsx,Illustration.tsx,Icon.module.css,icon.test.tsx,Icon.stories.tsx}`,
`src/showcase/csf.ts` (U12: владелец формы — эта задача, m16). Изменить: `src/ui/shell/{AppShell.tsx,types.ts,
types.test.ts}` (`NavIcon` → `IconName`), `src/app/nav.ts`, `src/ui/index.ts`. Удалить: `src/ui/shell/icons.ts`.

**Интерфейсы.**
- `Icon({ name: IconName; size?: 16 | 20 | 24; label?: string; className?: string })` — без `label`:
  `aria-hidden="true"` + `focusable="false"`; с `label`: `role="img"` + `aria-label`. Толщина контура — атрибутом
  `stroke-width` (презентационный атрибут SVG, не стиль): для иконок артборда — `layer.width × OPTICAL[size]`; для
  группы `nav` — **абсолютная** ширина слоя без множителя (сейчас 2.2 из `AppShell.module.css`, m1) — вид меню не
  меняется.
- `Illustration({ name: IconName; size: 48 | 64 })` — всегда `aria-hidden`.
- Цвет — классами CSS Module (U7): у `Icon` слой `object` — `currentColor` (наследует цвет пункта меню, вид не
  меняется), `detail` — `text-subtle`, `accent` и `accent-detail` — `--wf-color-accent` (в UF1b → `accent-ink`;
  `accent-detail` — с прозрачностью класса), `knockout` — `bg-elevated` (`on: 'accent'` — `text-inverse`), `opacity`
  слоя — атрибутом; у `Illustration` `object` — `text-muted`. `NavItem.icon: IconName`.
- `src/showcase/csf.ts` — типы CSF (`StoryMeta`, `Story<P>`, интерфейс — UF3.2); форма замораживается этой задачей:
  UF3 её не меняет, пока UF2a/UF2b пишут истории (m16).

**Тесты:** `icon.test.tsx` → без `label` нет в дереве доступности; с `label` — `getByRole('img', { name })`; слой
`stroke` — `fill="none"`; `Icon name="log"` рисует тот же `d`, что прежний `NAV_ICONS.log` (значение — в тесте), и
`stroke-width` 2.2 на размерах 20 и 24 (без `OPTICAL`); иконка артборда на 20 — ширина × 1.25; слой `knockout` — класс
поверхности, `on: 'accent'` — класс `text-inverse`; `types.test.ts` → иконка пункта меню типа `IconName`.

**Подсадка.** `Icon` с `label` без `role="img"` — тест имени краснеет; множитель `OPTICAL` и у `nav` — тест ширины
краснеет.

**Приёмка.** Снимки `admin:shots` меню до и после (1440, 390) — без отличий (пути — в отчёте).

**Ловушки.** Атрибутов `style` нет (CSP); `fill`/`stroke` цветом — только через классы, не атрибутами с hex
(`csp.test.ts` ищет hex в CSS, но цвет в атрибутах обходил бы токены — запрещено правилом).

### UF1a.4: документы UF1a

**Цель.** Строки §6.4 для UF1a.

**Риск:** C.

**Файлы и содержание.**
- `CLAUDE.md` «Проверки»: `./task admin:shots -- <пути> --out <каталог>` — снимки для ревьюера, Windows годится,
  каталог — `.superpowers/sdd/<сессия>/shots/…`, удаляются после ревью.
- `.claude/agents/wf-implementer.md`: фронтовая задача в конце снимает свои экраны `admin:shots` (три ширины, обе
  темы), путь — в отчёте; снимки не коммитятся. `.claude/agents/wf-reviewer.md`: ревью фронта читает PNG и
  проверяет принципы 1, 4, 7, 9 по картинкам; снимки удаляются после ревью.
- `.claude/rules/frontend.md` «Устройство»: иконки и иллюстрации — только `@wf/icons` (+ `icons.extra.json`,
  дорисованное — с пометкой в `design/README.md`).
- `.claude/rules/admin.md`: `Icon`/`Illustration`; «Тесты» — режим `shots` (моки MSW в браузере, `src/test/base.ts`,
  алиас через плагин).
- `design/README.md`: раздел «Иконки: дорисованные *» (список `drawn`).

**Приёмка.** Документы совпадают с кодом группы.

## Группа UF1b «Токены и темы»

Спека §7 (стр. 1240), §2, §4.12, §6.1. Риск: A — тема до первого кадра под CSP и прод-CSS (UF1b.4, UF1b.5, UF1b.7),
остальное B. Деление на два захода (U1): **UF1b/I** — UF1b.1–UF1b.4, **UF1b/II** — UF1b.5–UF1b.8; ревью — после
каждого захода. **Приёмка палитры — снимки обеих тем пользователю до слияния группы** (решение Б; UF1b.8).

### UF1b.1: снимки «до» (первый шаг, до правки CSS)

**Цель.** Точка отсчёта «до/после» перекраски (спека §5.4, m-a): снимать «до» со старого коммита потом нельзя
(временные копии проекта запрещены).

**Риск:** C.

**Что сделать.** `./task admin:shots -- --out .superpowers/sdd/<сессия>/shots/UF1b-before --themes dark` на путях:
`/login` (роль `anonymous`), `/audit`, `/approvals`, `/staff`, `/me`, `/geo/cities`, `/geo/cities/<id фикстуры
cityDetails>`, `/setup-2fa` (роль `setup`); три ширины. Ничего не коммитится; путь — в отчёте. Снимки живут до ревью
UF1b.

### UF1b.2: `tokens.json` — пары, новые токены, генератор, страж контраста

**Цель.** Формат `tokens.json` §2.2, значения §2.3–§2.5 и §2.7, генерация §2.8, страж контраста §2.6 (1).

**Риск:** B.

**Файлы.** Изменить: `contracts/tokens/tokens.json`, `packages/tokens/scripts/build.mjs`, сгенерированные
`packages/tokens/{tokens.css,tokens.ts}`, `packages/tokens/test/build.test.mjs`. Создать:
`packages/tokens/scripts/contrast.mjs`, `packages/tokens/test/contrast.test.mjs`.

**Интерфейсы.**
- `tokens.json`: цвета — строка или `{ "dark", "light" }` (пары §2.5, новые §2.3: `accent-ink`, `focus`,
  `danger-fill`, `text-on-danger`, `border-control`, `scrim`, `qr-ink`, `qr-paper`), `shadow.overlay`/`shadow.toast`
  данными, `ease-exit`, `container` (`narrow` 480, `wide` 720), `density` (36/48), `contrast` (строки таблицы §2.6 с
  нормой), `breakpoint.compact` 1024, `size`: `sidebar` 236, `rail` 64, `palette` 640, `tabbar`, `table-header`,
  `toast` — числа снять с текущих CSS (`AppShell.module.css`, `DataTable.module.css`, `Toast.module.css`: высота
  однострочного тоста с кнопкой) и записать в отчёт, откуда взяты. Прежние имена и тёмные значения не меняются (P21).
- `buildCss(t)`: `:root { color-scheme: dark; … }`, `:root[data-theme='light'] { color-scheme: light }`,
  `:root[data-theme='system'] { color-scheme: light dark }`; пара → `light-dark(<light>, <dark>)` (сначала светлое!),
  строка → как есть; тень → `--wf-shadow-<имя>: 0 <y>px <blur>px light-dark(<light>, <dark>)`; `--wf-ease-exit`;
  `--wf-container-*`, `--wf-density-*-row` — px; блок `@media (prefers-reduced-motion: reduce) { :root {
  --wf-duration-*: 0ms } }`.
- `buildTs(t)`: тот же JSON `as const` + `export type ThemeName = 'light' | 'dark'`.
- `contrast.mjs`: `relativeLuminance(hex)`, `contrastRatio(a, b)` (формула WCAG), `colorOf(tokens, name, theme)`
  (строка — одна на обе темы). Общий модуль для стража токенов и стража CSS админки (UF1b.3).

**Тесты:**
- `build.test.mjs` → `--wf-color-bg: light-dark(#F3F6F3, #0B0F0D);`; `--wf-color-accent: #C6F24E;` (строка без
  `light-dark`); три правила `color-scheme`; блок reduced motion обнуляет все `--wf-duration-*`; у каждой
  `--wf-shadow-*` за `light-dark(` следует `#`; `--wf-ease-exit`; P21 переписан: тёмные значения всех прежних цветов —
  второй аргумент `light-dark(…)` и равны прежним (список прежних значений — в тесте); закоммиченные файлы совпадают с
  генерацией.
- `contrast.test.mjs` → каждый цвет — строка или объект с обоими ключами `#RRGGBB[AA]`; каждая строка `contrast` ≥
  нормы в обеих темах; выборочные числа §2.6 (`text-subtle`/`surface-sunken` тёмная 5.33 ± 0.01; `accent-ink`/`pitch`
  светлая 5.44 ± 0.01); пары `text-subtle` × `chip-bg` в `contrast` нет.

**Подсадка.** Светлый `text-subtle` `#7A8A7E` — `contrast.test` краснеет (§2.6); генератор пишет `light-dark(dark,
light)` — `build.test` краснеет; тень строкой `light-dark(0 24px …)` — страж тени краснеет.

**Приёмка.** Генерация — `node packages/tokens/scripts/build.mjs`; тесты — `node --test
"packages/tokens/test/*.test.mjs"` (без pnpm). Веб (`apps/web`) атрибут темы не ставит и остаётся тёмным
(`:root { color-scheme: dark }`) — `apps/web` не правится; сборку веба не гонять (ответ S1: полифил Next работает, веб тёмный; запись — UF1b.8).

**Ловушки.** Цвета с альфой (`scrim`, тени) в `contrast` не входят (смесь с фоном — не цвет текста). `light-dark()`
принимает только цвета — геометрия тени снаружи. Формат `tokens.json` — контракт с wf-native (Flutter читает ARGB из
`#RRGGBBAA`) — описать в `design/03-токены.md` (UF1b.8).

### UF1b.3: CSS админки по ролям, стражи контраста по CSS, forced-colors

**Цель.** Перевод всех CSS Modules на роли (спека §6.1 строка 1, §2.3) и страж пар из CSS (§2.6 (2), (3)); уборка V1
m4 (forced-colors).

**Риск:** B.

**Файлы.** Изменить: `src/ui/**/*.module.css`, `src/features/**/*.module.css` (по таблице §2.3: 13 `color`, 4
`border-color`, 1 `border-bottom-color`, `caret-color` → `accent-ink`; 12 `outline` → `focus`; рамки полей →
`border-control`; `Overlay .backdrop` → `scrim`, тени `Overlay`/`Toast` → `shadow-*` вместо `color-mix(…)`;
`Button .danger` → `danger-fill`/`text-on-danger`; `QrCode` → `qr-ink`/`qr-paper`; подписи на чипах →
`text-muted`; плейсхолдеры `Field`, `DataTable` → `text-subtle`). Создать: `src/ui/contrast-css.test.ts`.

**Интерфейсы.**
- Выбранный сегмент `Segmented` и включённый `Switch` — рамка `accent-ink` в обеих темах (U8: в тёмной она совпадает с
  заливкой и не видна; в светлой даёт 3:1 к белому).
- `@media (forced-colors: active)`: `Tabs .list` — `border-bottom: 1px solid CanvasText`; выбранный сегмент —
  `border-color: Highlight` (системные цвета — не hex, `csp.test.ts` не задет).
- `contrast-css.test.ts`: читает все `*.module.css`; правило с `background: var(--wf-color-X)` и `color`/`stroke`/
  `border-color: var(--wf-color-Y)` даёт пару (Y, X); у каждой пары — строка `contrast` в `tokens.json`, иначе падение
  со списком; текст без своего фона — ручной список «текстовый токен × `bg`, `bg-elevated`, `surface-sunken`,
  `chip-bg`»; `::placeholder` — только `text-subtle`; класс с фоном `chip-bg` не берёт `text-subtle`. Расчёт — через
  `packages/tokens/scripts/contrast.mjs`.

**Тесты:** `contrast-css.test.ts` › «пары из CSS Modules есть в contrast», «плейсхолдеры — text-subtle», «на chip-bg
нет text-subtle», «стражу есть что читать» (список известных файлов, как в `csp.test.ts`); `layout-css.test.ts` ›
«forced-colors: у вкладок есть линия, у выбранного сегмента — рамка».

**Подсадка.** `StatusChip .neutral` → `color: var(--wf-color-text-faint)` — страж пар краснеет (I9);
`::placeholder` → `text-faint` — страж плейсхолдеров краснеет; подпись чипа → `text-subtle` — страж чипов краснеет.

**Приёмка.** В CSS нет `accent` текстом/линией и `outline: … accent`; `color-mix` с `--wf-color-bg` для теней не
осталось; снимки после (тёмная тема, те же пути, что UF1b.1) отличаются от «до» только рамками полей (`border-control`)
— путь сравнения в отчёте.

**Ловушки.** Фон и цвет в разных правилах одного класса (`.x` и `.x:hover`, `.x[aria-pressed='true']`) — сводить по
базовому имени класса; ограничение стража (вложенность, текст на фоне родителя) — словами в комментарии стража.
`layout-css.test.ts` › «карточка сотрудника…» меняется в UF1b.6, не здесь.

### UF1b.4: `build.cssTarget` — минимальные браузеры админки

**Цель.** Прод-CSS = dev-CSS: Lightning CSS не заменяет `light-dark()` полифилом (спека §2.1, стр. 89–95, n1).

**Риск:** A (вместе с UF1b.7). Режимы отказа: цель ниже Safari 17.5/iOS 17.5 → полифил `--lightningcss-*` в проде,
который ни один страж dev не видит; `e2e/vite.config.ts` (merge) теряет цель; веб задет целью админки (не должен).

**Файлы.** Изменить: `vite.config.ts`, `vite.config.test.ts`, `Taskfile.yml` админки (`ci`: шаг проверки после
существующего `build`). Создать: `scripts/check-admin-dist.mjs`, `scripts/check-admin-dist.test.mjs`.

**Интерфейсы.**
- `build.cssTarget: ['chrome123', 'firefox125', 'safari17.5', 'ios17.5']`.
- `scripts/check-admin-dist.mjs [<каталог>]` (по умолчанию `apps/admin/dist` — путь от расположения скрипта, не от
  рабочего каталога: `admin:ci` идёт из `apps/admin`; n2; m5, S5): ищет только в `*.js`, `*.html`, `*.css` (двоичные
  `woff2` `@fontsource` не читаются — подстрока `msw` нашлась бы в них случайно) и только точные метки
  `mockServiceWorker`, `msw/browser`, `setupWorker`; в CSS есть `light-dark(` и нет `--lightningcss-`; код выхода ≠ 0 со
  списком нарушений.
  Вызывается в `admin:ci` сразу после существующего `build` (отдельной сборки нет — работает в `./task ci` и в job
  `admin` CI) и в `scripts/e2e-admin.sh` после сборки прод-проекта (UF1b.7).

**Тесты:** `vite.config.test.ts` › «цели CSS сборки — минимальные браузеры админки» → ровно этот список; конфиг e2e
(`e2e/vite.config.ts`, `mergeConfig`) — тот же список. `check-admin-dist.test.mjs` (`scripts:test`) › каталог-фикстура
с `--lightningcss-light` → ошибка; с `mockServiceWorker` в js → ошибка; чистый → 0; `woff2` с байтами `msw` и js со
словом `msw` вне меток → 0 (нет ложного срабатывания); запуск из другого каталога — тот же `dist`.

**Подсадка.** Убрать `ios17.5` — тест конфига краснеет; фикстура с полифилом — проверка `dist` краснеет. Сценарий (3)
прод-e2e полифил **не** ловит (полифил работает, фон тот же) — поведение прод-CSS стережёт проверка `dist` (m5).

**Документы.** `admin.md` — «Минимальные браузеры: Chrome 123, Firefox 125 (Popover API), Safari/iOS 17.5» (UF1b.8).

### UF1b.5: тема до первого кадра — `theme-init.js`, `ThemeProvider`, `useThemeColors`

**Цель.** Тема применяется до отрисовки без инлайн-скрипта; провайдер, синхронизация вкладок, цвета темы для JS
(спека §4.12, стр. 836–888; §2.8 `useThemeColors`).

**Риск:** A. Режимы отказа (таблица обязательна): storage бросает (приватный режим) или переполнен; в storage мусор
или пусто; другая вкладка сменила тему (событие `storage`); системная тема сменилась при `system`; скрипт не
загрузился или заблокирован CSP (статический `data-theme="system"` работает без JS); старая версия `theme-init.js` из
кэша (`no-cache` — сверка); провайдер при монтировании расходится со скриптом (источник истины — атрибут, который
поставил скрипт); смена сотрудника и выход — тема не личная и не стирается (U19); `matchMedia` отсутствует (jsdom).

**Файлы.** Создать: `public/theme-init.js`, `src/ui/theme/{ThemeProvider.tsx,useThemeColors.ts,theme.test.tsx,
theme-init.test.ts}`. Изменить: `index.html` (`<html lang="ru" data-theme="system">`, `<script
src="/theme-init.js"></script>` в `<head>` до стилей, две `<meta name="theme-color" media="(prefers-color-scheme:
…)">`), `src/app/providers.tsx` (`ThemeProvider` в `AppProviders`), `src/ui/index.ts`,
`backend/internal/httpapi/admin/static_test.go` (случай `/theme-init.js`).

**Интерфейсы.**
- `type ThemePref = 'system' | 'light' | 'dark'`; storage `localStorage['wf.theme']`.
- `theme-init.js` — обычный (не модульный) скрипт: `window.__wfTheme = { parse(raw: unknown): ThemePref }` (незнакомое
  и пустое → `system`), читает storage в `try/catch`, ставит `document.documentElement.dataset.theme`.
- `ThemeProvider({ children })`; `useTheme(): { theme: ThemePref; resolved: 'light' | 'dark'; setTheme(t: ThemePref):
  void }` — начальное значение из `dataset.theme`; `resolved` — по `matchMedia('(prefers-color-scheme: dark)')` с
  подпиской (без `matchMedia` — `dark`); `setTheme` пишет storage (`try/catch`) и `dataset.theme`; событие `storage`
  по `wf.theme` применяется; при явном выборе — `content` **обеих** `meta[name=theme-color]` (у каждой свой `media`)
  из `useThemeColors().bg`; при возврате к `system` — исходные значения из `index.html` (запомнить при монтировании,
  m21).
- `useThemeColors(): Record<ColorName, string>` — `tokens.color[имя][resolved]` или строка; без DOM и
  `getComputedStyle`.
- Go: случай теста статики `/theme-init.js` → `Cache-Control: no-cache` (уже `cacheOther` в `static.go`) и заголовок
  CSP — тот же `staticCSP`.

**Тесты:**
- `theme.test.tsx` › «по умолчанию system»; «выбор пишет storage и атрибут»; «storage бросает — тема меняется, ошибки
  нет»; «событие storage другой вкладки применяется»; «system + тёмная система → resolved dark, смена системной → light»;
  «после перезагрузки» (размонтировать, исполнить `theme-init`, смонтировать) → выбранная тема; `useThemeColors` при
  `resolved = light` → `bg` = `#F3F6F3`, не строка с `light-dark`; «theme-color»: явный `dark` → обе метки тёмный
  `bg`, возврат к `system` → исходные `content` меток.
- `theme-init.test.ts` → читает текст `public/theme-init.js`, исполняет в jsdom: `parse('dark')` → `dark`,
  `parse('x')`/`parse(null)` → `system`; storage `light` → `data-theme="light"`; storage бросает → `system`.
- `static_test.go` › `/theme-init.js` → 200, `no-cache`, CSP (`go test ./internal/httpapi/admin/ -run TestStatic
  -count=1 -timeout 300s` из `backend/`).

**Подсадка.** `setTheme` без записи в storage — «после перезагрузки» краснеет; `theme-init.js` без `try/catch` —
«storage бросает» краснеет.

**Приёмка.** Запись темы — `dataset`, не `element.style` (`csp.test.ts` зелёный без исключений); скрипт — файл
`public/`, не инлайн; смена темы мгновенная, без анимации.

**Ловушки.** Путь скрипта `/theme-init.js` (базового пути у админки нет); Vite копирует `public/` как есть — файл не
проходит сборку, поэтому синтаксис — тот, что понимают минимальные браузеры. `csp.test.ts` читает только `src` —
`public/theme-init.js` под ним не стоит, его страж — `theme-init.test.ts` и прод-e2e.

### UF1b.6: `ThemeSwitch`, карточка сотрудника, «Ещё», экраны входа

**Цель.** Переключатель темы в трёх местах; карточка сотрудника — язык, тема, «Выйти» своими строками (ревью V1
m3; спека §4.12 «ThemeSwitch», стр. 878–892).

**Риск:** B.

**Файлы.** Создать: `src/ui/theme/ThemeSwitch.tsx` (+ тест). Изменить: `src/ui/shell/{AppShell.tsx,
AppShell.module.css,AppShell.test.tsx}`, `src/features/auth/{AuthLayout.tsx,Auth.module.css}`,
`src/ui/layout-css.test.ts` (тест карточки), `messages/{ru,en}.json` (`ui.theme.{label,system,light,dark}`).

**Интерфейсы.** `ThemeSwitch()` — `Segmented` с `label` «Тема», три сегмента: `Icon` (`monitor`/`sun`/`moon` из
`drawn`) + `name` «Система»/«Светлая»/«Тёмная» + `title` (для мыши); `aria-pressed` по `theme`. Карточка: язык —
строкой, тема — строкой (или одной, если по снимкам EN помещаются), «Выйти» — последней строкой во всю ширину.

**Тесты:** `ThemeSwitch.test.tsx` › три кнопки с именами, нажатая — `aria-pressed`, клик → `setTheme`;
`AppShell.test` › тема в карточке и в шторке «Ещё»; `AuthLayout` (через `LoginPage.test`) › переключатель рядом с
языком; `layout-css.test.ts` › «Выйти» — своя строка во всю ширину карточки.

**Подсадка.** Убрать `ThemeSwitch` из шторки «Ещё» — тест шторки краснеет.

**Приёмка.** Снимки карточки EN на 1440/768/390 (обе темы) — «Sign out» не вылезает (V1 m3 закрыт глазами).

### UF1b.7: прод-проект живого e2e — собранная админка под настоящей CSP

**Цель.** Проверка того, что dev не видит: `theme-init.js` до первого кадра, отсутствие нарушений CSP при обходе
разделов, фон `body` по теме на собранной админке (спека §4.12, стр. 860–870; I3).

**Риск:** A. Режимы отказа: сборка устарела (собрана не из текущего дерева); слушатель `securitypolicyviolation`
поставлен после скриптов страницы (ранние нарушения потеряны); сравнение цветов `rgb(…)` против hex; глубокая ссылка
не отдаёт `index.html`; кука `__Host-` на http; занятые порты; прод-проект зависит от состояния `staff.spec.ts`
(не должен); умолчание эмуляции Playwright (`colorScheme: 'light'`) совпадает с ожидаемым и делает проверку темы
немой (I3).

**Файлы.** Изменить: `playwright.config.ts`, `e2e/{env.ts,env.test.ts}`, `scripts/e2e-admin.sh`,
`.github/workflows/admin.yml` (job `e2e`: `WF_E2E_PROJECTS: chromium,prod`). Создать: `e2e/prod.spec.ts`.

**Интерфейсы** (U9).
- Проект `prod`: своя пара — admin-api на порту 18183 с `ADMIN_STATIC_DIR=<корень>/apps/admin/dist` и
  `ADMIN_COOKIE_SECURE=true`, без Vite; `origin(prod)` = адрес admin-api; `enabledProjects` знает `prod` и **по
  умолчанию** (без `WF_E2E_PROJECTS`) возвращает `chromium, webkit, prod` — иначе локальный `./task ci` прод-проект не
  гоняет (m20; решение пользователя по Q2); порт 18183 — в проверку занятых портов `e2e-admin.sh`.
- `scripts/e2e-admin.sh` сам задаёт умолчание проектов и экспортирует его (сейчас `chromium,webkit`, стр. 43 и 138) —
  умолчание скрипта тоже `chromium,webkit,prod` (иначе под `./task admin:e2e` умолчание `env.ts` не применяется;
  m20, раунд 2); тест `env.test.ts` сверяет оба умолчания (скрипт читается текстом). Подсказку «без WebKit локально»
  в `admin.md` и `CLAUDE.md` заменить на `WF_E2E_PROJECTS=chromium,prod ./task ci` (UF1b.8).
- `scripts/e2e-admin.sh`: если в проектах есть `prod` — до Playwright `apps/admin/node_modules/.bin/vite build`
  (без `tsc`; сборка — часть стенда e2e), затем `node scripts/check-admin-dist.mjs` (UF1b.4; вторая линия стража
  UF1a.1, U4).
- `e2e/prod.spec.ts` (serial, свой сотрудник через `bootstrap` + `acceptAndSetup`, как `stale-cookie.spec.ts`):
  (1) явный выбор **против** системы (I3): `wf.theme = light` (`addInitScript`) при `emulateMedia({ colorScheme:
  'dark' })` → `/login`: фон `body` = светлый `bg`; `wf.theme = dark` при `colorScheme: 'light'` → тёмный; `system`
  при `dark` → тёмный, при `light` → светлый; (2) под сотрудником обход всех разделов `NAV` + открытие панели
  приглашения: слушатель `securitypolicyviolation` (`addInitScript`, до скриптов страницы) копит нарушения — список
  пуст, иначе — в отчёте теста; (3) фон `body` = `tokens.color.bg[тема]` (из `@wf/tokens`) для обеих тем.

**Тесты:** `env.test.ts` › проект `prod` — порт 18183, `ADMIN_STATIC_DIR` задан только ему; без `WF_E2E_PROJECTS` —
три проекта, включая `prod`; `prod.spec.ts` (выше). Прогон: `./task admin:e2e` (проекты по умолчанию) — один раз.

**Подсадка** (прогон только `WF_E2E_PROJECTS=prod`): `setAttribute('style', 'color: red')` в эффекте компонента
каркаса → (2) краснеет; `theme-init.js` убран из `index.html` → (1) краснеет на случае «`light` при тёмной системе»
(статический `data-theme="system"` дал бы тёмный фон). Подсадка `element.style.color = …` вакуумна (CSSOM CSP не
режет, R2-3) — не использовать.

**Приёмка.** Нарушений CSP — ноль; время job `e2e` в CI выросло не больше чем на ~2 мин (решение пользователя Q2);
UF2a и UF2b дописывают в сценарий (2) открытие своих всплывающих (`Select`, подсказка, виртуальная таблица).

**Ловушки.** Playwright стартует `webServer` до `globalSetup` — сборка только в `e2e-admin.sh`; `vite build` здесь —
шаг стенда, не «сборка по просьбе»; SPA-маршруты — `static.go` отдаёт `index.html` для путей без файла (сверить
глубокую ссылку `/staff`).

### UF1b.8: приёмка палитры снимками и документы UF1b

**Цель.** Решение Б (палитру показать снимками) и строки §6.4 для UF1b.

**Риск:** C.

**Что сделать.**
- Снимки обеих тем тех же путей, что UF1b.1, + карточка EN → `.superpowers/sdd/<сессия>/shots/UF1b-after/`;
  контроллер показывает пользователю до слияния группы. Правка значений после показа — только числа в `tokens.json`
  (страж контраста перепроверит).
- `design/03-токены.md`: пары, роли «чернила/заливка», новые токены, `#RRGGBBAA`, тени данными, таблица контраста
  ссылкой на §2.6, плейсхолдеры — `text-subtle`, строка про `text-faint` — только значки, на чипах — `text-muted`,
  `density`, `ease-exit`, `breakpoint.compact`, `container.*`, формат `tokens.json` для Flutter; **веб** (ответ на S1):
  Next 16 (Turbopack, Lightning CSS, цели по умолчанию Chrome 111 / Safari 16.4) полифилит `light-dark()` и в dev, и в
  сборке — полифил работает, потому что `color-scheme` объявлен в том же `tokens.css`, веб остаётся тёмным; при
  переводе веба на две темы — свои цели браузеров веба или осознанный полифил.
- `.claude/rules/frontend.md` «Устройство»: темы (`data-theme`, `light-dark()`, `accent-ink` для текста,
  `cssTarget`), ссылка на спеку в «Принципах».
- `.claude/rules/admin.md`: минимальные браузеры и проверка `dist` в `admin:ci`; «Токены» — строка переписывается
  (плейсхолдеры `text-subtle`, `text-faint` только значки, на чипах `text-muted`, `focus`, `border-control`);
  `ThemeProvider`/`ThemeSwitch`; живой e2e — проект `prod` в наборе по умолчанию, **локальный `./task admin:e2e`
  теперь собирает админку** (шаг стенда; m20, решение пользователя Q2).
- `CLAUDE.md` «Проверки»: `./task admin:e2e` собирает админку для проекта `prod` (по умолчанию в наборе:
  `chromium,webkit,prod`); без WebKit локально — `WF_E2E_PROJECTS=chromium,prod` (не `chromium`); то же в `admin.md`.
- `design/README.md`: светлая тема — «дорисовано *».
- Спека сотрудников §9.1, строка «Тема» — «две темы, по умолчанию системная (спека UI-фундамента §2, §4.12)» (U14).

**Приёмка.** Документ, расходящийся с кодом, — замечание ревью.

## Группа UF2a «Каркас, состояния, формы»

Спека §7 (стр. 1241), §3, §4.3–§4.4, §4.7–§4.8, §4.16–§4.17, §6.1 (строки UF2a), §6.3. Риск: B; `Popover` в стеке
оверлеев — A. Деление на три захода (U1; решение пользователя по Q1, m15): **UF2a/I** — UF2a.1–UF2a.5 (каркас и
всплывающее), **UF2a/II** — UF2a.6–UF2a.9 (формы, состояния, время и словарь), **UF2a/III** — UF2a.10–UF2a.13 (экраны,
уборка, документы, план B); ревью — после каждого. Параллельно с UF3; общие файлы — по таблице владения («Порядок
исполнения», I8). Каждый новый компонент `src/ui` — с историями CSF (U12) в той же задаче.

### UF2a.1: медиа-хуки, открытые разделы, `ActionGate` по устройству

**Цель.** Все разделы открыты на любой ширине; «только с компьютера» — действия по реестру и по устройству, а не
ширине (спека §3.1–§3.2, стр. 269–326).

**Риск:** B (ограничение интерфейса; решает сервер).

**Файлы.** Создать: `src/app/desktopOnly.ts`, `src/ui/gate/{ActionGate.tsx,ActionGate.module.css}`,
`src/test/media.ts`, `src/app/desktopOnly.test.tsx`. Изменить: `src/ui/media.ts`, `src/ui/states/states.tsx`
(`DesktopOnlyNotice`), `src/app/nav.ts` (+ `nav.test.ts`), `src/app/router.test.tsx`, `src/ui/shell/AppShell.tsx`
(таббар), `src/features/staff/{StaffDrawer,StaffPage,StaffList,InviteDrawer,RolesMatrix}.tsx`,
`src/ui/table/{DataTable.tsx,DataTable.module.css}` (проп `layout`), `src/features/approvals/ApprovalsList.tsx`,
`src/ui/index.ts`, `messages/{ru,en}.json`.

**Интерфейсы.**
- `src/ui/media.ts`: `useIsMobile()` (как есть), `useIsCompact()` — `< breakpoint.compact`, `useIsHandheld()` —
  `(pointer: coarse)` **и** `< breakpoint.compact`; без `matchMedia` — `false` (широкий компьютер).
- `src/app/desktopOnly.ts`: `DESKTOP_ONLY_PERMISSIONS: ReadonlySet<string>` — ровно 7 прав §3.2;
  `isDesktopOnly(permission: string | readonly string[]): boolean`; `isDesktopOnlyDecision(action: string): boolean` —
  `action.startsWith('staff.')` (незнакомое `staff.*` — закрыто по умолчанию).
- `ActionGate({ permission: string | readonly string[]; children: ReactNode; compact?: boolean })` — компьютер:
  `children`; карманное устройство и хоть одно право в реестре — `DesktopOnlyNotice({ compact })` («Выдача и снятие
  прав — только с компьютера»; `compact` — одна строка с иконкой). Пометка — текст, не кнопка, не в Tab-порядке. Право
  — **самого действия** (m5): «Завершить все сеансы» цели — `staff.sessions.revoke`.
- `NAV`: `desktopOnly` снят со всех пунктов (флаг остаётся в типе `NavItem`); таббар телефона — первые 4 разрешённых
  пункта `NAV` по порядку (U10).
- `src/test/media.ts`: `stubMedia({ width: number; pointer: 'fine' | 'coarse' })` — `matchMedia`, который разбирает
  запросы ширины (`max-width`/`min-width`) и указателя; `afterEach` снимает.
- **Матрица прав на телефоне** (I6; спека §3.1, §3.5): `DataTableProps.layout?: 'auto' | 'table'` — `auto` (по
  умолчанию) — карточки на узком экране, как сейчас; `table` — таблица на любой ширине: своя область горизонтальной
  прокрутки с явным `overflow-y` (страж V1). `RolesMatrix` — `layout: 'table'`; подпись роли в шапке не поворачивается,
  ширина столбца — по короткой подписи роли. Закрепление первого столбца — UF2b.2 (`stickyFirstColumn`), применение к
  матрице — UF2b.10.

**Тесты:**
- `desktopOnly.test.tsx` (§3.2, стр. 322–326): 390 + `coarse` — в `StaffDrawer`/`StaffPage` нет «Пригласить»,
  «Сохранить роль», «Сбросить доступ», «Выключить», есть пометка и «Завершить все сеансы» — в том числе у цели-админа
  при `grant_elevated`; 640 + `fine` — кнопки есть; 1280 + `fine` — есть.
- `ApprovalsList` (в `ApprovalsPage.test`) › 640 + `fine` — решения `staff.*` есть; 390 + `coarse` — пометка;
  `staff.future_action` на телефоне — пометка; `geo.*` — решения есть на обоих.
- `nav.test.ts` › ни один пункт не `desktopOnly`; `router.test.tsx` › «телефон — раздел открыт» для каждого раздела
  (тест G8 «телефон — `DesktopOnlyState`» переписан).
- `StaffPage.test` › 390 + `coarse` → матрица — `role="table"` со строками прав и столбцами ролей, не карточки;
  `DataTable.test` › `layout: 'table'` на узком экране — строки таблицы, `auto` — карточки.

**Подсадка.** Убрать `ActionGate` вокруг «Выключить» — 390 + `coarse` краснеет; гейт по `useIsMobile` вместо
`useIsHandheld` — 640 + `fine` краснеет; таблица «действие → право» вместо префикса — `staff.future_action` краснеет;
`layout` игнорируется (всегда `useIsMobile`) — тест матрицы краснеет.

**Приёмка.** Снимки `/staff` (панель сотрудника) и `/approvals` на 390 — пометки вместо кнопок; на 1440 — кнопки.

**Ловушки.** Существующие тесты, которые глушат `matchMedia` с `matches: true` для любого запроса, теперь означают
«узко **и** палец» — сверить их ожидания (пункты «Ещё», шторки), а не переписывать молча. `ActionGate` в `src/ui`
импортирует реестр из `src/app` — прецедент `AppShell → app/session` (U11).

### UF2a.2: стек оверлеев с видом записи, `Popover`, `setCssPx`, прокладка jsdom

**Цель.** Основа всего всплывающего — `Popover` в верхнем слое и в общем стеке оверлеев с видом записи; положение
— JS от якоря числами через единственный помощник записи стилей (спека §4.8, стр. 642–714; §4.5, стр. 577–588).

**Риск:** A. Режимы отказа (таблица обязательна): Esc при списке в панели (закрывается только верхний); Tab при
немодальной записи наверху (ловушка — по верхней модальной); блок прокрутки от подсказки (не должен); открытие
`ConfirmDialog`/`ReauthModal` поверх открытого `Popover` (все `Popover` закрываются); якорь размонтирован или
перерисован, пока пузырь открыт (закрыть, запись стека снять); размонтирование открытого `Popover` (запись снята,
`data-wf-scroll-lock` не залип); StrictMode — двойной `pushOverlay`; два открытых `Popover` у разных якорей; якорь
ушёл из видимой прокрутки (подсказка закрывается, меню — у края); `focusout` с `relatedTarget = null` (щелчок по
нефокусируемому); щелчок по `option` (`mousedown` отменён, `pointerdown` — нет); `input`/`textarea` в пузыре;
`showPopover` на уже открытом/отключённом элементе (`InvalidStateError`); прокрутка любого предка; `resize`;
`ResizeObserver` нет (jsdom).

**Файлы.** Изменить: `src/ui/overlay/{stack.ts,Overlay.tsx,overlays.test.tsx,stack.test.ts}`, `src/ui/csp.test.ts`,
`src/test/setup.ts`, `src/app/styles.ts` (импорт глобального CSS, UF1a.1). Создать: `src/ui/popover/{Popover.tsx,
Popover.module.css,position.ts,position.test.ts,popover.test.tsx}`, `src/ui/cssVar.ts`, `src/ui/global.css`.

**Интерфейсы.**
- `stack.ts`: `pushOverlay(id: symbol, panel: HTMLElement | null, opts?: { modal?: boolean }): () => void` (по
  умолчанию `modal: true` — прежние вызовы не меняются); `isTopOverlay(id)` — по всем записям; `isTopModal(id)`;
  `topOverlayPanel()` — панель верхней **модальной**; `hasModalOverlay(): boolean`; `isOverlayStackEmpty(): boolean`;
  `onModalOpen(cb: () => void): () => void` — подписка `Popover` на открытие любого `Overlay`. Блок прокрутки
  (`data-wf-scroll-lock`) — пока есть модальная запись.
- `Overlay`: Tab — `trapTab` только если запись верхняя модальная; Esc — только если верхняя из всех; при монтировании
  — оповещение `onModalOpen`.
- `Popover({ anchor: RefObject<HTMLElement | null>; open: boolean; onClose: (reason: 'escape' | 'focusout' |
  'overlay' | 'scroll') => void; placement?: 'bottom-start' | 'bottom-end' | 'top-start' | 'top-end' | 'right' |
  'left'; kind: 'hint' | 'menu' | 'modal'; label?: string; id?: string; children: ReactNode })` — элемент
  `popover="manual"`; `kind: 'hint'` — `role="tooltip"`; `'modal'` — `role="dialog"` + `aria-modal` + своя ловушка Tab
  и Esc; `'menu'` — роль задаёт содержимое (`listbox`, `menu`). Немодальный закрывается по `focusout`, когда фокус
  покинул и якорь, и пузырь. `onMouseDown` на пузыре → `preventDefault()`, кроме цели внутри `input`/`textarea`.
  **Рендер — на месте, без портала** (m14): верхний слой и так снимает обрезку `overflow`, а пузырь остаётся внутри
  `aria-modal`-панели — `aria-activedescendant` combobox ссылается внутрь диалога, читалка не теряет список.
- `position.ts`: `place(anchor: DOMRect, size: { width: number; height: number }, viewport: { width: number; height:
  number }, placement) → { x: number; y: number; maxHeight: number; side }` — чистая функция с переворотом.
- `cssVar.ts`: `type WfCssVar = \`--wf-${string}\``; `setCssPx(el: HTMLElement, name: WfCssVar, px: number): void` —
  единственное место `setProperty` во всём `src`. Пузырь получает `--wf-pop-x`, `--wf-pop-y`, `--wf-pop-max-h`.
- `global.css` (U24): `html { scrollbar-gutter: stable }` (m-k). Единственный глобальный CSS админки, кроме токенов и
  шрифтов.
- `src/test/setup.ts`: прокладка Popover API для jsdom (n3, m-b) — `showPopover`/`hidePopover`/`togglePopover` ставят
  и снимают `data-popover-open` и шлют `toggle`; `matches(':popover-open')` — по атрибуту; тестовый `<style>` с
  `[popover]:not([data-popover-open]) { display: none }`. `csp.test.ts` явно исключает `src/test/**` с причиной (U20).
- `csp.test.ts`: `setProperty`/`cssText`/`.style.` — только в `ui/cssVar.ts`; `setCssPx(` — только в `ui/popover/*`
  (UF2b добавит `ui/virtual/*`).

**Тесты** (часть UF2a списка §4.8, стр. 698–707, + свои):
- `overlays.test.tsx` › `Select` в `Drawer` → Esc → список закрыт, панель открыта, фокус на combobox; второй Esc —
  панель закрыта (в UF2a.2 — на тестовом немодальном `Popover` с кнопкой-якорем, в UF2a.6 повторяется на `Select`);
  подсказка открыта на странице → у `body` нет `data-wf-scroll-lock`; немодальный в `Drawer` → Tab с последнего
  элемента панели → фокус в панели **и** пузырь закрыт; `TermHint`-подобный пузырь открыт → открыт `ConfirmDialog` →
  пузыря нет; `kind: 'modal'` в `Drawer` и на странице — Tab циклится внутри.
- `stack.test.ts` › подсказка открыта → `hasModalOverlay()` ложен; `topOverlayPanel()` при немодальном в `Drawer` —
  панель `Drawer`; размонтирование открытого `Popover` снимает запись; StrictMode — одна запись.
- `popover.test.tsx` › два открытых `Popover` у разных якорей (`getBoundingClientRect` подменён) → разные
  `--wf-pop-y`, каждый от своего якоря (N3); `mouseDown` на элементе пузыря → `defaultPrevented`, на `input` в пузыре —
  нет; закрытый — нет в дереве доступности (страж прокладки); якорь удалён из DOM → `onClose`; `Popover` внутри
  `Drawer` — потомок элемента `role="dialog"` панели (без портала, m14).
- `position.test.ts` › снизу не помещается → сверху; у правого края → выравнивание по концу; `maxHeight` ≤ места.
- `csp.test.ts` › исключение только для `cssVar.ts` и вызывателей из `popover/*`.

**Подсадка.** `Popover` без `pushOverlay` — тест Esc в `Drawer` краснеет (закрылась панель); `pushOverlay` без учёта
вида — тесты блока прокрутки и Tab краснеют; координаты общего (последнего) якоря — тест двух пузырей краснеет;
прокладка без правила `display: none` — «закрытый — нет в дереве» краснеет; вызов `setCssPx` из `features/*` — страж
CSP краснеет.

**Приёмка.** Обработчики Esc делают `preventDefault`; `Overlay` закрывает только себя-верхнего (как было); новый
`Popover` — с историями (открыт, у края окна, две подсказки разом). Прод-сценарий (2) e2e дополняется в UF2a.6.

**Ловушки.** Слушатель `Overlay` подписан на `document` раньше и гасит Tab `preventDefault` — немодальный не может
закрываться по `keydown` с проверкой `defaultPrevented`, только по `focusout` (m-j). `pointerdown` не отменять (m-n).
Слушатель прокрутки — `capture: true` на `document`, один `requestAnimationFrame` на кадр, снятие при закрытии.
`ResizeObserver` — проверка наличия. Пузырь в верхнем слое — координаты от окна (`position: fixed` в CSS Module).

**Документы.** `.claude/rules/admin.md:22` (CSP) — исключение `setCssPx` для `src/ui/popover` — **тем же коммитом**,
что правка `csp.test.ts` (спека m-c).

### UF2a.3: стенд browser history для тестов ухода

**Цель.** Опция стенда для тестов «Назад» и блокировщика: memory history `back()` блокировщики не опрашивает (спека
§4.11, стр. 798–809; R3-1, m-l). Сами тесты ухода — UF2b.7–UF2b.8.

**Риск:** B.

**Файлы.** Изменить: `src/test/app.tsx`. Создать: `src/test/app.test.tsx` (страж стенда). Документ:
`.claude/rules/admin.md` «Тесты».

**Интерфейсы.** `renderApp(path, { me?, strict?, history?: 'memory' | 'browser' })` — `browser`:
`window.history.replaceState(null, '', path)` **до** `createBrowserHistory()`; `history.destroy()` — в `cleanup`
(регистрация через `onTestFinished` или общий список в `setup.ts`); `at()` — `location.pathname + location.search`;
`back()` — `window.history.back()` внутри `act` с ожиданием `popstate`. По умолчанию — `memory` (прежние тесты не
меняются). Записи истории окна jsdom живут весь файл (`destroy` их не стирает, m10): тест «Назад» не уходит дальше
записей, которые создал сам (начинает со своего `replaceState` и делает `back` не больше числа своих `push`).
**Проверить по исходникам `@tanstack/history` 1.162.5 первым шагом** (ревью не проверило): восстанавливает ли
`destroy()` исходные `window.history.pushState`/`replaceState`. Если нет — стенд сам сохраняет исходные функции до
`createBrowserHistory()` и возвращает их в `cleanup`; если пакет их не подменяет вовсе — страж стенда только баланс
слушателей. Результат — в отчёт.

**Тесты:** `app.test.tsx` › маршрут с `useBlocker` (`shouldBlockFn` → `true`): `browser` + `back()` → блокировщик
вызван 1 раз, адрес вернулся; после `cleanup` — `window.history.pushState`/`replaceState` восстановлены (те же
функции, что до `renderApp`), слушателей `popstate`/`beforeunload` столько же, сколько до (подсчёт обёрткой
`addEventListener`/`removeEventListener` окна в тесте).

**Подсадка.** Без `destroy()` в `cleanup` — проверка восстановленного `pushState` и баланса слушателей краснеет.

**Документы.** `admin.md` «Тесты»: «memory history не блокирует `back` — тесты блокировщика и закрытия панели — на
`renderApp(path, { history: 'browser' })`».

### UF2a.4: `Tooltip`, `TermHint`, `CopyButton`

**Цель.** Подсказка, термин с «i», копирование в один клик (спека §4.8, стр. 715–730).

**Риск:** B.

**Файлы.** Создать: `src/ui/hint/{Tooltip.tsx,TermHint.tsx,Hint.module.css,hint.test.tsx}`,
`src/ui/CopyButton.tsx` (+ css, тест), истории. Изменить: `messages/{ru,en}.json` (`glossary.*`, `ui.copy.*`),
`src/i18n/labels.test.ts` (страж `GlossaryKey`), `src/ui/index.ts`.

**Интерфейсы.**
- `Tooltip({ content: ReactNode; children: ReactElement; placement?; open?: boolean })` — `Popover kind: 'hint'`,
  по наведению и фокусу; Esc скрывает; наведение на пузырь держит (WCAG 1.4.13); связь — `aria-describedby`; `open` —
  управляемый режим (истории и визуальный набор: «две подсказки разом»).
- `TermHint({ term: GlossaryKey; children: ReactNode })` — пунктир + кнопка «i» (переключаемая подсказка, текст — в
  живой области); `type GlossaryKey = 'dual_approval' | 'scoped' | 'city_status' | 'geonames' | 'slug' |
  'recovery_codes' | 'feature_flag'` (перечень §4.8; имена ключей — исполнителю, список — в `labels.test.ts`).
- `CopyButton({ value: string; label: string; display?: 'icon' | 'text' })` — `navigator.clipboard.writeText`; успех
  — значок `check` на 2 с и «Скопировано» в вежливой живой области (без тоста); отказ — значение выделяется в поле
  только для чтения рядом и тост «Скопируйте вручную».

**Тесты:** `hint.test.tsx` › наведение и фокус открывают, Esc закрывает, `aria-describedby`; «i» — нажатие
открывает и текст в живой области; `CopyButton.test` › успех — значок и живая область, повтор через 2 с (фейковые
таймеры, `fireEvent`); `writeText` отклонён — поле только для чтения с выделенным значением и тост;
`labels.test.ts` › каждый `GlossaryKey` есть в ru и en.

**Подсадка.** `CopyButton` без ветки отказа — тест отказа краснеет; ключ `glossary` только в ru — страж краснеет.

**Ловушки.** `navigator.clipboard` в jsdom — подменять в тесте (`vi.stubGlobal`); таймер 2 с снимается при
размонтировании; подсказка `title` не используется.

### UF2a.5: узкая полоса 768–1023

**Цель.** Каркас трёх ширин (спека §3.4, стр. 343–356): ≥ 1024 — сайдбар, 768–1023 — полоса иконок, < 768 — таббар.

**Риск:** B.

**Файлы.** Изменить: `src/ui/shell/{AppShell.tsx,AppShell.module.css,AppShell.test.tsx}`, `src/ui/layout-css.test.ts`,
`src/ui/Card.module.css` (сетка карточек), `src/ui/form/Field.module.css` (раскладка форм), `src/ui/overlay/
Overlay.module.css` (тело панели — контейнер).

**Интерфейсы.**
- Каркас — по `useIsMobile`/`useIsCompact` (ширина окна). Полоса `size.rail`: пункт — ссылка с доступным именем
  раздела (`aria-label`) и `Tooltip` с названием; счётчик — точка с числом для читалки (`countLabelKey`); карточка
  сотрудника — аватар-кнопка, по нажатию — шторка «Ещё»-подобная (язык, тема, «Выйти»). `Drawer` на 768–1023 — во всю
  высоту, ширина ≤ 90 %.
- **Container queries внутри страниц** (I6; спека §3.4, стр. 354–356; §2.8): область страницы в каркасе и тело панели —
  `container-type: inline-size`; `Card`-сетка и формы перестраиваются `@container` по порогам `container.narrow` (480)
  и `container.wide` (720) литералами — только то, что спека отдаёт контейнеру (`Card`-сетка, формы, раскладка внутри
  панели); переключение «строки ↔ карточки» `DataTable` и скрытие столбцов `priority: 'low'` — по окну, как в спеке
  §4.5 (U26, N1; UF2b.3).

**Тесты:** `AppShell.test` › 900 + `fine` — полоса: ссылки с именами разделов, счётчик «Подтверждения» слышен;
аватар → шторка с языком, темой и «Выйти»; 1280 — сайдбар с подписями; 390 — таббар; `layout-css.test.ts` › ширины в
`@media` — из `tokens.breakpoint` (расширение стража V1 на `compact`); ширины в `@container` — из `tokens.container`
или `tokens.breakpoint` (для `max-width` — минус 0.02); у области страницы — `container-type: inline-size`.

**Подсадка.** Ссылки полосы без `aria-label` — тест имён краснеет; `@container (max-width: 500px)` — страж ширин
краснеет.

**Приёмка.** Снимки 768 и 900 всех разделов (обе темы).

### UF2a.6: свой `Select`/`SelectField`, `MultiSelectField`, помощник `pick`, живой e2e роли

**Цель.** Нативный `<select>` уходит отовсюду, в том числе с телефона; API `SelectField` не меняется; 24 вызова
`user.selectOptions` в 9 файлах → `pick`; живой e2e — по `combobox`/`option` (спека §4.16, стр. 947–975; решение
пользователя 28.10).

**Риск:** B (стек и `Popover` — уже A в UF2a.2; здесь — потребитель).

**Файлы.** Создать: `src/ui/form/Select.tsx` (+ css, `Select.test.tsx`, истории), `src/test/pick.ts`. Изменить:
`src/ui/form/{SelectField.tsx,MultiSelectField.tsx,Field.module.css}`, `src/ui/controls.test.ts` (`<select>` запрещён
и в `src/ui`), тесты с `selectOptions` (`approvals/count.test.tsx`, `audit/AuditPage.test.tsx`,
`geo/{ActivateDrawer,CitiesPage,CityPage}.test.tsx`, `staff/{InviteDrawer,StaffDrawer,StaffPage}.test.tsx`,
`ui/form/form.test.tsx`), тесты с ожиданиями нативного `<select>` (`geo/{ActivateDrawer,CitiesPage,LinkPlaceDialog}.
test.tsx` — перечень ниже), `e2e/{staff.spec.ts,keys.ts}`, `e2e/prod.spec.ts` (сценарий (2): открыть список).

**Интерфейсы.**
- `Select({ id, name, value, onChange(value: string), onBlur, options: Option[], placeholder?, disabled?, invalid?,
  describedBy?, labelledBy })` — шаблон WAI-ARIA «select-only combobox»: кнопка `role="combobox"` (`aria-expanded`,
  `aria-controls`, `aria-activedescendant`, `aria-labelledby`), `role="listbox"` + `role="option"` (`aria-selected`);
  ↓/↑/Home/End, Enter/Space — выбрать, Esc — закрыть без выбора, буквы — typeahead, Tab — закрыть и уйти; фокус
  остаётся на кнопке. Широкий экран — `Popover kind: 'menu'` под полем; телефон (`useIsMobile`) — `Sheet` (заголовок —
  подпись поля, строки ≥ `touch-min`, выбранная — галочкой, при > 10 опций — поле поиска сверху, выбор закрывает).
  Без виртуализации: прокрутка к активной — `scrollIntoView({ block: 'nearest' })`.
- `SelectField` — прежние пропсы (`name`, `label`, `options`, `placeholder`, `hint`, `rules`); внутри `Controller`;
  контракт `rules.onChange(e)` с `e.target.value` сохранён (экраны `CityCards`, `ActivateDrawer` на нём живут).
  Необязательное поле — опция «не выбрано» (значение `''`).
- `MultiSelectField` — тот же `Popover`/`Sheet`.
- **Имя combobox — только подпись поля** (I2; как в примере APG select-only): `aria-labelledby` = id подписи;
  выбранное значение — текст кнопки, в имя не входит. Тогда 16 запросов `getByRole('combobox', { name: 'Регион' })` в
  тестах экранов находят поле, как раньше.
- `src/test/pick.ts`: `pick(user: UserEvent, field: string | RegExp | HTMLElement, option: string | RegExp):
  Promise<void>` — открыть combobox по подписи поля, выбрать option по имени, дождаться закрытия;
  `selectedText(field): string` — выбранное значение (текст кнопки; замена `toHaveValue` у нативного поля);
  `openOptions(user, field): Promise<{ el: HTMLElement; value: string; label: string; disabled: boolean }[]>` —
  открыть список и вернуть опции (замена `within(поле).getByRole('option')` и чтения `HTMLOptionElement.value/
  .disabled`; закрыть — Esc). Для этого у `role="option"` своего `Select` — `data-value` (значение опции) и
  `aria-disabled="true"` у недоступной (часть интерфейса `Select`, проверяется в `Select.test`).
- **Правило переписываний тестов экранов** (I2, раунд 2): любая проверка нативного списка — `toHaveValue` у
  поля-списка, `within(поле).getByRole('option')`, чтение `HTMLOptionElement` — переписывается на помощники
  `selectedText`/`openOptions`/`pick` с тем же смыслом ожидания; прочие ожидания экранов не меняются. Известные места на
  03.11 (номера строк сверить):
  - `toHaveValue` → `selectedText`: `ActivateDrawer.test.tsx:176–178, 222`, `CitiesPage.test.tsx:171`,
    `LinkPlaceDialog.test.tsx:36`, `InviteDrawer.test.tsx:215, 297` («Роль»), `StaffDrawer.test.tsx:91` («Роль»),
    `StaffDrawer.test.tsx:212` («Причина» — `SelectField` `reason`), `StaffPage.test.tsx:196` (фильтр «Роль»),
    `AuditPage.test.tsx:168` («Вид объекта» — `SelectField` `object_type`);
  - опции внутри поля → `openOptions`: `ActivateDrawer.test.tsx:196, 218, 223, 236`;
  - помощник `roleOptions` (`InviteDrawer.test.tsx:41–45`, 3 вызова; читает `.value` и `.disabled` опций) → поверх
    `openOptions` (`value`, `disabled`);
  - 24 вызова `selectOptions` → `pick`.
  Найденное сверх списка, но подпадающее под правило — переписать и перечислить в отчёте; ожидание, которое правилом
  не покрыто, — NEEDS_CONTEXT, а не ослабление теста.

**Тесты:** `Select.test.tsx` › клавиатура (↓/↑/Home/End/Enter/Space/Esc), typeahead, Tab закрывает, `aria-*`,
телефон — шторка с поиском при > 10 опций; **страж R4-1** (стр. 693–695): открыть список →
`createEvent.mouseDown(option)` + `fireEvent` → `defaultPrevented === true` и combobox — `document.activeElement`;
закрытый список — опций нет в дереве доступности; `overlays.test.tsx` › `Select` в `Drawer` → Esc закрывает только
список; `form.test.tsx` › `SelectField` в `Form`: значение, `validation.failed` по полю, `rules.onChange` получает
`e.target.value`; `controls.test.ts` › `<select` нет нигде в `src`; `Select.test` › `getByRole('combobox', { name:
'Регион' })` находит поле с выбранным значением (имя — только подпись); у опций — `data-value`, у недоступной —
`aria-disabled="true"`, `openOptions` возвращает `{ value, label, disabled }`. Тесты экранов — на `pick`/
`selectedText`/`openOptions` по правилу выше, прочие ожидания не меняются.

**Живой e2e.** `staff.spec.ts`: роль приглашения — клик по `combobox` и `option` по подписи из `KEYS`; мышью
выбирается значение **не по умолчанию**, затем нужное, итог сверяется по тексту combobox и по письму/строке
подтверждения (браузерный страж R4-1: jsdom не проверяет выбор мышью по координатам). Прогон — один.

**Подсадка.** Без `preventDefault` на `mousedown` пузыря — страж R4-1 краснеет; `SelectField` через `register`
вместо `Controller` (прежний `<select>`) — `controls.test.ts` краснеет.

**Приёмка.** 24 места в экранах переведены без правки экранов (кроме устранения нативного `<select>` в `src/ui`);
историй — все состояния (закрыт, открыт, длинный список, ошибка, недоступен, телефон).

**Ловушки.** `<label htmlFor>` на кнопку работает (кнопка — labelable), но имя combobox — через `aria-labelledby`
только на подпись (значение в имя не входит, I2). Enter на закрытом combobox внутри `Form` не должен отправлять форму. `setValue` экранов
(сброс региона при смене страны) работает и с `Controller`. Шторка `Sheet` внутри `Drawer` — модальная запись стека
поверх модальной. Подписи для e2e — в `e2e/keys.ts`; правило `admin.md` «роль приглашения — нативный `<select>`»
меняется на «`combobox` + `option`» в UF2a.12.

### UF2a.7: свой календарь `DateField`

**Цель.** `TextField type="date"` → `DateField` с календарём; неделя — по языку интерфейса (спека §4.17,
стр. 977–995; решение пользователя 28.10).

**Риск:** B.

**Файлы.** Создать: `src/ui/form/{DateField.tsx,Calendar.tsx,DateField.module.css,DateField.test.tsx}`,
`src/i18n/week.ts` (+ тест), истории. Изменить: `src/features/audit/AuditFilters.tsx`, `src/ui/form/TextField.tsx`
(тип `date` убрать), `src/ui/controls.test.ts` (`type="date"` в `features` запрещён), `messages/{ru,en}.json`.

**Интерфейсы.**
- `DateField({ name, label, min?: string; max?: string; hint?; rules? })` — значение `YYYY-MM-DD`; поле текста в
  формате языка (маска по `Intl.DateTimeFormat#formatToParts`) + кнопка «Открыть календарь» (`Icon calendar`).
- Календарь — шаблон WAI-ARIA «Date Picker Dialog»: `role="grid"`, стрелки — дни, PageUp/PageDown — месяцы,
  Shift+PageUp/PageDown — годы, Home/End — неделя, Enter — выбрать, Esc — закрыть; заголовок месяца — живая область;
  дни вне `min`/`max` — `aria-disabled`. Широкий экран — `Popover kind: 'modal'`; телефон — `Sheet`; фокус после
  закрытия — на кнопку календаря.
- `firstDayOfWeek(locale: string): 1 | 7` (`src/i18n/week.ts`) — `new Intl.Locale(l).getWeekInfo?.()` или свойство
  `weekInfo`; нет API — таблица `ru → 1`, `en → 7`.

**Тесты:** `DateField.test.tsx` › `ru` — первый столбец «пн», `en` — «Sun»; клавиатура по шаблону; ввод текстом и
выбор в сетке дают одну строку `YYYY-MM-DD`; пояс `America/New_York` и `Asia/Kamchatka` не сдвигает дату; `max` —
недоступные дни; в `Drawer` и на странице Tab циклится в календаре, Esc закрывает только его, фокус — на кнопке;
`week.test.ts` › нет `getWeekInfo` (подмена) → таблица; `AuditPage.test` › период журнала через `DateField` — те же
`from`/`to` в запросе.

**Подсадка.** Первый день недели всегда `0`/воскресенье — тест `ru` краснеет.

**Ловушки.** Дата без времени: разбор строки по частям, не `new Date('YYYY-MM-DD')` (UTC-полночь сдвигает день в
западных поясах). `toAuditQuery` не меняется. «по» не раньше «с» — через `min` второго поля.

### UF2a.8: `DataState`, скелетоны, `NoNetworkState`, `EmptyState.illustration`

**Цель.** Пять состояний одним компонентом, скелетоны по форме, иллюстрация пустого (спека §4.3–§4.4,
стр. 473–506).

**Риск:** B. Внимание: `networkMode: 'online'` для запросов меняет поведение всего приложения без сети (m19) —
режимы «вкладка без сети → пауза → сеть вернулась → запрос продолжился» и «фоновая ошибка не прячет данные» — тестами.

**Файлы.** Создать: `src/ui/states/{DataState.tsx,Skeleton.tsx,Skeleton.module.css,dataState.test.tsx}`, истории.
Изменить: `src/ui/states/{states.tsx,index.ts}` (`NoNetworkState`, `LoadingRows` → обёртка над `Skeleton`,
`EmptyState.illustration`), `src/app/query.ts` (`queries.networkMode: 'online'`), `src/ui/csp.test.ts` или новый страж
`src/ui/states/loading.test.ts`.

**Интерфейсы.**
- `DataState<T>({ query: UseQueryResult<T>; skeleton: ReactNode; isEmpty?: (data: T) => boolean; empty?: ReactNode;
  children: (data: T) => ReactNode })` — таблица §4.3: загрузка — `skeleton` через 150 мс, `aria-busy`, «Загрузка…» для
  читалки один раз; пусто; ошибка — `ErrorState` / 403 `ForbiddenState` / 429 `RateLimitedState`; нет сети —
  `fetchStatus === 'paused'` без данных или `TypeError` fetch → `NoNetworkState`; данные — `children`, фоновая ошибка
  данные не прячет.
- `Skeleton.Text({ lines, width })`, `Skeleton.Block({ height })`, `Skeleton.Card()`, `Skeleton.Table({ columns,
  rows })`, `Skeleton.Form({ fields })`; мерцание — CSS-анимация, при reduced motion — статично.
- `EmptyState({ …, illustration?: IconName })` — `Illustration` 48–64, `aria-hidden`.

**Тесты:** `dataState.test.tsx` › ответ удержан (gate в MSW), через 100 мс (фейковые таймеры) скелетона ещё нет,
затем ответ — скелетона так и не было (m11: проверка в момент, когда без задержки скелетон уже стоял бы); через 150 мс
удержания — скелетон есть, `aria-busy`; `isEmpty` → `empty`; 403/429/500 — свои состояния; `onlineManager.setOnline(false)` → пауза →
`NoNetworkState`, сеть вернулась → данные; ошибка перечитывания при данных — данные на месте; страж: файлы `features/*`
не рендерят `LoadingRows`/«Загрузка…» вне `DataState`/`DataTable`.

**Подсадка.** Без задержки 150 мс — проверка «через 100 мс скелетона нет» краснеет; ветка `paused` убрана — «нет
сети» краснеет.

**Ловушки.** Фейковые таймеры + user-event зависают — `fireEvent` и `vi.useFakeTimers({ shouldAdvanceTime: true })`
(admin.md «Тесты»). Мутации остаются `networkMode: 'always'`. JS-анимаций фундамент не вводит (скелетон, тосты,
панели, View Transitions — CSS), поэтому `useReducedMotion()` (`src/ui/motion.ts`, §2.7) заводит UF2b.9 — единственный
вызыватель JS, `startViewTransition` (m22).

### UF2a.9: время, числа, деньги, единый словарь

**Цель.** `RelativeTime`, `useNow`, `useNumber`, `useMoney`; запрет прямого `Intl` в экранах; словарь `actions.*` со
стражем (спека §4.7, стр. 619–640).

**Риск:** B (деньги — правило 2 CLAUDE.md: без float).

**Файлы.** Создать: `src/ui/time/{RelativeTime.tsx,useNow.ts,RelativeTime.module.css,time.test.tsx}`,
`src/i18n/vocabulary.test.ts`, истории. Изменить: `src/i18n/{format.ts,format.test.tsx}`, `eslint.config.mjs`
(`no-restricted-syntax` в `src/features/**`: `toLocaleString`, `toLocaleDateString`, `toLocaleTimeString`,
`new Intl.*`), `messages/{ru,en}.json` (`actions.*` и перевод экранов на них), `src/features/audit/search.ts` →
`dayStartUtc` (сейчас `new Intl.DateTimeFormat` в `features/audit/search.ts:87`) переезжает в `src/i18n/` с тестами
пояса без изменения поведения (m8; тесты `search.test.ts`/`AuditPage.test` периода — зелёные без правки).

**Интерфейсы.**
- `RelativeTime({ value: string | Date; mode?: 'auto' | 'relative' | 'exact' })` → `<time dateTime>`: текст «5 мин
  назад», точное время — скрытым текстом внутри `<time>` и `Tooltip` по наведению и по `:focus-within` строки; своей
  остановки Tab нет; `auto`: < 1 мин — «только что», < 7 дней — относительно, старше — дата.
- `useNow(granularity: 'minute' | 'second'): Date` — одна подписка на всё приложение на гранулу; в скрытой вкладке
  стоит; в историях и визуальных тестах подменяется (`NowProvider` или `setNowForTests`).
- `useNumber(): (n: number, opts?) => string`; `useMoney(): (minor: string | number, currency: string, exponent:
  number) => string` (U13) — десятичная строка из целого без деления (`"123456"`, 2 → `"1234.56"`), отрицательные,
  экспонента 0 и 3; в `Intl.NumberFormat#format` — строкой.
- Словарь: `actions.{save,cancel,close,back,retry,archive,restore,delete,undo,copy,showMore,resetFilters,
  submitForApproval}`; экраны используют их вместо своих одинаковых ключей.

**Тесты:** `time.test.tsx` › `auto` на границах 59 с / 1 мин / 6 дн 23 ч / 7 дн; скрытое точное время в `<time>`; Tab
не останавливается на `RelativeTime`; подсказка по фокусу строки; таймер `useNow` один на много компонентов и стоит
при `visibilityState = hidden`; `format.test.tsx` › `useMoney('9007199254740993', 'RUB', 2)` — точная строка (больше
2^53), `'-500'`, `'JPY', 0`, `'KWD', 3`; `vocabulary.test.ts` › значение `actions.*` в ru не повторяется под другим
ключом (кроме списка исключений с причиной); запрещённые синонимы («Стереть», «Отмена» как действие, «Подтвердить»
вместо «Отправить на подтверждение») не встречаются.

**Подсадка.** `useMoney` через `Number(minor) / 10 ** e` — тест > 2^53 краснеет; дубль «Сохранить» под ключом
экрана — словарь краснеет.

**Ловушки.** Перевод экранов на `actions.*` — ключи, по которым ищет живой e2e, синхронно в `e2e/keys.ts`
(`keys.test.ts`). Lint-правило — только `features/*` (хуки в `src/i18n` используют `Intl` законно).

### UF2a.10: применение к экранам (спека §6.1, строки UF2a)

**Цель.** Экраны плана A и G8 на новых компонентах — без смены поведения.

**Риск:** B.

**Файлы и правки** (по строкам таблицы §6.1, стр. 1161–1171):
- «Роли и доступ» (`features/staff/*`): почта — `CopyButton`; «Правило двух рук» — `TermHint` (`dual_approval`); роль
  приглашения — `SelectField` (уже UF2a.6).
- «Мой доступ» (`features/me/*`): `DataState` + скелетон страницы; сеансы — `RelativeTime`.
- «Подтверждения» (`features/approvals/*`): запрошено и срок — `RelativeTime`; id — `CopyButton`.
- «Журнал» (`features/audit/*`): время — `RelativeTime`, id — `CopyButton`; период — `DateField` (UF2a.7).
- «Города и запуск», страница города, `ActivateDrawer`, `LinkPlaceDialog` (`features/geo/*`): `DataState` +
  `CityPageSkeleton` (собран из `Skeleton.*` рядом с экраном); адрес и `geoname_id` — `CopyButton`; пустые — с
  иллюстрацией и следующим шагом (таблица «экран → иллюстрация» — UF2a.12).
- `setup-2fa`, `RecoveryCodesPanel` (`features/auth/*`): секрет и коды — `CopyButton` (коды — только в памяти, как
  было: `CopyButton` их не кладёт ни в storage, ни в кэш).

**Тесты:** тесты экранов зелёные; новые проверки — по одному на экран: кнопка копирования с именем «Копировать:
<что>», `<time>` с `dateTime`, скелетон по форме при медленном ответе (страница города).

**Подсадка.** `CopyButton` с `value` из другого поля (почта вместо id) — тест имени/значения на экране краснеет.

**Приёмка.** Снимки всех разделов на трёх ширинах в обеих темах.

**Ловушки.** Коды восстановления и секрет 2FA — не в `sessionStorage`/кэш Query (правило `admin.md`; `storageDump()`
в тесте `SetupMfaPage`).

### UF2a.11: уборка фронта (открытые minor ревью V1 и G8)

**Цель.** Закрыть накопленные minor по теме фундамента. Сверено с кодом 03.11.2026: все пункты ниже открыты; закрытые
(G8 m2, m4, m7; V1 m3, m4 → UF1b) не включены. Каждый пункт — свой тест, красный до правки.

**Риск:** B (n4 — защита в глубину подтверждения вдвоём, A по смыслу, U15).

**Пункты.**
- **V1 m1** (`ActivateDrawer.tsx`, `LinkPlaceDialog.tsx`, `PlaceSearch`): выбор места снимается, когда его строки нет
  в результатах (смена страны, поиска) и при «Другое место». Тест: выбрать → страна KZ → «Выбрать» недоступна.
- **V1 m2**: смена шага переводит фокус на первое поле шага 2 / на список шага 1, не на body. Тест: ↓, Enter →
  `activeElement` не body.
- **V1 m5**: удалить мёртвые `geo.activate.columns.*` (ru, en) + страж неиспользуемых ключей (`labels.test.ts`: каждый
  ключ `messages` используется в `src` или подпадает под список динамических префиксов с причиной).
- **V1 m6**: `BackLink` нет на странице раздела (`/geo/cities`, `/audit`); тесты режима `select` в `DataTable.test`
  (↑, двойной клик, «Показать ещё»); память «Назад» при смене сотрудника (перенос провайдера выше `key={me.id}` —
  тест краснеет).
- **V1 m7**: `configure({ asyncUtilTimeout: 3000 })` в `src/test/setup.ts`.
- **G8 m1** (`CityPage.tsx`): `cityId` не uuid → «Город не найден» без запроса (или 400 по `path.cityId` → то же).
- **G8 m3**: после `409 geo.city_already_active` и после успешной активации/привязки — инвалидация `['geo','places']`;
  «Найти город» — по `city_id` перечитанного места.
- **G8 m5** (`PlaceSearch.tsx`): `q` обрезается до 100 (контракт `maxLength 100`).
- **G8 m6** (`CityNamesCard.tsx`): стёрли перевод и «Сохранить» — поле возвращается к сохранённому значению и
  подсказка «удалить — кнопкой».
- **G8 m9** (`CityCards.tsx`): чип «Все» убирает `status` из адреса (пустой массив — не значение).
- **G8 m10** (`features/geo/search.ts`): `region_id` без `country` отбрасывается.
- **G8 m11** (`ui/Card.tsx`): действие карточки «Открыть» — ссылка (`href` + `navigate`), имя — «Открыть: <город>».
- **G8 m12** (`CityStatusCard.tsx`): незнакомый статус показан опцией «—» (недоступной), отправка с ним — ошибка
  формы, не молчание.
- **G8 m13** (`CityPage.tsx`, `CitiesPage.tsx`): «Привязать» и «Активировать» — при обоих правах (`geo.cities.manage`
  / `geo.cities.activate` **и** `geo.places.search`).
- **G8 m14** (`ActivateDrawer.tsx`): исход активации неизвестен и панель закрыта → `invalidateCityLists`.
- **G8 n1** (`CityPage.tsx`): после `409 city_already_linked` закрытие окна (Esc) — фокус на «Сведения о городе».
- **G8 n2** (`DistrictsCard.tsx`, `CityNamesCard.tsx`): цели фокуса `refocus`/`deleted` гасятся после применения и в
  начале нового действия.
- **G8 n3** (`DistrictsCard.tsx`): переименование, меняющее порядок, — тот же `setRefocus` на «Переименовать: <имя>».
- **G8 n4, фронт** (`features/approvals/summary.ts`): ключи `payload.patch`, которых нет в `from ∪ to`, — тоже в
  `others` («и ещё N полей»). Бэкенд-сторона закрыта G6 (`TestCountryRulesPayloadShape`).
- **G8, непредусмотренный отказ №1** (m18; `features/geo/queries.ts`): смена языка на странице города размонтирует
  формы и стирает ввод (язык в ключе `geoKeys.city`) — `useGeoCity` с `placeholderData` **функцией**, которая отдаёт
  прежние данные только при том же `cityId` (сверка ключа прежнего запроса; другой город — `undefined`, скелетон), чтобы
  `DataState` не ставил скелетон поверх ввода; пока `isPlaceholderData` — формы города только для чтения (отправка
  недоступна), иначе «Сохранить» ушло бы со значениями прежнего ответа (n5). Тесты: ввод в поле адреса → смена языка →
  ввод на месте, «Сохранить» недоступна до ответа; переход «город A → город B» на том же маршруте → данных A под адресом
  B нет (скелетон, затем B).

**Подсадка.** По пункту — откат правки краснеет свой тест (список подсадок — в отчёт).

**Приёмка.** Ни один закрытый пункт не переделан; отчёт — таблица «пункт → тест → подсадка».

### UF2a.12: документы UF2a

**Цель.** Строки §6.4 для UF2a.

**Риск:** C.

**Содержание.**
- Спека сотрудников §9.4, абзац «Телефон (08)» — текстом §3.3 спеки (стр. 330–336); §9.2 — ссылка на §4 спеки UI как
  продолжение набора компонентов (U14).
- `.claude/rules/admin.md`: `ActionGate` по устройству (`useIsHandheld`) вместо `desktopOnly` разделов (строки
  «Новый раздел», «Мой доступ», «Роли и доступ», «Справочники», «Подтверждения» — правка «на телефоне закрыт»);
  компоненты `Popover`, `Select`/`SelectField`, `DateField`, `DataState`, `Skeleton`, `NoNetworkState`, `Tooltip`,
  `TermHint`, `CopyButton`, `RelativeTime`, `useNumber`/`useMoney`; исключение CSP (`cssVar.ts`, `popover/*`) — строка
  22 правится тем же коммитом, что UF2a.2 (не здесь); «роль приглашения — нативный `<select>`» → «`combobox` +
  `option`»; `TextField type="date"` → `DateField`; глобальный CSS (U24); `DataTable` `layout: 'table'` (матрица прав);
  container queries внутри страниц.
- `.claude/rules/frontend.md` (UF2a — владелец файла в волне, I8): `useNumber`/`useMoney`/`RelativeTime`; деньги —
  строкой без float; container queries — пороги `container.*` литералами.
- `design/README.md`: мобильные разделы, полоса 768–1023, свой список и календарь — «дорисовано *»; таблица «экран →
  иллюстрация» (§4.4); пометка «текст макета 08 „открытие города — только с компьютера“ устарел».

### UF2a.13: правки плана B — этап 4 под принципы (перед брифами G9)

**Цель.** Спека §6.3 (стр. 1198–1210) — правки текста плана B, чтобы брифы G9 писались по новым правилам.

**Риск:** C.

**Файлы.** `docs/superpowers/plans/2026-10-27-staff-b-geo2.md` (только этап 4, Task 23–24, «Порядок исполнения»).

**Содержание.** Вводный абзац этапа 4 («Все пять разделов — `desktopOnly`» → разделы открыты, только с компьютера —
по реестру); Task 17 (тест «телефон — раздел открыт», `Switch`/`Card` — истории); Task 18–19 («Приёмка… `desktopOnly`»
снимается, правки — §6.1 спеки UI); Task 20–22 — по §6.2 спеки UI (таблица «было → становится»), брифы G9 — после
UF4; Task 23 (стартовая — главная, страна и статус — `combobox`/`option`, сценарии и в прод-проекте); Task 24
(`design/README.md` — светлая тема, главная, палитра, полоса, мобильные «Справочники»); «Порядок исполнения» — между G8
и G9 группы UF1a–UF4. Плюс ⚠️ G9 из этого плана (U18): панель страны (Task 21) — `/geo/countries?edit=<код>`
(не `?country=`, спека §15); `country` в поиске `/geo/imports` — фильтр (Task 20), с `PANEL_SEARCH_KEYS` не
пересекается (страж UF2b.8).

**Приёмка.** Правки — только формулировки и ссылки; сам план B по существу (задачи, бэкенд) не меняется.

## Группа UF2b «Таблицы и поведение»

Спека §7 (стр. 1242), §4.5, §4.6, §4.10, §4.11, §4.15, §6.1 (строки UF2b). Риск: **A** (массовые действия,
«Отменить», ключи идемпотентности, история, блокировщик). Деление на два захода (U1): **UF2b/I** — UF2b.1–UF2b.5
(таблицы), **UF2b/II** — UF2b.6–UF2b.10 (поведение, экраны, документы); ревью — после каждого. Старт — после ревью
UF2a/III; `pnpm install` в UF2b.1 — только по сигналу контроллера («тихо»: UF3 не гоняет процессов из
`node_modules`, dev-сервер пользователя остановлен — U2, m24). Общие файлы — по таблице владения (I8): строку
`docs/versions.md` UF2b не правит, а пишет в отчёт.

### UF2b.1: виртуализация — зависимость, `useVirtualRows`, `VirtualList`, исключение CSP

**Цель.** Одна основа виртуализации списков и строк таблицы; вторая и последняя разрешённая точка вызова `setCssPx`
(спека §4.5, стр. 573–588; n2).

**Риск:** B (CSP-исключение — по образцу UF2a.2).

**Файлы.** Изменить: `package.json` админки (`@tanstack/react-virtual` — последняя стабильная, спека называла
3.14.23), `pnpm-lock.yaml` (`pnpm install` по сигналу), `src/ui/csp.test.ts` (вызыватели `setCssPx` — `popover/*` и
`virtual/*`), `.claude/rules/admin.md:22` (тем же коммитом); строка `docs/versions.md` — в отчёт, вносит контроллер
(владелец файла в волне — UF3, I8). Создать:
`src/ui/virtual/{useVirtualRows.ts,VirtualList.tsx,Virtual.module.css,virtual.test.tsx}`, истории.

**Интерфейсы.**
- `useVirtualRows({ count: number; estimate: number; scrollRef: RefObject<HTMLElement | null>; overscan?: number;
  horizontal?: boolean }) → { items: { index: number; start: number; size: number }[]; totalSize: number;
  scrollToIndex(i: number): void; measure(el: HTMLElement | null): void }` — обёртка `useVirtualizer`.
- Положение строк и полная высота — только `setCssPx(el, '--wf-virtual-start' | '--wf-virtual-size' |
  '--wf-virtual-total', px)`; оформление — CSS Module.
- `VirtualList({ items, estimate, renderItem, label })` — для карточных/простых списков, где `content-visibility`
  не годится.

**Тесты:** `virtual.test.tsx` › 1000 элементов → в DOM ≤ видимые + запас (размеры контейнера подменены);
`scrollToIndex(900)` → элемент 900 в DOM; `csp.test.ts` › `setCssPx` из `virtual/*` разрешён, из `features/*` —
нет.

**Подсадка.** Запись положения через `el.style.transform` — `csp.test.ts` краснеет.

**Ловушки.** jsdom не считает размеры — `getBoundingClientRect`/`offsetHeight` подменять в тесте; `useVirtualizer` в
StrictMode — двойная подписка, проверить отсутствие утечки слушателей прокрутки.

### UF2b.2: `DataTable` — своя область прокрутки, закреплённая шапка и столбец, «фокус не закрыт»

**Цель.** Шапка «липнет» внутри своей области прокрутки по обеим осям, первый столбец широких таблиц закреплён,
фокус не уходит под шапку, столбец, подвал, таббар и тост (спека §4.5, стр. 537–550; WCAG 2.4.11).

**Риск:** B.

**Файлы.** Изменить: `src/ui/table/{DataTable.tsx,DataTable.module.css,model.ts,DataTable.test.tsx}`,
`src/ui/overlay/Overlay.module.css` (`scroll-padding-block-end` тела панели = высота подвала),
`src/ui/shell/AppShell.module.css` (страница на телефоне: `scroll-padding-block-end` = `size.tabbar`; при
`:root[data-toast-open]` — + `size.toast`; атрибут ставит UF2b.6).

**Интерфейсы.** `DataTableProps<T>`: `stickyHeader?: boolean` (по умолчанию — при строках > 20), `stickyFirstColumn?:
boolean` (работает и при `layout: 'table'` на телефоне — матрица прав, I6); обёртка таблицы — область прокрутки по
обеим осям с `max-block-size` до низа окна (container query), явный
`overflow-y` (страж V1 `layout-css.test.ts`); `scroll-padding-block-start` = `size.table-header`,
`scroll-padding-inline-start` = ширина закреплённого столбца.

**Тесты:** `DataTable.test.tsx` › `stickyHeader` по умолчанию при 21 строке и нет при 20; `stickyFirstColumn` — класс
у первой ячейки, в том числе при `layout: 'table'` на 390; `layout-css.test.ts` › у области прокрутки таблицы — `scroll-padding-block-start` из токена, у тела
панели — `scroll-padding-block-end`; у страницы на телефоне — `size.tabbar`.

**Подсадка.** Убрать `scroll-padding-block-start` — страж CSS краснеет.

**Приёмка.** Визуальная проверка — истории «фокус на строке под шапкой после Tab снизу вверх», «последнее поле формы
над подвалом», «фокус на последней строке при открытом тосте „Отменить“ (390)» (§4.5) — пишутся здесь с меткой
`visual: true`, снимает их визуальный job (UF3.6); локально — `admin:shots` `/audit` на 1440 и 768.

### UF2b.3: выбор столбцов и плотность

**Цель.** Меню «Столбцы» на `Popover` и одна плотность на все таблицы (спека §4.5, стр. 551–555).

**Риск:** B.

**Файлы.** Изменить: `src/ui/table/*`, `messages/{ru,en}.json`. Создать: `src/ui/table/{ColumnsMenu.tsx,prefs.ts,
prefs.test.ts}`.

**Интерфейсы.**
- `DataTableProps`: `tableId?: string` (без него кнопки «Столбцы» нет); `columns[].hideable?: boolean` (по умолчанию
  `true`, кроме первого); `columns[].priority?: 'high' | 'low'` — `low` скрыт **по окну** на 768–1023
  (`useIsCompact() && !useIsMobile()`, как в спеке §4.5 и §3.4; решение контроллера по N1) и в карточке телефона под
  «Подробнее»; от 1024 видны все столбцы.
- Видимость столбца = выбор пользователя важнее ширины: столбец `low`, **явно включённый** в «Столбцах», ширина не
  прячет; без явного выбора на 768–1023 он скрыт, а в меню отмечен как «скрыт по ширине» (флажок снят, подпись-
  пояснение) — меню не врёт.
- `ColumnsMenu` — кнопка «Столбцы» рядом с «Ещё фильтры», `Popover kind: 'menu'`, шаблон WAI-ARIA menu с
  `menuitemcheckbox` (стрелки внутри, Tab закрывает и уходит дальше — U23); хотя бы один видимый; в меню — плотность
  `compact | normal`. На телефоне (карточки) кнопки нет.
- `prefs.ts`: `readColumns(tableId) → { hidden: string[]; shown: string[] }` / `writeColumns(tableId, prefs)` —
  `wf.admin.table.<tableId>` (`shown` — явно включённые `low`), незнакомые id отбрасываются; `useDensity(): ['compact' | 'normal', set]` — `wf.admin.density`, на `pointer: coarse`
  всегда `normal`; высота строки — `density.*.row`. Storage — `try/catch` (U22).

**Тесты:** `prefs.test.ts` › незнакомый id столбца отброшен; storage бросает — значения по умолчанию; `DataTable.test`
› скрыть столбец → нет в таблице и после перемонтирования; скрыть последний видимый нельзя; `stubMedia` 900 + `fine`
→ столбец `low` скрыт, в меню — «скрыт по ширине»; 1280 → виден (в том числе с открытым сайдбаром — решение по окну,
не по контейнеру); 900 + явно включённый в меню `low` → виден и после перемонтирования; плотность `compact` на `coarse`
— `normal`; меню: стрелки по флажкам, Esc закрывает только меню, Tab — закрыт.

**Подсадка.** Без отбрасывания незнакомых id — тест «столбец из старой версии» краснеет (все столбцы скрыты); скрытие
`low` без учёта `shown` — тест «явно включённый на 900» краснеет; порог по `useIsMobile` вместо `useIsCompact` — тест
900 краснеет.

### UF2b.4: возврат на ту же позицию, виртуализация строк

**Цель.** «Назад» из карточки — те же страницы, прокрутка и фокус на строке; длинные таблицы — виртуальные с
`aria-rowcount`/`aria-rowindex` (спека §4.5, стр. 556–560, 573–577).

**Риск:** B.

**Файлы.** Изменить: `src/ui/table/*`, `src/app/router.tsx` (`scrollRestoration: true`), карточные списки
(`CityCards` CSS: `content-visibility: auto` + `contain-intrinsic-size`).

**Интерфейсы.** `DataTableProps`: `virtualize?: boolean | { estimateRow?: number }` (по умолчанию — при строках > 200),
`restoreKey?: string` (по умолчанию путь + search); контейнер — `data-scroll-restoration-id`; `activeRowKey` → фокус
после восстановления; в режиме `select` (V1) перед сменой `aria-activedescendant` — `scrollToIndex`. Режимы `select` и
`bulk` взаимоисключающие — типом (дискриминированный союз пропсов).

**Тесты:** `DataTable.test` › 300 строк → в DOM не все, `aria-rowcount=300`, `aria-rowindex` у строк; `select` +
виртуализация: ↓ до строки вне окна → она в DOM и `aria-activedescendant` указывает на существующий id; «Назад» из
карточки (на `renderApp`) → фокус на строке, с которой ушли; перечитывание при возврате не сдвигает строк (замена
целиком, позиция по ключу).

**Подсадка.** Без `scrollToIndex` перед `aria-activedescendant` — тест «активная вне окна» краснеет (id нет в DOM).

**Ловушки.** `content-visibility` не применяется к строкам таблицы (только карточки). Строки с разной высотой —
`measure`. Ключ позиции — без `PANEL_SEARCH_KEYS` (UF2b.8; фильтры, в том числе `country`, в ключе остаются — I1).
Не проверено ревью: вставляет ли `scrollRestoration` TanStack Router 1.170 инлайн-скрипт (`ScriptOnce`) на клиенте —
исполнитель проверяет по исходникам роутера и пишет в отчёт; ответ подтверждает прод-проект e2e, сценарий (2).

### UF2b.5: массовые действия `bulk`

**Цель.** Флажки, «выбрать видимые», панель «Выбрано N», право на строку, исполнение по одной с ключами, итог по видам
(спека §4.5, стр. 561–572; I14). Экраны с массовыми действиями появятся позже — здесь интерфейс и тесты на стенде.

**Риск:** A. Режимы отказа (таблица обязательна): частичный успех; `202` части строк; 429 посреди пачки (`Retry-After`,
пауза и продолжение); неизвестный исход (`isUnknownOutcome`) части строк — повтор тем же ключом, не новым; 4xx —
повтор новым ключом; `202` не повторяется никогда; права сменились посреди пачки (me перечитан после `403` —
оставшиеся строки перепроверяются `permission(row)` до запроса); строка исчезла из списка при перечитывании во время
пачки; пользователь ушёл со страницы / размонтировал таблицу посреди пачки (запросы в полёте доходят, новые не
стартуют, итог — тостом); двойной клик по действию; выбор строк, которых больше нет в данных после перечитывания;
`concurrency` (по умолчанию 3) не превышается; «выбрать видимые» — только загруженные строки; смена сотрудника —
выбор и итог не переживают `key={me.id}`.

**Файлы.** Создать: `src/ui/table/{bulk.ts,BulkBar.tsx,bulk.test.tsx}`, истории. Изменить: `src/ui/table/{DataTable.tsx,
model.ts}`, `messages/{ru,en}.json`.

**Интерфейсы.**
- `DataTableProps.bulk?: { selected: ReadonlySet<string>; onChange(next: ReadonlySet<string>): void; actions:
  BulkAction<T>[] }`.
- `BulkAction<T> = { id: string; label: string; tone?: 'default' | 'danger'; permission: (row: T) => string; confirm:
  { title: (n: number) => string; word?: string }; run: (row: T, idempotencyKey: string) => Promise<BulkOutcome>;
  concurrency?: number }`; `BulkOutcome = 'executed' | 'pending_approval'`.
- `runBulk(rows, action, { decide, signal, onProgress }) → Promise<BulkReport>` (`bulk.ts`, без React):
  `BulkReport = { executed: T[]; pending: T[]; failed: { row: T; error: ApiError }[]; unknown: { row: T; key: string
  }[]; skipped: T[] }`; ключ — один на строку на действие; «Повторить неудавшиеся» — `failed` новым ключом, `unknown` —
  прежним; после любого `pending` — `useInvalidateApprovals()`.
- Пауза по 429 (m12) — **общая для всей пачки**: 429 любой строки останавливает старт новых запросов у всех
  исполнителей до конца паузы (строка с 429 повторяется тем же ключом); `Retry-After` нет — пауза по умолчанию
  `BULK_RETRY_DEFAULT_MS` (5 с); повторный 429 — пауза снова, без потери строки.
- Строка, где `decide` = `deny` для всех действий, не выбирается (флажок недоступен, причина — `Tooltip`); действие
  в панели доступно, если разрешено хоть для одной выбранной; `ConfirmDialog` называет обе части («Выключить 3
  учётки; ещё 2 вы выключить не можете — пропустим»). «Только с компьютера» — `ActionGate` по праву строки.

**Тесты:** `bulk.test.tsx` (на `runBulk` с контролируемыми промисами, без `sleep`) › 5 строк: 3 `executed`, 1 `202`, 1
`422` → отчёт по видам; 429 с `Retry-After: 2` на второй → пауза (фейковые таймеры) — за паузу не стартует ни один
запрос других исполнителей, затем продолжение, параллельно не больше `concurrency`; 429 без `Retry-After` — пауза
`BULK_RETRY_DEFAULT_MS`; обрыв ответа → `unknown`, «Повторить» шлёт тот же ключ; `422` → «Повторить» — новый ключ;
`202` — в повтор не попадает; отмена `signal` посреди → новые не стартуют; `DataTable` › строка с `deny` — флажок
`disabled` с подсказкой; «выбрать видимые» не выбирает недоступные; подтверждение называет обе части.

**Подсадка.** Повтор `unknown` новым ключом — тест обрыва краснеет; `202` в «Повторить неудавшиеся» — тест краснеет;
без паузы по `Retry-After` — тест 429 краснеет; пауза только у исполнителя, получившего 429, — тест «за паузу ни
одного запроса» краснеет.

**Приёмка.** Таблица режимов отказа с тестом на каждую строку или причиной «гарантирует сервер/стенд».

**Ловушки.** Ключ идемпотентности — через `useAdminMutation` нельзя (он один на действие экрана) — `runBulk` создаёт
ключи сам, но тем же генератором, что `src/api/mutation.ts` (вынести общий `newIdempotencyKey`); `X-WF-Admin` ставит
клиент; `401 auth.reauth_required` посреди пачки — запросы ждут разблокировку (`reauthBus`), это не ошибка строки.

### UF2b.6: тост «Отменить» и `useUndoable`

**Цель.** Обратимые действия — «Отменить» в тосте; тост с «Отменить» сам не исчезает; Esc закрывает тост только при
пустом стеке; на телефоне — снизу над таббаром, `data-toast-open` (спека §4.10, стр. 753–781; §4.5 n5; §4.8 m-h).

**Риск:** A. Режимы отказа (таблица обязательна): обратное упало 4xx (тост-ошибка, исходное остаётся сделанным);
обратное с неизвестным исходом (перечитать цель, сказать факт; повтор — только тем же ключом); `409 *_stale` у обратного
(«изменили — данные обновлены», без отмены); двойное нажатие «Отменить»; следующий тост с «Отменить» пришёл, пока
обратное в полёте (прежний тост заменяется, обратное доходит, итог — отдельным тостом); уход со страницы, пока тост
висит (тост живёт, обратное работает — запрос не зависит от экрана); выход/смена сотрудника — тосты с «Отменить»
снимаются (обратное от имени другого сотрудника недопустимо); Esc при открытой подсказке (закрывает подсказку, тост
остаётся); 401 `reauth_required` на обратном (ждёт разблокировку).

**Файлы.** Изменить: `src/ui/toast/{ToastProvider.tsx,context.ts,Toast.module.css,toast.test.tsx}`,
`src/ui/overlay/{Overlay.tsx,Overlay.module.css}`, `contracts/tokens/tokens.json` + сгенерированные
`packages/tokens/{tokens.css,tokens.ts}` (токен `size.overlay-footer`, n6), `src/ui/layout-css.test.ts`. Создать:
`src/api/undo.ts`, `src/api/undo.test.tsx`, истории.

**Интерфейсы.**
- `useToast().success(text, { undo?: () => Promise<void>; action? })` — с `undo` тост не исчезает сам (как ошибка),
  кнопка «Отменить» (`actions.undo`); новый такой тост заменяет прежний; пока на экране — `data-toast-open` на
  `<html>` (атрибут); на телефоне — однострочный (многоточие, полный текст — читалке), снизу над таббаром.
- **Тосты не над подвалом открытой панели** (m23; §4.5): пока верхняя модальная запись — панель с `footer`, область
  тостов встаёт вне подвала (на широком — над подвалом, на телефоне — над шторкой); признак — атрибут на корне от
  `Overlay` (`data-overlay-footer`), правило — CSS Module тостов. **Высота подвала — токен** `size.overlay-footer`
  (одна строка кнопок: `size.control` + отступы подвала; число — из текущего `Overlay.module.css`, в отчёт), а не замер:
  `setCssPx` из `overlay/*` запрещён стражем CSP (n6). Подвал на широком экране — `min-block-size` по этому токену
  (кнопки в одну строку, при нехватке места — перенос, а не выход за край `Drawer`; R2-m3), отступ тостов — тот же
  токен; оба правила сверяет `layout-css.test.ts`. Приёмка — снимок подвала на английском на 768. Новый токен —
  строка в `design/03-токены.md` и прогон `node --test "packages/tokens/test/*.test.mjs"` (сгенерированное совпадает с
  генерацией; токены — контракт с wf-native, R2-m2).
- Esc в `ToastProvider` — только при `isOverlayStackEmpty()` (UF2a.2), закрывает новейший тост.
- `useUndoable<V, R>(mutation: UseAdminMutation<V, R>, inverse: { run: (v: V, result: R, key: string) =>
  Promise<void>; reread: (v: V, result: R) => Promise<'undone' | 'not_undone'> }): { mutate(v: V, opts: {
  successText: string }) }` (I5) — обратное получает **результат прямого** (`R` — ответ прямой мутации: актуальная
  `version` флага, состояние района после правки); прежнее значение для отмены (имя района до переименования) экран
  кладёт в `v`. Выполнить → тост с «Отменить» → обратное **новым** ключом (новое действие, в журнале отдельной
  записью) → тост итога; 4xx — тост по коду; `409 *_stale` — «изменили — данные обновлены»; `isUnknownOutcome` —
  `reread`, тост по факту.
- Сброс тостов с `undo` — подписка на смену сотрудника/выход (`SessionProvider`).

**Тесты:** `undo.test.tsx` › успех → тост с «Отменить» не исчезает через 8 с; «Отменить» → обратный запрос с ключом ≠
ключу исходного; прямой ответ `{ version: 8 }` → обратный запрос несёт `version: 8` (I5); переименование «А» → «Б» →
«Отменить» шлёт имя «А»; открыт `Drawer` с подвалом + тост → область тостов не перекрывает подвал (атрибут и класс,
m23); `layout-css.test.ts` › высота подвала на широком экране и отступ области тостов — `var(--wf-size-overlay-footer)`,
у подвала нет переноса строк (n6); обрыв ответа обратного → один запрос, `reread`, тост «не отменено»/«отменено» по факту; `409
platform.flag_stale` → тост «изменили», без повтора; двойной клик — один обратный запрос; выход — тост снят;
`toast.test.tsx` › подсказка открыта + тост → Esc закрывает подсказку, тост остаётся; второй Esc — тост (m-h); при
открытом `Drawer` Esc тост не трогает; `data-toast-open` есть, пока тост с «Отменить» на экране.

**Подсадка.** Гейт тоста «нет модальных» вместо «стек пуст» — тест «подсказка → тост» краснеет (тост закрыт первым
Esc); обратное тем же ключом, что исходное, — тест ключа краснеет; повтор при неизвестном исходе — тест «один запрос»
краснеет; `version` из `v` вместо `result` — тест версии краснеет.

**Приёмка.** Критерий обратимого (§4.10, стр. 766–776) записан в `admin.md` списком: подходят — архив/возврат и
переименование района, флаг функции; не подходят — включение страны, версии, приглашение, роль, сброс, выключение,
решения, активация, удаление перевода. Оптимистичных серверных мутаций нет (список в `admin.md` пуст).

### UF2b.7: защита от потери — `useLeaveGuard`, `Overlay.dirty`, вопрос при «Выйти»

**Цель.** Свой диалог при уходе с несохранённого, стандартный — при F5 и закрытии вкладки; один вопрос на один уход
(спека §4.11, стр. 783–834; N4, R2-2).

**Риск:** A. Режимы отказа (таблица обязательна): два вопроса на одно закрытие панели (`Overlay.dirty` + блокировщик);
«Назад»/жест iOS/кнопка Android с грязной панели (должен спросить ровно раз); «Остаться» — адрес и введённое на месте;
успех формы → переход без вопроса (сброс `isDirty` до перехода); `beforeunload` висит на чистой форме (не должен);
выход при грязной форме — вопрос в `useLogout` до POST (редирект роутера идёт с `ignoreBlocker`); разблокировка после
бездействия — без вопроса и без потери; конец сессии — без вопроса, вернётся только черновик; переход палитрой и `g x`
(UF4) — тот же блокировщик; второй вопрос отрисовался на следующем такте (проверка на установившемся состоянии, m-m).

**Файлы.** Создать: `src/app/leaveGuard.ts` (+ `leaveGuard.test.tsx`). Изменить: `src/ui/overlay/{Overlay.tsx,
overlays.test.tsx}`, `src/app/logout.ts`, `messages/{ru,en}.json` (`ui.leave.{title,text,stay,leave}`).

**Интерфейсы.**
- `useLeaveGuard(dirty: boolean, opts?: { title?: string; text?: string }): void` — поверх `useBlocker`
  (`shouldBlockFn`, `withResolver`, `enableBeforeUnload: dirty`): свой `ConfirmDialog` «Уйти без сохранения? Введённое
  пропадёт», «Остаться» — фокус по умолчанию. Исключений по ключу панели нет.
- `OverlayProps.dirty?: boolean` — Esc, фон, «Закрыть» при `dirty` — тот же диалог (вложенный оверлей); после «Уйти» —
  закрытие навигацией с `ignoreBlocker: true` (панель в адресе — UF2b.8). `ConfirmDialog` своего `dirty` не имеет.
- `useLogout()` — при зарегистрированных грязных формах (реестр `useLeaveGuard`) — вопрос до POST.

**Тесты** (все — `renderApp(path, { history: 'browser' })`, «ровно один» — `waitFor` с `getAllByRole(…)` длины 1 после
того, как адрес и панель пришли в конечное состояние): форма страницы — пункт меню при вводе → ровно один вопрос;
«Остаться» — адрес и ввод на месте; успех отправки → переход без вопроса; чистая форма — `beforeunload` не повешен;
«Выйти» при вводе → вопрос до POST, «Остаться» — POST не ушёл. Тесты панелей в адресе — UF2b.8.

**Подсадка.** `enableBeforeUnload: true` всегда — «чистая форма не вешает beforeunload» краснеет.

**Ловушки.** `useBlocker` 1.170: сверить имена опций (`shouldBlockFn`, `withResolver`, `enableBeforeUnload`) и
`ignoreBlocker` у `navigate`/`history.back`. Черновик `useDraft` пишется только по вводу — после «Уйти» его судьба
прежняя (§9.3 спеки сотрудников).

### UF2b.8: панели в адресе, `PANEL_SEARCH_KEYS`, история, право и чужой id

**Цель.** Открытая панель — состояние экрана в адресе; «Назад» и F5 работают; возврат к родителю не открывает пустую
панель (спека §4.6, стр. 590–617; I6, m9, m10, R2-2).

**Риск:** A. Режимы отказа (таблица обязательна): панель открыта изнутри → закрытие `history.back()`; открыта глубокой
ссылкой → закрытие `navigate` без ключа с `replace`; уход из панели на другой путь (успех активации → город) —
`replace`, браузерная «Назад» — на список без панели; `BackLink` после панели — адрес родителя без ключей панели; ключ
без права (`?invite=1` без права выдачи, `?account=` без `staff.accounts.read`) — панель не открывается, ключ снимается
`replace`; `?account=<id>` → 403/404 — одно состояние «Сотрудник не найден»; F5 на открытой панели; грязная панель +
Esc → «Уйти» → ровно один вопрос (оба варианта открытия); грязная + `window.history.back()` → ровно один вопрос,
«Остаться» — `?invite=1` и почта на месте; права сняли, пока панель открыта; ключ панели совпал с ключом фильтра
(`BackMemory` вырезал бы фильтр — I1); двойной Esc или клик «Закрыть» до `popstate` (два `history.back` увели бы из
раздела — m13).

**Файлы.** Изменить: `src/app/paths.ts` (`PANEL_SEARCH_KEYS`, `FILTER_SEARCH_KEYS`), `src/ui/shell/BackLink.tsx`
(`BackMemoryProvider` вырезает ключи), `src/app/router.tsx` (`validateSearch` ключей), `features/*/search.ts`
(экспорт ключей своих фильтров), создать `src/app/panels.ts` (+ тест), `src/app/searchKeys.test.ts`.

**Интерфейсы.**
- `PANEL_SEARCH_KEYS = ['account', 'invite', 'activate', 'entry', 'edit'] as const` — панель страны (G9 Task 21) —
  `?edit=<код>`, не `?country=` (спека §4.6, §15; решение контроллера по I1).
- `FILTER_SEARCH_KEYS: Record<путь, readonly string[]>` — ключи фильтров каждого пути из `features/*/search.ts`
  (`/audit`: `tab, action, actor, object_type, object_id, from, to`; `/staff`: `q, status, role`; `/approvals`: `box`;
  `/geo/cities`: `q, status, country, region_id`; G9 добавит `/geo/imports` (`country`) и свои). Ключи объявлены
  **рядом с парсером**: каждый `features/*/search.ts` экспортирует `KEYS` (`as const`), и его парсер разбирает и
  возвращает только `KEYS` (обходом списка, а не отдельными литералами) — ключ парсера вне списка невозможен по
  построению (n7); `FILTER_SEARCH_KEYS` собирается из этих `KEYS`.
- `usePanelParam<K extends PanelKey>(key: K, opts: { allowed: boolean }) → { value: string | undefined; open(v:
  string): void; close(): void }` — `open` — `navigate` с `push` и `state: { panel: true }`; `close` — при метке
  `history.back({ ignoreBlocker: true })`, без метки — `navigate` без ключа, `replace: true`, `ignoreBlocker: true`;
  `close` идемпотентен в пределах одного открытия (повторный вызов до смены адреса — ничего; m13); `allowed: false`
  при ключе в адресе — снять ключ `replace` без открытия.
- `leavePanelTo(to)` — `navigate({ to, replace: true })`.

**Тесты** (browser history; §4.11, стр. 810–823): грязное приглашение открыто изнутри → ввод почты → Esc → «Уйти» →
ровно один `ConfirmDialog`, панель закрыта, адрес без `invite`; то же глубокой ссылкой (`/staff?invite=1`) → ровно
один; открыто изнутри → ввод → `window.history.back()` → один диалог «Уйти без сохранения» → «Остаться» →
`location.search` = `?invite=1`, почта в поле, диалогов нет; грязное приглашение → пункт меню → ровно один;
`CitiesPage.test` › активация → город → `BackLink` → списка с открытой активацией нет; то же браузерной «Назад»;
`?invite=1` у модератора без права — панели нет, ключ снят; `?account=<id>` → 404 и 403 — одинаковый текст;
`CitiesPage.test` (V1, без правки) › `/geo/cities?country=RU` → город → `BackLink` → `/geo/cities?country=RU`; панель
открыта изнутри → двойной Esc (и «Закрыть» дважды) → ровно один `history.back`, адрес — список раздела;
`searchKeys.test.ts` (страж I1) › `PANEL_SEARCH_KEYS` ∩ ключи `FILTER_SEARCH_KEYS` всех путей = ∅ и ∩ имена
query-параметров списочных операций контракта admin (`api/gen/admin.d.ts`, текстом) = ∅; тип результата каждого
парсера — `Partial<Record<(typeof KEYS)[number], …>>` (лишний ключ не скомпилируется), а тест на объекте с ключами
`KEYS` и посторонним ключом возвращает только `KEYS` (n7).

**Подсадка.** Закрытие без `ignoreBlocker` — первые два теста видят второй диалог; исключение для ключа панели в
`shouldBlockFn` — тест «Назад» видит ноль диалогов; без вырезания ключей в `BackMemory` — тест `BackLink` краснеет;
вернуть `country` в `PANEL_SEARCH_KEYS` — страж пересечения и тест V1 `CitiesPage.test` краснеют; `close` без защиты от
повтора — тест двойного Esc краснеет.

**Ловушки.** Тесты на memory history здесь вакуумны (R3-1) — только `history: 'browser'`. Метка `state.panel`
переживает F5 (browser history хранит `state`) — после F5 закрытие `back()` всё ещё верно.

### UF2b.9: ленивые маршруты, предзагрузка, View Transitions

**Цель.** Каждый раздел — свой кусок; предзагрузка по намерению; переходы «список → карточка» и смена вкладок (спека
§4.15, стр. 934–945).

**Риск:** B.

**Файлы.** Изменить: `src/app/router.tsx` (+ `router.test.tsx`), `src/ui/global.css` (`::view-transition-group(*)` при
reduced motion), `src/ui/Card.tsx`/`CityCards.tsx` (класс `.vtSource` на нажатую карточку), `PageHeader` (имя
перехода у заголовка). Создать: `src/ui/motion.ts` (+ тест) — `useReducedMotion(): boolean` по `matchMedia`
`(prefers-reduced-motion: reduce)` с подпиской (§2.7; единственный JS-потребитель сейчас — переходы: при reduced
motion навигация без `viewTransition`; m22).

**Интерфейсы.** Разделы `features/<раздел>` — `lazyRouteComponent`/`.lazy()` (сверить API 1.170); экраны входа и
каркас — в основном куске; `defaultPreload: 'intent'`; `pendingComponent` — скелетон раздела; ошибка загрузки куска —
`ErrorState` с «Обновить страницу»; `viewTransition` у навигации «список → карточка» и вкладок; одно статическое имя
`view-transition-name: wf-entity` у `.vtSource` и заголовка назначения (CSP: без инлайн-имён).

**Тесты:** `router.test.tsx` › раздел открывается (ленивый кусок в Vitest грузится); ошибка импорта куска (подмена) →
«Обновить страницу»; нет `document.startViewTransition` → переход мгновенный без ошибки; `motion.test.ts` › reduced
motion → `true`, смена настройки — обновление; при reduced motion навигация «список → карточка» без `viewTransition`.

**Подсадка.** Без `errorComponent` у ленивого маршрута — тест ошибки куска краснеет.

**Ловушки.** `features/*/commands.ts` (UF4) — в основном куске и экранов не импортируют (страж UF4.3). Скелетон
раздела — из `Skeleton.*`, не «Загрузка…» (страж UF2a.8).

### UF2b.10: применение к экранам (спека §6.1, строки UF2b), виртуализация `Select`, документы

**Цель.** Экраны плана A и G8 на поведении UF2b; документы группы.

**Риск:** A (панели в адресе и защита от потери на живых экранах).

**Правки** (§6.1, стр. 1162, 1166, 1168):
- «Роли и доступ»: панель сотрудника — `?account=`, приглашение — `?invite=1` (`usePanelParam`); формы —
  `useLeaveGuard`/`Overlay.dirty`; `tableId: 'staff'`, плотность и столбцы; матрица прав — `stickyFirstColumn` при
  `layout: 'table'` (I6; тест `StaffPage.test` › 390 + `coarse` — у первой ячейки строки класс закрепления).
- «Журнал»: раскрытая запись — `?entry=`; `tableId: 'audit'`.
- «Города и запуск»: активация — `?activate=1`, переход из панели на город — `leavePanelTo` (`replace`); «В
  архив»/«Вернуть» и переименование района — `useUndoable`; формы города — `useLeaveGuard`; карточка → страница — View
  Transition.
- `Select`: виртуализация списка (`useVirtualRows`, `scrollToIndex` активной) — **только если замер** (250 стран в
  списке на телефоне и широком экране, время открытия и прокрутки в отчёт) показал нужду; иначе — запись «не нужна» с
  числами (n2).
- Прод-e2e, сценарий (2): открыть экран, где на данных стенда e2e включается виртуализация (журнал при > 200
  записях или виртуализованный `Select`); если таких данных нет — запись в отчёт, что `virtual/*` под настоящей CSP
  проверит G9 (матрица флагов, горизонтальная виртуализация), и ⚠️ в брифы G9.

**Тесты:** тесты экранов зелёные; новые — по образцу UF2b.8 (по одному на экран: F5 на открытой панели, «Назад»).

**Документы.** `.claude/rules/admin.md` — таблицы (новые пропсы `tableId`, `priority`, `bulk`, `virtualize`,
`stickyFirstColumn`, плотность), панели в адресе, `PANEL_SEARCH_KEYS` и правило «ключ панели ≠ ключ фильтра» со
стражем, `useUndoable` (результат прямого — в обратное) и критерий обратимого, `useLeaveGuard`/`Overlay.dirty`, список
оптимистичных мутаций (пуст), ленивые маршруты, View Transitions, `useReducedMotion`. Строка `docs/versions.md`
(`@tanstack/react-virtual`) — в отчёт UF2b.1, вносит контроллер (I8).

## Группа UF3 «Витрина и визуальные тесты»

Спека §7 (стр. 1243), §5.1–§5.3. Риск: C (витрина, истории), B — workflow CI. Один исполнитель; идёт параллельно с
UF2a и UF2b, правит только свои пути (не `ui/index.ts`, не `messages/*`, не компоненты UF2a/UF2b, не
`src/showcase/csf.ts` — его форма заморожена UF1a.3, m16). Общие файлы — по таблице владения (I8): в волне
UF2a/UF2b ∥ UF3 группа UF3 владеет `docs/versions.md`, `CLAUDE.md`, `.claude/agents/wf-reviewer.md`, корневым и
админским `Taskfile.yml`; `.claude/rules/frontend.md` и `admin.md` не правит — строки в отчёт. Старт — после ревью UF1b
(историям нужны темы). Установку Ladle для пробы делает контроллер (U2, I7), **до** старта UF2a/I.

### UF3.1: проба Ladle и выбор витрины

**Цель.** Решить по пробе: Ladle 5.1.1 (тянет свой Vite 6, исключение в `docs/versions.md`) или своя витрина на нашем
Vite 8 (спека §5.1, стр. 999–1022).

**Риск:** C.

**Что проверить (час-два, результат — в отчёт и одной строкой пользователю в итоге группы).** Ladle 5.1.1 поднимает
`src/ui` с CSS Modules, `@wf/tokens` (`light-dark()`), `@fontsource`, use-intl; конфиг `vite.config.ts` админки ему не
мешает (иначе — `.ladle/vite.config.ts`); собственный переключатель темы Ladle не перетирает наш `data-theme` на
`<html>` (перетирает — тема из нашего параметра адреса); тему и историю можно задать адресом
(`?story=<id>&mode=preview` + параметр темы); `ladle build` даёт статику, которую снимает Playwright; его msw 2 не
пересекается с нашим (истории без API).

**Порядок пробы — с точкой остановки исполнителя** (I7; решение контроллера 03.11). Запустить Ladle без установки
нельзя, а исполнителю pnpm запрещён (U2):
1. Исполнитель готовит пробу без установки: `.ladle/{components.tsx,config.mjs}` (и `.ladle/vite.config.ts`, если
   нужен), 3–4 истории-образца (`Button`, `Segmented`, поле, `Modal`) на типах `csf.ts`, перечень проверок выше. Затем
   **останавливается** со статусом «ждёт установки Ladle» (раздел отчёта «Проба: готово к установке»).
2. Контроллер в тихом окне (никто не гоняет процессов из `node_modules`, dev-сервер пользователя остановлен) ставит
   `pnpm --filter @wf/admin add -D @ladle/react@5.1.1` и возобновляет того же исполнителя (`SendMessage`). При установке
   контроллер же проверяет, не требует ли Ladle (esbuild для его Vite 6) разрешения сборочных скриптов: pnpm 12 роняет
   install на пакете со скриптом вне `allowBuilds` (`pnpm-workspace.yaml`); решение (`true`/`false` со строкой-причиной)
   — коммитом контроллера вместе с установкой, откат — вместе с откатом (ревью, «не проверено»).
3. Исполнитель гоняет пробу бинарником (`node_modules/.bin/ladle serve`/`build` из `apps/admin`, без pnpm) и пишет
   исход. **Ладл** — коммит `package.json` + `pnpm-lock.yaml` вместе с `.ladle/*` (своими путями). **Отказ** —
   исполнитель снова останавливается («проба не прошла»); контроллер в тихом окне откатывает установку
   (`pnpm --filter @wf/admin remove @ladle/react` и проверка, что `package.json`/lock совпали с `HEAD`) и возобновляет
   исполнителя — он удаляет `.ladle/*` и идёт по запасному пути.

**Исход.** Ладл — `devDependencies` админки, строка-исключение в `docs/versions.md` («Ladle 5.1.1 / Vite 6 внутри —
последний выпуск; поддержка Vite 8 в main, не выпущена; апгрейд — с выходом 5.2/6.0»). Своя витрина — без новой
зависимости, строки в `versions.md` нет. Истории в обоих случаях — CSF (U12). UF2a/I стартует только после исхода
(тихое окно контроллера не должно совпасть с тестами UF2a).

### UF3.2: оболочка витрины, обёртка провайдеров, `admin:stories`

**Цель.** Смотреть все истории в обеих темах и двух языках; адрес истории для снимков (спека §5.2).

**Риск:** C.

**Файлы.** Ладл: `.ladle/{components.tsx,config.mjs}` (+ `.ladle/vite.config.ts`, если нужен). Своя: `showcase.html`,
`src/showcase/{main.tsx,Showcase.module.css}` (`import.meta.glob('../**/*.stories.tsx')`, список слева, переключатели
темы, языка и ширины, адрес `?story=<id>&theme=<t>&locale=<l>`, ~200 строк). Оба: `src/showcase/csf.ts` — уже
есть (UF1a.3), не меняется (m16); `src/showcase/Provider.tsx` (обёртка: `IntlProvider` ru/en, `ToastProvider`,
`QueryClient` без сети, `ThemeProvider`, подменённый `useNow` на `2026-10-28T12:00:00Z`), `Taskfile.yml` админки
(`stories` — `serve` локально, `stories:build` — статика для CI).

**Интерфейсы.** `src/showcase/csf.ts` (форму задаёт UF1a.3 по этой записи): `type StoryMeta = { title: string;
visual?: boolean; layout?: boolean }`, `type Story<P> = { (args: P): ReactNode; storyName?: string; meta?: StoryMeta
}` (формат совместим с Ladle; обёртка Ladle —
`.ladle/components.tsx` переиспользует `Provider`). Список историй для снимков — JSON (`meta.json` Ладла или свой
экспорт оболочки): `{ id, title, visual, layout }[]`.

**Тесты:** `src/showcase/showcase.test.tsx` (если своя) › разбор адреса истории; обёртка ставит `data-theme` по
параметру; незнакомая история — понятное сообщение.

**Приёмка.** `./task admin:stories` открывает витрину на Windows; переключение темы меняет вид без перезагрузки.

**Ловушки.** Витрина — вне прод-сборки (`showcase.html` — отдельный вход только у `stories:build`); страж UF1a.1
(граф от `main.tsx`) её не видит — так и задумано. `csp.test.ts` читает и истории — они тоже без `style=`.

### UF3.3: истории существующих компонентов `src/ui`

**Цель.** Каждый экспортируемый компонент — истории всех состояний в обеих темах (спека §5.2, стр. 1032–1036).

**Риск:** C.

**Файлы.** Создать `*.stories.tsx` рядом с компонентами плана A, G8 и V1 (`Button`, `StatusChip`, `CountBadge`,
`Avatar`, `QrCode`, `Segmented`, `Switch`, `Card`, `OtpInput`, поля формы (кроме тех, что переделывает UF2a — их
истории пишет UF2a), `Modal`/`Drawer`/`Sheet`/`ConfirmDialog`, тосты, состояния, `PageHeader`, `Tabs`, `AppShell`,
`DataTable` (базовые состояния; новые пропсы — UF2b), `ThemeSwitch`; `Icon`/`Illustration` — уже UF1a.3).
Истории `AppShell`, оверлеев и `DataTable` пишутся, пока UF2a.2/UF2a.5/UF2b.2 меняют их пропсы (m17): перед каждым
коммитом историй — перечитать текущие пропсы этих компонентов в дереве; расхождение после чужого коммита ловит
`admin typecheck` контроллера между заходами — чинит UF3 своим коммитом.

**Правила.** Одна история на состояние, имена — `Default`, `Loading`, `Empty`, `Error`, `Disabled`, `Busy`,
`LongText`, `Mobile`; у компонента с данными — все пять состояний; тексты — из локалей; даты — фиксированные; метка
`meta: { visual: true }` — только ключевые (кнопки, поля, таблица, состояния, тосты, оверлеи, карточка каркаса), `layout:
true` — компонентам с раскладкой (снимаются на трёх ширинах). Набор `visual` — ограниченный (оценка §5.3: 120–200
PNG на обе темы).

**Приёмка.** Число историй `visual` и оценка PNG — в отчёт; порог вопроса пользователю — набор > 20 МБ или job > 12 мин.

### UF3.4: страж историй и дымовой прогон

**Цель.** Новый компонент без историй — падение; сломанная история — падение без роста набора снимков (спека §5.2,
стр. 1037–1038; §5.3 «дымовой прогон»).

**Риск:** C.

**Файлы.** Создать: `src/ui/stories.test.ts`.

**Интерфейсы.** Страж: каждый компонент, экспортируемый из `ui/index.ts`, имеет `*.stories.tsx` рядом; исключения —
списком с причиной (провайдеры, хуки, функции). Компоненты UF2a/UF2b, которые ещё не слиты к моменту коммита стража,
стража не касаются (страж читает текущий `ui/index.ts`); каждый их коммит приносит истории сам (U12).

**Тесты:** страж › «стражу есть что читать»; удалить истории `Button` (подсадка) — падение со списком.

**Дымовой прогон** — часть job `snapshots` (UF3.6): каждая история отрисовалась, ошибок консоли нет.

### UF3.5: `admin-visual.yml` — job `baseline` и `scripts/visual-baseline.mjs`

**Цель.** Отдельный workflow без `paths`: база PR, фильтр визуальных путей, поиск эталона — артефакта подходящего
прогона `main`, проверка срока (спека §5.3, стр. 1057–1112; N1, m-d…m-g).

**Риск:** B. Режимы отказа (таблица — желательно, ревью пройдёт их): эталона нет (первый прогон, ни один не подходит,
артефакт истёк) — зелёный пропуск с предупреждением; `main` обновился во время PR — база `HEAD^1` merge-коммита; прогон
`main` ещё идёт/упал — не эталон; посторонняя метка (`docs`) не отменяет идущее сравнение; `labeled visual-change` без
нового пуша перезапускает сравнение; PR из форка (токен только на чтение — артефакты читаются, комментарии не пишутся);
версии образа Playwright у эталона и PR разошлись — сравнение информативное.

**Файлы.** Создать: `.github/workflows/admin-visual.yml` (оба job), `scripts/visual-baseline.mjs`,
`scripts/visual-baseline.test.mjs`. Изменить: CI-job, который гоняет проверки репозитория (`repo.yml`; шаг `./task
scripts:test` — задача заведена в UF1a.1, U21; сверить по месту).

**Интерфейсы.**
- Триггеры: `push` в `main` и `pull_request` с `types: [opened, synchronize, reopened, labeled, unlabeled]` — без
  `paths`; `permissions: { contents: read, actions: read, pull-requests: read }`; `concurrency`: группа
  `admin-visual-<номер PR или SHA>`, для `labeled`/`unlabeled` с меткой не `visual-change` — плюс `-<run_id>`;
  `cancel-in-progress: true`.
- Job `baseline` (`ubuntu-latest`, `actions/checkout` с `fetch-depth: 0`), условие для `labeled`/`unlabeled` —
  `github.event.label.name == 'visual-change'`; выходы `base`, `changed`, `run-id`, `pr`, `image`.
- `scripts/visual-baseline.mjs` — чистые функции с внедряемым исполнителем команд: `VISUAL_PATHS` (один список §5.3,
  стр. 1080–1083); `findBase(run)` → `git rev-parse HEAD^1`; `changed(run, base)` → `git diff --name-only <база> HEAD
  -- <пути>` непуст; `findBaseline(run, base)` → `gh run list --workflow admin-visual.yml --branch main --event push
  --status success --json databaseId,headSha --limit 100`, первый с `git merge-base --is-ancestor <headSha> <база>` и
  `git diff --quiet <headSha> <база> -- <пути>`; `artifactAlive(run, runId)` → `gh api …/actions/runs/<id>/artifacts`,
  `expired: false`; `prOfCommit(run, sha)` (для `push`) → номер и метки PR; запись выходов в `GITHUB_OUTPUT`.
- Все действия — по полному SHA с тегом в комментарии (как в `admin.yml`); `actions/upload-artifact`/
  `download-artifact` (v8) — строки в `docs/versions.md`.

**Тесты:** `visual-baseline.test.mjs` (подменённый исполнитель) › нет визуальных изменений → `changed=false`; эталон —
первый предок базы без изменений путей между ними (второй в списке, первый — не предок); артефакт `expired` → пусто;
список пуст → пусто; `labeled` с другой меткой — группа `concurrency` уникальна (функция ключа группы, если вынесена).
Workflow — `actionlint` (`./task workflows`, lint своего пути).

**Подсадка.** Брать `pull_request.base.sha` вместо `HEAD^1` — тест «main ушёл вперёд» краснеет; без проверки
«между ними нет изменений путей» — тест второго кандидата краснеет.

**Ловушки.** В образе Playwright может не быть `git`/`gh` — поэтому поиск эталона только в `baseline` на раннере
(m-d). Проверка в защите `main` **не обязательна** на старте (решение контроллера) — в настройки репозитория ничего не
добавлять.

### UF3.6: job `snapshots` — снимки, сравнение, отчёт

**Цель.** Снимки ограниченного набора в закреплённом контейнере Playwright, сравнение с эталоном, отчёт «было / стало
/ разница», артефакт эталона на `main` (спека §5.3, стр. 1076–1125).

**Риск:** B.

**Файлы.** Создать: `visual/{playwright.visual.config.ts,stories.visual.spec.ts,screens.visual.spec.ts}` (в
`apps/admin`), изменить `admin-visual.yml` (job `snapshots`), `Taskfile.yml` админки (`visual` — только для CI;
локально тесты пропускаются с понятным текстом), `vite.shots.ts` (UF1a.1; m4) — в режиме `shots` при сборке плагин
кладёт `mockServiceWorker.js` в выход, `build.outDir` режима — `dist-shots` (не `dist`: его отдаёт прод-проект e2e и
проверяет `check-admin-dist.mjs`), `dist-shots` — под `.gitignore` (сверить корневой; нет — строка там), в
`globalIgnores` eslint админки (`eslint.config.mjs` в волне — UF2a: строка в отчёт, I8).

**Интерфейсы.**
- `snapshots`: `needs: baseline`, `if: needs.baseline.outputs.changed == 'true'` (на `push` в `main` — всегда);
  `container: mcr.microsoft.com/playwright:v<версия @playwright/test>-noble` (тег сверить при реализации).
  Шаги: сборка витрины (`stories:build`) и сборки режима `shots` → снимки → скачивание эталона по `run-id` (если есть)
  → сравнение → отчёт в `GITHUB_STEP_SUMMARY` и артефакт отчёта; на `main` — артефакт `visual-baseline-<sha>`
  (`retention-days: 90`, метаданные — версия образа) + информативное сравнение с прошлым эталоном и `::warning
  title=Вид изменён без метки::PR #<номер>: <число> отличий…`, если у PR коммита нет метки `visual-change` (m-f).
- Набор: истории `visual: true` (снимок корня истории `locator.screenshot`), `layout: true` — на 390/768/1440, прочие —
  одна ширина; обе темы; 6–8 экранов режима `shots` (вход, главная — после UF4, журнал, «Роли и доступ», «Города и
  запуск», страница города). Прочие истории — дымовой прогон (отрисовалась, нет ошибок консоли).
- Стабильность: `animations: 'disabled'`, `caret: 'hide'`, `reducedMotion: 'reduce'`, `timezoneId: 'UTC'`, `locale:
  'ru-RU'`, `document.fonts.ready`, фиксированные даты и `useNow`; допуск `maxDiffPixelRatio: 0.001`.
- Сравнение — без новой зависимости (U17): `toHaveScreenshot`/`toMatchSnapshot` Playwright с каталогом эталона,
  подложенным из артефакта; метка `visual-change` — отчёт без падения; эталона нет — пропуск с предупреждением в
  сводке; версии образа разошлись — только информативно. `test.skip(process.platform !== 'linux', 'визуальные тесты —
  только в Linux-CI: шрифты Windows дают ложные отличия')`.

**Тесты.** Локально — только `--list` (набор собирается, на Windows всё помечено пропуском). Первый реальный прогон —
в CI на PR этой ветки (эталона ещё нет → зелёный пропуск с предупреждением — ожидаемо).

**Приёмка.** Время job и размер набора — из первого прогона CI в отчёт контроллера; порог вопроса пользователю —
набор > 20 МБ или job > 12 мин.

**Ловушки.** `toHaveScreenshot` при отсутствии эталона пишет новый и падает — запуск с `--update-snapshots=missing`
либо явная ветка «эталона нет»; снимок корня истории, а не страницы 1440 (меньше шума); шрифты — `@fontsource`, не
системные. Экраны в CI — сборка режима `shots` (`vite build --mode shots` в `dist-shots`); обычная сборка — без
воркера (страж UF1a.1 и `check-admin-dist.mjs`, UF1b.4).

### UF3.7: документы UF3

**Цель.** Строки §6.4 для UF3.

**Риск:** C.

**Содержание.** `CLAUDE.md` «Проверки»: `./task admin:stories`; визуальные тесты — только CI, эталоны — артефакт
последнего зелёного `main` (не в репозитории), метка `visual-change`; проверка `admin-visual` не обязательна, критерий
перевода — §5.3 (стр. 1113–1120). `.claude/agents/wf-reviewer.md` — пункт чек-листа ревью фронта «красный visual без
метки — не одобрять; с меткой — открыть дифф глазами». `docs/versions.md` — Ladle (исключение) или ничего; образ
Playwright в CI; `upload-artifact`/`download-artifact` (SHA). Строки для `.claude/rules/frontend.md` (витрина и
скриншоты, эталоны — артефакт `main`) и `admin.md` (`admin:stories`, истории рядом с компонентом, метки `visual`/
`layout`) — в отчёт, вносит контроллер после ревью (I8: владелец этих файлов в волне — UF2a/UF2b).

## Группа UF4 «Главная и палитра»

Спека §7 (стр. 1244), §4.1, §4.2, §4.13, §3.3 (абзац «Меню»). Риск: B (недавнее в `localStorage` — категория «общее
устройство»: таблица режимов для UF4.5 обязательна). Старт — после ревью UF2b/II и UF3. Один исполнитель, один заход.

### UF4.1: главная — маршрут, стартовая страница, пункт меню

**Цель.** `/` — «Что требует внимания» внутри каркаса; стартовая после входа для всех; `?return=` важнее (спека §4.13,
стр. 894–899; решение пользователя 28.10).

**Риск:** B.

**Файлы.** Изменить: `src/app/{nav.ts,nav.test.ts,guard.ts,guard.test.ts,router.tsx,router.test.tsx,paths.ts}`.
Создать: `src/features/home/HomePage.tsx` (каркас страницы; плитки — UF4.2).

**Интерфейсы.** `NAV`: первый пункт «Главная» (`paths.root`, `permission: null`, иконка `home`); `startPath(me)` →
`paths.root`; маршрут `/` — ребёнок `shellRoute` (сейчас — редирект на первый раздел); таббар телефона — «Главная»
первой (U10).

**Тесты:** `guard.test.ts` › вход без `return` → `/`; с `return=/staff` → `/staff`; `nav.test.ts` › `startPath` любой
роли — `/`; «Главная» первая и без права.

**Подсадка.** `startPath` → первый раздел — тест входа краснеет.

### UF4.2: плитки, список дел, `AttentionTile`, `TodoList`

**Цель.** Индикаторы и список дел из существующих чтений, контракт не меняется (спека §4.13, стр. 901–925).

**Риск:** B.

**Файлы.** Создать: `src/ui/home/{AttentionTile.tsx,TodoList.tsx,Home.module.css}` (+ тесты, истории),
`src/features/home/{HomePage.tsx,queries.ts,HomePage.test.tsx}`. Изменить: `messages/{ru,en}.json`, `src/test/`
(мок главной в `sectionsServer`).

**Интерфейсы.**
- `AttentionTile({ title: string; value: number | null; cap?: number; tone: ChipTone; href: string; state: 'loading' |
  'error' | 'ready'; onRetry: () => void; more?: boolean })` — вся плитка — ссылка (имя = заголовок + число); «99+»
  при `value ≥ 100` или `more`; ошибка — «не загрузилось · Повторить» внутри плитки.
- `TodoList({ items: { id: string; text: string; href: string; tone? }[] })`; пусто — `Illustration` и «Всё спокойно».
- Плитки (порядок фиксирован): входящие подтверждения (`self`, `useIncomingApprovalsCount`; на карманном устройстве
  по `staff.*` — «решать с компьютера»), мои запросы (`self`, `listApprovals?box=mine&status=pending&limit=100`,
  ближайший срок — `RelativeTime`), импорт GeoNames (`geo.imports.read`, `listGeoImports?limit=5`: **среди последних
  5 записей журнала импорта** последний по стране упал или идёт > 30 мин — S4: точный «последний по каждой стране» —
  после разморозки контракта; подпись плитки не обещает больше), города в ожидании (`geo.cities.read`, `listGeoCities?status=waitlist&limit=100`). Личное —
  «Осталось N резервных кодов» (`getStaffMfa`, ≤ 3) → `/me`. Все чтения — `...background()`, без интервала (кроме уже
  опрашиваемого счётчика), при открытии и возврате во вкладку.
- Ссылки списка дел — через объект `search` роутера (`/geo/cities` — `{ status: ['waitlist'] }`, массив;
  `/geo/imports` — `{ country: 'RU' }`, U18).

**Тесты:** `HomePage.test.tsx` › модератор — без плитки импорта и без её запроса; ошибка одной плитки не роняет
остальные; 100 строк → «99+», 99 + `next_cursor` → «99+», 99 без курсора → «99»; все запросы с `X-WF-Background: 1`;
пусто — «Всё спокойно»; телефон (390 + `coarse`) — входящие `staff.*` с «решать с компьютера».

**Подсадка.** Плитка импорта без проверки права — тест модератора краснеет (неописанный запрос MSW).

**Ловушки.** `/geo/imports` до G9 — заглушка `pendingSection`; ключ `country` в её `validateSearch` — ⚠️ G9 Task 20
(U18). Снимки главной на трёх ширинах — в отчёт; главная входит в набор экранов CI (UF3.6) после этой задачи.

### UF4.3: реестр команд и страж импортов

**Цель.** Команды объявляют модули рядом с экранами; основной кусок сборки не тянет экраны (спека §4.1, стр. 413–421;
m17).

**Риск:** B.

**Файлы.** Создать: `src/app/commands.ts` (+ `commands.test.ts`), `src/features/{staff,geo,audit,approvals,me}/
commands.ts`.

**Интерфейсы.** `type Command = { id: string; group: 'section' | 'action'; labelKey: string; keywordsKey?: string;
icon?: IconName; permission?: string | null; desktopOnly?: boolean; shortcut?: string; run: (ctx: CommandContext) =>
void }`; `CommandContext = { navigate; setTheme; setLocale; setDensity; openShortcuts; logout }` (`setDensity` — из
`useDensity`, UF2b.3; m9). Общие — «Тема: светлая/тёмная/
системная», «Язык», «Плотность таблиц», «Горячие клавиши», «Выйти». Действия, которым нужен экран, — переход с ключом
панели (`/staff?invite=1`, `/geo/cities?activate=1`) — работает и из адреса, и после F5 (UF2b.8).

**Тесты:** `commands.test.ts` › импорты файлов `features/*/commands.ts` — только из разрешённого списка (`paths`,
типы, ключи локалей, хуки прав, `desktopOnly`); у каждой команды `labelKey` есть в ru и en; id уникальны.

**Подсадка.** Импорт `StaffPage` в `features/staff/commands.ts` — страж краснеет.

### UF4.4: `fuzzyRank`

**Цель.** Свой нечёткий поиск по ~50 локальным строкам с раскладкой (спека §4.1, стр. 423–428).

**Риск:** B.

**Файлы.** Создать: `src/ui/palette/{fuzzy.ts,fuzzy.test.ts}`.

**Интерфейсы.** `fuzzyRank<T>(query: string, items: readonly T[], opts: { fields: (item: T) => string[] }) → { item: T;
score: number; ranges: [number, number][] }[]` — регистр, `ё`→`е`, NFKC; раскладка ru↔en («ujhjl» → «город»);
английские ключевые слова (`keywordsKey`) при русском интерфейсе; ранжирование «с начала > с начала слова >
подпоследовательность»; диапазоны совпадения для `<mark>`.

**Тесты:** таблица случаев › «гор» → «Города и запуск» выше «Импорт GeoNames»; «ujhjl» → «город»; «ёлка»/«елка»;
«city» при ru; «жрн» → «Журнал» (подпоследовательность) ниже «журнал…» с начала; пустой запрос — исходный порядок;
диапазоны точные.

**Подсадка.** Без таблицы раскладки — случай «ujhjl» краснеет.

### UF4.5: `CommandPalette` — группы, сущности, недавнее, доступность

**Цель.** Палитра Ctrl/⌘+K: недавнее, разделы, действия, сущности с сервера; шаблон combobox + listbox (спека §4.1,
стр. 396–446).

**Риск:** B. Режимы отказа (таблица обязательна): гонка ответов поиска сущностей (последний побеждает, отмена
`AbortSignal`); запрос по праву, которого нет (не уходит); ошибка одной группы (строка в группе, палитра жива);
недавнее другого сотрудника на том же компьютере (`claimRecent` при входе, `clearRecent` при выходе); пункт недавнего,
к которому пропал доступ (не показывается); storage бросает; карманное устройство — без действий из реестра;
открытие поверх модалки (не открывается, m15); Esc при непустом поле — сначала очистить.

**Файлы.** Создать: `src/ui/palette/{CommandPalette.tsx,Palette.module.css,palette.test.tsx}`, `src/app/recent.ts`
(+ тест), истории. Изменить: `src/ui/shell/AppShell.tsx` (кнопка «Искать везде ⌘K» над меню; на телефоне — иконка
поиска в «Ещё» и первым пунктом шторки), `src/app/{logout.ts,session.tsx}` (`clearRecent`, `claimRecent`),
`messages/{ru,en}.json`.

**Интерфейсы.**
- `CommandPalette({ open, onClose })` — `Modal` (на узком — `Sheet` во всю высоту); поле `role="combobox"`
  (`aria-expanded`, `aria-controls`, `aria-activedescendant`), группы `role="group"` с подписью, ↑/↓, Home/End, Enter;
  число результатов — вежливая живая область после паузы ввода; совпадение — `<mark>`. Открытие в адрес не пишется.
- Сущности: города — `listGeoCities?q=&limit=5` (`geo.cities.read`), сотрудники — `listStaffAccounts?q=&limit=5`
  (`staff.accounts.read`), страны — локально из кэша `listGeoCountries` (`geo.countries.read`); от 2 символов, после
  `SEARCH_DEBOUNCE_MS`; каждая группа — своё «ищем…»/ошибку.
- `src/app/recent.ts`: `pushRecent(staffId, { kind, id, label, path })` — `localStorage['wf.admin.recent.<staffId>']`,
  не больше 8, у сотрудника — только `display_name`; `readRecent(staffId)`; `clearRecent()`; `claimRecent(staffId)` —
  стирает ключи `wf.admin.recent.*` других сотрудников.

**Тесты:** `palette.test.tsx` › Ctrl+K и ⌘K открывают; `k` в поле ввода — нет; разделы — только разрешённые; действие
`dual` — с пометкой «на подтверждение»; 390 + `coarse` — нет «Пригласить»; сущности — запрос только с правом, от 2
символов, после паузы, последний ответ побеждает (контролируемые промисы MSW); клавиатура и `aria-activedescendant`;
Esc с текстом — очистка, второй — закрытие; `recent.test.ts` › 9-й вытесняет первый; `claimRecent(B)` стирает ключи A;
выход — `clearRecent`; в записи сотрудника нет почты.

**Подсадка.** Фильтр по праву снят — тест модератора («Пригласить» видна) краснеет.

### UF4.6: горячие клавиши и шпаргалка

**Цель.** `ShortcutsProvider`, `useShortcut`, `ShortcutsHelp`; клавиши по `code`; отключаемые односимвольные (спека
§4.2, стр. 448–471; WCAG 2.1.4).

**Риск:** B.

**Файлы.** Создать: `src/ui/shortcuts/{ShortcutsProvider.tsx,ShortcutsHelp.tsx,Shortcuts.module.css,
shortcuts.test.tsx}`, истории. Изменить: `src/app/providers.tsx` (провайдер под роутером), `features/*/commands.ts`
(сочетания `g x`), `messages/{ru,en}.json`.

**Интерфейсы.** Один слушатель `keydown` на `document`; `useShortcut(combo: string, handler: () => void, opts?: {
enabled?: boolean; allowInInput?: boolean })`; конфликт одной комбинации — `console.error` в разработке и тест;
таблица §4.2 (`Ctrl/⌘+K`, `/`, `g` + буква ≤ 1 с, `?`); по `KeyboardEvent.code`; не срабатывают в `input`,
`textarea`, `select`, `[contenteditable]`, `[role=combobox]`, `[role=textbox]`, при `isComposing`, с зажатыми
Ctrl/Alt/Meta (кроме своих); при `hasModalOverlay()` — ничего, кроме Esc; подсказка (немодальная) клавиши не глушит.
Шпаргалка — `Modal`, строится из реестра; символ по раскладке (`navigator.keyboard.getLayoutMap()`, нет — латиница);
переключатель «Односимвольные клавиши» (`wf.admin.shortcuts.single`, по умолчанию включены), Ctrl+K — всегда; на
карманном устройстве — без действий из реестра «только с компьютера».

**Тесты:** `shortcuts.test.tsx` › `g c` в русской раскладке (`code: 'KeyG'`, `key: 'п'`) → «Города»; `g` и пауза
1,1 с, затем `c` — ничего; в поле ввода `/` не срабатывает; под модалкой Ctrl+K не открывает палитру; **подсказка
открыта → Ctrl+K открывает палитру** (§4.8 UF4, R2-1); переключатель выключил односимвольные — `g c` молчит, Ctrl+K
работает; конфликт регистраций — ошибка; шпаргалка содержит все зарегистрированные; переход `g x` с грязной формой —
вопрос `useLeaveGuard` (UF2b.7).

**Подсадка.** Проверка «под модалкой» по любой записи стека (не по `hasModalOverlay`) — тест «подсказка → Ctrl+K»
краснеет; по `key` вместо `code` — тест русской раскладки краснеет.

### UF4.7: e2e стартовой страницы и документы UF4

**Цель.** Живой e2e под главную; строки §6.4 для UF4.

**Риск:** C.

**Файлы.** `e2e/{staff.spec.ts,keys.ts}` (сценарий, который ждал журнал после входа, ждёт главную; «Выйти» видна на
1280), `e2e/prod.spec.ts` (обход — с главной и палитрой); спека сотрудников §9.4, абзац «Меню — по макету 07…»
(«Главная» первой; «Стартовая страница после входа — „Главная“ (спека UI-фундамента §4.13); адрес из `?return=`
важнее»); `.claude/rules/admin.md` (главная, палитра и реестр команд, клавиши, недавнее и его стирание, стартовая
страница — строка в «Экраны входа»); `design/README.md` (главная, палитра, шпаргалка — «дорисовано *»).

**Тесты:** `keys.test.ts` › новые ключи в `KEYS`; прогон `./task admin:e2e` — один.

## Порядок исполнения

```
волна 1   UF1a
волна 2   UF1b/I ──> UF1b/II ──> [приёмка палитры снимками — пользователь]
волна 3   UF3.1 (подготовка → стоп → установка Ладл контроллером → проба → [откат контроллером]) ──> UF3.2 … ──┐
              └──> (после исхода пробы) UF2a/I ──> UF2a/II ──> UF2a/III ─────────────────────────────────────┤
волна 4   UF2b/I (pnpm install по сигналу) ──> UF2b/II   ∥   UF3 (остаток) ────────────────────────────────────┤
волна 5   UF4 ──> брифы G9 <──────────────────────────────────────────────────────────────────────────────────┘
бэкенд плана B (ревью G6 и N1, G7, normalize_text) — параллельно всему, файлов фронта не трогает
```

- **Заходы и ревью.** Один заход — один исполнитель `wf-implementer` (opus) с брифом из задач захода; после захода —
  ревью `wf-reviewer` (opus) с пунктом «принципы 1–15» и, для A, 10 категорий; раунды — по Important. Заходы (U1):
  UF1a; UF1b/I (UF1b.1–UF1b.4), UF1b/II (UF1b.5–UF1b.8); UF2a/I (UF2a.1–UF2a.5), UF2a/II (UF2a.6–UF2a.9), UF2a/III
  (UF2a.10–UF2a.13); UF2b/I (UF2b.1–UF2b.5), UF2b/II (UF2b.6–UF2b.10); UF3; UF4 — десять заходов. Внутри захода задачи
  — по порядку номеров, у каждой свой коммит (или несколько) и свой раздел отчёта.
- **Параллельность.** UF3 идёт параллельно с UF2a и UF2b. Пути кода не пересекаются (UF3 не правит `ui/index.ts`,
  `messages/*`, компоненты UF2a/UF2b, `src/showcase/csf.ts`; истории новых компонентов пишут их авторы — U12); общие
  файлы — по таблице владения ниже. Одновременно не больше трёх opus (исполнитель UF2a/UF2b, исполнитель UF3,
  ревьюер) при лимите 4 (память `parallel-agents-limit`).
- **`pnpm`** (U2): UF1a.2 — до всех; Ладл для пробы UF3.1 ставит (и при отказе откатывает) контроллер в тихом окне,
  исполнитель UF3 ждёт на точке остановки (I7); UF2a/I стартует после исхода пробы; UF2b.1 — по сигналу «тихо» (UF3 не
  гоняет процессов из `node_modules`, dev-сервер пользователя остановлен, m24). Тесты всех заходов — бинарниками без
  pnpm.
- **Владение общими файлами** (I8; решение контроллера 03.11). Рабочее дерево одно, а `git commit -- <файл>` забирает и
  чужой незакоммиченный кусок. Поэтому у общего файла в волне — один владелец; другие группы волны файл не правят, а
  пишут нужные строки в отчёт (раздел «Строки для общих файлов»), контроллер вносит их своим коммитом после ревью
  захода. В волнах 1, 2, 5 группа одна — владеет всем.

  | Файл | Волна 3 (UF2a ∥ UF3) | Волна 4 (UF2b ∥ UF3) |
  | --- | --- | --- |
  | `docs/versions.md` | UF3 | UF3 (строка `@tanstack/react-virtual` — из отчёта UF2b.1) |
  | `CLAUDE.md`, `.claude/agents/wf-{implementer,reviewer}.md` | UF3 | UF3 |
  | `.claude/rules/frontend.md`, `.claude/rules/admin.md`, `design/README.md` | UF2a | UF2b |
  | корневой `Taskfile.yml`, `apps/admin/Taskfile.yml`, `.github/workflows/*` | UF3 | UF3 |
  | `apps/admin/package.json`, `pnpm-lock.yaml` | контроллер (установка Ладла) → коммит UF3 (UF3.1, шаг 3) | UF2b (UF2b.1) |
  | `apps/admin/vite.config.ts`, `vite.shots.ts`, `apps/admin/tsconfig*.json` | UF3 | UF3 |
  | `apps/admin/eslint.config.mjs`, `src/ui/{csp,controls,layout-css}.test.ts`, `src/test/setup.ts` | UF2a | UF2b |
  | `src/ui/index.ts`, `messages/{ru,en}.json`, `e2e/*` | UF2a | UF2b |
  | `contracts/tokens/tokens.json`, `packages/tokens/*` | контроллер (короткий раунд палитры UF1b; UF2a токены не правит, R2-m1) | UF2b (UF2b.6, токен `size.overlay-footer`) |

  Владелец меняется только между волнами; файл вне таблицы правит только группа, чьи это пути. Перед правкой общего
  файла владелец проверяет `git diff --quiet -- <файл>` (чужих незакоммиченных правок нет) и коммитит его сразу.
  Контроллер, внося строки не-владельцев, делает ту же проверку перед своим коммитом: владелец может держать
  незакоммиченную правку того же файла — тогда контроллер ждёт его коммита (n3).
- **Приёмка палитры** (UF1b.8) — пользователь смотрит снимки обеих тем; правка значений — короткий раунд UF1b (числа в
  `tokens.json`) параллельно с началом UF2a/I (UF2a не правит токены).
- **Между заходами** — контроллер: `admin typecheck` (tsc напрямую); снимки захода смотрит ревьюер, затем они
  удаляются. `admin:test`, `packages:ci`, `gen:check` — в конце плана B (K5).
- **После UF4** — брифы G9 (Task 20–22) по §6.2 спеки и правленному плану B (UF2a.13), ревью брифов с пунктом
  «принципы 1–15» и ⚠️ этого плана (U18, проверка `virtual/*` под CSP — UF2b.10).
- Пачки по сессиям (память `work-cadence`): например, «UF1a» → «UF1b/I + UF1b/II» → «UF3.1 → UF2a/I ∥ UF3» →
  «UF2a/II ∥ UF3» → «UF2a/III» → «UF2b/I» → «UF2b/II» → «UF4».

## Решения плана

Решения, которых нет в спеке или которые уточняют её стыки. Ответы пользователя (раздел ниже) — выше решений.
Решение, меняющее поведение, переносится в документы группы (`admin.md`, `frontend.md`), спеку не правим (исключение — §4.6 и §15 по решению контроллера, I1).

- **U1. Заходы: UF1b и UF2b — по два, UF2a — три** (решение пользователя 03.11 по Q1; UF2a — три по m15). Состав
  групп — ровно §7 спеки; но UF2a — 13 задач (каркас, стек оверлеев уровня A, два своих контрола, состояния, время,
  словарь по всем экранам, экраны, уборка из 20 пунктов, документы, план B), а UF2b — пять задач уровня A. Деление — по
  швам зависимостей: UF2a/I даёт `Popover`, на котором стоят `Select` и `DateField` в UF2a/II; UF2a/III — применение к
  экранам, уборка и документы; UF2b/I — таблицы, UF2b/II — поведение. Ревью после каждого захода.
- **U2. pnpm и параллельные агенты.** Правило «два pnpm-процесса нельзя» распространяется и на `pnpm exec`: при
  параллельных UF2a/UF2b ∥ UF3 исполнители гоняют тесты бинарниками (`node_modules/.bin/vitest`, `eslint`,
  `playwright`, `ladle`; токены и дизайн — `node`), как группа V1. Установки: UF1a.2 и UF2b.1 — исполнитель по
  сигналу контроллера; **Ладл для пробы UF3.1 — контроллер** в тихом окне (`pnpm --filter @wf/admin add -D
  @ladle/react@5.1.1`), исполнитель UF3 стоит на точке остановки и возобновляется сообщением; при отказе от Ладла откат
  (`remove` + сверка `package.json`/lock с `HEAD`) — тоже контроллер в тихом окне (I7, решение контроллера 03.11).
  «Тихо» — ни один агент не гоняет процессов из `node_modules`, dev-сервер пользователя остановлен (Windows держит
  нативные файлы `node_modules` открытыми, m24).
- **U3. Режим `shots`.** Подмена `src/test/server.ts` и `base.ts` — `resolveId` плагина по абсолютному пути (алиас по
  спецификатору не ловит относительные импорты); точка входа — `transformIndexHtml` того же `index.html` (в нём с UF1b
  `theme-init.js`, снимки видят настоящий первый кадр); свои `cacheDir` и порт 5176; роли — параметр адреса; каталог
  снимков — обязательный `--out`.
- **U4. Страж «нет `msw` в обычной сборке» — без запуска `vite build` в тестах.** Исполнителю `build` запрещён
  (глобальный CLAUDE.md), а сборка внутри Vitest долгая. Первая линия — статический страж графа импортов от
  `src/main.tsx` (статические и динамические импорты) и тест конфига (UF1a.1); вторая — `scripts/check-admin-dist.mjs`
  (в `*.js/*.html/*.css` нет меток `mockServiceWorker`, `msw/browser`, `setupWorker`, в CSS `light-dark(` есть и `--lightningcss-` нет) в `admin:ci` сразу после
  существующего `build` (работает в `./task ci` и job `admin` CI) и в `scripts/e2e-admin.sh` после сборки
  прод-проекта (UF1b.4, UF1b.7; ответ ревью на S5, принят контроллером).
- **U5. Толщина контура иконок — по слоям и «лесенке» размеров.** В артборде 14 толщина зависит от размера (оптическая
  коррекция) и различается у слоёв («контур 2.4, внутренняя линия 1.5»). Храним `width` слоя в единицах мастер-сетки 48
  и `$optical` — множитель на размер, общий для всех слоёв (20 px ×1.25, 24 px ×1.1667; ревью S2); `Icon` пишет
  `stroke-width` атрибутом; иконки `nav` — абсолютная ширина без множителя (m1).
- **U6. Иконки меню — группа `nav` в `icons.extra.json`.** Они из макета 08, не из артборда 14: под страж выгрузки не
  годятся, но и не «дорисованы». `icons.extra.json` — две группы: `nav` (источник — макет 08) и `drawn` (дорисовано *,
  кандидаты в артборд, строки в `design/README.md`). Пересечение ключей запрещено по всем трём источникам.
- **U7. Цвета иконок.** `Icon`: предмет — `currentColor` (вид меню не меняется, активный пункт красит иконку сам),
  деталь — `text-subtle`, акцент — `accent-ink`; `Illustration`: предмет — `text-muted`, акцент — `accent-ink` (как
  §4.4); вырезы — U25.
- **U8. Рамка выбранного сегмента и включённого `Switch` — `accent-ink` в обеих темах.** В тёмной `accent-ink` =
  `accent` — рамка сливается с заливкой и вид не меняется; в светлой даёт 3:1 (§2.3). Нового токена и правил по
  `data-theme` не нужно (системная тема тоже светлая — правило по атрибуту её бы пропустило).
  **Поправка 10.11 (решение пользователя, раунд 1 UF1b/I):** посылка верна только для `Switch` (заливка `accent`). У
  выбранного `Segmented` заливка `pitch` — `accent-ink` давал в тёмной лаймовое кольцо, расходясь с артбордом 07 и P21.
  Рамка выбранного `Segmented` — токен-пара `selected-border` (тёмная `#2C4434` — рамка выбранного артборда 07, светлая
  `#3B6600` = `accent-ink`); строки `contrast` с нормой `{light: 3}` к `surface-sunken` и `pitch` — формат нормы
  `contrast` расширен: число (обе темы) или `{dark?, light?}` (описать в `design/03-токены.md`, UF1b.8). `Switch` — как
  выше. Выбранный чип `DataTable` в светлой (~1.2:1 к фону) — показать пользователю на приёмке UF1b.8.
- **U9. Прод-проект e2e — третья пара `prod`.** admin-api на 18183 с `ADMIN_STATIC_DIR` (без Vite) на той же базе
  `wf_e2e`; свой сотрудник через `bootstrap` (как `stale-cookie.spec.ts`) — от состояния `staff.spec.ts` не зависит;
  сборка — в `scripts/e2e-admin.sh` до Playwright (Playwright стартует `webServer` до `globalSetup`); CI —
  `WF_E2E_PROJECTS: chromium,prod`; локально `prod` — в наборе по умолчанию, `./task admin:e2e` собирает админку
  (решение пользователя 03.11 по Q2, m20).
- **U10. Таббар телефона — первые 4 разрешённых пункта `NAV`.** Прежнее правило «первые 4 открытых на телефоне» без
  фильтра `desktopOnly` (его ни у кого нет); с UF4 «Главная» первая.
- **U11. `ActionGate` (`src/ui`) импортирует реестр из `src/app/desktopOnly.ts`** — как `AppShell` импортирует
  `app/session`; реестр — данные приложения, а не набора компонентов.
- **U12. Истории — CSF на своих типах** (`src/showcase/csf.ts`): не зависят от выбора Ладл/своя витрина, поэтому
  UF2a/UF2b пишут истории своих компонентов, не дожидаясь UF3; страж историй (UF3.4) читает текущий `ui/index.ts`.
  Владелец `csf.ts` — UF1a.3 (истории `Icon`), форма замораживается; UF3 её не меняет, пока UF2a/UF2b пишут истории
  (m16).
- **U13. `useMoney` — экспонента параметром.** Справочника экспонент ISO 4217 в админке нет (появится с первым экраном
  сумм, §4.7); до него вызов обязан передать число знаков явно — без CLDR и без float.
- **U14. Спека сотрудников §9.1 и §9.2** (в §6.4 спеки без группы): §9.1 «Тема» — UF1b.8 (тема сделана там), §9.2
  (ссылка на §4 спеки UI) — UF2a.12; §9.4 «Телефон» — UF2a.12, «Меню/Стартовая» — UF4.7.
- **U15. n4 ревью G8, фронтовая часть — в уборку UF2a.11.** Ключи `patch` вне `from ∪ to` попадают в «и ещё N полей».
  Бэкенд инвариант держит G6, но сводка подтверждающего — граница доверия подтверждения вдвоём (уровень A), защита в
  глубину стоит одной функции и теста.
- **U16. Память и журнал** (`progress.md`, память `project-setup-order`, `frontend-principles`) обновляет контроллер
  после ревью каждого захода; исполнители их не правят.
- **U17. Сравнение скриншотов — без новой зависимости:** `toHaveScreenshot`/`toMatchSnapshot` Playwright с эталоном,
  подложенным из артефакта; `pixelmatch` — только если Playwright не даёт нужного отчёта (тогда строка в
  `docs/versions.md`).
- **U18. Ключи адреса экранов G9.** Панель страны (Task 21) — `/geo/countries?edit=<код>` (спека §4.6, §15; I1):
  `country` — ключ фильтра (`/geo/cities`, импорт), и ключи `PANEL_SEARCH_KEYS` не пересекаются с ключами фильтров ни
  на одном пути (страж UF2b.8). `/geo/imports?country=RU` (ссылка главной) — **фильтр** `country` в `validateSearch`
  экрана импорта, добавляет G9 Task 20 и вносит его в `FILTER_SEARCH_KEYS` (⚠️ в бриф G9; до него заглушка ключ
  игнорирует). Ссылка на города в ожидании — `search: { status: ['waitlist'] }` (массив, роутер хранит JSON), не
  строкой.
- **U19. Что переживает выход и смену сотрудника.** Тема, плотность, столбцы и переключатель односимвольных клавиш —
  предпочтения браузера, не личные данные (§4.12: «один браузер — одна тема»), не стираются. Недавнее палитры —
  стирается: `clearRecent()` при выходе, `claimRecent(staffId)` при входе другого (§4.1; m19).
- **U20. `csp.test.ts` исключает `src/test/**` явно**, с причиной: прокладка Popover API вставляет тестовый `<style>`
  (CSP тестов не касается, §4.8 m-b); `src/shots/**` и истории — под стражем.
- **U21. Тесты скриптов репозитория** (`scripts/*.test.mjs`: снимки, проверка `dist`, поиск эталона) — задача
  `scripts:test` в корневом `Taskfile.yml` **с UF1a.1** (первый такой тест — `admin-shots.test.mjs`, m7), входит в
  `./task ci`; в CI — job, который уже гоняет проверки репозитория (`repo.yml`; подключает UF3.5).
- **U22. Ключи хранилища:** `wf.theme` (спека), `wf.admin.table.<tableId>` (спека), `wf.admin.density`,
  `wf.admin.recent.<staffId>` (спека), `wf.admin.shortcuts.single`; каждый доступ — `try/catch`, значения по умолчанию
  при отказе.
- **U23. Меню «Столбцы» — шаблон WAI-ARIA menu с `menuitemcheckbox`:** стрелки внутри меню, Tab закрывает и уходит
  дальше (так `focusout` закрывает немодальный пузырь при любом владельце Tab — §4.8 m-j, m-o).
- **U24. Один глобальный CSS админки** — `src/ui/global.css` (импорт в `src/app/styles.ts` — общем модуле стилей
  `main.tsx` и режима `shots`, m3): `html { scrollbar-gutter: stable }` (m-k) и `::view-transition-group(*)` при
  reduced motion (§4.15) — правилам корня и псевдоэлементам документа нет места в CSS Modules. Цветов и токенов в нём
  нет; строка в `admin.md`. **Поправка 10.11 (ревью UF2a/I, m9):** исключение — `body { margin: 0; background:
  var(--wf-color-bg) }`: спека §2.1 и §4.12 требуют вычисленный фон именно `body` (прод-e2e сверяет его), иных цветов
  и токенов в `global.css` нет.
- **U25. Роли слоёв иконок и вырезы** (I4). Модель `object | detail | accent | accent-detail | knockout` (+ `opacity`)
  выражает все цвета артборда 14; незнакомый цвет — ошибка выгрузки, а не молча `object` (пакет общий с вебом, страж
  «выгрузка = файл» закрепил бы порчу). Вырез (`knockout`) рисуется цветом поверхности (`bg-elevated` по умолчанию —
  карточки, меню, панели; другая поверхность — класс родителя), вырез поверх акцентного слоя (`on: 'accent'`) —
  `text-inverse`. Иконок артборда — 32.
- **U26. `DataTable` — по окну, как в спеке; контейнер — только то, что спека отдаёт контейнеру** (I6; решение
  контроллера по N1 раунда 2). Переход «строки ↔ карточки» (`useIsMobile`) и скрытие столбцов `priority: 'low'` на
  768–1023 (`useIsCompact`) — по ширине окна (спека §4.5, §3.4): порог по контейнеру прятал бы `low` на ноутбуке 1280
  (область ≈ 996 px из-за сайдбара 236) вопреки спеке. Явно включённый пользователем в «Столбцах» столбец ширина не
  прячет. `@container` по `container.*`/`breakpoint.*` — `Card`-сетка, формы, раскладка внутри панели (спека §3.4,
  стр. 354–356). Таблицы, которым карточки вредны (матрица прав), — `layout: 'table'`.

## Ответы пользователя (03.11.2026) — обязательны для исполнения

- **Q1 — делить на заходы: да.** UF1b и UF2b — по два захода, UF2a — три (по m15 ревью плана: объём UF2a/II был
  тяжелее UF2a/I). Исполнено: U1, «Порядок исполнения».
- **Q2 — прод-проект живого e2e в CI: да.** С учётом m20: проект `prod` — в наборе по умолчанию (локальный `./task ci`
  его гоняет), локальный `./task admin:e2e` теперь собирает админку — записать в документы (UF1b.8: `admin.md`,
  `CLAUDE.md`). Исполнено: U9, UF1b.7, UF1b.8.

Исходные вопросы (закрыты ответами, для истории): Q1 — деление UF1b, UF2a, UF2b на заходы; Q2 — прод-проект e2e в CI
(+1–2 минуты job `e2e`).

## Вопросы к спеке — ответы

Все вопросы раунда 0 закрыты ответами ревью плана, принятыми контроллером 03.11; новых вопросов к спеке нет.

- **S1 (веб и `light-dark()`)** — риска нет: в `apps/web/src` нет ни одного `--wf-color-*`; Next 16 (Turbopack, Lightning
  CSS) полифилит `light-dark()`, полифил работает (`color-scheme` в том же `tokens.css`), веб остаётся тёмным.
  `web:e2e` и сборку веба ради этого не гонять; хватит `./task web:test` в K5. Запись — `design/03-токены.md` (UF1b.8).
- **S2 (толщина контура)** — U5 верно; `$optical` — множитель, общий для слоёв; роли слоёв — U25.
- **S3 (иллюстрации)** — совпадение верно: «время» — секундомер (коронка, боковая кнопка, риски), «угловой» — флажок на
  древке; дорисовывать не нужно.
- **S4 (плитка импорта)** — формулировка «среди последних 5 записей журнала импорта»; точный «последний по стране» —
  после разморозки контракта (UF4.2).
- **S5 (страж «нет `msw` в сборке»)** — U4 + проверка `dist` в `admin:ci` после существующего `build` и «нет
  `--lightningcss-`» (UF1b.4); тест над сборкой в Vitest не нужен.
- **S6 (справочно)** — подтверждено: 24 вызова `selectOptions` в 9 файлах; прочие правки тестов — перечень UF2a.6 (I2).
- **I1 (ключ `country`)** — спека правлена контроллером-разрешённым местом: §4.6, §6.2 (строка Task 21) и §15 «Поправки по ревью плана (ред. 7)» —
  панель страны `?edit=<код>`, страж «ключи панелей ∩ ключи фильтров = ∅».

## Сверка хвостов (откуда → куда)

| Хвост | Источник | Задача |
| --- | --- | --- |
| п. 8 V1: свой `SelectField` (на телефоне шторкой), e2e под него, неделя по языку | `progress.md` (решения 28.10), спека §1.3, §4.16–§4.17 | UF2a.6, UF2a.7 |
| Эталоны скриншотов — артефакт CI `main`, не в репо; снимки ревьюера временные | `progress.md` (28.10), спека §5.3–§5.4 | UF1a.1, UF3.5–UF3.6 |
| Главная — стартовая, `?return=` важнее | `progress.md` (28.10), спека §4.13 | UF4.1 |
| Ladle — проба в UF3, запасной путь — своя витрина | `progress.md` (28.10), спека §5.1 | UF3.1 |
| Палитра принята предварительно; окончательно — снимки настоящих экранов | `progress.md` (28.10) | UF1b.8 |
| V1 m1 (выбор места после смены страны), m2 (фокус при смене шага) | `review-v1.md` | UF2a.11 |
| V1 m3 («Sign out» в колонке) | `review-v1.md` | UF1b.6 |
| V1 m4 (forced-colors) | `review-v1.md` | UF1b.3 |
| V1 m5 (мёртвые ключи + страж), m6 (дыры стражей), m7 (`asyncUtilTimeout`) | `review-v1.md` | UF2a.11 |
| G8 m1, m3, m5, m6, m9, m10, m11, m12, m13, m14 | `review-g8.md` | UF2a.11 |
| G8 m2, m4, m7 | `review-g8.md` | закрыты раундом 1 G8 (5614b0f) — не включены |
| G8 n1, n2, n3 (фокус) | `review-g8.md`, повторное ревью | UF2a.11 |
| G8 n4 — фронт (ключи `patch` вне `from/to`) | `review-g8.md`, повторное ревью | UF2a.11 (U15) |
| G8, непредусмотренный отказ №1 (смена языка стирает ввод на странице города) | `review-g8.md` | UF2a.11 (m18) |
| Правка спеки сотрудников §9.4 «Телефон» | спека §3.3 | UF2a.12 |
| Правка спеки сотрудников §9.4 «Меню/Стартовая» | спека §3.3 | UF4.7 |
| Правка спеки сотрудников §9.1, §9.2 | спека §6.4 | UF1b.8, UF2a.12 (U14) |
| План B, этап 4 и «Порядок исполнения» | спека §6.3 | UF2a.13 |
| ⚠️ G9 Task 20: фильтр `country` в поиске `/geo/imports` + строка в `FILTER_SEARCH_KEYS` | U18 | бриф G9 |
| ⚠️ G9 Task 21: панель страны — `?edit=<код>`, не `?country=` | спека §4.6, §15; U18 | бриф G9 |
| ⚠️ G9: проверка `virtual/*` под настоящей CSP, если стенд e2e не дал | UF2b.10 | бриф G9 |
| `pendingSection` и маршруты-заглушки (G8) | `review-g8.md` ⚠️ G9 | не здесь — G9 Task 20–22 |

## Поправки раунда 1

Ревью плана: `.superpowers/sdd/2026-10-28-staff-b-geo2/review-uf-plan.md` (Needs fixes: Important I1–I8, Minor
m1–m24). Решения пользователя 03.11 (Q1, Q2) и контроллера (I1, I7, I8, S1–S5) — в тексте выше.

| № | Что изменено | Где |
| --- | --- | --- |
| I1 | ключ панели страны — `?edit=<код>` (не `country`, это фильтр); `FILTER_SEARCH_KEYS` и страж «ключи панелей ∩ ключи фильтров всех путей и query-параметры контракта = ∅» с подсадкой; тест V1 `CitiesPage.test` (`?country=RU` через `BackLink`) — без правки; спека §4.6 и §15 (одна строка) | UF2b.8, UF2b.4, UF2a.13, U18, Review Focus 10, «Сверка хвостов», спека §4.6/§15 |
| I2 | имя combobox — только подпись (APG); `selectedText`, `openOptions` рядом с `pick`; перечень разрешённых переписываний тестов экранов (`toHaveValue`, опции внутри поля) | UF2a.6, Review Focus 6, Global Constraints |
| I3 | сценарий (1) прод-e2e — явный выбор против эмулированной системной темы; подсадка «без `theme-init.js`» теперь краснеет | UF1b.7, Review Focus 1 |
| I4 | роли слоёв `object / detail / accent / accent-detail / knockout` + `opacity`, исчерпывающая таблица цветов, незнакомый цвет — ошибка; вырез на акценте — `text-inverse`; 32 иконки | UF1a.2, UF1a.3, U7, U25 |
| I5 | `useUndoable<V, R>`: обратное получает результат прямого (`version`), прежнее значение — в `v`; тест версии и подсадка | UF2b.6, Review Focus 8 |
| I6 | матрица прав на телефоне — `DataTable layout: 'table'` + `stickyFirstColumn`; container queries внутри страниц со стражем `@container`; скрытие `priority: 'low'` — по контейнеру | UF2a.1, UF2a.5, UF2b.2, UF2b.3, UF2b.10, U26, Review Focus 14 |
| I7 | проба Ладла — точка остановки исполнителя; установку и откат делает контроллер в тихом окне; UF2a/I — после исхода пробы | UF3.1, U2, Global Constraints, «Порядок исполнения» |
| I8 | таблица владения общими файлами по волнам; не-владелец пишет строки в отчёт, вносит контроллер | «Порядок исполнения», Global Constraints, UF2b.1, UF2b.10, UF3, UF3.6, UF3.7, UF2a.12 |
| m1 | иконки `nav` — абсолютная ширина контура, без `OPTICAL` | UF1a.3, U5 |
| m2 | общий обработчик `GET /v1/approvals` — `src/test/defaults.ts` для обоих серверов; мок `POST /v1/staff/me/mfa/setup` для роли `setup` | UF1a.1 |
| m3 | общий модуль стилей `src/app/styles.ts` для `main.tsx` и режима `shots` | UF1a.1, UF2a.2, U24 |
| m4 | сборка режима `shots` — правка `vite.shots.ts` в UF3.6, `outDir` `dist-shots` | UF3.6 |
| m5 | `scripts/check-admin-dist.mjs` в `admin:ci` после `build`: нет `msw`, есть `light-dark(`, нет `--lightningcss-`; фраза «настоящая проверка — (3)» исправлена | UF1b.4, UF1b.7, U4, Review Focus 1 |
| m6 | страж графа импортов идёт и по динамическим `import()` | UF1a.1 |
| m7 | `scripts:test` заводится в UF1a.1 | UF1a.1, UF3.5, U21 |
| m8 | `dayStartUtc` (`new Intl.*` в `features/audit/search.ts`) переезжает в `src/i18n` | UF2a.9 |
| m9 | `setDensity` в `CommandContext` | UF4.3 |
| m10 | страж стенда browser history — восстановление `pushState` и баланс слушателей; тесты «Назад» не уходят за свои записи | UF2a.3 |
| m11 | «быстрый ответ» — проверка на удержанном ответе через 100 мс | UF2a.8 |
| m12 | пауза по 429 — общая для пачки; без `Retry-After` — `BULK_RETRY_DEFAULT_MS` | UF2b.5 |
| m13 | `close` панели идемпотентен в пределах открытия; тест двойного Esc | UF2b.8 |
| m14 | `Popover` рендерится на месте, без портала | UF2a.2 |
| m15 | UF2a — три захода (UF2a/III — UF2a.10–UF2a.13) | U1, UF2a, «Порядок исполнения» |
| m16 | владелец `src/showcase/csf.ts` — UF1a.3, форма заморожена | UF1a.3, UF3.2, U12 |
| m17 | истории `AppShell`/оверлеев/`DataTable` — перечитать пропсы перед коммитом | UF3.3 |
| m18 | `useGeoCity` с `keepPreviousData` (непредусмотренный отказ №1 ревью G8) | UF2a.11, «Сверка хвостов» |
| m19 | U19 переписан без противоречия | U19 |
| m20 | `prod` — в наборе проектов по умолчанию, порт 18183 — в проверке портов; «`admin:e2e` собирает админку» — в документы | UF1b.7, UF1b.8, U9 |
| m21 | обе `meta[theme-color]` при явном выборе, возврат исходных при `system` | UF1b.5 |
| m22 | `useReducedMotion` (`src/ui/motion.ts`) — UF2b.9 | UF2a.8, UF2b.9 |
| m23 | тосты не над подвалом открытой панели | UF2b.6 |
| m24 | сигнал «тихо» учитывает dev-сервер пользователя | U2, Global Constraints, UF2b |
| S1–S6 | ответы ревью приняты; раздел «Вопросы к спеке» → «Вопросы к спеке — ответы» | UF1b.2, UF1b.8, UF4.2, разделы ответов |
| Q1, Q2 | ответы пользователя 03.11 | «Ответы пользователя (03.11.2026)» |

### Раунд 2

Повторное ревью: раздел «# Повторное ревью раунда 1» в `review-uf-plan.md` (I2 — частично, новая N1, остаток m20,
Minor n1–n7, два «не проверено»). Решения контроллера 03.11 — по N1 и n4, n6.

| № | Что изменено | Где |
| --- | --- | --- |
| I2 | правило «любая проверка нативного списка → помощники, по смыслу та же»; в перечень добавлены 7 `toHaveValue` (`InviteDrawer` ×2, `StaffDrawer` ×2, `StaffPage`, `AuditPage`) и помощник `roleOptions`; у опции `data-value` и `aria-disabled`; `openOptions` → `{ el, value, label, disabled }`; «иных правок нет» заменено на «сверх списка — по правилу, иначе NEEDS_CONTEXT» | UF2a.6 |
| N1 | столбцы `priority: 'low'` скрываются **по окну** (`useIsCompact`, 768–1023), как в спеке §4.5; явно включённый в «Столбцах» столбец ширина не прячет, в меню — «скрыт по ширине»; `prefs` хранит `shown`; `@container` — только `Card`-сетка, формы, тело панели | UF2b.3, UF2a.5, U26, Review Focus 14 |
| m20 | умолчание проектов и в `scripts/e2e-admin.sh` — `chromium,webkit,prod`; подсказка «без WebKit» — `WF_E2E_PROJECTS=chromium,prod` в `admin.md`/`CLAUDE.md` | UF1b.7, UF1b.8 |
| n1 | выгрузка иконок берёт только `<svg width="46">` с подписью из таблицы; образцы 40 px и абзац под ними — не иконки | UF1a.2 |
| n2 | `check-admin-dist.mjs` ищет только в `*.js/*.html/*.css` по точным меткам (не `msw` в `woff2`); путь по умолчанию — от скрипта | UF1b.4, U4 |
| n3 | контроллер перед своим коммитом общего файла — та же проверка `git diff --quiet`, ждёт коммита владельца | «Порядок исполнения» |
| n4 | спека: §6.2 строка Task 21 → `?edit=<код>`; §15 переименован «Поправки по ревью плана (ред. 7)»; в шапке спеки — строка о редакции 7 | спека §6.2, §15, шапка; план «Spec», ответы I1 |
| n5 | `placeholderData` — функция, прежние данные только при том же `cityId`; пока `isPlaceholderData` — формы только для чтения; тест «город A → город B» | UF2a.11 |
| n6 | высота подвала панели — новый токен `size.overlay-footer` (подвал в одну строку), отступ тостов — тот же токен; без замера и без `setCssPx` в `overlay/*`; `tokens.json` в волне 4 — у UF2b | UF2b.6, «Порядок исполнения» (владение) |
| n7 | ключи фильтров объявлены рядом с парсером (`KEYS` в `features/*/search.ts`), парсер работает только по ним, тип результата не допускает лишних ключей | UF2b.8 |
| R2-m1 | `tokens.json` в волне 3 — у контроллера (короткий раунд палитры UF1b), не у UF2a | «Порядок исполнения» (владение) |
| R2-m2 | `size.overlay-footer` — строка в `design/03-токены.md` и прогон тестов `packages/tokens` | UF2b.6 |
| R2-m3 | подвал — `min-block-size` по токену, перенос вместо выхода за край; снимок английского подвала на 768 | UF2b.6 |
| не проверено (1) | UF2a.3 первым шагом сверяет по исходникам `@tanstack/history`, восстанавливает ли `destroy()` `pushState`; нет — стенд сам сохраняет и возвращает исходные | UF2a.3 |
| не проверено (2) | `allowBuilds` для Ladle (esbuild Vite 6) проверяет контроллер при установке, решение — его коммитом вместе с установкой или откатом | UF3.1 |

