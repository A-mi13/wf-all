package media

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/ratelimit"
)

// Сроки сценариев (§3.1, §3.5, §3.11).
const (
	// uploadTTL — срок билета: подпись загрузки и media_uploads.expires_at.
	uploadTTL = 15 * time.Minute
	// CompleteWindow — окно «завершить» после срока билета (медленная загрузка, начатая до срока; m2): принимается
	// строго до expires_at + CompleteWindow.
	CompleteWindow = time.Hour
	// maxPending — незавершённых живых билетов у игрока одновременно.
	maxPending = 5
	// privateLinkTTL и privateCacheControl — подписанная ссылка на закрытое (§3.5).
	privateLinkTTL      = 5 * time.Minute
	privateCacheControl = "private, no-store"
	// methodPOST — метод билета (R3: только подписанный POST с политикой точного размера).
	methodPOST = "POST"
)

// Классы суточных лимитов игрока (R19), ключ — ratelimit.SubjectKey.
const (
	classUploadDay = "media_upload_day"
	classBytesDay  = "media_bytes_day"
)

// errProgrammer — неверный вызов владельцем (неизвестная цель, тип владельца не из цели, два загрузивших): 500, не
// код клиента.
var errProgrammer = errors.New("media: неверный вызов")

type service struct {
	d         Deps
	q         *mediadb.Queries
	uploadDay ratelimit.Policy
	bytesDay  ratelimit.Policy
}

// New — сборка; без обязательной зависимости — ошибка (отказ старта). При Blob.Available нужны бакет входящего и
// подпись.
func New(d Deps) (Service, error) {
	switch {
	case d.Pool == nil, d.Clock == nil, d.Registry == nil, d.Limiter == nil, d.Log == nil:
		return nil, errors.New("media: New без пула, часов, реестра, лимитера или журнала")
	case d.Blob.Available && (d.Blob.Buckets.In == nil || d.Blob.Presigner == nil):
		return nil, errors.New("media: хранилище без бакета входящего или подписи")
	}
	rules := d.Rules
	if rules == nil {
		rules = ratelimit.DefaultRules()
	}
	day, ok1 := rules[classUploadDay]
	bytes, ok2 := rules[classBytesDay]
	// класса нет — отказ сборки: нулевая политика молча выключила бы предел (off задаётся явно — ParseRules)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("media: нет правил %s / %s", classUploadDay, classBytesDay)
	}
	if err := errors.Join(day.User.Validate(), bytes.User.Validate()); err != nil {
		return nil, fmt.Errorf("media: правила %s / %s: %w", classUploadDay, classBytesDay, err)
	}
	return &service{d: d, q: mediadb.New(d.Pool), uploadDay: day.User, bytesDay: bytes.User}, nil
}

// dayRetryAfter — Retry-After исчерпанного суточного счёта: интервал политики (следующий билет — не позже; у Over нет
// точного срока, как у отказов сессии admin-api).
func (s *service) dayRetryAfter() time.Duration {
	return s.uploadDay.Period / time.Duration(s.uploadDay.Limit)
}

func (s *service) Available() bool { return s.d.Blob.Available }

func (s *service) PublicURL(key string) string { return s.d.Blob.PublicBaseURL + key }

// mimeOf — MIME входа цели: заявленный тип сверяется с ним (ранний отказ) и подписывается в политике загрузки.
var mimeOf = map[InputKind]string{
	InputJPEG:   "image/jpeg",
	InputPNG:    "image/png",
	InputWebP:   "image/webp",
	InputLottie: "application/json",
}

// AcceptedTypes — MIME входов цели (Ticket.AcceptedTypes; CreateDirectInTx сверяет с ним Staged).
func AcceptedTypes(p Purpose) []string {
	out := make([]string, 0, len(p.Inputs))
	for _, in := range p.Inputs {
		out = append(out, mimeOf[in])
	}
	return out
}

// purposeFor — цель и проверка типа владельца: нарушение — ошибка программиста.
func (s *service) purposeFor(name string, owner OwnerRef) (Purpose, error) {
	p, ok := s.d.Registry.Get(name)
	if !ok {
		return Purpose{}, fmt.Errorf("%w: неизвестная цель %q", errProgrammer, name)
	}
	if !slices.Contains(p.OwnerTypes, owner.Type) {
		return Purpose{}, fmt.Errorf("%w: тип владельца %q не из цели %q", errProgrammer, owner.Type, name)
	}
	return p, nil
}

// checkDeclared — ранний отказ по заявленному (§1.4): тип из входов цели, размер 1…MaxBytes.
func checkDeclared(p Purpose, d Declared) error {
	if !slices.Contains(AcceptedTypes(p), d.ContentType) {
		return ErrUnsupportedType
	}
	if d.Bytes <= 0 || d.Bytes > p.MaxBytes {
		return ErrTooLarge
	}
	return nil
}
