package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNoTx — механизм платформы, которому нужна транзакция из ctx (events.Publish,
// audit.Write), вызван вне db.InTx.
var ErrNoTx = errors.New("db: нужна транзакция из db.InTx")

// TxStarter открывает транзакцию; его реализует *pgxpool.Pool.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type txKey struct{}

// InTx исполняет fn в новой транзакции и кладёт её в ctx — оттуда её читают только
// механизмы платформы (события, аудит, идемпотентность), спека §4.4. Транзакция открывается
// всегда новая, даже если в ctx уже есть другая: невидимого присоединения к чужой транзакции
// нет. nil — коммит; ошибка или паника — откат (паника пробрасывается).
func InTx(ctx context.Context, s TxStarter, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// откат и после отмены ctx: иначе соединение вернётся в пул с открытой транзакцией
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TxFrom — транзакция, которую положил db.InTx.
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
