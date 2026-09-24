-- 0003_identity.sql
-- Пользователи, сессии, роли и заявки на капитанство.
--
-- Ключевое решение: роль — это не колонка в users, а запись со скоупом.
-- Иначе первая же задача «капитан в одной команде и обычный игрок в другой»
-- ломает всю модель.

-- +goose Up

CREATE TYPE player_position AS ENUM ('GK', 'DEF', 'MID', 'FWD', 'ANY');

CREATE TYPE skill_level AS ENUM (
    'novice', 'novice_plus',
    'amateur', 'amateur_plus',
    'experienced', 'experienced_plus',
    'semi_pro'
);

CREATE TYPE user_status AS ENUM ('active', 'suspended', 'banned', 'deleted');

CREATE TABLE users (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    email               citext,
    email_verified_at   timestamptz,
    phone               text,
    phone_verified_at   timestamptz,

    nickname            text        NOT NULL,
    nickname_normalized text GENERATED ALWAYS AS (normalize_text(nickname)) STORED,
    display_name        text,

    birth_date          date        NOT NULL,
    city_id             uuid        REFERENCES cities (id) ON DELETE SET NULL,

    primary_position    player_position NOT NULL DEFAULT 'ANY',
    secondary_position  player_position,
    -- Заявленный уровень ставит сам игрок; подтверждённый считается из оценок
    -- соперников и становится основным после 10 матчей.
    claimed_skill       skill_level NOT NULL DEFAULT 'amateur',
    confirmed_skill     skill_level,

    preferred_foot      text,
    height_cm           smallint,
    shirt_number        smallint,
    favourite_club      text,
    motto               text,

    locale              text        NOT NULL DEFAULT 'ru',
    timezone            text,

    avatar_item_id      uuid,
    status              user_status NOT NULL DEFAULT 'active',

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz,

    CONSTRAINT users_contact_present CHECK (email IS NOT NULL OR phone IS NOT NULL),
    CONSTRAINT users_foot CHECK (preferred_foot IS NULL OR preferred_foot IN ('left','right','both')),
    CONSTRAINT users_shirt CHECK (shirt_number IS NULL OR shirt_number BETWEEN 1 AND 99)
);

-- Ник уникален глобально. Удалённые аккаунты не блокируют ник навсегда,
-- но карантин на 90 дней держится отдельной таблицей ниже.
CREATE UNIQUE INDEX users_nickname_key ON users (nickname_normalized) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX users_email_key    ON users (email) WHERE email IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX users_phone_key    ON users (phone) WHERE phone IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX users_city_idx ON users (city_id) WHERE deleted_at IS NULL;

CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Возраст считается на дату, а не хранится. is_minor — это функция от даты рождения
-- и совершеннолетия страны, поэтому переключается сам в день 18-летия.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION age_years(birth date, on_date date DEFAULT current_date)
RETURNS int
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT date_part('year', age(on_date, birth))::int
$$;
-- +goose StatementEnd

-- Карантин освободившихся ников: 90 дней после смены или удаления аккаунта,
-- чтобы никто не перехватил чужую репутацию.
CREATE TABLE nickname_reservations (
    nickname_normalized text PRIMARY KEY,
    previous_user_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    released_at         timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL DEFAULT now() + interval '90 days'
);

CREATE INDEX nickname_reservations_expiry_idx ON nickname_reservations (expires_at);

-- История смен ника: смена не чаще раза в 30 дней.
CREATE TABLE nickname_changes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    old_value  text        NOT NULL,
    new_value  text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX nickname_changes_user_idx ON nickname_changes (user_id, created_at DESC);

-- Служебные и зарезервированные ники. Заполняется до запуска.
CREATE TABLE reserved_nicknames (
    nickname_normalized text PRIMARY KEY,
    reason              text NOT NULL DEFAULT 'system'
);

-- ---------------------------------------------------------------------------
-- Сессии и устройства
-- ---------------------------------------------------------------------------

CREATE TABLE sessions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Храним только хэш refresh-токена.
    refresh_hash   text        NOT NULL,
    -- Семья токенов: при повторном использовании старого refresh отзывается вся семья.
    family_id      uuid        NOT NULL,
    parent_id      uuid        REFERENCES sessions (id) ON DELETE SET NULL,
    device_name    text,
    device_os      text,
    ip             inet,
    user_agent     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_seen_at   timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    revoked_at     timestamptz,
    revoked_reason text
);

CREATE UNIQUE INDEX sessions_refresh_hash_key ON sessions (refresh_hash);
CREATE INDEX sessions_user_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_family_idx ON sessions (family_id);

CREATE TABLE push_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider   text        NOT NULL,
    token      text        NOT NULL,
    locale     text,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT push_tokens_provider CHECK (provider IN ('fcm', 'apns', 'web'))
);

CREATE UNIQUE INDEX push_tokens_token_key ON push_tokens (provider, token);
CREATE INDEX push_tokens_user_idx ON push_tokens (user_id) WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Роли со скоупом
-- ---------------------------------------------------------------------------

CREATE TYPE role_name AS ENUM ('player', 'captain', 'vice_captain', 'city_moderator', 'admin');

CREATE TYPE role_scope AS ENUM ('global', 'country', 'city', 'team');

CREATE TABLE role_assignments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        role_name   NOT NULL,
    scope_type  role_scope  NOT NULL,
    -- NULL только для scope_type = 'global'
    scope_id    uuid,
    granted_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    granted_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz,

    CONSTRAINT role_scope_id_present
        CHECK ((scope_type = 'global' AND scope_id IS NULL)
            OR (scope_type <> 'global' AND scope_id IS NOT NULL))
);

CREATE UNIQUE INDEX role_assignments_unique
    ON role_assignments (user_id, role, scope_type, COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE revoked_at IS NULL;

CREATE INDEX role_assignments_scope_idx ON role_assignments (scope_type, scope_id) WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Заявки на капитанство
-- ---------------------------------------------------------------------------

CREATE TYPE application_status AS ENUM ('pending', 'approved', 'rejected', 'withdrawn');

CREATE TABLE captain_applications (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    city_id          uuid        NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    proposed_format  text,
    proposed_pitch   text,
    play_frequency   text,
    contact_link     text,
    note             text,
    status           application_status NOT NULL DEFAULT 'pending',
    -- Испытательный капитан ведёт команду полноценно, но его протоколы
    -- не идут в городские рейтинги до трёх сыгранных матчей.
    is_probationary  boolean     NOT NULL DEFAULT true,
    reviewed_by      uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at      timestamptz,
    rejection_reason text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Одна открытая заявка на город.
CREATE UNIQUE INDEX captain_applications_open_key
    ON captain_applications (user_id, city_id) WHERE status = 'pending';
CREATE INDEX captain_applications_queue_idx
    ON captain_applications (city_id, created_at) WHERE status = 'pending';

CREATE TRIGGER captain_applications_updated_at
    BEFORE UPDATE ON captain_applications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down

DROP TABLE IF EXISTS captain_applications;
DROP TYPE IF EXISTS application_status;
DROP TABLE IF EXISTS role_assignments;
DROP TYPE IF EXISTS role_scope;
DROP TYPE IF EXISTS role_name;
DROP TABLE IF EXISTS push_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS reserved_nicknames;
DROP TABLE IF EXISTS nickname_changes;
DROP TABLE IF EXISTS nickname_reservations;
DROP FUNCTION IF EXISTS age_years(date, date);
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS user_status;
DROP TYPE IF EXISTS skill_level;
DROP TYPE IF EXISTS player_position;
