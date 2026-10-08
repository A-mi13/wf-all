package audit_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

// entry — действие модератора в городе: сотрудник-автор, снимок имени, город записи.
func entry() audit.Entry {
	return audit.Entry{
		ActorStaffID: id.New(), ActorRole: "moderator", ActorLabel: "Мария К.",
		Action: "geo.city_updated", ObjectType: "city", ObjectID: id.New(), ScopeCityID: id.New(),
		Before: map[string]any{"status": "draft", "email": "ivan@mail.ru"},
		After:  map[string]any{"status": "active"},
		Reason: "fix", IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "test",
	}
}

func rows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_log").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteInTransactionMasksAndPersists(t *testing.T) {
	pool := dbtest.NewPool(t)
	e := entry()
	if err := db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		return audit.Write(ctx, e)
	}); err != nil {
		t.Fatal(err)
	}
	var before, action, ip, label string
	var staff, city uuid.UUID
	var noUser bool
	if err := pool.QueryRow(context.Background(), `SELECT before::text, action, host(ip), actor_staff_id,
		actor_label, scope_city_id, actor_user_id IS NULL FROM audit_log`).Scan(&before, &action, &ip, &staff, &label, &city, &noUser); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "ivan@mail.ru") || !strings.Contains(before, "i***@mail.ru") ||
		action != "geo.city_updated" || ip != "203.0.113.7" {
		t.Fatalf("запись аудита: before=%s action=%s ip=%s", before, action, ip)
	}
	if staff != e.ActorStaffID || label != "Мария К." || city != e.ScopeCityID || !noUser {
		t.Fatalf("автор и город: staff=%s label=%q city=%s без игрока=%v", staff, label, city, noUser)
	}
}

// Каждый вид автора записывается (CHECK audit_log из 0020 их пропускает): игрок, сотрудник, оператор,
// система, аноним.
func TestWriteActorKinds(t *testing.T) {
	pool := dbtest.NewPool(t)
	for name, e := range map[string]audit.Entry{
		"игрок":     {ActorUserID: id.New(), ActorRole: "captain"},
		"сотрудник": {ActorStaffID: id.New(), ActorRole: "owner", ActorLabel: "Анна"},
		"оператор":  {ActorRole: "operator", ActorLabel: "operator"},
		"система":   {ActorRole: "system", ActorLabel: "system"},
		"аноним":    {ActorRole: "anonymous", After: map[string]any{"email": "ivan@mail.ru"}},
	} {
		e.Action, e.ObjectType = "staff.login_failed", "staff_account"
		if err := audit.WriteRead(context.Background(), pool, e); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := rows(t, pool); n != 5 {
		t.Fatalf("строк %d, нужно 5", n)
	}
}

// Entry.ID задан — запись получает его (письмо о действии ссылается на запись журнала заранее);
// пуст — id выдаёт аудит.
func TestWriteKeepsGivenID(t *testing.T) {
	pool := dbtest.NewPool(t)
	given := id.New()
	e := entry()
	e.ID = given
	if err := audit.WriteRead(context.Background(), pool, e); err != nil {
		t.Fatal(err)
	}
	if err := audit.WriteRead(context.Background(), pool, entry()); err != nil {
		t.Fatal(err)
	}
	var withGiven, generated int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE id = $1),
		count(*) FILTER (WHERE id <> $1) FROM audit_log`, given).Scan(&withGiven, &generated); err != nil {
		t.Fatal(err)
	}
	if withGiven != 1 || generated != 1 {
		t.Fatalf("с заданным id %d, с выданным %d", withGiven, generated)
	}
}

func TestWriteNeedsTransactionAndRollsBackWithIt(t *testing.T) {
	pool := dbtest.NewPool(t)
	if err := audit.Write(context.Background(), entry()); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("вне транзакции: %v", err)
	}
	_ = db.InTx(context.Background(), pool, func(ctx context.Context, _ pgx.Tx) error {
		if err := audit.Write(ctx, entry()); err != nil {
			t.Fatal(err)
		}
		return errors.New("действие не удалось")
	})
	if n := rows(t, pool); n != 0 {
		t.Fatalf("аудит пережил откат действия: %d", n)
	}
}

// Аудит чтения чувствительного (почта, связанные аккаунты) — без транзакции действия.
func TestWriteReadWithoutTransaction(t *testing.T) {
	pool := dbtest.NewPool(t)
	e := entry()
	e.Action, e.Before, e.After = "identity.email_viewed", nil, nil
	if err := audit.WriteRead(context.Background(), pool, e); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, pool); n != 1 {
		t.Fatalf("строк %d", n)
	}
}

// Правила автора повторяют CHECK audit_log (0020): нарушение — ErrInvalidEntry до SQL, а не сырой
// 23514 базы.
func TestWriteRejectsInvalidEntry(t *testing.T) {
	pool := dbtest.NewPool(t)
	for name, mutate := range map[string]func(*audit.Entry){
		"действие без модуля": func(e *audit.Entry) { e.Action = "banned" },
		"нет типа объекта":    func(e *audit.Entry) { e.ObjectType = "" },
		"игрок и сотрудник":   func(e *audit.Entry) { e.ActorUserID = id.New() },
		"нет роли":            func(e *audit.Entry) { e.ActorRole = "" },
		"роль не из списка":   func(e *audit.Entry) { e.ActorRole = "root" },
		"сотрудник с ролью игрока": func(e *audit.Entry) {
			e.ActorRole = "captain"
		},
		"сотрудник со служебной ролью": func(e *audit.Entry) { e.ActorRole = "system" },
		"сотрудник без имени":          func(e *audit.Entry) { e.ActorLabel = "" },
		"имя из пробелов":              func(e *audit.Entry) { e.ActorLabel = "   " },
		"имя длиннее 120":              func(e *audit.Entry) { e.ActorLabel = strings.Repeat("я", 121) },
		"игрок со служебной ролью": func(e *audit.Entry) {
			e.ActorStaffID, e.ActorUserID, e.ActorRole = uuid.Nil, id.New(), "operator"
		},
		"без автора с ролью игрока": func(e *audit.Entry) {
			e.ActorStaffID, e.ActorRole, e.ActorLabel = uuid.Nil, "captain", ""
		},
		"без автора с ролью сотрудника": func(e *audit.Entry) {
			e.ActorStaffID, e.ActorRole = uuid.Nil, "admin"
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := entry()
			mutate(&e)
			if err := audit.WriteRead(context.Background(), pool, e); !errors.Is(err, audit.ErrInvalidEntry) {
				t.Fatalf("err = %v", err)
			}
			if n := rows(t, pool); n != 0 {
				t.Fatalf("невалидная запись попала в аудит: %d", n)
			}
		})
	}
}

// Системное действие (воркер, миграция): нулевые значения пишутся как NULL, а не как пустые строки.
// Роль у записи без автора обязательна (0020, CHECK audit_log_service_actor) — system.
func TestWriteSystemEntryStoresNulls(t *testing.T) {
	pool := dbtest.NewPool(t)
	e := audit.Entry{ActorRole: "system", Action: "worker.cleanup_done", ObjectType: "session", ObjectID: uuid.Nil}
	if err := audit.WriteRead(context.Background(), pool, e); err != nil {
		t.Fatal(err)
	}
	var nulls bool
	if err := pool.QueryRow(context.Background(), `SELECT actor_user_id IS NULL AND actor_staff_id IS NULL
		AND actor_role = 'system' AND actor_label IS NULL AND scope_city_id IS NULL
		AND object_id IS NULL AND ip IS NULL AND user_agent IS NULL AND reason IS NULL
		AND before IS NULL AND after IS NULL FROM audit_log`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if !nulls {
		t.Fatal("нулевые значения записаны не как NULL")
	}
}
