package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/geo"
	"wf/backend/internal/identity/internal/domain"
	"wf/backend/internal/identity/internal/store/identitydb"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/flags"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/ipgeo"
	"wf/backend/internal/platform/keys"
	"wf/backend/internal/platform/ratelimit"
	"wf/backend/internal/platform/risk"
	"wf/backend/internal/platform/token"
)

// Сборка модуля (план I-2, Task 8; I1 ревью плана): cmd/* и internal/httpapi/* собирают identity только через
// корневой пакет — identity/internal/* им не виден (правило Go). Корень не импортирует ни internal/app, ни пакеты-
// сборщики (identity/httpapi, admin, jobs, rekey): app сам импортирует корень (Deps, коды, события), обратный
// импорт — цикл (N2).

// Deps — зависимости сценариев identity у api (identityhttp.New). Дополнять — только через контроллера плана (R5).
type Deps struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Secrets *keys.Ring // API_IDENTITY_SECRET_KEYS: коды, TOTP, данные регистрации, адреса для писем, ключи лимитов
	Devices *keys.Ring // API_IDENTITY_DEVICE_KEYS: HMAC install_id и метки устройства BFF
	Hasher  PasswordHasher
	Limiter ratelimit.Limiter
	// LimiterTx — тот же лимитер на соединении транзакции: внутри db.InTx лимит только через него — второе соединение
	// пула под замком users при исчерпанном пуле ждёт вечно (ревью волны 4, C1)
	LimiterTx func(pgx.Tx) ratelimit.Limiter
	Risk      *risk.Assessor
	Human     humancheck.Verifier // PoW (humancheck.Require по риску)
	Geo       geo.Service
	Flags     *flags.Store
	IPGeo     ipgeo.Locator // город по IP (ipgeo.None — без базы)
	Legal     LegalDocs     // ParseLegalDocuments(API_LEGAL_DOCUMENTS)
	Tokens    *token.Issuer
	// River — клиент только для вставки (queue.InsertConfig): письма ставятся в транзакции действия
	River      *river.Client[pgx.Tx]
	Attestor   Attestor          // до I-3 — AttestOff (R2)
	Providers  ProviderReadiness // до I-3 — NoProviders (R4)
	TOTPIssuer string            // API_IDENTITY_TOTP_ISSUER
	// CaptainChecks — порты условий капитанства, ровно RequiredCaptainChecks (app.New: иначе отказ старта; К1)
	CaptainChecks []CaptainCheck
	Log           *slog.Logger
}

// AdminDeps — зависимости админских сценариев identity у admin-api (app.NewAdmin; план I-4, Task 2): пул роли admin
// (К2 — users и sessions не пишет), часы, клиент River только для вставки (письма — очередь mail), geo на пуле admin,
// порты условий капитанства (перепроверка при одобрении, галочки карточки) и связь «сотрудник → игрок» (R38).
// Кольца identity у admin-api нет (N3).
type AdminDeps struct {
	Pool          *pgxpool.Pool
	Clock         clock.Clock
	River         *river.Client[pgx.Tx]
	Geo           geo.Service
	CaptainChecks []CaptainCheck
	StaffLinks    StaffLinks
	Log           *slog.Logger
}

// WorkerDeps — зависимости задач identity у воркера (письма, анонимизация, исполнение сброса 2FA, чистка): без
// хэшера, токенов и geo (app.NewMaintenance).
type WorkerDeps struct {
	Pool    *pgxpool.Pool
	Clock   clock.Clock
	Secrets *keys.Ring // WORKER_IDENTITY_SECRET_KEYS: то же кольцо, что у api
	River   *river.Client[pgx.Tx]
	Catalog *i18n.Catalog
	Log     *slog.Logger
}

// PasswordHasher — порт хэшера паролей (R3-2): *password.Hasher его реализует; тесты подставляют хэшер на канале
// (argon2 вне транзакции, R31).
type PasswordHasher interface {
	Hash(ctx context.Context, pw string) (string, error)
	Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error)
	VerifyDummy(ctx context.Context, pw string) error
}

// LegalDocs — текущие версии документов (спека §3.16).
type LegalDocs = domain.LegalDocs

// ParseLegalDocuments — API_LEGAL_DOCUMENTS (разбор домена): cmd/api зовёт до подключения к базе.
func ParseLegalDocuments(s string) (LegalDocs, error) { return domain.ParseLegalDocuments(s) }

// DeviceInput — устройство мобильного клиента из тела запроса (DeviceInfo, спека §6.2.3); от BFF не берётся.
// install_id — только в HMAC (keys.Ring.MAC): в логи, аудит, события и аргументы задач не попадает.
type DeviceInput struct {
	Platform         string // ios | android
	InstallID        string
	Model            string
	OSVersion        string
	AppVersion       string // как прислал клиент; при сохранении — domain.NormalizeAppVersion
	DeviceCheckToken string
	Integrity        *DeviceIntegrity
}

// DeviceIntegrity — данные аттестации (§3.8); проверяет Attestor (I-3).
type DeviceIntegrity struct {
	ChallengeID uuid.UUID
	Kind        string // app_attest_attestation | app_attest_assertion | play_integrity
	KeyID       string
	Payload     string // base64 как прислал клиент
}

// AttestVerdict — итог проверки целостности: Checked — данные были и проверены; Passed — проверка прошла;
// RequireHuman — запрос не отклонять, а потребовать PoW (режим observe); Reject — отказ (режим enforce).
type AttestVerdict struct {
	Checked      bool
	Passed       bool
	RequireHuman bool
	Reject       bool
}

// Attestor — порт проверки целостности устройства (App Attest, Play Integrity — I-3).
type Attestor interface {
	Check(ctx context.Context, d DeviceInput) (AttestVerdict, error)
}

// AttestOff — аттестация выключена (до I-3, R2): «данных нет, не требовать».
type AttestOff struct{}

// Check — всегда пустой вердикт без требований.
func (AttestOff) Check(context.Context, DeviceInput) (AttestVerdict, error) {
	return AttestVerdict{}, nil
}

// ProviderReadiness — настроены ли у api провайдеры входа (спека §11.3: способ без конфигурации выключен, R4).
type ProviderReadiness interface {
	Apple() bool
	Google() bool
	Passkeys() bool
}

// NoProviders — ни один внешний способ входа не настроен (до I-3).
type NoProviders struct{}

func (NoProviders) Apple() bool    { return false }
func (NoProviders) Google() bool   { return false }
func (NoProviders) Passkeys() bool { return false }

// ErrUserNotFound — аккаунта нет (Service.User).
var ErrUserNotFound = errors.New("identity: аккаунта нет")

// UserSummary — аккаунт для других модулей, без ПД (спека §10.1).
type UserSummary struct {
	ID, CityID               uuid.UUID
	Nickname, Status, Locale string
}

// CaptainPermit — живой допуск капитана игрока в городе (спека §10.1): ApplicationID — одобренная заявка допуска
// (R25), Probationary — испытательный срок заявки.
type CaptainPermit struct {
	CityID        uuid.UUID
	GrantedAt     time.Time
	ApplicationID uuid.UUID
	Probationary  bool
}

// Service — синхронный API модуля для других модулей (спека §10.1).
type Service interface {
	User(ctx context.Context, id uuid.UUID) (UserSummary, error)
	// CaptainPermits — живые допуски капитана игрока по городам (нет — пусто, не ошибка).
	CaptainPermits(ctx context.Context, userID uuid.UUID) ([]CaptainPermit, error)
}

// New — Service на пуле вызывающего.
func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

type service struct{ pool *pgxpool.Pool }

// User — аккаунт по id из экспортированного представления identity_read_users (у удалённого — ник-заглушка); нет —
// ErrUserNotFound.
func (s *service) User(ctx context.Context, id uuid.UUID) (UserSummary, error) {
	r, err := identitydb.New(s.pool).UserSummary(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSummary{}, ErrUserNotFound
	}
	if err != nil {
		return UserSummary{}, fmt.Errorf("identity: аккаунт: %w", err)
	}
	u := UserSummary{ID: r.ID, Nickname: r.Nickname, Status: string(r.Status), Locale: r.Locale}
	if r.CityID != nil {
		u.CityID = *r.CityID
	}
	return u, nil
}

// CaptainPermits — живые допуски игрока из экспортированного представления identity_read_captain_permits (по времени
// выдачи); отозванные не видны.
func (s *service) CaptainPermits(ctx context.Context, userID uuid.UUID) ([]CaptainPermit, error) {
	rows, err := identitydb.New(s.pool).ReadCaptainPermits(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: допуски капитана: %w", err)
	}
	out := make([]CaptainPermit, 0, len(rows))
	for _, r := range rows {
		p := CaptainPermit{GrantedAt: r.GrantedAt, Probationary: r.IsProbationary}
		if r.CityID != nil {
			p.CityID = *r.CityID
		}
		if r.ApplicationID != nil {
			p.ApplicationID = *r.ApplicationID
		}
		out = append(out, p)
	}
	return out, nil
}
