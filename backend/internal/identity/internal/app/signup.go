package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/risk"
)

// Регистрация по почте (спека §3.1, §3.16, §9.1) — владелец Task 10. Ни ответ, ни время не раскрывают занятую почту:
// обе ветки хэшируют пароль, создают строку auth_codes и задачу письма; разница уходит письмом владельцу.

// SignupInput — startSignup.
type SignupInput struct {
	Email, Password, Nickname, Locale string
	BirthDate                         time.Time
	CityID                            uuid.UUID
	Accepted                          []domain.Accepted
}

// ConfirmInput — confirmSignup: код и, если ник заняли, новый ник.
type ConfirmInput struct {
	CodeID         uuid.UUID
	Code, Nickname string
}

// LegalDocument — текущий документ (getLegalDocuments).
type LegalDocument = domain.LegalDoc

var (
	errSignupEmailShape      = httpx.NewFieldError("body.email", "format")
	errSignupBirthDate       = httpx.NewFieldError("body.birth_date", "format")
	errSignupPasswordWeak    = httpx.NewError(http.StatusUnprocessableEntity, identity.CodePasswordWeak)
	errSignupNickInvalid     = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeNicknameInvalid)
	errSignupNickTaken       = httpx.NewError(http.StatusConflict, identity.CodeNicknameTaken)
	errSignupDocuments       = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeDocumentsOutdated)
	errSignupCityNotFound    = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeCityNotFound)
	errSignupTooYoung        = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeTooYoung)
	errSignupEmailDisposable = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeEmailDisposable)
	errSignupEmailTaken      = httpx.NewError(http.StatusConflict, identity.CodeEmailTaken)
	errDeviceUntrusted       = httpx.NewError(http.StatusForbidden, identity.CodeDeviceUntrusted)
)

// signupPayload — данные регистрации до кода (payload_enc, Seal с aad = id кода): хэш пароля, не пароль.
type signupPayload struct {
	PasswordHash string            `json:"password_hash"`
	Nickname     string            `json:"nickname"`
	BirthDate    string            `json:"birth_date"` // YYYY-MM-DD
	CityID       uuid.UUID         `json:"city_id"`
	Locale       string            `json:"locale"`
	Accepted     []domain.Accepted `json:"accepted"`
}

// weakPassword — 422 identity.password_weak с причиной; ошибка не о слабости — 400 по полю.
func weakPassword(err error, field string) error {
	if reason, ok := domain.PasswordWeakReason(err); ok {
		return errSignupPasswordWeak.WithReasons(reason)
	}
	return httpx.NewFieldError(field, "format")
}

// signupLocale — язык аккаунта: присланный, иначе язык страны города, иначе en (поддержаны ru и en).
func signupLocale(in string, cs geo.CitySettings) string {
	for _, l := range []string{in, cs.DefaultLocale} {
		if l == "ru" || l == "en" {
			return l
		}
	}
	return "en"
}

// signupCity — город существует, его страна включена и статус известен (CitySettings видимость не фильтрует, m9);
// иначе 422 city_not_found. Таймзона не разбирается — nil (AgeYears ответит ErrNoTimezone, 500, не паника).
func (a *App) signupCity(ctx context.Context, cityID uuid.UUID) (geo.CitySettings, *time.Location, error) {
	cs, err := a.geo.CitySettings(ctx, cityID)
	if errors.Is(err, geo.ErrCityNotFound) {
		return cs, nil, errSignupCityNotFound
	}
	if err != nil {
		return cs, nil, fmt.Errorf("identity: город регистрации: %w", err)
	}
	switch cs.Status {
	case geo.StatusWaitlist, geo.StatusPilot, geo.StatusLive:
	default:
		return cs, nil, errSignupCityNotFound
	}
	if !cs.CountryEnabled {
		return cs, nil, errSignupCityNotFound
	}
	if cs.Timezone == "" {
		return cs, nil, nil // AgeYears ответит ErrNoTimezone
	}
	tz, err := time.LoadLocation(cs.Timezone)
	if err != nil {
		return cs, nil, fmt.Errorf("%w: %s: %w", domain.ErrNoTimezone, cs.Timezone, err)
	}
	return cs, tz, nil
}

// deviceSignupsReached — мобильное устройство уже исчерпало регистрации (только чтение; счётчик — countSignup при
// подтверждении). Веб и мобильный без device — false.
func (a *App) deviceSignupsReached(ctx context.Context, c Client) (bool, error) {
	if c.Kind == domain.ClientWeb || c.Device == nil {
		return false, nil
	}
	platform, installID, _, _, _, ok := deviceOf(c)
	if !ok {
		return false, nil
	}
	ids := a.devices.IDs()
	macs := make([][]byte, len(ids))
	for i, k := range ids {
		m, err := a.deviceMAC(k, platform, installID)
		if err != nil {
			return false, err
		}
		macs[i] = m
	}
	n, err := identitydb.New(a.pool).DeviceSignups(ctx, identitydb.DeviceSignupsParams{Platform: platform, Macs: macs})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: регистрации устройства: %w", err)
	}
	return int(n) >= maxSignupsPerDevice, nil
}

// signupHuman — аттестация устройства (порт Attestor, до I-3 выключена) и риск регистраций с подсети: отказ
// аттестации — 403 device_untrusted; требование проверки или риск — humancheck.Require (403 humancheck.required,
// решённая задача пропускает). Сигнал Signup учитывает подтверждение (ConfirmSignup, m9), не старт: старт без
// подтверждения аккаунта не создаёт.
func (a *App) signupHuman(ctx context.Context, c Client) error {
	needHuman := false
	if c.Device != nil {
		v, err := a.attestor.Check(ctx, *c.Device)
		if err != nil {
			return err
		}
		if v.Reject {
			return errDeviceUntrusted
		}
		needHuman = v.RequireHuman
	}
	subnet := risk.Subnet(c.IP)
	if subnet != "" && !needHuman {
		susp, err := a.risk.Suspicious(ctx, risk.Signup, subnet)
		if err != nil {
			return err
		}
		needHuman = susp
	}
	if needHuman {
		return humancheck.Require(ctx, a.human)
	}
	return nil
}

// StartSignup — начало регистрации (§3.1 п. 2, порядок проверок — тест TestStartSignupOrder): форма → документы →
// устройство → город → возраст (ничего не сохраняется) → ник свободен → одноразовый домен → лимит устройства →
// аттестация и риск → лимит по почте → хэш пароля в обеих ветках (вне tx, R31) → код и письмо signup_code или
// приманка и письмо signup_account_exists (почта занята, в том числе pending_deletion). Ответ одинаков.
func (a *App) StartSignup(ctx context.Context, in SignupInput, c Client) (CodeSent, error) {
	email, ok := emailaddr.Normalize(in.Email)
	if !ok {
		return CodeSent{}, errSignupEmailShape
	}
	if err := domain.CheckBirthDate(in.BirthDate, a.clock.Now()); err != nil {
		return CodeSent{}, errSignupBirthDate
	}
	if err := password.Check(in.Password); err != nil {
		return CodeSent{}, weakPassword(err, "body.password")
	}
	nick, nickReason, err := a.nicknameVerdict(ctx, identitydb.New(a.pool), in.Nickname)
	if err != nil {
		return CodeSent{}, err
	}
	if nickReason == NickInvalid {
		return CodeSent{}, errSignupNickInvalid
	}
	if err := a.legal.Check(in.Accepted); err != nil {
		return CodeSent{}, errSignupDocuments
	}
	if err := a.requireDevice(c); err != nil {
		return CodeSent{}, err
	}
	cs, tz, err := a.signupCity(ctx, in.CityID)
	if err != nil {
		return CodeSent{}, err
	}
	age, err := domain.AgeYears(in.BirthDate, a.clock.Now(), tz)
	if err != nil {
		return CodeSent{}, fmt.Errorf("identity: возраст в городе %s: %w", cs.CityID, err)
	}
	if age < cs.MinSignupAge {
		return CodeSent{}, errSignupTooYoung
	}
	if nickReason != "" {
		return CodeSent{}, errSignupNickTaken
	}
	if domain.IsDisposable(emailaddr.Domain(email)) {
		return CodeSent{}, errSignupEmailDisposable
	}
	full, err := a.deviceSignupsReached(ctx, c)
	if err != nil {
		return CodeSent{}, err
	}
	if full {
		return CodeSent{}, errDeviceSignupLimit
	}
	if err := a.signupHuman(ctx, c); err != nil {
		return CodeSent{}, err
	}
	if err := a.codeSendAllowed(ctx, a.limiter, identitydb.AuthCodePurposeSignup, email); err != nil {
		return CodeSent{}, err
	}
	// хэш — в обеих ветках и до tx: время ответа не зависит от занятости почты (§9.1)
	hash, err := a.hasher.Hash(ctx, in.Password)
	if err != nil {
		return CodeSent{}, err
	}
	payload, err := json.Marshal(signupPayload{PasswordHash: hash, Nickname: nick,
		BirthDate: in.BirthDate.UTC().Format(time.DateOnly), CityID: cs.CityID, Locale: signupLocale(in.Locale, cs),
		Accepted: in.Accepted})
	if err != nil {
		return CodeSent{}, err
	}
	var sent CodeSent
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := identitydb.New(tx).UserByEmail(ctx, email)
		taken := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("identity: аккаунт по почте: %w", err)
		}
		p := StartCode{Purpose: identitydb.AuthCodePurposeSignup, Email: email, Decoy: taken, Client: c}
		mail := MailSignupAccountExists
		if !taken {
			p.Payload, mail = payload, MailSignupCode
		}
		if sent, err = a.startCode(ctx, tx, p); err != nil {
			return err
		}
		return a.enqueueMail(ctx, tx, mail, sent.CodeID)
	})
	return sent, err
}

// openSignupPayload — данные регистрации из payload_enc настоящего кода.
func (a *App) openSignupPayload(row codeRow) (signupPayload, error) {
	var p signupPayload
	if row.PayloadEnc == nil || row.KeyID == nil {
		return p, errors.New("identity: код регистрации без данных")
	}
	raw, err := a.secrets.Open(row.PayloadEnc, *row.KeyID, row.ID[:])
	if err != nil {
		return p, fmt.Errorf("identity: данные регистрации: %w", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("identity: данные регистрации: %w", err)
	}
	return p, nil
}

// uniqueViolation — 23505 по ограничению name.
func uniqueViolation(err error, name string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == name
}

// eventPlatform — платформа события регистрации: ios | android у мобильного, web у браузера.
func eventPlatform(c Client) string {
	if c.Kind == domain.ClientMobile && c.Device != nil {
		return c.Device.Platform
	}
	return string(domain.ClientWeb)
}

// SignupOutcome — итог confirmSignup: вход и documents_acceptance_required (§3.16).
type SignupOutcome struct {
	LoginOutcome
	// DocumentsRequired — принятые при старте версии уже не текущие (документ обновили, пока код ждал ввода).
	DocumentsRequired bool
}

// ConfirmSignup — аккаунт и сессия по коду (§3.1 п. 3): сверка без погашения (CodeVerifyOnly; неудача коммитится);
// ник перепроверяется — занят: 409 без траты попытки и без погашения (клиент присылает новый nickname); затем одна tx:
// погашение кода, users, credentials, consents, устройство (signups + 1, signed_up), сессия, журнал signup, событие
// identity.user_registered, аудит. Гонка почты (23505 users_email_key) — 409 email_taken, ника — 409 nickname_taken.
func (a *App) ConfirmSignup(ctx context.Context, in ConfirmInput, c Client) (SignupOutcome, error) {
	if err := a.requireDevice(c); err != nil {
		return SignupOutcome{}, err
	}
	var newNick string
	if in.Nickname != "" {
		n, reason, err := a.nicknameVerdict(ctx, identitydb.New(a.pool), in.Nickname)
		if err != nil {
			return SignupOutcome{}, err
		}
		if reason == NickInvalid {
			return SignupOutcome{}, errSignupNickInvalid
		}
		newNick = n
	}
	var out SignupOutcome
	err := a.inTxCommitFailure(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row, err := a.checkCode(ctx, tx, identitydb.AuthCodePurposeSignup, in.CodeID, in.Code, CodeVerifyOnly)
		if err != nil {
			return err
		}
		p, err := a.openSignupPayload(row)
		if err != nil {
			return err
		}
		nick := p.Nickname
		if newNick != "" {
			nick = newNick
		}
		q := identitydb.New(tx)
		if _, reason, err := a.nicknameVerdict(ctx, q, nick); err != nil {
			return err
		} else if reason != "" {
			return errSignupNickTaken
		}
		birth, err := time.Parse(time.DateOnly, p.BirthDate)
		if err != nil {
			return fmt.Errorf("identity: дата рождения регистрации: %w", err)
		}
		if err := a.consumeCode(ctx, tx, row.ID); err != nil {
			return err
		}
		now := a.clock.Now()
		uid := id.New()
		city := p.CityID
		err = q.InsertUser(ctx, identitydb.InsertUserParams{ID: uid, Email: row.Email, Now: now, Nickname: nick,
			BirthDate: pgtype.Date{Time: birth, Valid: true}, CityID: &city, Locale: p.Locale})
		switch {
		case uniqueViolation(err, "users_email_key"):
			return errSignupEmailTaken
		case uniqueViolation(err, "users_nickname_key"):
			return errSignupNickTaken
		case err != nil:
			return fmt.Errorf("identity: аккаунт: %w", err)
		}
		if err := q.InsertCredentials(ctx, identitydb.InsertCredentialsParams{UserID: uid, PasswordHash: p.PasswordHash,
			Now: now}); err != nil {
			return fmt.Errorf("identity: пароль: %w", err)
		}
		for _, d := range p.Accepted {
			if err := q.InsertConsent(ctx, identitydb.InsertConsentParams{UserID: uid,
				Document: identitydb.LegalDocument(d.Document), Version: d.Version, Now: now,
				ClientKind: identitydb.ClientKind(c.Kind)}); err != nil {
				return fmt.Errorf("identity: согласие: %w", err)
			}
		}
		u, err := a.lockUser(ctx, tx, uid)
		if err != nil {
			return err
		}
		dev, err := a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		if err := a.countSignup(ctx, tx, dev); err != nil {
			return err
		}
		issued, err := a.issueSession(ctx, tx, uid, dev, c, domain.MethodPassword, nil)
		if err != nil {
			return err
		}
		if err := a.linkDevice(ctx, tx, dev, uid, true); err != nil {
			return err
		}
		if _, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: uid, Kind: domain.SecSignup, SessionID: issued.SessionID,
			DeviceID: dev, IP: c.IP, UserAgent: c.UserAgent,
			Meta: map[string]any{"method": string(domain.MethodPassword), "client_kind": string(c.Kind)}}); err != nil {
			return err
		}
		if _, err := events.Publish(ctx, identity.UserRegistered(identity.UserRef{ID: uid, Version: u.Version}, city,
			string(domain.MethodPassword), eventPlatform(c))); err != nil {
			return err
		}
		if err := a.auditUser(ctx, ActionUserRegistered, uid, nil, map[string]any{"method": string(domain.MethodPassword),
			"client_kind": string(c.Kind)}); err != nil {
			return err
		}
		out.LoginOutcome = LoginOutcome{Status: StatusAuthenticated, Issued: &issued,
			User: UserBrief{ID: uid, Nickname: nick, Status: string(u.Status), Locale: u.Locale}}
		// согласия аккаунта — ровно принятые при старте: устарели к подтверждению — экран согласия (m6, §3.16)
		out.DocumentsRequired = a.legal.Check(p.Accepted) != nil
		return nil
	})
	if err != nil {
		return SignupOutcome{}, err
	}
	// сигнал Signup — созданный аккаунт (m9); лимитер — вне tx, сбой не отменяет регистрацию
	if subnet := risk.Subnet(c.IP); subnet != "" {
		if rerr := a.risk.Record(ctx, risk.Signup, subnet); rerr != nil {
			a.log.ErrorContext(ctx, "identity: учёт регистрации в риске", "err", rerr)
		}
	}
	return out, nil
}

// LegalDocuments — текущие версии документов (§3.16).
func (a *App) LegalDocuments() []LegalDocument { return a.legal.Items() }
