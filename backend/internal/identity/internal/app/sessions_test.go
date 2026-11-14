package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/queue"
	"wf/backend/internal/platform/token"
)

func issue(t *testing.T, e *env, userID uuid.UUID, c Client) Issued {
	t.Helper()
	var out Issued
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		dev, err := e.a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		out, err = e.a.issueSession(ctx, tx, userID, dev, c, domain.MethodPassword, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// §3.7: сроки по виду клиента (мобильный 90/365 дней, веб 30/180), reauth_at = now; refresh в базе — только SHA-256;
// sid access = TokenPair.SessionID.
func TestIssueSessionTerms(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "terms_user", "ru")
	for _, tc := range []struct {
		c              Client
		idle, absolute time.Duration
	}{
		{mobile("ios", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF"), 90 * 24 * time.Hour, 365 * 24 * time.Hour},
		{web("bff-label"), 30 * 24 * time.Hour, 180 * 24 * time.Hour},
	} {
		got := issue(t, e, uid, tc.c)
		var idle, abs, reauth time.Time
		var kind string
		if err := e.owner.QueryRow(context.Background(), `SELECT idle_expires_at, absolute_expires_at, reauth_at, client_kind
			FROM sessions WHERE id = $1`, got.SessionID).Scan(&idle, &abs, &reauth, &kind); err != nil {
			t.Fatal(err)
		}
		now := e.clock.Now()
		if !idle.Equal(now.Add(tc.idle)) || !abs.Equal(now.Add(tc.absolute)) || !reauth.Equal(now) || kind != string(tc.c.Kind) {
			t.Errorf("%s: idle %v, absolute %v, reauth %v", kind, idle, abs, reauth)
		}
		if !got.Tokens.RefreshIdleExpires.Equal(idle) || got.Tokens.SessionID != got.SessionID {
			t.Errorf("%s: TokenPair %+v", kind, got.Tokens)
		}
		var hash []byte
		if err := e.owner.QueryRow(context.Background(), `SELECT token_hash FROM refresh_tokens WHERE session_id = $1`,
			got.SessionID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(hash, token.HashRefresh(got.Tokens.Refresh)) {
			t.Errorf("%s: в базе не SHA-256 refresh", kind)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM refresh_tokens WHERE position($1::bytea IN token_hash) > 0`,
			[]byte(got.Tokens.Refresh)); n != 0 {
			t.Errorf("%s: сырой refresh в базе", kind)
		}
		claims, err := e.tokens.Verify(got.Tokens.Access)
		if err != nil || claims.SessionID != got.SessionID || claims.UserID != uid {
			t.Errorf("%s: access %+v %v", kind, claims, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND last_login_at = $2`, uid, e.clock.Now()); n != 1 {
		t.Error("last_login_at не записан")
	}
}

// seedLiveSessions — n живых сессий аккаунта, last_seen_at растёт с номером (первая — самая старая).
func seedLiveSessions(t *testing.T, e *env, uid uuid.UUID, n int) []uuid.UUID {
	t.Helper()
	return seedSessions(t, e, uid, n, e.clock.Now().Add(-time.Hour), 30*24*time.Hour)
}

// seedSessions — n неотозванных сессий: last_seen_at = base + g с (по возрастанию), idle_expires_at = base + idle.
func seedSessions(t *testing.T, e *env, uid uuid.UUID, n int, base time.Time, idle time.Duration) []uuid.UUID {
	t.Helper()
	rows, err := e.owner.Query(context.Background(), `INSERT INTO sessions (id, user_id, client_kind, auth_method, ip,
		created_at, last_seen_at, idle_expires_at, absolute_expires_at)
		SELECT gen_random_uuid(), $1, 'mobile', 'password', '203.0.113.7', $2::timestamptz, $2::timestamptz + g * interval '1 second',
		       $4::timestamptz, $2::timestamptz + interval '300 days'
		FROM generate_series(1, $3) g ORDER BY g RETURNING id`, uid, base, n, base.Add(idle))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// R17: 100 живых + новая — старшая по last_seen_at отозвана (revoked), живых 100.
func TestIssueSessionCapsLive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "cap_user", "ru")
	oldest := seedLiveSessions(t, e, uid, domain.MaxLiveSessions)[0]
	issue(t, e, uid, web("bff"))
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != domain.MaxLiveSessions {
		t.Fatalf("живых %d", n)
	}
	var reason string
	if err := e.owner.QueryRow(context.Background(), `SELECT revoked_reason FROM sessions WHERE id = $1`, oldest).Scan(&reason); err != nil || reason != "revoked" {
		t.Fatalf("старшая: %q %v", reason, err)
	}
}

// m4 ревью W3: часы другого экземпляра впереди — 100 живых сессий с last_seen_at в будущем; новая сессия не попадает
// в «лишние» (выдаётся живой), отзывается старшая из прежних.
func TestIssueSessionNotExcessUnderClockSkew(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "skew_user", "ru")
	oldest := seedSessions(t, e, uid, domain.MaxLiveSessions, e.clock.Now().Add(time.Hour), 30*24*time.Hour)[0]
	sid := issue(t, e, uid, web("bff")).SessionID
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid); n != 1 {
		t.Fatal("новая сессия выдана отозванной")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NOT NULL`, oldest); n != 1 {
		t.Fatal("старшая не отозвана")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != domain.MaxLiveSessions {
		t.Fatalf("живых %d", n)
	}
}

// m1 и n1 ревью W3: «выйти везде» отзывает все неотозванные (истёкшая по часам этого сервера может быть живой по
// часам отстающего — она не должна пережить смену пароля), а в ответе и событии — только живые: 100 живых и 40
// истёкших по простою → отозваны все 140, в событии ровно 100 живых.
func TestRevokeSessionsOnlyLive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "revoke_live", "ru")
	live := seedLiveSessions(t, e, uid, domain.MaxLiveSessions)
	seedSessions(t, e, uid, 40, e.clock.Now().Add(-40*24*time.Hour), 24*time.Hour)
	var got []uuid.UUID
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = e.a.revokeSessions(ctx, tx, uid, uuid.Nil, domain.RevokedPasswordChanged)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(got, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	want := slices.Clone(live)
	slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(got, want) {
		t.Fatalf("отозвано %d, живых было %d", len(got), len(want))
	}
	var payload []byte
	if err := e.owner.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE event_type = $1`,
		identity.EventSessionsRevoked).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var p identity.SessionsRevokedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(p.SessionIDs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(p.SessionIDs, want) {
		t.Fatalf("в событии %d id, живых %d", len(p.SessionIDs), len(want))
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 0 {
		t.Fatalf("неотозванных %d: истёкшая по часам этого сервера могла быть живой по часам другого (n1)", n)
	}
}

// revokeSessions: 3 сессии, кроме текущей — 2 отозваны, событие с двумя id, users.version + 1.
func TestRevokeSessionsEvent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "revoke_user", "ru")
	cur := issue(t, e, uid, web("a")).SessionID
	issue(t, e, uid, web("b"))
	issue(t, e, uid, web("c"))
	var got []uuid.UUID
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		got, err = e.a.revokeSessions(ctx, tx, uid, cur, domain.RevokedOthers)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("отозвано %v", got)
	}
	var payload []byte
	var ver, aggVer int64
	if err := e.owner.QueryRow(context.Background(), `SELECT o.payload, o.aggregate_version, u.version FROM outbox o
		JOIN users u ON u.id = o.aggregate_id WHERE o.event_type = $1`, identity.EventSessionsRevoked).
		Scan(&payload, &aggVer, &ver); err != nil {
		t.Fatal(err)
	}
	var p identity.SessionsRevokedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.SessionIDs) != 2 || p.Reason != "revoked_others" || ver != 2 || aggVer != 2 {
		t.Fatalf("payload %+v, версия %d/%d", p, ver, aggVer)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 1 {
		t.Fatalf("живых %d", n)
	}
	// второй раз отзывать нечего — без события и версии
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := e.a.revokeSessions(ctx, tx, uid, cur, domain.RevokedOthers)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`, identity.EventSessionsRevoked); n != 1 {
		t.Fatalf("событий %d", n)
	}
}

// Свежий вход: reauth_at = now − 11 мин → reauth_required; now − 9 мин → ок; отозванная сессия — 401.
func TestRequireFresh(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "fresh_user", "ru")
	sid := issue(t, e, uid, web("a")).SessionID
	check := func(ago time.Duration) error {
		if _, err := e.owner.Exec(context.Background(), `UPDATE sessions SET reauth_at = $2 WHERE id = $1`, sid,
			e.clock.Now().Add(-ago)); err != nil {
			t.Fatal(err)
		}
		return e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			_, err := e.a.requireFresh(ctx, tx, sid)
			return err
		})
	}
	if got := problemCode(check(11 * time.Minute)); got != identity.CodeReauthRequired {
		t.Fatalf("11 мин: %q", got)
	}
	if err := check(domain.ReauthFresh); err != nil {
		t.Fatalf("ровно 10 мин: %v", err)
	}
	if err := check(9 * time.Minute); err != nil {
		t.Fatalf("9 мин: %v", err)
	}
	if _, err := e.owner.Exec(context.Background(), `UPDATE sessions SET revoked_at = now(), revoked_reason = 'logout' WHERE id = $1`, sid); err != nil {
		t.Fatal(err)
	}
	if got := problemCode(check(time.Minute)); got != httpx.CodeUnauthenticated {
		t.Fatalf("отозванная: %q", got)
	}
}

// Письмо — задача в tx действия: откат — задачи нет; коммит — одна, аргументы {v, mail, ref} без адреса.
func TestEnqueueMailInTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ref := uuid.New()
	boom := errors.New("откат")
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		if err := e.a.enqueueMail(ctx, tx, MailMfaEnabled, ref); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send'`); n != 0 {
		t.Fatalf("после отката задач %d", n)
	}
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error { return e.a.enqueueMail(ctx, tx, MailMfaEnabled, ref) }); err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	var q string
	if err := e.owner.QueryRow(context.Background(), `SELECT args, queue FROM river_job WHERE kind = 'mail.send'`).Scan(&args, &q); err != nil {
		t.Fatal(err)
	}
	if len(args) != 3 || args["v"] != float64(1) || args["mail"] != MailMfaEnabled || args["ref"] != ref.String() || q != queue.Mail {
		t.Fatalf("args %v, очередь %s", args, q)
	}
}

// Город по IP — на языке аккаунта (§5.9): фикстура ipgeo, аккаунт en → Stavropol; ipgeo.None → NULL.
func TestIPCityLocale(t *testing.T) {
	t.Parallel()
	loc, closer, err := ipgeo.Open("../../../platform/ipgeo/testdata/city-test.mmdb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	e := newEnv(t, func(d *identity.Deps) { d.IPGeo = loc })
	en := issue(t, e, seedUser(t, e.owner, "city_en", "en"), web("a")).SessionID
	ru := issue(t, e, seedUser(t, e.owner, "city_ru", "ru"), web("b")).SessionID
	city := func(sid uuid.UUID) *string {
		var c *string
		if err := e.owner.QueryRow(context.Background(), `SELECT ip_city FROM sessions WHERE id = $1`, sid).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := city(en); c == nil || *c != "Stavropol" {
		t.Fatalf("en: %v", c)
	}
	if c := city(ru); c == nil || *c != "Ставрополь" {
		t.Fatalf("ru: %v", c)
	}
	none := newEnv(t)
	if c := func() *string {
		sid := issue(t, none, seedUser(t, none.owner, "city_none", "en"), web("a")).SessionID
		var c *string
		if err := none.owner.QueryRow(context.Background(), `SELECT ip_city FROM sessions WHERE id = $1`, sid).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}(); c != nil {
		t.Fatalf("ipgeo.None: %q", *c)
	}
}

func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// I2, N6: «отзыв всех, кроме A» ∥ «отзыв одной B» с барьером afterFirstLock — ни одного 40P01 за 20 повторов (при
// верном порядке второй стоит на users и барьер истекает; при «sessions раньше users» оба держат первый замок и идут
// друг на друга).
func TestLockOrderNoDeadlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "lock_order", "ru")
	for round := range 20 {
		a := issue(t, e, uid, web("a")).SessionID
		b := issue(t, e, uid, web("b")).SessionID
		wait := pairBarrier(200 * time.Millisecond)
		e.a.hooks = &hooks{afterFirstLock: func(op string) {
			if op == "revoke_sessions" || op == "revoke_session" {
				wait()
			}
		}}
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			errs[0] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
				_, err := e.a.revokeSessions(ctx, tx, uid, a, domain.RevokedOthers)
				return err
			})
		})
		wg.Go(func() {
			<-start
			errs[1] = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
				_, err := e.a.revokeSession(ctx, tx, uid, b, domain.RevokedByUser)
				return err
			})
		})
		close(start)
		wg.Wait()
		e.a.hooks = nil
		for _, err := range errs {
			if err != nil {
				t.Fatalf("раунд %d: %v (40P01: %v)", round, err, isDeadlock(err))
			}
		}
	}
}

// N3: NewMaintenance без кольца — запись журнала без адреса проходит, с адресом — errNoSecrets (не паника).
func TestSecurityEventNoRing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(e.a.pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMaintenance(e.a.pool, e.clock, nil, ins, log)
	if err != nil {
		t.Fatal(err)
	}
	uid := seedUser(t, e.owner, "no_ring", "ru")
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := m.securityEvent(ctx, tx, SecurityEvent{UserID: uid, Kind: domain.SecMfaDisabled, Meta: map[string]any{"by": "support"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := m.securityEvent(ctx, tx, SecurityEvent{UserID: uid, Kind: domain.SecEmailChanged, Address: "old@example.com"})
		return err
	}); !errors.Is(err, errNoSecrets) {
		t.Fatalf("с адресом без кольца: %v", err)
	}
	// с кольцом: адрес только в meta_enc, открывается с aad = id записи
	var id uuid.UUID
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = e.a.securityEvent(ctx, tx, SecurityEvent{UserID: uid, Kind: domain.SecEmailChanged, Address: "old@example.com"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var enc []byte
	var keyID string
	var meta string
	if err := e.owner.QueryRow(context.Background(), `SELECT meta_enc, key_id, meta::text FROM security_events WHERE id = $1`, id).
		Scan(&enc, &keyID, &meta); err != nil {
		t.Fatal(err)
	}
	pt, err := e.a.secrets.Open(enc, keyID, id[:])
	if err != nil || string(pt) != "old@example.com" || strings.Contains(meta, "example.com") {
		t.Fatalf("meta_enc %q %v, meta %s", pt, err, meta)
	}
	if _, err := NewMaintenance(nil, e.clock, nil, ins, log); err == nil {
		t.Fatal("NewMaintenance без пула")
	}
}

// Забытая зависимость — ошибка New с именем поля, не паника в запросе.
func TestNewRequiresDeps(t *testing.T) {
	t.Parallel()
	_, err := New(identity.Deps{})
	if err == nil || !strings.Contains(err.Error(), "Hasher") || !strings.Contains(err.Error(), "Legal") {
		t.Fatalf("err = %v", err)
	}
}

// Аудит игрока — автор и объект аккаунт, ПД в before/after маскируются; неудачный вход — anonymous без object_id
// (§8.3: журнал — не оракул существования аккаунта).
func TestAuditUserAndAnonymous(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "audit_user", "ru")
	if err := e.inTx(t, func(ctx context.Context, _ pgx.Tx) error {
		if err := e.a.auditUser(ctx, ActionEmailChanged, uid, map[string]any{"email": "old@example.com"},
			map[string]any{"email": "new@example.com"}); err != nil {
			return err
		}
		return e.a.auditAnonymous(ctx, ActionLoginFailed)
	}); err != nil {
		t.Fatal(err)
	}
	var role, objType, after string
	if err := e.owner.QueryRow(context.Background(), `SELECT actor_role, object_type, after::text FROM audit_log
		WHERE action = $1 AND actor_user_id = $2 AND object_id = $2`, ActionEmailChanged, uid).Scan(&role, &objType, &after); err != nil {
		t.Fatal(err)
	}
	if role != rolePlayer || objType != objectUser || strings.Contains(after, "new@example.com") {
		t.Fatalf("роль %s, объект %s, after %s", role, objType, after)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_role = $2
		AND actor_user_id IS NULL AND object_id IS NULL`, ActionLoginFailed, roleAnonymous); n != 1 {
		t.Fatalf("login_failed: %d", n)
	}
	if roleSystem != "system" {
		t.Fatal(roleSystem)
	}
}
