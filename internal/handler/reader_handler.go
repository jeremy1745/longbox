package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type ReaderHandler struct {
	readerSvc *service.ReaderService
	fileRepo  *repository.FileRepo
	issueRepo *repository.IssueRepo
}

func NewReaderHandler(
	readerSvc *service.ReaderService,
	fileRepo *repository.FileRepo,
	issueRepo *repository.IssueRepo,
) *ReaderHandler {
	return &ReaderHandler{
		readerSvc: readerSvc,
		fileRepo:  fileRepo,
		issueRepo: issueRepo,
	}
}

// ListPages returns the page list for an issue's comic file.
// GET /api/v1/reader/{id}/pages
func (h *ReaderHandler) ListPages(w http.ResponseWriter, r *http.Request) {
	issueID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	issue, err := h.issueRepo.GetByID(issueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if issue == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "issue not found")
		return
	}
	if issue.FileID == nil {
		writeError(w, http.StatusNotFound, "NO_FILE", "issue has no associated file")
		return
	}

	file, err := h.fileRepo.GetByID(*issue.FileID)
	if err != nil || file == nil {
		writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "comic file not found")
		return
	}

	pages, err := h.readerSvc.ListPages(file.FilePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issue_id":       issueID,
		"file_id":        file.ID,
		"page_count":     len(pages),
		"pages":          pages,
		"last_read_page": issue.LastReadPage,
	})
}

// ServePage serves a single page image from an issue's comic file.
// GET /api/v1/reader/{id}/pages/{page}
func (h *ReaderHandler) ServePage(w http.ResponseWriter, r *http.Request) {
	issueID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	pageIndex, err := strconv.Atoi(chi.URLParam(r, "page"))
	if err != nil || pageIndex < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_PAGE", "invalid page number")
		return
	}

	issue, err := h.issueRepo.GetByID(issueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if issue == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "issue not found")
		return
	}
	if issue.FileID == nil {
		writeError(w, http.StatusNotFound, "NO_FILE", "issue has no associated file")
		return
	}

	file, err := h.fileRepo.GetByID(*issue.FileID)
	if err != nil || file == nil {
		writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "comic file not found")
		return
	}

	rc, mimeType, err := h.readerSvc.ExtractPage(file.FilePath, pageIndex)
	if err != nil {
		writeError(w, http.StatusNotFound, "PAGE_NOT_FOUND", err.Error())
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	io.Copy(w, rc)
}

// UpdateProgress updates the last-read page and auto-updates read status.
// PUT /api/v1/reader/{id}/progress
// Body: {"page": 5}
func (h *ReaderHandler) UpdateProgress(w http.ResponseWriter, r *http.Request) {
	issueID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	var body struct {
		Page int `json:"page"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	issue, err := h.issueRepo.GetByID(issueID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if issue == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "issue not found")
		return
	}

	// Validate page bounds
	if body.Page < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_PAGE", "page must be non-negative")
		return
	}

	// Update last_read_page
	if err := h.issueRepo.UpdateLastReadPage(issueID, body.Page); err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	// Auto-transition from "unread" to "reading"
	if issue.ReadStatus == "unread" {
		issue.ReadStatus = "reading"
		if err := h.issueRepo.Update(issue); err != nil {
			writeError(w, http.StatusInternalServerError, "STATUS_UPDATE_FAILED", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"last_read_page": body.Page,
		"read_status":    issue.ReadStatus,
	})
}
