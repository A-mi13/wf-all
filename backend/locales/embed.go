// Package locales — тексты писем и пушей бэкенда (спека §6.9, hard rule 6 CLAUDE.md):
// ICU MessageFormat, ключи в ru и en одинаковы (страж — internal/platform/i18n).
package locales

import "embed"

//go:embed *.json
var FS embed.FS
