//go:build wf_e2e

package main

import (
	"wf/backend/internal/identity"
	"wf/backend/internal/identity/identitytest"
)

// captainChecks — порты условий капитанства сборки живого e2e (тег wf_e2e, спека identity §10.3): оба условия
// выполнены. Прод-образ (backend/Dockerfile) собирается без тега.
func captainChecks() []identity.CaptainCheck { return identitytest.AllowAll() }
