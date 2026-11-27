package lottie

import (
	"bytes"
	"fmt"
	"strconv"
)

// maxPrecompDepth — предел вложенности прекомпозиций от корня: lottie-web (completeLayers, PreCompElement) и
// lottie-android/Flutter (CompositionLayer) разворачивают уровень рекурсией в несколько кадров стека, а ассеты лежат
// плоско — предел глубины JSON цепочку c0 → c1 → … не ловит (ревью R1, I2: 5000 уровней в 448 КБ). Экспорт After
// Effects вкладывает обычно до 10 уровней; 32 — запас ×3 к нему и далеко от стека браузера и устройства.
const maxPrecompDepth = 32

// checkPrecomps — граф ссылок выхода, как его разворачивают плееры (lottie-web DataManager.js completeLayers,
// findCompLayers; lottie-android/Flutter): слой ty 0 берёт слои ассета с id = refId и разворачивает их рекурсивно,
// каждое повторное использование — глубокая копия слоёв; слой-картинка ty 2 — ассет-картинку. Отказ ErrUnsafe (вешает
// или роняет каждого клиента, сервер ссылок не раскрывает): повтор id среди ассетов (lottie-web берёт первый,
// Flutter — последний: проверялся бы не тот, ревью R1, I1), цикл и вложенность больше maxPrecompDepth (переполнение
// стека), refId нет или ассет с этим id не того вида (TypeError при загрузке), сумма байт выхода развёрнутых слоёв
// больше maxBytes (цепочка «N слоёв → следующий ассет» раздувается экспоненциально). Разворачивается только то, на что
// ссылается корень, — неиспользуемые ассеты плеер не трогает (повтор id проверяется у всех).
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
	comps := map[string]*precomp{} // ассет по id; layers == nil — картинка
	if a, ok := out.get("assets"); ok {
		for i, as := range a.arr {
			id, ok := as.get("id")
			if !ok {
				continue
			}
			if comps[id.s] != nil {
				return 0, unsafeAt(fmt.Sprintf("assets[%d].id", i), "повтор id ассета")
			}
			p := &precomp{index: i}
			p.layers, _ = as.get("layers")
			comps[id.s] = p
		}
	}
	root, _ := out.get("layers")
	g := expander{comps: comps, limit: limit}
	n, _, err := g.layers(root, "layers", 1)
	return n, err
}

type precomp struct {
	index  int
	layers *jvalue
	state  uint8 // 0 — не разворачивался, 1 — разворачивается (цикл), 2 — готов
	size   int64
	height int // уровней вложенности прекомпозиций вместе с собой (лист — 1)
}

type expander struct {
	comps map[string]*precomp
	limit int64
}

// layers — сумма развёрнутых прекомпозиций слоёв ty 0 массива (насыщается на limit+1: дальше считать незачем) и
// наибольшая высота их прекомпозиций; level — уровень вложенности прекомпозиций этих слоёв (у корня — 1). Слой-картинка
// ty 2 ссылается на ассет с id = refId (lottie-web getAssetData): нет refId, нет ассета или он прекомпозиция —
// ErrUnsafe (TypeError или запрос images/undefined у клиента).
func (g *expander) layers(arr *jvalue, path string, level int) (total int64, height int, err error) {
	for i, l := range arr.arr {
		ty := layerType(l)
		if ty != 0 && ty != 2 {
			continue
		}
		p := fmt.Sprintf("%s[%d].refId", path, i)
		ref, ok := l.get("refId")
		switch {
		case !ok:
			return 0, 0, unsafeAt(p, "слой без refId")
		case ty == 2:
			if c := g.comps[ref.s]; c == nil || c.layers != nil {
				return 0, 0, unsafeAt(p, "ссылка не на картинку")
			}
			continue
		}
		n, h, err := g.comp(ref.s, p, level)
		if err != nil {
			return 0, 0, err
		}
		total, height = min(total+n, g.limit+1), max(height, h)
	}
	return total, height, nil
}

// comp — байты выхода слоёв прекомпозиции id на уровне level вместе со всем, что они разворачивают, и её высота.
// Рекурсия не глубже maxPrecompDepth + 1: уровень проверяется до спуска, у готовой — уровень + высота.
func (g *expander) comp(id, path string, level int) (int64, int, error) {
	deep := func() error {
		return unsafeAt(path, fmt.Sprintf("вложенность прекомпозиций больше %d", maxPrecompDepth))
	}
	c := g.comps[id]
	switch {
	case c == nil || c.layers == nil:
		return 0, 0, unsafeAt(path, "ссылка не на прекомпозицию")
	case c.state == 1:
		return 0, 0, unsafeAt(path, "цикл прекомпозиций")
	case c.state == 2:
		if level+c.height-1 > maxPrecompDepth {
			return 0, 0, deep()
		}
		return c.size, c.height, nil
	case level > maxPrecompDepth:
		return 0, 0, deep()
	}
	c.state = 1
	var buf bytes.Buffer
	if err := write(&buf, c.layers); err != nil {
		return 0, 0, err
	}
	inner, h, err := g.layers(c.layers, fmt.Sprintf("assets[%d].layers", c.index), level+1)
	if err != nil {
		return 0, 0, err
	}
	c.state, c.size, c.height = 2, min(int64(buf.Len())+inner, g.limit+1), h+1
	return c.size, c.height, nil
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
