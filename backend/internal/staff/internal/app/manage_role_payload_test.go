package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/platform/approvals"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/perm"
	"wf/backend/internal/staff/internal/app"
)

// rolePayloadEnv — два владельца и админ x с ожидающим запросом staff.role_change (admin → support,
// grant_elevated), payload которого тест переписывает в обход сценария (W5-back: исполнитель — последняя
// граница перед записью роли).
type rolePayloadEnv struct {
	*menv
	b  *auth.Staff // подтверждающий
	x  member
	ap approvals.Approval
}

func newRolePayloadEnv(t *testing.T) *rolePayloadEnv {
	t.Helper()
	e := newManageEnv(t)
	a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
	b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
	x := e.addMember("admin@wf.example", memberOpts{Role: "admin"})
	ap, err := e.app.ChangeRole(context.Background(), a, x.ID, perm.Support, nil, metaWeb)
	if err != nil || ap == nil || ap.Permission != "staff.roles.grant_elevated" {
		t.Fatalf("%+v %v", ap, err)
	}
	return &rolePayloadEnv{menv: e, b: b, x: x, ap: *ap}
}

// patch — поля payload запроса заменяются (jsonb ||), как если бы его записал не сценарий.
func (e *rolePayloadEnv) patch(fields map[string]any) {
	e.t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE approvals SET payload = payload || $2::jsonb WHERE id = $1`, e.ap.ID, string(raw))
}

// wantStaleNothingApplied — approve: failed с platform.approval_stale (и в ответе, и в строке), payload в
// тексте ошибки нет, роль и города цели прежние, аудита staff.role_changed нет.
func (e *rolePayloadEnv) wantStaleNothingApplied(res approvals.Approval, err error, leak string) {
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
	if got := e.acct(e.x.ID); got.Role != "admin" {
		e.t.Fatalf("роль цели %+v", got)
	}
	if cs := e.citiesOf(e.x.ID); len(cs) != 0 {
		e.t.Fatalf("города цели %v", cs)
	}
	if au := e.audits("staff.role_changed"); len(au) != 0 {
		e.t.Fatalf("аудит %+v", au)
	}
}

// Роль вне perm.Roles в payload (и «стало», и «было») — исполнитель отказывает бизнес-отказом ErrStale,
// а не пишет роль (enum базы дал бы 500 и запрос pending до истечения).
func TestExecRoleChangeRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		fields map[string]any
	}{
		{"role", map[string]any{"role": "root"}},
		{"from_role", map[string]any{"from_role": "root"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newRolePayloadEnv(t)
			e.patch(c.fields)
			res, err := e.approve(e.b, e.ap.ID)
			e.wantStaleNothingApplied(res, err, "root")
		})
	}
}

// Форма городов роли из payload: модератор без городов, админ с городом, повтор города — ErrStale, ничего не
// записано. Регресс: модератор с двумя городами по возрастанию (как пишет сценарий) — исполняется.
func TestExecRoleChangeRejectsCityShape(t *testing.T) {
	t.Parallel()
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
		{"admin с городом", func(c1, _ uuid.UUID) map[string]any {
			return map[string]any{"role": "admin", "city_ids": []uuid.UUID{c1}}
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
			t.Parallel()
			e := newRolePayloadEnv(t)
			e.patch(c.fields(e.city("kazan"), e.city("perm")))
			res, err := e.approve(e.b, e.ap.ID)
			e.wantStaleNothingApplied(res, err, "")
		})
	}
	t.Run("регресс: модератор с двумя городами", func(t *testing.T) {
		t.Parallel()
		e := newRolePayloadEnv(t)
		cities := sortedIDs(e.city("kazan"), e.city("perm"))
		e.patch(map[string]any{"role": "moderator", "city_ids": cities})
		e.mustApprove(e.b, e.ap.ID)
		if got := e.acct(e.x.ID); got.Role != "moderator" || !slices.Equal(e.citiesOf(e.x.ID), cities) {
			t.Fatalf("учётка %+v, города %v", got, e.citiesOf(e.x.ID))
		}
	})
}

// Проверка формы — до execLocks: отказ не ждёт замка стража владельцев (его держит третья транзакция).
func TestExecRoleChangeShapeBeforeLocks(t *testing.T) {
	t.Parallel()
	e := newRolePayloadEnv(t)
	e.patch(map[string]any{"role": "root"})
	release := e.hold(`SELECT pg_advisory_xact_lock($1)`, app.OwnersLockKey)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := e.svc.Approve(ctx, e.b, "Подтверждающий", apMeta, e.ap.ID, "")
	e.wantStaleNothingApplied(res, err, "root")
}

// Города «было» из payload (M2 ревью G3): сценарий пишет их из базы по возрастанию и без повторов (citiesOf), иначе
// payload не от сценария — ErrStale ДО execLocks (замок владельцев держит третья транзакция: отказ не ждёт его).
// Подложный неотсортированный список при той же роли «было» и «стало» давал rolePermission «изменение» и проводил
// смену роли, которой нет (версия +1, аудит). Регресс: null у не-модератора (старый формат и nil-срез) исполняется.
func TestExecRoleChangeRejectsFromCityShape(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		fields func(s []uuid.UUID) map[string]any
	}{
		{"было не по возрастанию", func(s []uuid.UUID) map[string]any {
			return map[string]any{"from_role": "moderator", "from_city_ids": []uuid.UUID{s[1], s[0]}, "role": "moderator", "city_ids": s}
		}},
		{"повтор в было", func(s []uuid.UUID) map[string]any {
			return map[string]any{"from_role": "moderator", "from_city_ids": []uuid.UUID{s[0], s[0], s[1]}, "role": "moderator", "city_ids": s}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newRolePayloadEnv(t)
			e.patch(c.fields(sortedIDs(e.city("kazan"), e.city("perm"))))
			release := e.hold(`SELECT pg_advisory_xact_lock($1)`, app.OwnersLockKey)
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := e.svc.Approve(ctx, e.b, "Подтверждающий", apMeta, e.ap.ID, "")
			e.wantStaleNothingApplied(res, err, "")
		})
	}
	t.Run("регресс: было null", func(t *testing.T) {
		t.Parallel()
		e := newRolePayloadEnv(t)
		e.patch(map[string]any{"from_city_ids": nil})
		e.mustApprove(e.b, e.ap.ID)
		if got := e.acct(e.x.ID); got.Role != "support" {
			t.Fatalf("учётка %+v", got)
		}
	})
}

// «Было» в нормальной форме, но не то, что в базе (m1 ревью U1): под замком исполнитель сверяет роль и города цели
// с from_role/from_city_ids payload — расхождение значит «payload не от сценария» (версия цели та же, значит, база с
// запроса не менялась): ErrStale, ничего не применено. Иначе право пересчитывалось по подложному «было», а
// применялось «было» из базы — та же пустая или чужая смена роли, что у неотсортированного списка.
func TestExecRoleChangeRejectsFromMismatch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		fields func(s []uuid.UUID) map[string]any
	}{
		{"роль было не та", func(s []uuid.UUID) map[string]any {
			// цель — admin без городов, «было» — модератор с теми же (пустыми) городами: отказ даёт только сверка роли
			// (m4 повторного ревью U1), право revoke/grant вместо grant_elevated
			return map[string]any{"from_role": "moderator", "from_city_ids": []uuid.UUID{}, "role": "moderator", "city_ids": s}
		}},
		{"города было не те", func(s []uuid.UUID) map[string]any {
			// цель — admin без городов, «было» — admin с городом (отсортированный список)
			return map[string]any{"from_city_ids": s[:1]}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newRolePayloadEnv(t)
			e.patch(c.fields(sortedIDs(e.city("kazan"), e.city("perm"))))
			res, err := e.approve(e.b, e.ap.ID)
			e.wantStaleNothingApplied(res, err, "")
		})
	}
}

// Регресс I1 ревью U1: законная смена роли модератора с двумя городами «было» (непустой список в нормальной форме)
// исполняется — проверка формы «было» не отвергает законные списки длиной ≥ 2.
func TestExecRoleChangeFromTwoCities(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		role  perm.Role
		extra bool // добавить третий город
	}{
		{"модератор [c1 c2] → [c1 c2 c3]", perm.Moderator, true},
		{"модератор [c1 c2] → admin", perm.Admin, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newManageEnv(t)
			a := e.as(e.addMember("owner.a@wf.example", memberOpts{Role: "owner"}))
			b := e.as(e.addMember("owner.b@wf.example", memberOpts{Role: "owner"}))
			c1, c2, c3 := e.city("kazan"), e.city("perm"), e.city("omsk")
			m := e.addMember("mod@wf.example", memberOpts{Role: "moderator", Cities: []uuid.UUID{c1, c2}})
			v := e.acct(m.ID).Version
			var to []uuid.UUID
			if c.extra {
				to = []uuid.UUID{c1, c2, c3}
			}
			ap, err := e.app.ChangeRole(context.Background(), a, m.ID, c.role, to, metaWeb)
			if err != nil || ap == nil {
				t.Fatalf("%+v %v", ap, err)
			}
			var p struct {
				From []uuid.UUID `json:"from_city_ids"`
			}
			if err := json.Unmarshal(ap.Payload, &p); err != nil || len(p.From) != 2 {
				t.Fatalf("from_city_ids %v %v — ждали два города «было»", p.From, err)
			}
			e.mustApprove(b, ap.ID)
			got := e.acct(m.ID)
			if got.Role != string(c.role) || got.Version != v+1 || !slices.Equal(e.citiesOf(m.ID), sortedIDs(to...)) {
				t.Fatalf("учётка %+v (версия была %d), города %v", got, v, e.citiesOf(m.ID))
			}
		})
	}
}
