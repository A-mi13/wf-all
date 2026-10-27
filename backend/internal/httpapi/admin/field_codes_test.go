package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/testkit/apitest"
)

// Волна 2, HTTP Minor 2: коды полей, описанные в контракте createStaffInvite и changeStaffRole, — те, что отдаёт
// сервер. body.city_ids: max_items — города у роли не moderator (сценарий; запрос по схеме допустим — ответ
// сверяется с контрактом), maxItems — больше 100 (валидатор схемы); body.email: minLength / maxLength — короче 3 или
// длиннее 320 символов (валидатор, до формата). Запросы вне схемы шлются без сверки e.v (она отвергла бы сам запрос).
func TestStaffFieldCodesMatchContract(t *testing.T) {
	e := newStaffEnv(t)
	a := e.owners(1)[0]
	c := e.city("vremensk")
	sup := e.invite("sup@wf.example", "support")
	path := "/v1/staff/accounts/" + sup.String() + "/role"
	many := make([]uuid.UUID, 101)
	for i := range many {
		many[i] = uuid.New()
	}

	// вне схемы — без сверки запроса
	wantField(t, e.inviteReq(a.token, "", "new@wf.example", "moderator", many...), "body.city_ids", "maxItems")
	wantField(t, e.inviteReq(a.token, "", "a@", "support"), "body.email", "minLength")
	wantField(t, e.inviteReq(a.token, "", strings.Repeat("a", 310)+"@wf.example", "support"), "body.email", "maxLength")
	wantField(t, e.send(http.MethodPut, path, a.token, uuid.NewString(), roleBody("moderator", many...)), "body.city_ids", "maxItems")

	// по схеме допустимо — отказ сценария, ответ сверяется с контрактом
	e.v = apitest.New(t, oapi.GetSpec)
	wantField(t, e.inviteReq(a.token, "", "new@wf.example", "support", c), "body.city_ids", "max_items")
	wantField(t, e.send(http.MethodPut, path, a.token, uuid.NewString(), roleBody("support", c)), "body.city_ids", "max_items")
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("отказы создали запросы: %d", n)
	}
}
