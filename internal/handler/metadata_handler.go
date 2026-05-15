package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/service"
)

type MetadataHandler struct {
	metaSvc *service.MetadataService
}

func NewMetadataHandler(metaSvc *service.MetadataService) *MetadataHandler {
	return &MetadataHandler{metaSvc: metaSvc}
}

// writeMatchConflict inspects err for any of the three match-conflict error
// types and, if found, writes a unified 409 MERGE_REQUIRED response and
// returns true. The payload is the same shape regardless of which conflict
// fired (external-ID already taken, or title+year collision) because the
// frontend resolves all of them the same way: merge requestedSeriesID into
// conflictingSeriesID via POST /series/{id}/merge-into/{dst_id}.
func writeMatchConflict(w http.ResponseWriter, requestedSeriesID int64, err error) bool {
	var (
		conflictID    int64
		conflictTitle string
		matched       bool
	)

	var cvConflict *service.CVMatchConflictError
	var metronConflict *service.MetronMatchConflictError
	var seriesConflict *service.SeriesMatchConflictError

	switch {
	case errors.As(err, &cvConflict):
		matched = true
		if cvConflict.ConflictingSeries != nil {
			conflictID = cvConflict.ConflictingSeries.ID
			conflictTitle = cvConflict.ConflictingSeries.Title
		}
	case errors.As(err, &metronConflict):
		matched = true
		if metronConflict.ConflictingSeries != nil {
			conflictID = metronConflict.ConflictingSeries.ID
			conflictTitle = metronConflict.ConflictingSeries.Title
		}
	case errors.As(err, &seriesConflict):
		matched = true
		if seriesConflict.ConflictingSeries != nil {
			conflictID = seriesConflict.ConflictingSeries.ID
			conflictTitle = seriesConflict.ConflictingSeries.Title
		}
	}

	if !matched {
		return false
	}

	body := map[string]any{
		"error": map[string]any{
			"code":    "MERGE_REQUIRED",
			"message": err.Error(),
		},
		"conflicting_series_id":    conflictID,
		"conflicting_series_title": conflictTitle,
	}
	// Only include requested_series_id when non-zero — a zero means no series
	// was created yet (e.g. conflict during WantTrack before any DB write), and
	// exposing 0 as a series ID would mislead the frontend into calling
	// POST /series/0/merge-into/{dst}.
	if requestedSeriesID != 0 {
		body["requested_series_id"] = requestedSeriesID
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(body)
	return true
}

// SearchVolumes searches ComicVine for volumes matching a query.
// GET /api/v1/metadata/search?q=batman&page=1
func (h *MetadataHandler) SearchVolumes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "query parameter 'q' is required")
		return
	}

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}

	results, total, err := h.metaSvc.SearchVolumes(query, page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEARCH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
		"total":   total,
		"page":    page,
	})
}

// GetVolume fetches detailed volume info from ComicVine.
// GET /api/v1/metadata/volume/{cvid}
func (h *MetadataHandler) GetVolume(w http.ResponseWriter, r *http.Request) {
	cvidStr := chi.URLParam(r, "cvid")
	cvid, err := strconv.Atoi(cvidStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid ComicVine ID")
		return
	}

	volume, err := h.metaSvc.GetVolume(cvid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "VOLUME_FETCH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, volume)
}

// MatchSeries matches a local series to a ComicVine volume.
// POST /api/v1/series/{id}/match
// Body: {"comicvine_id": 12345}
func (h *MetadataHandler) MatchSeries(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	seriesID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	var req struct {
		ComicVineID int `json:"comicvine_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.ComicVineID <= 0 {
		writeError(w, http.StatusBadRequest, "MISSING_CVID", "comicvine_id is required")
		return
	}

	if err := h.metaSvc.MatchSeriesToVolume(r.Context(), seriesID, req.ComicVineID); err != nil {
		if writeMatchConflict(w, seriesID, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "MATCH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "matched"})
}

// SearchMetron searches Metron for series matching the given query.
// GET /api/v1/metadata/metron/search?q=batman&page=1
func (h *MetadataHandler) SearchMetron(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "query parameter 'q' is required")
		return
	}
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	results, total, err := h.metaSvc.SearchMetron(r.Context(), query, page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "METRON_SEARCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"total":   total,
		"page":    page,
	})
}

// MatchSeriesToMetron matches a local series to a Metron series.
// POST /api/v1/series/{id}/match-metron
// Body: {"metron_id": 12345}
func (h *MetadataHandler) MatchSeriesToMetron(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	seriesID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}
	var req struct {
		MetronID int `json:"metron_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.MetronID <= 0 {
		writeError(w, http.StatusBadRequest, "MISSING_METRON_ID", "metron_id is required")
		return
	}
	if err := h.metaSvc.MatchSeriesToMetronVolume(r.Context(), seriesID, req.MetronID); err != nil {
		if writeMatchConflict(w, seriesID, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "METRON_MATCH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "matched"})
}

// RefreshSeriesFromMetron re-fetches Metron metadata for an already-matched series.
// POST /api/v1/series/{id}/refresh-metron
func (h *MetadataHandler) RefreshSeriesFromMetron(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	seriesID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}
	if err := h.metaSvc.RefreshSeriesFromMetron(r.Context(), seriesID); err != nil {
		writeError(w, http.StatusInternalServerError, "METRON_REFRESH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}

// GetVolumeIssues returns all issues for a ComicVine volume (read-only preview).
// GET /api/v1/metadata/volume/{cvid}/issues
func (h *MetadataHandler) GetVolumeIssues(w http.ResponseWriter, r *http.Request) {
	cvidStr := chi.URLParam(r, "cvid")
	cvid, err := strconv.Atoi(cvidStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid ComicVine ID")
		return
	}

	issues, err := h.metaSvc.GetVolumeIssuesPreview(cvid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ISSUES_FETCH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"issues": issues,
		"total":  len(issues),
	})
}

// RefreshSeries re-fetches metadata for an already-matched series.
// POST /api/v1/series/{id}/refresh
func (h *MetadataHandler) RefreshSeries(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	seriesID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	if err := h.metaSvc.RefreshSeriesMetadata(r.Context(), seriesID); err != nil {
		writeError(w, http.StatusInternalServerError, "REFRESH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}
