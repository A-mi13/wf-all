package public

import (
	"context"

	"wf/backend/internal/httpapi/public/oapi"
)

// Server собирает публичный API: встраивает хендлеры модулей (internal/<модуль>/httpapi),
// операции платформы (тег platform) реализует сам. Компилятор ловит и нереализованную
// операцию, и реализованную двумя модулями (неоднозначный метод) — спека §8.2.
type Server struct{}

var _ oapi.StrictServerInterface = Server{}

func (Server) GetHealth(context.Context, oapi.GetHealthRequestObject) (oapi.GetHealthResponseObject, error) {
	return oapi.GetHealth200JSONResponse{Status: oapi.Ok}, nil
}
