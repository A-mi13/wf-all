-- 0020_staff.sql
-- Сотрудники и админка (спека docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md, §3, с
-- решениями ревью от 06.10.2026; ролевая модель — docs/05-ролевая-модель.md):
--   * модуль staff — учётки, роль и города модератора, TOTP, резервные коды, приглашения, сессии;
--   * платформа — подтверждения вдвоём (approvals), автор-сотрудник, снимок имени и город в audit_log,
--     комментарий idempotency_keys.user_id (ключ субъекта — игрока или сотрудника);
--   * identity — role_assignments: только допуск капитана в городе.
-- id новых таблиц — UUIDv7 из Go (platform/id), время — из clock.Clock приложения: умолчаний нет
-- намеренно. Секреты — только шифротекст (keys.Ring, AES-256-GCM) или HMAC; key_id — имя ключа
-- (keys.ID, 11 символов base64url), по нему ротация находит ключ.

-- +goose Up

-- Роли сотрудников переехали в staff_roles. ALTER TYPE … DROP VALUE в PostgreSQL нет — типы ролей
-- игроков пересоздаются ниже; строки, которые в новые типы не лягут, — понятный отказ, а не общее
-- «invalid input value for enum». Транзакция миграции откатится целиком.
-- +goose StatementBegin
DO $$
DECLARE
  n bigint;
BEGIN
  SELECT count(*) INTO n FROM role_assignments WHERE role::text <> 'captain' OR scope_type::text <> 'city';
  IF n > 0 THEN
    RAISE EXCEPTION 'role_assignments: строк с ролями сотрудников или не городским скоупом — % (роли сотрудников теперь в staff_roles, docs/05-ролевая-модель.md): перенесите или удалите их и повторите migrate up', n;
  END IF;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Модуль staff (§3.1). Права БД — только admin (grants.sql: staff\_%); api и worker не видят ничего.
-- ---------------------------------------------------------------------------

CREATE TYPE staff_role AS ENUM ('owner', 'admin', 'moderator', 'support');
CREATE TYPE staff_status AS ENUM ('invited', 'active', 'disabled');
CREATE TYPE staff_session_stage AS ENUM ('mfa_pending', 'setup', 'active');
CREATE TYPE staff_invite_purpose AS ENUM ('invite', 'access_reset');

CREATE TABLE staff_accounts (
    id              uuid         PRIMARY KEY,
    -- рабочая почта; нормализует Go (NFKC, trim, нижний регистр — Review Focus 1), база держит
    -- нижний регистр и края без пробелов, citext — уникальность без учёта регистра
    email           citext       NOT NULL CONSTRAINT staff_accounts_email_key UNIQUE,
    display_name    text         NOT NULL,
    locale          text         NOT NULL DEFAULT 'ru',
    status          staff_status NOT NULL DEFAULT 'invited',
    -- argon2id (platform/password); NULL — до принятия приглашения и после сброса доступа
    password_hash   text,
    -- неудачи второго фактора подряд (D7); неудачи пароля сюда не идут
    mfa_failures     int          NOT NULL DEFAULT 0,
    locked_until     timestamptz,
    -- последнее письмо staff.locked (не чаще раза в час на учётку, спека §3.1); пишет блокировка (6a)
    lock_notified_at timestamptz,
    last_login_at    timestamptz,
    created_at      timestamptz  NOT NULL,
    -- NULL — создана оператором (admin-api staff bootstrap)
    created_by      uuid         REFERENCES staff_accounts (id),
    -- версия агрегата: события и сверка цели подтверждения (D17)
    version         bigint       NOT NULL DEFAULT 1,
    CONSTRAINT staff_accounts_email_format CHECK (
        email::text = lower(btrim(email::text))
        AND char_length(email::text) <= 254
        AND email::text ~ '^[^@[:space:]]+@[^@[:space:]]+$'),
    CONSTRAINT staff_accounts_display_name_check CHECK (char_length(display_name) BETWEEN 1 AND 80 AND btrim(display_name) <> ''),
    CONSTRAINT staff_accounts_locale_check CHECK (locale IN ('ru', 'en')),
    CONSTRAINT staff_accounts_active_password CHECK (status <> 'active' OR password_hash IS NOT NULL),
    CONSTRAINT staff_accounts_mfa_failures_check CHECK (mfa_failures >= 0),
    CONSTRAINT staff_accounts_version_check CHECK (version > 0)
);

-- одна роль на сотрудника (§2.3); снять роль = выключить учётку, строка роли остаётся
CREATE TABLE staff_roles (
    staff_id   uuid        PRIMARY KEY REFERENCES staff_accounts (id) ON DELETE CASCADE,
    role       staff_role  NOT NULL,
    -- NULL — выдана оператором
    granted_by uuid        REFERENCES staff_accounts (id),
    granted_at timestamptz NOT NULL
);

-- активные владельцы и админы: последний владелец, письма о блокировке
CREATE INDEX staff_roles_role_idx ON staff_roles (role);

-- города модератора; город — справочник geo (только внешний ключ: модуль staff geo не импортирует, D1)
CREATE TABLE staff_role_cities (
    staff_id uuid NOT NULL REFERENCES staff_roles (staff_id) ON DELETE CASCADE,
    city_id  uuid NOT NULL REFERENCES cities (id),
    PRIMARY KEY (staff_id, city_id)
);

-- журнал и подтверждения модератора по городу
CREATE INDEX staff_role_cities_city_idx ON staff_role_cities (city_id);

-- Модератор ⇔ хотя бы один город. Проверка отложена до COMMIT: роль и города меняются в одной
-- транзакции в любом порядке. Ошибка — 23514 с именем staff_role_cities_moderator.
-- Гонка: при READ COMMITTED две транзакции, снимающие разные последние города одного модератора,
-- видят каждая чужой город и проходят обе. Защита — у вызывающего: любая правка роли или городов
-- блокирует строку учётки (UPDATE staff_accounts SET version = version + 1 в той же транзакции,
-- спека §3.1), тогда вторая транзакция ждёт первую и проверяет уже её результат.
-- +goose StatementBegin
CREATE FUNCTION staff_role_cities_check()
RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  ids    uuid[] := '{}';
  sid    uuid;
  r      staff_role;
  cities bigint;
BEGIN
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    ids := ids || OLD.staff_id;
  END IF;
  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    ids := ids || NEW.staff_id;
  END IF;
  FOREACH sid IN ARRAY ids LOOP
    SELECT role INTO r FROM staff_roles WHERE staff_id = sid;
    -- роли нет: удалена вместе с учёткой, города ушли каскадом
    CONTINUE WHEN NOT FOUND;
    SELECT count(*) INTO cities FROM staff_role_cities WHERE staff_id = sid;
    IF (r = 'moderator') <> (cities > 0) THEN
      RAISE EXCEPTION USING
        ERRCODE = 'check_violation',
        CONSTRAINT = 'staff_role_cities_moderator',
        TABLE = 'staff_role_cities',
        MESSAGE = format('сотрудник %s: роль %s, городов %s — города бывают только у модератора, и у него хотя бы один', sid, r, cities);
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER staff_roles_moderator_cities
    AFTER INSERT OR UPDATE ON staff_roles
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION staff_role_cities_check();

CREATE CONSTRAINT TRIGGER staff_role_cities_moderator_cities
    AFTER INSERT OR UPDATE OR DELETE ON staff_role_cities
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION staff_role_cities_check();

-- второй фактор (TOTP, RFC 6238): секрет зашифрован ключом key_id
CREATE TABLE staff_mfa (
    staff_id       uuid        PRIMARY KEY REFERENCES staff_accounts (id) ON DELETE CASCADE,
    secret_enc     bytea       NOT NULL,
    key_id         text        NOT NULL,
    -- NULL — настройка не подтверждена
    confirmed_at   timestamptz,
    -- шаг последнего принятого кода: повтор кода и коды старше него отвергаются
    last_used_step bigint      NOT NULL DEFAULT 0,
    CONSTRAINT staff_mfa_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$'),
    CONSTRAINT staff_mfa_last_used_step_check CHECK (last_used_step >= 0)
);

-- резервные коды: HMAC-SHA-256 ключом key_id (D5); 10 штук на поколение, перевыпуск удаляет старые
CREATE TABLE staff_recovery_codes (
    staff_id   uuid        NOT NULL REFERENCES staff_accounts (id) ON DELETE CASCADE,
    code_mac   bytea       NOT NULL,
    key_id     text        NOT NULL,
    used_at    timestamptz,
    PRIMARY KEY (staff_id, code_mac),
    CONSTRAINT staff_recovery_codes_code_mac_check CHECK (octet_length(code_mac) = 32),
    CONSTRAINT staff_recovery_codes_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$')
);

-- ротация ключей (D5): сколько неиспользованных кодов ещё на старых ключах
CREATE INDEX staff_recovery_codes_key_idx ON staff_recovery_codes (key_id) WHERE used_at IS NULL;

-- Приглашения и сброс доступа. Код (6 цифр) зашифрован кольцом ключей, aad = id приглашения (D4):
-- письмо собирает составитель admin-api по id. NULL — кода нет: стёрт после 5 неверных вводов
-- (приглашение живо, новый код — повторной отправкой, D12) или приглашение закрыто.
CREATE TABLE staff_invites (
    id           uuid                 PRIMARY KEY,
    staff_id     uuid                 NOT NULL REFERENCES staff_accounts (id) ON DELETE CASCADE,
    purpose      staff_invite_purpose NOT NULL,
    code_enc     bytea,
    code_key_id  text,
    expires_at   timestamptz          NOT NULL,
    -- неверные вводы текущего кода
    code_attempts int                 NOT NULL DEFAULT 0,
    sends        int                  NOT NULL DEFAULT 0,
    -- последняя отправка кода (пауза 60 с); пишется вместе со строкой (InviteInsert, Task 7)
    last_sent_at timestamptz          NOT NULL,
    created_at   timestamptz          NOT NULL,
    -- погашено на recovery-codes/ack (D12)
    consumed_at  timestamptz,
    revoked_at   timestamptz,
    CONSTRAINT staff_invites_code_pair CHECK ((code_enc IS NULL) = (code_key_id IS NULL)),
    CONSTRAINT staff_invites_code_key_id_format CHECK (code_key_id ~ '^[A-Za-z0-9_-]{11}$'),
    CONSTRAINT staff_invites_closed_no_code CHECK ((consumed_at IS NULL AND revoked_at IS NULL) OR code_enc IS NULL),
    CONSTRAINT staff_invites_code_attempts_check CHECK (code_attempts BETWEEN 0 AND 5),
    -- 5 неверных вводов стирают код (D12): код с исчерпанными попытками не живёт; новый код — с code_attempts = 0
    CONSTRAINT staff_invites_code_wiped CHECK (code_attempts < 5 OR code_enc IS NULL),
    CONSTRAINT staff_invites_sends_check CHECK (sends >= 0)
);

-- живое приглашение у учётки одно
CREATE UNIQUE INDEX staff_invites_live_key ON staff_invites (staff_id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX staff_invites_staff_idx ON staff_invites (staff_id);
-- чистка: просроченные и закрытые старше 30 дней
CREATE INDEX staff_invites_expires_idx ON staff_invites (expires_at);

-- Сессии админки — своя таблица, не общая sessions (у api на неё DML). Токен — в cookie, в базе
-- только SHA-256; ротация токена (D9) заменяет token_hash.
CREATE TABLE staff_sessions (
    id             uuid                PRIMARY KEY,
    staff_id       uuid                NOT NULL REFERENCES staff_accounts (id) ON DELETE CASCADE,
    token_hash     bytea               NOT NULL CONSTRAINT staff_sessions_token_hash_key UNIQUE,
    stage          staff_session_stage NOT NULL,
    created_at     timestamptz         NOT NULL,
    last_seen_at   timestamptz         NOT NULL,
    -- время последнего второго фактора (вход, разблокировка)
    mfa_at         timestamptz,
    -- абсолютный срок
    expires_at     timestamptz         NOT NULL,
    ip             inet                NOT NULL,
    -- заголовок User-Agent как есть; нет заголовка — пустая строка
    user_agent     text                NOT NULL,
    revoked_at     timestamptz,
    revoked_reason text,
    CONSTRAINT staff_sessions_token_hash_check CHECK (octet_length(token_hash) = 32),
    CONSTRAINT staff_sessions_active_mfa CHECK (stage <> 'active' OR mfa_at IS NOT NULL),
    CONSTRAINT staff_sessions_revoked_pair CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL)),
    CONSTRAINT staff_sessions_revoked_reason_check CHECK (revoked_reason IN
        ('logout', 'expired', 'revoked', 'locked', 'password_changed', 'access_reset', 'disabled', 'replaced'))
);

-- свои сессии, «завершить все»
CREATE INDEX staff_sessions_staff_idx ON staff_sessions (staff_id) WHERE revoked_at IS NULL;
-- чистка истёкших
CREATE INDEX staff_sessions_expires_idx ON staff_sessions (expires_at);

-- ---------------------------------------------------------------------------
-- Подтверждение вдвоём (D2) — механизм платформы: исполнителей регистрируют модули. Внешних ключей
-- нет (платформа не зависит от таблиц модулей); имена инициатора и решившего — снимком.
-- ---------------------------------------------------------------------------

CREATE TYPE approval_status AS ENUM ('pending', 'executed', 'rejected', 'cancelled', 'expired', 'failed');

CREATE TABLE approvals (
    id                 uuid            PRIMARY KEY,
    action             text            NOT NULL,  -- <модуль>.<действие>: staff.invite, staff.role_change…
    permission         text            NOT NULL,  -- право, по которому решают (R7)
    payload            jsonb           NOT NULL,  -- аргументы исполнителя с версией формата v
    target_id          uuid,                      -- цель (сотрудник): решивший — не цель (D17)
    scope_city_id      uuid,                      -- город цели для права «свои города»
    requested_by       uuid            NOT NULL,
    requested_by_label text            NOT NULL,
    requested_at       timestamptz     NOT NULL,
    expires_at         timestamptz     NOT NULL,  -- 24 ч
    status             approval_status NOT NULL DEFAULT 'pending',
    decided_by         uuid,                      -- NULL у просроченного и отменённого системой
    decided_by_label   text,                      -- снимок имени; 'system' у решённого системой
    decided_at         timestamptz,
    decision_comment   text,
    error              text,                      -- причина failed
    CONSTRAINT approvals_action_format CHECK (action ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$'),
    CONSTRAINT approvals_permission_format CHECK (permission ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$'),
    CONSTRAINT approvals_payload_versioned CHECK (
        jsonb_typeof(payload) = 'object' AND coalesce(jsonb_typeof(payload -> 'v'), '') = 'number'),
    CONSTRAINT approvals_requested_by_label_check CHECK (char_length(btrim(requested_by_label)) BETWEEN 1 AND 120),
    CONSTRAINT approvals_decided_by_label_check CHECK (char_length(btrim(decided_by_label)) BETWEEN 1 AND 120),
    CONSTRAINT approvals_decision_comment_check CHECK (char_length(decision_comment) <= 1000),
    CONSTRAINT approvals_expiry CHECK (expires_at > requested_at),
    CONSTRAINT approvals_pending_undecided CHECK (status <> 'pending' OR (decided_at IS NULL AND decided_by IS NULL)),
    CONSTRAINT approvals_decided_at CHECK (status = 'pending' OR decided_at IS NOT NULL),
    -- решивший-человек — всегда со снимком имени; решённое без решившего — только просрочка и отмена
    -- системой, и всегда с меткой 'system' (ApprovalExpireDue, ApprovalCancelByInitiator — Task 8a)
    CONSTRAINT approvals_decider_label CHECK (decided_by IS NULL OR decided_by_label IS NOT NULL),
    CONSTRAINT approvals_system_decider CHECK (decided_by IS NOT NULL
        OR (status = 'pending' AND decided_by_label IS NULL)
        OR (status IN ('expired', 'cancelled') AND coalesce(decided_by_label, '') = 'system')),
    CONSTRAINT approvals_decider_required CHECK (status NOT IN ('executed', 'rejected', 'failed') OR decided_by IS NOT NULL),
    -- правило двух рук: решает не инициатор и не цель (отменить может инициатор — статус cancelled)
    CONSTRAINT approvals_two_hands CHECK (status NOT IN ('executed', 'rejected', 'failed')
        OR (decided_by <> requested_by AND decided_by IS DISTINCT FROM target_id)),
    -- отменяет человек — только инициатор (§4.8) и без error: error у отмены — только у системной
    CONSTRAINT approvals_cancel_by_initiator CHECK (status <> 'cancelled' OR decided_by IS NULL
        OR (decided_by = requested_by AND error IS NULL)),
    CONSTRAINT approvals_reject_comment CHECK (status <> 'rejected' OR coalesce(btrim(decision_comment), '') <> ''),
    -- error — причина failed; у отменённого системой — initiator_changed (CancelByInitiator, D17)
    CONSTRAINT approvals_failed_error CHECK (CASE status
        WHEN 'failed' THEN error IS NOT NULL
        WHEN 'cancelled' THEN true
        ELSE error IS NULL END)
);

-- входящие и мои: по статусу и времени
CREATE INDEX approvals_status_idx ON approvals (status, requested_at DESC, id DESC);
CREATE INDEX approvals_requested_by_idx ON approvals (requested_by, requested_at DESC);
-- просрочка ожидающих
CREATE INDEX approvals_pending_expiry_idx ON approvals (expires_at) WHERE status = 'pending';
-- отмена ожидающих по цели
CREATE INDEX approvals_target_idx ON approvals (target_id) WHERE status = 'pending' AND target_id IS NOT NULL;
-- чистка решённых старше 180 дней
CREATE INDEX approvals_decided_idx ON approvals (decided_at) WHERE status <> 'pending';

-- ---------------------------------------------------------------------------
-- Журнал действий (D11): автор-сотрудник, снимок имени, город записи (журнал модератора).
-- Запись без автора — система (воркер, миграция), оператор или аноним (неудачный вход); роль у неё
-- обязательна (спека §3.2). У автора-игрока пока проверяется только «роль не служебная»: импорт geo
-- пишет actor_user_id + admin до перевода на actor_staff_id (план B).
-- ---------------------------------------------------------------------------

ALTER TABLE audit_log
    ADD COLUMN actor_staff_id uuid,
    ADD COLUMN actor_label    text,
    ADD COLUMN scope_city_id  uuid,
    ADD CONSTRAINT audit_log_one_actor CHECK (actor_user_id IS NULL OR actor_staff_id IS NULL),
    ADD CONSTRAINT audit_log_actor_role CHECK (actor_role IN
        ('player', 'captain', 'owner', 'admin', 'moderator', 'support', 'system', 'operator', 'anonymous')),
    ADD CONSTRAINT audit_log_staff_actor CHECK (actor_staff_id IS NULL
        OR (coalesce(actor_role, '') IN ('owner', 'admin', 'moderator', 'support') AND actor_label IS NOT NULL)),
    -- роль есть всегда; служебная роль ⇔ нет автора
    ADD CONSTRAINT audit_log_service_actor CHECK (actor_role IS NOT NULL
        AND (actor_user_id IS NULL AND actor_staff_id IS NULL) = (actor_role IN ('system', 'operator', 'anonymous'))),
    ADD CONSTRAINT audit_log_actor_label CHECK (actor_label IS NULL OR char_length(btrim(actor_label)) BETWEEN 1 AND 120);

-- журнал сотрудника и журнал модератора по городу; keyset (created_at, id) — как у журнала целиком
CREATE INDEX audit_log_staff_actor_idx ON audit_log (actor_staff_id, created_at DESC, id DESC) WHERE actor_staff_id IS NOT NULL;
CREATE INDEX audit_log_scope_city_idx ON audit_log (scope_city_id, created_at DESC, id DESC) WHERE scope_city_id IS NOT NULL;
-- журнал целиком: created_at DESC, id (keyset)
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC, id DESC);

-- Идемпотентность (D19): колонка не переименовывается; ключ — (id субъекта, ключ).
COMMENT ON COLUMN idempotency_keys.user_id IS 'id субъекта: игрок (users) или сотрудник (staff_accounts); UUIDv7 не пересекаются';

-- ---------------------------------------------------------------------------
-- Роли игроков (§3.2, M1): только допуск капитана в городе. Старый CHECK ссылается на 'global' —
-- снимается первым; типы пересоздаются через text; индексы перестраиваются сами.
-- ---------------------------------------------------------------------------

ALTER TABLE role_assignments DROP CONSTRAINT role_scope_id_present;
ALTER TABLE role_assignments
    ALTER COLUMN role TYPE text USING role::text,
    ALTER COLUMN scope_type TYPE text USING scope_type::text;
DROP TYPE role_name;
DROP TYPE role_scope;
CREATE TYPE role_name AS ENUM ('captain');
CREATE TYPE role_scope AS ENUM ('city');
ALTER TABLE role_assignments
    ALTER COLUMN role TYPE role_name USING role::role_name,
    ALTER COLUMN scope_type TYPE role_scope USING scope_type::role_scope,
    ADD CONSTRAINT role_scope_id_present CHECK (scope_id IS NOT NULL);

-- +goose Down

-- Возвращает схему 0019 в точности. Данные staff_* и approvals, авторы-сотрудники, снимки имён и
-- города записей журнала теряются (README).
ALTER TABLE role_assignments DROP CONSTRAINT role_scope_id_present;
ALTER TABLE role_assignments
    ALTER COLUMN role TYPE text USING role::text,
    ALTER COLUMN scope_type TYPE text USING scope_type::text;
DROP TYPE role_name;
DROP TYPE role_scope;
CREATE TYPE role_name AS ENUM ('player', 'captain', 'vice_captain', 'city_moderator', 'admin');
CREATE TYPE role_scope AS ENUM ('global', 'country', 'city', 'team');
ALTER TABLE role_assignments
    ALTER COLUMN role TYPE role_name USING role::role_name,
    ALTER COLUMN scope_type TYPE role_scope USING scope_type::role_scope,
    ADD CONSTRAINT role_scope_id_present
        CHECK ((scope_type = 'global' AND scope_id IS NULL)
            OR (scope_type <> 'global' AND scope_id IS NOT NULL));

COMMENT ON COLUMN idempotency_keys.user_id IS NULL;

DROP INDEX audit_log_created_idx;
DROP INDEX audit_log_scope_city_idx;
DROP INDEX audit_log_staff_actor_idx;
ALTER TABLE audit_log
    DROP CONSTRAINT audit_log_actor_label,
    DROP CONSTRAINT audit_log_service_actor,
    DROP CONSTRAINT audit_log_staff_actor,
    DROP CONSTRAINT audit_log_actor_role,
    DROP CONSTRAINT audit_log_one_actor,
    DROP COLUMN scope_city_id,
    DROP COLUMN actor_label,
    DROP COLUMN actor_staff_id;

DROP TABLE approvals;
DROP TYPE approval_status;

DROP TABLE staff_sessions;
DROP TABLE staff_invites;
DROP TABLE staff_recovery_codes;
DROP TABLE staff_mfa;
DROP TABLE staff_role_cities;
DROP TABLE staff_roles;
DROP FUNCTION staff_role_cities_check();
DROP TABLE staff_accounts;
DROP TYPE staff_invite_purpose;
DROP TYPE staff_session_stage;
DROP TYPE staff_status;
DROP TYPE staff_role;
