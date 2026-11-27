# Свой сервер — `docker compose`

Тот же образ бэкенда, что на dev-стенде Render (`backend/Dockerfile`), но фон — отдельным сервисом `worker`. Переход со
стенда — правкой конфигурации, без правок кода (спека `docs/superpowers/specs/2026-11-16-embedded-worker-design.md` §5).

| Сервис | Что делает |
| --- | --- |
| `migrate` | `migrate up`: миграции и права ролей — разово; остальные стартуют после его успешного выхода |
| `api` | публичный API, `:8080` внутри сети; `API_EMBED_WORKER=false` задан в `compose.yaml` явно |
| `admin-api` | API сотрудников и собранная админка (`ADMIN_STATIC_DIR=/app/admin`), `:8081` внутри сети |
| `worker` | River, relay outbox, периодика; журнал старта — `воркер запущен` |
| `storage` | хранилище файлов Garage (S3), том `storage_data`; порты только внутри сети `wf` (раздел «Хранилище файлов») |
| `storage-init` | разовая настройка хранилища (`storage/init.sh`): бакеты, ключи процессов, права; процессы стартуют после его успешного выхода |
| `caddy` | TLS (ACME) и маршрут: `WF_API_DOMAIN` → `api`, `WF_ADMIN_DOMAIN` → `admin-api`, `WF_MEDIA_DOMAIN` и `WF_STORAGE_DOMAIN` → `storage`; наружу — только 80/443 |

Файлы: `compose.yaml` (прод), `Caddyfile`, `.env.example` (переменные compose), `<сервис>.env.example` (переменные
бинарников), `storage.env.example` (секреты Garage), `storage/garage.toml` и `storage/init.sh` (хранилище),
`compose.ci.yaml` и `compose.ci-embed.yaml` — только проверка (CI и локально).

## Встроенный режим — только стенд

`API_EMBED_WORKER=true` (фон внутри api) — режим бесплатного стенда Render, где отдельного воркера нет. **На сервере
запрещён:** `compose.yaml` держит `"false"` литералом и перекрывает им `api.env`; фон исполняет сервис `worker`. Стражи —
`TestServerComposeNoEmbeddedWorker` (`backend/internal/platform/config`) и job `server` в `.github/workflows/image.yml`.

## Подготовка сервера

1. Docker Engine с плагином Compose v2.
2. DNS: записи A доменов API, админки, медиа и хранилища — на адрес сервера (домен медиа может смотреть на CDN —
   раздел «Хранилище файлов»). AAAA — только с IPv6 в сети `wf` (раздел «Клиентский IP»).
3. Открыты входящие 80/tcp (ACME HTTP-01 и редирект на HTTPS), 443/tcp и 443/udp (HTTP/3). Остальные порты наружу не
   нужны: `api`, `admin-api`, `worker` и база (если на сервере) не публикуются.
4. Каталог `deploy/server` из того же коммита, что образ.

## Переменные

- `.env` ← `.env.example`: `WF_IMAGE` (образ с тегом релиза), `WF_API_DOMAIN`, `WF_ADMIN_DOMAIN`, `WF_ACME_EMAIL`,
  `WF_MEDIA_DOMAIN`, `WF_STORAGE_DOMAIN`, `WF_WEB_ORIGIN` (раздел «Хранилище файлов»).
- `storage.env` ← `storage.env.example`: `GARAGE_RPC_SECRET`, `GARAGE_ADMIN_TOKEN` — только сервисам `storage` и
  `storage-init`.
- `migrate.env`, `api.env`, `admin-api.env`, `worker.env` ← свои `*.env.example`; описание ключей —
  `backend/.env.example`. Рабочие `*.env` под `.gitignore` (`deploy/server/*.env`): в них секреты.
- `api.blob.env`, `admin-api.blob.env`, `worker.blob.env` ← свои `*.blob.env.example`: ключи хранилища процесса
  (`*_BLOB_*`, `*_MEDIA_PUBLIC_BASE_URL`). Отдельный файл, потому что его же получает `storage-init` (сторонний образ
  curl): ему видны только ключи хранилища, а не база, кольца и SMTP процесса.
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

**Образ.** Ни один workflow пока не публикует образ в реестр (Render собирает его сам из репозитория; публикация —
задача плана выкатки сервера). До её появления образ собирается на сервере из того же коммита, что `deploy/server`:

```sh
git checkout <sha>                                        # репозиторий на сервере — коммит релиза
docker build -f backend/Dockerfile -t wf-backend:<sha> .  # из корня репо (долго)
```

и в `.env` — `WF_IMAGE=wf-backend:<sha>`. С реестром — `WF_IMAGE=<реестр>/wf-backend:<тег-релиза>` (реестр не выбран)
и `docker compose pull`.

**Обновление** — по шагам, из `deploy/server`:

1. `git checkout <sha>` нового релиза — `deploy/server` (compose, Caddyfile, шаблоны) того же коммита, что образ;
   сверить свои `*.env` с изменившимися `*.env.example`.
2. Собрать образ `wf-backend:<sha>` (выше) или опубликовать его в реестр.
3. Сменить тег `WF_IMAGE` в `.env` на новый — `pull` с прежним тегом ничего не меняет.
4. Подъём:

```sh
docker compose pull   # только с реестром
docker compose up -d
```

`migrate` идёт первым; `api`, `admin-api`, `worker` стартуют после его успешного выхода, `caddy` — когда `api` и
`admin-api` здоровы. Миграция упала — сервисы не обновляются, журнал — `docker compose logs migrate`. Так же они
ждут `storage-init`: **хранилище лежит** (испорчен том, полный диск, admin API не отвечает), а выкатить надо
исправление не про картинки — в обход зависимостей, если ключи процессов уже заведены в Garage прошлым успешным
`storage-init`:

```sh
docker compose up --no-deps migrate                   # без -d: ждёт выхода, код 0 — миграции применены
docker compose up -d --no-deps api admin-api worker caddy
```

Картинки до починки хранилища не работают (загрузки и обработка — ошибки с повтором); после починки — обычный
`docker compose up -d`. Остановка: окно
`stop_grace_period: 30s` — бюджет процессов 25 с (`config.StopBudget`), задачи в полёте возвращаются в повтор.

Проверка здоровья — `/healthz` (процесс жив, базу не трогает), не `/readyz`: частые пробы не должны будить Neon.
`/readyz` — ручная проверка с базой после выкатки:
`curl -fsS https://<api-домен>/readyz`, `curl -fsS https://<admin-домен>/readyz`.

## Переход со стенда

Спека §5 (I8), по шагам:

1. Сервер — на **том же коммите**, что стенд (`WF_IMAGE` — образ этого коммита, раздел «Выкатка и обновления»).
2. Ждущие задачи `geo.import` на стенде — отменить (кнопка «Отменить» в админке) до старта серверного воркера.
3. Встроенный воркер стенда выключить **коммитом в `render.yaml`** (`API_EMBED_WORKER: 'false'` у `wf-api`; страж
   `TestRenderYAMLEmbeddedWorker` — под этот коммит), а не в панели Render: синхронизация Blueprint вернёт значение из
   файла со следующим коммитом в `main` и выкатит на Render новый релиз, пока сервер на старом, — два воркера разных
   релизов на одной схеме (повторы и «мёртвые» задачи). Нужно выключить раньше коммита — сначала выключить автодеплой
   `wf-api` в панели. Выкатка этого коммита на Render фона не несёт, поэтому сервер на шаге 4 может быть на нём же.
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

## Хранилище файлов

Картинки и файлы (спека `docs/superpowers/specs/2026-11-18-media-design.md` §6) — S3-совместимое хранилище **Garage**
(`dxflrs/garage:v2.6.0`, один узел, том `storage_data`). Лицензия Garage — **AGPLv3**: он работает отдельным сервисом
из официального образа без изменений, наш код с ним не линкуется (план media R1); вопрос лицензии — к юристу до
запуска в прод. Порты Garage наружу не публикуются: S3 (`3900`), сайт публичного бакета (`3902`) и admin API (`3903`)
— только в сети `wf`.

### Ключи и права

Бакеты: `wf-in` (входящие загрузки), `wf-pub` (публичные варианты), `wf-priv` (закрытые). Имена зашиты в `Caddyfile`,
`storage/init.sh` и шаблонах `*.env.example` — в `*.env` их не менять (`storage-init` откажет). Права (R2; `write`
включает удаление, `read` — листинг; `owner` — ни у кого):

| Ключ процесса | `wf-in` | `wf-pub` | `wf-priv` |
| --- | --- | --- | --- |
| api (`API_BLOB_S3_*`) | read, write | — | read |
| admin-api (`ADMIN_BLOB_S3_*`) | read, write | — | read |
| worker (`WORKER_BLOB_S3_*`) | read, write | read, write | read, write |

Ключи процессов генерирует оператор — у каждого процесса своя пара, в формате ключей Garage:

```sh
printf 'GK%s\n' "$(openssl rand -hex 12)"   # *_BLOB_S3_ACCESS_KEY
openssl rand -hex 32                        # *_BLOB_S3_SECRET_KEY
```

`storage-init` импортирует их в Garage (`ImportKey` admin API) — единственный источник ключей остаётся в
`<сервис>.blob.env` процессов. Документация Garage предупреждает не выдумывать идентификаторы ключей: случайные 12 байт после `GK` — тот
же формат, что генерирует сам Garage. Секреты Garage — в `storage.env`: `GARAGE_RPC_SECRET` (`openssl rand -hex 32`),
`GARAGE_ADMIN_TOKEN` (`openssl rand -base64 32`; токен admin API есть только у `storage` и `storage-init`). Lifecycle
и CORS `storage-init` ставит через admin API — ключа с правом `owner` нет ни у кого.

**Ротация ключа процесса:** новая пара в его `<сервис>.blob.env` → `docker compose up -d` (`storage-init` импортирует
новый ключ, процесс перезапускается) → **подождать не меньше 15 минут** после перезапуска всех процессов: выданные
старым ключом билеты загрузки живут 15 минут, подписанные ссылки на закрытое — 5 минут (`uploadTTL`,
`privateLinkTTL` в `backend/internal/media/service.go`), и после раннего удаления загрузка в полёте получит 403 →
старый удалить: `docker compose exec storage /garage key delete --yes <старый GK…>`.
Удалённый идентификатор повторно не импортируется — каждый раз новая пара. Сменить только секрет при прежнем
`*_ACCESS_KEY` нельзя: `storage-init` откажет «уже есть с другим секретом».

### Первый старт

1. `storage.env` и ключи в `api.blob.env`, `admin-api.blob.env`, `worker.blob.env` (выше); адреса там же:
   `*_BLOB_S3_PUBLIC_ENDPOINT=https://<WF_STORAGE_DOMAIN>`, `*_MEDIA_PUBLIC_BASE_URL=https://<WF_MEDIA_DOMAIN>/`;
   внутренний `*_BLOB_S3_ENDPOINT=http://storage:3900` — из шаблона.
2. В `.env`: `WF_MEDIA_DOMAIN`, `WF_STORAGE_DOMAIN`, `WF_WEB_ORIGIN` (origin веба без «/» в конце — CORS загрузки).
3. `docker compose up -d`: `storage` → `storage-init` (слой узла, бакеты, ключи, права, сайт `wf-pub`, lifecycle `wf-in`
   1 сутки, CORS `wf-in`) → `api`, `admin-api`, `worker`. Журнал — `docker compose logs storage-init`, последняя строка
   `готово`. Упал (плейсхолдер вместо ключа, чужой секрет, имя бакета, origin) — процессы не стартуют, причина — в
   журнале; исправить `*.env` и повторить `up -d`.

`storage-init` идёт при каждом `up` и безопасен повторно: недостающее создаёт, права приводит к таблице выше
(лишние снимает). Ёмкость узла в слое — условная (на одном узле не ограничивает), задаётся только при первом старте.

**Воркер не должен стоять дольше суток.** Жизненный цикл `wf-in` (1 сутки) удаляет входящие файлы, а обрабатывает их
только воркер: всё, что загрузили за время простоя дольше суток, после его подъёма уйдёт в `rejected
(processing_failed)` (задача не находит файл, повторы, затем отказ) — игрок увидит «не принято» и загрузит заново.
Поэтому тревога на остановленный `worker` — та же, что на диск. Простой затягивается — временно поднять срок
(`"Days":1` в правиле `wf-in-1d`, `storage/init.sh`) и применить `docker compose run --rm storage-init`; после
подъёма воркера и разбора очереди вернуть 1 и повторить (каждый `up` ставит срок из `init.sh`).

### Домены

| Домен | Что пропускает Caddy | CDN |
| --- | --- | --- |
| `WF_MEDIA_DOMAIN` (`*_MEDIA_PUBLIC_BASE_URL`) | только `GET`/`HEAD` → сайт бакета `wf-pub`; корень, `/wf-in…`, `/wf-priv…` — 404; прочие методы — 405; `X-Content-Type-Options: nosniff` | можно (держит копии публичного) |
| `WF_STORAGE_DOMAIN` (`*_BLOB_S3_PUBLIC_ENDPOINT`) | `POST`/`OPTIONS /wf-in` (подписанная загрузка и предзапрос CORS), `GET`/`HEAD /wf-priv/*` с подписью в query; прочее — 404 | **нельзя**: подпись SigV4 включает Host и путь, через CDN пошло бы закрытое |

`Cache-Control` и `Content-Type` публичных вариантов — метаданные объекта (пишет воркер), Garage отдаёт их сам.
Проверка обоих доменов — job `server` в `.github/workflows/image.yml` (функция `storage_check`).

### Диск и память

- **Диск.** Garage на одном узле без реплик: заполненный том — отказ загрузок и публикации. Тревога при заполнении
  > 80 % (спека §3.11) — cron хоста, например раз в 10 минут:
  `df --output=pcent "$(docker volume inspect -f '{{.Mountpoint}}' wf_storage_data)" | tail -n 1` → число > 80 —
  уведомление оператору (расширить диск, проверить чистку `media`). В наблюдаемость — когда она появится.
- **Память воркера.** Обработка картинки 40 Мп берёт до ≈ 0,6 ГиБ кучи на исполнителя у JPEG (бюджет декодера — не
  больше 8 Б/пикс. предела цели, тяжёлые прогрессивные JPEG отклоняются до декодирования) и до ≈ 0,75–0,9 ГиБ у PNG
  16 бит; худший принятый вход — PNG 16 бит Adam7, ≈ 0,9 ГиБ (замер финальной волны плана media, с мусором до сборки).
  Перепроверка финального ревью, 27.11: два таких файла разом в двух исполнителях — пик памяти рантайма ≈ 1,84 ГиБ
  на голом процессе, сверху River, пулы pgx, relay и прочие очереди. Поэтому при `WORKER_QUEUE_MEDIA=2` (по
  умолчанию) воркеру нужно **≥ 2,5 ГиБ**; меньше — `WORKER_QUEUE_MEDIA=1`.

### Бэкап тома

Реплик нет — копия тома единственная страховка. Копии хранилища и базы согласуются по времени (m10): **сначала база,
потом хранилище**. Хранилище новее базы — лишние файлы без строк (их уберёт сверка); хранилище старее базы — строки
`ready` без файлов (битые картинки), этого избегаем. Порядок не закрывает удаления **между** копией базы и снимком
хранилища (снятие или удаление аккаунта в эти минуты): после восстановления их строки `ready` останутся без файлов.
Их покажет сверка `worker media reconcile` (раздел «Восстановление», шаг 5) как «строка без файла» — сама она их не
правит, разбирает оператор. Cron хоста, например раз в сутки, из `deploy/server`:

```sh
# 1. база: Neon — запомнить время T (точка восстановления по времени); база на сервере — pg_dump (раздел «База»)
# 2. согласованный снимок метаданных Garage (LMDB): meta/snapshots/<время UTC>, хранятся два последних
docker compose exec -T storage /garage meta snapshot
# 3. архив тома без живого файла метаданных — и вынести копию с сервера
tar -C "$(docker volume inspect -f '{{.Mountpoint}}' wf_storage_data)" --exclude=./meta/db.lmdb \
  -czf /srv/backup/storage-$(date -u +%Y-%m-%dT%H%MZ).tgz .
```

Снимок метаданных Garage делает и сам каждые 6 часов (`metadata_auto_snapshot_interval` в `storage/garage.toml`);
`metadata_fsync` и `data_fsync` включены — без них LMDB на одном узле портится при нечистом выключении.

### Восстановление

1. `docker compose stop` (тома не трогать). Новый сервер — `docker compose create storage` (создаст том).
2. Распаковать архив в том и поставить снимок метаданных на место базы Garage:

```sh
v=$(docker volume inspect -f '{{.Mountpoint}}' wf_storage_data)
tar -C "$v" -xzf storage-<время>.tgz
mv "$v/meta/db.lmdb" "$v/meta/db.lmdb.bak" 2>/dev/null || true
cp -r "$v/meta/snapshots/<последний>" "$v/meta/db.lmdb"
```

3. `docker compose up -d storage`, затем `docker compose exec storage /garage repair -a --yes tables` (доки Garage,
   «Recovering from failures»).
4. База — на момент **не позже** копии хранилища (Neon — восстановление ветки на время T шага 1 бэкапа).
5. `docker compose up -d`, затем сверка: `docker compose run --rm --no-deps worker worker media reconcile` — отчёт
   «строка без файла / файл без строки» (только id и ключи); с `--fix` удаляются файлы без строк. Строки `ready` без
   файлов команда не правит — разбирает оператор.

## Проверка локально

Docker Desktop; проект — только свой `-p wf-srv`, команды — из `deploy/server`. Образ:
`docker build -f backend/Dockerfile -t wf-backend .` из корня репо (долго).

1. Рабочие `*.env` и `.env` из шаблонов со значениями CI — как шаг «env из шаблонов» job `server` в
   `.github/workflows/image.yml` (строки к `db` под ролями из `deploy/dev/initdb/20_wf.sql`, одно кольцо identity у api
   и worker, SMTP `127.0.0.1:1` с TLS `none`, `WF_IMAGE=wf-backend`, домены `api.wf.test`, `admin.wf.test`,
   `media.wf.test`, `storage.wf.test`, одноразовые ключи хранилища в `*.blob.env` и `storage.env`); в `.env` — свободные порты
   `WF_HTTP_PORT`, `WF_HTTPS_PORT` (80/443 хоста могут быть заняты).
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
6. Хранилище — тело функции `storage_check` job `server` (порт `WF_HTTPS_PORT` в `--resolve`, адресах и в `host:`
   подписи GET): подписанный `POST` в `wf-in` через `storage.wf.test` с CORS на ответе, копии в `wf-pub` и `wf-priv`
   ключом воркера, `GET` через `media.wf.test` с `nosniff` и `Cache-Control`, подписанный `GET` закрытого через
   `storage.wf.test` (и отказ с подделанной подписью), запреты (405/404) обоих доменов, предзапрос CORS, повторный
   `storage-init`. В Git Bash —
   `-o NUL` вместо `-o /dev/null` (curl Windows при `MSYS_NO_PATHCONV=1` не открывает `/dev/null`).
7. `docker compose -p wf-srv -f compose.yaml -f compose.ci.yaml down -v` — только свой проект; удалить рабочие
   `*.env`, `.env`, `root.crt`, `hello.txt`, `got.txt`.

Встроенный режим (как на стенде) — те же шаги с `-f compose.yaml -f compose.ci.yaml -f compose.ci-embed.yaml`:
сервиса `worker` нет, `воркер запущен` (с `"component":"worker"`) — в журнале `api`.
