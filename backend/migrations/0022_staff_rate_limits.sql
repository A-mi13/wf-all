-- 0022_staff_rate_limits.sql
-- Лимиты admin-api — своя таблица (план B, Task 3, R2; спека сотрудников §3.2–3.3). Ключи входа сотрудников
-- (email:<hmac>, ip:staff_auth:<ip>), субъекта (staff:<класс>:<id>) и потолок аудита приглашений жили в
-- rate_limits, где у роли api DML: взлом публичного API стирал бы счётчики перебора входа сотрудника или
-- закрывал вход всем вставкой большого tat. Права на staff_rate_limits — только у admin (ветка staff_* в
-- grants.sql); у admin на rate_limits — ничего. Схема — копия rate_limits (0018), имена ограничений свои.
-- Чистит тикер admin-api (staff/jobs.Cleaner): у воркера прав нет.

-- +goose Up

-- UNLOGGED, как rate_limits: после сбоя сервера пуста — короткое окно без лимита допустимо
CREATE UNLOGGED TABLE staff_rate_limits (
    key text        PRIMARY KEY,
    tat timestamptz NOT NULL
);

CREATE INDEX staff_rate_limits_tat_idx ON staff_rate_limits (tat);

-- +goose Down

DROP TABLE staff_rate_limits;
