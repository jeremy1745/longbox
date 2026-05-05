package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type BacklogHandler struct {
	backlogRepo *repository.BacklogRepo
	backlogSvc  *service.BacklogService
}

func NewBacklogHandler(backlogRepo *repository.BacklogRepo, backlogSvc *service.BacklogService) *BacklogHandler {
	return &BacklogHandler{
		backlogRepo: backlogRepo,
		backlogSvc:  backlogSvc,
	}
}

func (h *BacklogHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}

	runs, total, err := h.backlogRepo.ListRuns(page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "BACKLOG_LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    runs,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *BacklogHandler) CreateRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SeriesID        int64 `json:"series_id"`
		IncludeVariants *bool `json:"include_variants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if body.SeriesID == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_SERIES_ID", "series_id is required")
		return
	}

	run, err := h.backlogSvc.CreateRun(body.SeriesID, body.IncludeVariants)
	if err != nil {
		if errors.Is(err, service.ErrSeriesNotFound) {
			writeError(w, http.StatusNotFound, "SERIES_NOT_FOUND", "series not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "BACKLOG_CREATE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, run)
}

func (h *BacklogHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	runIDStr := r.URL.Query().Get("run_id")
	if runIDStr == "" {
		writeError(w, http.StatusBadRequest, "MISSING_RUN_ID", "run_id is required")
		return
	}
	runID, err := strconv.ParseInt(runIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_RUN_ID", "invalid run_id")
		return
	}

	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))

	items, total, err := h.backlogRepo.ListItems(runID, status, page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ITEM_LIST_FAILED", err.Error())
		return
	}

	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *BacklogHandler) PauseRun(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid run ID")
		return
	}

	run, err := h.backlogSvc.PauseRun(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PAUSE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, run)
}

func (h *BacklogHandler) ResumeRun(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid run ID")
		return
	}

	run, err := h.backlogSvc.ResumeRun(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RESUME_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, run)
}

// RetryAllInRun resets every failed/errored item in a run back to pending.
// POST /api/v1/backlog/runs/{id}/retry-all
func (h *BacklogHandler) RetryAllInRun(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid run ID")
		return
	}

	count, run, err := h.backlogSvc.RetryAllInRun(runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RETRY_ALL_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"retried": count,
		"run":     run,
	})
}

func (h *BacklogHandler) RetryItem(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid item ID")
		return
	}

	item, err := h.backlogSvc.RetryItem(itemID)
	if err != nil {
		if errors.Is(err, service.ErrBacklogItemNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "RETRY_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}
