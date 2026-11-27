// Package mediapurposes — реестр целей media бинарников (R12 плана media): cmd/api, cmd/admin-api и воркер
// (cmd/internal/workerhost) берут цели и проверку содержимого только отсюда — набор одинаков у трёх по построению.
// Прод-сборка (prod.go, без тега) — общий список прода; сборка живого e2e (e2e.go, тег wf_e2e) добавляет тестовые
// цели mediatest и тестовую проверку содержимого (R20). media.NewRegistry вне этого пакета и тестов не вызывается —
// страж archtest.TestMediaPurposesOnlyFromRegistry.
package mediapurposes

import "wf/backend/internal/media"

// prodPurposes — цели прода: их добавляют планы владельцев (economy, pitches, …) своими строками. Новый срез на вызов.
func prodPurposes() []media.Purpose { return nil }

// Registry — реестр целей бинарника: Purposes() с проверкой каждой цели (ошибка — отказ старта).
func Registry() (*media.Registry, error) { return media.NewRegistry(Purposes()...) }
