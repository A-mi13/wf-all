package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/server"
	"wf/backend/internal/platform/testkit/dbtest"
)

// freeAddr — свободный локальный адрес для теста.
func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestRunHTTPServesAndStops(t *testing.T) {
	addr := freeAddr(t)
	url := dbtest.NewURL(t) // до горутины: t.Fatal нельзя звать из неё
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.RunHTTP(ctx, "test", server.HTTPConfig{
			HTTP: config.HTTP{Addr: addr, ShutdownTimeout: time.Second},
			DB:   config.DB{URL: url, MaxConns: 2},
		}, io.Discard, func(_ *slog.Logger, _ *pgxpool.Pool) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), nil
		})
	}()
	waitStatus(t, "http://"+addr+"/", http.StatusNoContent)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunHTTP: %v", err)
	}
}

// Хендлер не собрался (битый контракт) — RunHTTP отказывает до открытия порта, а не
// поднимает сервер без хендлера.
func TestRunHTTPFailsWhenBuildFails(t *testing.T) {
	errBuild := errors.New("контракт не собрался")
	// дедлайн — только страховка: с багом RunHTTP поднял бы сервер и не вернулся; запас —
	// на подключение к базе под нагрузкой параллельных тестов
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := server.RunHTTP(ctx, "test", server.HTTPConfig{
		HTTP: config.HTTP{Addr: freeAddr(t), ShutdownTimeout: time.Second},
		DB:   config.DB{URL: dbtest.NewURL(t), MaxConns: 2},
	}, io.Discard, func(_ *slog.Logger, _ *pgxpool.Pool) (http.Handler, error) {
		return nil, errBuild
	})
	if !errors.Is(err, errBuild) {
		t.Fatalf("RunHTTP = %v, want %v", err, errBuild)
	}
}

func waitStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s не ответил %d за 10 с", url, want)
}
