# Воркер: встроенный режим api (стенд) и свой сервер — без переделок при переходе

Дата: 16.11.2026. Ред. 3 — Approved (ревью: I1–I9, m1–m8; раунд 2: N1–N3, m-r2-1…3; раунд 3: minor 1–5 внесены). Основание — решение пользователя 16.11: фонового воркера на
Render бесплатно нет (render.com/docs/free: бесплатные инстансы — web, Postgres, Key Value); фон должен работать на
бесплатном стенде **сейчас**, переход на свой сервер с доменом — **правкой конфигурации, без правок кода**.

## 1. Цель и рамки

- **Сейчас (путь 1):** `wf-api` (бесплатный web-сервис Render) по флагу запускает в своём процессе тот же воркер, что
  `cmd/worker`: исполнители River, relay outbox, периодику.
- **Потом (путь 3):** свой сервер — `docker compose` из того же образа: `migrate`, `api`, `admin-api`, `worker`,
  обратный прокси с TLS. Переход: у api флаг выключен, те же `WORKER_*` — отдельному процессу `worker`.
- **Решения пользователя 16.11:** встроенный режим — отступление от «Ловушек» Task 21 плана I-2 («не подменять воркер
  веб-сервисом») **только для dev-стенда**; Task 21 заменяется этой спекой. Риск принимается для стенда (I9): процесс
  публичного API держит строку роли `worker` (её права шире `api`: `cities`/`regions`, `geonames_*`, `outbox`,
  `role_assignments` — `grants.sql`) и пароль SMTP; кольцо identity у api есть и так.
- **Прод — только отдельный процесс:** в `deploy/server/compose.yaml` у api `API_EMBED_WORKER=false` явно; CI
  проверяет это по `docker compose config`; `deploy/server/README.md` — запрет.
- **Не входит:** аренда сервера и домена, провижининг (Ansible/Terraform), веб `apps/web` (в образе и compose его
  нет), наблюдаемость сверх логов и проб, перенос Neon на свой Postgres (описан как вариант).

## 2. Единый код воркера — `backend/cmd/internal/workerhost`

- Пакет лежит в **`backend/cmd/internal/workerhost`**: импортировать его могут только `cmd/*` (правило Go для
  `internal`); он вне `wf/backend/internal/`, поэтому archtest модулей его не ограничивает (`TestInternalDirsAreKnown`,
  `importViolation`), а транзитивные стражи `TestPublicBinariesDoNotDepend*` его видят. depguard (`.golangci.yml`,
  `files`) — расширить на этот путь (как на `cmd/*`).
- Переезжает из `cmd/worker/main.go`: конфиг и проверки до базы (`config.Worker`, `config.Mail`,
  `config.WorkerIdentity`, relay, GeoNames, SMTP, кольцо, локали), `waitSchema`, клиент вставки, `subscriptions()`,
  `mails()`, `allWorkers`/`realWorkers`, `riverConfig`, relay и остановка.
- API (имена — в плане):
  - `Prepare(environ, log) (Prepared, error)` — всё до базы; ошибка — отказ старта вызывающего до базы;
  - `Run(ctx, prepared, pool, log, stop Stop) error`, `Stop{Relay, Soft time.Duration}` (жёсткая — `config.HardStop`):
    сроки — параметр (отдельный воркер — `config.WorkerRelayStop`/`WorkerRiverSoftStop`, встроенный — свои, §3);
    отмена ctx (в том числе во время `waitSchema`) — **`nil`**, не ошибка: обычная остановка не даёт выход с кодом 1.
- `cmd/worker` = `Prepare` + `db.Open(WORKER_DATABASE_URL)` + `Run`; разовые команды (`events replay`, `geo import`)
  остаются в `cmd/worker`. Ожидания тестов `cmd/worker` (виды, очереди, обслуживание River, бюджет, ключи на старте)
  не меняются; вызовы `realWorkers`, `allWorkers`, `riverConfig`, `runOn` в тестах переписываются на API пакета.
- Документы, ссылающиеся на `subscriptions()` в `cmd/worker/main.go` (`backend-platform.md`), — на новый путь.

## 3. Встроенный режим api

- Флаг **`API_EMBED_WORKER`** (bool, по умолчанию `false`) — поле `config.API`.
- `false` — `cmd/api` как сейчас (`server.RunHTTP`); `WORKER_*` не читаются и не требуются.
- `true`:
  - до базы — `workerhost.Prepare` с переменными **`WORKER_*`**; нет `WORKER_DATABASE_URL`, почты или кольца — отказ
    старта api до базы; `WORKER_IDENTITY_SECRET_KEYS` не равно `API_IDENTITY_SECRET_KEYS` — отказ старта (m1:
    одно кольцо, `env.md`; полуротация в панели иначе ломает письма с кодами); сравниваются разобранные списки ключей
    (разделитель окружения), не сырые строки; неудачный деплой на Render старый инстанс не гасит — безопасно;
  - **второй пул** открывает `run` после `Prepare` (`WORKER_DATABASE_URL`, роль `worker`, прямое подключение без
    pooler — relay и River держат `LISTEN`), закрывается после остановки фона (`defer` вне `RunService`); пул api —
    по-прежнему роль `api`;
  - запуск через `server.RunService`: `Background` = `workerhost.Run` на пуле воркера; журнал фона — журнал api с
    меткой `component=worker` (без второго `slog.SetDefault`);
  - **бюджет остановки** ≤ `config.StopBudget` (25 с из окна Render 30 с): дренаж HTTP `API_SHUTDOWN_TIMEOUT` +
    relay + River мягко + жёстко 5 с. Встроенный режим — свои константы (ориентир: дренаж 8 + relay 2 + мягко 10 +
    жёстко 5 = 25) и `config.APIEmbeddedStopTime(drain)`; на стенде `API_SHUTDOWN_TIMEOUT=8s` явно; страж — по образцу
    `TestAdminShutdownBudget`.
- `api identity rekey` и прочие разовые команды `cmd/api` флаг не читают.
- **Сон бесплатного сервиса (допущение только стенда, I4, m5):**
  - письма из запросов к api уходят сразу (запрос будит процесс);
  - письма и события, которые ставит **admin-api**, уходят сразу: admin-api будит api после успешной мутации (§8.1);
  - периодика (`identity.mfa_reset_execute`, `identity.anonymize`, чистки) идёт, пока api не спит: River ставит
    задачи `RunOnStart`, когда клиент становится лидером; пока лидер admin-api, задачи копятся и исполняются при
    пробуждении api — исполнение сброса 2FA может опоздать, но не прийти раньше срока; окно отмены сохраняется
    (уведомление о назначении ушло сразу).
  - пока api не спит, River держит Neon бодрым (m6) — записать в `deploy-dev.md`.
- **`geo.import` во встроенном режиме (I5):** импорт держит в памяти архив до 50 МБ и идёт до 30 мин; на бесплатном
  инстансе он конкурирует с HTTP (argon2id) и прерывается сном. Решение — §8.2.
- **Очереди стенда (m2):** малые `WORKER_QUEUE_*`, чтобы слоты ≤ пула − 3 (`WarnPool`).
- **Лидер и рескьюер:** встроенный воркер регистрирует ровно то же, что отдельный (общий код), включая заглушки
  `AddForeign(AdminKinds())`; лидер — один из api/admin-api, периодика — общий список.
- **Границы:** `cmd/api` не зависит от `staff` и пакетов `admin` (archtest) — `workerhost` их не импортирует.

## 4. Стенд Render (путь 1)

- `render.yaml`, `wf-api`: в файле — `API_EMBED_WORKER=true`, `API_SHUTDOWN_TIMEOUT=8s`, `WORKER_MAIL_SMTP_TLS=mandatory`,
  `WORKER_DB_MAX_CONNS`, малые `WORKER_QUEUE_*`, `WORKER_GEO_IMPORT_ENABLED=false` (§8.2); **`sync: false`** — `WORKER_DATABASE_URL`,
  `WORKER_IDENTITY_SECRET_KEYS`, `WORKER_MAIL_SMTP_ADDR`, `WORKER_MAIL_FROM`, `WORKER_MAIL_SMTP_USERNAME`,
  `WORKER_MAIL_SMTP_PASSWORD` (как у admin-api: адрес, отправитель и логин — тоже в панели).
- Стражи `render_stand_test.go`: `TestRenderYAMLKnownKeys` — префикс `WORKER_` у `wf-api` с полями `config.Worker`,
  `config.Mail`, `config.WorkerIdentity`; `apiPanel`/`apiFill` — новые ключи панели; `TestRenderYAMLPools` — пул
  `wf-api` плюс `WORKER_DB_MAX_CONNS` и +1 соединение `LISTEN` relay (вне пула) при флаге; `TestRenderYAMLStopBudget` — полная сумма встроенного режима;
  `TestRenderYAMLSecretsNotInFile` — секреты и адреса воркера только `sync: false`; `TestRenderYAMLNoWorker` — верен.
  Комментарии «Task 21» в `render.yaml` и тесте — на эту спеку.
- **Порядок выкатки** (пользователь в панели, контроллер проверяет):
  1. до мержа — очередь на Neon: `SELECT kind, state, count(*) FROM river_job GROUP BY 1, 2`; ждущие `geo.import` —
     отменить **обязательно** (§8.2); устаревшие `mail.send` (коды регистраций, пришедшие бы тестерам с опозданием) —
     с согласия пользователя; повторить запрос непосредственно перед мержем и не запускать импорт из админки, пока
     идёт выкатка (иначе новая `geo.import` в `maintenance` исполнится сразу после старта);
  2. задать `WORKER_*` у `wf-api` в панели (до мержа: с флагом `true` api без них не стартует);
  3. мерж → деплой → журнал: «воркер запущен», лидер, периодика;
  4. регистрация на стенде → письмо с кодом пришло → `confirmSignup` → 201.

## 5. Свой сервер (путь 3) — готовим сейчас

- `deploy/server/`:
  - `compose.yaml`: `migrate` (разово, `depends_on: service_completed_successfully`), `api`
    (`API_EMBED_WORKER=false` явно), `admin-api`, `worker`, `caddy`; образ — переменная (`WF_IMAGE`, тег релиза из
    реестра); `stop_grace_period` ≥ 30 с; healthcheck живости — `/healthz` (не `/readyz`: Neon free не засыпает от
    проверок, m6);
  - `Caddyfile`: `api.<домен>` → api, `admin.<домен>` → admin-api; TLS автоматически; заголовок клиентского IP
    согласован с `API_TRUSTED_PROXIES` / `*_CLIENT_IP_HEADER`;
  - `.env.example` по сервисам (только плейсхолдеры); `README.md` — подготовка сервера, выкатка, переход со стенда,
    бэкапы (`pg_dump` по расписанию, если база на сервере), обновления, запрет встроенного режима в проде.
- Postgres: по умолчанию Neon; вариант «база на сервере» — сервис `postgis/postgis:18-3.6` с томом и
  `deploy/dev/initdb` (описание, не по умолчанию). Для Neon free — сверить лимит вычислительных часов при постоянно
  работающем воркере (m6).
- **Переход со стенда (I8):** сервер на **том же коммите**, что стенд → на Render `API_EMBED_WORKER=false` (второй
  воркер на старом релизе брал бы задачи незнакомых версий — повторы и «мёртвые» задачи) → DNS на сервер → сервисы
  Render приостановить; дальше выкатки только на сервер. Два воркера одного релиза на одной схеме безопасны: River
  делит задачи, relay — `FOR UPDATE SKIP LOCKED`, лидер один, заглушки чужих видов у всех.
- **CI (I7):** `compose.ci.yaml` поверх основного — образ `wf-backend`, собранный в job; сервис `db`
  (`postgis/postgis:18-3.6`, `deploy/dev/initdb`); Caddy с `local_certs` и проверкой через `curl --resolve`.
  Проверки: `docker compose config` с `--env-file` из `.env.example` (и что у api флаг `false`); подъём; `/readyz` у
  api и admin-api; журнал «воркер запущен»; периодика исполнилась — `psql` к `river_job` (`state = 'completed'`).
  Второй прогон — api со встроенным воркером (флаг `true`, без сервиса `worker`) — те же проверки исполнения.
  Сквозную отправку письма CI не проверяет (`mail.NewSMTP` с TLS `none` — только петля) — её проверяет выкатка на
  стенд (§4, шаг 4). `paths` в `image.yml` — плюс `deploy/server/**`.

## 6. Тесты и стражи

- `workerhost`: `Prepare` — отказы до базы; `Run` — старт и остановка на ctx; отмена во время `waitSchema` — `nil`.
- `cmd/api`, **поведенческие** (I6), на `dbtest` с флагом `true`:
  - периодическая задача с `RunOnStart` (например, `ratelimit.cleanup`) доходит до `completed`;
  - зависшая в `running` задача чужого вида (`staff.mail`) после рескьюера — `retryable`, не `discarded`;
  - соединение фона — под ролью `worker` (`current_user` в исполнителе-подсадке или `pg_stat_activity`);
  - флаг `false` — `WORKER_*` не читаются, старт без них; `true` без них или с другим кольцом — отказ до базы;
  - бюджет остановки встроенного режима; фон с ошибкой гасит HTTP; SIGTERM во время `waitSchema` — выход без ошибки.
- archtest и depguard: границы `cmd/api` — без изменений, зелёные; `workerhost` виден транзитивным стражам.
- Подсадки: флаг по умолчанию `true`; `Background` не задан; пул api вместо пула воркера; бюджет больше
  `StopBudget`; кольца разные.

## 7. Документы

`docs/deploy-dev.md` (встроенный режим, переменные, сон, порядок выкатки, Neon бодр), `deploy/server/README.md`,
`.claude/rules/backend-platform.md` (`workerhost`, два способа запуска, `subscriptions()` — новый путь),
`.claude/rules/env.md` (`API_EMBED_WORKER`, `WORKER_*` у api на стенде, проверка одного кольца),
`backend/.env.example`, план I-2 Task 21 — пометка «заменено спекой 2026-11-16», спека бэкенда §2/§11 — режимы
воркера, `render.yaml` и `render_stand_test.go` — комментарии.

## 8. Решения пользователя 16.11

1. **Сон и admin-api (I4) — admin-api будит api (N3).** Флаг стенда `ADMIN_WAKE_API_URL` (`https://`, без пути —
   проверка на старте до базы; пусто — выключено; на сервере не задаётся; `render.yaml` — `sync: false` (адрес стенда — в панели); `.env.example`; `TestRenderYAMLKnownKeys` — через `config.Admin`).
   - **Место — одно, без правки сценариев:** middleware конвейера admin-api **снаружи** `idempotency.Middleware`;
     срабатывает на ответ 2xx мутирующей операции (по правилу «успех только после коммита» 2xx = коммит; откат —
     ошибка, побудки нет). Не через `db.WithTxHook`: хук на ctx один, второй перекрыл бы хук идемпотентности.
   - Запрос `GET <url>/healthz` — в фоне на detached ctx со сроком 60 с (решение плана R16: спящий инстанс Render free
     просыпается до минуты, оборванный за 2 с запрос мог не довести пробуждение; ответ сотруднику запрос не держит), не
     чаще раза в несколько секунд
     (singleflight/пауза), ошибки — в журнал, не в ответ. Лишняя побудка (мутация без задачи для воркера, повтор по
     `Idempotency-Key`) безвредна.
   - Вне покрытия (подписчиков сейчас нет — задержка ни на что не влияет): тикер `staff/jobs.Cleaner` (событие
     `platform.approval_decided` у `ExpireDue`) и команды оператора `admin-api …`.
   - Страж: 2xx мутации → один запрос на подставной сервер; 4xx/5xx и GET → ни одного; пустой URL → ни одного;
     с включённой побудкой повтор с тем же
     `Idempotency-Key` отдаёт сохранённый ответ, сценарий исполнен один раз; подсадки — «побудка на любой статус»
     (краснеет «4xx — ни одного запроса») и «побудка до вызова `next`». На стенде — проверить, что
     оборванный по сроку запрос будит спящий инстанс (журнал api «воркер запущен» в течение минуты).
2. **`geo.import` на стенде (I5) — «правильно сразу» (N1, N2):**
   - **Своя очередь импорта** `queue.Import`: константа и `queue.Config`, поле `config.Queues`
     (`WORKER_QUEUE_IMPORT`, по умолчанию 1), `ImportArgs.InsertOpts().Queue` (и комментарий про слот
     `maintenance`), строка в `TestWorkerKindsQueues`, список очередей в `backend-platform.md` и спеке бэкенда §9.1.
     Поведение отдельного воркера меняется полезно: импорт больше не занимает слот `maintenance` до 30 мин.
   - **Выключение — отдельным флагом** `WORKER_GEO_IMPORT_ENABLED` (bool, по умолчанию `true`; `false` — очереди
     импорта нет в карте клиента River, вид зарегистрирован, задача ждёт). Не «0 слотов»: River отвергает
     `MaxWorkers < 1` при создании клиента; остальные очереди — по-прежнему ≥ 1, проверка до базы.
     `TestWorkerKindsQueues` — на значениях по умолчанию; страж стенда — в `render.yaml` импорт выключен.
   - Импорт на стенде — командой оператора `worker geo import` (как сейчас); на своём сервере отдельный воркер
     исполняет импорт из админки как обычно; кнопка импорта в админке стенда — подсказка в `docs/deploy-dev.md`.
   - **Переходный хвост — обязательно:** ждущие `geo.import` на Neon лежат в очереди `maintenance` — встроенный
     воркер исполнил бы их при первом старте; до мержа — отменить (или перевести в новую очередь; уникальность по
     аргументам без `ByQueue` это допускает). То же при переезде на сервер: ждущие на стенде импорты отменить до
     старта серверного воркера.
3. **Прочая очередь на Neon перед включением (§4, шаг 1)** — устаревшие `mail.send`: по факту запроса, с согласия
   пользователя.
