-- 0005_pitches.sql
-- Поля. На MVP добавляют капитаны вручную, подтверждает модератор города.
-- Интеграция с букингом невозможна: большинство объектов не публикуют даже цены.
--
-- Если на вашем хостинге нет PostGIS: замените geography(Point, 4326) на пару
-- double precision lat/lon, а поиск дублей — на bounding box по широте/долготе.

-- +goose Up

CREATE TYPE pitch_surface AS ENUM ('artificial', 'natural', 'ground', 'parquet', 'asphalt', 'sand');

CREATE TYPE pitch_kind AS ENUM ('outdoor', 'indoor', 'covered');

CREATE TYPE moderation_status AS ENUM ('pending', 'approved', 'rejected', 'merged');

CREATE TABLE pitches (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id        uuid        NOT NULL REFERENCES cities (id) ON DELETE RESTRICT,
    district_id    uuid        REFERENCES districts (id) ON DELETE SET NULL,

    name           text        NOT NULL,
    address        text        NOT NULL,
    location       geography(Point, 4326) NOT NULL,

    surface        pitch_surface NOT NULL,
    kind           pitch_kind    NOT NULL,
    formats        match_format[] NOT NULL DEFAULT '{}',

    has_lighting   boolean     NOT NULL DEFAULT false,
    has_changing_rooms boolean NOT NULL DEFAULT false,
    has_parking    boolean     NOT NULL DEFAULT false,

    -- Дворовая коробка: бронировать нечего, контроль слотов работает
    -- как предупреждение, а не как запрет.
    is_bookable    boolean     NOT NULL DEFAULT true,
    is_free        boolean     NOT NULL DEFAULT false,
    -- Справочная цена за час в минорных единицах валюты страны.
    typical_price_minor bigint,
    currency       char(3),

    admin_phone    text,
    admin_note     text,
    photo_urls     text[]      NOT NULL DEFAULT '{}',

    submitted_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    status         moderation_status NOT NULL DEFAULT 'pending',
    reviewed_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at    timestamptz,
    reject_reason  text,
    -- Заполняется при слиянии дублей: все матчи перевешиваются на основное поле.
    merged_into_id uuid        REFERENCES pitches (id) ON DELETE SET NULL,

    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    deleted_at     timestamptz,

    CONSTRAINT pitches_price_currency
        CHECK ((typical_price_minor IS NULL) = (currency IS NULL)),
    CONSTRAINT pitches_price_non_negative
        CHECK (typical_price_minor IS NULL OR typical_price_minor >= 0),
    CONSTRAINT pitches_free_has_no_price
        CHECK (NOT is_free OR typical_price_minor IS NULL)
);

-- Главный гео-индекс: «поля рядом» и поиск дублей в радиусе 150 м.
CREATE INDEX pitches_location_idx ON pitches USING gist (location);
CREATE INDEX pitches_city_idx ON pitches (city_id) WHERE deleted_at IS NULL AND status = 'approved';
CREATE INDEX pitches_moderation_queue_idx ON pitches (city_id, created_at) WHERE status = 'pending';

CREATE TRIGGER pitches_updated_at
    BEFORE UPDATE ON pitches
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Кандидаты в дубли, которые показываются капитану при добавлении поля.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION nearby_pitches(
    p_city_id uuid,
    p_lat double precision,
    p_lon double precision,
    p_radius_m double precision DEFAULT 150
)
RETURNS TABLE (id uuid, name text, address text, distance_m double precision)
LANGUAGE sql STABLE AS $$
  SELECT p.id,
         p.name,
         p.address,
         ST_Distance(p.location, ST_MakePoint(p_lon, p_lat)::geography) AS distance_m
    FROM pitches p
   WHERE p.city_id = p_city_id
     AND p.deleted_at IS NULL
     AND p.status IN ('pending', 'approved')
     AND ST_DWithin(p.location, ST_MakePoint(p_lon, p_lat)::geography, p_radius_m)
   ORDER BY distance_m
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION IF EXISTS nearby_pitches(uuid, double precision, double precision, double precision);
DROP TABLE IF EXISTS pitches;
DROP TYPE IF EXISTS moderation_status;
DROP TYPE IF EXISTS pitch_kind;
DROP TYPE IF EXISTS pitch_surface;
