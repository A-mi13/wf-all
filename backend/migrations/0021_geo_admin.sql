-- 0021_geo_admin.sql
-- geo 2/2 в админке (спека docs/superpowers/specs/2026-10-06-staff-admin-geo2-design.md §3.2, §6.3 п.6;
-- план B, R12): оптимистичная версия флагов функций, строгий store_url версий приложения, форматы
-- правил страны. Код проверяет строже (действующие ISO 3166-1 и ISO 4217, поддерживаемые локали,
-- хост стора платформы, U+00A0 и U+0085 в store_url) — CHECK здесь защита в глубину. Права
-- (DELETE на city_slug_history у admin) — internal/platform/grants/grants.sql.
-- Старые строки, не проходящие новые CHECK, валят миграцию целиком (23514 с именем ограничения):
-- данные стенда сверяются до мержа (план B, Task 25).

-- +goose Up

-- версия флага: PATCH /v1/flags/{key} сверяет её (409 platform.flag_stale); сид получает 1
ALTER TABLE feature_flags
    ADD COLUMN version bigint NOT NULL DEFAULT 1 CONSTRAINT feature_flags_version_check CHECK (version > 0);

-- https:// в нижнем регистре и ни одного пробельного или управляющего символа во всей строке
ALTER TABLE app_versions
    DROP CONSTRAINT app_versions_store_url_check,
    ADD CONSTRAINT app_versions_store_url_check CHECK (store_url ~ '^https://[^[:space:][:cntrl:]]+$');

-- форматы правил страны (R12): ISO 3166-1 alpha-2, ISO 4217, язык ISO 639-1, префикс +1–4 цифры,
-- возрастные пороги 1–120 (порядок min ≤ majority — countries_age_order из 0002)
ALTER TABLE countries
    ADD CONSTRAINT countries_code_format CHECK (code ~ '^[A-Z]{2}$'),
    ADD CONSTRAINT countries_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    ADD CONSTRAINT countries_default_locale_format CHECK (default_locale ~ '^[a-z]{2}$'),
    ADD CONSTRAINT countries_phone_prefix_format CHECK (phone_prefix ~ '^\+[1-9][0-9]{0,3}$'),
    ADD CONSTRAINT countries_ages_range CHECK (min_signup_age BETWEEN 1 AND 120 AND age_of_majority BETWEEN 1 AND 120);

-- +goose Down

ALTER TABLE countries
    DROP CONSTRAINT countries_ages_range,
    DROP CONSTRAINT countries_phone_prefix_format,
    DROP CONSTRAINT countries_default_locale_format,
    DROP CONSTRAINT countries_currency_format,
    DROP CONSTRAINT countries_code_format;

-- прежний CHECK 0019 с прежним (автоматическим) именем
ALTER TABLE app_versions
    DROP CONSTRAINT app_versions_store_url_check,
    ADD CONSTRAINT app_versions_store_url_check CHECK (store_url ~ '^https://');

ALTER TABLE feature_flags DROP COLUMN version;
