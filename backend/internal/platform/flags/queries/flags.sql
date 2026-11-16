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

-- Строки правки под замком (спека identity §4.2): флаг, его зависимость и зависимые — одним запросом в порядке
-- ключа (LockRows над сортировкой берёт замки по возрастанию key): два PATCH связанных флагов ждут друг друга, а не
-- взаимоблокируются. Строки — актуальные после коммита соседа: «было», сверка версии и проверка зависимости.
-- name: LockFlags :many
SELECT key, description, enabled_globally, enabled_city_ids, version, updated_at
FROM feature_flags WHERE key = ANY(@keys::text[]) ORDER BY key FOR UPDATE;

-- Правка флага с оптимистичной версией (строка уже под замком LockFlags): version не та — 0 строк (409
-- platform.flag_stale). updated_at ставит триггер set_updated_at (время базы); источник порядка — version.
-- name: UpdateFlag :one
UPDATE feature_flags
SET enabled_globally = @enabled_globally, enabled_city_ids = @enabled_city_ids::uuid[], version = version + 1
WHERE key = @key AND version = @version
RETURNING key, description, enabled_globally, enabled_city_ids, version, updated_at;
