package approvals_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// actLocked — действие модуля не из staff: свои замки акторов не берёт (LockActors), инициатор и
// подтверждающий перепроверяются хуком платформы (R21).
const actLocked = "test.locked"

// R5: Initiator — ровно поля init_id и init_ver (имена — как у payload staff: их читает checkInitiator),
// значения — из принципала.
func TestInitiatorOf(t *testing.T) {
	st := &auth.Staff{StaffID: uuid.MustParse("01a18200-0000-7000-8000-000000000001"), Version: 7, Label: "Анна"}
	in := approvals.InitiatorOf(st)
	if in.ID != st.StaffID || in.Ver != 7 {
		t.Fatalf("Initiator %+v", in)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"init_id":"01a18200-0000-7000-8000-000000000001","init_ver":7}`; string(raw) != want {
		t.Fatalf("JSON %s, ждали %s", raw, want)
	}
	// встраивается в payload модуля плоско, рядом с v
	type modulePayload struct {
		V int `json:"v"`
		approvals.Initiator
	}
	raw, _ = json.Marshal(modulePayload{V: 1, Initiator: in})
	if want := `{"v":1,"init_id":"01a18200-0000-7000-8000-000000000001","init_ver":7}`; string(raw) != want {
		t.Fatalf("payload %s, ждали %s", raw, want)
	}
}

// Действие с LockActors без хука — ошибка сборки (Validate); хук один на реестр, nil — паника.
func TestRegistryValidateLockActors(t *testing.T) {
	r := approvals.NewRegistry()
	r.SetInitiatorCheck(noCheck)
	r.Register(approvals.Action{Name: "staff.invite", Permission: "staff.roles.grant", Exec: noop})
	if err := r.Validate(); err != nil {
		t.Fatalf("без LockActors хук не нужен: %v", err)
	}
	r.Register(approvals.Action{Name: "geo.country_rules", Permission: "geo.countries.rules", Exec: noop, LockActors: true})
	if err := r.Validate(); err == nil {
		t.Fatal("действие с LockActors без SetActorLock прошло Validate")
	}
	r.SetActorLock(noLock)
	if err := r.Validate(); err != nil {
		t.Fatalf("готовый реестр: %v", err)
	}
	mustPanic(t, "второй хук", "уже зарегистрирован", func() { r.SetActorLock(noLock) })
	mustPanic(t, "пустой хук", "пустой", func() { approvals.NewRegistry().SetActorLock(nil) })
}

func noLock(context.Context, pgx.Tx, approvals.Approval, *auth.Staff) error { return nil }

// lockEnv — env с действием actLocked и подменяемым хуком; calls — что видел хук.
type lockEnv struct {
	*env
	mu    sync.Mutex
	hook  approvals.ActorLock
	calls []lockCall
}

type lockCall struct {
	ap       approvals.Approval
	approver auth.Staff
	execsNow int32 // сколько раз исполнитель уже вызван к моменту хука
}

func newLockEnv(t *testing.T, withHook bool) *lockEnv {
	t.Helper()
	e := &lockEnv{env: newEnv(t)}
	e.reg.Register(approvals.Action{Name: actLocked, Permission: "geo.countries.rules", Exec: e.run, Target: target, LockActors: true})
	if withHook {
		e.reg.SetActorLock(func(ctx context.Context, tx pgx.Tx, a approvals.Approval, approver *auth.Staff) error {
			e.mu.Lock()
			e.calls = append(e.calls, lockCall{ap: a, approver: *approver, execsNow: e.execs.Load()})
			h := e.hook
			e.mu.Unlock()
			if h != nil {
				return h(ctx, tx, a, approver)
			}
			return nil
		})
	}
	return e
}

func (e *lockEnv) setHook(h approvals.ActorLock) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hook = h
}

func (e *lockEnv) seen() []lockCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]lockCall(nil), e.calls...)
}

// Хук зовётся у действия с LockActors — после проверки инициатора, до исполнителя, с запросом целиком и
// подтверждающим; у действия без LockActors — нет. Отказ хука Fail(ErrInitiatorChanged) — failed
// (initiator_changed), наружу ErrStale, исполнитель не вызван; Fail(иное) — failed с кодом; сбой — откат всего,
// запрос pending.
func TestApproveCallsActorLock(t *testing.T) {
	e := newLockEnv(t, true)
	a, b := e.member(perm.Owner), e.member(perm.Admin)

	plain := e.request(a, actGrant, payload{V: 1})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, plain.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(e.seen()); n != 0 {
		t.Fatalf("хук у действия без LockActors вызван %d раз", n)
	}

	ok := e.request(a, actLocked, payload{V: 1})
	execsBefore := e.execs.Load()
	res, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ok.ID, "")
	if err != nil || res.Status != approvals.StatusExecuted {
		t.Fatalf("%+v %v", res, err)
	}
	calls := e.seen()
	if len(calls) != 1 || calls[0].ap.ID != ok.ID || calls[0].ap.RequestedBy != a.StaffID ||
		calls[0].ap.Permission != "geo.countries.rules" || calls[0].approver.StaffID != b.StaffID ||
		calls[0].execsNow != execsBefore {
		t.Fatalf("хук: %+v (исполнитель до хука: %d)", calls, execsBefore)
	}

	for _, c := range []struct {
		name      string
		hookErr   error
		wantErr   error
		status    string
		errorText string
	}{
		{"инициатор изменился", approvals.Fail(approvals.ErrInitiatorChanged), approvals.ErrStale, approvals.StatusFailed, approvals.ErrorInitiatorChanged},
		{"бизнес-отказ с кодом", approvals.Fail(approvals.ErrStale), approvals.ErrStale, approvals.StatusFailed, httpx.CodeApprovalStale},
		{"сбой", errors.New("хук: соединение"), nil, approvals.StatusPending, ""},
		{"подтверждающий выключен", auth.ErrSessionInvalid, auth.ErrSessionInvalid, approvals.StatusPending, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e.setHook(func(context.Context, pgx.Tx, approvals.Approval, *auth.Staff) error { return c.hookErr })
			ap := e.request(a, actLocked, payload{V: 1})
			execs, writes := e.execs.Load(), e.moduleWrites()
			_, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, "")
			if err == nil || (c.wantErr != nil && !errors.Is(err, c.wantErr)) {
				t.Fatalf("ошибка %v, ждали %v", err, c.wantErr)
			}
			if r := e.row(ap.ID); r.Status != c.status || r.Error != c.errorText {
				t.Fatalf("строка %+v, ждали %s %q", r, c.status, c.errorText)
			}
			if e.execs.Load() != execs || e.moduleWrites() != writes {
				t.Fatal("исполнитель вызван после отказа хука")
			}
		})
	}
}

// Fail-closed: действие с LockActors в реестре без хука (сборка пропустила Validate) — Approve ничего не
// исполняет, запрос остаётся pending.
func TestApproveLockActorsWithoutHook(t *testing.T) {
	e := newLockEnv(t, false)
	a, b := e.member(perm.Owner), e.member(perm.Admin)
	ap := e.request(a, actLocked, payload{V: 1})
	if _, err := e.svc.Approve(context.Background(), b, "Борис", metaWeb, ap.ID, ""); err == nil {
		t.Fatal("исполнено без хука замка акторов")
	}
	if r := e.row(ap.ID); r.Status != approvals.StatusPending || e.execs.Load() != 0 {
		t.Fatalf("строка %+v, исполнителей %d", r, e.execs.Load())
	}
}

// R21: подпись подтверждающего в исполнителе (approver.Label) — та же строка, что decided_by_label (снимок
// решения, без крайних пробелов), а не Label загрузки сессии: имя сменили между загрузкой и решением. И у хука
// тот же подтверждающий. Принципал запроса не меняется — исполнитель получает копию.
func TestApproverLabelMatchesDecided(t *testing.T) {
	e := newLockEnv(t, true)
	a, b := e.member(perm.Owner), e.member(perm.Admin)
	b.Label = "Борис (старое имя)"
	var got string
	e.setExec(func(_ context.Context, _ pgx.Tx, _ json.RawMessage, approver *auth.Staff) error {
		got = approver.Label
		return nil
	})
	for _, act := range []string{actGrant, actLocked} {
		ap := e.request(a, act, payload{V: 1})
		res, err := e.svc.Approve(context.Background(), b, "  Борис Новый  ", metaWeb, ap.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if r := e.row(ap.ID); got != "Борис Новый" || r.Label != got || res.DecidedByLabel != got {
			t.Fatalf("%s: исполнитель %q, decided_by_label %q, ответ %q", act, got, r.Label, res.DecidedByLabel)
		}
	}
	if calls := e.seen(); len(calls) != 1 || calls[0].approver.Label != "Борис Новый" {
		t.Fatalf("хук: %+v", calls)
	}
	if b.Label != "Борис (старое имя)" {
		t.Fatalf("принципал изменён: %q", b.Label)
	}
}
