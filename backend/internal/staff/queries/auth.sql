-- Вход и второй фактор (спека сотрудников §4.6). Блокировки строк — всегда в порядке
-- учётка → сессия → staff_mfa → резервные коды: иначе блокировка учётки (гасит все сессии) и
-- разблокировка в другой вкладке взаимно ждут.

-- name: GetLoginAccount :one
-- Учётка для шага пароля: статус, хэш, закрыта ли и подтверждён ли второй фактор.
SELECT a.id, a.status, a.password_hash, a.locked_until,
       (m.confirmed_at IS NOT NULL)::bool AS mfa_confirmed
FROM staff_accounts a
LEFT JOIN staff_mfa m ON m.staff_id = a.id
WHERE a.email = sqlc.arg(email)::citext;

-- name: LockAccount :one
-- Учётка под замком на время проверки фактора, записи неудачи или изменения своей учётки: роль и
-- снимок имени — для аудита, хэш пароля — проверка текущего при смене, язык — «было» в аудите.
SELECT a.id, a.status, a.display_name, a.mfa_failures, a.locked_until, a.lock_notified_at, a.version,
       a.password_hash, a.locale, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id)
FOR UPDATE OF a;

-- name: LockMFA :one
SELECT secret_enc, key_id, confirmed_at, last_used_step
FROM staff_mfa
WHERE staff_id = sqlc.arg(staff_id)
FOR UPDATE;

-- name: SetMFAStep :exec
-- Шаг принятого кода TOTP: повтор и коды старше него отвергаются.
UPDATE staff_mfa SET last_used_step = sqlc.arg(step) WHERE staff_id = sqlc.arg(staff_id);

-- name: LockUnusedRecoveryCodes :many
SELECT code_mac, key_id
FROM staff_recovery_codes
WHERE staff_id = sqlc.arg(staff_id) AND used_at IS NULL
ORDER BY code_mac
FOR UPDATE;

-- name: UseRecoveryCode :execrows
-- Резервный код сгорает при любом использовании.
UPDATE staff_recovery_codes SET used_at = sqlc.arg(now)::timestamptz
WHERE staff_id = sqlc.arg(staff_id) AND code_mac = sqlc.arg(code_mac) AND used_at IS NULL;

-- name: FactorSucceeded :exec
-- Успех второго фактора: счётчик неудач подряд — 0; при входе — last_login_at.
UPDATE staff_accounts
SET mfa_failures = 0,
    last_login_at = coalesce(sqlc.narg(login_at), last_login_at)
WHERE id = sqlc.arg(id);

-- name: AddFactorFailure :one
UPDATE staff_accounts SET mfa_failures = mfa_failures + 1
WHERE id = sqlc.arg(id)
RETURNING mfa_failures;

-- name: LockOut :one
-- Пятая неудача второго фактора подряд: вход закрыт до locked_until; notified_at задан — письмо
-- о блокировке уходит сейчас (не чаще раза в час на учётку). Версия агрегата растёт (событие).
UPDATE staff_accounts
SET locked_until = sqlc.arg(locked_until)::timestamptz,
    lock_notified_at = coalesce(sqlc.narg(notified_at), lock_notified_at),
    version = version + 1
WHERE id = sqlc.arg(id)
RETURNING version;

-- name: ListLockRecipients :many
-- Кому письмо staff.locked: активные владельцы и админы.
SELECT a.id
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.status = 'active' AND r.role IN ('owner', 'admin')
ORDER BY a.id;
