package domain_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/identity/internal/domain"
)

// Ник (спека identity §5.8): формат проверяет домен, регистр и двойники — normalize_text базы.
func TestCheckNickname(t *testing.T) {
	cases := []struct {
		in, want string // want "" — отказ
	}{
		{"abc", "abc"},
		{"ab", ""}, // короче 3
		{"abcdefghijklmnopqrst", "abcdefghijklmnopqrst"}, // ровно 20
		{"abcdefghijklmnopqrstu", ""},                    // 21
		{"Иван", "Иван"},
		{"Ваня_2010", "Ваня_2010"},
		{"ivan.petrov", "ivan.petrov"},
		{"ivan-petrov", "ivan-petrov"},
		{"a_b.c-d", "a_b.c-d"},
		{"Ｉｖａｎ", "Ivan"},                                  // полноширинные → NFKC
		{"ａｂ", ""},                                        // 2 символа после NFKC
		{strings.Repeat("ﬁ", 10), "fifififififififififi"}, // лигатуры: 10 → 20 после NFKC
		{strings.Repeat("ﬁ", 11), ""},                     // 11 → 22 после NFKC
		{"a..b", ""},                                      // два знака подряд
		{"a._b", ""},                                      // два знака подряд
		{".ab", ""},                                       // знак в начале
		{"ab-", ""},                                       // знак в конце
		{"_abc", ""},                                      // знак в начале
		{"123", ""},                                       // одни цифры
		{"1234567", ""},
		{"١٢٣", ""}, // одни цифры (арабско-индийские)
		{"123a", "123a"},
		{"a\u200bbc", ""},      // невидимый
		{"ab\u2800c", ""},      // пустой символ Брайля
		{"abc\u00ad", ""},      // мягкий перенос (Cf)
		{"ab\tc", ""},          // управляющий
		{"ab c", ""},           // пробел внутри
		{"ab\U0001F600c", ""},  // эмодзи
		{"ab@c", ""},           // недопустимый знак
		{"1.2.3", ""},          // цифры и знаки без букв
		{"12-34", ""},          // цифры и знаки без букв
		{"1_000", ""},          // цифры и знаки без букв
		{"deleted_0a1b2c", ""}, // форма заглушки с другим разделителем
		{"deleted.0a1b2c", ""},
		{"dеlеtеd-0a1b", ""}, // кириллическая «е»
		{"ԁeleted-0a1b", ""}, // кириллическая «ԁ»
		{"de1eted-0a1b", ""}, // «1» вместо «l»
		{"DELETED-0A1B", ""},
		{"délèted-0a1b", ""},         // Task 7 m7: диакритика (normalize_text через unaccent сводит к заглушке)
		{"ḍeleted-0a1b", ""},         // точка под буквой
		{"DÉLETED_0a1b", ""},         // диакритика и верхний регистр
		{"đeleted-0a1b", ""},         // уборка W5 m1: перечёркнутые без разложения NFD (unaccent сводит к d, l, t, e)
		{"Đeleted-0a1b", ""},         //
		{"dełeted-0a1b", ""},         //
		{"deŁeted-0a1b", ""},         //
		{"deleŧed-0a1b", ""},         //
		{"dɇleted-0a1b", ""},         //
		{"ɗeƚeʈeɖ-0a1b", ""},         //
		{"đøħ", "đøħ"},               // перечёркнутые — обычные буквы ника
		{"délètedfan", "délètedfan"}, // без разделителя — обычный ник и с диакритикой
		{"deletedfan", "deletedfan"}, // без разделителя — обычный ник
		{"deleted-0a1b2c3d", ""},
		{"Deleted-0a1b2c3d", ""},
		{"deleted", "deleted"}, // форма заглушки — только с дефисом
		{"Ivаn", "Ivаn"},       // кириллическая «а» среди латиницы: формат ок, двойника ловит БД
		{"", ""},
		{"  abc  ", "abc"}, // пробелы по краям — обрезаются
		{"José", "José"},   // NFD → NFC (составной символ)
	}
	for _, c := range cases {
		got, problem := domain.CheckNickname(c.in)
		if c.want == "" {
			if problem != domain.NicknameInvalid || got != "" {
				t.Errorf("%q: ждали отказ, получили %q %q", c.in, got, problem)
			}
			continue
		}
		if problem != domain.NicknameOK || got != c.want {
			t.Errorf("%q: ждали %q, получили %q %q", c.in, c.want, got, problem)
		}
	}
}

func TestNicknameTerms(t *testing.T) {
	if domain.NicknameChangeEvery.Hours() != 30*24 || domain.NicknameQuarantine.Hours() != 90*24 {
		t.Fatalf("%s %s", domain.NicknameChangeEvery, domain.NicknameQuarantine)
	}
}

// Заглушка ника удалённого: deleted-<12 hex> из случайной части UUIDv7 (не из метки времени — иначе удалённые в одну
// минуту совпали бы); ровно 20 символов — предел ника, но CheckNickname её отвергает.
func TestDeletedNickname(t *testing.T) {
	a := uuid.MustParse("01930000-0000-7000-8000-0a1b2c3d4e5f")
	got := domain.DeletedNickname(a)
	if got != "deleted-0a1b2c3d4e5f" {
		t.Fatalf("%q", got)
	}
	// та же метка времени, разная случайная часть — разные заглушки (в том числе в старших байтах хвоста)
	for _, other := range []string{"01930000-0000-7000-8000-ff1b2c3d4e5f", "01930000-0000-7000-8000-0a1b2c3d4e50"} {
		if domain.DeletedNickname(uuid.MustParse(other)) == got {
			t.Errorf("%s — та же заглушка", other)
		}
	}
	// разная метка времени, та же случайная часть — метка в заглушку не входит
	if domain.DeletedNickname(uuid.MustParse("01990000-0000-7000-8000-0a1b2c3d4e5f")) != got {
		t.Error("метка времени попала в заглушку")
	}
	if len(got) != domain.NicknameMaxLen || !strings.HasPrefix(got, "deleted-") {
		t.Fatalf("%q", got)
	}
	if _, p := domain.CheckNickname(got); p != domain.NicknameInvalid {
		t.Fatal("заглушку можно выбрать ником")
	}
}
