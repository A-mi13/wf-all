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
	"runtime"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/cmd/internal/workerhost"
	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/public"
	"wf/backend/internal/identity"
	identityhttp "wf/backend/internal/identity/httpapi"
	"wf/backend/internal/platform/appversion"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/logx"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/peer"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/server"
	"wf/backend/internal/platform/token"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := dispatch(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "api:", err)
		os.Exit(exitCode(err))
	}
}

func run(ctx context.Context, environ []string, logOut io.Writer) error {
	cfg, err := config.Load[config.API]("API_", environ)
	if err != nil {
		return err
	}
	// ключи, лимиты и доверие к заголовкам проверяются до подключения к базе
	jwtKeys, err := token.ParseKeys(cfg.Auth.JWTSeeds)
	if err != nil {
		return err
	}
	if _, err := humancheck.ParseKeys(cfg.Humancheck.Keys); err != nil {
		return err
	}
	rules, err := ratelimit.ParseRules(cfg.RateLimits, ratelimit.DefaultRules())
	if err != nil {
		return err
	}
	if err := rules.Validate(); err != nil {
		return err
	}
	// identity: кольца, документы и база города по IP — до базы: битое значение — отказ старта
	secrets, err := keys.NewRing(cfg.Identity.SecretKeys)
	if err != nil {
		return fmt.Errorf("API_IDENTITY_SECRET_KEYS: %w", err)
	}
	devices, err := keys.NewRing(cfg.Identity.DeviceKeys)
	if err != nil {
		return fmt.Errorf("API_IDENTITY_DEVICE_KEYS: %w", err)
	}
	legal, err := identity.ParseLegalDocuments(cfg.LegalDocuments)
	if err != nil {
		return err
	}
	// /.well-known до веба (К4): битый API_WELLKNOWN_ANDROID — отказ старта здесь, до базы
	wellKnown, err := identityhttp.WellKnown(cfg.WellKnown.AppleAppIDs, cfg.WellKnown.Android)
	if err != nil {
		return err
	}
	pc := peer.Config{TrustedProxies: cfg.Peer.TrustedProxies, BFFNets: cfg.Peer.BFFNets, BFFSecrets: cfg.Peer.BFFSecrets,
		ClientIPHeader: cfg.Peer.ClientIPHeader}
	if err := pc.Validate(); err != nil {
		return err
	}
	loc, closeGeo, err := ipgeo.Open(cfg.IPGeoDB)
	if err != nil {
		return err
	}
	// закрывается после возврата RunHTTP/RunService — HTTP уже дренирован, фон остановлен: запрос в полёте не читает
	// закрытый mmap (иначе SIGSEGV maxminddb)
	defer func() { _ = closeGeo.Close() }()
	hc := server.HTTPConfig{Log: cfg.Log, HTTP: cfg.HTTP, DB: cfg.DB}
	build := func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error) {
		// PoW — проверка решения по риску в сценариях identity (humancheck.Require)
		pow, err := humancheck.NewPoW(pool, clock.System, humancheck.PoWConfig{
			Keys: cfg.Humancheck.Keys, TTL: cfg.Humancheck.TTL, MaxNumber: cfg.Humancheck.MaxNumber,
		})
		if err != nil {
			return nil, err
		}
		tokens := token.NewIssuer(jwtKeys, clock.System)
		limiter := ratelimit.NewPG(pool, clock.System, ratelimit.Players)
		idh, err := buildIdentity(log, pool, identity.Deps{Clock: clock.System, Secrets: secrets, Devices: devices,
			Limiter: limiter, LimiterTx: func(tx pgx.Tx) ratelimit.Limiter { return ratelimit.NewPG(tx, clock.System, ratelimit.Players) },
			Human: pow, Geo: geo.New(pool), Flags: flags.NewStore(pool), IPGeo: loc, Legal: legal,
			Tokens: tokens, TOTPIssuer: cfg.Identity.TOTPIssuer, CaptainChecks: captainChecks()})
		if err != nil {
			return nil, err
		}
		return public.NewHandler(log, public.Options{
			Docs:           cfg.HTTP.DocsEnabled,
			RequestTimeout: cfg.HTTP.RequestTimeout,
			Peer:           pc,
			DB:             pool,
			Tokens:         tokens,
			Identity:       idh,
			WellKnown:      wellKnown,
			// загрузчик identity (статус, отзыв, сроки, ограничения, допуски) под кэшем 5 с: отзыв действует ≤ 5 с
			Sessions:    auth.NewCachedLoader(identity.NewSessionLoader(pool, clock.System), 5*time.Second, clock.System, 100_000),
			Limiter:     limiter,
			RateRules:   rules,
			Geo:         geo.New(pool),
			AppVersions: appversion.NewReader(pool),
		})
	}
	if !cfg.EmbedWorker {
		// прод: фон — отдельный процесс worker, WORKER_* api не читает
		return server.RunHTTP(ctx, "api", hc, logOut, build)
	}
	// журнал предупреждений старта — тот же формат и вывод, что у сервиса (RunService создаёт свой позже)
	startLog := logx.New(logOut, cfg.Log).With("service", "api")
	p, err := prepareEmbedded(cfg, secrets, environ, startLog)
	if err != nil {
		return err
	}
	// второй пул — роль worker; закрывается после возврата RunService, т. е. после остановки фона
	wpool, err := db.Open(ctx, p.DB())
	if err != nil {
		// SIGTERM во время подключения (холодный старт Neon) — штатная остановка, как отмена фона (R11)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer wpool.Close()
	return server.RunService(ctx, "api", hc, logOut, func(log *slog.Logger, pool *pgxpool.Pool) (server.Service, error) {
		h, err := build(log, pool)
		if err != nil {
			return server.Service{}, err
		}
		wlog := log.With("component", "worker")
		// фон — только на wpool: пул api в воркер не попадает (у роли api нет прав фона, R14)
		return server.Service{Handler: h, Background: func(ctx context.Context) error {
			return workerhost.Run(ctx, p, wpool, wlog, embeddedStop())
		}}, nil
	})
}

// buildIdentity — ручки identity на пуле api (план I-2, Task 8): хэшер argon2id под семафором по числу ядер (R13),
// оценка риска на лимитере игроков, клиент River только для вставки (письма в транзакции действия), аттестация и
// внешние провайдеры выключены до I-3 (R2, R4). Остальное — d вызывающего.
func buildIdentity(log *slog.Logger, pool *pgxpool.Pool, d identity.Deps) (identityhttp.Handler, error) {
	hasher, err := password.NewHasher(password.DefaultParams(), max(1, runtime.GOMAXPROCS(0)))
	if err != nil {
		return identityhttp.Handler{}, err
	}
	assessor, err := risk.NewAssessor(d.Limiter, risk.DefaultPolicies())
	if err != nil {
		return identityhttp.Handler{}, err
	}
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		return identityhttp.Handler{}, err
	}
	d.Pool, d.Hasher, d.Risk, d.River, d.Log = pool, hasher, assessor, ins, log
	d.Attestor, d.Providers = identity.AttestOff{}, identity.NoProviders{}
	return identityhttp.New(d)
}
