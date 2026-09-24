-- 0008_economy.sql
-- Две валюты, подписки, косметика, достижения, прогнозы.
--
-- Главное правило схемы: очки и кредиты — РАЗНЫЕ счета, они не смешиваются
-- никогда и ни при каких акциях. Прогнозы идут только за заработанные очки —
-- это то, что держит механику вне азартных игр. Вывести в деньги нельзя ничего.

-- +goose Up

CREATE TYPE currency_kind AS ENUM (
    'points',  -- зарабатываются: явка, голы, серии, рефералы
    'credits'  -- покупаются или даются с подпиской
);

CREATE TABLE wallets (
    user_id   uuid          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind      currency_kind NOT NULL,
    balance   bigint        NOT NULL DEFAULT 0,
    updated_at timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind),
    CONSTRAINT wallets_non_negative CHECK (balance >= 0)
);

-- Двойная запись: баланс всегда должен сходиться с суммой проводок.
CREATE TABLE ledger_entries (
    id         bigserial PRIMARY KEY,
    user_id    uuid          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       currency_kind NOT NULL,
    delta      bigint        NOT NULL,
    balance_after bigint     NOT NULL,
    reason     text          NOT NULL,
    ref_type   text,
    ref_id     uuid,
    created_at timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT ledger_entries_non_zero CHECK (delta <> 0)
);

CREATE INDEX ledger_entries_user_idx ON ledger_entries (user_id, kind, created_at DESC);
CREATE INDEX ledger_entries_ref_idx ON ledger_entries (ref_type, ref_id) WHERE ref_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Подписки
-- ---------------------------------------------------------------------------

CREATE TYPE subscription_tier AS ENUM ('free', 'player_pro', 'captain_pro');

CREATE TYPE subscription_source AS ENUM ('app_store', 'google_play', 'web', 'grant', 'lootbox');

CREATE TABLE subscriptions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tier          subscription_tier NOT NULL,
    source        subscription_source NOT NULL,
    -- Идентификатор транзакции стора для сверки
    external_id   text,
    starts_at     timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    auto_renew    boolean     NOT NULL DEFAULT false,
    cancelled_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_period CHECK (expires_at > starts_at)
);

-- Одна активная подписка каждого типа на пользователя.
-- Через EXCLUDE, а не через частичный уникальный индекс: now() в предикате
-- индекса недопустим, потому что предикат обязан быть IMMUTABLE.
-- Побочная польза — периоды подписки одного тира не могут пересекаться,
-- то есть двойное списание за один и тот же месяц отловится на уровне БД.
ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_no_overlap
    EXCLUDE USING gist (
        user_id WITH =,
        tier WITH =,
        tstzrange(starts_at, expires_at, '[)') WITH &&
    ) WHERE (cancelled_at IS NULL);

CREATE INDEX subscriptions_user_idx ON subscriptions (user_id, expires_at DESC);
CREATE UNIQUE INDEX subscriptions_external_key ON subscriptions (source, external_id) WHERE external_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Каталог косметики
-- ---------------------------------------------------------------------------

CREATE TYPE item_kind AS ENUM (
    'avatar', 'frame', 'boots', 'kit', 'socks', 'background',
    'team_crest', 'team_colors', 'team_cover', 'rating_animation'
);

CREATE TYPE item_acquisition AS ENUM (
    'default',      -- выдаётся всем
    'points',       -- за заработанные очки
    'credits',      -- за купленную валюту
    'achievement',  -- только за достижение, не продаётся
    'subscription'  -- доступно, пока активна подписка
);

CREATE TABLE catalog_items (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind          item_kind   NOT NULL,
    code          text        NOT NULL UNIQUE,
    name          text        NOT NULL,
    description   text,
    asset_url     text,
    acquisition   item_acquisition NOT NULL,
    price_points  bigint,
    price_credits bigint,
    -- Для предметов, привязанных к достижению
    achievement_code text,
    -- Косметика команды покупается капитаном
    is_team_item  boolean     NOT NULL DEFAULT false,
    is_active     boolean     NOT NULL DEFAULT true,
    sort_order    int         NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT catalog_items_price_matches_acquisition CHECK (
        (acquisition = 'points'  AND price_points  IS NOT NULL AND price_credits IS NULL) OR
        (acquisition = 'credits' AND price_credits IS NOT NULL AND price_points  IS NULL) OR
        (acquisition IN ('default','achievement','subscription')
             AND price_points IS NULL AND price_credits IS NULL)
    )
);

CREATE INDEX catalog_items_kind_idx ON catalog_items (kind) WHERE is_active;

CREATE TABLE user_inventory (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    item_id    uuid        NOT NULL REFERENCES catalog_items (id) ON DELETE CASCADE,
    acquired_via item_acquisition NOT NULL,
    acquired_at timestamptz NOT NULL DEFAULT now(),
    -- Предметы от подписки пропадают из доступных при её окончании,
    -- но запись остаётся: вернул подписку — вернул и предмет.
    expires_at timestamptz,
    PRIMARY KEY (user_id, item_id)
);

CREATE TABLE team_inventory (
    team_id    uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    item_id    uuid        NOT NULL REFERENCES catalog_items (id) ON DELETE CASCADE,
    bought_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    acquired_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, item_id)
);

-- ---------------------------------------------------------------------------
-- Достижения: четыре ветки, чтобы каждый тип игрока нашёл свою
-- ---------------------------------------------------------------------------

CREATE TYPE achievement_branch AS ENUM ('participation', 'sport', 'reliability', 'organizer');

CREATE TABLE achievements (
    code        text PRIMARY KEY,
    branch      achievement_branch NOT NULL,
    name        text        NOT NULL,
    description text        NOT NULL,
    -- Порог: 10 матчей, 50 матчей и так далее
    threshold   int,
    tier        smallint    NOT NULL DEFAULT 1,
    -- Шильдик за это достижение никогда не продаётся
    item_id     uuid        REFERENCES catalog_items (id) ON DELETE SET NULL,
    points_award bigint     NOT NULL DEFAULT 0,
    is_active   boolean     NOT NULL DEFAULT true
);

CREATE TABLE user_achievements (
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code        text        NOT NULL REFERENCES achievements (code) ON DELETE CASCADE,
    unlocked_at timestamptz NOT NULL DEFAULT now(),
    -- Прогресс до следующего порога показывается полоской в профиле:
    -- без этого достижения не работают как мотивация.
    progress    int         NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, code)
);

CREATE INDEX user_achievements_user_idx ON user_achievements (user_id, unlocked_at DESC);

-- ---------------------------------------------------------------------------
-- Лутбоксы: только награда за задания, никогда не продаются.
-- Внутри — дни подписки и косметика. Валюты внутри нет намеренно.
-- ---------------------------------------------------------------------------

CREATE TABLE lootbox_types (
    code        text PRIMARY KEY,
    name        text        NOT NULL,
    description text,
    -- Состав и шансы показываются ДО открытия.
    contents    jsonb       NOT NULL,
    is_active   boolean     NOT NULL DEFAULT true,
    CONSTRAINT lootbox_never_sold CHECK (true)
);

CREATE TABLE lootbox_grants (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type_code    text        NOT NULL REFERENCES lootbox_types (code) ON DELETE RESTRICT,
    -- За что выдан: код задания или сезонного пропуска
    granted_for  text        NOT NULL,
    granted_at   timestamptz NOT NULL DEFAULT now(),
    opened_at    timestamptz,
    -- Что выпало. Пустой лутбокс невозможен: минимум — один день подписки.
    result       jsonb
);

CREATE INDEX lootbox_grants_user_idx ON lootbox_grants (user_id, granted_at DESC);
CREATE INDEX lootbox_grants_unopened_idx ON lootbox_grants (user_id) WHERE opened_at IS NULL;

-- ---------------------------------------------------------------------------
-- Сезонный пропуск и задания
-- ---------------------------------------------------------------------------

CREATE TABLE quests (
    code        text PRIMARY KEY,
    name        text        NOT NULL,
    description text        NOT NULL,
    period      text        NOT NULL,
    target      int         NOT NULL,
    reward_points bigint    NOT NULL DEFAULT 0,
    reward_lootbox text     REFERENCES lootbox_types (code) ON DELETE SET NULL,
    is_active   boolean     NOT NULL DEFAULT true,
    CONSTRAINT quests_period CHECK (period IN ('weekly', 'monthly', 'season'))
);

CREATE TABLE user_quests (
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code        text        NOT NULL REFERENCES quests (code) ON DELETE CASCADE,
    period_start date       NOT NULL,
    progress    int         NOT NULL DEFAULT 0,
    completed_at timestamptz,
    claimed_at  timestamptz,
    PRIMARY KEY (user_id, code, period_start)
);

CREATE INDEX user_quests_active_idx ON user_quests (user_id, period_start DESC);

-- ---------------------------------------------------------------------------
-- Прогнозы: ТОЛЬКО за заработанные очки, без денежных призов.
-- Схема запрещает ставку кредитами на уровне констрейнта.
-- ---------------------------------------------------------------------------

CREATE TABLE predictions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    match_id    uuid        NOT NULL REFERENCES matches (id) ON DELETE CASCADE,
    -- Валюта ставки жёстко зафиксирована: только points.
    stake_kind  currency_kind NOT NULL DEFAULT 'points',
    stake       bigint      NOT NULL,
    predicted_home smallint NOT NULL,
    predicted_away smallint NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    settled_at  timestamptz,
    payout      bigint,
    CONSTRAINT predictions_points_only CHECK (stake_kind = 'points'),
    CONSTRAINT predictions_stake_positive CHECK (stake > 0),
    CONSTRAINT predictions_scores CHECK (predicted_home >= 0 AND predicted_away >= 0)
);

CREATE UNIQUE INDEX predictions_key ON predictions (match_id, user_id);
CREATE INDEX predictions_settlement_idx ON predictions (match_id) WHERE settled_at IS NULL;

-- ---------------------------------------------------------------------------
-- Рефералы
-- ---------------------------------------------------------------------------

CREATE TABLE referrals (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id  uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    referred_id  uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code         text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- Награда начисляется не за регистрацию, а за первый сыгранный матч:
    -- иначе это ферма мёртвых аккаунтов.
    qualified_at timestamptz,
    CONSTRAINT referrals_no_self CHECK (referrer_id <> referred_id)
);

CREATE UNIQUE INDEX referrals_referred_key ON referrals (referred_id);
CREATE INDEX referrals_referrer_idx ON referrals (referrer_id);

-- +goose Down

DROP TABLE IF EXISTS referrals;
DROP TABLE IF EXISTS predictions;
DROP TABLE IF EXISTS user_quests;
DROP TABLE IF EXISTS quests;
DROP TABLE IF EXISTS lootbox_grants;
DROP TABLE IF EXISTS lootbox_types;
DROP TABLE IF EXISTS user_achievements;
DROP TABLE IF EXISTS achievements;
DROP TYPE IF EXISTS achievement_branch;
DROP TABLE IF EXISTS team_inventory;
DROP TABLE IF EXISTS user_inventory;
DROP TABLE IF EXISTS catalog_items;
DROP TYPE IF EXISTS item_acquisition;
DROP TYPE IF EXISTS item_kind;
DROP TABLE IF EXISTS subscriptions;
DROP TYPE IF EXISTS subscription_source;
DROP TYPE IF EXISTS subscription_tier;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS wallets;
DROP TYPE IF EXISTS currency_kind;
