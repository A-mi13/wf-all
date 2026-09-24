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
GRANT USAGE ON SCHEMA public TO api, admin, worker;
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO api, admin, worker;
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO api, admin, worker;
