-- Удаление аккаунта, отмена, анонимизация (спека identity §3.11; план I-2, Task 15). Порядок замков R30: users первым
-- (lockUser или SKIP LOCKED воркера) → sessions → mfa_resets → auth_codes. Имена — с префиксом Deletion.

-- name: DeletionSchedule :one
-- прежний статус (active | suspended — CHECK users_status_before_deletion) — отмена возвращает его
UPDATE users
SET status = 'pending_deletion', status_before_deletion = status, deletion_requested_at = @now, purge_at = @purge_at,
    version = version + 1
WHERE id = @id
RETURNING version;

-- name: DeletionWithdrawApplications :many
-- открытая заявка в капитаны — withdrawn (допуски не трогаются до анонимизации); событие на каждую (R24)
UPDATE captain_applications SET status = 'withdrawn', closed_at = @now, version = version + 1
WHERE user_id = @user_id AND status = 'pending'
RETURNING id, city_id, version;

-- name: DeletionCancelMfaResets :many
-- живой сброс 2FA поддержкой — отменён системой (m4)
UPDATE mfa_resets SET cancelled_at = @now, cancelled_by = 'system'
WHERE user_id = @user_id AND executed_at IS NULL AND cancelled_at IS NULL
RETURNING id, execute_at;

-- name: DeletionScrubMfaResets :exec
-- анонимизация (решение пользователя 15.11): причина и тикет сброса 2FA — свободный текст сотрудника, может называть
-- игрока; reason NOT NULL с CHECK 3–500 — заглушка, ticket — NULL. Все сбросы аккаунта, и прошлые
UPDATE mfa_resets SET reason = @reason_erased, ticket = NULL WHERE user_id = @user_id;

-- name: DeletionRevokeCodes :exec
-- живые коды аккаунта (смена почты, сброс пароля) — отозваны, код для письма стирается
UPDATE auth_codes SET revoked_at = @now, code_enc = NULL, payload_enc = NULL
WHERE user_id = @user_id AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: DeletionRestore :one
-- прежний статус (решение пользователя 15.11): suspended → удаление → отмена = suspended
UPDATE users
SET status = status_before_deletion, status_before_deletion = NULL, deletion_requested_at = NULL, purge_at = NULL,
    version = version + 1
WHERE id = @id
RETURNING version;

-- name: DeletionRevokeRestoreTokens :exec
-- m3: живые restore_token прошлых периодов гаснут при новом запросе удаления (auth_challenges — после users, R30)
UPDATE auth_challenges SET consumed_at = @now
WHERE user_id = @user_id AND purpose = 'deletion_restore' AND consumed_at IS NULL;

-- name: DeletionDue :many
-- аккаунты с наступившим сроком: строка под замком, занятую (отмена удаления в процессе) пропустить; skip — упавшие за
-- этот прогон воркера (m8: иначе пачка из одних отравленных строк, старших по purge_at, заслоняла бы остальные навсегда)
SELECT id, email, nickname_normalized FROM users
WHERE status = 'pending_deletion' AND purge_at <= @now
  AND NOT (id = ANY(coalesce(sqlc.narg(skip)::uuid[], '{}')))
ORDER BY purge_at, id
LIMIT sqlc.arg(batch)::int
FOR NO KEY UPDATE SKIP LOCKED;

-- name: DeletionAnonymizeUser :one
-- все ПД аккаунта (решение пользователя 15.11): заглушки почты (deleted+<id>@invalid, R24) и ника
-- (domain.DeletedNickname); телефон, имя, дата рождения (CHECK users_birth_date_required — NULL только у deleted),
-- город, игровые поля профиля (умолчания), часовой пояс, user handle WebAuthn, время входа. Остаются: id, locale
-- (язык письма account_deleted), avatar_item_id (предмет косметики economy, не ПД), email_verified_at, времена
-- создания и удаления, version
UPDATE users
SET status = 'deleted', deleted_at = @now, email = @email, nickname = @nickname, phone = NULL,
    phone_verified_at = NULL, display_name = NULL, birth_date = NULL, city_id = NULL, primary_position = 'ANY',
    secondary_position = NULL, claimed_skill = 'amateur', confirmed_skill = NULL, preferred_foot = NULL,
    height_cm = NULL, shirt_number = NULL, favourite_club = NULL, motto = NULL, timezone = NULL,
    webauthn_handle = NULL, last_login_at = NULL, status_before_deletion = NULL, deletion_requested_at = NULL,
    purge_at = NULL, version = version + 1
WHERE id = @id
RETURNING version;

-- name: DeletionScrubApplications :exec
-- поля формы заявок в капитаны — свободный текст и контакты игрока; комментарий модератора тоже может называть
-- игрока: у отказа other он обязателен (CHECK captain_applications_other_comment) — прочерк
UPDATE captain_applications
SET phone = NULL, note = NULL, contact_link = NULL, proposed_format = NULL, proposed_pitch = NULL,
    play_frequency = NULL, comment = CASE WHEN rejection_code = 'other' THEN '-' END, version = version + 1
WHERE user_id = @user_id;

-- name: DeletionDeleteNicknameChanges :exec
-- I3: история ника связала бы надгробие с прежними никами навсегда
DELETE FROM nickname_changes WHERE user_id = $1;

-- name: DeletionDeleteSessions :exec
-- IP, город по IP, UA и версия приложения входов; refresh_tokens и вызовы сессий — каскадом
DELETE FROM sessions WHERE user_id = $1;

-- name: DeletionDeleteSecurityEvents :exec
-- журнал безопасности (IP, UA, адреса в meta_enc), кроме записи анонимизации: её meta_enc — адрес письма
-- account_deleted (стирает чистка после письма)
DELETE FROM security_events WHERE user_id = @user_id AND id <> @keep;

-- name: DeletionDropReservation :exec
DELETE FROM nickname_reservations WHERE nickname_normalized = @nickname_normalized;

-- name: DeletionReserveNickname :exec
-- у worker нет UPDATE на nickname_reservations (§5.5): прежняя строка удаляется DeletionDropReservation
INSERT INTO nickname_reservations (nickname_normalized, previous_user_id, released_at, expires_at)
VALUES (@nickname_normalized, @user_id, @now, @expires_at);

-- name: DeletionDeleteCredentials :exec
DELETE FROM credentials WHERE user_id = $1;

-- name: DeletionDeleteMfa :exec
DELETE FROM user_mfa WHERE user_id = $1;

-- name: DeletionDeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes WHERE user_id = $1;

-- name: DeletionDeletePasskeys :exec
DELETE FROM passkeys WHERE user_id = $1;

-- name: DeletionDeleteIdentities :exec
DELETE FROM user_identities WHERE user_id = $1;

-- name: DeletionDeleteConsents :exec
DELETE FROM consents WHERE user_id = $1;

-- name: DeletionDeleteCodes :exec
-- коды аккаунта и коды на его почту (приманки регистрации) хранят адрес открыто
DELETE FROM auth_codes WHERE user_id = @user_id OR email = @email;
