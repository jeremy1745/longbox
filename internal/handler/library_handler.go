package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

type LibraryHandler struct {
	librarySvc *service.LibraryService
	fileRepo   *repository.FileRepo
	seriesRepo *repository.SeriesRepo
	scheduler  *scheduler.Scheduler
}

func NewLibraryHandler(
	librarySvc *service.LibraryService,
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	sched *scheduler.Scheduler,
) *LibraryHandler {
	return &LibraryHandler{
		librarySvc: librarySvc,
		fileRepo:   fileRepo,
		seriesRepo: seriesRepo,
		scheduler:  sched,
	}
}

// Scan triggers a background library scan and returns the job immediately.
func (h *LibraryHandler) Scan(w http.ResponseWriter, r *http.Request) {
	job, err := h.scheduler.Submit(model.JobTypeScan)
	if err != nil {
		writeError(w, http.StatusConflict, "SCAN_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// ReattachOrphans walks every comic_files row with issue_id NULL, re-parses
// the filename / parent folder with the current parser, and links the row
// to an existing series + issue when one can be resolved. Synchronous —
// expected to finish in well under the request timeout for typical libraries
// (197 orphans on the live data, no file I/O per row).
//
// POST /api/v1/admin/reattach-orphans
func (h *LibraryHandler) ReattachOrphans(w http.ResponseWriter, r *http.Request) {
	result, err := h.librarySvc.ReattachOrphanFiles(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "REATTACH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
	slog.Info("reattach-orphans handler complete",
		"total", result.Total, "attached", result.Attached,
		"no_parse", result.SkippedNoSeriesParse,
		"no_series", result.SkippedNoSeriesMatch,
		"no_issue", result.SkippedNoIssueNumber,
		"errors", result.Errors,
	)
}

func (h *LibraryHandler) Stats(w http.ResponseWriter, r *http.Request) {
	_, totalFiles, err := h.fileRepo.List(1, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STATS_FAILED", err.Error())
		return
	}

	_, totalSeries, err := h.seriesRepo.List(1, 1, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STATS_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_files":  totalFiles,
		"total_series": totalSeries,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// writeInternalError logs the detailed error but returns a generic message to the client.
func writeInternalError(w http.ResponseWriter, code string, err error) {
	slog.Error("internal error", "code", code, "error", err)
	writeError(w, http.StatusInternalServerError, code, "an internal error occurred")
}
