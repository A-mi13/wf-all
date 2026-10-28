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

// SetStageHook — хук активации (тесты гонок): stage "region" — перед выбором региона, "insert" — после проверки
// занятости slug, перед вставкой города (в транзакции сценария). В проде nil.
func (a *Admin) SetStageHook(f func(ctx context.Context, stage string)) {
	a.hook = f
}

// CountryLockKey — ключ advisory-замка страны (сверка ↔ активация, ревью G5 I1-r1).
func CountryLockKey(country string) (int32, int32) { return countryLockKey(country) }
