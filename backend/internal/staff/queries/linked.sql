-- Связь «сотрудник → учётка игрока» для чужой транзакции (порт identity.StaffLinks, план I-4 R38): строка учётки
-- сотрудника FOR SHARE (сериализует с PutPlayerLink/DeletePlayerLink — у них FOR UPDATE той же строки), связь — из
-- staff_player_links; связи нет — user_id NULL; сотрудника нет — ни одной строки.

-- name: LinkedPlayerForShare :one
SELECT l.user_id
FROM staff_accounts a
LEFT JOIN staff_player_links l ON l.staff_id = a.id
WHERE a.id = sqlc.arg(staff_id)::uuid
FOR SHARE OF a;
