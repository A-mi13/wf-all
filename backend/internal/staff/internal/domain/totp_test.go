package domain_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"wf/backend/internal/staff/internal/domain"
)

// rfcSecret — общий секрет тестовых векторов RFC 6238, приложение B (SHA-1).
var rfcSecret = []byte("12345678901234567890")

// Векторы RFC 6238 (приложение B) даны 8-значными; 6-значный код — остаток того же усечённого
// HMAC по 10^6, то есть последние 6 цифр 8-значного (10^6 делит 10^8).
func TestTOTPVectorsRFC6238(t *testing.T) {
	cases := []struct {
		unix  int64
		step  int64
		eight string
	}{
		{59, 1, "94287082"},
		{1111111109, 37037036, "07081804"},
		{1111111111, 37037037, "14050471"},
		{1234567890, 41152263, "89005924"},
		{2000000000, 66666666, "69279037"},
		{20000000000, 666666666, "65353130"},
	}
	for _, c := range cases {
		at := time.Unix(c.unix, 0).UTC()
		if got := domain.TOTPStep(at); got != c.step {
			t.Errorf("TOTPStep(%d) = %d, ждали %d", c.unix, got, c.step)
		}
		want := c.eight[2:]
		if got := domain.TOTPCode(rfcSecret, c.step); got != want {
			t.Errorf("TOTPCode(шаг %d) = %q, ждали %q (последние 6 цифр %s)", c.step, got, want, c.eight)
		}
		if step, ok := domain.VerifyTOTP(rfcSecret, want, at, 0); !ok || step != c.step {
			t.Errorf("VerifyTOTP(%q, %d) = %d %v, ждали %d true", want, c.unix, step, ok, c.step)
		}
	}
}

// Review Focus 2: окно ±1 шаг; код предыдущего шага принимается один раз; тот же код вторично и
// код шага старше last_used_step отвергаются.
func TestTOTPWindow(t *testing.T) {
	now := time.Unix(1234567890, 0).UTC() // шаг T = 41152263, ровно начало шага (1234567890 mod 30 = 0)
	T := domain.TOTPStep(now)
	code := func(step int64) string { return domain.TOTPCode(rfcSecret, step) }

	cases := []struct {
		name     string
		code     string
		lastUsed int64
		wantStep int64
		wantOK   bool
	}{
		{"текущий шаг", code(T), 0, T, true},
		{"предыдущий шаг — в окне", code(T - 1), 0, T - 1, true},
		{"следующий шаг — в окне", code(T + 1), 0, T + 1, true},
		{"два шага назад — вне окна", code(T - 2), 0, 0, false},
		{"два шага вперёд — вне окна", code(T + 2), 0, 0, false},
		{"предыдущий шаг повторно (last_used = T-1)", code(T - 1), T - 1, 0, false},
		{"текущий после принятого текущего", code(T), T, 0, false},
		{"предыдущий после принятого текущего — старше last_used", code(T - 1), T, 0, false},
		{"следующий после принятого текущего", code(T + 1), T, T + 1, true},
		{"с пробелами — нормализуется", " " + code(T)[:3] + " " + code(T)[3:] + "\n", 0, T, true},
		{"не цифры", "12a456", 0, 0, false},
		{"пять цифр", code(T)[:5], 0, 0, false},
		{"пусто", "", 0, 0, false},
	}
	for _, c := range cases {
		step, ok := domain.VerifyTOTP(rfcSecret, c.code, now, c.lastUsed)
		if ok != c.wantOK || step != c.wantStep {
			t.Errorf("%s: VerifyTOTP(%q, last=%d) = %d %v, ждали %d %v", c.name, c.code, c.lastUsed, step, ok, c.wantStep, c.wantOK)
		}
	}

	// сценарий «код предыдущего шага принимается один раз»: первая проверка ставит last_used,
	// вторая с тем же кодом — отказ
	step, ok := domain.VerifyTOTP(rfcSecret, code(T-1), now, 0)
	if !ok {
		t.Fatal("первый ввод кода предыдущего шага отвергнут")
	}
	if _, again := domain.VerifyTOTP(rfcSecret, code(T-1), now, step); again {
		t.Fatal("код предыдущего шага принят второй раз")
	}
	// граница шага: за секунду до конца шага T (+29 с) код шага T+1 уже принимается; за секунду до
	// конца шага T+1 (+59 с) код T — ещё да; с начала шага T+2 (+60 с) код T — уже нет
	if _, ok := domain.VerifyTOTP(rfcSecret, code(T+1), now.Add(29*time.Second), 0); !ok {
		t.Error("конец шага T: код T+1 отвергнут")
	}
	if _, ok := domain.VerifyTOTP(rfcSecret, code(T), now.Add(59*time.Second), 0); !ok {
		t.Error("конец шага T+1: код T отвергнут")
	}
	if _, ok := domain.VerifyTOTP(rfcSecret, code(T), now.Add(60*time.Second), 0); ok {
		t.Error("шаг T+2: код T принят")
	}
}

func TestNewTOTPSecret(t *testing.T) {
	src := bytes.Repeat([]byte{7}, 25)
	s, err := domain.NewTOTPSecret(bytes.NewReader(src))
	if err != nil || len(s) != domain.TOTPSecretBytes || !bytes.Equal(s, src[:20]) {
		t.Fatalf("NewTOTPSecret = %x %v", s, err)
	}
	if _, err := domain.NewTOTPSecret(bytes.NewReader(make([]byte, 19))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("короткий источник: %v, ждали io.ErrUnexpectedEOF", err)
	}
}

func TestTOTPConstants(t *testing.T) {
	if domain.TOTPDigits != 6 || domain.TOTPPeriod != 30 || domain.TOTPSkew != 1 || domain.TOTPSecretBytes != 20 {
		t.Fatalf("параметры TOTP %d/%d/%d/%d, ждали 6/30/1/20 (спека §4.1)",
			domain.TOTPDigits, domain.TOTPPeriod, domain.TOTPSkew, domain.TOTPSecretBytes)
	}
}

func TestBase32Secret(t *testing.T) {
	cases := map[string]string{
		string(rfcSecret): "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"\xff":            "74", // без '='
		"":                "",
	}
	for in, want := range cases {
		if got := domain.Base32Secret([]byte(in)); got != want {
			t.Errorf("Base32Secret(%x) = %q, ждали %q", in, got, want)
		}
	}
}

func TestOTPAuthURI(t *testing.T) {
	got := domain.OTPAuthURI("Где Футбол Admin", "maria.k+ops@example.com", rfcSecret)
	want := "otpauth://totp/%D0%93%D0%B4%D0%B5%20%D0%A4%D1%83%D1%82%D0%B1%D0%BE%D0%BB%20Admin:" +
		"maria.k%2Bops%40example.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" +
		"&issuer=%D0%93%D0%B4%D0%B5%20%D0%A4%D1%83%D1%82%D0%B1%D0%BE%D0%BB%20Admin" +
		"&algorithm=SHA1&digits=6&period=30"
	if got != want {
		t.Fatalf("OTPAuthURI:\n got  %s\n want %s", got, want)
	}
	// '&' и '=' в имени не ломают query
	if u := domain.OTPAuthURI("A&B=C", "x@y.z", rfcSecret); !bytes.Contains([]byte(u), []byte("issuer=A%26B%3DC&")) {
		t.Fatalf("спецсимволы issuer не экранированы: %s", u)
	}
}
