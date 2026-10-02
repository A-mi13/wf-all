-- Идемпотентность мутирующих запросов (спека бэкенда §6.4). Владелец idempotency_keys — platform.
-- Время — время базы: срок ключа (24 ч) задаёт DEFAULT таблицы.

-- name: Get :one
SELECT request_hash, response_status, response_headers, response_body
FROM idempotency_keys
WHERE user_id = @user_id AND key = @key AND expires_at > now();

-- Вставка ключа внутри бизнес-транзакции. Просроченный ключ переиспользуется; живой — 0 строк.
-- Параллельный дубль ждёт здесь на уникальном индексе, пока первая транзакция не закончится
-- (не дольше lock_timeout).
-- name: Claim :one
INSERT INTO idempotency_keys AS k (user_id, key, endpoint, request_hash)
VALUES (@user_id, @key, @endpoint, @request_hash)
ON CONFLICT (user_id, key) DO UPDATE
SET endpoint = excluded.endpoint,
    request_hash = excluded.request_hash,
    response_status = NULL,
    response_headers = NULL,
    response_body = NULL,
    created_at = now(),
    expires_at = now() + interval '24 hours'
WHERE k.expires_at <= now()
RETURNING created_at;

-- Ответ сохраняется после коммита бизнес-транзакции; 0 строк — транзакция откатилась (ключа нет).
-- name: SaveResponse :execrows
UPDATE idempotency_keys
SET response_status = @status, response_headers = @headers, response_body = @body
WHERE user_id = @user_id AND key = @key AND request_hash = @request_hash AND response_status IS NULL;

-- name: CurrentLockTimeout :one
SELECT current_setting('lock_timeout')::text;

-- Только до конца текущей транзакции (is_local = true).
-- name: SetLockTimeout :exec
SELECT set_config('lock_timeout', @value::text, true);

-- name: DeleteExpired :execrows
DELETE FROM idempotency_keys
WHERE (user_id, key) IN (SELECT user_id, key FROM idempotency_keys WHERE expires_at <= now() LIMIT @batch);
