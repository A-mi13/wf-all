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

-- Хранимые нормализованные ники (исходного текста нет): новое значение — translate старого. Значения, совпавшие после
-- пересчёта, схлопываются в одно до UPDATE (иначе PK): зарезервированный — любой из совпавших, карантин — с поздним
-- сроком. После схлопывания UPDATE конфликтов не даёт: translate идемпотентен, и строка, уже хранящая чьё-то новое
-- значение, совпала бы с ней и была удалена.
DELETE FROM reserved_nicknames r
 USING reserved_nicknames o
 WHERE translate(r.nickname_normalized, 'abcehkmoptxy', 'авсенкмортху')
     = translate(o.nickname_normalized, 'abcehkmoptxy', 'авсенкмортху')
   AND r.nickname_normalized > o.nickname_normalized;

UPDATE reserved_nicknames
   SET nickname_normalized = translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху')
 WHERE nickname_normalized <> translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху');

DELETE FROM nickname_reservations r
 USING nickname_reservations o
 WHERE translate(r.nickname_normalized, 'abcehkmoptxy', 'авсенкмортху')
     = translate(o.nickname_normalized, 'abcehkmoptxy', 'авсенкмортху')
   AND (o.expires_at, o.nickname_normalized) > (r.expires_at, r.nickname_normalized);

UPDATE nickname_reservations
   SET nickname_normalized = translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху')
 WHERE nickname_normalized <> translate(nickname_normalized, 'abcehkmoptxy', 'авсенкмортху');

-- +goose Down

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

-- Хранимые ники обратно не переводятся: исходного текста нет, а translate необратим. Новые значения остаются (это
-- тоже ники в прежней форме — из заглавных двойников), зарезервированные ники сида 0010 возвращаются в прежней форме.
INSERT INTO reserved_nicknames (nickname_normalized, reason)
SELECT normalize_text(v), 'system'
  FROM (VALUES
    ('admin'), ('administrator'), ('root'), ('support'), ('help'),
    ('moderator'), ('moder'), ('staff'), ('team'), ('official'),
    ('system'), ('bot'), ('api'), ('null'), ('undefined'),
    ('me'), ('you'), ('everyone'), ('all'),
    ('админ'), ('администратор'), ('поддержка'), ('модератор'),
    ('система'), ('бот'), ('все')
  ) AS t(v)
ON CONFLICT DO NOTHING;
