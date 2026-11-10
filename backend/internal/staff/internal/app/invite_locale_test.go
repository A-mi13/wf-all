package app_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/staff"
)

// Язык с экрана приглашения (решение пользователя 10.11): сохраняется в учётку вместе с паролем и только
// при успехе принятия.

func ptr(s string) *string { return &s }

// localeOf — язык учётки.
func (e *env) localeOf(staffID uuid.UUID) string {
	e.t.Helper()
	var l string
	if err := e.owner.QueryRow(context.Background(), `SELECT locale FROM staff_accounts WHERE id = $1`, staffID).Scan(&l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

// Неудачи (пароль, мусор в locale, неверный код, срок) язык не меняют; без поля и с тем же языком — прежний
// язык и аудит без языка; другой язык — сохранён, аудит было/стало, письма дальше на нём; повторное
// принятие перезаписывает.
func TestAcceptInviteLocale(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support", Name: "Иван П."}, purposeInvite)
	wantLocale := func(step, want string) {
		t.Helper()
		if got := e.localeOf(m.ID); got != want {
			t.Fatalf("%s: язык %q, ждали %q", step, got, want)
		}
	}

	// пароль не по правилам — до кода и до языка
	_, err := e.app.AcceptInvite(ctx, m.Email, code, "короткий", ptr("de"), "", metaWeb)
	wantFieldError(t, err, "body.password", "too_short")
	wantLocale("пароль", "ru")
	// незнакомый язык — как SetLocale: validation.failed по body.locale, попытка не пишется
	for _, bad := range []string{"de", "", "EN", "en-US", " en"} {
		_, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, ptr(bad), "", metaWeb)
		wantFieldError(t, err, "body.locale", "enum")
	}
	wantLocale("мусор", "ru")
	if n := len(e.audits("staff.invite_accept_failed")); n != 0 {
		t.Fatalf("мусор в языке записан как неудача: %d", n)
	}
	// неверный код
	if _, err := e.app.AcceptInvite(ctx, m.Email, otherCode(code), newPassword, ptr("en"), "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("неверный код: %v", err)
	}
	wantLocale("неверный код", "ru")
	if n := e.count(`SELECT count(*) FROM staff_sessions WHERE staff_id = $1`, m.ID); n != 0 {
		t.Fatalf("сессий %d", n)
	}

	// без поля и тем же языком — язык прежний, в аудите языка нет
	for _, l := range []*string{nil, ptr("ru")} {
		e.clock.Advance(time.Second)
		if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, l, "", metaWeb); err != nil {
			t.Fatal(err)
		}
		wantLocale("без смены", "ru")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.invite_accepted'
		AND before IS NULL AND NOT after ? 'locale'`); n != 2 {
		t.Fatalf("аудит без смены языка: %d", n)
	}

	// другой язык — сохранён, было/стало в той же записи принятия
	e.clock.Advance(time.Second)
	if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, ptr("en"), "", metaWeb); err != nil {
		t.Fatal(err)
	}
	wantLocale("en", "en")
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.invite_accepted' AND actor_staff_id = $1
		AND before->>'locale' = 'ru' AND after->>'locale' = 'en' AND after->>'purpose' = 'invite'`, m.ID); n != 1 {
		t.Fatal("аудит смены языка при принятии")
	}
	if n := len(e.audits("staff.locale_changed")); n != 0 {
		t.Fatalf("отдельная запись locale_changed: %d", n)
	}
	// письма дальше — на выбранном языке
	msg, err := e.app.ComposeInvite(ctx, e.liveInvite(m.ID).ID, m.ID, staff.MailInvite)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Text, "You have been invited") {
		t.Fatalf("письмо не на en:\n%s", msg.Text)
	}

	// неудачи после смены — язык не трогают
	if _, err := e.app.AcceptInvite(ctx, m.Email, otherCode(code), newPassword, ptr("ru"), "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("неверный код: %v", err)
	}
	wantLocale("неверный код после en", "en")

	// повторное принятие другим языком — перезапись
	e.clock.Advance(time.Second)
	if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, ptr("ru"), "", metaWeb); err != nil {
		t.Fatal(err)
	}
	wantLocale("повтор", "ru")

	// срок истёк — язык не меняется
	e.clock.Advance(24 * time.Hour)
	if _, err := e.app.AcceptInvite(ctx, m.Email, code, newPassword, ptr("en"), "", metaWeb); !errors.Is(err, staff.ErrInviteInvalid) {
		t.Fatalf("истёкшее: %v", err)
	}
	wantLocale("срок", "ru")
}

// Две вкладки принимают одновременно с разными языками: замок учётки выстраивает их, обе успешны; язык
// учётки — язык вкладки, чья сессия осталась живой (последней). Детерминированно (раунд 1 IL, m3): тест держит
// замок учётки, вкладка en встаёт в очередь замка первой, ru — второй (обе уже прочли кандидата без замка),
// затем замок отпускается. Порядок исполнения не проверяется — проверяется инвариант.
func TestAcceptInviteLocaleRace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	m, code := e.invitee("ivan.p@wf.example", memberOpts{Role: "support"}, purposeInvite)
	tx, err := e.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM staff_accounts WHERE id = $1 FOR UPDATE`, m.ID); err != nil {
		t.Fatal(err)
	}
	langs := []string{"en", "ru"}
	toks, errs := make([]string, len(langs)), make([]error, len(langs))
	var wg sync.WaitGroup
	for i, l := range langs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			toks[i], errs[i] = e.app.AcceptInvite(ctx, m.Email, code, newPassword, ptr(l), "", metaWeb)
		}()
		e.waitLockWaiters(i + 1) // вкладка i ждёт замок учётки
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	live := -1
	for i := range langs {
		if errs[i] != nil {
			t.Fatalf("%s: %v", langs[i], errs[i])
		}
		if _, err := e.app.LoadStaff(context.Background(), toks[i], ipHome, false); err == nil {
			if live >= 0 {
				t.Fatal("живы обе сессии")
			}
			live = i
		}
	}
	if live < 0 {
		t.Fatal("живой сессии нет")
	}
	if got := e.localeOf(m.ID); got != langs[live] {
		t.Fatalf("язык %q, а живая сессия — вкладки %q", got, langs[live])
	}
}
