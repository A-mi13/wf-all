package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
	"wf/backend/internal/platform/queue"
)

// QueueEvents — очередь доставки событий подписчикам (спека §9.1).
const QueueEvents = queue.Events

// DeliverArgs — доставить событие одному подписчику. Только id: событие читается из outbox
// (хранится 30 дней), персональных данных в аргументах нет (§9.5). V — версия аргументов.
type DeliverArgs struct {
	V          int       `json:"v"`
	EventID    uuid.UUID `json:"event_id"`
	Subscriber string    `json:"subscriber"`
}

// NewDeliverArgs — аргументы доставки текущей версии (V = 1).
func NewDeliverArgs(eventID uuid.UUID, subscriber string) DeliverArgs {
	return DeliverArgs{V: 1, EventID: eventID, Subscriber: subscriber}
}

func (DeliverArgs) Kind() string { return "events.deliver" }

// InsertOpts: уникальность по аргументам — relay и переигровка не ставят одну доставку дважды.
func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents, MaxAttempts: 20, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// DeliverWorker — задача River: доставляет одно событие одному подписчику ровно один раз
// (отметка event_inbox в транзакции обработчика) и, для LatestState, без отката к устаревшей
// версии агрегата (курсор event_cursors под блокировкой строки).
type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	pool db.TxStarter
	q    *eventsdb.Queries
	reg  *Registry
}

// NewDeliverWorker: pool открывает транзакцию доставки, q — пул для чтения события из outbox
// вне транзакции, reg — подписки этого релиза воркера.
func NewDeliverWorker(pool db.TxStarter, q eventsdb.DBTX, reg *Registry) *DeliverWorker {
	return &DeliverWorker{pool: pool, q: eventsdb.New(q), reg: reg}
}

// Timeout — одна доставка не держит слот очереди дольше минуты.
func (*DeliverWorker) Timeout(*river.Job[DeliverArgs]) time.Duration { return time.Minute }

func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	row, err := w.q.GetEvent(ctx, job.Args.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Событие уже удалено чисткой — повторять бессмысленно, оно не вернётся.
		return river.JobCancel(fmt.Errorf("events: событие %s удалено чисткой", job.Args.EventID))
	}
	if err != nil {
		return fmt.Errorf("events: чтение события %s: %w", job.Args.EventID, err)
	}
	sub, ok := w.reg.Get(job.Args.Subscriber, row.EventType)
	if !ok {
		// Не отмена: при выкатке новый релиз добавляет подписку и его relay ставит задачу, а взять
		// её может ещё работающий воркер старого релиза. Обычная ошибка — River повторит с
		// паузой, задачу заберёт новый воркер; исчерпав попытки, задача станет discarded
		// («мёртвой») — видна и переигрывается, а не теряется молча.
		return fmt.Errorf("events: нет подписки %s на %s (воркер другого релиза?)", job.Args.Subscriber, row.EventType)
	}
	e := envelope(row)
	return db.InTx(ctx, w.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := eventsdb.New(tx)
		fresh, err := q.InsertInbox(ctx, eventsdb.InsertInboxParams{Subscriber: sub.Subscriber, EventID: e.ID})
		if err != nil {
			return fmt.Errorf("events: inbox %s: %w", sub.Subscriber, err)
		}
		if fresh == 0 {
			return nil // уже обработано этим подписчиком
		}
		if sub.Delivery == LatestState {
			key := eventsdb.EnsureCursorParams{Subscriber: sub.Subscriber, AggregateType: e.AggregateType, AggregateID: e.AggregateID}
			if err := q.EnsureCursor(ctx, key); err != nil {
				return fmt.Errorf("events: курсор %s: %w", sub.Subscriber, err)
			}
			applied, err := q.LockCursor(ctx, eventsdb.LockCursorParams(key))
			if err != nil {
				return fmt.Errorf("events: блокировка курсора %s: %w", sub.Subscriber, err)
			}
			if e.AggregateVersion < applied {
				return nil // устаревшее: отметка inbox остаётся, эффекта нет
			}
		}
		if err := sub.Handle(ctx, tx, e); err != nil {
			return fmt.Errorf("events: %s: %w", sub.Subscriber, err)
		}
		if sub.Delivery == LatestState {
			if err := q.AdvanceCursor(ctx, eventsdb.AdvanceCursorParams{
				Version: e.AggregateVersion, Subscriber: sub.Subscriber,
				AggregateType: e.AggregateType, AggregateID: e.AggregateID,
			}); err != nil {
				return fmt.Errorf("events: сдвиг курсора %s: %w", sub.Subscriber, err)
			}
		}
		return nil
	})
}

// AddWorkers регистрирует задачи платформы событий в реестре воркера.
func AddWorkers(w *river.Workers, pool *pgxpool.Pool, reg *Registry) {
	river.AddWorker(w, NewDeliverWorker(pool, pool, reg))
	river.AddWorker(w, NewCleanupWorker(pool))
}
