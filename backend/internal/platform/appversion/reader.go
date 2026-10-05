package appversion

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wf/backend/internal/platform/appversion/appversiondb"
)

// Reader — версии платформы; ok=false — строки нет (версии не заведены: публичная ручка
// отдаёт null-поля, спека geo §4.2).
type Reader interface {
	Get(ctx context.Context, p Platform) (Versions, bool, error)
}

type reader struct{ q *appversiondb.Queries }

// NewReader — чтение app_versions; роли api и admin читают её по grants.sql.
func NewReader(pool *pgxpool.Pool) Reader { return reader{q: appversiondb.New(pool)} }

func (r reader) Get(ctx context.Context, p Platform) (Versions, bool, error) {
	row, err := r.q.GetAppVersions(ctx, string(p))
	if errors.Is(err, pgx.ErrNoRows) {
		return Versions{}, false, nil
	}
	if err != nil {
		return Versions{}, false, fmt.Errorf("appversion: %w", err)
	}
	return Versions{
		Platform:    Platform(row.Platform),
		Min:         row.MinVersion,
		Recommended: row.RecommendedVersion,
		StoreURL:    row.StoreUrl,
		Version:     row.Version,
		UpdatedAt:   row.UpdatedAt,
	}, true, nil
}
