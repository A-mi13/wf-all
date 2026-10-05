-- Версии приложения платформы; 0 строк — версии не заведены (публичная ручка отдаёт null-поля).
-- name: GetAppVersions :one
SELECT platform, min_version, recommended_version, store_url, version, updated_at
FROM app_versions
WHERE platform = @platform;
