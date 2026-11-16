# Identity I-3: устройства, соцвходы, ключи доступа — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

> **Формат — бриф задач, без кода реализации** (решение пользователя 13.10.2026, память `plan-format-brief`). Каждая
> задача: цель, риск, файлы, точные интерфейсы (сигнатуры Go, таблицы и колонки, operationId), тесты (имя → вход →
> ожидаемое), подсадка, приёмка, ловушки, «Не трогай». Как писать SQL и Go — решает исполнитель по TDD, спеке и
> `.claude/rules/`. Авторитет: спека → «Решения плана» (R, явно отступающее от спеки, действует до правки спеки в
> Task 14) → тексты задач. Где задача расходится со спекой без ссылки на R — права спека, задача — ошибка плана
> (сообщить контроллеру). Решения плана I-2 цитируются как «I-2 R30» и т. п.

**Goal:** этапы 6–8 спеки identity (§15) на замороженном контракте I-1 — 14 операций public из `pendingOps` I-3
(`backend/internal/identity/httpapi/pending_i3.go`) и ветки I-3 в операциях I-2:
- **устройства:** задача аттестации `createDeviceChallenge`, App Attest (аттестация и утверждения со строго растущим
  счётчиком), Play Integrity, Apple DeviceCheck (счётчик регистраций iOS), режим `API_IDENTITY_ATTEST_MODE`
  (`off | observe | enforce`), письмо о входе с нового устройства, событие `identity.device_shared`, связанные
  аккаунты (представление `identity_read_linked_accounts` уже есть — I-2);
- **соцвходы:** nonce, проверка `id_token` Apple и Google (`go-oidc`), вход привязанных, автопривязка без обхода 2FA и
  без pre-account takeover, дорегистрация, привязка и отвязка в настройках, обмен `authorization_code` и отзыв
  refresh-токена Apple (при отвязке и анонимизации), свежий вход соцвходом;
- **ключи доступа:** добавление (resident key, UV обязательна, аттестация `none`), переименование, удаление, вход без
  пароля (ключ закрывает 2FA), ключ как второй фактор (`startMfaPasskey` + `completeMfa`), свежий вход ключом;
- **платформа и стык:** конфиг и env §11.3 (Apple, Google, WebAuthn, App Attest, Play Integrity, режим аттестации; у
  воркера — ключ Apple для отзыва и DeviceCheck), порты внешних провайдеров с реальной `ProviderReadiness` вместо
  `NoProviders`, очередь вызовов провайдеров `provider_calls` (R9), письма и события I-3.
- В `pendingOps` после I-3 остаются только 4 public операции капитанства (I-4) и 9 admin (I-4).

Не здесь: капитанство, admin-ручки и экраны (в том числе флаги способов входа в админке и `getAuthMethodsUsage`),
связь «сотрудник → игрок» в контракте, живой e2e identity — план I-4; веб (BFF, Services ID Apple с `redirect_uri`,
перенос `.well-known` в веб) — спека веба.

**Architecture:**
- **Внешние провайдеры — за портами корня `identity`** (R2): `SocialVerifier`, `AppleTokens`, `Attestor`,
  `DeviceChecker`, `*PasskeyRP` (псевдоним обёртки `go-webauthn`) и `ProviderReadiness`. Реализации — листовые пакеты
  `identity/internal/{applekey,social,webauthn,attest}` (не импортируют корень и `app`); сборка — один конструктор
  корня `identity.NewProviders` (api) и `identity.NewWorkerProviders` (воркер) из конфигурации; тесты —
  `identity/identitytest` (свой JWKS и подписант `id_token`, виртуальный аутентификатор WebAuthn, цепочка App Attest на
  тестовом корне, подделки Play Integrity и DeviceCheck на `httptest`).
- **Сеть — вне транзакций.** Проверка `id_token` (JWKS с кэшем), обмен кода Apple, Play Integrity, DeviceCheck — до
  транзакции сценария; в транзакции — только сверка и запись. Одноразовые строки (nonce соцвхода, задача
  аттестации) гасятся короткой отдельной транзакцией до сценария (R6, R10).
- **Ядро входа — то же** (I-2 R6): новые способы входа идут через `finishLogin` (`method = apple|google|passkey`,
  `mfaDone = true` у ключа); соцвход не обходит 2FA, автопривязка при 2FA откладывается до второго фактора (данные — в
  `auth_challenges.data_enc` шага `mfa`, R16).
- **Исходящие вызовы с повтором** (отзыв Apple, обновление DeviceCheck) — строка `provider_calls` с шифротекстом
  кольца + задача River `identity.provider_call {v, ref}` в транзакции действия; исполняет воркер (R9, R28).
- **Аттестация — сценарий `app.deviceTrust`**, а не порт-«вердикт»: задача, ключ App Attest и счётчик живут в
  `auth_challenges` и `devices`; порт `Attestor` проверяет только криптографию и ответ Google (R10).

**Tech Stack:** Go 1.27 (chi, pgx v5, sqlc, goose, River), новые зависимости — `github.com/go-webauthn/webauthn`
v0.18.2 (19.09.2026), `github.com/coreos/go-oidc/v3` v3.21.0 (01.09.2026), `github.com/fxamacker/cbor/v2` v2.10.0
(28.10.2026) — проверено `go list -m -versions` и `go list -m -json` 16.11.2026: это последние версии, все старше двух
недель. `google.golang.org/api` не берётся (R22). `github.com/golang-jwt/jwt/v5` v5.3.1 — уже в `go.mod`.

**Spec:** `docs/superpowers/specs/2026-11-13-identity-design.md` (дальше «спека»): §0 и §0.1, §3.3 (второй фактор
ключом), §3.5, §3.6, §3.7 (письмо о новом устройстве), §3.8, §3.13 (свежий вход ключом и соцвходом), §4, §5.3–§5.5,
§5.10, §6.2–§6.5, §8, §9.3–§9.5, §11, §12, §13, §15 (этапы 6–8), §16, §18. Контракт — замороженный срез I-1
(`contracts/openapi/public/identity/{paths,schemas}.yaml`, страж `contracts/test/identity-freeze.test.mjs`). План I-2 —
`docs/superpowers/plans/2026-11-13-identity-2-core.md` («Решения плана» R1–R31, «Передача следующим планам»). Правило
модуля — `.claude/rules/backend-identity.md`. Спека встроенного воркера —
`docs/superpowers/specs/2026-11-16-embedded-worker-design.md` (`WORKER_*` у api на стенде).

**Предусловие старта:** ветка `spec/identity-3` от `origin/main` **после слияния** ветки `spec/embedded-worker`
(она правит `backend/cmd/**`, `backend/internal/platform/{queue,config}/**`, `render.yaml`, `deploy/server/**` — те же
файлы, что задачи 1 и 7 этого плана). Номер миграции — следующий свободный в момент Task 5 (сейчас последняя —
`0026_identity_deletion.sql`, R31). До старта — Шаг 0 контроллера (зависимости, «Порядок исполнения»).

## Global Constraints

- **Правила репо** — `CLAUDE.md`, `.claude/rules/{backend,backend-identity,backend-schema,backend-platform,backend-http,env,contracts}.md`.
  TDD: сначала падающий тест; каждый новый страж проверяется подсадкой бага (`node scripts/plant.mjs …`, в отчёт —
  что подсажено и что упало).
- **Риск задачи** — A/B/C по `.claude/agents/wf-implementer.md`: для A исполнитель до первого теста пишет таблицу
  режимов отказа (гонки, повторы, права в момент действия, частичный отказ, время, перечисление аккаунтов, недоступный
  провайдер), ревьюер проходит 10 категорий нестандартных условий.
- **Контракт заморожен** (I-1, R17): YAML identity не правится, новых кодов ошибок нет — все коды I-3 уже в
  `identity/codes.go` и локалях; общий `feature.disabled` (`httpx.CodeFeatureDisabled`, 403) — платформа. Расхождение
  контракта и сценария — стоп и вопрос контроллеру. Единственная возможная правка заморозки — Q6 («правка заморозки —
  решение пользователя»); без ответа пользователя план её не делает.
- **Заглушки:** реализованная операция в той же задаче уходит из `pending_<область>_test.go`; `TestNoStubOperations` и
  `TestPendingOpsExpire` зелёные после каждой задачи; `pendingOpsAtFreeze` и его хэш не меняются.
- **Коммиты** — делает контроллер (субагенты не коммитят): пути и сообщение — в разделе «Коммит» отчёта; одна строка
  по-русски без тела; `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A/.`, `commit -a`, `stash`,
  `reset`, `clean`, `checkout -- .`. Доки нативщика `docs/WF_…_2026_10_19.md` и `docs/screens/` — не коммитить.
- **Код:** комментарии по-русски, идентификаторы по-английски, LF. Время — только `clock.Clock` (тесты —
  `clocktest.Fake`), в том числе в `go-oidc` (`Config.Now`) и в проверках App Attest/Play Integrity; у `go-webauthn`
  срок сессии обнуляется (R5). id — UUIDv7 (`id.New`).
- **Секреты и ПД** (спека §1.4, §9.6): `id_token`, `authorization_code`, refresh-токен Apple, токен DeviceCheck, токен
  Play Integrity, сырой nonce, `signup_token`, утверждения WebAuthn, `install_id`, почта провайдера — не в логах,
  событиях, аргументах задач, `meta` журнала и `Before/After` аудита. Долгоживущее (refresh Apple) — `keys.Ring.Seal`
  с aad = id строки; токены шагов — SHA-256; nonce — SHA-256 (R6). Ответы с nonce, задачами WebAuthn и токенами —
  `Cache-Control: no-store` (контракт).
- **Перечисление аккаунтов** (спека §9.1): неизвестный ключ доступа и неверная подпись — один ответ
  `400 identity.passkey_invalid`; `signup_required` соцвхода видит только владелец учётки провайдера с подтверждённой
  почтой; автопривязка не раскрывает аккаунт до успешного входа.
- **Транзакции и замки:** порядок I-2 R30 + дополнение R18; внутри `db.InTx` лимит — только `a.limiterTx(tx)`;
  неудачи (попытки токенов, гашение одноразовых задач, счётчики) коммитятся до ответа ошибкой (`inTxCommitFailure`,
  `failureToCommit`). Ручка с `Idempotency-Key` — ровно одна закоммиченная `db.InTx` сценария (отдельная короткая tx
  гашения nonce или задачи — до неё и идемпотентности не касается: повтор с тем же ключом получает сохранённый ответ
  раньше сценария).
- **Окружение:** новый env-ключ — сразу в `platform/config`, `backend/.env.example`, `deploy/server/*.env.example`,
  локальный `backend/.env` (скриптом из scratchpad, значения не выводить) и `render.yaml` (Task 1;
  `.claude/rules/env.md`).
- **Тесты:** исполнитель гоняет тесты своих пакетов (`go test -p 4 ./internal/identity/... ./internal/httpapi/public/...`
  и т. п. из `backend/`; Postgres — `bash scripts/pg.sh start` в фоне) и typecheck своего пакета (`go vet`); полный
  `./task backend:test` и lint — контроллер в конце пачки. Тесты с базой — параллельные (`t.Parallel()`, `dbtest`),
  проверки прав — под ролью прода. Внешние провайдеры в тестах — только `identitytest` на `httptest`, без сети.
- **Кодогенерация:** `sqlc generate` (из `backend/`) — исполнители сами; pnpm в I-3 не нужен (контракт и локали
  фронтов не меняются); `./task gen` — только контроллер перед ci.
- **Строки:** тексты писем — `backend/locales/{ru,en}.json` (`identity.mail.<письмо>.{subject,text…}`, `en` с теми же
  ключами). Названия провайдеров (Apple, Google), моделей устройств и браузеров — имена собственные, не UI-строки.

## Review Focus

1. **`id_token` и nonce.** Подпись по JWKS провайдера, `iss` строго (Google — две формы), `aud` — только наши client
   id, `exp` по `clock`, `alg` только RS256/ES256 из JWKS (не `none`, не HS*), nonce одноразовый, с сервера, сверка
   Apple — hex SHA-256, Google — как есть; сырой nonce в базе не хранится (R6). Task 2, 10.
2. **Соцвход не обходит 2FA и не захватывает аккаунт.** Ветки §3.5 таблицей (`domain.DecideSocial`): автопривязка
   только при `email_verified = true`, не relay Apple, привязка — вместе с успешным входом и после второго фактора;
   `email_verified ≠ true` и нет почты — 422; дорегистрация не создаёт аккаунт на занятую почту. Task 6, 8, 10.
3. **WebAuthn.** Задача одноразовая, 5 минут по `clock`, привязана к цели, аккаунту и где есть — к сессии и
   `mfa_token`; `origin` из списка (Android — `RPOpaqueOrigins`, R4), `rpIdHash`, UP и **UV**, подпись, ключ принадлежит
   аккаунту (на шаге 2FA и свежем входе — только его ключи, `allowCredentials`), `userHandle` = `users.webauthn_handle`;
   клон (счётчик уменьшился у ненулевого) — вход проходит, журнал. Task 3, 12, 13.
4. **Последний способ входа и свежий вход.** Отвязка и удаление ключа — свежий вход, проверка «останется ≥ 1 способ»
   под `lockUser` (две параллельные отвязки разных способов не оставляют аккаунт без входа). Task 11, 12, 14.
5. **Флаги.** Матрица R17: основной флаг выключен — привязанные входят, новые регистрации и привязки 403; аварийный
   выключен — вход этим способом 403 всем (вход ключом, второй фактор ключом, свежий вход ключом и соцвходом); отвязка и
   удаление — при любых флагах; провайдер без конфигурации у api ≡ выключен. Task 8, 10–14.
6. **Аттестация.** Задача одноразовая (условное гашение), App Attest: цепочка до корня Apple, nonce, `rpIdHash` App ID,
   `aaguid` по среде, `credentialId`; утверждение — подпись и строго растущий счётчик под замком строки устройства;
   Play Integrity — `requestHash`, пакет, `PLAY_RECOGNIZED`, `MEETS_DEVICE_INTEGRITY`, свежесть ≤ 5 мин; `enforce`
   отказывает только на отрицательном вердикте и отсутствии данных, недоступность провайдера — PoW в любом режиме.
   Task 4, 9.
7. **Секреты.** refresh Apple — `{client_id, refresh_token}` под кольцом (R8), при отвязке и анонимизации переносится
   в `provider_calls` (R9), не теряется и не попадает в аргументы задачи; права ролей на `provider_calls` — ровно R9;
   ни одного токена провайдера в логах и журнале. Task 5, 7, 9, 11.
8. **Замки и гонки.** Порядок R18; nonce и задача аттестации — отдельной короткой tx; одна задача WebAuthn — один
   успех при параллельных предъявлениях; автопривязка при гонке привязок не роняет транзакцию 23505 (`ON CONFLICT DO
   NOTHING`). Task 8–13.
9. **Новое устройство и общее устройство.** Письмо `new_device` — вход с устройства, которого нет в `device_accounts`
   аккаунта, кроме первой регистрации и веба без метки; `device_shared` — > 3 аккаунтов за 30 дней. Task 8.

---

## File Structure

| Путь | Ответственность | Задача |
| --- | --- | --- |
| `backend/go.mod`, `backend/go.sum`, `docs/versions.md` | зависимости I-3 | Шаг 0 (контроллер) |
| `backend/internal/platform/config/{config.go,config_test.go,render_stand_test.go}`, `backend/.env.example`, `backend/.env` (скриптом), `deploy/server/{api,worker}.env.example`, `render.yaml`, `docs/deploy-dev.md`, `.claude/rules/env.md` | env §11.3 для I-3 | 1 |
| `backend/internal/identity/internal/{applekey,social}/*`, `backend/internal/identity/identitytest/{social.go,apple.go}` (+ тесты) | ключ Apple, проверка `id_token`, обмен и отзыв Apple, фикстуры | 2 |
| `backend/internal/identity/internal/webauthn/*`, `backend/internal/identity/identitytest/{authenticator.go,authenticator_test.go}` | обёртка `go-webauthn`, виртуальный аутентификатор | 3 |
| `backend/internal/identity/internal/attest/*` (+ вшитый корень Apple), `backend/internal/identity/identitytest/{appattest.go,play.go,devicecheck.go}` | App Attest, Play Integrity, DeviceCheck, фикстуры | 4 |
| `backend/migrations/{00NN_identity_provider_calls.sql,identity_00NN_test.go,README.md}`, `backend/internal/platform/grants/{grants.sql,grants_test.go}`, `backend/internal/archtest/ownership.go`, `backend/internal/identity/queries/models.sql`, `backend/internal/identity/internal/store/identitydb/models.go` | таблица `provider_calls`, права, владение | 5 |
| `backend/internal/identity/{events.go,events_test.go,testdata/events/*.golden.json}`, `backend/internal/identity/internal/domain/{security.go,session_terms.go,social.go,methods.go,passkey_name.go,device_shared.go,provider_calls.go}` (+ тесты), `backend/internal/identity/internal/app/{mail.go,audit.go}`, `backend/internal/identity/mail/{composer.go,composer_test.go}`, `backend/locales/{ru,en}.json` | домен, события, виды писем и Composer I-3 | 6 |
| `backend/internal/identity/{identity.go,providers.go,providers_test.go}`, `backend/internal/identity/identitytest/fakes.go`, `backend/internal/identity/internal/app/{app.go,provider_calls.go,cleanup.go (одна строка)}` + файлы-заглушки волны 3, `backend/internal/identity/httpapi/{social.go,passkeys.go,devices.go}` (из `pending_i3.go`, он удаляется), `backend/internal/httpapi/public/{pending_social_test.go,pending_passkeys_test.go,pending_devices_test.go,nostub_test.go,handler_test.go}` (`pending_i3_test.go` удаляется), `backend/internal/identity/jobs/{jobs.go,provider_calls.go}`, `backend/internal/platform/queue/foreign.go`, `backend/cmd/api/{main.go,main_test.go}`, `backend/cmd/internal/workerhost/{workerhost.go,registry.go}` (+ тесты), `backend/internal/archtest/boundary_test.go` | порты, `Deps`, сборка api и воркера, раскладка заглушек | 7 |
| `backend/internal/identity/internal/app/{login_core.go,mfa_login.go,reauth_op.go,devices.go,signup.go,login.go,mfa.go,new_device.go,identities.go}`, `backend/internal/identity/queries/{devices.sql,identities.sql}`, `backend/internal/identity/httpapi/{mfa.go,password.go}` | ветки I-3 в ядре входа, новое и общее устройство, отложенная привязка | 8 |
| `…/app/{device_trust.go,device_challenge.go,provider_calls_run.go}`, `…/queries/{device_trust.sql,provider_calls.sql}`, `…/httpapi/devices.go`, `…/jobs/provider_calls.go`, `pending_devices_test.go` | аттестация в сценариях, DeviceCheck, исполнитель `provider_calls` | 9 |
| `…/app/social.go`, `…/queries/social.sql`, `…/httpapi/social.go` (3 операции), `pending_social_test.go` | nonce, соцвход, дорегистрация | 10 |
| `…/app/{social_link.go,anonymize.go,cleanup.go}`, `…/queries/{social_link.sql,deletion.sql,cleanup.sql}`, `…/httpapi/social.go` (2 операции), `pending_social_test.go` | привязка, отвязка, отзыв Apple, свежий вход соцвходом | 11 |
| `…/app/passkeys.go`, `…/queries/passkeys.sql`, `…/httpapi/passkeys.go` (4 операции), `pending_passkeys_test.go` | добавление, переименование, удаление ключей | 12 |
| `…/app/passkey_auth.go`, `…/queries/passkey_auth.sql`, `…/httpapi/passkeys.go` (4 операции), `pending_passkeys_test.go` | вход ключом, второй фактор и свежий вход ключом | 13 |
| `backend/internal/httpapi/public/{identity_core_test.go,identity_i3_test.go,nostub_test.go}`, `.claude/rules/backend-identity.md`, `docs/01-домен-и-правила.md`, спека identity (§0.2, §5.3, §11, §18), спека бэкенда, `docs/deploy-dev.md` (раздел I-3), `CLAUDE.md` (если меняется список правил) | сквозные стражи и документы I-3 | 14 |
| вне репозитория — панель Render, флаги в админке стенда; `render.yaml` (значения не секретов, если решено) | выкатка ключей и флагов на стенд | 15 (контроллер) |

`…` — `backend/internal/identity`. Внутри пакета исполнитель раскладывает файлы сам, если не трогает файлы-владельцы
других задач и интерфейсы стыков.

# Задачи

Обозначения: **Риск** — A/B/C; **Тесты** — `Имя` → вход → ожидаемое; **Подсадка** — какой баг подсадить, какой тест
обязан упасть; **Приёмка** — что проверяет ревьюер; **Не трогай** — файлы других задач волны. Сквозные условия задач
сценариев (9–13): тесты `app` — на реальном Postgres под ролью прода; на каждый код ошибки операции — свой случай;
событие, аудит, задача письма и строка `provider_calls` — в той же транзакции (откат — ничего из этого нет); HTTP-тест
на каждую операцию — успех по контракту, `Cache-Control` по контракту и хотя бы один код; операция уходит из
`pending_*`; внешние провайдеры — `identitytest`.

## Шаг 0 (контроллер, до волны 1)

- Сверить, что `spec/embedded-worker` слита в `main`, ветка `spec/identity-3` — от `origin/main`.
- Перепроверить версии (`go list -m -versions` и `go list -m -json <модуль>@<версия>` в каталоге-песочнице scratchpad, не
  в `backend/`): берётся последняя версия, опубликованная ≥ 2 недель назад. На 16.11.2026: `go-webauthn` v0.18.2,
  `go-oidc/v3` v3.21.0, `cbor/v2` v2.10.0. Косвенная `go-jose/v4` — та, что требует `go-oidc` (v4.1.4 по его `go.mod`),
  не поднимать до v4.1.9 (05.11 — моложе двух недель) без отдельного решения.
- `cd backend && go get github.com/go-webauthn/webauthn@v0.18.2 github.com/coreos/go-oidc/v3@v3.21.0 github.com/fxamacker/cbor/v2@v2.10.0`
  (требования помечены `// indirect` до первого импорта — это нормально; `go mod tidy` — в Task 14).
- `docs/versions.md` — три строки (версия, где закреплено, зачем) и строка «`google.golang.org/api` — не используется,
  R22».
- Коммит: «I-3: зависимости go-webauthn, go-oidc, cbor». Исполнители I-3 `go get`/`go mod tidy` не запускают; нужен
  ещё модуль — через контроллера.

## Этап 6–8, волна 1. Платформа и провайдеры

### Task 1: конфиг и окружение I-3

**Цель.** Все ключи §11.3 для I-3 у api и воркера, с проверкой «группа целиком или пусто» до базы (R3), режим
аттестации, шаблоны и стенд без значений секретов.

**Риск:** B (отказ старта и секреты в репозитории).

**Файлы.** Изменить: `backend/internal/platform/config/{config.go,config_test.go,render_stand_test.go}`,
`backend/.env.example`, `deploy/server/api.env.example`, `deploy/server/worker.env.example`, `render.yaml`,
`docs/deploy-dev.md` (раздел identity: ключи I-3, откуда берутся — ссылка на «Что нужно от пользователя»),
`.claude/rules/env.md` (строка про `WORKER_IDENTITY_APPLE_*` и base64 секретов). Локальный `backend/.env` — скриптом
из scratchpad: новые ключи **пустыми** (провайдеры не настроены) и `API_IDENTITY_ATTEST_MODE=off`.

**Интерфейсы.**
- `config.Identity` (префикс `API_IDENTITY_`) дополняется вложенными структурами без `required`:
  - `Apple struct { TeamID string \`env:"APPLE_TEAM_ID"\`; KeyID string \`env:"APPLE_KEY_ID"\`; PrivateKey string
    \`env:"APPLE_PRIVATE_KEY"\` /* base64 файла .p8 одной строкой */; ClientIDs []string \`env:"APPLE_CLIENT_IDS"
    envSeparator:","\` /* bundle id приложений и Services ID */ }`;
  - `Google struct { ClientIDs []string \`env:"GOOGLE_CLIENT_IDS" envSeparator:","\` }` (iOS, Android, web);
  - `WebAuthn struct { RPID string \`env:"WEBAUTHN_RP_ID"\`; RPName string \`env:"WEBAUTHN_RP_NAME"\`; Origins []string
    \`env:"WEBAUTHN_ORIGINS" envSeparator:","\` }`;
  - `AppAttest struct { AppIDs []string \`env:"APPATTEST_APP_IDS" envSeparator:","\`; Env string
    \`env:"APPATTEST_ENV" envDefault:"production"\` }` (`<TEAMID>.<bundle id>`; `development | production` — R23);
  - `Play struct { Package string \`env:"PLAY_PACKAGE"\`; Credentials string \`env:"PLAY_CREDENTIALS"\` /* base64 JSON
    сервисного аккаунта */ }`;
  - `AttestMode string \`env:"ATTEST_MODE" envDefault:"observe"\`` (спека §3.8).
- `config.WorkerIdentity` (префикс `WORKER_IDENTITY_`) — `Apple struct { TeamID, KeyID, PrivateKey }` (те же значения,
  что у api; без `ClientIDs`: отзыв берёт `client_id` из шифротекста, R8) и `AppAttestEnv string
  \`env:"APPATTEST_ENV" envDefault:"production"\`` (хост DeviceCheck, R23). Обязательных полей не добавляется —
  `WORKER_*` у api во встроенном режиме читаются по спеке 2026-11-16 и без ключа Apple стартуют.
- `func (i Identity) Validate() error` и `func (w WorkerIdentity) Validate() error` — зовутся там, где сейчас
  проверяется конфиг до базы (`cmd/api`, `workerhost.Prepare`; вызовы ставит Task 7):
  - группа Apple, WebAuthn, App Attest, Play — все поля заданы или все пусты, иначе ошибка с именами пустых ключей
    (`API_IDENTITY_APPLE_KEY_ID` …; значения в текст ошибки не попадают); Google — одно поле;
  - `AttestMode` ∈ `off | observe | enforce`; `AppAttest.Env`/`AppAttestEnv` ∈ `development | production`;
  - `WebAuthn.Origins`: каждый — `https://<host>[:port]` или `android:apk-key-hash:<base64url>` (R4); `RPID` — имя хоста
    без схемы и порта, `https://<RPID>` или поддомен — среди origins (иначе ключи не создадутся ни у кого);
  - `AppAttest.AppIDs` — `^[A-Z0-9]{10}\.[A-Za-z0-9.-]+$`;
  - содержимое ключей (PEM, JSON) здесь не разбирается — это `identity.NewProviders` (Task 7).
- `render.yaml`, сервис `wf-api`: `API_IDENTITY_ATTEST_MODE=off` значением в файле (Q1; решение пользователя может
  сменить на `observe` в Task 15); все прочие новые ключи api и `WORKER_IDENTITY_APPLE_*` — `sync: false` (значения
  — в панели, Task 15; секреты `*_PRIVATE_KEY`, `*_PLAY_CREDENTIALS` — только так).
- `.env.example` и `deploy/server/*.env.example` — ключи с комментарием и пустым значением (не плейсхолдером
  `<base64-32-байта>`: `scripts/env-keys.sh` не должен генерировать ключ Apple).

**Тесты.**
- `TestIdentityProviderGroupsAllOrNothing` → таблица: Apple без `KEY_ID`, WebAuthn без `ORIGINS`, Play без
  `CREDENTIALS`, App Attest без `APP_IDS` → ошибка с именем пустого ключа и без значений; всё пусто → ок; всё задано →
  ок.
- `TestIdentityAttestMode` → `""` (умолчание) → `observe`; `strict` → ошибка; `off` → ок.
- `TestWebAuthnOriginsForm` → `http://x` → ошибка; `android:apk-key-hash:` без значения → ошибка; RPID `wf.example`,
  origin `https://app.wf.example` → ок; RPID не покрыт origins → ошибка.
- `TestAppAttestAppIDsForm` → `com.wf.app` без Team ID → ошибка.
- `TestWorkerIdentityAppleOptional` → `WORKER_IDENTITY_SECRET_KEYS` без Apple → ок; Apple частично → ошибка.
- `render_stand_test.go`: `TestRenderYAMLKnownKeys` знает новые ключи (`config.Identity` и `config.WorkerIdentity`);
  `TestRenderYAMLSecretsNotInFile` — `API_IDENTITY_APPLE_PRIVATE_KEY`, `API_IDENTITY_PLAY_CREDENTIALS`,
  `WORKER_IDENTITY_APPLE_PRIVATE_KEY` только `sync: false`; новый `TestRenderYAMLAttestModeExplicit` — у `wf-api` режим
  задан явно (умолчание `observe` без клиента нативщика давало бы PoW на каждом входе).

**Подсадка.** Убрать проверку «всё или ничего» у Apple → `TestIdentityProviderGroupsAllOrNothing` красный. Вписать
значение `API_IDENTITY_APPLE_PRIVATE_KEY` в `render.yaml` → `TestRenderYAMLSecretsNotInFile` красный.

**Приёмка.** Без новых ключей api и воркер стартуют как раньше; ни одного значения секрета в репозитории; ошибки не
печатают значения; `docs/deploy-dev.md` перечисляет ключи и формат (base64 одной строкой).

**Ловушки.** `.p8` — многострочный PEM: в окружение — base64 файла целиком (Render и `.env` переносы строк ломают).
`WORKER_*` у api во встроенном режиме — по спеке 2026-11-16: проверка «одно кольцо» не касается ключа Apple.

**Не трогай.** `backend/internal/identity/**`, `backend/cmd/**` (вызовы `Validate` ставит Task 7).

### Task 2: ключ Apple, проверка `id_token`, обмен и отзыв Apple

**Цель.** Проверка `id_token` Apple и Google без доверия клиенту (§3.5, §9.3) и клиент Apple `/auth/token` и
`/auth/revoke` с client secret ES256 — чистые пакеты без базы, с фикстурами для всех следующих задач.

**Риск:** A (подмена токена другого приложения, `alg`, nonce, сдвиг времени).

**Файлы.** Создать: `backend/internal/identity/internal/applekey/{applekey.go,applekey_test.go}`;
`backend/internal/identity/internal/social/{verifier.go,claims.go,nonce.go,apple.go}` и тесты;
`backend/internal/identity/identitytest/{social.go,apple.go}` (не `_test`: импортируют тесты `app` и
`internal/httpapi/public`).

**Интерфейсы.**
- `package applekey`: `type Key struct { TeamID, KeyID string; /* *ecdsa.PrivateKey */ }`; `func Parse(teamID, keyID,
  p8Base64 string) (*Key, error)` (base64 → PEM → PKCS#8 → ECDSA P-256; иначе ошибка без содержимого);
  `func (k *Key) ClientSecret(clientID string, now time.Time) (string, error)` — JWT ES256, заголовок `kid`, `iss` =
  Team ID, `sub` = client id, `aud` = `https://appleid.apple.com`, `iat`, `exp = iat + 5 мин`; `func (k *Key)
  ProviderJWT(now time.Time) (string, error)` — JWT ES256 `iss` = Team ID, `iat`, `kid` (DeviceCheck, Task 4).
- `package social`: `type Provider string` (`apple`, `google`); `type Claims struct { Provider Provider; Subject,
  Email, Audience, Nonce string; HasEmail, EmailVerified, PrivateRelay bool }`; `type Config struct {
  AppleClientIDs, GoogleClientIDs []string; AppleJWKS, GoogleJWKS string /* пусто — боевые адреса */; HTTP
  *http.Client; Now func() time.Time }`; `func NewVerifier(ctx context.Context, cfg Config) *Verifier` (без сети —
  R7); `func (v *Verifier) Verify(ctx context.Context, p Provider, raw string) (Claims, error)` — ошибки
  `ErrInvalid` (подпись, `iss`, `aud`, `exp`, `alg`, формат, провайдер без client id) и `ErrUnavailable` (JWKS
  недоступен); `func NonceMatches(p Provider, claim string, stored [32]byte) bool` — Apple: `claim` =
  hex(SHA-256(nonce)) нижним регистром, сравнение с `hex(stored)` за постоянное время; Google: SHA-256(`claim`) =
  `stored` (R6).
- Издатели: Apple `https://appleid.apple.com`; Google `https://accounts.google.com` и `accounts.google.com`. Боевые
  JWKS: `https://appleid.apple.com/auth/keys`, `https://www.googleapis.com/oauth2/v3/certs`. `go-oidc`:
  `oidc.NewRemoteKeySet(ctx, jwks)` + `oidc.NewVerifier(issuer, keySet, &oidc.Config{SkipClientIDCheck: true,
  SkipIssuerCheck: <только Google, своя сверка>, SupportedSigningAlgs: ["RS256", "ES256"], Now: cfg.Now})`;
  `aud` — сверка со списком провайдера (любое совпадение в массиве `aud`), `Audience` — совпавшее значение.
- Apple: `email_verified` и `is_private_email` — строка `"true"/"false"` или bool (§18); почты нет — `HasEmail =
  false`; `PrivateRelay` у Google всегда `false`.
- `type AppleClient`; `func NewAppleClient(key *applekey.Key, baseURL string /* пусто — https://appleid.apple.com */,
  hc *http.Client, now func() time.Time) *AppleClient`; `func (c *AppleClient) Exchange(ctx, clientID, code string)
  (refresh string, err error)` (`POST /auth/token`, `grant_type=authorization_code`); `func (c *AppleClient)
  Revoke(ctx, clientID, refresh string) error` (`POST /auth/revoke`, `token_type_hint=refresh_token`). Ответы: 200 —
  успех; 400 `invalid_grant`/`invalid_request`/`invalid_client` — `ErrInvalid` (повтор не поможет); 5xx, таймаут, сеть
  — `ErrUnavailable`. Тело ответа ошибки и токены не попадают в текст ошибки.
- `identitytest`: `type OIDCProvider struct{ URL string /* JWKS */ }`; `func NewOIDCProvider(t testing.TB)
  *OIDCProvider` (httptest, ключи RS256, ротация `Rotate()`), `func (p *OIDCProvider) Mint(t testing.TB, c
  TokenClaims) string` (`TokenClaims` — iss, aud, sub, email, email_verified как `any` — для строк Apple, nonce, exp,
  kid, alg, ключ подписи: настоящий или чужой); `Down()` — JWKS отвечает 503. `type AppleServer` — подделка
  `/auth/token` и `/auth/revoke` (проверяет форму и client secret по открытому ключу тестового `.p8`, отдаёт
  заданный ответ, считает вызовы); `func TestAppleKey(t testing.TB) (teamID, keyID, p8Base64 string)`.

**Тесты.**
- `TestParseAppleKey` → годный `.p8` → ок; не base64, не PEM, RSA вместо EC → ошибка без содержимого ключа в тексте.
- `TestClientSecretClaims` → разбор выданного JWT открытым ключом: `alg = ES256`, `kid`, `iss`, `sub`, `aud`,
  `exp − iat = 5 мин` по переданному `now`.
- `TestVerifyAppleOK`, `TestVerifyGoogleOK` → годный токен → `Claims` с `Subject`, `Email`, `Audience`, `Nonce`.
- `TestVerifyRejects` — таблица: чужой `aud`; чужой `iss`; Apple-токен через Google; истёк (`Now` после `exp`); подпись
  чужим ключом с тем же `kid`; `alg: none`; `alg: HS256` с открытым ключом как секретом; нет `kid`; мусор вместо JWT;
  провайдер без client id в конфиге → каждый `ErrInvalid`.
- `TestGoogleIssuerBothForms` → `https://accounts.google.com` и `accounts.google.com` → ок; `https://evil` → `ErrInvalid`.
- `TestAppleBoolOrString` → `email_verified`: `"true"`, `true` → `true`; `"false"`, `false`, нет поля → `false`; то же
  `is_private_email`.
- `TestEmailMissing` → токен без `email` → `HasEmail = false`, ошибки нет (решение — у сценария).
- `TestNonceMatches` → Apple: hex SHA-256 — да; сырой nonce — нет; верхний регистр hex — нет; Google: сырой — да; hex — нет.
- `TestJWKSUnavailable` → `Down()` → `ErrUnavailable` (не `ErrInvalid`).
- `TestJWKSRotation` → провайдер сменил ключ (`Rotate`) → токен новым `kid` проходит без перезапуска.
- `TestAppleExchangeRevoke` → форма запроса (`client_id`, `client_secret` проверяется подделкой, `code`/`token`),
  200 → ок; `invalid_grant` → `ErrInvalid`; 503 → `ErrUnavailable`; текст ошибки без токенов.

**Подсадка.** Не сверять `aud` → `TestVerifyRejects` красный. Сравнивать nonce Apple с сырым значением →
`TestNonceMatches` красный. Разрешить `HS256` → `TestVerifyRejects` красный.

**Приёмка.** Пакеты не импортируют корень `identity`, `app`, базу; сети при создании `Verifier` нет; время — только
`Now`; секреты (ключ `.p8`, токены) не в ошибках и логах.

**Ловушки.** `go-oidc` проверяет `iss` строго — для Google либо два верификатора, либо `SkipIssuerCheck` и своя
сверка; `aud` у Google бывает массивом. `RemoteKeySet` — один на процесс и провайдера (кэш JWKS), его `ctx` — ctx
процесса, не запроса.

**Не трогай.** `backend/go.mod` (Шаг 0), `identity/identity.go` (Task 7).

### Task 3: WebAuthn — обёртка `go-webauthn` и виртуальный аутентификатор

**Цель.** Регистрация и проверка ключей доступа по §3.6, §9.4 через `go-webauthn` v0.18.2 за узким API без базы;
свой виртуальный аутентификатор для всех тестов (R21).

**Риск:** A (origin, rpId, UV, клон, сериализация сессии).

**Файлы.** Создать: `backend/internal/identity/internal/webauthn/{rp.go,session.go,user.go}` и тесты;
`backend/internal/identity/identitytest/{authenticator.go,authenticator_test.go}`.

**Интерфейсы.**
- `type Config struct { RPID, RPName string; Origins []string }`; `func New(cfg Config) (*RP, error)` — `https://…` →
  `webauthn.Config.RPOrigins`, `android:apk-key-hash:…` → `RPOpaqueOrigins` (R4; проверено по исходнику v0.18.2).
- `type Cred struct { ID, PublicKey []byte; AAGUID uuid.UUID; SignCount uint32; Transports []string; BackupEligible,
  BackupState bool; AttestationFormat string }`; `type User struct { Handle []byte; Name, DisplayName string; Creds
  []Cred }` (адаптер `webauthn.User` — внутри пакета; `Name`/`DisplayName` — ник, не почта).
- `type Result struct { Cred Cred /* обновлённые счётчик и флаги */; CloneWarning bool }`; `var ErrInvalid`.
- `func (r *RP) BeginRegistration(u User) (options, session []byte, err error)` — `residentKey: required`,
  `userVerification: required`, `attestation: none`, `excludeCredentials` — ключи `u`; `options` — объект
  `publicKey` из `protocol.CredentialCreation` (контракт: `PublicKeyCredentialCreationOptionsJSON`, без обёртки).
- `func (r *RP) FinishRegistration(u User, session, credential []byte) (Cred, error)` —
  `protocol.ParseCredentialCreationResponseBytes` + `CreateCredential`; любая ошибка разбора и проверки — `ErrInvalid`.
- `func (r *RP) BeginDiscoverableLogin() (options, session []byte, err error)` — пустой `allowCredentials`, UV required.
- `func (r *RP) BeginLogin(u User) (options, session []byte, err error)` — `allowCredentials` — ключи `u`.
- `func (r *RP) FinishDiscoverableLogin(session, credential []byte, find func(handle, credID []byte) (User, error))
  (User, Result, error)` — `ValidatePasskeyLogin`; `find` вернул ошибку → `ErrInvalid`.
- `func (r *RP) FinishLogin(u User, session, credential []byte) (Result, error)` — `ValidateLogin`.
- `func ParseAssertionIDs(credential []byte) (credID, userHandle []byte, err error)` — для поиска строки до замка
  (Task 13).
- Сессия (`session`) — JSON `webauthn.SessionData` с **обнулённым `Expires`** (R5): библиотека сверяет его с
  `time.Now()`, срок задачи — `auth_challenges.expires_at` по `clock`.
- `identitytest.Authenticator`: `func NewAuthenticator(t testing.TB, origin, rpID string) *Authenticator`; поля-ручки
  `Origin`, `RPIDForHash`, `UV`, `UP`, `BE`, `BS bool`, `Counter uint32`; `func (a *Authenticator) Register(t, options
  []byte, userHandle []byte) (credential, credID []byte)` (attestation `none`, ES256, COSE через `cbor`); `func (a
  *Authenticator) Assert(t, options []byte, credID []byte) []byte` (подпись `authData ‖ SHA-256(clientDataJSON)`,
  `userHandle` — сохранённый при регистрации); `OtherKey()` — подписать чужим ключом.

**Тесты.**
- `TestRegisterNoneOK` → `BeginRegistration` → `Authenticator.Register` → `FinishRegistration` → `Cred` с COSE-ключом,
  флагами BE/BS, `AttestationFormat = "none"`.
- `TestRegistrationOptionsShape` → JSON `options`: `rp.id`, `user.id` = handle (base64url), `user.name` = ник,
  `authenticatorSelection.residentKey = required`, `userVerification = required`, `attestation = none`,
  `excludeCredentials` — переданные ключи; обёртки `publicKey` нет.
- `TestRegisterRejects` — таблица: чужой origin, `rpIdHash` чужого домена, без UV, без UP, `type: webauthn.get`,
  challenge не тот → `ErrInvalid`.
- `TestDiscoverableLoginOK` → `allowCredentials` пуст, `find` получает handle и id ключа, `Result.Cred.SignCount`
  вырос.
- `TestLoginRejects` — таблица: чужой origin, чужой rpId, без UV, подпись чужим ключом, ключ не из
  `allowCredentials` (`FinishLogin`), неизвестный ключ (`find` → ошибка), повреждённый JSON → `ErrInvalid`.
- `TestAndroidOpaqueOrigin` → origin `android:apk-key-hash:<x>` в конфиге → вход проходит; другой hash → `ErrInvalid`;
  без opaque-origin в конфиге → `ErrInvalid`.
- `TestCloneWarning` → сохранённый счётчик 5, предъявлен 3 → `CloneWarning = true`, ошибки нет; сохранённый 0 и
  предъявлен 0 → `false`.
- `TestSessionExpiresZeroed` → в `session` поле `expires` нулевое; сессия, сериализованная «вчера» по настоящим часам,
  проходит (срок — забота вызывающего).

**Подсадка.** Перенести opaque-origins в `RPOrigins` или потерять их → `TestAndroidOpaqueOrigin` красный (если
библиотека сравнивает строки и тест остался зелёным — записать в отчёт, тест оставить как страж конфигурации). Снять
требование UV (`preferred`) → `TestLoginRejects` (без UV) красный.

**Приёмка.** Пакет без базы и без корня `identity`; ник, а не почта, в `user.name`; ни одной зависимости кроме
`go-webauthn`, `cbor`, `uuid`.

**Ловушки.** Имена API v0.18.2 сверены (`ValidatePasskeyLogin`, `ValidateLogin`, `CreateCredential`,
`Parse*ResponseBytes`, `CloneWarning`); v0.x — API меняется между минорными, закрепить версию. Флаги BE/BS:
библиотека сверяет постоянство BE — сохранённые флаги передавать в `User.Creds`.

**Не трогай.** `identitytest/{social,apple}.go` (Task 2), `identitytest/{appattest,play,devicecheck}.go` (Task 4).

### Task 4: целостность устройства — App Attest, Play Integrity, DeviceCheck

**Цель.** Проверки §9.5 чистыми функциями и HTTP-клиентами без базы: состояние (задача, ключ, счётчик) — у сценария
Task 9.

**Риск:** A (цепочка сертификатов, nonce, счётчик, свежесть, недоступность провайдера).

**Файлы.** Создать: `backend/internal/identity/internal/attest/{appattest.go,assertion.go,playintegrity.go,devicecheck.go,roots.go,apple_app_attestation_root.pem}`
и тесты; `backend/internal/identity/identitytest/{appattest.go,play.go,devicecheck.go}`.

**Интерфейсы.**
- Ошибки пакета: `ErrInvalid` (отрицательный вердикт), `ErrUnavailable` (провайдер недоступен, 5xx, таймаут).
- App Attest: `type AppAttestConfig struct { AppIDs []string; Env string /* development | production */; Roots
  *x509.CertPool /* nil — вшитый корень Apple */; Now func() time.Time }`; `func NewAppAttest(cfg AppAttestConfig)
  (*AppAttest, error)`; `type AttestedKey struct { KeyID, PublicKey []byte; AppID string; Counter uint32 }`;
  `func (v *AppAttest) VerifyAttestation(keyID, attestation, clientDataHash []byte) (AttestedKey, error)` — CBOR
  `{fmt: "apple-appattest", attStmt: {x5c, receipt}, authData}`; цепочка `x5c` до корня на `Now`; nonce =
  SHA-256(`authData ‖ clientDataHash`) равен значению расширения OID `1.2.840.113635.100.8.2` листа; `credentialId`
  в `authData` = `keyID` = SHA-256(открытого ключа листа, несжатый X9.62); `rpIdHash` = SHA-256 одного из `AppIDs`;
  `counter = 0`; `aaguid` — `appattestdevelop` (development) или `appattest` + семь нулевых байт (production).
  `func (v *AppAttest) VerifyAssertion(publicKey []byte, appID string, prevCounter uint32, assertion, clientDataHash
  []byte) (uint32, error)` — CBOR `{signature, authenticatorData}`, подпись ECDSA над SHA-256(`authenticatorData ‖
  clientDataHash`), `rpIdHash` = SHA-256(`appID`), счётчик строго больше `prevCounter`.
- Play Integrity: `type PlayConfig struct { Package string; Credentials []byte /* JSON сервисного аккаунта */;
  APIBase, TokenURL string /* пусто — боевые */; HTTP *http.Client; Now func() time.Time }`; `func NewPlayIntegrity(cfg
  PlayConfig) (*PlayIntegrity, error)` (разбор JSON: `client_email`, `private_key` RSA, `token_uri`); `func (p
  *PlayIntegrity) Verify(ctx, token, requestHash string) error` — access-токен по JWT bearer (RS256 через
  `golang-jwt`, scope `https://www.googleapis.com/auth/playintegrity`, кэш до `expires_in − 60 с`), `POST
  /v1/{package}:decodeIntegrityToken`; проверки: `requestDetails.requestHash == requestHash`,
  `requestDetails.requestPackageName == Package`, `timestampMillis` не старше 5 минут по `Now`,
  `appIntegrity.appRecognitionVerdict == PLAY_RECOGNIZED`, `deviceIntegrity.deviceRecognitionVerdict` содержит
  `MEETS_DEVICE_INTEGRITY`; ответ 400 «токен не разбирается» — `ErrInvalid`; 5xx/сеть — `ErrUnavailable` (R22: без
  `google.golang.org/api`).
- DeviceCheck: `func NewDeviceCheck(key *applekey.Key, env string, base string /* пусто — по env */, hc *http.Client,
  now func() time.Time) *DeviceCheck`; хосты `https://api.devicecheck.apple.com` и
  `https://api.development.devicecheck.apple.com` (R23); `func (d *DeviceCheck) Query(ctx, token string) (count int,
  err error)` — `POST /v1/query_two_bits` (`device_token`, `transaction_id`, `timestamp` мс), биты `bit0 | bit1<<1`
  → 0–3; ответ «биты не заданы» (200 с текстом вместо JSON — §18) → 0; `func (d *DeviceCheck) Update(ctx, token string,
  count int) error` — `POST /v1/update_two_bits` (`count` 0–3 → биты); 400 (токен негоден) — `ErrInvalid`; 5xx/сеть —
  `ErrUnavailable`.
- Вшитый `apple_app_attestation_root.pem` — Apple App Attestation Root CA с
  `https://www.apple.com/certificateauthority/Apple_App_Attestation_Root_CA.pem`; в комментарии `roots.go` — SHA-256
  отпечаток, сверенный с опубликованным Apple (исполнитель скачивает один раз и пишет в отчёт, откуда и отпечаток).
- `identitytest`: `func NewAppAttestCA(t) *AppAttestCA` (свой корень и промежуточный); `func (ca) Device(t, appID, env
  string) *AttestDevice` с `Attest(clientDataHash) (keyID, attestation []byte)` и `Assert(clientDataHash, counter
  uint32) []byte` и ручками порчи (`WrongNonce`, `WrongAppID`, `WrongAAGUID`); `NewPlayServer(t)` — подделка токен-ручки
  Google и `decodeIntegrityToken` с заданным вердиктом; `NewDeviceCheckServer(t)` — подделка двух ручек со счётчиком
  по токену, режимы 503 и «биты не заданы».

**Тесты.**
- `TestAppAttestOK` → тестовый корень, годная аттестация → `AttestedKey` с ключом и `AppID`.
- `TestAppAttestRejects` — таблица: чужой корень, истёкший лист (`Now`), nonce не тот, `keyID` ≠ SHA-256 ключа, чужой
  App ID, `aaguid` development при `Env = production` и наоборот, `counter ≠ 0`, `fmt` не тот, мусор CBOR → `ErrInvalid`.
- `TestAssertionCounter` → счётчик 1 при `prev = 0` → 1; повтор с 1 при `prev = 1` → `ErrInvalid`; меньший → `ErrInvalid`;
  подпись чужим ключом → `ErrInvalid`.
- `TestEmbeddedRootParses` → вшитый PEM разбирается, это CA, отпечаток равен константе в коде.
- `TestPlayIntegrityVerdicts` — таблица: годный → ок; `requestHash` чужой, пакет чужой, `UNRECOGNIZED_VERSION`, нет
  `MEETS_DEVICE_INTEGRITY`, `timestampMillis` старше 5 мин → `ErrInvalid`; 503 → `ErrUnavailable`.
- `TestPlayIntegrityTokenCache` → два `Verify` подряд → один запрос к токен-ручке; после `expires_in` → новый.
- `TestDeviceCheckQueryUpdate` → нет битов → 0; `Update(2)` → `Query` → 2; JWT в `Authorization: Bearer` подписан
  ключом Apple (`kid`, `iss`); 503 → `ErrUnavailable`; 400 → `ErrInvalid`.

**Подсадка.** Не сверять nonce расширения → `TestAppAttestRejects` красный. Разрешить равный счётчик →
`TestAssertionCounter` красный. Не проверять свежесть Play → `TestPlayIntegrityVerdicts` красный.

**Приёмка.** Без базы и корня `identity`; время — `Now`; токены и ключ сервисного аккаунта не в ошибках и логах;
`google.golang.org/api` не импортирован (R22).

**Ловушки.** Расширение nonce — ASN.1 `SEQUENCE { [1] EXPLICIT OCTET STRING }`; открытый ключ листа — несжатая точка
(65 байт) для `credentialId`. Тестовые векторы Apple не используются (§18 — публичных годных к приёму нет); вместо них
— своя цепочка `identitytest` и проверка вшитого корня.

**Не трогай.** `identitytest/{social,apple,authenticator}.go`.

### Task 5: схема — `provider_calls`, права, владение

**Цель.** Хранение секретов исходящих вызовов с повтором (отзыв Apple, обновление DeviceCheck) вне аргументов задач
(R9) и права ролей ровно по ним.

**Риск:** B.

**Файлы.** Создать: `backend/migrations/00NN_identity_provider_calls.sql`, `backend/migrations/identity_00NN_test.go`.
Изменить: `backend/migrations/README.md`, `backend/internal/platform/grants/{grants.sql,grants_test.go}`,
`backend/internal/archtest/ownership.go`, `backend/internal/identity/queries/models.sql` (якорь модели),
`backend/internal/identity/internal/store/identitydb/models.go` (`sqlc generate`).

**Интерфейсы.**
- ```
  CREATE TYPE provider_call_kind AS ENUM ('apple_revoke', 'devicecheck_update');
  provider_calls: id uuid PK, kind provider_call_kind NOT NULL, payload_enc bytea NOT NULL, key_id text NOT NULL
    (CHECK формат как у прочих key_id), created_at timestamptz NOT NULL; индекс (created_at)
  ```
  Связи с `users` нет: после анонимизации строка не должна указывать на аккаунт (R9).
- `grants.sql`: `api` — `INSERT` (отвязка, обновление DeviceCheck после регистрации); `worker` — `SELECT, INSERT,
  DELETE` (исполнение, перенос при анонимизации и сверке, чистка); `admin` — ничего. `UPDATE` нет ни у кого (повторы
  считает River).
- Владелец — `identity` (`ownership.go`); якорь в `queries/models.sql`, чтобы модель `ProviderCall` была у всех задач.
- `down` — `DROP TABLE`, `DROP TYPE` (строки теряются — допустимо, `README.md`).

**Тесты.**
- `identity_00NN_test.go` → вверх-вниз до точной прежней схемы (как `identity_0026_test.go`).
- `grants_test.go`: `TestProviderCallsGrants` → под `admin` — `SELECT` 42501; под `api` — `INSERT` ок, `SELECT`
  42501; под `worker` — `SELECT`, `DELETE` ок, `UPDATE` 42501.

**Подсадка.** Дать `api` `SELECT` → `TestProviderCallsGrants` красный.

**Приёмка.** Ветка CASE в `grants.sql` явная (не `ELSE`); `README.md` миграций — таблица, назначение, R9.

**Ловушки.** Номер — следующий свободный на момент задачи (R31); применённые миграции не правятся.

**Не трогай.** `config/**` (Task 1).

### Task 6: домен, события, виды писем и Composer I-3

**Цель.** Чистые правила I-3 и всё, что сценарии волны 3 пишут в журнал, события и письма, — заранее, одним
владельцем: сценарии волны 3 только зовут готовое.

**Риск:** B (ПД в событиях и журнале, ветки автопривязки).

**Файлы.** Изменить: `backend/internal/identity/{events.go,events_test.go}` (+ `testdata/events/{identity_linked,identity_unlinked,passkey_added,passkey_removed,device_shared}.golden.json`);
`backend/internal/identity/internal/domain/{security.go,session_terms.go}`; создать
`backend/internal/identity/internal/domain/{social.go,methods.go,passkey_name.go,device_shared.go,provider_calls.go}` и
тесты; изменить `backend/internal/identity/internal/app/{mail.go,audit.go}`,
`backend/internal/identity/mail/{composer.go,composer_test.go}`, `backend/locales/{ru,en}.json`.

**Интерфейсы.**
- События (спека §8.1): `EventIdentityLinked`, `EventIdentityUnlinked` (агрегат user, payload `{user_id, provider}`);
  `EventPasskeyAdded`, `EventPasskeyRemoved` (user, `{user_id, passkey_id}`); `EventDeviceShared` (агрегат `device`,
  `AggregateDevice = "device"`, `type DeviceRef struct { ID uuid.UUID; Version int64 }` — версия = unix-секунды
  `devices.last_seen_at`, payload `{device_id, accounts_30d}`). Конструкторы `IdentityLinked(u UserRef, provider
  string)`, `IdentityUnlinked(…)`, `PasskeyAdded(u UserRef, passkeyID uuid.UUID)`, `PasskeyRemoved(…)`,
  `DeviceShared(d DeviceRef, accounts int)`; `EventTypes()` — все события I-2 и I-3.
- Журнал (`domain.SecurityKind`): `SecNewDevice = "new_device"`, `SecIdentityLinked`, `SecIdentityUnlinked`,
  `SecPasskeyAdded`, `SecPasskeyRemoved`, `SecPasskeyCloneWarning`, `SecAttestFailed`. `meta` (без ПД, Приложение 2):
  `new_device` — `{platform, model?, browser?, os?}`; `identity_*` — `{provider}`; `passkey_*`, `passkey_clone_warning`
  — `{passkey_id}`; `attest_failed` — `{kind, reason}` (`reason` — из короткого закрытого списка, не текст ошибки).
- `domain.ChallengeTTL` — все значения `auth_challenge_purpose`: `social_nonce` 10 мин, `social_signup` 30 мин,
  `webauthn_register`/`webauthn_login`/`webauthn_mfa`/`webauthn_reauth` 5 мин, `device_attest` 5 мин (спека §3.5,
  §3.6, §3.8); константы `ChallengeSocialNonce` … `ChallengeDeviceAttest`.
- `domain/social.go`: `type SocialFacts struct { Linked, HasEmail, EmailVerified, PrivateRelay, AccountWithEmail,
  AccountHasProvider bool }`; `type SocialBranch int` (`SocialLinked`, `SocialAutoLink`, `SocialSignup`,
  `SocialEmailUnverified`, `SocialEmailMissing`); `func DecideSocial(f SocialFacts) SocialBranch` — порядок §3.5:
  привязан → вход; нет почты → missing; relay Apple → signup; почта не подтверждена → unverified; есть аккаунт с
  почтой: у него уже есть привязка этого провайдера (другой `sub`) → signup (R32), иначе автопривязка; иначе signup.
- `domain/methods.go`: `func CanRemoveMethod(hasPassword bool, identities, passkeys int) bool` — после удаления одного
  остаётся ≥ 1 способ (вызывающий передаёт счёт **до** удаления).
- `domain/passkey_name.go`: `func DefaultPasskeyName(model, browser, os, platform string) string` (R20; ≤ 64 символа,
  никогда не пустое).
- `domain/device_shared.go`: `DeviceSharedThreshold = 3`, `DeviceSharedWindow = 30 * 24 * time.Hour`,
  `PasskeyLimit = 10`.
- `domain/provider_calls.go`: `type ProviderCallKind string` (`apple_revoke`, `devicecheck_update`),
  `ProviderCallTTL = 7 * 24 * time.Hour`, `type AppleRevokePayload struct { ClientID, RefreshToken string }`, `type
  DeviceCheckPayload struct { Token string }` (JSON под кольцом; Task 7 их шифрует).
- `app/mail.go`: `MailNewDevice = "identity.new_device"`, `MailIdentityLinked`, `MailIdentityUnlinked`,
  `MailPasskeyAdded`, `MailPasskeyRemoved` (ref — `security_events.id`); `MailKinds()` — с ними.
- `app/audit.go`: `ActionIdentityLinked = "identity.identity_linked"`, `ActionIdentityUnlinked`, `ActionPasskeyAdded`,
  `ActionPasskeyRemoved`.
- Composer (`identity/mail`): ветка журнала (`event`) знает пять видов; `new_device` — устройство (модель или
  «браузер · ОС» из `meta`, иначе платформа), город (`security_events.ip_city`; нет — вариант `text_no_city`), время
  `created_at` в поясе получателя (`mailZone`, решение пользователя 16.11); `identity_linked/unlinked` — провайдер
  (Apple/Google); `passkey_added/removed` — время, без имени ключа (имя задаёт игрок — может быть ПД).
- Локали: `identity.mail.{new_device,identity_linked,identity_unlinked,passkey_added,passkey_removed}.{subject,text}` +
  `new_device.text_no_city`, `ru` и `en` с теми же ключами; в `new_device` — «это не вы? смените пароль и выйдите
  везде» (§3.7).

**Тесты.**
- `TestEventPayloadsGolden` (+5 golden), `TestEventPayloadsNoPII` (новые события без почты, токенов, `install_id`).
- `TestDecideSocial` — таблица всех веток §3.5 и R32, в том числе «привязан, но нет почты» → вход; «relay и есть
  аккаунт с этой почтой» → signup.
- `TestCanRemoveMethod` — таблица: только пароль; пароль + Apple (удаляем Apple) → да; только Apple → нет; Apple + ключ
  → да; 0 способов → нет.
- `TestDefaultPasskeyName` → модель; без модели — «Chrome · Windows»; ничего — `iOS`/`Android`/`Web`; 100 символов →
  64.
- `TestChallengeTTLAllPurposes` → каждое значение enum `auth_challenge_purpose` из `identitydb` имеет срок.
- Composer: `TestComposeNewDevice` (с городом и без, пояс `users.timezone` → города → UTC), `TestComposeIdentityLinked`,
  `TestComposePasskeyMails`; существующие стражи «Composer на каждый вид `MailKinds()`» и «локали ru/en с одинаковыми
  ключами» — зелёные.

**Подсадка.** В `DecideSocial` проверять подтверждение почты после поиска аккаунта (автопривязка без
`email_verified`) → `TestDecideSocial` красный. Положить почту провайдера в payload `identity_linked` →
`TestEventPayloadsNoPII` красный.

**Приёмка.** Ни одна строка UI не в коде; meta журнала без ПД; `MailKinds()` и Composer совпадают.

**Ловушки.** Composer — только чтение под ролью `worker` (I-2): новые письма читают `security_events`, `users`,
`geo_read_city_settings` — права есть.

**Не трогай.** `app/app.go` и прочие файлы `app` (Task 7, 8).

## Волна 2. Стык: порты, сборка, ядро входа

### Task 7: порты провайдеров, `Deps`, сборка api и воркера, раскладка заглушек

**Цель.** Общая основа волны 3 (как Task 8 плана I-2): порты корня, конструкторы провайдеров из конфига, новые поля
`Deps`/`WorkerDeps`, сборка `cmd/api` и `workerhost`, вид задачи `identity.provider_call`, постановка вызова
провайдера, сигнатуры всех методов волны 3 заглушками в файлах-владельцах, раскладка `pending_i3` по областям.

**Риск:** A (ключевой стык; отказ старта; секреты в аргументах задач).

**Файлы.** Создать: `backend/internal/identity/{providers.go,providers_test.go}`,
`backend/internal/identity/identitytest/fakes.go`, `backend/internal/identity/internal/app/provider_calls.go` (+ тест),
файлы-заглушки `app/{device_trust.go,device_challenge.go,provider_calls_run.go,social.go,social_link.go,passkeys.go,passkey_auth.go}`,
`backend/internal/identity/httpapi/{social.go,passkeys.go,devices.go}`,
`backend/internal/httpapi/public/{pending_social_test.go,pending_passkeys_test.go,pending_devices_test.go}`,
`backend/internal/identity/jobs/provider_calls.go`. Удалить: `httpapi/pending_i3.go`,
`backend/internal/httpapi/public/pending_i3_test.go`. Изменить: `backend/internal/identity/identity.go`,
`app/app.go`, `app/cleanup.go` (одна строка — вызов `a.cleanupProviderCalls`), `jobs/jobs.go`,
`backend/internal/platform/queue/foreign.go` (`WorkerKinds` + `identity.provider_call`),
`backend/internal/httpapi/public/{nostub_test.go,handler_test.go}`, `backend/cmd/api/{main.go,main_test.go}`,
`backend/cmd/internal/workerhost/{workerhost.go,registry.go}` и их тесты, `backend/internal/archtest/boundary_test.go`.

**Интерфейсы (корень `identity`).**
- Ошибки: `ErrProviderInvalid` (отрицательный вердикт или негодный токен — повтор не поможет),
  `ErrProviderUnavailable` (сеть, 5xx). Реализации оборачивают ошибки своих пакетов в них.
- `type SocialClaims struct { Provider, Subject, Email, Audience, Nonce string; HasEmail, EmailVerified, PrivateRelay
  bool }`; `type SocialVerifier interface { Verify(ctx context.Context, provider, rawIDToken string) (SocialClaims,
  error) }`; `func NonceMatches(provider, claim string, stored [32]byte) bool` (обёртка `social.NonceMatches`).
- `type AppleTokens interface { Exchange(ctx context.Context, clientID, code string) (string, error); Revoke(ctx
  context.Context, clientID, refresh string) error }`.
- `type PasskeyRP = webauthn.RP` (псевдоним типа `identity/internal/webauthn`; `nil` — не настроено).
- `type AttestedKey struct { KeyID, PublicKey []byte; AppID string; Counter uint32 }`; `type Attestor interface {
  IOS() bool; Android() bool; VerifyAppAttestation(keyID, attestation, clientDataHash []byte) (AttestedKey, error);
  VerifyAppAssertion(publicKey []byte, appID string, prev uint32, assertion, clientDataHash []byte) (uint32, error);
  VerifyPlayIntegrity(ctx context.Context, token, requestHash string) error }` — **заменяет** порт I-2
  `Check(ctx, DeviceInput) (AttestVerdict, error)`; `AttestOff` и `AttestVerdict` удаляются (режим `off` — R10).
- `type DeviceChecker interface { Query(ctx context.Context, token string) (int, error); Update(ctx context.Context,
  token string, count int) error }`.
- `type AttestMode string` (`AttestModeOff`, `AttestModeObserve`, `AttestModeEnforce`).
- `type ProvidersConfig struct { AppleTeamID, AppleKeyID, AppleKey string; AppleClientIDs, GoogleClientIDs []string;
  RPID, RPName string; Origins []string; AppAttestAppIDs []string; AppleEnv string; PlayPackage, PlayCredentials
  string; HTTP *http.Client; Now func() time.Time }` (поля — из `config.Identity`, перекладывает `cmd/api`).
- `type Providers struct { Social SocialVerifier; Apple AppleTokens; Passkeys *PasskeyRP; Attestor Attestor;
  DeviceCheck DeviceChecker }` + методы `ProviderReadiness`: `Apple()` — клиенты и ключ Apple заданы; `Google()` —
  клиенты Google; `Passkeys()` — RP задан. `func NewProviders(ctx context.Context, cfg ProvidersConfig, log
  *slog.Logger) (*Providers, error)` — разбор ключей (`applekey.Parse`, JSON Play) — ошибка = отказ старта (без
  значений в тексте); пустая группа — поле `nil`/`false` и предупреждение в журнал («способ не настроен — выключен»,
  §11.3); `Social` есть всегда (без client id провайдер отвергается как `ErrProviderInvalid`); `Attestor` — `nil`,
  если не задано ни App Attest, ни Play; `DeviceCheck` — при ключе Apple. HTTP-клиент — один, таймаут 5 с.
- `type WorkerProvidersConfig struct { AppleTeamID, AppleKeyID, AppleKey, AppleEnv string; HTTP *http.Client; Now
  func() time.Time }`; `func NewWorkerProviders(cfg WorkerProvidersConfig, log *slog.Logger) (AppleTokens,
  DeviceChecker, error)` — без ключа оба `nil` и предупреждение.
- `Deps` +: `Social SocialVerifier`, `Apple AppleTokens`, `Passkeys *PasskeyRP`, `DeviceCheck DeviceChecker`,
  `AttestMode AttestMode`; `Attestor` — новый тип; `Providers ProviderReadiness` остаётся. `WorkerDeps` +: `Apple
  AppleTokens`, `DeviceCheck DeviceChecker` (могут быть `nil`).
- `app.New`: `Social` и `AttestMode` (валидный) обязательны; согласованность — `Providers.Apple()` ⇒ `Apple != nil`,
  `Providers.Passkeys()` ⇒ `Passkeys != nil`; иначе `errMissingDeps`. `NoProviders` остаётся (тесты I-2).

**Интерфейсы (`app`).**
- `func (a *App) enqueueProviderCall(ctx context.Context, tx pgx.Tx, kind domain.ProviderCallKind, payload any) error`
  — реализация здесь: `id.New()`, JSON → `Secrets.Seal(…, aad = id)`, `INSERT provider_calls`, `River.InsertTx(ctx,
  tx, ProviderCallArgs{V: 1, Ref: id}, &river.InsertOpts{Queue: maintenance})`. `ProviderCallArgs` (`Kind() =
  "identity.provider_call"`) — в `identity/jobs` или корне по образцу `mail.SendArgs` (исполнитель решает, чтобы `app`
  не импортировал `jobs`).
- Заглушки (тело `errNotImplemented`, кроме отмеченных «пропуск» — они возвращают нулевое значение, чтобы тесты I-2
  оставались зелёными): Task 9 — `CreateDeviceChallenge(ctx) (DeviceChallenge, error)`, `deviceTrust(ctx, c Client, op
  TrustOp) (needHuman bool, err error)` (**пропуск**: `false, nil`), `queueDeviceCheckUpdate(ctx, tx, c Client,
  deviceID uuid.UUID) error` (**пропуск**), `RunProviderCall(ctx, ref uuid.UUID) error`, `cleanupProviderCalls(ctx)
  error` (**пропуск**); Task 10 — `CreateSocialNonce`, `SocialLogin`, `CompleteSocialSignup`, `checkSocialToken`;
  Task 11 — `LinkIdentity`, `UnlinkIdentity`, `verifyReauthSocial`; Task 12 — `StartPasskeyRegistration`,
  `RegisterPasskey`, `RenamePasskey`, `DeletePasskey`, `passkeyUser`; Task 13 — `StartPasskeyLogin`, `PasskeyLogin`,
  `StartMfaPasskey`, `StartReauthPasskey`, `verifyMfaPasskey`, `verifyReauthPasskey`. Сигнатуры — в задачах 9–13;
  `TrustOp` — `TrustSignup | TrustLogin`.
- Типы: `type DeviceChallenge struct { ID uuid.UUID; Challenge string; ExpiresAt time.Time }`; `type WebAuthnOptions
  struct { ChallengeID uuid.UUID; Options json.RawMessage }`; `type SocialNonce struct { ID uuid.UUID; Nonce string;
  ExpiresAt time.Time }`.
- `identity/httpapi/{social,passkeys,devices}.go` — перенос 14 заглушек из `pending_i3.go` по областям (Приложение
  1); `pendingOps = slices.Concat(…, pendingSocial, pendingPasskeys, pendingDevices, pendingI4)`.
- `jobs`: исполнитель `identity.provider_call` → `app.RunProviderCall` (воркер, `app.NewMaintenance` + провайдеры
  воркера: `NewMaintenance` получает их через новый вариант `NewMaintenanceWith(…, apple, dc)` или поля — решает
  исполнитель, без изменения вызовов admin-api); у admin-api — заглушка `queue.AddForeign` по `WorkerKinds`.
- `cmd/api`: `cfg.Identity.Validate()` до базы; `identity.NewProviders` до базы (битый ключ — отказ старта до базы);
  `buildIdentity` берёт `Providers`, `AttestMode` из конфига вместо `AttestOff{}`/`NoProviders{}`. `workerhost.Prepare`:
  `WorkerIdentity.Validate()`, `NewWorkerProviders`; `WorkerDeps.Apple/DeviceCheck`.
- `identitytest/fakes.go`: `FakeAttestor` (вердикты по ручкам, счётчик вызовов), `FakeDeviceCheck` (счётчик по токену,
  режим «недоступно»), `FakeAppleTokens` (запоминает обмены и отзывы, режимы ошибок), `Providers(t, opts…)
  *identity.Providers` — все провайдеры на подделках и тестовых ключах Task 2–4.
- `archtest`: `TestIdentitytestOnlyInTests` — пакет `identity/identitytest` не импортируется ни одним не-тестовым
  файлом (`cmd/**`, `internal/**`).

**Тесты.**
- `TestNewProvidersEmptyGroups` → пустой конфиг → `Apple/Passkeys/Attestor/DeviceCheck = nil`, `Apple() = Google() =
  Passkeys() = false`, предупреждения в журнале без значений.
- `TestNewProvidersBadKey` → `AppleKey` не base64 / Play не JSON → ошибка без содержимого.
- `TestAppNewConsistency` → `Providers.Apple() = true`, `Apple = nil` → `errMissingDeps`; `AttestMode = "x"` → ошибка.
- `TestEnqueueProviderCallInTx` → откат → ни строки, ни задачи; коммит → строка с `payload_enc`, в котором нет байтов
  токена в открытом виде, задача с аргументами ровно `{v, ref}`.
- `TestNoStubOperations`/`TestPendingOpsExpire` — зелёные, в `pendingOps` те же 18 операций (14 + 4).
- `cmd/api`: `TestRunRejectsBadAppleKey`, `TestRunRejectsPartialWebAuthn` — ошибка до базы. `workerhost`:
  `TestPrepareWorkerAppleOptional`; `TestWorkerKindsIncludeProviderCall` (воркер исполняет, admin-api — заглушка).
- `TestIdentitytestOnlyInTests`.

**Подсадка.** Убрать проверку согласованности → `TestAppNewConsistency` красный. Положить токен в аргументы задачи →
`TestEnqueueProviderCallInTx` красный. Импортировать `identitytest` из `cmd/api` → `TestIdentitytestOnlyInTests` красный.

**Приёмка.** Поведение всех 50 операций не изменилось (14 — по-прежнему 501); корень не импортирует `app`; `Deps`
содержит всё для задач 8–13 (дополнять позже — через контроллера); `go vet ./internal/identity/...` зелёный с
заглушками.

**Ловушки.** Новые поля конфига (Task 1) уже есть; проверки `Validate` — здесь. `RemoteKeySet` создаётся с ctx
процесса. Признак «настроен» у `getAuthMethods` — тот же `Providers` (I-2 `AuthMethods` уже его зовёт).

**Не трогай.** `app/{login_core,mfa_login,reauth_op,devices,signup,login,mfa}.go` (Task 8).

### Task 8: ветки I-3 в ядре входа — новое и общее устройство, отложенная привязка, passkey/social в 2FA и свежем входе

**Цель.** Все правки общих файлов ядра I-2 одним владельцем (R27), чтобы волна 3 только реализовала заглушки.

**Риск:** A (обход 2FA, письма, порядок замков, гонка привязки).

**Файлы.** Изменить: `app/{login_core.go,mfa_login.go,reauth_op.go,devices.go,signup.go,login.go,mfa.go}`,
`httpapi/{mfa.go,password.go}`, `queries/devices.sql`; создать `app/{new_device.go,identities.go}`,
`queries/identities.sql` (+ `identitydb/*.sql.go`), тесты.

**Интерфейсы.**
- `PasskeyInput struct { ChallengeID uuid.UUID; Credential []byte }` (сырой JSON `WebAuthnCredential`); `SocialInput
  struct { Provider string; NonceID uuid.UUID; IDToken string }`; `ReauthInput { …; Passkey *PasskeyInput; Social
  *SocialInput }`. `httpapi/mfa.go`, `httpapi/password.go` перекладывают тела полностью.
- `CompleteMfa`: ветка `passkey` — аварийный флаг `auth_passkeys_login` и готовность (`a.providers.Passkeys()`)
  проверяются **до** лимита и попытки (выключено → `403 feature.disabled`, попытка не тратится); затем
  `a.verifyMfaPasskey(ctx, tx, uid, ch.ID, *passkey) error` (Task 13; `errFactorInvalid` — неудача, R25), ответ
  неудачи — `passkey_invalid` + `attempts_left`. Успех — как у TOTP. После `finishLogin` со статусом `authenticated` —
  отложенная привязка из `data_enc` вызова (R16) через `createIdentity`.
- `Reauthenticate`: `passkey` — флаг (403, не неудача) → в tx `a.verifyReauthPasskey(ctx, tx, s, *in.Passkey)` (Task
  13); успех ставит `mfa_at = now` и `cancelResetsByOwner` (R24). `social` — до tx: 2FA включена → `422
  reauth_method_not_allowed` без сети и без неудачи (как пароль); флаг `auth_<p>_login` и готовность (403); `claims, err
  := a.checkSocialToken(ctx, *in.Social)` (Task 10; `ErrProviderInvalid` → неудача свежего входа, недоступность →
  500); в tx — `a.verifyReauthSocial(ctx, tx, s.UserID, claims)` (Task 11). Неудача любой ветки — счётчик сессии (I-2
  R16).
- `mfaStep`: `passkey` в `methods` — при ключах, флаге **и** `providers.Passkeys()`.
- `finishLoginAs(…, link *PendingLink)` (новый параметр; `finishLogin` передаёт `nil`; добавить `finishLoginLinked(ctx,
  tx, u, c, deviceID, method, link *PendingLink)` для соцвхода): при `mfa_required` и `link != nil` — `link`
  шифруется в `auth_challenges.data_enc` вызова `mfa` (aad — id вызова; `newChallenge` получает необязательный
  шифруемый хвост); при `deletion_pending` — привязки нет (R16); при `authenticated` — `createIdentity` в той же tx.
- `type PendingLink struct { Provider string; Subject, Email string; PrivateRelay bool; AppleBlob []byte /* открытый
  {client_id, refresh_token} или nil */ }` (в `identities.go`).
- `func (a *App) createIdentity(ctx, tx pgx.Tx, userID uuid.UUID, l PendingLink, quiet bool) (IdentityView, error)` —
  `INSERT user_identities … ON CONFLICT DO NOTHING RETURNING` (без 23505, обрывающего tx); не вставилось → перечитать:
  `(provider, subject)` у другого → `errIdentityTaken` (409 `identity_taken`), у аккаунта уже есть провайдер →
  `errIdentityExists` (409 `identity_exists`); Apple-blob → `Seal(aad = id привязки)`; `quiet = false` →
  `security_events (identity_linked)` + письмо + событие + аудит + `users.version + 1`; `quiet = true` (дорегистрация)
  — без письма и события `identity_linked` (R33).
- Новое устройство (`new_device.go`): `func (a *App) noteDevice(ctx, tx, userID, deviceID, sessionID uuid.UUID, c
  Client, signup bool) error` — зовётся в `finishLoginAs` **до** `linkDevice`: `deviceID ≠ Nil`, пары `(device, user)`
  нет, не регистрация, у аккаунта есть другие пары → `security_events (new_device)` с `meta` (Task 6), письмо
  `new_device`, `risk.Record(NewDevice, userID)` через оценщик на `a.limiterTx(tx)` (R13, R14 — без ворот). После
  `linkDevice` новой пары: различных аккаунтов устройства с `last_seen_at > now − 30 д` больше
  `DeviceSharedThreshold` → событие `identity.device_shared` (R15).
- `deviceTrust` (Task 9) встраивается: `signupHuman` зовёт `a.deviceTrust(ctx, c, TrustSignup)` вместо порта I-2 и
  объединяет `needHuman` с риском подсети в **один** `humancheck.Require`; `Login` — `a.deviceTrust(ctx, c,
  TrustLogin)` после лимитов, объединение с `risk.LoginFailed` так же. `ConfirmSignup` после `countSignup` —
  `a.queueDeviceCheckUpdate(ctx, tx, c, dev)`.
- `signup.go`: создание аккаунта из `ConfirmSignup` выносится без изменения поведения в `func (a *App)
  createAccount(ctx, tx, q, n NewAccount) (userRow, error)` (`NewAccount{Email, Nickname, BirthDate, CityID, Locale,
  PasswordHash *string, Accepted, Client}`; без пароля — без строки `credentials`) — его зовёт Task 10.

**Тесты.**
- `TestFinishLoginNewDeviceMail` — таблица: второе устройство → журнал `new_device` + письмо; то же устройство →
  ничего; первое устройство регистрации → ничего; веб без метки BFF → ничего; `meta` без `install_id`.
- `TestNewDeviceRiskRecorded` → сигнал `NewDevice` по аккаунту учтён в tx (откат — не учтён).
- `TestDeviceSharedEvent` → 4-й аккаунт за 30 дней → событие `accounts_30d = 4`; 5-й → ещё одно; пары старше 30 дней не
  считаются; повторный вход того же аккаунта → события нет.
- `TestMfaStepCarriesLink` → соцвход с 2FA: `data_enc` есть, `sub` и почты нет ни в `data`, ни в открытых байтах
  `data_enc`.
- `TestCompleteMfaCreatesPendingLink` → TOTP верен → привязка, письмо, событие; пока шёл шаг 2FA `sub` привязали к
  другому → вход проходит, привязки нет, транзакция не оборвана.
- `TestCreateIdentityConflicts` → `identity_taken`, `identity_exists`, успех (событие, письмо, аудит, версия);
  `quiet` — без письма и события.
- `TestCompleteMfaPasskeyFlagOff` → аварийный флаг выключен → 403 `feature.disabled`, `attempts` токена не изменились.
- `TestReauthSocialWith2FANotAllowed` → 422, `FakeVerifier` не вызван, счётчик сессии не изменился.
- `TestReauthPasskeyFlagOff` → 403, неудачей не считается.
- `TestSignupSingleHumancheck` → `deviceTrust` (через хук теста) и риск подсети оба требуют PoW → один вызов
  `Require`.
- Стражи I-2 (`TestIssueSessionOnlyInFinishLogin`, `TestLockOrderNoDeadlock`, `TestStartSignupOrder`) — зелёные.

**Подсадка.** Слать `new_device` и на регистрации → `TestFinishLoginNewDeviceMail` красный. Считать пары за всё время →
`TestDeviceSharedEvent` красный. Класть `PendingLink` в `data` открыто → `TestMfaStepCarriesLink` красный. Обычный
`INSERT` без `ON CONFLICT` → `TestCompleteMfaCreatesPendingLink` красный (25P02/23505).

**Приёмка.** Порядок `finishLogin` I-2 R6 не изменился; ни одной сессии в обход `finishLoginAs`; все 32 операции I-2
ведут себя как раньше (полный прогон `internal/identity/...` и `internal/httpapi/public/...`).

**Ловушки.** `noteDevice` — до `linkDevice` (иначе пара уже есть). `risk.Assessor` на пуле внутри tx — запрещено (C1
волны 4 I-2). `data_enc` вызова шифруется кольцом `Secrets` (у api оно есть).

**Не трогай.** Файлы-заглушки волны 3 (`app/{device_trust,social,social_link,passkeys,passkey_auth,provider_calls_run}.go`).

## Волна 3. Сценарии

### Task 9: целостность устройства в сценариях, DeviceCheck, исполнитель `provider_calls`

**Цель.** `createDeviceChallenge`, `deviceTrust` по режимам §3.8/§9.5, ворота DeviceCheck на регистрации,
обновление битов после регистрации и исполнитель очереди вызовов провайдеров (отзыв Apple и DeviceCheck).

**Риск:** A (одноразовость задачи, счётчик, режимы, недоступность провайдера).

**Файлы.** Реализовать заглушки: `app/{device_trust.go,device_challenge.go,provider_calls_run.go}`; создать
`queries/{device_trust.sql,provider_calls.sql}`, `jobs/provider_calls.go` (тело), тесты; `httpapi/devices.go`
(`CreateDeviceChallenge`), `pending_devices_test.go` (операция уходит).

**Интерфейсы.**
- `CreateDeviceChallenge(ctx) (DeviceChallenge, error)` — 32 байта CSPRNG → `challenge` (base64url), строка
  `auth_challenges` (`purpose = device_attest`, `token_hash` NULL, `data = {"challenge": …}`, 5 мин); ответ `201`,
  `no-store`. Работает при любом режиме (клиент не знает режим).
- `deviceTrust(ctx, c Client, op TrustOp) (needHuman bool, err error)`:
  1. веб, режим `off`, платформа без проверки (`Attestor == nil` или `!IOS()`/`!Android()`) → `false, nil`;
  2. `integrity` нет → «не прошла»;
  3. задача — чтение по `challenge_id` пулом (не погашена, `now < expires_at`, `purpose = device_attest`), вид
     сверяется с платформой (`app_attest_*` — iOS, `play_integrity` — Android);
  4. проверка вне tx: аттестация — `VerifyAppAttestation(keyID, payload, SHA-256(challenge))`; утверждение — ключ и
     счётчик строки устройства (по HMAC `install_id` всеми ключами кольца и `attest_key_id = keyID`) → `VerifyAppAssertion`;
     Play — `VerifyPlayIntegrity(token, base64url(SHA-256(challenge)))`;
  5. короткая tx (`devices` → `auth_challenges`, без `users`, R18): аттестация — `upsertDevice` и запись
     `attest_key_id`, `attest_public_key`, `attest_counter = 0`, `attested_at` (конфликт `attest_key_id` с другим
     устройством → «не прошла»); утверждение — `FOR UPDATE` строки устройства и условная запись `attest_counter = new
     WHERE attest_counter < new`; гашение задачи условным `UPDATE … WHERE consumed_at IS NULL AND expires_at > now`
     (0 строк → «не прошла»);
  6. «не прошла» → отдельной tx `security_events (attest_failed)` без `user_id` (`meta {kind, reason}`); `observe` →
     `true, nil`; `enforce` → `403 identity.device_untrusted`. `ErrProviderUnavailable` → `true, nil` в любом режиме +
     журнал (§9.5, R30);
  7. `op = TrustSignup`, iOS, `DeviceCheck != nil`: нет `device_check_token` → как «не прошла» (по режиму); `Query` → `≥ 2`
     → `403 identity.device_signup_limit`; недоступно → `needHuman`.
- `queueDeviceCheckUpdate(ctx, tx, c, deviceID)` — iOS, `DeviceCheck != nil`, есть `device_check_token` →
  `enqueueProviderCall(devicecheck_update, DeviceCheckPayload{Token})`; иначе ничего (R12).
- `RunProviderCall(ctx, ref uuid.UUID) error` (воркер): строки нет → `nil`; `Open` (aad — `ref`): `keys.ErrUnknownKey`
  → ошибка (повтор), `keys.ErrDecrypt` → удалить строку, журнал, `nil`; `apple_revoke` → `Apple.Revoke(clientID,
  token)`; `devicecheck_update` → `Query` → `Update(min(3, n + 1))`; успех или `ErrProviderInvalid` → удалить строку
  (`ErrProviderInvalid` — журнал без токена); провайдер не настроен у воркера или `ErrProviderUnavailable` → ошибка
  (River повторит; после исчерпания — видимая «мёртвая» задача, строку удалит чистка).
- `cleanupProviderCalls(ctx)` — строки старше `ProviderCallTTL` (граница строгая) удаляются одним оператором.

**Тесты.**
- `TestCreateDeviceChallenge` → строка `device_attest`, 5 мин, 32 байта, без `user_id`.
- `TestDeviceTrustModes` — таблица режим × данные (нет, годные, негодные, провайдер недоступен) × платформа (ios,
  android, web) → `needHuman`/403/ок; `attest_failed` — ровно на «не прошла».
- `TestAppAttestFlow` → аттестация пишет ключ; утверждение со счётчиком 1, затем 2 → ок; повтор счётчика → «не
  прошла»; утверждение ключом другого устройства → «не прошла».
- `TestDeviceTrustChallengeOneTime` → два параллельных предъявления одной задачи → ровно одно прошло; истёкшая задача →
  «не прошла».
- `TestPlayRequestHash` → `FakeAttestor` получил `base64url(SHA-256(challenge))`.
- `TestDeviceCheckGate` → счётчик Apple 2 → 403 `device_signup_limit`; недоступно → `needHuman`; нет токена в `enforce`
  → 403 `device_untrusted`.
- `TestQueueDeviceCheckUpdate` → iOS с токеном → строка и задача; Android, веб, без токена → ничего.
- `TestRunProviderCall` — таблица: отзыв успех → строки нет; `ErrProviderInvalid` → нет; недоступно → ошибка, строка
  есть; ключ вне кольца → ошибка; повреждённый шифротекст → строки нет, `nil`; строки нет → `nil`; Apple не настроен →
  ошибка.
- `TestRunProviderCallDeviceCheckSaturates` → было 3 → `Update(3)`.
- `TestCleanupProviderCalls` → ровно 7 дней — остаётся, старше — удалена.
- HTTP: `createDeviceChallenge` → 201, `no-store`, без `Idempotency-Key`.

**Подсадка.** Гасить задачу без условия `consumed_at IS NULL` → `TestDeviceTrustChallengeOneTime` красный.
Разрешить равный счётчик в записи → `TestAppAttestFlow` красный. Отказывать в `enforce` при недоступности провайдера →
`TestDeviceTrustModes` красный.

**Приёмка.** Сеть — вне tx; в tx аттестации нет `users`; журнал без токенов; исполнитель — только у воркера.

**Ловушки.** Play Integrity — стандартный запрос: `requestHash` клиент передаёт как строку; формат согласован в
описании `DeviceIntegrity.payload` контракта. Встроенный воркер api (стенд) берёт ключ Apple из `WORKER_*` (R28).

**Не трогай.** `app/{social*,passkey*}.go`, общие файлы Task 7/8.

### Task 10: соцвход — nonce, вход, дорегистрация

**Цель.** `createSocialNonce`, `socialLogin` (ветки §3.5 через `domain.DecideSocial`), `completeSocialSignup`; общий
`checkSocialToken` для Task 11 и свежего входа.

**Риск:** A (pre-account takeover, обход 2FA, nonce, гонки привязки).

**Файлы.** Реализовать `app/social.go`; создать `queries/social.sql`, тесты; `httpapi/social.go` (3 операции),
`pending_social_test.go`.

**Интерфейсы.**
- `CreateSocialNonce(ctx, provider string) (SocialNonce, error)` — включён основной или аварийный флаг провайдера и
  провайдер готов (Google — с учётом `Requires`), иначе `403 feature.disabled`; 32 байта → `nonce` (base64url);
  строка `social_nonce`: `token_hash = SHA-256(nonce)`, `data = {"provider"}`, 10 мин; `201`, `no-store`.
- `checkSocialToken(ctx, in SocialInput) (identity.SocialClaims, error)` — `Social.Verify` вне tx
  (`ErrProviderInvalid` → `400 identity.social_token_invalid`; `ErrProviderUnavailable` → 500 `internal`, R7);
  гашение nonce **отдельной короткой tx** (R6): строка по `id` и `purpose` `FOR UPDATE`; нет, погашена, истекла,
  провайдер не тот или `NonceMatches` ложь → гашение (если строка есть) и `social_token_invalid`; почта токена —
  `emailaddr.Normalize` (не нормализуется → как «нет почты»).
- `SocialLogin(ctx, in SocialLoginInput{Provider string; NonceID uuid.UUID; IDToken, AppleCode string}, c Client)
  (AuthOutcome, error)`. Порядок: `requireDevice` → `checkSocialToken` → факты пулом (привязка `(provider, sub)` и её
  аккаунт; аккаунт по почте; есть ли у него привязка провайдера) → `DecideSocial` → флаги по ветке (R17) → `422`
  веток unverified/missing → `deviceTrust(TrustLogin)` + риск `LoginFailed` по IP → один PoW → обмен кода Apple вне tx,
  если провайдер Apple, код есть и токена у привязки нет (R29; `client_id` — `claims.Audience`; сбой — журнал,
  продолжаем) → tx:
  - вход привязанного: `lockUser` аккаунта → перечитать привязку `FOR UPDATE` (после `users`, R18) — не та или нет →
    повтор решения один раз (факты заново, nonce уже погашен), второй раз → 500 с журналом; `last_login_at`, токен Apple
    (если получен и не было); `finishLogin(method apple|google, mfaDone=false)`;
  - автопривязка: `lockUser` аккаунта по почте → перепроверка почты и отсутствия привязки → `finishLoginLinked(…,
    &PendingLink{…})` (Task 8: без 2FA — привязка сразу, с 2FA — в `mfa_token`, `deletion_pending` — без привязки);
  - signup: `auth_challenges (social_signup)`: `token_hash = SHA-256(signup_token)`, `data_enc` — `{provider, sub,
    email, private_relay, apple_blob?}` (aad — id), 30 мин → `200 AuthResult signup_required {signup_token,
    expires_at, email, email_is_private_relay}`.
  `banned` → `403 account_blocked` (через `finishLogin`); удалённый аккаунт привязки — привязок у него нет (§3.11).
- `CompleteSocialSignup(ctx, in SocialSignupInput{Token string; CityID uuid.UUID; BirthDate time.Time; Nickname string;
  Accepted []AcceptedDocument; Locale string}, c Client) (AuthOutcome, error)`. Порядок: `requireDevice` → токен пулом
  (нет/истёк/погашен → `410 signup_token_invalid`) → основной флаг провайдера из данных токена → форма и документы →
  город → возраст → ник → лимит устройства → `deviceTrust(TrustSignup)` + риск `Signup` → один PoW → tx: строка токена
  (без `users`) `FOR UPDATE`, перепроверка → `createAccount` (без пароля) — 23505 почты → `409 email_taken` (токен
  гасится), ника → `409 nickname_taken` (не гасится) → согласия → `createIdentity(quiet = true)` (R33) → устройство,
  `countSignup`, `queueDeviceCheckUpdate` → `finishLoginAs(journalSignup)` → событие `user_registered {method:
  apple|google}`, аудит → гашение токена → `201`.
- Ошибки `ErrProviderUnavailable` обмена Apple не влияют на ответ.

**Тесты.**
- `TestCreateSocialNonce` → оба флага выключены → 403; один из двух → 201; провайдер не готов → 403; в базе нет
  сырого nonce.
- `TestCheckSocialTokenNonce` → повтор nonce → `social_token_invalid`; nonce другого провайдера → то же; истёк → то же;
  неверный `nonce` в токене гасит строку; JWKS недоступен → 500, nonce не погашен.
- `TestSocialLoginBranches` — таблица §3.5 (привязан; автопривязка; relay → signup; unverified 422; missing 422; нет
  аккаунта → signup; аккаунт с другой привязкой провайдера → signup, R32) с ожидаемыми записями (привязка, письмо
  `identity_linked`, событие).
- `TestSocialLoginNo2FABypass` → привязанный аккаунт с 2FA → `mfa_required`, сессии нет; автопривязка с 2FA →
  `mfa_required`, привязки нет до `completeMfa`.
- `TestSocialAutoLinkRequiresVerified` → `email_verified = "false"` при существующем аккаунте → 422, привязки нет.
- `TestSocialLoginFlags` — матрица R17 (основной выключен: привязанный входит, автопривязка и signup 403; аварийный
  выключен: привязанный 403).
- `TestSocialLoginStates` → `banned` → 403 `account_blocked` с `blocked_until`; `pending_deletion` → `deletion_pending`
  без привязки.
- `TestSocialLoginAppleExchange` → код обменян, blob записан под кольцом; обмен не удался → вход прошёл, токена нет;
  у привязки токен уже есть → обмена нет.
- `TestCompleteSocialSignup` → успех: аккаунт без `credentials`, привязка без письма, `user_registered.method = apple`,
  201; токен повторно → 410; почту заняли → 409 `email_taken`; ник занят → 409, токен жив; `too_young`,
  `city_not_found`, `documents_outdated`, `device_signup_limit`, `device_required`; флаг выключен → 403.
- `TestCompleteSocialSignupRace` → две параллельные дорегистрации одним токеном → один 201, второй 410.
- HTTP: три операции — успех, `no-store`, без `Idempotency-Key`, по одному коду.

**Подсадка.** Автопривязка без проверки `email_verified` → `TestSocialAutoLinkRequiresVerified` красный. Выдать сессию
при 2FA соцвходу → `TestSocialLoginNo2FABypass` красный. Гасить nonce в tx сценария после `users` →
`TestCheckSocialTokenNonce` или стражи замков красные (описать в отчёте).

**Приёмка.** Ответ «аккаунт найден» до успешного входа невозможен; сырой nonce, `id_token`, код Apple — нигде в базе
и журнале; `signup_token` — только SHA-256.

**Ловушки.** Почта Apple со скрытым адресом — `privaterelay.appleid.com`, канонизация `emailaddr` её не ломает.
`aud` Apple — bundle id или Services ID: обмен для Services ID без `redirect_uri` вернёт `invalid_grant` (Q5).

**Не трогай.** `app/social_link.go`, `anonymize.go`, `cleanup.go` (Task 11, тот же исполнитель — после).

### Task 11: привязка и отвязка, отзыв Apple, свежий вход соцвходом

**Цель.** `linkIdentity`, `unlinkIdentity` (последний способ — R19), перенос токена Apple в `provider_calls` при
отвязке, анонимизации и сверке удаления, ветка `social` свежего входа.

**Риск:** A (последний способ под гонкой, потеря токена отзыва, IDOR).

**Файлы.** Реализовать `app/social_link.go`; изменить `app/{anonymize.go,cleanup.go}`, `queries/{social_link.sql,deletion.sql,cleanup.sql}`;
`httpapi/social.go` (2 операции), `pending_social_test.go`; тесты.

**Интерфейсы.**
- `LinkIdentity(ctx, sessionID uuid.UUID, in SocialLinkInput{SocialInput; AppleCode string}) (IdentityView, error)` —
  свежесть пулом до сети (нет → `403 reauth_required`, nonce не тратится) → основной флаг и готовность → 
  `checkSocialToken` → обмен Apple (R29) → tx: `requireFresh` (окончательно) → `lockUser` → `createIdentity(quiet =
  false)` → `201 LinkedIdentity`, `private, no-store` по контракту. Требований к почте нет (§3.5).
- `UnlinkIdentity(ctx, sessionID, identityID uuid.UUID) error` — tx: `requireFresh` → `lockUser` → строка привязки
  аккаунта `FOR UPDATE` (чужая или нет → `404 identity_not_found`) → счёт способов под замком,
  `domain.CanRemoveMethod` → `409 last_method` → удаление; токен Apple: `Open(aad = id привязки)` → `enqueueProviderCall(
  apple_revoke, …)` (новое aad — id строки вызова); журнал `identity_unlinked`, письмо, событие, аудит, версия;
  `204`. Флаги не проверяются (§4.3).
- `verifyReauthSocial(ctx, tx, userID uuid.UUID, c identity.SocialClaims) error` — привязка `(provider, sub)` этого
  аккаунта есть → `nil` (и `last_login_at`), иначе `errFactorInvalid`.
- Анонимизация (`anonymize.go`) и сверка удаления (`cleanup.go`, `CleanupDeletedIdentities`): **до** удаления
  привязок — выбрать привязки Apple с токеном, перенести каждую в `provider_calls` (воркер: кольцо есть, права
  `SELECT` на `user_identities` и `INSERT` на `provider_calls` — Task 5). `keys.ErrUnknownKey` → ошибка аккаунта (его
  `SAVEPOINT` откатывается, следующий прогон повторит); `keys.ErrDecrypt` → журнал, привязка удаляется без отзыва.

**Тесты.**
- `TestLinkIdentity` → 201, письмо, событие, аудит; без свежести → 403 и nonce жив; флаг выключен → 403; `sub` у
  другого → 409 `identity_taken`; провайдер уже привязан → 409 `identity_exists`; Apple: blob записан; обмен не удался
  → привязка без токена.
- `TestUnlinkIdentity` → 204, письмо и событие; чужая → 404; последний способ (без пароля и ключей) → 409; флаги
  выключены → работает; Apple с токеном → строка `provider_calls` и задача, в аргументах только `ref`.
- `TestUnlinkConcurrentLastMethod` → аккаунт без пароля с Apple и Google; две параллельные отвязки → одна 204, вторая
  409 `last_method`.
- `TestReauthSocial` → привязанный `sub` → `reauth_until`; чужой `sub` → `reauth_invalid` + `attempts_left`.
- `TestAnonymizeQueuesAppleRevoke` → удалённый по сроку аккаунт с Apple → привязки нет, строка `provider_calls` есть,
  её расшифровка даёт `{client_id, refresh_token}`.
- `TestAnonymizeRevokeUnknownKey` → ключ привязки вне кольца воркера → аккаунт не анонимизирован в этом прогоне, прочие
  — да.
- `TestCleanupReconcileQueuesRevoke` → `deleted` с недочищенной привязкой Apple → перенос и удаление.
- HTTP: IDOR `unlinkIdentity` чужой привязки → 404.

**Подсадка.** Считать способы до `lockUser` → `TestUnlinkConcurrentLastMethod` красный. Удалять привязки до переноса
токена → `TestAnonymizeQueuesAppleRevoke` красный.

**Приёмка.** Ни один путь удаления привязки Apple с токеном не теряет токен молча (кроме нечитаемого — с журналом);
права воркера не расширены сверх Task 5.

**Ловушки.** `anonymize` идёт под ролью `worker` — тесты под ней (`roleApp`). Порядок замков: `users` → `user_identities`
(R18).

**Не трогай.** `app/{passkeys,passkey_auth,device_trust}.go`.

### Task 12: ключи доступа — добавление, переименование, удаление

**Цель.** `startPasskeyRegistration`, `registerPasskey`, `renamePasskey`, `deletePasskey` (§3.6) и общий адаптер
аккаунта для WebAuthn, которым пользуется Task 13.

**Риск:** A (привязка задачи к сессии, лимит, последний способ).

**Файлы.** Реализовать `app/passkeys.go`; создать `queries/passkeys.sql`, тесты; `httpapi/passkeys.go` (4 операции),
`pending_passkeys_test.go`.

**Интерфейсы.**
- `passkeyUser(ctx, q *identitydb.Queries, u userRow) (webauthn.User, error)` — handle, ник, ключи аккаунта с флагами
  BE/BS и счётчиком.
- `StartPasskeyRegistration(ctx, sessionID uuid.UUID) (WebAuthnOptions, error)` — основной флаг `auth_passkeys` и
  готовность (403) → tx: `requireFresh` → `lockUser` → ключей ≥ `PasskeyLimit` → `409 passkey_limit` →
  `webauthn_handle` — 32 случайных байта, если `NULL` (один раз за жизнь аккаунта) → `BeginRegistration` → строка
  `webauthn_register` (`user_id`, `session_id`, `data` = сессия, 5 мин) → `200`, `no-store`.
- `RegisterPasskey(ctx, sessionID uuid.UUID, in RegisterPasskeyInput{ChallengeID uuid.UUID; Credential []byte; Name
  string}) (PasskeyView, error)` — флаг (403) → `inTxCommitFailure`: строка сессии приёмом `lockByRow` (`users` первым)
  → задача по `id` `FOR UPDATE`: `purpose`, `session_id` = эта сессия, `user_id` = её аккаунт, не погашена, не истекла
  — иначе `400 passkey_invalid`; задача гасится при любом исходе (коммит) → `FinishRegistration` (`ErrInvalid` → 400) →
  ключей уже ≥ 10 (гонка двух задач) → `400 passkey_invalid` (R34, Q6) → `INSERT … ON CONFLICT (credential_id) DO
  NOTHING` → конфликт → `409 passkey_exists` → имя: из тела или `DefaultPasskeyName` (модель устройства сессии → UA →
  платформа, R20) → журнал `passkey_added`, письмо, событие, аудит, версия → `201 Passkey`. Свежесть не проверяется
  (контракт: «проверила задача»).
- `RenamePasskey(ctx, userID, passkeyID uuid.UUID, name string) (PasskeyView, error)` — `lockUser` → ключ аккаунта
  (чужой/нет → `404 passkey_not_found`) → имя (обрезка пробелов; пустое после обрезки — `400 validation.failed` по
  `body.name`) → `200`.
- `DeletePasskey(ctx, sessionID, passkeyID uuid.UUID) error` — `requireFresh` → `lockUser` → ключ аккаунта `FOR
  UPDATE` (404) → `CanRemoveMethod` → `409 last_method` → удаление → журнал `passkey_removed`, письмо, событие, аудит
  → `204`; флаги не проверяются.

**Тесты.**
- `TestStartPasskeyRegistration` → без свежести 403; флаг выключен 403; RP не настроен 403; 10 ключей → 409; handle
  создан один раз и стабилен между вызовами; `excludeCredentials` — ключи аккаунта; `user.name` — ник.
- `TestRegisterPasskey` → `identitytest.Authenticator` → 201: строка с COSE-ключом, `aaguid`, флагами, транспортами,
  именем по умолчанию; письмо, событие, аудит.
- `TestRegisterPasskeyRejects` → задача другой сессии, погашенная, истёкшая (`clock`), чужой origin, без UV → 400 и
  задача погашена; повтор того же ключа → 409 `passkey_exists`; флаг выключен → 403.
- `TestRegisterPasskeyLimitRace` → 9 ключей, две задачи, две регистрации параллельно → одна 201, вторая 400; ключей 10.
- `TestRenamePasskey` → ок; чужой → 404.
- `TestDeletePasskey` → ок; без свежести 403; последний способ → 409; чужой → 404; флаги выключены → работает.
- HTTP: IDOR `renamePasskey`/`deletePasskey` чужого ключа → 404; `Cache-Control` по контракту.

**Подсадка.** Не сверять `session_id` задачи → `TestRegisterPasskeyRejects` красный. Убрать перепроверку лимита в
регистрации → `TestRegisterPasskeyLimitRace` красный.

**Приёмка.** `user.id` — `webauthn_handle`, не id аккаунта; аттестация `none`; имя ключа не в журнале и событиях.

**Ловушки.** Анонимизация обнуляет `webauthn_handle` (I-2) — новых задач у удалённых нет. Порядок замков: `users` →
`sessions` → `passkeys` → `auth_challenges` (R18).

**Не трогай.** `app/passkey_auth.go` (Task 13 — тот же исполнитель, после).

### Task 13: вход ключом, второй фактор и свежий вход ключом

**Цель.** `startPasskeyLogin`, `passkeyLogin`, `startMfaPasskey` + ветка `completeMfa`, `startReauthPasskey` + ветка
`reauthenticate` (§3.3, §3.6, §3.13).

**Риск:** A (обход 2FA, чужой ключ на шаге 2FA, повтор задачи, клон).

**Файлы.** Реализовать `app/passkey_auth.go`; создать `queries/passkey_auth.sql`, тесты; `httpapi/passkeys.go`
(4 операции), `pending_passkeys_test.go`.

**Интерфейсы.**
- `StartPasskeyLogin(ctx) (WebAuthnOptions, error)` — аварийный флаг `auth_passkeys_login` и готовность (403) →
  `BeginDiscoverableLogin` → строка `webauthn_login` без аккаунта, 5 мин → `200`, `no-store`.
- `PasskeyLogin(ctx, in PasskeyInput, c Client) (AuthOutcome, error)` — `requireDevice` → флаг → риск `LoginFailed`
  по IP + `deviceTrust(TrustLogin)` → один PoW → `ParseAssertionIDs` (мусор → гашение задачи, 400) →
  `inTxCommitFailure`: строка `passkeys` по `credential_id` без замка → `lockUser` → `FOR UPDATE` ключа с перепроверкой
  `user_id` (`lockByRow`, R18) → задача `webauthn_login` `FOR UPDATE`, гашение при любом исходе → `userHandle` =
  `users.webauthn_handle` → `FinishDiscoverableLogin` → неудача: коммит гашения, `risk.Record(LoginFailed, IP)` после
  коммита, `400 passkey_invalid`; успех: `sign_count`, `backup_state`, `last_used_at`; `CloneWarning` → журнал
  `passkey_clone_warning` → `finishLogin(method passkey, mfaDone = true)` (R24): `banned` → 403 `account_blocked`,
  `deleted`/`purgeDue` → 400 `passkey_invalid`, `pending_deletion` → `deletion_pending`. Неизвестный ключ — гашение
  задачи отдельной короткой tx и тот же 400 (время и ответ как у неверной подписи).
- `StartMfaPasskey(ctx, mfaToken string) (WebAuthnOptions, error)` — флаг (403) → вызов `mfa` по SHA-256 пулом (нет,
  истёк, погашен, попытки исчерпаны → `410 mfa_expired`; попытка не тратится) → ключей нет → `409 passkey_none` →
  `BeginLogin(ключи аккаунта)` → строка `webauthn_mfa` (`user_id`, `data = {session, mfa_challenge_id}`) → `200`.
- `verifyMfaPasskey(ctx, tx, userID, mfaChallengeID uuid.UUID, p PasskeyInput) error` — задача `webauthn_mfa` `FOR
  UPDATE` (после строки `mfa`, R18): `user_id` и `mfa_challenge_id` совпадают, не погашена, не истекла; гашение;
  `FinishLogin` с ключами **этого** аккаунта; обновление ключа как у входа; любая неудача — `errFactorInvalid` (R25).
- `StartReauthPasskey(ctx, sessionID uuid.UUID) (WebAuthnOptions, error)` — флаг (403) → ключей нет → 409
  `passkey_none` → строка `webauthn_reauth` (`user_id`, `session_id`) → `200`, `no-store`.
- `verifyReauthPasskey(ctx, tx, s sessionRow, p PasskeyInput) error` — задача `webauthn_reauth` этой сессии; как выше;
  неудача — `errFactorInvalid`.

**Тесты.**
- `TestStartPasskeyLogin` → флаг выключен 403; `allowCredentials` пуст; `userVerification = required`.
- `TestPasskeyLogin` → `authenticated`, `sessions.auth_method = passkey`, `mfa_at` задан; аккаунт с 2FA — без
  `mfa_required`; живой сброс 2FA поддержкой отменён; `pending_deletion` → `deletion_pending`; `banned` → 403 с
  `blocked_until`; счётчик и `last_used_at` обновлены; `device_required` 422; флаг выключен 403.
- `TestPasskeyLoginRejects` → неизвестный ключ, повтор задачи, чужой origin/rpId, без UV, `userHandle` другого аккаунта,
  подпись чужим ключом → одинаковый 400 `passkey_invalid`, задача погашена.
- `TestPasskeyLoginCloneWarning` → счётчик уменьшился → вход прошёл, журнал `passkey_clone_warning`.
- `TestPasskeyLoginConcurrentSameChallenge` → два параллельных предъявления → один успех.
- `TestStartMfaPasskey` → токен истёк 410; ключей нет 409; `allowCredentials` — только ключи аккаунта.
- `TestCompleteMfaPasskey` → успех → сессия, `mfa_at`; ключ другого аккаунта → `passkey_invalid` + `attempts_left`;
  задача от другого `mfa_token` → то же; пятая неудача → 410; неудачи учтены в `user:mfa`.
- `TestReauthPasskey` → `reauth_until`, `mfa_at` → `disableTotp` после этого разрешён (I-2 R27); задача другой сессии
  → `reauth_invalid`; флаг выключен → 403 (из Task 8).
- HTTP: 4 операции — успех, `no-store`, анонимные без `Idempotency-Key`, `startReauthPasskey` с ним.

**Подсадка.** Не сверять `userHandle` → `TestPasskeyLoginRejects` красный. Принять ключ другого аккаунта на шаге 2FA
(`FinishLogin` с ключами из задачи, а не аккаунта) → `TestCompleteMfaPasskey` красный. `mfaDone = false` у входа ключом
→ `TestPasskeyLogin` (2FA) красный.

**Приёмка.** Ключ закрывает 2FA только как фактор этого аккаунта; ответы неизвестного и неверного ключа одинаковы;
задачи одноразовые.

**Ловушки.** Сессия задачи — `data` (`jsonb`) с нулевым `Expires` (R5); срок — `expires_at` по `clock`. Задача
`webauthn_login` без аккаунта — гашение до `lockUser` нельзя (порядок), поэтому при известном ключе — после
`lockByRow`, при неизвестном — отдельной tx.

**Не трогай.** `app/{social*,device_trust}.go`, общие файлы Task 7/8.

## Волна 4. Закрытие

### Task 14: сквозные стражи и документы I-3

**Цель.** Стражи на собранном сервере для всех 46 операций identity public, проверка матрицы флагов и аттестации
поперёк операций, гонка последнего способа разными видами, документы этапа.

**Риск:** B.

**Файлы.** Изменить: `backend/internal/httpapi/public/{identity_core_test.go,nostub_test.go,handler_test.go}`; создать
`backend/internal/httpapi/public/identity_i3_test.go`; `backend/go.mod`/`go.sum` (`go mod tidy` — только снятие
`// indirect`; иное — стоп); документы: `.claude/rules/backend-identity.md`, `docs/01-домен-и-правила.md`, спека
identity (новый §0.2 «Поправки плана I-3» — R этого плана; §5.3 `provider_calls`; §11.1 без `google.golang.org/api`;
§11.3 `WORKER_IDENTITY_APPLE_*`, base64; §18 — отметить проверенное), спека бэкенда (§2 «Вход», §4.1 таблица
`provider_calls`), `docs/deploy-dev.md` (порядок включения провайдеров на стенде), `docs/versions.md` (если Шаг 0 что-то
отложил).

**Интерфейсы / стражи.**
- `TestCoreOpsNotStub` → I-2 32 + I-3 14 = 46 операций отвечают не 501; `pendingOps` — ровно 4 public (I-4) и 9 admin.
- `i3Scenario` (`identity_i3_test.go`, сервер с `identitytest.Providers`): nonce → соцвход `signup_required` →
  дорегистрация → привязка второго провайдера → свежий вход соцвходом → отвязка; ключ: задача → регистрация → вход →
  2FA (TOTP) → вход паролем → второй фактор ключом → свежий вход ключом → переименование → удаление; задача аттестации.
- `TestCoreCacheControl`, `TestCoreAnonymousNoIdempotency` (16 анонимных — 9 I-2 + 7 I-3), `TestCoreNoPIIInEvents`
  (события I-3), `TestCoreIDOR` (+ `renamePasskey`, `deletePasskey`, `unlinkIdentity` чужого → 404).
- `TestDeviceTrustAppliedToOps` → режим `enforce`, негодная аттестация: `startSignup`, `login`, `socialLogin`,
  `completeSocialSignup`, `passkeyLogin` → 403 `device_untrusted`; `confirmSignup`, `completeMfa`,
  `confirmPasswordReset`, `cancelDeletion` — проверки нет (R11).
- `TestFlagMatrix` — R17 по всем операциям I-3 и веткам I-2 (`completeMfa` с ключом, `reauthenticate` ключом и
  соцвходом), «провайдер не настроен» ≡ «флаг выключен»; `unlinkIdentity`/`deletePasskey` — при любых флагах.
- `TestLastMethodRaceAcrossTypes` → аккаунт без пароля с Apple и ключом: параллельно `unlinkIdentity` и
  `deletePasskey` → один успех, другой 409.

**Подсадка.** Вернуть одну операцию в 501 → `TestCoreOpsNotStub` красный. Убрать `deviceTrust` из `socialLogin` →
`TestDeviceTrustAppliedToOps` красный.

**Приёмка.** Документы совпадают с кодом; `backend-identity.md` описывает провайдеры, nonce, аттестацию,
`provider_calls`, дополнение порядка замков R18; спека — с поправками R; «Передача следующим планам» этого плана
перенесена в план I-4 при его написании (контроллер).

**Не трогай.** Код сценариев (найденные дефекты — в отчёт, правит владелец задачи).

### Task 15 (контроллер): провайдеры на стенде

**Цель.** Включить на стенде то, на что у пользователя есть учётки и ключи («Что нужно от пользователя»).

**Риск:** C. **Когда:** после итогового ревью, `./task ci`, мержа PR I-3 и появления ключей; частями — по мере ключей.

**Шаги.** (1) В панели Render у `wf-api` — значения групп (Apple, Google, WebAuthn, App Attest, Play) и
`WORKER_IDENTITY_APPLE_*`; секреты — base64 одной строкой, копия — у пользователя вне репозитория. (2) `API_WELLKNOWN_*`
(AASA, assetlinks) — с Team ID, bundle id, пакетом и SHA-256 сертификатов. (3) Деплой → журнал старта: «способ не
настроен» только у незаданных групп. (4) Проверки: `GET /.well-known/apple-app-site-association` и `assetlinks.json` —
200, `application/json`, без редиректа; `getAuthMethods` — готовые способы. (5) Флаги в админке стенда: `auth_apple`
(затем `auth_google`), `auth_passkeys` — только после пробного входа с устройства нативщика. (6) `API_IDENTITY_ATTEST_MODE`
— `observe` только после того, как клиент шлёт `integrity` (Q1). Отчёт — без значений ключей.

## Порядок исполнения

```
Шаг 0 (контроллер):   слияние embedded-worker проверено, ветка, go get трёх модулей, versions.md
Волна 1 (платформа):  Исп. А: 1 → 5 → 6     Исп. Б: 2 → 4     Исп. В: 3
Волна 2 (стык):       Исп. А: 7 → 8          (одна ветка: все общие файлы)
Волна 3 (сценарии):   Исп. А: 9              Исп. Б: 10 → 11   Исп. В: 12 → 13
Волна 4 (итог):       Исп. А: 14; затем итоговое ревью ветки, волна исправлений, ./task ci, пуш и PR — по команде
                      пользователя; Task 15 — контроллер, после мержа и ключей
Ревью — одно на группу исполнителя в волне (wf-reviewer, opus, effort high).
```

- Пути внутри волны не пересекаются: волна 1 — `platform/config` + `render.yaml` + `deploy/server/*.env.example` +
  `migrations` + `grants` + `identity/{events.go,internal/domain,mail}` + `app/{mail,audit}.go` (А) ∥
  `identity/internal/{applekey,social,attest}` + `identitytest/{social,apple,appattest,play,devicecheck}.go` (Б) ∥
  `identity/internal/webauthn` + `identitytest/authenticator*.go` (В). Волна 3 — файлы-владельцы R27 (`app/<область>.go`,
  `queries/<область>.sql`, `httpapi/{devices,social,passkeys}.go`, `pending_{devices,social,passkeys}_test.go`); общие
  (`app/app.go`, `login_core.go`, `mfa_login.go`, `reauth_op.go`, `devices.go`, `signup.go`, `login.go`,
  `identities.go`, `httpapi/{mfa,password}.go`, `nostub_test.go`, `identity.go`) — у Task 7/8, в брифах волны 3 —
  «Не трогай». Исключение: `cleanup.go` — Task 7 (одна строка вызова), затем Task 11.
- `identitytest/fakes.go` — Task 7; волна 3 его не правит (нужна ручка — через контроллера).
- Зависимости: 4 ← 2 (`applekey`); 7 ← 1–6; 8 ← 7; 9–13 ← 8; 11 ← 10 (`checkSocialToken`); 13 ← 12 (`passkeyUser`);
  14 ← все.
- `sqlc generate` — исполнители сами (I-2 R5: модели закреплены якорем; `ProviderCall` — якорь Task 5).
- pnpm в I-3 не запускается; `./task gen` и `./task ci` — контроллер после Task 14.
- Пачки по 3–5 задач за сессию (`work-cadence`): «Шаг 0 + 1 ∥ 2 ∥ 3» → «5 + 6 ∥ 4» → «7 → 8» → «9 ∥ 10 ∥ 12» →
  «11 ∥ 13» → «14». Не больше 4 opus-агентов одновременно.
- Ворота перед ревью (контроллер): «Done when» задачи (её тесты и подсадки), `go vet` пакетов, `gofmt -l` по путям,
  `git status` против списка путей отчёта.
- Коммит — контроллер, один на задачу: «I-3 Task 10: соцвход — nonce, вход и дорегистрация».
- Брифы волны 3: красный тест — только через сигнатуру, объявленную Task 7; новая сигнатура или поле `Deps` — через
  контроллера; порядок замков R18 — в «Ловушках».

## Решения плана

Решения, которых нет в спеке или которые её уточняют; поведенческие вносятся в спеку в Task 14 (§0.2).

- **R1. Раздел операций** — Приложение 1: 14 public по задачам 9–13, admin в I-3 нет.
- **R2. Провайдеры за портами корня** (`SocialVerifier`, `AppleTokens`, `Attestor`, `DeviceChecker`, `*PasskeyRP`,
  `ProviderReadiness`), реализации — листовые `identity/internal/{applekey,social,webauthn,attest}`, сборка —
  `identity.NewProviders` / `NewWorkerProviders`; тестовые подделки и фикстуры — `identity/identitytest`, импорт только
  из тестов (страж archtest). Корень может импортировать эти листы (они не импортируют корень).
- **R3. Конфиг группами «всё или ничего»** (Apple, WebAuthn, App Attest, Play; Google — одно поле): частичная группа —
  отказ старта до базы; пустая — способ не настроен (`ProviderReadiness` false, предупреждение). Секреты
  (`.p8`, JSON сервисного аккаунта) — base64 одной строкой.
- **R4. Origins.** `https://…` → `RPOrigins`, `android:apk-key-hash:…` → `RPOpaqueOrigins` (`go-webauthn` v0.18.2 —
  отдельное поле для непрозрачных origin, проверено по исходнику).
- **R5. Сессия WebAuthn** — `auth_challenges.data` (`SessionData` JSON) с обнулённым `Expires`: библиотека сверяет его
  с `time.Now()` (проверено по исходнику), срок — `expires_at` по `clock.Clock`. `challenge_id`, `nonce_id` —
  `auth_challenges.id` (контракт: uuid); `token_hash` — только у nonce (R6) и `signup_token`.
- **R6. Nonce соцвхода** — `token_hash = SHA-256(nonce)`, сырой nonce не хранится: Google — SHA-256(claim) = хэш,
  Apple — claim = hex(хэш). Гашение — отдельной короткой tx до сценария: строка без аккаунта, а сценарии `linkIdentity`
  и `reauthenticate` держат `users` раньше — в одной tx это нарушало бы порядок замков I-2 R30.
- **R7. `id_token` без discovery**: `NewRemoteKeySet` на известные JWKS, `SkipClientIDCheck` + своя сверка `aud` со
  списком, издатели — свой список (Google — две формы), `Config.Now` — `clock`. Сеть — вне tx. JWKS недоступен — 500
  `internal` (кода 503 в контракте нет, а `social_token_invalid` солгал бы клиенту).
- **R8. Шифротекст Apple** — `{client_id, refresh_token}` в `apple_refresh_enc` (aad — id привязки): отзыв требует тот
  же `client_id`, колонки для него в схеме нет; `rekey` I-2 перешифровывает его как непрозрачные байты.
- **R9. Очередь вызовов провайдеров `provider_calls`** (новая таблица, Task 5): отзыв Apple после отвязки и
  анонимизации, обновление DeviceCheck после регистрации. Payload — под кольцом (aad — id строки), задача River
  `identity.provider_call {v, ref}` (повторы — River), успех и безнадёжный отказ удаляют строку, чистка — старше 7
  дней. Отвергнуто: токен в аргументах задачи (спека §1.4), строка `user_identities` как очередь (UNIQUE `(provider,
  subject)` и `(user_id, provider)` мешают повторной привязке и регистрации тем же Apple ID). Отступление от «двух
  миграций» спеки §5.1 — Q3.
- **R10. Аттестация — сценарий `deviceTrust`**: порт `Attestor` проверяет только криптографию и ответ Google; задача,
  ключ и счётчик App Attest — `auth_challenges`/`devices` в короткой tx (`devices` → `auth_challenges`, без `users`) до
  сценария. Порт I-2 `Check → AttestVerdict` и `AttestOff` удаляются; режим `off` их заменяет. Платформа без
  конфигурации не проверяется (предупреждение старта).
- **R11. Где аттестация** — `startSignup`, `completeSocialSignup`, `login`, `socialLogin`, `passkeyLogin` (у них
  `device_untrusted` в контракте); `confirmSignup`, `completeMfa`, `confirmPasswordReset`, `cancelDeletion` — нет
  (кода нет; устройство второго шага сверяет `stepClientMatches` I-2).
- **R12. DeviceCheck.** Ворота — на `startSignup` и `completeSocialSignup` (вне tx; Apple ≥ 2 → `device_signup_limit`;
  нет токена у iOS — как нет аттестации; Apple недоступна — PoW). Обновление — после создания аккаунта
  (`confirmSignup`, `completeSocialSignup`) строкой `provider_calls` с токеном **этого** запроса (нет — без обновления,
  наш счётчик `devices.signups` остаётся главным); исполнитель читает биты и пишет `min(3, n + 1)`.
- **R13. Новое устройство** — `noteDevice` в `finishLoginAs` до `linkDevice`: пары не было, не регистрация, у аккаунта
  есть другие пары → журнал, письмо, сигнал `risk.NewDevice` на лимитере tx. Веб без метки BFF — письма нет (устройство
  не опознать).
- **R14. `risk.NewDevice` — только учёт, без ворот PoW**: ворота до проверки пароля — оракул существования аккаунта,
  после — оракул верного пароля. Пороги и ворота — по данным стенда, отдельно.
- **R15. `device_shared`** — при новой паре, если аккаунтов устройства за 30 дней > 3; событие на каждую новую пару
  сверх порога; версия агрегата — unix-секунды `devices.last_seen_at`.
- **R16. Отложенная автопривязка** — `PendingLink` в `auth_challenges.data_enc` шага `mfa` (aad — id вызова),
  создаётся после второго фактора в tx `completeMfa`; `sub` уже занят или провайдер уже привязан — вход проходит без
  привязки (журнал); `deletion_pending` — без привязки (войдёт после отмены — привяжется).
- **R17. Флаги по веткам**: вход привязанного — `auth_<p>_login`; автопривязка — `auth_<p>` **и** `auth_<p>_login`;
  `signup_required`, `completeSocialSignup`, `linkIdentity` — `auth_<p>`; `createSocialNonce` — любой из двух;
  свежий вход соцвходом — `auth_<p>_login`; ключи: добавление — `auth_passkeys`, вход, второй фактор, свежий вход —
  `auth_passkeys_login`; отвязка, удаление, переименование — без флагов. Провайдер не настроен ≡ флаг выключен (403
  `feature.disabled`). Флаг — после проверки токена, до записей (кроме гашения nonce).
- **R18. Порядок замков** (дополнение I-2 R30): `users` → `sessions` → `refresh_tokens` → `devices` →
  `user_mfa`/`user_recovery_codes` → `user_identities`/`passkeys` → `mfa_resets` → `auth_codes`/`auth_challenges`;
  `provider_calls` — только вставка. Вход ключом — `lockByRow` по строке `passkeys`; аттестация — `devices` →
  `auth_challenges` в своей tx без `users`; nonce — своя tx (R6).
- **R19. Последний способ входа** — пароль (строка `credentials`), привязки, ключи; счёт и удаление — под `lockUser`
  одной tx.
- **R20. Имя ключа по умолчанию** — модель устройства сессии → «браузер · ОС» из UA (I-2 R25) → платформа; ≤ 64; это
  имена собственные, не UI-строки.
- **R21. Виртуальный аутентификатор и подписанты `id_token`** — свои (`identitytest`, ES256/RS256, `cbor`), без
  сторонних библиотек.
- **R22. Play Integrity и DeviceCheck — свой HTTP-клиент**, без `google.golang.org/api`: модуль выходит еженедельно
  (на 16.11.2026 v0.313–0.315 от 11–13.11 — все моложе двух недель), тянет десятки модулей, а нужна одна ручка. JWT
  bearer сервисного аккаунта — `golang-jwt` (уже есть). Отступление от спеки §11.1 — Q2.
- **R23. Среда Apple одна** — `*_APPATTEST_ENV` задаёт `aaguid` App Attest и хост DeviceCheck.
- **R24. Ключ — второй фактор**: вход ключом — `mfaDone` (`mfa_at = now`, отмена сброса 2FA владельцем); свежий вход
  ключом — `mfa_at = now` и `cancelResetsByOwner`; соцвход для свежего входа — только без 2FA (с 2FA — 422, не
  неудача).
- **R25. Неудача ключа на шаге 2FA** — как неудача фактора: лимит `user:mfa`, попытка `mfa_token`, задача WebAuthn
  гасится при любом исходе; `passkey_invalid` + `attempts_left`, пятая — 410.
- **R26. Неудача входа ключом** — задача гасится (коммит), 400 `passkey_invalid`, `risk.LoginFailed` по IP (аккаунта
  нет — ключ неизвестен); неизвестный ключ и неверная подпись неотличимы.
- **R27. Файлы-владельцы I-3**: общие файлы ядра I-2 правит только Task 8 (и Task 7 — `app.go`, сборку); волна 3
  реализует заглушки в своих файлах.
- **R28. Исполнитель `provider_calls` — воркер** (и встроенный воркер api на стенде); ключ Apple воркера —
  `WORKER_IDENTITY_APPLE_*`; нет ключа — задача падает видимо, строку удалит чистка; предупреждение старта.
- **R29. Обмен кода Apple** — вне tx и только когда нужен (привязки нет или у неё нет токена); `client_id` — `aud`
  токена; сбой — вход проходит, журнал (спека §3.5).
- **R30. Недоступность провайдера**: JWKS — 500 (R7); обмен/отзыв Apple — R29/R9; Play Integrity и DeviceCheck — PoW в
  любом режиме (§9.5).
- **R31. Номер миграции** — следующий свободный в момент Task 5.
- **R32. Автопривязка к аккаунту, у которого уже есть привязка того же провайдера с другим `sub`** — не делается: ветка
  `signup_required`, дорегистрация ответит `409 email_taken` («войдите»). Кодов `identity_exists` у `socialLogin` нет
  (контракт), вход без привязки по совпадению почты был бы привязкой по почте в обход правил. Q4.
- **R33. Дорегистрация соцвходом** создаёт привязку без письма и события `identity_linked` (`user_registered.method`
  говорит то же).
- **R34. Гонка лимита 10 ключей в `registerPasskey`** — `400 passkey_invalid` (у операции нет `passkey_limit`; лимит
  проверяет `startPasskeyRegistration`). Правка заморозки — Q6.

## Что нужно от пользователя

Секреты — только в панель Render и локальный `backend/.env` (скриптом), не в чат, не в репозиторий, не в отчёты. До
появления любой учётки работа не стоит: все задачи 1–14 тестируются на `identitytest` (свои ключи, JWKS, цепочка App
Attest, подделки Google и Apple на `httptest`), на стенде группа не задана → способ «не настроен» и скрыт в
`getAuthMethods`, флаги выключены, `API_IDENTITY_ATTEST_MODE=off`.

| # | Что | Где сделать | Значения → куда | Задача | До появления |
| --- | --- | --- | --- | --- | --- |
| 1 | Apple Developer Program (платная) | developer.apple.com | Team ID → `API_IDENTITY_APPLE_TEAM_ID`, `WORKER_IDENTITY_APPLE_TEAM_ID` | 15 | Apple выключен |
| 2 | App ID приложения с возможностями Sign in with Apple, App Attest, Associated Domains | Certificates, Identifiers & Profiles | bundle id → `API_IDENTITY_APPLE_CLIENT_IDS`; `<TEAMID>.<bundle>` → `API_IDENTITY_APPATTEST_APP_IDS`, `API_WELLKNOWN_APPLE_APP_IDS` | 15 | — |
| 3 | Ключ (.p8) с Sign in with Apple **и** DeviceCheck (скачивается один раз — сохранить у себя) | Keys | Key ID → `*_APPLE_KEY_ID`; base64 файла → `*_APPLE_PRIVATE_KEY` (api и воркер) | 15 | обмен/отзыв Apple и DeviceCheck не работают — их и нет |
| 4 | Среда App Attest сборок (Xcode — development; TestFlight/App Store — production) | — | `API_IDENTITY_APPATTEST_ENV`, `WORKER_IDENTITY_APPATTEST_ENV` | 15 | `production` по умолчанию |
| 5 | Google Cloud: экран согласия OAuth; OAuth client id iOS (bundle), Android (пакет + SHA-1 сертификатов загрузки и Play App Signing), Web (server client id — его указывает Credential Manager) | console.cloud.google.com | все id → `API_IDENTITY_GOOGLE_CLIENT_IDS` | 15 | Google выключен (и без Apple не включится — `Requires`) |
| 6 | Google Play Console: приложение, Play Integrity API включён и связан с проектом GCP; сервисный аккаунт с доступом к Play Integrity, ключ JSON | play.google.com/console, GCP IAM | пакет → `API_IDENTITY_PLAY_PACKAGE`; base64 JSON → `API_IDENTITY_PLAY_CREDENTIALS` | 15 | Android не аттестуется |
| 7 | SHA-256 сертификатов подписи Android (загрузки и Play App Signing) | Play Console → Целостность приложения | `android:apk-key-hash:<base64url>` → `API_IDENTITY_WEBAUTHN_ORIGINS`; `<пакет>=<SHA-256>` → `API_WELLKNOWN_ANDROID` | 15 | ключи доступа на Android не работают |
| 8 | Домен rpId: на стенде — домен `wf-api` (решение В7: конфиг деплоя); подтвердить, что ключи стенда на прод не переедут | — | `API_IDENTITY_WEBAUTHN_RP_ID`, `…_RP_NAME` (`WF dev`), `…_ORIGINS` (`https://<домен api>`) | 15 | passkeys скрыты |
| 9 | Нативщик (wf-native): `device.integrity` по `createDeviceChallenge`, `device_check_token` и на `confirmSignup` (R12), associated domains `webcredentials:<rpId>`, Credential Manager, nonce Apple — SHA-256 hex | передать документом | — | 15 (режим `observe`) | `ATTEST_MODE=off` |
| 10 | Решения: Q1–Q6 ниже | ответ в чате | — | 1, 5, 10, 12, 14 | план исполняется с рекомендациями по умолчанию |

## Открытые вопросы

- **Q1. Режим аттестации на стенде.** Рекомендация — `off` в `render.yaml` до клиента нативщика с `integrity` (иначе
  `observe` требует PoW на каждой регистрации и входе с мобильного); затем `observe`, `enforce` — только на проде по
  данным. Спека §3.8 умолчание `observe` не меняется (код).
- **Q2. Отказ от `google.golang.org/api`** (R22) — отступление от спеки §11.1. Рекомендация — согласовать.
- **Q3. Таблица `provider_calls`** (R9) — миграция сверх «двух миграций» спеки §5.1. Рекомендация — согласовать.
- **Q4. Автопривязка при другой привязке того же провайдера** (R32): `signup_required` → `email_taken`. Альтернатива —
  правка заморозки (`identity_exists` у `socialLogin`). Рекомендация — R32 без правки.
- **Q5. Sign in with Apple в вебе (Services ID).** Обмен кода требует `redirect_uri` веба; до спеки веба токен отзыва
  для веб-входов не получаем (вход проходит, отзыв невозможен). Рекомендация — отложить до спеки веба (BFF ещё нет).
- **Q6. `passkey_limit` у `registerPasskey`** (R34) — **правка заморозки — решение пользователя**: добавить код в
  `x-error-codes` операции (хэш среза и журнал изменений нативщику). Рекомендация — не править: гонка двух регистраций
  на 10-м ключе редка, ответ `passkey_invalid` честен («задача недействительна»).

## Передача следующим планам

| Хвост | Куда |
| --- | --- |
| `getAuthMethodsUsage` (сколько входят только этим способом) — агрегирующее представление без секретов по `credentials`, `user_identities`, `passkeys` | I-4 |
| Живой e2e identity: соцвход и ключи на `identitytest` за тегом сборки (как `wf_e2e`) или только apitest — решить в плане I-4 | I-4 |
| Ворота PoW по `risk.NewDevice` и пороги — по данным стенда (R14) | после стенда |
| Веб: Services ID Apple и `redirect_uri` (Q5), origin веба в WebAuthn, перенос `.well-known` в веб | спека веба |
| Прод-домен rpId (бренд) и ключи доступа на проде — смена домена обнуляет ключи (В7) | выбор бренда |
| `API_IDENTITY_ATTEST_MODE=observe` на стенде после клиента нативщика (Q1) | Task 15 / нативщик |
| Срок жизни токена DeviceCheck и формат ответа «биты не заданы» — сверить на стенде с настоящим Apple (§18) | Task 15 |
| Сигнал клона ключа (`passkey_clone_warning`) и `attest_failed` модератору | спека `moderation` |

## Сверка хвостов плана I-2 («Передача следующим планам»)

| Хвост I-2 | Задача I-3 |
| --- | --- |
| 14 операций public I-3 | 9–13 (Приложение 1) |
| Ветки `passkey`/`social` в `completeMfa`, `reauthenticate`; `methods` с `passkey` | 8 (диспетчер, флаги), 11, 13 (проверки) |
| `ProviderReadiness` из конфига Apple/Google/WebAuthn | 7 |
| `Attestor` (App Attest, Play Integrity), DeviceCheck, `API_IDENTITY_ATTEST_MODE` | 1, 4, 7, 9 |
| Письмо `new_device`, событие `device_shared`, связанные аккаунты | 6, 8 (представление — I-2) |
| Отзыв Apple при отвязке и анонимизации | 9 (исполнитель), 11 (постановка) |
| Ключи env Apple, Google, WebAuthn, App Attest, Play; библиотеки | Шаг 0, 1 |
| Стенд: ключи провайдеров | 15 |

# Приложения

## Приложение 1. Операции по задачам и файлам-владельцам

| Задача | Файл `httpapi` / `pending_*` | Операции |
| --- | --- | --- |
| 9 | `devices` | `createDeviceChallenge` |
| 10 | `social` | `createSocialNonce`, `socialLogin`, `completeSocialSignup` |
| 11 | `social` | `linkIdentity`, `unlinkIdentity` |
| 12 | `passkeys` | `startPasskeyRegistration`, `registerPasskey`, `renamePasskey`, `deletePasskey` |
| 13 | `passkeys` | `startPasskeyLogin`, `passkeyLogin`, `startMfaPasskey`, `startReauthPasskey` |

Итого 1 + 3 + 2 + 4 + 4 = 14. Анонимные мутации I-3 (7): `createDeviceChallenge`, `createSocialNonce`, `socialLogin`,
`completeSocialSignup`, `startPasskeyLogin`, `passkeyLogin`, `startMfaPasskey` — вместе с 9 из I-2 это 16 из
`x-anonymous-mutations`. Ветки I-2, меняемые в I-3: `completeMfa` (ключ), `reauthenticate` (ключ, соцвход),
`startSignup`/`login` (аттестация), `confirmSignup` (DeviceCheck), `getAuthMethods` (готовность) — Task 8.

## Приложение 2. Письма, события, аудит, журнал I-3

- **Письма** (§8.2, ref — `security_events.id`): `new_device` (8), `identity_linked` (8 — `createIdentity`, зовут 8, 10,
  11), `identity_unlinked` (11), `passkey_added`, `passkey_removed` (12). Composer и локали — Task 6.
- **События** (§8.1): `identity_linked` (8), `identity_unlinked` (11), `passkey_added`, `passkey_removed` (12),
  `device_shared` (8), `user_registered` с `method = apple|google` (10).
- **Аудит** (§8.3): `identity.identity_linked`/`unlinked`, `identity.passkey_added`/`removed`, `identity.user_registered`
  (10) — константы Task 6.
- **Журнал `security_events`**: `new_device` `{platform, model?, browser?, os?}` (8); `identity_linked`/`unlinked`
  `{provider}` (8, 11); `passkey_added`/`removed`/`passkey_clone_warning` `{passkey_id}` (12, 13); `attest_failed`
  `{kind, reason}` без `user_id` (9); `login` с `method` (`finishLogin`).

## Приложение 3. Переменные окружения I-3

| Ключ | Бинарник | Формат | Секрет |
| --- | --- | --- | --- |
| `API_IDENTITY_APPLE_TEAM_ID`, `…_APPLE_KEY_ID` | api | строки Apple | нет (`sync: false`) |
| `API_IDENTITY_APPLE_PRIVATE_KEY` | api | base64 `.p8` | да |
| `API_IDENTITY_APPLE_CLIENT_IDS` | api | через запятую | нет |
| `API_IDENTITY_GOOGLE_CLIENT_IDS` | api | через запятую | нет |
| `API_IDENTITY_WEBAUTHN_RP_ID`, `…_RP_NAME`, `…_ORIGINS` | api | хост; имя; через запятую | нет |
| `API_IDENTITY_APPATTEST_APP_IDS`, `…_APPATTEST_ENV` | api | `<TEAMID>.<bundle>`; `development\|production` | нет |
| `API_IDENTITY_PLAY_PACKAGE`, `…_PLAY_CREDENTIALS` | api | пакет; base64 JSON | `CREDENTIALS` — да |
| `API_IDENTITY_ATTEST_MODE` | api | `off\|observe\|enforce` (умолчание `observe`, стенд — `off`) | нет |
| `WORKER_IDENTITY_APPLE_TEAM_ID`, `…_KEY_ID`, `…_PRIVATE_KEY`, `WORKER_IDENTITY_APPATTEST_ENV` | worker (и api во встроенном режиме) | как у api | `PRIVATE_KEY` — да |


