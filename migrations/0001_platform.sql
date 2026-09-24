-- 0001_platform.sql
-- Базовый слой: расширения, служебные функции, аудит, outbox, идемпотентность, фича-флаги.
-- Ничего доменного здесь нет — только то, от чего зависят все остальные миграции.

-- +goose Up

-- btree_gist нужен для EXCLUDE-констрейнта на слоты полей (uuid WITH = + tstzrange WITH &&).
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- unaccent — снятие диакритики при нормализации ников и названий команд.
CREATE EXTENSION IF NOT EXISTS unaccent;

-- citext — регистронезависимый email без ручного lower() в каждом запросе.
CREATE EXTENSION IF NOT EXISTS citext;

-- PostGIS — гео для полей: поиск рядом, поиск дублей в радиусе.
-- Если managed-хостинг не даёт PostGIS, смотри комментарий в 0005_pitches.sql.
CREATE EXTENSION IF NOT EXISTS postgis;

-- ---------------------------------------------------------------------------
-- Служебные функции
-- ---------------------------------------------------------------------------

-- unaccent объявлен STABLE, а в generated-колонках и индексах допустимы только
-- IMMUTABLE-функции. Обёртка с явным указанием словаря — стандартный приём.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION immutable_unaccent(text)
RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT public.unaccent('public.unaccent'::regdictionary, $1)
$$;
-- +goose StatementEnd

-- Нормализация пользовательского текста для проверок уникальности.
-- Порядок операций важен:
--   1. СНАЧАЛА гомоглифы, ПОТОМ нижний регистр. Латинские заглавные B, H, M, T
--      визуально неотличимы от кириллических В, Н, М, Т, но после lower()
--      расходятся (b/в, h/н), и клон названия проходит проверку уникальности.
--   2. нижний регистр и снятие диакритики;
--   3. выкидывает всё, кроме букв и цифр — пробелы, точки, подчёркивания.
--
-- Побочный эффект: латинские названия частично кириллизуются в нормализованной
-- форме («Barcelona» → «bаrсеlоnа»). Это не важно: нормализованное значение
-- нигде не показывается, оно нужно только для сравнения.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_text(input text)
RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT regexp_replace(
           lower(
             immutable_unaccent(
               translate(
                 btrim(input),
                 -- латиница, неотличимая от кириллицы, в обоих регистрах
                 'ABEKMHOPCTXYaeopcxyk',
                 'АВЕКМНОРСТХУаеорсхук'
               )
             )
           ),
           '[^a-zа-яё0-9]+', '', 'g'
         )
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Аудит-лог. Append-only: изменять и удалять записи нельзя никому.
-- ---------------------------------------------------------------------------

CREATE TABLE audit_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid,
    actor_role    text,
    action        text        NOT NULL,
    object_type   text        NOT NULL,
    object_id     uuid,
    before        jsonb,
    after         jsonb,
    reason        text,
    ip            inet,
    user_agent    text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_object_idx ON audit_log (object_type, object_id, created_at DESC);
CREATE INDEX audit_log_actor_idx  ON audit_log (actor_user_id, created_at DESC);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_log_is_append_only()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit_log is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_log_no_update
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_is_append_only();

-- ---------------------------------------------------------------------------
-- Transactional outbox. События пишутся в той же транзакции, что и данные,
-- иначе бывают матчи без статистики и статистика без матчей.
-- ---------------------------------------------------------------------------

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

-- Частичный индекс: воркер читает только неопубликованное, а это доли процента таблицы.
CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;

-- ---------------------------------------------------------------------------
-- Идемпотентность мутирующих запросов.
-- Мобильный интернет теряет ответы, люди жмут кнопку дважды.
-- ---------------------------------------------------------------------------

CREATE TABLE idempotency_keys (
    user_id         uuid        NOT NULL,
    key             text        NOT NULL,
    endpoint        text        NOT NULL,
    request_hash    text        NOT NULL,
    response_status int,
    response_body   jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    PRIMARY KEY (user_id, key)
);

CREATE INDEX idempotency_keys_expiry_idx ON idempotency_keys (expires_at);

-- ---------------------------------------------------------------------------
-- Фича-флаги. Включение функций по городам без релиза.
-- ---------------------------------------------------------------------------

CREATE TABLE feature_flags (
    key              text PRIMARY KEY,
    description      text        NOT NULL DEFAULT '',
    enabled_globally boolean     NOT NULL DEFAULT false,
    enabled_city_ids uuid[]      NOT NULL DEFAULT '{}',
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER feature_flags_updated_at
    BEFORE UPDATE ON feature_flags
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down

DROP TABLE IF EXISTS feature_flags;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS outbox;
DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
DROP FUNCTION IF EXISTS audit_log_is_append_only();
DROP TABLE IF EXISTS audit_log;
DROP FUNCTION IF EXISTS set_updated_at();
DROP FUNCTION IF EXISTS normalize_text(text);
DROP FUNCTION IF EXISTS immutable_unaccent(text);
