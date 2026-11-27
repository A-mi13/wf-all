package media

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/media/internal/store/mediadb"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/db"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/id"
	"wf/backend/internal/platform/ratelimit"
)

// NewUpload — «начать загрузку» (§3.1 п. 2, §3.11, R3, R19). Порядок: режим none — ErrUnavailable; цель и тип
// владельца (ошибка программиста); заявленные тип и размер — 422; у игрока — суточный счёт билетов Limiter.Over без
// расхода (исчерпан — 429 ratelimit.exceeded); одна транзакция: PendingLock игрока, живых незавершённых ≥ 5 —
// ErrTooManyPending, объём за сутки сверх предела — ErrQuotaExceeded, объект pending_upload и билет; после коммита —
// Limiter.Add (отказы счёт не тратят, ревью Task 6 m3) и подпись POST. Over и Add — не под замком игрока (лимитер на
// своём соединении, второе соединение внутри транзакции грозит исчерпанием пула): параллельные «начать» у границы
// превышают предел не больше чем на maxPending − 1 (незавершённых больше 5 замок не пустит) — принято. Сбой подписи
// оставляет pending_upload без билета клиенту — его уберёт чистка окна (принято).
func (s *service) NewUpload(ctx context.Context, purpose string, owner OwnerRef, by Uploader, d Declared) (Ticket, error) {
	if !s.Available() {
		return Ticket{}, ErrUnavailable
	}
	p, err := s.purposeFor(purpose, owner)
	if err != nil {
		return Ticket{}, err
	}
	if owner.ID == uuid.Nil {
		return Ticket{}, fmt.Errorf("%w: владелец без id", errProgrammer)
	}
	if by.UserID != uuid.Nil && by.StaffID != uuid.Nil {
		return Ticket{}, fmt.Errorf("%w: два загрузивших", errProgrammer)
	}
	if err := checkDeclared(p, d); err != nil {
		return Ticket{}, err
	}
	player := by.UserID != uuid.Nil
	dayKey := ratelimit.SubjectKey(classUploadDay, auth.Subject{Kind: auth.SubjectUser, ID: by.UserID})
	if player {
		over, err := s.d.Limiter.Over(ctx, dayKey, s.uploadDay)
		if err != nil {
			return Ticket{}, fmt.Errorf("media: суточный счёт билетов: %w", err)
		}
		if over {
			return Ticket{}, httpx.NewError(http.StatusTooManyRequests, httpx.CodeRateLimited).WithRetryAfter(s.dayRetryAfter())
		}
	}
	now := s.d.Clock.Now()
	// срок — в целых секундах: так же его подписывает хранилище (политика POST, DevSigner) — билет, строка и подпись
	// истекают в один момент
	expires := now.Add(uploadTTL).Truncate(time.Second)
	objectID := id.New()
	err = db.InTx(ctx, s.d.Pool, func(ctx context.Context, tx pgx.Tx) error {
		q := mediadb.New(tx)
		if player {
			if err := s.checkPlayerLimits(ctx, q, by.UserID, now, d.Bytes); err != nil {
				return err
			}
		}
		if err := q.InsertObject(ctx, mediadb.InsertObjectParams{ID: objectID, Purpose: p.Name,
			Visibility: string(p.Visibility), UploadedByUser: optID(by.UserID), UploadedByStaff: optID(by.StaffID),
			DeclaredType: d.ContentType, DeclaredBytes: d.Bytes, Now: now}); err != nil {
			return fmt.Errorf("media: объект билета: %w", err)
		}
		if err := q.InsertUpload(ctx, mediadb.InsertUploadParams{ObjectID: objectID, IntendedOwnerType: owner.Type,
			IntendedOwnerID: owner.ID, Method: methodPOST, MaxBytes: d.Bytes, ExpiresAt: expires}); err != nil {
			return fmt.Errorf("media: билет: %w", err)
		}
		return nil
	})
	if err != nil {
		return Ticket{}, err
	}
	if player {
		// билет закоммичен — расход суточного счёта; сбой (база лимитов, отмена) — недосчёт одного билета, не отказ
		if err := s.d.Limiter.Add(context.WithoutCancel(ctx), dayKey, s.uploadDay); err != nil {
			s.d.Log.ErrorContext(ctx, "media: расход суточного счёта билетов", "object_id", objectID.String(), "err", err)
		}
	}
	form, err := s.d.Blob.Presigner.PresignUpload(ctx, blob.UploadSpec{Key: objectID.String(), ContentType: d.ContentType,
		Bytes: d.Bytes, TTL: expires.Sub(now)})
	if err != nil {
		s.d.Log.ErrorContext(ctx, "media: подпись загрузки", "object_id", objectID.String(), "err", err)
		return Ticket{}, fmt.Errorf("media: подпись загрузки: %w", err)
	}
	return Ticket{
		UploadID:       objectID,
		Method:         methodPOST,
		URL:            form.URL,
		Headers:        map[string]string{},
		Fields:         form.Fields,
		ExpiresAt:      expires,
		CompleteBefore: expires.Add(CompleteWindow),
		MaxBytes:       form.MaxBytes,
		AcceptedTypes:  AcceptedTypes(p),
	}, nil
}

func optID(u uuid.UUID) *uuid.UUID {
	if u == uuid.Nil {
		return nil
	}
	return &u
}
