-- 0019_geo.sql
-- Модуль geo (спека docs/superpowers/specs/2026-10-02-geo-design.md, §3): источник GeoNames,
-- названия по локалям, история slug, идентичность (geoname_id) и точка города, архив районов,
-- версии агрегатов, экспортированное представление geo_read_city_settings вместо city_settings,
-- минимальная версия приложения (app_versions, владелец platform) и данные сида (§3.6).
-- sqlc разбирает грамматику PostgreSQL 17: генерируемые колонки — явно STORED (в PG18 по
-- умолчанию VIRTUAL, на ней нет индекса).

-- +goose Up

-- ---------------------------------------------------------------------------
-- Источник GeoNames (§3.1). Перезагружаемая копия: на продукт не влияет, город на неё не
-- ссылается внешним ключом.
-- ---------------------------------------------------------------------------

CREATE TABLE geonames_imports (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    country_code    char(2)     NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    status          text        NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    -- задача River; NULL — команда оператора. Строка running с тем же id задачи — прерванная
    -- попытка этой же задачи: она продолжается, а не получает 409 (§5.4 шаг 1)
    river_job_id    bigint,
    source_files    jsonb       NOT NULL DEFAULT '[]',  -- [{name, url, bytes, sha256}]
    -- время — из clock.Clock приложения, умолчания нет намеренно
    started_at      timestamptz NOT NULL,
    finished_at     timestamptz,
    places_upserted int,
    places_removed  int,
    places_missing  int,
    names_upserted  int,
    reconciled      jsonb,                              -- отчёт сверки (§5.4 шаг 5)
    error           text,
    started_by      uuid                                -- сотрудник; NULL — команда оператора
);

-- один импорт страны за раз: параллельный получит 409 geo.import_in_progress
CREATE UNIQUE INDEX geonames_imports_running ON geonames_imports (country_code) WHERE status = 'running';
-- журнал страны и порог 90 % от прошлого успешного импорта
CREATE INDEX geonames_imports_country_idx ON geonames_imports (country_code, started_at DESC);

CREATE TABLE geonames_places (
    geoname_id    bigint PRIMARY KEY,
    country_code  char(2)     NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    admin1_code   text,
    name          text        NOT NULL,
    ascii_name    text        NOT NULL,
    -- индекс по выражению с normalize_text не строится: с PG17 CREATE INDEX вычисляет выражения
    -- с search_path = pg_catalog, pg_temp, а normalize_text зовёт immutable_unaccent без схемы
    ascii_name_normalized text GENERATED ALWAYS AS (normalize_text(ascii_name)) STORED,
    feature_code  text        NOT NULL,
    population    bigint      NOT NULL DEFAULT 0 CHECK (population >= 0),
    location      geography(Point, 4326) NOT NULL,
    timezone      text        NOT NULL,
    -- пропало из источника, но на него ссылается город (§5.4 шаг 4)
    missing_since timestamptz,
    import_id     uuid        NOT NULL REFERENCES geonames_imports (id)
);

-- «накрывающее» место: кандидаты в радиусе 30 км (§4.3)
CREATE INDEX geonames_places_location_idx ON geonames_places USING gist (location);
-- поиск по префиксу ascii-названия в админке
CREATE INDEX geonames_places_ascii_idx ON geonames_places (country_code, ascii_name_normalized text_pattern_ops);
CREATE INDEX geonames_places_import_idx ON geonames_places (import_id);

CREATE TABLE geonames_place_names (
    geoname_id      bigint NOT NULL REFERENCES geonames_places (geoname_id) ON DELETE CASCADE,
    locale          text   NOT NULL CHECK (locale ~ '^[a-z]{2}$'),
    name            text   NOT NULL,
    name_normalized text GENERATED ALWAYS AS (normalize_text(name)) STORED,
    PRIMARY KEY (geoname_id, locale)
);

-- поиск места по префиксу названия на любом языке
CREATE INDEX geonames_place_names_normalized_idx ON geonames_place_names (name_normalized text_pattern_ops);

CREATE TABLE geonames_admin1 (
    country_code char(2) NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    admin1_code  text    NOT NULL,
    geoname_id   bigint  NOT NULL,  -- 4-я колонка admin1CodesASCII.txt: по нему выбираются названия (§3.4)
    ascii_name   text    NOT NULL,
    PRIMARY KEY (country_code, admin1_code)
);

CREATE TABLE geonames_admin1_names (
    country_code char(2) NOT NULL,
    admin1_code  text    NOT NULL,
    locale       text    NOT NULL CHECK (locale ~ '^[a-z]{2}$'),
    name         text    NOT NULL,
    PRIMARY KEY (country_code, admin1_code, locale),
    FOREIGN KEY (country_code, admin1_code) REFERENCES geonames_admin1 (country_code, admin1_code) ON DELETE CASCADE
);

-- ---------------------------------------------------------------------------
-- Справочник (§3.2)
-- ---------------------------------------------------------------------------

-- city_settings заменяется geo_read_city_settings (§3.7, правило <модуль>_read_*)
DROP VIEW city_settings;

ALTER TABLE cities
    -- идентичность города — место GeoNames; без FK: источник перезагружаем, город — нет
    ADD COLUMN geoname_id bigint CONSTRAINT cities_geoname_id_key UNIQUE,
    -- фиксируется при активации; импорт у заведённых городов её не меняет
    ADD COLUMN population bigint NOT NULL DEFAULT 0 CONSTRAINT cities_population_check CHECK (population >= 0),
    -- версия агрегата для событий (§6)
    ADD COLUMN version    bigint NOT NULL DEFAULT 1 CONSTRAINT cities_version_check CHECK (version > 0),
    -- slug — ссылка веба: [a-z0-9], дефис только между частями, до 100 символов (maxLength контракта)
    ADD CONSTRAINT cities_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 100);

-- Тёзки: в GeoNames 269 тёзок в России, 41 — внутри одного региона (Михайловск 70 и 71,
-- Заречный ×5), а normalize_text даёт '' для письменностей кроме латиницы и кириллицы.
-- Идентичность города — geoname_id; по имени — только неуникальный индекс для поиска.
DROP INDEX cities_country_name_key;
CREATE INDEX cities_country_name_idx ON cities (country_id, name_normalized);
-- ближайший открытый город: ORDER BY centroid <-> point (§4.3)
CREATE INDEX cities_centroid_idx ON cities USING gist (centroid);

ALTER TABLE regions ADD COLUMN geoname_admin1_code text;
CREATE UNIQUE INDEX regions_country_admin1_key ON regions (country_id, geoname_admin1_code)
    WHERE geoname_admin1_code IS NOT NULL;

ALTER TABLE countries
    ADD COLUMN version bigint NOT NULL DEFAULT 1 CONSTRAINT countries_version_check CHECK (version > 0);

-- Районы не удаляются: на них ссылаются pitches.district_id и challenges.district_id
-- (ON DELETE SET NULL), а читать чужие таблицы geo не может. Архивный район скрыт из
-- публичных ответов, его имя свободно.
ALTER TABLE districts
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN version     bigint NOT NULL DEFAULT 1 CONSTRAINT districts_version_check CHECK (version > 0);
DROP INDEX districts_city_name_key;
CREATE UNIQUE INDEX districts_city_name_active_key ON districts (city_id, name_normalized)
    WHERE archived_at IS NULL;

-- старые slug продолжают находить город (ссылки веба)
CREATE TABLE city_slug_history (
    slug        text        PRIMARY KEY CONSTRAINT city_slug_history_slug_format
                                CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND char_length(slug) <= 100),
    city_id     uuid        NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    replaced_at timestamptz NOT NULL -- время — из clock.Clock записывающего (geo 2/2), без DEFAULT now() (R13)
);

CREATE INDEX city_slug_history_city_idx ON city_slug_history (city_id);

-- ---------------------------------------------------------------------------
-- Названия по локалям (§3.3). Строка на языке страны — источник канонического name:
-- правка этой строки в той же транзакции переписывает cities.name (regions.name,
-- countries.name). Импорт заполняет только отсутствующие строки.
-- ---------------------------------------------------------------------------

CREATE TABLE country_names (
    country_id uuid NOT NULL REFERENCES countries (id) ON DELETE CASCADE,
    locale     text NOT NULL CHECK (locale ~ '^[a-z]{2}$'),
    name       text NOT NULL CHECK (btrim(name) <> ''),
    PRIMARY KEY (country_id, locale)
);

CREATE TABLE region_names (
    region_id uuid NOT NULL REFERENCES regions (id) ON DELETE CASCADE,
    locale    text NOT NULL CHECK (locale ~ '^[a-z]{2}$'),
    name      text NOT NULL CHECK (btrim(name) <> ''),
    PRIMARY KEY (region_id, locale)
);

CREATE TABLE city_names (
    city_id         uuid NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    locale          text NOT NULL CHECK (locale ~ '^[a-z]{2}$'),
    name            text NOT NULL CHECK (btrim(name) <> ''),
    name_normalized text GENERATED ALWAYS AS (normalize_text(name)) STORED,
    PRIMARY KEY (city_id, locale)
);

-- поиск города по префиксу названия на любом поддерживаемом языке (listCities q)
CREATE INDEX city_names_normalized_idx ON city_names (name_normalized text_pattern_ops);

-- ---------------------------------------------------------------------------
-- Минимальная версия приложения (§3.5) — механизм платформы. min ≤ recommended и хост
-- стора по платформе проверяет код (semver строкой не сравнивается).
-- ---------------------------------------------------------------------------

CREATE TABLE app_versions (
    platform            text        PRIMARY KEY CHECK (platform IN ('ios', 'android')),
    min_version         text        NOT NULL CHECK (min_version ~ '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$'),
    recommended_version text        NOT NULL CHECK (recommended_version ~ '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$'),
    store_url           text        NOT NULL CHECK (store_url ~ '^https://'),
    version             bigint      NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER app_versions_updated_at
    BEFORE UPDATE ON app_versions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Данные (§3.6), проверены по GeoNames 02.10.2026. Трогаются только строки сида 0010;
-- новых городов миграция не заводит.
-- ---------------------------------------------------------------------------

UPDATE regions r
   SET geoname_admin1_code = '70'
  FROM countries co
 WHERE co.id = r.country_id AND co.code = 'RU' AND r.name = 'Ставропольский край';

-- ST_MakePoint(долгота, широта)
UPDATE cities c
   SET geoname_id = v.geoname_id,
       population = v.population,
       centroid   = ST_SetSRID(ST_MakePoint(v.lon, v.lat), 4326)::geography
  FROM (VALUES ('stavropol',  487846::bigint, 45.03442::float8, 41.9642::float8,  433931::bigint),
               ('mihaylovsk', 493702::bigint, 45.13097::float8, 42.02703::float8, 59198::bigint))
       AS v (slug, geoname_id, lat, lon, population),
       countries co
 WHERE c.slug = v.slug AND co.id = c.country_id AND co.code = 'RU';

-- строки на языке страны — из канонического name
INSERT INTO country_names (country_id, locale, name)
SELECT id, default_locale, name FROM countries;

INSERT INTO region_names (region_id, locale, name)
SELECT r.id, co.default_locale, r.name
  FROM regions r JOIN countries co ON co.id = r.country_id;

INSERT INTO city_names (city_id, locale, name)
SELECT c.id, co.default_locale, c.name
  FROM cities c JOIN countries co ON co.id = c.country_id;

-- en-строки сида
INSERT INTO country_names (country_id, locale, name)
SELECT id, 'en', 'Russia' FROM countries WHERE code = 'RU'
ON CONFLICT DO NOTHING;

INSERT INTO region_names (region_id, locale, name)
SELECT r.id, 'en', 'Stavropol Krai'
  FROM regions r JOIN countries co ON co.id = r.country_id
 WHERE co.code = 'RU' AND r.geoname_admin1_code = '70'
ON CONFLICT DO NOTHING;

INSERT INTO city_names (city_id, locale, name)
SELECT c.id, 'en', v.name
  FROM (VALUES ('stavropol', 'Stavropol'), ('mihaylovsk', 'Mikhaylovsk')) AS v (slug, name)
  JOIN cities c ON c.slug = v.slug
  JOIN countries co ON co.id = c.country_id AND co.code = 'RU'
ON CONFLICT DO NOTHING;

-- Точка обязательна. Другие города без точки (не чистая база и не стенд) — понятный отказ
-- со списком, а не общее «column contains null values». Транзакция миграции откатится целиком.
-- +goose StatementBegin
DO $$
DECLARE
  missing text;
BEGIN
  SELECT string_agg(slug, ', ' ORDER BY slug) INTO missing FROM cities WHERE centroid IS NULL;
  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'города без centroid: % — заполните точки (см. спеку geo §3.6)', missing;
  END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE cities ALTER COLUMN centroid SET NOT NULL;

-- ---------------------------------------------------------------------------
-- Экспортированное представление (§3.7, спека бэкенда §4.2): массовое чтение настроек
-- города другими модулями. Удалить или переименовать колонку — ломающее изменение API модуля.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE VIEW geo_read_city_settings AS
SELECT
    c.id              AS city_id,
    c.name            AS city_name,
    c.slug,
    c.timezone,
    c.status,
    c.centroid,
    co.id             AS country_id,
    co.code           AS country_code,
    co.is_enabled     AS country_enabled,
    co.currency,
    co.default_locale,
    co.phone_prefix,
    co.week_starts_on,
    co.min_signup_age,
    co.age_of_majority
FROM cities c
JOIN countries co ON co.id = c.country_id;
-- +goose StatementEnd

-- +goose Down

-- Возвращает схему 0002 в точности. Упадёт на построении уникальных индексов, если после 0019
-- заведены тёзки в одной стране или архивный район делит имя с активным (README).
DROP VIEW geo_read_city_settings;
DROP TABLE app_versions;
DROP TABLE city_names;
DROP TABLE region_names;
DROP TABLE country_names;
DROP TABLE city_slug_history;
DROP TABLE geonames_admin1_names;
DROP TABLE geonames_admin1;
DROP TABLE geonames_place_names;
DROP TABLE geonames_places;
DROP TABLE geonames_imports;

DROP INDEX districts_city_name_active_key;
ALTER TABLE districts
    DROP COLUMN version,
    DROP COLUMN archived_at;
CREATE UNIQUE INDEX districts_city_name_key ON districts (city_id, name_normalized);

ALTER TABLE countries DROP COLUMN version;

DROP INDEX regions_country_admin1_key;
ALTER TABLE regions DROP COLUMN geoname_admin1_code;

DROP INDEX cities_centroid_idx;
DROP INDEX cities_country_name_idx;
ALTER TABLE cities
    ALTER COLUMN centroid DROP NOT NULL,
    DROP CONSTRAINT cities_slug_format,
    DROP COLUMN version,
    DROP COLUMN population,
    DROP COLUMN geoname_id;
CREATE UNIQUE INDEX cities_country_name_key ON cities (country_id, name_normalized);

-- +goose StatementBegin
CREATE VIEW city_settings AS
SELECT
    c.id              AS city_id,
    c.name            AS city_name,
    c.timezone,
    c.status,
    co.id             AS country_id,
    co.code           AS country_code,
    co.currency,
    co.default_locale,
    co.min_signup_age,
    co.age_of_majority
FROM cities c
JOIN countries co ON co.id = c.country_id;
-- +goose StatementEnd
