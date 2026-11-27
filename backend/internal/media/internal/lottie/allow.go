package lottie

// Схема-разрешение Lottie (спека media §3.6, I-8): переносятся только перечисленные здесь поля, остальное
// выбрасывается.
//
// Сверено со схемой LAC 1.0.1 (https://lottie.github.io/lottie-spec/1.0.1/lottie.schema.json, "$version": 10001)
// 26.11.2026. Отличия от схемы — намеренные, каждое помечено «вне схемы» или «уже схемы»:
//   - `v` у анимации — вне схемы LAC (версия формата lottie-web, строка); её пишут все экспортёры и примеры LAC,
//     плееры по ней включают совместимость старых файлов;
//   - `st`, `td`, `hasMask` у слоёв — вне схемы, нужны плеерам (layerPlayerCompat ниже);
//   - `slots` анимации — выбрасываются: значение слота в схеме не типизировано (`p: {}`), разрешить его по схеме
//     нельзя (через слот прошли бы выражения и внешние картинки); `sid` у свойств и ассетов без слотов инертен;
//   - `unknown-layer` схемы (любой другой `ty` слоя) — отказ `ErrUnsafe`, а не перенос: так режутся аудио (6),
//     текст (5) и всё, что появится в будущих версиях; `unknown-shape` — форма выбрасывается;
//   - текстовых слоёв, шрифтов и `chars` в LAC 1.0.1 нет — текст в анимации не допускается вовсе (только формами);
//   - типы значений проверяются мягче схемы: целые — как числа, int-boolean и boolean — bool или число,
//     перечисления-числа (`lc`, `lj`, `tt`, …) — как числа; строковые перечисления и `sc` (#rrggbb) — строго.

// valKind — вид значения поля.
type valKind uint8

const (
	vStr   valKind = iota + 1 // строка
	vNum                      // число
	vBool                     // boolean или int-boolean (bool либо число)
	vNums                     // число или массив чисел (values/vector, values/color, values/gradient)
	vVecs                     // массив массивов чисел (вершины values/bezier)
	vHex                      // values/hexcolor: #rrggbb
	vEnum                     // строка из списка enum
	vNode                     // объект узла node
	vNodes                    // массив объектов узла node
)

// nodeKind — тип узла схемы.
type nodeKind uint8

const (
	nAnimation    nodeKind = iota + 1 // composition/animation
	nAsset                            // assets/all-assets: precomposition (есть layers) или image
	nMarker                           // helpers/marker
	nLayer                            // layers/all-layers: по ty
	nTransform                        // helpers/transform
	nMask                             // helpers/mask
	nShape                            // shapes/all-graphic-elements: по ty
	nStrokeDash                       // shapes/stroke-dash
	nGradient                         // properties/gradient-property
	nEasing                           // properties/easing-handle
	nBezier                           // values/bezier
	nPropScalar                       // properties/scalar-property
	nPropVector                       // properties/vector-property
	nPropPosition                     // properties/position-property
	nPropColor                        // properties/color-property
	nPropBezier                       // properties/bezier-property
	nPropStops                        // properties/gradient-stops
	nSplittable                       // properties/splittable-position-property: s → split-position, иначе position
	nSplit                            // properties/split-position
	nKfVector                         // properties/vector-keyframe (и color-, gradient-keyframe: s — числа)
	nKfPosition                       // properties/position-keyframe
	nKfBezier                         // properties/bezier-keyframe

	nKindEnd // граница: новый тип — перед ней (TestEveryNodeKindHandled обходит все до неё)
)

type field struct {
	kind valKind
	node nodeKind
	enum []string
}

type fields map[string]field

var (
	str  = field{kind: vStr}
	num  = field{kind: vNum}
	bln  = field{kind: vBool}
	nums = field{kind: vNums}
	vecs = field{kind: vVecs}
	hex  = field{kind: vHex}
)

func node(n nodeKind) field     { return field{kind: vNode, node: n} }
func nodes(n nodeKind) field    { return field{kind: vNodes, node: n} }
func enum(vals ...string) field { return field{kind: vEnum, enum: vals} }

func merge(parts ...fields) fields {
	out := fields{}
	for _, p := range parts {
		for k, f := range p {
			out[k] = f
		}
	}
	return out
}

// animationFields — composition/animation. fr, ip, op, w, h, layers обязательны (проверка — sanitize.go);
// fonts и chars не переносятся, но fonts.list[].fPath/origin — отказ (sanitize.go).
var animationFields = fields{
	"v":   str, // вне схемы, см. выше
	"ver": num, "nm": str, "fr": num, "ip": num, "op": num, "w": num, "h": num,
	"assets": nodes(nAsset), "markers": nodes(nMarker), "layers": nodes(nLayer),
}

// precompAssetFields — assets/precomposition (asset + composition).
var precompAssetFields = fields{"id": str, "nm": str, "layers": nodes(nLayer)}

// imageAssetFields — assets/image. p — только вшитые data:image/png|jpeg|webp, на выходе — WebP с e = 1;
// u проверяется и не переносится (sanitize.go).
var imageAssetFields = fields{"id": str, "nm": str, "sid": str, "w": num, "h": num, "p": str, "e": bln}

var markerFields = fields{"cm": str, "tm": num, "dr": num}

// layerPlayerCompat — поля слоя вне схемы: не LAC 1.0.1, нужно lottie-web/Flutter (сверено с исходниками
// lottie-web: RenderableElement.js checkLayerLimits, SVGRendererBase.js renderFrame, SVGBaseElement.js
// createContainerElements, DataManager.js completeLayers). Только числа, ни ссылок, ни выражений.
//   - `st` — время начала слоя: без него время слоя — NaN и слой не рисуется; нет на входе — пишется 0 (sanitize.go);
//   - `td` — слой — источник track matte (уходит в <defs>); без него мат рисуется видимым, слой под матом пропадает;
//     int-boolean или boolean: lottie-web проверяет истинность (`if (this.data.td)`);
//   - `hasMask` — не из входа: выставляется true, если в выходе непустой masksProperties (sanitize.go); без него
//     касательные контура маски не переводятся в абсолютные.
var layerPlayerCompat = fields{"st": num, "td": bln}

// Слои: layers/layer + layers/visual-layer + поля совместимости + поля типа.
var (
	layerBase = merge(fields{
		"nm": str, "hd": bln, "ty": num, "ind": num, "parent": num, "ip": num, "op": num,
		"ks": node(nTransform), "ao": bln, "tt": num, "tp": num, "masksProperties": nodes(nMask),
	}, layerPlayerCompat)
	layerFields = map[int]fields{
		0: merge(layerBase, fields{ // precomposition-layer
			"refId": str, "w": num, "h": num, "sr": num, "st": num, "tm": node(nPropScalar),
		}),
		1: merge(layerBase, fields{"sw": num, "sh": num, "sc": hex}), // solid-layer
		2: merge(layerBase, fields{"refId": str}),                    // image-layer
		3: layerBase,                                                 // null-layer
		4: merge(layerBase, fields{"shapes": nodes(nShape)}),         // shape-layer
	}
)

var transformFields = fields{
	"a": node(nPropPosition), "p": node(nSplittable), "r": node(nPropScalar), "s": node(nPropVector),
	"o": node(nPropScalar), "sk": node(nPropScalar), "sa": node(nPropScalar),
}

var maskFields = fields{"mode": enum("n", "a", "s", "i"), "o": node(nPropScalar), "pt": node(nPropBezier)}

// Формы: shapes/graphic-element + shape | shape-style | modifier + поля типа.
var (
	graphicBase  = fields{"nm": str, "hd": bln, "ty": str}
	shapeBase    = merge(graphicBase, fields{"d": num})
	styleBase    = merge(graphicBase, fields{"o": node(nPropScalar)})
	baseGradient = fields{
		"g": node(nGradient), "s": node(nPropPosition), "e": node(nPropPosition), "t": num,
		"h": node(nPropScalar), "a": node(nPropScalar),
	}
	baseStroke = fields{
		"lc": num, "lj": num, "ml": num, "ml2": node(nPropScalar), "w": node(nPropScalar), "d": nodes(nStrokeDash),
	}
	shapeFields = map[string]fields{
		"el": merge(shapeBase, fields{"p": node(nPropPosition), "s": node(nPropVector)}),
		"rc": merge(shapeBase, fields{"p": node(nPropPosition), "s": node(nPropVector), "r": node(nPropScalar)}),
		"sr": merge(shapeBase, fields{
			"p": node(nPropPosition), "or": node(nPropScalar), "os": node(nPropScalar), "r": node(nPropScalar),
			"pt": node(nPropScalar), "sy": num, "ir": node(nPropScalar), "is": node(nPropScalar),
		}),
		"sh": merge(shapeBase, fields{"ks": node(nPropBezier)}),
		"gr": merge(graphicBase, fields{"np": num, "it": nodes(nShape)}),
		"fl": merge(styleBase, fields{"c": node(nPropColor), "r": num}),
		"gf": merge(styleBase, baseGradient, fields{"r": num}),
		"st": merge(styleBase, baseStroke, fields{"c": node(nPropColor)}),
		"gs": merge(styleBase, baseStroke, baseGradient),
		"tr": merge(graphicBase, transformFields),
		"tm": merge(graphicBase, fields{
			"s": node(nPropScalar), "e": node(nPropScalar), "o": node(nPropScalar), "m": num,
		}),
	}
)

var (
	strokeDashFields = fields{"nm": str, "n": enum("d", "g", "o"), "v": node(nPropScalar)}
	gradientFields   = fields{"p": num, "k": node(nPropStops)}
	easingFields     = fields{"x": nums, "y": nums}
	bezierFields     = fields{"c": bln, "i": vecs, "o": vecs, "v": vecs}
	splitFields      = fields{"s": bln, "x": node(nPropScalar), "y": node(nPropScalar)}

	baseKeyframe   = fields{"t": num, "h": bln, "i": node(nEasing), "o": node(nEasing)}
	keyframeFields = map[nodeKind]fields{
		nKfVector:   merge(baseKeyframe, fields{"s": nums}),
		nKfPosition: merge(baseKeyframe, fields{"s": nums, "ti": nums, "to": nums}),
		nKfBezier:   merge(baseKeyframe, fields{"s": nodes(nBezier)}),
	}
)

// propSpec — анимируемое свойство: a, k, sid (slottable-property) и запрещённое x (выражение).
// k — массив ключевых кадров kf, если это массив объектов (так решают плееры, а не по a), иначе — static.
type propSpec struct {
	kf        nodeKind
	static    field
	slottable bool
}

var propSpecs = map[nodeKind]propSpec{
	nPropScalar:   {kf: nKfVector, static: nums, slottable: true},
	nPropVector:   {kf: nKfVector, static: nums, slottable: true},
	nPropPosition: {kf: nKfPosition, static: nums, slottable: true},
	nPropColor:    {kf: nKfVector, static: nums, slottable: true},
	nPropBezier:   {kf: nKfBezier, static: node(nBezier)},
	nPropStops:    {kf: nKfVector, static: nums},
}

// plainNodes — узлы без выбора по полю.
var plainNodes = map[nodeKind]fields{
	nMarker:     markerFields,
	nTransform:  transformFields,
	nMask:       maskFields,
	nStrokeDash: strokeDashFields,
	nGradient:   gradientFields,
	nEasing:     easingFields,
	nBezier:     bezierFields,
	nSplit:      splitFields,
}
