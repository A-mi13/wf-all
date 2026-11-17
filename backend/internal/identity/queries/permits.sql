-- Допуски капитана (план I-4, Task 7; спека §10.1–10.2; R24, R25): отзыв (RevokeRoleInTx, анонимизация) и чтение
-- для других модулей (Service.CaptainPermits). Порядок замков R7: captain_applications → role_assignments.

-- name: PermitsLive :many
-- живые допуски аккаунта (все города или один — city_id); без замка: замок — строка заявки, затем отзыв с
-- перепроверкой revoked_at
SELECT scope_id AS city_id, application_id FROM role_assignments
WHERE user_id = @user_id AND role = 'captain' AND scope_type = 'city' AND revoked_at IS NULL
  AND (sqlc.narg(city_id)::uuid IS NULL OR scope_id = sqlc.narg(city_id)::uuid)
ORDER BY scope_id;

-- name: PermitLockApplication :one
-- заявка допуска под замком (агрегат события, R25) — раньше строки допуска (R7)
SELECT version FROM captain_applications WHERE id = @id FOR UPDATE;

-- name: PermitRevoke :execrows
-- отзыв живого допуска заявки; 0 строк — отозван параллельно (READ COMMITTED перепроверит revoked_at)
UPDATE role_assignments
SET revoked_at = @now, revoked_by_staff_id = sqlc.narg(staff_id), revoke_reason = @reason
WHERE user_id = @user_id AND role = 'captain' AND scope_type = 'city' AND scope_id = @city_id::uuid
  AND application_id = @application_id::uuid AND revoked_at IS NULL;

-- name: PermitBumpApplication :one
-- версия агрегата заявки + 1 (строка уже под замком PermitLockApplication)
UPDATE captain_applications SET version = version + 1 WHERE id = @id RETURNING version;

-- name: ReadCaptainPermits :many
-- живые допуски игрока по городам (экспортированное представление): Service.CaptainPermits
SELECT city_id, granted_at, application_id, is_probationary FROM identity_read_captain_permits
WHERE user_id = @user_id
ORDER BY granted_at, city_id;

-- name: PermitRevokeOrphan :execrows
-- анонимизация: допуск без заявки (R25 — такой строки быть не может) отзывается без события, удаление не стоит
UPDATE role_assignments
SET revoked_at = @now, revoked_by_staff_id = sqlc.narg(staff_id), revoke_reason = @reason
WHERE user_id = @user_id AND role = 'captain' AND scope_type = 'city' AND scope_id = @city_id::uuid
  AND application_id IS NULL AND revoked_at IS NULL;
