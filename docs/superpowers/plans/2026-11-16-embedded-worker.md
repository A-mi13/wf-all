# Воркер: встроенный режим api и свой сервер — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.

> **Формат — бриф задач, без кода реализации** (решение пользователя 13.10.2026, память `plan-format-brief`). Каждая
> задача: цель, риск, файлы, точные интерфейсы, тесты (имя → вход → ожидаемое), подсадка, приёмка, ловушки, «Не
> трогай». Как писать Go, YAML и shell — решает исполнитель по TDD, спеке и `.claude/rules/`.
> Авторитет: спека → «Решения плана» (R, явно отступающее от спеки, действует до правки спеки контроллером после
> итогового ревью) → тексты задач. Задача расходится со спекой без ссылки на R — права спека, задача — ошибка плана
> (сообщить контроллеру).

**Goal:** фон (River, relay outbox, периодика) работает на бесплатном стенде Render **сейчас** — встроенным режимом
`wf-api` по флагу `API_EMBED_WORKER`; переход на свой сервер (`docker compose` из того же образа) — правкой
конфигурации без правок кода. Попутно: своя очередь импорта GeoNames `queue.Import` и флаг
`WORKER_GEO_IMPORT_ENABLED`; побудка спящего api из admin-api (`ADMIN_WAKE_API_URL`).

Не здесь: аренда сервера и домена, провижининг, веб `apps/web`, наблюдаемость сверх логов и проб, перенос Neon на
свой Postgres (только описание варианта в README).

**Architecture:**
- **`backend/cmd/internal/workerhost`** — единственный код воркера: проверки до базы, ожидание схемы, клиент вставки,
  реестры (`subscriptions`, `mails`, исполнители, заглушки чужих видов), `riverConfig`, relay, остановка. Импортируют
  только `cmd/worker` и `cmd/api` (правило Go для `internal`); archtest модулей его не ограничивает, транзитивные
  стражи границ (`TestPublicBinariesDoNot*`) видят.
- **`cmd/worker`** = `workerhost.Load` + журнал + `workerhost.Prepare` + `db.Open(WORKER_DATABASE_URL)` +
  `workerhost.Run`; разовые команды (`events replay`, `geo import`) остаются в `cmd/worker`.
- **`cmd/api`** с `API_EMBED_WORKER=true` — то же до базы на переменных `WORKER_*`, сверка колец identity, второй пул
  (роль `worker`, прямое подключение), `server.RunService` с `Background` = `workerhost.Run`; свои сроки остановки
  (`config.APIEmbeddedStopTime`). С `false` — как сейчас (`server.RunHTTP`), `WORKER_*` не читаются.
- **admin-api** — middleware побудки снаружи `idempotency.Middleware`: 2xx мутирующей операции → фоновый
  `GET <ADMIN_WAKE_API_URL>/healthz` (в фоне, срок 60 с — R16, не чаще раза в 5 с).
- **Стенд** — `render.yaml` (`wf-api`: флаг, `WORKER_*`; `wf-admin-api`: `ADMIN_WAKE_API_URL`) и его стражи.
- **Свой сервер** — `deploy/server/` (`compose.yaml`, `Caddyfile`, `.env.example` по сервисам, README) и job CI в
  `image.yml`: `docker compose config`, подъём, `/readyz`, журнал «воркер запущен», `river_job completed`; второй
  прогон — api со встроенным воркером.

**Tech Stack:** Go 1.27 (pgx v5, River, chi), Docker Compose (локально — Docker Desktop, 16.11; WSL нет; в CI — дополнительно), Caddy
(последняя стабильная, запись в `docs/versions.md`), `postgis/postgis:18-3.6`.

**Spec:** `docs/superpowers/specs/2026-11-16-embedded-worker-design.md` (ред. 3, Approved; дальше «спека») — целиком.
Связанные: план I-2 `docs/superpowers/plans/2026-11-13-identity-2-core.md` Task 21 (заменяется этим планом),
спека бэкенда `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` §2, §9, §9.1.

**Предусловие старта:** ветка `spec/embedded-worker` (коммит спеки `627d19e`), I-2 слит в `main` (PR #16).

## Global Constraints

- **Правила репо** — `CLAUDE.md`, `.claude/rules/{backend,backend-platform,backend-http,backend-staff,env}.md`. TDD:
  сначала падающий тест; каждый новый страж проверяется подсадкой (`node scripts/plant.mjs …`, вызов Bash с
  `timeout: 600000`), в отчёт — что подсажено и что упало.
- **Риск** — A/B/C по `.claude/agents/wf-implementer.md`: для A исполнитель до первого теста пишет таблицу режимов
  отказа (остановка посреди фазы, повторы, частичный отказ, время, права роли в момент действия), ревьюер проходит 10
  категорий нестандартных условий.
- **Контракт не трогаем:** новых ручек нет; `contracts/**` и сгенерированное — вне плана. `./task gen` не нужен.
- **Env-ключ** (`.claude/rules/env.md`) — в ТОМ ЖЕ коммите: поле `platform/config` (default или `required`), строка с
  комментарием в `backend/.env.example`, рабочее значение в `backend/.env` — скриптом из scratchpad (`.sh` →
  `bash <скрипт>`: дописать ключ, если его нет; значения не выводить). Новые ключи плана и их значения в `backend/.env`:
  `WORKER_QUEUE_IMPORT=1`, `WORKER_GEO_IMPORT_ENABLED=true` (Task 1), `API_EMBED_WORKER=false` (Task 2),
  `ADMIN_WAKE_API_URL=` (пусто, Task 3). `render.yaml` — только Task 4.
- **Коммиты** — делает контроллер (субагенты не коммитят): пути и одна строка сообщения по-русски — в раздел «Коммит»
  отчёта; `git add <пути> && git commit -m "…" -- <пути>`. Никогда `git add -A/.`, `commit -a`, `stash`, `reset`,
  `clean`, `checkout -- .`. Доки нативщика `docs/WF_…_2026_10_19.md` и `docs/screens/` — не коммитить.
- **Код:** комментарии по-русски, идентификаторы по-английски, LF. Текущее время — `clock.Clock`; сроки и паузы —
  `time.Now()`/`time.Since` (монотонные).
- **Тесты:** исполнитель гоняет свои пакеты из `backend/`: `go test -p 4 ./cmd/... ./internal/platform/queue/...` и
  т. п. (Postgres — `bash scripts/pg.sh start` в фоне), typecheck своего пакета (`go vet ./cmd/...` своих путей);
  полный `./task backend:test` и `./task ci` — контроллер после итогового ревью. Пакет, где `*_test.go` импортирует
  `dbtest`, — все `Test*` первой строкой `t.Parallel()` (или `// serial: <причина>`), `TestMain` с `dbtest.Main`
  (`archtest.TestDBTestsAreParallel`).
- **Docker:** Docker Desktop работает локально (16.11), WSL — нет. Compose и Caddy проверяются локально (Task 5, «Done
  when»; встроенный прогон — ворота волны 3); job CI `server` — дополнительно на PR. Docker не входит в `./task ci`;
  быстрый страж без Docker — Go-тест файлов `deploy/server` в `backend:test` (Task 4, R8). Один `docker build` за
  раз, долгий — в фоне; проект compose — только свой (`-p wf-srv`), чужие контейнеры и тома не трогать.
- **Поведение отдельного воркера** не меняется, кроме двух решённых спекой изменений: отмена ctx (в том числе во время
  `waitSchema`) — выход `nil`; `geo.import` — в очереди `import`.

## Review Focus

1. **Перенос без смены поведения.** `cmd/worker` после Task 1 — те же проверки до базы (ключи в текстах ошибок, коды
   выхода), тот же реестр видов (`TestWorkerKinds`), те же очереди, обслуживание River и периодика
   (`TestWorkerRiverMaintenance`, `TestPeriodicSameInBothBinaries`), то же ожидание схемы, те же разовые команды;
   `slog.SetDefault` — только у `cmd/worker`, не в `workerhost`.
2. **Остановка.** Отмена ctx в любой фазе `Run` (ожидание схемы, сборка, работа) — `nil`, а не обёрнутая
   `context.Canceled`; River стартует на `context.WithoutCancel`, порядок relay → `queue.Stop`; встроенный режим —
   дренаж HTTP → relay → River мягко → жёстко ≤ `config.StopBudget`; второй пул закрывается после фона, пул api — после
   него же (`RunService`).
3. **Встроенный режим под ролью `worker`.** Фон api работает только на втором пуле (`WORKER_DATABASE_URL`); пул api в
   фон не попадает (поведенческий страж через relay outbox — R14); флаг `false` не читает `WORKER_*`; флаг `true` без
   них или с другим кольцом — отказ до базы.
4. **Флаг импорта.** `WORKER_GEO_IMPORT_ENABLED=false` — очереди `import` нет в карте River (а не `MaxWorkers: 0`),
   вид `geo.import` зарегистрирован, задача ждёт; остальные очереди ≥ 1 — отказ до базы.
5. **Побудка.** Ровно 2xx мутирующих операций, после `next`; не ломает хук идемпотентности (повтор по ключу — сохранённый
   ответ, сценарий один раз); запрос в фоне на detached ctx со сроком 60 с (R16); пустой URL — ни одного запроса; ошибки — в журнал.
6. **Стенд.** Секреты и адреса воркера — только `sync: false`; бюджет остановки и пулы Neon с учётом встроенного
   режима (+ `WORKER_DB_MAX_CONNS` + 1 LISTEN); `WORKER_*` допустимы только у `wf-api`; импорт на стенде выключен.
7. **Свой сервер.** У api в `compose.yaml` `API_EMBED_WORKER=false` явно (CI и Go-страж); `migrate` до сервисов;
   `stop_grace_period` ≥ 30 с; живость `/healthz`; доверие к заголовку IP согласовано с Caddy; плейсхолдеры без секретов.

---

## File Structure

| Путь | Ответственность | Задача |
| --- | --- | --- |
| `backend/cmd/internal/workerhost/{workerhost.go,registry.go,validate.go}` (раскладка внутри — исполнителя), `…/workerhost/{workerhost_test.go,registry_test.go,testmain_test.go}` | код воркера: `Load`, `Prepare`, `Run`, `Stop`, реестры, проверки | 1 |
| `backend/cmd/worker/{main.go,main_test.go,kinds_test.go,maintenance_test.go,schema_test.go,budget_test.go}` | тонкий бинарник + разовые команды; реестровые тесты переезжают в `workerhost` | 1 |
| `backend/internal/platform/queue/{queue.go,queue_test.go}` | константа `Import`, `Config` с восемью очередями | 1 |
| `backend/internal/geo/internal/app/{import_start.go,import_start_test.go}`, `backend/internal/geo/jobs/jobs_test.go` | `ImportArgs.InsertOpts().Queue = queue.Import` | 1 |
| `backend/internal/platform/config/{config.go,config_test.go}` | `Queues.Import`, `Worker.GeoImportEnabled` (1); `API.EmbedWorker`, сроки встроенного режима (2); `Admin.WakeAPIURL` (3) | 1, 2, 3 |
| `backend/.env.example`, `backend/.env` (скриптом) | ключи задач | 1, 2, 3 |
| `backend/.golangci.yml` | depguard `files` + `**/cmd/internal/workerhost/**` | 1 |
| `backend/internal/archtest/boundary_test.go` | `required` бинарников — `wf/backend/cmd/internal/workerhost` | 1 |
| `backend/cmd/api/{main.go,embed.go,main_test.go,embed_test.go,testmain_test.go,rekey_test.go}` | встроенный режим, стражи §6 | 2 |
| `backend/internal/httpapi/admin/{wake.go,handler.go,wake_test.go}`, `backend/cmd/admin-api/{main.go,main_test.go}` | побудка api | 3 |
| `render.yaml`, `backend/internal/platform/config/{render_stand_test.go,server_compose_test.go}`, `docs/deploy-dev.md`, `.claude/rules/{backend-platform,backend-http,env}.md`, `docs/superpowers/plans/2026-11-13-identity-2-core.md` (пометка Task 21), спека бэкенда (§2, §9, §9.1), `CLAUDE.md` (File structure), `.github/workflows/backend.yml` (paths) | стенд, стражи конфигураций, документы | 4 |
| `deploy/server/{compose.yaml,compose.ci.yaml,compose.ci-embed.yaml,Caddyfile,.env.example,api.env.example,admin-api.env.example,worker.env.example,migrate.env.example,README.md}`, `.github/workflows/image.yml` (job `server`, paths), `docs/versions.md` (Caddy) | свой сервер и его проверка в CI | 5 |
| вне репозитория — панель Render, Neon `wf-dev` | выкатка на стенд | 6 (контроллер) |

# Задачи

Обозначения: **Риск** — A/B/C; **Тесты** — `Имя` → вход → ожидаемое; **Подсадка** — какой баг подсадить, какой тест
обязан упасть; **Приёмка** — что проверяет ревьюер.

### Task 1: `workerhost` — перенос воркера, очередь импорта, флаг импорта

**Цель.** Код исполняющего воркера переезжает из `cmd/worker/main.go` в `backend/cmd/internal/workerhost` без смены
поведения (кроме двух решённых: отмена ctx → `nil`; импорт — своя очередь). `cmd/worker` становится тонким. Заодно —
очередь `queue.Import` и флаг `WORKER_GEO_IMPORT_ENABLED` (спека §2, §8.2).

**Риск:** B (перенос с теми же стражами; остановка; новая очередь меняет поведение отдельного воркера).

**Файлы.**
- Создать: `backend/cmd/internal/workerhost/*.go` (раскладка — исполнителя), тесты в том же каталоге (пакет
  `workerhost`, белый ящик), `testmain_test.go` (`dbtest.Main`), если тесты импортируют `dbtest`.
- Изменить: `backend/cmd/worker/main.go` (остаются `main`, `host`, `run`, `runOn`, `usage`, `errUsage`, `usageError`,
  `exitCode`, `command`, `replay`, `geoImport`, `printGeoSummary`), `backend/cmd/worker/{kinds_test.go,
  maintenance_test.go}` (реестровые тесты — переезжают в `workerhost`; процессные `TestRescuerKeepsAdminJobs`,
  `waitRescued` — остаются), `backend/cmd/worker/main_test.go` (ожидания не меняются; новый тест остановки ниже),
  `backend/internal/platform/queue/{queue.go,queue_test.go}`, `backend/internal/platform/config/{config.go,
  config_test.go}` (только `Queues`, `Worker`), `backend/cmd/admin-api/maintenance_test.go` (только комментарий), `backend/internal/geo/internal/app/{import_start.go,
  import_start_test.go}`, `backend/internal/geo/jobs/jobs_test.go`, `backend/.golangci.yml`,
  `backend/internal/archtest/boundary_test.go`, `backend/.env.example` (раздел `WORKER_`), `backend/.env` (скриптом),
  `.claude/rules/backend-platform.md` (абзац «Подписчик…» — путь `subscriptions()`; абзац «Задача River объявляет
  очередь» — список с `import`; абзац о воркере — `workerhost`), спека бэкенда §9.1 (строка `import`).

**Интерфейсы.**
- `config.Queues` + `Import int \`env:"QUEUE_IMPORT" envDefault:"1"\``; `config.Worker` + `GeoImportEnabled bool
  \`env:"GEO_IMPORT_ENABLED" envDefault:"true"\`` (R3).
- `queue.Import = "import"` (комментарий: импорт GeoNames, до 30 мин — отдельно от `maintenance`); `queue.Config(c
  config.Queues)` — восемь очередей всегда (включая `Import: {MaxWorkers: c.Import}`).
- `geo/internal/app.ImportArgs.InsertOpts().Queue = queue.Import`; комментарий «занимает слот maintenance» — заменить
  на «своя очередь import: не держит слот maintenance; на стенде очередь выключена флагом — задача ждёт».
- Пакет `workerhost`:
  - `type Base struct { Config config.Worker; Geo geojobs.Config }` — конфиг `WORKER_` и источник GeoNames, проверенные
    до базы; общий для сервиса и разовых команд.
  - `func Load(environ []string, geoHTTP *http.Client) (Base, error)` — `config.Load[config.Worker]("WORKER_", …)`,
    `validateRelay`, `validateGeoNames`, `geoNamesClient(geoHTTP, …)` (как сейчас в `runOn`, порядок проверок тот же).
  - `type Prepared struct{ … }` (неэкспортируемые поля: `Base`, `*mail.SMTP`, `*keys.Ring`, `*i18n.Catalog`);
    `func Prepare(b Base, environ []string, log *slog.Logger) (Prepared, error)` — `validateQueues`
    (R4), `config.Mail` и `config.WorkerIdentity` с префиксом `WORKER_`, `mail.NewSMTP` (журнал — `log`), `keys.NewRing`
    (ошибка с префиксом `WORKER_IDENTITY_SECRET_KEYS:`), `i18n.Load(locales.FS)`.
  - `func (p Prepared) DB() config.DB`; `func (p Prepared) RingIDs() []string` — `(*keys.Ring).IDs()` кольца воркера
    (имена ключей по порядку, не сами секреты) — для сверки колец в Task 2 (R6).
  - `type Stop struct { Relay, Soft time.Duration }` (жёсткая — всегда `config.HardStop` внутри `queue.Stop`);
    `var ServiceStop = Stop{Relay: config.WorkerRelayStop, Soft: config.WorkerRiverSoftStop}`.
  - `func Run(ctx context.Context, p Prepared, pool *pgxpool.Pool, log *slog.Logger, stop Stop) error` — тело
    нынешнего `runOn` после `db.Open`: `waitSchema(ctx, pool, p.DB().SchemaWait, log)` → клиент вставки →
    `identity.WorkerDeps` → `allWorkers` → `river.NewClient(…, riverConfig(queues(p.Base.Config), …))` →
    `queue.WarnPool(log, queue.Slots(queues(…)), p.DB().MaxConns)` → `client.Start(context.WithoutCancel(ctx))` →
    relay (`StopTimeout: stop.Relay`) → `log.Info("воркер запущен", "queues", …)` → `<-ctx.Done()` → `wg.Wait()` →
    `queue.Stop(context.Background(), client, stop.Soft)`. Отмена ctx до `Start` в любой фазе — `nil` (R11).
  - `func WaitSchema(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration, log *slog.Logger) error` —
    экспортирован для разовой команды `cmd/worker` (`timeout 0`).
  - `func Subscriptions() (*events.Registry, error)` — экспортирован: им пользуются `Run` и `cmd/worker command`
    (реестр для `events replay`); комментарий «каждая спека модуля добавляет сюда свои Subscription».
  - Неэкспортируемые (переезжают как есть): `mails`, `realWorkers`, `allWorkers`, `riverConfig`, `validateRelay`,
    `validateGeoNames`, `geoNamesClient`, `geoNamesTimeout`, `schemaPoll`; новые `validateQueues(c config.Worker) error`,
    `queues(c config.Worker) map[string]river.QueueConfig` (`queue.Config(c.Queues)` без `queue.Import`, если
    `!c.GeoImportEnabled`).
- `cmd/worker`: `runOn` = `workerhost.Load(environ, h.httpClient)` → журнал `logx.New(logOut, b.Config.Log).With("service",
  "worker")` → разовая команда: `slog.SetDefault`, `db.Open`, `command(…)` (как сейчас; `reg` из
  `workerhost.Subscriptions`, `geo` — `b.Geo`) → иначе `workerhost.Prepare(b, environ, log)` → `slog.SetDefault(log)` →
  `db.Open(ctx, p.DB())` → `workerhost.Run(ctx, p, pool, log, workerhost.ServiceStop)`.

**Тесты.**
- Переезжают в `workerhost` без смены ожиданий (имена те же): `TestWorkerKinds`, `TestWorkerKindsQueues` (таблица
  очередей по аргументам; `served` — `queues(config.Worker{Queues: …умолчания…, GeoImportEnabled: true})` — R3;
  `geo.import` → `queue.Import`), `TestWorkerRiverMaintenance`, `TestPeriodicSameInBothBinaries`,
  `TestWorkerMailsIdentity`, хелперы `names`, `queuesOf`, `periodicIDs`, `lazyIdentityDeps`. Зеркало
  `TestPeriodicSameInBothBinaries` в `cmd/admin-api/maintenance_test.go` — правка комментария (ссылка на
  `workerhost` вместо `cmd/worker`); `cmd/admin-api/maintenance_test.go` — в списке файлов задачи.
- Остаются в `cmd/worker` без смены ожиданий: все тесты `main_test.go`, `schema_test.go`, `budget_test.go`,
  `TestRescuerKeepsAdminJobs`.
- Новые:
  - `TestConfigHasSpecQueues` (queue, правка существующего: «семь» → «восемь») → `config.Queues{…, Import: 8}` →
    восемь очередей, `import` = 8.
  - `TestImportArgsQueue` (или правка `jobs_test.go:35`, `:78`, `import_start_test.go:120`, `:492`) → очередь задачи
    `geo.import` — `queue.Import`; клиент теста `import_start_test.go:492` — очередь `queue.Import`.
  - `TestQueuesImportFlag` (workerhost) → `queues` с `GeoImportEnabled=false` → нет ключа `import`, остальные семь есть;
    с `true` → `import` с `MaxWorkers` из конфига.
  - `TestPrepareRejectsZeroQueues` (workerhost) → `WORKER_QUEUE_MAIL=0`, `WORKER_QUEUE_IMPORT=0` (при флаге `true`),
    база недостижима → ошибка называет ключ; `WORKER_QUEUE_IMPORT=0` при `WORKER_GEO_IMPORT_ENABLED=false` — без
    ошибки очереди (импорт выключен).
  - `TestRunStopsDuringSchemaWait` (cmd/worker) → база без последней миграции (`behindURL`),
    `WORKER_DB_SCHEMA_WAIT=10m`, журнал в синхронизированный буфер; дождаться строки «схема базы отстаёт», `cancel()` →
    `run` = `nil` за ≤ 5 с, `river_job` пуста.
  - `TestRunCancelledBeforeStart` (workerhost) → `Run` с уже отменённым ctx на живой базе → `nil`, задач нет.
  - `TestGeoImportDisabledJobWaits` (workerhost, dbtest) → флаг `false`, `WORKER_GEONAMES_BASE_URL` — недостижимый
    `https://127.0.0.1:1` (взятый по ошибке импорт упал бы, а не пошёл в сеть), `Run` на клоне; вставлена задача `geo.import`
    (через `geojobs.NewImportArgs`, клиент вставки); после старта и появления `completed` у периодики (`ratelimit.cleanup`)
    — задача импорта всё ещё `available`; `Run` остановлен — `nil`.
- `TestPublicBinariesDoNotDependOnAdmin` / `…OnStaff` (archtest) → `required` у `./cmd/worker` и `./cmd/api` +
  `wf/backend/cmd/internal/workerhost` (для `./cmd/api` — после Task 2; в Task 1 только `./cmd/worker`).

**Подсадка.**
- `queues` не удаляет `import` при `false` → `TestQueuesImportFlag` и `TestGeoImportDisabledJobWaits` красные.
- `ImportArgs` обратно в `queue.Maintenance` → `TestImportArgsQueue` красный (`TestWorkerKindsQueues` его не поймает:
  `maintenance` обслуживается воркером).
- `Run` возвращает ошибку `waitSchema` без проверки `ctx.Err()` → `TestRunStopsDuringSchemaWait` красный.
- `validateQueues` без проверки `Mail` → `TestPrepareRejectsZeroQueues` красный.

**Приёмка.** `cmd/worker/main.go` без River, relay и реестров; `go test ./cmd/worker/... ./cmd/internal/... ./internal/
platform/queue/... ./internal/geo/... ./internal/archtest/...` зелёные; depguard `files` включает
`'**/cmd/internal/workerhost/**'`; документы — новый путь `subscriptions()` и очередь `import`.

**Ловушки и стыки.**
- River отвергает `MaxWorkers < 1` при `NewClient` — после базы; поэтому `validateQueues` до базы и выключение импорта —
  отсутствием очереди в карте, не нулём.
- `migrate.WaitSchema` на отменённом ctx может вернуть не `ctx.Err()`, а обёрнутую ошибку запроса («версия схемы:
  context canceled») — проверять `ctx.Err() != nil`, а не `errors.Is(err, context.Canceled)`.
- `slog.SetDefault` в `workerhost` не вызывать: во встроенном режиме журнал — api (Task 2).
- `TestRunWarnsOpportunisticSMTPInServiceLog` требует, чтобы `Prepare` писал предупреждение SMTP в журнал воркера в
  формате `WORKER_LOG_FORMAT` — журнал строится до `Prepare` из `Base.Config.Log` (R1).
- Разовые команды не читают почту, кольцо и очереди (как сейчас: `TestReplayDoesNotRequireMail`).
- Уникальность `geo.import` — по аргументам без `ByQueue`: ждущая задача в `maintenance` (старый релиз) и новая в
  `import` — дубль; переходный хвост на стенде — Task 6, шаг 1.
- `cmd/admin-api` своих очередей воркера не берёт: `queue.Config` вызывает только `workerhost` — проверить grep'ом.
- Скрипт `backend/.env`: дописать `WORKER_QUEUE_IMPORT=1`, `WORKER_GEO_IMPORT_ENABLED=true`, если ключей нет.

**Не трогай.** `cmd/api`, `cmd/admin-api/main.go`, `internal/httpapi/**`, `render.yaml`, `deploy/**`,
`.github/workflows/**`, поля `config.API`/`config.Admin`, `server/**`.

### Task 2: встроенный режим `cmd/api`

**Цель.** `API_EMBED_WORKER=true` запускает в процессе api тот же воркер (`workerhost`) на втором пуле роли `worker`
через `server.RunService`; `false` — как сейчас (спека §3, §6).

**Риск:** A (процесс публичного API получает права `worker`; остановка в окне Render; отказ фона гасит HTTP).

**Файлы.** Создать: `backend/cmd/api/embed.go` (сборка встроенного режима), `backend/cmd/api/embed_test.go`,
`backend/cmd/api/testmain_test.go` (`dbtest.Main`). Изменить: `backend/cmd/api/main.go` (`run`),
`backend/cmd/api/{main_test.go,rekey_test.go}` (`t.Parallel()` первой строкой каждого `Test*` — пакет начинает
импортировать `dbtest`), `backend/internal/platform/config/{config.go,config_test.go}` (только `API` и константы
остановки), `backend/internal/archtest/boundary_test.go` (`required` у `./cmd/api`), `backend/.env.example` (раздел
`API_`), `backend/.env` (скриптом).

**Интерфейсы.**
- `config.API` + `EmbedWorker bool \`env:"EMBED_WORKER" envDefault:"false"\`` (комментарий: только dev-стенд; прод —
  отдельный процесс `worker`).
- `config.APIEmbeddedRelayStop = 2 * time.Second`, `config.APIEmbeddedRiverSoftStop = 8 * time.Second` (R5);
  `func APIEmbeddedStopTime(drain time.Duration) time.Duration` = drain + relay + soft + `HardStop`.
- `cmd/api`:
  - `func embeddedStop() workerhost.Stop` → `{Relay: config.APIEmbeddedRelayStop, Soft: config.APIEmbeddedRiverSoftStop}`.
  - `func prepareEmbedded(cfg config.API, secrets *keys.Ring, environ []string, startLog *slog.Logger) (workerhost.Prepared,
    error)` —
    `workerhost.Load(environ, nil)` → `workerhost.Prepare(b, environ, startLog.With("component", "worker"))` →
    `slices.Equal(secrets.IDs(), p.RingIDs())` — `secrets` — уже собранное кольцо `API_IDENTITY_SECRET_KEYS`
    (`*keys.Ring` из `run`), сравниваются имена ключей по порядку, не сырые секреты; иначе ошибка, называющая оба
    ключа (`WORKER_IDENTITY_SECRET_KEYS` и `API_IDENTITY_SECRET_KEYS`) без значений (R6). Сигнатура — с
    `secrets *keys.Ring` аргументом.
  - `run`: после всех нынешних проверок до базы — `if cfg.EmbedWorker` → `startLog := logx.New(logOut,
    cfg.Log).With("service", "api")`, `prepareEmbedded`, предупреждение в `startLog`, если
    `config.APIEmbeddedStopTime(cfg.HTTP.ShutdownTimeout) > config.StopBudget` (как admin-api); `wpool, err :=
    db.Open(ctx, p.DB())`, `defer wpool.Close()` (вне `RunService`: закрывается после возврата `RunService`, т. е. после
    остановки фона); `server.RunService(ctx, "api", …, build)`, где `build` собирает тот же хендлер, что сейчас, и
    `Background: func(ctx) error { return workerhost.Run(ctx, p, wpool, log.With("component", "worker"), embeddedStop()) }`.
    Без флага — `server.RunHTTP` как сейчас; общую сборку хендлера вынести в функцию, чтобы обе ветки звали одну.
  - `closeGeo` по-прежнему после возврата `RunService` (HTTP дренирован, фон остановлен).

**Тесты** (`embed_test.go`, на `dbtest`; окружение — по образцу `cmd/admin-api/helpers_test.go` `newTestEnv`: одна
база, `dbtest.NewPoolsAs(t, "api")` применяет права; `API_DATABASE_URL` — строка владельца с `options=-c role=api`,
`WORKER_DATABASE_URL` — с `-c role=worker`; `API_HTTP_ADDR` — свободный порт; SMTP — `127.0.0.1:1`, TLS `none`;
кольца — один ключ у обоих).
- `TestEmbeddedRunsPeriodic` → флаг `true` → в течение 90 с у `ratelimit.cleanup` есть строка `state = 'completed'`;
  `cancel()` → `run` = `nil`.
- `TestEmbeddedRelayUnderWorkerRole` → событие в `outbox` (как `TestRunPublishesPendingEventsAndStops`) → за 10 с
  `published_at` заполнен (у роли `api` на `outbox` только `INSERT` — R14).
- `TestEmbeddedRescuerKeepsAdminJobs` → строка `staff.mail` в `running` 2 часа назад (как в `cmd/worker`) → `retryable`
  или `available` за 90 с, не `discarded`.
- `TestEmbedFlagOffIgnoresWorkerEnv` → флаг не задан, окружение api + мусор `WORKER_RELAY_BATCH=0`, без
  `WORKER_DATABASE_URL`, база api недостижима → ошибка о подключении к базе, в тексте нет `WORKER_`.
- `TestEmbedRequiresWorkerEnv` → флаг `true`, по очереди нет `WORKER_DATABASE_URL`, `WORKER_MAIL_SMTP_ADDR`,
  `WORKER_IDENTITY_SECRET_KEYS` (база недостижима) → ошибка называет отсутствующий ключ.
- `TestEmbedRejectsDifferentRing` → `WORKER_IDENTITY_SECRET_KEYS` ≠ `API_IDENTITY_SECRET_KEYS` → ошибка с обоими
  именами до базы, без значений ключей; тот же ключ в base64 и base64url — старт проходит проверку (ошибка уже о
  базе); ключ теста — байты, дающие в base64 и `+`, и `/` (например, 32 байта `0xFB 0xFF …`: иначе обе записи
  совпадают и случай вакуумен — проверить в тесте, что строки различны); кольца из тех же ключей в другом порядке —
  отказ.
- `TestEmbeddedShutdownBudget` → `config.APIEmbeddedStopTime(дренаж по умолчанию config.API)` ≤ `config.StopBudget`
  и = дренаж + `embeddedStop().Relay` + `embeddedStop().Soft` + `config.HardStop`.
- `TestEmbeddedBackgroundFailureStopsHTTP` → схема без последней миграции, `WORKER_DB_SCHEMA_WAIT=1s`, ctx теста **не
  отменяется** → `run` сам возвращается за ≤ 1 с + `API_SHUTDOWN_TIMEOUT` теста + 10 с запаса с ошибкой
  `migrate.ErrSchemaBehind` (`errors.Is`) и префиксом «фон»; возврат без `cancel()` — и есть доказательство, что
  отказ фона погасил HTTP; после возврата `/healthz` не отвечает.
- `TestEmbeddedStopDuringSchemaWait` → схема отстаёт, `WORKER_DB_SCHEMA_WAIT=10m`; дождаться `/healthz` 200 и строки
  «схема базы отстаёт» в журнале; `cancel()` → `run` = `nil` за ≤ `APIEmbeddedStopTime` дренажа теста.
- `TestEmbeddedLogComponent` → журнал (`API_LOG_FORMAT=json`) содержит «воркер запущен» с `"component":"worker"` и
  `"service":"api"`.
- `TestRekeyIgnoresEmbedFlag` (`rekey_test.go`) → `identity rekey --dry-run` с `API_EMBED_WORKER=true` без `WORKER_*`
  → поведение прежнее (ошибка о базе/успех, но не о `WORKER_`).

**Подсадка.**
- `envDefault:"true"` у `EmbedWorker` → `TestEmbedFlagOffIgnoresWorkerEnv` красный.
- `Background` не задан (ветка флага зовёт `RunHTTP`) → `TestEmbeddedRunsPeriodic` красный.
- пул api вместо `wpool` в `workerhost.Run` → `TestEmbeddedRelayUnderWorkerRole` красный.
- `embeddedStop()` = `workerhost.ServiceStop` → `TestEmbeddedShutdownBudget` красный.
- сверка колец всегда успешна (проверка убрана) → `TestEmbedRejectsDifferentRing` красный.

**Приёмка.** Флаг `false` — путь кода прежний (`RunHTTP`), `WORKER_*` не читаются; archtest и depguard зелёные (`cmd/api`
не тянет `staff` и `admin`); второй пул закрывается после фона; журнал фона — api с `component=worker`, без второго
`slog.SetDefault`.

**Ловушки и стыки.**
- `RunService` сам открывает пул api и зовёт `slog.SetDefault`; второй пул — в `run` до `RunService` (`defer` снаружи):
  иначе пул закроется раньше остановки фона.
- Relay держит `LISTEN` на отдельном соединении вне пула (+1 к `WORKER_DB_MAX_CONNS`); River — свой `LISTEN` в пуле.
  `WORKER_DATABASE_URL` на Neon — прямой хост (без `-pooler`): через PgBouncer `LISTEN` не работает.
- Фон вернулся до отмены — `RunService` гасит HTTP (`errBackgroundStopped`); `workerhost.Run` возвращает `nil` только
  при отменённом ctx, иначе — ошибку.
- Тесты пакета — параллельные (`archtest.TestDBTestsAreParallel`): существующие `Test*` в `main_test.go`,
  `rekey_test.go` получают `t.Parallel()`; `t.Setenv` не использовать.
- `api identity rekey` идёт через `dispatch` до `run` — флаг не читает, проверить тестом.
- Встроенный клиент River api исполняет `mail.send` и периодику; клиент вставки identity в `buildIdentity` — на пуле api
  (как сейчас), это нормально: вставка — `INSERT` в `river_job` под `api`.

**Не трогай.** `cmd/internal/workerhost` (только вызовы; нужен экспорт — вопрос контроллеру), `cmd/worker`,
`cmd/admin-api`, `internal/httpapi/**`, `server/**`, `render.yaml`, `deploy/**`.

### Task 3: побудка api из admin-api

**Цель.** Письма и события, которые ставит admin-api, уходят сразу и на спящем стенде: после успешной мутации admin-api
будит `wf-api` запросом `GET <ADMIN_WAKE_API_URL>/healthz` (спека §8.1).

**Риск:** B (место в конвейере рядом с идемпотентностью; фоновые запросы; не ломает ответы).

**Файлы.** Создать: `backend/internal/httpapi/admin/wake.go`, `backend/internal/httpapi/admin/wake_test.go`.
Изменить: `backend/internal/httpapi/admin/handler.go` (поле `Options.Wake`, middleware в конвейере),
`backend/cmd/admin-api/main.go` (проверка до базы, сборка `Waker`), `backend/cmd/admin-api/main_test.go` (отказ старта),
`backend/internal/platform/config/{config.go,config_test.go}` (только `Admin`), `backend/.env.example` (раздел
`ADMIN_`), `backend/.env` (скриптом, пустое значение), `.claude/rules/backend-http.md` (конвейер admin-api: побудка
перед `idempotency.Middleware`).

**Интерфейсы.**
- `config.Admin` + `WakeAPIURL string \`env:"WAKE_API_URL"\`` (комментарий: только dev-стенд; пусто — выключено).
- `admin.ValidateWakeURL(s string) error` — пусто → `nil`; иначе `https`, непустой хост, без пути (кроме пустого или
  `/`), запроса, фрагмента и userinfo; текст ошибки называет `ADMIN_WAKE_API_URL` (по образцу `ValidateOrigin`).
- `type Waker struct{ … }` (поля `gap`, `timeout` — для тестов, подменяются через `export_test.go`, не переменными
  пакета); `func NewWaker(base string, client *http.Client, log *slog.Logger) *Waker` — `base` пусто → `nil`;
  завершающий `/` обрезается (`https://h/` → запрос `https://h/healthz`, не `//healthz`); `client` nil → свой с
  `Timeout` = `wakeTimeout`. `func (w *Waker) Poke()` — безопасен на nil; не блокирует;
  не чаще `wakeMinGap` (монотонные часы, атомарная отметка) и не больше одного запроса в полёте; запрос `GET
  base+"/healthz"` на `context.WithTimeout(context.Background(), wakeTimeout)`; ошибка или не-2xx — `log.Warn`, не
  наружу. Константы: `wakeTimeout = 60 * time.Second` (R16: спящий инстанс Render free просыпается до минуты, а
  запрос, оборванный за 2 с, может не разбудить его; запрос в фоне ответ не держит), `wakeMinGap = 5 * time.Second`;
  пока запрос в полёте, новые не начинаются (троттлинг сохраняется).
- `func wake(w *Waker) func(http.Handler) http.Handler` — nil → пропуск как есть; иначе обёртка
  `middleware.NewWrapResponseWriter` (chi), `next.ServeHTTP`, затем `Poke()`, если метод не `GET/HEAD/OPTIONS`,
  маршрут контракта найден (`httpx.RouteFrom`) и статус 200–299.
- `admin.Options.Wake *Waker` (nil — выключено). В `NewHandler` — `wake(o.Wake)` в списке конвейера сразу перед
  `idempotency.Middleware(…)`.
- `cmd/admin-api.run`: `admin.ValidateWakeURL(cfg.WakeAPIURL)` рядом с `ValidateOrigin` (до базы, до разбора команды
  оператора не нужно — после `config.Load`); `Wake: admin.NewWaker(cfg.WakeAPIURL, nil, log)` в `admin.Options` сервера;
  команды оператора побудку не получают (спека §8.1, «вне покрытия»).

**Тесты.** Подставной сервер `httptest.NewServer` с атомарным счётчиком запросов к `/healthz`; журнал `Waker` в
тестах — `slog.DiscardHandler`; перед выходом из теста — дождаться завершения запроса в полёте (счётчик стабилен,
сервер закрыт после), иначе горутина `Poke` переживёт тест.

*Юнит на звено `wake(w)`* (`wake_test.go`, внутренний тест или через `export_test.go`): подставной хендлер с заданным
статусом и маршрутом (`httpx` ctx маршрута), `httptest.NewRecorder`, `Waker` с `gap = 0`. Подставной хендлер
фиксирует значение **синхронного** атомарного счётчика вызовов `Poke` (растёт до запуска горутины, читается через
`export_test.go`) в момент своего вызова — для проверки «после next». Ожидания запросов: «не больше одного в полёте»
действует и при `gap = 0` — после каждого вызова, который должен разбудить, дождаться роста счётчика запросов
(опрос ≤ 3 с) и только потом слать следующий.
- `TestWakeOnSuccessfulMutation` → `POST`/`PUT`/`PATCH`/`DELETE`, маршрут контракта, 200 и 204 → по одному запросу на
  каждый (с ожиданием роста после каждого); синхронный счётчик `Poke` в момент вызова хендлера = прежнему (побудка строго после `next`).
- `TestWakeNotOnFailure` → мутация с 400, 404, 409, 422, 500 → 0 запросов (пауза ≥ 1 с).
- `TestWakeNotOnRead` → `GET`, `HEAD`, `OPTIONS` с 200 → 0 запросов.
- `TestWakeNotOutsideContract` → `POST` 200 без маршрута контракта (статика, `/docs`) → 0 запросов.
- `TestWakeNilWaker` → `wake(nil)` возвращает хендлер, ответ тот же; `NewWaker("", …) == nil`; `Poke` на nil не
  паникует.
- `TestWakeThrottled` → `gap` = 200 мс: 10 вызовов `Poke` подряд → 1 запрос; после паузы > `gap` — ещё 1; при
  запросе в полёте (сервер держит ответ) новые `Poke` не начинают второй.
- `TestWakeTrimsSlash` → `NewWaker(srv.URL+"/", …)` → путь запроса ровно `/healthz`.
- `TestWakeErrorsLogged` → сервер отвечает 500 / закрыт → `Poke` не паникует, предупреждение в журнале (буфер), ответ
  звена не меняется.
- `TestValidateWakeURL` → `""` ok; `https://wf-api.onrender.com` ok; `https://wf-api.onrender.com/` ok;
  `http://…`, `https://`, `https://h/path`, `https://h?x=1`, `https://u:p@h`, `ftp://h` → ошибка с `ADMIN_WAKE_API_URL`.

*Полный конвейер* (`newStaffEnv(t, func(o *admin.Options){ o.Wake = … })`, `Waker` с `gap = 0` через `export_test.go`) —
только место звена и идемпотентность. Подготовка окружения (`firstLogin` и т. п.) сама шлёт 2xx-мутации: после неё
дождаться стабильного счётчика (не меняется ≥ 1 с) и мерить **разность**.
- `TestWakeInPipeline` → `PUT /v1/staff/accounts/{id}/role` 200 (как `TestStaffIdempotencyOwnTable`) → разность = 1;
  `GET /v1/staff/me` → разность 0.
- `TestWakeKeepsIdempotency` → два `PUT` с одним `Idempotency-Key` → второй — сохранённый ответ (`replayed`), ответы
  равны, сценарий исполнен один раз (одна строка аудита `staff.role_changed` или аналог); между первым и вторым `PUT` —
  дождаться роста счётчика запросов (иначе второй гасится как «уже в полёте»); разность = 2 (повтор будит —
  безвредно).
- `TestRunRejectsBadWakeURL` (`cmd/admin-api`) → `ADMIN_WAKE_API_URL=http://x` → отказ до базы с именем ключа.

**Подсадка** (все — KILLED, `scripts/plant.mjs`).
- побудка на любой статус (условие 2xx убрано) → `TestWakeNotOnFailure` красный.
- побудка на `GET` (проверка метода убрана) → `TestWakeNotOnRead` красный.
- `Poke()` до `next.ServeHTTP` → `TestWakeNotOnFailure` красный (статуса до `next` нет) и
  `TestWakeOnSuccessfulMutation` красный (синхронный счётчик `Poke` в момент вызова хендлера вырос).
- проверка маршрута убрана → `TestWakeNotOutsideContract` красный.
- `ValidateWakeURL` пропускает `http` → `TestValidateWakeURL` красный.

**Приёмка.** Побудка — только снаружи идемпотентности, без `db.WithTxHook`; запросы в фоне со сроком; повтор по ключу
и лишняя побудка безвредны; пустой URL — конвейер без изменений.

**Ловушки и стыки.**
- Не через `db.WithTxHook`: хук на ctx один, второй перекрыл бы хук идемпотентности.
- Повтор по `Idempotency-Key` отдаёт сохранённый 2xx — побудка сработает ещё раз: безвредно (спека §8.1).
- Горутина побудки может пережить дренаж HTTP (до `wakeTimeout`): процесс её не ждёт — выход по возврату `run`
  обрывает её, это безвредно (побудка — подсказка, не обязательство); ждать её в остановке не нужно.
- Тесты пакета `httpapi/admin` — с базой: `t.Parallel()`; счётчик запросов — атомарный; ожидание «0 запросов» —
  фиксированная пауза ≥ 1 с, не ноль; в полном конвейере — только разность после стабилизации.
- Вне покрытия (спека §8.1): тикер `staff/jobs.Cleaner`, команды оператора; и отказы, коммитящие запись, — `409` approve
  с закоммиченным `failed`/`expired` (подтверждение вдвоём): ответ не 2xx, побудки нет; подписчиков сейчас нет —
  задержка ни на что не влияет. Записать строкой в `docs/deploy-dev.md` (Task 4).
- Адрес в журнал при ошибке — только хост (не ПД, но без пути и запросов).

**Не трогай.** `cmd/api`, `cmd/worker`, `cmd/internal/**`, поля `config.API`/`config.Worker`, `idempotency/**`,
`render.yaml`, `deploy/**`.

### Task 4: стенд Render — `render.yaml`, стражи, документы

**Цель.** `wf-api` стенда — со встроенным воркером, `wf-admin-api` будит его; стражи `render_stand_test.go` знают новый
режим; Go-страж файлов `deploy/server` (R8); документы по спеке §7.

**Риск:** B (секреты в панели, пулы Neon, бюджет остановки; ошибка — деплой стенда не стартует).

**Файлы.** Изменить: `render.yaml`, `backend/internal/platform/config/render_stand_test.go`, `docs/deploy-dev.md`,
`.claude/rules/{backend-platform,env}.md`, `docs/superpowers/plans/2026-11-13-identity-2-core.md` (Task 21 — пометка),
`docs/superpowers/specs/2026-10-01-backend-architecture-design.md` (§2 — строка «Воркер», §9 — режимы запуска),
`CLAUDE.md` (Task 4 — его владелец: File structure — `deploy/server/`, `cmd/internal/workerhost`; What NOT to do — строка о
Docker; Проверки — локальная проверка `deploy/server`), `.github/workflows/backend.yml` (paths +
`deploy/server/**`). Создать: `backend/internal/platform/config/server_compose_test.go`.

**`render.yaml`, `wf-api`, в файле:** `API_EMBED_WORKER: 'true'`, `API_SHUTDOWN_TIMEOUT: 8s`,
`WORKER_MAIL_SMTP_TLS: mandatory`, `WORKER_DB_MAX_CONNS: '10'`, `WORKER_QUEUE_EVENTS/LIFECYCLE/NOTIFY/MAIL/STATS/MEDIA/
MAINTENANCE: '1'` (7 слотов ≤ 10 − 3), `WORKER_GEO_IMPORT_ENABLED: 'false'`; `sync: false`: `WORKER_DATABASE_URL`,
`WORKER_IDENTITY_SECRET_KEYS`, `WORKER_MAIL_SMTP_ADDR`, `WORKER_MAIL_FROM`, `WORKER_MAIL_SMTP_USERNAME`,
`WORKER_MAIL_SMTP_PASSWORD`. `WORKER_LOG_*` не задаются (журнал — api). Комментарии: встроенный режим — только стенд
(спека 2026-11-16, Task 21 плана I-2 заменён); прямая строка Neon роли `worker`; кольцо = `API_IDENTITY_SECRET_KEYS`;
бюджет 8 + 2 + 8 + 5 = 23 с. `wf-admin-api`: `ADMIN_WAKE_API_URL` — `sync: false` (адрес `wf-api` из панели). Шапки
сервисов — «воркера на Render нет» → «фон — встроенный в wf-api».

**Стражи `render_stand_test.go`.**
- `apiPanel` + шесть ключей воркера; `apiFill` + значения (`WORKER_DATABASE_URL=postgres://worker@db.example/wf?
  sslmode=require`, кольцо `key32` — то же, что у api, SMTP `smtp.resend.com:2587`, …); `adminFill` +
  `ADMIN_WAKE_API_URL=https://wf-api.onrender.com`.
- `TestRenderYAMLKnownKeys` — известные ключи по сервису: `wf-api` — `API_`, `MIGRATOR_` и `WORKER_` (`config.Worker`,
  `config.Mail`, `config.WorkerIdentity`); `wf-admin-api` — только `ADMIN_` (`WORKER_` у admin-api — ошибка).
- `TestRenderYAMLEmbeddedWorker` (новый) → флаг `true` (`apiConfig(t).EmbedWorker`); `config.Load` `Worker`, `Mail`,
  `WorkerIdentity` с префиксом `WORKER_` из окружения `wf-api` с `apiFill` разбираются; `GeoImportEnabled == false`;
  `queue.Slots(queue.Config(q))` без `import` ≤ `WORKER_DB_MAX_CONNS − queue.PoolReserve`; `WORKER_MAIL_SMTP_TLS =
  mandatory`; в `apiFill` кольца api и воркера равны (страж заглушек, кольцо стенда — в панели).
- `TestRenderYAMLPools` → `wf-api` = `API_DB_MAX_CONNS` + при флаге `WORKER_DB_MAX_CONNS` + 1 (`LISTEN` relay вне
  пула); сумма с мигратором, admin-api и `operatorCommandsReserve` ≤ 108.
- `TestRenderYAMLStopBudget` → при флаге `config.APIEmbeddedStopTime(API_SHUTDOWN_TIMEOUT)` ≤ `StopBudget`;
  `API_SHUTDOWN_TIMEOUT` задан в файле.
- `TestRenderYAMLSecretsNotInFile` → секреты и адреса воркера только `sync: false` (слова уже ловят `DATABASE_URL`,
  `SECRET`, `SMTP_ADDR`, `MAIL_FROM`, `USERNAME`, `PASSWORD`); `ADMIN_WAKE_API_URL` — через `adminFill`.
- `TestRenderYAMLNoWorker` — без изменений (отдельного сервиса воркера нет), комментарий — на спеку 2026-11-16.
- `TestRenderYAMLIdentityEnv` — комментарий «воркер на стенде с кольцом — Task 21» → «кольцо воркера — у wf-api
  (встроенный режим), `WORKER_IDENTITY_SECRET_KEYS`».

**Страж `server_compose_test.go`** (R8; `repoFS`, YAML как в `render_stand_test.go`):
- `TestServerComposeNoEmbeddedWorker` → `deploy/server/compose.yaml`: у сервиса `api` в `environment`
  `API_EMBED_WORKER` = `"false"` (строкой); сервис `worker` с командой `worker`; `migrate` с `migrate up`; у `api`,
  `admin-api`, `worker` — `depends_on.migrate.condition = service_completed_successfully` и `stop_grace_period` ≥ 30 с.
- `TestServerEnvExamplesKnownKeys` → ключи `deploy/server/<сервис>.env.example` известны config своего префикса
  (`api.env` — `API_`, `admin-api.env` — `ADMIN_`, `worker.env` — `WORKER_` (`Worker`, `Mail`, `WorkerIdentity`),
  `migrate.env` — `MIGRATOR_`); в `api.env.example` нет `API_EMBED_WORKER=true`; значения ключей со словами секрета
  (как в `TestRenderYAMLSecretsNotInFile`) — плейсхолдеры `<…>`; **полнота:** каждый ключ с `required`/`notEmpty` в
  тегах config своего префикса (через `env.GetFieldParamsWithOptions`: `Required`, `NotEmpty`) есть в шаблоне сервиса
  (`worker.env` — и `Mail`, `WorkerIdentity`) — иначе сервис на сервере не стартует.

**Документы.**
- `docs/deploy-dev.md`: таблица «Как устроено» (API — встроенный воркер), ограничения стенда (фон есть, пока api не
  спит; периодика — при пробуждении; Neon бодр, пока api не спит — m6), таблица переменных `wf-api` (новые строки,
  прямая строка `worker`), раздел «Импорт городов GeoNames» (на стенде импорт из админки не исполнится: очередь выключена
  — кнопка «Отменить»; импорт — командой оператора), «admin-api» (`ADMIN_WAKE_API_URL`), порядок выкатки (спека §4 —
  дословно по шагам), «Потом — свой сервер» → ссылка на `deploy/server/README.md`.
- `.claude/rules/env.md`: `API_EMBED_WORKER` (только стенд), `WORKER_*` у `wf-api` на стенде, одно кольцо проверяется
  на старте; `deploy/server/` — в списке `.env.example`.
- `.claude/rules/backend-platform.md`: абзац `RunService` — встроенный режим api, `APIEmbeddedStopTime`; два способа
  запуска `workerhost`.
- План I-2, заголовок Task 21: строка «> Заменено спекой `2026-11-16-embedded-worker-design.md` и планом
  `2026-11-16-embedded-worker.md` (решение пользователя 16.11). Шаги 2 (кольца `API_IDENTITY_*` в панели, копия у
  пользователя) и 3a (проверка Neon до мержа миграций) выполнены 16.11 при выкатке PR #16».
- `CLAUDE.md`: What NOT to do — «Docker/WSL на машине не работают — Docker только в CI» → «Docker Desktop работает
  (16.11): `deploy/server` проверяется локально compose; WSL не работает; Docker не входит в `./task ci`»; Проверки —
  строка с командой локальной проверки `deploy/server` (по README Task 5: `docker compose -p wf-srv … config` / `up -d
  --wait` / `down -v`).
- `docs/deploy-dev.md` (побудка): вне покрытия — тикер `staff/jobs.Cleaner`, команды оператора, `409` approve с
  закоммиченным `failed`/`expired`.
- Спека бэкенда: §2 — строка «Воркер: отдельный процесс; на dev-стенде — встроенный режим api (спека 2026-11-16)»; §9 —
  абзац о `workerhost` и двух способах запуска (§11 — схема, правок нет: R12).

**Подсадка.**
- `WORKER_IDENTITY_SECRET_KEYS` значением в файле → `TestRenderYAMLSecretsNotInFile` красный.
- `API_SHUTDOWN_TIMEOUT: 12s` → `TestRenderYAMLStopBudget` красный.
- `WORKER_QUEUE_EVENTS: '5'` → `TestRenderYAMLEmbeddedWorker` красный.
- граничное `WORKER_DB_MAX_CONNS: '58'` в `render.yaml` (сумма с `+1` LISTEN = 109 > 108) → `TestRenderYAMLPools`
  обязан покраснеть (KILLED); зелёный — значит `+1` не учтён.
- `WORKER_RELAY_BATCH` у `wf-admin-api` → `TestRenderYAMLKnownKeys` красный.
- `API_EMBED_WORKER: "true"` в `compose.yaml` → `TestServerComposeNoEmbeddedWorker` красный.

**Приёмка.** `go test ./internal/platform/config/...` зелёный; `render.yaml` разбирается стражами; документы
описывают встроенный режим, сон, порядок выкатки, побудку; `git grep -nE "воркера нет|Воркер на стенде не запущен|воркера
на Render нет" -- docs render.yaml .claude CLAUDE.md` — пусто (кроме исторических планов и спек).

**Ловушки.** Пулы Neon (112 − 4): api 10 + воркер 10 + 1 + мигратор 10 + admin 10 + 20 = 61. Импорт на стенде выключен
— `WORKER_QUEUE_IMPORT` в файле не задаётся. `backend.yml` paths + `deploy/server/**` — страж читает эти файлы.

**Не трогай.** Go вне `config/*_test.go`; `deploy/server/**` (только читать; правка — исполнителю Task 5 через
контроллера).

### Task 5: свой сервер — `deploy/server` и проверка в CI

**Цель.** Переход со стенда на свой сервер — правкой конфигурации: `docker compose` из того же образа (`migrate`, `api`,
`admin-api`, `worker`, `caddy`), документация и job CI, который поднимает compose и проверяет исполнение фона в обоих
режимах (спека §5).

**Риск:** B (Docker Desktop локально, CI — дополнительно; секреты в шаблонах; прод-запрет встроенного режима).

**Файлы.** Создать: `deploy/server/compose.yaml`, `deploy/server/compose.ci.yaml`, `deploy/server/compose.ci-embed.yaml`
(R9), `deploy/server/Caddyfile`, `deploy/server/.env.example` (уровень compose: `WF_IMAGE`, `WF_API_DOMAIN`,
`WF_ADMIN_DOMAIN`, `WF_ACME_EMAIL`), `deploy/server/{api,admin-api,worker,migrate}.env.example`,
`deploy/server/README.md`. Изменить: `.gitignore` (R13), `.github/workflows/image.yml` (job `server`, paths +
`deploy/server/**`),
`docs/versions.md` (образ Caddy — последняя стабильная, точный тег; `postgis/postgis:18-3.6` — строка «CI и вариант
базы на сервере»).

**`compose.yaml`.**
- Общий якорь бэкенда: `image: ${WF_IMAGE:?…}`, `restart: unless-stopped`, `stop_grace_period: 30s`, сеть `wf` с
  фиксированной подсетью (R10).
- `migrate`: `command: ["migrate", "up"]`, `env_file: migrate.env`, `restart: "no"`.
- `api`: `command: ["api"]`, `env_file: api.env`, `environment: { API_EMBED_WORKER: "false" }` (явно, перекрывает
  файл; комментарий — запрет встроенного режима в проде, ссылка на README), `depends_on: migrate:
  service_completed_successfully`, healthcheck `wget -q -O /dev/null http://127.0.0.1:<порт>/healthz` (в образе alpine
  нет curl; busybox `wget` есть), порты наружу не публикуются.
- `admin-api`: `command: ["admin-api"]`, `env_file: admin-api.env`, `ADMIN_STATIC_DIR=/app/admin` в шаблоне, тот же
  healthcheck, `depends_on` migrate.
- `worker`: `command: ["worker"]`, `env_file: worker.env`, `depends_on` migrate; без healthcheck (нет HTTP).
- `caddy`: образ Caddy (тег из `docs/versions.md`), порты 80, 443, 443/udp, тома `caddy_data`, `caddy_config`,
  `./Caddyfile:/etc/caddy/Caddyfile:ro`, переменные доменов, `depends_on` api/admin-api `service_healthy`.
- Postgres по умолчанию — Neon (строки в `*.env`); вариант «база на сервере» — закомментированный сервис
  `postgis/postgis:18-3.6` с томом и `../dev/initdb` и описание в README, не по умолчанию.

**`Caddyfile`.** `{$WF_API_DOMAIN}` → `reverse_proxy api:<порт>`, `{$WF_ADMIN_DOMAIN}` → `reverse_proxy admin-api:<порт>`;
TLS автоматически (ACME, `email {$WF_ACME_EMAIL}`). Клиентский IP — `X-Forwarded-For` от Caddy (Caddy без
`trusted_proxies` ставит адрес пира): в `api.env.example` `API_TRUSTED_PROXIES=<подсеть wf>`, `API_CLIENT_IP_HEADER`
пусто; в `admin-api.env.example` так же; комментарий — с CDN впереди нужен `trusted_proxies` Caddy и `*_CLIENT_IP_HEADER`.
`local_certs` для CI включается без правки прод-файла (переменная-плейсхолдер в глобальном блоке или отдельный CI-файл
— решение исполнителя; прод-Caddyfile `local_certs`/`tls internal` не содержит).

**Шаблоны `*.env.example`.** Только плейсхолдеры секретов (`<base64-32-байта>`, `postgres://worker:<пароль>@<хост>/wf?
sslmode=require`); все обязательные ключи каждого бинарника; кольцо identity — одно значение у api и worker
(комментарий); `ADMIN_ORIGIN=https://<admin-домен>`; `WORKER_MAIL_SMTP_TLS=mandatory`; `ADMIN_WAKE_API_URL` не
задаётся (на сервере не нужен). Рабочие `deploy/server/{api,admin-api,worker,migrate}.env` шаблон `.env` корневого
`.gitignore` не ловит (имя файла не `.env`) — добавить `deploy/server/*.env` (R13); `deploy/server/.env` уже под ним.

**`compose.ci.yaml`** (CI поверх основного): сервис `db` (`postgis/postgis:18-3.6`, `POSTGRES_PASSWORD`,
`../dev/initdb:/docker-entrypoint-initdb.d:ro`, healthcheck `pg_isready`); `depends_on: db: service_healthy` у
`migrate`; Caddy с `local_certs`. **`compose.ci-embed.yaml`** (второй прогон): у `api` `API_EMBED_WORKER: "true"` и
`env_file` + `worker.env`; сервис `worker` выключен (`profiles: [off]`).

**Job `server` в `image.yml`** (`timeout-minutes: 20`, без `services:` — база в compose):
1. checkout; `docker build -f backend/Dockerfile -t wf-backend .`.
2. Подготовка env: `cp deploy/server/<x>.env.example <x>.env` и подстановка значений CI (строки к `db` под ролями
   `migrator/api/admin/worker` с паролями из `20_wf.sql`, одно сгенерированное кольцо identity у api и worker, сиды JWT,
   ключи PoW и сотрудников, SMTP `127.0.0.1:1` с TLS `none`, `WF_IMAGE=wf-backend`, домены `api.wf.test`, `admin.wf.test`).
3. `docker compose --env-file .env.example -f compose.yaml config --format json` → `jq -e
   '.services.api.environment.API_EMBED_WORKER == "false"'`; то же с `-f compose.ci.yaml`.
4. `up -d` (основной + ci); дождаться выхода `migrate` с кодом 0; корень Caddy —
   `docker compose exec caddy cat /data/caddy/pki/authorities/local/root.crt`; `curl --cacert … --resolve
   api.wf.test:443:127.0.0.1 https://api.wf.test/readyz` и то же для `admin.wf.test` (цикл до 60 с).
5. Журнал `worker` содержит «воркер запущен» (цикл до 60 с).
6. `docker compose exec db psql -U postgres -d wf -tAc "SELECT count(*) FROM river_job WHERE kind =
   'ratelimit.cleanup' AND state = 'completed'"` ≥ 1 (цикл до 120 с: выборы лидера, RunOnStart).
7. `down -v`; второй прогон с `compose.ci-embed.yaml`: те же шаги 4–6, журнал «воркер запущен» — у `api`
   (`"component":"worker"`), сервиса `worker` нет.
8. При любом отказе — `docker compose logs` (trap, как в шаге admin-api `image.yml`).
- `paths` workflow + `deploy/server/**`.

**README** (`deploy/server/README.md`): подготовка сервера (Docker, DNS-записи доменов на сервер, порты 80/443),
заполнение `*.env` (кольцо одно у api и worker; строки — прямые, не pooler), выкатка (`docker compose pull && up -d`:
`migrate` первым), **запрет встроенного режима в проде**, переход со стенда (спека §5 I8: сервер на том же коммите →
`API_EMBED_WORKER=false` на Render → DNS → сервисы Render приостановить; ждущие `geo.import` на стенде — отменить до старта
серверного воркера), бэкапы (`pg_dump` по расписанию — для варианта «база на сервере»; Neon — его снимки), обновления,
лимит часов Neon free при постоянно работающем воркере (m6), проверка здоровья `/healthz` (не `/readyz`); раздел
«Проверка локально» — команды «Done when» (на них ссылается `CLAUDE.md`, Task 4).

**Done when** (локально, Docker Desktop; проект только свой `-p wf-srv`, каталог `deploy/server`):
1. `docker build -f backend/Dockerfile -t wf-backend .` из корня — в фоне (`run_in_background`), долго.
2. Рабочие `*.env` из шаблонов — скриптом в scratchpad (значения CI-типа, секреты сгенерировать, не выводить; файлы
   под `.gitignore`, R13).
3. `docker compose -p wf-srv --env-file .env.example -f compose.yaml config --format json` → проверка флага через `node`
   (на машине нет `jq`; в CI — `jq`): `services.api.environment.API_EMBED_WORKER === "false"`; то же с `-f compose.ci.yaml`.
4. `up -d --wait` (основной + ci); порты публикации Caddy — переменными (`WF_HTTP_PORT`, `WF_HTTPS_PORT`, по умолчанию
   80/443; локально — свободные, 80/443 хоста могут быть заняты); `/readyz` api и admin — `curl --cacert <root Caddy>
   --resolve api.wf.test:<порт>:127.0.0.1` (или изнутри сети compose).
5. `docker compose -p wf-srv logs worker` содержит «воркер запущен»; `river_job` — строка `ratelimit.cleanup` в
   `completed` (через `docker compose -p wf-srv exec db psql …`, цикл до 120 с).
6. `docker compose -p wf-srv down -v` — только свой проект; чужие контейнеры, тома и образы не трогать.
- Второй прогон (встроенный режим, `compose.ci-embed.yaml`) — локально только после коммита Task 2: перенесён в ворота
  волны 3 (контроллер).

**Подсадка.** `node scripts/plant.mjs --file deploy/server/compose.yaml --find 'API_EMBED_WORKER: "false"' --replace
'API_EMBED_WORKER: "true"' -- <команда шага 3>` → проверка флага красная (KILLED); база без подсадки — зелёная.

**Приёмка.** Шаги 1–6 «Done when» пройдены локально (вывод — в отчёт); job `server` зелёный на PR (оба прогона);
шаблоны без секретов (gitleaks в pre-commit); README покрывает спеку §5.

**Ловушки.**
- `env_file` с отсутствующим файлом валит `compose config` — CI сначала копирует шаблоны.
- `API_EMBED_WORKER` в `environment` — строкой `"false"` (YAML bool `false` compose превратит в `"false"`, но страж и
  `jq` сравнивают строку — писать строкой).
- `mail.NewSMTP` с TLS `none` — только петля: в контейнере `127.0.0.1:1` — петля контейнера, старт проходит, писем нет.
- Сервис `db` на `5432` внутри сети, наружу не публикуется (локально не мешает системному PostgreSQL :5432 и
  `scripts/pg.sh` :15432 — их не трогать).
- Один `docker build` за раз; долгий — в фоне, ожидание — Monitor/уведомление, не повторный запуск.
- Сид городов и права `grants.sql` применяет `migrate up` — роли из `20_wf.sql` уже есть.

**Не трогай.** Go-код, `render.yaml`, `backend/Dockerfile`, `backend.yml`, job `image` в `image.yml` (только новый job и
`paths`).

### Task 6: выкатка на стенд (контроллер, с согласия пользователя)

**Цель.** Встроенный воркер и побудка работают на `wf-api`/`wf-admin-api` стенда (спека §4, §8).

**Риск:** B (панель Render, Neon, необратимые правки `river_job`).

**Исходное состояние** (проверено контроллером 16.11): на стенде релиз I-2 — деплой `cb4233c` live, кольца
`API_IDENTITY_*` заданы в панели (копия у пользователя), `goose_db_version` = 26, `/readyz` 200, `/v1/auth/methods` 200.

**Шаги** (контроллер; значения секретов не выводить; правки базы — только после «да» пользователя):
0. Сверить, что так и осталось: `/healthz` и `/readyz` `wf-api` — 200; `SELECT max(version_id) FROM goose_db_version
   WHERE is_applied` = 26; журнал деплоя `wf-api` — последний live тот же коммит. Расхождение — стоп, к пользователю.
1. До мержа — Neon `wf-dev`, роль владельца: `SELECT kind, state, count(*) FROM river_job GROUP BY 1, 2`. Ждущие
   `geo.import` (`available|scheduled|retryable|pending`) — отменить **обязательно** (показать пользователю число и
   запрос, затем `UPDATE river_job SET state = 'cancelled', finalized_at = now() WHERE kind = 'geo.import' AND state IN
   (…)`); устаревшие `mail.send` — только с согласия пользователя. Накопленная с 13.11 периодика (лидер — admin-api,
   исполнителя не было): дубли ждущих периодических задач одного вида (`available`) — показать пользователю и с его
   согласия отменить, оставив по одной на вид (`identity.anonymize`, `identity.mfa_reset_execute` идемпотентны —
   оставшаяся доделает всё). Попросить не запускать импорт из админки до конца
   выкатки. Повторить запрос непосредственно перед мержем.
2. Пользователь задаёт в панели `wf-api`: `WORKER_DATABASE_URL` (прямая строка Neon роли `worker`, `sslmode=require`,
   без `-pooler`), `WORKER_IDENTITY_SECRET_KEYS` (= `API_IDENTITY_SECRET_KEYS`), `WORKER_MAIL_SMTP_ADDR`,
   `WORKER_MAIL_FROM`, `WORKER_MAIL_SMTP_USERNAME`, `WORKER_MAIL_SMTP_PASSWORD` (как у `wf-admin-api`); в панели
   `wf-admin-api`: `ADMIN_WAKE_API_URL=https://wf-api-o3rn.onrender.com`. Роль `worker` на Neon уже есть
   (`docs/deploy-dev.md`, «Роль worker»): сверить права (`has_table_privilege('worker', 'outbox', 'UPDATE')`).
3. Мерж (после итогового ревью и `./task ci`, пуш и PR — по просьбе пользователя) → деплой → журнал `wf-api`:
   «воркер запущен» с `component=worker`, выборы лидера, `completed` у периодики.
4. Регистрация на стенде → письмо с кодом пришло → `confirmSignup` → 201.
5. Побудка: дождаться сна `wf-api` (≥ 15 мин без запросов), мутация в админке → в журнале `wf-api` «воркер запущен» в
   течение минуты. План Б (не проснулся): журнал `wf-admin-api` — предупреждение побудки (срок, адрес, статус); проверить
   `ADMIN_WAKE_API_URL` в панели и ручной `curl https://<wf-api>/healthz` — будит ли запрос вообще; не будит при
   `wakeTimeout` 60 с — к пользователю (вариант — внешний пинг или платный инстанс), фон стенда до решения — с
   задержкой до пробуждения api.
6. Память: метрика памяти `wf-api` в панели Render (лимит free — 512 МБ: argon2id + River + relay в одном процессе) в
   течение суток после выкатки и в момент регистраций; при OOM или > 85 % — уменьшить `WORKER_QUEUE_*` (`mail`,
   `events` до 1 уже; снизить пул) и семафор хэшера argon2id (сейчас `GOMAXPROCS`) — правкой кода/конфигурации
   отдельной задачей по согласию пользователя; записать замер в журнал сессии.
7. Записать в журнал сессии (`.superpowers/sdd/<сессия>/progress.md`) результаты шагов 1–5 без кодов и адресов.

**Приёмка.** Шаги 0, 3–6 подтверждены журналами и метриками; ждущих `geo.import` в `maintenance` нет.

**Ловушки.** С флагом `true` api без `WORKER_*` не стартует — переменные до мержа; неудачный деплой Render старый
инстанс не гасит. Neon не засыпает, пока api бодрствует (River) — ожидаемо.

## Порядок исполнения

```
Волна 1:  Исп. А: Task 1                 Исп. Б: Task 5
Волна 2:  Исп. А: Task 2 → Task 3        (Б свободен; config.go и backend/.env.example — один владелец)
Волна 3:  Исп. Б (или А): Task 4         (после коммитов 1, 2, 3, 5: стражи читают поля config и deploy/server)
          ворота волны 3 (контроллер): локальный второй прогон compose — compose.ci-embed.yaml, встроенный режим
          (шаги 3–6 «Done when» Task 5: «воркер запущен» у api с component=worker, river_job completed)
Волна 4:  итоговое ревью ветки → волна исправлений → ./task ci → пуш и PR (по просьбе) → Task 6 (контроллер)
Ревью — одно на группу исполнителя в волне (wf-reviewer, opus, effort high).
```

- Пути волны 1 не пересекаются: А — `backend/**` (Go, `.golangci.yml`, `.env.example`), Б — `deploy/server/**`,
  `.github/workflows/image.yml`, `docs/versions.md`, `.gitignore`.
- `config.go`, `config_test.go`, `backend/.env.example`, `backend/.env` — задачи 1, 2, 3 одним исполнителем по очереди
  (R15); коммиты контроллера — по задаче.
- Task 2 требует экспорта `workerhost` из Task 1 (коммит 1 — до старта 2). Task 3 от 1–2 не зависит, но делит файлы
  конфига.
- Task 5 пишет имена переменных из плана (`API_EMBED_WORKER`, `WORKER_QUEUE_IMPORT`, `WORKER_GEO_IMPORT_ENABLED`);
  основной прогон compose — в Task 5 локально (отдельный воркер работает и до Task 2), встроенный — ворота волны 3;
  job CI `server` — дополнительно на PR (пуш — только перед PR).

## Решения плана

- **R1. `Load` + `Prepare(base, environ, log)` вместо `Prepare(environ, log)` спеки.** Журнал отдельного воркера строится
  из `WORKER_LOG_*` (часть `config.Worker`), а предупреждения SMTP должны идти в него (`TestRunWarnsOpportunisticSMTP…`);
  разовым командам нужны `Base` без почты и кольца. Во встроенном режиме журнал — api (`startLog` + `component=worker`).
- **R2. Реестровые тесты — в `workerhost` (белый ящик), процессные — в `cmd/worker`.** API пакета не раздувается
  экспортом `realWorkers`/`allWorkers`/`riverConfig`/`mails`; ожидания тестов не меняются.
- **R3. Флаг импорта — `config.Worker.GeoImportEnabled`, очередь — `config.Queues.Import`.** `queue.Config` отдаёт все
  восемь очередей; выключение — в `workerhost.queues`. `TestWorkerKindsQueues` — на значениях по умолчанию с флагом
  `true` (спека §8.2).
- **R4. Проверка очередей ≥ 1 до базы — новая.** Спека пишет «по-прежнему», но в коде её нет (River отказывает в
  `NewClient` после подключения); добавляется в `Prepare` (не в разовые команды).
- **R5. Мягкая остановка River встроенного режима — 8 с (спека: ориентир 10).** Дренаж по умолчанию 10 + 2 + 8 + 5 = 25 —
  страж по образцу `TestAdminShutdownBudget` на умолчаниях проходит; стенд 8 + 2 + 8 + 5 = 23.
- **R6. Сверка колец — по ID ключей: `secrets.IDs()` api против `Prepared.RingIDs()` воркера, в порядке списка (ID —
  `keys.ID` от раскодированного ключа).** Тот же ключ в base64 и base64url — одно кольцо;
  другой порядок (другой текущий ключ) — отказ.
- **R7. Побудка — в `internal/httpapi/admin/wake.go`** (`Waker`, `ValidateWakeURL`, middleware); мутация — метод не
  `GET/HEAD/OPTIONS` у маршрута контракта; успех — 200–299.
- **R8. Go-страж `deploy/server`** (`server_compose_test.go`, пакет `config`) — быстрый страж в `backend:test` без
  Docker: запрет встроенного режима, ключи и полнота шаблонов ловятся на каждом прогоне тестов, а не только при ручном
  `docker compose` или на PR.
- **R9. Второй прогон CI — overlay `compose.ci-embed.yaml`**, не переменная в `compose.yaml`: прод-файл держит `"false"`
  литералом (спека §1, §5).
- **R10. Сеть compose с фиксированной подсетью** — значение `API_TRUSTED_PROXIES`/`ADMIN_TRUSTED_PROXIES` в шаблонах.
- **R11. `Run` = `nil` при отменённом ctx в любой фазе до `Start`** (`ctx.Err() != nil`), после `Start` — результат
  `queue.Stop` как сейчас.
- **R12. Спека бэкенда: §2 и §9** (в спеке §7 указан «§11» — это раздел схемы, правок схемы нет).
- **R13. `.gitignore` + `deploy/server/*.env`** — рабочие файлы сервера с секретами.
- **R14. «Фон под ролью `worker`» — поведенческий страж через relay outbox** (у `api` на `outbox` только `INSERT`). Не
  `current_user` и не `pg_stat_activity`: в тестах роль задаётся `-c role=…` поверх суперпользователя — `usename`
  (сессионный пользователь) у обоих пулов один, а `current_user` пришлось бы читать подсадкой в исполнителе.
- **R15. Общие файлы конфига** — один исполнитель задач 1→2→3, параллельно только Task 5 (свои пути).
- **R16. Срок побудки — 60 с (спека §8.1: 2 с).** Спящий инстанс Render free просыпается до минуты; оборванный за 2 с
  запрос может не довести пробуждение. Запрос в фоне, ответ не держит; троттлинг (5 с, один в полёте) сохраняется.
  Спеку поправить после итогового ревью.

## Открытые вопросы

1. Устаревшие `mail.send` и дубли накопленной периодики на Neon (Task 6, шаг 1) — решение пользователя по факту
   запроса.
