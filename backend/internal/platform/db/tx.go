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
// after (может быть nil) InTx зовёт, когда исход транзакции известен: true — Commit прошёл,
// false — откат (ошибка хука или fn, паника, Goexit, неудачный Commit). Запись хука живёт ровно
// столько, сколько транзакция: без after механизм не отличил бы закоммиченное от откаченного.
type TxHook func(ctx context.Context, tx pgx.Tx) (after func(committed bool), err error)

type hookKey struct{}

// WithTxHook — хук для всех db.InTx с этим ctx.
func WithTxHook(ctx context.Context, h TxHook) context.Context {
	return context.WithValue(ctx, hookKey{}, h)
}

// InTx исполняет fn в новой транзакции и кладёт её в ctx — оттуда её читают только
// механизмы платформы (события, аудит), спека §4.4; идемпотентность получает транзакцию
// аргументом хука (WithTxHook), а не из ctx. Транзакция открывается всегда новая, даже если
// в ctx уже есть другая: невидимого присоединения к чужой транзакции нет. nil — коммит; ошибка или паника — откат (паника пробрасывается). Хук из ctx
// (WithTxHook) исполняется первым в той же транзакции; его after — после коммита или отката.
func InTx(ctx context.Context, s TxStarter, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	completed, committed := false, false
	var after func(committed bool)
	defer func() {
		p := recover()
		// !completed — fn не вернулась и не запаниковала: runtime.Goexit (t.FailNow в тесте)
		if p != nil || !completed || err != nil {
			// WithoutCancel даёт откату завершиться и после отмены ctx: иначе pgx
			// уничтожит соединение, а не вернёт его в пул для повторного использования
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
		if after != nil {
			after(committed)
		}
		if p != nil {
			panic(p)
		}
	}()
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if h, ok := ctx.Value(hookKey{}).(TxHook); ok && h != nil {
		if after, err = h(txCtx, tx); err != nil {
			completed = true
			return err
		}
	}
	err = fn(txCtx, tx)
	completed = true
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

// TxFrom — транзакция, которую положил db.InTx.
func TxFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
