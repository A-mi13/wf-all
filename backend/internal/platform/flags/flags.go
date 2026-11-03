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

func (s *Store) EnabledGlobally(ctx context.Context, key string) (bool, error) {
	row, err := s.q.GetFlag(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	if err != nil {
		return false, err
	}
	return row.EnabledGlobally, nil
}

// Enabled — флаг включён глобально или в городе.
func (s *Store) Enabled(ctx context.Context, key string, cityID uuid.UUID) (bool, error) {
	on, err := s.q.IsEnabled(ctx, flagsdb.IsEnabledParams{Key: key, CityID: cityID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnknownFlag
	}
	return on, err
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

// Update — правка флага в транзакции tx вызывающего (событие и аудит пишет он, в той же tx): строка флага (нет —
// ErrUnknownFlag), version не та — ErrStale (и когда значения не меняются); globally nil — не меняется, cities nil —
// не меняются, иначе — множество (повторы схлопываются, порядок по id). Ничего не меняется — before == after без
// записи (version не растёт); иначе UPDATE … WHERE key AND version, version + 1: параллельная правка той же версии
// получает 0 строк — ErrStale. before прочитан при той же version, на которой прошёл UPDATE, — это и есть «было».
func (s *Store) Update(ctx context.Context, tx pgx.Tx, key string, version int64, globally *bool, cities []uuid.UUID) (before, after Flag, err error) {
	q := s.q.WithTx(tx)
	cur, err := q.FlagByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return Flag{}, Flag{}, ErrUnknownFlag
	}
	if err != nil {
		return Flag{}, Flag{}, err
	}
	before = flagOf(cur.Key, cur.Description, cur.EnabledGlobally, cur.EnabledCityIds, cur.Version, cur.UpdatedAt)
	if cur.Version != version {
		return Flag{}, Flag{}, fmt.Errorf("%w: %s — версия %d, прислана %d", ErrStale, key, cur.Version, version)
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
