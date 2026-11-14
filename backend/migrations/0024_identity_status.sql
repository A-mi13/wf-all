-- 0024_identity_status.sql
-- identity (спека docs/superpowers/specs/2026-11-13-identity-design.md §5.1, §3.11): статус pending_deletion —
-- период отмены удаления аккаунта. Новое значение enum нельзя использовать в той же транзакции, где оно добавлено
-- (CHECK и представления 0025 его используют), поэтому — отдельная миграция без транзакции.
-- +goose NO TRANSACTION

-- +goose Up
ALTER TYPE user_status ADD VALUE IF NOT EXISTS 'pending_deletion' BEFORE 'deleted';

-- +goose Down
-- Убрать значение из enum в PostgreSQL нельзя — тип пересоздаётся (как 0020 с ролями). Миграция без транзакции,
-- поэтому всё — одним блоком DO: он атомарен, отказ или сбой посередине не оставляют полупересозданный тип.
-- Строки pending_deletion в прежний тип не лягут — понятный отказ: переведите их в другой статус и повторите.
-- +goose StatementBegin
DO $$
DECLARE
  n bigint;
BEGIN
  SELECT count(*) INTO n FROM users WHERE status::text = 'pending_deletion';
  IF n > 0 THEN
    RAISE EXCEPTION 'users: строк в статусе pending_deletion — % (откат 0024 убирает это значение): отмените удаление или завершите анонимизацию и повторите migrate down', n;
  END IF;
  ALTER TABLE users ALTER COLUMN status DROP DEFAULT;
  ALTER TABLE users ALTER COLUMN status TYPE text USING status::text;
  DROP TYPE user_status;
  CREATE TYPE user_status AS ENUM ('active', 'suspended', 'banned', 'deleted');
  ALTER TABLE users ALTER COLUMN status TYPE user_status USING status::user_status;
  ALTER TABLE users ALTER COLUMN status SET DEFAULT 'active';
END
$$;
-- +goose StatementEnd
