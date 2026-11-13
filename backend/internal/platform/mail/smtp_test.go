package mail_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"wf/backend/internal/platform/mail"
)

// smtpServer — минимальный SMTP-сервер теста: принимает письма и отдаёт DATA в канал.
func smtpServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	return smtpServerRcpt(t, false)
}

// smtpServerRcpt — то же; rejectRcpt: на RCPT TO отвечает 550 с адресом получателя в тексте;
// ext — дополнительные расширения в ответе EHLO (ENHANCEDSTATUSCODES).
func smtpServerRcpt(t *testing.T, rejectRcpt bool, ext ...string) (string, <-chan string) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = fmt.Fprintf(conn, "%s\r\n", s) }
		write("220 test")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					write("250 ok")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				write("250-test")
				for _, e := range ext {
					write("250-" + e)
				}
				write("250 8BITMIME")
			case rejectRcpt && strings.HasPrefix(cmd, "RCPT TO"):
				write("550 5.1.1 <user@example.com> recipient rejected")
			case cmd == "DATA":
				inData = true
				write("354 go")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

func TestSMTPSends(t *testing.T) {
	addr, got := smtpServer(t)
	s, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "WF <noreply@example.com>", TLS: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Send(ctx, mail.Message{To: "user@example.com", Subject: "Code", Text: "code 123456", HTML: "<b>code 123456</b>"}); err != nil {
		t.Fatal(err)
	}
	var data string
	select {
	case data = <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("сервер не получил письмо")
	}
	for _, want := range []string{"To: <user@example.com>", "Subject: Code", "code 123456", "text/html", "noreply@example.com"} {
		if !strings.Contains(data, want) {
			t.Errorf("в письме нет %q:\n%s", want, data)
		}
	}
}

func TestNewSMTPValidates(t *testing.T) {
	for name, c := range map[string]mail.SMTPConfig{
		"нет порта":        {Addr: "localhost", From: "a@b.co", TLS: "none"},
		"порт не число":    {Addr: "localhost:x", From: "a@b.co", TLS: "none"},
		"нет отправителя":  {Addr: "localhost:25", TLS: "none"},
		"битый From":       {Addr: "localhost:25", From: "nope", TLS: "none"},
		"неизвестный TLS":  {Addr: "localhost:25", From: "a@b.co", TLS: "maybe"},
		"пароль без имени": {Addr: "localhost:25", From: "a@b.co", TLS: "none", Password: "x"},
		"пустой TLS":       {Addr: "localhost:25", From: "a@b.co"},
	} {
		if _, err := mail.NewSMTP(c); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
}

// Без TLS — только Mailpit (план C, R8): опечатка в ADMIN_MAIL_SMTP_TLS на стенде не должна отправить API-ключ
// Resend открытым текстом. Локальный — только точный список хостов, «похожие» имена — нет.
func TestNewSMTPRejectsPlainRemote(t *testing.T) {
	for _, addr := range []string{
		"smtp.example.com:587", "smtp.resend.com:587", "localhost.example.com:25", "127.0.0.1.nip.io:25",
		"0.0.0.0:25", ":25", "10.0.0.5:25", "127.0.0.2:25", "[::ffff:127.0.0.1]:25",
	} {
		t.Run(addr, func(t *testing.T) {
			_, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "a@b.co", TLS: "none"})
			if err == nil {
				t.Fatalf("%s без TLS принят", addr)
			}
			if !strings.Contains(err.Error(), "TLS") {
				t.Fatalf("ошибка не про TLS: %v", err)
			}
		})
	}
}

// Логин на петле без TLS — тоже отказ: учётные данные есть только у настоящего провайдера, а петля на стенде — это
// локальный прокси Render, не Mailpit. Секрет в текст ошибки (лог старта) не попадает.
func TestNewSMTPRejectsPlainWithAuth(t *testing.T) {
	const user, pass = "resend-user-x", "re_secret_value_42"
	for _, addr := range []string{"127.0.0.1:1025", "localhost:1025", "[::1]:1025"} {
		t.Run(addr, func(t *testing.T) {
			_, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "a@b.co", TLS: "none", Username: user, Password: pass})
			if err == nil {
				t.Fatal("логин без TLS принят")
			}
			if strings.Contains(err.Error(), pass) || strings.Contains(err.Error(), user) {
				t.Fatalf("в ошибке учётные данные: %v", err)
			}
		})
	}
	// логин без пароля — тоже логин
	if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: "127.0.0.1:1025", From: "a@b.co", TLS: "none", Username: user}); err == nil {
		t.Fatal("имя пользователя без TLS принято")
	}
}

// Mailpit (dev, живой e2e, проверка образа в CI) — без TLS и логина на петле — работает как раньше.
func TestNewSMTPMailpit(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:11026", "127.0.0.1:11025", "localhost:1025", "LOCALHOST:1025", "[::1]:1025"} {
		if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "WF <noreply@wf.local>", TLS: "none"}); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	// с TLS логин допустим на любом хосте
	for _, tls := range []string{"mandatory", "opportunistic"} {
		if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: "smtp.example.com:587", From: "a@b.co", TLS: tls,
			Username: "u", Password: "p"}); err != nil {
			t.Errorf("%s: %v", tls, err)
		}
	}
}

// opportunistic с нелокальным хостом — старт, но предупреждение в лог (на стенде — mandatory): сервер без STARTTLS
// получил бы логин открытым текстом. На петле — без предупреждения. Журнал — SMTPConfig.Log (журнал сервиса), без
// него — slog.Default().
func TestNewSMTPOpportunisticRemoteWarns(t *testing.T) {
	check := func(t *testing.T, buf *bytes.Buffer, logTo *slog.Logger) {
		t.Helper()
		if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: "127.0.0.1:1025", From: "a@b.co", TLS: "opportunistic",
			Log: logTo}); err != nil {
			t.Fatal(err)
		}
		if buf.Len() != 0 {
			t.Fatalf("предупреждение на петле: %s", buf.String())
		}
		if _, err := mail.NewSMTP(mail.SMTPConfig{Addr: "smtp.example.com:587", From: "a@b.co", TLS: "opportunistic",
			Username: "u", Password: "secret-p", Log: logTo}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "smtp.example.com") {
			t.Fatalf("нет предупреждения про opportunistic: %q", out)
		}
		if strings.Contains(out, "secret-p") {
			t.Fatalf("пароль в логе: %q", out)
		}
	}
	t.Run("журнал сервиса", func(t *testing.T) {
		var buf bytes.Buffer
		check(t, &buf, slog.New(slog.NewTextHandler(&buf, nil)))
	})
	t.Run("без журнала — slog.Default", func(t *testing.T) {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		t.Cleanup(func() { slog.SetDefault(prev) })
		check(t, &buf, nil)
	})
}

func TestMemory(t *testing.T) {
	var m mail.Memory
	_ = m.Send(context.Background(), mail.Message{To: "a@b.co"})
	if msgs := m.Messages(); len(msgs) != 1 || msgs[0].To != "a@b.co" {
		t.Fatalf("%+v", msgs)
	}
}

// Адрес получателя — персональные данные: River пишет текст ошибки в river_job.errors,
// минуя маскирование логов (§6.9), поэтому в ошибке Send адреса быть не должно.
func TestSMTPErrorsHaveNoAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("сервер отклонил получателя", func(t *testing.T) {
		addr, _ := smtpServerRcpt(t, true)
		s, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "noreply@example.com", TLS: "none"})
		if err != nil {
			t.Fatal(err)
		}
		err = s.Send(ctx, mail.Message{To: "user@example.com", Subject: "Code", Text: "x"})
		if err == nil {
			t.Fatal("отказ сервера не вернул ошибку")
		}
		if strings.Contains(err.Error(), "user@example.com") || strings.Contains(err.Error(), "example.com>") {
			t.Fatalf("в ошибке адрес получателя: %v", err)
		}
	})

	// текст уходит в river_job.errors: код SMTP без висящего пробела, расширенный — если сервер
	// объявил ENHANCEDSTATUSCODES
	for _, c := range []struct {
		name string
		ext  []string
		want string
	}{
		{"без расширенного кода", nil, "(SMTP 550)"},
		{"с расширенным кодом", []string{"ENHANCEDSTATUSCODES"}, "(SMTP 550 5.1.1)"},
	} {
		t.Run("код отказа: "+c.name, func(t *testing.T) {
			addr, _ := smtpServerRcpt(t, true, c.ext...)
			s, err := mail.NewSMTP(mail.SMTPConfig{Addr: addr, From: "noreply@example.com", TLS: "none"})
			if err != nil {
				t.Fatal(err)
			}
			err = s.Send(ctx, mail.Message{To: "user@example.com", Subject: "Code", Text: "x"})
			if err == nil || !strings.HasSuffix(err.Error(), c.want) {
				t.Fatalf("err = %q, ждали окончание %q", err, c.want)
			}
		})
	}

	t.Run("битый адрес получателя", func(t *testing.T) {
		s, err := mail.NewSMTP(mail.SMTPConfig{Addr: "127.0.0.1:1", From: "noreply@example.com", TLS: "none"})
		if err != nil {
			t.Fatal(err)
		}
		err = s.Send(ctx, mail.Message{To: "secret.user@@example.com", Subject: "Code", Text: "x"})
		if err == nil {
			t.Fatal("битый адрес принят")
		}
		if strings.Contains(err.Error(), "secret.user") {
			t.Fatalf("в ошибке адрес получателя: %v", err)
		}
	})
}

// Висящий SMTP (m2 ревью 10c): сервер принял соединение, ответил на EHLO и замолчал посреди обмена. Отмена ctx
// отправки (остановка River, срок задачи) обрывает соединение сразу, а не ждёт таймаута go-mail (20 с): иначе
// остановка admin-api и воркера не укладывается в бюджет до SIGKILL.
func TestSMTPSendHonorsCancel(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	hung := make(chan struct{}) // сервер замолчал
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		fmt.Fprint(c, "220 hang ESMTP\r\n")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(strings.ToUpper(line), "EHLO") {
				fmt.Fprint(c, "250 hang\r\n")
				continue
			}
			close(hung) // MAIL FROM и дальше — без ответа, до закрытия соединения клиентом
			_, _ = io.Copy(io.Discard, r)
			return
		}
	}()
	s, err := mail.NewSMTP(mail.SMTPConfig{Addr: ln.Addr().String(), From: "WF <noreply@wf.local>", TLS: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Send(ctx, mail.Message{To: "a@wf.example", Subject: "s", Text: "t"}) }()
	select {
	case <-hung:
	case <-time.After(10 * time.Second):
		t.Fatal("сервер не дождался MAIL FROM")
	}
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, ждали context.Canceled", err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Fatalf("отправка вернулась через %s после отмены", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("отмена ctx не прервала висящую отправку за 5 с")
	}
}
