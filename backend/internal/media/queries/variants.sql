-- Варианты (спека media §3.3 п. 7): вставляются при публикации вместе со сменой состояния.

-- name: InsertVariant :exec
-- только при переходе processing → ready под замком строки объекта (§3.3 п. 7): строк вариантов до него нет, конфликт
-- недостижим. Без ON CONFLICT … DO UPDATE: он требует права UPDATE даже без конфликта, а у worker на media_variants
-- UPDATE нет (§5.3)
INSERT INTO media_variants (object_id, name, blob_key, content_type, width, height, bytes)
VALUES (@object_id, @name, @blob_key, @content_type, @width, @height, @bytes);

-- name: ListVariants :many
SELECT * FROM media_variants WHERE object_id = @object_id ORDER BY name;
