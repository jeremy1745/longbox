package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/sabnzbd"
)

type DownloadClientHandler struct {
	dlClientRepo *repository.DownloadClientRepo
}

func NewDownloadClientHandler(dlClientRepo *repository.DownloadClientRepo) *DownloadClientHandler {
	return &DownloadClientHandler{dlClientRepo: dlClientRepo}
}

// List returns all configured download clients with API keys masked.
// GET /api/v1/download-clients
func (h *DownloadClientHandler) List(w http.ResponseWriter, r *http.Request) {
	clients, err := h.dlClientRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	for i := range clients {
		clients[i].APIKey = clients[i].MaskAPIKey()
	}

	if clients == nil {
		clients = []model.DownloadClient{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"download_clients": clients,
	})
}

// Create adds a new download client.
// POST /api/v1/download-clients
func (h *DownloadClientHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		URL      string `json:"url"`
		APIKey   string `json:"api_key"`
		Category string `json:"category"`
		Priority int    `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.Name == "" || req.URL == "" || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "name, url, and api_key are required")
		return
	}

	dcType := model.DownloadClientType(req.Type)
	if dcType == "" {
		dcType = model.DownloadClientTypeSABnzbd
	}
	category := req.Category
	if category == "" {
		category = "comics"
	}
	priority := req.Priority
	if priority == 0 {
		priority = 50
	}

	dc := &model.DownloadClient{
		Name:     req.Name,
		Type:     dcType,
		URL:      req.URL,
		APIKey:   req.APIKey,
		Category: category,
		Priority: priority,
		Enabled:  true,
	}

	if err := h.dlClientRepo.Create(dc); err != nil {
		writeError(w, http.StatusInternalServerError, "CREATE_FAILED", err.Error())
		return
	}

	dc.APIKey = dc.MaskAPIKey()
	writeJSON(w, http.StatusCreated, dc)
}

// Update modifies an existing download client.
// PUT /api/v1/download-clients/{id}
func (h *DownloadClientHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid download client ID")
		return
	}

	existing, err := h.dlClientRepo.GetByID(id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "download client not found")
		return
	}

	var req struct {
		Name     *string `json:"name"`
		Type     *string `json:"type"`
		URL      *string `json:"url"`
		APIKey   *string `json:"api_key"`
		Category *string `json:"category"`
		Priority *int    `json:"priority"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Type != nil {
		existing.Type = model.DownloadClientType(*req.Type)
	}
	if req.URL != nil {
		existing.URL = *req.URL
	}
	if req.APIKey != nil {
		existing.APIKey = *req.APIKey
	}
	if req.Category != nil {
		existing.Category = *req.Category
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := h.dlClientRepo.Update(existing); err != nil {
		writeError(w, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	existing.APIKey = existing.MaskAPIKey()
	writeJSON(w, http.StatusOK, existing)
}

// Delete removes a download client.
// DELETE /api/v1/download-clients/{id}
func (h *DownloadClientHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid download client ID")
		return
	}

	if err := h.dlClientRepo.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Test verifies connectivity to a download client.
// POST /api/v1/download-clients/{id}/test
func (h *DownloadClientHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid download client ID")
		return
	}

	dc, err := h.dlClientRepo.GetByID(id)
	if err != nil || dc == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "download client not found")
		return
	}

	client := sabnzbd.NewClient(dc.URL, dc.APIKey)
	version, err := client.TestConnection()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Connected to SABnzbd " + version,
		"version": version,
	})
}
