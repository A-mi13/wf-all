# Свой сервер — `docker compose`

Тот же образ бэкенда, что на dev-стенде Render (`backend/Dockerfile`), но фон — отдельным сервисом `worker`. Переход со
стенда — правкой конфигурации, без правок кода (спека `docs/superpowers/specs/2026-11-16-embedded-worker-design.md` §5).

| Сервис | Что делает |
| --- | --- |
| `migrate` | `migrate up`: миграции и права ролей — разово; остальные стартуют после его успешного выхода |
| `api` | публичный API, `:8080` внутри сети; `API_EMBED_WORKER=false` задан в `compose.yaml` явно |
| `admin-api` | API сотрудников и собранная админка (`ADMIN_STATIC_DIR=/app/admin`), `:8081` внутри сети |
| `worker` | River, relay outbox, периодика; журнал старта — `воркер запущен` |
| `caddy` | TLS (ACME) и маршрут: `WF_API_DOMAIN` → `api`, `WF_ADMIN_DOMAIN` → `admin-api`; наружу — только 80/443 |

Файлы: `compose.yaml` (прод), `Caddyfile`, `.env.example` (переменные compose), `<сервис>.env.example` (переменные
бинарников), `compose.ci.yaml` и `compose.ci-embed.yaml` — только проверка (CI и локально).

## Встроенный режим — только стенд

`API_EMBED_WORKER=true` (фон внутри api) — режим бесплатного стенда Render, где отдельного воркера нет. **На сервере
запрещён:** `compose.yaml` держит `"false"` литералом и перекрывает им `api.env`; фон исполняет сервис `worker`. Стражи —
`TestServerComposeNoEmbeddedWorker` (`backend/internal/platform/config`) и job `server` в `.github/workflows/image.yml`.

## Подготовка сервера

1. Docker Engine с плагином Compose v2.
2. DNS: записи A доменов API и админки — на адрес сервера. AAAA — только с IPv6 в сети `wf` (раздел «Клиентский IP»).
3. Открыты входящие 80/tcp (ACME HTTP-01 и редирект на HTTPS), 443/tcp и 443/udp (HTTP/3). Остальные порты наружу не
   нужны: `api`, `admin-api`, `worker` и база (если на сервере) не публикуются.
4. Каталог `deploy/server` из того же коммита, что образ.

## Переменные

- `.env` ← `.env.example`: `WF_IMAGE` (образ с тегом релиза), `WF_API_DOMAIN`, `WF_ADMIN_DOMAIN`, `WF_ACME_EMAIL`.
- `migrate.env`, `api.env`, `admin-api.env`, `worker.env` ← свои `*.env.example`; описание ключей —
  `backend/.env.example`. Рабочие `*.env` под `.gitignore` (`deploy/server/*.env`): в них секреты.
- Ключи — `openssl rand -base64 32` (у `ADMIN_STAFF_SECRET_KEYS` — base64url); кольца — через запятую, первый текущий,
  ротация — `.claude/rules/env.md`.
- **Кольцо identity одно:** `API_IDENTITY_SECRET_KEYS` в `api.env` = `WORKER_IDENTITY_SECRET_KEYS` в `worker.env`
  (письма с кодами собирает воркер).
- Строки подключения — **прямые**, не pooler Neon (goose держит блокировку на сессии, relay держит `LISTEN`); каждая —
  под своей ролью (`migrator`, `api`, `admin`, `worker`).
- Почта — SMTP провайдера с `*_MAIL_SMTP_TLS=mandatory`.
- `ADMIN_WAKE_API_URL` не задаётся: побудка спящего api нужна только на стенде.

## Клиентский IP

Caddy ставит в `X-Forwarded-For` адрес своего пира; api и admin-api верят заголовку только от подсети сети `wf`
(`172.29.0.0/24` — `networks.wf` в `compose.yaml` = `API_TRUSTED_PROXIES` = `ADMIN_TRUSTED_PROXIES`; меняешь одно —
меняй всё). `*_CLIENT_IP_HEADER` пусто. С CDN впереди: в глобальном блоке `Caddyfile` — `servers { trusted_proxies
static <сети CDN> }`, у сервисов — заголовок CDN (`CF-Connecting-IP` у Cloudflare) в `*_CLIENT_IP_HEADER`.

- **IPv6.** Сеть `wf` — только IPv4. При записи AAAA и демоне Docker без IPv6 в сети подключение по IPv6 к :443
  проксирует `docker-proxy`: Caddy видит пиром шлюз моста `172.29.0.1`, и **все** IPv6-клиенты получают один IP —
  общая корзина лимитов (массовые 429 на входе и регистрации). Поэтому либо только записи A, либо `enable_ipv6` у сети
  `wf` с v6-подсетью — и её же в `API_TRUSTED_PROXIES`/`ADMIN_TRUSTED_PROXIES`. Шлюз `.1` в доверенной подсети — не
  Caddy: доверие к нему держится на том, что снаружи порты публикует только Caddy.
- **Подсеть занята.** `172.29.0.0/24` может пересечься с другой сетью Docker на машине — `up` падает с `Pool overlaps
  with other one on this address space`. Выбрать свободную подсеть и сменить её в трёх местах: `networks.wf` в
  `compose.yaml`, `API_TRUSTED_PROXIES` в `api.env`, `ADMIN_TRUSTED_PROXIES` в `admin-api.env`.

## Выкатка и обновления

```sh
docker compose pull
docker compose up -d
```

`migrate` идёт первым; `api`, `admin-api`, `worker` стартуют после его успешного выхода, `caddy` — когда `api` и
`admin-api` здоровы. Миграция упала — сервисы не обновляются, журнал — `docker compose logs migrate`. Остановка: окно
`stop_grace_period: 30s` — бюджет процессов 25 с (`config.StopBudget`), задачи в полёте возвращаются в повтор.

Проверка здоровья — `/healthz` (процесс жив, базу не трогает), не `/readyz`: частые пробы не должны будить Neon.
`/readyz` — ручная проверка с базой после выкатки:
`curl -fsS https://<api-домен>/readyz`, `curl -fsS https://<admin-домен>/readyz`.

## Переход со стенда

Спека §5 (I8), по шагам:

1. Сервер — на **том же коммите**, что стенд (`WF_IMAGE` — тег этого коммита).
2. Ждущие задачи `geo.import` на стенде — отменить (кнопка «Отменить» в админке) до старта серверного воркера.
3. На Render у `wf-api` — `API_EMBED_WORKER=false`: второй воркер на старом релизе брал бы задачи незнакомых версий.
4. `docker compose up -d` на сервере; проверка — `/readyz` обоих доменов, `docker compose logs worker` содержит
   `воркер запущен`.
5. DNS доменов — на сервер.
6. Сервисы Render приостановить; дальше выкатки — только на сервер.

Два воркера одного релиза на одной схеме безопасны (River делит задачи, relay — `FOR UPDATE SKIP LOCKED`, лидер
один), поэтому окно между шагами 4 и 6 не опасно.

## База

**Neon (по умолчанию).** Строки в `*.env`. Постоянно работающий воркер держит соединения (`LISTEN`, опросы), и
вычислитель Neon не засыпает — до перехода сверить лимит вычислительных часов текущего тарифа Neon (на бесплатном
постоянная работа может его исчерпать). Бэкапы — средства Neon (история и точки восстановления тарифа).

**База на сервере** (не по умолчанию). В `compose.yaml` раскомментировать сервис `db` и том `db_data`, у `migrate` —
`depends_on: db: condition: service_healthy` (как в `compose.ci.yaml`); строки — `postgres://<роль>:<пароль>@db:5432/wf
?sslmode=disable` (трафик внутри сети `wf`). Роли и расширения создаёт `deploy/dev/initdb` при первом старте пустого
тома; пароли ролей там dev-значения — сразу сменить (`ALTER ROLE … PASSWORD …`) и записать в `*.env`. Бэкапы —
`pg_dump` по расписанию (cron хоста) с выносом копии с сервера, например:
`docker compose exec -T db pg_dump -U postgres -Fc wf > wf-$(date +%F).dump`; восстановление проверять.

## Проверка локально

Docker Desktop; проект — только свой `-p wf-srv`, команды — из `deploy/server`. Образ:
`docker build -f backend/Dockerfile -t wf-backend .` из корня репо (долго).

1. Рабочие `*.env` и `.env` из шаблонов со значениями CI — как шаг «env из шаблонов» job `server` в
   `.github/workflows/image.yml` (строки к `db` под ролями из `deploy/dev/initdb/20_wf.sql`, одно кольцо identity у api
   и worker, SMTP `127.0.0.1:1` с TLS `none`, `WF_IMAGE=wf-backend`, домены `api.wf.test`, `admin.wf.test`); в `.env`
   — свободные порты `WF_HTTP_PORT`, `WF_HTTPS_PORT` (80/443 хоста могут быть заняты).
2. Флаг: `docker compose -p wf-srv --env-file .env.example -f compose.yaml config --format json` →
   `services.api.environment.API_EMBED_WORKER` строго `"false"` (то же с `-f compose.yaml -f compose.ci.yaml`).
3. Подъём: `docker compose -p wf-srv -f compose.yaml -f compose.ci.yaml up -d --wait`.
4. Готовность через Caddy: корень CA — `docker compose -p wf-srv exec -T caddy cat
   /data/caddy/pki/authorities/local/root.crt > root.crt`, затем `curl --cacert root.crt --resolve
   api.wf.test:<WF_HTTPS_PORT>:127.0.0.1 https://api.wf.test:<WF_HTTPS_PORT>/readyz` и то же для `admin.wf.test`.
   В Git Bash — `MSYS_NO_PATHCONV=1` перед `docker compose exec` (иначе путь в контейнере перепишется) и
   `curl --ssl-no-revoke` (schannel Windows проверяет отзыв, у локального CA его нет).
5. Фон: `docker compose -p wf-srv logs worker` содержит `воркер запущен`; периодика —
   `docker compose -p wf-srv exec -T db psql -U postgres -d wf -tAc "SELECT count(*) FROM river_job WHERE kind =
   'ratelimit.cleanup' AND state = 'completed'"` ≥ 1 (до 120 с).
6. `docker compose -p wf-srv -f compose.yaml -f compose.ci.yaml down -v` — только свой проект.

Встроенный режим (как на стенде) — те же шаги с `-f compose.yaml -f compose.ci.yaml -f compose.ci-embed.yaml`:
сервиса `worker` нет, `воркер запущен` (с `"component":"worker"`) — в журнале `api`.
