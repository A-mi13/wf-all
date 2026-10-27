// Package server — общий жизненный цикл HTTP-бинарников: api и admin-api отличаются
// префиксом конфига, хендлером и фоном (admin-api — River очереди staff и чистка, P27).
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/clock"
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

// Service — то, что собирает бинарник на открытом пуле: HTTP-хендлер и фон рядом с ним.
type Service struct {
	Handler http.Handler
	// Background — фон процесса (nil — фона нет): работает до отмены ctx, останавливается сам (мягко,
	// без потери задач) и возвращает nil. Ошибка или возврат до отмены ctx — отказ сервиса: HTTP
	// останавливается, RunService возвращает ошибку.
	Background func(ctx context.Context) error
}

// RunHTTP — HTTP-сервис без фона (cmd/api); обёртка над RunService.
func RunHTTP(ctx context.Context, name string, c HTTPConfig, logOut io.Writer,
	build func(log *slog.Logger, pool *pgxpool.Pool) (http.Handler, error)) error {
	return RunService(ctx, name, c, logOut, func(log *slog.Logger, pool *pgxpool.Pool) (Service, error) {
		h, err := build(log, pool)
		return Service{Handler: h}, err
	})
}

// errBackgroundStopped — фон вернулся без ошибки до остановки сервиса.
var errBackgroundStopped = errors.New("фон остановился раньше сервиса")

// RunService — жизненный цикл HTTP-бинарника с фоном. Порядок: пул → сборка (отказ — до открытия порта и
// без фона) → порт → фон → HTTP до отмены ctx. Остановка: сначала дренаж HTTP (запрос в полёте может
// поставить задачу фону), затем отмена фона и ожидание его остановки, последним — закрытие пула (фон пишет
// в базу состояние задач до конца). Отказ фона гасит HTTP: сервис без писем и чистки не остаётся
// полуживым.
func RunService(ctx context.Context, name string, c HTTPConfig, logOut io.Writer,
	build func(log *slog.Logger, pool *pgxpool.Pool) (Service, error)) error {
	log := logx.New(logOut, c.Log).With("service", name)
	// сторонние библиотеки и стандартный log — через тот же логгер с маскированием ПД
	slog.SetDefault(log)
	pool, err := db.Open(ctx, c.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	// хендлер собирается до открытия порта: битый контракт — отказ старта, а не полуживой сервер
	svc, err := build(log, pool)
	if err != nil {
		return err
	}
	// пробы — до хендлера бинарника: без логов доступа и проверки по контракту; /readyz
	// открыт — пинг базы не чаще раза в секунду
	h := health.Handler(health.Cached(pool.Ping, time.Second, clock.System.Now), svc.Handler)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", c.HTTP.Addr)
	if err != nil {
		return err
	}
	// HTTP живёт до отмены ctx или отказа фона
	httpCtx, stopHTTP := context.WithCancel(ctx)
	defer stopHTTP()
	bgDone := make(chan error, 1)
	stopBg := func() {}
	if svc.Background != nil {
		// фон не видит отмену ctx сервиса: его останавливают после дренажа HTTP
		bgCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		stopBg = cancel
		go func() {
			err := svc.Background(bgCtx)
			if bgCtx.Err() == nil {
				// фон завершился сам — отказ: гасим HTTP
				if err == nil {
					err = errBackgroundStopped
				}
				log.Error("фон остановился, сервис останавливается", "err", err)
				stopHTTP()
			}
			bgDone <- err
		}()
	} else {
		bgDone <- nil
	}
	defer stopBg()
	log.Info("слушаю", "addr", ln.Addr().String())
	httpErr := httpx.Serve(httpCtx, ln, h, c.HTTP.ShutdownTimeout)
	stopBg()
	bgErr := <-bgDone
	if bgErr != nil {
		bgErr = fmt.Errorf("%s: фон: %w", name, bgErr)
	}
	return errors.Join(bgErr, httpErr)
}
