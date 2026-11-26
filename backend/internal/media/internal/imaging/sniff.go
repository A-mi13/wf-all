// Package imaging — конвейер картинок модуля media (спека §3.3 шаги 3–6, §12): тип по содержимому, защита от
// «бомб», EXIF-ориентация, профиль ICC и варианты WebP без метаданных. Чистая библиотека без базы и хранилища —
// её вызывает задача media.process. Корень модуля media не импортирует: свои типы.
//
// Декодеры вызываются явно по типу (image/jpeg, image/png, golang.org/x/image/webp), а не через image.Decode:
// реестр форматов общий для процесса, и github.com/deepteams/webp регистрирует в нём свой декодер WebP.
package imaging

import (
	"bytes"
	"errors"
)

// Kind — тип входа, определённый по содержимому (заявленный клиентом тип сюда не передаётся вовсе).
type Kind string

const (
	KindJPEG Kind = "jpeg"
	KindPNG  Kind = "png"
	KindWebP Kind = "webp"
)

// Header — заголовок входа. Width и Height — размеры после EXIF-ориентации (как картинку видит человек).
type Header struct {
	Kind          Kind
	Width, Height int
	Animated      bool
}

// Ошибки-отказы: вызывающий переводит их в коды отклонения объекта.
var (
	ErrUnsupported   = errors.New("imaging: unsupported type")
	ErrTooManyPixels = errors.New("imaging: too many pixels")
	ErrCorrupt       = errors.New("imaging: corrupt")
)

var (
	sigJPEG = []byte{0xFF, 0xD8, 0xFF}
	sigPNG  = []byte("\x89PNG\r\n\x1a\n")
)

// kindOf — тип по сигнатуре; неизвестное — "".
func kindOf(b []byte) Kind {
	switch {
	case bytes.HasPrefix(b, sigJPEG):
		return KindJPEG
	case bytes.HasPrefix(b, sigPNG):
		return KindPNG
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return KindWebP
	}
	return ""
}

// Sniff определяет тип по сигнатуре и читает заголовок без декодирования пикселей. Неизвестная сигнатура —
// ErrUnsupported; сигнатура известна, но заголовок не читается — ErrCorrupt.
func Sniff(b []byte) (Header, error) {
	in, err := inspect(b)
	if err != nil {
		return Header{}, err
	}
	return in.Header, nil
}
