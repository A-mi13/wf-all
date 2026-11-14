package domain

import "wf/backend/internal/platform/emailaddr"

// NormalizeEmail — рабочая почта сотрудника в каноническом ASCII-виде (emailaddr.Normalize; политика —
// решение пользователя 08.10.2026, спека сотрудников §4.1). Приглашение, принятие, повторная отправка и вход
// находят одну учётку (Review Focus 1). ok=false — не адрес.
func NormalizeEmail(s string) (string, bool) { return emailaddr.Normalize(s) }

// isInvisible — невидимый или «пустой» символ (emailaddr.IsInvisible): им пользуется DisplayNameOK.
func isInvisible(r rune) bool { return emailaddr.IsInvisible(r) }
