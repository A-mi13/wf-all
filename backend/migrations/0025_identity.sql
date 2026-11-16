-- 0025_identity.sql
-- identity, модель данных целиком (спека docs/superpowers/specs/2026-11-13-identity-design.md §5; этапы I-3 и I-4
-- миграций не добавляют):
--   * users, sessions, role_assignments, captain_applications — §5.2;
--   * новые таблицы §5.3: пароли, коды из письма, токены шагов, устройства, refresh-токены, 2FA, привязки
--     провайдеров, ключи доступа, события безопасности, ограничения, согласия, сбросы 2FA поддержкой;
--   * staff_player_links — связь «сотрудник → учётка игрока» модуля staff (§7.6);
--   * экспортированные представления identity_read_* (§5.6);
--   * строки шести флагов способов входа (§4.1).
-- id новых таблиц — UUIDv7 из Go (platform/id), время — из clock.Clock: умолчаний now() и gen_random_uuid() нет
-- намеренно. Секреты — только шифротекст (keys.Ring, AES-256-GCM) или HMAC/SHA-256; key_id — имя ключа (keys.ID,
-- 11 символов base64url). Значение user_status 'pending_deletion' добавила 0024 (своей транзакцией).

-- +goose Up

-- Перед изменением — данные (§5.1): почта обязательна, подтверждена и в канонической форме, sessions
-- перестраивается (строки старой схемы в новую не переносятся), заявки в капитаны проходят новые CHECK (решённая —
-- со временем решения; у отклонённой кода причины до 0025 не бывает; ссылка — https, длины полей). На стенде таблицы
-- пусты; иначе — понятный текст вместо голого 23514 на ADD CONSTRAINT, транзакция миграции откатится целиком.
-- Выражения повторяют CHECK ниже: правишь CHECK — правь и проверку.
-- +goose StatementBegin
DO $$
DECLARE
  n bigint;
BEGIN
  SELECT count(*) INTO n FROM users WHERE email IS NULL OR email_verified_at IS NULL;
  IF n > 0 THEN
    RAISE EXCEPTION 'users: строк без подтверждённой почты — % (с 0025 почта аккаунта обязательна и подтверждена, спека identity §5.2): удалите или исправьте их и повторите migrate up', n;
  END IF;
  SELECT count(*) INTO n FROM users
   WHERE NOT (octet_length(email::text) <= 254 AND (
           (email::text ~ '^[a-z0-9!#$%&''*+/=?^_`{|}~-]+(\.[a-z0-9!#$%&''*+/=?^_`{|}~-]+)*@([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
            AND octet_length(split_part(email::text, '@', 1)) <= 64)
           OR email::text ~ '^deleted\+[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}@invalid$'));
  IF n > 0 THEN
    RAISE EXCEPTION 'users: строк с почтой не в канонической форме — % (CHECK users_email_format, форма platform/emailaddr, спека identity §5.7): приведите почту к нижнему регистру и форме emailaddr.Normalize и повторите migrate up', n;
  END IF;
  SELECT count(*) INTO n FROM captain_applications
   WHERE status = 'rejected'
      OR (status IN ('approved', 'rejected')) <> (reviewed_at IS NOT NULL)
      OR NOT coalesce(contact_link ~* '^https://' AND char_length(contact_link) <= 300, true)
      OR NOT coalesce(char_length(note) <= 1000, true)
      OR NOT coalesce(char_length(proposed_format) <= 200 AND char_length(proposed_pitch) <= 200
                      AND char_length(play_frequency) <= 200, true);
  IF n > 0 THEN
    RAISE EXCEPTION 'captain_applications: строк, не проходящих новые проверки — % (отклонённая без кода причины, решённая без времени решения, ссылка не https или длиннее 300, длины полей формы; спека identity §5.2, §7.3): исправьте или удалите их и повторите migrate up', n;
  END IF;
  SELECT count(*) INTO n FROM sessions;
  IF n > 0 THEN
    RAISE EXCEPTION 'sessions: строк — % (0025 перестраивает sessions, старые сессии не переносятся, спека identity §5.2): удалите их (игроки войдут заново) и повторите migrate up', n;
  END IF;
END
$$;
-- +goose StatementEnd

CREATE TYPE auth_code_purpose AS ENUM ('signup', 'password_reset', 'email_change');
CREATE TYPE auth_challenge_purpose AS ENUM ('mfa', 'social_signup', 'social_nonce', 'webauthn_register',
    'webauthn_login', 'webauthn_mfa', 'webauthn_reauth', 'device_attest', 'deletion_restore');
CREATE TYPE legal_document AS ENUM ('terms', 'privacy');
CREATE TYPE client_kind AS ENUM ('mobile', 'web');
CREATE TYPE device_platform AS ENUM ('ios', 'android', 'web');
CREATE TYPE auth_method AS ENUM ('password', 'apple', 'google', 'passkey');
CREATE TYPE identity_provider AS ENUM ('apple', 'google');

-- ---------------------------------------------------------------------------
-- users (§5.2). Игровые колонки (primary_position … avatar_item_id) не трогаются — их переносит спека players.
-- ---------------------------------------------------------------------------

ALTER TABLE users DROP CONSTRAINT users_contact_present;
ALTER TABLE users
    ALTER COLUMN email SET NOT NULL,
    ALTER COLUMN email_verified_at SET NOT NULL,
    -- версия агрегата: события и оптимистичные правки
    ADD COLUMN version bigint NOT NULL DEFAULT 1,
    -- user handle WebAuthn (32 случайных байта): NULL до первого ключа доступа
    ADD COLUMN webauthn_handle bytea CONSTRAINT users_webauthn_handle_key UNIQUE,
    ADD COLUMN last_login_at timestamptz,
    -- период отмены удаления (§3.11): заданы ⇔ status = 'pending_deletion'
    ADD COLUMN deletion_requested_at timestamptz,
    ADD COLUMN purge_at timestamptz,
    ADD CONSTRAINT users_version_check CHECK (version > 0),
    ADD CONSTRAINT users_webauthn_handle_len CHECK (octet_length(webauthn_handle) = 32),
    ADD CONSTRAINT users_deletion_pending CHECK (
        (status = 'pending_deletion') = (deletion_requested_at IS NOT NULL)
        AND (status = 'pending_deletion') = (purge_at IS NOT NULL)),
    -- каноническая ASCII-форма platform/emailaddr (§5.7: нижний регистр, dot-atom ≤ 64, домен A-label — в том числе
    -- из одной метки, как localhost и xn--p1ai, ≤ 254 байт) или заглушка анонимизации deleted+<uuid>@invalid (R24 плана I-2). Сравнение — по text:
    -- у citext оператор ~ регистр не различает
    ADD CONSTRAINT users_email_format CHECK (
        octet_length(email::text) <= 254 AND (
            (email::text ~ '^[a-z0-9!#$%&''*+/=?^_`{|}~-]+(\.[a-z0-9!#$%&''*+/=?^_`{|}~-]+)*@([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'
             AND octet_length(split_part(email::text, '@', 1)) <= 64)
            OR email::text ~ '^deleted\+[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}@invalid$'));

-- анонимизация по сроку (воркер)
CREATE INDEX users_purge_idx ON users (purge_at) WHERE purge_at IS NOT NULL;

-- пароль: argon2id (platform/password); строки нет — пароль не задан (вход только провайдером)
CREATE TABLE credentials (
    user_id       uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash text        NOT NULL,
    updated_at    timestamptz NOT NULL
);

-- ---------------------------------------------------------------------------
-- Устройства (§3.8): install_id — только HMAC кольца устройств (метка identity-device).
-- ---------------------------------------------------------------------------

CREATE TABLE devices (
    id                uuid            PRIMARY KEY,
    platform          device_platform NOT NULL,
    install_mac       bytea           NOT NULL,
    key_id            text            NOT NULL,
    model             text,
    os_version        text,
    app_version       text,
    -- регистраций с устройства (правило домена §3.8)
    signups           smallint        NOT NULL DEFAULT 0,
    attest_key_id     bytea           CONSTRAINT devices_attest_key_id_key UNIQUE,
    attest_public_key bytea,
    attest_counter    bigint,
    attested_at       timestamptz,
    first_seen_at     timestamptz     NOT NULL,
    last_seen_at      timestamptz     NOT NULL,
    CONSTRAINT devices_install_key UNIQUE (platform, install_mac),
    CONSTRAINT devices_install_mac_len CHECK (octet_length(install_mac) = 32),
    CONSTRAINT devices_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$'),
    CONSTRAINT devices_signups_check CHECK (signups BETWEEN 0 AND 3),
    CONSTRAINT devices_strings_check CHECK (char_length(model) <= 64 AND char_length(os_version) <= 64
        AND char_length(app_version) <= 64)
);

CREATE TABLE device_accounts (
    device_id     uuid        NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    signed_up     boolean     NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    PRIMARY KEY (device_id, user_id)
);

CREATE INDEX device_accounts_user_idx ON device_accounts (user_id);
CREATE INDEX device_accounts_seen_idx ON device_accounts (device_id, last_seen_at);

-- ---------------------------------------------------------------------------
-- Сессии (§5.3): строка = вход (семья refresh-токенов). Таблица пуста (проверка выше) — пересоздаётся.
-- ---------------------------------------------------------------------------

DROP TABLE sessions;

CREATE TABLE sessions (
    id                  uuid        PRIMARY KEY, -- sid в JWT
    user_id             uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- NULL — веб без метки BFF (R18 плана I-2)
    device_id           uuid        REFERENCES devices (id),
    client_kind         client_kind NOT NULL,
    auth_method         auth_method NOT NULL,
    -- последний второй фактор (вход, свежий вход TOTP/резервным кодом)
    mfa_at              timestamptz,
    reauth_at           timestamptz,
    -- неудачи свежего входа: окно 15 минут от первой (reauth_failed_at), успех обнуляет (R16 плана I-2)
    reauth_failures     smallint    NOT NULL DEFAULT 0,
    reauth_failed_at    timestamptz,
    ip                  inet        NOT NULL,
    ip_city             text,
    user_agent          text,
    app_version         text,
    created_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    idle_expires_at     timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at          timestamptz,
    revoked_reason      text,
    CONSTRAINT sessions_reauth_failures_check CHECK (reauth_failures >= 0),
    CONSTRAINT sessions_user_agent_check CHECK (char_length(user_agent) <= 512),
    CONSTRAINT sessions_revoked_pair CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL)),
    -- список — domain.RevokedReason
    CONSTRAINT sessions_revoked_reason_check CHECK (revoked_reason IN ('logout', 'revoked', 'revoked_others',
        'password_changed', 'password_reset', 'refresh_reuse', 'account_deleted', 'banned', 'expired'))
);

CREATE INDEX sessions_user_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_absolute_expires_idx ON sessions (absolute_expires_at);

-- refresh-токены семьи: только SHA-256; преемник в окне гонки 15 с — token.SealSuccessor
CREATE TABLE refresh_tokens (
    id              uuid        PRIMARY KEY,
    session_id      uuid        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    token_hash      bytea       NOT NULL CONSTRAINT refresh_tokens_token_hash_key UNIQUE,
    created_at      timestamptz NOT NULL,
    used_at         timestamptz,
    successor_id    uuid,
    successor_enc   bytea,
    successor_until timestamptz,
    CONSTRAINT refresh_tokens_token_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT refresh_tokens_successor_pair CHECK ((successor_enc IS NULL) = (successor_until IS NULL))
);

CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);

-- ---------------------------------------------------------------------------
-- Коды из письма и токены шагов (§3.14, §5.4)
-- ---------------------------------------------------------------------------

-- Код: HMAC (code_mac, метка identity-code) и шифротекст для письма (code_enc, aad — id строки). Приманка (decoy)
-- выравнивает ответ на «нет аккаунта» (§9.1) — без секретов.
CREATE TABLE auth_codes (
    id           uuid              PRIMARY KEY,
    purpose      auth_code_purpose NOT NULL,
    email        citext            NOT NULL,
    user_id      uuid              REFERENCES users (id) ON DELETE CASCADE,
    decoy        boolean           NOT NULL,
    code_mac     bytea,
    code_enc     bytea,
    -- данные регистрации до кода (хэш пароля, ник, дата рождения, город, язык)
    payload_enc  bytea,
    key_id       text,
    attempts     smallint          NOT NULL DEFAULT 0,
    sends        smallint          NOT NULL DEFAULT 1,
    last_sent_at timestamptz       NOT NULL,
    expires_at   timestamptz       NOT NULL,
    consumed_at  timestamptz,
    revoked_at   timestamptz,
    ip           inet              NOT NULL,
    device_id    uuid              REFERENCES devices (id),
    created_at   timestamptz       NOT NULL,
    CONSTRAINT auth_codes_attempts_check CHECK (attempts BETWEEN 0 AND 5),
    CONSTRAINT auth_codes_sends_check CHECK (sends BETWEEN 1 AND 4),
    CONSTRAINT auth_codes_decoy_no_secret CHECK (NOT decoy OR (code_mac IS NULL AND code_enc IS NULL AND payload_enc IS NULL)),
    CONSTRAINT auth_codes_real_has_mac CHECK (decoy OR code_mac IS NOT NULL),
    CONSTRAINT auth_codes_code_mac_len CHECK (octet_length(code_mac) = 32),
    -- HMAC и шифротекст — ключом key_id
    CONSTRAINT auth_codes_secret_key CHECK ((code_mac IS NULL AND code_enc IS NULL AND payload_enc IS NULL) OR key_id IS NOT NULL),
    CONSTRAINT auth_codes_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$'),
    -- закрытый код не прочитать для письма
    CONSTRAINT auth_codes_closed_no_code CHECK ((consumed_at IS NULL AND revoked_at IS NULL) OR code_enc IS NULL)
);

-- один живой код на (назначение, почта)
CREATE UNIQUE INDEX auth_codes_live_key ON auth_codes (purpose, email) WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX auth_codes_expires_idx ON auth_codes (expires_at);
-- гашение кодов аккаунта (удаление, смена почты)
CREATE INDEX auth_codes_user_idx ON auth_codes (user_id) WHERE user_id IS NOT NULL;

-- токены шагов (mfa_token, restore_token, WebAuthn, соцвход): только SHA-256
CREATE TABLE auth_challenges (
    id          uuid                   PRIMARY KEY,
    purpose     auth_challenge_purpose NOT NULL,
    token_hash  bytea                  CONSTRAINT auth_challenges_token_hash_key UNIQUE,
    user_id     uuid                   REFERENCES users (id) ON DELETE CASCADE,
    session_id  uuid                   REFERENCES sessions (id) ON DELETE CASCADE,
    -- несекретное: сессия WebAuthn, провайдер
    data        jsonb,
    data_enc    bytea,
    key_id      text,
    attempts    smallint               NOT NULL DEFAULT 0,
    expires_at  timestamptz            NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz            NOT NULL,
    CONSTRAINT auth_challenges_token_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT auth_challenges_data_key CHECK (data_enc IS NULL OR key_id IS NOT NULL),
    CONSTRAINT auth_challenges_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$'),
    CONSTRAINT auth_challenges_attempts_check CHECK (attempts BETWEEN 0 AND 5)
);

CREATE INDEX auth_challenges_expires_idx ON auth_challenges (expires_at);

-- ---------------------------------------------------------------------------
-- 2FA (§3.3): секрет TOTP — шифротекст (aad — id пользователя), резервные коды — HMAC (метка identity-recovery)
-- ---------------------------------------------------------------------------

CREATE TABLE user_mfa (
    user_id              uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret_enc           bytea       NOT NULL,
    key_id               text        NOT NULL,
    -- NULL — настройка не подтверждена
    confirmed_at         timestamptz,
    -- шаг последнего принятого кода: повтор и коды старше отвергаются
    last_used_step       bigint,
    failures             smallint    NOT NULL DEFAULT 0,
    failures_notified_at timestamptz,
    created_at           timestamptz NOT NULL,
    CONSTRAINT user_mfa_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$'),
    CONSTRAINT user_mfa_last_used_step_check CHECK (last_used_step >= 0),
    CONSTRAINT user_mfa_failures_check CHECK (failures >= 0)
);

CREATE TABLE user_recovery_codes (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_mac   bytea       NOT NULL,
    key_id     text        NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, code_mac),
    CONSTRAINT user_recovery_codes_code_mac_len CHECK (octet_length(code_mac) = 32),
    CONSTRAINT user_recovery_codes_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$')
);

-- ротация кольца (R28 плана I-2): сколько неиспользованных кодов ещё на старом ключе
CREATE INDEX user_recovery_codes_key_idx ON user_recovery_codes (key_id) WHERE used_at IS NULL;

-- ---------------------------------------------------------------------------
-- Соцвход и ключи доступа (§3.5, §3.6; сценарии — I-3)
-- ---------------------------------------------------------------------------

CREATE TABLE user_identities (
    id                     uuid              PRIMARY KEY,
    user_id                uuid              NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider               identity_provider NOT NULL,
    subject                text              NOT NULL,
    email                  citext,
    email_is_private_relay boolean           NOT NULL,
    -- refresh-токен Apple (отзыв при отвязке и удалении), aad — id привязки
    apple_refresh_enc      bytea,
    key_id                 text,
    linked_at              timestamptz       NOT NULL,
    last_login_at          timestamptz,
    CONSTRAINT user_identities_subject_key UNIQUE (provider, subject),
    CONSTRAINT user_identities_user_provider_key UNIQUE (user_id, provider),
    CONSTRAINT user_identities_subject_check CHECK (char_length(subject) BETWEEN 1 AND 255),
    CONSTRAINT user_identities_apple_refresh CHECK (apple_refresh_enc IS NULL OR (provider = 'apple' AND key_id IS NOT NULL)),
    CONSTRAINT user_identities_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$')
);

CREATE TABLE passkeys (
    id                 uuid        PRIMARY KEY,
    user_id            uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id      bytea       NOT NULL CONSTRAINT passkeys_credential_id_key UNIQUE,
    -- открытый ключ COSE — не секрет
    public_key         bytea       NOT NULL,
    aaguid             uuid        NOT NULL,
    sign_count         bigint      NOT NULL,
    transports         text[]      NOT NULL DEFAULT '{}',
    backup_eligible    boolean     NOT NULL,
    backup_state       boolean     NOT NULL,
    attestation_format text        NOT NULL,
    name               text        NOT NULL,
    created_at         timestamptz NOT NULL,
    last_used_at       timestamptz,
    CONSTRAINT passkeys_credential_id_len CHECK (octet_length(credential_id) BETWEEN 16 AND 1023),
    CONSTRAINT passkeys_sign_count_check CHECK (sign_count >= 0),
    CONSTRAINT passkeys_name_check CHECK (char_length(name) BETWEEN 1 AND 64)
);

CREATE INDEX passkeys_user_idx ON passkeys (user_id);

-- ---------------------------------------------------------------------------
-- События безопасности, ограничения, согласия, сбросы 2FA поддержкой
-- ---------------------------------------------------------------------------

-- meta — без ПД; адрес для писем после смены почты и удаления — meta_enc (стирается чисткой через 7 дней)
CREATE TABLE security_events (
    id         uuid        PRIMARY KEY,
    user_id    uuid        REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    session_id uuid,
    device_id  uuid,
    ip         inet,
    ip_city    text,
    user_agent text,
    meta       jsonb       NOT NULL DEFAULT '{}',
    meta_enc   bytea,
    key_id     text,
    created_at timestamptz NOT NULL,
    CONSTRAINT security_events_kind_format CHECK (kind ~ '^[a-z][a-z_]*$'),
    CONSTRAINT security_events_meta_object CHECK (jsonb_typeof(meta) = 'object'),
    CONSTRAINT security_events_user_agent_check CHECK (char_length(user_agent) <= 512),
    CONSTRAINT security_events_meta_key CHECK (meta_enc IS NULL OR key_id IS NOT NULL),
    CONSTRAINT security_events_key_id_format CHECK (key_id ~ '^[A-Za-z0-9_-]{11}$')
);

CREATE INDEX security_events_user_idx ON security_events (user_id, created_at DESC);
CREATE INDEX security_events_created_idx ON security_events (created_at);

-- ограничения игрока (санкции moderation); until NULL — бессрочно
CREATE TABLE user_restrictions (
    id         uuid        PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    capability text        NOT NULL,
    until      timestamptz,
    source_id  uuid        NOT NULL,
    created_at timestamptz NOT NULL,
    lifted_at  timestamptz,
    CONSTRAINT user_restrictions_capability_format CHECK (capability ~ '^[a-z][a-z_]*$')
);

CREATE INDEX user_restrictions_user_idx ON user_restrictions (user_id) WHERE lifted_at IS NULL;

-- согласие с версией документа (§3.16)
CREATE TABLE consents (
    user_id     uuid           NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    document    legal_document NOT NULL,
    version     text           NOT NULL,
    accepted_at timestamptz    NOT NULL,
    client_kind client_kind    NOT NULL,
    PRIMARY KEY (user_id, document, version),
    CONSTRAINT consents_version_check CHECK (char_length(version) BETWEEN 1 AND 32)
);

CREATE INDEX consents_latest_idx ON consents (user_id, document, accepted_at DESC);

-- Сброс 2FA поддержкой (§3.15): строку пишет исполнитель подтверждения (approvals у api недоступна — без FK);
-- исполняется не раньше execute_at.
CREATE TABLE mfa_resets (
    id                    uuid        PRIMARY KEY,
    user_id               uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    approval_id           uuid        NOT NULL CONSTRAINT mfa_resets_approval_key UNIQUE,
    requested_by_staff_id uuid        NOT NULL,
    requested_by_label    text        NOT NULL,
    reason                text        NOT NULL,
    ticket                text,
    execute_at            timestamptz NOT NULL,
    executed_at           timestamptz,
    cancelled_at          timestamptz,
    -- owner | staff:<имя> | system
    cancelled_by          text,
    created_at            timestamptz NOT NULL,
    CONSTRAINT mfa_resets_requested_by_label_check CHECK (char_length(btrim(requested_by_label)) BETWEEN 1 AND 120),
    CONSTRAINT mfa_resets_reason_check CHECK (char_length(reason) BETWEEN 3 AND 500),
    CONSTRAINT mfa_resets_ticket_check CHECK (char_length(ticket) <= 100),
    CONSTRAINT mfa_resets_one_outcome CHECK (executed_at IS NULL OR cancelled_at IS NULL),
    CONSTRAINT mfa_resets_cancelled_pair CHECK ((cancelled_at IS NULL) = (cancelled_by IS NULL)),
    CONSTRAINT mfa_resets_cancelled_by_format CHECK (cancelled_by ~ '^(owner|system|staff:.+)$')
);

-- один живой сброс на аккаунт
CREATE UNIQUE INDEX mfa_resets_live_key ON mfa_resets (user_id) WHERE executed_at IS NULL AND cancelled_at IS NULL;
-- исполнение по сроку
CREATE INDEX mfa_resets_due_idx ON mfa_resets (execute_at) WHERE executed_at IS NULL AND cancelled_at IS NULL;

-- ---------------------------------------------------------------------------
-- Связь «сотрудник → учётка игрока» (§7.6, модуль staff; права только admin — ветка staff_* в grants.sql). Входа
-- не даёт: только запрет действий над собой (identity.self_action).
-- ---------------------------------------------------------------------------

CREATE TABLE staff_player_links (
    staff_id   uuid        PRIMARY KEY REFERENCES staff_accounts (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL CONSTRAINT staff_player_links_user_id_key UNIQUE REFERENCES users (id),
    created_by uuid        NOT NULL,
    created_at timestamptz NOT NULL
);

-- ---------------------------------------------------------------------------
-- Роли и заявки (§5.2, §7.3)
-- ---------------------------------------------------------------------------

ALTER TABLE role_assignments
    DROP COLUMN granted_by,
    -- staff_* у api недоступны — без FK
    ADD COLUMN granted_by_staff_id uuid,
    ADD COLUMN application_id uuid REFERENCES captain_applications (id),
    ADD COLUMN revoked_by_staff_id uuid,
    ADD COLUMN revoke_reason text,
    ADD CONSTRAINT role_assignments_revoke_reason_check CHECK (char_length(revoke_reason) <= 500);

ALTER TABLE captain_applications
    DROP COLUMN reviewed_by,
    DROP COLUMN rejection_reason,
    -- необязательный контакт, не подтверждён (§3.12)
    ADD COLUMN phone text,
    ADD COLUMN reviewed_by_staff_id uuid,
    ADD COLUMN reviewed_by_label text,
    ADD COLUMN rejection_code text,
    -- комментарий модератора, виден игроку
    ADD COLUMN comment text,
    ADD COLUMN closed_at timestamptz,
    ADD COLUMN version bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT captain_applications_phone_format CHECK (phone ~ '^\+[1-9][0-9]{6,14}$'),
    ADD CONSTRAINT captain_applications_rejection_code_check CHECK (rejection_code IN ('profile_incomplete',
        'insufficient_info', 'contact_unreachable', 'suspicious_activity', 'duplicate_account', 'other')),
    ADD CONSTRAINT captain_applications_comment_check CHECK (char_length(comment) <= 1000),
    ADD CONSTRAINT captain_applications_form_lengths CHECK (char_length(note) <= 1000
        AND char_length(proposed_format) <= 200 AND char_length(proposed_pitch) <= 200
        AND char_length(play_frequency) <= 200),
    ADD CONSTRAINT captain_applications_contact_link_check CHECK (contact_link ~* '^https://'
        AND char_length(contact_link) <= 300),
    ADD CONSTRAINT captain_applications_reviewed_by_label_check CHECK (char_length(btrim(reviewed_by_label)) BETWEEN 1 AND 120),
    ADD CONSTRAINT captain_applications_version_check CHECK (version > 0),
    -- решённая ⇔ есть время решения; отклонённая ⇔ есть код причины; other — с комментарием (§7.3)
    ADD CONSTRAINT captain_applications_reviewed CHECK ((status IN ('approved', 'rejected')) = (reviewed_at IS NOT NULL)),
    ADD CONSTRAINT captain_applications_rejected_code CHECK ((status = 'rejected') = (rejection_code IS NOT NULL)),
    ADD CONSTRAINT captain_applications_other_comment CHECK (rejection_code IS DISTINCT FROM 'other'
        OR coalesce(btrim(comment), '') <> '');

-- ---------------------------------------------------------------------------
-- Экспортированные представления (§5.6): без почты, телефона и даты рождения.
-- ---------------------------------------------------------------------------

-- hidden — не показывать в выдачах (период удаления и удалённые); у удалённого — ник-заглушка
-- deleted-<12 hex> из последних 6 байт id (domain.DeletedNickname), даже если анонимизация ещё не записала её
CREATE VIEW identity_read_users AS
SELECT id,
       CASE WHEN status = 'deleted' THEN 'deleted-' || right(replace(id::text, '-', ''), 12) ELSE nickname END AS nickname,
       status,
       city_id,
       locale,
       created_at,
       status = 'deleted' AS deleted,
       status IN ('pending_deletion', 'deleted') AS hidden
  FROM users;

-- аккаунты с общим устройством — пара в обе стороны; last_seen_at — последнее появление любого из пары
CREATE VIEW identity_read_linked_accounts AS
SELECT a.user_id,
       b.user_id AS linked_user_id,
       a.device_id,
       greatest(a.last_seen_at, b.last_seen_at) AS last_seen_at
  FROM device_accounts a
  JOIN device_accounts b ON b.device_id = a.device_id AND b.user_id <> a.user_id;

-- живые допуски капитана в городе; испытательный срок — у одобренной заявки (без заявки — нет)
CREATE VIEW identity_read_captain_permits AS
SELECT r.user_id,
       r.scope_id AS city_id,
       r.granted_at,
       r.application_id,
       coalesce(a.is_probationary, false) AS is_probationary
  FROM role_assignments r
  LEFT JOIN captain_applications a ON a.id = r.application_id
 WHERE r.role = 'captain' AND r.scope_type = 'city' AND r.revoked_at IS NULL;

-- действующие ограничения
CREATE VIEW identity_read_restrictions AS
SELECT user_id, capability, until
  FROM user_restrictions
 WHERE lifted_at IS NULL AND (until IS NULL OR until > now());

-- ---------------------------------------------------------------------------
-- Флаги способов входа (§4.1): только «везде» (flags.Rules: GlobalOnly; auth_google требует auth_apple). Основные —
-- выключены, аварийные auth_*_login — включены (выключение — авария: вход этим способом закрыт всем).
-- ---------------------------------------------------------------------------

INSERT INTO feature_flags (key, description, enabled_globally, enabled_city_ids, version) VALUES
    ('auth_apple',          'Новые регистрации и привязки через Apple',                   false, '{}', 1),
    ('auth_google',         'Новые регистрации и привязки через Google (требует auth_apple)', false, '{}', 1),
    ('auth_passkeys',       'Добавление ключей доступа',                                  false, '{}', 1),
    ('auth_apple_login',    'Вход через Apple; выключение — авария',                      true,  '{}', 1),
    ('auth_google_login',   'Вход через Google; выключение — авария',                     true,  '{}', 1),
    ('auth_passkeys_login', 'Вход ключом доступа, ключ как второй фактор; выключение — авария', true, '{}', 1)
ON CONFLICT (key) DO NOTHING;

-- +goose Down

-- Возвращает схему 0024 в точности. Данные новых таблиц, сессии, связи сотрудников с игроками, авторы-сотрудники
-- допусков и решений по заявкам при откате теряются (README).
DELETE FROM feature_flags WHERE key IN
    ('auth_apple', 'auth_google', 'auth_passkeys', 'auth_apple_login', 'auth_google_login', 'auth_passkeys_login');

DROP VIEW identity_read_restrictions;
DROP VIEW identity_read_captain_permits;
DROP VIEW identity_read_linked_accounts;
DROP VIEW identity_read_users;

ALTER TABLE captain_applications
    DROP CONSTRAINT captain_applications_other_comment,
    DROP CONSTRAINT captain_applications_rejected_code,
    DROP CONSTRAINT captain_applications_reviewed,
    DROP CONSTRAINT captain_applications_version_check,
    DROP CONSTRAINT captain_applications_reviewed_by_label_check,
    DROP CONSTRAINT captain_applications_contact_link_check,
    DROP CONSTRAINT captain_applications_form_lengths,
    DROP CONSTRAINT captain_applications_comment_check,
    DROP CONSTRAINT captain_applications_rejection_code_check,
    DROP CONSTRAINT captain_applications_phone_format,
    DROP COLUMN version,
    DROP COLUMN closed_at,
    DROP COLUMN comment,
    DROP COLUMN rejection_code,
    DROP COLUMN reviewed_by_label,
    DROP COLUMN reviewed_by_staff_id,
    DROP COLUMN phone,
    ADD COLUMN reviewed_by uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN rejection_reason text;

ALTER TABLE role_assignments
    DROP CONSTRAINT role_assignments_revoke_reason_check,
    DROP COLUMN revoke_reason,
    DROP COLUMN revoked_by_staff_id,
    DROP COLUMN application_id,
    DROP COLUMN granted_by_staff_id,
    ADD COLUMN granted_by uuid REFERENCES users (id) ON DELETE SET NULL;

DROP TABLE staff_player_links;
DROP TABLE mfa_resets;
DROP TABLE consents;
DROP TABLE user_restrictions;
DROP TABLE security_events;
DROP TABLE passkeys;
DROP TABLE user_identities;
DROP TABLE user_recovery_codes;
DROP TABLE user_mfa;
DROP TABLE auth_challenges;
DROP TABLE auth_codes;
DROP TABLE refresh_tokens;
DROP TABLE sessions;
DROP TABLE device_accounts;
DROP TABLE devices;
DROP TABLE credentials;

-- sessions схемы 0003
CREATE TABLE sessions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_hash   text        NOT NULL,
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

DROP INDEX users_purge_idx;
ALTER TABLE users
    DROP CONSTRAINT users_email_format,
    DROP CONSTRAINT users_deletion_pending,
    DROP CONSTRAINT users_webauthn_handle_len,
    DROP CONSTRAINT users_version_check,
    DROP COLUMN purge_at,
    DROP COLUMN deletion_requested_at,
    DROP COLUMN last_login_at,
    DROP COLUMN webauthn_handle,
    DROP COLUMN version,
    ALTER COLUMN email_verified_at DROP NOT NULL,
    ALTER COLUMN email DROP NOT NULL,
    ADD CONSTRAINT users_contact_present CHECK (email IS NOT NULL OR phone IS NOT NULL);

DROP TYPE identity_provider;
DROP TYPE auth_method;
DROP TYPE device_platform;
DROP TYPE client_kind;
DROP TYPE legal_document;
DROP TYPE auth_challenge_purpose;
DROP TYPE auth_code_purpose;
