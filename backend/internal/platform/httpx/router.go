package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter — роутер с общим набором middleware. Бинарники только монтируют свои маршруты.
func NewRouter(log *slog.Logger) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, accessLog(log), recoverer(log))
	// chi собирает цепочку middleware лениво — только при первой регистрации
	// маршрута (Handle/Get/...). Без этого вызова, пока в роутере нет ни одного
	// маршрута, 404/405 идут в NotFoundHandler/MethodNotAllowedHandler мимо
	// RequestID/лога/recoverer напрямую из Mux.ServeHTTP.
	_ = r.With()
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusNotFound, "http.not_found", "")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusMethodNotAllowed, "http.method_not_allowed", "")
	})
	return r
}

func accessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.InfoContext(r.Context(), "http",
				"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()))
		})
	}
}

func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // так сигналит net/http
						panic(v)
					}
					log.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()))
					WriteProblem(w, r, http.StatusInternalServerError, "internal", "")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
