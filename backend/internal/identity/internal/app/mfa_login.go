package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/httpx"
)

// Второй шаг входа (спека §3.3 «Вход со второй ступенью») — владелец Task 16.

var (
	errMfaExpired     = httpx.NewError(http.StatusGone, identity.CodeMfaExpired)
	errPasskeyInvalid = httpx.NewError(http.StatusBadRequest, identity.CodePasskeyInvalid)
)

// CompleteMfa — второй шаг входа по mfa_token (анонимная мутация: защита — одноразовый токен, 5 попыток на токен и
// лимит user:mfa — 10 неудач фактора в час). Порядок: устройство мобильного (422) → ровно один способ (400
// validation.failed, без записей) → одна tx на detached (неудача коммитится и при обрыве запроса):
// takeChallenge(mfa) приёмом R30 (users первым, затем строка токена; нет, истёк, погашен — 410 mfa_expired) →
// аккаунт забанен — токен гасится и коммитится, 410 до проверки фактора (R29: повторы не крутят user_mfa.failures и
// письма mfa_failures) → клиент не тот, что на первом шаге (stepClientMatches) — 410 без записей → 2FA у аккаунта
// больше нет (сброс исполнен между шагами) — токен гасится, 410 → лимит user:mfa на соединении tx (429, попытка не тратится) → passkey в I-2 — неудача (400
// passkey_invalid, R3) → verifySecondFactor. Неудача: учёт в user:mfa, failChallenge — пятая гасит (410), иначе 400
// mfa_invalid + attempts_left. Успех: токен гасится, устройство запроса, finishLogin(mfaDone, способ первого шага из
// токена) — сессия с mfa_at = now или deletion_pending; documents_acceptance_required — в этой же tx.
//
// Порядок замков: строка auth_challenges берётся раньше user_mfa и devices (аккаунт известен только из токена) —
// то же исключение, что у кодов (app.go): всё под замком users аккаунта, взаимной блокировки нет.
func (a *App) CompleteMfa(ctx context.Context, raw string, f SecondFactor, passkey *PasskeyInput, c Client) (AuthOutcome, error) {
	if err := a.requireDevice(c); err != nil {
		return AuthOutcome{}, err
	}
	n := 0
	for _, set := range []bool{f.TOTP != "", f.Recovery != "", passkey != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return AuthOutcome{}, errFactorShape
	}
	var out AuthOutcome
	err := a.inTxCommitFailure(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ch, err := a.takeChallenge(ctx, tx, domain.ChallengeMfa, raw)
		if errors.Is(err, errChallengeGone) {
			return errMfaExpired
		}
		if err != nil {
			return err
		}
		if ch.UserID == nil {
			return errMfaExpired // токен шага 2FA всегда с аккаунтом; защита в глубину
		}
		uid := *ch.UserID
		u, err := a.lockUser(ctx, tx, uid) // уже под замком takeChallenge — без ожидания
		if errors.Is(err, errUserGone) {
			return errMfaExpired
		}
		if err != nil {
			return err
		}
		if u.Status == identitydb.UserStatusDeleted {
			return errMfaExpired
		}
		// R36: banned или бан поверх периода удаления — токен гаснет, 410 (account_blocked в контракте нет, R29)
		if blocked, err := a.blocked(ctx, identitydb.New(tx), u, a.clock.Now()); err != nil {
			return err
		} else if blocked {
			if err := a.consumeChallenge(ctx, tx, ch.ID); err != nil {
				return fmt.Errorf("identity: погашение mfa_token: %w", err)
			}
			return failureToCommit{errMfaExpired}
		}
		var data challengeData
		if err := json.Unmarshal(ch.Data, &data); err != nil {
			return fmt.Errorf("identity: данные mfa_token: %w", err)
		}
		if same, err := a.stepClientMatches(ctx, tx, data, c); err != nil {
			return err
		} else if !same {
			return errMfaExpired // без записей: токен остаётся у клиента первого шага
		}
		on, err := identitydb.New(tx).MfaConfirmed(ctx, uid)
		if err != nil {
			return fmt.Errorf("identity: 2FA: %w", err)
		}
		if !on {
			// 2FA снята между шагами (сброс поддержкой исполнен, выключение): проверять нечего — токен гасится, клиент
			// входит заново и проходит без второго шага; неудачей фактора не считается
			if err := a.consumeChallenge(ctx, tx, ch.ID); err != nil {
				return fmt.Errorf("identity: погашение mfa_token: %w", err)
			}
			return failureToCommit{errMfaExpired}
		}
		limiter := a.limiterTx(tx)
		over, err := limiter.Over(ctx, mfaFailureKey(uid), mfaFailurePolicy)
		if err != nil {
			return fmt.Errorf("identity: лимит второго фактора: %w", err)
		}
		if over {
			return errRateLimited.WithRetryAfter(mfaFailurePolicy.Period / time.Duration(mfaFailurePolicy.Limit))
		}
		fail := errMfaInvalid
		if passkey != nil {
			err = errFactorInvalid // ключей доступа в I-2 нет (R3): ответ как у будущего «неизвестный ключ»
			fail = errPasskeyInvalid
		} else {
			_, err = a.verifySecondFactor(ctx, tx, uid, f)
		}
		if errors.Is(err, errFactorInvalid) {
			if err := limiter.Add(ctx, mfaFailureKey(uid), mfaFailurePolicy); err != nil {
				return fmt.Errorf("identity: учёт неудачи второго фактора: %w", err)
			}
			left, err := a.failChallenge(ctx, tx, ch)
			if err != nil {
				return err
			}
			if left == 0 {
				return failureToCommit{errMfaExpired}
			}
			return failureToCommit{fail.WithAttemptsLeft(left)}
		}
		if err != nil {
			return err
		}
		if err := a.consumeChallenge(ctx, tx, ch.ID); err != nil {
			return fmt.Errorf("identity: погашение mfa_token: %w", err)
		}
		dev, err := a.upsertDevice(ctx, tx, c)
		if err != nil {
			return err
		}
		out = AuthOutcome{}
		out.LoginOutcome, err = a.finishLogin(ctx, tx, u, c, dev, data.Method, true, true)
		if errors.Is(err, errBlockedAsExpired) || errors.Is(err, errAccountDeleted) {
			return errMfaExpired
		}
		if err != nil {
			return err
		}
		if out.Status == StatusAuthenticated {
			out.DocumentsRequired, err = a.documentsRequired(ctx, identitydb.New(tx), uid)
		}
		return err
	})
	if err != nil {
		return AuthOutcome{}, err
	}
	return out, nil
}

// stepClientMatches — запрос второго шага (completeMfa, cancelDeletion) пришёл с клиента первого шага (m2 ревью W5-А):
// тот же вид клиента, у мобильного — то же устройство (строка devices по HMAC install_id всеми ключами кольца, как в
// upsertDevice, но без записи; devices — после users и строки токена, раньше user_mfa — R30). Веб сверяется только
// видом: метка BFF — не устройство. Несовпадение — токен недействителен для этого клиента: вызывающий отвечает
// «истёк» без записей — попытка не тратится и фактор не проверяется (нет оракула кода на чужом клиенте), токен
// остаётся у клиента первого шага.
func (a *App) stepClientMatches(ctx context.Context, tx pgx.Tx, data challengeData, c Client) (bool, error) {
	if data.ClientKind != c.Kind {
		return false, nil
	}
	if c.Kind == domain.ClientWeb {
		return true, nil
	}
	platform, installID, _, _, _, ok := deviceOf(c)
	if !ok || data.DeviceID == nil {
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
	row, err := identitydb.New(tx).FindDeviceForUpdate(ctx, identitydb.FindDeviceForUpdateParams{Platform: platform,
		Macs: macs, CurrentKey: ids[0]})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: устройство: %w", err)
	}
	return row.ID == *data.DeviceID, nil
}
