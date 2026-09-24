// Command api — публичный HTTP API для веба и приложений. Ничего админского сюда не линкуется.
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

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Environ(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.API]("API_", environ)
	if err != nil {
		return err
	}
	return server.RunHTTP(ctx, "api", server.HTTPConfig(cfg), logOut,
		func(log *slog.Logger, _ *pgxpool.Pool) http.Handler { return public.NewHandler(log) })
}
