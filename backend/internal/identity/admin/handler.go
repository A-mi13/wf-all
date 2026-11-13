// Package admin — ручки модуля identity в админке (тег identity контракта admin, спека identity §7): заявки в
// капитаны, поиск игрока по почте, сброс 2FA поддержкой, статистика способов входа. Handler встраивается в
// internal/httpapi/admin.Server: тег операции = модуль-реализатор (apitest.TagViolations).
//
// План I-1 (контракт до кода): нулевое значение Handler — каркас, каждая операция отвечает 501
// http.not_implemented, пока этап спеки её не реализует; такие операции — в pendingOps стража TestNoStubOperations
// (internal/httpapi/admin).
package admin

// Handler — админские ручки identity. Нулевое значение — заглушка: методы появляются вместе с операциями контракта.
type Handler struct{}
