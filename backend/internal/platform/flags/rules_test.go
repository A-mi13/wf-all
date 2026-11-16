package flags

import (
	"regexp"
	"testing"
)

// Таблица правил согласована: у каждого Requires есть правило с этим ключом, циклов нет, ключи — как в
// feature_flags; зависимость бывает только между флагами «везде» (проверка зависимости сравнивает enabled_globally).
func TestRulesConsistent(t *testing.T) {
	t.Parallel()
	keyRe := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	if len(rules) == 0 {
		t.Fatal("таблица правил пуста — страж вхолостую")
	}
	for k, r := range rules {
		if !keyRe.MatchString(k) {
			t.Errorf("ключ %q не по шаблону", k)
		}
		if r.Requires == "" {
			continue
		}
		dep, ok := rules[r.Requires]
		if !ok {
			t.Errorf("%s требует %s, а правила %s нет", k, r.Requires, r.Requires)
			continue
		}
		if !r.GlobalOnly || !dep.GlobalOnly {
			t.Errorf("%s → %s: зависимость только между GlobalOnly", k, r.Requires)
		}
		seen := map[string]bool{k: true}
		for cur := r.Requires; cur != ""; cur = rules[cur].Requires {
			if seen[cur] {
				t.Errorf("цикл зависимостей через %s", k)
				break
			}
			seen[cur] = true
		}
	}
	want := map[string]Rule{
		"auth_apple": {GlobalOnly: true}, "auth_passkeys": {GlobalOnly: true}, "auth_apple_login": {GlobalOnly: true},
		"auth_google_login": {GlobalOnly: true}, "auth_passkeys_login": {GlobalOnly: true},
		"auth_google": {GlobalOnly: true, Requires: "auth_apple"},
	}
	for k, r := range want {
		if RuleOf(k) != r {
			t.Errorf("RuleOf(%s) = %+v, ждали %+v", k, RuleOf(k), r)
		}
	}
	if len(rules) != len(want) {
		t.Errorf("правил %d, ждали %d", len(rules), len(want))
	}
	if RuleOf("ads") != (Rule{}) {
		t.Error("у флага без правила — пустое правило")
	}
	if got := lockKeys("auth_apple"); len(got) != 2 || got[0] != "auth_apple" || got[1] != "auth_google" {
		t.Errorf("lockKeys(auth_apple) = %v", got)
	}
	if got := lockKeys("auth_google"); len(got) != 2 || got[0] != "auth_google" || got[1] != "auth_apple" {
		t.Errorf("lockKeys(auth_google) = %v", got)
	}
	if got := lockKeys("ads"); len(got) != 1 {
		t.Errorf("lockKeys(ads) = %v", got)
	}
}
