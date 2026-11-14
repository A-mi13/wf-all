package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/identity/internal/domain"
)

// mfaToken — токен шага 2FA аккаунта через finishLogin.
func mfaToken(t *testing.T, e *env, nick string) (uuid.UUID, string) {
	t.Helper()
	uid := seedUser(t, e.owner, nick, "ru")
	seedMfa(t, e, uid)
	out, err := finish(t, e, uid, web("x"), false, false)
	if err != nil || out.MfaToken == "" {
		t.Fatalf("%+v %v", out, err)
	}
	return uid, out.MfaToken
}

// take — takeChallenge и, при fail, failChallenge в одной tx (неудача коммитится).
func take(t *testing.T, e *env, raw string, fail bool) (challengeRow, int, error) {
	t.Helper()
	var ch challengeRow
	left := -1
	var takeErr error
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		ch, takeErr = e.a.takeChallenge(ctx, tx, domain.ChallengeMfa, raw)
		if takeErr == nil && fail {
			var err error
			left, err = e.a.failChallenge(ctx, tx, ch)
			return err
		}
		return nil // ошибка takeChallenge — результат теста, tx коммитится
	}); err != nil {
		t.Fatal(err)
	}
	return ch, left, takeErr
}

// 5 попыток на токен шага: пятая неудача гасит — дальше errChallengeGone; истёкший и чужого назначения — тоже.
func TestTakeChallengeAttempts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, raw := mfaToken(t, e, "attempts_user")
	for i := range domain.ChallengeAttempts {
		_, left, err := take(t, e, raw, true)
		if err != nil || left != domain.ChallengeAttempts-1-i {
			t.Fatalf("попытка %d: осталось %d, %v", i+1, left, err)
		}
	}
	if _, _, err := take(t, e, raw, false); !errors.Is(err, errChallengeGone) {
		t.Fatalf("после пятой: %v", err)
	}
	// попытки исчерпаны, а consumed_at нет (защита в глубину: строку правили не failChallenge)
	_, worn := mfaToken(t, e, "worn_user")
	if _, err := e.owner.Exec(context.Background(), `UPDATE auth_challenges SET attempts = 5 WHERE token_hash = $1`,
		domain.HashStepToken(worn)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := take(t, e, worn, false); !errors.Is(err, errChallengeGone) {
		t.Fatalf("attempts = 5 без consumed_at: %v", err)
	}
	if _, _, err := take(t, e, "нет-такого", false); !errors.Is(err, errChallengeGone) {
		t.Fatalf("неизвестный: %v", err)
	}
	_, fresh := mfaToken(t, e, "expiry_user")
	e.clock.Advance(5 * time.Minute)
	if _, _, err := take(t, e, fresh, false); !errors.Is(err, errChallengeGone) {
		t.Fatalf("ровно в срок: %v", err)
	}
	// погашенный успехом
	_, used := mfaToken(t, e, "consumed_user")
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		ch, err := e.a.takeChallenge(ctx, tx, domain.ChallengeMfa, used)
		if err != nil {
			return err
		}
		return e.a.consumeChallenge(ctx, tx, ch.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := take(t, e, used, false); !errors.Is(err, errChallengeGone) {
		t.Fatalf("погашенный: %v", err)
	}
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		_, err := e.a.takeChallenge(ctx, tx, domain.ChallengeDeletionRestore, used)
		return err
	}); !errors.Is(err, errChallengeGone) {
		t.Fatalf("чужое назначение: %v", err)
	}
}

// R3-4, приём lockByRow: takeChallenge встаёт на afterRowFound; второй участник — lockUser → UPDATE той же строки →
// коммит; затем takeChallenge продолжается — ни одного 40P01, видна перепроверенная строка. При FOR UPDATE строки до
// lockUser участник стоит на строке, барьер истекает (2 с), а takeChallenge идёт на users — 40P01.
func TestTakeChallengeLockOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	uid, raw := mfaToken(t, e, "lock_order_ch")
	found := make(chan struct{})
	done := make(chan struct{})
	e.a.hooks = &hooks{afterRowFound: func(kind string) {
		if kind != "challenge" {
			return
		}
		close(found)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		}
	}}
	var wg sync.WaitGroup
	var partErr error
	wg.Go(func() {
		<-found
		defer close(done)
		partErr = e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := e.a.lockUser(ctx, tx, uid); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE auth_challenges SET attempts = attempts + 1 WHERE token_hash = $1`,
				domain.HashStepToken(raw))
			return err
		})
	})
	ch, _, err := take(t, e, raw, false)
	wg.Wait()
	e.a.hooks = nil
	if err != nil || partErr != nil {
		t.Fatalf("takeChallenge: %v (40P01: %v); участник: %v (40P01: %v)", err, isDeadlock(err), partErr, isDeadlock(partErr))
	}
	if ch.Attempts != 1 {
		t.Fatalf("строка не перепроверена под замком: attempts = %d", ch.Attempts)
	}
	// I3 ревью W3: пока tx takeChallenge открыта, users аккаунта под замком lockByRow — замок со второго соединения
	// без ожидания получает 55P03 (пропуск lockUser в lockByRow иначе не виден: участник выше ничему не мешает).
	if err := e.inTx(t, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := e.a.takeChallenge(ctx, tx, domain.ChallengeMfa, raw); err != nil {
			return err
		}
		_, err := e.owner.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, uid)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Errorf("users аккаунта не под замком takeChallenge: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
