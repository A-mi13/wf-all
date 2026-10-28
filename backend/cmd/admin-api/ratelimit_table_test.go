package main

import (
	"net/http"
	"testing"
)

// План B, Task 3 (R2): ключи лимитов admin-api — вход по почте (email:<hmac>), IP класса staff_auth, субъект
// сотрудника (staff:default:<id>) — только в staff_rate_limits, права на которую только у роли admin; в
// rate_limits (у api и worker там DML) их нет. Сервер собран как в проде (assemble, конвейер с тем же лимитером)
// и работает под ролью admin: забытый путь в rate_limits дал бы 42501 — лимит «недоступен» и пропуск, строк в
// staff_rate_limits не было бы.
func TestStaffRateLimitsOwnTable(t *testing.T) {
	e := newTestEnv(t)
	sid, tok := e.staffSession("anna@wf.example", "owner")
	cancel, done := e.startServer()

	// неверный пароль: лимит по почте (сценарий входа) и IP-лимит staff_auth (конвейер)
	if code, body := e.request(http.MethodPost, "/v1/staff/auth/login", "",
		`{"email":"boris@wf.example","password":"wrong-password-123"}`); code != http.StatusBadRequest {
		t.Fatalf("вход с неверным паролем: %d %s", code, body)
	}
	// запрос сотрудника: лимит класса default по субъекту
	if code, body := e.request(http.MethodGet, "/v1/staff/me", tok, ""); code != http.StatusOK {
		t.Fatalf("GET /v1/staff/me: %d %s", code, body)
	}

	for _, c := range []struct{ name, sql string }{
		{"почта входа", `SELECT count(*) FROM staff_rate_limits WHERE key LIKE 'email:staff_login:%'`},
		{"IP staff_auth", `SELECT count(*) FROM staff_rate_limits WHERE key LIKE 'ip:staff_auth:%'`},
		{"субъект сотрудника", `SELECT count(*) FROM staff_rate_limits WHERE key = 'staff:default:` + sid.String() + `'`},
	} {
		if n := e.count(c.sql); n != 1 {
			t.Errorf("%s: строк в staff_rate_limits %d, ждали 1", c.name, n)
		}
	}
	if n := e.count(`SELECT count(*) FROM rate_limits`); n != 0 {
		t.Errorf("ключи admin-api в rate_limits: %d строк", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_rate_limits WHERE key LIKE '%wf.example%'`); n != 0 {
		t.Errorf("почта открытым текстом в ключе лимита: %d строк", n)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("остановка: %v", err)
	}
}
