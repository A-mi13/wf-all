-- Сборка писем I-2 воркером (спека identity §8.2; план I-2, Task 19): только чтение под ролью worker (§5.5). Адрес и
-- язык — по ref задачи: auth_codes.id, security_events.id или mfa_resets.id; адресов в аргументах задач нет.

-- name: MailCode :one
SELECT id, purpose, email, user_id, decoy, code_enc, payload_enc, key_id, expires_at, consumed_at, revoked_at
FROM auth_codes WHERE id = @id;

-- name: MailUserByID :one
SELECT email, locale, status FROM users WHERE id = @id;

-- name: MailUserByEmail :one
SELECT locale, status FROM users WHERE email = @email;

-- Пояс времени в письме: users.timezone, затем пояс города аккаунта (экспортированное представление geo, спека §4.2), затем
-- UTC — выбирает Composer (решение пользователя 16.11).

-- name: MailSecurityEvent :one
SELECT e.kind, e.meta, e.meta_enc, e.key_id, e.created_at, u.email, u.locale, u.status,
       u.timezone, c.timezone AS city_timezone
FROM security_events e
JOIN users u ON u.id = e.user_id
LEFT JOIN geo_read_city_settings c ON c.city_id = u.city_id
WHERE e.id = @id;

-- name: MailCaptainApplication :one
-- Письмо решения по заявке (I-4, Task 4): статус и решение заявки, адрес, язык, ник и статус заявителя, название
-- города заявки (geo_read_city_settings.city_name — название на языке страны города: переводов city_names
-- экспортированное представление geo не отдаёт). permit_live — живой допуск по заявке: без него письма «вы капитан»
-- нет (допуск отозван до сборки письма; решение пользователя 17.11).
SELECT a.status, a.rejection_code, a.comment, u.email, u.locale, u.nickname, u.status AS user_status,
       c.city_name,
       EXISTS (SELECT 1 FROM role_assignments r WHERE r.application_id = a.id AND r.revoked_at IS NULL) AS permit_live
FROM captain_applications a
JOIN users u ON u.id = a.user_id
LEFT JOIN geo_read_city_settings c ON c.city_id = a.city_id
WHERE a.id = @id;

-- name: MailCaptainWithdrawnCity :one
-- Письмо об отзыве заявки сменой города (I-4, Task 7; R39): статус заявки, адрес, язык, ник и статус игрока, названия
-- прежнего города (город заявки) и нового (текущий город игрока) — на языке страны города.
SELECT a.status, u.email, u.locale, u.nickname, u.status AS user_status, fc.city_name AS from_city,
       tc.city_name AS to_city
FROM captain_applications a
JOIN users u ON u.id = a.user_id
LEFT JOIN geo_read_city_settings fc ON fc.city_id = a.city_id
LEFT JOIN geo_read_city_settings tc ON tc.city_id = u.city_id
WHERE a.id = @id;

-- name: MailMfaReset :one
-- has_mfa — есть ли подтверждённая 2FA (у worker — SELECT user_id, confirmed_at, §5.5; как в исполнении сброса):
-- неподтверждённая настройка SetupTotp — не 2FA; отмена из DisableTotp снимает 2FA в той же tx — письму об отмене
-- нельзя говорить «защита продолжает действовать» (I1 ревью 4, добор F п. 1)
SELECT r.execute_at, r.executed_at, r.cancelled_at, r.cancelled_by, u.email, u.locale, u.status,
       u.timezone, c.timezone AS city_timezone,
       EXISTS (SELECT 1 FROM user_mfa m WHERE m.user_id = r.user_id AND m.confirmed_at IS NOT NULL) AS has_mfa
FROM mfa_resets r
JOIN users u ON u.id = r.user_id
LEFT JOIN geo_read_city_settings c ON c.city_id = u.city_id
WHERE r.id = @id;
