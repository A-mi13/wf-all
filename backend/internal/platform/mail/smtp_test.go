package mail_test

import (
	"bufio"
	"context"
	"fmt"
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

// smtpServerRcpt — то же; rejectRcpt: на RCPT TO отвечает 550 с адресом получателя в тексте.
func smtpServerRcpt(t *testing.T, rejectRcpt bool) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	} {
		if _, err := mail.NewSMTP(c); err == nil {
			t.Errorf("%s: принято", name)
		}
	}
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
