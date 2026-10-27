-- Настройка второго фактора и резервные коды (спека сотрудников §4.5, §4.7). Порядок замков — как во
-- входе: учётка → сессия → staff_mfa → резервные коды.

-- name: UpsertPendingMFA :exec
-- Новый неподтверждённый секрет TOTP (MFASetup): повторный вызов до подтверждения заменяет секрет.
-- Подтверждённый секрет не трогается — его отсекает сценарий под замком строки.
INSERT INTO staff_mfa (staff_id, secret_enc, key_id)
VALUES (sqlc.arg(staff_id), sqlc.arg(secret_enc), sqlc.arg(key_id)::text)
ON CONFLICT (staff_id) DO UPDATE
SET secret_enc = excluded.secret_enc, key_id = excluded.key_id, last_used_step = 0
WHERE staff_mfa.confirmed_at IS NULL;

-- name: ConfirmMFA :exec
-- Подтверждение секрета кодом: принятый шаг — в last_used_step.
UPDATE staff_mfa SET confirmed_at = sqlc.arg(now)::timestamptz, last_used_step = sqlc.arg(step)
WHERE staff_id = sqlc.arg(staff_id);

-- name: DeleteMFA :exec
-- Принятие приглашения: второй фактор учётки invited ещё не действует — настраивается заново.
DELETE FROM staff_mfa WHERE staff_id = sqlc.arg(staff_id);

-- name: DeleteRecoveryCodes :exec
-- Новое поколение резервных кодов (и принятие приглашения) удаляет все прежние.
DELETE FROM staff_recovery_codes WHERE staff_id = sqlc.arg(staff_id);

-- name: InsertRecoveryCodes :exec
-- Поколение резервных кодов: HMAC нормализованного кода ключом key_id (один ключ на поколение).
INSERT INTO staff_recovery_codes (staff_id, code_mac, key_id)
SELECT sqlc.arg(staff_id), unnest(sqlc.arg(code_macs)::bytea[]), sqlc.arg(key_id)::text;

-- name: GetMFAStatus :one
-- GET /me/mfa: когда подтверждён второй фактор и сколько резервных кодов не использовано.
SELECT m.confirmed_at,
       (SELECT count(*) FROM staff_recovery_codes c WHERE c.staff_id = a.id AND c.used_at IS NULL) AS codes_left
FROM staff_accounts a
LEFT JOIN staff_mfa m ON m.staff_id = a.id
WHERE a.id = sqlc.arg(staff_id);
