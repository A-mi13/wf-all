-- 0002_geo.sql
-- География: страна → регион → город → район.
-- Страна — носитель всех правил, которые иначе размажутся по коду:
-- валюта, язык, возрастные пороги, формат телефона, первый день недели.

-- +goose Up

CREATE TABLE countries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ISO 3166-1 alpha-2
    code             char(2)     NOT NULL UNIQUE,
    name             text        NOT NULL,
    -- ISO 4217. Валюта матча берётся отсюда через город и никогда не выбирается руками.
    currency         char(3)     NOT NULL,
    default_locale   text        NOT NULL,
    phone_prefix     text        NOT NULL,
    -- 1 = понедельник, 7 = воскресенье (ISO-8601)
    week_starts_on   smallint    NOT NULL DEFAULT 1,

    -- Минимальный возраст регистрации. В ЕС порог цифрового согласия по GDPR (ст. 8)
    -- плавает от 13 до 16 в зависимости от страны, поэтому это поле, а не константа.
    min_signup_age   smallint    NOT NULL,
    -- Совершеннолетие: капитанство и покупки привязаны к нему, а не к min_signup_age.
    age_of_majority  smallint    NOT NULL,

    is_enabled       boolean     NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT countries_age_order CHECK (min_signup_age <= age_of_majority),
    CONSTRAINT countries_week_day  CHECK (week_starts_on BETWEEN 1 AND 7)
);

CREATE TRIGGER countries_updated_at
    BEFORE UPDATE ON countries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE regions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    country_id uuid        NOT NULL REFERENCES countries (id) ON DELETE RESTRICT,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX regions_country_idx ON regions (country_id);

CREATE TYPE city_status AS ENUM (
    'waitlist',  -- витрины нет, можно оставить заявку на капитана
    'pilot',     -- есть модератор и первые команды
    'live'
);

CREATE TABLE cities (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    country_id     uuid        NOT NULL REFERENCES countries (id) ON DELETE RESTRICT,
    region_id      uuid        REFERENCES regions (id) ON DELETE SET NULL,
    name           text        NOT NULL,
    name_normalized text GENERATED ALWAYS AS (normalize_text(name)) STORED,
    slug           text        NOT NULL,
    -- Таймзона города. Матч «в 20:00» — это время поля, а не телефона игрока,
    -- который может быть в отпуске в другом поясе.
    timezone       text        NOT NULL,
    centroid       geography(Point, 4326),
    status         city_status NOT NULL DEFAULT 'waitlist',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX cities_slug_key ON cities (slug);
CREATE UNIQUE INDEX cities_country_name_key ON cities (country_id, name_normalized);
CREATE INDEX cities_status_idx ON cities (status) WHERE status <> 'waitlist';

CREATE TRIGGER cities_updated_at
    BEFORE UPDATE ON cities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE districts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id         uuid        NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    name            text        NOT NULL,
    name_normalized text GENERATED ALWAYS AS (normalize_text(name)) STORED,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX districts_city_name_key ON districts (city_id, name_normalized);

-- Удобное представление: валюта и возрастные пороги города без джойнов в коде.
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

-- +goose Down

DROP VIEW IF EXISTS city_settings;
DROP TABLE IF EXISTS districts;
DROP TABLE IF EXISTS cities;
DROP TYPE IF EXISTS city_status;
DROP TABLE IF EXISTS regions;
DROP TABLE IF EXISTS countries;
