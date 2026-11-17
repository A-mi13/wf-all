package intx_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity/intx"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var testNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// Без зависимости — отказ сборки.
func TestNewRequiresDeps(t *testing.T) {
	t.Parallel()
	if _, err := intx.New(intx.Deps{}); err == nil {
		t.Fatal("New без зависимостей")
	}
}

func exec(t *testing.T, p *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, p *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Три операции через пакет intx под ролью api (R13): допуск отозван, бан и его снятие, смена города; ошибки — значения
// пакета (errors.Is).
func TestIntxUnderAPIRole(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "api")
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	x, err := intx.New(intx.Deps{Pool: pools.As, Clock: clocktest.New(testNow), Geo: geo.New(pools.As), River: ins, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var uid, home, away, app uuid.UUID
	if err := pools.Owner.QueryRow(ctx, `SELECT id FROM cities WHERE slug = 'stavropol'`).Scan(&home); err != nil {
		t.Fatal(err)
	}
	if err := pools.Owner.QueryRow(ctx, `SELECT id FROM cities WHERE slug = 'mihaylovsk'`).Scan(&away); err != nil {
		t.Fatal(err)
	}
	exec(t, pools.Owner, `UPDATE countries SET is_enabled = true`)
	if err := pools.Owner.QueryRow(ctx, `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale, city_id)
		VALUES ('intx@example.com', now(), 'intx_user', '2000-01-01', 'ru', $1) RETURNING id`, home).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := pools.Owner.QueryRow(ctx, `INSERT INTO captain_applications (user_id, city_id, status, reviewed_at,
		reviewed_by_label, closed_at) VALUES ($1, $2, 'approved', $3, 'Модератор', $3) RETURNING id`, uid, home, testNow).
		Scan(&app); err != nil {
		t.Fatal(err)
	}
	exec(t, pools.Owner, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, application_id)
		VALUES ($1, 'captain', 'city', $2, $3)`, uid, home, app)
	in := func(fn func(ctx context.Context, tx pgx.Tx) error) error { return db.InTx(ctx, pools.As, fn) }

	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		return x.RevokeRoleInTx(ctx, tx, uid, home, intx.RevokeBy{System: true}, "команда распущена")
	}); err != nil {
		t.Fatal(err)
	}
	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		return x.RevokeRoleInTx(ctx, tx, uid, home, intx.RevokeBy{System: true}, "")
	}); !errors.Is(err, intx.ErrPermitNotFound) {
		t.Fatalf("повтор отзыва: %v", err)
	}

	src := uuid.New()
	var res intx.RestrictionsResult
	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = x.SetRestrictionsInTx(ctx, tx, uid, intx.RestrictionsChange{SourceID: src, Status: intx.StatusBanned,
			Add: []intx.Restriction{{Capability: "login"}}})
		return err
	}); err != nil || res.Status != intx.StatusBanned {
		t.Fatalf("бан: %+v %v", res, err)
	}
	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = x.SetRestrictionsInTx(ctx, tx, uid, intx.RestrictionsChange{SourceID: src, LiftSource: true})
		return err
	}); err != nil || res.Status != intx.StatusActive {
		t.Fatalf("снятие бана: %+v %v", res, err)
	}
	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		_, err := x.SetRestrictionsInTx(ctx, tx, uid, intx.RestrictionsChange{Status: intx.StatusBanned})
		return err
	}); !errors.Is(err, intx.ErrBanNeedsLogin) {
		t.Fatalf("бан без login: %v", err)
	}

	if err := in(func(ctx context.Context, tx pgx.Tx) error { return x.ChangeCityInTx(ctx, tx, uid, away) }); err != nil {
		t.Fatal(err)
	}
	if count(t, pools.Owner, `SELECT count(*) FROM users WHERE id = $1 AND city_id = $2 AND status = 'active'`, uid, away) != 1 {
		t.Fatal("итог")
	}
	if err := in(func(ctx context.Context, tx pgx.Tx) error { return x.ChangeCityInTx(ctx, tx, uid, uuid.New()) }); !errors.Is(err, intx.ErrCityNotFound) {
		t.Fatalf("нет города: %v", err)
	}
	if err := in(func(ctx context.Context, tx pgx.Tx) error { return x.ChangeCityInTx(ctx, tx, uuid.New(), away) }); !errors.Is(err, intx.ErrUserNotFound) {
		t.Fatalf("нет аккаунта: %v", err)
	}

	// I2 ревью T7: SAVEPOINT вызывающего (как у исполнителя подтверждений) — законно; событие уходит через ctx на то же
	// соединение и откатывается вместе с точкой сохранения
	events := func() int {
		return count(t, pools.Owner, `SELECT count(*) FROM outbox WHERE event_type = 'identity.city_changed'`)
	}
	before := events()
	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if err := x.ChangeCityInTx(ctx, sp, uid, home); err != nil {
			t.Errorf("под SAVEPOINT: %v", err)
		}
		return sp.Rollback(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if events() != before {
		t.Fatal("событие пережило откат SAVEPOINT")
	}

	// m7: tx другого db.InTx (другое соединение) или ctx без транзакции — отказ до записей
	if err := in(func(ctx context.Context, tx pgx.Tx) error {
		if err := db.InTx(context.Background(), pools.As, func(_ context.Context, other pgx.Tx) error {
			if _, err := x.SetRestrictionsInTx(ctx, other, uid, intx.RestrictionsChange{Status: intx.StatusSuspended}); !errors.Is(err, intx.ErrTxMismatch) {
				t.Errorf("tx другого db.InTx: %v", err)
			}
			return nil
		}); err != nil {
			return err
		}
		if err := x.ChangeCityInTx(context.Background(), tx, uid, home); !errors.Is(err, intx.ErrTxMismatch) {
			t.Errorf("ctx без транзакции: %v", err)
		}
		if err := x.RevokeRoleInTx(context.Background(), tx, uid, home, intx.RevokeBy{System: true}, ""); !errors.Is(err, intx.ErrTxMismatch) {
			t.Errorf("отзыв, ctx без транзакции: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count(t, pools.Owner, `SELECT count(*) FROM users WHERE id = $1 AND city_id = $2 AND status = 'active'`, uid, away) != 1 {
		t.Fatal("после отказов что-то записано")
	}
}
