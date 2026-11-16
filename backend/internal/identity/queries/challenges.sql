-- Токены шагов (mfa_token, restore_token) в auth_challenges: в базе только SHA-256 (спека identity §5.4; план I-2,
-- Task 9). Поиск по токену — без замка, затем lockUser и FOR UPDATE строки (R30).

-- name: InsertChallenge :exec
INSERT INTO auth_challenges (id, purpose, token_hash, user_id, data, expires_at, created_at)
VALUES (@id, @purpose, @token_hash, @user_id, @data, @expires_at, @now);

-- name: FindChallenge :one
SELECT id, user_id FROM auth_challenges WHERE token_hash = @token_hash AND purpose = @purpose;

-- name: LockChallenge :one
SELECT * FROM auth_challenges WHERE id = $1 FOR UPDATE;

-- name: FailChallenge :one
-- неудача по токену: пятая гасит (consumed_at)
UPDATE auth_challenges
SET attempts = attempts + 1,
    consumed_at = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::smallint THEN @now ELSE consumed_at END
WHERE id = @id
RETURNING attempts;

-- name: ConsumeChallenge :exec
UPDATE auth_challenges SET consumed_at = @now WHERE id = @id AND consumed_at IS NULL;

-- name: RevokeUserChallenges :exec
-- смена/сброс пароля и «выйти везде» гасят живые токены шагов аккаунта (mfa, deletion_restore) — под замком users
UPDATE auth_challenges SET consumed_at = @now WHERE user_id = @user_id AND consumed_at IS NULL;
