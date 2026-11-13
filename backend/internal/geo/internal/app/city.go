package app

// Правка города админкой (updateGeoCity, Task 11): спека geo §5.2, спека сотрудников §6.1, §6.3 п.5, R10, R11.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
)

const (
	permCitiesManage = "geo.cities.manage" // статус, slug, привязка
	permCitiesRename = "geo.cities.rename" // названия

	actionCityUpdated = "geo.city_updated"

	slugHistoryKey = "city_slug_history_pkey"
)

// slugLockClass — класс advisory-замка смены адреса города (SlugLock): «geos» в ASCII; своё пространство
// двухаргументной формы, не пересекается с countryLockClass («geor») и importJobLockClass («geoj»).
const slugLockClass int32 = 0x67656f73

// errForbidden — 403 auth.forbidden: право сотрудника на поле или действие — Deny.
var errForbidden = httpx.NewError(http.StatusForbidden, httpx.CodeForbidden)

// errUnexpectedApproval — у права записи geo, которое сценарий исполняет сразу, в матрице появился Dual: без
// сценария подтверждения это нарушенный инвариант — 500 с логом, ничего не записано.
var errUnexpectedApproval = errors.New("geo: право требует подтверждения вдвоём, а сценарий его не умеет")

// CityPatch — правка города: только присланные поля. Names: значение nil — удалить перевод.
type CityPatch struct {
	Status *geo.Status
	Slug   *string
	Names  map[string]*string
}

// authorize — решение по каждому праву над городом cityID (R10): хоть одно Deny — 403 на весь запрос, NeedsApproval
// (матрица изменилась без сценария) — 500 с логом.
func (a *Admin) authorize(ctx context.Context, s *auth.Staff, cityID uuid.UUID, perms ...string) error {
	var dual string
	for _, p := range perms {
		switch s.Decide(p, cityID) {
		case auth.Allow:
		case auth.NeedsApproval:
			dual = p
		default:
			return errForbidden
		}
	}
	if dual != "" {
		a.d.Log.ErrorContext(ctx, "geo: право записи требует подтверждения — сценария нет", "permission", dual)
		return fmt.Errorf("%w: %s", errUnexpectedApproval, dual)
	}
	return nil
}

// cityEdit — правка, приведённая к виду записи: названия очищены (CleanName), языки по алфавиту.
type cityEdit struct {
	status  *geo.Status
	slug    *string
	locales []string
	names   map[string]*string
}

// checkCityPatch — форма правки до транзакции: пустая — 400 body minProperties; статус — из enum; slug — формат
// cities_slug_format; язык — из поддерживаемых (enum); название — CleanName (пусто после очистки — minLength,
// длиннее NameMax — maxLength, управляющие и невидимые — pattern).
func checkCityPatch(p CityPatch) (cityEdit, error) {
	if p.Status == nil && p.Slug == nil && len(p.Names) == 0 {
		return cityEdit{}, httpx.NewFieldError("body", "minProperties")
	}
	e := cityEdit{status: p.Status, slug: p.Slug, names: make(map[string]*string, len(p.Names))}
	if p.Status != nil {
		switch *p.Status {
		case geo.StatusWaitlist, geo.StatusPilot, geo.StatusLive:
		default:
			return cityEdit{}, httpx.NewFieldError("body.status", "enum")
		}
	}
	if p.Slug != nil && !domain.ValidSlug(*p.Slug) {
		return cityEdit{}, httpx.NewFieldError("body.slug", "pattern")
	}
	e.locales = slices.Sorted(maps.Keys(p.Names))
	for _, l := range e.locales {
		field := "body.names." + l
		if !domain.LocaleSupported(l) {
			return cityEdit{}, httpx.NewFieldError(field, "enum")
		}
		raw := p.Names[l]
		if raw == nil {
			e.names[l] = nil
			continue
		}
		clean, ok := domain.CleanName(*raw)
		if !ok {
			return cityEdit{}, httpx.NewFieldError(field, nameRule(*raw))
		}
		e.names[l] = &clean
	}
	return e, nil
}

// nameRule — какое правило нарушило название, не прошедшее CleanName.
func nameRule(s string) string {
	switch {
	case strings.TrimFunc(s, unicode.IsSpace) == "":
		return "minLength"
	case utf8.RuneCountInString(s) > domain.NameMax:
		return "maxLength"
	}
	return "pattern"
}

// UpdateCity — PATCH /v1/geo/cities/{cityId} одной транзакцией под Idempotency-Key вызывающего:
//  1. права по присланным полям (status, slug — geo.cities.manage; names — geo.cities.rename) до записи;
//  2. город FOR UPDATE (последняя запись выигрывает, R11); нет — geo.ErrCityNotFound;
//  3. статус — любой переход; slug — занят текущим или историей другого города — geo.ErrSlugTaken (и 23505 при
//     гонке), свой старый — строка истории удаляется; прежний текущий — в историю с временем часов;
//  4. названия: строка языка страны переписывает cities.name, её удаление — 400 body.names.<язык> required;
//  5. событие на каждый вид изменения (статус, slug, названия) с последовательными версиями, version города +
//     число событий; одна запись аудита geo.city_updated с изменёнными полями. Ничего не изменилось — ни
//     событий, ни аудита.
//
// Возвращает карточку города (как getGeoCity) на языке locale.
func (a *Admin) UpdateCity(ctx context.Context, s *auth.Staff, id uuid.UUID, p CityPatch, locale string, m approvals.Meta) (geo.AdminCity, error) {
	var perms []string
	if p.Status != nil || p.Slug != nil {
		perms = append(perms, permCitiesManage)
	}
	// пустой names — тоже правка названий: права решаются до формы сценария (у чужого города — 403, а не ошибка
	// формы; ревью G6 m3). По HTTP {"names":{}} сюда не доходит: валидатор контракта (GeoNamesPatch minProperties: 1)
	// отвечает 400 до хендлера одинаково для своего и чужого города — порядок важен для вызовов сценария не по HTTP
	if p.Names != nil {
		perms = append(perms, permCitiesRename)
	}
	if err := a.authorize(ctx, s, id, perms...); err != nil {
		return geo.AdminCity{}, err
	}
	edit, err := checkCityPatch(p)
	if err != nil {
		return geo.AdminCity{}, err
	}
	now := a.d.Clock.Now()
	var card geo.AdminCity
	err = db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		row, err := q.CityForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return geo.ErrCityNotFound
		}
		if err != nil {
			return err
		}
		ch, err := a.applyCityEdit(ctx, q, row, edit, now)
		if err != nil {
			return err
		}
		if ch.events() > 0 {
			if err := ch.publish(ctx, q, row, s, m); err != nil {
				return err
			}
		}
		a.at(ctx, "updated")
		card, err = cityCard(ctx, q, id, locale)
		return err
	})
	return card, err
}

// cityChange — что правка меняет в городе (сравнение с текущими значениями под замком).
type cityChange struct {
	status       *geo.Status
	slug         *string
	name         string             // cities.name после правки
	before       map[string]*string // названия до правки по изменённым языкам (nil — не было)
	after        map[string]*string // названия после правки (nil — удалено)
	namesChanged []string           // изменённые языки по алфавиту
}

func (c cityChange) events() int64 {
	var n int64
	if c.status != nil {
		n++
	}
	if c.slug != nil {
		n++
	}
	if len(c.namesChanged) > 0 {
		n++
	}
	return n
}

// applyCityEdit — запись slug (история) и названий; поля cities пишет publish одним UPDATE вместе с версией.
func (a *Admin) applyCityEdit(ctx context.Context, q *geodb.Queries, row geodb.CityForUpdateRow, e cityEdit, now time.Time) (cityChange, error) {
	ch := cityChange{name: row.Name, before: map[string]*string{}, after: map[string]*string{}}
	if e.status != nil && string(*e.status) != row.Status {
		ch.status = e.status
	}
	if e.slug != nil && *e.slug != row.Slug {
		if err := takeSlug(ctx, q, row.ID, *e.slug); err != nil {
			return cityChange{}, err
		}
		a.at(ctx, "slug")
		ch.slug = e.slug
	}
	if len(e.locales) > 0 {
		cur, err := q.AdminCityNames(ctx, row.ID)
		if err != nil {
			return cityChange{}, err
		}
		have := make(map[string]string, len(cur))
		for _, n := range cur {
			have[n.Locale] = n.Name
		}
		for _, l := range e.locales {
			v := e.names[l]
			old, had := have[l]
			switch {
			case v == nil && l == row.DefaultLocale:
				return cityChange{}, httpx.NewFieldError("body.names."+l, "required")
			case v == nil && !had, v != nil && had && old == *v:
				continue
			case v == nil:
				if err := q.DeleteCityName(ctx, geodb.DeleteCityNameParams{CityID: row.ID, Locale: l}); err != nil {
					return cityChange{}, err
				}
			default:
				if err := q.UpsertCityName(ctx, geodb.UpsertCityNameParams{CityID: row.ID, Locale: l, Name: *v}); err != nil {
					return cityChange{}, err
				}
				if l == row.DefaultLocale {
					ch.name = *v
				}
			}
			if had {
				ch.before[l] = &old
			} else {
				ch.before[l] = nil
			}
			ch.after[l] = v
			ch.namesChanged = append(ch.namesChanged, l)
		}
	}
	if ch.slug != nil {
		// прежний текущий адрес — в историю: старые ссылки веба продолжают находить город
		if err := q.InsertSlugHistory(ctx, geodb.InsertSlugHistoryParams{Slug: row.Slug, CityID: row.ID, ReplacedAt: now}); err != nil {
			return cityChange{}, slugConflict(err)
		}
	}
	return ch, nil
}

// takeSlug — новый адрес свободен для города: текущий slug или история другого города — geo.ErrSlugTaken; свой
// прежний из истории — строка истории удаляется (адрес снова текущий). Проверка — под замком slug (SlugLock, ревью
// G6 m4): смена на тот же адрес ждёт коммита предыдущей и видит её итог.
func takeSlug(ctx context.Context, q *geodb.Queries, cityID uuid.UUID, slug string) error {
	if err := q.SlugLock(ctx, geodb.SlugLockParams{Class: slugLockClass, Slug: slug}); err != nil {
		return fmt.Errorf("geo: замок адреса: %w", err)
	}
	owner, err := q.SlugOwner(ctx, slug)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return err
	case owner.CityID != cityID:
		return geo.ErrSlugTaken
	}
	_, err = q.DeleteOwnSlugHistory(ctx, geodb.DeleteOwnSlugHistoryParams{Slug: slug, CityID: cityID})
	return err
}

// slugConflict — 23505 по адресу (cities_slug_key, city_slug_history_pkey: адрес заняли параллельно после
// проверки) — geo.ErrSlugTaken; иначе — как есть.
func slugConflict(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeUniqueViolation &&
		(pgErr.ConstraintName == citySlugKey || pgErr.ConstraintName == slugHistoryKey) {
		return geo.ErrSlugTaken
	}
	return err
}

// publish — поля города и версия (+ число событий), события по видам изменения с последовательными версиями и
// одна запись аудита с изменёнными полями.
func (c cityChange) publish(ctx context.Context, q *geodb.Queries, row geodb.CityForUpdateRow, s *auth.Staff, m approvals.Meta) error {
	status, slug := row.Status, row.Slug
	if c.status != nil {
		status = string(*c.status)
	}
	if c.slug != nil {
		slug = *c.slug
	}
	n := c.events()
	version, err := q.UpdateCityFields(ctx, geodb.UpdateCityFieldsParams{Status: status, Slug: slug, Name: c.name, Bump: n, ID: row.ID})
	if err != nil {
		return slugConflict(err)
	}
	v := version - n
	before, after := map[string]any{}, map[string]any{}
	if c.status != nil {
		v++
		if _, err := events.Publish(ctx, geo.CityStatusChangedEvent(row.ID, v, geo.Status(row.Status), *c.status)); err != nil {
			return err
		}
		before["status"], after["status"] = row.Status, status
	}
	if c.slug != nil {
		v++
		if _, err := events.Publish(ctx, geo.CitySlugChangedEvent(row.ID, v, row.Slug, slug)); err != nil {
			return err
		}
		before["slug"], after["slug"] = row.Slug, slug
	}
	if len(c.namesChanged) > 0 {
		v++
		if _, err := events.Publish(ctx, geo.CityRenamedEvent(row.ID, v, c.namesChanged)); err != nil {
			return err
		}
		before["names"], after["names"] = c.before, c.after
	}
	return audit.Write(ctx, audit.Entry{
		ActorStaffID: s.StaffID, ActorRole: s.Role, ActorLabel: s.Label, Action: actionCityUpdated,
		ObjectType: objectCity, ObjectID: row.ID, ScopeCityID: row.ID, Before: before, After: after,
		IP: m.IP, UserAgent: m.UserAgent,
	})
}
