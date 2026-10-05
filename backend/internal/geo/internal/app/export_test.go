package app

import (
	"context"

	"wf/backend/internal/geo/internal/source"
)

// SetFetch — подмена загрузки GeoNames в тестах импорта.
func (im *Importer) SetFetch(f func(context.Context, source.FetchConfig, string) (source.Raw, error)) {
	im.fetch = f
}
