package app

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/identity"
	"wf/backend/internal/platform/auth"
)

// Тест загрузчика корневого пакета живёт здесь: у app уже есть TestMain с dbtest.Main и окружение под ролью api
// (корневой пакет identity без базы — его TestMain столкнулся бы с тестом Service.User, Task 14).

// §6.1: живая сессия — принципал с действующими ограничениями и живыми допусками капитана; отозванная, просроченная
// (idle и absolute), banned, pending_deletion, deleted, чужой id — auth.ErrSessionInvalid.
func TestSessionLoader(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	loader := identity.NewSessionLoader(e.a.pool, e.clock)
	uid, is := loggedIn(t, e, "loaded", iphone)
	city := uuid.New()
	now := e.clock.Now()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at)
			VALUES (gen_random_uuid(), $1, 'comment', NULL, gen_random_uuid(), now()),
			       (gen_random_uuid(), $1, 'create_match', $2, gen_random_uuid(), now()),
			       (gen_random_uuid(), $1, 'expired_cap', $3, gen_random_uuid(), now())`,
			[]any{uid, now.Add(time.Hour), now.Add(-time.Hour)}},
		{`INSERT INTO user_restrictions (id, user_id, capability, until, source_id, created_at, lifted_at)
			VALUES (gen_random_uuid(), $1, 'lifted_cap', NULL, gen_random_uuid(), now(), now())`, []any{uid}},
		{`INSERT INTO role_assignments (user_id, role, scope_type, scope_id) VALUES ($1, 'captain', 'city', $2)`,
			[]any{uid, city}},
		{`INSERT INTO role_assignments (user_id, role, scope_type, scope_id, revoked_at) VALUES ($1, 'captain', 'city', $2, now())`,
			[]any{uid, uuid.New()}},
	} {
		if _, err := e.owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	p, err := loader.LoadSession(ctx, is.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != uid || p.SessionID != is.SessionID || p.DeviceID == uuid.Nil || p.Status != "active" ||
		!slices.Equal(p.Restrictions, []string{"comment", "create_match"}) ||
		!slices.Equal(p.Roles, []auth.Role{{Name: "captain", ScopeType: "city", ScopeID: city}}) {
		t.Fatalf("%+v", p)
	}
	// веб без метки — DeviceID Nil
	_, webIs := loggedIn(t, e, "loaded_web", web(""))
	if p, err := loader.LoadSession(ctx, webIs.SessionID); err != nil || p.DeviceID != uuid.Nil {
		t.Fatalf("веб: %+v %v", p, err)
	}
	invalid := func(name string, sid uuid.UUID) {
		t.Helper()
		if _, err := loader.LoadSession(ctx, sid); !errors.Is(err, auth.ErrSessionInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	invalid("чужой id", uuid.New())
	for _, st := range []string{"banned", "pending_deletion", "deleted"} {
		u, s := loggedIn(t, e, "loader_"+st, iphone)
		setStatus(t, e, u, st)
		invalid(st, s.SessionID)
	}
	u, s := loggedIn(t, e, "loader_suspended", iphone)
	setStatus(t, e, u, "suspended")
	if p, err := loader.LoadSession(ctx, s.SessionID); err != nil || p.Status != "suspended" {
		t.Fatalf("suspended: %+v %v", p, err)
	}
	_, rev := loggedIn(t, e, "loader_revoked", iphone)
	if err := e.a.Logout(ctx, rev.SessionID); err != nil {
		t.Fatal(err)
	}
	invalid("отозванная", rev.SessionID)
	_, idle := loggedIn(t, e, "loader_idle", iphone)
	if _, err := e.owner.Exec(ctx, `UPDATE sessions SET idle_expires_at = $2 WHERE id = $1`, idle.SessionID, now); err != nil {
		t.Fatal(err)
	}
	invalid("idle ровно сейчас", idle.SessionID)
	_, abs := loggedIn(t, e, "loader_abs", iphone)
	if _, err := e.owner.Exec(ctx, `UPDATE sessions SET absolute_expires_at = $2 WHERE id = $1`, abs.SessionID, now); err != nil {
		t.Fatal(err)
	}
	invalid("absolute ровно сейчас", abs.SessionID)

	// кэш 5 с поверх загрузчика: отзыв виден не позже чем через 5 с по часам кэша
	cached := auth.NewCachedLoader(loader, 5*time.Second, e.clock, 100)
	_, live := loggedIn(t, e, "loader_cached", iphone)
	if _, err := cached.LoadSession(ctx, live.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := e.a.Logout(ctx, live.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.LoadSession(ctx, live.SessionID); err != nil {
		t.Fatalf("в пределах кэша: %v", err)
	}
	e.clock.Advance(5 * time.Second)
	if _, err := cached.LoadSession(ctx, live.SessionID); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Fatalf("через 5 с: %v", err)
	}
}

// Страж (ревью W2 m4): каждая константа domain.RevokedReason (из исходника домена — новая без миграции не
// проскочит) вставляется в sessions.revoked_reason под ролью api — CHECK sessions_revoked_reason_check и домен
// совпадают; значение вне домена CHECK отвергает (проба: CHECK вообще работает).
func TestRevokedReasonsFitCheck(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	reasons := revokedReasonConsts(t)
	if len(reasons) < 9 {
		t.Fatalf("причин в домене %d: разбор исходника не нашёл констант", len(reasons))
	}
	uid := seedUser(t, e.owner, "reasons", "ru")
	ctx := context.Background()
	for _, r := range append(reasons, "nonsense") {
		sid := issue(t, e, uid, web("")).SessionID
		_, err := e.a.pool.Exec(ctx, `UPDATE sessions SET revoked_at = now(), revoked_reason = $2 WHERE id = $1`, sid, r)
		if r == "nonsense" {
			if err == nil {
				t.Fatal("CHECK пропустил причину вне домена")
			}
			continue
		}
		if err != nil {
			t.Errorf("причина %q не проходит CHECK: %v", r, err)
		}
	}
}

// revokedReasonConsts — значения всех констант типа RevokedReason в исходниках пакета domain.
func revokedReasonConsts(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../domain")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".go") || strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../domain", ent.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		{
			for _, d := range f.Decls {
				g, ok := d.(*ast.GenDecl)
				if !ok || g.Tok != token.CONST {
					continue
				}
				for _, s := range g.Specs {
					vs := s.(*ast.ValueSpec)
					if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "RevokedReason" {
						continue
					}
					for _, v := range vs.Values {
						lit, ok := v.(*ast.BasicLit)
						if !ok {
							t.Fatalf("причина не литералом: %T", v)
						}
						s, err := strconv.Unquote(lit.Value)
						if err != nil {
							t.Fatal(err)
						}
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}
