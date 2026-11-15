-- Вход по почте и паролю (спека identity §3.2; план I-2, Task 11). Аккаунт ищется пулом без замка — argon2 вне
-- транзакции (R31); замок users и перепроверка хэша — в транзакции входа.

-- name: LoginFindUser :one
-- почта — каноническая форма emailaddr.Normalize (users.email citext)
SELECT id, status FROM users WHERE email = @email;

-- name: LoginConsents :many
-- принятые версии документов аккаунта: documents_acceptance_required результата входа (§3.16)
SELECT document::text AS document, version FROM consents WHERE user_id = @user_id;
