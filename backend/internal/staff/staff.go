// Package staff — сотрудники админки (спека сотрудников §4): учётки, роль и города модератора,
// вход с паролем и вторым фактором, сессии, блокировка, приглашения. Корневой пакет — API модуля
// для admin-api (P1): зависимости, DTO, ошибки и коды, события, аргументы задачи письма. Сценарии —
// internal/app (импортирует корень), хендлеры — admin, исполнители очереди staff — jobs; корень
// их не импортирует (был бы цикл). Модуль — слой 0: geo он не импортирует, города модератора —
// только внешний ключ в базе.
package staff

import (
	"io"
	"log/slog"
	"net/netip"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/password"
	"wf/backend/internal/platform/ratelimit"
)

// Deps — зависимости сценариев staff; собирает cmd/admin-api (роль БД admin: у api и worker прав
// на staff_* нет).
type Deps struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Keys    *keys.Ring       // ADMIN_STAFF_SECRET_KEYS: секрет TOTP, коды приглашений, HMAC резервных кодов и почты
	Hasher  *password.Hasher // argon2id паролей
	Limiter ratelimit.Limiter
	River   *river.Client[pgx.Tx] // постановка задачи staff.mail в транзакции действия
	Rand    io.Reader             // источник случайности секретов и кодов (crypto/rand.Reader)
	Origin  string                // ADMIN_ORIGIN — ссылка на админку в письмах
	Locales *i18n.Catalog         // backend/locales — тексты писем
	Log     *slog.Logger
}

// Meta — откуда пришёл запрос сотрудника: адрес клиента (peer) и User-Agent как есть — в сессию и
// аудит.
type Meta struct {
	IP        netip.Addr
	UserAgent string
}
