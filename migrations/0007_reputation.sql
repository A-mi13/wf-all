-- 0007_reputation.sql
-- Надёжность игрока, паспорт команды, метки неплательщиков, запросы на сброс.
--
-- Всё считается ночью в снапшоты. Агрегат по всей истории на каждый открытый
-- экран — первое, что ляжет при росте.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Надёжность игрока: главный вопрос капитана — не «как играет», а «приедет ли»
-- ---------------------------------------------------------------------------

CREATE TABLE reliability_snapshots (
    user_id            uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- Окно: последние 20 матчей
    window_matches     smallint NOT NULL DEFAULT 0,
    confirmed_count    smallint NOT NULL DEFAULT 0,
    attended_count     smallint NOT NULL DEFAULT 0,
    -- Отклики против реальных выходов
    requests_count     smallint NOT NULL DEFAULT 0,
    played_count       smallint NOT NULL DEFAULT 0,
    late_cancels       smallint NOT NULL DEFAULT 0,
    -- Опоздания считаются по последним 10 матчам
    late_arrivals      smallint NOT NULL DEFAULT 0,
    current_streak     smallint NOT NULL DEFAULT 0,
    avg_response_minutes int,
    -- Пока матчей меньше 5, в интерфейсе бейдж «Новичок» без цифр,
    -- иначе человека никто никуда не возьмёт.
    is_newcomer        boolean  NOT NULL DEFAULT true,
    computed_at        timestamptz NOT NULL DEFAULT now()
);

-- Запрос на сброс: после 10 матчей без нарушений человек может начать заново.
-- Не чаще раза в год — иначе сброс станет способом прятать неявки.
CREATE TABLE reliability_reset_requests (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status      application_status NOT NULL DEFAULT 'pending',
    decided_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX reliability_reset_open_key
    ON reliability_reset_requests (user_id) WHERE status = 'pending';
CREATE INDEX reliability_reset_history_idx
    ON reliability_reset_requests (user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Паспорт команды: «собираются 9 из 10» или «играют раз в месяц»
-- ---------------------------------------------------------------------------

CREATE TABLE team_stats_daily (
    team_id            uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    stat_date          date        NOT NULL,
    format             match_format NOT NULL,

    -- Скользящее окно 90 дней
    matches_played     smallint    NOT NULL DEFAULT 0,
    matches_created    smallint    NOT NULL DEFAULT 0,
    matches_cancelled  smallint    NOT NULL DEFAULT 0,
    reschedules        smallint    NOT NULL DEFAULT 0,
    matches_per_month  numeric(5,2),

    -- Медиана «пришло / подтвердило»
    median_attendance  numeric(5,2),
    -- Средний процент закрытых слотов к старту
    avg_fill_rate      numeric(5,2),
    -- Сколько человек сыграли 5+ матчей подряд
    core_players       smallint    NOT NULL DEFAULT 0,

    typical_weekday    smallint,
    typical_hour       smallint,
    median_share_minor bigint,
    currency           char(3),

    wins               smallint    NOT NULL DEFAULT 0,
    draws              smallint    NOT NULL DEFAULT 0,
    losses             smallint    NOT NULL DEFAULT 0,
    goals_for          smallint    NOT NULL DEFAULT 0,
    goals_against      smallint    NOT NULL DEFAULT 0,

    -- Цифры показываются только после 5 сыгранных матчей, иначе одна отмена
    -- из одного матча даёт «100% отмен» и убивает команду в день создания.
    is_established     boolean     NOT NULL DEFAULT false,

    computed_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, stat_date, format)
);

CREATE INDEX team_stats_daily_latest_idx ON team_stats_daily (team_id, stat_date DESC);

-- Сброс командной статистики при смене капитана. Не стирается бесследно:
-- в паспорте остаётся пометка «статистика с такой-то даты».
CREATE TABLE team_stat_resets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    requested_by uuid       NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    reason      text        NOT NULL,
    status      application_status NOT NULL DEFAULT 'pending',
    decided_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at  timestamptz,
    -- С этой даты считается статистика после одобрения.
    effective_from date,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX team_stat_resets_open_key
    ON team_stat_resets (team_id) WHERE status = 'pending';

-- ---------------------------------------------------------------------------
-- Рейтинги игрока и команды
-- ---------------------------------------------------------------------------

CREATE TABLE player_stats_daily (
    user_id          uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    stat_date        date        NOT NULL,
    format           match_format NOT NULL,
    matches          int         NOT NULL DEFAULT 0,
    goals            int         NOT NULL DEFAULT 0,
    assists          int         NOT NULL DEFAULT 0,
    wins             int         NOT NULL DEFAULT 0,
    draws            int         NOT NULL DEFAULT 0,
    losses           int         NOT NULL DEFAULT 0,
    gk_minutes       int         NOT NULL DEFAULT 0,
    mvp_awards       int         NOT NULL DEFAULT 0,
    avg_rating       numeric(3,2),
    -- Вес статистики в городских таблицах. Падает, если последние 20 матчей
    -- сыграны с одними и теми же четырьмя людьми.
    diversity_weight numeric(3,2) NOT NULL DEFAULT 1.00,
    computed_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, stat_date, format)
);

CREATE INDEX player_stats_daily_latest_idx ON player_stats_daily (user_id, stat_date DESC);

-- Спортивный рейтинг команды (Elo) и надёжность — это две разные цифры,
-- их нельзя смешивать: слабая, но надёжная команда — отличный вариант для новичка.
CREATE TABLE team_ratings (
    team_id       uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    format        match_format NOT NULL,
    elo           int         NOT NULL DEFAULT 1200,
    reliability   numeric(5,2),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, format)
);

-- ---------------------------------------------------------------------------
-- Реестр оплат и метки неплательщиков
--
-- Платформа не сторона расчётов и не подтверждает факт долга. Метка — это
-- сигнал «несколько капитанов отметили проблему», без сумм.
-- ---------------------------------------------------------------------------

CREATE TABLE payment_notes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    match_id     uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    team_id      uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    marked_by    uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- true — не рассчитался
    unpaid       boolean     NOT NULL DEFAULT true,
    cleared_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (match_id, user_id)
);

CREATE INDEX payment_notes_user_idx ON payment_notes (user_id, created_at DESC) WHERE unpaid;

CREATE TABLE payment_flags (
    user_id          uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- Сколько разных капитанов из разных команд отметили проблему
    distinct_markers smallint    NOT NULL DEFAULT 0,
    -- Публичной метка становится только от двух разных капитанов
    is_public        boolean     NOT NULL DEFAULT false,
    first_marked_at  timestamptz,
    last_marked_at   timestamptz,
    expires_at       timestamptz,
    disputed_at      timestamptz,
    computed_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payment_flags_public_requires_two CHECK (NOT is_public OR distinct_markers >= 2)
);

CREATE INDEX payment_flags_expiry_idx ON payment_flags (expires_at) WHERE is_public;

-- Оспаривание метки: разбирает модератор города.
CREATE TABLE payment_flag_disputes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    note_id    uuid        REFERENCES payment_notes (id) ON DELETE SET NULL,
    reason     text        NOT NULL,
    status     application_status NOT NULL DEFAULT 'pending',
    decided_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payment_flag_disputes_queue_idx ON payment_flag_disputes (created_at) WHERE status = 'pending';

-- +goose Down

DROP TABLE IF EXISTS payment_flag_disputes;
DROP TABLE IF EXISTS payment_flags;
DROP TABLE IF EXISTS payment_notes;
DROP TABLE IF EXISTS team_ratings;
DROP TABLE IF EXISTS player_stats_daily;
DROP TABLE IF EXISTS team_stat_resets;
DROP TABLE IF EXISTS team_stats_daily;
DROP TABLE IF EXISTS reliability_reset_requests;
DROP TABLE IF EXISTS reliability_snapshots;
