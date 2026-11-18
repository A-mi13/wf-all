-- Заявки в капитаны в админке (спека identity §3.12 п. 6, §7.1; план I-4, Task 4). Роль admin: users — только
-- чтение без FOR … (у admin нет UPDATE на users, К2 — замок дал бы 42501); под замок — только строка
-- captain_applications. Заявки удалённых аккаунтов (status = deleted) не видны нигде (R9). Скоуп модератора — фильтр
-- city_id ∈ city_ids (scoped), карточку и решение чужого города сценарий отвечает как несуществующую.

-- name: AdminCaptainQueuePending :many
-- Очередь pending, старые сверху: keyset (created_at, id) ASC.
SELECT sqlc.embed(a), u.nickname, u.birth_date, u.created_at AS user_created_at, c.timezone AS city_timezone
  FROM captain_applications a
  JOIN users u ON u.id = a.user_id
  LEFT JOIN geo_read_city_settings c ON c.city_id = a.city_id
 WHERE a.status = 'pending' AND u.status <> 'deleted'
   AND (NOT sqlc.arg(scoped)::bool OR a.city_id = ANY (sqlc.arg(city_ids)::uuid[]))
   AND (sqlc.narg(city_id)::uuid IS NULL OR a.city_id = sqlc.narg(city_id)::uuid)
   AND (NOT sqlc.arg(has_cursor)::bool
        OR (a.created_at, a.id) > (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
 ORDER BY a.created_at, a.id
 LIMIT sqlc.arg(lim);

-- name: AdminCaptainQueueDecided :many
-- Решённые и отозванные заявки статуса status, новые решения сверху: keyset (decided_at, id) DESC, decided_at =
-- coalesce(reviewed_at, closed_at); created_at — защита от строки без времени закрытия (ключ не бывает NULL).
SELECT sqlc.embed(a), u.nickname, u.birth_date, u.created_at AS user_created_at, c.timezone AS city_timezone,
       coalesce(a.reviewed_at, a.closed_at, a.created_at)::timestamptz AS decided_key
  FROM captain_applications a
  JOIN users u ON u.id = a.user_id
  LEFT JOIN geo_read_city_settings c ON c.city_id = a.city_id
 WHERE a.status = sqlc.arg(status)::application_status AND u.status <> 'deleted'
   AND (NOT sqlc.arg(scoped)::bool OR a.city_id = ANY (sqlc.arg(city_ids)::uuid[]))
   AND (sqlc.narg(city_id)::uuid IS NULL OR a.city_id = sqlc.narg(city_id)::uuid)
   AND (NOT sqlc.arg(has_cursor)::bool
        OR (coalesce(a.reviewed_at, a.closed_at, a.created_at), a.id)
           < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
 ORDER BY coalesce(a.reviewed_at, a.closed_at, a.created_at) DESC, a.id DESC
 LIMIT sqlc.arg(lim);

-- name: AdminCaptainApplication :one
-- Заявка без замка (карточка, контакты); нет — ErrNoRows.
SELECT * FROM captain_applications WHERE id = $1;

-- name: AdminLockCaptainApplication :one
-- Заявка под замком решения (R6, R7: первый замок админского сценария); нет — ErrNoRows.
SELECT * FROM captain_applications WHERE id = $1 FOR UPDATE;

-- name: AdminCaptainApplicant :one
-- Заявитель без замка: ник, дата рождения и стаж, статус, текущий город, пояс города заявки (возраст — по нему).
SELECT u.nickname, u.birth_date, u.created_at, u.status, u.city_id,
       coalesce((SELECT c.timezone FROM geo_read_city_settings c WHERE c.city_id = sqlc.arg(city_id)::uuid), '')::text
           AS city_timezone
  FROM users u
 WHERE u.id = sqlc.arg(user_id);

-- name: AdminCaptainApplicantEmail :one
-- Почта заявителя (только для ответа контактов: в журнал не пишется).
SELECT email FROM users WHERE id = $1;

-- name: AdminCaptainHistory :many
-- Прошлые заявки игрока, новые сверху, без текущей и без предела (m11: пауза 7 дней ограничивает их число).
SELECT * FROM captain_applications
 WHERE user_id = sqlc.arg(user_id) AND id <> sqlc.arg(exclude_id)
 ORDER BY created_at DESC, id DESC;

-- name: AdminCaptainLinkedAccounts :one
-- Сколько других аккаунтов делили с заявителем устройство (экспортированное представление 0025).
SELECT count(DISTINCT linked_user_id)::bigint AS linked FROM identity_read_linked_accounts WHERE user_id = $1;

-- name: AdminGrantCaptainPermit :exec
-- Допуск капитана в городе по заявке (R25: всегда с application_id). Живой допуск в городе уже есть — 23505
-- role_assignments_unique (правило условий отсекает раньше: already_permitted).
INSERT INTO role_assignments (id, user_id, role, scope_type, scope_id, granted_by_staff_id, application_id, granted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), 'captain', 'city', sqlc.arg(city_id)::uuid, sqlc.arg(staff_id)::uuid,
        sqlc.arg(application_id)::uuid, sqlc.arg(now));

-- name: AdminApproveCaptainApplication :one
-- Одобрение: строка под замком и pending (проверил сценарий); версия агрегата + 1.
UPDATE captain_applications
   SET status = 'approved', reviewed_at = sqlc.arg(now), reviewed_by_staff_id = sqlc.arg(staff_id)::uuid,
       reviewed_by_label = sqlc.arg(label)::text, comment = sqlc.narg(comment), version = version + 1
 WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- name: AdminRejectCaptainApplication :one
-- Отказ: строка под замком и pending; код причины, комментарий (other — с комментарием, CHECK 0025).
UPDATE captain_applications
   SET status = 'rejected', reviewed_at = sqlc.arg(now), reviewed_by_staff_id = sqlc.arg(staff_id)::uuid,
       reviewed_by_label = sqlc.arg(label)::text, rejection_code = sqlc.arg(code)::text, comment = sqlc.narg(comment),
       version = version + 1
 WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;
