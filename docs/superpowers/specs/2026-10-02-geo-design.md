# Спека модуля `geo`

Дата: 02.10.2026. Статус: на ревью.
Родительская спека: `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` (дальше — «спека
бэкенда»), §13 п. 1. Домен: `docs/01-домен-и-правила.md`, раздел «Геоиерархия».

## 1. Зачем и для кого

`geo` — первый продуктовый модуль. От него зависит регистрация (`identity`): город обязателен, порог
возраста и валюта берутся из страны города. Нативному приложению (Flutter) модуль даёт четыре
экрана: выбор города, автоопределение города по геопозиции, настройки города, принудительное
обновление приложения.

Решения пользователя (02.10.2026):

- публичный список и автоопределение отдают города всех статусов, со статусом в ответе: `waitlist`
  клиент показывает как «пока нет игр — станьте капитаном» (экран «Пустой город»);
- источник городов — GeoNames (CC BY) с активацией: импорт в таблицу-источник, админ находит город
  поиском и включает его;
- минимальная версия приложения — в базе, меняется ручкой admin-api с аудитом, без деплоя;
- админские ручки `geo` пишутся сразу и правильно — со входом сотрудника и правом из ролей; до спеки
  `identity` они отвечают 401. Временных обходов (команды «активировать город» и т. п.) нет;
- без спешки и без отступлений от принятых решений; объём не урезается.

Успех: на стенде после первого этапа плана и первого импорта нативщик получает список городов,
город с настройками, ближайший город по координатам и минимальную версию; админские ручки готовы и
оживают вместе со входом сотрудников.

## 2. Границы модуля

Владелец (спека бэкенда §4.1, `internal/archtest/ownership.go`):

| Объект | Владелец |
| --- | --- |
| `countries`, `regions`, `cities`, `districts` (есть, `0002_geo.sql`) | `geo` |
| `country_names`, `region_names`, `city_names` (новые) | `geo` |
| `geonames_places`, `geonames_place_names`, `geonames_imports` (новые) | `geo` |
| представление `geo_read_city_settings` (экспортированное, новое) | `geo` |
| представление `city_settings` (есть) | `geo`, удаляется (§3.6) |
| `app_versions` (новая) | `platform` |

Минимальная версия приложения — механизм платформы, не география: таблица и ручки принадлежат
`platform` (пакет `internal/platform/appversion`), в этой спеке потому, что §13 спеки бэкенда и
план «Фундамент 3/3» отнесли её к первому модулю вместе с удалением `/v1/health`.

Не входит: заявка на капитана в новом городе (`teams`), реакции на смену статуса города
(`teams`, `notify` — их спеки), поля и `nearby_pitches()` (`pitches`), вход сотрудников и имена
ролей (`identity`).

## 3. Модель данных

Новая миграция `0019_geo.sql` только добавляет к `0002`, кроме удаления `city_settings` (§3.6).
sqlc разбирает грамматику PostgreSQL 17 — синтаксис PG18 не использовать.

### 3.1. Источник GeoNames

```sql
geonames_places (
  geoname_id      bigint PRIMARY KEY,           -- id GeoNames
  country_code    char(2)  NOT NULL,            -- ISO 3166-1 alpha-2
  admin1_code     text,                          -- код региона GeoNames
  name            text     NOT NULL,            -- основное название GeoNames
  name_normalized text GENERATED ALWAYS AS (normalize_text(name)) STORED,
  feature_code    text     NOT NULL,            -- PPL, PPLA, PPLC, …
  population      bigint   NOT NULL DEFAULT 0,
  location        geography(Point, 4326) NOT NULL,
  timezone        text     NOT NULL,            -- IANA
  imported_at     timestamptz NOT NULL,
  import_id       uuid NOT NULL REFERENCES geonames_imports(id)
)
geonames_place_names (geoname_id → geonames_places ON DELETE CASCADE, locale text, name text,
                      name_normalized GENERATED, PRIMARY KEY (geoname_id, locale))
geonames_admin1 (country_code, admin1_code, name, PRIMARY KEY (country_code, admin1_code))
geonames_admin1_names (country_code, admin1_code, locale, name, PRIMARY KEY (…, locale))
geonames_imports (
  id uuid PRIMARY KEY, country_code char(2), source_files jsonb,  -- имена, размеры, sha256
  status text CHECK (status IN ('running','succeeded','failed')),
  started_at, finished_at timestamptz, places_upserted int, names_upserted int,
  reconciled jsonb,   -- отчёт сверки (§5.3): сопоставленные и неоднозначные города
  error text, started_by uuid NULL   -- сотрудник; NULL — команда оператора
)
```

Индексы: GiST по `geonames_places.location` (автоопределение), btree `(country_code,
name_normalized text_pattern_ops)` и `geonames_place_names (name_normalized text_pattern_ops)`
(поиск по префиксу в админке). Один импорт страны за раз — частичный уникальный индекс
`geonames_imports (country_code) WHERE status = 'running'`.

Загружаются только населённые пункты (класс `P`) с населением ≥ 500 стран из `countries` с
`is_enabled`; на стенде — Россия.

### 3.2. Справочник

- `cities`: `+ geoname_id bigint UNIQUE` (без внешнего ключа: источник перезагружаем, город — нет),
  `+ population bigint NOT NULL DEFAULT 0`. `centroid` становится `NOT NULL` отдельной миграцией
  `0020_geo_centroid_required.sql` (§5.4). `name` — каноническое название на языке страны
  (`countries.default_locale`): по нему нормализация и уникальность `(country_id, name_normalized)`.
  `slug` — латиница `[a-z0-9-]`, уникален глобально (как сейчас).
- `regions`: `+ geoname_admin1_code text`, уникальность `(country_id, geoname_admin1_code)` где не NULL.
- Переводы: `country_names (country_id, locale, name)`, `region_names (region_id, locale, name)`,
  `city_names (city_id, locale, name, name_normalized GENERATED)` — PK `(id, locale)`,
  `locale` ∈ поддерживаемых (`ru`, `en`; список — `@wf/i18n`/`backend/locales`, CHECK по формату
  `^[a-z]{2}$`). Каноническое `name` дублируется строкой на языке страны — чтобы выдача и поиск шли
  по одной таблице; поддерживает это только приложение `geo` в той же транзакции.
- `districts` — без изменений (вносит админ; переводы районов — не нужны до первого запроса).

### 3.3. Выбор языка

Локаль ответа: `Accept-Language` (RFC 9110, q-веса) → первая поддерживаемая (`ru`, `en`) → иначе
`countries.default_locale` города. Название: `*_names` в выбранной локали → каноническое `name`.
В ответе `Content-Language` и `Vary: Accept-Language`. Разбор заголовка — общий помощник
платформы (`platform/i18n`), не модуля: им же воспользуются другие модули.

### 3.4. Минимальная версия приложения

```sql
app_versions (
  platform            text PRIMARY KEY CHECK (platform IN ('ios','android')),
  min_version         text NOT NULL,   -- semver MAJOR.MINOR.PATCH
  recommended_version text NOT NULL,
  store_url           text NOT NULL,
  updated_at          timestamptz NOT NULL DEFAULT now()
)
```

CHECK формата semver (`^\d+\.\d+\.\d+$`); `min_version ≤ recommended_version` — проверка в коде
(semver не сравнивается строкой). Добавление платформы — миграция CHECK и значение enum в контракте
(enum открытый).

### 3.5. Права ролей (`grants.sql`)

| Роль | `geo`-таблицы, `geonames_*` | `app_versions` |
| --- | --- | --- |
| `api` | SELECT | SELECT |
| `admin` | SELECT, INSERT, UPDATE, DELETE (кроме `geonames_*` — SELECT) | SELECT, INSERT, UPDATE |
| `worker` | SELECT; `geonames_*` — DML; `cities`, `regions`, `*_names` — UPDATE/INSERT для сверки (§5.3) | SELECT |

Каждое отличие от «DML всем» — ветка CASE в `grants.sql` и случай в `grants_test.go`.

### 3.6. Экспортированное представление

`geo_read_city_settings` (спека бэкенда §4.2) — то же, что `city_settings`, плюс `centroid`,
`week_starts_on`, `phone_prefix`. `city_settings` удаляется в `0019` (на неё никто не ссылается —
проверить поиском при реализации; если ссылается — сначала перевести). Удалить или переименовать
колонку `geo_read_city_settings` — ломающее изменение API модуля.

## 4. Публичный API

Контракт `contracts/openapi/public/geo/` (и `public/platform/` для версии), тег `geo` (и `platform`).
Все операции анонимные (`security: []`): нужны до входа. Конвенции — `.claude/rules/contracts.md`:
полное описание и примеры, `x-error-codes`, открытые enum'ы (`x-extensible-enum`), `x-rate-limit`.

### 4.1. Схемы

- `CityStatus` — `waitlist | pilot | live` (открытый enum).
- `City` — `id`, `slug`, `name`, `status`, `timezone`, `location {lat, lon}`, `region {id, name} | null`,
  `country {code, name}`.
- `CityDetails` — `City` + `currency`, `default_locale`, `phone_prefix`, `week_starts_on`,
  `min_signup_age`, `age_of_majority`, `districts [{id, name}]`.
- `GeoPlace` — `geoname_id`, `name`, `region_name | null`, `country {code, name}`, `distance_m`,
  `city: City | null` (если место заведено как город).

### 4.2. Операции

| Операция | Что делает |
| --- | --- |
| `GET /v1/cities?status=&country=&q=&cursor=&limit=` (`listCities`) | Список: сортировка `live`, `pilot`, `waitlist`, затем население по убыванию, затем `id`. `q` — префикс названия на любом языке после `normalize_text` (≥ 1 символ, ≤ 100). Курсор — `platform/page` (лимит 20 / максимум 100). |
| `GET /v1/cities/{cityId}` (`getCity`) | `CityDetails`; нет — 404 `geo.city_not_found`. |
| `GET /v1/cities/by-slug/{slug}` (`getCityBySlug`) | То же по `slug` (ссылки веба). |
| `GET /v1/cities/nearest?lat=&lon=` (`findNearestCity`) | `{ nearest_open: {city: City, distance_m} \| null, here: GeoPlace \| null }`. `nearest_open` — ближайший город со статусом `pilot`/`live` без ограничения расстояния; `here` — ближайшее место GeoNames в радиусе 30 км (константа модуля). `lat` ∈ [-90, 90], `lon` ∈ [-180, 180]. Координаты не сохраняются и не логируются (лог доступа пишет путь без query). |
| `GET /v1/app/min-version?platform=` (`getAppMinVersion`, тег `platform`) | `{platform, min_version, recommended_version, store_url}`; для незаведённой платформы — 200 с `null`-полями (клиент ничего не блокирует). |

Кэш: справочники — `Cache-Control: public, max-age=300`; `nearest` — `private, no-store`;
`min-version` — `public, max-age=60`.

Rate limit: класс `geo` (все, кроме `nearest`) — `ip=120/1m:30`; `geo_nearest` — `ip=30/1m:10`.
Правила — в `ratelimit.DefaultRules` до появления класса в контракте (иначе API не стартует).

### 4.3. Удаление `/v1/health`

`/v1/health` уходит из обоих контрактов (план «Фундамент 1/3», остатки). На операциях `geo`
держатся: страж «тег = модуль» (`apitest.TagViolations`), bundle-тест, smoke типизированных
клиентов web/admin (MSW). Удаление — не ломающее для клиентов стенда: нативщик ручку не использует
(проверить у него при реализации); `oasdiff` сочтёт удаление ломающим — исключение фиксируется в
PR как согласованное (ручка инфраструктурная, заменена `/healthz`).

## 5. Админка и импорт

Контракт `contracts/openapi/admin/geo/`, тег `geo` (версии — `platform`).

### 5.1. Доступ

Каждая операция требует вход сотрудника (`auth.Principal` из сессии сотрудника) и право на
управление справочником. Имена ролей и прав определяет спека `identity`; `geo` спрашивает право
через интерфейс авторизации платформы (`auth.Principal.Can(perm, scope)` или его аналог из спеки
`identity`) — до неё admin-api отвечает 401 (`auth.NoSessions`). Мутирующие операции — с
`Idempotency-Key`, аудит (`platform/audit`) и событие в outbox — в одной транзакции с данными.

### 5.2. Операции

| Операция | Что делает |
| --- | --- |
| `GET /v1/geo/places?q=&country=&cursor=` | Поиск по источнику (префикс названия на любом языке), с `city_id`, если место заведено. |
| `POST /v1/geo/cities` `{geoname_id, status?}` | Активация: `name` на языке страны, переводы, `centroid`, `timezone`, `population`, регион (создаётся из `geonames_admin1`, если его нет), `slug` — транслитерация канонического названия, при занятости — `-<регион>`, затем `-2…`. Статус по умолчанию `waitlist`. Страна должна быть включена — иначе 422 `geo.country_not_enabled`; уже заведён — 409 `geo.city_already_active`. Событие `geo.city_activated`. |
| `PATCH /v1/geo/cities/{cityId}` | `status` (любой переход), `slug` (409 `geo.slug_taken`), названия по локалям. События `geo.city_status_changed`, `geo.city_renamed`. |
| `POST /v1/geo/cities/{cityId}/districts`, `PATCH`/`DELETE …/districts/{districtId}` | Районы. Удаление района, на который ссылаются (будущие поля), — 409 `geo.district_in_use`. |
| `POST /v1/geo/countries`, `PATCH /v1/geo/countries/{code}` | Страна и её правила (`currency`, `default_locale`, `phone_prefix`, `week_starts_on`, `min_signup_age`, `age_of_majority`, `is_enabled`, названия). Значения вносит человек, сверяя с законом; из GeoNames не берутся. Событие `geo.country_updated`. |
| `POST /v1/geo/imports` `{country}`, `GET /v1/geo/imports` | Поставить импорт (§5.3); журнал. Импорт уже идёт — 409 `geo.import_in_progress`. |
| `PUT /v1/app/versions/{platform}` (тег `platform`) | Версии и ссылка в стор; `min ≤ recommended` — иначе 422 `platform.app_version_order`. Событие `platform.app_versions_changed`. |

Понижение статуса (`live → pilot`, `→ waitlist`) разрешено: реакции (`teams`, `notify`) — спеки
подписчиков. Payload событий — по шаблону платформы (спека бэкенда §6.5), версия 1.

### 5.3. Импорт GeoNames

Задача River `geo.import` (очередь `maintenance`, одна на страну). Ставят её ручка `POST
/v1/geo/imports` или команда оператора `worker geo import --country RU` (как `worker events replay`:
пишет в аудит от имени оператора, `started_by = NULL`). Шаги:

1. Скачать по HTTPS `https://download.geonames.org/export/dump/<CC>.zip`,
   `…/alternatenames/<CC>.zip` (если отдельного файла страны для альтернативных названий нет —
   `alternateNamesV2` не грузим целиком: фильтр по `geoname_id` из основного файла) и
   `admin1CodesASCII.txt`. Лимиты размера (из конфига), проверка формата строк; sha256 — в журнал.
   Адреса — в конфиге (`WORKER_GEONAMES_BASE_URL`), в тестах — локальный сервер с выдержками.
2. Отобрать класс `P`, население ≥ 500; названия `ru`/`en` (предпочтительные — флаг
   `isPreferredName`, без исторических и разговорных).
3. Upsert пачками по 1000 строк; строки, исчезнувшие из источника, не удаляются, если на них
   ссылается город (иначе удаляются).
4. Сверка заведённых городов без `geoname_id`: однозначное совпадение страны, региона (по
   названию) и `name_normalized` с местом источника → `geoname_id`, `centroid`, `population`,
   переводы. Ноль или несколько совпадений — в отчёт `reconciled`, город не трогается.
5. Итог в `geonames_imports`, событие `geo.import_finished`.

Повторный запуск безопасен (upsert, сверка только городов без `geoname_id`). Атрибуция CC BY:
`NOTICE` в корне репозитория; клиенты показывают «Данные о городах: GeoNames (CC BY 4.0)» на экране
«О приложении» — требование к спекам клиентов.

### 5.4. Обязательный `centroid`

`0020_geo_centroid_required.sql` делает `cities.centroid NOT NULL`. Миграция проверяет города без
точки и при их наличии падает с сообщением «запустите `worker geo import --country <CC>`» (а не
молча). Порядок на стенде: деплой с `0019` → импорт → деплой с `0020`. Новые города без точки
появиться не могут: активация берёт её из источника.

## 6. API модуля для других модулей

Корневой пакет `internal/geo`:

- `CitySettings(ctx, cityID) (CitySettings, error)` — для `identity` (порог возраста, валюта,
  таймзона, статус); `ErrCityNotFound`.
- Другие модули читают город массово через `geo_read_city_settings`.

## 7. Ошибки

Коды (`x-error-codes`, тексты — `backend/locales/{ru,en}.json`):

| Код | HTTP | Где |
| --- | --- | --- |
| `geo.city_not_found` | 404 | `getCity`, `getCityBySlug`, админские по `cityId` |
| `geo.place_not_found` | 404 | активация по несуществующему `geoname_id` |
| `geo.city_already_active` | 409 | активация |
| `geo.slug_taken` | 409 | смена `slug` |
| `geo.district_in_use` | 409 | удаление района |
| `geo.country_not_enabled` | 422 | активация, импорт |
| `geo.import_in_progress` | 409 | импорт |
| `platform.app_version_order` | 422 | `PUT /v1/app/versions/{platform}` |

Валидация параметров (`lat`, `lon`, `limit`, `status`, `platform`) — общая валидация по контракту.

## 8. Структура кода

`internal/geo/` — корневой пакет (API модуля), `app/` (сценарии: список, город, ближайший,
активация, правки, импорт), `store/` (sqlc, `internal/geo/queries/`), `httpapi/` (публичные
ручки), `admin/` (админские), `jobs/` (`geo.import`), `domain/` — только если появятся правила
(slug, выбор языка — кандидаты). `internal/platform/appversion/` — таблица, сравнение semver,
публичная и админская ручки. Регистрация: строка в `Layers` (`internal/archtest/modules.go`),
элемент `sql` в `backend/sqlc.yaml` с `omit_unused_structs: true`, владение в `ownership.go`.

## 9. Тесты (TDD, стражи с подсадкой бага)

- Хранилище и `nearest` — на настоящем PostGIS через `dbtest` (несколько городов, расстояния,
  граница 30 км, только `pilot`/`live` в `nearest_open`).
- Список: сортировка, фильтры, `q` на ru/en с нормализацией (`ё`, регистр, латиница-двойники),
  курсор.
- Выбор языка: `Accept-Language` с q-весами, неподдерживаемый язык → язык страны, нет перевода →
  каноническое `name`; заголовки `Content-Language`, `Vary`.
- Импорт: парсер на выдержках файлов GeoNames в `testdata` (с атрибуцией), локальный HTTP-сервер
  вместо download.geonames.org, лимит размера, повтор, сверка (однозначно / неоднозначно / нет).
- Миграции: `0019` вверх-вниз, `0020` отказывает при городе без точки и проходит без них.
- Контракт (`apitest`): коды, кэш-заголовки, `x-rate-limit`, анонимность.
- Админка: 401 без входа; с тестовым `Principal` (до `identity` — через `auth.With` в тесте) —
  идемпотентность, аудит и событие в одной транзакции, 409/422 по таблице §7.
- `appversion`: сравнение semver, `min ≤ recommended`, пустая платформа → 200 с `null`.
- Стражи: `archtest` (владение, слои), `grants_test` (§3.5), локали (одинаковые ключи ru/en).

## 10. Порядок в плане

1. **Этап 1 — стенд для нативщика:** миграция `0019`, импорт и команда оператора, публичные ручки
   `geo`, `min-version`, удаление `/v1/health`, документация (`deploy-dev.md`: как запустить импорт
   против Neon), затем на стенде: деплой → импорт → деплой `0020`.
2. **Этап 2 — админка:** админские ручки `geo` и версий; оживают со входом сотрудников (`identity`).

Сразу после `geo` — спека `identity` (вход сотрудников с TOTP включительно).

## 11. Не проверено — проверить первыми шагами плана

- Есть ли у GeoNames отдельный файл альтернативных названий по стране (`alternatenames/<CC>.zip`)
  и его формат на сегодня; размер `RU.zip` и число строк класса `P` с населением ≥ 500.
- Есть ли в CI и на Neon расширения, нужные индексам (`postgis` есть; `text_pattern_ops` — btree,
  расширений не требует).
- Ссылается ли что-нибудь на `city_settings` (перед удалением).
- Использует ли нативщик `/v1/health`.
