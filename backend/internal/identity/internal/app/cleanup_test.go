package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
)

// Сроки чистки (спека §5.10) на границе: ровно в срок строка остаётся до следующего запуска, срок + 1 с — уходит.
// Чистка — под ролью worker (права §5.5): нехватка права — ошибка Cleanup, а не тихий пропуск.
func TestCleanupRetention(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	uid := seedUser(t, e.owner, "cleanup_ret", "ru")
	now := testNow
	day := 24 * time.Hour
	at := func(d time.Duration) time.Time { return now.Add(-d) }

	// коды: истёк, погашен, отозван — через 24 ч
	code := func(email string, expires time.Time, consumed, revoked *time.Time) uuid.UUID {
		id := uuid.New()
		acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, decoy, last_sent_at, expires_at, consumed_at, revoked_at,
			ip, created_at) VALUES ($1, 'signup', $2, true, $3, $3, $4, $5, '203.0.113.7', $3)`,
			id, email, expires, consumed, revoked)
		return id
	}
	future := now.Add(time.Hour)
	codeKeep := code("keep@example.com", at(day), nil, nil)
	codeDrop := code("drop@example.com", at(day+time.Second), nil, nil)
	consumedKeep, consumedDrop := at(day), at(day+time.Second)
	codeConsumedKeep := code("ck@example.com", future, &consumedKeep, nil)
	codeConsumedDrop := code("cd@example.com", future, &consumedDrop, nil)
	codeRevokedDrop := code("rd@example.com", future, nil, &consumedDrop)

	// вызовы: истёк, закрыт — через 24 ч
	challenge := func(expires time.Time, consumed *time.Time) uuid.UUID {
		id := uuid.New()
		acctExec(t, e, `INSERT INTO auth_challenges (id, purpose, expires_at, consumed_at, created_at)
			VALUES ($1, 'mfa', $2, $3, $2)`, id, expires, consumed)
		return id
	}
	chKeep := challenge(at(day), nil)
	chDrop := challenge(at(day+time.Second), nil)
	chConsumedDrop := challenge(future, &consumedDrop)

	// сессии: отозванные и истёкшие — через 30 дней
	month := 30 * day
	session := func(idle, absolute time.Time, revoked *time.Time) uuid.UUID {
		id := uuid.New()
		var reason *string
		if revoked != nil {
			r := "logout"
			reason = &r
		}
		acctExec(t, e, `INSERT INTO sessions (id, user_id, client_kind, auth_method, ip, created_at, last_seen_at,
			idle_expires_at, absolute_expires_at, revoked_at, revoked_reason)
			VALUES ($1, $2, 'web', 'password', '203.0.113.7', $3, $3, $4, $5, $6, $7)`,
			id, uid, at(400*day), idle, absolute, revoked, reason)
		return id
	}
	far := now.Add(month)
	revKeep, revDrop := at(month), at(month+time.Second)
	sRevokedKeep := session(far, far, &revKeep)
	sRevokedDrop := session(far, far, &revDrop)
	sIdleKeep := session(at(month), far, nil)
	sIdleDrop := session(at(month+time.Second), far, nil)
	sAbsDrop := session(far, at(month+time.Second), nil)
	sLive := session(far, far, nil)

	// преемник refresh — после окна (successor_until)
	refresh := func(until time.Time) uuid.UUID {
		id := uuid.New()
		hash := make([]byte, 32)
		copy(hash, id[:])
		acctExec(t, e, `INSERT INTO refresh_tokens (id, session_id, token_hash, created_at, used_at, successor_id,
			successor_enc, successor_until) VALUES ($1, $2, $3, $4, $4, $5, '\x01', $6)`,
			id, sLive, hash, at(time.Minute), uuid.New(), until)
		return id
	}
	rKeep := refresh(now)
	rDrop := refresh(at(time.Second))

	// журнал безопасности — 180 дней; адрес (meta_enc) — 7 дней
	secEvent := func(created time.Time) uuid.UUID {
		id := uuid.New()
		acctExec(t, e, `INSERT INTO security_events (id, user_id, kind, meta_enc, key_id, created_at)
			VALUES ($1, $2, 'password_changed', '\x01', 'AAAAAAAAAAA', $3)`, id, uid, created)
		return id
	}
	evKeep := secEvent(at(180 * day))
	evDrop := secEvent(at(180*day + time.Second))
	metaKeep := secEvent(at(7 * day))
	metaDrop := secEvent(at(7*day + time.Second))

	// карантин ника — после expires_at
	acctExec(t, e, `INSERT INTO nickname_reservations (nickname_normalized, released_at, expires_at) VALUES
		('ret_keep', $1, $2), ('ret_drop', $1, $3)`, at(90*day), now, at(time.Second))

	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}

	exists := func(table string, id uuid.UUID) bool {
		return count(t, e.owner, `SELECT count(*) FROM `+pgx.Identifier{table}.Sanitize()+` WHERE id = $1`, id) == 1
	}
	for _, c := range []struct {
		name  string
		table string
		id    uuid.UUID
		want  bool
	}{
		{"код ровно сутки после срока", "auth_codes", codeKeep, true},
		{"код сутки и 1 с после срока", "auth_codes", codeDrop, false},
		{"погашенный ровно сутки", "auth_codes", codeConsumedKeep, true},
		{"погашенный сутки и 1 с", "auth_codes", codeConsumedDrop, false},
		{"отозванный сутки и 1 с", "auth_codes", codeRevokedDrop, false},
		{"вызов ровно сутки после срока", "auth_challenges", chKeep, true},
		{"вызов сутки и 1 с после срока", "auth_challenges", chDrop, false},
		{"закрытый вызов сутки и 1 с", "auth_challenges", chConsumedDrop, false},
		{"отозванная сессия ровно 30 дней", "sessions", sRevokedKeep, true},
		{"отозванная сессия 30 дней и 1 с", "sessions", sRevokedDrop, false},
		{"бездействие ровно 30 дней", "sessions", sIdleKeep, true},
		{"бездействие 30 дней и 1 с", "sessions", sIdleDrop, false},
		{"абсолютный срок 30 дней и 1 с", "sessions", sAbsDrop, false},
		{"живая сессия", "sessions", sLive, true},
		{"событие ровно 180 дней", "security_events", evKeep, true},
		{"событие 180 дней и 1 с", "security_events", evDrop, false},
		{"событие 7 дней и 1 с остаётся", "security_events", metaDrop, true},
		{"refresh с преемником остаётся", "refresh_tokens", rDrop, true},
	} {
		if got := exists(c.table, c.id); got != c.want {
			t.Errorf("%s: строка есть = %v, ждали %v", c.name, got, c.want)
		}
	}
	metaOf := func(id uuid.UUID) bool {
		return count(t, e.owner, `SELECT count(*) FROM security_events WHERE id = $1 AND meta_enc IS NOT NULL`, id) == 1
	}
	if !metaOf(metaKeep) || metaOf(metaDrop) {
		t.Errorf("адрес: ровно 7 дней есть = %v (ждали да), 7 дней и 1 с есть = %v (ждали нет)", metaOf(metaKeep), metaOf(metaDrop))
	}
	succ := func(id uuid.UUID) bool {
		return count(t, e.owner, `SELECT count(*) FROM refresh_tokens WHERE id = $1 AND successor_enc IS NOT NULL
			AND successor_until IS NOT NULL`, id) == 1
	}
	if !succ(rKeep) || succ(rDrop) {
		t.Errorf("преемник: ровно в конце окна есть = %v (ждали да), окно + 1 с есть = %v (ждали нет)", succ(rKeep), succ(rDrop))
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_reservations WHERE nickname_normalized = 'ret_keep'`); n != 1 {
		t.Error("карантин ровно в срок снят")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_reservations WHERE nickname_normalized = 'ret_drop'`); n != 0 {
		t.Error("карантин после срока остался")
	}
}

// Пачки: строк больше пачки — чистка идёт до исчерпания (каждая пачка — своя транзакция), отчёт считает все.
func TestCleanupBatches(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	old := testNow.Add(-48 * time.Hour)
	for range 5 {
		acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, decoy, last_sent_at, expires_at, ip, created_at)
			VALUES (gen_random_uuid(), 'signup', gen_random_uuid()::text || '@example.com', true, $1, $1, '203.0.113.7', $1)`, old)
	}
	rep, err := w.cleanup(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes`); n != 0 || rep.Rows["auth_codes"] != 5 {
		t.Fatalf("осталось кодов %d, в отчёте %d", n, rep.Rows["auth_codes"])
	}
}

// Истёкшие ограничения (until ≤ now) — lifted_at и событие identity.restrictions_changed в той же транзакции: статус
// и оставшиеся закрытые возможности, версия аккаунта + 1; бессрочные и будущие не трогаются; повтор — без событий.
func TestCleanupLiftsRestrictions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	uid := seedUser(t, e.owner, "cleanup_lift", "ru")
	other := seedUser(t, e.owner, "cleanup_quiet", "ru")
	now := testNow
	restrict := func(user uuid.UUID, capability string, until *time.Time) {
		acctExec(t, e, `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
			VALUES (gen_random_uuid(), $1, $2, $3, gen_random_uuid(), $4)`, user, capability, until, now.Add(-time.Hour))
	}
	past, exact, later := now.Add(-time.Second), now, now.Add(time.Hour)
	restrict(uid, "comment", &past)
	restrict(uid, "chat", &exact)
	restrict(uid, "match_join", &later)
	restrict(uid, "create_match", nil)
	restrict(other, "comment", &later)
	var verBefore int64
	if err := e.owner.QueryRow(context.Background(), `SELECT version FROM users WHERE id = $1`, uid).Scan(&verBefore); err != nil {
		t.Fatal(err)
	}

	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE user_id = $1 AND lifted_at = $2
		AND capability IN ('comment', 'chat')`, uid, now); n != 2 {
		t.Fatalf("снято истёкших %d, ждали 2", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE lifted_at IS NOT NULL
		AND (user_id = $1 AND capability IN ('match_join', 'create_match') OR user_id = $2)`, uid, other); n != 0 {
		t.Fatalf("сняты действующие: %d", n)
	}
	var (
		payload []byte
		ver     int64
	)
	if err := e.owner.QueryRow(context.Background(), `SELECT payload, aggregate_version FROM outbox
		WHERE event_type = $1 AND aggregate_id = $2`, identity.EventRestrictionsChanged, uid).Scan(&payload, &ver); err != nil {
		t.Fatal(err)
	}
	var p identity.RestrictionsChangedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.UserID != uid || p.Status != "active" || !slices.Equal(p.Capabilities, []string{"create_match", "match_join"}) {
		t.Fatalf("событие %+v", p)
	}
	if ver != verBefore+1 || count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND version = $2`, uid, ver) != 1 {
		t.Fatalf("версия события %d, до — %d", ver, verBefore)
	}
	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`, identity.EventRestrictionsChanged); n != 1 {
		t.Fatalf("событий после повтора %d, ждали 1", n)
	}
}

// Аккаунт под замком сценария (users FOR NO KEY UPDATE) — пропуск без ожидания и без ошибки; следующий запуск снимает.
func TestCleanupLiftSkipsLockedUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	uid := seedUser(t, e.owner, "cleanup_locked", "ru")
	past := testNow.Add(-time.Second)
	acctExec(t, e, `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
		VALUES (gen_random_uuid(), $1, 'comment', $2, gen_random_uuid(), $2)`, uid, past)
	ctx := context.Background()
	tx, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // при падении — не держать замок до конца теста
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, uid); err != nil {
		t.Fatal(err)
	}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := w.Cleanup(tctx); err != nil {
		t.Fatalf("занятый аккаунт: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE lifted_at IS NULL`); n != 1 {
		t.Fatal("снято под чужим замком")
	}
	if _, err := w.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE lifted_at IS NULL`); n != 0 {
		t.Fatal("после снятия замка не снято")
	}
}

// scrubTables — все таблицы сверки удаления (CleanupDeletedLeftovers): каждая досеяна, чтобы пропуск любого шага красил
// тест (m1 ревью R1W6).
var scrubTables = []string{"credentials", "user_mfa", "user_recovery_codes", "passkeys", "user_identities", "consents",
	"auth_codes", "sessions", "nickname_changes"}

// seedLeftovers — по строке в каждой таблице scrubTables у аккаунта.
func seedLeftovers(t *testing.T, e *env, u uuid.UUID) {
	t.Helper()
	acctExec(t, e, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h:x', $2)`, u, testNow)
	acctExec(t, e, `INSERT INTO user_mfa (user_id, secret_enc, key_id, created_at) VALUES ($1, '\x01', 'AAAAAAAAAAA', $2)`, u, testNow)
	acctExec(t, e, `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at)
		VALUES ($1, decode(repeat('ab', 32), 'hex'), 'AAAAAAAAAAA', $2)`, u, testNow)
	acctExec(t, e, `INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count, backup_eligible,
		backup_state, attestation_format, name, created_at)
		VALUES (gen_random_uuid(), $1::uuid, uuid_send($1::uuid), '\x01', gen_random_uuid(), 0, false, false, 'none', 'key', $2)`, u, testNow)
	acctExec(t, e, `INSERT INTO user_identities (id, user_id, provider, subject, email_is_private_relay, linked_at)
		VALUES (gen_random_uuid(), $1::uuid, 'google', $1::uuid::text, false, $2)`, u, testNow)
	acctExec(t, e, `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
		VALUES ($1, 'terms', '2026-11-01', $2, 'web')`, u, testNow)
	acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, user_id, decoy, last_sent_at, expires_at, ip, created_at)
		VALUES (gen_random_uuid(), 'email_change', $1::uuid::text || '@example.com', $1::uuid, true, $2, $3, '203.0.113.7', $2)`,
		u, testNow, testNow.Add(time.Hour))
	acctExec(t, e, `INSERT INTO sessions (id, user_id, client_kind, auth_method, ip, created_at, last_seen_at,
		idle_expires_at, absolute_expires_at) VALUES (gen_random_uuid(), $1, 'web', 'password', '203.0.113.7', $2, $2, $3, $3)`,
		u, testNow, testNow.Add(time.Hour))
	acctExec(t, e, `INSERT INTO nickname_changes (user_id, old_value, new_value, created_at) VALUES ($1, 'a', 'b', $2)`, u, testNow)
}

// leftovers — строк аккаунта в таблице сверки.
func leftovers(t *testing.T, e *env, table string, u uuid.UUID) int {
	t.Helper()
	return count(t, e.owner, `SELECT count(*) FROM `+pgx.Identifier{table}.Sanitize()+` WHERE user_id = $1`, u)
}

// Сверка удаления (спека бэкенда §6.5): у аккаунта deleted остались секреты, привязки, согласия, коды, сессии и история
// ника — дочищаются все 9 таблиц; у живого аккаунта — не трогаются.
func TestCleanupScrubsDeleted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	gone := seedUser(t, e.owner, "cleanup_gone", "ru")
	live := seedUser(t, e.owner, "cleanup_live", "ru")
	for _, u := range []uuid.UUID{gone, live} {
		seedLeftovers(t, e, u)
	}
	acctExec(t, e, `UPDATE users SET status = 'deleted' WHERE id = $1`, gone)

	rep, err := w.Cleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range scrubTables {
		if n := leftovers(t, e, table, gone); n != 0 {
			t.Errorf("%s удалённого: %d", table, n)
		}
		if n := leftovers(t, e, table, live); n != 1 {
			t.Errorf("%s живого: %d", table, n)
		}
	}
	if rep.Rows["deleted_leftovers"] != 1 {
		t.Errorf("дочищено аккаунтов %d, ждали 1", rep.Rows["deleted_leftovers"])
	}
}

// I2 ревью R1W6: аккаунт, на котором сверка падает, не откатывает и не заслоняет остальных (tx на аккаунт): у good
// дочищено, у bad всё на месте, Cleanup — errCleanupRowsFailed; после починки повтор дочищает bad без ошибки.
func TestCleanupScrubSkipsFailedUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	bad := seedUser(t, e.owner, "scrub_bad", "ru")
	good := seedUser(t, e.owner, "scrub_good", "ru")
	for _, u := range []uuid.UUID{bad, good} {
		acctExec(t, e, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h:x', $2)`, u, testNow)
		acctExec(t, e, `UPDATE users SET status = 'deleted' WHERE id = $1`, u)
	}
	acctExec(t, e, `CREATE FUNCTION poison_scrub() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF OLD.user_id = '`+bad.String()+`' THEN RAISE EXCEPTION 'отравленная строка'; END IF;
			RETURN OLD;
		END $$`)
	acctExec(t, e, `CREATE TRIGGER poison_scrub BEFORE DELETE ON credentials FOR EACH ROW EXECUTE FUNCTION poison_scrub()`)

	rep, err := w.Cleanup(context.Background())
	if !errors.Is(err, errCleanupRowsFailed) {
		t.Fatalf("ждали errCleanupRowsFailed, получили %v", err)
	}
	if leftovers(t, e, "credentials", good) != 0 || rep.Rows["deleted_leftovers"] != 1 {
		t.Fatalf("good не дочищен (в отчёте %d)", rep.Rows["deleted_leftovers"])
	}
	if leftovers(t, e, "credentials", bad) != 1 {
		t.Fatal("у bad удалено под упавшим шагом")
	}
	acctExec(t, e, `DROP TRIGGER poison_scrub ON credentials`)
	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if leftovers(t, e, "credentials", bad) != 0 {
		t.Fatal("bad не дочищен повтором")
	}
}

// I1 ревью R1W6: аккаунт, на котором снятие ограничения падает, — в журнал и пропуск: good снят с событием, bad нет,
// Cleanup — errCleanupRowsFailed; после починки повтор снимает bad без ошибки.
func TestCleanupLiftSkipsFailedUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	bad := seedUser(t, e.owner, "lift_bad", "ru")
	good := seedUser(t, e.owner, "lift_good", "ru")
	past := testNow.Add(-time.Second)
	for _, u := range []uuid.UUID{bad, good} {
		acctExec(t, e, `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
			VALUES (gen_random_uuid(), $1, 'comment', $2, gen_random_uuid(), $2)`, u, past)
	}
	acctExec(t, e, `CREATE FUNCTION poison_lift() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.user_id = '`+bad.String()+`' THEN RAISE EXCEPTION 'отравленная строка'; END IF;
			RETURN NEW;
		END $$`)
	acctExec(t, e, `CREATE TRIGGER poison_lift BEFORE UPDATE ON user_restrictions FOR EACH ROW EXECUTE FUNCTION poison_lift()`)
	lifted := func(u uuid.UUID) int {
		return count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE user_id = $1 AND lifted_at IS NOT NULL`, u)
	}
	events := func(u uuid.UUID) int {
		return count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2`,
			identity.EventRestrictionsChanged, u)
	}

	rep, err := w.Cleanup(context.Background())
	if !errors.Is(err, errCleanupRowsFailed) {
		t.Fatalf("ждали errCleanupRowsFailed, получили %v", err)
	}
	if lifted(good) != 1 || events(good) != 1 || rep.Rows["user_restrictions"] != 1 {
		t.Fatalf("good: снято %d, событий %d, в отчёте %d", lifted(good), events(good), rep.Rows["user_restrictions"])
	}
	if lifted(bad) != 0 || events(bad) != 0 {
		t.Fatal("bad снят под упавшим шагом")
	}
	acctExec(t, e, `DROP TRIGGER poison_lift ON user_restrictions`)
	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if lifted(bad) != 1 || events(bad) != 1 {
		t.Fatal("bad не снят повтором")
	}
}

// m3 ревью R1W6: шаги чистки независимы — упавший шаг (коды) не прерывает остальные (журнал, снятие ограничений,
// сверка удаления); ошибка шага возвращается, отчёт — по успевшим.
func TestCleanupStepsIndependent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	uid := seedUser(t, e.owner, "steps_ind", "ru")
	gone := seedUser(t, e.owner, "steps_gone", "ru")
	old := testNow.Add(-200 * 24 * time.Hour)
	acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, decoy, last_sent_at, expires_at, ip, created_at)
		VALUES (gen_random_uuid(), 'signup', 'steps@example.com', true, $1, $1, '203.0.113.7', $1)`, old)
	acctExec(t, e, `INSERT INTO security_events (id, user_id, kind, created_at)
		VALUES (gen_random_uuid(), $1, 'password_changed', $2)`, uid, old)
	acctExec(t, e, `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
		VALUES (gen_random_uuid(), $1, 'comment', $2, gen_random_uuid(), $2)`, uid, old)
	acctExec(t, e, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h:x', $2)`, gone, testNow)
	acctExec(t, e, `UPDATE users SET status = 'deleted' WHERE id = $1`, gone)
	acctExec(t, e, `CREATE FUNCTION poison_codes() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'отравленная строка'; END $$`)
	acctExec(t, e, `CREATE TRIGGER poison_codes BEFORE DELETE ON auth_codes FOR EACH ROW EXECUTE FUNCTION poison_codes()`)

	rep, err := w.Cleanup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "auth_codes") || !strings.Contains(err.Error(), "отравленная строка") {
		t.Fatalf("ждали ошибку шага auth_codes, получили %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes`); n != 1 {
		t.Fatalf("кодов %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events`); n != 0 || rep.Rows["security_events"] != 1 {
		t.Errorf("журнал после упавшего шага: осталось %d, в отчёте %d", n, rep.Rows["security_events"])
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE lifted_at IS NOT NULL`); n != 1 {
		t.Error("ограничение не снято после упавшего шага")
	}
	if leftovers(t, e, "credentials", gone) != 0 {
		t.Error("сверка удаления не прошла после упавшего шага")
	}
}

// m2 ревью R1W6: сценарий (RevokeUserCodes, DeletionRevokeCodes) держит замок кода second и затем берёт first; чистка,
// ждущая second, не держит замок first (код за кодом, каждый — своим оператором), — цикла нет, 40P01 ни у кого. Пачкой
// одним DELETE чистка держала бы first и ждала second — взаимная блокировка.
func TestCleanupCodesNoDeadlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	old := testNow.Add(-48 * time.Hour)
	uid := seedUser(t, e.owner, "codes_dl", "ru")
	first := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	second := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	for i, id := range []uuid.UUID{first, second} { // вставка по порядку id: и порядок кучи, и порядок индекса
		acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, user_id, decoy, last_sent_at, expires_at, ip, created_at)
			VALUES ($1::uuid, $2::auth_code_purpose, 'dl@example.com', $3, true, $4, $4, '203.0.113.7', $4)`,
			id, []string{"password_reset", "email_change"}[i], uid, old)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM auth_codes WHERE id = $1 FOR UPDATE`, second); err != nil {
		t.Fatal(err)
	}
	type result struct {
		rep CleanupReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := w.Cleanup(ctx)
		done <- result{rep, err}
	}()
	// барьер: чистка ждёт замок second (не по времени — по pg_stat_activity)
	for {
		var waiting int
		if err := e.owner.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query ILIKE '%DELETE FROM auth_codes%'`).Scan(&waiting); err != nil {
			t.Fatalf("ожидание чистки: %v", err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("чистка не ждала занятый код: %+v", r)
		default:
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE auth_codes SET revoked_at = $2 WHERE id = $1`, first, testNow); err != nil {
		t.Fatalf("сценарий: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("чистка: %v", r.err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes`); n != 0 || r.rep.Rows["auth_codes"] != 2 {
		t.Fatalf("осталось кодов %d, в отчёте %d", n, r.rep.Rows["auth_codes"])
	}
}
