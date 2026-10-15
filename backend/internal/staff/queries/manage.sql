-- Управление сотрудниками (спека сотрудников §2.6, §4.9, D16, D17): список и карточка, роль и города,
-- выключение, сброс доступа, страж последнего владельца, исполнители подтверждений staff.*.
-- Порядок замков: pg_advisory_xact_lock(staff_owners) (где возможен страж последнего владельца) →
-- учётки по возрастанию id (цель — FOR UPDATE, автор, инициатор и подтверждающий — FOR SHARE) →
-- сессии → staff_mfa → резервные коды → приглашения.

-- name: ManageOwnersLock :exec
-- Страж последнего владельца (D17): все пути снятия, выключения, сброса и понижения активного
-- владельца сериализуются этим замком до коммита; счёт владельцев — следующим оператором, уже после
-- замка (READ COMMITTED: свежий снимок видит коммит соседа).
SELECT pg_advisory_xact_lock(sqlc.arg(key)::bigint);

-- name: ManageCountOtherActiveOwners :one
-- Активные владельцы, кроме staff_id.
SELECT count(*)
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.status = 'active' AND r.role = 'owner' AND a.id <> sqlc.arg(staff_id)::uuid;

-- name: ManageLockAccount :one
-- Цель управления под замком (выключение, сброс, роль, сессии, приглашение).
SELECT a.id, a.status::text AS status, a.display_name, a.email::text AS email, a.locked_until, a.version,
       r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id)::uuid
FOR UPDATE OF a;

-- name: ManageShareAccount :one
-- Автор, инициатор или подтверждающий под разделяемым замком: пока действие идёт, его нельзя
-- выключить, сбросить или сменить ему роль (M-4, m4).
SELECT a.id, a.status::text AS status, a.display_name, a.email::text AS email, a.locked_until, a.version,
       r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id)::uuid
FOR SHARE OF a;

-- name: ManageAccountState :one
-- Инициатор запроса без замка (предварительная проверка инициатора; под замком её повторяет
-- исполнитель).
SELECT a.status::text AS status, a.locked_until, a.version, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id)::uuid;

-- name: ManageSessionState :one
-- Сессия автора или подтверждающего: жива ли (учётка уже под замком — гашение сессий идёт под ним же).
SELECT staff_id, stage::text AS stage, expires_at, revoked_at
FROM staff_sessions
WHERE id = sqlc.arg(id)::uuid;

-- name: ManageFindByEmail :one
-- Учётка по нормализованной почте без замка (приглашение: занята ли почта, выключенная — вернуть её).
SELECT a.id, a.status::text AS status, a.version
FROM staff_accounts a
WHERE a.email = sqlc.arg(email)::citext;

-- name: ManageKnownCities :one
-- Сколько городов из ids существует. Города — справочник geo: только экспортированное представление
-- (спека бэкенда §4.2).
SELECT count(DISTINCT city_id)::int FROM geo_read_city_settings WHERE city_id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ManageCitiesOf :many
-- Города модераторов ids.
SELECT staff_id, city_id
FROM staff_role_cities
WHERE staff_id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY staff_id, city_id;

-- name: ManageLiveInvites :many
-- Живые приглашения учёток ids: назначение, срок и выпуск (у живого код есть всегда).
SELECT staff_id, purpose::text AS purpose, expires_at, created_at
FROM staff_invites
WHERE staff_id = ANY(sqlc.arg(ids)::uuid[]) AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: ManageListAccounts :many
-- Страница «Роли и доступ»: новые сверху, keyset (created_at, id). q — начало почты или имени без учёта
-- регистра (шаблон LIKE с экранированными % и _ строит сценарий).
SELECT a.id, a.email::text AS email, a.display_name, a.locale, a.status::text AS status, a.last_login_at,
       a.locked_until, a.created_at, a.version, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE (sqlc.narg(status)::text IS NULL OR a.status::text = sqlc.narg(status)::text)
  AND (sqlc.narg(role)::text IS NULL OR r.role::text = sqlc.narg(role)::text)
  AND (sqlc.narg(q)::text IS NULL
       OR a.email::text ILIKE sqlc.narg(q)::text ESCAPE '\'
       OR a.display_name ILIKE sqlc.narg(q)::text ESCAPE '\')
  AND (NOT sqlc.arg(has_cursor)::bool
       OR (a.created_at, a.id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(lim);

-- name: ManageGetAccount :one
-- Карточка сотрудника.
SELECT a.id, a.email::text AS email, a.display_name, a.locale, a.status::text AS status, a.last_login_at,
       a.locked_until, a.created_at, a.version, r.role::text AS role
FROM staff_accounts a
JOIN staff_roles r ON r.staff_id = a.id
WHERE a.id = sqlc.arg(id)::uuid;

-- name: ManageSetRole :exec
-- Новая роль (granted_by — кто выдал; NULL — оператор).
UPDATE staff_roles
SET role = (sqlc.arg(role)::text)::staff_role, granted_by = sqlc.narg(granted_by), granted_at = sqlc.arg(now)
WHERE staff_id = sqlc.arg(staff_id)::uuid;

-- name: ManageDeleteCities :exec
-- Все города сотрудника (перед записью нового списка; отложенный триггер проверит итог на коммите).
DELETE FROM staff_role_cities WHERE staff_id = sqlc.arg(staff_id)::uuid;

-- name: ManageBumpVersion :one
-- Смена роли или городов: версия агрегата растёт (сверка подтверждений, D17).
UPDATE staff_accounts SET version = version + 1 WHERE id = sqlc.arg(id)::uuid RETURNING version;

-- name: ManageDisable :one
-- Выключение (снять роль): вход закрыт, версия растёт.
UPDATE staff_accounts SET status = 'disabled', version = version + 1
WHERE id = sqlc.arg(id)::uuid
RETURNING version;

-- name: ManageResetAccess :one
-- Сброс доступа: пароля нет, блокировка второго фактора снята, статус invited, версия растёт;
-- display_name задан — новое имя (повторное приглашение выключенной учётки).
UPDATE staff_accounts
SET status = 'invited', password_hash = NULL, mfa_failures = 0, locked_until = NULL,
    display_name = coalesce(sqlc.narg(display_name)::text, display_name),
    version = version + 1
WHERE id = sqlc.arg(id)::uuid
RETURNING version;

-- name: ManageAccountMail :one
-- Письмо staff.admin_disabled: кого выключили.
SELECT display_name, email::text AS email FROM staff_accounts WHERE id = sqlc.arg(id)::uuid;
