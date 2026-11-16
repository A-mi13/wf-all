-- Якорь моделей identitydb (план I-2, R5): по запросу на каждую таблицу identity, чтобы models.go содержал модели
-- всех таблиц с Task 6 и не менялся в задачах сценариев (omit_unused_structs оставил бы только используемые).
-- Запросы не вызываются; новая таблица identity — строка здесь.

-- name: AnchorAuthChallenges :many
SELECT * FROM auth_challenges LIMIT 0;

-- name: AnchorAuthCodes :many
SELECT * FROM auth_codes LIMIT 0;

-- name: AnchorCaptainApplications :many
SELECT * FROM captain_applications LIMIT 0;

-- name: AnchorConsents :many
SELECT * FROM consents LIMIT 0;

-- name: AnchorCredentials :many
SELECT * FROM credentials LIMIT 0;

-- name: AnchorDeviceAccounts :many
SELECT * FROM device_accounts LIMIT 0;

-- name: AnchorDevices :many
SELECT * FROM devices LIMIT 0;

-- name: AnchorMfaResets :many
SELECT * FROM mfa_resets LIMIT 0;

-- name: AnchorNicknameChanges :many
SELECT * FROM nickname_changes LIMIT 0;

-- name: AnchorNicknameReservations :many
SELECT * FROM nickname_reservations LIMIT 0;

-- name: AnchorPasskeys :many
SELECT * FROM passkeys LIMIT 0;

-- name: AnchorRefreshTokens :many
SELECT * FROM refresh_tokens LIMIT 0;

-- name: AnchorReservedNicknames :many
SELECT * FROM reserved_nicknames LIMIT 0;

-- name: AnchorRoleAssignments :many
SELECT * FROM role_assignments LIMIT 0;

-- name: AnchorSecurityEvents :many
SELECT * FROM security_events LIMIT 0;

-- name: AnchorSessions :many
SELECT * FROM sessions LIMIT 0;

-- name: AnchorUserIdentities :many
SELECT * FROM user_identities LIMIT 0;

-- name: AnchorUserMfa :many
SELECT * FROM user_mfa LIMIT 0;

-- name: AnchorUserRecoveryCodes :many
SELECT * FROM user_recovery_codes LIMIT 0;

-- name: AnchorUserRestrictions :many
SELECT * FROM user_restrictions LIMIT 0;

-- name: AnchorUsers :many
SELECT * FROM users LIMIT 0;
