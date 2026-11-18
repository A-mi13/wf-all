-- Смена города игрока (план I-4, Task 7; спека §10.2; R8, R39): ChangeCityInTx (identity/intx, вызывающий — teams).
-- users уже под замком (lockUser); заявки — после users (R7).

-- name: CityChangeUser :one
-- текущий город игрока, версия агрегата + 1
UPDATE users SET city_id = @city_id::uuid, version = version + 1 WHERE id = @id RETURNING version;

-- name: CityWithdrawApplications :many
-- открытые заявки не в новом городе — withdrawn (паузы такой отзыв не ставит: пауза — только от rejected, R5, R39)
UPDATE captain_applications SET status = 'withdrawn', closed_at = @now, version = version + 1
WHERE user_id = @user_id AND status = 'pending' AND city_id <> @city_id::uuid
RETURNING id, city_id, version;
