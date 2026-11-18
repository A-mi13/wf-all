package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
)

// Заявки в капитаны через весь конвейер admin-api (план I-4, Task 4): настоящие staff (сессии, связь сотрудника с
// игроком — staff.LinkedPlayerTx), geo и identity на пуле роли admin; порты условий — «выполнено» (identityDeps).

func captainPath(app uuid.UUID, tail string) string {
	return "/v1/identity/captain-applications/" + app.String() + tail
}

// captainApplicant — игрок в городе city с открытой заявкой (телефон указан); id игрока и заявки.
func (e *staffEnv) captainApplicant(nick string, city uuid.UUID) (uid, app uuid.UUID) {
	e.t.Helper()
	uid = e.player(nick)
	e.exec(`UPDATE users SET city_id = $2 WHERE id = $1`, uid, city)
	app = uuid.New()
	e.exec(`INSERT INTO captain_applications (id, user_id, city_id, proposed_format, phone, status, created_at, updated_at)
		VALUES ($1, $2, $3, '6×6 по средам', '+79990001122', 'pending', $4, $4)`, app, uid, city,
		e.clock.Now().Add(-time.Hour))
	return uid, app
}

// waitLock — ждёт, пока хоть один запрос базы теста встанет на замок (pg_stat_activity), не дольше 5 с.
func (e *staffEnv) waitLock() bool {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCaptainApplicationsHTTP — пять операций по контракту: очередь и карточка (200, Cache-Control private,
// no-store), контакты с причиной (200; короткая причина — 422, битая кодировка — 400; запись журнала), одобрение (200,
// повтор ключа — тот же ответ), отказ other без комментария — 422 body.comment, с кодом — 200; модератор чужого
// города — 404; поддержка решает — 403 конвейера.
func TestCaptainApplicationsHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	own, other := e.city("cap-own"), e.city("cap-other")
	owner := e.owners(1)[0]
	mod := e.firstLoginModerator("mod@wf.example", own)
	sup := e.firstLogin("sup@wf.example", string(perm.Support))
	uid, app := e.captainApplicant("cap_http", own)
	_, foreign := e.captainApplicant("cap_foreign", other)

	rec := e.send(http.MethodGet, "/v1/identity/captain-applications?status=pending&limit=20", mod.token, "", "")
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(rec.Body.String(), app.String()) ||
		strings.Contains(rec.Body.String(), foreign.String()) || strings.Contains(rec.Body.String(), "79990001122") {
		t.Fatalf("очередь модератора: %s", rec.Body.String())
	}
	rec = e.send(http.MethodGet, captainPath(app, ""), mod.token, "", "")
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(rec.Body.String(), `"checks":[{"check":"age","state":"ok"}`) {
		t.Fatalf("карточка: %s", rec.Body.String())
	}
	wantProblem(t, e.send(http.MethodGet, captainPath(foreign, ""), mod.token, "", ""), http.StatusNotFound,
		identity.CodeApplicationNotFound)

	rec = e.send(http.MethodGet, captainPath(app, "/contact"), sup.token, "", "", "X-WF-Access-Reason", accessReason)
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"email":"cap_http@example.com"`) || !strings.Contains(rec.Body.String(), `"phone":"+79990001122"`) ||
		rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("контакты: %s", rec.Body.String())
	}
	for _, c := range []struct {
		reason string
		status int
	}{{"ab", http.StatusUnprocessableEntity}, {"%D0%", http.StatusBadRequest}, {"abc%00", http.StatusUnprocessableEntity}} {
		rec := e.send(http.MethodGet, captainPath(app, "/contact"), sup.token, "", "", "X-WF-Access-Reason", c.reason)
		wantProblem(t, rec, c.status, httpx.CodeValidationFailed)
		if !strings.Contains(rec.Body.String(), `"field":"header.X-WF-Access-Reason"`) {
			t.Fatalf("%q: %s", c.reason, rec.Body.String())
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'identity.pii_read' AND object_id = $1`, uid); n != 1 {
		t.Fatalf("записей чтения контактов %d", n)
	}

	wantProblem(t, e.send(http.MethodPost, captainPath(app, "/approve"), sup.token, uuid.NewString(), `{}`),
		http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.send(http.MethodPost, captainPath(foreign, "/approve"), mod.token, uuid.NewString(), `{}`),
		http.StatusNotFound, identity.CodeApplicationNotFound)
	key := uuid.NewString()
	rec = e.send(http.MethodPost, captainPath(app, "/approve"), mod.token, key, `{"comment":"Знаю по турниру"}`)
	wantStatus(t, rec, http.StatusOK)
	var got struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Status != "approved" {
		t.Fatalf("одобрение: %v %s", err, rec.Body.String())
	}
	again := e.send(http.MethodPost, captainPath(app, "/approve"), mod.token, key, `{"comment":"Знаю по турниру"}`)
	var first, replay map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if err := json.Unmarshal(again.Body.Bytes(), &replay); err != nil || again.Code != http.StatusOK || !reflect.DeepEqual(first, replay) {
		t.Fatalf("повтор ключа: %d %s", again.Code, again.Body.String())
	}
	wantProblem(t, e.send(http.MethodPost, captainPath(app, "/approve"), mod.token, uuid.NewString(), `{}`),
		http.StatusConflict, identity.CodeApplicationClosed)

	_, second := e.captainApplicant("cap_http2", own)
	rec = e.send(http.MethodPost, captainPath(second, "/reject"), owner.token, uuid.NewString(), `{"rejection_code":"other"}`)
	wantProblem(t, rec, http.StatusUnprocessableEntity, httpx.CodeValidationFailed)
	if !strings.Contains(rec.Body.String(), `"field":"body.comment"`) || !strings.Contains(rec.Body.String(), `"code":"required"`) {
		t.Fatalf("other без комментария: %s", rec.Body.String())
	}
	rec = e.send(http.MethodPost, captainPath(second, "/reject"), owner.token, uuid.NewString(),
		`{"rejection_code":"insufficient_info","comment":"Мало сведений"}`)
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"status":"rejected"`) || !strings.Contains(rec.Body.String(), `"rejection_code":"insufficient_info"`) {
		t.Fatalf("отказ: %s", rec.Body.String())
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE args->>'mail' IN ('identity.captain_application_approved',
		'identity.captain_application_rejected')`); n != 2 {
		t.Fatalf("писем %d", n)
	}
}

// TestPutPlayerLinkVsApprove — связь сотрудника с игроком (настоящий PutPlayerLink, Task 6) и одобрение заявки этого
// игрока тем же сотрудником (R38): решение перечитывает связь FOR SHARE учётки, PUT держит её FOR UPDATE — ровно один
// исход без 40P01 и 500. Связь первой (PUT стоит на шве после замка учётки) — одобрение ждёт и получает 409
// self_action; одобрение первым (шов после перечтения связи) — PUT ждёт коммита одобрения, оба 200.
func TestPutPlayerLinkVsApprove(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"связь", "одобрение"} {
		t.Run(first, func(t *testing.T) {
			t.Parallel()
			e := newIdentityStaffEnv(t)
			city := e.city("race-city")
			o := e.owners(2)
			a, b := o[0], o[1]
			uid, app := e.captainApplicant("race_p", city)

			seam := "-- name: PlayerLinkPut "
			if first == "одобрение" {
				seam = "-- name: AdminGrantCaptainPermit "
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var fired atomic.Bool
			hook := func(sql string) {
				if strings.Contains(sql, seam) && fired.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}
			e.commits.onQuery.Store(&hook)
			defer e.commits.onQuery.Store(nil)

			var wg sync.WaitGroup
			var putCode, apCode int
			var apBody string
			put := func() {
				putCode = e.send(http.MethodPut, playerLinkPath(b.id), a.token, uuid.NewString(), playerLinkBody(uid)).Code
			}
			approve := func() {
				rec := e.send(http.MethodPost, captainPath(app, "/approve"), b.token, uuid.NewString(), `{}`)
				apCode, apBody = rec.Code, rec.Body.String()
			}
			firstFn, secondFn := put, approve
			if first == "одобрение" {
				firstFn, secondFn = approve, put
			}
			wg.Go(firstFn)
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				wg.Wait()
				t.Fatalf("шов %q не сработал: PUT %d, одобрение %d %s", seam, putCode, apCode, apBody)
			}
			wg.Go(secondFn)
			waited := e.waitLock()
			close(release)
			wg.Wait()
			if !waited {
				t.Fatalf("второй не ждал замка учётки: PUT %d, одобрение %d %s", putCode, apCode, apBody)
			}
			if putCode != http.StatusOK {
				t.Fatalf("PUT: %d", putCode)
			}
			links := e.count(`SELECT count(*) FROM staff_player_links WHERE staff_id = $1 AND user_id = $2`, b.id, uid)
			permits := e.count(`SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid)
			switch first {
			case "связь":
				if apCode != http.StatusConflict || !strings.Contains(apBody, `"code":"`+identity.CodeSelfAction+`"`) ||
					permits != 0 || links != 1 {
					t.Fatalf("связь первой: одобрение %d %s, допусков %d, связей %d", apCode, apBody, permits, links)
				}
			default:
				if apCode != http.StatusOK || permits != 1 || links != 1 {
					t.Fatalf("одобрение первым: %d %s, допусков %d, связей %d", apCode, apBody, permits, links)
				}
			}
		})
	}
}

// TestPutPlayerLinkVsMfaResetApprove — та же гонка для подтверждения сброса 2FA (исполнитель identity.mfa_reset
// перечитывает связь подтверждающего портом StaffLinks): владелец A связывает подтверждающего B с игроком, пока B
// подтверждает сброс 2FA этого игрока. Связь первой — подтверждение ждёт замка учётки B (хук LockActors), исполнитель
// видит связь: 409, сброса нет, запрос failed; подтверждение первым — PUT ждёт его коммита, оба 200.
func TestPutPlayerLinkVsMfaResetApprove(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"связь", "подтверждение"} {
		t.Run(first, func(t *testing.T) {
			t.Parallel()
			e := newIdentityStaffEnv(t)
			o := e.owners(3)
			a, b, c := o[0], o[1], o[2]
			uid := e.player("race_reset")
			ap := e.accepted(e.send(http.MethodPost, mfaResetPath(uid), c.token, uuid.NewString(), mfaResetBody))

			seam := "-- name: PlayerLinkPut "
			if first == "подтверждение" {
				seam = "-- name: MfaResetInsert "
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var fired atomic.Bool
			hook := func(sql string) {
				if strings.Contains(sql, seam) && fired.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}
			e.commits.onQuery.Store(&hook)
			defer e.commits.onQuery.Store(nil)

			var wg sync.WaitGroup
			var putCode, apCode int
			var apBody string
			put := func() {
				putCode = e.send(http.MethodPut, playerLinkPath(b.id), a.token, uuid.NewString(), playerLinkBody(uid)).Code
			}
			approve := func() {
				rec := e.decide("approve", ap, b.token, "", "")
				apCode, apBody = rec.Code, rec.Body.String()
			}
			firstFn, secondFn := put, approve
			if first == "подтверждение" {
				firstFn, secondFn = approve, put
			}
			wg.Go(firstFn)
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				wg.Wait()
				t.Fatalf("шов %q не сработал: PUT %d, подтверждение %d %s", seam, putCode, apCode, apBody)
			}
			wg.Go(secondFn)
			waited := e.waitLock()
			close(release)
			wg.Wait()
			if !waited {
				t.Fatalf("второй не ждал замка учётки: PUT %d, подтверждение %d %s", putCode, apCode, apBody)
			}
			if putCode != http.StatusOK {
				t.Fatalf("PUT: %d", putCode)
			}
			resets := e.count(`SELECT count(*) FROM mfa_resets WHERE user_id = $1`, uid)
			status, _ := e.approvalRow(ap)
			switch first {
			case "связь":
				if apCode != http.StatusConflict || resets != 0 || status == "executed" {
					t.Fatalf("связь первой: подтверждение %d %s, сбросов %d, запрос %s", apCode, apBody, resets, status)
				}
			default:
				if apCode != http.StatusOK || resets != 1 || status != "executed" {
					t.Fatalf("подтверждение первым: %d %s, сбросов %d, запрос %s", apCode, apBody, resets, status)
				}
			}
		})
	}
}

// TestCaptainDecisionStaffChangedWhileWaiting — m1 ревью Task 4: сотрудника изменили (снят город, понижена роль,
// учётка выключена — версия учётки + 1), пока его решение по заявке ждало замок учётки (порт StaffLinks, FOR SHARE).
// Права снимка загрузки сессии устарели: 403 auth.forbidden, допуска нет, заявка pending. Держатель — как сценарии
// staff: учётка FOR UPDATE, правка, версия + 1, коммит после того, как решение встало на замок.
func TestCaptainDecisionStaffChangedWhileWaiting(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, sql string }{
		{"город снят", `DELETE FROM staff_role_cities WHERE staff_id = $1 AND city_id = $2`},
		{"роль понижена", `WITH d AS (DELETE FROM staff_role_cities WHERE staff_id = $1)
			UPDATE staff_roles SET role = 'support' WHERE staff_id = $1 AND $2::uuid IS NOT NULL`},
		{"учётка выключена", `UPDATE staff_accounts SET status = 'disabled' WHERE id = $1 AND $2::uuid IS NOT NULL`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newIdentityStaffEnv(t)
			own, spare := e.city("stale-own"), e.city("stale-spare")
			mod := e.firstLoginModerator("stale.mod@wf.example", own, spare)
			uid, app := e.captainApplicant("stale_p", own)

			ctx := context.Background()
			hold, err := e.owner.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = hold.Rollback(ctx) }()
			for _, q := range []string{`SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, c.sql,
				`UPDATE staff_accounts SET version = version + 1 WHERE id = $1`} {
				args := []any{mod.id}
				if q == c.sql {
					args = append(args, own)
				}
				if _, err := hold.Exec(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			var rec *httpRec
			done := make(chan struct{})
			go func() {
				defer close(done)
				rec = e.send(http.MethodPost, captainPath(app, "/approve"), mod.token, uuid.NewString(), `{}`)
			}()
			waited := e.waitLock()
			if err := hold.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			<-done
			if !waited {
				t.Fatalf("решение не ждало замка учётки: %d %s", rec.Code, rec.Body.String())
			}
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"`+httpx.CodeForbidden+`"`) {
				t.Fatalf("решение устаревшего сотрудника: %d %s", rec.Code, rec.Body.String())
			}
			if n := e.count(`SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 0 {
				t.Fatalf("допусков %d", n)
			}
			if n := e.count(`SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'pending'`, app); n != 1 {
				t.Fatal("заявка не pending")
			}
		})
	}
}

// TestCaptainIneligibleReasonsHTTP — m4 ревью Task 4: 409 identity.captain_ineligible через весь конвейер несёт reasons
// по контракту (ответ сверяет apitest): заявитель младше порога — ["too_young"], допуска нет, заявка pending.
func TestCaptainIneligibleReasonsHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec)
	city := e.city("inel-city")
	owner := e.owners(1)[0]
	uid, app := e.captainApplicant("inel_p", city)
	e.exec(`UPDATE users SET birth_date = '2016-01-01' WHERE id = $1`, uid)

	rec := e.send(http.MethodPost, captainPath(app, "/approve"), owner.token, uuid.NewString(), `{}`)
	wantStatus(t, rec, http.StatusConflict)
	var body struct {
		Code    string   `json:"code"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != identity.CodeCaptainIneligible || !reflect.DeepEqual(body.Reasons, []string{"too_young"}) {
		t.Fatalf("тело 409: %s", rec.Body.String())
	}
	if n := e.count(`SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("допусков %d", n)
	}
	if n := e.count(`SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'pending'`, app); n != 1 {
		t.Fatal("заявка не pending")
	}
}
