package domain_test

import (
	"errors"
	"testing"

	"wf/backend/internal/media/internal/domain"
)

var states = []domain.State{domain.StatePendingUpload, domain.StateUploaded, domain.StateProcessing, domain.StateReady,
	domain.StateRejected, domain.StateDetached, domain.StateTakenDown}

// allowed — переходы спеки media §4 и §3.3 (таблица теста, а не код): прикрепление; захват обработки (в том числе
// повторный после падения исполнителя — processing → processing) и возврат в uploaded обычной ошибкой; публикация;
// отклонение воркером; DetachInTx и TakeDownInTx из uploaded | processing | ready.
var allowed = map[[2]domain.State]bool{
	{domain.StatePendingUpload, domain.StateUploaded}: true,
	{domain.StateUploaded, domain.StateProcessing}:    true,
	{domain.StateUploaded, domain.StateRejected}:      true,
	{domain.StateUploaded, domain.StateDetached}:      true,
	{domain.StateUploaded, domain.StateTakenDown}:     true,
	{domain.StateProcessing, domain.StateProcessing}:  true,
	{domain.StateProcessing, domain.StateUploaded}:    true,
	{domain.StateProcessing, domain.StateReady}:       true,
	{domain.StateProcessing, domain.StateRejected}:    true,
	{domain.StateProcessing, domain.StateDetached}:    true,
	{domain.StateProcessing, domain.StateTakenDown}:   true,
	{domain.StateReady, domain.StateDetached}:         true,
	{domain.StateReady, domain.StateTakenDown}:        true,
}

// Полная матрица 7×7: разрешено ровно то, что в таблице; прочее — ErrTransition (в том числе выход из конечных:
// «без изменений» решает IsFinal у вызывающего, не Transition).
func TestTransitions(t *testing.T) {
	t.Parallel()
	for _, from := range states {
		for _, to := range states {
			err := domain.Transition(from, to)
			switch want := allowed[[2]domain.State{from, to}]; {
			case want && err != nil:
				t.Errorf("%s → %s: ждали разрешение, получили %v", from, to, err)
			case !want && !errors.Is(err, domain.ErrTransition):
				t.Errorf("%s → %s: ждали ErrTransition, получили %v", from, to, err)
			}
		}
	}
	for _, c := range [][2]domain.State{{"", domain.StateUploaded}, {domain.StateUploaded, "deleted"}, {"deleted", "deleted"}} {
		if err := domain.Transition(c[0], c[1]); !errors.Is(err, domain.ErrTransition) {
			t.Errorf("%q → %q: ждали ErrTransition, получили %v", c[0], c[1], err)
		}
	}
}

// Конечные — detached, taken_down, rejected: из них DetachInTx/TakeDownInTx — «без изменений», не ошибка.
func TestIsFinal(t *testing.T) {
	t.Parallel()
	final := map[domain.State]bool{domain.StateRejected: true, domain.StateDetached: true, domain.StateTakenDown: true}
	for _, s := range append(states, "", "deleted") {
		if got := domain.IsFinal(s); got != final[s] {
			t.Errorf("IsFinal(%q) = %v", s, got)
		}
	}
}
