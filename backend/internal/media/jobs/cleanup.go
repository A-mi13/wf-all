package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/media"
	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/queue"
)

// Сроки чистки (спека §3.9); все границы строгие (<).
const (
	cleanupBatch = 100
	// completeWindow — окно «завершить» после срока билета (media.CompleteWindow): входящее удаляется после него.
	completeWindow = media.CompleteWindow
	// pendingRowAge — строка незавершённого билета живёт сутки от created_at: суточная квота байт считается по
	// строкам (ревью Task 6), ранняя чистка вернула бы её.
	pendingRowAge = 24 * time.Hour
	// staleAfter — uploaded/processing дольше часа: задача обработки потеряна или зависла — поставить заново.
	staleAfter = time.Hour
	// endedAge — detached/taken_down; rejectedAge — rejected (владелец успевает показать «не принято»).
	endedAge    = 24 * time.Hour
	rejectedAge = 30 * 24 * time.Hour
	// inboxAge — входящее без строки (страховка правила жизненного цикла бакета in).
	inboxAge = 24 * time.Hour
)

// CleanupArgs — чистка media по срокам (§3.9): без данных объектов.
type CleanupArgs struct {
	V int `json:"v"`
}

// Kind — вид задачи.
func (CleanupArgs) Kind() string { return "media.cleanup" }

// InsertOpts — очередь maintenance, одна задача в 10 минут (как периодика).
func (CleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Maintenance, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByPeriod: 10 * time.Minute}}
}

// Periodic — периодика media: чистка раз в 10 минут. Подключают оба бинарника, которые могут стать лидером River
// (cmd/internal/workerhost и cmd/admin-api, страж TestPeriodicSameInBothBinaries); исполняет воркер. Новый срез и
// задача на вызов; ID = вид.
func Periodic() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{V: 1}, nil },
		&river.PeriodicJobOpts{ID: CleanupArgs{}.Kind(), RunOnStart: true})}
}

// CleanupWorker — исполнитель media.cleanup.
type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	d   WorkerDeps
	ins insertClient
}

// NewCleanupWorker — исполнитель чистки; клиент River только для перезапуска media.process — на том же пуле.
func NewCleanupWorker(d WorkerDeps) (*CleanupWorker, error) {
	if d.Pool == nil || d.Clock == nil || d.Registry == nil || d.Log == nil {
		return nil, errors.New("media/jobs: чистка без пула, часов, реестра или журнала")
	}
	if err := checkBlob(d.Blob); err != nil {
		return nil, err
	}
	ins, err := river.NewClient(riverpgxv5.New(d.Pool), queue.InsertConfig(d.Log))
	if err != nil {
		return nil, fmt.Errorf("media/jobs: клиент River: %w", err)
	}
	return &CleanupWorker{d: d, ins: ins}, nil
}

// Timeout — срок задачи (строка media.cleanup в queue.WorkerKinds).
func (*CleanupWorker) Timeout(*river.Job[CleanupArgs]) time.Duration { return 10 * time.Minute }

// cleanupStats — итог прохода для журнала (только числа).
type cleanupStats struct {
	pendingFiles, pendingRows, restarted, endedRows, inbox int
}

// Work — проход чистки на одном «сейчас»: незавершённые, зависшие, конечные, входящее без строки. Строки — пачками
// по 100 со SKIP LOCKED (строку под замком сценария чистка пропускает, не ждёт), каждая пачка — своя транзакция;
// файлы удаляются после коммита (Delete идемпотентен). Режим none — только строки: хранилища нет, а media.process
// отменился бы. Сбой хранилища не прерывает проход — ошибка River в конце (повтор безвреден).
func (w *CleanupWorker) Work(ctx context.Context, job *river.Job[CleanupArgs]) error {
	if job.Args.V != 1 {
		return fmt.Errorf("%w: media.cleanup v=%d", errVersion, job.Args.V)
	}
	now := w.d.Clock.Now()
	var st cleanupStats
	var errs []error
	if err := w.pending(ctx, now, &st, &errs); err != nil {
		return err
	}
	if w.d.Blob.Available {
		if err := w.restartStale(ctx, now, &st); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		state media.State
		age   time.Duration
	}{{media.StateDetached, endedAge}, {media.StateTakenDown, endedAge}, {media.StateRejected, rejectedAge}} {
		if err := w.ended(ctx, f.state, now.Add(-f.age), &st, &errs); err != nil {
			return err
		}
	}
	if w.d.Blob.Available {
		if err := w.inbox(ctx, now, &st); err != nil {
			errs = append(errs, err)
		}
	}
	if st != (cleanupStats{}) {
		w.d.Log.InfoContext(ctx, "media: чистка", "pending_files", st.pendingFiles, "pending_rows", st.pendingRows,
			"restarted", st.restarted, "ended_rows", st.endedRows, "inbox", st.inbox)
	}
	return errors.Join(errs...)
}

// pending — незавершённые с прошедшим окном (expires_at + 1 ч < now): входящее — сразу, строка (билет каскадом) —
// когда created_at старше суток. Курсор по id: оставленные строки не занимают следующие пачки.
func (w *CleanupWorker) pending(ctx context.Context, now time.Time, st *cleanupStats, errs *[]error) error {
	rowBefore := now.Add(-pendingRowAge)
	after := uuid.Nil
	for {
		var rows []mediadb.ExpiredPendingRow
		err := db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
			q := mediadb.New(tx)
			var err error
			rows, err = q.ExpiredPending(ctx, mediadb.ExpiredPendingParams{Before: now.Add(-completeWindow), After: after,
				Lim: cleanupBatch})
			if err != nil {
				return fmt.Errorf("media/jobs: чистка незавершённых: %w", err)
			}
			var old []uuid.UUID
			for _, r := range rows {
				if r.CreatedAt.Before(rowBefore) {
					old = append(old, r.ID)
				}
			}
			if len(old) == 0 {
				return nil
			}
			deleted, err := q.DeleteObjects(ctx, old)
			if err != nil {
				return fmt.Errorf("media/jobs: удаление незавершённых: %w", err)
			}
			st.pendingRows += len(deleted)
			return nil
		})
		if err != nil {
			return err
		}
		if w.d.Blob.Available {
			for _, r := range rows {
				if err := w.d.Blob.Buckets.In.Delete(ctx, r.ID.String()); err != nil {
					*errs = append(*errs, fmt.Errorf("media/jobs: чистка: входящее %s: %w", r.ID, err))
					continue
				}
				st.pendingFiles++
			}
		}
		if len(rows) < cleanupBatch {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// restartStale — uploaded/processing с uploaded_at старше часа: media.process заново (ProcessInsertOpts — по объекту
// одна живая задача, дубля нет; круги ограничивает process_attempts).
func (w *CleanupWorker) restartStale(ctx context.Context, now time.Time, st *cleanupStats) error {
	q := mediadb.New(w.d.Pool)
	before := now.Add(-staleAfter)
	opts := ProcessInsertOpts()
	after := uuid.Nil
	for {
		ids, err := q.StaleProcessing(ctx, mediadb.StaleProcessingParams{Before: &before, After: after, Lim: cleanupBatch})
		if err != nil {
			return fmt.Errorf("media/jobs: чистка зависших: %w", err)
		}
		for _, oid := range ids {
			r, err := w.ins.Insert(ctx, ProcessArgs{V: 1, ObjectID: oid}, &opts)
			if err != nil {
				return fmt.Errorf("media/jobs: перезапуск обработки: %w", err)
			}
			if !r.UniqueSkippedAsDuplicate {
				st.restarted++
			}
		}
		if len(ids) < cleanupBatch {
			return nil
		}
		after = ids[len(ids)-1]
	}
}

// deletion — файлы удалённой строки: бакет видимости и ключи.
type deletion struct {
	store blob.Store
	keys  []string
}

// ended — конечные строки состояния state с ended_at < before: строки (варианты, билет — каскадом), после коммита —
// Delete всех ключей вариантов цели в бакете видимости (их уже удалил media.purge — сверка).
func (w *CleanupWorker) ended(ctx context.Context, state media.State, before time.Time, st *cleanupStats, errs *[]error) error {
	for {
		var files []deletion
		n := 0
		err := db.InTx(ctx, w.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
			q := mediadb.New(tx)
			rows, err := q.EndedBefore(ctx, mediadb.EndedBeforeParams{State: string(state), Before: &before, Lim: cleanupBatch})
			if err != nil {
				return fmt.Errorf("media/jobs: чистка %s: %w", state, err)
			}
			n = len(rows)
			if n == 0 {
				return nil
			}
			ids := make([]uuid.UUID, n)
			for i, r := range rows {
				ids[i] = r.ID
				keys, err := w.variantKeys(ctx, q, r)
				if err != nil {
					return err
				}
				files = append(files, deletion{store: w.bucket(media.Visibility(r.Visibility)), keys: keys})
			}
			deleted, err := q.DeleteObjects(ctx, ids)
			if err != nil {
				return fmt.Errorf("media/jobs: удаление %s: %w", state, err)
			}
			st.endedRows += len(deleted)
			return nil
		})
		if err != nil {
			return err
		}
		if w.d.Blob.Available {
			for _, f := range files {
				for _, k := range f.keys {
					if err := f.store.Delete(ctx, k); err != nil {
						*errs = append(*errs, fmt.Errorf("media/jobs: чистка: вариант %s: %w", k, err))
					}
				}
			}
		}
		if n < cleanupBatch {
			return nil
		}
	}
}

// variantKeys — все ключи вариантов цели (VariantKeys); цели нет в реестре — ключи строк media_variants.
func (w *CleanupWorker) variantKeys(ctx context.Context, q *mediadb.Queries, r mediadb.EndedBeforeRow) ([]string, error) {
	if p, ok := w.d.Registry.Get(r.Purpose); ok {
		return VariantKeys(r.ID, p), nil
	}
	vs, err := q.ListVariants(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("media/jobs: варианты %s: %w", r.ID, err)
	}
	keys := make([]string, len(vs))
	for i, v := range vs {
		keys[i] = v.BlobKey
	}
	return keys, nil
}

func (w *CleanupWorker) bucket(v media.Visibility) blob.Store {
	if v == media.VisibilityPrivate {
		return w.d.Blob.Buckets.Priv
	}
	return w.d.Blob.Buckets.Pub
}

// inbox — входящее без строки старше суток: возраст — время UUIDv7 ключа (id объекта создаётся вместе с билетом или
// перед Stage); чужой ключ (не UUIDv7) не трогается — его уберёт правило жизненного цикла. Удаление — после обхода
// (List не обязан терпеть удаление на ходу); в in лежит не больше суток загрузок (жизненный цикл), список невелик.
func (w *CleanupWorker) inbox(ctx context.Context, now time.Time, st *cleanupStats) error {
	q := mediadb.New(w.d.Pool)
	before := now.Add(-inboxAge)
	in := w.d.Blob.Buckets.In
	var orphans []string
	err := in.List(ctx, "", func(key string) error {
		oid, ok := inboxID(key)
		if !ok || !v7Time(oid).Before(before) {
			return nil
		}
		exists, err := q.ObjectExists(ctx, oid)
		if err != nil {
			return fmt.Errorf("media/jobs: чистка входящего: %w", err)
		}
		if !exists {
			orphans = append(orphans, key)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("media/jobs: чистка входящего: %w", err)
	}
	for _, key := range orphans {
		if err := in.Delete(ctx, key); err != nil {
			return fmt.Errorf("media/jobs: чистка: входящее %s: %w", key, err)
		}
		st.inbox++
	}
	return nil
}

// inboxID — id объекта из ключа входящего: каноническая запись UUIDv7, иначе ok=false (чужой ключ).
func inboxID(key string) (uuid.UUID, bool) {
	oid, err := uuid.Parse(key)
	return oid, err == nil && oid.Version() == 7 && oid.String() == key
}

// v7Time — момент создания UUIDv7: первые 48 бит — миллисекунды Unix.
func v7Time(u uuid.UUID) time.Time {
	var ms int64
	for _, b := range u[:6] {
		ms = ms<<8 | int64(b)
	}
	return time.UnixMilli(ms)
}
