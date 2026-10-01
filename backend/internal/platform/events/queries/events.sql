-- События платформы (спека бэкенда §6.5). Владелец outbox, event_inbox, event_cursors — platform.

-- name: InsertEvent :exec
INSERT INTO outbox (id, event_type, schema_version, aggregate_type, aggregate_id, aggregate_version, payload)
VALUES (@id, @event_type, @schema_version, @aggregate_type, @aggregate_id, @aggregate_version, @payload);

-- Пачка для relay; SKIP LOCKED — несколько воркеров берут разные строки.
-- name: LockUnpublished :many
SELECT * FROM outbox
WHERE published_at IS NULL
ORDER BY occurred_at, id
LIMIT @batch
FOR UPDATE SKIP LOCKED;

-- name: MarkPublished :exec
UPDATE outbox SET published_at = now() WHERE id = ANY(@ids::uuid[]);

-- name: GetEvent :one
SELECT * FROM outbox WHERE id = @id;

-- name: EventIDsForReplay :many
SELECT id FROM outbox
WHERE event_type = @event_type AND occurred_at >= @since
ORDER BY occurred_at, id;

-- 0 строк — событие этим подписчиком уже обработано.
-- name: InsertInbox :execrows
INSERT INTO event_inbox (subscriber, event_id) VALUES (@subscriber, @event_id)
ON CONFLICT DO NOTHING;

-- Курсор создаётся заранее, чтобы параллельные события одного агрегата встали в очередь на
-- блокировке строки, а не разошлись на «строки ещё нет».
-- name: EnsureCursor :exec
INSERT INTO event_cursors (subscriber, aggregate_type, aggregate_id, version)
VALUES (@subscriber, @aggregate_type, @aggregate_id, 0)
ON CONFLICT DO NOTHING;

-- name: LockCursor :one
SELECT version FROM event_cursors
WHERE subscriber = @subscriber AND aggregate_type = @aggregate_type AND aggregate_id = @aggregate_id
FOR UPDATE;

-- name: AdvanceCursor :exec
UPDATE event_cursors SET version = @version, updated_at = now()
WHERE subscriber = @subscriber AND aggregate_type = @aggregate_type AND aggregate_id = @aggregate_id
  AND version < @version;

-- Чистка (спека §6.5, §9.3): опубликованное и обработанное старше 30 дней, пачками.
-- name: DeleteOldPublished :execrows
DELETE FROM outbox WHERE id IN (
  SELECT id FROM outbox WHERE published_at < now() - interval '30 days' LIMIT @batch
);

-- name: DeleteOldInbox :execrows
DELETE FROM event_inbox WHERE (subscriber, event_id) IN (
  SELECT subscriber, event_id FROM event_inbox WHERE processed_at < now() - interval '30 days' LIMIT @batch
);
