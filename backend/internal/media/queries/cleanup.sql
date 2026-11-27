-- Чистка и удаление аккаунта (спека media §3.9, §3.10): пачками SKIP LOCKED — чистка уступает сценарию; граница
-- строгая.

-- name: ExpiredPending :many
-- pending_upload, окно «завершить» прошло: вызывающий передаёт before = now − 1 ч (окно expires_at + 1 ч, §3.1 п. 5).
-- Страницами по id после after: строку моложе суток чистка оставляет (квота байт считается по строкам) — без курсора
-- она занимала бы пачку на каждом проходе. Замок + JOIN: SKIP LOCKED не ждёт, а строку, прикреплённую после снимка,
-- перепроверка READ COMMITTED отсекает по o.state (билет прикрепление удаляет в той же tx)
SELECT o.id, o.created_at
  FROM media_objects o
  JOIN media_uploads u ON u.object_id = o.id
 WHERE o.state = 'pending_upload' AND u.expires_at < @before AND o.id > @after::uuid
 ORDER BY o.id
 LIMIT @lim
   FOR UPDATE OF o SKIP LOCKED;

-- name: StaleProcessing :many
-- зависшие uploaded/processing (uploaded_at < before = now − 1 ч): media.process вставляется заново (UniqueOpts —
-- дубля нет), замок не нужен; страницами по id после after (состояние вставка не меняет)
SELECT id
  FROM media_objects
 WHERE state IN ('uploaded', 'processing') AND uploaded_at < @before AND id > @after::uuid
 ORDER BY id
 LIMIT @lim;

-- name: EndedBefore :many
-- конечные строки старше срока своего состояния (detached/taken_down — 24 ч, rejected — 30 сут: вызывающий
-- передаёт состояние и границу); цель и видимость — ключи вариантов и бакет для удаления файлов
SELECT id, purpose, visibility
  FROM media_objects
 WHERE state = @state AND ended_at < @before
 ORDER BY ended_at
 LIMIT @lim
   FOR UPDATE SKIP LOCKED;

-- name: DeleteObjects :many
-- удаление строк под замком чистки (билет и варианты — каскадом); живые (uploaded, processing, ready) не удаляются
-- никогда — защита в глубину от ошибочного id
DELETE FROM media_objects
 WHERE id = ANY (@ids::uuid[]) AND state IN ('pending_upload', 'rejected', 'detached', 'taken_down')
RETURNING id;

-- name: ClearUploadedByUser :execrows
-- удаление аккаунта (I-6): обезличивание загрузившего; CHECK «не больше одного» допускает оба NULL
UPDATE media_objects SET uploaded_by_user = NULL WHERE uploaded_by_user = @uploaded_by_user::uuid;

-- name: DeletePendingByUser :many
-- удаление аккаунта: незавершённые билеты аккаунта (входящее по id удаляет вызывающий)
DELETE FROM media_objects
 WHERE uploaded_by_user = @uploaded_by_user::uuid AND state = 'pending_upload'
RETURNING id;

-- name: ReadyVariantKeys :many
-- сверка хранилища (worker media reconcile): ключи вариантов опубликованных объектов и видимость (бакет) по порядку
-- ключа, страницами после after; варианты одного объекта идут подряд (ключ начинается с id)
SELECT v.object_id, o.visibility, v.blob_key
  FROM media_variants v
  JOIN media_objects o ON o.id = v.object_id
 WHERE o.state = 'ready' AND v.blob_key > @after::text
 ORDER BY v.blob_key
 LIMIT @lim;
