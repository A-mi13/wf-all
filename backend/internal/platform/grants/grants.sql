-- Права ролей БД (спека бэкенда §10.1). Применяет cmd/migrate после up, down и reset: права на
-- таблицы, созданные миграциями, иначе не выдать. Идемпотентно: на каждом объекте сначала
-- REVOKE ALL, потом ровно нужное. Обходятся только объекты public, которыми владеет текущая
-- роль, кроме объектов расширений (spatial_ref_sys PostGIS). Роли нет — пропуск с NOTICE
-- (на Neon — роли api и worker: worker для разовых команд оператора, docs/deploy-dev.md). Правила по объектам — в ветках CASE ниже.
DO $grants$
DECLARE
  r       record;
  who     text;
  privs   text;
  colpriv text;
  cols    text[];
BEGIN
  FOREACH who IN ARRAY ARRAY['api', 'admin', 'worker'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = who) THEN
      RAISE NOTICE 'grants: роли % нет — пропуск', who;
      CONTINUE;
    END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA public TO %I', who);
    FOR r IN
      SELECT c.oid AS oid, c.relname AS name, c.relkind AS kind
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
        -- последовательности сотрудников и подтверждений — только admin-api (M12)
        WHEN r.kind = 'S' AND (r.name LIKE 'staff\_%' OR r.name LIKE 'approvals\_%') THEN
          CASE who WHEN 'admin' THEN 'USAGE, SELECT' END
        WHEN r.kind = 'S' THEN 'USAGE, SELECT'
        -- служебное мигратора: пишет только он; читают все — admin-api и воркер ждут версию схемы при старте
        -- (migrate.WaitSchema, план C R3); версия схемы не секрет
        WHEN r.name = 'goose_db_version' THEN 'SELECT'
        -- аудит только дописывается; читает — только админка
        WHEN r.name = 'audit_log' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT' ELSE 'INSERT' END
        -- вход сотрудников, ключи идемпотентности admin-api (staff_idempotency_keys: ответы с ПД) и его
        -- лимиты (staff_rate_limits: счётчики перебора входа) — только admin-api
        WHEN r.name LIKE 'staff\_%' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- лимиты игроков: API считает, воркер чистит; admin-api ходит только в staff_rate_limits (ветка
        -- выше, план B R2) — у admin ничего
        WHEN r.name = 'rate_limits' THEN CASE who WHEN 'admin' THEN NULL ELSE 'SELECT, INSERT, UPDATE, DELETE' END
        -- ключи идемпотентности игроков: API ведёт, воркер чистит; admin-api ходит только в
        -- staff_idempotency_keys (ветка выше) — у admin ничего
        WHEN r.name = 'idempotency_keys' THEN CASE who WHEN 'admin' THEN NULL ELSE 'SELECT, INSERT, UPDATE, DELETE' END
        -- подтверждения вдвоём (D2) — только admin-api; DELETE — чистка решённых старше 180 дней
        WHEN r.name = 'approvals' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- identity (спека identity §5.5): каждая таблица — своей веткой, умолчание ELSE дало бы DML всем, а для
        -- секретов это неприемлемо. admin-api в I-2 не пишет users и sessions (К2); секреты (пароли, коды, токены
        -- шагов, 2FA, привязки, ключи доступа) admin не видит вовсе, воркер — только колонки для WHERE удаления
        -- (колоночно ниже). Порядок веток важен: staff_player_links ловит ветка staff_* выше
        WHEN r.name = 'users' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                            WHEN 'worker' THEN 'SELECT, UPDATE' ELSE 'SELECT' END
        -- воркер: удаление аккаунта и чистка; читает только колонки WHERE (ниже)
        WHEN r.name IN ('credentials', 'auth_challenges', 'user_mfa', 'user_recovery_codes', 'passkeys', 'consents') THEN
          CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE' WHEN 'worker' THEN 'DELETE' END
        -- коды из письма: воркер собирает письмо (code_enc) и чистит
        WHEN r.name = 'auth_codes' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                 WHEN 'worker' THEN 'SELECT, DELETE' END
        -- привязки провайдеров: воркер отзывает токен Apple и чистит
        WHEN r.name = 'user_identities' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                      WHEN 'worker' THEN 'SELECT, UPDATE, DELETE' END
        -- сброс 2FA: заводит и отменяет сотрудник (admin), отменяет владелец (api), исполняет по сроку воркер
        WHEN r.name = 'mfa_resets' THEN CASE who WHEN 'admin' THEN 'SELECT, INSERT, UPDATE' ELSE 'SELECT, UPDATE' END
        -- сессии: admin только считает (без UPDATE, К2); refresh-токены admin не видит
        WHEN r.name IN ('sessions', 'refresh_tokens') THEN CASE who
          WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
          WHEN 'worker' THEN 'SELECT, UPDATE, DELETE'
          WHEN 'admin' THEN CASE r.name WHEN 'sessions' THEN 'SELECT' END
        END
        WHEN r.name IN ('devices', 'device_accounts') THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                                   WHEN 'worker' THEN 'SELECT, UPDATE' ELSE 'SELECT' END
        -- события безопасности только дописываются; воркер стирает адрес для писем (UPDATE (meta_enc) ниже, К6) и
        -- чистит; admin пишет события сброса 2FA
        WHEN r.name = 'security_events' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT'
                                                      WHEN 'worker' THEN 'SELECT, INSERT, DELETE' ELSE 'INSERT' END
        -- ограничения: запись admin — со спекой moderation (К2)
        WHEN r.name = 'user_restrictions' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                        WHEN 'worker' THEN 'SELECT, UPDATE' ELSE 'SELECT' END
        -- допуски и заявки капитана: решения — в админке; воркер анонимизирует
        WHEN r.name IN ('role_assignments', 'captain_applications') THEN CASE who WHEN 'worker' THEN 'SELECT, UPDATE'
                                                                                 ELSE 'SELECT, INSERT, UPDATE, DELETE' END
        -- ники: карантин при анонимизации и чистка — воркер
        WHEN r.name = 'nickname_reservations' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                            WHEN 'worker' THEN 'SELECT, INSERT, DELETE' ELSE 'SELECT' END
        WHEN r.name IN ('nickname_changes', 'reserved_nicknames') THEN CASE who
          WHEN 'api' THEN 'SELECT, INSERT, UPDATE, DELETE' WHEN 'worker' THEN 'SELECT, DELETE' ELSE 'SELECT' END
        -- события: API и админка публикуют, воркер раскладывает и чистит
        WHEN r.name = 'outbox' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                             WHEN 'admin' THEN 'SELECT, INSERT' ELSE 'INSERT' END
        WHEN r.name IN ('event_inbox', 'event_cursors') THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE' END
        -- очередь: API ставит задачи (письма с кодами); воркер и admin-api исполняют (admin-api —
        -- очередь staff, D3) и могут стать лидером River: чистка, переиндексация river_job (MAINTAIN,
        -- PG17+), служебные таблицы; UPDATE у api — уникальная вставка River идёт через ON CONFLICT DO UPDATE
        WHEN r.name = 'river_job' THEN CASE who WHEN 'api' THEN 'SELECT, INSERT, UPDATE'
                                                ELSE 'SELECT, INSERT, UPDATE, DELETE, MAINTAIN' END
        WHEN r.name LIKE 'river\_%' THEN CASE who WHEN 'api' THEN NULL
                                                  ELSE 'SELECT, INSERT, UPDATE, DELETE' END
        -- справочник geo (спека geo §3.8): API только читает; админка правит, но не удаляет —
        -- районы архивируются, города и страны остаются; исключение — строка истории slug: возврат
        -- города к своему старому slug удаляет её (спека сотрудников §3.3); воркер (сверка импорта,
        -- §5.4) обновляет города и регионы, страны только читает
        WHEN r.name IN ('countries', 'regions', 'cities', 'districts', 'city_slug_history') THEN CASE who
          WHEN 'api' THEN 'SELECT'
          WHEN 'admin' THEN CASE r.name WHEN 'city_slug_history' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                        ELSE 'SELECT, INSERT, UPDATE' END
          WHEN 'worker' THEN CASE WHEN r.name IN ('cities', 'regions') THEN 'SELECT, UPDATE'
                                  WHEN r.name = 'countries' THEN 'SELECT' END
        END
        -- переводы: админка удаляет перевод; воркер дописывает только недостающие переводы
        -- городов и регионов (ON CONFLICT DO NOTHING), переводы стран только читает
        WHEN r.name IN ('country_names', 'region_names', 'city_names') THEN CASE who
          WHEN 'api' THEN 'SELECT'
          WHEN 'admin' THEN 'SELECT, INSERT, UPDATE, DELETE'
          WHEN 'worker' THEN CASE r.name WHEN 'country_names' THEN 'SELECT' ELSE 'SELECT, INSERT' END
        END
        -- источник GeoNames пишет только воркер; журнал импорта API не видит
        WHEN r.name = 'geonames_imports' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                       WHEN 'admin' THEN 'SELECT' END
        WHEN r.name LIKE 'geonames\_%' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, UPDATE, DELETE'
                                                     ELSE 'SELECT' END
        -- минимальная версия приложения: API читает, админка задаёт; воркеру не нужна
        WHEN r.name = 'app_versions' THEN CASE who WHEN 'api' THEN 'SELECT'
                                                   WHEN 'admin' THEN 'SELECT, INSERT, UPDATE' END
        -- media (спека media §5.3): api и admin создают объекты, прикрепляют, открепляют и снимают (без DELETE строк),
        -- ведут билеты (прикрепление удаляет билет), варианты только читают; воркер меняет состояние, размеры,
        -- попытки и обезличивает загрузившего, пишет варианты, чистит объекты и билеты, объектов не создаёт.
        -- Представление media_read_objects — ветка представлений ниже. TRUNCATE — никому
        WHEN r.name = 'media_objects' THEN CASE who WHEN 'worker' THEN 'SELECT, UPDATE, DELETE'
                                                    ELSE 'SELECT, INSERT, UPDATE' END
        WHEN r.name = 'media_uploads' THEN CASE who WHEN 'worker' THEN 'SELECT, DELETE'
                                                    ELSE 'SELECT, INSERT, DELETE' END
        WHEN r.name = 'media_variants' THEN CASE who WHEN 'worker' THEN 'SELECT, INSERT, DELETE' ELSE 'SELECT' END
        WHEN r.kind IN ('v', 'm') THEN 'SELECT'
        ELSE 'SELECT, INSERT, UPDATE, DELETE'
      END;
      IF privs IS NOT NULL THEN
        EXECUTE format('GRANT %s ON %I TO %I', privs, r.name, who);
      END IF;
      -- Колоночные права (спека identity §5.5): DELETE … WHERE user_id = $1 требует SELECT на колонки из WHERE;
      -- воркер стирает адрес для писем (К6); admin видит, есть ли у игрока 2FA, но не секрет. REVOKE ALL выше
      -- снимает и колоночные права, поэтому выдача идемпотентна. Нет хоть одной колонки (схема до 0025) —
      -- нечего выдавать.
      colpriv := NULL;
      cols := NULL;
      IF who = 'worker' AND r.name = 'user_mfa' THEN
        -- есть ли подтверждённая 2FA (письмо отмены сброса и исполнение сброса); confirmed_at — не секрет
        colpriv := 'SELECT'; cols := ARRAY['user_id', 'confirmed_at'];
      ELSIF who = 'worker' AND r.name IN ('credentials', 'user_recovery_codes', 'passkeys', 'consents') THEN
        colpriv := 'SELECT'; cols := ARRAY['user_id'];
      ELSIF who = 'worker' AND r.name = 'auth_challenges' THEN
        colpriv := 'SELECT'; cols := ARRAY['expires_at', 'consumed_at'];
      ELSIF who = 'worker' AND r.name = 'security_events' THEN
        colpriv := 'UPDATE'; cols := ARRAY['meta_enc'];
      ELSIF who = 'admin' AND r.name = 'user_mfa' THEN
        colpriv := 'SELECT'; cols := ARRAY['user_id', 'confirmed_at'];
      END IF;
      IF colpriv IS NOT NULL AND (
           SELECT count(*) FROM pg_attribute a
           WHERE a.attrelid = r.oid AND a.attname = ANY (cols) AND a.attnum > 0 AND NOT a.attisdropped
         ) = cardinality(cols) THEN
        EXECUTE format('GRANT %s (%s) ON %I TO %I', colpriv,
                       (SELECT string_agg(quote_ident(c), ', ') FROM unnest(cols) AS c), r.name, who);
      END IF;
    END LOOP;
  END LOOP;
END
$grants$;
