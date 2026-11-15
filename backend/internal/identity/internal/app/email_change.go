package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
)

// Смена почты (спека §3.9, I7) — владелец Task 13: код на новую почту; занятая — приманка и письмо на новый адрес,
// ответ тот же; после подтверждения — письмо на старую.

var (
	errEmailChangeShape = httpx.NewFieldError("body.new_email", "format")
	errEmailSame        = httpx.NewError(http.StatusUnprocessableEntity, identity.CodeEmailSame)
)

// StartEmailChange — код на новую почту (§3.9 п. 1): форма (400) → tx: requireFresh (users первым) → та же почта —
// 422 email_same → одноразовый домен — 422 → лимит по почте (a.limiterTx) → занята другим аккаунтом — приманка с user_id вызывающего
// (I7: подтверждение ведёт себя как у настоящего кода) и письмо email_change_taken на новый адрес; иначе код
// (payload_enc — новая почта) и письмо email_change_code.
func (a *App) StartEmailChange(ctx context.Context, sessionID uuid.UUID, newEmail string, c Client) (CodeSent, error) {
	norm, ok := emailaddr.Normalize(newEmail)
	if !ok {
		return CodeSent{}, errEmailChangeShape
	}
	var sent CodeSent
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		s, err := a.requireFresh(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		u, err := a.lockUser(ctx, tx, s.UserID)
		if err != nil {
			return err
		}
		if u.Email == norm {
			return errEmailSame
		}
		if domain.IsDisposable(emailaddr.Domain(norm)) {
			return errSignupEmailDisposable
		}
		// лимит — после свежести (несвежая сессия не тратит чужой счётчик почты) и на соединении tx (I1): второе
		// соединение пула под замком users при залпе занимало бы пул
		if err := a.codeSendAllowed(ctx, a.limiterTx(tx), identitydb.AuthCodePurposeEmailChange, norm); err != nil {
			return err
		}
		_, err = identitydb.New(tx).UserByEmail(ctx, norm)
		taken := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("identity: аккаунт по почте: %w", err)
		}
		p := StartCode{Purpose: identitydb.AuthCodePurposeEmailChange, Email: norm, UserID: u.ID, Decoy: taken,
			Client: Client{IP: c.IP}} // адрес текущего запроса (m5), не строки сессии
		mail := MailEmailChangeTaken
		if !taken {
			p.Payload, mail = []byte(norm), MailEmailChangeCode
		}
		if sent, err = a.startCode(ctx, tx, p); err != nil {
			return err
		}
		return a.enqueueMail(ctx, tx, mail, sent.CodeID)
	})
	return sent, err
}

// ConfirmEmailChange — новая почта по коду (§3.9 п. 2): код чужого аккаунта (или без аккаунта) — 410 без попытки и
// без замка чужого аккаунта; checkCode(CodeConsume) приёмом R30 (неудача коммитится; приманка с user_id отвечает как
// настоящий код); старая почта — meta_enc записи email_changed (адрес письма email_changed на старую почту); users.email,
// email_verified_at, version + 1; гонка 23505 — 409 email_taken; живые коды аккаунта отозваны (I2); событие
// identity.user_email_changed без адреса, аудит; сессии не гасятся. Ответ — Me с новой почтой.
func (a *App) ConfirmEmailChange(ctx context.Context, userID, codeID uuid.UUID, code string) (MeView, error) {
	var view MeView
	err := a.inTxCommitFailure(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := identitydb.New(tx)
		r, err := q.FindCode(ctx, codeID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (r.UserID == nil || *r.UserID != userID)) {
			return errCodeExpired
		}
		if err != nil {
			return fmt.Errorf("identity: код: %w", err)
		}
		row, err := a.checkCode(ctx, tx, identitydb.AuthCodePurposeEmailChange, codeID, code, CodeConsume)
		if err != nil {
			return err
		}
		if row.UserID == nil || *row.UserID != userID || row.PayloadEnc == nil || row.KeyID == nil {
			return errCodeExpired
		}
		raw, err := a.secrets.Open(row.PayloadEnc, *row.KeyID, row.ID[:])
		if err != nil {
			return fmt.Errorf("identity: новая почта: %w", err)
		}
		u, err := a.lockUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		ver, err := q.ChangeUserEmail(ctx, identitydb.ChangeUserEmailParams{Email: string(raw), Now: a.clock.Now(), ID: userID})
		if uniqueViolation(err, "users_email_key") {
			return errSignupEmailTaken
		}
		if err != nil {
			return fmt.Errorf("identity: смена почты: %w", err)
		}
		// живые коды аккаунта (сброс пароля на прежний адрес и прочие) — отозваны (I2): взломанная прежняя почта не
		// захватит аккаунт кодом, выданным до смены
		if err := q.RevokeUserCodes(ctx, identitydb.RevokeUserCodesParams{Now: new(a.clock.Now()), UserID: &userID}); err != nil {
			return fmt.Errorf("identity: отзыв кодов аккаунта: %w", err)
		}
		ref, err := a.securityEvent(ctx, tx, SecurityEvent{UserID: userID, Kind: domain.SecEmailChanged, Address: u.Email})
		if err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailEmailChanged, ref); err != nil {
			return err
		}
		if _, err := events.Publish(ctx, identity.UserEmailChanged(identity.UserRef{ID: userID, Version: ver})); err != nil {
			return err
		}
		if err := a.auditUser(ctx, ActionEmailChanged, userID, nil, nil); err != nil {
			return err
		}
		view, err = a.me(ctx, q, userID)
		return err
	})
	return view, err
}
