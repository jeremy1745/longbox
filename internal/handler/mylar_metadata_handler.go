package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

type LongboxMetadataHandler struct {
	seriesRepo     *repository.SeriesRepo
	sched          *scheduler.Scheduler
	sidecarSvc     *service.LongboxMetadataService
	folderImageSvc *service.FolderImageService
}

func NewLongboxMetadataHandler(
	seriesRepo *repository.SeriesRepo,
	sched *scheduler.Scheduler,
	sidecarSvc *service.LongboxMetadataService,
	folderImageSvc *service.FolderImageService,
) *LongboxMetadataHandler {
	return &LongboxMetadataHandler{
		seriesRepo:     seriesRepo,
		sched:          sched,
		sidecarSvc:     sidecarSvc,
		folderImageSvc: folderImageSvc,
	}
}

// WriteAll triggers a background job to write LongBox-native sidecars for all matched series.
func (h *LongboxMetadataHandler) WriteAll(w http.ResponseWriter, r *http.Request) {
	series, err := h.seriesRepo.ListWithComicVineID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	if len(series) == 0 {
		writeError(w, http.StatusBadRequest, "NO_MATCHED_SERIES",
			"No series matched to ComicVine. Match series from the Library page first.")
		return
	}

	job, err := h.sched.Submit(model.JobTypeLongboxMetadata)
	if err != nil {
		writeError(w, http.StatusConflict, "JOB_SUBMIT_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":       job.ID,
		"total_series": len(series),
		"message":      "Writing LongBox sidecars (longbox-series.json + longbox-series.txt)",
	})
}

// WriteForSeries writes the sidecars for a single series synchronously and
// returns the outcome. POST /api/v1/series/{id}/write-longbox-metadata
func (h *LongboxMetadataHandler) WriteForSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	outcome, dir, err := h.sidecarSvc.WriteForSeries(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SIDECAR_WRITE_FAILED", err.Error())
		return
	}

	resp := map[string]any{
		"series_id": id,
		"folder":    dir,
		"outcome":   sidecarOutcomeToString(outcome),
	}
	writeJSON(w, http.StatusOK, resp)
}

// WriteAllFolderImages triggers a background job to drop folder.jpg and
// cover.jpg into every series folder. Force=true regenerates even when an
// existing file is byte-identical (UI button always sets force).
// POST /api/v1/library/write-folder-images
func (h *LongboxMetadataHandler) WriteAllFolderImages(w http.ResponseWriter, r *http.Request) {
	job, err := h.sched.Submit(model.JobTypeFolderImages)
	if err != nil {
		writeError(w, http.StatusConflict, "JOB_SUBMIT_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":  job.ID,
		"message": "Writing folder.jpg + cover.jpg to every series folder",
	})
}

// WriteFolderImageForSeries writes folder images for a single series synchronously.
// POST /api/v1/series/{id}/write-folder-image
func (h *LongboxMetadataHandler) WriteFolderImageForSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}
	outcome, dir, err := h.folderImageSvc.WriteForSeries(r.Context(), id, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FOLDER_IMAGE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"series_id": id,
		"folder":    dir,
		"outcome":   folderImageOutcomeToString(outcome),
	})
}

func folderImageOutcomeToString(o service.FolderImageOutcome) string {
	switch o {
	case service.FolderImageWritten:
		return "written"
	case service.FolderImageUnchanged:
		return "unchanged"
	case service.FolderImageSkippedNoSource:
		return "no_cover_source"
	case service.FolderImageSkippedNoFolder:
		return "no_folder"
	case service.FolderImageSkippedNoFiles:
		return "no_files"
	case service.FolderImageFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func sidecarOutcomeToString(o service.SidecarWriteOutcome) string {
	switch o {
	case service.SidecarWritten:
		return "written"
	case service.SidecarSkippedUnchanged:
		return "unchanged"
	case service.SidecarSkippedNoFiles:
		return "no_files"
	case service.SidecarSkippedNoCVMatch:
		return "no_cv_match"
	case service.SidecarSkippedNoFolder:
		return "no_folder"
	case service.SidecarFailed:
		return "failed"
	default:
		return "unknown"
	}
}
