-- Подтверждение вдвоём (спека сотрудников §4.8, D2, D17): таблица платформы approvals, роль admin.
-- Статус пишется текстом с приведением к approval_status: запросы не зависят от Go-типа перечисления.

-- name: InsertApproval :one
INSERT INTO approvals (id, action, permission, payload, target_id, scope_city_id, requested_by,
                       requested_by_label, requested_at, expires_at, status)
VALUES (@id, @action, @permission, @payload, sqlc.narg(target_id), sqlc.narg(scope_city_id), @requested_by,
        @requested_by_label, @requested_at, @expires_at, 'pending')
RETURNING *;

-- Строка под замком решения (Approve, Reject, Cancel): параллельное решение ждёт коммита этого.
-- name: LockApproval :one
SELECT * FROM approvals WHERE id = @id FOR UPDATE;

-- Решение ожидающего запроса; строка уже под замком LockApproval, условие pending — страховка.
-- name: DecideApproval :one
UPDATE approvals
SET status           = (sqlc.arg(status)::text)::approval_status,
    decided_by       = sqlc.narg(decided_by),
    decided_by_label = sqlc.arg(decided_by_label)::text,
    decided_at       = sqlc.arg(decided_at)::timestamptz,
    decision_comment = sqlc.narg(decision_comment),
    error            = sqlc.narg(error)
WHERE id = @id AND status = 'pending'
RETURNING *;

-- Гашение ожидающих запросов инициатора системой (D17). SKIP LOCKED: строку под замком
-- параллельного решения доводит до конца это решение (его InitiatorCheck увидит смену инициатора);
-- ожидание замка здесь дало бы взаимную блокировку с путём «замок учётки → гашение» модуля staff.
-- name: CancelPendingByInitiator :many
UPDATE approvals
SET status = 'cancelled', decided_by_label = 'system', decided_at = sqlc.arg(now)::timestamptz, error = 'initiator_changed'
WHERE id IN (SELECT a.id FROM approvals a
             WHERE a.requested_by = sqlc.arg(requested_by) AND a.status = 'pending'
             FOR UPDATE SKIP LOCKED)
  AND status = 'pending'
RETURNING *;

-- Порция просроченных ожидающих (expires_at <= now) → expired от системы. SKIP LOCKED: строку под
-- замком решения переводит само решение (Approve/Reject/Cancel проверяют срок под замком).
-- name: ExpireDueBatch :many
UPDATE approvals
SET status = 'expired', decided_by_label = 'system', decided_at = sqlc.arg(now)::timestamptz
WHERE id IN (SELECT a.id FROM approvals a
             WHERE a.status = 'pending' AND a.expires_at <= sqlc.arg(now)::timestamptz
             ORDER BY a.expires_at, a.id
             LIMIT sqlc.arg(lim)
             FOR UPDATE SKIP LOCKED)
  AND status = 'pending'
RETURNING *;

-- Порция решённых старше границы (P27: хранятся DecidedRetention); ожидающие не трогаются.
-- name: PurgeDecidedBatch :execrows
DELETE FROM approvals
WHERE id IN (SELECT a.id FROM approvals a
             WHERE a.status <> 'pending' AND a.decided_at < sqlc.arg(before)::timestamptz
             ORDER BY a.decided_at, a.id
             LIMIT sqlc.arg(lim));

-- Страница запросов, новые сверху, keyset (requested_at, id). mine — созданные смотрящим; иначе
-- входящие: не его и не о нём, право запроса у смотрящего allow|dual (allowed) или scoped (scoped) и
-- город запроса задан и в его скоупе (scoped_any_city — глобальная роль, иначе city_ids). Фильтр
-- прав — в SQL: страница не «худеет», лимит и курсор честные.
-- name: ListApprovals :many
SELECT * FROM approvals
WHERE (sqlc.narg(status)::text IS NULL OR status = (sqlc.narg(status)::text)::approval_status)
  AND CASE WHEN sqlc.arg(mine)::bool THEN requested_by = sqlc.arg(viewer)::uuid
      ELSE requested_by <> sqlc.arg(viewer)::uuid
       AND target_id IS DISTINCT FROM sqlc.arg(viewer)::uuid
       AND (permission = ANY (sqlc.arg(allowed)::text[])
            OR (permission = ANY (sqlc.arg(scoped)::text[]) AND scope_city_id IS NOT NULL
                AND (sqlc.arg(scoped_any_city)::bool OR scope_city_id = ANY (sqlc.arg(city_ids)::uuid[]))))
      END
  AND (NOT sqlc.arg(has_cursor)::bool
       OR (requested_at, id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY requested_at DESC, id DESC
LIMIT sqlc.arg(lim);
