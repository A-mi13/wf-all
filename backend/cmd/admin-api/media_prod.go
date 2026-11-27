//go:build !wf_e2e

package main

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/httpx"
)

// mediaE2E — тестовые ручки media прод-сборки (без тега wf_e2e): их нет, маршрута /_e2e/ и исключений лимита тела нет.
// Сборка живого e2e — media_e2e.go.
func mediaE2E(mediaKit, assembly, string, *pgxpool.Pool, *slog.Logger) (http.Handler, []httpx.BodyLimit) {
	return nil, nil
}
