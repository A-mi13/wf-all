package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/identity"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/events"
)

// ChangeCityInTx — смена текущего города игрока в транзакции вызывающего (спека §10.2; R8, R39; вызывающий — teams):
// lockUser (users первым, R30); нет аккаунта — identity.ErrUserNotFound, deleted — ErrUserDeleted. Город — по
// экспортированному представлению geo_read_city_settings на соединении tx (C1: второе соединение пула под замком users
// не берём): нет города или страна выключена — ErrCityNotFound. Тот же город — ничего. Иначе users.city_id, версия + 1,
// событие city_changed, аудит identity.city_changed (автор — игрок: смену города просит он сам); открытые заявки не в
// новом городе — withdrawn с событием и письмом captain_application_withdrawn_city (паузы нет — она только от
// rejected; допуски прежнего города не трогаются — судьба команды решается в teams).
func (a *App) ChangeCityInTx(ctx context.Context, tx pgx.Tx, userID, cityID uuid.UUID) error {
	u, err := a.lockUser(ctx, tx, userID)
	if errors.Is(err, errUserGone) {
		return identity.ErrUserNotFound
	}
	if err != nil {
		return err
	}
	if u.Status == identitydb.UserStatusDeleted {
		return ErrUserDeleted
	}
	q := identitydb.New(tx)
	city, err := q.CaptaincyCity(ctx, cityID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !city.CountryEnabled) {
		return ErrCityNotFound
	}
	if err != nil {
		return fmt.Errorf("identity: город: %w", err)
	}
	// города не было (users.city_id NULL) — from = uuid.Nil: payload v1 city_changed — uuid без null (golden Task 2),
	// «нулевой uuid = города нет» — как getMe и getMyCaptaincy (m8 ревью T7)
	var from uuid.UUID
	if u.CityID != nil {
		from = *u.CityID
	}
	if from == cityID {
		return nil
	}
	ver, err := q.CityChangeUser(ctx, identitydb.CityChangeUserParams{CityID: cityID, ID: u.ID})
	if err != nil {
		return fmt.Errorf("identity: смена города: %w", err)
	}
	if _, err := events.Publish(ctx, identity.CityChanged(identity.UserRef{ID: u.ID, Version: ver}, from, cityID)); err != nil {
		return err
	}
	if err := a.auditUser(ctx, ActionCityChanged, u.ID, map[string]any{"city_id": from},
		map[string]any{"city_id": cityID}); err != nil {
		return err
	}
	now := a.clock.Now()
	apps, err := q.CityWithdrawApplications(ctx, identitydb.CityWithdrawApplicationsParams{Now: &now, UserID: u.ID,
		CityID: cityID})
	if err != nil {
		return fmt.Errorf("identity: заявка в капитаны прежнего города: %w", err)
	}
	for _, ap := range apps {
		if _, err := events.Publish(ctx, identity.CaptainApplicationWithdrawn(
			identity.ApplicationRef{ID: ap.ID, Version: ap.Version}, u.ID, ap.CityID)); err != nil {
			return err
		}
		if err := a.enqueueMail(ctx, tx, MailCaptainApplicationWithdrawnCity, ap.ID); err != nil {
			return err
		}
	}
	return nil
}
