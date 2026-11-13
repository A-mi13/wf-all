-- name: GetFlag :one
SELECT key, enabled_globally FROM feature_flags WHERE key = $1;

-- Включён ли флаг в городе: глобально или город в списке. 0 строк — флага нет.
-- name: IsEnabled :one
SELECT (enabled_globally OR @city_id::uuid = ANY(enabled_city_ids))::boolean AS enabled
FROM feature_flags WHERE key = @key;

-- Флаги для админки (план B, Task 15): все, по ключу.
-- name: ListFlags :many
SELECT key, description, enabled_globally, enabled_city_ids, version, updated_at
FROM feature_flags ORDER BY key;

-- Строка флага до правки (без замка): «было» и сверка версии; гонку правок решает UpdateFlag (WHERE version).
-- name: FlagByKey :one
SELECT key, description, enabled_globally, enabled_city_ids, version, updated_at
FROM feature_flags WHERE key = @key;

-- Правка флага с оптимистичной версией: параллельная правка той же версии ждёт замок строки, после коммита первой
-- условие version уже ложно — 0 строк (409 platform.flag_stale). updated_at ставит триггер set_updated_at (время
-- базы); источник порядка — version.
-- name: UpdateFlag :one
UPDATE feature_flags
SET enabled_globally = @enabled_globally, enabled_city_ids = @enabled_city_ids::uuid[], version = version + 1
WHERE key = @key AND version = @version
RETURNING key, description, enabled_globally, enabled_city_ids, version, updated_at;
