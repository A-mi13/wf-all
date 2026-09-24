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
	// r.Use применяется к обычным маршрутам, но не к NotFound/MethodNotAllowed,
	// пока в роутере нет ни одного зарегистрированного маршрута (внутренний
	// mx.handler у chi ленивый — строится только при первом Handle/Get/...).
	// ensureRequestID подстраховывает этот случай, не задваивая ID, когда
	// внешняя цепочка уже отработала.
	r.NotFound(ensureRequestID(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusNotFound, "http.not_found", "")
	}))
	r.MethodNotAllowed(ensureRequestID(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, http.StatusMethodNotAllowed, "http.method_not_allowed", "")
	}))
	return r
}

// ensureRequestID гарантирует наличие request id в контексте, не перегенерируя его,
// если он уже проставлен внешней цепочкой middleware.RequestID.
func ensureRequestID(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if middleware.GetReqID(r.Context()) == "" {
			middleware.RequestID(next).ServeHTTP(w, r)
			return
		}
		next(w, r)
	}
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
