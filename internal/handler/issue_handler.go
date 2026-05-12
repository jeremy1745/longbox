package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
)

type IssueHandler struct {
	issueRepo *repository.IssueRepo
}

func NewIssueHandler(issueRepo *repository.IssueRepo) *IssueHandler {
	return &IssueHandler{issueRepo: issueRepo}
}

func (h *IssueHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	issue, err := h.issueRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if issue == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "issue not found")
		return
	}

	writeJSON(w, http.StatusOK, issue)
}

// UpdateSkipStatus sets the skip status of an issue.
// PUT /api/v1/issues/{id}/skip-status
func (h *IssueHandler) UpdateSkipStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	var body struct {
		SkipStatus *string `json:"skip_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	// Validate skip_status
	if body.SkipStatus != nil {
		switch *body.SkipStatus {
		case "skipped", "ignored":
			// valid
		default:
			writeError(w, http.StatusBadRequest, "INVALID_STATUS", "skip_status must be 'skipped', 'ignored', or null")
			return
		}
	}

	if err := h.issueRepo.SetSkipStatus(id, body.SkipStatus); err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	issue, err := h.issueRepo.GetByID(id)
	if err != nil || issue == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

func (h *IssueHandler) UpdateReadStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	var body struct {
		ReadStatus string `json:"read_status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	// Validate read_status
	switch body.ReadStatus {
	case "unread", "reading", "read":
		// valid
	default:
		writeError(w, http.StatusBadRequest, "INVALID_STATUS", "read_status must be 'unread', 'reading', or 'read'")
		return
	}

	issue, err := h.issueRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if issue == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "issue not found")
		return
	}

	issue.ReadStatus = body.ReadStatus
	if err := h.issueRepo.Update(issue); err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, issue)
}
