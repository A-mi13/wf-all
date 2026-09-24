package server_test

import (
	"context"
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
		}, io.Discard, func(_ *slog.Logger, _ *pgxpool.Pool) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		})
	}()
	waitStatus(t, "http://"+addr+"/", 204)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunHTTP: %v", err)
	}
}

func waitStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil { //nolint:noctx // тестовый опрос
			resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s не ответил %d за 10 с", url, want)
}
