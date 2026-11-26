package media

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"wf/backend/internal/platform/blob"
	"wf/backend/internal/platform/id"
)

// Stage — прямая загрузка (§2.3, §3.6, §3.7): тип из входов цели (ранний отказ), байты читаются не больше
// MaxBytes + 1 — больше предела или пусто — ErrTooLarge; кладутся во входящее по ключу нового object_id. Записи в
// базе нет: объект создаёт CreateDirectInTx в транзакции владельца, её откат оставляет только файл (чистит
// жизненный цикл входящего).
func (s *service) Stage(ctx context.Context, purpose string, r io.Reader, d Declared) (Staged, error) {
	if !s.Available() {
		return Staged{}, ErrUnavailable
	}
	p, ok := s.d.Registry.Get(purpose)
	if !ok {
		return Staged{}, fmt.Errorf("%w: неизвестная цель %q", errProgrammer, purpose)
	}
	if err := checkDeclared(p, Declared{ContentType: d.ContentType, Bytes: 1}); err != nil {
		return Staged{}, err
	}
	if d.Bytes > p.MaxBytes {
		return Staged{}, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(r, p.MaxBytes+1))
	if err != nil {
		return Staged{}, fmt.Errorf("media: чтение прямой загрузки: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > p.MaxBytes {
		return Staged{}, ErrTooLarge
	}
	objectID := id.New()
	if err := s.d.Blob.Buckets.In.Put(ctx, objectID.String(), bytes.NewReader(data),
		blob.PutOptions{ContentType: d.ContentType}); err != nil {
		return Staged{}, fmt.Errorf("media: входящее: %w", err)
	}
	return Staged{ObjectID: objectID, Purpose: p.Name, Bytes: int64(len(data)), ContentType: d.ContentType}, nil
}
