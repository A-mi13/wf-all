-- Нормализация строки поиска той же функцией, что у name_normalized: пустой результат — 400.
-- name: NormalizeQuery :one
SELECT normalize_text(sqlc.arg(input)::text)::text AS normalized;

-- Публичный список городов (спека geo §4.2). Видимость — только страны is_enabled (§4.1).
-- Названия: строка *_names запрошенной локали, иначе каноническое name (язык страны);
-- locale = '' — локаль не согласована: строк с пустой локалью нет (CHECK ^[a-z]{2}$), все названия
-- канонические. Регион: region_id NULL — региона нет, region_name тогда ''.
-- Порядок: ранг статуса (live 0, pilot 1, waitlist 2), population по убыванию, id; курсор —
-- строго после (after_rank, after_population, after_id) в этом порядке.
-- statuses пуст — без фильтра; country '' — без фильтра; q_prefix '' — без поиска. q_prefix уже
-- нормализован (normalize_text оставляет только [a-zа-яё0-9], поэтому % и _ в нём невозможны).
-- Перед «||» — sqlc.arg(x), не @x: префиксный «@» захватил бы всё выражение (см. ratelimit.sql).
-- name: ListCities :many
SELECT c.id, c.slug,
       coalesce(cn.name, c.name)::text AS name,
       c.status::text AS status,
       c.timezone,
       ST_Y(c.centroid::geometry)::float8 AS lat,
       ST_X(c.centroid::geometry)::float8 AS lon,
       c.region_id,
       coalesce(rn.name, r.name, '')::text AS region_name,
       co.code::text AS country_code,
       coalesce(con.name, co.name)::text AS country_name,
       c.population,
       (CASE c.status WHEN 'live' THEN 0 WHEN 'pilot' THEN 1 ELSE 2 END)::bigint AS status_rank
FROM cities c
JOIN countries co ON co.id = c.country_id
LEFT JOIN regions r ON r.id = c.region_id
LEFT JOIN city_names cn ON cn.city_id = c.id AND cn.locale = sqlc.arg(locale)::text
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
WHERE co.is_enabled
  AND (coalesce(cardinality(sqlc.arg(statuses)::text[]), 0) = 0
       OR c.status::text = ANY (sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(country)::text = '' OR co.code = sqlc.arg(country)::text)
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

-- Город с правилами страны (CityDetails без районов); страна выключена или города нет — 0 строк.
-- name: GetVisibleCity :one
SELECT c.id, c.slug,
       coalesce(cn.name, c.name)::text AS name,
       c.status::text AS status,
       c.timezone,
       ST_Y(c.centroid::geometry)::float8 AS lat,
       ST_X(c.centroid::geometry)::float8 AS lon,
       c.region_id,
       coalesce(rn.name, r.name, '')::text AS region_name,
       co.code::text AS country_code,
       coalesce(con.name, co.name)::text AS country_name,
       co.currency::text AS currency,
       co.default_locale,
       co.phone_prefix,
       co.week_starts_on,
       co.min_signup_age,
       co.age_of_majority
FROM cities c
JOIN countries co ON co.id = c.country_id
LEFT JOIN regions r ON r.id = c.region_id
LEFT JOIN city_names cn ON cn.city_id = c.id AND cn.locale = sqlc.arg(locale)::text
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
WHERE c.id = sqlc.arg(id)::uuid AND co.is_enabled;

-- Город по slug: текущий slug раньше истории (prio 0); видимость проверяет GetVisibleCity.
-- name: ResolveCitySlug :one
SELECT s.city_id FROM (
  SELECT id AS city_id, 0 AS prio FROM cities WHERE slug = sqlc.arg(slug)::text
  UNION ALL
  SELECT h.city_id, 1 AS prio FROM city_slug_history h WHERE h.slug = sqlc.arg(slug)::text
) s
ORDER BY s.prio
LIMIT 1;

-- Районы города без архивных (§3.2).
-- name: ListActiveDistricts :many
SELECT id, name FROM districts
WHERE city_id = sqlc.arg(city_id)::uuid AND archived_at IS NULL
ORDER BY name_normalized, id;

-- Ближайший видимый город pilot/live по centroid без ограничения расстояния (§4.3): KNN «<->»
-- по GiST-индексу, расстояние — ST_Distance по geography (сфероид, антимеридиан учтён).
-- name: NearestOpenCity :one
SELECT c.id, c.slug,
       coalesce(cn.name, c.name)::text AS name,
       c.status::text AS status,
       c.timezone,
       ST_Y(c.centroid::geometry)::float8 AS lat,
       ST_X(c.centroid::geometry)::float8 AS lon,
       c.region_id,
       coalesce(rn.name, r.name, '')::text AS region_name,
       co.code::text AS country_code,
       coalesce(con.name, co.name)::text AS country_name,
       ST_Distance(c.centroid,
                   ST_SetSRID(ST_MakePoint(sqlc.arg(lon)::float8, sqlc.arg(lat)::float8), 4326)::geography)::float8
         AS distance_m
FROM cities c
JOIN countries co ON co.id = c.country_id
LEFT JOIN regions r ON r.id = c.region_id
LEFT JOIN city_names cn ON cn.city_id = c.id AND cn.locale = sqlc.arg(locale)::text
LEFT JOIN region_names rn ON rn.region_id = r.id AND rn.locale = sqlc.arg(locale)::text
LEFT JOIN country_names con ON con.country_id = co.id AND con.locale = sqlc.arg(locale)::text
WHERE co.is_enabled AND c.status IN ('pilot', 'live')
ORDER BY c.centroid <-> ST_SetSRID(ST_MakePoint(sqlc.arg(lon)::float8, sqlc.arg(lat)::float8), 4326)::geography,
         c.id
LIMIT 1;

-- Настройки города для других модулей (§7): без фильтра видимости — country_enabled в ответе.
-- name: GetCitySettings :one
SELECT c.id, c.timezone, c.status::text AS status, co.code::text AS country_code,
       co.is_enabled AS country_enabled, co.currency::text AS currency, co.default_locale,
       co.min_signup_age, co.age_of_majority
FROM cities c
JOIN countries co ON co.id = c.country_id
WHERE c.id = sqlc.arg(id)::uuid;
