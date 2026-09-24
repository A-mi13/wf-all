// Package flags читает фича-флаги. Модули зависят от Store, а не от SQL.
package flags

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/flags/flagsdb"
)

var ErrUnknownFlag = errors.New("flags: неизвестный флаг")

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
