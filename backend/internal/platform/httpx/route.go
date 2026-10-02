package httpx

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Route — операция контракта, на которую пришёл запрос. Кладёт Routes; читают аутентификация
// (security), валидатор и rate limit (x-rate-limit) — маршрут ищется один раз на запрос. Идемпотентность маршрут не
// читает: ей нужен фактический путь (r.URL.Path).
type Route struct {
	route  *routers.Route
	params map[string]string
}

func (r *Route) Operation() *openapi3.Operation { return r.route.Operation }

// Extension — строковое расширение операции (x-rate-limit); нет или не строка — "".
func (r *Route) Extension(name string) string {
	s, _ := r.route.Operation.Extensions[name].(string)
	return s
}

// AcceptsBearer — есть ли в security операции (без своего — в общем security документа) схема
// HTTP Bearer, обязательная или опциональная ([{}, {bearer: []}]). Нет — операция только
// анонимная (security: [] или эквивалент): слой аутентификации заголовок Authorization не читает.
func (r *Route) AcceptsBearer() bool {
	security := r.route.Operation.Security
	if security == nil {
		security = &r.route.Spec.Security
	}
	var schemes openapi3.SecuritySchemes
	if c := r.route.Spec.Components; c != nil {
		schemes = c.SecuritySchemes
	}
	for _, req := range *security {
		for name := range req {
			if s := schemes[name]; s != nil && s.Value != nil &&
				s.Value.Type == "http" && strings.EqualFold(s.Value.Scheme, "bearer") {
				return true
			}
		}
	}
	return false
}

type routeKey struct{}

func RouteFrom(ctx context.Context) (*Route, bool) {
	rt, ok := ctx.Value(routeKey{}).(*Route)
	return rt, ok
}

// Routes находит операцию контракта по методу и пути. Маршрут не из контракта (/docs, 404,
// чужой метод) идёт дальше без Route: следующие слои его пропускают, отвечает роутер.
func Routes(spec *openapi3.T) (func(http.Handler) http.Handler, error) {
	spec.Servers = nil // иначе FindRoute сверяет хост и префикс из servers
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("httpx: роутер контракта: %w", err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if route, params, err := router.FindRoute(r); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), routeKey{}, &Route{route: route, params: params}))
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
