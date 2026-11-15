package app

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/password"
)

func setPassword(t *testing.T, e *env, uid uuid.UUID, hash string) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO credentials (user_id, password_hash, updated_at)
		VALUES ($1, $2, now()) ON CONFLICT (user_id) DO UPDATE SET password_hash = EXCLUDED.password_hash`, uid, hash); err != nil {
		t.Fatal(err)
	}
}

// R31: хэш со старыми параметрами после верного пароля пересчитан; пароль сменили между Verify и записью — новый
// хэш не затирается, а пароль перепроверяется по новому хэшу (I2 ревью W3) — старый не подходит: false, seenHash —
// новый хэш (вызывающий ответит как на неверный пароль).
func TestCheckPasswordRehash(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "rehash", "ru")
	setPassword(t, e, uid, "old:secret")
	ok, seen, err := e.a.checkPassword(context.Background(), uid, "secret")
	if err != nil || !ok || seen != "h:secret" {
		t.Fatalf("ok %v, seen %q, %v", ok, seen, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM credentials WHERE user_id = $1 AND password_hash = 'h:secret'`, uid); n != 1 {
		t.Fatal("хэш не пересчитан")
	}
	// вызывающий в своей tx сверяет хэш, виденный проверкой (R6)
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.a.lockUser(ctx, tx, uid); err != nil {
			return err
		}
		same, err := e.a.passwordUnchanged(ctx, tx, uid, seen)
		if err != nil || !same {
			t.Errorf("тот же хэш: %v %v", same, err)
		}
		same, err = e.a.passwordUnchanged(ctx, tx, uid, "old:secret")
		if err != nil || same {
			t.Errorf("сменённый хэш: %v %v", same, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// смена пароля во время Verify
	setPassword(t, e, uid, "old:secret")
	h := &fakeHasher{entered: make(chan struct{}, 1), gate: make(chan struct{})}
	e.a.hasher = h
	done := make(chan struct{})
	go func() {
		defer close(done)
		ok, seen, err = e.a.checkPassword(context.Background(), uid, "secret")
	}()
	<-h.entered
	setPassword(t, e, uid, "h:other")
	close(h.gate)
	<-done
	if err != nil || ok || seen != "h:other" {
		t.Fatalf("ok %v, seen %q, %v", ok, seen, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM credentials WHERE user_id = $1 AND password_hash = 'h:other'`, uid); n != 1 {
		t.Fatal("пересчёт затёр новый пароль")
	}
	// нет пароля у аккаунта — false без ошибки
	other := seedUser(t, e.owner, "no_password", "ru")
	e.a.hasher = &fakeHasher{}
	if ok, _, err := e.a.checkPassword(context.Background(), other, "secret"); ok || err != nil {
		t.Fatalf("без пароля: %v %v", ok, err)
	}
}

// I2 ревью W3: два верных входа на старых параметрах разом — один пересчитал хэш, у второго условная запись не
// прошла (0 строк); второй перечитывает хэш и проверяет пароль по нему — оба входа успешны, и seenHash обоих
// совпадает с базой под lockUser (иначе вызывающий ответил бы invalid_credentials на верный пароль).
func TestCheckPasswordConcurrentRehash(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "concurrent_rehash", "ru")
	setPassword(t, e, uid, "old:secret")
	// барьер: оба прочитали старый хэш и стоят в Verify; ёмкость 4 — повторная проверка проигравшего не ждёт
	h := &fakeHasher{entered: make(chan struct{}, 4), gate: make(chan struct{})}
	e.a.hasher = h
	var wg sync.WaitGroup
	var oks [2]bool
	var seens [2]string
	var errs [2]error
	for i := range 2 {
		wg.Go(func() { oks[i], seens[i], errs[i] = e.a.checkPassword(context.Background(), uid, "secret") })
	}
	<-h.entered
	<-h.entered
	close(h.gate)
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil || !oks[i] {
			t.Fatalf("%d: ok %v, %v", i, oks[i], errs[i])
		}
		if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := e.a.lockUser(ctx, tx, uid); err != nil {
				return err
			}
			same, err := e.a.passwordUnchanged(ctx, tx, uid, seens[i])
			if err != nil || !same {
				t.Errorf("%d: seen %q не совпал с базой: %v %v", i, seens[i], same, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// argonHasher — настоящий хэшер со счётом проверок, дошедших до argon2 (без ошибки на входе, ErrTooLong — не в счёт).
type argonHasher struct {
	*password.Hasher
	runs atomic.Int32
}

func (h *argonHasher) Verify(ctx context.Context, pw, encoded string) (bool, bool, error) {
	ok, rehash, err := h.Hasher.Verify(ctx, pw, encoded)
	if err == nil {
		h.runs.Add(1)
	}
	return ok, rehash, err
}

func (h *argonHasher) VerifyDummy(ctx context.Context, pw string) error {
	err := h.Hasher.VerifyDummy(ctx, pw)
	if err == nil {
		h.runs.Add(1)
	}
	return err
}

// I1 ревью W3: пароль длиннее password.MaxInputBytes — неверный пароль (не 500): checkPassword → false без ошибки,
// dummyPassword → nil; argon2 всё равно отработал один раз (время как у неверного пароля).
func TestCheckPasswordTooLong(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	argon, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	h := &argonHasher{Hasher: argon}
	e.a.hasher = h
	uid := seedUser(t, e.owner, "too_long", "ru")
	hash, err := argon.Hash(context.Background(), "настоящий пароль")
	if err != nil {
		t.Fatal(err)
	}
	setPassword(t, e, uid, hash)
	long := strings.Repeat("я", 600) // 1200 байт
	ok, seen, err := e.a.checkPassword(context.Background(), uid, long)
	if ok || err != nil || seen != hash {
		t.Fatalf("checkPassword: ok %v, seen %q, %v", ok, seen, err)
	}
	if n := h.runs.Load(); n != 1 {
		t.Fatalf("checkPassword: argon2 %d раз, нужен 1", n)
	}
	if err := e.a.dummyPassword(context.Background(), long); err != nil {
		t.Fatalf("dummyPassword: %v", err)
	}
	if n := h.runs.Load(); n != 2 {
		t.Fatalf("dummyPassword: argon2 %d раз всего, нужно 2", n)
	}
}

// I10, N9: argon2 не держит соединение пула — 8 параллельных checkPassword внутри Verify, занятых соединений 0.
func TestCheckPasswordNoTxDuringArgon(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "argon_no_tx", "ru")
	setPassword(t, e, uid, "h:secret")
	const n = 8
	h := &fakeHasher{entered: make(chan struct{}, n), gate: make(chan struct{})}
	e.a.hasher = h
	var wg sync.WaitGroup
	oks := make([]bool, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() { oks[i], _, errs[i] = e.a.checkPassword(context.Background(), uid, "secret") })
	}
	// все 8 внутри Verify; не дошли за 10 с — соединения заняты теми, кто уже в Verify (пул меньше 8)
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	entered := 0
	for entered < n {
		select {
		case <-h.entered:
			entered++
		case <-timer.C:
			close(h.gate)
			wg.Wait()
			t.Fatalf("в Verify вошли %d из %d: остальные ждут соединения пула, занятого на время argon2", entered, n)
		}
	}
	acquired := e.a.pool.Stat().AcquiredConns()
	close(h.gate)
	wg.Wait()
	if acquired != 0 {
		t.Fatalf("во время Verify занято соединений: %d", acquired)
	}
	for i := range n {
		if errs[i] != nil || !oks[i] {
			t.Fatalf("%d: %v %v", i, oks[i], errs[i])
		}
	}
}

// §9.1: «нет аккаунта» (dummyPassword) и неверный пароль (checkPassword) на настоящем argon2 — медианы 50 вызовов
// расходятся меньше чем на 15 %.
//
// serial: порог реального времени.
func TestCheckPasswordNoAccountCostsLikeReal(t *testing.T) {
	e := newEnv(t)
	hasher, err := password.NewHasher(password.DefaultParams(), 1)
	if err != nil {
		t.Fatal(err)
	}
	e.a.hasher = hasher
	uid := seedUser(t, e.owner, "timing", "ru")
	hash, err := hasher.Hash(context.Background(), "настоящий пароль")
	if err != nil {
		t.Fatal(err)
	}
	setPassword(t, e, uid, hash)
	// вызовы чередуются: фоновая нагрузка машины (соседние пакеты -p 4) делится между ветками поровну
	timed := func(f func()) time.Duration {
		start := time.Now()
		f()
		return time.Since(start)
	}
	var realRuns, fakeRuns []time.Duration
	for range 50 {
		realRuns = append(realRuns, timed(func() {
			if ok, _, err := e.a.checkPassword(context.Background(), uid, "неверный пароль"); ok || err != nil {
				t.Fatal(ok, err)
			}
		}))
		fakeRuns = append(fakeRuns, timed(func() {
			if err := e.a.dummyPassword(context.Background(), "неверный пароль"); err != nil {
				t.Fatal(err)
			}
		}))
	}
	median := func(ds []time.Duration) time.Duration {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2]
	}
	checked, dummy := median(realRuns), median(fakeRuns)
	diff := float64(checked-dummy) / float64(checked)
	if diff < 0 {
		diff = -diff
	}
	if diff >= 0.15 {
		t.Fatalf("медианы: проверка %v, приманка %v — расхождение %.0f%%", checked, dummy, diff*100)
	}
}

func finish(t *testing.T, e *env, uid uuid.UUID, c Client, mfaDone, blockedAsExpired bool) (LoginOutcome, error) {
	t.Helper()
	var out LoginOutcome
	err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		u, err := e.a.lockUser(ctx, tx, uid)
		if err != nil {
			return err
		}
		dev, err := e.a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		out, err = e.a.finishLogin(ctx, tx, u, c, dev, domain.MethodPassword, mfaDone, blockedAsExpired)
		return err
	})
	return out, err
}

func setStatus(t *testing.T, e *env, uid uuid.UUID, status string) {
	t.Helper()
	q := `UPDATE users SET status = $2, status_before_deletion = NULL, deletion_requested_at = NULL, purge_at = NULL WHERE id = $1`
	if status == "pending_deletion" {
		q = `UPDATE users SET status = $2, status_before_deletion = 'active', deletion_requested_at = now(),
			purge_at = now() + interval '30 days' WHERE id = $1`
	}
	if _, err := e.owner.Exec(context.Background(), q, uid, status); err != nil {
		t.Fatal(err)
	}
}

func sessions(t *testing.T, e *env, uid uuid.UUID) int {
	t.Helper()
	return count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1`, uid)
}

// R6, §3.2, §3.11: порядок finishLogin — banned → 2FA → pending_deletion → сессия.
func TestFinishLoginBranches(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := mobile("ios", "6F9619FF-8B86-4D01-B42D-00CF4FC964FF")

	t.Run("banned до даты и бессрочно", func(t *testing.T) {
		uid := seedUser(t, e.owner, "banned_user", "ru")
		setStatus(t, e, uid, "banned")
		until := e.clock.Now().Add(72 * time.Hour)
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
			VALUES (gen_random_uuid(), $1, $2, $3, gen_random_uuid(), now())`, uid, banCapability, until); err != nil {
			t.Fatal(err)
		}
		_, err := finish(t, e, uid, c, false, false)
		var pe httpx.ProblemError
		if !errors.As(err, &pe) || pe.Problem().Code != identity.CodeAccountBlocked || pe.Problem().BlockedUntil == nil ||
			!pe.Problem().BlockedUntil.Equal(until) {
			t.Fatalf("до даты: %v", err)
		}
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_restrictions (id, user_id, capability, source_id, created_at)
			VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), now())`, uid, banCapability); err != nil {
			t.Fatal(err)
		}
		_, err = finish(t, e, uid, c, false, false)
		if !errors.As(err, &pe) || pe.Problem().Code != identity.CodeAccountBlocked || pe.Problem().BlockedUntil != nil {
			t.Fatalf("бессрочно: %v", err)
		}
		if _, err := finish(t, e, uid, c, true, true); !errors.Is(err, errBlockedAsExpired) {
			t.Fatalf("blockedAsExpired: %v", err)
		}
		if sessions(t, e, uid) != 0 {
			t.Fatal("сессия забаненному")
		}
	})

	t.Run("2FA без второго фактора", func(t *testing.T) {
		uid := seedUser(t, e.owner, "mfa_user", "ru")
		seedMfa(t, e, uid)
		out, err := finish(t, e, uid, c, false, false)
		if err != nil || out.Status != StatusMfaRequired || out.Issued != nil || out.MfaToken == "" ||
			!slices.Equal(out.Methods, []string{"totp", "recovery_code"}) || !out.MfaExpires.Equal(e.clock.Now().Add(5*time.Minute)) {
			t.Fatalf("%+v %v", out, err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM auth_challenges WHERE user_id = $1 AND purpose = 'mfa'
			AND token_hash = $2 AND data->>'client_kind' = 'mobile' AND data->>'method' = 'password' AND (data->>'device_id')::uuid IS NOT NULL`,
			uid, domain.HashStepToken(out.MfaToken)); n != 1 {
			t.Fatal("mfa_token в базе — не SHA-256 или без данных первого шага")
		}
		if sessions(t, e, uid) != 0 {
			t.Fatal("сессия до второго фактора")
		}
	})

	t.Run("pending_deletion: сначала 2FA, затем токен восстановления", func(t *testing.T) {
		uid := seedUser(t, e.owner, "pending_user", "ru")
		seedMfa(t, e, uid)
		setStatus(t, e, uid, "pending_deletion")
		out, err := finish(t, e, uid, c, false, false)
		if err != nil || out.Status != StatusMfaRequired || out.RestoreToken != "" {
			t.Fatalf("без фактора: %+v %v", out, err)
		}
		out, err = finish(t, e, uid, c, true, false)
		if err != nil || out.Status != StatusDeletionPending || out.RestoreToken == "" || out.Issued != nil ||
			out.PurgeAt.IsZero() || !out.RestoreExpires.Equal(e.clock.Now().Add(10*time.Minute)) {
			t.Fatalf("после фактора: %+v %v", out, err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM auth_challenges WHERE user_id = $1 AND purpose = 'deletion_restore'
			AND token_hash = $2 AND (data->>'mfa_done')::boolean`, uid, domain.HashStepToken(out.RestoreToken)); n != 1 {
			t.Fatal("restore_token: нет строки с mfa_done")
		}
		if sessions(t, e, uid) != 0 {
			t.Fatal("сессия в периоде удаления")
		}
	})

	t.Run("banned: истёкшее неснятое ограничение — не blocked_until", func(t *testing.T) {
		uid := seedUser(t, e.owner, "banned_expired", "ru")
		setStatus(t, e, uid, "banned")
		if _, err := e.owner.Exec(context.Background(), `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
			VALUES (gen_random_uuid(), $1, $2, $3, gen_random_uuid(), $4)`, uid, banCapability, e.clock.Now().Add(-time.Hour),
			e.clock.Now().Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
		_, err := finish(t, e, uid, c, false, false)
		var pe httpx.ProblemError
		if !errors.As(err, &pe) || pe.Problem().Code != identity.CodeAccountBlocked || pe.Problem().BlockedUntil != nil {
			t.Fatalf("истёкшее ограничение: %v", err)
		}
	})

	t.Run("deleted — errAccountDeleted без сессии", func(t *testing.T) {
		uid := seedUser(t, e.owner, "deleted_user", "ru")
		setStatus(t, e, uid, "deleted")
		if _, err := finish(t, e, uid, c, true, false); !errors.Is(err, errAccountDeleted) {
			t.Fatalf("deleted: %v", err)
		}
		if sessions(t, e, uid) != 0 {
			t.Fatal("сессия удалённому")
		}
	})

	for _, status := range []string{"active", "suspended"} {
		t.Run(status, func(t *testing.T) {
			uid := seedUser(t, e.owner, status+"_user", "ru")
			setStatus(t, e, uid, status)
			out, err := finish(t, e, uid, c, false, false)
			if err != nil || out.Status != StatusAuthenticated || out.Issued == nil || out.User.ID != uid {
				t.Fatalf("%+v %v", out, err)
			}
			if count(t, e.owner, `SELECT count(*) FROM security_events WHERE user_id = $1 AND kind = 'login' AND session_id = $2`,
				uid, out.Issued.SessionID) != 1 || count(t, e.owner, `SELECT count(*) FROM device_accounts WHERE user_id = $1`, uid) != 1 {
				t.Fatal("нет журнала входа или связи с устройством")
			}
		})
	}

	t.Run("вход со вторым фактором ставит mfa_at", func(t *testing.T) {
		uid := seedUser(t, e.owner, "mfa_done_user", "ru")
		seedMfa(t, e, uid)
		out, err := finish(t, e, uid, c, true, false)
		if err != nil || out.Status != StatusAuthenticated {
			t.Fatalf("%+v %v", out, err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND mfa_at = $2`, out.Issued.SessionID, e.clock.Now()); n != 1 {
			t.Fatal("mfa_at не записан")
		}
	})
}

// seedPasskey — ключ доступа аккаунта (для methods шага 2FA).
func seedPasskey(t *testing.T, e *env, uid uuid.UUID) {
	t.Helper()
	cred := make([]byte, 32)
	_, _ = rand.Read(cred)
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count,
		backup_eligible, backup_state, attestation_format, name, created_at)
		VALUES (gen_random_uuid(), $1, $2, '\x01', gen_random_uuid(), 0, false, false, 'none', 'ключ', now())`, uid, cred); err != nil {
		t.Fatal(err)
	}
}

// m3: флаг auth_passkeys_login читается в tx входа, а не пулом — на пуле в одно соединение (его держит tx входа)
// шаг 2FA не ждёт второго соединения.
func TestMfaStepReadsFlagsInTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(d *identity.Deps) {
		cfg := d.Pool.Config()
		cfg.MaxConns = 1
		p, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		d.Pool, d.Flags = p, flags.NewStore(p)
	})
	uid := seedUser(t, e.owner, "flags_in_tx", "ru")
	seedMfa(t, e, uid)
	seedPasskey(t, e, uid)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out LoginOutcome
	err := db.InTx(ctx, e.a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := e.a.lockUser(ctx, tx, uid)
		if err != nil {
			return err
		}
		out, err = e.a.finishLogin(ctx, tx, u, web(""), uuid.Nil, domain.MethodPassword, false, false)
		return err
	})
	if err != nil || !slices.Equal(out.Methods, []string{"totp", "recovery_code", "passkey"}) {
		t.Fatalf("шаг 2FA на пуле в одно соединение: %+v %v", out.Methods, err)
	}
}

// R17 через finishLogin: после 101-го входа живых сессий 100, старшая по last_seen_at — revoked.
func TestFinishLoginKeeps100Live(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "many_logins", "ru")
	var first uuid.UUID
	for i := range domain.MaxLiveSessions + 1 {
		e.clock.Advance(time.Second)
		out, err := finish(t, e, uid, web(""), false, false)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = out.Issued.SessionID
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != domain.MaxLiveSessions {
		t.Fatalf("живых %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_reason = 'revoked'`, first); n != 1 {
		t.Fatal("старшая не отозвана")
	}
}

// §3.3: methods шага 2FA — без ключей [totp, recovery_code]; с ключом и флагом auth_passkeys_login — + passkey;
// аварийный флаг выключен — без passkey; живой сброс 2FA — reset_at.
func TestMfaChallengeMethods(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "methods_user", "ru")
	seedMfa(t, e, uid)
	c := web("m")
	methods := func() LoginOutcome {
		out, err := finish(t, e, uid, c, false, false)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := methods().Methods; !slices.Equal(got, []string{"totp", "recovery_code"}) {
		t.Fatalf("без ключей: %v", got)
	}
	seedPasskey(t, e, uid)
	if got := methods().Methods; !slices.Equal(got, []string{"totp", "recovery_code", "passkey"}) {
		t.Fatalf("с ключом, флаг по умолчанию включён: %v", got)
	}
	if _, err := e.owner.Exec(context.Background(), `UPDATE feature_flags SET enabled_globally = false WHERE key = 'auth_passkeys_login'`); err != nil {
		t.Fatal(err)
	}
	if got := methods().Methods; slices.Contains(got, "passkey") {
		t.Fatalf("флаг выключен: %v", got)
	}
	if _, err := e.owner.Exec(context.Background(), `UPDATE feature_flags SET enabled_globally = true WHERE key = 'auth_passkeys_login'`); err != nil {
		t.Fatal(err)
	}
	if got := methods().Methods; !slices.Equal(got, []string{"totp", "recovery_code", "passkey"}) {
		t.Fatalf("с ключом и флагом: %v", got)
	}
	at := e.clock.Now().Add(7 * 24 * time.Hour)
	if _, err := e.owner.Exec(context.Background(), `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id,
		requested_by_label, reason, execute_at, created_at)
		VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Поддержка', 'потерян телефон', $2, now())`, uid, at); err != nil {
		t.Fatal(err)
	}
	if got := methods().ResetAt; got == nil || !got.Equal(at) {
		t.Fatalf("reset_at %v", got)
	}
}
