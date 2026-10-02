-- Rate limit по GCRA (спека бэкенда §6.6). Владелец rate_limits — platform. Время — из часов
-- приложения (@now): тесты двигают их без ожидания.
-- Перед «+» — sqlc.arg(now), не @now: «@» в PostgreSQL ещё и префиксный оператор с приоритетом
-- ниже «+», и «@now::timestamptz + x» разбирается как «@(now::timestamptz + x)» — sqlc ломает запрос.

-- Запрос влезает в всплеск — tat сдвигается, строка возвращается; не влезает — 0 строк,
-- tat не меняется. Один оператор: гонок между проверкой и записью нет.
-- name: Take :one
INSERT INTO rate_limits AS r (key, tat)
VALUES (@key, sqlc.arg(now)::timestamptz + make_interval(secs => @interval_s::float8))
ON CONFLICT (key) DO UPDATE
SET tat = greatest(r.tat, @now::timestamptz) + make_interval(secs => @interval_s::float8)
WHERE greatest(r.tat, @now::timestamptz) + make_interval(secs => @interval_s::float8)
      <= sqlc.arg(now)::timestamptz + make_interval(secs => @window_s::float8)
RETURNING tat;

-- Счётчик сигнала риска: расходуется всегда, даже сверх порога.
-- name: Add :exec
INSERT INTO rate_limits AS r (key, tat)
VALUES (@key, sqlc.arg(now)::timestamptz + make_interval(secs => @interval_s::float8))
ON CONFLICT (key) DO UPDATE
SET tat = greatest(r.tat, @now::timestamptz) + make_interval(secs => @interval_s::float8);

-- name: GetTAT :one
SELECT tat FROM rate_limits WHERE key = @key;

-- Строка с tat в прошлом ничего не ограничивает. Пачками — короткие транзакции.
-- name: DeleteExpired :execrows
DELETE FROM rate_limits
WHERE key IN (SELECT key FROM rate_limits WHERE tat < @now::timestamptz LIMIT @batch);
