// Package intx — операции media в транзакции владельца (спека media §2.3, спека бэкенда §4.4): прикрепление
// загрузки (AttachInTx), прямая загрузка (CreateDirectInTx), открепление (DetachInTx), снятие (TakeDownInTx).
// Транзакция — явным параметром; строки media, задачи River (media.process, media.purge), аудит и событие снятия
// пишутся в ней — откат владельца ничего не оставляет, кроме файла во входящем (его убирает жизненный цикл).
// Импортировать пакет могут только пары archtest.IntxPairs (их добавляют планы владельцев, R11).
//
// Обязанности вызывающего:
//   - tx — транзакция db.InTx из ctx или её SAVEPOINT: аудит и событие берут tx из ctx, запросы — из параметра.
//     Соединение сверяется, чужое — ErrTxMismatch до любых записей;
//   - права пользователя на владельца проверяет владелец заново в момент действия (§3.1 п. 4);
//   - ConfirmUpload (HEAD входящего, сеть) — до транзакции; AttachInTx в сеть не ходит;
//   - замок: строка media_objects берётся FOR UPDATE (ждать) — после строк владельца, как и остальные его записи.
package intx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"wf/backend/internal/media"
	"wf/backend/internal/media/internal/domain"
	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/media/jobs"
	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/clock"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/events"
)

// Deps — зависимости: часы, реестр целей, клиент River только для вставки, публичный адрес хранилища (адреса вариантов
// для Purger в media.purge; "" в режиме none — объектов там нет), журнал. Обязательны все, кроме PublicBaseURL.
type Deps struct {
	Clock         clock.Clock
	Registry      *media.Registry
	River         *river.Client[pgx.Tx]
	PublicBaseURL string
	Log           *slog.Logger
}

// Tx — операции media в транзакции владельца.
type Tx struct{ d Deps }

// ErrTxMismatch — tx из параметра на другом соединении, чем транзакция db.InTx в ctx (или ctx без транзакции): ничего
// не записано.
var ErrTxMismatch = errors.New("media/intx: tx параметра не совпадает с транзакцией ctx")

// errProgrammer — неверный вызов владельцем (неизвестная цель, тип владельца не из цели, Staged не от Stage, причина
// снятия не код): 500, не код клиента.
var errProgrammer = errors.New("media/intx: неверный вызов")

// reasonCode — причина снятия: код, не свободный текст (§2.3, ПД в журнал не попадают).
var reasonCode = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

const (
	actionTakenDown = "media.object_taken_down"
	maxReasonLen    = 100
)

// New — сборка; без зависимости — ошибка (отказ старта, а не паника в запросе).
func New(d Deps) (*Tx, error) {
	if d.Clock == nil || d.Registry == nil || d.River == nil || d.Log == nil {
		return nil, errors.New("media/intx: New без часов, реестра, River или журнала")
	}
	return &Tx{d: d}, nil
}

// sameTx — tx параметра на соединении транзакции db.InTx из ctx (там же события и аудит).
func sameTx(ctx context.Context, tx pgx.Tx) error {
	// соединение, а не объект: SAVEPOINT вызывающего — другой pgx.Tx на том же соединении той же транзакции
	if got, ok := db.TxFrom(ctx); !ok || got.Conn() != tx.Conn() {
		return ErrTxMismatch
	}
	return nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// uploaderOf — загрузивший строки (NULL — uuid.Nil).
func uploaderOf(o mediadb.MediaObject) media.Uploader {
	return media.Uploader{UserID: deref(o.UploadedByUser), StaffID: deref(o.UploadedByStaff)}
}

func ownerOf(o mediadb.MediaObject) media.OwnerRef {
	return media.OwnerRef{Type: deref(o.OwnerType), ID: deref(o.OwnerID)}
}

func hasOwner(o mediadb.MediaObject, owner media.OwnerRef) bool {
	return o.OwnerType != nil && o.OwnerID != nil && *o.OwnerType == owner.Type && *o.OwnerID == owner.ID
}

// lock — строка объекта FOR UPDATE (ждать); нет строки — ok=false.
func lock(ctx context.Context, q *mediadb.Queries, objectID uuid.UUID) (mediadb.MediaObject, bool, error) {
	o, err := q.LockObject(ctx, objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, false, nil
	}
	if err != nil {
		return o, false, fmt.Errorf("media/intx: замок объекта: %w", err)
	}
	return o, true, nil
}

// AttachInTx — «завершить» (§2.3, I-5): строка FOR UPDATE; уже прикреплён к тому же владельцу тем же загрузившим —
// текущий Image (повтор, без второй задачи). Иначе годится только pending_upload с билетом на этого владельца, тем же
// загрузившим и целью, допускающей тип владельца; окно — строго до expires_at + 1 ч (иначе ErrUploadExpired).
// Прочее (нет строки, другое состояние или владелец) — ErrObjectUnavailable. Успех: uploaded, билет удалён, задача
// media.process (уникальна по объекту) — в tx владельца.
func (t *Tx) AttachInTx(ctx context.Context, tx pgx.Tx, objectID uuid.UUID, owner media.OwnerRef, by media.Uploader) (media.Image, error) {
	if err := sameTx(ctx, tx); err != nil {
		return media.Image{}, err
	}
	q := mediadb.New(tx)
	o, ok, err := lock(ctx, q, objectID)
	if err != nil {
		return media.Image{}, err
	}
	if !ok {
		return media.Image{}, media.ErrObjectUnavailable
	}
	p, ok := t.d.Registry.Get(o.Purpose)
	if !ok || !slices.Contains(p.OwnerTypes, owner.Type) || uploaderOf(o) != by {
		return media.Image{}, media.ErrObjectUnavailable
	}
	state := media.State(o.State)
	if state != media.StatePendingUpload {
		// повтор к тому же владельцу: живой или отклонённый — как есть; откреплённый и снятый — уже не его
		if hasOwner(o, owner) && state != media.StateDetached && state != media.StateTakenDown {
			return t.imageOf(ctx, q, o)
		}
		return media.Image{}, media.ErrObjectUnavailable
	}
	u, err := q.GetUpload(ctx, objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return media.Image{}, media.ErrObjectUnavailable
	}
	if err != nil {
		return media.Image{}, fmt.Errorf("media/intx: билет: %w", err)
	}
	if u.IntendedOwnerType != owner.Type || u.IntendedOwnerID != owner.ID {
		return media.Image{}, media.ErrObjectUnavailable
	}
	now := t.d.Clock.Now()
	if !now.Before(u.ExpiresAt.Add(media.CompleteWindow)) {
		return media.Image{}, media.ErrUploadExpired
	}
	if err := domain.Transition(state, media.StateUploaded); err != nil {
		return media.Image{}, err
	}
	row, err := q.AttachObject(ctx, mediadb.AttachObjectParams{ID: objectID, OwnerType: &owner.Type, OwnerID: &owner.ID, Now: &now})
	if errors.Is(err, pgx.ErrNoRows) {
		return media.Image{}, media.ErrObjectUnavailable
	}
	if err != nil {
		return media.Image{}, fmt.Errorf("media/intx: прикрепление: %w", err)
	}
	if err := q.DeleteUpload(ctx, objectID); err != nil {
		return media.Image{}, fmt.Errorf("media/intx: билет: %w", err)
	}
	if err := t.insertProcess(ctx, tx, objectID); err != nil {
		return media.Image{}, err
	}
	return media.Image{ID: row.ID, State: domain.ClientState(media.State(row.State))}, nil
}

func (t *Tx) insertProcess(ctx context.Context, tx pgx.Tx, objectID uuid.UUID) error {
	opts := jobs.ProcessInsertOpts()
	if _, err := t.d.River.InsertTx(ctx, tx, jobs.ProcessArgs{V: 1, ObjectID: objectID}, &opts); err != nil {
		return fmt.Errorf("media/intx: задача обработки: %w", err)
	}
	return nil
}

// imageOf — Image строки (повтор прикрепления): варианты только у ready, адрес — только у публичного.
func (t *Tx) imageOf(ctx context.Context, q *mediadb.Queries, o mediadb.MediaObject) (media.Image, error) {
	state := media.State(o.State)
	img := media.Image{ID: o.ID, State: domain.ClientState(state), RejectCode: media.RejectCode(deref(o.RejectCode))}
	if !domain.ShowVariants(state) {
		return img, nil
	}
	vs, err := q.ListVariants(ctx, o.ID)
	if err != nil {
		return media.Image{}, fmt.Errorf("media/intx: варианты: %w", err)
	}
	img.Variants = make(map[string]media.VariantView, len(vs))
	for _, v := range vs {
		img.Variants[v.Name] = media.VariantView{URL: domain.VariantURL(o.Visibility, t.d.PublicBaseURL, v.BlobKey),
			ContentType: v.ContentType, Width: int(deref(v.Width)), Height: int(deref(v.Height))}
	}
	return img, nil
}

// CreateDirectInTx — прямая загрузка (§2.3, §3.6, §3.7): байты уже во входящем (media.Service.Stage), объект сразу
// uploaded с владельцем, загрузивший — by (оба пусты — система), задача media.process — в tx владельца. Цель не
// допускает тип владельца, Staged не от Stage (размер, тип, id), два загрузивших — ошибка программиста.
func (t *Tx) CreateDirectInTx(ctx context.Context, tx pgx.Tx, s media.Staged, owner media.OwnerRef, by media.Uploader) (media.Image, error) {
	if err := sameTx(ctx, tx); err != nil {
		return media.Image{}, err
	}
	p, ok := t.d.Registry.Get(s.Purpose)
	switch {
	case !ok:
		return media.Image{}, fmt.Errorf("%w: неизвестная цель %q", errProgrammer, s.Purpose)
	case !slices.Contains(p.OwnerTypes, owner.Type) || owner.ID == uuid.Nil:
		return media.Image{}, fmt.Errorf("%w: владелец %q не из цели %q", errProgrammer, owner.Type, s.Purpose)
	case s.ObjectID == uuid.Nil || s.Bytes <= 0 || s.Bytes > p.MaxBytes || !slices.Contains(media.AcceptedTypes(p), s.ContentType):
		return media.Image{}, fmt.Errorf("%w: Staged не от Stage", errProgrammer)
	case by.UserID != uuid.Nil && by.StaffID != uuid.Nil:
		return media.Image{}, fmt.Errorf("%w: два загрузивших", errProgrammer)
	}
	now := t.d.Clock.Now()
	row, err := mediadb.New(tx).InsertDirectObject(ctx, mediadb.InsertDirectObjectParams{ID: s.ObjectID, Purpose: p.Name,
		Visibility: string(p.Visibility), OwnerType: &owner.Type, OwnerID: &owner.ID, UploadedByUser: optID(by.UserID),
		UploadedByStaff: optID(by.StaffID), DeclaredType: s.ContentType, DeclaredBytes: s.Bytes, Now: now})
	if err != nil {
		return media.Image{}, fmt.Errorf("media/intx: объект прямой загрузки: %w", err)
	}
	if err := t.insertProcess(ctx, tx, s.ObjectID); err != nil {
		return media.Image{}, err
	}
	return media.Image{ID: row.ID, State: domain.ClientState(media.State(row.State))}, nil
}

func optID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}

// DetachInTx — открепление владельцем (§2.3): чужой владелец (и незавершённый — владельца ещё нет) —
// ErrObjectUnavailable; конечное и отсутствующая строка — nil без изменений (m3-1: подписчик владельца
// идемпотентен); из uploaded|processing|ready — detached, ended_at, version + 1 и media.purge (варианты и входящее)
// в tx владельца.
func (t *Tx) DetachInTx(ctx context.Context, tx pgx.Tx, objectID uuid.UUID, owner media.OwnerRef) error {
	if err := sameTx(ctx, tx); err != nil {
		return err
	}
	q := mediadb.New(tx)
	o, ok, err := lock(ctx, q, objectID)
	if err != nil || !ok {
		return err
	}
	if !hasOwner(o, owner) {
		return media.ErrObjectUnavailable
	}
	if domain.IsFinal(media.State(o.State)) {
		return nil
	}
	_, err = t.end(ctx, tx, q, o, media.StateDetached, false)
	return err
}

// TakeDownInTx — снятие (§2.3, §3.8): решение модератора или удаление автором. Конечное и отсутствующая строка — nil
// без записей; незавершённый — ErrObjectUnavailable; из uploaded|processing|ready — taken_down, аудит
// media.object_taken_down (актор, объект, причина — код), событие media.object_taken_down с версией строки и
// media.purge с публичными адресами вариантов (Purger) — всё в транзакции решения.
func (t *Tx) TakeDownInTx(ctx context.Context, tx pgx.Tx, objectID uuid.UUID, a media.Actor, reason string) error {
	if err := sameTx(ctx, tx); err != nil {
		return err
	}
	if len(reason) > maxReasonLen || !reasonCode.MatchString(reason) {
		return fmt.Errorf("%w: причина снятия — код вида a_b.c_d", errProgrammer)
	}
	q := mediadb.New(tx)
	o, ok, err := lock(ctx, q, objectID)
	if err != nil || !ok {
		return err
	}
	from := media.State(o.State)
	if domain.IsFinal(from) {
		return nil
	}
	if from == media.StatePendingUpload {
		return media.ErrObjectUnavailable
	}
	row, err := t.end(ctx, tx, q, o, media.StateTakenDown, true)
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, audit.Entry{ActorUserID: a.UserID, ActorStaffID: a.StaffID, ActorRole: a.Role,
		ActorLabel: a.Label, Action: actionTakenDown, ObjectType: media.AggregateObject, ObjectID: objectID,
		Before: map[string]string{"state": string(from)}, After: map[string]string{"state": row.State},
		Reason: reason}); err != nil {
		return fmt.Errorf("media/intx: аудит снятия: %w", err)
	}
	if _, err := events.Publish(ctx, media.ObjectTakenDown(media.ObjectRef{ID: objectID, Version: row.Version},
		row.Purpose, ownerOf(row))); err != nil {
		return fmt.Errorf("media/intx: событие снятия: %w", err)
	}
	return nil
}

// end — переход в конечное (detached | taken_down) под замком строки и задачи media.purge в той же tx: ключи вариантов
// в бакете видимости (с публичными адресами — при withURLs у публичного) и входящее (файл бывает и после обработки:
// публикация удаляет его после коммита, Delete идемпотентен).
func (t *Tx) end(ctx context.Context, tx pgx.Tx, q *mediadb.Queries, o mediadb.MediaObject, to media.State, withURLs bool) (mediadb.MediaObject, error) {
	if err := domain.Transition(media.State(o.State), to); err != nil {
		return mediadb.MediaObject{}, err
	}
	now := t.d.Clock.Now()
	state := string(to)
	row, err := q.SetState(ctx, mediadb.SetStateParams{ID: o.ID, State: state, EndedAt: &now})
	if err != nil {
		return mediadb.MediaObject{}, fmt.Errorf("media/intx: %s: %w", to, err)
	}
	vs, err := q.ListVariants(ctx, o.ID)
	if err != nil {
		return mediadb.MediaObject{}, fmt.Errorf("media/intx: варианты: %w", err)
	}
	purges := []jobs.PurgeArgs{{V: 1, Bucket: jobs.BucketIn, Keys: []string{o.ID.String()}}}
	if len(vs) > 0 {
		v := jobs.PurgeArgs{V: 1, Bucket: jobs.BucketPub}
		if media.Visibility(o.Visibility) == media.VisibilityPrivate {
			v.Bucket = jobs.BucketPriv
		}
		for _, x := range vs {
			v.Keys = append(v.Keys, x.BlobKey)
			if withURLs && v.Bucket == jobs.BucketPub {
				v.PublicURLs = append(v.PublicURLs, t.d.PublicBaseURL+x.BlobKey)
			}
		}
		purges = append(purges, v)
	}
	opts := jobs.PurgeInsertOpts()
	for _, a := range purges {
		if _, err := t.d.River.InsertTx(ctx, tx, a, &opts); err != nil {
			return mediadb.MediaObject{}, fmt.Errorf("media/intx: задача удаления: %w", err)
		}
	}
	return row, nil
}
