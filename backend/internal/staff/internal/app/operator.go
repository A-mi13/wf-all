package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/domain"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// Операции оператора (спека §4.2): подкоманды admin-api staff … — доступ к серверу корень доверия,
// матрицей прав они не ограничены, аудит — actor_role = operator. Обёртки с --print-code — 10c;
// OperatorResetAccess и OperatorDisable — 8b.

// OperatorBootstrap — новый владелец (§4.2, U2) в одной транзакции: учётка owner в статусе invited
// (почта и имя — по правилам приглашения, validateInvitee; почта занята в любом статусе —
// staff.ErrEmailTaken), приглашение (IssueInvite, письмо staff.invite новой учётке), аудит
// staff.bootstrapped от оператора, событие staff.invited и письмо staff.bootstrapped каждому активному
// владельцу (новая учётка — invited, письма не получает). Доступен всегда, не только на пустой базе.
// Возвращает код приглашения (печатается только с --print-code).
func (a *App) OperatorBootstrap(ctx context.Context, email, name string) (string, error) {
	var code string
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		now := a.clock.Now()
		c, err := a.createInvited(ctx, q, newAccount{email: email, name: name, role: roleOwner}, now)
		if err != nil {
			return err
		}
		if code, err = a.IssueInvite(ctx, tx, c.id, purposeInvite); err != nil {
			return err
		}
		if err := audit.Write(ctx, operatorActor.entry(actionBootstrapped, c.id, nil,
			map[string]any{"role": roleOwner, "email": c.email}, staff.Meta{})); err != nil {
			return err
		}
		if err := publishInvited(ctx, c, roleOwner); err != nil {
			return err
		}
		owners, err := q.ListActiveOwners(ctx)
		if err != nil {
			return err
		}
		for _, o := range owners {
			if _, err := a.river.InsertTx(ctx, tx, staff.NewMailArgs(staff.MailBootstrapped, c.id, o), nil); err != nil {
				return fmt.Errorf("staff: письмо staff.bootstrapped: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// OperatorInviteResend — новый код приглашения оператором (истекло приглашение первого владельца):
// как повторная отправка админом, но без паузы и без проверки прав. Нет учётки (и кривая почта) —
// staff.ErrNotFound, не invited — staff.ErrWrongStage; аудит staff.invite_resent от оператора.
func (a *App) OperatorInviteResend(ctx context.Context, email string) (string, error) {
	norm, ok := domain.NormalizeEmail(email)
	if !ok {
		return "", staff.ErrNotFound
	}
	var code string
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		acct, err := q.LockAccountByEmail(ctx, norm)
		if errors.Is(err, pgx.ErrNoRows) {
			return staff.ErrNotFound
		}
		if err != nil {
			return err
		}
		purpose, err := a.lockForResend(ctx, q, acct.ID, false)
		if err != nil {
			return err
		}
		if code, err = a.IssueInvite(ctx, tx, acct.ID, purpose); err != nil {
			return err
		}
		return audit.Write(ctx, operatorActor.entry(actionInviteResent, acct.ID, nil, map[string]any{"purpose": purpose}, staff.Meta{}))
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// RekeyReport — итог перешифровки: сколько секретов TOTP и кодов приглашений перешифровано текущим
// ключом и сколько неиспользованных резервных кодов ещё на других ключах (старый ключ убирается из
// ADMIN_STAFF_SECRET_KEYS, только когда это число 0).
type RekeyReport struct {
	MFA, Invites, RecoveryOnOldKeys int
	// MFAOnOldKeys, InvitesOnOldKeys — сколько строк после прогона всё ещё не на текущем ключе
	// (нечитаемые и зашифрованные старым ключом экземпляром прежнего релиза при скользящем деплое):
	// ключ убирают, только когда все три счётчика — 0.
	MFAOnOldKeys, InvitesOnOldKeys int
	// UnreadableMFA (id сотрудников) и UnreadableInvites (id приглашений) — строки, которые не
	// расшифровались: их ключа нет в кольце (убран раньше времени) или шифротекст повреждён.
	UnreadableMFA, UnreadableInvites []uuid.UUID
}

// Rekey — перешифровка текущим ключом кольца (§4.11, синхронно, без River): секреты TOTP (staff_mfa)
// и коды приглашений (code_enc не NULL), которые не на текущем ключе. Каждая строка — своей короткой
// транзакцией под замком учётки (порядок как у сценариев: учётка → staff_mfa | приглашение): гонки с
// настройкой 2FA и принятием нет, а строка, которую успели перезаписать текущим ключом, повторно не
// трогается. Нечитаемая строка (ключ убрали из кольца раньше времени, шифротекст повреждён) не
// останавливает прогон: пропускается и перечисляется в UnreadableMFA / UnreadableInvites. Резервные
// коды перехэшировать нельзя (исходника нет) — только счёт. После прогона — сколько строк всё ещё не
// на текущем ключе (MFAOnOldKeys, InvitesOnOldKeys, RecoveryOnOldKeys): ключ убирают, только когда
// все три — 0. Итог — запись аудита staff.rekeyed от оператора (ротация — действие оператора), и при
// частичном прогоне: сбой базы посреди прогона — запись с partial = true на detached, ошибка — как
// есть.
func (a *App) Rekey(ctx context.Context) (RekeyReport, error) {
	cur := a.keys.CurrentID()
	rep, runErr := a.rekeyRows(ctx, cur)
	if runErr == nil {
		runErr = a.rekeyCounts(ctx, cur, &rep)
	}
	actx, cancel := detached(ctx) // итог пишется и после обрыва
	defer cancel()
	err := db.InTx(actx, a.pool, func(ctx context.Context, _ pgx.Tx) error {
		e := operatorActor.entry(actionRekeyed, uuid.Nil, nil, map[string]any{
			"key_id": cur, "mfa_count": rep.MFA, "invites_count": rep.Invites,
			"recovery_codes_count":      rep.RecoveryOnOldKeys,
			"mfa_on_old_keys_count":     rep.MFAOnOldKeys,
			"invites_on_old_keys_count": rep.InvitesOnOldKeys,
			"unreadable_count":          len(rep.UnreadableMFA) + len(rep.UnreadableInvites),
			"partial":                   runErr != nil,
		}, staff.Meta{})
		e.ObjectType = objectStaffKeys
		return audit.Write(ctx, e)
	})
	return rep, errors.Join(runErr, err)
}

// rekeyRows — перешифровка строк не на ключе cur; нечитаемые — в отчёт, не в ошибку.
func (a *App) rekeyRows(ctx context.Context, cur string) (RekeyReport, error) {
	var rep RekeyReport
	q := staffdb.New(a.pool)
	staffIDs, err := q.ListMFAOnOldKeys(ctx, cur)
	if err != nil {
		return rep, err
	}
	for _, sid := range staffIDs {
		res, err := a.rekeyMFA(ctx, sid)
		if err != nil {
			return rep, err
		}
		switch res {
		case rekeyDone:
			rep.MFA++
		case rekeyUnreadable:
			rep.UnreadableMFA = append(rep.UnreadableMFA, sid)
		}
	}
	invites, err := q.ListInvitesOnOldKeys(ctx, cur)
	if err != nil {
		return rep, err
	}
	for _, inv := range invites {
		res, err := a.rekeyInvite(ctx, inv.StaffID, inv.ID)
		if err != nil {
			return rep, err
		}
		switch res {
		case rekeyDone:
			rep.Invites++
		case rekeyUnreadable:
			rep.UnreadableInvites = append(rep.UnreadableInvites, inv.ID)
		}
	}
	return rep, nil
}

// rekeyCounts — сколько строк после прогона ещё не на ключе cur.
func (a *App) rekeyCounts(ctx context.Context, cur string, rep *RekeyReport) error {
	q := staffdb.New(a.pool)
	mfa, err := q.CountMFAOnOldKeys(ctx, cur)
	if err != nil {
		return err
	}
	inv, err := q.CountInvitesOnOldKeys(ctx, cur)
	if err != nil {
		return err
	}
	rec, err := q.CountRecoveryOnOldKeys(ctx, cur)
	if err != nil {
		return err
	}
	rep.MFAOnOldKeys, rep.InvitesOnOldKeys, rep.RecoveryOnOldKeys = int(mfa), int(inv), int(rec)
	return nil
}

// rekeyResult — исход перешифровки одной строки.
type rekeyResult int

const (
	rekeySkipped    rekeyResult = iota // строки нет или она уже на текущем ключе
	rekeyDone                          // перешифрована
	rekeyUnreadable                    // не расшифровалась: ключа нет в кольце или шифротекст повреждён
)

// rekeyMFA — секрет TOTP сотрудника staffID текущим ключом под замком учётки.
func (a *App) rekeyMFA(ctx context.Context, staffID uuid.UUID) (rekeyResult, error) {
	res := rekeySkipped
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		if _, err := q.LockAccount(ctx, staffID); err != nil {
			return ignoreNoRows(err)
		}
		mfa, err := q.LockMFA(ctx, staffID)
		if err != nil {
			return ignoreNoRows(err)
		}
		if mfa.KeyID == a.keys.CurrentID() {
			return nil
		}
		secret, err := a.openTOTP(staffID, mfa.SecretEnc, mfa.KeyID)
		if err != nil {
			res = rekeyUnreadable
			a.log.WarnContext(ctx, "staff: rekey — секрет TOTP не расшифровался", "staff_id", staffID.String(), "key_id", mfa.KeyID, "err", err)
			return nil //nolint:nilerr // нечитаемая строка — в отчёт (UnreadableMFA), прогон продолжается
		}
		ct, keyID, err := a.sealTOTP(staffID, secret)
		if err != nil {
			return fmt.Errorf("staff: секрет TOTP сотрудника %s: %w", staffID, err)
		}
		res = rekeyDone
		return q.RekeyMFA(ctx, staffdb.RekeyMFAParams{SecretEnc: ct, KeyID: keyID, StaffID: staffID})
	})
	return res, err
}

// rekeyInvite — код приглашения inviteID текущим ключом под замком учётки staffID; кода уже нет
// (стёрт, приглашение закрыто) или он на текущем ключе — rekeySkipped.
func (a *App) rekeyInvite(ctx context.Context, staffID, inviteID uuid.UUID) (rekeyResult, error) {
	res := rekeySkipped
	err := db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := staffdb.New(tx)
		if _, err := q.LockAccount(ctx, staffID); err != nil {
			return ignoreNoRows(err)
		}
		row, err := q.LockInviteCode(ctx, inviteID)
		if err != nil {
			return ignoreNoRows(err)
		}
		if row.CodeEnc == nil || row.CodeKeyID == nil || *row.CodeKeyID == a.keys.CurrentID() {
			return nil
		}
		code, err := a.openInviteCode(inviteID, row.CodeEnc, *row.CodeKeyID)
		if err != nil {
			res = rekeyUnreadable
			a.log.WarnContext(ctx, "staff: rekey — код приглашения не расшифровался", "invite_id", inviteID.String(), "key_id", *row.CodeKeyID, "err", err)
			return nil //nolint:nilerr // нечитаемая строка — в отчёт (UnreadableInvites), прогон продолжается
		}
		ct, keyID, err := a.keys.Seal([]byte(code), inviteAAD(inviteID))
		if err != nil {
			return fmt.Errorf("staff: код приглашения %s: %w", inviteID, err)
		}
		res = rekeyDone
		return q.RekeyInvite(ctx, staffdb.RekeyInviteParams{CodeEnc: ct, CodeKeyID: keyID, ID: inviteID})
	})
	return res, err
}

// ignoreNoRows — строки нет (удалили между списком и замком) — не ошибка.
func ignoreNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
