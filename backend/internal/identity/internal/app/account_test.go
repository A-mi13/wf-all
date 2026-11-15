package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/httpx"
)

// acctExec — запрос владельца без результата.
func acctExec(t *testing.T, e *env, q string, args ...any) {
	t.Helper()
	if _, err := e.owner.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// acctAvailableAt — available_at ошибки httpx (nil — поля нет).
func acctAvailableAt(err error) *time.Time {
	var pe httpx.ProblemError
	if errors.As(err, &pe) {
		return pe.Problem().AvailableAt
	}
	return nil
}

// Me — поля §6.2.4: документы (принятая и текущая версии, новая версия → documents_acceptance_required), пароль,
// 2FA, действующие ограничения, срок смены ника.
func TestMeFields(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "me_user", "en")
	acctExec(t, e, `INSERT INTO consents (user_id, document, version, accepted_at, client_kind) VALUES
		($1, 'terms', '2026-11-01', $2, 'mobile'), ($1, 'privacy', '2026-06-01', $2, 'mobile')`, uid, testNow.Add(-time.Hour))
	acctExec(t, e, `INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at) VALUES
		(gen_random_uuid(), $1, 'comment', $2, gen_random_uuid(), $3), (gen_random_uuid(), $1, 'create_match', NULL, gen_random_uuid(), $3),
		(gen_random_uuid(), $1, 'captain', $4, gen_random_uuid(), $3)`, uid, testNow.Add(24*time.Hour), testNow.Add(-48*time.Hour),
		testNow.Add(-time.Hour))
	m, err := e.a.Me(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != uid || m.Nickname != "me_user" || m.Email != "me_user@example.com" || m.Locale != "en" ||
		m.Status != "active" || m.HasPassword || m.MfaEnabled || m.NicknameChangeableAt != nil ||
		m.BirthDate.Format(time.DateOnly) != "2000-01-01" {
		t.Fatalf("Me %+v", m)
	}
	if !m.DocumentsAcceptanceRequired || len(m.Documents) != 2 {
		t.Fatalf("документы %+v", m.Documents)
	}
	terms, privacy := m.Documents[0], m.Documents[1]
	if terms.Document != domain.DocTerms || terms.AcceptedVersion == nil || *terms.AcceptedVersion != "2026-11-01" ||
		terms.CurrentVersion != "2026-11-01" || terms.URL != "https://example.com/terms" {
		t.Fatalf("terms %+v", terms)
	}
	if privacy.AcceptedVersion == nil || *privacy.AcceptedVersion != "2026-06-01" || privacy.CurrentVersion != "2026-11-01" {
		t.Fatalf("privacy %+v", privacy)
	}
	// истёкшее (captain) не в счёте
	if len(m.Restrictions) != 2 || m.Restrictions[0].Capability != "comment" || m.Restrictions[0].Until == nil ||
		m.Restrictions[1].Capability != "create_match" || m.Restrictions[1].Until != nil {
		t.Fatalf("ограничения %+v", m.Restrictions)
	}
	acctExec(t, e, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h:x', now())`, uid)
	acctExec(t, e, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, '\x00', 'AAAAAAAAAAA', now(), now())`, uid)
	acctExec(t, e, `INSERT INTO consents (user_id, document, version, accepted_at, client_kind)
		VALUES ($1, 'privacy', '2026-11-01', $2, 'web')`, uid, testNow)
	acctExec(t, e, `INSERT INTO nickname_changes (user_id, old_value, new_value, created_at) VALUES ($1, 'a', 'me_user', $2)`,
		uid, testNow.Add(-10*24*time.Hour))
	if m, err = e.a.Me(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	want := testNow.Add(20 * 24 * time.Hour)
	if !m.HasPassword || !m.MfaEnabled || m.DocumentsAcceptanceRequired || m.NicknameChangeableAt == nil ||
		!m.NicknameChangeableAt.Equal(want) {
		t.Fatalf("после изменений %+v", m)
	}
	if _, err := e.a.Me(context.Background(), uuid.New()); err == nil {
		t.Fatal("чужой id — ждали ошибку")
	}
}

// Язык — ru|en, version + 1 без события; тот же язык — без записи.
func TestUpdateMe(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "locale_user", "ru")
	m, err := e.a.UpdateMe(context.Background(), uid, "en")
	if err != nil || m.Locale != "en" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := e.a.UpdateMe(context.Background(), uid, "en"); err != nil {
		t.Fatal(err)
	}
	if v := count(t, e.owner, `SELECT version FROM users WHERE id = $1`, uid); v != 2 {
		t.Fatalf("версия %d", v)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox`); n != 0 {
		t.Fatalf("событий %d", n)
	}
	if _, err := e.a.UpdateMe(context.Background(), uid, "de"); problemCode(err) != httpx.CodeValidationFailed {
		t.Fatalf("de: %v", err)
	}
}

// Смена ника не чаще 30 дней: раньше — 409 nickname_change_too_soon с available_at; ровно через 30 дней — можно.
// Успех — событие nickname_changed с версией, аудит, история.
func TestChangeNicknameTooSoon(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "soon_user", "ru")
	m, err := e.a.ChangeNickname(context.Background(), uid, "  soon_new ")
	if err != nil || m.Nickname != "soon_new" || m.NicknameChangeableAt == nil {
		t.Fatalf("%+v %v", m, err)
	}
	var payload []byte
	var ver int64
	if err := e.owner.QueryRow(context.Background(), `SELECT payload, aggregate_version FROM outbox WHERE event_type = $1`,
		identity.EventNicknameChanged).Scan(&payload, &ver); err != nil {
		t.Fatal(err)
	}
	if ver != 2 || strings.Contains(string(payload), "soon") {
		t.Fatalf("событие %s, версия %d", payload, ver)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2`, ActionNicknameChanged, uid); n != 1 {
		t.Fatalf("аудит %d", n)
	}
	e.clock.Advance(29 * 24 * time.Hour)
	_, err = e.a.ChangeNickname(context.Background(), uid, "soon_third")
	if problemCode(err) != identity.CodeNicknameChangeTooSoon {
		t.Fatalf("через 29 дней: %v", err)
	}
	if at := acctAvailableAt(err); at == nil || !at.Equal(testNow.Add(domain.NicknameChangeEvery)) {
		t.Fatalf("available_at %v", at)
	}
	e.clock.Advance(24 * time.Hour)
	if _, err := e.a.ChangeNickname(context.Background(), uid, "soon_third"); err != nil {
		t.Fatalf("ровно 30 дней: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_changes WHERE user_id = $1`, uid); n != 2 {
		t.Fatalf("история %d", n)
	}
}

// Свой прежний ник из карантина вернуть можно (строка карантина уходит), чужой — 409; цепочка A→B→A→C без 23505.
func TestChangeNicknameOwnQuarantine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := seedUser(t, e.owner, "alpha_q", "ru")
	b := seedUser(t, e.owner, "other_q", "ru")
	if _, err := e.a.ChangeNickname(context.Background(), a, "beta_q"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.ChangeNickname(context.Background(), b, "ALPHA_Q"); problemCode(err) != identity.CodeNicknameTaken {
		t.Fatalf("чужой из карантина: %v", err)
	}
	e.clock.Advance(domain.NicknameChangeEvery)
	if _, err := e.a.ChangeNickname(context.Background(), a, "alpha_q"); err != nil {
		t.Fatalf("свой из карантина: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_reservations WHERE nickname_normalized = normalize_text('alpha_q')`); n != 0 {
		t.Fatalf("карантин своего ника остался: %d", n)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_reservations WHERE nickname_normalized = normalize_text('beta_q')
		AND previous_user_id = $1 AND expires_at = $2`, a, e.clock.Now().Add(domain.NicknameQuarantine)); n != 1 {
		t.Fatalf("карантин beta_q: %d", n)
	}
	e.clock.Advance(domain.NicknameChangeEvery)
	if _, err := e.a.ChangeNickname(context.Background(), a, "gamma_q"); err != nil {
		t.Fatalf("A→C: %v", err)
	}
	// карантин истёк — ник свободен всем
	e.clock.Advance(domain.NicknameQuarantine)
	if _, err := e.a.ChangeNickname(context.Background(), b, "beta_q"); err != nil {
		t.Fatalf("истёкший карантин: %v", err)
	}
}

// Формат (§5.8): домен и SQL length(normalize_text) < 3 (CJK и греческий нормализуются в пусто, a.b — в «ab»),
// заглушка удалённых, резерв, занятый ник — с учётом регистра и двойников.
func TestChangeNicknameInvalid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "fmt_user", "ru")
	seedUser(t, e.owner, "kosta", "ru")
	acctExec(t, e, `INSERT INTO reserved_nicknames (nickname_normalized, reason) VALUES (normalize_text('admin_wf'), 'system')`)
	for nick, code := range map[string]string{
		"李小龍": identity.CodeNicknameInvalid, "Ἀλέξης": identity.CodeNicknameInvalid, "a.b": identity.CodeNicknameInvalid,
		"deleted-0a1b2c3d4e5f": identity.CodeNicknameInvalid, "x": identity.CodeNicknameInvalid,
		"admin_wf": identity.CodeNicknameTaken, "KOSTA": identity.CodeNicknameTaken, "коsта": identity.CodeNicknameTaken,
	} {
		if _, err := e.a.ChangeNickname(context.Background(), uid, nick); problemCode(err) != code {
			t.Errorf("%q: %v, ждали %s", nick, err, code)
		}
	}
	if n := count(t, e.owner, `SELECT count(*) FROM nickname_changes`); n != 0 {
		t.Fatalf("история после отказов %d", n)
	}
}

// Гонка: два аккаунта на один свободный ник — оба прошли проверки (барьер), один 200, второй 409 (23505).
func TestChangeNicknameRace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	u1 := seedUser(t, e.owner, "race_one", "ru")
	u2 := seedUser(t, e.owner, "race_two", "ru")
	barrier := pairBarrier(3 * time.Second)
	e.a.hooks = &hooks{afterFirstLock: func(op string) {
		if op == "change_nickname" {
			barrier()
		}
	}}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, uid := range []uuid.UUID{u1, u2} {
		wg.Go(func() {
			_, errs[i] = e.a.ChangeNickname(context.Background(), uid, "race_target")
		})
	}
	wg.Wait()
	ok, taken := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case problemCode(err) == identity.CodeNicknameTaken:
			taken++
		default:
			t.Fatalf("неожиданная ошибка: %v", err)
		}
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("успехов %d, занято %d", ok, taken)
	}
}

// acceptDocuments: подмножество — с последними принятыми версиями прочих; устаревшая версия — 422 без записей;
// повтор — без ошибки; откат конфига к прежней версии не даёт вечного 422.
func TestAcceptDocumentsOutdated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "docs_user", "ru")
	acctExec(t, e, `INSERT INTO consents (user_id, document, version, accepted_at, client_kind) VALUES
		($1, 'terms', '2026-11-01', $2, 'mobile'), ($1, 'privacy', '2026-06-01', $2, 'mobile')`, uid, testNow.Add(-time.Hour))
	ctx := context.Background()
	// одна privacy устаревшей версии
	if _, err := e.a.AcceptDocuments(ctx, uid, []domain.Accepted{{Document: domain.DocPrivacy, Version: "2026-06-01"}},
		domain.ClientWeb); problemCode(err) != identity.CodeDocumentsOutdated {
		t.Fatalf("устаревшая: %v", err)
	}
	// terms текущая, privacy из consents устарела — набор не полон
	if _, err := e.a.AcceptDocuments(ctx, uid, []domain.Accepted{{Document: domain.DocTerms, Version: "2026-11-01"}},
		domain.ClientWeb); problemCode(err) != identity.CodeDocumentsOutdated {
		t.Fatalf("неполный набор: %v", err)
	}
	if _, err := e.a.AcceptDocuments(ctx, uid, []domain.Accepted{{Document: domain.DocPrivacy, Version: "2026-11-01"},
		{Document: domain.DocPrivacy, Version: "2026-11-01"}}, domain.ClientWeb); problemCode(err) != identity.CodeDocumentsOutdated {
		t.Fatalf("повтор документа: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM consents WHERE user_id = $1`, uid); n != 2 {
		t.Fatalf("записи после отказов %d", n)
	}
	m, err := e.a.AcceptDocuments(ctx, uid, []domain.Accepted{{Document: domain.DocPrivacy, Version: "2026-11-01"}}, domain.ClientWeb)
	if err != nil || m.DocumentsAcceptanceRequired {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := e.a.AcceptDocuments(ctx, uid, []domain.Accepted{{Document: domain.DocPrivacy, Version: "2026-11-01"}}, domain.ClientWeb); err != nil {
		t.Fatalf("повтор: %v", err)
	}
	var after []byte
	if err := e.owner.QueryRow(ctx, `SELECT after FROM audit_log WHERE action = $1 ORDER BY created_at LIMIT 1`,
		ActionDocumentsAccepted).Scan(&after); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(after, &got); err != nil || !strings.Contains(string(after), "2026-11-01") {
		t.Fatalf("аудит %s %v", after, err)
	}
	// конфиг откатили к 2026-06-01 (уже принятой раньше): принять её снова — и она снова последняя
	legal, err := identity.ParseLegalDocuments("terms=2026-11-01|https://example.com/terms,privacy=2026-06-01|https://example.com/privacy")
	if err != nil {
		t.Fatal(err)
	}
	e.a.legal = legal
	e.clock.Advance(time.Minute)
	if m, err = e.a.AcceptDocuments(ctx, uid, []domain.Accepted{{Document: domain.DocPrivacy, Version: "2026-06-01"}}, domain.ClientWeb); err != nil {
		t.Fatalf("откат конфига: %v", err)
	}
	if m.DocumentsAcceptanceRequired {
		t.Fatalf("после отката: %+v", m.Documents)
	}
}

// Мои способы входа: пароль, привязки и ключи из таблиц, 2FA (коды — только при включённой), живой сброс.
func TestMyAuthMethods(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "methods_user", "ru")
	v, err := e.a.MyAuthMethods(context.Background(), uid)
	if err != nil || v.HasPassword || len(v.Identities) != 0 || len(v.Passkeys) != 0 || v.Mfa.TOTPEnabled ||
		v.Mfa.RecoveryCodesLeft != 0 || v.Mfa.ResetScheduledAt != nil {
		t.Fatalf("%+v %v", v, err)
	}
	acctExec(t, e, `INSERT INTO credentials (user_id, password_hash, updated_at) VALUES ($1, 'h:x', now())`, uid)
	acctExec(t, e, `INSERT INTO user_mfa (user_id, secret_enc, key_id, confirmed_at, created_at)
		VALUES ($1, '\x00', 'AAAAAAAAAAA', $2, now())`, uid, testNow)
	for i := range 3 {
		acctExec(t, e, `INSERT INTO user_recovery_codes (user_id, code_mac, key_id, used_at, created_at)
			VALUES ($1, $2, 'AAAAAAAAAAA', $3, now())`, uid, []byte(strings.Repeat(string(rune('a'+i)), 32)),
			map[bool]*time.Time{true: &testNow, false: nil}[i == 0])
	}
	acctExec(t, e, `INSERT INTO mfa_resets (id, user_id, approval_id, requested_by_staff_id, requested_by_label, reason,
		execute_at, created_at) VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(), 'Ольга', 'потерян телефон', $2, now())`,
		uid, testNow.Add(domain.MfaResetDelay))
	acctExec(t, e, `INSERT INTO passkeys (id, user_id, credential_id, public_key, aaguid, sign_count, transports,
		backup_eligible, backup_state, attestation_format, name, created_at)
		VALUES (gen_random_uuid(), $1, '\x0102030405060708090a0b0c0d0e0f10', '\x02', gen_random_uuid(), 0, '{}', true, true, 'none', 'iPhone', now())`, uid)
	if v, err = e.a.MyAuthMethods(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	if !v.HasPassword || !v.Mfa.TOTPEnabled || v.Mfa.ConfirmedAt == nil || v.Mfa.RecoveryCodesLeft != 2 ||
		v.Mfa.ResetScheduledAt == nil || len(v.Passkeys) != 1 || v.Passkeys[0].Name != "iPhone" || !v.Passkeys[0].BackedUp {
		t.Fatalf("%+v", v)
	}
}

// Service.User: ник, статус, город, язык; удалённый — заглушка ника представления; нет аккаунта — ErrUserNotFound.
func TestServiceUser(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid := seedUser(t, e.owner, "svc_user", "en")
	s := identity.New(e.a.pool)
	u, err := s.User(context.Background(), uid)
	if err != nil || u.ID != uid || u.Nickname != "svc_user" || u.Status != "active" || u.Locale != "en" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := s.User(context.Background(), uuid.New()); !errors.Is(err, identity.ErrUserNotFound) {
		t.Fatalf("нет аккаунта: %v", err)
	}
	// m6: удалённый — ник-заглушка представления (даже если строка ещё хранит прежний ник), дата рождения затёрта
	gone := seedUser(t, e.owner, "svc_gone", "ru")
	acctExec(t, e, `UPDATE users SET status = 'deleted', deleted_at = now(), birth_date = NULL WHERE id = $1`, gone)
	u, err = s.User(context.Background(), gone)
	if err != nil || u.Nickname != domain.DeletedNickname(gone) || u.Status != "deleted" {
		t.Fatalf("удалённый: %+v %v", u, err)
	}
}

// UserSummary — без ПД: ни почты, ни телефона, ни даты рождения (спека §10.1).
func TestUserSummaryNoPII(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[identity.UserSummary]()
	for i := range typ.NumField() {
		name := strings.ToLower(typ.Field(i).Name)
		for _, bad := range []string{"email", "mail", "phone", "birth", "password"} {
			if strings.Contains(name, bad) {
				t.Errorf("поле %s — ПД", typ.Field(i).Name)
			}
		}
	}
}
