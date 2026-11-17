package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/page"
)

// Капитанство игрока (спека §3.12 п. 1–5, 7; план I-4, Task 3): экран «Стать капитаном», история своих заявок, подача
// и отзыв. Условия — одно правило evaluateCaptaincy (R5, captain_rules.go). Все чтения и записи привязаны к игроку
// запроса: чужая заявка неотличима от несуществующей. Порядок замков (R7): users → captain_applications.

var (
	errCaptainIneligible   = httpx.NewError(http.StatusConflict, identity.CodeCaptainIneligible)
	errApplicationExists   = httpx.NewError(http.StatusConflict, identity.CodeApplicationExists)
	errApplicationCooldown = httpx.NewError(http.StatusConflict, identity.CodeApplicationCooldown)
	errApplicationNotFound = httpx.NewError(http.StatusNotFound, identity.CodeApplicationNotFound)
	errApplicationClosed   = httpx.NewError(http.StatusConflict, identity.CodeApplicationClosed)
)

// opSubmitCaptainApplication — метка hooks.afterFirstLock подачи (после замка аккаунта и правила условий: барьер
// гонки двух подач видит обе на одной точке, только если замок не взят).
const opSubmitCaptainApplication = "submit_captain_application"

// captainApplicationsOpenKey — уникальный индекс «одна открытая заявка в городе» (0003): 23505 по нему — та же
// открытая заявка (запасной путь, подачу сериализует lockUser).
const captainApplicationsOpenKey = "captain_applications_open_key"

// captainApplicationsSet — набор полей курсора истории заявок (page.Keyset).
const captainApplicationsSet = "identity.my_captain_applications(created_at,id)"

// Статусы заявки (enum application_status).
const (
	applicationPending   = "pending"
	applicationWithdrawn = "withdrawn"
)

// Captaincy — экран «Стать капитаном»: допуски по городам, можно ли подать в текущем городе (CityID; Reasons — в
// domain.ReasonOrder, пусто — не nil; AvailableAt — конец паузы после отказа), открытая заявка (nil — нет).
type Captaincy struct {
	Permits         []CaptainPermit
	CityID          uuid.UUID
	Eligible        bool
	Reasons         []string
	AvailableAt     *time.Time
	OpenApplication *CaptainApplication
}

// CaptainPermit — живой допуск капитана в городе (Probationary — испытательный срок заявки допуска).
type CaptainPermit struct {
	CityID       uuid.UUID
	GrantedAt    time.Time
	Probationary bool
}

// CaptainApplication — заявка, как её видит заявитель: телефон не возвращается (HasPhone), DecidedAt — время решения
// или отзыва (coalesce(reviewed_at, closed_at)).
type CaptainApplication struct {
	ID                                                        uuid.UUID
	CityID                                                    uuid.UUID
	Status                                                    string
	ProposedFormat, ProposedPitch, PlayFrequency, ContactLink *string
	Note, RejectionCode, Comment                              *string
	HasPhone                                                  bool
	CreatedAt                                                 time.Time
	DecidedAt                                                 *time.Time
}

// CaptainApplicationInput — форма заявки (все поля необязательны; длины и форматы проверил контракт, CHECK таблицы —
// защита в глубину).
type CaptainApplicationInput struct {
	ProposedFormat, ProposedPitch, PlayFrequency, ContactLink, Note, Phone *string
}

// Правила полей формы для 400 validation.failed (как у валидатора контракта).
const (
	rulePattern   = "pattern"
	ruleMinLength = "minLength"
)

// checkApplicationText — свободные поля формы сверх контракта (I1, m3 ревью Task 3): управляющие символы (Cc: NUL —
// 22021 в text, был бы 500; ESC, DEL, C1) — 400 pattern по полю; перевод строки и табуляция — только в note
// (многострочный текст игрока). В contact_link — ещё невидимые (emailaddr.IsInvisible: Cf, в том числе bidi U+202E и
// ZWSP) и пробельные: видимая ссылка не должна расходиться с адресом перехода. Поле из одних пробелов и невидимых —
// 400 minLength (контракт: minLength 1 — передал поле, значит не пустое). Значения не меняются.
func checkApplicationText(in CaptainApplicationInput) error {
	fields := []struct {
		name      string
		v         *string
		multiline bool
	}{
		{"body.proposed_format", in.ProposedFormat, false}, {"body.proposed_pitch", in.ProposedPitch, false},
		{"body.play_frequency", in.PlayFrequency, false}, {"body.note", in.Note, true},
	}
	for _, f := range fields {
		if f.v == nil {
			continue
		}
		visible := false
		for _, r := range *f.v {
			if unicode.IsControl(r) && (!f.multiline || r != '\n' && r != '\t') {
				return httpx.NewFieldError(f.name, rulePattern)
			}
			if !unicode.IsSpace(r) && !emailaddr.IsInvisible(r) {
				visible = true
			}
		}
		if !visible {
			return httpx.NewFieldError(f.name, ruleMinLength)
		}
	}
	if in.ContactLink != nil && strings.ContainsFunc(*in.ContactLink, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || emailaddr.IsInvisible(r)
	}) {
		return httpx.NewFieldError("body.contact_link", rulePattern)
	}
	return nil
}

func applicationView(r identitydb.CaptainApplication) CaptainApplication {
	v := CaptainApplication{ID: r.ID, CityID: r.CityID, Status: string(r.Status), ProposedFormat: r.ProposedFormat,
		ProposedPitch: r.ProposedPitch, PlayFrequency: r.PlayFrequency, ContactLink: r.ContactLink, Note: r.Note,
		RejectionCode: r.RejectionCode, Comment: r.Comment, HasPhone: r.Phone != nil, CreatedAt: r.CreatedAt.UTC()}
	// время — в UTC (контракт): pgx отдаёт timestamptz в поясе процесса
	if d := cmp.Or(r.ReviewedAt, r.ClosedAt); d != nil {
		u := d.UTC()
		v.DecidedAt = &u
	}
	return v
}

// cityOf — город заявки: текущий город игрока; NULL (у живого аккаунта не бывает) — uuid.Nil: правило не найдёт
// настроек города и ответит checks_unavailable.
func cityOf(c *uuid.UUID) uuid.UUID {
	if c == nil {
		return uuid.Nil
	}
	return *c
}

// MyCaptaincy — getMyCaptaincy: читающая транзакция (порты условий зовутся в tx). Аккаунта нет — 401 (errSessionGone).
func (a *App) MyCaptaincy(ctx context.Context, userID uuid.UUID) (Captaincy, error) {
	var out Captaincy
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		out = Captaincy{Permits: []CaptainPermit{}, Reasons: []string{}}
		q := identitydb.New(tx)
		city, err := q.CaptaincyUserCity(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errSessionGone
		}
		if err != nil {
			return fmt.Errorf("identity: город игрока: %w", err)
		}
		permits, err := q.CaptaincyPermits(ctx, userID)
		if err != nil {
			return fmt.Errorf("identity: допуски капитана: %w", err)
		}
		for _, p := range permits {
			out.Permits = append(out.Permits, CaptainPermit{CityID: cityOf(p.CityID), GrantedAt: p.GrantedAt.UTC(),
				Probationary: p.IsProbationary})
		}
		out.CityID = cityOf(city)
		v, err := a.evaluateCaptaincy(ctx, tx, userID, out.CityID, a.clock.Now())
		if err != nil {
			return err
		}
		out.Eligible, out.AvailableAt = v.Eligible(), v.AvailableAt
		out.Reasons = append(out.Reasons, v.Reasons...)
		if v.OpenApplicationID != uuid.Nil {
			r, err := q.MyCaptainApplication(ctx, identitydb.MyCaptainApplicationParams{ID: v.OpenApplicationID, UserID: userID})
			if err != nil {
				return fmt.Errorf("identity: открытая заявка: %w", err)
			}
			open := applicationView(r)
			out.OpenApplication = &open
		}
		return nil
	})
	return out, err
}

// ListMyCaptainApplications — история своих заявок, новые сверху: keyset (created_at, id) DESC; испорченный курсор —
// page.ErrBadCursor (400 по query.cursor). next — курсор следующей страницы ("" — страниц больше нет).
func (a *App) ListMyCaptainApplications(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]CaptainApplication, string, error) {
	arg := identitydb.ListMyCaptainApplicationsParams{UserID: userID, Lim: int32(limit) + 1} //nolint:gosec // G115: limit ≤ page.MaxLimit
	if cursor != "" {
		k, err := page.DecodeKeyset(cursor, captainApplicationsSet, page.KindTime, page.KindUUID)
		if err != nil {
			return nil, "", err
		}
		arg.HasCursor, arg.CursorAt, arg.CursorID = true, k.Values[0].(time.Time), k.Values[1].(uuid.UUID)
	}
	rows, err := identitydb.New(a.pool).ListMyCaptainApplications(ctx, arg)
	if err != nil {
		return nil, "", fmt.Errorf("identity: история заявок: %w", err)
	}
	var next string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = page.EncodeKeyset(page.Keyset{Set: captainApplicationsSet, Values: []any{last.CreatedAt, last.ID}},
			page.KindTime, page.KindUUID)
	}
	out := make([]CaptainApplication, 0, len(rows))
	for _, r := range rows {
		out = append(out, applicationView(r))
	}
	return out, next, nil
}

// SubmitCaptainApplication — подача (одна db.InTx под Idempotency-Key): lockUser (аккаунт не живой — 401); город —
// users.city_id; правило условий; первая ошибка по порядку R5: причины (409 captain_ineligible с reasons) → открытая
// заявка (409 application_exists) → пауза (409 application_cooldown с available_at). Иначе вставка, событие
// captain_application_submitted (версия 1) и аудит игрока со scope_city_id. Писем нет. Форма — checkApplicationText
// до транзакции.
func (a *App) SubmitCaptainApplication(ctx context.Context, userID uuid.UUID, in CaptainApplicationInput) (CaptainApplication, error) {
	var out CaptainApplication
	if err := checkApplicationText(in); err != nil {
		return out, err
	}
	link := in.ContactLink
	if link != nil {
		norm, ok := domain.NormalizeContactLink(*link)
		if !ok {
			return out, httpx.NewFieldError("body.contact_link", rulePattern)
		}
		link = &norm
	}
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := a.lockUser(ctx, tx, userID)
		if errors.Is(err, errUserGone) {
			return errSessionGone
		}
		if err != nil {
			return err
		}
		// удаление прошло раньше (сессии отозваны, кэш загрузчика ещё пропустил запрос) — как мёртвая сессия
		if u.Status == identitydb.UserStatusPendingDeletion || u.Status == identitydb.UserStatusDeleted {
			return errSessionGone
		}
		city := cityOf(u.CityID)
		now := a.clock.Now()
		v, err := a.evaluateCaptaincy(ctx, tx, userID, city, now)
		if err != nil {
			return err
		}
		a.firstLock(opSubmitCaptainApplication)
		switch {
		case len(v.Reasons) > 0:
			return errCaptainIneligible.WithReasons(v.Reasons...)
		case v.OpenApplicationID != uuid.Nil:
			return errApplicationExists
		case v.AvailableAt != nil:
			return errApplicationCooldown.WithAvailableAt(*v.AvailableAt)
		}
		r, err := identitydb.New(tx).InsertCaptainApplication(ctx, identitydb.InsertCaptainApplicationParams{
			ID: id.New(), UserID: userID, CityID: city, ProposedFormat: in.ProposedFormat, ProposedPitch: in.ProposedPitch,
			PlayFrequency: in.PlayFrequency, ContactLink: link, Note: in.Note, Phone: in.Phone, Now: now})
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok && pe.Code == "23505" && pe.ConstraintName == captainApplicationsOpenKey {
			return errApplicationExists
		}
		if err != nil {
			return fmt.Errorf("identity: заявка в капитаны: %w", err)
		}
		ref := identity.ApplicationRef{ID: r.ID, Version: r.Version}
		if _, err := events.Publish(ctx, identity.CaptainApplicationSubmitted(ref, userID, city)); err != nil {
			return err
		}
		if err := a.auditApplication(ctx, ActionCaptainApplicationSubmitted, userID, r, nil,
			map[string]any{"status": applicationPending, "version": r.Version}); err != nil {
			return err
		}
		out = applicationView(r)
		return nil
	})
	if err != nil {
		return CaptainApplication{}, err
	}
	return out, nil
}

// WithdrawCaptainApplication — отзыв своей заявки (одна db.InTx): lockUser → строка заявки FOR UPDATE по id и игроку
// (R7); чужая или нет — 404 application_not_found; не pending — 409 application_closed; иначе withdrawn, closed_at,
// версия + 1, событие captain_application_withdrawn и аудит. Паузы не ставит.
func (a *App) WithdrawCaptainApplication(ctx context.Context, userID, applicationID uuid.UUID) (CaptainApplication, error) {
	var out CaptainApplication
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := a.lockUser(ctx, tx, userID); err != nil {
			if errors.Is(err, errUserGone) {
				return errSessionGone
			}
			return err
		}
		q := identitydb.New(tx)
		cur, err := q.LockMyCaptainApplication(ctx, identitydb.LockMyCaptainApplicationParams{ID: applicationID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errApplicationNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: заявка под замком: %w", err)
		}
		if string(cur.Status) != applicationPending {
			return errApplicationClosed
		}
		now := a.clock.Now()
		r, err := q.WithdrawMyCaptainApplication(ctx, identitydb.WithdrawMyCaptainApplicationParams{Now: &now, ID: cur.ID})
		if err != nil {
			return fmt.Errorf("identity: отзыв заявки: %w", err)
		}
		ref := identity.ApplicationRef{ID: r.ID, Version: r.Version}
		if _, err := events.Publish(ctx, identity.CaptainApplicationWithdrawn(ref, userID, r.CityID)); err != nil {
			return err
		}
		if err := a.auditApplication(ctx, ActionCaptainApplicationWithdrawn, userID, r,
			map[string]any{"status": applicationPending}, map[string]any{"status": applicationWithdrawn, "version": r.Version}); err != nil {
			return err
		}
		out = applicationView(r)
		return nil
	})
	if err != nil {
		return CaptainApplication{}, err
	}
	return out, nil
}

// auditApplication — действие игрока над своей заявкой: объект — заявка, город записи — город заявки (журнал
// модератора). before/after — только статус и версия: форма заявки (свободный текст, телефон, ссылка) — ПД.
func (a *App) auditApplication(ctx context.Context, action string, userID uuid.UUID, r identitydb.CaptainApplication,
	before, after map[string]any) error {
	e := audit.Entry{ActorUserID: userID, ActorRole: rolePlayer, Action: action, ObjectType: ObjectCaptainApplication,
		ObjectID: r.ID, ScopeCityID: r.CityID}
	if before != nil {
		e.Before = before
	}
	if after != nil {
		e.After = after
	}
	return audit.Write(ctx, e)
}
