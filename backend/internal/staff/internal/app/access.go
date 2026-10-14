package app

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Поля тела запроса и правила для 400 validation.failed (контракт: changeStaffPassword,
// updateStaffMeLocale). Правила пароля — коды политики platform/password.
const (
	fieldNewPassword = "body.new_password"
	fieldCode        = "body.code"
	fieldLocale      = "body.locale"
	ruleTooShort     = "too_short"
	ruleTooLong      = "too_long"
	ruleCommon       = "common"
	ruleRequired     = "required" // ни кода, ни резервного кода
	ruleOneOf        = "oneOf"    // и код, и резервный код
	ruleEnum         = "enum"
)

// Me — «кто вошёл» (GET /v1/staff/me, R15, P20), в любой стадии и под Reauth (ReauthRequired). В
// mfa_pending — только стадия и сессия: учётки нет, города и права — пустые (не nil); в setup —
// учётка без прав; права — только в active (копия Grants сессии). Города — только у модератора.
func (a *App) Me(ctx context.Context, s *auth.Staff) (staff.MeView, error) {
	row, err := staffdb.New(a.pool).GetMe(ctx, staffdb.GetMeParams{SessionID: s.SessionID, StaffID: s.StaffID})
	if errors.Is(err, pgx.ErrNoRows) {
		return staff.MeView{}, auth.ErrSessionInvalid
	}
	if err != nil {
		return staff.MeView{}, err
	}
	v := staff.MeView{
		CityIDs: []uuid.UUID{}, Stage: s.Stage, ReauthRequired: s.Reauth,
		Permissions: map[string]auth.Grant{},
		Session:     &staff.MeSession{ID: s.SessionID, ExpiresAt: row.ExpiresAt},
	}
	if s.Stage == auth.StageMFAPending {
		return v, nil
	}
	v.Account = &staff.MeAccount{
		ID: s.StaffID, Email: row.Email, DisplayName: row.DisplayName, Locale: row.Locale, Role: s.Role,
	}
	if s.Role == auth.RoleModerator {
		v.CityIDs = append(v.CityIDs, s.CityIDs...)
	}
	if s.Stage == auth.StageActive {
		maps.Copy(v.Permissions, s.Grants)
	}
	return v, nil
}

// MySessions — свои живые сессии («Мой доступ»), текущая отмечена.
func (a *App) MySessions(ctx context.Context, s *auth.Staff) ([]staff.SessionView, error) {
	return a.sessionViews(ctx, s.StaffID, s.SessionID)
}

// sessionViews — живые сессии учётки staffID (не погашены, срок не истёк), новые сверху; current —
// сессия смотрящего (uuid.Nil — ни одна не отмечается: чужие сессии в карточке, 8b). IP — подсеть
// /24 или /48 (audit.MaskIP); пустой User-Agent в базе — nil. Устройство и браузер из User-Agent
// разбирает админка (контракт StaffSession).
func (a *App) sessionViews(ctx context.Context, staffID, current uuid.UUID) ([]staff.SessionView, error) {
	rows, err := staffdb.New(a.pool).ListLiveSessions(ctx, staffdb.ListLiveSessionsParams{StaffID: staffID, Now: a.clock.Now()})
	if err != nil {
		return nil, err
	}
	out := make([]staff.SessionView, 0, len(rows))
	for _, r := range rows {
		v := staff.SessionView{
			ID: r.ID, Stage: r.Stage, Current: current != uuid.Nil && r.ID == current,
			CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, ExpiresAt: r.ExpiresAt, IP: audit.MaskIP(r.Ip),
		}
		if r.UserAgent != "" {
			ua := r.UserAgent
			v.UserAgent = &ua
		}
		out = append(out, v)
	}
	return out, nil
}

// RevokeMySession — завершить одну свою живую сессию (revoked). Чужая, несуществующая, уже
// погашенная или истёкшая — staff.session_not_found без различия. Текущую тоже можно (контракт
// revokeStaffMySession: «тогда следующий запрос ответит 401») — это выход с этого устройства.
func (a *App) RevokeMySession(ctx context.Context, s *auth.Staff, sessionID uuid.UUID, m staff.Meta) error {
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := a.lockSelf(ctx, q, s)
		if err != nil {
			return err
		}
		n, err := q.RevokeOwnSession(ctx, staffdb.RevokeOwnSessionParams{
			Now: a.clock.Now(), Reason: reasonRevoked, ID: sessionID, StaffID: s.StaffID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return staff.ErrSessionNotFound
		}
		return audit.Write(ctx, selfEntry(acct, actionSessionRevoked, nil,
			map[string]any{"session_id": sessionID, "current": sessionID == s.SessionID}, m))
	})
}

// RevokeOtherSessions — завершить все свои живые сессии, кроме текущей (revoked). Возвращает,
// сколько завершено; 0 — без записи в аудит.
func (a *App) RevokeOtherSessions(ctx context.Context, s *auth.Staff, m staff.Meta) (int, error) {
	var n int64
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := a.lockSelf(ctx, q, s)
		if err != nil {
			return err
		}
		if n, err = q.RevokeOtherSessions(ctx, staffdb.RevokeOtherSessionsParams{
			Now: a.clock.Now(), Reason: reasonRevoked, StaffID: s.StaffID, Keep: s.SessionID,
		}); err != nil || n == 0 {
			return err
		}
		return audit.Write(ctx, selfEntry(acct, actionSessionsRevoked, nil,
			map[string]any{"count": n, "kept_session_id": s.SessionID}, m))
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ChangePassword — смена своего пароля (§4.7, D7, M9): текущий пароль, второй фактор (ровно одно из
// code и recovery) и новый пароль. До транзакции — выбор фактора (body.code) и политика нового
// пароля (body.new_password: too_short | too_long | common), затем хэш нового: ошибки поля неудачей
// не считаются. Проверка — через withFactor под замком учётки: сессия (погашена, заменена, учётка
// закрыта — отказ без записи неудачи), текущий пароль (неверный — staff.password_invalid; второй
// фактор тогда не проверяется и резервный код не сгорает), второй фактор (неверный —
// staff.mfa_invalid). Обе неудачи — в счётчик второго фактора с attempts_left, пятая подряд
// закрывает учётку. Успех — новый хэш, счётчик 0, резервный код сгорел, другие живые сессии
// погашены (password_changed), текущая остаётся; аудит staff.password_changed. Новый пароль,
// совпадающий с текущим, допустим: контракт кода для него не знает, а смена всё равно гасит
// другие сессии.
func (a *App) ChangePassword(ctx context.Context, s *auth.Staff, current, code, recovery, next string, m staff.Meta) error {
	switch {
	case code == "" && recovery == "":
		return httpx.NewFieldError(fieldCode, ruleRequired)
	case code != "" && recovery != "":
		return httpx.NewFieldError(fieldCode, ruleOneOf)
	}
	if err := password.Check(next); err != nil {
		return passwordPolicyError(fieldNewPassword, err)
	}
	hash, err := a.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	err = a.withFactor(ctx, s, stepPasswordChange, m, func(ctx context.Context, _ pgx.Tx, q *staffdb.Queries, acct staffdb.LockAccountRow, now time.Time) (error, error) {
		sess, err := lockLiveSession(ctx, q, s, acct, now)
		if err != nil {
			return nil, err
		}
		if sess.Stage != auth.StageActive {
			return nil, staff.ErrWrongStage
		}
		ok, err := a.verifyCurrent(ctx, current, acct.PasswordHash)
		if err != nil {
			return nil, err
		}
		if !ok {
			return staff.ErrPasswordInvalid, nil
		}
		if ok, err = a.checkFactor(ctx, q, s.StaffID, code, recovery, now); err != nil {
			return nil, err
		}
		if !ok {
			return staff.ErrMFAInvalid, nil
		}
		if err := q.SetPassword(ctx, staffdb.SetPasswordParams{PasswordHash: &hash, ID: s.StaffID}); err != nil {
			return nil, err
		}
		if err := q.FactorSucceeded(ctx, staffdb.FactorSucceededParams{ID: s.StaffID}); err != nil {
			return nil, err
		}
		revoked, err := q.RevokeOtherSessions(ctx, staffdb.RevokeOtherSessionsParams{
			Now: now, Reason: reasonPasswordChanged, StaffID: s.StaffID, Keep: s.SessionID,
		})
		if err != nil {
			return nil, err
		}
		return nil, audit.Write(ctx, selfEntry(acct, actionPasswordChanged, nil,
			map[string]any{"method": factorMethod(code), "sessions_revoked": revoked}, m))
	})
	// токен сессии только что заменён (unlock в другой вкладке): 401 без стирания новой cookie
	return replacedAsSession(err)
}

// verifyCurrent — подходит ли текущий пароль к хэшу учётки. Нет хэша или пароль длиннее потолка
// хэшера — просто «не подходит» (неудача), а не ошибка.
func (a *App) verifyCurrent(ctx context.Context, pw string, hash *string) (bool, error) {
	if hash == nil {
		return false, nil
	}
	ok, _, err := a.hasher.Verify(ctx, pw, *hash)
	if errors.Is(err, password.ErrTooLong) {
		return false, nil
	}
	return ok, err
}

// SetLocale — язык админки и писем сотруднику (ru | en — i18n.Supported, как CHECK
// staff_accounts_locale_check). Иное — 400 validation.failed по body.locale. Тот же язык — без
// изменений и без записи в аудит; смена — аудит staff.locale_changed (было/стало).
func (a *App) SetLocale(ctx context.Context, s *auth.Staff, locale string, m staff.Meta) error {
	if !slices.Contains(i18n.Supported, locale) {
		return httpx.NewFieldError(fieldLocale, ruleEnum)
	}
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := a.lockSelf(ctx, q, s)
		if err != nil || acct.Locale == locale {
			return err
		}
		if err := q.SetLocale(ctx, staffdb.SetLocaleParams{Locale: locale, ID: s.StaffID}); err != nil {
			return err
		}
		return audit.Write(ctx, selfEntry(acct, actionLocaleChanged,
			map[string]any{"locale": acct.Locale}, map[string]any{"locale": locale}, m))
	})
}

// lockSelf — своя учётка, затем сессия запроса под замком (порядок: учётка → сессия) с перепроверкой
// сессии (lockLiveSession): между загрузкой в конвейере и замком её могли погасить, заменить
// (unlock в другой вкладке) или закрыть учётку. Учётки нет, сессия погашена, истекла, учётка
// закрыта — auth.ErrSessionInvalid; заменена — auth.ErrSessionReplaced (401 без стирания новой
// cookie): иначе «завершить остальные» по старой сессии погасил бы свежую. Учётка — роль и снимок
// имени для аудита.
func (a *App) lockSelf(ctx context.Context, q *staffdb.Queries, s *auth.Staff) (staffdb.LockAccountRow, error) {
	acct, err := q.LockAccount(ctx, s.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return acct, auth.ErrSessionInvalid
	}
	if err != nil {
		return acct, err
	}
	// время — после замка: параллельный запрос мог держать учётку
	_, err = lockLiveSession(ctx, q, s, acct, a.clock.Now())
	return acct, replacedAsSession(err)
}

// replacedAsSession — errReplaced (сессию запроса только что заменили) как auth.ErrSessionReplaced.
// Окно replacedGrace здесь осознанно не проверяется: lockLiveSession видит замену, случившуюся
// между загрузкой в конвейере и замком, то есть только что — старше минуты она быть не может
// (конвейер такую сессию уже не загрузил бы).
func replacedAsSession(err error) error {
	if errors.Is(err, errReplaced) {
		return auth.ErrSessionReplaced
	}
	return err
}

// selfEntry — запись аудита действия сотрудника над своей учёткой: автор и объект — учётка acct.
func selfEntry(acct staffdb.LockAccountRow, action string, before, after map[string]any, m staff.Meta) audit.Entry {
	e := audit.Entry{
		ActorStaffID: acct.ID, ActorRole: acct.Role, ActorLabel: label(acct.DisplayName),
		Action: action, ObjectType: objectAccount, ObjectID: acct.ID,
		After: after, IP: m.IP, UserAgent: m.UserAgent,
	}
	if before != nil {
		e.Before = before
	}
	return e
}

// passwordPolicyError — нарушение политики пароля (password.Check) как 400 validation.failed по
// полю field с кодом too_short | too_long | common; иная ошибка — как есть.
func passwordPolicyError(field string, err error) error {
	switch {
	case errors.Is(err, password.ErrTooShort):
		return httpx.NewFieldError(field, ruleTooShort)
	case errors.Is(err, password.ErrTooLong):
		return httpx.NewFieldError(field, ruleTooLong)
	case errors.Is(err, password.ErrCommon):
		return httpx.NewFieldError(field, ruleCommon)
	}
	return err
}
