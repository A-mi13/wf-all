package jobs

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"wf/backend/internal/geo/internal/app"
	"wf/backend/internal/platform/clock"
)

type importFunc func(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error)

func (f importFunc) Import(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error) {
	return f(ctx, country, startedBy, riverJobID)
}

// NewImportWorkerWith — воркер с подменённым импортом и часами (тесты Work и RunNow).
func NewImportWorkerWith(clk clock.Clock, f func(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error)) *ImportWorker {
	return &ImportWorker{im: importFunc(f), clk: clk, log: slog.New(slog.DiscardHandler)}
}
