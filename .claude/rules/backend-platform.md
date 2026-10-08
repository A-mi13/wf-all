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
  UUIDv7 не совпадают). Операция с `x-idempotency: no-store` (ответ с секретами) хранит только хэш и статус: повтор —
  `409 idempotency.replay_unavailable`; `Cache-Control: no-store` ставит хендлер.
- События — только id и непрофильные значения, без персональных данных; у агрегата `version`, подписчик в режиме `LatestState` отбрасывает устаревшие версии (§6.5).
- Подписчик события — `events.Subscription` с явным `Delivery` (`EveryEvent` | `LatestState`), регистрируется в `subscriptions()` в `cmd/worker/main.go`; обработчик идемпотентен по `event_inbox` сам собой, пишет только в своей транзакции `tx`. Доставка на подписку, которой нет в реестре этого воркера (воркер другого релиза при выкатке), — обычная ошибка с повтором, а затем «мёртвая» задача (видна, переигрывается), не отмена; отменяется только задача на событие, уже удалённое чисткой. Переигровка — `worker events replay --type T --subscriber S --since RFC3339`.
  Импорт городов оператором — `worker geo import --country RU`: синхронно в процессе, без задачи River, под ролью `worker`; задача `geo.import` ставится только через `jobs.NewImportArgs`.
- Задача River объявляет очередь из `platform/queue` (`events`, `lifecycle`, `notify`, `mail`, `stats`, `media`, `maintenance`) в `InsertOpts`; очереди `default` нет. Аргументы — с полем версии `V`, без ПД, уникальность — `UniqueOpts`. River в воркере работает на неотменяемом ctx: по SIGTERM сначала останавливается relay, затем `queue.Stop` — мягкая остановка (25 с) и, не уложившись, `StopAndCancel` (5 с): сумма укладывается в 30 с между SIGTERM и SIGKILL. `WORKER_RELAY_BATCH` ≥ 1 и `WORKER_RELAY_POLL` > 0 проверяются на старте воркера.
- Аудит — `audit.Write(ctx, entry)` в транзакции действия; чтение чувствительного — `audit.WriteRead`. ПД в `Before`/`After` маскирует `audit.Mask` по словам ключа (camelCase/PascalCase делятся на слова: `contactEmail`, `contact_email`, `ContactEmail` — одно и то же); не маскируется ключ, оканчивающийся на `at`/`count`/`left`/`verified`/`enabled`/`required`. Маскирование по ключу, а не по содержимому: свободный текст с ПД (комментарии, описания) в `Before`/`After` класть нельзя. Новые чувствительные слова — в списки слов и пар в `audit/mask.go`.
  Автор — ровно один: `ActorUserID` (игрок) или `ActorStaffID` (сотрудник), без автора — `ActorRole`
  `system|operator|anonymous`; `ActorLabel` — снимок отображаемого имени (журнал показывает его); `ScopeCityID` —
  город записи (по нему модератор видит журнал своих городов). Два автора — `ErrInvalidEntry`. Журнал читается
  только `audit.List` и `audit.Get` (фильтры, скоуп модератора `CityIDs`+`OwnStaffID`, курсор `audit.log(created_at,id)`).
  `CityIDs == nil` — без ограничения городом (глобальная роль); модератору вызывающий ВСЕГДА передаёт не-nil срез
  (копия `append(make([]uuid.UUID, 0, len(s.CityIDs)), s.CityIDs...)`), скоуп выбирается по `GrantScoped`, а не по
  `len`: модератор без городов видит только свои записи, а не весь журнал. IP
  наружу — подсетью `audit.MaskIP` (/24, /48). Слово `code` в ключе — секрет, кроме кода-справочника
  (`country_code`, `error_code`: список `publicCode`).
  Роль обязательна всегда; `insert` повторяет CHECK `audit_log` из 0020 (роль из списка, служебная ⇔ нет автора, у
  сотрудника — роль сотрудника и `ActorLabel` 1–120 символов) и отвергает нарушение `ErrInvalidEntry` до SQL: поменял
  CHECK — поменяй списки ролей в `audit/audit.go`. `Entry.ID` задан — запись получает его (ссылка из письма); одна
  запись — `audit.Get` (поля и маска IP как в `List`, нет — `ok=false`).

## Край платформы (план 3/3)

- Текущее время — только `clock.Clock` (в тестах `clocktest.Fake`); длительности и таймауты —
  `start := time.Now()` … `time.Since(start)` (монотонные часы).
- Ошибка, которую должен увидеть клиент (лимит, антибот, идемпотентность, флаг), — `httpx.ProblemError`
  (`httpx.NewError(статус, httpx.Code…)`); код строкой — падение стража `TestProblemCodesAreConstants`.
- Почта — только задача `mail.SendArgs{V: 1, Mail: "<модуль>.<письмо>", Ref: id}` (на другую версию воркер
  вернёт ошибку, задача уйдёт в повтор и после `MaxAttempts` станет «мёртвой»); адрес находит
  `mail.Composer` модуля, текст — `i18n.Catalog.Text` (`backend/locales`, ключи ru = en).
- Пароли — `password.Check` + `password.Hasher` (argon2id под семафором; `password.DefaultParams()` — минимум OWASP);
  «нет почты» — `VerifyDummy`. Пароль приводится к NFKC; длиннее `password.MaxInputBytes` (1024 байта) —
  `ErrTooLong` до хэширования; хэш из базы с параметрами выше потолков (m 256 МиБ, t 10, p 16) или солью < 16 байт —
  `ErrMalformed`.
- Секреты в базе — `keys.Ring` из `*_SECRET_KEYS`: `Seal(данные, aad = id строки)` → шифротекст и `key_id` (хранить
  рядом), `Open(ct, key_id, aad)`; пустой aad — `keys.ErrNoAAD`. Хэш-коды — `MAC(key_id, метка, сообщение)` (своя
  метка на назначение, сравнение — `hmac.Equal`). Новые строки — `CurrentID()`; старый ключ убирают из окружения,
  когда счёт строк с этим `key_id` = 0; `Has` — есть ли ключ в кольце.
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
- Ключ лимита — только `ratelimit.IPKey` / `SubjectKey` / `EmailKey` (`<вид>:<класс>:<значение>`; виды `ip`,
  `user`, `staff`, `device`, `email`): счётчик, который сценарий ведёт сам через `Limiter`, попадает в то же
  пространство, что и `ratelimit.Middleware`.
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
