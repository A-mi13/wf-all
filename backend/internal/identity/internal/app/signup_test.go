package app

import (
	"context"
	"net/netip"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
)

const signupPassword = "Жёлтая карточка на 90-й" //nolint:gosec // G101: пароль теста, не учётные данные

// seedSignupCity — страна (код code, порог minAge, включена ли) и город в ней с таймзоной Europe/Moscow.
func seedSignupCity(t *testing.T, e *env, code string, enabled bool, minAge int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, err := e.owner.Exec(ctx, `INSERT INTO countries (code, name, currency, default_locale, phone_prefix,
		week_starts_on, min_signup_age, age_of_majority, is_enabled) VALUES ($1, $4, 'XXX', 'ru', '+999', 1, $2, 18, $3)`,
		code, minAge, enabled, "Страна "+code); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, 'Город ' || code, lower(code) || '-city', 'Europe/Moscow', 'live', ST_SetSRID(ST_MakePoint(41.9, 45.0), 4326)::geography
		FROM countries WHERE code = $1 RETURNING id`, code).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func signupInput(email, nick string, city uuid.UUID) SignupInput {
	return SignupInput{Email: email, Password: signupPassword, Nickname: nick, Locale: "ru",
		BirthDate: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), CityID: city,
		Accepted: []domain.Accepted{{Document: domain.DocTerms, Version: "2026-11-01"},
			{Document: domain.DocPrivacy, Version: "2026-11-01"}}}
}

// §3.1 п. 2, §9.1: свободная и занятая почта (в том числе pending_deletion) — одинаковый ответ (сроки от now те же),
// по строке auth_codes на каждую (у занятой — приманка без секретов), письма — signup_code и signup_account_exists.
func TestStartSignupDecoyIndistinguishable(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := seedSignupCity(t, e, "XS", true, 14)
	seedUser(t, e.owner, "decoy_taken", "ru")
	pending := seedUser(t, e.owner, "decoy_pending", "ru")
	setStatus(t, e, pending, "pending_deletion")
	now := e.clock.Now()
	var sents []CodeSent
	for i, email := range []string{"decoy_free@example.com", "decoy_taken@example.com", "decoy_pending@example.com"} {
		sent, err := e.a.StartSignup(context.Background(), signupInput(email, "decoy_nick"+string(rune('a'+i)), city),
			mobile("ios", "decoy-dev"))
		if err != nil {
			t.Fatalf("%s: %v", email, err)
		}
		if sent.CodeID == uuid.Nil || sent.ExpiresAt.Sub(now) != domain.CodeTTL || sent.ResendAvailable.Sub(now) != 60*time.Second {
			t.Fatalf("%s: %+v", email, sent)
		}
		sents = append(sents, sent)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND NOT decoy AND payload_enc IS NOT NULL`,
		sents[0].CodeID); n != 1 {
		t.Fatal("свободная почта: нет настоящего кода")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = ANY($1) AND decoy AND code_mac IS NULL
		AND payload_enc IS NULL AND user_id IS NULL`, []uuid.UUID{sents[1].CodeID, sents[2].CodeID}); n != 2 {
		t.Fatal("занятая почта: нет приманки")
	}
	if mailJobs(t, e, MailSignupCode) != 1 || mailJobs(t, e, MailSignupAccountExists) != 2 {
		t.Fatalf("письма: код %d, «уже есть» %d", mailJobs(t, e, MailSignupCode), mailJobs(t, e, MailSignupAccountExists))
	}
	// в аргументах задачи — только вид и ref (адреса нет)
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args::text LIKE '%@%'`); n != 0 {
		t.Fatal("почта в аргументах задачи")
	}
}

// §9.1: время ответа «почта свободна / занята» на настоящем argon2 — медианы 50 вызовов расходятся меньше чем на 15 %.
//
// serial: порог реального времени.
func TestStartSignupTiming(t *testing.T) {
	e := newEnv(t)
	hasher, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	e.a.hasher = hasher
	city := seedSignupCity(t, e, "XS", true, 14)
	seedUser(t, e.owner, "timing_taken", "ru")
	timed := func(email string) time.Duration {
		start := time.Now()
		if _, err := e.a.StartSignup(context.Background(), signupInput(email, "timing_nick", city), web("x")); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	var free, taken []time.Duration
	for range 50 {
		free = append(free, timed("timing_free@example.com"))
		taken = append(taken, timed("timing_taken@example.com"))
	}
	median := func(ds []time.Duration) time.Duration {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2]
	}
	f, k := median(free), median(taken)
	diff := float64(f-k) / float64(max(f, k))
	if diff < 0 {
		diff = -diff
	}
	if diff >= 0.15 {
		t.Fatalf("медианы: свободна %v, занята %v — расхождение %.0f%%", f, k, diff*100)
	}
}

// signupRisky — риск регистрации с подсети превышен (лимитер риска на базе, порог 1).
func signupRisky(d *identity.Deps) {
	a, err := risk.NewAssessor(ratelimit.NewPG(d.Pool, d.Clock, ratelimit.Players),
		map[risk.Signal]ratelimit.Policy{risk.Signup: {Limit: 1, Period: time.Hour}, risk.LoginFailed: {Limit: 5, Period: time.Hour},
			risk.NewDevice: {Limit: 3, Period: time.Hour}, risk.Activity: {Limit: 120, Period: time.Minute}})
	if err != nil {
		panic(err)
	}
	d.Risk = a
}

// §3.1 п. 2: порядок проверок startSignup — каждая следующая ошибка видна только при прошедших предыдущих; too_young
// ничего не сохраняет (ни кода, ни письма).
func TestStartSignupOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t, signupRisky)
	ctx := context.Background()
	city := seedSignupCity(t, e, "XS", true, 14)
	off := seedSignupCity(t, e, "XT", false, 14)
	other := seedUser(t, e.owner, "order_owner", "ru")
	if _, err := e.owner.Exec(ctx, `INSERT INTO reserved_nicknames (nickname_normalized, reason) VALUES (normalize_text('order_admin'), 'system')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO nickname_reservations (nickname_normalized, previous_user_id, released_at, expires_at)
		VALUES (normalize_text('order_old'), $1, $2, $3)`, other, e.clock.Now(), e.clock.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	full := mobile("ios", "order-full")
	mac, err := e.a.deviceMAC(e.a.devices.CurrentID(), "ios", "order-full")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO devices (id, platform, install_mac, key_id, signups, first_seen_at, last_seen_at)
		VALUES ($1, 'ios', $2, $3, 2, now(), now())`, uuid.New(), mac, e.a.devices.CurrentID()); err != nil {
		t.Fatal(err)
	}
	risky := mobile("ios", "order-risky")
	risky.IP = netip.MustParseAddr("198.51.100.9")
	for range 2 {
		if err := e.a.risk.Record(ctx, risk.Signup, risk.Subnet(risky.IP)); err != nil {
			t.Fatal(err)
		}
	}
	ok := mobile("ios", "order-ok")
	type tc struct {
		name, code string
		in         func(*SignupInput)
		c          Client
		check      func(httpx.Problem) bool
	}
	cases := []tc{
		{"почта", httpx.CodeValidationFailed, func(s *SignupInput) { s.Email = "не почта"; s.Password = "short" }, ok,
			func(p httpx.Problem) bool { return len(p.Errors) == 1 && p.Errors[0].Field == "body.email" }},
		{"дата рождения", httpx.CodeValidationFailed, func(s *SignupInput) { s.BirthDate = testNow.Add(72 * time.Hour); s.Password = "short" }, ok,
			func(p httpx.Problem) bool { return len(p.Errors) == 1 && p.Errors[0].Field == "body.birth_date" }},
		{"пароль", identity.CodePasswordWeak, func(s *SignupInput) { s.Password = "short"; s.Nickname = "a" }, ok,
			func(p httpx.Problem) bool { return len(p.Reasons) == 1 && p.Reasons[0] == "too_short" }},
		{"ник формат", identity.CodeNicknameInvalid, func(s *SignupInput) { s.Nickname = "a"; s.Accepted = nil }, ok, nil},
		{"ник 李小龍", identity.CodeNicknameInvalid, func(s *SignupInput) { s.Nickname = "李小龍"; s.Accepted = nil }, ok, nil},
		{"ник Ἀλέξης", identity.CodeNicknameInvalid, func(s *SignupInput) { s.Nickname = "Ἀλέξης"; s.Accepted = nil }, ok, nil},
		{"ник a.b", identity.CodeNicknameInvalid, func(s *SignupInput) { s.Nickname = "a.b"; s.Accepted = nil }, ok, nil},
		{"документы", identity.CodeDocumentsOutdated, func(s *SignupInput) { s.Accepted = s.Accepted[:1] }, Client{Kind: domain.ClientMobile}, nil},
		{"устройство", identity.CodeDeviceRequired, func(s *SignupInput) { s.CityID = uuid.New() }, Client{Kind: domain.ClientMobile}, nil},
		{"города нет", identity.CodeCityNotFound, func(s *SignupInput) { s.CityID = uuid.New(); s.BirthDate = testNow }, ok, nil},
		{"страна выключена", identity.CodeCityNotFound, func(s *SignupInput) { s.CityID = off; s.BirthDate = testNow }, ok, nil},
		{"возраст", identity.CodeTooYoung, func(s *SignupInput) {
			s.BirthDate = time.Date(2012, 11, 15, 0, 0, 0, 0, time.UTC) // 14 лет — завтра по Москве
			s.Nickname = "order_admin"
		}, ok, nil},
		{"ник в резерве", identity.CodeNicknameTaken, func(s *SignupInput) { s.Nickname = "Order_Admin"; s.Email = "x@mailinator.com" }, ok, nil},
		{"ник в карантине", identity.CodeNicknameTaken, func(s *SignupInput) { s.Nickname = "order_old"; s.Email = "x@mailinator.com" }, ok, nil},
		{"ник занят двойником", identity.CodeNicknameTaken, func(s *SignupInput) { s.Nickname = "оrder_оwner"; s.Email = "x@mailinator.com" }, ok, nil},
		{"одноразовая почта", identity.CodeEmailDisposable, func(s *SignupInput) { s.Email = "x@mailinator.com" }, full, nil},
		{"лимит устройства", identity.CodeDeviceSignupLimit, func(*SignupInput) {}, full, nil},
		{"риск подсети", httpx.CodeHumancheckRequired, func(*SignupInput) {}, risky, nil},
	}
	for _, c := range cases {
		in := signupInput("order_new@example.com", "order_new", city)
		c.in(&in)
		_, err := e.a.StartSignup(ctx, in, c.c)
		p := problemOfErr(err)
		if p.Code != c.code || (c.check != nil && !c.check(p)) {
			t.Errorf("%s: ждали %s, получили %+v (%v)", c.name, c.code, p, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes`); n != 0 {
		t.Fatalf("после отказов строк auth_codes %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send'`); n != 0 {
		t.Fatalf("после отказов писем %d", n)
	}
	// 14 лет исполняется сегодня по Москве (UTC ещё вчера не важен: testNow — полдень UTC) — проходит
	in := signupInput("order_new@example.com", "order_new", city)
	in.BirthDate = time.Date(2012, 11, 14, 0, 0, 0, 0, time.UTC)
	if _, err := e.a.StartSignup(ctx, in, ok); err != nil {
		t.Fatalf("ровно 14: %v", err)
	}
}

// startAndCode — старт регистрации и код из письма.
func startAndCode(t *testing.T, e *env, email, nick string, city uuid.UUID, c Client) (CodeSent, string) {
	t.Helper()
	sent, err := e.a.StartSignup(context.Background(), signupInput(email, nick, city), c)
	if err != nil {
		t.Fatal(err)
	}
	return sent, plainCode(t, e, sent.CodeID)
}

// §3.1 п. 3: подтверждение — аккаунт, пароль, согласия, устройство (signups = 1, signed_up), сессия, событие, аудит,
// журнал безопасности; код погашен.
func TestConfirmSignupHappy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := seedSignupCity(t, e, "XS", true, 14)
	c := mobile("android", "happy-dev")
	sent, code := startAndCode(t, e, "Happy.User@Example.com", "happy_user", city, c)
	out, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, c)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusAuthenticated || out.Issued == nil || out.User.Nickname != "happy_user" {
		t.Fatalf("%+v", out)
	}
	uid := out.User.ID
	for q, want := range map[string]int{
		`SELECT count(*) FROM users WHERE id = $1 AND email = 'happy.user@example.com' AND version = 1 AND status = 'active'
			AND email_verified_at IS NOT NULL AND city_id IS NOT NULL AND locale = 'ru' AND birth_date = '2000-01-01'`: 1,
		`SELECT count(*) FROM credentials WHERE user_id = $1 AND password_hash = 'h:` + signupPassword + `'`:     1,
		`SELECT count(*) FROM consents WHERE user_id = $1 AND version = '2026-11-01' AND client_kind = 'mobile'`: 2,
		`SELECT count(*) FROM device_accounts da JOIN devices d ON d.id = da.device_id
			WHERE da.user_id = $1 AND da.signed_up AND d.signups = 1`: 1,
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND auth_method = 'password' AND revoked_at IS NULL`: 1,
		`SELECT count(*) FROM outbox WHERE event_type = 'identity.user_registered' AND aggregate_id = $1
			AND payload->>'platform' = 'android' AND payload->>'method' = 'password'`: 1,
		`SELECT count(*) FROM audit_log WHERE action = 'identity.user_registered' AND actor_user_id = $1`: 1,
		`SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'signup'`:                     1,
		`SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'login'`:                      0,
	} {
		if n := count(t, e.owner, q, uid); n != want {
			t.Errorf("%s: %d, ждали %d", q, n, want)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND consumed_at IS NOT NULL AND code_enc IS NULL`,
		sent.CodeID); n != 1 {
		t.Fatal("код не погашен")
	}
	if _, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, c); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("повтор: %v", err)
	}
	if _, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, Client{Kind: domain.ClientMobile}); problemCode(err) != identity.CodeDeviceRequired {
		t.Fatalf("без устройства: %v", err)
	}
}

// §3.1 п. 3, §3.14: 4 неверных — 400 с attempts_left 4…1, пятая — 410, верный после — 410; приманка — те же ответы.
func TestConfirmSignupAttempts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := seedSignupCity(t, e, "XS", true, 14)
	seedUser(t, e.owner, "attempts_taken", "ru")
	c := web("x")
	genuine, code := startAndCode(t, e, "attempts_free@example.com", "attempts_new", city, c)
	decoy, err := e.a.StartSignup(context.Background(), signupInput("attempts_taken@example.com", "attempts_new2", city), c)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]uuid.UUID{"настоящий": genuine.CodeID, "приманка": decoy.CodeID} {
		for i := range 4 {
			_, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: id, Code: wrongCode(code)}, c)
			p := problemOfErr(err)
			if p.Code != identity.CodeCodeInvalid || p.Status != 400 || p.AttemptsLeft == nil || *p.AttemptsLeft != 4-i {
				t.Fatalf("%s, неудача %d: %+v", name, i+1, p)
			}
		}
		_, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: id, Code: wrongCode(code)}, c)
		if p := problemOfErr(err); p.Code != identity.CodeCodeExpired || p.Status != 410 {
			t.Fatalf("%s, пятая: %+v", name, p)
		}
		if _, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: id, Code: code}, c); problemCode(err) != identity.CodeCodeExpired {
			t.Fatalf("%s, верный после пятой: %v", name, err)
		}
	}
}

// §3.1 п. 3: ник заняли между стартом и подтверждением — 409 nickname_taken, попытка не потрачена, код жив; повтор с
// новым ником — аккаунт.
func TestConfirmSignupNicknameRace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := seedSignupCity(t, e, "XS", true, 14)
	c := web("x")
	sent, code := startAndCode(t, e, "race_nick@example.com", "race_nick", city, c)
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ('other_race@example.com', now(), 'RACE_NICK', '2000-01-01', 'ru')`); err != nil {
		t.Fatal(err)
	}
	_, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, c)
	if problemCode(err) != identity.CodeNicknameTaken {
		t.Fatalf("ник занят: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND attempts = 0 AND consumed_at IS NULL
		AND revoked_at IS NULL`, sent.CodeID); n != 1 {
		t.Fatal("занятый ник потратил попытку или погасил код")
	}
	if _, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code, Nickname: "a"}, c); problemCode(err) != identity.CodeNicknameInvalid {
		t.Fatalf("новый ник неверного формата: %v", err)
	}
	out, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code, Nickname: "race_nick2"}, c)
	if err != nil || out.User.Nickname != "race_nick2" {
		t.Fatalf("новый ник: %+v %v", out, err)
	}
}

// §3.1 п. 3: второй старт на ту же почту гасит первый код (410); почту заняли напрямую между стартом и
// подтверждением — 23505 users_email_key → 409 email_taken, аккаунта не создано.
func TestConfirmSignupEmailRace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := seedSignupCity(t, e, "XS", true, 14)
	c := web("x")
	first, firstCode := startAndCode(t, e, "race_mail@example.com", "race_one", city, c)
	_, _ = startAndCode(t, e, "race_mail@example.com", "race_two", city, c)
	if _, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: first.CodeID, Code: firstCode}, c); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("первый код после второго старта: %v", err)
	}
	sent, code := startAndCode(t, e, "race_mail2@example.com", "race_three", city, c)
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ('race_mail2@example.com', now(), 'race_other', '2000-01-01', 'ru')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.ConfirmSignup(context.Background(), ConfirmInput{CodeID: sent.CodeID, Code: code}, c); problemCode(err) != identity.CodeEmailTaken {
		t.Fatalf("почта занята: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE nickname = 'race_three'`); n != 0 {
		t.Fatal("аккаунт создан при занятой почте")
	}
}

// §3.14: 6-й старт за час на одну почту — 429 ratelimit.exceeded одинаково для существующей и новой почты.
func TestEmailSendLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, pgLimiters)
	city := seedSignupCity(t, e, "XS", true, 14)
	seedUser(t, e.owner, "limit_taken", "ru")
	for _, email := range []string{"limit_free@example.com", "limit_taken@example.com"} {
		for i := range 6 {
			_, err := e.a.StartSignup(context.Background(), signupInput(email, "limit_nick", city), web("x"))
			if i < 5 {
				if err != nil {
					t.Fatalf("%s, старт %d: %v", email, i+1, err)
				}
				continue
			}
			if p := problemOfErr(err); p.Code != httpx.CodeRateLimited || p.Status != 429 || p.RetryAfter < 1 {
				t.Fatalf("%s, 6-й старт: %+v", email, p)
			}
		}
	}
}

// §5.8: доступность ника — invalid (формат и длина после normalize_text), taken (двойник латиница/кириллица и регистр,
// живой карантин чужого ника), reserved, свободный.
func TestCheckNickname(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	owner := seedUser(t, e.owner, "kosta_10", "ru")
	if _, err := e.owner.Exec(ctx, `INSERT INTO reserved_nicknames (nickname_normalized, reason) VALUES (normalize_text('reserved_nick'), 'system')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.owner.Exec(ctx, `INSERT INTO nickname_reservations (nickname_normalized, previous_user_id, released_at, expires_at)
		VALUES (normalize_text('old_nick'), $1, $2, $3), (normalize_text('gone_nick'), $1, $2, $2),
		       (normalize_text('orphan_nick'), NULL, $2, $3)`, owner,
		e.clock.Now(), e.clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for nick, want := range map[string]string{
		"a": "invalid", "李小龍": "invalid", "a.b": "invalid", "deleted-123": "invalid",
		"КОSТА_10": "taken", "Kosta_10": "taken", "old_nick": "taken", "orphan_nick": "taken",
		"Reserved_Nick": "reserved",
		"free_nick":     "", "gone_nick": "",
	} {
		ok, reason, err := e.a.CheckNickname(ctx, nick)
		if err != nil || ok != (want == "") || reason != want {
			t.Errorf("%q: available %v, reason %q, %v; ждали %q", nick, ok, reason, err, want)
		}
	}
}

// getLegalDocuments — текущие документы конфига.
func TestLegalDocuments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	docs := e.a.LegalDocuments()
	if len(docs) != 2 || docs[0].Document != domain.DocTerms || docs[1].Version != "2026-11-01" {
		t.Fatalf("%+v", docs)
	}
}
