-- Чистка identity воркером (спека identity §5.10; план I-2, Task 19). Роль worker, права §5.5; каждая пачка — своя
-- транзакция (App.Cleanup). Граница сроков: строка уходит, когда срок прошёл строго (< @cutoff): ровно в срок —
-- остаётся до следующего запуска. Имена — с префиксом Cleanup.

-- name: CleanupAuthCodesDue :many
-- коды через 24 ч после истечения или закрытия — пачка id без замка; удаляет CleanupAuthCode по одному (m2 ревью
-- R1W6). Пачкой одним DELETE чистка держала бы замки удалённых кодов и ждала занятый: RevokeUserCodes и
-- DeletionRevokeCodes берут замки неотозванных кодов аккаунта (среди них и давно истёкших) в своём порядке — цикл, 40P01.
-- FOR UPDATE SKIP LOCKED воркеру недоступен (у worker нет UPDATE на auth_codes, §5.5)
SELECT c.id FROM auth_codes c
WHERE c.expires_at < @cutoff OR c.consumed_at < @cutoff OR c.revoked_at < @cutoff
ORDER BY c.id
LIMIT @batch;

-- name: CleanupAuthCode :execrows
-- один код своим оператором вне транзакции: ожидая занятый код, чистка не держит замков других — цикла нет. Условие
-- срока — снова (код мог измениться после выборки)
DELETE FROM auth_codes c
WHERE c.id = @id AND (c.expires_at < @cutoff OR c.consumed_at < @cutoff OR c.revoked_at < @cutoff);

-- name: CleanupAuthChallenges :execrows
-- вызовы через 24 ч после истечения или закрытия. Без пачки: worker видит у auth_challenges только expires_at и
-- consumed_at (§5.5) — ни id, ни ctid для выборки пачки; вызовы живут минуты, за запуск (раз в 10 мин) их немного
DELETE FROM auth_challenges WHERE expires_at < @cutoff OR consumed_at < @cutoff;

-- name: CleanupRefreshSuccessors :execrows
-- преемник refresh в окне гонки — после окна (successor_until); занятые сценарием строки — следующим запуском
UPDATE refresh_tokens SET successor_enc = NULL, successor_until = NULL
WHERE id IN (SELECT r.id FROM refresh_tokens r
             WHERE r.successor_until < @now
             ORDER BY r.id
             LIMIT @batch
             FOR UPDATE SKIP LOCKED);

-- name: CleanupSessions :execrows
-- отозванные и истёкшие (бездействие или абсолютный срок) — через 30 дней; refresh_tokens и вызовы сессии уходят
-- каскадом
DELETE FROM sessions
WHERE id IN (SELECT s.id FROM sessions s
             WHERE s.revoked_at < @cutoff OR s.idle_expires_at < @cutoff OR s.absolute_expires_at < @cutoff
             ORDER BY s.id
             LIMIT @batch
             FOR UPDATE SKIP LOCKED);

-- name: CleanupSecurityEvents :execrows
-- журнал безопасности — 180 дней
DELETE FROM security_events
WHERE id IN (SELECT e.id FROM security_events e
             WHERE e.created_at < @cutoff
             ORDER BY e.id
             LIMIT @batch
             FOR UPDATE SKIP LOCKED);

-- name: CleanupSecurityMeta :execrows
-- адрес для писем (meta_enc) — 7 дней; у worker колоночное UPDATE (meta_enc), key_id остаётся (CHECK допускает)
UPDATE security_events SET meta_enc = NULL
WHERE id IN (SELECT e.id FROM security_events e
             WHERE e.meta_enc IS NOT NULL AND e.created_at < @cutoff
             ORDER BY e.id
             LIMIT @batch
             FOR UPDATE SKIP LOCKED);

-- name: CleanupNicknameReservations :execrows
-- карантин ника — после expires_at; у worker нет UPDATE — без FOR UPDATE
DELETE FROM nickname_reservations
WHERE nickname_normalized IN (SELECT n.nickname_normalized FROM nickname_reservations n
                              WHERE n.expires_at < @now
                              ORDER BY n.nickname_normalized
                              LIMIT @batch);

-- name: CleanupRestrictionsDue :many
-- аккаунты с истёкшими неснятыми ограничениями, без замка: замок users и снятие — на аккаунт (CleanupLiftRestrictions).
-- until ≤ now — ограничение уже не действует (загрузчик сессии считает живым until > now); skip — аккаунты, упавшие
-- или занятые в этом прогоне
SELECT DISTINCT r.user_id FROM user_restrictions r
WHERE r.lifted_at IS NULL AND r.until IS NOT NULL AND r.until <= @now
  AND NOT (r.user_id = ANY(coalesce(sqlc.narg(skip)::uuid[], '{}')))
ORDER BY r.user_id
LIMIT @batch;

-- name: CleanupLockUser :one
-- воркер: аккаунт под замком (R30: users первым) или ничего — занят сценарием игрока, возьмёт следующий запуск
SELECT id, status::text AS status FROM users WHERE id = @id FOR NO KEY UPDATE SKIP LOCKED;

-- name: CleanupLiftRestrictions :many
-- снятие истёкших ограничений аккаунта (users уже под замком)
UPDATE user_restrictions SET lifted_at = @now
WHERE user_id = @user_id AND lifted_at IS NULL AND until IS NOT NULL AND until <= @now
RETURNING capability;

-- name: CleanupLiveCapabilities :many
-- закрытые возможности аккаунта после снятия (как загрузчик сессии: until NULL или > now)
SELECT DISTINCT capability FROM user_restrictions
WHERE user_id = @user_id AND lifted_at IS NULL AND (until IS NULL OR until > @now)
ORDER BY capability;

-- name: CleanupDeletedLeftovers :many
-- сверка удаления (спека бэкенда §6.5): аккаунты deleted, у которых остались секреты, привязки, согласия, коды, сессии
-- или история ника (анонимизация их стирает; строка могла появиться гонкой или старым релизом). Без замка: замок users
-- (CleanupLockUser, R30) и удаление — на аккаунт своей tx (I2 ревью R1W6); skip — аккаунты, упавшие или занятые в этом
-- прогоне. Вызовы auth_challenges воркеру по user_id не видны (§5.5) — их убирает срок (CleanupAuthChallenges)
SELECT u.id FROM users u
WHERE u.status = 'deleted'
  AND NOT (u.id = ANY(coalesce(sqlc.narg(skip)::uuid[], '{}')))
  AND (EXISTS (SELECT 1 FROM credentials c WHERE c.user_id = u.id)
       OR EXISTS (SELECT 1 FROM user_mfa m WHERE m.user_id = u.id)
       OR EXISTS (SELECT 1 FROM user_recovery_codes rc WHERE rc.user_id = u.id)
       OR EXISTS (SELECT 1 FROM passkeys p WHERE p.user_id = u.id)
       OR EXISTS (SELECT 1 FROM user_identities i WHERE i.user_id = u.id)
       OR EXISTS (SELECT 1 FROM consents cs WHERE cs.user_id = u.id)
       OR EXISTS (SELECT 1 FROM auth_codes ac WHERE ac.user_id = u.id)
       OR EXISTS (SELECT 1 FROM sessions s WHERE s.user_id = u.id)
       OR EXISTS (SELECT 1 FROM nickname_changes nc WHERE nc.user_id = u.id))
ORDER BY u.id
LIMIT @batch;

-- Удаление остатков — по одному аккаунту (users под замком CleanupLockUser).

-- name: CleanupDeletedCredentials :exec
DELETE FROM credentials WHERE user_id = @user_id;

-- name: CleanupDeletedMfa :exec
DELETE FROM user_mfa WHERE user_id = @user_id;

-- name: CleanupDeletedRecoveryCodes :exec
DELETE FROM user_recovery_codes WHERE user_id = @user_id;

-- name: CleanupDeletedPasskeys :exec
DELETE FROM passkeys WHERE user_id = @user_id;

-- name: CleanupDeletedIdentities :exec
DELETE FROM user_identities WHERE user_id = @user_id;

-- name: CleanupDeletedConsents :exec
DELETE FROM consents WHERE user_id = @user_id;

-- name: CleanupDeletedCodes :exec
DELETE FROM auth_codes WHERE user_id = @user_id::uuid;

-- name: CleanupDeletedSessions :exec
DELETE FROM sessions WHERE user_id = @user_id;

-- name: CleanupDeletedNicknameChanges :exec
DELETE FROM nickname_changes WHERE user_id = @user_id;
