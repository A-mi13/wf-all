---
paths:
  - "apps/admin/**"
---

# Админка (`apps/admin`) — ядро

Спека — `docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §9, UI —
`docs/superpowers/specs/2026-10-28-ui-foundation-design.md`. Общие правила фронтов — `frontend.md`. Области (грузятся по
путям): компоненты, тема, каркас, витрина и снимки — `admin-ui.md`; живой e2e — `admin-e2e.md`; «Справочники» —
`admin-geo.md`; сессия, вход, «Мой доступ», «Роли и доступ», «Подтверждения», «Журнал», язык до входа —
`admin-staff.md`.

## Устройство
- **Структура:** `src/app/` — роутер, провайдеры, сессия, права, guard'ы; `src/ui/` — набор компонентов;
  `src/features/<раздел>/` — экраны; `src/api/` — клиент и мутации; `src/i18n/` — язык. `src/app/stubs.tsx` — только «не
  найдено» и загрузка сессии.
- **Компоненты** — только из `src/ui` (импорт `../../ui`); своих кнопок, полей, модалок, таблиц и состояний в `features`
  не заводить. Принципы:
  1. у каждого вложенного экрана — возврат к родителю общим `BackLink` (его рисует `AppShell` для любого адреса внутри
     раздела меню, на последний адрес родителя с его фильтрами), своих кнопок «назад» нет;
  2. контролы форм и кнопки — только компоненты `src/ui`, нативные `<button>`/`<input>`/`<select>`/`<textarea>` в
     `features` и `app` запрещены (страж `src/ui/controls.test.ts`);
  3. действие панели/диалога — в закреплённом подвале (`footer` у `Modal`/`Drawer`/`Sheet`; кнопка отправки формы из
     подвала — `type="submit" form={id}`), прокручивается только содержимое;
  4. вёрстка без «прыжков» и случайных полос: горизонтальная прокрутка — с явным `overflow-y`, выбранное состояние не
     меняет метрик шрифта (страж `src/ui/layout-css.test.ts`).
- **Новый раздел:** путь — константа в `src/app/paths.ts` (других путей нет); маршрут — ребёнок `shellRoute` в
  `src/app/routes.tsx` (роутер — `router.tsx`), экран — ленивым куском (`lazyRouteComponent(() => import(…), 'Имя')` + `...section`: скелетон
  `SectionSkeleton`, ошибка куска — `ChunkErrorState` «Обновить страницу»; экраны входа и каркас — в основном куске,
  страж — граф импортов от `main.tsx` в `router.test.tsx`; `defaultPreload: 'intent'`); фильтры в `validateSearch` —
  строкой в `src/app/filterKeys.ts` (страж обходит дерево маршрутов); пункт в `NAV` (`src/app/nav.ts`) —
  `{ path, labelKey, permission, icon }`. `permission` — ключ каталога (`null` — своё, любому активному). Пункта без права в меню нет, прямой адрес (и вложенные `/раздел/…`) —
  `ForbiddenState` сам AppShell; экран раздела проверку права не дублирует. Все разделы открыты на любой ширине и
  устройстве (спека UI §3.1): флаг `desktopOnly` остался в типе заделом, каркас его не читает, в `NAV` его нет (страж
  `nav.test.ts`). Страница начинается с `PageHeader` (единственный `h1`), вкладки — `Tabs` (управляемые, значение —
  search-параметр; счётчик — `count`, имя вкладки «Входящие 3»).
- **«Только с компьютера» — действие и устройство** (§3.2): `ActionGate({ permission, compact? })` (`src/ui/gate`) на
  карманном устройстве (`useIsHandheld()` — палец **и** экран уже `breakpoint-compact`; компьютер при масштабе 200 % —
  нет) и праве из реестра `DESKTOP_ONLY_PERMISSIONS` (`src/app/desktopOnly.ts`: выдача, снятие ролей, сброс доступа — 7
  прав) рисует `DesktopOnlyNotice` (текст, не кнопка, не в Tab-порядке; `compact` — строка со значком); право — самого
  действия («Завершить все сеансы» — `staff.sessions.revoke`, не в реестре); решения подтверждений —
  `isDesktopOnlyDecision(action)` (любой `staff.*`). Ширина — только раскладка: `useIsMobile()` (< `breakpoint-mobile`),
  `useIsCompact()` (< `breakpoint-compact`) из `src/ui/media.ts`.

## Экран: данные, списки, формы
- **Данные экрана** — только `DataState({ query, skeleton, isEmpty?, empty?, scope?, pending?, children })`
  (`src/ui/states/DataState.tsx`, ветки — в комментарии): скелетон через `SKELETON_DELAY_MS`; 401 (me перечитывается) —
  скелетон, без кадра ошибки; фоновая ошибка данных не прячет; `scope="block"` — карточка или панель (403 без `h1`);
  `pending` — данные устарели, показывать нельзя (секрет 2FA) — скелетон. Скелетоны —
  `Skeleton.{Text,Block,Card,Table,Form}`; `LoadingRows` и «Загрузка…» в экранах — нет (страж `loading.test.ts`;
  `LoadingRows` жив внутри `DataTable`). Состояния — `EmptyState`, `ErrorState`, `ForbiddenState`, `RateLimitedState` и
  др. (`admin-ui.md`).
- **Списки** — `DataTable` (`columns`, `rowKey`, данные — `{...infiniteTableProps(useInfiniteQuery(…))}` со страницами
  `{items, next_cursor}` и `getNextPageParam: (p) => p.next_cursor ?? undefined`); сам показывает скелетон, пусто,
  ошибку с кодом и «Повторить». Фильтры и сортировку держит экран (search-параметры маршрута). Пропсы — поиск, чипы,
  фильтры, сортировка, карточки, список выбора — `admin-ui.md`.
- **Формы** — `Form form={useForm()} onSubmit={(v) => m.mutateAsync(v)}` и поля `TextField` (`type` без `date`),
  `PasswordField`, `SelectField`, `MultiSelectField`, `DateField`, `CheckboxField` (устройство — `admin-ui.md`); своё
  поле — через `Controller` + `FieldError`. Ошибка `onSubmit`: `validation.failed` → `body.<поле>` раскладывается по
  полям (текст `ui.field_errors.<код>`), остальное — `FormError` (`errors.<код>`, откат на `errors.internal`). Особые
  случаи (`attempts_left`, 429) — `onError` экрана, вернуть `true`. Текст ошибки по коду вне формы — только
  `useErrorText()` или `<FormError error={…} />`, своих откатов не писать. `Form` гасит повторную отправку, пока идёт
  первая; ошибка не от API — `errors.internal` на экране и `console.error`.
- **Оверлеи** — `Modal`, `Drawer` (панель справа), `Sheet` (шторка снизу), `ConfirmDialog`: `open` + `onClose` у экрана,
  `dismissible={false}` / `loading` — пока идёт запрос; тосты — `useToast().success|info|error(текст, { action })`.
  Поведение — `admin-ui.md`.
- **«Отменить» в тосте** (§4.10) — `useUndoable(mutation, { run(v, result, key), reread(v, result) })`
  (`src/api/undo.ts`): обратное — новым ключом, с ответом прямого (`version`), прежнее значение экран кладёт в `v`;
  `mutate` — ошибку прямого показывает экран (`onError` мутации), `mutateAsync` — промис прямого для формы и строки
  (ошибка — вызывающему, `Form` раскладывает её по полям; тост «Отменить» — при успехе так же).
  Обратимое — только если все условия: обратное — одна ручка того же права; `200` у прямого и обратного (не `202`, не
  вдвоём); нет неотзываемых внешних последствий (письмо, пуш, удаление, требование к клиентам); при `409 *_stale` —
  тост «уже изменили», без отмены. Подходят: «В архив»/«Вернуть» район, переименование района, флаг функции. Не
  подходят: включение страны, версии приложения, приглашение, роль, сброс, выключение, решения подтверждений,
  активация города, удаление перевода. Оптимистичных серверных мутаций нет (все пишут в журнал) — список пуст.
- **Защита от потери** (§4.11) — форма с вводом: `useLeaveGuard(isDirty)` (`src/app/leaveGuard.ts`; вопрос —
  `LeaveGuardProvider` в `AppProviders`, «Выйти» спрашивает в `useLogout` до POST); панель с вводом — `dirty` у
  `Modal`/`Drawer`/`Sheet`. Успех формы: сбросить `isDirty`, переход — после применения (эффектом) или с
  `ignoreBlocker: true`. Тесты ухода — на browser history.
- **Панели в адресе** (§4.6) — `usePanelParam(key, { allowed })` (`src/app/panels.ts`): `open` — push с
  `state.panel`, `close` — `history.back` (метка) или `replace` без ключа (глубокая ссылка), оба с `ignoreBlocker`;
  повтор `close` до смены записи — ничего; `switch(v)` — другое значение открытой панели (`replace` с прежним
  `state`: одна запись истории на панель, закрытие — по-прежнему `back`; панели нет — как `open`); ключ без права
  снимается `replace`; прокрутка не сбрасывается — ключ позиции роутера (`getScrollRestorationKey`) —
  `scrollRestorationId(restoreKeyOf(location))`, у открытия, смены и закрытия панели он один. Ключ — в
  `PANEL_SEARCH_KEYS` (`src/app/paths.ts`, не совпадает с фильтрами и параметрами контракта — страж
  `searchKeys.test.ts`) и `panelSearch([...])` в `validateSearch` маршрута; уход из панели на другой путь —
  `useLeavePanelTo()` (replace). Фильтры экрана — `KEYS` + `parseKeys` (`src/app/search.ts`) в `features/*/search.ts`.
  Переход к странице ленивого раздела с `viewTransition` — только если кусок уже готов (греть по намерению
  `router.preloadRoute`; иначе роутер оставит флаг перехода следующей навигации), и не после ухода сотрудника.
- **CSP** admin-api (`style-src 'self'`): во всём `src` (кроме тестов и стенда `src/test/**` — прокладка Popover API
  вставляет тестовый `<style>`) нет атрибута `style`, `<style>`, `dangerouslySetInnerHTML` и записи в `element.style` —
  только CSS Modules; цвета — только `var(--wf-color-*)`. Исключение — `setCssPx(el, '--wf-…', число)` из
  `src/ui/cssVar.ts` (единственный `setProperty`; вызыватели — только `src/ui/popover/*` и `src/ui/virtual/*`).
  Страж — `src/ui/csp.test.ts`.
- **Раскладка** внутри страниц — `@container` (область страницы и тело панели — `container-type: inline-size`), пороги —
  числа из `tokens.container`/`breakpoint` (страж `layout-css.test.ts`); каркас — по окну. Глобальный CSS — только
  `src/ui/global.css` (`scrollbar-gutter: stable`, фон и `margin: 0` у `body`), импорт — `src/app/styles.ts`.

## Данные и сессия
- **Права** — только из `me.permissions` (`useCan`, `useDecide`, для многих прав разом — `usePermissionCheck` из
  `src/app/session.tsx`), не по роли: `allow` — сразу, `scoped` — в `me.city_ids`, `dual` — «можно запросить»
  (`useDecide` → `needs_approval`). Решает всегда сервер.
- **Запросы** — `useApi()` (openapi-fetch, тот же origin, `credentials: 'same-origin'`); запросы —
  `networkMode: 'online'` (`query.ts`). Клиент сам ставит `X-WF-Admin` на мутациях и бросает
  `ApiError {status, code, fields, retryAfter, attemptsLeft, replaySkipped}` на любой не-2xx (`retryAfter` — только из
  заголовка `Retry-After`: поля `retry_after` в теле нет). `401 auth.reauth_required` — ждёт разблокировку (`reauthBus`)
  и повторяет запрос; `401 auth.unauthenticated` и `403 auth.forbidden` любого запроса или мутации перечитывают me
  (`query.ts`, `cancelRefetch: false`: серия отказов разом — одно перечитывание, идущее не отменяется; права сняли, пока
  вкладка открыта, — меню и разделы следуют новым). Мутации с кодом второго фактора в теле — `...noReplay()` (после
  разблокировки не повторяются; `admin-staff.md` «Разблокировка»). Данные ответа — `unwrap(api.GET(…))`
  (`src/api/client.ts`), без `!` и `as`; ответу 204 `unwrap` не нужен.
- **Мутации** — только `useAdminMutation(fn(vars, idempotencyKey))`: ключ идемпотентности — один на действие, `retry()`
  повторяет с тем же ключом; сам клиент ключ не создаёт. Исход прошлого действия неизвестен (`isUnknownOutcome`: 5xx,
  обрыв, 408/499 прокси, 2xx без тела, `409 idempotency.replay_unavailable` и `idempotency.in_progress` — первый запрос
  ещё исполняется), а `mutate`/`mutateAsync` пришли с теми же значениями (повторная отправка формы; сравнение по
  содержимому) — ключ прежний: сервер вернёт сохранённый ответ, а не исполнит второй раз; другие значения, успех или
  известный отказ (4xx) — новый ключ. `networkMode: 'always'` — офлайн не копится.
- **Фоновые опросы** (счётчики, статус импорта) — только с `...background()` (возвращает лишь `{ headers }`): заголовок
  `X-WF-Background: 1` не продлевает сессию. `401 auth.reauth_required` фонового запроса, как и любого, открывает
  разблокировку (§9.3: через 20 минут бездействия вкладка показывает модалку, а не замёрзший счётчик); открыта уже —
  запрос ждёт её же, второй раз не открывает.
- **Черновики форм** — `useDraft(<путь маршрута>, me.id)` (sessionStorage) внутри каркаса — черновик сотрудника: другой
  за тем же компьютером (сессия истекла, вошёл другой) его не видит, а `SessionProvider` при входе стирает чужие
  (`claimDrafts`); без владельца — только экраны входа (почта до входа). Пароли и коды в черновик не класть.
- **Общее устройство** — без сессии или другой сотрудник `SessionProvider` стирает кэш запросов, кроме me, и кэш
  мутаций: данные прошлого сотрудника не видны следующему; каркас — `key={me.id}`. Одноразовые запросы —
  `meta: ONE_SHOT` (`app/query.ts`): смена сотрудника их удаляет, а не перезапускает. Секреты (пароли, коды, секрет 2FA)
  — только в памяти экрана, мутации с ними — `gcTime: 0`. Механика — `admin-staff.md` «Сессия»; там же стадия `error` в
  `AppRouter` (429 — `RateLimitedState page`, 4xx — `ConnectionErrorState`).

## Тексты, даты, токены
- **Тексты** — только `messages/{ru,en}.json`, en — перевод, не пустая строка. Подписи прав — `permissions.<ключ>`,
  действий подтверждений — `approvals.actions.<действие>`, ошибок — `errors.<код>`, общие действия — `actions.*` (страж
  словаря `vocabulary.test.ts`); стражи — `src/i18n/labels.test.ts` (и неиспользуемые ключи: разбор
  `src/test/i18nKeys.ts`, динамические префиксы — `DYNAMIC` с причиной), `keys.test.ts` (видит `t.rich`, `t.markup`,
  `t.has`), `error-codes.test.ts`. Подписи, по которым ищет живой e2e, — `KEYS` в `e2e/keys.ts` (страж
  `e2e/keys.test.ts`).
- **Даты и числа** — только `useDateTime()`, `useNumber()`, `useMoney()(minor, currency, exponent)`
  (`src/i18n/format.ts`; деньги — десятичной строкой, без float) и `RelativeTime` (`src/ui/time`; время событий в
  строках — журнал, подтверждения, сеансы; внутри фразы — `t.rich` с тегом `<time>`); «сейчас» — `useNow(granularity)`,
  в витрине и shots — `NowProvider`; полночь дня в поясе — `dayStartUtc` (`src/i18n/zone.ts`). `Intl` и `toLocale*` в
  `src/features/**` запрещены lint. Пояс браузера — `timeZone` задаёт `LocaleProvider` (без него use-intl пишет
  ENVIRONMENT_FALLBACK в консоль). Язык интерфейса — `useLocale()` (`admin-staff.md` «Язык до входа»).
- **Токены** — только `--wf-*` (две темы — `design/03-токены.md`): `accent` — только заливка; текст, линия, курсор —
  `accent-ink`; кольцо фокуса — `focus`; рамки полей — `border-control`; рамка выбранного сегмента и выбранного чипа
  `DataTable` — `selected-border`; подписи и плейсхолдеры — `text-subtle`, на чипах (`chip-bg`) — `text-muted`;
  `text-faint` — только значки; `text-ghost` — только disabled; `breakpoint-mobile` — ширина переключения раскладки (в
  `@media` — через `tokens.ts`). Страж — `src/ui/contrast-css.test.ts`: новая пара «цвет × фон» в CSS Modules → строка
  `contrast` в `tokens.json`.
- **Минимальные браузеры**: Chrome 123, Firefox 125 (Popover API), Safari/iOS 17.5 — `build.cssTarget`
  (`vite.config.ts`; ниже Lightning CSS заменил бы `light-dark()` полифилом `--lightningcss-*`). Шрифты в CSS не
  встраиваются (`build.assetsInlineLimit`: `data:font/…` отверг бы `font-src 'self'`). Прод-CSS = dev-CSS сверяет
  `scripts/check-admin-dist.mjs` в `admin:ci` после `build` и в `scripts/e2e-admin.sh`: `light-dark(` есть,
  `--lightningcss-` и `data:font/` нет, меток MSW нет.

## Тесты
- Стенд — `renderWithProviders(ui, { me })` / `providers({ me })` из `src/test/render.tsx` (me в кэше; `me: null` — тест
  сам мокает `GET /v1/staff/me`); экран, который ведёт стадия сессии, — `renderApp(path, { me, strict, history })` из
  `src/test/app.tsx` (всё приложение на memory history; `me.current` меняет ручка мока, `storageDump()` — проверка, что
  секретов нет в storage); ответы `me` — `src/test/me.ts`, ошибки — `problem()` из `src/test/problem.ts`. Компонент
  `src/ui` без API и сессии — `renderWithIntl(ui, { locale, timeZone })` (язык, пояс — по умолчанию браузера, как у
  `LocaleProvider`, и тосты; возвращает `user`).
- **Memory history не блокирует `back`** — тесты блокировщика и закрытия панели — на
  `renderApp(path, { history: 'browser' })`: адрес ставится до `createBrowserHistory`, после теста — `destroy()`
  (возвращает `pushState`/`replaceState`, снимает слушатели; страж `src/test/app.test.tsx`), `back()` ждёт `popstate`,
  записи истории окна живут весь файл — тест не уходит «Назад» дальше своих push.
- Неописанный запрос MSW роняет тест (`request:unhandled` копится, `afterEach` падает со списком — `src/test/setup.ts`,
  так же в `apps/web`): тест маршрутов и входа, который приводит в раздел каркаса, ставит `sectionsServer()`
  (`src/test/sections.ts`: журнал, «Роли и доступ», «Мой доступ», «Справочники» — `geoServer()`, `admin-geo.md`);
  `staffServer()` отдаёт и сеансы цели (пусто); общий мок `GET /v1/approvals` (пусто) — `defaultHandlers`
  (`src/test/defaults.ts`; счётчик опрашивает на любом разделе; их ставят и тестовый сервер, и режим shots). Каркас с
  модератором запрашивает `GET /v1/geo/cities` (названия его городов) — мок в тесте (`src/app/router.test.tsx`).
- Время и пояс: даты в двух поясах на экране — `process.env.TZ` до `renderApp` (пояс `LocaleProvider` берёт при
  монтировании), вернуть в `afterEach`; с `vi.useFakeTimers()` события — `fireEvent`: user-event зависает (RTL ждёт
  настоящий `setTimeout`); экран с MSW и отсчётом — `vi.useFakeTimers({ shouldAdvanceTime: true })` (иначе ответ мока и
  `findBy*` не дождутся), время — `act(() => vi.advanceTimersByTime(…))`. В тестах язык браузера — `ru-RU`
  (`src/test/setup.ts`, геттер: тест языка подменяет его `vi.spyOn(navigator, 'language', 'get')`).
- Устройство — `stubMedia({ width, pointer })` из `src/test/media.ts` (разбирает `max-width`/`min-width` и `pointer`,
  `set()` меняет на лету, после теста возвращает прежний `matchMedia`; телефон — `390` + `coarse`, масштаб на ноутбуке —
  `640` + `fine`; без matchMedia — широкий компьютер). Выпадающий список — `pick`/`pickMany`/`selectedText`/
  `openOptions` из `src/test/pick.ts`, не `selectOptions`/`toHaveValue`; `asyncUtilTimeout` RTL — 3 с (`setup.ts`).
  Popover API в jsdom — прокладка `src/test/setup.ts` (`data-popover-open`, событие `toggle`, `:popover-open` в
  `matches`, тестовый `<style>` поверх таблицы jsdom): проверять видимость и роли, а не факт вызова. Живые области
  тостов без ролей: `getByRole('alert')` в тестах экранов находит только их ошибки.
- Vitest берёт только `*.test.*` (`test.include`); живой e2e — `admin-e2e.md`; режим `shots` и витрина (каждый компонент
  `ui/index.ts` — с `*.stories.tsx`) — `admin-ui.md`.
