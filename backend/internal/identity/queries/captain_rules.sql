-- Правило условий капитанства (план I-4, Task 2, R5) и предикат «заблокирован» (R36). Чтения без замков: замки держит
-- вызывающий. Всё — на соединении транзакции вызывающего (настройки города — экспортированное представление geo, не
-- второе соединение пула; C1 волны 4 плана I-2).

-- name: UserBlocked :one
-- «Заблокирован» — одно правило для всех мест (R36): banned (или период удаления поверх бана — status_before_deletion =
-- banned, R11) с живой строкой login (снята — lifted_at; срочная — живёт строго до until). Бан истёк, чистка ещё не
-- прошла — не блок (R12; m1 итогового ревью Г): статус вернёт expireBanTx (чистка, вход, отмена удаления).
SELECT ((u.status = 'banned' OR (u.status = 'pending_deletion' AND u.status_before_deletion = 'banned'))
        AND EXISTS (
            SELECT 1 FROM user_restrictions r
             WHERE r.user_id = u.id AND r.capability = 'login' AND r.lifted_at IS NULL
               AND (r.until IS NULL OR sqlc.arg(now)::timestamptz < r.until)))::boolean AS blocked
  FROM users u
 WHERE u.id = sqlc.arg(id);

-- name: CaptaincyUser :one
-- Аккаунт для правила условий — без замка.
SELECT * FROM users WHERE id = $1;

-- name: CaptaincyCity :one
-- Пояс, включена ли страна и порог возраста города заявки.
SELECT timezone, country_enabled, min_signup_age FROM geo_read_city_settings WHERE city_id = $1;

-- name: CaptainRestricted :one
-- Живое ограничение captain (снятое — lifted_at; срочное живёт строго до until).
SELECT EXISTS (
    SELECT 1 FROM user_restrictions
     WHERE user_id = sqlc.arg(user_id) AND capability = 'captain' AND lifted_at IS NULL
       AND (until IS NULL OR sqlc.arg(now)::timestamptz < until))::boolean AS restricted;

-- name: CaptainPermitted :one
-- Живой допуск капитана в городе.
SELECT EXISTS (
    SELECT 1 FROM role_assignments
     WHERE user_id = sqlc.arg(user_id) AND role = 'captain' AND scope_type = 'city'
       AND scope_id = sqlc.arg(city_id)::uuid AND revoked_at IS NULL)::boolean AS permitted;

-- name: LastCaptainRejection :one
-- Время последнего отказа игроку в любом городе (пауза — по игроку, R5, Q4); нет — ErrNoRows.
SELECT reviewed_at FROM captain_applications
 WHERE user_id = $1 AND status = 'rejected'
 ORDER BY reviewed_at DESC
 LIMIT 1;

-- name: OpenCaptainApplication :one
-- Открытая заявка игрока в городе (одна — captain_applications_open_key); нет — ErrNoRows.
SELECT id FROM captain_applications WHERE user_id = $1 AND city_id = $2 AND status = 'pending';
