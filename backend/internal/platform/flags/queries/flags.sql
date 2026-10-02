-- name: GetFlag :one
SELECT key, enabled_globally FROM feature_flags WHERE key = $1;

-- Включён ли флаг в городе: глобально или город в списке. 0 строк — флага нет.
-- name: IsEnabled :one
SELECT (enabled_globally OR @city_id::uuid = ANY(enabled_city_ids))::boolean AS enabled
FROM feature_flags WHERE key = @key;
