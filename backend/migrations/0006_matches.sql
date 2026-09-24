-- 0006_matches.sql
-- Матчи, слоты, состав, протоколы, вызовы.
-- Самая важная миграция: здесь живёт EXCLUDE-констрейнт, который делает
-- двойное бронирование поля физически невозможным.

-- +goose Up

CREATE TYPE match_type AS ENUM (
    'friendly',  -- две команды друг против друга, полная статистика
    'pickup',    -- один капитан собирает N слотов, делит стороны на месте
    'training'   -- тренировка своей команды, только посещаемость
);

CREATE TYPE match_visibility AS ENUM (
    'team_only',   -- видят только свои
    'invite_only', -- виден в городе, вход по одобрению капитана
    'public'       -- занимай слот сам
);

CREATE TYPE match_status AS ENUM (
    'draft', 'scheduled', 'confirmed', 'live',
    'awaiting_report', 'completed', 'cancelled', 'expired'
);

CREATE TYPE match_side AS ENUM ('home', 'away');

-- Турниры появляются в v2, но поле competition_id у матча нужно с первого дня:
-- иначе турнир придётся писать как параллельную вселенную.
CREATE TYPE competition_kind AS ENUM ('cup', 'round_robin', 'groups_playoff', 'league');

CREATE TABLE competitions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id     uuid        NOT NULL REFERENCES cities (id) ON DELETE RESTRICT,
    name        text        NOT NULL,
    slug        text        NOT NULL UNIQUE,
    kind        competition_kind NOT NULL,
    format      match_format NOT NULL,
    organizer_id uuid       NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    starts_on   date,
    ends_on     date,
    min_age     smallint,
    max_age     smallint,
    -- Призы только неденежные, пока не пройдена юридическая проверка.
    prize_description text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX competitions_city_idx ON competitions (city_id);

CREATE TABLE competition_teams (
    competition_id uuid NOT NULL REFERENCES competitions (id) ON DELETE CASCADE,
    team_id        uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    seed           smallint,
    joined_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (competition_id, team_id)
);

-- ---------------------------------------------------------------------------
-- Матчи
-- ---------------------------------------------------------------------------

CREATE TABLE matches (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id        uuid        NOT NULL REFERENCES cities (id) ON DELETE RESTRICT,
    pitch_id       uuid        NOT NULL REFERENCES pitches (id) ON DELETE RESTRICT,
    competition_id uuid        REFERENCES competitions (id) ON DELETE SET NULL,

    type           match_type       NOT NULL,
    visibility     match_visibility NOT NULL DEFAULT 'team_only',
    status         match_status     NOT NULL DEFAULT 'draft',
    format         match_format     NOT NULL,

    home_team_id   uuid        NOT NULL REFERENCES teams (id) ON DELETE RESTRICT,
    away_team_id   uuid        REFERENCES teams (id) ON DELETE SET NULL,

    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    -- Занятый интервал с техническим зазором. Заполняется триггером,
    -- потому что timestamptz + interval не IMMUTABLE и в generated-колонку не годится.
    slot           tstzrange   NOT NULL,
    -- Дворовая коробка слот не блокирует — только предупреждает.
    blocks_pitch   boolean     NOT NULL DEFAULT true,

    -- Возрастные пороги матча. По умолчанию наследуются от команды.
    min_age        smallint,
    max_age        smallint,

    -- Деньги за аренду платформа не принимает никогда. Это справочный расчёт.
    is_free        boolean     NOT NULL DEFAULT false,
    rent_total_minor bigint,
    currency       char(3),
    -- Кому отдавать: часто платит не капитан, а тот, кто приехал раньше.
    payee_user_id  uuid        REFERENCES users (id) ON DELETE SET NULL,

    -- Сменный вратарь: по минутам или по числу пропущенных голов.
    gk_rotation           boolean  NOT NULL DEFAULT false,
    gk_rotation_minutes   smallint,
    gk_rotation_goals     smallint,

    slots_total    smallint    NOT NULL,
    created_by     uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    cancelled_at   timestamptz,
    cancel_reason  text,
    reschedule_count smallint  NOT NULL DEFAULT 0,

    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT matches_time_order      CHECK (ends_at > starts_at),
    CONSTRAINT matches_duration_sane   CHECK (ends_at - starts_at BETWEEN interval '30 minutes' AND interval '8 hours'),
    CONSTRAINT matches_age_order       CHECK (min_age IS NULL OR max_age IS NULL OR min_age <= max_age),
    CONSTRAINT matches_price_currency  CHECK ((rent_total_minor IS NULL) = (currency IS NULL)),
    CONSTRAINT matches_free_no_price   CHECK (NOT is_free OR rent_total_minor IS NULL),
    CONSTRAINT matches_paid_has_price  CHECK (is_free OR rent_total_minor IS NOT NULL),
    CONSTRAINT matches_rent_non_negative CHECK (rent_total_minor IS NULL OR rent_total_minor >= 0),
    CONSTRAINT matches_teams_differ    CHECK (away_team_id IS NULL OR away_team_id <> home_team_id),
    CONSTRAINT matches_friendly_has_away CHECK (type <> 'friendly' OR away_team_id IS NOT NULL),
    CONSTRAINT matches_gk_rotation_mode CHECK (
        NOT gk_rotation OR (gk_rotation_minutes IS NOT NULL) <> (gk_rotation_goals IS NOT NULL)
    )
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION matches_set_slot()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  -- 10 минут после свистка: люди должны успеть разойтись.
  NEW.slot := tstzrange(NEW.starts_at, NEW.ends_at + interval '10 minutes', '[)');
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER matches_slot_sync
    BEFORE INSERT OR UPDATE OF starts_at, ends_at ON matches
    FOR EACH ROW EXECUTE FUNCTION matches_set_slot();

CREATE TRIGGER matches_updated_at
    BEFORE UPDATE ON matches
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Главная защита продукта: два капитана не могут занять одно поле на одно время,
-- даже если нажали «Создать» в одну миллисекунду.
ALTER TABLE matches
    ADD CONSTRAINT matches_no_pitch_overlap
    EXCLUDE USING gist (pitch_id WITH =, slot WITH &&)
    WHERE (blocks_pitch AND status IN ('scheduled', 'confirmed', 'live'));

-- Главный запрос всего продукта — лента города на ближайшие дни.
CREATE INDEX matches_city_time_idx
    ON matches (city_id, starts_at)
    WHERE status IN ('scheduled', 'confirmed', 'live');
CREATE INDEX matches_team_idx ON matches (home_team_id, starts_at DESC);
CREATE INDEX matches_away_team_idx ON matches (away_team_id, starts_at DESC) WHERE away_team_id IS NOT NULL;
CREATE INDEX matches_pitch_idx ON matches (pitch_id, starts_at);
CREATE INDEX matches_competition_idx ON matches (competition_id) WHERE competition_id IS NOT NULL;
-- Для воркера, который переводит забытые матчи в expired.
CREATE INDEX matches_awaiting_report_idx ON matches (ends_at) WHERE status = 'awaiting_report';

-- ---------------------------------------------------------------------------
-- Слоты и состав
-- ---------------------------------------------------------------------------

CREATE TABLE match_slots (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id    uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    side        match_side  NOT NULL,
    slot_no     smallint    NOT NULL,
    position    player_position NOT NULL DEFAULT 'ANY',
    user_id     uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- Слот занят по платной брони позиции.
    is_reserved boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX match_slots_key ON match_slots (match_id, side, slot_no);
-- Игрок не может занимать два слота в одном матче.
CREATE UNIQUE INDEX match_slots_one_per_user
    ON match_slots (match_id, user_id) WHERE user_id IS NOT NULL;

CREATE TYPE rsvp_status AS ENUM ('invited', 'requested', 'confirmed', 'declined', 'waitlist', 'kicked');

CREATE TABLE match_participants (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id        uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    team_id         uuid        REFERENCES teams (id) ON DELETE SET NULL,
    side            match_side,
    rsvp            rsvp_status NOT NULL DEFAULT 'invited',
    rsvp_at         timestamptz,
    -- Отметил капитан
    attended        boolean,
    -- Подтвердил сам игрок одним тапом в пуше — нужно для веса статистики
    self_confirmed  boolean     NOT NULL DEFAULT false,
    was_late        boolean     NOT NULL DEFAULT false,
    -- Отмена менее чем за 3 часа до старта считается поздней.
    cancelled_at    timestamptz,
    is_late_cancel  boolean     NOT NULL DEFAULT false,
    -- Доля аренды в минорных единицах. Пересчитывается при каждом изменении состава.
    share_minor     bigint,
    paid            boolean     NOT NULL DEFAULT false,
    paid_marked_at  timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX match_participants_key ON match_participants (match_id, user_id);
CREATE INDEX match_participants_user_idx ON match_participants (user_id, created_at DESC);
CREATE INDEX match_participants_match_idx ON match_participants (match_id, rsvp);

CREATE TRIGGER match_participants_updated_at
    BEFORE UPDATE ON match_participants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Платная бронь позиции
-- ---------------------------------------------------------------------------

CREATE TABLE position_reservations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id      uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    team_id       uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    position      player_position NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- Капитан может снять бронь только если позиций на всех не хватает
    -- и только с указанной причиной.
    revoked_at    timestamptz,
    revoked_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    revoke_reason text,
    compensated   boolean     NOT NULL DEFAULT false,
    CONSTRAINT position_reservations_revoke_reason
        CHECK (revoked_at IS NULL OR revoke_reason IS NOT NULL)
);

CREATE UNIQUE INDEX position_reservations_key ON position_reservations (match_id, user_id);
CREATE INDEX position_reservations_revoked_idx
    ON position_reservations (user_id, team_id, revoked_at) WHERE revoked_at IS NOT NULL;

-- Не больше трёх снятых броней у одного игрока в одной команде за сезон.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION position_reservations_revoke_limit()
RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  revoked_count int;
BEGIN
  IF NEW.revoked_at IS NOT NULL AND (OLD.revoked_at IS NULL) THEN
    SELECT count(*) INTO revoked_count
      FROM position_reservations
     WHERE user_id = NEW.user_id
       AND team_id = NEW.team_id
       AND revoked_at IS NOT NULL
       AND revoked_at > now() - interval '1 year';

    IF revoked_count >= 3 THEN
      RAISE EXCEPTION 'reservation revoke limit reached for this player in this team';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER position_reservations_revoke_guard
    BEFORE UPDATE ON position_reservations
    FOR EACH ROW EXECUTE FUNCTION position_reservations_revoke_limit();

-- ---------------------------------------------------------------------------
-- Протокол
-- ---------------------------------------------------------------------------

CREATE TYPE match_event_type AS ENUM ('goal', 'own_goal', 'assist', 'gk_shift');

CREATE TABLE match_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id        uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    type            match_event_type NOT NULL,
    side            match_side  NOT NULL,
    user_id         uuid        REFERENCES users (id) ON DELETE SET NULL,
    assist_user_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    -- Минуты не спрашиваем: их никто не помнит. Поле есть для турниров.
    minute          smallint,
    gk_minutes      smallint,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT match_events_assist_differs CHECK (assist_user_id IS NULL OR assist_user_id <> user_id)
);

CREATE INDEX match_events_match_idx ON match_events (match_id);
CREATE INDEX match_events_user_idx ON match_events (user_id, created_at DESC) WHERE user_id IS NOT NULL;

CREATE TYPE report_state AS ENUM ('draft', 'submitted', 'confirmed', 'disputed', 'resolved', 'expired');

CREATE TABLE match_reports (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id       uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    home_score     smallint    NOT NULL,
    away_score     smallint    NOT NULL,
    submitted_by   uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    submitted_at   timestamptz NOT NULL DEFAULT now(),
    -- Капитан соперника подтверждает счёт; в pickup-матче окно 24 часа на возражения.
    confirmed_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    confirmed_at   timestamptz,
    state          report_state NOT NULL DEFAULT 'submitted',
    dispute_reason text,
    disputed_at    timestamptz,
    resolved_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    resolved_at    timestamptz,
    -- Учитывается ли матч в рейтингах. Выключается при спорах, накрутке,
    -- испытательном капитане или недоборе состава.
    counts_towards_stats boolean NOT NULL DEFAULT true,
    CONSTRAINT match_reports_scores CHECK (home_score >= 0 AND away_score >= 0)
);

CREATE UNIQUE INDEX match_reports_match_key ON match_reports (match_id);
CREATE INDEX match_reports_disputed_idx ON match_reports (disputed_at) WHERE state = 'disputed';

-- Голосование за лучшего игрока: голосуют все участники, голос за себя не считается.
CREATE TABLE match_votes (
    match_id   uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    voter_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    nominee_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (match_id, voter_id),
    CONSTRAINT match_votes_no_self CHECK (voter_id <> nominee_id)
);

CREATE INDEX match_votes_nominee_idx ON match_votes (match_id, nominee_id);

-- Оценки соперников — источник подтверждённого уровня игрока.
CREATE TABLE match_ratings (
    match_id   uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    rater_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rated_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rating     smallint    NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (match_id, rater_id, rated_id),
    CONSTRAINT match_ratings_range CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT match_ratings_no_self CHECK (rater_id <> rated_id)
);

-- ---------------------------------------------------------------------------
-- Комментарии под матчем: бесплатно, но с лимитом. Мессенджер против Telegram
-- мы не строим.
-- ---------------------------------------------------------------------------

CREATE TABLE match_comments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id   uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT match_comments_body_len CHECK (char_length(body) BETWEEN 1 AND 1000)
);

CREATE INDEX match_comments_match_idx ON match_comments (match_id, created_at);
CREATE INDEX match_comments_rate_idx ON match_comments (match_id, user_id) WHERE deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- Вызовы: самый сильный движок ликвидности. Один капитан поднимает 20 человек.
-- ---------------------------------------------------------------------------

CREATE TYPE challenge_status AS ENUM ('open', 'matched', 'cancelled', 'expired');

CREATE TABLE challenges (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id      uuid        NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    team_id      uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    pitch_id     uuid        REFERENCES pitches (id) ON DELETE SET NULL,
    district_id  uuid        REFERENCES districts (id) ON DELETE SET NULL,
    format       match_format NOT NULL,
    window_from  timestamptz NOT NULL,
    window_to    timestamptz NOT NULL,
    min_age      smallint,
    max_age      smallint,
    share_cost   boolean     NOT NULL DEFAULT true,
    note         text,
    status       challenge_status NOT NULL DEFAULT 'open',
    matched_match_id uuid    REFERENCES matches (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    CONSTRAINT challenges_window CHECK (window_to > window_from)
);

CREATE INDEX challenges_feed_idx ON challenges (city_id, window_from) WHERE status = 'open';

CREATE TABLE challenge_responses (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_id uuid        NOT NULL REFERENCES challenges (id) ON DELETE CASCADE,
    team_id      uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    message      text,
    status       application_status NOT NULL DEFAULT 'pending',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX challenge_responses_key ON challenge_responses (challenge_id, team_id);

-- +goose Down

DROP TABLE IF EXISTS challenge_responses;
DROP TABLE IF EXISTS challenges;
DROP TYPE IF EXISTS challenge_status;
DROP TABLE IF EXISTS match_comments;
DROP TABLE IF EXISTS match_ratings;
DROP TABLE IF EXISTS match_votes;
DROP TABLE IF EXISTS match_reports;
DROP TYPE IF EXISTS report_state;
DROP TABLE IF EXISTS match_events;
DROP TYPE IF EXISTS match_event_type;
DROP TRIGGER IF EXISTS position_reservations_revoke_guard ON position_reservations;
DROP FUNCTION IF EXISTS position_reservations_revoke_limit();
DROP TABLE IF EXISTS position_reservations;
DROP TABLE IF EXISTS match_participants;
DROP TYPE IF EXISTS rsvp_status;
DROP TABLE IF EXISTS match_slots;
DROP TRIGGER IF EXISTS matches_slot_sync ON matches;
DROP FUNCTION IF EXISTS matches_set_slot();
DROP TABLE IF EXISTS matches;
DROP TABLE IF EXISTS competition_teams;
DROP TABLE IF EXISTS competitions;
DROP TYPE IF EXISTS competition_kind;
DROP TYPE IF EXISTS match_side;
DROP TYPE IF EXISTS match_status;
DROP TYPE IF EXISTS match_visibility;
DROP TYPE IF EXISTS match_type;
