// Package media — загрузка, обработка и отдача файлов (спека docs/superpowers/specs/2026-11-18-media-design.md).
// Корневой пакет — API модуля для владельцев (pitches, economy, results, moderation, players и др.): типы, реестр
// целей, коды ошибок, события и сервис media.New (service.go, upload.go, quota.go, confirm.go, stage.go, read.go —
// прямо на internal/store/mediadb: internal/app работал бы на типах корня — цикл). Правила без базы —
// internal/domain, SQL — queries/ → internal/store/mediadb, картинки — internal/imaging, Lottie — internal/lottie,
// задачи — media/jobs; транзакции владельца — media/intx (спека §2.3).
package media

import (
	"context"
	"image"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"wf/backend/internal/media/internal/domain"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/ratelimit"
)

// Visibility — видимость цели и объекта (media_objects.visibility): публичное отдаётся адресом, закрытое — только
// подписанной ссылкой (§3.5).
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

// State — состояние объекта (§4); переходы — domain.Transition.
type State = domain.State

const (
	StatePendingUpload = domain.StatePendingUpload
	StateUploaded      = domain.StateUploaded
	StateProcessing    = domain.StateProcessing
	StateReady         = domain.StateReady
	StateRejected      = domain.StateRejected
	StateDetached      = domain.StateDetached
	StateTakenDown     = domain.StateTakenDown
)

// RejectCode — причина отклонения (media_objects.reject_code, открытый enum контракта, §7.2).
type RejectCode string

const (
	RejectTooLarge         RejectCode = "too_large"
	RejectUnsupportedType  RejectCode = "unsupported_type"
	RejectTooManyPixels    RejectCode = "too_many_pixels"
	RejectCorrupt          RejectCode = "corrupt"
	RejectContentRejected  RejectCode = "content_rejected"
	RejectUnsafeAnimation  RejectCode = "unsafe_animation"
	RejectProcessingFailed RejectCode = "processing_failed"
)

// InputKind — формат входа цели; итог определяет воркер по байтам, заявленный тип — только ранний отказ (§1.4).
type InputKind string

const (
	InputJPEG   InputKind = "jpeg"
	InputPNG    InputKind = "png"
	InputWebP   InputKind = "webp"
	InputLottie InputKind = "lottie"
)

// Variant — вариант цели: ключ хранилища <object_id>/<Name>.webp. MaxSide 0 — «как есть» (Lottie, share_card);
// Lossy и Quality (1–100) — параметры WebP.
type Variant struct {
	Name    string
	MaxSide int
	Lossy   bool
	Quality int
}

// Purpose — цель загрузки (§2.2): объявляет владелец при сборке бинарника, набор одинаков у api, worker и admin-api.
type Purpose struct {
	Name         string
	Visibility   Visibility
	Inputs       []InputKind
	MaxBytes     int64
	MaxPixels    int64 // 0 — для Lottie; у растра обязателен (защита от «бомбы», §3.3 п. 3)
	Variants     []Variant
	OwnerTypes   []string // типы владельца, к которым прикрепляется объект цели (I-5)
	ContentCheck bool     // вызывать ли порт проверки содержимого (§8)
}

// OwnerRef — владелец объекта: тип (из OwnerTypes цели) и id строки владельца.
type OwnerRef struct {
	Type string
	ID   uuid.UUID
}

// Uploader — кто загрузил: игрок или сотрудник; оба uuid.Nil — система (§3.7). Оба заданы — недопустимо
// (CHECK media_objects_one_uploader).
type Uploader struct {
	UserID, StaffID uuid.UUID
}

// Actor — кто снимает объект (TakeDownInTx): сотрудник или игрок-автор, роль и снимок имени для аудита
// (audit.Entry.ActorLabel: у сотрудника обязателен).
type Actor struct {
	UserID, StaffID uuid.UUID
	Role            string
	Label           string
}

// Declared — заявленные клиентом тип и размер: только ранний отказ (§1.4, §3.2).
type Declared struct {
	ContentType string
	Bytes       int64
}

// Staged — байты прямой загрузки уже во «входящем» без записи в базу (§2.3, CreateDirectInTx).
type Staged struct {
	ObjectID    uuid.UUID
	Purpose     string
	Bytes       int64
	ContentType string
}

// VariantView — вариант для клиента: адрес (публичный или подписанный), тип и размеры.
type VariantView struct {
	URL, ContentType string
	Width, Height    int
}

// Image — изображение для ответа владельца (= схема MediaImage контракта, §7.1).
type Image struct {
	ID         uuid.UUID
	State      State
	RejectCode RejectCode
	Variants   map[string]VariantView
}

// Ticket — билет загрузки (= схема UploadTicket, §7.1): ссылка и поля формы — секрет на срок билета.
type Ticket struct {
	UploadID                  uuid.UUID
	Method, URL               string
	Headers, Fields           map[string]string
	ExpiresAt, CompleteBefore time.Time
	MaxBytes                  int64
	AcceptedTypes             []string
}

// PrivateLink — подписанная ссылка на закрытый вариант и её срок (§3.5).
type PrivateLink struct {
	URL       string
	ExpiresAt time.Time
}

// ContentCheck — порт проверки содержимого (§8); вызывается у целей с ContentCheck.
type ContentCheck interface {
	Check(ctx context.Context, img image.Image) (Verdict, error)
}

// Verdict — итог проверки: Allow или отказ с категорией.
type Verdict struct {
	Allow    bool
	Category string
}

// CheckOff — проверка выключена (М13: проверка вручную): всё allow.
var CheckOff ContentCheck = checkOff{}

type checkOff struct{}

func (checkOff) Check(context.Context, image.Image) (Verdict, error) {
	return Verdict{Allow: true}, nil
}

// Service — API модуля для владельцев (§2.3, §3.1–§3.5): билеты загрузки, проверка перед «завершить», прямая загрузка
// во входящее, чтение и адреса. Меняющее объект на стороне владельца — media/intx в его транзакции.
type Service interface {
	// Available — false в режиме none (§6.4): владелец генерируемого не рисует и не ставит задач.
	Available() bool
	// NewUpload — «начать загрузку» (§3.1 п. 2): ранние проверки заявленного, лимиты игрока (§3.11), объект
	// pending_upload и билет одной транзакцией, затем подпись. Неизвестная цель или тип владельца — ошибка программиста.
	NewUpload(ctx context.Context, purpose string, owner OwnerRef, by Uploader, d Declared) (Ticket, error)
	// ConfirmUpload — до транзакции владельца «завершить» (§3.1 п. 5): объект не pending_upload — nil (повтор);
	// окно прошло — ErrUploadExpired; файла во входящем нет — ErrUploadIncomplete.
	ConfirmUpload(ctx context.Context, uploadID uuid.UUID) error
	// Stage — прямая загрузка (§2.3, §3.6, §3.7): байты во входящее по ключу нового object_id, без записи в базу.
	Stage(ctx context.Context, purpose string, r io.Reader, d Declared) (Staged, error)
	// Image — объект для ответа владельца; нет строки — (Image{}, false, nil).
	Image(ctx context.Context, objectID uuid.UUID) (Image, bool, error)
	// Images — пачкой для списков владельца; отсутствующих id в карте нет.
	Images(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Image, error)
	// PublicURL — публичный адрес ключа (владельцы адресов не склеивают, §3.4).
	PublicURL(key string) string
	// PrivateURL — подписанная ссылка на вариант закрытого ready-объекта на 5 минут (§3.5); иначе ErrObjectUnavailable.
	PrivateURL(ctx context.Context, objectID uuid.UUID, variant string) (PrivateLink, error)
}

// Deps — зависимости Service. Blob.Available = false — режим none. River — клиент только для вставки (задачи
// воркера), Service сам задач не ставит. Limiter — суточный счёт билетов игрока (R19).
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Registry *Registry
	Blob     blob.Opened
	Limiter  ratelimit.Limiter
	River    *river.Client[pgx.Tx]
	Log      *slog.Logger
}
