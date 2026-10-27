package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	geoadmin "wf/backend/internal/geo/admin"
	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Страж «заглушек не осталось» (Task 10b): собранный сервер (настоящие staff, geo и подтверждения, как
// сборка 10c) ни на одной операции контракта не отвечает 501 http.not_implemented. Запрос — годный по
// схеме (строки теста прав), от активного владельца; анонимные — без cookie (вход с cookie погасил бы
// сессию владельца, D9), выход — последним. Ответ хендлера — любой, кроме 501 и отказа конвейера.
func TestNoStubOperations(t *testing.T) {
	e := newStaffEnv(t, func(o *admin.Options) { o.Geo = geoadmin.New(geo.New(o.DB)) })
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
				rec := e.send(req.method, req.path, token, key, req.body)
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
