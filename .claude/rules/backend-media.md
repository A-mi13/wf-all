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
  не имеет — переход проверяет вызывающий под `LockObject`. Замки: в сценариях — `LockObject` (`FOR UPDATE`, ждать);
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
