// Package health — пробы для инфраструктуры (спека бэкенда §6.9), вне контракта API:
// /healthz — процесс жив (без обращения к зависимостям), /readyz — готов принимать трафик
// (база отвечает). Пробы обходят логи доступа: хостинг дёргает их постоянно.
package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
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

// Cached — результат ready не чаще раза в ttl, одновременные пробы ждут одну проверку: поток
// запросов к открытому /readyz не превращается в поток пингов базы. Ошибка тоже кэшируется на
// ttl. Проверка не обрывается отменой одной из проб, но ограничена readyTimeout.
func Cached(ready func(context.Context) error, ttl time.Duration, now func() time.Time) func(context.Context) error {
	var (
		mu      sync.Mutex
		last    error
		expires time.Time
		group   singleflight.Group
	)
	return func(ctx context.Context) error {
		mu.Lock()
		if now().Before(expires) {
			err := last
			mu.Unlock()
			return err
		}
		mu.Unlock()
		_, err, _ := group.Do("ready", func() (any, error) {
			// проба могла опоздать к общей проверке, которая только что закончилась, — её
			// результат уже в кэше
			mu.Lock()
			if now().Before(expires) {
				err := last
				mu.Unlock()
				return nil, err
			}
			mu.Unlock()
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), readyTimeout)
			defer cancel()
			err := ready(c)
			mu.Lock()
			last, expires = err, now().Add(ttl)
			mu.Unlock()
			return nil, err
		})
		return err
	}
}
