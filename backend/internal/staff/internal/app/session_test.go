package app_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// seen — last_seen_at и ip сессии.
func (e *env) seen(sessionID uuid.UUID) (time.Time, netip.Addr) {
	e.t.Helper()
	var (
		at time.Time
		ip netip.Addr
	)
	if err := e.owner.QueryRow(context.Background(), `SELECT last_seen_at, ip FROM staff_sessions WHERE id = $1`,
		sessionID).Scan(&at, &ip); err != nil {
		e.t.Fatal(err)
	}
	return at.UTC(), ip
}

// Абсолютный срок 8 ч: дальше — ErrSessionInvalid, сессия гасится как expired.
func TestSessionAbsoluteExpiry(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.signIn(m)
	sid := e.load(tok).SessionID
	for range 8 * 3 { // каждые 20 минут — без Reauth, но абсолютный срок идёт
		e.clock.Advance(20 * time.Minute)
		if e.clock.Now().Before(t0.Add(8 * time.Hour)) {
			e.load(tok)
		}
	}
	if _, err := e.app.LoadStaff(context.Background(), tok, ipHome, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("через 8 ч: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE id = $1 AND revoked_reason = 'expired' AND revoked_at = $2`,
		sid, t0.Add(8*time.Hour)); n != 1 {
		t.Fatal("сессия не погашена как expired")
	}
}

// Бездействие больше 20 минут → Reauth (ровно 20 — ещё нет); права остаются в Grants, но Decide
// под Reauth — Deny.
func TestSessionIdleReauth(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{Role: "owner"})
	tok := e.signIn(m)
	e.clock.Advance(20 * time.Minute)
	if s := e.load(tok); s.Reauth {
		t.Fatal("ровно 20 минут — уже Reauth")
	}
	e.clock.Advance(20*time.Minute + time.Second)
	s := e.load(tok)
	if !s.Reauth || s.Decide("staff.accounts.read", uuid.Nil) != auth.Deny {
		t.Fatalf("бездействие > 20 мин: %+v", s)
	}
}

// Смена сети: IPv4 вне /24, IPv6 вне /48 → Reauth; в пределах префикса — нет.
func TestSessionNetworkChange(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		reauth   bool
	}{
		{"v4 в пределах /24", "203.0.113.10", "203.0.113.250", false},
		{"v4 другая /24", "203.0.113.10", "203.0.114.10", true},
		{"v6 в пределах /48", "2001:db8:1::1", "2001:db8:1:ffff::2", false},
		{"v6 другая /48", "2001:db8:1::1", "2001:db8:2::1", true},
		{"v4 → v6", "203.0.113.10", "2001:db8:1::1", true},
		{"v4 и v4 в v6", "203.0.113.10", "::ffff:203.0.113.99", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			m := e.addMember("olga.v@wf.example", memberOpts{})
			from := netip.MustParseAddr(c.from)
			pending, err := e.app.Login(context.Background(), m.Email, testPassword, staffMeta(from))
			if err != nil {
				t.Fatal(err)
			}
			ps, err := e.app.LoadStaff(context.Background(), pending.Token, from, false)
			if err != nil {
				t.Fatal(err)
			}
			tok, err := e.app.VerifyMFA(context.Background(), ps, e.totp(m), "", staffMeta(from))
			if err != nil {
				t.Fatal(err)
			}
			s, err := e.app.LoadStaff(context.Background(), tok, netip.MustParseAddr(c.to), false)
			if err != nil || s.Reauth != c.reauth {
				t.Fatalf("Reauth %v, ждали %v (%v)", s.Reauth, c.reauth, err)
			}
		})
	}
}

// Review Focus 2, P11, D10: last_seen_at — не чаще раза в минуту и не на фоновых запросах; фоновый
// опрос раз в минуту 25 минут приводит к Reauth; в Reauth ни last_seen_at, ни ip не меняются.
func TestSessionLastSeen(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.signIn(m)
	sid := e.load(tok).SessionID
	near := netip.MustParseAddr("203.0.113.77")

	e.clock.Advance(59 * time.Second)
	e.load(tok)
	if at, _ := e.seen(sid); !at.Equal(t0) {
		t.Fatalf("обновлено раньше минуты: %v", at)
	}
	e.clock.Advance(time.Second)
	if _, err := e.app.LoadStaff(context.Background(), tok, near, false); err != nil {
		t.Fatal(err)
	}
	if at, ip := e.seen(sid); !at.Equal(t0.Add(time.Minute)) || ip != near {
		t.Fatalf("через минуту: %v %v", at, ip)
	}

	// фоновые опросы раз в минуту не двигают бездействие
	var s *auth.Staff
	for range 25 {
		e.clock.Advance(time.Minute)
		var err error
		if s, err = e.app.LoadStaff(context.Background(), tok, ipHome, true); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Reauth {
		t.Fatal("фоновые опросы 25 минут — нет Reauth")
	}
	if at, ip := e.seen(sid); !at.Equal(t0.Add(time.Minute)) || ip != near {
		t.Fatalf("фон обновил сессию: %v %v", at, ip)
	}
	// в Reauth обычный запрос с другого адреса — тоже без обновления
	if s := e.load(tok); !s.Reauth {
		t.Fatal("Reauth пропал")
	}
	if at, ip := e.seen(sid); !at.Equal(t0.Add(time.Minute)) || ip != near {
		t.Fatalf("Reauth обновил сессию: %v %v", at, ip)
	}
}

// В стадиях setup и mfa_pending Reauth не ставится (P11): свои сроки — 8 ч и 5 мин.
func TestNoReauthBeforeActive(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	tok := e.login(context.Background(), m)
	e.clock.Advance(4 * time.Minute)
	if s, err := e.app.LoadStaff(context.Background(), tok, netip.MustParseAddr("198.51.100.1"), false); err != nil || s.Reauth {
		t.Fatalf("mfa_pending: %+v %v", s, err)
	}
	// setup: первый вход по приглашению (учётка invited) — сессию заводит Task 7, здесь — строкой
	inv := e.addMember("new@wf.example", memberOpts{Status: "invited", NoMFA: true})
	setupTok := "setup-token-for-test"
	e.exec(`INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, expires_at, ip, user_agent)
		VALUES ($1, $2, sha256($3::bytea), 'setup', $4, $4, $5, '203.0.113.10', '')`,
		uuid.New(), inv.ID, []byte(setupTok), e.clock.Now(), e.clock.Now().Add(8*time.Hour))
	e.clock.Advance(7 * time.Hour)
	s, err := e.app.LoadStaff(context.Background(), setupTok, netip.MustParseAddr("198.51.100.1"), false)
	if err != nil || s.Reauth || s.Stage != auth.StageSetup {
		t.Fatalf("setup через 7 ч из другой сети: %+v %v", s, err)
	}
	if s.Decide("staff.accounts.read", uuid.Nil) != auth.Deny {
		t.Fatal("setup получил право")
	}
}

// Права — perm.Grants(роль) при загрузке; города модератора — из staff_role_cities, у глобальных
// ролей — пусто. Выключенная или закрытая учётка — ErrSessionInvalid.
func TestLoadStaffGrantsAndCities(t *testing.T) {
	e := newEnv(t)
	c1, c2 := e.city("kazan"), e.city("perm")
	mod := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c2, c1}})
	tok := e.signIn(mod)
	s := e.load(tok)
	want := []uuid.UUID{c1, c2}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if s.Role != "moderator" || !slices.Equal(s.CityIDs, want) {
		t.Fatalf("модератор %+v", s)
	}
	if len(s.Grants) != len(perm.Grants(perm.Moderator)) {
		t.Fatalf("права %v", s.Grants)
	}
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	os := e.load(e.signIn(owner))
	if len(os.CityIDs) != 0 || os.Grant("staff.roles.grant") != auth.GrantDual {
		t.Fatalf("владелец %+v", os)
	}
	e.exec(`UPDATE staff_accounts SET status = 'disabled' WHERE id = $1`, mod.ID)
	if _, err := e.app.LoadStaff(context.Background(), tok, ipHome, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("выключенная учётка: %v", err)
	}
	if _, err := e.app.LoadStaff(context.Background(), "чужой токен", ipHome, false); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("неизвестный токен: %v", err)
	}
}
