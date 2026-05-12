package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type StoryArcHandler struct {
	arcRepo  *repository.StoryArcRepo
	metaSvc  *service.MetadataService
	cvClient *comicvine.Client
}

func NewStoryArcHandler(
	arcRepo *repository.StoryArcRepo,
	metaSvc *service.MetadataService,
	cvClient *comicvine.Client,
) *StoryArcHandler {
	return &StoryArcHandler{
		arcRepo:  arcRepo,
		metaSvc:  metaSvc,
		cvClient: cvClient,
	}
}

// List returns all imported story arcs.
// GET /api/v1/story-arcs
func (h *StoryArcHandler) List(w http.ResponseWriter, r *http.Request) {
	arcs, err := h.arcRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}
	if arcs == nil {
		arcs = []model.StoryArc{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"story_arcs": arcs,
		"total":      len(arcs),
	})
}

// Get returns a story arc with its issues.
// GET /api/v1/story-arcs/{id}
func (h *StoryArcHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid story arc ID")
		return
	}

	arc, err := h.arcRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if arc == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "story arc not found")
		return
	}

	issues, err := h.arcRepo.ListIssues(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if issues == nil {
		issues = []model.StoryArcIssue{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"story_arc": arc,
		"issues":    issues,
	})
}

// Import imports a story arc from ComicVine.
// POST /api/v1/story-arcs/import
func (h *StoryArcHandler) Import(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ComicVineID int `json:"comicvine_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.ComicVineID == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_ID", "comicvine_id is required")
		return
	}

	// Check if already imported
	cvID := int64(req.ComicVineID)
	existing, _ := h.arcRepo.GetByComicVineID(cvID)
	if existing != nil {
		writeJSON(w, http.StatusOK, existing)
		return
	}

	// Fetch from ComicVine
	cvArc, err := h.cvClient.GetStoryArc(req.ComicVineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "CV_FETCH_FAILED", err.Error())
		return
	}

	// Create local arc
	arc := &model.StoryArc{
		Name:        cvArc.Name,
		ComicVineID: &cvID,
		Description: comicvine.StripHTML(cvArc.Description),
	}
	if err := h.arcRepo.Create(arc); err != nil {
		writeError(w, http.StatusInternalServerError, "CREATE_FAILED", err.Error())
		return
	}

	// For each issue in the arc, look up by CV ID and link
	linked := 0
	for i, issueRef := range cvArc.Issues {
		cvIssue, err := h.cvClient.GetIssue(issueRef.ID)
		if err != nil {
			slog.Warn("failed to fetch arc issue from CV", "cv_id", issueRef.ID, "error", err)
			continue
		}

		// Track the volume if not already tracked
		if cvIssue.Volume != nil {
			h.metaSvc.TrackFromComicVine(cvIssue.Volume.ID, nil)
		}

		// Find local issue by CV ID
		localIssue, err := h.metaSvc.FindIssueByCVID(int64(issueRef.ID))
		if err != nil || localIssue == nil {
			continue
		}

		seq := i + 1
		if err := h.arcRepo.AddIssue(arc.ID, localIssue.ID, &seq); err != nil {
			slog.Warn("failed to link arc issue", "arc_id", arc.ID, "issue_id", localIssue.ID, "error", err)
			continue
		}
		linked++
	}

	// Re-fetch with counts
	arc, _ = h.arcRepo.GetByID(arc.ID)
	writeJSON(w, http.StatusCreated, arc)
}

// Delete removes a story arc.
// DELETE /api/v1/story-arcs/{id}
func (h *StoryArcHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid story arc ID")
		return
	}

	if err := h.arcRepo.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Search searches ComicVine for story arcs.
// GET /api/v1/metadata/story-arcs/search
func (h *StoryArcHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "q parameter is required")
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	results, total, err := h.cvClient.SearchStoryArcs(query, page)
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
