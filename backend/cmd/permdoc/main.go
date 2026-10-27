// Команда permdoc пишет таблицу прав в docs/05-ролевая-модель.md (между маркерами
// permissions:begin/end) и каталог прав для стража контракта admin — contracts/openapi/admin/permissions.json.
// Источник — backend/internal/platform/perm. Вызывается из ./task gen (backend:gen) с cwd backend/.
package main

import (
	"flag"
	"fmt"
	"os"

	"wf/backend/internal/platform/perm"
)

func main() {
	doc := flag.String("doc", "../docs/05-ролевая-модель.md", "документ ролевой модели")
	js := flag.String("json", "../contracts/openapi/admin/permissions.json", "каталог прав контракта admin")
	flag.Parse()
	if err := run(*doc, *js); err != nil {
		fmt.Fprintln(os.Stderr, "permdoc:", err)
		os.Exit(1)
	}
}

// run перезаписывает таблицу в документе и каталог. Документ без пары маркеров — ошибка, и ни один
// файл не пишется.
func run(docPath, jsonPath string) error {
	src, err := os.ReadFile(docPath) //nolint:gosec // путь — флаг команды кодогенерации
	if err != nil {
		return err
	}
	out, err := perm.Splice(string(src))
	if err != nil {
		return fmt.Errorf("%s: %w", docPath, err)
	}
	catalog, err := perm.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(docPath, []byte(out), 0o644); err != nil { //nolint:gosec // документ репозитория, не секрет
		return err
	}
	return os.WriteFile(jsonPath, catalog, 0o644) //nolint:gosec // каталог прав контракта, не секрет
}
