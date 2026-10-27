-- Чистка своих таблиц модуля (спека сотрудников §4.10, P27): тикер admin-api раз в час, порциями. Порция —
-- один оператор (своя короткая транзакция), строки — FOR UPDATE SKIP LOCKED, как approvals.ExpireDueBatch:
-- два экземпляра admin-api чистят параллельно, не ожидая друг друга и не удаляя строку дважды; строку под
-- замком сценария порция пропускает (её возьмёт следующий проход).

-- name: CleanupSessionsBatch :execrows
-- Порция сессий, умерших раньше границы: истёкших (expires_at) или погашенных (revoked_at) — что раньше.
-- Давно истёкшую сессию гасят как expired только при загрузке, поэтому момент смерти — LEAST, а не revoked_at.
DELETE FROM staff_sessions
WHERE id IN (SELECT s.id FROM staff_sessions s
             WHERE LEAST(s.expires_at, COALESCE(s.revoked_at, s.expires_at)) < sqlc.arg(before)::timestamptz
             ORDER BY s.expires_at, s.id
             LIMIT sqlc.arg(lim)
             FOR UPDATE SKIP LOCKED);

-- name: CleanupInvitesBatch :execrows
-- Порция приглашений: погашенных (consumed_at) и отозванных (revoked_at) — по моменту закрытия, живых — по сроку
-- (expires_at), граница — before. Последнее приглашение сотрудника (как в GetLastInvite: created_at, id) не
-- удаляется никогда, даже давно просроченное: по нему повторная отправка админом и оператором берёт назначение
-- (invite или access_reset), а карточка показывает истёкшее приглашение.
DELETE FROM staff_invites
WHERE id IN (SELECT i.id FROM staff_invites i
             WHERE COALESCE(i.consumed_at, i.revoked_at, i.expires_at) < sqlc.arg(before)::timestamptz
               AND EXISTS (SELECT 1 FROM staff_invites n
                           WHERE n.staff_id = i.staff_id
                             AND (n.created_at, n.id) > (i.created_at, i.id))
             ORDER BY i.created_at, i.id
             LIMIT sqlc.arg(lim)
             FOR UPDATE SKIP LOCKED);
