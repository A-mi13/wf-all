package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/id"
)

// Токены шагов (mfa_token, restore_token; спека §3.3, §3.11): 256 бит, в базе только SHA-256, одноразовые,
// domain.ChallengeAttempts попыток.

// challengeRow — строка auth_challenges под замком.
type challengeRow = identitydb.AuthChallenge

// errChallengeGone — токена шага нет, он истёк, погашен или исчерпал попытки (ответ «истёк» операции).
var errChallengeGone = errors.New("identity: токен шага недействителен")

// newChallenge — токен шага аккаунта: data — несекретные данные первого шага (способ, устройство, вид клиента).
func (a *App) newChallenge(ctx context.Context, tx pgx.Tx, purpose domain.ChallengePurpose, userID uuid.UUID, data any) (raw string, expires time.Time, err error) {
	ttl, ok := domain.ChallengeTTL(purpose)
	if !ok {
		return "", time.Time{}, fmt.Errorf("identity: у назначения %q нет срока", purpose)
	}
	raw, hash, err := domain.NewStepToken()
	if err != nil {
		return "", time.Time{}, err
	}
	body, err := json.Marshal(data)
	if err != nil {
		return "", time.Time{}, err
	}
	now := a.clock.Now()
	expires = now.Add(ttl)
	if err := identitydb.New(tx).InsertChallenge(ctx, identitydb.InsertChallengeParams{ID: id.New(),
		Purpose: identitydb.AuthChallengePurpose(purpose), TokenHash: hash, UserID: &userID, Data: body,
		ExpiresAt: expires, Now: now}); err != nil {
		return "", time.Time{}, fmt.Errorf("identity: токен шага: %w", err)
	}
	return raw, expires, nil
}

// takeChallenge — токен шага по SHA-256 приёмом R30 (N1): поиск без замка → hooks.afterRowFound("challenge") →
// lockUser(user_id) → FOR UPDATE строки и перепроверка; нет, истёк (now ≥ expires_at), погашен или попытки
// исчерпаны — errChallengeGone. Строка не гасится: успех — consumeChallenge, неудача — failChallenge.
func (a *App) takeChallenge(ctx context.Context, tx pgx.Tx, purpose domain.ChallengePurpose, raw string) (challengeRow, error) {
	var chID uuid.UUID
	var ch challengeRow
	err := a.lockByRow(ctx, tx, "challenge",
		func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
			row, err := q.FindChallenge(ctx, identitydb.FindChallengeParams{TokenHash: domain.HashStepToken(raw),
				Purpose: identitydb.AuthChallengePurpose(purpose)})
			chID = row.ID
			return row.UserID, err
		},
		func(ctx context.Context, q *identitydb.Queries) (*uuid.UUID, error) {
			var err error
			ch, err = q.LockChallenge(ctx, chID)
			return ch.UserID, err
		})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, errRowGone) {
		return challengeRow{}, errChallengeGone
	}
	if err != nil {
		return challengeRow{}, err
	}
	if ch.ConsumedAt != nil || !a.clock.Now().Before(ch.ExpiresAt) || ch.Attempts >= domain.ChallengeAttempts {
		return challengeRow{}, errChallengeGone
	}
	return ch, nil
}

// failChallenge — неудача по токену шага (строка под замком takeChallenge): attempts + 1, пятая гасит. Вызывающий
// коммитит неудачу до ответа ошибкой.
func (a *App) failChallenge(ctx context.Context, tx pgx.Tx, ch challengeRow) (attemptsLeft int, err error) {
	now := a.clock.Now()
	n, err := identitydb.New(tx).FailChallenge(ctx, identitydb.FailChallengeParams{MaxAttempts: domain.ChallengeAttempts,
		Now: &now, ID: ch.ID})
	if err != nil {
		return 0, fmt.Errorf("identity: неудача токена шага: %w", err)
	}
	return max(0, domain.ChallengeAttempts-int(n)), nil
}

// consumeChallenge — погашение токена шага после успеха.
func (a *App) consumeChallenge(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	now := a.clock.Now()
	return identitydb.New(tx).ConsumeChallenge(ctx, identitydb.ConsumeChallengeParams{Now: &now, ID: id})
}

// revokeChallenges — гасит все живые токены шагов аккаунта (users под замком вызывающего): смена и сброс пароля, «выйти
// везде» — недоведённый вход вторым фактором (mfa_token) и отмена удаления (restore_token) первого шага старыми
// учётными данными не доходят до сессии (ревью W5-А, непредусмотренное 1).
func (a *App) revokeChallenges(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	now := a.clock.Now()
	if err := identitydb.New(tx).RevokeUserChallenges(ctx, identitydb.RevokeUserChallengesParams{Now: &now,
		UserID: &userID}); err != nil {
		return fmt.Errorf("identity: токены шагов: %w", err)
	}
	return nil
}
