package jobs

import (
	"context"

	"github.com/google/uuid"

	"wf/backend/internal/geo/internal/app"
)

type importFunc func(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error)

func (f importFunc) Import(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error) {
	return f(ctx, country, startedBy, riverJobID)
}

// NewImportWorkerWith — воркер с подменённым импортом (тесты Work и RunNow).
func NewImportWorkerWith(f func(ctx context.Context, country string, startedBy *uuid.UUID, riverJobID *int64) (app.ImportResult, error)) *ImportWorker {
	return &ImportWorker{im: importFunc(f)}
}
