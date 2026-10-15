package approvals_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/perm"
)

// Запрос: право по Permit, срок now+TTL, снимок имени, цель и город; аудит без payload.
func TestRequestCreatesPending(t *testing.T) {
	e := newEnv(t)
	owner := e.member(perm.Owner)
	tg := id.New()
	ap := e.request(owner, actGrant, payload{V: 1, Target: &tg, Elevated: true, Note: "секрет-заметка ivan@mail.ru"})
	if ap.Status != approvals.StatusPending || ap.Permission != "staff.roles.grant_elevated" || ap.Action != actGrant ||
		ap.TargetID != tg || ap.RequestedBy != owner.StaffID || ap.RequestedByLabel != "Инициатор owner" ||
		!ap.RequestedAt.Equal(t0) || !ap.ExpiresAt.Equal(t0.Add(approvals.TTL)) || ap.DecidedAt != nil {
		t.Fatalf("запрос: %+v", ap)
	}
	if r := e.row(ap.ID); r.Status != "pending" {
		t.Fatalf("строка: %+v", r)
	}
	var after, label string
	if err := e.owner.QueryRow(context.Background(), `SELECT coalesce(after::text, ''), actor_label FROM audit_log
		WHERE action = 'platform.approval_requested' AND object_id = $1 AND object_type = 'approval'
		  AND actor_staff_id = $2 AND actor_role = 'owner'`, ap.ID, owner.StaffID).Scan(&after, &label); err != nil {
		t.Fatalf("аудит запроса: %v", err)
	}
	if strings.Contains(after, "секрет") || strings.Contains(after, "ivan") || label != "Инициатор owner" {
		t.Fatalf("аудит запроса: after=%s label=%q", after, label)
	}
	// обычная роль — обычное право
	admin := e.member(perm.Admin)
	if ap := e.request(admin, actGrant, payload{V: 1}); ap.Permission != "staff.roles.grant" || ap.TargetID != [16]byte{} {
		t.Fatalf("право обычной выдачи: %+v", ap)
	}
}

// Без права запроса (deny), повышенное право не по роли, scoped вне своих городов, сессия не
// active — ErrForbidden, строки нет.
func TestRequestForbidden(t *testing.T) {
	e := newEnv(t)
	cityA, cityB := id.New(), id.New()
	mod := e.member(perm.Moderator, cityA)
	admin := e.member(perm.Admin)
	reauth := e.member(perm.Owner)
	reauth.Reauth = true
	setup := e.member(perm.Owner)
	setup.Stage = "setup"
	cases := map[string]func() error{
		"модератор выдаёт роль":  func() error { _, err := e.tryRequest(mod, actGrant, payload{V: 1}); return err },
		"админ выдаёт владельца": func() error { _, err := e.tryRequest(admin, actGrant, payload{V: 1, Elevated: true}); return err },
		"scoped чужой город":     func() error { _, err := e.tryRequest(mod, actRename, payload{V: 1, City: &cityB}); return err },
		"scoped без города":      func() error { _, err := e.tryRequest(mod, actRename, payload{V: 1}); return err },
		"под reauth":             func() error { _, err := e.tryRequest(reauth, actGrant, payload{V: 1}); return err },
		"стадия setup":           func() error { _, err := e.tryRequest(setup, actGrant, payload{V: 1}); return err },
	}
	for name, f := range cases {
		if err := f(); !isErr(err, approvals.ErrForbidden) {
			t.Errorf("%s: %v, ждали ErrForbidden", name, err)
		}
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("строк %d", n)
	}
	// свой город — можно
	ap := e.request(mod, actRename, payload{V: 1, City: &cityA})
	if ap.ScopeCityID != cityA || ap.Permission != "geo.cities.rename" {
		t.Fatalf("scoped свой город: %+v", ap)
	}
	// город записи аудита — город запроса (журнал модератора)
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_requested' AND object_id = $1
		AND scope_city_id = $2`, ap.ID, cityA); n != 1 {
		t.Fatalf("город аудита запроса: %d", n)
	}
}

// payload — JSON-объект с целым v ≥ 1; иначе ошибка до SQL (а не 500 от CHECK); неизвестное
// действие и право Permit не из каталога — ошибка; пустая метка — ошибка.
func TestRequestPayload(t *testing.T) {
	e := newEnv(t)
	owner := e.member(perm.Owner)
	e.reg.Register(approvals.Action{Name: "test.badperm", Permission: "staff.roles.grant", Exec: e.run,
		Permit: func(json.RawMessage) (string, error) { return "staff.roles.nope", nil }})
	e.reg.Register(approvals.Action{Name: "test.draftperm", Permission: "staff.roles.grant", Exec: e.run,
		Permit: func(json.RawMessage) (string, error) { return "teams.captains.approve", nil }})
	bad := map[string]struct {
		action string
		p      any
	}{
		"без v":                {actGrant, map[string]any{"x": 1}},
		"v = 0":                {actGrant, map[string]any{"v": 0}},
		"v строкой":            {actGrant, map[string]any{"v": "1"}},
		"v дробное":            {actGrant, map[string]any{"v": 1.5}},
		"массив":               {actGrant, []int{1}},
		"строка":               {actGrant, "v"},
		"nil":                  {actGrant, nil},
		"сырой не JSON":        {actGrant, json.RawMessage(`{"v":1`)},
		"не сериализуется":     {actGrant, map[string]any{"v": 1, "f": func() {}}},
		"нет действия":         {"test.none", payload{V: 1}},
		"право не из каталога": {"test.badperm", payload{V: 1}},
		"черновое право":       {"test.draftperm", payload{V: 1}},
	}
	for name, c := range bad {
		if _, err := e.tryRequest(owner, c.action, c.p); err == nil || isErr(err, approvals.ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	err := db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := e.svc.Request(ctx, tx, owner, "   ", metaWeb, actGrant, payload{V: 1})
		return err
	})
	if err == nil {
		t.Error("пустая метка инициатора прошла")
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("строк %d", n)
	}
	// годные формы: структура, map, сырой JSON
	e.request(owner, actGrant, payload{V: 2})
	if _, err := e.tryRequest(owner, actGrant, json.RawMessage(`{"v": 1, "elevated": true}`)); err != nil {
		t.Fatal(err)
	}
}

// Запрос живёт в транзакции модуля: виден после её коммита, откат модуля откатывает и запрос.
func TestRequestInModuleTx(t *testing.T) {
	e := newEnv(t)
	owner := e.member(perm.Owner)
	err := db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.svc.Request(ctx, tx, owner, "Ольга", metaWeb, actGrant, payload{V: 1}); err != nil {
			return err
		}
		if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
			t.Errorf("запрос виден до коммита: %d", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 1 {
		t.Fatalf("после коммита %d", n)
	}
	errRollback := errFake("откат модуля")
	err = db.InTx(context.Background(), e.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.svc.Request(ctx, tx, owner, "Ольга", metaWeb, actGrant, payload{V: 1}); err != nil {
			return err
		}
		return errRollback
	})
	if !isErr(err, errRollback) {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 1 {
		t.Fatalf("после отката модуля %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'platform.approval_requested'`); n != 1 {
		t.Fatalf("аудит запросов %d", n)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
