-- 0017_platform_events.sql
-- События платформы (спека бэкенда §6.5, §11): outbox с конвертом события, inbox подписчиков
-- и курсоры версий агрегатов; NOTIFY будит relay в воркере.
-- Старый outbox из 0001 пуст во всех окружениях (продюсеров не было) — пересоздаём.

-- +goose Up

DROP TABLE outbox;

CREATE TABLE outbox (
    id                uuid PRIMARY KEY,          -- UUIDv7 из Go (platform/id)
    event_type        text        NOT NULL,      -- <модуль>.<факт>
    schema_version    int         NOT NULL CHECK (schema_version > 0),
    aggregate_type    text        NOT NULL,
    aggregate_id      uuid        NOT NULL,
    aggregate_version bigint      NOT NULL CHECK (aggregate_version > 0),
    -- время транзакции из базы, не часы инстанса
    occurred_at       timestamptz NOT NULL DEFAULT now(),
    payload           jsonb       NOT NULL,
    trace             jsonb,                     -- контекст трассировки; заполнится с OTel
    published_at      timestamptz
);

-- relay берёт неопубликованное по порядку; таких строк — доли процента таблицы
CREATE INDEX outbox_unpublished_idx ON outbox (occurred_at, id) WHERE published_at IS NULL;
-- переигровка: события типа начиная с момента
CREATE INDEX outbox_type_idx ON outbox (event_type, occurred_at);
-- чистка опубликованного старше 30 дней
CREATE INDEX outbox_published_idx ON outbox (published_at) WHERE published_at IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION outbox_notify()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  -- один сигнал на оператор: relay сам заберёт всю пачку
  PERFORM pg_notify('wf_outbox', '');
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER outbox_notify
    AFTER INSERT ON outbox
    FOR EACH STATEMENT EXECUTE FUNCTION outbox_notify();

-- Подписчик отмечает обработанное событие в своей транзакции: конфликт — уже обработано.
CREATE TABLE event_inbox (
    subscriber   text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, event_id)
);

CREATE INDEX event_inbox_processed_idx ON event_inbox (processed_at);

-- Последняя применённая версия агрегата у подписчика, следящего за состоянием: событие со
-- строго меньшей версией — устаревшее и отбрасывается.
CREATE TABLE event_cursors (
    subscriber     text        NOT NULL,
    aggregate_type text        NOT NULL,
    aggregate_id   uuid        NOT NULL,
    version        bigint      NOT NULL CHECK (version >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, aggregate_type, aggregate_id)
);

-- +goose Down

DROP TABLE IF EXISTS event_cursors;
DROP TABLE IF EXISTS event_inbox;
DROP TRIGGER IF EXISTS outbox_notify ON outbox;
DROP FUNCTION IF EXISTS outbox_notify();
DROP TABLE IF EXISTS outbox;

CREATE TABLE outbox (
    id             bigserial PRIMARY KEY,
    aggregate_type text        NOT NULL,
    aggregate_id   uuid        NOT NULL,
    event_type     text        NOT NULL,
    payload        jsonb       NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz,
    attempts       int         NOT NULL DEFAULT 0,
    last_error     text
);

CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
