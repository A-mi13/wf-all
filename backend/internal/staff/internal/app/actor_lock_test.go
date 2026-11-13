package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff/internal/app"
)

// R5: загрузчик сессии отдаёт версию учётки и подпись (display_name без крайних пробелов) — модуль, не
// читающий staff_*, кладёт их в payload запроса (approvals.InitiatorOf) и в аудит. После смены роли и
// городов следующая загрузка — уже новая версия; во всех стадиях.
func TestLoadStaffCarriesVersionAndLabel(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	c1, c2 := e.city("kazan"), e.city("perm")
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Name: "  Мария Модер  ", Cities: []uuid.UUID{c1, c2}})
	before := e.as(m)
	v := e.acct(m.ID).Version
	if before.Version != v || before.Label != "Мария Модер" {
		t.Fatalf("загрузка: Version %d (в базе %d), Label %q", before.Version, v, before.Label)
	}
	// только снятие города — staff.roles.revoke, сразу: версия +1
	if ap, err := e.app.ChangeRole(context.Background(), a, m.ID, perm.Moderator, []uuid.UUID{c1}, metaWeb); err != nil || ap != nil {
		t.Fatalf("%+v %v", ap, err)
	}
	after := e.as(m)
	if after.Version != v+1 || after.Version != e.acct(m.ID).Version || after.Label != "Мария Модер" {
		t.Fatalf("после смены: Version %d, ждали %d; Label %q", after.Version, v+1, after.Label)
	}
	if before.Version != v {
		t.Fatal("принципал прошлой загрузки изменился")
	}
	for _, stage := range []string{auth.StageMFAPending, auth.StageSetup} {
		tok, _ := e.rawSession(m.ID, stage, ipHome, metaWeb.UserAgent, e.clock.Now())
		st := e.load(tok)
		if st.Version != v+1 || st.Label != "Мария Модер" {
			t.Fatalf("стадия %s: Version %d, Label %q", stage, st.Version, st.Label)
		}
	}
}

// actNoop — действие «модуля не из staff» (как geo.country_rules): payload — v и approvals.Initiator, свои
// замки акторов не берёт (LockActors), право — geo.countries.rules.
const actNoop = "test.noop"

type noopPayload struct {
	V int `json:"v"`
	approvals.Initiator
}

// noopEnv — menv с зарегистрированным test.noop; run — подменяемый исполнитель, execs — сколько раз вызван.
type noopEnv struct {
	*menv
	execs atomic.Int32
	run   func(ctx context.Context) error // nil — успех без записей
}

func newNoopEnv(t *testing.T) *noopEnv {
	t.Helper()
	e := &noopEnv{menv: newManageEnv(t)}
	e.reg.Register(approvals.Action{
		Name: actNoop, Permission: "geo.countries.rules", LockActors: true,
		Exec: func(ctx context.Context, _ pgx.Tx, _ json.RawMessage, _ *auth.Staff) error {
			e.execs.Add(1)
			if e.run != nil {
				return e.run(ctx)
			}
			return nil
		},
	})
	if err := e.reg.Validate(); err != nil {
		t.Fatalf("Validate с test.noop: %v", err)
	}
	return e
}

// request — запрос test.noop от st с инициатором из принципала (InitiatorOf).
func (e *noopEnv) request(st *auth.Staff) approvals.Approval {
	e.t.Helper()
	var ap approvals.Approval
	if err := db.InTx(context.Background(), e.deps.Pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ap, err = e.svc.Request(ctx, tx, st, st.Label, apMeta, actNoop, noopPayload{V: 1, Initiator: approvals.InitiatorOf(st)})
		return err
	}); err != nil {
		e.t.Fatalf("Request: %v", err)
	}
	return ap
}

// wantInitiatorChanged — approve: failed (initiator_changed), наружу ErrStale (409 platform.approval_stale).
func (e *noopEnv) wantInitiatorChanged(res approvals.Approval, err error, apID uuid.UUID) {
	e.t.Helper()
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed {
		e.t.Fatalf("исход %+v %v", res, err)
	}
	if st, errText := e.apState(apID); st != approvals.StatusFailed || errText != approvals.ErrorInitiatorChanged {
		e.t.Fatalf("строка запроса %s %q", st, errText)
	}
}

// R5: запрос модуля не из staff с Initiator текущей версии проходит проверку инициатора staff
// (checkInitiator читает init_id и init_ver из чужого payload); инициатора выключили и вернули (версия выросла,
// снова active с тем же правом) — failed, initiator_changed, 409.
func TestCheckInitiatorForeignPayload(t *testing.T) {
	t.Parallel()
	e := newNoopEnv(t)
	a := e.as(e.addMember("admin.a@wf.example", memberOpts{Role: "admin"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	e.mustApprove(b, e.request(a).ID)
	if e.execs.Load() != 1 {
		t.Fatalf("исполнитель вызван %d раз", e.execs.Load())
	}

	ap := e.request(a)
	e.exec(`UPDATE staff_accounts SET status = 'disabled', version = version + 1 WHERE id = $1`, a.StaffID)
	e.exec(`UPDATE staff_accounts SET status = 'active', version = version + 1 WHERE id = $1`, a.StaffID)
	res, err := e.approve(b, ap.ID)
	e.wantInitiatorChanged(res, err, ap.ID)
	if e.execs.Load() != 1 {
		t.Fatal("исполнитель вызван после смены инициатора")
	}
}

// Review Focus 7, R21 (на test.noop; на настоящем geo.country_rules — Task 13): approve прошёл
// checkInitiator (без замка) и ждёт перед хуком — выключение инициатора коммитится; хук под FOR SHARE видит
// выключенного: 409, исполнитель не вызван. Обратный порядок: хук взял FOR SHARE первым — выключение ждёт
// коммита approve: запрос исполнен, затем учётка выключена. 40P01 нет ни в одном порядке.
func TestCountryRulesInitiatorDisabledConcurrently(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*noopEnv, *auth.Staff, *auth.Staff, *auth.Staff, member) {
		e := newNoopEnv(t)
		c := e.as(e.addMember("owner.c@wf.example", memberOpts{Role: "owner"}))
		// подтверждающий создан раньше инициатора: его id меньше — хук берёт его замок первым
		b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
		am := e.addMember("admin.a@wf.example", memberOpts{Role: "admin"})
		a := e.as(am)
		if bytes.Compare(b.StaffID[:], a.StaffID[:]) >= 0 {
			t.Fatal("порядок id: подтверждающий должен быть меньше инициатора")
		}
		return e, a, b, c, am
	}
	disable := func(e *noopEnv, c *auth.Staff, am member) error {
		_, err := e.app.Disable(context.Background(), c, am.ID, "left_team", "", am.Email, metaWeb)
		return err
	}

	t.Run("выключение раньше хука", func(t *testing.T) {
		e, a, b, c, am := setup(t)
		ap := e.request(a)
		// замок учётки подтверждающего держит третья транзакция: approve проходит checkInitiator (без замков
		// учёток) и встаёт в хуке на первой учётке по id — до инициатора
		release := e.holdAccount(b.StaffID)
		type res struct {
			ap  approvals.Approval
			err error
		}
		out := make(chan res, 1)
		go func() {
			r, err := e.approve(b, ap.ID)
			out <- res{r, err}
		}()
		e.waitLockWaiters(1)
		if err := disable(e, c, am); err != nil { // строку запроса держит approve — гашение её пропускает
			t.Fatalf("выключение: %v", err)
		}
		release()
		r := <-out
		e.wantInitiatorChanged(r.ap, r.err, ap.ID)
		if e.execs.Load() != 0 {
			t.Fatal("исполнитель вызван для выключенного инициатора")
		}
	})

	t.Run("хук раньше выключения", func(t *testing.T) {
		e, a, b, c, am := setup(t)
		ap := e.request(a)
		entered, proceed := make(chan struct{}), make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(proceed) }) }
		t.Cleanup(release) // падение до release не оставляет approve висеть с открытой транзакцией
		e.run = func(context.Context) error {
			close(entered)
			<-proceed
			return nil
		}
		out := make(chan error, 1)
		go func() {
			_, err := e.approve(b, ap.ID)
			out <- err
		}()
		<-entered // хук уже держит FOR SHARE учёток
		disabled := make(chan error, 1)
		go func() { disabled <- disable(e, c, am) }()
		e.waitLockWaiters(1) // выключение ждёт замок учётки инициатора
		release()
		if err := <-out; err != nil {
			t.Fatalf("approve: %v", err)
		}
		if err := <-disabled; err != nil {
			t.Fatalf("выключение: %v", err)
		}
		if st, _ := e.apState(ap.ID); st != approvals.StatusExecuted || e.acct(am.ID).Status != "disabled" {
			t.Fatalf("запрос %s, учётка %s", st, e.acct(am.ID).Status)
		}
	})
}

// Подтверждающий перепроверяется хуком под замком (как execLocks): сессию погасили, учётку выключили или
// сменили роль после подписи — обычная ошибка (401/403), запрос остаётся pending, исполнитель не вызван.
func TestActorLockApproverRechecked(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		sql  string
		want error
	}{
		{"сессия погашена", `UPDATE staff_sessions SET revoked_at = now(), revoked_reason = 'revoked' WHERE staff_id = $1`, auth.ErrSessionInvalid},
		{"сессия заменена", `UPDATE staff_sessions SET revoked_at = now(), revoked_reason = 'replaced' WHERE staff_id = $1`, auth.ErrSessionReplaced},
		{"учётка выключена", `UPDATE staff_accounts SET status = 'disabled', version = version + 1 WHERE id = $1`, auth.ErrSessionInvalid},
		{"роль сменилась", `UPDATE staff_roles SET role = 'admin' WHERE staff_id = $1`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newNoopEnv(t)
			a := e.as(e.addMember("admin.a@wf.example", memberOpts{Role: "admin"}))
			b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
			ap := e.request(a)
			e.exec(c.sql, b.StaffID)
			_, err := e.approve(b, ap.ID)
			switch {
			case c.want != nil && !errors.Is(err, c.want):
				t.Fatalf("ошибка %v, ждали %v", err, c.want)
			case c.want == nil:
				wantForbidden(t, err)
			}
			if st, _ := e.apState(ap.ID); st != approvals.StatusPending || e.execs.Load() != 0 {
				t.Fatalf("запрос %s, исполнитель %d", st, e.execs.Load())
			}
		})
	}
}

// Защита в глубину: init_id в payload не тот, кто запросил (checkInitiator отсёк бы раньше), — хук сам
// отказывает Fail(ErrInitiatorChanged), без инициатора — тоже.
func TestActorLockForeignInitiatorMismatch(t *testing.T) {
	t.Parallel()
	e := newNoopEnv(t)
	a := e.as(e.addMember("admin.a@wf.example", memberOpts{Role: "admin"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	c := e.as(e.addMember("admin.c@wf.example", memberOpts{Role: "admin"}))
	for name, in := range map[string]approvals.Initiator{
		"чужой init_id": approvals.InitiatorOf(c),
		"без init_id":   {},
	} {
		raw, _ := json.Marshal(noopPayload{V: 1, Initiator: in})
		ap := approvals.Approval{ID: uuid.New(), Action: actNoop, Permission: "geo.countries.rules", Payload: raw, RequestedBy: a.StaffID}
		err := db.InTx(context.Background(), e.deps.Pool, func(ctx context.Context, tx pgx.Tx) error {
			return e.app.LockActors(ctx, tx, ap, b)
		})
		if !errors.Is(err, approvals.ErrInitiatorChanged) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// Действия staff.* хук не зовут (LockActors = false): их execLocks берёт замок стража владельцев ДО учёток.
// Наблюдаемо: approve staff.role_change, вставший на замке учётки инициатора, уже держит замок владельцев;
// хук перед исполнителем встал бы на учётке раньше замка владельцев.
func TestStaffActionsDoNotLockActors(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.ChangeRole(ctx, a, x.ID, perm.Support, nil, metaWeb)
	if err != nil || ap == nil {
		t.Fatalf("%+v %v", ap, err)
	}
	release := e.holdAccount(a.StaffID)
	out := make(chan error, 1)
	go func() {
		_, err := e.approve(b, ap.ID)
		out <- err
	}()
	e.waitLockWaiters(1)
	var free bool
	if err := db.InTx(ctx, e.owner, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, app.OwnersLockKey).Scan(&free)
	}); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-out; err != nil {
		t.Fatalf("approve: %v", err)
	}
	if free {
		t.Fatal("approve staff.role_change ждёт учётку, не взяв замок владельцев: хук замка акторов вызван до execLocks")
	}
}

// Ни одно действие staff.* реестра не зовёт хук замка акторов (M3 ревью G3): перебор Registry.Actions(), а не
// список руками — новое действие staff.* попадёт под страж само. Наблюдаемо одинаково для всех действий: учётку
// инициатора держит третья транзакция FOR UPDATE, payload запроса — неизвестной версии формата. Без хука approve
// доходит до исполнителя, тот отказывает на decode (сбой, запрос pending) сразу; хук встал бы на учётке
// инициатора FOR SHARE раньше исполнителя — approve ждал бы до истечения ctx.
func TestStaffRegistryActionsSkipActorLock(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	var names []string
	for _, n := range e.reg.Actions() {
		if strings.HasPrefix(n, "staff.") {
			names = append(names, n)
		}
	}
	if !slices.Contains(names, app.ActionRoleChange) {
		t.Fatalf("действия staff.* реестра: %v — перебор пуст?", names)
	}
	payload := fmt.Sprintf(`{"v": 999, "init_id": %q, "init_ver": %d}`, a.StaffID, a.Version)
	release := e.holdAccount(a.StaffID)
	defer release()
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			apID := id.New()
			now := e.clock.Now()
			e.exec(`INSERT INTO approvals (id, action, permission, payload, requested_by, requested_by_label, requested_at, expires_at)
				VALUES ($1, $2, 'staff.roles.grant_elevated', $3::jsonb, $4, 'Инициатор', $5, $6)`,
				apID, name, payload, a.StaffID, now, now.Add(24*time.Hour))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := e.svc.Approve(ctx, b, "Подтверждающий", apMeta, apID, "")
			if !errors.Is(err, app.ErrPayloadVersion) {
				t.Fatalf("approve %s: %v — ждали отказ исполнителя на decode (app.ErrPayloadVersion) без ожидания учётки "+
					"инициатора; исполнитель нового действия разбирает payload не через decode — поправь страж", name, err)
			}
			if st, _ := e.apState(apID); st != approvals.StatusPending {
				t.Fatalf("статус %s", st)
			}
		})
	}
}
