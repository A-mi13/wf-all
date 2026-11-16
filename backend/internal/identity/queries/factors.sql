-- Пароль, второй фактор и состояние аккаунта для завершения входа (спека identity §3.2, §3.3; план I-2, Task 9).

-- name: GetPasswordHash :one
SELECT password_hash FROM credentials WHERE user_id = $1;

-- name: RehashPassword :execrows
-- условная запись: пароль сменили между Verify и записью — 0 строк, новый хэш не затирается
UPDATE credentials SET password_hash = @new_hash, updated_at = @now
WHERE user_id = @user_id AND password_hash = @seen_hash;

-- name: LockUserMfa :one
SELECT * FROM user_mfa WHERE user_id = $1 FOR UPDATE;

-- name: MfaAccepted :exec
UPDATE user_mfa SET last_used_step = coalesce(sqlc.narg(step), last_used_step), failures = 0 WHERE user_id = @user_id;

-- name: MfaFailed :one
-- счётчик насыщается у предела smallint (I1 итогового ревью): иначе неудача на 32767 — 22003 и откат tx, а с ним лимит
-- user:mfa, попытка mfa_token и письмо mfa_failures
UPDATE user_mfa SET failures = least(failures, 32766) + 1 WHERE user_id = $1 RETURNING failures, failures_notified_at;

-- name: MfaFailuresNotified :exec
UPDATE user_mfa SET failures_notified_at = @now WHERE user_id = @user_id;

-- name: LockRecoveryKeys :many
-- ключи живых резервных кодов аккаунта; строки — под замком до погашения
SELECT DISTINCT key_id FROM (SELECT key_id FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL FOR UPDATE) k;

-- name: BurnRecoveryCode :execrows
UPDATE user_recovery_codes SET used_at = @now
WHERE user_id = @user_id AND code_mac = @code_mac AND key_id = @key_id AND used_at IS NULL;

-- name: RecoveryCodesLeft :one
SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL;

-- name: MfaConfirmed :one
SELECT EXISTS (SELECT 1 FROM user_mfa WHERE user_id = $1 AND confirmed_at IS NOT NULL);

-- name: HasPasskeys :one
SELECT EXISTS (SELECT 1 FROM passkeys WHERE user_id = $1);

-- name: LiveMfaResetAt :one
SELECT execute_at FROM mfa_resets WHERE user_id = $1 AND executed_at IS NULL AND cancelled_at IS NULL;

-- name: BanRestrictions :many
-- действующие ограничения бана: until NULL — бессрочно; истёкшая, ещё не снятая чисткой строка — не в счёт (m2)
SELECT until FROM user_restrictions
WHERE user_id = @user_id AND capability = @capability AND lifted_at IS NULL AND (until IS NULL OR until > sqlc.arg(now)::timestamptz);
