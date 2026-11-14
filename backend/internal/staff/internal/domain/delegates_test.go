package domain_test

import (
	"bytes"
	"testing"
	"time"

	"wf/backend/internal/platform/emailaddr"
	"wf/backend/internal/platform/otp"
	"wf/backend/internal/staff/internal/domain"
)

// Перенос в платформу без изменения поведения (план I-2, Task 1): на одном вводе staff и платформа дают
// один ответ — и на мусоре (ok=false — часть поведения).
func TestStaffDomainDelegates(t *testing.T) {
	if domain.TOTPDigits != otp.Digits || domain.TOTPPeriod != otp.Period || domain.TOTPSkew != otp.Skew ||
		domain.TOTPSecretBytes != otp.SecretBytes {
		t.Fatal("константы TOTP staff разошлись с platform/otp")
	}

	emails := []string{
		"maria.k@example.com", "  Maria.K@Example.COM ", "user@клуб.рф",
		"user@XN--90ARD6A.xn--p1ai", "maria.k@exa\u2800mple.com", "\u200bmaria.k@example.com",
		"maria.k@xn--example-ob0e.com", "maria.k@exa\u20ddmple.com", "Ｍａｒｉａ.Ｋ＠Ｅｘａｍｐｌｅ.ＣＯＭ",
		`"maria"@example.com`, "maria..k@example.com", "", "@", "a@b@c",
	}
	for _, in := range emails {
		g, gok := domain.NormalizeEmail(in)
		w, wok := emailaddr.Normalize(in)
		if g != w || gok != wok {
			t.Errorf("NormalizeEmail(%+q) = %q %v, платформа %q %v", in, g, gok, w, wok)
		}
	}

	codes := []string{
		"123456", " 123 456 ", "１２３４５６", "123-456", "123\u200b456", "١٢٣٤٥٦", "12345",
		"abcde-fghij", "ABCDE – FGHIJ", "ＡＢＣＤＥ－ＦＧＨＩＪ", "\ufeffabcde-fghij", "abcde_fghij", "abcde-fgh1j", "",
	}
	for _, in := range codes {
		g, gok := domain.NormalizeCode(in)
		w, wok := otp.NormalizeCode(in)
		if g != w || gok != wok {
			t.Errorf("NormalizeCode(%+q) = %q %v, платформа %q %v", in, g, gok, w, wok)
		}
		w, wok = otp.NormalizeRecoveryCode(in)
		if g, gok = domain.NormalizeRecoveryCode(in); g != w || gok != wok {
			t.Errorf("NormalizeRecoveryCode(%+q) = %q %v, платформа %q %v", in, g, gok, w, wok)
		}
		if g, gok = domain.NormalizeInviteCode(in); g != w || gok != wok {
			t.Errorf("NormalizeInviteCode(%+q) = %q %v, платформа %q %v", in, g, gok, w, wok)
		}
	}

	secret := []byte("12345678901234567890")
	now := time.Unix(1234567890, 0).UTC()
	if domain.TOTPStep(now) != otp.Step(now) {
		t.Fatal("TOTPStep разошёлся с otp.Step")
	}
	T := otp.Step(now)
	for _, step := range []int64{1, T - 2, T - 1, T, T + 1, T + 2} {
		code := otp.Code(secret, step)
		if g := domain.TOTPCode(secret, step); g != code {
			t.Errorf("TOTPCode(%d) = %q, платформа %q", step, g, code)
		}
		for _, last := range []int64{0, T - 1, T, T + 1} {
			gs, gok := domain.VerifyTOTP(secret, code, now, last)
			ws, wok := otp.Verify(secret, code, now, last)
			if gs != ws || gok != wok {
				t.Errorf("VerifyTOTP(шаг %d, last %d) = %d %v, платформа %d %v", step, last, gs, gok, ws, wok)
			}
		}
	}
	if g, w := domain.Base32Secret(secret), otp.Base32(secret); g != w {
		t.Errorf("Base32Secret = %q, платформа %q", g, w)
	}
	if g, w := domain.OTPAuthURI("A&B C", "x+y@z.ru", secret), otp.URI("A&B C", "x+y@z.ru", secret); g != w {
		t.Errorf("OTPAuthURI = %q, платформа %q", g, w)
	}

	src := bytes.Repeat([]byte{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}, 20)
	gs, gerr := domain.NewTOTPSecret(bytes.NewReader(src))
	ws, werr := otp.NewSecret(bytes.NewReader(src))
	if !bytes.Equal(gs, ws) || (gerr == nil) != (werr == nil) {
		t.Errorf("NewTOTPSecret = %x %v, платформа %x %v", gs, gerr, ws, werr)
	}
	gi, _ := domain.NewInviteCode(bytes.NewReader(src))
	wi, _ := otp.NewCode(bytes.NewReader(src))
	if gi != wi {
		t.Errorf("NewInviteCode = %q, платформа %q", gi, wi)
	}
	gr, gerr := domain.NewRecoveryCodes(bytes.NewReader(src))
	wr, werr := otp.NewRecoveryCodes(bytes.NewReader(src))
	if len(gr) != len(wr) || (gerr == nil) != (werr == nil) {
		t.Fatalf("NewRecoveryCodes = %q %v, платформа %q %v", gr, gerr, wr, werr)
	}
	for i := range gr {
		if gr[i] != wr[i] {
			t.Errorf("NewRecoveryCodes[%d] = %q, платформа %q", i, gr[i], wr[i])
		}
	}
}
