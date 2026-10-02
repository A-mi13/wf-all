-- 0018_platform_edge.sql
-- Край платформы (спека бэкенда §6.4, §6.6, §6.7, §11): rate limit, израсходованные решения
-- антибота, заголовки сохранённого ответа идемпотентного запроса.

-- +goose Up

-- GCRA: одна строка на ключ, tat — «теоретическое время прихода» следующего запроса.
-- UNLOGGED: без WAL — быстрее; после сбоя сервера таблица пуста, и это допустимо: худшее —
-- короткое окно без лимита. Строки с tat в прошлом ничего не ограничивают — их чистит воркер.
CREATE UNLOGGED TABLE rate_limits (
    key text        PRIMARY KEY,
    tat timestamptz NOT NULL
);

CREATE INDEX rate_limits_tat_idx ON rate_limits (tat);

-- Использованные решения PoW (протокол ALTCHA) — до истечения срока задачи: повтор решения
-- не проходит. UNLOGGED: после сбоя повтор возможен до конца срока задачи (минуты) — допустимо.
CREATE UNLOGGED TABLE humancheck_spent (
    signature  text        PRIMARY KEY,
    expires_at timestamptz NOT NULL
);

CREATE INDEX humancheck_spent_expiry_idx ON humancheck_spent (expires_at);

-- Заголовки сохранённого ответа (Content-Type, Location, ETag): повтор запроса получает тот же
-- ответ, включая адрес созданного ресурса.
ALTER TABLE idempotency_keys ADD COLUMN response_headers jsonb;

-- +goose Down

ALTER TABLE idempotency_keys DROP COLUMN response_headers;
DROP TABLE humancheck_spent;
DROP TABLE rate_limits;
