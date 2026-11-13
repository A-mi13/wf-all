# Спека модуля `geo`

Дата: 02.10.2026. Статус: на ревью (редакция 3 — по итогам ревью редакций 1 и 2).
Родительская спека: `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` (дальше — «спека
бэкенда»), §13 п. 1. Домен: `docs/01-домен-и-правила.md`, раздел «Геоиерархия».

## 1. Зачем и для кого

`geo` — первый продуктовый модуль. От него зависит регистрация (`identity`): город обязателен, порог
возраста и валюта берутся из страны города. Нативному приложению (Flutter) модуль даёт четыре
экрана: выбор города, автоопределение города по геопозиции, настройки города, принудительное
обновление приложения.

Решения пользователя (02.10.2026):

1. Публичный список и автоопределение отдают города всех статусов, статус — в ответе: `waitlist`
   клиент показывает как «пока нет игр — станьте капитаном» (экран «Пустой город»). Это меняет
   строку домена «включаем только города с подтверждённым капитаном» (§12).
2. Источник городов — GeoNames (CC BY 4.0) с активацией: импорт в таблицу-источник, админ находит
   место поиском и заводит город.
3. Админ при активации может переопределить страну и регион города (пример: GeoNames относит
   Севастополь и Симферополь к `UA`); источник можно загрузить и по стране, которая у нас не
   включена — источник на продукт не влияет.
4. Минимальная версия приложения — в базе, меняется ручкой admin-api с аудитом, без деплоя.
5. Админские ручки `geo` пишутся сразу и правильно — вход сотрудника, право из ролей,
   идемпотентность, аудит. Вход сотрудников — модуль `staff` (спека сотрудников, план A), а не
   `identity`: ручки работают с сессией сотрудника, без неё — 401 (§5.1). Временных обходов нет.
6. `/v1/health` удаляется из публичного контракта сейчас: правило «ломать public только в `/v2`»
   уточняется — до первого тега `contracts-v1.0.0` осознанные ломающие правки public допустимы
   через явный файл исключений (§4.4).
7. Без спешки и без отступлений от принятых решений; объём не урезается.

Успех этапа 1 (§11): на стенде нативщик получает список городов (Ставрополь, Михайловск — оба
`pilot`, с точками), город с настройками, ближайший город и место по координатам, `min-version`.
Ожидаемо: `min-version` до `identity` отдаёт пустые поля (задать версии нечем — админка закрыта),
города `waitlist` и новые города появятся после `identity` (активация админом).

## 2. Границы модуля

Владение (спека бэкенда §4.1, `internal/archtest/ownership.go` — каждая строка ниже обязана
попасть в `Schema`, иначе падает `TestEverySchemaObjectHasOwner`):

| Объект | Владелец |
| --- | --- |
| `countries`, `regions`, `cities`, `districts` (есть, `0002_geo.sql`) | `geo` |
| `country_names`, `region_names`, `city_names`, `city_slug_history` (новые) | `geo` |
| `geonames_places`, `geonames_place_names`, `geonames_admin1`, `geonames_admin1_names`, `geonames_imports` (новые) | `geo` |
| представление `geo_read_city_settings` (экспортированное, новое) | `geo` |
| представление `city_settings` (было в 0002) | удаляется в `0019` (§3.7) |
| `app_versions` (новая) | `platform` |

Минимальная версия — механизм платформы: таблица и логика — `internal/platform/appversion`
(хранилище, сравнение semver, правила), ручки — в `internal/httpapi/{public,admin}` (платформа не
импортирует `httpapi`, страж archtest), тег `platform`. В этой спеке потому, что план
«Фундамент 3/3» отнёс её к первому модулю.

Не входит: заявка на капитана в новом городе (`teams`), реакции на смену статуса города
(`teams`, `notify`), поля и `nearby_pitches()` (`pitches`), вход сотрудников, сессии admin-api,
выдача ролей (`identity`).

## 3. Модель данных

Миграция `0019_geo.sql` (одна; всё ниже). sqlc разбирает грамматику PostgreSQL 17: синтаксис PG18 не
использовать, генерируемые колонки — явно `STORED` (в PG18 по умолчанию `VIRTUAL`, на ней нет
индекса). `down` возвращает схему `0002` в точности, включая `city_settings` и уникальность
`cities_country_name_key`.

### 3.1. Источник GeoNames

```
geonames_imports (
  id uuid PK, country_code char(2) NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
  status text NOT NULL CHECK (status IN ('running','succeeded','failed')),
  source_files jsonb NOT NULL DEFAULT '[]',  -- [{name, url, bytes, sha256}]
  started_at timestamptz NOT NULL, finished_at timestamptz,
  places_upserted int, places_removed int, places_missing int, names_upserted int,
  reconciled jsonb,                           -- отчёт сверки §5.4
  error text, started_by uuid                 -- сотрудник; NULL — команда оператора
)
-- один импорт страны за раз
UNIQUE INDEX geonames_imports_running ON geonames_imports (country_code) WHERE status = 'running'

geonames_places (
  geoname_id bigint PK, country_code char(2) NOT NULL, admin1_code text,
  name text NOT NULL, ascii_name text NOT NULL,
  ascii_name_normalized text GENERATED ALWAYS AS (normalize_text(ascii_name)) STORED,
  feature_code text NOT NULL, population bigint NOT NULL DEFAULT 0,
  location geography(Point, 4326) NOT NULL, timezone text NOT NULL,
  missing_since timestamptz,                  -- пропало из источника, но на него ссылается город
  import_id uuid NOT NULL REFERENCES geonames_imports(id)
)
geonames_place_names (geoname_id → geonames_places ON DELETE CASCADE, locale text, name text,
  name_normalized text GENERATED ALWAYS AS (normalize_text(name)) STORED, PK (geoname_id, locale))
geonames_admin1 (country_code char(2), admin1_code text, geoname_id bigint, ascii_name text,
  PK (country_code, admin1_code))
geonames_admin1_names (country_code, admin1_code, locale, name, PK (country_code, admin1_code, locale))
```

Индексы: GiST `geonames_places (location)`; `geonames_place_names (name_normalized
text_pattern_ops)` и `geonames_places (country_code, ascii_name_normalized text_pattern_ops)` (индекс по
выражению с `normalize_text` не строится: с PG17 `CREATE INDEX` вычисляет выражения с `search_path =
pg_catalog, pg_temp`) — поиск по префиксу в админке.

### 3.2. Справочник

- `cities`:
  - `+ geoname_id bigint UNIQUE` (без FK: источник перезагружаем, город — нет) — идентичность
    города; `+ population bigint NOT NULL DEFAULT 0` (фиксируется при активации, импорт её не
    меняет у заведённых городов); `+ version bigint NOT NULL DEFAULT 1` (§6).
  - **снять** `cities_country_name_key`: в GeoNames 269 тёзок в России, 41 — внутри одного
    региона (Михайловск 70 и 71, Заречный ×5), а `normalize_text` даёт `''` для письменностей кроме
    латиницы и кириллицы; вместо неё — неуникальный индекс `(country_id, name_normalized)`.
  - `centroid geography(Point, 4326) NOT NULL` — в этой же миграции после заполнения сида (§3.6);
    GiST-индекс по `centroid`.
  - `name` — каноническое название на языке страны (`countries.default_locale`), зеркало строки
    `city_names` этого языка (§3.3); `slug` —
    `^[a-z0-9]+(-[a-z0-9]+)*$` (дефис только между частями), не длиннее 100 символов (CHECK
    `cities_slug_format`; длинный slug при активации обрезается — geo 2/2), уникален глобально.
- `regions`: `+ geoname_admin1_code text`, уникальность `(country_id, geoname_admin1_code)` где не
  NULL.
- `countries`: `+ version bigint NOT NULL DEFAULT 1`.
- `districts`: `+ archived_at timestamptz`, `+ version bigint NOT NULL DEFAULT 1`; уникальность
  `districts_city_name_key` заменяется частичной `(city_id, name_normalized) WHERE archived_at IS
  NULL` — имя архивного района свободно; дубль — 409 `geo.district_name_taken`. Районы не
  удаляются: на них ссылаются `pitches.district_id`, `challenges.district_id` (`ON DELETE SET NULL`),
  а читать чужие таблицы `geo` не может (§4.2–4.3 спеки бэкенда). Архивный район скрыт из публичных
  ответов, ссылки на него остаются.
- `city_slug_history (slug text PK, city_id uuid NOT NULL REFERENCES cities, replaced_at
  timestamptz NOT NULL)`, slug — тот же формат (CHECK `city_slug_history_slug_format`), строки
  удаляются вместе с городом (`ON DELETE CASCADE`); время — из часов записывающего, без
  `DEFAULT now()`; старые `slug` продолжают находить город (ссылки веба).

Сверх этого (план geo 1/2, R13): названия `*_names` непустые (`btrim(name) <> ''`), `population >= 0`,
`version > 0`, CHECK `country_code`/`locale` в таблицах `geonames_*`, `*_names` удаляются каскадом;
имена ограничений для 23505 — `cities_geoname_id_key`, `districts_city_name_active_key`,
`geonames_imports_running`, `regions_country_admin1_key`.

### 3.3. Названия по локалям

`country_names (country_id, locale, name)`, `region_names (region_id, locale, name)`,
`city_names (city_id, locale, name, name_normalized STORED)` — PK `(id, locale)`, `locale` — CHECK
`^[a-z]{2}$`, поддерживаемые сейчас — `ru`, `en` (константа модуля рядом с `backend/locales`).

- Строка на языке страны — **источник** канонического имени: правка этой строки админом в той же
  транзакции переписывает `cities.name` (`regions.name`, `countries.name`). Обратного пути нет.
- Импорт и сверка заполняют только **отсутствующие** строки (`ON CONFLICT DO NOTHING`): правки
  админа не затираются.

### 3.4. Выбор названия из GeoNames

Для места и локали `L` (детерминированно, в таком порядке):

1. Кандидаты — альтернативные названия места с `isolanguage = L`, без `isHistoric`, `isColloquial`,
   с пустым или будущим `to`.
2. Предпочтительное (`isPreferredName`) и не краткое (`isShortName` пусто).
3. Не краткое и написанное письменностью языка `L` (`ru` — кириллица, `en` — латиница).
4. Предпочтительное (в том числе краткое).
5. Написанное письменностью языка `L`.
6. Минимальный `alternateNameId` среди оставшихся кандидатов.
7. Для языка страны, если кандидатов нет: предпочтительное название без языка (`isolanguage` пусто)
   в письменности языка; иначе `name` GeoNames. Для `en` без кандидатов — `ascii_name`.

Проверено на данных 02.10.2026: Ставрополь — ru «Ставрополь» (шаг 3: ru-варианты без
`isPreferredName`, кириллица), en «Stavropol» (шаг 4: предпочтительное и краткое); Михайловск — ru «Михайловск»
(шаг 2). Регионы — те же правила по `geonameid` региона из `admin1CodesASCII.txt` (4-я колонка):
у Ставропольского края предпочтительное ru — краткое «Ставрополье», шаг 3 выбирает полное
«Ставропольский Край»; админ правит (§3.3).

### 3.5. Минимальная версия приложения

```
app_versions (
  platform text PK CHECK (platform IN ('ios','android')),
  min_version text NOT NULL CHECK (min_version ~ '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$'),
  recommended_version text NOT NULL CHECK (recommended_version ~ '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$'),
  store_url text NOT NULL CHECK (store_url ~ '^https://'),
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  updated_at timestamptz NOT NULL DEFAULT now()
)
```

`min_version ≤ recommended_version` — в коде (semver не сравнивается строкой). `store_url` — только
`https` и хосты `apps.apple.com` (ios), `play.google.com` (android). Клиент сравнивает
`MAJOR.MINOR.PATCH`, отбрасывая сборку (`1.2.3+45` → `1.2.3`).

### 3.6. Данные в миграции

`0019` заполняет то, без чего справочник неконсистентен (значения проверены по GeoNames 02.10.2026):

| `slug` | `geoname_id` | `centroid` (lat, lon) | `population` |
| --- | --- | --- | --- |
| `stavropol` | 487846 | 45.03442, 41.9642 | 433931 |
| `mihaylovsk` | 493702 | 45.13097, 42.02703 | 59198 |

Регион «Ставропольский край» — `geoname_admin1_code = '70'`. Для существующих стран, регионов и
городов — строки `*_names` на языке страны из канонического `name`; en-строки сида — «Russia»,
«Stavropol Krai», «Stavropol», «Mikhaylovsk». Затем `centroid SET NOT NULL`; если в базе есть другие
города без точки (не на чистой базе и не на стенде) — миграция падает с текстом «города без
centroid: <slug…> — заполните точки (см. спеку geo §3.6)», а не молча.

### 3.7. Экспортированное представление

`geo_read_city_settings` (спека бэкенда §4.2): `city_id`, `city_name`, `slug`, `timezone`, `status`,
`centroid`, `country_id`, `country_code`, `country_enabled`, `currency`, `default_locale`,
`phone_prefix`, `week_starts_on`, `min_signup_age`, `age_of_majority`. `city_settings` удаляется:
ссылки на неё — `internal/archtest/ownership.go` (`Schema`, `legacyExported`), фикстуры
`ownership_test.go`, текст спеки бэкенда §4.1–4.2 — переводятся на новое имя в том же коммите;
`down` возвращает `city_settings`. Удалить или переименовать колонку `geo_read_city_settings` —
ломающее изменение API модуля.

### 3.8. Права ролей (`grants.sql`)

| Роль | Права |
| --- | --- |
| `api` | SELECT: `countries`, `regions`, `cities`, `districts`, `*_names`, `city_slug_history`, `geonames_places`, `geonames_place_names`, `geonames_admin1*`, `app_versions` |
| `admin` | SELECT на всё перечисленное и `geonames_imports`; INSERT/UPDATE: `countries`, `regions`, `cities`, `districts`, `*_names`, `city_slug_history`, `app_versions`; DELETE: `country_names`, `region_names`, `city_names`, `city_slug_history` (возврат города к своему старому `slug` удаляет строку истории, §5.2; план B, `0021`) |
| `worker` | DML: `geonames_*`; SELECT/UPDATE: `cities`, `regions` (сверка §5.4); SELECT/INSERT: `city_names`, `region_names` (только отсутствующие переводы); SELECT: `countries`, `country_names` |

Каждое отличие от «DML всем» — ветка CASE в `grants.sql` и случай в `grants_test.go`.

## 4. Публичный API

Контракт `contracts/openapi/public/geo/` (версия — `public/platform/`), теги `geo` и `platform`. Все
операции анонимные (`security: []`). Конвенции — `.claude/rules/contracts.md`: полное описание и
примеры, `x-error-codes`, открытые enum'ы в ответах (`x-extensible-enum`).

### 4.1. Схемы

- `CityStatus` — `waitlist | pilot | live` (открытый enum).
- `City` — `id`, `slug`, `name`, `status`, `timezone`, `location {lat, lon}`, `region {id, name} |
  null`, `country {code, name}`.
- `CityDetails` — `City` + `currency`, `default_locale`, `phone_prefix`, `week_starts_on`,
  `min_signup_age`, `age_of_majority`, `districts [{id, name}]` (без архивных).
- `GeoPlace` — `geoname_id`, `name`, `region_name | null`, `country {code, name}`, `distance_m`,
  `city: City | null` (место заведено и видимо).

Видимость: город виден публично, только если его страна `is_enabled`; иначе 404 и нет в списках.

Страна места (`GeoPlace.country`): если место заведено городом — страна города (в том числе
переопределённая админом, решение 3: Севастополь — по нашему справочнику, а не по GeoNames); иначе —
страна источника. `here` отдаёт место, только если эта страна есть в `countries` и `is_enabled`;
иначе `here = null`. Импорт разрешён для страны, которая есть в `countries` (в том числе
выключенной); названия страны — из `country_names`.

### 4.2. Операции

| Операция | Что делает |
| --- | --- |
| `GET /v1/cities?status=&country=&q=&cursor=&limit=` (`listCities`) | Порядок: ранг статуса (`live`, `pilot`, `waitlist`), `population` по убыванию, `id`. `q` — префикс названия на любом поддерживаемом языке после `normalize_text` (1–100 символов; пусто после нормализации — 400 `validation.failed`). Курсор keyset по `(ранг, population, id)` — расширение `platform/page` (§4.5); лимит 20, максимум 100. Город, сменивший статус между страницами, может встретиться дважды или ни разу — для справочника допустимо, описано в контракте. |
| `GET /v1/cities/{cityId}` (`getCity`) | `CityDetails`; нет или страна выключена — 404 `geo.city_not_found`. |
| `GET /v1/cities/by-slug/{slug}` (`getCityBySlug`) | То же по текущему `slug` или по `city_slug_history`; ответ всегда с текущим `slug`. |
| `GET /v1/cities/nearest?lat=&lon=` (`findNearestCity`) | `{ nearest_open: {city: City, distance_m} \| null, here: GeoPlace \| null }` (§4.3). `lat` ∈ [-90, 90], `lon` ∈ [-180, 180]; `NaN`/`Inf` — 400 `validation.failed` (явная проверка, не только схема). |
| `GET /v1/app/min-version?platform=ios\|android` (`getAppMinVersion`, тег `platform`) | `{platform, min_version, recommended_version, store_url}`; незаведённая платформа — 200 с `null`-полями; неизвестное значение — 400 `validation.failed`: enum параметра помечен `x-extensible-enum: true` (страж конвенций требует его у каждого enum), но валидатор контракта незнакомое значение отвергает — это описано в контракте. |

Маршруты `/v1/cities/nearest` и `/v1/cities/by-slug/{slug}` не должны перехватываться шаблоном
`/v1/cities/{cityId}` — тест маршрутизации.

Язык (§3.3–3.4): локаль из `Accept-Language` (RFC 9110, q-веса) — первая поддерживаемая; есть —
`Content-Language: <L>`, у каждой записи название на `L`, нет перевода — каноническое `name`. Нет
поддерживаемой — названия на языке страны каждой записи, `Content-Language` не ставится. Всегда
`Vary: Accept-Language`. Разбор заголовка — `platform/i18n` (общий помощник).

Кэш: справочники — `Cache-Control: public, max-age=300`; `nearest` — `private, no-store`;
`min-version` — `public, max-age=60`.

Rate limit: справочники и `min-version` — класс `default` (подсказка города при вводе с одного IP
оператора не должна упираться в 429, спека бэкенда §6.6); `nearest` — класс `geo_nearest`,
`ip=30/1m:10` (правило — в `ratelimit.DefaultRules` до появления класса в контракте).

Приватность: координаты не сохраняются и не пишутся в логи приложения (лог доступа — путь без
query). Query видят Cloudflare и Render — спека клиентов требует округлять координаты до 0,01°
(~1 км, для города достаточно).

### 4.3. Автоопределение

- `nearest_open` — ближайший видимый город со статусом `pilot`/`live` по `centroid`, без ограничения
  расстояния (GiST, `ORDER BY centroid <-> point LIMIT 1`, расстояние — `ST_Distance` по geography).
- `here` — место GeoNames, «накрывающее» точку: кандидаты в 30 км; место накрывает точку, если
  расстояние ≤ `r(pop) = clamp(2 км + 20 м · √population, 2 км, 30 км)`; из накрывающих — ближайшее;
  нет накрывающих — `null`. Константы — в коде модуля, тест на них. Пример: Москва (√12 млн ≈ 3,5 тыс.)
  накрывает 30 км; село на 500 жителей — 2,4 км.
- Антимеридиан: geography считает расстояние по сфере — тест на точках Чукотки (`lon < 0`).

### 4.4. Удаление `/v1/health`

- **Public** — удаляется на этапе 1. `scripts/contracts-breaking.sh` получает файл исключений
  `contracts/oasdiff-err-ignore-public.txt` (формат как у admin) и страж: если существует тег
  `contracts-v1.0.0` или новее, файл обязан быть пуст — иначе CI падает. Правила
  `.claude/rules/contracts.md` и комментарий скрипта обновляются.
- **Admin** — удаляется на этапе 2, вместе с первой админской операцией `geo` (на ней держатся
  страж «тег = модуль» и bundle-тест admin), через существующий `oasdiff-err-ignore-admin.txt`.
- **Фронты:** `HealthStatus` в `apps/web` (`src/app/page.tsx`, `features/health/`) заменяется
  проверкой связи через `listCities` (этап 1); в `apps/admin` (`src/App.tsx`, `features/health/`) —
  на этапе 2 через `GET /v1/geo/places` или другую первую операцию. TDD, тесты фронтов и smoke MSW.

### 4.5. Расширение `platform/page`

До этой спеки `platform/page` кодировал только `(created_at, id)` (wire v1), а свой курсор модулям писать
запрещено (`.claude/rules/backend-platform.md`). Расширение: keyset-курсор с произвольным набором
полей сортировки (`page.Keyset{Set, Values}`: `Set` — имя списка с полями сортировки по порядку, например
`geo.cities(rank,population,id)`, `Values` — значения `int64 | string | time | uuid` по `Kind`ам), wire
v2 с версией и отпечатком набора полей (хэш `Set` — имён полей — и их типов, не HMAC: курсор не секрет, отпечаток
отсекает курсор чужого списка); v1 продолжает декодироваться. Тесты платформы — порча,
чужой набор полей, граничные значения.

## 5. Админка и импорт

Контракт `contracts/openapi/admin/geo/`, тег `geo` (версии — `platform`).

### 5.1. Доступ

Каждая операция требует сессию сотрудника и право из матрицы (спека сотрудников
`docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md` §2.5, каталог — `platform/perm`). Шов
сессий реализован модулем `staff` (план A спеки сотрудников): `auth.StaffSessions.LoadStaff(ctx, token,
ip, background) (*auth.Staff, error)` загружает сотрудника по токену cookie `__Host-wf_admin_session`.
Конвейер admin-api (аллоулист IP, сессия, CSRF — `Origin` и `X-WF-Admin`, валидация, `x-permission`,
лимит, идемпотентность; спека сотрудников §5.1) проверяет право операции по `x-permission`, а `scoped` —
по `cityId` в пути; список без `cityId` (`GET /v1/geo/cities`) модератору фильтрует ручка по его городам
(`auth.Staff.CityIDs`). Право, зависящее от тела (поля `PATCH …/cities/{cityId}`), проверяет сценарий
через `auth.StaffFrom(ctx).Decide(право, cityID)` — без импорта `staff`. Отказ — `403 auth.forbidden`;
без сессии — `401 auth.unauthenticated`. В тестах — загрузчик, отдающий заданный `auth.Staff`. Мутирующие
операции — с `Idempotency-Key`; аудит (`platform/audit`: `actor_staff_id`, `actor_label`, `scope_city_id`
— город операции) и событие (§6) — в транзакции с данными.

### 5.2. Операции и матрица прав

Право — ключ матрицы `platform/perm` (`x-permission` операции; значения по ролям — спека сотрудников
§2.5, `docs/05-ролевая-модель.md`); `handler` — право решает сценарий по присланным полям.

| Операция | Право | Что делает |
| --- | --- | --- |
| `GET /v1/geo/places?q=&country=&cursor=` (`listGeoPlaces`) | `geo.places.search` | Поиск по источнику (префикс на любом языке и по `ascii_name`); порядок — `population` по убыванию, `geoname_id`; у места — `city_id`, если заведено. |
| `GET /v1/geo/cities?status=&country=&region_id=&q=&ids=&cursor=` (`listGeoCities`), `GET /v1/geo/cities/{cityId}` (`getGeoCity`) | `geo.cities.read` (модератор — `scoped`: только свои города) | Все статусы и страны (в том числе выключенные), с `geoname_id`, `population`, названиями по всем локалям. |
| `GET /v1/geo/cities/{cityId}/districts` (`listGeoDistricts`) | `geo.cities.read` | Районы, включая архивные (`archived_at`). |
| `GET /v1/geo/countries` (`listGeoCountries`) | `geo.countries.read` (`allow` у всех ролей, R9) | Все страны с правилами, названиями и числом открытых (`pilot`, `live`) городов. |
| `GET /v1/geo/countries/{code}/regions` (`listGeoRegions`) | `geo.cities.read` (R9) | Регионы страны — выбор региона при активации; нет страны — 404 `geo.country_not_found`. |
| `POST /v1/geo/cities` `{geoname_id, status?, country_code?, region_id?}` (`activateGeoCity`) | `geo.cities.activate` | Активация (§5.3). |
| `PATCH /v1/geo/cities/{cityId}` `{status?, slug?, names?}` (`updateGeoCity`) | `handler`: `geo.cities.manage` (статус, `slug`), `geo.cities.rename` (названия) | Сценарий зовёт `Decide(право, cityID)` по каждому присланному полю; хоть одно `deny` — `403 auth.forbidden` на весь запрос, без частичного применения (R10). Статус — любой переход; `slug` занят в `cities` или в истории другого города — 409 `geo.slug_taken`; старый — в `city_slug_history`; возврат к своему старому `slug` удаляет его из истории; названия по локалям (§3.3). Правки — без оптимистичной версии: последняя запись выигрывает (R11). |
| `POST /v1/geo/cities/{cityId}/link {geoname_id}` (`linkGeoCity`) | `geo.cities.manage` | Ручная привязка города без `geoname_id` к кандидату из отчёта сверки — та же функция, что сверка импорта (§5.4 шаг 5, R14). Место должно быть в источнике, не пропавшим (`missing_since`) и **той же страны, что город** (страна города, в том числе переопределённая), — иначе 404 `geo.place_not_found`: иначе региону ушёл бы код admin1 чужой страны. Город уже привязан — 409 `geo.city_already_linked`; место у другого города — 409 `geo.city_already_active`. |
| `POST /v1/geo/cities/{cityId}/districts`, `PATCH …/districts/{districtId}` (`name?`, `archived?`) | `geo.districts.manage` | Районы; архивирование вместо удаления (§3.2); район чужого города — 404 `geo.district_not_found`. |
| `POST /v1/geo/countries` (`createGeoCountry`) | `geo.countries.rules` | **Всегда `202 {approval}`** — действие `geo.country_create` (вдвоём, U7, R25): второй проверяет валюту и пороги; код уже в справочнике — 409 `geo.country_exists` до запроса, гонка двух запросов — второй `failed` при исполнении. |
| `PATCH /v1/geo/countries/{code}` (`updateGeoCountry`) | `handler`: `geo.countries.rules`, `geo.countries.manage` | Смена валюты, возрастных порогов и **выключение** страны (сравнение с текущими значениями под замком строки, а не присутствие поля) — всегда `202 {approval}` действия `geo.country_rules` с patch целиком, страна до исполнения не меняется; при исполнении сверяется `version` страны (изменилась — `failed`, `platform.approval_stale`). Включение и прочие поля — сразу, `200`, по `geo.countries.manage`. `default_locale` задаётся только при создании (R12). Правила — вносит человек, сверяя с законом; из GeoNames не берутся. |
| `POST /v1/geo/imports` `{country}` (`startGeoImport`), `GET /v1/geo/imports?country=&job_id=&cursor=` (`listGeoImports`), `GET /v1/geo/imports/{importId}` (`getGeoImport`) | `geo.imports.run` / `geo.imports.read` | Поставить импорт (§5.4): `202 {job_id}`; уже в очереди или идёт — 409 `geo.import_in_progress` с `job_id` существующей задачи (R26; у импорта оператора — без `job_id`); страны нет — 404 `geo.country_not_found`. Журнал с подписью «кто запустил» (`started_by_label`, §5.4) и отчёт сверки одного импорта (`ambiguous`, `conflict`). |
| `GET /v1/geo/imports/jobs/{jobId}` (`getGeoImportJob`) | `geo.imports.read` | Состояние задачи импорта по `JobGet` клиента River (R7, без SQL по `river_job`): `queued` (ждущие состояния River), `running`, `completed`, `failed` (в том числе `discarded`), `cancelled`; незнакомое новое состояние River — `queued`; с последней строкой журнала задачи. Нет задачи или она другого вида — 404 `geo.import_job_not_found`. |
| `POST /v1/geo/imports/jobs/{jobId}/cancel` (`cancelGeoImportJob`) | `geo.imports.run` | Отмена задачи «в очереди» (R22, §5.4); выполняется, выполнена или выброшена — 409 `geo.import_job_not_cancellable`; уже отменена — 200 без второй записи журнала. |
| `GET /v1/app/versions` (`listAppVersions`), `PUT /v1/app/versions/{platform}` (`putAppVersions`; тег `platform`) | `platform.versions.read` / `platform.versions.manage` | Версии и `store_url`; нарушение `min ≤ recommended` или хоста — 400 `validation.failed` с полем. |

Отказ по праву — `403 auth.forbidden` (спека сотрудников §2.4, §6.4): кодов `geo.forbidden` и
`platform.forbidden` нет. Тест прав на каждую операцию — по субъектам спеки сотрудников §11 (роли,
модератор своего и чужого города, без сессии).

### 5.3. Активация города

По `geoname_id` (место должно быть в источнике и не пропавшим — без `missing_since`, иначе 404
`geo.place_not_found`; R13). Место (здесь и в `linkGeoCity`) читается без замка строки: перенос места импортом другой
страны или пометка пропавшим (`ImportDeleteGone`) до сверки — принятое окно (город привязан к месту, как при пропаже
места после привязки); `FOR SHARE` не ставить — цикл замков со сверкой (ревью G6 m2):

1. Страна: `country_code` из запроса (переопределение, решение 3) или страна места; должна быть
   включена — иначе 422 `geo.country_not_enabled`.
2. Регион: `region_id` из запроса (должен принадлежать стране, иначе 422 `geo.region_not_found`) или
   регион из `geonames_admin1` по `(страна места, admin1_code)` — существующий с этим
   `geoname_admin1_code` или новый с названиями по §3.4 (гонка двух активаций одного нового региона —
   `ON CONFLICT DO NOTHING` и перечитывание). При переопределённой стране без `region_id`, а также при
   admin1 `00` («неизвестно» в GeoNames) или пустом — регион не ставится (`NULL`, R13).
   Регион без `geoname_admin1_code` (заведённый до импорта) по названию не ищется: активация заведёт
   второй одноимённый регион с кодом, а сверка импорта код первому уже не даст — два региона с одним
   названием (ревью G5 m4; слияние регионов этой спекой не предусмотрено — открытый вопрос).
3. `name` и строки `city_names` — по §3.4 для языка страны и `en`; `centroid`, `timezone`
   (проверка `time.LoadLocation` со встроенной `time/tzdata`), `population` — из источника.
4. `slug` (R13) — `ascii_name` в нижнем регистре, не `[a-z0-9]` → `-`, схлопнуть дефисы; пусто после
   этого — `city-<geoname_id>`; кандидаты — база → `-<ascii_name региона>` → `-2`, `-3`…, под суффикс
   обрезается **база** (кандидат ≤ 100 символов, без `--` и хвостового `-`); занятые (`cities` или
   `city_slug_history`) пропускаются; вставка — под savepoint: гонка с уникальностью slug — следующий
   кандидат, всего до 20 попыток.
5. Статус по умолчанию `waitlist`. Нарушение `cities.geoname_id UNIQUE` (в том числе гонка двух
   активаций) — 409 `geo.city_already_active`.
6. Аудит `geo.city_activated` (город записи — новый город) и событие — в той же транзакции; ответ —
   карточка города, как `getGeoCity`.

### 5.4. Импорт GeoNames

Логика — `internal/geo/internal/app` (`Import(ctx, country, startedBy, riverJobID)`), вызывается из двух мест:

- **Задача River** `geo.import` (очередь `maintenance`): ставит её `POST /v1/geo/imports`
  (`startGeoImport`). В аргументах (`ImportArgs`, `v = 1`) — **только** страна и `started_by` (id
  сотрудника), без имени и роли (ответ пользователя Q1, R3): `river_job` читает роль `api`, ПД
  сотрудников видит только admin-api (страж `TestImportArgsNoPII`). Постановка — одна транзакция под
  `Idempotency-Key`: `InsertTx` задачи и аудит `geo.import_requested` **от сотрудника** (объект
  `geonames_import_job`, `object_id = id.Named("geonames_import_job", <job_id>)`, `after {country_code,
  job_id}`, без города). Ответ — `202 {job_id}`: строку журнала создаёт сама задача (шаг 1), до неё
  экран следит за задачей через `getGeoImportJob` (R7).
  `Timeout() = 30 мин`, `MaxAttempts = 3`, уникальность по стране в состояниях `available`,
  `pending`, `scheduled`, `running`, `retryable` (без `completed` — повторный импорт той же страны
  должен ставиться). Работает там, где запущен воркер (на стенде воркера нет — §11).
  - **409 `geo.import_in_progress`** при постановке — дубль уникальности River (`job_id` существующей
    задачи, R26) или **свежая** строка `running` страны (`started_at` не старше `StaleAfter` = 40 мин;
    `job_id` — её `river_job_id`, у оператора — без `job_id`). Дубль River проверяется раньше строки
    `running`: ждущая задача видна сотруднику и при импорте оператора. Зависшая строка `running`
    постановке не мешает: её закроет `failed` (аудит `system`) шаг 1 поставленной задачи; если по часам
    воркера строка ещё не зависшая — задача ждёт (ниже).
  - Импорт страны уже идёт (у оператора) — задача уходит в `river.JobSnooze(1 мин)`: попытка не
    тратится, в `discarded` не уходит; задача старше 2 ч от постановки не откладывается, а
    отменяется (страховка от вечного ожидания, R8) — уникальность страны освобождается.
  - Аудит итога импорта задачи — **от `system`** (`ActorLabel = "system"`, `after.started_by` — id
    сотрудника); «кто запустил» в журнале импортов (`started_by_label` у `listGeoImports`) берётся из
    записи постановки `geo.import_requested` запросом журнала по объектам всей страницы
    (`audit.ListParams.ObjectIDs`): новейшая запись задачи с `created_at ≤ started_at` строки + 1 мин
    допуска часов (защита от повтора `river_job.id` после сброса последовательности); у оператора и без
    записи — подписи нет. Экрану не нужно право `audit.log.read`. Схема `geonames_imports` не меняется (`started_by` — uuid без FK).
  - **Отмена** (`cancelGeoImportJob`, R22) — только задачи «в очереди»: без воркера (стенд)
    поставленная задача держала бы уникальность страны вечно. Одна транзакция: advisory-замок задачи
    (отмены одной задачи идут по очереди), строка River «до» (`JobGetTx`; нет или другой вид — 404
    `geo.import_job_not_found`), `JobCancelTx`; итог не `cancelled` (уже `running` — River 0.x на
    `running` не ошибается, а ставит метку отмены; `completed`, `discarded`) — откат вместе с меткой и
    409 `geo.import_job_not_cancellable`; отменил этот вызов — аудит `geo.import_cancelled` от
    сотрудника; «до» уже `cancelled` (повтор другим ключом, второй сотрудник) — 200 без второй записи.
    Бегущий импорт не отменяется. Остаточное окно (принято, ревью G7 m1): задачу `available` между
    `JobGetTx` и `JobCancelTx` может взять воркер и сам же отменить (страны нет, ожидание дольше 2 ч) —
    тогда журнал запишет отмену от сотрудника; последствие — только подпись в журнале (строкой
    `river_job` под `FOR UPDATE` не чинится: модуль не пишет SQL по таблицам River, R7).
- **Команда оператора** `worker geo import --country RU` — выполняет импорт **синхронно в своём
  процессе** (не ставит задачу), под ролью `worker` (`WORKER_DATABASE_URL`); аудит —
  `ActorRole = "operator"`, `ActorLabel = "operator"`, `started_by = NULL`. Время журнала оператора —
  по часам базы (`OperatorClock`: сдвиг `now()` базы против часов машины оператора, замер один раз), а
  не ноутбука: отставшие часы делали живой импорт оператора «зависшим» для admin-api (ревью G7 m9).

Шаги `Import`:

1. Журнал: у строки `geonames_imports` есть `river_job_id bigint` (NULL у команды оператора).
   Строка `running` страны с тем же `river_job_id` — прерванная попытка этой же задачи: она
   продолжается (шаги заново в той же строке), а не даёт 409. Строка `running` старше `Timeout` +
   10 минут (40 минут) → `failed` («прерван»). Иначе вставить свою строку `running`; уникальный
   индекс — параллельный импорт получит 409 `geo.import_in_progress`. При отмене ctx (штатная
   остановка воркера, таймаут) строка переводится в `failed` через `context.WithoutCancel`.
   Весь `Import` идёт под сроком `Timeout` (30 мин) и у команды оператора: иначе живой импорт пережил бы
   40 минут и был бы закрыт как зависший; закрытие зависшей строки — в аудите от закрывшего запуска.
2. Скачать `<base>/export/dump/<CC>.zip`, `<base>/export/dump/alternatenames/<CC>.zip`,
   `<base>/export/dump/admin1CodesASCII.txt` (`base` — `WORKER_GEONAMES_BASE_URL`, по умолчанию
   `https://download.geonames.org`). Только HTTPS, редиректы — только на тот же хост; лимиты
   сжатого и распакованного размера (конфиг; по умолчанию 50 МБ / 200 МБ на файл); из zip читается
   ровно одна ожидаемая запись (`<CC>.txt`) потоком, на диск не распаковывается; допускается и не
   читается `readme.txt` (она есть в настоящих архивах), любая другая запись — отказ; sha256 и
   размеры — в журнал, при ошибке — уже скачанных файлов. `country` — `^[A-Z]{2}$`.
3. Разбор (формат проверен 02.10.2026: основной — 19 колонок; альтернативные названия V2 — 10
   колонок с `from`/`to` и флагами `isPreferredName`, `isShortName`, `isColloquial`, `isHistoric`;
   `admin1CodesASCII` — `code, name, name ascii, geonameid`, названия английские). Отбор: класс `P`,
   `feature_code` не из `PPLX` (часть населённого пункта), `PPLQ`, `PPLH`, `PPLW`, `PPLCH`; население
   ≥ 500 **или** административный центр (`PPLA`…`PPLA4`, `PPLC`). Пустая или неизвестная таймзона —
   строка пропускается, счёт — в отчёт. Названия `ru`/`en` — по §3.4, плюс названия регионов по
   `geonameid` из `admin1CodesASCII`.
4. Только после **полностью успешного** разбора: upsert пачками по 1000. Строки, пропавшие из
   источника: если новых строк меньше 90 % от прошлого успешного импорта — импорт падает (защита от
   обрезанного файла); иначе удаляются, а те, на которые ссылается город, получают `missing_since`
   и попадают в отчёт (`reconciled.missing`, `geoname_id`). До записи проверяется и admin1 (решение
   пользователя 05.10.2026): каждый непустой код admin1 отобранных мест, кроме `00` («неизвестно» в
   GeoNames), есть в наборе admin1 страны — иначе импорт падает со списком кодов (обрезанный общий
   файл `admin1CodesASCII.txt`); строк admin1 страны не меньше 90 % от текущих в `geonames_admin1`.
5. Сверка заведённых городов страны без `geoname_id`: совпадение `name_normalized` с ru- или
   основным названием места **и** региона (по `geoname_admin1_code`, иначе по `region_names` после
   `normalize_text`) → `geoname_id`, переводы (только отсутствующие, §3.3), региону —
   `geoname_admin1_code`. `centroid`/`population` у заведённого города не меняются; `version`
   города растёт, событие `geo.city_linked`. Ноль или несколько совпадений — в `reconciled`, город
   не трогается. Город без региона кандидатов не получает (`not_found`): условие «и региона» не
   выполнить. Каждая привязка — под savepoint: место за это время привязал другой город (активация
   админом, 23505 `cities_geoname_id_key`) или сам город за это время привязан (`ImportLinkCity` не
   нашёл строку с `geoname_id IS NULL`) — тоже откат savepoint, `reconciled.conflict`; откат — только
   этой привязки, импорт идёт дальше (решение пользователя 05.10.2026). Активация (этап 2) на
   тот же 23505 отвечает 409 `geo.city_already_active` (§5.3).
6. Итог в журнал (`succeeded`/`failed` с текстом ошибки), событие `geo.import_finished`.

Атрибуция CC BY 4.0: `NOTICE` в корне репозитория («GeoNames, https://www.geonames.org, CC BY 4.0,
https://creativecommons.org/licenses/by/4.0/, с изменениями: отбор, нормализация названий»); клиенты
показывают «Данные о городах: GeoNames (CC BY 4.0)» на экране «О приложении» — требование к спекам
клиентов.

## 6. События

Агрегаты получают `version` (инкремент на каждое изменение, в той же транзакции); payload — только
идентификаторы и непрофильные значения, `v = 1` (шаблон платформы §6.5). Каталог:

| Событие | Агрегат (тип, id, версия) | Payload |
| --- | --- | --- |
| `geo.city_activated` | `city`, `cities.id`, `cities.version` | `city_id`, `country_code`, `region_id`, `status`, `geoname_id` |
| `geo.city_status_changed` | `city` | `city_id`, `from`, `to` |
| `geo.city_renamed` | `city` | `city_id`, `locales` (изменённые) |
| `geo.city_slug_changed` | `city` | `city_id`, `old_slug`, `new_slug` |
| `geo.city_linked` | `city` | `city_id`, `geoname_id` (сверка импорта) |
| `geo.district_created`, `geo.district_updated`, `geo.district_archived` | `district`, `districts.id`, `districts.version` | `district_id`, `city_id` |
| `geo.country_created` (план B, R15) | `country`, `countries.id`, 1 | `country_code` |
| `geo.country_updated` | `country`, `countries.id`, `countries.version` | `country_code`, `changed` (имена полей) |
| `geo.import_finished` | `geonames_import`, `geonames_imports.id`, 1 | `import_id`, `country_code`, `status` |
| `platform.app_versions_changed` | `app_versions`, `id.Named("app_versions", <платформа>)`, `app_versions.version` | `platform` |
| `platform.flag_changed` (спека сотрудников §6.1) | `feature_flag`, `id.Named("feature_flag", <ключ>)`, `feature_flags.version` | `key`, `enabled_globally`, `enabled_city_ids` |

Одна правка — одно событие: архивирование района вместе с переименованием в одном `PATCH` даёт только
`geo.district_archived` (переименование подписчику не видно — новое имя он читает из справочника; ревью
G6 m6). События одной транзакции получают последовательные версии агрегата (R11).

Подписчиков в этой спеке нет. На стенде без воркера outbox растёт (relay и чистка — в воркере):
объём — единицы событий, допустимо.

## 7. API модуля для других модулей

Корневой пакет `internal/geo` (`geo.go`, `events.go`):

- `Service.CitySettings(ctx, cityID) (CitySettings, error)` — для `identity`: `timezone`, `status`,
  `country_code`, `country_enabled`, `currency`, `default_locale`, `min_signup_age`,
  `age_of_majority`; `ErrCityNotFound`.
- Массовое чтение — `geo_read_city_settings`.
- Типы и имена событий §6 — в `events.go`.

## 8. Ошибки

Коды операций (`x-error-codes`); тексты — `errors.<код>` в `apps/web/messages/{ru,en}.json` (публичные)
и `apps/admin/messages/{ru,en}.json` (админские) по правилу `.claude/rules/contracts.md`:

Список модуля — `backend/internal/geo/codes.go`, делится на `geo.PublicCodes` (сверка с контрактом
public) и `geo.AdminCodes` (сверка с admin и `apps/admin/messages`; спека сотрудников §6.3 п. 7). Коды
плана B — его Приложение 3 (`docs/superpowers/plans/2026-10-27-staff-b-geo2.md`), R4.

| Код | HTTP | Где |
| --- | --- | --- |
| `geo.city_not_found` | 404 | `getCity`, `getCityBySlug`, админские по `cityId` |
| `geo.district_not_found` | 404 | `updateGeoDistrict` (в том числе район чужого города) |
| `geo.place_not_found` | 404 | `activateGeoCity`, `linkGeoCity` (нет места, пропало из источника, у привязки — другой страны, чем город) |
| `geo.country_not_found` | 404 | `updateGeoCountry`, `listGeoRegions`, `startGeoImport` |
| `geo.import_not_found` | 404 | `getGeoImport` |
| `geo.import_job_not_found` | 404 | `getGeoImportJob`, `cancelGeoImportJob` (нет задачи или задача другого вида) |
| `geo.city_already_active` | 409 | `activateGeoCity`, `linkGeoCity` (место у другого города) |
| `geo.city_already_linked` | 409 | `linkGeoCity` |
| `geo.slug_taken` | 409 | `updateGeoCity` (смена `slug`) |
| `geo.country_exists` | 409 | `createGeoCountry` |
| `geo.import_in_progress` | 409 | `startGeoImport` (с `job_id` существующей задачи, R26) |
| `geo.import_job_not_cancellable` | 409 | `cancelGeoImportJob` (задача выполняется, выполнена или выброшена) |
| `geo.district_name_taken` | 409 | создание, переименование, разархивирование района |
| `geo.country_not_enabled` | 422 | `activateGeoCity` |
| `geo.region_not_found` | 422 | `activateGeoCity` (`region_id` не из страны города) |
| `platform.flag_not_found` | 404 | `updateFeatureFlag` |
| `platform.flag_stale` | 409 | `updateFeatureFlag` (`version` не та) |
| `platform.flag_city_not_found` | 422 | `updateFeatureFlag` (города флага нет) |

Кодов `geo.forbidden` и `platform.forbidden` нет: отказ по праву — `403 auth.forbidden` (§5.2).
Исполнители подтверждений geo (`geo.country_create`, `geo.country_rules`) наружу отдают только
`platform.approval_*`: нарушение данных запроса при исполнении — `failed` и `409 platform.approval_stale`.

Общие: `validation.failed` (400: параметры, `NaN`, `q` из одних знаков, `min ≤ recommended`, хост
`store_url`, правила страны), `auth.unauthenticated`, `auth.forbidden`, `ratelimit.exceeded`, коды
идемпотентности.

## 9. Структура кода

По спеке бэкенда §3:

```
backend/internal/geo/
  geo.go, events.go, codes.go, read.go, locale.go   API модуля; чтения справочника и выбор языка (geo.New)
  internal/app/      сценарии записи: Import (этап 1); активация, правки, страны (этап 2)
  internal/source/   загрузка и разбор GeoNames (§5.4 шаги 2–3)
  internal/store/    sqlc geodb + репозиторий
  internal/domain/   slug, выбор названия §3.4, r(pop) §4.3 — чистые правила
  httpapi/  admin/  jobs/  queries/
backend/internal/platform/appversion/   таблица, semver, правила; ручки — internal/httpapi/{public,admin}
```

Отступление от раскладки §3 спеки бэкенда: чтения справочника — в корневом пакете `geo`, а не в
`internal/app`. Импорт (`internal/app`) публикует события из `geo/events.go` и потому импортирует корневой
`geo`; реализация чтений в `internal/app` дала бы цикл импортов.

Регистрация: строка `geo` в `Layers` (`internal/archtest/modules.go`), элемент `sql` в
`backend/sqlc.yaml` (`omit_unused_structs: true`), владение в `ownership.go`, классы лимитов в
`DefaultRules`, команда `geo import` в `cmd/worker`, задача — в списке воркера, конфиг
`WORKER_GEONAMES_*` в `config`, `.env.example`, `.env` (скриптом).

## 10. Тесты (TDD, стражи с подсадкой бага)

- Миграция `0019` вверх-вниз на чистой базе: сид получил точки, названия, `geoname_id`; `down`
  возвращает `city_settings` и уникальность; отказ при городе без точки (подставной город в тесте).
- Хранилище и `nearest` на PostGIS (`dbtest`): `nearest_open` только `pilot`/`live` видимых стран;
  `here` — `r(pop)`, граница 30 км, «Москва против района» (PPLX исключён на импорте), Чукотка.
- Список: порядок, фильтры, `q` ru/en с нормализацией (`ё`, регистр, латиница-двойники), курсор v2,
  видимость по `is_enabled`.
- Язык: q-веса, неподдерживаемый, нет перевода, смешанные страны — `Content-Language`, `Vary`.
- Выбор названия §3.4 — табличный тест на реальных строках Ставрополя и Михайловска.
- Импорт: выдержки GeoNames в `testdata` (с атрибуцией), `httptest.NewTLSServer`; лимиты сжатого и
  распакованного, лишняя запись в zip, редирект на другой хост, обрезанный файл (< 90 %),
  `missing_since`, зависший `running`, повтор, сверка (одно / несколько / ни одного совпадения,
  тёзки в одном регионе), правки админа не затираются.
- Активация: переопределение страны и региона, тёзки → разные `slug`, гонка двух активаций → 409.
- Контракт (`apitest`): коды, кэш-заголовки, анонимность, `x-rate-limit`, маршрутизация
  `nearest`/`by-slug` против `{cityId}`, `NaN`.
- Админка: права на каждую операцию по субъектам спеки сотрудников §11 (роли, модератор своего и чужого
  города, без сессии — 401, стадии `mfa_pending`/`setup`, `Reauth`), идемпотентность, аудит и событие в
  одной транзакции, `version` растёт.
- `appversion`: semver, `min ≤ recommended`, хосты стора, пустая платформа → 200 с `null`.
- `page` v2, `contracts-breaking.sh` (файл исключений public пуст после тега — страж), фронты
  (`listCities` вместо health), `archtest`, `grants_test`, локали фронтов.

## 11. Порядок в плане и стенд

1. **Этап 1 — стенд для нативщика:** `page` v2, `0019`, импорт (логика, команда оператора, задача),
   публичные ручки `geo`, `min-version`, удаление `/v1/health` из public с исключением, фронт web,
   документы (§12). На стенде после деплоя список, город и `nearest_open` работают
   сразу (сид с точками). `here` требует импорта: оператор запускает `worker geo import --country RU`
   со своей машины против Neon под ролью `worker` — роли на Neon пока нет: `deploy-dev.md` получает
   шаги создания роли `worker` и повторного `migrate up` (применит `grants.sql`). Админская ручка
   импорта на стенде без воркера не исполнится — задача стоит в очереди (уникальность страны занята);
   отменить её — кнопкой «Отменить» на экране импорта (`cancelGeoImportJob`, §5.4); не отменённую
   исполнит первый запущенный воркер.
2. **Этап 2 — админка (выполнен планом B в ветке `spec/geo2-render`, до мержа; `docs/superpowers/plans/2026-10-27-staff-b-geo2.md`):**
   админские ручки `geo`, версий и флагов, фронт admin — план B спеки сотрудников. Живой e2e импорта и
   активации идёт против HTTPS-фикстуры GeoNames (`backend/cmd/geofixture`, доверие воркера —
   `WORKER_GEONAMES_CA_FILE`, R16), без внешней сети. Шов
   сессий admin-api, вход сотрудников, удаление `/v1/health` из admin и чтение городов
   `GET /v1/geo/cities` (выбор городов модератора) сделаны её планом A (модуль `staff`).

Вход сотрудников с TOTP сделан модулем `staff` (спека сотрудников); `identity` — вход и роли игроков.

## 12. Обновление документов (в коммитах плана)

- `docs/01-домен-и-правила.md:145` — решение 1 (города всех статусов видны, `waitlist` — «станьте
  капитаном»), источник GeoNames с активацией, переопределение страны.
- Спека бэкенда: §4.1–4.2 (`geo_read_city_settings` вместо `city_settings`, новые таблицы,
  `app_versions` у `platform`), §2 и §6.9 (`platform/page` — keyset v2), §4.6 (каталог событий `geo` — ссылка сюда), §8.3 (правило ломающих
  правок public до `contracts-v1.0.0`), §10 (матрица прав `geo` — ссылка сюда).
- `backend/migrations/README.md` — `0019`, решения по уникальности тёзок и архиву районов; `down`
  `0019` не восстановит `cities_country_name_key`, если тёзки уже заведены (ожидаемо, записать).
- `.claude/rules/contracts.md` — файл исключений public и страж тега; `.claude/rules/backend-http.md`
  — `/v1/health` удалён из public, admin — этап 2; `.claude/rules/backend-platform.md` — `page` v2.
- `docs/deploy-dev.md` — роль `worker` на Neon, импорт оператором, `WORKER_GEONAMES_*`.
- `.env.example` (`WORKER_GEONAMES_BASE_URL`, лимиты размеров), `NOTICE`.

## 13. Проверено при ревью (02.10.2026) и что осталось проверить

Проверено скачиванием: `RU.zip` 15,2 МБ (`RU.txt` 61,8 МБ, 412 812 строк), `alternatenames/RU.zip`
12,6 МБ (`RU.txt` 50 МБ, формат V2), `admin1CodesASCII.txt` (английские названия, 4 колонки);
лицензия CC BY 4.0 (`readme.txt`); класс `P` с населением ≥ 500 в RU — 5214 мест; данные §3.6.

После фильтра §5.4 в RU — 5171 место (ревью, 02.10.2026). Проверить первыми шагами плана:
поведение kin-openapi на `NaN`/`Inf` в `number`; порядок маршрутов gorillamux; использует ли нативщик
`/v1/health`.
