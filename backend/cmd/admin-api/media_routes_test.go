package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/peer"
)

// adminRoutes — маршруты собранного хендлера admin-api (newAdminHandler — как в run) с хранилищем fs: сколько под
// /_e2e и сколько под /v1/ (обход не пуст).
func adminRoutes(t *testing.T) (e2e, v1 int) {
	t.Helper()
	e := newTestEnv(t)
	a := e.assemble()
	env := e.environ("ADMIN_BLOB_BACKEND=fs", "ADMIN_BLOB_FS_ROOT="+t.TempDir(),
		"ADMIN_BLOB_FS_BASE_URL=http://127.0.0.1:8080", "ADMIN_BLOB_DEV_SIGN_KEY="+e.key)
	cfg, err := config.Load[config.Admin]("ADMIN_", env)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := rateRules(cfg.RateLimits)
	if err != nil {
		t.Fatal(err)
	}
	b, reg, err := openBlob(context.Background(), env, clock.System)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	mk, err := newMedia(log, a.deps.Pool, b, reg, a.deps.Clock, a.deps.Limiter, a.deps.River)
	if err != nil {
		t.Fatal(err)
	}
	h, err := newAdminHandler(log, a.deps.Pool, cfg, peer.Config{}, rules, a, mk)
	if err != nil {
		t.Fatal(err)
	}
	routes, ok := h.(chi.Routes)
	if !ok {
		t.Fatalf("обработчик admin-api %T — не chi.Routes", h)
	}
	err = chi.Walk(routes, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch {
		case strings.HasPrefix(route, "/_e2e"):
			e2e++
		case strings.HasPrefix(route, "/v1/"):
			v1++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return e2e, v1
}
