-- Приглашения и оператор (спека сотрудников §4.2–4.4, §4.11). Код приглашения — только шифротекст
-- (keys.Ring, aad = id приглашения). Сценарии с приглашением сначала берут замок учётки
-- (LockAccount / LockAccountByEmail), затем строку приглашения: учётка сериализует принятие,
-- recovery-codes/ack, повторные отправки, выпуск нового приглашения и перешифровку.

-- name: LockAccountByEmail :one
-- Учётка по нормализованной почте под замком: принятие приглашения, повторная отправка, оператор.
SELECT a.id, a.status::text AS status, a.display_name, a.version, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.email = sqlc.arg(email)::citext
FOR UPDATE OF a;

-- name: GetInviteCandidate :one
-- Учётка по почте и её живое приглашение БЕЗ замков: повторной отправке и принятию нечего делать
-- (учётка не invited, приглашения нет, срок, пауза, лимит, кода нет) — ответ без замков и записей,
-- время ответа не выдаёт существование почты. Шифротекст кода — для сверки без замков: неверный код
-- пишет только аудит неудачи. Подходящему кандидату (и верному коду) сценарий берёт замки и
-- проверяет заново.
SELECT a.id, a.status::text AS status, i.id AS invite_id, i.expires_at, i.last_sent_at, i.sends,
       i.code_enc, i.code_key_id
FROM staff_accounts a
LEFT JOIN staff_invites i ON i.staff_id = a.id AND i.consumed_at IS NULL AND i.revoked_at IS NULL
WHERE a.email = sqlc.arg(email)::citext;

-- name: LockLiveInvite :one
-- Живое (не погашенное и не принятое-и-закрытое) приглашение учётки под замком; срок проверяет
-- сценарий.
SELECT id, purpose::text AS purpose, code_enc, code_key_id, expires_at, sends, last_sent_at
FROM staff_invites
WHERE staff_id = sqlc.arg(staff_id) AND consumed_at IS NULL AND revoked_at IS NULL
FOR UPDATE;

-- name: GetLastInvite :one
-- Последнее приглашение учётки (живое или закрытое): назначение и время выпуска — повторная отправка
-- админом и оператором берёт назначение отсюда, пауза админа считается от выпуска (created_at), а не от
-- last_sent_at: его двигают анонимные самоотправки, и чужое нажатие не должно блокировать админа.
SELECT purpose::text AS purpose, created_at
FROM staff_invites
WHERE staff_id = sqlc.arg(staff_id)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: RevokeLiveInvite :execrows
-- Гашение живого приглашения учётки перед выпуском нового: код стирается (CHECK
-- staff_invites_closed_no_code).
UPDATE staff_invites
SET revoked_at = sqlc.arg(now)::timestamptz, code_enc = NULL, code_key_id = NULL
WHERE staff_id = sqlc.arg(staff_id) AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: InsertInvite :exec
INSERT INTO staff_invites (id, staff_id, purpose, code_enc, code_key_id, expires_at, sends, last_sent_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(staff_id), sqlc.arg(purpose)::staff_invite_purpose, sqlc.arg(code_enc),
        sqlc.arg(code_key_id)::text, sqlc.arg(expires_at), 1, sqlc.arg(now), sqlc.arg(now));

-- name: MarkInviteResent :one
-- Повторная отправка самим (§4.3 п.3): тот же код (строка не перешифровывается — решение
-- пользователя 14.10.2026), отправка +1, срок не меняется.
UPDATE staff_invites
SET sends = sends + 1, last_sent_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id)
RETURNING sends;

-- name: ConsumeInvite :exec
-- recovery-codes/ack: приглашение погашено, код стирается.
UPDATE staff_invites
SET consumed_at = sqlc.arg(now)::timestamptz, code_enc = NULL, code_key_id = NULL
WHERE id = sqlc.arg(id);

-- name: AcceptInvitePassword :exec
-- Принятие приглашения: новый пароль; прежняя блокировка второго фактора снимается — второй фактор
-- учётки настраивается заново.
UPDATE staff_accounts
SET password_hash = sqlc.arg(password_hash), mfa_failures = 0, locked_until = NULL
WHERE id = sqlc.arg(id);

-- name: ActivateAccount :exec
-- recovery-codes/ack: учётка активна, вход — сейчас.
UPDATE staff_accounts
SET status = 'active', mfa_failures = 0, last_login_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id);

-- name: GetLiveSessionByToken :one
-- Сессия старой cookie при принятии приглашения: живая (не погашена, срок не истёк), чья угодно.
SELECT id
FROM staff_sessions
WHERE token_hash = sqlc.arg(token_hash) AND revoked_at IS NULL AND expires_at > sqlc.arg(now)::timestamptz;

-- name: GetInviteMail :one
-- Письмо staff.invite / staff.access_reset: приглашение и его учётка.
SELECT i.staff_id, i.purpose::text AS purpose, i.code_enc, i.code_key_id, i.expires_at, i.consumed_at, i.revoked_at,
       a.email::text AS email, a.display_name, a.locale, a.status::text AS status
FROM staff_invites i
JOIN staff_accounts a ON a.id = i.staff_id
WHERE i.id = sqlc.arg(id);

-- name: GetBootstrappedMail :one
-- Письмо staff.bootstrapped: новая учётка, её роль и кто создал (NULL — оператор).
SELECT a.display_name, a.email::text AS email, a.created_at, a.created_by, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id);

-- name: GetActorLabel :one
-- Снимок имени сотрудника-автора для аудита.
SELECT display_name FROM staff_accounts WHERE id = sqlc.arg(id);

-- name: InsertInvitedAccount :one
-- Новая учётка в статусе invited; почта занята (любой статус) — нет строки.
INSERT INTO staff_accounts (id, email, display_name, status, created_at, created_by)
VALUES (sqlc.arg(id), sqlc.arg(email)::citext, sqlc.arg(display_name), 'invited', sqlc.arg(now), sqlc.narg(created_by))
ON CONFLICT ON CONSTRAINT staff_accounts_email_key DO NOTHING
RETURNING version;

-- name: InsertRole :exec
INSERT INTO staff_roles (staff_id, role, granted_by, granted_at)
VALUES (sqlc.arg(staff_id), (sqlc.arg(role)::text)::staff_role, sqlc.narg(granted_by), sqlc.arg(now));

-- name: InsertRoleCities :exec
-- Города модератора (отложенный триггер «модератор ⇔ хотя бы один город» проверит на коммите).
INSERT INTO staff_role_cities (staff_id, city_id)
SELECT sqlc.arg(staff_id), unnest(sqlc.arg(city_ids)::uuid[]);

-- name: ListActiveOwners :many
-- Кому письмо staff.bootstrapped: активные владельцы.
SELECT a.id
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.status = 'active' AND r.role = 'owner'
ORDER BY a.id;

-- name: ListMFAOnOldKeys :many
-- Перешифровка (§4.11): учётки с секретом TOTP не на текущем ключе.
SELECT staff_id FROM staff_mfa WHERE key_id <> sqlc.arg(current_key)::text ORDER BY staff_id;

-- name: RekeyMFA :exec
UPDATE staff_mfa SET secret_enc = sqlc.arg(secret_enc), key_id = sqlc.arg(key_id)::text
WHERE staff_id = sqlc.arg(staff_id);

-- name: ListInvitesOnOldKeys :many
-- Перешифровка: приглашения с кодом не на текущем ключе (код есть только у живых).
SELECT id, staff_id FROM staff_invites
WHERE code_enc IS NOT NULL AND code_key_id <> sqlc.arg(current_key)::text
ORDER BY id;

-- name: LockInviteCode :one
-- Код приглашения под замком (перешифровка после замка учётки).
SELECT code_enc, code_key_id FROM staff_invites WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: RekeyInvite :exec
UPDATE staff_invites SET code_enc = sqlc.arg(code_enc), code_key_id = sqlc.arg(code_key_id)::text
WHERE id = sqlc.arg(id);

-- name: CountRecoveryOnOldKeys :one
-- Неиспользованные резервные коды не на текущем ключе: перехэшировать нельзя (исходника нет), старый
-- ключ убирается, когда счёт = 0.
SELECT count(*) FROM staff_recovery_codes WHERE used_at IS NULL AND key_id <> sqlc.arg(current_key)::text;

-- name: CountMFAOnOldKeys :one
-- Итог перешифровки: секреты TOTP не на текущем ключе (нечитаемые и записанные старым ключом
-- экземпляром прежнего релиза при скользящем деплое).
SELECT count(*) FROM staff_mfa WHERE key_id <> sqlc.arg(current_key)::text;

-- name: CountInvitesOnOldKeys :one
-- Итог перешифровки: коды приглашений не на текущем ключе.
SELECT count(*) FROM staff_invites WHERE code_enc IS NOT NULL AND code_key_id <> sqlc.arg(current_key)::text;
