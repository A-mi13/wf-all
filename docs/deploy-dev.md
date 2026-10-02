# Dev-стенд

Публичный API для нативного приложения и ручных проверок. Бесплатно: база — Neon, API — Render.
Тот же образ потом запускается на своём сервере без изменений кода.

## Как устроено

| Часть | Где | Что |
| --- | --- | --- |
| Образ | `backend/Dockerfile` | один образ, внутри `api`, `admin-api`, `worker`, `migrate` и сценарий `migrate-then-api`; команда по умолчанию — `api`; миграции вшиты в бинарники |
| База | Neon, проект `wf-dev`, Postgres 18, регион Frankfurt | база `wf`; роль-владелец Neon = наш `migrator`, роль `api` — только DML |
| API | Render, веб-сервис `wf-api` (`render.yaml`), бесплатный тариф, Frankfurt — https://wf-api-o3rn.onrender.com | команда `migrate-then-api`: `migrate up`, затем `api` (shell в `dockerCommand` Render не разбирает — сценарий лежит в образе); проверка живости `/healthz` (процесс жив, без базы; готовность с базой — `/readyz`); деплой из `main` после зелёного CI |
| Swagger | https://wf-api-o3rn.onrender.com/docs | Swagger UI по контракту, вшитому в образ (`/docs/openapi.json` — сам контракт для генераторов клиентов); «Try it out» шлёт запросы на этот же стенд |
| Проверка образа | CI, задание `image` в `.github/workflows/backend.yml` | сборка, миграции на чистую базу, старт API, `/healthz`, `/readyz`, `/docs` — как на Render; локально Docker не нужен |

Ограничения бесплатного стенда: Render усыпляет сервис после 15 минут без запросов, первый запрос
будит его около минуты; воркера нет (на бесплатном тарифе Render фоновых процессов нет); база Neon
засыпает через 5 минут простоя и просыпается на первом подключении. Данные — только тестовые.

## Переменные окружения сервиса `wf-api`

| Переменная | Значение |
| --- | --- |
| `API_HTTP_ADDR` | `0.0.0.0:10000` (в `render.yaml`) |
| `API_LOG_FORMAT`, `MIGRATOR_LOG_FORMAT` | `json` (в `render.yaml`) |
| `API_DOCS_ENABLED` | `true` (в `render.yaml`) — Swagger на `/docs`; на проде не включать |
| `API_DATABASE_URL` | строка подключения Neon под ролью `api`, **прямая** (не pooler), `sslmode=require` |
| `MIGRATOR_DATABASE_URL` | строка подключения Neon под ролью-владельцем, прямая, `sslmode=require` |
| `API_JWT_SEEDS` | сиды Ed25519 access-токенов (base64, 32 байта) через запятую: первый подписывает, все проверяют. `generateValue: true` в `render.yaml` — Render генерирует сам при синхронизации Blueprint; без ключа API не стартует |
| `API_HUMANCHECK_KEYS` | ключи HMAC задач антибота (PoW, ALTCHA), base64 ≥ 32 байт, через запятую; `generateValue: true` — как у `API_JWT_SEEDS` |
| `API_TRUSTED_PROXIES` | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` (в `render.yaml`) — частные сети балансировщика Render: только им API верит `X-Forwarded-For`. Через запятую без висячей запятой: пустой элемент — отказ старта |
| `API_RATE_LIMITS` | не задана — умолчания `ratelimit.DefaultRules`; переопределение — `<класс>.<ip\|user\|device>=<лимит>/<период>[:<всплеск>]` или `=off`, через запятую |
| `API_BFF_NETS`, `API_BFF_SECRETS` | не заданы: BFF (Next.js) на стенде нет, заголовкам `X-WF-*` API не верит |

Воркер на стенде не запущен — переменные `WORKER_*`, в том числе `WORKER_MAIL_*`, на Render не нужны.

**Ротация ключей** (`API_JWT_SEEDS`, `API_HUMANCHECK_KEYS`): в настройках сервиса Render новый ключ
ставится первым, старый — следом через запятую (сгенерировать: `openssl rand -base64 32`); после
деплоя подписывает новый, проверяют оба. Старый убрать: у JWT — через 10 минут (срок access-токена),
у PoW — через срок задачи (`API_HUMANCHECK_TTL`, по умолчанию 5 минут). Значения ключей в git, чаты и
вывод команд не попадают.

Не проверено на стенде: в каком виде балансировщик Render передаёт `X-Forwarded-For`. Если там
`ip:port`, а не голый IP, все клиенты получат общий адрес прокси (лимиты по IP станут общими) —
проверить по логам после первого деплоя с ключами.

Строки подключения — только в настройках сервиса Render (`sync: false`), никогда в git.
Прямое подключение обязательно: relay событий и River используют `LISTEN`, с PgBouncer в
transaction mode он несовместим (спека бэкенда §6.5).

## Роли базы на Neon

Выполняется один раз владельцем базы `wf`:

```sql
CREATE ROLE api LOGIN PASSWORD '<генерируется>';
```

Права на таблицы роль получает от `migrate up` — он применяет `backend/internal/platform/grants/grants.sql`
после миграций (матрица — в его комментариях и спеке бэкенда §10.1). Роли `admin` и `worker`
создаются так же, когда на стенд выйдут admin-api и воркер; до тех пор `grants.sql` их пропускает.
Умолчания `ALTER DEFAULT PRIVILEGES`, выданные на Neon при создании стенда, надо снять: если
`migrate up` упадёт посреди наката, таблицы этого прогона останутся открыты по умолчаниям до
следующего успешного `up`. Разово, владельцем базы:

```sql
ALTER DEFAULT PRIVILEGES FOR ROLE <владелец> IN SCHEMA public REVOKE ALL ON TABLES FROM api;
ALTER DEFAULT PRIVILEGES FOR ROLE <владелец> IN SCHEMA public REVOKE ALL ON SEQUENCES FROM api;
```

## Потом — свой сервер

Тот же образ, другие команды запуска:

- перед обновлением — разовый `migrate up` (отдельный контейнер), а не в старте API;
- `api`, `admin-api`, `worker` — отдельные сервисы из одного образа (`CMD` переопределяется:
  `api` | `admin-api` | `worker`), у каждого своя роль БД и свой префикс переменных
  (`API_`, `ADMIN_`, `WORKER_`, `MIGRATOR_` — `internal/platform/config`);
- база — managed Postgres или свой Postgres 18 + PostGIS; переезд — смена строк подключения.

Детали (домены, TLS, бэкапы с проверкой восстановления, секреты) — в спеке деплоя.
