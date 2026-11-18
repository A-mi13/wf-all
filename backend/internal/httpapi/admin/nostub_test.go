package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Страж «заглушек не осталось» (Task 10b): собранный сервер (настоящие staff, geo и подтверждения, как
// сборка 10c) ни на одной операции контракта не отвечает 501 http.not_implemented. Запрос — годный по
// схеме (строки теста прав), от активного владельца; анонимные — без cookie (вход с cookie погасил бы
// сессию владельца, D9), выход — последним. Ответ хендлера — любой, кроме 501 и отказа конвейера.
// Временного списка заглушек нет: pendingOps плана B (R17) удалён в его Task 16, список identity плана I-1 (R2) —
// в Task 12 плана I-4 (R1), когда все операции admin реализованы. Новая операция контракта без хендлера — падение.
func TestNoStubOperations(t *testing.T) {
	t.Parallel()
	// действия geo и identity «вдвоём» — как в сборке admin-api (Task 13; I-2 Task 17): без них страны и сброс 2FA
	// отвечали бы 501
	e := newIdentityStaffEnv(t)
	owner := e.firstLogin("olga.v@wf.example", string(perm.Owner))
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var last []func()
	n := 0
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			req, ok := opsBase[opID(op)]
			if !ok {
				t.Errorf("%s: нет строки запроса (opsBase)", opID(op))
				continue
			}
			token := owner.token
			if op.Extensions["x-permission"] == perm.Anonymous {
				token = ""
			}
			check := func() {
				n++
				key := ""
				if req.method != http.MethodGet {
					key = uuid.NewString()
				}
				rec := e.send(req.method, req.path, token, key, req.body, opsHeaders[opID(op)]...)
				body := rec.Body.String()
				if rec.Code == http.StatusNotImplemented || strings.Contains(body, `"code":"`+httpx.CodeNotImplemented+`"`) {
					t.Errorf("%s: заглушка — %d %s", opID(op), rec.Code, body)
				}
				if rec.Code == http.StatusUnauthorized || strings.Contains(body, `"code":"`+httpx.CodeSetupRequired+`"`) {
					t.Errorf("%s: не дошёл до хендлера — %d %s", opID(op), rec.Code, body)
				}
			}
			if opID(op) == "staffLogout" {
				last = append(last, check)
				continue
			}
			check()
		}
	}
	for _, f := range last {
		f()
	}
	if n != len(opsBase) {
		t.Fatalf("проверено %d операций из %d", n, len(opsBase))
	}
}
