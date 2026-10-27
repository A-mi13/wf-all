package apitest_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"wf/backend/internal/platform/httpx"
	"wf/backend/internal/platform/testkit/apitest"
)

type (
	fakeReq  struct{ ID int }
	fakeResp interface{ visit() }
	okResp   struct{}
)

func (okResp) visit() {}

var errStub = httpx.NewError(http.StatusNotImplemented, httpx.CodeNotImplemented)

type stubHandler struct{}

func (stubHandler) GetA(context.Context, fakeReq) (fakeResp, error) { return nil, errStub }
func (stubHandler) GetB(context.Context, fakeReq) (fakeResp, error) { return nil, errStub }

// Подсадка багов: метод ответил, вернул другую ошибку, другой код или статус.
type mixedHandler struct{}

func (mixedHandler) Answers(context.Context, fakeReq) (fakeResp, error) { return okResp{}, nil }
func (mixedHandler) Fails(context.Context, fakeReq) (fakeResp, error) {
	return nil, errors.New("сбой")
}
func (mixedHandler) WrongCode(context.Context, fakeReq) (fakeResp, error) {
	return nil, httpx.NewError(http.StatusNotImplemented, httpx.CodeInternal)
}
func (mixedHandler) WrongStatus(context.Context, fakeReq) (fakeResp, error) {
	return nil, httpx.NewError(http.StatusBadRequest, httpx.CodeNotImplemented)
}
func (mixedHandler) Stub(context.Context, fakeReq) (fakeResp, error) { return nil, errStub }
func (mixedHandler) NotAnOperation(int) int                          { return 0 }

func TestNotImplemented(t *testing.T) {
	n, bad := apitest.NotImplemented(stubHandler{})
	if n != 2 || len(bad) != 0 {
		t.Fatalf("заглушки: проверено %d, нарушения %v", n, bad)
	}
	n, bad = apitest.NotImplemented(mixedHandler{})
	if want := []string{"Answers", "Fails", "WrongCode", "WrongStatus"}; n != 5 || !slices.Equal(bad, want) {
		t.Fatalf("проверено %d, нарушения %v, want %v", n, bad, want)
	}
}
