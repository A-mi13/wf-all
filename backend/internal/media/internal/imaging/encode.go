package imaging

import (
	"bytes"
	"fmt"
	"image"

	"github.com/deepteams/webp"
)

// contentTypeWebP — тип всех вариантов (ключ хранилища — <object_id>/<вариант>.webp).
const contentTypeWebP = "image/webp"

// encodeWebP — вариант в WebP (R6: github.com/deepteams/webp; альфа сохраняется, вывод детерминирован). В выход
// идут только пиксели и, у P3, эталонный профиль: EXIF и XMP кодировщику не передаются никогда.
func encodeWebP(m image.Image, s Spec, icc []byte) ([]byte, error) {
	o := webp.DefaultOptions()
	o.Lossless = !s.Lossy
	o.Quality = float32(s.Quality)
	o.ICC = icc
	var buf bytes.Buffer
	if err := webp.Encode(&buf, m, o); err != nil {
		return nil, fmt.Errorf("imaging: кодирование варианта %q: %w", s.Name, err)
	}
	return buf.Bytes(), nil
}
