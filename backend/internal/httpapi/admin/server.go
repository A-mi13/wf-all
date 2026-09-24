package admin

import "context"

// Server реализует маршруты API админки. Зависимости модулей добавляются полями.
type Server struct{}

var _ StrictServerInterface = Server{}

func (Server) GetHealth(context.Context, GetHealthRequestObject) (GetHealthResponseObject, error) {
	return GetHealth200JSONResponse{Status: Ok}, nil
}
