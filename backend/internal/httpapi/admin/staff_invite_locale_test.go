package admin_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// IL2 (§4.3): язык новичка задаёт приглашающий — поле locale тела приглашения. Мусор отсекает валидатор
// контракта (400 validation.failed по body.locale, enum), запроса нет; ru | en — в запросе на подтверждение и
// после подтверждения в учётке.
func TestStaffInviteLocale(t *testing.T) {
	t.Parallel()
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	body := func(locale string) string {
		return `{"email":"olga.v@wf.example","display_name":"Ольга В.","role":"support","locale":` + locale + `}`
	}
	for _, bad := range []string{`"de"`, `""`, `"EN"`, `"en-US"`, `null`} {
		wantField(t, e.send(http.MethodPost, "/v1/staff/invites", a.token, uuid.NewString(), body(bad)), "body.locale", "enum")
	}
	if n := e.count(`SELECT count(*) FROM approvals`); n != 0 {
		t.Fatalf("запросов после мусора: %d", n)
	}
	id := e.accepted(e.send(http.MethodPost, "/v1/staff/invites", a.token, uuid.NewString(), body(`"en"`)))
	if n := e.count(`SELECT count(*) FROM approvals WHERE id = $1 AND payload->>'locale' = 'en'`, id); n != 1 {
		t.Fatal("язык не в payload")
	}
	wantStatus(t, e.decide("approve", id, b.token, uuid.NewString(), ""), http.StatusOK)
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'olga.v@wf.example' AND status = 'invited' AND locale = 'en'`); n != 1 {
		t.Fatal("учётка не на языке приглашения")
	}
}
