package main

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/config"
	"wf/backend/internal/platform/i18n"
	"wf/backend/locales"
)

// Сборка geo 2/2 в admin-api (план B, Task 16; ревью G6): действия подтверждения geo регистрируются в том же реестре,
// что staff, до Validate; geo получает клиент вставки staff; UA и IP операций geo — в аудите.

func (e *testEnv) assemble() assembly {
	e.t.Helper()
	cfg, err := config.Load[config.Admin]("ADMIN_", e.environ())
	if err != nil {
		e.t.Fatal(err)
	}
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		e.t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), e.dbURL)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(pool.Close)
	a, err := assemble(slog.New(slog.DiscardHandler), pool, cfg, e.ring, cat)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// Собранный реестр знает действия geo (geo.country_rules, geo.country_create) рядом с действиями staff; geo собран
// с клиентом вставки staff (одна переменная, урок 10b).
func TestAssembleRegistersGeoActions(t *testing.T) {
	t.Parallel()
	a := newTestEnv(t).assemble()
	acts := a.reg.Actions()
	for _, want := range []string{"geo.country_create", "geo.country_rules", "staff.invite"} {
		if !slices.Contains(acts, want) {
			t.Errorf("в реестре нет %s: %v", want, acts)
		}
	}
	if a.geo == nil || a.geoDeps.River == nil || any(a.geoDeps.River) != any(a.deps.River) || a.geoDeps.Pool != a.deps.Pool {
		t.Fatal("geo собран без клиента вставки staff")
	}
}

// Действия geo держат инициатора под замком (LockActors): без хука SetActorLock от staff реестр не проходит Validate —
// сборка без staff отказывает при старте, а не исполняет правила страны без перепроверки учёток.
func TestAssembleActorLockRequired(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	pool, err := pgxpool.New(context.Background(), e.dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reg := approvals.NewRegistry()
	svc := approvals.New(pool, clock.System, reg)
	geoadmin.New(geo.New(pool), geoadmin.Deps{Pool: pool, Clock: clock.System}).RegisterApprovals(reg, svc)
	reg.SetInitiatorCheck(func(context.Context, pgx.Tx, approvals.Approval) error { return nil })
	if err := reg.Validate(); err == nil {
		t.Fatal("реестр с действиями geo без замка акторов прошёл Validate")
	}
}

// Собранный сервер: /v1/geo/places без cookie — 401 (маршрут geo жив); создание страны вдвоём исполняется через
// approve (200 executed — действия geo в том же сервисе); импорт ставится (202 — клиент River у geo есть); аудит
// постановки несёт UA и IP запроса (requestMeta для операций geo).
func TestStartupWiring(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	_, a := e.staffSession("anna@wf.example", "owner")
	_, b := e.staffSession("boris@wf.example", "owner")
	cancel, done := e.startServer()
	if code, body := e.request(http.MethodGet, "/v1/geo/places?q=moskva", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/geo/places без cookie: %d %s", code, body)
	}
	code, body := e.request(http.MethodPost, "/v1/geo/countries", a, `{"code":"KZ","currency":"KZT","default_locale":"ru",`+
		`"phone_prefix":"+7","week_starts_on":1,"min_signup_age":14,"age_of_majority":18,"names":{"ru":"Казахстан"}}`)
	if code != http.StatusAccepted {
		t.Fatalf("создание страны: %d %s", code, body)
	}
	var apID uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM approvals WHERE action = 'geo.country_create'`).Scan(&apID); err != nil {
		t.Fatal(err)
	}
	if code, body := e.request(http.MethodPost, "/v1/approvals/"+apID.String()+"/approve", b, `{}`); code != http.StatusOK ||
		!strings.Contains(body, `"executed"`) {
		t.Fatalf("approve: %d %s", code, body)
	}
	if e.count(`SELECT count(*) FROM countries WHERE code = 'KZ'`) != 1 {
		t.Fatal("страна не создана")
	}
	if code, body := e.request(http.MethodPost, "/v1/geo/imports", a, `{"country":"RU"}`); code != http.StatusAccepted {
		t.Fatalf("импорт: %d %s", code, body)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested' AND user_agent LIKE 'Go-http-client/%'
		AND ip IS NOT NULL`); n != 1 {
		t.Fatalf("аудит постановки без UA и IP: %d", n)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}
