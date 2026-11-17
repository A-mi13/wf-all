-- 0027_identity_captaincy.sql
-- identity, капитанство и админка (план I-4, Task 1; спека docs/superpowers/specs/2026-11-13-identity-design.md §5, §7,
-- §17; решения плана R2, R11, R40):
--   * identity_read_auth_method_usage — статистика способов входа для экрана флагов admin-api: только агрегаты, ни
--     одного user_id; исполняется правами владельца (не security_invoker), поэтому admin видит агрегат, не видя
--     user_identities, passkeys и credentials (§5.5, К2);
--   * индексы заявок: очередь всех городов и фильтр статуса, история игрока и пауза 7 дней (по игроку, R5); очередь
--     модератора — прежний captain_applications_queue_idx (city_id, created_at) WHERE status = 'pending';
--   * users.status_before_deletion допускает banned — бан поверх удаления (R11, решение пользователя Q1);
--   * живой код смены почты — один на (purpose, user_id, email) (R40, решение Q5): чужой живой код на тот же адрес не
--     гасится; прочие назначения — по-прежнему один на (purpose, email).

-- +goose Up

-- способы входа без пароля: linked — у скольких способ есть, only_method — входят только им (нет пароля и других
-- способов); удалённые не учитываются; строки всегда три (агрегат без GROUP BY даёт строку и на пустой базе)
CREATE VIEW identity_read_auth_method_usage AS
WITH live AS (
    SELECT EXISTS (SELECT 1 FROM credentials c WHERE c.user_id = u.id) AS password,
           EXISTS (SELECT 1 FROM user_identities i WHERE i.user_id = u.id AND i.provider = 'apple') AS apple,
           EXISTS (SELECT 1 FROM user_identities i WHERE i.user_id = u.id AND i.provider = 'google') AS google,
           EXISTS (SELECT 1 FROM passkeys p WHERE p.user_id = u.id) AS passkeys
      FROM users u
     WHERE u.status <> 'deleted'
)
SELECT 'apple'::text AS method,
       count(*) FILTER (WHERE apple AND NOT password AND NOT google AND NOT passkeys) AS only_method,
       count(*) FILTER (WHERE apple) AS linked
  FROM live
UNION ALL
SELECT 'google'::text,
       count(*) FILTER (WHERE google AND NOT password AND NOT apple AND NOT passkeys),
       count(*) FILTER (WHERE google)
  FROM live
UNION ALL
SELECT 'passkeys'::text,
       count(*) FILTER (WHERE passkeys AND NOT password AND NOT apple AND NOT google),
       count(*) FILTER (WHERE passkeys)
  FROM live;

-- очередь всех городов и фильтр статуса (pending — старые сверху, прочие — по времени); история игрока и пауза
CREATE INDEX captain_applications_status_idx ON captain_applications (status, created_at, id);
CREATE INDEX captain_applications_user_idx ON captain_applications (user_id, created_at DESC);

ALTER TABLE users
    DROP CONSTRAINT users_status_before_deletion,
    -- то же имя: сортируется после users_deletion_pending (порядок проверки CHECK — по имени, см. 0026)
    ADD CONSTRAINT users_status_before_deletion CHECK (
        (status = 'pending_deletion') = (status_before_deletion IS NOT NULL)
        AND (status_before_deletion IS NULL OR status_before_deletion IN ('active', 'suspended', 'banned')));

-- один живой код на (назначение, почта) — кроме смены почты; у смены почты — на (аккаунт, почта): арбитры ON CONFLICT
-- вставки кода (queries/codes.sql) повторяют эти предикаты дословно
DROP INDEX auth_codes_live_key;
CREATE UNIQUE INDEX auth_codes_live_key ON auth_codes (purpose, email)
    WHERE purpose <> 'email_change' AND consumed_at IS NULL AND revoked_at IS NULL;
CREATE UNIQUE INDEX auth_codes_live_email_change_key ON auth_codes (purpose, user_id, email)
    WHERE purpose = 'email_change' AND consumed_at IS NULL AND revoked_at IS NULL;

-- +goose Down

-- Возвращает схему 0026 в точности. Бан поверх удаления и несколько живых кодов смены почты на один адрес схема 0026
-- выразить не может — понятный отказ вместо голого 23514/23505.
-- +goose StatementBegin
DO $$
DECLARE
  n bigint;
BEGIN
  SELECT count(*) INTO n FROM users WHERE status_before_deletion = 'banned';
  IF n > 0 THEN
    RAISE EXCEPTION 'users: строк со status_before_deletion = banned — % (бан поверх удаления, R11; схема 0026 его не допускает): дождитесь окончания бана или удаления и повторите migrate down', n;
  END IF;
  SELECT count(*) INTO n FROM (
    SELECT email FROM auth_codes
     WHERE purpose = 'email_change' AND consumed_at IS NULL AND revoked_at IS NULL
     GROUP BY email HAVING count(*) > 1) d;
  IF n > 0 THEN
    RAISE EXCEPTION 'auth_codes: почт с несколькими живыми кодами смены почты — % (схема 0026 допускает один живой код на назначение и почту): дождитесь их истечения или отзовите лишние (revoked_at) и повторите migrate down', n;
  END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX auth_codes_live_email_change_key;
DROP INDEX auth_codes_live_key;
CREATE UNIQUE INDEX auth_codes_live_key ON auth_codes (purpose, email) WHERE consumed_at IS NULL AND revoked_at IS NULL;

ALTER TABLE users
    DROP CONSTRAINT users_status_before_deletion,
    ADD CONSTRAINT users_status_before_deletion CHECK (
        (status = 'pending_deletion') = (status_before_deletion IS NOT NULL)
        AND (status_before_deletion IS NULL OR status_before_deletion IN ('active', 'suspended')));

DROP INDEX captain_applications_user_idx;
DROP INDEX captain_applications_status_idx;

DROP VIEW identity_read_auth_method_usage;
