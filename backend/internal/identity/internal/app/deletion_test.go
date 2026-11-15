package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/queue"
)

// delCity — страна и город для заявок и допусков капитана.
func delCity(t *testing.T, e *env) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	acctExec(t, e, `INSERT INTO countries (code, name, currency, default_locale, phone_prefix, week_starts_on,
		min_signup_age, age_of_majority, is_enabled) VALUES ('ZD', 'Страна ZD', 'XXX', 'ru', '+999', 1, 14, 18, true)`)
	var id uuid.UUID
	if err := e.owner.QueryRow(ctx, `INSERT INTO cities (country_id, name, slug, timezone, status, centroid)
		SELECT id, 'Город ZD', 'zd-city', 'Europe/Moscow', 'live', ST_SetSRID(ST_MakePoint(41.9, 45.0), 4326)::geography
		FROM countries WHERE code = 'ZD' RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// delWorker — сценарии воркера (NewMaintenance) на той же базе под ролью worker и с тем же кольцом секретов.
func delWorker(t *testing.T, e *env) *App {
	t.Helper()
	return delWorkerLog(t, e, slog.New(slog.DiscardHandler))
}

// anonymizeDone — одна пачка AnonymizeDue без пропуска: анонимизировано строк (выбрано минус упавшие).
func anonymizeDone(a *App, batch int) (int, error) {
	selected, failed, err := a.AnonymizeDue(context.Background(), batch, nil)
	return selected - len(failed), err
}

// delWorkerLog — delWorker с журналом log.
func delWorkerLog(t *testing.T, e *env, log *slog.Logger) *App {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(e.owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE worker")
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewMaintenance(pool, e.clock, e.a.secrets, ins, log)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// delMailAddress — адрес письма kind: ref задачи → security_events.meta_enc, расшифрованный кольцом (как Composer).
func delMailAddress(t *testing.T, e *env, kind string) string {
	t.Helper()
	var args []byte
	if err := e.owner.QueryRow(context.Background(), `SELECT args FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1`,
		kind).Scan(&args); err != nil {
		t.Fatalf("письмо %s: %v", kind, err)
	}
	var a struct {
		Ref uuid.UUID `json:"ref"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		t.Fatal(err)
	}
	var ct []byte
	var keyID *string
	if err := e.owner.QueryRow(context.Background(), `SELECT meta_enc, key_id FROM security_events WHERE id = $1`, a.Ref).
		Scan(&ct, &keyID); err != nil {
		t.Fatal(err)
	}
	if ct == nil || keyID == nil {
		return ""
	}
	pt, err := e.a.secrets.Open(ct, *keyID, a.Ref[:])
	if err != nil {
		t.Fatal(err)
	}
	return string(pt)
}

// delSeed — аккаунт с паролем, 2FA-строками, согласиями, допуском капитана, заявкой (с телефоном и полями формы),
// историей ника, полями профиля с ПД и сессией; id сессии.
func delSeed(t *testing.T, e *env, nick string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	uid := seedUser(t, e.owner, nick, "ru")
	city := delCity(t, e)
	acctExec(t, e, `UPDATE users SET city_id = $2, display_name = 'Иван Петров', secondary_position = 'GK',
		confirmed_skill = 'experienced', claimed_skill = 'semi_pro', preferred_foot = 'left', height_cm = 182,
		shirt_number = 10, favourite_club = 'Динамо', motto = 'до последнего', timezone = 'Europe/Moscow',
		webauthn_handle = decode(repeat('ab', 32), 'hex'), last_login_at = now(), phone = '+79990001122',
		phone_verified_at = now() WHERE id = $1`, uid, city)
	acctExec(t, e, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h:pw', now())`, uid)
	acctExec(t, e, `INSERT INTO consents (user_id, document, version, accepted_at, client_kind) VALUES ($1, 'terms', '2026-11-01', now(), 'web')`, uid)
	acctExec(t, e, `INSERT INTO captain_applications (user_id, city_id, phone, note, contact_link, proposed_format,
		proposed_pitch, play_frequency) VALUES ($1, $2, '+79990001122', 'звоните после семи', 'https://t.me/ivan', '5x5',
		'поле у школы 12', 'по средам')`, uid, city)
	acctExec(t, e, `INSERT INTO nickname_changes (user_id, old_value, new_value, created_at) VALUES ($1, 'old_nick', $2, now())`, uid, nick)
	acctExec(t, e, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, 'captain', 'city', $2)`, uid, city)
	sid := issue(t, e, uid, web("a")).SessionID
	issue(t, e, uid, web("b"))
	return uid, sid
}

func delOutbox(t *testing.T, e *env, typ string) int {
	t.Helper()
	return count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`, typ)
}

// §3.11 п. 1: аккаунт скрыт, все сессии отозваны (account_deleted), заявка withdrawn, допуски живы, событие
// user_deletion_scheduled (не user_deleted), письмо с адресом в meta_enc, аудит.
func TestDeleteAccountHides(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "del_user")
	purge, err := e.a.DeleteAccount(context.Background(), sid, " DEL_USER ")
	if err != nil {
		t.Fatal(err)
	}
	if !purge.Equal(testNow.Add(domain.DeletionGrace)) {
		t.Fatalf("purge_at %v", purge)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM identity_read_users WHERE id = $1 AND hidden AND status = 'pending_deletion'`, uid); n != 1 {
		t.Fatal("аккаунт не скрыт")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND (revoked_at IS NULL OR revoked_reason <> 'account_deleted')`, uid); n != 0 {
		t.Fatalf("живых сессий %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE user_id = $1 AND status = 'withdrawn' AND closed_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("заявка не отозвана")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 1 {
		t.Fatal("допуск капитана тронут до анонимизации")
	}
	if delOutbox(t, e, identity.EventUserDeletionScheduled) != 1 || delOutbox(t, e, identity.EventUserDeleted) != 0 {
		t.Fatal("события запроса")
	}
	var payload []byte
	if err := e.owner.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE event_type = $1`,
		identity.EventUserDeletionScheduled).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"purge_at"`) || strings.Contains(string(payload), "example.com") {
		t.Fatalf("payload %s", payload)
	}
	if got := delMailAddress(t, e, MailAccountDeletionScheduled); got != "del_user@example.com" {
		t.Fatalf("адрес письма %q", got)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2`, ActionUserDeletionRequested, uid); n != 1 {
		t.Fatal("аудит")
	}
}

// Ник не совпал — 422 без изменений; без свежего входа — 403 reauth_required раньше сверки ника.
func TestDeleteConfirmMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "mismatch_user")
	if _, err := e.a.DeleteAccount(context.Background(), sid, "other_nick"); problemCode(err) != identity.CodeConfirmMismatch {
		t.Fatalf("несовпадение: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'active'`, uid); n != 1 {
		t.Fatal("аккаунт изменён")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 2 {
		t.Fatal("сессии изменены")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox`); n != 0 {
		t.Fatalf("событий %d", n)
	}
	acctExec(t, e, `UPDATE sessions SET reauth_at = $2 WHERE id = $1`, sid, testNow.Add(-11*time.Minute))
	if _, err := e.a.DeleteAccount(context.Background(), sid, "other_nick"); problemCode(err) != identity.CodeReauthRequired {
		t.Fatalf("без свежего входа: %v", err)
	}
}

// Живой код смены почты и живой сброс 2FA закрыты; сброс — cancelled_by = system, событие mfa_reset_cancelled
// (версия агрегата 2), письма о сбросе нет.
func TestDeleteAccountClosesCodesAndReset(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "codes_user")
	acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, user_id, decoy, code_mac, code_enc, key_id, last_sent_at,
		expires_at, ip, created_at) VALUES (gen_random_uuid(), 'email_change', 'codes.new@example.com', $1, false, $2, '\x01',
		'AAAAAAAAAAA', now(), now() + interval '1 hour', '203.0.113.7', now())`, uid, make([]byte, 32))
	acctExec(t, e, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		execute_at, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Ольга', 'потерян телефон', $2, now())`,
		uid, testNow.Add(domain.MfaResetDelay))
	if _, err := e.a.DeleteAccount(context.Background(), sid, "codes_user"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE user_id = $1 AND revoked_at IS NOT NULL AND code_enc IS NULL`, uid); n != 1 {
		t.Fatal("код не отозван")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM mfa_resets WHERE user_id = $1 AND cancelled_by = 'system' AND cancelled_at IS NOT NULL`, uid); n != 1 {
		t.Fatal("сброс не отменён")
	}
	var ver int64
	if err := e.owner.QueryRow(context.Background(), `SELECT aggregate_version FROM outbox WHERE event_type = $1`,
		identity.EventMfaResetCancelled).Scan(&ver); err != nil || ver != 2 {
		t.Fatalf("событие отмены сброса: %d %v", ver, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1`, MailMfaResetCancelled); n != 0 {
		t.Fatal("письмо об отмене сброса системой")
	}
}

// §3.11 п. 2: вход в период — deletion_pending без сессии; cancelDeletion — active, сессия, событие, письмо, аудит.
func TestLoginDuringGrace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "grace_user")
	if _, err := e.a.DeleteAccount(context.Background(), sid, "grace_user"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(24 * time.Hour)
	out, err := finish(t, e, uid, web("a"), false, false)
	if err != nil || out.Status != StatusDeletionPending || out.Issued != nil || out.RestoreToken == "" {
		t.Fatalf("%+v %v", out, err)
	}
	live := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid)
	got, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a"))
	if err != nil || got.Status != StatusAuthenticated || got.Issued == nil {
		t.Fatalf("%+v %v", got, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'active' AND purge_at IS NULL
		AND deletion_requested_at IS NULL`, uid); n != 1 {
		t.Fatal("аккаунт не восстановлен")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != live+1 {
		t.Fatalf("сессий %d", n)
	}
	if delOutbox(t, e, identity.EventUserDeletionCancelled) != 1 {
		t.Fatal("событие отмены")
	}
	if got := delMailAddress(t, e, MailAccountRestored); got != "" {
		t.Fatalf("письмо account_restored с адресом в журнале: %q", got)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2`, ActionUserDeletionCancelled, uid); n != 1 {
		t.Fatal("аудит")
	}
}

// restore_token одноразовый и живёт 10 минут; мобильный без device — 422, токен не тратится.
func TestCancelDeletionTokenOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "once_user")
	if _, err := e.a.DeleteAccount(context.Background(), sid, "once_user"); err != nil {
		t.Fatal(err)
	}
	out, err := finish(t, e, uid, web("a"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, Client{Kind: domain.ClientMobile}); problemCode(err) != identity.CodeDeviceRequired {
		t.Fatalf("без device: %v", err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a")); problemCode(err) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("повтор: %v", err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), "unknown-token", web("a")); problemCode(err) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("чужой токен: %v", err)
	}
	// новый запрос удаления и токен, истёкший ровно в срок
	sid2 := issue(t, e, uid, web("c")).SessionID
	if _, err := e.a.DeleteAccount(context.Background(), sid2, "once_user"); err != nil {
		t.Fatal(err)
	}
	// прежний токен погашен: в новом периоде удаления (статус снова pending_deletion) он не действует
	if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a")); problemCode(err) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("прежний токен в новом периоде: %v", err)
	}
	out, err = finish(t, e, uid, web("a"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(10 * time.Minute)
	if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a")); problemCode(err) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("через 10 минут: %v", err)
	}
}

// §3.11 п. 3 под ролью worker: заглушки почты и ника, секреты и привязки удалены, device_accounts живы, допуски
// отозваны, старый ник в карантине, user_deleted, письмо по meta_enc новой записи; повтор — ничего.
func TestAnonymizeDue(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "anon_user")
	upsert(t, e, mobile("ios", "anon-install"))
	acctExec(t, e, `INSERT INTO device_accounts (device_id, user_id, signed_up, first_seen_at, last_seen_at)
		SELECT id, $1, true, now(), now() FROM devices LIMIT 1`, uid)
	acctExec(t, e, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at) VALUES ($1, '\x00', 'AAAAAAAAAAA', now(), now())`, uid)
	acctExec(t, e, `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, created_at) VALUES ($1, $2, 'AAAAAAAAAAA', now())`, uid, make([]byte, 32))
	acctExec(t, e, `INSERT INTO auth_codes (id, purpose, email, decoy, last_sent_at, expires_at, ip, created_at)
		VALUES (gen_random_uuid(), 'signup', 'anon_user@example.com', true, now(), now() + interval '1 hour', '203.0.113.7', now())`)
	if _, err := e.a.DeleteAccount(context.Background(), sid, "anon_user"); err != nil {
		t.Fatal(err)
	}
	w := delWorker(t, e)
	e.clock.Advance(domain.DeletionGrace - time.Second)
	if n, err := anonymizeDone(w, 10); err != nil || n != 0 {
		t.Fatalf("до срока: %d %v", n, err)
	}
	e.clock.Advance(time.Second)
	if n, err := anonymizeDone(w, 10); err != nil || n != 1 {
		t.Fatalf("в срок: %d %v", n, err)
	}
	var email, nick, status string
	var deletedAt *time.Time
	if err := e.owner.QueryRow(context.Background(), `SELECT email::text, nickname, status::text, deleted_at FROM users WHERE id = $1`, uid).
		Scan(&email, &nick, &status, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if email != "deleted+"+uid.String()+"@invalid" || nick != domain.DeletedNickname(uid) || status != "deleted" || deletedAt == nil {
		t.Fatalf("заглушки: %s %s %s %v", email, nick, status, deletedAt)
	}
	for _, tbl := range []string{"credentials", "user_mfa", "user_recovery_codes", "passkeys", "user_identities", "consents", "auth_codes"} {
		if n := count(t, e.owner, `SELECT count(*) FROM `+tbl+` WHERE user_id = $1`, uid); n != 0 {
			t.Errorf("%s: строк %d", tbl, n)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM auth_codes WHERE email = 'anon_user@example.com'`); n != 0 {
		t.Error("код с прежней почтой")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM device_accounts WHERE user_id = $1`, uid); n != 1 {
		t.Error("device_accounts удалены")
	}
	// решение пользователя 15.11: анонимизация затирает все ПД (I3 — история ника тоже)
	for _, tbl := range []string{"nickname_changes", "sessions"} {
		if n := count(t, e.owner, `SELECT count(*) FROM `+tbl+` WHERE user_id = $1`, uid); n != 0 {
			t.Errorf("%s: строк %d", tbl, n)
		}
	}
	var pii string
	if err := e.owner.QueryRow(context.Background(), `SELECT concat_ws(',', birth_date, display_name, city_id,
		secondary_position, confirmed_skill, preferred_foot, height_cm, shirt_number, favourite_club, motto, timezone,
		webauthn_handle, last_login_at, phone, phone_verified_at, status_before_deletion, deletion_requested_at, purge_at)
		|| '|' || primary_position || '|' || claimed_skill FROM users WHERE id = $1`, uid).Scan(&pii); err != nil {
		t.Fatal(err)
	}
	if pii != "|ANY|amateur" {
		t.Errorf("ПД в users: %q", pii)
	}
	if err := e.owner.QueryRow(context.Background(), `SELECT string_agg(concat_ws(',', phone, note, contact_link,
		proposed_format, proposed_pitch, play_frequency, comment), ';') FROM captain_applications WHERE user_id = $1`,
		uid).Scan(&pii); err != nil {
		t.Fatal(err)
	}
	if pii != "" {
		t.Errorf("ПД в заявке: %q", pii)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM security_events WHERE user_id = $1 AND kind <> 'account_deleted'`, uid); n != 0 {
		t.Errorf("события безопасности с IP и UA: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoked_at IS NOT NULL AND revoke_reason = 'account_deleted'`, uid); n != 1 {
		t.Error("допуск не отозван")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_reservations WHERE nickname_normalized = normalize_text('anon_user')
		AND previous_user_id = $1 AND expires_at = $2`, uid, e.clock.Now().Add(domain.NicknameQuarantine)); n != 1 {
		t.Error("ник не в карантине")
	}
	if delOutbox(t, e, identity.EventUserDeleted) != 1 {
		t.Error("user_deleted")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_role = 'system' AND object_id = $2`, ActionUserDeleted, uid); n != 1 {
		t.Error("аудит system")
	}
	if got := delMailAddress(t, e, MailAccountDeleted); got != "anon_user@example.com" {
		t.Errorf("адрес письма %q", got)
	}
	// повтор — ничего
	if n, err := anonymizeDone(w, 10); err != nil || n != 0 {
		t.Fatalf("повтор: %d %v", n, err)
	}
	if delOutbox(t, e, identity.EventUserDeleted) != 1 {
		t.Error("повтор опубликовал событие")
	}
}

// Пачки под ролью worker: три аккаунта, batch = 2 — 2, затем 1, затем 0 (права §5.5: запись в таблицу, которой у
// worker нет, — ошибка).
func TestAnonymizeAsWorker(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, nick := range []string{"w_one", "w_two", "w_three"} {
		uid := seedUser(t, e.owner, nick, "ru")
		sid := issue(t, e, uid, web(nick)).SessionID
		if _, err := e.a.DeleteAccount(context.Background(), sid, nick); err != nil {
			t.Fatal(err)
		}
	}
	w := delWorker(t, e)
	e.clock.Advance(domain.DeletionGrace)
	for i, want := range []int{2, 1, 0} {
		n, err := anonymizeDone(w, 2)
		if err != nil || n != want {
			t.Fatalf("пачка %d: %d %v", i, n, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE status = 'deleted'`); n != 3 {
		t.Fatalf("удалено %d", n)
	}
}

// I3: адрес письма account_deleted — из записи анонимизации, а не из записи запроса (её meta_enc чистка стирает
// через 7 дней). Чистку (Task 19) замещает стирание meta_enc записей старше 7 дней.
func TestAnonymizeMailAfterCleanup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "clean_user", "ru")
	sid := issue(t, e, uid, web("a")).SessionID
	if _, err := e.a.DeleteAccount(context.Background(), sid, "clean_user"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(8 * 24 * time.Hour)
	acctExec(t, e, `UPDATE security_events SET meta_enc = NULL WHERE created_at < $1`, e.clock.Now().Add(-7*24*time.Hour))
	e.clock.Advance(domain.DeletionGrace - 8*24*time.Hour)
	if n, err := anonymizeDone(delWorker(t, e), 10); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if got := delMailAddress(t, e, MailAccountDeleted); got != "clean_user@example.com" {
		t.Fatalf("адрес письма %q", got)
	}
}

// Гонка отмены и анонимизации при purge_at = now: отмена держит строку users — анонимизация её пропускает
// (SKIP LOCKED, не ждёт); анонимизация первой — отмена отвечает 410. Ровно один исход.
func TestAnonymizeVsCancelRace(t *testing.T) {
	t.Parallel()
	t.Run("отмена держит замок", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid := seedUser(t, e.owner, "race_cancel", "ru")
		sid := issue(t, e, uid, web("a")).SessionID
		if _, err := e.a.DeleteAccount(context.Background(), sid, "race_cancel"); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(domain.DeletionGrace - time.Minute)
		out, err := finish(t, e, uid, web("a"), false, false)
		if err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Minute) // purge_at = now
		w := delWorker(t, e)
		locked := make(chan struct{})
		anonDone := make(chan struct{})
		var anonN int
		var anonErr error
		anonWaited := false
		e.a.hooks = &hooks{afterFirstLock: func(op string) {
			if op != "cancel_deletion" {
				return
			}
			close(locked)
			select { // анонимизация с SKIP LOCKED не ждёт замка; без него — ждала бы (срок барьера)
			case <-anonDone:
			case <-time.After(3 * time.Second):
				anonWaited = true
			}
		}}
		var wg sync.WaitGroup
		wg.Go(func() {
			<-locked
			anonN, anonErr = anonymizeDone(w, 10)
			close(anonDone)
		})
		got, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a"))
		wg.Wait()
		if anonErr != nil || err != nil || got.Status != StatusAuthenticated {
			t.Fatalf("отмена %+v %v, анонимизация %d %v", got, err, anonN, anonErr)
		}
		if anonWaited {
			t.Fatal("анонимизация ждала замок отмены — нет SKIP LOCKED")
		}
		if anonN != 0 || count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'active'`, uid) != 1 {
			t.Fatalf("два исхода: анонимизировано %d, статус не active", anonN)
		}
	})
	t.Run("анонимизация первой", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		uid := seedUser(t, e.owner, "race_anon", "ru")
		sid := issue(t, e, uid, web("a")).SessionID
		if _, err := e.a.DeleteAccount(context.Background(), sid, "race_anon"); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(domain.DeletionGrace - time.Minute)
		out, err := finish(t, e, uid, web("a"), false, false)
		if err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Minute)
		if n, err := anonymizeDone(delWorker(t, e), 10); err != nil || n != 1 {
			t.Fatalf("%d %v", n, err)
		}
		if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a")); problemCode(err) != identity.CodeRestoreTokenInvalid {
			t.Fatalf("отмена после анонимизации: %v", err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 0 {
			t.Fatalf("сессий у удалённого %d", n)
		}
	})
}

// Решение пользователя 15.11: отмена удаления возвращает прежний статус — suspended → удаление → отмена = suspended
// (не «отмывка» в active); прежний статус хранится в периоде удаления и очищается отменой.
func TestCancelDeletionRestoresStatus(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "susp_user", "ru")
	acctExec(t, e, `UPDATE users SET status = 'suspended' WHERE id = $1`, uid)
	sid := issue(t, e, uid, web("a")).SessionID
	if _, err := e.a.DeleteAccount(context.Background(), sid, "susp_user"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'pending_deletion'
		AND status_before_deletion = 'suspended'`, uid); n != 1 {
		t.Fatal("прежний статус не записан")
	}
	out, err := finish(t, e, uid, web("a"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web("a"))
	if err != nil || got.Status != StatusAuthenticated || got.User.Status != "suspended" {
		t.Fatalf("%+v %v", got, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'suspended'
		AND status_before_deletion IS NULL`, uid); n != 1 {
		t.Fatal("статус после отмены не suspended")
	}
}

// m3: новый запрос удаления гасит живые restore_token прошлого периода — токен, выданный до первой отмены и не
// использованный, не отменяет второе удаление.
func TestDeleteAccountRevokesRestoreTokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "stale_tok", "ru")
	sid := issue(t, e, uid, web("a")).SessionID
	if _, err := e.a.DeleteAccount(context.Background(), sid, "stale_tok"); err != nil {
		t.Fatal(err)
	}
	stale, err := finish(t, e, uid, web("a"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	used, err := finish(t, e, uid, web("b"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), used.RestoreToken, web("b")); err != nil {
		t.Fatal(err)
	}
	sid2 := issue(t, e, uid, web("c")).SessionID
	if _, err := e.a.DeleteAccount(context.Background(), sid2, "stale_tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.CancelDeletion(context.Background(), stale.RestoreToken, web("a")); problemCode(err) != identity.CodeRestoreTokenInvalid {
		t.Fatalf("токен прошлого периода: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'pending_deletion'`, uid); n != 1 {
		t.Fatal("второе удаление отменено старым токеном")
	}
}

// m1: documents_acceptance_required ответа отмены считается в транзакции отмены (после коммита база не читается):
// без согласий — true, с принятыми текущими версиями — false.
func TestCancelDeletionDocuments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, c := range []struct {
		nick     string
		accepted bool
	}{{"docs_none", false}, {"docs_all", true}} {
		uid := seedUser(t, e.owner, c.nick, "ru")
		if c.accepted {
			for _, d := range e.a.legal.Items() {
				acctExec(t, e, `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
					VALUES ($1, $2, $3, now(), 'web')`, uid, string(d.Document), d.Version)
			}
		}
		sid := issue(t, e, uid, web(c.nick)).SessionID
		if _, err := e.a.DeleteAccount(context.Background(), sid, c.nick); err != nil {
			t.Fatal(err)
		}
		out, err := finish(t, e, uid, web(c.nick), false, false)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, web(c.nick))
		if err != nil || got.DocumentsRequired == c.accepted {
			t.Fatalf("%s: documents_acceptance_required %v %v", c.nick, got.DocumentsRequired, err)
		}
	}
}

// Отравленная пачка: строка, на которой анонимизация падает, откатывается одна (SAVEPOINT) и попадает в журнал
// (id и ошибка, без почты); остальные строки пачки анонимизированы, n — только успешные.
func TestAnonymizePoisonedRow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ids := map[string]uuid.UUID{}
	for _, nick := range []string{"p_bad", "p_one", "p_two"} { // p_bad — первой по purge_at
		uid := seedUser(t, e.owner, nick, "ru")
		ids[nick] = uid
		sid := issue(t, e, uid, web(nick)).SessionID
		if _, err := e.a.DeleteAccount(context.Background(), sid, nick); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Second)
	}
	acctExec(t, e, `CREATE FUNCTION poison_anonymize() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = 'deleted' AND OLD.nickname = 'p_bad' THEN RAISE EXCEPTION 'отравленная строка'; END IF;
			RETURN NEW;
		END $$`)
	acctExec(t, e, `CREATE TRIGGER poison_anonymize BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION poison_anonymize()`)
	var buf bytes.Buffer
	w := delWorkerLog(t, e, slog.New(slog.NewTextHandler(&buf, nil)))
	e.clock.Advance(domain.DeletionGrace)
	n, err := anonymizeDone(w, 10)
	if err != nil || n != 2 {
		t.Fatalf("пачка: %d %v", n, err)
	}
	if got := count(t, e.owner, `SELECT count(*) FROM users WHERE status = 'deleted'`); got != 2 {
		t.Fatalf("анонимизировано %d", got)
	}
	if got := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'pending_deletion'
		AND email = 'p_bad@example.com'`, ids["p_bad"]); got != 1 {
		t.Fatal("отравленная строка изменена")
	}
	if delOutbox(t, e, identity.EventUserDeleted) != 2 ||
		count(t, e.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1`, MailAccountDeleted) != 2 {
		t.Fatal("событие или письмо отравленной строки не откатились")
	}
	if !strings.Contains(buf.String(), ids["p_bad"].String()) || !strings.Contains(buf.String(), "отравленная строка") ||
		strings.Contains(buf.String(), "p_bad@example.com") {
		t.Fatalf("журнал: %s", buf.String())
	}
}

// m7: сброс 2FA, назначенный поддержкой в период отмены, анонимизация отменяет системой (событие
// mfa_reset_cancelled, без письма).
func TestAnonymizeCancelsMfaReset(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "reset_anon", "ru")
	sid := issue(t, e, uid, web("a")).SessionID
	if _, err := e.a.DeleteAccount(context.Background(), sid, "reset_anon"); err != nil {
		t.Fatal(err)
	}
	acctExec(t, e, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		ticket, execute_at, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Ольга',
		'потерян телефон', 'SUP-1', $2, now())`,
		uid, testNow.Add(domain.DeletionGrace+domain.MfaResetDelay))
	// прошлый, уже отменённый сброс — свободный текст сотрудника тоже затирается (решение пользователя 15.11)
	acctExec(t, e, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		ticket, execute_at, cancelled_at, cancelled_by, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(),
		gen_random_uuid(), 'Ольга', 'звонил Иван Петров, +79990000000', 'SUP-0', now(), now(), 'owner', now())`, uid)
	e.clock.Advance(domain.DeletionGrace)
	if n, err := anonymizeDone(delWorker(t, e), 10); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM mfa_resets WHERE user_id = $1 AND reason = $2 AND ticket IS NULL`,
		uid, mfaResetReasonErased); n != 2 {
		t.Fatalf("reason/ticket затёрты у %d из 2 сбросов", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM mfa_resets WHERE user_id = $1 AND cancelled_by = 'system'`, uid); n != 1 {
		t.Fatal("сброс 2FA не отменён")
	}
	if delOutbox(t, e, identity.EventMfaResetCancelled) != 1 {
		t.Fatal("событие отмены сброса")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1`, MailMfaResetCancelled); n != 0 {
		t.Fatal("письмо об отмене сброса системой")
	}
}

// m8: batch = 2, две отравленные строки старше нормальной — первая пачка выбирает обе и обе падают; со skip упавших
// следующая пачка выбирает нормальную и анонимизирует её (без skip отравленные заслоняли бы её навсегда).
func TestAnonymizeSkipsFailedInRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ids := map[string]uuid.UUID{}
	for _, nick := range []string{"s_bad1", "s_bad2", "s_good"} { // отравленные — первыми по purge_at
		uid := seedUser(t, e.owner, nick, "ru")
		ids[nick] = uid
		sid := issue(t, e, uid, web(nick)).SessionID
		if _, err := e.a.DeleteAccount(context.Background(), sid, nick); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Second)
	}
	acctExec(t, e, `CREATE FUNCTION poison_anonymize() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status = 'deleted' AND OLD.nickname IN ('s_bad1', 's_bad2') THEN RAISE EXCEPTION 'отравленная строка'; END IF;
			RETURN NEW;
		END $$`)
	acctExec(t, e, `CREATE TRIGGER poison_anonymize BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION poison_anonymize()`)
	w := delWorker(t, e)
	e.clock.Advance(domain.DeletionGrace)
	selected, failed, err := w.AnonymizeDue(context.Background(), 2, nil)
	if err != nil || selected != 2 || len(failed) != 2 {
		t.Fatalf("первая пачка: выбрано %d, упало %v, %v", selected, failed, err)
	}
	if !slices.Contains(failed, ids["s_bad1"]) || !slices.Contains(failed, ids["s_bad2"]) {
		t.Fatalf("упавшие %v", failed)
	}
	selected, failed, err = w.AnonymizeDue(context.Background(), 2, failed)
	if err != nil || selected != 1 || len(failed) != 0 {
		t.Fatalf("пачка со skip: выбрано %d, упало %v, %v", selected, failed, err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'deleted'`, ids["s_good"]); n != 1 {
		t.Fatal("нормальная строка не анонимизирована")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE status = 'pending_deletion'`); n != 2 {
		t.Fatalf("отравленных в ожидании %d", n)
	}
}

// m2 ревью W5-А: restore_token привязан к клиенту первого шага — другой вид клиента или другое устройство мобильного —
// 410 restore_token_invalid без записей; с исходного устройства отмена проходит.
func TestCancelDeletionClientMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "restore_client")
	if _, err := e.a.DeleteAccount(context.Background(), sid, "restore_client"); err != nil {
		t.Fatal(err)
	}
	out, err := finish(t, e, uid, iphone, false, false)
	if err != nil || out.RestoreToken == "" {
		t.Fatalf("%+v %v", out, err)
	}
	upsert(t, e, mobile("ios", "11111111-2222-3333-4444-555555555555")) // чужое устройство уже известно базе
	for name, c := range map[string]Client{"веб": web("a"), "другое устройство": mobile("ios", "11111111-2222-3333-4444-555555555555")} {
		if _, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, c); problemCode(err) != identity.CodeRestoreTokenInvalid {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM users WHERE id = $1 AND status = 'pending_deletion'`, uid); n != 1 {
		t.Fatal("удаление отменено с чужого клиента")
	}
	if res, err := e.a.CancelDeletion(context.Background(), out.RestoreToken, iphone); err != nil || res.Status != StatusAuthenticated {
		t.Fatalf("исходное устройство: %+v %v", res, err)
	}
}
