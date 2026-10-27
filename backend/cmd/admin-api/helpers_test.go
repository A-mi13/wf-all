package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/testkit/dbtest"
)

const testOrigin = "http://127.0.0.1:5173"

// letter — письмо, принятое SMTP-заглушкой.
type letter struct {
	To   string
	Data string
}

// smtpStub — SMTP-сервер теста (без TLS и аутентификации, как Mailpit): принимает письма.
type smtpStub struct {
	ln      net.Listener
	mu      sync.Mutex
	letters []letter
	got     chan letter
}

func newSMTPStub(t *testing.T) *smtpStub {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpStub{ln: ln, got: make(chan letter, 100)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpStub) addr() string { return s.ln.Addr().String() }

func (s *smtpStub) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(line string) { _, _ = io.WriteString(c, line+"\r\n") }
	reply("220 stub ESMTP")
	var to string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-stub")
			reply("250 8BITMIME")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			to = strings.Trim(strings.TrimSpace(line)[len("RCPT TO:"):], "<> \r\n")
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go ahead")
			var data bytes.Buffer
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			l := letter{To: to, Data: data.String()}
			s.mu.Lock()
			s.letters = append(s.letters, l)
			s.mu.Unlock()
			s.got <- l
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// waitLetter — письмо получателю to за 30 с (River берёт задачу по LISTEN или опросу раз в секунду).
func (s *smtpStub) waitLetter(t *testing.T, to string) letter {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case l := <-s.got:
			if l.To == to {
				return l
			}
		case <-deadline:
			t.Fatalf("письмо для %s не пришло за 30 с", to)
		}
	}
}

// testEnv — окружение admin-api теста: своя база (права роли admin из grants.sql), кольцо ключей, SMTP.
type testEnv struct {
	t     *testing.T
	owner *pgxpool.Pool // суперпользователь: подготовка и проверки
	key   string        // ADMIN_STAFF_SECRET_KEYS
	ring  *keys.Ring
	smtp  *smtpStub
	dbURL string // строка admin-api: тот же сервер и база, роль admin
	addr  string // ADMIN_HTTP_ADDR
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "admin") // права ролей применены к базе теста
	u, err := url.Parse(pools.Owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	// admin-api работает под ролью admin, как в проде: соединение суперпользователя сразу SET ROLE admin
	q.Set("options", "-c role=admin")
	// пробел — %20: pgx не читает «+» как пробел
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(k)
	ring, err := keys.NewRing([]string{key})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{t: t, owner: pools.Owner, key: key, ring: ring, smtp: newSMTPStub(t), dbURL: u.String(), addr: freeAddr(t)}
}

// environ — переменные admin-api; extra дописываются последними (перекрывают).
func (e *testEnv) environ(extra ...string) []string {
	return append([]string{
		"ADMIN_HTTP_ADDR=" + e.addr, "ADMIN_DATABASE_URL=" + e.dbURL, "ADMIN_ORIGIN=" + testOrigin,
		"ADMIN_STAFF_SECRET_KEYS=" + e.key, "ADMIN_MAIL_SMTP_ADDR=" + e.smtp.addr(), "ADMIN_MAIL_FROM=WF <noreply@wf.local>",
		"ADMIN_MAIL_SMTP_TLS=none", "ADMIN_LOG_FORMAT=text", "ADMIN_SHUTDOWN_TIMEOUT=10s",
	}, extra...)
}

func (e *testEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.owner.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func (e *testEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.owner.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// liveCode — код живого приглашения учётки email (расшифровка кольцом теста, aad — id приглашения).
func (e *testEnv) liveCode(email string) string {
	e.t.Helper()
	var iid uuid.UUID
	var ct []byte
	var kid string
	if err := e.owner.QueryRow(context.Background(), `SELECT i.id, i.code_enc, i.code_key_id FROM staff_invites i
		JOIN staff_accounts a ON a.id = i.staff_id
		WHERE a.email = $1 AND i.consumed_at IS NULL AND i.revoked_at IS NULL`, email).Scan(&iid, &ct, &kid); err != nil {
		e.t.Fatal(err)
	}
	code, err := e.ring.Open(ct, kid, iid[:])
	if err != nil {
		e.t.Fatal(err)
	}
	return string(code)
}

// freeAddr — свободный локальный адрес.
func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// holdingSender — отправитель фона поверх SMTP (шов wrapSender): пока hold задан, письмо ждёт его закрытия
// или отмены своего ctx — «письмо в полёте» при остановке сервиса; cancelled — ctx письма отменили.
type holdingSender struct {
	next      mail.Sender
	mu        sync.Mutex
	hold      chan struct{}
	inSend    chan struct{}
	cancelled atomic.Bool
}

// holdMail — подменяет отправителя фона на время теста.
func holdMail(t *testing.T) *holdingSender {
	t.Helper()
	hs := &holdingSender{inSend: make(chan struct{}, 10)}
	prev := wrapSender
	wrapSender = func(s mail.Sender) mail.Sender { hs.next = s; return hs }
	t.Cleanup(func() { wrapSender = prev })
	return hs
}

func (s *holdingSender) setHold(hold chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hold = hold
}

func (s *holdingSender) Send(ctx context.Context, m mail.Message) error {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold != nil {
		s.inSend <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
			s.cancelled.Store(true)
			return ctx.Err()
		}
	}
	return s.next.Send(ctx, m)
}
