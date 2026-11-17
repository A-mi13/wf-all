package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/identitytest"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/dbtest"
)

var discard = slog.New(slog.DiscardHandler)

// noLinks — порт связи сотрудника без связей (сборка NewAdmin в тестах конструктора).
type noLinks struct{}

func (noLinks) LinkedUser(context.Context, pgx.Tx, uuid.UUID) (uuid.UUID, int64, error) {
	return uuid.Nil, 1, nil
}

// Неверный набор портов условий — отказ сборки api и admin-api (К1): один порт, повтор; NewAdmin — и без StaffLinks.
func TestAppNewRejectsBadChecks(t *testing.T) {
	t.Parallel()
	one := []identity.CaptainCheck{identitytest.AllowCheck(identity.CheckProfileComplete)}
	t.Run("api", func(t *testing.T) {
		t.Parallel()
		var d identity.Deps
		newEnv(t, func(got *identity.Deps) { d = *got }) // зависимости годной сборки
		for _, bad := range [][]identity.CaptainCheck{nil, one,
			append(identitytest.AllowAll(), identitytest.AllowCheck(identity.CheckCityCaptaincy))} {
			d.CaptainChecks = bad
			if _, err := New(d); !errors.Is(err, identity.ErrCaptainChecks) {
				t.Fatalf("New с портами %d: %v", len(bad), err)
			}
		}
		d.CaptainChecks = identitytest.AllowAll()
		if _, err := New(d); err != nil {
			t.Fatalf("New с полным набором: %v", err)
		}
	})
	t.Run("admin", func(t *testing.T) {
		t.Parallel()
		pools := dbtest.NewPoolsAs(t, "admin")
		ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(discard))
		if err != nil {
			t.Fatal(err)
		}
		d := identity.AdminDeps{Pool: pools.As, Clock: clock.System, River: ins, Geo: geo.New(pools.As),
			CaptainChecks: one, StaffLinks: noLinks{}, Log: discard}
		if _, err := NewAdmin(d); !errors.Is(err, identity.ErrCaptainChecks) {
			t.Fatalf("NewAdmin с одним портом: %v", err)
		}
		d.CaptainChecks = identitytest.AllowAll()
		if _, err := NewAdmin(d); err != nil {
			t.Fatalf("NewAdmin с полным набором: %v", err)
		}
		d.StaffLinks = nil
		if _, err := NewAdmin(d); !errors.Is(err, errMissingDeps) {
			t.Fatalf("NewAdmin без StaffLinks: %v", err)
		}
		if _, err := NewTx(TxDeps{Pool: pools.As, Clock: clock.System, River: ins, Geo: geo.New(pools.As), Log: discard}); err != nil {
			t.Fatalf("NewTx: %v", err)
		}
		if _, err := NewTx(TxDeps{Pool: pools.As, Clock: clock.System, River: ins, Log: discard}); !errors.Is(err, errMissingDeps) {
			t.Fatalf("NewTx без Geo: %v", err)
		}
	})
}

// captainCase — подготовка игрока и ожидание вердикта evaluateCaptaincy.
type captainCase struct {
	name    string
	city    func(c captainCities) uuid.UUID // город проверки; nil — основной
	checks  []identity.CaptainCheck         // nil — AllowAll
	prepare func(t *testing.T, e *env, uid uuid.UUID, c captainCities)
	birth   string // дата рождения; "" — 2000-01-01
	reasons []string
	checkOf map[string]string // ожидаемые Checks по ключам (только заданные)
	until   *time.Time        // ожидаемый AvailableAt
	open    bool              // есть открытая заявка
}

// captainCities — основной город (America/New_York, порог 14), второй город и город выключенной страны.
type captainCities struct{ main, other, disabled uuid.UUID }

// evalNow — 02:00 UTC 14.11: в Нью-Йорке ещё 13.11 (граница «сегодня» по поясу города).
var evalNow = time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)

func seedCaptainCities(t *testing.T, e *env) captainCities {
	t.Helper()
	ctx := context.Background()
	mk := func(code string, enabled bool, tz string) uuid.UUID {
		if _, err := e.owner.Exec(ctx, `INSERT INTO countries (code, name, currency, default_locale, phone_prefix,
			week_starts_on, min_signup_age, age_of_majority, is_enabled) VALUES ($1::text, 'Страна ' || $1::text, 'XXX', 'ru', '+999', 1, 14, 18, $2)`,
			code, enabled); err != nil {
			t.Fatal(err)
		}
		var id uuid.UUID
		if err := e.owner.QueryRow(ctx, `INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
			SELECT id, 'Город ' || code, lower(code) || '-city', $2, 'live', ST_SetSRID(ST_MakePoint(41.9, 45.0), 4326)::geography
			FROM countries WHERE code = $1 RETURNING id`, code, tz).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	main := mk("QA", true, "America/New_York")
	var other uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, 'Второй QA', 'qa-two', 'America/New_York', 'live', ST_SetSRID(ST_MakePoint(42.0, 45.1), 4326)::geography
		FROM countries WHERE code = 'QA' RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	return captainCities{main: main, other: other, disabled: mk("QB", false, "America/New_York")}
}

// rejectedApp — решённая отказом заявка игрока в городе city, решение — в decided.
func rejectedApp(t *testing.T, e *env, uid, city uuid.UUID, decided time.Time) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO captain_applications (user_id, city_id, status,
		reviewed_at, reviewed_by_label, rejection_code, closed_at) VALUES ($1, $2, 'rejected', $3, 'Модератор', 'insufficient_info', $3)`,
		uid, city, decided); err != nil {
		t.Fatal(err)
	}
}

func restrict(t *testing.T, e *env, uid uuid.UUID, capability string, until *time.Time) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_restrictions (id, user_id, capability, until,
		source_id, created_at) VALUES ($1, $2, $3, $4, $5, $6)`, uuid.New(), uid, capability, until, uuid.New(),
		evalNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// pendingDeletionBanned — аккаунт в периоде удаления, статус до удаления — banned (R11).
func pendingDeletionBanned(t *testing.T, e *env, uid uuid.UUID) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), `UPDATE users SET status = 'pending_deletion',
		status_before_deletion = 'banned', deletion_requested_at = $2, purge_at = $3 WHERE id = $1`,
		uid, evalNow.Add(-time.Hour), evalNow.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

// Одно правило условий капитанства (R5): возраст по поясу города, ограничения и «заблокирован» (R36), порты, допуск,
// пауза по игроку (Q4), открытая заявка; причины — в ReasonOrder.
func TestEvaluateCaptaincy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cities := seedCaptainCities(t, e)
	exec := func(t *testing.T, q string, args ...any) {
		t.Helper()
		if _, err := e.owner.Exec(context.Background(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	cases := []captainCase{
		{name: "всё выполнено", reasons: nil,
			checkOf: map[string]string{"age": "ok", "restrictions": "ok", "profile": "ok", "captaincy": "ok"}},
		{name: "моложе порога по поясу города", birth: "2012-11-14", reasons: []string{"too_young"},
			checkOf: map[string]string{"age": "failed"}},
		{name: "ровно порог по поясу города", birth: "2012-11-13", reasons: nil, checkOf: map[string]string{"age": "ok"}},
		{name: "ограничение captain", reasons: []string{"restricted"}, checkOf: map[string]string{"restrictions": "failed"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) { restrict(t, e, uid, "captain", nil) }},
		{name: "ограничение captain истекло ровно сейчас", reasons: nil, checkOf: map[string]string{"restrictions": "ok"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				restrict(t, e, uid, "captain", ptr(evalNow))
			}},
		{name: "suspended", reasons: []string{"restricted"}, checkOf: map[string]string{"restrictions": "failed"},
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, _ captainCities) {
				exec(t, `UPDATE users SET status = 'suspended' WHERE id = $1`, uid)
			}},
		{name: "удаление поверх suspended", reasons: []string{"restricted"},
			checkOf: map[string]string{"restrictions": "failed"},
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, _ captainCities) {
				exec(t, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'suspended',
					deletion_requested_at = $2, purge_at = $3 WHERE id = $1`, uid, evalNow.Add(-time.Hour),
					evalNow.Add(30*24*time.Hour))
			}},
		{name: "удаление поверх active", reasons: nil, checkOf: map[string]string{"restrictions": "ok"},
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, _ captainCities) {
				exec(t, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active',
					deletion_requested_at = $2, purge_at = $3 WHERE id = $1`, uid, evalNow.Add(-time.Hour),
					evalNow.Add(30*24*time.Hour))
			}},
		{name: "banned", reasons: []string{"restricted"}, checkOf: map[string]string{"restrictions": "failed"},
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, _ captainCities) {
				exec(t, `UPDATE users SET status = 'banned' WHERE id = $1`, uid)
				// бан по R42 — со строкой login (без неё бан истёк, R12)
				exec(t, `INSERT INTO user_restrictions (id, user_id, capability, source_id, created_at)
					VALUES (gen_random_uuid(), $1, 'login', gen_random_uuid(), now())`, uid)
			}},
		{name: "удаление поверх живого бана", reasons: []string{"restricted"},
			checkOf: map[string]string{"restrictions": "failed"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				pendingDeletionBanned(t, e, uid)
				restrict(t, e, uid, "login", ptr(evalNow.Add(time.Second)))
			}},
		{name: "удаление поверх истёкшего бана", reasons: nil, checkOf: map[string]string{"restrictions": "ok"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				pendingDeletionBanned(t, e, uid)
				restrict(t, e, uid, "login", ptr(evalNow.Add(-time.Minute)))
			}},
		{name: "строка login истекает ровно сейчас", reasons: nil, checkOf: map[string]string{"restrictions": "ok"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				pendingDeletionBanned(t, e, uid)
				restrict(t, e, uid, "login", ptr(evalNow))
			}},
		{name: "профиль не заполнен", reasons: []string{"profile_incomplete"},
			checks: []identity.CaptainCheck{identitytest.DenyCheck(identity.CheckProfileComplete, "profile_incomplete"),
				identitytest.AllowCheck(identity.CheckCityCaptaincy)},
			checkOf: map[string]string{"profile": "failed", "captaincy": "ok"}},
		{name: "порты недоступны", reasons: []string{"checks_unavailable"},
			checks: []identity.CaptainCheck{identity.UnavailableCheck(identity.CheckCityCaptaincy),
				identity.UnavailableCheck(identity.CheckProfileComplete)},
			checkOf: map[string]string{"profile": "unavailable", "captaincy": "unavailable"}},
		{name: "допуск в городе", reasons: []string{"already_permitted"},
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, c captainCities) {
				exec(t, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, 'captain', 'city', $2)`,
					uid, c.main)
			}},
		{name: "допуск в другом городе", reasons: nil,
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, c captainCities) {
				exec(t, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, 'captain', 'city', $2)`,
					uid, c.other)
			}},
		{name: "отказ 3 дня назад в другом городе", until: ptr(evalNow.Add(-3*24*time.Hour + 7*24*time.Hour)),
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				rejectedApp(t, e, uid, c.other, evalNow.Add(-3*24*time.Hour))
			}},
		{name: "последний из двух отказов", until: ptr(evalNow.Add(-1*24*time.Hour + 7*24*time.Hour)),
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				rejectedApp(t, e, uid, c.main, evalNow.Add(-6*24*time.Hour))
				rejectedApp(t, e, uid, c.other, evalNow.Add(-1*24*time.Hour))
			}},
		{name: "отказ ровно 7 дней назад", until: nil,
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				rejectedApp(t, e, uid, c.main, evalNow.Add(-7*24*time.Hour))
			}},
		{name: "отказ 8 дней назад", until: nil,
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				rejectedApp(t, e, uid, c.main, evalNow.Add(-8*24*time.Hour))
			}},
		{name: "отзыв паузы не ставит", until: nil,
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, c captainCities) {
				exec(t, `INSERT INTO captain_applications (user_id, city_id, status, closed_at) VALUES ($1, $2, 'withdrawn', $3)`,
					uid, c.main, evalNow.Add(-time.Hour))
			}},
		{name: "открытая заявка в городе", open: true,
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, c captainCities) {
				exec(t, `INSERT INTO captain_applications (user_id, city_id) VALUES ($1, $2)`, uid, c.main)
			}},
		{name: "открытая заявка в другом городе", open: false,
			prepare: func(t *testing.T, _ *env, uid uuid.UUID, c captainCities) {
				exec(t, `INSERT INTO captain_applications (user_id, city_id) VALUES ($1, $2)`, uid, c.other)
			}},
		{name: "несколько причин — по ReasonOrder", birth: "2012-11-14",
			reasons: []string{"too_young", "restricted", "profile_incomplete", "captain_elsewhere_in_city", "already_permitted"},
			checks: []identity.CaptainCheck{identitytest.DenyCheck(identity.CheckCityCaptaincy, "captain_elsewhere_in_city"),
				identitytest.DenyCheck(identity.CheckProfileComplete, "profile_incomplete")},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				exec(t, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, 'captain', 'city', $2)`,
					uid, c.main)
				restrict(t, e, uid, "captain", nil)
			}},
		{name: "страна города выключена", city: func(c captainCities) uuid.UUID { return c.disabled },
			reasons: []string{"checks_unavailable"}, checkOf: map[string]string{"age": "unavailable"}},
		{name: "города нет", city: func(captainCities) uuid.UUID { return uuid.New() },
			reasons: []string{"checks_unavailable"}, checkOf: map[string]string{"age": "unavailable"}},
	}
	for i, tc := range cases {
		uid := seedUser(t, e.owner, "cap_"+string(rune('a'+i)), "ru")
		// текущий город игрока — основной: пауза по городу (а не по игроку) не видела бы отказ во втором
		exec(t, `UPDATE users SET city_id = $2 WHERE id = $1`, uid, cities.main)
		if tc.birth != "" {
			exec(t, `UPDATE users SET birth_date = $2 WHERE id = $1`, uid, tc.birth)
		}
		if tc.prepare != nil {
			tc.prepare(t, e, uid, cities)
		}
		city := cities.main
		if tc.city != nil {
			city = tc.city(cities)
		}
		e.a.captainChecks = identitytest.AllowAll()
		if tc.checks != nil {
			e.a.captainChecks = tc.checks
		}
		var v captaincyVerdict
		if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			v, err = e.a.evaluateCaptaincy(ctx, tx, uid, city, evalNow)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(v.Reasons, tc.reasons) {
			t.Errorf("%s: reasons %v, ждали %v", tc.name, v.Reasons, tc.reasons)
		}
		for k, want := range tc.checkOf {
			if v.Checks[k] != want {
				t.Errorf("%s: Checks[%s] = %q, ждали %q (%v)", tc.name, k, v.Checks[k], want, v.Checks)
			}
		}
		switch {
		case tc.until == nil && v.AvailableAt != nil:
			t.Errorf("%s: пауза до %v, ждали без паузы", tc.name, v.AvailableAt)
		case tc.until != nil && (v.AvailableAt == nil || !v.AvailableAt.Equal(*tc.until)):
			t.Errorf("%s: пауза %v, ждали %v", tc.name, v.AvailableAt, tc.until)
		}
		if (v.OpenApplicationID != uuid.Nil) != tc.open {
			t.Errorf("%s: открытая заявка %v, ждали %v", tc.name, v.OpenApplicationID, tc.open)
		}
		wantPermitted := slices.Contains(tc.reasons, "already_permitted")
		if v.Permitted != wantPermitted {
			t.Errorf("%s: Permitted %v", tc.name, v.Permitted)
		}
		if eligible := len(tc.reasons) == 0 && tc.until == nil && !tc.open; v.Eligible() != eligible {
			t.Errorf("%s: Eligible %v, ждали %v", tc.name, v.Eligible(), eligible)
		}
	}
}

// Сбой порта — ошибка (500), а не «условие не выполнено»; порт без причины при ok = false — тоже ошибка.
func TestEvaluateCaptaincyPortError(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cities := seedCaptainCities(t, e)
	uid := seedUser(t, e.owner, "cap_err", "ru")
	boom := errors.New("порт упал")
	for _, bad := range []identity.CaptainCheck{failingCheck{err: boom}, failingCheck{}} {
		e.a.captainChecks = []identity.CaptainCheck{bad, identitytest.AllowCheck(identity.CheckCityCaptaincy)}
		err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			_, err := e.a.evaluateCaptaincy(ctx, tx, uid, cities.main, evalNow)
			return err
		})
		if err == nil || (bad.(failingCheck).err != nil && !errors.Is(err, boom)) {
			t.Fatalf("ошибка порта: %v", err)
		}
	}
}

// failingCheck — порт профиля: err != nil — сбой; иначе «не выполнено» без причины (нарушение контракта порта).
type failingCheck struct{ err error }

func (failingCheck) Name() string { return identity.CheckProfileComplete }

func (c failingCheck) Check(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (string, bool, error) {
	return "", false, c.err
}

// m1 ревью W1, К1: App без портов условий (NewTx, NewMaintenance) или с неполным набором не считает порты пройденными —
// каждое имя RequiredCaptainChecks без порта — unavailable и причина checks_unavailable (отказ, не доступ).
func TestEvaluateCaptaincyMissingPortsFailClosed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cities := seedCaptainCities(t, e)
	uid := seedUser(t, e.owner, "cap_noports", "ru")
	for name, checks := range map[string][]identity.CaptainCheck{
		"без портов":       nil,
		"без порта города": {identitytest.AllowCheck(identity.CheckProfileComplete)},
	} {
		e.a.captainChecks = checks
		var v captaincyVerdict
		if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			v, err = e.a.evaluateCaptaincy(ctx, tx, uid, cities.main, evalNow)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.Eligible() || !slices.Contains(v.Reasons, "checks_unavailable") || v.Checks[checkCaptaincy] != checkUnavailable {
			t.Fatalf("%s: вердикт %+v", name, v)
		}
		if checks == nil && v.Checks[checkProfile] != checkUnavailable {
			t.Fatalf("%s: профиль %q", name, v.Checks[checkProfile])
		}
	}
}
