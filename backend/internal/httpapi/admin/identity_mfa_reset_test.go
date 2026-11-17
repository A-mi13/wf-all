package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/identity"
	identityadmin "wf/backend/internal/identity/admin"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff"
)

// allowCheck — порт условия капитанства, условие выполнено (тесты httpapi не импортируют identity/identitytest —
// archtest; свой литерал порта, I7б ревью плана I-4).
type allowCheck string

func (c allowCheck) Name() string { return string(c) }

func (allowCheck) Check(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, bool, error) {
	return "", true, nil
}

// staffLinksTx — порт identity.StaffLinks над настоящим staff.LinkedPlayerTx (как адаптер cmd/admin-api).
type staffLinksTx struct{}

func (staffLinksTx) LinkedUser(ctx context.Context, tx pgx.Tx, staffID uuid.UUID) (uuid.UUID, error) {
	return staff.LinkedPlayerTx(ctx, tx, staffID)
}

// identityDeps — зависимости админского хендлера identity в тестах: geo на том же пуле, оба порта условий выполнены,
// связь сотрудника — настоящая.
func identityDeps(pool *pgxpool.Pool, clk clock.Clock, rc *river.Client[pgx.Tx]) identityadmin.Deps {
	checks := make([]identity.CaptainCheck, 0, len(identity.RequiredCaptainChecks))
	for _, n := range identity.RequiredCaptainChecks {
		checks = append(checks, allowCheck(n))
	}
	return identityadmin.Deps{Pool: pool, Clock: clk, River: rc, Geo: geo.New(pool), CaptainChecks: checks,
		StaffLinks: staffLinksTx{}, Log: quiet()}
}

// Сброс 2FA поддержкой через весь конвейер admin-api (спека identity §3.15, §7.1, §7.6; план I-2, Task 17): настоящие
// staff (сессии, LinkedUserID, хук замка акторов), geo и identity в одном реестре подтверждений — как в сборке
// cmd/admin-api; пул сценариев — роль admin.

// newIdentityStaffEnv — staffEnv с собранными geo и identity (действия обоих — в реестре staff до Validate).
func newIdentityStaffEnv(t *testing.T) *staffEnv {
	t.Helper()
	var (
		gh *geoadmin.Handler
		ih *identityadmin.Handler
	)
	e := newStaffEnv(t, func(o *admin.Options) {
		rc, err := river.NewClient(riverpgxv5.New(o.DB), &river.Config{})
		if err != nil {
			t.Fatal(err)
		}
		gh = geoadmin.New(geo.New(o.DB), geoadmin.Deps{Pool: o.DB, Clock: o.Clock, River: rc})
		o.Geo = gh
		ih, err = identityadmin.New(identity.New(o.DB), identityDeps(o.DB, o.Clock, rc))
		if err != nil {
			t.Fatal(err)
		}
		o.Identity = ih
	})
	gh.RegisterApprovals(e.reg, e.approvals)
	ih.RegisterApprovals(e.reg, e.approvals)
	if err := e.reg.Validate(); err != nil {
		t.Fatal(err)
	}
	return e
}

// player — игрок с подтверждённой 2FA (секрет — заглушка: admin его не читает).
func (e *staffEnv) player(nick string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ($1, now(), $2, '2000-01-01', 'ru') RETURNING id`, nick+"@example.com", nick).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, '\x00', 'AAAAAAAAAAA', now(), now())`, id)
	return id
}

const mfaResetBody = `{"reason":"Потерял телефон, личность проверена по документам","ticket":"SUP-4812"}`

func mfaResetPath(uid uuid.UUID) string { return "/v1/identity/users/" + uid.String() + "/mfa-reset" }

// TestMfaResetHTTP — запрос (202 по контракту, повтор ключа — тот же запрос), подтверждение вторым (200 executed,
// сброс назначен), отмена сотрудником (200 {id, cancelled_at}), повтор — 404; связанный с игроком сотрудник — 409
// self_action на запросе и отмене; поддержка — 403 конвейера.
func TestMfaResetHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	sup := e.firstLogin("support@wf.example", string(perm.Support))
	uid := e.player("reset_http")

	wantProblem(t, e.send(http.MethodPost, mfaResetPath(uid), sup.token, uuid.NewString(), mfaResetBody),
		http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.send(http.MethodPost, mfaResetPath(uuid.New()), a.token, uuid.NewString(), mfaResetBody),
		http.StatusNotFound, identity.CodeUserNotFound)

	key := uuid.NewString()
	ap := e.accepted(e.send(http.MethodPost, mfaResetPath(uid), a.token, key, mfaResetBody))
	if again := e.accepted(e.send(http.MethodPost, mfaResetPath(uid), a.token, key, mfaResetBody)); again != ap {
		t.Fatalf("повтор ключа: %s, ждали %s", again, ap)
	}
	wantStatus(t, e.decide("approve", ap, b.token, "", ""), http.StatusOK)
	var rid uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM mfa_resets WHERE user_id = $1 AND approval_id = $2`,
		uid, ap).Scan(&rid); err != nil {
		t.Fatalf("сброс не назначен: %v", err)
	}
	wantProblem(t, e.send(http.MethodPost, mfaResetPath(uid), a.token, uuid.NewString(), mfaResetBody),
		http.StatusConflict, identity.CodeMfaResetPending)

	// связь «b → игрок»: отмена своей учётки — 409, запрос тоже
	e.exec(`INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $3, now())`, b.id, uid, a.id)
	cancel := "/v1/identity/mfa-resets/" + rid.String() + "/cancel"
	wantProblem(t, e.send(http.MethodPost, cancel, b.token, uuid.NewString(), ""), http.StatusConflict, identity.CodeSelfAction)

	rec := e.send(http.MethodPost, cancel, a.token, uuid.NewString(), "")
	wantStatus(t, rec, http.StatusOK)
	var out struct {
		ID          uuid.UUID `json:"id"`
		CancelledAt string    `json:"cancelled_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID != rid || out.CancelledAt == "" {
		t.Fatalf("ответ отмены: %v %s", err, rec.Body.String())
	}
	wantProblem(t, e.send(http.MethodPost, cancel, a.token, uuid.NewString(), ""), http.StatusNotFound, identity.CodeMfaResetNotFound)
	wantProblem(t, e.send(http.MethodPost, mfaResetPath(uid), b.token, uuid.NewString(), mfaResetBody),
		http.StatusConflict, identity.CodeSelfAction)
}

// TestMfaResetApproverLinkedHTTP — подтверждающий связан с игроком: запрос failed (identity.self_action в error), ответ
// 409 platform.approval_stale (бизнес-кодов модулей у approve нет), сброса нет.
func TestMfaResetApproverLinkedHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	uid := e.player("reset_linked_http")
	ap := e.accepted(e.send(http.MethodPost, mfaResetPath(uid), a.token, uuid.NewString(), mfaResetBody))
	e.exec(`INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $3, now())`, b.id, uid, a.id)
	wantProblem(t, e.decide("approve", ap, b.token, "", ""), http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(ap); st != approvals.StatusFailed || errText != identity.CodeSelfAction {
		t.Fatalf("запрос: %s %q", st, errText)
	}
	if e.count(`SELECT count(*) FROM mfa_resets`) != 0 {
		t.Fatal("сброс назначен")
	}
}

// TestMfaResetInitiatorDisabledConcurrently — по образцу TestCountryRulesInitiatorDisabledConcurrently: approve встал
// на замке учётки подтверждающего (хук замка акторов staff), за это время инициатора выключили — запрос failed
// (initiator_changed), 409 approval_stale, сброса нет.
func TestMfaResetInitiatorDisabledConcurrently(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	o := e.owners(2)
	b, adm := o[0], o[1]
	if strings.Compare(b.id.String(), adm.id.String()) > 0 {
		b, adm = adm, b
	}
	uid := e.player("reset_race_http")
	ap := e.accepted(e.send(http.MethodPost, mfaResetPath(uid), adm.token, uuid.NewString(), mfaResetBody))

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
	if e.count(`SELECT count(*) FROM mfa_resets`) != 0 {
		t.Fatal("сброс назначен при выключенном инициаторе")
	}
}

// TestMfaResetNulHTTP — NUL (\u0000) в причине или обращении: 400 validation.failed по полю, а не 500 (text и jsonb
// его не хранят); запроса нет.
func TestMfaResetNulHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	a := e.owners(1)[0]
	uid := e.player("reset_nul_http")
	for field, body := range map[string]string{
		"body.reason": `{"reason":"Потерял\u0000телефон"}`,
		"body.ticket": `{"reason":"Потерял телефон","ticket":"SUP\u00004812"}`,
	} {
		rec := e.send(http.MethodPost, mfaResetPath(uid), a.token, uuid.NewString(), body)
		wantProblem(t, rec, http.StatusBadRequest, httpx.CodeValidationFailed)
		if !strings.Contains(rec.Body.String(), field) {
			t.Errorf("нет поля %s: %s", field, rec.Body.String())
		}
	}
	if e.count(`SELECT count(*) FROM approvals`) != 0 {
		t.Fatal("запрос создан")
	}
}
