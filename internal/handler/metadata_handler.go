package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
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

// MatchSeries matches a local series to a ComicVine volume OR a Metron series.
// POST /api/v1/series/{id}/match
// Body: {"comicvine_id": 12345}  OR  {"metron_id": 8477}  OR both
//
// Pre-flight conflict check: if ANOTHER local series already carries the
// requested external ID, returns 409 Conflict with a body the UI can
// use to prompt for a merge:
//
//	{
//	  "code": "MERGE_REQUIRED",
//	  "conflict_with": { "series_id": 28, "title": "Absolute Batman", "year": 2024 },
//	  "source": "comicvine" | "metron",
//	  "external_id": 160294
//	}
//
// The client then POSTs /series/{id}/merge-into/{conflict_series_id} to
// accept the merge.
func (h *MetadataHandler) MatchSeries(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	seriesID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	var req struct {
		ComicVineID int `json:"comicvine_id"`
		MetronID    int `json:"metron_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.ComicVineID <= 0 && req.MetronID <= 0 {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "comicvine_id or metron_id is required")
		return
	}

	// Conflict pre-flight: check each requested external ID against the
	// other tracked series. The UI gets one conflict per request; if both
	// IDs collide with different series, the first hit wins (rare in
	// practice).
	if req.ComicVineID > 0 {
		if existing, err := h.metaSvc.FindSeriesByExternalID("comicvine", int64(req.ComicVineID)); err == nil && existing != nil && existing.ID != seriesID {
			respondMergeConflict(w, "comicvine", int64(req.ComicVineID), existing)
			return
		}
	}
	if req.MetronID > 0 {
		if existing, err := h.metaSvc.FindSeriesByExternalID("metron", int64(req.MetronID)); err == nil && existing != nil && existing.ID != seriesID {
			respondMergeConflict(w, "metron", int64(req.MetronID), existing)
			return
		}
	}

	// Apply.
	if req.ComicVineID > 0 {
		if err := h.metaSvc.MatchSeriesToVolume(seriesID, req.ComicVineID); err != nil {
			writeError(w, http.StatusInternalServerError, "MATCH_FAILED", err.Error())
			return
		}
	}
	if req.MetronID > 0 {
		if err := h.metaSvc.MatchSeriesToMetron(r.Context(), seriesID, req.MetronID); err != nil {
			writeError(w, http.StatusInternalServerError, "MATCH_FAILED", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "matched",
		"comicvine_id": req.ComicVineID,
		"metron_id":    req.MetronID,
	})
}

// respondMergeConflict writes a 409 Conflict with the existing series
// info attached, so the UI can prompt the user to merge.
func respondMergeConflict(w http.ResponseWriter, source string, externalID int64, existing *model.Series) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	body := map[string]any{
		"code":    "MERGE_REQUIRED",
		"message": "another local series already carries this " + source + " id",
		"source":  source,
		"external_id": externalID,
		"conflict_with": map[string]any{
			"series_id": existing.ID,
			"title":     existing.Title,
			"year":      existing.Year,
			"tracked":   existing.Tracked,
		},
	}
	_ = json.NewEncoder(w).Encode(body)
}

// MergeSeries collapses one local series into another, repointing every
// child reference and deleting the source row. Caller is expected to have
// confirmed the merge after receiving a 409 MERGE_REQUIRED on /match.
//
// POST /api/v1/series/{id}/merge-into/{target_id}
func (h *MetadataHandler) MergeSeries(w http.ResponseWriter, r *http.Request) {
	sourceID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid source series ID")
		return
	}
	targetID, err := strconv.ParseInt(chi.URLParam(r, "target_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid target series ID")
		return
	}
	stats, err := h.metaSvc.MergeSeriesInto(sourceID, targetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MERGE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "merged",
		"source_series_id":    sourceID,
		"target_series_id":    targetID,
		"issues_relocated":    stats.IssuesRelocated,
		"issues_consolidated": stats.IssuesConsolidated,
		"files_repointed":     stats.FilesRepointed,
		"want_list_merged":    stats.WantListMerged,
		"story_arcs_merged":   stats.StoryArcsMerged,
		"backlog_repointed":   stats.BacklogRepointed,
		"note":                "Files on disk will be moved to the target folder on the next reorganize pass.",
	})
}

// EnrichFromMetron resolves the Metron record for a local series via
// cv_id (or its already-set metron_id) and pulls Metron's issue covers,
// description, and IDs into the local rows. Synchronous — one series
// usually finishes in 6-9 seconds (one /series/?cv_id call + one /issue/
// list + N issue writes).
//
// POST /api/v1/series/{id}/enrich-metron
func (h *MetadataHandler) EnrichFromMetron(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	seriesID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}
	result, err := h.metaSvc.EnrichSeriesFromMetron(r.Context(), seriesID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ENRICH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// EnrichAllFromMetron walks every tracked series and enriches it from
// Metron. Fire-and-forget — returns 202 immediately with a count, then
// runs the bulk pass in a background goroutine (the work runs at
// Metron's rate-limit pace ~3s per series, so a 174-series catalog
// takes ~9 min and would otherwise block the HTTP response).
//
// POST /api/v1/admin/enrich-all-from-metron
func (h *MetadataHandler) EnrichAllFromMetron(w http.ResponseWriter, r *http.Request) {
	if !h.metaSvc.HasMetron() {
		writeError(w, http.StatusBadRequest, "METRON_UNCONFIGURED", "metron credentials not configured")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := h.metaSvc.EnrichAllTrackedFromMetron(ctx, nil); err != nil {
			slog.Warn("bulk enrich-from-metron failed", "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "started",
		"message": "Metron enrich running in background; check logs / re-query to see progress",
	})
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
