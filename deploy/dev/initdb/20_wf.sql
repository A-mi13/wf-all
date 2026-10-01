-- Роли по бинарникам: ошибки прав всплывают в dev, а не на проде.
-- Точные права — в спеке бэкенда; здесь только каркас.
CREATE ROLE migrator LOGIN PASSWORD 'migrator';
CREATE ROLE api      LOGIN PASSWORD 'api';
CREATE ROLE admin    LOGIN PASSWORD 'admin';
CREATE ROLE worker   LOGIN PASSWORD 'worker';

-- PostGIS не «доверенное» расширение: создаёт только суперпользователь.
-- Ставим в template1 — его наследуют wf и все тестовые клоны,
-- а CREATE EXTENSION IF NOT EXISTS в миграциях становится no-op.
\connect template1
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS postgis;

\connect postgres
CREATE DATABASE wf OWNER migrator;

\connect wf
-- Права на таблицы выдаёт cmd/migrate после каждого наката (backend/internal/platform/grants):
-- широких умолчаний здесь нет, иначе каждая новая таблица до прогона grants была бы открыта
-- всем ролям (спека бэкенда §10.1).
