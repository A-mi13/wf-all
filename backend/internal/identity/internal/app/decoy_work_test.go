package app

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity"
)

// sqlRecorder — трассировщик пула сценариев: тексты запросов по порядку (BEGIN и COMMIT pgx тоже трассирует).
type sqlRecorder struct {
	mu  sync.Mutex
	sql []string
}

func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	r.sql = append(r.sql, strings.Join(strings.Fields(d.SQL), " "))
	r.mu.Unlock()
	return ctx
}

func (r *sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// take — записанное с прошлого вызова; запись начинается заново.
func (r *sqlRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.sql
	r.sql = nil
	return out
}

// withRecorder — пул сценариев с трассировщиком (те же роль и настройки, что у пула окружения).
func withRecorder(t *testing.T, r *sqlRecorder) envOpt {
	return func(d *identity.Deps) {
		cfg := d.Pool.Config().Copy()
		cfg.ConnConfig.Tracer = r
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		d.Pool = pool
	}
}

// §9.1 по устройству, а не по секундомеру: неудачный вход на существующую почту и на несуществующую делает одну и ту же
// работу с базой — те же запросы в том же порядке и одну закоммиченную транзакцию (запись в журнал); разница — только
// строки security_events существующего аккаунта, и они — внутри той же транзакции (между begin и commit: вынесенная в
// автокоммит вставка дала бы второй fsync). Ветки deleted и «срок отмены удаления вышел» (purgeDue) — как «нет почты».
// Без транзакции у «нет почты» коммит с fsync выдавал существующую почту по времени ответа (CI main 18.11: медианы
// 34 мс против 25 мс, TestLoginTiming); секундомер ловит это только на медленном диске.
func TestLoginFailureSameWork(t *testing.T) {
	t.Parallel()
	r := &sqlRecorder{}
	e := newEnv(t, withRecorder(t, r))
	loginUser(t, e, "work_pass")
	seedUser(t, e.owner, "work_nopass", "ru") // аккаунт без пароля (только соцвход) — тоже «нет пароля»
	setStatus(t, e, loginUser(t, e, "work_gone"), "deleted")
	due := loginUser(t, e, "work_due")
	if _, err := e.owner.Exec(context.Background(), `UPDATE users SET status = 'pending_deletion',
		status_before_deletion = 'active', deletion_requested_at = $2, purge_at = $3 WHERE id = $1`, due,
		testNow.Add(-31*24*time.Hour), testNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// work — запросы неудачного входа (SET ROLE — настройка нового соединения пулом, а не работа сценария) и число
	// строк security_events; каждая из них — внутри транзакции
	work := func(email string) ([]string, int) {
		t.Helper()
		r.take()
		if _, err := e.a.Login(ctx, email, "неверный пароль", iphone); problemCode(err) != identity.CodeInvalidCredentials {
			t.Fatalf("%s: %v", email, err)
		}
		var out []string
		sec, inTx := 0, false
		for _, q := range r.take() {
			switch {
			case strings.HasPrefix(q, "SET ROLE"):
				continue
			case q == "begin":
				inTx = true
			case q == "commit" || q == "rollback":
				inTx = false
			case strings.Contains(q, "security_events"):
				if !inTx {
					t.Fatalf("%s: security_events вне транзакции неудачи: %q", email, q)
				}
				sec++
				continue
			}
			out = append(out, q)
		}
		return out, sec
	}
	unknown, sec := work("nobody_work@example.com")
	if !slices.Contains(unknown, "commit") || sec != 0 {
		t.Fatalf("нет почты — без закоммиченной транзакции или с security_events (%d): %q", sec, unknown)
	}
	for _, c := range []struct {
		email string
		sec   int
	}{
		{"work_pass@example.com", 1},
		{"work_nopass@example.com", 1},
		{"work_gone@example.com", 0},
		{"work_due@example.com", 0},
	} {
		got, sec := work(c.email)
		if !slices.Equal(got, unknown) || sec != c.sec {
			t.Fatalf("%s — другая работа с базой, чем у несуществующей почты (security_events %d, ждали %d):\n есть: %q\n нет:  %q",
				c.email, sec, c.sec, got, unknown)
		}
	}
}

// work — запросы сценария fn на пуле сценариев без SET ROLE (настройка нового соединения пулом).
func (r *sqlRecorder) work(fn func()) []string {
	r.take()
	fn()
	return slices.DeleteFunc(r.take(), func(s string) bool { return strings.HasPrefix(s, "SET ROLE") })
}

// §9.1 для кодов: настоящий код и приманка делают одну и ту же работу с базой в одной транзакции — повтор отправки
// (замок аккаунта строки и задача письма: у приманки — та же задача, её отбрасывает сборщик письма, «код другого
// вида»: R26 — письма нет) и неверный код сброса (замок аккаунта строки). Иначе лишние запросы настоящего кода выдают
// существование почты по времени ответа (ревью fix/login-timing, 18.11).
func TestCodeDecoySameWork(t *testing.T) {
	t.Parallel()
	r := &sqlRecorder{}
	e := newEnv(t, withRecorder(t, r))
	loginUser(t, e, "decoy_work")
	city := seedSignupCity(t, e, "DW", true, 14)
	ctx := context.Background()
	resend := func(id uuid.UUID) []string {
		t.Helper()
		e.clock.Advance(time.Minute)
		return r.work(func() {
			if _, err := e.a.ResendCode(ctx, id); err != nil {
				t.Fatalf("повтор: %v", err)
			}
		})
	}
	same := func(name string, genuine, decoy []string) {
		t.Helper()
		if !slices.Contains(genuine, "commit") || !slices.Equal(genuine, decoy) {
			t.Fatalf("%s — настоящий код и приманка работают с базой по-разному:\n код:      %q\n приманка: %q",
				name, genuine, decoy)
		}
	}

	resetReal := startReset(t, e, "decoy_work@example.com")
	resetDecoy := startReset(t, e, "nobody_decoy@example.com")
	same("повтор сброса", resend(resetReal.CodeID), resend(resetDecoy.CodeID))

	signup := func(email, nick string) CodeSent {
		t.Helper()
		sent, err := e.a.StartSignup(ctx, signupInput(email, nick, city), mobile("ios", "dw-"+nick))
		if err != nil {
			t.Fatalf("%s: %v", email, err)
		}
		return sent
	}
	signupReal := signup("free_decoy@example.com", "dw_free")
	signupDecoy := signup("decoy_work@example.com", "dw_taken")
	same("повтор регистрации", resend(signupReal.CodeID), resend(signupDecoy.CodeID))

	wrong := func(sent CodeSent) []string {
		t.Helper()
		return r.work(func() {
			if _, err := confirmReset(e, sent, "000000", nil, resetNewPassword); problemCode(err) != identity.CodeCodeInvalid {
				t.Fatalf("неверный код: %v", err)
			}
		})
	}
	same("неверный код сброса", wrong(resetReal), wrong(resetDecoy))
}
