package app_test

import (
	"context"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/id"
	"wf/backend/internal/staff/internal/app"
)

// cutoff — граница чистки на часах env: старше — удаляется.
func (e *env) cutoff() time.Time { return e.clock.Now().Add(-app.CleanupRetention) }

// session — строка staff_sessions с заданными сроком и гашением (revoked нулевое — живая).
func (e *env) session(staffID uuid.UUID, expires, revoked time.Time) uuid.UUID {
	e.t.Helper()
	sid := id.New()
	hash := make([]byte, 32)
	if _, err := rand.Read(hash); err != nil {
		e.t.Fatal(err)
	}
	var rev any
	var reason any
	if !revoked.IsZero() {
		rev, reason = revoked, "logout"
	}
	created := expires.Add(-8 * time.Hour)
	e.exec(`INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, mfa_at, expires_at,
		ip, user_agent, revoked_at, revoked_reason)
		VALUES ($1, $2, $3, 'active', $4, $4, $4, $5, '203.0.113.10', 'UA', $6, $7)`,
		sid, staffID, hash, created, expires, rev, reason)
	return sid
}

// invite — строка staff_invites: closed — "consumed" | "revoked" | "" (живое, с кодом), at — момент
// закрытия (у живого — срок), created — выпуск.
func (e *env) inviteAt(staffID uuid.UUID, purpose, closed string, at, created time.Time) uuid.UUID {
	e.t.Helper()
	iid := id.New()
	var codeEnc, keyID, consumed, revoked any
	expires := created.Add(24 * time.Hour)
	switch closed {
	case "consumed":
		consumed = at
	case "revoked":
		revoked = at
	default:
		ct, kid, err := e.ring.Seal([]byte("k7m2p-x4q6d"), iid[:])
		if err != nil {
			e.t.Fatal(err)
		}
		codeEnc, keyID, expires = ct, kid, at
	}
	e.exec(`INSERT INTO staff_invites (id, staff_id, purpose, code_enc, code_key_id, expires_at, sends, last_sent_at,
		created_at, consumed_at, revoked_at)
		VALUES ($1, $2, $3::staff_invite_purpose, $4, $5, $6, 1, $7, $7, $8, $9)`,
		iid, staffID, purpose, codeEnc, keyID, expires, created, consumed, revoked)
	return iid
}

func (e *env) exists(table string, rowID uuid.UUID) bool {
	e.t.Helper()
	return e.count(`SELECT count(*) FROM `+table+` WHERE id = $1`, rowID) == 1
}

// Сессии (§4.10): удаляется сессия, умершая (истёкшая или погашенная) больше 30 суток назад. Ровно 30 суток —
// остаётся, на микросекунду больше — удаляется; живая и недавно погасшая — остаются; давно истёкшая,
// которую погасили недавно (гашение expired при загрузке), — умерла по сроку, удаляется.
func TestCleanupSessions(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("a@wf.example", memberOpts{})
	e.clock.Advance(90 * 24 * time.Hour)
	now, cut := e.clock.Now(), e.cutoff()
	us := time.Microsecond
	keep := map[string]uuid.UUID{
		"живая": e.session(m.ID, now.Add(time.Hour), time.Time{}),
		"истекла ровно 30 суток":     e.session(m.ID, cut, time.Time{}),
		"погашена ровно 30 суток":    e.session(m.ID, now.Add(time.Hour), cut),
		"погашена вчера":             e.session(m.ID, now.Add(time.Hour), now.Add(-24*time.Hour)),
		"истекла 29 суток, погашена": e.session(m.ID, now.Add(-29*24*time.Hour), now.Add(-time.Hour)),
	}
	drop := map[string]uuid.UUID{
		"истекла 30 суток + 1 мкс":         e.session(m.ID, cut.Add(-us), time.Time{}),
		"погашена 30 суток + 1 мкс":        e.session(m.ID, now.Add(time.Hour), cut.Add(-us)),
		"истекла 40 суток, погашена вчера": e.session(m.ID, now.Add(-40*24*time.Hour), now.Add(-24*time.Hour)),
	}
	rep, err := e.app.Cleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != int64(len(drop)) || rep.Invites != 0 {
		t.Errorf("отчёт %+v, ждали сессий %d", rep, len(drop))
	}
	for name, sid := range keep {
		if !e.exists("staff_sessions", sid) {
			t.Errorf("%s: удалена", name)
		}
	}
	for name, sid := range drop {
		if e.exists("staff_sessions", sid) {
			t.Errorf("%s: осталась", name)
		}
	}
}

// Приглашения (§4.10, P27): погашенные и отозванные — по моменту закрытия, просроченные — по сроку, граница
// 30 суток (ровно — остаётся); последнее приглашение сотрудника остаётся всегда, даже давно просроченное:
// по нему повторная отправка берёт назначение (GetLastInvite).
func TestCleanupInvites(t *testing.T) {
	e := newEnv(t)
	e.clock.Advance(200 * 24 * time.Hour)
	now, cut := e.clock.Now(), e.cutoff()
	us := time.Microsecond
	inv := e.addMember("inv@wf.example", memberOpts{Status: "invited", NoMFA: true})
	act := e.addMember("act@wf.example", memberOpts{})
	off := e.addMember("off@wf.example", memberOpts{Status: "disabled"})
	day := 24 * time.Hour
	keep := map[string]uuid.UUID{
		"отозвано ровно 30 суток":     e.inviteAt(inv.ID, "invite", "revoked", cut, now.Add(-150*day)),
		"погашено ровно 30 суток":     e.inviteAt(act.ID, "invite", "consumed", cut, now.Add(-140*day)),
		"последнее, просрочено давно": e.inviteAt(inv.ID, "access_reset", "", now.Add(-100*day), now.Add(-101*day)),
		"последнее, погашено давно":   e.inviteAt(act.ID, "access_reset", "consumed", now.Add(-60*day), now.Add(-61*day)),
		"последнее, отозвано давно":   e.inviteAt(off.ID, "invite", "revoked", now.Add(-60*day), now.Add(-62*day)),
		"отозвано вчера":              e.inviteAt(off.ID, "invite", "revoked", now.Add(-day), now.Add(-63*day)),
	}
	drop := map[string]uuid.UUID{
		"отозвано 30 суток + 1 мкс": e.inviteAt(inv.ID, "invite", "revoked", cut.Add(-us), now.Add(-160*day)),
		"погашено 30 суток + 1 мкс": e.inviteAt(act.ID, "invite", "consumed", cut.Add(-us), now.Add(-145*day)),
		"отозвано давно":            e.inviteAt(off.ID, "access_reset", "revoked", now.Add(-90*day), now.Add(-95*day)),
	}
	rep, err := e.app.Cleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Invites != int64(len(drop)) || rep.Sessions != 0 {
		t.Errorf("отчёт %+v, ждали приглашений %d", rep, len(drop))
	}
	for name, iid := range keep {
		if !e.exists("staff_invites", iid) {
			t.Errorf("%s: удалено", name)
		}
	}
	for name, iid := range drop {
		if e.exists("staff_invites", iid) {
			t.Errorf("%s: осталось", name)
		}
	}
	// назначение повторной отправки — из оставшегося последнего (access_reset), а не invite по умолчанию
	if _, err := e.app.OperatorInviteResend(context.Background(), inv.Email); err != nil {
		t.Fatal(err)
	}
	if live := e.liveInvite(inv.ID); live.Purpose != "access_reset" {
		t.Fatalf("повторная отправка после чистки: назначение %q, ждали access_reset", live.Purpose)
	}
}

// Порции (как ExpireDue): строк больше порции — цикл идёт до неполной порции; ровно порция + 1 и кратное
// порции — всё удалено.
func TestCleanupBatches(t *testing.T) {
	e := newEnv(t)
	defer app.SetCleanupBatch(2)()
	m := e.addMember("a@wf.example", memberOpts{})
	e.clock.Advance(90 * 24 * time.Hour)
	old := e.cutoff().Add(-time.Hour)
	for range 5 {
		e.session(m.ID, old, time.Time{})
	}
	for range 4 {
		e.inviteAt(m.ID, "invite", "revoked", old, old.Add(-48*time.Hour))
	}
	e.inviteAt(m.ID, "invite", "consumed", old, old) // последнее — остаётся
	rep, err := e.app.Cleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 5 || rep.Invites != 4 {
		t.Fatalf("отчёт %+v, ждали 5 сессий и 4 приглашения", rep)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions`) + e.count(`SELECT count(*) FROM staff_invites`); n != 1 {
		t.Fatalf("осталось строк %d, ждали 1 (последнее приглашение)", n)
	}
}

// Два экземпляра admin-api чистят одновременно: порции SKIP LOCKED не пересекаются и не ждут друг друга —
// без ошибок, каждая строка удалена ровно одним проходом.
func TestCleanupConcurrent(t *testing.T) {
	e := newEnv(t)
	defer app.SetCleanupBatch(3)()
	m := e.addMember("a@wf.example", memberOpts{})
	e.clock.Advance(90 * 24 * time.Hour)
	old := e.cutoff().Add(-time.Hour)
	for range 40 {
		e.session(m.ID, old, time.Time{})
		e.inviteAt(m.ID, "invite", "revoked", old, old.Add(-48*time.Hour))
	}
	e.inviteAt(m.ID, "invite", "consumed", old, old)
	start := make(chan struct{})
	var wg sync.WaitGroup
	reps := make([]app.CleanupReport, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			reps[i], errs[i] = e.app.Cleanup(context.Background())
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("проход %d: %v", i, err)
		}
	}
	if s, inv := reps[0].Sessions+reps[1].Sessions, reps[0].Invites+reps[1].Invites; s != 40 || inv != 40 {
		t.Fatalf("удалено сессий %d, приглашений %d (отчёты %+v), ждали по 40", s, inv, reps)
	}
	if n := e.count(`SELECT count(*) FROM staff_sessions`) + e.count(`SELECT count(*) FROM staff_invites`); n != 1 {
		t.Fatalf("осталось строк %d", n)
	}
}

// Строку под замком (сценарий держит сессию или приглашение) порция пропускает, а не ждёт: чистка не
// встаёт в очередь за транзакциями сценариев и не держит их; пропущенное возьмёт следующий проход.
func TestCleanupSkipsLockedRows(t *testing.T) {
	e := newEnv(t)
	m := e.addMember("a@wf.example", memberOpts{})
	e.clock.Advance(90 * 24 * time.Hour)
	old := e.cutoff().Add(-time.Hour)
	lockedSession, freeSession := e.session(m.ID, old, time.Time{}), e.session(m.ID, old, time.Time{})
	lockedInvite := e.inviteAt(m.ID, "invite", "revoked", old, old.Add(-72*time.Hour))
	freeInvite := e.inviteAt(m.ID, "invite", "revoked", old, old.Add(-48*time.Hour))
	e.inviteAt(m.ID, "invite", "consumed", old, old)
	ctx := context.Background()
	tx, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM staff_sessions WHERE id = $1 FOR UPDATE`, lockedSession); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM staff_invites WHERE id = $1 FOR UPDATE`, lockedInvite); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rep, err := e.app.Cleanup(cctx)
	if err != nil {
		t.Fatalf("чистка ждала замок: %v", err)
	}
	if rep.Sessions != 1 || rep.Invites != 1 || e.exists("staff_sessions", freeSession) || e.exists("staff_invites", freeInvite) {
		t.Fatalf("отчёт %+v: свободные строки не удалены", rep)
	}
	if !e.exists("staff_sessions", lockedSession) || !e.exists("staff_invites", lockedInvite) {
		t.Fatal("строка под замком удалена")
	}
}

// Шаги чистки независимы (m3 ревью 10c): сбой чистки сессий не отменяет чистку приглашений — ошибка
// возвращается, но приглашения удалены (и наоборот).
func TestCleanupStepsIndependent(t *testing.T) {
	for _, broken := range []string{"staff_sessions", "staff_invites"} {
		t.Run(broken, func(t *testing.T) {
			e := newEnv(t)
			m := e.addMember("a@wf.example", memberOpts{})
			e.clock.Advance(90 * 24 * time.Hour)
			old := e.cutoff().Add(-time.Hour)
			sid := e.session(m.ID, old, time.Time{})
			iid := e.inviteAt(m.ID, "invite", "revoked", old, old.Add(-48*time.Hour))
			e.inviteAt(m.ID, "invite", "consumed", old, old)
			// у роли admin отнято удаление из одной таблицы — шаг этой таблицы падает
			e.exec(`REVOKE DELETE ON ` + broken + ` FROM admin`)
			rep, err := e.app.Cleanup(context.Background())
			if err == nil {
				t.Fatal("сбой шага не вернул ошибку")
			}
			if broken == "staff_sessions" && (rep.Invites != 1 || e.exists("staff_invites", iid)) {
				t.Fatalf("сбой сессий отменил чистку приглашений: %+v", rep)
			}
			if broken == "staff_invites" && (rep.Sessions != 1 || e.exists("staff_sessions", sid)) {
				t.Fatalf("сбой приглашений отменил чистку сессий: %+v", rep)
			}
		})
	}
}
