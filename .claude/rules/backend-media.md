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
  у растра `MaxPixels > 0` (защита от «бомбы»), варианты, типы владельца. Реестр неизменяем: `Get`/`Names` — копии,
  `Names` по алфавиту. Набор целей — только `cmd/internal/mediapurposes` (R12).
- **Состояния (§4, §3.3).** Переходы — только `domain.Transition(from, to)`: `pending_upload → uploaded`;
  `uploaded → processing | rejected | detached | taken_down`; `processing → processing` (захват после падения
  исполнителя, `process_attempts + 1`) `| uploaded` (обычная ошибка) `| ready | rejected | detached | taken_down`;
  `ready → detached | taken_down`. Из конечных (`IsFinal`: `rejected`, `detached`, `taken_down`) переходов нет:
  `DetachInTx`/`TakeDownInTx` на конечном — «без изменений» по `IsFinal`, не ошибка. `CreateDirectInTx` создаёт строку
  сразу `uploaded`.
- **CHECK базы (`0028`)** повторяют пары «состояние — поля»: владелец у всех, кроме `pending_upload`; `ended_at` ⇔
  конечное; `reject_code` (непустой) ⇔ `rejected`; `ready_at` у `ready`, может остаться у `detached`/`taken_down`;
  не больше одного загрузившего (I-6: обнуление `uploaded_by_user` проходит). Время колонок — из `clock.Clock`
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
  точное совпадение с MIME входов (`AcceptedTypes`), размер 1…`MaxBytes` (422). Игрок: `Limiter.Allow`
  `SubjectKey("media_upload_day")` (100/сутки, отказ — 429 `ratelimit.exceeded` + Retry-After), затем в **одной** tx:
  `PendingLock` → `CountPendingByUser(live_after = now)` ≥ 5 → `ErrTooManyPending` → `SumDeclaredBytesByUser(since =
  now − 24 ч, строго)` + заявка > 200 МиБ → `ErrQuotaExceeded` → `InsertObject` + `InsertUpload` (`POST`, `max_bytes` =
  заявка, срок now + 15 мин в целых секундах). Вес у `Limiter` нет (`TestLimiterHasNoWeight`) — объём только запросом,
  под тем же замком (`TestNewUploadQuotaConcurrent`). Сотрудник и система лимитов игрока не считают. Подпись — после
  коммита; `CompleteBefore = ExpiresAt + CompleteWindow` (1 ч).
- **`ConfirmUpload`** — до транзакции владельца: билета нет (прикреплён/строки нет) — `nil` без HEAD; `now ≥
  expires_at + 1 ч` — `ErrUploadExpired`; HEAD `ErrNotFound` — `ErrUploadIncomplete`. **`Stage`** — тип из входов,
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
  Открепление и снятие ставят `media.purge` на `in` (`<id>`) и, если есть варианты, на бакет видимости (ключи;
  `PublicURLs` — только у снятия публичного, `intx.Deps.PublicBaseURL`).
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
  `media.object_rejected`, `media.purge` входящего и ключей (после падения/последней попытки — все ключи цели).
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
