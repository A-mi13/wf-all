package admin

// Версии нативного приложения (тег platform, спека §6.1, §3.2; план B, Task 15): список обеих платформ и запись с
// событием platform.app_versions_changed и аудитом в одной транзакции под Idempotency-Key.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
)

// cachePrivate — ответы чтения платформы зависят от сотрудника (контракт: CachePrivate).
const cachePrivate = "private, no-store"

// platforms — платформы списка версий: обе всегда, по имени.
var platforms = []appversion.Platform{appversion.Android, appversion.IOS}

// ListAppVersions — GET /v1/app/versions: обе платформы; незаведённая — элемент с null-полями (как публичный
// min-version).
func (s Server) ListAppVersions(ctx context.Context, _ oapi.ListAppVersionsRequestObject) (oapi.ListAppVersionsResponseObject, error) {
	if s.audit == nil {
		return nil, errNotImplemented
	}
	rows, err := appversion.List(ctx, s.audit)
	if err != nil {
		return nil, err
	}
	have := map[appversion.Platform]appversion.Versions{}
	for _, r := range rows {
		have[r.Platform] = r
	}
	body := oapi.AppVersionsList{Items: make([]oapi.AppVersions, 0, len(platforms))}
	for _, p := range platforms {
		if v, ok := have[p]; ok {
			body.Items = append(body.Items, appVersionsOut(v))
		} else {
			body.Items = append(body.Items, oapi.AppVersions{Platform: oapi.AppPlatform(p)})
		}
	}
	return oapi.ListAppVersions200JSONResponse{Body: body, Headers: oapi.ListAppVersions200ResponseHeaders{CacheControl: cachePrivate}}, nil
}

// PutAppVersions — PUT /v1/app/versions/{platform}: appversion.Validate (все нарушения сразу — 400 с полями
// body.*/path.platform), затем одна транзакция: запись (version + 1), событие и аудит сотрудника (before/after, без
// города). Те же значения — 200 с текущей строкой без события и аудита.
func (s Server) PutAppVersions(ctx context.Context, req oapi.PutAppVersionsRequestObject) (oapi.PutAppVersionsResponseObject, error) {
	if s.audit == nil || s.clock == nil {
		return nil, errNotImplemented
	}
	st, err := staffFrom(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, httpx.NewFieldError("body", "required")
	}
	v := appversion.Versions{Platform: appversion.Platform(req.Platform), Min: req.Body.MinVersion,
		Recommended: req.Body.RecommendedVersion, StoreURL: req.Body.StoreUrl}
	if err := appversion.Validate(v); err != nil {
		var ve appversion.ValidationError
		if !errors.As(err, &ve) {
			return nil, err
		}
		return nil, versionFieldErrors(ve)
	}
	m := metaFrom(ctx)
	var after appversion.Versions
	err = db.InTx(ctx, s.audit, func(ctx context.Context, tx pgx.Tx) error {
		before, a, err := appversion.Put(ctx, tx, v, s.clock.Now())
		if err != nil {
			return err
		}
		after = a
		if before != nil && before.Version == a.Version {
			return nil // ничего не изменилось
		}
		if _, err := events.Publish(ctx, appversion.ChangedEvent(a)); err != nil {
			return err
		}
		var was any
		if before != nil {
			was = versionFields(*before)
		}
		return audit.Write(ctx, audit.Entry{
			ActorStaffID: st.StaffID, ActorRole: st.Role, ActorLabel: st.Label, Action: appversion.ActionChanged,
			ObjectType: appversion.AggregateVersion, ObjectID: appversion.AggregateID(a.Platform),
			Before: was, After: versionFields(a), IP: m.IP, UserAgent: m.UserAgent,
		})
	})
	if err != nil {
		return nil, err
	}
	return oapi.PutAppVersions200JSONResponse(appVersionsOut(after)), nil
}

// versionFieldErrors — нарушения Validate полями контракта: платформа — path.platform, остальное — body.<поле>.
func versionFieldErrors(ve appversion.ValidationError) error {
	out := make([]httpx.FieldError, 0, len(ve))
	for _, f := range ve {
		field := "body." + f.Field
		if f.Field == "platform" {
			field = "path.platform"
		}
		out = append(out, httpx.FieldError{Field: field, Code: f.Code})
	}
	return httpx.NewFieldErrors(out)
}

// versionFields — значения строки для before/after аудита.
func versionFields(v appversion.Versions) map[string]any {
	return map[string]any{"min_version": v.Min, "recommended_version": v.Recommended, "store_url": v.StoreURL}
}

func appVersionsOut(v appversion.Versions) oapi.AppVersions {
	updated := v.UpdatedAt.UTC()
	return oapi.AppVersions{Platform: oapi.AppPlatform(v.Platform), MinVersion: &v.Min, RecommendedVersion: &v.Recommended,
		StoreUrl: &v.StoreURL, Version: &v.Version, UpdatedAt: &updated}
}
