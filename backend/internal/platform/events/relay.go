package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
)

// Channel — канал NOTIFY триггера outbox_notify (миграция 0017).
const Channel = "wf_outbox"

// Inserter ставит задачи River в транзакции; его реализует *river.Client[pgx.Tx].
type Inserter interface {
	InsertManyTx(ctx context.Context, tx pgx.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// RelayConfig — настройки relay; Batch и Poll должны быть больше нуля.
type RelayConfig struct {
	Batch int           // событий за одну транзакцию
	Poll  time.Duration // страховочный опрос, если NOTIFY потерялся
}

// Relay раскладывает события outbox подписчикам: на каждого — задача River в той же
// транзакции, что и отметка published_at (спека §6.5). Инстансов воркера может быть сколько
// угодно: строки делятся через FOR UPDATE SKIP LOCKED, задачи уникальны по аргументам.
type Relay struct {
	pool *pgxpool.Pool
	ins  Inserter
	reg  *Registry
	log  *slog.Logger
	cfg  RelayConfig
}

// NewRelay: pool — транзакции пачек и конфиг отдельного LISTEN-соединения, ins — постановка
// задач доставки в той же транзакции, reg — подписки этого релиза воркера.
func NewRelay(pool *pgxpool.Pool, ins Inserter, reg *Registry, log *slog.Logger, cfg RelayConfig) *Relay {
	return &Relay{pool: pool, ins: ins, reg: reg, log: log, cfg: cfg}
}

// Run работает до отмены ctx. LISTEN держится на отдельном соединении вне пула: с PgBouncer
// в transaction mode он несовместим (docs/deploy-dev.md).
func (r *Relay) Run(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Go(func() { r.listen(ctx, wake) })
	defer wg.Wait()
	poll := time.NewTicker(r.cfg.Poll)
	defer poll.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			r.log.ErrorContext(ctx, "events: relay", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-poll.C:
		}
	}
}

// Drain публикует всё неопубликованное пачками по cfg.Batch.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	total := 0
	for {
		n, err := r.publishBatch(ctx)
		total += n
		if err != nil || n < r.cfg.Batch {
			return total, err
		}
	}
}

func (r *Relay) publishBatch(ctx context.Context) (int, error) {
	var n int
	err := db.InTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := eventsdb.New(tx)
		rows, err := q.LockUnpublished(ctx, int32(r.cfg.Batch)) //nolint:gosec // размер пачки из конфига, малый
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		var params []river.InsertManyParams
		for _, row := range rows {
			ids = append(ids, row.ID)
			for _, s := range r.reg.For(row.EventType) {
				// опции берутся из DeliverArgs.InsertOpts: очередь events, уникальность по аргументам
				params = append(params, river.InsertManyParams{Args: NewDeliverArgs(row.ID, s.Subscriber)})
			}
		}
		if len(params) > 0 {
			if _, err := r.ins.InsertManyTx(ctx, tx, params); err != nil {
				return fmt.Errorf("events: задачи доставки: %w", err)
			}
		}
		if err := q.MarkPublished(ctx, ids); err != nil {
			return err
		}
		n = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// listen будит Run по NOTIFY; обрыв — переподключение с растущей паузой. После каждого
// подключения — сигнал: события, вставленные без слушателя, забираются сразу.
func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	backoff := time.Second
	for {
		connected, err := r.listenOnce(ctx, wake)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = time.Second
		}
		r.log.WarnContext(ctx, "events: LISTEN оборвался, переподключаюсь", "err", err, "pause", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (r *Relay) listenOnce(ctx context.Context, wake chan<- struct{}) (bool, error) {
	conn, err := pgx.ConnectConfig(ctx, r.pool.Config().ConnConfig.Copy())
	if err != nil {
		return false, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return false, err
	}
	signal(wake)
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return true, err
		}
		signal(wake)
	}
}

func signal(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default: // сигнал уже ждёт — Drain заберёт всё разом
	}
}
