-- Сброс и смена пароля, свежий вход, смена почты (спека identity §3.4, §3.9, §3.10, §3.13; план I-2, Task 13).

-- name: UpsertPassword :exec
-- новый хэш; аккаунт без пароля (только соцвход) так его получает
INSERT INTO credentials (user_id, password_hash, updated_at) VALUES (@user_id, @password_hash, @now)
ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash, updated_at = EXCLUDED.updated_at;

-- name: ReauthSucceeded :exec
-- свежий вход: reauth_at = now, счёт неудач с нуля; mfa_at — при втором факторе (R27), иначе прежний
UPDATE sessions
SET reauth_at = @now, reauth_failures = 0, reauth_failed_at = NULL, mfa_at = coalesce(sqlc.narg(mfa_at), mfa_at)
WHERE id = @id;

-- name: ReauthFailed :exec
-- неудача свежего входа: счёт и начало окна (R16) считает сценарий
UPDATE sessions SET reauth_failures = @failures, reauth_failed_at = @failed_at WHERE id = @id;

-- name: ChangeUserEmail :one
UPDATE users SET email = @email, email_verified_at = @now, version = version + 1 WHERE id = @id RETURNING version;
