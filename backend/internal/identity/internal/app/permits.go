package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/events"
)

// Отзыв допуска капитана (план I-4, Task 7; спека §10.2; R24, R25): RevokeRoleInTx для identity/intx (teams,
// moderation) и анонимизация — одним путём revokePermitsTx. Агрегат события — заявка допуска (К2).

// maxRevokeReason — revoke_reason ≤ 500 символов (CHECK role_assignments_revoke_reason_check).
const maxRevokeReason = 500

// revokeReasonAccountDeleted — причина отзыва допусков анонимизацией.
const revokeReasonAccountDeleted = "account_deleted"

// errPermitWithoutApplication — допуск без application_id (R25: после 0025 такой строки быть не может; агрегата для
// события нет).
var errPermitWithoutApplication = errors.New("identity: допуск капитана без заявки — нарушен инвариант R25")

// RevokeBy — кто отзывает: сотрудник (StaffID, Label — подпись для аудита вызывающего) или система (System).
type RevokeBy struct {
	StaffID uuid.UUID
	Label   string
	System  bool
}

// RevokeRoleInTx — отзыв живого допуска капитана игрока в городе в транзакции вызывающего: revoked_at,
// revoked_by_staff_id (у системы — NULL), revoke_reason (≤ 500); версия заявки допуска + 1 и событие
// captain_permit_revoked на агрегате captain_application. Нет допуска (или отозван параллельно) — ErrPermitNotFound;
// допуск без заявки — ошибка инварианта (R25). Аудит пишет вызывающий (его действие).
func (a *App) RevokeRoleInTx(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID, by RevokeBy, reason string) error {
	if by.System == (by.StaffID != uuid.Nil) {
		return errors.New("identity: отзыв допуска — ровно один автор: сотрудник или система")
	}
	if utf8.RuneCountInString(reason) > maxRevokeReason {
		return fmt.Errorf("identity: причина отзыва длиннее %d символов", maxRevokeReason)
	}
	n, err := a.revokePermitsTx(ctx, tx, userID, &cityID, by, reason, true)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrPermitNotFound
	}
	return nil
}

// revokePermitsTx — отзыв живых допусков игрока (cityID nil — во всех городах) с событием на каждый: порядок замков
// R7 — строка заявки допуска FOR UPDATE, затем строка допуска с перепроверкой revoked_at (отозванный параллельно не
// считается). strict — допуск без заявки — ошибка (RevokeRoleInTx); иначе (анонимизация) он отзывается без события:
// инвариант R25 не должен останавливать удаление аккаунта. n — отозвано допусков.
func (a *App) revokePermitsTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, cityID *uuid.UUID, by RevokeBy,
	reason string, strict bool) (n int, err error) {
	q := identitydb.New(tx)
	rows, err := q.PermitsLive(ctx, identitydb.PermitsLiveParams{UserID: userID, CityID: cityID})
	if err != nil {
		return 0, fmt.Errorf("identity: допуски капитана: %w", err)
	}
	now := a.clock.Now()
	var staff *uuid.UUID
	if !by.System {
		staff = &by.StaffID
	}
	// пустая причина (и из одних пробелов) — NULL «без причины», не '' (m2 итогового ревью В)
	var why *string
	if strings.TrimSpace(reason) != "" {
		why = &reason
	}
	for _, r := range rows {
		if r.CityID == nil {
			continue // CHECK role_scope_id_present: у городского допуска город есть
		}
		if r.ApplicationID == nil {
			if strict {
				return n, errPermitWithoutApplication
			}
			if _, err := q.PermitRevokeOrphan(ctx, identitydb.PermitRevokeOrphanParams{Now: &now, StaffID: staff,
				Reason: why, UserID: userID, CityID: *r.CityID}); err != nil {
				return n, fmt.Errorf("identity: отзыв допуска: %w", err)
			}
			a.log.WarnContext(ctx, "identity: допуск капитана без заявки отозван без события (R25)",
				"user_id", userID.String())
			continue
		}
		app := *r.ApplicationID
		if _, err := q.PermitLockApplication(ctx, app); err != nil {
			return n, fmt.Errorf("identity: заявка допуска: %w", err)
		}
		revoked, err := q.PermitRevoke(ctx, identitydb.PermitRevokeParams{Now: &now, StaffID: staff, Reason: why,
			UserID: userID, CityID: *r.CityID, ApplicationID: app})
		if err != nil {
			return n, fmt.Errorf("identity: отзыв допуска: %w", err)
		}
		if revoked == 0 {
			continue // отозван параллельно (READ COMMITTED перепроверил revoked_at)
		}
		ver, err := q.PermitBumpApplication(ctx, app)
		if err != nil {
			return n, fmt.Errorf("identity: версия заявки допуска: %w", err)
		}
		if _, err := events.Publish(ctx, identity.CaptainPermitRevoked(identity.ApplicationRef{ID: app, Version: ver},
			userID, *r.CityID)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
