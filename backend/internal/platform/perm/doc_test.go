package perm_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"wf/backend/internal/platform/perm"
)

// Пути от backend/internal/platform/perm до корня репозитория.
const (
	docPath  = "../../../../docs/05-ролевая-модель.md"
	jsonPath = "../../../../contracts/openapi/admin/permissions.json"
)

func rowAt(md, key string) int { return strings.Index(md, "| `"+key+"` |") }

// Таблица: строка на каждое право, значения ячеек — по матрице, черновые — во второй таблице,
// маркеров внутри нет.
func TestMarkdown(t *testing.T) {
	md := perm.Markdown()
	drafts := strings.Index(md, "Черновые строки будущих модулей")
	if drafts < 0 {
		t.Fatal("нет подписи черновых строк")
	}
	for _, p := range perm.Catalog {
		if n := strings.Count(md, "| `"+p.Key+"` |"); n != 1 {
			t.Errorf("%s: строк в таблице %d", p.Key, n)
		}
		if at := rowAt(md, p.Key); p.Draft != (at > drafts) {
			t.Errorf("%s: черновое=%v, а строка в другой таблице", p.Key, p.Draft)
		}
	}
	for _, want := range []string{
		"| Право | Что разрешает | Вид | Владелец | Админ | Модератор | Поддержка |",
		"| `staff.roles.grant_elevated` | выдать роль владельца или админа | изменение | вдвоём | нет | нет | нет |",
		"| `geo.cities.rename` | названия города по языкам | изменение | да | да | свои города | нет |",
		"| `geo.countries.read` | страны и их правила | чтение | да | да | да | да |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("в таблице нет строки %q", want)
		}
	}
	if strings.Contains(md, perm.DocBegin) || strings.Contains(md, perm.DocEnd) || !strings.HasSuffix(md, "\n") {
		t.Error("Markdown содержит маркеры или не кончается переводом строки")
	}
}

// Каталог для стража контракта: служебные значения и все ключи по порядку с признаками черновика и
// изменения (страж 9a: черновой ключ в контракте запрещён, GET не объявляет право изменения).
func TestJSON(t *testing.T) {
	b, err := perm.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Service     []string `json:"service"`
		Permissions []struct {
			Key      string `json:"key"`
			Draft    bool   `json:"draft"`
			Mutating bool   `json:"mutating"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(doc.Service, perm.Service) || len(doc.Permissions) != len(perm.Catalog) {
		t.Fatalf("каталог: service=%v, прав %d", doc.Service, len(doc.Permissions))
	}
	for i, p := range perm.Catalog {
		got := doc.Permissions[i]
		if got.Key != p.Key || got.Draft != p.Draft || got.Mutating != p.Mutating {
			t.Errorf("%d: %+v, нужно %s draft=%v mutating=%v", i, got, p.Key, p.Draft, p.Mutating)
		}
	}
	if !bytes.Contains(b, []byte(`"mutating": true`)) || !bytes.Contains(b, []byte(`"mutating": false`)) {
		t.Error("в каталоге нет признака mutating")
	}
	if !bytes.HasSuffix(b, []byte("}\n")) {
		t.Error("JSON без завершающего перевода строки")
	}
}

func TestSplice(t *testing.T) {
	doc := "# до\n\n" + perm.DocBegin + "\nустаревшая таблица\n" + perm.DocEnd + "\n\nпосле\n"
	got, err := perm.Splice(doc)
	if err != nil {
		t.Fatal(err)
	}
	if want := "# до\n\n" + perm.DocBegin + "\n" + perm.Markdown() + perm.DocEnd + "\n\nпосле\n"; got != want {
		t.Fatalf("Splice:\n%s\nнужно:\n%s", got, want)
	}
	if again, err := perm.Splice(got); err != nil || again != got {
		t.Fatalf("повторный Splice меняет документ: %v", err)
	}
	for name, bad := range map[string]string{
		"нет маркеров": "# текст\n",
		"нет конца":    perm.DocBegin + "\n",
		"нет начала":   perm.DocEnd + "\n",
		"наоборот":     perm.DocEnd + "\n" + perm.DocBegin + "\n",
		"два начала":   perm.DocBegin + "\n" + perm.DocBegin + "\n" + perm.DocEnd + "\n",
		"два конца":    perm.DocBegin + "\n" + perm.DocEnd + "\n" + perm.DocEnd + "\n",
	} {
		if _, err := perm.Splice(bad); !errors.Is(err, perm.ErrMarkers) {
			t.Errorf("%s: %v, нужен ErrMarkers", name, err)
		}
	}
}

// docs/05 совпадает с матрицей: таблицу между маркерами пишет ./task gen (backend/cmd/permdoc).
func TestDocUpToDate(t *testing.T) {
	src, err := os.ReadFile(docPath) //nolint:gosec // документ репозитория по постоянному пути
	if err != nil {
		t.Fatal(err)
	}
	got, err := perm.Splice(string(src))
	if err != nil {
		t.Fatalf("%s: %v", docPath, err)
	}
	if got != string(src) {
		t.Fatal("docs/05-ролевая-модель.md расходится с platform/perm — ./task gen")
	}
}

// permissions.json совпадает с каталогом (страж x-permission контракта admin читает его).
func TestPermissionsJSONUpToDate(t *testing.T) {
	src, err := os.ReadFile(jsonPath) //nolint:gosec // файл контракта по постоянному пути
	if err != nil {
		t.Fatal(err)
	}
	want, err := perm.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, want) {
		t.Fatal("contracts/openapi/admin/permissions.json расходится с platform/perm — ./task gen")
	}
}
