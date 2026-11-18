package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/ratelimit"
)

// Коды из письма (спека §3.14) — владелец Task 10 (общие для Task 13): 6 цифр из crypto/rand, в базе — HMAC
// (code_mac, метка identity-code, сообщение id ‖ код) и шифротекст для письма (code_enc, aad — id); приманка — строка
// без секретов, совпасть нечему, счётчики и паузы — как у настоящего кода.

// CodeMode — что checkCode делает с верным кодом (N7).
type CodeMode int

const (
	// CodeConsume — верный код гасится сразу (consumed_at, code_enc = NULL).
	CodeConsume CodeMode = iota
	// CodeVerifyOnly — только сверка без записей; гасит вызывающий consumeCode в своей tx (Task 13).
	CodeVerifyOnly
)

// StartCode — новый код: назначение, почта, аккаунт (если есть), приманка, данные для payload_enc.
type StartCode struct {
	Purpose identitydb.AuthCodePurpose
	Email   string
	UserID  uuid.UUID // uuid.Nil — без аккаунта
	Decoy   bool
	Payload []byte // данные регистрации (signup) или новая почта (email_change)
	Client  Client
}

// CodeSent — ответ о коде (202 CodeSent).
type CodeSent struct {
	CodeID          uuid.UUID
	ExpiresAt       time.Time
	ResendAvailable time.Time
}

// codeRow — строка auth_codes под замком.
type codeRow = identitydb.AuthCode

// Лимит стартов кода на почту и назначение (§3.14, R16): одинаков для несуществующей почты.
var codeSendPolicy = ratelimit.Policy{Limit: 5, Period: time.Hour}

const codeSendClass = "auth_send"

var (
	errCodeInvalid   = httpx.NewError(http.StatusBadRequest, identity.CodeCodeInvalid)
	errCodeExpired   = httpx.NewError(http.StatusGone, identity.CodeCodeExpired)
	errResendTooSoon = httpx.NewError(http.StatusTooManyRequests, identity.CodeResendTooSoon)
	errRateLimited   = httpx.NewError(http.StatusTooManyRequests, httpx.CodeRateLimited)
)

// failureToCommit — неудача, которую tx обязана закоммитить до ответа ошибкой (попытка кода, R6).
type failureToCommit struct{ err error }

func (f failureToCommit) Error() string { return f.err.Error() }
func (f failureToCommit) Unwrap() error { return f.err }

// detachedTimeout — срок работы, которую обрыв запроса не должен прервать (засчитанная неудача кода, доведение смены
// пароля после сожжённого фактора): ctx без отмены клиента, но со сроком — зависшее ожидание (соединение пула, замок)
// не держит горутину и соединение вечно. 10 с — с запасом больше обычной tx сценария и одного argon2 под семафором;
// с таймаутом запроса конвейера не связан: ответ клиенту уже не нужен, нужна запись.
const detachedTimeout = 10 * time.Second

// detached — ctx без отмены клиента со сроком detachedTimeout (значения ctx — tx-хуки, peer — сохраняются).
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), detachedTimeout)
}

// inTxCommitFailure — db.InTx сценария, где неудача (failureToCommit из fn) коммитится, а наружу уходит её ошибка.
// Отмена клиента не откатывает засчитанную неудачу: tx идёт на detached (без отмены, со сроком), иначе обрыв
// соединения после неверного кода был бы бесплатной попыткой. Внутри fn — только соединение tx (лимит —
// a.limiterTx(tx)): второе соединение пула под замком users при исчерпанном пуле ждало бы до срока.
func (a *App) inTxCommitFailure(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	var fail error
	dctx, cancel := detached(ctx)
	defer cancel()
	err := db.InTx(dctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		err := fn(ctx, tx)
		var f failureToCommit
		if errors.As(err, &f) {
			fail = f.err
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	return fail
}

// codeSendAllowed — лимит стартов кода по почте: ключ email:auth_send:<HMAC purpose ‖ почта>. l — a.limiter до tx
// (регистрация, сброс: до argon2) или a.limiterTx(tx) внутри tx (смена почты — после свежести).
func (a *App) codeSendAllowed(ctx context.Context, l ratelimit.Limiter, purpose identitydb.AuthCodePurpose, email string) error {
	msg := make([]byte, 0, len(purpose)+1+len(email))
	msg = append(msg, purpose...)
	msg = append(msg, 0)
	msg = append(msg, email...)
	mac, err := a.secrets.MAC(a.secrets.CurrentID(), domain.LabelRateLimit, msg)
	if err != nil {
		return err
	}
	res, err := l.Allow(ctx, ratelimit.EmailKey(codeSendClass, mac), codeSendPolicy)
	if err != nil {
		return fmt.Errorf("identity: лимит отправки кода: %w", err)
	}
	if !res.Allowed {
		return errRateLimited.WithRetryAfter(res.RetryAfter)
	}
	return nil
}

// codeMAC — HMAC кода ключом keyID: id строки ‖ код.
func (a *App) codeMAC(keyID string, codeID uuid.UUID, code string) ([]byte, error) {
	msg := make([]byte, 0, len(codeID)+len(code))
	msg = append(msg, codeID[:]...)
	msg = append(msg, code...)
	return a.secrets.MAC(keyID, domain.LabelCode, msg)
}

// startCode — один живой код на (purpose, email), у смены почты — на (purpose, user_id, email) (0027, R40: чужой живой
// код на тот же адрес не гасится, подтверждение выбирает одного уникальностью users.email): прежний отзывается;
// приманка — без секретов. Письмо ставит
// вызывающий (вид письма зависит от ветки). Параллельные старты на ту же почту идут по очереди под
// pg_advisory_xact_lock (purpose, email) до конца tx (m1): отзыв видит закоммиченный код соседа (READ COMMITTED,
// новый снимок на оператор), вставка не конфликтует. ON CONFLICT DO NOTHING и повтор остаются защитой в глубину.
func (a *App) startCode(ctx context.Context, tx pgx.Tx, p StartCode) (CodeSent, error) {
	now := a.clock.Now()
	rowID := id.New()
	params := identitydb.InsertCodeParams{ID: rowID, Purpose: p.Purpose, Email: p.Email, UserID: optUUID(p.UserID),
		Decoy: p.Decoy, Now: now, ExpiresAt: now.Add(domain.CodeTTL), Ip: p.Client.IP}
	if !params.Ip.IsValid() {
		params.Ip = netip.IPv4Unspecified() // колонка NOT NULL: адрес не разобран — неуказанный, как у сессии
	}
	if !p.Decoy {
		code, err := domain.NewEmailCode(rand.Reader)
		if err != nil {
			return CodeSent{}, err
		}
		keyID := a.secrets.CurrentID()
		if params.CodeMac, err = a.codeMAC(keyID, rowID, code); err != nil {
			return CodeSent{}, err
		}
		if params.CodeEnc, _, err = a.secrets.Seal([]byte(code), rowID[:]); err != nil {
			return CodeSent{}, err
		}
		if p.Payload != nil {
			if params.PayloadEnc, _, err = a.secrets.Seal(p.Payload, rowID[:]); err != nil {
				return CodeSent{}, err
			}
		}
		params.KeyID = &keyID
	}
	q := identitydb.New(tx)
	if err := q.LockCodeSlot(ctx, identitydb.LockCodeSlotParams{Purpose: string(p.Purpose), Email: p.Email}); err != nil {
		return CodeSent{}, fmt.Errorf("identity: очередь стартов кода: %w", err)
	}
	// смена почты — один живой код на (аккаунт, почта) (R40): отзыв только своего кода; вставка — без цели арбитра,
	// уникальность держат оба индекса живых кодов
	for range 2 {
		if err := q.RevokeLiveCode(ctx, identitydb.RevokeLiveCodeParams{Now: &now, Purpose: p.Purpose, Email: p.Email,
			UserID: params.UserID}); err != nil {
			return CodeSent{}, fmt.Errorf("identity: отзыв прежнего кода: %w", err)
		}
		n, err := q.InsertCode(ctx, params)
		if err != nil {
			return CodeSent{}, fmt.Errorf("identity: код: %w", err)
		}
		if n == 1 {
			next, _ := domain.ResendAvailableAt(now, 1)
			return CodeSent{CodeID: rowID, ExpiresAt: params.ExpiresAt, ResendAvailable: next}, nil
		}
	}
	return CodeSent{}, errors.New("identity: живой код не вставлен после повтора")
}

// lockCode — строка кода приёмом R30 (lockByRow): поиск без замка → hooks.afterRowFound("code") → lockUser(user_id),
// если он есть → FOR UPDATE строки. Нет строки, чужое назначение (purpose != "" и не то) или аккаунт сменился —
// errCodeExpired.
func (a *App) lockCode(ctx context.Context, tx pgx.Tx, purpose identitydb.AuthCodePurpose, codeID uuid.UUID) (codeRow, error) {
	var row codeRow
	err := a.lockByRow(ctx, tx, "code",
		func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
			r, err := q.FindCode(ctx, codeID)
			if err != nil {
				return nil, err
			}
			if purpose != "" && r.Purpose != purpose {
				return nil, pgx.ErrNoRows
			}
			return r.UserID, nil
		},
		func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
			var err error
			row, err = q.LockCode(ctx, codeID)
			return row.UserID, err
		})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errRowGone) {
		return codeRow{}, errCodeExpired
	}
	if err != nil {
		return codeRow{}, fmt.Errorf("identity: код: %w", err)
	}
	return row, nil
}

// codeClosed — код погашен, отозван, истёк (now ≥ expires_at) или исчерпал попытки.
func (a *App) codeClosed(row codeRow) bool {
	return row.ConsumedAt != nil || row.RevokedAt != nil || !a.clock.Now().Before(row.ExpiresAt) ||
		row.Attempts >= domain.CodeAttempts
}

// checkCode — сверка кода приёмом R30 (lockCode); mode — гасить ли верный. Закрыт — 410 code_expired. Неверный
// (приманке совпасть нечему; ввод не из 6 цифр; ключ строки вне кольца) — attempts + 1: пятая гасит и 410, иначе
// 400 code_invalid + attempts_left — обе как failureToCommit (вызывающий коммитит неудачу, inTxCommitFailure).
func (a *App) checkCode(ctx context.Context, tx pgx.Tx, purpose identitydb.AuthCodePurpose, codeID uuid.UUID, code string, mode CodeMode) (codeRow, error) {
	row, err := a.lockCode(ctx, tx, purpose, codeID)
	if err != nil {
		return codeRow{}, err
	}
	if a.codeClosed(row) {
		return codeRow{}, errCodeExpired
	}
	ok, err := a.codeMatches(row, code)
	if err != nil {
		return codeRow{}, err
	}
	if !ok {
		now := a.clock.Now()
		n, err := identitydb.New(tx).FailCode(ctx, identitydb.FailCodeParams{MaxAttempts: domain.CodeAttempts, Now: now,
			ID: row.ID})
		if err != nil {
			return codeRow{}, fmt.Errorf("identity: неудача кода: %w", err)
		}
		if int(n) >= domain.CodeAttempts {
			return codeRow{}, failureToCommit{errCodeExpired}
		}
		return codeRow{}, failureToCommit{errCodeInvalid.WithAttemptsLeft(domain.CodeAttempts - int(n))}
	}
	if mode == CodeConsume {
		if err := a.consumeCode(ctx, tx, row.ID); err != nil {
			return codeRow{}, err
		}
	}
	return row, nil
}

// codeMatches — ввод совпал с кодом строки (hmac.Equal); приманка и ключ вне кольца — нет.
func (a *App) codeMatches(row codeRow, code string) (bool, error) {
	norm, ok := domain.NormalizeEmailCode(code)
	if !ok || row.Decoy || row.CodeMac == nil || row.KeyID == nil || !a.secrets.Has(*row.KeyID) {
		return false, nil
	}
	mac, err := a.codeMAC(*row.KeyID, row.ID, norm)
	if err != nil {
		return false, err
	}
	return hmac.Equal(mac, row.CodeMac), nil
}

// consumeCode — погашение кода, сверенного в CodeVerifyOnly (строка под замком вызывающего); уже закрыт — 410.
func (a *App) consumeCode(ctx context.Context, tx pgx.Tx, codeID uuid.UUID) error {
	n, err := identitydb.New(tx).ConsumeCode(ctx, identitydb.ConsumeCodeParams{Now: new(a.clock.Now()), ID: codeID})
	if err != nil {
		return fmt.Errorf("identity: погашение кода: %w", err)
	}
	if n == 0 {
		return errCodeExpired
	}
	return nil
}

// resendMail — письмо повтора по назначению кода. У приманки — тот же вид письма настоящего кода: задача ставится так
// же (§9.1: одна работа с базой, страж TestCodeDecoySameWork), а сборщик письма её отбрасывает — «код другого вида»
// (identity/mail, code: decoy строки ≠ decoy вида), так что приманка писем по-прежнему не шлёт (§3.14, R26).
func resendMail(row codeRow) (string, bool) {
	switch row.Purpose {
	case identitydb.AuthCodePurposeSignup:
		return MailSignupCode, true
	case identitydb.AuthCodePurposePasswordReset:
		return MailPasswordResetCode, true
	case identitydb.AuthCodePurposeEmailChange:
		return MailEmailChangeCode, true
	}
	return "", false
}

// ResendCode — повторная отправка того же кода (паузы domain.ResendAvailableAt): раньше паузы — 429
// resend_too_soon + Retry-After; закрыт или 4 отправки — 410. Приманка — те же паузы и счётчики без письма.
func (a *App) ResendCode(ctx context.Context, codeID uuid.UUID) (CodeSent, error) {
	var out CodeSent
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := a.lockCode(ctx, tx, "", codeID)
		if err != nil {
			return err
		}
		if a.codeClosed(row) {
			return errCodeExpired
		}
		next, ok := domain.ResendAvailableAt(row.LastSentAt, int(row.Sends))
		if !ok {
			return errCodeExpired
		}
		now := a.clock.Now()
		if now.Before(next) {
			return errResendTooSoon.WithRetryAfter(next.Sub(now))
		}
		if err := identitydb.New(tx).CodeResent(ctx, identitydb.CodeResentParams{Now: now, ID: row.ID}); err != nil {
			return fmt.Errorf("identity: повтор кода: %w", err)
		}
		if kind, ok := resendMail(row); ok {
			if err := a.enqueueMail(ctx, tx, kind, row.ID); err != nil {
				return err
			}
		}
		// следующая пауза не позже срока кода (m4): позже expires_at повтор уже 410
		out = CodeSent{CodeID: row.ID, ExpiresAt: row.ExpiresAt, ResendAvailable: row.ExpiresAt}
		if after, ok := domain.ResendAvailableAt(now, int(row.Sends)+1); ok && after.Before(row.ExpiresAt) {
			out.ResendAvailable = after
		}
		return nil
	})
	return out, err
}
