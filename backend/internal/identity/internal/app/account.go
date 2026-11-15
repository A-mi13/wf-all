package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
)

// Мой аккаунт, ник, документы, мои способы входа (спека §3.16, §5.8, §6.2.4) — владелец Task 14. Сигнатуры —
// Task 8 (I9). Ответ Me собирается в той же транзакции, что и правка (одна закоммиченная db.InTx под
// Idempotency-Key); getMe и listMyAuthMethods — чтения пулом без транзакции.

// MeView — аккаунт игрока (Me).
type MeView struct {
	ID                          uuid.UUID
	Email, Nickname, Locale     string
	Status                      string
	CityID                      uuid.UUID // uuid.Nil — город удалён из справочника (users.city_id ON DELETE SET NULL)
	BirthDate                   time.Time // календарная дата (UTC, полночь)
	CreatedAt                   time.Time
	NicknameChangeableAt        *time.Time // nil — можно сейчас
	HasPassword, MfaEnabled     bool
	Restrictions                []RestrictionView
	Documents                   []DocumentView
	DocumentsAcceptanceRequired bool
}

// RestrictionView — действующее ограничение (Until nil — бессрочно).
type RestrictionView struct {
	Capability string
	Until      *time.Time
}

// DocumentView — согласие с документом: последняя принятая версия (nil — не принимался) и текущая.
type DocumentView struct {
	Document        domain.Document
	AcceptedVersion *string
	CurrentVersion  string
	URL             string
}

// MyAuthMethodsView — мои способы входа.
type MyAuthMethodsView struct {
	HasPassword bool
	Identities  []IdentityView
	Passkeys    []PasskeyView
	Mfa         MfaStatusView
}

// IdentityView — привязка Apple или Google (в I-2 их нет: таблица пуста до I-3).
type IdentityView struct {
	ID                  uuid.UUID
	Provider            string
	Email               *string
	EmailIsPrivateRelay bool
	LinkedAt            time.Time
}

// PasskeyView — ключ доступа (в I-2 их нет).
type PasskeyView struct {
	ID         uuid.UUID
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	BackedUp   bool
}

var (
	errNicknameInvalid  = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeNicknameInvalid)
	errNicknameTaken    = httpx.NewError(http.StatusConflict, identity.CodeNicknameTaken)
	errNicknameTooSoon  = httpx.NewError(http.StatusConflict, identity.CodeNicknameChangeTooSoon)
	errDocumentsOutdate = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeDocumentsOutdated)
	errLocale           = httpx.NewFieldError("locale", "enum")
)

// locales — языки аккаунта (Locale контракта).
var locales = map[string]bool{"ru": true, "en": true}

// Me — мой аккаунт (getMe): чтение пулом.
func (a *App) Me(ctx context.Context, userID uuid.UUID) (MeView, error) {
	return a.me(ctx, identitydb.New(a.pool), userID)
}

// me — Me по Queries вызывающего (пул или tx правки). Нет аккаунта — errSessionGone (сессия ссылается на аккаунт).
func (a *App) me(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) (MeView, error) {
	u, err := q.AccountUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MeView{}, errSessionGone
	}
	if err != nil {
		return MeView{}, fmt.Errorf("identity: аккаунт: %w", err)
	}
	now := a.clock.Now()
	v := MeView{ID: u.ID, Email: u.Email, Nickname: u.Nickname, Locale: u.Locale, Status: string(u.Status),
		CreatedAt: u.CreatedAt, BirthDate: u.BirthDate.Time}
	if u.CityID != nil {
		v.CityID = *u.CityID
	}
	if v.HasPassword, err = q.AccountHasPassword(ctx, userID); err != nil {
		return MeView{}, err
	}
	mfa, err := a.mfaStatus(ctx, q, userID)
	if err != nil {
		return MeView{}, err
	}
	v.MfaEnabled = mfa.TOTPEnabled
	last, err := q.AccountLastNicknameChange(ctx, userID)
	switch {
	case err == nil:
		if at := last.Add(domain.NicknameChangeEvery); at.After(now) {
			v.NicknameChangeableAt = &at
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return MeView{}, err
	}
	rs, err := q.AccountRestrictions(ctx, identitydb.AccountRestrictionsParams{UserID: userID, Now: now})
	if err != nil {
		return MeView{}, err
	}
	v.Restrictions = make([]RestrictionView, 0, len(rs))
	for _, r := range rs {
		v.Restrictions = append(v.Restrictions, RestrictionView{Capability: r.Capability, Until: r.Until})
	}
	accepted, err := a.latestConsents(ctx, q, userID)
	if err != nil {
		return MeView{}, err
	}
	for _, d := range a.legal.Items() {
		dv := DocumentView{Document: d.Document, CurrentVersion: d.Version, URL: d.URL}
		if ver, ok := accepted[d.Document]; ok {
			dv.AcceptedVersion = &ver
		}
		if dv.AcceptedVersion == nil || *dv.AcceptedVersion != d.Version {
			v.DocumentsAcceptanceRequired = true
		}
		v.Documents = append(v.Documents, dv)
	}
	return v, nil
}

// latestConsents — последняя принятая версия каждого документа (accepted_at DESC).
func (a *App) latestConsents(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) (map[domain.Document]string, error) {
	rows, err := q.AccountLatestConsents(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: согласия: %w", err)
	}
	m := make(map[domain.Document]string, len(rows))
	for _, r := range rows {
		m[domain.Document(r.Document)] = r.Version
	}
	return m, nil
}

// mfaStatus — состояние 2FA (запрос MyMfaStatus — общий с getMyMfa, Task 16): коды — только при включённой 2FA.
func (a *App) mfaStatus(ctx context.Context, q *identitydb.Queries, userID uuid.UUID) (MfaStatusView, error) {
	r, err := q.MyMfaStatus(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MfaStatusView{}, errSessionGone
	}
	if err != nil {
		return MfaStatusView{}, fmt.Errorf("identity: состояние 2FA: %w", err)
	}
	v := MfaStatusView{TOTPEnabled: r.ConfirmedAt != nil, ConfirmedAt: r.ConfirmedAt, ResetScheduledAt: r.ResetScheduledAt}
	if v.TOTPEnabled {
		v.RecoveryCodesLeft = int(r.RecoveryCodesLeft)
	}
	return v, nil
}

// UpdateMe — язык аккаунта (ru|en): version + 1 без события (язык — не событие каталога); тот же язык — без записи.
func (a *App) UpdateMe(ctx context.Context, userID uuid.UUID, locale string) (MeView, error) {
	if !locales[locale] {
		return MeView{}, errLocale
	}
	var v MeView
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := a.lockUser(ctx, tx, userID)
		if errors.Is(err, errUserGone) {
			return errSessionGone
		}
		if err != nil {
			return err
		}
		q := identitydb.New(tx)
		if u.Locale != locale {
			if _, err := q.AccountSetLocale(ctx, identitydb.AccountSetLocaleParams{Locale: locale, ID: userID}); err != nil {
				return fmt.Errorf("identity: язык: %w", err)
			}
		}
		v, err = a.me(ctx, q, userID)
		return err
	})
	return v, err
}

// ChangeNickname — смена ника (§5.8): формат (домен и normalize_text не короче 3) → 422; не чаще 30 дней → 409
// nickname_change_too_soon с available_at; резерв, чужой живой карантин, чужой аккаунт → 409 nickname_taken (гонка —
// 23505 уникального индекса); свой прежний ник из карантина — можно, строка карантина уходит. Старый ник — в
// карантин 90 дней (UPSERT), событие nickname_changed, аудит, история nickname_changes. Тот же ник — без записи.
func (a *App) ChangeNickname(ctx context.Context, userID uuid.UUID, nick string) (MeView, error) {
	nick, problem := domain.CheckNickname(nick)
	if problem != domain.NicknameOK {
		return MeView{}, errNicknameInvalid
	}
	var v MeView
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		u, err := a.lockUser(ctx, tx, userID)
		if errors.Is(err, errUserGone) {
			return errSessionGone
		}
		if err != nil {
			return err
		}
		q := identitydb.New(tx)
		if u.Nickname == nick {
			v, err = a.me(ctx, q, userID)
			return err
		}
		now := a.clock.Now()
		last, err := q.AccountLastNicknameChange(ctx, userID)
		switch {
		case err == nil:
			if at := last.Add(domain.NicknameChangeEvery); now.Before(at) {
				return errNicknameTooSoon.WithAvailableAt(at)
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		busy, err := q.AccountNicknameBusy(ctx, identitydb.AccountNicknameBusyParams{Nickname: nick, UserID: userID})
		if err != nil {
			return fmt.Errorf("identity: ник: %w", err)
		}
		if len([]rune(busy.Normalized)) < domain.NicknameMinLen {
			return errNicknameInvalid
		}
		if busy.Reserved || busy.Used {
			return errNicknameTaken
		}
		holders, err := q.AccountNicknameQuarantine(ctx, identitydb.AccountNicknameQuarantineParams{
			NicknameNormalized: busy.Normalized, Now: now})
		if err != nil {
			return err
		}
		for _, h := range holders {
			if h == nil || *h != userID {
				return errNicknameTaken
			}
		}
		// свой прежний ник (или истёкший карантин) — строка карантина больше не нужна
		if err := q.AccountDeleteNicknameReservation(ctx, busy.Normalized); err != nil {
			return err
		}
		a.firstLock("change_nickname")
		ver, err := q.AccountSetNickname(ctx, identitydb.AccountSetNicknameParams{Nickname: nick, ID: userID})
		if accountUniqueViolation(err) {
			return errNicknameTaken
		}
		if err != nil {
			return fmt.Errorf("identity: ник: %w", err)
		}
		// старый ник — в карантин, если нормализованная форма меняется (смена регистра свою форму не освобождает)
		if old := u.NicknameNormalized; old != nil && *old != busy.Normalized {
			if err := q.AccountReserveNickname(ctx, identitydb.AccountReserveNicknameParams{NicknameNormalized: *old,
				UserID: &userID, Now: now, ExpiresAt: now.Add(domain.NicknameQuarantine)}); err != nil {
				return fmt.Errorf("identity: карантин ника: %w", err)
			}
		}
		if err := q.AccountInsertNicknameChange(ctx, identitydb.AccountInsertNicknameChangeParams{ID: id.New(),
			UserID: userID, OldValue: u.Nickname, NewValue: nick, Now: now}); err != nil {
			return err
		}
		if _, err := events.Publish(ctx, identity.NicknameChanged(identity.UserRef{ID: userID, Version: ver})); err != nil {
			return err
		}
		if err := a.auditUser(ctx, ActionNicknameChanged, userID, nil, nil); err != nil {
			return err
		}
		v, err = a.me(ctx, q, userID)
		return err
	})
	return v, err
}

// accountUniqueViolation — 23505.
func accountUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// AcceptDocuments — согласие с текущими документами (§3.16): набор «на каждый документ — присланная версия, иначе
// последняя принятая» проходит Legal.Check, иначе 422 documents_outdated без записей; consents — история версий
// (повтор той же версии обновляет время), аудит documents_accepted с версиями.
func (a *App) AcceptDocuments(ctx context.Context, userID uuid.UUID, docs []domain.Accepted, kind domain.ClientKind) (MeView, error) {
	sent := make(map[domain.Document]string, len(docs))
	for _, d := range docs {
		if _, dup := sent[d.Document]; dup {
			return MeView{}, errDocumentsOutdate
		}
		sent[d.Document] = d.Version
	}
	var v MeView
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := a.lockUser(ctx, tx, userID); errors.Is(err, errUserGone) {
			return errSessionGone
		} else if err != nil {
			return err
		}
		q := identitydb.New(tx)
		prev, err := a.latestConsents(ctx, q, userID)
		if err != nil {
			return err
		}
		merged := make([]domain.Accepted, 0, len(a.legal.Items()))
		for doc, ver := range prev {
			if _, ok := sent[doc]; !ok {
				merged = append(merged, domain.Accepted{Document: doc, Version: ver})
			}
		}
		merged = append(merged, docs...)
		if err := a.legal.Check(merged); err != nil {
			return errDocumentsOutdate
		}
		now := a.clock.Now()
		after := make(map[string]any, len(docs))
		for _, d := range docs {
			if err := q.AccountUpsertConsent(ctx, identitydb.AccountUpsertConsentParams{UserID: userID,
				Document: identitydb.LegalDocument(d.Document), Version: d.Version, Now: now,
				ClientKind: identitydb.ClientKind(kind)}); err != nil {
				return fmt.Errorf("identity: согласие: %w", err)
			}
			after[string(d.Document)] = d.Version
		}
		if err := a.auditUser(ctx, ActionDocumentsAccepted, userID, nil, map[string]any{"versions": after}); err != nil {
			return err
		}
		v, err = a.me(ctx, q, userID)
		return err
	})
	return v, err
}

// MyAuthMethods — пароль, привязки, ключи, 2FA (чтение пулом).
func (a *App) MyAuthMethods(ctx context.Context, userID uuid.UUID) (MyAuthMethodsView, error) {
	q := identitydb.New(a.pool)
	mfa, err := a.mfaStatus(ctx, q, userID)
	if err != nil {
		return MyAuthMethodsView{}, err
	}
	v := MyAuthMethodsView{Mfa: mfa, Identities: []IdentityView{}, Passkeys: []PasskeyView{}}
	if v.HasPassword, err = q.AccountHasPassword(ctx, userID); err != nil {
		return MyAuthMethodsView{}, err
	}
	ids, err := q.AccountIdentities(ctx, userID)
	if err != nil {
		return MyAuthMethodsView{}, err
	}
	for _, r := range ids {
		v.Identities = append(v.Identities, IdentityView{ID: r.ID, Provider: string(r.Provider), Email: r.Email,
			EmailIsPrivateRelay: r.EmailIsPrivateRelay, LinkedAt: r.LinkedAt})
	}
	keys, err := q.AccountPasskeys(ctx, userID)
	if err != nil {
		return MyAuthMethodsView{}, err
	}
	for _, r := range keys {
		v.Passkeys = append(v.Passkeys, PasskeyView{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt,
			LastUsedAt: r.LastUsedAt, BackedUp: r.BackupState})
	}
	return v, nil
}
