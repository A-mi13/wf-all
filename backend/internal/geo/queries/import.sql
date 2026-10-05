-- Импорт GeoNames (спека geo §5.4): журнал geonames_imports, источник geonames_*, сверка
-- заведённых городов. Исполняет воркер (роль worker, grants.sql §3.8).

-- name: ImportCountry :one
SELECT id, default_locale FROM countries WHERE code = sqlc.arg(code);

-- name: ImportFindOwnRunning :one
-- Строка running этой же задачи River — прерванная попытка: она продолжается (шаг 1).
SELECT id FROM geonames_imports
WHERE country_code = sqlc.arg(country_code)
  AND status = 'running'
  AND river_job_id = sqlc.arg(river_job_id)::bigint;

-- name: ImportRestart :exec
UPDATE geonames_imports SET started_at = sqlc.arg(started_at)::timestamptz WHERE id = sqlc.arg(id)::uuid;

-- name: ImportFailStale :many
-- Строки running старше таймаута задачи + 10 минут — процесс убит без итога (шаг 1).
UPDATE geonames_imports
SET status = 'failed', finished_at = sqlc.arg(now)::timestamptz, error = sqlc.arg(reason)::text
WHERE country_code = sqlc.arg(country_code)
  AND status = 'running'
  AND started_at < sqlc.arg(stale_before)::timestamptz
RETURNING id;

-- name: ImportStart :exec
INSERT INTO geonames_imports (id, country_code, status, started_at, started_by, river_job_id)
VALUES (sqlc.arg(id)::uuid, sqlc.arg(country_code)::text, 'running', sqlc.arg(started_at)::timestamptz,
        sqlc.narg(started_by)::uuid, sqlc.narg(river_job_id)::bigint);

-- name: ImportLastSucceededPlaces :one
-- Мест в прошлом успешном импорте страны — база порога 90 % (шаг 4).
SELECT COALESCE(places_upserted, 0)::int AS places
FROM geonames_imports
WHERE country_code = sqlc.arg(country_code) AND status = 'succeeded'
ORDER BY finished_at DESC, started_at DESC
LIMIT 1;

-- name: ImportAdmin1Count :one
-- Строк admin1 страны сейчас — база порога 90 % для admin1 источника (R34): общий файл
-- admin1CodesASCII.txt, обрезанный на границе строки, иначе молча удалил бы регионы страны.
SELECT count(*)::int AS n FROM geonames_admin1 WHERE country_code = sqlc.arg(country_code);

-- name: ImportUpsertPlaces :exec
-- Пачка мест (до 1000): массивы одной длины, по элементу на место.
INSERT INTO geonames_places (geoname_id, country_code, admin1_code, name, ascii_name, feature_code,
                             population, location, timezone, missing_since, import_id)
SELECT u.geoname_id, u.country_code, NULLIF(u.admin1_code, ''), u.name, u.ascii_name, u.feature_code,
       u.population, ST_SetSRID(ST_MakePoint(u.lon, u.lat), 4326)::geography, u.timezone, NULL,
       sqlc.arg(import_id)::uuid
FROM (SELECT unnest(sqlc.arg(geoname_id)::bigint[])  AS geoname_id,
             unnest(sqlc.arg(country_code)::text[])  AS country_code,
             unnest(sqlc.arg(admin1_code)::text[])   AS admin1_code,
             unnest(sqlc.arg(name)::text[])          AS name,
             unnest(sqlc.arg(ascii_name)::text[])    AS ascii_name,
             unnest(sqlc.arg(feature_code)::text[])  AS feature_code,
             unnest(sqlc.arg(population)::bigint[])  AS population,
             unnest(sqlc.arg(lat)::float8[])         AS lat,
             unnest(sqlc.arg(lon)::float8[])         AS lon,
             unnest(sqlc.arg(timezone)::text[])      AS timezone) AS u
ON CONFLICT (geoname_id) DO UPDATE SET
    country_code  = EXCLUDED.country_code,
    admin1_code   = EXCLUDED.admin1_code,
    name          = EXCLUDED.name,
    ascii_name    = EXCLUDED.ascii_name,
    feature_code  = EXCLUDED.feature_code,
    population    = EXCLUDED.population,
    location      = EXCLUDED.location,
    timezone      = EXCLUDED.timezone,
    missing_since = NULL,
    import_id     = EXCLUDED.import_id;

-- name: ImportDeletePlaceNames :exec
DELETE FROM geonames_place_names WHERE geoname_id = ANY(sqlc.arg(geoname_id)::bigint[]);

-- name: ImportInsertPlaceNames :execrows
INSERT INTO geonames_place_names (geoname_id, locale, name)
SELECT u.geoname_id, u.locale, u.name
FROM (SELECT unnest(sqlc.arg(geoname_id)::bigint[]) AS geoname_id,
             unnest(sqlc.arg(locale)::text[])       AS locale,
             unnest(sqlc.arg(name)::text[])         AS name) AS u;

-- name: ImportDeleteAdmin1Names :exec
DELETE FROM geonames_admin1_names WHERE country_code = sqlc.arg(country_code);

-- name: ImportUpsertAdmin1 :exec
INSERT INTO geonames_admin1 (country_code, admin1_code, geoname_id, ascii_name)
SELECT sqlc.arg(country_code)::text, u.admin1_code, u.geoname_id, u.ascii_name
FROM (SELECT unnest(sqlc.arg(admin1_code)::text[]) AS admin1_code,
             unnest(sqlc.arg(geoname_id)::bigint[]) AS geoname_id,
             unnest(sqlc.arg(ascii_name)::text[])   AS ascii_name) AS u
ON CONFLICT (country_code, admin1_code) DO UPDATE SET
    geoname_id = EXCLUDED.geoname_id,
    ascii_name = EXCLUDED.ascii_name;

-- name: ImportDeleteStaleAdmin1 :exec
DELETE FROM geonames_admin1
WHERE country_code = sqlc.arg(country_code)
  AND NOT (admin1_code = ANY(sqlc.arg(keep)::text[]));

-- name: ImportInsertAdmin1Names :execrows
INSERT INTO geonames_admin1_names (country_code, admin1_code, locale, name)
SELECT sqlc.arg(country_code)::text, u.admin1_code, u.locale, u.name
FROM (SELECT unnest(sqlc.arg(admin1_code)::text[]) AS admin1_code,
             unnest(sqlc.arg(locale)::text[])      AS locale,
             unnest(sqlc.arg(name)::text[])        AS name) AS u;

-- name: ImportMarkMissing :many
-- Пропали из источника, но на них ссылается город: остаются с отметкой и попадают в отчёт (шаг 4).
UPDATE geonames_places p
SET missing_since = COALESCE(p.missing_since, sqlc.arg(now)::timestamptz)
WHERE p.country_code = sqlc.arg(country_code)
  AND p.import_id <> sqlc.arg(import_id)::uuid
  AND EXISTS (SELECT 1 FROM cities c WHERE c.geoname_id = p.geoname_id)
RETURNING p.geoname_id;

-- name: ImportDeleteGone :execrows
DELETE FROM geonames_places p
WHERE p.country_code = sqlc.arg(country_code)
  AND p.import_id <> sqlc.arg(import_id)::uuid
  AND NOT EXISTS (SELECT 1 FROM cities c WHERE c.geoname_id = p.geoname_id);

-- name: ImportUnlinkedCities :many
-- Заведённые города страны без geoname_id — кандидаты сверки (шаг 5).
SELECT c.id, c.slug, c.region_id
FROM cities c
JOIN countries co ON co.id = c.country_id
WHERE co.code = sqlc.arg(country_code) AND c.geoname_id IS NULL
ORDER BY c.slug;

-- name: ImportCityCandidates :many
-- Места страны с тем же названием (основное или ru, после normalize_text) в том же регионе:
-- по geoname_admin1_code региона, а без кода — по названиям региона и admin1. Место, уже
-- привязанное к городу, не кандидат. Город без региона кандидатов не получает.
SELECT p.geoname_id, p.admin1_code
FROM cities c
JOIN regions r ON r.id = c.region_id,
     geonames_places p
WHERE c.id = sqlc.arg(city_id)::uuid
  AND p.country_code = sqlc.arg(country_code)
  AND (normalize_text(p.name) = c.name_normalized
       OR EXISTS (SELECT 1 FROM geonames_place_names pn
                  WHERE pn.geoname_id = p.geoname_id AND pn.locale = 'ru'
                    AND pn.name_normalized = c.name_normalized))
  AND (p.admin1_code = r.geoname_admin1_code
       OR (r.geoname_admin1_code IS NULL AND EXISTS (
             SELECT 1
             FROM region_names rn
             JOIN geonames_admin1_names an
               ON an.country_code = p.country_code AND an.admin1_code = p.admin1_code
             WHERE rn.region_id = r.id AND normalize_text(rn.name) = normalize_text(an.name))))
  AND NOT EXISTS (SELECT 1 FROM cities c2 WHERE c2.geoname_id = p.geoname_id)
ORDER BY p.geoname_id;

-- name: ImportLinkCity :one
UPDATE cities
SET geoname_id = sqlc.arg(geoname_id)::bigint, version = version + 1
WHERE id = sqlc.arg(id)::uuid AND geoname_id IS NULL
RETURNING version;

-- name: ImportCityNamesFromPlace :execrows
-- Только отсутствующие переводы (§3.3); строку языка страны сверка не создаёт — она источник
-- канонического cities.name.
INSERT INTO city_names (city_id, locale, name)
SELECT sqlc.arg(city_id)::uuid, pn.locale, pn.name
FROM geonames_place_names pn
WHERE pn.geoname_id = sqlc.arg(geoname_id)::bigint
  AND pn.locale <> sqlc.arg(country_locale)::text
  AND btrim(pn.name) <> ''
ON CONFLICT DO NOTHING;

-- name: ImportLinkRegion :execrows
-- Региону, найденному по названию, — код admin1 (если пуст и не занят другим регионом страны:
-- уникальность regions_country_admin1_key).
UPDATE regions r
SET geoname_admin1_code = sqlc.arg(admin1_code)::text
WHERE r.id = sqlc.arg(id)::uuid
  AND r.geoname_admin1_code IS NULL
  AND NOT EXISTS (SELECT 1 FROM regions r2
                  WHERE r2.country_id = r.country_id
                    AND r2.geoname_admin1_code = sqlc.arg(admin1_code)::text);

-- name: ImportRegionNamesFromAdmin1 :execrows
INSERT INTO region_names (region_id, locale, name)
SELECT sqlc.arg(region_id)::uuid, an.locale, an.name
FROM geonames_admin1_names an
WHERE an.country_code = sqlc.arg(country_code)
  AND an.admin1_code = sqlc.arg(admin1_code)::text
  AND an.locale <> sqlc.arg(country_locale)::text
  AND btrim(an.name) <> ''
ON CONFLICT DO NOTHING;

-- name: ImportSucceed :execrows
UPDATE geonames_imports
SET status = 'succeeded', finished_at = sqlc.arg(now)::timestamptz,
    source_files = sqlc.arg(source_files)::jsonb,
    places_upserted = sqlc.arg(places_upserted)::bigint, places_removed = sqlc.arg(places_removed)::bigint,
    places_missing = sqlc.arg(places_missing)::bigint, names_upserted = sqlc.arg(names_upserted)::bigint,
    reconciled = sqlc.arg(reconciled)::jsonb, error = NULL
WHERE id = sqlc.arg(id)::uuid AND status = 'running';

-- name: ImportFail :execrows
-- source_files — уже скачанные файлы (R27); NULL — ничего не скачано, прежнее значение остаётся.
UPDATE geonames_imports
SET status = 'failed', finished_at = sqlc.arg(now)::timestamptz, error = sqlc.arg(reason)::text,
    source_files = COALESCE(sqlc.narg(source_files)::jsonb, source_files)
WHERE id = sqlc.arg(id)::uuid AND status = 'running';
