-- Варианты (спека media §3.3 п. 7): вставляются при публикации вместе со сменой состояния.

-- name: InsertVariant :exec
-- повтор публикации после сбоя перезаписывает строку (ключи и байты те же)
INSERT INTO media_variants (object_id, name, blob_key, content_type, width, height, bytes)
VALUES (@object_id, @name, @blob_key, @content_type, @width, @height, @bytes)
ON CONFLICT (object_id, name) DO UPDATE
   SET blob_key = excluded.blob_key, content_type = excluded.content_type, width = excluded.width,
       height = excluded.height, bytes = excluded.bytes;

-- name: ListVariants :many
SELECT * FROM media_variants WHERE object_id = @object_id ORDER BY name;
