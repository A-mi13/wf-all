package audit_test

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/page"
	"wf/backend/internal/platform/testkit/dbtest"
)

var t0 = time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)

// row — запись журнала для посева: время и IP задаются явно (audit.Write ставит now()).
type row struct {
	id, staff, user, object, city uuid.UUID
	action, objectType, label     string
	ip                            string // "" — NULL
	at                            time.Time
}

// seed пишет записи владельцем базы (журнал только на вставку — триггер запрещает UPDATE/DELETE,
// но не INSERT с явным created_at).
func seed(t *testing.T, owner *pgxpool.Pool, rs ...row) {
	t.Helper()
	for _, r := range rs {
		if r.id == uuid.Nil {
			r.id = id.New()
		}
		if r.objectType == "" {
			r.objectType = "city"
		}
		role := "system"
		if r.staff != uuid.Nil {
			role = "admin"
			if r.label == "" {
				r.label = "Анна" // CHECK audit_log_staff_actor: у сотрудника снимок имени обязателен
			}
		}
		if r.user != uuid.Nil {
			role = "captain"
		}
		if _, err := owner.Exec(context.Background(), `INSERT INTO audit_log (id, actor_user_id, actor_staff_id,
			actor_role, actor_label, action, object_type, object_id, scope_city_id, before, after, ip, created_at)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, '{"status":"draft"}', '{"status":"active"}',
			NULLIF($10, '')::inet, $11)`,
			r.id, nilUUID(r.user), nilUUID(r.staff), role, r.label, r.action, r.objectType, nilUUID(r.object),
			nilUUID(r.city), r.ip, r.at); err != nil {
			t.Fatal(err)
		}
	}
}

func nilUUID(v uuid.UUID) *uuid.UUID {
	if v == uuid.Nil {
		return nil
	}
	return &v
}

// list — List под ролью admin (права прода: SELECT на audit_log только у admin).
func list(t *testing.T, pools dbtest.Pools, p audit.ListParams) ([]audit.Record, string) {
	t.Helper()
	items, next, err := audit.List(context.Background(), pools.As, p)
	if err != nil {
		t.Fatal(err)
	}
	return items, next
}

func ids(rs []audit.Record) []uuid.UUID {
	out := make([]uuid.UUID, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func sameIDs(got, want []uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Новые сверху; при равном времени — по id убыванию; курсор проходит все записи без пропусков и
// повторов, последняя страница — без next.
func TestListOrderAndCursor(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	// b и c — в одну микросекунду: между ними решает id (больший — раньше)
	a, b, c, d, e := id.New(), id.New(), id.New(), id.New(), id.New()
	if b.String() > c.String() {
		b, c = c, b
	}
	seed(t, pools.Owner,
		row{id: a, action: "geo.city_updated", at: t0},
		row{id: b, action: "geo.city_updated", at: t0.Add(time.Second)},
		row{id: c, action: "geo.city_updated", at: t0.Add(time.Second)},
		row{id: d, action: "geo.city_updated", at: t0.Add(2 * time.Second)},
		row{id: e, action: "geo.city_updated", at: t0.Add(3 * time.Second)},
	)
	want := []uuid.UUID{e, d, c, b, a}
	var got []uuid.UUID
	cursor := ""
	for range 10 {
		items, next := list(t, pools, audit.ListParams{Limit: 2, Cursor: cursor})
		got = append(got, ids(items)...)
		if next == "" {
			break
		}
		cursor = next
	}
	if !sameIDs(got, want) {
		t.Fatalf("порядок %v, нужно %v", got, want)
	}
	// ровно на границе: limit == число записей — next пуст, лишнего запроса не нужно
	if _, next := list(t, pools, audit.ListParams{Limit: 5}); next != "" {
		t.Fatalf("next на полной последней странице: %q", next)
	}
}

func TestListFilters(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	anna, ivan, city, obj := id.New(), id.New(), id.New(), id.New()
	r1, r2, r3, r4 := id.New(), id.New(), id.New(), id.New()
	seed(t, pools.Owner,
		row{id: r1, staff: anna, action: "staff.role_granted", objectType: "staff_account", object: obj, at: t0},
		row{id: r2, staff: ivan, action: "staff.login", objectType: "staff_account", at: t0.Add(time.Minute)},
		row{id: r3, staff: anna, action: "geo.city_updated", object: city, city: city, at: t0.Add(2 * time.Minute)},
		// «staffing.» не должно попасть под префикс «staff.»
		row{id: r4, action: "staffing.x", objectType: "staff_account", at: t0.Add(3 * time.Minute)},
	)
	cases := []struct {
		name string
		p    audit.ListParams
		want []uuid.UUID
	}{
		{"без фильтров", audit.ListParams{}, []uuid.UUID{r4, r3, r2, r1}},
		{"автор", audit.ListParams{ActorStaffID: anna}, []uuid.UUID{r3, r1}},
		{"префикс действия", audit.ListParams{ActionPrefix: "staff."}, []uuid.UUID{r2, r1}},
		{"префикс с символами LIKE", audit.ListParams{ActionPrefix: "staff%"}, nil},
		{"тип объекта", audit.ListParams{ObjectType: "city"}, []uuid.UUID{r3}},
		{"объект", audit.ListParams{ObjectID: obj}, []uuid.UUID{r1}},
		{"с — включительно", audit.ListParams{From: t0.Add(time.Minute)}, []uuid.UUID{r4, r3, r2}},
		{"по — исключительно", audit.ListParams{To: t0.Add(2 * time.Minute)}, []uuid.UUID{r2, r1}},
		{"вместе", audit.ListParams{ActorStaffID: anna, ActionPrefix: "geo.", From: t0, To: t0.Add(time.Hour)}, []uuid.UUID{r3}},
	}
	for _, c := range cases {
		items, _ := list(t, pools, c.p)
		if !sameIDs(ids(items), c.want) {
			t.Errorf("%s: %v, нужно %v", c.name, ids(items), c.want)
		}
	}
}

// Журнал модератора (спека §5.3): записи его городов и его собственные; чужие города, записи без
// города чужих авторов и системные — нет.
func TestListModeratorScope(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	mod, other, moscow, spb := id.New(), id.New(), id.New(), id.New()
	inCity, own, ownElsewhere, foreign, global := id.New(), id.New(), id.New(), id.New(), id.New()
	seed(t, pools.Owner,
		row{id: inCity, staff: other, action: "geo.city_updated", city: moscow, at: t0},
		row{id: own, staff: mod, action: "staff.login", objectType: "staff_account", at: t0.Add(time.Second)},
		row{id: ownElsewhere, staff: mod, action: "geo.city_updated", city: spb, at: t0.Add(2 * time.Second)},
		row{id: foreign, staff: other, action: "geo.city_updated", city: spb, at: t0.Add(3 * time.Second)},
		row{id: global, staff: other, action: "staff.role_granted", objectType: "staff_account", at: t0.Add(4 * time.Second)},
	)
	items, _ := list(t, pools, audit.ListParams{CityIDs: []uuid.UUID{moscow}, OwnStaffID: mod})
	if want := []uuid.UUID{ownElsewhere, own, inCity}; !sameIDs(ids(items), want) {
		t.Fatalf("модератор Москвы: %v, нужно %v", ids(items), want)
	}
	// пустой, но не nil список городов — только свои записи
	items, _ = list(t, pools, audit.ListParams{CityIDs: []uuid.UUID{}, OwnStaffID: mod})
	if want := []uuid.UUID{ownElsewhere, own}; !sameIDs(ids(items), want) {
		t.Fatalf("без городов: %v, нужно %v", ids(items), want)
	}
	// nil — глобальная роль: всё
	if items, _ = list(t, pools, audit.ListParams{OwnStaffID: mod}); len(items) != 5 {
		t.Fatalf("глобальная роль: %d записей", len(items))
	}
}

// IP наружу — только подсеть: /24 у IPv4 (и IPv4 внутри IPv6), /48 у IPv6; NULL — пусто.
func TestListMasksIP(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	v4, v6, mapped, none := id.New(), id.New(), id.New(), id.New()
	seed(t, pools.Owner,
		row{id: v4, action: "staff.login", ip: "203.0.113.7", at: t0},
		row{id: v6, action: "staff.login", ip: "2001:db8:abcd:1234::1", at: t0.Add(time.Second)},
		row{id: mapped, action: "staff.login", ip: "::ffff:198.51.100.200", at: t0.Add(2 * time.Second)},
		row{id: none, action: "staff.login", at: t0.Add(3 * time.Second)},
	)
	items, _ := list(t, pools, audit.ListParams{})
	want := map[uuid.UUID]string{v4: "203.0.113.0/24", v6: "2001:db8:abcd::/48", mapped: "198.51.100.0/24", none: ""}
	for _, r := range items {
		if r.IP != want[r.ID] {
			t.Errorf("%s: IP %q, нужно %q", r.ID, r.IP, want[r.ID])
		}
	}
}

// Поля записи доходят как есть: снимок имени, роль, город, было/стало (уже маскированные при записи).
func TestListRecordFields(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	staff, city, obj := id.New(), id.New(), id.New()
	seed(t, pools.Owner, row{staff: staff, label: "Мария К.", action: "geo.city_updated", object: obj, city: city, at: t0})
	items, _ := list(t, pools, audit.ListParams{})
	if len(items) != 1 {
		t.Fatalf("записей %d", len(items))
	}
	r := items[0]
	if r.ActorStaffID != staff || r.ActorUserID != uuid.Nil || r.ActorLabel != "Мария К." || r.ActorRole != "admin" ||
		r.ObjectID != obj || r.ScopeCityID != city || !r.CreatedAt.Equal(t0) ||
		string(r.Before) != `{"status": "draft"}` || string(r.After) != `{"status": "active"}` {
		t.Fatalf("запись: %+v before=%s after=%s", r, r.Before, r.After)
	}
}

func TestListLimitAndBadCursor(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	if _, err := pools.Owner.Exec(context.Background(), `INSERT INTO audit_log (actor_role, action, object_type, created_at)
		SELECT 'system', 'geo.city_updated', 'city', $1::timestamptz + make_interval(secs => g)
		FROM generate_series(1, 101) g`, t0); err != nil {
		t.Fatal(err)
	}
	if items, next := list(t, pools, audit.ListParams{}); len(items) != page.DefaultLimit || next == "" {
		t.Fatalf("по умолчанию: %d записей, next %q", len(items), next)
	}
	if items, next := list(t, pools, audit.ListParams{Limit: 1000}); len(items) != page.MaxLimit || next == "" {
		t.Fatalf("потолок: %d записей, next %q", len(items), next)
	}
	foreign := page.EncodeKeyset(page.Keyset{Set: "geo.cities(rank,population,id)", Values: []any{int64(1), int64(2), id.New()}},
		page.KindInt64, page.KindInt64, page.KindUUID)
	for name, cur := range map[string]string{"мусор": "%%%", "чужой список": foreign} {
		if _, _, err := audit.List(context.Background(), pools.As, audit.ListParams{Cursor: cur}); !errors.Is(err, page.ErrBadCursor) {
			t.Errorf("%s: %v, нужен page.ErrBadCursor", name, err)
		}
	}
}

func TestMaskIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7": "203.0.113.0/24", "::ffff:203.0.113.7": "203.0.113.0/24",
		"2001:db8:abcd:1234::1": "2001:db8:abcd::/48", "::1": "::/48",
	} {
		if got := audit.MaskIP(netip.MustParseAddr(in)); got != want {
			t.Errorf("%s: %q, нужно %q", in, got, want)
		}
	}
	if got := audit.MaskIP(netip.Addr{}); got != "" {
		t.Errorf("нулевой адрес: %q", got)
	}
}

// Get — одна запись по id (письмо о выключении админа читает её на пуле admin-api): поля как в
// List, IP маскирован; нет записи — ok = false без ошибки.
func TestGet(t *testing.T) {
	pools := dbtest.NewPoolsAs(t, "admin")
	staff, city, obj, v4, v6 := id.New(), id.New(), id.New(), id.New(), id.New()
	seed(t, pools.Owner,
		row{id: v4, staff: staff, label: "Мария К.", action: "staff.account_disabled", objectType: "staff_account",
			object: obj, city: city, ip: "203.0.113.7", at: t0},
		row{id: v6, action: "staff.login", ip: "2001:db8:abcd:1234::1", at: t0.Add(time.Second)},
	)
	listed, _ := list(t, pools, audit.ListParams{})
	byID := map[uuid.UUID]audit.Record{}
	for _, r := range listed {
		byID[r.ID] = r
	}
	for in, ip := range map[uuid.UUID]string{v4: "203.0.113.0/24", v6: "2001:db8:abcd::/48"} {
		got, ok, err := audit.Get(context.Background(), pools.As, in)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", in, ok, err)
		}
		if !reflect.DeepEqual(got, byID[in]) {
			t.Errorf("Get и List расходятся:\n%+v\n%+v", got, byID[in])
		}
		if got.IP != ip {
			t.Errorf("%s: IP %q, нужно %q", in, got.IP, ip)
		}
	}
	if got := byID[v4]; got.ActorStaffID != staff || got.ActorLabel != "Мария К." || got.ScopeCityID != city ||
		got.ObjectID != obj || got.Action != "staff.account_disabled" {
		t.Errorf("поля записи: %+v", got)
	}
	got, ok, err := audit.Get(context.Background(), pools.As, id.New())
	if err != nil || ok || !reflect.DeepEqual(got, audit.Record{}) {
		t.Fatalf("нет записи: %+v ok=%v err=%v", got, ok, err)
	}
}
