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
	"wf/backend/internal/platform/config"
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
	return server.RunHTTP(ctx, "admin-api", server.HTTPConfig(cfg), logOut,
		func(log *slog.Logger, _ *pgxpool.Pool) http.Handler { return admin.NewHandler(log) })
}
