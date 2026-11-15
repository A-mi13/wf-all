-- 2FA игрока: включение, управление, состояние (спека identity §3.3; план I-2, Task 16). Строки user_mfa и
-- user_recovery_codes — под замком users вызывающего (R30).

-- name: MfaStatus :one
-- состояние для getMyMfa одним чтением: подтверждение, остаток живых кодов, срок живого сброса поддержкой
SELECT m.confirmed_at,
       (SELECT count(*) FROM user_recovery_codes c WHERE c.user_id = u.id AND c.used_at IS NULL)::int AS codes_left,
       r.execute_at AS reset_at
FROM users u
LEFT JOIN user_mfa m ON m.user_id = u.id
LEFT JOIN mfa_resets r ON r.user_id = u.id AND r.executed_at IS NULL AND r.cancelled_at IS NULL
WHERE u.id = @user_id;

-- name: SetupMfa :execrows
-- новый неподтверждённый секрет; подтверждённая 2FA не перезаписывается — 0 строк (mfa_already_enabled)
INSERT INTO user_mfa (user_id, secret_enc, key_id, created_at)
VALUES (@user_id, @secret_enc, @key_id, @now)
ON CONFLICT (user_id) DO UPDATE
SET secret_enc = excluded.secret_enc, key_id = excluded.key_id, created_at = excluded.created_at,
    last_used_step = NULL, failures = 0, failures_notified_at = NULL
WHERE user_mfa.confirmed_at IS NULL;

-- name: ConfirmMfa :execrows
UPDATE user_mfa SET confirmed_at = @now, last_used_step = @step, failures = 0
WHERE user_id = @user_id AND confirmed_at IS NULL;

-- name: DeleteMfa :execrows
DELETE FROM user_mfa WHERE user_id = $1;

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes WHERE user_id = $1;

-- name: InsertRecoveryCodes :exec
INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at)
SELECT @user_id, mac, @key_id, @now FROM unnest(@macs::bytea[]) AS mac;
