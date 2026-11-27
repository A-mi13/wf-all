//go:build !wf_e2e

package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Спека media §12a: прод-сборка (без тега wf_e2e) не содержит тестовых ручек — ни одного маршрута /_e2e/ на собранном
// публичном api; dev-пути fs при этом смонтированы (обход видит не пустой роутер).
func TestProdBuildHasNoE2ERoutes(t *testing.T) {
	t.Parallel()
	m := newMediaAPI(t, "fs")
	routes, ok := m.h.(chi.Routes)
	if !ok {
		t.Fatalf("обработчик api %T — не chi.Routes", m.h)
	}
	var e2e, dev int
	err := chi.Walk(routes, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch {
		case strings.HasPrefix(route, "/_e2e"):
			e2e++
		case strings.HasPrefix(route, "/_blob/") || strings.HasPrefix(route, "/_media/"):
			dev++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if e2e != 0 || dev == 0 {
		t.Fatalf("маршрутов /_e2e: %d (ждали 0), dev-путей: %d", e2e, dev)
	}
	if h := mediaE2E(m.kit, nil, nil); h != nil {
		t.Fatalf("mediaE2E прод-сборки: %T", h)
	}
}
