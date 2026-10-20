package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Операции оператора (спека §4.2, §4.11) для подкоманд admin-api staff …: cmd/admin-api не может
// импортировать internal/app, поэтому подкоманды идут через собранный хендлер. Доступ к серверу — корень
// доверия: матрицей прав не ограничены, аудит — actor_role = operator. Письма сценарии ставят задачей
// staff.mail в своей транзакции — отправит работающий admin-api (подкоманда SMTP не трогает).
//
// Код приглашения отдаётся наружу только с printCode: без флага вызывающий его не получает вовсе и не может
// случайно напечатать или записать в лог (логи хостинга сохранили бы его).

// errNotBuilt — нулевой Handler (не собран на зависимостях admin-api).
var errNotBuilt = errors.New("staff: хендлер не собран — операции оператора недоступны")

// RekeyReport — итог перешифровки (§4.11) для вывода подкоманды rekey.
type RekeyReport struct {
	// MFA, Invites — перешифровано текущим ключом; RecoveryOnOldKeys — неиспользованных резервных кодов на
	// других ключах (перехэшировать нельзя: ключ убирают, когда их 0 — сотрудники перевыпустили коды)
	MFA, Invites, RecoveryOnOldKeys int
	// MFAOnOldKeys, InvitesOnOldKeys — строк после прогона всё ещё не на текущем ключе
	MFAOnOldKeys, InvitesOnOldKeys int
	// UnreadableMFA (id сотрудников), UnreadableInvites (id приглашений) — не расшифровались: ключа нет в
	// кольце (убран раньше времени) или шифротекст повреждён
	UnreadableMFA, UnreadableInvites []uuid.UUID
}

// Clean — старые ключи можно убирать из ADMIN_STAFF_SECRET_KEYS: все счётчики старых ключей 0 и
// нечитаемых строк нет.
func (r RekeyReport) Clean() bool {
	return r.MFAOnOldKeys == 0 && r.InvitesOnOldKeys == 0 && r.RecoveryOnOldKeys == 0 &&
		len(r.UnreadableMFA) == 0 && len(r.UnreadableInvites) == 0
}

// OperatorBootstrap — новый владелец (учётка invited, приглашение, письмо staff.bootstrapped активным
// владельцам — U2); почта занята — staff.ErrEmailTaken. Код — только при printCode.
func (h *Handler) OperatorBootstrap(ctx context.Context, email, name string, printCode bool) (string, error) {
	if h.app == nil {
		return "", errNotBuilt
	}
	return onlyIf(printCode)(h.app.OperatorBootstrap(ctx, email, name))
}

// OperatorInviteResend — новый код приглашения учётке invited (staff.ErrNotFound, staff.ErrWrongStage). Код —
// только при printCode.
func (h *Handler) OperatorInviteResend(ctx context.Context, email string, printCode bool) (string, error) {
	if h.app == nil {
		return "", errNotBuilt
	}
	return onlyIf(printCode)(h.app.OperatorInviteResend(ctx, email))
}

// OperatorResetAccess — сброс доступа без стража последнего владельца (выход из тупика, staff.ErrNotFound).
// Код — только при printCode.
func (h *Handler) OperatorResetAccess(ctx context.Context, email string, printCode bool) (string, error) {
	if h.app == nil {
		return "", errNotBuilt
	}
	return onlyIf(printCode)(h.app.OperatorResetAccess(ctx, email))
}

// OperatorDisable — выключение под стражем последнего владельца (staff.ErrLastOwner, staff.ErrNotFound,
// staff.ErrWrongStage — уже выключена).
func (h *Handler) OperatorDisable(ctx context.Context, email string) error {
	if h.app == nil {
		return errNotBuilt
	}
	return h.app.OperatorDisable(ctx, email)
}

// OperatorRekey — перешифровка текущим ключом кольца (синхронно, без River).
func (h *Handler) OperatorRekey(ctx context.Context) (RekeyReport, error) {
	if h.app == nil {
		return RekeyReport{}, errNotBuilt
	}
	r, err := h.app.Rekey(ctx)
	return RekeyReport{
		MFA: r.MFA, Invites: r.Invites, RecoveryOnOldKeys: r.RecoveryOnOldKeys,
		MFAOnOldKeys: r.MFAOnOldKeys, InvitesOnOldKeys: r.InvitesOnOldKeys,
		UnreadableMFA: r.UnreadableMFA, UnreadableInvites: r.UnreadableInvites,
	}, err
}

// onlyIf — код сценария наружу только при show; ошибка — как есть.
func onlyIf(show bool) func(code string, err error) (string, error) {
	return func(code string, err error) (string, error) {
		if err != nil || !show {
			return "", err
		}
		return code, nil
	}
}

// OperatorKeyCheck — сколько ключей секретов staff в базе есть в кольце команды и сколько нет (App.KeyCheck).
func (h *Handler) OperatorKeyCheck(ctx context.Context) (known, unknown int, err error) {
	if h.app == nil {
		return 0, 0, errNotBuilt
	}
	return h.app.KeyCheck(ctx)
}
