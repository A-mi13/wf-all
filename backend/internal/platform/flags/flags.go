// Package flags читает фича-флаги. Модули зависят от Store, а не от SQL.
package flags

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/flags/flagsdb"
	"wf/backend/internal/platform/httpx"
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
