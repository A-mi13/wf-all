package otp_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"wf/backend/internal/platform/otp"
)

// rfcSecret — общий секрет тестовых векторов RFC 6238, приложение B (SHA-1).
var rfcSecret = []byte("12345678901234567890")

// Векторы RFC 6238 (приложение B) даны 8-значными; 6-значный код — остаток того же усечённого
// HMAC по 10^6, то есть последние 6 цифр 8-значного (10^6 делит 10^8).
func TestRFC6238Vectors(t *testing.T) {
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
		if got := otp.Step(at); got != c.step {
			t.Errorf("TOTPStep(%d) = %d, ждали %d", c.unix, got, c.step)
		}
		want := c.eight[2:]
		if got := otp.Code(rfcSecret, c.step); got != want {
			t.Errorf("TOTPCode(шаг %d) = %q, ждали %q (последние 6 цифр %s)", c.step, got, want, c.eight)
		}
		if step, ok := otp.Verify(rfcSecret, want, at, 0); !ok || step != c.step {
			t.Errorf("VerifyTOTP(%q, %d) = %d %v, ждали %d true", want, c.unix, step, ok, c.step)
		}
	}
}

// Review Focus 2: окно ±1 шаг; код предыдущего шага принимается один раз; тот же код вторично и
// код шага старше last_used_step отвергаются.
func TestVerifyWindow(t *testing.T) {
	now := time.Unix(1234567890, 0).UTC() // шаг T = 41152263, ровно начало шага (1234567890 mod 30 = 0)
	T := otp.Step(now)
	code := func(step int64) string { return otp.Code(rfcSecret, step) }

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
		step, ok := otp.Verify(rfcSecret, c.code, now, c.lastUsed)
		if ok != c.wantOK || step != c.wantStep {
			t.Errorf("%s: VerifyTOTP(%q, last=%d) = %d %v, ждали %d %v", c.name, c.code, c.lastUsed, step, ok, c.wantStep, c.wantOK)
		}
	}

	// сценарий «код предыдущего шага принимается один раз»: первая проверка ставит last_used,
	// вторая с тем же кодом — отказ
	step, ok := otp.Verify(rfcSecret, code(T-1), now, 0)
	if !ok {
		t.Fatal("первый ввод кода предыдущего шага отвергнут")
	}
	if _, again := otp.Verify(rfcSecret, code(T-1), now, step); again {
		t.Fatal("код предыдущего шага принят второй раз")
	}
	// граница шага: за секунду до конца шага T (+29 с) код шага T+1 уже принимается; за секунду до
	// конца шага T+1 (+59 с) код T — ещё да; с начала шага T+2 (+60 с) код T — уже нет
	if _, ok := otp.Verify(rfcSecret, code(T+1), now.Add(29*time.Second), 0); !ok {
		t.Error("конец шага T: код T+1 отвергнут")
	}
	if _, ok := otp.Verify(rfcSecret, code(T), now.Add(59*time.Second), 0); !ok {
		t.Error("конец шага T+1: код T отвергнут")
	}
	if _, ok := otp.Verify(rfcSecret, code(T), now.Add(60*time.Second), 0); ok {
		t.Error("шаг T+2: код T принят")
	}
}

func TestNewSecret(t *testing.T) {
	src := bytes.Repeat([]byte{7}, 25)
	s, err := otp.NewSecret(bytes.NewReader(src))
	if err != nil || len(s) != otp.SecretBytes || !bytes.Equal(s, src[:20]) {
		t.Fatalf("NewTOTPSecret = %x %v", s, err)
	}
	if _, err := otp.NewSecret(bytes.NewReader(make([]byte, 19))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("короткий источник: %v, ждали io.ErrUnexpectedEOF", err)
	}
}

func TestConstants(t *testing.T) {
	if otp.Digits != 6 || otp.Period != 30 || otp.Skew != 1 || otp.SecretBytes != 20 {
		t.Fatalf("параметры TOTP %d/%d/%d/%d, ждали 6/30/1/20 (спека §4.1)",
			otp.Digits, otp.Period, otp.Skew, otp.SecretBytes)
	}
}

func TestBase32(t *testing.T) {
	cases := map[string]string{
		string(rfcSecret): "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"\xff":            "74", // без '='
		"":                "",
	}
	for in, want := range cases {
		if got := otp.Base32([]byte(in)); got != want {
			t.Errorf("Base32Secret(%x) = %q, ждали %q", in, got, want)
		}
	}
}

func TestURI(t *testing.T) {
	got := otp.URI("Где Футбол Admin", "maria.k+ops@example.com", rfcSecret)
	want := "otpauth://totp/%D0%93%D0%B4%D0%B5%20%D0%A4%D1%83%D1%82%D0%B1%D0%BE%D0%BB%20Admin:" +
		"maria.k%2Bops%40example.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" +
		"&issuer=%D0%93%D0%B4%D0%B5%20%D0%A4%D1%83%D1%82%D0%B1%D0%BE%D0%BB%20Admin" +
		"&algorithm=SHA1&digits=6&period=30"
	if got != want {
		t.Fatalf("OTPAuthURI:\n got  %s\n want %s", got, want)
	}
	// '&' и '=' в имени не ломают query
	if u := otp.URI("A&B=C", "x@y.z", rfcSecret); !bytes.Contains([]byte(u), []byte("issuer=A%26B%3DC&")) {
		t.Fatalf("спецсимволы issuer не экранированы: %s", u)
	}
}

// Повтор кода (спека identity §3.3, спека сотрудников §4.1): шаг, равный last_used_step, и шаг старше
// него не принимаются — иначе перехваченный код входа годился бы второй раз в том же окне.
func TestVerifyRejectsReplayedStep(t *testing.T) {
	now := time.Unix(1234567890, 0).UTC()
	T := otp.Step(now)
	for _, last := range []int64{T, T + 1} {
		if step, ok := otp.Verify(rfcSecret, otp.Code(rfcSecret, T), now, last); ok {
			t.Errorf("код шага %d принят при last_used=%d (шаг %d)", T, last, step)
		}
	}
	if step, ok := otp.Verify(rfcSecret, otp.Code(rfcSecret, T), now, T-1); !ok || step != T {
		t.Fatalf("код шага %d при last_used=%d: %d %v, ждали %d true", T, T-1, step, ok, T)
	}
}

func TestCodeConstants(t *testing.T) {
	if otp.RecoveryCount != 10 || otp.CodeLen != 10 {
		t.Fatalf("RecoveryCount/CodeLen = %d/%d, ждали 10/10 (спека сотрудников §4.1)", otp.RecoveryCount, otp.CodeLen)
	}
}
