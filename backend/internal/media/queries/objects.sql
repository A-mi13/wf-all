-- Объекты media (спека media §2.3, §3.1, §3.3, §4). Версия агрегата событий (version) растёт на каждую смену
-- состояния; переходы проверяет domain.Transition под замком LockObject, условия состояния в WHERE — защита в глубину
-- (не та строка — pgx.ErrNoRows у :one).

-- name: InsertObject :exec
-- «начать загрузку»: объект pending_upload без владельца (намеченный владелец — в билете media_uploads)
INSERT INTO media_objects (id, purpose, visibility, state, uploaded_by_user, uploaded_by_staff, declared_type,
                           declared_bytes, created_at)
VALUES (@id, @purpose, @visibility, 'pending_upload', sqlc.narg(uploaded_by_user), sqlc.narg(uploaded_by_staff),
        @declared_type, @declared_bytes, @now);

-- name: InsertDirectObject :one
-- прямая загрузка (CreateDirectInTx): байты уже во входящем, объект сразу uploaded с владельцем
INSERT INTO media_objects (id, purpose, visibility, state, owner_type, owner_id, uploaded_by_user, uploaded_by_staff,
                           declared_type, declared_bytes, created_at, uploaded_at)
VALUES (@id, @purpose, @visibility, 'uploaded', @owner_type, @owner_id, sqlc.narg(uploaded_by_user),
        sqlc.narg(uploaded_by_staff), @declared_type, @declared_bytes, @now, @now)
RETURNING *;

-- name: LockObject :one
-- замок строки объекта в сценариях — ждать, не SKIP LOCKED (спека §3.3 п. 1); чужие таблицы не соединяются
SELECT * FROM media_objects WHERE id = @id FOR UPDATE;

-- name: AttachObject :one
-- прикрепление (AttachInTx): pending_upload → uploaded с владельцем
UPDATE media_objects
   SET state = 'uploaded', owner_type = @owner_type, owner_id = @owner_id, uploaded_at = @now, version = version + 1
 WHERE id = @id AND state = 'pending_upload'
RETURNING *;

-- name: SetState :one
-- смена состояния без размеров: processing → uploaded (обычная ошибка), отклонение, открепление, снятие; ended_at и
-- reject_code — по CHECK §4 (у конечных ended_at, у rejected — код)
UPDATE media_objects
   SET state = @state, ended_at = sqlc.narg(ended_at), reject_code = sqlc.narg(reject_code), version = version + 1
 WHERE id = @id
RETURNING *;

-- name: SetProcessing :one
-- захват обработки (§3.3 п. 1): из uploaded — без счёта (обычная ошибка возвращает объект в uploaded и падением не
-- считается); застали processing — прошлый исполнитель упал: попытка + 1
UPDATE media_objects
   SET state = 'processing', process_attempts = process_attempts + CASE WHEN state = 'processing' THEN 1 ELSE 0 END,
       version = version + 1
 WHERE id = @id AND state IN ('uploaded', 'processing')
RETURNING *;

-- name: SetReady :one
-- публикация (§3.3 п. 7): processing → ready с размерами исходника
UPDATE media_objects
   SET state = 'ready', width = @width, height = @height, ready_at = @now, version = version + 1
 WHERE id = @id AND state = 'processing'
RETURNING *;

-- name: ReadObject :one
-- объект для владельца — из экспортированного представления (без «кто загрузил»)
SELECT * FROM media_read_objects WHERE id = @id;

-- name: ObjectExists :one
SELECT EXISTS (SELECT 1 FROM media_objects WHERE id = @id);

-- name: ReadObjects :many
-- объекты владельца пачкой (списки) — из того же представления
SELECT * FROM media_read_objects WHERE id = ANY (@ids::uuid[]);

-- name: SumDeclaredBytesByUser :one
-- суточный объём игрока (§3.11, R19): заявленные байты объектов, созданных позже since (граница строгая); считается
-- под PendingLock вместе с незавершёнными
SELECT coalesce(sum(declared_bytes), 0)::bigint AS total
  FROM media_objects
 WHERE uploaded_by_user = @uploaded_by_user::uuid AND created_at > @since;
