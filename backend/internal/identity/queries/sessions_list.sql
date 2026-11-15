-- «Где вы вошли» (спека identity §3.7; план I-2, Task 12): живые сессии аккаунта по часам приложения, новые сверху.
-- IP не выбирается — наружу не отдаётся (только город ip_city).

-- name: ListLiveSessions :many
SELECT s.id, s.client_kind, s.auth_method, s.ip_city, s.user_agent, s.created_at, s.last_seen_at,
       COALESCE(d.platform::text, '')::text AS platform, d.model, d.os_version, COALESCE(d.app_version, s.app_version) AS app_version
FROM sessions s
LEFT JOIN devices d ON d.id = s.device_id AND d.platform <> 'web'
WHERE s.user_id = @user_id AND s.revoked_at IS NULL
  AND s.idle_expires_at > sqlc.arg(now)::timestamptz AND s.absolute_expires_at > sqlc.arg(now)::timestamptz
ORDER BY s.last_seen_at DESC, s.id DESC;
