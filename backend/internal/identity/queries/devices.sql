-- Устройства (спека identity §3.8, план I-2 R2, Task 8): HMAC install_id любым ключом кольца, ленивая ротация.

-- name: FindDeviceForUpdate :one
SELECT id, key_id FROM devices
WHERE platform = @platform AND install_mac = ANY(@macs::bytea[])
ORDER BY (key_id = @current_key::text) DESC, id
LIMIT 1
FOR UPDATE;

-- name: UpdateDeviceSeen :exec
UPDATE devices
SET install_mac = @install_mac, key_id = @key_id, model = @model, os_version = @os_version,
    app_version = @app_version, last_seen_at = @now
WHERE id = @id;

-- name: InsertDevice :one
INSERT INTO devices (id, platform, install_mac, key_id, model, os_version, app_version, first_seen_at, last_seen_at)
VALUES (@id, @platform, @install_mac, @key_id, @model, @os_version, @app_version, @now, @now)
ON CONFLICT (platform, install_mac) DO NOTHING
RETURNING id;

-- name: LockDeviceSignups :one
SELECT platform, signups FROM devices WHERE id = $1 FOR UPDATE;

-- name: IncDeviceSignups :exec
UPDATE devices SET signups = signups + 1 WHERE id = $1;

-- name: UpsertDeviceAccount :exec
INSERT INTO device_accounts (device_id, user_id, signed_up, first_seen_at, last_seen_at)
VALUES (@device_id, @user_id, @signed_up, @now, @now)
ON CONFLICT (device_id, user_id) DO UPDATE
SET last_seen_at = EXCLUDED.last_seen_at, signed_up = device_accounts.signed_up OR EXCLUDED.signed_up;
