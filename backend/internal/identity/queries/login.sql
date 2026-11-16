-- Вход по почте и паролю (спека identity §3.2; план I-2, Task 11). Аккаунт ищется пулом без замка — argon2 вне
-- транзакции (R31); замок users и перепроверка хэша — в транзакции входа.

-- name: LoginFindUser :one
-- почта — каноническая форма emailaddr.Normalize (users.email citext); purge_at — срок отмены удаления (вышел — как
-- deleted, m4 ревью 2)
SELECT id, status, purge_at FROM users WHERE email = @email;
