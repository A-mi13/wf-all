// Package domain — правила модуля media без базы и хранилища: состояния объекта и переходы (спека media §4).
package domain

import (
	"errors"
	"fmt"
)

// State — состояние объекта media_objects.state (CHECK media_objects_state_check). Корень media отдаёт его
// псевдонимом media.State.
type State string

const (
	StatePendingUpload State = "pending_upload"
	StateUploaded      State = "uploaded"
	StateProcessing    State = "processing"
	StateReady         State = "ready"
	StateRejected      State = "rejected"
	StateDetached      State = "detached"
	StateTakenDown     State = "taken_down"
)

// ErrTransition — переход не разрешён спекой (§4, §3.3).
var ErrTransition = errors.New("media: переход состояния не разрешён")

// transitions — разрешённые переходы: прикрепление (pending_upload → uploaded); захват обработки — из uploaded и
// повторный из processing (прошлый исполнитель упал, §3.3 п. 1: process_attempts + 1); обычная ошибка обработки
// возвращает processing → uploaded; публикация processing → ready; отклонение воркером из uploaded | processing;
// DetachInTx и TakeDownInTx — из uploaded | processing | ready. Из конечных переходов нет: «без изменений» у
// DetachInTx/TakeDownInTx решает вызывающий по IsFinal. CreateDirectInTx создаёт строку сразу uploaded — не переход.
var transitions = map[State][]State{
	StatePendingUpload: {StateUploaded},
	StateUploaded:      {StateProcessing, StateRejected, StateDetached, StateTakenDown},
	StateProcessing:    {StateProcessing, StateUploaded, StateReady, StateRejected, StateDetached, StateTakenDown},
	StateReady:         {StateDetached, StateTakenDown},
}

// Transition — nil, если from → to разрешён; иначе ошибка, обёрнутая над ErrTransition.
func Transition(from, to State) error {
	for _, s := range transitions[from] {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrTransition, from, to)
}

// IsFinal — конечное состояние (rejected, detached, taken_down): ended_at задан, строку удаляет чистка по сроку.
func IsFinal(s State) bool {
	return s == StateRejected || s == StateDetached || s == StateTakenDown
}
