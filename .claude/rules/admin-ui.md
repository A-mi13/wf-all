---
paths:
  - "apps/admin/src/ui/**"
  - "apps/admin/src/showcase/**"
  - "apps/admin/src/shots/**"
  - "apps/admin/.ladle/**"
  - "apps/admin/visual/**"
  - "apps/admin/public/**"
  - "apps/admin/index.html"
  - "apps/admin/vite.shots.ts"
---

# Админка: компоненты, тема, витрина, снимки

Ядро — `admin.md`; спека — UI-фундамент `docs/superpowers/specs/2026-10-28-ui-foundation-design.md`. На устройство
элементов опирается живой e2e (роли и подписи — `admin-e2e.md` «На что опирается»): меняешь роль или структуру — сверь.

## Каркас
- `AppShell` (`src/ui/shell`) ставит роутер для стадии `active` (`ActiveShell` в `src/app/Shell.tsx`; дерево маршрутов — `routes.tsx`, роутер — `router.tsx`): меню из `NAV`
  (`src/app/nav.ts`), карточка «имя, почта (одна строка, многоточие, полная — `title`; без имени почта на его месте),
  роль · города» (`useAccountCard`, названия городов — `GET /v1/geo/cities?ids=`), под ней строками — переключатель
  языка «RU · EN» (`Segmented`), тема (`ThemeSwitch`) и кнопка «Выйти» во всю ширину (secondary с иконкой; `useLogout()`
  — `admin-staff.md` «Выход»).
- 768–1023 (`useIsCompact`) — полоса значков `size.rail` (название — `Tooltip`, карточка — аватар со шторкой); <
  `breakpoint-mobile` — нижний таббар (первые 4 разрешённых раздела `NAV`) и «Ещё» (шторка: карточка, все разрешённые
  разделы, язык, тема, «Выйти»). Счётчик пункта — проп `counts` по `path` (число) и `NavItem.countLabelKey` (подпись для
  читалки, `{count}`); счётчик «Подтверждения» — `useIncomingApprovalsCount()` в `ActiveShell` (`admin-staff.md`).
- `OfflineBanner` (`useOnline`) стоит в AppShell; `ToastProvider` — в `AppProviders`.
- Палитра — `PaletteProvider` (`src/app/PaletteProvider.tsx`) в `ActiveShell`; `usePalette().open()`
  (`src/ui/palette/context.ts`; вне провайдера — `null`, кнопок нет; поверх модального оверлея — ничего): «Искать
  везде Ctrl K/⌘K» над меню (`palette.open`, `aria-keyshortcuts`, клавиша по платформе), значок поиска первым в полосе,
  первый пункт шторки «Ещё» (шторка снимается `flushSync`).
- Клавиши каркаса: Ctrl/⌘+K — `useShortcut('mod+k', …, { allowInInput })` в `PaletteProvider`; `g x` и `/` —
  `CommandShortcuts` (`src/app/CommandShortcuts.tsx`): `g x` — `<ShortcutBinding>` на раздел из `sectionsFor(me)` (с
  `permission`), переход `run(context)` (вопрос `useLeaveGuard`); `/` — одна регистрация: `findPageSearch()`
  (`ui/table/model.ts`, поле `DataTable.search` с `data-page-search` — `PAGE_SEARCH_ATTR`, не под `inert`/`hidden`), нет —
  палитра. Вторую регистрацию `/` (или `g x`) в экранах не заводить — конфликт. `?` — `ShortcutsProvider`. «Горячие
  клавиши» палитры — `useOpenShortcutsHelp()` после закрытия палитры (фокус — её открывателю).

## Палитра (§4.1, `src/ui/palette`)
`CommandPalette({ open, onClose, context })` — `Modal` (на узком — `Sheet fill`), имя окна `palette.title`;
поле `role="combobox"` (`aria-expanded`, `aria-controls`, `aria-activedescendant`, подпись `palette.input`), список —
`listbox` с группами `role="group"` (недавнее, разделы, действия, города, сотрудники, страны), ↑/↓, Home/End, Enter;
Esc при непустом поле сперва очищает; число результатов — вежливая живая область после паузы ввода; совпадение —
`<mark>`; у каждой группы сущностей своё «Ищем…», «Нет сети» (`fetchStatus: 'paused'`) и ошибка; запрос на сервер —
только если в запросе есть буква или цифра (`MEANINGFUL` в `src/app/paletteSearch.ts`: «--» не уходит). Открытие в
адрес не пишется. Поиск —
`fuzzyRank(query, items, { fields })` (`fuzzy.ts`: регистр, `ё`→`е`, NFKC, раскладка ru↔en `switchLayout`, ранги «с
начала > с начала слова > подпоследовательность», `ranges` для `<mark>`; `keywordsKey` — синонимы на другом языке).

## Горячие клавиши (§4.2, `src/ui/shortcuts`)
`useShortcut(combo, handler, { enabled, allowInInput, label, permission })` — combo по `KeyboardEvent.code` (`mod+k`,
`g c` ≤ `SEQUENCE_MS` = 1 с, `/`, `?`); один слушатель `keydown` на `document` — `ShortcutsProvider` в `AppProviders`;
молчат в полях (`isEditableTarget`: input, textarea, select, contenteditable, combobox, textbox — кроме
`allowInInput`), при IME, под модальной записью стека (`hasModalOverlay`; подсказка и список `Select` не глушат);
односимвольные выключаются переключателем шпаргалки (`SHORTCUTS_SINGLE_KEY` = `wf.admin.shortcuts.single`), Ctrl/⌘+K —
всегда; `permission` из реестра «только с компьютера» — на карманном устройстве нет. `mod` — Ctrl или ⌘; «чужой»
модификатор (Ctrl на Apple, ⌘/Win на прочих — `isApplePlatform()` в `combo.ts`) не совпадает ни с чем. Конфликт — и
одной комбинации, и префикса («g» и «g c») — `console.error`. Шпаргалка `?` — `ShortcutsHelp` (`Modal`, одна строка на
клавишу — последняя регистрация, символ по `navigator.keyboard.getLayoutMap()`, нет — латиница; при выключенных
односимвольных их строки остаются с пометкой «· выключено»); открыть из кода — `useOpenShortcutsHelp()`.

## Списки — `DataTable`
Поиск (`search`, запрос уходит после паузы `SEARCH_DEBOUNCE_MS`; запоздавшее эхо отправленного значения набор не
затирает), чипы (`chips`), раскрывающийся фильтр (`filters: { content, active }` — поля экрана; число заданных — в имени
кнопки), сортировка (`sort` + `onSortChange`), «Показать ещё», карточки вместо строк (`card={(row) => <Card …/>}` —
сетка, список с именем `label`, `columns={[]}`), список выбора (`select: { value, onChange, onActivate, option }` —
`role="listbox"` с `aria-activedescendant`, клик и ↑/↓/Home/End выбирают, Enter и двойной клик — `onActivate`; таблица
заполняет высоту родителя, прокручивается только список), «Показано N» (`summary={false}` — статичная таблица, например
матрица прав), «Сбросить фильтры» (`dirty` + `onReset`); `layout: 'table'` — таблица на любой ширине со своей
горизонтальной прокруткой (по умолчанию `auto` — карточки на узком экране); пусто — `empty` (своё пустое без фильтров,
«Очередь пуста»), ошибка — «Повторить» (`retrying` — занята, пока идёт перезапрос). Роли строк и ячеек — явные: на
телефоне строки-карточки, подпись столбца в ячейке видна и слышна. Скелетон — `LoadingRows` (только здесь; удалять —
сперва перевести на `Skeleton.Table`). Поиск страницы без таблицы строк (матрица флагов) — `SearchBox` (то же поле
`search` таблицы, та же пауза и клавиша `/`).

Виртуализация (§4.5, `src/ui/virtual`): `useVirtualRows({ count, estimate, scrollRef, overscan?, horizontal?, keep? })` (`keep` — индекс, который
всегда в окне: элемент с фокусом) —
обёртка `@tanstack/react-virtual` (`items`, `totalSize`, `scrollToIndex`, `measure` — ref элемента с `data-index`,
`place`/`placeTotal` — положение числами `--wf-virtual-start/-size/-total` через `setCssPx`; вызыватели — только
`src/ui/popover/*` и `src/ui/virtual/*`, CSP-исключение — `admin.md`); `VirtualList({ items, estimate, renderItem, label, onReady? })` —
список с `aria-setsize`/`aria-posinset`, окно прокрутки — высота родителя. Карточные списки — `content-visibility`.
В jsdom размеров нет: `clientHeight` окна (`[data-virtual-viewport]`), `offsetHeight` строк, `scrollHeight` и
`scrollTo` подменяет тест (`virtual.test.tsx`).

Матрица (`src/ui/virtual/VirtualMatrix.tsx`; флаги функций — до 2000 городов столбцами): `VirtualMatrix({ label, rows,
rowKey, corner, rowHeader, fixed, columns, columnKey, columnHeader, cell })` — таблица (`table-layout: fixed`), первый
столбец закреплён (`sticky`), за ним неподвижные `fixed` («Везде»), дальше столбцы-значения: при числе >
`MATRIX_VIRTUALIZE_FROM` = 30 (замер G9-F) — горизонтальная виртуализация, в DOM окно ± запас, пропущенное — распорки
`--wf-virtual-gap`; ширины — `--wf-matrix-head`/`--wf-matrix-col` (через `setCssPx`); `aria-colcount` у таблицы,
`aria-colindex` у каждой ячейки. Клавиатура: столбец с фокусом всегда в DOM (`useVirtualRows({ keep })`), `focusin` —
`scrollToIndex(i, 'auto')`. Тесты — стаб `clientWidth`/`scrollLeft`/`scrollWidth` окна `[data-matrix-scroll]`
(`VirtualMatrix.test.tsx`).

Массовые действия (§4.5, `src/ui/table/{bulk.ts,BulkBar.tsx}`): `BulkAction<T>` (`permission(row) → { perm, cityId? }` —
право по городу строки, `confirm.title(n)`, `run(row, key) → 'executed' | 'pending_approval'`, `concurrency` — по
умолчанию 3). `runBulk(rows, action, { decide(perm, cityId), signal, onProgress, onForbidden })` — по строке, ключ на
строку из `newIdempotencyKey` (`src/api/idempotency.ts`); итог `executed/pending/failed/unknown/skipped/cancelled`;
`retryBulk(report, …)` — `failed` новым ключом, `unknown` прежним (без права остаётся `unknown`), `202` никогда; 429 —
общая пауза пачки (`Retry-After`, иначе `BULK_RETRY_DEFAULT_MS`); 403 и 401 `auth.unauthenticated` — пауза до
`onForbidden` (перечитать me), оставшиеся перепроверяются. `BulkBar({ rows, rowLabel, actions, onClear, onPending,
onSettled? })` — «Выбрано N», `ConfirmDialog` с числом и второй частью «ещё N без права — пропустим», итог в живой
области, «Повторить неудавшиеся»; размонтирование отменяет пачку (итог — тостом); `onPending` — экран передаёт
`useInvalidateApprovals()`, `onSettled` — перечитать список один раз; `onPause(ms)` у 429 — строка «пауза N с»; dual у всех
строк — «Отправить на подтверждение», у части — «из них N уйдут на подтверждение»; после 401 перечитывания me оставшиеся
— `deny`. В таблице — `DataTable.bulk: { selected, onChange, actions, rowLabel, onPending, onSettled? }` (выбор у экрана;
`select`/`card`/`bulk` взаимоисключающие типом): флажок строки (`aria-disabled` + `Tooltip`, если `deny` для всех действий
по городу строки; касание недоступного ставит на него фокус — причина видна и на телефоне), «Выбрать видимые» — все
загруженные доступные (и вне DOM при виртуализации; выбирать нечего — `aria-disabled` с причиной), `BulkBar` при выборе
≥ 1 — на уровне таблицы, вне ветки строк (поиск и чип посреди пачки её не отменяют); исчезнувшие и ставшие недоступными
строки снимаются с выбора (`onChange`). Итог привязан к версии выбора, которую меняет только пользователь
(`selectionVersion`): чистка выбора после перечитывания (`onSettled` убрал выполненные строки) итог не снимает — он живёт
до смены выбора, новой пачки или «Закрыть» (строк нет — остаются итог и «Закрыть»); повтор — строки из снимка итога
(`unknown` с прежним ключом); пачка, у которой пользователь сменил выбор, — итог тостом. «Снять выбор» и «Закрыть» снимают
панель вместе с кнопкой — фокус до этого уходит на «Выбрать видимые» (шапка скрыта на телефоне — флажок первой строки,
строк нет — корень таблицы с временным `tabindex="-1"` без кольца фокуса, r2-2; `BulkBar.focusAfterClose`).

Виртуализация и возврат (§4.5, UF2b.4): `DataTable.virtualize` (по умолчанию при строках > `VIRTUALIZE_FROM` = 200; строки —
распорки `--wf-virtual-gap` через `placeGap`, ключ замеров — `rowKey`; `aria-rowcount` = строки + шапка, `aria-rowindex` с 2;
список выбора — `scrollToIndex` перед сменой выбора стрелкой, `aria-activedescendant` только на строку в DOM; строки-карточки
телефона и `card` — `content-visibility`, без JS). Роутер — `scrollRestoration: true` (ключ — хеш `restoreKeyOf`: путь + search без ключей
панелей, тот же, что `data-scroll-restoration-id`; ScriptOnce на клиенте нет). Цена (принята, принцип 14): записи истории
одного списка делят позицию, и переход из меню на уже прокрученный раздел открывает его на прежней позиции (кэш позиций
вкладки живёт и после смены сотрудника — в нём только хеши и числа); контейнер таблицы — `data-scroll-restoration-id` = хеш ключа позиции (текст адреса в storage роутера не
уходит). `restoreKey` (`src/ui/table/restore.ts`: путь + search без `PANEL_SEARCH_KEYS` из `src/app/paths.ts`) — строка, с которой ушли (щелчок,
Enter), получает фокус при возврате, если фокус потерян; память — в памяти вкладки, стирает размонтирование каркаса.
Тесты — `stubTableLayout()` (`src/test/layout.ts`).

## Состояния
`EmptyState` (`illustration` — таблица «экран → иллюстрация» в `design/README.md`), `ErrorState` (код для поддержки +
«Повторить»; `retryLabel` — своя подпись), `SectionSkeleton` и `ChunkErrorState` («Обновить страницу») — ленивый раздел, `NoNetworkState`, `OfflineBanner`, `ForbiddenState`, `DesktopOnlyNotice` (пометка `ActionGate`),
`RateLimitedState` (`retryAfter` из `ApiError`, отсчёт; повторный 429 с тем же значением — `key={error}`; `page` —
вместо страницы, заголовок `h1`), `PendingApprovalNotice` (после `202`), `ConnectionErrorState` (стадия `error` сессии:
сеть и 5xx — «нет связи»; с `error` 4xx — «сервер не пустил», текст по коду и код для поддержки).

## Примитивы
- `Button` (`variant`: primary | secondary | danger | danger-outline | link; `size="lg"` — вход; `loading` —
  `aria-disabled` + `aria-busy`, не `disabled`: фокус не падает на body, клик и неявная отправка формы гасятся; по
  умолчанию `type="button"`), `StatusChip` (`tone`, текст обязателен), `CountBadge` (0 не рисуется, >99 — «99+»),
  `Avatar` (инициалы, декоративный), `QrCode` (`value`, `label`; матрица — `uqr`, SVG-путь без inline-стилей, без сети).
- `Segmented` (сегменты в одну строку: `label` группы, `options` `{value, text, name?, title?, lang?}` — `name`
  начинается с видимого `text`, WCAG 2.5.3; сегмент-значок — `{value, icon, name, title?}`: видимого текста нет, `name`
  обязателен, `title` — для мыши; `aria-pressed`, `busy` — `aria-disabled`, фокус остаётся, нажатие гасится; `wide` —
  подписи словами, сегмент по ширине своей подписи, а не 40 px: «Все · Живые · Пилот · Ожидание»).
- `Switch` (`checked`, `onChange`, `label`, `hideLabel` — подпись только для читалки, `disabled`, `busy` — запрос в
  полёте: `aria-busy` + `aria-disabled`, фокус остаётся, нажатие гасится; `role="switch"` + `aria-checked`, Space/Enter
  — нативная кнопка).
- `Icon` (`name` из `@wf/icons`, `size` 16 | 20 | 24; без `label` — `aria-hidden`, с `label` — `role="img"`; толщина —
  `stroke-width` атрибутом: слой × `OPTICAL[size]`, у иконок меню `nav` — абсолютная; цвет — классами ролей: предмет —
  `currentColor`, деталь — `text-subtle`, акцент — `accent-ink`, тень акцента — `accent-detail` (токен-пара, темнее
  акцента в обеих темах), вырез — `bg-elevated` (на иной поверхности — класс родителя), вырез на акценте —
  `text-inverse`), `Illustration` (`size` 48 | 64, всегда декоративная, предмет — `text-muted`).
- `Card` (`title`, `chip`, `stats` `{value, label}[]`, `note`, `action` — кнопка `{label, onClick, variant?, disabled?}`
  (описана заголовком; `variant` по умолчанию secondary) или ссылка `{label, name, href, onNavigate, onIntent?}`, `selected` —
  только вид, `headingLevel`; `article` с именем по заголовку; нажатая ссылка — источник View Transition `.vtSource`
  (`wf-entity`, одна на вкладку, снимается со списком), цель — `PageHeader entity`).
- Главная (`src/ui/home`): `AttentionTile` (`title`, `value`, `cap?`, `tone`, `href` + `onNavigate`/`onIntent?` — вся
  плитка ссылка, имя — заголовок и число; «99+» при `value > cap` (99) или `more`; `state` — `attentionState(query)`:
  скелетон, ошибка «Не загрузилось · Повторить» без ссылки (`onRetry`, `retrying`), `note` — описание ссылки, не имя) в
  сетке `AttentionTiles({ label })` (список; ≥ 720 px области — четыре в ряд); `TodoList({ items: TodoItem[], error?:
  TodoError })` — строки-ссылки с точкой тона, пусто — иллюстрация `goal` и «Всё спокойно», ошибка источника — строка
  «Повторить» (пусто + ошибка — только она). После удачного «Повторить» фокус — `useRetryFocus` (если фокус потерян).
- `useCountdown()` (`{ left, start(seconds) }` — отсчёт 429 по `retryAfter` и паузы повторной отправки; `start` с тем же
  числом перезапускает).
- Подсказки и копирование (§4.8): `Tooltip` (`src/ui/hint`; наведение и фокус, `aria-describedby`),
  `TermHint({ term, children | describedBy })` — «i» с текстом `glossary.<term>`; без `children` кнопка описана внешним
  `describedBy` (термин — заголовок, кнопка вне `h2`, «Правило двух рук»); `CopyButton({ value, label, display? })` —
  галочка и живая область, отказ буфера — значение в поле «вручную» (многострочное — `textarea`): id, почта, адрес,
  GeoNames, секрет и коды 2FA.

## Поля
- `SelectField` — свой `Select` (`src/ui/form/Select.tsx`, §4.16: select-only combobox WAI-ARIA, имя — подпись поля,
  список — `Popover kind 'menu'`, телефон — `Sheet` (> 10 опций — поиск), закрытый список не в DOM, необязательное с
  `placeholder` — опция `''`); `MultiSelectField` (тот же Popover/Sheet, `listbox aria-multiselectable`, поиск в якоре;
  `searchable`, `onSearch` — поиск на сервере); `DateField` (`name, label, min, max, hint, rules`; §4.17: текст в
  формате языка с маской `isoDate.ts`, календарь — `Popover kind 'modal'`/`Sheet`, значение `YYYY-MM-DD`, неделя —
  `src/i18n/week.ts`); `CheckboxField`; фокус после шторки — `useFocusAfterSheet`.
- `OtpInput` — код второго фактора: `onComplete(code)` один раз на полный ввод, после ошибки — `ref.current.reset()`
  (очистка, `onChange('')` — экран не отправит стёртый код — и фокус; пока ячейки `disabled` — фокус встанет, когда их
  включат); на время запроса — `busy` (ячейки `readOnly`, ввод и вставка гасятся, группа `aria-busy`; не `disabled` —
  фокус не падает на body), `disabled` — только когда ввод закрыт совсем (вход закрыт на час); резервный код — обычный
  `TextField`.

## Оверлеи
- `Modal`, `Drawer`, `Sheet`, `ConfirmDialog`: фокус-ловушка, Esc (только верхний), возврат фокуса на открыватель
  (запоминается до `autoFocus` содержимого), блок прокрутки — внутри. `ConfirmDialog` — `confirmWord` (слово или почта,
  точное совпадение с учётом регистра, пробелы по краям не считаются; пока `loading` — поле `readOnly`, кнопка
  `aria-disabled`; `children` — поля над вводом слова, например причина, в `FormProvider` экрана — у диалога своя
  `<form>`; `error` — ошибка запроса внутри диалога, он не закрывается и введённое не теряет); на узком экране сам
  становится шторкой (`useIsMobile`, порог — токен `breakpoint-mobile`).
- `Popover` (`src/ui/popover`, §4.8) — основа всплывающего: `popover="manual"` в верхнем слое, рендер на месте (без
  портала: пузырь внутри `aria-modal`-панели); `kind` `hint` (`role="tooltip"`) | `menu` (роль у содержимого) | `modal`
  (`role="dialog"`, своя ловушка Tab, фокус внутрь и обратно на якорь). Положение — `--wf-pop-x/-y/-max-h` (и
  `--wf-pop-anchor-w`) от **своего** якоря (`position.ts`: переворот, у края — по концу якоря; прокрутка предка,
  `resize`, `ResizeObserver` якоря и пузыря — один кадр на пересчёт); якорь ушёл из видимой области — подсказка
  закрывается, меню прижато к краю; якорь пропал — закрыть.
- Стек оверлеев с видом записи (`pushOverlay(id, panel, { modal })`, `stack.ts`): Esc — верхняя из всех записей; ловушка
  Tab, блок прокрутки, `topOverlayPanel()`, `hasModalOverlay()` — только модальные. Немодальный закрывается по
  `focusout` (фокус покинул и якорь, и пузырь) и по `pointerdown` вне них (Safari); `mousedown` в нём отменён (кроме
  `input`/`textarea`), `pointerdown` — нет. Модальный — фокус при показе на `[data-autofocus]`, иначе первое
  фокусируемое; закрывается `click` вне якоря и пузыря (`'outside'`, APG); фокус — на якорь, и после снятия содержимого
  — но щелчок по другому полю оставляет фокус в нём. Открытие любого `Overlay` закрывает все `Popover` (`onModalOpen`).
- Тосты — успех и справка исчезают через 8 с (`TOAST_HIDE_MS`, пауза при наведении и фокусе), ошибка — только руками.
  Живые области постоянные и без ролей (`aria-live` polite/assertive). Порядок: успехи и справки — над ошибками, внутри
  области — по появлению. При открытой модалке тосты входят в цикл Tab; закрыли тост с фокусом (кнопкой или
  `dismiss(id)`) — фокус туда, откуда пришли, иначе на верхний диалог по стеку оверлеев. Тост с `undo` («Отменить»,
  §4.10) сам не исчезает, новый такой заменяет прежний, `dismissUndo()` снимает их (выход и смена сотрудника —
  `UndoReset` в `AppProviders`); пока он на экране — `<html data-toast-open>` (отступы прокрутки страницы и тела
  панели). Esc закрывает новейший тост только при пустом стеке оверлеев (m-h). Пока верхняя модальная — панель с
  подвалом, `<html data-overlay-footer=<placement>>` (`stack.ts`): тосты над подвалом (токен `size.overlay-footer`,
  у шторки — сверху); на телефоне — над таббаром, одной строкой; любая верхняя модальная — `<html data-overlay-open=
  <placement>>`: на телефоне без подвала тосты у края окна, тело `.right`/`.bottom` (и `.center` на телефоне) — с запасом
  `toast + space-6`, нижние кнопки над тостом. На широком при тосте с «Отменить» окно по центру (с подвалом и без)
  ниже предела (`max-height`: зазор под окном ≥ `space-6 + toast + space-2`) — высокое `ConfirmDialog` не уходит под тост;
  окно ниже предела не меняется. Окно по центру с подвалом (`data-overlay-footer='center'`) — подвал не у края, тосты
  на обычном месте под окном (широкий — `space-6`, телефон — `space-2`); на телефоне при «Отменить» — свой предел
  (зазор ≥ `space-2 + toast + space-2`; r2-1, история `Toast:OverTallModalFooter`). На широком высота тоста не ограничена: длинный текст (3 строки) выше
  `size.toast`, и запаса тела или подвала во 2 ряд (R2-m3) может не хватить — тексты тостов держать короткими.
  `useUndoable` показывает тост по промису действия (`mutateAsync`), а не колбэку `mutate`: экран снят или второй
  вызов — тост всё равно есть; итог отмены после смены сотрудника или выхода не показывается (`undoEpoch` и
  сотрудник по кэшу me: смена отпускает ждущую разблокировку `reauthBus.reject()` раньше эффекта `UndoReset`).
- `Sheet fill` — шторка во всю высоту своего предела (палитра на узком).
- `Overlay.dirty` — Esc, фон и «Закрыть» сперва спрашивают `LeaveDialog` («Уйти без сохранения?», фокус на
  «Остаться»); «Уйти» — `onClose`.

## Тема (§4.12, `src/ui/theme`)
- Выбор — `localStorage['wf.theme']` (`system` | `light` | `dark`; свойство браузера, не сотрудника — выход и смена
  сотрудника его не стирают), применение — атрибут `<html data-theme>`. До первого кадра его ставит
  `public/theme-init.js` (обычный скрипт-файл в `<head>` до стилей — CSP `script-src 'self'`; синтаксис ES5, Vite
  копирует как есть; storage бросает — `system`; без JS — статический `data-theme="system"` в `index.html`; admin-api
  отдаёт его с `no-cache`); явный выбор красит обе `theme-color` уже там (`BG` = `tokens.color.bg`, страж).
- `ThemeProvider` (внешний в `AppProviders`) при монтировании берёт атрибут (не storage),
  `useTheme() → { theme, resolved, setTheme }` (`resolved` — по `matchMedia('(prefers-color-scheme: dark)')` с
  подпиской, без `matchMedia` — `dark`; `setTheme` пишет storage в `try/catch` и `dataset` — не `element.style`);
  событие `storage` другой вкладки применяет текущее значение storage, а не `e.newValue` (две вкладки почти
  одновременно). Обе `<meta name="theme-color">` при явном выборе — `bg` темы, при `system` — `bg` темы своего `media`
  (из `tokens.ts`, совпадает с `index.html`). Разбор значения у скрипта и провайдера один (`parseTheme`, страж
  `theme-init.test.ts`). Цвет темы в JS — `useThemeColors()` (из `tokens.ts`, не `getComputedStyle`).
- `ThemeSwitch` — `Segmented` «Тема» из значков `monitor`/`sun`/`moon` («Система · Светлая · Тёмная»): карточка
  сотрудника, шторка «Ещё», экраны входа (`AuthLayout`, справа от бренда после языка). Смена — мгновенно, без анимации.

## Витрина
`./task admin:stories` (запуск и CI — CLAUDE.md «Проверки»; Ladle со своим Vite 6 — `.ladle/`). Каждый компонент
`ui/index.ts` — с `*.stories.tsx` рядом (страж `src/ui/stories.test.ts`, исключения с причиной — там же); история на
состояние, тексты из локалей, данные — как фикстуры; `storyName`/`title` латиницей, «`.stories.`» — только в имени
файла. Метки `StoryMeta` (`src/showcase/csf.ts`): `visual` — ключевые (сравнение CI), `layout` — три ширины,
нестабильным — `visual: false`. Обёртка — `ShowcaseProvider` (`src/showcase/Provider.tsx`: тема, `?locale=en`,
`SHOWCASE_NOW`, UTC, без сети); с сессией и роутером — `SessionFrame`/`RouterFrame` (`frames.tsx`).

## Режим `shots` (снимки для ревьюера, `./task admin:shots`, §5.4)
`vite --mode shots` — порт 5176, свой `cacheDir`, без прокси; плагин `wf-shots` (`vite.shots.ts`) подменяет
`src/test/server.ts` и `src/test/base.ts` браузерными `src/shots/{server,base}.ts` через `resolveId` по абсолютному
пути, отдаёт `/mockServiceWorker.js` из пакета `msw` (не из `public/`) и ставит точку входа `src/shots/main.tsx`
(`?shots-role=owner|admin|moderator|setup|anonymous`, `?shots-locale=en`, `?shots-story=<путь>:<экспорт>`; у `anonymous`
язык задаёт `--locale` скрипта — язык браузера; `?shots-fixture=home-busy|palette-error` — данные состояния
(`SHOTS_FIXTURES` в `src/shots/fixtures.ts`: главная с делами и ошибкой, 503 поиска городов в палитре);
`?shots-palette=<текст>` — после первой отрисовки открыть палитру и ввести текст). База моков — только `TEST_API` из `src/test/base.ts`; фикстуры для
браузера не тянут `vitest`, `@testing-library`, `node:*` (`src/test/{app,render,setup}.tsx` из `src/shots` не
импортировать). Страж `src/shots/shots.test.ts`: граф импортов от `src/main.tsx` (и динамические `import()`) без `msw`,
`src/shots`, `src/test`; глобальные стили — только через `src/app/styles.ts`. `ShotsRoot` фиксирует «сейчас»
(`NowProvider(SHOWCASE_NOW)`); `vite build --mode shots` → `dist-shots` (не `dist`) — экраны визуальных тестов CI.
