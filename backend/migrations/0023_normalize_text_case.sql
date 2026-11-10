-- 0023_normalize_text_case.sql
-- normalize_text без учёта регистра (решение пользователя 28.10.2026, вне плана B; находка (а) ревью G5).
--
-- Причина: в 0001 translate стоит до lower и переводит в кириллицу заглавные двойники ABEKMHOPCTXY, а из строчных —
-- только aeopcxyk. Строчные b h m t оставались латиницей, а заглавные B H M T уходили в в н м т: 'mikh' → 'miкh',
-- 'Mikhaylovsk' → 'мiкh…' — префикс в нижнем регистре не находил город (поиск geo «без учёта регистра»), а 'Tom'
-- и 'tom' были разными никами. Заодно translate до unaccent оставлял букву с диакритикой латиницей: 'Café' ≠ 'Cafe'.
--
-- Новая функция: снять диакритику, нижний регистр, затем все 12 строчных латинских двойников — в кириллицу. Регистр
-- не влияет по построению; клоны латиницей («MOCKBA», «BMW» с кириллическими В и М) совпадают, как и раньше.
-- immutable_unaccent — со схемой: с PG17 maintenance-операции и pg_restore идут с узким search_path.
--
-- Связь функций: новое(x) = translate(старое(x), 'abcehkmoptxy', 'авсенкмортху') для любого x (двойники из таблицы
-- 0001 уже кириллица, остальное в старом — lower(unaccent(x))). На этом держатся:
--   - пересчёт хранимых значений без исходного текста (reserved_nicknames, nickname_reservations) — тем же translate;
--   - проверка стенда до наката (docs/deploy-dev.md, «До мержа миграции в main») — тем же translate по старым
--     значениям: строки, которые совпадут, считаются заранее;
--   - откат не создаёт конфликтов: старое(x) = старое(y) ⇒ новое(x) = новое(y).
--
-- Генерируемая STORED-колонка при смене функции сама не пересчитывается: ALTER COLUMN … SET EXPRESSION (есть в
-- PG17, sqlc его разбирает) переписывает таблицу и перестраивает её индексы, триггеры (updated_at) не срабатывают.
-- Если после пересчёта совпадут строки уникального индекса (users_nickname_key, teams_city_name_key,
-- districts_city_name_active_key), перестройка индекса падает 23505 «could not create unique index "<имя>"» с ключом
-- в DETAIL, и вся миграция откатывается (одна транзакция goose): версия остаётся 22, функция — прежней.

-- +goose Up

-- Замки (ревью N1 m5): все семь таблиц — одним оператором в начале, в порядке импорта GeoNames (geonames_* первыми:
-- импорт пишет их одной долгой транзакцией, затем читает cities). Пока накат ждёт импорт, остальные таблицы им не
-- заперты — публичные чтения городов и игроков идут. Ждать дольше lock_timeout накат не станет: 55P03, откат целиком
-- (версия 22), деплой повторить после импорта (docs/deploy-dev.md). 5 с — с запасом длиннее обычной транзакции
-- запроса (доли секунды), но короче импорта (минуты); очередь ACCESS EXCLUSIVE держит читателей таблицы не дольше
-- этого срока. Срок действует на каждое ожидание замка отдельно.
SET LOCAL lock_timeout = '5s';
LOCK TABLE geonames_places, geonames_place_names, cities, city_names, districts, users, teams IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_text(input text)
RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT regexp_replace(
           translate(
             lower(public.immutable_unaccent(btrim(input))),
             -- строчные латинские буквы, неотличимые от кириллических (после lower — все двойники)
             'abcehkmoptxy',
             'авсенкмортху'
           ),
           '[^a-zа-яё0-9]+', '', 'g'
         )
$$;
-- +goose StatementEnd

ALTER TABLE cities ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE city_names ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE districts ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE users ALTER COLUMN nickname_normalized SET EXPRESSION AS (normalize_text(nickname));
ALTER TABLE teams ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE geonames_places ALTER COLUMN ascii_name_normalized SET EXPRESSION AS (normalize_text(ascii_name));
ALTER TABLE geonames_place_names ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));

-- Хранимые нормализованные ники (исходного текста нет): новая форма — translate старой, добавляется рядом с прежней
-- строкой (ревью N1 m1, m2). Прежние строки не удаляются и не меняются: новая функция их не достигает (безвредны,
-- карантин истечёт сам), а откат точен — previous_user_id, released_at и reason каждой строки целы. Совпавшие формы
-- дают одну новую строку: у зарезервированного — данные первой по значению, у карантина — брони с поздним сроком;
-- уже существующая строка новой формы не меняется (ON CONFLICT DO NOTHING). Повторный накат ничего не добавляет.
INSERT INTO reserved_nicknames (nickname_normalized, reason)
SELECT DISTINCT ON (n) n, reason
  FROM (SELECT translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху') AS n, nickname_normalized AS old, reason
          FROM reserved_nicknames) AS s
 WHERE n <> old
 ORDER BY n, old
ON CONFLICT (nickname_normalized) DO NOTHING;

INSERT INTO nickname_reservations (nickname_normalized, previous_user_id, released_at, expires_at)
SELECT DISTINCT ON (n) n, previous_user_id, released_at, expires_at
  FROM (SELECT translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху') AS n, nickname_normalized AS old,
               previous_user_id, released_at, expires_at
          FROM nickname_reservations) AS s
 WHERE n <> old
 ORDER BY n, expires_at DESC, old
ON CONFLICT (nickname_normalized) DO NOTHING;

-- +goose Down

-- Замки — как у наката и по той же причине (ревью уборки m1): откат во время импорта GeoNames ждёт geonames_*, не
-- запирая остальные таблицы, и по lock_timeout падает 55P03 целиком, а не держит cities до конца импорта.
SET LOCAL lock_timeout = '5s';
LOCK TABLE geonames_places, geonames_place_names, cities, city_names, districts, users, teams IN ACCESS EXCLUSIVE MODE;

-- прежняя функция из 0001 дословно
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION normalize_text(input text)
RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT regexp_replace(
           lower(
             immutable_unaccent(
               translate(
                 btrim(input),
                 -- латиница, неотличимая от кириллицы, в обоих регистрах
                 'ABEKMHOPCTXYaeopcxyk',
                 'АВЕКМНОРСТХУаеорсхук'
               )
             )
           ),
           '[^a-zа-яё0-9]+', '', 'g'
         )
$$;
-- +goose StatementEnd

-- конфликтов уникальных индексов откат не создаёт: прежняя функция различает не меньше новой
ALTER TABLE cities ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE city_names ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE districts ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE users ALTER COLUMN nickname_normalized SET EXPRESSION AS (normalize_text(nickname));
ALTER TABLE teams ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));
ALTER TABLE geonames_places ALTER COLUMN ascii_name_normalized SET EXPRESSION AS (normalize_text(ascii_name));
ALTER TABLE geonames_place_names ALTER COLUMN name_normalized SET EXPRESSION AS (normalize_text(name));

-- Хранимые ники откат не трогает: накат прежних строк не менял (они и есть прежняя форма), а добавленные им
-- новые формы — тоже ники в прежней форме (из заглавных двойников) и неотличимы от прежних строк той же формы.
