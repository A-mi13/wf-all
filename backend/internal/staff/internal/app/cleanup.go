package app

import (
	"context"
	"fmt"
	"time"

	"wf/backend/internal/staff/internal/store/staffdb"
)

// CleanupRetention — сколько хранятся умершие сессии и закрытые или просроченные приглашения (§4.10).
const CleanupRetention = 30 * 24 * time.Hour

// cleanupBatch — строк в одной порции чистки (тесты меняют через SetCleanupBatch).
var cleanupBatch int32 = 500

// CleanupReport — сколько строк удалил проход чистки.
type CleanupReport struct{ Invites, Sessions int64 }

// Cleanup — один проход чистки staff (§4.10, P27; тикер admin-api staff/jobs.Cleaner): сессии, умершие
// (истёкшие или погашенные) больше CleanupRetention назад, и приглашения, погашенные, отозванные или
// просроченные больше CleanupRetention назад, кроме последнего приглашения сотрудника (по нему повторная
// отправка берёт назначение). Порциями по cleanupBatch, каждая — один оператор с SKIP LOCKED: долгих
// замков нет, два экземпляра admin-api не мешают друг другу. Граница — строго старше now − 30 суток.
// Отмена ctx (остановка сервиса) обрывает текущую порцию — она откатывается, удалённое раньше остаётся
// удалённым; ошибка — с тем, что успели удалить.
func (a *App) Cleanup(ctx context.Context) (CleanupReport, error) {
	before := a.clock.Now().Add(-CleanupRetention)
	q := staffdb.New(a.pool)
	var rep CleanupReport
	var err error
	if rep.Sessions, err = drain(ctx, func(ctx context.Context) (int64, error) {
		return q.CleanupSessionsBatch(ctx, staffdb.CleanupSessionsBatchParams{Before: before, Lim: cleanupBatch})
	}); err != nil {
		return rep, fmt.Errorf("staff: чистка сессий: %w", err)
	}
	if rep.Invites, err = drain(ctx, func(ctx context.Context) (int64, error) {
		return q.CleanupInvitesBatch(ctx, staffdb.CleanupInvitesBatchParams{Before: before, Lim: cleanupBatch})
	}); err != nil {
		return rep, fmt.Errorf("staff: чистка приглашений: %w", err)
	}
	return rep, nil
}

// drain — порции batch до первой неполной; итог — сколько удалено.
func drain(ctx context.Context, batch func(context.Context) (int64, error)) (int64, error) {
	var total int64
	for {
		n, err := batch(ctx)
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(cleanupBatch) {
			return total, nil
		}
	}
}
