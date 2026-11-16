-- Ротация кольца секретов identity (спека §5.4; план I-2, Task 19, R28): команда api identity rekey под ролью api.
-- Перешифровываются только долгоживущие шифротексты: user_mfa.secret_enc (и неподтверждённые) и
-- user_identities.apple_refresh_enc. Запись — условная (тот же key_id и тот же шифротекст): строку, которую сценарий
-- сменил между чтением и записью, команда не перетирает. Замков сверх строки UPDATE нет (R30: команда держит одну
-- строку и ничего не ждёт после неё — цикла со сценариями, которые берут users → user_mfa, быть не может).

-- name: RekeyMfaKeyCounts :many
SELECT key_id, count(*)::int AS n FROM user_mfa GROUP BY key_id ORDER BY key_id;

-- name: RekeyIdentityKeyCounts :many
SELECT key_id::text AS key_id, count(*)::int AS n
FROM user_identities
WHERE apple_refresh_enc IS NOT NULL
GROUP BY key_id
ORDER BY key_id;

-- name: RekeyRecoveryOldKeyCounts :many
-- неиспользованные резервные коды под ключами, отличными от текущего: MAC не пересчитать, ключ убирается из кольца
-- только когда их не осталось (перевыпуск владельцем)
SELECT key_id, count(*)::int AS n
FROM user_recovery_codes
WHERE used_at IS NULL AND key_id <> @current_key
GROUP BY key_id
ORDER BY key_id;

-- name: RekeyMfaBatch :many
-- пачка строк не под текущим ключом после курсора after (uuid.Nil — с начала), без замка
SELECT user_id, secret_enc, key_id
FROM user_mfa
WHERE key_id <> @current_key
  AND user_id > @after
ORDER BY user_id
LIMIT @batch;

-- name: RekeyMfaUpdate :execrows
UPDATE user_mfa SET secret_enc = @new_enc, key_id = @new_key
WHERE user_id = @user_id AND key_id = @old_key AND secret_enc = @old_enc;

-- name: RekeyIdentityBatch :many
SELECT id, apple_refresh_enc, key_id::text AS key_id
FROM user_identities
WHERE apple_refresh_enc IS NOT NULL
  AND key_id <> @current_key
  AND id > @after
ORDER BY id
LIMIT @batch;

-- name: RekeyIdentityUpdate :execrows
UPDATE user_identities SET apple_refresh_enc = @new_enc, key_id = @new_key
WHERE id = @id AND key_id = @old_key AND apple_refresh_enc = @old_enc;
