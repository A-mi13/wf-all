package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
)

// Импорт из админки (Task 14) за полным конвейером: настоящие staff, подтверждения и geo с клиентом River (вставка
// в транзакции сценария под Idempotency-Key). Ответы сверяются с контрактом (e.v).

func jobIDOf(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	var b struct {
		JobID *int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.JobID == nil {
		t.Fatalf("job_id: %v %s", err, rec.Body.String())
	}
	return *b.JobID
}

func importJobOf(t *testing.T, rec *httptest.ResponseRecorder) oapi.GeoImportJob {
	t.Helper()
	wantStatus(t, rec, http.StatusOK)
	var j oapi.GeoImportJob
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func cancelPath(job int64) string { return fmt.Sprintf("/v1/geo/imports/jobs/%d/cancel", job) }

// I-2, R26: две постановки одной страны — вторая 409 geo.import_in_progress с job_id первой; отмена этой задачи —
// 200 cancelled (повтор отмены другим ключом — тоже 200, аудит один, K6); третья постановка — 202. Повтор того же
// Idempotency-Key — тот же 202 и та же задача (ответ из ключа), а не 409. Карточка — queued; чужой id — 404.
func TestStartImportConflictCarriesJobID(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	key := uuid.NewString()
	rec := e.send(http.MethodPost, "/v1/geo/imports", adm.token, key, `{"country":"RU"}`)
	wantStatus(t, rec, http.StatusAccepted)
	first := jobIDOf(t, rec)
	again := e.send(http.MethodPost, "/v1/geo/imports", adm.token, key, `{"country":"RU"}`)
	if wantStatus(t, again, http.StatusAccepted); jobIDOf(t, again) != first {
		t.Fatalf("повтор ключа: %s", again.Body.String())
	}
	busy := e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"RU"}`)
	wantProblem(t, busy, http.StatusConflict, geo.CodeImportInProgress)
	if got := jobIDOf(t, busy); got != first {
		t.Fatalf("job_id %d, ждали %d", got, first)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'geo.import'`); n != 1 {
		t.Fatalf("задач %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested'`); n != 1 {
		t.Fatalf("аудитов постановки %d", n)
	}
	for range 2 {
		j := importJobOf(t, e.send(http.MethodPost, cancelPath(first), adm.token, uuid.NewString(), ""))
		if j.JobId != first || j.State != oapi.GeoImportJobStateCancelled {
			t.Fatalf("отмена: %+v", j)
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'geo.import_cancelled'`); n != 1 {
		t.Fatalf("аудитов отмены %d", n)
	}
	rec = e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"RU"}`)
	wantStatus(t, rec, http.StatusAccepted)
	third := jobIDOf(t, rec)
	if third == first {
		t.Fatal("третья постановка вернула отменённую задачу")
	}
	if j := importJobOf(t, e.get(fmt.Sprintf("/v1/geo/imports/jobs/%d", third), adm.token)); j.State != oapi.GeoImportJobStateQueued ||
		j.ImportId != nil || j.MaxAttempts != 3 {
		t.Fatalf("карточка: %+v", j)
	}
	wantProblem(t, e.get("/v1/geo/imports/jobs/987654", adm.token), http.StatusNotFound, geo.CodeImportJobNotFound)
	wantProblem(t, e.send(http.MethodPost, cancelPath(987654), adm.token, uuid.NewString(), ""), http.StatusNotFound, geo.CodeImportJobNotFound)
}

// Импорт оператора (строка running без задачи) — 409 без job_id; страны нет — 404; «ru» — 400 схемой (запрос вне
// контракта — мимо e.v); отмена выполняемой задачи — 409 geo.import_job_not_cancellable.
func TestStartImportHTTPRejects(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	low := e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"ru"}`)
	wantProblem(t, low, http.StatusBadRequest, httpx.CodeValidationFailed)
	e.v = apitest.New(t, oapi.GetSpec)
	wantProblem(t, e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"ZZ"}`),
		http.StatusNotFound, geo.CodeCountryNotFound)
	e.exec(`INSERT INTO geonames_imports (id, country_code, status, started_at) VALUES ($1, 'RU', 'running', now())`, uuid.New())
	op := e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"RU"}`)
	wantProblem(t, op, http.StatusConflict, geo.CodeImportInProgress)
	var b map[string]any
	if err := json.Unmarshal(op.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if _, ok := b["job_id"]; ok {
		t.Fatalf("импорт оператора: job_id в ответе %s", op.Body.String())
	}
	e.exec(`INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on, min_signup_age,
	                                age_of_majority, is_enabled) VALUES ('XB', 'Testland', 'XXX', 'en', '+998', 1, 14, 18, false)`)
	rec := e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"XB"}`)
	wantStatus(t, rec, http.StatusAccepted)
	job := jobIDOf(t, rec)
	e.exec(`UPDATE river_job SET state = 'running', attempt = 1, attempted_at = now() WHERE id = $1`, job)
	wantProblem(t, e.send(http.MethodPost, cancelPath(job), adm.token, uuid.NewString(), ""),
		http.StatusConflict, geo.CodeImportJobNotCancellable)
}

// Права (Приложение 1): поддержка читает задачу, ставить и отменять — 403 (модератор — 403 на всё: строки
// TestPermissionsAllOperations и сценарий, TestStartImportRejects).
func TestImportPermissions(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	sup := e.firstLogin("support@wf.example", string(perm.Support))
	rec := e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"RU"}`)
	wantStatus(t, rec, http.StatusAccepted)
	job := jobIDOf(t, rec)
	jobPath := fmt.Sprintf("/v1/geo/imports/jobs/%d", job)
	wantStatus(t, e.get(jobPath, sup.token), http.StatusOK)
	wantProblem(t, e.send(http.MethodPost, "/v1/geo/imports", sup.token, uuid.NewString(), `{"country":"RU"}`), http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.send(http.MethodPost, cancelPath(job), sup.token, uuid.NewString(), ""), http.StatusForbidden, httpx.CodeForbidden)
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'geo.import' AND state = 'available'`); n != 1 {
		t.Fatalf("задач %d", n)
	}
}

// R20, ревью G5: операции geo пишут в аудит IP и User-Agent запроса — requestMeta кладёт approvals.Meta в ctx всех
// операций, geo читает его (раньше UA в аудите geo был пуст). UA длиннее 512 байт — обрезается, как у платформы.
func TestGeoOperationsCarryMeta(t *testing.T) {
	t.Parallel()
	e := newGeoStaffEnv(t)
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	var stav uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM cities WHERE slug = 'stavropol'`).Scan(&stav); err != nil {
		t.Fatal(err)
	}
	ua := "Mozilla/5.0 (meta-test) " + strings.Repeat("x", 600)
	rec := e.send(http.MethodPost, "/v1/geo/cities/"+stav.String()+"/districts", adm.token, uuid.NewString(), `{"name":"Промышленный"}`,
		"User-Agent", ua)
	wantStatus(t, rec, http.StatusOK)
	var got string
	if err := e.owner.QueryRow(context.Background(), `SELECT user_agent FROM audit_log WHERE action = 'geo.district_created'
		AND ip IS NOT NULL`).Scan(&got); err != nil {
		t.Fatalf("аудит района: %v", err)
	}
	if len(got) != 512 || !strings.HasPrefix(got, "Mozilla/5.0 (meta-test) ") {
		t.Fatalf("UA в аудите: %d байт %.40q", len(got), got)
	}
	rec = e.send(http.MethodPost, "/v1/geo/imports", adm.token, uuid.NewString(), `{"country":"RU"}`, "User-Agent", "WF-Admin/1.0")
	wantStatus(t, rec, http.StatusAccepted)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'geo.import_requested' AND user_agent = 'WF-Admin/1.0' AND ip IS NOT NULL`); n != 1 {
		t.Fatalf("аудит постановки: %d", n)
	}
}
