package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wf/backend/internal/identity/internal/store/identitydb"
)

// Ротация кольца секретов identity (спека §5.4; план I-2, Task 19, R28, I4) — команда api identity rekey под ролью
// api: у worker нет UPDATE и чтения секретов user_mfa и user_identities (§5.5). Перешифровываются долгоживущие
// секреты — user_mfa.secret_enc (и неподтверждённые) и user_identities.apple_refresh_enc. Короткоживущие
// (auth_codes, auth_challenges — минуты, security_events.meta_enc — ≤ 7 дней) живут меньше, чем старый ключ
// остаётся в кольце; MAC (резервные коды, коды, устройства, лимиты) не пересчитать — только счёт резервных кодов под
// старыми ключами: ключ убирают из кольца, когда их не осталось (перевыпуск владельцем), и не раньше 7 дней.

const (
	// rowRekeyMfa — метка hooks.afterRowFound: пачка user_mfa прочитана без замка (гонка со сценарием 2FA).
	rowRekeyMfa = "rekey_mfa"
	// rekeyBatch — строк в пачке чтения.
	rekeyBatch = 500
)

// RekeyReport — итог ротации: перешифровано (при DryRun — было бы), строки по key_id до и после (после — nil при
// DryRun), живые резервные коды под ключами, отличными от текущего, и нечитаемые строки (ключа нет в кольце —
// убран раньше rekey — или шифротекст повреждён): id аккаунтов (2FA) и привязок (Apple). Секретов в отчёте нет.
type RekeyReport struct {
	CurrentKey                                   string
	DryRun                                       bool
	MFA, Apple                                   int
	MFABefore, MFAAfter, AppleBefore, AppleAfter map[string]int
	RecoveryOldKeys                              map[string]int
	UnreadableMFA, UnreadableApple               []uuid.UUID
}

// Clean — старые ключи можно убирать из кольца (по сроку — не раньше 7 дней после ротации, docs/deploy-dev.md):
// прогон не пробный, все секреты под текущим ключом, нечитаемых нет и живых резервных кодов под старыми ключами нет.
func (r RekeyReport) Clean() bool {
	onlyCurrent := func(m map[string]int) bool {
		for k, n := range m {
			if k != r.CurrentKey && n > 0 {
				return false
			}
		}
		return true
	}
	return !r.DryRun && onlyCurrent(r.MFAAfter) && onlyCurrent(r.AppleAfter) && len(r.RecoveryOldKeys) == 0 &&
		len(r.UnreadableMFA) == 0 && len(r.UnreadableApple) == 0
}

// Rekey — перешифровка долгоживущих секретов текущим ключом кольца. Строка читается пачкой без замка, открывается и
// запечатывается вне транзакции и пишется условным UPDATE (тот же key_id и тот же шифротекст): строку, которую
// сценарий сменил между чтением и записью (новая настройка TOTP, экземпляр прежнего релиза), команда не перетирает,
// а одиночный UPDATE не держит других замков — цикла с порядком R30 нет. Нечитаемая строка не останавливает прогон
// и не меняется — уходит в отчёт. dryRun — только чтение и пробное открытие. Повтор безопасен.
func (a *App) Rekey(ctx context.Context, dryRun bool) (RekeyReport, error) {
	return a.rekey(ctx, dryRun, rekeyBatch)
}

func (a *App) rekey(ctx context.Context, dryRun bool, batch int) (RekeyReport, error) {
	if a.secrets == nil {
		return RekeyReport{}, errNoSecrets
	}
	rep := RekeyReport{CurrentKey: a.secrets.CurrentID(), DryRun: dryRun}
	q := identitydb.New(a.pool)
	var err error
	if rep.MFABefore, rep.AppleBefore, err = a.rekeyCounts(ctx, q); err != nil {
		return rep, err
	}
	if err := a.rekeyMfa(ctx, q, &rep, batch); err != nil {
		return rep, err
	}
	if err := a.rekeyApple(ctx, q, &rep, batch); err != nil {
		return rep, err
	}
	if !dryRun {
		if rep.MFAAfter, rep.AppleAfter, err = a.rekeyCounts(ctx, q); err != nil {
			return rep, err
		}
	}
	rc, err := q.RekeyRecoveryOldKeyCounts(ctx, rep.CurrentKey)
	if err != nil {
		return rep, fmt.Errorf("identity rekey: резервные коды по ключам: %w", err)
	}
	rep.RecoveryOldKeys = map[string]int{}
	for _, r := range rc {
		rep.RecoveryOldKeys[r.KeyID] = int(r.N)
	}
	return rep, nil
}

// rekeyCounts — строки 2FA и refresh-токенов Apple по key_id.
func (a *App) rekeyCounts(ctx context.Context, q *identitydb.Queries) (mfa, apple map[string]int, err error) {
	mr, err := q.RekeyMfaKeyCounts(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("identity rekey: 2FA по ключам: %w", err)
	}
	ar, err := q.RekeyIdentityKeyCounts(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("identity rekey: привязки по ключам: %w", err)
	}
	mfa, apple = map[string]int{}, map[string]int{}
	for _, r := range mr {
		mfa[r.KeyID] = int(r.N)
	}
	for _, r := range ar {
		apple[r.KeyID] = int(r.N)
	}
	return mfa, apple, nil
}

// rekeyMfa — секреты TOTP не под текущим ключом (aad — id аккаунта), курсором по user_id.
func (a *App) rekeyMfa(ctx context.Context, q *identitydb.Queries, rep *RekeyReport, batch int) error {
	after := uuid.Nil
	for {
		rows, err := q.RekeyMfaBatch(ctx, identitydb.RekeyMfaBatchParams{CurrentKey: rep.CurrentKey, After: after,
			Batch: int32(min(max(batch, 1), rekeyBatch))}) //nolint:gosec // G115: пачка ≤ rekeyBatch
		if err != nil {
			return fmt.Errorf("identity rekey: пачка 2FA: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		a.rowFound(rowRekeyMfa)
		for _, r := range rows {
			after = r.UserID
			ct, kid, err := a.reseal(r.SecretEnc, r.KeyID, r.UserID[:])
			if errors.Is(err, errRekeyUnreadable) {
				rep.UnreadableMFA = append(rep.UnreadableMFA, r.UserID)
				continue
			}
			if err != nil {
				return err
			}
			if rep.DryRun {
				rep.MFA++
				continue
			}
			n, err := q.RekeyMfaUpdate(ctx, identitydb.RekeyMfaUpdateParams{NewEnc: ct, NewKey: kid, UserID: r.UserID,
				OldKey: r.KeyID, OldEnc: r.SecretEnc})
			if err != nil {
				return fmt.Errorf("identity rekey: запись 2FA %s: %w", r.UserID, err)
			}
			rep.MFA += int(n)
		}
	}
}

// rekeyApple — refresh-токены Apple не под текущим ключом (aad — id привязки), курсором по id.
func (a *App) rekeyApple(ctx context.Context, q *identitydb.Queries, rep *RekeyReport, batch int) error {
	after := uuid.Nil
	cur := rep.CurrentKey
	for {
		rows, err := q.RekeyIdentityBatch(ctx, identitydb.RekeyIdentityBatchParams{CurrentKey: &cur, After: after,
			Batch: int32(min(max(batch, 1), rekeyBatch))}) //nolint:gosec // G115: пачка ≤ rekeyBatch
		if err != nil {
			return fmt.Errorf("identity rekey: пачка привязок: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, r := range rows {
			after = r.ID
			ct, kid, err := a.reseal(r.AppleRefreshEnc, r.KeyID, r.ID[:])
			if errors.Is(err, errRekeyUnreadable) {
				rep.UnreadableApple = append(rep.UnreadableApple, r.ID)
				continue
			}
			if err != nil {
				return err
			}
			if rep.DryRun {
				rep.Apple++
				continue
			}
			oldKey := r.KeyID
			n, err := q.RekeyIdentityUpdate(ctx, identitydb.RekeyIdentityUpdateParams{NewEnc: ct, NewKey: &kid, ID: r.ID,
				OldKey: &oldKey, OldEnc: r.AppleRefreshEnc})
			if err != nil {
				return fmt.Errorf("identity rekey: запись привязки %s: %w", r.ID, err)
			}
			rep.Apple += int(n)
		}
	}
}

// errRekeyUnreadable — строка не открылась: ключа нет в кольце (убран раньше rekey) или шифротекст повреждён.
var errRekeyUnreadable = errors.New("identity rekey: строка нечитаема")

// reseal — открыть старым ключом и запечатать текущим; нечитаемая строка — errRekeyUnreadable.
func (a *App) reseal(ct []byte, keyID string, aad []byte) ([]byte, string, error) {
	pt, err := a.secrets.Open(ct, keyID, aad)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", errRekeyUnreadable, err)
	}
	nct, kid, err := a.secrets.Seal(pt, aad)
	clear(pt)
	if err != nil {
		return nil, "", fmt.Errorf("identity rekey: запечатывание: %w", err)
	}
	return nct, kid, nil
}
