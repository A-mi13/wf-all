package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/identity"
	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/platform/testkit/apitest"
	"wf/backend/internal/staff"
)

// identityPlayers — порт staff.PlayerDirectory тестов над настоящим identity.Service (как адаптер cmd/admin-api: нет
// или deleted — ok = false).
type identityPlayers struct{ svc identity.Service }

func (p identityPlayers) Player(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	u, err := p.svc.User(ctx, userID)
	if errors.Is(err, identity.ErrUserNotFound) {
		return "", false, nil
	}
	if err != nil || u.Status == "deleted" {
		return "", false, err
	}
	return u.Nickname, true, nil
}

func playerLinkPath(staffID uuid.UUID) string {
	return "/v1/staff/accounts/" + staffID.String() + "/player-link"
}

func playerLinkBody(uid uuid.UUID) string { return `{"user_id":"` + uid.String() + `"}` }

// TestStaffPlayerLinkHTTP — связь «сотрудник → учётка игрока» через весь конвейер (план I-4, Task 6): PUT — 200 по
// контракту, повтор ключа — тот же ответ, карточка владельцу — с player_link, админу — без; коды 404, 409, 422;
// админ — 403 конвейера; DELETE — 204 (и без связи — 204).
func TestStaffPlayerLinkHTTP(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	e.v = apitest.New(t, oapi.GetSpec) // ответы — сверка с контрактом (m3 итогового ревью Б)
	owner := e.firstLogin("olga.v@wf.example", string(perm.Owner))
	adm := e.firstLogin("admin@wf.example", string(perm.Admin))
	sup := e.firstLogin("sup@wf.example", string(perm.Support))
	other := e.firstLogin("other@wf.example", string(perm.Support))
	p := e.player("http_link")

	key := uuid.NewString()
	rec := e.send(http.MethodPut, playerLinkPath(sup.id), owner.token, key, playerLinkBody(p))
	wantStatus(t, rec, http.StatusOK)
	var got struct {
		UserID   uuid.UUID `json:"user_id"`
		Nickname string    `json:"nickname"`
		LinkedAt string    `json:"linked_at"`
		Label    string    `json:"linked_by_label"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.UserID != p || got.Nickname != "http_link" ||
		got.LinkedAt == "" || got.Label == "" {
		t.Fatalf("ответ: %v %s", err, rec.Body.String())
	}
	again := e.send(http.MethodPut, playerLinkPath(sup.id), owner.token, key, playerLinkBody(p))
	var replay struct {
		UserID   uuid.UUID `json:"user_id"`
		LinkedAt string    `json:"linked_at"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &replay); err != nil || again.Code != http.StatusOK ||
		replay.UserID != p || replay.LinkedAt != got.LinkedAt {
		t.Fatalf("повтор ключа: %d %s", again.Code, again.Body.String())
	}

	card := e.send(http.MethodGet, "/v1/staff/accounts/"+sup.id.String(), owner.token, "", "")
	wantStatus(t, card, http.StatusOK)
	if !strings.Contains(card.Body.String(), `"player_link":{`) || !strings.Contains(card.Body.String(), `"nickname":"http_link"`) {
		t.Fatalf("карточка владельцу: %s", card.Body.String())
	}
	card = e.send(http.MethodGet, "/v1/staff/accounts/"+sup.id.String(), adm.token, "", "")
	wantStatus(t, card, http.StatusOK)
	if strings.Contains(card.Body.String(), "player_link") {
		t.Fatalf("карточка админу несёт связь: %s", card.Body.String())
	}

	wantProblem(t, e.send(http.MethodPut, playerLinkPath(uuid.New()), owner.token, uuid.NewString(), playerLinkBody(p)),
		http.StatusNotFound, staff.CodeNotFound)
	wantProblem(t, e.send(http.MethodPut, playerLinkPath(other.id), owner.token, uuid.NewString(), playerLinkBody(p)),
		http.StatusConflict, staff.CodePlayerLinkTaken)
	wantProblem(t, e.send(http.MethodPut, playerLinkPath(other.id), owner.token, uuid.NewString(), playerLinkBody(uuid.New())),
		http.StatusUnprocessableEntity, staff.CodePlayerNotFound)
	wantProblem(t, e.send(http.MethodPut, playerLinkPath(other.id), adm.token, uuid.NewString(), playerLinkBody(e.player("http_x"))),
		http.StatusForbidden, httpx.CodeForbidden)
	wantProblem(t, e.send(http.MethodDelete, playerLinkPath(uuid.New()), owner.token, uuid.NewString(), ""),
		http.StatusNotFound, staff.CodeNotFound)

	wantStatus(t, e.send(http.MethodDelete, playerLinkPath(sup.id), owner.token, uuid.NewString(), ""), http.StatusNoContent)
	wantStatus(t, e.send(http.MethodDelete, playerLinkPath(sup.id), owner.token, uuid.NewString(), ""), http.StatusNoContent)
	if n := e.count(`SELECT count(*) FROM staff_player_links`); n != 0 {
		t.Fatalf("связей %d", n)
	}
}

// TestMfaResetInitiatorLinkedBetweenRequestAndApprove — хвост I-2 (R14): сотрудник A запросил сброс 2FA игрока P,
// владелец связал A с P, сотрудник B подтверждает — запрос failed (initiator_changed): связь подняла версию учётки A,
// 409 platform.approval_stale, сброса нет.
func TestMfaResetInitiatorLinkedBetweenRequestAndApprove(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	o := e.owners(3)
	a, b, own := o[0], o[1], o[2]
	uid := e.player("reset_link_race")
	ap := e.accepted(e.send(http.MethodPost, mfaResetPath(uid), a.token, uuid.NewString(), mfaResetBody))
	wantStatus(t, e.send(http.MethodPut, playerLinkPath(a.id), own.token, uuid.NewString(), playerLinkBody(uid)), http.StatusOK)
	wantProblem(t, e.decide("approve", ap, b.token, "", ""), http.StatusConflict, httpx.CodeApprovalStale)
	if st, errText := e.approvalRow(ap); st != approvals.StatusFailed || errText != approvals.ErrorInitiatorChanged {
		t.Fatalf("запрос: %s %q", st, errText)
	}
	if e.count(`SELECT count(*) FROM mfa_resets`) != 0 {
		t.Fatal("сброс назначен по запросу инициатора, связанного с игроком")
	}
}

// TestPutPlayerLinkDeletedBeforeResponse — m2 ревью Task 6: PUT связи закоммитился, и до сборки его ответа DELETE из
// второй вкладки снял связь. PUT обязан ответить 200 тем, что сделал (ответ — из данных транзакции, не перечтением),
// а повтор ключа — тем же ответом.
func TestPutPlayerLinkDeletedBeforeResponse(t *testing.T) {
	t.Parallel()
	e := newIdentityStaffEnv(t)
	owner := e.firstLogin("olga.v@wf.example", string(perm.Owner))
	sup := e.firstLogin("sup@wf.example", string(perm.Support))
	p := e.player("link_cut")

	var fired atomic.Bool // не sync.Once: коммит DELETE зовёт тот же шов изнутри (Once — взаимная блокировка)
	deleted := false
	f := func(context.Context) {
		if fired.CompareAndSwap(false, true) { // коммит PUT: вторая вкладка снимает связь до ответа PUT
			rec := e.send(http.MethodDelete, playerLinkPath(sup.id), owner.token, uuid.NewString(), "")
			deleted = rec.Code == http.StatusNoContent
		}
	}
	e.commits.onCommit.Store(&f)
	key := uuid.NewString()
	rec := e.send(http.MethodPut, playerLinkPath(sup.id), owner.token, key, playerLinkBody(p))
	e.commits.onCommit.Store(nil)
	if !deleted {
		t.Fatal("шов не сработал: DELETE не исполнился между коммитом и ответом PUT")
	}
	wantStatus(t, rec, http.StatusOK)
	var got struct {
		UserID   uuid.UUID `json:"user_id"`
		Nickname string    `json:"nickname"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.UserID != p || got.Nickname != "link_cut" {
		t.Fatalf("ответ PUT: %v %s", err, rec.Body.String())
	}
	again := e.send(http.MethodPut, playerLinkPath(sup.id), owner.token, key, playerLinkBody(p))
	if !replayed(again) || again.Code != http.StatusOK || !sameJSON(t, again, rec) {
		t.Fatalf("повтор ключа: %d %s", again.Code, again.Body.String())
	}
}
