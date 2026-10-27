package admin_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/testkit/apitest"
)

// Волна 2, HTTP непредусмотренный 3: ожидающий staff.invite подан, когда имя или почта проходили проверку, а
// к подтверждению правила стали строже (m1: «Иван<ZWJ>Петров» больше не имя). Исполнитель отказывает бизнес-отказом
// — запрос failed закоммичен с кодом, ответ 409 platform.approval_stale (переподать приглашение), учётка не
// создана и не тронута. Раньше — голая FieldError: откат всего, 400 validation.failed по body.display_name
// (поля в approve нет), запрос висел pending до истечения. Повтор тем же ключом — сохранённый 409. Оба пути
// исполнителя: новая учётка и повторное приглашение выключенной (existing_id: имя пишется сбросом доступа).
func TestApproveInviteInvalidPayloadFails(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	e.v = apitest.New(t, oapi.GetSpec)
	joined := "Иван" + string(rune(0x200D)) + "Петров" // до m1 — допустимое имя

	// выключенная учётка для пути existing_id
	gone := e.invite("gone@wf.example", "support")
	wantStatus(t, e.send(http.MethodPost, "/v1/staff/accounts/"+gone.String()+"/disable", a.token, uuid.NewString(),
		confirmed("left_team", "gone@wf.example")), http.StatusOK)

	for _, c := range []struct {
		name, email, field, value string
	}{
		{"имя, новая учётка", "ivan.p@wf.example", "display_name", joined},
		{"почта, новая учётка", "petr.s@wf.example", "email", "petr..s@wf.example"},
		{"имя, выключенная учётка", "gone@wf.example", "display_name", joined},
	} {
		t.Run(c.name, func(t *testing.T) {
			id := e.accepted(e.inviteReq(a.token, "", c.email, "support"))
			e.exec(`UPDATE approvals SET payload = jsonb_set(payload, ARRAY[$2::text], to_jsonb($3::text)) WHERE id = $1`,
				id, c.field, c.value)
			before := e.count(`SELECT count(*) FROM staff_accounts`)
			nameBefore := e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'gone@wf.example' AND status = 'disabled' AND display_name = 'Сотрудник gone'`)

			key := uuid.NewString()
			rec := e.decide("approve", id, b.token, key, "")
			wantProblem(t, rec, http.StatusConflict, httpx.CodeApprovalStale)
			if st, errText := e.approvalRow(id); st != approvals.StatusFailed || errText != httpx.CodeApprovalStale {
				t.Fatalf("запрос %s %q, ждали failed %s", st, errText, httpx.CodeApprovalStale)
			}
			if n := e.count(`SELECT count(*) FROM staff_accounts`); n != before {
				t.Fatalf("учёток стало %d, было %d", n, before)
			}
			if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'gone@wf.example' AND status = 'disabled' AND display_name = 'Сотрудник gone'`); n != nameBefore || n != 1 {
				t.Fatalf("выключенная учётка изменилась: %d", n)
			}
			again := e.decide("approve", id, b.token, key, "")
			if !replayed(again) || !sameJSON(t, again, rec) {
				t.Fatalf("повтор ключом: %d %s", again.Code, again.Body.String())
			}
		})
	}
}
