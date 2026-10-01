-- Права ролей БД (спека бэкенда §10.1). Применяет cmd/migrate после up, down и reset: права на
-- таблицы, созданные миграциями, иначе не выдать. Идемпотентно: на каждом объекте сначала
-- REVOKE ALL, потом ровно нужное. Обходятся только объекты public, которыми владеет текущая
-- роль, кроме объектов расширений (spatial_ref_sys PostGIS). Роли нет — пропуск с NOTICE
-- (на Neon пока есть только api). Правила по объектам — в ветках CASE ниже.
DO $grants$
DECLARE
  r     record;
  who   text;
  privs text;
BEGIN
  FOREACH who IN ARRAY ARRAY['api', 'admin', 'worker'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = who) THEN
      RAISE NOTICE 'grants: роли % нет — пропуск', who;
      CONTINUE;
    END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA public TO %I', who);
    FOR r IN
      SELECT c.relname AS name, c.relkind AS kind
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname = 'public'
        AND c.relkind IN ('r', 'p', 'v', 'm', 'S')
        AND c.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user)
        AND NOT EXISTS (SELECT 1 FROM pg_depend d
                        WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
    LOOP
      EXECUTE format('REVOKE ALL ON %I FROM %I', r.name, who);
      privs := CASE
        WHEN r.kind = 'S' THEN 'USAGE, SELECT'
        -- служебное мигратора
        WHEN r.name = 'goose_db_version' THEN NULL
        -- аудит только дописывается; читает — только админка
        WHEN r.name = 'audit_log' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT' ELSE 'INSERT' END
        -- вход сотрудников — только admin-api
        WHEN r.name LIKE 'staff\_%' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- пароли: воркер только удаляет (удаление аккаунта), хеш прочитать не может; SELECT (user_id)
        -- для WHERE удаления выдаётся колоночно ниже
        WHEN r.name = 'credentials' THEN CASE who WHEN 'worker' THEN 'DELETE' ELSE 'SELECT, INSERT, UPDATE, DELETE' END
        -- события: API и админка публикуют, воркер раскладывает и чистит
        WHEN r.name = 'outbox' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                             WHEN 'admin' THEN 'SELECT, INSERT' ELSE 'INSERT' END
        WHEN r.name IN ('event_inbox', 'event_cursors') THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- очередь: API ставит задачи (письма с кодами), админка смотрит и перезапускает, воркер
        -- исполняет; UPDATE у api — уникальная вставка River идёт через ON CONFLICT DO UPDATE.
        -- MAINTAIN (PG17+) у воркера — лидер River ежедневно переиндексирует river_job
        WHEN r.name = 'river_job' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE, MAINTAIN'
                                                ELSE 'SELECT, INSERT, UPDATE' END
        WHEN r.name LIKE 'river\_%' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        WHEN r.kind IN ('v', 'm') THEN 'SELECT'
        ELSE 'SELECT, INSERT, UPDATE, DELETE'
      END;
      IF privs IS NOT NULL THEN
        EXECUTE format('GRANT %s ON %I TO %I', privs, r.name, who);
      END IF;
      -- DELETE … WHERE user_id = $1 требует SELECT на колонки из WHERE. REVOKE ALL выше снимает
      -- и колоночные права, поэтому выдача идемпотентна. Нет колонки — нечего выдавать.
      IF r.name = 'credentials' AND who = 'worker' AND EXISTS (
           SELECT 1 FROM pg_attribute a
           WHERE a.attrelid = format('%I', r.name)::regclass AND a.attname = 'user_id'
             AND a.attnum > 0 AND NOT a.attisdropped) THEN
        EXECUTE format('GRANT SELECT (user_id) ON %I TO %I', r.name, who);
      END IF;
    END LOOP;
  END LOOP;
END
$grants$;
