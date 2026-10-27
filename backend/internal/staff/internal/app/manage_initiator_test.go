package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff/internal/app"
)

// Предикат проверки инициатора: active, не закрыт, версия та же, право запроса по текущей роли.
func TestInitiatorValid(t *testing.T) {
	who := uuid.New()
	later := t0.Add(time.Hour)
	cases := []struct {
		name, status, role string
		version            int64
		locked             *time.Time
		permission         string
		want               bool
	}{
		{"годен", "active", "admin", 3, nil, "staff.roles.grant", true},
		{"блокировка истекла", "active", "admin", 3, &t0, "staff.roles.grant", true},
		{"выключен", "disabled", "admin", 3, nil, "staff.roles.grant", false},
		{"приглашён", "invited", "admin", 3, nil, "staff.roles.grant", false},
		{"закрыт", "active", "admin", 3, &later, "staff.roles.grant", false},
		{"версия другая", "active", "admin", 4, nil, "staff.roles.grant", false},
		{"нет права по роли", "active", "admin", 3, nil, "staff.roles.grant_elevated", false},
		{"поддержка", "active", "support", 3, nil, "staff.roles.grant", false},
	}
	for _, c := range cases {
		if got := app.InitiatorValid(c.status, c.role, c.version, c.locked, who, 3, c.permission, t0); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// Проверка инициатора по сохранённому запросу (строки вставлены в обход Request: так выглядел бы
// запрос после смены матрицы прав при выкатке или с чужим init_id): право запроса по ТЕКУЩЕЙ роли
// инициатора — Deny, или init_id не совпадает с requested_by — failed, ничего не исполнено.
func TestInitiatorCheckStoredRequest(t *testing.T) {
	e := newManageEnv(t)
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	o := e.addMember("owner.o@wf.example", memberOpts{Role: "owner"})
	insert := func(by, initID uuid.UUID, permission, role string) uuid.UUID {
		apID := id.New()
		p, _ := json.Marshal(map[string]any{"v": 1, "init_id": initID, "init_ver": 1, "email": "n" + apID.String()[:4] + "@wf.example",
			"display_name": "Н", "role": role, "city_ids": []uuid.UUID{}})
		e.exec(`INSERT INTO approvals (id, action, permission, payload, requested_by, requested_by_label, requested_at, expires_at)
			VALUES ($1, 'staff.invite', $2, $3, $4, 'Инициатор', $5, $6)`, apID, permission, p, by, t0, t0.Add(approvals.TTL))
		return apID
	}
	cases := map[string]uuid.UUID{
		"админ и grant_elevated": insert(x.ID, x.ID, "staff.roles.grant_elevated", "admin"),
		"чужой init_id":          insert(x.ID, o.ID, "staff.roles.grant", "support"),
	}
	for name, apID := range cases {
		res, err := e.approve(b, apID)
		if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	// контроль: годный запрос той же формы исполняется
	e.mustApprove(b, insert(x.ID, x.ID, "staff.roles.grant", "support"))
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email LIKE 'n%'`); n != 1 {
		t.Fatalf("учёток %d", n)
	}
}

// Сессию автора погасили (учётка активна) между загрузкой и действием — auth.ErrSessionInvalid.
func TestManageActorSessionRevoked(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	am := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	admin := e.as(am)
	sup := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	if n, err := e.app.RevokeAccountSessions(ctx, owner, am.ID, metaWeb); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := e.app.Disable(ctx, admin, sup.ID, "other", "", sup.Email, metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("%v", err)
	}
	if e.acct(sup.ID).Status != "active" {
		t.Fatal("выключено погашенной сессией")
	}
}

// Уборка №8 m1: сессию автора заменили (вход, unlock или принятие в другой вкладке) между конвейером и
// подписью/замком — auth.ErrSessionReplaced (401 без стирания свежей cookie), как у конвейера и lockSelf;
// погашенная иначе (выход, завершена, блокировка) — по-прежнему ErrSessionInvalid. Действие не исполняется.
func TestManageActorSessionReplaced(t *testing.T) {
	cases := []struct {
		reason string
		want   error
	}{
		{"replaced", auth.ErrSessionReplaced},
		{"logout", auth.ErrSessionInvalid},
		{"revoked", auth.ErrSessionInvalid},
		{"locked", auth.ErrSessionInvalid},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			e := newManageEnv(t)
			ctx := context.Background()
			e.addMember("owner@wf.example", memberOpts{Role: "owner"})
			admin := e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"}))
			sup := e.addMember("sup@wf.example", memberOpts{Role: "support"})
			e.exec(`UPDATE staff_sessions SET revoked_at = $1, revoked_reason = $2 WHERE id = $3`, e.clock.Now(), c.reason, admin.SessionID)
			if _, err := e.app.Label(ctx, admin); !errors.Is(err, c.want) {
				t.Fatalf("Label: %v, ждали %v", err, c.want)
			}
			if _, err := e.app.Disable(ctx, admin, sup.ID, "other", "", sup.Email, metaWeb); !errors.Is(err, c.want) {
				t.Fatalf("Disable: %v, ждали %v", err, c.want)
			}
			if e.acct(sup.ID).Status != "active" {
				t.Fatal("выключено по погашенной сессии")
			}
		})
	}
}

// Встречные действия без замка стража владельцев (владелец I завершает сессии владельца T, T — сессии I)
// стартуют вместе: учётки берутся по возрастанию id — без взаимной блокировки (40P01). Одно проходит,
// второе видит погашенную сессию своего автора. -count=30.
func TestCounterRevokeSessionsNoDeadlock(t *testing.T) {
	e := newManageEnv(t)
	i := e.addMember("owner.i@wf.example", memberOpts{Role: "owner"})
	tm := e.addMember("owner.t@wf.example", memberOpts{Role: "owner"})
	si, st := e.as(i), e.as(tm)
	var ok, gone atomic.Int32
	barrier(2, func(k int) {
		by, target := si, tm.ID
		if k == 1 {
			by, target = st, i.ID
		}
		switch _, err := e.app.RevokeAccountSessions(context.Background(), by, target, metaWeb); {
		case err == nil:
			ok.Add(1)
		case errors.Is(err, auth.ErrSessionInvalid):
			gone.Add(1)
		default:
			t.Errorf("%d: %v", k, err)
		}
	})
	if ok.Load() != 1 || gone.Load() != 1 {
		t.Fatalf("прошло %d, сессия погашена %d", ok.Load(), gone.Load())
	}
}

// Учётка автора выключена или закрыта при живой сессии (состояние в обход сценариев: они гасят сессии
// вместе со сменой статуса) — тоже auth.ErrSessionInvalid: проверка смотрит и учётку, а не только сессию.
func TestManageActorAccountState(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	sup := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	for _, upd := range []string{
		`UPDATE staff_accounts SET status = 'disabled' WHERE id = $1 AND $2::timestamptz IS NOT NULL`,
		`UPDATE staff_accounts SET locked_until = $2 WHERE id = $1`,
	} {
		am := e.addMember("admin"+uuid.NewString()[:6]+"@wf.example", memberOpts{Role: "admin"})
		admin := e.as(am)
		e.exec(upd, am.ID, t0.Add(time.Hour))
		if _, err := e.app.Disable(ctx, admin, sup.ID, "other", "", sup.Email, metaWeb); !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatalf("%s: %v", upd, err)
		}
	}
	if e.acct(sup.ID).Status != "active" {
		t.Fatal("выключено")
	}
}

// hold — отдельная транзакция выполняет sql (замки) и держит их до release.
func (e *menv) hold(sql string, args ...any) (release func()) {
	e.t.Helper()
	tx, err := e.owner.Begin(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = tx.Rollback(context.Background()) }) }
	e.t.Cleanup(release)
	return release
}

// Встречные запросы (m7), детерминированно: замок стража владельцев держит третья транзакция, оба
// подтверждения проходят проверку инициатора и встают в очередь за ним. Проверка инициатора не берёт
// замок учётки — после освобождения исполнители берут учётки по id: одно исполняется, второе — failed,
// без 40P01.
func TestCounterRequestsQueuedOnOwnersLock(t *testing.T) {
	e := newManageEnv(t)
	ctx := context.Background()
	i := e.addMember("owner.i@wf.example", memberOpts{Role: "owner"})
	tm := e.addMember("owner.t@wf.example", memberOpts{Role: "owner"})
	c := e.as(e.addMember("owner.c@wf.example", memberOpts{Role: "owner"}))
	d := e.as(e.addMember("owner.d@wf.example", memberOpts{Role: "owner"}))
	r1, err := e.app.ChangeRole(ctx, e.as(i), tm.ID, perm.Admin, nil, metaWeb)
	if err != nil || r1 == nil {
		t.Fatal(err)
	}
	r2, err := e.app.ChangeRole(ctx, e.as(tm), i.ID, perm.Admin, nil, metaWeb)
	if err != nil || r2 == nil {
		t.Fatal(err)
	}
	release := e.hold(`SELECT pg_advisory_xact_lock($1)`, app.OwnersLockKey)
	type res struct {
		ap  approvals.Approval
		err error
	}
	out := make(chan res, 2)
	for k, st := range []*auth.Staff{c, d} {
		apID := []uuid.UUID{r1.ID, r2.ID}[k]
		go func() {
			ap, err := e.approve(st, apID)
			out <- res{ap, err}
		}()
	}
	e.waitLockWaiters(2)
	release()
	var executed, failed int
	for range 2 {
		r := <-out
		switch {
		case r.err == nil && r.ap.Status == approvals.StatusExecuted:
			executed++
		case errors.Is(r.err, approvals.ErrStale) && r.ap.Status == approvals.StatusFailed:
			failed++
		default:
			t.Errorf("исход %+v %v", r.ap, r.err)
		}
	}
	if executed != 1 || failed != 1 {
		t.Fatalf("исполнено %d, failed %d", executed, failed)
	}
}

// Встречные завершения сессий (без замка стража), детерминированно: третья транзакция держит обе учётки
// FOR SHARE — каждое действие берёт первый замок и ждёт второго. Учётки по возрастанию id — после
// освобождения одно проходит, второе видит погашенную сессию автора; без 40P01.
func TestCounterRevokeSessionsQueued(t *testing.T) {
	e := newManageEnv(t)
	i := e.addMember("owner.i@wf.example", memberOpts{Role: "owner"})
	tm := e.addMember("owner.t@wf.example", memberOpts{Role: "owner"})
	si, st := e.as(i), e.as(tm)
	release := e.hold(`SELECT 1 FROM staff_accounts WHERE id IN ($1, $2) FOR SHARE`, i.ID, tm.ID)
	out := make(chan error, 2)
	go func() { _, err := e.app.RevokeAccountSessions(context.Background(), si, tm.ID, metaWeb); out <- err }()
	go func() { _, err := e.app.RevokeAccountSessions(context.Background(), st, i.ID, metaWeb); out <- err }()
	e.waitLockWaiters(2)
	release()
	var ok, gone int
	for range 2 {
		switch err := <-out; {
		case err == nil:
			ok++
		case errors.Is(err, auth.ErrSessionInvalid):
			gone++
		default:
			t.Errorf("%v", err)
		}
	}
	if ok != 1 || gone != 1 {
		t.Fatalf("прошло %d, погашено %d", ok, gone)
	}
}
