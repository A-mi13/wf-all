package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"wf/backend/internal/httpapi/admin"
	"wf/backend/internal/platform/token"
	"wf/backend/internal/staff"
)

func TestRunFailsWithoutConfig(t *testing.T) {
	err := run(context.Background(), nil, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ADMIN_") {
		t.Fatalf("err = %v", err)
	}
}

// offlineEnv — все обязательные переменные, база недостижима: отказ должен случиться до подключения.
func offlineEnv(extra ...string) []string {
	return append([]string{"ADMIN_HTTP_ADDR=127.0.0.1:0", "ADMIN_DATABASE_URL=postgres://a@127.0.0.1:1/wf?connect_timeout=1",
		"ADMIN_ORIGIN=http://127.0.0.1:5173",
		// 32 нулевых байта: годный ключ без энтропии (gitleaks не примет за секрет)
		"ADMIN_STAFF_SECRET_KEYS=" + strings.Repeat("A", 43) + "=",
		"ADMIN_MAIL_SMTP_ADDR=127.0.0.1:1", "ADMIN_MAIL_FROM=WF <noreply@wf.local>"}, extra...)
}

// Заголовок CDN без прокси хостинга — отказ старта до подключения к базе: ADMIN_CLIENT_IP_HEADER
// доходит до peer.Config. Остальные обязательные переменные заданы: иначе отказ был бы про них.
func TestRunRejectsClientIPHeaderWithoutProxies(t *testing.T) {
	err := run(context.Background(), nil, offlineEnv("ADMIN_CLIENT_IP_HEADER=CF-Connecting-IP"), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "CF-Connecting-IP") {
		t.Fatalf("err = %v", err)
	}
}

// Неверный ADMIN_ORIGIN и аллоулист «пускает всех» — отказ старта до подключения к базе.
func TestRunRejectsBadOriginAndAllowlist(t *testing.T) {
	for _, extra := range [][]string{
		{"ADMIN_ORIGIN=http://127.0.0.1:5173/"},
		{"ADMIN_IP_ALLOWLIST=10.0.0.0/8,0.0.0.0/0"},
	} {
		err := run(context.Background(), nil, offlineEnv(extra...), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "ADMIN_") || strings.Contains(err.Error(), "connect") {
			t.Errorf("%v: err = %v", extra, err)
		}
	}
}

// Отказ старта лучше тихой деградации: пустое или битое кольцо ключей сотрудников — ошибка до подключения к
// базе, и у сервера, и у подкоманд (rekey при пустом кольце не запустится).
func TestRunRejectsBadStaffKeys(t *testing.T) {
	for _, keysEnv := range []string{"ADMIN_STAFF_SECRET_KEYS=c2hvcnQ=", "ADMIN_STAFF_SECRET_KEYS=,", "ADMIN_STAFF_SECRET_KEYS=не-base64"} {
		for _, args := range [][]string{nil, {"staff", "rekey"}} {
			err := run(context.Background(), args, offlineEnv(keysEnv), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "ADMIN_STAFF_SECRET_KEYS") || strings.Contains(err.Error(), "connect") {
				t.Errorf("%s %v: err = %v", keysEnv, args, err)
			}
		}
	}
}

// SMTP серверу обязателен: без ADMIN_MAIL_* или с битым адресом — отказ старта до подключения к базе (письма
// с кодами иначе молча копились бы в очереди).
func TestRunRequiresMail(t *testing.T) {
	var env []string
	for _, kv := range offlineEnv() {
		if !strings.HasPrefix(kv, "ADMIN_MAIL_SMTP_ADDR=") {
			env = append(env, kv)
		}
	}
	if err := run(context.Background(), nil, env, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "ADMIN_MAIL_SMTP_ADDR") {
		t.Fatalf("без SMTP: %v", err)
	}
	err := run(context.Background(), nil, offlineEnv("ADMIN_MAIL_SMTP_ADDR=nohost"), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mail") || strings.Contains(err.Error(), "connect") {
		t.Fatalf("битый SMTP: %v", err)
	}
}

// Неизвестная подкоманда, флаг или нехватка аргументов — ошибка использования (код выхода 2) до чтения
// конфига и подключения к базе: опечатка оператора не поднимает сервер.
func TestRunUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"stuff"},
		{"staff"},
		{"staff", "frobnicate"},
		{"staff", "bootstrap", "--email", "o@wf.example"},
		{"staff", "bootstrap", "--name", "Анна"},
		{"staff", "bootstrap", "--email", "o@wf.example", "--name", "Анна", "лишнее"},
		{"staff", "invite-resend"},
		{"staff", "reset-access", "--emial", "o@wf.example"},
		{"staff", "disable", "--email", "o@wf.example", "--print-code"},
		{"staff", "rekey", "--email", "o@wf.example"},
		{"staff", "disable", "--email", "o@wf.example", "--keys-lost"},
		{"staff", "rekey", "--keys-lost"},
	} {
		err := run(context.Background(), args, nil, io.Discard, io.Discard)
		if !errors.Is(err, errUsage) || exitCode(err) != 2 {
			t.Errorf("%v: err = %v, код %d — ждали ошибку использования и код 2", args, err, exitCode(err))
		}
	}
}

// 10c m-r8: --keys-lost и --print-code принимают ровно три команды, выпускающие код (bootstrap, invite-resend,
// reset-access); у disable и rekey флага нет (TestRunUnknownSubcommand). Без флагов — оба false.
func TestParseCommandFlags(t *testing.T) {
	cases := []struct {
		args []string
		want command
	}{
		{[]string{"staff", "bootstrap", "--email", "o@wf.example", "--name", "Анна", "--keys-lost"},
			command{name: "bootstrap", email: "o@wf.example", fullName: "Анна", keysLost: true}},
		{[]string{"staff", "bootstrap", "--email", "o@wf.example", "--name", "Анна"},
			command{name: "bootstrap", email: "o@wf.example", fullName: "Анна"}},
		{[]string{"staff", "invite-resend", "--email", "o@wf.example", "--keys-lost", "--print-code"},
			command{name: "invite-resend", email: "o@wf.example", printCode: true, keysLost: true}},
		{[]string{"staff", "invite-resend", "--email", "o@wf.example"},
			command{name: "invite-resend", email: "o@wf.example"}},
		{[]string{"staff", "reset-access", "--keys-lost", "--email", "o@wf.example"},
			command{name: "reset-access", email: "o@wf.example", keysLost: true}},
		{[]string{"staff", "reset-access", "--email", "o@wf.example", "--print-code"},
			command{name: "reset-access", email: "o@wf.example", printCode: true}},
		{[]string{"staff", "disable", "--email", "o@wf.example"}, command{name: "disable", email: "o@wf.example"}},
		{[]string{"staff", "rekey"}, command{name: "rekey"}},
	}
	for _, c := range cases {
		got, err := parseCommand(c.args)
		if err != nil || got == nil || *got != c.want {
			t.Errorf("%v: %+v, %v; ждали %+v", c.args, got, err, c.want)
		}
	}
}

func TestExitCode(t *testing.T) {
	if c := exitCode(errors.New("сбой")); c != 1 {
		t.Fatalf("обычная ошибка: %d", c)
	}
	if c := exitCode(usageError("staff: неизвестная подкоманда")); c != 2 {
		t.Fatalf("ошибка использования: %d", c)
	}
}

// operator — подкоманда оператора: вывод (stdout) и журнал (stderr) раздельно.
func (e *testEnv) operator(args ...string) (string, string, error) {
	e.t.Helper()
	var out, logOut bytes.Buffer
	err := run(context.Background(), append([]string{"staff"}, args...), e.environ(), &out, &logOut)
	return out.String(), logOut.String(), err
}

// Команды оператора (§4.2): код приглашения — только с --print-code и только в stdout (страж: ни в журнал,
// ни в текст ошибки); почта в выводе и ошибках — маской; письма — в очереди staff (команда SMTP не трогает:
// адрес SMTP заглушки в этом тесте никто не слушает до запуска сервера) — вывод говорит об этом. Повторный
// bootstrap той же почты, disable последнего владельца, неизвестная почта — подсказка и код выхода 1.
func TestOperatorCommands(t *testing.T) {
	e := newTestEnv(t)
	secretsIn := func(t *testing.T, where, text string, secrets ...string) {
		t.Helper()
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("%s содержит %q:\n%s", where, s, text)
			}
		}
	}

	out, logOut, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В.")
	if err != nil {
		t.Fatalf("bootstrap: %v\n%s", err, logOut)
	}
	code1 := e.liveCode("anna@wf.example")
	if !strings.Contains(out, "очеред") || !strings.Contains(out, "staff") || !strings.Contains(out, "***@wf.example") {
		t.Errorf("вывод bootstrap:\n%s", out)
	}
	secretsIn(t, "вывод без --print-code", out, code1, "anna@wf.example")
	secretsIn(t, "журнал", logOut, code1, "anna@wf.example")

	out, logOut, err = e.operator("bootstrap", "--email", "boris@wf.example", "--name", "Борис", "--print-code")
	if err != nil {
		t.Fatalf("bootstrap --print-code: %v", err)
	}
	code2 := e.liveCode("boris@wf.example")
	if !strings.Contains(out, code2) {
		t.Errorf("--print-code: кода %q нет в выводе:\n%s", code2, out)
	}
	secretsIn(t, "журнал при --print-code", logOut, code2, "boris@wf.example")
	if e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail' AND queue = 'staff'`) < 2 {
		t.Error("письма приглашений не поставлены в очередь staff")
	}

	_, logOut, err = e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В.", "--print-code")
	if !errors.Is(err, staff.ErrEmailTaken) || exitCode(err) != 1 || !strings.Contains(err.Error(), "invite-resend") {
		t.Errorf("повторный bootstrap: %v (код %d)", err, exitCode(err))
	}
	secretsIn(t, "ошибка повторного bootstrap", err.Error()+logOut, "anna@wf.example", code1)

	out, _, err = e.operator("invite-resend", "--email", "anna@wf.example", "--print-code")
	if err != nil {
		t.Fatalf("invite-resend: %v", err)
	}
	if code := e.liveCode("anna@wf.example"); code == code1 || !strings.Contains(out, code) {
		t.Errorf("invite-resend: новый код %q не в выводе:\n%s", code, out)
	}
	out, _, err = e.operator("reset-access", "--email", "boris@wf.example")
	if err != nil {
		t.Fatalf("reset-access: %v", err)
	}
	secretsIn(t, "вывод reset-access без --print-code", out, e.liveCode("boris@wf.example"))

	if _, _, err := e.operator("invite-resend", "--email", "nobody@wf.example"); !errors.Is(err, staff.ErrNotFound) || exitCode(err) != 1 {
		t.Errorf("invite-resend несуществующей: %v", err)
	}
	// единственный активный владелец — выключить нельзя
	e.exec(`UPDATE staff_accounts SET status = 'active', password_hash = '$argon2id$stub' WHERE email = 'anna@wf.example'`)
	_, _, err = e.operator("disable", "--email", "anna@wf.example")
	if !errors.Is(err, staff.ErrLastOwner) || exitCode(err) != 1 || !strings.Contains(err.Error(), "последний") ||
		!strings.Contains(err.Error(), "bootstrap") {
		t.Errorf("disable последнего владельца: %v", err)
	}
	if e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'anna@wf.example' AND status = 'active'`) != 1 {
		t.Error("последний владелец выключен")
	}
}

// rekey (§4.11, M-1b): вывод — все счётчики итога и id нечитаемых строк; нечитаемые есть (ключ убран раньше
// rekey) — код выхода ≠ 0 и подсказка; всё на текущем ключе — «можно убрать».
func TestOperatorRekey(t *testing.T) {
	e := newTestEnv(t)
	if _, _, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В."); err != nil {
		t.Fatal(err)
	}
	var iid uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM staff_invites`).Scan(&iid); err != nil {
		t.Fatal(err)
	}
	out, _, err := e.operator("rekey")
	if err != nil {
		t.Fatalf("rekey: %v\n%s", err, out)
	}
	for _, want := range []string{"перешифровано", "TOTP 0", "приглашений 0", "резервных кодов 0", "можно убрать"} {
		if !strings.Contains(out, want) {
			t.Errorf("вывод rekey: нет %q:\n%s", want, out)
		}
	}
	// ключ приглашения убран из кольца раньше rekey
	other := newTestEnv(t)
	var buf, logOut bytes.Buffer
	err = run(context.Background(), []string{"staff", "rekey"}, e.environ("ADMIN_STAFF_SECRET_KEYS="+other.key), &buf, &logOut)
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("rekey с нечитаемыми: %v", err)
	}
	for _, want := range []string{iid.String(), "приглашений 1", "нечитаем"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("вывод rekey с нечитаемыми: нет %q:\n%s", want, buf.String())
		}
	}
}

// staffSession — активная сессия сотрудника с адреса 127.0.0.1 (как после входа со вторым фактором): токен
// cookie.
func (e *testEnv) staffSession(email, role string) (uuid.UUID, string) {
	e.t.Helper()
	sid := uuid.New()
	now := time.Now().UTC()
	e.exec(`INSERT INTO staff_accounts (id, email, display_name, locale, status, password_hash, created_at)
		VALUES ($1, $2, $3, 'ru', 'active', '$argon2id$stub', $4)`, sid, email, "Сотрудник "+strings.Split(email, "@")[0], now)
	e.exec(`INSERT INTO staff_roles (staff_id, role, granted_at) VALUES ($1, $2::staff_role, $3)`, sid, role, now)
	raw, hash, err := token.NewRefresh()
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO staff_sessions (id, staff_id, token_hash, stage, created_at, last_seen_at, mfa_at, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, 'active', $4, $4, $4, $5, '127.0.0.1', 'test')`, uuid.New(), sid, hash, now, now.Add(8*time.Hour))
	return sid, raw
}

// request — запрос к поднятому admin-api: мутации — с Origin, X-WF-Admin и Idempotency-Key.
func (e *testEnv) request(method, path, tok, body string) (int, string) {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+e.addr+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: admin.CookieName(true), Value: tok, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("X-WF-Admin", "1")
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// startServer — admin-api в горутине до отмены; итог run — в канале.
func (e *testEnv) startServer() (context.CancelFunc, <-chan error) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, e.environ(), io.Discard, io.Discard) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if code, _ := e.tryGet("/healthz"); code == http.StatusOK {
			return cancel, done
		}
		select {
		case err := <-done:
			e.t.Fatalf("admin-api не стартовал: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			e.t.Fatal("admin-api не ответил /healthz за 30 с")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *testEnv) tryGet(path string) (int, error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+e.addr+path, nil)
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// Собранный сервер (Task 10c): GET /v1/staff/me без cookie → 401 (настоящие сессии — TestServerApproveExecutes);
// письмо, поставленное подкомандой, исполняет фон сервера (River очереди staff под ролью admin, MailWorker,
// SMTP ADMIN_MAIL_*); SIGTERM во время письма — сервис ждёт его (мягкая остановка River: ctx письма не
// отменяется), письмо доигрывается, задача завершена, сервис останавливается без ошибки.
func TestServerServesStaffAndSendsMail(t *testing.T) {
	e := newTestEnv(t)
	hs := holdMail(t)
	cancel, done := e.startServer()
	if code, body := e.request(http.MethodGet, "/v1/staff/me", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/staff/me без cookie: %d %s", code, body)
	}
	if _, _, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В."); err != nil {
		t.Fatal(err)
	}
	l := e.smtp.waitLetter(t, "anna@wf.example")
	if !strings.Contains(l.Data, e.liveCode("anna@wf.example")) {
		t.Fatalf("в письме нет кода приглашения:\n%s", l.Data)
	}
	// письмо в полёте при остановке: отправитель держит его до hold
	hold := make(chan struct{})
	hs.setHold(hold)
	if _, _, err := e.operator("bootstrap", "--email", "boris@wf.example", "--name", "Борис"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hs.inSend:
	case <-time.After(30 * time.Second):
		t.Fatal("письмо не взято в работу")
	}
	cancel() // SIGTERM
	select {
	case err := <-done:
		t.Fatalf("сервис остановился, не дождавшись письма: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(hold)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("остановка: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("сервис не остановился")
	}
	if hs.cancelled.Load() {
		t.Fatal("ctx письма в полёте отменён — жёсткая остановка вместо мягкой")
	}
	e.smtp.waitLetter(t, "boris@wf.example")
	if n := e.count(`SELECT count(*) FROM river_job WHERE kind = 'staff.mail' AND state = 'completed'`); n != 2 {
		t.Fatalf("завершённых писем %d, ждали 2 (письмо в полёте доиграно)", n)
	}
}

// Сборка (ревью 10b, отказ 1): подтверждение исполняет тот же сервис, в котором staff зарегистрировал
// действия, — approve через собранный сервер отвечает 200 executed (с двумя разными сервисами — 500 и запрос
// висит pending). Исполнение ставит письмо сброса — его отправляет фон.
func TestServerApproveExecutes(t *testing.T) {
	e := newTestEnv(t)
	_, a := e.staffSession("anna@wf.example", "owner")
	_, b := e.staffSession("boris@wf.example", "owner")
	target, _ := e.staffSession("ivan@wf.example", "admin")
	cancel, done := e.startServer()
	// загрузчик сессий — настоящий (у нулевой сборки — auth.NoStaffSessions: любая cookie недействительна)
	if code, body := e.request(http.MethodGet, "/v1/staff/me", a, ""); code != http.StatusOK || !strings.Contains(body, `"owner"`) {
		t.Fatalf("GET /v1/staff/me с cookie: %d %s", code, body)
	}
	code, body := e.request(http.MethodPost, "/v1/staff/accounts/"+target.String()+"/access-reset", a,
		`{"reason":"security","confirm":"ivan@wf.example"}`)
	if code != http.StatusAccepted {
		t.Fatalf("сброс доступа: %d %s", code, body)
	}
	var apID uuid.UUID
	if err := e.owner.QueryRow(context.Background(), `SELECT id FROM approvals WHERE status = 'pending'`).Scan(&apID); err != nil {
		t.Fatal(err)
	}
	if code, body := e.request(http.MethodPost, "/v1/approvals/"+apID.String()+"/approve", b, `{}`); code != http.StatusOK ||
		!strings.Contains(body, `"executed"`) {
		t.Fatalf("approve: %d %s", code, body)
	}
	if e.count(`SELECT count(*) FROM staff_accounts WHERE id = $1 AND status = 'invited'`, target) != 1 {
		t.Fatal("сброс не исполнен")
	}
	e.smtp.waitLetter(t, "ivan@wf.example")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}

// Не те ключи у команды (отказ 1 ревью 10c): в базе шифротексты на ключах, которых нет в
// ADMIN_STAFF_SECRET_KEYS команды, — команда, шифрующая код (bootstrap, invite-resend, reset-access), отказывает
// до записи (код 1, подсказка без ключей): иначе код зашифрован ключом, которого нет у сервера, письмо
// отменится, а прежнее живое приглашение уже погашено. Кольцо сервера с новым ключом первым — проходит.
func TestOperatorRefusesForeignKeys(t *testing.T) {
	e := newTestEnv(t)
	if _, _, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В."); err != nil {
		t.Fatal(err)
	}
	code := e.liveCode("anna@wf.example")
	other := newTestEnv(t).key
	for _, args := range [][]string{
		{"bootstrap", "--email", "boris@wf.example", "--name", "Борис"},
		{"invite-resend", "--email", "anna@wf.example", "--print-code"},
		{"reset-access", "--email", "anna@wf.example"},
	} {
		var out, logOut bytes.Buffer
		err := run(context.Background(), append([]string{"staff"}, args...), e.environ("ADMIN_STAFF_SECRET_KEYS="+other), &out, &logOut)
		if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "ADMIN_STAFF_SECRET_KEYS") {
			t.Fatalf("%v: err = %v", args, err)
		}
		// 10c m-r7: последствия --keys-lost при не том кольце названы в самом отказе
		if !strings.Contains(err.Error(), "код, который сервер не прочтёт") || !strings.Contains(err.Error(), "переменной сервиса") {
			t.Fatalf("%v: в отказе нет предупреждения о --keys-lost с не тем кольцом: %v", args, err)
		}
		if strings.Contains(err.Error()+out.String()+logOut.String(), other) || strings.Contains(err.Error(), e.key) {
			t.Fatalf("%v: ключ в выводе", args)
		}
	}
	if e.count(`SELECT count(*) FROM staff_accounts WHERE email = 'boris@wf.example'`) != 0 || e.liveCode("anna@wf.example") != code {
		t.Fatal("команда с чужим кольцом что-то записала")
	}
	// кольцо сервера после ротации: новый ключ первым, старый вторым — все ключи базы известны
	var out bytes.Buffer
	if err := run(context.Background(), []string{"staff", "invite-resend", "--email", "anna@wf.example"},
		e.environ("ADMIN_STAFF_SECRET_KEYS="+other+","+e.key), &out, io.Discard); err != nil {
		t.Fatalf("кольцо с новым ключом первым: %v", err)
	}
}

// Потерянный ключ (I-r1, сценарий Б ревью раунда 1): секрет TOTP владельца — на ключе, которого уже нет нигде, а
// остальные секреты базы — на ключе кольца. Документированный выход — reset-access — выполняется (код 0) с
// предупреждением в stderr (число чужих ключей, без самих ключей); отказ — только когда ни один ключ базы не в
// кольце и нет --keys-lost (TestOperatorRefusesForeignKeys, TestOperatorLostWholeKey).
func TestOperatorWarnsOnPartlyUnknownKeys(t *testing.T) {
	e := newTestEnv(t)
	if _, _, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.operator("bootstrap", "--email", "boris@wf.example", "--name", "Борис"); err != nil {
		t.Fatal(err)
	}
	// Анна — активный владелец с секретом TOTP на потерянном ключе
	e.exec(`UPDATE staff_accounts SET status = 'active', password_hash = '$argon2id$stub' WHERE email = 'anna@wf.example'`)
	e.exec(`INSERT INTO staff_mfa (staff_id, secret_enc, key_id, confirmed_at)
		SELECT id, decode('00', 'hex'), 'AAAAAAAAAAA', now() FROM staff_accounts WHERE email = 'anna@wf.example'`)
	e.exec(`UPDATE staff_invites SET revoked_at = now(), code_enc = NULL, code_key_id = NULL
		WHERE staff_id = (SELECT id FROM staff_accounts WHERE email = 'anna@wf.example')`)
	out, logOut, err := e.operator("reset-access", "--email", "anna@wf.example", "--print-code")
	if err != nil {
		t.Fatalf("reset-access при частично чужих ключах: %v", err)
	}
	if !strings.Contains(logOut, "ADMIN_STAFF_SECRET_KEYS") || !strings.Contains(logOut, "(ключей: 1)") {
		t.Fatalf("нет предупреждения в stderr:\n%s", logOut)
	}
	if strings.Contains(logOut, "AAAAAAAAAAA") || strings.Contains(logOut, e.key) {
		t.Fatalf("ключ в предупреждении:\n%s", logOut)
	}
	if code := e.liveCode("anna@wf.example"); !strings.Contains(out, code) {
		t.Fatalf("reset-access не выпустил код:\n%s", out)
	}
}

// Потерян единственный ключ (I-r2 ревью раунда 2): все секреты базы — на ключе, которого нет нигде; по базе это
// неотличимо от «не того кольца». Без флага — отказ (код 1) с подсказкой про --keys-lost, ничего не записано; с
// --keys-lost — предупреждение в stderr (число ключей, без id), команда выполняется: новый код — текущим ключом.
func TestOperatorLostWholeKey(t *testing.T) {
	e := newTestEnv(t)
	if _, _, err := e.operator("bootstrap", "--email", "anna@wf.example", "--name", "Анна В."); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE staff_accounts SET status = 'active', password_hash = '$argon2id$stub' WHERE email = 'anna@wf.example'`)
	e.exec(`INSERT INTO staff_mfa (staff_id, secret_enc, key_id, confirmed_at)
		SELECT id, decode('00', 'hex'), 'AAAAAAAAAAA', now() FROM staff_accounts WHERE email = 'anna@wf.example'`)
	e.exec(`UPDATE staff_invites SET revoked_at = now(), code_enc = NULL, code_key_id = NULL`)
	state := func() string {
		var s string
		if err := e.owner.QueryRow(context.Background(), `SELECT a.status::text || '/' || a.version::text || '/' ||
			(SELECT count(*) FROM staff_invites i WHERE i.staff_id = a.id AND i.revoked_at IS NULL AND i.consumed_at IS NULL)::text
			FROM staff_accounts a WHERE a.email = 'anna@wf.example'`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := state()
	_, _, err := e.operator("reset-access", "--email", "anna@wf.example", "--print-code")
	if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "--keys-lost") {
		t.Fatalf("без --keys-lost: %v", err)
	}
	if after := state(); after != before {
		t.Fatalf("без --keys-lost что-то записано: %s → %s", before, after)
	}
	out, logOut, err := e.operator("reset-access", "--email", "anna@wf.example", "--print-code", "--keys-lost")
	if err != nil {
		t.Fatalf("с --keys-lost: %v", err)
	}
	if !strings.Contains(logOut, "(ключей: 1)") || strings.Contains(logOut, "AAAAAAAAAAA") || strings.Contains(logOut, e.key) {
		t.Fatalf("предупреждение в stderr:\n%s", logOut)
	}
	if code := e.liveCode("anna@wf.example"); !strings.Contains(out, code) {
		t.Fatalf("код в выводе не равен живому (расшифрованному текущим ключом):\n%s", out)
	}
	// пожелание ревью 10c: команда с --keys-lost помечена в журнале действий (разбор инцидента), без флага — нет
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.access_reset' AND after->'keys_lost' = 'true'`); n != 1 {
		t.Fatalf("записей staff.access_reset с keys_lost: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE after ? 'keys_lost'`); n != 1 {
		t.Fatalf("keys_lost у команд без флага: %d записей", n)
	}
}
