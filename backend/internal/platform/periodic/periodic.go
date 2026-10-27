// Package periodic — общий список периодических задач платформы (P27). У River один лидер на схему, и
// периодику ставит только он — по своему списку. Поэтому список подключают все клиенты, которые могут
// стать лидером: воркер и admin-api. Задачи уходят в очереди воркера, исполняет их воркер (River не
// проверяет у периодической вставки, есть ли у лидера исполнитель).
package periodic

import (
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/events"
	"wf/backend/internal/platform/humancheck"
	"wf/backend/internal/platform/idempotency"
	"wf/backend/internal/platform/ratelimit"
)

// Jobs — чистки платформы: события, лимиты, антибот, идемпотентность. Новый срез и новые задачи на каждый
// вызов: River хранит указатели и навешивает на них своё состояние.
func Jobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{events.CleanupJob(), ratelimit.CleanupJob(), humancheck.CleanupJob(), idempotency.CleanupJob()}
}
