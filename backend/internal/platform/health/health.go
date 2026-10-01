// Package health — пробы для инфраструктуры (спека бэкенда §6.9), вне контракта API:
// /healthz — процесс жив (без обращения к зависимостям), /readyz — готов принимать трафик
// (база отвечает). Пробы обходят логи доступа: хостинг дёргает их постоянно.
package health

import (
	"context"
	"net/http"
	"time"
)

// readyTimeout — дольше проба не ждёт: зависшая база — «не готов», а не зависшая проба.
const readyTimeout = 2 * time.Second

func Handler(ready func(context.Context) error, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			write(w, http.StatusOK, "ok")
		case "/readyz":
			ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
			defer cancel()
			if err := ready(ctx); err != nil {
				// причину не раскрываем: в ней адреса и устройство инфраструктуры
				write(w, http.StatusServiceUnavailable, "unavailable")
				return
			}
			write(w, http.StatusOK, "ok")
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func write(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"status":"` + s + `"}`))
}
