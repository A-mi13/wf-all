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
)

// QueueEvents — очередь доставки событий подписчикам (спека §9.1).
const QueueEvents = "events"

// DeliverArgs — доставить событие одному подписчику. Только id: событие читается из outbox
// (хранится 30 дней), персональных данных в аргументах нет (§9.5). V — версия аргументов.
type DeliverArgs struct {
	V          int       `json:"v"`
	EventID    uuid.UUID `json:"event_id"`
	Subscriber string    `json:"subscriber"`
}

func NewDeliverArgs(eventID uuid.UUID, subscriber string) DeliverArgs {
	return DeliverArgs{V: 1, EventID: eventID, Subscriber: subscriber}
}

func (DeliverArgs) Kind() string { return "events.deliver" }

// InsertOpts: уникальность по аргументам — relay и переигровка не ставят одну доставку дважды.
func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEvents, MaxAttempts: 20, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	pool db.TxStarter
	q    *eventsdb.Queries
	reg  *Registry
}

func NewDeliverWorker(pool db.TxStarter, q eventsdb.DBTX, reg *Registry) *DeliverWorker {
	return &DeliverWorker{pool: pool, q: eventsdb.New(q), reg: reg}
}

// Timeout — одна доставка не держит слот очереди дольше минуты.
func (*DeliverWorker) Timeout(*river.Job[DeliverArgs]) time.Duration { return time.Minute }

func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	row, err := w.q.GetEvent(ctx, job.Args.EventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(fmt.Errorf("events: событие %s удалено чисткой", job.Args.EventID))
	}
	if err != nil {
		return err
	}
	sub, ok := w.reg.Get(job.Args.Subscriber, row.EventType)
	if !ok {
		return river.JobCancel(fmt.Errorf("events: нет подписки %s на %s", job.Args.Subscriber, row.EventType))
	}
	e := envelope(row)
	return db.InTx(ctx, w.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := eventsdb.New(tx)
		fresh, err := q.InsertInbox(ctx, eventsdb.InsertInboxParams{Subscriber: sub.Subscriber, EventID: e.ID})
		if err != nil || fresh == 0 {
			return err // 0 — уже обработано этим подписчиком
		}
		if sub.Delivery == LatestState {
			key := eventsdb.EnsureCursorParams{Subscriber: sub.Subscriber, AggregateType: e.AggregateType, AggregateID: e.AggregateID}
			if err := q.EnsureCursor(ctx, key); err != nil {
				return err
			}
			applied, err := q.LockCursor(ctx, eventsdb.LockCursorParams(key))
			if err != nil {
				return err
			}
			if e.AggregateVersion < applied {
				return nil // устаревшее: отметка inbox остаётся, эффекта нет
			}
		}
		if err := sub.Handle(ctx, tx, e); err != nil {
			return fmt.Errorf("events: %s: %w", sub.Subscriber, err)
		}
		if sub.Delivery == LatestState {
			return q.AdvanceCursor(ctx, eventsdb.AdvanceCursorParams{
				Version: e.AggregateVersion, Subscriber: sub.Subscriber,
				AggregateType: e.AggregateType, AggregateID: e.AggregateID,
			})
		}
		return nil
	})
}

// AddWorkers регистрирует задачи платформы событий в реестре воркера.
func AddWorkers(w *river.Workers, pool *pgxpool.Pool, reg *Registry) {
	river.AddWorker(w, NewDeliverWorker(pool, pool, reg))
}
