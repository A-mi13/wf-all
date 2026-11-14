package flags_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// ожидание замка — опрос тикером со сроком (ревью G7 m8: без горячего цикла с запросом на каждой итерации)
	if err := waitLockWithin(ctx, pools.Owner, done, 10*time.Second); err != nil {
		t.Fatal(err)
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

// waitLockWithin — ждать незахваченный замок в базе теста (второе встало за первым) не дольше d, опрос раз в 10 мс;
// второе завершилось раньше (done) — ошибка с его итогом.
func waitLockWithin(ctx context.Context, owner *pgxpool.Pool, done <-chan error, d time.Duration) error {
	wctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var n int
		if err := owner.QueryRow(wctx, `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			WHERE NOT l.granted AND a.datname = current_database()`).Scan(&n); err != nil {
			return fmt.Errorf("второе не встало на замке: %w", err)
		}
		if n > 0 {
			return nil
		}
		select {
		case err := <-done:
			return fmt.Errorf("второе завершилось, не встав на замок: %w", err)
		case <-wctx.Done():
			return fmt.Errorf("второе не встало на замке за %s", d)
		case <-tick.C:
		}
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

// setAuthFlags — строки auth_apple и auth_google владельцем схемы в обход Update (до Task 5 строк нет; после — те же
// значения поверх сида): флаги «везде», городов нет, version = 1.
func setAuthFlags(t *testing.T, owner *pgxpool.Pool, apple, google bool) {
	t.Helper()
	if _, err := owner.Exec(context.Background(), `INSERT INTO feature_flags (key, enabled_globally, enabled_city_ids, version)
		VALUES ('auth_apple', $1, '{}', 1), ('auth_google', $2, '{}', 1)
		ON CONFLICT (key) DO UPDATE SET enabled_globally = EXCLUDED.enabled_globally, enabled_city_ids = '{}', version = 1`,
		apple, google); err != nil {
		t.Fatal(err)
	}
}

// flagRow — флаг и версия в базе (владельцем).
func flagRow(t *testing.T, owner *pgxpool.Pool, key string) (on bool, cities []uuid.UUID, version int64) {
	t.Helper()
	if err := owner.QueryRow(context.Background(), `SELECT enabled_globally, enabled_city_ids, version FROM feature_flags
		WHERE key = $1`, key).Scan(&on, &cities, &version); err != nil {
		t.Fatal(err)
	}
	return on, cities, version
}

// GlobalOnly (спека §4.2): непустой список городов — ErrGlobalOnly до записи; пустое множество — допустимо.
func TestUpdateGlobalOnlyRejectsCities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "admin")
	setAuthFlags(t, pools.Owner, false, false)
	s := flags.NewStore(pools.As)
	err := inTx(t, pools.As, func(tx pgx.Tx) error {
		_, _, err := s.Update(ctx, tx, "auth_apple", 1, nil, []uuid.UUID{uuid.New()})
		return err
	})
	if !errors.Is(err, flags.ErrGlobalOnly) {
		t.Fatalf("город у GlobalOnly: %v", err)
	}
	if on, cities, v := flagRow(t, pools.Owner, "auth_apple"); on || len(cities) != 0 || v != 1 {
		t.Fatalf("строка изменилась: %v %v %d", on, cities, v)
	}
	on := true
	var after flags.Flag
	err = inTx(t, pools.As, func(tx pgx.Tx) (err error) {
		_, after, err = s.Update(ctx, tx, "auth_apple", 1, &on, []uuid.UUID{})
		return err
	})
	if err != nil || !after.EnabledGlobally || after.Version != 2 || len(after.EnabledCityIDs) != 0 {
		t.Fatalf("пустое множество городов: %+v %v", after, err)
	}
}

func requiresErr(t *testing.T, err error, enabling bool) {
	t.Helper()
	re, ok := errors.AsType[*flags.RequiresError](err)
	if !ok || re.Key != "auth_google" || re.Requires != "auth_apple" || re.Enabling != enabling || re.Error() == "" {
		t.Fatalf("ждали RequiresError{Enabling: %v}: %v", enabling, err)
	}
}

// Requires в обе стороны (спека §4.2): Google без Apple не включается, Apple при Google не выключается; строки
// зависимости нет — она выключена; порядок Apple → Google и обратно — успех.
func TestUpdateRequiresBothDirections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "admin")
	s := flags.NewStore(pools.As)
	on, off := true, false
	upd := func(key string, v int64, to *bool) error {
		return inTx(t, pools.As, func(tx pgx.Tx) error { _, _, err := s.Update(ctx, tx, key, v, to, nil); return err })
	}
	setAuthFlags(t, pools.Owner, false, false)
	if _, err := pools.Owner.Exec(ctx, `DELETE FROM feature_flags WHERE key = 'auth_apple'`); err != nil {
		t.Fatal(err)
	}
	requiresErr(t, upd("auth_google", 1, &on), true) // строки Apple нет

	setAuthFlags(t, pools.Owner, false, false)
	requiresErr(t, upd("auth_google", 1, &on), true)
	if g, _, v := flagRow(t, pools.Owner, "auth_google"); g || v != 1 {
		t.Fatalf("Google записан: %v %d", g, v)
	}

	setAuthFlags(t, pools.Owner, true, true)
	requiresErr(t, upd("auth_apple", 1, &off), false)
	if a, _, v := flagRow(t, pools.Owner, "auth_apple"); !a || v != 1 {
		t.Fatalf("Apple записан: %v %d", a, v)
	}

	setAuthFlags(t, pools.Owner, false, false)
	for _, st := range []struct {
		key string
		v   int64
		to  *bool
	}{{"auth_apple", 1, &on}, {"auth_google", 1, &on}, {"auth_google", 2, &off}, {"auth_apple", 2, &off}} {
		if err := upd(st.key, st.v, st.to); err != nil {
			t.Fatalf("%s → %v: %v", st.key, *st.to, err)
		}
	}
}

func isDeadlock(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "40P01"
}

// Гонка «включить Google» ∥ «выключить Apple» (оба исходно допустимы): замок обеих строк одним запросом в порядке
// ключа — ровно один успех, второй RequiresError или ErrStale, ни одного 40P01, итог согласован. Сначала
// детерминированно: блокирующая tx держит строку auth_google, обе правки встают в очередь (замки по одному в разном
// порядке здесь гарантированно дают 40P01); затем 50 гонок с общим стартом. Срок — 10 с.
func TestUpdateRequiresConcurrent(t *testing.T) {
	t.Parallel()
	pools := dbtest.NewPoolsAs(t, "admin")
	// срок — от готовой базы: клон под нагрузкой соседних пакетов сам занимает секунды
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := flags.NewStore(pools.As)
	on, off := true, false
	enableGoogle := func() error {
		return db.InTx(ctx, pools.As, func(ctx context.Context, tx pgx.Tx) error {
			_, _, err := s.Update(ctx, tx, "auth_google", 1, &on, nil)
			return err
		})
	}
	disableApple := func() error {
		return db.InTx(ctx, pools.As, func(ctx context.Context, tx pgx.Tx) error {
			_, _, err := s.Update(ctx, tx, "auth_apple", 1, &off, nil)
			return err
		})
	}
	check := func(round int, errG, errA error) {
		t.Helper()
		if isDeadlock(errG) || isDeadlock(errA) {
			t.Fatalf("раунд %d: взаимоблокировка: %v / %v", round, errG, errA)
		}
		lost := func(err error) bool {
			_, req := errors.AsType[*flags.RequiresError](err)
			return req || errors.Is(err, flags.ErrStale)
		}
		if (errG != nil || !lost(errA)) && (errA != nil || !lost(errG)) {
			t.Fatalf("раунд %d: ждали ровно один успех: Google %v, Apple %v", round, errG, errA)
		}
		a, _, _ := flagRow(t, pools.Owner, "auth_apple")
		g, _, _ := flagRow(t, pools.Owner, "auth_google")
		if g && !a {
			t.Fatalf("раунд %d: Google включён при выключенном Apple", round)
		}
	}

	setAuthFlags(t, pools.Owner, true, false)
	blocker, err := pools.Owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM feature_flags WHERE key = 'auth_google' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	g, a := make(chan error, 1), make(chan error, 1)
	go func() { g <- enableGoogle() }()
	if err := waitWaiters(ctx, pools.Owner, 1, g); err != nil {
		t.Fatal(err)
	}
	go func() { a <- disableApple() }()
	if err := waitWaiters(ctx, pools.Owner, 2, a); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	check(0, <-g, <-a)

	for round := 1; round <= 50; round++ {
		setAuthFlags(t, pools.Owner, true, false)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var errG, errA error
		wg.Add(2)
		go func() { defer wg.Done(); <-start; errG = enableGoogle() }()
		go func() { defer wg.Done(); <-start; errA = disableApple() }()
		close(start)
		wg.Wait()
		check(round, errG, errA)
	}
}

// waitWaiters — ждать, пока в базе теста не меньше n незахваченных замков (правки встали в очередь); правка
// завершилась раньше (done) — ошибка с её итогом. Опрос раз в 10 мс до срока ctx.
func waitWaiters(ctx context.Context, owner *pgxpool.Pool, n int, done <-chan error) error {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var got int
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
			WHERE NOT l.granted AND a.datname = current_database()`).Scan(&got); err != nil {
			return fmt.Errorf("ожидание %d замков: %w", n, err)
		}
		if got >= n {
			return nil
		}
		select {
		case err := <-done:
			return fmt.Errorf("правка завершилась, не встав на замок: %w", err)
		case <-ctx.Done():
			return fmt.Errorf("не дождались %d ожидающих замков: %w", n, ctx.Err())
		case <-tick.C:
		}
	}
}

// R12: чтение считает зависимый флаг выключенным при выключенной зависимости, даже если строка включена в обход
// Update; зависимости нет строки — ErrUnknownFlag (ошибка выкатки).
func TestEnabledHonoursRequires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pools := dbtest.NewPoolsAs(t, "api")
	s := flags.NewStore(pools.As)
	city := uuid.New()
	setAuthFlags(t, pools.Owner, false, true)
	if on, err := s.EnabledGlobally(ctx, "auth_google"); err != nil || on {
		t.Fatalf("EnabledGlobally при выключенном Apple: %v %v", on, err)
	}
	if on, err := s.Enabled(ctx, "auth_google", city); err != nil || on {
		t.Fatalf("Enabled при выключенном Apple: %v %v", on, err)
	}
	if err := s.Require(ctx, "auth_google", city); !errors.Is(err, flags.ErrDisabled) {
		t.Fatalf("Require при выключенном Apple: %v", err)
	}
	setAuthFlags(t, pools.Owner, true, true)
	if on, err := s.EnabledGlobally(ctx, "auth_google"); err != nil || !on {
		t.Fatalf("EnabledGlobally при включённом Apple: %v %v", on, err)
	}
	if err := s.Require(ctx, "auth_google", city); err != nil {
		t.Fatalf("Require при включённом Apple: %v", err)
	}
	if _, err := pools.Owner.Exec(ctx, `DELETE FROM feature_flags WHERE key = 'auth_apple'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnabledGlobally(ctx, "auth_google"); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("без строки Apple: %v", err)
	}
	if _, err := s.Enabled(ctx, "auth_google", city); !errors.Is(err, flags.ErrUnknownFlag) {
		t.Fatalf("Enabled без строки Apple: %v", err)
	}
}
