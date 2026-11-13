-- Запросы админки geo (план B, Task 9–10; спека сотрудников §6, спека geo §4.1, §5.3). Отдельно от
-- публичных cities.sql: админка видит все статусы и страны, в том числе выключенные (без co.is_enabled).
-- Исполняет admin-api под ролью admin (grants.sql: SELECT на справочник и источник, запись справочника).
-- Перед «||» — sqlc.arg(x), не @x (см. cities.sql).

-- Места источника для активации (listGeoPlaces). Страна места — по §4.1: заведено городом — страна
-- города (в том числе переопределённая), иначе страна источника; её нет в справочнике — код источника
-- вместо названия. Название: запрошенная локаль → язык страны → name источника; регион: у заведённого —
-- регион города, иначе admin1 источника (локаль → язык страны → ascii_name); пустая строка — нет.
-- пустой q_prefix — без поиска (уже нормализован normalize_text); пустой country — без фильтра (по стране места).
-- Порядок: population по убыванию, geoname_id; курсор — строго после (after_population, after_geoname_id).
-- name: AdminListPlaces :many
SELECT p.geoname_id,
       coalesce(pn.name, pd.name, p.name)::text AS name,
       p.ascii_name,
       p.feature_code,
       p.population,
       p.timezone,
       coalesce(CASE WHEN c.id IS NULL THEN coalesce(an.name, ad.name, a.ascii_name)
                     ELSE coalesce(rn.name, r.name) END, '')::text AS region_name,
       coalesce(co.code::text, p.country_code::text)::text AS country_code,
       coalesce(con.name, co.name, p.country_code::text)::text AS country_name,
       c.id AS city_id
FROM geonames_places p
LEFT JOIN cities c ON c.geoname_id = p.geoname_id
LEFT JOIN countries sco ON sco.code = p.country_code
LEFT JOIN countries co ON co.id = coalesce(c.country_id, sco.id)
LEFT JOIN geonames_place_names pn ON pn.geoname_id = p.geoname_id AND pn.locale = sqlc.arg(locale)::text
LEFT JOIN geonames_place_names pd ON pd.geoname_id = p.geoname_id AND pd.locale = co.default_locale
LEFT JOIN geonames_admin1 a ON a.country_code = p.country_code AND a.admin1_code = p.admin1_code
LEFT JOIN geonames_admin1_names an
       ON an.country_code = a.country_code AND an.admin1_code = a.admin1_code AND an.locale = sqlc.arg(locale)::text
LEFT JOIN geonames_admin1_names ad
       ON ad.country_code = a.country_code AND ad.admin1_code = a.admin1_code AND ad.locale = co.default_locale
LEFT JOIN regions r ON r.id = c.region_id
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
WHERE (sqlc.arg(country)::text = '' OR coalesce(co.code::text, p.country_code::text) = sqlc.arg(country)::text)
  AND (sqlc.arg(q_prefix)::text = ''
       OR p.ascii_name_normalized LIKE sqlc.arg(q_prefix)::text || '%'
       OR EXISTS (SELECT 1 FROM geonames_place_names qn
                  WHERE qn.geoname_id = p.geoname_id AND qn.name_normalized LIKE sqlc.arg(q_prefix)::text || '%'))
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR p.population < sqlc.arg(after_population)::bigint
       OR (p.population = sqlc.arg(after_population)::bigint AND p.geoname_id > sqlc.arg(after_geoname_id)::bigint))
ORDER BY p.population DESC, p.geoname_id
LIMIT sqlc.arg(row_limit)::bigint;

-- Города админки (listGeoCities): все статусы и страны (country_enabled в ответе), фильтры и порядок — как у
-- публичного ListCities (ранг статуса, population DESC, id) плюс region_id; uuid.Nil в region_id — без
-- фильтра (has_region). districts_count — районы без архивных.
-- name: AdminListCities :many
SELECT c.id, c.slug,
       coalesce(cn.name, c.name)::text AS name,
       c.status::text AS status,
       co.code::text AS country_code,
       co.is_enabled AS country_enabled,
       c.region_id,
       coalesce(rn.name, r.name, '')::text AS region_name,
       c.geoname_id,
       c.population,
       (SELECT count(*) FROM districts d WHERE d.city_id = c.id AND d.archived_at IS NULL)::bigint AS districts_count,
       (CASE c.status WHEN 'live' THEN 0 WHEN 'pilot' THEN 1 ELSE 2 END)::bigint AS status_rank
FROM cities c
JOIN countries co ON co.id = c.country_id
LEFT JOIN regions r ON r.id = c.region_id
LEFT JOIN city_names cn ON cn.city_id = c.id AND cn.locale = sqlc.arg(locale)::text
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
WHERE (coalesce(cardinality(sqlc.arg(statuses)::text[]), 0) = 0
       OR c.status::text = ANY (sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(country)::text = '' OR co.code = sqlc.arg(country)::text)
  AND (NOT sqlc.arg(has_region)::boolean OR c.region_id = sqlc.arg(region_id)::uuid)
  AND (NOT sqlc.arg(filter_ids)::boolean OR c.id = ANY (sqlc.arg(ids)::uuid[]))
  AND (sqlc.arg(q_prefix)::text = ''
       OR c.name_normalized LIKE sqlc.arg(q_prefix)::text || '%'
       OR EXISTS (SELECT 1 FROM city_names qn
                  WHERE qn.city_id = c.id AND qn.name_normalized LIKE sqlc.arg(q_prefix)::text || '%'))
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR (CASE c.status WHEN 'live' THEN 0 WHEN 'pilot' THEN 1 ELSE 2 END) > sqlc.arg(after_rank)::bigint
       OR ((CASE c.status WHEN 'live' THEN 0 WHEN 'pilot' THEN 1 ELSE 2 END) = sqlc.arg(after_rank)::bigint
           AND (c.population < sqlc.arg(after_population)::bigint
                OR (c.population = sqlc.arg(after_population)::bigint AND c.id > sqlc.arg(after_id)::uuid))))
ORDER BY status_rank, c.population DESC, c.id
LIMIT sqlc.arg(row_limit)::bigint;

-- Карточка города админки (getGeoCity): без фильтра видимости страны; canonical_locale — язык страны.
-- name: AdminGetCity :one
SELECT c.id, c.slug,
       coalesce(cn.name, c.name)::text AS name,
       c.status::text AS status,
       c.geoname_id,
       c.population,
       c.timezone,
       ST_Y(c.centroid::geometry)::float8 AS lat,
       ST_X(c.centroid::geometry)::float8 AS lon,
       co.code::text AS country_code,
       coalesce(con.name, co.name)::text AS country_name,
       co.is_enabled AS country_enabled,
       co.default_locale AS canonical_locale,
       c.region_id,
       coalesce(rn.name, r.name, '')::text AS region_name,
       (SELECT count(*) FROM districts d WHERE d.city_id = c.id AND d.archived_at IS NULL)::bigint AS districts_count,
       c.version
FROM cities c
JOIN countries co ON co.id = c.country_id
LEFT JOIN regions r ON r.id = c.region_id
LEFT JOIN city_names cn ON cn.city_id = c.id AND cn.locale = sqlc.arg(locale)::text
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
WHERE c.id = sqlc.arg(id)::uuid;

-- Названия города по всем языкам (GeoCityDetails.names).
-- name: AdminCityNames :many
SELECT locale, name FROM city_names WHERE city_id = sqlc.arg(city_id)::uuid ORDER BY locale;

-- История адресов города, новые сверху (GeoCityDetails.slug_history).
-- name: AdminCitySlugHistory :many
SELECT slug, replaced_at FROM city_slug_history
WHERE city_id = sqlc.arg(city_id)::uuid
ORDER BY replaced_at DESC, slug;

-- Город есть (любой статус и страна) — 404 geo.city_not_found у районов.
-- name: AdminCityExists :one
SELECT EXISTS (SELECT 1 FROM cities WHERE id = sqlc.arg(id)::uuid) AS found;

-- Районы города включая архивные: действующие по названию, затем архивные по названию.
-- name: AdminListDistricts :many
SELECT id, name, archived_at, version FROM districts
WHERE city_id = sqlc.arg(city_id)::uuid
ORDER BY (archived_at IS NOT NULL), name_normalized, id;

-- Все страны по коду: правила, название на локали (иначе каноническое), число городов pilot/live.
-- name: AdminListCountries :many
SELECT co.code::text AS code,
       coalesce(con.name, co.name)::text AS name,
       co.currency::text AS currency,
       co.default_locale,
       co.phone_prefix,
       co.week_starts_on,
       co.min_signup_age,
       co.age_of_majority,
       co.is_enabled,
       (SELECT count(*) FROM cities c WHERE c.country_id = co.id AND c.status IN ('pilot', 'live'))::bigint AS open_cities,
       co.version
FROM countries co
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
ORDER BY co.code;

-- Названия всех стран по всем языкам (GeoCountry.names).
-- name: AdminCountryNames :many
SELECT co.code::text AS code, cn.locale, cn.name
FROM country_names cn JOIN countries co ON co.id = cn.country_id
ORDER BY co.code, cn.locale;

-- Страна есть в справочнике (включённая или нет) — 404 geo.country_not_found у регионов.
-- name: AdminCountryExists :one
SELECT EXISTS (SELECT 1 FROM countries WHERE code = sqlc.arg(code)::text) AS found;

-- Регионы страны по названию на языке ответа (иначе каноническое), затем id.
-- name: AdminListRegions :many
SELECT r.id,
       coalesce(rn.name, r.name)::text AS name,
       r.geoname_admin1_code
FROM regions r
JOIN countries co ON co.id = r.country_id
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
WHERE co.code = sqlc.arg(code)::text
ORDER BY coalesce(rn.name, r.name), r.id;

-- Названия регионов страны по всем языкам (GeoRegion.names).
-- name: AdminRegionNames :many
SELECT rn.region_id, rn.locale, rn.name
FROM region_names rn
JOIN regions r ON r.id = rn.region_id
JOIN countries co ON co.id = r.country_id
WHERE co.code = sqlc.arg(code)::text
ORDER BY rn.region_id, rn.locale;

-- Журнал импортов, новые сверху (started_at DESC, id DESC); пустой country и has_job false — без фильтра;
-- курсор — строго после (after_started_at, after_id) в этом порядке.
-- name: AdminListImports :many
SELECT id, country_code::text AS country_code, status, started_at, finished_at, started_by, river_job_id,
       places_upserted, places_removed, places_missing, names_upserted, error
FROM geonames_imports
WHERE (sqlc.arg(country)::text = '' OR country_code = sqlc.arg(country)::text)
  AND (NOT sqlc.arg(has_job)::boolean OR river_job_id = sqlc.arg(job_id)::bigint)
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR started_at < sqlc.arg(after_started_at)::timestamptz
       OR (started_at = sqlc.arg(after_started_at)::timestamptz AND id < sqlc.arg(after_id)::uuid))
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg(row_limit)::bigint;

-- Строка журнала импорта с отчётом сверки (getGeoImport).
-- name: AdminGetImport :one
SELECT id, country_code::text AS country_code, status, started_at, finished_at, started_by, river_job_id,
       places_upserted, places_removed, places_missing, names_upserted, error, reconciled
FROM geonames_imports
WHERE id = sqlc.arg(id)::uuid;

-- Места-кандидаты отчёта сверки: название и регион на языке ответа (иначе язык страны источника, затем
-- name/ascii_name источника), население. Пропавших из таблицы мест (удалены следующим импортом) нет в ответе.
-- name: AdminPlacesByIDs :many
SELECT p.geoname_id,
       coalesce(pn.name, pd.name, p.name)::text AS name,
       coalesce(an.name, ad.name, a.ascii_name, '')::text AS region_name,
       p.population
FROM geonames_places p
LEFT JOIN countries co ON co.code = p.country_code
LEFT JOIN geonames_place_names pn ON pn.geoname_id = p.geoname_id AND pn.locale = sqlc.arg(locale)::text
LEFT JOIN geonames_place_names pd ON pd.geoname_id = p.geoname_id AND pd.locale = co.default_locale
LEFT JOIN geonames_admin1 a ON a.country_code = p.country_code AND a.admin1_code = p.admin1_code
LEFT JOIN geonames_admin1_names an
       ON an.country_code = a.country_code AND an.admin1_code = a.admin1_code AND an.locale = sqlc.arg(locale)::text
LEFT JOIN geonames_admin1_names ad
       ON ad.country_code = a.country_code AND ad.admin1_code = a.admin1_code AND ad.locale = co.default_locale
WHERE p.geoname_id = ANY (sqlc.arg(ids)::bigint[]);

-- Какие из id есть в cities (все статусы и страны; проверка городов флага, R19).
-- name: ExistingCityIDs :many
SELECT id FROM cities WHERE id = ANY (sqlc.arg(ids)::uuid[]) ORDER BY id;

-- ---------------------------------------------------------------------------
-- Активация города (activateGeoCity, Task 10; спека geo §5.3, R13). Всё — в одной транзакции сценария.
-- ---------------------------------------------------------------------------

-- Место источника для активации; missing — пропало из источника (missing_since не NULL, R13).
-- name: ActivatePlace :one
SELECT p.geoname_id, p.country_code::text AS country_code, coalesce(p.admin1_code, '')::text AS admin1_code,
       p.name, p.ascii_name, p.population, p.timezone, (p.missing_since IS NOT NULL)::boolean AS missing
FROM geonames_places p
WHERE p.geoname_id = sqlc.arg(geoname_id)::bigint;

-- Страна активации по коду: id, язык, включена ли.
-- name: ActivateCountry :one
SELECT id, default_locale, is_enabled FROM countries WHERE code = sqlc.arg(code)::text;

-- На месте уже есть город (быстрый ответ 409; гонку ловит cities_geoname_id_key).
-- name: ActivateCityOnPlace :one
SELECT EXISTS (SELECT 1 FROM cities WHERE geoname_id = sqlc.arg(geoname_id)::bigint) AS found;

-- Регион из запроса — только своей страны; ascii-название admin1 региона (для суффикса slug), если есть.
-- name: ActivateRegionOfCountry :one
SELECT r.id, coalesce(a.ascii_name, '')::text AS ascii_name
FROM regions r
JOIN countries co ON co.id = r.country_id
LEFT JOIN geonames_admin1 a ON a.country_code = co.code AND a.admin1_code = r.geoname_admin1_code
WHERE r.id = sqlc.arg(id)::uuid AND r.country_id = sqlc.arg(country_id)::uuid;

-- admin1 источника: ascii-название и название на языке страны (выбрано импортом по §3.4; пусто — нет).
-- name: ActivateAdmin1 :one
SELECT a.ascii_name, coalesce(an.name, '')::text AS local_name
FROM geonames_admin1 a
LEFT JOIN geonames_admin1_names an
       ON an.country_code = a.country_code AND an.admin1_code = a.admin1_code AND an.locale = sqlc.arg(locale)::text
WHERE a.country_code = sqlc.arg(country_code)::text AND a.admin1_code = sqlc.arg(admin1_code)::text;

-- Новый регион страны с кодом admin1; уже есть (в том числе параллельная активация вставила и закоммитила) —
-- ни одной строки, без 23505 (regions_country_admin1_key — частичный уникальный индекс).
-- name: ActivateInsertRegion :one
INSERT INTO regions (id, country_id, name, geoname_admin1_code, created_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(country_id)::uuid, sqlc.arg(name)::text, sqlc.arg(admin1_code)::text,
        sqlc.arg(created_at)::timestamptz)
ON CONFLICT (country_id, geoname_admin1_code) WHERE geoname_admin1_code IS NOT NULL DO NOTHING
RETURNING id;

-- Регион страны по коду admin1.
-- name: ActivateRegionByAdmin1 :one
SELECT id FROM regions
WHERE country_id = sqlc.arg(country_id)::uuid AND geoname_admin1_code = sqlc.arg(admin1_code)::text;

-- Названия нового региона: строка языка страны — каноническое имя, прочие — из admin1 источника.
-- name: ActivateRegionNames :exec
INSERT INTO region_names (region_id, locale, name)
SELECT sqlc.arg(region_id)::uuid, sqlc.arg(country_locale)::text, sqlc.arg(canonical)::text
UNION ALL
SELECT sqlc.arg(region_id)::uuid, an.locale, an.name
FROM geonames_admin1_names an
WHERE an.country_code = sqlc.arg(country_code)::text
  AND an.admin1_code = sqlc.arg(admin1_code)::text
  AND an.locale <> sqlc.arg(country_locale)::text
  AND btrim(an.name) <> ''
ON CONFLICT DO NOTHING;

-- Названия места по языкам (выбраны импортом по §3.4).
-- name: ActivatePlaceNames :many
SELECT locale, name FROM geonames_place_names WHERE geoname_id = sqlc.arg(geoname_id)::bigint;

-- Адрес занят: текущий slug города или прежний адрес из истории.
-- name: SlugTaken :one
SELECT (EXISTS (SELECT 1 FROM cities WHERE slug = sqlc.arg(slug)::text)
        OR EXISTS (SELECT 1 FROM city_slug_history WHERE slug = sqlc.arg(slug)::text))::boolean AS taken;

-- Новый город из места: точка — из источника, версия 1, время — часов сценария. 0 строк — место удалено
-- параллельным импортом после ActivatePlace (geo.place_not_found, а не FK на city_names).
-- name: ActivateInsertCity :execrows
INSERT INTO cities (id, country_id, region_id, name, slug, timezone, centroid, status, population, geoname_id,
                    version, created_at, updated_at)
SELECT sqlc.arg(id)::uuid, sqlc.arg(country_id)::uuid, sqlc.narg(region_id)::uuid, sqlc.arg(name)::text,
       sqlc.arg(slug)::text, sqlc.arg(timezone)::text, p.location, sqlc.arg(status)::text::city_status,
       p.population, p.geoname_id, 1, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz
FROM geonames_places p
WHERE p.geoname_id = sqlc.arg(geoname_id)::bigint;

-- Названия нового города по языкам (массивы одной длины).
-- name: ActivateCityNames :exec
INSERT INTO city_names (city_id, locale, name)
SELECT sqlc.arg(city_id)::uuid, u.locale, u.name
FROM (SELECT unnest(sqlc.arg(locales)::text[]) AS locale, unnest(sqlc.arg(names)::text[]) AS name) AS u;

-- Активация сериализуется со сверкой импорта страны (ревью G5 I1-r1): разделяемый advisory-замок транзакции
-- (класс, страна) первым замком сценария — активации друг друга не ждут, сверка страны (ImportReconcileLock,
-- эксклюзивный) ждёт их и наоборот. Ключ — app.countryLockKey.
-- name: ActivateCountryLock :exec
SELECT pg_advisory_xact_lock_shared(sqlc.arg(class)::int, sqlc.arg(country)::int);

-- ---------------------------------------------------------------------------
-- Правка города (updateGeoCity, Task 11; спека geo §5.2, R10, R11) и ручная привязка (linkGeoCity, R14).
-- ---------------------------------------------------------------------------

-- Город под замком правки: текущие значения для сравнения «изменилось ли», язык страны (каноническое имя).
-- name: CityForUpdate :one
SELECT c.id, c.slug, c.name, c.status::text AS status, c.version, c.geoname_id, c.region_id,
       co.code::text AS country_code, co.default_locale
FROM cities c
JOIN countries co ON co.id = c.country_id
WHERE c.id = sqlc.arg(id)::uuid
FOR UPDATE OF c;

-- Страна города без замка — ключ разделяемого замка страны до замка строки (linkGeoCity).
-- name: CityCountryCode :one
SELECT co.code::text AS country_code
FROM cities c JOIN countries co ON co.id = c.country_id
WHERE c.id = sqlc.arg(id)::uuid;

-- Смены адреса на один slug сериализуются (ревью G6 m4): эксклюзивный advisory-замок транзакции (класс, hashtext(slug))
-- до проверки SlugOwner — иначе проверка, прошедшая до коммита чужой смены на тот же slug, и третья смена, унёсшая
-- его в историю, дают текущий slug одного города в истории другого. Совпадение хэшей двух slug — только ожидание.
-- name: SlugLock :exec
SELECT pg_advisory_xact_lock(sqlc.arg(class)::int, hashtext(sqlc.arg(slug)::text));

-- Чей адрес: текущий slug города (history = false) или строка истории (history = true); нет — ни одной строки.
-- name: SlugOwner :one
SELECT id AS city_id, false AS history FROM cities WHERE slug = sqlc.arg(slug)::text
UNION ALL
SELECT city_id, true AS history FROM city_slug_history WHERE slug = sqlc.arg(slug)::text
LIMIT 1;

-- Свой прежний адрес возвращается текущим — строка истории удаляется.
-- name: DeleteOwnSlugHistory :execrows
DELETE FROM city_slug_history WHERE slug = sqlc.arg(slug)::text AND city_id = sqlc.arg(city_id)::uuid;

-- Прежний текущий адрес — в историю; время — часов сценария.
-- name: InsertSlugHistory :exec
INSERT INTO city_slug_history (slug, city_id, replaced_at)
VALUES (sqlc.arg(slug)::text, sqlc.arg(city_id)::uuid, sqlc.arg(replaced_at)::timestamptz);

-- Поля города после правки; версия растёт на число событий транзакции (R11).
-- name: UpdateCityFields :one
UPDATE cities
SET status = sqlc.arg(status)::text::city_status, slug = sqlc.arg(slug)::text, name = sqlc.arg(name)::text,
    version = version + sqlc.arg(bump)::bigint
WHERE id = sqlc.arg(id)::uuid
RETURNING version;

-- Название города на языке (новое или переписанное).
-- name: UpsertCityName :exec
INSERT INTO city_names (city_id, locale, name)
VALUES (sqlc.arg(city_id)::uuid, sqlc.arg(locale)::text, sqlc.arg(name)::text)
ON CONFLICT (city_id, locale) DO UPDATE SET name = EXCLUDED.name;

-- Перевод города удалён (строку языка страны сценарий не удаляет).
-- name: DeleteCityName :exec
DELETE FROM city_names WHERE city_id = sqlc.arg(city_id)::uuid AND locale = sqlc.arg(locale)::text;

-- ---------------------------------------------------------------------------
-- Районы (createGeoDistrict, updateGeoDistrict, Task 12; спека geo §3.2, §5.2). Дубль имени среди действующих —
-- 23505 districts_city_name_active_key (normalize_text: регистр, ё/е).
-- ---------------------------------------------------------------------------

-- Новый район города; время — часов сценария.
-- name: InsertDistrict :one
INSERT INTO districts (id, city_id, name, created_at)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(city_id)::uuid, sqlc.arg(name)::text, sqlc.arg(created_at)::timestamptz)
RETURNING id, name, archived_at, version;

-- Район города под замком правки; район другого города — ни одной строки (404 без раскрытия существования).
-- name: DistrictForUpdate :one
SELECT id, name, archived_at, version FROM districts
WHERE id = sqlc.arg(id)::uuid AND city_id = sqlc.arg(city_id)::uuid
FOR UPDATE;

-- Имя и архив района после правки, версия + 1.
-- name: UpdateDistrict :one
UPDATE districts
SET name = sqlc.arg(name)::text, archived_at = sqlc.narg(archived_at)::timestamptz, version = version + 1
WHERE id = sqlc.arg(id)::uuid
RETURNING id, name, archived_at, version;

-- ---------------------------------------------------------------------------
-- Страны (createGeoCountry, updateGeoCountry и исполнители geo.country_create, geo.country_rules, Task 13;
-- спека geo §5.2, R12, R25). Правила страны — всегда вдвоём (решение пользователя 27.10).
-- ---------------------------------------------------------------------------

-- Страна под замком правки по коду: текущие правила для сравнения «меняется ли» и итоговой проверки.
-- name: CountryForUpdate :one
SELECT id, code::text AS code, name, currency::text AS currency, default_locale, phone_prefix,
       week_starts_on::int AS week_starts_on, min_signup_age::int AS min_signup_age,
       age_of_majority::int AS age_of_majority, is_enabled, version
FROM countries
WHERE code = sqlc.arg(code)::text
FOR UPDATE;

-- Страна под замком исполнителя по id (из payload запроса).
-- name: CountryForUpdateByID :one
SELECT id, code::text AS code, name, currency::text AS currency, default_locale, phone_prefix,
       week_starts_on::int AS week_starts_on, min_signup_age::int AS min_signup_age,
       age_of_majority::int AS age_of_majority, is_enabled, version
FROM countries
WHERE id = sqlc.arg(id)::uuid
FOR UPDATE;

-- Названия страны по языкам.
-- name: CountryNamesOf :many
SELECT locale, name FROM country_names WHERE country_id = sqlc.arg(country_id)::uuid ORDER BY locale;

-- Новая страна (исполнитель geo.country_create): версия 1; занятый код — 23505 countries_code_key.
-- name: InsertCountry :exec
INSERT INTO countries (id, code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
                       age_of_majority, is_enabled, version)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(code)::text, sqlc.arg(name)::text, sqlc.arg(currency)::text,
        sqlc.arg(default_locale)::text, sqlc.arg(phone_prefix)::text, sqlc.arg(week_starts_on)::bigint,
        sqlc.arg(min_signup_age)::bigint, sqlc.arg(age_of_majority)::bigint, sqlc.arg(is_enabled)::boolean, 1);

-- Поля страны после правки, версия + 1.
-- name: UpdateCountryFields :one
UPDATE countries
SET name = sqlc.arg(name)::text, currency = sqlc.arg(currency)::text, phone_prefix = sqlc.arg(phone_prefix)::text,
    week_starts_on = sqlc.arg(week_starts_on)::bigint, min_signup_age = sqlc.arg(min_signup_age)::bigint,
    age_of_majority = sqlc.arg(age_of_majority)::bigint, is_enabled = sqlc.arg(is_enabled)::boolean,
    version = version + 1
WHERE id = sqlc.arg(id)::uuid
RETURNING version;

-- Название страны на языке (новое или переписанное).
-- name: UpsertCountryName :exec
INSERT INTO country_names (country_id, locale, name)
VALUES (sqlc.arg(country_id)::uuid, sqlc.arg(locale)::text, sqlc.arg(name)::text)
ON CONFLICT (country_id, locale) DO UPDATE SET name = EXCLUDED.name;

-- Перевод страны удалён (строку языка страны сценарий не удаляет).
-- name: DeleteCountryName :exec
DELETE FROM country_names WHERE country_id = sqlc.arg(country_id)::uuid AND locale = sqlc.arg(locale)::text;

-- Страна с правилами по коду для ответа правки (как строка AdminListCountries).
-- name: AdminGetCountry :one
SELECT co.code::text AS code,
       coalesce(con.name, co.name)::text AS name,
       co.currency::text AS currency,
       co.default_locale,
       co.phone_prefix,
       co.week_starts_on,
       co.min_signup_age,
       co.age_of_majority,
       co.is_enabled,
       (SELECT count(*) FROM cities c WHERE c.country_id = co.id AND c.status IN ('pilot', 'live'))::bigint AS open_cities,
       co.version,
       co.id
FROM countries co
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
WHERE co.code = sqlc.arg(code)::text;
