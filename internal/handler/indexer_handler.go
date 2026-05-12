package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/newznab"
	"github.com/jeremy/longbox/internal/repository"
)

type IndexerHandler struct {
	indexerRepo *repository.IndexerRepo
}

func NewIndexerHandler(indexerRepo *repository.IndexerRepo) *IndexerHandler {
	return &IndexerHandler{indexerRepo: indexerRepo}
}

// List returns all configured indexers with API keys masked.
// GET /api/v1/indexers
func (h *IndexerHandler) List(w http.ResponseWriter, r *http.Request) {
	indexers, err := h.indexerRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	// Mask API keys
	for i := range indexers {
		indexers[i].APIKey = indexers[i].MaskAPIKey()
	}

	if indexers == nil {
		indexers = []model.Indexer{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"indexers": indexers,
	})
}

// Create adds a new indexer.
// POST /api/v1/indexers
func (h *IndexerHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		URL        string `json:"url"`
		APIKey     string `json:"api_key"`
		Type       string `json:"type"`
		Priority   int    `json:"priority"`
		Categories string `json:"categories"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.Name == "" || req.URL == "" || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "name, url, and api_key are required")
		return
	}

	idxType := model.IndexerType(req.Type)
	if idxType == "" {
		idxType = model.IndexerTypeNewznab
	}
	categories := req.Categories
	if categories == "" {
		categories = "7030"
	}
	priority := req.Priority
	if priority == 0 {
		priority = 50
	}

	idx := &model.Indexer{
		Name:       req.Name,
		URL:        req.URL,
		APIKey:     req.APIKey,
		Type:       idxType,
		Priority:   priority,
		Enabled:    true,
		Categories: categories,
	}

	if err := h.indexerRepo.Create(idx); err != nil {
		writeError(w, http.StatusInternalServerError, "CREATE_FAILED", err.Error())
		return
	}

	idx.APIKey = idx.MaskAPIKey()
	writeJSON(w, http.StatusCreated, idx)
}

// Update modifies an existing indexer.
// PUT /api/v1/indexers/{id}
func (h *IndexerHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid indexer ID")
		return
	}

	existing, err := h.indexerRepo.GetByID(id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "indexer not found")
		return
	}

	var req struct {
		Name       *string `json:"name"`
		URL        *string `json:"url"`
		APIKey     *string `json:"api_key"`
		Type       *string `json:"type"`
		Priority   *int    `json:"priority"`
		Enabled    *bool   `json:"enabled"`
		Categories *string `json:"categories"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.URL != nil {
		existing.URL = *req.URL
	}
	if req.APIKey != nil {
		existing.APIKey = *req.APIKey
	}
	if req.Type != nil {
		existing.Type = model.IndexerType(*req.Type)
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Categories != nil {
		existing.Categories = *req.Categories
	}

	if err := h.indexerRepo.Update(existing); err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	existing.APIKey = existing.MaskAPIKey()
	writeJSON(w, http.StatusOK, existing)
}

// Delete removes an indexer.
// DELETE /api/v1/indexers/{id}
func (h *IndexerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid indexer ID")
		return
	}

	if err := h.indexerRepo.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Test verifies connectivity to an indexer.
// POST /api/v1/indexers/{id}/test
func (h *IndexerHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid indexer ID")
		return
	}

	idx, err := h.indexerRepo.GetByID(id)
	if err != nil || idx == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "indexer not found")
		return
	}

	isProwlarr := idx.Type == model.IndexerTypeProwlarr
	client := newznab.NewClient(idx.URL, idx.APIKey, isProwlarr)

	if err := client.TestConnection(); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Connection successful",
	})
}
