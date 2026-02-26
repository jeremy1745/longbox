package handler

import (
	"encoding/json"
	"net/http"

	"github.com/jeremy/longbox/internal/service"
)

type OrganizeHandler struct {
	organizeSvc *service.FileOrganizerService
	librarySvc  *service.LibraryService
}

func NewOrganizeHandler(organizeSvc *service.FileOrganizerService, librarySvc *service.LibraryService) *OrganizeHandler {
	return &OrganizeHandler{
		organizeSvc: organizeSvc,
		librarySvc:  librarySvc,
	}
}

// GetTemplate returns the current naming template.
// GET /api/v1/library/organize/template
func (h *OrganizeHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	tmpl := h.organizeSvc.GetTemplate()
	writeJSON(w, http.StatusOK, map[string]any{
		"template": tmpl,
	})
}

// SetTemplate validates and saves a new naming template.
// PUT /api/v1/library/organize/template
func (h *OrganizeHandler) SetTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.Template == "" {
		writeError(w, http.StatusBadRequest, "MISSING_TEMPLATE", "template is required")
		return
	}

	if err := h.organizeSvc.SetTemplate(req.Template); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_TEMPLATE", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "updated",
		"template": req.Template,
	})
}

// Preview generates a dry-run of file renames using the saved template.
// POST /api/v1/library/organize/preview
func (h *OrganizeHandler) Preview(w http.ResponseWriter, r *http.Request) {
	previews, err := h.organizeSvc.Preview(h.librarySvc.GetLibraryDir(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PREVIEW_FAILED", err.Error())
		return
	}

	// Summarize counts
	var moves, skips, conflicts, unlinked int
	for _, p := range previews {
		switch p.Status {
		case "move":
			moves++
		case "skip":
			skips++
		case "conflict":
			conflicts++
		case "unlinked":
			unlinked++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"previews":  previews,
		"total":     len(previews),
		"moves":     moves,
		"skips":     skips,
		"conflicts": conflicts,
		"unlinked":  unlinked,
	})
}

// PreviewTemplate generates a dry-run using a custom template (for live preview).
// POST /api/v1/library/organize/preview-template
func (h *OrganizeHandler) PreviewTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.Template == "" {
		writeError(w, http.StatusBadRequest, "MISSING_TEMPLATE", "template is required")
		return
	}

	previews, err := h.organizeSvc.Preview(h.librarySvc.GetLibraryDir(), req.Template)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PREVIEW_FAILED", err.Error())
		return
	}

	// Only return first few previews that have "move" status for the live preview
	var samples []service.RenamePreview
	for _, p := range previews {
		if p.Status == "move" && len(samples) < 5 {
			samples = append(samples, p)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"samples": samples,
		"total":   len(previews),
	})
}

// Execute actually moves files according to the current template.
// POST /api/v1/library/organize/execute
func (h *OrganizeHandler) Execute(w http.ResponseWriter, r *http.Request) {
	result, err := h.organizeSvc.Execute(h.librarySvc.GetLibraryDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "EXECUTE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
