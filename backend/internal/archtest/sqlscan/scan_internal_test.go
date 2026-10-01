package sqlscan

import "testing"

// Страж закрыт и для незнакомой формы дерева: узел-отношение без relname (например, обёрнутый
// {"RangeVar": …}) — ошибка Scan, а не молча пропущенная таблица.
func TestAddRejectsUnknownRelationShape(t *testing.T) {
	w := &walker{writes: map[string]bool{}, reads: map[string]bool{}, funcs: map[string]bool{}}
	w.add(map[string]any{"RangeVar": map[string]any{"relname": "teams"}}, w.writes, nil)
	if w.err == nil {
		t.Fatalf("ошибки нет, writes = %v", w.writes)
	}
	// отсутствующее отношение (COPY (SELECT …) TO) — не ошибка
	w = &walker{writes: map[string]bool{}, reads: map[string]bool{}, funcs: map[string]bool{}}
	w.add(nil, w.writes, nil)
	if w.err != nil {
		t.Fatalf("пустой узел: %v", w.err)
	}
}
