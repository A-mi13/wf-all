-- Аудит-лог (спека бэкенда §6.9, §10). Только вставка: изменение и удаление запрещены триггером.

-- name: InsertAudit :exec
INSERT INTO audit_log (id, actor_user_id, actor_role, action, object_type, object_id, before, after, reason, ip, user_agent)
VALUES (@id, @actor_user_id, @actor_role, @action, @object_type, @object_id, @before, @after, @reason, @ip, @user_agent);
