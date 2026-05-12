package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
)

type SeriesHandler struct {
	seriesRepo   *repository.SeriesRepo
	issueRepo    *repository.IssueRepo
	wantListRepo *repository.WantListRepo
}

func NewSeriesHandler(seriesRepo *repository.SeriesRepo, issueRepo *repository.IssueRepo, wantListRepo *repository.WantListRepo) *SeriesHandler {
	return &SeriesHandler{seriesRepo: seriesRepo, issueRepo: issueRepo, wantListRepo: wantListRepo}
}

func (h *SeriesHandler) List(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	sortBy := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")
	trackedOnly := r.URL.Query().Get("tracked") == "true"

	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	} else if perPage > 10000 {
		// The library page's browse view requests per_page=10000 in a single
		// shot so it can group client-side. The previous 500 cap silently
		// truncated the series list — on a 1651-row library that meant 1151
		// series simply didn't render in browse mode. 10000 is enough for any
		// reasonable single-user library; payload at this size stays under
		// 200 KB and the SQLite list query runs in tens of milliseconds.
		perPage = 10000
	}

	series, total, err := h.seriesRepo.List(page, perPage, sortBy, order, trackedOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"series":   series,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *SeriesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	series, err := h.seriesRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if series == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "series not found")
		return
	}

	// Load child (annual) series
	children, err := h.seriesRepo.GetChildSeries(id)
	if err == nil && len(children) > 0 {
		series.AnnualSeries = children
	}

	writeJSON(w, http.StatusOK, series)
}

// LinkAnnual links a series as an annual/special of the given parent.
// PUT /api/v1/series/{id}/link-annual
func (h *SeriesHandler) LinkAnnual(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	var body struct {
		ChildSeriesID int64 `json:"child_series_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if body.ChildSeriesID == id {
		writeError(w, http.StatusBadRequest, "SELF_LINK", "cannot link a series to itself")
		return
	}

	if err := h.seriesRepo.SetParentSeries(body.ChildSeriesID, &id); err != nil {
		writeError(w, http.StatusInternalServerError, "LINK_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "linked"})
}

// UnlinkAnnual removes the parent link from a child series.
// PUT /api/v1/series/{id}/unlink-annual
func (h *SeriesHandler) UnlinkAnnual(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	var body struct {
		ChildSeriesID int64 `json:"child_series_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if err := h.seriesRepo.SetParentSeries(body.ChildSeriesID, nil); err != nil {
		writeError(w, http.StatusInternalServerError, "UNLINK_FAILED", err.Error())
		return
	}

	_ = id // parent ID used for routing context
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

func (h *SeriesHandler) Track(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	if err := h.seriesRepo.SetTracked(id, true); err != nil {
		writeError(w, http.StatusInternalServerError, "TRACK_FAILED", err.Error())
		return
	}

	// Auto-add missing issues to want list
	if h.wantListRepo != nil {
		added, err := h.wantListRepo.AddMissingForSeries(id)
		if err != nil {
			// Log but don't fail — tracking itself succeeded
			writeJSON(w, http.StatusOK, map[string]any{"tracked": true, "want_list_error": err.Error()})
			return
		}
		series, _ := h.seriesRepo.GetByID(id)
		writeJSON(w, http.StatusOK, map[string]any{"tracked": true, "want_list_added": added, "series": series})
		return
	}

	series, _ := h.seriesRepo.GetByID(id)
	writeJSON(w, http.StatusOK, map[string]any{"tracked": true, "series": series})
}

func (h *SeriesHandler) Untrack(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	if err := h.seriesRepo.SetTracked(id, false); err != nil {
		writeError(w, http.StatusInternalServerError, "UNTRACK_FAILED", err.Error())
		return
	}

	// Remove want list entries for this series
	if h.wantListRepo != nil {
		h.wantListRepo.RemoveForSeries(id)
	}

	series, _ := h.seriesRepo.GetByID(id)
	writeJSON(w, http.StatusOK, map[string]any{"tracked": false, "series": series})
}

// BulkSetSkipStatus sets skip status for all matching issues in a series.
// PUT /api/v1/series/{id}/issues/skip-status
func (h *SeriesHandler) BulkSetSkipStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	var body struct {
		FromStatus *string `json:"from_status"`
		ToStatus   *string `json:"to_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	// Validate to_status
	if body.ToStatus != nil {
		switch *body.ToStatus {
		case "skipped", "ignored":
			// valid
		default:
			writeError(w, http.StatusBadRequest, "INVALID_STATUS", "to_status must be 'skipped', 'ignored', or null")
			return
		}
	}

	affected, err := h.issueRepo.BulkSetSkipStatus(id, body.FromStatus, body.ToStatus)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"affected": affected})
}

func (h *SeriesHandler) GetIssues(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	issues, err := h.issueRepo.ListBySeries(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issues": issues,
		"total":  len(issues),
	})
}
