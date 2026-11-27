package lottie

import (
	"bytes"
	"fmt"
	"strconv"
)

// checkPrecomps — граф прекомпозиций выхода, как его разворачивает lottie-web (DataManager.js completeLayers,
// findCompLayers): слой ty 0 берёт слои первого ассета с id = refId и разворачивает их рекурсивно, каждое повторное
// использование — глубокая копия слоёв; слой-картинка ty 2 — ассет-картинку. Отказ ErrUnsafe (вешает или роняет каждого клиента, сервер ссылок не
// раскрывает): цикл (переполнение стека), refId нет или первый ассет с этим id — не прекомпозиция (TypeError при
// загрузке), сумма байт выхода развёрнутых слоёв больше maxBytes (цепочка «N слоёв → следующий ассет» раздувается
// экспоненциально; плоские ассеты предел глубины не ловит). Разворачивается только то, на что ссылается корень, —
// неиспользуемые ассеты плеер не трогает.
func checkPrecomps(out *jvalue, maxBytes int64) error {
	n, err := expandedBytes(out, maxBytes)
	if err != nil {
		return err
	}
	if n > maxBytes {
		return unsafeAt("layers", fmt.Sprintf("развёрнутые прекомпозиции больше %d байт", maxBytes))
	}
	return nil
}

// expandedBytes — байты выхода всех слоёв, которые развернёт плеер от корня (без самих слоёв корня); больше limit —
// limit+1 (сумма насыщается: цепочка в килобайты даёт 10^20 и больше — переполнение int64).
func expandedBytes(out *jvalue, limit int64) (int64, error) {
	comps := map[string]*precomp{} // первый ассет с id; layers == nil — картинка
	if a, ok := out.get("assets"); ok {
		for i, as := range a.arr {
			id, ok := as.get("id")
			if !ok || comps[id.s] != nil {
				continue
			}
			p := &precomp{index: i}
			p.layers, _ = as.get("layers")
			comps[id.s] = p
		}
	}
	root, _ := out.get("layers")
	g := expander{comps: comps, limit: limit}
	return g.layers(root, "layers")
}

type precomp struct {
	index  int
	layers *jvalue
	state  uint8 // 0 — не разворачивался, 1 — разворачивается (цикл), 2 — готов
	size   int64
}

type expander struct {
	comps map[string]*precomp
	limit int64
}

// layers — сумма развёрнутых прекомпозиций слоёв ty 0 массива (насыщается на limit+1: дальше считать незачем).
// Слой-картинка ty 2 ссылается на первый ассет с id = refId (lottie-web getAssetData): нет refId, нет ассета или он
// прекомпозиция — ErrUnsafe (TypeError или запрос images/undefined у клиента).
func (g *expander) layers(arr *jvalue, path string) (int64, error) {
	var total int64
	for i, l := range arr.arr {
		ty := layerType(l)
		if ty != 0 && ty != 2 {
			continue
		}
		p := fmt.Sprintf("%s[%d].refId", path, i)
		ref, ok := l.get("refId")
		switch {
		case !ok:
			return 0, unsafeAt(p, "слой без refId")
		case ty == 2:
			if c := g.comps[ref.s]; c == nil || c.layers != nil {
				return 0, unsafeAt(p, "ссылка не на картинку")
			}
			continue
		}
		n, err := g.comp(ref.s, p)
		if err != nil {
			return 0, err
		}
		total = min(total+n, g.limit+1)
	}
	return total, nil
}

// comp — байты выхода слоёв прекомпозиции id вместе со всем, что они разворачивают.
func (g *expander) comp(id, path string) (int64, error) {
	c := g.comps[id]
	switch {
	case c == nil || c.layers == nil:
		return 0, unsafeAt(path, "ссылка не на прекомпозицию")
	case c.state == 1:
		return 0, unsafeAt(path, "цикл прекомпозиций")
	case c.state == 2:
		return c.size, nil
	}
	c.state = 1
	var buf bytes.Buffer
	if err := write(&buf, c.layers); err != nil {
		return 0, err
	}
	inner, err := g.layers(c.layers, fmt.Sprintf("assets[%d].layers", c.index))
	if err != nil {
		return 0, err
	}
	c.state, c.size = 2, min(int64(buf.Len())+inner, g.limit+1)
	return c.size, nil
}

// layerType — ty слоя выхода (0–4: проверен layer), без ty — −1.
func layerType(l *jvalue) float64 {
	ty, ok := l.get("ty")
	if !ok {
		return -1
	}
	f, err := strconv.ParseFloat(ty.s, 64)
	if err != nil {
		return -1
	}
	return f
}
