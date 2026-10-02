// Command admin-api — API админки. Отдельный бинарник, домен и порт.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "admin-api:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.Admin]("ADMIN_", environ)
	if err != nil {
		return err
	}
	// лимиты и доверие к заголовкам проверяются до подключения к базе
	rules, err := ratelimit.ParseRules(cfg.RateLimits, ratelimit.DefaultRules())
	if err != nil {
		return err
	}
	if err := rules.Validate(); err != nil {
		return err
	}
	// BFF у админки нет: её фронт ходит в API напрямую
	pc := peer.Config{TrustedProxies: cfg.TrustedProxies, ClientIPHeader: cfg.ClientIPHeader}
	if err := pc.Validate(); err != nil {
		return err
	}
	return server.RunHTTP(ctx, "admin-api", server.HTTPConfig{Log: cfg.Log, HTTP: cfg.HTTP, DB: cfg.DB}, logOut,
		func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error) {
			return admin.NewHandler(log, admin.Options{
				Docs:           cfg.HTTP.DocsEnabled,
				RequestTimeout: cfg.HTTP.RequestTimeout,
				Peer:           pc,
				DB:             pool,
				Limiter:        ratelimit.NewPG(pool, clock.System),
				RateRules:      rules,
			})
		})
}
