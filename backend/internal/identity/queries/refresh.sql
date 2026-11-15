-- Ротация refresh (спека identity §3.7, бэкенда §6.2; план I-2, Task 11). Порядок замков R30 «аккаунт по строке»:
-- строка токена без замка → lockUser(user_id) → sessions FOR UPDATE → refresh_tokens FOR UPDATE → devices.

-- name: RefreshFind :one
SELECT rt.id, rt.session_id, s.user_id
FROM refresh_tokens rt
JOIN sessions s ON s.id = rt.session_id
WHERE rt.token_hash = @token_hash;

-- name: RefreshLockToken :one
SELECT * FROM refresh_tokens WHERE id = $1 FOR UPDATE;

-- name: RefreshRotate :exec
-- токен использован: преемник и окно гонки (successor_enc — token.SealSuccessor, открыть может только держатель)
UPDATE refresh_tokens
SET used_at = @now, successor_id = @successor_id, successor_enc = @successor_enc, successor_until = @successor_until
WHERE id = @id AND used_at IS NULL;

-- name: RefreshTouchSession :exec
-- продление только срока бездействия (не дальше absolute_expires_at — считает сценарий) и последний визит
UPDATE sessions SET idle_expires_at = @idle_expires_at, last_seen_at = @now WHERE id = @id;

-- name: RefreshDevice :one
SELECT platform, install_mac, key_id FROM devices WHERE id = $1;

-- name: RefreshTouchDevice :exec
UPDATE devices SET last_seen_at = @now WHERE id = @id;
