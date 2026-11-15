-- Загрузчик сессии публичного API (спека identity §6.1, бэкенда §6.2; план I-2, Task 11): одним запросом сессия,
-- статус аккаунта, действующие ограничения и живые допуски капитана. Нет строки — сессия недействительна.

-- name: LoaderSession :one
SELECT s.user_id, s.device_id, u.status::text AS status,
       COALESCE((SELECT array_agg(DISTINCT r.capability ORDER BY r.capability)
                 FROM user_restrictions r
                 WHERE r.user_id = s.user_id AND r.lifted_at IS NULL
                   AND (r.until IS NULL OR r.until > sqlc.arg(now)::timestamptz)), '{}')::text[] AS restrictions,
       COALESCE((SELECT array_agg(ra.scope_id ORDER BY ra.scope_id)
                 FROM role_assignments ra
                 WHERE ra.user_id = s.user_id AND ra.role = 'captain' AND ra.scope_type = 'city'
                   AND ra.revoked_at IS NULL), '{}')::uuid[] AS captain_cities
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id = @id
  AND s.revoked_at IS NULL
  AND s.idle_expires_at > sqlc.arg(now)::timestamptz
  AND s.absolute_expires_at > sqlc.arg(now)::timestamptz
  AND u.status IN ('active', 'suspended');
