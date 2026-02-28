package handler

import (
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
	} else if perPage > 500 {
		perPage = 500
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

	writeJSON(w, http.StatusOK, series)
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
