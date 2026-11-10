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

-- Заведение платформы: новая строка — version 1 и updated_at из часов записывающего. Строку параллельно завела другая
-- транзакция — ни одной строки (ждёт её коммита, ON CONFLICT DO NOTHING): вызывающий перечитывает её под FOR UPDATE как
-- «было» (ревью G7 m2) — иначе вторая правка шла бы как создание с before = null.
-- name: InsertAppVersions :one
INSERT INTO app_versions (platform, min_version, recommended_version, store_url, updated_at)
VALUES (@platform, @min_version, @recommended_version, @store_url, @updated_at)
ON CONFLICT (platform) DO NOTHING
RETURNING platform, min_version, recommended_version, store_url, version, updated_at;

-- Правка заведённой платформы (строка под FOR UPDATE): version + 1, updated_at ставит триггер set_updated_at (время
-- базы; порядок правок — version).
-- name: UpdateAppVersions :one
UPDATE app_versions
SET min_version = @min_version, recommended_version = @recommended_version, store_url = @store_url,
    version = version + 1
WHERE platform = @platform
RETURNING platform, min_version, recommended_version, store_url, version, updated_at;
