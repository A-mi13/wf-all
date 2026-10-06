# Спека среза «Сотрудники и админка geo»

Дата: 06.10.2026. Статус: на ревью (редакция 2 — по ревью редакции 1: права и подтверждения перенесены в
платформу, всё о сотрудниках исполняет admin-api, код приглашения шифруется, блокировка только по второму
фактору, ротация токена; журнал решений — `.superpowers/sdd/2026-10-06-staff-a/decisions-r2.md`).
Родительская спека: `docs/superpowers/specs/2026-10-01-backend-architecture-design.md` (дальше — «спека
бэкенда»), §10 и §13 п. 2. Спека geo: `docs/superpowers/specs/2026-10-02-geo-design.md` (дальше — «спека
geo»), §5 и §11. Дизайн: `design/screens/07-Админка.dc.html`, `design/screens/08-Админка-в-телефоне.dc.html`.

## 1. Зачем и что входит

Срез делает админку живой. Сотрудник входит в неё по-настоящему: почта, пароль и обязательный второй
фактор. Владелец приглашает сотрудников, права выдаются по ролевой модели, опасное подтверждает второй
человек. Админ заводит и открывает города, запускает импорт GeoNames, правит страны, версии приложения
и флаги функций. Всё видно в журнале действий.

Решения пользователя (05–06.10.2026):

1. Бэкенд и админка — одной спекой, вертикальным срезом: контракт → бэкенд ∥ экраны → общее ревью.
2. **Сотрудник — отдельная учётка** (`staff_*`), не связанная с `users`. Это меняет §4.5 и §10 спеки
   бэкенда: роли `admin` и `city_moderator` уходят из `role_assignments` (§3.2).
3. Вход: первый раз — почта, новый пароль и код из письма-приглашения. Сразу после этого обязательная
   настройка 2FA. Дальше всегда — почта, пароль и TOTP (или резервный код).
4. Повторная отправка кода приглашения — сразу. Для 2FA — сохранение ключей доступа: секрет текстом
   рядом с QR, резервные коды «Скопировать», «Скачать .txt» и «Я сохранил».
5. **Четыре роли сотрудников** (владелец, админ, модератор, поддержка) и матрица прав. Ручки проверяют
   право, а не роль. Ролевая модель всего продукта выносится в отдельный документ
   `docs/05-ролевая-модель.md` и проходит **отдельное ревью до кода** (§12).
6. Сначала всё работает локально в живом режиме, развёртывание на Render — последний этап (§12, план C).
7. Ненарисованные экраны дорисовываются в коде в стилистике макетов 07/08.
8. В срез входят раздел «Подтверждения» (правило двух рук), журнал действий и флаги функций.
9. Объём не урезается, временных обходов нет (память: «без срезов»).

Не входит: разделы будущих модулей (очереди, дашборд, пользователи и санкции, глобальный поиск ⌘K).
Они придут со своими модулями на готовый каркас (§9).

## 2. Ролевая модель

Полная модель — в `docs/05-ролевая-модель.md`. Документ пишется первой задачей плана A, а таблица
матрицы в нём генерируется из кода (§2.4). Здесь — принципы и права, нужные срезу.

### 2.1. Два мира

| | Игроки (продукт) | Сотрудники (админка) |
|---|---|---|
| Учётка | `users` | `staff_accounts` (рабочая почта) |
| Модуль | `identity`; права на команду — `teams` | `staff` |
| API | `api` (JWT) | `admin-api` (cookie-сессия) |
| Роль БД | `api` | `admin`; у `api` доступа к `staff_*` нет |

Один человек может быть и игроком, и сотрудником — это две разные учётки, никак не связанные. Так
обязанности разделены намеренно, а взлом публичного API не даёт входа в админку (спека бэкенда §10).

### 2.2. Роли игроков: одно место хранения на роль

| Что | Где | Примечание |
|---|---|---|
| Гость | нет сессии | не роль |
| Игрок | любой активный `users` | не хранится |
| Допуск к капитанству в городе | `role_assignments(captain, city)` | выдаётся одобрением заявки; «испытательный / полный» — свойство допуска |
| Капитан и вице команды | `teams.captain_id`, `vice_captain_id` | права на команду проверяет `teams` |
| Организатор и участник матча | данные матча | права проверяет `matches` |
| Ограничения | `user_restrictions` | запрет, а не роль |
| Платные возможности | права `economy` | не роль |

### 2.3. Роли сотрудников: одна роль на человека

| Роль | Код | Скоуп | Назначение |
|---|---|---|---|
| Владелец | `owner` | глобально | всё, включая выдачу прав; последнего владельца снять нельзя |
| Админ | `admin` | глобально | операционное управление: города, флаги, версии, модерация |
| Модератор | `moderator` | список городов | очереди, жалобы, ограничения до 30 дней — в своих городах |
| Поддержка | `support` | глобально | только чтение |

У сотрудника ровно одна роль. Список городов бывает только у модератора и не может быть пустым.

### 2.4. Права

- Ключ права — `<модуль>.<ресурс>.<действие>`. Значение в матрице — `allow` (да), `scoped` (только города
  модератора), `dual` (вдвоём, §4.8), `deny`. Право в каталоге помечено `Mutating`, если меняет данные.
- **Каталог и матрица — платформа:** `backend/internal/platform/perm` (`perm.Role`, `Catalog`, `Matrix`,
  `Grants(role)`). Модуль, которому нужны свои права, добавляет строки в этот файл своим коммитом (как
  ключи флагов). Так права видят и конвейер, и любой модуль, а границы слоёв archtest не нарушаются:
  модуль `staff` — слой 0, geo его не импортирует.
- Права сотрудника считаются при загрузке сессии и лежат в `auth.Staff` (платформа):
  `auth.StaffFrom(ctx).Can(perm, cityID)` — для модулей без импорта `staff`.
- `./task gen` генерирует из матрицы таблицу в `docs/05-ролевая-модель.md` (между маркерами) и
  `contracts/openapi/admin/permissions.json`. `gen:check` ловит расхождение. Экран «Роли и доступ» берёт
  матрицу из `GET /v1/staff/roles`, подписи прав — из локалей админки (`permissions.<ключ>`).
- Каждая операция контракта admin объявляет право расширением `x-permission`. Значения:
  - ключ каталога (не черновой) — конвейер проверяет его сам; если в пути есть `cityId`, `scoped`
    сверяется с городами модератора;
  - `anonymous` ⇔ `security: []` — без сессии (вход, принятие приглашения, повторная отправка);
  - `session` — сессия в любой стадии, стадию проверяет сценарий (второй фактор входа, разблокировка,
    выход, `GET /v1/staff/me`, настройка 2FA);
  - `self` — активная сессия, своё («Мой доступ», список подтверждений);
  - `handler` с обязательным `x-permission-any: [<ключи>]` — право зависит от тела или цели (приглашение,
    смена роли, выключение, сброс доступа, решение подтверждения). Конвейер требует, чтобы хотя бы одно
    из перечисленных было не `deny`, точную проверку делает сценарий.
- Отказ — `403 auth.forbidden`, один код на всю админку. Коды `geo.forbidden` и `platform.forbidden`
  из спеки geo §5.2 не вводятся (§13).

### 2.5. Права среза

| Право | Влад. | Админ | Модер. | Подд. |
|---|---|---|---|---|
| `staff.roles.read` — матрица ролей | allow | allow | allow | allow |
| `staff.accounts.read` — список сотрудников | allow | allow | deny | deny |
| `staff.roles.grant_elevated` — выдать `owner`/`admin` | dual | deny | deny | deny |
| `staff.roles.grant` — выдать `moderator`/`support`, добавить города | dual | dual | deny | deny |
| `staff.roles.revoke_owner` | dual | deny | deny | deny |
| `staff.roles.revoke_admin` | allow | deny | deny | deny |
| `staff.roles.revoke` — `moderator`/`support`, убрать города | allow | allow | deny | deny |
| `staff.access.reset_elevated` — сброс доступа `owner`/`admin` | dual | deny | deny | deny |
| `staff.access.reset` — сброс доступа `moderator`/`support` | dual | dual | deny | deny |
| `staff.invites.resend` | allow | allow | deny | deny |
| `staff.sessions.revoke` — чужие сессии (`admin` — только не-elevated) | allow | allow | deny | deny |
| `audit.log.read` | allow | allow | scoped | allow |
| `geo.places.search` | allow | allow | deny | deny |
| `geo.cities.read` | allow | allow | scoped | allow |
| `geo.cities.activate` | allow | allow | deny | deny |
| `geo.cities.manage` — статус, slug, привязка `geoname_id` | allow | allow | deny | deny |
| `geo.cities.rename` — названия по локалям | allow | allow | scoped | deny |
| `geo.districts.manage` | allow | allow | scoped | deny |
| `geo.countries.read` | allow | allow | allow | allow |
| `geo.countries.manage` | allow | allow | deny | deny |
| `geo.imports.read` | allow | allow | deny | allow |
| `geo.imports.run` | allow | allow | deny | deny |
| `platform.versions.read` | allow | allow | allow | allow |
| `platform.versions.manage` | allow | allow | deny | deny |
| `platform.flags.read` | allow | allow | allow | allow |
| `platform.flags.manage` | allow | allow | deny | deny |

Строки будущих модулей — по макету 07, раздел «Роли и доступ»: капитаны, площадки, жалобы, ограничения,
вечная блокировка (`dual`), аннулирование протокола (`dual`), персональные данные (с аудитом чтения),
экономика. В `docs/05` они идут черновыми с пометкой «уточнит спека модуля». Удалять журнал не может
никто: триггер `audit_log_is_append_only` и отсутствие права.

### 2.6. Правила назначения

- Владелец и админ назначаются только владельцем, вдвоём. Модератор и поддержка — владельцем или
  админом, вдвоём.
- Смена роли и городов (`PUT …/role`):
  - старая или новая роль `owner`/`admin` → `staff.roles.grant_elevated` (вдвоём);
  - иначе роль меняется или добавляется хотя бы один город → `staff.roles.grant` (вдвоём);
  - иначе (только снятие городов, остаётся хотя бы один) → `staff.roles.revoke`, сразу;
  - пустой список городов у модератора → `400 validation.failed` (`body.city_ids`, `min_items`).
- Снять роль = выключить учётку (`status = disabled`). Исполняется сразу, кроме снятия владельца.
  Гасит все сессии сотрудника. Последнего активного владельца снять нельзя (`409 staff.last_owner`) —
  проверка под `pg_advisory_xact_lock` во всех путях снятия, выключения и сброса владельца.
- Выключение, сброс доступа и смена роли сотрудника гасят его ожидающие запросы на подтверждение.
- Никакое действие над собой через управление (`409 staff.self_action`): свои сессии и пароль — через
  «Мой доступ».
- Повторное приглашение на почту выключенной учётки возвращает ту же учётку через сброс доступа. История
  и аудит сохраняются.
- Первый владелец создаётся командой оператора (§4.2). Тупики, где второго человека нет (единственный
  владелец потерял 2FA, снять одного из двух владельцев), решает оператор подкомандами admin-api (§4.2).

## 3. Модель данных

Миграция плана A — `0020_staff.sql`: таблицы `staff_*`, таблица платформы `approvals`, правки
`audit_log` и `role_assignments`. Миграции плана B — свои. Все таблицы модуля — с префиксом `staff_`,
их закрывает правило `staff\_%` в `grants.sql` (у `admin` полный DML, у остальных ничего).

### 3.1. Таблицы `staff`

```sql
CREATE TYPE staff_role AS ENUM ('owner', 'admin', 'moderator', 'support');
CREATE TYPE staff_status AS ENUM ('invited', 'active', 'disabled');
CREATE TYPE staff_session_stage AS ENUM ('mfa_pending', 'setup', 'active');
CREATE TYPE staff_invite_purpose AS ENUM ('invite', 'access_reset');
```

- **`staff_accounts`**:
  - `id` uuid PK, `email` citext UNIQUE, `display_name` text (1–80);
  - `locale` text (`ru|en`), `status` staff_status;
  - `password_hash` text NULL — NULL до принятия приглашения и после сброса доступа;
  - `mfa_failures` int (подряд, обнуляется успехом второго фактора), `locked_until` timestamptz NULL,
    `lock_notified_at` timestamptz NULL (письмо о блокировке — не чаще раза в час), `last_login_at`;
  - `created_at`, `created_by` uuid NULL — NULL у созданного оператором;
  - `version` bigint — растёт и при смене роли или городов (сверка подтверждений).
- **`staff_roles`**: `staff_id` PK → accounts, `role` staff_role, `granted_by` uuid NULL, `granted_at`.
- **`staff_role_cities`**: `(staff_id, city_id)` PK, `city_id` → `cities` (FK только в БД, модуль geo не
  импортируется). Отложенный триггер: у `moderator` хотя бы один город, у остальных ни одного.
- **`staff_mfa`**:
  - `staff_id` PK, `secret_enc` bytea, `key_id` text;
  - `confirmed_at` NULL — NULL пока идёт настройка;
  - `last_used_step` bigint.
- **`staff_recovery_codes`**: `(staff_id, code_mac)` PK, `key_id` text, `used_at` NULL. Ровно 10 штук на
  поколение, перевыпуск удаляет старые.
- **`staff_invites`**:
  - `id`, `staff_id`, `purpose`;
  - `code_enc` bytea NULL, `code_key_id` text NULL — код шифруется (письмо собирается по id, §4.12),
    стирается после погашения или 5 неверных вводов;
  - `expires_at` (24 ч), `code_attempts`, `sends`, `last_sent_at`;
  - `consumed_at` (на `recovery-codes/ack`), `revoked_at`.
  - Живое приглашение у учётки одно (частичный UNIQUE `staff_invites_live_key` по `staff_id` WHERE
    `consumed_at IS NULL AND revoked_at IS NULL`).
- **`staff_sessions`**:
  - `id`, `staff_id`, `token_hash` UNIQUE, `stage`;
  - `created_at`, `last_seen_at`, `mfa_at`, `expires_at` (абсолютный срок);
  - `ip` inet, `user_agent`;
  - `revoked_at`, `revoked_reason`.

### 3.2. Таблицы и правки платформы

- **`approvals`** (платформа, `platform/approvals`, §4.8):
  - `id`, `action` text, `permission` text, `payload` jsonb (с полем `v`);
  - `target_id` uuid NULL, `scope_city_id` uuid NULL, `requested_by` uuid (сотрудник),
    `requested_by_label` text, `requested_at`, `expires_at` (24 ч);
  - `status` (`pending|executed|rejected|cancelled|expired|failed`), `decided_by` NULL,
    `decided_by_label` NULL, `decided_at`, `decision_comment`, `error` NULL.
  - Права: `admin` — DML, `api` и `worker` — ничего.
- **`role_assignments`** (identity). Новые перечисления: `role_name` → только `captain`, `role_scope` →
  только `city`. Старые значения удаляются: таблица пуста — проверка в миграции; сначала снимается
  CHECK `role_scope_id_present`, затем тип пересоздаётся, затем новый CHECK. Правило 4 `CLAUDE.md`
  уточняется: роли игроков — `role_assignments`, сотрудников — `staff_roles`.
- **`audit_log`** (платформа):
  - `actor_staff_id uuid NULL`, `actor_label text NULL` (снимок отображаемого имени на момент действия:
    имя сотрудника, `operator`, `system`), `scope_city_id uuid NULL` (город записи — для журнала
    модератора);
  - CHECK: `actor_user_id` и `actor_staff_id` не оба; без актора `actor_role` ∈
    `system|operator|anonymous` (анонимные события — неудачный вход, анонимная повторная отправка);
  - индексы `(actor_staff_id, created_at DESC)`, `(scope_city_id, created_at DESC)`.
- **`idempotency_keys`** не меняется: ключ — `(user_id = id субъекта, key)`. UUIDv7 игрока и сотрудника
  не совпадают; комментарий колонки — «id субъекта: игрок или сотрудник».
- **`feature_flags`** (план B): `version bigint`. Ключи флагов заводятся миграциями модулей, админ только
  переключает.

### 3.3. Права ролей БД (`grants.sql`)

- `worker` — **ничего** на `staff_*` и `approvals`: всё о сотрудниках исполняет admin-api (§4.10).
- `admin` — права на служебные таблицы River, как у воркера (исполнение задач очереди `staff`, §4.10).
- `admin`: `DELETE` на `city_slug_history` (план B; спека geo §3.8).
- `api`: к `staff_*` и `approvals` доступа нет, sequence-прав тоже (страж §11).

## 4. Модуль `staff` (бэкенд)

Код: `backend/internal/staff/` по раскладке модулей спеки бэкенда §3 (`internal/{app,domain,store}`,
`queries/`, `admin/` — хендлеры, `jobs/`). Модуль — слой 0: geo его не импортирует, он не импортирует
geo. archtest: модуль заносится в `modules.go` и `ownership.go` со всеми таблицами `staff_*`; `approvals`
— владелец `platform`.

### 4.1. Криптография и форматы

- Пароль: `platform/password` (argon2id, ≥10 символов, список распространённых, NFKC, потолок длины
  на входе) — остатки фундамента 3/3 закрываются здесь же.
- TOTP: RFC 6238, SHA-1, 6 цифр, шаг 30 с, допуск ±1 шаг, секрет 160 бит (base32). `last_used_step`
  запрещает повтор кода и кодов старше уже принятого.
- Ключи — `ADMIN_STAFF_SECRET_KEYS`, кольцо `keys.Ring` (первый — текущий, остальные расшифровывают).
  Ключи есть только у admin-api.
- Секрет TOTP и код приглашения шифруются AES-256-GCM (`Ring.Seal`, aad — id строки); `key_id` в строке
  говорит, каким ключом. Сверка кода — расшифровка и сравнение за постоянное время.
- Резервные коды — 10 штук по 10 символов base32 (`xxxxx-xxxxx`), хранится HMAC-SHA-256 ключом
  `key_id` строки (HKDF, метка `staff-recovery`).
- Токен сессии — `token.NewRefresh` (256 бит), в базе — SHA-256.

### 4.2. Оператор: первый владелец и тупики

Подкоманды бинарника admin-api (доступ к серверу — корень доверия, аудит `actor_role = operator`):

| Подкоманда | Что делает |
|---|---|
| `admin-api staff bootstrap --email --name` | новая учётка `owner` в статусе `invited` и приглашение; повтор для существующей почты — ошибка с подсказкой |
| `admin-api staff invite-resend --email` | новый код для учётки `invited` (истекло приглашение первого владельца) |
| `admin-api staff reset-access --email` | сброс доступа (единственный владелец потерял 2FA) |
| `admin-api staff disable --email` | выключение (снять одного из двух владельцев, когда подтвердить некому) |
| `admin-api staff rekey` | перешифровка текущим ключом (§4.11) |

- Письмо уходит задачей `staff.mail` (§4.10). Код печатается в консоль только с флагом `--print-code`:
  логи хостинга иначе сохранят его.
- Два владельца на старте заводятся двумя запусками — правилу двух рук нужен второй человек.

### 4.3. Приглашение и повторная отправка

1. `POST /v1/staff/invites {email, display_name, role, city_ids?}` (`x-permission: handler`, право —
   `staff.roles.grant` или `…grant_elevated` по роли) создаёт подтверждение и отвечает `202 {approval}`.
2. Исполнение подтверждения:
   - создаётся учётка `invited`, роль и города;
   - приглашение получает новый код;
   - письмо `staff.invite` со ссылкой `ADMIN_ORIGIN/accept` (без почты в адресе) и кодом.

   Почта уже есть: учётка выключена — сброс доступа той же учётки, иначе `409 staff.email_taken`.
3. Повторная отправка самим сотрудником: `POST /v1/staff/auth/invite/resend {email}` — анонимная,
   всегда `202`.
   - Новый код отправляется, только если есть живое приглашение, с последней отправки прошло 60 с,
     а за сутки отправок меньше 5.
   - Новый код заменяет старый, срок 24 ч не продлевается.
   - Клиент показывает отсчёт 60 с.
4. Повторная отправка админом: `POST /v1/staff/accounts/{id}/invite/resend` (`staff.invites.resend`).
   Выпускается новый код, срок 24 ч начинается заново. Пауза 60 с — `429 ratelimit.exceeded` с
   `Retry-After`.

### 4.4. Принятие приглашения (первый вход)

`POST /v1/staff/auth/invite/accept {email, code, password}`:

- Проверки — в таком порядке, без изменения состояния до успеха кода:
  - пароль проходит правила: `400 validation.failed`, поле `body.password`, код
    `too_short|common|too_long`;
  - приглашение живо и код верен. 5 неверных кодов стирают текущий код, приглашение остаётся живым —
    новый код повторной отправкой;
  - учётка `invited`.
- Ответ при любой ошибке кода, почты или срока одинаковый: `400 staff.invite_invalid`.
- Успех:
  - пароль сохраняется, неподтверждённый TOTP (если был) сбрасывается;
  - приглашение **не гасится** — до `recovery-codes/ack`. Потерянную сессию настройки можно
    возобновить повторным `accept` тем же или новым кодом;
  - старая сессия из cookie гасится, создаётся новая в стадии `setup` (cookie §5.2);
  - аудит `staff.invite_accepted`.

### 4.5. Настройка 2FA (стадия `setup`)

- `POST /v1/staff/me/mfa/setup` → `{secret, otpauth_uri}`. До подтверждения повторный вызов выпускает
  новый секрет. Секрет отдаётся только в стадии `setup`.
- `POST /v1/staff/me/mfa/confirm {code}` → `{recovery_codes: [10]}`. Код сверяется с неподтверждённым
  секретом, `confirmed_at` ставится, коды выдаются один раз.
- `POST /v1/staff/me/mfa/recovery-codes/ack`:
  - гасит приглашение, переводит учётку в `active`;
  - **выдаёт новый токен** сессии в стадии `active` (старый недействителен), ставит `mfa_at`;
  - аудит `staff.mfa_enabled`.

  Без `ack` сессия остаётся в `setup` — сервер сам не пускает дальше (страж §11).
- В стадии `setup` доступны только `GET /v1/staff/me`, три ручки выше и выход. Остальные →
  `403 auth.setup_required`. Ручка не для текущей стадии (например `mfa/setup` после подтверждения) →
  `409 staff.wrong_stage`.
- Ответы с секретами не сохраняются в `idempotency_keys` (§5.1, `x-idempotency: no-store`) и идут с
  `Cache-Control: no-store`.

### 4.6. Вход и блокировка

1. `POST /v1/staff/auth/login {email, password}`:
   - успех → сессия в стадии `mfa_pending` на 5 минут, ответ `200 {stage: "mfa_pending"}`; старая сессия
     из cookie гасится;
   - нет почты, неверный пароль, учётка не `active`, без 2FA или **закрыта** (`locked_until` в
     будущем — пароль тогда не проверяется) — один ответ `400 staff.invalid_credentials`. Время
     выравнивает `VerifyDummy`.
   - Неудачи пароля никого не блокируют и сессий не гасят. Их сдерживают лимиты: по IP (класс
     `staff_auth`) и по нормализованной почте (ключ `email:<hmac почты>`, 10 за 15 минут — одинаково и
     для несуществующих адресов). Превышение — `429 ratelimit.exceeded`.
2. `POST /v1/staff/auth/mfa {code | recovery_code}` (стадия `mfa_pending`):
   - успех → **новый токен** в стадии `active`, `mfa_at` и `last_login_at`, счётчик неудач обнуляется,
     аудит `staff.login`;
   - резервный код сгорает;
   - неудача → `400 staff.mfa_invalid {attempts_left}`.
3. Блокировка — только по второму фактору (пароль уже известен атакующему, это и есть сигнал):
   - неудачи второго фактора считаются подряд — шаг `mfa`, `unlock`, TOTP при смене пароля и перевыпуске
     кодов, неверный текущий пароль в `/me/password` (`400 staff.password_invalid`);
   - пятая неудача ставит `locked_until = now + 1 ч`, гасит все сессии сотрудника и отвечает
     `attempts_left = 0`;
   - письмо `staff.locked` уходит всем активным владельцам и админам, не чаще раза в час на учётку.
4. Запись неудачи (счётчик, блокировка, аудит) коммитится отдельной транзакцией до ответа 4xx.
5. Лимит по IP: класс `staff_auth` для всех `/v1/staff/auth/*`.

### 4.7. Сессия

- Абсолютный срок — 8 ч от входа. Истёк → `401 auth.unauthenticated`, сессия гасится (`expired`).
- Бездействие больше 20 минут или смена сети (IP вне прежнего префикса `/24` для IPv4 или `/48` для
  IPv6) → `401 auth.reauth_required`. Сессия жива, но доступны только `POST /v1/staff/auth/unlock {code
  | recovery_code}`, `GET /v1/staff/me` и выход. Успешный `unlock` выдаёт **новый токен**, ставит
  `mfa_at` и принимает новый IP.
- `last_seen_at` обновляется не чаще раза в минуту на сессию и **не обновляется** запросами с заголовком
  `X-WF-Background: 1` (фоновые опросы клиента), иначе открытая вкладка никогда не уйдёт в
  `reauth_required`.
- Выход: `POST /v1/staff/auth/logout` гасит сессию и стирает cookie.
- «Мой доступ»:
  - `GET /v1/staff/me/sessions` — устройство и браузер из UA, город по IP не определяем, IP
    маскируется до /24 или /48;
  - `DELETE /v1/staff/me/sessions/{id}` (чужая или несуществующая — `404 staff.session_not_found`);
  - `POST /v1/staff/me/sessions/revoke-others`.
- Пароль: `POST /v1/staff/me/password {current_password, code | recovery_code, new_password}` гасит
  другие сессии.
- Резервные коды: `GET /v1/staff/me/mfa` → `{confirmed_at, recovery_codes_left}`;
  `POST /v1/staff/me/mfa/recovery-codes {code}` (только TOTP) → новые 10, старые удаляются.
- Резервный код сгорает при любом использовании.
- Язык: `PATCH /v1/staff/me/locale {locale}` (`self`) — переключатель языка админки.

### 4.8. Подтверждение вдвоём (механизм платформы)

- Механизм — `backend/internal/platform/approvals`, таблица `approvals` (§3.2). Модули регистрируют
  исполнителей: `Registry.Register(action, permission string, ex Executor)`. Платформа открывает
  транзакцию подтверждения и вызывает исполнителя; модуль пишет только свои таблицы, аудит и события —
  атомарно.
- Операция с правом `dual` не исполняется, а создаёт запрос и отвечает `202 {approval}`.
- Ручки (тег `platform`, реализует `httpapi/admin.Server`):
  - `GET /v1/approvals?box=incoming|mine&status=&cursor=` (`self`) — входящие: запросы, которые
    сотрудник может подтвердить (право запроса не `deny`, город в скоупе, не инициатор, не цель);
  - `POST /v1/approvals/{id}/approve {comment?}` и `…/reject {comment}` (`handler`);
  - `POST /v1/approvals/{id}/cancel` (`self`, только инициатор).
- При `approve` проверяется:
  - подтверждающий не инициатор (`409 platform.approval_self`) и не цель запроса;
  - срок не истёк, запрос `pending` (`409 platform.approval_closed`);
  - инициатор всё ещё `active` и имеет право запроса;
  - версия цели (`version` в `payload`) — изменилась → статус `failed`, `409 platform.approval_stale`.
- Срок 24 ч. Исполнение само проверяет срок; просроченные помечает задача `staff.cleanup`.
- Каждый шаг пишется в аудит; событие `platform.approval_decided`. Письма о новых запросах — в этом срезе
  нет: счётчик в меню (§9.3).
- Регистрируются в этом срезе (модуль `staff`): `staff.invite`, `staff.role_change`, `staff.access_reset`,
  `staff.revoke_owner`. Подписи действий — в локалях админки (`approvals.actions.<action>`).

### 4.9. Управление сотрудниками

| Ручка | `x-permission` | Ответ |
|---|---|---|
| `GET /v1/staff/accounts?status=&role=&q=&cursor=` | `staff.accounts.read` | список: роль, города, статус, последний вход, «приглашение не принято / просрочено» |
| `GET /v1/staff/accounts/{id}` | `staff.accounts.read` | карточка |
| `POST /v1/staff/invites` | `handler`: grant, grant_elevated | `202` |
| `POST /v1/staff/accounts/{id}/invite/resend` | `staff.invites.resend` | `204` |
| `PUT /v1/staff/accounts/{id}/role {role, city_ids}` | `handler`: grant, grant_elevated, revoke | `200` или `202` по таблице §2.6 |
| `POST /v1/staff/accounts/{id}/disable {reason, comment?, confirm}` | `handler`: revoke, revoke_admin, revoke_owner | `200` или `202` |
| `POST /v1/staff/accounts/{id}/access-reset {reason, comment?, confirm}` | `handler`: access.reset, reset_elevated | `202` |
| `GET /v1/staff/accounts/{id}/sessions`, `POST …/sessions/revoke-all` | `staff.sessions.revoke` | |
| `GET /v1/staff/roles` | `staff.roles.read` | роли, права, матрица |
| `GET /v1/staff/me` | `session` | учётка, роль, города, **права**, стадия, сессия |

- `confirm` — почта цели, введённая руками (спека бэкенда §10, разрушающие действия). `reason` — из
  списка (`left_team`, `security`, `role_change`, `other`) плюс комментарий.
- Несуществующий город в `city_ids` → `422 staff.city_not_found`.
- Сброс доступа гасит пароль, 2FA, коды и сессии, ставит `status = invited` и выпускает приглашение
  `access_reset`. Самостоятельного «забыл пароль» у сотрудников нет — доступ восстанавливает только
  другой сотрудник или оператор (защита от захвата через почту).

### 4.10. Фоновые задачи — в admin-api

Всё о сотрудниках исполняет admin-api под ролью БД `admin`: у воркера нет ни ключей, ни прав на `staff_*`.
admin-api запускает свой River-клиент с исполнителями очереди `staff`; River-клиент воркера эту очередь не
берёт.

- `staff.mail` — письмо. Аргументы `{v, kind, ref, recipient}` — только id: `river_job` читает и `api`,
  поэтому ни адресов, ни кодов в аргументах нет. Составитель читает `staff_accounts` и расшифровывает
  `staff_invites.code_enc`. SMTP — `ADMIN_MAIL_*` (тот же `config.Mail`, что у воркера).
- `staff.cleanup` — тикер в процессе admin-api, раз в час (не River-задача: периодику River ставит
  только лидер; общий список периодики платформы — `platform/periodic.Jobs()`):
  - удаляет приглашения, погашенные или просроченные больше 30 дней назад (кроме последнего приглашения
    сотрудника — по нему повторная отправка берёт назначение);
  - удаляет сессии, истёкшие или отозванные больше 30 дней назад;
  - помечает просроченные подтверждения `expired`. Решённые подтверждения хранятся 180 дней.

### 4.11. Ротация ключей

`admin-api staff rekey` (синхронно, без River):

- перешифровывает текущим ключом TOTP-секреты и живые коды приглашений;
- печатает, сколько резервных кодов ещё ссылается на старые ключи. Их перехэшировать нельзя (исходника
  нет), поэтому старый ключ убирается из `ADMIN_STAFF_SECRET_KEYS`, только когда счёт = 0 — сотрудники
  перевыпустили коды. Описание — `docs/deploy-dev.md`.

### 4.12. Письма, события, аудит

- Письма (`backend/locales/{ru,en}.json`, задача `staff.mail`):
  - `staff.invite` — приглашение и код;
  - `staff.access_reset` — новый код после сброса;
  - `staff.locked` — владельцам и админам: кто, когда, IP маскирован (из `staff_accounts` и строки сессии,
    на которой закрылась учётка).

  Язык — `staff_accounts.locale`, с нормализацией `en-US` → `en` (остаток фундамента 3/3).
- События: `staff.invited`, `staff.role_granted`, `staff.role_revoked`, `staff.access_reset`,
  `staff.locked`. Агрегат — учётка сотрудника, `version`.
- Аудит:
  - каждое изменение в транзакции: кто (`actor_staff_id`, `actor_label`, роль на момент действия),
    действие, объект, было/стало, причина, IP, UA;
  - входы и неудачные входы. Неудачи пароля — `actor_role = anonymous`, почта маскирована; неудачи
    второго фактора — от учётки;
  - чтение журнала не аудируется, чтение персональных данных — аудируется (с модулями игроков).

## 5. Платформа admin-api

### 5.1. Конвейер

peer → IP-аллоулист → LimitBody → Timeout → маршруты → сессия сотрудника → CSRF → валидация по
контракту → проверка `x-permission` → rate limit → идемпотентность → хендлер.

- **IP-аллоулист**: `ADMIN_IP_ALLOWLIST` — список CIDR, пустой = выключен. По спеке бэкенда §8.4(в)
  включается только с закрытым origin — решение спеки деплоя, не dev-стенда. Отказ —
  `403 auth.ip_not_allowed`.
- **Сессия**: `auth.StaffSessions.LoadStaff(ctx, token, ip) (*auth.Staff, error)`. Шов из спеки geo §5.1
  меняется: загрузчик по токену cookie, Principal сотрудника — отдельный тип
  `auth.Staff{StaffID, SessionID, Role, CityIDs, Stage, Reauth, Grants}`. Положительного кэша нет —
  сессия сверяется с базой на каждый запрос. Отказ кэшируется на 5 с по хэшу токена и считается в
  IP-лимите класса `staff_auth` — перебор токенов упирается в лимит (остаток M-3).
- **CSRF**: для `POST/PUT/PATCH/DELETE` обязателен `Origin`, равный `ADMIN_ORIGIN`, и заголовок
  `X-WF-Admin: 1` (описан в контракте). Нарушение — `403 auth.csrf_failed`. Double-submit не нужен
  (спека бэкенда §10).
- **Права**: страж `x-permission` по таблице операций из бандла (§2.4). Стадия `setup` →
  `403 auth.setup_required`, `mfa_pending` → `401 auth.unauthenticated`, `Reauth` →
  `401 auth.reauth_required`.
- **Идемпотентность**: субъект берётся из `auth.SubjectFrom(ctx)` — игрок или сотрудник. Анонимные
  мутации (`x-anonymous-mutations` = операции с `security: []`) идут без ключа. Операции с
  `x-idempotency: no-store` хранят только хэш запроса: повтор с тем же ключом →
  `409 idempotency.replay_unavailable`.
- **Rate limit**: ключ «пользователь» для сотрудника — `staff:<id>`. Новый класс `staff_auth`.
- **Коды конвейера** — отдельный список кодов платформы admin (`httpx.AdminPlatformCodes`): в публичный
  контракт они не попадают.

### 5.2. Cookie и origin

- Cookie `__Host-wf_admin_session`: `HttpOnly; Secure; SameSite=Strict; Path=/`, без `Domain`, без
  `Max-Age` (сессионная; сроки — на сервере). Контракт меняет имя в `securitySchemes.session`.
- **Один origin без rewrite:** admin-api сам отдаёт собранную `apps/admin` из `ADMIN_STATIC_DIR`
  (пусто — не отдаёт). `/v1/*` — API, остальное — статика с fallback на `index.html`; заголовки CSP,
  `X-Content-Type-Options`, `Referrer-Policy`, `no-store` у `index.html`. В dev — прокси Vite
  (`WF_ADMIN_API_ORIGIN`), `ADMIN_ORIGIN=http://127.0.0.1:5173`. `VITE_ADMIN_API_URL` удаляется —
  другой origin запрещён.
- Новые переменные `ADMIN_ORIGIN`, `ADMIN_STAFF_SECRET_KEYS`, `ADMIN_IP_ALLOWLIST`, `ADMIN_COOKIE_SECURE`,
  `ADMIN_STATIC_DIR`, `ADMIN_MAIL_*` заносятся сразу в конфиг, `.env.example` и `.env` (память: ключи
  окружения). `./task setup` генерирует `ADMIN_STAFF_SECRET_KEYS` локально.

### 5.3. Журнал действий

`GET /v1/audit?actor_staff_id=&action_prefix=&object_type=&object_id=&from=&to=&cursor=` (тег
`platform`, право `audit.log.read`):

- модератор видит записи своих городов (`scope_city_id`) и свои собственные;
- «кто» — из снимка `actor_label` (журнал не обращается к таблицам `staff`);
- `before`/`after` уже маскированы при записи (`platform/audit/mask.go`); IP маскируется до /24 или /48;
- сортировка `created_at DESC, id`.

Новая функция `audit.List` в `platform/audit`. `scope_city_id` проставляют модули: geo — город
операции, staff — NULL.

### 5.4. Остатки фундамента, закрываемые срезом

- Principal кладётся в контекст (`auth.With` / `auth.WithStaff`), иначе идемпотентность молча выключена.
- Отказ сессии считается в лимите (M-3).
- Потолки параметров argon2 из хэша и минимум соли, `DefaultParams` → константы.
- NFKC и длина пароля на входе.
- Нормализация локали письма.
- Нестроковый `x-rate-limit` — ошибка, а не тихий `default`. Страж ветки `UnknownClasses`.
- Маскирование `client_ip` в аудите; словарь маскирования знает `code`, `secret`, `recovery`, `password`,
  `token`.
- Конструктор аргументов письма сам ставит `V` (`mail.NewSendArgs`; для `staff.mail` — `NewMailArgs`).
- У `staff_*` и `approvals` нет sequence-прав для `api`.

## 6. geo 2/2 — бэкенд админки

Ручки — по спеке geo §5 с изменениями ниже. Код — `backend/internal/geo/admin/`, сценарии —
`internal/app`. Каждая мутация пишет аудит (`scope_city_id` — город) и событие в той же транзакции.

План A вводит только `GET /v1/geo/cities?q=&cursor=` (`geo.cities.read`, все статусы) — для выбора
городов модератора. План B её расширяет, а не создаёт.

### 6.1. Ручки

| Ручка | Право | Изменения к спеке geo |
|---|---|---|
| `GET /v1/geo/places?q=&country=&cursor=` | `geo.places.search` | — |
| `GET /v1/geo/cities?status=&country=&region_id=&q=&cursor=`, `GET …/{cityId}` | `geo.cities.read` | модератор — только свои (список в плане A — без фильтра по скоупу: нужен для выбора городов) |
| `POST /v1/geo/cities` | `geo.cities.activate` | — |
| `PATCH /v1/geo/cities/{cityId}` | `handler`: `geo.cities.manage` (статус, slug), `geo.cities.rename` (названия) | поле без права → `403 auth.forbidden` |
| `POST /v1/geo/cities/{cityId}/link {geoname_id}` | `geo.cities.manage` | **новая**: ручная привязка неоднозначного города из отчёта сверки |
| `GET …/{cityId}/districts` | `geo.cities.read` | — |
| `POST …/{cityId}/districts`, `PATCH …/districts/{districtId}` | `geo.districts.manage` | район чужого города → `404 geo.district_not_found` (тест: модератор своего города, район чужого) |
| `GET /v1/geo/countries`, `POST`, `PATCH /{code}` | read / `geo.countries.manage` | валюта — по списку ISO 4217, `country_code` — ISO 3166-1 alpha-2 |
| `GET /v1/geo/countries/{code}/regions` | `geo.cities.read` | **новая**: выбор региона при активации |
| `POST /v1/geo/imports {country}`, `GET /v1/geo/imports`, `GET …/{importId}` | `geo.imports.run` / `…read` | **новая** `GET {id}` — отчёт сверки (ambiguous, conflict) |
| `GET /v1/app/versions`, `PUT /v1/app/versions/{platform}` | `platform.versions.read` / `…manage` | **новая** `GET` |
| `GET /v1/flags`, `PATCH /v1/flags/{key} {version, enabled_globally?, enabled_city_ids?}` | `platform.flags.read` / `…manage` | **новые**; запись в `platform/flags`; `version` в теле (расхождение → `409 platform.flag_stale`); несуществующий флаг → `404 platform.flag_not_found`; города проверяются; событие `platform.flag_changed` с детерминированным uuid агрегата |

### 6.2. Импорт из админки

- admin-api ставит задачу своим River-клиентом (§4.10).
- Ручка отвечает `202 {job_id}`: строку `geonames_imports` создаёт сам импорт в воркере (спека geo §5.4
  шаг 1), у `admin` на ней только чтение. Журнал ищется по `river_job_id`. Строка `running` уже есть или
  River отказал по уникальности → `409 geo.import_in_progress`.
- Страна проверяется по `^[A-Z]{2}$`, а `jobs.NewImportArgs` нормализует регистр.
- Задача `geo.import` на `ErrImportInProgress` (параллельный импорт оператора) уходит в
  `river.JobSnooze(1 мин)`, а не в discarded.
- Аудит постановки — от имени сотрудника. В `geonames_imports.started_by` (uuid без FK, «сотрудник; NULL —
  оператор») пишется id из `staff_accounts`; `writeAudit` импорта переходит с `actor_user_id` на
  `actor_staff_id`.

### 6.3. Обязательные хвосты этапа 1

Это первые задачи плана B.

1. Стражи ограничений R13 из 0019: `city_slug_history_slug_format`, `replaced_at NOT NULL`, каскады
   `*_names` и истории, population/version, CHECK locale/country_code в `geonames_*`.
2. Пробелы теста прав:
   - `api SELECT regions/region_names`;
   - `admin INSERT/UPDATE countries/regions`;
   - `admin SELECT geonames_places/admin1`;
   - `worker INSERT geonames_place_names`.
3. Активация:
   - `23505 cities_geoname_id_key` → `409 geo.city_already_active`;
   - пустой `Slugify` → slug из `geoname_id` (`city-<id>`);
   - обрезка базы slug до 100 с учётом суффикса;
   - `replaced_at` из `clock.Clock`.
4. Гонка `regions_country_admin1_key` в `ImportLinkRegion` — свой savepoint, повтор чтения региона.
5. `409 geo.slug_taken` для slug из истории другого города. Возврат к своему старому slug удаляет его
   из истории.
6. `store_url`: запрет пробелов и управляющих символов в коде и в CHECK `app_versions_store_url_check`.
   В контракте — «`https://` в нижнем регистре».
7. Страж кодов ошибок admin: каждый код из `codes.go` модулей есть в контракте admin и в
   `apps/admin/messages`.
8. Флак тестов под нагрузкой: ограничить `-p` в `backend/Taskfile.yml`, один клон базы на пакет в
   handler-тестах.

Удаление `/v1/health` из admin сделано в плане A (`HealthStatus` заменён `GET /v1/staff/me`).

### 6.4. События и коды

- События — по спеке geo §6: `geo.city_activated`, `geo.city_status_changed`, `geo.city_renamed`,
  `geo.city_slug_changed`, `geo.city_linked`, `geo.district_created|updated|archived`,
  `geo.country_updated`, `platform.app_versions_changed`, плюс `platform.flag_changed`.
- Коды — по спеке geo §8, без `geo.forbidden` и `platform.forbidden` (§2.4).
- Подписи флагов — в локалях админки (`flags.<ключ>`).

## 7. Ошибки

Новые коды среза. Тексты — в `apps/admin/messages/{ru,en}.json` (`errors.<код>`; для админки en
заполняется переводом), каждый код — в контракте admin.

Коды платформы admin (`httpx.AdminPlatformCodes`, только в контракте admin):

| Код | HTTP | Когда |
|---|---|---|
| `auth.forbidden` | 403 | нет права или город вне скоупа |
| `auth.csrf_failed` | 403 | нет `Origin`/`X-WF-Admin` или чужой origin |
| `auth.ip_not_allowed` | 403 | IP вне аллоулиста |
| `auth.reauth_required` | 401 | бездействие или смена сети — нужен второй фактор |
| `auth.setup_required` | 403 | стадия `setup`, операция недоступна |
| `platform.approval_not_found` | 404 | — |
| `platform.approval_self` | 409 | подтвердить свой запрос или запрос о себе |
| `platform.approval_stale` | 409 | цель изменилась после запроса |
| `platform.approval_closed` | 409 | запрос уже решён, отменён или просрочен |

Коды модуля `staff`:

| Код | HTTP | Когда |
|---|---|---|
| `staff.invalid_credentials` | 400 | вход: почта или пароль не подошли (в том числе учётка закрыта) |
| `staff.mfa_invalid` | 400 | код второго фактора не подошёл, `attempts_left` |
| `staff.password_invalid` | 400 | неверный текущий пароль в `/me/password` (счётчик второго фактора) |
| `staff.wrong_stage` | 409 | операция не для текущей стадии |
| `staff.invite_invalid` | 400 | код приглашения, почта или срок |
| `staff.email_taken` | 409 | приглашение на почту активной учётки |
| `staff.not_found` | 404 | нет сотрудника |
| `staff.session_not_found` | 404 | чужая или несуществующая сессия |
| `staff.city_not_found` | 422 | несуществующий город в `city_ids` |
| `staff.last_owner` | 409 | снять последнего владельца |
| `staff.self_action` | 409 | действие управления над собой |

Схема `Problem` (в public и admin — одинаковая копия) получает необязательные `attempts_left` (int) и
`retry_after` (int, секунды).

## 8. Контракт

- Модули: `contracts/openapi/admin/{staff,geo,platform}/`, бандл `admin.yaml`. Полное описание и
  примеры всего — `.claude/rules/contracts.md`. Общий параметр мутаций — заголовок `X-WF-Admin: 1`.
- Новые расширения:
  - `x-permission` — обязательно на каждой операции (§2.4);
  - `x-permission-any: [<ключи>]` — обязательно при `x-permission: handler`;
  - `x-idempotency: no-store` — у операций, отдающих секреты (`staffMfaSetup`, `staffMfaConfirm`,
    `regenerateStaffRecoveryCodes`).
- `x-anonymous-mutations` — ровно операции с `security: []`: `staffLogin`, `staffAcceptInvite`,
  `staffResendInviteSelf`. Остальные мутации, включая `staffVerifyMfa` и `staffUnlock`, требуют сессию и
  `Idempotency-Key`.
- Операции с `dual` документируют ответ `202` со схемой `Approval`.
- Стражи в `contracts/src`:
  - у каждой операции admin есть `x-permission`; значение — из каталога (не черновое) или служебное;
  - `handler` ⇔ непустой `x-permission-any` из каталога;
  - `anonymous` ⇔ `security: []`;
  - операция с полями `secret`, `otpauth_uri` или `recovery_codes` в ответе обязана иметь
    `x-idempotency: no-store`.

  Каталог выгружается из Go в `contracts/openapi/admin/permissions.json` через `./task gen`.

## 9. Админка (`apps/admin`)

### 9.1. Технические решения

| Что | Решение |
|---|---|
| Роутер | TanStack Router, маршруты кодом; фильтры списков — типизированные search-параметры |
| Данные | TanStack Query + openapi-fetch (уже есть). Middleware клиента добавляет `X-WF-Admin`; фоновые опросы помечаются `X-WF-Background: 1`. Ключ идемпотентности создаёт хук мутации — один на действие, повтор того же действия идёт с тем же ключом |
| Стили | CSS Modules поверх `--wf-*`. Недостающие цвета макетов (фоны чипов и статусов) — новые токены в `contracts/tokens/tokens.json` |
| Шрифты | Oswald и Manrope через `@fontsource`, без внешних запросов |
| Формы | react-hook-form; `validation.failed` раскладывается по полям |
| QR | SVG на клиенте (библиотека — первым шагом плана); секрет не уходит наружу |
| Тема | тёмная (токены только тёмные) |
| Локали | ru и en (en — перевод), переключатель в меню профиля, выбор сохраняется `PATCH /v1/staff/me/locale` |

### 9.2. Набор компонентов (`src/ui`)

- **AppShell**: на десктопе — сайдбар с группами меню, счётчиками и карточкой сотрудника («Мария К.,
  модератор · Ставрополь», «Выйти»); на телефоне — нижний таббар и «Ещё». Ширина-переключатель —
  токен, по макету 08 (390).
- **DataTable**:
  - поиск, чипы-фильтры, раскрывающийся фильтр, сортировка, курсорная пагинация;
  - «Показано N», «Сбросить фильтры»;
  - состояния: скелетон, пусто («Ничего не нашлось… Это не ошибка — просто пусто»), ошибка с кодом и
    «Повторить».
- **Остальные компоненты**:
  - Form и поля;
  - OtpInput (6 ячеек, вставка из буфера, автоотправка);
  - Drawer (панель справа) и Sheet (шторка снизу на телефоне);
  - ConfirmDialog с вводом слова или почты;
  - Toast (успех исчезает, ошибка — нет);
  - StatusChip, CountBadge.
- **Состояния** (раздел «Состояния» макета 07):
  - нет сети — баннер, мутации не копятся;
  - нет прав — прямой адрес раздела без права (в меню такого раздела нет);
  - 429 — таймер по `Retry-After`;
  - «только с компьютера»;
  - «отправлено на подтверждение» — после `202`.

### 9.3. Сессия на клиенте

- При загрузке `GET /v1/staff/me`. Нет сессии — экран входа с возвратом на исходный адрес. Стадия
  `mfa_pending` — шаг 2 входа, `setup` — только настройка 2FA (и после перезагрузки страницы).
- `401 auth.reauth_required` — модалка ввода второго фактора поверх текущей страницы. Состояние форм не
  теряется, после разблокировки исходный запрос повторяется.
- `401 auth.unauthenticated` — экран входа. Черновик формы сохраняется в `sessionStorage` по ключу
  маршрута.
- Меню и кнопки строятся по `permissions` из `me`: раздела без права в меню нет. Кнопка «Пригласить» и
  выбор роли в форме ограничены ролями, которые сотрудник может выдать.
- Счётчик «Подтверждения» — `GET /v1/approvals?box=incoming&status=pending` с `X-WF-Background: 1`, раз
  в минуту и при фокусе окна.

### 9.4. Экраны

Нарисованы в макете — делаем по нему. Дорисованы в коде — помечены «*», список уходит в
`design/README.md`.

1. **Вход** (07, 08) — шаг 1: почта и пароль (поле пароля — *); шаг 2: 6 ячеек или «Использовать
   резервный код» (*). Ошибка «Код не подошёл. Осталось N попыток…»; на последней — «доступ закрыт на
   час, мы сообщили администраторам». Подпись «вход только с рабочей почты и вторым фактором, каждый
   вход пишется в журнал».
2. **Принятие приглашения** (*) — `/accept`: почта, пароль дважды с подсказкой требований, код из письма,
   «Отправить код ещё раз» с отсчётом 60 с.
3. **Настройка 2FA** (07 «Мой доступ», первая часть):
   - QR и секрет текстом с кнопкой «Скопировать»;
   - ввод кода, «ПОДТВЕРДИТЬ»;
   - резервные коды: «Скопировать», «Скачать .txt», чекбокс «Я сохранил коды» (*), затем вход.
4. **Мой доступ** (07):
   - статус 2FA, «ПЕРЕВЫПУСТИТЬ КОДЫ» (с TOTP);
   - активные сеансы: «Завершить», «ЗАВЕРШИТЬ ВСЕ ДРУГИЕ СЕАНСЫ»;
   - смена пароля (*);
   - «Потерял доступ» — текст: доступ восстанавливает владелец или админ, вдвоём.
5. **Роли и доступ** (07):
   - матрица прав из API (подписи — из локалей), «Кто имеет доступ», блок «Правило двух рук» (текст —
     по матрице);
   - «ПРИГЛАСИТЬ» → форма в панели (*): почта, имя, роль, города модератора (поиск по
     `GET /v1/geo/cities`);
   - по строке сотрудника (*): переотправить приглашение, изменить роль и города, сессии, сбросить
     доступ, выключить.
6. **Подтверждения** (*, группа «Безопасность»): вкладки «Входящие» и «Мои», карточка запроса (кто,
   что, кому, когда истекает), «Подтвердить», «Отклонить» с комментарием, «Отменить».
7. **Журнал действий** (07): вкладки по разделам, фильтры (кто, действие, объект, период), строка
   «время · кто · тег · действие · id», раскрытие «было/стало».
8. **Города и запуск** (07 — карточки и фильтры):
   - карточка: название, статус-чип («ожидание / пилот / живой»), регион, население, районы;
   - «Активировать город» (*): поиск места по GeoNames → выбор → переопределение страны и региона →
     статус;
   - страница города (*): статус, slug с историей, названия по локалям, районы с архивом, привязка
     `geoname_id`.

   Метрики макета (игроки, капитаны, матчи) появятся с модулями players, teams и matches.
9. **Импорт GeoNames** (*): журнал импортов, «Запустить импорт» по стране, статус обновляется опросом
   раз в 5 с (`X-WF-Background`), отчёт сверки со ссылками «Привязать».
10. **Страны** (*): список, правка правил в панели, включение страны.
11. **Версии приложения** (*): iOS и Android — минимальная, рекомендуемая, ссылка на стор.
12. **Флаги функций** (07): матрица «функция × город» с переключателями, глобальный переключатель.

Меню — по макету 07: «Безопасность» (Журнал, Роли и доступ, Подтверждения, Мой доступ), «Справочники»
(Города и запуск, Страны, Импорт, Версии, Флаги). Разделы будущих модулей не показываются, пока модуль
не построен. Стартовая страница — первый разрешённый раздел.

Телефон (08) — ограничение интерфейса, не защита:
- работают вход, принятие приглашения, **настройка 2FA**, журнал, подтверждения (список и чтение);
- подтверждение и отклонение действий `staff.*` скрыты — «выдача прав только с компьютера»;
- закрыты города, импорт, страны, версии, флаги, роли и «Мой доступ» — «Этот раздел открывается только
  с компьютера».

### 9.5. Структура

`src/app/` (роутер, провайдеры, guard'ы), `src/ui/`, `src/features/{auth,me,staff,approvals,audit,
geo,versions,flags}/`, `src/api/`. Правила — новое `.claude/rules/admin.md`: компоненты только из
`src/ui`, права — из `me.permissions`, тексты — только локали. Подписи прав (`permissions.<ключ>`),
действий подтверждений (`approvals.actions.<action>`) и флагов (`flags.<ключ>`) — в локалях; страж —
каждый ключ каталога и каждое действие есть в ru и en.

## 10. Структура кода бэкенда

- `backend/internal/staff/` — модуль (§4), слой 0.
- `backend/internal/platform/perm/` — каталог прав и матрица (§2.4).
- `backend/internal/platform/approvals/` — подтверждение вдвоём (§4.8).
- `backend/internal/platform/auth/` — `Grant`, `Staff`, `StaffSessions`, `Subject`.
- `backend/internal/platform/keys/` — кольцо ключей `Ring`.
- `backend/internal/httpapi/admin/` — конвейер §5.1, CSRF, аллоулист, права, статика, ручки
  подтверждений и журнала.
- `backend/internal/platform/audit/` — `List`, `actor_staff_id`, `actor_label`, `scope_city_id`.
- `backend/internal/platform/flags/` — запись (план B).
- `backend/cmd/admin-api/` — подкоманды `staff …` (§4.2), River-клиент с очередью `staff` (§4.10).

## 11. Тесты и стражи (TDD, страж проверяется подсадкой бага)

- **Права**:
  - тест на каждую операцию admin по субъектам «владелец, админ, модератор своего города, модератор
    чужого, поддержка, без сессии, стадии `mfa_pending` и `setup`, `Reauth`»;
  - для ключей каталога таблица ожиданий выводится из матрицы; для `handler` — ручные табличные тесты;
  - инварианты матрицы: у `support` нет ни одного мутирующего права; у `moderator` мутирующие права
    только `scoped`; `grant_elevated` и `revoke_owner` — только у `owner`.
- **Контракт**: стражи §8; `docs/05` совпадает с матрицей (`gen:check`).
- **БД**: `api` не читает `staff_*` и `approvals`; `worker` не видит `staff_*`; CHECK акторов `audit_log`
  — каждая ветка; модератор без городов невозможен.
- **Вход**:
  - одинаковые ответы и время для «нет почты / неверный пароль / не active / без 2FA / закрыта»;
  - лимит по почте одинаков для существующих и несуществующих адресов;
  - повтор TOTP запрещён (`last_used_step`); 2FA не привязать без приглашения;
  - `setup` без `ack` не пускает дальше;
  - блокировка после 5 неудач второго фактора, гашение сессий и одно письмо в час;
  - неудачи пароля сессий не гасят;
  - запись неудачи переживает ответ 4xx.
- **Сессия**:
  - абсолютный срок; 20 минут бездействия; смена сети → `reauth_required`;
  - фоновые запросы раз в минуту 25 минут → `reauth_required`;
  - новый токен после второго фактора, `ack` и `unlock`, старый недействителен;
  - CSRF без заголовка и с чужим `Origin`; отказ сессии считается в лимите.
- **Вдвоём**: свой запрос и запрос о себе не подтвердить; устаревшая цель → `stale`; просроченный —
  `closed`; инициатор выключен → запросы погашены; исполнение атомарно с аудитом и событием; права
  подтверждающего со скоупом; гонка последнего владельца.
- **Идемпотентность**: ответ с секретом не попадает в `idempotency_keys`, повтор → `replay_unavailable`.
- **geo**: по §6.3 и тесты прав.
- **Админка**: компоненты и экраны — Vitest и MSW по типам контракта; ключи локалей ru и en (en не
  пустой); каждый код ошибки имеет текст.
- **Живой e2e** (Playwright, `./task admin:e2e`) поверх настоящих admin-api, Postgres и Mailpit, TOTP
  генерируется в тесте:
  1. bootstrap владельца → письмо;
  2. принятие → 2FA → резервные коды;
  3. выход и вход;
  4. приглашение админа → подтверждение вторым владельцем → принятие;
  5. активация города, импорт страны (план B: HTTPS-фикстура через `WORKER_GEONAMES_BASE_URL`, без
     внешнего GeoNames);
  6. запись в журнале.

  В CI — с Postgres CI и Mailpit (проверить, §14).

## 12. Этапы

Спека одна, планов три. Каждый исполняется и ревьюится отдельно (субагенты по
`.claude/agents/`).

| План | Состав | Что видно вживую |
|---|---|---|
| **A. Ролевая модель и вход** | 1) `docs/05-ролевая-модель.md` и **отдельное ревью модели** (opus и пользователь) до кода. 2) Платформа: миграция 0020, `auth.Staff`, `perm`, `approvals`, конвейер §5, контракт `x-permission`. 3) Параллельно: модуль `staff` ∥ каркас админки, набор компонентов, экраны 1–7 на MSW. 4) Сборка вживую и e2e | вход, 2FA, приглашения, роли, подтверждения, журнал |
| **B. geo 2/2** | 1) Хвосты §6.3. 2) Ручки §6.1–6.2 ∥ экраны 8–12. 3) e2e | города, импорт, страны, версии, флаги |
| **C. Render** | `wf-admin-api` (тот же образ, `CMD admin-api`) отдаёт и статику админки (`ADMIN_STATIC_DIR`, один origin без rewrite); `wf-worker` (импорт geo); роль БД `admin` на Neon; SMTP для писем (`ADMIN_MAIL_*`); `staff bootstrap` первого владельца; `docs/deploy-dev.md` | админка на стенде |

Решения плана C за пользователем: background worker на Render платный, нужен SMTP-провайдер.

## 13. Обновление документов (в коммитах планов)

- Спека бэкенда:
  - §4.1 — модуль `staff` и его таблицы, `approvals` у платформы;
  - §4.3/§4.4 — слой `staff` = 0; подтверждение вдвоём — механизм платформы (исполнитель модуля в
    транзакции платформы);
  - §4.5 — роли игроков;
  - §6.1 — конвейер admin-api (сессия, CSRF, права, статика);
  - §6.3 — права: конвейер по `x-permission` плюс сценарий; `auth.forbidden` в админке;
  - §6.4 — анонимные мутации admin-api, `x-idempotency: no-store`;
  - §8.4(в) — условие включения аллоулиста; IP — сигнал повторного второго фактора, не авторизация;
  - §10 — сотрудники отдельно, ссылка на эту спеку и `docs/05`, сроки сессии, правило двух рук, задачи
    сотрудников в admin-api;
  - §10.1 — права `admin` (River, `city_slug_history`), `worker` без `staff_*`;
  - §11 — схема (без `sessions.audience` для админки);
  - §12 — новые стражи;
  - §13 — порядок: `staff` выполнен до `identity`.
- Спека geo:
  - §5.1 — шов сессии по §5.1 этой спеки;
  - §5.2 — новые ручки, `auth.forbidden` вместо `geo.forbidden`/`platform.forbidden`;
  - §5.4 — `started_by` и аудит от сотрудника, ответ `{job_id}`;
  - §3.8 — `DELETE` на `city_slug_history`;
  - §8 — коды;
  - §10 — «401 до identity» больше не верно.
- `docs/01-домен-и-правила.md` — таблица ролей: модератор и админ — сотрудники, капитан — допуск и
  команда.
- `docs/02-архитектура.md` — раздел «Админка»: сроки сессии, правило двух рук, IP.
- `CLAUDE.md` — правило 4 (роли), правило 5 (анонимные мутации в админке).
- Спека структуры репо — cookie и origin админки.
- `.claude/rules/`:
  - `admin.md` (новое);
  - правило бэкенда для модуля `staff`;
  - `backend-http.md` — конвейер admin-api;
  - `backend-platform.md` — `perm`, `approvals`, `auth.Staff`, `keys.Ring`, почта `staff.mail`;
  - `contracts.md` — `x-permission`, `x-idempotency`, анонимные мутации в админке.
- `design/README.md` — список дорисованных в коде экранов.
- `docs/deploy-dev.md` — план C, ротация `ADMIN_STAFF_SECRET_KEYS`.
- `docs/versions.md` — новые зависимости.
- Память — статус и остатки после каждого плана.

## 14. Не проверено — проверить первыми шагами плана A

- `__Host-` cookie с `Secure` на `http://127.0.0.1` в dev (Chrome, Firefox, Playwright). Не работает →
  в dev имя без префикса и `Secure` по флагу конфига, в проде — строго.
- TOTP на Go: своя реализация по RFC 6238 с тестовыми векторами RFC или библиотека — решить по
  состоянию библиотек и версий.
- Последние стабильные версии TanStack Router, react-hook-form, `@fontsource/*`, генератора QR —
  запись в `docs/versions.md`.
- `ALTER TYPE … DROP VALUE` недоступен в PostgreSQL. Чистка `role_name`/`role_scope` — пересоздание
  типа в миграции (таблица пуста).
- Права роли `admin` на служебные таблицы River для исполнения задач очереди `staff` в admin-api.
- Отдача статики admin-api из `ADMIN_STATIC_DIR` и IP клиента за прокси Render (план C).
- Mailpit в CI для живого e2e админки.
