package handler

import (
	"net/http"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
)

type MylarMetadataHandler struct {
	seriesRepo *repository.SeriesRepo
	sched      *scheduler.Scheduler
}

func NewMylarMetadataHandler(
	seriesRepo *repository.SeriesRepo,
	sched *scheduler.Scheduler,
) *MylarMetadataHandler {
	return &MylarMetadataHandler{
		seriesRepo: seriesRepo,
		sched:      sched,
	}
}

// WriteAll triggers a background job to write cvinfo and poster.jpg for all matched series.
func (h *MylarMetadataHandler) WriteAll(w http.ResponseWriter, r *http.Request) {
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

	job, err := h.sched.Submit(model.JobTypeMylarMetadata)
	if err != nil {
		writeError(w, http.StatusConflict, "JOB_SUBMIT_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":       job.ID,
		"total_series": len(series),
		"message":      "Writing cvinfo and poster.jpg to series folders",
	})
}
