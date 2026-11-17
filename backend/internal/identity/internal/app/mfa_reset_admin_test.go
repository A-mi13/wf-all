package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/queue"
)

// Сброс 2FA поддержкой — сторона admin-api (спека §3.15 п. 2–4, §7.1, §7.6; план I-2, Task 17). Сценарии идут под
// ролью admin на той же базе, что env (роль прода: у admin нет UPDATE на users, sessions, user_mfa — любой FOR … на них
// или запись дали бы 42501). Подтверждения — настоящий approvals.Service; проверка инициатора и замок акторов — заглушки
// (учёток staff в базе нет; их перепроверку закрывают тесты httpapi/admin на настоящем staff).

// resetEnv — env игрока плюс сценарии admin-api и подтверждения вдвоём.
type resetEnv struct {
	*env
	adm *App
	reg *approvals.Registry
	svc *approvals.Service
	// links — текущие связи сотрудников с игроками (порт StaffLinks; учёток staff в базе теста нет)
	links *fakeLinks
}

// fakeLinks — порт identity.StaffLinks теста: связь и версия учётки по id сотрудника, задаются тестом в любой момент;
// версия по умолчанию — 1, как у staffMember.
type fakeLinks struct{ m, ver sync.Map }

func (f *fakeLinks) set(staffID, userID uuid.UUID) { f.m.Store(staffID, userID) }

// setVersion — версия учётки сотрудника «в базе» (изменили после загрузки сессии — больше, чем auth.Staff.Version).
func (f *fakeLinks) setVersion(staffID uuid.UUID, v int64) { f.ver.Store(staffID, v) }

func (f *fakeLinks) LinkedUser(_ context.Context, _ pgx.Tx, staffID uuid.UUID) (uuid.UUID, int64, error) {
	v := int64(1)
	if got, ok := f.ver.Load(staffID); ok {
		v = got.(int64)
	}
	if u, ok := f.m.Load(staffID); ok {
		return u.(uuid.UUID), v, nil
	}
	return uuid.Nil, v, nil
}

// roleApp — NewMaintenance на той же базе под ролью role (admin | worker), без кольца секретов (как admin-api, N3)
// или с кольцом env (воркер).
func roleApp(t *testing.T, e *env, role string, withSecrets bool) *App {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(e.owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 6
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+role)
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	var secrets = e.a.secrets
	if !withSecrets {
		secrets = nil
	}
	a, err := NewMaintenance(pool, e.clock, secrets, ins, log)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newResetEnv(t *testing.T) *resetEnv {
	t.Helper()
	e := newEnv(t)
	adm := roleApp(t, e, "admin", false)
	links := &fakeLinks{}
	adm.staffLinks = links // admin-api: связь подтверждающего перечитывается в tx решения (R38)
	reg := approvals.NewRegistry()
	svc := approvals.New(adm.pool, e.clock, reg)
	reg.Register(adm.MfaResetAction())
	reg.SetInitiatorCheck(func(context.Context, pgx.Tx, approvals.Approval) error { return nil })
	reg.SetActorLock(func(context.Context, pgx.Tx, approvals.Approval, *auth.Staff) error { return nil })
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}
	return &resetEnv{env: e, adm: adm, reg: reg, svc: svc, links: links}
}

// staffMember — активный сотрудник роли role со связью linked (uuid.Nil — без связи).
func staffMember(role perm.Role, label string, linked uuid.UUID) *auth.Staff {
	return &auth.Staff{StaffID: id.New(), SessionID: id.New(), Role: string(role), Stage: auth.StageActive,
		Grants: perm.Grants(role), Version: 1, Label: label, LinkedUserID: linked}
}

// withMfa — подтверждённая 2FA игрока (секрет — заглушка: admin его не читает).
func (r *resetEnv) withMfa(t *testing.T, uid uuid.UUID, confirmed bool) {
	t.Helper()
	var at *time.Time
	if confirmed {
		at = &testNow
	}
	acctExec(t, r.env, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, '\x00', 'AAAAAAAAAAA', $2, now())`, uid, at)
}

// liveReset — живой сброс фикстурой (как после подтверждения).
func (r *resetEnv) liveReset(t *testing.T, uid uuid.UUID) uuid.UUID {
	t.Helper()
	rid := id.New()
	acctExec(t, r.env, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		execute_at, created_at) VALUES ($1, $2, gen_random_uuid(), gen_random_uuid(), 'Ольга', 'потерян телефон', $3, $4)`,
		rid, uid, testNow.Add(domain.MfaResetDelay), testNow)
	return rid
}

// request — запрос сброса сотрудником st.
func (r *resetEnv) request(st *auth.Staff, uid uuid.UUID, reason string, ticket *string) (approvals.Approval, error) {
	return r.adm.RequestMfaReset(context.Background(), r.svc, st, approvals.Meta{},
		MfaResetInput{UserID: uid, Reason: reason, Ticket: ticket})
}

// problemStatus — код ProblemError ("" — не ProblemError).
func problemStatus(err error) (int, string) {
	if pe, ok := errors.AsType[httpx.ProblemError](err); ok {
		p := pe.Problem()
		return p.Status, p.Code
	}
	return 0, ""
}

const resetReason = "Потерял телефон, личность проверена по документам"

func TestRequestMfaResetCodes(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	noMfa := seedUser(t, r.owner, "reset_nomfa", "ru")
	unconfirmed := seedUser(t, r.owner, "reset_unconf", "ru")
	r.withMfa(t, unconfirmed, false)
	pending := seedUser(t, r.owner, "reset_pending", "ru")
	r.withMfa(t, pending, true)
	r.liveReset(t, pending)
	deleted := seedUser(t, r.owner, "reset_deleted", "ru")
	r.withMfa(t, deleted, true)
	acctExec(t, r.env, `UPDATE users SET status = 'deleted' WHERE id = $1`, deleted)
	target := seedUser(t, r.owner, "reset_target", "ru")
	r.withMfa(t, target, true)
	long := strings.Repeat("я", 101)
	nulTicket := "SUP\x004812"

	cases := []struct {
		name   string
		st     *auth.Staff
		uid    uuid.UUID
		reason string
		ticket *string
		status int
		code   string
		field  string
	}{
		{"нет игрока", owner, id.New(), resetReason, nil, http.StatusNotFound, identity.CodeUserNotFound, ""},
		{"удалён", owner, deleted, resetReason, nil, http.StatusNotFound, identity.CodeUserNotFound, ""},
		{"своя учётка", staffMember(perm.Admin, "Борис", target), target, resetReason, nil, http.StatusConflict,
			identity.CodeSelfAction, ""},
		{"нет 2FA", owner, noMfa, resetReason, nil, http.StatusConflict, identity.CodeMfaNotEnabled, ""},
		{"2FA не подтверждена", owner, unconfirmed, resetReason, nil, http.StatusConflict, identity.CodeMfaNotEnabled, ""},
		{"живой сброс", owner, pending, resetReason, nil, http.StatusConflict, identity.CodeMfaResetPending, ""},
		{"причина короче 3", owner, target, "ab", nil, http.StatusBadRequest, httpx.CodeValidationFailed, "body.reason"},
		{"причина длиннее 500", owner, target, strings.Repeat("я", 501), nil, http.StatusBadRequest,
			httpx.CodeValidationFailed, "body.reason"},
		{"обращение длиннее 100", owner, target, resetReason, &long, http.StatusBadRequest, httpx.CodeValidationFailed,
			"body.ticket"},
		// NUL: text и jsonb его не хранят (22021/22P05 → было 500)
		{"NUL в причине", owner, target, "Потерял\x00телефон", nil, http.StatusBadRequest, httpx.CodeValidationFailed,
			"body.reason"},
		{"NUL в обращении", owner, target, resetReason, &nulTicket, http.StatusBadRequest, httpx.CodeValidationFailed,
			"body.ticket"},
		{"поддержка", staffMember(perm.Support, "Вера", uuid.Nil), target, resetReason, nil, http.StatusForbidden,
			httpx.CodeForbidden, ""},
	}
	for _, c := range cases {
		_, err := r.request(c.st, c.uid, c.reason, c.ticket)
		status, code := problemStatus(err)
		if status != c.status || code != c.code {
			t.Errorf("%s: %v (%d %s), ждали %d %s", c.name, err, status, code, c.status, c.code)
			continue
		}
		if c.field != "" && !strings.Contains(err.Error(), c.field) {
			if fe, ok := errors.AsType[httpx.ProblemError](err); !ok || !strings.Contains(fmtProblem(fe), c.field) {
				t.Errorf("%s: нет поля %s в %v", c.name, c.field, err)
			}
		}
	}
	if n := count(t, r.owner, `SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("отказы оставили запросы: %d", n)
	}

	ticket := "SUP-4812"
	ap, err := r.request(owner, target, resetReason, &ticket)
	if err != nil {
		t.Fatal(err)
	}
	if ap.Status != approvals.StatusPending || ap.Action != ActionMfaReset || ap.TargetID != target {
		t.Fatalf("запрос: %+v", ap)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM audit_log WHERE action = 'platform.approval_requested'`); n != 1 {
		t.Fatalf("аудит запроса: %d", n)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE user_id = $1`, target); n != 0 {
		t.Fatal("запрос исполнился сам (dual)")
	}
}

// fmtProblem — поля ошибки валидации текстом (для поиска поля).
func fmtProblem(pe httpx.ProblemError) string {
	var b strings.Builder
	for _, f := range pe.Problem().Errors {
		b.WriteString(f.Field)
		b.WriteString(" ")
	}
	return b.String()
}

// approve — подтверждение запроса сотрудником st.
func (r *resetEnv) approve(st *auth.Staff, apID uuid.UUID) (approvals.Approval, error) {
	return r.svc.Approve(context.Background(), st, st.Label, approvals.Meta{}, apID, "")
}

func TestMfaResetApproveExecutes(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	uid := seedUser(t, r.owner, "reset_exec", "ru")
	r.withMfa(t, uid, true)
	sid := issue(t, r.env, uid, web("a")).SessionID
	var verBefore int64
	if err := r.owner.QueryRow(context.Background(), `SELECT version FROM users WHERE id = $1`, uid).Scan(&verBefore); err != nil {
		t.Fatal(err)
	}
	initiator := staffMember(perm.Owner, "Ольга", uuid.Nil)
	approver := staffMember(perm.Admin, "Борис", uuid.Nil)
	ticket := "SUP-4812"
	ap, err := r.request(initiator, uid, resetReason, &ticket)
	if err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Hour)
	done, err := r.approve(approver, ap.ID)
	if err != nil || done.Status != approvals.StatusExecuted {
		t.Fatalf("подтверждение: %v %+v", err, done)
	}
	var (
		rid               uuid.UUID
		executeAt         time.Time
		by, label, reason string
		tk                *string
		apID              uuid.UUID
	)
	if err := r.owner.QueryRow(context.Background(), `SELECT id, execute_at, requested_by_staff_id::text, requested_by_label,
		reason, ticket, approval_id FROM mfa_resets WHERE user_id = $1 AND executed_at IS NULL AND cancelled_at IS NULL`, uid).
		Scan(&rid, &executeAt, &by, &label, &reason, &tk, &apID); err != nil {
		t.Fatal(err)
	}
	if want := testNow.Add(time.Hour + domain.MfaResetDelay); !executeAt.Equal(want) {
		t.Errorf("execute_at %s, ждали %s", executeAt, want)
	}
	if by != initiator.StaffID.String() || label != "Ольга" || reason != resetReason || tk == nil || *tk != ticket || apID != ap.ID {
		t.Errorf("строка сброса: %s %s %q %v %s", by, label, reason, tk, apID)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_type = 'mfa_reset'
		AND aggregate_id = $2 AND aggregate_version = 1`, identity.EventMfaResetScheduled, rid); n != 1 {
		t.Errorf("событие mfa_reset_scheduled: %d", n)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'mfa_reset_scheduled'`, uid); n != 1 {
		t.Errorf("журнал безопасности: %d", n)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE queue = 'mail' AND args->>'mail' = $1 AND args->>'ref' = $2`,
		MailMfaResetScheduled, rid.String()); n != 1 {
		t.Errorf("письмо mfa_reset_scheduled: %d", n)
	}
	// аудит — от подтверждающего с подписью решения; инициатор и approval_id — в after; причины текстом нет
	var after string
	if err := r.owner.QueryRow(context.Background(), `SELECT after::text FROM audit_log WHERE action = $1
		AND actor_staff_id = $2 AND actor_label = 'Борис' AND object_id = $3`, ActionMfaResetRequested, approver.StaffID, uid).
		Scan(&after); err != nil {
		t.Fatalf("аудит mfa_reset_requested: %v", err)
	}
	for _, want := range []string{ap.ID.String(), initiator.StaffID.String(), rid.String()} {
		if !strings.Contains(after, want) {
			t.Errorf("after без %s: %s", want, after)
		}
	}
	if strings.Contains(after, "Потерял") {
		t.Errorf("свободный текст причины в аудите: %s", after)
	}
	// обращение — только признак наличия (R1 W5: свободный текст сотрудника в after не кладётся)
	if strings.Contains(after, ticket) || !strings.Contains(after, `"ticket_present": true`) {
		t.Errorf("обращение в аудите: %s", after)
	}
	// users и sessions не тронуты (у admin нет UPDATE — К2)
	var verAfter int64
	if err := r.owner.QueryRow(context.Background(), `SELECT version FROM users WHERE id = $1`, uid).Scan(&verAfter); err != nil {
		t.Fatal(err)
	}
	if verAfter != verBefore || count(t, r.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid) != 1 {
		t.Fatal("исполнитель тронул users или sessions")
	}
	if count(t, r.owner, `SELECT count(*) FROM user_mfa WHERE user_id = $1`, uid) != 1 {
		t.Fatal("2FA снята до срока")
	}
}

func TestMfaResetApproverLinkedFails(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	uid := seedUser(t, r.owner, "reset_linked", "ru")
	r.withMfa(t, uid, true)
	ap, err := r.request(staffMember(perm.Owner, "Ольга", uuid.Nil), uid, resetReason, nil)
	if err != nil {
		t.Fatal(err)
	}
	// связь появилась после загрузки сессии: в снимке её нет, исполнитель перечитывает её в tx решения
	b := staffMember(perm.Admin, "Борис", uuid.Nil)
	r.links.set(b.StaffID, uid)
	out, err := r.approve(b, ap.ID)
	if status, code := problemStatus(err); status != http.StatusConflict || code != identity.CodeSelfAction {
		t.Fatalf("подтверждающий связан с игроком: %v", err)
	}
	if out.Status != approvals.StatusFailed {
		t.Fatalf("запрос: %s", out.Status)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets`); n != 0 {
		t.Fatal("сброс назначен связанным сотрудником")
	}
}

func TestMfaResetStaleOnApprove(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		after func(r *resetEnv, uid uuid.UUID)
	}{
		{"игрок выключил 2FA", func(r *resetEnv, uid uuid.UUID) {
			acctExec(t, r.env, `DELETE FROM user_mfa WHERE user_id = $1`, uid)
		}},
		{"сброс уже назначен", func(r *resetEnv, uid uuid.UUID) { r.liveReset(t, uid) }},
		{"аккаунт в удалении", func(r *resetEnv, uid uuid.UUID) {
			acctExec(t, r.env, `UPDATE users SET status = 'pending_deletion', status_before_deletion = status,
				deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`, uid)
		}},
		{"аккаунт удалён", func(r *resetEnv, uid uuid.UUID) {
			acctExec(t, r.env, `UPDATE users SET status = 'deleted' WHERE id = $1`, uid)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newResetEnv(t)
			uid := seedUser(t, r.owner, "reset_stale", "ru")
			r.withMfa(t, uid, true)
			ap, err := r.request(staffMember(perm.Owner, "Ольга", uuid.Nil), uid, resetReason, nil)
			if err != nil {
				t.Fatal(err)
			}
			c.after(r, uid)
			before := count(t, r.owner, `SELECT count(*) FROM mfa_resets`)
			out, err := r.approve(staffMember(perm.Admin, "Борис", uuid.Nil), ap.ID)
			if !errors.Is(err, approvals.ErrStale) || out.Status != approvals.StatusFailed {
				t.Fatalf("ждали failed + approval_stale: %v %s", err, out.Status)
			}
			if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets`); n != before {
				t.Fatal("исполнитель записал сброс")
			}
			if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`, identity.EventMfaResetScheduled); n != 0 {
				t.Fatal("событие при отказе")
			}
		})
	}
}

// TestMfaResetConcurrentApprovals — два запроса на одного игрока подтверждаются разом: оба прошли проверку «живого
// сброса нет» (барьер после чтения состояния), гонку решает mfa_resets_live_key — один executed, второй failed и
// 409 platform.approval_stale (23505 → ErrStale), не 500 и не pending.
func TestMfaResetConcurrentApprovals(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	uid := seedUser(t, r.owner, "reset_race", "ru")
	r.withMfa(t, uid, true)
	var aps []uuid.UUID
	for _, name := range []string{"Ольга", "Вера"} {
		ap, err := r.request(staffMember(perm.Owner, name, uuid.Nil), uid, resetReason, nil)
		if err != nil {
			t.Fatal(err)
		}
		aps = append(aps, ap.ID)
	}
	barrier := pairBarrier(10 * time.Second)
	r.adm.hooks = &hooks{afterRowFound: func(kind string) {
		if kind == rowMfaResetState {
			barrier()
		}
	}}
	start := make(chan struct{})
	errs := make([]error, 2)
	outs := make([]approvals.Approval, 2)
	var wg sync.WaitGroup
	for i := range aps {
		wg.Go(func() {
			<-start
			outs[i], errs[i] = r.approve(staffMember(perm.Admin, "Борис", uuid.Nil), aps[i])
		})
	}
	close(start)
	wg.Wait()
	executed, stale := 0, 0
	for i := range errs {
		switch {
		case errs[i] == nil && outs[i].Status == approvals.StatusExecuted:
			executed++
		case errors.Is(errs[i], approvals.ErrStale) && outs[i].Status == approvals.StatusFailed:
			stale++
		default:
			t.Errorf("подтверждение %d: %v %s", i, errs[i], outs[i].Status)
		}
	}
	if executed != 1 || stale != 1 {
		t.Fatalf("executed %d, stale %d", executed, stale)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE user_id = $1`, uid); n != 1 {
		t.Fatalf("сбросов %d", n)
	}
}

func TestCancelMfaResetByStaff(t *testing.T) {
	t.Parallel()
	r := newResetEnv(t)
	uid := seedUser(t, r.owner, "reset_cancel", "ru")
	r.withMfa(t, uid, true)
	rid := r.liveReset(t, uid)
	ctx := context.Background()

	// своя учётка — 409, сброс жив
	_, err := r.adm.CancelMfaResetByStaff(ctx, staffMember(perm.Admin, "Борис", uid), approvals.Meta{}, rid)
	if status, code := problemStatus(err); status != http.StatusConflict || code != identity.CodeSelfAction {
		t.Fatalf("своя учётка: %v", err)
	}
	// поддержка (нет права) — 403
	_, err = r.adm.CancelMfaResetByStaff(ctx, staffMember(perm.Support, "Вера", uuid.Nil), approvals.Meta{}, rid)
	if status, _ := problemStatus(err); status != http.StatusForbidden {
		t.Fatalf("поддержка: %v", err)
	}
	r.clock.Advance(time.Hour)
	st := staffMember(perm.Owner, "Ольга", uuid.Nil)
	out, err := r.adm.CancelMfaResetByStaff(ctx, st, approvals.Meta{}, rid)
	if err != nil || out.ID != rid || !out.CancelledAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("отмена: %v %+v", err, out)
	}
	if n := count(t, r.owner, `SELECT count(*) FROM mfa_resets WHERE id = $1 AND cancelled_by = 'staff:Ольга'`, rid); n != 1 {
		t.Fatal("cancelled_by")
	}
	if n := count(t, r.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2 AND aggregate_version = 2`,
		identity.EventMfaResetCancelled, rid); n != 1 {
		t.Fatal("событие отмены")
	}
	if n := count(t, r.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1 AND args->>'ref' = $2`,
		MailMfaResetCancelled, rid.String()); n != 1 {
		t.Fatal("письмо отмены")
	}
	if n := count(t, r.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_staff_id = $2 AND object_id = $3`,
		ActionMfaResetCancelled, st.StaffID, uid); n != 1 {
		t.Fatal("аудит отмены")
	}
	// повтор и чужой id — 404
	for _, target := range []uuid.UUID{rid, id.New()} {
		_, err = r.adm.CancelMfaResetByStaff(ctx, st, approvals.Meta{}, target)
		if status, code := problemStatus(err); status != http.StatusNotFound || code != identity.CodeMfaResetNotFound {
			t.Fatalf("повтор или чужой: %v", err)
		}
	}
}
