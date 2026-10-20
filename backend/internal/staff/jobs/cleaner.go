package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

// CleanupInterval — как часто проходит чистка (§4.10).
const CleanupInterval = time.Hour

// Cleaner — чистка staff.cleanup (P27): не периодическая задача River (её ставит только лидер River, а
// чистке нужны права admin), а тикер в процессе admin-api. Каждый экземпляр чистит сам: порции SKIP LOCKED
// не мешают друг другу.
type Cleaner struct {
	app       *app.App
	approvals *approvals.Service
	pool      *pgxpool.Pool // ключи идемпотентности admin-api (staff_idempotency_keys)
	log       *slog.Logger
	// pass — один проход (Tick), tick — тики Run (nil — time.Ticker на CleanupInterval); тест подменяет оба
	pass func(context.Context) error
	tick <-chan time.Time
}

// NewCleaner — чистка на зависимостях admin-api и сервисе подтверждений сборки.
func NewCleaner(d staff.Deps, svc *approvals.Service) *Cleaner {
	c := &Cleaner{app: app.New(d), approvals: svc, pool: d.Pool, log: d.Log}
	c.pass = c.Tick
	return c
}

// Tick — один проход: сессии и приглашения staff старше 30 суток (App.Cleanup), просроченные подтверждения
// → expired (ExpireDue), решённые старше 180 суток удаляются (PurgeDecided), просроченные ключи идемпотентности
// admin-api (staff_idempotency_keys — у воркера на них прав нет, финальное ревью I-1) удаляются. Шаги
// независимы: сбой одного не отменяет остальные, ошибки — вместе. Итог — строкой журнала с числами.
func (c *Cleaner) Tick(ctx context.Context) error {
	rep, errStaff := c.app.Cleanup(ctx)
	expired, errExpire := c.approvals.ExpireDue(ctx)
	purged, errPurge := c.approvals.PurgeDecided(ctx, approvals.DecidedRetention)
	keys, errKeys := idempotency.DeleteExpired(ctx, c.pool, idempotency.Staff)
	c.log.InfoContext(ctx, "staff: чистка", "sessions", rep.Sessions, "invites", rep.Invites,
		"approvals_expired", expired, "approvals_purged", purged, "idempotency_keys", keys)
	if err := errors.Join(errStaff, errExpire, errPurge, errKeys); err != nil {
		return fmt.Errorf("staff: чистка: %w", err)
	}
	return nil
}

// Run — проход сразу, затем раз в CleanupInterval до отмены ctx. Ошибка прохода пишется в журнал сервера и
// сервис не роняет: следующий тик попробует снова. Отмена ctx посреди прохода (остановка сервиса) обрывает
// текущую порцию (она откатывается) — это не сбой.
func (c *Cleaner) Run(ctx context.Context) {
	tick := c.tick
	if tick == nil {
		t := time.NewTicker(CleanupInterval)
		defer t.Stop()
		tick = t.C
	}
	for {
		if err := c.pass(ctx); err != nil && ctx.Err() == nil {
			c.log.ErrorContext(ctx, "staff: проход чистки не удался", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}
