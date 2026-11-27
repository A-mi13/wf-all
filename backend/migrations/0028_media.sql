-- 0028_media.sql
-- media: загрузка, обработка и отдача файлов (план media, Task 5; спека docs/superpowers/specs/2026-11-18-media-design.md
-- §4, §5):
--   * media_objects — объект: цель, видимость, состояние, владелец, кто загрузил, заявленное, размеры, причина отказа,
--     попытки обработки, версия агрегата событий (§9). Перечисления — текстом с CHECK (ALTER TYPE не нужен);
--     допустимые пары «состояние — поля» — CHECK по §4 (I-6: не больше одного загрузившего — обнуление
--     uploaded_by_user при удалении аккаунта проходит);
--   * media_uploads — билет загрузки (живёт только у pending_upload: прикрепление удаляет строку); секрета подписи нет;
--   * media_variants — варианты обработанного объекта (ключ хранилища, тип, размеры);
--   * media_read_objects — экспортированное представление для владельцев (§5.4): без «кто загрузил» и заявленного.
-- Колонки владельцев не трогаются (§5.1): ссылку на объект владелец хранит без внешнего ключа (§2.4). Время — из
-- clock.Clock приложения, умолчаний now() нет. Права ролей — grants.sql (ветка media, §5.3).

-- +goose Up

CREATE TABLE media_objects (
    id                uuid PRIMARY KEY,
    purpose           text NOT NULL,
    visibility        text NOT NULL CONSTRAINT media_objects_visibility_check CHECK (visibility IN ('public', 'private')),
    state             text NOT NULL CONSTRAINT media_objects_state_check CHECK (state IN (
                          'pending_upload', 'uploaded', 'processing', 'ready', 'rejected', 'detached', 'taken_down')),
    owner_type        text,
    owner_id          uuid,
    uploaded_by_user  uuid,
    uploaded_by_staff uuid,
    declared_type     text NOT NULL,
    declared_bytes    bigint NOT NULL CONSTRAINT media_objects_declared_bytes_check CHECK (declared_bytes > 0),
    width             int,
    height            int,
    reject_code       text,
    process_attempts  int NOT NULL DEFAULT 0,
    version           bigint NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL,
    uploaded_at       timestamptz,
    ready_at          timestamptz,
    ended_at          timestamptz,
    -- I-6: игрок или сотрудник, не оба; оба NULL — система или обезличенный после удаления аккаунта
    CONSTRAINT media_objects_one_uploader CHECK (uploaded_by_user IS NULL OR uploaded_by_staff IS NULL),
    -- владелец — у всех, кроме pending_upload (у него намеченный владелец — в билете)
    CONSTRAINT media_objects_owner CHECK (CASE WHEN state = 'pending_upload' THEN owner_type IS NULL AND owner_id IS NULL
                                               ELSE owner_type IS NOT NULL AND owner_id IS NOT NULL END),
    CONSTRAINT media_objects_ended CHECK ((ended_at IS NOT NULL) = (state IN ('detached', 'taken_down', 'rejected'))),
    -- coalesce: CHECK с результатом NULL пропускает строку, а '' — не причина
    CONSTRAINT media_objects_reject_code CHECK (CASE WHEN state = 'rejected' THEN coalesce(reject_code, '') <> ''
                                                     ELSE reject_code IS NULL END),
    -- ready_at остаётся у снятого и откреплённого готового; у прочих — нет
    CONSTRAINT media_objects_ready_at CHECK (CASE WHEN state = 'ready' THEN ready_at IS NOT NULL
                                                  WHEN state IN ('detached', 'taken_down') THEN true
                                                  ELSE ready_at IS NULL END),
    -- загружен у всех, кроме pending_upload: из pending_upload выходит только прикрепление (ставит uploaded_at), прямая
    -- загрузка вставляет uploaded сразу с ним; StaleProcessing (uploaded_at < before) на NULL молча не сработал бы
    CONSTRAINT media_objects_uploaded_at CHECK ((uploaded_at IS NULL) = (state = 'pending_upload')),
    CONSTRAINT media_objects_size CHECK ((width IS NULL OR width > 0) AND (height IS NULL OR height > 0))
);

-- чистка конечных по сроку (§3.9)
CREATE INDEX media_objects_cleanup_idx ON media_objects (state, ended_at);
-- объекты владельца
CREATE INDEX media_objects_owner_idx ON media_objects (owner_type, owner_id);
-- удаление аккаунта и подсчёт незавершённых (§3.10, §3.11)
CREATE INDEX media_objects_uploaded_by_user_idx ON media_objects (uploaded_by_user) WHERE uploaded_by_user IS NOT NULL;
-- повторный запуск зависших (§3.9)
CREATE INDEX media_objects_stale_idx ON media_objects (state, uploaded_at) WHERE state IN ('uploaded', 'processing');

CREATE TABLE media_uploads (
    object_id           uuid PRIMARY KEY REFERENCES media_objects ON DELETE CASCADE,
    intended_owner_type text NOT NULL,
    intended_owner_id   uuid NOT NULL,
    method              text NOT NULL CONSTRAINT media_uploads_method_check CHECK (method IN ('PUT', 'POST')),
    max_bytes           bigint NOT NULL,
    expires_at          timestamptz NOT NULL
);

-- окно «завершить» прошло — чистка (§3.9)
CREATE INDEX media_uploads_expires_idx ON media_uploads (expires_at);

CREATE TABLE media_variants (
    object_id    uuid NOT NULL REFERENCES media_objects ON DELETE CASCADE,
    name         text NOT NULL,
    blob_key     text NOT NULL,
    content_type text NOT NULL,
    width        int,
    height       int,
    bytes        bigint,
    PRIMARY KEY (object_id, name)
);

-- варианты — массив по имени; без вариантов — пустой массив
CREATE VIEW media_read_objects AS
SELECT o.id,
       o.purpose,
       o.visibility,
       o.state,
       o.owner_type,
       o.owner_id,
       o.reject_code,
       o.width,
       o.height,
       coalesce((SELECT jsonb_agg(jsonb_build_object('name', v.name, 'blob_key', v.blob_key,
                                                     'content_type', v.content_type, 'width', v.width,
                                                     'height', v.height, 'bytes', v.bytes) ORDER BY v.name)
                   FROM media_variants v
                  WHERE v.object_id = o.id), '[]'::jsonb) AS variants
  FROM media_objects o;

-- +goose Down

DROP VIEW media_read_objects;
DROP TABLE media_variants;
DROP TABLE media_uploads;
DROP TABLE media_objects;
