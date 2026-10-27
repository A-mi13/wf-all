package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/server"
	"wf/backend/internal/platform/testkit/dbtest"
)

// runService — RunService в горутине на свободном порту; ответ — адрес, отмена и канал итога.
func runService(t *testing.T, build func(*slog.Logger, *pgxpool.Pool) (server.Service, error)) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	addr := freeAddr(t)
	url := dbtest.NewURL(t) // до горутины: t.Fatal нельзя звать из неё
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- server.RunService(ctx, "test", server.HTTPConfig{
			HTTP: config.HTTP{Addr: addr, ShutdownTimeout: 10 * time.Second},
			DB:   config.DB{URL: url, MaxConns: 2},
		}, io.Discard, build)
	}()
	return addr, cancel, done
}

// Порядок остановки (P27): сначала дренаж HTTP — запрос в полёте отвечает, пока фон ещё работает
// (запрос мог поставить письмо); затем отмена фона и ожидание его остановки — пул в это время открыт
// (River пишет состояние задач); пул закрывается последним.
func TestRunServiceStopOrder(t *testing.T) {
	inFlight := make(chan struct{})  // запрос дошёл до хендлера
	release := make(chan struct{})   // отпустить запрос
	bgStopped := make(chan error, 1) // фон увидел отмену: ошибка пинга пула в этот момент
	var mu sync.Mutex
	var order []string
	note := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, s)
	}
	addr, cancel, done := runService(t, func(_ *slog.Logger, pool *pgxpool.Pool) (server.Service, error) {
		return server.Service{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/slow" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				close(inFlight)
				<-release
				note("http-ответ")
				w.WriteHeader(http.StatusNoContent)
			}),
			Background: func(ctx context.Context) error {
				<-ctx.Done()
				note("фон-отмена")
				// пул ещё открыт: фон останавливается до закрытия пула
				bgStopped <- pool.Ping(context.Background())
				return nil
			},
		}, nil
	})
	waitStatus(t, "http://"+addr+"/", http.StatusNoContent)
	resp := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/slow", nil)
		r, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			resp <- 0
			return
		}
		_ = r.Body.Close()
		resp <- r.StatusCode
	}()
	<-inFlight
	cancel() // SIGTERM при запросе в полёте
	select {
	case err := <-bgStopped:
		t.Fatalf("фон остановлен до дренажа HTTP (пинг пула: %v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if code := <-resp; code != http.StatusNoContent {
		t.Fatalf("запрос в полёте: %d", code)
	}
	if err := <-bgStopped; err != nil {
		t.Fatalf("пул закрыт до остановки фона: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunService: %v", err)
	}
	if strings.Join(order, ",") != "http-ответ,фон-отмена" {
		t.Fatalf("порядок: %v", order)
	}
}

// Фон упал (River не стартовал) — сервис не продолжает отвечать без писем и чистки: HTTP
// останавливается, RunService возвращает ошибку фона.
func TestRunServiceBackgroundFailureStopsService(t *testing.T) {
	errBg := errors.New("river не стартовал")
	fail := make(chan struct{})
	addr, _, done := runService(t, func(*slog.Logger, *pgxpool.Pool) (server.Service, error) {
		return server.Service{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
			Background: func(context.Context) error {
				<-fail
				return errBg
			},
		}, nil
	})
	waitStatus(t, "http://"+addr+"/", http.StatusNoContent)
	close(fail)
	select {
	case err := <-done:
		if !errors.Is(err, errBg) {
			t.Fatalf("RunService = %v, ждали ошибку фона", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("сервис продолжил работу после отказа фона")
	}
}

// Фон вернулся без ошибки до остановки сервиса — тоже отказ: тихо остановившийся фон — сервис без
// писем и чистки.
func TestRunServiceBackgroundEarlyNil(t *testing.T) {
	addr, _, done := runService(t, func(*slog.Logger, *pgxpool.Pool) (server.Service, error) {
		return server.Service{
			Handler:    http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
			Background: func(context.Context) error { return nil },
		}, nil
	})
	_ = addr
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "фон") {
			t.Fatalf("RunService = %v, ждали ошибку «фон остановился»", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("сервис продолжил работу после остановки фона")
	}
}

// Сборка не удалась — фон не запускается, ошибка сборки наружу.
func TestRunServiceBuildFailsWithoutBackground(t *testing.T) {
	errBuild := errors.New("реестр подтверждений невалиден")
	started := make(chan struct{}, 1)
	_, _, done := runService(t, func(*slog.Logger, *pgxpool.Pool) (server.Service, error) {
		return server.Service{Background: func(context.Context) error { started <- struct{}{}; return nil }}, errBuild
	})
	select {
	case err := <-done:
		if !errors.Is(err, errBuild) {
			t.Fatalf("RunService = %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("RunService не вернулся")
	}
	select {
	case <-started:
		t.Fatal("фон запущен при отказе сборки")
	default:
	}
}

// Без фона (Background nil) — как прежний RunHTTP.
func TestRunServiceWithoutBackground(t *testing.T) {
	addr, cancel, done := runService(t, func(*slog.Logger, *pgxpool.Pool) (server.Service, error) {
		return server.Service{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}, nil
	})
	waitStatus(t, "http://"+addr+"/", http.StatusNoContent)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunService: %v", err)
	}
}
