package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/page"
)

// Заявки в капитаны в админке (спека §3.12 п. 6, §7.1, §7.6; план I-4, Task 4, R6, R7, R9, R10, R37, R38). Роль admin:
// users и sessions не пишутся и не берутся под замок (К2: FOR … на users — 42501); под замок — только строка
// captain_applications (первый замок админского сценария, R7), затем учётка сотрудника FOR SHARE портом StaffLinks
// (R38), затем role_assignments. Скоуп модератора — по auth.GrantScoped, не по len(CityIDs) (R37): заявка чужого
// города — 404, как несуществующая; заявки удалённых аккаунтов не видны нигде (R9). Форма заявки и комментарии — не в
// аудите и не в событиях (свободный текст, ПД).

const (
	permCaptainRead   = "identity.captain_applications.read"
	permCaptainDecide = "identity.captain_applications.decide"
	// opDecideCaptainApplication — метка hooks.afterFirstLock решения: строка заявки под замком.
	opDecideCaptainApplication = "decide_captain_application"
	// roleAssignmentsUnique — живой допуск (user, role, scope) один (0003): 23505 по нему — допуск уже есть.
	roleAssignmentsUnique = "role_assignments_unique"

	applicationApproved = "approved"
	applicationRejected = "rejected"
	rejectionOther      = "other"
	maxCaptainComment   = 1000 // символов (CHECK captain_applications_comment_check, контракт)
)

// Наборы полей курсоров очереди (page.Keyset): у pending и решённых — разные поля и порядок.
const (
	captainQueuePendingSet = "identity.captain_queue_pending(created_at,id)"
	captainQueueDecidedSet = "identity.captain_queue_decided(decided_at,id)"
)

// AdminApplication — заявка в очереди и решённая заявка (IdentityCaptainApplication): поля заявки, заявитель (ник,
// возраст по поясу города заявки, стаж аккаунта в полных сутках), подпись решившего (nil — не решена или отозвана).
type AdminApplication struct {
	CaptainApplication
	UserID         uuid.UUID
	Nickname       string
	AgeYears       int
	AccountAgeDays int
	DecidedByLabel *string
}

// AdminApplicant — заявитель в карточке; CityID — текущий город игрока (uuid.Nil — города нет, как в getMe).
type AdminApplicant struct {
	UserID         uuid.UUID
	Nickname       string
	CityID         uuid.UUID
	AgeYears       int
	AccountAgeDays int
}

// CaptainCheckState — условие карточки (age | restrictions | profile | captaincy) и его состояние
// (ok | failed | unavailable).
type CaptainCheckState struct{ Check, State string }

// AdminApplicationCard — карточка заявки: заявка, заявитель, условия сейчас (одно правило R5), прошлые заявки игрока
// (новые сверху, без текущей), число других аккаунтов с общим устройством.
type AdminApplicationCard struct {
	Application    AdminApplication
	Applicant      AdminApplicant
	Checks         []CaptainCheckState
	History        []AdminApplication
	LinkedAccounts int
}

// CaptainQueueFilter — фильтр очереди: Status ("" — pending), CityID (uuid.Nil — все города скоупа), курсор и
// размер страницы (page.Limit уже применён).
type CaptainQueueFilter struct {
	Status string
	CityID uuid.UUID
	Cursor string
	Limit  int
}

// cardChecks — порядок галочек карточки (IdentityCaptainCheck).
var cardChecks = []string{checkAge, checkRestrictions, checkProfile, checkCaptaincy}

// fieldProblem — validation.failed по полю тела с заданным статусом (у httpx.NewFieldError всегда 400): отказ
// без комментария при other — 422 по контракту.
type fieldProblem struct {
	status      int
	field, rule string
}

func (e fieldProblem) Error() string {
	return "httpx: " + httpx.CodeValidationFailed + ": " + e.field + " " + e.rule
}

func (e fieldProblem) Problem() httpx.Problem {
	return httpx.Problem{Status: e.status, Code: httpx.CodeValidationFailed,
		Errors: []httpx.FieldError{{Field: e.field, Code: e.rule}}}
}

// checkDecisionComment — комментарий решения (увидит игрок, письмо): управляющие символы, кроме перевода строки и
// табуляции, — 400 pattern (NUL Postgres в text не примет — был бы 500); длина — до 1000 символов (контракт проверил,
// CHECK — защита в глубину). blank — нет ни одного видимого символа (пробелы и невидимые): у other это «нет
// комментария», у прочих — 400 minLength (передал поле — значит не пустое; как форма заявки, m3 ревью Task 3).
func checkDecisionComment(c *string) (blank bool, err error) {
	if c == nil {
		return true, nil
	}
	visible := false
	for _, r := range *c {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false, httpx.NewFieldError("body.comment", rulePattern)
		}
		if !unicode.IsSpace(r) && !emailaddr.IsInvisible(r) {
			visible = true
		}
	}
	if len([]rune(*c)) > maxCaptainComment {
		return false, httpx.NewFieldError("body.comment", "maxLength")
	}
	return !visible, nil
}

// staffMayCaptain — сотрудник в стадии active без Reauth и с правом allow или scoped; иначе 403. scoped — модератор
// (видит только свои города, по Decide).
func staffMayCaptain(st *auth.Staff, perm string) error {
	if st == nil || st.Stage != auth.StageActive || st.Reauth {
		return errStaffForbidden
	}
	if g := st.Grant(perm); g != auth.GrantAllow && g != auth.GrantScoped {
		return errStaffForbidden
	}
	return nil
}

// ageAndTenure — возраст заявителя в годах по поясу города заявки (пояса нет или он битый — UTC: карточке нужно
// число, проверку возраста делает правило условий) и стаж аккаунта в полных сутках (не меньше 0).
func ageAndTenure(birth pgtype.Date, created, now time.Time, tz *string) (age, days int) {
	loc := time.UTC
	if tz != nil && *tz != "" {
		if l, err := time.LoadLocation(*tz); err == nil {
			loc = l
		}
	}
	if birth.Valid {
		if a, err := domain.AgeYears(birth.Time, now, loc); err == nil {
			age = a
		}
	}
	if d := now.Sub(created); d > 0 {
		days = int(d / (24 * time.Hour))
	}
	return age, days
}

func adminApplicationView(r identitydb.CaptainApplication, nickname string, birth pgtype.Date, created, now time.Time,
	tz *string) AdminApplication {
	age, days := ageAndTenure(birth, created, now, tz)
	return AdminApplication{CaptainApplication: applicationView(r), UserID: r.UserID, Nickname: nickname, AgeYears: age,
		AccountAgeDays: days, DecidedByLabel: r.ReviewedByLabel}
}

// ListCaptainApplications — listCaptainApplications: pending — (created_at, id) ASC, прочие статусы — (decided_at, id)
// DESC; у scoped — только города модератора (без городов — пусто); фильтр чужого города — пустая страница. Курсор
// другого статуса или испорченный — page.ErrBadCursor. next — "" на последней странице.
func (a *App) ListCaptainApplications(ctx context.Context, st *auth.Staff, f CaptainQueueFilter) ([]AdminApplication, string, error) {
	if err := staffMayCaptain(st, permCaptainRead); err != nil {
		return nil, "", err
	}
	scoped := st.Grant(permCaptainRead) == auth.GrantScoped
	cities := append(make([]uuid.UUID, 0, len(st.CityIDs)), st.CityIDs...)
	var city *uuid.UUID
	if f.CityID != uuid.Nil {
		city = &f.CityID
	}
	status := cmp.Or(f.Status, applicationPending)
	set := captainQueueDecidedSet + ":" + status
	if status == applicationPending {
		set = captainQueuePendingSet
	}
	var (
		hasCursor bool
		curAt     time.Time
		curID     uuid.UUID
	)
	if f.Cursor != "" {
		k, err := page.DecodeKeyset(f.Cursor, set, page.KindTime, page.KindUUID)
		if err != nil {
			return nil, "", err
		}
		hasCursor, curAt, curID = true, k.Values[0].(time.Time), k.Values[1].(uuid.UUID)
	}
	lim := int32(f.Limit) + 1 //nolint:gosec // G115: limit ≤ page.MaxLimit
	now := a.clock.Now()
	q := identitydb.New(a.pool)
	out := make([]AdminApplication, 0, f.Limit)
	var keys [][2]any
	if status == applicationPending {
		rows, err := q.AdminCaptainQueuePending(ctx, identitydb.AdminCaptainQueuePendingParams{Scoped: scoped,
			CityIds: cities, CityID: city, HasCursor: hasCursor, CursorAt: curAt, CursorID: curID, Lim: lim})
		if err != nil {
			return nil, "", fmt.Errorf("identity: очередь заявок: %w", err)
		}
		for _, r := range rows {
			out = append(out, adminApplicationView(r.CaptainApplication, r.Nickname, r.BirthDate, r.UserCreatedAt, now,
				r.CityTimezone))
			keys = append(keys, [2]any{r.CaptainApplication.CreatedAt, r.CaptainApplication.ID})
		}
	} else {
		rows, err := q.AdminCaptainQueueDecided(ctx, identitydb.AdminCaptainQueueDecidedParams{
			Status: identitydb.ApplicationStatus(status), Scoped: scoped, CityIds: cities, CityID: city,
			HasCursor: hasCursor, CursorAt: curAt, CursorID: curID, Lim: lim})
		if err != nil {
			return nil, "", fmt.Errorf("identity: заявки статуса %s: %w", status, err)
		}
		for _, r := range rows {
			out = append(out, adminApplicationView(r.CaptainApplication, r.Nickname, r.BirthDate, r.UserCreatedAt, now,
				r.CityTimezone))
			keys = append(keys, [2]any{r.DecidedKey, r.CaptainApplication.ID})
		}
	}
	var next string
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := keys[f.Limit-1]
		next = page.EncodeKeyset(page.Keyset{Set: set, Values: []any{last[0], last[1]}}, page.KindTime, page.KindUUID)
	}
	return out, next, nil
}

// visibleApplication — заявка и заявитель для сотрудника: нет заявки, город вне скоупа права perm (Decide) или аккаунт
// удалён — errApplicationNotFound. lock — строка заявки FOR UPDATE (решение), иначе без замка.
func (a *App) visibleApplication(ctx context.Context, tx pgx.Tx, st *auth.Staff, perm string, appID uuid.UUID,
	lock bool) (identitydb.CaptainApplication, identitydb.AdminCaptainApplicantRow, error) {
	q := identitydb.New(tx)
	var (
		r   identitydb.CaptainApplication
		err error
	)
	if lock {
		r, err = q.AdminLockCaptainApplication(ctx, appID)
	} else {
		r, err = q.AdminCaptainApplication(ctx, appID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return r, identitydb.AdminCaptainApplicantRow{}, errApplicationNotFound
	}
	if err != nil {
		return r, identitydb.AdminCaptainApplicantRow{}, fmt.Errorf("identity: заявка: %w", err)
	}
	if st.Decide(perm, r.CityID) != auth.Allow {
		return r, identitydb.AdminCaptainApplicantRow{}, errApplicationNotFound
	}
	u, err := q.AdminCaptainApplicant(ctx, identitydb.AdminCaptainApplicantParams{CityID: r.CityID, UserID: r.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && u.Status == identitydb.UserStatusDeleted {
		return r, u, errApplicationNotFound
	}
	if err != nil {
		return r, u, fmt.Errorf("identity: заявитель: %w", err)
	}
	return r, u, nil
}

func tzPtr(s string) *string { return &s }

// CaptainApplicationCard — getCaptainApplication: читающая транзакция под admin (порты условий зовутся в ней).
func (a *App) CaptainApplicationCard(ctx context.Context, st *auth.Staff, appID uuid.UUID) (AdminApplicationCard, error) {
	if err := staffMayCaptain(st, permCaptainRead); err != nil {
		return AdminApplicationCard{}, err
	}
	var out AdminApplicationCard
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, u, err := a.visibleApplication(ctx, tx, st, permCaptainRead, appID, false)
		if err != nil {
			return err
		}
		now := a.clock.Now()
		tz := tzPtr(u.CityTimezone)
		out.Application = adminApplicationView(r, u.Nickname, u.BirthDate, u.CreatedAt, now, tz)
		age, days := ageAndTenure(u.BirthDate, u.CreatedAt, now, tz)
		out.Applicant = AdminApplicant{UserID: r.UserID, Nickname: u.Nickname, CityID: cityOf(u.CityID), AgeYears: age,
			AccountAgeDays: days}
		v, err := a.evaluateCaptaincy(ctx, tx, r.UserID, r.CityID, now)
		if err != nil {
			return err
		}
		out.Checks = make([]CaptainCheckState, 0, len(cardChecks))
		for _, k := range cardChecks {
			out.Checks = append(out.Checks, CaptainCheckState{Check: k, State: cmp.Or(v.Checks[k], checkUnavailable)})
		}
		q := identitydb.New(tx)
		hist, err := q.AdminCaptainHistory(ctx, identitydb.AdminCaptainHistoryParams{UserID: r.UserID, ExcludeID: r.ID})
		if err != nil {
			return fmt.Errorf("identity: история заявок: %w", err)
		}
		out.History = make([]AdminApplication, 0, len(hist))
		for _, h := range hist {
			out.History = append(out.History, adminApplicationView(h, u.Nickname, u.BirthDate, u.CreatedAt, now, tz))
		}
		linked, err := q.AdminCaptainLinkedAccounts(ctx, r.UserID)
		if err != nil {
			return fmt.Errorf("identity: связанные аккаунты: %w", err)
		}
		out.LinkedAccounts = int(linked)
		return nil
	})
	if err != nil {
		return AdminApplicationCard{}, err
	}
	return out, nil
}

// CaptainApplicantContact — getCaptainApplicationContact: почта аккаунта и телефон заявки; в одной транзакции с
// чтением — запись identity.pii_read (объект — заявитель, город заявки, причина — Entry.Reason; почты и телефона в
// записи нет, R10). Заявки нет или она не видна — 404 без записи.
func (a *App) CaptainApplicantContact(ctx context.Context, st *auth.Staff, appID uuid.UUID, reason string) (string, *string, error) {
	if err := staffMayCaptain(st, permPiiRead); err != nil {
		return "", nil, err
	}
	var (
		email string
		phone *string
	)
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, _, err := a.visibleApplication(ctx, tx, st, permPiiRead, appID, false)
		if err != nil {
			return err
		}
		if email, err = identitydb.New(tx).AdminCaptainApplicantEmail(ctx, r.UserID); err != nil {
			return fmt.Errorf("identity: почта заявителя: %w", err)
		}
		phone = r.Phone
		m := approvals.MetaFrom(ctx)
		return audit.WriteRead(ctx, tx, audit.Entry{ActorStaffID: st.StaffID, ActorRole: st.Role, ActorLabel: st.Label,
			Action: ActionPiiRead, ObjectType: objectUser, ObjectID: r.UserID, ScopeCityID: r.CityID, Reason: reason,
			IP: m.IP, UserAgent: m.UserAgent})
	})
	if err != nil {
		return "", nil, err
	}
	return email, phone, nil
}

// decision — общее одобрения и отказа (R6, R38): строка заявки FOR UPDATE (нет, чужой город, заявитель удалён — 404)
// → hooks.afterFirstLock → связь сотрудника, перечитанная портом в этой транзакции (своя учётка — 409 self_action) →
// не pending или заявитель в периоде удаления — 409 application_closed (заявку закроет удаление) → apply.
func (a *App) decision(ctx context.Context, st *auth.Staff, appID uuid.UUID,
	apply func(ctx context.Context, tx pgx.Tx, r identitydb.CaptainApplication, u identitydb.AdminCaptainApplicantRow,
		now time.Time) (identitydb.CaptainApplication, error)) (AdminApplication, error) {
	if err := staffMayCaptain(st, permCaptainDecide); err != nil {
		return AdminApplication{}, err
	}
	var out AdminApplication
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, u, err := a.visibleApplication(ctx, tx, st, permCaptainDecide, appID, true)
		if err != nil {
			return err
		}
		a.firstLock(opDecideCaptainApplication)
		linked, version, err := a.approverLink(ctx, tx, st)
		if err != nil {
			return err
		}
		if linked == r.UserID {
			return errSelfAction
		}
		// роль, города, выключение и сброс сотрудника поднимают версию его учётки: изменился между загрузкой сессии и
		// замком — права и скоуп снимка (st.Grants, st.CityIDs) устарели, решение — 403 (m1 ревью Task 4). Учётка под
		// FOR SHARE до коммита: изменить её, пока решение идёт, нельзя.
		if version != st.Version {
			return errStaffForbidden
		}
		if string(r.Status) != applicationPending || u.Status == identitydb.UserStatusPendingDeletion {
			return errApplicationClosed
		}
		now := a.clock.Now()
		got, err := apply(ctx, tx, r, u, now)
		if err != nil {
			return err
		}
		out = adminApplicationView(got, u.Nickname, u.BirthDate, u.CreatedAt, now, tzPtr(u.CityTimezone))
		return nil
	})
	if err != nil {
		return AdminApplication{}, err
	}
	return out, nil
}

// auditDecision — запись решения сотрудника: объект — заявка, город записи — город заявки; after — только статус,
// версия и код причины (reason_code: слово code маскируется, reason_code — справочник); комментарий — нет.
func auditDecision(ctx context.Context, st *auth.Staff, action string, r identitydb.CaptainApplication) error {
	after := map[string]any{"status": string(r.Status), "version": r.Version, "user_id": r.UserID}
	if r.RejectionCode != nil {
		after["reason_code"] = *r.RejectionCode
	}
	m := approvals.MetaFrom(ctx)
	return audit.Write(ctx, audit.Entry{ActorStaffID: st.StaffID, ActorRole: st.Role, ActorLabel: st.Label,
		Action: action, ObjectType: ObjectCaptainApplication, ObjectID: r.ID, ScopeCityID: r.CityID,
		Before: map[string]any{"status": applicationPending}, After: after, IP: m.IP, UserAgent: m.UserAgent})
}

// ApproveCaptainApplication — approveCaptainApplication (одна db.InTx под Idempotency-Key): decision → правило
// условий по городу заявки (любая причина — 409 captain_ineligible {reasons}, в том числе checks_unavailable при
// прод-портах) → допуск role_assignments с заявкой → заявка approved, версия + 1 → события decided и permit_granted
// (одна версия агрегата) → аудит → письмо игроку.
func (a *App) ApproveCaptainApplication(ctx context.Context, st *auth.Staff, appID uuid.UUID, comment *string) (AdminApplication, error) {
	if blank, err := checkDecisionComment(comment); err != nil {
		return AdminApplication{}, err
	} else if blank && comment != nil {
		return AdminApplication{}, httpx.NewFieldError("body.comment", ruleMinLength)
	}
	return a.decision(ctx, st, appID, func(ctx context.Context, tx pgx.Tx, r identitydb.CaptainApplication,
		_ identitydb.AdminCaptainApplicantRow, now time.Time) (identitydb.CaptainApplication, error) {
		v, err := a.evaluateCaptaincy(ctx, tx, r.UserID, r.CityID, now)
		if err != nil {
			return r, err
		}
		if len(v.Reasons) > 0 {
			return r, errCaptainIneligible.WithReasons(v.Reasons...)
		}
		q := identitydb.New(tx)
		err = q.AdminGrantCaptainPermit(ctx, identitydb.AdminGrantCaptainPermitParams{ID: id.New(), UserID: r.UserID,
			CityID: r.CityID, StaffID: st.StaffID, ApplicationID: r.ID, Now: now})
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == pgUniqueViolation &&
			pe.ConstraintName == roleAssignmentsUnique {
			return r, errCaptainIneligible.WithReasons(domain.ReasonAlreadyPermitted)
		}
		if err != nil {
			return r, fmt.Errorf("identity: допуск капитана: %w", err)
		}
		got, err := q.AdminApproveCaptainApplication(ctx, identitydb.AdminApproveCaptainApplicationParams{Now: &now,
			StaffID: st.StaffID, Label: st.Label, Comment: comment, ID: r.ID})
		if err != nil {
			return r, fmt.Errorf("identity: одобрение заявки: %w", err)
		}
		ref := identity.ApplicationRef{ID: got.ID, Version: got.Version}
		for _, ev := range []events.Event{
			identity.CaptainApplicationDecided(ref, got.UserID, got.CityID, applicationApproved, ""),
			identity.CaptainPermitGranted(ref, got.UserID, got.CityID),
		} {
			if _, err := events.Publish(ctx, ev); err != nil {
				return r, err
			}
		}
		if err := auditDecision(ctx, st, ActionCaptainApplicationApproved, got); err != nil {
			return r, err
		}
		return got, a.enqueueMail(ctx, tx, MailCaptainApplicationApproved, got.ID)
	})
}

// RejectCaptainApplication — rejectCaptainApplication: код из domain.RejectionCodes (иначе 400 enum — валидатор
// отсекает раньше); other без видимого комментария — 422 body.comment required. Та же decision, условия не
// проверяются; заявка rejected, событие decided с кодом, аудит, письмо игроку. Пауза 7 дней — от reviewed_at (R5).
func (a *App) RejectCaptainApplication(ctx context.Context, st *auth.Staff, appID uuid.UUID, code string, comment *string) (AdminApplication, error) {
	if !slices.Contains(domain.RejectionCodes, code) {
		return AdminApplication{}, httpx.NewFieldError("body.rejection_code", "enum")
	}
	blank, err := checkDecisionComment(comment)
	switch {
	case err != nil:
		return AdminApplication{}, err
	case blank && code == rejectionOther:
		return AdminApplication{}, fieldProblem{status: http.StatusUnprocessableEntity, field: "body.comment", rule: "required"}
	case blank && comment != nil:
		return AdminApplication{}, httpx.NewFieldError("body.comment", ruleMinLength)
	}
	return a.decision(ctx, st, appID, func(ctx context.Context, tx pgx.Tx, r identitydb.CaptainApplication,
		_ identitydb.AdminCaptainApplicantRow, now time.Time) (identitydb.CaptainApplication, error) {
		got, err := identitydb.New(tx).AdminRejectCaptainApplication(ctx, identitydb.AdminRejectCaptainApplicationParams{
			Now: &now, StaffID: st.StaffID, Label: st.Label, Code: code, Comment: comment, ID: r.ID})
		if err != nil {
			return r, fmt.Errorf("identity: отказ по заявке: %w", err)
		}
		ref := identity.ApplicationRef{ID: got.ID, Version: got.Version}
		if _, err := events.Publish(ctx, identity.CaptainApplicationDecided(ref, got.UserID, got.CityID,
			applicationRejected, code)); err != nil {
			return r, err
		}
		if err := auditDecision(ctx, st, ActionCaptainApplicationRejected, got); err != nil {
			return r, err
		}
		return got, a.enqueueMail(ctx, tx, MailCaptainApplicationRejected, got.ID)
	})
}
