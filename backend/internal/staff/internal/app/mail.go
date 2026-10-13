package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"wf/backend/internal/platform/audit"
	"wf/backend/internal/platform/i18n"
	"wf/backend/internal/platform/mail"
	"wf/backend/internal/staff"
	"wf/backend/internal/staff/internal/store/staffdb"
)

// mailTime — время в письмах сотрудникам: UTC с подписью (сотрудники в разных часовых поясах).
const mailTime = "2006-01-02 15:04 UTC"

// ComposeLocked — письмо staff.locked (§4.12) получателю recipient: кто закрыт (имя и почта),
// когда и из какой сети (IP маскирован до /24 или /48) — из учётки и строки сессии ref, на которой
// учётка закрылась. Язык — staff_accounts.locale получателя (mailLocale). Письмо больше не нужно
// (сессии нет или она погашена не блокировкой; получатель не активный владелец или админ) —
// mail.ErrSkip: задача отменяется без повторов.
func (a *App) ComposeLocked(ctx context.Context, ref, recipient uuid.UUID) (mail.Message, error) {
	q := staffdb.New(a.pool)
	row, err := q.GetLockedMail(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return mail.Message{}, mail.ErrSkip
	}
	if err != nil {
		return mail.Message{}, err
	}
	if row.RevokedReason == nil || *row.RevokedReason != reasonLocked || row.RevokedAt == nil {
		return mail.Message{}, mail.ErrSkip
	}
	to, ok, err := a.recipient(ctx, q, recipient, roleOwner, roleAdmin)
	if err != nil || !ok {
		return mail.Message{}, err
	}
	args := map[string]any{
		"name":   row.DisplayName,
		"email":  row.Email,
		"time":   row.RevokedAt.UTC().Format(mailTime),
		"ip":     audit.MaskIP(row.Ip),
		"origin": a.origin,
	}
	return a.compose(to, staff.MailLocked, args)
}

// mailTo — адрес и язык получателя письма.
type mailTo struct{ email, locale string }

// recipient — активный получатель одной из ролей roles; нет или не подходит — ok=false и ошибка
// mail.ErrSkip.
func (a *App) recipient(ctx context.Context, q *staffdb.Queries, id uuid.UUID, roles ...string) (mailTo, bool, error) {
	r, err := q.GetMailRecipient(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return mailTo{}, false, mail.ErrSkip
	}
	if err != nil {
		return mailTo{}, false, err
	}
	if r.Status != statusActive || !slices.Contains(roles, r.Role) {
		return mailTo{}, false, mail.ErrSkip
	}
	return mailTo{email: r.Email, locale: mailLocale(r.Locale)}, true, nil
}

// compose — письмо вида kind из backend/locales (<kind>.subject, <kind>.body) с подписью mail.footer.
func (a *App) compose(to mailTo, kind string, args map[string]any) (mail.Message, error) {
	subject, err := a.locales.Text(to.locale, kind+".subject", args)
	if err != nil {
		return mail.Message{}, err
	}
	body, err := a.locales.Text(to.locale, kind+".body", args)
	if err != nil {
		return mail.Message{}, err
	}
	footer, err := a.locales.Text(to.locale, "mail.footer", nil)
	if err != nil {
		return mail.Message{}, err
	}
	return mail.Message{To: to.email, Subject: subject, Text: fmt.Sprintf("%s\n\n%s\n", body, footer)}, nil
}

// mailLocale — язык письма из staff_accounts.locale: первичный подтег в нижнем регистре
// (en-US, en_GB → en); не из i18n.Supported — i18n.Default (остаток фундамента 3/3).
func mailLocale(locale string) string {
	primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(locale)), "-")
	primary, _, _ = strings.Cut(primary, "_")
	if slices.Contains(i18n.Supported, primary) {
		return primary
	}
	return i18n.Default
}
