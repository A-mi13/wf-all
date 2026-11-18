package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/identitytest"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/queue"
)

// Заявки в капитаны в админке (спека §3.12 п. 6, §7.1, §7.6, §8.2; план I-4, Task 4). Сценарии — под ролью admin
// (роль прода admin-api: users и sessions не пишет, любой FOR … на users дал бы 42501) через NewAdmin с портами
// условий и портом связи сотрудника (fakeLinks: учёток staff в базе теста нет; настоящий PutPlayerLink —
// httpapi/admin TestPutPlayerLinkVsApprove). Игрок и его заявки — env под ролью api.

// capEnv — env игрока, города капитанства и сценарии admin-api.
type capEnv struct {
	*env
	adm   *App
	links *fakeLinks
	c     captainCities
}

// adminPool — пул той же базы под ролью admin (SET ROLE).
func adminPool(t *testing.T, e *env) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(e.owner.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 6
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE admin")
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// captainAdmin — сценарии admin-api (NewAdmin) на пуле роли admin с портами checks и связью links.
func captainAdmin(t *testing.T, e *env, checks []identity.CaptainCheck, links identity.StaffLinks) *App {
	t.Helper()
	pool := adminPool(t, e)
	log := slog.New(slog.DiscardHandler)
	ins, err := river.NewClient(riverpgxv5.New(pool), queue.InsertConfig(log))
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewAdmin(identity.AdminDeps{Pool: pool, Clock: e.clock, River: ins, Geo: geo.New(pool),
		CaptainChecks: checks, StaffLinks: links, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newCapEnv(t *testing.T) *capEnv {
	t.Helper()
	e := newEnv(t)
	links := &fakeLinks{}
	return &capEnv{env: e, adm: captainAdmin(t, e, identitytest.AllowAll(), links), links: links,
		c: seedCaptainCities(t, e)}
}

// player — игрок в городе city (ник — почта <nick>@example.com).
func (c *capEnv) player(t *testing.T, nick string, city uuid.UUID) uuid.UUID {
	t.Helper()
	uid := seedUser(t, c.owner, nick, "ru")
	setCity(t, c.env, uid, city)
	return uid
}

// pending — открытая заявка игрока в городе, поданная в created; телефон и форма — для контактов и ответа.
func (c *capEnv) pending(t *testing.T, uid, city uuid.UUID, created time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := c.owner.QueryRow(context.Background(), `INSERT INTO captain_applications (id, user_id, city_id,
		proposed_format, note, phone, status, created_at, updated_at) VALUES (gen_random_uuid(), $1, $2, '6×6 по средам',
		'Собираю ребят', '+79990001122', 'pending', $3, $3) RETURNING id`, uid, city, created).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// decided — решённая (approved | rejected) или отозванная заявка, время решения — at.
func (c *capEnv) decided(t *testing.T, uid, city uuid.UUID, status string, created, at time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	q := `INSERT INTO captain_applications (id, user_id, city_id, status, reviewed_at, reviewed_by_label, rejection_code,
		closed_at, created_at, updated_at) VALUES (gen_random_uuid(), $1, $2, $3::application_status,
		CASE WHEN $3 IN ('approved', 'rejected') THEN $4::timestamptz END,
		CASE WHEN $3 IN ('approved', 'rejected') THEN 'Модератор' END,
		CASE WHEN $3 = 'rejected' THEN 'insufficient_info' END,
		CASE WHEN $3 = 'withdrawn' THEN $4::timestamptz END, $5, $5) RETURNING id`
	if err := c.owner.QueryRow(context.Background(), q, uid, city, status, at, created).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func moderatorOf(cities ...uuid.UUID) *auth.Staff {
	st := staffMember(perm.Moderator, "Модератор", uuid.Nil)
	st.CityIDs = append(make([]uuid.UUID, 0, len(cities)), cities...)
	return st
}

func wantAppNotFound(t *testing.T, what string, err error) {
	t.Helper()
	wantProblem(t, what, err, http.StatusNotFound, identity.CodeApplicationNotFound)
}

func appIDs(list []AdminApplication) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(list))
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func appStatus(t *testing.T, c *capEnv, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := c.owner.QueryRow(context.Background(), `SELECT status::text FROM captain_applications WHERE id = $1`, id).
		Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

const contactReason = "Обращение SUP-4812: проверка контакта"

// Скоуп (R9, R37): модератор города A видит и решает только заявки A; фильтр чужого города — пусто; карточка,
// контакты, одобрение и отказ заявки B — 404, как несуществующей; владелец видит обе; модератор без городов (пустой
// не-nil срез) не видит ничего; поддержка решать не может (403).
func TestCaptainQueueScope(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	ua := c.player(t, "scope_a", c.c.main)
	ub := c.player(t, "scope_b", c.c.other)
	a := c.pending(t, ua, c.c.main, testNow.Add(-2*time.Hour))
	b := c.pending(t, ub, c.c.other, testNow.Add(-time.Hour))

	mod := moderatorOf(c.c.main)
	list, _, err := c.adm.ListCaptainApplications(ctx, mod, CaptainQueueFilter{Limit: 20})
	if err != nil || !slices.Equal(appIDs(list), []uuid.UUID{a}) {
		t.Fatalf("модератор A: %v %v", appIDs(list), err)
	}
	list, _, err = c.adm.ListCaptainApplications(ctx, mod, CaptainQueueFilter{CityID: c.c.other, Limit: 20})
	if err != nil || len(list) != 0 {
		t.Fatalf("фильтр чужого города: %v %v", appIDs(list), err)
	}
	if _, err := c.adm.CaptainApplicationCard(ctx, mod, a); err != nil {
		t.Fatalf("карточка своего города: %v", err)
	}
	_, err = c.adm.CaptainApplicationCard(ctx, mod, b)
	wantAppNotFound(t, "карточка чужого", err)
	_, _, err = c.adm.CaptainApplicantContact(ctx, mod, b, contactReason)
	wantAppNotFound(t, "контакты чужого", err)
	_, err = c.adm.ApproveCaptainApplication(ctx, mod, b, nil)
	wantAppNotFound(t, "одобрение чужого", err)
	_, err = c.adm.RejectCaptainApplication(ctx, mod, b, "insufficient_info", nil)
	wantAppNotFound(t, "отказ чужого", err)
	if s := appStatus(t, c, b); s != "pending" {
		t.Fatalf("чужая заявка: %s", s)
	}

	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	list, _, err = c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{Limit: 20})
	if err != nil || !slices.Equal(appIDs(list), []uuid.UUID{a, b}) {
		t.Fatalf("владелец: %v %v", appIDs(list), err)
	}
	list, _, err = c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{CityID: c.c.other, Limit: 20})
	if err != nil || !slices.Equal(appIDs(list), []uuid.UUID{b}) {
		t.Fatalf("владелец, фильтр города: %v %v", appIDs(list), err)
	}

	none := moderatorOf() // модератор без городов: CityIDs — пустой не-nil срез (I4 ревью)
	list, _, err = c.adm.ListCaptainApplications(ctx, none, CaptainQueueFilter{Limit: 20})
	if err != nil || len(list) != 0 {
		t.Fatalf("модератор без городов: %v %v", appIDs(list), err)
	}
	_, err = c.adm.CaptainApplicationCard(ctx, none, a)
	wantAppNotFound(t, "без городов, карточка", err)
	_, err = c.adm.ApproveCaptainApplication(ctx, none, a, nil)
	wantAppNotFound(t, "без городов, одобрение", err)

	sup := staffMember(perm.Support, "Поддержка", uuid.Nil)
	if _, err := c.adm.CaptainApplicationCard(ctx, sup, a); err != nil {
		t.Fatalf("поддержка читает: %v", err)
	}
	_, err = c.adm.ApproveCaptainApplication(ctx, sup, a, nil)
	wantProblem(t, "поддержка решает", err, http.StatusForbidden, httpx.CodeForbidden)
	if s := appStatus(t, c, a); s != "pending" {
		t.Fatalf("после отказов: %s", s)
	}
}

// Порядок (R9): pending — старые сверху, решённые — новые решения сверху; курсор по 2; удалённый заявитель скрыт;
// поля строки — ник, возраст по поясу города, стаж, has_phone без телефона.
func TestCaptainQueueOrderAndCursor(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	var pend []uuid.UUID
	for i, nick := range []string{"q_1", "q_2", "q_3"} {
		uid := c.player(t, nick, c.c.main)
		pend = append(pend, c.pending(t, uid, c.c.main, testNow.Add(-time.Duration(10-i)*time.Hour)))
	}
	gone := c.player(t, "q_gone", c.c.main)
	c.pending(t, gone, c.c.main, testNow.Add(-20*time.Hour))
	acctExec(t, c.env, `UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`, gone)

	u := c.player(t, "q_dec", c.c.main)
	old := c.decided(t, u, c.c.main, "approved", testNow.Add(-72*time.Hour), testNow.Add(-48*time.Hour))
	mid := c.decided(t, u, c.c.other, "approved", testNow.Add(-40*time.Hour), testNow.Add(-30*time.Hour))
	recent := c.decided(t, u, c.c.disabled, "approved", testNow.Add(-20*time.Hour), testNow.Add(-5*time.Hour))
	c.decided(t, u, c.c.main, "rejected", testNow.Add(-9*time.Hour), testNow.Add(-1*time.Hour))

	walk := func(status string) []uuid.UUID {
		var all []uuid.UUID
		cursor := ""
		for range 5 {
			list, next, err := c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{Status: status, Cursor: cursor, Limit: 2})
			if err != nil {
				t.Fatalf("%s: %v", status, err)
			}
			all = append(all, appIDs(list)...)
			if next == "" {
				return all
			}
			cursor = next
		}
		t.Fatalf("%s: курсор не кончается", status)
		return nil
	}
	if got := walk(""); !slices.Equal(got, pend) {
		t.Fatalf("pending: %v, ждали %v", got, pend)
	}
	if got := walk("approved"); !slices.Equal(got, []uuid.UUID{recent, mid, old}) {
		t.Fatalf("approved: %v", got)
	}
	// курсор другого статуса — 400 query.cursor
	_, next, err := c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{Limit: 2})
	if err != nil || next == "" {
		t.Fatalf("первая страница: %v %q", err, next)
	}
	_, _, err = c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{Status: "approved", Cursor: next, Limit: 2})
	if s, _ := problemStatus(err); s != http.StatusBadRequest {
		t.Fatalf("чужой курсор: %v", err)
	}

	list, _, err := c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{Limit: 1})
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	row := list[0]
	// родился 2000-01-01, сейчас 14.11.2026 в Нью-Йорке — 26 лет; аккаунт создан now() базы — 0 дней не отрицательно
	if row.Nickname != "q_1" || row.AgeYears != 26 || row.AccountAgeDays < 0 || !row.HasPhone || row.Status != "pending" ||
		row.DecidedAt != nil || row.DecidedByLabel != nil || row.ProposedFormat == nil {
		t.Fatalf("строка: %+v", row)
	}
}

// m6 ревью Task 4: keyset (created_at, id) при равных временах на границе страницы — каждая заявка ровно один раз:
// очередь — по возрастанию id при одном created_at, решённые — по убыванию id при одном reviewed_at.
func TestCaptainQueueCursorEqualTimes(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	at := testNow.Add(-3 * time.Hour)
	var pend []uuid.UUID
	for _, nick := range []string{"eq_1", "eq_2", "eq_3", "eq_4", "eq_5"} {
		pend = append(pend, c.pending(t, c.player(t, nick, c.c.main), c.c.main, at))
	}
	slices.SortFunc(pend, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	u := c.player(t, "eq_dec", c.c.main)
	var dec []uuid.UUID
	for _, city := range []uuid.UUID{c.c.main, c.c.other, c.c.disabled} {
		dec = append(dec, c.decided(t, u, city, "approved", at.Add(-time.Hour), at))
	}
	slices.SortFunc(dec, func(a, b uuid.UUID) int { return slices.Compare(b[:], a[:]) })

	walk := func(status string) []uuid.UUID {
		var all []uuid.UUID
		cursor := ""
		for range 6 {
			list, next, err := c.adm.ListCaptainApplications(ctx, owner, CaptainQueueFilter{Status: status, Cursor: cursor, Limit: 2})
			if err != nil {
				t.Fatalf("%s: %v", status, err)
			}
			all = append(all, appIDs(list)...)
			if next == "" {
				return all
			}
			cursor = next
		}
		t.Fatalf("%s: курсор не кончается", status)
		return nil
	}
	if got := walk(""); !slices.Equal(got, pend) {
		t.Fatalf("pending при равных created_at: %v, ждали %v", got, pend)
	}
	if got := walk("approved"); !slices.Equal(got, dec) {
		t.Fatalf("approved при равных reviewed_at: %v, ждали %v", got, dec)
	}
}

// Карточка: галочки по правилу условий (UnavailableCheck — profile и captaincy unavailable), история без текущей,
// связанные аккаунты по общему устройству.
func TestCaptainCard(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	prod := captainAdmin(t, c.env, []identity.CaptainCheck{identity.UnavailableCheck(identity.CheckProfileComplete),
		identity.UnavailableCheck(identity.CheckCityCaptaincy)}, c.links)
	uid := c.player(t, "card_p", c.c.main)
	twin := c.player(t, "card_twin", c.c.main)
	first := c.decided(t, uid, c.c.main, "rejected", testNow.Add(-30*24*time.Hour), testNow.Add(-29*24*time.Hour))
	second := c.decided(t, uid, c.c.main, "withdrawn", testNow.Add(-20*24*time.Hour), testNow.Add(-19*24*time.Hour))
	cur := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
	acctExec(t, c.env, `INSERT INTO devices (id, platform, install_mac, key_id, first_seen_at, last_seen_at)
		VALUES ('0193a1c9-0000-7000-8000-000000000001', 'ios', decode(repeat('ab', 32), 'hex'), 'AAAAAAAAAAA', now(), now())`)
	acctExec(t, c.env, `INSERT INTO device_accounts (device_id, user_id, signed_up, first_seen_at, last_seen_at)
		SELECT '0193a1c9-0000-7000-8000-000000000001', u, true, now(), now() FROM unnest($1::uuid[]) u`, []uuid.UUID{uid, twin})

	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	card, err := prod.CaptainApplicationCard(ctx, owner, cur)
	if err != nil {
		t.Fatal(err)
	}
	want := []CaptainCheckState{{"age", "ok"}, {"restrictions", "ok"}, {"profile", "unavailable"},
		{"captaincy", "unavailable"}}
	if !slices.Equal(card.Checks, want) {
		t.Fatalf("checks: %+v", card.Checks)
	}
	if !slices.Equal(appIDs(card.History), []uuid.UUID{second, first}) {
		t.Fatalf("история: %v", appIDs(card.History))
	}
	if card.LinkedAccounts != 1 || card.Application.ID != cur || card.Applicant.UserID != uid ||
		card.Applicant.Nickname != "card_p" || card.Applicant.CityID != c.c.main || card.Applicant.AgeYears != 26 {
		t.Fatalf("карточка: %+v", card)
	}
	// с портами «выполнено» — все ok
	card, err = c.adm.CaptainApplicationCard(ctx, owner, cur)
	if err != nil || card.Checks[2].State != "ok" || card.Checks[3].State != "ok" {
		t.Fatalf("AllowAll: %+v %v", card.Checks, err)
	}
	_, err = c.adm.CaptainApplicationCard(ctx, owner, uuid.New())
	wantAppNotFound(t, "нет заявки", err)
	acctExec(t, c.env, `UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`, uid)
	_, err = c.adm.CaptainApplicationCard(ctx, owner, cur)
	wantAppNotFound(t, "удалённый заявитель", err)
}

// Контакты: почта и телефон; чтение — в журнал identity.pii_read (объект — заявитель, город заявки, причина в
// reason), почты и телефона в записи нет; заявка удалённого — 404 без записи.
func TestCaptainContactAudited(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	uid := c.player(t, "contact_p", c.c.main)
	app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
	mod := moderatorOf(c.c.main)
	email, phone, err := c.adm.CaptainApplicantContact(ctx, mod, app, contactReason)
	if err != nil || email != "contact_p@example.com" || phone == nil || *phone != "+79990001122" {
		t.Fatalf("контакты: %q %v %v", email, phone, err)
	}
	rows := staffAudit(t, &resetEnv{env: c.env}, mod.StaffID)
	if len(rows) != 1 {
		t.Fatalf("записей %d", len(rows))
	}
	r := rows[0]
	if r.action != ActionPiiRead || r.objectType != objectUser || r.objectID == nil || *r.objectID != uid ||
		r.scope == nil || *r.scope != c.c.main || r.reason != contactReason {
		t.Fatalf("запись: %+v", r)
	}
	for _, needle := range []string{"contact_p@", "@example.com", "79990001122"} {
		if strings.Contains(r.whole, needle) {
			t.Fatalf("ПД в журнале (%s): %s", needle, r.whole)
		}
	}
	acctExec(t, c.env, `UPDATE users SET status = 'deleted', deleted_at = now() WHERE id = $1`, uid)
	_, _, err = c.adm.CaptainApplicantContact(ctx, mod, app, contactReason)
	wantAppNotFound(t, "удалённый", err)
	if n := len(staffAudit(t, &resetEnv{env: c.env}, mod.StaffID)); n != 1 {
		t.Fatalf("после 404 записей %d", n)
	}
}

// Одобрение под ролью admin: допуск с заявкой и испытательным сроком, заявка approved (версия 2), два события на
// агрегате заявки той же версии, аудит со scope_city_id, задача письма; users не тронуты.
func TestApproveCaptainApplication(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	uid := c.player(t, "approve_p", c.c.main)
	app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
	var userVer int64
	if err := c.owner.QueryRow(ctx, `SELECT version FROM users WHERE id = $1`, uid).Scan(&userVer); err != nil {
		t.Fatal(err)
	}
	mod := moderatorOf(c.c.main)
	comment := "Знаю по городскому турниру"
	got, err := c.adm.ApproveCaptainApplication(ctx, mod, app, &comment)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "approved" || got.DecidedAt == nil || !got.DecidedAt.Equal(testNow) || got.DecidedByLabel == nil ||
		*got.DecidedByLabel != "Модератор" || got.Comment == nil || *got.Comment != comment || got.UserID != uid ||
		got.Nickname != "approve_p" {
		t.Fatalf("ответ: %+v", got)
	}
	if n := count(t, c.owner, `SELECT count(*) FROM identity_read_captain_permits WHERE user_id = $1 AND city_id = $2
		AND application_id = $3 AND is_probationary`, uid, c.c.main, app); n != 1 {
		t.Fatalf("допуск: %d", n)
	}
	if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE application_id = $1 AND granted_by_staff_id = $2
		AND granted_at = $3`, app, mod.StaffID, testNow); n != 1 {
		t.Fatalf("строка допуска: %d", n)
	}
	if n := count(t, c.owner, `SELECT count(*) FROM captain_applications WHERE id = $1 AND version = 2
		AND reviewed_by_staff_id = $2`, app, mod.StaffID); n != 1 {
		t.Fatal("заявка не approved версии 2")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM outbox WHERE aggregate_type = 'captain_application'
		AND aggregate_id = $1 AND aggregate_version = 2 AND event_type IN ('identity.captain_application_decided',
		'identity.captain_permit_granted')`, app); n != 2 {
		t.Fatalf("событий %d", n)
	}
	if n := count(t, c.owner, `SELECT count(*) FROM outbox WHERE aggregate_id = $1
		AND payload->>'decision' = 'approved' AND payload->'rejection_code' = 'null'::jsonb`, app); n != 1 {
		t.Fatal("payload решения")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND actor_staff_id = $2 AND object_id = $3
		AND scope_city_id = $4 AND object_type = 'captain_application'`, ActionCaptainApplicationApproved, mod.StaffID, app,
		c.c.main); n != 1 {
		t.Fatal("аудит")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM audit_log WHERE object_id = $1 AND (coalesce(after::text, '') LIKE '%Знаю%'
		OR coalesce(before::text, '') LIKE '%Знаю%')`, app); n != 0 {
		t.Fatal("комментарий в аудите")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM river_job WHERE kind = 'mail.send' AND args->>'mail' = $1
		AND args->>'ref' = $2::text`, MailCaptainApplicationApproved, app); n != 1 {
		t.Fatalf("письмо: %d", n)
	}
	var ver int64
	if err := c.owner.QueryRow(ctx, `SELECT version FROM users WHERE id = $1`, uid).Scan(&ver); err != nil || ver != userVer {
		t.Fatalf("users.version %d → %d", userVer, ver)
	}
	// повтор после решения — 409 application_closed, второго допуска нет
	_, err = c.adm.ApproveCaptainApplication(ctx, mod, app, nil)
	wantProblem(t, "повтор", err, http.StatusConflict, identity.CodeApplicationClosed)
}

// Коды одобрения: прод-порты — captain_ineligible [checks_unavailable]; too_young; self_action по связи; закрыта
// после отзыва; заявитель в периоде удаления — application_closed; забанен — restricted; нет — 404. Ни один отказ
// ничего не пишет.
func TestApproveCodes(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	prod := captainAdmin(t, c.env, []identity.CaptainCheck{identity.UnavailableCheck(identity.CheckProfileComplete),
		identity.UnavailableCheck(identity.CheckCityCaptaincy)}, c.links)
	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	type tc struct {
		name    string
		adm     *App
		prepare func(uid, app uuid.UUID)
		status  int
		code    string
		reasons []string
	}
	cases := []tc{
		{name: "прод-порты", adm: prod, status: http.StatusConflict, code: identity.CodeCaptainIneligible,
			reasons: []string{domain.ReasonChecksUnavailable}},
		{name: "too_young", prepare: func(uid, _ uuid.UUID) {
			acctExec(t, c.env, `UPDATE users SET birth_date = '2016-01-01' WHERE id = $1`, uid)
		}, status: http.StatusConflict, code: identity.CodeCaptainIneligible, reasons: []string{domain.ReasonTooYoung}},
		{name: "self_action", prepare: func(uid, _ uuid.UUID) { c.links.set(owner.StaffID, uid) },
			status: http.StatusConflict, code: identity.CodeSelfAction},
		// m1 ревью Task 4: учётку сотрудника изменили после загрузки сессии (версия под замком больше) — 403
		{name: "сотрудник изменён", prepare: func(uuid.UUID, uuid.UUID) { c.links.setVersion(owner.StaffID, owner.Version+1) },
			status: http.StatusForbidden, code: httpx.CodeForbidden},
		{name: "отозвана", prepare: func(uid, app uuid.UUID) {
			if _, err := c.a.WithdrawCaptainApplication(ctx, uid, app); err != nil {
				t.Fatal(err)
			}
		}, status: http.StatusConflict, code: identity.CodeApplicationClosed},
		{name: "период удаления", prepare: func(uid, _ uuid.UUID) {
			acctExec(t, c.env, `UPDATE users SET status = 'pending_deletion', status_before_deletion = 'active',
				deletion_requested_at = now(), purge_at = now() + interval '30 days' WHERE id = $1`, uid)
		}, status: http.StatusConflict, code: identity.CodeApplicationClosed},
		{name: "забанен", prepare: func(uid, _ uuid.UUID) {
			acctExec(t, c.env, `UPDATE users SET status = 'banned' WHERE id = $1`, uid)
			restrict(t, c.env, uid, "login", nil)
		}, status: http.StatusConflict, code: identity.CodeCaptainIneligible, reasons: []string{domain.ReasonRestricted}},
		{name: "нет заявки", status: http.StatusNotFound, code: identity.CodeApplicationNotFound},
	}
	for i, k := range cases {
		uid := c.player(t, "codes_"+string(rune('a'+i)), c.c.main)
		app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
		target := app
		if k.name == "нет заявки" {
			target = uuid.New()
		}
		if k.prepare != nil {
			k.prepare(uid, app)
		}
		adm := k.adm
		if adm == nil {
			adm = c.adm
		}
		_, err := adm.ApproveCaptainApplication(ctx, owner, target, nil)
		wantProblem(t, k.name, err, k.status, k.code)
		if k.reasons != nil {
			pe, _ := errors.AsType[httpx.ProblemError](err)
			if !slices.Equal(pe.Problem().Reasons, k.reasons) {
				t.Fatalf("%s: reasons %v", k.name, pe.Problem().Reasons)
			}
		}
		if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 0 {
			t.Fatalf("%s: допуск выдан", k.name)
		}
		if n := count(t, c.owner, `SELECT count(*) FROM audit_log WHERE actor_staff_id = $1`, owner.StaffID); n != 0 {
			t.Fatalf("%s: аудит при отказе", k.name)
		}
		c.links.set(owner.StaffID, uuid.Nil)
		c.links.setVersion(owner.StaffID, owner.Version)
	}
}

// R38: связь сотрудника перечитывается в транзакции решения портом, не берётся из снимка сессии: сессия загружена
// без связи (LinkedUserID = Nil), связь с заявителем закоммичена позже — одобрение и отказ — 409 self_action.
func TestApproveSelfLinkRace(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	uid := c.player(t, "self_p", c.c.main)
	app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
	owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
	c.links.set(owner.StaffID, uid)
	_, err := c.adm.ApproveCaptainApplication(ctx, owner, app, nil)
	wantProblem(t, "одобрение", err, http.StatusConflict, identity.CodeSelfAction)
	_, err = c.adm.RejectCaptainApplication(ctx, owner, app, "insufficient_info", nil)
	wantProblem(t, "отказ", err, http.StatusConflict, identity.CodeSelfAction)
	if s := appStatus(t, c, app); s != "pending" {
		t.Fatalf("статус %s", s)
	}
	// чтение своей заявки не запрещено
	if _, err := c.adm.CaptainApplicationCard(ctx, owner, app); err != nil {
		t.Fatalf("карточка своей: %v", err)
	}
}

// Отказ: rejected с кодом и комментарием, событие decided с кодом, аудит с кодом в after, письмо; other без
// комментария (и из одних пробелов) — 422 body.comment required; управляющий символ — 400 pattern; после отказа
// подача игроком — application_cooldown.
func TestRejectCaptainApplication(t *testing.T) {
	t.Parallel()
	c := newCapEnv(t)
	ctx := context.Background()
	uid := c.player(t, "reject_p", c.c.main)
	c.a.captainChecks = identitytest.AllowAll()
	app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
	mod := moderatorOf(c.c.main)

	for _, k := range []struct {
		name    string
		code    string
		comment *string
		status  int
		rule    string
	}{
		{"other без комментария", "other", nil, http.StatusUnprocessableEntity, "required"},
		{"other из пробелов", "other", ptr("    "), http.StatusUnprocessableEntity, "required"},
		{"NUL", "insufficient_info", ptr("a\x00b"), http.StatusBadRequest, "pattern"},
		{"ESC", "other", ptr("a\x1bb"), http.StatusBadRequest, "pattern"},
		{"пробелы не other", "insufficient_info", ptr("   "), http.StatusBadRequest, "minLength"},
		{"неизвестный код", "nope", nil, http.StatusBadRequest, "enum"},
	} {
		_, err := c.adm.RejectCaptainApplication(ctx, mod, app, k.code, k.comment)
		pe, ok := errors.AsType[httpx.ProblemError](err)
		if !ok {
			t.Fatalf("%s: %v", k.name, err)
		}
		p := pe.Problem()
		wantField := "body.comment"
		if k.rule == "enum" {
			wantField = "body.rejection_code"
		}
		if p.Status != k.status || p.Code != httpx.CodeValidationFailed || len(p.Errors) != 1 ||
			p.Errors[0].Field != wantField || p.Errors[0].Code != k.rule {
			t.Fatalf("%s: %+v", k.name, p)
		}
	}
	if s := appStatus(t, c, app); s != "pending" {
		t.Fatalf("после отказов валидации: %s", s)
	}

	comment := "Расскажите подробнее,\nгде и когда собираете игры"
	got, err := c.adm.RejectCaptainApplication(ctx, mod, app, "other", &comment)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "rejected" || got.RejectionCode == nil || *got.RejectionCode != "other" || got.Comment == nil ||
		*got.Comment != comment || got.DecidedByLabel == nil {
		t.Fatalf("ответ: %+v", got)
	}
	if n := count(t, c.owner, `SELECT count(*) FROM outbox WHERE aggregate_id = $1 AND aggregate_version = 2
		AND event_type = 'identity.captain_application_decided' AND payload->>'decision' = 'rejected'
		AND payload->>'rejection_code' = 'other'`, app); n != 1 {
		t.Fatal("событие отказа")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM outbox WHERE aggregate_id = $1`, app); n != 1 {
		t.Fatal("лишние события")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM audit_log WHERE action = $1 AND object_id = $2 AND scope_city_id = $3
		AND after->>'reason_code' = 'other' AND after::text NOT LIKE '%подробнее%'`, ActionCaptainApplicationRejected, app,
		c.c.main); n != 1 {
		t.Fatal("аудит отказа")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1 AND args->>'ref' = $2::text`,
		MailCaptainApplicationRejected, app); n != 1 {
		t.Fatal("письмо отказа")
	}
	if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 0 {
		t.Fatal("допуск при отказе")
	}
	_, err = c.a.SubmitCaptainApplication(ctx, uid, CaptainApplicationInput{})
	wantProblem(t, "подача после отказа", err, http.StatusConflict, identity.CodeApplicationCooldown)
}

// opDecide — метка барьера решения: строка заявки под замком (hooks.afterFirstLock).
func decideBarrier(a *App) (inside, resume chan struct{}) {
	inside, resume = make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.hooks = &hooks{afterFirstLock: func(op string) {
		if op == opDecideCaptainApplication {
			once.Do(func() { close(inside); <-resume })
		}
	}}
	return inside, resume
}

// waitInside — ждёт барьер не дольше 10 с; не дождался — отпускает и падает.
func waitInside(t *testing.T, inside, resume chan struct{}, wg *sync.WaitGroup) {
	t.Helper()
	select {
	case <-inside:
	case <-time.After(10 * time.Second):
		close(resume)
		wg.Wait()
		t.Fatal("решение не дошло до барьера")
	}
}

// holdApplication — строка заявки под замком соединением владельца (очередь ожидающих — по порядку прихода).
func holdApplication(t *testing.T, c *capEnv, app uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	hold, err := c.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Rollback(ctx) })
	if _, err := hold.Exec(ctx, `SELECT 1 FROM captain_applications WHERE id = $1 FOR UPDATE`, app); err != nil {
		t.Fatal(err)
	}
	return hold
}

// Отзыв игроком и одобрение — ровно один исход, без 40P01 и 500: одобрение первым — approved, отзыв 409 closed;
// отзыв первым — withdrawn, одобрение 409 closed.
func TestWithdrawVsApprove(t *testing.T) {
	t.Parallel()
	t.Run("одобрение первым", func(t *testing.T) {
		t.Parallel()
		c := newCapEnv(t)
		ctx := context.Background()
		uid := c.player(t, "wva_a", c.c.main)
		app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
		owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
		inside, resume := decideBarrier(c.adm)
		var wg sync.WaitGroup
		var apErr, wdErr error
		wg.Go(func() { _, apErr = c.adm.ApproveCaptainApplication(ctx, owner, app, nil) })
		waitInside(t, inside, resume, &wg)
		wg.Go(func() { _, wdErr = c.a.WithdrawCaptainApplication(ctx, uid, app) })
		waited := lockWaiters(t, c.owner, 1)
		close(resume)
		wg.Wait()
		if !waited {
			t.Fatalf("отзыв не ждал замка заявки: %v %v", apErr, wdErr)
		}
		if apErr != nil {
			t.Fatalf("одобрение: %v", apErr)
		}
		wantProblem(t, "отзыв", wdErr, http.StatusConflict, identity.CodeApplicationClosed)
		if s := appStatus(t, c, app); s != "approved" {
			t.Fatal(s)
		}
	})
	t.Run("отзыв первым", func(t *testing.T) {
		t.Parallel()
		c := newCapEnv(t)
		ctx := context.Background()
		uid := c.player(t, "wva_b", c.c.main)
		app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
		owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
		hold := holdApplication(t, c, app)
		var wg sync.WaitGroup
		var apErr, wdErr error
		wg.Go(func() { _, wdErr = c.a.WithdrawCaptainApplication(ctx, uid, app) })
		waited := lockWaiters(t, c.owner, 1)
		wg.Go(func() { _, apErr = c.adm.ApproveCaptainApplication(ctx, owner, app, nil) })
		waited = waited && lockWaiters(t, c.owner, 2)
		if err := hold.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if !waited {
			t.Fatalf("не встали в очередь: %v %v", wdErr, apErr)
		}
		if wdErr != nil {
			t.Fatalf("отзыв: %v", wdErr)
		}
		wantProblem(t, "одобрение", apErr, http.StatusConflict, identity.CodeApplicationClosed)
		if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 0 {
			t.Fatal("допуск после отзыва")
		}
	})
}

// Два решения одной заявки (одобрение ∥ одобрение, одобрение ∥ отказ) — одна 200, вторая 409 closed; один допуск.
func TestApproveVsApprove(t *testing.T) {
	t.Parallel()
	for _, second := range []string{"approve", "reject"} {
		t.Run(second, func(t *testing.T) {
			t.Parallel()
			c := newCapEnv(t)
			ctx := context.Background()
			uid := c.player(t, "ava_"+second, c.c.main)
			app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
			a, b := staffMember(perm.Owner, "Ольга", uuid.Nil), staffMember(perm.Admin, "Админ", uuid.Nil)
			inside, resume := decideBarrier(c.adm)
			var wg sync.WaitGroup
			var e1, e2 error
			wg.Go(func() { _, e1 = c.adm.ApproveCaptainApplication(ctx, a, app, nil) })
			waitInside(t, inside, resume, &wg)
			wg.Go(func() {
				if second == "approve" {
					_, e2 = c.adm.ApproveCaptainApplication(ctx, b, app, nil)
				} else {
					_, e2 = c.adm.RejectCaptainApplication(ctx, b, app, "insufficient_info", nil)
				}
			})
			waited := lockWaiters(t, c.owner, 1)
			close(resume)
			wg.Wait()
			if !waited {
				t.Fatalf("второе решение не ждало: %v %v", e1, e2)
			}
			if e1 != nil {
				t.Fatalf("первое: %v", e1)
			}
			wantProblem(t, "второе", e2, http.StatusConflict, identity.CodeApplicationClosed)
			if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 1 {
				t.Fatalf("допусков %d", n)
			}
		})
	}
}

// Одобрение и удаление аккаунта (R6): одобрение без замка users, FK role_assignments берёт FOR KEY SHARE —
// совместим с FOR NO KEY UPDATE удаления: без 40P01. Одобрение первым — approved, удаление проходит, допуск остаётся
// до анонимизации (§3.11); удаление первым — withdrawn, одобрение 409 closed.
func TestApproveVsDeleteAccount(t *testing.T) {
	t.Parallel()
	t.Run("одобрение первым", func(t *testing.T) {
		t.Parallel()
		c := newCapEnv(t)
		ctx := context.Background()
		uid := c.player(t, "avd_a", c.c.main)
		app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
		sid := issue(t, c.env, uid, web("x")).SessionID
		owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
		inside, resume := decideBarrier(c.adm)
		var wg sync.WaitGroup
		var apErr, delErr error
		wg.Go(func() { _, apErr = c.adm.ApproveCaptainApplication(ctx, owner, app, nil) })
		waitInside(t, inside, resume, &wg)
		wg.Go(func() { _, delErr = c.a.DeleteAccount(ctx, sid, "avd_a") })
		waited := lockWaiters(t, c.owner, 1)
		close(resume)
		wg.Wait()
		if !waited {
			t.Fatalf("удаление не ждало замка заявки: %v %v", apErr, delErr)
		}
		if apErr != nil || delErr != nil {
			t.Fatalf("одобрение %v, удаление %v", apErr, delErr)
		}
		if s := appStatus(t, c, app); s != "approved" {
			t.Fatal(s)
		}
		if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1 AND revoked_at IS NULL`, uid); n != 1 {
			t.Fatal("допуск пропал")
		}
	})
	t.Run("удаление первым", func(t *testing.T) {
		t.Parallel()
		c := newCapEnv(t)
		ctx := context.Background()
		uid := c.player(t, "avd_b", c.c.main)
		app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
		sid := issue(t, c.env, uid, web("x")).SessionID
		owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
		hold := holdApplication(t, c, app)
		var wg sync.WaitGroup
		var apErr, delErr error
		wg.Go(func() { _, delErr = c.a.DeleteAccount(ctx, sid, "avd_b") })
		waited := lockWaiters(t, c.owner, 1)
		wg.Go(func() { _, apErr = c.adm.ApproveCaptainApplication(ctx, owner, app, nil) })
		waited = waited && lockWaiters(t, c.owner, 2)
		if err := hold.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if !waited {
			t.Fatalf("не встали в очередь: %v %v", delErr, apErr)
		}
		if delErr != nil {
			t.Fatalf("удаление: %v", delErr)
		}
		wantProblem(t, "одобрение", apErr, http.StatusConflict, identity.CodeApplicationClosed)
		if s := appStatus(t, c, app); s != "withdrawn" {
			t.Fatal(s)
		}
	})
}

// Решение по заявке и смена города (teams, ChangeCityInTx; m1 итогового ревью А) — ровно один исход, без 40P01:
// решение первым — заявка остаётся approved/rejected (допуск прежнего города при одобрении жив), письма
// withdrawn_city нет, город сменён; смена города первой — withdrawn с письмом, решение 409 closed, допусков нет.
func TestDecideVsChangeCity(t *testing.T) {
	t.Parallel()
	decide := func(c *capEnv, app uuid.UUID, kind string) error {
		owner := staffMember(perm.Owner, "Ольга", uuid.Nil)
		if kind == "approve" {
			_, err := c.adm.ApproveCaptainApplication(context.Background(), owner, app, nil)
			return err
		}
		_, err := c.adm.RejectCaptainApplication(context.Background(), owner, app, "insufficient_info", nil)
		return err
	}
	withdrawnMail := func(t *testing.T, c *capEnv, app uuid.UUID) int {
		return count(t, c.owner, `SELECT count(*) FROM river_job WHERE args->>'mail' = $1 AND args->>'ref' = $2::text`,
			MailCaptainApplicationWithdrawnCity, app)
	}
	for _, kind := range []string{"approve", "reject"} {
		t.Run(kind+"/решение первым", func(t *testing.T) {
			t.Parallel()
			c := newCapEnv(t)
			uid := c.player(t, "dvc_a_"+kind, c.c.main)
			app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
			inside, resume := decideBarrier(c.adm)
			var wg sync.WaitGroup
			var dErr, cErr error
			wg.Go(func() { dErr = decide(c, app, kind) })
			waitInside(t, inside, resume, &wg)
			wg.Go(func() { cErr = changeCity(c.env, uid, c.c.other) })
			waited := lockWaiters(t, c.owner, 1)
			close(resume)
			wg.Wait()
			if !waited {
				t.Fatalf("смена города не ждала замка заявки: %v %v", dErr, cErr)
			}
			if dErr != nil || cErr != nil {
				t.Fatalf("решение %v, смена города %v", dErr, cErr)
			}
			want := map[string]string{"approve": "approved", "reject": "rejected"}[kind]
			if s := appStatus(t, c, app); s != want {
				t.Fatal(s)
			}
			if n := withdrawnMail(t, c, app); n != 0 {
				t.Fatal("письмо withdrawn_city после решения")
			}
			permits := map[string]int{"approve": 1, "reject": 0}[kind]
			if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1 AND scope_id = $2
				AND revoked_at IS NULL`, uid, c.c.main); n != permits {
				t.Fatalf("живых допусков прежнего города %d", n)
			}
			if n := count(t, c.owner, `SELECT count(*) FROM users WHERE id = $1 AND city_id = $2`, uid, c.c.other); n != 1 {
				t.Fatal("город не сменён")
			}
		})
		t.Run(kind+"/смена города первой", func(t *testing.T) {
			t.Parallel()
			c := newCapEnv(t)
			ctx := context.Background()
			uid := c.player(t, "dvc_b_"+kind, c.c.main)
			app := c.pending(t, uid, c.c.main, testNow.Add(-time.Hour))
			hold := holdApplication(t, c, app)
			var wg sync.WaitGroup
			var dErr, cErr error
			wg.Go(func() { cErr = changeCity(c.env, uid, c.c.other) })
			waited := lockWaiters(t, c.owner, 1)
			wg.Go(func() { dErr = decide(c, app, kind) })
			waited = waited && lockWaiters(t, c.owner, 2)
			if err := hold.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			if !waited {
				t.Fatalf("не встали в очередь: %v %v", cErr, dErr)
			}
			if cErr != nil {
				t.Fatalf("смена города: %v", cErr)
			}
			wantProblem(t, "решение", dErr, http.StatusConflict, identity.CodeApplicationClosed)
			if s := appStatus(t, c, app); s != "withdrawn" {
				t.Fatal(s)
			}
			if n := withdrawnMail(t, c, app); n != 1 {
				t.Fatal("нет письма withdrawn_city")
			}
			if n := count(t, c.owner, `SELECT count(*) FROM role_assignments WHERE user_id = $1`, uid); n != 0 {
				t.Fatal("допуск после отзыва сменой города")
			}
		})
	}
}
