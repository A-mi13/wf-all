// Package lottie — разбор Lottie-JSON в схему-разрешение (спека media §3.6, I-8): из недоверенного JSON переносятся
// только поля таблицы allow.go, выражения и внешние ссылки — отказ, вшитые растры перекодируются в WebP.
// Пакет не знает ни корня media, ни imaging: перекодирование подставляет вызывающий (ImageFunc).
package lottie

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Limits — пределы анимации (спека §3.6). Все границы включительно: отказ — строго больше предела.
type Limits struct {
	MaxBytes            int     // размер JSON
	MaxDepth            int     // вложенность объектов и массивов, корень — 1
	MaxSeconds          float64 // (op − ip) / fr
	MaxImageBytes       int     // вшитая картинка после base64
	MaxImagePixels      int64   // ширина × высота вшитой картинки по заголовку
	MaxTotalImagePixels int64   // сумма ширина × высота всех вшитых картинок
	MaxExpandedBytes    int64   // слои прекомпозиций, развёрнутые по refId от корня (байты выхода)
}

// DefaultLimits — пределы спеки §3.6; сумма пикселей картинок (4 картинки предела) и развёрнутые прекомпозиции
// (4 документа предела) — доработка media (ревью Task 8, финальное D): одна картинка и одна копия слоёв ограничены и
// раньше, а плеер загружает все картинки и копирует слои на каждое использование.
var DefaultLimits = Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxSeconds: 10, MaxImageBytes: 256 << 10, MaxImagePixels: 4_000_000,
	MaxTotalImagePixels: 16_000_000, MaxExpandedBytes: 4 << 20}

// ImageFunc — перекодирование вшитого растра (png, jpeg или webp по заголовку) в WebP без метаданных
// (Task 9 подставляет imaging.Process).
type ImageFunc func(ctx context.Context, b []byte) (webp []byte, err error)

var (
	// ErrUnsafe — выражения, внешние ссылки, аудио и текст, неизвестные слои, превышение пределов
	// (reject_code unsafe_animation).
	ErrUnsafe = errors.New("lottie: unsafe animation")
	// ErrCorrupt — невалидный JSON или значение известного поля не того типа (reject_code corrupt).
	ErrCorrupt = errors.New("lottie: corrupt")
)

// Sanitize разбирает in по схеме-разрешению и возвращает компактный JSON. Порядок: размер → разбор с подсчётом
// глубины → обход по таблице (все отказы) → граф прекомпозиций (checkPrecomps) → перекодирование картинок (дорогое —
// только для прошедшего проверки).
// Ошибка img возвращается обёрнутой (%w): её код решает вызывающий; отмена ctx — ctx.Err().
func Sanitize(ctx context.Context, in []byte, lim Limits, img ImageFunc) ([]byte, error) {
	if len(in) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: размер %d > %d", ErrUnsafe, len(in), lim.MaxBytes)
	}
	root, err := parse(in, lim.MaxDepth)
	if err != nil {
		return nil, err
	}
	s := &sanitizer{lim: lim}
	out, err := s.animation(root)
	if err != nil {
		return nil, err
	}
	if err := checkPrecomps(out, lim.MaxExpandedBytes); err != nil {
		return nil, err
	}
	for i, im := range s.images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if img == nil {
			return nil, errors.New("lottie: в анимации картинки, а ImageFunc не задана")
		}
		webp, err := img(ctx, im.raw)
		if err != nil {
			return nil, fmt.Errorf("lottie: картинка %d (%s): %w", i, im.path, err)
		}
		im.p.s = "data:image/webp;base64," + base64.StdEncoding.EncodeToString(webp)
	}
	var buf bytes.Buffer
	buf.Grow(len(in))
	if err := write(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- разбор ----

type jkind uint8

const (
	jNull jkind = iota
	jBool
	jNum
	jStr
	jArr
	jObj
)

// jvalue — узел JSON с порядком ключей; число — исходный литерал (json.Number), без потери точности.
type jvalue struct {
	k    jkind
	b    bool
	s    string // строка или литерал числа
	arr  []*jvalue
	keys []string
	vals []*jvalue
}

func (v *jvalue) get(key string) (*jvalue, bool) {
	for i, k := range v.keys {
		if k == key {
			return v.vals[i], true
		}
	}
	return nil, false
}

func (v *jvalue) add(key string, val *jvalue) {
	v.keys = append(v.keys, key)
	v.vals = append(v.vals, val)
}

func corrupt(err error) error { return fmt.Errorf("%w: %w", ErrCorrupt, err) }

// parse читает документ потоком токенов: глубина проверяется на каждой открывающей скобке, до чтения остатка
// (вложенность 10 000 не разбирается целиком). Ровно одно значение верхнего уровня.
func parse(in []byte, maxDepth int) (*jvalue, error) {
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, corrupt(err)
	}
	root, err := parseValue(dec, tok, 1, maxDepth)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: данные после документа", ErrCorrupt)
	}
	return root, nil
}

func parseValue(dec *json.Decoder, tok json.Token, depth, maxDepth int) (*jvalue, error) {
	switch t := tok.(type) {
	case nil:
		return &jvalue{k: jNull}, nil
	case bool:
		return &jvalue{k: jBool, b: t}, nil
	case json.Number:
		return &jvalue{k: jNum, s: string(t)}, nil
	case string:
		return &jvalue{k: jStr, s: t}, nil
	case json.Delim:
		if depth > maxDepth {
			return nil, fmt.Errorf("%w: глубина больше %d", ErrUnsafe, maxDepth)
		}
		if t == '[' {
			v := &jvalue{k: jArr}
			for {
				tok, err := dec.Token()
				if err != nil {
					return nil, corrupt(err)
				}
				if tok == json.Delim(']') {
					return v, nil
				}
				el, err := parseValue(dec, tok, depth+1, maxDepth)
				if err != nil {
					return nil, err
				}
				v.arr = append(v.arr, el)
			}
		}
		// '{': дубль ключа — значение последнего на месте первого (как JSON.parse плеера)
		v := &jvalue{k: jObj}
		idx := map[string]int{}
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, corrupt(err)
			}
			if tok == json.Delim('}') {
				return v, nil
			}
			key := tok.(string) // Decoder гарантирует строку на месте ключа
			tok, err = dec.Token()
			if err != nil {
				return nil, corrupt(err)
			}
			val, err := parseValue(dec, tok, depth+1, maxDepth)
			if err != nil {
				return nil, err
			}
			if i, ok := idx[key]; ok {
				v.vals[i] = val
				continue
			}
			idx[key] = len(v.keys)
			v.add(key, val)
		}
	}
	return nil, fmt.Errorf("%w: неожиданный токен", ErrCorrupt)
}

// ---- обход по схеме-разрешению ----

type pendingImage struct {
	path string
	raw  []byte
	p    *jvalue // значение p в выходе: заменяется на data:image/webp
}

type sanitizer struct {
	lim    Limits
	images []*pendingImage
	pixels int64 // сумма пикселей вшитых картинок
}

func corruptAt(path, what string) error { return fmt.Errorf("%w: %s: %s", ErrCorrupt, path, what) }
func unsafeAt(path, what string) error  { return fmt.Errorf("%w: %s: %s", ErrUnsafe, path, what) }

// number — значение поля-числа; отсутствует или не число — ErrCorrupt.
func number(obj *jvalue, key string) (float64, error) {
	v, ok := obj.get(key)
	if !ok || v.k != jNum {
		return 0, corruptAt(key, "нет числа")
	}
	f, err := strconv.ParseFloat(v.s, 64)
	if err != nil {
		return 0, corruptAt(key, "число вне диапазона")
	}
	return f, nil
}

func (s *sanitizer) animation(root *jvalue) (*jvalue, error) {
	if root.k != jObj {
		return nil, fmt.Errorf("%w: корень — не объект", ErrCorrupt)
	}
	if err := checkFonts(root); err != nil {
		return nil, err
	}
	fr, err := number(root, "fr")
	if err != nil {
		return nil, err
	}
	ip, err := number(root, "ip")
	if err != nil {
		return nil, err
	}
	op, err := number(root, "op")
	if err != nil {
		return nil, err
	}
	if fr <= 0 {
		return nil, unsafeAt("fr", "не больше 0")
	}
	if d := (op - ip) / fr; d > s.lim.MaxSeconds || math.IsNaN(d) {
		return nil, unsafeAt("op", fmt.Sprintf("длительность больше %g с", s.lim.MaxSeconds))
	}
	// размеры читает и воркер (float64): литерал вне диапазона — corrupt здесь, а не повторы задачи (ревью D, m1)
	for _, k := range []string{"w", "h"} {
		if _, err := number(root, k); err != nil {
			return nil, err
		}
	}
	if v, ok := root.get("layers"); !ok || v.k == jNull {
		return nil, corruptAt("layers", "обязательное поле")
	}
	return s.object(animationFields, root, "")
}

// checkFonts — шрифты не переносятся (в LAC 1.0.1 их нет), но ссылка на файл шрифта — явный отказ (§3.6).
func checkFonts(root *jvalue) error {
	fonts, ok := root.get("fonts")
	if !ok || fonts.k != jObj {
		return nil
	}
	list, ok := fonts.get("list")
	if !ok || list.k != jArr {
		return nil
	}
	for i, f := range list.arr {
		if f.k != jObj {
			continue
		}
		for _, k := range []string{"fPath", "origin"} {
			if _, ok := f.get(k); ok {
				return unsafeAt(fmt.Sprintf("fonts.list[%d].%s", i, k), "внешний шрифт")
			}
		}
	}
	return nil
}

// object переносит поля tbl по порядку входа; неизвестные и null — выбрасываются.
func (s *sanitizer) object(tbl fields, v *jvalue, path string) (*jvalue, error) {
	out := &jvalue{k: jObj}
	for i, key := range v.keys {
		f, ok := tbl[key]
		val := v.vals[i]
		if !ok || val.k == jNull {
			continue
		}
		sv, err := s.value(f, val, path+"."+key)
		if err != nil {
			return nil, err
		}
		if sv != nil {
			out.add(key, sv)
		}
	}
	return out, nil
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func isNums(v *jvalue) bool {
	if v.k == jNum {
		return true
	}
	if v.k != jArr {
		return false
	}
	for _, e := range v.arr {
		if e.k != jNum {
			return false
		}
	}
	return true
}

// value проверяет вид значения; узлы — через node. nil без ошибки — выбросить (неизвестная форма).
func (s *sanitizer) value(f field, v *jvalue, path string) (*jvalue, error) {
	ok := false
	switch f.kind {
	case vStr:
		ok = v.k == jStr
	case vNum:
		ok = v.k == jNum
	case vBool:
		ok = v.k == jBool || v.k == jNum
	case vNums:
		ok = isNums(v)
	case vVecs:
		ok = v.k == jArr
		for _, e := range v.arr {
			ok = ok && e.k == jArr && isNums(e)
		}
	case vHex:
		ok = v.k == jStr && hexColor.MatchString(v.s)
	case vEnum:
		for _, e := range f.enum {
			ok = ok || (v.k == jStr && v.s == e)
		}
	case vNode:
		if v.k != jObj {
			return nil, corruptAt(path, "не объект")
		}
		return s.node(f.node, v, path)
	case vNodes:
		if v.k != jArr {
			return nil, corruptAt(path, "не массив")
		}
		out := &jvalue{k: jArr, arr: []*jvalue{}}
		for i, e := range v.arr {
			p := fmt.Sprintf("%s[%d]", path, i)
			if e.k != jObj {
				return nil, corruptAt(p, "не объект")
			}
			sv, err := s.node(f.node, e, p)
			if err != nil {
				return nil, err
			}
			if sv != nil {
				out.arr = append(out.arr, sv)
			}
		}
		return out, nil
	}
	if !ok {
		return nil, corruptAt(path, "значение не по схеме")
	}
	return v, nil
}

func (s *sanitizer) node(kind nodeKind, v *jvalue, path string) (*jvalue, error) {
	if tbl, ok := plainNodes[kind]; ok {
		return s.object(tbl, v, path)
	}
	if tbl, ok := keyframeFields[kind]; ok {
		return s.object(tbl, v, path)
	}
	if spec, ok := propSpecs[kind]; ok {
		return s.property(spec, v, path, false)
	}
	switch kind {
	case nLayer:
		return s.layer(v, path)
	case nShape:
		ty, ok := v.get("ty")
		if !ok || ty.k != jStr {
			return nil, nil
		}
		tbl, ok := shapeFields[ty.s]
		if !ok {
			return nil, nil // unknown-shape схемы: форма выбрасывается
		}
		return s.object(tbl, v, path)
	case nAsset:
		if l, ok := v.get("layers"); ok {
			if l.k == jNull { // lottie-web считает ассет прекомпозицией и падает на layers.__used
				return nil, corruptAt(path+".layers", "прекомпозиция без слоёв")
			}
			return s.object(precompAssetFields, v, path)
		}
		return s.imageAsset(v, path)
	case nSplittable:
		sv, ok := v.get("s")
		if !ok {
			return s.property(propSpecs[nPropPosition], v, path, true)
		}
		switch {
		case sv.k == jBool && sv.b, sv.k == jNum && isNonZero(sv.s):
			return s.object(splitFields, v, path)
		case sv.k == jBool, sv.k == jNum, sv.k == jNull:
			return s.property(propSpecs[nPropPosition], v, path, true)
		}
		return nil, corruptAt(path+".s", "не boolean")
	}
	panic(fmt.Sprintf("lottie: узел %d без таблицы", kind))
}

func isNonZero(lit string) bool {
	f, err := strconv.ParseFloat(lit, 64)
	return err != nil || f != 0
}

// property — анимируемое свойство. Поле x — выражение (JavaScript): отказ при любом значении.
// k — ключевые кадры, если это непустой массив с объектом первым (так выбирают плееры), иначе — значение.
func (s *sanitizer) property(spec propSpec, v *jvalue, path string, grouped bool) (*jvalue, error) {
	if _, ok := v.get("x"); ok {
		return nil, unsafeAt(path+".x", "выражение")
	}
	out := &jvalue{k: jObj}
	for i, key := range v.keys {
		val := v.vals[i]
		if val.k == jNull {
			continue
		}
		var f field
		switch {
		case key == "a", grouped && key == "s":
			f = bln
		case key == "sid" && spec.slottable:
			f = str
		case key == "k":
			f = spec.static
			if val.k == jArr && len(val.arr) > 0 && val.arr[0].k == jObj {
				f = nodes(spec.kf)
			}
		default:
			continue
		}
		sv, err := s.value(f, val, path+"."+key)
		if err != nil {
			return nil, err
		}
		out.add(key, sv)
	}
	return out, nil
}

// layer — слой по ty: известные 0–4, остальное (аудио 6, текст 5, …) — отказ.
func (s *sanitizer) layer(v *jvalue, path string) (*jvalue, error) {
	ty, ok := v.get("ty")
	if !ok || ty.k != jNum {
		return nil, unsafeAt(path+".ty", "неизвестный тип слоя")
	}
	f, err := strconv.ParseFloat(ty.s, 64)
	if err != nil || f < 0 || f > 4 || f != math.Trunc(f) {
		return nil, unsafeAt(path+".ty", "неизвестный тип слоя")
	}
	out, err := s.object(layerFields[int(f)], v, path)
	if err != nil {
		return nil, err
	}
	// обязательные поля видимого слоя LAC (ревью Task 8, m2): без ip/op lottie-web не рисует слой (NaN в
	// checkLayerLimits), без ks — нет трансформа
	for _, k := range []string{"ip", "op", "ks"} {
		if _, ok := out.get(k); !ok {
			return nil, corruptAt(path+"."+k, "обязательное поле слоя")
		}
	}
	// совместимость с плеерами (allow.go, layerPlayerCompat): st обязателен, hasMask — по выходу, не по входу
	if _, ok := out.get("st"); !ok {
		out.add("st", &jvalue{k: jNum, s: "0"})
	}
	// td boolean — 1/0: lottie-web проверяет истинность, а Flutter читает td числом (ревью R1)
	if i := indexOf(out.keys, "td"); i >= 0 && out.vals[i].k == jBool {
		lit := "0"
		if out.vals[i].b {
			lit = "1"
		}
		out.vals[i] = &jvalue{k: jNum, s: lit}
	}
	if m, ok := out.get("masksProperties"); ok && len(m.arr) > 0 {
		out.add("hasMask", &jvalue{k: jBool, b: true})
	}
	return out, nil
}

var imageMIMEs = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// imageAsset — картинка только вшитая (data:image/png|jpeg|webp;base64); u не переносится, e = 1 (иначе
// lottie-web приклеит к p путь анимации). Перекодирование — после обхода (Sanitize).
func (s *sanitizer) imageAsset(v *jvalue, path string) (*jvalue, error) {
	if u, ok := v.get("u"); ok && u.k != jNull {
		if u.k != jStr {
			return nil, corruptAt(path+".u", "не строка")
		}
		if u.s != "" && !strings.HasPrefix(u.s, "data:") {
			return nil, unsafeAt(path+".u", "внешняя ссылка")
		}
	}
	p, ok := v.get("p")
	if !ok || p.k != jStr || !strings.HasPrefix(p.s, "data:") {
		return nil, unsafeAt(path+".p", "не вшитая картинка")
	}
	header, data, _ := strings.Cut(p.s[len("data:"):], ",")
	mime, enc, _ := strings.Cut(strings.ToLower(header), ";")
	if !imageMIMEs[mime] || enc != "base64" {
		return nil, unsafeAt(path+".p", "тип не png|jpeg|webp;base64")
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, corruptAt(path+".p", "base64")
	}
	if len(raw) > s.lim.MaxImageBytes {
		return nil, unsafeAt(path+".p", fmt.Sprintf("картинка больше %d байт", s.lim.MaxImageBytes))
	}
	w, h, err := imageSize(mime, raw)
	if err != nil {
		return nil, corruptAt(path+".p", "заголовок картинки не "+mime)
	}
	if int64(w)*int64(h) > s.lim.MaxImagePixels {
		return nil, unsafeAt(path+".p", fmt.Sprintf("картинка больше %d пикселей", s.lim.MaxImagePixels))
	}
	// все картинки ассетов перекодирует воркер и загружает плеер (lottie-web — и неиспользуемые)
	if s.pixels += int64(w) * int64(h); s.pixels > s.lim.MaxTotalImagePixels {
		return nil, unsafeAt(path+".p", fmt.Sprintf("картинок больше %d пикселей в сумме", s.lim.MaxTotalImagePixels))
	}
	out, err := s.object(imageAssetFields, v, path)
	if err != nil {
		return nil, err
	}
	pv, _ := out.get("p")
	s.images = append(s.images, &pendingImage{path: path, raw: raw, p: pv})
	one := &jvalue{k: jNum, s: "1"}
	if i := indexOf(out.keys, "e"); i >= 0 {
		out.vals[i] = one
	} else {
		out.add("e", one)
	}
	return out, nil
}

func indexOf(keys []string, key string) int {
	for i, k := range keys {
		if k == key {
			return i
		}
	}
	return -1
}

// imageSize — размер по заголовку заявленного типа: содержимое другого типа — ошибка (декодирование — у img).
func imageSize(mime string, b []byte) (w, h int, err error) {
	switch mime {
	case "image/png":
		c, err := png.DecodeConfig(bytes.NewReader(b))
		return c.Width, c.Height, err
	case "image/jpeg":
		c, err := jpeg.DecodeConfig(bytes.NewReader(b))
		return c.Width, c.Height, err
	}
	return webpSize(b)
}

var errWebP = errors.New("lottie: не WebP")

// webpSize — размер из заголовка WebP (RIFF): VP8 (с потерями), VP8L (без потерь), VP8X (расширенный, холст).
func webpSize(b []byte) (w, h int, err error) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, errWebP
	}
	d := b[20:]
	switch string(b[12:16]) {
	case "VP8 ":
		if d[3] != 0x9d || d[4] != 0x01 || d[5] != 0x2a {
			return 0, 0, errWebP
		}
		return int(binary.LittleEndian.Uint16(d[6:]) & 0x3fff), int(binary.LittleEndian.Uint16(d[8:]) & 0x3fff), nil
	case "VP8L":
		if d[0] != 0x2f {
			return 0, 0, errWebP
		}
		bits := binary.LittleEndian.Uint32(d[1:])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8X":
		le24 := func(p []byte) int { return int(p[0]) | int(p[1])<<8 | int(p[2])<<16 }
		return le24(d[4:]) + 1, le24(d[7:]) + 1, nil
	}
	return 0, 0, errWebP
}

// ---- запись ----

// write — компактный JSON; строки — json.Marshal (экранирует <, >, &, U+2028/2029), числа — исходный литерал.
func write(buf *bytes.Buffer, v *jvalue) error {
	switch v.k {
	case jNull:
		buf.WriteString("null")
	case jBool:
		buf.WriteString(strconv.FormatBool(v.b))
	case jNum:
		buf.WriteString(v.s)
	case jStr:
		b, err := json.Marshal(v.s)
		if err != nil {
			return err
		}
		buf.Write(b)
	case jArr:
		buf.WriteByte('[')
		for i, e := range v.arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := write(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case jObj:
		buf.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := write(buf, v.vals[i]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	}
	return nil
}
