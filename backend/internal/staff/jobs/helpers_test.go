package jobs_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
	"wf/backend/locales"
)

const testOrigin = "https://admin.wf.example"

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// syncBuffer — журнал сервера теста: пишут горутины River и тикера.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// env — фон staff на чистой базе под ролью admin; сценарии (app) — чтобы завести настоящие сущности писем.
type env struct {
	t     *testing.T
	owner *pgxpool.Pool
	clock *clocktest.Fake
	ring  *keys.Ring
	deps  staff.Deps
	app   *app.App
	svc   *approvals.Service
	log   *syncBuffer
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
	rc, err := river.NewClient(riverpgxv5.New(pools.As), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	log := &syncBuffer{}
	d := staff.Deps{
		Pool: pools.As, Clock: clk, Keys: ring, Limiter: ratelimit.Unlimited, River: rc, Rand: rand.Reader,
		Origin: testOrigin, Locales: cat, Log: slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	a := app.New(d)
	reg := approvals.NewRegistry()
	svc := approvals.New(pools.As, clk, reg)
	a.RegisterApprovals(reg, svc)
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, owner: pools.Owner, clock: clk, ring: ring, deps: d, app: a, svc: svc, log: log}
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

// member — учётка с ролью и статусом (без второго фактора: письмам он не нужен).
func (e *env) member(email, role, status, locale string) uuid.UUID {
	e.t.Helper()
	sid := id.New()
	// хэш-заглушка: письмам пароль не нужен, CHECK требует его у active
	e.exec(`INSERT INTO staff_accounts (id, email, display_name, locale, status, created_at, password_hash)
		VALUES ($1, $2, $3, $4, $5::staff_status, $6, '$argon2id$stub')`, sid, email, "Сотрудник "+strings.Split(email, "@")[0], locale, status, t0.Add(-24*time.Hour))
	e.exec(`INSERT INTO staff_roles (staff_id, role, granted_at) VALUES ($1, $2::staff_role, $3)`, sid, role, t0.Add(-24*time.Hour))
	return sid
}

// mails — задачи staff.mail, поставленные сценариями (аргументы как в river_job), в порядке вставки.
func (e *env) mails() []staff.MailArgs {
	e.t.Helper()
	rows, err := e.owner.Query(context.Background(), `SELECT args FROM river_job WHERE kind = 'staff.mail' ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []staff.MailArgs
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			e.t.Fatal(err)
		}
		var a staff.MailArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// mailOf — единственная задача вида kind получателю recipient.
func (e *env) mailOf(kind string, recipient uuid.UUID) staff.MailArgs {
	e.t.Helper()
	var found []staff.MailArgs
	for _, a := range e.mails() {
		if a.Mail == kind && a.Recipient == recipient {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		e.t.Fatalf("задач %s получателю %s: %d", kind, recipient, len(found))
	}
	return found[0]
}

// job — задача River с аргументами a (id задачи — для журнала сервера).
func job(a staff.MailArgs) *river.Job[staff.MailArgs] {
	return &river.Job[staff.MailArgs]{Args: a, JobRow: &rivertype.JobRow{ID: 42, Kind: "staff.mail"}}
}
