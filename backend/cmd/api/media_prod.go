//go:build !wf_e2e

package main

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mediaE2E — тестовые ручки media прод-сборки (без тега wf_e2e): их нет, маршрута /_e2e/ нет
// (TestProdBuildHasNoE2ERoutes). Сборка живого e2e — media_e2e.go.
func mediaE2E(mediaKit, *pgxpool.Pool, *slog.Logger) http.Handler { return nil }
