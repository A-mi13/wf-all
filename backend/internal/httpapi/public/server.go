package public

import (
	"context"

	geohttp "wf/backend/internal/geo/httpapi"
	"wf/backend/internal/httpapi/public/oapi"
	identityhttp "wf/backend/internal/identity/httpapi"
	"wf/backend/internal/platform/appversion"
)

// Server собирает публичный API: встраивает хендлеры модулей (internal/<модуль>/httpapi),
// операции платформы (тег platform) реализует сам. Компилятор ловит и нереализованную
// операцию, и реализованную двумя модулями (неоднозначный метод) — спека §8.2.
type Server struct {
	geohttp.Handler // тег geo: справочник городов, автоопределение
	// тег identity: вход, аккаунт, сессии; нулевое значение — 501 на каждую операцию (план I-1, R2)
	identityHandler
	versions appversion.Reader
}

// identityHandler — имя встроенного поля: хендлеры модулей называются одинаково (Handler), а второй встроенный
// Handler — ошибка компиляции «duplicate field». Модуль-реализатора apitest.Owners берёт из пакета типа.
type identityHandler = identityhttp.Handler

var _ oapi.StrictServerInterface = Server{}

// cacheMinVersion — min-version кэшируется на минуту (спека geo §4.2): смена версий в админке
// доходит до приложений не позже чем через минуту.
const cacheMinVersion = "public, max-age=60"

// GetAppMinVersion — GET /v1/app/min-version (тег platform). Незаведённая платформа — 200 с
// null-полями: приложение работает без проверки версии.
func (s Server) GetAppMinVersion(ctx context.Context, req oapi.GetAppMinVersionRequestObject) (oapi.GetAppMinVersionResponseObject, error) {
	// незнакомую платформу отверг валидатор контракта (400 enum); своей проверки нет — её нельзя
	// покрыть тестом, а пропущенное значение лишь не найдёт строку (CHECK таблицы) → null-поля
	p := req.Params.Platform
	v, ok, err := s.versions.Get(ctx, appversion.Platform(p))
	if err != nil {
		return nil, err
	}
	body := oapi.AppMinVersion{Platform: p}
	if ok {
		body.MinVersion, body.RecommendedVersion, body.StoreUrl = &v.Min, &v.Recommended, &v.StoreURL
	}
	return oapi.GetAppMinVersion200JSONResponse{Body: body,
		Headers: oapi.GetAppMinVersion200ResponseHeaders{CacheControl: cacheMinVersion}}, nil
}
