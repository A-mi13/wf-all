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
