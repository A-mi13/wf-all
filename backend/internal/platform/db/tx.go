package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrNoTx — механизм платформы, которому нужна транзакция из ctx (events.Publish,
// audit.Write), вызван вне db.InTx.
var ErrNoTx = errors.New("db: нужна транзакция из db.InTx")

// TxStarter открывает транзакцию; его реализует *pgxpool.Pool. Передавать нужно именно пул:
// Begin есть и у pgx.Tx, и у *pgx.Conn, а pgx.Tx открыл бы вложенную транзакцию через
// savepoint, то есть молча присоединился бы к внешней — спека §4.4 этого запрещает.
type TxStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type txKey struct{}

// TxHook — действие платформы в начале каждой транзакции db.InTx этого запроса: идемпотентность
// вставляет ключ в бизнес-транзакцию (спека §6.4). Ошибка хука откатывает транзакцию.
type TxHook func(ctx context.Context, tx pgx.Tx) error

type hookKey struct{}

// WithTxHook — хук для всех db.InTx с этим ctx.
func WithTxHook(ctx context.Context, h TxHook) context.Context {
	return context.WithValue(ctx, hookKey{}, h)
}

// InTx исполняет fn в новой транзакции и кладёт её в ctx — оттуда её читают только
// механизмы платформы (события, аудит, идемпотентность), спека §4.4. Транзакция открывается
// всегда новая, даже если в ctx уже есть другая: невидимого присоединения к чужой транзакции
// нет. nil — коммит; ошибка или паника — откат (паника пробрасывается). Хук из ctx
// (WithTxHook) исполняется первым в той же транзакции.
func InTx(ctx context.Context, s TxStarter, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		// !completed — fn не вернулась и не запаниковала: runtime.Goexit (t.FailNow в тесте)
		if !completed || err != nil {
			// WithoutCancel даёт откату завершиться и после отмены ctx: иначе pgx
			// уничтожит соединение, а не вернёт его в пул для повторного использования
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if h, ok := ctx.Value(hookKey{}).(TxHook); ok && h != nil {
		if err = h(txCtx, tx); err != nil {
			completed = true
			return err
		}
	}
	err = fn(txCtx, tx)
	completed = true
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TxFrom — транзакция, которую положил db.InTx.
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
