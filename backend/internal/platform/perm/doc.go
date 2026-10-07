package perm

import (
	"encoding/json"
	"errors"
	"strings"

	"wf/backend/internal/platform/auth"
)

// Маркеры генерируемой таблицы в docs/05-ролевая-модель.md.
const (
	DocBegin = "<!-- permissions:begin -->"
	DocEnd   = "<!-- permissions:end -->"
)

// ErrMarkers — в документе не ровно одна пара маркеров в правильном порядке.
var ErrMarkers = errors.New("perm: в документе нужна ровно одна пара маркеров permissions:begin … permissions:end")

var roleTitle = map[Role]string{Owner: "Владелец", Admin: "Админ", Moderator: "Модератор", Support: "Поддержка"}

// grantTitle — значение ячейки таблицы, как в макете 07.
func grantTitle(g auth.Grant) string {
	switch g {
	case auth.GrantAllow:
		return "да"
	case auth.GrantScoped:
		return "свои города"
	case auth.GrantDual:
		return "вдвоём"
	default:
		return "нет"
	}
}

// Markdown — содержимое docs/05 между маркерами: таблица прав и таблица черновых строк.
func Markdown() string {
	var b strings.Builder
	b.WriteString("<!-- Генерирует ./task gen (backend/cmd/permdoc) из backend/internal/platform/perm/catalog.go. Руками не править. -->\n\n")
	writeTable(&b, false)
	b.WriteString("\nЧерновые строки будущих модулей — уточнит спека модуля. В правах ролей (`perm.Grants`) и в контракте их нет.\n\n")
	writeTable(&b, true)
	b.WriteString("\n")
	return b.String()
}

func writeTable(b *strings.Builder, drafts bool) {
	b.WriteString("| Право | Что разрешает | Вид |")
	for _, r := range Roles {
		b.WriteString(" " + roleTitle[r] + " |")
	}
	b.WriteString("\n| --- | --- | --- |")
	for range Roles {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, p := range Catalog {
		if p.Draft != drafts {
			continue
		}
		kind := "чтение"
		if p.Mutating {
			kind = "изменение"
		}
		b.WriteString("| `" + p.Key + "` | " + p.Title + " | " + kind + " |")
		for _, r := range Roles {
			b.WriteString(" " + grantTitle(Matrix[p.Key][r]) + " |")
		}
		b.WriteString("\n")
	}
}

// JSON — каталог для стража контракта admin (contracts/openapi/admin/permissions.json): служебные
// значения x-permission и ключи прав по порядку с признаками черновика и изменения.
func JSON() ([]byte, error) {
	type item struct {
		Key      string `json:"key"`
		Draft    bool   `json:"draft"`
		Mutating bool   `json:"mutating"`
	}
	doc := struct {
		Service     []string `json:"service"`
		Permissions []item   `json:"permissions"`
	}{Service: Service, Permissions: make([]item, 0, len(Catalog))}
	for _, p := range Catalog {
		doc.Permissions = append(doc.Permissions, item{Key: p.Key, Draft: p.Draft, Mutating: p.Mutating})
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Splice — документ с Markdown() между маркерами; текст вне маркеров не меняется.
func Splice(doc string) (string, error) {
	if strings.Count(doc, DocBegin) != 1 || strings.Count(doc, DocEnd) != 1 {
		return "", ErrMarkers
	}
	i := strings.Index(doc, DocBegin) + len(DocBegin)
	j := strings.Index(doc, DocEnd)
	if j < i {
		return "", ErrMarkers
	}
	return doc[:i] + "\n" + Markdown() + doc[j:], nil
}
