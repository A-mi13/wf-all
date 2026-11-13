package admin_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
)

// Страж «заглушек не осталось» (Task 10b): собранный сервер (настоящие staff, geo и подтверждения, как
// сборка 10c) ни на одной операции контракта не отвечает 501 http.not_implemented. Запрос — годный по
// схеме (строки теста прав), от активного владельца; анонимные — без cookie (вход с cookie погасил бы
// сессию владельца, D9), выход — последним. Ответ хендлера — любой, кроме 501 и отказа конвейера.
// Временный список pendingOps плана B (R17) удалён в Task 16; план I-1 (R2) возвращает его для контракта identity,
// замороженного до кода: операция из pendingOps — ровно 501 http.not_implemented, реализованная в списке — падение.
func TestNoStubOperations(t *testing.T) {
	t.Parallel()
	// действия geo «вдвоём» — как в сборке admin-api (Task 13): без них страны отвечали бы 501
	e := newGeoStaffEnv(t)
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
				stub := rec.Code == http.StatusNotImplemented || strings.Contains(body, `"code":"`+httpx.CodeNotImplemented+`"`)
				switch {
				case slices.Contains(pendingOps, opID(op)):
					if rec.Code != http.StatusNotImplemented || !strings.Contains(body, `"code":"`+httpx.CodeNotImplemented+`"`) {
						t.Errorf("%s: в pendingOps, а ответ %d %s — реализована? убрать из pendingOps", opID(op), rec.Code, body)
					}
					return
				case stub:
					t.Errorf("%s: заглушка вне pendingOps — %d %s", opID(op), rec.Code, body)
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

// pendingOps — операции контракта admin, которые собранный сервер ещё не исполняет: отвечают 501
// http.not_implemented (контракт identity заморожен до кода, этапы спеки выкатываются по очереди — план I-1, R1, R2).
// Реализовали операцию — убрать её отсюда (TestNoStubOperations падает «убрать из pendingOps»). Список и
// TestPendingOpsExpire удаляет последняя задача последнего плана identity.
var pendingOps = []string{
	// Task 4: заявки в капитаны, поиск игрока, сброс 2FA, статистика способов входа — этап 9 спеки identity
	"listCaptainApplications", "getCaptainApplication", "getCaptainApplicationContact", "approveCaptainApplication",
	"rejectCaptainApplication", "getAuthMethodsUsage", "findPlayerByEmail", "requestMfaReset", "cancelMfaReset",
	// связь «сотрудник → учётка игрока» (тег staff, §7.6, R15) — этап 9
	"putStaffPlayerLink", "deleteStaffPlayerLink",
}

// pendingOpsAtFreezeSHA256 — SHA-256 pendingOpsAtFreeze (TestPendingOpsExpire).
const pendingOpsAtFreezeSHA256 = "0f7a931752bda90d7f417c20d4780f855b5a49c8eff3d2ecf914a353881cef68"

// pendingOpsAtFreeze — замороженное множество (R2, R17): после заморозки контракта identity (Task 5 плана I-1) не
// меняется — его SHA-256 сверяется с константой; уменьшается только pendingOps, его подмножество.
var pendingOpsAtFreeze = []string{
	// по алфавиту (TestPendingOpsExpire)
	"approveCaptainApplication", "cancelMfaReset", "deleteStaffPlayerLink", "findPlayerByEmail",
	"getAuthMethodsUsage", "getCaptainApplication", "getCaptainApplicationContact", "listCaptainApplications",
	"putStaffPlayerLink", "rejectCaptainApplication", "requestMfaReset",
}

// pendingStaffOps — операции тега staff, которым можно быть в pendingOps: связь «сотрудник → учётка игрока»
// (спека identity §7.6, R15) реализует этап 9 identity.
var pendingStaffOps = []string{"putStaffPlayerLink", "deleteStaffPlayerLink"}

// Срок временного списка (R2): (а) каждое имя pendingOps есть в контракте; (б) у операции тег identity, кроме ровно
// pendingStaffOps тега staff; (в) список ⊆ pendingOpsAtFreeze; (г) pendingOpsAtFreeze по алфавиту и его SHA-256 =
// константа (R17). Пока списки пусты, проверка не вакуумна за счёт пробы.
func TestPendingOpsExpire(t *testing.T) {
	t.Parallel()
	spec, err := oapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	tags := opTags(spec)
	for _, v := range pendingViolations(tags, pendingOps, pendingOpsAtFreeze) {
		t.Error(v)
	}
	// хэш замороженного множества (R17): вернуть операцию после отката реализации или дописать новую нельзя без
	// правки константы — то есть без контроллера плана; уменьшение pendingOps хэш не трогает
	if !slices.IsSorted(pendingOpsAtFreeze) {
		t.Error("pendingOpsAtFreeze: не по алфавиту")
	}
	if got := frozenSHA256(pendingOpsAtFreeze); got != pendingOpsAtFreezeSHA256 {
		t.Errorf("pendingOpsAtFreeze изменён (SHA-256 %s): замороженное множество правится только через контроллера плана "+
			"identity вместе с pendingOpsAtFreezeSHA256 (план I-1, R2, R17)", got)
	}
	for _, id := range pendingOpsAtFreeze {
		if _, ok := tags[id]; !ok {
			t.Errorf("pendingOpsAtFreeze: %s — нет в контракте", id)
		}
	}
	// проба на подставном контракте: нет операции, чужой тег, staff вне двух разрешённых, вне замороженного множества
	probe := map[string][]string{
		"listGeoCities": {"geo"}, "getStaffMe": {"staff"}, "putStaffPlayerLink": {"staff"},
		"listCaptainApplications": {"identity"},
	}
	got := pendingViolations(probe, []string{"getMe", "listGeoCities", "getStaffMe", "putStaffPlayerLink",
		"listCaptainApplications"}, []string{"listGeoCities", "getStaffMe", "putStaffPlayerLink", "listCaptainApplications"})
	want := []string{
		"pendingOps: getMe — нет в pendingOpsAtFreeze",
		"pendingOps: getMe — нет в контракте",
		"pendingOps: getStaffMe — тег [staff], а заглушки бывают только у identity и связи сотрудника",
		"pendingOps: listGeoCities — тег [geo], а заглушки бывают только у identity и связи сотрудника",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("проба стража:\n%v\nждали:\n%v", got, want)
	}
}

// opTags — теги операций контракта по operationId (lowerCamel).
func opTags(spec *openapi3.T) map[string][]string {
	out := map[string][]string{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			out[opID(op)] = op.Tags
		}
	}
	return out
}

// pendingViolations — нарушения срока списка заглушек по алфавиту.
func pendingViolations(tags map[string][]string, pending, frozen []string) []string {
	var out []string
	for _, id := range pending {
		tag, ok := tags[id]
		switch {
		case !ok:
			out = append(out, "pendingOps: "+id+" — нет в контракте")
		case slices.Equal(tag, []string{"identity"}),
			slices.Equal(tag, []string{"staff"}) && slices.Contains(pendingStaffOps, id):
		default:
			out = append(out, "pendingOps: "+id+" — тег ["+strings.Join(tag, ",")+"], а заглушки бывают только у identity и связи сотрудника")
		}
		if !slices.Contains(frozen, id) {
			out = append(out, "pendingOps: "+id+" — нет в pendingOpsAtFreeze")
		}
	}
	slices.Sort(out)
	return out
}

// frozenSHA256 — SHA-256 списка, имена через перевод строки.
func frozenSHA256(ids []string) string {
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return hex.EncodeToString(sum[:])
}
