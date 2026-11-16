package mail_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/identity/internal/app"
	imail "wf/backend/internal/identity/mail"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	pmail "wf/backend/internal/platform/mail"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/testkit/clocktest"
	"wf/backend/internal/platform/testkit/dbtest"
	"wf/backend/locales"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// testNow — часы теста (UTC, без дробей секунд).
var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// env — письма под ролью worker (§5.5) на клоне базы: owner — подготовка данных, сборка — через SendWorker
// платформы (тот же путь, что у воркера: ErrSkip → JobCancel).
type env struct {
	owner *pgxpool.Pool
	pool  *pgxpool.Pool // роль worker
	ring  *keys.Ring
	cat   *i18n.Catalog
	clock *clocktest.Fake
	reg   *pmail.Registry
}

func newRing(t *testing.T) *keys.Ring {
	t.Helper()
	k, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r, err := keys.NewRing([]string{k})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func catalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load(locales.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pools := dbtest.NewPoolsAs(t, "worker")
	e := &env{owner: pools.Owner, pool: pools.As, ring: newRing(t), cat: catalog(t), clock: clocktest.New(testNow), reg: pmail.NewRegistry()}
	if err := imail.Register(e.reg, imail.ComposerDeps{Pool: pools.As, Secrets: e.ring, Catalog: e.cat, Clock: e.clock}); err != nil {
		t.Fatal(err)
	}
	return e
}

// work — исход исполнителя очереди mail для письма kind по ref (письмо — в память и не проверяется).
func (e *env) work(kind string, ref uuid.UUID) error {
	return pmail.NewSendWorker(e.reg, &pmail.Memory{}).Work(context.Background(),
		&river.Job[pmail.SendArgs]{Args: pmail.SendArgs{V: 1, Mail: kind, Ref: ref}})
}

// send — письмо вида kind по ref через исполнитель очереди mail; skip — задача отменена (ErrSkip).
func (e *env) send(t *testing.T, kind string, ref uuid.UUID) (msg pmail.Message, skip bool) {
	t.Helper()
	mem := &pmail.Memory{}
	err := pmail.NewSendWorker(e.reg, mem).Work(context.Background(),
		&river.Job[pmail.SendArgs]{Args: pmail.SendArgs{V: 1, Mail: kind, Ref: ref}})
	if errors.Is(err, pmail.ErrSkip) {
		if !errors.Is(err, &river.JobCancelError{}) {
			t.Fatalf("ErrSkip без отмены задачи: %v", err)
		}
		return pmail.Message{}, true
	}
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	sent := mem.Messages()
	if len(sent) != 1 {
		t.Fatalf("%s: писем %d", kind, len(sent))
	}
	return sent[0], false
}

// mustSend — письмо обязано уйти.
func (e *env) mustSend(t *testing.T, kind string, ref uuid.UUID) pmail.Message {
	t.Helper()
	m, skip := e.send(t, kind, ref)
	if skip {
		t.Fatalf("%s: письмо отменено (ErrSkip)", kind)
	}
	return m
}

// mustSkip — письмо обязано отмениться ErrSkip.
func (e *env) mustSkip(t *testing.T, kind string, ref uuid.UUID) {
	t.Helper()
	if m, skip := e.send(t, kind, ref); !skip {
		t.Fatalf("%s: письмо ушло на %q, ждали ErrSkip", kind, m.To)
	}
}

// text — ожидаемый текст ключа identity.mail.<name>.<part> на языке locale.
func (e *env) text(t *testing.T, locale, name, part string, args map[string]any) string {
	t.Helper()
	s, err := e.cat.Text(locale, "identity.mail."+name+"."+part, args)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// user — аккаунт; id из базы.
func (e *env) user(t *testing.T, email, locale string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	nick := strings.Split(email, "@")[0]
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date, locale)
		VALUES ($1, now(), $2, '2000-01-01', $3) RETURNING id`, email, nick, locale).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// pendingDeletion — аккаунт в периоде отмены удаления.
func (e *env) pendingDeletion(t *testing.T, id uuid.UUID) {
	t.Helper()
	e.exec(t, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active',
		deletion_requested_at = $2, purge_at = $3 WHERE id = $1`, id, testNow, testNow.Add(30*24*time.Hour))
}

// anonymized — аккаунт после анонимизации: статус deleted, почта — заглушка; пояс и город затёрты, как в 0026.
func (e *env) anonymized(t *testing.T, id uuid.UUID) {
	t.Helper()
	e.exec(t, `UPDATE users SET status = 'deleted', deleted_at = $2, email = 'deleted+' || id::text || '@invalid',
		status_before_deletion = NULL, deletion_requested_at = NULL, purge_at = NULL, timezone = NULL, city_id = NULL
		WHERE id = $1`, id, testNow)
}

// zone — пояс аккаунта (tz, nil — NULL) и город по slug ("" — без города).
func (e *env) zone(t *testing.T, id uuid.UUID, tz *string, citySlug string) {
	t.Helper()
	e.exec(t, `UPDATE users SET timezone = $2, city_id = (SELECT c.id FROM cities c WHERE c.slug = NULLIF($3, ''))
		WHERE id = $1`, id, tz, citySlug)
	if citySlug != "" {
		var n int
		if err := e.owner.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE id = $1 AND city_id IS NOT NULL`,
			id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("город %q не найден: %v", citySlug, err)
		}
	}
}

// codeRow — строка auth_codes; code == "" — приманка без секретов.
type codeRow struct {
	purpose string
	email   string
	userID  *uuid.UUID
	code    string
	payload []byte
	expires time.Time
	ring    *keys.Ring // nil — кольцо окружения
}

func (e *env) code(t *testing.T, c codeRow) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	ring := c.ring
	if ring == nil {
		ring = e.ring
	}
	if c.expires.IsZero() {
		c.expires = testNow.Add(15 * time.Minute)
	}
	var mac, enc, payload []byte
	var keyID *string
	if c.code != "" {
		mac = make([]byte, 32)
		_, _ = rand.Read(mac)
		ct, kid, err := ring.Seal([]byte(c.code), id[:])
		if err != nil {
			t.Fatal(err)
		}
		enc, keyID = ct, &kid
		if c.payload != nil {
			if payload, _, err = ring.Seal(c.payload, id[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	e.exec(t, `INSERT INTO auth_codes (id, purpose, email, user_id, decoy, code_mac, code_enc, payload_enc, key_id,
			last_sent_at, expires_at, ip, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, '203.0.113.7', $10)`,
		id, c.purpose, c.email, c.userID, c.code == "", mac, enc, payload, keyID, testNow.Add(-time.Minute), c.expires)
	return id
}

// secEvent — запись журнала безопасности; address != "" — в meta_enc (Seal, aad = id записи).
func (e *env) secEvent(t *testing.T, userID uuid.UUID, kind string, meta map[string]any, address string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if meta == nil {
		meta = map[string]any{}
	}
	mj, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var enc []byte
	var keyID *string
	if address != "" {
		ct, kid, err := e.ring.Seal([]byte(address), id[:])
		if err != nil {
			t.Fatal(err)
		}
		enc, keyID = ct, &kid
	}
	e.exec(t, `INSERT INTO security_events (id, user_id, kind, meta, meta_enc, key_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, userID, kind, mj, enc, keyID, testNow.Add(-time.Minute))
	return id
}

// mfaReset — сброс 2FA поддержкой; state: live | cancelled | executed.
func (e *env) mfaReset(t *testing.T, userID uuid.UUID, state string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	var executed, cancelled *time.Time
	var by *string
	switch state {
	case "cancelled":
		cancelled, by = new(testNow), new("owner")
	case "executed":
		executed = new(testNow)
	}
	e.exec(t, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
			execute_at, executed_at, cancelled_at, cancelled_by, created_at)
		VALUES ($1, $2, $3, $4, 'Оператор', 'просьба игрока', $5, $6, $7, $8, $9)`,
		id, userID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), testNow.Add(7*24*time.Hour), executed, cancelled,
		by, testNow.Add(-time.Hour))
	return id
}

func mustContain(t *testing.T, what, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%s: нет %q в %q", what, sub, s)
	}
}

// Каждый вид письма I-2 зарегистрирован (повторное Add того же вида — отказ), у каждого — subject и text в ru и en.
func TestComposersCoverKinds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, kind := range app.MailKinds() {
		if err := e.reg.Add(kind, nil); err == nil {
			t.Errorf("%s: вид не зарегистрирован", kind)
		}
		name := strings.TrimPrefix(kind, "identity.")
		for _, loc := range i18n.Supported {
			keys := e.cat.Keys(loc)
			for _, part := range []string{"subject", "text"} {
				key := "identity.mail." + name + "." + part
				found := false
				for _, k := range keys {
					found = found || k == key
				}
				if !found {
					t.Errorf("%s: нет ключа %s", loc, key)
				}
			}
		}
	}
	// без кольца секретов письма с кодами не собрать — отказ сборки, а не паника в воркере
	if err := imail.Register(pmail.NewRegistry(), imail.ComposerDeps{Pool: e.pool, Catalog: e.cat, Clock: e.clock}); err == nil {
		t.Fatal("Register без кольца секретов")
	}
}

// Код регистрации — на почту строки, язык — из данных регистрации; погашен, отозван, истёк или не прочитать — ErrSkip;
// приманка — «уже есть аккаунт» владельцу (в периоде удаления — свой текст), настоящему коду этот вид не годится.
func TestComposeSignupCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	payload := []byte(`{"password_hash":"h","nickname":"neo","locale":"en"}`)
	ref := e.code(t, codeRow{purpose: "signup", email: "new@example.com", code: "123456", payload: payload})
	m := e.mustSend(t, app.MailSignupCode, ref)
	if m.To != "new@example.com" || m.Subject != e.text(t, "en", "signup_code", "subject", nil) {
		t.Fatalf("письмо: %q %q", m.To, m.Subject)
	}
	mustContain(t, "код", m.Text, "123456")
	mustContain(t, "срок", m.Text, "15 minutes")
	if again := e.mustSend(t, app.MailSignupCode, ref); again != m {
		t.Fatal("повтор задачи собрал другое письмо")
	}
	e.mustSkip(t, app.MailSignupAccountExists, ref) // настоящий код — не приманка

	// закрытые и истёкшие коды
	consumed := e.code(t, codeRow{purpose: "signup", email: "c@example.com", code: "111111"})
	e.exec(t, `UPDATE auth_codes SET consumed_at = $2, code_enc = NULL WHERE id = $1`, consumed, testNow)
	e.mustSkip(t, app.MailSignupCode, consumed)
	revoked := e.code(t, codeRow{purpose: "signup", email: "r@example.com", code: "222222"})
	e.exec(t, `UPDATE auth_codes SET revoked_at = $2, code_enc = NULL WHERE id = $1`, revoked, testNow)
	e.mustSkip(t, app.MailSignupCode, revoked)
	edge := e.code(t, codeRow{purpose: "signup", email: "x@example.com", code: "333333", expires: testNow})
	e.mustSkip(t, app.MailSignupCode, edge) // ровно срок — уже истёк
	alive := e.code(t, codeRow{purpose: "signup", email: "y@example.com", code: "444444", expires: testNow.Add(time.Second)})
	mustContain(t, "секунда до срока", e.mustSend(t, app.MailSignupCode, alive).Text, "444444")
	e.mustSkip(t, app.MailSignupCode, uuid.Must(uuid.NewV7()))
	e.mustSkip(t, app.MailPasswordResetCode, alive) // назначение кода не то

	// код под ключом, которого нет в кольце: отмена без повторов, кода нет в ошибке
	foreign := e.code(t, codeRow{purpose: "signup", email: "f@example.com", code: "987654", ring: newRing(t)})
	mem := &pmail.Memory{}
	err := pmail.NewSendWorker(e.reg, mem).Work(context.Background(),
		&river.Job[pmail.SendArgs]{Args: pmail.SendArgs{V: 1, Mail: app.MailSignupCode, Ref: foreign}})
	if !errors.Is(err, pmail.ErrSkip) || strings.Contains(err.Error(), "987654") || strings.Contains(err.Error(), "f@example.com") {
		t.Fatalf("нечитаемый код: %v", err)
	}

	// приманка: аккаунт есть — письмо владельцу на его языке
	owner := e.user(t, "taken@example.com", "ru")
	decoy := e.code(t, codeRow{purpose: "signup", email: "taken@example.com"})
	m = e.mustSend(t, app.MailSignupAccountExists, decoy)
	if m.To != "taken@example.com" || !strings.HasPrefix(m.Text, e.text(t, "ru", "signup_account_exists", "text", nil)) {
		t.Fatalf("приманка: %q %q", m.To, m.Text)
	}
	e.mustSkip(t, app.MailSignupCode, decoy) // у приманки кода нет
	// настоящий код — не приманка, даже если на адрес уже есть аккаунт
	e.user(t, "both@example.com", "ru")
	e.mustSkip(t, app.MailSignupAccountExists, e.code(t, codeRow{purpose: "signup", email: "both@example.com", code: "121212"}))
	// отозванная приманка (новый старт погасил прежнюю) — письмо уходит по живой строке, не по этой
	e.user(t, "late@example.com", "ru")
	closed := e.code(t, codeRow{purpose: "signup", email: "late@example.com"})
	e.exec(t, `UPDATE auth_codes SET revoked_at = $2 WHERE id = $1`, closed, testNow)
	e.mustSkip(t, app.MailSignupAccountExists, closed)
	// аккаунт в периоде удаления — «аккаунт ожидает удаления, войдите, чтобы отменить» (§3.11)
	e.pendingDeletion(t, owner)
	m = e.mustSend(t, app.MailSignupAccountExists, decoy)
	if !strings.HasPrefix(m.Text, e.text(t, "ru", "signup_account_exists", "text_pending", nil)) {
		t.Fatalf("приманка в периоде удаления: %q", m.Text)
	}
	// аккаунт анонимизирован — почты больше нет, письмо не нужно
	e.anonymized(t, owner)
	e.mustSkip(t, app.MailSignupAccountExists, decoy)
}

// Сброс пароля: код — владельцу; приманка (нет аккаунта, deleted, banned — m12) — ErrSkip; аккаунт анонимизирован,
// пока письмо в очереди, — ErrSkip.
func TestComposePasswordResetCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "p@example.com", "en")
	ref := e.code(t, codeRow{purpose: "password_reset", email: "p@example.com", userID: &u, code: "555111"})
	m := e.mustSend(t, app.MailPasswordResetCode, ref)
	if m.To != "p@example.com" || m.Subject != e.text(t, "en", "password_reset_code", "subject", nil) {
		t.Fatalf("письмо: %q %q", m.To, m.Subject)
	}
	mustContain(t, "код", m.Text, "555111")
	e.mustSkip(t, app.MailPasswordResetCode, e.code(t, codeRow{purpose: "password_reset", email: "nobody@example.com"}))
	e.anonymized(t, u)
	e.mustSkip(t, app.MailPasswordResetCode, ref)
}

// Код сброса не уходит, если сбросом пароль уже не сменить (resetBlocked, R29/I2): аккаунт забанен или его почта
// больше не почта строки кода (сменил почту, пока письмо в очереди).
func TestComposePasswordResetCodeBlocked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.user(t, "ban@example.com", "ru")
	banned := e.code(t, codeRow{purpose: "password_reset", email: "ban@example.com", userID: &b, code: "123123"})
	e.mustSend(t, app.MailPasswordResetCode, banned) // контрольная: до бана письмо уходит
	e.exec(t, `UPDATE users SET status = 'banned' WHERE id = $1`, b)
	e.mustSkip(t, app.MailPasswordResetCode, banned)

	m := e.user(t, "move@example.com", "ru")
	moved := e.code(t, codeRow{purpose: "password_reset", email: "move@example.com", userID: &m, code: "456456"})
	e.mustSend(t, app.MailPasswordResetCode, moved)
	e.exec(t, `UPDATE users SET email = 'moved-away@example.com' WHERE id = $1`, m)
	e.mustSkip(t, app.MailPasswordResetCode, moved)
}

// Смена почты: код — на новый адрес на языке аккаунта; приманка «адрес занят» — на новый адрес на языке его владельца.
func TestComposeEmailChange(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "old@example.com", "ru")
	ref := e.code(t, codeRow{purpose: "email_change", email: "fresh@example.com", userID: &u, code: "777000",
		payload: []byte("fresh@example.com")})
	m := e.mustSend(t, app.MailEmailChangeCode, ref)
	if m.To != "fresh@example.com" || m.Subject != e.text(t, "ru", "email_change_code", "subject", nil) {
		t.Fatalf("код смены: %q %q", m.To, m.Subject)
	}
	mustContain(t, "код", m.Text, "777000")
	e.mustSkip(t, app.MailPasswordResetCode, ref) // код смены почты — не код сброса

	e.user(t, "busy@example.com", "en")
	taken := e.code(t, codeRow{purpose: "email_change", email: "busy@example.com", userID: &u})
	m = e.mustSend(t, app.MailEmailChangeTaken, taken)
	if m.To != "busy@example.com" || m.Subject != e.text(t, "en", "email_change_taken", "subject", nil) {
		t.Fatalf("адрес занят: %q %q", m.To, m.Subject)
	}
	e.mustSkip(t, app.MailEmailChangeCode, taken)
	// владелец адреса успел сменить почту — письмо не о чем
	e.exec(t, `UPDATE users SET email = 'moved@example.com' WHERE email = 'busy@example.com'`)
	e.mustSkip(t, app.MailEmailChangeTaken, taken)

	e.anonymized(t, u)
	e.mustSkip(t, app.MailEmailChangeCode, ref)
}

// email_changed — на СТАРУЮ почту из meta_enc, не на текущую users.email; meta_enc стёрт чисткой — ErrSkip.
func TestComposeEmailChangedToOldAddress(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "now@example.com", "ru")
	ref := e.secEvent(t, u, "email_changed", nil, "was@example.com")
	m := e.mustSend(t, app.MailEmailChanged, ref)
	if m.To != "was@example.com" {
		t.Fatalf("адрес: %q, ждали старую почту", m.To)
	}
	e.exec(t, `UPDATE security_events SET meta_enc = NULL WHERE id = $1`, ref)
	e.mustSkip(t, app.MailEmailChanged, ref)
}

// account_deleted — после анонимизации адрес из meta_enc новой записи (I3); meta_enc стёрт — ErrSkip.
func TestComposeAccountDeleted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "gone@example.com", "en")
	ref := e.secEvent(t, u, "account_deleted", nil, "gone@example.com")
	e.anonymized(t, u)
	m := e.mustSend(t, app.MailAccountDeleted, ref)
	if m.To != "gone@example.com" || m.Subject != e.text(t, "en", "account_deleted", "subject", nil) {
		t.Fatalf("письмо: %q %q", m.To, m.Subject)
	}
	e.exec(t, `UPDATE security_events SET meta_enc = NULL WHERE id = $1`, ref)
	e.mustSkip(t, app.MailAccountDeleted, ref)

	// получатель жив (анонимизации не было) — «аккаунт удалён» не о чем
	v := e.user(t, "alive@example.com", "en")
	e.mustSkip(t, app.MailAccountDeleted, e.secEvent(t, v, "account_deleted", nil, "alive@example.com"))
	e.pendingDeletion(t, v)
	e.mustSkip(t, app.MailAccountDeleted, e.secEvent(t, v, "account_deleted", nil, "alive@example.com"))
}

// Письма по журналу — на users.email (account_restored тоже: адреса в журнале нет); получатель анонимизирован, пока
// письмо в очереди, — ErrSkip; запись другого вида — ErrSkip; счётчики и дата — из meta.
func TestComposeSecurityEvents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "me@example.com", "ru")
	m := e.mustSend(t, app.MailAccountRestored, e.secEvent(t, u, "account_restored", map[string]any{"client_kind": "web"}, ""))
	if m.To != "me@example.com" {
		t.Fatalf("account_restored: %q", m.To)
	}
	mustContain(t, "осталось кодов", e.mustSend(t, app.MailRecoveryCodeUsed,
		e.secEvent(t, u, "recovery_code_used", map[string]any{"left": 7}, "")).Text, "7")
	pw := e.secEvent(t, u, "password_changed", nil, "")
	e.mustSkip(t, app.MailMfaEnabled, pw) // вид записи не тот
	e.mustSend(t, app.MailPasswordChanged, pw)
	// сброс пароля кодом пишет журнал password_reset — письмо то же «пароль изменён»
	e.mustSend(t, app.MailPasswordChanged, e.secEvent(t, u, "password_reset", nil, ""))
	e.anonymized(t, u)
	e.mustSkip(t, app.MailPasswordChanged, pw)
	e.mustSkip(t, app.MailPasswordChanged, uuid.Must(uuid.NewV7()))
}

// account_deletion_scheduled — только пока аккаунт в периоде удаления: River не гарантирует порядок, и письмо, которое
// дошло до сборки после отмены удаления (account_restored), не уходит.
func TestComposeDeletionScheduledOnlyPending(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "pd@example.com", "ru")
	e.pendingDeletion(t, u)
	ref := e.secEvent(t, u, "account_deletion_scheduled", map[string]any{"purge_at": "2026-12-16T12:00:00Z"}, "pd@example.com")
	m := e.mustSend(t, app.MailAccountDeletionScheduled, ref)
	mustContain(t, "дата удаления", m.Text, "2026-12-16")
	e.exec(t, `UPDATE users SET status = 'active', status_before_deletion = NULL, deletion_requested_at = NULL,
		purge_at = NULL WHERE id = $1`, u)
	e.mustSkip(t, app.MailAccountDeletionScheduled, ref)
}

// Нет ключа meta, который пишет производитель (left, failures, purge_at), — расхождение кода, а не «письмо не нужно»:
// обычная ошибка (повторы, затем видимая мёртвая задача), не ErrSkip.
func TestComposeMetaMissingIsError(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "meta@example.com", "ru")
	p := e.user(t, "meta-pd@example.com", "ru")
	e.pendingDeletion(t, p)
	for kind, ref := range map[string]uuid.UUID{
		app.MailRecoveryCodeUsed:         e.secEvent(t, u, "recovery_code_used", nil, ""),
		app.MailMfaFailures:              e.secEvent(t, u, "mfa_failures", map[string]any{"left": 3}, ""),
		app.MailAccountDeletionScheduled: e.secEvent(t, p, "account_deletion_scheduled", nil, "meta-pd@example.com"),
	} {
		err := e.work(kind, ref)
		if err == nil || errors.Is(err, pmail.ErrSkip) || errors.Is(err, &river.JobCancelError{}) {
			t.Fatalf("%s без ключа meta: %v; ждали обычную ошибку", kind, err)
		}
	}
}

// Контракт «производитель → Composer»: запись журнала пишет настоящий сценарий app (DeleteAccount: meta purge_at,
// адрес в meta_enc), письмо собирает Composer под ролью worker — имена ключей meta и aad адреса совпадают.
func TestComposeFromAppDeleteAccount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "contract@example.com", "en")
	sid := uuid.Must(uuid.NewV7())
	e.exec(t, `INSERT INTO sessions (id, user_id, client_kind, auth_method, reauth_at, ip, created_at, last_seen_at,
			idle_expires_at, absolute_expires_at)
		VALUES ($1, $2, 'web', 'password', $3, '203.0.113.7', $3, $3, $4, $4)`, sid, u, testNow, testNow.Add(24*time.Hour))
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(e.owner), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.NewMaintenance(e.owner, e.clock, e.ring, ins, log)
	if err != nil {
		t.Fatal(err)
	}
	purge, err := a.DeleteAccount(context.Background(), sid, "contract")
	if err != nil {
		t.Fatal(err)
	}
	var ref uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM security_events
		WHERE user_id = $1 AND kind = 'account_deletion_scheduled'`, u).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	m := e.mustSend(t, app.MailAccountDeletionScheduled, ref)
	if m.To != "contract@example.com" || m.Subject != e.text(t, "en", "account_deletion_scheduled", "subject", nil) {
		t.Fatalf("письмо: %q %q", m.To, m.Subject)
	}
	mustContain(t, "дата удаления", m.Text, purge.UTC().Format("2006-01-02 15:04"))
}

// Сброс 2FA поддержкой — по mfa_resets.id под ролью worker: назначен — пока живой, отменён и выполнен — по исходу;
// записи нет или аккаунт анонимизирован — ErrSkip.
func TestComposeMfaReset(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u := e.user(t, "two@example.com", "en")
	live := e.mfaReset(t, u, "live")
	m := e.mustSend(t, app.MailMfaResetScheduled, live)
	if m.To != "two@example.com" {
		t.Fatalf("адрес: %q", m.To)
	}
	mustContain(t, "срок исполнения", m.Text, "2026-11-23")
	e.mustSkip(t, app.MailMfaResetCancelled, live)
	e.mustSkip(t, app.MailMfaResetDone, live)

	v := e.user(t, "three@example.com", "ru")
	cancelled := e.mfaReset(t, v, "cancelled")
	m = e.mustSend(t, app.MailMfaResetCancelled, cancelled)
	when := testNow.Format("2006-01-02 15:04") + " (UTC)" // без пояса и города — UTC
	if want := e.text(t, "ru", "mfa_reset_cancelled", "text", map[string]any{"by": "owner", "time": when}); !strings.HasPrefix(m.Text, want) {
		t.Fatalf("отмена владельцем: %q", m.Text)
	}
	e.mustSkip(t, app.MailMfaResetScheduled, cancelled)
	// отмена сотрудником — cancelled_by = staff:<id>: в письме «поддержка», без id сотрудника
	staffID := uuid.Must(uuid.NewV7())
	e.exec(t, `UPDATE mfa_resets SET cancelled_by = 'staff:' || $2::text WHERE id = $1`, cancelled, staffID)
	m = e.mustSend(t, app.MailMfaResetCancelled, cancelled)
	if want := e.text(t, "ru", "mfa_reset_cancelled", "text", map[string]any{"by": "staff", "time": when}); !strings.HasPrefix(m.Text, want) {
		t.Fatalf("отмена сотрудником: %q", m.Text)
	}
	if strings.Contains(m.Text, staffID.String()) {
		t.Fatal("id сотрудника в письме")
	}

	w := e.user(t, "four@example.com", "ru")
	done := e.mfaReset(t, w, "executed")
	e.mustSend(t, app.MailMfaResetDone, done)
	e.anonymized(t, w)
	e.mustSkip(t, app.MailMfaResetDone, done)
	e.mustSkip(t, app.MailMfaResetScheduled, uuid.Must(uuid.NewV7()))
}

// Время в письмах — в поясе получателя (решение пользователя 16.11): users.timezone, нет или битый — пояс города
// аккаунта (cities.timezone; город посева stavropol — Europe/Moscow), нет и его — UTC. Подпись — имя пояса IANA.
// Битое имя и «Local» (пояс сервера) — не ошибка и не ErrSkip, а следующий шаг цепочки.
func TestComposeTimeInUserZone(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// testNow = 12:00 UTC; запись журнала — минутой раньше
	cases := []struct {
		name, tz, city, done, event string
	}{
		{"пояс аккаунта", "Europe/Moscow", "", "2026-11-16 15:00 (Europe/Moscow)", "2026-11-16 14:59 (Europe/Moscow)"},
		{"пояс аккаунта важнее города", "Asia/Yekaterinburg", "stavropol", "2026-11-16 17:00 (Asia/Yekaterinburg)", "2026-11-16 16:59 (Asia/Yekaterinburg)"},
		{"без пояса — город", "", "stavropol", "2026-11-16 15:00 (Europe/Moscow)", "2026-11-16 14:59 (Europe/Moscow)"},
		{"битый пояс — город", "Mars/Olympus", "stavropol", "2026-11-16 15:00 (Europe/Moscow)", "2026-11-16 14:59 (Europe/Moscow)"},
		{"битый пояс без города — UTC", "Mars/Olympus", "", "2026-11-16 12:00 (UTC)", "2026-11-16 11:59 (UTC)"},
		{"пояс сервера не берём", "Local", "", "2026-11-16 12:00 (UTC)", "2026-11-16 11:59 (UTC)"},
		{"путь вместо пояса", "../../etc/passwd", "", "2026-11-16 12:00 (UTC)", "2026-11-16 11:59 (UTC)"},
		{"ничего — UTC", "", "", "2026-11-16 12:00 (UTC)", "2026-11-16 11:59 (UTC)"},
	}
	for i, c := range cases {
		u := e.user(t, fmt.Sprintf("tz%d@example.com", i), "ru")
		var tz *string
		if c.tz != "" {
			tz = &c.tz
		}
		e.zone(t, u, tz, c.city)
		mustContain(t, c.name+": сброс выполнен", e.mustSend(t, app.MailMfaResetDone, e.mfaReset(t, u, "executed")).Text, c.done)
		mustContain(t, c.name+": журнал", e.mustSend(t, app.MailPasswordChanged, e.secEvent(t, u, "password_changed", nil, "")).Text, c.event)
	}
	// остальные даты писем — в том же поясе: срок сброса (execute_at = +7 дней), отмена, дата удаления из meta
	u := e.user(t, "tz-all@example.com", "en")
	e.zone(t, u, new("Europe/Moscow"), "")
	mustContain(t, "срок сброса", e.mustSend(t, app.MailMfaResetScheduled, e.mfaReset(t, u, "live")).Text, "2026-11-23 15:00 (Europe/Moscow)")
	mustContain(t, "отмена сброса", e.mustSend(t, app.MailMfaResetCancelled, e.mfaReset(t, u, "cancelled")).Text, "2026-11-16 15:00 (Europe/Moscow)")
	e.pendingDeletion(t, u)
	mustContain(t, "дата удаления", e.mustSend(t, app.MailAccountDeletionScheduled, e.secEvent(t, u, "account_deletion_scheduled",
		map[string]any{"purge_at": "2026-12-16T12:00:00Z"}, "tz-all@example.com")).Text, "2026-12-16 15:00 (Europe/Moscow)")
	// анонимизация затирает timezone и city_id (0026): время «аккаунт удалён» считалось бы в UTC — это ожидаемо (в
	// тексте письма даты сейчас нет); письмо собирается без пояса
	del := e.secEvent(t, u, "account_deleted", nil, "tz-all@example.com")
	e.anonymized(t, u)
	e.mustSend(t, app.MailAccountDeleted, del)
}

// Все 19 видов собираются на обоих языках: аргументы текстов совпадают с тем, что даёт Composer (ICU требует все).
func TestComposeAllKindsBothLocales(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, loc := range i18n.Supported {
		u := e.user(t, "u-"+loc+"@example.com", loc)
		e.user(t, "o-"+loc+"@example.com", loc)
		pl := []byte(`{"locale":"` + loc + `"}`)
		refs := map[string]uuid.UUID{
			app.MailSignupCode:          e.code(t, codeRow{purpose: "signup", email: "s-" + loc + "@example.com", code: "100200", payload: pl}),
			app.MailSignupAccountExists: e.code(t, codeRow{purpose: "signup", email: "o-" + loc + "@example.com"}),
			app.MailPasswordResetCode:   e.code(t, codeRow{purpose: "password_reset", email: "u-" + loc + "@example.com", userID: &u, code: "300400"}),
			app.MailEmailChangeCode: e.code(t, codeRow{purpose: "email_change", email: "n-" + loc + "@example.com", userID: &u,
				code: "500600", payload: []byte("n@example.com")}),
			app.MailEmailChangeTaken:         e.code(t, codeRow{purpose: "email_change", email: "o-" + loc + "@example.com", userID: &u}),
			app.MailEmailChanged:             e.secEvent(t, u, "email_changed", nil, "prev@example.com"),
			app.MailPasswordChanged:          e.secEvent(t, u, "password_changed", nil, ""),
			app.MailSessionRevokedSuspicious: e.secEvent(t, u, "refresh_reuse", nil, ""),
			app.MailMfaEnabled:               e.secEvent(t, u, "mfa_enabled", nil, ""),
			app.MailMfaDisabled:              e.secEvent(t, u, "mfa_disabled", nil, ""),
			app.MailRecoveryCodeUsed:         e.secEvent(t, u, "recovery_code_used", map[string]any{"left": 1}, ""),
			app.MailRecoveryCodesRegenerated: e.secEvent(t, u, "recovery_codes_regenerated", nil, ""),
			app.MailMfaFailures:              e.secEvent(t, u, "mfa_failures", map[string]any{"failures": 5}, ""),
			app.MailAccountRestored:          e.secEvent(t, u, "account_restored", nil, ""),
			app.MailMfaResetScheduled:        e.mfaReset(t, u, "live"),
		}
		for _, kind := range app.MailKinds() {
			ref, ok := refs[kind]
			if !ok {
				continue
			}
			m := e.mustSend(t, kind, ref)
			name := strings.TrimPrefix(kind, "identity.")
			if m.Subject == "" || m.To == "" || !strings.HasSuffix(m.Text, e.footer(t, loc)+"\n") {
				t.Errorf("%s %s: %+v", loc, name, m)
			}
		}
		// виды, которым нужен другой аккаунт или исход
		d := e.user(t, "d-"+loc+"@example.com", loc)
		del := e.secEvent(t, d, "account_deleted", nil, "d-"+loc+"@example.com")
		c := e.user(t, "c-"+loc+"@example.com", loc)
		cr := e.mfaReset(t, c, "cancelled")
		x := e.user(t, "x-"+loc+"@example.com", loc)
		xr := e.mfaReset(t, x, "executed")
		pd := e.user(t, "pd-"+loc+"@example.com", loc)
		e.pendingDeletion(t, pd)
		e.mustSend(t, app.MailAccountDeletionScheduled, e.secEvent(t, pd, "account_deletion_scheduled",
			map[string]any{"purge_at": "2026-12-16T12:00:00Z"}, "pd-"+loc+"@example.com"))
		e.anonymized(t, d)
		e.mustSend(t, app.MailAccountDeleted, del)
		e.mustSend(t, app.MailMfaResetCancelled, cr)
		e.mustSend(t, app.MailMfaResetDone, xr)
	}
}

func (e *env) footer(t *testing.T, loc string) string {
	t.Helper()
	s, err := e.cat.Text(loc, "mail.footer", nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
