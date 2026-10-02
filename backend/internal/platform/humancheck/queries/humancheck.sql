-- Израсходованные решения PoW (спека бэкенда §6.7). Владелец humancheck_spent — platform.

-- 0 строк — решение уже предъявляли: повтор.
-- name: Spend :execrows
INSERT INTO humancheck_spent (signature, expires_at) VALUES (@signature, @expires_at)
ON CONFLICT (signature) DO NOTHING;

-- name: DeleteExpired :execrows
DELETE FROM humancheck_spent
WHERE signature IN (SELECT signature FROM humancheck_spent WHERE expires_at < @now::timestamptz LIMIT @batch);
