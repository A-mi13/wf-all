package approvals_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
)

// bizErr — бизнес-ошибка модуля (как staff.ErrLastOwner): её код (Code) — текст error у failed.
type bizErr struct{}

func (bizErr) Error() string { return "бизнес: последний владелец" }

func (bizErr) Code() string { return "test.last_owner" }

var errBiz error = bizErr{}

// Подтверждение: исполнитель вызван с подтверждающим, executed, решивший и комментарий в строке,
// аудит от решившего с комментарием в reason, одно событие platform.approval_decided.
func TestApproveExecutes(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	tg := id.New()
	ap := e.request(a, actGrant, payload{V: 1, Target: &tg})
	var got *auth.Staff
	var gotPayload string
	e.setExec(func(ctx context.Context, _ pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
		got, gotPayload = approver, string(raw)
		return moduleWrite(ctx, approver)
	})
	e.clock.Advance(time.Hour)
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "  проверил лично  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != b || !strings.Contains(gotPayload, tg.String()) {
		t.Fatalf("исполнитель: approver=%v payload=%s", got, gotPayload)
	}
	if res.Status != approvals.StatusExecuted || res.DecidedBy != b.StaffID || res.DecidedByLabel != "Борис" ||
		res.Comment != "проверил лично" || res.DecidedAt == nil || !res.DecidedAt.Equal(t0.Add(time.Hour)) || res.Error != "" {
		t.Fatalf("ответ: %+v", res)
	}
	if r := e.row(ap.ID); r.Status != "executed" || r.Label != "Борис" || r.Comment != "проверил лично" || *r.DecidedBy != b.StaffID {
		t.Fatalf("строка: %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_executed' AND object_id = $1
		AND actor_staff_id = $2 AND actor_label = 'Борис' AND actor_role = 'owner' AND reason = 'проверил лично'`,
		ap.ID, b.StaffID); n != 1 {
		t.Fatalf("аудит решения %d", n)
	}
	if n := e.decided(ap.ID); n != 1 {
		t.Fatalf("событий %d", n)
	}
	var pl string
	if err := e.owner.QueryRow(context.Background(), `SELECT payload::text FROM outbox WHERE aggregate_id = $1
		AND aggregate_type = 'approval' AND aggregate_version = 1 AND schema_version = 1`, ap.ID).Scan(&pl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pl, `"status": "executed"`) || !strings.Contains(pl, actGrant) || strings.Contains(pl, "Борис") {
		t.Fatalf("payload события: %s", pl)
	}
	if n := e.moduleWrites(); n != 2 {
		t.Fatalf("изменения модуля %d", n)
	}
}

// Правило двух рук: инициатор и цель не решают (ErrSelf), ничего не пишется.
func TestApproveSelf(t *testing.T) {
	e := newEnv(t)
	a, tgt := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1, Target: &tgt.StaffID})
	for name, st := range map[string]*auth.Staff{"инициатор": a, "цель": tgt} {
		if _, err := e.svc.Approve(context.Background(), st, "Я", metaWeb, ap.ID, ""); !isErr(err, approvals.ErrSelf) {
			t.Errorf("approve %s: %v", name, err)
		}
		if _, err := e.svc.Reject(context.Background(), st, "Я", metaWeb, ap.ID, "нет"); !isErr(err, approvals.ErrSelf) {
			t.Errorf("reject %s: %v", name, err)
		}
	}
	if r := e.row(ap.ID); r.Status != "pending" || e.execs.Load() != 0 || e.decided(ap.ID) != 0 {
		t.Fatalf("после отказа: %+v, исполнений %d", r, e.execs.Load())
	}
}

// Без права запроса, чужой город модератора, сессия не active — ErrForbidden; свой город — можно.
func TestApproveForbidden(t *testing.T) {
	e := newEnv(t)
	cityA, cityB := id.New(), id.New()
	a := e.member(perm.Owner)
	grant := e.request(a, actGrant, payload{V: 1})
	rename := e.request(a, actRename, payload{V: 1, City: &cityA})
	reauth := e.member(perm.Owner)
	reauth.Reauth = true
	setup := e.member(perm.Owner)
	setup.Stage = auth.StageSetup
	cases := []struct {
		name string
		st   *auth.Staff
		apID uuid.UUID
	}{
		{"модератор выдаёт роль", e.member(perm.Moderator, cityA), grant.ID},
		{"поддержка выдаёт роль", e.member(perm.Support), grant.ID},
		{"модератор чужого города", e.member(perm.Moderator, cityB), rename.ID},
		{"поддержка переименовывает", e.member(perm.Support), rename.ID},
		{"под reauth", reauth, grant.ID},
		{"стадия setup", setup, grant.ID},
		{"без сотрудника", nil, grant.ID},
	}
	for _, c := range cases {
		if _, err := e.svc.Approve(context.Background(), c.st, "Кто-то", metaWeb, c.apID, ""); !isErr(err, approvals.ErrForbidden) {
			t.Errorf("approve %s: %v", c.name, err)
		}
		if _, err := e.svc.Reject(context.Background(), c.st, "Кто-то", metaWeb, c.apID, "нет"); !isErr(err, approvals.ErrForbidden) {
			t.Errorf("reject %s: %v", c.name, err)
		}
	}
	if e.execs.Load() != 0 || e.row(grant.ID).Status != "pending" || e.row(rename.ID).Status != "pending" {
		t.Fatal("отказ что-то изменил")
	}
	if _, err := e.svc.Approve(context.Background(), e.member(perm.Moderator, cityA), "Мод", metaWeb, rename.ID, ""); err != nil {
		t.Fatalf("модератор своего города: %v", err)
	}
}

// Право подтверждающего — сохранённое при запросе (approvals.permission), а не пересчёт Permit по
// текущему payload: админ не подтверждает запрос на выдачу роли владельца (grant_elevated), даже
// если payload в строке поправили, а «обычный» запрос подтверждает.
func TestApproveUsesStoredPermission(t *testing.T) {
	e := newEnv(t)
	a, admin := e.member(perm.Owner), e.member(perm.Admin)
	elevated := e.request(a, actGrant, payload{V: 1, Elevated: true})
	plain := e.request(a, actGrant, payload{V: 1})
	if _, err := e.owner.Exec(context.Background(), `UPDATE approvals SET payload = jsonb_set(payload, '{elevated}',
		CASE WHEN id = $1 THEN 'false'::jsonb ELSE 'true'::jsonb END) WHERE id IN ($1, $2)`, elevated.ID, plain.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(context.Background(), admin, "Админ", metaWeb, elevated.ID, ""); !isErr(err, approvals.ErrForbidden) {
		t.Fatalf("админ подтвердил grant_elevated: %v", err)
	}
	if _, err := e.svc.Approve(context.Background(), admin, "Админ", metaWeb, plain.ID, ""); err != nil {
		t.Fatalf("админ и обычная выдача: %v", err)
	}
}

// Срок вышел: строка expired от системы коммитится (D8), наружу ErrClosed, исполнителя нет. Граница —
// ровно expires_at: за микросекунду до неё ещё можно.
func TestApproveExpired(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	late := e.request(a, actGrant, payload{V: 1})
	e.clock.Advance(time.Microsecond)
	early := e.request(a, actGrant, payload{V: 1})
	e.clock.Set(late.ExpiresAt) // early истекает через 1 мкс
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, late.ID, "")
	if !isErr(err, approvals.ErrClosed) || res.Status != approvals.StatusExpired {
		t.Fatalf("ровно в срок: %v %+v", err, res)
	}
	r := e.row(late.ID)
	if r.Status != "expired" || r.Label != approvals.SystemLabel || r.DecidedBy != nil || r.Error != "" ||
		r.DecidedAt == nil || !r.DecidedAt.Equal(late.ExpiresAt) {
		t.Fatalf("строка: %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_expired' AND object_id = $1
		AND actor_staff_id IS NULL AND actor_role = 'system' AND actor_label = 'system'`, late.ID); n != 1 || e.decided(late.ID) != 1 {
		t.Fatalf("аудит %d, событий %d", n, e.decided(late.ID))
	}
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, early.ID, ""); err != nil {
		t.Fatalf("за микросекунду до срока: %v", err)
	}
	if e.execs.Load() != 1 {
		t.Fatalf("исполнений %d", e.execs.Load())
	}
	// отклонение и отмена просроченного — тоже expired и ErrClosed
	rej := e.request(a, actGrant, payload{V: 1})
	can := e.request(a, actGrant, payload{V: 1})
	e.clock.Advance(approvals.TTL)
	if _, err := e.svc.Reject(context.Background(), b, "Борис", metaWeb, rej.ID, "поздно"); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("reject просроченного: %v", err)
	}
	if _, err := e.svc.Cancel(context.Background(), a, "Анна", metaWeb, can.ID); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("cancel просроченного: %v", err)
	}
	if e.row(rej.ID).Status != "expired" || e.row(can.ID).Status != "expired" {
		t.Fatal("просроченные не expired")
	}
}

// Решённый запрос не решается второй раз: ErrClosed, исполнитель — один раз.
func TestApproveTwiceClosed(t *testing.T) {
	e := newEnv(t)
	a, b, c := e.member(perm.Owner), e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, ""); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("повтор: %v", err)
	}
	if _, err := e.svc.Approve(context.Background(), c, "Вера", metaWeb, ap.ID, ""); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("второй подтверждающий: %v", err)
	}
	if _, err := e.svc.Reject(context.Background(), c, "Вера", metaWeb, ap.ID, "нет"); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("отклонение решённого: %v", err)
	}
	if _, err := e.svc.Cancel(context.Background(), a, "Анна", metaWeb, ap.ID); !isErr(err, approvals.ErrClosed) {
		t.Fatalf("отмена решённого: %v", err)
	}
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, id.New(), ""); !isErr(err, approvals.ErrNotFound) {
		t.Fatalf("нет запроса: %v", err)
	}
	if e.execs.Load() != 1 || e.decided(ap.ID) != 1 {
		t.Fatalf("исполнений %d, событий %d", e.execs.Load(), e.decided(ap.ID))
	}
}

// Инициатор выключен или лишился права: failed с причиной initiator_changed коммитится, наружу
// ErrStale, исполнителя нет; аудит failed от подтверждающего и событие.
func TestApproveInitiatorChanged(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	off := e.request(a, actGrant, payload{V: 1})
	e.disable(a.StaffID)
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, off.ID, "")
	if !isErr(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || res.Error != approvals.ErrorInitiatorChanged {
		t.Fatalf("выключенный инициатор: %v %+v", err, res)
	}
	if r := e.row(off.ID); r.Status != "failed" || r.Error != approvals.ErrorInitiatorChanged || *r.DecidedBy != b.StaffID {
		t.Fatalf("строка: %+v", r)
	}
	if e.audits(off.ID, "platform.approval_failed") != 1 || e.decided(off.ID) != 1 {
		t.Fatal("нет аудита или события failed")
	}
	// инициатор-владелец стал админом: права grant_elevated больше нет
	o := e.member(perm.Owner)
	elevated := e.request(o, actGrant, payload{V: 1, Elevated: true})
	e.mu.Lock()
	e.staff[o.StaffID] = &auth.Staff{StaffID: o.StaffID, Role: "admin", Stage: auth.StageActive, Grants: perm.Grants(perm.Admin)}
	e.mu.Unlock()
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, elevated.ID, ""); !isErr(err, approvals.ErrStale) {
		t.Fatalf("инициатор без права: %v", err)
	}
	if e.execs.Load() != 0 {
		t.Fatalf("исполнений %d", e.execs.Load())
	}
}

// Бизнес-отказ исполнителя (Fail) после его записей и ошибки SQL: savepoint откатывает изменения
// модуля, failed с кодом ошибки коммитится, наружу — исходная ошибка. То же — ErrStale исполнителя.
func TestApproveExecutorFail(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	e.setExec(func(ctx context.Context, tx pgx.Tx, _ json.RawMessage, approver *auth.Staff) error {
		if err := moduleWrite(ctx, approver); err != nil {
			return err
		}
		// ошибка SQL ломает транзакцию — без savepoint пометка failed была бы невозможна
		if _, err := tx.Exec(ctx, `SELECT 1 / 0`); err == nil {
			return errors.New("деление на ноль прошло")
		}
		return approvals.Fail(fmt.Errorf("исполнитель: %w", errBiz))
	})
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "ок")
	if !errors.Is(err, errBiz) || isErr(err, approvals.ErrStale) {
		t.Fatalf("ошибка наружу: %v", err)
	}
	if res.Status != approvals.StatusFailed || res.Error != "test.last_owner" {
		t.Fatalf("ответ: %+v", res)
	}
	if r := e.row(ap.ID); r.Status != "failed" || r.Error != "test.last_owner" || r.Comment != "ок" {
		t.Fatalf("строка: %+v", r)
	}
	if n := e.moduleWrites(); n != 0 {
		t.Fatalf("изменения модуля не откатились: %d", n)
	}
	if e.audits(ap.ID, "platform.approval_failed") != 1 || e.decided(ap.ID) != 1 {
		t.Fatal("нет аудита или события failed")
	}

	stale := e.request(a, actGrant, payload{V: 1})
	e.setExec(func(ctx context.Context, _ pgx.Tx, _ json.RawMessage, approver *auth.Staff) error {
		if err := moduleWrite(ctx, approver); err != nil {
			return err
		}
		return fmt.Errorf("версия цели: %w", approvals.ErrStale)
	})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, stale.ID, ""); !isErr(err, approvals.ErrStale) {
		t.Fatalf("ErrStale исполнителя: %v", err)
	}
	if r := e.row(stale.ID); r.Status != "failed" || r.Error != httpx.CodeApprovalStale {
		t.Fatalf("строка stale: %+v", r)
	}
	if n := e.moduleWrites(); n != 0 {
		t.Fatalf("изменения модуля stale не откатились: %d", n)
	}
}

// Прочая ошибка или паника исполнителя, ошибка проверки инициатора — сбой: откат всего, запрос
// остаётся pending, ни аудита решения, ни события.
func TestApproveFailureRollsBack(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	errDB := errors.New("сеть упала")
	e.setExec(func(ctx context.Context, _ pgx.Tx, _ json.RawMessage, approver *auth.Staff) error {
		if err := moduleWrite(ctx, approver); err != nil {
			return err
		}
		return errDB
	})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, ""); !errors.Is(err, errDB) {
		t.Fatalf("прочая ошибка: %v", err)
	}
	assertUntouched(t, e, ap.ID)

	e.setExec(func(ctx context.Context, _ pgx.Tx, _ json.RawMessage, approver *auth.Staff) error {
		if err := moduleWrite(ctx, approver); err != nil {
			return err
		}
		panic("исполнитель упал")
	})
	func() {
		defer func() {
			if p := recover(); p != "исполнитель упал" {
				t.Fatalf("паника: %v", p)
			}
		}()
		_, _ = e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "")
	}()
	assertUntouched(t, e, ap.ID)

	e.setExec(nil)
	e.mu.Lock()
	e.check = func(context.Context, pgx.Tx, approvals.Approval) error { return errDB }
	e.mu.Unlock()
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, ""); !errors.Is(err, errDB) {
		t.Fatalf("ошибка проверки инициатора: %v", err)
	}
	assertUntouched(t, e, ap.ID)
}

func assertUntouched(t *testing.T, e *env, apID uuid.UUID) {
	t.Helper()
	if r := e.row(apID); r.Status != "pending" || r.DecidedAt != nil {
		t.Fatalf("строка: %+v", r)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE object_id = $1 AND action <> 'platform.approval_requested'`, apID); n != 0 {
		t.Fatalf("аудит решения %d", n)
	}
	if e.decided(apID) != 0 || e.moduleWrites() != 0 {
		t.Fatalf("событий %d, изменений модуля %d", e.decided(apID), e.moduleWrites())
	}
}

// Без зарегистрированной проверки инициатора подтверждение не исполняется (fail-closed).
func TestApproveWithoutInitiatorCheck(t *testing.T) {
	e := newEnv(t)
	reg := approvals.NewRegistry()
	reg.Register(approvals.Action{Name: actGrant, Permission: "staff.roles.grant", Exec: e.run})
	svc := approvals.New(e.pool, e.clock, reg)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	_, err := svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "")
	if err == nil || isErr(err, approvals.ErrStale) || isErr(err, approvals.ErrForbidden) {
		t.Fatalf("без проверки инициатора: %v", err)
	}
	assertUntouched(t, e, ap.ID)
	if e.execs.Load() != 0 {
		t.Fatal("исполнитель вызван без проверки инициатора")
	}
}

// Комментарий подтверждения: пробелы — без комментария; длиннее 500 символов — 400 по body.comment.
func TestApproveComment(t *testing.T) {
	e := newEnv(t)
	a, b := e.member(perm.Owner), e.member(perm.Owner)
	ap := e.request(a, actGrant, payload{V: 1})
	_, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, strings.Repeat("ж", 501))
	assertField(t, err, "body.comment", "maxLength")
	if e.row(ap.ID).Status != "pending" {
		t.Fatal("длинный комментарий изменил запрос")
	}
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, " \n\t "); err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := e.owner.QueryRow(context.Background(), `SELECT decision_comment IS NULL FROM approvals WHERE id = $1`, ap.ID).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("пустой комментарий: null=%v %v", isNull, err)
	}
	// пустая метка решившего — ошибка до решения
	ap2 := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Approve(context.Background(), b, "  ", metaWeb, ap2.ID, ""); err == nil {
		t.Fatal("пустая метка решившего прошла")
	}
	if e.row(ap2.ID).Status != "pending" {
		t.Fatal("пустая метка изменила запрос")
	}
}

func assertField(t *testing.T, err error, field, code string) {
	t.Helper()
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !ok {
		t.Fatalf("ждали ошибку поля %s, получили %v", field, err)
	}
	p := pe.Problem()
	if p.Code != httpx.CodeValidationFailed || len(p.Errors) != 1 || p.Errors[0].Field != field || p.Errors[0].Code != code {
		t.Fatalf("ошибка поля: %+v", p)
	}
}
