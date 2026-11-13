package app

// Районы города (createGeoDistrict, updateGeoDistrict, Task 12): спека geo §3.2, §5.2, спека сотрудников §6.1.
// Районы не удаляются — архивируются (на них ссылаются поля и вызовы других модулей).

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"wf/backend/internal/geo"
	"wf/backend/internal/geo/internal/domain"
	"wf/backend/internal/geo/internal/store/geodb"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
)

const (
	permDistrictsManage = "geo.districts.manage"

	actionDistrictCreated = "geo.district_created"
	actionDistrictUpdated = "geo.district_updated"
	objectDistrict        = "district"

	districtNameKey = "districts_city_name_active_key"
)

// DistrictPatch — правка района: только присланные поля (Archived: true — в архив, false — из архива).
type DistrictPatch struct {
	Name     *string
	Archived *bool
}

// districtName — название из запроса в виде для записи; не проходит CleanName — 400 по body.name.
func districtName(raw string) (string, error) {
	name, ok := domain.CleanName(raw)
	if !ok {
		return "", httpx.NewFieldError("body.name", nameRule(raw))
	}
	return name, nil
}

// nameTaken — 23505 по действующему имени района города — geo.ErrDistrictNameTaken; иначе — как есть.
func nameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation &&
		pgErr.ConstraintName == districtNameKey {
		return geo.ErrDistrictNameTaken
	}
	return err
}

func districtOf(id uuid.UUID, name string, archivedAt *time.Time, version int64) geo.AdminDistrict {
	return geo.AdminDistrict{ID: id, Name: name, ArchivedAt: archivedAt, Version: version}
}

// CreateDistrict — POST /v1/geo/cities/{cityId}/districts одной транзакцией: право geo.districts.manage в городе
// (конвейер проверил, здесь — защита в глубину), имя — CleanName; нет города — geo.ErrCityNotFound; имя занято
// действующим районом города (23505 districts_city_name_active_key — и при гонке) — geo.ErrDistrictNameTaken; событие
// geo.district_created (версия 1) и аудит — в той же транзакции.
func (a *Admin) CreateDistrict(ctx context.Context, s *auth.Staff, cityID uuid.UUID, name string, m approvals.Meta) (geo.AdminDistrict, error) {
	if err := a.authorize(ctx, s, cityID, permDistrictsManage); err != nil {
		return geo.AdminDistrict{}, err
	}
	clean, err := districtName(name)
	if err != nil {
		return geo.AdminDistrict{}, err
	}
	now := a.d.Clock.Now()
	var out geo.AdminDistrict
	err = db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		found, err := q.AdminCityExists(ctx, cityID)
		if err != nil {
			return err
		}
		if !found {
			return geo.ErrCityNotFound
		}
		a.at(ctx, "district")
		d, err := q.InsertDistrict(ctx, geodb.InsertDistrictParams{ID: id.New(), CityID: cityID, Name: clean, CreatedAt: now})
		if err != nil {
			return nameTaken(err)
		}
		if _, err := events.Publish(ctx, geo.DistrictEvent(geo.EventDistrictCreated, d.ID, cityID, d.Version)); err != nil {
			return err
		}
		if err := audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionDistrictCreated,
			ObjectType: objectDistrict, ObjectID: d.ID, ScopeCityID: cityID, After: map[string]any{"name": d.Name},
			IP: m.IP, UserAgent: m.UserAgent,
		}); err != nil {
			return err
		}
		out = districtOf(d.ID, d.Name, d.ArchivedAt, d.Version)
		return nil
	})
	return out, err
}

// UpdateDistrict — PATCH /v1/geo/cities/{cityId}/districts/{districtId} одной транзакцией: право в городе пути;
// район — строго этого города (чужой — geo.ErrDistrictNotFound, существование не раскрывается) под замком; имя —
// CleanName; архив — archived_at из часов, из архива — NULL; новое имя или имя возвращаемого из архива занято
// действующим районом — geo.ErrDistrictNameTaken. Событие: архивирование — geo.district_archived, остальное —
// geo.district_updated (одно на запрос, версия + 1), аудит с изменёнными полями. Ничего не изменилось — без записей.
func (a *Admin) UpdateDistrict(ctx context.Context, s *auth.Staff, cityID, districtID uuid.UUID, p DistrictPatch, m approvals.Meta) (geo.AdminDistrict, error) {
	if err := a.authorize(ctx, s, cityID, permDistrictsManage); err != nil {
		return geo.AdminDistrict{}, err
	}
	if p.Name == nil && p.Archived == nil {
		return geo.AdminDistrict{}, httpx.NewFieldError("body", "minProperties")
	}
	var name string
	if p.Name != nil {
		clean, err := districtName(*p.Name)
		if err != nil {
			return geo.AdminDistrict{}, err
		}
		name = clean
	}
	now := a.d.Clock.Now()
	var out geo.AdminDistrict
	err := db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		cur, err := q.DistrictForUpdate(ctx, geodb.DistrictForUpdateParams{ID: districtID, CityID: cityID})
		if errors.Is(err, pgx.ErrNoRows) {
			return geo.ErrDistrictNotFound
		}
		if err != nil {
			return err
		}
		out = districtOf(cur.ID, cur.Name, cur.ArchivedAt, cur.Version)
		next := geodb.UpdateDistrictParams{ID: cur.ID, Name: cur.Name, ArchivedAt: cur.ArchivedAt}
		before, after := map[string]any{}, map[string]any{}
		if p.Name != nil && name != cur.Name {
			next.Name = name
			before["name"], after["name"] = cur.Name, name
		}
		archiving := false
		if p.Archived != nil && *p.Archived != (cur.ArchivedAt != nil) {
			if *p.Archived {
				next.ArchivedAt, archiving = &now, true
			} else {
				next.ArchivedAt = nil
			}
			before["archived"], after["archived"] = cur.ArchivedAt != nil, *p.Archived
		}
		if len(after) == 0 {
			return nil
		}
		d, err := q.UpdateDistrict(ctx, next)
		if err != nil {
			return nameTaken(err)
		}
		typ := geo.EventDistrictUpdated
		if archiving {
			typ = geo.EventDistrictArchived
		}
		if _, err := events.Publish(ctx, geo.DistrictEvent(typ, d.ID, cityID, d.Version)); err != nil {
			return err
		}
		if err := audit.Write(ctx, audit.Entry{
			ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionDistrictUpdated,
			ObjectType: objectDistrict, ObjectID: d.ID, ScopeCityID: cityID, Before: before, After: after,
			IP: m.IP, UserAgent: m.UserAgent,
		}); err != nil {
			return err
		}
		out = districtOf(d.ID, d.Name, d.ArchivedAt, d.Version)
		return nil
	})
	return out, err
}
