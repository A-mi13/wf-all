-- «Мой доступ» (спека сотрудников §4.7): кто вошёл, свои сессии, пароль, язык. Каждый запрос
-- привязан к staff_id сессии запроса — id из запроса без него не доверяется.

-- name: GetMe :one
-- Учётка и абсолютный срок текущей сессии для GET /v1/staff/me.
SELECT a.email::text AS email, a.display_name, a.locale, s.expires_at
FROM staff_sessions s
JOIN staff_accounts a ON a.id = s.staff_id
WHERE s.id = sqlc.arg(session_id) AND s.staff_id = sqlc.arg(staff_id);

-- name: ListLiveSessions :many
-- Живые сессии сотрудника (не погашены, срок не истёк), новые сверху.
SELECT id, stage::text AS stage, created_at, last_seen_at, expires_at, ip, user_agent
FROM staff_sessions
WHERE staff_id = sqlc.arg(staff_id) AND revoked_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz
ORDER BY created_at DESC, id DESC;

-- name: RevokeOwnSession :execrows
-- Гашение одной живой сессии сотрудника: чужая, несуществующая, погашенная и истёкшая — 0 строк.
UPDATE staff_sessions SET revoked_at = sqlc.arg(now)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE id = sqlc.arg(id) AND staff_id = sqlc.arg(staff_id) AND revoked_at IS NULL
  AND expires_at > sqlc.arg(now)::timestamptz;

-- name: RevokeOtherSessions :execrows
-- Гашение всех живых сессий сотрудника, кроме keep (текущей).
UPDATE staff_sessions SET revoked_at = sqlc.arg(now)::timestamptz, revoked_reason = sqlc.arg(reason)::text
WHERE staff_id = sqlc.arg(staff_id) AND id <> sqlc.arg(keep) AND revoked_at IS NULL
  AND expires_at > sqlc.arg(now)::timestamptz;

-- name: SetPassword :exec
UPDATE staff_accounts SET password_hash = sqlc.arg(password_hash) WHERE id = sqlc.arg(id);

-- name: SetLocale :exec
UPDATE staff_accounts SET locale = sqlc.arg(locale) WHERE id = sqlc.arg(id);
