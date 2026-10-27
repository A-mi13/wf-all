package admin_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/staff"
	staffadmin "wf/backend/internal/staff/admin"
	"wf/backend/locales"
)

// opEnv — хендлер staff на чистой базе под ролью admin, как его собирает cmd/admin-api для подкоманд.
type opEnv struct {
	t     *testing.T
	owner *pgxpool.Pool
	deps  staff.Deps
}

func newKey(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func newOpEnv(t *testing.T, ringKeys ...string) *opEnv {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "admin")
	ring, err := keys.NewRing(ringKeys)
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
	return &opEnv{t: t, owner: pools.Owner, deps: staff.Deps{
		Pool: pools.As, Clock: clock.System, Keys: ring, Limiter: ratelimit.Unlimited, River: rc, Rand: rand.Reader,
		Origin: "https://admin.wf.example", Locales: cat, Log: slog.New(slog.DiscardHandler),
	}}
}

// handler — собранный хендлер с кольцом ring (по умолчанию — кольцо env).
func (e *opEnv) handler(ring *keys.Ring) *staffadmin.Handler {
	d := e.deps
	if ring != nil {
		d.Keys = ring
	}
	h := staffadmin.New(d, staffadmin.Cookie{Name: "__Host-wf_admin_session", Secure: true})
	reg := approvals.NewRegistry()
	h.RegisterApprovals(reg, approvals.New(d.Pool, d.Clock, reg))
	if err := reg.Validate(); err != nil {
		e.t.Fatal(err)
	}
	return h
}

// liveCode — код живого приглашения учётки email, расшифрованный кольцом env (aad — id приглашения).
func (e *opEnv) liveCode(email string) string {
	e.t.Helper()
	var iid uuid.UUID
	var ct []byte
	var kid string
	if err := e.owner.QueryRow(context.Background(), `SELECT i.id, i.code_enc, i.code_key_id FROM staff_invites i
		JOIN staff_accounts a ON a.id = i.staff_id
		WHERE a.email = $1 AND i.consumed_at IS NULL AND i.revoked_at IS NULL`, email).Scan(&iid, &ct, &kid); err != nil {
		e.t.Fatal(err)
	}
	code, err := e.deps.Keys.Open(ct, kid, iid[:])
	if err != nil {
		e.t.Fatal(err)
	}
	return string(code)
}

var codeFormat = regexp.MustCompile(`^[a-z2-7]{5}-[a-z2-7]{5}$`)

// Обёртки подкоманд (§4.2): код приглашения отдаётся наружу только с printCode — без него вызывающий кода
// не получает вовсе (не может случайно напечатать или записать в лог); с ним — настоящий код живого
// приглашения. Ошибки сценариев — как есть (errors.Is для подсказок команды).
func TestOperatorWrappersPrintCode(t *testing.T) {
	e := newOpEnv(t, newKey(t))
	h := e.handler(nil)
	ctx := context.Background()
	// одни и те же учётки: bootstrap заводит, invite-resend и reset-access выпускают им новые коды
	for _, c := range []struct {
		name string
		run  func(email string, show bool) (string, error)
	}{
		{"bootstrap", func(email string, p bool) (string, error) {
			return h.OperatorBootstrap(ctx, email, "Владелец", p)
		}},
		{"invite-resend", func(email string, p bool) (string, error) {
			return h.OperatorInviteResend(ctx, email, p)
		}},
		{"reset-access", func(email string, p bool) (string, error) {
			return h.OperatorResetAccess(ctx, email, p)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for i, p := range []bool{false, true} {
				email := []string{"o1@wf.example", "o2@wf.example"}[i]
				code, err := c.run(email, p)
				if err != nil {
					t.Fatalf("printCode=%v: %v", p, err)
				}
				if !p && code != "" {
					t.Fatalf("без printCode отдан код %q", code)
				}
				if p && (!codeFormat.MatchString(code) || code != e.liveCode(email)) {
					t.Fatalf("с printCode отдан %q, живой код другой", code)
				}
			}
		})
	}
	if _, err := h.OperatorBootstrap(ctx, "o1@wf.example", "Повтор", true); !errors.Is(err, staff.ErrEmailTaken) {
		t.Fatalf("повторный bootstrap той же почты: %v", err)
	}
	if _, err := h.OperatorInviteResend(ctx, "nobody@wf.example", true); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("invite-resend несуществующей: %v", err)
	}
	// последний активный владелец (стража — сценарий; обёртка не прячет ошибку)
	e.exec(`UPDATE staff_accounts SET status = 'active', password_hash = '$argon2id$stub' WHERE email = 'o1@wf.example'`)
	if err := h.OperatorDisable(ctx, "o1@wf.example"); !errors.Is(err, staff.ErrLastOwner) {
		t.Fatalf("disable последнего владельца: %v", err)
	}
}

func (e *opEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.owner.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

// OperatorRekey отдаёт все поля итога сценария: перешифровано, осталось на старых ключах, нечитаемые id.
// Ключ убран раньше rekey — приглашение нечитаемо (id в UnreadableInvites, InvitesOnOldKeys = 1); с новым
// ключом первым и старым вторым — перешифровано (Invites = 1, на старых — 0).
func TestOperatorRekeyReport(t *testing.T) {
	oldKey, newKeyB64 := newKey(t), newKey(t)
	e := newOpEnv(t, oldKey)
	ctx := context.Background()
	if _, err := e.handler(nil).OperatorBootstrap(ctx, "o@wf.example", "Владелец", false); err != nil {
		t.Fatal(err)
	}
	var iid, sid uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT id, staff_id FROM staff_invites`).Scan(&iid, &sid); err != nil {
		t.Fatal(err)
	}
	// секрет TOTP старым ключом (aad — id сотрудника) и резервный код на старом ключе
	ct, kid, err := e.deps.Keys.Seal([]byte("12345678901234567890"), sid[:])
	if err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO staff_mfa (staff_id, secret_enc, key_id) VALUES ($1, $2, $3)`, sid, ct, kid)
	e.exec(`INSERT INTO staff_recovery_codes (staff_id, code_mac, key_id) VALUES ($1, decode(repeat('ab', 32), 'hex'), $2)`, sid, kid)

	onlyNew, err := keys.NewRing([]string{newKeyB64})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := e.handler(onlyNew).OperatorRekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.MFA != 0 || rep.Invites != 0 || rep.MFAOnOldKeys != 1 || rep.InvitesOnOldKeys != 1 || rep.RecoveryOnOldKeys != 1 ||
		!slices.Equal(rep.UnreadableInvites, []uuid.UUID{iid}) || !slices.Equal(rep.UnreadableMFA, []uuid.UUID{sid}) || rep.Clean() {
		t.Fatalf("ключ убран раньше rekey: %+v", rep)
	}
	both, err := keys.NewRing([]string{newKeyB64, oldKey})
	if err != nil {
		t.Fatal(err)
	}
	rep, err = e.handler(both).OperatorRekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.MFA != 1 || rep.Invites != 1 || rep.MFAOnOldKeys != 0 || rep.InvitesOnOldKeys != 0 || rep.RecoveryOnOldKeys != 1 ||
		len(rep.UnreadableInvites)+len(rep.UnreadableMFA) != 0 || rep.Clean() {
		t.Fatalf("новый ключ первым: %+v", rep)
	}
	e.exec(`DELETE FROM staff_recovery_codes`)
	if rep, err = e.handler(both).OperatorRekey(ctx); err != nil || !rep.Clean() {
		t.Fatalf("всё на новом ключе: %+v, %v", rep, err)
	}
}

// Несобранный хендлер (нулевой) операций оператора не исполняет — ошибка, без паники.
func TestOperatorZeroHandler(t *testing.T) {
	h := &staffadmin.Handler{}
	if _, err := h.OperatorBootstrap(context.Background(), "o@wf.example", "Владелец", true); err == nil {
		t.Fatal("нулевой хендлер исполнил bootstrap")
	}
	if _, err := h.OperatorRekey(context.Background()); err == nil {
		t.Fatal("нулевой хендлер исполнил rekey")
	}
}
