package flags

import (
	"errors"
	"slices"
)

// Rule — правило флага (спека identity §4.2): GlobalOnly — у флага не бывает списка городов (только «везде»);
// Requires — ключ флага, без которого этот флаг не включается (и который не выключается при включённом этом).
// Зависимость бывает только между флагами GlobalOnly: проверка сравнивает enabled_globally (страж TestRulesConsistent).
type Rule struct {
	GlobalOnly bool
	Requires   string
}

// rules — декларация платформы рядом с каталогом флагов: модуль заводит строки миграцией, правило — строкой здесь
// своим коммитом (как ключи прав в perm). Флаги без правила — Rule{}.
var rules = map[string]Rule{
	"auth_apple":          {GlobalOnly: true},
	"auth_google":         {GlobalOnly: true, Requires: "auth_apple"},
	"auth_passkeys":       {GlobalOnly: true},
	"auth_apple_login":    {GlobalOnly: true},
	"auth_google_login":   {GlobalOnly: true},
	"auth_passkeys_login": {GlobalOnly: true},
}

// RuleOf — правило флага; у флага без правила — пустое.
func RuleOf(key string) Rule { return rules[key] }

// ErrGlobalOnly — непустой список городов у флага GlobalOnly (422 platform.flag_global_only ставит ручка admin).
var ErrGlobalOnly = errors.New("flags: у флага не бывает списка городов")

// RequiresError — правка нарушила зависимость: Key требует Requires. Enabling — включали Key при выключенном
// Requires; иначе выключали Requires при включённом Key. Error() — текст для detail ответа 409 platform.flag_requires
// (код ставит ручка admin, как у ErrStale).
type RequiresError struct {
	Key, Requires string
	Enabling      bool
}

func (e *RequiresError) Error() string {
	if e.Enabling {
		return e.Key + " требует " + e.Requires
	}
	return "сначала выключите " + e.Key
}

// lockKeys — ключи, строки которых правка key берёт под замок: сам флаг, его зависимость и зависимые от него флаги
// (зависимые — по ключу). Порядок замка задаёт запрос (ORDER BY key), а не этот срез.
func lockKeys(key string) []string {
	out := []string{key}
	if dep := rules[key].Requires; dep != "" {
		out = append(out, dep)
	}
	return append(out, dependents(key)...)
}

// dependents — флаги, которые требуют key, по ключу.
func dependents(key string) []string {
	var out []string
	for k, r := range rules {
		if r.Requires == key {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
