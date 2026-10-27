package staff

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"wf/backend/internal/platform/queue"
)

// Виды писем сотрудникам (§4.12). Тексты — backend/locales/{ru,en}.json (<вид>.subject, <вид>.body).
const (
	MailInvite        = "staff.invite"         // ref — приглашение, recipient — его учётка
	MailAccessReset   = "staff.access_reset"   // ref — приглашение сброса, recipient — его учётка
	MailLocked        = "staff.locked"         // ref — сессия, на которой закрылась учётка; recipient — владелец или админ
	MailBootstrapped  = "staff.bootstrapped"   // ref — новая учётка владельца; recipient — активный владелец
	MailAdminDisabled = "staff.admin_disabled" // ref — запись аудита staff.disabled; recipient — активный владелец
)

// MailArgs — задача staff.mail (D3): письмо сотруднику собирает и отправляет admin-api по id — в
// аргументах нет ни адресов, ни кодов. V — версия формата аргументов; ставится только NewMailArgs.
type MailArgs struct {
	V         int       `json:"v"`
	Mail      string    `json:"kind"`
	Ref       uuid.UUID `json:"ref"`
	Recipient uuid.UUID `json:"recipient"`
}

func (MailArgs) Kind() string { return "staff.mail" }

// InsertOpts — очередь staff: её обслуживает только admin-api (у воркера нет прав на staff_*).
func (MailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.Staff, MaxAttempts: 10}
}

// NewMailArgs — аргументы письма kind (Mail*) о сущности ref получателю recipient, V = 1.
func NewMailArgs(kind string, ref, recipient uuid.UUID) MailArgs {
	return MailArgs{V: 1, Mail: kind, Ref: ref, Recipient: recipient}
}
