# Dev-стенд

Публичный API для нативного приложения и ручных проверок. Бесплатно: база — Neon, API — Render.
Тот же образ потом запускается на своём сервере без изменений кода.

## Как устроено

| Часть | Где | Что |
| --- | --- | --- |
| Образ | `backend/Dockerfile` | один образ, внутри `api`, `admin-api`, `worker`, `migrate`; команда по умолчанию — `api`; миграции вшиты в бинарники |
| База | Neon, проект `wf-dev`, Postgres 18, регион Frankfurt | база `wf`; роль-владелец Neon = наш `migrator`, роль `api` — только DML |
| API | Render, веб-сервис `wf-api` (`render.yaml`), бесплатный тариф, Frankfurt | при старте `migrate up`, затем `api`; проверка живости `/v1/health`; деплой из `main` после зелёного CI |
| Проверка образа | CI, задание `image` в `.github/workflows/backend.yml` | сборка, миграции на чистую базу, старт API, `/v1/health` — как на Render; локально Docker не нужен |

Ограничения бесплатного стенда: Render усыпляет сервис после 15 минут без запросов, первый запрос
будит его около минуты; воркера нет (на бесплатном тарифе Render фоновых процессов нет); база Neon
засыпает через 5 минут простоя и просыпается на первом подключении. Данные — только тестовые.

## Переменные окружения сервиса `wf-api`

| Переменная | Значение |
| --- | --- |
| `API_HTTP_ADDR` | `0.0.0.0:10000` (в `render.yaml`) |
| `API_LOG_FORMAT`, `MIGRATOR_LOG_FORMAT` | `json` (в `render.yaml`) |
| `API_DATABASE_URL` | строка подключения Neon под ролью `api`, **прямая** (не pooler), `sslmode=require` |
| `MIGRATOR_DATABASE_URL` | строка подключения Neon под ролью-владельцем, прямая, `sslmode=require` |

Строки подключения — только в настройках сервиса Render (`sync: false`), никогда в git.
Прямое подключение обязательно: relay событий и River используют `LISTEN`, с PgBouncer в
transaction mode он несовместим (спека бэкенда §6.5).

## Роли базы на Neon

Выполняется один раз владельцем базы `wf` **до первого наката миграций** — права по умолчанию
действуют только на таблицы, созданные после них:

```sql
CREATE ROLE api LOGIN PASSWORD '<генерируется>';
GRANT USAGE ON SCHEMA public TO api;
ALTER DEFAULT PRIVILEGES FOR ROLE <владелец> IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO api;
ALTER DEFAULT PRIVILEGES FOR ROLE <владелец> IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO api;
```

Это та же схема, что `deploy/dev/initdb/20_wf.sql` для локальной базы. Роли `admin` и `worker`
добавятся, когда на стенд выйдут admin-api и воркер.

## Потом — свой сервер

Тот же образ, другие команды запуска:

- перед обновлением — разовый `migrate up` (отдельный контейнер), а не в старте API;
- `api`, `admin-api`, `worker` — отдельные сервисы из одного образа (`CMD` переопределяется:
  `api` | `admin-api` | `worker`), у каждого своя роль БД и свой префикс переменных
  (`API_`, `ADMIN_`, `WORKER_`, `MIGRATOR_` — `internal/platform/config`);
- база — managed Postgres или свой Postgres 18 + PostGIS; переезд — смена строк подключения.

Детали (домены, TLS, бэкапы с проверкой восстановления, секреты) — в спеке деплоя.
