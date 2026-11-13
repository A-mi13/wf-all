package queue

import (
	"log/slog"

	"github.com/riverqueue/river"
)

// PoolReserve — соединения пула, которые нужны процессу помимо задач River: LISTEN River (notifier), лидер и
// обслуживание, HTTP (admin-api) или пачка relay (воркер).
const PoolReserve = 3

// Slots — сколько задач клиент River исполняет разом (сумма MaxWorkers очередей): каждая может держать соединение пула.
func Slots(queues map[string]river.QueueConfig) int {
	n := 0
	for _, q := range queues {
		n += q.MaxWorkers
	}
	return n
}

// WarnPool — предупреждение (не отказ, план C R4), если слотов больше, чем пул минус PoolReserve: задачи будут ждать
// соединения, а HTTP и LISTEN — их. Локальные умолчания воркера (28 слотов на пул 10) так и работают; на стенде —
// поднять <PREFIX>_DB_MAX_CONNS в пределах max_connections базы или уменьшить очереди. Возвращает, было ли предупреждение.
func WarnPool(log *slog.Logger, slots int, maxConns int32) bool {
	if slots <= int(maxConns)-PoolReserve {
		return false
	}
	log.Warn("слотов очередей River больше, чем пул соединений минус запас: задачи будут ждать соединения — "+
		"поднимите <PREFIX>_DB_MAX_CONNS (в пределах max_connections базы) или уменьшите очереди",
		"slots", slots, "pool", maxConns, "reserve", PoolReserve)
	return true
}
