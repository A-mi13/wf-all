package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	identityrekey "wf/backend/internal/identity/rekey"
	"wf/backend/internal/platform/keys"
)

// Разовые команды api — без базы: разбор аргументов и окружения до подключения (поведение с базой —
// identity/rekey.TestRekey под ролью api).

func TestCommandRejectsUnknown(t *testing.T) {
	for _, args := range [][]string{{"identity"}, {"identity", "rekeys"}, {"geo", "import"},
		{"identity", "rekey", "extra"}, {"identity", "rekey", "--dry"}} {
		err := dispatch(context.Background(), args, nil, io.Discard, io.Discard)
		if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "identity rekey [--dry-run]") {
			t.Fatalf("%q: %v; want errUsage с подсказкой", args, err)
		}
	}
}

func TestRekeyNeedsRingBeforeDatabase(t *testing.T) {
	key, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		environ []string
		want    string
	}{
		"нет кольца": {[]string{"API_DATABASE_URL=postgres://127.0.0.1:1/x"}, "SECRET_KEYS"},
		"битое кольцо": {[]string{"API_DATABASE_URL=postgres://127.0.0.1:1/x", "API_IDENTITY_SECRET_KEYS=bad",
			"API_IDENTITY_DEVICE_KEYS=" + key, "API_IDENTITY_TOTP_ISSUER=WF"}, "API_IDENTITY_SECRET_KEYS"},
		"нет базы": {[]string{"API_IDENTITY_SECRET_KEYS=" + key, "API_IDENTITY_DEVICE_KEYS=" + key,
			"API_IDENTITY_TOTP_ISSUER=WF"}, "DATABASE_URL"},
	} {
		err := dispatch(context.Background(), []string{"identity", "rekey"}, tc.environ, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v; want ошибку про %s", name, err, tc.want)
		}
	}
}

// Прогон прерван (обрыв соединения посреди пачки) — сначала то, что успели (перешифровано до обрыва, нечитаемые уже
// найденные), без совета убирать ключи, затем ошибка с подсказкой «повторите»; прогон не начался — только ошибка.
func TestRekeyInterruptedPrintsPartial(t *testing.T) {
	bad := uuid.MustParse("01930000-0000-7000-8000-000000000002")
	runErr := errors.New("conn reset")
	var out bytes.Buffer
	err := reportRekey(&out, identityrekey.Report{CurrentKey: "cur", MFA: 3, MFABefore: map[string]int{"old": 5},
		UnreadableMFA: []uuid.UUID{bad}}, runErr)
	if !errors.Is(err, runErr) || !strings.Contains(err.Error(), "повторите") {
		t.Fatalf("ошибка: %v", err)
	}
	for _, w := range []string{"текущий ключ cur", "прогон прерван", "перешифровано до обрыва: секретов TOTP 3",
		"до old=5", "нечитаемые секреты TOTP (id аккаунтов): " + bad.String()} {
		if !strings.Contains(out.String(), w) {
			t.Fatalf("в выводе нет %q:\n%s", w, out.String())
		}
	}
	for _, no := range []string{"можно убрать", "после"} {
		if strings.Contains(out.String(), no) {
			t.Fatalf("в частичном итоге %q:\n%s", no, out.String())
		}
	}
	out.Reset()
	if err := reportRekey(&out, identityrekey.Report{}, runErr); !errors.Is(err, runErr) || out.Len() != 0 {
		t.Fatalf("прогон не начался: %v, вывод %q", err, out.String())
	}
	out.Reset()
	if err := reportRekey(&out, identityrekey.Report{CurrentKey: "cur", RecoveryOldKeys: map[string]int{}}, nil); err != nil ||
		!strings.Contains(out.String(), "по числам старые ключи можно убрать") {
		t.Fatalf("полный прогон: %v\n%s", err, out.String())
	}
}

func TestPrintRekeyReport(t *testing.T) {
	bad := uuid.MustParse("01930000-0000-7000-8000-000000000001")
	for name, tc := range map[string]struct {
		rep     identityrekey.Report
		want    []string
		wantErr bool
	}{
		"чисто": {rep: identityrekey.Report{CurrentKey: "cur", MFA: 2, Apple: 1,
			MFABefore: map[string]int{"old": 2}, MFAAfter: map[string]int{"cur": 2}, AppleBefore: map[string]int{"old": 1},
			AppleAfter: map[string]int{"cur": 1}, RecoveryOldKeys: map[string]int{}},
			want: []string{"текущий ключ cur", "перешифровано: секретов TOTP 2, refresh-токенов Apple 1",
				"секреты TOTP по ключам: до old=2; после cur=2", "резервные коды под старыми ключами: нет",
				"по числам старые ключи можно убрать", "не раньше 7 дней"}},
		"коды под старым": {rep: identityrekey.Report{CurrentKey: "cur", MFAAfter: map[string]int{"cur": 1},
			AppleAfter: map[string]int{}, RecoveryOldKeys: map[string]int{"old": 3}},
			want: []string{"резервные коды под старыми ключами: old=3", "старые ключи убирать нельзя"}},
		"пробный": {rep: identityrekey.Report{CurrentKey: "cur", DryRun: true, MFA: 4, MFABefore: map[string]int{"old": 4}},
			want: []string{"пробный прогон", "было бы перешифровано: секретов TOTP 4"}},
		"нечитаемые": {rep: identityrekey.Report{CurrentKey: "cur", UnreadableMFA: []uuid.UUID{bad}},
			want: []string{"нечитаемые секреты TOTP (id аккаунтов): " + bad.String()}, wantErr: true},
	} {
		var out bytes.Buffer
		err := printRekey(&out, tc.rep)
		for _, w := range tc.want {
			if !strings.Contains(out.String(), w) {
				t.Fatalf("%s: в выводе нет %q:\n%s", name, w, out.String())
			}
		}
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: ошибка %v, want %v", name, err, tc.wantErr)
		}
	}
}
