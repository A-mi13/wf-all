package admin_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
	"wf/backend/internal/platform/perm"
)

// createGeoDistrict и updateGeoDistrict (Task 12) за полным конвейером: ответ по контракту, повтор с тем же
// Idempotency-Key, модератор своего города — можно, чужого — 403 конвейера; район чужого города по своему cityId —
// 404 сценария.
func TestGeoDistrictsHTTP(t *testing.T) {
	t.Parallel()
	mod := staffOf(perm.Moderator, auth.StageActive, false)
	mod.Label = "Модератор С."
	e := newGeoEnv(t, map[string]*auth.Staff{"admin": activator(), "mod": mod})
	mod.CityIDs = []uuid.UUID{e.stav}
	stavPath := "/v1/geo/cities/" + e.stav.String() + "/districts"
	mihPath := "/v1/geo/cities/" + e.mih.String() + "/districts"

	key := uuid.NewString()
	rec := e.send(t, http.MethodPost, "mod", stavPath, key, `{"name":" Промышленный "}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("создание: %d %s", rec.Code, rec.Body.String())
	}
	d := decode[oapi.GeoDistrict](t, rec)
	if d.Name != "Промышленный" || d.Version != 1 || d.ArchivedAt != nil {
		t.Fatalf("район: %+v", d)
	}
	again := e.send(t, http.MethodPost, "mod", stavPath, key, `{"name":" Промышленный "}`, "")
	if d2 := decode[oapi.GeoDistrict](t, again); again.Code != http.StatusOK || d2.Id != d.Id || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("повтор: %d %s", again.Code, again.Body.String())
	}
	wantProblem(t, e.send(t, http.MethodPost, "mod", stavPath, "", `{"name":"промышленный"}`, ""), http.StatusConflict, geo.CodeDistrictNameTaken)
	wantProblem(t, e.send(t, http.MethodPost, "admin", "/v1/geo/cities/"+uuid.NewString()+"/districts", "", `{"name":"Центр"}`, ""),
		http.StatusNotFound, geo.CodeCityNotFound)
	// модератор чужого города — 403 конвейера
	wantProblem(t, serve(e.h, call(http.MethodPost, mihPath, "mod", `{"name":"Центр"}`)), http.StatusForbidden, "auth.forbidden")

	// район Михайловска по пути Ставрополя — 404 (модератор Ставрополя проходит конвейер своим городом)
	rec = e.send(t, http.MethodPost, "admin", mihPath, "", `{"name":"Центр"}`, "")
	foreign := decode[oapi.GeoDistrict](t, rec)
	wantProblem(t, e.send(t, http.MethodPatch, "mod", stavPath+"/"+foreign.Id.String(), "", `{"archived":true}`, ""),
		http.StatusNotFound, geo.CodeDistrictNotFound)

	rec = e.send(t, http.MethodPatch, "mod", stavPath+"/"+d.Id.String(), "", `{"archived":true}`, "")
	if a := decode[oapi.GeoDistrict](t, rec); rec.Code != http.StatusOK || a.ArchivedAt == nil || a.Version != 2 {
		t.Fatalf("архив: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := e.owner.QueryRow(context.Background(), `SELECT count(*) FROM districts WHERE id = $1 AND archived_at IS NULL`, foreign.Id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("чужой район изменён: %d %v", n, err)
	}
}
