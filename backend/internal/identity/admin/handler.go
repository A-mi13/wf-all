// Package admin — ручки модуля identity в админке (тег identity контракта admin, спека identity §7): заявки в
// капитаны, поиск игрока по почте, сброс 2FA поддержкой, статистика способов входа. Handler встраивается в
// internal/httpapi/admin.Server: тег операции = модуль-реализатор (apitest.TagViolations).
//
// План I-1 (контракт до кода): нулевое значение Handler — каркас, каждая операция отвечает 501
// http.not_implemented, пока этап спеки её не реализует; такие операции — в pendingOps стража TestNoStubOperations
// (internal/httpapi/admin).
package admin

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
)

// Deps — зависимости админских сценариев identity у admin-api: пул роли admin, часы, клиент River только для вставки
// (письма identity — mail.SendArgs в очередь mail, исполняет воркер; R21), geo на пуле admin, порты условий
// капитанства (ровно identity.RequiredCaptainChecks — иначе отказ старта, К1), связь «сотрудник → игрок» (R38 плана
// I-4), журнал.
type Deps struct {
	Pool          *pgxpool.Pool
	Clock         clock.Clock
	River         *river.Client[pgx.Tx]
	Geo           geo.Service
	CaptainChecks []identity.CaptainCheck
	StaffLinks    identity.StaffLinks
	Log           *slog.Logger
}

// Handler — админские ручки identity. Нулевое значение — заглушка: каждая операция отвечает 501, пока этап спеки её
// не реализует. Собранный New — сброс 2FA поддержкой (Task 17 плана I-2); операции заявок (captains.go) и поиска
// игрока со статистикой (players.go) — по-прежнему 501 (pendingOps), их реализует план I-4.
type Handler struct {
	svc       identity.Service
	app       *app.App           // nil — заглушка
	approvals *approvals.Service // RegisterApprovals; nil — запрос сброса отвечает 501
}

// New — админские ручки identity на зависимостях admin-api (app.NewAdmin; кольца identity у admin-api нет, N3).
func New(svc identity.Service, d Deps) (*Handler, error) {
	a, err := app.NewAdmin(identity.AdminDeps{Pool: d.Pool, Clock: d.Clock, River: d.River, Geo: d.Geo,
		CaptainChecks: d.CaptainChecks, StaffLinks: d.StaffLinks, Log: d.Log})
	if err != nil {
		return nil, err
	}
	return &Handler{svc: svc, app: a}, nil
}

// errNotImplemented — ответ операции, которую этап спеки ещё не реализовал.
var (
	errNotImplemented  = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)
	errUnauthenticated = httpx.NewError(http.StatusUnauthorized, httpx.CodeUnauthenticated)
	errNoBody          = httpx.NewFieldError("body", "required") // тело обязательно — валидатор отверг бы раньше
)
