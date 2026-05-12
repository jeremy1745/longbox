package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/service"
)

// MetadataWriterHandler handles API requests to write metadata into comic files.
type MetadataWriterHandler struct {
	writerSvc *service.MetadataWriterService
}

// NewMetadataWriterHandler creates a new handler for metadata writing operations.
func NewMetadataWriterHandler(writerSvc *service.MetadataWriterService) *MetadataWriterHandler {
	return &MetadataWriterHandler{writerSvc: writerSvc}
}

// WriteToFile writes ComicInfo.xml to a single file by file ID.
// POST /api/v1/files/{id}/write-metadata
func (h *MetadataWriterHandler) WriteToFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid file ID")
		return
	}

	result, err := h.writerSvc.WriteMetadataToFile(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WRITE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// WriteToIssue writes ComicInfo.xml to the file linked to the given issue.
// POST /api/v1/issues/{id}/write-metadata
func (h *MetadataWriterHandler) WriteToIssue(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid issue ID")
		return
	}

	result, err := h.writerSvc.WriteMetadataForIssue(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WRITE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// WriteToSeries writes ComicInfo.xml to all CBZ files in a series.
// POST /api/v1/series/{id}/write-metadata
func (h *MetadataWriterHandler) WriteToSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	results, err := h.writerSvc.WriteMetadataForSeries(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "WRITE_FAILED", err.Error())
		return
	}

	// Summarize
	var succeeded, failed, skipped int
	for _, r := range results {
		switch {
		case r.Skipped:
			skipped++
		case r.Success:
			succeeded++
		default:
			failed++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results":   results,
		"total":     len(results),
		"succeeded": succeeded,
		"failed":    failed,
		"skipped":   skipped,
	})
}

// WriteToFiles writes ComicInfo.xml to multiple files by ID.
// POST /api/v1/files/write-metadata (body: { "file_ids": [1,2,3] })
func (h *MetadataWriterHandler) WriteToFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileIDs []int64 `json:"file_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if len(body.FileIDs) == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_IDS", "file_ids array is required")
		return
	}

	results := make([]service.WriteResult, 0, len(body.FileIDs))
	for _, fid := range body.FileIDs {
		result, err := h.writerSvc.WriteMetadataToFile(fid)
		if err != nil {
			results = append(results, service.WriteResult{
				FileID:  fid,
				Success: false,
				Message: err.Error(),
			})
			continue
		}
		results = append(results, *result)
	}

	var succeeded, failed, skipped int
	for _, r := range results {
		switch {
		case r.Skipped:
			skipped++
		case r.Success:
			succeeded++
		default:
			failed++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results":   results,
		"total":     len(results),
		"succeeded": succeeded,
		"failed":    failed,
		"skipped":   skipped,
	})
}
