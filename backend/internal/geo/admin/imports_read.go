package admin

import (
	"context"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
)

// ListGeoImports — GET /v1/geo/imports: журнал импортов, новые сверху; «кто поставил» — из аудита постановки
// (R3). Модератору закрыт матрицей (geo.imports.read).
func (h *Handler) ListGeoImports(ctx context.Context, req oapi.ListGeoImportsRequestObject) (oapi.ListGeoImportsResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	p := geo.ImportQuery{JobID: req.Params.JobId, Limit: req.Params.Limit}
	if req.Params.Country != nil {
		p.Country = *req.Params.Country
	}
	if req.Params.Cursor != nil {
		p.Cursor = *req.Params.Cursor
	}
	pg, err := r.Imports(ctx, p)
	if err != nil {
		return nil, Problem(err)
	}
	body := oapi.GeoImportPage{Items: make([]oapi.GeoImport, 0, len(pg.Items)), NextCursor: next(pg.Next)}
	for _, it := range pg.Items {
		body.Items = append(body.Items, importOf(it))
	}
	return oapi.ListGeoImports200JSONResponse{Body: body,
		Headers: oapi.ListGeoImports200ResponseHeaders{CacheControl: cachePrivate}}, nil
}

func importOf(it geo.Import) oapi.GeoImport {
	return oapi.GeoImport{
		Id: it.ID, CountryCode: it.CountryCode, Status: oapi.GeoImportStatus(it.Status), StartedAt: it.StartedAt,
		FinishedAt: it.FinishedAt, StartedBy: it.StartedBy, StartedByLabel: it.StartedByLabel, RiverJobId: it.RiverJobID,
		PlacesUpserted: it.PlacesUpserted, PlacesRemoved: it.PlacesRemoved, PlacesMissing: it.PlacesMissing,
		NamesUpserted: it.NamesUpserted, Error: it.Error,
	}
}

// GetGeoImport — GET /v1/geo/imports/{importId}: строка журнала с отчётом сверки; нет — 404 geo.import_not_found.
func (h *Handler) GetGeoImport(ctx context.Context, req oapi.GetGeoImportRequestObject) (oapi.GetGeoImportResponseObject, error) {
	r, err := h.reader()
	if err != nil {
		return nil, err
	}
	locale, lang := negotiate(req.Params.AcceptLanguage)
	rep, err := r.Import(ctx, req.ImportId, locale)
	if err != nil {
		return nil, Problem(err)
	}
	it := importOf(rep.Import)
	body := oapi.GeoImportReport{
		Id: it.Id, CountryCode: it.CountryCode, Status: it.Status, StartedAt: it.StartedAt, FinishedAt: it.FinishedAt,
		StartedBy: it.StartedBy, StartedByLabel: it.StartedByLabel, RiverJobId: it.RiverJobId,
		PlacesUpserted: it.PlacesUpserted, PlacesRemoved: it.PlacesRemoved, PlacesMissing: it.PlacesMissing,
		NamesUpserted: it.NamesUpserted, Error: it.Error,
	}
	if rc := rep.Reconciled; rc != nil {
		out := &oapi.GeoReconcileReport{
			Linked: make([]oapi.GeoReconcileLinked, 0, len(rc.Linked)), Ambiguous: reconcileCities(rc.Ambiguous),
			Conflict: reconcileCities(rc.Conflict), NotFound: make([]oapi.GeoReconcileNotFound, 0, len(rc.NotFound)),
			Missing: rc.Missing, SkippedTimezone: rc.SkippedTimezone,
		}
		for _, l := range rc.Linked {
			out.Linked = append(out.Linked, oapi.GeoReconcileLinked{CityId: l.CityID, Slug: l.Slug, GeonameId: l.GeonameID})
		}
		for _, n := range rc.NotFound {
			out.NotFound = append(out.NotFound, oapi.GeoReconcileNotFound{CityId: n.CityID, Slug: n.Slug})
		}
		body.Reconciled = out
	}
	return oapi.GetGeoImport200JSONResponse{Body: body, Headers: oapi.GetGeoImport200ResponseHeaders{
		CacheControl: cachePrivate, Vary: varyLanguage, ContentLanguage: lang,
	}}, nil
}

func reconcileCities(list []geo.ReconcileCity) []oapi.GeoReconcileCity {
	out := make([]oapi.GeoReconcileCity, 0, len(list))
	for _, c := range list {
		rc := oapi.GeoReconcileCity{CityId: c.CityID, Slug: c.Slug, Candidates: make([]oapi.GeoReconcileCandidate, 0, len(c.Candidates))}
		for _, p := range c.Candidates {
			rc.Candidates = append(rc.Candidates, oapi.GeoReconcileCandidate{GeonameId: p.GeonameID, Name: p.Name,
				RegionName: p.RegionName, Population: p.Population})
		}
		out = append(out, rc)
	}
	return out
}
