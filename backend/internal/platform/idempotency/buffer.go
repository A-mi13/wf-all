package idempotency

import (
	"bytes"
	"net/http"
)

// buffer — ответ хендлера целиком в памяти: решение «отдать, заменить на 409/500 или сохранить
// для повтора» принимается после того, как хендлер закончил.
type buffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBuffer() *buffer { return &buffer{header: http.Header{}} }

func (b *buffer) Header() http.Header { return b.header }

func (b *buffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *buffer) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	return b.body.Write(p)
}

func (b *buffer) code() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}

func (b *buffer) flushTo(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	w.WriteHeader(b.code())
	_, _ = w.Write(b.body.Bytes())
}
