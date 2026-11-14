# Identity I-2: платформа, схема, ядро и 2FA — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

> **Формат — бриф задач, без кода реализации** (решение пользователя 13.10.2026, память `plan-format-brief`). Каждая
> задача: цель, риск, файлы, точные интерфейсы (сигнатуры Go, таблицы и колонки, operationId), тесты (имя → вход →
> ожидаемое), подсадка, приёмка, ловушки. Как писать SQL и Go — решает исполнитель по TDD, спеке и `.claude/rules/`.
> Авторитет: спека → «Решения плана» (R, явно отступающее от спеки, действует до правки спеки в Task 20) → тексты
> задач. Где задача расходится со спекой без ссылки на R — права спека, задача — ошибка плана (сообщить контроллеру).

**Goal:** этапы 2–5 спеки identity (§15) — живые регистрация, вход и 2FA игрока на замороженном контракте I-1:
- **платформа:** `platform/otp` и `platform/emailaddr` (staff переходит без изменения поведения), `flags.Rules`
  (`GlobalOnly`, `Requires`) с выдачей `platform.flag_global_only`/`flag_requires`, `platform/ipgeo`, новые поля
  `httpx.Problem` с хелперами, конфиг и env;
- **схема:** миграции §5 (`pending_deletion`, все таблицы и представления identity, правки `users`, `sessions`,
  `role_assignments`, `captain_applications`, строки шести флагов §4.1, `staff_player_links` §7.6), `grants.sql`,
  `archtest/ownership.go`, sqlc; enum `FeatureFlagKey` и подписи флагов — тем же коммитом (R4 плана I-1);
- **ядро:** регистрация с согласиями, вход, коды из письма, сессии и refresh, `identity.SessionLoader` в `cmd/api`,
  выход, «Где вы вошли», сброс пароля, `me`, ник, документы, свежий вход, смена почты и пароля, удаление с периодом
  отмены и анонимизацией, письма, чистка, `.well-known` в `api`;
- **2FA:** TOTP, резервные коды, шаг входа, сброс пароля со вторым фактором; сброс 2FA поддержкой
  (`identity.mfa.reset`, исполнитель подтверждения, задача исполнения, отмена сотрудником и владельцем);
- 32 операции public и 2 операции admin уходят из `pendingOps` (Приложение 1); в `pendingOps` остаются 18 public
  (I-3: 14, I-4: 4) и 9 admin (I-4).

Не здесь: устройства сверх минимума сессии (DeviceCheck, App Attest, Play Integrity, письмо о новом устройстве,
`device_shared`, связанные аккаунты), соцвходы, passkeys — план I-3; капитанство, остальные админ-ручки и экраны,
связь «сотрудник → игрок» в контракте, стенд и живой e2e — план I-4.

**Architecture:**
- **Модуль `identity`** по спеке бэкенда §3 и спеке §12: корневой пакет — API модуля (`identity.go` — `New`,
  `Service.User`; `session_loader.go` — `SessionLoader`; `events.go`; `codes.go` из I-1), `internal/domain` — чистые
  правила (ник, возраст, коды, сроки сессий, документы, одноразовые домены, UA), `internal/app` — сценарии §3 в
  транзакциях платформы (`db.InTx`, события и аудит в той же транзакции, письма — задачей River в ней же),
  `internal/store` — sqlc `identitydb` + репозиторий, `httpapi` — public, `admin` — admin-api, `jobs` — задачи
  воркера, `queries/`.
- **Ядро входа — одно место** (R6): `app` держит три примитива, которые зовут все сценарии входа — проверку пароля
  (`VerifyDummy` на «нет аккаунта», пересчёт хэша), проверку второго фактора (TOTP с `last_used_step`, резервный код,
  счётчик неудач и письма) и завершение входа (`banned` → 403, `pending_deletion` → `deletion_pending`, 2FA →
  `mfa_required`, иначе сессия). Сценарии регистрации, входа, сброса, отмены удаления и второго шага — тонкие
  обёртки над ними.
- **Сессия** — строка `sessions` (= `sid` JWT) + цепочка `refresh_tokens` с ротацией и окном гонки 15 с
  (`token.SealSuccessor`); access — `token.Issuer.Access` (есть). Загрузчик `identity.NewSessionLoader` за
  `auth.NewCachedLoader` (5 с) заменяет `auth.NoSessions` в `cmd/api`.
- **Устройство** — минимум, без которого нет сессии (R2): HMAC `install_id` (кольцо `API_IDENTITY_DEVICE_KEYS`),
  строки `devices`/`device_accounts`, правило `device_required` (К5), лимит двух регистраций, нормализация
  `app_version`. Целостность — порт `Attestor` с реализацией «выключено» до I-3.
- **Сброс 2FA поддержкой** — действие `platform/approvals` `identity.mfa_reset` (`LockActors`, хук staff), исполнитель
  в admin-api пишет только `mfa_resets`, `security_events`, `audit_log`, `outbox`, `river_job`; исполнение по сроку —
  периодическая задача воркера.
- **Фон:** письма — `mail.SendArgs` (очередь `mail`, исполняет воркер; `mail.Composer` identity по `ref`); чистка,
  анонимизация, исполнение сбросов 2FA, снятие истёкших ограничений — периодические задачи `maintenance` identity,
  которые ставит лидер River (воркер или admin-api, R7).

**Tech Stack:** Go 1.27 (chi, pgx v5, sqlc с грамматикой PG17, goose, River 0.x), kin-openapi, oapi-codegen strict;
новые зависимости — `github.com/oschwald/maxminddb-golang/v2` (v2.9.0, спека §11.1) и для фикстуры MMDB в модуле
`backend/tools` — `github.com/maxmind/mmdbwriter` (последняя стабильная). Точные версии — `docs/versions.md`.

**Spec:** `docs/superpowers/specs/2026-11-13-identity-design.md` (дальше «спека»): §0, §2, §3.1–3.4, §3.7, §3.9–3.11,
§3.13–3.16, §4, §5, §6.1–6.2, §6.4, §7.1 (две операции сброса 2FA), §7.6, §8, §9.1–9.3, §9.6, §10.1, §11.3, §12,
§13, §15 (этапы 2–5), §16, §18. Контракт — замороженный срез I-1 (`contracts/openapi/{public,admin}/identity/`,
страж `contracts/test/identity-freeze.test.mjs`); план I-1 — `docs/superpowers/plans/2026-11-13-identity-1-contract.md`
(R1–R20, «Передача следующим планам»). Спека бэкенда `docs/superpowers/specs/2026-10-01-backend-architecture-design.md`
§3–§7 (модуль, владение, `intx`, события, сессии и JWT). Образец формата — план B
`docs/superpowers/plans/2026-10-27-staff-b-geo2.md`.

**Предусловие старта:** ветка `spec/identity-2` от `spec/identity-1` (I-1 исполнен, коммиты `30a9bdb…1cd84ea`).
Если I-1 к старту слит в `main` — ветка от `origin/main`; номера миграций — следующие свободные в момент Task 5
(R11). Выкатка на стенд — Task 21 (воркер на Render, ключи стенда задаёт контроллер; решения пользователя U1–U4).

## Global Constraints

- **Правила репо** — `CLAUDE.md`, `.claude/rules/{backend,backend-schema,backend-platform,backend-http,backend-staff,env,contracts}.md`.
  TDD: сначала падающий тест; каждый страж проверяется подсадкой бага (`node scripts/plant.mjs …`, в отчёт — что
  подсажено и что упало).
- **Риск задачи** — A/B/C по `.claude/agents/wf-implementer.md`: для A исполнитель до первого теста пишет таблицу
  режимов отказа (гонки, повторы, права в момент действия, частичный отказ, время, перечисление аккаунтов), ревьюер
  проходит 10 категорий нестандартных условий.
- **Контракт заморожен** (I-1, R17): YAML identity не правится. Единственная заранее согласованная правка — enum
  `FeatureFlagKey` в коммите миграции флагов (Task 5, R4 плана I-1; хэш среза он не меняет). Расхождение контракта и
  сценария — стоп и вопрос контроллеру, не правка YAML. Новых кодов ошибок нет: все коды I-2 уже в
  `identity/codes.go`, `httpx/codes.go` и локалях (I-1).
- **Заглушки:** реализованная операция в той же задаче уходит из `pendingOps` своего пакета (`internal/httpapi/{public,admin}`);
  `TestNoStubOperations` требует, чтобы вне списка операция отвечала не 501, `TestPendingOpsExpire` — чтобы
  реализованной в списке не было. `pendingOpsAtFreeze` и его хэш не меняются.
- **Коммиты** — делает контроллер (субагенты не коммитят): пути и сообщение — в разделе «Коммит» отчёта; одна строка
  по-русски без тела; `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A/.`, `commit -a`,
  `stash`, `reset`, `clean`, `checkout -- .`. Доки нативщика `docs/WF_…_2026_10_19.md` и `docs/screens/` — не
  коммитить.
- **Код:** комментарии по-русски, идентификаторы по-английски, LF. Время — только `clock.Clock` (тесты —
  `clocktest.Fake`), id — UUIDv7 из Go (`id.New`), умолчаний `now()` в новых таблицах нет (спека §5.3).
- **Секреты и ПД** (спека §1.4, §9.6): пароль — `password.Hasher`; коды, резервные коды, ключи лимитов, устройства —
  HMAC кольца (`keys.Ring.MAC`, своя метка на назначение); секреты TOTP, данные регистрации, адреса для писем —
  `keys.Ring.Seal` с aad = id строки; токены `mfa_token`, `restore_token`, refresh — SHA-256. Почта, телефон, коды,
  токены, `install_id` — не в событиях, аргументах задач, логах, `Before/After` аудита (маскирование `audit.Mask`).
- **Перечисление аккаунтов** (спека §9.1): ответ, код и время у «есть аккаунт» и «нет аккаунта» одинаковы; разница
  уходит письмом владельцу, а не в ответ.
- **Схема:** sqlc — грамматика PG17; индекс по нормализованному тексту — через `*_normalized STORED`; SQL модуля —
  только DML верхнего уровня (`sqlscan`); новая таблица — строка владельца в `ownership.go`, ветка CASE в `grants.sql`
  и случай в `grants_test.go`. Применённые миграции не правятся.
- **Транзакции:** ручка с `Idempotency-Key` — ровно одна закоммиченная `db.InTx`; анонимные мутации идут без
  идемпотентности (`x-anonymous-mutations`), их защищают одноразовые коды и токены, уникальные индексы и лимиты.
  Аудит, события, задача письма — в транзакции действия.
- **Окружение:** новый env-ключ — сразу в `platform/config`, `backend/.env.example`, локальный `backend/.env`
  (скриптом из scratchpad, значения не выводить; `.claude/rules/env.md`) и `render.yaml` (Task 4).
- **Тесты:** исполнитель гоняет тесты своих пакетов (`go test -p 4 ./internal/identity/... ./internal/httpapi/public/...`
  и т. п. из `backend/`; Postgres — `bash scripts/pg.sh start` в фоне) и typecheck своего пакета; полный
  `./task backend:test`, `./task admin:test`, `./task packages:ci` и lint — контроллер в конце пачки. Тесты пакетов
  с базой — параллельные (`t.Parallel()`, `TestMain` с `dbtest.Main`), чтения — `dbtest.SharedAs`, запись и HTTP —
  `dbtest.New*`, проверки прав ролей — под ролью прода.
- **Кодогенерация:** бэкенд-задачи — прямой `sqlc generate` (из `backend/`) без pnpm; `./task gen` (pnpm) — только
  Task 5 (enum `FeatureFlagKey`) и Task 17 (если меняется что-то в генерации admin; по плану — нет). pnpm — один
  процесс на машину: на время Task 5 другие исполнители pnpm не запускают.
- **Строки:** тексты писем — `backend/locales/{ru,en}.json` (ключи `identity.mail.<письмо>.{subject,text}`, en с теми
  же ключами); тексты UI и ошибок уже есть (I-1), кроме подписей шести флагов (Task 5).

## Review Focus

1. **Перечисление аккаунтов и время.** `startSignup` (приманка `decoy`, хэш пароля в обеих ветках, письмо владельцу),
   `login` (`VerifyDummy` на «нет почты» и «нет пароля», один код `invalid_credentials`, лимит по почте одинаков),
   `startPasswordReset` (приманка без письма), `startEmailChange` (письмо на новый адрес вместо ответа),
   `resendAuthCode` (приманка с теми же паузами), `confirmPasswordReset` (сверка кода раньше любой ветки 2FA —
   `TestConfirmResetNoOracle`, C1 ревью плана), `confirmEmailChange` (приманка с `user_id`) — одинаковы ответом,
   статусом, заголовками и временем. Стражи
   времени (медиана 50 запросов, < 15 %) — Task 10, Task 11. Ни один ответ и ни один код ошибки не различает «есть /
   нет».
2. **2FA не обходится.** Шаг входа после пароля (`mfa_required`), сброс пароля при 2FA только с фактором (код не
   тратится без фактора), свежий вход при 2FA — только вторым фактором (`reauth_method_not_allowed` на пароль),
   выключение 2FA — свежим входом вторым фактором; повтор кода TOTP того же или старшего шага (`last_used_step`)
   отвергается; резервный код сгорает при любом использовании; `mfa_token` — 5 попыток, одноразовый. Task 9, 13, 16.
3. **Refresh и сессии.** Ротация на каждый `refreshSession`; повтор использованного токена вне окна 15 с или с
   другого устройства → отзыв сессии, `security_events (refresh_reuse)`, письмо; в окне с того же устройства — тот же
   преемник; отзыв (выход, смена и сброс пароля, удаление) действует ≤ 5 с (кэш загрузчика); срок бездействия и
   абсолютный максимум по `client_kind` (90/365 и 30/180); `client_kind` ставит сервер по `peer.ViaBFF`. Task 8, 11.
4. **Свежий вход.** Каждая операция с «С» в §6.3 без `reauth_at` моложе 10 минут → `403 identity.reauth_required`
   до любых изменений; вход сам ставит `reauth_at`; 5 неудач свежего входа на сессию за 15 минут — `429`. Task 9,
   13, 15, 16.
5. **Удаление.** Запрос — `pending_deletion`, `hidden`, все сессии отозваны, заявка `withdrawn`, событие
   `user_deletion_scheduled` (не `user_deleted`); вход в период — `deletion_pending` без сессии; отмена — `active` и
   новая сессия; анонимизация по `purge_at` — необратима, повторяема, стирает секреты и привязки, ник — в карантин,
   почта — заглушка, `user_deleted` только здесь; права воркера ровно §5.5. Task 15, 19.
6. **Сброс 2FA поддержкой.** `requestMfaReset` — только запрос `approvals` (`dual`); исполнитель — `mfa_resets`
   (`execute_at = now + 7 дней`), письмо, событие на агрегате `mfa_reset`, аудит; admin-api не пишет `users`,
   `sessions` (у роли `admin` нет `UPDATE`); `self_action` по `auth.Staff.LinkedUserID` у запроса, подтверждения и
   отмены; вход владельца вторым фактором отменяет сброс; исполнение по сроку стирает TOTP и коды и отзывает сессии,
   ключи доступа не трогает. Task 17, 18.
7. **Секреты в базе и правах.** `no-store` и повтор `setupTotp` → `409 idempotency.replay_unavailable` через весь
   конвейер public (m15 плана I-1); под ролью `admin` не читаются `credentials`, `user_mfa.secret_enc`, `auth_codes`,
   `user_identities`, `passkeys`; под `worker` — `credentials.password_hash`, `user_mfa.secret_enc`; ПД нет в
   событиях (golden-файлы payload), аргументах задач и аудите. Task 6, 7, 16.
8. **Флаги.** `GlobalOnly` — `422 platform.flag_global_only`; `Requires` в обе стороны — `409 platform.flag_requires`
   под гонкой двух `PATCH` без взаимоблокировки (замок строк по возрастанию ключа); `Enabled*` считает Google
   выключенным при выключенном Apple. Task 2.
9. **Миграции.** Вверх-вниз до точной прежней схемы, отказ на данных (`users.email IS NULL`, непустая `sessions`),
   значение `pending_deletion` — отдельной миграцией без транзакции, `down` — пересоздание типа с отказом при строках
   `pending_deletion`; enum `FeatureFlagKey` равен `feature_flags` (`TestFlagKeysMatchContract`). Task 5, 6.

---

## File Structure

| Путь | Ответственность | Задача |
| --- | --- | --- |
| `backend/internal/platform/otp/*`, `backend/internal/platform/emailaddr/*`, `backend/internal/staff/internal/domain/{totp,codes,email}.go` | TOTP, резервные коды, общий генератор кода, нормализация почты; staff — тонкие делегаты | 1 |
| `backend/internal/platform/flags/{rules.go,flags.go,queries/flags.sql,flagsdb/*}`, `backend/internal/httpapi/admin/{flags.go,platform_flags_test.go}`, `.claude/rules/{contracts,backend-platform}.md` (строки о флагах) | `flags.Rules`, замок двух строк, выдача кодов флагов | 2 |
| `backend/internal/platform/ipgeo/*`, `backend/tools/{go.mod,go.sum,ipgeofixture/main.go}`, `backend/internal/platform/httpx/{problem.go,problem_test.go}`, `backend/go.mod`, `backend/go.sum`, `docs/versions.md`, `NOTICE` | город по IP, поля `Problem` и хелперы | 3 |
| `backend/internal/platform/config/{config.go,config_test.go,render_stand_test.go}`, `backend/.env.example`, `backend/.env` (скриптом), `render.yaml`, `docs/deploy-dev.md` | env §11.3 в части I-2 | 4 |
| `backend/migrations/{0024_identity_status.sql,0025_identity.sql,identity_0025_test.go,README.md}`, `contracts/openapi/admin/platform/schemas.yaml` (enum `FeatureFlagKey`), сгенерированное (`contracts/openapi/admin.yaml`, `backend/internal/httpapi/admin/oapi/*`, `apps/admin/src/api/gen/admin.d.ts`), `apps/admin/messages/{ru,en}.json` (`flags.<ключ>.*`) | схема §5, флаги §4.1, `staff_player_links` | 5 |
| `backend/internal/platform/grants/{grants.sql,grants_test.go}`, `backend/internal/archtest/ownership.go`, `backend/sqlc.yaml`, `backend/internal/identity/queries/models.sql`, `backend/internal/identity/internal/store/identitydb/{db.go,models.go}` | права ролей §5.5, владение, элемент sqlc и якорь моделей | 6 |
| `backend/internal/identity/internal/domain/*`, `backend/internal/identity/events.go` (+ `testdata/events/*.golden.json`) | чистые правила, каталог событий I-2 | 7 |
| `backend/internal/identity/{identity.go}`, `backend/internal/identity/internal/app/{app.go,devices.go,sessions.go,security.go,mail.go,audit.go,reauth.go}`, `backend/internal/identity/internal/store/{store.go}`, `backend/internal/identity/queries/{devices,sessions,security}.sql`, `backend/internal/identity/httpapi/{handler.go,<область>.go — раскладка заглушек}`, `backend/internal/httpapi/public/{handler.go,server.go,nostub_test.go,pending_*_test.go}`, `backend/cmd/api/main.go` | каркас модуля, устройство, выдача сессии, сборка `cmd/api` | 8 |
| `backend/internal/identity/internal/app/{login_core.go,factors.go,mfa_reset_hook.go}`, `backend/internal/identity/queries/{factors,challenges}.sql` | завершение входа, пароль, второй фактор, токены шагов | 9 |
| `…/app/signup.go`, `…/queries/{codes,signup}.sql`, `…/httpapi/signup.go`, `…/httpapi/public/pending_signup_test.go` | регистрация, коды, ник, документы | 10 |
| `…/app/login.go`, `…/app/refresh.go`, `backend/internal/identity/session_loader.go`, `…/queries/login.sql`, `…/httpapi/login.go`, `pending_login_test.go`, `backend/cmd/api/main.go` (загрузчик) | вход, refresh, выход, `SessionLoader` | 11 |
| `…/app/sessions_list.go`, `…/app/methods.go`, `…/httpapi/{sessions.go,wellknown.go}`, `backend/internal/httpapi/public/handler.go` (монтаж `.well-known`), `pending_sessions_test.go`, `.claude/rules/backend-http.md` | «Где вы вошли», `getAuthMethods`, `.well-known` | 12 |
| `…/app/{password.go,reauth_op.go,email_change.go}`, `…/queries/password.sql`, `…/httpapi/password.go`, `pending_password_test.go` | сброс и смена пароля, свежий вход, смена почты | 13 |
| `…/app/account.go`, `…/queries/account.sql`, `…/httpapi/account.go`, `pending_account_test.go` | `me`, ник, документы, способы входа | 14 |
| `…/app/{deletion.go,anonymize.go}`, `…/queries/deletion.sql`, `…/httpapi/deletion.go`, `…/jobs/anonymize.go`, `pending_deletion_test.go` | удаление, отмена, анонимизация | 15 |
| `…/app/{mfa.go,mfa_login.go}`, `…/queries/mfa.sql`, `…/httpapi/mfa.go`, `pending_mfa_test.go`, `backend/internal/httpapi/public/nostore_test.go` | TOTP, коды, шаг входа, сквозной `no-store` | 16 |
| `backend/internal/identity/admin/{handler.go,mfa_reset.go,approvals.go}`, `…/app/mfa_reset_admin.go`, `…/queries/mfa_reset.sql`, `backend/internal/platform/auth/staff.go` (`LinkedUserID`), `backend/internal/staff/{queries/sessions.sql,internal/app/session.go}`, `backend/internal/httpapi/admin/{server.go,nostub_test.go,permissions_test.go}`, `backend/cmd/admin-api/{main.go,identity_wiring_test.go}`, `.claude/rules/backend-staff.md` | сброс 2FA сотрудником, исполнитель, сборка admin-api | 17 |
| `…/app/{mfa_reset_hook.go,mfa_reset_player.go,mfa_reset_exec.go}`, `…/httpapi/mfa_reset.go`, `pending_mfa_reset_test.go` | отмена владельцем, исполнение по сроку | 18 |
| `backend/internal/identity/mail/*` (composers), `backend/locales/{ru,en}.json`, `backend/internal/identity/jobs/{jobs.go,maintenance.go}`, `backend/internal/identity/rekey/rekey.go`, `…/app/{cleanup.go,rekey.go}`, `…/queries/cleanup.sql`, `backend/internal/platform/queue/foreign.go`, `backend/cmd/worker/{main.go,kinds_test.go,maintenance_test.go}`, `backend/cmd/admin-api/main.go` (периодика), `backend/cmd/api/{main.go,rekey.go}` (команда `api identity rekey`) | письма, задачи и чистка, ротация кольца | 19 |
| `backend/internal/httpapi/public/identity_core_test.go`, `.claude/rules/backend-identity.md` (новый), `CLAUDE.md` (список правил), `docs/01-домен-и-правила.md`, спека бэкенда (§2, §4.1, §4.6, §6.2, §7.1, §7.6, §11, §13), спека identity (поправки R) | сквозные стражи и документы этапа | 20 |
| `render.yaml` (сервис воркера), `backend/internal/platform/config/render_stand_test.go` (`TestRenderYAMLNoWorker` → страж воркера), `docs/deploy-dev.md`; вне репозитория — панель Render и Neon | выкатка воркера и ключей на стенд | 21 |

`…` — `backend/internal/identity`. Внутри пакета исполнитель раскладывает файлы сам, если не трогает чужие файлы-владельцы
(R5) и интерфейсы стыков.

# Задачи

Обозначения: **Риск** — A/B/C; **Тесты** — `Имя` → вход → ожидаемое; **Подсадка** — какой баг подсадить, какой тест
обязан упасть; **Приёмка** — что проверяет ревьюер. Сквозные условия всех задач сценариев (10–18): тесты `app` — на
реальном Postgres под ролью прода; для каждого кода ошибки операции — свой случай; событие, аудит и задача письма
проверяются в той же транзакции (откат — ничего из этого нет); HTTP-тест (`apitest`) на каждую операцию — успех по
контракту и хотя бы один код; операция уходит из `pendingOps`.

## Этап 2. Платформа (спека §15 п. 2)

### Task 1: `platform/otp` и `platform/emailaddr` — перенос из staff без изменения поведения

**Цель.** Игрокам нужны те же примитивы, что сотрудникам (спека §3.3, §5.7; decisions (К)): TOTP RFC 6238, резервные
коды, нормализация почты. Реализация переезжает из `staff/internal/domain` в платформу; staff зовёт платформу через
тонкие делегаты, его тесты не меняются ни строкой.

**Риск:** B (поведение входа сотрудников обязано остаться байт в байт; новых путей данных нет).

**Файлы.** Создать: `backend/internal/platform/otp/{otp.go,codes.go,otp_test.go,codes_test.go}`,
`backend/internal/platform/emailaddr/{emailaddr.go,emailaddr_test.go}` (тесты — перенос `totp_test.go`, `codes_test.go`,
`email_test.go` staff с заменой имён + новые ниже). Изменить: `backend/internal/staff/internal/domain/{totp.go,codes.go,email.go}`
— тела функций заменяются вызовами платформы, экспортированные имена и константы staff остаются (псевдонимы
констант: `TOTPDigits = otp.Digits` и т. д.). `pictographic.go`, `name.go` staff — не трогать.

**Интерфейсы.**
- `package otp`: константы `Digits = 6`, `Period = 30`, `Skew = 1`, `SecretBytes = 20`, `RecoveryCount = 10`,
  `CodeLen = 10`; `func NewSecret(rnd io.Reader) ([]byte, error)`; `func Step(t time.Time) int64`;
  `func Code(secret []byte, step int64) string`; `func Verify(secret []byte, code string, now time.Time, lastUsed int64) (step int64, ok bool)`
  (шаг `≤ lastUsed` — `false`); `func Base32(secret []byte) string`; `func URI(issuer, account string, secret []byte) string`;
  `func NewCode(rnd io.Reader) (string, error)` (формат `xxxxx-xxxxx`, 10 символов base32 строчными с дефисом — как
  нынешний `newCode` staff, `codes.go`; общий генератор кода приглашения и резервного кода);
  `func NewRecoveryCodes(rnd io.Reader) ([]string, error)` (10 кодов, без повторов); `func NormalizeRecoveryCode(s string) (string, bool)`;
  `func NormalizeCode(s string) (string, bool)` (6 цифр TOTP — ровно поведение нынешнего `domain.NormalizeCode`:
  дефис — отказ; правила не расширять).
- `package emailaddr`: `const MaxLen = 254`; `func Normalize(s string) (string, bool)` — ровно правила
  `staff/internal/domain.NormalizeEmail` (NFKC, нижний регистр, dot-atom, IDN → A-label с белым списком, невидимые —
  отказ, ≤ 254 байт); `func Domain(addr string) string` — доменная часть канонического адреса (блок-лист
  одноразовых, Task 7).
- staff: `domain.NewTOTPSecret`, `TOTPStep`, `TOTPCode`, `VerifyTOTP`, `Base32Secret`, `OTPAuthURI`, `NewInviteCode`,
  `NewRecoveryCodes`, `NormalizeRecoveryCode`, `NormalizeInviteCode`, `NormalizeCode`, `NormalizeEmail` — те же
  сигнатуры, тело — вызов платформы.

**Тесты.**
- Перенесённые тесты staff — в платформе под новыми именами (`TestVerifyRejectsReplayedStep`, `TestRFC6238Vectors`,
  `TestNormalizeIDN…` и т. д.); тесты `staff/internal/domain/*_test.go` — **без правок** и зелёные.
- `TestStaffDomainDelegates` (staff, новый файл) — таблица входов (почта с IDN и невидимыми, коды с дефисами и
  полноширинными цифрами, векторы TOTP) → ответ `domain.X` равен ответу платформы.
- `TestDomainOfCanonical` (emailaddr) → `Domain("a@xn--80ak6aa92e.xn--p1ai") = "xn--80ak6aa92e.xn--p1ai"`; на
  неканоническом вводе — пустая строка.
- Весь `go test ./internal/staff/...` — зелёный без правок тестов (в отчёт — число тестов до и после).

**Подсадка.** В `otp.Verify` разрешить шаг, равный `lastUsed` → `TestVerifyRejectsReplayedStep` и тест повтора кода
входа сотрудника (`internal/staff/internal/app/factor_test.go`) красные. В `emailaddr.Normalize` убрать отказ на
U+2800 → тест платформы и `staff/internal/domain/email_test.go` красные.

**Приёмка.** `git diff` по `staff/internal/domain/*_test.go` пуст; в `staff/internal/domain/{totp,codes,email}.go`
нет логики — только делегаты; `platform/otp`, `platform/emailaddr` не импортируют модули (`archtest`); комментарии
пакетов ссылаются на спеку identity §5.7 и спеку сотрудников §4.1.

**Ловушки.** Метки HMAC резервных кодов (`staff-recovery`) остаются в staff — в платформу уходит только формат кода,
не MAC. `golang.org/x/net/idna` и `x/text` уже в `go.mod` — новых зависимостей нет. Порядок проверок нормализации не
менять: ответ на мусорный ввод (`ok=false`) — часть поведения.

### Task 2: `flags.Rules` — только «везде» и зависимость флагов

**Цель.** Спека §4.2, §7.5: флаги способов входа бывают только «везде» (`GlobalOnly`), `auth_google` требует
`auth_apple` (`Requires`). Правила объявляет платформа рядом с каталогом; `Store.Update` под замком строк отвергает
нарушения; чтение считает зависимый флаг выключенным при выключенной зависимости (защита в глубину, R12).

**Риск:** A (гонка двух `PATCH`, взаимоблокировка, обход зависимости).

**Файлы.** Создать: `backend/internal/platform/flags/{rules.go,rules_test.go}`. Изменить:
`backend/internal/platform/flags/{flags.go,flags_test.go}`, `backend/internal/platform/flags/queries/flags.sql` (+
`flagsdb` — `sqlc generate`), `backend/internal/httpapi/admin/{flags.go,platform_flags_test.go}`,
`.claude/rules/backend-platform.md` (абзац «Флаги функций» — правила и порядок замка). `.claude/rules/contracts.md`
не трогать (строка о `GlobalOnly` есть с I-1).

**Интерфейсы.**
- `type Rule struct { GlobalOnly bool; Requires string }`; `func RuleOf(key string) Rule`; таблица правил —
  неэкспортируемая: `auth_apple`, `auth_passkeys`, `auth_apple_login`, `auth_google_login`, `auth_passkeys_login` —
  `{GlobalOnly: true}`; `auth_google` — `{GlobalOnly: true, Requires: "auth_apple"}`.
- Ошибки: `var ErrGlobalOnly = errors.New(…)`; `type RequiresError struct { Key, Requires string; Enabling bool }`
  (`Error()` — текст зависимости для `detail`: «auth_google требует auth_apple» / «сначала выключите auth_google»).
- `Store.Update(ctx, tx, key, version, globally, cities)` — сигнатура прежняя; новое: (1) непустой `CitySet(cities)`
  у `GlobalOnly` → `ErrGlobalOnly` до записи; (2) замок строк флага, его зависимости и зависимых флагов **одним
  запросом в порядке ключа**; (3) после применения: включённый флаг при выключенной зависимости или выключенная
  зависимость при включённом зависимом → `*RequiresError`.
- `Store.EnabledGlobally`, `Store.Enabled`, `Store.Require` — флаг с `Requires` включён, только если включена его
  зависимость.
- Запрос sqlc: `LockFlags :many` — `SELECT … FROM feature_flags WHERE key = ANY(@keys::text[]) ORDER BY key FOR UPDATE`.
- `httpapi/admin/flags.go`: `ErrGlobalOnly` → `422 platform.flag_global_only`; `*RequiresError` → `409
  platform.flag_requires`, `detail` = текст ошибки; проверка городов (`geo.Service.ExistingCityIDs`) у `GlobalOnly`
  не вызывается (отказ раньше).

**Тесты.**
- `TestRulesConsistent` → у каждого `Requires` есть правило с этим ключом; циклов нет; ключи — `^[a-z][a-z0-9_]*$`.
- `TestUpdateGlobalOnlyRejectsCities` → `auth_apple` с городами → `ErrGlobalOnly`, строка и версия не изменились.
- `TestUpdateRequiresBothDirections` → включить `auth_google` при выключенном `auth_apple` → `*RequiresError{Enabling: true}`;
  выключить `auth_apple` при включённом `auth_google` → `*RequiresError{Enabling: false}`; Apple, затем Google — успех.
- `TestUpdateRequiresConcurrent` → два параллельных `Update` в разных транзакциях («включить Google» и «выключить
  Apple», оба исходно допустимы): ровно один успех, второй — `*RequiresError` или `ErrStale`; 50 повторов — ни
  одного `40P01`, срок теста 10 с.
- `TestEnabledHonoursRequires` → `auth_google` включён в строке, `auth_apple` выключен (вставка владельцем схемы в
  обход `Update`) → `EnabledGlobally("auth_google") = false`.
- `httpapi/admin`: `TestUpdateFlagGlobalOnly` → `PATCH /v1/flags/auth_apple` с `enabled_city_ids` → `422
  platform.flag_global_only`, без события и аудита; `TestUpdateFlagRequires` → `409 platform.flag_requires`, `detail`
  непустой. До Task 5 строк шести флагов нет — тесты заводят их фикстурой (`INSERT` владельцем схемы в клон).

**Подсадка.** Убрать `ORDER BY key` и брать замки по одному → `TestUpdateRequiresConcurrent` ловит `40P01` (не ловит
за 50 повторов — усилить тест барьером, а не оставить вакуумным). Убрать проверку зависимости при выключении →
`TestUpdateRequiresBothDirections` красный.

**Приёмка.** Решение — под замком всех затронутых строк в одной транзакции; ни один путь не пишет флаг без правил;
identity не импортируется платформой; `TestFlagKeysMatchContract` не затронут (строк ещё нет).

**Ловушки.** Не брать замок двумя запросами (свой, затем зависимость) — порядок разный у двух `PATCH`.
`RequiresError` — не `httpx.ProblemError` (код ставит ручка admin, как у `ErrStale`). Тексты кодов в
`apps/admin/messages` уже есть (I-1, Task 4).

### Task 3: `platform/ipgeo` и поля `Problem`

**Цель.** (1) Город по IP для «Где вы вошли» и писем (спека §5.9): MMDB DB-IP «IP to City Lite», чтение
`maxminddb-golang/v2`, пустой путь — города нет. (2) Хвост m5 плана I-1: поля `reasons`, `methods`, `available_at`,
`blocked_until` в `httpx.Problem` (контракт их уже описывает) и хелперы, `Retry-After` для `identity.resend_too_soon`.

**Риск:** B.

**Файлы.** Создать: `backend/internal/platform/ipgeo/{ipgeo.go,ipgeo_test.go,testdata/city-test.mmdb}`,
`backend/tools/ipgeofixture/main.go` (генератор фикстуры на `mmdbwriter`). Изменить: `backend/go.mod`, `backend/go.sum`,
`backend/tools/{go.mod,go.sum}`, `backend/internal/platform/httpx/{problem.go,problem_test.go}`, `docs/versions.md`
(две строки), `NOTICE` (атрибуция DB-IP, CC BY 4.0).

**Интерфейсы.**
- `type Place struct { Names map[string]string; CountryCode string }`; `type Locator interface { Lookup(ip netip.Addr) (Place, bool) }`;
  `func Open(path string) (Locator, io.Closer, error)` (пусто — `None` и пустой `Closer`; файла нет — ошибка старта
  с путём); `var None Locator` — всегда `false`. Имена — `city.names` MMDB по ключам локали; город без имён —
  `false`. Частные, петлевые, link-local и нулевой адрес — `false` без чтения файла.
- `func (p Place) Name(locale string) (string, bool)` — имя на языке, иначе `en`, иначе `false`.
- `httpx.Problem`: `Reasons []string json:"reasons,omitempty"`, `Methods []string json:"methods,omitempty"`,
  `AvailableAt *time.Time json:"available_at,omitempty"`, `BlockedUntil *time.Time json:"blocked_until,omitempty"`;
  `(*Error).WithReasons(r ...string) *Error`, `WithMethods(m ...string) *Error`, `WithAvailableAt(t time.Time) *Error`,
  `WithBlockedUntil(t time.Time) *Error`, `WithRetryAfter(d time.Duration) *Error` (секунды вверх, только заголовок;
  если хелпер `Retry-After` уже есть — не дублировать). Каждый возвращает **новую** ошибку, как `WithAttemptsLeft`.

**Тесты.**
- `TestLookupFixture` → адрес из фикстуры → `Place{Names: {ru: "Ставрополь", en: "Stavropol"}, CountryCode: "RU"}`;
  вне фикстуры → `false`; `10.0.0.1`, `127.0.0.1`, `::1`, `fe80::1`, нулевой адрес → `false`.
- `TestOpenEmptyPathIsNone`; `TestOpenMissingFile` → ошибка содержит путь.
- `TestPlaceNameFallback` → `Name("de")` → английское имя; без `en` → `false`.
- `TestProblemNewFieldsJSON` → ключи `reasons`, `methods`, `available_at` (RFC 3339 UTC), `blocked_until`; пустые —
  без ключей.
- `TestProblemHelpersDoNotMutate` → `base := NewError(…)`; `base.WithReasons("common")` → у `base` поля нет.
- `TestProblemMatchesContract` (страж полей `Problem` против схемы обоих корней; есть — расширить, нет — создать в
  `internal/httpapi/public`) → JSON-ключи `httpx.Problem` = свойства `components.schemas.Problem` public и admin.
- `TestRetryAfterHeader` → `WithRetryAfter(61*time.Second)` → `Retry-After: 61`, в теле `retry_after` нет.

**Подсадка.** Вернуть из `WithReasons` тот же указатель → `TestProblemHelpersDoNotMutate` красный. Убрать фильтр
частных адресов → `TestLookupFixture` красный (в фикстуре нарочно есть запись на `10.0.0.0/8` с именем).

**Приёмка.** Фикстура < 50 КБ, воспроизводима (`go run ./ipgeofixture` из `backend/tools`), данные вымышленные;
`maxminddb-golang/v2` — последняя стабильная (спека §11.1 — v2.9.0; моложе двух недель — предыдущая с записью в
исключения `docs/versions.md`); `mmdbwriter` — только в `backend/tools/go.mod`.

**Ловушки.** Настоящий файл DB-IP в репозиторий не кладётся — только путь `API_IPGEO_DB` (Task 4). На Windows
открытый MMDB держит файл — `Close` в тестах через `t.Cleanup`. `Problem` правится только в Go: YAML заморожен.

### Task 4: конфиг и окружение identity (I-2)

**Цель.** Ключи §11.3, нужные этапам 2–5: кольцо секретов identity у `api` и `worker`, кольцо HMAC устройств,
издатель TOTP, путь MMDB, документы, файлы `.well-known`. Ключи Apple, Google, WebAuthn, App Attest, Play
Integrity и режим аттестации — план I-3.

**Риск:** B (отказ старта стенда после мержа, утечка секрета в файл).

**Файлы.** Изменить: `backend/internal/platform/config/{config.go,config_test.go,render_stand_test.go}`,
`backend/.env.example`, `backend/.env` (скриптом из scratchpad, значения не выводить), `render.yaml`; все
потребители конфига, которые иначе упадут на новых обязательных ключах (I6 ревью плана): `backend/cmd/api/main_test.go`
(базовое окружение тестов `run`), `backend/cmd/worker/{main_test.go,kinds_test.go}` и прочие тесты воркера с `run`,
`.github/workflows/image.yml` (smoke `migrate-then-api` — значения-заглушки колец), `apps/admin/e2e/env.ts` и
`scripts/e2e-admin.sh` (окружение воркера и api в e2e), `scripts/env-keys.sh` (`./task setup` заменяет
плейсхолдеры новых колец; одно значение `API_IDENTITY_SECRET_KEYS` и `WORKER_IDENTITY_SECRET_KEYS`, N8), любые другие места запуска `api`/`worker` (найти `git grep
-n "API_JWT_SEEDS\|WORKER_DATABASE_URL"`); ключ, его потребители, CI и e2e — одним коммитом.
`docs/deploy-dev.md` (env identity; шаги §11.2 п. 7–8 в части I-2; ротация кольца — «новый ключ первым →
`api identity rekey` → старый убрать только когда под ним нет резервных кодов (отчёт `rekey` — число
`user_recovery_codes` по `key_id`; коды — после перевыпуска владельцем) и не раньше 7 дней», спека §5.4, R28).

**Интерфейсы.**
- `config.API` += `Identity Identity` (вложенная структура, префикс `API_IDENTITY_`): `SecretKeys []string env:"SECRET_KEYS,required,notEmpty" envSeparator:","`,
  `DeviceKeys []string env:"DEVICE_KEYS,required,notEmpty" envSeparator:","`, `TOTPIssuer string env:"TOTP_ISSUER,required,notEmpty"`
  (бренд не финален — значение только в окружении). Отдельно, **полями самой `config.API`** (без префикса
  `IDENTITY_`, m13; спека §11.3): `IPGeoDB string env:"IPGEO_DB"` (`API_IPGEO_DB`),
  `LegalDocuments string env:"LEGAL_DOCUMENTS,required,notEmpty"` (`API_LEGAL_DOCUMENTS`; разбор —
  `identity.ParseLegalDocuments`, Task 7–8; `cmd/api` зовёт его до базы), `WellKnown WellKnown` (`API_WELLKNOWN_*`) —
  `AppleAppIDs []string env:"WELLKNOWN_APPLE_APP_IDS" envSeparator:","`, `Android string env:"WELLKNOWN_ANDROID"`
  (`<package>=<SHA-256>[;…]`).
- `config.WorkerIdentity{SecretKeys []string env:"IDENTITY_SECRET_KEYS,required,notEmpty" envSeparator:","}`
  (`WORKER_IDENTITY_SECRET_KEYS` — то же кольцо, что у api) — отдельный конфиг, как `config.Mail`: `cmd/worker` читает
  его только в режиме воркера, до базы; разовым командам (`events replay`, `geo import`) кольцо не нужно (решение
  контроллера 14.11).
- `render.yaml` (`wf-api`): `API_IDENTITY_SECRET_KEYS`, `API_IDENTITY_DEVICE_KEYS` — `sync: false` (значения
  генерирует и задаёт контроллер при выкатке, копия — файлом вне репозитория, U2; R10); `API_IDENTITY_TOTP_ISSUER =
  WF dev`, `API_LEGAL_DOCUMENTS` — заглушки версии `0.1.0` (U3, приведено к формату §3.16) со ссылкой-заглушкой (U3, U4) — значения в файле;
  `API_IPGEO_DB` и `.well-known` — без значения (города нет; 404, К4). Сервис воркера и
  `WORKER_IDENTITY_SECRET_KEYS` на стенде — Task 21 (U1).

**Тесты.**
- `TestAPIIdentityRequired` → без каждого из четырёх обязательных ключей — ошибка с именем ключа; с ними — поля
  заполнены, кольца — в порядке.
- `TestWorkerIdentityRequired` → без `WORKER_IDENTITY_SECRET_KEYS` — ошибка; `config.Worker` без кольца разбирается.
  `TestRunRequiresIdentityKeys` (`cmd/worker`) — режим воркера без кольца — отказ до базы; `geo import` без кольца проходит.
- `.env.example` — строки с комментариями, секреты — плейсхолдер `<base64-32-байта>`; есть общий страж «поле
  конфига ↔ шаблон» — его достаточно, нет — сверка руками в отчёте.
- `TestRenderYAMLIdentityEnv` (`render_stand_test.go`) → у `wf-api` ключи identity есть, кольца — `sync: false`;
  `TestRenderYAMLSecretsNotInFile` — список секретов расширен.

**Подсадка.** Снять `required` с `SECRET_KEYS` → `TestAPIIdentityRequired` красный; `sync: false` → `generateValue: true`
у `API_IDENTITY_SECRET_KEYS` → `TestRenderYAMLIdentityEnv` красный.

**Приёмка.** Каждый ключ — в конфиге, `.env.example`, локальном `.env`, `render.yaml`, CI и e2e; `go test -p 4
./cmd/... ./internal/platform/config/...` зелёный; smoke `image.yml` проходит с новыми ключами (проверка — чтением
шага и локальным запуском контейнерной команды не требуется: Docker на машине нет — сверка списка env в отчёте); в
отчёте нет значений; `docs/deploy-dev.md` перечисляет, что контроллер задаёт в панели Render до выкатки (U2);
`./task setup` (`scripts/env-keys.sh`) заменяет плейсхолдеры новых колец.

**Ловушки.** `API_IDENTITY_SECRET_KEYS` и `WORKER_IDENTITY_SECRET_KEYS` — одно кольцо (письма с кодами собирает
воркер): локальный `.env` получает одинаковое значение обоим ключам одним скриптом. admin-api кольцо identity не
получает (сброс 2FA в admin-api не шифрует и не расшифровывает, §5.5). `LEGAL_DOCUMENTS` с `|` и `,` — в
`.env.example` в кавычках.

## Этап 3. Схема (спека §15 п. 3)

### Task 5: миграции identity, строки флагов и enum `FeatureFlagKey`

**Цель.** Вся модель данных §5 одним заходом (этапы I-3 и I-4 миграций не добавляют): значение `pending_deletion`,
правки `users`, `sessions`, `role_assignments`, `captain_applications`, новые таблицы §5.3, представления §5.6,
строки шести флагов §4.1, таблица связи `staff_player_links` §7.6 (R8). Тем же коммитом — enum `FeatureFlagKey` и
подписи флагов (R4 плана I-1: `TestFlagKeysMatchContract` сверяет enum с таблицей на равенство).

**Риск:** A (миграция на стенде после мержа накатывается `wf-api` сама; данные; откат enum).

**Файлы.** Создать: `backend/migrations/0024_identity_status.sql`, `backend/migrations/0025_identity.sql` (номера —
следующие свободные в момент задачи, R11), `backend/migrations/identity_0025_test.go`. Изменить:
`backend/migrations/README.md` (порядок, решения, таблица гарантий базы), `contracts/openapi/admin/platform/schemas.yaml`
(enum `FeatureFlagKey` += `auth_apple`, `auth_google`, `auth_passkeys`, `auth_apple_login`, `auth_google_login`,
`auth_passkeys_login`), `apps/admin/messages/{ru,en}.json` (`flags.<ключ>.{title,note}` — шесть пар; у аварийных в
`note` — «авария: выключает вход всем»), сгенерированное `./task gen` (`contracts/openapi/admin.yaml`,
`backend/internal/httpapi/admin/oapi/*`, `apps/admin/src/api/gen/admin.d.ts`).

**Интерфейсы (схема).**
- `0024_identity_status.sql` — `-- +goose NO TRANSACTION`; `ALTER TYPE user_status ADD VALUE IF NOT EXISTS
  'pending_deletion' BEFORE 'deleted'`. `down` — пересоздание типа без значения по образцу `0020` (новый тип,
  `ALTER COLUMN … TYPE … USING`, переименование) с `RAISE EXCEPTION`, если есть строки `pending_deletion`.
- `0025_identity.sql`, одна транзакция; первым шагом — проверка данных: `users.email IS NULL` или непустая
  `sessions` → `RAISE EXCEPTION` с понятным текстом (как `0020`). Затем:
  - типы §5.3: `auth_code_purpose`, `auth_challenge_purpose` (все девять значений, включая WebAuthn и аттестацию —
    I-3 миграций не пишет), `legal_document`, `client_kind`, `device_platform`, `auth_method`, `identity_provider`;
  - `users` — ровно §5.2: `email SET NOT NULL`, `DROP CONSTRAINT users_contact_present`, `email_verified_at SET NOT
    NULL`, `version bigint NOT NULL DEFAULT 1`, `webauthn_handle bytea UNIQUE` + `CHECK (octet_length = 32)`,
    `last_login_at`, `deletion_requested_at`, `purge_at` + CHECK «заданы ⇔ `status = 'pending_deletion'`», индекс
    `(purge_at) WHERE purge_at IS NOT NULL`, `CHECK users_email_format` (каноническая ASCII-форма ≤ 254 байт, R24);
  - `sessions` — пересоздание колонками §5.3 (строка = вход): `id`, `user_id`, `device_id NULL` → `devices` (R18),
    `client_kind`, `auth_method`, `mfa_at`, `reauth_at`, `reauth_failures smallint NOT NULL DEFAULT 0`,
    `reauth_failed_at timestamptz NULL` (начало окна неудач свежего входа, R16 — сверх §5.3), `ip`,
    `ip_city`, `user_agent` (≤ 512), `app_version`, `created_at`, `last_seen_at`, `idle_expires_at`,
    `absolute_expires_at`, `revoked_at`, `revoked_reason` (CHECK по списку §5.3); индексы `(user_id) WHERE revoked_at
    IS NULL`, `(absolute_expires_at)`; старые `refresh_hash`, `family_id`, `parent_id`, `device_name`, `device_os`,
    `expires_at` и их индексы — удаляются;
  - таблицы §5.3 с ограничениями и индексами построчно: `credentials`, `auth_codes`, `auth_challenges`, `devices`,
    `device_accounts`, `refresh_tokens`, `user_mfa`, `user_recovery_codes`, `user_identities`, `passkeys`,
    `security_events`, `user_restrictions`, `consents`, `mfa_resets`;
  - `role_assignments`: `+ granted_by_staff_id uuid`, `+ application_id uuid REFERENCES captain_applications`,
    `+ revoked_by_staff_id uuid`, `+ revoke_reason text`, `DROP COLUMN granted_by`;
  - `captain_applications` — ровно §5.2 (телефон E.164, `reviewed_by_staff_id`, `reviewed_by_label`,
    `rejection_code` CHECK по §7.3, `comment` ≤ 1000, `closed_at`, `version`, `DROP reviewed_by`,
    `DROP rejection_reason`, CHECK длин и согласованности);
  - `staff_player_links (staff_id uuid PRIMARY KEY REFERENCES staff_accounts, user_id uuid NOT NULL UNIQUE REFERENCES users,
    created_by uuid NOT NULL, created_at timestamptz NOT NULL)` (§7.6);
  - представления §5.6: `identity_read_users` (`hidden` — `status IN ('pending_deletion','deleted')`, у удалённого —
    ник-заглушка), `identity_read_linked_accounts` (пары в обе стороны), `identity_read_captain_permits`,
    `identity_read_restrictions` (`lifted_at IS NULL AND (until IS NULL OR until > now())`); без почты, телефона, даты
    рождения;
  - `INSERT INTO feature_flags` шести ключей (`ON CONFLICT DO NOTHING`, версия 1): основные — `false`, аварийные
    `auth_*_login` — `true`; описание — по-русски для разработчика.
- `down` 0025 — обратный порядок, точная прежняя схема (включая `users_contact_present`, старые колонки `sessions`
  и индексы, `granted_by`, `reviewed_by`, `rejection_reason`), строки флагов удаляются.

**Тесты** (`identity_0025_test.go`, по образцу `staff_0020_test.go`, `geo_0021_test.go`).
- `TestIdentityMigrationRoundTrip` → `DownTo(0023)` → каталог (колонки, типы, ограничения, индексы, представления,
  функции) равен снимку до `0024`; снова `Up` → равен снимку после.
- `TestIdentityMigrationRefusesData` → строка `users` с `email IS NULL` (под версией `0023`) → `Up` падает с
  текстом; непустая `sessions` → то же; на стенде таблицы пусты.
- `TestPendingDeletionDownRefuses` → строка `pending_deletion` → `down` 0024 падает с текстом.
- `TestIdentityConstraints` — таблица случаев, каждый проверяет **имя** ограничения (23514/23505/23502): `auth_codes`
  приманка с `code_mac` → нарушение; второй живой код `(purpose, email)` → 23505; `attempts = 6`; шифротекст без
  `key_id`; `refresh_tokens` `successor_enc` без `successor_until`; `user_identities` `apple_refresh_enc` у `google`;
  `passkeys.credential_id` 15 байт; `security_events.kind = 'Bad'`; `mfa_resets` второй живой сброс → 23505,
  `executed_at` и `cancelled_at` оба → нарушение; `users.purge_at` при `status = 'active'` → нарушение;
  `users_email_format` на `Ivan@Mail.ru` → нарушение, на `deleted+<uuid>@invalid` → ок (R24); `captain_applications`
  `rejected` без `rejection_code`; `staff_player_links` вторая связь на того же игрока → 23505.
- `TestIdentityFlagsSeeded` → шесть строк, значения по §4.1, версия 1.
- `TestFlagKeysMatchContract` (есть, `internal/httpapi/admin`) — зелёный с новым enum.
- `sqlc`: значение `pending_deletion` видно в сгенерированном enum (проверка §18 — в отчёт).

**Подсадка.** Убрать из `down` восстановление `users_contact_present` → `TestIdentityMigrationRoundTrip` красный. Снять
частичный UNIQUE `mfa_resets` → случай «второй живой сброс» красный. Убрать один ключ из enum `FeatureFlagKey` →
`TestFlagKeysMatchContract` красный.

**Приёмка.** Колонки и ограничения — построчно со спекой §5.2–5.3; в новых таблицах нет `DEFAULT now()` и
`gen_random_uuid()`; `identity-freeze.test.mjs` зелёный (enum вне среза, R4 плана I-1); `contracts:breaking` чистый
(добавление значения enum ответа — `info`); README миграций — новые строки «защищено базой».

**Ловушки.** Новое значение enum нельзя использовать в той же транзакции — потому две миграции; CHECK
`purge_at ⇔ pending_deletion` живёт во второй. `sessions` пересоздаётся, но FK `auth_challenges.session_id` и
`refresh_tokens.session_id` (CASCADE) создаются после неё. sqlc — грамматика PG17. `./task gen` — pnpm: на время
задачи другие исполнители pnpm не запускают. Применённые миграции (`0003`) не правятся.

### Task 6: права ролей, владение, элемент sqlc

**Цель.** Права ролей §5.5 ветками CASE (умолчание `ELSE` дало бы полный DML всем — для секретов неприемлемо),
владение новых таблиц и представлений, `captain_applications` → `identity` (§2.3), элемент `sql` identity в
`sqlc.yaml` с якорем моделей (R5).

**Риск:** A (утечка секретов через права роли, стражи владения).

**Файлы.** Изменить: `backend/internal/platform/grants/{grants.sql,grants_test.go}`, `backend/internal/archtest/ownership.go`,
`backend/sqlc.yaml`. Создать: `backend/internal/identity/queries/models.sql`,
`backend/internal/identity/internal/store/identitydb/{db.go,models.go,models.sql.go}` (`sqlc generate`).

**Интерфейсы.**
- `grants.sql` — ветки по таблице §5.5 построчно, колоночные `SELECT (…)` и `UPDATE (meta_enc)` (К6) — по образцу
  существующей колоночной ветки `credentials`; `staff_player_links` подпадает под `staff\_%` (только `admin`) —
  проверить, не дописывать отдельно.
- `ownership.go`: `credentials`, `auth_codes`, `auth_challenges`, `devices`, `device_accounts`, `refresh_tokens`,
  `user_mfa`, `user_recovery_codes`, `user_identities`, `passkeys`, `security_events`, `user_restrictions`,
  `consents`, `mfa_resets`, четыре представления `identity_read_*` → `identity`; `captain_applications` →
  `identity`; `staff_player_links` → `staff`; новые типы — если страж их перечисляет.
- `sqlc.yaml`: элемент `queries: internal/identity/queries/`, `out: internal/identity/internal/store/identitydb`,
  `omit_unused_structs: true`, `emit_pointers_for_null_types: true`, **`emit_interface: false`** (R5: без общего
  `querier.go`, который меняли бы параллельные задачи), переопределения типов — как у соседей.
- `queries/models.sql` — якорь: по одному `SELECT * … LIMIT 0` (`:many`) на каждую таблицу identity, чтобы
  `models.go` содержал все модели с этой задачи и не менялся в задачах 8–19 (R5).

**Тесты** (`grants_test.go`, под ролями прода).
- `TestIdentityGrantsMatrix` — таблица «роль × таблица × операция → да/нет» по §5.5 целиком (каждая клетка), в том
  числе обратные: `admin UPDATE users` → нет (К2), `admin UPDATE sessions` → нет, `admin INSERT security_events` → да,
  `admin INSERT mfa_resets` → да, `api INSERT mfa_resets` → нет, `worker UPDATE (meta_enc) security_events` → да,
  `worker UPDATE (kind) security_events` → нет.
- `TestIdentitySecretsHidden` (§5.5, страж) → под `admin` `SELECT` из `credentials`, `user_mfa.secret_enc`,
  `auth_codes`, `user_identities`, `passkeys` → 42501; под `admin` `SELECT user_id, confirmed_at FROM user_mfa` → ок;
  под `worker` `SELECT password_hash FROM credentials`, `SELECT secret_enc FROM user_mfa` → 42501.
- `TestEverySchemaObjectHasOwner`, `TestSqlcQueriesCoveredByOwnershipGuard` (есть) — зелёные; `captain_applications` у
  `identity`.

**Подсадка.** Убрать ветку `credentials` (остаётся `ELSE` — DML всем) → `TestIdentitySecretsHidden` красный. Дать
`admin` `UPDATE` на `users` → `TestIdentityGrantsMatrix` красный. Переименовать представление в `identity_users` →
страж имён `<модуль>_read_*` красный.

**Приёмка.** Каждая клетка §5.5 — случай теста; ни одна новая таблица не получает права через `ELSE`; `models.go`
содержит модели всех таблиц identity; `go build ./...` зелёный.

**Ловушки.** `grants.sql` применяется и на `down`/`reset` — ветки для таблиц, которых нет на старой версии, не должны
падать (цикл по существующим объектам). Колоночные права и `REVOKE ALL` — порядок как в существующей ветке.
`emit_interface: false` — проверить, что стражей «у каждого элемента sqlc `emit_interface`» нет; есть — R5 вариант
«`querier.go` коммитит контроллер» (записать в отчёт).

### Task 7: домен identity и каталог событий

**Цель.** Чистые правила без БД и HTTP (спека §12 `internal/domain`, §13 строка `domain`) и каталог событий §8.1 в
части I-2 с golden-файлами payload — до сценариев, параллельно со схемой.

**Риск:** B (правила возраста, ника и сроков — основа ответов `too_young`, `nickname_invalid`, сроков сессий).

**Файлы.** Создать: `backend/internal/identity/internal/domain/{nickname.go,age.go,code.go,session_terms.go,legal.go,disposable.go,disposable.txt,device.go,useragent.go,security.go,password.go,tokens.go}`
и тесты рядом; `backend/internal/identity/events.go`, `backend/internal/identity/events_test.go`,
`backend/internal/identity/testdata/events/*.golden.json`.

**Интерфейсы.**
- Ник (§5.8): `func CheckNickname(s string) (normalizedInput string, reason NicknameProblem)` — NFKC, 3–20 символов,
  буквы, цифры, `_`, `.`, `-`, не начинается и не кончается знаком, двух знаков подряд нет, не из одних цифр,
  невидимые и управляющие — отказ, форма заглушки `deleted-…` — отказ; `NicknameProblem` = `""` | `invalid`;
  уникальность (`normalize_text`) — забота БД. `const NicknameChangeEvery = 30 * 24 * time.Hour`,
  `NicknameQuarantine = 90 * 24 * time.Hour`; `func DeletedNickname(id uuid.UUID) string` (`deleted-<8 hex>`).
- Возраст: `func AgeYears(birth time.Time, now time.Time, tz *time.Location) int` (дата рождения — полночь UTC, как `date` из pgx) —
  «сегодня» в таймзоне города; `func CheckBirthDate(birth, now) error` (не в будущем, ≤ 120 лет).
- Код из письма: `const CodeTTL = 15 * time.Minute`, `CodeAttempts = 5`, `MaxSends = 4`;
  `func NewEmailCode(rnd io.Reader) (string, error)` (6 цифр, равномерно); `func NormalizeEmailCode(s string) (string, bool)`
  (через `otp.NormalizeCode`); `func ResendAvailableAt(lastSent time.Time, sends int) time.Time` (паузы 60 → 120 →
  300 с после 1-й, 2-й, 3-й отправки).
- Сроки сессий (§3.7): `type ClientKind string` (`mobile`, `web`); `func SessionTerms(k ClientKind) (idle, absolute time.Duration)`
  (90/365 и 30/180 дней); `const RefreshRaceWindow = 15 * time.Second`, `MaxLiveSessions = 100`,
  `ReauthFresh = 10 * time.Minute`, `ReauthFailures = 5`, `ReauthFailuresWindow = 15 * time.Minute`,
  `ChallengeTTL` (`mfa` 5 мин, `deletion_restore` 10 мин), `DeletionGrace = 30 * 24 * time.Hour`,
  `MfaResetDelay = 7 * 24 * time.Hour`; `RevokedReason` — константы списка §5.3.
- Документы (§3.16): `type LegalDocs struct{…}`; `func ParseLegalDocuments(s string) (LegalDocs, error)` (формат
  `terms=2026-11-01|https://…,privacy=…`; оба документа обязательны, `https://`, версия 1–32 символа);
  `func (LegalDocs) Check(accepted []Accepted) error` (`ErrDocumentsOutdated`: нет документа, лишний, версия ≠
  текущей).
- Одноразовые домены: `disposable.txt` (вшитый `embed`, источник — список `disposable-email-domains`, лицензия CC0
  — проверить, §18; в шапке файла — источник и дата); `func IsDisposable(domain string) bool` (точное совпадение и
  родительские домены).
- Устройство: `func NormalizeAppVersion(s string) (string, bool)` (`1.4` → `1.4.0`, `1.4.0` — как есть, иное — `false`;
  хвост плана I-1); пределы строк `model` ≤ 64, `os_version` ≤ 32.
- UA (R25): `func ParseUserAgent(ua string) (browser, os string)` — узнаёт Chrome, Edge, Firefox, Safari, Opera,
  Samsung Internet и ОС Windows, macOS, iOS, Android, Linux; иначе пусто; без зависимостей.
- Безопасность: `type SecurityKind string` — константы видов §8.3 в части I-2 (`signup`, `login`, `login_failed`,
  `refresh_reuse`, `password_changed`, `password_reset`, `email_changed`, `mfa_enabled`, `mfa_disabled`,
  `mfa_failures`, `recovery_code_used`, `recovery_codes_regenerated`, `account_deletion_scheduled`,
  `account_restored`, `account_deleted`); метки HMAC: `LabelCode = "identity-code"`, `LabelRecovery =
  "identity-recovery"`, `LabelDevice = "identity-device"`, `LabelRateLimit = "identity-ratelimit"`.
- Пароль: `func PasswordWeakReason(err error) (string, bool)` — `password.Check` → `too_short | common | too_long`.
- Токены шагов: `func NewStepToken() (raw string, hash []byte, error)` (256 бит base64url, SHA-256).
- `identity/events.go`: имена событий I-2 (`EventUserRegistered`, `EventUserDeletionScheduled`,
  `EventUserDeletionCancelled`, `EventUserDeleted`, `EventUserEmailChanged`, `EventNicknameChanged`,
  `EventPasswordChanged`, `EventMfaChanged`, `EventSessionsRevoked`, `EventRestrictionsChanged`,
  `EventMfaResetScheduled`, `EventMfaResetCancelled`), агрегаты `user`, `mfa_reset`, payload-структуры §8.1 с `v = 1`
  и конструкторы `func UserRegistered(u UserRef, cityID uuid.UUID, method, platform string) events.Event` и т. д.
  (версия агрегата — аргумент). События I-3/I-4 (`identity_linked`, `passkey_*`, `captain_*`, `device_shared`,
  `city_changed`) — их планы.

**Тесты.**
- `TestCheckNickname` — таблица ≥ 30 случаев: границы 3/20 после NFKC, полноширинные, `a..b`, `.ab`, `ab-`, `123`,
  невидимые U+200B, U+2800, эмодзи, `deleted-0a1b2c3d`, кириллица с латиницей (формат ок — двойника ловит БД).
- `TestAgeYearsCityTZ` → день рождения «завтра по UTC, сегодня по Владивостоку» → возраст по городу; 29 февраля.
- `TestEmailCodeUniform` → 100 000 кодов: 6 цифр, ведущие нули есть, χ² по первой цифре в пределах.
- `TestResendSchedule` → 1-я отправка + 59 с → рано; + 60 с → можно; 4 отправки → больше нельзя.
- `TestSessionTerms`, `TestParseLegalDocuments` (пусто, без `https`, один документ, мусор → ошибки), `TestLegalCheck`,
  `TestIsDisposable` (`mailinator.com`, `sub.mailinator.com` → да; `gmail.com` → нет), `TestNormalizeAppVersion`,
  `TestParseUserAgent` (10 реальных UA), `TestPasswordWeakReason`.
- `TestEventPayloadsGolden` → каждый конструктор → JSON равен golden-файлу; `TestEventPayloadsNoPII` → обход полей
  payload: нет ключей `email`, `phone`, `birth`, `nickname`, `ip`.

**Подсадка.** Разрешить ник из одних цифр → `TestCheckNickname` красный. Считать возраст в UTC →
`TestAgeYearsCityTZ` красный. Добавить `email` в payload `user_registered` → `TestEventPayloadsNoPII` красный.

**Приёмка.** Пакет `domain` импортирует только стандартную библиотеку, `x/text`, `uuid`, `platform/{otp,emailaddr,password,events}`
— без БД и HTTP; все константы сроков — здесь, сценарии их не дублируют; список одноразовых доменов — с источником,
датой и лицензией в шапке `disposable.txt`; строка в `docs/versions.md` (раздел данных) — через отчёт в Task 20
(владелец `versions.md` в волне 1 — Task 3, m1).

**Ловушки.** `age_years()` в БД считает по `current_date` сервера — сценарии берут возраст из домена (часы + таймзона
города из `geo.CitySettings`), не из SQL. Ник: регистр и двойники — `normalize_text` БД (`0023`), домен их не
повторяет. Golden-файлы — LF.

## Этап 4. Ядро (спека §15 п. 4)

### Task 8: каркас модуля, устройство и выдача сессии

**Цель.** Общая основа всех сценариев, которую параллельные задачи 10–18 не трогают (R5): зависимости модуля,
сборка `cmd/api`, раскладка заглушек и `pendingOps` по файлам-владельцам, устройство (R2), выдача и отзыв сессии,
журнал безопасности, постановка письма, аудит, проверка свежего входа.

**Риск:** A (ключевой стык: сроки сессий, хранение токенов, HMAC устройства, `device_required`).

**Файлы.** Создать: `backend/internal/identity/identity.go`; `backend/internal/identity/internal/app/{app.go,devices.go,sessions.go,security.go,mail.go,audit.go,reauth.go}`
и тесты; `backend/internal/identity/internal/store/store.go`; `backend/internal/identity/queries/{devices,sessions,security}.sql`
(+ `identitydb/*.sql.go`); `backend/internal/identity/httpapi/{signup,login,sessions,password,account,deletion,mfa,mfa_reset,pending_i3,pending_i4}.go`
— **перенос** заглушек из `handler.go` по областям (Приложение 1), `handler.go` остаётся с `Handler`, `Deps`, `New`;
`backend/internal/httpapi/public/pending_{signup,login,sessions,password,account,deletion,mfa,mfa_reset,i3,i4}_test.go`.
Изменить: `backend/internal/httpapi/public/{handler.go,server.go,nostub_test.go}` (`pendingOps =
slices.Concat(pendingSignup, …)`), `backend/cmd/api/{main.go,main_test.go}`, `backend/internal/platform/audit/mask.go`
(если нужны слова: `install`, `token`, `restore` — проверить список).

**Интерфейсы.**
- **Сборка — только через корневой пакет** (I1 ревью плана: `identity/internal/*` из `cmd/*` и `internal/httpapi/*`
  не импортируется — правило Go; образец — `staff.Deps` и `staffadmin.New`). `identity.go`: `type Service interface
  { User(ctx context.Context, id uuid.UUID) (UserSummary, error) }`; `type UserSummary struct { ID, CityID uuid.UUID;
  Nickname, Status, Locale string }` (без ПД; `ErrUserNotFound`); `func New(pool *pgxpool.Pool) Service`.
- `identity.Deps` (корень, для `api`): `Pool *pgxpool.Pool`, `Clock clock.Clock`, `Secrets *keys.Ring`, `Devices
  *keys.Ring`, `Hasher PasswordHasher`, `Limiter ratelimit.Limiter`, `Risk *risk.Assessor`, `Human
  humancheck.Verifier`, `Geo geo.Service`, `Flags *flags.Store`, `IPGeo ipgeo.Locator`, `Legal LegalDocs`, `Tokens
  *token.Issuer`, `River *river.Client[pgx.Tx]` (только вставка: `queue.InsertConfig`), `Attestor Attestor`,
  `Providers ProviderReadiness`, `TOTPIssuer string`, `Log *slog.Logger`. `identity.WorkerDeps` (воркер): `Pool`,
  `Clock`, `Secrets`, `River`, `Catalog *i18n.Catalog`, `Log`. `identityadmin.Deps` (admin-api, без колец): `Pool`,
  `Clock`, `River`, `Log`. Корень же экспортирует `type LegalDocs = domain.LegalDocs` и `func ParseLegalDocuments(s
  string) (LegalDocs, error)` (обёртка домена), `type Attestor interface { Check(ctx context.Context, d DeviceInput)
  (AttestVerdict, error) }` с реализацией `AttestOff` (всегда «нет данных, не требовать»; I-3 заменит, R2), `type
  ProviderReadiness interface { Apple() bool; Google() bool; Passkeys() bool }` с `NoProviders` (все `false`; R4).
- Внутри `app` — два конструктора с разным обязательным: `app.New(identity-зависимости api) (*App, error)` и
  `app.NewMaintenance(pool, clock, secrets, river, log) (*App, error)` — для воркера и admin-api (методы анонимизации,
  исполнения сбросов, чистки и исполнитель сброса 2FA не требуют хэшера, токенов и `Geo`); отсутствие обязательного —
  ошибка конструктора. У `NewMaintenance` обязательны `pool`, `clock`, `river`, `log`; **`secrets` необязателен** (N3):
  admin-api кольца identity не получает (Task 4, «Ловушки»), а сброс 2FA пишет `security_events` без `meta_enc`
  (спека §5.4: `meta_enc` — только адрес для писем после смены почты и удаления; письмо сброса 2FA — по
  `mfa_resets.id` и `users.email`). `securityEvent` с адресом при `Secrets == nil` — ошибка `errNoSecrets` (500), не
  паника; страж `TestSecurityEventNoRing`. Воркер и `rekey` передают кольцо. Пакеты-сборщики (`identity/httpapi`,
  `identity/admin`, `identity/jobs`, `identity/rekey`) лежат под `identity/` и импортируют `internal/app` законно;
  **корень `identity` не импортирует ни `app`, ни сборщики** — `app` сам импортирует корень (коды, события,
  `identity.Deps`), образец `staff/internal/app/app.go` (N2).
- **Порядок замков** (I2): `users` → `sessions` → `refresh_tokens` → `devices` → `user_mfa`/`user_recovery_codes` →
  `mfa_resets` → `auth_codes`/`auth_challenges`. Каждый сценарий, меняющий аккаунт или его сессии, первым берёт
  `users … FOR NO KEY UPDATE` (функция `a.lockUser(ctx, tx, userID)`); `requireFresh`, Refresh, отзыв сессий,
  исполнение сброса 2FA — тоже. Порядок — в комментарии `app/app.go` и в `backend-identity.md` (Task 20).
  **Аккаунт по строке кода, вызова или токена** (N1, R30): `user_id` известен только из строки `auth_codes` /
  `auth_challenges` (`mfa_token`, `restore_token`) или `refresh_tokens` → `sessions` (R3-3) — поиск строки **без замка** → `lockUser(row.user_id)` → `FOR UPDATE`
  той же строки и перепроверка (не погашена, не отозвана, не истекла, `user_id` тот же; иначе — ответ «истёк»
  операции). Строка без `user_id` (код регистрации, приманка сброса) — без `lockUser`. Помощник `a.lockByRow(ctx, tx,
  find, lock)` — в `app/app.go`.
- **Порт хэшера** (R3-2): корень объявляет `type PasswordHasher interface { Hash(ctx context.Context, pw string)
  (string, error); Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error); VerifyDummy(ctx
  context.Context, pw string) }` (сигнатуры — по `platform/password`); `*password.Hasher` его реализует, `cmd/api`
  передаёт его. Тесты подставляют хэшер на канале (`TestCheckPasswordNoTxDuringArgon`, `TestConfirmResetTwoPhase`).
- **Точки синхронизации тестов** (N1, N6): `App.hooks` (неэкспортируемая структура, `nil` в проде) — `afterRowFound
  func(kind string)` (после поиска строки кода или вызова, до `lockUser`) и `afterFirstLock func(op string)` (после
  первого замка сценария). Ставит только тест пакета `app`; объявляет Task 8, вызовы ставят владельцы сценариев.
  `afterFirstLock` вызывается **в сценарии, сразу после первого оператора с замком** — не внутри `lockUser`: иначе
  при подсадке «`sessions` раньше `users`» хук сместится за второй замок и барьер не сведёт участников (R3-5).
- **Заглушки методов и тестовая сборка** (I9): Task 8 объявляет сигнатуры всех методов `App` из «Интерфейсов» задач
  10–18 в их файлах-владельцах с телом `errNotImplemented` и собирает настоящий identity (`App` на `dbtest`) в
  `testOptions` пакета `internal/httpapi/public` (`handler_test.go`, общий файл — у Task 8). Красный шаг TDD в
  задачах 10–18 — только через уже объявленную сигнатуру: пакет `app` у параллельных исполнителей всегда
  компилируется. Новая сигнатура — через контроллера.
- Клиент запроса: `type Client struct { Kind domain.ClientKind; IP netip.Addr; UserAgent string; Device *DeviceInput;
  BFFDevice string }`; `func ClientFrom(ctx context.Context, body *oapi.DeviceInfo) Client` — в `httpapi` (не в
  `app`): `peer.From(ctx).ViaBFF` → `web` (тело `device` игнорируется, метка — `peer.Info.Device`), иначе `mobile`.
  `func (a *App) requireDevice(c Client) error` → `422 identity.device_required` у мобильного без `device`.
- Устройство: `func (a *App) upsertDevice(ctx, tx pgx.Tx, c Client) (deviceID uuid.UUID, err error)` — `install_mac =
  MAC(текущий ключ, LabelDevice, platform‖install_id)`, поиск по всем ключам кольца, найденное по старому ключу —
  переписать текущим (§3.8); веб — `platform = web`, `install_mac` от метки BFF; веб без метки — `uuid.Nil`
  (сессия без устройства, R18); найденная строка берётся `FOR UPDATE` до перезаписи `install_mac` (два запроса со
  старым ключом не дают 23505), вставка — `ON CONFLICT DO NOTHING` и перечитывание; `app_version` —
  `domain.NormalizeAppVersion`, негодное — `NULL`; `last_seen_at`.
  `func (a *App) linkDevice(ctx, tx, deviceID, userID uuid.UUID, signedUp bool) error` (`device_accounts` upsert).
  `func (a *App) countSignup(ctx, tx, deviceID) error` — мобильное с `signups ≥ 2` → `403 identity.device_signup_limit`,
  иначе `signups + 1` под `FOR UPDATE` строки устройства (R2).
- Сессия: `type TokenPair struct { Access string; AccessExpires time.Time; Refresh string; RefreshIdleExpires time.Time; SessionID uuid.UUID }`; `type Issued struct { SessionID uuid.UUID; Tokens TokenPair }`;
  `func (a *App) issueSession(ctx, tx, userID uuid.UUID, deviceID uuid.UUID, c Client, method domain.AuthMethod, mfaAt *time.Time) (Issued, error)`
  — строка `sessions` (`reauth_at = now`, сроки по `SessionTerms`, `ip_city` — `IPGeo.Lookup` на языке аккаунта),
  `refresh_tokens` (SHA-256), `users.last_login_at`, при живых сессиях > 100 — отзыв старших (`revoked`, R17), access —
  `Tokens.Access(userID, sessionID)`. `func (a *App) revokeSessions(ctx, tx, userID uuid.UUID, except uuid.UUID, reason domain.RevokedReason) ([]uuid.UUID, error)`
  — пишет событие `identity.sessions_revoked` (≤ 100 id) и `users.version + 1` в той же tx (после `lockUser`).
  `func (a *App) revokeSession(ctx, tx, userID, sessionID uuid.UUID, reason domain.RevokedReason) (bool, error)` — одна
  сессия тем же путём события и версии (m11: выход, `refresh_reuse`, `revokeMySession` — только через него).
- Журнал и письма: `func (a *App) securityEvent(ctx, tx, e SecurityEvent) (uuid.UUID, error)` (`meta` без ПД,
  `meta_enc` — `Secrets.Seal(…, aad = id)`); `func (a *App) enqueueMail(ctx, tx, kind string, ref uuid.UUID) error` —
  `River.InsertTx(ctx, tx, mail.SendArgs{V: 1, Mail: kind, Ref: ref}, nil)`; константы видов писем I-2
  (`MailSignupCode = "identity.signup_code"` … полный список §8.2 без писем I-3/I-4) — в `app/mail.go`.
- Аудит: `func (a *App) auditUser(ctx, action string, userID uuid.UUID, before, after map[string]any) error`
  (`ActorUserID`, роль игрока) и `auditAnonymous(ctx, action string)`; константы действий §8.3 (часть I-2).
- Свежий вход: `func (a *App) requireFresh(ctx, tx, sessionID uuid.UUID) (sessionRow, error)` — `reauth_at` старше
  `ReauthFresh` или нет → `403 identity.reauth_required`; строка сессии — `FOR UPDATE`.
- `identityhttp.New(d identity.Deps) (Handler, error)` (внутри — `app.New`); нулевой `Handler` — каждая операция 501
  (заглушки и реализованные без `App`), как прежде.
- `public.Options.Identity identityhttp.Handler` (встраивание значением сохраняется); `cmd/api` собирает:
  кольца (`keys.NewRing`), `password.NewHasher(password.DefaultParams(), max(1, runtime.GOMAXPROCS(0)))` (R13), PoW
  (больше не выбрасывается — `humancheck.Verifier` в Deps), `risk.NewAssessor(limiter, risk.DefaultPolicies())`,
  `flags.NewStore(pool)`, `ipgeo.Open(cfg.IPGeoDB)`, `identity.ParseLegalDocuments` — до базы, клиент River только для
  вставки.

**Тесты.**
- `TestUpsertDeviceFindsByOldKey` → устройство создано под ключом K1, кольцо `[K2, K1]` → найдено то же, `key_id =
  K2`, `install_mac` пересчитан.
- `TestUpsertDeviceWeb` → `ViaBFF` с меткой → `platform = web`; без метки → `uuid.Nil`; `device` в теле от BFF
  игнорируется.
- `TestRequireDevice` → мобильный без `device` → `422 identity.device_required`; BFF без `device` → ок.
- `TestCountSignupLimit` → два параллельных `countSignup` на устройстве с `signups = 1` → ровно один успех, второй
  `403 identity.device_signup_limit`; веб — без счётчика.
- `TestIssueSessionTerms` → мобильный: `idle = now + 90 д`, `absolute = now + 365 д`; веб: 30/180; `reauth_at = now`;
  refresh в базе — только SHA-256 (сырое значение в базе не встречается); `TokenPair.session_id` = `sid` access.
- `TestIssueSessionCapsLive` → 100 живых + новая → старшая `revoked`, живых 100.
- `TestRevokeSessionsEvent` → 3 сессии, `except` текущей → 2 отозваны, событие с двумя id, `users.version + 1`.
- `TestRequireFresh` → `reauth_at = now − 11 мин` → `reauth_required`; `now − 9 мин` → ок.
- `TestEnqueueMailInTx` → откат tx → задачи `mail.send` нет; коммит → одна, аргументы `{v, mail, ref}` без адреса.
- `TestIPCityLocale` → фикстура `ipgeo`, аккаунт `en` → `ip_city = "Stavropol"`; `None` → `NULL`.
- `TestUpsertDeviceOldKeyConcurrent` → два параллельных `upsertDevice` по старому ключу → одна строка, ни одного 23505.
- `TestLockOrderNoDeadlock` (I2, N6) → параллельно «отзыв всех сессий аккаунта из сессии A» и «отзыв одной
  сессии B» с барьером `hooks.afterFirstLock`: первый участник ждёт второго до 200 мс (при верном порядке второй
  стоит на `users` — барьер истекает; при неверном оба держат первый замок и идут друг на друга) → ни одного
  `40P01`; 20 повторов. Подсадка не ловится — усилить барьер, как в Task 2.
- `TestSecurityEventNoRing` (N3) → `NewMaintenance` без кольца: запись без адреса проходит, с адресом — `errNoSecrets`.
- `TestAppStubsCompile` — не тест, а приёмка: `go vet ./internal/identity/...` зелёный с заглушками всех сигнатур.
- `cmd/api`: `TestRunRejectsBadLegalDocuments` → негодный `API_LEGAL_DOCUMENTS` → ошибка до базы;
  `TestNoStubOperations`/`TestPendingOpsExpire` — зелёные после раскладки (те же 50 операций в `pendingOps`).

**Подсадка.** Брать `sessions` раньше `users` в `revokeSession` → `TestLockOrderNoDeadlock` красный. Хранить сырой
refresh вместо хэша → `TestIssueSessionTerms` красный. Убрать `FOR UPDATE` в
`countSignup` → `TestCountSignupLimit` красный (под гонкой — повторить 20 раз). Брать `client_kind` из тела запроса →
`TestUpsertDeviceWeb` красный.

**Приёмка.** Поведение операций не изменилось (все 50 по-прежнему 501); файлы-владельцы — по R5 и Приложению 1;
`app` не импортирует `httpapi`, `oapi` — только в `httpapi` (типы `app` — свои); `cmd/api` не тянет `admin`
(`archtest`); `Deps` содержит всё, что нужно задачам 9–18 (дополнять `Deps` позже — только через контроллера).

**Ловушки.** `install_id` — не в логах и не в аудите. Один `*river.Client` вставки на процесс. Окно гонки refresh и
`SealSuccessor` — Task 11, не здесь. HMAC устройства — по `platform‖install_id`: один `ANDROID_ID` на iOS и Android
не склеивается.

### Task 9: завершение входа, пароль и второй фактор

**Цель.** Три примитива ядра входа (R6), которыми пользуются регистрация, вход, сброс пароля, отмена удаления, шаг
2FA и свежий вход: проверка пароля без раскрытия аккаунта, проверка второго фактора и завершение входа по статусу
аккаунта.

**Риск:** A (обход 2FA, перечисление, повтор TOTP, блокировка аккаунта перебором).

**Файлы.** Создать: `backend/internal/identity/internal/app/{login_core.go,factors.go,challenges.go,mfa_reset_hook.go}`
и тесты; `backend/internal/identity/queries/{factors,challenges}.sql`. `mfa_reset_hook.go` — заглушка, владелец
дальше — Task 18 (R20).

**Интерфейсы.**
- **argon2 — вне транзакции** (I10, образец `backend-staff.md` — `Verify` до tx): `func (a *App) checkPassword(ctx,
  userID uuid.UUID, pw string) (ok bool, seenHash string, err error)` — хэш читается пулом без tx, нет пароля у
  аккаунта → `Hasher.VerifyDummy` и `false`; `Verify` — без открытой tx и без занятого соединения. Затем вызывающий
  открывает tx, `lockUser` и сверяет, что хэш не изменился (`seenHash`; изменился — ответ как неверный пароль, R6).
  Пересчёт устаревших параметров — `Hash` вне tx, запись условным `UPDATE … WHERE password_hash = seenHash` в tx
  входа. `func (a *App) dummyPassword(ctx, pw string)` — для «нет аккаунта».
- `type SecondFactor struct { TOTP, Recovery string }`; `func (a *App) verifySecondFactor(ctx, tx, userID uuid.UUID, f SecondFactor) (method string, err error)`
  — ровно одно поле (иначе `400 validation.failed`, поле `body`, R11 плана I-1); TOTP — `otp.Verify` с
  `last_used_step` под `FOR UPDATE` строки `user_mfa`, запись шага; резервный код — `MAC(key_id строки,
  LabelRecovery, userID‖норм. код)`, `used_at = now` в tx входа; неудача → `user_mfa.failures + 1`, пятая подряд — `security_events (mfa_failures)` и письмо, не чаще раза
  в сутки (`failures_notified_at`); успех — `failures = 0`; резервный код → письмо `recovery_code_used` (осталось N).
  Неудача возвращает `errFactorInvalid` — код ошибки ставит вызывающий (`mfa_invalid`, `reauth_invalid`).
- `type LoginOutcome struct { Status string; Issued *Issued; MfaToken string; MfaExpires time.Time; Methods []string;
  ResetAt *time.Time; RestoreToken string; RestoreExpires, PurgeAt time.Time; User UserBrief }`;
  `func (a *App) finishLogin(ctx, tx, u userRow, c Client, deviceID uuid.UUID, method domain.AuthMethod, mfaDone bool) (LoginOutcome, error)`
  — порядок §3.2 и §3.11: `banned` → `403 identity.account_blocked` (+`blocked_until` из ограничения бана, нет —
  бессрочно); 2FA включена и `!mfaDone` → `mfa_required` (`auth_challenges purpose = mfa`, 5 мин, 5 попыток,
  `data` — способ первого шага, `device_id` (UUID строки `devices`, не `install_id`), `client_kind`; `methods` — `totp`, `recovery_code`, `passkey` при ключах и флаге
  `auth_passkeys_login`; `reset_at` — живой `mfa_resets.execute_at`); `pending_deletion` → `deletion_pending`
  (`restore_token`, 10 мин, `deletion_restore`; `data` — способ входа, `mfa_done`, `device_id`, `client_kind`, m2) без
  сессии; иначе `issueSession` (`mfa_at = now` при `mfaDone`) +
  `linkDevice` + `security_events (login)`; `mfaDone` → `a.onSecondFactorLogin(ctx, tx, userID)` (хук Task 18).
  `deleted` сюда не доходит (вызывающий отвечает как «нет аккаунта»). У операций, в контракте которых нет
  `account_blocked` (`completeMfa`, `confirmPasswordReset`), бан между шагами даёт `410` их токена или кода (R29):
  `finishLogin` принимает флаг `blockedAsExpired`.
- `func (a *App) takeChallenge(ctx, tx, purpose, raw string) (challengeRow, error)` — по SHA-256 приёмом R30 (N1):
  поиск без замка → `hooks.afterRowFound` → `lockUser(user_id)` → `FOR UPDATE` строки и перепроверка; истёк, погашен
  или `attempts ≥ 5` → `errChallengeGone`; `func (a *App) failChallenge(…) (attemptsLeft int, err error)`.
- `mfa_reset_hook.go`: `func (a *App) onSecondFactorLogin(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error { return nil }`.

**Тесты.**
- `TestCheckPasswordNoAccountCostsLikeReal` → медиана 50 вызовов `dummyPassword` и `checkPassword` с неверным
  паролем на реальном argon2 расходится < 15 % (как `TestVerifyDummyCostsLikeVerify`; `// serial: порог реального
  времени`).
- `TestCheckPasswordRehash` → хэш со старыми параметрами → после верного пароля в базе новые параметры; смена пароля
  между `Verify` и записью → пересчёт не затирает новый хэш.
- `TestCheckPasswordNoTxDuringArgon` (I10, N9) → тестовый хэшер, чей `Verify` ждёт канала; 8 параллельных
  `checkPassword` на своём пуле теста; когда все 8 внутри `Verify` (счётчик), `pool.Stat().AcquiredConns() = 0`
  (хэш прочитан до `Verify`, соединение возвращено); затем канал открыт — все 8 завершились. Без реального времени
  и порогов.
- `TestVerifyTOTPReplay` → тот же код дважды → второй `errFactorInvalid`; код предыдущего шага после принятого
  текущего → отказ.
- `TestVerifyRecoveryBurns` → код принят, повтор → отказ; код с ключом вне кольца → отказ без 500.
- `TestVerifyBothFactorsRejected` → `TOTP` и `Recovery` вместе → `validation.failed`.
- `TestMfaFailuresMailOncePerDay` → 5 неудач → одно письмо `mfa_failures`; ещё 5 в тот же день → письма нет;
  успех между ними обнуляет счётчик.
- `TestFinishLoginBranches` — таблица: `banned` (с ограничением до даты и бессрочно), 2FA без `mfaDone`,
  `pending_deletion` (с 2FA — сначала `mfa_required`, после `mfaDone` — `deletion_pending`), `active` → сессия;
  `suspended` → сессия.
- `TestMfaChallengeMethods` → без ключей — `[totp, recovery_code]`; с ключом и флагом — `+passkey`; аварийный флаг
  выключен — без `passkey`.
- `TestTakeChallengeAttempts` → 5 неудач → `errChallengeGone`; повторное использование погашенного — тоже.
- `TestTakeChallengeLockOrder` (R3-4, TDD приёма `lockByRow`) → `takeChallenge` встаёт на `hooks.afterRowFound`;
  синтетический второй участник — tx теста: `lockUser` → `UPDATE auth_challenges` той же строки → коммит; затем
  `takeChallenge` продолжается → ни одного `40P01`, вызов видит перепроверенную строку. Подсадка: `FOR UPDATE` строки
  до `lockUser` → `40P01`, тест красный (барьер участника — до 2 с).

**Подсадка.** В `finishLogin` проверять `pending_deletion` раньше 2FA → `TestFinishLoginBranches` красный (удаление
выдало бы токен восстановления без второго фактора). Не писать `last_used_step` → `TestVerifyTOTPReplay` красный. Читать хэш в открытой tx и держать
её до `Verify` → `TestCheckPasswordNoTxDuringArgon` красный (N9).

**Приёмка.** Ни один путь не создаёт сессию в обход `finishLogin`; неудачи второго фактора не закрывают аккаунт
(спека §3.3 п. 3); письма — задачей в той же tx; `mfa_token`/`restore_token` в базе — только SHA-256.

**Ловушки.** Сбой argon2 из-за семафора (ctx отменён) — 500, не `invalid_credentials`. Неудача (счётчики попыток,
`user_mfa.failures`, письмо) обязана **сохраниться**: вызывающий коммитит tx с неудачей и возвращает ошибку после
коммита (образец — `withFactor` staff). Это правило фиксируется в коде комментарием
и тестом `TestFactorFailurePersists`.

### Task 10: регистрация по почте, коды из письма, ник, документы

**Цель.** Спека §3.1, §3.14, §3.16 (`getLegalDocuments`), §5.8 (`checkNickname`), §9.1: регистрация с кодом и
приманкой; ни ответ, ни время не раскрывают занятую почту. Операции: `checkNickname`, `startSignup`,
`confirmSignup`, `resendAuthCode`, `getLegalDocuments`.

**Риск:** A (перечисление, гонки почты и ника, перебор кода).

**Файлы.** Создать: `backend/internal/identity/internal/app/{codes.go,signup.go,nickname_check.go}` и тесты,
`backend/internal/identity/queries/{codes,signup}.sql`. Изменить (владелец): `backend/internal/identity/httpapi/signup.go`,
`backend/internal/httpapi/public/pending_signup_test.go` (пустеет).

**Интерфейсы.**
- Коды (общие для Task 13): `func (a *App) startCode(ctx, tx, p StartCode) (CodeSent, error)` (`purpose`, почта,
  `user_id?`, `decoy`, `payload` для `signup`); один живой код на `(purpose, email)` — прежний `revoked_at = now`;
  `code_mac = MAC(cur, LabelCode, id‖код)`, `code_enc = Seal(код, aad = id)`, `payload_enc = Seal(json, aad = id)`;
  приманка — без `code_mac/code_enc/payload_enc`; лимит `ratelimit.EmailKey("auth_send", MAC(cur, LabelRateLimit,
  purpose‖email))` — 5/ч одинаково для приманки (`429 ratelimit.exceeded`).
  `func (a *App) checkCode(ctx, tx, purpose string, codeID uuid.UUID, code string, mode CodeMode) (codeRow, error)`,
  `CodeMode` — `CodeConsume | CodeVerifyOnly` (N7; сигнатура объявляется Task 8 сразу с режимом) — строка приёмом
  R30 (N1): поиск без замка → `hooks.afterRowFound("code")` → `lockUser(user_id)`, если `user_id` есть → `FOR UPDATE`
  и перепроверка; истёк, погашен, отозван или `attempts = 5` → `410 identity.code_expired`; неверный (приманке
  совпасть нечему) → `attempts + 1`, пятая гасит → `410`, иначе `400 identity.code_invalid` + `attempts_left`
  (неудача коммитится, Task 9); успех в `CodeConsume` — `consumed_at`, `code_enc = NULL`; в `CodeVerifyOnly` — без
  записей (погашение — `consumeCode(ctx, tx, codeID)` вызывающего, Task 13). Приманка смены почты несёт `user_id`
  вызывающего (I7) и берёт `lockUser` как настоящий код; приманка без `user_id` (регистрация, сброс) — без замка
  аккаунта.
  `func (a *App) ResendCode(ctx, codeID uuid.UUID) (CodeSent, error)` — пауза `ResendAvailableAt` → `429
  identity.resend_too_soon` + `Retry-After`; `sends = 4` или код закрыт → `410`; приманка — те же паузы без письма,
  кроме `signup_account_exists` (письмо владельцу один раз на старт — R26).
- `func (a *App) StartSignup(ctx, in SignupInput, c Client) (CodeSent, error)` — порядок §3.1 п. 2 (тест
  `TestStartSignupOrder` идёт в нём же, m8): валидация
  (`emailaddr.Normalize` → `422 validation.failed` по `email`; `password.Check` → `422 identity.password_weak` +
  `reasons`; `CheckNickname` → `422 identity.nickname_invalid`; дата рождения), документы (`Legal.Check` → `422
  identity.documents_outdated`), `requireDevice`, город (`Geo.CitySettings` видимость не фильтрует — сценарий сам
  проверяет `Status` города и включённость страны, m9; нет, скрыт или страна выключена → `422
  identity.city_not_found`), возраст в таймзоне города → `422 identity.too_young` (ничего не сохраняется), ник
  свободен (`users`, `reserved_nicknames`, живой карантин) → `409 identity.nickname_taken`, одноразовый домен → `422
  identity.email_disposable`, лимит устройства (только чтение, счётчик — при подтверждении) → `403
  identity.device_signup_limit`, риск `risk.Signup` по подсети / вердикт `Attestor` → `humancheck.Require`; затем
  хэш пароля **в обеих ветках**; почта свободна → код + письмо `signup_code`; занята (в том числе
  `pending_deletion`) → приманка + письмо `signup_account_exists` (для `pending_deletion` — текст «войдите, чтобы
  отменить удаление»).
- `func (a *App) ConfirmSignup(ctx, in ConfirmInput, c Client) (LoginOutcome, error)` — `checkCode(CodeVerifyOnly)`; ник
  перепроверяется: занят → `409 identity.nickname_taken` **без** траты попытки и без погашения кода (новый `nickname` —
  в том же вызове); код гасит `consumeCode` в tx создания аккаунта;
  одна tx: `users` (`email_verified_at = now`, `version = 1`), `credentials`, `consents` (версии из `payload`,
  `client_kind`), `upsertDevice` + `countSignup` + `linkDevice(signed_up)`, `issueSession` (`auth_method =
  password`), событие `identity.user_registered`, аудит, `security_events (signup)`; гонка почты (23505 по
  `users_email_key`) → `409 identity.email_taken`, ника → `409 identity.nickname_taken`. Ответ `201 AuthResult`
  (`new_user = true`).
- `func (a *App) CheckNickname(ctx, nick string) (available bool, reason string)` — `invalid | taken | reserved`.
- `func (a *App) LegalDocuments() []LegalDocument`.
- httpapi: `getLegalDocuments` → `Cache-Control: public, max-age=300`; `startSignup`/`resendAuthCode` → `202
  CodeSent` с `Cache-Control: private, no-store`; `confirmSignup` → `201`, `Cache-Control: no-store`.

**Тесты.**
- `TestStartSignupDecoyIndistinguishable` → свободная и занятая почта: одинаковые статус, тело (кроме значений id и
  времени), заголовки; оба `auth_codes` с одним `expires_at`-шагом; письма — `signup_code` и
  `signup_account_exists`.
- `TestStartSignupTiming` (`// serial: порог реального времени`) → медиана 50 «свободна / занята» на реальном argon2,
  расхождение < 15 % (§9.1).
- `TestStartSignupOrder` — таблица кодов в порядке «Интерфейсов»: `validation.failed` (почта), `password_weak`
  (+`reasons`), `nickname_invalid`, `documents_outdated`, `device_required`, `city_not_found` (нет, скрыт, страна
  выключена), `too_young` (ничего не сохранено: ни кода, ни письма), `nickname_taken` (резерв и карантин чужого ника),
  `email_disposable`, `device_signup_limit`, `humancheck.required` (риск подсети).
- `TestConfirmSignupHappy` → пользователь, пароль, согласия, устройство (`signups = 1`, `signed_up`), сессия,
  событие, аудит, `201`.
- `TestConfirmSignupAttempts` → 4 неверных → `400` с `attempts_left` 4…1; пятая → `410`; верный после — `410`;
  приманка — те же ответы.
- `TestConfirmSignupNicknameRace` → ник занят между стартом и подтверждением → `409`, попытка не потрачена, повтор с
  новым ником → `201`.
- `TestConfirmSignupEmailRace` → два кода на одну почту (второй старт гасит первый: подтверждение первого → `410`);
  две параллельные регистрации разных кодов с одной почтой через разные `purpose`-строки невозможны — проверить
  23505 на прямой вставке → `409 identity.email_taken`.
- `TestCheckCodeLockOrder` (R3-4, TDD приёма `lockByRow`) → как `TestTakeChallengeLockOrder`, второй участник —
  `lockUser` → `UPDATE auth_codes … revoked_at`; итог — `410 code_expired` без `40P01`; подсадка та же. Интеграционные
  стражи с настоящими сценариями — Task 20.
- `TestResendSchedule` → 60/120/300 с, пятая отправка → `410`, рано → `429` + `Retry-After`; приманка — так же.
- `TestEmailSendLimit` → 6-й старт за час на одну почту (существующую и нет) → `429 ratelimit.exceeded` одинаково.
- `TestCheckNickname` → `invalid`, `taken` (двойник латиница/кириллица), `reserved`, свободный.
- HTTP: `TestSignupHTTP` → контракт, анонимность, `x-rate-limit` `auth_send`/`auth`, `Cache-Control`.

**Подсадка.** Не хэшировать пароль в ветке «почта занята» → `TestStartSignupTiming` красный. Тратить попытку при
занятом нике → `TestConfirmSignupNicknameRace` красный. Проверять возраст после создания кода →
`TestStartSignupOrder` (`too_young` — код создан) красный.

**Приёмка.** Ответ `202` одинаков во всех ветках; письма уходят задачей; в аргументах задачи и событиях нет почты;
приманка ведёт счётчики как настоящий код; `pendingSignup` пуст.

**Ловушки.** `payload_enc` хранит **хэш** пароля, не пароль. Код — `crypto/rand`. Почта сравнивается в канонической
форме (`citext` + `emailaddr`). Таймзона города — из `CitySettings`, не из `users`.

### Task 11: вход, обновление токенов, выход, `SessionLoader`

**Цель.** Спека §3.2, §3.7 (refresh), §6.1: вход по почте и паролю, ротация refresh с детектом повтора и окном
гонки, выход, загрузчик сессии в `cmd/api`. Операции: `login`, `refreshSession`, `logout`.

**Риск:** A.

**Файлы.** Создать: `backend/internal/identity/session_loader.go` (+ тест),
`backend/internal/identity/internal/app/{login.go,refresh.go,logout.go}` и тесты, `backend/internal/identity/queries/{login,refresh,loader}.sql`.
Изменить (владелец): `backend/internal/identity/httpapi/login.go`, `backend/internal/httpapi/public/pending_login_test.go`,
`backend/cmd/api/main.go` (одна строка загрузчика).

**Интерфейсы.**
- `func (a *App) Login(ctx, email, pw string, c Client) (LoginOutcome, error)` — лимиты: ключ
  `EmailKey("login", MAC(…, LabelRateLimit, "login"‖email))` (форма R15) 30/ч одинаково для несуществующих; `risk.LoginFailed` по почте
  и IP → `humancheck.Require`; `requireDevice`; нет аккаунта, `deleted`, нет пароля, неверный → `400
  identity.invalid_credentials` (`dummyPassword` / `checkPassword`), неудача: `risk.Record` по почте и IP, для
  существующего — `security_events (login_failed)` и аудит `identity.login_failed` от `anonymous` без `object_id`;
  верный → `finishLogin(method = password)`.
- `func (a *App) Refresh(ctx, raw, installID string, c Client) (TokenPair, error)` (тип `app`, не `oapi`) — по SHA-256
  приёмом R30 «аккаунт по строке» (R3-3): строка `refresh_tokens` и её сессия — без замка → `hooks.afterRowFound("refresh")`
  → `lockUser(user_id)` → `FOR UPDATE` сессии и токена с перепроверкой (на ветке повтора порядок «сессия → `users`»
  дал бы `40P01` с `deleteAccount`); токен не использован → ротация (новый токен, `used_at`, `successor_id`, `successor_enc =
  token.SealSuccessor(raw, new)`, `successor_until = now + 15 с`), продление `idle_expires_at` (не дальше
  `absolute_expires_at`), `last_seen_at` сессии и устройства, новый access; использован и `now < successor_until` и
  устройство совпало (HMAC `platform‖install_id` мобильного — платформа из строки `devices` сессии, m10 — или метка
  BFF) → тот же преемник (`token.OpenSuccessor`); иначе
  повтор → отзыв сессии (`refresh_reuse`), `security_events`, письмо `session_revoked_suspicious`, ответ `401
  identity.refresh_invalid`. Неизвестный, истёкший, отозванный, аккаунт не `active|suspended` → `401`.
- `func (a *App) Logout(ctx, sessionID uuid.UUID) error` — `revoked_reason = logout`, событие; уже отозванная —
  `204` (идемпотентно).
- `func NewSessionLoader(pool *pgxpool.Pool, c clock.Clock) auth.SessionLoader` (корневой пакет) — один запрос:
  сессия + аккаунт + действующие ограничения + живые допуски капитана → `*auth.Principal{UserID, SessionID,
  DeviceID, Status, Restrictions, Roles}`; отозвана, `idle`/`absolute` истекли, статус `banned|pending_deletion|deleted`
  → `auth.ErrSessionInvalid`. `cmd/api`: `auth.NewCachedLoader(identity.NewSessionLoader(pool, clock.System), 5*time.Second, …)`.

**Тесты.**
- `TestLoginInvalidCredentialsUniform` → нет почты / нет пароля / неверный / `deleted` → один код, тело и заголовки.
- `TestLoginTiming` (`// serial`) → медиана 50 «нет почты» против «неверный пароль» < 15 %.
- `TestLoginBlocked` → `banned` с паролем → `403` + `blocked_until`; неверный пароль у `banned` → `400`.
- `TestLoginRiskRequiresPoW` → 5 неудач по почте → следующий вход без решения PoW → `403 humancheck.required`.
- `TestLoginEmailLimitUniform` → 31-я попытка за час по несуществующей и существующей почте → `429`.
- `TestRefreshRotates` → новый refresh ≠ старый, старый в окне с того же устройства → тот же преемник; после окна →
  отзыв сессии, `401`, письмо, `security_events (refresh_reuse)`.
- `TestRefreshRaceOtherDevice` → повтор в окне с другим `install_id` → отзыв, `401`.
- `TestRefreshConcurrent` → два параллельных refresh одного токена с одного устройства → оба `200`, одна пара.
- `TestRefreshIdleExtendsWithinAbsolute` → продление не превышает `absolute_expires_at`; после абсолютного → `401`.
- `TestSessionLoader` — таблица: живая → принципал с ограничениями и допусками; отозванная, просроченная,
  `banned`, `pending_deletion` → `ErrSessionInvalid`; кэш 5 с: отзыв действует через ≤ 5 с (clocktest).
- HTTP: `TestLoginHTTP`, `TestRefreshHTTP` (`no-store`), `TestLogoutHTTP` (с замком, `Idempotency-Key`, `204`);
  `TestAccessAfterLogout` → запрос с access после выхода → `401` в пределах кэша.

**Подсадка.** Сравнивать устройство окна гонки только по `sid` → `TestRefreshRaceOtherDevice` красный. Пропускать
`dummyPassword` при «нет почты» → `TestLoginTiming` красный. Убрать проверку статуса в загрузчике →
`TestSessionLoader` (`banned`) красный.

**Приёмка.** Повтор вне окна всегда гасит сессию; refresh в ответе только `no-store`; в журнале неудачного входа нет
почты и `object_id`; `auth.NoSessions` в `cmd/api` больше не используется.

**Ловушки.** `pending_deletion` при верном пароле — ответ `deletion_pending`, а не `invalid_credentials` (знает пароль
— знает и статус). Ротация продлевает только `idle`. Время окна — часы приложения, не базы.

### Task 12: «Где вы вошли», способы входа, `.well-known`

**Цель.** Спека §3.7 (список и отзыв), §4.4 (`getAuthMethods`), §9.4/К4 (`.well-known` до веба). Операции:
`listMySessions`, `revokeMySession`, `revokeOtherSessions`, `getAuthMethods`.

**Риск:** B.

**Файлы.** Создать: `backend/internal/identity/internal/app/{sessions_list.go,methods.go}` и тесты,
`backend/internal/identity/httpapi/wellknown.go` (+ тест). Изменить (владелец): `backend/internal/identity/httpapi/sessions.go`,
`backend/internal/httpapi/public/{pending_sessions_test.go,handler.go}` (монтаж `/.well-known/*` до маршрутизатора
контракта, как пробы, R14), `backend/cmd/api/main.go` (передать `WellKnown`), `.claude/rules/backend-http.md` (строка
о `.well-known` вне контракта).

**Интерфейсы.**
- `func (a *App) ListSessions(ctx, userID, current uuid.UUID) ([]SessionView, error)` — живые, по `last_seen_at`
  убыв.: устройство (`platform`, `model`, `os_version`, `app_version`), `browser` (веб — `ParseUserAgent`),
  `auth_method`, `city` (`ip_city`), `created_at`, `last_seen_at`, `current`; IP не отдаётся.
- `func (a *App) RevokeSession(ctx, userID, sessionID uuid.UUID) error` — чужая или нет → `404
  identity.session_not_found`; `revoked_reason = revoked`; `func (a *App) RevokeOthers(ctx, userID, current uuid.UUID) (int, error)`
  (`revoked_others`).
- `func (a *App) AuthMethods(ctx) (AuthMethodsView, error)` — `password: true`; `apple/google {signup, login}`,
  `passkeys {register, login}` = флаг (`Flags.EnabledGlobally` — с учётом `Requires`, Task 2) **и**
  `Providers.Apple()/Google()/Passkeys()` (R4); `Cache-Control: public, max-age=60`.
- `func WellKnown(appleAppIDs []string, android string) http.Handler` — `GET /.well-known/apple-app-site-association`
  (`{"webcredentials": {"apps": [...]}}`) и `GET /.well-known/assetlinks.json` (`delegate_permission/common.get_login_creds`,
  `sha256_cert_fingerprints`), `Content-Type: application/json`, без редиректов; пустой конфиг — `404`; иное — `404`;
  `public.Options.WellKnown http.Handler`.

**Тесты.**
- `TestListSessionsShape` → две сессии (мобильная и веб) → поля, `current`, `browser` из UA, без IP; отозванные не
  видны.
- `TestRevokeSessionIDOR` → чужая сессия → `404`; своя → `204`, повтор → `404`.
- `TestRevokeOthers` → 3 сессии → `{revoked: 2}`, текущая жива, событие `sessions_revoked`.
- `TestAuthMethodsFlagsAndProviders` → флаги выключены → всё `false`; флаги включены, провайдеров нет → `false`;
  `auth_google` включён при выключенном Apple → `google.signup = false`.
- `TestWellKnown` → JSON по конфигу, `Content-Type`, `404` на пустом конфиге, нет 3xx; путь вне контракта не
  проходит валидатор (как `/docs`).
- HTTP: `TestSessionsHTTP` (замок, `Idempotency-Key` у DELETE и POST), `TestAuthMethodsHTTP` (анонимно, кэш).

**Подсадка.** Отдавать IP в `SessionView` → `TestListSessionsShape` красный. Не проверять владельца в
`RevokeSession` → `TestRevokeSessionIDOR` красный.

**Приёмка.** Ответ «чужая» и «нет» неотличим; `.well-known` не в контракте и не под лимитом `auth`; правило
`backend-http.md` обновлено.

**Ловушки.** Текущая сессия при `revokeMySession` своего `id` — тоже отзывается (клиент выходит). Пустой
`app_version` у веба — `null`, не пустая строка.

### Task 13: сброс пароля, свежий вход, смена пароля и почты

**Цель.** Спека §3.4, §3.9, §3.10, §3.13. Операции: `startPasswordReset`, `confirmPasswordReset` (целиком,
включая ветку 2FA — примитивы есть с Task 9, R23), `reauthenticate`, `changePassword`, `startEmailChange`,
`confirmEmailChange`.

**Риск:** A (обход 2FA сбросом по почте, перечисление, захват почты).

**Файлы.** Создать: `backend/internal/identity/internal/app/{password.go,reauth_op.go,email_change.go}` и тесты,
`backend/internal/identity/queries/password.sql`. Изменить (владелец): `backend/internal/identity/httpapi/password.go`,
`backend/internal/httpapi/public/pending_password_test.go`.

**Интерфейсы.**
- `func (a *App) StartPasswordReset(ctx, email string) (CodeSent, error)` — PoW по риску `risk.LoginFailed` (почта и
  IP); лимит `email:auth_send`; аккаунт `active|suspended|pending_deletion` → код + письмо `password_reset_code`; нет,
  `deleted` или `banned` → приманка без письма (R29: у `banned` сброс не ведёт ко входу, а `account_blocked` в
  контракте `confirmPasswordReset` нет); ответ одинаков (`202`). Приманка делает ту же работу (m12): строка
  `auth_codes` и задача `mail.send` вида `password_reset_code` — Composer на строке-приманке отвечает `mail.ErrSkip`
  (Task 19); тайминг-теста нет (флак), страж — одинаковое число вставок `auth_codes` и `river_job` в обеих ветках.
- `func (a *App) ConfirmPasswordReset(ctx, in ResetInput, c Client) (LoginOutcome, error)` — порядок (C1 ревью плана:
  ни одна ветка 2FA не отвечает раньше сверки кода; две tx, N10): `requireDevice`; `password.Check` нового пароля →
  `422 identity.password_weak` — **до tx1** (R3-1: чистая проверка без БД, ответ одинаков для любого аккаунта и
  приманки; резервный код не сгорает на слабом пароле).
  **tx1:** (1) `checkCode(CodeVerifyOnly)` (приём R30: строка без замка → `lockUser` → `FOR UPDATE`) — **сверка MAC
  кода первой**: неверный → `attempts + 1`, коммит неудачи, `400 identity.code_invalid` + `attempts_left` (пятая гасит
  → `410`) — одинаково для приманки, аккаунта без 2FA и с 2FA, независимо от наличия и верности `mfa`; (1a) только
  при верном коде: аккаунт `banned` → `410 identity.code_expired` **без записей** (N4, R29: до верного MAC бан не
  виден — неверный код у забаненного даёт тот же `400`); (2) 2FA включена и `mfa` пуст → `400 identity.mfa_required`
  + `methods: [totp, recovery_code]`, код не гасится и попытка не считается (код верен — считать нечего); (3) `mfa`
  передан → лимит `user:mfa:<id>` (как `completeMfa`, `429`) и `verifySecondFactor` (неудача → `400
  identity.mfa_invalid`, коммит неудачи, код не гасится); успех фактора (шаг TOTP, сгоревший резервный код)
  коммитится вместе с tx1. **Вне tx:** (4) `Hasher.Hash` нового пароля (R31). **tx2:** (5) `lockUser` → `FOR UPDATE` кода и перепроверка (не погашен, не отозван, не истёк, тот же
  `user_id`, аккаунт не `banned`; иначе `410 identity.code_expired`, без записей); `consumeCode`, `credentials` upsert,
  отзыв всех сессий (`password_reset`), `security_events (password_reset)`, письмо `password_changed`, событие
  `identity.password_changed`, аудит `identity.password_reset`; затем `finishLogin(method = password, mfaDone = фактор
  принят в tx1, blockedAsExpired)` — сессия или `deletion_pending` (§3.11 п. 2). Между tx1 и tx2 код могли отозвать
  (удаление, новый старт сброса) — ответ `410`, шаг TOTP или резервный код уже потрачены: принято (клиент начинает
  сброс заново).
- `func (a *App) Reauthenticate(ctx, sessionID uuid.UUID, in ReauthInput) (time.Time, error)` — ровно одно из
  `password`, `totp_code`, `recovery_code`, `passkey`, `social` (иначе `validation.failed`); 2FA включена и
  `password` → `422 identity.reauth_method_not_allowed`; 5 неудач за 15 минут → `429` + `Retry-After` до конца окна
  (R16: `sessions.reauth_failures` + `reauth_failed_at` — начало окна; окно истекло → счёт с нуля);
  неудача → `400 identity.reauth_invalid` + `attempts_left` (коммит неудачи); `passkey` и
  `social` в I-2 — `reauth_invalid` (ключей и привязок ещё нет, R3); успех → `reauth_at = now`, `reauth_failures =
  0` (TOTP или резервный код — ещё и `mfa_at = now`, R27), ответ `reauth_until = now + 10 мин`.
- `func (a *App) ChangePassword(ctx, sessionID uuid.UUID, pw string) error` — `password.Check` и `Hasher.Hash` **до
  tx** (N10, R31; неудача свежести тратит один argon2 — под лимитом `account_secure`); в tx — `requireFresh`
  (`lockUser` первым); аккаунт без пароля — задаёт; отзыв **других** сессий (`password_changed`), письмо, событие,
  аудит.
- `func (a *App) StartEmailChange(ctx, sessionID uuid.UUID, newEmail string) (CodeSent, error)` — `requireFresh`;
  нормализация; совпадает с текущей → `422 identity.email_same`; одноразовый → `422 identity.email_disposable`;
  занята другим → приманка + письмо `email_change_taken` на новый адрес; иначе код на новую почту
  (`email_change_code`, `payload_enc` — новая почта). Приманка несёт `user_id` вызывающего (I7): подтверждение
  приманки и настоящего кода ведёт себя одинаково.
- `func (a *App) ConfirmEmailChange(ctx, userID uuid.UUID, codeID uuid.UUID, code string) (MeView, error)` —
  `checkCode(CodeConsume)` приёмом R30 (код принадлежит этому пользователю, иначе `410`; `lockUser` — до `FOR UPDATE`
  кода, N1); старая почта — `meta_enc` в `security_events
  (email_changed)`; `users.email`, `email_verified_at = now`, `version + 1`; гонка 23505 → `409 identity.email_taken`;
  письмо `email_changed` на **старую** почту; событие `identity.user_email_changed` без адреса; сессии не гасятся.

**Тесты.**
- `TestStartResetDecoy` → есть / нет / `banned` → одинаковые статус, тело (кроме значений), заголовки; письмо только у
  `active|suspended|pending_deletion`.
- `TestConfirmResetNoOracle` (C1) → аккаунт A с 2FA, аккаунт B без 2FA и приманка D: неверный код без `mfa`, неверный
  код с неверным TOTP, неверный код с верным TOTP → у всех трёх одинаковые статус, код, тело и `attempts = 1, 2, 3`;
  `mfa_required` появляется только у A и только при верном коде.
- `TestConfirmResetRequiresMfa` → 2FA включена, верный код без `mfa` → `400 mfa_required` с `methods` без `passkey`,
  попытка не считается, код жив; с верным TOTP → пароль сменён, все прежние сессии отозваны, новая выдана; с неверным
  → `mfa_invalid`, пароль прежний, код жив; 11-я неудача TOTP за час → `429`.
- `TestConfirmResetWeakPasswordKeepsFactor` (R3-1) → резервный код и слабый пароль → `422 password_weak`, код
  сброса и резервный код живы, `attempts` не изменился.
- `TestConfirmResetBannedAfterStart` → `banned` между стартом и подтверждением: верный код → `410 code_expired`,
  пароль, сессии и `attempts` не тронуты; неверный код → `400 code_invalid` (как у незабаненного, N4).
- `TestConfirmResetTwoPhase` (N10) → `hooks.afterRowFound` второго вызова (хук считает вызовы: первый — tx1, второй —
  tx2 после хэша): код отозван → `410`, пароль прежний; тестовый хэшер
  фиксирует, что `Hash` вызван без занятого соединения (`AcquiredConns` = 0 на своём пуле).
- `TestStartResetDecoyWork` (m12) → есть / нет аккаунта → по одной вставке `auth_codes` и `river_job`.
- `TestConfirmResetPendingDeletion` → `deletion_pending`, сессии нет, пароль сменён.
- `TestConfirmResetSocialOnlySetsPassword` → аккаунт без `credentials` → пароль задан.
- `TestReauthMethods` — таблица: без 2FA пароль → ок; с 2FA пароль → `422`; TOTP → ок, `mfa_at = now`; резервный код →
  сгорел; два поля → `validation.failed`; `passkey`/`social` → `reauth_invalid`.
- `TestReauthWindow` (I8) → 5 неудач → `429`; `+15 мин 1 с` → попытка снова принимается (`attempts_left = 4` после
  неудачи); успех обнуляет счёт.
- `TestChangePasswordNeedsFresh` → `reauth_at` старше 10 мин → `403 reauth_required`, ничего не изменено; свежий →
  другие сессии отозваны, текущая жива.
- `TestEmailChangeTakenIndistinguishable` → занятая и свободная новая почта → одинаковый `202`; письма разные;
  `confirmEmailChange` с неверным кодом у приманки и у настоящего кода → одинаковые ответы на 1-й, 4-й и 5-й попытке
  (`attempts_left` 4, 1, затем `410`).
- `TestConfirmEmailChangeForeignCode` → код другого пользователя → `410`.
- `TestConfirmEmailChangeRace` → почта занята между стартом и подтверждением → `409 email_taken`.
- HTTP на каждую операцию (свежесть, `Idempotency-Key`, `x-rate-limit` `account_secure`/`auth_send`/`auth`).

**Подсадка.** Проверять 2FA до сверки кода → `TestConfirmResetNoOracle` красный; тратить попытку при верном коде без
`mfa` → `TestConfirmResetRequiresMfa` красный; приманка смены почты без `user_id` →
`TestEmailChangeTakenIndistinguishable` красный. Принимать пароль при
2FA в `Reauthenticate` → `TestReauthMethods` красный. Не сбрасывать счёт неудач по истечении окна →
`TestReauthWindow` (`+15 мин 1 с` снова `429`) красный (N9). Проверять бан до сверки MAC → `TestConfirmResetBannedAfterStart`
(неверный код → `410`) красный (N4). Хэшировать новый пароль в tx1 → `TestConfirmResetTwoPhase` красный (N10). Писать письмо `email_changed` на новый адрес →
`TestConfirmEmailChange…` (адрес письма — через Composer, проверка `ref` и `meta_enc`) красный.

**Приёмка.** Сброс по почте при 2FA не даёт сессию без фактора; захват почты не даёт аккаунт; новый адрес не
раскрывается вошедшему.

**Ловушки.** `confirmPasswordReset` — анонимная мутация без идемпотентности: повтор после успеха — `410` (код
погашен). Свежий вход не нужен `confirmEmailChange` (§6.3), но код принадлежит сессии пользователя. Строки кодов —
только приёмом R30 (`lockUser` раньше `FOR UPDATE` кода): иначе взаимоблокировка с `deleteAccount`, который берёт
`users` и гасит коды (стражи — Task 20).

### Task 14: `me`, ник, документы, способы входа

**Цель.** Спека §3.16, §5.8, §6.2.4. Операции: `getMe`, `updateMe`, `changeNickname`, `acceptDocuments`,
`listMyAuthMethods`; корневой `Service.User`.

**Риск:** B.

**Файлы.** Создать: `backend/internal/identity/internal/app/account.go` и тест, `backend/internal/identity/queries/account.sql`,
реализация `Service.User` (`backend/internal/identity/identity.go` — тело метода, владелец сигнатуры — Task 8).
Изменить (владелец): `backend/internal/identity/httpapi/account.go`, `backend/internal/httpapi/public/pending_account_test.go`.

**Интерфейсы.**
- `func (a *App) Me(ctx, userID uuid.UUID) (MeView, error)` — поля `Me` §6.2.4: `nickname_changeable_at`,
  `has_password`, `mfa_enabled`, `restrictions` (действующие), `documents` (принятая и текущая версии, `url`),
  `documents_acceptance_required`; `Cache-Control: private, no-store`.
- `func (a *App) UpdateMe(ctx, userID uuid.UUID, locale string) (MeView, error)` — `ru|en`, `version + 1`, без
  события (язык — не событие каталога).
- `func (a *App) ChangeNickname(ctx, userID uuid.UUID, nick string) (MeView, error)` — формат → `422`; не чаще 30 дней
  (`nickname_changes`) → `409 identity.nickname_change_too_soon` + `available_at`; свободен (свой прежний ник из
  карантина — можно, строка карантина удаляется) → `409 nickname_taken`; старый — в карантин 90 дней
  (`nickname_reservations`, UPSERT по `nickname_normalized`: цепочка A→B→A→C не даёт 23505, m5), событие
  `identity.nickname_changed`, аудит.
- `func (a *App) AcceptDocuments(ctx, userID uuid.UUID, docs []Accepted, kind domain.ClientKind) (MeView, error)` —
  `Legal.Check` → `422 identity.documents_outdated`; `consents` (повтор той же версии — без ошибки), аудит
  `identity.documents_accepted`.
- `func (a *App) MyAuthMethods(ctx, userID uuid.UUID) (MyAuthMethodsView, error)` — `has_password`, привязки и ключи
  из таблиц (в I-2 пусто), `mfa` (как `getMyMfa`, Task 16 читает тем же запросом — запрос в `account.sql`).

**Тесты.** `TestMeFields` (документы: новая версия в конфиге → `documents_acceptance_required = true`);
`TestChangeNicknameTooSoon` (+`available_at`); `TestChangeNicknameOwnQuarantine` (вернуть свой прежний ник —
можно, чужой из карантина — `409`); `TestChangeNicknameRace` (два пользователя на один ник → один `200`, второй
`409`); `TestAcceptDocumentsOutdated`; `TestUserSummaryNoPII` (у `UserSummary` нет почты и даты рождения —
рефлексией); HTTP на каждую операцию.

**Подсадка.** Не класть старый ник в карантин → `TestChangeNicknameOwnQuarantine` (чужой займёт) красный.

**Приёмка.** `Me` — ровно схема контракта (страж `apitest`); ник уникален по `normalize_text` под гонкой.

**Ловушки.** `PATCH /v1/me` — с `Idempotency-Key`: одна tx. Ник заглушки удалённых — `nickname_invalid`.

### Task 15: удаление аккаунта, отмена, анонимизация

**Цель.** Спека §3.11 (решение пользователя В9): запрос с периодом 30 дней, отмена входом, необратимая анонимизация
по сроку. Операции: `deleteAccount`, `cancelDeletion`; метод воркера анонимизации.

**Риск:** A (необратимость, повторяемость, права воркера, гонка отмены и анонимизации).

**Файлы.** Создать: `backend/internal/identity/internal/app/{deletion.go,anonymize.go}` и тесты,
`backend/internal/identity/queries/deletion.sql`, `backend/internal/identity/jobs/anonymize.go` (исполнитель River,
регистрирует Task 19). Изменить (владелец): `backend/internal/identity/httpapi/deletion.go`,
`backend/internal/httpapi/public/pending_deletion_test.go`.

**Интерфейсы.**
- `func (a *App) DeleteAccount(ctx, sessionID uuid.UUID, confirmNickname string) (purgeAt time.Time, err error)` —
  `requireFresh`; ник (NFKC, без учёта регистра) ≠ текущему → `422 identity.confirm_mismatch`; одна tx:
  `status = pending_deletion`, `deletion_requested_at`, `purge_at = now + 30 д`, `version + 1`; все сессии
  (`account_deleted`); открытая заявка в капитаны → `withdrawn` (`closed_at`); живые коды аккаунта (`auth_codes`
  смены почты, сброса) → `revoked_at`; живой сброс 2FA → `cancelled_at`, `cancelled_by = system` (m4; событие
  `identity.mfa_reset_cancelled` публикуется и при `system`, версия агрегата `mfa_reset` + 1, письма нет); порядок
  замков R30: `users` (`requireFresh`) → `sessions` → `mfa_resets` → `auth_codes`; адрес —
  `meta_enc` в `security_events (account_deletion_scheduled)` (для письма о запросе); событие `identity.user_deletion_scheduled {user_id, purge_at}`; письмо
  `account_deletion_scheduled`; аудит `identity.user_deletion_requested`.
- `func (a *App) CancelDeletion(ctx, raw string, c Client) (LoginOutcome, error)` — `takeChallenge(deletion_restore)`
  (приём R30: `lockUser` раньше `FOR UPDATE` вызова) → нет → `410 identity.restore_token_invalid`; `requireDevice`; аккаунт не `pending_deletion` (уже
  анонимизирован) → `410`; `active`, поля удаления — `NULL`, событие `identity.user_deletion_cancelled`, письмо
  `account_restored`, аудит; `issueSession` (способ — из `data` вызова, `mfa_at` — если вход был со вторым
  фактором).
- `func (a *App) AnonymizeDue(ctx context.Context, batch int) (n int, err error)` — под ролью `worker`; строки
  `status = 'pending_deletion' AND purge_at <= now` `FOR UPDATE SKIP LOCKED` пачкой; на строку: `status = deleted`,
  `deleted_at`, почта → `deleted+<id>@invalid`, ник → `DeletedNickname(id)`, прежний — в карантин 90 дней;
  `DELETE` `credentials`, `user_mfa`, `user_recovery_codes`, `passkeys`, `user_identities`, `consents`; допуски
  капитана → `revoked_at`, `revoke_reason = account_deleted`; событие `identity.user_deleted`; **до** замены почты —
  новая запись `security_events (account_deleted)` с `meta_enc = Seal(users.email, aad = id записи)` (у `worker` есть
  `INSERT` и кольцо) и письмо `account_deleted` с `ref` на неё (I3: `meta_enc` записи запроса чистка стирает через 7
  дней, а анонимизация — через 30); аудит от `system`; повтор на `deleted` — ничего.
- `jobs.AnonymizeWorker` (`Kind() = "identity.anonymize"`, очередь `maintenance`, `V = 1`, без аргументов-данных) —
  зовёт `AnonymizeDue` до исчерпания пачек.

**Тесты.**
- `TestDeleteAccountHides` → `identity_read_users.hidden = true`, сессии отозваны, заявка `withdrawn`, событие
  `user_deletion_scheduled`, **нет** `user_deleted`, письмо; допуски капитана живы.
- `TestDeleteConfirmMismatch` → `422`, ничего не изменено; без свежего входа → `403 reauth_required`.
- `TestLoginDuringGrace` → вход паролем → `deletion_pending` без сессии; `cancelDeletion` → `200`, `active`, сессия.
- `TestCancelDeletionTokenOnce` → второй вызов тем же токеном → `410`; через 10 мин → `410`.
- `TestAnonymizeDue` → после `purge_at`: заглушки почты и ника, секреты и привязки удалены, `device_accounts` живы,
  допуски отозваны, `user_deleted`, письмо по `meta_enc` новой записи; повтор — ничего, без ошибки.
- `TestAnonymizeMailAfterCleanup` (I3) → запрос в день 0, `Cleanup` на `+8 д`, анонимизация на `+30 д` → Composer
  отдаёт письмо со старым адресом.
- `TestDeleteAccountClosesCodesAndReset` → живой код смены почты и живой сброс 2FA закрыты (`cancelled_by = system`).
- `TestAnonymizeVsCancelRace` → параллельно `CancelDeletion` и `AnonymizeDue` на одном аккаунте при `purge_at = now`
  → ровно один исход (аккаунт `active` с сессией **или** `deleted`, а `CancelDeletion` — `410`).
- `TestAnonymizeAsWorker` → под ролью `worker` проходит; запись в таблицу, которой у `worker` нет по §5.5, — тест
  падает (страж прав анонимизации).
- `TestSignupOnPendingDeletionEmail` → регистрация на почту в периоде — приманка и письмо «войдите, чтобы отменить»
  (связка с Task 10).
- HTTP: `TestDeleteAccountHTTP` (`200 {purge_at}`), `TestCancelDeletionHTTP` (анонимно, `no-store`).

**Подсадка.** Публиковать `user_deleted` при запросе → `TestDeleteAccountHides` красный. Убрать `SKIP LOCKED`/замок →
`TestAnonymizeVsCancelRace` красный.

**Приёмка.** Анонимизация необратима (исходной почты и ника не остаётся нигде, кроме `meta_enc` до стирания
чисткой); повторяема; права воркера — ровно §5.5; отмена возвращает всё, кроме отозванных сессий.

**Ловушки.** `users_email_key` — частичный по `deleted_at IS NULL`: заглушка проходит CHECK `users_email_format`
(R24). Отзыв токена Apple при анонимизации — I-3 (привязок в I-2 нет; оставить точку расширения с комментарием).
Письмо `account_deleted` уходит после удаления почты из `users`: адрес только из `meta_enc` записи, созданной
анонимизацией. Замки — порядок R30 (`users` первым): `CancelDeletion` и `AnonymizeDue` берут строку `users`.

## Этап 5. 2FA (спека §15 п. 5)

### Task 16: 2FA игрока — TOTP, резервные коды, второй шаг входа

**Цель.** Спека §3.3: включение, управление и шаг входа. Операции: `getMyMfa`, `setupTotp`, `confirmTotp`,
`disableTotp`, `regenerateRecoveryCodes`, `completeMfa`; сквозной страж `no-store` в конвейере public (m15 плана I-1).

**Риск:** A.

**Файлы.** Создать: `backend/internal/identity/internal/app/{mfa.go,mfa_login.go}` и тесты,
`backend/internal/identity/queries/mfa.sql`. Изменить (владелец): `backend/internal/identity/httpapi/mfa.go`,
`backend/internal/httpapi/public/{pending_mfa_test.go,nostore_test.go}`.

**Интерфейсы.**
- `func (a *App) MfaStatus(ctx, userID uuid.UUID) (MfaStatusView, error)` — `totp_enabled`, `confirmed_at`,
  `recovery_codes_left`, `reset_scheduled_at` (живой `mfa_resets.execute_at`).
- `func (a *App) SetupTotp(ctx, sessionID uuid.UUID) (secret, uri string, err error)` — `requireFresh`; подтверждённая
  2FA → `409 identity.mfa_already_enabled`; иначе новый секрет (`otp.NewSecret`), `secret_enc = Seal(…, aad =
  userID)`, `confirmed_at = NULL` (повтор выпускает новый); `uri = otp.URI(TOTPIssuer, ник, secret)`.
- `func (a *App) ConfirmTotp(ctx, userID uuid.UUID, code string) ([]string, error)` — нет неподтверждённого секрета →
  `409 identity.mfa_setup_missing`; неверный → `400 identity.mfa_invalid`; верный → `confirmed_at`,
  `last_used_step`, 10 резервных кодов (`otp.NewRecoveryCodes`, хранится MAC), письмо `mfa_enabled`, событие
  `identity.mfa_changed {enabled: true}`, аудит; другие сессии не гасятся.
- `func (a *App) DisableTotp(ctx, sessionID uuid.UUID) error` — `requireFresh` **и** сессия подтвердила свежесть
  вторым фактором: `sessions.mfa_at` не старше 10 минут (ставят вход со вторым фактором и `reauthenticate` TOTP или
  резервным кодом, R27); иначе `403 identity.reauth_required`; нет 2FA → `409
  identity.mfa_not_enabled`; удалить секрет и коды, письмо `mfa_disabled`, событие, аудит.
- `func (a *App) RegenerateRecoveryCodes(ctx, sessionID uuid.UUID) ([]string, error)` — `requireFresh`; нет 2FA →
  `409`; старые удаляются, новые 10, письмо `recovery_codes_regenerated`.
- `func (a *App) CompleteMfa(ctx, raw string, f SecondFactor, passkey *PasskeyInput, c Client) (LoginOutcome, error)`
  — `requireDevice`; лимит `user:mfa:<id>` 10 неудач/ч (`429`); `takeChallenge(mfa)` (приём R30) → нет → `410
  identity.mfa_expired`; аккаунт стал `banned` → `mfa_token` гасится (`consumed_at`, коммит), `410
  identity.mfa_expired` **до** `verifySecondFactor` (R29; отказ 3 раунда 2: повторы не крутят `user_mfa.failures` и
  письма `mfa_failures` забаненного); ровно одно из трёх; `passkey` в I-2 → `400 identity.passkey_invalid` (ключей нет, R3);
  `verifySecondFactor` → неудача: `failChallenge`, пятая гасит → `410`, иначе `400 identity.mfa_invalid` +
  `attempts_left`; успех → `consumed_at`, `finishLogin(mfaDone = true, method = из challenge)`.
- httpapi: `setupTotp`, `confirmTotp`, `regenerateRecoveryCodes` — `Cache-Control: no-store` (заголовок ставит
  хендлер, `x-idempotency: no-store` — конвейер).

**Тесты.**
- `TestSetupConfirmTotp` → секрет в базе только шифротекстом, `uri` с издателем и ником; неверный код → `400`; верный
  → 10 кодов `xxxxx-xxxxx`, письмо, событие; повтор `setupTotp` после включения → `409`.
- `TestSetupTwiceReissues` → два `setupTotp` до подтверждения → код от первого секрета не подходит.
- `TestDisableNeedsSecondFactorFresh` → `reauth_at` свежий, `mfa_at` старше 10 минут → `403`; после
  `reauthenticate` TOTP → `204`, секрет и коды удалены.
- `TestRegenerateCodes` → старый код не проходит, новый — да.
- `TestCompleteMfaFlow` → `login` → `mfa_required` → `completeMfa` TOTP → сессия `mfa_at = now`, способ `password`;
  резервным кодом → письмо `recovery_code_used` с остатком.
- `TestCompleteMfaAttempts` → 4 неверных (`attempts_left` 4…1), пятый → `410`; тот же `mfa_token` после успеха →
  `410`; через 5 мин → `410`.
- `TestCompleteMfaUserLimit` → 10 неудач за час на разных `mfa_token` → `429`.
- `TestCompleteMfaPendingDeletion` → после фактора — `deletion_pending`.
- `TestCompleteMfaBannedBurnsToken` → бан между `login` и `completeMfa` → `410 mfa_expired`, `user_mfa.failures` не
  изменился; повтор тем же токеном → `410`.
- `TestPublicNoStoreReplay` (`internal/httpapi/public`, через весь конвейер) → `setupTotp` с `Idempotency-Key`, повтор с
  тем же ключом → `409 idempotency.replay_unavailable`; ответ — `Cache-Control: no-store`, тело в
  `idempotency_keys` не сохранено.
- HTTP на каждую операцию (свежесть, коды контракта).

**Подсадка.** Сохранять ответ `setupTotp` в ключе идемпотентности (убрать `x-idempotency` из признака маршрута в
тестовой подмене) → `TestPublicNoStoreReplay` красный. Не гасить `mfa_token` на пятой неудаче →
`TestCompleteMfaAttempts` красный. Пускать `disableTotp` по свежести паролем → `TestDisableNeedsSecondFactorFresh`
красный.

**Приёмка.** Секрет TOTP и резервные коды нигде не логируются и не сохраняются в ответах идемпотентности; шаг входа
неотличим по форме для TOTP и резервного кода; включение 2FA не гасит сессии.

**Ловушки.** Ник в `otpauth_uri` — URL-кодирование (`otp.URI`). Сравнение кодов — постоянное время (платформа).
`completeMfa` — анонимная мутация: защита — одноразовый `mfa_token` и лимиты.

### Task 17: сброс 2FA поддержкой — запрос, подтверждение, отмена сотрудником

**Цель.** Спека §3.15 п. 2–4, §7.1 (`requestMfaReset`, `cancelMfaReset`), §7.6 (запрет решений о своей учётке).
Сотрудник запрашивает сброс, второй подтверждает (`platform/approvals`, `dual`), исполнитель назначает сброс через 7
дней; сотрудник может отменить сразу.

**Риск:** A (права в момент действия, двое, self-action, admin-api не пишет `users`/`sessions`).

**Файлы.** Создать: `backend/internal/identity/admin/{mfa_reset.go,approvals.go}` и тесты,
`backend/internal/identity/internal/app/mfa_reset_admin.go` и тест, `backend/internal/identity/queries/mfa_reset.sql`,
`backend/cmd/admin-api/identity_wiring_test.go`. Изменить: `backend/internal/identity/admin/handler.go` (`Deps`, `New`;
нулевой — 501), `backend/internal/platform/auth/staff.go` (`LinkedUserID uuid.UUID`), `backend/internal/staff/queries/sessions.sql`
и `backend/internal/staff/internal/app/session.go` (загрузка `LinkedUserID` из `staff_player_links` тем же запросом),
`backend/internal/httpapi/admin/{server.go,nostub_test.go,permissions_test.go}` (`pendingOps` admin −2, `handlerRoles`
не трогать — у операций ключ, не `handler`), `backend/cmd/admin-api/main.go` (сборка identity), `.claude/rules/backend-staff.md`
(`staff_player_links`, `auth.Staff.LinkedUserID`), `.claude/rules/backend-http.md` (сборка identity в admin-api).

**Интерфейсы.**
- `auth.Staff.LinkedUserID uuid.UUID` — `uuid.Nil`, если связи нет; загрузчик staff читает связь одним запросом с
  сессией (R8). Поле только читается.
- Действие `approvals.Action{Name: "identity.mfa_reset", Permission: "identity.mfa.reset", LockActors: true, Target:
  игрок, Exec: …}`; payload `{v: 1, user_id, reason, ticket?, approvals.Initiator}` (причина — в
  `mfa_resets.reason`; в аудит — только `ticket` и длина причины: свободный текст в `Before/After` не кладётся, §8.3); регистрирует `func (h *Handler) RegisterApprovals(r *approvals.Registry, svc *approvals.Service)` —
  в сборке admin-api до `reg.Validate()`, как geo.
- `requestMfaReset` (`POST /v1/identity/users/{userId}/mfa-reset`, `IdentityMfaResetRequest` → `202 ApprovalAccepted`):
  игрока нет или `deleted` → `404 identity.user_not_found`; `st.LinkedUserID == userId` → `409 identity.self_action`;
  нет подтверждённой 2FA (`SELECT user_id, confirmed_at FROM user_mfa`) → `409 identity.mfa_not_enabled`; живой
  сброс есть → `409 identity.mfa_reset_pending`; иначе `approvals.Request` в tx (аудит
  `platform.approval_requested`). `reason` 3–500, `ticket` ≤ 100 → `validation.failed`.
- Исполнитель (под savepoint, `ctx` — подтверждающий): подтверждающий связан с игроком → `approvals.Fail(self_action)`;
  аккаунт без 2FA, с живым сбросом или `pending_deletion`/`deleted` → `approvals.Fail(ErrStale)` (`409
  platform.approval_stale`); иначе `mfa_resets` (`execute_at = now + 7 д`, `requested_by_*`, `approval_id`),
  `security_events` (вставка), событие `identity.mfa_reset_scheduled` (агрегат `mfa_reset`, версия 1), аудит
  `identity.mfa_reset_requested` (автор — подтверждающий с подписью решения, `approval_id` и инициатор — в `meta`), письмо
  `mfa_reset_scheduled` (`ref = mfa_resets.id`, `mail.SendArgs` в очередь `mail` клиентом вставки admin-api).
- `cancelMfaReset` (`POST /v1/identity/mfa-resets/{resetId}/cancel` → `200 IdentityMfaResetCancelled`): право
  `identity.mfa.reset` — `Decide` = `NeedsApproval` трактуется как разрешение (§7.2); закрытый или нет → `404
  identity.mfa_reset_not_found`; своя учётка → `409 identity.self_action`; `cancelled_at`, `cancelled_by =
  staff:<label>`, событие `identity.mfa_reset_cancelled` (версия 2), письмо `mfa_reset_cancelled`, аудит.
- `identityadmin.New(svc identity.Service, d Deps)`; `Deps{Pool, Clock, River, Log}`; `admin.Options.Identity`.

**Тесты.**
- `TestRequestMfaResetCodes` — таблица: нет игрока, своя учётка (связь в `staff_player_links` фикстурой), нет 2FA,
  живой сброс → коды; успех → `202`, запрос `pending`.
- `TestMfaResetApproveExecutes` → второй сотрудник подтверждает → `mfa_resets` с `execute_at = now + 7 д`, событие,
  аудит, задача письма; `users` и `sessions` не тронуты (роль `admin` — без прав, транзакция под ролью прода).
- `TestMfaResetApproverLinkedFails` → подтверждающий связан с игроком → `failed`, ответ `409`.
- `TestMfaResetStaleOnApprove` → между запросом и подтверждением игрок выключил 2FA → `failed`, `409
  platform.approval_stale`.
- `TestMfaResetInitiatorDisabledConcurrently` → по образцу `TestCountryRulesInitiatorDisabledConcurrently`.
- `TestCancelMfaResetByStaff` → `200 {id, cancelled_at}`; повтор → `404`; своя учётка → `409`.
- `TestLoadStaffLinkedUser` (staff) → связь есть → `LinkedUserID`; нет → `uuid.Nil`.
- `TestAssembleRegistersIdentityActions` (`cmd/admin-api`) → в реестре `identity.mfa_reset`, `LockActors`, сборка
  без хука staff — ошибка `Validate`.
- `TestPermissionsAllOperations` — зелёный: у двух операций реальные ответы вместо 501; `TestApprovalDecisionPermissionsAreDual`
  — зелёный.

**Подсадка.** Не проверять `LinkedUserID` у подтверждающего → `TestMfaResetApproverLinkedFails` красный. Писать
`users.version` в исполнителе → `TestMfaResetApproveExecutes` (42501 под ролью `admin`) красный.

**Приёмка.** Запрос никогда не исполняется сам (`dual`); исполнитель пишет только разрешённые `admin` таблицы; отмена
сотрудником — сразу, без подтверждения; `pendingOps` admin = 9.

**Ловушки.** Свободный текст причины — не в `Before/After` аудита (маскирование по ключу, не по содержимому). Письма
identity в admin-api — `mail.SendArgs` в очередь `mail` (исполняет воркер), **не** `staff.NewMailArgs`. Порядок замков
— строка `approvals` раньше строк модуля (`backend-platform.md`).

### Task 18: сброс 2FA — отмена владельцем и исполнение по сроку

**Цель.** Спека §3.15 п. 4–6: владелец отменяет сброс входом со вторым фактором или явно; по `execute_at` воркер
стирает TOTP и коды и отзывает сессии. Операция: `cancelMyMfaReset`; метод воркера исполнения.

**Риск:** A (гонка отмены и исполнения, отзыв сессий, ключи доступа не трогаются).

**Файлы.** Создать: `backend/internal/identity/internal/app/{mfa_reset_player.go,mfa_reset_exec.go}` и тесты,
`backend/internal/identity/jobs/mfa_reset.go` (исполнитель River, регистрирует Task 19). Изменить (владелец):
`backend/internal/identity/internal/app/mfa_reset_hook.go` (тело хука), `backend/internal/identity/httpapi/mfa_reset.go`,
`backend/internal/httpapi/public/pending_mfa_reset_test.go`.

**Интерфейсы.**
- `func (a *App) onSecondFactorLogin(ctx, tx, userID uuid.UUID) error` — живой сброс → `cancelled_at`,
  `cancelled_by = owner`, событие `mfa_reset_cancelled` (версия 2), письмо `mfa_reset_cancelled`, аудит от игрока.
- `func (a *App) CancelMyMfaReset(ctx, userID, sessionID uuid.UUID) error` (m3) — живой → отмена (как выше); нет
  живого, но сброс отменён владельцем (`cancelled_by = owner`) не раньше `sessions.mfa_at` этой сессии → `204`; иначе
  `404 identity.mfa_reset_not_found`.
- `func (a *App) ExecuteDueMfaResets(ctx context.Context, batch int) (int, error)` — под ролью `worker`; кандидаты —
  живые с `execute_at <= now`; на строку — порядок замков R30 (N5): `users … FOR NO KEY UPDATE SKIP LOCKED`, затем
  `user_mfa FOR UPDATE` (нет строки — 2FA нет), затем `mfa_resets FOR UPDATE` и перепроверка «живой»; аккаунт
  `pending_deletion`/`deleted` или 2FA уже нет → `cancelled_by = system` без писем (событие
  `identity.mfa_reset_cancelled` публикуется, версия агрегата + 1 — как в Task 15); иначе `DELETE user_mfa`, `user_recovery_codes` (ключи доступа остаются), отзыв
  всех сессий (`revoked`), `executed_at`, письмо `mfa_reset_done`, событие `identity.mfa_changed {enabled: false}`,
  аудит `identity.mfa_reset_executed` от `system`.
- `jobs.MfaResetWorker` (`Kind() = "identity.mfa_reset_execute"`, очередь `maintenance`, `V = 1`).

**Тесты.**
- `TestSecondFactorLoginCancelsReset` → вход паролем + TOTP → сброс отменён, письмо; вход паролем без 2FA-шага
  невозможен (2FA включена) — отдельный случай не нужен.
- `TestCancelMyMfaReset` → живой → `204`; повтор → `204` (уже отменён входом этой сессии) / `404` (сброса не было).
- `TestExecuteDueMfaResets` → после `execute_at`: TOTP и коды удалены, сессии отозваны, `executed_at`, событие,
  письмо; до срока — ничего; повтор — ничего.
- `TestExecuteVsOwnerCancelRace` → параллельно исполнение и отмена владельцем → ровно один исход.
- `TestExecuteKeepsPasskeys` → строка `passkeys` (фикстура) остаётся.
- `TestMfaStatusShowsReset` и `reset_at` в `mfa_required` (Task 9) → дата сброса видна.
- HTTP: `TestCancelMyMfaResetHTTP`.

**Подсадка.** Не отзывать сессии при исполнении → `TestExecuteDueMfaResets` красный. Убрать `SKIP LOCKED`/замок →
`TestExecuteVsOwnerCancelRace` красный.

**Приёмка.** Отмена владельцем — только доказательством второго фактора или явной ручкой с замком; исполнение
повторяемо; `pendingOps` public без `cancelMyMfaReset`.

**Ловушки.** Хук вызывается в tx входа: ошибка хука — откат входа (500), не тихий пропуск. Исполнение под ролью
`worker` — `UPDATE mfa_resets` есть, `INSERT` нет (§5.5).

## Этап 6. Фон и закрытие

### Task 19: письма, задачи воркера, чистка, ротация кольца

**Цель.** Спека §8.2 (письма I-2), §5.10 (чистка), §5.4 (ротация кольца — `api identity rekey`, R28): воркер собирает письма по `ref`,
исполняет анонимизацию и сбросы 2FA (Task 15, 18), чистит устаревшее; лидер River (воркер **или** admin-api) ставит
периодику identity (R7).

**Риск:** B (письма с кодами — секреты в памяти воркера; периодика у двух бинарников).

**Файлы.** Создать: `backend/internal/identity/mail/{composer.go,composer_test.go}` (по одному `mail.Composer` на вид
или один с таблицей видов), `backend/internal/identity/jobs/{jobs.go,maintenance.go}` и тесты,
`backend/internal/identity/internal/app/{cleanup.go,rekey.go}` и тесты, `backend/internal/identity/queries/{cleanup,mail}.sql`.
Изменить: `backend/locales/{ru,en}.json` (`identity.mail.<письмо>.{subject,text}` — все письма I-2),
`backend/internal/platform/queue/foreign.go` (виды identity в `WorkerKinds`), `backend/cmd/worker/{main.go,kinds_test.go,maintenance_test.go,main_test.go}`
(реестр писем, исполнители, периодика), `backend/cmd/admin-api/main.go` (периодика identity
в списке лидера), `backend/internal/platform/periodic/periodic_test.go` (если страж сверяет списки — расширить).
Ротация (R28, I4, N2): создать `backend/internal/identity/rekey/{rekey.go,rekey_test.go}` (сборщик под `identity/`,
не в корне — корень импортирует `app` нельзя, цикл), `backend/cmd/api/rekey.go`; изменить `backend/cmd/api/main.go`
(подкоманда). Вариантов в `cmd/worker` и `identity/jobs` нет.

**Интерфейсы.**
- Письма I-2 (виды — константы Task 8): `signup_code`, `signup_account_exists`, `password_reset_code`,
  `email_change_code`, `email_change_taken`, `email_changed`, `password_changed`, `session_revoked_suspicious`,
  `mfa_enabled`, `mfa_disabled`, `recovery_code_used`, `recovery_codes_regenerated`, `mfa_failures`,
  `account_deletion_scheduled`, `account_restored`, `account_deleted`, `mfa_reset_scheduled`, `mfa_reset_cancelled`,
  `mfa_reset_done`. `func Register(r *mail.Registry, d ComposerDeps) error`; `ComposerDeps{Pool, Secrets *keys.Ring,
  Catalog *i18n.Catalog, Clock}`; кольцо `Secrets` — из `config.WorkerIdentity` (Task 4), которое `cmd/worker` уже
  читает только в режиме воркера (разовым командам кольцо не нужно). Адрес — `users.email` либо `meta_enc` (`email_changed` — старая почта,
  `account_deleted`), язык — `users.locale`; код — `Open(code_enc)`; код погашен, истёк или стёрт → `mail.ErrSkip`;
  получатель `deleted` (кроме `account_deleted`) → `mail.ErrSkip` (m4: не слать на заглушку `…@invalid`);
  приманка → `signup_account_exists` / `email_change_taken` на почту строки; приманка сброса пароля
  (`password_reset_code` без `code_enc`) → `mail.ErrSkip` (m12, Task 13).
- `func (a *App) Cleanup(ctx context.Context) (CleanupReport, error)` — §5.10 в части I-2: `auth_codes` и
  `auth_challenges` через 24 ч после `expires_at` или закрытия; `refresh_tokens.successor_enc` после окна;
  `sessions` отозванные и истёкшие — через 30 дней; `security_events` — 180 дней, `meta_enc` — 7 дней (`UPDATE
  (meta_enc)`); `nickname_reservations` после `expires_at`; `user_restrictions` истёкшие → `lifted_at`, событие
  `identity.restrictions_changed`; сверка «`deleted` с недочищенными секретами» → дочистить. Пачками, каждая — своя
  транзакция, под ролью `worker`.
- `jobs`: `func Periodic() []*river.PeriodicJob` — `identity.maintenance` (раз в 10 мин: `Cleanup`), `identity.anonymize`
  (раз в час), `identity.mfa_reset_execute` (раз в 10 мин); `func AddWorkers(w *river.Workers, d Deps)` — исполнители
  Task 15, 18 и чистки; все в очереди `maintenance`, аргументы `{v: 1}`. `cmd/worker` и `cmd/admin-api` передают
  `append(periodic.Jobs(), identityjobs.Periodic()...)` (R7); виды — в `queue.WorkerKinds()` со сроками.
- `api identity rekey [--dry-run]` (команда `cmd/api`, роль `api`, R28; I4: у `worker` нет `UPDATE` и чтения
  секретов этих таблиц) — перешифровка долгоживущих секретов `user_mfa.secret_enc` и
  `user_identities.apple_refresh_enc` текущим ключом; HMAC не пересчитываются; короткоживущие (`auth_codes`,
  `auth_challenges` — минуты, `security_events.meta_enc` — ≤ 7 дней) не перешифровываются; MAC резервных кодов
  пересчитать нельзя — старый ключ убирается из кольца только когда под ним нет `user_recovery_codes` (перевыпуск
  владельцем, спека §5.4) и не раньше 7 дней после ротации; отчёт — число строк по `key_id` до и после, отдельно
  `user_recovery_codes` под старыми ключами. Сборка: `identityrekey.Run(ctx, d identityrekey.Deps{Pool, Clock,
  Secrets, River, Log}, dryRun bool) (Report, error)` (`backend/internal/identity/rekey`, внутри —
  `app.NewMaintenance` с кольцом и `(*App).Rekey`); `cmd/api/rekey.go` разбирает флаги и зовёт его.

**Тесты.**
- `TestComposersCoverKinds` → каждая константа вида письма I-2 зарегистрирована; у каждого — ключи ru и en в
  `backend/locales`; `TestLocalesHaveSameKeys` (есть) — зелёный.
- `TestComposeSignupCode` → письмо с кодом; код погашен → `ErrSkip`; приманка → «уже есть аккаунт».
- `TestComposeEmailChangedToOldAddress` → адрес из `meta_enc`, не текущий.
- `TestComposeAccountDeleted` → после анонимизации адрес из `meta_enc`; `meta_enc` стёрт → `ErrSkip`.
- `TestCleanupRetention` — таблица сроков §5.10 (граница «ровно срок» и «срок + 1 с»).
- `TestCleanupLiftsRestrictions` → истёкшее ограничение → `lifted_at`, событие.
- `TestWorkerKinds`, `TestRescuerKeepsWorkerJobs`, `TestLeaderWithoutWorkersEnqueuesPlatformJobs` — зелёные с
  видами identity; `TestPeriodicSameInBothBinaries` (новый, `cmd/worker` или общий тест) → множества видов
  периодики воркера и admin-api равны.
- `TestRekey` (под ролью `api`, `dbtest.NewPoolsAs`) → строки под старым ключом → после команды под текущим,
  открываются; `--dry-run` ничего не пишет; резервные коды под старым ключом — в отчёте, не изменены. Сборка
  `go build ./cmd/api ./cmd/worker ./cmd/admin-api` — в приёмке (цикл импорта, N2).
- `TestExecuteSkipsDeleted` (m4) → аккаунт в `pending_deletion` с живым сбросом → `cancelled_by = system`, письма нет.

**Подсадка.** Брать адрес `email_changed` из `users.email` → `TestComposeEmailChangedToOldAddress` красный. Не
добавить периодику identity в admin-api → `TestPeriodicSameInBothBinaries` красный.

**Приёмка.** В аргументах задач нет ПД; коды в логах воркера не появляются; каждая чистка — под ролью `worker` и в
пределах §5.5; письма идемпотентны к повтору (доставка «хотя бы раз»).

**Ловушки.** Лидером может быть admin-api: периодика ставится по **его** списку — поэтому общий `Periodic()`.
Исполнители identity — только у воркера (admin-api регистрирует их заглушками через `queue.AddForeign`, как прочие
виды воркера). `i18n.Catalog.Text` — ключи ru = en.

### Task 20: сквозные стражи и документы этапа

**Цель.** Закрыть I-2: сквозная проверка 32 операций public и 2 admin на собранном сервере, состояние `pendingOps`,
документы и правила, которые трогают несколько задач (R9).

**Риск:** B (стражи), C (документы).

**Файлы.** Создать: `backend/internal/httpapi/public/identity_core_test.go`,
`backend/internal/identity/internal/app/lock_order_test.go` (стражи N1), `.claude/rules/backend-identity.md`
(`paths: backend/internal/identity/**`). Изменить: `CLAUDE.md` (список правил `.claude/rules/` — `backend-identity`),
`docs/01-домен-и-правила.md` (вход и сессии, 2FA и её сброс поддержкой, согласие с документами, удаление с периодом
отмены, флаги способов входа), спека бэкенда (§2 — 2FA игроков сейчас; §4.1 — таблицы identity и
`captain_applications`; §4.6 — каталог событий — ссылка на спеку identity §8.1; §6.2 — сроки и строка сессии =
вход; §7.1 — сброс пароля при 2FA; §7.6 — период удаления, `user_deleted` при анонимизации; §11 — миграции identity,
`pending_deletion`; §13 — ссылка), спека identity (поправки по R этого плана: R2, R3, R4, R7, R16 (`reauth_failed_at`), R17, R18, R24–R31; §3.11/§5.10 —
письмо `account_deleted` по новой записи анонимизации (I3) —
пометкой «план I-2»). Изменения документов, собранные в отчётах задач 10–19 (раздел «Документы»), — сюда.

**Тесты** (`identity_core_test.go`, собранный сервер на реальной базе):
- `TestCoreOpsNotStub` → каждая из 32 операций (Приложение 1) отвечает не 501; `pendingOps` public = 18 операций
  (I-3: 14, I-4: 4), admin = 9 — точными множествами.
- `TestCoreIDOR` → сессии, коды смены почты, сброс 2FA владельцем: чужой объект → тот же ответ, что «нет».
- `TestCoreCacheControl` → у каждого 2xx с телом — значение из контракта (R5 плана I-1).
- `TestCoreAnonymousNoIdempotency` → 9 анонимных мутаций I-2 не пишут `idempotency_keys`.
- `TestCoreNoPIIInEvents` → полный сценарий (регистрация, вход, смена почты, удаление) → `outbox.payload` без почты,
  ника, IP; `river_job.args` без почты.
- `identity-freeze.test.mjs` — зелёный (контракт не правился, кроме enum флагов).
- `TestConfirmEmailChangeVsDeleteNoDeadlock`, `TestConfirmResetVsDeleteNoDeadlock` (N1, пакет `app`, реальная база) →
  `confirmEmailChange` / `confirmPasswordReset` (tx2) останавливается на `hooks.afterRowFound` (строка кода найдена);
  `deleteAccount` той же учётки проходит целиком (берёт `users`, гасит коды); затем подтверждение продолжается →
  ни одного `40P01`: удаление `200`, подтверждение `410 identity.code_expired`, почта и пароль прежние. Барьер
  детерминирован (канал), повторы не нужны.
- `TestRefreshReuseVsDeleteNoDeadlock` (R3-3) → повтор использованного refresh встаёт на `afterRowFound("refresh")`,
  `deleteAccount` проходит; затем refresh → `401 identity.refresh_invalid`, ни одного `40P01`.

**Подсадка.** Вернуть одну реализованную операцию в `pendingOps` → `TestPendingOpsExpire` и `TestCoreOpsNotStub`
красные. Брать `FOR UPDATE` строки кода до `lockUser` в `checkCode` → удаление встаёт на коде, подтверждение — на
`users`: `40P01`, `TestConfirmEmailChangeVsDeleteNoDeadlock` красный (N1). Для подсадки барьер удаления — ожидание
до 2 с, не бесконечное.

**Приёмка.** Документы не расходятся с кодом; `backend-identity.md` описывает ядро входа (R6), приманки, свежий
вход, файлы-владельцы (R5), периодику (R7), права воркера; полный `./task backend:test` контроллера зелёный.

**Ловушки.** `docs/00` не ведётся. Правила — по-русски, коротко, со ссылками на спеку, без копий текста спеки.

### Task 21: воркер на стенде и выкатка I-2

**Цель.** Решение пользователя U1: письма игрокам (коды регистрации, сброса, смены почты) и чистки `maintenance`
работают на стенде с мержа I-2 — Blueprint-сервис воркера на Render с ролью `worker` и SMTP воркера; ключи стенда
задаёт контроллер (U2); документы — заглушки `0.1.0` (U3, приведено к формату §3.16), издатель TOTP — `WF dev` (U4).

**Риск:** B (платный сервис, секреты в панели, порядок выкатки: схема → api → воркер).

**Файлы.** Изменить: `render.yaml` (сервис `wf-worker` типа background worker: образ того же `backend/Dockerfile`,
команда воркера, `WORKER_DATABASE_URL` роли `worker` — `sync: false`, `WORKER_IDENTITY_SECRET_KEYS` — `sync: false`,
`WORKER_MAIL_*` — как `ADMIN_MAIL_*` стенда, тот же SMTP-провайдер, пароль `sync: false`; тариф — ниже),
`backend/internal/platform/config/render_stand_test.go` (`TestRenderYAMLNoWorker` заменяется `TestRenderYAMLWorker`:
воркер есть, секреты не в файле, срок остановки — `config.WorkerStopTime()` ≤ окна Render), `docs/deploy-dev.md`
(раздел «Воркер»: роль, env, порядок выкатки, проверка письмом в Mailpit/почту).

**Шаги вне кода (контроллер, по порядку).**
1. Показать пользователю тариф background worker Render и цену в месяц (с ссылкой на страницу цен); сервис создаётся
   только после его «да». Без «да» — задача останавливается на шаге 2 (файл готов, сервис не создан).
2. Сгенерировать `API_IDENTITY_SECRET_KEYS`, `API_IDENTITY_DEVICE_KEYS` (`openssl rand -base64 32`), задать в панели
   `wf-api`; то же значение секретов — `WORKER_IDENTITY_SECRET_KEYS` воркера; копия — файлом вне репозитория для
   менеджера паролей пользователя (как ключи admin-api). Значения не выводить в отчёты.
3. Строка подключения роли `worker` (Neon) — в `WORKER_DATABASE_URL`; проверить права (`grants.sql` применяет мигратор).
4. Выкатка: мерж → `wf-api` накатывает миграции и стартует → воркер стартует (ждёт схему) → проверка: регистрация на
   стенде, письмо с кодом пришло, `confirmSignup` → `201`.

**Тесты.** `TestRenderYAMLWorker` (выше); `TestRenderYAMLSecretsNotInFile` — с ключами воркера; `TestRenderYAMLStopBudget`
— с воркером.

**Подсадка.** Значение `WORKER_IDENTITY_SECRET_KEYS` в файле → `TestRenderYAMLSecretsNotInFile` красный.

**Приёмка.** Сервис создан только после «да» пользователя (в журнале сессии — цена и ответ); проверка шага 4 — в
отчёте (без кода и почты); `docs/deploy-dev.md` описывает воркер.

**Ловушки.** Воркер и api — одно кольцо секретов (иначе письма с кодами не собираются: `Open` даёт ошибку и задача
уходит в повтор). Воркер не должен стартовать раньше миграций — ожидание схемы, как у admin-api. Бесплатного
background worker у Render нет — не подменять его веб-сервисом с портом-заглушкой.

## Порядок исполнения

```
Волна 1 (платформа):  Исп. А: 1 → 3        Исп. Б: 2        Исп. В: 4 → 7 (7 — после коммита 1)
Волна 2 (схема):      Исп. А: 5 → 6        (Исп. В доделывает 7, если не успел; pnpm — только Task 5)
Волна 3 (ядро-основа):Исп. А: 8 → 9        (одна ветка: все стыки задач 10–18)
Волна 4 (сценарии):   Исп. А: 10 → 13      Исп. Б: 11 → 12      Исп. В: 14 → 15
Волна 5 (2FA):        Исп. А: 16           Исп. Б: 17 → 18 (18 — после коммита 16)
Волна 6 (фон, итог):  Исп. А: 19 → 20; затем контроллер — Task 21 (выкатка, после итогового ревью и ./task ci)
Ревью — одно на группу исполнителя в волне (wf-reviewer, opus, effort high); итоговое ревью ветки — после 20.
```

- Пути не пересекаются внутри волны: волна 1 — `platform/{otp,emailaddr}` + `staff/internal/domain` ∥
  `platform/flags` + `httpapi/admin/flags.go` ∥ `platform/config` + `render.yaml` (+ `identity/internal/domain`,
  `identity/events.go`); волна 4 — файлы-владельцы R5 (`app/<область>.go`, `httpapi/<область>.go`,
  `queries/<область>.sql`, `pending_<область>_test.go`). Общие файлы (`app/app.go`, `httpapi/handler.go`,
  `nostub_test.go`, `identitydb/models.go`, `cmd/api/main.go`) — у Task 8; `httpapi/handler.go` здесь —
  `backend/internal/identity/httpapi/handler.go` (N8); в брифах волн 4–5 — «Не трогай».
  Исключения: Task 11 и 12 правят `cmd/api/main.go` по одной строке, Task 12 — ещё
  `backend/internal/httpapi/public/handler.go` (монтаж `.well-known`, R14) — один исполнитель (Б), по очереди.
- `sqlc generate` параллельные исполнители запускают сами: вывод детерминирован по всем файлам `queries/`, общий
  только `models.go`, а он закреплён якорем Task 6 (R5). Расхождение `gen:check` — контроллер перед коммитом пачки.
- pnpm: только Task 5 (`./task gen`). В остальных задачах — `sqlc generate` и `go test` напрямую.
- Зависимости: 7 ← 1 (домен импортирует `otp`, `emailaddr`); 8 ← 5, 6, 7; 9 ← 1, 8; 10–15 ← 9 (10 ← 3, 4; 12 ← 2); 13 ← 10 (коды); 16 ← 9; 17 ← 5 (связь);
  18 ← 16, 17; 19 ← 15, 18; 20 — последней.
- Ворота перед ревью (контроллер): «Done when» задачи, typecheck/`go vet` пакета, `gofmt -l` по путям, сверка
  `git status` со списком путей отчёта.
- Пачки по 3–5 задач за сессию (`work-cadence`): «1 + 2 + 4 → 3 + 7» → «5 → 6» → «8 → 9» → «10 ∥ 11 ∥ 14» →
  «13 ∥ 12 ∥ 15» → «16 ∥ 17» → «18» (18 ← 16, 17) → «19 → 20». Не больше 4 opus-агентов одновременно.
- Коммит — контроллер, один на задачу; пример: «I-2 Task 10: регистрация по почте, коды из письма, ник и документы».
- После Task 20 — итоговое ревью ветки по областям (платформа и схема; ядро входа и сессии; 2FA и сброс
  поддержкой; фон), волна исправлений, полный `./task ci` (постоянная просьба пользователя 05.10), пуш и PR — по
  команде пользователя; выкатка на стенд — Task 21 (сервис воркера — после «да» пользователя на цену).
- Правило для брифов волн 4–5 (I9): красный тест — только через сигнатуру, объявленную Task 8; новая сигнатура или
  поле `Deps` — через контроллера. Порядок замков R30 — в «Ловушках» каждого брифа сценария.

## Решения плана

Решения, которых нет в спеке или которые её уточняют; поведенческие вносятся в спеку в Task 20.

- **R1. Раздел операций по планам** — Приложение 1: I-2 — 32 public (13 «вход и регистрация», 19 «аккаунт») и 2
  admin; I-3 — 14 public (соцвходы, passkeys, `createDeviceChallenge`, `startMfaPasskey`, `startReauthPasskey`); I-4 —
  4 public (капитанство) и 9 admin. Критерий — операция целиком реализуема без внешних провайдеров (Apple, Google,
  WebAuthn, аттестация) и без портов `players`/`teams`.
- **R2. Устройство в I-2 — минимум сессии.** `devices`/`device_accounts`, HMAC `install_id`, `device_required` (К5),
  нормализация `app_version` (хвост I-1) и лимит двух регистраций с мобильного (`signups`, правило домена §3.8 —
  не откладывается, чтобы не обходить правило даже временно). DeviceCheck, App Attest, Play Integrity, письмо о
  новом устройстве, `device_shared`, связанные аккаунты — I-3. Целостность — порт `Attestor`; до I-3 реализация
  «выключено» (режим `API_IDENTITY_ATTEST_MODE` вводит I-3; без него `observe` требовал бы PoW на каждый вход —
  сломанный клиент вместо проверки).
- **R3. Ветки I-3 в операциях I-2.** `completeMfa` с `passkey` → `passkey_invalid`, `reauthenticate` с `passkey`/`social`
  → `reauth_invalid` (ключей и привязок в I-2 нет — ответы совпадают с будущими «неизвестный ключ / не привязан»);
  `methods` шага 2FA содержит `passkey` по правилу §3.3 (ключи + флаг) — в I-2 никогда. Коды контракта не меняются.
- **R4. `getAuthMethods` в I-2** — через порт `ProviderReadiness` (реализация «нет провайдеров» до I-3; спека §11.3:
  способ без конфигурации у api выключен). Клиент с I-2 строит экран входа по реальной ручке.
- **R5. Файлы-владельцы и sqlc.** Заглушки `identity/httpapi` и `pendingOps` раскладываются Task 8 по файлам
  областей (`httpapi/<область>.go`, `pending_<область>_test.go`, `pendingOps = slices.Concat(…)`), сценарии — в
  `app/<область>.go`, запросы — `queries/<область>.sql`. Элемент sqlc identity — `emit_interface: false`, модели
  закреплены якорем `queries/models.sql` (Task 6): параллельные задачи меняют только свои `*.sql.go`. Если страж
  требует `emit_interface` — `querier.go` коммитит контроллер.
- **R6. Ядро входа — три примитива** (`checkPassword`, `verifySecondFactor`, `finishLogin`, Task 9). Порядок
  `finishLogin`: `banned` → 2FA → `pending_deletion` → сессия (удаление не раскрывает токен восстановления до
  второго фактора). Неудачи (попытки, счётчики, письма) коммитятся до ответа ошибкой.
- **R7. Периодика identity у обоих бинарников.** Лидер River ставит периодические задачи по своему списку, а лидером
  бывает admin-api; платформа модули не импортирует. Поэтому `identity/jobs.Periodic()` подключают `cmd/worker` и
  `cmd/admin-api` (`append(periodic.Jobs(), …)`), страж равенства множеств; исполнители — только воркер, у admin-api
  — заглушки `queue.AddForeign` по `WorkerKinds`.
- **R8. `staff_player_links` и `auth.Staff.LinkedUserID` — в I-2.** Без них `self_action` у `requestMfaReset`,
  подтверждения и `cancelMfaReset` (спека §7.6) вакуумен. Таблица — в миграции Task 5, загрузка поля — Task 17;
  ручки `putStaffPlayerLink`/`deleteStaffPlayerLink`, ник через порт identity и экран — I-4 (до тех пор связь
  заводит только тест; владелец без ручки связать учётку не может — поведение «связи нет»).
- **R9. Общие документы — одной задачей.** `docs/01`, спека бэкенда, `.claude/rules/backend-identity.md`, `CLAUDE.md`
  правят несколько задач — их правит Task 20 по разделам «Документы» отчётов 10–19. Документы с одним владельцем
  (`migrations/README.md` — 5, `deploy-dev.md` — 4, `versions.md`/`NOTICE` — 3, `backend-platform.md` — 2,
  `backend-staff.md` — 17, `backend-http.md` — 12, 17) — в своих задачах.
- **R10. Кольца identity на Render — `sync: false`.** Потеря кольца — потеря секретов TOTP и опознания устройств;
  значения генерирует контроллер при выкатке, копия — файлом вне репозитория (U2), как ключи admin-api.
  `generateValue` не подходит ещё и потому, что воркеру нужен тот же ключ.
- **R11. Номера миграций** — следующие свободные в момент Task 5 (сейчас `0024`, `0025`; спека §5.1).
- **R12. `flags.Store.Enabled*` учитывают `Requires`** — защита в глубину в платформе, а не в каждом сценарии
  (спека §4.2 пишет «сценарии считают» — одно место надёжнее; у прочих флагов правил нет — поведение не меняется).
- **R13. Хэшер в `cmd/api`** — `password.NewHasher(password.DefaultParams(), max(1, GOMAXPROCS))`, как в admin-api;
  отдельного env нет.
- **R14. `.well-known` монтирует `public.NewHandler`** (`Options.WellKnown`) до маршрутизатора контракта, как пробы и
  `/docs`: вне контракта, без лимитов `auth`, без валидатора.
- **R15. Метки HMAC** — `identity-code`, `identity-recovery`, `identity-device`, `identity-ratelimit` (спека §5.4);
  сообщение кода — `id ‖ код`, резервного — `user_id ‖ код`, устройства — `platform ‖ install_id`, лимита —
  `purpose ‖ email` / `login ‖ email`.
- **R16. Ключи лимитов сценариев** — `ratelimit.EmailKey("login"|"auth_send", mac)`, `ratelimit.SubjectKey("mfa", …)`
  для `user:mfa` (`completeMfa` и ветка 2FA `confirmPasswordReset`). Неудачи свежего входа — колонки
  `sessions.reauth_failures` и `reauth_failed_at` (новая колонка сверх §5.3, Task 5; I8): окно 15 минут от первой
  неудачи, истекло — счёт с нуля, успех обнуляет; `attempts_left` и `429` + `Retry-After` — из них. Ключ лимитера
  `user:reauth:<sid>` отвергнут: `attempts_left` из лимитера не получить, а две меры (ключ и колонка) расходились бы.
- **R17. Сверх 100 живых сессий** — старшие по `last_seen_at` отзываются при создании новой с причиной `revoked`
  (спека §6.3 `listMySessions`; отдельной причины в списке §5.3 нет).
- **R18. `sessions.device_id` — `NULL` допустим**: веб без метки BFF (`auth.Principal.DeviceID = uuid.Nil` уже
  предусмотрен). Мобильный без `device` не доходит до сессии (`device_required`).
- **R19. `client_kind`, IP и устройство** — только из `peer.Info` (`ViaBFF`, `IP`, `Device`); тело `device` от BFF
  игнорируется (§6.2.3).
- **R20. Хук отмены сброса 2FA** — `onSecondFactorLogin` в tx `finishLogin`; файл создаёт Task 9 пустым, тело —
  Task 18 (иначе 16 и 18 правили бы один файл).
- **R21. Письма identity из admin-api** — `mail.SendArgs` в очередь `mail` клиентом вставки admin-api; исполняет
  воркер (`WorkerKinds` — `mail.send`), а не очередь `staff`.
- **R22. Аудит `identity.mfa_reset_requested`** пишет исполнитель от подтверждающего (подпись решения), инициатор и
  `approval_id` — в `meta`; запрос — аудит платформы `platform.approval_requested`.
- **R23. `confirmPasswordReset` целиком — Task 13** (ветка 2FA на примитивах Task 9), не этап 5: иначе операция
  жила бы наполовину.
- **R24. `users_email_format`** принимает каноническую форму `emailaddr` **и** заглушку анонимизации
  `deleted+<uuid>@invalid` (одноуровневый домен); тест — оба случая.
- **R25. UA — свой разбор в домене** (браузер и ОС для «Где вы вошли»), без зависимости: нужно десять семейств, а
  библиотеки UA тянут базы сигнатур.
- **R26. Письмо `signup_account_exists`** — одно на старт регистрации; повтор отправки приманки письма не шлёт
  (спека §3.14 «повтор отправки не шлёт письма»), пауза и счётчик — как у настоящего кода.
- **R27. Свежесть вторым фактором** (`disableTotp`, спека §3.13, §6.3 «да (вторым фактором)») — признак
  `sessions.mfa_at` не старше 10 минут; `mfa_at` ставят вход со вторым фактором и `reauthenticate` TOTP или резервным
  кодом. Отдельной колонки не нужно.
- **R28. Ротация кольца identity — `api identity rekey`** (I4): долгоживущие секреты (`user_mfa.secret_enc`,
  `user_identities.apple_refresh_enc`) — в таблицах, где `UPDATE` есть только у `api`; у `worker` чтение этих
  секретов запрещено §5.5 намеренно. Короткоживущие (`auth_codes`, `auth_challenges`, `security_events.meta_enc` ≤ 7
  дней) не перешифровываются. MAC резервных кодов не пересчитывается: старый ключ убирается из кольца только когда
  под ним не осталось `user_recovery_codes` (перевыпуск, спека §5.4) и не раньше 7 дней после ротации (отказ 2
  раунда 2). Сборщик — пакет `identity/rekey`, не корень: корень импортируется `app`, обратный импорт — цикл (N2).
  Спека §5.4 («`worker identity rekey`») правится в Task 20. Вариант «колоночные права `worker` на секреты» отвергнут:
  он снимает страж `TestIdentitySecretsHidden`.
- **R29. `banned` и операции без `account_blocked` в контракте** (I5): контракт заморожен, у `confirmPasswordReset` и
  `completeMfa` кода `account_blocked` нет. `startPasswordReset` для `banned` — приманка без письма (сброс не ведёт
  ко входу; знающий пароль узнает о бане в `login`); бан между шагами — `410` кода или `mfa_token`. Место проверки:
  сброс — сразу после верного MAC, до ветвей 2FA, без записей, и повторно в tx2 (N4); `completeMfa` — сразу после
  `takeChallenge`, токен гасится до `verifySecondFactor` (отказ 3 раунда 2); `finishLogin` с `blockedAsExpired` —
  защита в глубину. Вариант «правка среза через журнал заморозки» отвергнут: он меняет контракт ради состояния,
  которого клиент не исправит.
- **R30. Порядок замков identity** (I2): `users` → `sessions` → `refresh_tokens` → `devices` →
  `user_mfa`/`user_recovery_codes` → `mfa_resets` → `auth_codes`/`auth_challenges`; сценарий, меняющий аккаунт или
  его сессии, первым берёт `users FOR NO KEY UPDATE` (`lockUser`); задачи воркера по аккаунтам — `SKIP LOCKED` на
  `users`. Строка `approvals` — раньше строк модуля (платформа). **Аккаунт по строке** (N1): если `user_id` известен
  только из строки `auth_codes`/`auth_challenges` (код, `mfa_token`, `restore_token`) или `refresh_tokens` (R3-3),
  строка ищется без замка →
  `lockUser` → `FOR UPDATE` строки с перепроверкой (не погашена, не отозвана, не истекла, тот же `user_id`); строка
  без `user_id` — без `lockUser`. Отвергнуто «снять гашение кодов из `deleteAccount`»: гашение закрывает отказ 1
  раунда 1 (живой код смены почты в периоде удаления), а перенос в проверку статуса при подтверждении размазывает
  правило по каждой операции с кодом.
- **R31. argon2 вне транзакции** (I10, N10): `Verify` и `Hash` — и проверка текущего, и хэш **нового** пароля — не
  держат соединение пула и замок `users`; в tx — сверка, что хэш не изменился, и условная запись. `changePassword` —
  хэш до tx; `confirmPasswordReset` — две tx: сверка кода и фактора → хэш вне tx → `lockUser`, перепроверка кода,
  запись и погашение.
- **Не принято из ревью плана.** Непредусмотренный отказ 3 раунда 1 (CGNAT и PoW по `risk.Signup` при `AttestOff`) —
  пороги `risk.DefaultPolicies` спека §6.4 не меняет; наблюдение на стенде после Task 21, правка порогов — отдельно
  по данным. Остальные пункты ревью раунда 1 (C1, I1–I10, m1–m13, отказы 1–2) и раунда 2 (N1–N10, I4, m12, отказы
  2–3, наблюдения) внесены; отказ 1 раунда 2 — в «Передача следующим планам» (I-4).

## Решения пользователя

Источник — `.superpowers/sdd/2026-11-13-identity-2/decisions.md` (13.11.2026: «выбирай лучший и правильный вариант
везде»). Важнее «Решений плана».

- **U1. Воркер на Render — в конце I-2** (письма игрокам и чистки `maintenance`): Task 21; платный сервис создаётся
  после показа пользователю тарифа и цены и его «да».
- **U2. Ключи стенда** `API_IDENTITY_SECRET_KEYS`, `API_IDENTITY_DEVICE_KEYS` (и `WORKER_IDENTITY_SECRET_KEYS`)
  генерирует и задаёт контроллер при выкатке, копия — файлом вне репозитория (R10, Task 21).
- **U3. `API_LEGAL_DOCUMENTS` на стенде** — заглушки версии `0.1.0` (U3, приведено к формату §3.16) со ссылкой-заглушкой; настоящие тексты — сменой
  версии (игроки примут заново, §3.16).
- **U4. Издатель TOTP на стенде** — `WF dev`; на проде — имя бренда (не финален).

## Передача следующим планам

| Хвост | Куда |
| --- | --- |
| 14 операций public: соцвходы (`createSocialNonce`, `socialLogin`, `completeSocialSignup`, `linkIdentity`, `unlinkIdentity`), passkeys (`startPasskeyLogin`, `passkeyLogin`, `startPasskeyRegistration`, `registerPasskey`, `renamePasskey`, `deletePasskey`, `startMfaPasskey`, `startReauthPasskey`), `createDeviceChallenge` | I-3 |
| Ветки `passkey`/`social` в `completeMfa`, `reauthenticate`; `methods` с `passkey`; `ProviderReadiness` из конфига Apple/Google/WebAuthn (R3, R4) | I-3 |
| `Attestor` — App Attest, Play Integrity, DeviceCheck, `API_IDENTITY_ATTEST_MODE`, письмо `new_device`, событие `device_shared`, связанные аккаунты (R2) | I-3 |
| Отзыв токена Apple при отвязке и анонимизации (точка расширения в Task 15), `rekey` для `apple_refresh_enc` уже покрыт | I-3 |
| Ключи env Apple, Google, WebAuthn, App Attest, Play Integrity (§11.3); библиотеки `go-webauthn`, `go-oidc`, `cbor`, `playintegrity` | I-3 |
| 4 операции капитанства public, 9 операций admin, порты `CaptainCheck` и тег `wf_e2e`, `intx` (`RevokeRoleInTx`, `SetRestrictionsInTx`, `ChangeCityInTx`), `Service.CaptainPermits`, письма и события заявок | I-4 |
| Ручки связи «сотрудник → игрок», ник через порт identity (R8, R15 плана I-1), декодирование заголовков `X-WF-*` | I-4 |
| Экраны админки (капитаны, сброс 2FA, связь), флаги способов входа в админке (`getAuthMethodsUsage`, подтверждение аварийного), тексты причин | I-4 |
| Стенд: живой e2e identity, ключи Apple/Google/WebAuthn (воркер и SMTP — Task 21 этого плана) | I-4 |
| Удаление `pendingOps`, `TestPendingOpsExpire`, решение о `identity-freeze` | последняя задача I-4 (R2, R17 плана I-1) |
| Хвосты §16: спека бэкенда §4.4 (`GrantRoleInTx`), `docs/05` (U5 применена), ответ нативщику | I-4 |
| Наблюдение (отказ 1 раунда 2 ревью плана): `startCode` гасит прежний живой код на `(purpose, email)` — вошедший, запустив `startEmailChange` на чужой свободный адрес, отзывает чужой живой код смены почты на тот же адрес (тот получает `410`). Правило спеки «один живой код на `(purpose, email)`»; решить — ключ «один живой код» на `(purpose, user_id, email)` или оставить | I-4 (спека) |

## Сверка хвостов плана I-1 (раздел «Передача следующим планам»)

| Хвост I-1 | Задача I-2 |
| --- | --- |
| Enum `FeatureFlagKey` + шесть ключей + подписи флагов (R4) | 5 |
| `flags.Rules` и выдача `platform.flag_global_only`/`flag_requires` | 2 |
| `captain_applications` → владелец `identity` | 6 |
| Исполнитель `identity.mfa_reset`, `TestAssembleRegistersIdentityActions` | 17 |
| Поля `reasons`/`methods`/`available_at`/`blocked_until` и хелперы `With…` (m5) | 3 |
| Нормализация `DeviceInfo.app_version` | 7 (домен), 8 (сохранение) |
| Сквозной `no-store` в конвейере public (m15) | 16 |
| `auth.Staff.LinkedUserID` | 17 (R8); ник в `StaffPlayerLink` — I-4 |
| Хвосты документов §16 в части этапов 2–5 | 2–5, 12, 17, 20 (R9) |
| Удаление `pendingOps`, решение о стражах заморозки, тексты причин, заголовки `X-WF-*` | I-4 |

# Приложения

## Приложение 1. Операции по задачам и файлам-владельцам

| Задача | Файл заглушек / `pending_*` | Операции |
| --- | --- | --- |
| 10 | `signup` | `checkNickname`, `startSignup`, `confirmSignup`, `resendAuthCode`, `getLegalDocuments` |
| 11 | `login` | `login`, `refreshSession`, `logout` |
| 12 | `sessions` | `listMySessions`, `revokeMySession`, `revokeOtherSessions`, `getAuthMethods` |
| 13 | `password` | `startPasswordReset`, `confirmPasswordReset`, `reauthenticate`, `changePassword`, `startEmailChange`, `confirmEmailChange` |
| 14 | `account` | `getMe`, `updateMe`, `changeNickname`, `acceptDocuments`, `listMyAuthMethods` |
| 15 | `deletion` | `deleteAccount`, `cancelDeletion` |
| 16 | `mfa` | `getMyMfa`, `setupTotp`, `confirmTotp`, `disableTotp`, `regenerateRecoveryCodes`, `completeMfa` |
| 18 | `mfa_reset` | `cancelMyMfaReset` |
| 17 (admin) | `identity/admin`, `pendingOps` admin | `requestMfaReset`, `cancelMfaReset` |
| I-3 | `pending_i3` | 14 операций (см. «Передача») |
| I-4 | `pending_i4` | `getMyCaptaincy`, `listMyCaptainApplications`, `submitCaptainApplication`, `withdrawCaptainApplication` |

Итого I-2: 5 + 3 + 4 + 6 + 5 + 2 + 6 + 1 = 32 public, 2 admin. Анонимные мутации I-2 (9): `startSignup`,
`confirmSignup`, `resendAuthCode`, `login`, `completeMfa`, `startPasswordReset`, `confirmPasswordReset`,
`refreshSession`, `cancelDeletion`.

## Приложение 2. Письма, события, аудит I-2

- **Письма** (§8.2): `signup_code`, `signup_account_exists` (10); `password_reset_code`, `email_change_code`,
  `email_change_taken`, `email_changed`, `password_changed` (13); `session_revoked_suspicious` (11);
  `mfa_enabled`, `mfa_disabled`, `recovery_codes_regenerated` (16); `recovery_code_used`, `mfa_failures` (9);
  `account_deletion_scheduled`, `account_restored`, `account_deleted` (15); `mfa_reset_scheduled`,
  `mfa_reset_cancelled` (17, 18), `mfa_reset_done` (18). Сборка — Task 19.
- **События** (§8.1): `user_registered` (10), `user_deletion_scheduled`, `user_deletion_cancelled`, `user_deleted`
  (15), `user_email_changed`, `password_changed` (13), `nickname_changed` (14), `mfa_changed` (16, 18),
  `sessions_revoked` (8), `restrictions_changed` (19), `mfa_reset_scheduled` (17), `mfa_reset_cancelled` (17, 18).
- **Аудит** (§8.3): `user_registered`, `login_failed`, `password_changed`, `password_reset`, `email_changed`,
  `nickname_changed`, `documents_accepted`, `mfa_enabled`, `mfa_disabled`, `sessions_revoked`,
  `user_deletion_requested`, `user_deletion_cancelled`, `user_deleted`, `mfa_reset_requested`, `mfa_reset_cancelled`,
  `mfa_reset_executed` — константы Task 8, запись — задача сценария.
