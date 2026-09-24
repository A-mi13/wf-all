// Package server — общий жизненный цикл HTTP-бинарников: api и admin-api отличаются
// только префиксом конфига и хендлером.
package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/logx"
)

type HTTPConfig struct {
	Log  config.Log
	HTTP config.HTTP
	DB   config.DB
}

func RunHTTP(ctx context.Context, name string, c HTTPConfig, logOut io.Writer,
	build func(log *slog.Logger, pool *pgxpool.Pool) http.Handler) error {
	log := logx.New(logOut, c.Log).With("service", name)
	pool, err := db.Open(ctx, c.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	ln, err := net.Listen("tcp", c.HTTP.Addr)
	if err != nil {
		return err
	}
	log.Info("слушаю", "addr", ln.Addr().String())
	return httpx.Serve(ctx, ln, build(log, pool), c.HTTP.ShutdownTimeout)
}
