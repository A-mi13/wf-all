package approvals_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/perm"
)

func ids(items []approvals.Approval) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(items))
	for _, a := range items {
		out = append(out, a.ID)
	}
	return out
}

func (e *env) list(st *auth.Staff, box, status, cursor string, limit int) ([]approvals.Approval, string) {
	e.t.Helper()
	items, next, err := e.svc.List(context.Background(), st, box, status, cursor, limit)
	if err != nil {
		e.t.Fatalf("List(%s, %s): %v", box, status, err)
	}
	return items, next
}

// all — все страницы списка по курсору.
func (e *env) all(st *auth.Staff, box, status string, limit int) []uuid.UUID {
	e.t.Helper()
	var out []uuid.UUID
	cursor := ""
	for range 100 {
		items, next := e.list(st, box, status, cursor, limit)
		if len(items) > limit {
			e.t.Fatalf("страница %d > лимита %d", len(items), limit)
		}
		if next != "" && len(items) != limit {
			e.t.Fatalf("неполная страница %d при лимите %d и следующей странице", len(items), limit)
		}
		out = append(out, ids(items)...)
		if next == "" {
			return out
		}
		cursor = next
	}
	e.t.Fatal("курсор не кончается")
	return nil
}

// Входящие: не свои, не о себе, право запроса не deny, scoped — только город запроса из своих;
// исходящие (mine) — только свои. Новые сверху.
func TestListBoxes(t *testing.T) {
	e := newEnv(t)
	cityA, cityB := id.New(), id.New()
	owner, owner2, admin := e.member(perm.Owner), e.member(perm.Owner), e.member(perm.Admin)
	mod := e.member(perm.Moderator, cityA)
	support := e.member(perm.Support)
	step := func() { e.clock.Advance(time.Minute) }

	own := e.request(owner, actGrant, payload{V: 1})
	step()
	aboutOwner := e.request(owner2, actGrant, payload{V: 1, Target: &owner.StaffID})
	step()
	elevated := e.request(owner2, actGrant, payload{V: 1, Elevated: true})
	step()
	plain := e.request(owner2, actGrant, payload{V: 1})
	step()
	renameA := e.request(owner2, actRename, payload{V: 1, City: &cityA})
	step()
	renameB := e.request(owner2, actRename, payload{V: 1, City: &cityB})
	step()
	renameNoCity := e.request(owner2, actRename, payload{V: 1})
	step()
	byMod := e.request(mod, actRename, payload{V: 1, City: &cityA})

	cases := []struct {
		name string
		st   *auth.Staff
		want []uuid.UUID
	}{
		{"владелец", owner, []uuid.UUID{byMod.ID, renameNoCity.ID, renameB.ID, renameA.ID, plain.ID, elevated.ID}},
		{"админ", admin, []uuid.UUID{byMod.ID, renameNoCity.ID, renameB.ID, renameA.ID, plain.ID, aboutOwner.ID, own.ID}},
		// модератор: свой город, без города и чужой — нет; свой запрос — нет
		{"модератор", mod, []uuid.UUID{renameA.ID}},
		{"поддержка", support, nil},
	}
	for _, c := range cases {
		if got := ids(must(e.list(c.st, approvals.BoxIncoming, "", "", 50))); !slices.Equal(got, c.want) {
			t.Errorf("входящие %s:\n got %v\nwant %v", c.name, got, c.want)
		}
	}
	if got := ids(must(e.list(owner, approvals.BoxMine, "", "", 50))); !slices.Equal(got, []uuid.UUID{own.ID}) {
		t.Errorf("мои владельца: %v", got)
	}
	if got := ids(must(e.list(mod, approvals.BoxMine, "", "", 50))); !slices.Equal(got, []uuid.UUID{byMod.ID}) {
		t.Errorf("мои модератора: %v", got)
	}
	// фильтр статуса
	if _, err := e.svc.Reject(context.Background(), owner, "Ольга", plain.ID, "нет"); err != nil {
		t.Fatal(err)
	}
	if got := ids(must(e.list(admin, approvals.BoxIncoming, approvals.StatusRejected, "", 50))); !slices.Equal(got, []uuid.UUID{plain.ID}) {
		t.Errorf("rejected: %v", got)
	}
	if got := ids(must(e.list(owner, approvals.BoxIncoming, approvals.StatusPending, "", 50))); slices.Contains(got, plain.ID) || len(got) != 5 {
		t.Errorf("pending: %v", got)
	}
}

func must(items []approvals.Approval, _ string) []approvals.Approval { return items }

// Keyset на одинаковом requested_at: страницы без дублей и пропусков, порядок id DESC; невидимые
// смотрящему запросы между видимыми не «худят» страницу.
func TestListKeysetTies(t *testing.T) {
	e := newEnv(t)
	owner, owner2 := e.member(perm.Owner), e.member(perm.Owner)
	admin := e.member(perm.Admin)
	var want []uuid.UUID
	for i := range 9 {
		// время не идёт: все запросы — в одну микросекунду
		ap := e.request(owner2, actGrant, payload{V: 1, Elevated: i%2 == 0}) // админу не видны чётные
		if i%2 != 0 {
			want = append(want, ap.ID)
		}
		e.request(owner, actGrant, payload{V: 1, Target: &admin.StaffID}) // о админе: не видны
	}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return -slices.Compare(a[:], b[:]) })
	for _, limit := range []int{1, 2, 3, 4} {
		if got := e.all(admin, approvals.BoxIncoming, "", limit); !slices.Equal(got, want) {
			t.Fatalf("лимит %d:\n got %v\nwant %v", limit, got, want)
		}
	}
	// ровно на лимите — без пустой следующей страницы
	if _, next := e.list(admin, approvals.BoxIncoming, "", "", len(want)); next != "" {
		t.Fatal("курсор на пустую страницу")
	}
}

// Негодные box, status, курсор — 400; сессия не active — ErrForbidden; лимит — page.Limit.
func TestListValidation(t *testing.T) {
	e := newEnv(t)
	owner := e.member(perm.Owner)
	_, _, err := e.svc.List(context.Background(), owner, "outbox", "", "", 10)
	assertField(t, err, "query.box", "enum")
	_, _, err = e.svc.List(context.Background(), owner, approvals.BoxMine, "done", "", 10)
	assertField(t, err, "query.status", "enum")
	if _, _, err := e.svc.List(context.Background(), owner, approvals.BoxMine, "", "мусор", 10); !isErr(err, page.ErrBadCursor) {
		t.Fatalf("курсор: %v", err)
	}
	reauth := *owner
	reauth.Reauth = true
	if _, _, err := e.svc.List(context.Background(), &reauth, approvals.BoxMine, "", "", 10); !isErr(err, approvals.ErrForbidden) {
		t.Fatalf("reauth: %v", err)
	}
	if _, _, err := e.svc.List(context.Background(), nil, approvals.BoxMine, "", "", 10); !isErr(err, approvals.ErrForbidden) {
		t.Fatalf("без сотрудника: %v", err)
	}
	for range 25 {
		e.request(owner, actGrant, payload{V: 1})
	}
	if items, next := e.list(owner, approvals.BoxMine, "", "", 0); len(items) != page.DefaultLimit || next == "" {
		t.Fatalf("лимит 0: %d", len(items))
	}
	if items, _ := e.list(owner, approvals.BoxMine, "", "", 1000); len(items) != 25 {
		t.Fatalf("лимит 1000: %d", len(items))
	}
}

// Состояния пакета (фильтр status списка) совпадают с перечислением approval_status из 0020.
func TestStatusesMatchEnum(t *testing.T) {
	e := newEnv(t)
	var got []string
	if err := e.owner.QueryRow(context.Background(), `SELECT enum_range(NULL::approval_status)::text[]`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, approvals.Statuses) {
		t.Fatalf("approval_status %v, пакет %v", got, approvals.Statuses)
	}
}
