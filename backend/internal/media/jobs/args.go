// Package jobs — задачи River модуля media (спека §3.3, §3.8): обработка загрузки (media.process) и удаление файлов
// (media.purge). Аргументы — здесь, их вставляет media/intx в транзакции владельца; исполнители — Task 9 плана.
package jobs

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/queue"
)

// Бакеты задачи удаления (PurgeArgs.Bucket): внутренние имена blob.Buckets.
const (
	BucketIn   = "in"
	BucketPub  = "pub"
	BucketPriv = "priv"
)

// ProcessArgs — обработка объекта: версия аргументов и id; без ПД (только id).
type ProcessArgs struct {
	V        int       `json:"v"`
	ObjectID uuid.UUID `json:"object_id"`
}

func (ProcessArgs) Kind() string { return "media.process" }

// ProcessInsertOpts — по объекту одна живая задача (§3.3): уникальность по аргументам среди ждущих, повторяемых и
// исполняемых — повтор «завершить» и перезапуск чисткой дубля не ставят; завершённая не мешает.
func ProcessInsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       queue.Media,
		MaxAttempts: 10,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStateScheduled, rivertype.JobStatePending,
			rivertype.JobStateRetryable, rivertype.JobStateRunning,
		}},
	}
}

// PurgeArgs — удаление ключей одного бакета (Delete идемпотентен) и сброс кэша CDN по публичным адресам (Purger).
type PurgeArgs struct {
	V          int      `json:"v"`
	Bucket     string   `json:"bucket"`
	Keys       []string `json:"keys"`
	PublicURLs []string `json:"public_urls"`
}

func (PurgeArgs) Kind() string { return "media.purge" }

// PurgeInsertOpts — очередь media, до 25 попыток (сбой хранилища повторяется River).
func PurgeInsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Media, MaxAttempts: 25}
}
