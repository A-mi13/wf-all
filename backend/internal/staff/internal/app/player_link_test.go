package app_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
)

// Связь «сотрудник → учётка игрока» (спека identity §7.6; план I-4, Task 6, R14, R15) под ролью admin: только
// владелец, автор перепроверяется под замком, версия учётки растёт, аудит и письмо — в транзакции действия.

// player — учётка игрока с почтой <nick>@example.com.
func (e *env) player(nick string) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO users (email, email_verified_at, nickname, birth_date,
		locale) VALUES ($1, now(), $2, '2000-01-01', 'ru') RETURNING id`, nick+"@example.com", nick).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// linkOf — связь сотрудника (uuid.Nil — нет).
func (e *env) linkOf(staffID uuid.UUID) uuid.UUID {
	e.t.Helper()
	var u uuid.UUID
	_ = e.owner.QueryRow(context.Background(), `SELECT user_id FROM staff_player_links WHERE staff_id = $1`, staffID).Scan(&u)
	return u
}

// linkAudits — записи аудита связи по сотруднику.
func (e *env) linkAudits(action string, staffID uuid.UUID) int {
	return e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2`, action, staffID)
}

func TestPutPlayerLink(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	om := e.addMember("owner@wf.example", memberOpts{Role: "owner", Name: "Ольга В."})
	owner := e.as(om)
	sm := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	p := e.player("link_one")
	v0 := e.acct(sm.ID).Version

	got, err := e.app.PutPlayerLink(ctx, owner, sm.ID, p, metaWeb)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != p || got.Nickname != "link_one" || !got.LinkedAt.Equal(t0) || got.LinkedByLabel != "Ольга В." {
		t.Fatalf("ответ: %+v", got)
	}
	if e.linkOf(sm.ID) != p || e.acct(sm.ID).Version != v0+1 || e.linkAudits("staff.player_linked", sm.ID) != 1 ||
		e.mails(staff.MailPlayerLinked, uuid.Nil, sm.ID) != 1 {
		t.Fatalf("после связи: link %v, версия %d, аудит %d, писем %d", e.linkOf(sm.ID), e.acct(sm.ID).Version,
			e.linkAudits("staff.player_linked", sm.ID), e.mails(staff.MailPlayerLinked, uuid.Nil, sm.ID))
	}
	// ref письма — запись аудита; в аргументах задачи нет почты
	var ref uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT id FROM audit_log WHERE action = 'staff.player_linked' AND object_id = $1`,
		sm.ID).Scan(&ref); err != nil || e.mails(staff.MailPlayerLinked, ref, sm.ID) != 1 {
		t.Fatalf("ref письма: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM river_job WHERE args::text LIKE '%@%'`); n != 0 {
		t.Fatal("почта в аргументах задач")
	}
	// после связи загрузка сессии сотрудника видит связь (LoadStaff → LinkedUserID)
	if s := e.as(sm); s.LinkedUserID != p {
		t.Fatalf("LinkedUserID: %v", s.LinkedUserID)
	}

	// повтор той же связи — без версии, аудита и письма
	if again, err := e.app.PutPlayerLink(ctx, owner, sm.ID, p, metaWeb); err != nil || again != got {
		t.Fatalf("повтор: %+v %v", again, err)
	}
	if e.acct(sm.ID).Version != v0+1 || e.linkAudits("staff.player_linked", sm.ID) != 1 ||
		e.mails(staff.MailPlayerLinked, uuid.Nil, sm.ID) != 1 {
		t.Fatal("повтор той же связи что-то записал")
	}

	// замена — другая учётка: версия + 1, before — прежняя
	p2 := e.player("link_two")
	if _, err := e.app.PutPlayerLink(ctx, owner, sm.ID, p2, metaWeb); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := e.owner.QueryRow(ctx, `SELECT before::text, after::text FROM audit_log WHERE action = 'staff.player_linked'
		AND object_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, sm.ID).Scan(&before, &after); err != nil {
		t.Fatal(err)
	}
	if e.linkOf(sm.ID) != p2 || e.acct(sm.ID).Version != v0+2 || !strings.Contains(before, p.String()) ||
		!strings.Contains(after, p2.String()) {
		t.Fatalf("замена: %s → %s", before, after)
	}

	// письмо собирается: имя сотрудника, подпись владельца, ник
	m, err := e.app.ComposePlayerLink(ctx, ref, sm.ID, staff.MailPlayerLinked)
	if err != nil || m.To != "sup@wf.example" || !strings.Contains(m.Text, "Ольга В.") || !strings.Contains(m.Text, "link_one") {
		t.Fatalf("письмо: %+v %v", m, err)
	}
	if _, err := e.app.ComposePlayerLink(ctx, ref, om.ID, staff.MailPlayerLinked); err == nil {
		t.Fatal("письмо о связи другому сотруднику собрано")
	}
}

func TestPutPlayerLinkCodes(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	admin := e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"}))
	a := e.addMember("a@wf.example", memberOpts{Role: "support"})
	b := e.addMember("b@wf.example", memberOpts{Role: "support"})
	p := e.player("codes_p")
	gone := e.player("codes_gone")
	e.exec(`UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`, gone)

	if _, err := e.app.PutPlayerLink(ctx, owner, uuid.New(), p, metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет сотрудника: %v", err)
	}
	if _, err := e.app.PutPlayerLink(ctx, owner, uuid.New(), uuid.New(), metaWeb); !errors.Is(err, staff.ErrNotFound) {
		t.Fatalf("нет ни сотрудника, ни игрока — сначала сотрудник: %v", err)
	}
	for name, uid := range map[string]uuid.UUID{"удалён": gone, "нет": uuid.New()} {
		if _, err := e.app.PutPlayerLink(ctx, owner, a.ID, uid, metaWeb); !errors.Is(err, staff.ErrPlayerNotFound) {
			t.Fatalf("игрок %s: %v", name, err)
		}
	}
	if _, err := e.app.PutPlayerLink(ctx, owner, a.ID, p, metaWeb); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.PutPlayerLink(ctx, owner, b.ID, p, metaWeb); !errors.Is(err, staff.ErrPlayerLinkTaken) {
		t.Fatalf("учётка у другого: %v", err)
	}
	_, err := e.app.PutPlayerLink(ctx, admin, b.ID, e.player("codes_q"), metaWeb)
	wantForbidden(t, err)
	wantForbidden(t, e.app.DeletePlayerLink(ctx, admin, a.ID, metaWeb))
	if !errors.Is(e.app.DeletePlayerLink(ctx, owner, uuid.New(), metaWeb), staff.ErrNotFound) {
		t.Fatal("DELETE: нет сотрудника")
	}
	if e.linkOf(b.ID) != uuid.Nil || e.linkOf(a.ID) != p {
		t.Fatal("отказ что-то записал")
	}
}

// Две связи одной учётки игрока параллельно: ровно одна, другая — staff.player_link_taken, без 500. Детерминированно —
// через 23505: связь чужой транзакции не закоммичена (проверка «учётка у другого» её не видит), вставка ждёт индекс
// UNIQUE (user_id), после коммита — 23505 → player_link_taken.
func TestPutPlayerLinkRace(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner"}))
	a := e.addMember("a@wf.example", memberOpts{Role: "support"})
	b := e.addMember("b@wf.example", memberOpts{Role: "support"})

	t.Run("параллельно", func(t *testing.T) {
		p := e.player("race_p")
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, target := range []uuid.UUID{a.ID, b.ID} {
			wg.Go(func() {
				<-start
				_, errs[i] = e.app.PutPlayerLink(ctx, owner, target, p, metaWeb)
			})
		}
		close(start)
		wg.Wait()
		ok, taken := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, staff.ErrPlayerLinkTaken):
				taken++
			default:
				t.Fatalf("неожиданная ошибка: %v", err)
			}
		}
		if ok != 1 || taken != 1 || e.count(`SELECT count(*) FROM staff_player_links WHERE user_id = $1`, p) != 1 {
			t.Fatalf("исходы: %d успех, %d занято", ok, taken)
		}
	})

	t.Run("23505", func(t *testing.T) {
		p := e.player("race_q")
		c := e.addMember("c@wf.example", memberOpts{Role: "support"})
		tx, err := e.owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at)
			VALUES ($1, $2, $1, now())`, c.ID, p); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := e.app.PutPlayerLink(ctx, owner, a.ID, p, metaWeb)
			done <- err
		}()
		e.waitLockWaiters(1)
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, staff.ErrPlayerLinkTaken) {
			t.Fatalf("гонка на индексе: %v", err)
		}
	})
}

// Автор перепроверяется под замком (M-4, по образцу TestManageActorRechecked): роль понижена — 403, учётка выключена
// или сессия погашена — 401, заменена — ErrSessionReplaced; связь и версия цели не меняются. PUT и DELETE.
func TestPutPlayerLinkActorRechecked(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	e.addMember("owner.keep@wf.example", memberOpts{Role: "owner"})
	target := e.addMember("t@wf.example", memberOpts{Role: "support"})
	linked := e.player("recheck_linked")
	e.exec(`INSERT INTO staff_player_links (staff_id, user_id, created_by, created_at) VALUES ($1, $2, $1, now())`,
		target.ID, linked)
	p := e.player("recheck_p")
	v0 := e.acct(target.ID).Version

	cases := []struct {
		name  string
		spoil func(m member, s *auth.Staff)
		want  func(error) bool
	}{
		{"роль понижена", func(m member, _ *auth.Staff) {
			e.exec(`UPDATE staff_roles SET role = 'admin' WHERE staff_id = $1`, m.ID)
		}, func(err error) bool {
			pe, ok := errors.AsType[httpx.ProblemError](err)
			return ok && pe.Problem().Status == http.StatusForbidden
		}},
		{"учётка выключена", func(m member, _ *auth.Staff) {
			e.exec(`UPDATE staff_accounts SET status = 'disabled' WHERE id = $1`, m.ID)
		}, func(err error) bool { return errors.Is(err, auth.ErrSessionInvalid) }},
		{"сессия погашена", func(_ member, s *auth.Staff) {
			e.exec(`UPDATE staff_sessions SET revoked_at = now(), revoked_reason = 'revoked' WHERE id = $1`, s.SessionID)
		}, func(err error) bool { return errors.Is(err, auth.ErrSessionInvalid) }},
		{"сессия заменена", func(_ member, s *auth.Staff) {
			e.exec(`UPDATE staff_sessions SET revoked_at = now(), revoked_reason = 'replaced' WHERE id = $1`, s.SessionID)
		}, func(err error) bool { return errors.Is(err, auth.ErrSessionReplaced) }},
	}
	for i, c := range cases {
		for _, op := range []string{"PUT", "DELETE"} {
			m := e.addMember("owner"+string(rune('a'+i))+strings.ToLower(op)+"@wf.example", memberOpts{Role: "owner"})
			s := e.as(m)
			c.spoil(m, s)
			var err error
			if op == "PUT" {
				_, err = e.app.PutPlayerLink(ctx, s, target.ID, p, metaWeb)
			} else {
				err = e.app.DeletePlayerLink(ctx, s, target.ID, metaWeb)
			}
			if !c.want(err) {
				t.Errorf("%s %s: %v", c.name, op, err)
			}
		}
	}
	if e.linkOf(target.ID) != linked || e.acct(target.ID).Version != v0 {
		t.Fatal("действие перепроверенного автора что-то изменило")
	}
}

func TestDeletePlayerLink(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner", Name: "Ольга В."}))
	sm := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	p := e.player("del_p")
	if _, err := e.app.PutPlayerLink(ctx, owner, sm.ID, p, metaWeb); err != nil {
		t.Fatal(err)
	}
	v := e.acct(sm.ID).Version
	if err := e.app.DeletePlayerLink(ctx, owner, sm.ID, metaWeb); err != nil {
		t.Fatal(err)
	}
	if e.linkOf(sm.ID) != uuid.Nil || e.acct(sm.ID).Version != v+1 || e.linkAudits("staff.player_unlinked", sm.ID) != 1 ||
		e.mails(staff.MailPlayerUnlinked, uuid.Nil, sm.ID) != 1 {
		t.Fatal("снятие связи: нет записи, версии или письма")
	}
	var ref uuid.UUID
	if err := e.owner.QueryRow(ctx, `SELECT id FROM audit_log WHERE action = 'staff.player_unlinked' AND object_id = $1`,
		sm.ID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if m, err := e.app.ComposePlayerLink(ctx, ref, sm.ID, staff.MailPlayerUnlinked); err != nil ||
		!strings.Contains(m.Text, "del_p") || !strings.Contains(m.Text, "Ольга В.") {
		t.Fatalf("письмо: %+v %v", m, err)
	}
	// игрок удалён (анонимизирован) — письмо о снятии всё равно уходит (спека §7.6), ник — заглушкой локали (m3 ревью)
	e.exec(`UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`, p)
	if m, err := e.app.ComposePlayerLink(ctx, ref, sm.ID, staff.MailPlayerUnlinked); err != nil ||
		!strings.Contains(m.Text, "(аккаунт удалён)") || strings.Contains(m.Text, "del_p") || m.To != "sup@wf.example" {
		t.Fatalf("письмо об удалённом игроке: %+v %v", m, err)
	}
	// повтор — 204 без записи и письма
	if err := e.app.DeletePlayerLink(ctx, owner, sm.ID, metaWeb); err != nil {
		t.Fatal(err)
	}
	if e.acct(sm.ID).Version != v+1 || e.linkAudits("staff.player_unlinked", sm.ID) != 1 ||
		e.mails(staff.MailPlayerUnlinked, uuid.Nil, sm.ID) != 1 {
		t.Fatal("повтор снятия что-то записал")
	}
}

// Карточка: player_link — только владельцу (staff.player_links.manage = allow), с ником через порт; админ (есть
// staff.accounts.read) — поля нет; связи нет — поля нет.
func TestStaffCardPlayerLinkOwnerOnly(t *testing.T) {
	t.Parallel()
	e := newManageEnv(t)
	ctx := context.Background()
	owner := e.as(e.addMember("owner@wf.example", memberOpts{Role: "owner", Name: "Ольга В."}))
	admin := e.as(e.addMember("admin@wf.example", memberOpts{Role: "admin"}))
	sm := e.addMember("sup@wf.example", memberOpts{Role: "support"})
	free := e.addMember("free@wf.example", memberOpts{Role: "support"})
	p := e.player("card_p")
	if _, err := e.app.PutPlayerLink(ctx, owner, sm.ID, p, metaWeb); err != nil {
		t.Fatal(err)
	}
	v, err := e.app.Account(ctx, owner, sm.ID)
	if err != nil || v.PlayerLink == nil || v.PlayerLink.UserID != p || v.PlayerLink.Nickname != "card_p" ||
		v.PlayerLink.LinkedByLabel != "Ольга В." {
		t.Fatalf("владелец: %+v %v", v.PlayerLink, err)
	}
	// ник — на каждое чтение
	e.exec(`UPDATE users SET nickname = 'card_renamed' WHERE id = $1`, p)
	if v, err := e.app.Account(ctx, owner, sm.ID); err != nil || v.PlayerLink == nil || v.PlayerLink.Nickname != "card_renamed" {
		t.Fatalf("новый ник: %+v %v", v.PlayerLink, err)
	}
	if v, err := e.app.Account(ctx, admin, sm.ID); err != nil || v.PlayerLink != nil {
		t.Fatalf("админ видит связь: %+v %v", v.PlayerLink, err)
	}
	if v, err := e.app.Account(ctx, owner, free.ID); err != nil || v.PlayerLink != nil {
		t.Fatalf("без связи: %+v %v", v.PlayerLink, err)
	}
	if views, _, err := e.app.Accounts(ctx, owner, staff.AccountFilter{}); err != nil {
		t.Fatal(err)
	} else {
		for _, v := range views {
			if v.PlayerLink != nil {
				t.Fatalf("список несёт связь: %s", v.ID)
			}
		}
	}
}
