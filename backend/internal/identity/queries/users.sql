-- Аккаунт: замок и версия (план I-2, Task 8). Порядок замков R30: users первым (FOR NO KEY UPDATE).

-- name: LockUser :one
SELECT * FROM users WHERE id = $1 FOR NO KEY UPDATE;

-- name: BumpUserVersion :one
UPDATE users SET version = version + 1 WHERE id = $1 RETURNING version;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = @now WHERE id = @id;
