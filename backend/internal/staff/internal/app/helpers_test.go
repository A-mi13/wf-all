package app_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/locales"
)

const (
	testPassword = "Жёлтая карточка на 90-й" //nolint:gosec // G101: пароль тестовой учётки
	testOrigin   = "https://admin.wf.example"
)

var (
	t0      = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	ipHome  = netip.MustParseAddr("203.0.113.10")
	metaWeb = staff.Meta{IP: ipHome, UserAgent: "Mozilla/5.0 test"}

	// argon2 считает долго — хэшер и хэш пароля один на пакет
	sharedHasher = sync.OnceValues(func() (*password.Hasher, error) {
		return password.NewHasher(password.DefaultParams(), 4)
	})
	sharedHash = sync.OnceValues(func() (string, error) {
		h, err := sharedHasher()
		if err != nil {
			return "", err
		}
		return h.Hash(context.Background(), testPassword)
	})
)

// env — сценарии staff на чистой базе под ролью admin (права прода), часы — фальшивые.
type env struct {
	t     *testing.T
	owner *pgxpool.Pool // суперпользователь: подготовка и проверки
	clock *clocktest.Fake
	ring  *keys.Ring
	app   *app.App
	deps  staff.Deps // зависимости app: сценарии с другим кольцом ключей (rekey)
}

// withRing — сценарии на той же базе и часах, но с кольцом ring (ротация ключей).
func (e *env) withRing(ring *keys.Ring) *app.App {
	d := e.deps
	d.Keys = ring
	return app.New(d)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "admin")
	clk := clocktest.New(t0)
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	ring, err := keys.NewRing([]string{base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	h, err := sharedHasher()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := river.NewClient(riverpgxv5.New(pools.As), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	d := staff.Deps{
		Pool: pools.As, Clock: clk, Keys: ring, Hasher: h, Limiter: ratelimit.NewPG(pools.As, clk),
		River: rc, Rand: rand.Reader, Origin: testOrigin, Locales: cat, Log: slog.New(slog.DiscardHandler),
	}
	return &env{t: t, owner: pools.Owner, clock: clk, ring: ring, app: app.New(d), deps: d}
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.owner.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// member — сотрудник теста.
type member struct {
	ID     uuid.UUID
	Email  string
	Secret []byte   // секрет TOTP (nil — второй фактор не настроен)
	Codes  []string // резервные коды как выданы (xxxxx-xxxxx)
}

// memberOpts — что завести: роль, статус, язык, второй фактор, города модератора.
type memberOpts struct {
	Role, Status, Locale, Name string
	NoMFA                      bool
	Cities                     []uuid.UUID
	PasswordHash               string // пусто — хэш testPassword
}

func (e *env) addMember(email string, o memberOpts) member {
	e.t.Helper()
	if o.Role == "" {
		o.Role = "admin"
	}
	if o.Status == "" {
		o.Status = "active"
	}
	if o.Locale == "" {
		o.Locale = "ru"
	}
	if o.Name == "" {
		o.Name = "Сотрудник " + strings.Split(email, "@")[0]
	}
	if o.PasswordHash == "" {
		h, err := sharedHash()
		if err != nil {
			e.t.Fatal(err)
		}
		o.PasswordHash = h
	}
	m := member{ID: id.New(), Email: email}
	e.exec(`INSERT INTO staff_accounts (id, email, display_name, locale, status, password_hash, created_at)
		VALUES ($1, $2, $3, $4, $5::staff_status, $6, $7)`, m.ID, email, o.Name, o.Locale, o.Status, o.PasswordHash, t0.Add(-24*time.Hour))
	// роль и города — одним оператором: отложенный триггер «модератор ⇔ хотя бы один город»
	// проверяет их вместе на коммите
	e.exec(`WITH r AS (INSERT INTO staff_roles (staff_id, role, granted_at) VALUES ($1, $2::staff_role, $3))
		INSERT INTO staff_role_cities (staff_id, city_id) SELECT $1, unnest($4::uuid[])`,
		m.ID, o.Role, t0.Add(-24*time.Hour), o.Cities)
	if o.NoMFA {
		return m
	}
	secret, err := domain.NewTOTPSecret(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	ct, keyID, err := e.app.SealTOTP(m.ID, secret)
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO staff_mfa (staff_id, secret_enc, key_id, confirmed_at) VALUES ($1, $2, $3, $4)`,
		m.ID, ct, keyID, t0.Add(-23*time.Hour))
	codes, err := domain.NewRecoveryCodes(rand.Reader)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, c := range codes {
		norm, ok := domain.NormalizeRecoveryCode(c)
		if !ok {
			e.t.Fatalf("код %q", c)
		}
		keyID := e.ring.CurrentID()
		mac, err := e.app.RecoveryMAC(keyID, m.ID, norm)
		if err != nil {
			e.t.Fatal(err)
		}
		e.exec(`INSERT INTO staff_recovery_codes (staff_id, code_mac, key_id) VALUES ($1, $2, $3)`, m.ID, mac, keyID)
	}
	m.Secret, m.Codes = secret, codes
	return m
}

// totp — код текущего шага фальшивых часов.
func (e *env) totp(m member) string {
	return domain.TOTPCode(m.Secret, domain.TOTPStep(e.clock.Now()))
}

// login — шаг пароля; падение теста при ошибке.
func (e *env) login(ctx context.Context, m member) string {
	e.t.Helper()
	res, err := e.app.Login(ctx, m.Email, testPassword, metaWeb)
	if err != nil {
		e.t.Fatalf("вход %s: %v", m.Email, err)
	}
	return res.Token
}

// load — сотрудник по токену (как конвейер: IP клиента, не фон).
func (e *env) load(tok string) *auth.Staff {
	e.t.Helper()
	s, err := e.app.LoadStaff(context.Background(), tok, ipHome, false)
	if err != nil {
		e.t.Fatalf("LoadStaff: %v", err)
	}
	return s
}

// signIn — вход целиком: пароль и TOTP; токен активной сессии.
func (e *env) signIn(m member) string {
	e.t.Helper()
	ctx := context.Background()
	pending := e.load(e.login(ctx, m))
	tok, err := e.app.VerifyMFA(ctx, pending, e.totp(m), "", metaWeb)
	if err != nil {
		e.t.Fatalf("второй фактор: %v", err)
	}
	return tok
}

// city — город для модератора (справочник geo — только внешний ключ staff_role_cities).
func (e *env) city(slug string) uuid.UUID {
	e.t.Helper()
	var cid uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO cities (country_id, name, slug, timezone, centroid)
		SELECT id, $1, $1, 'Europe/Moscow', ST_SetSRID(ST_MakePoint(37.6, 55.7), 4326)::geography
		FROM countries WHERE code = 'RU' RETURNING id`, slug).Scan(&cid); err != nil {
		e.t.Fatal(err)
	}
	return cid
}

// pgxTx — транзакция в сигнатуре db.TxHook.
type pgxTx = pgx.Tx

func netip4(s string) netip.Addr { return netip.MustParseAddr(s) }

// staffMeta — запрос с адреса ip.
func staffMeta(ip netip.Addr) staff.Meta { return staff.Meta{IP: ip, UserAgent: metaWeb.UserAgent} }

// auditRow — запись журнала для проверок.
type auditRow struct {
	Action, Role string
	StaffID      *uuid.UUID
	Label        *string
	ObjectID     *uuid.UUID
	After        string
}

func (e *env) audits(action string) []auditRow {
	e.t.Helper()
	rows, err := e.owner.Query(context.Background(), `SELECT action, actor_role, actor_staff_id, actor_label, object_id,
		coalesce(after::text, '') FROM audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.Action, &r.Role, &r.StaffID, &r.Label, &r.ObjectID, &r.After); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}
