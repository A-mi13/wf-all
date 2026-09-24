-- 0009_moderation_notifications.sql
-- Модерация, жалобы, санкции, уведомления и рекламные блоки.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Жалобы и санкции
-- ---------------------------------------------------------------------------

CREATE TYPE report_target AS ENUM ('user', 'team', 'match', 'pitch', 'comment');

CREATE TABLE abuse_reports (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    target_type   report_target NOT NULL,
    target_id     uuid        NOT NULL,
    city_id       uuid        REFERENCES cities (id) ON DELETE SET NULL,
    category      text        NOT NULL,
    body          text,
    status        application_status NOT NULL DEFAULT 'pending',
    decided_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    decided_at    timestamptz,
    resolution    text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX abuse_reports_queue_idx ON abuse_reports (city_id, created_at) WHERE status = 'pending';
CREATE INDEX abuse_reports_target_idx ON abuse_reports (target_type, target_id);

CREATE TYPE sanction_kind AS ENUM ('warning', 'feature_block', 'suspension', 'ban');

CREATE TABLE sanctions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        sanction_kind NOT NULL,
    -- Какую возможность закрыли: 'create_match', 'comment', 'captain'
    feature     text,
    reason      text        NOT NULL,
    issued_by   uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    starts_at   timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Предикат индекса обязан быть IMMUTABLE, поэтому now() сюда не попадает:
-- отбор по сроку делает запрос, индекс лишь сужает выборку до неотозванных.
CREATE INDEX sanctions_active_idx ON sanctions (user_id, expires_at)
    WHERE revoked_at IS NULL;

-- Флаги аномалий от античит-проверок. Не автобан, а сигнал модератору.
CREATE TABLE anomaly_flags (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type text       NOT NULL,
    subject_id  uuid        NOT NULL,
    city_id     uuid        REFERENCES cities (id) ON DELETE SET NULL,
    rule        text        NOT NULL,
    details     jsonb       NOT NULL DEFAULT '{}',
    status      application_status NOT NULL DEFAULT 'pending',
    reviewed_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX anomaly_flags_queue_idx ON anomaly_flags (city_id, created_at) WHERE status = 'pending';
CREATE INDEX anomaly_flags_subject_idx ON anomaly_flags (subject_type, subject_id);

-- ---------------------------------------------------------------------------
-- Уведомления
-- ---------------------------------------------------------------------------

CREATE TYPE notification_category AS ENUM (
    'team_match_created',
    'team_join_request',
    'match_almost_full',
    'match_reminder',
    'match_cancelled',
    'report_pending',
    'city_new_team',
    'city_new_match',
    'achievement',
    'weekly_digest'
);

CREATE TYPE notification_channel AS ENUM ('push', 'email', 'in_app');

-- Тонкие настройки по категориям, а не одна кнопка «выключить всё»:
-- человек, который отключил все пуши, уже потерян.
CREATE TABLE notification_preferences (
    user_id  uuid                  NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category notification_category NOT NULL,
    channel  notification_channel  NOT NULL,
    enabled  boolean               NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, category, channel)
);

CREATE TABLE notification_settings (
    user_id          uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- Тихие часы по часовому поясу игрока. Отмены матча игнорируют их.
    quiet_hours_from time,
    quiet_hours_to   time,
    -- Полное отключение доступно: это требование стора и просто уважение.
    all_muted        boolean     NOT NULL DEFAULT false,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notifications (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category   notification_category NOT NULL,
    -- Текст генерится на сервере на языке получателя (users.locale)
    title      text        NOT NULL,
    body       text        NOT NULL,
    deep_link  text,
    ref_type   text,
    ref_id     uuid,
    scheduled_at timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz,
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Воркер разбирает очередь по времени: в четверг в 18:00 стартуют сотни
-- напоминаний одновременно, их надо размазывать по минуте.
CREATE INDEX notifications_queue_idx ON notifications (scheduled_at) WHERE sent_at IS NULL;
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Рекламные места. В ленте матчей и в пушах рекламы нет никогда —
-- это зафиксировано констрейнтом, а не договорённостью.
-- ---------------------------------------------------------------------------

CREATE TABLE ad_placements (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slot        text        NOT NULL,
    city_id     uuid        REFERENCES cities (id) ON DELETE CASCADE,
    pitch_id    uuid        REFERENCES pitches (id) ON DELETE CASCADE,
    title       text        NOT NULL,
    body        text,
    image_url   text,
    target_url  text,
    starts_at   timestamptz NOT NULL DEFAULT now(),
    ends_at     timestamptz,
    is_active   boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ad_placements_allowed_slots CHECK (
        slot IN ('pitch_picker', 'pitch_card', 'city_tables_footer')
    )
);

CREATE INDEX ad_placements_city_idx ON ad_placements (city_id, slot) WHERE is_active;

-- Скрытые пользователем баннеры: закрыл крестиком — не возвращается в сессии.
CREATE TABLE ad_dismissals (
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    placement_id uuid        NOT NULL REFERENCES ad_placements (id) ON DELETE CASCADE,
    dismissed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, placement_id)
);

-- +goose Down

DROP TABLE IF EXISTS ad_dismissals;
DROP TABLE IF EXISTS ad_placements;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS notification_settings;
DROP TABLE IF EXISTS notification_preferences;
DROP TYPE IF EXISTS notification_channel;
DROP TYPE IF EXISTS notification_category;
DROP TABLE IF EXISTS anomaly_flags;
DROP TABLE IF EXISTS sanctions;
DROP TYPE IF EXISTS sanction_kind;
DROP TABLE IF EXISTS abuse_reports;
DROP TYPE IF EXISTS report_target;
