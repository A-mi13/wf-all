package oapi_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/approvals"
)

// Ожидающий запрос: цели, города, решившего, времени решения, комментария и ошибки нет — null (не
// пустая строка и не нулевой uuid); payload — объект как есть.
func TestApprovalOfPending(t *testing.T) {
	at := time.Date(2026, 10, 6, 12, 20, 0, 0, time.FixedZone("MSK", 3*3600))
	a := approvals.Approval{
		ID: uuid.New(), Action: "staff.invite", Permission: "staff.roles.grant_elevated", Status: approvals.StatusPending,
		Payload:     json.RawMessage(`{"v":1,"email":"ivan.p@wf.example","role":"admin","city_ids":[]}`),
		RequestedBy: uuid.New(), RequestedByLabel: "Ольга В.", RequestedAt: at, ExpiresAt: at.Add(24 * time.Hour),
	}
	got, err := oapi.ApprovalOf(a)
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetId != nil || got.ScopeCityId != nil || got.DecidedBy != nil || got.DecidedAt != nil ||
		got.Comment != nil || got.Error != nil {
		t.Fatalf("пустые поля не null: %+v", got)
	}
	want := map[string]any{"v": float64(1), "email": "ivan.p@wf.example", "role": "admin", "city_ids": []any{}}
	if !reflect.DeepEqual(got.Payload, want) {
		t.Fatalf("payload %v", got.Payload)
	}
	if got.Id != a.ID || got.Action != "staff.invite" || got.Permission != a.Permission || got.Status != "pending" ||
		got.RequestedBy.Id != a.RequestedBy || got.RequestedBy.Label != "Ольга В." {
		t.Fatalf("запрос: %+v", got)
	}
	if got.RequestedAt.Location() != time.UTC || !got.RequestedAt.Equal(at) || got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("время не UTC: %v %v", got.RequestedAt, got.ExpiresAt)
	}
	// в JSON — именно null, а не отсутствующие поля (required + nullable в контракте)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"target_id", "scope_city_id", "decided_by", "decided_at", "comment", "error"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("%s: %v (есть %v) — ждали null", k, v, ok)
		}
	}
}

// Решённый человеком запрос: решивший с подписью, время, комментарий, ошибка, цель, город. payload null
// (и пустой) — пустой объект; не объект — ошибка (контракт: payload — объект).
func TestApprovalOfDecided(t *testing.T) {
	at := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	a := approvals.Approval{
		ID: uuid.New(), Action: "staff.access_reset", Permission: "staff.access.reset", Status: approvals.StatusFailed,
		Payload: json.RawMessage("null"), TargetID: uuid.New(), ScopeCityID: uuid.New(), RequestedBy: uuid.New(),
		RequestedByLabel: "Ольга В.", DecidedBy: uuid.New(), DecidedByLabel: "Борис Н.", DecidedAt: &at,
		Comment: "Сначала обучение", Error: "staff.last_owner",
	}
	got, err := oapi.ApprovalOf(a)
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetId == nil || *got.TargetId != a.TargetID || got.ScopeCityId == nil || *got.ScopeCityId != a.ScopeCityID ||
		got.DecidedBy == nil || got.DecidedBy.Id != a.DecidedBy || got.DecidedBy.Label != "Борис Н." ||
		got.DecidedAt == nil || !got.DecidedAt.Equal(at) || got.Comment == nil || *got.Comment != "Сначала обучение" ||
		got.Error == nil || *got.Error != "staff.last_owner" || got.Payload == nil || len(got.Payload) != 0 {
		t.Fatalf("решённый: %+v", got)
	}
	a.Payload = nil
	if got, err := oapi.ApprovalOf(a); err != nil || got.Payload == nil || len(got.Payload) != 0 {
		t.Fatalf("пустой payload: %v %v", got.Payload, err)
	}
	for _, bad := range []string{`[1,2]`, `"x"`, `1`, `{`} {
		a.Payload = json.RawMessage(bad)
		if _, err := oapi.ApprovalOf(a); err == nil {
			t.Errorf("payload %s принят", bad)
		}
	}
}

// Решила система (просрочка, гашение): decided_by — null при подписи system, время решения есть.
func TestApprovalOfSystemDecided(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 20, 0, 0, time.UTC)
	a := approvals.Approval{
		ID: uuid.New(), Action: "staff.invite", Permission: "staff.roles.grant", Status: approvals.StatusExpired,
		Payload: json.RawMessage(`{"v":1}`), RequestedBy: uuid.New(), RequestedByLabel: "Ольга В.",
		DecidedByLabel: approvals.SystemLabel, DecidedAt: &at, Error: approvals.ErrorInitiatorChanged,
	}
	got, err := oapi.ApprovalOf(a)
	if err != nil {
		t.Fatal(err)
	}
	if got.DecidedBy != nil || got.DecidedAt == nil || got.Status != "expired" || got.Error == nil {
		t.Fatalf("система: %+v", got)
	}
}
