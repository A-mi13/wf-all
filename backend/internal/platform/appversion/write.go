package appversion

// Запись версий из админки (план B, Task 15; спека §6.1): список, запись в транзакции вызывающего, событие
// platform.app_versions_changed с детерминированным uuid агрегата (R18). Аудит и событие пишет вызывающий в той же
// транзакции (httpapi/admin).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/appversion/appversiondb"
	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/id"
)

// List — заведённые платформы по имени (незаведённой строки нет: ручка отдаёт её null-полями).
func List(ctx context.Context, q appversiondb.DBTX) ([]Versions, error) {
	rows, err := appversiondb.New(q).ListAppVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("appversion: %w", err)
	}
	out := make([]Versions, 0, len(rows))
	for _, r := range rows {
		out = append(out, versionsOf(r))
	}
	return out, nil
}

// Put — запись версий платформы в транзакции tx (после Validate): строка под FOR UPDATE — «было» (nil — платформа не
// заведена); те же значения — before == after без записи (version не растёт, события и аудита у вызывающего нет);
// иначе правка — version + 1 (updated_at — триггер). Не заведена — вставка: version 1 и updated_at = now (часы
// записывающего). Вставку опередила параллельная транзакция (её строка закоммичена, вставка — ни одной строки) —
// строка перечитывается под FOR UPDATE и запись идёт как правка с её «было» (ревью G7 m2: иначе аудит и событие
// второй выглядели бы созданием с before = null).
func Put(ctx context.Context, tx pgx.Tx, v Versions, now time.Time) (before *Versions, after Versions, err error) {
	q := appversiondb.New(tx)
	for attempt := 0; ; attempt++ {
		cur, err := q.AppVersionsForUpdate(ctx, string(v.Platform))
		switch {
		case err == nil:
			b := versionsOf(cur)
			if b.Min == v.Min && b.Recommended == v.Recommended && b.StoreURL == v.StoreURL {
				return &b, b, nil
			}
			row, err := q.UpdateAppVersions(ctx, appversiondb.UpdateAppVersionsParams{Platform: string(v.Platform),
				MinVersion: v.Min, RecommendedVersion: v.Recommended, StoreUrl: v.StoreURL})
			if err != nil {
				return nil, Versions{}, fmt.Errorf("appversion: запись %s: %w", v.Platform, err)
			}
			return &b, versionsOf(row), nil
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, Versions{}, fmt.Errorf("appversion: %w", err)
		case attempt > 0:
			// строку только что завела другая транзакция, а под FOR UPDATE её нет — удалить строку версий нельзя
			return nil, Versions{}, fmt.Errorf("appversion: строка %s пропала после параллельного заведения", v.Platform)
		}
		row, err := q.InsertAppVersions(ctx, appversiondb.InsertAppVersionsParams{Platform: string(v.Platform),
			MinVersion: v.Min, RecommendedVersion: v.Recommended, StoreUrl: v.StoreURL, UpdatedAt: now})
		switch {
		case err == nil:
			return nil, versionsOf(row), nil
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, Versions{}, fmt.Errorf("appversion: запись %s: %w", v.Platform, err)
		}
		// ON CONFLICT DO NOTHING: строку завела параллельная транзакция — перечитать её как «было»
	}
}

func versionsOf(r appversiondb.AppVersion) Versions {
	return Versions{Platform: Platform(r.Platform), Min: r.MinVersion, Recommended: r.RecommendedVersion,
		StoreURL: r.StoreUrl, Version: r.Version, UpdatedAt: r.UpdatedAt}
}

// Событие и аудит правки версий (R15, R18): агрегат app_versions/<платформа>, версия агрегата — version строки,
// payload — только платформа.
const (
	EventChanged     = "platform.app_versions_changed"
	AggregateVersion = "app_versions"
	ActionChanged    = "platform.app_versions_changed"
)

// Changed — payload platform.app_versions_changed.
type Changed struct {
	Platform Platform `json:"platform"`
}

// AggregateID — uuid агрегата и объекта аудита версий платформы: id.Named("app_versions", платформа).
func AggregateID(p Platform) uuid.UUID { return id.Named(AggregateVersion, string(p)) }

// ChangedEvent — событие записи для events.Publish (v — строка после записи).
func ChangedEvent(v Versions) events.Event {
	return events.Event{Type: EventChanged, SchemaVersion: 1, AggregateType: AggregateVersion,
		AggregateID: AggregateID(v.Platform), AggregateVersion: v.Version, Payload: Changed{Platform: v.Platform}}
}
