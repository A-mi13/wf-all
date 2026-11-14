package app

import (
	"context"
	"log/slog"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/platform/token"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// testNow — начало часов теста (UTC, секунды без дробей: сравнение с timestamptz без округлений).
var testNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// env — сценарии на клоне базы под ролью api: owner — подготовка и проверки, app — пул роли.
type env struct {
	a       *App
	owner   *pgxpool.Pool
	clock   *clocktest.Fake
	devKeys []string // ключи кольца устройств, текущий первым
	tokens  *token.Issuer
}

type envOpt func(*identity.Deps)

func newRing(t *testing.T, encoded ...string) *keys.Ring {
	t.Helper()
	r, err := keys.NewRing(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func genKey(t *testing.T) string {
	t.Helper()
	k, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// fakeHasher — хэшер без argon2: хэш — «h:» + пароль; ждёт gate, если он задан (argon2 вне tx, Task 9).
type fakeHasher struct {
	mu      sync.Mutex
	inside  int
	entered chan struct{} // сигнал «вошёл в Verify»
	gate    chan struct{} // nil — без ожидания
}

func (h *fakeHasher) Hash(_ context.Context, pw string) (string, error) { return "h:" + pw, nil }

func (h *fakeHasher) Verify(ctx context.Context, pw, encoded string) (bool, bool, error) {
	if h.gate != nil {
		h.mu.Lock()
		h.inside++
		h.mu.Unlock()
		h.entered <- struct{}{}
		select {
		case <-h.gate:
		case <-ctx.Done():
			return false, false, ctx.Err()
		}
	}
	if encoded == "old:"+pw {
		return true, true, nil // старые параметры — пересчитать
	}
	return encoded == "h:"+pw, false, nil
}

func (h *fakeHasher) VerifyDummy(context.Context, string) error { return nil }

// fakeHuman — PoW без решения: в тестах сценариев риск не превышается.
type fakeHuman struct{}

func (fakeHuman) NewChallenge(context.Context) (humancheck.Challenge, error) {
	return humancheck.Challenge{}, nil
}

func (fakeHuman) Verify(context.Context, string) error { return nil }

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "api")
	c := clocktest.New(testNow)
	devKey := genKey(t)
	seed := genKey(t)
	tk, err := token.ParseKeys([]string{seed})
	if err != nil {
		t.Fatal(err)
	}
	tokens := token.NewIssuer(tk, c)
	legal, err := identity.ParseLegalDocuments("terms=2026-11-01|https://example.com/terms,privacy=2026-11-01|https://example.com/privacy")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pools.As), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	assessor, err := risk.NewAssessor(ratelimit.Unlimited, risk.DefaultPolicies())
	if err != nil {
		t.Fatal(err)
	}
	d := identity.Deps{Pool: pools.As, Clock: c, Secrets: newRing(t, genKey(t)), Devices: newRing(t, devKey),
		Hasher: &fakeHasher{}, Limiter: ratelimit.Unlimited, Risk: assessor, Human: fakeHuman{},
		Geo: geo.New(pools.As), Flags: flags.NewStore(pools.As), IPGeo: ipgeo.None, Legal: legal, Tokens: tokens,
		River: ins, Attestor: identity.AttestOff{}, Providers: identity.NoProviders{}, TOTPIssuer: "WF test", Log: log}
	for _, o := range opts {
		o(&d)
	}
	a, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return &env{a: a, owner: pools.Owner, clock: c, devKeys: []string{devKey}, tokens: tokens}
}

// seedUser — аккаунт с почтой, ником и языком; id — из базы.
func seedUser(t *testing.T, owner *pgxpool.Pool, nick, locale string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := owner.QueryRow(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ($1, now(), $2, '2000-01-01', $3) RETURNING id`, nick+"@example.com", nick, locale).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// inTx — fn в транзакции сценариев (db.InTx на пуле роли api).
func (e *env) inTx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx) error) error {
	t.Helper()
	return db.InTx(context.Background(), e.a.pool, fn)
}

// mobile — мобильный клиент с устройством.
func mobile(platform, installID string) Client {
	return Client{Kind: domain.ClientMobile, IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "WF/1.4 iOS",
		Device: &identity.DeviceInput{Platform: platform, InstallID: installID, Model: "iPhone15,2", OSVersion: "18.1",
			AppVersion: "1.4"}}
}

// web — браузер через BFF с меткой устройства (пусто — без метки).
func web(label string) Client {
	return Client{Kind: domain.ClientWeb, IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "Mozilla/5.0",
		BFFDevice: label}
}

// pairBarrier — барьер двух участников: каждый ждёт второго не дольше wait (при верном порядке замков второй стоит
// на замке, и барьер истекает).
func pairBarrier(wait time.Duration) func() {
	var mu sync.Mutex
	n := 0
	both := make(chan struct{})
	return func() {
		mu.Lock()
		n++
		if n == 2 {
			close(both)
		}
		mu.Unlock()
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-both:
		case <-t.C:
		}
	}
}

// count — одно число запросом владельца.
func count(t *testing.T, owner *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
