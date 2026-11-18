-- Связь «сотрудник → учётка игрока» (спека identity §7.6; план I-4, Task 6). Учётка сотрудника уже под замком
-- сценария (lockAccounts: FOR UPDATE) — строка связи этого сотрудника меняется только под ним.

-- name: PlayerLinkOf :one
-- текущая связь сотрудника; нет — ни одной строки
SELECT user_id, created_by, created_at
FROM staff_player_links
WHERE staff_id = sqlc.arg(staff_id)::uuid;

-- name: PlayerLinkHolder :one
-- сотрудник, связанный с учёткой игрока; нет — ни одной строки
SELECT staff_id
FROM staff_player_links
WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: PlayerLinkPut :exec
-- вставка или замена связи сотрудника; учётка игрока у другого сотрудника — 23505 staff_player_links_user_id_key
INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at)
VALUES (sqlc.arg(staff_id)::uuid, sqlc.arg(user_id)::uuid, sqlc.arg(created_by)::uuid, sqlc.arg(created_at))
ON CONFLICT (staff_id) DO UPDATE
SET user_id = EXCLUDED.user_id, created_by = EXCLUDED.created_by, created_at = EXCLUDED.created_at;

-- name: PlayerLinkDelete :many
-- снять связь; вернуть снятую учётку игрока (нет связи — пусто)
DELETE FROM staff_player_links
WHERE staff_id = sqlc.arg(staff_id)::uuid
RETURNING user_id;

-- name: PlayerLinkCard :one
-- связь для карточки: учётка игрока, когда и кем (текущие имя и почта автора; учётки сотрудников не удаляются,
-- LEFT JOIN — защита в глубину: created_by без внешнего ключа)
SELECT l.user_id, l.created_at, coalesce(a.display_name, '')::text AS by_name, coalesce(a.email::text, '')::text AS by_email
FROM staff_player_links l
LEFT JOIN staff_accounts a ON a.id = l.created_by
WHERE l.staff_id = sqlc.arg(staff_id)::uuid;
