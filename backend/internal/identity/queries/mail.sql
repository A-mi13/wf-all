-- Сборка писем I-2 воркером (спека identity §8.2; план I-2, Task 19): только чтение под ролью worker (§5.5). Адрес и
-- язык — по ref задачи: auth_codes.id, security_events.id или mfa_resets.id; адресов в аргументах задач нет.

-- name: MailCode :one
SELECT id, purpose, email, user_id, decoy, code_enc, payload_enc, key_id, expires_at, consumed_at, revoked_at
FROM auth_codes WHERE id = @id;

-- name: MailUserByID :one
SELECT email, locale, status FROM users WHERE id = @id;

-- name: MailUserByEmail :one
SELECT locale, status FROM users WHERE email = @email;

-- name: MailSecurityEvent :one
SELECT e.kind, e.meta, e.meta_enc, e.key_id, e.created_at, u.email, u.locale, u.status
FROM security_events e
JOIN users u ON u.id = e.user_id
WHERE e.id = @id;

-- name: MailMfaReset :one
SELECT r.execute_at, r.executed_at, r.cancelled_at, r.cancelled_by, u.email, u.locale, u.status
FROM mfa_resets r
JOIN users u ON u.id = r.user_id
WHERE r.id = @id;
