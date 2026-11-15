-- Регистрация по почте и доступность ника (спека identity §3.1, §5.8; план I-2, Task 10).

-- name: NicknameState :one
-- длина после normalize_text (§5.8: не короче 3), резерв, занятость живым аккаунтом и живой карантин
SELECT length(normalize_text(@nickname::text))::int AS norm_len,
       EXISTS (SELECT 1 FROM reserved_nicknames r WHERE r.nickname_normalized = normalize_text(@nickname::text)) AS reserved,
       EXISTS (SELECT 1 FROM users u
               WHERE u.nickname_normalized = normalize_text(@nickname::text) AND u.deleted_at IS NULL) AS taken,
       EXISTS (SELECT 1 FROM nickname_reservations n
               WHERE n.nickname_normalized = normalize_text(@nickname::text) AND n.expires_at > sqlc.arg(now)::timestamptz) AS quarantined;

-- name: UserByEmail :one
-- живой аккаунт с этой почтой (анонимизированный носит заглушку deleted+<uuid>@invalid и сюда не попадает)
SELECT id, status FROM users WHERE email = @email AND deleted_at IS NULL;

-- name: DeviceSignups :one
-- регистрации устройства без замка (startSignup только читает, счётчик — при подтверждении)
SELECT signups FROM devices WHERE platform = @platform AND install_mac = ANY(@macs::bytea[])
ORDER BY signups DESC
LIMIT 1;

-- name: InsertUser :exec
INSERT INTO users (id, email, email_verified_at, nickname, birth_date, city_id, locale, version, created_at, updated_at)
VALUES (@id, @email, @now, @nickname, @birth_date, @city_id, @locale, 1, @now, @now);

-- name: InsertCredentials :exec
INSERT INTO credentials (user_id, password_hash, updated_at) VALUES (@user_id, @password_hash, @now);

-- name: InsertConsent :exec
INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
VALUES (@user_id, @document, @version, @now, @client_kind)
ON CONFLICT (user_id, document, version) DO NOTHING;
