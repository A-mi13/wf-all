package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"wf/backend/internal/platform/httpx"
)

// User-Agent решения в журнале (неизменяемом) — не длиннее 512 байт (ревью ветки, HTTP Minor 4): requestMeta
// берёт его из httpx.UserAgent. UA 100 КБ — решение проходит, в audit_log ≤ 512 байт (начало заголовка).
func TestDecisionHugeUserAgent(t *testing.T) {
	e := newStaffEnv(t)
	o := e.owners(2)
	a, b := o[0], o[1]
	id := e.accepted(e.inviteReq(a.token, "", "ivan.p@wf.example", "support"))
	huge := "Mozilla/5.0 " + strings.Repeat("x", 100<<10)
	rec := e.decide("reject", id, b.token, "", `{"comment":"Рано"}`, "User-Agent", huge)
	wantStatus(t, rec, http.StatusOK)
	var n int
	var prefix bool
	if err := e.owner.QueryRow(t.Context(), `SELECT octet_length(user_agent), starts_with(user_agent, 'Mozilla/5.0 xxx')
		FROM audit_log WHERE action = 'platform.approval_rejected' AND object_id = $1`, id).Scan(&n, &prefix); err != nil {
		t.Fatal(err)
	}
	if n != httpx.MaxUserAgentBytes || !prefix {
		t.Fatalf("User-Agent в журнале: %d байт (ждали %d), начало сохранено: %v", n, httpx.MaxUserAgentBytes, prefix)
	}
}
