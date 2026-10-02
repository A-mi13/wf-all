// Package mail — транзакционные письма (спека бэкенда §6.9): отправка только задачей River в
// очереди mail; в аргументах — вид письма и id получателя или кода, адрес находит Composer
// модуля. Dev и прод — одна реализация SMTP (в dev — Mailpit, ./task mail).
package mail

import (
	"context"
	"sync"
)

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string // необязательная HTML-версия
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Memory — письма в памяти: тесты сценариев модулей.
type Memory struct {
	mu   sync.Mutex
	sent []Message
}

func (m *Memory) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *Memory) Messages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.sent...)
}
