-- Билеты загрузки (спека media §3.1, §3.11): строка живёт только у pending_upload, прикрепление её удаляет.

-- name: InsertUpload :exec
INSERT INTO media_uploads (object_id, intended_owner_type, intended_owner_id, method, max_bytes, expires_at)
VALUES (@object_id, @intended_owner_type, @intended_owner_id, @method, @max_bytes, @expires_at);

-- name: GetUpload :one
SELECT * FROM media_uploads WHERE object_id = @object_id;

-- name: GetUploadWithUploader :one
-- ConfirmUpload: билет и загрузивший — чужой билет (другой намеченный владелец или загрузивший) неотличим от
-- несуществующего (ревью Task 6 m5)
SELECT u.intended_owner_type, u.intended_owner_id, u.expires_at, o.uploaded_by_user, o.uploaded_by_staff
  FROM media_uploads u
  JOIN media_objects o ON o.id = u.object_id
 WHERE u.object_id = @object_id;

-- name: DeleteUpload :exec
DELETE FROM media_uploads WHERE object_id = @object_id;

-- name: PendingLock :exec
-- подсчёт и создание незавершённых билета игрока — по очереди до конца tx (m5): два параллельных «начать» не дают
-- шестой
SELECT pg_advisory_xact_lock(hashtextextended('media.pending:' || (@uploaded_by_user::uuid)::text, 0));

-- name: CountPendingByUser :one
-- незавершённые билеты игрока: pending_upload с билетом, срок которого позже live_after (вызывающий решает границу;
-- просроченные дожидаются чистки и не мешают)
SELECT count(*)
  FROM media_objects o
  JOIN media_uploads u ON u.object_id = o.id
 WHERE o.uploaded_by_user = @uploaded_by_user::uuid AND o.state = 'pending_upload' AND u.expires_at > @live_after;
