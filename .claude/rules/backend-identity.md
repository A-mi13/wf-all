---
paths:
  - "backend/internal/identity/**"
---

# Модуль identity (аккаунт игрока)

Спека — `docs/superpowers/specs/2026-11-13-identity-design.md`; решения плана I-2 (R1–R31) — раздел «Решения плана»
`docs/superpowers/plans/2026-11-13-identity-2-core.md`. Комментарий пакета `internal/app/app.go` — порядок замков и
правило лимитов дословно.

- **Пакеты.** Корень `identity` — `Deps`/`WorkerDeps`, порты (`PasswordHasher`, `Attestor`, `ProviderReadiness`,
  `LegalDocs`), коды (`codes.go`), события (`events.go`), `SessionLoader`. Сценарии — `internal/app` (собирает
  `app.New` для api, `app.NewMaintenance` для воркера, admin-api и rekey), домен без БД — `internal/domain`, SQL —
  `queries/` → `internal/store/identitydb`. Снаружи: `httpapi` (public), `admin` (admin-api), `jobs` (воркер),
  `mail` (письма), `rekey` (команда api). `rekey` — не в корне: корень импортирует `app`, обратный импорт — цикл (N2).
- **Файлы-владельцы (R5).** Область — `httpapi/<область>.go`, `app/<область>.go`, `queries/<область>.sql`,
  `backend/internal/httpapi/public/pending_<область>_test.go`; модели sqlc закреплены якорем `queries/models.sql` (`emit_interface:
  false`). Новая операция — в файлы своей области, общие (`app.go`, `handler.go`, `nostub_test.go`) — через одного
  владельца.
- **Ядро входа (R6)** — `app/login_core.go`: `checkPassword` (argon2 без tx и соединения, R31; в tx — сверка, что хэш
  не изменился), `verifySecondFactor`, `finishLogin`. Порядок `finishLogin`: `banned` → 2FA → `pending_deletion` →
  сессия — `restore_token` не раскрывается до второго фактора. Неудачи (попытки, счётчики, письма) коммитятся до ответа
  ошибкой. Новый способ входа — через эти три примитива, не своей копией.
- **Бан** = ограничение `user_restrictions` с `capability = login` (спека §5.3): `blocked_until` — самое позднее
  `until` живых строк, нет строки или бессрочная — без поля. Операции без `account_blocked` в контракте отвечают
  `410` кода или `mfa_token` (R29).
- **Приманки** (спека §9.1): ответ и работа одинаковы для «есть аккаунт» и «нет». Нет почты на входе —
  `dummyPassword` (тот же запрос хэша и `VerifyDummy`); пароль длиннее предела — та же работа argon2. Регистрация на
  занятую почту и смена почты на занятую — строка `auth_codes` с `decoy` и письмо владельцу; сброс для нет/`deleted`/
  `banned` — приманка без `user_id`, задача письма ставится, Composer отвечает `ErrSkip`; повтор отправки приманки
  писем не шлёт (R26).
- **Свежий вход** — `requireFresh` (`app/reauth.go`): `sessions.reauth_at` не старше `domain.ReauthFresh` (10 мин),
  иначе `403 identity.reauth_required` до изменений. Свежесть вторым фактором (`disableTotp`) — `sessions.mfa_at`
  (R27). Неудачи — `reauth_failures`/`reauth_failed_at`, окно `domain.ReauthFailuresWindow` от первой (R16), не
  лимитер.
- **Порядок замков (R30):** `users` → `sessions` → `refresh_tokens` → `devices` → `user_mfa`/`user_recovery_codes` →
  `mfa_resets` → `auth_codes`/`auth_challenges`; сценарий над аккаунтом первым берёт `lockUser` (`FOR NO KEY UPDATE`).
  Аккаунт известен только из строки кода, вызова или токена — `lockByRow` (строка без замка → `lockUser` → `FOR UPDATE`
  строки с перепроверкой). Исключение — строка кода/вызова сразу после `users` (комментарий `app.go`). Строка
  `approvals` — раньше строк модуля. Воркер по аккаунтам — `users` первым `SKIP LOCKED`.
- **Внутри `db.InTx` лимит — только `a.limiterTx(tx)`**, не `a.limiter`/`a.risk`/`a.pool`: второе соединение под
  замком `users` при исчерпанном пуле ждёт вечно (C1 волны 4). Тяжёлое (argon2, сеть) — вне tx.
- **Периодика (R7)** — `jobs.Periodic()` (чистка и исполнение сброса 2FA — 10 мин, анонимизация — 1 ч, `RunOnStart`)
  подключают `cmd/worker` и `cmd/admin-api` (`append(periodic.Jobs(), identityjobs.Periodic()...)`): лидером River
  бывает любой. Исполнители — только воркер (`jobs.AddWorkers`), у admin-api — заглушки `queue.AddForeign` по
  `queue.WorkerKinds`. Страж — `TestPeriodicSameInBothBinaries` (`cmd/admin-api` и `cmd/worker`). Смена лидера
  сдвигает интервал: сброс 2FA может уйти до ~20 мин позже `execute_at`.
- **Права воркера** (спека §5.5, `platform/grants/grants.sql`): у `worker` на `credentials`, `user_mfa`,
  `user_recovery_codes`, `passkeys`, `consents` — только `DELETE` и чтение `user_id` (секретов не видит, rekey — у
  api); `user_identities.apple_refresh_enc` читает — для отзыва токена Apple (I-3); на `auth_codes` — `SELECT, DELETE` (`FOR UPDATE` — 42501); `auth_challenges` — чтение только `expires_at`,
  `consumed_at` (чистка без пачки по id); `security_events.meta_enc` — колоночный `UPDATE`. Тесты фона, писем и rekey — под ролью прода (`worker`, rekey —
  `api`): `dbtest.NewPoolsAs` или `roleApp` (`SET ROLE`).
- **Письма** (`identity/mail`, спека §8.2): Composer на каждый вид `app.MailKinds()`, только чтение под `worker`,
  повтор собирает то же письмо; логгера нет, кодов и адресов в ошибках нет. `mail.ErrSkip` — только когда повтор не
  поможет (строки нет, код погашен/истёк — ровно срок истёк, ключ вне кольца, аккаунт `deleted`); сбой базы и
  нарушенный контракт производителя (нет ключа `meta`) — ошибка: повтор, затем видимая «мёртвая» задача. Время — `mailZone`: `users.timezone` → пояс города
  (`geo_read_city_settings`) → UTC, пустое/`Local`/битое имя — следующий шаг; формат `2026-11-16 15:00
  (Europe/Moscow)` (решение пользователя 16.11).
- **Чистка** (`app/cleanup.go`, спека §5.10): граница строгая (ровно срок — остаётся), шаги независимы
  (`errors.Join`). Работа над аккаунтом (снятие ограничений, сверка удаления) — `eachAccount`: tx на аккаунт, `users`
  `SKIP LOCKED`, пачки курсором `id > @after` (обработанный, занятый и упавший сдвигают курсор), упавший — в журнал,
  итог `errCleanupRowsFailed`. Коды и брони ника — по одному оператору вне tx (пачкой под замком сценария —
  `40P01`; стражи `TestCleanupCodesNoDeadlock`, `TestCleanupNicknameReservationsNoDeadlock`). Анонимизация — `SAVEPOINT` на аккаунт, упавшие исключаются до конца
  прогона.
- **Ротация кольца** — `api identity rekey [--dry-run]` (R28, `cmd/api/rekey.go` → `identity/rekey.Run` →
  `App.Rekey`) под ролью api: `user_mfa.secret_enc` (и неподтверждённые) и `user_identities.apple_refresh_enc`
  пачками без замка `users`; запись — условный `UPDATE` (тот же `key_id` и шифротекст): параллельная запись
  сценария не перетирается. Нечитаемые — код выхода 1; обрыв печатает частичный итог. MAC не пересчитываются.
  Порядок ротации — `docs/deploy-dev.md`.
- **Стражи:** `TestNoStubOperations` (`httpapi/public/nostub_test.go`, `pendingOps` по `pending_*_test.go`);
  `identity_core_test.go` (`TestCoreOpsNotStub` — 32 операции и `pendingOps` 18/9 точными множествами,
  `TestCoreIDOR`, `TestCoreCacheControl`, `TestCoreAnonymousNoIdempotency`, `TestCoreNoPIIInEvents`);
  `app/lock_order_test.go` (`*VsDeleteNoDeadlock`, `TestConfirmResetDeleteBetweenTx` — барьер `hooks.afterRowFound`,
  ожидание ≤ 2 с). Реализовал операцию — убери её из `pending_*`.
