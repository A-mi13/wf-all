package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/db"
)

// setRestrictions — SetRestrictionsInTx в своей транзакции пула роли api (R13).
func setRestrictions(e *env, uid uuid.UUID, ch RestrictionsChange) (RestrictionsResult, error) {
	var res RestrictionsResult
	err := db.InTx(context.Background(), e.a.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = e.a.SetRestrictionsInTx(ctx, tx, uid, ch)
		return err
	})
	return res, err
}

// banFor — бан со строкой login источника source до until (nil — бессрочно).
func banFor(source uuid.UUID, until *time.Time) RestrictionsChange {
	return RestrictionsChange{SourceID: source, Status: StatusBanned,
		Add: []Restriction{{Capability: banCapability, Until: until}}}
}

// userState — статус, статус до удаления ("" — NULL), purge_at и версия аккаунта.
func userState(t *testing.T, e *env, uid uuid.UUID) (status, before string, purge *time.Time, ver int64) {
	t.Helper()
	var b *string
	if err := e.owner.QueryRow(context.Background(), `SELECT status::text, status_before_deletion::text, purge_at, version
		FROM users WHERE id = $1`, uid).Scan(&status, &b, &purge, &ver); err != nil {
		t.Fatal(err)
	}
	if b != nil {
		before = *b
	}
	return status, before, purge, ver
}

// restrictionsPayload — payload последнего события restrictions_changed аккаунта.
func restrictionsPayload(t *testing.T, e *env, uid uuid.UUID) (identity.RestrictionsChangedPayload, int64) {
	t.Helper()
	var raw []byte
	var ver int64
	if err := e.owner.QueryRow(context.Background(), `SELECT payload, aggregate_version FROM outbox
		WHERE event_type = $1 AND aggregate_id = $2 ORDER BY aggregate_version DESC LIMIT 1`,
		identity.EventRestrictionsChanged, uid).Scan(&raw, &ver); err != nil {
		t.Fatal(err)
	}
	var p identity.RestrictionsChangedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p, ver
}

// §10.2: бан со строкой login → banned, все сессии отозваны (banned), события sessions_revoked и restrictions_changed,
// версия растёт; бан без строки login → ErrBanNeedsLogin, ничего не записано (SAVEPOINT в tx вызывающего).
func TestSetRestrictionsBan(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "ban_me", "ru")
	issue(t, e, uid, web("a"))
	issue(t, e, uid, web("b"))
	_, _, _, ver0 := userState(t, e, uid)

	// без строки login — отказ без записей, и транзакция вызывающего остаётся годной (её запись коммитится)
	err := db.InTx(context.Background(), e.a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.a.SetRestrictionsInTx(ctx, tx, uid, RestrictionsChange{SourceID: uuid.New(), Status: StatusBanned,
			Add: []Restriction{{Capability: "chat"}}}); !errors.Is(err, ErrBanNeedsLogin) {
			t.Errorf("бан без login: %v", err)
		}
		_, err := tx.Exec(ctx, `UPDATE users SET locale = 'en' WHERE id = $1`, uid)
		return err
	})
	if err != nil {
		t.Fatalf("tx вызывающего после отказа: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("после отказа строк ограничений %d", n)
	}
	if st, _, _, ver := userState(t, e, uid); st != "active" || ver != ver0 ||
		count(t, e.owner, `SELECT count(*) FROM outbox WHERE aggregate_id = $1`, uid) != 0 {
		t.Fatalf("после отказа: %s, версия %d (было %d)", st, ver, ver0)
	}

	until := testNow.Add(48 * time.Hour)
	res, err := setRestrictions(e, uid, banFor(uuid.New(), &until))
	if err != nil {
		t.Fatal(err)
	}
	st, _, _, ver := userState(t, e, uid)
	if st != "banned" || res.Status != "banned" || res.RevokedSessions != 2 || res.Version != ver || ver != ver0+2 {
		t.Fatalf("бан: %s %+v, версия %d (было %d)", st, res, ver, ver0)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND (revoked_at IS NULL
		OR revoked_reason <> 'banned')`, uid); n != 0 {
		t.Fatalf("живых или не banned сессий %d", n)
	}
	p, pv := restrictionsPayload(t, e, uid)
	if p.Status != "banned" || !slices.Equal(p.Capabilities, []string{banCapability}) || pv != ver0+1 {
		t.Fatalf("restrictions_changed %+v v%d", p, pv)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1 AND aggregate_id = $2
		AND aggregate_version = $3 AND payload->>'reason' = 'banned'`, identity.EventSessionsRevoked, uid, ver); n != 1 {
		t.Fatal("нет sessions_revoked")
	}
	// бан закрывает вход: загрузчик и вход видят banned
	if _, err := setRestrictions(e, uid, RestrictionsChange{Status: "pending_deletion"}); err == nil {
		t.Fatal("статус pending_deletion через ограничения")
	}
}

// R12: снятие строк источника, на котором держится последняя живая login, у забаненного → active и событие; пока жива
// login другого источника — остаётся banned.
func TestSetRestrictionsLiftReturnsActive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "lift_me", "ru")
	first, second := uuid.New(), uuid.New()
	if _, err := setRestrictions(e, uid, banFor(first, nil)); err != nil {
		t.Fatal(err)
	}
	later := testNow.Add(time.Hour)
	if _, err := setRestrictions(e, uid, banFor(second, &later)); err != nil {
		t.Fatal(err)
	}
	res, err := setRestrictions(e, uid, RestrictionsChange{SourceID: first, LiftSource: true})
	if err != nil || res.Status != "banned" {
		t.Fatalf("снят один из двух банов: %+v %v", res, err)
	}
	res, err = setRestrictions(e, uid, RestrictionsChange{SourceID: second, LiftSource: true})
	if err != nil || res.Status != "active" || len(res.Capabilities) != 0 {
		t.Fatalf("снят последний бан: %+v %v", res, err)
	}
	st, _, _, ver := userState(t, e, uid)
	p, pv := restrictionsPayload(t, e, uid)
	if st != "active" || p.Status != "active" || len(p.Capabilities) != 0 || pv != ver {
		t.Fatalf("после снятия: %s %+v v%d/%d", st, p, pv, ver)
	}
}

// R42: login без бана у active → ErrLoginNeedsBan без записей; к уже забаненному — можно; явный active при живой
// login — тоже отказ.
func TestSetRestrictionsLoginNeedsBan(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "login_only", "ru")
	if _, err := setRestrictions(e, uid, RestrictionsChange{SourceID: uuid.New(),
		Add: []Restriction{{Capability: banCapability}}}); !errors.Is(err, ErrLoginNeedsBan) {
		t.Fatalf("login без бана: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM user_restrictions WHERE user_id = $1`, uid); n != 0 {
		t.Fatalf("строк после отказа %d", n)
	}
	if _, err := setRestrictions(e, uid, banFor(uuid.New(), nil)); err != nil {
		t.Fatal(err)
	}
	if res, err := setRestrictions(e, uid, RestrictionsChange{SourceID: uuid.New(),
		Add: []Restriction{{Capability: banCapability}}}); err != nil || res.Status != "banned" {
		t.Fatalf("login к забаненному: %+v %v", res, err)
	}
	if _, err := setRestrictions(e, uid, RestrictionsChange{Status: StatusActive}); !errors.Is(err, ErrLoginNeedsBan) {
		t.Fatalf("active при живой login: %v", err)
	}
	if _, err := setRestrictions(e, uuid.New(), RestrictionsChange{Status: StatusActive}); !errors.Is(err, identity.ErrUserNotFound) {
		t.Fatalf("нет аккаунта: %v", err)
	}
	acctExec(t, e, `UPDATE users SET status = 'deleted' WHERE id = $1`, uid)
	if _, err := setRestrictions(e, uid, RestrictionsChange{Status: StatusActive}); !errors.Is(err, ErrUserDeleted) {
		t.Fatalf("удалённый: %v", err)
	}
}

// §17, R11, R36: бан поверх периода удаления — status остаётся pending_deletion, бан в status_before_deletion, purge_at
// прежний; «заблокирован» везде (вход паролем и со вторым фактором — 403, шаг 2FA и отмена удаления токенами до бана —
// 410, сброс — приманка, код до бана — 410); ровно в until бан уже не действует; чистка возвращает
// status_before_deletion = active, вход — deletion_pending, отмена — active.
func TestBanOverPendingDeletion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := loginUser(t, e, "bpd_plain")
	mfaUID := loginUser(t, e, "bpd_mfa")
	secret := seedMfa(t, e, mfaUID)
	setStatus(t, e, uid, "pending_deletion")
	setStatus(t, e, mfaUID, "pending_deletion")
	_, _, purge0, _ := userState(t, e, uid)

	// до бана: токен отмены удаления, шаг 2FA, код сброса пароля
	out, err := e.a.Login(context.Background(), "bpd_plain@example.com", "secret", iphone)
	if err != nil || out.Status != StatusDeletionPending {
		t.Fatalf("вход до бана: %+v %v", out, err)
	}
	restore := out.RestoreToken
	mfaTok := passwordStep(t, e, "bpd_mfa")
	sent := startReset(t, e, "bpd_plain@example.com")
	code := plainCode(t, e, sent.CodeID)
	mfaSent := startReset(t, e, "bpd_mfa@example.com")
	mfaCode := plainCode(t, e, mfaSent.CodeID)

	until := testNow.Add(48 * time.Hour)
	for _, u := range []uuid.UUID{uid, mfaUID} {
		res, err := setRestrictions(e, u, banFor(uuid.New(), &until))
		if err != nil || res.Status != "pending_deletion" || res.StatusBeforeDeletion != "banned" {
			t.Fatalf("бан поверх удаления: %+v %v", res, err)
		}
	}
	if st, before, purge, _ := userState(t, e, uid); st != "pending_deletion" || before != "banned" ||
		purge == nil || !purge.Equal(*purge0) {
		t.Fatalf("после бана: %s %s %v (было %v)", st, before, purge, purge0)
	}

	for _, email := range []string{"bpd_plain@example.com", "bpd_mfa@example.com"} {
		_, err := e.a.Login(context.Background(), email, "secret", iphone)
		if p := problemOf(t, err); p.Status != 403 || p.Code != identity.CodeAccountBlocked || p.BlockedUntil == nil ||
			!p.BlockedUntil.Equal(until) {
			t.Fatalf("%s: вход в бане: %+v", email, p)
		}
	}
	// фактор в бане не проверяется: и неверный TOTP — 410 (не mfa_invalid с остатком попыток), токен гаснет
	if _, err := complete(e, mfaTok, SecondFactor{TOTP: wrongCode(totpNow(e, secret))}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("шаг 2FA в бане: %v", err)
	}
	if _, err := complete(e, mfaTok, SecondFactor{TOTP: totpNow(e, secret)}); problemCode(err) != identity.CodeMfaExpired {
		t.Fatalf("шаг 2FA в бане, повтор: %v", err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), restore, iphone); problemCode(err) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("отмена удаления в бане: %v", err)
	}
	if st, before, _, _ := userState(t, e, uid); st != "pending_deletion" || before != "banned" {
		t.Fatalf("отмена удаления изменила статус: %s %s", st, before)
	}
	decoy := startReset(t, e, "bpd_plain@example.com")
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE id = $1 AND decoy AND user_id IS NULL`, decoy.CodeID); n != 1 {
		t.Fatal("сброс в бане — не приманка")
	}
	if _, err := confirmReset(e, sent, code, nil, resetNewPassword); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("код сброса до бана: %v", err)
	}
	// у аккаунта с 2FA бан отсекается до второго фактора (tx1): 410, не 400 mfa_required
	if _, err := confirmReset(e, mfaSent, mfaCode, nil, resetNewPassword); problemCode(err) != identity.CodeCodeExpired {
		t.Fatalf("код сброса до бана, аккаунт с 2FA: %v", err)
	}

	// ровно в until бан не действует (строго до until), даже до чистки — один предикат UserBlocked
	e.clock.Advance(until.Sub(e.clock.Now()))
	out, err = e.a.Login(context.Background(), "bpd_plain@example.com", "secret", iphone)
	if err != nil || out.Status != StatusDeletionPending {
		t.Fatalf("вход ровно в until: %+v %v", out, err)
	}
	// I1 ревью T7: бан истёк, чистка ещё не прошла — отмена удаления сама применяет R12 (banned → active, событие,
	// аудит ban_expired) и возвращает active, а не banned
	got, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, iphone)
	if err != nil || got.Status != StatusAuthenticated || got.User.Status != "active" {
		t.Fatalf("отмена после бана до чистки: %+v %v", got, err)
	}
	if st, before, _, _ := userState(t, e, uid); st != "active" || before != "" {
		t.Fatalf("после отмены: %s %s", st, before)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2 AND actor_role = 'system'`,
		ActionBanExpired, uid); n != 1 {
		t.Fatalf("аудит ban_expired при отмене: %d", n)
	}

	// чистка: у второго аккаунта бан истёк в периоде удаления — status_before_deletion = active, purge_at прежний;
	// аудит — с реальным status (pending_deletion)
	_, _, mfaPurge, _ := userState(t, e, mfaUID)
	w := roleApp(t, e, "worker", true)
	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, before, purge, _ := userState(t, e, mfaUID); st != "pending_deletion" || before != "active" || !purge.Equal(*mfaPurge) {
		t.Fatalf("после чистки: %s %s %v", st, before, purge)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2
		AND after->>'status' = 'pending_deletion' AND after->>'status_before_deletion' = 'active'`, ActionBanExpired, mfaUID); n != 1 {
		t.Fatalf("аудит ban_expired чистки: %d", n)
	}
	if st, _, _, _ := userState(t, e, uid); st != "active" {
		t.Fatalf("чистка после отмены: %s", st)
	}
}

// m3 ревью W1: снятие приостановки в периоде удаления меняет status_before_deletion (отмена вернёт актуальный).
func TestSetRestrictionsSuspendDuringDeletion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "susp_del", "ru")
	acctExec(t, e, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'suspended',
		deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`, uid)
	res, err := setRestrictions(e, uid, RestrictionsChange{Status: StatusActive})
	if err != nil || res.Status != "pending_deletion" || res.StatusBeforeDeletion != "active" {
		t.Fatalf("%+v %v", res, err)
	}
	if st, before, _, _ := userState(t, e, uid); st != "pending_deletion" || before != "active" {
		t.Fatalf("%s %s", st, before)
	}
}

// R12 в чистке (роль worker): истекла последняя строка login у banned → active, версия, событие, аудит
// identity.ban_expired от system; две строки login, одна истекла — остаётся banned.
func TestCleanupBanExpiryReturnsActive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	w := roleApp(t, e, "worker", true)
	one := seedUser(t, e.owner, "ban_expiry", "ru")
	two := seedUser(t, e.owner, "ban_two", "ru")
	soon, later := testNow.Add(time.Hour), testNow.Add(3*time.Hour)
	if _, err := setRestrictions(e, one, banFor(uuid.New(), &soon)); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*time.Time{&soon, &later} {
		if _, err := setRestrictions(e, two, banFor(uuid.New(), u)); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _, ver0 := userState(t, e, one)
	e.clock.Advance(time.Hour)
	if _, err := w.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _, _, ver := userState(t, e, one)
	p, pv := restrictionsPayload(t, e, one)
	if st != "active" || ver != ver0+1 || pv != ver || p.Status != "active" || len(p.Capabilities) != 0 {
		t.Fatalf("истёк бан: %s v%d (было %d), событие %+v v%d", st, ver, ver0, p, pv)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2 AND actor_role = 'system'`,
		ActionBanExpired, one); n != 1 {
		t.Fatalf("аудит ban_expired: %d", n)
	}
	if st, _, _, _ := userState(t, e, two); st != "banned" {
		t.Fatalf("вторая строка login жива, а статус %s", st)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2`, ActionBanExpired, two); n != 0 {
		t.Fatal("аудит ban_expired при живом бане")
	}
}

// §17: бан закоммичен, загрузчик сессии (кэш ≤ 5 с) ещё пропускает запрос — changePassword и deleteAccount под замком
// видят статус и отвечают 401 (как мёртвая сессия), не 500 и без записей. Бан записан в обход отзыва сессий (сессия
// жива): проверяется именно статус, не revoked_at.
func TestRequireFreshBannedIsGone(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := loginUser(t, e, "fresh_ban")
	sid := issue(t, e, uid, web("a")).SessionID
	acctExec(t, e, `UPDATE users SET status = 'banned' WHERE id = $1`, uid)
	hash := passwordHash(t, e, uid)
	if err := e.a.ChangePassword(context.Background(), sid, resetNewPassword, web("a")); problemCode(err) != "auth.unauthenticated" {
		t.Fatalf("changePassword в бане: %v", err)
	}
	if passwordHash(t, e, uid) != hash {
		t.Fatal("пароль сменён в бане")
	}
	if _, err := e.a.DeleteAccount(context.Background(), sid, "fresh_ban"); problemCode(err) != "auth.unauthenticated" {
		t.Fatalf("deleteAccount в бане: %v", err)
	}
	if st, _, _, _ := userState(t, e, uid); st != "banned" {
		t.Fatalf("статус %s", st)
	}
}

// R13: у роли admin нет UPDATE на users (К2) — SetRestrictionsInTx под ней падает 42501 (граница для спеки moderation).
func TestSetRestrictionsUnderAdminRole(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "admin_ban", "ru")
	pool := adminPool(t, e)
	ad, err := NewTx(TxDeps{Pool: pool, Clock: clock.System, River: e.a.river, Geo: e.a.geo, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(context.Background(), pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := ad.SetRestrictionsInTx(ctx, tx, uid, banFor(uuid.New(), nil))
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("под ролью admin: %v", err)
	}
}

// m1 ревью T7: пустое изменение (снимать у источника нечего, добавить нечего, статус тот же) — без версии и события.
func TestSetRestrictionsNoop(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "noop_user", "ru")
	_, _, _, ver0 := userState(t, e, uid)
	for name, ch := range map[string]RestrictionsChange{
		"пусто":         {},
		"тот же статус": {Status: StatusActive},
		"снять нечего":  {SourceID: uuid.New(), LiftSource: true},
	} {
		res, err := setRestrictions(e, uid, ch)
		if err != nil || res.Status != "active" || res.Version != ver0 {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}
	if _, _, _, ver := userState(t, e, uid); ver != ver0 || count(t, e.owner, `SELECT count(*) FROM outbox`) != 0 {
		t.Fatalf("пустое изменение записало: версия %d (было %d)", ver, ver0)
	}
}
