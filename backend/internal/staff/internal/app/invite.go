package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/token"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Поля тела запроса и правила для 400 validation.failed (контракт: staffAcceptInvite,
// StaffInviteRequest).
const (
	fieldPassword    = "body.password"
	fieldEmail       = "body.email"
	fieldDisplayName = "body.display_name"
	ruleFormat       = "format"
	ruleMinLength    = "minLength"
	ruleMaxLength    = "maxLength"
	maxDisplayName   = 80 // символов: контракт StaffInviteRequest и CHECK staff_accounts_display_name_check
)

// errForbidden — 403 auth.forbidden: право сотрудника не Allow (сценарий перепроверяет конвейер).
var errForbidden = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)

// inviteAAD — aad шифрования кода приглашения: id приглашения (D4) — шифротекст другой строки не
// расшифруется.
func inviteAAD(inviteID uuid.UUID) []byte { return inviteID[:] }

// newInviteCode — новый код приглашения (xxxxx-xxxxx, domain.NewInviteCode) и его шифротекст текущим
// ключом кольца.
func (a *App) newInviteCode(inviteID uuid.UUID) (code string, ct []byte, keyID string, err error) {
	if code, err = domain.NewInviteCode(a.rand); err != nil {
		return "", nil, "", err
	}
	if ct, keyID, err = a.keys.Seal([]byte(code), inviteAAD(inviteID)); err != nil {
		return "", nil, "", fmt.Errorf("staff: шифрование кода приглашения: %w", err)
	}
	return code, ct, keyID, nil
}

// openInviteCode — код приглашения из staff_invites.
func (a *App) openInviteCode(inviteID uuid.UUID, ct []byte, keyID string) (string, error) {
	plain, err := a.keys.Open(ct, keyID, inviteAAD(inviteID))
	if err != nil {
		return "", fmt.Errorf("staff: код приглашения: %w", err)
	}
	return string(plain), nil
}

// inviteCodeMatches — совпадает ли нормализованный ввод input (domain.NormalizeInviteCode; кривой ввод —
// "") с кодом приглашения inviteID; сравнение за постоянное время. Нечитаемый код (readInviteCode) —
// не совпал: принятие отвечает staff.invite_invalid, а не 500.
func (a *App) inviteCodeMatches(ctx context.Context, inviteID uuid.UUID, ct []byte, keyID, input string) bool {
	_, want, ok := a.readInviteCode(ctx, inviteID, ct, keyID)
	return ok && subtle.ConstantTimeCompare([]byte(want), []byte(input)) == 1
}

// readInviteCode — код приглашения: plain — показанная форма (письмо), norm — нормализованная (сверка);
// ok=false — код нечитаем (ключ убран из кольца
// раньше rekey, шифротекст повреждён, открытый код не в формате xxxxx-xxxxx): предупреждение в журнал
// сервера с id приглашения и ключа, без кода. Исправление — вернуть ключ в ADMIN_STAFF_SECRET_KEYS или
// выпустить новое приглашение (админ, оператор).
func (a *App) readInviteCode(ctx context.Context, inviteID uuid.UUID, ct []byte, keyID string) (plain, norm string, ok bool) {
	plain, err := a.openInviteCode(inviteID, ct, keyID)
	if err != nil {
		a.log.WarnContext(ctx, "staff: код приглашения не расшифровался — нужен ключ или новое приглашение",
			"invite_id", inviteID.String(), "key_id", keyID, "err", err)
		return "", "", false
	}
	if norm, ok = domain.NormalizeInviteCode(plain); !ok {
		a.log.WarnContext(ctx, "staff: код приглашения не в формате xxxxx-xxxxx — нужно новое приглашение",
			"invite_id", inviteID.String())
		return "", "", false
	}
	return plain, norm, true
}

// acceptFailed — принять нечего (кривая или чужая почта, учётка не invited, нет приглашения, срок,
// неверный или нечитаемый код): одна запись аудита staff.invite_accept_failed без автора (почта
// маскируется, кода нет), как у неудачного входа, — пути «нет приглашения» и «неверный код» пишут
// одинаково. Объект — учётка, если она есть (uuid.Nil — нет). Журнал не засыпать: не больше
// inviteAuditPolicy записей на почту (ratelimit.EmailKey от HMAC почты), сверх потолка — без записи.
// Хранилище лимитов недоступно — запись идёт (лучше лишняя, чем потерянная). Без замков учётки и
// строки; ctx — detached (D8). Ответ всегда staff.ErrInviteInvalid (или ошибка записи).
func (a *App) acceptFailed(ctx context.Context, object uuid.UUID, email string, m staff.Meta) error {
	key, err := a.emailKey(classInviteAudit, email)
	if err != nil {
		return err
	}
	res, err := a.limiter.Allow(ctx, key, inviteAuditPolicy)
	switch {
	case err != nil:
		a.log.ErrorContext(ctx, "staff: потолок аудита приглашения недоступен", "err", err)
	case !res.Allowed:
		return staff.ErrInviteInvalid
	}
	if err := db.InTx(ctx, a.pool, func(ctx context.Context, _ pgx.Tx) error {
		return audit.Write(ctx, audit.Entry{
			ActorRole: roleAnonymous, Action: actionInviteAcceptFailed, ObjectType: objectAccount, ObjectID: object,
			After: map[string]any{"email": email},
			IP:    m.IP, UserAgent: m.UserAgent,
		})
	}); err != nil {
		return err
	}
	return staff.ErrInviteInvalid
}

// inviteMailKind — вид письма по назначению приглашения: invite — staff.invite, access_reset —
// staff.access_reset.
func inviteMailKind(purpose string) (string, error) {
	switch purpose {
	case purposeInvite:
		return staff.MailInvite, nil
	case purposeAccessReset:
		return staff.MailAccessReset, nil
	}
	return "", fmt.Errorf("staff: неизвестное назначение приглашения %q", purpose)
}

// IssueInvite — новое приглашение учётки staffID с назначением purpose (invite | access_reset) в
// транзакции вызывающего tx (оператор, повторная отправка админом, исполнители подтверждений 8b):
// живое приглашение учётки гасится (код стирается), новое живёт 24 ч с одной отправкой, код
// шифруется кольцом (aad — id приглашения), письмо — задача staff.mail (ref — приглашение, recipient —
// учётка) в той же транзакции. Код возвращается вызывающему (оператор печатает его только с
// --print-code); в базе, аргументах задачи и аудите его нет. Учётку вызывающий держит под замком:
// живое приглашение у неё одно (staff_invites_live_key).
func (a *App) IssueInvite(ctx context.Context, tx pgx.Tx, staffID uuid.UUID, purpose string) (string, error) {
	kind, err := inviteMailKind(purpose)
	if err != nil {
		return "", err
	}
	q := staffdb.New(tx)
	now := a.clock.Now()
	if _, err := q.RevokeLiveInvite(ctx, staffdb.RevokeLiveInviteParams{Now: now, StaffID: staffID}); err != nil {
		return "", err
	}
	inviteID := id.New()
	code, ct, keyID, err := a.newInviteCode(inviteID)
	if err != nil {
		return "", err
	}
	if err := q.InsertInvite(ctx, staffdb.InsertInviteParams{
		ID: inviteID, StaffID: staffID, Purpose: staffdb.StaffInvitePurpose(purpose), CodeEnc: ct, CodeKeyID: keyID,
		ExpiresAt: now.Add(inviteTTL), Now: now,
	}); err != nil {
		return "", err
	}
	if _, err := a.river.InsertTx(ctx, tx, staff.NewMailArgs(kind, inviteID, staffID), nil); err != nil {
		return "", fmt.Errorf("staff: письмо %s: %w", kind, err)
	}
	return code, nil
}

// ResendInviteSelf — повторная отправка кода самим сотрудником (§4.3 п.3, анонимно): для вызывающего
// всегда nil — по ответу не узнать, есть ли почта. Письмо уходит, только если почта нормальная,
// учётка invited, приглашение живо, с кодом и не истекло, с прошлой отправки прошло 60 с и отправок
// у приглашения меньше 5 (строка живёт не дольше суток: срок 24 ч самостоятельная отправка не
// продлевает, поэтому sends строки — отправки за её сутки). Код ТОТ ЖЕ (решение пользователя
// 14.10.2026): строка не перешифровывается, sends + 1, срок прежний — чужое нажатие лишь присылает
// приглашённому копию его кода и не делает недействительным уже полученное письмо. Код в аргументы
// задачи не идёт: составитель расшифровывает его по id приглашения. Сначала чтение без замков:
// отправлять нечего — ответ без замков и записей (иначе существующая почта отвечала бы дольше
// несуществующей). Подходящему кандидату — замки учётки и строки и проверка заново: две быстрые
// отправки дают одно письмо. Аудит отправки — без автора (anonymous), почта маскируется.
func (a *App) ResendInviteSelf(ctx context.Context, email string, m staff.Meta) error {
	norm, ok := domain.NormalizeEmail(email)
	if !ok {
		return nil
	}
	// без замков: отправлять нечего — ответ без замков и записей (время не выдаёт почту)
	c, ok, err := a.inviteCandidate(ctx, norm)
	if err != nil || !ok || c.CodeEnc == nil || !selfResendable(*c.ExpiresAt, *c.LastSentAt, *c.Sends, a.clock.Now()) {
		return err
	}
	// кандидат подходит — под замками учётки и строки проверка заново: две быстрые отправки дают
	// одно письмо
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := q.LockAccountByEmail(ctx, norm)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if acct.Status != statusInvited {
			return nil
		}
		inv, err := q.LockLiveInvite(ctx, acct.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		now := a.clock.Now() // после замков: параллельная отправка могла их держать
		if inv.CodeEnc == nil || !selfResendable(inv.ExpiresAt, inv.LastSentAt, inv.Sends, now) {
			return nil
		}
		kind, err := inviteMailKind(inv.Purpose)
		if err != nil {
			return err
		}
		sends, err := q.MarkInviteResent(ctx, staffdb.MarkInviteResentParams{Now: now, ID: inv.ID})
		if err != nil {
			return err
		}
		if _, err := a.river.InsertTx(ctx, tx, staff.NewMailArgs(kind, inv.ID, acct.ID), nil); err != nil {
			return fmt.Errorf("staff: письмо %s: %w", kind, err)
		}
		return audit.Write(ctx, audit.Entry{
			ActorRole: roleAnonymous, Action: actionInviteResent, ObjectType: objectAccount, ObjectID: acct.ID,
			After: map[string]any{"email": norm, "purpose": inv.Purpose, "sends": sends},
			IP:    m.IP, UserAgent: m.UserAgent,
		})
	})
}

// ResendInvite — повторная отправка приглашения админом (§4.3 п.4): право staff.invites.resend
// (иначе 403 auth.forbidden), не над собой (staff.self_action), учётка есть (staff.not_found) и в
// статусе invited (staff.wrong_stage); с выпуска последнего приглашения меньше 60 с —
// *staff.ResendTooSoonError с остатком паузы. Новое приглашение (IssueInvite) с назначением последнего приглашения учётки — срок
// 24 ч заново; аудит staff.invite_resent от сотрудника.
func (a *App) ResendInvite(ctx context.Context, s *auth.Staff, staffID uuid.UUID, m staff.Meta) error {
	if s.Decide(permInvitesResend, uuid.Nil) != auth.Allow {
		return errForbidden
	}
	if staffID == s.StaffID {
		return staff.ErrSelfAction
	}
	return db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		purpose, err := a.lockForResend(ctx, q, staffID, true)
		if err != nil {
			return err
		}
		if _, err := a.IssueInvite(ctx, tx, staffID, purpose); err != nil {
			return err
		}
		ac, err := staffActor(ctx, q, s)
		if err != nil {
			return err
		}
		return audit.Write(ctx, ac.entry(actionInviteResent, staffID, nil, map[string]any{"purpose": purpose}, m))
	})
}

// lockForResend — учётка staffID под замком для нового приглашения админом или оператором: нет —
// staff.not_found, не invited — staff.wrong_stage; pause — отказ раньше 60 с с выпуска последнего
// приглашения (*staff.ResendTooSoonError; анонимные самоотправки паузу админа не двигают). Назначение — последнего приглашения учётки (нет приглашений — invite).
func (a *App) lockForResend(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID, pause bool) (string, error) {
	acct, err := q.LockAccount(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", staff.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if acct.Status != statusInvited {
		return "", staff.ErrWrongStage
	}
	last, err := q.GetLastInvite(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return purposeInvite, nil
	}
	if err != nil {
		return "", err
	}
	if wait := resendPause - a.clock.Now().Sub(last.CreatedAt); pause && wait > 0 {
		return "", &staff.ResendTooSoonError{RetryAfter: wait}
	}
	return last.Purpose, nil
}

// AcceptInvite — принятие приглашения (§4.4): почта, код из письма и новый пароль; oldToken —
// cookie запроса (может быть пустой или чужой). Порядок: политика пароля (400 по body.password —
// без обращения к базе и без траты попытки), хэш пароля (argon2 — до транзакции, чтобы не держать
// замок учётки; считается и для несуществующей почты), затем сверка кода и только для верного кода —
// замки. Почта, учётка не invited, нет живого приглашения, срок, нет кода или неверный код — один
// staff.ErrInviteInvalid.
//
// Неверный код (решение пользователя 14.10.2026) ничего не стирает и в строке приглашения не
// считается: код — 50 бит, перебор под IP-лимитом staff_auth недостижим за 24 ч, а стирание давало
// знающему почту анонимно срывать приглашение. Неудача — только запись аудита без автора (почта
// маскируется, кода нет) в короткой транзакции без замков, на detached (обрыв соединения её не теряет,
// D8). Порядок: чтение кандидата без замков (принимать нечего — ответ без замков и записей), сверка с
// его кодом (неверный — запись неудачи без замков: учётка под чужим замком не задерживает ответ, I-1),
// верный — замки учётки и строки, проверка заново (lockAcceptable) и сверка с кодом закрытой замком
// строки: за время ожидания админ мог выпустить новое приглашение — тогда прежний код неверен.
//
// Успех: пароль сохранён, блокировка второго фактора снята (учётка настраивает его заново),
// staff_mfa и резервные коды удалены; все сессии учётки и сессия oldToken (живая, чья угодно)
// погашены replaced; новая сессия — setup на 8 ч (токен — в cookie); аудит staff.invite_accepted.
// Приглашение не гасится до recovery-codes/ack — accept можно повторить.
func (a *App) AcceptInvite(ctx context.Context, email, code, pw, oldToken string, m staff.Meta) (string, error) {
	if err := password.Check(pw); err != nil {
		return "", passwordPolicyError(fieldPassword, err)
	}
	hash, err := a.hasher.Hash(ctx, pw)
	if err != nil {
		return "", err
	}
	// обрыв соединения клиента не теряет запись неудачи
	dctx, cancel := detached(ctx)
	defer cancel()
	norm, ok := domain.NormalizeEmail(email)
	if !ok {
		return "", a.acceptFailed(dctx, uuid.Nil, email, m) // кривая почта — в журнал как есть (маскируется)
	}
	input, _ := domain.NormalizeInviteCode(code) // кривой код — "": неверный код
	var oldHash []byte
	if oldToken != "" {
		oldHash = token.HashRefresh(oldToken)
	}
	// без замков: принимать нечего или код неверен — ответ без замков, только запись неудачи
	c, found, err := a.inviteCandidate(dctx, norm)
	if err != nil {
		return "", err
	}
	if !found || c.CodeEnc == nil || c.CodeKeyID == nil || !a.clock.Now().Before(*c.ExpiresAt) ||
		!a.inviteCodeMatches(dctx, *c.InviteID, c.CodeEnc, *c.CodeKeyID, input) {
		return "", a.acceptFailed(dctx, c.ID, norm, m) // учётки нет — c.ID нулевой, объекта нет
	}
	var (
		raw     string
		invalid bool
		object  uuid.UUID
	)
	err = db.InTx(dctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, inv, ok, err := a.lockAcceptable(ctx, q, norm)
		object = acct.ID
		if err != nil || !ok {
			invalid = !ok
			return err
		}
		now := a.clock.Now()
		// под замком — код этой строки: приглашение могли перевыпустить, пока ждали замок
		if !a.inviteCodeMatches(ctx, inv.ID, inv.CodeEnc, *inv.CodeKeyID, input) {
			invalid = true // запись неудачи — после транзакции, без замков
			return nil
		}
		if err := q.AcceptInvitePassword(ctx, staffdb.AcceptInvitePasswordParams{PasswordHash: &hash, ID: acct.ID}); err != nil {
			return err
		}
		// порядок замков: учётка → сессии → staff_mfa → резервные коды
		if _, err := revokeAccountSessions(ctx, q, acct.ID, reasonReplaced, now); err != nil {
			return err
		}
		old, err := liveSessionByToken(ctx, q, oldHash, now)
		if err != nil {
			return err
		}
		if raw, err = a.replaceSession(ctx, q, old, newSession{
			staffID: acct.ID, stage: auth.StageSetup, created: now, expires: now.Add(sessionTTL),
		}, now, m); err != nil {
			return err
		}
		if err := q.DeleteMFA(ctx, acct.ID); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, acct.ID); err != nil {
			return err
		}
		return audit.Write(ctx, audit.Entry{
			ActorStaffID: acct.ID, ActorRole: acct.Role, ActorLabel: label(acct.DisplayName),
			Action: actionInviteAccepted, ObjectType: objectAccount, ObjectID: acct.ID,
			After: map[string]any{"purpose": inv.Purpose},
			IP:    m.IP, UserAgent: m.UserAgent,
		})
	})
	if err != nil {
		return "", err
	}
	if invalid {
		return "", a.acceptFailed(dctx, object, norm, m)
	}
	return raw, nil
}

// inviteCandidate — учётка по почте и её живое приглашение без замков (GetInviteCandidate); ok=false —
// учётки нет, она не invited или живого приглашения нет: делать нечего.
func (a *App) inviteCandidate(ctx context.Context, email string) (staffdb.GetInviteCandidateRow, bool, error) {
	c, err := staffdb.New(a.pool).GetInviteCandidate(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	return c, c.Status == statusInvited && c.InviteID != nil && c.ExpiresAt != nil && c.LastSentAt != nil && c.Sends != nil, nil
}

// selfResendable — можно ли самому выслать код ещё раз (§4.3 п.3): приглашение не истекло, с прошлой
// отправки прошло 60 с, отправок меньше 5.
func selfResendable(expires, lastSent time.Time, sends int32, now time.Time) bool {
	return now.Before(expires) && now.Sub(lastSent) >= resendPause && sends < maxInviteSends
}

// lockAcceptable — учётка по почте и её живое приглашение под замками (учётка, затем приглашение),
// по которому ещё можно принять: учётка invited, срок не истёк, код есть. Иначе ok=false — без записи
// неудачи (ответ тот же staff.ErrInviteInvalid).
func (a *App) lockAcceptable(ctx context.Context, q *staffdb.Queries, email string) (staffdb.LockAccountByEmailRow, staffdb.LockLiveInviteRow, bool, error) {
	var inv staffdb.LockLiveInviteRow
	acct, err := q.LockAccountByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return acct, inv, false, nil
	}
	if err != nil || acct.Status != statusInvited {
		return acct, inv, false, err
	}
	inv, err = q.LockLiveInvite(ctx, acct.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return acct, inv, false, nil
	}
	if err != nil {
		return acct, inv, false, err
	}
	usable := a.clock.Now().Before(inv.ExpiresAt) && inv.CodeEnc != nil && inv.CodeKeyID != nil
	return acct, inv, usable, nil
}

// liveSessionByToken — id живой сессии по хэшу токена (cookie запроса); нет хэша или сессии —
// uuid.Nil.
func liveSessionByToken(ctx context.Context, q *staffdb.Queries, hash []byte, now time.Time) (uuid.UUID, error) {
	if hash == nil {
		return uuid.Nil, nil
	}
	sid, err := q.GetLiveSessionByToken(ctx, staffdb.GetLiveSessionByTokenParams{TokenHash: hash, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return sid, err
}

// actor — автор записи аудита: сотрудник (id, роль на момент действия, снимок имени) или оператор
// сервера (без id, роль и метка operator). Общий для сценариев управления (7, 8b).
type actor struct {
	staffID     uuid.UUID
	role, label string
}

// operatorActor — подкоманды admin-api staff …: доступ к серверу — корень доверия (§4.2).
var operatorActor = actor{role: roleOperator, label: roleOperator}

// staffActor — сотрудник запроса как автор: роль — из сессии, имя — снимок из staff_accounts.
func staffActor(ctx context.Context, q *staffdb.Queries, s *auth.Staff) (actor, error) {
	name, err := q.GetActorLabel(ctx, s.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return actor{}, auth.ErrSessionInvalid
	}
	if err != nil {
		return actor{}, err
	}
	return actor{staffID: s.StaffID, role: s.Role, label: label(name)}, nil
}

// entry — запись аудита действия автора над учёткой object.
func (ac actor) entry(action string, object uuid.UUID, before, after map[string]any, m staff.Meta) audit.Entry {
	e := audit.Entry{
		ActorStaffID: ac.staffID, ActorRole: ac.role, ActorLabel: ac.label,
		Action: action, ObjectType: objectAccount, ObjectID: object,
		After: after, IP: m.IP, UserAgent: m.UserAgent,
	}
	if before != nil {
		e.Before = before
	}
	return e
}

// newAccount — новая учётка invited: почта и имя как ввели (проверяет и нормализует
// createInvited), роль, города модератора и автор (uuid.Nil — оператор).
type newAccount struct {
	email, name, role string
	cities            []uuid.UUID
	by                uuid.UUID
}

// created — созданная учётка: id, версия агрегата (для события staff.invited) и нормализованная почта
// (для аудита).
type created struct {
	id      uuid.UUID
	version int64
	email   string
}

// validateInvitee — почта и имя приглашаемого по правилам контракта (StaffInviteRequest): почта —
// domain.NormalizeEmail (иначе 400 по body.email, format), имя без крайних пробелов — 1–80 символов
// (body.display_name, minLength | maxLength). Возвращает нормализованные почту и имя.
func validateInvitee(email, name string) (string, string, error) {
	norm, ok := domain.NormalizeEmail(email)
	if !ok {
		return "", "", httpx.NewFieldError(fieldEmail, ruleFormat)
	}
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", "", httpx.NewFieldError(fieldDisplayName, ruleMinLength)
	case utf8.RuneCountInString(name) > maxDisplayName:
		return "", "", httpx.NewFieldError(fieldDisplayName, ruleMaxLength)
	}
	return norm, name, nil
}

// createInvited — учётка invited с ролью и городами модератора в транзакции q (оператор, исполнитель
// подтверждения staff.invite в 8b): почта и имя — validateInvitee; почта занята (любой статус) —
// staff.ErrEmailTaken. Несуществующий город — ошибка внешнего ключа (вызывающий переводит в
// staff.city_not_found), модератор без городов — отложенный триггер на коммите. Автор by — created_by
// и granted_by (uuid.Nil — оператор, NULL). Приглашение, аудит и событие — у вызывающего.
func (a *App) createInvited(ctx context.Context, q *staffdb.Queries, na newAccount, now time.Time) (created, error) {
	email, name, err := validateInvitee(na.email, na.name)
	if err != nil {
		return created{}, err
	}
	var by *uuid.UUID
	if na.by != uuid.Nil {
		by = &na.by
	}
	c := created{id: id.New(), email: email}
	c.version, err = q.InsertInvitedAccount(ctx, staffdb.InsertInvitedAccountParams{
		ID: c.id, Email: email, DisplayName: name, Now: now, CreatedBy: by,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return created{}, staff.ErrEmailTaken
	}
	if err != nil {
		return created{}, err
	}
	if err := q.InsertRole(ctx, staffdb.InsertRoleParams{StaffID: c.id, Role: na.role, GrantedBy: by, Now: now}); err != nil {
		return created{}, err
	}
	if len(na.cities) > 0 {
		if err := q.InsertRoleCities(ctx, staffdb.InsertRoleCitiesParams{StaffID: c.id, CityIds: na.cities}); err != nil {
			return created{}, err
		}
	}
	return c, nil
}

// revokeAccountSessions — гашение всех живых сессий учётки с причиной reason (сброс доступа,
// выключение — 8b; принятие приглашения — replaced). Возвращает, сколько погашено.
func revokeAccountSessions(ctx context.Context, q *staffdb.Queries, staffID uuid.UUID, reason string, now time.Time) (int64, error) {
	return q.RevokeStaffSessions(ctx, staffdb.RevokeStaffSessionsParams{Now: now, Reason: reason, StaffID: staffID})
}

// publishInvited — событие staff.invited новой учётки (оператор, исполнитель staff.invite в 8b).
func publishInvited(ctx context.Context, c created, role string) error {
	_, err := events.Publish(ctx, staff.InvitedEvent(c.id, c.version, role))
	return err
}
