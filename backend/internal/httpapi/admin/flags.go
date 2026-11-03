package admin

// Флаги функций (тег platform, спека §6.1, §3.2; план B, Task 15): список и правка с оптимистичной версией. Города
// флага проверяет этот слой через geo.Service.ExistingCityIDs (R19): platform не импортирует модули. Города не
// удаляются (у admin нет DELETE на cities) — проверка до записи без гонки.

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
)

var (
	errFlagNotFound     = httpx.NewError(http.StatusNotFound, httpx.CodeFlagNotFound)
	errFlagStale        = httpx.NewError(http.StatusConflict, httpx.CodeFlagStale)
	errFlagCityNotFound = httpx.NewError(http.StatusUnprocessableEntity, httpx.CodeFlagCityNotFound)
)

// ListFeatureFlags — GET /v1/flags: все флаги по ключу.
func (s Server) ListFeatureFlags(ctx context.Context, _ oapi.ListFeatureFlagsRequestObject) (oapi.ListFeatureFlagsResponseObject, error) {
	if s.flags == nil {
		return nil, errNotImplemented
	}
	list, err := s.flags.List(ctx)
	if err != nil {
		return nil, err
	}
	body := oapi.FeatureFlagList{Items: make([]oapi.FeatureFlag, 0, len(list))}
	for _, f := range list {
		body.Items = append(body.Items, flagOut(f))
	}
	return oapi.ListFeatureFlags200JSONResponse{Body: body, Headers: oapi.ListFeatureFlags200ResponseHeaders{CacheControl: cachePrivate}}, nil
}

// UpdateFeatureFlag — PATCH /v1/flags/{key}: version обязательна; enabled_city_ids — множество (повторы схлопываются,
// порядок по id); несуществующий город — 422 platform.flag_city_not_found до записи; затем одна транзакция под
// Idempotency-Key: flags.Store.Update (нет флага — 404 platform.flag_not_found, version не та — 409
// platform.flag_stale), событие platform.flag_changed и аудит сотрудника. Ничего не изменилось — 200 с текущим
// флагом без события и аудита, version не растёт.
func (s Server) UpdateFeatureFlag(ctx context.Context, req oapi.UpdateFeatureFlagRequestObject) (oapi.UpdateFeatureFlagResponseObject, error) {
	if s.flags == nil || s.cities == nil {
		return nil, errNotImplemented
	}
	st, err := staffFrom(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, httpx.NewFieldError("body", "required")
	}
	var cities []uuid.UUID
	if req.Body.EnabledCityIds != nil {
		cities = flags.CitySet(*req.Body.EnabledCityIds)
		found, err := s.cities(ctx, cities)
		if err != nil {
			return nil, err
		}
		if len(found) != len(cities) {
			return nil, errFlagCityNotFound
		}
	}
	m := metaFrom(ctx)
	var after flags.Flag
	err = db.InTx(ctx, s.audit, func(ctx context.Context, tx pgx.Tx) error {
		before, a, err := s.flags.Update(ctx, tx, req.Key, req.Body.Version, req.Body.EnabledGlobally, cities)
		if err != nil {
			return err
		}
		after = a
		if before.Version == a.Version {
			return nil // ничего не изменилось
		}
		if _, err := events.Publish(ctx, flags.ChangedEvent(a)); err != nil {
			return err
		}
		return audit.Write(ctx, audit.Entry{
			ActorStaffID: st.StaffID, ActorRole: st.Role, ActorLabel: st.Label, Action: flags.ActionFlagChanged,
			ObjectType: flags.AggregateFlag, ObjectID: flags.FlagID(a.Key),
			Before: flagFields(before), After: flagFields(a), IP: m.IP, UserAgent: m.UserAgent,
		})
	})
	switch {
	case errors.Is(err, flags.ErrUnknownFlag):
		return nil, errFlagNotFound
	case errors.Is(err, flags.ErrStale):
		return nil, errFlagStale
	case err != nil:
		return nil, err
	}
	return oapi.UpdateFeatureFlag200JSONResponse(flagOut(after)), nil
}

// flagFields — значения флага для before/after аудита (только id городов).
func flagFields(f flags.Flag) map[string]any {
	return map[string]any{"enabled_globally": f.EnabledGlobally, "enabled_city_ids": f.EnabledCityIDs}
}

func flagOut(f flags.Flag) oapi.FeatureFlag {
	return oapi.FeatureFlag{Key: oapi.FeatureFlagKey(f.Key), Description: f.Description, EnabledGlobally: f.EnabledGlobally,
		EnabledCityIds: f.EnabledCityIDs, Version: f.Version, UpdatedAt: f.UpdatedAt.UTC()}
}
