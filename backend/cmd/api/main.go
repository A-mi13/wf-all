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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/server"
	"wf/backend/internal/platform/token"
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
	// ключи, лимиты и доверие к заголовкам проверяются до подключения к базе
	keys, err := token.ParseKeys(cfg.Auth.JWTSeeds)
	if err != nil {
		return err
	}
	rules, err := ratelimit.ParseRules(cfg.RateLimits, ratelimit.DefaultRules())
	if err != nil {
		return err
	}
	if err := rules.Validate(); err != nil {
		return err
	}
	pc := peer.Config{TrustedProxies: cfg.Peer.TrustedProxies, BFFNets: cfg.Peer.BFFNets, BFFSecrets: cfg.Peer.BFFSecrets}
	if err := pc.Validate(); err != nil {
		return err
	}
	return server.RunHTTP(ctx, "api", server.HTTPConfig{Log: cfg.Log, HTTP: cfg.HTTP, DB: cfg.DB}, logOut,
		func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error) {
			// ключи PoW проверяются на старте; сценарии identity получат этот же PoW
			if _, err := humancheck.NewPoW(pool, clock.System, humancheck.PoWConfig{
				Keys: cfg.Humancheck.Keys, TTL: cfg.Humancheck.TTL, MaxNumber: cfg.Humancheck.MaxNumber,
			}); err != nil {
				return nil, err
			}
			return public.NewHandler(log, public.Options{
				Docs:           cfg.HTTP.DocsEnabled,
				RequestTimeout: cfg.HTTP.RequestTimeout,
				Peer:           pc,
				DB:             pool,
				Tokens:         token.NewIssuer(keys, clock.System),
				// сессий нет до спеки identity: любой токен — 401; identity подставит свой загрузчик
				Sessions:  auth.NewCachedLoader(auth.NoSessions, 5*time.Second, clock.System, 100_000),
				Limiter:   ratelimit.NewPG(pool, clock.System),
				RateRules: rules,
			})
		})
}
