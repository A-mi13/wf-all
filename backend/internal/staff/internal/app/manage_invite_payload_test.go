package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/staff"
)

// invitePayloadEnv — владельцы a и b, ожидающий запрос staff.invite от a (support, grant), payload которого
// тест переписывает в обход сценария (M1 ревью G3: исполнитель приглашения — та же последняя граница перед
// записью роли, что и исполнитель смены роли).
type invitePayloadEnv struct {
	*menv
	a, b *auth.Staff // инициатор и подтверждающий
	ap   approvals.Approval
}

const invitePayloadEmail = "invitee@wf.example"

func newInvitePayloadEnv(t *testing.T) *invitePayloadEnv {
	t.Helper()
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	ap, err := e.app.Invite(context.Background(), a, staff.InviteInput{
		Email: invitePayloadEmail, DisplayName: "Приглашённый", Role: "support",
	}, metaWeb)
	if err != nil || ap.Permission != "staff.roles.grant" {
		t.Fatalf("%+v %v", ap, err)
	}
	return &invitePayloadEnv{menv: e, a: a, b: b, ap: ap}
}

// patch — поля payload запроса заменяются (jsonb ||), как если бы его записал не сценарий.
func (e *invitePayloadEnv) patch(fields map[string]any) {
	e.t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE approvals SET payload = payload || $2::jsonb WHERE id = $1`, e.ap.ID, string(raw))
}

// wantStaleNothingCreated — approve: failed с platform.approval_stale (и в ответе, и в строке), payload в тексте
// ошибки нет, учётки с почтой приглашения нет, аудита и события staff.invited нет.
func (e *invitePayloadEnv) wantStaleNothingCreated(res approvals.Approval, err error, leak string) {
	e.t.Helper()
	if !errors.Is(err, approvals.ErrStale) || res.Status != approvals.StatusFailed || res.Error != httpx.CodeApprovalStale {
		e.t.Fatalf("исход %+v %v", res, err)
	}
	if st, errText := e.apState(e.ap.ID); st != approvals.StatusFailed || errText != httpx.CodeApprovalStale {
		e.t.Fatalf("строка запроса %s %q", st, errText)
	}
	if leak != "" && strings.Contains(err.Error(), leak) {
		e.t.Fatalf("payload в тексте ошибки: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM staff_accounts WHERE email = $1`, invitePayloadEmail); n != 0 {
		e.t.Fatalf("учёток с почтой приглашения: %d", n)
	}
	if au := e.audits("staff.invited"); len(au) != 0 {
		e.t.Fatalf("аудит %+v", au)
	}
	if n := e.count(`SELECT count(*) FROM outbox WHERE event_type = $1`, staff.EventInvited); n != 0 {
		e.t.Fatalf("событий staff.invited: %d", n)
	}
}

// Роль вне perm.Roles в payload приглашения — исполнитель отказывает бизнес-отказом ErrStale, а не пишет учётку
// (enum базы дал бы 500 и запрос pending до истечения).
func TestExecInviteRejectsUnknownRole(t *testing.T) {
	e := newInvitePayloadEnv(t)
	e.patch(map[string]any{"role": "root"})
	res, err := e.approve(e.b, e.ap.ID)
	e.wantStaleNothingCreated(res, err, "root")
}

// Форма городов роли из payload приглашения: модератор без городов (пусто и null), support с городом, повтор и
// порядок не по возрастанию — ErrStale, ничего не создано. Регресс: модератор с двумя городами по возрастанию и
// support с пустым списком (как пишет сценарий) — исполняются.
func TestExecInviteRejectsCityShape(t *testing.T) {
	for _, c := range []struct {
		name   string
		fields func(c1, c2 uuid.UUID) map[string]any
	}{
		{"moderator без городов", func(_, _ uuid.UUID) map[string]any {
			return map[string]any{"role": "moderator", "city_ids": []uuid.UUID{}}
		}},
		{"moderator без поля городов", func(_, _ uuid.UUID) map[string]any {
			return map[string]any{"role": "moderator", "city_ids": nil}
		}},
		{"support с городом", func(c1, _ uuid.UUID) map[string]any {
			return map[string]any{"city_ids": []uuid.UUID{c1}}
		}},
		{"повтор города", func(c1, _ uuid.UUID) map[string]any {
			return map[string]any{"role": "moderator", "city_ids": []uuid.UUID{c1, c1}}
		}},
		{"города не по возрастанию", func(c1, c2 uuid.UUID) map[string]any {
			s := sortedIDs(c1, c2)
			return map[string]any{"role": "moderator", "city_ids": []uuid.UUID{s[1], s[0]}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newInvitePayloadEnv(t)
			e.patch(c.fields(e.city("kazan"), e.city("perm")))
			res, err := e.approve(e.b, e.ap.ID)
			e.wantStaleNothingCreated(res, err, "")
		})
	}
	t.Run("регресс: support без городов", func(t *testing.T) {
		e := newInvitePayloadEnv(t)
		e.mustApprove(e.b, e.ap.ID)
		if got := e.acct(e.accountID(invitePayloadEmail)); got.Role != "support" || got.Status != "invited" {
			t.Fatalf("учётка %+v", got)
		}
	})
	t.Run("регресс: модератор с двумя городами", func(t *testing.T) {
		e := newInvitePayloadEnv(t)
		cities := sortedIDs(e.city("kazan"), e.city("perm"))
		e.patch(map[string]any{"role": "moderator", "city_ids": cities})
		e.mustApprove(e.b, e.ap.ID)
		id := e.accountID(invitePayloadEmail)
		if got, cs := e.acct(id), e.citiesOf(id); got.Role != "moderator" || len(cs) != 2 || cs[0] != cities[0] || cs[1] != cities[1] {
			t.Fatalf("учётка %+v, города %v", got, cs)
		}
	})
}

// Проверка формы приглашения — до execLocks: отказ не ждёт замка учётки инициатора (его держит третья
// транзакция FOR UPDATE; execLocks встал бы на нём FOR SHARE до истечения ctx).
func TestExecInviteShapeBeforeLocks(t *testing.T) {
	e := newInvitePayloadEnv(t)
	e.patch(map[string]any{"role": "root"})
	release := e.hold(`SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, e.a.StaffID)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := e.svc.Approve(ctx, e.b, "Подтверждающий", apMeta, e.ap.ID, "")
	e.wantStaleNothingCreated(res, err, "root")
}
