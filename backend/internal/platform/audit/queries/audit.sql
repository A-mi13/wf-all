-- Аудит-лог (спека бэкенда §6.9, §10). Только вставка: изменение и удаление запрещены триггером.

-- name: InsertAudit :exec
INSERT INTO audit_log (id, actor_user_id, actor_staff_id, actor_role, actor_label, action, object_type, object_id,
                       scope_city_id, before, after, reason, ip, user_agent)
VALUES (@id, @actor_user_id, @actor_staff_id, @actor_role, @actor_label, @action, @object_type, @object_id,
        @scope_city_id, @before, @after, @reason, @ip, @user_agent);

-- Журнал действий (спека §5.3): новые сверху, keyset по (created_at, id). Пустой фильтр (NULL) не
-- сужает выборку. Действие: action_prefix — начало строки (starts_with, без шаблонов LIKE), action_exact —
-- точное действие (вместе с action_prefix «<действие>.» — действие и его подвиды; выбирает audit.List).
-- scoped — журнал модератора: записи его городов и его собственные.
-- name: ListAudit :many
SELECT id, actor_user_id, actor_staff_id, actor_label, actor_role, action, object_type, object_id,
       scope_city_id, before, after, reason, ip, user_agent, created_at
FROM audit_log
WHERE (sqlc.narg(actor_staff_id)::uuid IS NULL OR actor_staff_id = sqlc.narg(actor_staff_id))
  AND (sqlc.narg(action_prefix)::text IS NULL OR starts_with(action, sqlc.narg(action_prefix))
       OR action = sqlc.narg(action_exact)::text)
  AND (sqlc.narg(object_type)::text IS NULL OR object_type = sqlc.narg(object_type))
  AND (sqlc.narg(object_id)::uuid IS NULL OR object_id = sqlc.narg(object_id))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR created_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR created_at < sqlc.narg(to_at))
  AND (NOT @scoped::bool OR scope_city_id = ANY(@city_ids::uuid[]) OR actor_staff_id = @own_staff_id::uuid)
  AND (NOT @has_cursor::bool OR (created_at, id) < (@cursor_at::timestamptz, @cursor_id::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- Одна запись журнала по id (письмо о действии ссылается на неё); поля — как у ListAudit.
-- name: GetAudit :one
SELECT id, actor_user_id, actor_staff_id, actor_label, actor_role, action, object_type, object_id,
       scope_city_id, before, after, reason, ip, user_agent, created_at
FROM audit_log
WHERE id = @id;
