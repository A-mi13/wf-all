package media

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/media/internal/store/mediadb"
)

// checkPlayerLimits — в транзакции «начать» под замком игрока (m5): живых незавершённых билетов (срок позже now) не
// больше maxPending − 1 до вставки, заявленный объём за окно media_bytes_day (граница строгая: объект ровно на границе
// уже не считается) вместе с новым — не больше предела (R19). Под одним замком два параллельных «начать» не проходят
// ни шестым билетом, ни сверх квоты.
func (s *service) checkPlayerLimits(ctx context.Context, q *mediadb.Queries, user uuid.UUID, now time.Time, bytes int64) error {
	if err := q.PendingLock(ctx, user); err != nil {
		return fmt.Errorf("media: замок незавершённых: %w", err)
	}
	n, err := q.CountPendingByUser(ctx, mediadb.CountPendingByUserParams{UploadedByUser: user, LiveAfter: now})
	if err != nil {
		return fmt.Errorf("media: незавершённые: %w", err)
	}
	if n >= maxPending {
		return ErrTooManyPending
	}
	total, err := q.SumDeclaredBytesByUser(ctx, mediadb.SumDeclaredBytesByUserParams{UploadedByUser: user,
		Since: now.Add(-s.bytesDay.Period)})
	if err != nil {
		return fmt.Errorf("media: суточный объём: %w", err)
	}
	if total+bytes > int64(s.bytesDay.Limit) {
		return ErrQuotaExceeded
	}
	return nil
}
