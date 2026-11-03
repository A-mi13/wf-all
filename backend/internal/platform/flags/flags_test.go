package flags_test

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/testkit/dbtest"
)

func TestEnabledByCity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	moscow, kazan := uuid.New(), uuid.New()
	// 'ads' уже заведён сидом 0010 выключенным — берём ключи теста с префиксом t_.
	if _, err := pools.Owner.Exec(ctx, `INSERT INTO feature_flags (key, enabled_globally, enabled_city_ids) VALUES
		('t_chat', false, ARRAY[$1::uuid]), ('t_ads', true, '{}'), ('t_off', false, '{}')`, moscow); err != nil {
		t.Fatal(err)
	}
	s := flags.NewStore(pools.As)
	cases := []struct {
		key  string
		city uuid.UUID
		want bool
	}{
		{"t_chat", moscow, true}, {"t_chat", kazan, false}, {"t_ads", kazan, true}, {"t_off", moscow, false},
	}
	for _, c := range cases {
		got, err := s.Enabled(ctx, c.key, c.city)
		if err != nil || got != c.want {
			t.Errorf("%s в %v: %v %v", c.key, c.city, got, err)
		}
	}
	if _, err := s.Enabled(ctx, "nope", moscow); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("неизвестный флаг: %v", err)
	}

	if err := s.Require(ctx, "t_chat", moscow); err != nil {
		t.Fatal(err)
	}
	err := s.Require(ctx, "t_chat", kazan)
	pe, ok := errors.AsType[httpx.ProblemError](err)
	if !errors.Is(err, flags.ErrDisabled) || !ok || pe.Problem().Status != http.StatusForbidden || pe.Problem().Code != httpx.CodeFeatureDisabled {
		t.Fatalf("выключенный флаг: %v", err)
	}
	if err := s.Require(ctx, "nope", moscow); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("Require неизвестного флага: %v", err)
	}
}

// Сид 0010 заводит флаги выключенными: «чего сейчас НЕ делаем» из CLAUDE.md.
func TestSeededFlagIsOff(t *testing.T) {
	t.Parallel()
	on, err := flags.NewStore(dbtest.NewPool(t)).EnabledGlobally(context.Background(), "subscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("subscriptions должен быть выключен")
	}
}

func TestUnknownFlag(t *testing.T) {
	t.Parallel()
	_, err := flags.NewStore(dbtest.NewPool(t)).EnabledGlobally(context.Background(), "no_such_flag")
	if !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("err = %v, ждали ErrUnknownFlag", err)
	}
}

// Список флагов (Task 15): сид 0010 — все ключи, version = 1, по ключу; города — как в базе.
func TestListFlags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "admin")
	list, err := flags.NewStore(pools.As).List(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("%v %v", list, err)
	}
	for i, f := range list {
		if f.Version != 1 || f.UpdatedAt.IsZero() || f.EnabledCityIDs == nil || (i > 0 && list[i-1].Key >= f.Key) {
			t.Fatalf("флаг %d: %+v", i, f)
		}
	}
}

func inTx(t *testing.T, pool *pgxpool.Pool, f func(pgx.Tx) error) error {
	t.Helper()
	return db.InTx(context.Background(), pool, func(_ context.Context, tx pgx.Tx) error { return f(tx) })
}

// Update: версия сходится — запись, version + 1, города множеством (повторы схлопнуты, порядок по id), nil — поле
// не меняется; устаревшая версия — ErrStale без записи; нет ключа — ErrUnknownFlag; ничего не меняется — before ==
// after, version не растёт.
func TestUpdateFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "admin")
	s := flags.NewStore(pools.As)
	a, b := uuid.MustParse("00000000-0000-7000-8000-00000000000b"), uuid.MustParse("00000000-0000-7000-8000-00000000000a")
	on := true
	var before, after flags.Flag
	err := inTx(t, pools.As, func(tx pgx.Tx) (err error) {
		before, after, err = s.Update(ctx, tx, "ads", 1, &on, []uuid.UUID{a, b, a})
		return err
	})
	if err != nil || before.Version != 1 || before.EnabledGlobally || len(before.EnabledCityIDs) != 0 ||
		after.Version != 2 || !after.EnabledGlobally || !slices.Equal(after.EnabledCityIDs, []uuid.UUID{b, a}) {
		t.Fatalf("%+v → %+v %v", before, after, err)
	}
	if n := 0; pools.Owner.QueryRow(ctx, `SELECT count(*) FROM feature_flags WHERE key = 'ads' AND enabled_city_ids = $1`,
		[]uuid.UUID{b, a}).Scan(&n) != nil || n != 1 {
		t.Fatal("в базе города не множеством")
	}
	// только города; globally не трогается
	err = inTx(t, pools.As, func(tx pgx.Tx) (err error) {
		_, after, err = s.Update(ctx, tx, "ads", 2, nil, []uuid.UUID{})
		return err
	})
	if err != nil || after.Version != 3 || !after.EnabledGlobally || len(after.EnabledCityIDs) != 0 {
		t.Fatalf("города: %+v %v", after, err)
	}
	// ничего не меняется — версия та же
	err = inTx(t, pools.As, func(tx pgx.Tx) (err error) {
		before, after, err = s.Update(ctx, tx, "ads", 3, &on, nil)
		return err
	})
	if err != nil || after.Version != 3 || before.Version != 3 {
		t.Fatalf("без изменений: %+v %v", after, err)
	}
	if err := inTx(t, pools.As, func(tx pgx.Tx) error { _, _, err := s.Update(ctx, tx, "ads", 2, &on, nil); return err }); !errors.Is(err, flags.ErrStale) {
		t.Fatalf("устаревшая без изменений: %v", err)
	}
	off := false
	if err := inTx(t, pools.As, func(tx pgx.Tx) error { _, _, err := s.Update(ctx, tx, "ads", 2, &off, nil); return err }); !errors.Is(err, flags.ErrStale) {
		t.Fatalf("устаревшая с изменением: %v", err)
	}
	if err := inTx(t, pools.As, func(tx pgx.Tx) error { _, _, err := s.Update(ctx, tx, "nope", 1, &on, nil); return err }); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("нет флага: %v", err)
	}
	var v int64
	if err := pools.Owner.QueryRow(ctx, `SELECT version FROM feature_flags WHERE key = 'ads'`).Scan(&v); err != nil || v != 3 {
		t.Fatalf("версия в базе %d %v", v, err)
	}
}

// Два обновления одной версии: первое изменило строку (транзакция открыта), второе прочитало version 1 и ждёт
// замок строки в UPDATE; первое коммитит — второе ErrStale (WHERE version), в базе одна запись (version 2).
func TestUpdateFlagConcurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "admin")
	s := flags.NewStore(pools.As)
	on := true
	tx1, err := pools.As.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, _, err := s.Update(ctx, tx1, "ads", 1, &on, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- inTx(t, pools.As, func(tx pgx.Tx) error { _, _, err := s.Update(ctx, tx, "ads", 1, &on, nil); return err })
	}()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		var n int
		if err := pools.Owner.QueryRow(wctx, `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			WHERE NOT l.granted AND a.datname = current_database()`).Scan(&n); err != nil {
			t.Fatalf("второе не встало на замке: %v", err)
		}
		if n > 0 {
			break
		}
		runtime.Gosched()
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, flags.ErrStale) {
		t.Fatalf("второе: %v", err)
	}
	var v int64
	if err := pools.Owner.QueryRow(ctx, `SELECT version FROM feature_flags WHERE key = 'ads'`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("версия %d %v", v, err)
	}
}

// R18: событие правки — агрегат feature_flag с uuid id.Named("feature_flag", ключ), версия агрегата — version флага,
// payload — ключ, флаг и id городов (без ПД).
func TestFlagChangedEventAggregate(t *testing.T) {
	t.Parallel()
	city := uuid.New()
	ev := flags.ChangedEvent(flags.Flag{Key: "ads", EnabledGlobally: true, EnabledCityIDs: []uuid.UUID{city}, Version: 7, Description: "x"})
	if ev.Type != "platform.flag_changed" || ev.AggregateType != "feature_flag" || ev.AggregateID != id.Named("feature_flag", "ads") ||
		ev.AggregateVersion != 7 || ev.SchemaVersion != 1 {
		t.Fatalf("%+v", ev)
	}
	if p, ok := ev.Payload.(flags.FlagChanged); !ok || p.Key != "ads" || !p.EnabledGlobally || !slices.Equal(p.EnabledCityIDs, []uuid.UUID{city}) {
		t.Fatalf("payload %+v", ev.Payload)
	}
}
