// Package server — общий жизненный цикл HTTP-бинарников: api и admin-api отличаются
// только префиксом конфига и хендлером.
package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/health"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/logx"
)

type HTTPConfig struct {
	Log  config.Log
	HTTP config.HTTP
	DB   config.DB
}

func RunHTTP(ctx context.Context, name string, c HTTPConfig, logOut io.Writer,
	build func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error)) error {
	log := logx.New(logOut, c.Log).With("service", name)
	// сторонние библиотеки и стандартный log — через тот же логгер с маскированием ПД
	slog.SetDefault(log)
	pool, err := db.Open(ctx, c.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	// хендлер собирается до открытия порта: битый контракт — отказ старта, а не полуживой сервер
	h, err := build(log, pool)
	if err != nil {
		return err
	}
	// пробы — до хендлера бинарника: без логов доступа и проверки по контракту; /readyz
	// открыт — пинг базы не чаще раза в секунду
	h = health.Handler(health.Cached(pool.Ping, time.Second, time.Now), h)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", c.HTTP.Addr)
	if err != nil {
		return err
	}
	log.Info("слушаю", "addr", ln.Addr().String())
	return httpx.Serve(ctx, ln, h, c.HTTP.ShutdownTimeout)
}
