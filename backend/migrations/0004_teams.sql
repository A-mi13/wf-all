-- 0004_teams.sql
-- Команды, составы, заявки на вступление.
--
-- Правила, зашитые в схему:
--   * одна команда на капитана в пределах города;
--   * название уникально внутри города, а не глобально («Быки Ставрополь» и «Быки Москва»);
--   * история членства не удаляется никогда — на ней держится статистика игрока;
--   * возрастные пороги команды задаёт капитан.

-- +goose Up

CREATE TYPE match_format AS ENUM ('5x5', '6x6', '7x7', '8x8', '9x9', '11x11');

CREATE TYPE team_status AS ENUM ('active', 'paused', 'archived');

CREATE TABLE teams (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id          uuid        NOT NULL REFERENCES cities (id) ON DELETE RESTRICT,

    name             text        NOT NULL,
    name_normalized  text GENERATED ALWAYS AS (normalize_text(name)) STORED,
    -- «Быки» + город → «Быки Ставрополь». Генерится приложением при создании.
    display_name     text        NOT NULL,
    slug             text        NOT NULL,

    captain_id       uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    vice_captain_id  uuid        REFERENCES users (id) ON DELETE SET NULL,

    -- Основной формат. Команда может играть и в других — см. matches.format.
    primary_format   match_format NOT NULL,
    roster_limit     smallint     NOT NULL,

    -- Возрастные пороги команды. Наследуются матчем по умолчанию.
    min_age          smallint,
    max_age          smallint,

    crest_item_id    uuid,
    colors           jsonb,
    description      text,

    status           team_status NOT NULL DEFAULT 'active',
    -- Пока капитан испытательный, протоколы команды не идут в городские рейтинги.
    is_probationary  boolean     NOT NULL DEFAULT true,

    renamed_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,

    CONSTRAINT teams_roster_limit  CHECK (roster_limit BETWEEN 5 AND 40),
    CONSTRAINT teams_age_order     CHECK (min_age IS NULL OR max_age IS NULL OR min_age <= max_age),
    CONSTRAINT teams_min_age_floor CHECK (min_age IS NULL OR min_age >= 14),
    CONSTRAINT teams_vice_not_captain CHECK (vice_captain_id IS NULL OR vice_captain_id <> captain_id)
);

CREATE UNIQUE INDEX teams_slug_key ON teams (slug);
-- Название уникально внутри города.
CREATE UNIQUE INDEX teams_city_name_key
    ON teams (city_id, name_normalized) WHERE deleted_at IS NULL;
-- Один капитан — одна команда в пределах города.
CREATE UNIQUE INDEX teams_one_per_captain_per_city
    ON teams (captain_id, city_id) WHERE deleted_at IS NULL AND status <> 'archived';
CREATE INDEX teams_city_idx ON teams (city_id) WHERE deleted_at IS NULL;

CREATE TRIGGER teams_updated_at
    BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Старые slug'и после переименования: ссылки не должны умирать.
CREATE TABLE team_slug_history (
    slug       text PRIMARY KEY,
    team_id    uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Состав
-- ---------------------------------------------------------------------------

CREATE TYPE membership_status AS ENUM ('pending', 'active', 'left', 'removed');

CREATE TABLE team_members (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status      membership_status NOT NULL DEFAULT 'pending',
    -- Позицию внутри команды может закрепить капитан.
    position    player_position,
    shirt_number smallint,
    joined_at   timestamptz,
    left_at     timestamptz,
    removed_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    remove_reason text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Игрок может входить в команду только один раз одновременно,
-- но история прошлых членств сохраняется.
CREATE UNIQUE INDEX team_members_active_key
    ON team_members (team_id, user_id) WHERE status IN ('pending', 'active');
CREATE INDEX team_members_user_idx ON team_members (user_id, status);
CREATE INDEX team_members_team_idx ON team_members (team_id, status);
CREATE UNIQUE INDEX team_members_shirt_key
    ON team_members (team_id, shirt_number)
    WHERE status = 'active' AND shirt_number IS NOT NULL;

CREATE TRIGGER team_members_updated_at
    BEFORE UPDATE ON team_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Заявки и приглашения
-- ---------------------------------------------------------------------------

CREATE TYPE join_request_kind AS ENUM ('request', 'invite');

CREATE TABLE team_join_requests (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id      uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         join_request_kind NOT NULL,
    message      text,
    status       application_status NOT NULL DEFAULT 'pending',
    decided_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL DEFAULT now() + interval '30 days'
);

CREATE UNIQUE INDEX team_join_requests_open_key
    ON team_join_requests (team_id, user_id) WHERE status = 'pending';
CREATE INDEX team_join_requests_queue_idx
    ON team_join_requests (team_id, created_at) WHERE status = 'pending';

-- Передача капитанства: инициирует капитан, подтверждает принимающий.
CREATE TABLE captaincy_transfers (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    from_user_id uuid       NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    to_user_id  uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- 'captain' — обычная передача, 'moderator' — капитан пропал на 60 дней
    initiated_by text       NOT NULL DEFAULT 'captain',
    status      application_status NOT NULL DEFAULT 'pending',
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz,
    CONSTRAINT captaincy_transfers_initiator CHECK (initiated_by IN ('captain', 'moderator'))
);

CREATE UNIQUE INDEX captaincy_transfers_open_key
    ON captaincy_transfers (team_id) WHERE status = 'pending';

-- +goose Down

DROP TABLE IF EXISTS captaincy_transfers;
DROP TABLE IF EXISTS team_join_requests;
DROP TYPE IF EXISTS join_request_kind;
DROP TABLE IF EXISTS team_members;
DROP TYPE IF EXISTS membership_status;
DROP TABLE IF EXISTS team_slug_history;
DROP TABLE IF EXISTS teams;
DROP TYPE IF EXISTS team_status;
DROP TYPE IF EXISTS match_format;
