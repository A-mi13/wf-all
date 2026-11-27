---
paths:
  - "backend/internal/media/**"
---

# Модуль media (файлы: загрузка, обработка, отдача)

Спека — `docs/superpowers/specs/2026-11-18-media-design.md`; решения плана (R1–R20) и общие ограничения —
`docs/superpowers/plans/2026-11-26-media.md`. Задачи плана дополняют этот файл своими разделами.

- **Пакеты.** Корень `media` — типы (`Purpose`, `Variant`, `OwnerRef`, `Uploader`, `Actor`, `Declared`, `Staged`,
  `Image`, `Ticket`, `PrivateLink`), реестр целей (`registry.go`), порт `ContentCheck` и `CheckOff`, коды (`codes.go`),
  события (`events.go`). `State` — псевдоним `domain.State`: корень импортирует `internal/domain`, домен ничего из
  модуля не импортирует. SQL — `queries/{objects,uploads,variants,cleanup}.sql` → `internal/store/mediadb`
  (`sqlc generate` из `backend/`; модели всех таблиц используются запросами, якоря нет; варианты представления —
  `json.RawMessage`).
- **Цели.** `NewRegistry` проверяет цель при сборке бинарника (отказ старта, не 500): слаг имени и имени варианта
  (вариант входит в ключ `<object_id>/<вариант>.webp`), видимость, входы (Lottie — только один), `MaxBytes > 0`,
  у растра `MaxPixels > 0` (защита от «бомбы»), варианты (`Quality` 0–100, у `Lossy` — 1–100), типы владельца. Реестр
  неизменяем: `Get`/`Names` — копии, `Names` по алфавиту. Набор целей — только `cmd/internal/mediapurposes` (R12).
- **Владельцам (планы `economy` и др.).** Тип владельца, у которого есть билеты (`NewUpload`), входит в `OwnerTypes`
  ровно одной цели: `AttachInTx` цель не принимает и сверяет только тип владельца с целью строки. Конечные состояния
  показывает ручка владельца: `Service.Image` отдаёт `ClientState` и для `detached`/`taken_down`, а в `MediaImage` их
  нет — ручка отвечает «изображения нет» (как `shown` тестовых ручек `mediae2e`), `pending_upload` — так же.
- **Состояния (§4, §3.3).** Переходы — только `domain.Transition(from, to)`: `pending_upload → uploaded`;
  `uploaded → processing | rejected | detached | taken_down`; `processing → processing` (захват после падения
  исполнителя, `process_attempts + 1`) `| uploaded` (обычная ошибка) `| ready | rejected | detached | taken_down`;
  `ready → detached | taken_down`. Из конечных (`IsFinal`: `rejected`, `detached`, `taken_down`) переходов нет:
  `DetachInTx`/`TakeDownInTx` на конечном — «без изменений» по `IsFinal`, не ошибка. `CreateDirectInTx` создаёт строку
  сразу `uploaded`.
- **CHECK базы (`0028`)** повторяют пары «состояние — поля»: владелец у всех, кроме `pending_upload`; `ended_at` ⇔
  конечное; `reject_code` (непустой) ⇔ `rejected`; `ready_at` у `ready`, может остаться у `detached`/`taken_down`;
  не больше одного загрузившего (I-6: обнуление `uploaded_by_user` проходит); `uploaded_at` ⇔ не `pending_upload`
  (`media_objects_uploaded_at`: на нём держится `StaleProcessing`). Время колонок — из `clock.Clock`
  параметром запроса, умолчаний `now()` нет.
- **Запросы.** `version + 1` — на каждую смену состояния (`AttachObject`, `SetState`, `SetProcessing`, `SetReady`);
  событие несёт версию из `RETURNING`. Условие состояния в `WHERE` (`AttachObject` — `pending_upload`, `SetProcessing` —
  `uploaded|processing`, `SetReady` — `processing`) — защита в глубину: не та строка — `pgx.ErrNoRows`. `SetState` условия
  не имеет — переход проверяет вызывающий под `LockObject`. `SetProcessing` считает только падения: из `uploaded` —
  `process_attempts` без изменений, из `processing` — `+ 1` (`TestSetProcessingCountsOnlyCrashes`). `InsertVariant` —
  простой `INSERT` (только при `processing → ready` под замком, конфликт недостижим): `ON CONFLICT … DO UPDATE` требует
  права `UPDATE`, которого у `worker` на `media_variants` нет (`TestWorkerInsertsVariant`). Замки: в сценариях — `LockObject` (`FOR UPDATE`, ждать);
  `SKIP LOCKED` — только чистка (`ExpiredPending`, `EndedBefore`). `PendingLock` — `pg_advisory_xact_lock` по
  загрузившему перед `CountPendingByUser` и вставкой билета. `DeleteObjects` не удаляет `uploaded|processing|ready`.
  Границы чистки строгие (`<`), окно «завершить» — `expires_at + 1 ч`: вызывающий передаёт `before = now − 1 ч`.
- **Права ролей (§5.3, `grants.sql`).** `api`/`admin`: объекты `SIU`, билеты `SID`, варианты `S`; `worker`: объекты
  `SUD`, билеты `SD`, варианты `SID`; представление — `S` всем; `TRUNCATE` — никому. Страж — `TestMediaGrants`.
- **Чтение владельцами** — только `media_read_objects` (без `uploaded_by_*` и `declared_*`; варианты — массив, пустой
  `[]`); страж колонок — `TestReadViewHidesUploader`. Ссылка владельца на объект — без FK (§2.4).
- **События (§9)** — `media.object_ready`, `media.object_rejected` (+ `reject_code`), `media.object_taken_down`;
  агрегат `media_object`; payload — `object_id`, `purpose`, `owner_type`, `owner_id`; без адресов, ключей и
  загрузившего (`TestEventPayloadsNoPII`).
- **Коды (§7.2, R18)** — `media.*` в `OwnerCodes`, ошибки-значения `Err…` (`*httpx.Error`); отдают операции
  владельцев. Новая константа `Code…` без списка — `TestCodeConstantsListed`.
- **`intx`** (спека §2.3; Task 6): всё, что меняет объект на стороне владельца, — в транзакции владельца (`tx`
  параметром); пары владельцев — `archtest.IntxPairs` их планами (R11).

## Сервис и intx (Task 6)

- **Раскладка.** `media.Service` (`media.New(Deps)`) реализован в корне на `mediadb` (`service.go`, `upload.go`,
  `quota.go`, `confirm.go`, `stage.go`, `read.go`): `internal/app` корню недоступен — он работал бы на типах корня
  (цикл). Транзакционные операции — `media/intx` напрямую на `mediadb`/`domain`/`jobs`. Общие правила показа —
  `domain/view.go` (`ClientState`: `uploaded` → `processing`; `ShowVariants` — только `ready`; `VariantURL` — у
  закрытого пусто). Аргументы задач — `media/jobs/args.go` (`ProcessArgs`, `PurgeArgs`, `Bucket{In,Pub,Priv}`,
  `ProcessInsertOpts` — уникальность по аргументам, `PurgeInsertOpts`).
- **`NewUpload`:** `none` → `ErrUnavailable`; цель/тип владельца/два загрузивших — ошибка программиста (500); тип —
  точное совпадение с MIME входов (`AcceptedTypes`), размер 1…`MaxBytes` (422). Игрок: `Limiter.Over`
  `SubjectKey("media_upload_day")` без расхода (100/сутки; исчерпан — 429 `ratelimit.exceeded` + Retry-After = интервал
  политики), затем в **одной** tx: `PendingLock` → `CountPendingByUser(live_after = now)` ≥ 5 → `ErrTooManyPending` →
  `SumDeclaredBytesByUser(since = now − 24 ч, строго)` + заявка > 200 МиБ → `ErrQuotaExceeded` → `InsertObject` +
  `InsertUpload` (`POST`, `max_bytes` = заявка, срок now + 15 мин в целых секундах); после коммита — `Limiter.Add`
  (`WithoutCancel`; сбой — ERROR, билет выдан): отказы счёт не тратят (`TestNewUploadRefusalKeepsDayCount`). Лимитер —
  не под замком (второе соединение в tx грозит исчерпанием пула), поэтому параллельные «начать» у границы превышают
  суточный счёт не больше чем на 4 (незавершённых ≤ 5) — принято. Пределы — из `Deps.Rules` (cmd/api передаёт
  `API_RATE_LIMITS` поверх `DefaultRules`, nil — умолчания; класса нет — отказ `New`; `media_bytes_day.user=off` — объём
  не считается; `TestNewUploadRulesOverride`, `TestNewMediaRateRules`); admin-api — умолчания (сотрудник их не
  считает). Вес у `Limiter` нет (`TestLimiterHasNoWeight`) — объём только запросом, под тем же замком
  (`TestNewUploadQuotaConcurrent`). Сотрудник и система лимитов игрока не считают. Подпись — после коммита;
  `CompleteBefore = ExpiresAt + CompleteWindow` (1 ч).
- **`ConfirmUpload(ctx, id, owner, by)`** — до транзакции владельца: билета нет (прикреплён/строки нет) или билет на
  другого владельца или от другого загрузившего — `nil` без HEAD (чужой неотличим от несуществующего, отказ даст
  `AttachInTx`; `TestConfirmUploadForeign`); `now ≥ expires_at + 1 ч` — `ErrUploadExpired`; HEAD `ErrNotFound` —
  `ErrUploadIncomplete`. **`Stage`** — тип из входов,
  `LimitReader(MaxBytes+1)`, пусто/больше — `ErrTooLarge`, `Put` во `in` по новому `id.New()`, базы нет.
- **Чтение:** `Image`/`Images` — `media_read_objects`; `PrivateURL` — только `private` + `ready` + вариант есть, иначе
  `ErrObjectUnavailable` (у публичного — тоже); `PresignGet(5 мин, "private, no-store")`.
- **`intx`:** каждый метод сначала `sameTx` (`ErrTxMismatch`), затем `LockObject`. `AttachInTx`: нет строки, цель не
  допускает тип, загрузивший ≠ `by` — `ErrObjectUnavailable`; не `pending_upload` — повтор к тому же владельцу
  (кроме `detached`/`taken_down`) отдаёт текущее без задачи; билет на другого владельца — отказ; окно строго до
  `expires_at + 1 ч` (`ErrUploadExpired`); успех — `AttachObject`, `DeleteUpload`, `media.process`.
  `CreateDirectInTx` сверяет `Staged` с целью (ошибка программиста). `DetachInTx`: чужой владелец (и `pending_upload`)
  — 409, конечное/нет строки — `nil`. `TakeDownInTx`: причина — код `a_b(.c)*` ≤ 100; конечное/нет строки — `nil`,
  `pending_upload` — 409; аудит (`Actor.Label` — снимок имени сотрудника), событие с версией из `RETURNING`.
  Открепление и снятие ставят `media.purge` на `in` (`<id>`) и на бакет видимости: у `ready` — ключи строк
  `media_variants`, у `uploaded|processing` — все ключи цели `jobs.VariantKeys` (строк ещё нет, а неудачная или упавшая
  попытка могла записать файлы — ревью Task 9, I-1; страж `TestEndAfterFailedAttempt`); `PublicURLs` — только у
  снятия публичного (`jobs.PublicURLs`, `intx.Deps.PublicBaseURL`).
- **Тестовые помощники** — `mediatest`: `E2EPurposes` (`e2e_image`, `e2e_image_private`, владелец `e2e_owner`),
  `MarkerCheck` (R20), `MemoryBlob(clock)` (Memory + `DevSigner`), `Registry`, `SeedObject` (посев по CHECK 0028).

## Конвейер (Task 9)

- **Исполнители** — `media/jobs`: `AddWorkers(w, WorkerDeps{Pool, Clock, Registry, Blob, Check, Log})` (без зависимости
  или с открытым хранилищем без бакета/`Purger` — отказ старта); клиент River только для вставки `media.purge` — на том
  же пуле (роль `worker`). Виды и сроки — `queue.WorkerKinds` (`media.process` 2 мин, `media.purge` 1 мин), у
  admin-api — заглушки `AddForeign`. `V != 1` — обычная ошибка; режим `none` (`!Blob.Available`) —
  `river.JobCancel(media.ErrUnavailable)` у обеих.
- **`media.process` (§3.3).** Захват — короткая tx `LockObject` (ждать): не `uploaded|processing`/нет строки — успех без
  действий (хранилище не трогается); `SetProcessing`; `process_attempts ≥ 3` (третий захват, заставший `processing`)
  — `rejected(processing_failed)` в той же tx. Шаги 2–6 — без транзакций: `Get(in)` + `LimitReader(MaxBytes+1)`
  (больше — `too_large`), растр — `imaging.Process` (входы и пиксели цели, порт проверки при `ContentCheck`), Lottie —
  `lottie.Sanitize` с вшитыми картинками через `imaging.Process` (`full` без масштаба, ≤ 4 Мп, WebP q 85). Коды:
  `lottie.ErrUnsafe` → `unsafe_animation`, `imaging.ErrContentRejected` → `content_rejected`, `ErrUnsupported` →
  `unsupported_type`, `ErrTooManyPixels` → `too_many_pixels`, `ErrCorrupt` (оба пакета) → `corrupt`; прочее — обычная
  ошибка. Ключи — `<id>/<вариант>.webp`, Lottie — `<id>/anim.json` (`application/json`, имя строки — вариант цели);
  бакет — по `visibility` строки; `Cache-Control` `public, max-age=86400` / `private, no-store`.
- **Публикация** — tx `LockObject`: `processing` — `InsertVariant` × N, `SetReady` (размеры исходника после ориентации;
  у Lottie — `w`/`h` анимации), `media.object_ready`; `ready` — ничего; `detached|taken_down|rejected` или строки нет —
  `media.purge` записанных ключей (+ `PublicURLs` у `pub`); `uploaded` — обычная ошибка. После коммита — `Delete(in)`
  (сбой — WARN, не повтор). **Отклонение** — tx: только из `uploaded|processing` — `rejected`, `ended_at`,
  `media.object_rejected`, `media.purge` входящего и всех ключей цели `VariantKeys` (с `PublicURLs` у `pub`) — прошлая
  неудачная попытка могла записать варианты.
- **Ошибки.** Обычная ошибка шагов 2–7 — `context.WithoutCancel` + 5 с: `processing → uploaded` (только из
  `processing`) и ошибка River (падением не считается); `job.Attempt ≥ MaxAttempts` — `rejected(processing_failed)`.
  Входящего нет (`ErrNotFound`) — перечитать строку: не `uploaded|processing` — успех без изменений (сняли, открепили,
  опубликовал другой), иначе обычная ошибка. Паника исполнителя оставляет `processing` — засчитывается как падение.
- **`media.purge` (§3.8):** `Delete` каждого ключа (идемпотентно), затем `Purger.Purge(PublicURLs)`; сбой — повтор
  (25 попыток); неизвестный бакет и `blob.ErrBadKey` — `JobCancel`.
- **Lottie** (`internal/lottie`, Task 8): схема-разрешение LAC 1.0.1 в `allow.go` (дата сверки в комментарии; новые
  поля — только со сверкой со схемой), текстовые слои отклоняются целиком, слоты не допускаются, вшитые картинки —
  через `ImageFunc` после обхода; отказы — `ErrUnsafe`/`ErrCorrupt`; выход совместим с lottie-web (`st`, `td`, `hasMask`).
- **Стражи** — `media/jobs`: `TestSecondExecutorOnReady`, `TestTakeDownBetweenWriteAndPublish` (снятие из хука `Put`:
  запись под замком строки дала бы взаимную блокировку), `TestCrashCountsAttempts`, `TestStoreErrorsDoNotCountAsCrash`,
  `TestRollbackUsesWithoutCancel`, `TestUniqueWhileRetryable`, `TestProcessWaitsForRowLock`,
  `TestProcessInputGoneAfterTakeDown`, `TestProcessTooLargeAfterReupload`, `TestProcessRejectCodes`.

## Чистка, сверка, подписчик (Task 10)

- **`media.cleanup`** (`jobs/cleanup.go`, очередь `maintenance`, 5 попыток, уникальность 10 мин; `jobs.Periodic()` —
  раз в 10 мин, `RunOnStart`, у воркера и admin-api; срок 10 мин в `queue.WorkerKinds`). Один проход на одном «сейчас»,
  строки — пачками по 100 со `SKIP LOCKED` (строку под замком сценария пропускает, не ждёт), пачка — своя tx, файлы —
  после коммита (`Delete` идемпотентен; сбой хранилища не прерывает проход — ошибка River в конце). Границы строгие:
  `pending_upload` с `expires_at + 1 ч < now` — `Delete(in, id)` сразу, строка (билет каскадом) — только при
  `created_at < now − 24 ч` (суточная квота байт считается по строкам); `ExpiredPending` идёт курсором по id —
  оставленные строки не занимают пачку (входящее таких строк удаляется повторно каждым проходом до суток — `Delete`
  идемпотентен). timestamptz хранит микросекунды: граница окна различима с точностью 1 мкс (чистка отстаёт в
  безопасную сторону). `uploaded|processing` с `uploaded_at < now − 1 ч` — `media.process` заново (`ProcessInsertOpts`,
  дубля нет; `StaleProcessing` — курсором по id). `detached|taken_down` — `ended_at < now − 24 ч`, `rejected` —
  `< now − 30 сут`: строки, затем `Delete` всех ключей цели `VariantKeys` в бакете видимости (цели нет в реестре —
  ключи строк `media_variants`). Входящее без строки старше суток (время UUIDv7 ключа; чужой ключ не трогается) —
  `Delete` после обхода `List`. Режим `none` — только строки: хранилище не трогается, `media.process` не ставится.
  Стражи — `TestCleanupExpiredPending`, `…Pages`, `TestCleanupRestartsStale`, `TestCleanupYieldsToScenario`,
  `TestCleanupFinalStates`, `TestCleanupInboxOrphans`, `TestCleanupNone`.
- **Откат на конечном** (ревью Task 9, m-r1-1): `release` (обычная ошибка шагов 2–7) и `rejectFailed` (последняя
  попытка River), заставшие `detached|taken_down|rejected`, ставят `media.purge` всех ключей цели (`purgeEnded`, +
  `PublicURLs` у `pub`) — purge снятия мог пройти раньше записи варианта этой попыткой
  (`TestReleaseOnEndedPurgesVariants`, `TestLastAttemptOnEndedPurgesVariants`).
- **Подписчик** `media.uploaders` (`subscribers`, `identity.user_deleted`, `EveryEvent`; имя — ключ `event_inbox`, не
  менять): в tx доставки `DeletePendingByUser`, затем `ClearUploadedByUser` (I-6); входящее удалённых билетов уберут
  жизненный цикл и чистка. Регистрация — `workerhost.Subscriptions(log)`. Страж — `TestSubscriberUserDeleted` (relay +
  River под ролью `worker`).
- **Сверка** `worker media reconcile [--fix]` (`reconcile.Run`, роль `worker`, хранилище — `config.LoadBlob("WORKER_")`,
  `none` — «хранилище выключено», код 0): строки `ready` без файла варианта — в отчёт (правки нет: из `ready` в
  `rejected` перехода нет — решает оператор); файлы `pub`/`priv`, чей `<id>` не принадлежит строке `media_objects`, —
  в отчёт, с `--fix` — `Delete` (файл объекта со строкой, в том числе в обработке, сиротой не считается). Вывод — только
  id и ключи. Запрос — `ReadyVariantKeys` (курсор по ключу).
- **Сборка воркера** (`cmd/internal/workerhost`): `Prepare` читает `config.LoadBlob("WORKER_", environ)` (fs в
  прод-образе — отказ старта) и `blobopen.Open`; цели и проверка содержимого — только `mediapurposes.Registry()` /
  `Check()` (`cmd/internal/mediapurposes`: прод — пусто и `CheckOff`, тег `wf_e2e` — `mediatest.E2EPurposes` и
  `MarkerCheck`); `media.NewRegistry` вне этого пакета, `mediatest` и тестов — `archtest.TestMediaPurposesOnlyFromRegistry`;
  тот же страж запрещает импорт `mediatest` в файле, который собирается без тега `wf_e2e` (кроме `_test.go` и самого
  `mediatest`; условие `//go:build` разбирается, `TestMediatestImports`).
  Встроенный режим с `none` — `TestEmbeddedWorkerWithNone`.

## Контракт, dev-пути, сборка api и admin-api (Task 11)

- **Схемы** (§7.1) — `contracts/openapi/{public,admin}/media/schemas.yaml`: `UploadRequest`, `UploadTicket` (`method` —
  открытый enum `PUT|POST`, сервер выдаёт `POST`), `MediaImage` (`state` — `processing|ready|rejected`, `reject_code` —
  `null` кроме `rejected`, `variants` — объект по имени, у закрытого без `url`), `PrivateMediaLink`; в admin — ещё
  `MediaDirectUpload` (multipart, файл последним, 5 МиБ). Копии в public и admin равны (`contracts.test.mjs`). Своих
  операций нет — ссылаются операции владельцев; `pending_upload`/`detached`/`taken_down` владелец показывает как
  «изображения нет».
- **dev-пути fs** (§6.3, R4) — `media/httpdev`: `New(blob.Opened, реестр, журнал)` — `nil` при `s3`/`none`
  (`TestDevPathsOnlyInFS`), `cmd/api` передаёт `public.Options.Media` только не-nil. `POST /_blob/in` — форма как S3
  POST: поля до файла, файл последним полем `file` (иначе 400), буфер файла ≤ максимум реестра + 1, `VerifyUpload`
  (подпись, ключ, тип из поля `Content-Type` формы, точный размер, срок) — до `Put`, отказ — 403 без записи; предел
  тела — `UploadLimit()` = максимум реестра + 64 КиБ (`httpx.BodyLimit`, R16); без сессии и CSRF, CORS — только
  loopback-origin dev-фронтов. В `public` dev-пути — вне конвейера (свой роутер: peer, лимит тела, таймаут; прочее —
  `Mount("/")`): `Authorization` и `Idempotency-Key` не читаются, как у S3 (`TestDevMediaOutsidePipeline`). `GET /_media/pub/<id>/<вариант>` — тип из meta, `nosniff`, `public, max-age=86400`;
  `GET /_media/priv/…?exp&sig` — `VerifyGet` (нет, чужая, истекла — 403), `private, no-store`; ключ без «/» (входящее)
  и обход каталога — 404. Стражи — `TestDevUploadForm`, `TestDevMediaHeaders`, `TestDevUploadCORS`.
- **Сборка** (`cmd/api/media.go`, `cmd/admin-api/media.go`): `openBlob` — `config.LoadBlob("API_"|"ADMIN_")` (fs в
  прод-образе — отказ до базы, `TestRunRejectsFSInProdImage` у обоих), `mediapurposes.Registry()`, `blobopen.Open`;
  `newMedia` — `media.New` + `intx.New` с `PublicBaseURL` из того же `blob.Opened` (клиент River только для вставки:
  у api — свой, у admin-api — `staff.Deps.River`). admin-api читает хранилище только сервером (команды оператора — нет)
  и отдаёт `PublicBaseURL` в `admin.Options.MediaBaseURL` (origin — в `img-src` CSP статики, `none` — без него).
- **Тестовые ручки** (`//go:build wf_e2e`, вне контракта, `/_e2e/media/…`) — пакет `cmd/internal/mediae2e` целиком под
  тегом (страж `TestProdBuildWithoutE2ETag` читает все его файлы), подключают `cmd/{api,admin-api}/media_e2e.go`;
  прод — `media_prod.go` (`nil`; маршруты обходит `chi.Walk`: api — `TestProdBuildHasNoE2ERoutes`, admin-api —
  `TestAdminProdBuildHasNoE2ERoutes` на `newAdminHandler`, пара сборки wf_e2e — `TestAdminE2EBuildHasE2ERoutes`). api (сессия игрока —
  конвейер public, `Idempotency-Key` — его же): `POST uploads` → `NewUpload` (он сам — единственная `db.InTx` запроса:
  в свою `InTx` не оборачивать, иначе `errSecondTx` → 500), `POST objects` → `ConfirmUpload` до транзакции +
  `db.InTx(AttachInTx)`, `GET objects/{id}`, `GET objects/{id}/private?variant=` (ответы — `Cache-Control: no-store`).
  admin-api: звено сессии конвейера идёт только по маршрутам контракта — ручки сами грузят сотрудника (cookie,
  `active`, без `Reauth`) и ставят `idempotency.Middleware(Staff)`; CSRF и лимит тела — конвейер (`E2EBodyLimits`:
  прямая загрузка 5 МиБ; сверх — 413 и с `Idempotency-Key`: `idempotency.Middleware` на `MaxBytesError` отвечает 413).
  `POST direct` (multipart, файл последним; дубль поля и поле > 1 КиБ — 400) → `Stage` + `db.InTx(CreateDirectInTx)`;
  `POST objects/{id}/take-down` → `Label` до транзакции, `db.InTx(TakeDownInTx)` (причина `e2e.take_down`). Владелец —
  `e2e_owner` с `owner_id` из запроса. Стражи — `TestMediaE2E`, `TestNoPublicURLForPrivate` (`cmd/api`),
  `TestMediaE2EAdmin` (`cmd/admin-api`); в `backend:test` — команда `go test -tags wf_e2e -run …` по `cmd/api`,
  `cmd/admin-api`, `cmd/worker`, `cmd/internal/mediapurposes` (страж `TestE2ECaptainChecksTestInCI`); воркер живого e2e
  собирается с тегом (`scripts/e2e-admin.sh`).

## Живой путь на S3 и сверка (Task 13)

- **Интеграция с dev-Garage** (R13) — `internal/media/e2e_s3_test.go`: `TestMediaE2ES3` — весь путь `TestMediaE2E` на
  трёх ключах процессов по R2 через `blobopen.Open` (api — `NewUpload`, `ConfirmUpload`/HEAD, `PrivateURL`; admin —
  `Stage` + `CreateDirectInTx`, `PrivateURL`; worker — `media.process`); публичное читается сайтом бакета `wf-pub`
  (`http://wf-pub.web.localhost:<WF_S3_WEB_PORT>/`, соединение — на 127.0.0.1 с исходным Host), «Размер»: ровно
  `MaxBytes` — `ready`, перезаливка по живому билету телом больше — отказ хранилища, обход политики ключом с записью во
  входящее — `too_large`. `TestMediaS3KeyRights` — операции таблицы §6.4 своим ключом проходят, запрещённое R2 (запись
  и удаление в `pub`/`priv`, любой доступ к `pub` у api и admin) — 403 (`smithyhttp.ResponseError`, у HEAD тела нет).
  Расширенные права R2 (api и admin удаляют и листают во `in`, листают `priv`) тест не проверяет ни на успех, ни на
  отказ. Запуск — `bash scripts/s3.sh up` + `eval "$(bash scripts/s3.sh env)"`, без переменных — пропуск, `CI=true` —
  падение.
- **Прикрепление ∥ чистка** — `TestAttachVsExpiredCleanup` (`media/intx`) гоняет настоящий `CleanupWorker` на часах
  воркера, смещённых вперёд (у api окно ещё живо, у чистки прошло): строка моложе суток остаётся, прикрепление
  проходит всегда; порядок «чистка, затем прикрепление» — объект без входящего, последняя попытка `media.process` —
  `rejected(processing_failed)` (спека §12 «Гонки»). На одних часах недостижимо.

## Пределы imaging (финальное ревью, волна исправлений)

- **До декодирования**, кроме пикселей и стороны: у JPEG — сканов ≤ `maxScans` (100; больше — `ErrUnsupported`,
  `unsupported_type`; `walkJPEG` проходит весь файл как `image/jpeg` — энтропийные данные до маркера, FF DA внутри
  COM/APPn сканом не считается) и байты декодера `decodeBytes` ≤ `MaxPixels × decodeBytesPerPixel` (8; больше —
  `ErrTooManyPixels`, `too_many_pixels`). `decodeBytes` повторяет выделения `image/jpeg` Go 1.27 (плоскости на сетке MCU,
  чёрный канал, RGBA/CMYK на выходе — APP0/APP14 по всему файлу, коэффициенты прогрессивного 256 Б на блок); обновили Go
  или формулу — `TestDecodeBytesMatchesDecoder` (сверка с `TotalAlloc` декодера). Стражи — `TestProgressiveCoeffBomb`,
  `TestDecodeBudgetBoundary`, `TestScanLimit`, граница WebP 16383/16384 — `TestVariantSideLimit`.
- **ICC `desc` (mluc)** — записей ≤ 64, запись ≤ 512 Б, всего ≤ 4 КиБ; нарушение — профиль не P3 (как битый: картинка
  не отклоняется, варианты без профиля). Стражи — `TestICCDescLimits`, `TestICCDescBomb`.
- Генераторы злых входов (`zeroJPEG`, `progScans`, `eobRuns`, `mlucBomb`, `p3WithDesc`) — `helpers_test.go`; тесты на
  `TotalAlloc` — `// serial`. Пик кучи на 40 Мп — комментарий `Process` и `deploy/server/README.md` («Память воркера»).
- **Intx:** повтор «завершить» к `detached`/`taken_down` — `ErrObjectUnavailable` (`TestAttachRights`).
