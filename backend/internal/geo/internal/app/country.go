package app

// Страны (createGeoCountry, updateGeoCountry, Task 13): спека geo §5.2, R12, R25; решения пользователя 27.10 —
// создание страны, смена валюты, возрастных порогов и выключение — всегда вдвоём (действия geo.country_create и
// geo.country_rules с правом geo.countries.rules); включение и прочие поля — сразу (geo.countries.manage).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

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
	permCountriesManage = "geo.countries.manage"
	permCountriesRules  = "geo.countries.rules"

	// ActionCountryRules и ActionCountryCreate — действия подтверждения вдвоём (approvals.actions.<действие> в
	// локалях админки).
	ActionCountryRules  = "geo.country_rules"
	ActionCountryCreate = "geo.country_create"

	actionCountryCreated = "geo.country_created"
	actionCountryUpdated = "geo.country_updated"
	objectCountry        = "country"

	countryCodeKey  = "countries_code_key"
	codeCheckFailed = "23514"

	// Поля правки страны — имена контракта (GeoCountryPatch): ключи from/to/patch payload, changed события, аудит.
	fieldIsEnabled = "is_enabled"
	fieldNames     = "names"
)

// Отказы исполнителей с кодом причины (error у failed): наружу approve отвечает 409 platform.approval_stale
// (approveProblem, R4) — бизнес-кодов geo у подтверждения нет.
var (
	errExecCountryExists   = httpx.NewError(http.StatusConflict, geo.CodeCountryExists)
	errExecCountryNotFound = httpx.NewError(http.StatusNotFound, geo.CodeCountryNotFound)
)

// CountryInput — новая страна: правила, включена ли сразу, названия по языкам (на default_locale — обязательно).
type CountryInput struct {
	domain.CountryRules
	IsEnabled bool
	Names     map[string]string
}

// CountryPatch — правка страны: только присланные поля. Names: значение nil — удалить перевод.
type CountryPatch struct {
	Currency, PhonePrefix                     *string
	WeekStartsOn, MinSignupAge, AgeOfMajority *int
	IsEnabled                                 *bool
	Names                                     map[string]*string
}

// countryPatchJSON — patch в payload geo.country_rules: имена полей контракта, присланные поля (названия уже
// очищены CleanName; null — удалить перевод).
type countryPatchJSON struct {
	Currency      *string            `json:"currency,omitempty"`
	PhonePrefix   *string            `json:"phone_prefix,omitempty"`
	WeekStartsOn  *int               `json:"week_starts_on,omitempty"`
	MinSignupAge  *int               `json:"min_signup_age,omitempty"`
	AgeOfMajority *int               `json:"age_of_majority,omitempty"`
	IsEnabled     *bool              `json:"is_enabled,omitempty"`
	Names         map[string]*string `json:"names,omitempty"`
}

func (p countryPatchJSON) patch() CountryPatch { return CountryPatch(p) }

func patchJSON(p CountryPatch) countryPatchJSON { return countryPatchJSON(p) }

// countryRulesPayload — payload geo.country_rules (v = 1): инициатор, страна (id, код, версия на момент запроса),
// from/to — только меняющиеся поля (экрану «было → стало», summary.ts), patch — всё, что исполнится.
type countryRulesPayload struct {
	V int `json:"v"`
	approvals.Initiator
	CountryID uuid.UUID        `json:"country_id"`
	Code      string           `json:"code"`
	Version   int64            `json:"version"`
	From      map[string]any   `json:"from"`
	To        map[string]any   `json:"to"`
	Patch     countryPatchJSON `json:"patch"`
}

// countryCreatePayload — payload geo.country_create (v = 1): инициатор и поля CountryInput наверху.
type countryCreatePayload struct {
	V int `json:"v"`
	approvals.Initiator
	Code          string            `json:"code"`
	Currency      string            `json:"currency"`
	DefaultLocale string            `json:"default_locale"`
	PhonePrefix   string            `json:"phone_prefix"`
	WeekStartsOn  int               `json:"week_starts_on"`
	MinSignupAge  int               `json:"min_signup_age"`
	AgeOfMajority int               `json:"age_of_majority"`
	IsEnabled     bool              `json:"is_enabled"`
	Names         map[string]string `json:"names"`
}

func (p countryCreatePayload) rules() domain.CountryRules {
	return domain.CountryRules{Code: p.Code, Currency: p.Currency, DefaultLocale: p.DefaultLocale, PhonePrefix: p.PhonePrefix,
		WeekStartsOn: p.WeekStartsOn, MinSignupAge: p.MinSignupAge, AgeOfMajority: p.AgeOfMajority}
}

// RegisterApprovals — действия geo «вдвоём» (R25): geo.country_rules и geo.country_create с одним правом
// geo.countries.rules; инициатора и подтверждающего под замком перепроверяет хук платформы (LockActors, R21); цели
// и города нет. svc — сервис подтверждений для запросов из сценариев. Зовёт сборка admin-api до reg.Validate().
func (a *Admin) RegisterApprovals(r *approvals.Registry, svc *approvals.Service) {
	a.approvals = svc
	r.Register(approvals.Action{Name: ActionCountryRules, Permission: permCountriesRules, LockActors: true, Exec: a.execCountryRules})
	r.Register(approvals.Action{Name: ActionCountryCreate, Permission: permCountriesRules, LockActors: true, Exec: a.execCountryCreate})
}

// requester — сервис подтверждений; не зарегистрирован (сборка без RegisterApprovals) — 501 вместо паники.
func (a *Admin) requester() (*approvals.Service, error) {
	if a.approvals == nil {
		return nil, httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)
	}
	return a.approvals, nil
}

// ruleError — первое нарушение правил страны по полю тела.
func ruleError(errs []domain.FieldError) error {
	if len(errs) == 0 {
		return nil
	}
	return httpx.NewFieldError("body."+errs[0].Field, errs[0].Code)
}

// cleanNames — названия из запроса: язык — из поддерживаемых (enum), значение — CleanName (nil — удалить, остаётся
// nil). Языки по алфавиту — первое нарушение детерминировано.
func cleanNames(in map[string]*string) (map[string]*string, error) {
	out := make(map[string]*string, len(in))
	for _, l := range slices.Sorted(maps.Keys(in)) {
		field := "body.names." + l
		if !domain.LocaleSupported(l) {
			return nil, httpx.NewFieldError(field, "enum")
		}
		if in[l] == nil {
			out[l] = nil
			continue
		}
		clean, ok := domain.CleanName(*in[l])
		if !ok {
			return nil, httpx.NewFieldError(field, nameRule(*in[l]))
		}
		out[l] = &clean
	}
	return out, nil
}

// checkCountryInput — новая страна: правила (R12), названия; название на default_locale обязательно.
func checkCountryInput(in CountryInput) (map[string]string, error) {
	if err := ruleError(domain.ValidateCountry(in.CountryRules)); err != nil {
		return nil, err
	}
	raw := make(map[string]*string, len(in.Names))
	for l, n := range in.Names {
		raw[l] = &n
	}
	clean, err := cleanNames(raw)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(clean))
	for l, n := range clean {
		names[l] = *n
	}
	if names[in.DefaultLocale] == "" {
		return nil, httpx.NewFieldError("body.names."+in.DefaultLocale, "required")
	}
	return names, nil
}

// CreateCountry — POST /v1/geo/countries: новая страна только через подтверждение вдвоём (geo.country_create, R25) —
// всегда запрос (202), страны до исполнения нет. Право geo.countries.rules (Deny — 403); правила и названия —
// здесь и ещё раз в исполнителе; код уже в справочнике — geo.ErrCountryExists до запроса (два ждущих запроса на
// один код допустимы — второй при исполнении failed).
func (a *Admin) CreateCountry(ctx context.Context, s *auth.Staff, in CountryInput, m approvals.Meta) (approvals.Approval, error) {
	if s.Decide(permCountriesRules, uuid.Nil) == auth.Deny {
		return approvals.Approval{}, errForbidden
	}
	svc, err := a.requester()
	if err != nil {
		return approvals.Approval{}, err
	}
	names, err := checkCountryInput(in)
	if err != nil {
		return approvals.Approval{}, err
	}
	var ap approvals.Approval
	err = db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		exists, err := geodb.New(tx).AdminCountryExists(ctx, in.Code)
		if err != nil {
			return err
		}
		if exists {
			return geo.ErrCountryExists
		}
		ap, err = svc.Request(ctx, tx, s, s.Label, m, ActionCountryCreate, countryCreatePayload{
			V: 1, Initiator: approvals.InitiatorOf(s), Code: in.Code, Currency: in.Currency, DefaultLocale: in.DefaultLocale,
			PhonePrefix: in.PhonePrefix, WeekStartsOn: in.WeekStartsOn, MinSignupAge: in.MinSignupAge,
			AgeOfMajority: in.AgeOfMajority, IsEnabled: in.IsEnabled, Names: names,
		})
		return err
	})
	return ap, err
}

// execCountryCreate — исполнитель geo.country_create (под хуком замка акторов и savepoint платформы): правила и
// названия заново — не проходят — Fail(ErrStale); код заняли (второй запрос на тот же код) — Fail(geo.country_exists);
// иначе страна (версия 1), названия, событие geo.country_created и аудит от подтверждающего.
func (a *Admin) execCountryCreate(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	var p countryCreatePayload
	if err := json.Unmarshal(raw, &p); err != nil || p.V != 1 {
		return fmt.Errorf("geo: payload %s v%d: %w", ActionCountryCreate, p.V, errors.Join(err, errBadPayload))
	}
	names, err := checkCountryInput(CountryInput{CountryRules: p.rules(), IsEnabled: p.IsEnabled, Names: p.Names})
	if err != nil {
		return approvals.Fail(approvals.ErrStale)
	}
	q := geodb.New(tx)
	countryID := id.New()
	if err := q.InsertCountry(ctx, geodb.InsertCountryParams{
		ID: countryID, Code: p.Code, Name: names[p.DefaultLocale], Currency: p.Currency, DefaultLocale: p.DefaultLocale,
		PhonePrefix: p.PhonePrefix, WeekStartsOn: int64(p.WeekStartsOn), MinSignupAge: int64(p.MinSignupAge),
		AgeOfMajority: int64(p.AgeOfMajority), IsEnabled: p.IsEnabled,
	}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			switch {
			case pgErr.Code == codeUniqueViolation && pgErr.ConstraintName == countryCodeKey:
				return approvals.Fail(errExecCountryExists)
			case pgErr.Code == codeCheckFailed:
				return approvals.Fail(approvals.ErrStale)
			}
		}
		return err
	}
	for _, l := range slices.Sorted(maps.Keys(names)) {
		if err := q.UpsertCountryName(ctx, geodb.UpsertCountryNameParams{CountryID: countryID, Locale: l, Name: names[l]}); err != nil {
			return err
		}
	}
	if _, err := events.Publish(ctx, geo.CountryCreatedEvent(countryID, p.Code)); err != nil {
		return err
	}
	apID, _ := approvals.ApprovalIDFrom(ctx)
	return audit.Write(ctx, audit.Entry{
		ActorStaffID: approver.StaffID, ActorRole: approver.Role, ActorLabel: approver.Label, Action: actionCountryCreated,
		ObjectType: objectCountry, ObjectID: countryID,
		After: map[string]any{"country_code": p.Code, "currency": p.Currency, "default_locale": p.DefaultLocale,
			"phone_prefix": p.PhonePrefix, "week_starts_on": p.WeekStartsOn, "min_signup_age": p.MinSignupAge,
			"age_of_majority": p.AgeOfMajority, fieldIsEnabled: p.IsEnabled, fieldNames: names,
			"requested_by": p.ID, "approval_id": apID},
	})
}

// errBadPayload — payload запроса не разобрался или незнакомой версии: нарушенный инвариант (500, запрос pending).
var errBadPayload = errors.New("geo: payload запроса не разобран")

// countryRow — страна под замком (CountryForUpdate, CountryForUpdateByID).
type countryRow struct {
	ID                                        uuid.UUID
	Code, Name, Currency, DefaultLocale       string
	PhonePrefix                               string
	WeekStartsOn, MinSignupAge, AgeOfMajority int
	IsEnabled                                 bool
	Version                                   int64
}

func (c countryRow) rules() domain.CountryRules {
	return domain.CountryRules{Code: c.Code, Currency: c.Currency, DefaultLocale: c.DefaultLocale, PhonePrefix: c.PhonePrefix,
		WeekStartsOn: c.WeekStartsOn, MinSignupAge: c.MinSignupAge, AgeOfMajority: c.AgeOfMajority}
}

// countryDiff — что правка меняет в стране (сравнение с текущими значениями под замком).
type countryDiff struct {
	next     countryRow
	from, to map[string]any
	names    map[string]*string // изменённые языки: новое значение или nil — удалить
	rules    bool               // меняется правило «вдвоём» (RulesChange)
}

func (d countryDiff) changed() []string { return slices.Sorted(maps.Keys(d.to)) }

// RulesChange — правка требует подтверждения вдвоём: МЕНЯЕТСЯ (не просто присутствует в теле) currency,
// min_signup_age, age_of_majority или is_enabled с true на false (решение пользователя 27.10, Review Focus 4).
func RulesChange(cur geo.AdminCountry, p CountryPatch) bool {
	return p.Currency != nil && *p.Currency != cur.Currency ||
		p.MinSignupAge != nil && *p.MinSignupAge != cur.MinSignupAge ||
		p.AgeOfMajority != nil && *p.AgeOfMajority != cur.AgeOfMajority ||
		p.IsEnabled != nil && !*p.IsEnabled && cur.IsEnabled
}

// diffCountry — изменения правки p (названия уже очищены) против текущей страны cur и её названий have: итоговые
// правила проверяются целиком (новый min_signup_age с текущим age_of_majority), нарушение — по полю, которое
// прислано (у записи справочника могут быть «старые» значения вне правил — правке других полей они не мешают);
// удаление строки языка страны — 400 required.
func diffCountry(cur countryRow, have map[string]string, p CountryPatch) (countryDiff, error) {
	d := countryDiff{next: cur, from: map[string]any{}, to: map[string]any{}, names: map[string]*string{}}
	sets := map[string]bool{}
	setS := func(field string, v *string, dst *string) {
		if v != nil {
			sets[field] = true
			if *v != *dst {
				d.from[field], d.to[field] = *dst, *v
				*dst = *v
			}
		}
	}
	setI := func(field string, v *int, dst *int) {
		if v != nil {
			sets[field] = true
			if *v != *dst {
				d.from[field], d.to[field] = *dst, *v
				*dst = *v
			}
		}
	}
	setS(domain.FieldCurrency, p.Currency, &d.next.Currency)
	setS(domain.FieldPhonePrefix, p.PhonePrefix, &d.next.PhonePrefix)
	setI(domain.FieldWeekStartsOn, p.WeekStartsOn, &d.next.WeekStartsOn)
	setI(domain.FieldMinSignupAge, p.MinSignupAge, &d.next.MinSignupAge)
	setI(domain.FieldAgeOfMajority, p.AgeOfMajority, &d.next.AgeOfMajority)
	if p.IsEnabled != nil && *p.IsEnabled != cur.IsEnabled {
		d.from[fieldIsEnabled], d.to[fieldIsEnabled] = cur.IsEnabled, *p.IsEnabled
		d.next.IsEnabled = *p.IsEnabled
	}
	for _, f := range domain.ValidateCountry(d.next.rules()) {
		if sets[f.Field] || f.Field == domain.FieldAgeOfMajority && sets[domain.FieldMinSignupAge] {
			return countryDiff{}, httpx.NewFieldError("body."+f.Field, f.Code)
		}
	}
	nf, nt := map[string]any{}, map[string]any{}
	for _, l := range slices.Sorted(maps.Keys(p.Names)) {
		v := p.Names[l]
		old, had := have[l]
		switch {
		case v == nil && l == cur.DefaultLocale:
			return countryDiff{}, httpx.NewFieldError("body.names."+l, "required")
		case v == nil && !had, v != nil && had && old == *v:
			continue
		}
		if had {
			nf[l] = old
		} else {
			nf[l] = nil
		}
		nt[l] = v
		d.names[l] = v
		if v != nil && l == cur.DefaultLocale {
			d.next.Name = *v
		}
	}
	if len(nt) > 0 {
		d.from[fieldNames], d.to[fieldNames] = nf, nt
	}
	d.rules = RulesChange(geo.AdminCountry{Currency: cur.Currency, MinSignupAge: cur.MinSignupAge,
		AgeOfMajority: cur.AgeOfMajority, IsEnabled: cur.IsEnabled}, p)
	return d, nil
}

// countryNames — названия страны по языкам.
func countryNames(ctx context.Context, q *geodb.Queries, countryID uuid.UUID) (map[string]string, error) {
	rows, err := q.CountryNamesOf(ctx, countryID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Locale] = r.Name
	}
	return out, nil
}

// applyCountry — запись правки: поля, названия, version + 1, событие geo.country_updated (changed — поля), аудит
// автора (прямая правка — сотрудник; исполнитель — подтверждающий с requested_by и approval_id в after).
func applyCountry(ctx context.Context, q *geodb.Queries, cur countryRow, d countryDiff, actor *auth.Staff, m approvals.Meta, extra map[string]any) error {
	n := d.next
	version, err := q.UpdateCountryFields(ctx, geodb.UpdateCountryFieldsParams{ID: cur.ID, Name: n.Name, Currency: n.Currency,
		PhonePrefix: n.PhonePrefix, WeekStartsOn: int64(n.WeekStartsOn), MinSignupAge: int64(n.MinSignupAge),
		AgeOfMajority: int64(n.AgeOfMajority), IsEnabled: n.IsEnabled})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == codeCheckFailed {
			return fmt.Errorf("geo: правка страны %s нарушила CHECK: %w", cur.Code, err)
		}
		return err
	}
	for _, l := range slices.Sorted(maps.Keys(d.names)) {
		if v := d.names[l]; v == nil {
			err = q.DeleteCountryName(ctx, geodb.DeleteCountryNameParams{CountryID: cur.ID, Locale: l})
		} else {
			err = q.UpsertCountryName(ctx, geodb.UpsertCountryNameParams{CountryID: cur.ID, Locale: l, Name: *v})
		}
		if err != nil {
			return err
		}
	}
	if _, err := events.Publish(ctx, geo.CountryUpdatedEvent(cur.ID, version, cur.Code, d.changed())); err != nil {
		return err
	}
	after := maps.Clone(d.to)
	maps.Copy(after, extra)
	return audit.Write(ctx, audit.Entry{
		ActorStaffID: actor.StaffID, ActorRole: actor.Role, ActorLabel: actor.Label, Action: actionCountryUpdated,
		ObjectType: objectCountry, ObjectID: cur.ID, Before: d.from, After: after, IP: m.IP, UserAgent: m.UserAgent,
	})
}

// UpdateCountry — PATCH /v1/geo/countries/{code} одной транзакцией под Idempotency-Key вызывающего:
//  1. форма: пустой patch — 400 body; названия — язык и CleanName;
//  2. страна FOR UPDATE по коду (нет — geo.ErrCountryNotFound); «меняется» — против значений под замком; итоговые
//     правила — ValidateCountry (400 по присланному полю); удалить строку языка страны — 400 required;
//  3. RulesChange → Decide(geo.countries.rules): NeedsApproval — весь patch в запрос geo.country_rules в той же
//     транзакции, страна не меняется (Approval != nil ⇒ 202); Deny — 403; Allow (в матрице нет) — сразу;
//  4. иначе Decide(geo.countries.manage) == Allow — правка сразу: version + 1, событие и аудит. Ничего не
//     меняется — 200 с текущей страной без записей.
//
// Возвращает страну (как listGeoCountries) на языке locale — при запросе текущую, без изменений.
func (a *Admin) UpdateCountry(ctx context.Context, s *auth.Staff, code string, p CountryPatch, locale string, m approvals.Meta) (geo.AdminCountry, *approvals.Approval, error) {
	if s.Decide(permCountriesManage, uuid.Nil) == auth.Deny && s.Decide(permCountriesRules, uuid.Nil) == auth.Deny {
		return geo.AdminCountry{}, nil, errForbidden
	}
	if p.Currency == nil && p.PhonePrefix == nil && p.WeekStartsOn == nil && p.MinSignupAge == nil &&
		p.AgeOfMajority == nil && p.IsEnabled == nil && len(p.Names) == 0 {
		return geo.AdminCountry{}, nil, httpx.NewFieldError("body", "minProperties")
	}
	names, err := cleanNames(p.Names)
	if err != nil {
		return geo.AdminCountry{}, nil, err
	}
	p.Names = names
	var out geo.AdminCountry
	var ap *approvals.Approval
	err = db.InTx(ctx, a.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := geodb.New(tx)
		r, err := q.CountryForUpdate(ctx, code)
		if errors.Is(err, pgx.ErrNoRows) {
			return geo.ErrCountryNotFound
		}
		if err != nil {
			return err
		}
		cur := countryOfRow(r)
		have, err := countryNames(ctx, q, cur.ID)
		if err != nil {
			return err
		}
		d, err := diffCountry(cur, have, p)
		if err != nil {
			return err
		}
		direct := true
		if d.rules {
			switch s.Decide(permCountriesRules, uuid.Nil) {
			case auth.NeedsApproval:
				direct = false
			case auth.Deny:
				return errForbidden
			}
		} else if err := a.authorize(ctx, s, uuid.Nil, permCountriesManage); err != nil {
			return err
		}
		switch {
		case !direct:
			svc, err := a.requester()
			if err != nil {
				return err
			}
			req, err := svc.Request(ctx, tx, s, s.Label, m, ActionCountryRules, countryRulesPayload{
				V: 1, Initiator: approvals.InitiatorOf(s), CountryID: cur.ID, Code: cur.Code, Version: cur.Version,
				From: d.from, To: d.to, Patch: patchJSON(p),
			})
			if err != nil {
				return err
			}
			ap = &req
		case len(d.to) > 0:
			if err := applyCountry(ctx, q, cur, d, s, m, nil); err != nil {
				return err
			}
		}
		out, err = countryCard(ctx, q, cur.ID, cur.Code, locale)
		return err
	})
	if err != nil {
		return geo.AdminCountry{}, nil, err
	}
	return out, ap, nil
}

// execCountryRules — исполнитель geo.country_rules (под хуком замка акторов и savepoint платформы): страна FOR UPDATE
// по id — нет — Fail(geo.country_not_found); версия не та, что при запросе (страну правили) — ErrStale; patch не
// проходит текущих правил — Fail(ErrStale); иначе — та же запись, что у прямой правки, аудит от подтверждающего
// (подпись решения) с requested_by и approval_id.
func (a *Admin) execCountryRules(ctx context.Context, tx pgx.Tx, raw json.RawMessage, approver *auth.Staff) error {
	var p countryRulesPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.V != 1 {
		return fmt.Errorf("geo: payload %s v%d: %w", ActionCountryRules, p.V, errors.Join(err, errBadPayload))
	}
	q := geodb.New(tx)
	r, err := q.CountryForUpdateByID(ctx, p.CountryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return approvals.Fail(errExecCountryNotFound)
	}
	if err != nil {
		return err
	}
	cur := countryOfRow(geodb.CountryForUpdateRow(r))
	if cur.Version != p.Version {
		return approvals.ErrStale
	}
	have, err := countryNames(ctx, q, cur.ID)
	if err != nil {
		return err
	}
	patch := p.Patch.patch()
	if patch.Names, err = cleanNames(patch.Names); err != nil {
		return approvals.Fail(approvals.ErrStale)
	}
	d, err := diffCountry(cur, have, patch)
	if err != nil {
		return approvals.Fail(approvals.ErrStale)
	}
	if len(d.to) == 0 {
		return nil
	}
	apID, _ := approvals.ApprovalIDFrom(ctx)
	return applyCountry(ctx, q, cur, d, approver, approvals.Meta{}, map[string]any{"requested_by": p.ID, "approval_id": apID})
}

// countryCard — страна с правилами и названиями (как элемент listGeoCountries) на языке locale.
func countryCard(ctx context.Context, q *geodb.Queries, countryID uuid.UUID, code, locale string) (geo.AdminCountry, error) {
	c, err := q.AdminGetCountry(ctx, geodb.AdminGetCountryParams{Locale: nameLocale(locale), Code: code})
	if err != nil {
		return geo.AdminCountry{}, err
	}
	names, err := countryNames(ctx, q, countryID)
	if err != nil {
		return geo.AdminCountry{}, err
	}
	return geo.AdminCountry{
		Code: c.Code, Name: c.Name, Names: names, Currency: c.Currency, DefaultLocale: c.DefaultLocale,
		PhonePrefix: c.PhonePrefix, WeekStartsOn: int(c.WeekStartsOn), MinSignupAge: int(c.MinSignupAge),
		AgeOfMajority: int(c.AgeOfMajority), IsEnabled: c.IsEnabled, OpenCities: int(c.OpenCities), Version: c.Version,
	}, nil
}

// countryOfRow — строка CountryForUpdate (и CountryForUpdateByID — те же поля) в виде сценария.
func countryOfRow(r geodb.CountryForUpdateRow) countryRow {
	return countryRow{ID: r.ID, Code: r.Code, Name: r.Name, Currency: r.Currency, DefaultLocale: r.DefaultLocale,
		PhonePrefix: r.PhonePrefix, WeekStartsOn: int(r.WeekStartsOn), MinSignupAge: int(r.MinSignupAge),
		AgeOfMajority: int(r.AgeOfMajority), IsEnabled: r.IsEnabled, Version: r.Version}
}
