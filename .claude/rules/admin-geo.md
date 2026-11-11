---
paths:
  - "apps/admin/src/features/geo/**"
  - "apps/admin/src/test/geo.ts"
---

# Админка: «Справочники» (`src/features/geo`)

Ядро — `admin.md`; план B, Task 17–22.

## Раздел
- Группа меню `nav.groups.directories` после «Безопасности»: «Города и запуск» (`/geo/cities`, `geo.cities.read`;
  страница города `/geo/cities/$cityId` — вложенный адрес раздела, его право), «Страны» (`/geo/countries`,
  `geo.countries.read`), «Импорт GeoNames» (`/geo/imports`, `geo.imports.read`), «Версии приложения» (`/app/versions`,
  `platform.versions.read`), «Флаги функций» (`/flags`, `platform.flags.read`) — все открыты и на телефоне. Разделы,
  экран которых ещё не построен, — временно только шапка (`pendingSection` в `router.tsx`; задача экрана заменяет).
- Чтения — `features/geo/queries.ts` (`useGeoCities`, `useGeoCity`, `useGeoDistricts`, `useGeoCountries`,
  `useGeoRegions`, `useGeoPlaces`, `useGeoImports`, `useGeoImport`, `useGeoImportJob`, `useAppVersions`,
  `useFeatureFlags`): ключи `['geo', …]` (`CITIES_KEY` — списки, карточка, районы и подписи городов модератора; язык
  ответа — в ключе, `Accept-Language` интерфейса), версии и флаги — `['platform', …]`; массив `status` у `listGeoCities`
  уходит повторённым параметром (`status=pilot&status=live`).
- Статус города — `useCityStatus()` (`features/geo/status.ts`): живой — успех, пилот — внимание, ожидание — нейтральный,
  незнакомый — «—».
- Тесты: `geoServer()` из `src/test/geo.ts` — чтения geo, версии, флаги по контракту, фикстуры `country`, `region`,
  `place`, `cityDetails`, `district`, `geoImport`, `importJob`, `appVersions`, `flag`.

## «Города и запуск» (`CitiesPage`)
- Карточки (`CityCards`: название, чип статуса, население, районы, регион или «регион не задан», «страна выключена»;
  живой — `selected`; «Открыть: <город>» — ссылка на страницу; пусто — `illustration="pitch"` и «Активировать город»).
- Фильтры — search-параметры `/geo/cities` (`parseCitiesSearch` в `validateSearch`: `q` ≤ 100, `status` — массив
  `CityStatus` (роутер держит JSON-ом; строка — массив из одного), `country` ISO, `region_id` uuid (без `country`
  отбрасывается); негодное отбрасывается; `replace`): поиск, чипы «Все/Живые/Пилот/Ожидание» (один статус; «Все» убирает
  `status`), страна и регион — «Ещё фильтры» (значение из адреса в списке сразу, пока справочник грузится).
- «Активировать город» — `geo.cities.activate` **и** `geo.places.search`: `ActivateDrawer`.
  - Шаг 1 — поиск места (`PlaceSearch`: `listGeoPlaces`, `q` 1–100 символов, без него — подсказка с
    `illustration="ball"` и без запроса; поиск и «Страна места» держит окно (`PlaceFilter`); выбор держится, пока его
    строка в результатах; места — список выбора `DataTable select`, прокручивается только он). Действие — `PlaceAction`
    в подвале: «Выбрать» (до выбора недоступна, Enter по месту — то же), заведённое место — «уже заведён» и «Открыть
    город», выбрать нельзя; «Активировать» шага 2 — тоже в подвале, `form={id}`.
  - Шаг 2 — «Страна города» (по умолчанию — места; список — включённые и страна места), «Регион» (пусто — «по данным
    GeoNames», при смене страны — сброс и «без региона» с подсказкой), «Статус» (`waitlist`; список фиксированный —
    `waitlist`/`pilot`/`live`). Тело — `geoname_id`, `status`, `country_code` ТОЛЬКО при переопределении (иначе сервер
    снял бы регион по умолчанию), `region_id` — только выбранный.
  - Успех — карточка в кэш `getGeoCity`, `CITIES_KEY` инвалидирован, тост и переход на город (ответ пришёл после ухода
    со страницы — без перехода); `409 geo.city_already_active` — текст и «Найти город в списке» (`?q=` названия места),
    место перечитано — с `city_id` «Открыть город» ведёт на него; 409 и успех — `GEO_PLACES_KEY` устарел; исход
    неизвестен и панель закрыта — `invalidateCityLists`; `422 geo.country_not_enabled` / `geo.region_not_found` — у
    поля; повтор после обрыва — тот же ключ.

## Страница города (`CityPage`, `/geo/cities/$cityId`)
- `DataState` + `CityPageSkeleton`; `cityId` не uuid — «Город не найден» без запроса; `useGeoCity` — `placeholderData`
  того же `cityId`, пока карточка перечитывается на новом языке (формы только для чтения, ввод не стирается:
  `keepDirtyValues`; после ответа — `reset` к карточке сервера, удаление перевода — `resetField`). Ответ `getGeoCity`
  403 — `ForbiddenState` (модератор чужого города), 404 — «Город не найден».
- Шапка — название, чип статуса, «Привязать к GeoNames» (без `geoname_id`, с `geo.cities.manage` **и**
  `geo.places.search`); над шапкой — `BackLink` каркаса «← Города и запуск» с фильтрами списка. «Сведения о городе» —
  страна «Имя (КОД)» и «выключена», регион или «не задан», население, GeoNames или «не привязан», пояс, адрес (адрес и
  GeoNames — `CopyButton`).
- Карточки — по праву в ЭТОМ городе (`useCan(право, city.id)`): «Статус» (`CityStatusCard`; незнакомый статус — контракт
  открытый — опция «—» недоступна, отправка — ошибка поля) и «Адрес в вебе» — `geo.cities.manage` (без права карточек
  нет), «Названия» — `geo.cities.rename` (без права — только чтение), «Районы» — `geo.districts.manage` (без права —
  таблица без действий).
- Правки — `features/geo/mutations.ts`: ответ `updateGeoCity`/`linkGeoCity` — карточка в кэш `getGeoCity`, списки и
  подписи (`CITIES_KEY` без карточек и районов, `invalidateCityLists`) — устарели; районы — перечитываются районы,
  карточка и списки. Адрес — формат `^[a-z0-9]+(-[a-z0-9]+)*$` ≤ 100 до запроса, `409 geo.slug_taken` — у поля, история
  прежних адресов; названия — `ru`, `en`, язык страны «основное» (обязательно, без удаления), «Сохранить» — только
  изменённые непустые, удаление перевода — `ConfirmDialog` (слово «УДАЛИТЬ»), тело `names: {<язык>: null}`; районы —
  добавление и переименование (окно) с `409 geo.district_name_taken` у поля, «В архив»/«Вернуть» — `{archived}`, отказ —
  тост. Отказ сервера (403) — текстом формы, страница цела.
- Открыватель окна исчез после действия — фокус не на body: привязка — на «Сведения о городе» (`tabIndex -1`), удаление
  перевода — на поле языка, архив/возврат района — на кнопку этой строки (`data-district-action`; строка переезжает в
  DOM); эффект ждёт и закрытия окна, и обновления карточки (порядок отрисовок любой).
- `LinkPlaceDialog` — `PlaceSearch` (общий с активацией; фильтр — страна города; `bounded` — у окна по центру высоты
  нет) → `{geoname_id}`, «Выбрать»/«Привязать» — в подвале окна; коды — текст в окне. Успех и
  `409 geo.city_already_active` — `GEO_PLACES_KEY` устарел; `409 geo.city_already_linked` — карточка перечитывается.
