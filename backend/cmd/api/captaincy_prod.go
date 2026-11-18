//go:build !wf_e2e

package main

import "wf/backend/internal/identity"

// captainChecks — порты условий капитанства прод-сборки (спека identity §10.3, К1, R4 плана I-4): до модулей players
// и teams оба — «недоступно», заявку подать и одобрить нельзя. Модуль выше заменит свой порт настоящей проверкой.
// Тестовые порты — только в сборке с тегом wf_e2e (captaincy_e2e.go); страж — TestCaptainChecksProdBuild.
func captainChecks() []identity.CaptainCheck {
	out := make([]identity.CaptainCheck, 0, len(identity.RequiredCaptainChecks))
	for _, name := range identity.RequiredCaptainChecks {
		out = append(out, identity.UnavailableCheck(name))
	}
	return out
}
