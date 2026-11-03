package admin

import (
	"context"

	"wf/backend/internal/geo"
	"wf/backend/internal/httpapi/admin/oapi"
	"wf/backend/internal/platform/auth"
)

// StartGeoImport — POST /v1/geo/imports: задача River geo.import под Idempotency-Key конвейера (одна транзакция с
// аудитом постановки, R3). Уже в очереди или идёт — 409 geo.import_in_progress с job_id задачи (R26).
func (h *Handler) StartGeoImport(ctx context.Context, req oapi.StartGeoImportRequestObject) (oapi.StartGeoImportResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	if req.Body == nil {
		return nil, errNoBody
	}
	job, err := a.StartImport(ctx, s, req.Body.Country, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.StartGeoImport202JSONResponse{JobId: job}, nil
}

// GetGeoImportJob — GET /v1/geo/imports/jobs/{jobId}: состояние задачи импорта (R7); не задача импорта — 404.
func (h *Handler) GetGeoImportJob(ctx context.Context, req oapi.GetGeoImportJobRequestObject) (oapi.GetGeoImportJobResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	job, err := a.ImportJob(ctx, s, req.JobId)
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.GetGeoImportJob200JSONResponse{Body: importJob(job),
		Headers: oapi.GetGeoImportJob200ResponseHeaders{CacheControl: cachePrivate}}, nil
}

// CancelGeoImportJob — POST /v1/geo/imports/jobs/{jobId}/cancel: отмена задачи «в очереди» (R22) под
// Idempotency-Key; уже отменённая — 200 без второго аудита (K6); выполняется или завершена — 409.
func (h *Handler) CancelGeoImportJob(ctx context.Context, req oapi.CancelGeoImportJobRequestObject) (oapi.CancelGeoImportJobResponseObject, error) {
	a, err := h.writer()
	if err != nil {
		return nil, err
	}
	s, ok := auth.StaffFrom(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	job, err := a.CancelImportJob(ctx, s, req.JobId, meta(ctx))
	if err != nil {
		return nil, Problem(err)
	}
	return oapi.CancelGeoImportJob200JSONResponse(importJob(job)), nil
}

func importJob(j geo.ImportJob) oapi.GeoImportJob {
	return oapi.GeoImportJob{JobId: j.JobID, State: oapi.GeoImportJobState(j.State), ImportId: j.ImportID,
		Attempt: j.Attempt, MaxAttempts: j.MaxAttempts, CreatedAt: j.CreatedAt}
}
