// Package rekey — сборщик команды api identity rekey (спека identity §5.4; план I-2, Task 19, R28, N2): перешифровка
// долгоживущих секретов identity текущим ключом кольца API_IDENTITY_SECRET_KEYS под ролью api. Пакет под identity/,
// а не в корне модуля: корень импортируется app, обратный импорт дал бы цикл.
package rekey

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/identity/internal/app"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/keys"
)

// Deps — зависимости команды: пул роли api, часы, кольцо секретов (текущий ключ первым), клиент River только для
// вставки (обязателен для сборки сценариев обслуживания; команда задач не ставит), журнал.
type Deps struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Secrets *keys.Ring
	River   *river.Client[pgx.Tx]
	Log     *slog.Logger
}

// Report — итог ротации (app.RekeyReport): перешифровано, строки по key_id до и после, резервные коды под старыми
// ключами, нечитаемые строки; Clean — старые ключи можно убирать из кольца (по числам; срок 7 дней — у оператора).
type Report = app.RekeyReport

// Run — одна ротация; dryRun — только чтение и пробное открытие, без записи.
func Run(ctx context.Context, d Deps, dryRun bool) (Report, error) {
	a, err := app.NewMaintenance(d.Pool, d.Clock, d.Secrets, d.River, d.Log)
	if err != nil {
		return Report{}, err
	}
	return a.Rekey(ctx, dryRun)
}
