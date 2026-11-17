-- Капитанство игрока (спека identity §3.12 п. 1–5, 7; план I-4, Task 3): экран «Стать капитаном», история своих
-- заявок, подача и отзыв. Правило условий — captain_rules.sql (R5). Каждый запрос привязан к игроку запроса
-- (user_id): чужая заявка неотличима от несуществующей.

-- name: CaptaincyUserCity :one
-- Текущий город игрока (город заявки, §3.12) — без замка (экран; подача берёт lockUser).
SELECT city_id FROM users WHERE id = $1;

-- name: CaptaincyPermits :many
-- Живые допуски капитана игрока по городам (экспортированное представление, испытательный срок — по заявке допуска).
SELECT city_id, granted_at, is_probationary FROM identity_read_captain_permits
 WHERE user_id = $1
 ORDER BY granted_at, city_id;

-- name: MyCaptainApplication :one
-- Заявка игрока по id (открытая заявка экрана); чужая — ErrNoRows.
SELECT * FROM captain_applications WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ListMyCaptainApplications :many
-- История заявок игрока, новые сверху: keyset (created_at, id) DESC (индекс captain_applications_user_idx, 0027).
SELECT * FROM captain_applications
 WHERE user_id = sqlc.arg(user_id)
   AND (NOT sqlc.arg(has_cursor)::bool
        OR (created_at, id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
 ORDER BY created_at DESC, id DESC
 LIMIT sqlc.arg(lim);

-- name: InsertCaptainApplication :one
-- Новая заявка: pending, испытательный срок, версия 1; время — часы приложения. Вторая открытая заявка в городе —
-- 23505 captain_applications_open_key (запасной путь: подачу сериализует lockUser).
INSERT INTO captain_applications (id, user_id, city_id, proposed_format, proposed_pitch, play_frequency, contact_link,
                                  note, phone, status, is_probationary, version, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(city_id), sqlc.narg(proposed_format), sqlc.narg(proposed_pitch),
        sqlc.narg(play_frequency), sqlc.narg(contact_link), sqlc.narg(note), sqlc.narg(phone), 'pending', true, 1,
        sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: LockMyCaptainApplication :one
-- Своя заявка под замком (R7: после users); чужая или нет — ErrNoRows.
SELECT * FROM captain_applications
 WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
   FOR UPDATE;

-- name: WithdrawMyCaptainApplication :one
-- Отзыв игроком: withdrawn, closed_at, версия агрегата + 1 (строка уже под замком и pending).
UPDATE captain_applications
   SET status = 'withdrawn', closed_at = sqlc.arg(now), version = version + 1
 WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;
