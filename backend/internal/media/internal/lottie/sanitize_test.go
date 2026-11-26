package lottie

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// baseLayer — минимальный слой форм.
const baseLayer = `{"ty":4,"ip":0,"op":60,"ks":{"o":{"a":0,"k":100}},"shapes":[]}`

// anim — анимация 2 с (30 к/с, 0…60) 100×100; extra начинается с запятой; без слоёв — baseLayer.
func anim(extra string, layers ...string) []byte {
	if len(layers) == 0 {
		layers = []string{baseLayer}
	}
	return []byte(`{"v":"5.7.4","fr":30,"ip":0,"op":60,"w":100,"h":100` + extra +
		`,"layers":[` + strings.Join(layers, ",") + `]}`)
}

// timing — анимация с заданными fr, ip, op.
func timing(fr, ip, op string) []byte {
	return []byte(`{"fr":` + fr + `,"ip":` + ip + `,"op":` + op + `,"w":100,"h":100,"layers":[` + baseLayer + `]}`)
}

func assets(list ...string) string { return `,"assets":[` + strings.Join(list, ",") + `]` }

func imageAsset(id, p string) string {
	return `{"id":"` + id + `","w":10,"h":10,"u":"","p":"` + p + `","e":1}`
}

func imageLayer(refID string) string {
	return `{"ty":2,"ip":0,"op":60,"refId":"` + refID + `","ks":{}}`
}

func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegWithEXIF — JPEG с сегментом APP1 Exif, в котором координаты GPS.
func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	const payload = "Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08GPSLatitude=55.75;GPSLongitude=37.61"
	out := append(append([]byte{}, b[:2]...), 0xFF, 0xE1, byte((len(payload)+2)>>8), byte(len(payload)+2))
	out = append(out, payload...)
	return append(out, b[2:]...)
}

// riff — контейнер WebP; поля размеров RIFF и чанка — нули: Sanitize их не читает, перекодирует ImageFunc.
func riff(chunk string, data []byte) []byte {
	return append([]byte("RIFF\x00\x00\x00\x00WEBP"+chunk+"\x00\x00\x00\x00"), data...)
}

// Заголовки WebP трёх видов (пиксели не нужны: размер берётся из заголовка).
func webpVP8L(w, h uint32) []byte {
	d := make([]byte, 10) // хвост — как у настоящего файла
	d[0] = 0x2f
	binary.LittleEndian.PutUint32(d[1:], (w-1)|(h-1)<<14)
	return riff("VP8L", d)
}

func webpVP8X(w, h uint32) []byte {
	d := make([]byte, 11) // холст: ширина−1 и высота−1 по 24 бита с d[4] и d[7]
	binary.LittleEndian.PutUint32(d[4:], w-1)
	binary.LittleEndian.PutUint32(d[7:], h-1)
	return riff("VP8X", d[:10])
}

func webpVP8(w, h uint16) []byte {
	d := []byte{0, 0, 0, 0x9d, 0x01, 0x2a, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(d[6:], w)
	binary.LittleEndian.PutUint16(d[8:], h)
	return riff("VP8 ", d)
}

// stubImg — подставное перекодирование: запоминает входы, отдаёт fakeWebP(номер вызова).
type stubImg struct {
	mu    sync.Mutex
	calls [][]byte
	err   error
}

func fakeWebP(n int) []byte {
	return []byte("RIFF\x00\x00\x00\x00WEBPVP8L-fake-" + strconv.Itoa(n))
}

func (s *stubImg) fn(_ context.Context, b []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]byte{}, b...))
	if s.err != nil {
		return nil, s.err
	}
	return fakeWebP(len(s.calls) - 1), nil
}

func (s *stubImg) n() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// nested — значение глубины depth (массивы), всего с корневым объектом — depth+1.
func nested(depth int) string { return strings.Repeat("[", depth) + strings.Repeat("]", depth) }

// padTo — дополняет документ пробелами до n байт.
func padTo(b []byte, n int) []byte { return append(b, bytes.Repeat([]byte(" "), n-len(b))...) }

func lim(edit func(*Limits)) Limits {
	l := DefaultLimits
	edit(&l)
	return l
}

func TestSanitizeRejects(t *testing.T) {
	t.Parallel()
	png10 := pngBytes(t, 10, 10)
	textLayer := `{"ty":5,"ip":0,"op":60,"ks":{},"t":{"d":{"k":[{"s":{"t":"GO","f":"Arial","s":20},"t":0}]}}}`
	glyph := func(ch string) string {
		return `{"ch":"` + ch + `","fFamily":"Arial","style":"Regular","size":20,"w":10,"data":{"shapes":[]}}`
	}
	cases := []struct {
		name string
		in   []byte
		lim  Limits
		want error
	}{
		{"expression", anim("", `{"ty":4,"ip":0,"op":60,"ks":{"o":{"a":0,"k":100,"x":"var $bm_rt = time;"}},"shapes":[]}`), DefaultLimits, ErrUnsafe},
		{"expression_in_path", anim("", `{"ty":4,"ip":0,"op":60,"ks":{},"shapes":[{"ty":"gr","it":[{"ty":"sh","ks":{"a":0,"k":{"c":false,"i":[],"o":[],"v":[]},"x":"loopOut()"}}]}]}`), DefaultLimits, ErrUnsafe},
		{"expression_grouped_position", anim("", `{"ty":4,"ip":0,"op":60,"ks":{"p":{"a":0,"k":[0,0],"x":"wiggle(1,2)"}},"shapes":[]}`), DefaultLimits, ErrUnsafe},
		{"expression_in_precomp_asset", anim(assets(`{"id":"pc","layers":[{"ty":3,"ip":0,"op":60,"ks":{"r":{"a":0,"k":0,"x":"time"}}}]}`)), DefaultLimits, ErrUnsafe},
		{"external_asset_p", anim(assets(imageAsset("img", "https://example.com/a.png"))), DefaultLimits, ErrUnsafe},
		{"external_asset_u", anim(assets(`{"id":"img","w":10,"h":10,"u":"https://cdn.example.com/","p":"` + dataURL("image/png", png10) + `","e":1}`)), DefaultLimits, ErrUnsafe},
		// схема из 5 символов, как у data:, с «правильным» типом дальше — режется проверкой префикса, а не разбором типа
		{"external_asset_other_scheme", anim(assets(imageAsset("img", strings.Replace(dataURL("image/png", png10), "data:", "blob:", 1)))), DefaultLimits, ErrUnsafe},
		{"asset_without_p", anim(assets(`{"id":"img","w":10,"h":10}`)), DefaultLimits, ErrUnsafe},
		{"font_fpath", anim(`,"fonts":{"list":[{"fName":"A","fFamily":"A","fStyle":"Regular","fPath":"https://example.com/a.ttf"}]}`), DefaultLimits, ErrUnsafe},
		{"font_origin", anim(`,"fonts":{"list":[{"fName":"A","fFamily":"A","fStyle":"Regular","origin":0}]}`), DefaultLimits, ErrUnsafe},
		{"audio_layer", anim("", `{"ty":6,"ip":0,"op":60,"refId":"snd"}`), DefaultLimits, ErrUnsafe},
		{"text_without_glyph", anim(`,"chars":[`+glyph("G")+`]`, textLayer), DefaultLimits, ErrUnsafe},
		{"text_with_glyphs", anim(`,"chars":[`+glyph("G")+`,`+glyph("O")+`]`, textLayer), DefaultLimits, ErrUnsafe},
		{"unknown_layer_ty", anim("", `{"ty":99,"ip":0,"op":60}`), DefaultLimits, ErrUnsafe},
		{"layer_ty_string", anim("", `{"ty":"4","ip":0,"op":60,"shapes":[]}`), DefaultLimits, ErrUnsafe},
		{"layer_without_ty", anim("", `{"ip":0,"op":60}`), DefaultLimits, ErrUnsafe},
		{"fr_zero", timing("0", "0", "60"), DefaultLimits, ErrUnsafe},
		// op < ip: длительность при fr = 0 — −Inf, проверку длительности проходит; режет только fr ≤ 0
		{"fr_zero_reversed", timing("0", "60", "0"), DefaultLimits, ErrUnsafe},
		{"fr_negative", timing("-30", "0", "60"), DefaultLimits, ErrUnsafe},
		{"duration_11s", timing("30", "0", "330"), DefaultLimits, ErrUnsafe},
		{"duration_overflow", timing("30", "-1e308", "1e308"), DefaultLimits, ErrUnsafe},
		{"depth_65", anim(`,"deep":` + nested(64)), DefaultLimits, ErrUnsafe},
		// незакрытая глубокая вложенность: отказ по глубине до конца чтения, а не «битый JSON»
		{"depth_streaming", []byte(`{"deep":` + strings.Repeat("[", 100_000)), DefaultLimits, ErrUnsafe},
		{"size_1mb_plus_1", padTo(anim(""), DefaultLimits.MaxBytes+1), DefaultLimits, ErrUnsafe},
		{"svg", anim(assets(imageAsset("img", dataURL("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))))), DefaultLimits, ErrUnsafe},
		{"gif", anim(assets(imageAsset("img", dataURL("image/gif", []byte("GIF89a"))))), DefaultLimits, ErrUnsafe},
		{"not_base64", anim(assets(imageAsset("img", "data:image/png,abc"))), DefaultLimits, ErrUnsafe},
		{"image_too_big", anim(assets(imageAsset("img", dataURL("image/png", png10)))), lim(func(l *Limits) { l.MaxImageBytes = len(png10) - 1 }), ErrUnsafe},
		{"image_pixels_png", anim(assets(imageAsset("img", dataURL("image/png", png10)))), lim(func(l *Limits) { l.MaxImagePixels = 99 }), ErrUnsafe},
		{"image_pixels_vp8l", anim(assets(imageAsset("img", dataURL("image/webp", webpVP8L(20, 20))))), lim(func(l *Limits) { l.MaxImagePixels = 399 }), ErrUnsafe},
		{"image_pixels_vp8x", anim(assets(imageAsset("img", dataURL("image/webp", webpVP8X(20, 20))))), lim(func(l *Limits) { l.MaxImagePixels = 399 }), ErrUnsafe},
		{"image_pixels_vp8", anim(assets(imageAsset("img", dataURL("image/webp", webpVP8(20, 20))))), lim(func(l *Limits) { l.MaxImagePixels = 399 }), ErrUnsafe},

		{"invalid_json", []byte(`{"fr":`), DefaultLimits, ErrCorrupt},
		{"trailing_data", append(anim(""), `{}`...), DefaultLimits, ErrCorrupt},
		{"top_level_array", []byte(`[]`), DefaultLimits, ErrCorrupt},
		{"missing_fr", []byte(`{"ip":0,"op":60,"w":100,"h":100,"layers":[]}`), DefaultLimits, ErrCorrupt},
		{"missing_layers", []byte(`{"fr":30,"ip":0,"op":60,"w":100,"h":100}`), DefaultLimits, ErrCorrupt},
		{"fr_string", timing(`"30"`, "0", "60"), DefaultLimits, ErrCorrupt},
		{"op_out_of_range", timing("30", "0", "1e400"), DefaultLimits, ErrCorrupt},
		{"layer_not_object", anim("", `1`), DefaultLimits, ErrCorrupt},
		{"solid_color_url", anim("", `{"ty":1,"ip":0,"op":60,"ks":{},"sw":10,"sh":10,"sc":"url(https://example.com/x)"}`), DefaultLimits, ErrCorrupt},
		{"split_position_string_x", anim("", `{"ty":4,"ip":0,"op":60,"ks":{"p":{"s":true,"x":"time","y":{"a":0,"k":0}}},"shapes":[]}`), DefaultLimits, ErrCorrupt},
		{"bad_base64", anim(assets(imageAsset("img", "data:image/png;base64,@@@@"))), DefaultLimits, ErrCorrupt},
		{"png_declared_jpeg_bytes", anim(assets(imageAsset("img", dataURL("image/png", jpegWithEXIF(t))))), DefaultLimits, ErrCorrupt},
		{"webp_declared_png_bytes", anim(assets(imageAsset("img", dataURL("image/webp", png10)))), DefaultLimits, ErrCorrupt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubImg{}
			out, err := Sanitize(context.Background(), c.in, c.lim, stub.fn)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if out != nil {
				t.Fatalf("out = %q, want nil", out)
			}
			if n := stub.n(); n != 0 {
				t.Fatalf("ImageFunc вызвана %d раз до отказа", n)
			}
		})
	}
}

// TestSanitizeAcceptsAtLimits — ровно на пределе — не отказ (граница «>», а не «≥»).
func TestSanitizeAcceptsAtLimits(t *testing.T) {
	t.Parallel()
	png10 := pngBytes(t, 10, 10)
	cases := []struct {
		name string
		in   []byte
		lim  Limits
	}{
		{"size_exactly_max", padTo(anim(""), DefaultLimits.MaxBytes), DefaultLimits},
		{"depth_64", anim(`,"deep":` + nested(63)), DefaultLimits},
		{"duration_10s", timing("60", "0", "600"), DefaultLimits},
		{"image_bytes_exactly_max", anim(assets(imageAsset("img", dataURL("image/png", png10)))), lim(func(l *Limits) { l.MaxImageBytes = len(png10) })},
		{"image_pixels_exactly_max", anim(assets(imageAsset("img", dataURL("image/png", png10)))), lim(func(l *Limits) { l.MaxImagePixels = 100 })},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubImg{}
			if _, err := Sanitize(context.Background(), c.in, c.lim, stub.fn); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSanitizeDropsUnknown(t *testing.T) {
	t.Parallel()
	// Дубль ключа: побеждает последний (как JSON.parse плеера); fr 0 первым не должен дать отказ.
	in := `{"v":"5.7.4","__proto__":{"polluted":true},"fr":0,"fr":30,"ip":0,"op":60,"w":512,"h":512,"ddd":0,` +
		`"meta":{"g":"AE","a":"author"},"slots":{"s1":{"p":{"a":0,"k":1,"x":"time"}}},"nm":"Мяч ⚽\u2028<b>",` +
		`"layers":[{"ddd":0,"ty":4,"nm":null,"ind":1,"ip":0,"op":60,"st":0,"bm":0,"foo":{"x":"evil"},` +
		`"ks":{"o":{"a":0,"k":100,"ix":11},` +
		`"r":{"a":1,"k":[{"t":0,"s":[0],"i":{"x":[0.833],"y":[0.833]},"o":{"x":[0.167],"y":[0.167]},"e":[360]},{"t":60,"s":[360]}]},` +
		`"p":{"s":true,"x":{"a":0,"k":0.1000000000000000055511151231257827},"y":{"a":0,"k":1e2},"z":{"a":0,"k":0}}},` +
		`"shapes":[{"ty":"rp","nm":"Repeater"},{"ty":"gr","it":[{"ty":"el","p":{"a":0,"k":[0,0]},"s":{"a":0,"k":[10,10]},"mn":"x"},` +
		`{"ty":"fl","c":{"a":0,"k":[1,0,0,1]},"o":{"a":0,"k":100},"r":1,"bm":0}]}],` +
		`"masksProperties":[{"inv":false,"mode":"a","pt":{"a":0,"k":{"c":true,"i":[[0,0]],"o":[[0,0]],"v":[[1,2]]}},"o":{"a":0,"k":100},"x":{"a":0,"k":0}}]}],` +
		`"markers":[{"cm":"start","tm":0,"dr":0,"extra":1}]}`
	// U+2028 и HTML-символы в строках экранируются: выход безопасно встраивать и в HTML.
	want := `{"v":"5.7.4","fr":30,"ip":0,"op":60,"w":512,"h":512,"nm":"Мяч ⚽\u2028\u003cb\u003e",` +
		`"layers":[{"ty":4,"ind":1,"ip":0,"op":60,` +
		`"ks":{"o":{"a":0,"k":100},` +
		`"r":{"a":1,"k":[{"t":0,"s":[0],"i":{"x":[0.833],"y":[0.833]},"o":{"x":[0.167],"y":[0.167]}},{"t":60,"s":[360]}]},` +
		`"p":{"s":true,"x":{"a":0,"k":0.1000000000000000055511151231257827},"y":{"a":0,"k":1e2}}},` +
		`"shapes":[{"ty":"gr","it":[{"ty":"el","p":{"a":0,"k":[0,0]},"s":{"a":0,"k":[10,10]}},` +
		`{"ty":"fl","c":{"a":0,"k":[1,0,0,1]},"o":{"a":0,"k":100},"r":1}]}],` +
		`"masksProperties":[{"mode":"a","pt":{"a":0,"k":{"c":true,"i":[[0,0]],"o":[[0,0]],"v":[[1,2]]}},"o":{"a":0,"k":100}}]}],` +
		`"markers":[{"cm":"start","tm":0,"dr":0}]}`
	stub := &stubImg{}
	out, err := Sanitize(context.Background(), []byte(in), DefaultLimits, stub.fn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(out) != want {
		t.Fatalf("выход:\n%s\nожидалось:\n%s", out, want)
	}
	again, err := Sanitize(context.Background(), out, DefaultLimits, stub.fn)
	if err != nil || !bytes.Equal(again, out) {
		t.Fatalf("повторный разбор не идемпотентен: err = %v\n%s", err, again)
	}
}

func TestSanitizeReencodesEmbedded(t *testing.T) {
	t.Parallel()
	jpg := jpegWithEXIF(t)
	png4 := pngBytes(t, 4, 4)
	webp := webpVP8L(10, 10)
	in := anim(assets(
		// u = "" и e = 0 у вшитой: на выходе u нет, e = 1 (иначе lottie-web приклеит путь к data:)
		`{"id":"a","w":8,"h":8,"u":"","p":"`+dataURL("image/jpeg", jpg)+`","e":0}`,
		`{"id":"b","w":4,"h":4,"p":"`+strings.Replace(dataURL("image/png", png4), "image/png", "IMAGE/PNG", 1)+`"}`,
		imageAsset("c", dataURL("image/webp", webp)),
	), imageLayer("a"), imageLayer("b"), imageLayer("c"))

	stub := &stubImg{}
	out, err := Sanitize(context.Background(), in, DefaultLimits, stub.fn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if stub.n() != 3 {
		t.Fatalf("ImageFunc вызвана %d раз, want 3 (по разу на картинку)", stub.n())
	}
	for i, want := range [][]byte{jpg, png4, webp} {
		if !bytes.Equal(stub.calls[i], want) {
			t.Fatalf("вызов %d: на вход не исходные байты картинки", i)
		}
	}
	var doc struct {
		Assets []map[string]json.RawMessage `json:"assets"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Assets) != 3 {
		t.Fatalf("ассетов %d", len(doc.Assets))
	}
	for i, a := range doc.Assets {
		var p string
		if err := json.Unmarshal(a["p"], &p); err != nil {
			t.Fatal(err)
		}
		if want := dataURL("image/webp", fakeWebP(i)); p != want {
			t.Fatalf("ассет %d: p = %q, want %q", i, p, want)
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(p, "data:image/webp;base64,"))
		if bytes.Contains(raw, []byte("Exif")) || bytes.Contains(raw, []byte("GPS")) {
			t.Fatalf("ассет %d: в выходе метаданные исходника", i)
		}
		if string(a["e"]) != "1" {
			t.Fatalf("ассет %d: e = %s, want 1", i, a["e"])
		}
		if _, ok := a["u"]; ok {
			t.Fatalf("ассет %d: u перенесён", i)
		}
	}
	if bytes.Contains(out, []byte(base64.StdEncoding.EncodeToString(jpg))) {
		t.Fatal("исходный JPEG попал в выход")
	}

	t.Run("img_error", func(t *testing.T) {
		t.Parallel()
		errStub := errors.New("imaging: битая картинка")
		stub := &stubImg{err: errStub}
		out, err := Sanitize(context.Background(), anim(assets(imageAsset("a", dataURL("image/png", png4)))), DefaultLimits, stub.fn)
		if !errors.Is(err, errStub) || out != nil {
			t.Fatalf("err = %v, out = %q; want обёрнутая ошибка ImageFunc и nil", err, out)
		}
	})
	t.Run("ctx_canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stub := &stubImg{}
		out, err := Sanitize(ctx, anim(assets(imageAsset("a", dataURL("image/png", png4)))), DefaultLimits, stub.fn)
		if !errors.Is(err, context.Canceled) || out != nil || stub.n() != 0 {
			t.Fatalf("err = %v, out = %q, вызовов %d; want context.Canceled без вызова", err, out, stub.n())
		}
	})
}

// TestSanitizeAcceptsLACExamples — примеры из репозитория спецификации LAC (github.com/lottie/lottie-spec,
// тег 1.0.1, docs/static/examples/{ellipse,path,trim_path}.json; лицензия примеров — MIT, спецификации —
// Community Specification License 1.0, файл License.md репозитория); переформатированы prettier.
func TestSanitizeAcceptsLACExamples(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"lac-ellipse.json", "lac-path.json", "lac-trim_path.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // имя — константа теста, каталог testdata
			if err != nil {
				t.Fatal(err)
			}
			stub := &stubImg{}
			out, err := Sanitize(context.Background(), in, DefaultLimits, stub.fn)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("выход — не JSON: %v", err)
			}
			if v, ok := doc["v"].(string); !ok || v == "" {
				t.Fatalf("v = %v", doc["v"])
			}
			for _, k := range []string{"fr", "ip", "op", "w", "h"} {
				if _, ok := doc[k].(float64); !ok {
					t.Fatalf("%s = %v", k, doc[k])
				}
			}
			if doc["fr"].(float64) <= 0 || doc["op"].(float64) <= doc["ip"].(float64) {
				t.Fatalf("fr/ip/op: %v %v %v", doc["fr"], doc["ip"], doc["op"])
			}
			layers, ok := doc["layers"].([]any)
			if !ok || len(layers) == 0 {
				t.Fatalf("layers = %v", doc["layers"])
			}
			l0 := layers[0].(map[string]any)
			if s, ok := l0["shapes"].([]any); !ok || len(s) == 0 || l0["ks"] == nil {
				t.Fatalf("слой форм потерял shapes/ks: %v", l0)
			}
			for _, k := range []string{"meta", "mn", "ddd"} {
				if bytes.Contains(out, []byte(`"`+k+`":`)) {
					t.Fatalf("поле %s вне схемы осталось в выходе", k)
				}
			}
			again, err := Sanitize(context.Background(), out, DefaultLimits, stub.fn)
			if err != nil || !bytes.Equal(again, out) {
				t.Fatalf("повторный разбор не идемпотентен: err = %v", err)
			}
		})
	}
}

// TestSanitizeKeepsAllowed — документ только из полей таблицы (все типы узлов, кадры, разделённая позиция,
// градиенты, штрихи) проходит без изменений: опечатка в allow.go выбросила бы поле.
func TestSanitizeKeepsAllowed(t *testing.T) {
	t.Parallel()
	in := `{"v":"5.7.4","ver":10001,"nm":"all","fr":30,"ip":0,"op":60,"w":100,"h":100,` +
		`"assets":[{"id":"pc","nm":"pre","layers":[{"nm":"n","hd":false,"ty":3,"ind":1,"parent":2,"ip":0,"op":60,` +
		`"ks":{"a":{"a":0,"k":[0,0]},"p":{"a":1,"k":[{"t":0,"s":[0,0],"i":{"x":0.5,"y":0.5},"o":{"x":0.5,"y":0.5},"ti":[0,0],"to":[1,1],"h":0},{"t":60,"s":[10,10]}]},` +
		`"r":{"a":0,"k":0},"s":{"a":0,"k":[100,100]},"o":{"a":0,"k":100},"sk":{"a":0,"k":0},"sa":{"a":0,"k":0}},"ao":0}]}],` +
		`"markers":[{"cm":"m","tm":0,"dr":10}],"layers":[` +
		`{"ty":0,"refId":"pc","w":100,"h":100,"sr":1,"st":0,"tm":{"a":0,"k":0},"ip":0,"op":60,"ks":{},"tt":1,"tp":2,` +
		`"masksProperties":[{"mode":"a","o":{"a":0,"k":100},"pt":{"a":1,"k":[{"t":0,"s":[{"c":true,"i":[[0,0]],"o":[[0,0]],"v":[[0,0]]}]},{"t":60}]}}]},` +
		`{"ty":1,"sw":10,"sh":10,"sc":"#ff00AA","ip":0,"op":60,"ks":{}},` +
		`{"ty":4,"ip":0,"op":60,"ks":{},"shapes":[{"ty":"gr","nm":"g","hd":false,"np":3,"it":[` +
		`{"ty":"rc","d":1,"p":{"a":0,"k":[0,0]},"s":{"a":0,"k":[10,10]},"r":{"a":0,"k":2}},` +
		`{"ty":"sr","p":{"a":0,"k":[0,0]},"or":{"a":0,"k":10},"os":{"a":0,"k":0},"r":{"a":0,"k":0},"pt":{"a":0,"k":5},"sy":1,"ir":{"a":0,"k":5},"is":{"a":0,"k":0}},` +
		`{"ty":"gf","o":{"a":0,"k":100},"r":1,"g":{"p":2,"k":{"a":0,"k":[0,1,0,0,1,0,0,1]}},"s":{"a":0,"k":[0,0]},"e":{"a":0,"k":[10,0]},"t":2,"h":{"a":0,"k":0},"a":{"a":0,"k":0}},` +
		`{"ty":"gs","o":{"a":0,"k":100},"lc":2,"lj":1,"ml":4,"ml2":{"a":0,"k":4},"w":{"a":0,"k":2},"d":[{"nm":"dash","n":"d","v":{"a":0,"k":3}},{"n":"g","v":{"a":0,"k":1}}],` +
		`"g":{"p":2,"k":{"a":1,"k":[{"t":0,"s":[0,1,1,1,1,0,0,0]},{"t":60,"s":[0,0,0,0,1,1,1,1]}]}},"s":{"a":0,"k":[0,0]},"e":{"a":0,"k":[10,0]},"t":1},` +
		`{"ty":"st","o":{"a":0,"k":100},"c":{"a":1,"k":[{"t":0,"s":[1,0,0,1]},{"t":60,"s":[0,0,1,1]}]},"w":{"a":0,"k":1},"lc":1,"lj":1},` +
		`{"ty":"tm","s":{"a":0,"k":0},"e":{"a":0,"k":100},"o":{"a":0,"k":0},"m":1},` +
		`{"ty":"tr","a":{"a":0,"k":[0,0]},"p":{"s":true,"x":{"a":0,"k":1},"y":{"a":0,"k":2}},"s":{"a":0,"k":[100,100]},"r":{"a":0,"k":0},"o":{"a":0,"k":100},"sk":{"a":0,"k":0},"sa":{"a":0,"k":0}}]},` +
		`{"ty":"el","p":{"a":0,"k":[0,0],"sid":"pos"},"s":{"a":0,"k":[5,5]}},` +
		`{"ty":"sh","ks":{"a":0,"k":{"c":false,"i":[[0,0],[1,1]],"o":[[0,0],[1,1]],"v":[[0,0],[5,5]]}}},` +
		`{"ty":"fl","c":{"a":0,"k":[1,1,1]},"r":2}]},` +
		`{"ty":2,"refId":"pc","ip":0,"op":60,"ks":{"p":{"s":false,"a":0,"k":[0,0]}}}]}`
	out, err := Sanitize(context.Background(), []byte(in), DefaultLimits, (&stubImg{}).fn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(out) != in {
		t.Fatalf("разрешённые поля изменены:\n%s\nожидалось:\n%s", out, in)
	}
}

// TestEveryNodeKindHandled — у каждого типа узла есть таблица или разбор (node не паникует).
func TestEveryNodeKindHandled(t *testing.T) {
	t.Parallel()
	for k := nAsset; k <= nKfBezier; k++ {
		s := &sanitizer{lim: DefaultLimits}
		_, _ = s.node(k, &jvalue{k: jObj}, "test") // отказ допустим (слой без ty), паника — нет
	}
}
