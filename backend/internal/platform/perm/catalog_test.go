package perm_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// readActions — действия прав чтения; любое другое действие меняет состояние (Mutating).
var readActions = map[string]bool{"read": true, "search": true}

func action(key string) string { return key[strings.LastIndexByte(key, '.')+1:] }

// Права среза — спека §2.5 дословно: владелец, админ, модератор, поддержка.
var spec25 = []struct{ key, owner, admin, moderator, support string }{
	{"staff.roles.read", "allow", "allow", "allow", "allow"},
	{"staff.accounts.read", "allow", "allow", "deny", "deny"},
	{"staff.roles.grant_elevated", "dual", "deny", "deny", "deny"},
	{"staff.roles.grant", "dual", "dual", "deny", "deny"},
	{"staff.roles.revoke_owner", "dual", "deny", "deny", "deny"},
	{"staff.roles.revoke_admin", "allow", "deny", "deny", "deny"},
	{"staff.roles.revoke", "allow", "allow", "deny", "deny"},
	{"staff.access.reset_elevated", "dual", "deny", "deny", "deny"},
	{"staff.access.reset", "dual", "dual", "deny", "deny"},
	{"staff.invites.resend", "allow", "allow", "deny", "deny"},
	{"staff.sessions.revoke", "allow", "allow", "deny", "deny"},
	{"audit.log.read", "allow", "allow", "scoped", "allow"},
	{"geo.places.search", "allow", "allow", "deny", "deny"},
	{"geo.cities.read", "allow", "allow", "scoped", "allow"},
	{"geo.cities.activate", "allow", "allow", "deny", "deny"},
	{"geo.cities.manage", "allow", "allow", "deny", "deny"},
	{"geo.cities.rename", "allow", "allow", "scoped", "deny"},
	{"geo.districts.manage", "allow", "allow", "scoped", "deny"},
	{"geo.countries.read", "allow", "allow", "allow", "allow"},
	{"geo.countries.manage", "allow", "allow", "deny", "deny"},
	{"geo.imports.read", "allow", "allow", "deny", "allow"},
	{"geo.imports.run", "allow", "allow", "deny", "deny"},
	{"platform.versions.read", "allow", "allow", "allow", "allow"},
	{"platform.versions.manage", "allow", "allow", "deny", "deny"},
	{"platform.flags.read", "allow", "allow", "allow", "allow"},
	{"platform.flags.manage", "allow", "allow", "deny", "deny"},
}

// ownerOnly — права, которые есть только у владельца (§2.5, §2.6; M5).
var ownerOnly = []string{"staff.roles.grant_elevated", "staff.roles.revoke_owner", "staff.roles.revoke_admin",
	"staff.access.reset_elevated"}

// Ключи по схеме <модуль>.<ресурс>.<действие>, без повторов; описание не ломает таблицу документа;
// Mutating совпадает с действием ключа; Lookup находит каждое право; служебное значение x-permission
// не похоже на ключ каталога.
func TestCatalogKeys(t *testing.T) {
	if len(perm.Catalog) == 0 {
		t.Fatal("каталог пуст")
	}
	seen := map[string]bool{}
	for _, p := range perm.Catalog {
		if !keyRe.MatchString(p.Key) {
			t.Errorf("%q: ключ не <модуль>.<ресурс>.<действие>", p.Key)
		}
		if seen[p.Key] {
			t.Errorf("%q: ключ повторяется", p.Key)
		}
		seen[p.Key] = true
		if strings.TrimSpace(p.Title) == "" || strings.ContainsAny(p.Title, "|`\r\n") {
			t.Errorf("%q: описание %q пустое или ломает таблицу", p.Key, p.Title)
		}
		if p.Mutating == readActions[action(p.Key)] {
			t.Errorf("%q: Mutating=%v не совпадает с действием %q (read/search — чтение)", p.Key, p.Mutating, action(p.Key))
		}
		if got, ok := perm.Lookup(p.Key); !ok || got != p {
			t.Errorf("Lookup(%q) = %+v, %v", p.Key, got, ok)
		}
	}
	if _, ok := perm.Lookup("staff.roles.nope"); ok {
		t.Error("Lookup нашёл несуществующее право")
	}
	if !slices.Equal(perm.Service, []string{perm.Anonymous, perm.Session, perm.Self, perm.Handler}) {
		t.Errorf("служебные значения: %v", perm.Service)
	}
	for _, s := range perm.Service {
		if keyRe.MatchString(s) || seen[s] {
			t.Errorf("служебное значение %q совпадает по форме с ключом каталога", s)
		}
	}
}

// Матрица полная: у каждого права значение для каждой роли и только известные значения.
func TestMatrixComplete(t *testing.T) {
	if !slices.Equal(perm.Roles, []perm.Role{perm.Owner, perm.Admin, perm.Moderator, perm.Support}) {
		t.Fatalf("роли: %v", perm.Roles)
	}
	if len(perm.Matrix) != len(perm.Catalog) {
		t.Errorf("в матрице %d строк, в каталоге %d", len(perm.Matrix), len(perm.Catalog))
	}
	for _, p := range perm.Catalog {
		values, ok := perm.Matrix[p.Key]
		if !ok {
			t.Errorf("%s: нет строки матрицы", p.Key)
			continue
		}
		if len(values) != len(perm.Roles) {
			t.Errorf("%s: значений %d, ролей %d", p.Key, len(values), len(perm.Roles))
		}
		for _, r := range perm.Roles {
			g, ok := values[r]
			if !ok || g > auth.GrantDual {
				t.Errorf("%s: роль %s — значение %d (есть: %v)", p.Key, r, g, ok)
			}
		}
	}
}

// Права среза совпадают со спекой §2.5 и не черновые; в обе стороны: нечерновое право вне §2.5 —
// правка матрицы мимо спеки.
func TestMatrixMatchesSpec(t *testing.T) {
	inSpec := map[string]bool{}
	for _, s := range spec25 {
		inSpec[s.key] = true
	}
	for _, p := range perm.Catalog {
		if !p.Draft && !inSpec[p.Key] {
			t.Errorf("%s: нечерновое право, которого нет в спеке §2.5 (и в spec25)", p.Key)
		}
	}
	for _, s := range spec25 {
		p, ok := perm.Lookup(s.key)
		if !ok || p.Draft {
			t.Errorf("%s: права нет в каталоге или оно черновое", s.key)
			continue
		}
		m := perm.Matrix[s.key]
		got := [4]string{m[perm.Owner].String(), m[perm.Admin].String(), m[perm.Moderator].String(), m[perm.Support].String()}
		if want := [4]string{s.owner, s.admin, s.moderator, s.support}; got != want {
			t.Errorf("%s: %v, по спеке %v", s.key, got, want)
		}
	}
}

// Инварианты ролевой модели (docs/05, M5).
func TestMatrixInvariants(t *testing.T) {
	for _, p := range perm.Catalog {
		m := perm.Matrix[p.Key]
		if m[perm.Owner] == auth.GrantDeny {
			t.Errorf("%s: у владельца deny — владелец может всё", p.Key)
		}
		for _, r := range perm.Roles {
			if m[r] == auth.GrantScoped && r != perm.Moderator {
				t.Errorf("%s: scoped у роли %s — города бывают только у модератора", p.Key, r)
			}
			if m[r] == auth.GrantDual && !p.Mutating {
				t.Errorf("%s: dual у права чтения", p.Key)
			}
		}
		if p.Mutating && m[perm.Support] != auth.GrantDeny {
			t.Errorf("%s: поддержка только читает, а здесь %s", p.Key, m[perm.Support])
		}
		if p.Mutating && m[perm.Moderator] != auth.GrantScoped && m[perm.Moderator] != auth.GrantDeny {
			t.Errorf("%s: модератор меняет только в своих городах, а здесь %s", p.Key, m[perm.Moderator])
		}
	}
	for _, key := range ownerOnly {
		if _, ok := perm.Lookup(key); !ok {
			t.Errorf("%s: права нет — инвариант проверяет вхолостую", key)
			continue
		}
		for _, r := range []perm.Role{perm.Admin, perm.Moderator, perm.Support} {
			if g := perm.Matrix[key][r]; g != auth.GrantDeny {
				t.Errorf("%s: у роли %s значение %s — право только владельца", key, r, g)
			}
		}
	}
}

// strength — сила значения для сравнения ролей: allow (сам, везде) > scoped (сам, в своих городах) >
// dual (только вдвоём) > deny.
func strength(g auth.Grant) int {
	switch g {
	case auth.GrantAllow:
		return 3
	case auth.GrantScoped:
		return 2
	case auth.GrantDual:
		return 1
	default:
		return 0
	}
}

// Иерархия ролей (R1, M3): у владельца ни в одной строке не строже, чем у админа (нет owner=dual при
// admin=allow); админ не слабее модератора и поддержки; scoped — только у прав, привязанных к городу;
// права audit.* — только чтение (удалить или править журнал не может никто).
func TestMatrixHierarchy(t *testing.T) {
	cityBound := 0
	for _, p := range perm.Catalog {
		m := perm.Matrix[p.Key]
		if strength(m[perm.Owner]) < strength(m[perm.Admin]) {
			t.Errorf("%s: у владельца %s строже, чем у админа %s", p.Key, m[perm.Owner], m[perm.Admin])
		}
		for _, r := range []perm.Role{perm.Moderator, perm.Support} {
			if strength(m[perm.Admin]) < strength(m[r]) {
				t.Errorf("%s: админ (%s) слабее роли %s (%s)", p.Key, m[perm.Admin], r, m[r])
			}
		}
		if p.CityBound {
			cityBound++
		}
		for _, r := range perm.Roles {
			if m[r] == auth.GrantScoped && !p.CityBound {
				t.Errorf("%s: scoped у права без привязки к городу — городам модератора не с чем сверяться", p.Key)
			}
		}
		if strings.HasPrefix(p.Key, "audit.") && p.Mutating {
			t.Errorf("%s: право изменения журнала действий — журнал только читают", p.Key)
		}
	}
	if cityBound == 0 {
		t.Fatal("нет прав с привязкой к городу — проверка scoped вхолостую")
	}
	if p, ok := perm.Lookup("audit.log.read"); !ok || p.Mutating {
		t.Fatal("audit.log.read: права нет или оно не чтение — проверка audit.* вхолостую")
	}
}

// Права с привязкой к городу перечислены явно: значение scoped у них осмысленно.
func TestCityBound(t *testing.T) {
	want := []string{"audit.log.read", "geo.cities.read", "geo.cities.manage", "geo.cities.rename",
		"geo.districts.manage", "teams.captains.approve", "pitches.pitches.moderate", "moderation.reports.read",
		"moderation.reports.resolve", "moderation.restrictions.apply", "results.reports.annul", "identity.pii.read"}
	var got []string
	for _, p := range perm.Catalog {
		if p.CityBound {
			got = append(got, p.Key)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("права с привязкой к городу:\n%v\nнужно:\n%v", got, want)
	}
}

// Права роли: без черновых строк и без deny, значения — из матрицы, отдаётся копия (P12).
func TestGrants(t *testing.T) {
	drafts := 0
	for _, p := range perm.Catalog {
		if p.Draft {
			drafts++
		}
	}
	if drafts == 0 {
		t.Fatal("в каталоге нет черновых строк — проверка «без Draft» вхолостую")
	}
	for _, r := range perm.Roles {
		g := perm.Grants(r)
		for key, v := range g {
			p, ok := perm.Lookup(key)
			if !ok || p.Draft {
				t.Errorf("%s: %s — неизвестное или черновое право в правах роли", r, key)
			}
			if v == auth.GrantDeny || v != perm.Matrix[key][r] {
				t.Errorf("%s: %s = %s, в матрице %s", r, key, v, perm.Matrix[key][r])
			}
		}
		for _, p := range perm.Catalog {
			if _, ok := g[p.Key]; !ok && !p.Draft && perm.Matrix[p.Key][r] != auth.GrantDeny {
				t.Errorf("%s: право %s пропущено", r, p.Key)
			}
		}
	}
	g := perm.Grants(perm.Support)
	g["staff.roles.grant_elevated"] = auth.GrantAllow
	if _, ok := perm.Grants(perm.Support)["staff.roles.grant_elevated"]; ok {
		t.Fatal("Grants отдаёт общую карту: правка вызывающего изменила права роли")
	}
	if n := len(perm.Grants(perm.Role("intern"))); n != 0 {
		t.Fatalf("незнакомая роль получила %d прав", n)
	}
}
