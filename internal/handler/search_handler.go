package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

type SearchHandler struct {
	searchSvc     *service.SearchService
	dlHistoryRepo *repository.DownloadHistoryRepo
	scheduler     *scheduler.Scheduler
}

func NewSearchHandler(
	searchSvc *service.SearchService,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	sched *scheduler.Scheduler,
) *SearchHandler {
	return &SearchHandler{
		searchSvc:     searchSvc,
		dlHistoryRepo: dlHistoryRepo,
		scheduler:     sched,
	}
}

// SearchIssue searches indexers for a specific issue.
// GET /api/v1/search/issue/{id}
func (h *SearchHandler) SearchIssue(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	results, err := h.searchSvc.SearchForIssue(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEARCH_FAILED", err.Error())
		return
	}

	if results == nil {
		results = []service.ScoredResult{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
		"total":   len(results),
	})
}

// SearchQuery searches indexers with a raw query string.
// GET /api/v1/search?q=Batman+45
func (h *SearchHandler) SearchQuery(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "q parameter is required")
		return
	}

	results, err := h.searchSvc.SearchQuery(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEARCH_FAILED", err.Error())
		return
	}

	if results == nil {
		results = []service.ScoredResult{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
		"total":   len(results),
	})
}

// Grab sends an NZB to the download client.
// POST /api/v1/search/grab
func (h *SearchHandler) Grab(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NZBURL    string `json:"nzb_url"`
		NZBName   string `json:"nzb_name"`
		IndexerID int64  `json:"indexer_id"`
		IssueID   *int64 `json:"issue_id"`
		Size      int64  `json:"size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.NZBURL == "" || req.NZBName == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "nzb_url and nzb_name are required")
		return
	}

	item, err := h.searchSvc.GrabResult(r.Context(), req.NZBURL, req.NZBName, req.Size, req.IndexerID, req.IssueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "GRAB_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// DownloadHistory lists recent download history.
// GET /api/v1/downloads
func (h *SearchHandler) DownloadHistory(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	} else if perPage > 500 {
		perPage = 500
	}

	items, total, err := h.dlHistoryRepo.List(page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	if items == nil {
		items = []model.DownloadHistoryItem{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

// TriggerPullListSearch manually triggers the weekly pull list search.
// POST /api/v1/search/pull-list
func (h *SearchHandler) TriggerPullListSearch(w http.ResponseWriter, r *http.Request) {
	job, err := h.scheduler.Submit(model.JobTypePullListSearch)
	if err != nil {
		writeError(w, http.StatusConflict, "JOB_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
