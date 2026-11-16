-- Выдача, отзыв и свежесть сессий (спека identity §3.7, §3.13; план I-2, Task 8).

-- name: InsertSession :exec
INSERT INTO sessions (id, user_id, device_id, client_kind, auth_method, mfa_at, reauth_at, ip, ip_city, user_agent,
                      app_version, created_at, last_seen_at, idle_expires_at, absolute_expires_at)
VALUES (@id, @user_id, @device_id, @client_kind, @auth_method, @mfa_at, @reauth_at, @ip, @ip_city, @user_agent,
        @app_version, @now, @now, @idle_expires_at, @absolute_expires_at);

-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (id, session_id, token_hash, created_at) VALUES (@id, @session_id, @token_hash, @now);

-- name: DeviceAppVersion :one
SELECT app_version FROM devices WHERE id = $1;

-- name: ExcessLiveSessions :many
-- живые сверх keep, старшие по last_seen_at (R17); текущая (только что выданная) не в списке: при часах другого
-- экземпляра впереди её last_seen_at младше чужих, и она ушла бы в «лишние» (m4 ревью W3) — keep без неё
SELECT id FROM sessions
WHERE user_id = @user_id AND revoked_at IS NULL AND idle_expires_at > @now AND absolute_expires_at > @now
  AND id <> @current
ORDER BY last_seen_at DESC, id DESC
OFFSET sqlc.arg(keep)::int;

-- name: RevokeSessionsByIDs :many
UPDATE sessions SET revoked_at = @now, revoked_reason = @reason
WHERE user_id = @user_id AND id = ANY(@ids::uuid[]) AND revoked_at IS NULL
RETURNING id;

-- name: RevokeUserSessions :many
-- все неотозванные (n1 ревью W3: истёкшая по часам этого сервера может быть живой по часам отстающего — смену пароля
-- и «выйти везде» она не переживёт); live — живая по @now: в ответ и событие (≤ 100 id) идут только живые (m1)
UPDATE sessions SET revoked_at = @now, revoked_reason = @reason
WHERE user_id = @user_id AND revoked_at IS NULL AND id <> @keep_id
RETURNING id, (idle_expires_at > @now AND absolute_expires_at > @now)::boolean AS live;

-- name: SessionUser :one
SELECT user_id FROM sessions WHERE id = $1;

-- name: LockSession :one
SELECT * FROM sessions WHERE id = $1 FOR UPDATE;
