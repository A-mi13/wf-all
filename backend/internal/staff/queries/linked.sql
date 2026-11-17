-- Связь «сотрудник → учётка игрока» для чужой транзакции (порт identity.StaffLinks, план I-4 R38): строка учётки
-- сотрудника FOR SHARE (сериализует с PutPlayerLink/DeletePlayerLink — у них FOR UPDATE той же строки), затем связь из
-- staff_player_links — ОТДЕЛЬНЫМ оператором. Один оператор «FOR SHARE OF a + LEFT JOIN связи» после ожидания замка
-- перечитывает (READ COMMITTED) только заблокированную строку учётки, а связь берёт из снимка до ожидания: связь,
-- заведённая во время решения, не видна (TestStaffLinkedPlayerTxSeesLinkCommittedWhileWaiting). Второй оператор берёт
-- новый снимок уже под замком.

-- name: LockAccountForShare :one
-- Учётка сотрудника под FOR SHARE до конца tx вызывающего; сотрудника нет — ErrNoRows.
SELECT id FROM staff_accounts WHERE id = sqlc.arg(staff_id)::uuid FOR SHARE;

-- name: LinkedPlayerOf :one
-- Связь сотрудника (после LockAccountForShare); связи нет — ErrNoRows.
SELECT user_id FROM staff_player_links WHERE staff_id = sqlc.arg(staff_id)::uuid;
