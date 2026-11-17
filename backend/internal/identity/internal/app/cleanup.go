package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
)

// Чистка identity (спека §5.10 в части I-2; план I-2, Task 19). Исполняет задача identity.maintenance воркера
// (NewMaintenance под ролью worker, права §5.5). Анонимизация и исполнение сброса 2FA — свои задачи (anonymize.go,
// mfa_reset_exec.go). Отзыв токенов Apple и DeviceCheck — I-3.

// cleanupBatch — строк (аккаунтов) в одной транзакции чистки.
const cleanupBatch = 1000

// Сроки §5.10.
const (
	cleanupCodesAfter    = 24 * time.Hour       // auth_codes, auth_challenges — после истечения или закрытия
	cleanupSessionsAfter = 30 * 24 * time.Hour  // отозванные и истёкшие сессии
	cleanupSecurityAfter = 180 * 24 * time.Hour // журнал безопасности
	cleanupMetaAfter     = 7 * 24 * time.Hour   // адрес для писем в журнале (meta_enc)
)

// errCleanupRowsFailed — часть аккаунтов (снятие ограничений, сверка удаления) не обработана: каждый в журнале; задача завершается
// ошибкой (видна, River повторит), обработанное закоммичено — как у анонимизации и сброса 2FA.
var errCleanupRowsFailed = errors.New("identity: чистка — часть аккаунтов не обработана")

// CleanupReport — сколько строк вычищено по видам (ключи — имена таблиц и видов чистки, без ПД).
type CleanupReport struct {
	Rows map[string]int
}

// Cleanup — чистка identity пачками: каждая пачка — своя короткая транзакция; повтор безопасен. Граница сроков —
// строго «срок прошёл»: ровно в срок строка остаётся до следующего запуска (раз в 10 минут).
func (a *App) Cleanup(ctx context.Context) (CleanupReport, error) {
	return a.cleanup(ctx, cleanupBatch)
}

// cleanupStep — одна пачка вида: число затронутых строк.
type cleanupStep struct {
	name string
	run  func(ctx context.Context, q *identitydb.Queries, now time.Time, batch int32) (int64, error)
	// once — без пачки (запрос удаляет всё сразу): повторять не нужно
	once bool
	// noTx — пачка вне транзакции (q на пуле): каждый оператор шага — своя транзакция (auth_codes, m2 ревью R1W6)
	noTx bool
}

// cleanup — шаги независимы (m3 ревью R1W6): ошибка шага не прерывает остальные — она в итоговой (errors.Join), отчёт
// по успевшим; прерывает только отмена ctx.
func (a *App) cleanup(ctx context.Context, batch int) (CleanupReport, error) {
	rep := CleanupReport{Rows: map[string]int{}}
	now := a.clock.Now()
	b := int32(min(max(batch, 1), cleanupBatch)) //nolint:gosec // G115: пачка ≤ cleanupBatch
	var errs []error
	for _, s := range cleanupSteps() {
		n, err := a.cleanupStep(ctx, s, now, b)
		rep.Rows[s.name] = n
		if err != nil {
			errs = append(errs, fmt.Errorf("identity: чистка %s: %w", s.name, err))
			if ctx.Err() != nil {
				return rep, errors.Join(errs...)
			}
		}
	}
	failed := 0
	for _, acc := range []struct {
		name string
		run  func(context.Context) (done, failed int, err error)
	}{
		{"user_restrictions", func(ctx context.Context) (int, int, error) { return a.liftExpiredRestrictions(ctx, now, b) }},
		{"deleted_leftovers", func(ctx context.Context) (int, int, error) { return a.scrubDeleted(ctx, b) }},
	} {
		done, f, err := acc.run(ctx)
		rep.Rows[acc.name] = done
		failed += f
		if err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				return rep, errors.Join(errs...)
			}
		}
	}
	if failed > 0 {
		errs = append(errs, fmt.Errorf("%w: %d", errCleanupRowsFailed, failed))
	}
	return rep, errors.Join(errs...)
}

// cleanupStep — пачки одного вида до исчерпания, каждая — своя короткая транзакция; n — затронуто строк.
func (a *App) cleanupStep(ctx context.Context, s cleanupStep, now time.Time, b int32) (int, error) {
	total := 0
	for {
		var (
			n   int64
			err error
		)
		if s.noTx {
			n, err = s.run(ctx, identitydb.New(a.pool), now, b)
		} else {
			err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				n, err = s.run(ctx, identitydb.New(tx), now, b)
				return err
			})
		}
		if err != nil {
			return total, err
		}
		total += int(n)
		if s.once || n < int64(b) {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// cleanupSteps — удаление и стирание по срокам §5.10.
func cleanupSteps() []cleanupStep {
	return []cleanupStep{
		// коды — по одному, каждый своим оператором вне tx (m2 ревью R1W6): ожидая код под замком сценария
		// (RevokeUserCodes, DeletionRevokeCodes), чистка не держит замков других кодов — цикла и 40P01 нет
		{name: "auth_codes", noTx: true, run: func(ctx context.Context, q *identitydb.Queries, now time.Time, b int32) (int64, error) {
			cutoff := now.Add(-cleanupCodesAfter)
			ids, err := q.CleanupAuthCodesDue(ctx, identitydb.CleanupAuthCodesDueParams{Cutoff: cutoff, Batch: b})
			if err != nil {
				return 0, err
			}
			var n int64
			for _, id := range ids {
				d, err := q.CleanupAuthCode(ctx, identitydb.CleanupAuthCodeParams{ID: id, Cutoff: cutoff})
				if err != nil {
					return n, err
				}
				n += d
			}
			return n, nil
		}},
		{name: "auth_challenges", once: true, run: func(ctx context.Context, q *identitydb.Queries, now time.Time, _ int32) (int64, error) {
			return q.CleanupAuthChallenges(ctx, now.Add(-cleanupCodesAfter))
		}},
		{name: "refresh_successors", run: func(ctx context.Context, q *identitydb.Queries, now time.Time, b int32) (int64, error) {
			return q.CleanupRefreshSuccessors(ctx, identitydb.CleanupRefreshSuccessorsParams{Now: &now, Batch: b})
		}},
		{name: "sessions", run: func(ctx context.Context, q *identitydb.Queries, now time.Time, b int32) (int64, error) {
			cutoff := now.Add(-cleanupSessionsAfter)
			return q.CleanupSessions(ctx, identitydb.CleanupSessionsParams{Cutoff: &cutoff, Batch: b})
		}},
		{name: "security_events", run: func(ctx context.Context, q *identitydb.Queries, now time.Time, b int32) (int64, error) {
			return q.CleanupSecurityEvents(ctx, identitydb.CleanupSecurityEventsParams{Cutoff: now.Add(-cleanupSecurityAfter), Batch: b})
		}},
		{name: "security_meta", run: func(ctx context.Context, q *identitydb.Queries, now time.Time, b int32) (int64, error) {
			return q.CleanupSecurityMeta(ctx, identitydb.CleanupSecurityMetaParams{Cutoff: now.Add(-cleanupMetaAfter), Batch: b})
		}},
		// карантины ников — по одному вне tx, как коды (m-r1 ревью R1W6): смена ника берёт замки Y → X, пачка одним
		// DELETE — в порядке ника; чистка, ждущая Y, не держит X — цикла и 40P01 нет
		{name: "nickname_reservations", noTx: true, run: func(ctx context.Context, q *identitydb.Queries, now time.Time, b int32) (int64, error) {
			nicks, err := q.CleanupNicknameReservationsDue(ctx, identitydb.CleanupNicknameReservationsDueParams{Now: now, Batch: b})
			if err != nil {
				return 0, err
			}
			var n int64
			for _, nick := range nicks {
				d, err := q.CleanupNicknameReservation(ctx, identitydb.CleanupNicknameReservationParams{NicknameNormalized: nick, Now: now})
				if err != nil {
					return n, err
				}
				n += d
			}
			return n, nil
		}},
	}
}

// liftExpiredRestrictions — истёкшие ограничения (until ≤ now) снимаются по аккаунтам: на аккаунт — своя tx, users
// первым FOR NO KEY UPDATE SKIP LOCKED (R30; занят сценарием — пропуск до следующего запуска), lifted_at, версия
// аккаунта + 1 и событие identity.restrictions_changed (статус и оставшиеся закрытые возможности) в той же tx; снята
// последняя живая login у забаненного — статус active и аудит identity.ban_expired (R12); у
// удалённого (deleted) — только lifted_at, без версии и события.
// Упавший аккаунт — в журнал и позади курсора до конца прогона (не заслоняет остальных); failed — их число.
func (a *App) liftExpiredRestrictions(ctx context.Context, now time.Time, batch int32) (lifted, failed int, err error) {
	return a.eachAccount(ctx, "снятие истёкших ограничений", batch,
		func(ctx context.Context, after uuid.UUID) ([]uuid.UUID, error) {
			return identitydb.New(a.pool).CleanupRestrictionsDue(ctx,
				identitydb.CleanupRestrictionsDueParams{Now: &now, After: after, Batch: batch})
		},
		func(ctx context.Context, id uuid.UUID) (bool, error) { return a.liftRestrictions(ctx, id, now) })
}

// eachAccount — пачки аккаунтов по возрастанию id (due — без замка, after — курсор: id последнего обработанного, занятого
// или упавшего в этом прогоне; m-r3 — вместо растущего списка пропусков), на аккаунт — do своей tx (ok = false — занят
// или делать уже нечего). Упавший аккаунт — в журнал (id и ошибка, без ПД) и позади курсора: не откатывает и не
// заслоняет остальных, повтор — следующим запуском; failed — их число, итог решает вызывающий (errCleanupRowsFailed).
func (a *App) eachAccount(ctx context.Context, what string, batch int32,
	due func(ctx context.Context, after uuid.UUID) ([]uuid.UUID, error),
	do func(ctx context.Context, id uuid.UUID) (bool, error),
) (done, failed int, err error) {
	var after uuid.UUID // uuid.Nil — с начала: id аккаунтов (v7) больше
	for {
		ids, err := due(ctx, after)
		if err != nil {
			return done, failed, fmt.Errorf("identity: %s: %w", what, err)
		}
		for _, id := range ids {
			ok, err := do(ctx, id)
			switch {
			case err != nil && ctx.Err() != nil:
				return done, failed, errors.Join(ctx.Err(), err)
			case err != nil:
				a.log.ErrorContext(ctx, "identity: "+what+" не удалось — аккаунт пропущен до следующего запуска",
					"user_id", id.String(), "err", err)
				failed++
			case ok:
				done++
			}
			after = id // обработан, занят (следующий запуск) или упал
		}
		if len(ids) < int(batch) {
			return done, failed, nil
		}
		if err := ctx.Err(); err != nil {
			return done, failed, err
		}
	}
}

// liftRestrictions — одна tx на аккаунт; ok = false — аккаунт занят или снимать уже нечего.
func (a *App) liftRestrictions(ctx context.Context, userID uuid.UUID, now time.Time) (ok bool, err error) {
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		ok = false
		q := identitydb.New(tx)
		u, err := q.CleanupLockUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: замок аккаунта: %w", err)
		}
		caps, err := q.CleanupLiftRestrictions(ctx, identitydb.CleanupLiftRestrictionsParams{Now: &now, UserID: userID})
		if err != nil {
			return fmt.Errorf("identity: снятие ограничений: %w", err)
		}
		if len(caps) == 0 {
			return nil // сняли раньше (параллельный запуск)
		}
		// удалённый аккаунт (анонимизация user_restrictions не стирает): снято молча — без версии и события
		// restrictions_changed, аккаунта для подписчиков больше нет (m5 ревью 4); deleted — конечный статус
		if u.Status == statusDeleted {
			ok = true
			return nil
		}
		live, err := q.CleanupLiveCapabilities(ctx, identitydb.CleanupLiveCapabilitiesParams{UserID: userID, Now: &now})
		if err != nil {
			return fmt.Errorf("identity: действующие ограничения: %w", err)
		}
		// R12: снята последняя живая строка login у забаненного — expireBanTx (статус, событие, аудит); иначе — версия
		// и событие
		ok = true
		if done, err := a.expireBanTx(ctx, q, userID, u.Status, u.StatusBeforeDeletion, live); err != nil || done {
			return err
		}
		ver, err := q.BumpUserVersion(ctx, userID)
		if err != nil {
			return fmt.Errorf("identity: версия аккаунта: %w", err)
		}
		_, err = events.Publish(ctx, identity.RestrictionsChanged(identity.UserRef{ID: userID, Version: ver}, u.Status, live))
		return err
	})
	return ok, err
}

// scrubDeleted — сверка удаления (спека бэкенда §6.5): аккаунты deleted с недочищенными секретами, привязками,
// согласиями, кодами, сессиями или историей ника — по аккаунту своей tx (I2 ревью R1W6: упавший не откатывает пачку и
// не заслоняет остальных), users первым FOR NO KEY UPDATE SKIP LOCKED (R30; занят — следующий запуск). done —
// дочищенных аккаунтов, failed — упавших.
func (a *App) scrubDeleted(ctx context.Context, batch int32) (done, failed int, err error) {
	return a.eachAccount(ctx, "сверка удаления", batch,
		func(ctx context.Context, after uuid.UUID) ([]uuid.UUID, error) {
			return identitydb.New(a.pool).CleanupDeletedLeftovers(ctx,
				identitydb.CleanupDeletedLeftoversParams{After: after, Batch: batch})
		},
		a.scrubAccount)
}

// scrubAccount — одна tx на аккаунт; ok = false — аккаунт занят.
func (a *App) scrubAccount(ctx context.Context, userID uuid.UUID) (ok bool, err error) {
	err = db.InTx(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		ok = false
		q := identitydb.New(tx)
		// статус не перепроверяется: deleted — конечный (выборка CleanupDeletedLeftovers его уже видела)
		_, err := q.CleanupLockUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: замок аккаунта: %w", err)
		}
		for _, del := range []func(context.Context, uuid.UUID) error{
			q.CleanupDeletedCredentials, q.CleanupDeletedMfa, q.CleanupDeletedRecoveryCodes, q.CleanupDeletedPasskeys,
			q.CleanupDeletedIdentities, q.CleanupDeletedConsents, q.CleanupDeletedCodes, q.CleanupDeletedSessions,
			q.CleanupDeletedNicknameChanges,
		} {
			if err := del(ctx, userID); err != nil {
				return fmt.Errorf("identity: сверка удаления: %w", err)
			}
		}
		ok = true
		return nil
	})
	return ok, err
}
