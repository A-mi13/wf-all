package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riverqueue/river"

	"wf/backend/internal/media"
	"wf/backend/internal/platform/blob"
)

var errBucket = errors.New("media/jobs: неизвестный бакет")

// PurgeWorker — исполнитель media.purge (спека §3.8): удаление ключей одного бакета, затем сброс кэша CDN по
// публичным адресам. Сбой хранилища или Purger — повтор River (до 25 попыток: файлы обязаны исчезнуть).
type PurgeWorker struct {
	river.WorkerDefaults[PurgeArgs]
	d WorkerDeps
}

// Timeout — срок задачи (строка media.purge в queue.WorkerKinds).
func (*PurgeWorker) Timeout(*river.Job[PurgeArgs]) time.Duration { return time.Minute }

// Work — Delete каждого ключа (нет файла — не ошибка), затем Purger.Purge(PublicURLs). Режим none, неизвестный бакет
// и негодный ключ — отмена: повтор их не исправит.
func (w *PurgeWorker) Work(ctx context.Context, job *river.Job[PurgeArgs]) error {
	a := job.Args
	if a.V != 1 {
		return fmt.Errorf("%w: media.purge v=%d", errVersion, a.V)
	}
	if !w.d.Blob.Available {
		return river.JobCancel(media.ErrUnavailable)
	}
	var s blob.Store
	switch a.Bucket {
	case BucketIn:
		s = w.d.Blob.Buckets.In
	case BucketPub:
		s = w.d.Blob.Buckets.Pub
	case BucketPriv:
		s = w.d.Blob.Buckets.Priv
	default:
		return river.JobCancel(fmt.Errorf("%w: %q", errBucket, a.Bucket))
	}
	for _, k := range a.Keys {
		if err := s.Delete(ctx, k); err != nil {
			if errors.Is(err, blob.ErrBadKey) {
				return river.JobCancel(fmt.Errorf("media/jobs: удаление из %s: %w", a.Bucket, err))
			}
			return fmt.Errorf("media/jobs: удаление из %s: %w", a.Bucket, err)
		}
	}
	if len(a.PublicURLs) > 0 {
		if err := w.d.Blob.Purger.Purge(ctx, a.PublicURLs); err != nil {
			return fmt.Errorf("media/jobs: сброс кэша CDN: %w", err)
		}
	}
	return nil
}
