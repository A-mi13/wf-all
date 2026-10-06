# Спека среза «Сотрудники и админка geo»

Дата: 06.10.2026. Статус: на ревью (редакция 1).
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
  модератора), `dual` (вдвоём, §4.8), `read` (только чтение — для прав чтения равно `allow`), `deny`.
- Матрица — константа в коде модуля `staff` (`internal/staff/perm`). Модуль, которому нужны свои
  права, добавляет строки туда же своим коммитом.
- `./task gen` генерирует из матрицы таблицу в `docs/05-ролевая-модель.md` (между маркерами).
  `gen:check` ловит расхождение. Экран «Роли и доступ» берёт матрицу из `GET /v1/staff/roles`.
- Каждая операция контракта admin объявляет право расширением `x-permission`. Кроме ключей каталога
  есть три служебных значения: `anonymous` — без сессии (вход, принятие приглашения, повторная
  отправка); `session` — сессия в любой стадии, стадию проверяет хендлер (второй фактор входа,
  разблокировка, выход, `GET /v1/staff/me`, настройка 2FA); `self` — активная сессия, своё
  («Мой доступ»). Конвейер проверяет его сам. Если в пути есть `cityId`,
  `scoped` сверяется с городами модератора. Для остальных случаев хендлер вызывает
  `staff.Can(ctx, perm, cityID)`.
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
- Добавить модератору город — тоже выдача (`staff.roles.grant`, вдвоём). Убрать город — сразу.
- Снять роль = выключить учётку (`status = disabled`). Исполняется сразу, кроме снятия владельца.
  Гасит все сессии сотрудника. Последнего активного владельца снять нельзя (`409 staff.last_owner`).
- Никакое действие над собой через управление (`409 staff.self_action`): свои сессии и пароль — через
  «Мой доступ».
- Повторное приглашение на почту выключенной учётки возвращает ту же учётку через сброс доступа. История
  и аудит сохраняются.
- Первый владелец создаётся командой оператора (§4.2).

## 3. Модель данных

Миграция `0020_staff.sql` (модуль `staff`) и правки платформы. Все таблицы модуля — с префиксом
`staff_`, их закрывает правило `staff\_%` в `grants.sql` (у `admin` полный DML, у остальных ничего).

### 3.1. Таблицы `staff`

```sql
CREATE TYPE staff_role AS ENUM ('owner', 'admin', 'moderator', 'support');
CREATE TYPE staff_status AS ENUM ('invited', 'active', 'disabled');
CREATE TYPE staff_session_stage AS ENUM ('mfa_pending', 'setup', 'active');
CREATE TYPE staff_invite_purpose AS ENUM ('invite', 'access_reset');
CREATE TYPE staff_approval_status AS ENUM ('pending', 'executed', 'rejected', 'cancelled', 'expired', 'failed');
```

- **`staff_accounts`**:
  - `id` uuid PK, `email` citext UNIQUE, `display_name` text (1–80);
  - `locale` text (`ru|en`), `status` staff_status;
  - `password_hash` text NULL — NULL до принятия приглашения и после сброса доступа;
  - `failed_attempts` int, `locked_until` timestamptz NULL, `last_login_at`;
  - `created_at`, `created_by` uuid NULL — NULL у созданного оператором;
  - `version` bigint.
- **`staff_roles`**: `staff_id` PK → accounts, `role` staff_role, `granted_by` uuid NULL, `granted_at`.
- **`staff_role_cities`**: `(staff_id, city_id)` PK, `city_id` → `cities`. CHECK на уровне сервиса и
  отложенный триггер: у `moderator` хотя бы один город, у остальных ни одного.
- **`staff_mfa`**:
  - `staff_id` PK, `secret_enc` bytea, `key_id` text;
  - `confirmed_at` NULL — NULL пока идёт настройка;
  - `last_used_step` bigint.
- **`staff_recovery_codes`**: `(staff_id, code_hash)` PK, `used_at` NULL. Ровно 10 штук на поколение,
  перевыпуск удаляет старые.
- **`staff_invites`**:
  - `id`, `staff_id`, `purpose`, `code_hash`;
  - `expires_at` (24 ч), `attempts`, `sends`, `last_sent_at`;
  - `consumed_at`, `revoked_at`.
  - Живое приглашение у учётки одно (частичный UNIQUE по `staff_id` WHERE `consumed_at IS NULL AND
    revoked_at IS NULL`).
- **`staff_sessions`**:
  - `id`, `staff_id`, `token_hash` UNIQUE, `stage`;
  - `created_at`, `last_seen_at`, `mfa_at`, `expires_at` (абсолютный срок);
  - `ip` inet, `user_agent`;
  - `revoked_at`, `revoked_reason`.
- **`staff_approvals`**:
  - `id`, `permission` text, `action` text, `payload` jsonb (с полем `v`);
  - `target_staff_id` NULL, `requested_by`, `requested_at`, `expires_at` (24 ч);
  - `status`, `decided_by` NULL, `decided_at`, `decision_comment`, `error` NULL.

### 3.2. Правки чужих таблиц

- **`role_assignments`** (identity). Новые перечисления: `role_name` → только `captain`, `role_scope` →
  только `city`. Старые значения удаляются (таблица пуста — проверка в миграции). Правило 4
  `CLAUDE.md` уточняется: роли игроков — `role_assignments`, сотрудников — `staff_roles`.
- **`audit_log`** (платформа):
  - `actor_staff_id uuid NULL` и `scope_city_id uuid NULL` (город, к которому относится запись, — для
    журнала модератора);
  - CHECK: не больше одного актора из `actor_user_id`/`actor_staff_id` (оба NULL — система или
    оператор, тогда `actor_role` = `system|operator`);
  - индекс `(actor_staff_id, created_at DESC)`, `(scope_city_id, created_at DESC)`.
- **`idempotency_keys`** (платформа): `subject_kind text NOT NULL DEFAULT 'user'` (`user|staff`),
  PK `(subject_kind, user_id, key)`, колонка `user_id` переименовывается в `subject_id`. Ключи игрока и
  сотрудника больше не сталкиваются.
- **`feature_flags`**: `version bigint`. Ключи флагов заводятся миграциями модулей, а не из админки:
  админ только переключает.

### 3.3. Права ролей БД (`grants.sql`)

- `worker`: `SELECT, DELETE` на `staff_invites`, `staff_sessions`, `staff_approvals` (чистка, §4.10) —
  точечное исключение из правила `staff\_%`. Плюс `SELECT, UPDATE` на `staff_mfa` для перешифровки
  (§4.11). `grants_test` проверяет и точечные права, и то, что остальное `staff_*` воркеру закрыто.
- `admin`: `DELETE` на `city_slug_history` (спека geo §3.8 — возврат к своему старому slug).
- `api`: без изменений — к `staff_*` доступа нет (страж §11).

## 4. Модуль `staff` (бэкенд)

Код: `backend/internal/staff/` по раскладке модулей спеки бэкенда §3 (`internal/{app,domain}`,
`queries/`, `admin/` — хендлеры, `jobs/`, `perm/` — каталог прав и матрица, `intx/` — пока пусто).
archtest: модуль заносится в `modules.go` и `ownership.go` со всеми таблицами `staff_*`.

### 4.1. Криптография и форматы

- Пароль: `platform/password` (argon2id, ≥10 символов, список распространённых, NFKC, потолок длины
  на входе) — остатки фундамента 3/3 закрываются здесь же.
- TOTP: RFC 6238, SHA-1, 6 цифр, шаг 30 с, допуск ±1 шаг, секрет 160 бит (base32). `last_used_step`
  запрещает повтор кода и кодов старше уже принятого.
- Секрет TOTP шифруется AES-256-GCM. Ключи — `ADMIN_SECRET_KEYS`, список для ротации в формате
  `platform/keys` (первый — текущий). `key_id` в строке говорит, каким ключом зашифровано.
- Коды приглашения — 6 цифр, хранится HMAC-SHA-256 с ключом, выведенным из `ADMIN_SECRET_KEYS`
  (HKDF, метка `staff-invite`). Резервные коды — 10 штук по 10 символов base32 (`xxxxx-xxxxx`),
  хранится HMAC той же схемы (метка `staff-recovery`).
- Токен сессии — `token.NewRefresh` (256 бит), в базе — SHA-256.

### 4.2. Первый владелец

`admin-api staff bootstrap --email <почта> --name <имя>` — подкоманда бинарника admin-api.

- Создаёт учётку `owner` в статусе `invited` и приглашение. Письмо уходит через outbox, а код и ссылка
  печатаются в консоль — на случай, если почта ещё не настроена.
- Аудит: `actor_role = operator`.
- Запуск разрешён в любой момент: доступ к серверу и есть корень доверия, ограничение давало бы ложное
  чувство защиты. Повтор для существующей почты — ошибка с подсказкой.
- Два владельца на старте заводятся двумя запусками — правилу двух рук нужен второй человек.

### 4.3. Приглашение и повторная отправка

1. `POST /v1/staff/invites {email, display_name, role, city_ids?}` (`staff.roles.grant` или
   `…grant_elevated` по роли) создаёт подтверждение и отвечает `202 {approval}`.
2. Исполнение подтверждения:
   - создаётся учётка `invited`, роль и города;
   - приглашение получает новый код;
   - письмо `staff.invite` уходит со ссылкой `ADMIN_ORIGIN/accept?email=…` и кодом.

   Почта уже есть: учётка выключена — сброс доступа той же учётки, иначе `409 staff.email_taken`.
3. Повторная отправка самим сотрудником: `POST /v1/staff/auth/invite/resend {email}` — анонимная,
   всегда `202`.
   - Новый код отправляется, только если есть живое приглашение, с последней отправки прошло 60 с,
     а за сутки отправок меньше 5.
   - Новый код гасит старый, срок 24 ч не продлевается.
   - Клиент показывает отсчёт 60 с.
4. Повторная отправка админом: `POST /v1/staff/accounts/{id}/invite/resend` (`staff.invites.resend`).
   Выпускается новый код, срок 24 ч начинается заново. Если с прошлой отправки не прошло 60 с —
   `429 staff.invite_resend_too_soon`.

### 4.4. Принятие приглашения (первый вход)

`POST /v1/staff/auth/invite/accept {email, code, password}`:

- Проверки:
  - приглашение живо и код верен (5 неверных кодов гасят приглашение — нужен повторный запрос);
  - пароль проходит правила;
  - учётка `invited` или после сброса доступа.
- Ответ при любой ошибке кода, почты или срока одинаковый: `400 staff.invite_invalid`. Ошибки пароля —
  `400 validation.failed` с полем `password` и кодом `too_short|common`.
- Успех:
  - пароль сохраняется, приглашение гасится;
  - создаётся сессия в стадии `setup` (cookie §5.2);
  - аудит `staff.invite_accepted`.

### 4.5. Настройка 2FA (стадия `setup`)

- `POST /v1/staff/me/mfa/setup` → `{secret, otpauth_uri}`. До подтверждения повторный вызов выпускает
  новый секрет. Секрет отдаётся только в стадии `setup`.
- `POST /v1/staff/me/mfa/confirm {code}` → `{recovery_codes: [10]}`. Код сверяется с неподтверждённым
  секретом, `confirmed_at` ставится, коды выдаются один раз.
- `POST /v1/staff/me/mfa/recovery-codes/ack`:
  - переводит сессию в `active`, учётку — в `active`;
  - ставит `mfa_at`;
  - аудит `staff.mfa_enabled`.

  Без `ack` сессия остаётся в `setup` — сервер сам не пускает дальше (страж §11).
- В стадии `setup` доступны только `GET /v1/staff/me`, три ручки выше и выход. Остальные →
  `403 staff.setup_required`.

### 4.6. Вход

1. `POST /v1/staff/auth/login {email, password}`:
   - успех → сессия в стадии `mfa_pending` на 5 минут (та же cookie), ответ `200 {stage: "mfa_pending"}`;
   - нет почты, неверный пароль, учётка не `active` или без 2FA — один ответ `400
     staff.invalid_credentials`. Время выравнивает `VerifyDummy`.
2. Учётка закрыта (`locked_until` в будущем): при **верном** пароле — `423 staff.locked` с
   `retry_after`, при неверном — тот же `staff.invalid_credentials`. Так о блокировке узнаёт только тот,
   кто знает пароль.
3. `POST /v1/staff/auth/mfa {code | recovery_code}` (стадия `mfa_pending`):
   - успех → стадия `active`, `mfa_at` и `last_login_at` ставятся, аудит `staff.login`;
   - резервный код при входе сгорает;
   - неудача → `400 staff.mfa_invalid {attempts_left}`.
4. Блокировка:
   - пять неудач подряд (пароль, второй фактор, разблокировка) ставят `locked_until = now + 1 ч` и
     гасят все сессии сотрудника;
   - письмо `staff.locked` уходит всем активным владельцам и админам;
   - успешный вход обнуляет счётчик.
5. Лимит по IP: класс `staff_auth` для всех `/v1/staff/auth/*`.

### 4.7. Сессия

- Абсолютный срок — 8 ч от входа. Истёк → `401 auth.unauthenticated`, сессия гасится (`expired`).
- Бездействие больше 20 минут или смена IP → `401 staff.reauth_required`. Сессия жива, но доступны
  только `POST /v1/staff/auth/unlock {code}` (TOTP или резервный код; ставит `mfa_at`, принимает новый
  IP), `GET /v1/staff/me` и выход.
- `last_seen_at` обновляется не чаще раза в минуту на сессию.
- Выход: `POST /v1/staff/auth/logout` гасит сессию и стирает cookie.
- «Мой доступ»:
  - `GET /v1/staff/me/sessions` — устройство и браузер из UA, город по IP не определяем, IP
    маскируется до /24 или /48;
  - `DELETE /v1/staff/me/sessions/{id}`;
  - `POST /v1/staff/me/sessions/revoke-others`.
- Пароль: `POST /v1/staff/me/password {current_password, code, new_password}` гасит другие сессии.
- Резервные коды: `GET /v1/staff/me/mfa` → `{confirmed_at, recovery_codes_left}`;
  `POST /v1/staff/me/mfa/recovery-codes {code}` → новые 10, старые удаляются.

### 4.8. Подтверждение вдвоём

- Операция с правом `dual` не исполняется, а создаёт `staff_approvals` и отвечает
  `202 {approval}`.
- Исполнитель действия регистрируется модулем: `approvals.Register(action, permission, Executor)`.
  Executor получает `payload` и исполняется в транзакции подтверждения: данные, аудит и события
  атомарны.
- `GET /v1/staff/approvals?box=incoming|mine&status=` — входящие видит тот, у кого есть право действия
  (с учётом скоупа), кроме инициатора.
- `POST /v1/staff/approvals/{id}/approve {comment?}`:
  - инициатор не может подтвердить свой запрос (`409 staff.approval_self`);
  - перед исполнением сверяется версия цели (`version` в `payload`). Изменилась → статус `failed`,
    `409 staff.approval_stale`.
- `…/reject {comment}` и `…/cancel` (только инициатор).
- Срок 24 ч, просроченные помечает воркер (`expired`).
- Каждый шаг пишется в аудит. Событие `staff.approval_decided` уходит в outbox. Письма о новых
  запросах — в этом срезе нет: счётчик в меню (§9.3).
- Регистрируются в этом срезе: `staff.invite`, `staff.role_change`, `staff.access_reset`,
  `staff.revoke_owner`.

### 4.9. Управление сотрудниками

| Ручка | Право | Ответ |
|---|---|---|
| `GET /v1/staff/accounts?status=&role=&q=&cursor=` | `staff.accounts.read` | список: роль, города, статус, последний вход, «приглашение не принято / просрочено» |
| `GET /v1/staff/accounts/{id}` | `staff.accounts.read` | карточка |
| `POST /v1/staff/invites` | grant / grant_elevated | `202` |
| `POST /v1/staff/accounts/{id}/invite/resend` | `staff.invites.resend` | `204` |
| `PUT /v1/staff/accounts/{id}/role {role, city_ids}` | расширение — grant (`202`), сужение городов — revoke (`200`) | |
| `POST /v1/staff/accounts/{id}/disable {reason, confirm}` | revoke / revoke_admin / revoke_owner | `200` или `202` |
| `POST /v1/staff/accounts/{id}/access-reset {reason, confirm}` | access.reset / reset_elevated | `202` |
| `GET /v1/staff/accounts/{id}/sessions`, `POST …/sessions/revoke-all` | `staff.sessions.revoke` | |
| `GET /v1/staff/roles` | `staff.roles.read` | роли, права, матрица |
| `GET /v1/staff/me` | `session` | учётка, роль, города, **список прав**, стадия, сессия |

- `confirm` — почта цели, введённая руками (спека бэкенда §10, разрушающие действия). `reason` — из
  списка (`left_team`, `security`, `role_change`, `other`) плюс комментарий.
- Сброс доступа гасит пароль, 2FA, коды и сессии, ставит `status = invited` и выпускает приглашение
  `access_reset`. Самостоятельного «забыл пароль» у сотрудников нет — доступ восстанавливает только
  другой сотрудник (защита от захвата через почту).

### 4.10. Воркер

Задача River `staff.cleanup` (периодическая, очередь `maintenance`):

- удаляет приглашения, погашенные или просроченные больше 30 дней назад;
- удаляет сессии, истёкшие или отозванные больше 30 дней назад;
- помечает просроченные подтверждения как `expired`.

### 4.11. Ротация ключа секретов

Задача River `staff.mfa_rekey` перешифровывает `staff_mfa` текущим ключом. Ставится командой
`worker staff rekey`. Старый ключ убирается из `ADMIN_SECRET_KEYS` только после неё (описание —
`docs/deploy-dev.md`).

### 4.12. Письма, события, аудит

- Письма (`backend/locales/{ru,en}.json`, `platform/mail`):
  - `staff.invite` — приглашение и код;
  - `staff.access_reset` — новый код после сброса;
  - `staff.locked` — владельцам и админам: кто, когда, IP маскирован.

  Язык — `staff_accounts.locale`, с нормализацией `en-US` → `en` (остаток фундамента 3/3).
- События: `staff.invited`, `staff.role_granted`, `staff.role_revoked`, `staff.access_reset`,
  `staff.locked`, `staff.approval_decided`. Агрегат — учётка сотрудника, `version`.
- Аудит:
  - каждое изменение в транзакции: кто (`actor_staff_id`, роль на момент действия), действие, объект,
    было/стало, причина, IP, UA;
  - входы и неудачные входы (неудачи — без `actor_staff_id`, с почтой в маскированном виде);
  - чтение журнала не аудируется, чтение персональных данных — аудируется (с модулями игроков).

## 5. Платформа admin-api

### 5.1. Конвейер

peer → IP-аллоулист → LimitBody → Timeout → маршруты → сессия сотрудника → CSRF → валидация по
контракту → проверка `x-permission` → rate limit → идемпотентность → хендлер.

- **IP-аллоулист**: `ADMIN_IP_ALLOWLIST` — список CIDR, пустой = выключен. Для прода включение — пункт
  спеки деплоя (не dev-стенда). Отказ — `403 auth.ip_not_allowed`.
- **Сессия**: `auth.StaffSessions` — `Load(ctx, token) (*auth.Staff, error)`. Шов из спеки geo §5.1
  меняется: загрузчик по токену cookie, Principal сотрудника — отдельный тип `auth.Staff{StaffID,
  SessionID, Role, CityIDs, Stage, Permissions}`. Отказ загрузки кэшируется на 5 с по хэшу токена и
  считается в IP-лимите — перебор токенов упирается в лимит (остаток M-3).
- **CSRF**: для `POST/PUT/PATCH/DELETE` обязателен `Origin`, равный `ADMIN_ORIGIN`, и заголовок
  `X-WF-Admin: 1`. Нарушение — `403 auth.csrf_failed`. Double-submit не нужен (спека бэкенда §10).
- **Права**: страж `x-permission` по таблице операций из бандла, `scoped` — по `cityId` в пути.
- **Идемпотентность**: субъект берётся из общего интерфейса `auth.Subject` (`kind`, `id`) — игрок
  или сотрудник. Анонимные мутации из `x-anonymous-mutations` идут без ключа.
- **Rate limit**: ключ «пользователь» для сотрудника — `staff:<id>`. Новые классы `staff_auth` и
  `admin_default`.

### 5.2. Cookie и origin

- Cookie `__Host-wf_admin_session`: `HttpOnly; Secure; SameSite=Strict; Path=/`, без `Domain`, без
  `Max-Age` (сессионная; сроки — на сервере). Контракт меняет имя в `securitySchemes.session`.
- Один origin: статика админки и `/v1` за одним хостом. В dev — прокси Vite (`WF_ADMIN_API_ORIGIN`),
  `ADMIN_ORIGIN=http://127.0.0.1:5173`. `VITE_ADMIN_API_URL` удаляется — другой origin запрещён.
- Новые переменные `ADMIN_ORIGIN`, `ADMIN_SECRET_KEYS`, `ADMIN_IP_ALLOWLIST` заносятся сразу в
  конфиг, `.env.example` и `.env` (память: ключи окружения). `./task setup` генерирует
  `ADMIN_SECRET_KEYS` локально.

### 5.3. Журнал действий

`GET /v1/audit?actor_staff_id=&action_prefix=&object_type=&object_id=&from=&to=&cursor=` (тег
`platform`, право `audit.log.read`):

- модератор видит записи своих городов (`scope_city_id`) и свои собственные;
- `before`/`after` уже маскированы при записи (`platform/audit/mask.go`);
- сортировка `created_at DESC, id`.

Новый запрос `ListAudit` в `platform/audit`. `scope_city_id` проставляют модули: geo — город
операции, staff — NULL.

### 5.4. Остатки фундамента, закрываемые срезом

- Principal кладётся в контекст (`auth.With`), иначе идемпотентность молча выключена.
- Отказ сессии считается в лимите (M-3).
- Потолки параметров argon2 из хэша и минимум соли, `DefaultParams` → константы.
- NFKC и длина пароля на входе.
- Нормализация локали письма.
- Нестроковый `x-rate-limit` — ошибка, а не тихий `default`. Страж ветки `UnknownClasses`.
- Маскирование `client_ip` в аудите.

## 6. geo 2/2 — бэкенд админки

Ручки — по спеке geo §5 с изменениями ниже. Код — `backend/internal/geo/admin/`, сценарии —
`internal/app`. Каждая мутация пишет аудит (`scope_city_id` — город) и событие в той же транзакции.

### 6.1. Ручки

| Ручка | Право | Изменения к спеке geo |
|---|---|---|
| `GET /v1/geo/places?q=&country=&cursor=` | `geo.places.search` | — |
| `GET /v1/geo/cities?status=&country=&region_id=&q=&cursor=`, `GET …/{cityId}` | `geo.cities.read` | модератор — только свои |
| `POST /v1/geo/cities` | `geo.cities.activate` | — |
| `PATCH /v1/geo/cities/{cityId}` | статус и slug — `geo.cities.manage`; `names` — `geo.cities.rename` | поле без права → `403 auth.forbidden` |
| `POST /v1/geo/cities/{cityId}/link {geoname_id}` | `geo.cities.manage` | **новая**: ручная привязка неоднозначного города из отчёта сверки |
| `GET/POST …/{cityId}/districts`, `PATCH …/districts/{districtId}` | `geo.districts.manage` | — |
| `GET /v1/geo/countries`, `POST`, `PATCH /{code}` | read / `geo.countries.manage` | валюта — по списку ISO 4217, `country_code` — ISO 3166-1 alpha-2 |
| `GET /v1/geo/countries/{code}/regions` | `geo.cities.read` | **новая**: выбор региона при активации |
| `POST /v1/geo/imports {country}`, `GET /v1/geo/imports`, `GET …/{importId}` | `geo.imports.run` / `…read` | **новая** `GET {id}` — отчёт сверки (ambiguous, conflict) |
| `GET /v1/app/versions`, `PUT /v1/app/versions/{platform}` | `platform.versions.read` / `…manage` | **новая** `GET` |
| `GET /v1/flags`, `PATCH /v1/flags/{key} {enabled_globally?, enabled_city_ids?}` | `platform.flags.read` / `…manage` | **новые**; запись в `platform/flags`, событие `platform.flag_changed` |

### 6.2. Импорт из админки

- admin-api получает River-клиент только на вставку, по образцу `cmd/worker`.
- Ручка отвечает `202 {import_id, job_id}`. Строка `running` уже есть или River отказал по уникальности
  → `409 geo.import_in_progress`.
- Страна проверяется по `^[A-Z]{2}$`, а `jobs.NewImportArgs` нормализует регистр.
- Задача `geo.import` на `ErrImportInProgress` (параллельный импорт оператора) уходит в
  `river.JobSnooze(1 мин)`, а не в discarded.
- Аудит постановки — от имени сотрудника (`started_by` → `actor_staff_id`). В
  `geonames_imports.started_by` (uuid без FK, «сотрудник; NULL — оператор») пишется id из
  `staff_accounts`; `writeAudit` импорта переходит с `actor_user_id` на `actor_staff_id`.

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
6. `store_url`: запрет пробелов и управляющих символов в коде и в CHECK. В контракте — «`https://` в
   нижнем регистре».
7. Страж кодов ошибок admin: каждый код из `codes.go` модулей есть в контракте admin и в
   `apps/admin/messages`.
8. Удаление `/v1/health` из admin:
   - запись в `contracts/oasdiff-err-ignore-admin.txt`;
   - правка `contracts/test/{bundle,contracts}.test.mjs`;
   - `HealthStatus` в `apps/admin` заменяет связь через `GET /v1/staff/me`.
9. Флак тестов под нагрузкой: ограничить `-p` в `backend/Taskfile.yml`, один клон базы на пакет в
   handler-тестах.

### 6.4. События и коды

- События — по спеке geo §6: `geo.city_activated`, `geo.city_status_changed`, `geo.city_renamed`,
  `geo.city_slug_changed`, `geo.city_linked`, `geo.district_created|updated|archived`,
  `geo.country_updated`, `platform.app_versions_changed`, плюс `platform.flag_changed`.
- Коды — по спеке geo §8, без `geo.forbidden` и `platform.forbidden` (§2.4).

## 7. Ошибки

Новые коды среза. Тексты — в `apps/admin/messages/{ru,en}.json` (`errors.<код>`), каждый код — в
контракте admin.

| Код | HTTP | Когда |
|---|---|---|
| `auth.forbidden` | 403 | нет права или города вне скоупа |
| `auth.csrf_failed` | 403 | нет `Origin`/`X-WF-Admin` или чужой origin |
| `auth.ip_not_allowed` | 403 | IP вне аллоулиста |
| `staff.invalid_credentials` | 400 | вход: почта или пароль не подошли |
| `staff.locked` | 423 | учётка закрыта, `retry_after` |
| `staff.mfa_invalid` | 400 | код второго фактора не подошёл, `attempts_left` |
| `staff.reauth_required` | 401 | бездействие или смена IP — нужен TOTP |
| `staff.setup_required` | 403 | стадия `setup`, операция недоступна |
| `staff.invite_invalid` | 400 | код приглашения, почта или срок |
| `staff.invite_resend_too_soon` | 429 | повторная отправка админом раньше 60 с |
| `staff.email_taken` | 409 | приглашение на почту активной учётки |
| `staff.not_found` | 404 | нет сотрудника |
| `staff.last_owner` | 409 | снять последнего владельца |
| `staff.self_action` | 409 | действие управления над собой |
| `staff.approval_not_found` | 404 | — |
| `staff.approval_self` | 409 | подтвердить свой запрос |
| `staff.approval_stale` | 409 | цель изменилась после запроса |
| `staff.approval_closed` | 409 | запрос уже решён, отменён или просрочен |

## 8. Контракт

- Модули: `contracts/openapi/admin/{staff,geo,platform}/`, бандл `admin.yaml`. Полное описание и
  примеры всего — `.claude/rules/contracts.md`.
- Новые расширения: `x-permission` (обязательно на каждой операции, значения — каталог прав, `anonymous`,
  `session`, `self`).
- `x-anonymous-mutations`: login, mfa, invite/accept, invite/resend. `auth/mfa` не анонимна: требует
  сессию `mfa_pending`, но без права.
- Операции с `dual` документируют ответ `202` со схемой `Approval`.
- Страж в `contracts/src`: у каждой операции admin есть `x-permission`, и его значение есть в каталоге.
  Каталог выгружается из Go в `contracts/openapi/admin/permissions.json` через `./task gen`.

## 9. Админка (`apps/admin`)

### 9.1. Технические решения

| Что | Решение |
|---|---|
| Роутер | TanStack Router, маршруты кодом; фильтры списков — типизированные search-параметры |
| Данные | TanStack Query + openapi-fetch (уже есть); middleware клиента добавляет `X-WF-Admin` и `Idempotency-Key` (один ключ на попытку мутации, повтор — тот же) |
| Стили | CSS Modules поверх `--wf-*`. Недостающие цвета макетов (фоны чипов и статусов) — новые токены в `contracts/tokens/tokens.json` |
| Шрифты | Oswald и Manrope через `@fontsource`, без внешних запросов |
| Формы | react-hook-form; `validation.failed` раскладывается по полям |
| QR | SVG на клиенте (библиотека — первым шагом плана); секрет не уходит наружу |
| Тема | тёмная (токены только тёмные) |
| Локали | ru и en, переключатель в меню профиля, выбор сохраняется в `staff_accounts.locale` |

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
  - нет прав — пункт меню виден, раздел закрыт;
  - 429 — таймер по `Retry-After`;
  - «только с компьютера»;
  - «отправлено на подтверждение» — после `202`.

### 9.3. Сессия на клиенте

- При загрузке `GET /v1/staff/me`. Нет сессии — экран входа с возвратом на исходный адрес. Стадия
  `setup` — только настройка 2FA.
- `401 staff.reauth_required` — модалка ввода TOTP поверх текущей страницы. Состояние форм не
  теряется, после разблокировки исходный запрос повторяется.
- `401 auth.unauthenticated` — экран входа. Черновик формы сохраняется в `sessionStorage` по ключу
  маршрута.
- Меню и кнопки строятся по `permissions` из `me`. Счётчик «Подтверждения» — из
  `GET /v1/staff/approvals?box=incoming&status=pending`, обновляется раз в минуту и при фокусе окна.

### 9.4. Экраны

Нарисованы в макете — делаем по нему. Дорисованы в коде — помечены «*», список уходит в
`design/README.md`.

1. **Вход** (07, 08) — шаг 1: почта и пароль (поле пароля — *); шаг 2: 6 ячеек или «Использовать
   резервный код» (*). Ошибка «Код не подошёл. Осталось N попыток…». Подпись «вход только с рабочей
   почты и вторым фактором, каждый вход пишется в журнал».
2. **Принятие приглашения** (*) — `/accept?email=`: почта, пароль дважды с подсказкой требований, код
   из письма, «Отправить код ещё раз» с отсчётом 60 с.
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
   - матрица прав из API, «Кто имеет доступ», блок «Правило двух рук»;
   - «ПРИГЛАСИТЬ АДМИНИСТРАТОРА» → форма в панели (*): почта, имя, роль, города модератора;
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
   раз в 5 с, отчёт сверки со ссылками «Привязать».
10. **Страны** (*): список, правка правил в панели, включение страны.
11. **Версии приложения** (*): iOS и Android — минимальная, рекомендуемая, ссылка на стор.
12. **Флаги функций** (07): матрица «функция × город» с переключателями, глобальный переключатель.

Меню — по макету 07: «Безопасность» (Журнал, Роли и доступ, Подтверждения, Мой доступ), «Справочники»
(Города и запуск, Страны, Импорт, Версии, Флаги). Разделы будущих модулей не показываются, пока модуль
не построен. Стартовая страница — первый разрешённый раздел. На телефоне (08) закрыты города, импорт,
страны, версии, флаги, роли и «Мой доступ» — тост «Этот раздел открывается только с компьютера».
Вход, принятие приглашения, подтверждения и журнал работают и на телефоне.

### 9.5. Структура

`src/app/` (роутер, провайдеры, guard'ы), `src/ui/`, `src/features/{auth,me,staff,approvals,audit,
geo,versions,flags}/`, `src/api/`. Правила — новое `.claude/rules/admin.md`: компоненты только из
`src/ui`, права — из `me.permissions`, тексты — только локали.

## 10. Структура кода бэкенда

- `backend/internal/staff/` — модуль (§4).
- `backend/internal/platform/auth/` — `Staff`, `StaffSessions`, `Subject`.
- `backend/internal/httpapi/admin/` — конвейер §5.1, middleware CSRF, аллоулиста и прав.
- `backend/internal/platform/audit/` — `ListAudit`, `actor_staff_id`, `scope_city_id`.
- `backend/internal/platform/flags/` — запись.
- `backend/cmd/admin-api/` — подкоманда `staff bootstrap`, River insert-клиент.
- `backend/cmd/worker/` — `staff.cleanup`, `staff.mfa_rekey`, `worker staff rekey`.

## 11. Тесты и стражи (TDD, страж проверяется подсадкой бага)

- **Права**: тест на каждую операцию admin по субъектам «владелец, админ, модератор своего города,
  модератор чужого, поддержка, без сессии, стадия `setup`». Таблица ожиданий выводится из матрицы,
  а не пишется руками.
- **Контракт**: `x-permission` есть у каждой операции и существует в каталоге; `docs/05` совпадает с
  матрицей (`gen:check`).
- **БД**: `api` не читает `staff_*`; `worker` удаляет только три таблицы; CHECK акторов `audit_log`;
  модератор без городов невозможен.
- **Вход**: одинаковые ответы и время для «нет почты / неверный пароль / не active / без 2FA»; повтор
  TOTP запрещён (`last_used_step`); 2FA не привязать без приглашения; `setup` без `ack` не пускает
  дальше; блокировка после 5 неудач и письмо; `423` только при верном пароле.
- **Сессия**: абсолютный срок, 20 минут бездействия, смена IP → `reauth_required`; CSRF без заголовка
  и с чужим `Origin`; отказ сессии считается в лимите.
- **Вдвоём**: свой запрос не подтвердить; устаревшая цель → `stale`; исполнение атомарно с аудитом и
  событием; права подтверждающего со скоупом.
- **geo**: по §6.3 и тесты прав.
- **Админка**: компоненты и экраны — Vitest и MSW по типам контракта; ключи локалей ru и en; каждый
  код ошибки имеет текст.
- **Живой e2e** (Playwright, `./task admin:e2e`) поверх настоящих admin-api, worker, Postgres и
  Mailpit, TOTP генерируется в тесте:
  1. bootstrap владельца → письмо;
  2. принятие → 2FA → резервные коды;
  3. выход и вход;
  4. приглашение админа → подтверждение вторым владельцем → принятие;
  5. активация города, импорт страны;
  6. запись в журнале.

  В CI — с Postgres CI и Mailpit (проверить, §14).

## 12. Этапы

Спека одна, планов три. Каждый исполняется и ревьюится отдельно (субагенты по
`.claude/agents/`).

| План | Состав | Что видно вживую |
|---|---|---|
| **A. Ролевая модель и вход** | 1) `docs/05-ролевая-модель.md` и **отдельное ревью модели** (opus и пользователь) до кода. 2) Платформа: миграции §3.2, `auth.Staff`, конвейер §5, контракт `x-permission`. 3) Параллельно: модуль `staff` ∥ каркас админки, набор компонентов, экраны 1–7 на MSW. 4) Сборка вживую и e2e | вход, 2FA, приглашения, роли, подтверждения, журнал |
| **B. geo 2/2** | 1) Хвосты §6.3. 2) Ручки §6.1–6.2 ∥ экраны 8–12. 3) e2e | города, импорт, страны, версии, флаги |
| **C. Render** | `wf-admin-api` (тот же образ, `CMD admin-api`); статика админки на одном origin с API — rewrite `/v1` на Render (проверить); `wf-worker`; роль БД `admin` на Neon; SMTP для писем; `staff bootstrap` первого владельца; `docs/deploy-dev.md` | админка на стенде |

Решения плана C за пользователем: background worker на Render платный, нужен SMTP-провайдер.

## 13. Обновление документов (в коммитах планов)

- Спека бэкенда:
  - §4.1 — модуль `staff` и его таблицы;
  - §4.5 — роли игроков;
  - §10 — сотрудники отдельно, ссылка на эту спеку и `docs/05`, сроки сессии, правило двух рук;
  - §11 — схема;
  - §13 — порядок: `staff` выполнен до `identity`.
- Спека geo:
  - §5.1 — шов сессии по §5.1 этой спеки;
  - §5.2 — новые ручки, `auth.forbidden` вместо `geo.forbidden`/`platform.forbidden`;
  - §3.8 — `DELETE` на `city_slug_history`;
  - §8 — коды.
- `docs/01-домен-и-правила.md` — таблица ролей: модератор и админ — сотрудники, капитан — допуск и
  команда.
- `docs/02-архитектура.md` — раздел «Админка»: сроки сессии, правило двух рук, IP.
- `CLAUDE.md` — правило 4.
- Спека структуры репо — cookie и origin админки.
- `.claude/rules/` — `admin.md` (новое), правило бэкенда для модуля `staff`, `contracts.md`
  (`x-permission`).
- `design/README.md` — список дорисованных в коде экранов.
- `docs/deploy-dev.md` — план C, ротация `ADMIN_SECRET_KEYS`.
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
- Rewrite `/v1` статического сайта Render на внешний сервис с сохранением cookie (план C).
- Mailpit в CI для живого e2e админки.
