//go:build wf_e2e

package main

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/cmd/internal/mediae2e"
)

// mediaE2E — тестовые ручки media сборки живого e2e (тег wf_e2e, спека media §12a): /_e2e/media/* на сервисе и intx
// процесса. Прод-образ (backend/Dockerfile) собирается без тега — media_prod.go.
func mediaE2E(k mediaKit, pool *pgxpool.Pool, log *slog.Logger) http.Handler {
	return mediae2e.API(mediae2e.Deps{Service: k.svc, Tx: k.tx, Pool: pool, Log: log})
}
