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
  ошибкой. Новый способ входа — через эти три примитива, не своей копией. `finishLogin` — обёртка над
  `finishLoginAs(…, journal)`: регистрация идёт через ядро с `journalSignup`; сессию выдаёт только `finishLoginAs`
  (страж `TestIssueSessionOnlyInFinishLogin`). `pending_deletion` с `purge_at ≤ now` для входа и отмены удаления —
  как удалённый (`purgeDue`). Согласия — одно правило для входа и `me`: последняя принятая версия
  (`latestConsents`) ≠ текущей → принять (`consentsOutdated`).
- **2FA.** `user_mfa.failures` насыщается (`least(failures, 32766) + 1`). `SetupTotp`, `ConfirmTotp`, `DisableTotp`
  и `reauthenticate` вторым фактором, как и вход вторым фактором (`onSecondFactorLogin`), отменяют живой сброс 2FA
  владельцем (`cancelResetsByOwner`). «2FA есть» для сброса и его писем — только подтверждённая строка `user_mfa`
  (`confirmed_at IS NOT NULL`; `MfaResetUserMfa`, `has_mfa` в `MailMfaReset`). Явная отмена `cancelMyMfaReset` без
  подтверждённой 2FA — любой живой сессией (второго фактора нет — `403 reauth_required` был бы петлёй).
- **Бан** = ограничение `user_restrictions` с `capability = login` (спека §5.3): `blocked_until` — самое позднее
  `until` живых строк, нет строки или бессрочная — без поля. Операции без `account_blocked` в контракте отвечают
  `410` кода или `mfa_token` (R29).
- **Приманки** (спека §9.1): ответ и работа одинаковы для «есть аккаунт» и «нет». Нет почты на входе —
  `dummyPassword` (тот же запрос хэша и `VerifyDummy`); пароль длиннее предела — та же работа argon2. Работа с
  базой у неудачи — тоже одна: `loginFailed` открывает транзакцию с аудитом `login_failed` у любой почты
  (`security_events` — внутри неё, только у существующей); коммит лишь в одной ветке выдаёт почту по времени
  (страж по запросам — `TestLoginFailureSameWork`, секундомер — `TestLoginTiming`). Новая ветка сценария с приманкой —
  те же запросы и коммиты, что у настоящей. Коды: строка без аккаунта (приманка, код регистрации) в `lockByRow` берёт замок
  `users` вхолостую по нулевому id; повтор приманки ставит задачу письма вида настоящего кода — её отбрасывает сборщик
  («код другого вида»), письма нет (R26; страж `TestCodeDecoySameWork`). Регистрация на
  занятую почту и смена почты на занятую — строка `auth_codes` с `decoy` и письмо владельцу; сброс для нет/`deleted`/
  `banned` и `pending_deletion` с `purgeDue` — приманка без `user_id`, задача письма ставится, Composer отвечает `ErrSkip`; повтор отправки приманки
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
  `user_recovery_codes`, `passkeys`, `consents` — только `DELETE` и чтение `user_id` (на `user_mfa` ещё `confirmed_at` —
  есть ли подтверждённая 2FA; секретов не видит, rekey — у api); `user_identities.apple_refresh_enc` читает — для отзыва токена Apple (I-3); на `auth_codes` — `SELECT, DELETE` (`FOR UPDATE` — 42501); `auth_challenges` — чтение только `expires_at`,
  `consumed_at` (чистка без пачки по id); `security_events.meta_enc` — колоночный `UPDATE`. Тесты фона, писем и rekey — под ролью прода (`worker`, rekey —
  `api`): `dbtest.NewPoolsAs` или `roleApp` (`SET ROLE`).
- **Письма** (`identity/mail`, спека §8.2): Composer на каждый вид `app.MailKinds()`, только чтение под `worker`,
  повтор собирает то же письмо; логгера нет, кодов и адресов в ошибках нет. `mail.ErrSkip` — только когда повтор не
  поможет (строки нет, код погашен/истёк — ровно срок истёк, шифротекст не расшифровывается — `keys.ErrDecrypt`,
  аккаунт `deleted`); ключ строки вне кольца воркера (`keys.ErrUnknownKey` — при ротации api мог получить ключ раньше),
  сбой базы и нарушенный контракт производителя (нет ключа `meta`) — ошибка: повтор, затем видимая «мёртвая» задача.
  Отмена сброса 2FA без подтверждённой 2FA (`has_mfa = false`: снята или не подтверждена) — текст `text_disabled`;
  отмена владельцем — нейтрально («отменён вами»), без способа отмены. Время — `mailZone`: `users.timezone` → пояс города
  (`geo_read_city_settings`) → UTC, пустое/`Local`/битое имя — следующий шаг; формат `2026-11-16 15:00
  (Europe/Moscow)` (решение пользователя 16.11).
- **Чистка** (`app/cleanup.go`, спека §5.10): граница строгая (ровно срок — остаётся), шаги независимы
  (`errors.Join`). Работа над аккаунтом (снятие ограничений, сверка удаления) — `eachAccount`: tx на аккаунт, `users`
  `SKIP LOCKED`, пачки курсором `id > @after` (обработанный, занятый и упавший сдвигают курсор), упавший — в журнал,
  итог `errCleanupRowsFailed`. У `deleted` истёкшие ограничения снимаются молча (только `lifted_at`, без `version` и
  события). Коды и брони ника — по одному оператору вне tx (пачкой под замком сценария —
  `40P01`; стражи `TestCleanupCodesNoDeadlock`, `TestCleanupNicknameReservationsNoDeadlock`). Анонимизация — `SAVEPOINT` на аккаунт, упавшие исключаются до конца
  прогона.
- **Ротация кольца** — `api identity rekey [--dry-run]` (R28, `cmd/api/rekey.go` → `identity/rekey.Run` →
  `App.Rekey`) под ролью api: `user_mfa.secret_enc` (и неподтверждённые) и `user_identities.apple_refresh_enc`
  пачками без замка `users`; запись — условный `UPDATE` (тот же `key_id` и шифротекст): параллельная запись
  сценария не перетирается. Нечитаемые — код выхода 1; обрыв печатает частичный итог. MAC кодов не пересчитываются;
  MAC устройства (`API_IDENTITY_DEVICE_KEYS`) переписывается текущим ключом лениво — при refresh (`rekeyDevice`) и
  входе (`upsertDevice`). Порядок ротации — `docs/deploy-dev.md`.
- **Капитанство (план I-4, спека §3.12, §7, §10; решения плана R1–R42).** Порты условий — на этапе сборки, не env:
  прод (`captaincy_prod.go`, `!wf_e2e`) — `UnavailableCheck` по обоим именам, тег `wf_e2e` (`captaincy_e2e.go` в
  `cmd/api`, `cmd/admin-api`) — тестовые; набор сверяет `ValidateCaptainChecks` при старте; в `backend/Dockerfile` тега
  нет. Проверка файлов с тегом — `go vet -tags wf_e2e ./cmd/api ./cmd/admin-api` (в `backend:lint`), тесты —
  `go test -tags wf_e2e -run TestCaptainChecksE2EBuild ./cmd/api ./cmd/admin-api` (в `backend:test`). Условия — одно правило `evaluateCaptaincy` (экран, подача, карточка, одобрение);
  пустой набор портов — `checks_unavailable`; город — в tx сценария. Одобрение — под ролью `admin` без замка `users`:
  строка заявки `FOR UPDATE`, затем учётка решающего `FOR SHARE` через порт `StaffLinks` (R38: связь и версия —
  перечитанные в tx; версия ≠ `auth.Staff.Version` — 403), затем `role_assignments`; тот же порт — у исполнителя
  `identity.mfa_reset`. Порядок замков (R7): `users` → `sessions` → … → `captain_applications` → `role_assignments` →
  `mfa_resets` → коды. Свободные поля заявки и комментарий решения — без управляющих и невидимых (`\n`, `\t` — только в
  `note`); заголовки `X-WF-*` — `admin/headers.go`. Бан и статус: один предикат «заблокирован» `a.blocked` (R36);
  истечение бана — `expireBanTx` в чистке, входе (`finishLoginAs`) и отмене удаления (R12; истёкший бан — не блок);
  живая `login` ⇔ `banned` (R42). Вставка кода — `ON CONFLICT DO NOTHING` без цели (предикаты индексов не повторять:
  `42P10`); письмо «вы капитан» — только при живом допуске по заявке.
- **`identity/intx`** (спека §10.2): tx — параметром, на соединении транзакции ctx (`sameTx` сверяет `Conn()`:
  SAVEPOINT вызывающего законен, чужая tx — `ErrTxMismatch`); вызывающий берёт `users` первым; аудит пишет вызывающий
  (кроме `ChangeCityInTx`); сборка `app.NewTx` — без портов условий.
- **Стражи:** `TestNoStubOperations` (`httpapi/public/nostub_test.go`, `pendingOps` по `pending_*_test.go`; после
  I-4 непуст только `pendingI3` — 14 операций I-3; admin — строгий, без списка: план I-4, Task 12, R1);
  `identity_core_test.go` (`TestCoreOpsNotStub` — 36 операций I-2 и I-4 не 501, `pendingOps` public = `pendingI3`
  точно, операции identity контракта = I-2 ⊔ I-4 ⊔ I-3; `TestCoreIDOR` — сессия, код почты, сброс 2FA и заявка в
  капитаны чужого = несуществующее; `TestCoreCacheControl`, `TestCoreAnonymousNoIdempotency`, `TestCoreNoPIIInEvents`);
  `app/lock_order_test.go` (`*VsDeleteNoDeadlock`, `TestConfirmResetDeleteBetweenTx` — барьер `hooks.afterRowFound`,
  ожидание ≤ 2 с). Реализовал операцию I-3 — убери её из `pending_i3_test.go`; список, `pendingOpsAtFreeze` и
  `TestPendingOpsExpire` public удаляет последняя задача I-3.
