package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/db"
)

// grantPermit — одобренная заявка игрока в городе и живой допуск по ней (как одобрение Task 4); id заявки.
func grantPermit(t *testing.T, e *env, uid, city uuid.UUID) uuid.UUID {
	t.Helper()
	var app uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `INSERT INTO captain_applications (user_id, city_id, status,
		reviewed_at, reviewed_by_label, closed_at, version) VALUES ($1, $2, 'approved', $3, 'Модератор', $3, 2) RETURNING id`,
		uid, city, testNow.Add(-time.Hour)).Scan(&app); err != nil {
		t.Fatal(err)
	}
	acctExec(t, e, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id, application_id, granted_at)
		VALUES ($1, 'captain', 'city', $2, $3, $4)`, uid, city, app, testNow.Add(-time.Hour))
	return app
}

func revokeRole(e *env, uid, city uuid.UUID, by RevokeBy, reason string) error {
	return db.InTx(context.Background(), e.a.pool, func(ctx context.Context, tx pgx.Tx) error {
		return e.a.RevokeRoleInTx(ctx, tx, uid, city, by, reason)
	})
}

// permitEvent — payload и версия события captain_permit_revoked заявки app (ошибка — события нет).
func permitEvent(t *testing.T, e *env, app uuid.UUID) (identity.CaptainPermitPayload, int64) {
	t.Helper()
	var raw []byte
	var ver int64
	if err := e.owner.QueryRow(context.Background(), `SELECT payload, aggregate_version FROM outbox WHERE event_type = $1
		AND aggregate_type = 'captain_application' AND aggregate_id = $2`, identity.EventCaptainPermitRevoked, app).
		Scan(&raw, &ver); err != nil {
		t.Fatalf("события captain_permit_revoked заявки %s нет: %v", app, err)
	}
	var p identity.CaptainPermitPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p, ver
}

// §10.2: отзыв живого допуска — revoked_at, автор и причина, версия заявки + 1 и событие на агрегате заявки,
// представление допуск не видит; повтор → ErrPermitNotFound; допуск без заявки — ошибка инварианта (R25) без записей.
func TestRevokeRoleInTx(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cities := seedCaptainCities(t, e)
	uid := seedUser(t, e.owner, "revoke_me", "ru")
	app := grantPermit(t, e, uid, cities.main)
	other := grantPermit(t, e, uid, cities.other)
	staff := uuid.New()
	if err := revokeRole(e, uid, cities.main, RevokeBy{StaffID: staff, Label: "Модератор"}, "жалобы игроков"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE application_id = $1 AND revoked_at = $2
		AND revoked_by_staff_id = $3 AND revoke_reason = 'жалобы игроков'`, app, testNow, staff); n != 1 {
		t.Fatal("строка допуска не отозвана")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM identity_read_captain_permits WHERE user_id = $1`, uid); n != 1 {
		t.Fatalf("живых допусков в представлении %d, ждали 1 (второй город)", n)
	}
	p, ver := permitEvent(t, e, app)
	if p.UserID != uid || p.CityID != cities.main || p.ApplicationID != app || ver != 3 ||
		count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND version = 3`, app) != 1 {
		t.Fatalf("событие %+v v%d", p, ver)
	}
	if err := revokeRole(e, uid, cities.main, RevokeBy{System: true}, ""); !errors.Is(err, ErrPermitNotFound) {
		t.Fatalf("повтор: %v", err)
	}
	if err := revokeRole(e, uid, cities.other, RevokeBy{}, ""); err == nil {
		t.Fatal("отзыв без автора")
	}
	if err := revokeRole(e, uid, cities.other, RevokeBy{System: true}, strings.Repeat("я", 501)); err == nil {
		t.Fatal("причина длиннее 500")
	}
	if err := revokeRole(e, uid, cities.other, RevokeBy{System: true}, strings.Repeat("я", 500)); err != nil {
		t.Fatalf("причина ровно 500: %v", err)
	}
	if _, v := permitEvent(t, e, other); v != 3 {
		t.Fatalf("версия второй заявки %d", v)
	}

	// m2 итогового ревью В: пустая причина (и из одних пробелов) — NULL «без причины», не ''
	for i, reason := range []string{"", "  "} {
		u := seedUser(t, e.owner, fmt.Sprintf("revoke_noreason_%d", i), "ru")
		a := grantPermit(t, e, u, cities.main)
		if err := revokeRole(e, u, cities.main, RevokeBy{System: true}, reason); err != nil {
			t.Fatal(err)
		}
		if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE application_id = $1
			AND revoked_at IS NOT NULL AND revoke_reason IS NULL`, a); n != 1 {
			t.Fatalf("причина %q не NULL", reason)
		}
	}

	// R25: допуск без заявки — ошибка инварианта, ничего не записано
	orphan := seedUser(t, e.owner, "orphan_permit", "ru")
	acctExec(t, e, `INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, 'captain', 'city', $2)`,
		orphan, cities.main)
	if err := revokeRole(e, orphan, cities.main, RevokeBy{System: true}, ""); err == nil || errors.Is(err, ErrPermitNotFound) {
		t.Fatalf("допуск без заявки: %v", err)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoked_at IS NULL`, orphan); n != 1 {
		t.Fatal("допуск без заявки отозван")
	}
}

// Две параллельные транзакции отзывают один допуск: ровно одна успешна, вторая — ErrPermitNotFound, одно событие, без
// 40P01 (замок строки заявки, затем строка допуска с перепроверкой).
func TestRevokeRoleConcurrent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	city := delCity(t, e)
	uid := seedUser(t, e.owner, "revoke_race", "ru")
	app := grantPermit(t, e, uid, city)
	ctx := context.Background()
	holder, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM captain_applications WHERE id = $1 FOR UPDATE`, app); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- revokeRole(e, uid, city, RevokeBy{System: true}, "") }()
	}
	if !lockWaiters(t, e.owner, 2) {
		_ = holder.Rollback(ctx)
		t.Fatal("отзывы не встали на замок заявки")
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var ok, notFound int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrPermitNotFound):
			notFound++
		default:
			t.Fatalf("отзыв: %v", err)
		}
	}
	if ok != 1 || notFound != 1 || count(t, e.owner, `SELECT count(*) FROM outbox WHERE event_type = $1`,
		identity.EventCaptainPermitRevoked) != 1 {
		t.Fatalf("успех %d, нет допуска %d", ok, notFound)
	}
}

// §10.1: Service.CaptainPermits — живые допуски по городам (отозванный не виден).
func TestCaptainPermitsService(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cities := seedCaptainCities(t, e)
	uid := seedUser(t, e.owner, "permits_svc", "ru")
	keep := grantPermit(t, e, uid, cities.main)
	grantPermit(t, e, uid, cities.other)
	if err := revokeRole(e, uid, cities.other, RevokeBy{System: true}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := identity.New(e.a.pool).CaptainPermits(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CityID != cities.main || got[0].ApplicationID != keep || !got[0].Probationary ||
		!got[0].GrantedAt.Equal(testNow.Add(-time.Hour)) {
		t.Fatalf("допуски %+v", got)
	}
	if got, err := identity.New(e.a.pool).CaptainPermits(context.Background(), uuid.New()); err != nil || len(got) != 0 {
		t.Fatalf("чужой игрок: %+v %v", got, err)
	}
}

// R24: анонимизация публикует captain_permit_revoked на каждый живой допуск (агрегат — заявка допуска), payload без ПД.
func TestAnonymizePublishesPermitRevoked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "anon_permit")
	var city uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT city_id FROM users WHERE id = $1`, uid).Scan(&city); err != nil {
		t.Fatal(err)
	}
	// delSeed заводит допуск без заявки (до R25) — заменяем его допуском по одобренной заявке
	acctExec(t, e, `DELETE FROM role_assignments WHERE user_id = $1`, uid)
	app := grantPermit(t, e, uid, city)
	if _, err := e.a.DeleteAccount(context.Background(), sid, "anon_permit"); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(31 * 24 * time.Hour)
	if n, err := anonymizeDone(delWorker(t, e), 10); err != nil || n != 1 {
		t.Fatalf("анонимизировано %d: %v", n, err)
	}
	p, _ := permitEvent(t, e, app)
	if p.UserID != uid || p.CityID != city || p.ApplicationID != app {
		t.Fatalf("событие %+v", p)
	}
	if n := count(t, e.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 0 {
		t.Fatal("допуск жив после анонимизации")
	}
	if n := count(t, e.owner, `SELECT count(*) FROM outbox WHERE payload::text LIKE '%anon_permit%'
		OR payload::text LIKE '%7999%'`); n != 0 {
		t.Fatal("ПД в событиях")
	}
}

// R24: запрос удаления публикует captain_application_withdrawn на открытую заявку (версия + 1).
func TestDeleteAccountPublishesWithdrawn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, sid := delSeed(t, e, "del_withdrawn")
	var app uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM captain_applications WHERE user_id = $1 AND status = 'pending'`,
		uid).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.DeleteAccount(context.Background(), sid, "del_withdrawn"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var ver int64
	if err := e.owner.QueryRow(context.Background(), `SELECT payload, aggregate_version FROM outbox WHERE event_type = $1
		AND aggregate_type = 'captain_application' AND aggregate_id = $2`, identity.EventCaptainApplicationWithdrawn, app).
		Scan(&raw, &ver); err != nil {
		t.Fatalf("события отзыва нет: %v", err)
	}
	var p identity.CaptainApplicationPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.ApplicationID != app || p.UserID != uid || ver != 2 ||
		count(t, e.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND status = 'withdrawn' AND version = 2`, app) != 1 {
		t.Fatalf("событие %+v v%d", p, ver)
	}
	if strings.Contains(string(raw), "79990001122") || strings.Contains(string(raw), "t.me") {
		t.Fatalf("ПД в событии: %s", raw)
	}
}
