// Package flags читает фича-флаги. Модули зависят от Store, а не от SQL.
package flags

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/flags/flagsdb"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
)

var ErrUnknownFlag = errors.New("flags: неизвестный флаг")

// ErrDisabled — функция выключена в городе: клиенту 403 feature.disabled (спека §6.9).
var ErrDisabled = httpx.NewError(http.StatusForbidden, httpx.CodeFeatureDisabled)

type Store struct{ q *flagsdb.Queries }

func NewStore(db flagsdb.DBTX) *Store { return &Store{q: flagsdb.New(db)} }

// EnabledGlobally — флаг включён «везде»; флаг с Requires — только если включена и его зависимость (R12: защита в
// глубину, строку могли включить в обход Update). Строки зависимости нет — ErrUnknownFlag (ошибка выкатки).
func (s *Store) EnabledGlobally(ctx context.Context, key string) (bool, error) {
	row, err := s.q.GetFlag(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	if err != nil {
		return false, err
	}
	if dep := RuleOf(key).Requires; row.EnabledGlobally && dep != "" {
		return s.EnabledGlobally(ctx, dep)
	}
	return row.EnabledGlobally, nil
}

// Enabled — флаг включён глобально или в городе; флаг с Requires — только если и зависимость включена там же (R12).
func (s *Store) Enabled(ctx context.Context, key string, cityID uuid.UUID) (bool, error) {
	on, err := s.q.IsEnabled(ctx, flagsdb.IsEnabledParams{Key: key, CityID: cityID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	if err != nil {
		return false, err
	}
	if dep := RuleOf(key).Requires; on && dep != "" {
		return s.Enabled(ctx, dep, cityID)
	}
	return on, nil
}

// Require — для app/ модуля: выключено — ErrDisabled (403), флага нет — ErrUnknownFlag (500:
// флаг в коде без строки в базе — ошибка выкатки).
func (s *Store) Require(ctx context.Context, key string, cityID uuid.UUID) error {
	on, err := s.Enabled(ctx, key, cityID)
	if err != nil {
		return err
	}
	if !on {
		return ErrDisabled
	}
	return nil
}

// ErrStale — флаг изменили после чтения: version запроса не совпала (409 platform.flag_stale, спека §6.1).
var ErrStale = errors.New("flags: версия флага устарела")

// Flag — флаг для админки (listFeatureFlags, updateFeatureFlag). EnabledCityIDs — множество по возрастанию id
// (не nil); Version растёт на каждую правку; UpdatedAt — время базы (триггер), порядок правок — Version.
type Flag struct {
	Key, Description string
	EnabledGlobally  bool
	EnabledCityIDs   []uuid.UUID
	Version          int64
	UpdatedAt        time.Time
}

// List — все флаги по ключу.
func (s *Store) List(ctx context.Context) ([]Flag, error) {
	rows, err := s.q.ListFlags(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Flag, 0, len(rows))
	for _, r := range rows {
		out = append(out, flagOf(r.Key, r.Description, r.EnabledGlobally, r.EnabledCityIds, r.Version, r.UpdatedAt))
	}
	return out, nil
}

// Update — правка флага в транзакции tx вызывающего (событие и аудит пишет он, в той же tx). Правила (rules.go):
// непустой список городов у GlobalOnly — ErrGlobalOnly до любого запроса. Затем строки флага, его зависимости и
// зависимых — под замком одним запросом в порядке ключа (LockFlags): две правки связанных флагов не
// взаимоблокируются, а вторая видит итог первой. Строки флага нет — ErrUnknownFlag, version не та — ErrStale (и
// когда значения не меняются); globally nil — не меняется, cities nil — не меняются, иначе — множество (повторы
// схлопываются, порядок по id). Ничего не меняется — before == after без записи (version не растёт). Итог нарушает
// зависимость (включён флаг при выключенной или незаведённой зависимости, выключена зависимость при включённом
// зависимом) — *RequiresError без записи. Иначе UPDATE … WHERE key AND version, version + 1.
func (s *Store) Update(ctx context.Context, tx pgx.Tx, key string, version int64, globally *bool, cities []uuid.UUID) (before, after Flag, err error) {
	if RuleOf(key).GlobalOnly && len(CitySet(cities)) > 0 {
		return Flag{}, Flag{}, fmt.Errorf("%w: %s", ErrGlobalOnly, key)
	}
	q := s.q.WithTx(tx)
	rows, err := q.LockFlags(ctx, lockKeys(key))
	if err != nil {
		return Flag{}, Flag{}, err
	}
	locked := make(map[string]Flag, len(rows))
	for _, r := range rows {
		locked[r.Key] = flagOf(r.Key, r.Description, r.EnabledGlobally, r.EnabledCityIds, r.Version, r.UpdatedAt)
	}
	before, ok := locked[key]
	if !ok {
		return Flag{}, Flag{}, ErrUnknownFlag
	}
	if before.Version != version {
		return Flag{}, Flag{}, fmt.Errorf("%w: %s — версия %d, прислана %d", ErrStale, key, before.Version, version)
	}
	next := before
	if globally != nil {
		next.EnabledGlobally = *globally
	}
	if cities != nil {
		next.EnabledCityIDs = CitySet(cities)
	}
	if next.EnabledGlobally == before.EnabledGlobally && slices.Equal(next.EnabledCityIDs, before.EnabledCityIDs) {
		return before, before, nil
	}
	if err := checkRequires(next, locked); err != nil {
		return Flag{}, Flag{}, err
	}
	row, err := q.UpdateFlag(ctx, flagsdb.UpdateFlagParams{Key: key, Version: version, EnabledGlobally: next.EnabledGlobally,
		EnabledCityIds: next.EnabledCityIDs})
	if errors.Is(err, pgx.ErrNoRows) {
		return Flag{}, Flag{}, fmt.Errorf("%w: %s", ErrStale, key)
	}
	if err != nil {
		return Flag{}, Flag{}, err
	}
	return before, flagOf(row.Key, row.Description, row.EnabledGlobally, row.EnabledCityIds, row.Version, row.UpdatedAt), nil
}

// checkRequires — зависимости после правки next по строкам под замком (locked): зависимость бывает только между
// флагами GlobalOnly, поэтому сравнивается enabled_globally. Незаведённая строка зависимости — выключена.
func checkRequires(next Flag, locked map[string]Flag) error {
	if dep := RuleOf(next.Key).Requires; dep != "" && next.EnabledGlobally && !locked[dep].EnabledGlobally {
		return &RequiresError{Key: next.Key, Requires: dep, Enabling: true}
	}
	if !next.EnabledGlobally {
		for _, k := range dependents(next.Key) {
			if locked[k].EnabledGlobally {
				return &RequiresError{Key: k, Requires: next.Key}
			}
		}
	}
	return nil
}

// CitySet — города флага множеством: без повторов, по возрастанию id, не nil.
func CitySet(ids []uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	out = slices.Compact(out)
	if out == nil {
		out = []uuid.UUID{}
	}
	return out
}

func flagOf(key, description string, globally bool, cities []uuid.UUID, version int64, updated time.Time) Flag {
	return Flag{Key: key, Description: description, EnabledGlobally: globally, EnabledCityIDs: CitySet(cities),
		Version: version, UpdatedAt: updated}
}

// События и аудит правки флага (спека §6.1, §6.4; R18): агрегат feature_flag с детерминированным uuid от ключа,
// версия агрегата — version флага; payload — только ключ, флаг и id городов.
const (
	EventFlagChanged  = "platform.flag_changed"
	AggregateFlag     = "feature_flag"
	ActionFlagChanged = "platform.flag_changed"
)

// FlagChanged — payload platform.flag_changed.
type FlagChanged struct {
	Key             string      `json:"key"`
	EnabledGlobally bool        `json:"enabled_globally"`
	EnabledCityIDs  []uuid.UUID `json:"enabled_city_ids"`
}

// FlagID — uuid агрегата и объекта аудита флага: id.Named("feature_flag", ключ).
func FlagID(key string) uuid.UUID { return id.Named(AggregateFlag, key) }

// ChangedEvent — событие правки для events.Publish (f — флаг после правки).
func ChangedEvent(f Flag) events.Event {
	return events.Event{
		Type: EventFlagChanged, SchemaVersion: 1, AggregateType: AggregateFlag, AggregateID: FlagID(f.Key),
		AggregateVersion: f.Version,
		Payload:          FlagChanged{Key: f.Key, EnabledGlobally: f.EnabledGlobally, EnabledCityIDs: f.EnabledCityIDs},
	}
}
