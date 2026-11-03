package admin_test

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Страны (Task 13) за полным конвейером на настоящих staff и подтверждениях: действия geo.country_create и
// geo.country_rules зарегистрированы как в сборке admin-api (до Validate), хук замка акторов — staff.

// newGeoStaffEnv — newStaffEnv с собранным хендлером geo (с клиентом River для импорта, Task 14) и его действиями в
// реестре подтверждений.
func newGeoStaffEnv(t *testing.T) *staffEnv {
	t.Helper()
	var gh *geoadmin.Handler
	e := newStaffEnv(t, func(o *admin.Options) {
		rc, err := river.NewClient(riverpgxv5.New(o.DB), &river.Config{})
		if err != nil {
			t.Fatal(err)
		}
		gh = geoadmin.New(geo.New(o.DB), geoadmin.Deps{Pool: o.DB, Clock: o.Clock, River: rc})
		o.Geo = gh
	})
	gh.RegisterApprovals(e.reg, e.approvals)
	if err := e.reg.Validate(); err != nil {
		t.Fatal(err)
	}
	return e
}

const kzBody = `{"code":"KZ","currency":"KZT","default_locale":"ru","phone_prefix":"+7","week_starts_on":1,` +
	`"min_signup_age":14,"age_of_majority":18,"is_enabled":true,"names":{"ru":"Казахстан","en":"Kazakhstan"}}`

func TestGeoCountriesHTTP(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	sup := e.firstLogin("support@wf.example", string(perm.Support))

	// создание — всегда 202 (Approval по контракту), повтор ключа — тот же запрос
	key := uuid.NewString()
	ap := e.accepted(e.send(http.MethodPost, "/v1/geo/countries", adm.token, key, kzBody))
	if again := e.accepted(e.send(http.MethodPost, "/v1/geo/countries", adm.token, key, kzBody)); again != ap {
		t.Fatalf("повтор: %s, ждали %s", again, ap)
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 1 {
		t.Fatalf("запросов %d", n)
	}
	wantProblem(t, e.send(http.MethodPost, "/v1/geo/countries", adm.token, uuid.NewString(), strings.Replace(kzBody, `"KZ"`, `"RU"`, 1)),
		http.StatusConflict, geo.CodeCountryExists)
	wantProblem(t, e.send(http.MethodPost, "/v1/geo/countries", sup.token, uuid.NewString(), kzBody), http.StatusForbidden, "auth.forbidden")
	// второй ждущий запрос на тот же код — допустим (исполнится вторым и не пройдёт)
	ap2 := e.accepted(e.send(http.MethodPost, "/v1/geo/countries", adm.token, uuid.NewString(), kzBody))

	// TestApprovalsIncomingGeo: во «Входящих» второго владельца и админа — есть, у поддержки — нет
	for _, c := range []struct {
		m    member
		want bool
	}{{b, true}, {a, true}, {sup, false}} {
		rec := e.get("/v1/approvals?box=incoming", c.m.token)
		if got := rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), ap.String()); got != c.want {
			t.Errorf("входящие %s: %v (%d %s)", c.m.email, got, rec.Code, rec.Body.String())
		}
	}

	// подтверждение — страна создана; инициатор — 409 approval_self
	wantProblem(t, e.decide("approve", ap, adm.token, "", ""), http.StatusConflict, httpx.CodeApprovalSelf)
	wantStatus(t, e.decide("approve", ap, b.token, "", ""), http.StatusOK)
	if n := e.count(`SELECT count(*) FROM countries WHERE code = 'KZ' AND is_enabled`); n != 1 {
		t.Fatal("страна не создана")
	}

	// TestCountryCreateTwoRequests на настоящем действии: второй запрос на тот же код (ждал раньше создания) —
	// failed с кодом причины geo.country_exists, наружу 409 platform.approval_stale (⚠️ G4: approveProblem)
	wantProblem(t, e.decide("approve", ap2, b.token, "", ""), http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(ap2); st != approvals.StatusFailed || errText != geo.CodeCountryExists {
		t.Fatalf("второй запрос: %s %q", st, errText)
	}

	// правка: поле «сразу» — 200 по контракту с Vary; правило — 202; лишнее поле — 400
	rec := e.send(http.MethodPatch, "/v1/geo/countries/KZ", adm.token, uuid.NewString(), `{"phone_prefix":"+77"}`, "Accept-Language", "en")
	if rec.Code != http.StatusOK || rec.Header().Get("Vary") != "Accept-Language" || rec.Header().Get("Content-Language") != "en" ||
		!strings.Contains(rec.Body.String(), `"phone_prefix":"+77"`) {
		t.Fatalf("сразу: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	rules := e.accepted(e.send(http.MethodPatch, "/v1/geo/countries/KZ", adm.token, uuid.NewString(), `{"currency":"USD","phone_prefix":"+7"}`))
	wantProblem(t, e.send(http.MethodPatch, "/v1/geo/countries/KZ", adm.token, uuid.NewString(), `{"default_locale":"en"}`),
		http.StatusBadRequest, httpx.CodeValidationFailed)
	wantProblem(t, e.send(http.MethodPatch, "/v1/geo/countries/ZZ", adm.token, uuid.NewString(), `{"phone_prefix":"+1"}`),
		http.StatusNotFound, geo.CodeCountryNotFound)
	// TestCountryRulesStale на настоящем действии: страну правили напрямую после запроса — 409 stale
	wantStatus(t, e.send(http.MethodPatch, "/v1/geo/countries/KZ", adm.token, uuid.NewString(), `{"week_starts_on":7}`), http.StatusOK)
	wantProblem(t, e.decide("approve", rules, b.token, "", ""), http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(rules); st != approvals.StatusFailed || errText != httpx.CodeApprovalStale {
		t.Fatalf("устаревший: %s %q", st, errText)
	}
	if n := e.count(`SELECT count(*) FROM countries WHERE code = 'KZ' AND currency = 'KZT' AND phone_prefix = '+77'`); n != 1 {
		t.Fatal("устаревший запрос применён")
	}
}

// Версия учётки инициатора выросла после запроса (выключен и снова включён) — failed (initiator_changed), 409,
// правила не применены.
func TestCountryRulesInitiatorChanged(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	b := e.owners(1)[0]
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	ap := e.accepted(e.send(http.MethodPatch, "/v1/geo/countries/RU", adm.token, uuid.NewString(), `{"currency":"USD"}`))
	e.exec(`UPDATE staff_accounts SET status = 'disabled', version = version + 1 WHERE id = $1`, adm.id)
	e.exec(`UPDATE staff_accounts SET status = 'active', version = version + 1 WHERE id = $1`, adm.id)
	wantProblem(t, e.decide("approve", ap, b.token, "", ""), http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(ap); st != approvals.StatusFailed || errText != approvals.ErrorInitiatorChanged {
		t.Fatalf("запрос: %s %q", st, errText)
	}
	if n := e.count(`SELECT count(*) FROM countries WHERE code = 'RU' AND currency = 'RUB'`); n != 1 {
		t.Fatal("правило применено")
	}
}

// Review Focus 7, R21 на настоящем geo.country_rules: approve прошёл проверку инициатора (без замка) и ждёт в хуке
// замка акторов на учётке подтверждающего (её держит третья транзакция; id подтверждающего меньше — хук берёт её
// первым); инициатора выключают и коммитят; хук под FOR SHARE видит изменившегося инициатора — 409 stale,
// initiator_changed, правило не применено. Без LockActors исполнитель применил бы правило.
func TestCountryRulesInitiatorDisabledConcurrently(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	// два владельца: подтверждающий — с меньшим id (хук берёт учётки по возрастанию id — его первой), инициатор — с
	// большим (порядок id учёток в одну миллисекунду не гарантирован — выбираем, а не полагаемся)
	o := e.owners(2)
	b, adm := o[0], o[1]
	if strings.Compare(b.id.String(), adm.id.String()) > 0 {
		b, adm = adm, b
	}
	ap := e.accepted(e.send(http.MethodPatch, "/v1/geo/countries/RU", adm.token, uuid.NewString(), `{"currency":"USD"}`))

	ctx := context.Background()
	hold, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, b.id); err != nil {
		t.Fatal(err)
	}
	out := make(chan *httpRec, 1)
	go func() { out <- e.decide("approve", ap, b.token, "", "") }()
	waitWaiters(t, e)
	e.exec(`UPDATE staff_accounts SET status = 'disabled', version = version + 1 WHERE id = $1`, adm.id)
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, <-out, http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(ap); st != approvals.StatusFailed || errText != approvals.ErrorInitiatorChanged {
		t.Fatalf("запрос: %s %q", st, errText)
	}
	if n := e.count(`SELECT count(*) FROM countries WHERE code = 'RU' AND currency = 'RUB'`); n != 1 {
		t.Fatal("правило применено при выключенном инициаторе")
	}
}

// waitWaiters — в базе теста есть незахваченный замок (approve встал в хуке); срок — 60 с, без sleep-ожиданий
// результата: опрос pg_locks своей базы.
func waitWaiters(t *testing.T, e *staffEnv) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if e.count(`SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			WHERE NOT l.granted AND a.datname = current_database()`) > 0 {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("approve не встал на замке учётки за 60 с")
}
