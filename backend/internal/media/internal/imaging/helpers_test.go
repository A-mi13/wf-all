package imaging

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
	"unicode/utf16"

	"github.com/deepteams/webp"
	xwebp "golang.org/x/image/webp"
)

// Генераторы тестовых картинок: все входы собираются в тестах, файлов в testdata нет.

// Узкие преобразования генераторов: маска — явное усечение (размеры и цвета тестовых данных заведомо в диапазоне).
func u8(v int) uint8   { return uint8(v & 0xFF) }
func u16(v int) uint16 { return uint16(v & 0xFFFF) }
func u32(v int) uint32 { return uint32(v & 0xFFFFFFFF) }

// hashNoise — детерминированный псевдошум 0–255 без генератора случайных чисел.
func hashNoise(a, b int) int {
	h := a*73856093 ^ b*19349663 ^ 0x5bd1e995
	h ^= h >> 13
	h *= 0x5bd1e995
	h ^= h >> 15
	return h & 0xFF
}

// gradient — непрозрачная картинка с плавным градиентом (быстро кодируется).
func gradient(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			m.SetNRGBA(x, y, color.NRGBA{u8(x * 255 / w), u8(y * 255 / h), 128, 255})
		}
	}
	return m
}

// noisy — градиент с шумом: у JPEG длинные энтропийные данные (обрезка и полиглот попадают в тело).
func noisy(w, h int) *image.NRGBA {
	m := gradient(w, h)
	for i := 0; i < len(m.Pix); i += 4 {
		for c := range 3 {
			m.Pix[i+c] = u8(int(m.Pix[i+c])/2 + hashNoise(i, c)%128)
		}
	}
	return m
}

func encJPEG(t testing.TB, m image.Image, q int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encPNG(t testing.TB, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encWebP(t testing.TB, m image.Image, opts *webp.EncoderOptions) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := webp.Encode(&buf, m, opts); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegSeg — сегмент JPEG: маркер FF xx, длина (с собой), данные.
func jpegSeg(marker byte, payload []byte) []byte {
	s := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(s[2:], u16(len(payload)+2))
	return append(s, payload...)
}

// withJPEGSegs вставляет сегменты сразу после SOI.
func withJPEGSegs(jpg []byte, segs ...[]byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, jpg[2:]...)
}

// tiffIFD — TIFF (MM) с IFD0 из записей SHORT/LONG; ascii — значения ASCII-тегов (кладутся после IFD).
type tiffEntry struct {
	tag, typ uint16
	value    int    // SHORT — в старших 2 байтах поля значения, LONG — целиком
	ascii    string // непусто — тег ASCII со смещением на строку
}

func tiff(entries ...tiffEntry) []byte {
	be := binary.BigEndian
	b := []byte{'M', 'M', 0, 42, 0, 0, 0, 8}
	ifd := make([]byte, 2+12*len(entries)+4)
	be.PutUint16(ifd, u16(len(entries)))
	extra := []byte{}
	extraOff := 8 + len(ifd)
	for i, e := range entries {
		p := ifd[2+12*i:]
		be.PutUint16(p, e.tag)
		switch {
		case e.ascii != "":
			be.PutUint16(p[2:], 2)
			be.PutUint32(p[4:], u32(len(e.ascii)+1))
			be.PutUint32(p[8:], u32(extraOff+len(extra)))
			extra = append(extra, e.ascii...)
			extra = append(extra, 0)
		case e.typ == 3:
			be.PutUint16(p[2:], 3)
			be.PutUint32(p[4:], 1)
			be.PutUint16(p[8:], u16(e.value))
		default:
			be.PutUint16(p[2:], 4)
			be.PutUint32(p[4:], 1)
			be.PutUint32(p[8:], u32(e.value))
		}
	}
	return append(append(b, ifd...), extra...)
}

// exifApp1 — APP1 «Exif» с ориентацией o.
func exifApp1(o int) []byte {
	return jpegSeg(0xE1, append([]byte("Exif\x00\x00"), tiff(tiffEntry{tag: 0x0112, typ: 3, value: o})...))
}

// gpsExif — TIFF с ориентацией, ImageDescription и указателем на GPS IFD (строки-маркеры для поиска в выходе).
func gpsExif() []byte {
	return tiff(
		tiffEntry{tag: 0x010E, ascii: "GPS WF-SECRET-MARK description"},
		tiffEntry{tag: 0x0112, typ: 3, value: 1},
		tiffEntry{tag: 0x8825, typ: 4, value: 8}, // GPS IFD → тот же IFD0: важны байты, не смысл
		tiffEntry{tag: 0x0001, ascii: "N GPSLatitudeRef WF-SECRET-MARK"},
	)
}

const xmpNS = "http://ns.adobe.com/xap/1.0/"

func xmpPacket() []byte {
	return []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:Description xmlns:xmp="` + xmpNS +
		`" exif:GPSLatitude="55,45.0N">WF-SECRET-MARK</rdf:Description></x:xmpmeta>`)
}

// pngChunk — чанк PNG с CRC.
func pngChunk(typ string, data []byte) []byte {
	c := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(c, u32(len(data)))
	copy(c[4:], typ)
	c = append(c, data...)
	return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(c[4:]))
}

// withPNGChunks вставляет чанки сразу после IHDR (до IDAT).
func withPNGChunks(p []byte, chunks ...[]byte) []byte {
	const afterIHDR = 8 + 25
	out := append([]byte{}, p[:afterIHDR]...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, p[afterIHDR:]...)
}

// pngAfterIDAT вставляет чанки перед IEND (после IDAT).
func pngAfterIDAT(p []byte, chunks ...[]byte) []byte {
	iend := len(p) - 12
	out := append([]byte{}, p[:iend]...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, p[iend:]...)
}

func zlibBytes(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func iccpChunk(t testing.TB, profile []byte) []byte {
	return pngChunk("iCCP", append([]byte("ICC Profile\x00\x00"), zlibBytes(t, profile)...))
}

// jpegICC — профиль в APP2 «ICC_PROFILE», разрезанный на parts сегментов; reverse — сегменты в обратном порядке
// (склейка обязана идти по номеру, а не по порядку в файле).
func jpegICC(profile []byte, parts int, reverse bool) [][]byte {
	n := (len(profile) + parts - 1) / parts
	segs := make([][]byte, 0, parts)
	for i := range parts {
		end := min((i+1)*n, len(profile))
		payload := append([]byte("ICC_PROFILE\x00"), u8(i+1), u8(parts))
		segs = append(segs, jpegSeg(0xE2, append(payload, profile[i*n:end]...)))
	}
	if reverse {
		for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
			segs[i], segs[j] = segs[j], segs[i]
		}
	}
	return segs
}

// Генераторы «злых» JPEG (финальное ревью media, D-C2 и D-I1): все коэффициенты нулевые, поэтому файл крошечный,
// а память и время декодера — как у настоящего снимка тех же размеров и выборки.

// zeroJPEGHead — SOI, DQT (все единицы), APP14 Adobe у 4 компонент (без него Go не декодирует CMYK), SOF0/SOF2 w×h с
// факторами выборки hv (0x11, 0x22, …; идентификаторы компонент 1…n), DHT: DC '0' → 0x00, '1' → 0x01; AC '0' →
// 0xE0 (EOBRUN, 14 бит), '1' → 0x00 (EOB).
func zeroJPEGHead(w, h int, hv []byte, progressive bool) []byte {
	b := []byte{0xFF, 0xD8}
	q := make([]byte, 65)
	for i := 1; i < 65; i++ {
		q[i] = 1
	}
	b = append(b, jpegSeg(0xDB, q)...)
	if len(hv) == 4 {
		b = append(b, jpegSeg(0xEE, []byte("Adobe\x00\x64\x00\x00\x00\x00\x00"))...)
	}
	sof := []byte{8, u8(h >> 8), u8(h), u8(w >> 8), u8(w), u8(len(hv))}
	for c, f := range hv {
		sof = append(sof, u8(c+1), f, 0)
	}
	marker := byte(0xC0)
	if progressive {
		marker = 0xC2
	}
	b = append(b, jpegSeg(marker, sof)...)
	return append(b, jpegSeg(0xC4, []byte{
		0x00, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x00, 0x01,
		0x10, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xE0, 0x00,
	})...)
}

// mcuGrid — число MCU по осям и блоков в MCU (как считает image/jpeg: у одной компоненты MCU — один блок).
func mcuGrid(w, h int, hv []byte) (mxx, myy, perMCU int) {
	maxH, maxV := 1, 1
	if len(hv) > 1 {
		for _, f := range hv {
			maxH, maxV = max(maxH, int(f>>4)), max(maxV, int(f&0x0F))
			perMCU += int(f>>4) * int(f&0x0F)
		}
	} else {
		perMCU = 1
	}
	return (w + 8*maxH - 1) / (8 * maxH), (h + 8*maxV - 1) / (8 * maxV), perMCU
}

// zeroJPEG — базовый (один скан всех компонент: блок — «DC 0, EOB», 2 бита) или прогрессивный (по одному AC-скану на
// компоненту, нулевые коэффициенты — символами EOBRUN по 32767 блоков) JPEG w×h.
func zeroJPEG(w, h int, hv []byte, progressive bool) []byte {
	b := zeroJPEGHead(w, h, hv, progressive)
	mxx, myy, perMCU := mcuGrid(w, h, hv)
	if !progressive {
		sos := []byte{u8(len(hv))}
		for c := range hv {
			sos = append(sos, u8(c+1), 0x00)
		}
		b = append(b, jpegSeg(0xDA, append(sos, 0, 63, 0))...)
		bits := 2 * mxx * myy * perMCU
		data := bytes.Repeat([]byte{0x55}, bits/8) // «01» на блок
		if bits%8 != 0 {
			data = append(data, 0x5F) // остаток «01» и единицы до байта
		}
		return append(append(b, data...), 0xFF, 0xD9)
	}
	for c := range hv {
		b = append(b, jpegSeg(0xDA, []byte{1, u8(c + 1), 0x00, 1, 63, 0x00})...)
		b = append(b, eobRuns((mxx*myy*perMCU+32766)/32767)...)
	}
	return append(b, 0xFF, 0xD9)
}

// progScans — прогрессивный серый JPEG w×h с n AC-сканами, каждый — символы EOBRUN на все блоки (скан в десятки
// байт, а декодер обходит все блоки на каждом скане).
func progScans(w, h, n int) []byte {
	b := zeroJPEGHead(w, h, []byte{0x11}, true)
	runs := eobRuns((((w+7)/8)*((h+7)/8) + 32766) / 32767)
	for range n {
		b = append(b, jpegSeg(0xDA, []byte{1, 1, 0x00, 1, 63, 0x00})...)
		b = append(b, runs...)
	}
	return append(b, 0xFF, 0xD9)
}

// eobRuns — n символов EOBRUN 32767 (код '0' + 14 единиц) подряд, хвост байта — единицами, 0xFF — с 0x00.
func eobRuns(n int) []byte {
	var (
		out []byte
		acc byte
		nb  int
	)
	put := func(bit byte) {
		acc = acc<<1 | bit
		if nb++; nb == 8 {
			out = append(out, acc)
			if acc == 0xFF {
				out = append(out, 0x00)
			}
			acc, nb = 0, 0
		}
	}
	for range n {
		put(0)
		for range 14 {
			put(1)
		}
	}
	for nb != 0 {
		put(1)
	}
	return out
}

// mlucBomb — профиль с первичными sRGB и тегом desc (mluc): recs записей, каждая указывает на весь тег (до правки
// D-C1 разбор выделял recs × длина тега байт).
func mlucBomb(recs int) []byte {
	tagLen := 16 + recs*12
	desc := []byte("mluc\x00\x00\x00\x00")
	desc = binary.BigEndian.AppendUint32(desc, u32(recs))
	desc = binary.BigEndian.AppendUint32(desc, 12)
	for range recs {
		desc = append(desc, "enUS"...)
		desc = binary.BigEndian.AppendUint32(desc, u32(tagLen&^1))
		desc = binary.BigEndian.AppendUint32(desc, 0)
	}
	return iccWithTags(iccTag{"rXYZ", xyzTag(srgb[0])}, iccTag{"gXYZ", xyzTag(srgb[1])}, iccTag{"bXYZ", xyzTag(srgb[2])},
		iccTag{"desc", desc})
}

type iccTag struct {
	sig  string
	data []byte
}

// iccWithTags — минимальный профиль: заголовок 128 байт («acsp»), таблица тегов, данные подряд.
func iccWithTags(tags ...iccTag) []byte {
	head := make([]byte, 128)
	copy(head[36:], "acsp")
	table := binary.BigEndian.AppendUint32(nil, u32(len(tags)))
	off := 128 + 4 + 12*len(tags)
	var data []byte
	for _, tg := range tags {
		table = append(table, tg.sig...)
		table = binary.BigEndian.AppendUint32(table, u32(off+len(data)))
		table = binary.BigEndian.AppendUint32(table, u32(len(tg.data)))
		data = append(data, tg.data...)
	}
	p := append(append(head, table...), data...)
	binary.BigEndian.PutUint32(p, u32(len(p)))
	return p
}

// mlucDesc — тег desc (mluc) из записей: каждая — UTF-16BE своей строки, данные после таблицы записей.
func mlucDesc(texts ...string) []byte {
	desc := []byte("mluc\x00\x00\x00\x00")
	desc = binary.BigEndian.AppendUint32(desc, u32(len(texts)))
	desc = binary.BigEndian.AppendUint32(desc, 12)
	var data []byte
	off := 16 + 12*len(texts)
	for _, s := range texts {
		var u []byte
		for _, r := range utf16.Encode([]rune(s)) {
			u = binary.BigEndian.AppendUint16(u, r)
		}
		desc = append(desc, "enUS"...)
		desc = binary.BigEndian.AppendUint32(desc, u32(len(u)))
		desc = binary.BigEndian.AppendUint32(desc, u32(off+len(data)))
		data = append(data, u...)
	}
	return append(desc, data...)
}

// p3WithDesc — профиль с первичными Apple P3 и desc из записей mluc.
func p3WithDesc(texts ...string) []byte {
	return iccWithTags(iccTag{"rXYZ", xyzTag(appleP3[0])}, iccTag{"gXYZ", xyzTag(appleP3[1])},
		iccTag{"bXYZ", xyzTag(appleP3[2])}, iccTag{"desc", mlucDesc(texts...)})
}

// Первичные из отчёта спайка (spike.md §3), D50: эталон Compact ICC, профиль Apple «Display P3», sRGB.
var (
	refP3   = [3][3]float64{{0.51512, 0.24120, -0.00105}, {0.29198, 0.69225, 0.04189}, {0.15710, 0.06656, 0.78407}}
	appleP3 = [3][3]float64{{0.51512, 0.24120, -0.00105}, {0.29198, 0.69225, 0.04189}, {0.15710, 0.06657, 0.78407}}
	srgb    = [3][3]float64{{0.43607, 0.22249, 0.01392}, {0.38515, 0.71687, 0.09708}, {0.14307, 0.06061, 0.71410}}
)

// iccOpts — как собрать тестовый профиль: desc в mluc (v4) или textDescription (v2), уникальный хвост cprt.
type iccOpts struct {
	desc  string
	v2    bool
	prim  [3][3]float64
	noise []byte
}

func xyzTag(v [3]float64) []byte {
	b := []byte("XYZ \x00\x00\x00\x00")
	for _, f := range v {
		b = binary.BigEndian.AppendUint32(b, u32(int(math.Round(f*65536))))
	}
	return b
}

func buildICC(o iccOpts) []byte {
	var desc []byte
	if o.v2 {
		desc = append([]byte("desc\x00\x00\x00\x00"), 0, 0, 0, u8(len(o.desc)+1))
		desc = append(append(desc, o.desc...), 0)
	} else {
		u := utf16.Encode([]rune(o.desc))
		desc = []byte("mluc\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x0cenUS")
		desc = binary.BigEndian.AppendUint32(desc, u32(2*len(u)))
		desc = binary.BigEndian.AppendUint32(desc, 28)
		for _, r := range u {
			desc = binary.BigEndian.AppendUint16(desc, r)
		}
	}
	noise := o.noise
	if noise == nil {
		noise = make([]byte, 96)
		for i := range noise {
			noise[i] = u8(hashNoise(i, len(o.desc)))
		}
	}
	tags := []struct {
		sig  string
		data []byte
	}{
		{"desc", desc},
		{"rXYZ", xyzTag(o.prim[0])},
		{"gXYZ", xyzTag(o.prim[1])},
		{"bXYZ", xyzTag(o.prim[2])},
		{"cprt", append([]byte("text\x00\x00\x00\x00"), noise...)},
	}
	head := make([]byte, 128)
	copy(head[4:], "wfts")
	binary.BigEndian.PutUint32(head[8:], 0x04200000)
	copy(head[12:], "mntrRGB XYZ ")
	copy(head[36:], "acsp")
	table := binary.BigEndian.AppendUint32(nil, u32(len(tags)))
	off := 128 + 4 + 12*len(tags)
	var data []byte
	for _, tg := range tags {
		for (off+len(data))%4 != 0 {
			data = append(data, 0)
		}
		table = append(table, tg.sig...)
		table = binary.BigEndian.AppendUint32(table, u32(off+len(data)))
		table = binary.BigEndian.AppendUint32(table, u32(len(tg.data)))
		data = append(data, tg.data...)
	}
	p := append(append(head, table...), data...)
	binary.BigEndian.PutUint32(p, u32(len(p)))
	return p
}

// riffChunk — чанк выходного WebP.
type riffChunk struct {
	id   string
	data []byte
}

func riffChunks(t testing.TB, b []byte) []riffChunk {
	t.Helper()
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		t.Fatalf("выход не WebP: % x", b[:min(16, len(b))])
	}
	var out []riffChunk
	for p := 12; p+8 <= len(b); {
		n := int(binary.LittleEndian.Uint32(b[p+4:]))
		if p+8+n > len(b) {
			t.Fatalf("чанк %q выходит за файл", b[p:p+4])
		}
		out = append(out, riffChunk{string(b[p : p+4]), b[p+8 : p+8+n]})
		p += 8 + n + n%2
	}
	return out
}

func chunkData(cs []riffChunk, id string) []byte {
	for _, c := range cs {
		if c.id == id {
			return c.data
		}
	}
	return nil
}

func decodeOut(t testing.TB, b []byte) image.Image {
	t.Helper()
	m, err := xwebp.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("независимый декодер не читает выход: %v", err)
	}
	return m
}

// webpWithChunks — RIFF WEBP из готовых чанков (id + данные).
func webpWithChunks(chunks ...riffChunk) []byte {
	var body []byte
	for _, c := range chunks {
		body = append(body, c.id...)
		body = binary.LittleEndian.AppendUint32(body, u32(len(c.data)))
		body = append(body, c.data...)
		if len(c.data)%2 == 1 {
			body = append(body, 0)
		}
	}
	out := []byte("RIFF")
	out = binary.LittleEndian.AppendUint32(out, u32(4+len(body)))
	out = append(out, "WEBP"...)
	return append(out, body...)
}

func vp8x(flags byte, w, h int) []byte {
	b := []byte{flags, 0, 0, 0}
	b = append(b, u8(w-1), u8((w-1)>>8), u8((w-1)>>16))
	return append(b, u8(h-1), u8((h-1)>>8), u8((h-1)>>16))
}
