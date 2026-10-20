package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/staff"
)

// Подтверждения вдвоём (тег platform, спека §4.8) на настоящих сценариях staff и сервисе платформы за полным
// конвейером: 202 без побочных эффектов, решение вторым владельцем, повтор ключа, гонка двух подтверждающих,
// срок, неудача исполнения (200 — только executed, D347777), подпись решившего, аудит с IP и UA.

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) wf-admin-test"

// owners — n активных владельцев (первый вход пройден).
func (e *staffEnv) owners(n int) []member {
	e.t.Helper()
	out := make([]member, n)
	for i := range out {
		out[i] = e.firstLogin(string(rune('a'+i))+"-owner@wf.example", string(perm.Owner))
	}
	return out
}

// inviteReq — POST /v1/staff/invites с ключом key ("" — новый): тело StaffInviteRequest.
func (e *staffEnv) inviteReq(token, key, email, role string, cities ...uuid.UUID) *httpRec {
	e.t.Helper()
	if key == "" {
		key = uuid.NewString()
	}
	body := map[string]any{"email": email, "display_name": "Иван П.", "role": role}
	if len(cities) > 0 {
		body["city_ids"] = cities
	}
	return e.send(http.MethodPost, "/v1/staff/invites", token, key, jsonOf(e.t, body))
}

// accepted — 202 {approval}: id запроса.
func (e *staffEnv) accepted(rec *httpRec) uuid.UUID {
	e.t.Helper()
	wantStatus(e.t, rec, http.StatusAccepted)
	var b struct {
		Approval struct {
			ID     uuid.UUID `json:"id"`
			Status string    `json:"status"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.Approval.ID == uuid.Nil || b.Approval.Status != approvals.StatusPending {
		e.t.Fatalf("202: %v %s", err, rec.Body.String())
	}
	return b.Approval.ID
}

// decide — POST /v1/approvals/{id}/{verb} (approve | reject | cancel) с ключом key ("" — новый).
func (e *staffEnv) decide(verb string, id uuid.UUID, token, key, body string, hdr ...string) *httpRec {
	e.t.Helper()
	if key == "" {
		key = uuid.NewString()
	}
	return e.send(http.MethodPost, "/v1/approvals/"+id.String()+"/"+verb, token, key, body, hdr...)
}

// approvalRow — состояние запроса в базе: статус и error ("" — NULL).
func (e *staffEnv) approvalRow(id uuid.UUID) (status, errText string) {
	e.t.Helper()
	var ep *string
	if err := e.owner.QueryRow(context.Background(), `SELECT status::text, error FROM approvals WHERE id = $1`, id).
		Scan(&status, &ep); err != nil {
		e.t.Fatalf("запрос %s: %v", id, err)
	}
	if ep != nil {
		errText = *ep
	}
	return status, errText
}

func (e *staffEnv) accounts(email string) int {
	return e.count(`SELECT count(*) FROM staff_accounts WHERE email = $1`, email)
}

func replayed(rec *httpRec) bool { return rec.Header().Get("Idempotency-Replayed") == "true" }

// sameJSON — тела ответов равны как JSON (повтор отдаёт тело из jsonb — форматирование другое).
func sameJSON(t *testing.T, a, b *httpRec) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b.Body.Bytes(), &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

// 202 — не исполнено: создан только запрос (и аудит platform.approval_requested), учётки, задачи River и
// события нет; повтор тем же ключом — тот же запрос, строка одна. Подтверждение вторым владельцем — 200
// executed с решившим; повтор тем же ключом — сохранённый 200, исполнитель не зовётся (учётка одна);
// новым ключом — 409 closed. Аудит решения — с IP (подсеть) и User-Agent запроса.
func TestInviteApproveFlow(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	jobs, events := e.count(`SELECT count(*) FROM river_job`), e.count(`SELECT count(*) FROM outbox`)

	key := uuid.NewString()
	rec := e.inviteReq(a.token, key, "ivan.p@wf.example", "support")
	id := e.accepted(rec)
	if e.accounts("ivan.p@wf.example") != 0 || e.count(`SELECT count(*) FROM river_job`) != jobs ||
		e.count(`SELECT count(*) FROM outbox`) != events {
		t.Fatal("202 оставил побочные эффекты: учётку, задачу или событие")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_requested' AND object_id = $1`, id); n != 1 {
		t.Fatalf("аудит запроса: %d", n)
	}
	again := e.inviteReq(a.token, key, "ivan.p@wf.example", "support")
	if !replayed(again) || e.accepted(again) != id || e.count(`SELECT count(*) FROM approvals`) != 1 {
		t.Fatalf("повтор 202: %v %s", again.Header(), again.Body.String())
	}

	// входящие B — запрос A; у A во входящих его нет, в «моих» — есть
	if rec := e.get("/v1/approvals?box=incoming&status=pending", b.token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), id.String()) {
		t.Fatalf("входящие B: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.get("/v1/approvals?box=incoming", a.token); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), id.String()) {
		t.Fatalf("входящие A: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.get("/v1/approvals?box=mine", a.token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), id.String()) {
		t.Fatalf("мои A: %d %s", rec.Code, rec.Body.String())
	}

	key = uuid.NewString()
	rec = e.decide("approve", id, b.token, key, `{"comment":"Проверила лично"}`, "User-Agent", userAgent)
	wantStatus(t, rec, http.StatusOK)
	got := bodyOf(t, rec)
	decided, _ := got["decided_by"].(map[string]any)
	if got["status"] != approvals.StatusExecuted || decided["id"] != b.id.String() || decided["label"] != "Сотрудник b-owner" ||
		got["comment"] != "Проверила лично" {
		t.Fatalf("approve: %s", rec.Body.String())
	}
	if e.accounts("ivan.p@wf.example") != 1 {
		t.Fatal("приглашение не исполнено")
	}
	var ip, ua *string
	if err := e.owner.QueryRow(context.Background(), `SELECT host(ip), user_agent FROM audit_log
		WHERE action = 'platform.approval_executed' AND object_id = $1`, id).Scan(&ip, &ua); err != nil {
		t.Fatal(err)
	}
	if ip == nil || *ip != clientIP || ua == nil || *ua != userAgent {
		t.Fatalf("аудит решения без IP/UA: %v %v", ip, ua)
	}
	again = e.decide("approve", id, b.token, key, `{"comment":"Проверила лично"}`, "User-Agent", userAgent)
	if !replayed(again) || again.Code != http.StatusOK || !sameJSON(t, again, rec) || e.accounts("ivan.p@wf.example") != 1 {
		t.Fatalf("повтор approve: %d %v %s", again.Code, again.Header(), again.Body.String())
	}
	wantProblem(t, e.decide("approve", id, b.token, "", "{}"), http.StatusConflict, httpx.CodeApprovalClosed)
}

// Свой запрос не подтвердить (409 self); без тела approve — допустимо (комментарий необязателен); чужой
// запрос не отменить (404 — существование не раскрывается); несуществующий — 404.
func TestApprovalDecisionRules(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	wantProblem(t, e.decide("approve", id, a.token, "", ""), http.StatusConflict, httpx.CodeApprovalSelf)
	wantProblem(t, e.decide("reject", id, a.token, "", `{"comment":"нет"}`), http.StatusConflict, httpx.CodeApprovalSelf)
	wantProblem(t, e.decide("cancel", id, b.token, "", ""), http.StatusNotFound, httpx.CodeApprovalNotFound)
	wantProblem(t, e.decide("approve", uuid.New(), b.token, "", ""), http.StatusNotFound, httpx.CodeApprovalNotFound)
	// отказ без комментария (одни пробелы) — 400 body.comment
	rec := e.decide("reject", id, b.token, "", `{"comment":"   "}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"body.comment"`) {
		t.Fatalf("reject без комментария: %d %s", rec.Code, rec.Body.String())
	}
	if st, _ := e.approvalRow(id); st != approvals.StatusPending {
		t.Fatalf("после отказов статус %s", st)
	}
	wantStatus(t, e.decide("approve", id, b.token, "", ""), http.StatusOK)
}

// Отклонение и отмена: 200 с итогом; повтор тем же ключом — сохранённый ответ (второго решения нет).
// Аудит решения — с IP и User-Agent запроса (ревью 10b m1).
func TestRejectCancelReplay(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	r1 := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	r2 := e.accepted(e.inviteReq(a.token, "", "petr.s@wf.example", "support"))
	// «мои» по одной странице: новые сверху, курсор ведёт ко второй
	rec := e.get("/v1/approvals?box=mine&limit=1", a.token)
	wantStatus(t, rec, http.StatusOK)
	var page oapi.ApprovalPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Id != r2 || page.NextCursor == nil {
		t.Fatalf("первая страница: %v %s", err, rec.Body.String())
	}
	rec = e.get("/v1/approvals?box=mine&limit=1&cursor="+*page.NextCursor, a.token)
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Id != r1 || page.NextCursor != nil {
		t.Fatalf("вторая страница: %v %s", err, rec.Body.String())
	}

	key := uuid.NewString()
	rec = e.decide("reject", r1, b.token, key, `{"comment":"Город после испытательного срока"}`, "User-Agent", userAgent)
	wantStatus(t, rec, http.StatusOK)
	if got := bodyOf(t, rec); got["status"] != approvals.StatusRejected || got["comment"] != "Город после испытательного срока" {
		t.Fatalf("reject: %s", rec.Body.String())
	}
	e.wantAuditMeta("platform.approval_rejected", r1)
	again := e.decide("reject", r1, b.token, key, `{"comment":"Город после испытательного срока"}`)
	if !replayed(again) || again.Code != http.StatusOK || !sameJSON(t, again, rec) {
		t.Fatalf("повтор reject: %d %s", again.Code, again.Body.String())
	}

	key = uuid.NewString()
	rec = e.decide("cancel", r2, a.token, key, "", "User-Agent", userAgent)
	wantStatus(t, rec, http.StatusOK)
	if got := bodyOf(t, rec); got["status"] != approvals.StatusCancelled {
		t.Fatalf("cancel: %s", rec.Body.String())
	}
	e.wantAuditMeta("platform.approval_cancelled", r2)
	again = e.decide("cancel", r2, a.token, key, "")
	if !replayed(again) || again.Code != http.StatusOK || !sameJSON(t, again, rec) {
		t.Fatalf("повтор cancel: %d %s", again.Code, again.Body.String())
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action IN ('platform.approval_rejected', 'platform.approval_cancelled')`); n != 2 {
		t.Fatalf("решений в журнале %d, ждали 2", n)
	}
	wantProblem(t, e.decide("cancel", r2, a.token, "", ""), http.StatusConflict, httpx.CodeApprovalClosed)
}

// Ровно в момент истечения (now = requested_at + 24 ч): expired от системы коммитится, ответ 409 closed;
// повтор тем же ключом — тот же сохранённый 409.
func TestApproveAtExpiry(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	var expires time.Time
	if err := e.owner.QueryRow(context.Background(), `SELECT expires_at FROM approvals WHERE id = $1`, id).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	// сессия B истекла за сутки — вход заново; signIn сдвигает часы на шаг TOTP (30 с)
	e.clock.Set(expires.Add(-30 * time.Second))
	tok := e.signIn(b)
	if !e.clock.Now().Equal(expires) {
		t.Fatalf("часы %v, срок %v", e.clock.Now(), expires)
	}
	key := uuid.NewString()
	rec := e.decide("approve", id, tok, key, "")
	wantProblem(t, rec, http.StatusConflict, httpx.CodeApprovalClosed)
	if st, _ := e.approvalRow(id); st != approvals.StatusExpired || e.accounts("ivan.p@wf.example") != 0 {
		t.Fatalf("статус %s, учёток %d", st, e.accounts("ivan.p@wf.example"))
	}
	again := e.decide("approve", id, tok, key, "")
	if !replayed(again) || again.Code != http.StatusConflict || !sameJSON(t, again, rec) {
		t.Fatalf("повтор: %d %v %s", again.Code, again.Header(), again.Body.String())
	}
}

// Два подтверждающих одновременно (барьер: оба прошли конвейер до решения) — один 200 executed, другой 409
// closed; учётка одна.
func TestApproveConcurrent(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(3)
	id := e.accepted(e.inviteReq(o[0].token, "", "ivan.p@wf.example", "support"))
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	hook := func(s *auth.Staff) {
		if s.StaffID == o[1].id || s.StaffID == o[2].id {
			arrived.Done()
			<-release
		}
	}
	e.onLoad.Store(&hook)
	codes := make([]int, 2)
	var done sync.WaitGroup
	for i, m := range o[1:] {
		done.Add(1)
		go func() {
			defer done.Done()
			codes[i] = e.decide("approve", id, m.token, "", "").Code
		}()
	}
	arrived.Wait()
	close(release)
	done.Wait()
	e.onLoad.Store(nil)
	ok, closed := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			closed++
		}
	}
	if ok != 1 || closed != 1 || e.accounts("ivan.p@wf.example") != 1 {
		t.Fatalf("ответы %v, учёток %d", codes, e.accounts("ivan.p@wf.example"))
	}
}

// Подтверждающий выключен или сменил роль между загрузкой сессии и решением: подпись решившего перечитывает
// учётку и сессию — 401 / 403, запрос остаётся pending, решений в журнале нет (approve и reject).
func TestDecisionByChangedApprover(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(3)
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(o[0].token, "", "ivan.p@wf.example", "support"))
	for _, c := range []struct {
		name   string
		who    member
		sql    string
		status int
		code   string
	}{
		{"выключен", o[1], `UPDATE staff_accounts SET status = 'disabled' WHERE id = $1`, http.StatusUnauthorized, httpx.CodeUnauthenticated},
		{"сменил роль", o[2], `UPDATE staff_roles SET role = 'admin' WHERE staff_id = $1`, http.StatusForbidden, httpx.CodeForbidden},
	} {
		for _, verb := range []string{"approve", "reject"} {
			done := false
			hook := func(s *auth.Staff) {
				if s.StaffID == c.who.id && !done {
					done = true
					e.exec(c.sql, c.who.id)
				}
			}
			e.onLoad.Store(&hook)
			rec := e.decide(verb, id, c.who.token, "", `{"comment":"ок"}`)
			e.onLoad.Store(nil)
			wantProblem(t, rec, c.status, c.code)
			// выключен — сессия больше не годится: cookie стирается (ревью 10b m3); роль сменилась — нет
			if c.status == http.StatusUnauthorized {
				clearedCookie(t, rec)
			} else {
				noCookie(t, rec)
			}
			if st, _ := e.approvalRow(id); st != approvals.StatusPending {
				t.Fatalf("%s, %s: статус %s", c.name, verb, st)
			}
			// вернуть как было — для второго глагола
			if c.status == http.StatusUnauthorized {
				e.exec(`UPDATE staff_accounts SET status = 'active' WHERE id = $1`, c.who.id)
			} else {
				e.exec(`UPDATE staff_roles SET role = 'owner' WHERE staff_id = $1`, c.who.id)
			}
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action LIKE 'platform.approval\_%' AND action <> 'platform.approval_requested'`); n != 0 {
		t.Fatalf("решений в журнале: %d", n)
	}
}

// Сессию погасили между конвейером и решением: подпись решившего (Label) перечитывает сессию — 401 и
// стирание cookie, как у операций staff (ревью 10b m3); запрос остаётся pending. approve, reject и cancel.
func TestDecisionRevokedSessionClearsCookie(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	for _, c := range []struct {
		verb string
		who  member
		body string
	}{
		{"approve", b, ""},
		{"reject", b, `{"comment":"Рано"}`},
		{"cancel", a, ""},
	} {
		tok := e.signIn(c.who) // погашенная сессия прошлого шага больше не годится
		e.revokeOnLoad("logout")
		rec := e.decide(c.verb, id, tok, "", c.body)
		wantProblem(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
		clearedCookie(t, rec)
		if st, _ := e.approvalRow(id); st != approvals.StatusPending {
			t.Fatalf("%s: статус %s", c.verb, st)
		}
	}
}

// Неудача исполнения — ошибка с кодом (200 только executed), запрос failed закоммичен под ключом: почта
// занята к моменту подтверждения (второе приглашение на ту же почту) → 409 staff.email_taken; повтор тем
// же ключом — сохранённый 409.
func TestApproveEmailTakenFailed(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	first := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	second := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	wantStatus(t, e.decide("approve", first, b.token, "", ""), http.StatusOK)
	key := uuid.NewString()
	rec := e.decide("approve", second, b.token, key, "", "User-Agent", userAgent)
	wantProblem(t, rec, http.StatusConflict, staff.CodeEmailTaken)
	if st, errText := e.approvalRow(second); st != approvals.StatusFailed || errText != staff.CodeEmailTaken {
		t.Fatalf("запрос %s %s", st, errText)
	}
	e.wantAuditMeta("platform.approval_failed", second)
	again := e.decide("approve", second, b.token, key, "")
	if !replayed(again) || again.Code != http.StatusConflict || !sameJSON(t, again, rec) {
		t.Fatalf("повтор: %d %s", again.Code, again.Body.String())
	}
}

// Город модератора удалён после запроса — 422 staff.city_not_found, failed с этим кодом.
func TestApproveCityNotFoundFailed(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	e.v = apitest.New(t, oapi.GetSpec)
	city := e.city("vremensk")
	id := e.accepted(e.inviteReq(o[0].token, "", "ivan.p@wf.example", "moderator", city))
	e.exec(`DELETE FROM cities WHERE id = $1`, city)
	wantProblem(t, e.decide("approve", id, o[1].token, "", ""), http.StatusUnprocessableEntity, staff.CodeCityNotFound)
	if st, errText := e.approvalRow(id); st != approvals.StatusFailed || errText != staff.CodeCityNotFound {
		t.Fatalf("запрос %s %s", st, errText)
	}
}

// Инициатор изменился после запроса (версия учётки) — 409 platform.approval_stale, failed с причиной
// initiator_changed.
func TestApproveInitiatorChanged(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	e.v = apitest.New(t, oapi.GetSpec)
	id := e.accepted(e.inviteReq(o[0].token, "", "ivan.p@wf.example", "support"))
	e.exec(`UPDATE staff_accounts SET version = version + 1 WHERE id = $1`, o[0].id)
	wantProblem(t, e.decide("approve", id, o[1].token, "", ""), http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(id); st != approvals.StatusFailed || errText != approvals.ErrorInitiatorChanged {
		t.Fatalf("запрос %s %s", st, errText)
	}
}

// Каждый staff.* у approve — закоммиченный failed с этим кодом (тестовые действия: ветки исполнителя без
// живого сценария — последний владелец в пути подтверждения недостижим, инициатор и подтверждающий оба
// владельцы). Код вне x-error-codes approve (staff.not_found) и отказ без кода — 500 internal, запрос всё
// равно failed; сбой без Fail — 500, запрос pending.
func TestApproveStaffCodesAreCommittedFailed(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	errBoom := errors.New("сбой исполнителя: ivan.p@wf.example") // текст с ПД не должен попасть в ответ и строку
	cases := []struct {
		action  string
		exec    error
		status  int
		code    string
		row     string // статус запроса
		errText string
	}{
		{"test.last_owner", approvals.Fail(staff.ErrLastOwner), http.StatusConflict, staff.CodeLastOwner, approvals.StatusFailed, staff.CodeLastOwner},
		{"test.email_taken", approvals.Fail(staff.ErrEmailTaken), http.StatusConflict, staff.CodeEmailTaken, approvals.StatusFailed, staff.CodeEmailTaken},
		{"test.city_not_found", approvals.Fail(staff.ErrCityNotFound), http.StatusUnprocessableEntity, staff.CodeCityNotFound, approvals.StatusFailed, staff.CodeCityNotFound},
		{"test.not_found", approvals.Fail(staff.ErrNotFound), http.StatusInternalServerError, httpx.CodeInternal, approvals.StatusFailed, staff.CodeNotFound},
		{"test.no_code", approvals.Fail(errBoom), http.StatusInternalServerError, httpx.CodeInternal, approvals.StatusFailed, approvals.ErrorExecutionFailed},
		{"test.crash", errBoom, http.StatusInternalServerError, httpx.CodeInternal, approvals.StatusPending, ""},
	}
	for _, c := range cases {
		e.reg.Register(approvals.Action{Name: c.action, Permission: "staff.roles.grant",
			Exec: func(context.Context, pgx.Tx, json.RawMessage, *auth.Staff) error { return c.exec }})
		id := e.request(a, c.action)
		rec := e.decide("approve", id, b.token, uuid.NewString(), "")
		wantProblem(t, rec, c.status, c.code)
		if strings.Contains(rec.Body.String(), "ivan.p") {
			t.Fatalf("%s: текст ошибки в ответе: %s", c.action, rec.Body.String())
		}
		if st, errText := e.approvalRow(id); st != c.row || errText != c.errText {
			t.Errorf("%s: запрос %s %q, ждали %s %q", c.action, st, errText, c.row, c.errText)
		}
	}
}

// wantAuditMeta — запись журнала action об объекте id с IP (адрес запроса) и User-Agent запроса.
func (e *staffEnv) wantAuditMeta(action string, id uuid.UUID) {
	e.t.Helper()
	var ip, ua *string
	if err := e.owner.QueryRow(context.Background(), `SELECT host(ip), user_agent FROM audit_log
		WHERE action = $1 AND object_id = $2`, action, id).Scan(&ip, &ua); err != nil {
		e.t.Fatalf("журнал %s %s: %v", action, id, err)
	}
	if ip == nil || *ip != clientIP || ua == nil || *ua != userAgent {
		e.t.Fatalf("журнал %s без IP/UA запроса: %v %v", action, ip, ua)
	}
}

// request — запрос подтверждения действия action от сотрудника m (как его создаёт модуль: в своей
// транзакции, payload с версией учётки инициатора — его сверяет проверка инициатора staff).
func (e *staffEnv) request(m member, action string) uuid.UUID {
	e.t.Helper()
	var ver int64
	if err := e.owner.QueryRow(context.Background(), `SELECT version FROM staff_accounts WHERE id = $1`, m.id).Scan(&ver); err != nil {
		e.t.Fatal(err)
	}
	st := &auth.Staff{StaffID: m.id, Role: string(perm.Owner), Stage: auth.StageActive, Grants: perm.Grants(perm.Owner)}
	var out approvals.Approval
	err := db.InTx(context.Background(), e.owner, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = e.approvals.Request(ctx, tx, st, "Сотрудник a-owner", approvals.Meta{}, action,
			map[string]any{"v": 1, "init_id": m.id, "init_ver": ver})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out.ID
}

// Коды staff.* у approve (approveStaffCodes) — ровно staff.* из x-error-codes approveApproval: новый
// бизнес-отказ исполнителя без строки в контракте (или наоборот) — падение. staff.not_found в нём нет.
func TestApproveCodesMatchContract(t *testing.T) {
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	byOp, err := apitest.OperationCodes(spec)
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for op, codes := range byOp {
		if !strings.EqualFold(op, "approveApproval") {
			continue
		}
		for _, c := range codes {
			if strings.HasPrefix(c, "staff.") {
				documented = append(documented, c)
			}
		}
	}
	slices.Sort(documented)
	got := slices.Sorted(slices.Values(admin.ApproveStaffCodes))
	if len(documented) == 0 || !slices.Equal(got, documented) || slices.Contains(got, staff.CodeNotFound) {
		t.Fatalf("approve: в коде %v, в контракте %v", got, documented)
	}
}
