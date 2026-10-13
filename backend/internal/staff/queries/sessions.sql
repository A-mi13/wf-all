-- Сессии сотрудников (спека сотрудников §4.7). Токен — только SHA-256 (token_hash).

-- name: InsertSession :exec
INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, mfa_at, expires_at, ip, user_agent)
VALUES (sqlc.arg(id), sqlc.arg(staff_id), sqlc.arg(token_hash), sqlc.arg(stage)::staff_session_stage,
        sqlc.arg(created_at), sqlc.arg(last_seen_at), sqlc.narg(mfa_at), sqlc.arg(expires_at), sqlc.arg(ip),
        sqlc.arg(user_agent));

-- name: LockSession :one
-- Живая или погашенная — решает сценарий (заменённая — гонка двух вкладок).
SELECT id, staff_id, stage::text AS stage, created_at, expires_at, revoked_reason
FROM staff_sessions
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: RevokeSession :execrows
UPDATE staff_sessions SET revoked_at = sqlc.arg(now)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeStaffSessions :execrows
UPDATE staff_sessions SET revoked_at = sqlc.arg(now)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE staff_id = sqlc.arg(staff_id) AND revoked_at IS NULL;

-- name: LoadSession :one
-- Загрузчик сессии admin-api: один запрос на запрос — сессия, учётка, роль и города модератора.
SELECT s.id, s.staff_id, s.stage::text AS stage, s.last_seen_at, s.expires_at, s.ip,
       s.revoked_at, s.revoked_reason,
       a.status::text AS status, a.locked_until,
       r.role::text AS role,
       coalesce((SELECT array_agg(c.city_id ORDER BY c.city_id) FROM staff_role_cities c
                 WHERE c.staff_id = s.staff_id), '{}')::uuid[] AS city_ids
FROM staff_sessions s
JOIN staff_accounts a ON a.id = s.staff_id
JOIN staff_roles r ON r.staff_id = s.staff_id
WHERE s.token_hash = sqlc.arg(token_hash);

-- name: TouchSession :exec
-- Отметка активности не чаще раза в минуту на сессию (seen_before — граница) и адрес клиента.
UPDATE staff_sessions SET last_seen_at = sqlc.arg(now), ip = sqlc.arg(ip)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL AND last_seen_at <= sqlc.arg(seen_before);

-- name: GetLockedMail :one
-- Письмо staff.locked: кто закрыт и откуда — по сессии, на которой закрылась учётка.
SELECT s.ip, s.revoked_at, s.revoked_reason, a.display_name, a.email::text AS email, a.locked_until
FROM staff_sessions s
JOIN staff_accounts a ON a.id = s.staff_id
WHERE s.id = sqlc.arg(id);

-- name: GetMailRecipient :one
-- Получатель письма: адрес, язык, статус и роль (письмо уходит только активному адресату нужной роли).
SELECT a.email::text AS email, a.locale, a.status::text AS status, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id);
