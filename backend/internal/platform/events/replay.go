package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events/eventsdb"
)

// ErrUnknownSubscription — подписчик не подписан на этот тип события в реестре воркера.
var ErrUnknownSubscription = errors.New("events: нет такой подписки")

// Replay заново раскладывает события типа eventType начиная с since одному подписчику (спека
// §6.5): подписчик появился позже события или пропустил его. Уже обработанное отсечёт
// event_inbox, уже стоящее в очереди — уникальность River. Outbox хранит 30 дней.
func Replay(ctx context.Context, pool db.TxStarter, ins Inserter, reg *Registry,
	eventType, subscriber string, since time.Time) (int, error) {
	if _, ok := reg.Get(subscriber, eventType); !ok {
		return 0, fmt.Errorf("%w: %s на %s", ErrUnknownSubscription, subscriber, eventType)
	}
	var inserted int
	err := db.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		ids, err := eventsdb.New(tx).EventIDsForReplay(ctx, eventsdb.EventIDsForReplayParams{EventType: eventType, Since: since})
		if err != nil {
			return err
		}
		const chunk = 1000
		for start := 0; start < len(ids); start += chunk {
			params := make([]river.InsertManyParams, 0, chunk)
			for _, eventID := range ids[start:min(start+chunk, len(ids))] {
				params = append(params, river.InsertManyParams{Args: NewDeliverArgs(eventID, subscriber)})
			}
			res, err := ins.InsertManyTx(ctx, tx, params)
			if err != nil {
				return err
			}
			for _, r := range res {
				if !r.UniqueSkippedAsDuplicate {
					inserted++
				}
			}
		}
		return nil
	})
	return inserted, err
}
