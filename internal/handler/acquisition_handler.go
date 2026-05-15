package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

// AcquisitionHandler exposes the want+track acquisition flow and
// individual retry endpoints.
type AcquisitionHandler struct {
	acqSvc      *service.AcquisitionService
	wantListRepo *repository.WantListRepo
}

// NewAcquisitionHandler constructs an AcquisitionHandler.
func NewAcquisitionHandler(
	acqSvc *service.AcquisitionService,
	wantListRepo *repository.WantListRepo,
) *AcquisitionHandler {
	return &AcquisitionHandler{
		acqSvc:      acqSvc,
		wantListRepo: wantListRepo,
	}
}

// WantTrack triggers the full want+track acquisition flow for a series.
// POST /api/v1/pull-list/want-track
// Body: {"comicvine_id": int, "metron_id": int, "source_issue_id": int}
// At least one of comicvine_id or metron_id is required.
func (h *AcquisitionHandler) WantTrack(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ComicVineID    *int64 `json:"comicvine_id"`
		MetronID       *int64 `json:"metron_id"`
		SourceIssueID  *int64 `json:"source_issue_id"`
		LinkToSeriesID *int64 `json:"link_to_series_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.ComicVineID == nil && req.MetronID == nil {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "at least one of comicvine_id or metron_id is required")
		return
	}

	input := service.WantTrackInput{
		ComicVineID:    req.ComicVineID,
		MetronID:       req.MetronID,
		SourceIssueID:  req.SourceIssueID,
		LinkToSeriesID: req.LinkToSeriesID,
	}

	result, err := h.acqSvc.WantAndTrackSeries(r.Context(), input)
	if err != nil {
		// Pass seriesID=0 when no series was created yet (conflict fires before
		// the series row exists — the conflict error itself carries the
		// conflicting series ID, which writeMatchConflict surfaces in the body).
		if writeMatchConflict(w, 0, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "ACQUISITION_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// RetryProcurement re-dispatches a single failed want-list item through Prowlarr.
// POST /api/v1/wantlist/{id}/retry
// {id} is the want-list item ID (NOT the issue ID).
func (h *AcquisitionHandler) RetryProcurement(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	wantListID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid want-list item ID")
		return
	}

	// Resolve the want-list item to get the issue ID.
	item, err := h.wantListRepo.GetByID(wantListID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LOOKUP_FAILED", err.Error())
		return
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "want-list item not found")
		return
	}

	updated, err := h.acqSvc.RetryIssue(r.Context(), item.IssueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RETRY_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// ListWantlist lists want-list items, optionally filtered by procurement_status.
// GET /api/v1/wantlist?procurement_status=<status>&page=<n>&per_page=<n>
//
// Response envelope is consistent with WantListHandler.List:
//
//	{"items": [...], "total": N, "page": N, "per_page": N}
//
// When procurement_status is set the filtered list is returned without DB-level
// pagination (status-filtered sets are small), but the envelope shape is the
// same — page=1, per_page=total.
func (h *AcquisitionHandler) ListWantlist(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("procurement_status")
	if status != "" {
		items, err := h.wantListRepo.ListByProcurementStatus(status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
			return
		}
		if items == nil {
			items = []model.WantListItem{}
		}
		total := len(items)
		writeJSON(w, http.StatusOK, map[string]any{
			"items":    items,
			"total":    total,
			"page":     1,
			"per_page": total,
		})
		return
	}

	// No filter: paginated, matching WantListHandler.List clamp rules.
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 100
	} else if perPage > 500 {
		perPage = 500
	}

	items, total, err := h.wantListRepo.List(page, perPage, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}
	if items == nil {
		items = []model.WantListItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}
