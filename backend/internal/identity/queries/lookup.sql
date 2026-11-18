-- Поиск игрока по почте для поддержки и статистика способов входа (спека identity §3.15 п. 2, §7.1, §4.3; план I-4,
-- Task 5). Роль admin: только чтение, без FOR … (у admin нет UPDATE на users и user_mfa, К2); у user_mfa — только
-- колонки user_id и confirmed_at, секретов admin не видит.

-- name: LookupPlayerByEmail :one
-- аккаунт по канонической почте (citext, users_email_key): удалённые не находятся; 2FA — только подтверждённая
-- строка user_mfa; живой сброс 2FA — не исполнен и не отменён (не больше одного — mfa_resets_live_key)
SELECT u.id, u.nickname, u.city_id, u.status::text AS status, u.created_at,
       EXISTS (SELECT 1 FROM user_mfa m WHERE m.user_id = u.id AND m.confirmed_at IS NOT NULL) AS mfa_enabled,
       r.id AS reset_id, r.execute_at AS reset_execute_at
FROM users u
LEFT JOIN mfa_resets r ON r.user_id = u.id AND r.executed_at IS NULL AND r.cancelled_at IS NULL
WHERE u.email = @email AND u.deleted_at IS NULL AND u.status <> 'deleted';

-- name: AuthMethodUsage :many
-- статистика способов входа — агрегирующее представление 0027 (R2): admin не читает user_identities, passkeys и
-- credentials; порядок — apple, google, passkeys
SELECT method::text AS method, only_method::bigint AS only_method, linked::bigint AS linked
FROM identity_read_auth_method_usage
ORDER BY array_position(ARRAY['apple', 'google', 'passkeys']::text[], method);
