-- Коды из письма (спека identity §3.14; план I-2, Task 10): регистрация, сброс пароля, смена почты. Код — только
-- HMAC (code_mac) и шифротекст для письма (code_enc); приманка (decoy) — без секретов. Строка ищется без замка, затем
-- lockUser и FOR UPDATE строки с перепроверкой (R30).

-- name: RevokeLiveCode :exec
-- один живой код на (назначение, почта): новый старт гасит прежний
UPDATE auth_codes SET revoked_at = @now, code_enc = NULL
WHERE purpose = @purpose AND email = @email AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: InsertCode :execrows
-- 0 строк — живой код вставила параллельная транзакция: вызывающий гасит его и повторяет вставку
INSERT INTO auth_codes (id, purpose, email, user_id, decoy, code_mac, code_enc, payload_enc, key_id, last_sent_at,
                        expires_at, ip, created_at)
VALUES (@id, @purpose, @email, @user_id, @decoy, @code_mac, @code_enc, @payload_enc, @key_id, @now, @expires_at, @ip,
        @now)
ON CONFLICT (purpose, email) WHERE consumed_at IS NULL AND revoked_at IS NULL DO NOTHING;

-- name: FindCode :one
SELECT id, purpose, user_id FROM auth_codes WHERE id = $1;

-- name: LockCode :one
SELECT * FROM auth_codes WHERE id = $1 FOR UPDATE;

-- name: FailCode :one
-- неверный ввод: attempts + 1, пятая неудача гасит код (revoked_at, code_enc = NULL)
UPDATE auth_codes
SET attempts = attempts + 1,
    revoked_at = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::smallint THEN sqlc.arg(now)::timestamptz ELSE revoked_at END,
    code_enc = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::smallint THEN NULL ELSE code_enc END
WHERE id = @id
RETURNING attempts;

-- name: ConsumeCode :execrows
UPDATE auth_codes SET consumed_at = @now, code_enc = NULL
WHERE id = @id AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: CodeResent :exec
UPDATE auth_codes SET sends = sends + 1, last_sent_at = @now WHERE id = @id;

-- name: LockCodeSlot :exec
-- старты кода на (назначение, почта) — по очереди до конца tx: отзыв прежнего и вставка видят закоммиченный код
-- соседа, вставка не упирается в его незакоммиченную строку (m1 ревью W4-А: три параллельных старта)
SELECT pg_advisory_xact_lock(hashtextextended('identity.auth_code:' || @purpose::text || ':' || @email::text, 0));

-- name: RevokeUserCodes :exec
-- смена почты: живые коды аккаунта (сброс пароля на прежний адрес и прочие) отозваны, секреты стёрты (I2 ревью W4-А)
UPDATE auth_codes SET revoked_at = @now, code_enc = NULL, payload_enc = NULL
WHERE user_id = @user_id AND consumed_at IS NULL AND revoked_at IS NULL;
