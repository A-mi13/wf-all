//go:build wf_e2e

package main

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/cmd/internal/mediae2e"
	"wf/backend/internal/platform/httpx"
)

// mediaE2E — тестовые ручки media сборки живого e2e (тег wf_e2e, спека media §12a): прямая загрузка и снятие под
// /_e2e/media/ на сессии сотрудника (cookie — admin.CookieName) и пуле admin-api; прямая загрузка — свой предел тела
// 5 МиБ (R16). Прод-образ собирается без тега — media_prod.go.
func mediaE2E(k mediaKit, a assembly, cookie string, pool *pgxpool.Pool, log *slog.Logger) (http.Handler, []httpx.BodyLimit) {
	h := mediae2e.Admin(mediae2e.AdminDeps{
		Deps:     mediae2e.Deps{Service: k.svc, Tx: k.tx, Pool: pool, Log: log},
		Sessions: a.staff.Sessions(), Cookie: cookie, Label: a.staff.Label,
	})
	return h, []httpx.BodyLimit{{Method: http.MethodPost, Prefix: mediae2e.DirectPath, Max: mediae2e.DirectMax}}
}
