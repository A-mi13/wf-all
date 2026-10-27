---
paths:
  - "backend/internal/platform/**"
  - "backend/cmd/worker/**"
  - "backend/internal/*/subscribers/**"
  - "backend/internal/*/jobs/**"
  - "backend/internal/*/internal/app/**"
---

# Платформа: транзакции, события, очереди, аудит

- Транзакция платформы — `db.InTx(ctx, pool, fn)`: всегда новая, кладёт tx в ctx; оттуда её читают только `events.Publish`, `audit.Write`. Вне `InTx` они возвращают `db.ErrNoTx`. Идемпотентность tx из ctx не читает — получает аргументом хука (ниже).
- Идемпотентность (`idempotency.Middleware`, спека §6.4) вставляет ключ хуком `db.WithTxHook` первой операцией транзакции ручки; `after` хука узнаёт исход (коммит/откат), ответ сохраняется только при коммите. Ручка с `Idempotency-Key` — ровно одна закоммиченная `db.InTx` (повтор после отката можно, вторая после коммита или вложенная — 500); успех вернуть только после коммита, иначе 500 «без транзакции». Чтения до неё — без транзакции.
  Ключ — `(user_id, key)`, где `user_id` — id субъекта `auth.SubjectFrom` (игрок или сотрудник; колонки вида нет —
  UUIDv7 не совпадают). Таблица — параметр `Config.Table` (`idempotency.Table`): `Players` (нулевое значение) —
  `idempotency_keys` у public API, `Staff` — `staff_idempotency_keys` у admin-api (ответы сотрудников с ПД; права на неё
  только у роли `admin`); неизвестная — паника при сборке. Запросы — пары `X`/`StaffX` в `queries/idempotency.sql`,
  равные текстом после замены имени таблицы (страж `TestStaffQueriesMirrorPublic`). Чистка — `idempotency.DeleteExpired(ctx, db, таблица)` пулом
  роли с правами на таблицу: `Players` — воркер (`CleanupWorker`, River, раз в час), `Staff` — тикер admin-api (шаг
  `staff/jobs.Cleaner.Tick`). Операция с `x-idempotency: no-store` (ответ с секретами) хранит только хэш и статус: повтор —
  `409 idempotency.replay_unavailable`; `Cache-Control: no-store` ставит хендлер.
- События — только id и непрофильные значения, без персональных данных; у агрегата `version`, подписчик в режиме `LatestState` отбрасывает устаревшие версии (§6.5).
- Подписчик события — `events.Subscription` с явным `Delivery` (`EveryEvent` | `LatestState`), регистрируется в `subscriptions()` в `cmd/worker/main.go`; обработчик идемпотентен по `event_inbox` сам собой, пишет только в своей транзакции `tx`. Доставка на подписку, которой нет в реестре этого воркера (воркер другого релиза при выкатке), — обычная ошибка с повтором, а затем «мёртвая» задача (видна, переигрывается), не отмена; отменяется только задача на событие, уже удалённое чисткой. Переигровка — `worker events replay --type T --subscriber S --since RFC3339`.
  Импорт городов оператором — `worker geo import --country RU`: синхронно в процессе, без задачи River, под ролью `worker`; задача `geo.import` ставится только через `jobs.NewImportArgs`.
- Задача River объявляет очередь из `platform/queue` (`events`, `lifecycle`, `notify`, `mail`, `stats`, `media`, `maintenance`) в `InsertOpts`; очереди `default` нет. Аргументы — с полем версии `V`, без ПД, уникальность — `UniqueOpts`. River в воркере работает на неотменяемом ctx: по SIGTERM сначала останавливается relay, затем `queue.Stop` — мягкая остановка (25 с) и, не уложившись, `StopAndCancel` (5 с): сумма укладывается в 30 с между SIGTERM и SIGKILL. `WORKER_RELAY_BATCH` ≥ 1 и `WORKER_RELAY_POLL` > 0 проверяются на старте воркера.
- Периодика платформы — только `platform/periodic.Jobs()` (новый срез и новые задачи на вызов): её подключают и воркер, и
  admin-api — лидер River один на схему и ставит периодику по своему списку (P27), исполняет воркер (River не проверяет
  исполнителя у периодической вставки; страж `TestLeaderWithoutWorkersEnqueuesPlatformJobs` под ролью `admin`). Новая
  чистка платформы — в `Jobs()`, не в `cmd/worker`. Чистки, которым нужны права `admin` (`staff_*`, `approvals`,
  `staff_idempotency_keys`), — не River, а тикер в процессе admin-api (`staff/jobs.Cleaner`: `Tick` = `App.Cleanup` +
  `ExpireDue` + `PurgeDecided` + `idempotency.DeleteExpired(…, Staff)`).
  Исполняющий клиент River — только `queue.ClientConfig(очереди, исполнители, периодика, журнал)` у обоих бинарников
  (`riverConfig` в `cmd/worker` и `cmd/admin-api`), клиент только для вставки — `queue.InsertConfig(журнал)`; свой
  `river.Config{…}` вне `platform/queue` (литерал, `new`, переменная типа) не собирать — страж
  `archtest.TestRiverConfigOnlyInQueue` (тесты `*_test.go` — исключение): общая часть —
  `queue.MaintenanceConfig` (сроки чистки выполненных, отменённых и «мёртвых» задач, порог рескьюера — умолчания River
  v0.47–v0.49, заданные явно), потому что обслуживание делает лидер схемы, а лидером может стать любой из двух. Стражи —
  `TestMaintenanceConfig`, `TestMaintenanceDiff` (`queue`), `TestWorkerRiverMaintenance`, `TestAdminRiverMaintenance`
  (все поля `river.Config`, кроме `Queues`, `Workers`, `PeriodicJobs`, `Logger`, равны у обоих). При подъёме River —
  сверить умолчания с константами `queue/maintenance.go`.
- HTTP-бинарник с фоном — `server.RunService(ctx, name, cfg, logOut, build)`: `build(log, pool)` → `Service{Handler,
  Background}`. Фон стартует после сборки и открытия порта на ctx без отмены сервиса; остановка — дренаж HTTP, затем отмена
  фона и ожидание (фон сам мягко останавливает River — `queue.Stop`), последним — закрытие пула. Фон вернулся до отмены
  (ошибка или nil) — HTTP гасится, `RunService` возвращает ошибку (стражи `service_test.go`). `RunHTTP` — обёртка без фона
  (`cmd/api`). У admin-api: HTTP-дренаж `ADMIN_SHUTDOWN_TIMEOUT` (10 с) + River мягко 12 с + жёстко 5 с = 27 с (запас до SIGKILL 30 с).
- Виды задач исполняющих бинарников — списки `queue.WorkerKinds()` и `queue.AdminKinds()` (имя и срок исполнителя).
  Рескьюер лидера River (задача `running` дольше часа) ищет исполнителя вида в СВОЁМ клиенте и без него выбрасывает
  задачу (`discarded`), поэтому каждый бинарник регистрирует виды другого заглушками `queue.AddForeign` (срок — как у
  настоящего исполнителя; исполнять заглушка отказывается). Новый вид — строка в своём списке, иначе падают
  `TestWorkerKinds` / `TestAdminKinds` (реестр сверяется со списком через `testkit/rivertest.Kinds`, сроки — с
  исполнителями); стражи рескьюера — `TestRescuerKeepsAdminJobs`, `TestRescuerKeepsWorkerJobs`, `TestForeignTimeoutRespected`.
  Очередь каждого вида — среди очередей своего бинарника и не у чужого (`TestWorkerKindsQueues`, `TestAdminKindsQueues`):
  иначе задачу взяла бы заглушка, а её ошибка сожгла бы попытку. Выкатывать worker и admin-api вместе.
- Аудит — `audit.Write(ctx, entry)` в транзакции действия; чтение чувствительного — `audit.WriteRead`. ПД в `Before`/`After` маскирует `audit.Mask` по словам ключа (camelCase/PascalCase делятся на слова: `contactEmail`, `contact_email`, `ContactEmail` — одно и то же); не маскируется ключ, оканчивающийся на `at`/`count`/`left`/`verified`/`enabled`/`required`. Маскирование по ключу, а не по содержимому: свободный текст с ПД (комментарии, описания) в `Before`/`After` класть нельзя. Новые чувствительные слова — в списки слов и пар в `audit/mask.go`.
  Автор — ровно один: `ActorUserID` (игрок) или `ActorStaffID` (сотрудник), без автора — `ActorRole`
  `system|operator|anonymous`; `ActorLabel` — снимок отображаемого имени (журнал показывает его); `ScopeCityID` —
  город записи (по нему модератор видит журнал своих городов). Два автора — `ErrInvalidEntry`. Журнал читается
  только `audit.List` и `audit.Get` (фильтры, скоуп модератора `CityIDs`+`OwnStaffID`, курсор `audit.log(created_at,id)`).
  `ActionPrefix` — по сегментам, без шаблонов LIKE: на «.» или «_» — начало строки (`staff.`, `platform.approval_`);
  иначе — само действие и его подвиды через точку (`staff.login` не ловит `staff.login_failed`).
  `CityIDs == nil` — без ограничения городом (глобальная роль); модератору вызывающий ВСЕГДА передаёт не-nil срез
  (копия `append(make([]uuid.UUID, 0, len(s.CityIDs)), s.CityIDs...)`), скоуп выбирается по `GrantScoped`, а не по
  `len`: модератор без городов видит только свои записи, а не весь журнал. IP
  наружу — подсетью `audit.MaskIP` (/24, /48). Слово `code` в ключе — секрет, кроме кода-справочника
  (`country_code`, `error_code`: список `publicCode`).
  Роль обязательна всегда; `insert` повторяет CHECK `audit_log` из 0020 (роль из списка, служебная ⇔ нет автора, у
  сотрудника — роль сотрудника и `ActorLabel` 1–120 символов) и отвергает нарушение `ErrInvalidEntry` до SQL: поменял
  CHECK — поменяй списки ролей в `audit/audit.go`; расхождение списков и CHECK ловит `TestActorRolesMatchCheck`
  (`audit/roles_test.go`). `Entry.ID` задан — запись получает его (ссылка из письма); одна
  запись — `audit.Get` (поля и маска IP как в `List`, нет — `ok=false`).

## Край платформы (план 3/3)

- Текущее время — только `clock.Clock` (в тестах `clocktest.Fake`); длительности и таймауты —
  `start := time.Now()` … `time.Since(start)` (монотонные часы).
- Ошибка, которую должен увидеть клиент (лимит, антибот, идемпотентность, флаг), — `httpx.ProblemError`
  (`httpx.NewError(статус, httpx.Code…)`); код строкой — падение стража `TestProblemCodesAreConstants`.
  Остаток попыток (неверный код или пароль) — `.WithAttemptsLeft(n)`: новая ошибка, поле `attempts_left` (0 тоже
  уходит); `Retry-After` — только заголовком (`Problem.RetryAfter`, в теле `retry_after` нет).
- Почта — только задача `mail.SendArgs{V: 1, Mail: "<модуль>.<письмо>", Ref: id}` (на другую версию воркер
  вернёт ошибку, задача уйдёт в повтор и после `MaxAttempts` станет «мёртвой»); адрес находит
  `mail.Composer` модуля, текст — `i18n.Catalog.Text` (`backend/locales`, ключи ru = en). `mail.SMTP.Send` закрывает
  соединение по отмене ctx (остановка River, срок задачи): висящий SMTP не держит остановку до своего таймаута 20 с
  (`TestSMTPSendHonorsCancel`). Письма сотрудников — не `mail.SendArgs`, а `staff.NewMailArgs` на очереди
  `queue.Staff`: их исполняет River-клиент admin-api (SMTP `ADMIN_MAIL_*`), не воркер (`backend-staff.md`, «Фон»).
- Пароли — `password.Check` + `password.Hasher` (argon2id под семафором; `password.DefaultParams()` — минимум OWASP);
  «нет почты» — `VerifyDummy`. Пароль приводится к NFKC; длиннее `password.MaxInputBytes` (1024 байта) —
  `ErrTooLong` до хэширования; хэш из базы с параметрами выше потолков (m 256 МиБ, t 10, p 16) или солью < 16 байт —
  `ErrMalformed`.
- Секреты в базе — `keys.Ring` из `*_SECRET_KEYS`: `Seal(данные, aad = id строки)` → шифротекст и `key_id` (хранить
  рядом), `Open(ct, key_id, aad)`; пустой aad — `keys.ErrNoAAD`. Хэш-коды — `MAC(key_id, метка, сообщение)` (своя
  метка на назначение, сравнение — `hmac.Equal`). Новые строки — `CurrentID()`; старый ключ убирают из окружения,
  когда счёт строк с этим `key_id` = 0; `Has` — есть ли ключ в кольце. Перешифровка секретов сотрудников и
  двухфазная ротация `ADMIN_STAFF_SECRET_KEYS` — `admin-api staff rekey` (`docs/deploy-dev.md`, раздел «admin-api»).
- Когда требовать PoW — `risk.Assessor`; требование — `humancheck.Require` (403 с задачей).
- Тесты механизмов с SQL — под ролью прода: `dbtest.NewPoolsAs(t, "api"|"worker"|"admin")`.
- Пагинация — пакет `page`, свой курсор модули не пишут: v1 `Encode/Decode` — keyset по `(created_at, id)`;
  v2 `EncodeKeyset/DecodeKeyset` — произвольный порядок: `page.Keyset{Set: "<модуль>.<список>(<поле>,…)", Values}` и
  `Kind`ы полей (`int64 | string | time.Time | uuid.UUID`, строго по Kinds, иначе паника); отпечаток Set и Kinds
  отсекает курсор чужого списка и старого набора полей. Негодный курсор — `page.ErrBadCursor` (400 по `query.cursor`);
  `page.Limit`: 20 по умолчанию, максимум 100. Поменял поля сортировки или их порядок — поменяй Set и Kinds.
- Язык ответа по `Accept-Language` — только `i18n.Negotiate(header, supported)` (q-веса, `*` пропускается, регион
  отбрасывается, мусорная запись пропускается, заголовок длиннее 1 КБ — языка нет; `supported` — первичные
  подтеги (`ru`, `en`)); свой разбор заголовка модули не пишут. «Нет языка» (`ok=false`) решает вызывающий: `i18n.Default` не подставляется.
- Версии нативного приложения — пакет `appversion`: чтение `Reader.Get` (нет строки — `ok=false`, не ошибка),
  сравнение только `appversion.Compare` (semver `MAJOR.MINOR.PATCH`, строкой версии не сравнивать), перед записью —
  `appversion.Validate` (формат, `min ≤ recommended`, `https` и точный хост стора платформы); нарушения —
  `ValidationError` с полями контракта, ручка переводит их в 400 `validation.failed`.
- `*auth.Principal` из `auth.From(ctx)` — общий для всех запросов сессии (кэш отдаёт один указатель): срезы
  `Roles` и `Restrictions` не изменять (append, sort, запись по индексу).
- Сотрудник admin-api — `*auth.Staff` из `auth.StaffFrom(ctx)` (`Grants`, `CityIDs` только читать). Право —
  `Decide(perm, cityID)`: `Allow` — исполнить, `NeedsApproval` — только запросом подтверждения вдвоём, `Deny` —
  403; `Can` = «можно сразу» (для `Dual` — false). Вне стадии `active` или под `Reauth` — `Deny` на всё. Модули
  проверяют права так, не импортируя `staff`. Механизмы, которым всё равно, игрок или сотрудник (идемпотентность,
  лимиты), берут `auth.SubjectFrom`.
- Ключ лимита — только `ratelimit.IPKey` / `SubjectKey` / `SessionDeviceKey` / `BFFDeviceKey` / `EmailKey`
  (`<вид>:<класс>:<значение>`; виды `ip`, `user`, `staff`, `device`, `email`): счётчик, который сценарий ведёт
  сам через `Limiter`, попадает в то же пространство, что и `ratelimit.Middleware`.
- В логи — только строки и ошибки: `logx` маскирует ПД (почту, телефоны) только в них; структуры, `Stringer`
  и срезы не маскируются — с ПД их в лог не класть.
- Права сотрудников — каталог и матрица `platform/perm` (`docs/05-ролевая-модель.md`): ключ
  `<модуль>.<ресурс>.<действие>`, значение `auth.Grant` (`Deny`, `Allow`, `Scoped`, `Dual`), `Mutating` — право
  меняет состояние. Модуль добавляет свои строки в `perm/catalog.go` своим коммитом (до спеки модуля —
  `draft`), затем `./task gen` (таблица docs/05 и `contracts/openapi/admin/permissions.json`; расхождение ловят
  `gen:check` и тесты `perm`, они же — инварианты матрицы). Права роли — `perm.Grants(role)`: копия, без черновых и
  `Deny`. Ручки и сценарии проверяют право, а не роль.
- Право над объектом города (город, район, запись журнала, заявка, жалоба) — обёртка `city(…)` в `perm/catalog.go`
  (`Permission.CityBound`): `scoped` бывает только у таких прав (тест `TestCityBound` перечисляет их явно). `scoped`
  без `{cityId}` в пути конвейер пропускает — список фильтрует по `auth.Staff.CityIDs`, изменение проверяет город
  цели через `Decide(ключ, cityID)`.

## Подтверждение вдвоём (`platform/approvals`, спека сотрудников §4.8, D17, P13)

- **Регистрация** — при сборке admin-api, до старта сервера: `reg.Register(approvals.Action{Name, Permission,
  Permit, Exec, Target})` (имя `<модуль>.<действие>`, право из каталога `perm` и не черновое, дубль — паника),
  `reg.SetInitiatorCheck` (один раз, регистрирует staff), затем `reg.Validate()`. Без проверки инициатора
  `Approve` не исполняет ничего (ошибка, запрос остаётся `pending`). `Actions()` — имена по алфавиту (подписи D21).
- **Право запроса** — `Permit(payload)` (nil → `Permission`) вычисляется в `Request` и хранится в
  `approvals.permission`; подтверждающий сверяется с ним, а не с пересчётом по payload или текущей роли инициатора.
  `Request` — в транзакции модуля (`tx` аргументом, ctx из её `db.InTx`): payload — JSON-объект с целым `v ≥ 1`,
  инициатор `Decide(право, город цели) != Deny`, иначе `ErrForbidden`; аудит `platform.approval_requested` без
  payload (в нём свободный текст).
- **Метаданные:** `Request`, `Approve`, `Reject`, `Cancel` принимают `approvals.Meta{IP, UserAgent}` (модуль — из
  своего `staff.Meta`, ручки 10b — из запроса): IP и UA человека — в аудит запроса и решения; у решений системы
  (просрочка, гашение) — пусто.
- **Кто решает:** сессия `active` без `Reauth`; первым — `Decide(сохранённое право, scope_city_id) != Deny`
  (`ErrForbidden` до любых других ответов: без права не видно состояние запроса и клик ничего не меняет — даже
  просроченный не переводится в `expired`); затем не инициатор и не цель (`ErrSelf`, CHECK `approvals_two_hands`);
  затем `pending` и срок. Отмена — только инициатор; чужой запрос — `ErrNotFound` (существование не раскрывается).
  Входящие (`List` `incoming`) — те же правила в SQL; ожидающий с вышедшим сроком в списках — `expired` (в фильтре
  `pending` и счётчике меню его нет).
- **`InitiatorCheck(ctx, tx, approvals.Approval)`** получает запрос целиком (право, город, payload, `RequestedAt`).
  Контракт: отказать (`Fail`/`ErrStale`), если инициатор не `active`, без права запроса в городе запроса, **или с
  момента `requested_at` выключался, сбрасывался или менял роль — даже если снова активен с тем же правом** (staff
  кладёт версию учётки инициатора в payload при `Request` и сравнивает с текущей). Платформа о версиях учёток не
  знает; на этот контракт опирается `CancelByInitiator` (ниже).
- **Одна транзакция на решение (D8):** строка `FOR UPDATE`; срок вышел (`now >= expires_at`) — `expired` от
  `system` коммитится, наружу `ErrClosed`; проверка инициатора отказала (`Fail`/`ErrStale`) — `failed` с
  `error = initiator_changed` коммитится, наружу `ErrStale`. Под `Idempotency-Key` это та самая единственная
  закоммиченная `db.InTx`: 409 сохраняется в ключ.
- **Исполнитель — под savepoint:** пишет только свои таблицы, аудит и события — через ctx. Бизнес-отказ —
  `return approvals.Fail(err)` (или `ErrStale`): savepoint откатывает все его изменения (и аудит), `failed` с
  кодом ошибки (`ProblemError.Code` / `Code()`; ошибка без кода — общий `execution_failed`, её текст в строку и
  аудит не пишется: в нём бывают ПД) коммитится, наружу — исходная `err`
  (`errors.Is(err, staff.ErrLastOwner)`). Ошибка без `Fail` или паника — откат всего, запрос `pending` (500).
  `approvals.ErrInitiatorChanged` (обёртка `ErrStale`) — отказ исполнителя из-за изменившегося инициатора:
  наружу `ErrStale`, у `failed` — `error = initiator_changed`; голый `ErrStale` — `platform.approval_stale`.
- **Каждое решение** — аудит `platform.approval_<status>` (`executed|rejected|cancelled|expired|failed`; актор —
  решивший, у просрочки и гашения — `system`, комментарий — в `Reason`) и событие `platform.approval_decided`
  (агрегат `approval`, версия 1) в той же транзакции.
- **`CancelByInitiator(ctx, tx, staffID)`** — staff зовёт при выключении, сбросе доступа и смене роли
  инициатора, в своей транзакции: его `pending` → `cancelled` от `system`, `initiator_changed`. Запросы о нём не
  гасятся (их отсекает версия цели в исполнителе). `SKIP LOCKED`: строку под замком параллельного решения гашение
  пропускает — ожидание дало бы взаимную блокировку «замок учётки → гашение» ∥ «замок запроса → проверка
  инициатора». Если то решение откатится (чужой approve → `ErrSelf`, чужой cancel → 404, 500), запрос останется
  `pending` — его отсечёт `InitiatorCheck` при следующем подтверждении по контракту выше (страж
  `TestSkippedCancelFailsAfterReactivation`). Порядок замков: строка `approvals` раньше строк модуля.
- **Чистка** — тикер admin-api (`staff/jobs.Cleaner.Tick`, раз в час): `ExpireDue` (порции `SKIP LOCKED`, своя транзакция на порцию; у истёкшего
  `decided_at = expires_at` — и у тика, и у клика approve/reject/cancel, как в списках) и
  `PurgeDecided(ctx, approvals.DecidedRetention)` (решённые старше 180 суток; `pending` не трогает).
