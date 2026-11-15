-- Сброс 2FA поддержкой (спека identity §3.15; план I-2, Task 17–18). Имена — с префиксом MfaReset.
-- admin-api (роль admin): users и user_mfa читаются БЕЗ FOR … — у admin нет UPDATE на них (К2), замок дал бы 42501;
-- гонку «два сброса» закрывает частичный UNIQUE mfa_resets_live_key, «сброс и отмена» — замок строки mfa_resets.

-- name: MfaResetUserState :one
-- состояние игрока для запроса и исполнителя: статус, подтверждённая 2FA (у admin — колонки user_id и confirmed_at),
-- живой сброс
SELECT u.status::text AS status,
       EXISTS (SELECT 1 FROM user_mfa m WHERE m.user_id = u.id AND m.confirmed_at IS NOT NULL) AS mfa_enabled,
       EXISTS (SELECT 1 FROM mfa_resets r
               WHERE r.user_id = u.id AND r.executed_at IS NULL AND r.cancelled_at IS NULL) AS reset_pending
FROM users u
WHERE u.id = @id;

-- name: MfaResetInsert :exec
INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason, ticket, execute_at,
                        created_at)
VALUES (@id, @user_id, @approval_id, @requested_by_staff_id, @requested_by_label, @reason, sqlc.narg(ticket),
        @execute_at, @created_at);

-- name: MfaResetLockLive :one
-- живой сброс под замком (отмена сотрудником): закрытого нет
SELECT id, user_id, execute_at
FROM mfa_resets
WHERE id = @id AND executed_at IS NULL AND cancelled_at IS NULL
FOR UPDATE;

-- name: MfaResetCancel :exec
UPDATE mfa_resets SET cancelled_at = @now, cancelled_by = @cancelled_by
WHERE id = @id AND executed_at IS NULL AND cancelled_at IS NULL;

-- Отмена владельцем и исполнение по сроку (Task 18). Порядок замков R30: users (lockUser или SKIP LOCKED воркера) →
-- user_mfa → mfa_resets.

-- name: MfaResetCancelByOwner :many
-- живой сброс отменяет владелец (вход вторым фактором или явная отмена); users уже под замком вызывающего
UPDATE mfa_resets SET cancelled_at = @now, cancelled_by = 'owner'
WHERE user_id = @user_id AND executed_at IS NULL AND cancelled_at IS NULL
RETURNING id, execute_at;

-- name: MfaResetCancelledBySession :one
-- сброс уже отменён владельцем не раньше входа этой сессии вторым фактором (повтор cancelMyMfaReset — 204)
SELECT EXISTS (
    SELECT 1 FROM mfa_resets r JOIN sessions s ON s.user_id = r.user_id
    WHERE r.user_id = @user_id AND s.id = @session_id AND r.cancelled_by = 'owner' AND s.mfa_at IS NOT NULL
      AND r.cancelled_at >= s.mfa_at
) AS cancelled;

-- name: MfaResetDue :many
-- кандидаты исполнения по сроку, без замка: замки и перепроверка — на строку (ExecuteDueMfaResets)
SELECT id, user_id FROM mfa_resets
WHERE executed_at IS NULL AND cancelled_at IS NULL AND execute_at <= @now
ORDER BY execute_at, id
LIMIT @batch;

-- name: MfaResetLockUserSkip :one
-- воркер: аккаунт под замком или ничего (занят сценарием игрока — строку возьмёт следующий запуск)
SELECT id, status::text AS status FROM users WHERE id = @id FOR NO KEY UPDATE SKIP LOCKED;

-- name: MfaResetUserMfa :one
-- есть ли 2FA: у worker нет UPDATE на user_mfa (§5.5) — FOR UPDATE дал бы 42501; строку меняют только сценарии api
-- под замком users (R30), его воркер уже держит — чтение без замка не устаревает до конца транзакции. Колонка — только
-- user_id (колоночное право worker), confirmed_at воркеру не видна
SELECT user_id FROM user_mfa WHERE user_id = @user_id;

-- name: MfaResetMarkExecuted :exec
UPDATE mfa_resets SET executed_at = @now WHERE id = @id AND executed_at IS NULL AND cancelled_at IS NULL;

-- name: MfaResetSessionLive :one
-- сессия запроса жива и принадлежит аккаунту (загрузчик кэширует до 5 с; users уже под замком)
SELECT EXISTS (SELECT 1 FROM sessions WHERE id = @id AND user_id = @user_id AND revoked_at IS NULL) AS live;
