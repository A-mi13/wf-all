package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/identitytest"
)

// Капитанство игрока (план I-4, Task 3): тесты под ролью api (newEnv), часы — testNow.

// captaincyPlayer — игрок в основном городе капитанства (America/New_York, порог 14), порты — AllowAll.
func captaincyPlayer(t *testing.T, e *env, nick string) (uuid.UUID, captainCities) {
	t.Helper()
	cities := seedCaptainCities(t, e)
	uid := seedUser(t, e.owner, nick, "ru")
	setCity(t, e, uid, cities.main)
	e.a.captainChecks = identitytest.AllowAll()
	return uid, cities
}

func setCity(t *testing.T, e *env, uid, city uuid.UUID) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), `UPDATE users SET city_id = $2 WHERE id = $1`, uid, city); err != nil {
		t.Fatal(err)
	}
}

// wantProblem — ошибка — Problem со статусом и кодом.
func wantProblem(t *testing.T, what string, err error, status int, code string) {
	t.Helper()
	if s, c := problemStatus(err); s != status || c != code {
		t.Fatalf("%s: ждали %d %s, получили %v", what, status, code, err)
	}
}

func TestSubmitCaptainApplicationHappy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, cities := captaincyPlayer(t, e, "cap_happy")
	ctx := context.Background()
	in := CaptainApplicationInput{ProposedFormat: ptr("6×6 по средам"), ContactLink: ptr("HTTPS://t.me/Kosta_10"),
		Phone: ptr("+79181234567"), Note: ptr("Собираю ребят с работы")}
	got, err := e.a.SubmitCaptainApplication(ctx, uid, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.CityID != cities.main || !got.HasPhone || got.DecidedAt != nil ||
		!got.CreatedAt.Equal(testNow) || got.ID.Version() != 7 {
		t.Fatalf("заявка %+v", got)
	}
	if got.ContactLink == nil || *got.ContactLink != "https://t.me/Kosta_10" {
		t.Fatalf("contact_link %v: схема — в нижнем регистре, остальное как есть", got.ContactLink)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND user_id = $2 AND city_id = $3
		AND status = 'pending' AND is_probationary AND version = 1 AND phone = '+79181234567' AND created_at = $4`,
		got.ID, uid, cities.main, testNow); n != 1 {
		t.Fatal("строка заявки не та")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_type = 'captain_application'
		AND aggregate_id = $2 AND aggregate_version = 1`, identity.EventCaptainApplicationSubmitted, got.ID); n != 1 {
		t.Fatal("события подачи нет")
	}
	var after string
	if err := e.owner.QueryRow(ctx, `SELECT coalesce(after::text, '') FROM audit_log WHERE action = $1 AND object_id = $2
		AND actor_user_id = $3 AND scope_city_id = $4 AND object_type = 'captain_application'`,
		ActionCaptainApplicationSubmitted, got.ID, uid, cities.main).Scan(&after); err != nil {
		t.Fatalf("аудит подачи: %v", err)
	}
	for _, pii := range []string{"79181234567", "t.me", "Собираю"} {
		if strings.Contains(after, pii) {
			t.Errorf("ПД в аудите: %s", after)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE payload::text LIKE '%7918%' OR payload::text LIKE '%t.me%'`); n != 0 {
		t.Error("ПД в событии")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job`); n != 0 {
		t.Error("подача не шлёт писем")
	}
}

func TestSubmitCaptainApplicationCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		prepare func(t *testing.T, e *env, uid uuid.UUID, c captainCities)
		status  int
		code    string
		reasons []string
		avail   *time.Time
	}{
		{name: "прод-порты", status: 409, code: identity.CodeCaptainIneligible, reasons: []string{"checks_unavailable"},
			prepare: func(_ *testing.T, e *env, _ uuid.UUID, _ captainCities) {
				e.a.captainChecks = []identity.CaptainCheck{identity.UnavailableCheck(identity.CheckProfileComplete),
					identity.UnavailableCheck(identity.CheckCityCaptaincy)}
			}},
		{name: "моложе порога", status: 409, code: identity.CodeCaptainIneligible, reasons: []string{"too_young"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				if _, err := e.owner.Exec(context.Background(), `UPDATE users SET birth_date = '2013-01-01' WHERE id = $1`, uid); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "ограничение captain", status: 409, code: identity.CodeCaptainIneligible, reasons: []string{"restricted"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) { restrict(t, e, uid, "captain", nil) }},
		{name: "допуск в городе", status: 409, code: identity.CodeCaptainIneligible, reasons: []string{"already_permitted"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				if _, err := e.owner.Exec(context.Background(), `INSERT INTO role_assignments (user_id, role, scope_type, scope_id)
					VALUES ($1, 'captain', 'city', $2)`, uid, c.main); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "города нет (NULL)", status: 409, code: identity.CodeCaptainIneligible, reasons: []string{"checks_unavailable"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				if _, err := e.owner.Exec(context.Background(), `UPDATE users SET city_id = NULL WHERE id = $1`, uid); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "открытая заявка", status: 409, code: identity.CodeApplicationExists,
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				if _, err := e.a.SubmitCaptainApplication(context.Background(), uid, CaptainApplicationInput{}); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "причины раньше открытой заявки", status: 409, code: identity.CodeCaptainIneligible, reasons: []string{"restricted"},
			prepare: func(t *testing.T, e *env, uid uuid.UUID, _ captainCities) {
				if _, err := e.a.SubmitCaptainApplication(context.Background(), uid, CaptainApplicationInput{}); err != nil {
					t.Fatal(err)
				}
				restrict(t, e, uid, "captain", nil)
			}},
		{name: "отказ 2 дня назад", status: 409, code: identity.CodeApplicationCooldown,
			avail: ptr(testNow.Add(-2*24*time.Hour + 7*24*time.Hour)),
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				rejectedApp(t, e, uid, c.other, testNow.Add(-2*24*time.Hour))
			}},
		{name: "открытая заявка раньше паузы", status: 409, code: identity.CodeApplicationExists,
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				if _, err := e.a.SubmitCaptainApplication(context.Background(), uid, CaptainApplicationInput{}); err != nil {
					t.Fatal(err)
				}
				rejectedApp(t, e, uid, c.other, testNow.Add(-2*24*time.Hour))
			}},
		{name: "отказ ровно 7 дней назад", status: 201,
			prepare: func(t *testing.T, e *env, uid uuid.UUID, c captainCities) {
				rejectedApp(t, e, uid, c.other, testNow.Add(-7*24*time.Hour))
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			uid, cities := captaincyPlayer(t, e, "cap_codes")
			if tc.prepare != nil {
				tc.prepare(t, e, uid, cities)
			}
			before := count(t, e.owner, `SELECT count(*) FROM captain_applications`)
			beforeEv := count(t, e.owner, `SELECT count(*) FROM outbox`)
			_, err := e.a.SubmitCaptainApplication(context.Background(), uid, CaptainApplicationInput{})
			if tc.status == 201 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			wantProblem(t, tc.name, err, tc.status, tc.code)
			p := problemOfErr(err)
			if !slices.Equal(p.Reasons, tc.reasons) {
				t.Errorf("reasons %v, ждали %v", p.Reasons, tc.reasons)
			}
			switch {
			case tc.avail == nil && p.AvailableAt != nil:
				t.Errorf("available_at %v без паузы", p.AvailableAt)
			case tc.avail != nil && (p.AvailableAt == nil || !p.AvailableAt.Equal(*tc.avail)):
				t.Errorf("available_at %v, ждали %v", p.AvailableAt, tc.avail)
			}
			if count(t, e.owner, `SELECT count(*) FROM captain_applications`) != before ||
				count(t, e.owner, `SELECT count(*) FROM outbox`) != beforeEv {
				t.Error("отказ что-то записал")
			}
		})
	}
}

// Свободные поля формы (I1, m3 ревью Task 3): управляющие символы (NUL — 22021 в text, был бы 500) — 400
// validation.failed по полю; в ссылке — ещё невидимые, bidi и пробелы (подмена видимого адреса); поле из одних
// пробелов и невидимых — 400 minLength. Перевод строки и табуляция допустимы только в note. Отказ ничего не пишет.
func TestSubmitCaptainApplicationFormText(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, _ := captaincyPlayer(t, e, "cap_form")
	ctx := context.Background()
	cases := []struct {
		name  string
		in    CaptainApplicationInput
		field string
		rule  string
	}{
		{"NUL в note", CaptainApplicationInput{Note: ptr("a\x00b")}, "body.note", "pattern"},
		{"ESC в proposed_format", CaptainApplicationInput{ProposedFormat: ptr("6×6\x1b")}, "body.proposed_format", "pattern"},
		{"перевод строки в proposed_pitch", CaptainApplicationInput{ProposedPitch: ptr("Динамо\nполе")}, "body.proposed_pitch", "pattern"},
		{"DEL в play_frequency", CaptainApplicationInput{PlayFrequency: ptr("раз\x7f")}, "body.play_frequency", "pattern"},
		{"NEL (C1) в note", CaptainApplicationInput{Note: ptr("a\u0085b")}, "body.note", "pattern"},
		{"NUL в contact_link", CaptainApplicationInput{ContactLink: ptr("https://t.me/x\x00")}, "body.contact_link", "pattern"},
		{"bidi в contact_link", CaptainApplicationInput{ContactLink: ptr("https://t.me/\u202emoc.live")}, "body.contact_link", "pattern"},
		{"ZWSP в contact_link", CaptainApplicationInput{ContactLink: ptr("https://t.me/ko\u200bsta")}, "body.contact_link", "pattern"},
		{"пробел в contact_link", CaptainApplicationInput{ContactLink: ptr("https://t.me/ko sta")}, "body.contact_link", "pattern"},
		{"одни пробелы в proposed_pitch", CaptainApplicationInput{ProposedPitch: ptr("   ")}, "body.proposed_pitch", "minLength"},
		{"одни невидимые в note", CaptainApplicationInput{Note: ptr("\u200b\u2060")}, "body.note", "minLength"},
		{"NBSP в play_frequency", CaptainApplicationInput{PlayFrequency: ptr("\u00a0")}, "body.play_frequency", "minLength"},
	}
	for _, tc := range cases {
		_, err := e.a.SubmitCaptainApplication(ctx, uid, tc.in)
		p := problemOfErr(err)
		if p.Status != http.StatusBadRequest || len(p.Errors) != 1 || p.Errors[0].Field != tc.field || p.Errors[0].Code != tc.rule {
			t.Errorf("%s: ждали 400 %s %s, получили %v", tc.name, tc.field, tc.rule, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications`); n != 0 {
		t.Fatalf("отказ записал заявок: %d", n)
	}
	// многострочная заметка и обычный текст с пробелами — можно
	got, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{Note: ptr("Собираю ребят.\n\tПо средам"),
		ProposedPitch: ptr(" Динамо, поле 2 ")})
	if err != nil || got.Note == nil || *got.Note != "Собираю ребят.\n\tПо средам" {
		t.Fatalf("многострочная заметка: %+v %v", got.Note, err)
	}
}

// Две подачи одного игрока разом: подачу сериализует lockUser — одна 201, вторая 409 application_exists; ни одной
// 500. Барьер — afterFirstLock (после замка и правила): при верном замке вторая стоит на users, барьер первой
// истекает; без замка обе проходят правило и вставляют — вторая упирается в captain_applications_open_key.
func TestSubmitConcurrentSameUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, _ := captaincyPlayer(t, e, "cap_race")
	barrier := pairBarrier(time.Second)
	e.a.hooks = &hooks{afterFirstLock: func(op string) {
		if op == opSubmitCaptainApplication {
			barrier()
		}
	}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			<-start
			_, errs[i] = e.a.SubmitCaptainApplication(context.Background(), uid, CaptainApplicationInput{})
		})
	}
	close(start)
	wg.Wait()
	ok, exists := 0, 0
	for _, err := range errs {
		switch s, c := problemStatus(err); {
		case err == nil:
			ok++
		case s == http.StatusConflict && c == identity.CodeApplicationExists:
			exists++
		default:
			t.Errorf("исход %v", err)
		}
	}
	if ok != 1 || exists != 1 {
		t.Fatalf("успехов %d, application_exists %d: %v", ok, exists, errs)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE user_id = $1`, uid); n != 1 {
		t.Fatalf("заявок %d", n)
	}
}

// lockWaiters — ждёт, пока n запросов клона встанут на замок (pg_stat_activity), не дольше 5 с; не дождался — false
// (вызывающий сначала отпускает участников, затем падает: иначе висящая tx держит пул до таймаута пакета).
func lockWaiters(t *testing.T, owner *pgxpool.Pool, n int) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if count(t, owner, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock'`) >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Подача и удаление аккаунта: (а) удаление первым на замке users — подача после него видит аккаунт не живым → 401
// (errSessionGone), заявки нет; (б) подача первой держит users — удаление ждёт и затем закрывает заявку (withdrawn).
// Ни одной 500 и 40P01.
func TestSubmitVsDeleteAccount(t *testing.T) {
	t.Parallel()
	t.Run("удаление первым", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid, _ := captaincyPlayer(t, e, "cap_del_a")
		sid := issue(t, e, uid, web("x")).SessionID
		ctx := context.Background()
		hold, err := e.owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = hold.Rollback(ctx) }()
		if _, err := hold.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, uid); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var delErr, subErr error
		wg.Go(func() { _, delErr = e.a.DeleteAccount(ctx, sid, "cap_del_a") })
		waited := lockWaiters(t, e.owner, 1)
		wg.Go(func() { _, subErr = e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{}) })
		waited = waited && lockWaiters(t, e.owner, 2)
		if err := hold.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if !waited {
			t.Fatalf("удаление и подача не встали на замок users: удаление %v, подача %v", delErr, subErr)
		}
		if delErr != nil {
			t.Fatalf("удаление: %v", delErr)
		}
		if !errors.Is(subErr, errSessionGone) {
			t.Fatalf("подача после удаления: %v", subErr)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE user_id = $1`, uid); n != 0 {
			t.Fatalf("заявок %d", n)
		}
	})
	t.Run("подача первой", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid, _ := captaincyPlayer(t, e, "cap_del_b")
		sid := issue(t, e, uid, web("x")).SessionID
		ctx := context.Background()
		inside := make(chan struct{})
		resume := make(chan struct{})
		var once sync.Once
		e.a.hooks = &hooks{afterFirstLock: func(op string) {
			if op == opSubmitCaptainApplication {
				once.Do(func() { close(inside); <-resume })
			}
		}}
		var wg sync.WaitGroup
		var delErr, subErr error
		wg.Go(func() { _, subErr = e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{}) })
		select {
		case <-inside:
		case <-time.After(10 * time.Second):
			close(resume)
			wg.Wait()
			t.Fatalf("подача не дошла до барьера: %v", subErr)
		}
		wg.Go(func() { _, delErr = e.a.DeleteAccount(ctx, sid, "cap_del_b") })
		waited := lockWaiters(t, e.owner, 1)
		close(resume)
		wg.Wait()
		if !waited {
			t.Fatalf("удаление не ждало замка подачи: подача %v, удаление %v", subErr, delErr)
		}
		if subErr != nil || delErr != nil {
			t.Fatalf("подача %v, удаление %v", subErr, delErr)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE user_id = $1 AND status = 'withdrawn'`, uid); n != 1 {
			t.Fatal("удаление не закрыло заявку")
		}
	})
}

func TestWithdrawCaptainApplication(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, cities := captaincyPlayer(t, e, "cap_wd")
	other := seedUser(t, e.owner, "cap_wd_other", "ru")
	setCity(t, e, other, cities.main)
	ctx := context.Background()
	mine, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{Phone: ptr("+79181234567")})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.a.SubmitCaptainApplication(ctx, other, CaptainApplicationInput{})
	if err != nil {
		t.Fatal(err)
	}
	// чужая — как несуществующая, и не меняется
	_, errForeign := e.a.WithdrawCaptainApplication(ctx, uid, foreign.ID)
	wantProblem(t, "чужая", errForeign, http.StatusNotFound, identity.CodeApplicationNotFound)
	_, errMissing := e.a.WithdrawCaptainApplication(ctx, uid, uuid.Must(uuid.NewV7()))
	wantProblem(t, "несуществующая", errMissing, http.StatusNotFound, identity.CodeApplicationNotFound)
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'pending' AND version = 1`,
		foreign.ID); n != 1 {
		t.Fatal("чужая заявка тронута")
	}
	e.clock.Advance(time.Hour)
	got, err := e.a.WithdrawCaptainApplication(ctx, uid, mine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "withdrawn" || got.DecidedAt == nil || !got.DecidedAt.Equal(testNow.Add(time.Hour)) || !got.HasPhone {
		t.Fatalf("отзыв %+v", got)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'withdrawn' AND version = 2
		AND closed_at = $2`, mine.ID, testNow.Add(time.Hour)); n != 1 {
		t.Fatal("строка отзыва не та")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2 AND aggregate_version = 2`,
		identity.EventCaptainApplicationWithdrawn, mine.ID); n != 1 {
		t.Fatal("события отзыва нет")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2 AND actor_user_id = $3
		AND scope_city_id = $4`, ActionCaptainApplicationWithdrawn, mine.ID, uid, cities.main); n != 1 {
		t.Fatal("аудита отзыва нет")
	}
	_, err = e.a.WithdrawCaptainApplication(ctx, uid, mine.ID)
	wantProblem(t, "повтор", err, http.StatusConflict, identity.CodeApplicationClosed)
	// отзыв паузы не ставит
	if _, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{}); err != nil {
		t.Fatalf("подача после отзыва: %v", err)
	}
}

func TestMyCaptaincy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("открытая заявка и допуск в другом городе", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid, cities := captaincyPlayer(t, e, "cap_my_open")
		var appID uuid.UUID
		if err := e.owner.QueryRow(ctx, `INSERT INTO captain_applications (id, user_id, city_id, status, reviewed_at,
			reviewed_by_label, is_probationary) VALUES (gen_random_uuid(), $1, $2, 'approved', $3, 'Модератор', true) RETURNING id`,
			uid, cities.other, testNow.Add(-48*time.Hour)).Scan(&appID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.owner.Exec(ctx, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, granted_at,
			application_id) VALUES ($1, 'captain', 'city', $2, $3, $4)`, uid, cities.other, testNow.Add(-48*time.Hour), appID); err != nil {
			t.Fatal(err)
		}
		open, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{})
		if err != nil {
			t.Fatal(err)
		}
		c, err := e.a.MyCaptaincy(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		if c.Eligible || len(c.Reasons) != 0 || c.AvailableAt != nil || c.CityID != cities.main {
			t.Fatalf("eligibility %+v", c)
		}
		if c.OpenApplication == nil || c.OpenApplication.ID != open.ID || c.OpenApplication.Status != "pending" {
			t.Fatalf("открытая заявка %+v", c.OpenApplication)
		}
		if len(c.Permits) != 1 || c.Permits[0].CityID != cities.other || !c.Permits[0].Probationary ||
			!c.Permits[0].GrantedAt.Equal(testNow.Add(-48*time.Hour)) {
			t.Fatalf("допуски %+v", c.Permits)
		}
	})
	t.Run("пауза после отказа", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid, cities := captaincyPlayer(t, e, "cap_my_cool")
		rejectedApp(t, e, uid, cities.main, testNow.Add(-3*24*time.Hour))
		c, err := e.a.MyCaptaincy(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		if c.Eligible || len(c.Reasons) != 0 || c.AvailableAt == nil || !c.AvailableAt.Equal(testNow.Add(4*24*time.Hour)) ||
			c.OpenApplication != nil || c.Permits == nil || len(c.Permits) != 0 {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("прод-порты", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid, _ := captaincyPlayer(t, e, "cap_my_prod")
		e.a.captainChecks = []identity.CaptainCheck{identity.UnavailableCheck(identity.CheckProfileComplete),
			identity.UnavailableCheck(identity.CheckCityCaptaincy)}
		c, err := e.a.MyCaptaincy(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		if c.Eligible || !slices.Equal(c.Reasons, []string{"checks_unavailable"}) {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("можно подать", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid, _ := captaincyPlayer(t, e, "cap_my_ok")
		c, err := e.a.MyCaptaincy(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Eligible || c.Reasons == nil || len(c.Reasons) != 0 {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("аккаунта нет", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		if _, err := e.a.MyCaptaincy(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, errSessionGone) {
			t.Fatalf("%v", err)
		}
	})
}

func TestListMyCaptainApplications(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, _ := captaincyPlayer(t, e, "cap_list")
	other := seedUser(t, e.owner, "cap_list_other", "ru")
	ctx := context.Background()
	var ids []uuid.UUID
	for i := range 3 {
		if i == 2 {
			e.clock.Advance(time.Minute) // две первые — в одно время: порядок решает id
		}
		a, err := e.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.WithdrawCaptainApplication(ctx, uid, a.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO captain_applications (user_id, city_id)
		SELECT $1, city_id FROM users WHERE id = $2`, other, uid); err != nil {
		t.Fatal(err)
	}
	page1, next, err := e.a.ListMyCaptainApplications(ctx, uid, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].ID != ids[2] || page1[1].ID != ids[1] || next == "" {
		t.Fatalf("страница 1: %v %q", page1, next)
	}
	page2, next2, err := e.a.ListMyCaptainApplications(ctx, uid, next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].ID != ids[0] || next2 != "" {
		t.Fatalf("страница 2: %v %q", page2, next2)
	}
	_, _, err = e.a.ListMyCaptainApplications(ctx, uid, "испорчен", 2)
	p := problemOfErr(err)
	if p.Status != http.StatusBadRequest || len(p.Errors) != 1 || p.Errors[0].Field != "query.cursor" {
		t.Fatalf("испорченный курсор: %v", err)
	}
}
