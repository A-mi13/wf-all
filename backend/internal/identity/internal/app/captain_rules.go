package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
)

// Виды писем I-4 (план I-4, Task 2): ref — captain_applications.id. В MailKinds() и Composer их добавляют сценарии —
// одобрение и отказ (Task 4, страж TestComposersCoverKinds), отзыв сменой города (Task 7, R39).
const (
	MailCaptainApplicationApproved      = "identity.captain_application_approved"
	MailCaptainApplicationRejected      = "identity.captain_application_rejected"
	MailCaptainApplicationWithdrawnCity = "identity.captain_application_withdrawn_city"
)

// Ключи Checks вердикта (галочки карточки заявки) и их значения.
const (
	checkAge          = "age"
	checkRestrictions = "restrictions"
	checkProfile      = "profile"
	checkCaptaincy    = "captaincy"
	checkOK           = "ok"
	checkFailed       = "failed"
	checkUnavailable  = "unavailable"
)

// checkKeys — ключ галочки по имени порта.
var checkKeys = map[string]string{
	identity.CheckProfileComplete: checkProfile,
	identity.CheckCityCaptaincy:   checkCaptaincy,
}

// captaincyVerdict — итог правила условий (R5): Reasons — причины в domain.ReasonOrder (пусто — условия выполнены);
// Checks — age|restrictions|profile|captaincy → ok|failed|unavailable; AvailableAt — конец паузы после последнего
// отказа (nil — паузы нет); OpenApplicationID — открытая заявка в городе (uuid.Nil — нет); Permitted — допуск в
// городе уже есть (тогда в Reasons — already_permitted).
type captaincyVerdict struct {
	Reasons           []string
	Checks            map[string]string
	AvailableAt       *time.Time
	OpenApplicationID uuid.UUID
	Permitted         bool
}

// Eligible — подать можно сейчас: нет причин, паузы и открытой заявки (открытая заявка — eligible = false без
// причины, клиент показывает заявку; R5).
func (v captaincyVerdict) Eligible() bool {
	return len(v.Reasons) == 0 && v.AvailableAt == nil && v.OpenApplicationID == uuid.Nil
}

// evaluateCaptaincy — одно правило условий капитанства для getMyCaptaincy, подачи, карточки и одобрения (R5): возраст
// по поясу города на «сегодня» (domain.AgeYears; города нет или страна выключена — checks_unavailable), ограничение
// captain, suspended (и в периоде удаления — status_before_deletion) и «заблокирован» (R36) → restricted, порты по именам, живой допуск в городе → already_permitted,
// пауза — последний отказ игроку в любом городе (Q4), открытая заявка в городе. Без записи и без замков; все чтения —
// на tx вызывающего, порты — тоже в ней.
func (a *App) evaluateCaptaincy(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID, now time.Time) (captaincyVerdict, error) {
	q := identitydb.New(tx)
	v := captaincyVerdict{Checks: map[string]string{}}
	reasons := map[string]bool{}
	u, err := q.CaptaincyUser(ctx, userID)
	if err != nil {
		return v, fmt.Errorf("identity: правило капитанства — аккаунт: %w", err)
	}

	// возраст по поясу города
	v.Checks[checkAge] = checkUnavailable
	city, err := q.CaptaincyCity(ctx, cityID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return v, fmt.Errorf("identity: правило капитанства — город: %w", err)
	case city.CountryEnabled && u.BirthDate.Valid:
		tz, err := time.LoadLocation(city.Timezone)
		if err != nil || city.Timezone == "" {
			break // битый пояс — честное «проверка недоступна», не UTC молча
		}
		age, err := domain.AgeYears(u.BirthDate.Time, now, tz)
		if err != nil {
			return v, err
		}
		v.Checks[checkAge] = checkOK
		if age < int(city.MinSignupAge) {
			v.Checks[checkAge] = checkFailed
			reasons[domain.ReasonTooYoung] = true
		}
	}
	if v.Checks[checkAge] == checkUnavailable {
		reasons[domain.ReasonChecksUnavailable] = true
	}

	// ограничения и статус
	restricted, err := q.CaptainRestricted(ctx, identitydb.CaptainRestrictedParams{UserID: userID, Now: now})
	if err != nil {
		return v, fmt.Errorf("identity: правило капитанства — ограничения: %w", err)
	}
	blocked, err := a.blocked(ctx, q, u, now)
	if err != nil {
		return v, err
	}
	v.Checks[checkRestrictions] = checkOK
	// suspended — и в периоде удаления: статус до удаления хранится в status_before_deletion (0026; m3 ревью W1)
	suspended := u.Status == identitydb.UserStatusSuspended || (u.Status == identitydb.UserStatusPendingDeletion &&
		u.StatusBeforeDeletion != nil && *u.StatusBeforeDeletion == identitydb.UserStatusSuspended)
	if restricted || blocked || suspended {
		v.Checks[checkRestrictions] = checkFailed
		reasons[domain.ReasonRestricted] = true
	}

	// порты модулей выше
	for _, c := range a.captainChecks {
		key := checkKeys[c.Name()]
		reason, ok, err := c.Check(ctx, tx, userID, cityID)
		switch {
		case err != nil:
			return v, fmt.Errorf("identity: порт %s: %w", c.Name(), err)
		case ok:
			v.Checks[key] = checkOK
		case reason == "":
			return v, fmt.Errorf("identity: порт %s: отказ без причины", c.Name())
		case reason == domain.ReasonChecksUnavailable:
			v.Checks[key] = checkUnavailable
			reasons[reason] = true
		default:
			v.Checks[key] = checkFailed
			reasons[reason] = true
		}
	}

	// допуск, пауза, открытая заявка
	if v.Permitted, err = q.CaptainPermitted(ctx, identitydb.CaptainPermittedParams{UserID: userID, CityID: cityID}); err != nil {
		return v, fmt.Errorf("identity: правило капитанства — допуск: %w", err)
	}
	if v.Permitted {
		reasons[domain.ReasonAlreadyPermitted] = true
	}
	last, err := q.LastCaptainRejection(ctx, userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return v, fmt.Errorf("identity: правило капитанства — пауза: %w", err)
	case last != nil:
		if until := domain.CooldownUntil(*last); now.Before(until) {
			until = until.UTC()
			v.AvailableAt = &until
		}
	}
	open, err := q.OpenCaptainApplication(ctx, identitydb.OpenCaptainApplicationParams{UserID: userID, CityID: cityID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return v, fmt.Errorf("identity: правило капитанства — открытая заявка: %w", err)
	default:
		v.OpenApplicationID = open
	}
	v.Reasons = orderReasons(reasons)
	return v, nil
}

// orderReasons — причины в domain.ReasonOrder, незнакомые (новые значения портов) — после, по алфавиту; пусто — nil.
func orderReasons(set map[string]bool) []string {
	var out, rest []string
	for _, r := range domain.ReasonOrder {
		if set[r] {
			out = append(out, r)
		}
	}
	for r := range set {
		if !slices.Contains(domain.ReasonOrder, r) {
			rest = append(rest, r)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// blocked — «заблокирован» (R36): одно правило — запрос UserBlocked (banned или период удаления поверх живого бана);
// удалённый — false без запроса. Зовут evaluateCaptaincy и сценарии входа, отмены удаления, сброса пароля (Task 7);
// Composer писем — тот же запрос через identitydb.
func (a *App) blocked(ctx context.Context, q *identitydb.Queries, u userRow, now time.Time) (bool, error) {
	if u.Status == identitydb.UserStatusDeleted {
		return false, nil
	}
	b, err := q.UserBlocked(ctx, identitydb.UserBlockedParams{ID: u.ID, Now: now})
	if err != nil {
		return false, fmt.Errorf("identity: заблокирован ли аккаунт: %w", err)
	}
	return b, nil
}
