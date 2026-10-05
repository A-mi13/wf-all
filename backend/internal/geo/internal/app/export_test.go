package app

import (
	"context"

	"github.com/google/uuid"

	"wf/backend/internal/geo/internal/source"
)

// SetFetch — подмена загрузки GeoNames в тестах импорта.
func (im *Importer) SetFetch(f func(context.Context, source.FetchConfig, string) (source.Raw, error)) {
	im.fetch = f
}

// SetBeforeLink — хук сверки: зовётся после отбора кандидатов, перед привязкой города к месту
// (тест гонки с активацией того же места админом). В проде nil.
func (im *Importer) SetBeforeLink(f func(ctx context.Context, cityID uuid.UUID, geonameID int64)) {
	im.beforeLink = f
}
