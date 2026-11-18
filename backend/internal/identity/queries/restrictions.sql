-- Ограничения и статус аккаунта (план I-4, Task 7; спека §10.2, §17; R11, R12, R42): SetRestrictionsInTx (identity/intx)
-- и возврат статуса после истечения бана в чистке. users уже под замком вызывающего (lockUser или CleanupLockUser).

-- name: RestrictionsLiftSource :execrows
-- снять живые строки источника (санкции moderation) — lifted_at
UPDATE user_restrictions SET lifted_at = @now
WHERE user_id = @user_id AND source_id = @source_id AND lifted_at IS NULL;

-- name: RestrictionsAdd :exec
INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
VALUES (@id, @user_id, @capability, sqlc.narg(until), @source_id, @now);

-- name: RestrictionsLiveCapabilities :many
-- закрытые возможности после изменения: живая строка — не снята и until NULL или строго позже now (как UserBlocked и
-- загрузчик сессии)
SELECT DISTINCT capability FROM user_restrictions
WHERE user_id = @user_id AND lifted_at IS NULL AND (until IS NULL OR @now::timestamptz < until)
ORDER BY capability;

-- name: SetUserStatus :one
-- статус аккаунта; в периоде удаления меняется status_before_deletion, а status и purge_at — нет (R11: удаление
-- сильнее, отмена удаления вернёт актуальный статус — §17 п. 3). Версия агрегата + 1. deleted сюда не попадает.
UPDATE users
SET status = CASE WHEN status = 'pending_deletion' THEN status ELSE @status::user_status END,
    status_before_deletion = CASE WHEN status = 'pending_deletion' THEN @status::user_status
                                  ELSE status_before_deletion END,
    version = version + 1
WHERE id = @id
RETURNING version, status;
