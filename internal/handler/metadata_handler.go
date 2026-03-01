package handler

import (
	"encoding/json"
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

	if err := h.metaSvc.MatchSeriesToVolume(seriesID, req.ComicVineID); err != nil {
		writeError(w, http.StatusInternalServerError, "MATCH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "matched"})
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

	if err := h.metaSvc.RefreshSeriesMetadata(seriesID); err != nil {
		writeError(w, http.StatusInternalServerError, "REFRESH_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}
