-- Кандидаты «here» (спека geo §4.3): места источника в радиусе поиска, ближайшие первыми.
-- Накрытие r(population) отбирает Go (domain.CoverRadiusMeters) — константы в одном месте.
-- name: PlacesWithin :many
SELECT p.geoname_id, p.population,
       ST_Distance(p.location,
                   ST_SetSRID(ST_MakePoint(sqlc.arg(lon)::float8, sqlc.arg(lat)::float8), 4326)::geography)::float8
         AS distance_m
FROM geonames_places p
WHERE ST_DWithin(p.location,
                 ST_SetSRID(ST_MakePoint(sqlc.arg(lon)::float8, sqlc.arg(lat)::float8), 4326)::geography,
                 sqlc.arg(radius_m)::float8)
ORDER BY distance_m, p.geoname_id;

-- Место для ответа «here» (§4.1). Страна: место заведено городом — страна города (в том числе
-- переопределённая админом), иначе страна источника по коду; страны нет в countries или она
-- выключена — 0 строк (here = null). Названия: запрошенная локаль → язык страны → name источника;
-- регион: у заведённого — регион города (может отсутствовать), иначе admin1 источника; '' — нет.
-- name: GetVisiblePlace :one
SELECT p.geoname_id,
       coalesce(pn.name, pd.name, p.name)::text AS name,
       coalesce(CASE WHEN c.id IS NULL THEN coalesce(an.name, ad.name, a.ascii_name)
                     ELSE coalesce(rn.name, r.name) END, '')::text AS region_name,
       co.code::text AS country_code,
       coalesce(con.name, co.name)::text AS country_name,
       c.id AS city_id
FROM geonames_places p
LEFT JOIN cities c ON c.geoname_id = p.geoname_id
LEFT JOIN countries sco ON sco.code = p.country_code
JOIN countries co ON co.id = coalesce(c.country_id, sco.id)
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
WHERE p.geoname_id = sqlc.arg(geoname_id)::bigint AND co.is_enabled;
