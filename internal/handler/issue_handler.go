package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/service"
)

type IssueHandler struct {
	issueRepo   *repository.IssueRepo
	librarySvc  *service.LibraryService
	organizeSvc *service.FileOrganizerService
}

func NewIssueHandler(issueRepo *repository.IssueRepo, librarySvc *service.LibraryService, organizeSvc *service.FileOrganizerService) *IssueHandler {
	return &IssueHandler{issueRepo: issueRepo, librarySvc: librarySvc, organizeSvc: organizeSvc}
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

func (h *IssueHandler) UpdateMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	var body struct {
		Title       *string `json:"title"`
		IssueNumber string  `json:"issue_number"`
		Writers     *string `json:"writers"`
		Artists     *string `json:"artists"`
		Rename      *bool   `json:"rename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	body.IssueNumber = strings.TrimSpace(body.IssueNumber)
	if body.IssueNumber == "" {
		writeError(w, http.StatusBadRequest, "INVALID_NUMBER", "issue_number is required")
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

	newTitle := issue.Title
	if body.Title != nil {
		newTitle = strings.TrimSpace(*body.Title)
	}
	sortNumber := scanner.SortNumber(body.IssueNumber)
	if err := h.issueRepo.UpdateTitleAndNumber(id, newTitle, body.IssueNumber, sortNumber); err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	if body.Writers != nil || body.Artists != nil {
		issue.Title = newTitle
		issue.IssueNumber = body.IssueNumber
		if body.Writers != nil {
			issue.Writers = strings.TrimSpace(*body.Writers)
		}
		if body.Artists != nil {
			issue.Artists = strings.TrimSpace(*body.Artists)
		}
		if err := h.issueRepo.Update(issue); err != nil {
			writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
			return
		}
	}

	// Optionally rename the on-disk file to match the new metadata. Default
	// is true — the user expects metadata edits to keep the file in sync.
	doRename := true
	if body.Rename != nil {
		doRename = *body.Rename
	}

	var renameWarning string
	var renamedPath string
	if doRename && h.organizeSvc != nil {
		_, newPath, changed, err := h.organizeSvc.RenameForIssue(id, h.librarySvc.GetLibraryDir())
		if err != nil {
			renameWarning = err.Error()
		} else if changed {
			renamedPath = newPath
		}
	}

	updated, err := h.issueRepo.GetByID(id)
	if err != nil || updated == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
		return
	}
	resp := map[string]any{"issue": updated}
	if renamedPath != "" {
		resp["renamed_to"] = renamedPath
	}
	if renameWarning != "" {
		resp["rename_warning"] = renameWarning
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *IssueHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	if err := h.librarySvc.DeleteIssue(id); err != nil {
		if errors.Is(err, service.ErrIssueNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "issue not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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
