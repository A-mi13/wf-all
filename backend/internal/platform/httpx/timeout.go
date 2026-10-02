package httpx

import (
	"context"
	"net/http"
	"time"
)

// Timeout — крайний срок запроса в ctx (спека §6.1): запросы к базе и исходящие вызовы
// прерываются по нему. Ответ не подменяется — хендлер сам вернёт ошибку отмены.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
