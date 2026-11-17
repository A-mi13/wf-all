# Identity I-4: капитанство и админка — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

> **Формат — бриф задач, без кода реализации** (решение пользователя 13.10.2026, память `plan-format-brief`). Каждая
> задача: цель, риск, файлы, точные интерфейсы (сигнатуры Go и TS, таблицы и колонки, operationId), тесты (имя →
> вход → ожидаемое), подсадка, приёмка, «Done when», ловушки, «Не трогай». Как писать SQL, Go и TSX — решает
> исполнитель по TDD, спеке и `.claude/rules/`. Авторитет: спека → «Решения пользователя» → «Решения плана» (R,
> явно отступающее от спеки, действует до правки спеки в Task 10) → тексты задач. Где задача расходится со спекой без
> ссылки на R — права спека, задача — ошибка плана (сообщить контроллеру).

**Goal:** этап 9 спеки identity (§15) и хвосты плана I-2 («Передача следующим планам», строки 1828–1837; требования
к I-4 — спека §17):
- **капитанство public:** `getMyCaptaincy`, `listMyCaptainApplications`, `submitCaptainApplication`,
  `withdrawCaptainApplication` — условия через порты `CaptainCheck` (реализация «недоступно» в проде, тестовые порты
  в Go-тестах и в бинарниках с тегом `wf_e2e`), пауза 7 дней после отказа;
- **админка identity:** очередь и карточка заявок, контакты заявителя с причиной, одобрение и отказ (письма, события,
  аудит), поиск игрока по почте для поддержки, статистика способов входа для экрана флагов;
- **связь «сотрудник → игрок»:** `putStaffPlayerLink`, `deleteStaffPlayerLink`, поле `StaffAccount.player_link`,
  ник через порт identity, подъём версии учётки (закрывает хвост «связь инициатора сброса 2FA»);
- **`identity/intx`:** `RevokeRoleInTx`, `SetRestrictionsInTx`, `ChangeCityInTx`; `Service.CaptainPermits`;
  требования §17 — истечение бана возвращает статус, бан поверх удаления, актуальный `status_before_deletion`,
  статус в `requireFresh` и `deleteAccount`;
- **фронт админки:** разделы «Капитаны» и «Игроки» (сброс 2FA), блок связи в карточке сотрудника, флаги способов
  входа (только «везде», аварийный выключатель с числом «входят только так»), журнал и подтверждения для действий
  identity;
- **закрытие I-4:** живой e2e (admin e2e + `api` с тегом `wf_e2e`), документы §16, сокращение `pendingOps` до
  операций I-3 (R1), выкатка на стенд.

Итог по заглушкам: public `pendingOps` 18 → 14 (только I-3), admin `pendingOps` 9 → 0 (список и его страж удаляются).

**Не здесь (план I-3, отложен до подготовки к выпуску в сторы — решение пользователя 16.11):** соцвходы, passkeys,
аттестация устройств, `createDeviceChallenge`, письмо `new_device`, `device_shared`, ключи Apple/Google/WebAuthn —
14 операций public остаются заглушками 501; заморозка контракта identity остаётся до последней задачи I-3 (R1).
Не здесь также: модули `players`, `teams`, `moderation` (порты и `intx` здесь — только механизм и вызывающие из
тестов), веб (BFF).

**Architecture:**
- **Порты условий** (спека §10.3, К1): корень `identity` объявляет `CaptainCheck` и `RequiredCaptainChecks`;
  `identity.Deps` и новый `identity.AdminDeps` несут `CaptainChecks`; `app.New`/`app.NewAdmin` отказывают, если имена
  не равны ровно `RequiredCaptainChecks`. Прод собирает `identity.UnavailableCheck(name)`; тестовые порты —
  `identity/identitytest` (Go-тесты) и файлы `cmd/{api,admin-api}/captaincy_e2e.go` под `//go:build wf_e2e`.
- **Одно правило условий** (R5): `app.evaluateCaptaincy` — для `getMyCaptaincy`, `submitCaptainApplication`,
  карточки и одобрения; порты зовутся в транзакции вызывающего (`pgx.Tx`).
- **Одобрение под ролью `admin`** (К2, R6): замок строки заявки `FOR UPDATE`, без замка `users` (у `admin` нет
  `UPDATE` на `users`), допуск `role_assignments` и заявка `approved` одной транзакцией, события на агрегате
  `captain_application`, письмо `mail.SendArgs` в очередь `mail` (R21 плана I-2).
- **Связь «сотрудник → игрок»** — модуль `staff` (слой 0): порт `staff.PlayerDirectory` (ник и существование),
  адаптер над `identity.Service` — в `cmd/admin-api`.
- **`intx`** — пакет `identity/intx` поверх `internal/app`: методы с `pgx.Tx` вызывающего; вызывающих в этом плане
  нет, кроме тестов (`teams`, `moderation` — их спеки).
- **Фронт** — `apps/admin/src/features/{captains,players}`, правки `features/{staff,flags,approvals,audit,home}`;
  данные — по сгенерированным типам замороженного контракта, моки MSW; один исполнитель (pnpm и общие файлы, R18).

**Tech Stack:** Go 1.27 (chi, pgx v5, sqlc с грамматикой PG17, goose, River 0.x), kin-openapi, oapi-codegen strict;
React 19 + Vite 8, TanStack Router/Query, openapi-fetch, MSW, Vitest, Playwright, Ladle. Новых зависимостей нет.

**Spec:** `docs/superpowers/specs/2026-11-13-identity-design.md` (дальше «спека»): §0, §0.1, §2.3, §3.11, §3.12,
§3.15 п. 7, §4.3, §5.2–5.6, §7 целиком, §8, §10, §13, §15 (этап 9, 10), §16, §17. Контракт — замороженный срез I-1
(`contracts/openapi/{public,admin}/identity/`, `admin/staff/paths.yaml` `account-player-link`, страж
`contracts/test/identity-freeze.test.mjs`). Планы: I-1 `docs/superpowers/plans/2026-11-13-identity-1-contract.md`
(R1, R2, R15, R17–R19), I-2 `docs/superpowers/plans/2026-11-13-identity-2-core.md` (R5, R7, R8, R20–R22, R29, R30;
«Передача следующим планам»). UI — `docs/superpowers/specs/2026-10-28-ui-foundation-design.md`, правила
`.claude/rules/{admin,admin-ui,admin-staff,admin-e2e}.md`. Образец формата — план I-2.

**Предусловие старта.** (1) Ветка `spec/embedded-worker` слита в `main`: она правит `backend/cmd/**`,
`backend/internal/platform/{queue,config}/**`, `deploy/server/**`, `.github/**` — Task 2, 6, 11 правят
`cmd/api/main.go`, `cmd/admin-api/main.go` и сборку e2e. Не слита — Task 2 ждёт. (2) Ветка `spec/identity-4` от
`origin/main`. (3) Номер миграции — следующий свободный в момент Task 1 (сейчас `0027`; план I-3 пишется параллельно
и берёт номер после I-4 — R2). (4) План I-3 (`docs/superpowers/plans/2026-11-16-identity-3-devices-social-passkeys.md`)
исполняется после I-4 и наследует `pendingI3`, `TestPendingOpsExpire` public и решение о заморозке (R1, «Передача»).

## Global Constraints

- **Правила репо** — `CLAUDE.md`, `.claude/rules/{backend,backend-schema,backend-platform,backend-http,backend-identity,backend-staff,env,contracts,admin,admin-ui,admin-staff,admin-e2e,admin-geo}.md`.
  TDD: сначала падающий тест; каждый новый страж проверяется подсадкой (`node scripts/plant.mjs …`, в отчёт — что
  подсажено и какой тест упал).
- **Риск задачи** — A/B/C по `.claude/agents/wf-implementer.md`: для A исполнитель до первого теста пишет таблицу
  режимов отказа (гонки, повторы, права в момент действия — роль сотрудника и его города, связь с игроком, частичный
  отказ, время и таймзона, перечисление аккаунтов, ПД в журналах); ревьюер проходит 10 категорий.
- **Контракт identity заморожен** (I-1, R17): YAML среза не правится. Единственная правка контракта плана — поле
  `FeatureFlag.rules` платформы (R3, Task 1): вне среза identity, хэш среза не меняется. Расхождение контракта и
  сценария — стоп и вопрос контроллеру. Новых кодов ошибок нет: коды I-4 уже в `identity/codes.go`,
  `staff/codes.go`, локалях.
- **Заглушки:** реализованная операция в той же задаче уходит из своего файла `pending_<область>_test.go`
  (public — `backend/internal/httpapi/public/`, admin — `backend/internal/httpapi/admin/`, раскладка admin — Task 2,
  R16); `pendingOpsAtFreeze` и хэши не меняются до Task 12.
- **Коммиты** — делает контроллер (субагенты не коммитят): пути и сообщение — в разделе «Коммит» отчёта; одна строка
  по-русски без тела; `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A/.`, `commit -a`,
  `stash`, `reset`, `clean`, `checkout -- .`. Доки нативщика `docs/WF_…_2026_10_19.md` и `docs/screens/` — не
  коммитить и не править.
- **Код:** комментарии по-русски, идентификаторы по-английски, LF. Время — только `clock.Clock` (тесты —
  `clocktest.Fake`), id — UUIDv7 из Go (`id.New`). Строки UI — только `apps/admin/messages/{ru,en}.json`, письма —
  `backend/locales/{ru,en}.json` (en — те же ключи).
- **ПД** (спека §1.4, §9.6): почта, телефон, дата рождения, причина доступа и номер обращения — не в событиях, не в
  аргументах задач, не в логах, не в `Before/After` аудита; причина доступа — в `audit_log.reason` (поле записи, не
  `meta` с ПД); на фронте — только в памяти экрана (R19).
- **Транзакции:** ручка с `Idempotency-Key` — ровно одна закоммиченная `db.InTx`; событие, аудит, задача письма — в
  транзакции действия; чтения с аудитом (`audit.WriteRead`) — в той же транзакции, что чтение. Порядок замков —
  R30 плана I-2 с дополнением R7.
- **Права ролей БД** — §5.5 без расширения: `admin` не пишет `users`/`sessions` (К2), секретов не читает; всё, что
  admin-api должен знать о секретах (способы входа), — агрегирующим представлением (R2).
- **Тесты:** исполнитель гоняет тесты своих пакетов (`go test -p 4 ./internal/identity/... ./internal/httpapi/admin/...`
  из `backend/`; Postgres — `bash scripts/pg.sh start` в фоне) и typecheck своего пакета; фронт — `./task admin:test`
  (pnpm — один процесс на машину: в волне фронт-исполнитель единственный, кто запускает pnpm, R18); полный
  `./task backend:test`, `./task admin:test`, `./task packages:ci` — контроллер в конце пачки. Тесты с базой —
  параллельные (`t.Parallel()`, `dbtest.Main`), проверки ролей — под ролью прода (`dbtest.NewPoolsAs`, `SET ROLE`).
- **Кодогенерация:** бэкенд — прямой `sqlc generate` (из `backend/`); `./task gen` (pnpm) — только Task 1 (поле
  `FeatureFlag.rules`) и, если меняется `docs/05` таблица прав, — Task 10 (по плану — нет).
- **Окружение:** новых env-ключей план не вводит (порты — на этапе сборки, R4). Появился ключ — по
  `.claude/rules/env.md` сразу в конфиг, `.env.example`, `.env` (скриптом), `render.yaml`.

## Review Focus

1. **Правило не обходится** (К1). Прод-сборка `api` и `admin-api` подключает для обоих имён только
   `UnavailableCheck`: подача и одобрение — `409 identity.captain_ineligible {reasons: [checks_unavailable]}`;
   тестовые порты есть только в бинарниках с тегом `wf_e2e` и в Go-тестах; опечатка или пропуск имени порта — отказ
   старта. Task 2, 3, 4.
2. **Одно правило условий.** `getMyCaptaincy.eligibility`, отказ подачи, галочки карточки и перепроверка при
   одобрении считаются одной функцией (R5): возраст по таймзоне города на «сегодня», ограничение `captain`, порты,
   допуск в городе, пауза 7 дней (`available_at`), открытая заявка. Task 3, 4.
3. **Гонки заявки.** Две подачи одного игрока; подача и удаление аккаунта; отзыв и одобрение; два одобрения; одобрение
   и отказ; одобрение и удаление — ровно один исход, без `40P01` и `500`; одобрение под ролью `admin` без замка
   `users` (R6). Task 3, 4.
4. **Права в момент действия и скоуп.** Модератор видит и решает только заявки своих городов (чужая — `404`, как
   несуществующая; фильтр чужого города — пустая страница; ветвление по `GrantScoped`, модератор без городов не
   видит ничего — R37); `findPlayerByEmail` модератору — только игроки его городов; `self_action` у одобрения и
   отказа — по связи, перечитанной в транзакции решения (R38), не по `auth.Staff.LinkedUserID` загрузки; связь
   сотрудника — `lockAccounts` и `checkActor` под замком; поддержка не решает. Task 4, 5, 6.
5. **ПД.** Контакты и поиск — только с причиной (заголовок, percent-encoding, 3–200 символов), каждое чтение и каждый
   поиск (в том числе «не найдено») — в журнале без почты; ответы без кэша (`Cache-Control: private, no-store` — `CachePrivate` контракта, m3); на фронте почта и причина не
   попадают в адрес, storage, черновики, «Недавнее» палитры. Task 4, 5, 9.
6. **Связь «сотрудник → игрок».** Только владелец; учётка занята — `409 staff.player_link_taken`; удалённый игрок —
   `422 staff.player_not_found`; PUT и DELETE поднимают `staff_accounts.version` — запрос сброса 2FA, созданный до
   связи, при подтверждении отсекается `InitiatorCheck` (R14). Task 6.
7. **Бан и статус** (§17). Снятие последнего живого ограничения `login` (чисткой или `SetRestrictionsInTx`) возвращает
   `banned → active` в той же транзакции с событием и аудитом; бан поверх `pending_deletion` не теряет удаление и не
   затирает `purge_at` (R11); `status_before_deletion` актуален; «заблокирован» — один предикат во всех местах
   (вход, второй фактор, отмена удаления, сброс пароля, письмо сброса — R36); живая строка `login` без бана —
   отказ (R42); `requireFresh`/`deleteAccount` при забаненном под замком — `401`, не `500`. Task 7.
8. **События и письма.** Payload заявок — только id и коды (golden-файлы); `captain_permit_granted/revoked` — на
   агрегате `captain_application`; удаление аккаунта публикует `captain_application_withdrawn`, анонимизация —
   `captain_permit_revoked` (R24); письма решения — по `ref = captain_applications.id`, язык игрока, причина отказа
   текстом локали. Task 2, 4, 7.
9. **Фронт.** Только компоненты `src/ui`, тексты в локалях, права из `me.permissions`, `DataState`/`DataTable`, панели в
   адресе, мутации с ключом идемпотентности; `Одобрить` недоступно, пока хоть одно условие не `ok`; аварийный флаг —
   диалог с числом «входят только так»; глобальный флаг — без городов. Task 8, 9.

---

## File Structure

| Путь | Ответственность | Задача |
| --- | --- | --- |
| `backend/migrations/{0027_identity_captaincy.sql,identity_0027_test.go,README.md}`, `backend/internal/platform/grants/grants_test.go`, `backend/internal/archtest/ownership.go`, `backend/internal/identity/queries/models.sql`, `backend/internal/identity/internal/store/identitydb/*`, `contracts/openapi/admin/platform/schemas.yaml`, `contracts/openapi/admin/root.yaml` (версия), `backend/internal/httpapi/admin/{flags.go,platform_flags_test.go}`, сгенерированное (`contracts/openapi/admin.yaml`, `backend/internal/httpapi/admin/oapi/*`, `apps/admin/src/api/gen/admin.d.ts`), `backend/internal/identity/internal/app/codes.go` (+ тест), `backend/internal/identity/queries/codes.sql` (R40), `apps/admin/src/test/{geo,flags}.ts`, `apps/admin/src/api/undo.test.tsx` | схема I-4, правила флагов в контракте, живые коды смены почты | 1 |
| `backend/internal/identity/{captaincy.go,identity.go,events.go}` (+ `testdata/events/*.golden.json`), `backend/internal/identity/identitytest/*`, `backend/internal/identity/internal/domain/captaincy.go`, `backend/internal/identity/internal/app/{app.go,audit.go,captain_rules.go}`, `backend/internal/identity/admin/{handler.go,captains.go,players.go,headers.go}`, `backend/internal/httpapi/admin/{nostub_test.go,pending_captains_test.go,pending_players_test.go,pending_staff_link_test.go}`, `backend/internal/httpapi/public/identity_core_test.go`, `backend/cmd/api/{main.go,captaincy_prod.go,captaincy_e2e.go,captaincy_test.go}`, `backend/cmd/admin-api/{main.go,captaincy_prod.go,captaincy_e2e.go,captaincy_test.go,staff_links.go}`, `backend/internal/staff/{linked.go,queries/linked.sql}` (+ `staffdb`), `backend/locales/{ru,en}.json`, `backend/Taskfile.yml` (`lint`: vet с тегом), сборки `identity.Deps`/`identityadmin.New` в тестах (`httpapi/public/{handler_test,identity_core_test,pending_signup_test}.go`, `identity/internal/app/{helpers_test,sessions_test}.go`, `httpapi/admin/{identity_mfa_reset_test,permissions_test}.go`, `cmd/admin-api/identity_wiring_test.go`) | каркас: порты (`CaptainCheck`, `StaffLinks`), сборка, `app.NewTx`, домен, каталог событий и аудита, раскладка заглушек admin, ключи писем | 2 |
| `backend/internal/identity/internal/app/captaincy.go`, `backend/internal/identity/queries/captaincy.sql`, `backend/internal/identity/httpapi/captaincy.go` (вместо `pending_i4.go`), `backend/internal/httpapi/public/{captaincy_test.go,nostub_test.go (строка Concat)}` (вместо `pending_i4_test.go`) | капитанство игрока | 3 |
| `backend/internal/identity/internal/app/captain_admin.go`, `backend/internal/identity/queries/captain_admin.sql`, `backend/internal/identity/admin/captains.go`, `backend/internal/identity/mail/composer.go` (+ тест), `backend/internal/identity/internal/app/mail.go` (`MailKinds`), `backend/internal/httpapi/admin/{pending_captains_test.go,identity_captains_test.go}` | заявки в админке, письма решения | 4 |
| `backend/internal/identity/internal/app/lookup.go`, `backend/internal/identity/queries/lookup.sql`, `backend/internal/identity/admin/players.go`, `backend/internal/httpapi/admin/{pending_players_test.go,identity_lookup_test.go}` | поиск игрока, статистика способов входа | 5 |
| `backend/internal/staff/{staff.go,dto.go,mail.go}`, `backend/internal/staff/internal/app/{player_link.go,mail.go,accounts.go}`, `backend/internal/staff/queries/player_links.sql`, `backend/internal/staff/internal/store/staffdb/*`, `backend/internal/staff/admin/accounts.go`, `backend/cmd/admin-api/{main.go,players_port.go}`, `backend/internal/httpapi/admin/{pending_staff_link_test.go,staff_player_link_test.go}`, `backend/locales/{ru,en}.json` — только если ключей Task 2 не хватило (через контроллера) | связь «сотрудник → игрок» | 6 |
| `backend/internal/identity/intx/*`, `backend/internal/identity/internal/app/{restrictions.go,permits.go,city.go,cleanup.go,reauth.go,deletion.go,anonymize.go,login_core.go,password.go,mfa_login.go,mail.go}`, `backend/internal/identity/mail/composer.go` (после коммита Task 4), `backend/internal/identity/queries/{restrictions.sql,permits.sql,cleanup.sql,deletion.sql}`, `backend/internal/identity/identity.go` (`Service.CaptainPermits`) | `intx`, бан и статус, события удаления | 7 |
| `apps/admin/src/features/captains/*`, `apps/admin/src/app/{paths.ts,nav.ts,routes.tsx,filterKeys.ts,commands.ts}`, `apps/admin/src/api/client.ts` (`ApiError.reasons`), `apps/admin/src/features/{home,audit}/*`, `apps/admin/src/test/{identity.ts,defaults.ts,sections.ts}`, `apps/admin/src/shots/fixtures.ts`, `apps/admin/messages/{ru,en}.json` | раздел «Капитаны», общие файлы фронта I-4 | 8 |
| `apps/admin/src/features/players/*`, `apps/admin/src/features/staff/{StaffDrawer.tsx,PlayerLinkBlock.tsx,…}`, `apps/admin/src/features/flags/*`, `apps/admin/src/features/approvals/summary.ts`, `.claude/rules/admin-identity.md` (новый), `design/README.md` | «Игроки», связь в карточке, флаги, сводка подтверждения | 9 |
| `.claude/rules/{backend-identity,backend-staff,backend-http}.md`, `CLAUDE.md`, `docs/01-домен-и-правила.md`, `docs/05-ролевая-модель.md`, спека бэкенда, спека identity, `backend/migrations/README.md` (если не Task 1) | документы этапа | 10 |
| `scripts/e2e-admin.sh`, `apps/admin/e2e/{env.ts,keys.ts,captaincy.spec.ts,identity-api.spec.ts,api.ts}`, `apps/admin/playwright.config.ts` | живой e2e | 11 |
| `backend/internal/httpapi/public/{nostub_test.go,identity_core_test.go,pending_i3_test.go}`, `backend/internal/httpapi/admin/{nostub_test.go,pending_*_test.go}`, `.claude/rules/{contracts,backend-http,backend-identity}.md`, `.superpowers/sdd/2026-11-13-identity-1/native-handoff.md` (журнал) | закрытие заглушек I-4 | 12 |
| `render.yaml` (если нужно), `docs/deploy-dev.md`; вне репозитория — Render, Neon | выкатка на стенд | 13 |

`…` в задачах — `backend/internal/identity`. Внутри пакета исполнитель раскладывает файлы сам, если не трогает
файлы-владельцы чужих задач и сигнатуры стыков (R5 плана I-2).

# Задачи

Обозначения: **Риск** — A/B/C; **Тесты** — `Имя` → вход → ожидаемое; **Подсадка** — какой баг подсадить и какой тест
обязан упасть; **Приёмка** — что проверяет ревьюер; **Done when** — команды ворот контроллера. Сквозные условия задач
сценариев (3–7): тесты `app` — на реальном Postgres под ролью прода (`api` для public, `admin` для admin-api, `worker`
для чистки); для каждого кода ошибки операции — свой случай; событие, аудит и задача письма проверяются в той же
транзакции (откат — ничего нет); HTTP-тест на каждую операцию — успех по контракту и хотя бы один код; операция
уходит из `pending_*`.

## Этап A. Основа

### Task 1: миграция `0027`, представление способов входа, правила флагов в контракте

**Цель.** Дать admin-api то, чего ему не хватает по правам (§5.5): статистику способов входа без доступа к секретам
(хвост I-2 «`getAuthMethodsUsage`»); индексы очереди и истории заявок; значение `banned` в `status_before_deletion`
(R11, решение пользователя Q1); «один живой код» смены почты по `(purpose, user_id, email)` (R40, решение Q5) — индекс
и `startCode` одной задачей; правила флагов (`GlobalOnly`, `Requires`) — в ответе `listFeatureFlags`, чтобы админка
не держала копию правил (R3).

**Риск:** B (миграция и права ролей; контракт платформы; индекс живых кодов).

**Файлы.** Создать: `backend/migrations/0027_identity_captaincy.sql`, `backend/migrations/identity_0027_test.go`.
Изменить: `backend/migrations/README.md` (раздел identity: `0027`), `backend/internal/platform/grants/grants_test.go`
(случаи представления), `backend/internal/archtest/ownership.go` (представление — владелец `identity`),
`backend/internal/identity/queries/models.sql` (якорь новых колонок и представления, если sqlc их требует),
`backend/internal/identity/internal/store/identitydb/*` (`sqlc generate`), `contracts/openapi/admin/platform/schemas.yaml`
(`FeatureFlagRules`, поле `FeatureFlag.rules`), `contracts/openapi/admin/root.yaml` (`info.version` 0.3.0 → 0.4.0 —
только добавление), `backend/internal/httpapi/admin/flags.go` (выдача `rules` из `flags.RuleOf`),
`backend/internal/httpapi/admin/platform_flags_test.go`; `./task gen` → `contracts/openapi/admin.yaml`,
`backend/internal/httpapi/admin/oapi/*`, `apps/admin/src/api/gen/admin.d.ts`. Q5 (R40):
`backend/internal/identity/internal/app/codes.go` (+ `codes_test.go`), `backend/internal/identity/queries/codes.sql`
(`RevokeLiveCode`, `ON CONFLICT` вставки), `backend/migrations/identity_0025_test.go` (случай `auth_codes_live_key` —
только если имя индекса меняется). Фронт (I1 ревью плана — обязательное `rules` ломает typecheck админки):
`apps/admin/src/test/geo.ts` (фабрика `flag()` — правило по умолчанию `{global_only: false, requires: null}`),
`apps/admin/src/test/flags.ts` (MSW `flagsServer`: `PATCH` отдаёт флаг с `rules` прежнего состояния, `GET` — как
есть), `apps/admin/src/api/undo.test.tsx` (`flagServer` на `flag()` — сверить, что ответ несёт `rules`). Сверка
grep'ом 16.11: литерал `FeatureFlag` без спреда — только `flag()` в `test/geo.ts`; `test/flags.ts` и `undo.test.tsx`
собирают флаг спредом/фабрикой; в `apps/admin/src/shots/fixtures.ts` флагов нет — в пути не входит, если
`admin:typecheck` не покажет иного (тогда — в отчёт и в пути).

**Интерфейсы.**
- `0027` (одна транзакция; `down` — точная схема `0026`, тест вверх-вниз как `identity_0025_test.go`):
  - `CREATE VIEW identity_read_auth_method_usage (method text, only_method bigint, linked bigint)` — три строки
    `apple`, `google`, `passkeys`; учитываются аккаунты не `deleted`; `linked` — у скольких способ есть
    (`user_identities.provider`, хотя бы одна строка `passkeys`); `only_method` — у кого нет строки `credentials` и
    нет других способов, кроме этого. Только агрегаты — ни одного `user_id` (страж ниже). Права — ветка представлений
    `grants.sql` (`SELECT` всем ролям), правка `grants.sql` не нужна; представление исполняется правами владельца
    (мигратора), поэтому `admin` видит агрегат, не видя таблиц.
  - Индексы: `captain_applications (status, created_at, id)` — очередь всех городов и фильтр статуса;
    `captain_applications (user_id, created_at DESC)` — история игрока и пауза. Очередь модератора идёт по
    существующему `captain_applications_queue_idx (city_id, created_at) WHERE status = 'pending'`; новый индекс по
    городу — только если исполнитель покажет план запроса, который его требует (запись в README).
  - `users_status_before_deletion`: допустимые значения `active | suspended | banned` (R11; пересоздание CHECK с тем
    же именем). `down` — отказ (`RAISE EXCEPTION` с понятным текстом), если есть строки с `banned`.
  - Живые коды (R40, Q5): `auth_codes_live_key (purpose, email)` (`0025`) сужается до `purpose <> 'email_change'`;
    новый частичный уникальный индекс `(purpose, user_id, email) WHERE purpose = 'email_change' AND consumed_at IS
    NULL AND revoked_at IS NULL`. `down` — прежний индекс (отказ с понятным текстом, если есть два живых кода
    `email_change` на одну почту). `startCode` (`codes.go`) и `RevokeLiveCode` для `email_change` гасят только код
    того же `user_id`; `ON CONFLICT` вставки называет арбитр, совпадающий с предикатом нужного индекса (иначе
    Postgres не выведет арбитр — поэтому индекс и код в одной задаче). Advisory-замок `(purpose, email)` остаётся.
    Подтверждение смены по-прежнему выбирает одного (уникальность `users.email`).
- Контракт: `FeatureFlagRules {global_only: boolean, requires: FeatureFlagKey | null}` (оба `required`, описания и
  примеры по `contracts.md`); `FeatureFlag.rules` — `required` в ответе (сервер отдаёт всегда; добавление в ответ —
  не ломающее, oasdiff чист). Флаг без правила — `{global_only: false, requires: null}`. `requires` — по образцу
  `Approval.decided_by` (`platform/schemas.yaml`: `nullable: true` + `allOf: [$ref]`): oapi-codegen даёт
  `*FeatureFlagKey` без `omitempty` (сверено по `DecidedBy *ApprovalActor json:"decided_by"` в `oapi/api.gen.go`),
  openapi-typescript — `FeatureFlagKey | null`. Исполнитель сверяет сгенерированное после `./task gen` (в отчёт —
  строка типа из `api.gen.go` и `admin.d.ts`); `null` сериализуется как `null`, не пропуском поля.
- `flags.go`: `rules` — из `flags.RuleOf(key)`; других изменений ручек флагов нет.

**Тесты.**
- `TestIdentity0027UpDown` → вверх → вниз → схема равна `0026` (снимок как в `identity_0025_test.go`); вниз со
  строкой `status_before_deletion = banned` → понятный отказ.
- `TestAuthMethodUsageView` → фикстура: A — пароль + Apple; B — только Apple; C — только ключ доступа; D — `deleted` с
  Apple → `apple {only_method 1, linked 2}`, `google {0, 0}`, `passkeys {1, 1}`.
- `TestAuthMethodUsageNoUserIDs` → колонки представления ровно `method, only_method, linked` (`information_schema`).
- `TestGrantsAdminReadsUsageNotSecrets` (`grants_test`) → под `admin`: `SELECT` из представления — да; из
  `user_identities`, `passkeys`, `credentials` — `42501` (дополнить существующий случай).
- `TestStatusBeforeDeletionAllowsBanned` → `pending_deletion` с `status_before_deletion = banned` проходит, с
  `deleted` — `23514`.
- `TestFlagRulesInList` (`platform_flags_test.go`) → `GET /v1/flags` (путь сверен по `admin/root.yaml`):
  `auth_google.rules = {global_only: true, requires: auth_apple}`, `subscriptions.rules = {global_only: false,
  requires: null}` (в JSON — `"requires": null`, не пропуск); ответ валиден по схеме.
- `TestEmailChangeCodePerUser` (`app`, R40) → игрок A начал смену на свободный адрес X, игрок B — на тот же X → у A
  код жив (ввод A проходит), у B — свой; повторный `startEmailChange` A на X гасит только прежний код A; `signup` и
  `password_reset` — по-прежнему один живой код на `(purpose, email)` (второй гасит первый).
- `TestIdentity0027UpDown` дополнить: вниз при двух живых `email_change` на одну почту → понятный отказ.
- Стражи контракта (`contracts/test/*`) зелёные; `identity-freeze.test.mjs` зелёный (срез не тронут).
- Фронт: `./task admin:typecheck` и `./task admin:test` зелёные после `./task gen` (фабрики с `rules`).

**Подсадка.** В представлении не исключать `deleted` → `TestAuthMethodUsageView` красный. Отдавать пустые `rules`
всем → `TestFlagRulesInList` красный. В `RevokeLiveCode` не фильтровать `user_id` у `email_change` →
`TestEmailChangeCodePerUser` красный.

**Приёмка.** Ни одна роль не получила новых прав на таблицы; представление без `user_id`; `down` точный; версия
admin поднята; `admin.d.ts` содержит `rules`; тип `requires` — указатель/`| null`; чужой код смены почты не гасится.

**Done when.** `cd backend && go test -p 4 ./migrations/... ./internal/platform/grants/... ./internal/archtest/... ./internal/httpapi/admin/ -run 'Flag|Grants|0027|0025|AuthMethodUsage|Ownership'`;
`go test -p 4 ./internal/identity/internal/app/ -run 'Code|EmailChange'`;
`./task gen`, затем `git status` — сгенерированное в списке путей; `node --test "contracts/test/*.test.mjs"`;
`./task admin:typecheck`; `./task admin:test`.

**Ловушки.** Представление с `security_invoker` сломает чтение под `admin` — не ставить. `./task gen`,
`admin:typecheck`, `admin:test` — pnpm: на время задачи никто другой pnpm не запускает. Номер `0027` — сверить с
`main` в момент старта (R2). Путь ручки флагов — `/v1/flags` (`admin/root.yaml:225`). `codes.go` — сценарий I-2
(ни Task 2, ни Task 7 его не трогают — R40 целиком здесь).

**Не трогай.** `contracts/openapi/{public,admin}/identity/**`, `contracts/openapi/admin/staff/**`, `grants.sql`.

### Task 2: каркас I-4 — порты, сборка, домен, каталоги, раскладка заглушек

**Цель.** Все стыки задач 3–7 в одной задаче (как Task 8 плана I-2): порты условий и их сборка (К1, спека §10.3),
`AdminDeps` для сценариев admin-api, домен заявки, общее правило условий, константы событий, аудита и писем, ключи
локалей писем, раскладка заглушек admin по файлам областей, ослабление `TestCoreOpsNotStub` до «подмножества»
(R1, R5, R16, R17).

**Риск:** A (прод-сборка обязана подключать только «недоступно»; ошибка — обход ручного правила капитанства).

**Файлы.** Создать: `backend/internal/identity/captaincy.go`, `backend/internal/identity/identitytest/{checks.go,checks_test.go}`,
`backend/internal/identity/internal/domain/captaincy.go` (+ тест), `backend/internal/identity/internal/app/captain_rules.go`
(+ тест), `backend/internal/identity/queries/captain_rules.sql`, `backend/internal/identity/admin/{captains.go,players.go,headers.go}`
(+ `headers_test.go`), `backend/internal/httpapi/admin/{pending_captains_test.go,pending_players_test.go,pending_staff_link_test.go}`,
`backend/cmd/api/{captaincy_prod.go,captaincy_e2e.go,captaincy_test.go}`,
`backend/cmd/admin-api/{captaincy_prod.go,captaincy_e2e.go,captaincy_test.go}`,
`backend/internal/identity/testdata/events/<новые>.golden.json` (запрос `UserBlocked` — в `captain_rules.sql`, уже в
списке), `backend/internal/staff/linked.go` (+ тест) и
`backend/internal/staff/queries/linked.sql` (+ `staffdb` sqlc) — читатель связи для порта `StaffLinks` (R38),
`backend/cmd/admin-api/staff_links.go` (адаптер порта). Изменить: `backend/internal/identity/{identity.go,events.go}`
(+ `events_test.go`), `backend/internal/identity/internal/app/{app.go,audit.go}`, `backend/internal/identity/admin/handler.go`
(стабы переезжают в `captains.go`/`players.go`; `Deps`, `New`), `backend/internal/httpapi/admin/nostub_test.go`
(`pendingOps = slices.Concat(…)`), `backend/internal/httpapi/public/identity_core_test.go` (`TestCoreOpsNotStub`),
`backend/cmd/api/main.go`, `backend/cmd/admin-api/main.go`, `backend/locales/{ru,en}.json`, `backend/Taskfile.yml`
(задача `lint`: `go vet -tags wf_e2e ./cmd/api ./cmd/admin-api` — m8 ревью; `./task ci` и CI идут через неё).
Все существующие сборки `identity.Deps` / `identityadmin.Deps` / `identityadmin.New` в тестах (I7а ревью; сверено
`git grep "identity.Deps{\|identityadmin.New(\|identityadmin.Deps{"` 16.11 — повторить в начале задачи):
`backend/internal/httpapi/public/{handler_test,identity_core_test,pending_signup_test}.go`,
`backend/internal/identity/internal/app/{helpers_test,sessions_test}.go`,
`backend/internal/httpapi/admin/{identity_mfa_reset_test,permissions_test}.go` (окружения admin),
`backend/cmd/admin-api/identity_wiring_test.go`.

**Интерфейсы.**
- `identity/captaincy.go`:
  - `type CaptainCheck interface { Name() string; Check(ctx context.Context, q pgx.Tx, userID, cityID uuid.UUID) (reason string, ok bool, err error) }`
    — `ok = false` и `reason` из `CaptainIneligibility` (`profile_incomplete`, `captain_elsewhere_in_city`,
    `checks_unavailable`); ошибка — сбой (500).
  - `var RequiredCaptainChecks = []string{"players.profile_complete", "teams.city_captaincy"}`.
  - `func UnavailableCheck(name string) CaptainCheck` — всегда `checks_unavailable`, без запросов к базе.
  - `func ValidateCaptainChecks(cs []CaptainCheck) error` — имена ровно `RequiredCaptainChecks` (порядок не важен),
    без повторов и `nil`; иначе ошибка с перечнем расхождений.
- `identity.Deps.CaptainChecks []CaptainCheck`; новый `identity.AdminDeps{Pool, Clock, River, Geo geo.Service,
  CaptainChecks []CaptainCheck, StaffLinks StaffLinks, Log}`; `app.New` и новый `app.NewAdmin(d identity.AdminDeps) (*App, error)` зовут
  `ValidateCaptainChecks` (ошибка — отказ старта); `NewAdmin` отказывает и при `StaffLinks == nil`.
  `app.NewMaintenance` не меняется (воркеру порты не нужны).
- `app.NewTx(d TxDeps) (*App, error)` (I7в ревью) — конструктор для `identity/intx` (Task 7): `Pool`, `Clock`,
  `River`, `Geo`, `Log`, **без портов условий** (`intx` правило капитанства не считает). `app.go` остаётся у Task 2;
  Task 7 только зовёт `NewTx` из `intx.New`.
- `identity.StaffLinks interface { LinkedUser(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, error) }`
  (R38) — текущая связь сотрудника в транзакции вызывающего: строка `staff_accounts` сотрудника `FOR SHARE`, затем
  `staff_player_links`; `uuid.Nil` — связи нет. Замок сериализует решение по заявке с `PutPlayerLink`/`DeletePlayerLink`
  (они держат `FOR UPDATE` той же строки — Task 6). Реализация — `staff.LinkedPlayerTx(ctx, tx, staffID)` (`staff/linked.go`,
  запрос `staff/queries/linked.sql`); адаптер — `cmd/admin-api/staff_links.go`; в Go-тестах — литерал порта.
- `identityadmin.Deps` += `Geo geo.Service`, `CaptainChecks []identity.CaptainCheck`, `StaffLinks identity.StaffLinks`;
  `identityadmin.New` собирает `app.NewAdmin` (сброс 2FA Task 17 плана I-2 работает на том же `*app.App`, его
  сценарии не меняются — меняются только сборки окружений тестов, см. «Файлы»). Нулевой `identityadmin.Handler`
  (псевдоним `identityHandler` в `httpapi/admin`) по-прежнему отвечает `501` без паники (m4; страж — случай
  «нулевой обработчик» в `TestLookupHeadersEncoding` или отдельный тест конвейера).
- `identity/identitytest`: `AllowCheck(name string) identity.CaptainCheck`, `DenyCheck(name, reason string)
  identity.CaptainCheck`, `AllowAll() []identity.CaptainCheck` (оба имени). Пакет импортируют только тесты и файлы
  `//go:build wf_e2e`.
- `cmd/api` и `cmd/admin-api`: `func captainChecks() []identity.CaptainCheck` — в `captaincy_prod.go`
  (`//go:build !wf_e2e`, только `UnavailableCheck` по `RequiredCaptainChecks`) и `captaincy_e2e.go`
  (`//go:build wf_e2e`, `identitytest.AllowAll()`); `main.go` передаёт в `identity.Deps` / `identityadmin.Deps`
  (у admin-api ещё `Geo` — сервис geo на пуле `admin`).
- `domain/captaincy.go`: константы причин (`ReasonTooYoung … ReasonChecksUnavailable`), `ApplicationCooldown = 7 * 24
  * time.Hour`, `RejectionCodes` (§7.3), `func NormalizeContactLink(s string) (string, bool)` (схема `https` в любом
  регистре → `https://`, остальное как есть, ≤ 300 — хвост I-2, R26), `func CooldownUntil(decidedAt time.Time)
  time.Time`, `var ReasonOrder` (детерминированный порядок `reasons`).
- `app/captain_rules.go` — общее правило условий (R5):
  `type captaincyVerdict struct { Reasons []string; Checks map[string]string /* age|restrictions|profile|captaincy → ok|failed|unavailable */; AvailableAt *time.Time; OpenApplicationID uuid.UUID; Permitted bool }`;
  `func (a *App) evaluateCaptaincy(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID, now time.Time) (captaincyVerdict, error)`
  — возраст (`domain.AgeYears` по таймзоне города из `a.geo.CitySettings`, порог `MinSignupAge`; город не найден или
  страна выключена → `checks_unavailable` — код существующий, контракт заморожен; так же отвечает и `getMyCaptaincy`,
  запись для ответа нативщику — Task 10), ограничение `captain` (живые строки `user_restrictions`), статус
  `suspended` и «заблокирован» (`a.blocked`, R36) → `restricted` (`Checks[restrictions] = failed`; I6 ревью), порты
  по именам (`profile`, `captaincy`), живой допуск в городе → `already_permitted`,
  пауза — последний `rejected` игрока в любом городе (R5) моложе 7 дней → `AvailableAt`, открытая заявка в городе.
  Без записи, без замков (вызывающий держит, что нужно).
- Предикат «заблокирован» (R36, N1 повторного ревью) — **одна функция, заводит Task 2**:
  `func (a *App) blocked(ctx context.Context, q *identitydb.Queries, u userRow, now time.Time) (bool, error)` поверх
  запроса `UserBlocked :one` в `queries/captain_rules.sql`: `status = banned` ИЛИ (`pending_deletion` ∧
  `status_before_deletion = banned` ∧ существует живая строка `user_restrictions` с `capability = login`:
  `lifted_at IS NULL AND (until IS NULL OR @now < until)` — сверить имена колонок с `0025`). Правило целиком — в
  тексте этого запроса; `blocked` — тонкая обёртка (`deleted` → `false` без запроса). Её зовут `evaluateCaptaincy`
  (Task 2) и сценарии Task 7; Composer (`identity/mail`, без `*App`) зовёт тот же `identitydb.UserBlocked` — второй
  копии правила нет нигде.
- События (`events.go`, `EventTypes()` дополняется, golden на каждое): `EventCaptainApplicationSubmitted`,
  `…Withdrawn`, `…Decided` (`decision` — `approved|rejected`, `rejection_code` — строка или `null`),
  `EventCaptainPermitGranted`, `EventCaptainPermitRevoked`, `EventCityChanged` (`from_city_id`, `to_city_id`);
  `AggregateCaptainApplication = "captain_application"`; `type ApplicationRef struct{ID uuid.UUID; Version int64}`;
  конструкторы `CaptainApplicationSubmitted(ref ApplicationRef, userID, cityID uuid.UUID) events.Event` и т. д.
- Аудит (`app/audit.go`): `ActionCaptainApplicationSubmitted|Withdrawn|Approved|Rejected`, `ActionPiiRead =
  "identity.pii_read"`, `ActionPlayerLookup = "identity.player_lookup"` (R10), `ActionBanExpired =
  "identity.ban_expired"` (R12), `ActionCityChanged = "identity.city_changed"`, `ActionCaptainPermitRevoked`; объект
  `captain_application`.
- Виды писем — константы `MailCaptainApplicationApproved = "identity.captain_application_approved"`,
  `MailCaptainApplicationRejected` (в `MailKinds()` их добавляет Task 4 вместе с Composer — страж
  `TestComposersCoverKinds`), `MailCaptainApplicationWithdrawnCity = "identity.captain_application_withdrawn_city"`
  (R39, Q6; в `MailKinds()` и Composer — Task 7).
- `backend/locales/{ru,en}.json` (R17): `identity.mail.captain_application_approved.{subject,text}`,
  `identity.mail.captain_application_rejected.{subject,text}`, `identity.mail.captain_application_withdrawn_city.{subject,text}`
  («заявка отозвана из-за смены города — подайте заново в новом городе»; аргументы — ник, прежний город, новый город),
  `identity.captain_rejection.<код>` (6 кодов §7.3),
  `staff.mail.player_linked.{subject,text}`, `staff.mail.player_unlinked.{subject,text}`; аргументы одинаковы в ru и
  en (`TestLocalesHaveSameArguments`): ник, город, комментарий, имя владельца.
- `identity/admin/headers.go`: `func decodeAccessReason(raw string) (string, error)`, `func decodeLookupEmail(raw
  string) (string, error)` — `url.PathUnescape`; битая кодировка → `400 validation.failed` по полю
  `header.X-WF-Access-Reason` / `header.X-WF-Lookup-Email`; после декодирования причина 3–200 символов (руны), почта
  ≤ 254 байт и проходит `emailaddr.Normalize` — иначе `422 validation.failed` по полю заголовка (контракт §7.1,
  план I-1 R19).
- Раскладка admin (R16): `pendingOps = slices.Concat(pendingCaptains, pendingPlayers, pendingStaffLink)`; файлы —
  `pending_captains_test.go` (`listCaptainApplications`, `getCaptainApplication`, `getCaptainApplicationContact`,
  `approveCaptainApplication`, `rejectCaptainApplication`), `pending_players_test.go` (`findPlayerByEmail`,
  `getAuthMethodsUsage`), `pending_staff_link_test.go` (`putStaffPlayerLink`, `deleteStaffPlayerLink`).
  `TestPendingOpsExpire` admin — без изменений.
- `TestCoreOpsNotStub` (R1): public — `pendingOps ⊆ wantPendingPublic` (I-3 ∪ I-4) и `pendingI3` равен 14
  операциям точно; разбор литерала admin (`adminPendingOps`) — убрать (admin сторожит свой `TestNoStubOperations`).

**Тесты.**
- `TestValidateCaptainChecks` → таблица: ровно два имени — ok; пропуск, опечатка, повтор, `nil`, лишнее — ошибка с
  именем.
- `TestUnavailableCheckNoDB` → `Check` с `nil`-транзакцией → `checks_unavailable`, без паники.
- `TestCaptainChecksProdBuild` (`cmd/api`, `cmd/admin-api`, тесты без тега) → `captainChecks()` — ровно
  `RequiredCaptainChecks`, каждая отвечает `checks_unavailable`; `go list -deps` пакета без тега не содержит
  `identity/identitytest`.
- `TestCaptainChecksE2EBuild` (`//go:build wf_e2e`) → обе — `ok`.
- `TestAppNewRejectsBadChecks` → `app.New`/`app.NewAdmin` с одним портом → ошибка.
- `TestEvaluateCaptaincy` (`app`, реальная база, тестовые порты) → таблица: моложе порога (день рождения завтра в
  поясе города, но уже сегодня в UTC) → `too_young`; ограничение `captain` → `restricted`; `suspended` →
  `restricted`; `DenyCheck(profile, profile_incomplete)` → `profile_incomplete`, `Checks[profile] = failed`; допуск в
  городе → `already_permitted`; отказ 3 дня назад в другом городе → `AvailableAt = решение + 7 д`; отказ 8 дней назад
  → паузы нет; `withdrawn` паузы не ставит; несколько причин — в `ReasonOrder`; `UnavailableCheck` →
  `checks_unavailable` и `Checks[profile] = unavailable`; `banned` → `restricted`, `Checks[restrictions] = failed`;
  `pending_deletion` + `status_before_deletion = banned` + живая строка `login` → `restricted`; то же без живой
  строки `login` (бан истёк, чистка ещё не прошла) → не `restricted` (I6); строка `login` с `until = now` — уже не
  живая → не `restricted` (граница предиката); страна города выключена →
  `checks_unavailable`.
- `TestStaffLinkedPlayerTx` (`staff`) → связь есть → id игрока; нет → `uuid.Nil`; вызов держит `FOR SHARE` строки
  сотрудника (параллельный `UPDATE … FOR UPDATE` ждёт до коммита).
- `TestNormalizeContactLink` → `HTTPS://t.me/x` → `https://t.me/x`; `http://` → отказ; 301 символ → отказ.
- `TestDecodeAccessReason`, `TestDecodeLookupEmail` → закодированная кириллица → строка; `%D0%` → 400; 2 символа →
  422; 201 → 422; `ivan.petrov%40mail.example` → канон; адрес, не прошедший `emailaddr`, → 422.
- `TestEventPayloadsNoPII`, golden-файлы новых событий — зелёные.
- `TestCoreOpsNotStub` — зелёный на текущем состоянии (18 ⊆, 14 I-3 точно); `TestNoStubOperations` admin зелёный.

**Подсадка.** В `captaincy_prod.go` вернуть `identitytest.AllowAll()` (без тега) → `TestCaptainChecksProdBuild`
красный. В `ValidateCaptainChecks` не проверять повторы → `TestValidateCaptainChecks` красный. Пауза по городу заявки
вместо пользователя → случай «отказ в другом городе» `TestEvaluateCaptaincy` красный. В `UserBlocked` заменить
`@now < until` на `@now <= until` → случай «строка `login` истекает ровно в `now`» `TestEvaluateCaptaincy` красный (та
же подсадка после Task 7 красит `TestBanOverPendingDeletion` — страж общей функции).

**Приёмка.** Прод-бинарники не тянут `identitytest`; `admin-api` и `api` не стартуют с неверным набором портов; в
`identity/admin/handler.go` нет стабов операций (они в файлах областей, по-прежнему 501); `pendingOps` admin = 9,
public = 18 — ни одна операция этой задачей не реализована.

**Done when.** `cd backend && go test -p 4 ./internal/identity/... ./internal/staff/... ./internal/httpapi/... ./internal/archtest/... ./cmd/api ./cmd/admin-api`;
`go test -tags wf_e2e ./cmd/api ./cmd/admin-api -run CaptainChecks`; `go vet ./...`;
`go vet -tags wf_e2e ./cmd/api ./cmd/admin-api`; `gofmt -l` по путям.

**Ловушки.** archtest (`modules_test.go` смотрит `TestImports`/`XTestImports`) запрещает тестам `internal/httpapi/*`
импортировать `identity/identitytest` (I7б ревью): в **каждом** тестовом пакете `httpapi` (`public`, `admin`) —
свой литерал порта (`CaptainCheck` и `StaffLinks` — интерфейсы корня), `identitytest` — только тестам
`identity/**` и файлам `//go:build wf_e2e` в `cmd/*`. `geo.Service` у admin-api — на пуле роли `admin` (`SELECT` на geo есть).
Корень не импортирует `app` (N2 плана I-2). Возраст — только `domain.AgeYears` (не `age_years()` базы: она по
`current_date` сервера). Тег — ровно `wf_e2e` (спека §10.3). `cmd/*/main.go` после слияния embedded-worker — сверить
текущую сборку до правки.

**Не трогай.** Сценарии I-2 (`app/{login*,signup,password,deletion,anonymize,cleanup,mfa*,codes}.go`), контракт,
`identity/httpapi/pending_i4.go` (Task 3), `staff/**` (Task 6) — кроме `staff/{linked.go,queries/linked.sql}` и их
sqlc.

## Этап B. Сценарии бэкенда

### Task 3: капитанство игрока — четыре операции public

**Цель.** Спека §3.12 п. 1–5, 7: экран «Стать капитаном», история заявок, подача и отзыв. Операции:
`getMyCaptaincy`, `listMyCaptainApplications`, `submitCaptainApplication`, `withdrawCaptainApplication`.

**Риск:** A (гонки подачи и удаления, правило не обходится, IDOR на отзыве).

**Файлы.** Создать: `backend/internal/identity/internal/app/captaincy.go` (+ тест), `backend/internal/identity/queries/captaincy.sql`,
`backend/internal/identity/httpapi/captaincy.go`, `backend/internal/httpapi/public/captaincy_test.go`. Удалить:
`backend/internal/identity/httpapi/pending_i4.go`, `backend/internal/httpapi/public/pending_i4_test.go`. Изменить:
`backend/internal/httpapi/public/nostub_test.go` — только строка `pendingOps = slices.Concat(…)` (без `pendingI4`),
`backend/internal/identity/internal/store/identitydb/captaincy.sql.go` (`sqlc generate`).

**Интерфейсы.**
- `func (a *App) MyCaptaincy(ctx context.Context, userID uuid.UUID) (Captaincy, error)` — читающая транзакция:
  допуски (`identity_read_captain_permits`, все города, `probationary`), `eligibility` по текущему городу игрока
  (`evaluateCaptaincy`: `eligible = len(Reasons) == 0 && AvailableAt == nil && нет открытой заявки`), открытая заявка.
- `func (a *App) ListMyCaptainApplications(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]CaptainApplication, string, error)`
  — новые сверху, keyset `(created_at, id)` DESC (`platform/page`), испорченный курсор — `400 validation.failed` по
  `query.cursor`.
- `func (a *App) SubmitCaptainApplication(ctx context.Context, userID uuid.UUID, in CaptainApplicationInput) (CaptainApplication, error)`
  — одна `db.InTx`: `lockUser`; город — `users.city_id`; `evaluateCaptaincy`; причины → `409
  identity.captain_ineligible` с `reasons` (`httpx.Problem.WithReasons`); открытая заявка → `409
  identity.application_exists`; пауза → `409 identity.application_cooldown` с `available_at`; порядок проверок —
  причины, затем открытая, затем пауза (R5; одна ошибка на ответ — первая по порядку); иначе вставка (`id.New`,
  `status = pending`, `is_probationary = true`, `contact_link` через `domain.NormalizeContactLink`, `phone` как есть
  после проверки контракта, `version = 1`), событие `captain_application_submitted` (версия 1), аудит от игрока со
  `scope_city_id`. Писем нет.
- `func (a *App) WithdrawCaptainApplication(ctx context.Context, userID, applicationID uuid.UUID) (CaptainApplication, error)`
  — `lockUser` → строка заявки `FOR UPDATE` (R7); чужая или нет → `404 identity.application_not_found`; не
  `pending` → `409 identity.application_closed`; иначе `withdrawn`, `closed_at`, `version + 1`, событие `…_withdrawn`,
  аудит. Паузы не ставит.
- `identity/httpapi/captaincy.go` — четыре метода `Handler` (вместо стабов): маппинг в `oapi.Captaincy`,
  `oapi.CaptainApplication` (`has_phone`, телефон не возвращается; `decided_at` = `coalesce(reviewed_at, closed_at)`);
  `Cache-Control: private, no-store` (`CachePrivate` контракта, как `accountCachePrivate` в `identity/httpapi/account.go`; m3 ревью).

**Тесты.**
- `TestSubmitCaptainApplicationHappy` (тестовые порты `AllowAll`) → `201`, строка `pending`, событие и аудит в tx,
  `has_phone` верен, `contact_link` в нижнем регистре схемы.
- `TestSubmitCaptainApplicationCodes` → таблица: `UnavailableCheck` → `409 captain_ineligible [checks_unavailable]`;
  моложе порога → `[too_young]`; ограничение `captain` → `[restricted]`; допуск → `[already_permitted]`; открытая →
  `application_exists`; отказ 2 дня назад → `application_cooldown`, `available_at` = решение + 7 д.
- `TestSubmitConcurrentSameUser` → две подачи параллельно (барьер) → одна `201`, вторая `409 application_exists`;
  ни одной `500` (уникальный индекс `captain_applications_open_key` — запасной: `23505` по имени → тот же код).
- `TestSubmitVsDeleteAccount` → подача ждёт `lockUser`, удаление прошло → подача: аккаунт уже не живой → ответ
  `401` (`errSessionGone`, как у мёртвой сессии), ни одной `500`; или подача первой — заявка затем `withdrawn`
  удалением (событие — Task 7).
- `TestWithdrawCaptainApplication` → `200 withdrawn`; повтор → `409 application_closed`; чужая → `404` (тот же ответ,
  что несуществующая — `TestCoreIDOR` дополнить); после отзыва новая подача — без паузы.
- `TestWithdrawVsApprove` (вместе с Task 4 — тест в Task 4).
- `TestMyCaptaincy` → нет допусков, есть открытая заявка → `eligible false`, `reasons []`, `open_application`;
  отказ 3 дня назад → `available_at`; `UnavailableCheck` → `reasons [checks_unavailable]`.
- `TestListMyCaptainApplications` → три заявки → новые сверху, курсор по 2, испорченный курсор → `400`.
- HTTP (`captaincy_test.go`): каждая операция — успех по контракту и код; `submit` с `Idempotency-Key` — повтор
  отдаёт тот же ответ; `Cache-Control` по контракту.
- `TestNoStubOperations` public: четыре операции отвечают не 501; `TestPendingOpsExpire` зелёный.

**Подсадка.** Не брать `lockUser` в подаче → `TestSubmitConcurrentSameUser` красный (500 на `23505` без маппинга или
две строки при снятом индексе — подсадка снимает и маппинг). Отзыв без проверки владельца → `TestWithdrawCaptainApplication`
(«чужая») красный.

**Приёмка.** Подача невозможна при прод-портах; ответ никогда не раскрывает чужую заявку; `pendingOps` public = 14
(только I-3).

**Done when.** `cd backend && go test -p 4 ./internal/identity/... ./internal/httpapi/public/...`; `gofmt -l`.

**Ловушки.** `users.city_id` у живого аккаунта не `NULL`, но защищаться: `NULL` → `checks_unavailable`. Возраст «на
дату заявки» — `a.clock.Now()` в поясе города. Порт вызывается в транзакции под ролью `api` — модули выше должны
давать `api` чтение своих представлений (передача `players`/`teams`). `lockUser` под удалением —
`errSessionGone` (как в I-2).

**Не трогай.** `captain_rules.go` (Task 2; нужна правка правила — через контроллера), `identity/admin/**`,
`identity_core_test.go` (Task 12).

### Task 4: заявки в админке — очередь, карточка, контакты, одобрение, отказ, письма

**Цель.** Спека §7.1 (пять операций заявок), §7.6 (`self_action`), §8.2 (письма решения). Операции:
`listCaptainApplications`, `getCaptainApplication`, `getCaptainApplicationContact`, `approveCaptainApplication`,
`rejectCaptainApplication`.

**Риск:** A (права в момент действия, скоуп модератора, ПД, роль `admin` без `UPDATE users`).

**Файлы.** Создать: `backend/internal/identity/internal/app/captain_admin.go` (+ тест),
`backend/internal/identity/queries/captain_admin.sql`, `backend/internal/httpapi/admin/identity_captains_test.go`.
Изменить: `backend/internal/identity/admin/captains.go` (стабы Task 2 → ручки),
`backend/internal/httpapi/admin/pending_captains_test.go` (пусто), `backend/internal/identity/mail/composer.go`
(+ `composer_test.go`), `backend/internal/identity/internal/app/mail.go` (`MailKinds()` += 2),
`backend/internal/identity/queries/mail.sql` (чтение заявки для письма), sqlc.

**Интерфейсы.**
- Скоуп (R9, R37): `perm.Decide`/`st.Decide(key, cityID)`; ветвление — по `auth.GrantScoped`, **не** по
  `len(st.CityIDs)` (образец — `geo/admin/read.go:19,53`; модератору без городов `CityIDs` — пустой не-nil срез,
  и проверка длины дала бы ему все города). Для списка у `GrantScoped` — фильтр `city_id ∈ st.CityIDs` (без городов —
  пустая страница; фильтр чужого города — пустая страница, не 403); карточка, контакты, решение чужого города →
  `404 identity.application_not_found`. Заявки аккаунтов `deleted` не видны нигде (R9).
- `func (a *App) ListCaptainApplications(ctx context.Context, st *auth.Staff, f CaptainQueueFilter) ([]AdminApplication, string, error)`
  — `CaptainQueueFilter{Status string /* по умолчанию pending */; CityID uuid.UUID; Cursor string; Limit int}`;
  порядок (R9): `pending` — `(created_at, id)` ASC; прочие статусы — `(decided_at, id)` DESC, где `decided_at =
  coalesce(reviewed_at, closed_at)`. Строка: ник (`identity_read_users`), город, `age_years` (по таймзоне города,
  `geo_read_city_settings`), `account_age_days` (полные сутки от `users.created_at`), поля формы, `has_phone`,
  `decided_by_label`, код, комментарий.
- `func (a *App) CaptainApplicationCard(ctx context.Context, st *auth.Staff, id uuid.UUID) (AdminApplicationCard, error)`
  — читающая tx под `admin`: заявка, заявитель, `checks` из `evaluateCaptaincy` (на город заявки; `age`,
  `restrictions`, `profile`, `captaincy` → `ok|failed|unavailable`), история игрока (новые сверху, без текущей, **без
  предела** — m11 ревью: контракт `history` предела не задаёт, а пауза 7 дней ограничивает число заявок игрока),
  `linked_accounts` — число различных `linked_user_id` в `identity_read_linked_accounts`.
- `func (a *App) CaptainApplicantContact(ctx context.Context, st *auth.Staff, id uuid.UUID, reason string) (email string, phone *string, err error)`
  — в одной tx чтение и `audit.WriteRead` (`ActionPiiRead`, объект — `user` заявителя, `scope_city_id` — город
  заявки, причина — в `Entry.Reason`; почты и телефона в записи нет).
- `func (a *App) ApproveCaptainApplication(ctx context.Context, st *auth.Staff, id uuid.UUID, comment *string) (AdminApplication, error)`
  — одна `db.InTx` под `admin` (R6): заявка `FOR UPDATE` (нет / чужой город / `deleted` → 404); своя учётка
  → `409 identity.self_action` — связь сотрудника **перечитывается в этой транзакции** портом
  `a.staffLinks.LinkedUser(ctx, tx, st.StaffID)` (R38: `FOR SHARE` строки сотрудника; `st.LinkedUserID` из загрузки
  сессии не используется — связь могла появиться между загрузкой и решением); порядок замков —
  `captain_applications` → `staff_accounts` (`FOR SHARE`) → `role_assignments`; не `pending` → `409
  application_closed`; заявитель в `pending_deletion` → `409 application_closed` (заявку закроет удаление — R6);
  `evaluateCaptaincy` по городу заявки: любая причина → `409
  captain_ineligible {reasons}` (в том числе `checks_unavailable`); иначе `role_assignments (role = captain,
  scope_type = city, scope_id = city, application_id, granted_by_staff_id, granted_at)`, заявка `approved`,
  `reviewed_at`, `reviewed_by_staff_id`, `reviewed_by_label = st.Label`, `comment`, `version + 1`; события
  `captain_application_decided` и `captain_permit_granted` (одна версия агрегата); аудит
  `identity.captain_application_approved` (`scope_city_id`); письмо `mail.SendArgs{kind:
  identity.captain_application_approved, ref: application.id}` клиентом вставки admin-api.
- `func (a *App) RejectCaptainApplication(ctx context.Context, st *auth.Staff, id uuid.UUID, code string, comment *string) (AdminApplication, error)`
  — та же последовательность до `application_closed` (включая перечитывание связи портом, R38); `other` без комментария (пробелы — не комментарий) → `422
  validation.failed` по `body.comment` (`required`); `rejected`, `rejection_code`, остальное как у одобрения;
  событие `…_decided`; аудит `…_rejected` (код в `after`); письмо `…_rejected`. Условия не проверяются.
- Composer (`identity/mail`): `captain_application_approved|rejected` по `ref` — адрес и язык игрока
  (`users.email`, `users.locale`), город — название на языке игрока (`geo_read_city_names` или существующий способ
  в composer), комментарий, текст причины `identity.captain_rejection.<код>`; заявка не в нужном статусе или аккаунт
  `deleted` → `mail.ErrSkip`; заявитель в `pending_deletion` — письмо идёт (аккаунт ещё есть).

**Тесты.**
- `TestCaptainQueueScope` → модератор города A: в списке только A; `city_id = B` → пусто; карточка, контакты,
  одобрение, отказ заявки города B → `404`; владелец видит оба; **модератор без городов** (`CityIDs` — пустой
  не-nil срез, I4 ревью) → список пуст, карточка → `404`, одобрение → `404`.
- `TestCaptainQueueOrderAndCursor` → `pending` старые сверху, `approved` новые решения сверху, курсор по 2;
  `deleted`-заявитель скрыт.
- `TestCaptainCard` → `checks` с `UnavailableCheck` → `profile`, `captaincy` — `unavailable`; история без текущей;
  `linked_accounts` — 1 при общем устройстве (фикстура `device_accounts`).
- `TestCaptainContactAudited` → `200 {email, phone}`; в `audit_log` запись `identity.pii_read` с причиной, без
  почты и телефона в `before/after` (поля `meta` у записи нет); откат tx (подсадка ошибки после чтения) — записи нет.
- `TestApproveCaptainApplication` (тестовые порты) → `200 approved`; допуск в `identity_read_captain_permits` с
  `is_probationary`; события, аудит, задача письма в одной tx; `users` и `sessions` не тронуты (транзакция под ролью
  `admin` — любая запись туда дала бы `42501`).
- `TestApproveCodes` → таблица: прод-порты → `409 captain_ineligible [checks_unavailable]`; `too_young`;
  `self_action` (связь фикстурой); `application_closed` после отзыва; заявитель в `pending_deletion` → `409
  application_closed`; заявитель `banned` (живая строка `login`) → `409 captain_ineligible [restricted]` (I6).
- `TestApproveSelfLinkRace` (непредусмотренный отказ 1 ревью) → (а) сессия сотрудника загружена без связи
  (`st.LinkedUserID = Nil`), затем связь с заявителем закоммичена (фикстура `staff_player_links`) → одобрение и
  отказ → `409 self_action`; (б) `TestPutPlayerLinkVsApprove` (N2 повторного ревью): одобрение и **настоящий**
  `PutPlayerLink` Task 6 параллельно (барьер; Task 6 к этому времени закоммичен — зависимость 4 ← 6) → ровно один
  исход: либо `self_action`, либо
  `approved` и связь ждёт коммита одобрения; без `40P01` и `500`. Тест — в `internal/httpapi/admin`
  (`identity_captains_test.go`; там собраны и identity, и staff): тесты `identity/**` не импортируют `staff`
  (границы модулей archtest — сверить первым шагом).
- `TestRejectCaptainApplication` → `200 rejected`; `other` без комментария → `422 body.comment`; затем подача игроком
  → `application_cooldown`.
- `TestApproveVsWithdraw` → параллельно → ровно один исход (`approved` и `409` у отзыва, или `withdrawn` и `409
  application_closed` у одобрения); `TestApproveVsApprove` → одна `200`, вторая `409`; `TestApproveVsDeleteAccount`
  → без `40P01`: либо `approved` (допуск остаётся до анонимизации, §3.11), либо `withdrawn` и `409`.
- `TestComposeCaptainDecision` → ru и en, отказ `other` с комментарием, причина текстом локали; `deleted` → `ErrSkip`.
- HTTP (`identity_captains_test.go`): пять операций — успех по контракту и код; `TestPermissionsAllOperations` —
  зелёный (поддержка: чтение да, решение `403`); `X-WF-Access-Reason` битый → `400`, короткий → `422`.

**Подсадка.** Не проверять скоуп в одобрении → `TestCaptainQueueScope` красный. Не перепроверять условия при
одобрении → случай «прод-порты» `TestApproveCodes` красный. Писать `users.version` при одобрении →
`TestApproveCaptainApplication` (`42501`) красный. Скоуп по `len(st.CityIDs) > 0` вместо `GrantScoped` → случай
«модератор без городов» `TestCaptainQueueScope` красный. Брать `st.LinkedUserID` вместо порта →
`TestApproveSelfLinkRace` (а) красный.

**Приёмка.** Одобрение невозможно при прод-портах; admin-api пишет только `captain_applications`,
`role_assignments`, `audit_log`, `outbox`, `river_job`; ПД не в аудите; `pending_captains` пуст.

**Done when.** `cd backend && go test -p 4 ./internal/identity/... ./internal/httpapi/admin/...`; `gofmt -l`.

**Ловушки.** `FOR UPDATE` под `admin` только на `captain_applications` (у `admin` есть `UPDATE`); `users` и
`user_mfa` — без `FOR …` (`42501`, ревью W2 плана I-2). FK `role_assignments.user_id` берёт `FOR KEY SHARE` на
`users` — совместим с `FOR NO KEY UPDATE` у `lockUser` (R6): взаимоблокировки с удалением нет — тест обязателен.
Пауза после отказа считается от `reviewed_at`. Подпись сотрудника — `st.Label` (как в сбросе 2FA). Письма — не
`staff.NewMailArgs`. `composer.go` и `app/mail.go` после коммита этой задачи правит Task 7 (R36 — предикат
«заблокирован» в composer, письмо смены города R39): Task 7 стартует только после коммита Task 4 («Порядок»).
Причина доступа к контактам в `audit_log.reason` неизменяема и переживает анонимизацию (R41) — в UI подсказка
«не указывайте персональные данные игрока» (Task 8).

**Не трогай.** `captain_rules.go`, `identity/admin/players.go` (Task 5), `identity/httpapi/**` (Task 3),
`staff/**` (порт `StaffLinks` и `staff/linked.go` — Task 2).

### Task 5: поиск игрока по почте и статистика способов входа

**Цель.** Спека §3.15 п. 2, §7.1: `findPlayerByEmail` (поддержка и сброс 2FA, связь сотрудника) и
`getAuthMethodsUsage` (экран флагов, §4.3).

**Риск:** A (ПД, оракул существования почты для модератора, аудит поиска).

**Файлы.** Создать: `backend/internal/identity/internal/app/lookup.go` (+ тест), `backend/internal/identity/queries/lookup.sql`.
Изменить: `backend/internal/identity/admin/players.go` (стабы Task 2 → ручки),
`backend/internal/httpapi/admin/pending_players_test.go` (пусто), `backend/internal/httpapi/admin/identity_lookup_test.go`
(живые HTTP-случаи поиска — новым тестом рядом; `TestLookupHeadersEncoding` остаётся тестом конвейера, m4), sqlc.

**Интерфейсы.**
- `func (a *App) FindPlayerByEmail(ctx context.Context, st *auth.Staff, email, reason string) (PlayerLookup, error)`
  — почта уже канонизирована `decodeLookupEmail`; одна tx под `admin`: аккаунт по `users.email` (не `deleted`;
  `pending_deletion` находится — статус в ответе); у `auth.GrantScoped` (не по `len(st.CityIDs)` — R37, образец
  `geo/admin/read.go:19,53`) игрок вне городов модератора → как «нет» (R10), модератор без городов не находит
  никого; нет → `404
  identity.user_not_found`. `mfa_enabled` — подтверждённая строка `user_mfa` (колоночный `SELECT (user_id,
  confirmed_at)`), `mfa_reset` — живой `mfa_resets`. Аудит — **каждый** поиск (R10): найден — `audit.WriteRead`
  `ActionPiiRead`, объект `user`, `scope_city_id` — город игрока; не найден — `ActionPlayerLookup` по образцу
  `auditAnonymous` (`app/audit.go:53–56`): `ObjectType: user`, `ObjectID: uuid.Nil`, `After: {found: false}` (m2 ревью:
  поля `Meta` в `audit.Entry` нет — исход пишется в `After`); причина — `Entry.Reason`;
  почты нет нигде в записи.
- `PlayerLookup{ID, Nickname, CityID uuid.UUID?, Status, CreatedAt, MfaEnabled, MfaReset *{ID, ExecuteAt}}` —
  маппинг в `oapi.IdentityPlayerLookup`.
- `func (a *App) AuthMethodsUsage(ctx context.Context) ([]AuthMethodUsage, error)` — чтение
  `identity_read_auth_method_usage` (Task 1); всегда три строки в порядке `apple, google, passkeys`.

**Тесты.**
- `TestFindPlayerByEmail` → найден: поля, `mfa_enabled` только при подтверждённой 2FA (неподтверждённая настройка —
  `false`), живой сброс — `mfa_reset`; `pending_deletion` — найден со статусом; `deleted` — `404`.
- `TestFindPlayerScope` → модератор города A ищет игрока города B → `404` (ответ и время запроса — того же порядка,
  без отдельной ветки «есть, но чужой» в ответе); модератор без городов → `404`; владелец и поддержка — найден.
- `TestFindPlayerAudit` → найден — запись `identity.pii_read` с причиной; не найден — `identity.player_lookup`,
  `object_type = user`, `object_id` пуст, `after = {found: false}`; в `before/after` и `reason` нет почты (поиск подстроки адреса по всей строке записи).
- `TestAuthMethodsUsage` → три строки; до I-3 (нет привязок и ключей) — нули.
- HTTP (m4 ревью): `TestLookupHeadersEncoding` **остаётся тестом конвейера** — окружение с нулевым
  `identityadmin.Handler`, закодированные случаи по-прежнему `501`, сырые — `400` (валидатор контракта); живые
  случаи — новым тестом `TestFindPlayerByEmailHTTP` (`identity_lookup_test.go`: закодированные почта и причина →
  `200`/`404`; `X-WF-Lookup-Email` с адресом, не прошедшим нормализацию, → `422` по полю заголовка; `%D0%` → `400`)
  и в `identity_captains_test.go` (Task 4: причина контактов); `getAuthMethodsUsage` под `platform.flags.read` —
  все роли каталога `200` (`TestPermissionsAllOperations`).

**Подсадка.** Не писать аудит при «не найден» → `TestFindPlayerAudit` красный. Не фильтровать скоуп → `TestFindPlayerScope`
красный.

**Приёмка.** Ни одного пути, где почта уходит в журнал, лог или событие; модератор не отличает «нет» от «чужой»;
`pending_players` пуст; нулевой `identityadmin.Handler` → `501` без паники (m4).

**Done when.** `cd backend && go test -p 4 ./internal/identity/... ./internal/httpapi/admin/...`; `gofmt -l`.

**Ловушки.** Почта в ключе запроса — каноническая форма `emailaddr` (как в `users.email`, CHECK
`users_email_format`); `citext` — сравнение без учёта регистра уже есть. Логгер запроса не пишет заголовки
(`X-WF-Lookup-Email`) — проверить журнал запросов admin-api (`httpx`); пишет — убрать заголовок из журнала здесь же
и отметить в отчёте.

**Не трогай.** `identity/admin/captains.go` (Task 4), `staff/**` (Task 6).

### Task 6: связь «сотрудник → учётка игрока»

**Цель.** Спека §7.6, план I-1 R15, план I-2 R8 и хвост «связь инициатора сброса 2FA»: владелец связывает
сотрудника с его учёткой игрока и снимает связь; карточка сотрудника показывает связь владельцу. Операции:
`putStaffPlayerLink`, `deleteStaffPlayerLink`; поле `StaffAccount.player_link`.

**Риск:** A (право только владельца, гонка двух связей на одну учётку, отсечение запросов инициатора).

**Файлы.** Создать: `backend/internal/staff/internal/app/player_link.go` (+ тест), `backend/internal/staff/queries/player_links.sql`,
`backend/cmd/admin-api/players_port.go` (+ тест), `backend/internal/httpapi/admin/staff_player_link_test.go`.
Изменить: `backend/internal/staff/staff.go` (порт `PlayerDirectory`, `Deps.Players`), `backend/internal/staff/dto.go`
(`PlayerLink`), `backend/internal/staff/mail.go` (`MailPlayerLinked`, `MailPlayerUnlinked`),
`backend/internal/staff/internal/app/{mail.go,accounts.go}` (письма; `player_link` в карточке),
`backend/internal/staff/admin/accounts.go` (две ручки, маппинг `player_link`), `backend/internal/staff/internal/store/staffdb/*`
(sqlc), `backend/cmd/admin-api/main.go` (`staff.Deps.Players`), `backend/internal/httpapi/admin/pending_staff_link_test.go`
(пусто), `.claude/rules/backend-staff.md` (ручки связи, порт, подъём версии).

**Интерфейсы.**
- `staff.PlayerDirectory interface { Player(ctx context.Context, userID uuid.UUID) (nickname string, ok bool, err error) }`
  — `ok = false` у несуществующего и `deleted`; `pending_deletion` — `ok` (R15). Адаптер в `cmd/admin-api` — над
  `identity.Service.User` (статус `deleted` → `ok = false`). `staff` (слой 0) identity не импортирует.
- `func (a *App) PutPlayerLink(ctx context.Context, by *auth.Staff, staffID, userID uuid.UUID, m staff.Meta) (staff.PlayerLink, error)`
  — одна tx (I5 ревью, правило `.claude/rules/backend-staff.md` «Порядок замков (m7, M-4)», шаблон `ChangeRole` в
  `staff/internal/app/accounts.go`): `lockAccounts(ctx, q, staffID, by.StaffID)` — учётки по возрастанию id, цель
  `FOR UPDATE`, автор `FOR SHARE` (`lockOwners` не нужен — страж последнего владельца не затрагивается); затем
  `checkActor` под замком (учётка автора не active, сессия погашена/заменена → 401, роль сменилась и право
  `staff.player_links.manage` пропало → 403); цели нет → `404 staff.not_found`; игрок через порт (нет → `422
  staff.player_not_found`); та же связь → `200` без изменений (без версии, аудита, письма); учётка у другого
  сотрудника → `409 staff.player_link_taken` (и `23505` по имени `staff_player_links_user_id_key` при гонке — тот же
  код); иначе вставка или замена (`created_by = by.ID`, `created_at`), `staff_accounts.version + 1` (R14), аудит
  `staff.player_linked` (объект — сотрудник, `after {user_id}`), письмо сотруднику `staff.player_linked`.
- `func (a *App) DeletePlayerLink(ctx context.Context, by *auth.Staff, staffID uuid.UUID, m staff.Meta) error` —
  те же `lockAccounts` и `checkActor` под замком; нет сотрудника → `404`; связи нет → `204` без записи и письма;
  иначе удаление, версия + 1, аудит `staff.player_unlinked`, письмо `staff.player_unlinked`.
- Ответ после коммита (I5): сборка `PlayerLink` для ответа (ник через порт, подпись) — на
  `context.WithTimeout(context.WithoutCancel(ctx), rereadTimeout)`, как `committedAccount`
  (`staff/admin/accounts.go:225–238`): обрыв клиента после коммита не даёт `500` при выполненном действии и
  ответ сохраняется под `Idempotency-Key`.
- `staff.PlayerLink{UserID uuid.UUID; Nickname string; LinkedAt time.Time; LinkedByLabel string}` — `linked_by_label`
  — текущая подпись сотрудника `created_by` тем же правилом, что подпись в журнале и подтверждениях (имя, без имени —
  почта); учётки сотрудников не удаляются (выключение), поэтому строка есть всегда; ник — через порт.
- Карточка `getStaffAccount`: `player_link` — только если у запрашивающего `staff.player_links.manage` = `allow`;
  иначе поля нет; связи нет — поля нет (контракт R15 I-1). Список сотрудников поле не несёт.
- Ник в карточке — через порт на каждое чтение (ник меняется в identity).

**Тесты.**
- `TestPutPlayerLink` → `200 {user_id, nickname, linked_at, linked_by_label}`; версия цели + 1; аудит и задача
  письма в tx; повтор той же связи → `200`, версия не изменилась, писем 1.
- `TestPutPlayerLinkCodes` → нет сотрудника → `404`; удалённый игрок → `422 player_not_found`; учётка у другого →
  `409 player_link_taken`; не владелец (админ) → `403` конвейера (`TestPermissionsAllOperations`).
- `TestPutPlayerLinkRace` → два PUT на одну учётку игрока для двух сотрудников параллельно → `200` и `409`, без
  `500`.
- `TestPutPlayerLinkActorRechecked` (по образцу `TestManageActorRechecked`, `manage_accounts_test.go:242`) → владелец
  загружен, затем до PUT/DELETE его роль понижена (или учётка выключена, сессия заменена) → `403` / `401`, связи нет,
  версия цели не изменилась; PUT и DELETE.
- `TestDeletePlayerLink` → `204`, версия + 1, письмо; повтор → `204` без записи и письма.
- `TestStaffCardPlayerLinkOwnerOnly` → владелец видит `player_link` с ником; админ — поля нет; связи нет — поля нет.
- `TestMfaResetInitiatorLinkedBetweenRequestAndApprove` (`internal/httpapi/admin`, сценарий хвоста I-2): сотрудник A
  запросил сброс 2FA игрока P; владелец связал A с P; сотрудник B подтверждает → `409 platform.approval_stale`,
  запрос `failed (initiator_changed)`, `mfa_resets` пуст.
- `TestLoadStaffLinkedUser` (staff, есть) — зелёный; после PUT следующая загрузка сессии видит `LinkedUserID`.
- `TestPlayersPortAdapter` (`cmd/admin-api`) → активный → ник; `deleted` → `ok = false`; нет → `ok = false`.

**Подсадка.** Не поднимать версию учётки при PUT → `TestMfaResetInitiatorLinkedBetweenRequestAndApprove` красный.
Отдавать `player_link` любому с `staff.accounts.read` → `TestStaffCardPlayerLinkOwnerOnly` красный. Убрать
`checkActor` из PUT → `TestPutPlayerLinkActorRechecked` красный.

**Приёмка.** Связь входа не даёт; право — только владелец; `pending_staff_link` пуст; `pendingOps` admin = 0 после
Task 4–6.

**Done when.** `cd backend && go test -p 4 ./internal/staff/... ./internal/httpapi/admin/... ./cmd/admin-api`;
`gofmt -l`.

**Ловушки.** Подъём версии отсекает **все** ожидающие запросы этого сотрудника (`initiator_changed`) — принято
(R14), отметить в `backend-staff.md`. Письма staff — `staff.NewMailArgs` (очередь `staff`, исполняет admin-api), не
`mail.SendArgs`. Порядок замков staff: строка `approvals` раньше `staff_accounts` — здесь `approvals` не трогается;
решение по заявке (Task 4) держит `FOR SHARE` строки своего сотрудника через `staff.LinkedPlayerTx` (R38) — с
`FOR UPDATE` цели здесь сериализуется, взаимоблокировки нет (заявок связь не трогает); гонку с настоящим
`PutPlayerLink` проверяет Task 4 (`TestPutPlayerLinkVsApprove`) после коммита этой задачи.
`staff_player_links.staff_id` — `ON DELETE CASCADE` уже есть.

**Не трогай.** `identity/**` (кроме чтения `identity.Service` в адаптере), `cmd/api/**`.

### Task 7: `identity/intx`, бан и статус, события удаления

**Цель.** Спека §10.1–10.2, §17 (требования к I-4), хвосты I-2 «бан = ограничение `login`»: межмодульные операции
для `teams` и `moderation`, правила бана и статуса, события заявок и допусков в удалении и анонимизации (R24),
`Service.CaptainPermits`.

**Риск:** A (статус аккаунта, отзыв сессий, удаление поверх бана, гонки с чисткой).

**Файлы.** Создать: `backend/internal/identity/intx/{intx.go,intx_test.go}`, `backend/internal/identity/internal/app/{restrictions.go,permits.go,city.go}`
(+ тесты), `backend/internal/identity/queries/{restrictions.sql,permits.sql}`. Изменить:
`backend/internal/identity/identity.go` (`Service.CaptainPermits`, `CaptainPermit`), `backend/internal/identity/internal/app/{cleanup.go,reauth.go,deletion.go,anonymize.go,login_core.go}`,
`backend/internal/identity/queries/{cleanup.sql,deletion.sql}`, sqlc. Вызовы предиката «заблокирован» (R36, I2 ревью;
сам предикат — Task 2, здесь не правится):
`backend/internal/identity/internal/app/{password.go,mfa_login.go}` (+ тесты), `backend/internal/identity/mail/composer.go`
(+ `composer_test.go`). Письмо смены города (R39, Q6): `backend/internal/identity/internal/app/mail.go` (`MailKinds()`
+= 1), `composer.go`. **Старт — после коммита Task 4** (`composer.go`, `app/mail.go` — его файлы в той же волне).

**Интерфейсы.**
- `package intx`: `type Deps struct{ Pool *pgxpool.Pool; Clock clock.Clock; Geo geo.Service; River *river.Client[pgx.Tx]; Log *slog.Logger }`;
  `func New(d Deps) (*Tx, error)` — собирает `app.NewTx` (Task 2; портов условий нет); методы:
  - `func (t *Tx) RevokeRoleInTx(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID, by RevokeBy, reason string) error`
    — `RevokeBy{StaffID uuid.UUID; Label string; System bool}`; живой допуск капитана в городе → `revoked_at`,
    `revoked_by_staff_id`, `revoke_reason` (≤ 500); заявка допуска `version + 1`, событие
    `captain_permit_revoked` (агрегат `captain_application`); нет допуска → `ErrPermitNotFound`; допуск без
    `application_id` → ошибка-инвариант (R25). Аудит пишет вызывающий (его действие).
  - `func (t *Tx) SetRestrictionsInTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, ch RestrictionsChange) (RestrictionsResult, error)`
    — `RestrictionsChange{SourceID uuid.UUID; Status string /* "" | active | suspended | banned */; Add []Restriction{Capability string; Until *time.Time}; LiftSource bool}`;
    `lockUser`; `banned` без живой строки `login` после применения → `ErrBanNeedsLogin`; бан — отзыв всех сессий
    (`banned`), событие `sessions_revoked`; аккаунт `pending_deletion` — меняется `status_before_deletion`, не
    `status`, `purge_at` не трогается (R11); `deleted` → `ErrUserDeleted`; снятие последней живой строки `login` у
    `banned` → `active` (R12); событие `restrictions_changed {status, capabilities}`, `users.version + 1`.
    Инвариант «живая строка `login` ⇔ заблокирован» (R42, m10 ревью): живая строка `login` после применения при
    итоговом статусе не `banned` (и у `pending_deletion` — `status_before_deletion` не `banned`) → `ErrLoginNeedsBan`,
    ничего не записано; `login` добавляется только вместе со `Status = banned` (или к уже забаненному).
  - `func (t *Tx) ChangeCityInTx(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID) error` — `lockUser`;
    город через `geo.CitySettings` (нет или страна выключена → `ErrCityNotFound`); тот же город — ничего; иначе
    `users.city_id`, `version + 1`, событие `city_changed`; открытая заявка в прежнем городе → `withdrawn` с событием
    (R8, R39; решение пользователя Q6 — вариант (а)) и письмом игроку `mail.SendArgs{kind:
    identity.captain_application_withdrawn_city, ref: application.id}` («заявка отозвана из-за смены города — подайте
    заново в новом городе»; Composer — язык игрока, названия прежнего и нового города; аккаунт `deleted` или заявка
    не `withdrawn` → `ErrSkip`). Паузы такой отзыв **не** ставит (пауза — только от `rejected`, R5). Допуски
    прежнего города не трогает (судьба — спека `teams`).
  - Ошибки: `ErrUserNotFound`, `ErrUserDeleted`, `ErrPermitNotFound`, `ErrCityNotFound`, `ErrBanNeedsLogin`,
    `ErrLoginNeedsBan`.
- `identity.Service.CaptainPermits(ctx context.Context, userID uuid.UUID) ([]CaptainPermit, error)` —
  `CaptainPermit{CityID uuid.UUID; GrantedAt time.Time; ApplicationID uuid.UUID; Probationary bool}` из
  `identity_read_captain_permits`. Фейки `identity.Service` в тестах других пакетов — дополнить.
- Чистка (`cleanup.go`, шаг снятия истёкших ограничений, R12): снята последняя живая строка `login` — `banned` →
  `active` (у `pending_deletion` — `status_before_deletion banned → active`), `version + 1`, событие
  `restrictions_changed`, аудит `identity.ban_expired` от `system`; у `deleted` — по-прежнему молча.
- Предикат «заблокирован» (R36, N1): только вызовы `a.blocked(ctx, q, u, now)` Task 2 (своего `isBlocked` нет) во
  всех местах, где сейчас проверяется только `UserStatusBanned`:
  - `finishLogin` (`login_core.go:195`) — как `banned` (`403 identity.account_blocked` с `blocked_until`), не
    `deletion_pending`;
  - `CancelDeletion` (`deletion.go:132`) с живым токеном — ответ как у недействительного токена (`410
    restore_token_invalid` — сверить с `x-error-codes` операции; `account_blocked` там нет — R29 плана I-2);
  - `password.go`: `resetBlocked` (~117) и ветка приманки `startPasswordReset` (~65–99) — заблокированный получает
    приманку (письма нет), `confirmPasswordReset` → `410` как у недействительного кода;
  - `mfa_login.go` (~72–78, `switch u.Status`) — как `banned`;
  - `identity/mail/composer.go` (~215, письмо `password_reset`) — `ErrSkip` для заблокированного: тот же запрос
    `identitydb.UserBlocked` (Composer без `*App`).
  Запрос выполняется в той же транзакции (под `lockUser` там, где он уже взят).
- `requireFresh` и `deleteAccount` (§17): статус под замком не `active|suspended` → `errSessionGone` (`401`).
- Удаление и анонимизация (R24): `deleteAccount` публикует `captain_application_withdrawn` на каждую отозванную
  заявку (версия + 1 — уже растёт); анонимизация публикует `captain_permit_revoked` на каждый отозванный допуск
  (через общий `app.revokePermitsTx`, его же зовёт `RevokeRoleInTx`).

**Тесты.**
- `TestRevokeRoleInTx` → допуск снят, событие, представление не видит допуск; повтор → `ErrPermitNotFound`.
- `TestSetRestrictionsBan` → бан со строкой `login` → `banned`, все сессии `revoked_reason = banned`, события
  `sessions_revoked` и `restrictions_changed`; без строки `login` → `ErrBanNeedsLogin`, ничего не записано.
- `TestSetRestrictionsLiftReturnsActive` → `LiftSource` последней строки `login` → `active`, событие.
- `TestBanOverPendingDeletion` → `pending_deletion` + бан → статус `pending_deletion`, `status_before_deletion =
  banned`, `purge_at` прежний; вход паролем → `403 account_blocked`; вход со вторым фактором → `403
  account_blocked`; `cancelDeletion` живым токеном → `410`, статус не изменился; `startPasswordReset` → ответ как
  обычно, письма нет (задача письма — приманка/`ErrSkip`); `confirmPasswordReset` с кодом, выданным до бана, →
  `410`; срок бана истёк, чистка → `status_before_deletion = active`; вход → `deletion_pending`; отмена удаления →
  `active`.
- `TestSetRestrictionsLoginNeedsBan` (R42) → `Add login` без `Status = banned` у `active` → `ErrLoginNeedsBan`,
  ничего не записано; `Add login` к уже `banned` — проходит.
- `TestChangeCityWithdrawNoCooldown` (R39) → смена города отозвала заявку → письмо
  `identity.captain_application_withdrawn_city` в tx; подача в новом городе → `201` (паузы нет); допуск прежнего
  города на месте. `TestComposeWithdrawnCity` → ru и en, названия городов.
- `TestCleanupBanExpiryReturnsActive` (роль `worker`) → `banned` с истёкшей строкой `login` → `active`, событие,
  аудит `identity.ban_expired`; две строки, одна истекла → остаётся `banned`.
- `TestRequireFreshBannedIsGone` → загрузчик в кэше видит живую сессию, бан закоммичен → `changePassword` → `401`,
  не `500`; то же `deleteAccount`.
- `TestChangeCityInTx` → город сменён, событие; открытая заявка прежнего города `withdrawn` с событием; выключенная
  страна → `ErrCityNotFound`.
- `TestDeleteAccountPublishesWithdrawn`, `TestAnonymizePublishesPermitRevoked` → события в outbox, payload без ПД.
- `TestCaptainPermitsService` → два города, один отозван → один.
- Под ролью `api` (R13): все `intx`-тесты; отдельный случай `TestSetRestrictionsUnderAdminRole` → `42501`
  (фиксирует границу К2 для спеки `moderation`).

**Подсадка.** Проверять в `resetBlocked` только `UserStatusBanned` вместо `a.blocked` → `TestBanOverPendingDeletion`
(сброс пароля) красный. В `UserBlocked` (Task 2) `@now < until` → `@now <= until` → красные и
`TestEvaluateCaptaincy`, и `TestBanOverPendingDeletion` (случай «бан истекает ровно в `now`» — добавить) — страж
того, что предикат один. Ставить паузу после отзыва сменой города → `TestChangeCityWithdrawNoCooldown` красный. В чистке не
возвращать статус → `TestCleanupBanExpiryReturnsActive` красный. В
`SetRestrictionsInTx` писать `status = banned` поверх `pending_deletion` → `TestBanOverPendingDeletion` красный.
Убрать проверку статуса в `requireFresh` → `TestRequireFreshBannedIsGone` красный (`500`).

**Приёмка.** Бан не бывает вечным из-за чистки; удаление не теряется баном; `intx` не зовётся из admin-api в этом
плане; события удаления и анонимизации есть.

**Done when.** `cd backend && go test -p 4 ./internal/identity/...`; `go test ./internal/archtest/...`; `gofmt -l`.

**Ловушки.** Порядок замков R30 + R7 (по коду `deletion.go:45–77`): `users` → `sessions` → `captain_applications`
→ `role_assignments` → `mfa_resets` → `auth_codes`.
Чистка — `users` первым `SKIP LOCKED` (как `eachAccount`). `identity/intx` импортирует `internal/app` своего модуля —
разрешено; archtest — пары `teams → identity/intx`, `moderation → identity/intx` уже есть. Q1 решён (R11 принят).

**Не трогай.** `identity/admin/**`, `identity/httpapi/**`, `captain_rules.go`, `app/app.go` (Task 2: `NewTx`),
`app/codes.go` (Task 1), `queries/captain_rules.sql` и `app/captain_rules.go` (предикат — Task 2; правка — через
контроллера), `staff/**`.

## Этап C. Админка (фронт)

Макеты (Приложение 4): «Заявки капитанов» — есть в `design/screens/07-Админка.dc.html` (список + правая панель
«Решение по заявке») и `08-Админка-в-телефоне.dc.html` (карточка «Заявка капитана»); «Игроки → Сброс 2FA», связь
«сотрудник → игрок» в карточке сотрудника, глобальные и аварийные флаги, сводка подтверждения сброса 2FA — **в
`design/screens` экранов нет**: дорисовываются по UI-фундаменту (`docs/superpowers/specs/2026-10-28-ui-foundation-design.md`,
`.claude/rules/admin*.md`) из существующих компонентов `src/ui` и строкой в таблице `design/README.md` «дорисовано в
коде». Фронт работает по сгенерированным типам замороженного контракта на моках MSW и от бэкенда волны не зависит,
кроме поля `FeatureFlag.rules` (Task 1).

### Task 8: раздел «Капитаны» и общие файлы фронта I-4

**Цель.** Спека §7.4 «Капитаны» и «Карточка заявки»: очередь со счётчиком ожидающих, карточка панелью, контакты с
причиной, одобрение и отказ. Задача — владелец общих файлов фронта I-4 (R18): локали всех экранов I-4 (ключи
Приложения 3, кроме ключей Task 9 — их добавляет Task 9 тем же исполнителем следующим), меню, пути, маршруты,
фильтры, ключи панелей, реестр команд, журнал (вкладка и подписи identity), моки.

**Риск:** B (ПД в UI, права кнопок; бизнес-правила решает сервер).

**Файлы.** Создать: `apps/admin/src/features/captains/{CaptainsPage.tsx,CaptainsList.tsx,ApplicationDrawer.tsx,ContactDialog.tsx,ApproveDialog.tsx,RejectDialog.tsx,checks.ts,labels.ts,queries.ts,mutations.ts,search.ts,commands.ts,captains.module.css}`
и тесты (`CaptainsPage.test.tsx`, `ApplicationDrawer.test.tsx`, `queries.test.tsx`, `search.test.ts`),
`apps/admin/src/test/identity.ts` (`identityServer()` — заявки, контакты, решения, поиск, статистика; состояние как у
`approvalsServer`). Изменить: `apps/admin/src/app/{paths.ts,nav.ts,routes.tsx,filterKeys.ts,commands.ts}`,
`apps/admin/src/api/client.ts` (+ тест; `ApiError.reasons?: string[]`, `availableAt?: string` из `Problem`),
`apps/admin/src/features/home/{queries.ts,HomePage.tsx}` (+ тест; плитка «Заявки капитанов»),
`apps/admin/src/features/audit/{search.ts,labels.ts}` (+ тест; вкладка «Игроки» — префикс `identity.`),
`apps/admin/src/test/{defaults.ts,sections.ts}`, `apps/admin/src/shots/fixtures.ts`, `apps/admin/messages/{ru,en}.json`.

**Интерфейсы.**
- Пути: `paths.captains = '/captains'`; панель карточки — `?application=<id>` (`PANEL_SEARCH_KEYS` += `application`,
  R21); фильтры `/captains` — `status` (`pending|approved|rejected|withdrawn`, по умолчанию `pending`), `city`
  (`filterKeys.ts`, `parseKeys`; не совпадают с ключами панелей — страж `searchKeys.test.ts`).
- `NAV`: группа «Игроки» (`nav.groups.players`) — пункт «Капитаны» `{ path: paths.captains, labelKey:
  'nav.captains', permission: 'identity.captain_applications.read', icon: 'captain', countLabelKey:
  'nav.counts.captains' }` (пункт «Игроки» добавляет Task 9 в ту же группу). Счётчик — `useCaptainQueueCount()`
  (`features/captains/queries.ts`, ключ `['identity','captains','count']`): `status=pending&limit=100` с
  `...background()`, `refetchInterval: 60_000`, `enabled` по праву; «99+» — 100 строк или `next_cursor`; подключение
  в `ActiveShell` `counts` по образцу `useIncomingApprovalsCount`.
- Список — `DataTable` (`tableId: 'captains'`): ник, город (название — `useCity…` как в «Справочниках»), возраст,
  стаж, подана (`RelativeTime`); у решённых — итог, кто решил, когда. Чипы статуса — `Tabs` или чипы `DataTable`
  (как в «Подтверждениях»); город — `CityField` (модератору — только свои города, как в приглашении).
- Карточка — `Drawer` (`usePanelParam('application')`): поля формы, «Телефон указан / не указан», условия
  (`checks.ts`: `age|restrictions|profile|captaincy` × `ok|failed|unavailable` → значок и текст; незнакомое — «—»),
  пояснение при `unavailable` («проверка появится с модулями профиля и команд — одобрить нельзя»), история, связанные
  аккаунты (число). Подвал: «Одобрить» (`disabled`, пока хоть одно условие не `ok`, с подсказкой — R22), «Отклонить».
  У решённой — итог вместо кнопок. «Показать контакты» — кнопка в теле (право `identity.pii.read`).
- `ContactDialog` — `Modal`: поле «Причина» (3–200, обязательно) с подсказкой «не указывайте персональные данные
  игрока» (`captains.contact.reason_hint`; R41 — причина в журнале неизменяема и переживает анонимизацию) → `GET
  …/contact` с `X-WF-Access-Reason: encodeURIComponent(reason)`; запрос — по явному «Показать» (`useQuery` с
  `enabled` по нажатию или вызов вне кэша запросов), опции — `staleTime: Infinity`, `gcTime: 0`, `retry: false`, `refetchOnWindowFocus: false`, `refetchOnReconnect: false`,
  `meta: ONE_SHOT` (образец — `apps/admin/src/features/auth/SetupMfaPage.tsx:168–183`; I3 ревью: иначе фокус окна
  или переподключение повторяют чтение ПД — лишняя запись в журнале); ключ без причины не строить (R19); ответ —
  в состоянии диалога; закрыли — данные стёрты. Почта и телефон — с `CopyButton`.
- `ApproveDialog` — `Modal` с необязательным комментарием (≤ 1000); `RejectDialog` — `SelectField` кода
  (`captains.rejection.<код>`), комментарий (обязателен при `other`; пробелы — не комментарий). Мутации —
  `useAdminMutation`; успех — тост, инвалидация `['identity','captains']`; `409 application_closed` — тост и
  перечитывание карточки; `409 captain_ineligible` — причины `ApiError.reasons` текстами `captains.reasons.<код>`
  в карточке; `409 self_action` — текст `errors.identity.self_action`; `404` — «Заявка не найдена», панель
  закрывается.
- На телефоне решения доступны (не `staff.*`; R27), карточка — `Drawer` во всю ширину по правилам `admin-ui`.
- Команды палитры — `features/captains/commands.ts`: раздел «Капитаны» (`shortcut` — свободное `g …`, страж
  `commands.test.ts`).
- Главная: плитка «Заявки капитанов» (`AttentionTile`) по тому же запросу, что счётчик (одна подписка), без права —
  нет плитки и запроса.
- Журнал: вкладка «Игроки» — префикс `identity.`; подписи `audit.actions.identity.*` для всех действий identity
  (I-2 и I-4, список — `app/audit.go`), объекты `audit.objects.{user,captain_application,mfa_reset}`.
- Моки: `defaultHandlers` (`src/test/defaults.ts`) — `GET /v1/identity/captain-applications` (пусто): счётчик опрашивает
  на любом разделе, иначе каждый тест каркаса падает на неописанном запросе; `sectionsServer()` — `identityServer()`;
  shots — фикстуры очереди и карточки (`?shots-fixture=captains-busy`).

**Тесты.**
- `CaptainsPage.test.tsx` → список из мока: колонки, фильтр статуса в адресе, «Показать ещё»; модератор — фильтр
  города только из его городов; без права — `ForbiddenState` (каркас).
- `ApplicationDrawer.test.tsx` → `checks` с `unavailable` → «Одобрить» недоступна и подсказка; все `ok` → одобрение
  с комментарием → тост, строка ушла из `pending`; отказ `other` без комментария → ошибка поля до запроса; `409
  application_closed` → тост и перечитывание; `409 captain_ineligible {reasons}` → причины текстом; `self_action` →
  текст.
- `ContactDialog` (в том же файле) → причина 2 символа — ошибка поля; запрос несёт заголовок в percent-encoding
  (кириллица); подсказка о ПД видна; открыть диалог и показать контакты → `fireEvent.focus(window)` (и событие
  `online`) → второго запроса нет (счётчик вызовов мока = 1; I3); после закрытия повторное открытие не показывает
  прежние данные; `storageDump()` без почты и причины.
- `queries.test.tsx` → счётчик: 100 строк → «99+»; без права — запроса нет.
- `search.test.ts` → незнакомый статус отбрасывается.
- `HomePage.test.tsx` → плитка с числом; ошибка — «Повторить» в плитке.
- Аудит: `search.test.ts` вкладки «Игроки»; стражи локалей (`labels.test.ts`, `keys.test.ts`,
  `error-codes.test.ts`, `vocabulary.test.ts`), `controls.test.ts`, `csp.test.ts`, `layout-css.test.ts`,
  `contrast-css.test.ts`, `router.lazy.test.tsx`, `nav.test.ts`, `commands.test.ts`, `searchKeys.test.ts` — зелёные.

**Подсадка.** Включить «Одобрить» при `unavailable` → `ApplicationDrawer.test.tsx` («недоступна») красный. Класть
контакты в кэш с `gcTime` по умолчанию → случай «повторное открытие» красный. Убрать `refetchOnWindowFocus: false`
→ случай «фокус окна» красный.

**Приёмка.** Снимки `./task admin:shots -- captains '/captains?application=<id>' --out .superpowers/sdd/<сессия>/shots/i4-captains/`
(1440/768/390 × тёмная/светлая, `--role moderator`, `--locale en`, `--touch` для 390) приложены к ревью; вёрстка — по
макету 07 с расхождениями Приложения 4; новых компонентов в `src/ui` нет (иначе — история Ladle и страж
`stories.test.ts`).

**Done when.** `./task admin:test`; `./task admin:typecheck` (своего пакета);
`prettier --check` по путям.

**Ловушки.** Ключи локалей для e2e — в `e2e/keys.ts` их добавляет Task 11, не эта задача. Заголовок причины — только
`encodeURIComponent` (контракт; сырая кириллица — 400). `RelativeTime`/`useDateTime` — не `Intl` в `features`.
`ApiError.reasons` — общий клиент: тест `client.test.ts`.

**Не трогай.** `src/ui/**` (кроме случая, когда без нового компонента не обойтись — тогда через контроллера),
`features/{staff,flags,approvals}` (Task 9), `e2e/**` (Task 11).

### Task 9: «Игроки» — сброс 2FA; связь в карточке сотрудника; флаги способов входа; сводка подтверждения

**Цель.** Спека §7.4 («Поддержка: сброс 2FA», «Связь сотрудник → игрок», «Флаги функций»), §4.3, §7.5, §3.15;
правила фронта identity — новый файл `.claude/rules/admin-identity.md`.

**Риск:** B (ПД в UI; аварийное выключение входа).

**Файлы.** Создать: `apps/admin/src/features/players/{PlayersPage.tsx,LookupForm.tsx,PlayerCard.tsx,RequestResetDialog.tsx,queries.ts,mutations.ts,commands.ts,players.module.css}`
и тесты, `apps/admin/src/features/staff/PlayerLinkBlock.tsx` (+ тест), `apps/admin/src/features/flags/{authFlags.ts,EmergencyDialog.tsx}`
(+ тесты), `.claude/rules/admin-identity.md` (`paths: apps/admin/src/features/{captains,players}/**`). Изменить:
`apps/admin/src/app/{paths.ts,nav.ts,routes.tsx,commands.ts}` (пункт «Игроки»), `apps/admin/src/features/staff/StaffDrawer.tsx`
(+ `StaffPanels.test.tsx`), `apps/admin/src/features/flags/{FlagsMatrix.tsx,FlagsList.tsx,toggle.tsx}` (+ тесты),
`apps/admin/src/features/approvals/summary.ts` (+ `summary.test.ts`), `apps/admin/src/test/{identity.ts,staff.ts,flags.ts}`,
`apps/admin/src/shots/fixtures.ts`, `apps/admin/messages/{ru,en}.json`, `design/README.md` (строки «дорисовано»),
`.claude/rules/admin.md` (список областей — `admin-identity`).

**Интерфейсы.**
- «Игроки» (R20): `paths.players = '/players'`, пункт `NAV` в группе «Игроки» `{ labelKey: 'nav.players',
  permission: 'identity.pii.read', icon: 'team' }`. Экран: `LookupForm` (почта, причина 3–200; `Form`) →
  `GET /v1/identity/users/lookup` с заголовками `X-WF-Lookup-Email`, `X-WF-Access-Reason` (`encodeURIComponent`) →
  `PlayerCard`: ник, город, статус (чип; незнакомый — общий текст), «2FA включена / нет», назначенный сброс с датой и
  «Отменить сброс» (`ConfirmDialog` → `cancelMfaReset`; после `200` карточка обновляется **локально по ответу**
  (`mfa_reset` снят) — повторного поиска нет: он потребовал бы снова причину и дал бы лишнюю запись `pii_read`, m7
  ревью; право `identity.mfa.reset`, «вдвоём» трактуется как разрешение —
  §7.2, поэтому кнопка видна при `allow` и `needs_approval`), «Запросить сброс» (`RequestResetDialog`: причина 3–500,
  номер обращения ≤ 100 → `202` → `PendingApprovalNotice`, инвалидация `['approvals']`; без 2FA или со сбросом —
  кнопки нет, сервер всё равно ответит `409`). `404 user_not_found` — «Игрок с такой почтой не найден» в форме. Почта,
  причина и результат — только в памяти экрана (R19): не в адресе, не в `useDraft`, не в «Недавнем»; поиск — по
  явной отправке формы, опции запроса — `staleTime: Infinity`, `gcTime: 0`, `retry: false`, `refetchOnWindowFocus: false`, `refetchOnReconnect: false`,
  `meta: ONE_SHOT` (образец — `apps/admin/src/features/auth/SetupMfaPage.tsx:168–183`; I3 ревью: иначе фокус окна
  или переподключение повторяют чтение ПД — лишняя запись в журнале); уход со страницы стирает. У поля причины — та же подсказка
  «не указывайте персональные данные игрока» (R41).
- `PlayerLinkBlock` в `StaffDrawer` — только при `useCan('staff.player_links.manage')`: связь (ник, когда, кем) или
  «Не связан»; «Связать» — `Modal`: почта и причина (подсказка R41) → `findPlayerByEmail` (опции запроса — как в
  «Игроках», I3) → подтверждение «Связать с <ник>?» →
  `putStaffPlayerLink` (`staff.player_link_taken`, `staff.player_not_found` — текстом в окне); «Снять» —
  `ConfirmDialog` → `deleteStaffPlayerLink`. Успех — инвалидация `['staff','accounts']`. На телефоне доступно (R27).
- Флаги (R3, §4.3, §7.5): `rules.global_only` — строка без ячеек городов (матрица: только «Везде»; список на телефоне —
  без раскрытия городов); `rules.requires` — подсказка «требует <флаг>» и «нужен для <флаг>» (обратная связь — по
  `requires` других флагов); `authFlags.ts`: `LOGIN_FLAG_METHOD = { auth_apple_login: 'apple', auth_google_login:
  'google', auth_passkeys_login: 'passkeys' }` (страж: ключи ⊆ `FeatureFlagKey` из `admin.d.ts`). Выключение
  аварийного флага — `EmergencyDialog`: `GET /v1/identity/auth-methods/usage` → «Только этим способом входят N
  игроков — им останется „Забыли пароль?“», подтверждение; ошибка статистики — диалог с «Повторить» и без числа
  (выключить можно — авария важнее). `409 platform.flag_requires`, `422 platform.flag_global_only` — `errors.*` (есть).
  Аварийные строки — пометка «авария: выключает вход всем» (`flags.<ключ>.note` есть).
- Сводка подтверждения `identity.mfa_reset` (R28): `summarizePayload(payload, 'identity.mfa_reset')` → «Игрок
  <user_id>», «Обращение <ticket>», «Причина <reason>»; незнакомые поля — «и ещё N полей»; на телефоне решение
  доступно (не `staff.*`).
- `.claude/rules/admin-identity.md` — коротко: разделы, ПД только в памяти, заголовки причин, «Одобрить» по условиям,
  счётчик и `defaultHandlers`, флаги `rules` и аварийный диалог.

**Тесты.**
- `PlayersPage.test.tsx` → поиск: заголовки закодированы; найден — карточка; `404` — текст в форме; найден →
  `fireEvent.focus(window)` → второго запроса поиска нет (I3); запрос сброса → `202` и пометка «на подтверждение»;
  отмена сброса → тост, в карточке сброса нет, запросов поиска по-прежнему 1 (m7); `storageDump()` и `location` без
  почты и причины; уход и возврат — пустая форма.
- `PlayerLinkBlock.test.tsx` → не владелец — блока нет; связать (поиск → подтверждение → PUT) — блок с ником;
  фокус окна во время подтверждения — второго поиска нет;
  `player_link_taken` — текст в окне; снять — «Не связан».
- `FlagsMatrix.test.tsx`/`FlagsList.test.tsx` → глобальный флаг без городов; `requires` — подсказка.
- `EmergencyDialog.test.tsx` → выключение `auth_apple_login` показывает число из мока; ошибка статистики — «Повторить»
  и кнопка выключения доступна; включение аварийного — без диалога.
- `summary.test.ts` → сводка `identity.mfa_reset`.
- Стражи локалей и UI — зелёные (как в Task 8).

**Подсадка.** Положить почту поиска в search-параметр → `PlayersPage.test.tsx` («`location` без почты») красный.
После отмены сброса перезапускать поиск → случай m7 красный.
Показывать городские ячейки у `global_only` → тест матрицы красный.

**Приёмка.** Снимки `./task admin:shots -- players flags '/staff?account=<id>' --out .superpowers/sdd/<сессия>/shots/i4-players/`
(три ширины × две темы, `--role owner|admin|moderator`, `--touch` для 390); `design/README.md` — строки «Игроки»,
«Связь с учёткой игрока», «Флаги: только везде и аварийный диалог», «Капитаны: отличия от макета 07».

**Done when.** `./task admin:test`; `./task admin:typecheck`; `prettier --check` по путям.

**Ловушки.** `findPlayerByEmail` в блоке связи — тот же аудит ПД, что в «Игроках» (причина обязательна и там).
`cancelMfaReset` — `200`, не `202`: не `PendingApprovalNotice`. Флаги: `rules` есть только после Task 1 (`admin.d.ts`).

**Не трогай.** `features/captains/**` (Task 8), `src/ui/**`, `e2e/**`.

## Этап D. Документы, e2e, закрытие

### Task 10: документы этапа (бэкенд и спеки)

**Цель.** Спека §16 в части этапа 9 и хвост I-2 «§16: спека бэкенда §4.4, `docs/05`, ответ нативщику»; поправки
спеки по R этого плана (R9 плана I-2: общие документы — одной задачей по разделам «Документы» отчётов 1–7).

**Риск:** C.

**Файлы.** Изменить: `docs/superpowers/specs/2026-11-13-identity-design.md` (новый §0.2 «Поправки плана I-4»: R2,
R5, R6, R8–R15, R24, R25, R26, R36–R42 и решения пользователя Q1–Q6 от 16.11 с пометкой «план I-4»; отдельной
строкой — R41: причина доступа к ПД в `audit_log.reason` неизменяема и переживает анонимизацию, в UI подсказка; §3.12 — пауза по пользователю, город заявки и смена города;
§5.5 — представление `identity_read_auth_method_usage`; §5.6 — то же; §10.2 — сигнатуры `intx` и правило статуса;
§15 — I-3 после I-4 (решение пользователя 16.11); §17 — требования I-4 закрыты, ссылки на задачи),
`docs/superpowers/specs/2026-10-01-backend-architecture-design.md` (§4.4 — строка `GrantRoleInTx` удаляется,
одобрение внутри identity; исключение R38 — порт `identity.StaffLinks`: код staff (`staff.LinkedPlayerTx`) в
транзакции identity, прецедент — хук `LockActors` подтверждений; то же — в §0.2 спеки identity; §4.1 — `captain_applications` у identity, если ещё не исправлено в I-2),
`docs/01-домен-и-правила.md` (заявка в капитаны: условия, пауза 7 дней, ручное одобрение, проверка невозможна до
модулей профиля и команд; бан и удаление), `docs/05-ролевая-модель.md` (§5.3 U5 — исполнено identity планом I-4;
связь видна только владельцу; подъём версии учётки), `.claude/rules/backend-identity.md` (капитанство: порты, тег
`wf_e2e`, одно правило условий, одобрение под `admin` без замка `users`, `intx`, бан и статус, порядок замков с
`captain_applications`), `.claude/rules/backend-http.md` (заголовки `X-WF-Lookup-Email`, `X-WF-Access-Reason` —
декодирование в модуле, журнал запросов без них), `CLAUDE.md` (список правил: `admin-identity`; раздел «Проверки» —
`go test -tags wf_e2e ./cmd/...`), `backend/migrations/README.md` (если Task 1 не дописал). Вне коммита:
`.superpowers/sdd/<сессия>/native-answer-i4.md` — текст для нативщика (капитанство на стенде отвечает
`checks_unavailable` до модулей `players`/`teams` — решение Q3, тестовой сборки стенда нет; `getMyCaptaincy` при
выключенной стране города — тоже `checks_unavailable`: код существующий, контракт заморожен, нового кода не будет;
поля `Captaincy`; пауза 7 дней по игроку (Q4), смена города отзывает заявку без паузы и с письмом (Q6); I-3 позже) — пользователь переносит в свой
документ `docs/WF_…_2026_10_19.md` сам (его не править и не коммитить).

**Тесты.** Нет (документы). Сверка: каждое R этого плана, меняющее поведение, отражено в спеке; `grep GrantRoleInTx`
по `docs/` — только в истории (§0/§1.5).

**Приёмка.** Документы не расходятся с кодом (ревьюер сверяет по диффу задач 1–7); правила — коротко, со ссылками на
спеку, без копий текста.

**Done when.** `git diff --stat` — только пути задачи; `rg -n "GrantRoleInTx" docs` — ожидаемые места.

**Ловушки.** `docs/00` не ведётся. Матрица прав `docs/05` генерируется `./task gen` — правка прав этим планом не
делается, значит и `gen` не нужен.

**Не трогай.** Код, `apps/admin/**`, `.claude/rules/admin*.md` (Task 9).

### Task 11: живой e2e — капитанство через api и админку, ядро identity

**Цель.** Спека §13 «e2e»: капитанская заявка через админку с тестовыми портами (К1) и сценарии ядра (регистрация с
письмом из Mailpit, вход, обновление, выход, удаление и отмена) — в харнессе `admin:e2e` (R23).

**Риск:** B (харнесс, сборка с тегом, порты и секреты прогона).

**Файлы.** Изменить: `scripts/e2e-admin.sh` (сборка `api` и `admin-api` с `-tags wf_e2e`; запуск `api` на своём порту с
базой `wf_e2e` под ролью `api`, кольцами прогона и Mailpit воркера; готовность — `/v1/health`), `apps/admin/e2e/env.ts`
(+ `env.test.ts`: порт и окружение `api`, `WF_E2E_*` → `API_*` только у `api`), `apps/admin/e2e/keys.ts`
(+ `keys.test.ts`), `apps/admin/playwright.config.ts` (сервер `api`, проект `identityProject` — как `geoProject`: один
браузер). Создать: `apps/admin/e2e/api.ts` (клиент public API на `APIRequestContext`: `device` в теле, PoW по
`humancheck`, если риск его требует), `apps/admin/e2e/identity-api.spec.ts`, `apps/admin/e2e/captaincy.spec.ts`.
Изменить `.claude/rules/admin-e2e.md` (сценарии и сервер `api`).

**Окружение `api` прогона** (не проверено ревьюером — исполнитель первым шагом сверяет с
`backend/internal/platform/config/config.go`, обязательные помечены `required`): `API_HTTP_ADDR` (свой порт),
`API_DATABASE_URL` (база `wf_e2e`, роль `api`), `API_JWT_SEEDS`, `API_HUMANCHECK_KEYS` (≥ 32 байт),
`API_IDENTITY_SECRET_KEYS` (**то же кольцо**, что `WORKER_IDENTITY_SECRET_KEYS` — из `WF_E2E_IDENTITY_SECRET_KEYS`),
`API_IDENTITY_DEVICE_KEYS`, `API_IDENTITY_TOTP_ISSUER`, `API_LEGAL_DOCUMENTS` (документы сида e2e),
`API_EMBED_WORKER=false` (воркер в харнессе отдельный; ключ появляется после слияния `spec/embedded-worker` —
предусловие старта), `API_DOCS_ENABLED=false`; пустые: `API_TRUSTED_PROXIES`, `API_CLIENT_IP_HEADER`,
`API_BFF_*`, `API_IPGEO_DB`, `API_WELLKNOWN_*`; `API_RATE_LIMITS` — по умолчанию (риск PoW — «Ловушки»). Секреты —
одноразовые `WF_E2E_*` скрипта (как `WF_E2E_STAFF_SECRET_KEYS`), в `MSYS2_ENV_CONV_EXCL`; `scripts/e2e-admin.sh`
сбрасывает унаследованные `API_*` так же, как `ADMIN_*|WORKER_*` (строка `case "$k"`).

**Интерфейсы.**
- `identity-api.spec.ts` (serial): `startSignup` → код из Mailpit (`waitForMail`) → `confirmSignup` → `201` с
  токенами → `refreshSession` → `logout` → `login` → `deleteAccount` (свежий вход есть сразу после входа) → `login` →
  `deletion_pending` → `cancelDeletion` → `authenticated`.
- `captaincy.spec.ts` (serial): игрок из `api.ts` (регистрация как выше, город из сида e2e) → `submitCaptainApplication`
  → владелец в админке: «Капитаны» (счётчик 1) → карточка (все условия `ok` — тестовые порты) → «Показать контакты» с
  причиной (почта игрока) → «Одобрить» → итог; игрок: `getMyCaptaincy.permits` — город; журнал — запись
  `identity.captain_application_approved` и `identity.pii_read`. Вторая заявка второго игрока — «Отклонить» `other`
  с комментарием → письмо `captain_application_rejected` в Mailpit → игрок: `submit` → `409
  application_cooldown`. «Игроки»: поиск по почте игрока → карточка, `mfa_enabled = false` (запрос сброса недоступен).
- Без `securitypolicyviolation` на всём пути (как в `geo.spec.ts`).

**Тесты.** Сами сценарии; `env.test.ts` — окружение `api` без dev-значений, ключи `WF_E2E_*` не протекают в Vite и
Mailpit; `keys.test.ts`.

**Подсадка.** Собрать `admin-api` без тега → `captaincy.spec.ts` падает на «Одобрить» (условия `unavailable`) — в отчёт
(одна подсадка; повторных прогонов «для статистики» не делать — память `no-repeat-runs`).

**Приёмка.** Один зелёный прогон `./task admin:e2e` (локально `WF_E2E_PROJECTS=chromium,prod`, если нет WebKit);
прод-образ (`backend/Dockerfile`) — без тега (`grep` по Dockerfile в отчёт).

**Done when.** `./task admin:e2e`; `node --test "scripts/*.test.mjs"` если правились скрипты; `./task scripts:test`.

**Ловушки.** Риск входа и регистрации может требовать PoW с `127.0.0.1` — проверить первым шагом (`risk.DefaultPolicies`,
`humancheck`); решать задачу PoW в `api.ts`, а не ослаблять политику окружением. Мобильный клиент без `device` —
`422 device_required`. Письма игрокам отправляет воркер (уже в харнессе для geo) — `api` ставит задачи в ту же базу.
Кольца `api` и воркера — одно (иначе письма с кодами не собираются). Пары браузеров на одной базе — сценарий
капитанства берёт заявку своего игрока по ник-префиксу прогона.

**Не трогай.** `src/**` админки, бэкенд (кроме сборки в скрипте).

### Task 12: закрытие заглушек I-4 и решение о заморозке (последняя задача I-4)

**Цель.** Хвост I-1 R2/R17 и I-2 «Удаление `pendingOps`, `TestPendingOpsExpire`, решение о `identity-freeze`» с учётом
решения пользователя 16.11 (I-3 после I-4): 14 операций I-3 остаются заглушками (R1).

**Риск:** B (стражи).

**Файлы.** Изменить: `backend/internal/httpapi/admin/nostub_test.go` (удалить `pendingOps`, `pendingOpsAtFreeze`,
`pendingOpsAtFreezeSHA256`, `pendingStaffOps`, `TestPendingOpsExpire` admin и ветку `pendingOps` в
`TestNoStubOperations` — строгое «ни одной 501», как после плана B Task 16), удалить
`backend/internal/httpapi/admin/pending_{captains,players,staff_link}_test.go`; `backend/internal/httpapi/public/nostub_test.go`
(`pendingOps = pendingI3` — остальные области пусты; `TestPendingOpsExpire` public и `pendingOpsAtFreeze` остаются —
их удаляет последняя задача I-3), `backend/internal/httpapi/public/identity_core_test.go` (`TestCoreOpsNotStub`:
`pendingOps` public = `pendingI3` точно, 14 имён; комментарии), `backend/internal/httpapi/public/pending_i3_test.go`
(комментарий: «удаляет последняя задача I-3»), `.claude/rules/backend-identity.md` (строка «Стражи»),
`.claude/rules/contracts.md` (строка 35 — «кроме временных `pendingOps` public и admin»: admin-списка больше нет,
public — только I-3 до последней задачи I-3; m5), `.claude/rules/backend-http.md` (абзац про `pendingOps`
строки ~92–95: admin — строгое «ни одной 501», public — `pendingI3`; m5). Вне кода:
`.superpowers/sdd/2026-11-13-identity-1/native-handoff.md` — запись в журнал «I-4: капитанство реализовано; заморозка
среза — до I-3» (без правки среза).

**Интерфейсы.** Решение о заморозке (R1, решение пользователя Q2 16.11): `contracts/test/identity-freeze.test.mjs`
**не меняется** — оба среза (public и admin) заморожены до последней задачи I-3, где пользователь решает: удалить
страж или оставить до `contracts-v1.0.0`.

**Тесты.**
- `TestNoStubOperations` admin — без списка: ни одной 501.
- `TestCoreOpsNotStub` → 14 = `pendingI3`; любая из 36 операций I-2/I-4 — не 501.
- `TestPendingOpsExpire` public — зелёный.

**Подсадка.** Вернуть стаб `approveCaptainApplication` (501) → `TestNoStubOperations` admin красный. Вернуть
`getMyCaptaincy` в `pendingOps` → `TestCoreOpsNotStub` и `TestPendingOpsExpire` красные.

**Приёмка.** В admin нет временных списков; в public — только I-3; заморозка не тронута (Q2); правила
`contracts.md` и `backend-http.md` не расходятся со стражами.

**Done when.** `cd backend && go test -p 4 ./internal/httpapi/...`; `node --test "contracts/test/*.test.mjs"`.

**Ловушки.** `TestPendingOpsExpire` admin содержал пробу подставного контракта — она уходит вместе с тестом;
`pendingViolations` admin больше не нужен. Порядок: после коммитов задач 3–6.

**Не трогай.** Сценарии, контракт, `contracts/test/identity-freeze.test.mjs`.

### Task 13: выкатка I-4 на стенд (контроллер)

**Цель.** Стенд с капитанством и админкой identity; проверка, что прод-образ подключает только «недоступно».

**Риск:** B (миграция на Neon, порядок выкатки).

**Шаги (контроллер, после итогового ревью ветки, волны исправлений, полного `./task ci`, пуша и слияния PR — по
команде пользователя).**
1. До слияния — на `wf-dev` (Neon) роль мигратора: `SELECT count(*) FROM captain_applications; SELECT count(*) FROM
   users WHERE status = 'pending_deletion';` — `0027` пересоздаёт CHECK `users` (короткий `ACCESS EXCLUSIVE`); время
   наката — в журнал сессии.
2. Слияние → `wf-api` накатывает миграции и стартует → admin-api и воркер (в режиме, который ввёл план
   embedded-worker) ждут схему.
3. Проверка: игрок стенда → `getMyCaptaincy` → `eligibility.reasons = [checks_unavailable]`; `submit` → `409
   captain_ineligible`; админка: «Капитаны» пусто, «Игроки» — поиск своего тестового игрока с причиной, запись в
   журнале; флаги — `auth_*` без городов, диалог аварийного флага показывает нули.
4. `docs/deploy-dev.md` — раздел «Капитанство на стенде» (почему `checks_unavailable`, тег `wf_e2e` только в e2e).

**Приёмка.** Результаты шага 3 — в журнале сессии (без почты); новых env-ключей нет.

**Ловушки.** Тег `wf_e2e` в `backend/Dockerfile` недопустим (спека §10.3) — даже «на время» для нативщика (Q3).

## Порядок исполнения

```
Волна 1 (основа):     Исп. А: 1 (схема, правила флагов, ./task gen — единственный pnpm волны)
                      Исп. Б: 2 (каркас: порты, сборка, домен, каталоги, раскладка заглушек)
Волна 2 (сценарии):   Исп. А: 3, затем 4 — после коммитов 3 и 6 (капитанство public → заявки в админке)
                      Исп. Б: 5 ∥ 6 (поиск и статистика; связь «сотрудник → игрок») — пачка «3 ∥ 5 ∥ 6»
                      Исп. В: 7 (intx, бан и статус, события удаления) — после коммита Task 4 (I2:
                              composer.go, app/mail.go)
Волна 3 (фронт, доки):Исп. А: 8 → 9 (фронт; единственный pnpm волны)
                      Исп. Б: 10 (документы; после коммитов 1–7)
Волна 4 (закрытие):   Исп. А: 11 (живой e2e; pnpm)   Исп. Б: 12 (после коммитов 3–6)
Затем: итоговое ревью ветки → волна исправлений → полный ./task ci → пуш и PR по команде → Task 13 (контроллер).
Ревью — одно на группу исполнителя в волне (wf-reviewer, opus, effort high).
```

- **Пути не пересекаются внутри волны.** Волна 1: А — `migrations/`, `grants_test.go`, `ownership.go`,
  `identity/queries/models.sql`, контракт флагов, `httpapi/admin/flags.go`, сгенерированное; Б — корень `identity`,
  `identitytest`, `domain/captaincy.go`, `app/{app.go,audit.go,captain_rules.go}`, `identity/admin/*`, `cmd/*/`,
  `httpapi/admin/{nostub_test.go,pending_*}`, `identity_core_test.go`, `backend/locales`. Общий для обеих —
  `identitydb/*` (sqlc): Б пишет `captain_rules.sql`, А — якорь моделей; вывод sqlc детерминирован, расхождение
  `gen:check` сводит контроллер (R5 плана I-2). Волна 2: А — `app/{captaincy,captain_admin}.go`,
  `queries/{captaincy,captain_admin,mail}.sql`, `identity/httpapi/captaincy.go`, `identity/admin/captains.go`,
  `identity/mail/*`, `app/mail.go`, `public/{captaincy_test,nostub_test}.go`, `admin/{pending_captains,identity_captains}_test.go`;
  Б — `app/lookup.go`, `queries/lookup.sql`, `identity/admin/players.go`, `admin/{pending_players,identity_lookup,pending_staff_link,staff_player_link}_test.go`,
  `staff/**`, `cmd/admin-api/{main.go,players_port.go}`; В — `identity/intx/*`,
  `app/{restrictions,permits,city,cleanup,reauth,deletion,anonymize,login_core,password,mfa_login}.go`,
  `queries/{restrictions,permits,cleanup,deletion}.sql`, `identity/identity.go` (`CaptainPermits`); `identity/mail/composer.go`
  и `app/mail.go` — у Task 7 только после коммита Task 4 (последовательно, не параллельно). Пересечение —
  только `identitydb/*` (sqlc, как выше) и `identity.go` (Б волны 1 закончил до волны 2). Волна 1: Task 1 правит
  ещё `app/codes.go`, `queries/codes.sql` (R40) и фабрики флагов админки; Task 2 — `staff/{linked.go,queries/linked.sql}`,
  `staffdb`, `cmd/admin-api/staff_links.go`, сборки окружений тестов, `backend/Taskfile.yml`.
- **Общие файлы и владельцы.** `identity/identity.go`, `app/app.go`, `events.go`, `audit.go`, `backend/locales` —
  Task 2 (волна 1), в волне 2 только Task 7 добавляет `Service.CaptainPermits` в `identity.go`; `cmd/admin-api/main.go` —
  Task 2 (волна 1), Task 6 (волна 2); `cmd/api/main.go` — только Task 2; `messages/{ru,en}.json`, `nav.ts`, `paths.ts`,
  `routes.tsx`, `commands.ts` админки — один исполнитель (8 → 9); `e2e/keys.ts` — Task 11.
- **Зависимости:** 2 ⟂ 1 (sqlc якорь — контроллер сводит); 3 ← 2; 4 ← 2, 3 (`evaluateCaptaincy` и порт `StaffLinks` из 2, сценарий
  подачи для тестов паузы — из 3; настоящий `PutPlayerLink` для `TestPutPlayerLinkVsApprove` — из 6); 4 ← 6;
  5 ← 1, 2; 6 ← 2; 7 ← 1 (CHECK `banned`), 2 (`NewTx`), 4 (`composer.go`, `app/mail.go`); 8 ← 1 (типы `admin.d.ts`), 9 ← 1, 8;
  10 ← 1–7; 11 ← 3–9; 12 ← 3–6; 13 — после PR.
- **pnpm:** волна 1 — только Task 1 (`./task gen`); волна 3 — только исполнитель фронта; волна 4 — только Task 11.
  Task 12 гоняет `node --test` (не pnpm).
- **Пачки сессий** (3–5 задач, `work-cadence`): «1 ∥ 2» → «3 ∥ 5 ∥ 6» → «4 ∥ 8» → «7 ∥ 9» → «10 ∥ 11 ∥ 12» →
  итоговое ревью и ci → «13» (4 — после коммитов 3 и 6; 7 — после коммита 4; 10 — после коммитов 1–7; 11 — после 3–9). Не больше 3 исполнителей (и 4 opus-агентов) одновременно.
- **Ворота перед ревью (контроллер):** «Done when» задачи; `go vet`/typecheck пакета; `gofmt -l`/`prettier --check` по
  путям из файла (число путей сверено); `git status` против списка путей отчёта; для задач 2 и 11 — ещё
  `go test -tags wf_e2e ./cmd/api ./cmd/admin-api -run CaptainChecks` и `go vet -tags wf_e2e ./cmd/api ./cmd/admin-api`
  (m8); для задачи 1 — ещё `./task admin:typecheck` (I1).
- **Коммит** — контроллер, один на задачу; пример: «I-4 Task 4: заявки в капитаны в админке — очередь, карточка,
  решения и письма».
- **Итоговое ревью ветки** по областям: (а) порты, сборка и правило условий; (б) заявки и решения, гонки; (в) ПД и
  скоуп модератора, связь сотрудника; (г) `intx`, бан и статус; (д) фронт. Веер ревью больше чем одним агентом — с
  вопросом пользователю (память `ask-before-review-fanout`).

## Решения плана

Решения, которых нет в спеке или которые её уточняют; поведенческие вносятся в спеку в Task 10.

- **R1. I-4 раньше I-3 — последствия для заглушек и заморозки** (решение пользователя 16.11). Public: в конце I-4
  `pendingOps = pendingI3` (14 операций), `TestPendingOpsExpire` public и `pendingOpsAtFreeze` остаются — их удаляет
  последняя задача I-3. Admin: все 9 операций реализует I-4 (у I-3 операций admin нет) — список, `pendingOpsAtFreeze`
  и `TestPendingOpsExpire` admin удаляются в Task 12, `TestNoStubOperations` admin снова строгий. Заморозка
  (`identity-freeze.test.mjs`): оба среза остаются замороженными до последней задачи I-3 — там же решение «удалить или
  оставить до `contracts-v1.0.0`» (I-1 R17). Пока I-4 в работе, `TestCoreOpsNotStub` требует подмножество
  (`pendingOps ⊆ I-3 ∪ I-4`), в Task 12 — точное равенство `pendingI3`. `getAuthMethodsUsage` до I-3 честно отвечает
  нулями (привязок и ключей нет).
- **R2. Миграция `0027`** — представление `identity_read_auth_method_usage` (агрегаты без `user_id`; права — ветка
  представлений `grants.sql`, исполняется правами владельца) вместо расширения прав `admin` на секретные таблицы
  (хвост I-2, ревью W2: правка §5.5 отвергнута — снимала бы К2); индексы очереди и истории; CHECK
  `status_before_deletion` с `banned` (R11). Номер — следующий свободный на момент Task 1; план I-3 берёт номер после.
- **R3. Правила флагов — в контракте** (`FeatureFlag.rules {global_only, requires}`, admin 0.4.0): единственный
  источник правил — `flags.RuleOf`; админка не держит копию (иначе два списка разойдутся). Поле вне среза identity —
  заморозку не трогает. Связь «аварийный флаг → способ» статистики — константа админки (`LOGIN_FLAG_METHOD`) со
  стражем по `FeatureFlagKey`: это знание экрана, не правило флага.
- **R4. Порты на этапе сборки, не env.** Прод (`!wf_e2e`) — `UnavailableCheck` по обоим именам; `wf_e2e` —
  `identitytest.AllowAll()`; набор проверяет `ValidateCaptainChecks` при старте. Переменная окружения отвергнута:
  ошибка конфигурации стенда или прода открыла бы капитанство без проверки (К1 — «честно, а не в обход правила»).
- **R5. Одно правило условий** (`evaluateCaptaincy`) для экрана игрока, подачи, карточки и одобрения. Порядок
  `reasons` — `ReasonOrder`; ответ подачи — первая ошибка по порядку «причины → открытая заявка → пауза». Пауза 7 дней
  — **по пользователю** (последний отказ в любом городе): спека говорит «после отказа» без города; по городу её обходила
  бы смена города (решение пользователя Q4). Открытая заявка даёт `eligible = false` без причины (клиент показывает заявку).
- **R6. Одобрение под ролью `admin` без замка `users`.** Замок — строка заявки `FOR UPDATE`; `FOR KEY SHARE` от FK
  `role_assignments → users` совместим с `FOR NO KEY UPDATE` у `lockUser` — взаимоблокировки с удалением нет.
  Заявитель в `pending_deletion` → `409 application_closed` (заявку закрывает удаление). Гонку «одобрение и
  одновременное ограничение `captain`» замок не закрывает (роль `admin` не может держать `users`) — допуск снимает
  `moderation` через `RevokeRoleInTx` (передача).
- **R7. Порядок замков** (дополнение R30 плана I-2; m1 ревью — по коду `deleteAccount`, `deletion.go:45–77`, сверено
  16.11): `users` → `sessions` (→ `refresh_tokens` → `devices` → `user_mfa`/`user_recovery_codes`, как R30 I-2) →
  `captain_applications` → `role_assignments` → `mfa_resets` → `auth_codes`/`auth_challenges`. Админские сценарии
  заявок начинают с `captain_applications` (без `users`), затем `staff_accounts` сотрудника `FOR SHARE` (R38), затем
  `role_assignments`.
- **R8. Смена города отзывает открытую заявку** (`ChangeCityInTx`; подтверждено решением пользователя Q6, детали —
  R39): город заявки — текущий город игрока (§3.12); заявка прежнего города без игрока в нём не имеет смысла.
  Допуски прежнего города не трогаются — судьба команды — спека `teams`.
- **R9. Очередь заявок.** `pending` — старые сверху (контракт), прочие статусы — по времени решения, новые сверху.
  Заявки `deleted`-аккаунтов не показываются нигде (ПД затёрты; карточка — 404). Модератор чужого города — `404`, как
  несуществующая; фильтр чужого города — пустая страница. История в карточке — все прошлые заявки игрока, без
  предела (m11: контракт предела не задаёт, пауза 7 дней ограничивает их число).
- **R10. Аудит ПД.** Контакты заявителя и найденный игрок — `identity.pii_read` (`audit.WriteRead`, объект `user`,
  причина — `Entry.Reason`); поиск без результата — `identity.player_lookup` без `object_id` (`ObjectType: user`, `After {found: false}` — как
  `auditAnonymous`, m2):
  попытки перебора почт видны в журнале. Почты в записи нет никогда. Модератору «нет» и «чужой город» неотличимы.
- **R11. Бан поверх удаления** (§17; принят решением пользователя Q1 16.11). Удаление сильнее по статусу: `status` остаётся `pending_deletion`,
  `purge_at` не меняется (право на удаление не отменяется санкцией), бан пишется в `status_before_deletion = banned` и
  строку `login`. Вход в период бана — `403 account_blocked` (не `deletion_pending`: восстановление закрыто, пока бан
  жив); бан истёк — `status_before_deletion → active`, вход снова даёт `deletion_pending`. Тем самым
  `status_before_deletion` всегда актуален (§17 п. 3).
- **R12. Истечение бана возвращает статус** — в чистке и в `SetRestrictionsInTx` одной функцией: снята последняя
  живая строка `login` → `banned → active`, `version + 1`, событие `restrictions_changed`, аудит `identity.ban_expired`
  (`system` в чистке).
- **R13. `SetRestrictionsInTx` проверен под ролью `api`.** Вызывающего (`moderation`) ещё нет; у `admin` нет
  `UPDATE` на `users`/`sessions` (К2), поэтому роль исполнения санкций — решение спеки `moderation` (задача воркера по
  событию или правка §5.5); тест `TestSetRestrictionsUnderAdminRole` фиксирует границу.
- **R14. Связь меняет версию учётки сотрудника.** PUT и DELETE связи — изменение учётки (`StaffAccount.player_link`):
  `staff_accounts.version + 1`. Следствие — `InitiatorCheck` отсекает запрос сброса 2FA, созданный до связи (хвост I-2);
  побочно отсекаются все ожидающие запросы этого сотрудника (`initiator_changed`) — принято: операция владельца
  редкая, а перепроверка связи инициатора в исполнителе identity потребовала бы читать `staff_*` из identity.
- **R15. Ник для связи — порт `staff.PlayerDirectory`** (staff — слой 0, identity не импортирует); адаптер над
  `identity.Service.User` в `cmd/admin-api`; `deleted` → `422 staff.player_not_found`, `pending_deletion` — можно.
- **R16. Заглушки admin — по файлам областей** (`pending_{captains,players,staff_link}_test.go`), как R5 плана I-2:
  три параллельные задачи не правят один литерал.
- **R17. Ключи писем I-4 заводит Task 2.** Стража неиспользуемых ключей у `backend/locales` нет — ключи заранее
  снимают конфликт файла в волне 2 (письма identity — Task 4, письма staff — Task 6).
- **R18. Фронт — один исполнитель.** pnpm — один процесс на машину; локали, меню, маршруты — общие файлы.
- **R19. ПД во фронте — только в памяти экрана.** Почта поиска, причина доступа, контакты и результат поиска — не в
  адресе, storage, черновиках, «Недавнем», кэше запросов (`ONE_SHOT`, `gcTime: 0`).
- **R20. Раздел «Игроки»** (`/players`, право `identity.pii.read`; группа меню «Игроки» вместе с «Капитанами»): в
  I-4 — поиск и сброс 2FA; `moderation` расширит раздел карточкой игрока.
- **R21. Карточка заявки — панель в адресе** (`?application=<id>`), как карточка сотрудника; макет 07 — правая
  панель «Решение по заявке».
- **R22. «Одобрить» недоступна, пока хоть одно условие не `ok`**; сервер перепроверяет в момент решения.
- **R23. Живой e2e — в харнессе `admin:e2e`** (Mailpit, воркер, база `wf_e2e` уже есть): плюс `api` с тегом
  `wf_e2e`, сценарии API — `APIRequestContext` Playwright. Отдельный харнесс для API отвергнут: дубль инфраструктуры.
- **R24. Удаление и анонимизация публикуют события заявок и допусков** (`captain_application_withdrawn`,
  `captain_permit_revoked`): в I-2 этих событий не было в каталоге; `teams` будет на них опираться.
- **R25. Допуск капитана всегда с заявкой** (`application_id`): `RevokeRoleInTx` на допуске без заявки — ошибка
  инварианта (такой строки после `0025` быть не может; агрегат события — заявка).
- **R26. `contact_link` — схема `https` приводится к нижнему регистру** до записи (хвост I-2; CHECK принимает любой
  регистр).
- **R27. Новые действия не «только с компьютера»**: решения по заявкам, запрос и отмена сброса 2FA, связь сотрудника —
  не выдача прав; реестр `DESKTOP_ONLY_PERMISSIONS` не меняется.
- **R28. Сводка подтверждения `identity.mfa_reset`** — «Игрок <id>», обращение, причина из payload: операции «игрок по
  id» в контракте нет, а подтверждающему нужна причина первого сотрудника.
- **R36. Один предикат «заблокирован»** (I2 ревью, следствие R11): `status = banned` ИЛИ (`pending_deletion` ∧
  `status_before_deletion = banned` ∧ живая строка `login`). Одна функция `a.blocked` над одним запросом
  `UserBlocked` (`captain_rules.sql`) — заводит Task 2 (N1 повторного ревью); зовут `evaluateCaptaincy` (→
  `restricted`, I6) и Task 7: `finishLogin`, `CancelDeletion`, `password.go` (`resetBlocked` и приманка
  `startPasswordReset`), `mfa_login.go`; Composer (`identity/mail`) — тот же запрос через `identitydb`. Иначе
  забаненный в период удаления сбросил бы пароль или отменил удаление.
- **R37. Скоуп модератора — по `auth.GrantScoped`, не по `len(st.CityIDs)`** (I4 ревью; образец
  `geo/admin/read.go:19,53`, `httpapi/admin/server.go:288`): модератору без городов — пустой не-nil срез, проверка
  длины открыла бы ему все города. Очередь, карточка, контакты, решения, поиск игрока. Task 4, 5.
- **R38. `self_action` — по связи, перечитанной в транзакции решения** (непредусмотренный отказ 1 ревью): порт
  `identity.StaffLinks` (`AdminDeps`), реализация `staff.LinkedPlayerTx` (`FOR SHARE` строки сотрудника — сериализует
  с `PutPlayerLink`/`DeletePlayerLink`, держащими `FOR UPDATE`), адаптер в `cmd/admin-api`; `auth.Staff.LinkedUserID`
  загрузки сессии для решения не используется. Это межмодульная транзакция (код staff в транзакции identity) вне
  списка спеки бэкенда §4.4 — исключение по прецеденту хука `LockActors` подтверждений; Task 10 записывает его в
  спеку бэкенда §4.4 и спеку identity §0.2 (n2). `requestMfaReset`/`cancelMfaReset` по-прежнему берут `self_action`
  из `st.LinkedUserID` — принято: запрос перепроверяется при подтверждении (R14), отмена безвредна (n3). Task 2
  (порт, реализация), Task 4 (применение и гонка с настоящим `PutPlayerLink`).
- **R39. Отзыв заявки сменой города** (решение пользователя Q6, вариант (а)): `withdrawn` с событием; паузы 7 дней
  нет (пауза — только от `rejected`); письмо игроку `identity.captain_application_withdrawn_city` («заявка отозвана
  из-за смены города — подайте заново в новом городе», ключи ru/en — Task 2); допуск прежнего города не трогается
  (решает `teams`). Тест на отсутствие паузы — Task 7.
- **R40. «Один живой код» смены почты — по `(purpose, user_id, email)`** (решение пользователя Q5): чужой живой код на
  тот же адрес больше не гасится; подтверждение выбирает одного уникальностью `users.email`. Индекс (`0027`),
  `startCode`/`RevokeLiveCode` и `ON CONFLICT` — одной задачей (Task 1, m9): иначе вставка теряет арбитр.
- **R41. Причина доступа к ПД неизменяема** (непредусмотренный отказ 2 ревью; решение контроллера): `audit_log.reason`
  не затирается анонимизацией — журнал доступа неизменяем, это его назначение. Защита — у источника: в UI под полем
  причины подсказка «не указывайте персональные данные игрока» (контакты, поиск, связь — Task 8, 9). Записать в
  спеку §0.2 (Task 10).
- **R42. Живая строка `login` ⇔ заблокирован** (m10 ревью): `SetRestrictionsInTx` отказывает `ErrLoginNeedsBan`, если
  после применения есть живая строка `login`, а статус (у `pending_deletion` — `status_before_deletion`) не
  `banned`; строка `login` добавляется только вместе с баном. Иначе R12 (снятие последней `login` → `active`)
  «разбанивал» бы незабаненного, а предикат R36 расходился бы со статусом. Task 7.

## Решения пользователя

- **16.11.2026: I-4 идёт раньше I-3.** I-3 (соцвходы, passkeys, аттестация) — при подготовке к выпуску в сторы; его
  план пишется отдельно (`2026-11-16-identity-3-devices-social-passkeys.md`). Следствия — R1, «Передача».
- **16.11.2026, ответы на Q1–Q6 плана:**
  - **Q1** — R11 принят: бан поверх удаления не прерывает удаление (`purge_at` не меняется, бан — в
    `status_before_deletion`); пока бан жив, отменить удаление нельзя. Следствия — R36, R42.
  - **Q2** — оба среза (public и admin) заморожены до последней задачи I-3 (R1); Task 12 `identity-freeze.test.mjs` не
    трогает.
  - **Q3** — на стенде капитанство отвечает `checks_unavailable` до модулей `players`/`teams`; отдельной тестовой
    сборки стенда нет (тег `wf_e2e` — только e2e).
  - **Q4** — пауза 7 дней по игроку (последний отказ в любом городе, R5).
  - **Q5** — «один живой код» для `email_change` по `(purpose, user_id, email)` (R40, Task 1).
  - **Q6** — вариант (а): смена города отзывает открытую заявку (`withdrawn`), паузы после такого отзыва нет, письмо
    игроку, допуск прежнего города не трогается (R8, R39, Task 7).
- **Решение контроллера 16.11** (непредусмотренный отказ 2 ревью): причина доступа к ПД в `audit_log.reason`
  остаётся неизменяемой и переживает анонимизацию; в UI — подсказка (R41).
- Унаследовано: К1–К7, В1–В9 (спека §0); решения плана I-2 (R5, R7, R8, R20–R22, R29, R30) и пользователя 15–16.11
  (спека §0.1).

## Открытые вопросы к пользователю

Открытых вопросов нет (Q1–Q6 решены 16.11 — «Решения пользователя»). Не проверено ревьюером плана и сверяется
исполнителем первым шагом: окружение `api` прогона e2e (Task 11, «Окружение `api` прогона»); генерация nullable
`$ref` `requires` (Task 1 — образец `Approval.decided_by` сверен, итог — в отчёт Task 1).

## Передача следующим планам

| Хвост | Куда |
| --- | --- |
| 14 операций public I-3 (`pendingI3`), удаление `TestPendingOpsExpire` и `pendingOpsAtFreeze` public, решение о `identity-freeze` (удалить или до `contracts-v1.0.0`) | последняя задача I-3 (R1) |
| `getAuthMethodsUsage` начнёт считать ненулевое — привязки и ключи I-3; соцвход и вход ключом проходят `finishLogin` — правило R11 (бан в период удаления) действует без правок | I-3 |
| Номер миграции I-3 — после `0027` | I-3 |
| План I-3 (черновик 16.11) написан в порядке «I-3 раньше I-4»: после него в `pendingOps` «4 public I-4 и 9 admin», `pendingOps = Concat(…, pendingI4)`, `TestCoreOpsNotStub` = 46 + 4/9; `getAuthMethodsUsage` и живой e2e соцвходов и ключей переданы «в I-4». При порядке I-4 → I-3 план I-3 приводится к R1: после I-4 `pendingOps` public = `pendingI3`, admin-списка нет; представление usage уже есть (`0027`); e2e соцвходов и ключей (харнесс Task 11, тег `wf_e2e`) — в самом I-3 | контроллер при ревью плана I-3 |
| Роль исполнения `SetRestrictionsInTx` (у `admin` нет `UPDATE users/sessions`, R13); снятие допуска при ограничении `captain` (`RevokeRoleInTx`, R6); U5 для своих решений (`auth.Staff.LinkedUserID`) | спека `moderation` |
| Порт `teams.city_captaincy`, вызовы `RevokeRoleInTx`, `ChangeCityInTx`, снятие испытательного срока, события заявок и допусков | спека `teams` |
| Порт `players.profile_complete` (чтение своих представлений под ролями `api` и `admin`) | спека `players` |
| Экран «Стать капитаном» на вебе (BFF) | спека веба |
| Ответ нативщику (`.superpowers/sdd/<сессия>/native-answer-i4.md` → документ пользователя) | пользователь |

## Сверка хвостов плана I-2 («Передача следующим планам», строки 1828–1837) и спеки §17

| Хвост | Задача I-4 |
| --- | --- |
| 4 операции капитанства public, 9 операций admin | 3; 4, 5, 6 |
| Порты `CaptainCheck`, тег `wf_e2e` | 2 (сборка), 3–4 (применение), 11 (e2e) |
| `intx` (`RevokeRoleInTx`, `SetRestrictionsInTx`, `ChangeCityInTx`), `Service.CaptainPermits` | 7 |
| Письма и события заявок | 2 (каталог, ключи), 3, 4, 7 (R24) |
| Ручки связи «сотрудник → игрок», ник через порт identity | 6 (R15) |
| Декодирование заголовков `X-WF-*` | 2 (`headers.go`), 4, 5 |
| Экраны админки (капитаны, сброс 2FA, связь), флаги в админке, тексты причин | 8, 9; 1 (`rules`) |
| Стенд: живой e2e identity (ключи Apple/Google/WebAuthn — I-3) | 11, 13 |
| Удаление `pendingOps`, `TestPendingOpsExpire`, решение о `identity-freeze` | 12 (R1: admin — удаление; public и заморозка — до I-3) |
| Хвосты §16: спека бэкенда §4.4, `docs/05` (U5), ответ нативщику | 10 |
| `getAuthMethodsUsage` без прав `admin` на секреты | 1 (R2), 5 |
| Валидация `contact_link` | 2 (`NormalizeContactLink`), 3 (R26) |
| Бан = ограничение `login`; `SetRestrictionsInTx` пишет строку `login` | 7 (`ErrBanNeedsLogin`) |
| Чужой живой код смены почты | 1 (R40, Q5) |
| §17: истечение бана возвращает статус | 7 (R12) |
| §17: бан поверх `pending_deletion` | 1, 7 (R11, R36, R42; Q1 решён) |
| §17: `status_before_deletion` устаревает | 7 (R11) |
| §17: связь инициатора сброса 2FA | 6 (R14) |
| §17: статус в `requireFresh` и `deleteAccount` | 7 |

# Приложения

## Приложение 1. Операции по задачам и файлам-владельцам

| Задача | Файл заглушек | Операции |
| --- | --- | --- |
| 3 | `pending_i4_test.go` (удаляется), `identity/httpapi/pending_i4.go` → `captaincy.go` | `getMyCaptaincy`, `listMyCaptainApplications`, `submitCaptainApplication`, `withdrawCaptainApplication` |
| 4 | `admin/pending_captains_test.go`, `identity/admin/captains.go` | `listCaptainApplications`, `getCaptainApplication`, `getCaptainApplicationContact`, `approveCaptainApplication`, `rejectCaptainApplication` |
| 5 | `admin/pending_players_test.go`, `identity/admin/players.go` | `findPlayerByEmail`, `getAuthMethodsUsage` |
| 6 | `admin/pending_staff_link_test.go`, `staff/admin/accounts.go` | `putStaffPlayerLink`, `deleteStaffPlayerLink` |
| I-3 | `pending_i3_test.go`, `identity/httpapi/pending_i3.go` | 14 операций (план I-2, «Передача») |

Итого I-4: 4 public, 9 admin. Анонимных мутаций нет. Мутации с `Idempotency-Key`: `submitCaptainApplication`,
`withdrawCaptainApplication`, `approveCaptainApplication`, `rejectCaptainApplication`, `putStaffPlayerLink`,
`deleteStaffPlayerLink`.

## Приложение 2. Письма, события, аудит I-4

- **Письма:** `identity.captain_application_withdrawn_city` (Task 7, R39; очередь `mail`); `identity.captain_application_approved`, `identity.captain_application_rejected` (Task 4; очередь
  `mail`, исполняет воркер); `staff.player_linked`, `staff.player_unlinked` (Task 6; очередь `staff`, исполняет
  admin-api).
- **События** (агрегат `captain_application`, кроме `city_changed` — `user`): `captain_application_submitted` (3),
  `captain_application_withdrawn` (3, 7), `captain_application_decided` (4), `captain_permit_granted` (4),
  `captain_permit_revoked` (7), `city_changed` (7), `restrictions_changed` и `sessions_revoked` (7, уже в каталоге).
- **Аудит:** `identity.captain_application_submitted|withdrawn` (игрок), `identity.captain_application_approved|rejected`
  (сотрудник, `scope_city_id`, код причины), `identity.pii_read`, `identity.player_lookup` (сотрудник, причина),
  `identity.ban_expired` (`system`), `identity.city_changed`; `staff.player_linked|unlinked` (владелец).

## Приложение 3. Ключи локалей админки (заводит Task 8, кроме помеченных «9»)

- `nav.groups.players`, `nav.captains`, `nav.counts.captains`, `nav.players` (9);
- `captains.*`: `title`, `subtitle`, `tabs.<статус>`, `table.{nickname,city,age,account_age,submitted,decided,decided_by}`,
  `card.{form,phone_given,phone_missing,checks,history,linked_accounts,contacts}`, `checks.{age,restrictions,profile,captaincy}`,
  `check_state.{ok,failed,unavailable}`, `unavailable_hint`, `approve.{title,comment,submit,disabled_hint}`,
  `reject.{title,code,comment,submit}`, `rejection.<код>` (6), `reasons.<причина>` (6), `contact.{title,reason,reason_hint,show,email,phone}`,
  `toasts.{approved,rejected,closed}`, `empty`;
- `players.*` (9): `title`, `subtitle`, `lookup.{email,reason,reason_hint,submit,not_found}`, `card.{status,mfa_on,mfa_off,reset_at}`,
  `status.<статус>`, `reset.{request,reason,ticket,cancel,cancel_confirm}`;
- `staff.player_link.*` (9): `title`, `none`, `link`, `unlink`, `confirm`, `linked_by`;
- `flags.global_only`, `flags.requires`, `flags.required_by`, `flags.emergency.{title,text,count,retry,confirm}` (9);
- `approvals.summary.identity.{player,ticket,reason}` (9);
- `audit.tabs.players`, `audit.actions.identity.*` (все действия `app/audit.go`), `audit.objects.{user,captain_application,mfa_reset}`;
- `audit.actions.staff.player_linked`, `audit.actions.staff.player_unlinked` (m6 ревью: действия Task 6 в журнале —
  подписи заводит Task 8 вместе с прочими ключами журнала);
- `home.attention.captains.*`.

## Приложение 4. Макеты: что есть, что дорисовать

| Экран | Макет | Что делаем |
| --- | --- | --- |
| Заявки капитанов | `07-Админка.dc.html` (`captains`: список ИГРОК · ГОРОД · МАТЧЕЙ · НАДЁЖН. · ПОДАНА, поиск, чипы, панель «Решение по заявке»: галочки, чипы причины, «Одобрить», «Отклонить с причиной», «Запросить видеозвонок»), `08-Админка-в-телефоне.dc.html` (карточка «Заявка капитана») | раскладка и панель — по макету; отличия по контракту: вместо «Матчей» и «Надёжн.» — возраст и стаж (данных `players`/`stats` нет); «Телефон подтверждён» → «указан / нет» (телефон не подтверждается); «Возраст 18+» → порог страны; причины — шесть кодов §7.3 вместо четырёх; «Подать снова через 30 дней» → 7 дней; «Запросить видеозвонок» и массовые «Одобрить все»/«Отклонить с одной причиной» — нет в контракте, не делаем; «Просрочены» — нет (срока в спеке нет) |
| Игроки → Сброс 2FA | нет (в 07 есть «Пользователи и санкции» — раздел `moderation`) | дорисовать по UI-фундаменту: форма поиска, карточка, диалоги |
| Связь «сотрудник → игрок» | нет | блок в панели сотрудника по образцу блоков `StaffDrawer` |
| Флаги: только «везде», аварийный диалог | матрица в 07 есть | дорисовать строку без городов и диалог |
| Подтверждение сброса 2FA | раздел «Подтверждения» дорисован ранее | строка сводки |
