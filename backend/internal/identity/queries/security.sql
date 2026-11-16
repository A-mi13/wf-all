-- Журнал безопасности аккаунта (спека identity §8.3; план I-2, Task 8): ссылка писем, адрес — только meta_enc.

-- name: InsertSecurityEvent :exec
INSERT INTO security_events (id, user_id, kind, session_id, device_id, ip, ip_city, user_agent, meta, meta_enc, key_id,
                             created_at)
VALUES (@id, @user_id, @kind, @session_id, @device_id, @ip, @ip_city, @user_agent, @meta, @meta_enc, @key_id, @now);
