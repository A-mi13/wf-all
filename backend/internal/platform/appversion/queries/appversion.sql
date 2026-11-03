-- Версии приложения платформы; 0 строк — версии не заведены (публичная ручка отдаёт null-поля).
-- name: GetAppVersions :one
SELECT platform, min_version, recommended_version, store_url, version, updated_at
FROM app_versions
WHERE platform = @platform;

-- Заведённые платформы для админки (план B, Task 15), по имени; незаведённую ручка отдаёт null-полями.
-- name: ListAppVersions :many
SELECT platform, min_version, recommended_version, store_url, version, updated_at
FROM app_versions
ORDER BY platform;

-- Строка платформы под замком записи: «было» для аудита и сравнение «ничего не меняется».
-- name: AppVersionsForUpdate :one
SELECT platform, min_version, recommended_version, store_url, version, updated_at
FROM app_versions
WHERE platform = @platform
FOR UPDATE;

-- Запись версий: новая строка — version 1 и updated_at из часов записывающего; правка — version + 1, updated_at
-- ставит триггер set_updated_at (время базы; порядок правок — version).
-- name: UpsertAppVersions :one
INSERT INTO app_versions (platform, min_version, recommended_version, store_url, updated_at)
VALUES (@platform, @min_version, @recommended_version, @store_url, @updated_at)
ON CONFLICT (platform) DO UPDATE
SET min_version = EXCLUDED.min_version, recommended_version = EXCLUDED.recommended_version,
    store_url = EXCLUDED.store_url, version = app_versions.version + 1
RETURNING platform, min_version, recommended_version, store_url, version, updated_at;
