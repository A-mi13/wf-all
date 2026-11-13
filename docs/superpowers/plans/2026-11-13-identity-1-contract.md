# Identity I-1: контракт identity (public и admin) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

> **Формат — бриф задач, без кода и без полного YAML** (решение пользователя 13.10.2026, память `plan-format-brief`).
> Каждая задача: цель, риск, файлы, точные интерфейсы (operationId, пути, схемы по именам, коды), тесты (имя → вход →
> ожидаемое), подсадка, приёмка, ловушки. Как писать YAML и Go — решает исполнитель по TDD, спеке и
> `.claude/rules/contracts.md`. Авторитет: «Решения пользователя» (раздел после «Решений плана») → спека →
> «Решения плана» → тексты задач, с уточнением: решение R, явно отступающее от спеки (R3, R10, R13–R15 и т. п.),
> действует вместо спеки до внесения правки в спеку в Task 5. Где задача расходится со спекой без ссылки на решение R
> — права спека, задача — ошибка плана (сообщить контроллеру).

**Goal:** Этап 1 спеки identity (§15) — **заморозить контракт** до кода:
- все 50 публичных операций §6.3 и 9 админских §7.1 + 2 операции связи «сотрудник → игрок» §7.6 — в YAML с полным
  описанием и примерами, тегом-реализатором, `x-error-codes`, `x-rate-limit`, `Idempotency-Key`, `x-permission`;
- коды §6.5 — константами модуля со стражами в обе стороны; причины отказа §7.3 — открытым enum
  `CaptainRejectionCode` в контракте (Go-констант для него в I-1 нет); тексты ошибок — в локалях веба и админки;
- заморозка проверяется стражем: хэш замороженного среза identity и `pendingOpsAtFreeze` (R17, Task 5);
- `x-anonymous-mutations` public — ровно 16 операций; новые поля `Problem` (§6.6) — в обеих копиях;
- классы лимитов §6.4 — в `ratelimit.DefaultRules`; права §7.2 и `staff.player_links.manage` — в `platform/perm`,
  черновое `teams.captains.approve` удалено со стражами (§16, К7);
- собранные серверы отвечают на новые операции `501 http.not_implemented` по явным временным спискам со стражами
  истечения; репозиторий зелёный, `./task contracts:breaking` чистый;
- нативщик получил бандл, правила генерации типов и документ «что изменилось против ваших моков» (§14).

После I-1 контракт identity правится **только через контроллера** (как Task 9a плана A и Task 7 плана B); единственная
заранее согласованная правка — enum `FeatureFlagKey` в коммите миграции флагов (R4).

**Architecture:**
- **Исходники контракта** — `contracts/openapi/public/identity/{paths,schemas}.yaml` (тег `identity`) и
  `contracts/openapi/admin/identity/{paths,schemas}.yaml` (тег `identity`); операции связи сотрудника — в
  `contracts/openapi/admin/staff/*` (тег `staff`). Корни — регистрация путей и схем, теги, `x-anonymous-mutations`,
  `Problem`, `info.description`.
- **Модуль `identity` появляется каркасом**: корневой пакет `internal/identity` (только `codes.go` — коды и списки),
  хендлеры-заглушки `internal/identity/httpapi` (public) и `internal/identity/admin` (admin) — нулевое значение
  `Handler` отвечает 501 на каждую операцию, как `geoadmin.Handler{}`. Встраиваются в `httpapi/public.Server` и
  `httpapi/admin.Server` (страж «тег = модуль-реализатор»). Сценариев, таблиц и миграций в I-1 нет.
- **Платформа:** `httpx` (код `http.not_implemented` — общий для обоих контрактов, R1; коды флагов
  `platform.flag_global_only`, `platform.flag_requires`), `ratelimit.DefaultRules` (+4 класса), `perm` (каталог).
- **Заглушки:** public получает новый страж `TestNoStubOperations` (сейчас его нет — проверено: в
  `internal/httpapi/public` стража «без 501» нет) со списком `pendingOps`; admin — временный `pendingOps` в
  существующем `TestNoStubOperations` (`internal/httpapi/admin/nostub_test.go`, удалён планом B в Task 16). Оба списка
  — со стражами истечения (R2).

**Tech Stack:** OpenAPI 3.0.3 (redocly-бандл), kin-openapi, oapi-codegen strict, openapi-typescript, node:test
(`contracts/test`), Go 1.27 (`go test`), Vitest (локали), oasdiff (`./task contracts:breaking`). Точные версии —
`docs/versions.md`; новых зависимостей в I-1 нет.

**Spec:** `docs/superpowers/specs/2026-11-13-identity-design.md` (дальше «спека»): §0 (решения), §6 (публичный
контракт — таблицы §6.3 построчно), §7 (админка — §7.1, §7.2, §7.3, §7.5, §7.6), §8.1 (справочно: события в
описаниях не перечисляются, побочные эффекты — письма, отзыв сессий — описываются), §14 (нативщик), §15 (этап 1), §16 (документы, К7), §18 (непроверенное).
Решения брейнсторминга — `.superpowers/sdd/2026-11-13-identity-spec/decisions.md`. Правила контракта —
`.claude/rules/contracts.md`. Образец формата и заглушек — план B `docs/superpowers/plans/2026-10-27-staff-b-geo2.md`
(Task 7, R6, R17).

**Предусловие старта:** ветка с утверждённой спекой (`spec/staff-c-render`, коммит `333df8e`) слита в `main` или
I-1 идёт в новой ветке `spec/identity-1` от неё. I-1 самодостаточен: после него `main` зелёный, стенд отвечает на
операции identity `501` (это описано в контракте), и его можно мержить отдельно от реализации.

## Global Constraints

- **Правила репо** — `CLAUDE.md`, `.claude/rules/{contracts,backend,backend-http,backend-platform,backend-staff,admin,frontend}.md`.
  TDD: сначала падающий тест (строка таблицы операции, страж кодов), потом YAML/код; каждый страж проверяется подсадкой
  бага (`node scripts/plant.mjs …`; в отчёт — что подсажено и что упало).
- **Риск задачи** — A/B/C по `.claude/agents/wf-implementer.md`. Поведения в I-1 нет, но контракт — стык
  четырёх планов и нативного клиента: контрактные задачи — B (таблица режимов отказа — по стражам и сверке со спекой,
  а не по гонкам). Исключение — части Task 1 и Task 4 с матрицей прав: уровень A по категориям 7 и 10 (m14; прочие
  категории — «н/п»); страж заморозки Task 5 — B, документы — C.
- **Коммиты** — делает контроллер (субагенты не коммитят): пути и сообщение — в разделе «Коммит» отчёта. Одна строка
  по-русски без тела; `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A/.`, `commit -a`, `stash`,
  `reset`, `clean`, `checkout -- .`. Доки нативщика `docs/WF_…_2026_10_19.md` и `docs/screens/` — не коммитить.
- **Код:** комментарии по-русски, идентификаторы по-английски, LF.
- **Контракт** — `.claude/rules/contracts.md` целиком: ровно один тег; `operationId` — из таблиц спеки без
  переименований; у операции `summary`, `description` (что делает, кто вызывает, побочные эффекты, каждый код и когда
  он бывает), `x-error-codes`; у каждого параметра, тела, ответа, схемы и поля — `description` и `example`
  (`docs.mjs`); каждый `enum` — `x-extensible-enum: true`; обнуляемая ссылка — `type: object` + `nullable: true` +
  `allOf: [$ref]`. Мутации с замком — `$ref: '#/components/parameters/IdempotencyKey'`; анонимные — `security: []` и
  строка в `x-anonymous-mutations`. Admin: `x-permission` у каждой операции, мутации — `AdminHeader`.
- **Имена схем** — ровно реестр Приложения 2 (владелец схемы — одна задача; следующая задача ссылается, а не
  переопределяет). Схемы admin — с префиксом `Identity…` (как `Geo…`): одно пространство `components.schemas` бандла.
- **Коды ошибок** — константы `backend/internal/identity/codes.go`; новый код — в одном коммите: константа, строка
  списка (`PublicCodes` / `AdminCodes` / `CrossModuleCodes`), `x-error-codes` операций, `errors.<код>` в локалях
  (`apps/web/messages/{ru,en}.json` — en пустая строка; `apps/admin/messages/{ru,en}.json` — en перевод).
- **Тексты** — только локали; в YAML-описаниях — тексты для разработчика, не для показа.
- **Кодогенерация** — `./task gen` (бандлы, Go strict-серверы, TS-типы, `docs/05` и `permissions.json`);
  сгенерированное коммитится. **pnpm одновременно — один процесс**: задачи I-1 идут строго по очереди (R7).
- **Тесты:** исполнитель гоняет свои: `node --test "contracts/test/*.test.mjs"` (глоб в кавычках),
  `go test ./internal/httpapi/... ./internal/identity/... ./internal/platform/{perm,ratelimit,httpx,approvals}/...`
  (из `backend/`, Postgres — `bash scripts/pg.sh start` в фоне; `-p 4`), `pnpm --filter @wf/web test -- i18n`,
  `pnpm --filter @wf/admin test -- i18n labels`. Полные `./task backend:test`, `./task admin:test`,
  `./task web:test`, `./task packages:ci` — контроллер в конце пачки. `typecheck` админки и веба — контроллер после
  задач с `./task gen` (сгенерированные типы меняются).
- **Окружение:** новых env-ключей в I-1 нет.

## Review Focus

1. **Построчная сверка со спекой.** 50 строк §6.3 и 11 строк §7.1/§7.6: метод и путь, `operationId`, анонимность,
   `Idempotency-Key`, класс `x-rate-limit` (`default` — без расширения), тело, статус и схема успеха, коды с HTTP,
   `x-permission`. Таблицы-тесты `contracts/test/identity.test.mjs` и `admin.test.mjs` повторяют спеку — ревьюер
   сверяет тесты со спекой, а не с YAML.
2. **Анонимные мутации.** `x-anonymous-mutations` public — ровно 16 operationId (все POST таблицы «Вход и
   регистрация», кроме `logout`); у каждой `security: []` и обоснование в `description`; ни одна операция с замком
   туда не попала; `logout` — с замком и `Idempotency-Key`.
3. **Секреты и кэш.** `setupTotp`, `confirmTotp`, `regenerateRecoveryCodes` — `x-idempotency: no-store` и
   `Cache-Control: no-store`; каждый ответ с `TokenPair`, `mfa_token`, `signup_token`, `restore_token` —
   `Cache-Control: no-store` (R5). Страж секретов работает и на public (R6).
4. **Перечисление аккаунтов в контракте.** `startSignup`, `startPasswordReset`, `resendAuthCode` — одинаковый `202
   CodeSent` без кодов «почта занята / не найдена»; `login` — `invalid_credentials` одинаково для «нет аккаунта»; ни
   одно описание не обещает клиенту различимость (спека §9.1).
5. **Заглушки и их истечение.** `pendingOps` public = 50 операций identity, admin = 11; операция из списка отвечает
   501 через весь конвейер, вне списка — не 501; в списке только операции I-1; новая операция в список не
   добавляется без правки замороженного множества (R2), а само множество — под хэшем (R17).
8. **Заморозка проверяема.** `identity-freeze.test.mjs` хэширует весь срез (включая `x-error-codes`, описания,
   достижимые схемы); подсадки Task 5 (код у `socialLogin`, слово в описании) красные при зелёном oasdiff; правка
   `FeatureFlagKey` хэш не меняет (R4, R17).
6. **Права.** Матрица §7.2 и `staff.player_links.manage` построчно; `identity.mfa.reset` — `dual`, без города, в
   `x-permission-any` approve/reject (`TestApprovalDecisionPermissionsAreDual`); `identity.pii.read` — рабочее;
   `teams.captains.approve` удалён, а стражи, которые им проверяли черновые права, по-прежнему не вакуумны.
7. **Совместимость.** `./task contracts:breaking` — только добавления, файлы исключений пусты; `Problem` одинакова
   в двух корнях; ответ `Problem` (`Retry-After`) — тоже.

---

## File Structure

| Путь | Ответственность | Задача |
| --- | --- | --- |
| `backend/internal/platform/perm/{catalog.go,catalog_test.go}`, `backend/internal/platform/approvals/{registry_test.go,request_test.go}`, `contracts/test/{labels,permissions}.test.mjs`, `contracts/openapi/admin/permissions.json` (gen), `docs/05-ролевая-модель.md` (gen-таблица), `contracts/openapi/admin/platform/paths.yaml` (`x-permission-any` approve/reject), `apps/admin/messages/{ru,en}.json` (`permissions.*`) | права §7.2, `staff.player_links.manage`, удаление `teams.captains.approve` | 1 |
| `backend/internal/platform/ratelimit/{rules.go,rules_test.go}` | классы §6.4 | 1 |
| `backend/internal/platform/httpx/codes.go` (+ тест), `contracts/openapi/{public,admin}/root.yaml` (`Problem`, ответ `Problem`, `x-error-codes-common`, теги, `info.description`), `apps/web/messages/{ru,en}.json` (`errors.http.not_implemented`) | общий 501, поля `Problem`, тег `identity` | 1 |
| `backend/internal/identity/{codes.go,codes_test.go}`, `backend/internal/identity/httpapi/{handler.go,handler_test.go}`, `backend/internal/identity/admin/{handler.go,handler_test.go}`, `backend/internal/httpapi/public/{server.go,nostub_test.go,handler_test.go,fakes_test.go}`, `backend/internal/httpapi/admin/{server.go,nostub_test.go,handler_test.go}` | каркас модуля, стражи кодов и заглушек | 1 |
| `contracts/openapi/public/identity/{paths,schemas}.yaml`, `contracts/openapi/public/root.yaml`, `contracts/test/identity.test.mjs`, `backend/internal/identity/codes.go`, `backend/internal/httpapi/public/nostub_test.go` (строки запросов), `apps/web/messages/{ru,en}.json` | вход, регистрация, коды, токены, сессии, аккаунт без 2FA и соцвходов | 2 |
| те же файлы public | 2FA, ключи доступа, соцвходы, способы входа, свежий вход, сброс 2FA игроком | 3 |
| `contracts/openapi/public/identity/*`, `contracts/openapi/admin/identity/{paths,schemas}.yaml`, `contracts/openapi/admin/{root.yaml,staff/*.yaml,platform/*.yaml}`, `contracts/test/admin.test.mjs`, `backend/internal/identity/codes.go`, `backend/internal/staff/{codes.go,admin/handler.go,admin/codes_test.go}`, `backend/internal/platform/httpx/codes.go` (флаги), `backend/internal/httpapi/admin/{nostub_test.go,permissions_test.go,handler_test.go}`, `apps/admin/messages/{ru,en}.json`, `apps/web/messages/{ru,en}.json` | капитанство public, админка identity, связь «сотрудник → игрок», коды флагов, действие `identity.mfa_reset` | 4 |
| сгенерированное (`contracts/openapi/{public,admin}.yaml`, `backend/internal/httpapi/{public,admin}/oapi/*`, `apps/{web,admin}/src/api/gen/*.d.ts`) | `./task gen` в каждой задаче 1–4 | 1–4 |
| `contracts/test/identity-freeze.test.mjs`, `backend/internal/httpapi/{public,admin}/nostub_test.go` (хэш `pendingOpsAtFreeze`), `.claude/rules/contracts.md`, `docs/05-ролевая-модель.md` (проза §5.2, §5.3, §7), спека (поправки R), `contracts/openapi/{public,admin}/root.yaml` (комментарий заморозки, `info.version`); вне истории: `.superpowers/sdd/2026-11-13-identity-1/native-handoff.md` (U1) | страж заморозки (R17), документы, передача нативщику | 5 |

# Задачи

Обозначения: **Риск** — A/B/C; **Тесты** — `Имя` → вход → ожидаемое; **Подсадка** — какой баг подсадить, какой тест
обязан упасть; **Приёмка** — что проверяет ревьюер. Файлы — ориентир: внутри пакета исполнитель раскладывает сам, если
не меняет интерфейсы стыков.

Общая механика контрактных задач 2–4 (не повторяется в задачах):
- **Таблица-тест первым.** `contracts/test/identity.test.mjs` (public, создаёт Task 2) и `admin.test.mjs` (admin)
  держат строку на операцию: метод, путь, тег, анонимность, наличие `IdempotencyKey`, класс `x-rate-limit` (или его
  отсутствие), множество `x-error-codes`, статус и `$ref` схемы успешного ответа, значение `Cache-Control` (пример
  заголовка), `x-idempotency`. Строки пишутся из таблиц спеки до YAML — тест падает, затем YAML.
- **Коды.** Строки `x-error-codes` = коды `identity.*` из колонки спеки + платформенные, которые операция реально
  отдаёт сверх общих (их нет: `feature.disabled`, `humancheck.required` уже общие — в `x-error-codes` операции не
  повторяются, только упоминаются в `description`).
- **Заглушки.** Каждая новая операция сразу попадает в `pendingOps` своего стража со строкой годного по схеме запроса;
  `go test` пакетов `httpapi/{public,admin}` зелёный.
- **Генерация.** После YAML — `./task gen`; сгенерированное — в пути коммита задачи.

## Этап 1. Контракт identity

### Task 1: платформа контракта — права, лимиты, `Problem`, общий 501, каркас identity и стражи заглушек

**Цель.** Подготовить всё, на что опираются контрактные задачи 2–4, так, чтобы после задачи репозиторий был зелёным,
а новые стражи — не вакуумными: права §7.2 в каталоге, черновое `teams.captains.approve` удалено, классы лимитов
§6.4 в `DefaultRules`, `Problem` с полями §6.6 и R3, код `http.not_implemented` общий для обоих контрактов (R1),
модуль `identity` существует каркасом (коды и пустые хендлеры), у public есть страж «без заглушек» со списком
`pendingOps`, у admin — временный `pendingOps` со стражем истечения (R2).

**Риск:** A для части «Права» (матрица прав: `identity.pii.read` из черновика в рабочее, новое `dual`-право в
`x-permission-any` approve/reject — категории 7 «права в момент действия» и 10 «перекос стыков»; таблица режимов
отказа исполнителя — по этим двум категориям, остальные к контракту без поведения неприменимы — записать «н/п»);
B для остального (стражи заглушек — стык всех задач; ошибка тут маскирует ошибки задач 2–4).

**Предварительный шаг (контроллер, до Task 1 и до Task 4).** Правка admin-контракта по `contracts.md` — с паузой
фронтовой ветки: проверить, что нет незавершённой работы в `apps/admin` (свои ветки и параллельные агенты); если есть
— согласовать паузу и перегенерацию `admin.d.ts` до старта. После Task 1 матрица «Роли и доступ» получает новые
строки — снимки `admin-visual` изменятся: метка PR `visual-change`.

**Файлы.** File Structure, строки «1». Создать: `backend/internal/identity/{codes.go,codes_test.go}`,
`backend/internal/identity/httpapi/{handler.go,handler_test.go}`, `backend/internal/identity/admin/{handler.go,handler_test.go}`,
`backend/internal/httpapi/public/nostub_test.go`.

**Права (`perm/catalog.go`)** — блок `// identity — заявки в капитаны, ПД игроков, сброс 2FA` после блока `platform`
(порядок строк = порядок `docs/05` и подписей, R8):

| Ключ | Вид | Город | Владелец | Админ | Модератор | Поддержка | Описание (§7.2) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `identity.captain_applications.read` | `reading` | да | allow | allow | scoped | allow | очередь и карточки заявок в капитаны |
| `identity.captain_applications.decide` | `changing` | да | allow | allow | scoped | deny | одобрять и отклонять заявки в капитаны |
| `identity.pii.read` | `reading` | да | allow | allow | scoped | allow | почта и телефон игрока, поиск по почте; каждое чтение — в журнал, с причиной |
| `identity.mfa.reset` | `changing` | нет | dual | dual | deny | deny | сбросить 2FA игрока, потерявшего все факторы; отмена назначенного сброса |

- `identity.pii.read` — перенос черновой строки в рабочую (снять `draft`; гранты уже совпадают с §7.2).
- `staff.player_links.manage` — в блоке `staff`: `changing`, без города, allow / deny / deny / deny, «привязать к
  сотруднику его учётку игрока (запрет решений о себе)» (§7.6).
- Удалить `draft(city(changing("teams.captains.approve", …)))` и фикстуры, которые его называют (§16, К7):
  `perm/catalog_test.go:233-235` (`TestCityBound`: убрать `teams.captains.approve`, **добавить**
  `identity.captain_applications.read` и `.decide`), `approvals/registry_test.go:52`, `approvals/request_test.go:97`,
  `contracts/test/labels.test.mjs:12,30`, `contracts/test/permissions.test.mjs:21,133,164`. Во **всех** фикстурах,
  включая обе в `approvals`, — `pitches.pitches.moderate` (черновой в каталоге и остаётся черновым по R8): обе фикстуры
  `approvals` проверяют одну ветку — отказ для чернового права (`case p.Draft` в `checkPermission`); `registry_test`
  ждёт панику с текстом «черновое», `request_test` — ошибку не `ErrForbidden`. Вымышленный ключ или
  `identity.mfa.reset` здесь не годятся (ревью плана I1).
- `contracts/openapi/admin/platform/paths.yaml`: `approveApproval`, `rejectApproval` — `x-permission-any` +=
  `identity.mfa.reset` (этого требует `TestApprovalDecisionPermissionsAreDual`). Enum `ApprovalAction` здесь **не**
  меняется — его меняет Task 4 вместе с `requestMfaReset`.
- Подписи `permissions.<ключ>` (5 новых, ru и en) — `apps/admin/messages`; `./task gen` → `permissions.json` и
  таблица `docs/05`.

**Лимиты (`ratelimit.DefaultRules`)** — ровно §6.4: `auth_send` (IP 10/1m:5, User 5/1m:3, Device 5/1m:3),
`auth_refresh` (IP 300/1m:60, Device 30/1m:10), `auth_check` (IP 60/1m:20, Device 30/1m:10), `account_secure`
(IP 60/1m:20, User 10/1m:5); `auth` не меняется. Комментарий к классу — назначение из §6.4.

**Общий 501 (R1).** `httpx.CodeNotImplemented` переходит из `AdminCommonCodes` в `PlatformCodes` (14 кодов);
`x-error-codes-common` public += `http.not_implemented`, в admin — порядок под новое деление (комментарий «Первые 13»
→ «Первые 14»); `info.description` public — пункт: «`http.not_implemented` (501 — операция описана в контракте, но
сервер её ещё не исполняет: этапы выкатываются по очереди; это не ошибка клиента)». Текст
`errors.http.not_implemented` — `apps/web/messages` (ru — текст, en — пустая строка).

**`Problem` (оба корня одинаково; §6.6 + R3)** — необязательные поля:
- `reasons` — array of string: у `identity.captain_ineligible` — значения `CaptainIneligibility`; у
  `identity.password_weak` — ровно одно из `too_short | common | too_long` (R3);
- `methods` — array of string: вторые факторы аккаунта, которые **принимает эта операция** — у
  `identity.mfa_required` (R3); у `confirmPasswordReset` только `totp | recovery_code` (ключ доступа при сбросе не
  принимается, §3.4);
- `available_at` — date-time: у `identity.nickname_change_too_soon` и `identity.application_cooldown`;
- `blocked_until` — date-time, **без** `nullable`: у `identity.account_blocked`; «поля нет — бессрочно»
  (`httpx.Problem` сериализует указатели с `omitempty`, явного `null` не бывает).
Значения `reasons` у `password_weak` перечислены в описании поля и проверяются строкой `identity.test.mjs` (пример
`Problem` у `startSignup`), значения `CaptainIneligibility` — enum схемы (R3).
Описание каждого поля — у каких кодов оно приходит (как у `job_id`). Ответ `Problem` в обоих корнях: `Retry-After` —
«у 429 `ratelimit.exceeded` и `identity.resend_too_soon`».

**Каркас `identity`.**
- `codes.go`: `CodeRestricted = "identity.restricted"` и три списка — `PublicCodes`, `AdminCodes` (пока пустые) и
  `CrossModuleCodes = {CodeRestricted}`: коды модуля, которые отдают операции **других** модулей (§6.6, R9).
  Комментарий — где сверяется каждый список.
- `codes_test.go`: `TestCodeConstantsListed` (образец — `geo/codes_test.go`: AST всех нетестовых файлов пакета) —
  каждая экспортированная `Code…` хотя бы в одном из трёх списков; `TestCodeConstantsListedCatchesStray` — подсадка
  строкой исходника.
- `identity/httpapi.Handler` (public) и `identity/admin.Handler` (admin) — пустые структуры; методы появляются с
  операциями (Task 2–4), каждый отвечает `httpx.NewError(501, httpx.CodeNotImplemented)`. Встроены в
  `public.Server` и `admin.Server` полями-значениями (нулевое значение = заглушка; `Options` не растут). Оба пакета
  называют тип `Handler`: `public/server.go:15` уже встраивает `geohttp.Handler`, второй встроенный `Handler` — ошибка
  компиляции «duplicate field Handler». Встраивать через псевдонимы типов, как `admin/server.go:47-52`
  (`type identityHandler = identityhttp.Handler`); места сборки (`public/handler.go:94`) — по новому имени поля.
- Стражи сверки: `TestIdentityCodesDocumented` (`internal/httpapi/public`) — `identity.*` в `x-error-codes` операций
  тега `identity` public = `identity.PublicCodes`; `TestIdentityAdminCodesDocumented` (`internal/httpapi/admin`) —
  `identity.*` в `x-error-codes` всех операций admin = `identity.AdminCodes`; `TestCrossModuleCodesNotInIdentityOps`
  (public) — коды `CrossModuleCodes` не встречаются у операций тега `identity`; `adminCodePrefixes` += `"identity."`.
  До Task 2 множества пусты — невакуумность доказывает подсадка, с Task 2 — добавляется проверка «операций тега
  `identity` > 0».

**Стражи заглушек (R2).**
- **Public `TestNoStubOperations`** (новый): собранный `public.Server` с подделкой загрузчика сессий, строка годного
  по схеме запроса на **каждую** операцию контракта (`opsBase`, как в admin; без строки — падение). Операция из
  `pendingOps` — ровно `501 http.not_implemented` в формате `Problem` через весь конвейер; вне списка — не 501 и не
  401.
- **Admin `TestNoStubOperations`** — вернуть явный `pendingOps` (как R17 плана B).
- **`TestPendingOpsExpire`** (в обоих пакетах): (а) каждое имя списка есть в контракте; (б) у операции списка тег
  `identity`, кроме ровно двух admin-операций тега `staff` (`putStaffPlayerLink`, `deleteStaffPlayerLink`, Task 4);
  (в) список ⊆ `pendingOpsAtFreeze` — замороженного множества в том же файле (растёт в Task 2–4, после Task 5 только
  уменьшается): операция другого модуля или новая операция без хендлера в список не прячется. Реализованная операция
  в списке — падение «убрать из pendingOps» (основной страж). Списки и `TestPendingOpsExpire` удаляет последняя
  задача последнего плана identity (R2).
- В Task 1 оба `pendingOps` пусты.

**Тесты:**
- `TestCatalogIdentityRows` (`perm`) → четыре строки и `staff.player_links.manage` → вид, город, гранты — по таблице;
  существующий тест списка ключей → без `teams.captains.approve`.
- `TestDefaultRulesIdentityClasses` (`ratelimit`) → `DefaultRules()` → четыре класса с лимитами §6.4, `Validate()` —
  nil; `auth` прежний.
- `TestPlatformCodesDocumented` (public, admin), `TestAdminCodesNotInPublic` → зелёные после переноса 501;
  `TestAdminPlatformCodes` (`httpx/codes_test.go:190-192`, жёстко перечисляет `http.not_implemented` в `common`) →
  поправить ожидание под новое деление.
- `TestCityBound` (`perm`) → `identity.captain_applications.read`/`.decide`, `identity.pii.read` — `CityBound`;
  `identity.mfa.reset`, `staff.player_links.manage` — нет.
- `TestRegister…` (`approvals/registry_test.go`) и `TestRequestPayload` (`request_test.go`) → фикстура
  `pitches.pitches.moderate` → паника «черновое» / ошибка не `ErrForbidden`.
- `node --test "contracts/test/*.test.mjs"` → `contracts` (Problem одинакова), `labels`, `permissions`,
  `conventions`, `docs`, `bundle` — зелёные.
- `TestZeroHandlerNotImplemented` (`identity/httpapi`, `identity/admin`) → `apitest.NotImplemented(&Handler{})` → все
  методы 501 (с Task 2 — «методов > 0»).
- `TestNoStubOperations` (public) → текущий контракт (geo, platform) → ни одной 501.
- `TestApprovalDecisionPermissionsAreDual` → зелёный.
- `apps/admin`: `labels.test.ts`, `error-codes.test.ts`; `apps/web`: `error-codes.test.ts`, `messages.test.ts`.

**Подсадка.** (1) Вернуть `teams.captains.approve` в каталог — тест списка ключей падает. (2) Убрать
`identity.mfa.reset` из `x-permission-any` approve — `TestApprovalDecisionPermissionsAreDual` падает. (3) В
`pendingOps` public — `getMe`, которого ещё нет в контракте, — `TestPendingOpsExpire` падает. (4) `CodeStray` без
списка — `TestCodeConstantsListed` падает. (5) `reasons` только в public `Problem` — `contracts.test.mjs` падает.
(6) `auth_send` с `Burst` > `Limit` — `TestDefaultRulesIdentityClasses` падает. (7) Удалить `case p.Draft:` в
`approvals.checkPermission` — `go test ./internal/platform/approvals -run 'TestRequestPayload|TestRegister' -count=1`:
падают **оба** теста (с вымышленным ключом `TestRequestPayload` выжил бы — ревью плана I1).

**Приёмка.** Таблица прав `docs/05` = §7.2 построчно; `teams.captains.approve` нет нигде, кроме истории
(`docs/superpowers/*`); стражи прав ловят черновой ключ на `pitches.pitches.moderate`; серверы встраивают хендлеры
identity; `./task gen` прогнан; `./task contracts:breaking` чистый.

**Ловушки.**
- Тесты `public` собираются с `auth.NoSessions` — операции с замком отвечали бы 401 до хендлера. Нужна подделка
  `auth.SessionLoader` (принципал по `sid` выданного `token.Issuer` access-токена) в `fakes_test.go`; если похожая
  есть в `testkit` — переиспользовать.
- Тег `identity` без операций: проверить, не требует ли `docs.mjs`/`conventions.mjs` операций у тега; если требует —
  тег в корне добавляет Task 2/Task 4 вместе с первой операцией.
- `x-error-codes-common` admin сверяется с `PlatformCodes` + `AdminCommonCodes` — проверить, по порядку или
  множеством; список YAML переставить под новое деление.
- Фикстуры `approvals` — только черновой ключ из каталога (`pitches.pitches.moderate`): вымышленный ключ даёт панику
  «не из каталога» вместо «черновое» в `registry_test` и делает ветку `p.Draft` в `request_test` непроверяемой (дубль
  случая `badperm`); годное право (`identity.mfa.reset`) проходит регистрацию и создаёт запрос — оба теста красные.
- Перенос 501 в `PlatformCodes` — `TestPlatformCodesDocumented` public потребует строку в `x-error-codes-common`
  public в том же коммите; веб — текст ошибки.

### Task 2: public — регистрация и вход по почте, коды, токены, сессии, аккаунт

**Цель.** 24 операции ядра (этап 4 спеки) — в публичном контракте с полным описанием поведения, общие схемы входа
(владелец — эта задача), первые 10 строк `x-anonymous-mutations`, раздел «Вход и токены» в `info.description`,
правило `device_required`. Таблица-тест `contracts/test/identity.test.mjs` создаётся этой задачей.

**Риск:** B.

**Файлы.** Создать `contracts/openapi/public/identity/{paths,schemas}.yaml`, `contracts/test/identity.test.mjs`.
Изменить `contracts/openapi/public/root.yaml` (пути, схемы, тег — если не в Task 1, `x-anonymous-mutations`,
`info.description`, `components.headers.{CacheNoStore,CachePrivate}` — R5), `backend/internal/identity/codes.go`,
`backend/internal/identity/httpapi/handler.go`, `backend/internal/httpapi/public/nostub_test.go` (строки `opsBase`,
`pendingOps`, `pendingOpsAtFreeze`), `apps/web/messages/{ru,en}.json`; генерация.

**Операции** — строки §6.3 спеки без изменений, кроме R10 (у `confirmSignup` — ещё `device_required` 422):

| operationId | Метод и путь | Таблица спеки |
| --- | --- | --- |
| `checkNickname` | `GET /v1/nicknames/availability` | «Вход и регистрация» |
| `startSignup` | `POST /v1/auth/signup` | то же |
| `confirmSignup` | `POST /v1/auth/signup/confirm` | то же (+ R10) |
| `resendAuthCode` | `POST /v1/auth/codes/{codeId}/resend` | то же |
| `login` | `POST /v1/auth/login` | то же (+ `device_required`, примечание под таблицей) |
| `completeMfa` | `POST /v1/auth/mfa` | то же (+ `device_required`) |
| `startPasswordReset` | `POST /v1/auth/password-reset` | то же |
| `confirmPasswordReset` | `POST /v1/auth/password-reset/confirm` | то же (+ `device_required`) |
| `refreshSession` | `POST /v1/auth/refresh` | то же |
| `cancelDeletion` | `POST /v1/auth/deletion/cancel` | то же |
| `getLegalDocuments` | `GET /v1/legal/documents` | то же |
| `createDeviceChallenge` | `POST /v1/devices/challenges` | то же |
| `logout` | `POST /v1/auth/logout` | то же (с замком) |
| `getMe`, `updateMe` | `GET`, `PATCH /v1/me` | «Аккаунт» |
| `changeNickname` | `PATCH /v1/me/nickname` | то же |
| `listMySessions` | `GET /v1/me/sessions` | то же |
| `revokeMySession` | `DELETE /v1/me/sessions/{sessionId}` | то же |
| `revokeOtherSessions` | `POST /v1/me/sessions/revoke-others` | то же |
| `changePassword` | `POST /v1/me/password` | то же |
| `startEmailChange`, `confirmEmailChange` | `POST /v1/me/email-change`, `…/confirm` | то же |
| `acceptDocuments` | `POST /v1/me/documents` | то же |
| `deleteAccount` | `POST /v1/me/delete` | то же |

Сверх таблицы:
- **Анонимные** (`security: []`, строка в `x-anonymous-mutations`, без `IdempotencyKey`): `startSignup`,
  `confirmSignup`, `resendAuthCode`, `login`, `completeMfa`, `startPasswordReset`, `confirmPasswordReset`,
  `refreshSession`, `cancelDeletion`, `createDeviceChallenge`. Обоснование в `description` каждой — §6.3 под таблицей
  (вход возможен только без сессии; от повторов защищают одноразовые коды и токены, уникальность, лимиты). Анонимные
  GET — `checkNickname`, `getLegalDocuments` (`security: []`, в список не входят).
- **Свежий вход** (колонка С): `description` операций `changePassword`, `startEmailChange`, `deleteAccount` — «нужен
  свежий вход (≤ 10 минут, §3.13), иначе `403 identity.reauth_required`; получить — `reauthenticate`».
- **Устройство** (§6.2.3, К5): у `startSignup`, `confirmSignup`, `login`, `completeMfa`, `confirmPasswordReset`,
  `cancelDeletion` — поле `device` (`$ref DeviceInfo`, необязательное в схеме) и абзац: «мобильный клиент обязан
  передать `device`; без него — `422 identity.device_required`; веб (BFF) не передаёт, поле игнорируется». То же
  правило — в `info.description` (раздел «Вход и токены»).
- **PoW** (колонка «PoW»): `startSignup`, `login`, `startPasswordReset` — в `description`: «при подозрительной
  активности — `403 humancheck.required`, решите задачу и повторите» (код общий, в `x-error-codes` не дублируется).
- **Одинаковые ответы** (§9.1): `startSignup`, `startPasswordReset`, `resendAuthCode` — `description` фиксирует, что
  ответ `202 CodeSent` не зависит от существования аккаунта; `login` — `invalid_credentials` одинаков для «нет
  аккаунта» и «пароль неверен».
- **`AuthResult`** (§6.2.1) — описание каждой ветки `status` и что клиент делает дальше (`mfa_required` →
  `completeMfa`; `signup_required` → `completeSocialSignup`; `deletion_pending` → экран «Отменить удаление?» →
  `cancelDeletion`); `user.documents_acceptance_required` → экран «примите новую версию» → `acceptDocuments`.
- **`refreshSession`** — ротация на каждом вызове, окно гонки 15 с с того же устройства, повтор вне окна → отзыв
  сессии и `401 identity.refresh_invalid` (§3.7); `install_id` — для сверки устройства повтора.
- **Ошибки с полями** — `password_weak` → `reasons` (R3), `code_invalid`/`mfa_invalid` → `attempts_left`,
  `resend_too_soon` → `Retry-After`, `account_blocked` → `blocked_until`, `mfa_required` → `methods` (R3),
  `nickname_change_too_soon` → `available_at`; пример `Problem` в ответе операции для каждого такого кода.
- **Кэш (R5)** — `Cache-Control` у каждого 2xx с телом: `getLegalDocuments` — `public, max-age=300`; ответы с
  `AuthResult`/`TokenPair` — `no-store`; остальные — `private, no-store`. 204 — без тела и без заголовка.
- **`info.description`** — новый пункт «Вход и токены» по §6.1: access 10 минут, refresh непрозрачный с ротацией,
  хранение в Keychain/Keystore, при `401 auth.unauthenticated` — один `refreshSession` и повтор; веб — через BFF;
  правило `device`; ссылки на операции.

**Схемы** — Приложение 2, владелец «2» (в том числе общие для Task 3: `WebAuthnOptions`, `WebAuthnCredential`,
`PasskeyAssertion`, `MfaMethod`, `DeviceInfo`, `DeviceIntegrity`, `DeviceIntegrityKind`).

**Коды** — `identity.PublicCodes` получает все коды операций этой задачи (Приложение 3, колонка «Задача 2»); тексты
`errors.identity.*` — `apps/web/messages/ru.json` (текст для игрока), `en.json` (пустые строки); `errors.identity.restricted`
(R9) — тоже здесь.

**Тесты:**
- `identity.test.mjs`, строка на операцию (24) → метод, путь, тег `identity`, анонимность, `IdempotencyKey` (есть у
  `logout` и всех мутаций «Аккаунта»; нет у анонимных), `x-rate-limit` (`auth_send` — `startSignup`,
  `resendAuthCode`, `startPasswordReset`, `startEmailChange`; `auth` — `confirmSignup`, `login`, `completeMfa`,
  `confirmPasswordReset`, `cancelDeletion`, `createDeviceChallenge`, `confirmEmailChange`; `auth_refresh` —
  `refreshSession`; `auth_check` — `checkNickname`; `account_secure` — `changeNickname`, `changePassword`,
  `deleteAccount`; у прочих — расширения нет), множество `x-error-codes`, статус и схема успеха, `Cache-Control`.
- `x-anonymous-mutations: ровно 10 операций Task 2` (Task 3 доведёт до 16) → множество = анонимные POST контракта,
  у каждой `security: []`.
- `device: правило в описаниях` → у 6 операций поле `device` в теле и упоминание `identity.device_required` в
  `description`; в `info.description` — абзац про `device`.
- `секреты и кэш` (R6, в этом файле): ответ с полями `access_token`, `refresh_token`, `mfa_token`, `signup_token`,
  `restore_token`, `secret`, `otpauth_uri`, `recovery_codes` на любой глубине ⇒ `Cache-Control` с примером
  `no-store`; у мутаций с замком и такими полями — `x-idempotency: no-store`.
- `TestIdentityCodesDocumented` → `x-error-codes` тега `identity` = `PublicCodes`; включается проверка «операций > 0».
- `TestNoStubOperations` (public) → 24 операции из `pendingOps` отвечают 501; `TestPendingOpsExpire` → зелёный.
- `TestOperationsImplementedByTaggedModule` → операции тега `identity` реализует `identity/httpapi.Handler`.
- `TestRateLimitClassesKnown` (public) → все классы известны.
- `node --test "contracts/test/*.test.mjs"` (docs, conventions, bundle), `apps/web` `error-codes.test.ts`,
  `messages.test.ts`.

**Подсадка.** (1) Убрать `security: []` у `login` — `identity.test.mjs` (анонимность) и conventions падают.
(2) Убрать `code_expired` из `x-error-codes` `confirmSignup` — строка таблицы-теста и `TestIdentityCodesDocumented`
падают. (3) `Cache-Control: private, no-store` у `login` — тест секретов и кэша падает. (4) Удалить
`errors.identity.code_invalid` из ru — `error-codes.test.ts` падает. (5) Убрать `confirmSignup` из `pendingOps` —
`TestNoStubOperations` падает (501 вне списка).

**Приёмка.** 24 строки контракта = таблица спеки (ревьюер сверяет тест со спекой); описания отвечают на «что делать
клиенту» по каждому коду; Swagger (`/docs`) читается без вопросов (`docs.mjs` зелёный); ни одно описание не раскрывает
существование аккаунта.

**Ловушки.**
- `updateMe` — `PATCH` с `{locale}`: схема `MeUpdate` с `minProperties: 1`, `additionalProperties: false` (как тела
  правок плана B), `locale` — enum `ru | en` открытый.
- Параметры путей public (`sessionId`, `codeId`, позже `identityId`, `passkeyId`, `applicationId`) — компоненты
  `components.parameters` в `public/root.yaml` под теми же именами, что в admin (`SessionId` и т. д.; контракты
  независимы, совпадение имён не мешает), uuid с примером.
- `completeMfa` — «ровно одно из `code`, `recovery_code`, `passkey`»: `oneOf` из трёх схем с `required` и
  `additionalProperties: false` либо одна схема с описанием и проверкой в сценарии. kin-openapi валидирует `oneOf`, а
  openapi-typescript и Dart-генератор нативщика хуже переносят `oneOf` без `discriminator` — **R11: одна схема,
  поля необязательные, правило «ровно одно» — в `description` и сценарии (`422 validation.failed`)**. То же —
  `reauthenticate` (Task 3).
- `startSignup.accepted_documents` — массив `AcceptedDocument` (`document`, `version`), `minItems: 2`? Нет: число
  документов — данные (`getLegalDocuments`), не схема; `minItems: 1` и правило «все текущие» — `documents_outdated`.
- Даты: `birth_date` — `format: date`; все моменты — `date-time` UTC.
- `refreshSession` — анонимная, но токен в теле: описание — «не передавайте `Authorization`» не нужно (анонимные его
  не читают — `info.description`); нужно — «вызывайте последовательно, не параллельно, на одно устройство».

### Task 3: public — 2FA, ключи доступа, соцвходы, способы входа, свежий вход, сброс 2FA игроком

**Цель.** 22 операции этапов 5, 7, 8 спеки и `getAuthMethods`; `x-anonymous-mutations` доведён до 16; флаги
способов входа описаны в операциях; ответы с секретами — `x-idempotency: no-store` на public (R6).

**Риск:** B.

**Файлы.** Те же, что Task 2 (без создания).

**Операции** — строки §6.3:

| operationId | Метод и путь | Анонимно | Флаг (§4) |
| --- | --- | --- | --- |
| `getAuthMethods` | `GET /v1/auth/methods` | да (GET) | — (отражает флаги) |
| `startMfaPasskey` | `POST /v1/auth/mfa/passkey-options` | да | `auth_passkeys_login` |
| `createSocialNonce` | `POST /v1/auth/social/nonces` | да | провайдера: `auth_<p>` или `auth_<p>_login` (R12) |
| `socialLogin` | `POST /v1/auth/social` | да | `auth_<p>_login` вход; `auth_<p>` — новый аккаунт и автопривязка |
| `completeSocialSignup` | `POST /v1/auth/social/signup` | да | `auth_<p>` |
| `startPasskeyLogin` | `POST /v1/auth/passkey/options` | да | `auth_passkeys_login` |
| `passkeyLogin` | `POST /v1/auth/passkey` | да | `auth_passkeys_login` |
| `reauthenticate` | `POST /v1/me/reauth` | нет | ключом — `auth_passkeys_login`; соцвходом — `auth_<p>_login` |
| `startReauthPasskey` | `POST /v1/me/reauth/passkey-options` | нет | `auth_passkeys_login` |
| `listMyAuthMethods` | `GET /v1/me/auth-methods` | нет | — |
| `linkIdentity` | `POST /v1/me/identities` | нет | `auth_<p>` |
| `unlinkIdentity` | `DELETE /v1/me/identities/{identityId}` | нет | — (при любых флагах, §4.3) |
| `getMyMfa` | `GET /v1/me/mfa` | нет | — |
| `setupTotp` | `POST /v1/me/mfa/totp/setup` | нет | — |
| `confirmTotp` | `POST /v1/me/mfa/totp/confirm` | нет | — |
| `disableTotp` | `DELETE /v1/me/mfa/totp` | нет | — |
| `regenerateRecoveryCodes` | `POST /v1/me/mfa/recovery-codes` | нет | — |
| `startPasskeyRegistration` | `POST /v1/me/passkeys/registration-options` | нет | `auth_passkeys` |
| `registerPasskey` | `POST /v1/me/passkeys` | нет | `auth_passkeys` |
| `renamePasskey` | `PATCH /v1/me/passkeys/{passkeyId}` | нет | — |
| `deletePasskey` | `DELETE /v1/me/passkeys/{passkeyId}` | нет | — (при любых флагах) |
| `cancelMyMfaReset` | `DELETE /v1/me/mfa-reset` | нет | — |

Сверх таблицы:
- **Флаг выключен** → `403 feature.disabled` (общий код): `description` каждой операции с флагом называет ключ и
  различие «основной / аварийный» (§4.3). `getAuthMethods` — смысл каждого поля `{signup, login}` / `{register,
  login}`, «Google выключен, если выключен Apple» (§4.2), `Cache-Control: public, max-age=60`, что клиент показывает
  (§4.4).
- **Секреты (R6).** `setupTotp`, `confirmTotp`, `regenerateRecoveryCodes` — `x-idempotency: no-store`,
  `Cache-Control: no-store`, `description`: «повтор с тем же ключом — `409 idempotency.replay_unavailable`, секрет
  второй раз не отдаётся». Остальные ответы — `private, no-store`; `createSocialNonce` — `no-store`.
- **Свежий вход** (колонка С): `linkIdentity`, `unlinkIdentity`, `setupTotp`, `disableTotp` (вторым фактором),
  `regenerateRecoveryCodes`, `startPasskeyRegistration`, `deletePasskey` — абзац о `reauth_required` (§3.13).
- **Соцвход** — `description` `socialLogin` перечисляет ветки §3.5 (вход привязанного, автопривязка при подтверждённой
  почте, `signup_required`, `mfa_required` — 2FA соцвходом не обходится, `deletion_pending`); `apple.authorization_code`
  — зачем (отзыв токенов при удалении, §3.5). `completeSocialSignup` — PoW, `device_required` (у неё и у
  `socialLogin`, `passkeyLogin` — правило `device` Task 2).
- **WebAuthn** — `WebAuthnOptions.options` и `WebAuthnCredential` — `type: object, additionalProperties: true` с
  правдоподобным примером W3C L3 (`challenge`, `rpId`, `allowCredentials` / `user`, `pubKeyCredParams`); описание —
  «передать платформенному API как есть», `challenge_id` — вернуть с ответом.
- **`reauthenticate`** — «ровно одно из» `password | totp_code | recovery_code | passkey | social` (R11); при
  включённой 2FA пароль — `422 identity.reauth_method_not_allowed`; ответ `{reauth_until}`.
- **`cancelMyMfaReset`** — `204` и при уже отменённом сбросе сессией со вторым фактором (§3.15); описание
  предупреждения о сбросе (`MfaStatus.reset_scheduled_at`).

**Схемы** — Приложение 2, владелец «3». **Коды** — Приложение 3, колонка «Задача 3»; тексты веба (ru, en — пустые).

**Тесты:**
- `identity.test.mjs`, строки 22 операций (метод, путь, анонимность, `IdempotencyKey`, `x-rate-limit`: `auth` — шесть
  анонимных POST; `account_secure` — `reauthenticate`, `startReauthPasskey`, `linkIdentity`, `unlinkIdentity`,
  `setupTotp`, `confirmTotp`, `disableTotp`, `regenerateRecoveryCodes`, `startPasskeyRegistration`, `registerPasskey`,
  `deletePasskey`, `cancelMyMfaReset`; прочие — без расширения), коды, статусы, `Cache-Control`, `x-idempotency`.
- `x-anonymous-mutations — ровно 16` → список = все POST с `security: []` в public; `logout` не в списке.
- `x-idempotency: no-store — ровно у трёх операций с секретами` (public) → `setupTotp`, `confirmTotp`,
  `regenerateRecoveryCodes`; другое значение `x-idempotency` — ошибка (как `permissions.mjs` для admin).
- `флаги в описаниях` → у 11 операций с флагом (все строки таблицы с ключом, кроме `getAuthMethods`) `description` содержит ключ флага и `feature.disabled`.
- `device: правило в описаниях` — расширить до всех 9 операций §6.2.3: + `socialLogin`, `completeSocialSignup`,
  `passkeyLogin` (поле `device` в теле и `identity.device_required` в `description` и `x-error-codes`); пропущенное
  поле у `passkeyLogin` — падение.
- `TestIdentityCodesDocumented`, `TestNoStubOperations` (46 в `pendingOps`), `TestPendingOpsExpire`,
  `TestZeroHandlerNotImplemented`; веб — `error-codes.test.ts`.
- Проверка **маршрута** public (§18, пункт `x-idempotency`): `TestPublicRouteNoStore` (`internal/httpapi/public`) →
  маршрут `setupTotp` роутера контракта public → расширение читается как `no-store` (тот же признак, что
  `idempotency.noStore`). Это проверка маршрута, не конвейера: в I-1 реализованной мутации с замком в public нет.
  Конвейер маршрут видит (ревью: `public/handler.go:85-95` ставит `routes` до `idempotency.Middleware`); сквозной тест
  «повтор → `409 replay_unavailable`, тело не сохранено» — этап 5 (TOTP), строка в «Передаче следующим планам».

**Подсадка.** (1) Убрать `x-idempotency: no-store` у `confirmTotp` — тест секретов падает. (2) `security: []` у
`linkIdentity` — тест анонимных мутаций падает. (3) Лишний `identity.passkey_none` у `passkeyLogin` —
`TestIdentityCodesDocumented` не падает (код в списке), падает строка таблицы-теста — значит строка обязана сверять
множество кодов точно. (4) Убрать `auth_passkeys` из описания `registerPasskey` — тест флагов падает.

**Приёмка.** 22 строки = §6.3; итого public: 46 операций тега `identity` + 4 Task 4; описания флагов совпадают с §4.3
(основной — только новые регистрации и привязки; аварийный — вход всем); секреты не попадают в хранилище ключей.

**Ловушки.**
- `createSocialNonce {provider}` — R12: флаг, который проверяет выдача nonce: nonce нужен и для входа, и для
  регистрации, поэтому nonce выдаётся, если включён **хотя бы один** из флагов провайдера (`auth_<p>` или
  `auth_<p>_login`); окончательную проверку делает `socialLogin`.
- `SocialProvider` — открытый enum `apple | google`; незнакомый провайдер от старого клиента — `422 validation.failed`
  валидатора (enum в теле запроса закрыт для сервера по смыслу; `x-extensible-enum` касается чтения ответов).
- Пути `/v1/auth/passkey/options` и `/v1/auth/passkey` — разная глубина; `/v1/auth/mfa` и
  `/v1/auth/mfa/passkey-options` — тоже; маршруты не перекрываются, но строка `TestNoStubOperations` на каждый путь
  проверит это вживую.
- `DELETE /v1/me/mfa/totp` и `DELETE /v1/me/mfa-reset` — без тела; у `DELETE` с Idempotency-Key — параметр есть
  (conventions требует у всех мутаций с замком).

### Task 4: капитанство public, админка identity, связь «сотрудник → игрок», коды флагов, действие `identity.mfa_reset`

**Цель.** 4 публичные операции капитанства и 11 админских (9 тега `identity` §7.1, 2 тега `staff` §7.6) — в
контрактах; коды §7.1/§7.3/§7.6 и платформы флагов §7.5 — константами со стражами; действие подтверждения
`identity.mfa_reset` в enum `ApprovalAction` с подписью; тексты админки (ru и en — перевод).

**Риск:** A для части с матрицей прав (`x-permission` 11 операций, строки ролей `TestPermissionsAllOperations`,
`ApprovalAction` + исполнитель в описании approve — категории 7 и 10, таблица режимов отказа по ним, прочие категории
— «н/п»: поведения нет); B для остального (скоуп модератора закладывается описаниями; `self_action` — инвариант К3).
Перед стартом — предварительный шаг контроллера Task 1 (пауза фронтовой ветки `apps/admin`).

**Файлы.** Создать `contracts/openapi/admin/identity/{paths,schemas}.yaml`. Изменить: `contracts/openapi/public/identity/*`,
`contracts/openapi/public/root.yaml`, `contracts/openapi/admin/root.yaml` (тег, пути, схемы, параметры
`ApplicationId`, `UserId`, `ResetId`), `contracts/openapi/admin/staff/{paths,schemas}.yaml` (операции связи,
`StaffPlayerLink`, поле `StaffAccount.player_link` — R15), `contracts/openapi/admin/platform/{paths,schemas}.yaml`
(`ApprovalAction` += `identity.mfa_reset`; `updateFeatureFlag.x-error-codes` += два кода),
`contracts/test/{identity,admin}.test.mjs`, `backend/internal/identity/codes.go`,
`backend/internal/identity/{httpapi,admin}/handler.go`, `backend/internal/staff/codes.go` и
`backend/internal/staff/admin/handler.go` (два метода-заглушки 501), `backend/internal/platform/httpx/codes.go`
(`AdminFlagCodes`), `backend/internal/httpapi/admin/{nostub_test.go,permissions_test.go}` (строки `opsBase`, роли),
`apps/admin/messages/{ru,en}.json`, `apps/web/messages/{ru,en}.json`; генерация.

**Операции public** (§6.3 «Капитанство»): `getMyCaptaincy` `GET /v1/me/captaincy` → `200 Captaincy`;
`listMyCaptainApplications` `GET /v1/me/captain-applications?cursor=&limit=` → `200 CaptainApplicationPage`
(`platform/page` v1); `submitCaptainApplication` `POST /v1/captain-applications` → `201 CaptainApplication`, коды
`captain_ineligible` 409 (+`reasons`), `application_exists` 409, `application_cooldown` 409 (+`available_at`);
`withdrawCaptainApplication` `POST /v1/captain-applications/{applicationId}/withdraw` → `200 CaptainApplication`,
коды `application_not_found` 404, `application_closed` 409. Все — с замком, класс `default`, мутации — с
`IdempotencyKey`, ответы `private, no-store`. `description` `submitCaptainApplication` — причины §3.12 п.3, включая
`checks_unavailable` (до модулей `players`/`teams` заявку подать нельзя — честно, К1), город — текущий город игрока,
телефон необязателен и не подтверждается.

**Операции admin** — Приложение 1 (operationId, путь, `x-permission`, тело → ответ, коды). Сверх таблицы:
- Мутации — `IdempotencyKey` и `AdminHeader`; чтения — `Cache-Control: private, no-store` (`CachePrivate`).
- Скоуп модератора: у операций с правом `city` город не в пути — `description` фиксирует: «модератору — только заявки
  его городов; чужая — `404 identity.application_not_found`, в списке её нет» (§7.1).
- `getCaptainApplicationContact` и `findPlayerByEmail` — причина обязательна всем (3–200 символов), «чтение пишется в
  журнал с причиной» (U6). ПД и причина — **в заголовках, не в query** (R19, CWE-598): `findPlayerByEmail` —
  `X-WF-Lookup-Email` (почта) и `X-WF-Access-Reason`; `getCaptainApplicationContact` — `X-WF-Access-Reason`.
  Параметры `in: header`, `required: true`, компоненты `LookupEmail` и `AccessReason` в `admin/root.yaml`; операции
  остаются GET; ответ — `Cache-Control: private, no-store`. **Кодировка (ревью плана I4):** значение заголовка —
  UTF-8 в percent-encoding (RFC 3986; клиент — `encodeURIComponent`), иначе браузер бросает `TypeError` на кириллице
  ещё до запроса (значение заголовка в Fetch — байтовая строка). У обоих компонентов — `description` (что
  передаётся, «percent-encoding UTF-8», пределы после декодирования, зачем не query — CWE-598), `example` в
  закодированном виде (`AccessReason`: `'%D0%BE%D0%B1%D1%80%D0%B0%D1%89%D0%B5%D0%BD%D0%B8%D0%B5%204812'`;
  `LookupEmail`: `'ivan.petrov%40mail.example'`), `schema.pattern: '^[A-Za-z0-9%._~!*()''-]+$'`, `maxLength`
  закодированной формы (`AccessReason` — 2400 = 200 символов × до 12 байт; `LookupEmail` — 3048 = 254 × 12).
  Сервер декодирует; негодная кодировка (битая `%`-последовательность или не UTF-8) → `400 validation.failed` по
  полю `header.X-WF-Access-Reason` / `header.X-WF-Lookup-Email`; пределы после декодирования (причина 3–200
  символов, почта ≤ 254) — в сценарии, `422 validation.failed` по тому же полю.
- `requestMfaReset` — `202 ApprovalAccepted` (существующая схема), «второй сотрудник подтверждает во «Входящих»;
  после подтверждения — 7 дней ожидания с письмом владельцу» (§3.15); `cancelMfaReset` — сразу, по тому же праву
  (§7.2: отмена не требует второго).
- `self_action` (К3, §7.6) — у `approveCaptainApplication`, `rejectCaptainApplication`, `requestMfaReset`,
  `cancelMfaReset`: «цель — ваша собственная учётка игрока (связь «сотрудник → игрок»)».
- `rejectCaptainApplication` — `comment` обязателен при `rejection_code = other`: правило в `description`, нарушение —
  `422 validation.failed` по `body.comment` (код поля `required`, без нового кода — как правила плана B).
- `getAuthMethodsUsage` — для экрана флагов (§7.4): «сколько игроков входят только этим способом».
- `ApprovalAction` += `identity.mfa_reset` (`x-extensible-enum`), подпись `approvals.actions.identity.mfa_reset`;
  описание `approveApproval` — исполнитель `identity.mfa_reset` может отказать `409 platform.approval_stale`
  (у игрока уже нет 2FA, сброс уже назначен **или подтверждающий связан с этим игроком**, §7.6: код модуля
  превращается в stale через `moduleCode`, `admin/server.go:197-205`) — `x-error-codes` approve не растёт (как R4
  плана B).
- Связь «сотрудник → игрок» (R15): `description` `putStaffPlayerLink`/`deleteStaffPlayerLink` — побочные эффекты §7.6
  (аудит, письмо сотруднику); `StaffAccount.player_link` — **необязательное** поле (не в `required`: типизированные
  фикстуры админки `apps/admin/src/test/staff.ts:16`, `src/ui/table/DataTable.test.tsx:23` не ломаются),
  описание: «только для владельца (право `staff.player_links.manage`); у остальных поля нет; до этапа 9 спеки identity
  сервер поле не отдаёт никому».
- Тест (R19): строки `admin.test.mjs` `findPlayerByEmail` и `getCaptainApplicationContact` → параметры
  `X-WF-Lookup-Email`/`X-WF-Access-Reason` `in: header`, обязательные; в query у этих операций нет `email` и `reason`;
  подсадка — вернуть `email` в query → строка падает. Строка `TestNoStubOperations`/`opsBase` — запрос с заголовками
  (без них валидатор отвечает 400 и до заглушки не доходит).
- Тест кодировки (I4): `admin.test.mjs` → у `LookupEmail` и `AccessReason` есть `description` с «percent-encoding
  UTF-8», `example`, удовлетворяющий `pattern`, `pattern` не пропускает кириллицу и пробел, `maxLength` 2400/3048;
  `TestLookupHeadersEncoding` (`internal/httpapi/admin`, через конвейер до заглушки) → закодированная кириллическая
  причина `обращение 4812` → проходит валидатор (501 заглушки); сырая кириллица → `400 validation.failed`
  (`header.X-WF-Access-Reason`, `pattern`); битая кодировка (`%D0%`) проходит `pattern` — её отказ (400 по полю
  заголовка) и пределы после декодирования — тест сценария на этапе 9 (строка «Передачи следующим планам»). Подсадка —
  убрать `pattern` у `AccessReason` → строка `admin.test.mjs` падает.
- Флаги (§7.5): `httpx.AdminFlagCodes` += `platform.flag_global_only` (422), `platform.flag_requires` (409);
  `updateFeatureFlag` — `x-error-codes` и описание (`GlobalOnly` — город не принимается; `auth_google` требует
  `auth_apple`; `detail` — зависимость). Enum `FeatureFlagKey` — **не здесь** (R4).

**Схемы** — Приложение 2, владелец «4». **Коды** — Приложение 3, колонка «Задача 4»: `identity.PublicCodes` +=
капитанские; `identity.AdminCodes` = все `identity.*` админки; `staff.Codes` += `staff.player_link_taken` (409),
`staff.player_not_found` (422, R15) — `TestStaffCodesDocumented`.

**Тесты:**
- `identity.test.mjs` — 4 строки капитанства; итог: 50 операций тега `identity` в public, ровно множество §6.3.
- `admin.test.mjs`: `65 операций: 33 плана A, 21 плана B, 11 identity I-1 — и только они` (переименовать тест «54
  операции»); строка на каждую из 11 (метод, путь, тег, `x-permission`, коды, статус и схема, `AdminHeader` у
  мутаций); `ApprovalAction` содержит `identity.mfa_reset`; `x-anonymous-mutations` admin не изменился.
- `TestIdentityAdminCodesDocumented`, `TestStaffCodesDocumented`, `TestAdminPlatformCodesDocumented` (+ два кода
  флагов), `TestAdminCodePrefixesKnown`, `TestIdentityCodesDocumented`.
- `TestPermissionsAllOperations` (admin) → строки 11 операций: по ролям — владелец и админ проходят конвейер
  (`requestMfaReset` — до 202 после реализации; сейчас 501), модератор — `identity.captain_applications.*` и
  `identity.pii.read` проходят (`scoped`, города нет в пути — точная проверка в сценарии), `identity.mfa.reset` и
  `staff.player_links.manage` — 403; поддержка — чтения заявок и `pii.read` проходят, решения и сброс — 403.
- `TestNoStubOperations` (admin) → 11 операций в `pendingOps` отвечают 501; `TestPendingOpsExpire` → зелёный (две
  операции `staff` — разрешённое исключение (б)).
- `TestApprovalDecisionPermissionsAreDual` → зелёный; `labels.test.mjs` и `apps/admin` `labels.test.ts` →
  подпись `approvals.actions.identity.mfa_reset` есть; `error-codes.test.ts` (admin ru/en, web ru).

**Подсадка.** (1) `identity.self_action` убрать из `rejectCaptainApplication` — строка `admin.test.mjs` падает.
(2) Лишний код `identity.foo` в контракте admin — `TestIdentityAdminCodesDocumented` падает. (3) Убрать
`platform.flag_requires` из `AdminFlagCodes` — `TestAdminPlatformCodesDocumented` падает. (4) Удалить
`errors.identity.user_not_found` из admin en — `error-codes.test.ts` падает. (5) `x-permission: identity.mfa.reset`
у `getCaptainApplication` — страж `permissions.mjs` («GET не требует изменяющего права») падает.

**Приёмка.** Приложение 1 и контракт совпадают построчно; у каждой операции admin — `x-permission` из каталога;
описания закрывают скоуп модератора, причину чтения ПД и `self_action`; `./task contracts:breaking` чистый (поле
`StaffAccount.player_link` — добавление).

**Ловушки.**
- `cancelMfaReset` под `x-permission: identity.mfa.reset` (`dual`): конвейер для `dual` пропускает (решение «вдвоём»
  принимает сценарий) — проверить в `httpapi/admin/permission.go`; если конвейер на `dual` отвечает иначе — стоп и к
  контроллеру (не менять конвейер в I-1).
- `/v1/identity/captain-applications/{applicationId}` и `…/{applicationId}/contact|approve|reject` — разная глубина;
  `/v1/identity/users/lookup` и `/v1/identity/users/{userId}/mfa-reset` — тоже; строки `TestNoStubOperations` на
  каждый путь проверят маршрутизацию.
- Public: компонентов `Cursor`/`Limit` в `public/root.yaml` нет — завести (те же описания, что admin, страница
  `platform/page` v1) либо описать параметры в операции; один способ на весь public.
- `staff` — слой 0, `identity` — 1: `staff` не импортирует `identity` (ник игрока в `StaffPlayerLink` — через порт
  при сборке admin-api, R15; в I-1 только контракт).

### Task 5: заморозка, документы, передача нативщику

**Цель.** Сделать заморозку проверяемой (страж-хэш среза, R17), зафиксировать её в корнях и правилах, внести решения
R в спеку, обновить `docs/05` (проза), подготовить передачу нативщику, прогнать проверки контроллера.

**Риск:** B для стража заморозки (ошибка в выборе среза делает заморозку словесной — ревью плана I2), C для
документов.

**Файлы.** Создать `contracts/test/identity-freeze.test.mjs` (+ `contracts/src/freeze.mjs` — функция среза, если
исполнитель выносит её из теста). Изменить `contracts/openapi/public/root.yaml` и `admin/root.yaml` (комментарий
заморозки, `info.version` — R16), `backend/internal/httpapi/{public,admin}/nostub_test.go` (страж
`pendingOpsAtFreeze`), `.claude/rules/contracts.md`, `docs/05-ролевая-модель.md` (проза), спека; генерация (`./task
gen` после смены версий). Вне репозитория (не коммитится, `.superpowers/sdd/.gitignore` = `*`):
`.superpowers/sdd/2026-11-13-identity-1/native-handoff.md` — решение пользователя U1.

**Содержание.**
- **Страж заморозки (R17)** — `contracts/test/identity-freeze.test.mjs`:
  - **граница среза** (ревью плана I3, I5, o1) — содержимое хэшируется только у «своего»: операций среза и
    компонентов, которые ввёл I-1. «Своё» = все схемы `identity/schemas.yaml` (public и admin) **плюс явный список
    `FREEZE_OWN` в тесте** — то, что I-1 вводит в чужих файлах и корнях: схемы `StaffPlayerLink`,
    `StaffPlayerLinkPut` (`staff/schemas.yaml`), свойство `StaffAccount.properties.player_link` (вместе со схемой,
    на которую оно ссылается), поля `Problem` `reasons`, `methods`, `available_at`, `blocked_until`, описание
    `Retry-After` ответа `Problem`, компоненты корней `LookupEmail`, `AccessReason`, `ApplicationId`, `UserId`,
    `ResetId` (admin), параметры путей identity и `CacheNoStore`/`CachePrivate` (public, R5). Всё прочее, на что
    ссылается срез, — схемы других модулей и платформы (`ApprovalAccepted`, `Approval`, `ApprovalActor`,
    `StaffAccount` вне `player_link`, `AppPlatform`) и общие компоненты корней, которые I-1 не вводил
    (`IdempotencyKey`, `AdminHeader`, `Cursor`, `Limit` admin, прочие поля `Problem`), — входит **только строкой
    `$ref`** (или именем поля), обход в них не спускается. Enum `ApprovalAction` и `FeatureFlagKey` не входят вовсе
    (открытые, добавление значения не ломает — спека бэкенда §8.3, R4). `FREEZE_OWN` правится только вместе с
    хэшем; проверка «не вакуумен» требует, чтобы каждое имя `FREEZE_OWN` нашлось в бандле;
  - срез public: операции тега `identity` целиком (параметры, тела, ответы, заголовки, `summary`, `description`,
    `security` и **все** расширения операции — `x-error-codes`, `x-rate-limit`, `x-idempotency` и любые другие `x-…`;
    из хэша ничего не исключается), достижимые свои схемы, `Problem` и ответ `Problem`, `x-anonymous-mutations`,
    `x-error-codes-common`; из `info.description` — только раздел «Вход и токены» (вырезается по заголовку до
    следующего пункта) и строка про `identity.restricted`. `identity.test.mjs` проверяет, что раздел и строка есть
    (иначе срез пуст и страж вакуумен). Падение по `Problem`/`x-error-codes-common` — текст «общая часть public —
    затрагивает нативщика, запись в журнал обязательна»;
  - срез admin: 11 операций I-1 (тег `identity` и `put/deleteStaffPlayerLink`) по той же границе;
  - канонический JSON (ключи по алфавиту, порядок массивов сохраняется) → SHA-256 → сверка с константами
    `PUBLIC_IDENTITY_SHA256` и `ADMIN_IDENTITY_SHA256` в тесте; при расхождении — падение с текстом «правка
    замороженного контракта identity: только через контроллера, запись в «Журнал изменений после заморозки»
    `native-handoff.md`, новый хэш в этом тесте» и печать нового хэша;
  - `pendingOpsAtFreeze` (оба `nostub_test.go`) — отсортированный список и рядом константа его SHA-256;
    `TestPendingOpsExpire` (в) дополняется сверкой хэша: изменить замороженное множество (в том числе вернуть
    операцию после отката реализации при слиянии параллельных этапов) без правки константы нельзя; уменьшение
    `pendingOps` хэш не трогает.
- **Заморозка.** Комментарий в шапке обоих корней: «После I-1 (план identity) контракт identity заморожен и
  сторожится `contracts/test/identity-freeze.test.mjs`: правка — только через контроллера плана с записью в журнал
  изменений документа передачи нативщику; заранее согласованная — enum `FeatureFlagKey` с миграцией флагов (R4)».
  `info.version`: public `0.1.0 → 0.2.0`, admin `0.2.0 → 0.3.0` (R16).
- **`.claude/rules/contracts.md`:** `x-anonymous-mutations` public — закрытый список 16 операций identity с
  обоснованием; `x-idempotency: no-store` и `Cache-Control` на public (R5, R6, страж `identity.test.mjs`); поля
  `Problem` (`reasons`, `methods`, `available_at`, `blocked_until`); `http.not_implemented` — общий код обоих
  контрактов (R1); правило `device_required` (К5); коды `identity` — `PublicCodes`/`AdminCodes`/`CrossModuleCodes` и
  их стражи, префикс `identity.`; схемы admin — с префиксом `Identity`; «одна схема вместо `oneOf`» (R11);
  заглушки: строка «Заглушек нет…» (п. 32) заменяется на «временные `pendingOps` public и admin (план identity I-1,
  R2) — со стражем истечения; удаляются последней задачей плана identity»; заморозка и страж-хэш (R17); флаги
  `GlobalOnly` (§16): коды `platform.flag_global_only`/`platform.flag_requires` и правило «город у такого флага не
  принимается».
- **`docs/05`:** §5.2 — пометка про `teams.captains.approve` → ссылка на спеку identity §7.2; §5.3 — U6: причина при
  чтении ПД закреплена в контракте (`reason` у `identity.pii.read`-операций); U5: право `staff.player_links.manage`
  введено, исполнение — этап 9 спеки identity; §7 — действие `identity.mfa_reset`.
- **Спека:** решения R1, R3, R5, R10–R15, R17 — правки §6.3, §6.6, §7.1, §7.6, §15 с пометкой «(план I-1, R…)»;
  §16 (фикстуры approvals → `pitches.pitches.moderate`) уже поправлена при ревью плана.
- **Передача нативщику** — раздел ниже; файл `native-handoff.md` вне истории репозитория, пересылает пользователь
  (U1); в нём — хэш коммита заморозки, значения `PUBLIC_IDENTITY_SHA256` и пустой раздел «Журнал изменений после
  заморозки».
- **Проверки контроллера** (ворота перед ревью): `./task gen` → `git status` без изменений (сгенерированное
  совпадает); `node --test "contracts/test/*.test.mjs"`; `./task contracts:breaking`; `go test ./internal/httpapi/...
  ./internal/identity/... ./internal/staff/... ./internal/platform/...`; `./task web:test`, `./task admin:test`,
  typecheck веба и админки; `prettier --check` и `gofmt -l` по путям.

**Тесты:**
- `identity-freeze: срез public совпадает с замороженным` → бандл `public.yaml` → SHA-256 среза = константа.
- `identity-freeze: срез admin совпадает с замороженным` → бандл `admin.yaml` → SHA-256 = константа.
- `identity-freeze: срез не вакуумен` → в срезе public 50 операций, admin 11, `Problem`, `x-anonymous-mutations` и
  раздел «Вход и токены» — есть; чужие схемы представлены только `$ref`.
- `TestPendingOpsExpire` (public, admin) → хэш `pendingOpsAtFreeze` = константа.
- Все стражи задач 1–4 зелёные; `bundle.test.mjs` — бандлы совпадают со свежей сборкой.

**Подсадка.** Слишком узкий срез (страж обязан краснеть): (1) добавить код в `x-error-codes` `socialLogin`,
`./task gen` → `identity-freeze` падает (oasdiff и прочие стражи — зелёные: эту дыру закрывает R17). (2) Поменять
слово в `description` `getMe` — падает. (3) Сделать `apple.authorization_code` обязательным — падает. (4) Поменять
`maxLength` у поля `IdentityMfaResetRequest.reason` (своя схема admin) — падает; добавить значение в
`CaptainRejectionCode` — падает. (4b) Переименовать поле `StaffPlayerLink.linked_by_label` (схема в
`staff/schemas.yaml`) — падает; поменять `description` у `Problem.methods` или `example` у `AccessReason` — падает.
Слишком широкий срез (страж обязан молчать): (5) добавить значение в
`ApprovalAction` — зелёный; (6) добавить поле в `StaffAccount` вне `player_link` и в схему `Approval` — зелёный;
(7) дописать пункт в общий раздел `info.description` public (например, «Деньги») — зелёный; (7b) поменять
`description` компонента `Cursor` admin или `IdempotencyKey` — зелёный (o1). (8) Вернуть в
`pendingOpsAtFreeze` лишнюю операцию — `TestPendingOpsExpire` падает. Подсадки (1)–(7) — на бандле, возврат байт в
байт (`scripts/plant.mjs`).

**Приёмка.** Документы не расходятся с контрактом; передача нативщику отвечает на каждый пункт §14; память контроллера
(`project-setup-order`) — следующий шаг: план I-2.

## Передача нативщику (что именно)

| Что | Где | Как |
| --- | --- | --- |
| Бандл public | `contracts/openapi/public.yaml` на коммите заморозки (хэш коммита и `PUBLIC_IDENTITY_SHA256` — в документе) | файл из репозитория `A-mi13/wf-all`; после мержа — тег `contracts-v0.2.0` (U2: тег и пуш — только по команде пользователя) |
| Типы | Dart генерирует wf-native из бандла (в монорепо Dart-генерации нет — `docs/04`: «позже Dart») | требования к генератору: неизвестное значение enum не роняет разбор (`x-extensible-enum`); `oneOf` нет (R11); `WebAuthnOptions.options`, `WebAuthnCredential` — свободный JSON, передаётся платформенному API как есть; TS-типы `apps/web/src/api/gen/public.d.ts` — справочно |
| Документ «что изменилось против ваших моков» | `.superpowers/sdd/2026-11-13-identity-1/native-handoff.md` — вне истории репозитория, не коммитится (U1) | пересылает пользователь; пункты ниже |
| Поведение | спека §3, §6 (ссылки в документе) | описания операций в Swagger `/docs` стенда |

Документ (по §14, без пересказа контракта):
1. Основной вход — почта + пароль; код из письма — только регистрация, восстановление пароля, смена почты (код —
   только на новую, на старую — уведомление). Моки «код + Apple/Google» заменить.
2. Apple, Google, ключи доступа — есть, за флагами: кнопки — по `getAuthMethods` (`{signup, login}`,
   `{register, login}`), кэш 60 с.
3. Регистрация: город, дата рождения, почта, пароль, ник, согласия (`getLegalDocuments`) → код 6 цифр, повтор
   через 60 с (`resend_available_at`); экран «примите новую версию» по `documents_acceptance_required`.
4. Ветки `AuthResult.status`: `mfa_required` (`completeMfa`, предупреждение `mfa.reset_at`), `signup_required`
   (`completeSocialSignup`), `deletion_pending` (экран «Отменить удаление?», 30 дней).
5. Обязательства клиента: `device` на 9 ручках входа и регистрации (иначе `422 identity.device_required`); refresh — в
   Keychain/Keystore, при `401 auth.unauthenticated` один `refreshSession` и повтор, обновления последовательно;
   `Idempotency-Key` на мутациях с замком; `403 humancheck.required` — ALTCHA; `Retry-After`; поля `Problem`
   (`attempts_left`, `reasons`, `methods`, `available_at`, `blocked_until`); незнакомые значения enum.
6. Капитан: телефон необязателен и не подтверждается, порог — `min_signup_age` страны (в России 14), экрана
   «Подтверждение телефона» нет; до модулей `players`/`teams` заявка отвечает `captain_ineligible` с
   `checks_unavailable`.
7. «Устройства и сеансы» → `listMySessions`, «выйти здесь» / «выйти везде»; дата рождения и город в настройках не
   меняются.
8. Что когда заработает на стенде — таблица §14; до этого операция отвечает `501 http.not_implemented` (R1) — моки по
   типам.
9. Таблица «экран карты → operationId» для экранов входа, регистрации, настроек безопасности, капитана.
10. Ответ на Б-01 (`docs/WF_…_2026_10_19.md`) — ссылка на этот документ; сам файл нативщика не правим и не коммитим.
11. Вопросы нативщику: переносит ли его Dart-генератор `type: object, additionalProperties: true`
    (`WebAuthnOptions.options`) и открытые enum в теле запроса (`SocialProvider`, `Locale`); `Problem.reasons` —
    строки двух словарей (`CaptainIneligibility` и причины `password_weak`) — ветвиться по `code`, затем по значению.
12. Раздел «Журнал изменений после заморозки» — пуст при передаче; каждая согласованная контроллером правка среза
    identity (новый хэш R17) — строка: дата, операция, что изменилось, ломающее ли для клиента.

## Порядок исполнения

```
Шаг 0 (контроллер): пауза фронтовой ветки apps/admin (m13)
Task 1 → Task 2 → Task 3 → Task 4 → Task 5      (строго по очереди)
Исполнитель А: 1 + 2      Исполнитель Б: 3 + 4 + 5 (страж заморозки R17; документы — с контроллером)
Ревью: одно на группу А (после 2), одно на группу Б (после 5)
```

- Страж заморозки (R17) — последним, после того как срез окончателен: его хэш фиксирует итог задач 1–4.

- Параллели нет (R7): задачи 2–4 правят одни и те же файлы (`public/identity/*.yaml`, `public/root.yaml`,
  `identity/codes.go`, `nostub_test.go`, `apps/web/messages`), а `./task gen` перегенерирует оба бандла и типы — два
  pnpm-процесса запрещены. I-1 короткий: последовательность дешевле синхронизации.
- Каждая задача оставляет репозиторий зелёным: новые операции сразу в `pendingOps`, коды — вместе с текстами.
- Коммит — контроллер, один на задачу (сообщения — в отчётах исполнителей). Пример: «I-1 Task 1: права identity,
  классы лимитов, Problem, общий 501, каркас identity и стражи заглушек».
- После Task 5 — ревью ветки (wf-reviewer, opus, effort high), волна исправлений, полный `./task ci` (постоянная
  просьба пользователя 05.10), пуш и PR — по команде пользователя. Передача нативщику — после мержа (бандл с `main`).

## Решения плана

Решения, которых нет в спеке или которые её уточняют; каждое, меняющее контракт, вносится в спеку в Task 5.

- **R1. `http.not_implemented` — общий код обоих контрактов.** Public собирается компилятором из хендлеров модулей
  (`oapi.StrictServerInterface`): операции identity обязаны иметь методы с первого коммита, а до реализации отвечают
  501. Код 501 был только в admin (`AdminCommonCodes`); оставить его неописанным в public — нарушить правило «каждый
  код в контракте», а временный код — заморозить контракт с мусором. Перенос в `PlatformCodes` — добавление (oasdiff
  чист), полезен и дальше: этапы выкатываются по очереди, клиент отличает «ещё нет» от «сломалось».
- **R2. Заглушки — явные списки со стражем истечения.** Public: стража «без 501» не было (§18 — проверено: в
  `internal/httpapi/public` его нет) — вводится по образцу admin сразу со списком 50 операций. Admin: страж есть
  (`TestNoStubOperations`, список плана B удалён) — временный `pendingOps` (11). Истечение: имена существуют; только
  тег `identity` (+ 2 операции `staff` §7.6); подмножество замороженного `pendingOpsAtFreeze`; реализованная операция
  в списке — падение. Удаление списков и `TestPendingOpsExpire` — последняя задача последнего плана identity (этапы
  9–10). Срок датой не задаётся: тест с датой — бомба замедленного действия в CI.
- **R3. `Problem.methods` и `reasons` для `password_weak`.** Спека кладёт `methods` у `mfa_required` и `reason` у
  `password_weak` «в `detail`», но `detail` — текст для разработчика, клиенту разбирать его запрещено
  (`info.description`). `methods` — новое необязательное поле (значения `MfaMethod`), `password_weak` отдаёт причину
  в уже вводимом `reasons` (одно значение). Без этого экран сброса пароля не знает, какой фактор спросить.
  Уточнения ревью плана: `methods` — только факторы, которые принимает эта операция (у `confirmPasswordReset` — без
  `passkey`, §3.4, m4); `blocked_until` — без `nullable`, «поля нет — бессрочно» (`httpx.Problem` — `omitempty`, m5).
- **R4. Enum `FeatureFlagKey` — в коммите миграции флагов, не в I-1.** `TestFlagKeysMatchContract` сверяет enum с
  `feature_flags` после миграций **на равенство**: шесть ключей §4.1 без миграции уронят тест. Добавление значения в
  enum ответа — не ломающее (`oasdiff-severity`: `response-property-enum-value-added info`). Это единственная заранее
  согласованная правка замороженного контракта: миграция + строки enum + `flags.<ключ>.{title,note}` — одним
  коммитом этапа «Схема». Коды `platform.flag_global_only`/`flag_requires` вводятся в I-1 (Task 4).
- **R5. `Cache-Control` у каждого 2xx с телом в identity.** Токен-ответы обязаны быть некэшируемыми (RFC 6749 §5.1:
  `no-store`); спека задаёт заголовок только у двух GET. Значения: `public, max-age=60` (`getAuthMethods`),
  `public, max-age=300` (`getLegalDocuments`), `no-store` (ответы с токенами, nonce и секретами), прочее —
  `private, no-store`. Компоненты `CacheNoStore`, `CachePrivate` — в `public/root.yaml` (как в admin).
- **R6. Страж секретов public — в `identity.test.mjs`.** `permissions.mjs` проверяет `x-idempotency` только в admin.
  Тот же признак (поля `secret`, `otpauth_uri`, `recovery_codes`; плюс токены для `Cache-Control`) на public — тестом
  контракта; работу расширения в конвейере public подтверждает `TestPublicRouteNoStore` (§18; сквозной повтор — этап 5).
- **R7. Без параллели.** См. «Порядок исполнения».
- **R8. Порядок строк каталога прав:** блок `identity` — после `platform`, `staff.player_links.manage` — последней
  строкой блока `staff`; черновые строки — как были, без `teams.captains.approve` и `identity.pii.read`.
- **R9. `identity.restricted` — список `CrossModuleCodes`.** Код отдают операции других модулей (§6.6), а стражи сверки
  identity требуют равенства списка и `x-error-codes` тега `identity`. Третий список: константа есть, в операциях
  identity её нет (страж), операции модулей ссылаются на неё с появлением. Текст `errors.identity.restricted` — в вебе
  сразу; в `info.description` public — строка о коде.
- **R10. `confirmSignup` получает `device_required` 422.** §6.2.3 перечисляет `confirmSignup` среди операций с
  обязательным `device`, а таблица §6.3 кода не даёт — правило §6.2.3 (К5) важнее пропуска в таблице.
- **R11. «Ровно одно из» — одна схема, не `oneOf`.** `completeMfa`, `reauthenticate`, `confirmPasswordReset.mfa`:
  поля необязательные, правило — в `description`, нарушение — `422 validation.failed` из сценария (поле `body`).
  `oneOf` без дискриминатора плохо переносят openapi-typescript и генераторы Dart, а сообщения валидатора по `oneOf`
  невнятны.
- **R12. Выдача nonce соцвхода** (`createSocialNonce`) — при включённом **любом** из флагов провайдера (основном или
  аварийном): nonce нужен и входу, и регистрации; окончательная проверка — в `socialLogin` / `linkIdentity`.
- **R13. Ответы решений по заявке** (`approveCaptainApplication`, `rejectCaptainApplication`) — `200
  IdentityCaptainApplication` (решённая заявка): экрану не нужно перечитывать карточку; спека пишет просто `200`.
- **R14. Коды `requestMfaReset`:** `identity.user_not_found` 404, `identity.self_action` 409,
  `identity.mfa_not_enabled` 409 (у игрока нет 2FA — сбрасывать нечего; код уже есть в §6.5), новый
  `identity.mfa_reset_pending` 409 (живой сброс уже назначен — частичный UNIQUE `mfa_resets`, §5.3). Спека §7.1
  кодов операции не перечисляет.
- **R15. Связь «сотрудник → игрок» в контракте.** `putStaffPlayerLink` / `deleteStaffPlayerLink` (§7.6; operationId —
  этот план): `PUT {user_id}` → `200 StaffPlayerLink {user_id, nickname, linked_at, linked_by_label}`; `DELETE` →
  `204`, повтор без связи — тоже `204`. Коды: `staff.not_found` 404 (есть), `staff.player_link_taken` 409 (§7.6),
  новый `staff.player_not_found` 422 (игрока с `user_id` нет: `staff` не может отдавать `identity.*`, слой 0).
  Карточка показывает связь через новое **необязательное** поле `StaffAccount.player_link` (не в `required`; есть
  только у владельца, у остальных поля нет — отсутствие у владельца значит «связи нет»; до этапа 9 сервер поле не
  отдаёт; m6). Побочные эффекты PUT/DELETE — аудит и письмо сотруднику (§7.6) — в `description`. `nickname` при
  реализации даёт порт identity, переданный `staff` при сборке admin-api.
- **R16. Версии контрактов** — public `0.2.0`, admin `0.3.0` (минорная: только добавления); тег — U2.
- **R17. Заморозка — страж-хэш среза** (ревью плана I2). `contracts:breaking` видит только ломающее, до
  `contracts-v1.0.0` ломающая правка public проходит строкой в `oasdiff-err-ignore-public.txt`, а `x-error-codes`,
  `x-anonymous-mutations`, `x-rate-limit`, `x-idempotency` и описания oasdiff не видит вовсе. `identity-freeze.test.mjs`
  (Task 5) хэширует канонический срез identity public и 11 операций admin; изменение — красный тест, новый хэш —
  только осознанной правкой через контроллера с записью в «Журнал изменений после заморозки» документа передачи.
  `pendingOpsAtFreeze` — под тем же приёмом (хэш списка). Срок стража: после реализации всех этапов identity срез
  перестаёт быть «замороженным до кода» — последняя задача последнего плана identity решает с пользователем,
  оставить страж (до `contracts-v1.0.0`) или удалить вместе с `pendingOps`.
- **R18. Значения перечислений, которых нет в спеке** (m10): `IdentityAuthMethodsUsage.items[].method` — enum
  `IdentityAuthMethodKind` = `apple | google | passkeys` (как в §7.1 и ключах флагов `auth_passkeys`; намеренно
  отличается от public `AuthMethod.passkey` — это «способ входа в статистике флагов», а не способ входа сессии;
  описание схемы называет различие); `IdentityCaptainApplicationCard.checks[].check` — enum
  `IdentityCaptainCheck` = `age | restrictions | profile | captaincy` (галочки §7.4), `state` — enum
  `IdentityCheckState` = `ok | failed | unavailable`.
- **R19. ПД поиска и причина чтения — в заголовках, не в query** (ревью плана m16, решение контроллера 13.11;
  CWE-598). `findPlayerByEmail` остаётся `GET /v1/identity/users/lookup`, почта — заголовок `X-WF-Lookup-Email`,
  причина — `X-WF-Access-Reason`; `getCaptainApplicationContact` — причина в `X-WF-Access-Reason`. URL (и журналы
  прокси, CDN, Logpush) не несут ни почты, ни свободного текста сотрудника. POST-поиск отвергнут: обязательные
  `Idempotency-Key`/`AdminHeader` и сохранение ответа с почтой в `staff_idempotency_keys`. Заголовки — того же
  источника, CORS не мешает (`X-WF-Admin` уже ходит заголовком); kin-openapi и openapi-fetch `in: header`
  поддерживают. Значения — UTF-8 в percent-encoding (RFC 3986, `encodeURIComponent`; ревью плана I4): значение
  заголовка Fetch — байтовая строка, кириллица без кодирования не уходит из браузера. В контракте — `pattern` и
  `maxLength` закодированной формы (2400 / 3048), пример закодированный; сервер декодирует, битая кодировка → `400
  validation.failed` по полю заголовка; пределы после декодирования (причина 3–200, почта ≤ 254) — сценарий. Спека
  §7.1 поправлена при ревью плана.
- **R20. `linkIdentity` → `201 LinkedIdentity`; тело `passkeyLogin` — `PasskeyLoginRequest`** (ревью группы Б,
  решение контроллера 13.11). Спека §6.3 пишет `201 {identity}`; контракт отдаёт саму привязку схемой
  `LinkedIdentity` — как `registerPasskey → 201 Passkey`, экрану «Способы входа» не нужна обёртка.
  `PasskeyAssertion` закрыт `additionalProperties: false`, а `passkeyLogin` несёт и `device` (§6.2.3) — тело
  операции — новая схема `PasskeyLoginRequest` (`challenge_id`, `credential`, `device?`). Обе — в Приложении 2;
  спеку (§6.3, реестр схем) правит Task 5.
- **Ревью плана:** minor m1–m17 приняты и внесены (m16 — в виде R19, а не POST); раунд 2 — I3 и n1–n3 внесены.

## Решения пользователя

- **U1 (ответ на Q1, 13.11).** Документ передачи нативщику — файл вне истории репозитория
  `.superpowers/sdd/2026-11-13-identity-1/native-handoff.md` (каталог в `.gitignore`); пересылает пользователь; не
  коммитится.
- **U2 (ответ на Q2, 13.11).** Тег `contracts-v0.2.0` — после мержа I-1; постановка тега и пуш — только по команде
  пользователя.

## Передача следующим планам

| Хвост | Куда |
| --- | --- |
| Удаление `pendingOps` и `TestPendingOpsExpire` (public, admin) | последняя задача последнего плана identity (R2) |
| Enum `FeatureFlagKey` + шесть ключей + подписи флагов | коммит миграции флагов, этап «Схема» (R4) |
| `flags.Rules` (`GlobalOnly`, `Requires`) и выдача `platform.flag_global_only`/`flag_requires` | этап «Платформа» |
| Ник в `StaffPlayerLink` через порт identity; `auth.Staff.LinkedUserID` | этап 9 (R15) |
| `captain_applications` → владелец `identity` в `archtest/ownership.go` | этап «Схема» (§2.3) |
| Исполнитель `identity.mfa_reset` в `approvals.Registry`, `TestAssembleRegistersIdentityActions` | этап 5 |
| Тексты причин отказа и неподходящести (`CaptainRejectionCode`, `CaptainIneligibility`) для игрока и админки | этап 9 (экраны) |
| Поля `reasons`/`methods`/`available_at`/`blocked_until` в `httpx.Problem` и хелперы `With…` | этап «Платформа» (m5) |
| Декодирование `X-WF-Lookup-Email`/`X-WF-Access-Reason`: битая кодировка → `400`, пределы после декодирования → `422` (тесты сценария) | этап 9 (R19) |
| Нормализация `DeviceInfo.app_version` вида `MAJOR.MINOR` в `MAJOR.MINOR.0` при сохранении устройства: `appversion.Compare` — строгий semver, иначе сравнение с min-version упадёт (ревью группы Б) | этап 4 |
| Сквозной тест `x-idempotency: no-store` в конвейере public (повтор `setupTotp` → `409 replay_unavailable`) | этап 5 (m15) |
| Решение о стражах `identity-freeze` и `pendingOpsAtFreeze` после реализации | последняя задача последнего плана identity (R17) |
| Хвосты документов §16: спека бэкенда (§2, §4.1, §4.4, §4.6, §6.2, §7.1, §7.6, §11, §13), `docs/01`, `backend/migrations/README.md`, `.claude/rules/{backend-identity,backend-staff,backend-http}.md`, `docs/deploy-dev.md`, `docs/versions.md`, `NOTICE` | этапы 2–10 по месту изменения (I-1 правит только `contracts.md`, `docs/05` и спеку) |

# Приложения

## Приложение 1. Операции контракта admin (I-1)

| operationId | Метод и путь | `x-permission` | Тело → ответ | Коды |
| --- | --- | --- | --- | --- |
| `listCaptainApplications` | `GET /v1/identity/captain-applications?status=&city_id=&cursor=&limit=` | `identity.captain_applications.read` | → `200 IdentityCaptainApplicationPage` (по умолчанию `pending`, старые сверху) | — |
| `getCaptainApplication` | `GET /v1/identity/captain-applications/{applicationId}` | `identity.captain_applications.read` | → `200 IdentityCaptainApplicationCard` | `identity.application_not_found` 404 |
| `getCaptainApplicationContact` | `GET /v1/identity/captain-applications/{applicationId}/contact`, заголовок `X-WF-Access-Reason` (R19) | `identity.pii.read` | → `200 IdentityApplicantContact` | `identity.application_not_found` 404 |
| `approveCaptainApplication` | `POST /v1/identity/captain-applications/{applicationId}/approve` | `identity.captain_applications.decide` | `IdentityCaptainApprove` → `200 IdentityCaptainApplication` (R13) | `application_not_found` 404, `application_closed` 409, `captain_ineligible` 409 (+`reasons`), `self_action` 409 |
| `rejectCaptainApplication` | `POST /v1/identity/captain-applications/{applicationId}/reject` | `identity.captain_applications.decide` | `IdentityCaptainReject` → `200 IdentityCaptainApplication` | `application_not_found` 404, `application_closed` 409, `self_action` 409 |
| `getAuthMethodsUsage` | `GET /v1/identity/auth-methods/usage` | `platform.flags.read` | → `200 IdentityAuthMethodsUsage` | — |
| `findPlayerByEmail` | `GET /v1/identity/users/lookup`, заголовки `X-WF-Lookup-Email`, `X-WF-Access-Reason` (R19) | `identity.pii.read` | → `200 IdentityPlayerLookup` | `identity.user_not_found` 404 |
| `requestMfaReset` | `POST /v1/identity/users/{userId}/mfa-reset` | `identity.mfa.reset` | `IdentityMfaResetRequest` → `202 ApprovalAccepted` | `user_not_found` 404, `self_action` 409, `mfa_not_enabled` 409, `mfa_reset_pending` 409 (R14) |
| `cancelMfaReset` | `POST /v1/identity/mfa-resets/{resetId}/cancel` | `identity.mfa.reset` | → `200 IdentityMfaResetCancelled` | `mfa_reset_not_found` 404, `self_action` 409 |
| `putStaffPlayerLink` | `PUT /v1/staff/accounts/{staffId}/player-link` | `staff.player_links.manage` | `StaffPlayerLinkPut` → `200 StaffPlayerLink` | `staff.not_found` 404, `staff.player_link_taken` 409, `staff.player_not_found` 422 |
| `deleteStaffPlayerLink` | `DELETE /v1/staff/accounts/{staffId}/player-link` | `staff.player_links.manage` | → `204` | `staff.not_found` 404 |

Коды без префикса в таблице — `identity.*`. Все — без `x-rate-limit` (класс `default` admin-api).

## Приложение 2. Реестр схем (имя → владелец → источник полей)

Поля, типы и пределы — по указанному разделу спеки; каждое поле — `description` и `example`; enum — открытый.

**Public, Task 2.** `AuthResult`, `AuthStatus` (enum §6.2.1), `TokenPair`, `AuthUser`, `MfaChallenge`, `MfaMethod`
(`totp | recovery_code | passkey`), `SocialSignupPending`, `DeletionPending` — §6.2.1; `CodeSent` — §6.2.2;
`DeviceInfo` (`platform` — `$ref AppPlatform`, существующая схема), `DeviceIntegrity`, `DeviceIntegrityKind`,
`DeviceChallenge` — §6.2.3, §6.3; `WebAuthnOptions`, `WebAuthnCredential`, `PasskeyAssertion` (`challenge_id`,
`credential`) — §6.2.4; `NicknameAvailability`, `NicknameUnavailableReason` — §6.3; `LegalDocumentKind`
(`terms | privacy`), `LegalDocument`, `LegalDocumentList`, `AcceptedDocument` — §6.2.4, §3.16; `Locale` (`ru | en`);
`AccountStatus` (значения `user_status` §5.2, включая `pending_deletion`); `Me`, `MeRestriction`, `MeDocument`,
`MeUpdate` — §6.2.4; `Session`, `ClientKind` (`mobile | web`), `AuthMethod` (`password | apple | google | passkey`),
`SessionList`, `RevokedCount` — §6.2.4, §5.3; запросы: `SignupStartRequest`, `SignupConfirmRequest`, `LoginRequest`,
`MfaCompleteRequest`, `PasswordResetStartRequest`, `PasswordResetConfirmRequest`, `RefreshRequest`,
`DeletionCancelRequest`, `NicknameChangeRequest`, `PasswordChangeRequest`, `EmailChangeStartRequest`,
`EmailChangeConfirmRequest`, `DocumentsAcceptRequest`, `DeletionRequest`; ответ `DeletionScheduled` (`purge_at`).

**Public, Task 3.** `AuthMethods`, `AuthMethodSwitch` (`signup`, `login`), `PasskeyMethodSwitch` (`register`,
`login`) — §4.4; `SocialProvider` (`apple | google`), `SocialNonceRequest`, `SocialNonce`, `SocialLoginRequest`,
`AppleAuthorization`, `SocialSignupCompleteRequest`, `SocialLinkRequest` — §6.3; `MfaPasskeyOptionsRequest`;
`ReauthRequest`, `ReauthSocial`, `ReauthResult` — §6.3, §3.13; `MyAuthMethods`, `LinkedIdentity`, `Passkey`,
`MfaStatus`, `TotpSetup`, `TotpConfirmRequest`, `RecoveryCodes` — §6.2.4; `PasskeyRegisterRequest`,
`PasskeyRenameRequest` — §6.3; `PasskeyLoginRequest` (`challenge_id`, `credential`, `device?` — тело
`passkeyLogin`), `LinkedIdentity` — также ответ `201` у `linkIdentity` (R20).

**Public, Task 4.** `Captaincy`, `CaptainPermit`, `CaptainEligibility`, `CaptainIneligibility` — §3.12 п.1–3;
`CaptainApplicationStatus`, `CaptainApplication`, `CaptainApplicationPage`, `CaptainApplicationSubmitRequest`
(пределы — §5.2: `note` ≤ 1000, прочие ≤ 200, `contact_link` `https://` ≤ 300, `phone` E.164) — §6.2.4;
`CaptainRejectionCode` — §7.3.

**Admin, Task 4.** `IdentityCaptainApplicationStatus`, `IdentityCaptainRejectionCode` (копии enum public: контракты
независимы), `IdentityCaptainApplication` (строка очереди и решённая заявка; с `age_years` и `account_age_days`
заявителя — §7.4, ревью группы Б), `IdentityCaptainApplicationPage`, `IdentityCaptainApplicationCard`
(заявка; заявитель: ник, город, возраст в годах, стаж в днях; условия сейчас — `[{check, state: ok | failed |
unavailable}]`; прошлые заявки; число связанных аккаунтов — §7.4), `IdentityApplicantContact` (`email`, `phone` или
`null`), `IdentityCaptainApprove` (`comment?`), `IdentityCaptainReject` (`rejection_code`, `comment?` ≤ 1000),
`IdentityAuthMethodsUsage` (`items: [{method: IdentityAuthMethodKind, only_method, linked}]`), `IdentityAuthMethodKind`,
`IdentityCaptainCheck`, `IdentityCheckState` (значения — R18), `IdentityPlayerLookup` (§7.1),
`IdentityMfaResetRequest` (`reason` 3–500, `ticket?` ≤ 100 — §5.3 `mfa_resets`), `IdentityMfaResetCancelled`;
staff: `StaffPlayerLinkPut`, `StaffPlayerLink` (R15). Параметры admin root: `ApplicationId`, `UserId`, `ResetId`
(uuid).

## Приложение 3. Коды по задачам

| Задача | Список | Коды |
| --- | --- | --- |
| 1 | `CrossModuleCodes` | `identity.restricted` |
| 1 | `httpx.PlatformCodes` | `http.not_implemented` (перенос, R1) |
| 2 | `PublicCodes` | `invalid_credentials`, `account_blocked`, `code_invalid`, `code_expired`, `resend_too_soon`, `too_young`, `city_not_found`, `password_weak`, `nickname_invalid`, `nickname_taken`, `nickname_change_too_soon`, `email_disposable`, `email_taken`, `email_same`, `device_signup_limit`, `device_untrusted`, `device_required`, `mfa_required`, `mfa_invalid`, `mfa_expired`, `passkey_invalid`, `refresh_invalid`, `reauth_required`, `session_not_found`, `confirm_mismatch`, `documents_outdated`, `restore_token_invalid` |
| 3 | `PublicCodes` | `mfa_already_enabled`, `mfa_not_enabled`, `mfa_setup_missing`, `social_token_invalid`, `social_email_unverified`, `social_email_missing`, `signup_token_invalid`, `identity_taken`, `identity_exists`, `identity_not_found`, `last_method`, `passkey_none`, `passkey_exists`, `passkey_limit`, `passkey_not_found`, `reauth_invalid`, `reauth_method_not_allowed`, `mfa_reset_not_found` |
| 4 | `PublicCodes` | `captain_ineligible`, `application_exists`, `application_not_found`, `application_closed`, `application_cooldown` |
| 4 | `AdminCodes` | `application_not_found`, `application_closed`, `captain_ineligible`, `self_action`, `user_not_found`, `mfa_not_enabled`, `mfa_reset_pending` (R14), `mfa_reset_not_found` |
| 4 | `staff.Codes` | `staff.player_link_taken`, `staff.player_not_found` (R15) |
| 4 | `httpx.AdminFlagCodes` | `platform.flag_global_only`, `platform.flag_requires` |

Префикс `identity.` у кодов задач 2–4 опущен. Итог `PublicCodes` — 50 кодов §6.5 без `user_not_found`, `self_action`
(только admin) и `restricted` (R9); код, который в Task 2 не отдаёт ни одна операция задачи, переносится в задачу
своей операции — исполнитель сверяет по таблице §6.3 (страж равенства не даст ошибиться).
