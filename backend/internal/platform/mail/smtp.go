package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	netmail "net/mail"
	"net/netip"
	"strconv"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"wf/backend/internal/platform/logx"
)

type SMTPConfig struct {
	Addr     string // host:port
	From     string // «Имя <адрес>» отправителя
	Username string // пусто — без аутентификации (Mailpit)
	Password string
	TLS      string // mandatory | opportunistic | none
}

type SMTP struct {
	host string
	port int
	from string
	user string
	pass string
	tls  gomail.TLSPolicy
}

func NewSMTP(c SMTPConfig) (*SMTP, error) {
	host, portRaw, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return nil, fmt.Errorf("mail: адрес SMTP %q: %w", c.Addr, err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("mail: порт SMTP %q", portRaw)
	}
	if _, err := netmail.ParseAddress(c.From); err != nil {
		return nil, fmt.Errorf("mail: отправитель %q: %w", c.From, err)
	}
	if c.Password != "" && c.Username == "" {
		return nil, errors.New("mail: пароль SMTP без имени пользователя")
	}
	tls := map[string]gomail.TLSPolicy{"mandatory": gomail.TLSMandatory, "opportunistic": gomail.TLSOpportunistic, "none": gomail.NoTLS}
	policy, ok := tls[c.TLS]
	if !ok {
		return nil, fmt.Errorf("mail: TLS %q — нужен mandatory, opportunistic или none", c.TLS)
	}
	// Без TLS — только Mailpit (план C, R8): петля и без учётных данных. Опечатка в переменной стенда не должна
	// отправить ключ провайдера открытым текстом; логин на петле — тоже отказ (у Mailpit логина нет).
	local := loopbackHost(host)
	if policy == gomail.NoTLS && (!local || c.Username != "") {
		return nil, fmt.Errorf("mail: TLS none допустим только для Mailpit — хост 127.0.0.1, ::1 или localhost "+
			"без логина; хост %q — нужен mandatory", host)
	}
	if policy == gomail.TLSOpportunistic && !local {
		// сервер без STARTTLS получил бы логин открытым текстом; на стенде — mandatory
		slog.Default().Warn("mail: SMTP с TLS opportunistic на нелокальном хосте — без STARTTLS письмо и логин уйдут открытым текстом; нужен mandatory",
			"host", host)
	}
	return &SMTP{host: host, port: port, from: c.From, user: c.Username, pass: c.Password, tls: policy}, nil
}

// loopbackHost — точный список локальных хостов: 127.0.0.1, ::1 (в любой записи) и localhost. «Похожие» имена
// (localhost.example.com), прочие адреса 127/8 и ::ffff:127.0.0.1 — не локальные.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && (ip == netip.AddrFrom4([4]byte{127, 0, 0, 1}) || ip == netip.IPv6Loopback())
}

// Send — одно соединение на письмо: транзакционных писем немного, пул соединений не нужен.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("mail: отправитель: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		// текст ошибки go-mail содержит адрес, а River пишет ошибку в river_job.errors (§6.9)
		return errors.New("mail: некорректный адрес получателя")
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	msg.SetMessageID()
	msg.SetDate()
	// соединение закрывается по отмене ctx (m2 ревью 10c): go-mail отмену видит только при установке соединения, а
	// посреди обмена ждал бы своего таймаута (20 с) — остановка River и срок задачи не укладывались бы в бюджет
	var stopWatch func() bool
	dial := func(dctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(dctx, network, addr)
		if err != nil {
			return nil, err
		}
		stopWatch = context.AfterFunc(ctx, func() { _ = conn.Close() })
		return conn, nil
	}
	defer func() {
		if stopWatch != nil {
			stopWatch()
		}
	}()
	opts := []gomail.Option{gomail.WithPort(s.port), gomail.WithTLSPolicy(s.tls), gomail.WithTimeout(20 * time.Second),
		gomail.WithDialContextFunc(dial)}
	if s.user != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthPlain), gomail.WithUsername(s.user), gomail.WithPassword(s.pass))
	}
	c, err := gomail.NewClient(s.host, opts...)
	if err != nil {
		return fmt.Errorf("mail: клиент SMTP: %w", err)
	}
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("mail: отправка прервана: %w", ctxErr)
		}
		return scrub(err)
	}
	return nil
}

// scrub — ошибка отправки без персональных данных: текст SendError go-mail дописывает адреса
// получателей, а ответ сервера на RCPT TO часто повторяет адрес. Из SendError берём только
// причину и коды; остальные ошибки (соединение, контекст) — с маскированием на всякий случай.
func scrub(err error) error {
	var se *gomail.SendError
	if errors.As(err, &se) {
		msg := fmt.Sprintf("mail: отправка: %s", se.Reason)
		if code := se.ErrorCode(); code != 0 {
			// расширенный код (5.1.1) — только если сервер объявил ENHANCEDSTATUSCODES
			if esc := se.EnhancedStatusCode(); esc != "" {
				msg += fmt.Sprintf(" (SMTP %d %s)", code, esc)
			} else {
				msg += fmt.Sprintf(" (SMTP %d)", code)
			}
		}
		return errors.New(msg)
	}
	return &maskedError{msg: logx.Mask("mail: отправка: " + err.Error()), cause: err}
}

// maskedError — текст без ПД, цепочка (context.DeadlineExceeded и т. п.) сохранена для errors.Is.
type maskedError struct {
	msg   string
	cause error
}

func (e *maskedError) Error() string { return e.msg }
func (e *maskedError) Unwrap() error { return e.cause }
