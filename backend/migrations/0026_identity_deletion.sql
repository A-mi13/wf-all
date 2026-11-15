-- 0026_identity_deletion.sql
-- identity, удаление аккаунта (спека docs/superpowers/specs/2026-11-13-identity-design.md §3.11; решения пользователя
-- 15.11.2026, раунд 1 волны 4 плана I-2):
--   * users.status_before_deletion — статус до запроса удаления (active | suspended): отмена удаления возвращает его,
--     а не active (иначе удаление с отменой «отмывало» бы suspended); задан ⇔ status = 'pending_deletion';
--   * users.birth_date — NULL допустим только у удалённого: анонимизация затирает все ПД, включая дату рождения;
--   * удалённые до 0026 аккаунты затираются так же, как анонимизация (app.anonymize): иначе их ПД остались бы бессрочно.

-- +goose Up

ALTER TABLE users ADD COLUMN status_before_deletion user_status, ALTER COLUMN birth_date DROP NOT NULL;

-- до 0026 отмена всегда возвращала active — строки периода удаления получают его
UPDATE users SET status_before_deletion = 'active' WHERE status = 'pending_deletion';

-- удалённые до 0026: ПД users и формы заявок в капитаны — как у анонимизации (список колонок — там же)
UPDATE users
SET birth_date = NULL, display_name = NULL, city_id = NULL, primary_position = 'ANY', secondary_position = NULL,
    claimed_skill = 'amateur', confirmed_skill = NULL, preferred_foot = NULL, height_cm = NULL, shirt_number = NULL,
    favourite_club = NULL, motto = NULL, timezone = NULL, webauthn_handle = NULL, last_login_at = NULL,
    phone = NULL, phone_verified_at = NULL
WHERE status = 'deleted';

UPDATE captain_applications a
SET phone = NULL, note = NULL, contact_link = NULL, proposed_format = NULL, proposed_pitch = NULL,
    play_frequency = NULL, comment = CASE WHEN a.rejection_code = 'other' THEN '-' END
FROM users u
WHERE u.id = a.user_id AND u.status = 'deleted';

ALTER TABLE users
    -- имя сортируется после users_deletion_pending: строка, нарушающая оба, называет прежний CHECK (порядок проверки
    -- CHECK в PostgreSQL — по имени)
    ADD CONSTRAINT users_status_before_deletion CHECK (
        (status = 'pending_deletion') = (status_before_deletion IS NOT NULL)
        AND (status_before_deletion IS NULL OR status_before_deletion IN ('active', 'suspended'))),
    ADD CONSTRAINT users_birth_date_required CHECK (birth_date IS NOT NULL OR status = 'deleted');

-- +goose Down

-- Возвращает схему 0025 в точности. Прежний статус строк периода удаления теряется (отмена после отката вернёт
-- active). Дату рождения затёртым аккаунтам не вернуть — понятный отказ вместо голого 23502.
-- +goose StatementBegin
DO $$
DECLARE
  n bigint;
BEGIN
  SELECT count(*) INTO n FROM users WHERE birth_date IS NULL;
  IF n > 0 THEN
    RAISE EXCEPTION 'users: строк без даты рождения — % (анонимизированные аккаунты; откат 0026 возвращает NOT NULL, а дату затёртым не вернуть): удалите эти строки или оставьте 0026 и повторите migrate down', n;
  END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE users
    DROP CONSTRAINT users_birth_date_required,
    DROP CONSTRAINT users_status_before_deletion,
    ALTER COLUMN birth_date SET NOT NULL,
    DROP COLUMN status_before_deletion;
