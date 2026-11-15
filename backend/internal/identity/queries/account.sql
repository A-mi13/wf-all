-- Мой аккаунт, ник, документы, мои способы входа (спека identity §3.16, §5.8, §6.2.4; план I-2, Task 14) и
-- Service.User (§10.1). Имена — с префиксом Account: запросы области не пересекаются с соседними файлами пакета.

-- name: AccountUser :one
SELECT * FROM users WHERE id = $1;

-- name: AccountHasPassword :one
SELECT EXISTS (SELECT 1 FROM credentials WHERE user_id = $1);

-- name: AccountLastNicknameChange :one
SELECT created_at FROM nickname_changes WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: AccountRestrictions :many
-- действующие ограничения: until NULL — бессрочно; истёкшая, ещё не снятая чисткой строка — не в счёт
SELECT capability, until FROM user_restrictions
WHERE user_id = @user_id AND lifted_at IS NULL AND (until IS NULL OR until > sqlc.arg(now)::timestamptz)
ORDER BY capability, until;

-- name: AccountLatestConsents :many
-- последняя принятая версия каждого документа (consents хранит историю версий)
SELECT DISTINCT ON (document) document, version FROM consents
WHERE user_id = $1
ORDER BY document, accepted_at DESC, version DESC;

-- name: AccountUpsertConsent :exec
-- повтор той же версии обновляет время: после отката конфига к прежней версии она снова «последняя принятая»
INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
VALUES (@user_id, @document, @version, @now, @client_kind)
ON CONFLICT (user_id, document, version) DO UPDATE
SET accepted_at = EXCLUDED.accepted_at, client_kind = EXCLUDED.client_kind;

-- name: AccountNicknameBusy :one
-- нормализованный ник (§5.8: короче 3 после normalize_text — nickname_invalid), резерв и занятость чужим живым
-- аккаунтом; карантин — AccountNicknameQuarantine
SELECT normalize_text(@nickname::text)::text AS normalized,
       EXISTS (SELECT 1 FROM reserved_nicknames r WHERE r.nickname_normalized = normalize_text(@nickname::text)) AS reserved,
       EXISTS (SELECT 1 FROM users u
               WHERE u.nickname_normalized = normalize_text(@nickname::text) AND u.deleted_at IS NULL
                 AND u.id <> @user_id) AS used;

-- name: AccountNicknameQuarantine :many
-- живой карантин ника: previous_user_id NULL — прежний владелец удалён (чужой)
SELECT previous_user_id FROM nickname_reservations
WHERE nickname_normalized = @nickname_normalized AND expires_at > @now;

-- name: AccountDeleteNicknameReservation :exec
DELETE FROM nickname_reservations WHERE nickname_normalized = @nickname_normalized;

-- name: AccountReserveNickname :exec
-- старый ник в карантин: UPSERT — цепочка A→B→A→C не даёт 23505 (m5)
INSERT INTO nickname_reservations (nickname_normalized, previous_user_id, released_at, expires_at)
VALUES (@nickname_normalized, @user_id, @now, @expires_at)
ON CONFLICT (nickname_normalized) DO UPDATE
SET previous_user_id = EXCLUDED.previous_user_id, released_at = EXCLUDED.released_at, expires_at = EXCLUDED.expires_at;

-- name: AccountSetNickname :one
UPDATE users SET nickname = @nickname, version = version + 1 WHERE id = @id RETURNING version;

-- name: AccountInsertNicknameChange :exec
INSERT INTO nickname_changes (id, user_id, old_value, new_value, created_at)
VALUES (@id, @user_id, @old_value, @new_value, @now);

-- name: AccountSetLocale :one
UPDATE users SET locale = @locale, version = version + 1 WHERE id = @id RETURNING version;

-- name: AccountIdentities :many
SELECT id, provider, email, email_is_private_relay, linked_at FROM user_identities
WHERE user_id = $1 ORDER BY linked_at, id;

-- name: AccountPasskeys :many
SELECT id, name, created_at, last_used_at, backup_state FROM passkeys WHERE user_id = $1 ORDER BY created_at, id;

-- name: MyMfaStatus :one
-- состояние 2FA (getMyMfa, listMyAuthMethods): живой сброс поддержкой — не больше одного (mfa_resets_live_key)
SELECT m.confirmed_at,
       (SELECT count(*) FROM user_recovery_codes r WHERE r.user_id = u.id AND r.used_at IS NULL)::int AS recovery_codes_left,
       mr.execute_at AS reset_scheduled_at
FROM users u
LEFT JOIN user_mfa m ON m.user_id = u.id
LEFT JOIN mfa_resets mr ON mr.user_id = u.id AND mr.executed_at IS NULL AND mr.cancelled_at IS NULL
WHERE u.id = $1;

-- name: UserSummary :one
-- Service.User (§10.1): экспортированное представление, без ПД
SELECT id, nickname::text AS nickname, status, city_id, locale FROM identity_read_users WHERE id = $1;
