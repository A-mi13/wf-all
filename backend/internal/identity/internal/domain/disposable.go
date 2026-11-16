package domain

import (
	_ "embed"
	"strings"
	"sync"
)

// disposableRaw — список одноразовых почтовых доменов (источник, дата и лицензия — в шапке файла).
//
//go:embed disposable.txt
var disposableRaw string

var disposableSet = sync.OnceValue(func() map[string]struct{} {
	set := make(map[string]struct{}, 12000)
	for line := range strings.SplitSeq(disposableRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[strings.ToLower(line)] = struct{}{}
	}
	return set
})

// IsDisposable — домен почты (A-label, как его отдаёт emailaddr.Domain) или любой его родительский домен — в списке
// одноразовых (identity.email_disposable). Регистр и точка в конце не важны.
func IsDisposable(domain string) bool {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	set := disposableSet()
	for d != "" {
		if _, ok := set[d]; ok {
			return true
		}
		_, parent, ok := strings.Cut(d, ".")
		if !ok {
			return false
		}
		d = parent
	}
	return false
}
