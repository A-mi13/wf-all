package app_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/app"
)

// accountID — id учётки по почте.
func (e *env) accountID(email string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM staff_accounts WHERE email = $1`, email).Scan(&id); err != nil {
		e.t.Fatalf("учётка %s: %v", email, err)
	}
	return id
}

// OperatorBootstrap (§4.2, U2): учётка owner в статусе invited, созданная оператором; приглашение
// (письмо staff.invite новой учётке); аудит staff.bootstrapped от оператора; событие staff.invited;
// письмо staff.bootstrapped каждому активному владельцу (выключенному и новой учётке — нет). Код —
// рабочий.
func TestOperatorBootstrap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.addMember("owner.a@wf.example", memberOpts{Role: "owner"})
	b := e.addMember("owner.b@wf.example", memberOpts{Role: "owner"})
	e.addMember("owner.off@wf.example", memberOpts{Role: "owner", Status: "disabled"})
	e.addMember("admin@wf.example", memberOpts{Role: "admin"})

	code, err := e.app.OperatorBootstrap(ctx, " New.Owner@WF.example ", "  Пётр Н. ")
	if err != nil {
		t.Fatal(err)
	}
	id := e.accountID("new.owner@wf.example")
	if n := e.count(`SELECT count(*) FROM staff_accounts a JOIN staff_roles r ON r.staff_id = a.id
		WHERE a.id = $1 AND a.status = 'invited' AND a.display_name = 'Пётр Н.' AND a.created_by IS NULL
		AND a.created_at = $2 AND a.password_hash IS NULL AND r.role = 'owner' AND r.granted_by IS NULL`, id, t0); n != 1 {
		t.Fatal("учётка или роль")
	}
	inv := e.liveInvite(id)
	if inv.Purpose != purposeInvite || e.mails(staff.MailInvite, inv.ID, id) != 1 {
		t.Fatalf("приглашение %+v", inv)
	}
	if e.mails(staff.MailBootstrapped, id, a.ID) != 1 || e.mails(staff.MailBootstrapped, id, b.ID) != 1 ||
		e.mails(staff.MailBootstrapped, uuid.Nil, uuid.Nil) != 2 {
		t.Fatal("письма staff.bootstrapped — двум активным владельцам")
	}
	rows := e.audits("staff.bootstrapped")
	if len(rows) != 1 || rows[0].Role != "operator" || rows[0].StaffID != nil || rows[0].Label == nil ||
		*rows[0].Label != "operator" || rows[0].ObjectID == nil || *rows[0].ObjectID != id ||
		!strings.Contains(rows[0].After, `"email": "n***@wf.example"`) {
		// нормализованная почта под маскируемым ключом: сырой ввод дал бы " ***@WF.example ",
		// немаскируемый ключ — почту открытым текстом
		t.Fatalf("аудит %+v", rows)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = 'staff.invited' AND aggregate_id = $1
		AND payload->>'role' = 'owner' AND aggregate_version = 1`, id); n != 1 {
		t.Fatal("событие staff.invited")
	}
	e.accept(member{ID: id, Email: "new.owner@wf.example"}, code)

	// почта занята в любом регистре и статусе
	for _, email := range []string{"NEW.OWNER@wf.example", "owner.off@wf.example"} {
		if _, err := e.app.OperatorBootstrap(ctx, email, "Кто-то"); !errors.Is(err, staff.ErrEmailTaken) {
			t.Fatalf("%s: %v", email, err)
		}
	}
	// проверки ввода — как у приглашения в контракте
	if _, err := e.app.OperatorBootstrap(ctx, "не почта", "Имя"); err == nil {
		t.Fatal("кривая почта")
	} else {
		wantFieldError(t, err, "body.email", "format")
	}
	for name, rule := range map[string]string{"": "minLength", "   ": "minLength", strings.Repeat("я", 81): "maxLength"} {
		_, err := e.app.OperatorBootstrap(ctx, "x@wf.example", name)
		wantFieldError(t, err, "body.display_name", rule)
	}
}

// На пустой базе bootstrap не ставит ни одного письма staff.bootstrapped (только приглашение).
func TestOperatorBootstrapEmpty(t *testing.T) {
	e := newEnv(t)
	if _, err := e.app.OperatorBootstrap(context.Background(), "first@wf.example", "Первый"); err != nil {
		t.Fatal(err)
	}
	if n := e.mails(staff.MailBootstrapped, uuid.Nil, uuid.Nil); n != 0 {
		t.Fatalf("писем staff.bootstrapped %d", n)
	}
	if n := e.mails(staff.MailInvite, uuid.Nil, uuid.Nil); n != 1 {
		t.Fatalf("приглашений %d", n)
	}
}

// OperatorInviteResend: новый код без паузы и проверки прав, назначение — последнего приглашения;
// не invited — wrong_stage, нет — not_found; аудит от оператора.
func TestOperatorInviteResend(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m, old := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeReset)
	c1, err := e.app.OperatorInviteResend(ctx, "IVAN.P@wf.example")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := e.app.OperatorInviteResend(ctx, m.Email) // сразу же — паузы нет
	if err != nil {
		t.Fatal(err)
	}
	all := e.invites(m.ID)
	if len(all) != 3 || all[0].Live || all[1].Live || !all[2].Live || all[2].Purpose != purposeReset {
		t.Fatalf("приглашения %+v", all)
	}
	if n := e.mails(staff.MailAccessReset, all[2].ID, m.ID); n != 1 {
		t.Fatalf("писем %d", n)
	}
	for _, c := range []string{old, c1} {
		if c == c2 {
			continue
		}
		if _, err := e.app.AcceptInvite(ctx, m.Email, c, newPassword, "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
			t.Fatalf("прежний код %s: %v", c, err)
		}
	}
	e.accept(m, c2)
	rows := e.audits("staff.invite_resent")
	if len(rows) != 2 || rows[0].Role != "operator" || rows[0].Label == nil || *rows[0].Label != "operator" {
		t.Fatalf("аудит %+v", rows)
	}
	active := e.addMember("active@wf.example", memberOpts{})
	if _, err := e.app.OperatorInviteResend(ctx, active.Email); !errors.Is(err, staff.ErrWrongStage) {
		t.Fatalf("active: %v", err)
	}
	for _, email := range []string{"nobody@wf.example", "не почта"} {
		if _, err := e.app.OperatorInviteResend(ctx, email); !errors.Is(err, staff.ErrNotFound) {
			t.Fatalf("%q: %v", email, err)
		}
	}
}

// createInvited (для 8b): модератор с городами, автор — сотрудник; почта занята — ErrEmailTaken.
func TestCreateInvited(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.addMember("owner@wf.example", memberOpts{Role: "owner"})
	kazan, perm := e.city("kazan"), e.city("perm")
	var id uuid.UUID
	err := db.InTx(ctx, e.owner, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = e.app.CreateInvited(ctx, tx, "Mod@wf.example", "Модератор", "moderator", []uuid.UUID{kazan, perm}, owner.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts a JOIN staff_roles r ON r.staff_id = a.id
		WHERE a.id = $1 AND a.email = 'mod@wf.example' AND a.created_by = $2 AND r.granted_by = $2 AND r.role = 'moderator'
		AND (SELECT count(*) FROM staff_role_cities WHERE staff_id = a.id) = 2`, id, owner.ID); n != 1 {
		t.Fatal("учётка модератора")
	}
	err = db.InTx(ctx, e.owner, func(ctx context.Context, tx pgx.Tx) error {
		_, err := e.app.CreateInvited(ctx, tx, "mod@wf.example", "Ещё", "support", nil, owner.ID)
		return err
	})
	if !errors.Is(err, staff.ErrEmailTaken) {
		t.Fatalf("занятая почта: %v", err)
	}
}

// newRing — кольцо из новых ключей и prev (первый — текущий).
func newRing(t *testing.T, prev ...string) (*keys.Ring, string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(key)
	r, err := keys.NewRing(append([]string{enc}, prev...))
	if err != nil {
		t.Fatal(err)
	}
	return r, enc
}

// Rekey (§4.11): после смены кольца (новый ключ первым) секреты TOTP и коды живых приглашений
// перешифрованы текущим ключом и открываются без старого; резервные коды на старом ключе
// посчитаны; повторный rekey ничего не трогает.
func TestRekey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// старое кольцо: сотрудники созданы с ним
	oldRing, oldKey := newRing(t)
	e.deps.Keys, e.ring = oldRing, oldRing
	e.app = app.New(e.deps)
	m := e.addMember("olga.v@wf.example", memberOpts{})
	e.addMember("anna.k@wf.example", memberOpts{Role: "owner"})
	inv, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	wiped, _ := e.invitee("wiped@wf.example", memberOpts{Role: "support"}, purposeInvite)
	for range 5 {
		_, _ = e.app.AcceptInvite(ctx, wiped.Email, "abc", newPassword, "", metaWeb)
	}
	oldID := oldRing.CurrentID()

	rotated, newKey := newRing(t, oldKey)
	ra := e.withRing(rotated)
	rep, err := ra.Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.MFA != 2 || rep.Invites != 1 || rep.RecoveryOnOldKeys != 20 || rep.MFAOnOldKeys != 0 || rep.InvitesOnOldKeys != 0 ||
		len(rep.UnreadableMFA) != 0 || len(rep.UnreadableInvites) != 0 {
		t.Fatalf("отчёт %+v", rep)
	}
	if n := e.count(`SELECT count(*) FROM staff_mfa WHERE key_id = $1`, rotated.CurrentID()); n != 2 {
		t.Fatalf("секретов на новом ключе %d", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_invites WHERE code_key_id = $1`, oldID); n != 0 {
		t.Fatalf("кодов на старом ключе %d", n)
	}
	rows := e.audits("staff.rekeyed")
	if len(rows) != 1 || rows[0].Role != "operator" || !strings.Contains(rows[0].After, `"mfa_count": 2`) {
		t.Fatalf("аудит %+v", rows)
	}

	// без старого ключа: TOTP и код приглашения открываются
	onlyNew, err := keys.NewRing([]string{newKey})
	if err != nil {
		t.Fatal(err)
	}
	na := e.withRing(onlyNew)
	e.app = na
	e.clock.Advance(30 * time.Second)
	if _, err := na.VerifyMFA(ctx, e.pending(m), e.totp(m), "", metaWeb); err != nil {
		t.Fatalf("TOTP после rekey: %v", err)
	}
	e.accept(inv, code)

	// повторный rekey (кольцо с новым ключом первым): всё уже на текущем ключе
	rep, err = ra.Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.MFA != 0 || rep.Invites != 0 || rep.MFAOnOldKeys != 0 || rep.InvitesOnOldKeys != 0 {
		t.Fatalf("повтор %+v", rep)
	}
}

// M-1 ревью: ключ убрали из кольца раньше времени — нечитаемые строки пропускаются и
// перечисляются (id), остальные перешифровываются, итог (и счёт строк не на текущем ключе) — в отчёте
// и аудите staff.rekeyed.
func TestRekeyUnreadable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	use := func(r *keys.Ring) {
		e.deps.Keys, e.ring = r, r
		e.app = app.New(e.deps)
	}
	lostRing, _ := newRing(t)
	use(lostRing) // этот ключ потом потеряют
	lost := e.addMember("lost@wf.example", memberOpts{})
	inv, _ := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	lostInvite := e.liveInvite(inv.ID).ID
	oldRing, oldKey := newRing(t)
	use(oldRing)
	m := e.addMember("olga.v@wf.example", memberOpts{})

	rotated, _ := newRing(t, oldKey) // потерянного ключа в кольце нет
	rep, err := e.withRing(rotated).Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.MFA != 1 || rep.Invites != 0 || rep.MFAOnOldKeys != 1 || rep.InvitesOnOldKeys != 1 || rep.RecoveryOnOldKeys != 20 ||
		len(rep.UnreadableMFA) != 1 || rep.UnreadableMFA[0] != lost.ID ||
		len(rep.UnreadableInvites) != 1 || rep.UnreadableInvites[0] != lostInvite {
		t.Fatalf("отчёт %+v", rep)
	}
	if n := e.count(`SELECT count(*) FROM staff_mfa WHERE staff_id = $1 AND key_id = $2`, m.ID, rotated.CurrentID()); n != 1 {
		t.Fatal("читаемый секрет не перешифрован")
	}
	rows := e.audits("staff.rekeyed")
	if len(rows) != 1 || !strings.Contains(rows[0].After, `"unreadable_count": 2`) ||
		!strings.Contains(rows[0].After, `"mfa_on_old_keys_count": 1`) {
		t.Fatalf("аудит %+v", rows)
	}
}
